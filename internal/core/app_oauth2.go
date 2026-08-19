package core

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type oauth2TokenResponse struct {
	AccessToken  string
	IDToken      string
	RefreshToken string
	Values       map[string]interface{}
	CreatedAt    time.Time
	ExpiresAt    time.Time
}

func (response oauth2TokenResponse) expired(now time.Time) bool {
	return !response.ExpiresAt.IsZero() && !now.Before(response.ExpiresAt)
}

func oauth2TokenResponseFromStorage(storage oauth2TokenStorage) oauth2TokenResponse {
	values := map[string]interface{}{}
	for key, value := range storage.Values {
		values[key] = value
	}
	response := oauth2TokenResponse{
		AccessToken:  storage.AccessToken,
		IDToken:      storage.IDToken,
		RefreshToken: storage.RefreshToken,
		Values:       values,
	}
	if storage.CreatedAt > 0 {
		response.CreatedAt = time.UnixMilli(storage.CreatedAt)
	}
	if storage.ExpiresAt > 0 {
		response.ExpiresAt = time.UnixMilli(storage.ExpiresAt)
	}
	return response
}

func (a *App) fetchOAuth2Token(auth OAuth2Auth, vars map[string]string) (string, error) {
	token, _, err := a.fetchOAuth2TokenWithTimeline(auth, vars)
	return token, err
}

func (a *App) fetchOAuth2TokenWithTimeline(auth OAuth2Auth, vars map[string]string) (string, []TimelineItem, error) {
	cfg := interpolateOAuth2Auth(auth, vars)
	key := oauth2CacheKey(cfg)
	now := time.Now()
	a.oauth2Mu.Lock()
	if cached, ok := a.oauth2[key]; ok {
		if token := oauth2TokenValue(cached, cfg.TokenSource); token != "" && !cached.expired(now) {
			a.oauth2Mu.Unlock()
			return token, nil, nil
		}
		if cfg.AutoRefreshToken && cached.RefreshToken != "" {
			a.oauth2Mu.Unlock()
			refreshed, timelineEntry, err := requestOAuth2RefreshTokenWithTimeline(cfg, cached.RefreshToken)
			if err != nil {
				return "", optionalTimelineEntry(timelineEntry), err
			}
			a.oauth2Mu.Lock()
			a.oauth2[key] = refreshed
			a.oauth2Mu.Unlock()
			return oauth2TokenValue(refreshed, cfg.TokenSource), optionalTimelineEntry(timelineEntry), nil
		}
	}
	a.oauth2Mu.Unlock()

	if strings.TrimSpace(cfg.GrantType) == "authorization_code" {
		response, timelineEntries, err := a.requestOAuth2AuthorizationCodeTokenWithTimeline(cfg)
		if err != nil {
			return "", timelineEntries, err
		}
		a.oauth2Mu.Lock()
		a.oauth2[key] = response
		a.oauth2Mu.Unlock()
		return oauth2TokenValue(response, cfg.TokenSource), timelineEntries, nil
	}
	if strings.TrimSpace(cfg.GrantType) == "implicit" {
		response, timelineEntries, err := a.requestOAuth2ImplicitTokenWithTimeline(cfg)
		if err != nil {
			return "", timelineEntries, err
		}
		a.oauth2Mu.Lock()
		a.oauth2[key] = response
		a.oauth2Mu.Unlock()
		return oauth2TokenValue(response, cfg.TokenSource), timelineEntries, nil
	}

	response, timelineEntry, err := requestOAuth2TokenWithTimeline(cfg)
	if err != nil {
		return "", optionalTimelineEntry(timelineEntry), err
	}
	a.oauth2Mu.Lock()
	a.oauth2[key] = response
	a.oauth2Mu.Unlock()
	return oauth2TokenValue(response, cfg.TokenSource), optionalTimelineEntry(timelineEntry), nil
}

func fetchOAuth2Token(auth OAuth2Auth, vars map[string]string) (string, error) {
	cfg := interpolateOAuth2Auth(auth, vars)
	response, err := requestOAuth2Token(cfg)
	if err != nil {
		return "", err
	}
	return oauth2TokenValue(response, cfg.TokenSource), nil
}

func oauth2CacheKey(cfg OAuth2Auth) string {
	return firstNonEmpty(cfg.AccessTokenURL, cfg.AuthorizationURL) + "|" + firstNonEmpty(cfg.CredentialsID, "credentials")
}

func requestOAuth2Token(cfg OAuth2Auth) (oauth2TokenResponse, error) {
	response, _, err := requestOAuth2TokenWithGrantTimeline(cfg, strings.TrimSpace(cfg.GrantType), "")
	return response, err
}

func requestOAuth2TokenWithTimeline(cfg OAuth2Auth) (oauth2TokenResponse, *TimelineItem, error) {
	return requestOAuth2TokenWithGrantTimeline(cfg, strings.TrimSpace(cfg.GrantType), "")
}

func requestOAuth2RefreshTokenWithTimeline(cfg OAuth2Auth, refreshToken string) (oauth2TokenResponse, *TimelineItem, error) {
	response, timelineEntry, err := requestOAuth2TokenWithGrantTimeline(cfg, "refresh_token", refreshToken)
	if err != nil {
		return oauth2TokenResponse{}, timelineEntry, err
	}
	if response.RefreshToken == "" {
		response.RefreshToken = refreshToken
		if response.Values == nil {
			response.Values = map[string]interface{}{}
		}
		response.Values["refresh_token"] = refreshToken
	}
	return response, timelineEntry, nil
}

func requestOAuth2TokenWithGrantTimeline(cfg OAuth2Auth, grantType, refreshToken string) (oauth2TokenResponse, *TimelineItem, error) {
	if grantType == "" {
		return oauth2TokenResponse{}, nil, nil
	}
	if grantType != "client_credentials" && grantType != "password" && grantType != "refresh_token" {
		return oauth2TokenResponse{}, nil, fmt.Errorf("OAuth2 grant type %s requires browser support and is not implemented", grantType)
	}
	tokenURL := cfg.AccessTokenURL
	if grantType == "refresh_token" {
		tokenURL = firstNonEmpty(cfg.RefreshTokenURL, cfg.AccessTokenURL)
	}
	if strings.TrimSpace(tokenURL) == "" {
		return oauth2TokenResponse{}, nil, errors.New("OAuth2 access token URL is required")
	}
	if strings.TrimSpace(cfg.ClientID) == "" {
		return oauth2TokenResponse{}, nil, errors.New("OAuth2 client ID is required")
	}
	form := url.Values{}
	form.Set("grant_type", grantType)
	if cfg.Scope != "" {
		form.Set("scope", cfg.Scope)
	}
	if grantType == "refresh_token" {
		if refreshToken == "" {
			return oauth2TokenResponse{}, nil, errors.New("OAuth2 refresh token is required")
		}
		form.Set("refresh_token", refreshToken)
	}
	if grantType == "password" {
		if cfg.Username == "" {
			return oauth2TokenResponse{}, nil, errors.New("OAuth2 username is required for password grant")
		}
		if cfg.Password == "" {
			return oauth2TokenResponse{}, nil, errors.New("OAuth2 password is required for password grant")
		}
		form.Set("username", cfg.Username)
		form.Set("password", cfg.Password)
	}
	params := oauth2AdditionalParamsForGrant(cfg, grantType)
	params = append(params, legacyOAuth2AdditionalParams(cfg.AdditionalParams)...)
	return requestOAuth2TokenFormWithTimeline(cfg, tokenURL, form, params)
}

func requestOAuth2TokenFormWithTimeline(cfg OAuth2Auth, tokenURL string, form url.Values, params []OAuth2AdditionalParam) (oauth2TokenResponse, *TimelineItem, error) {
	tokenReq, err := http.NewRequest(http.MethodPost, tokenURL, nil)
	if err != nil {
		timelineEntry := oauth2TimelineItemFromRequest(tokenReq, nil, 0, err)
		return oauth2TokenResponse{}, &timelineEntry, err
	}
	tokenReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	tokenReq.Header.Set("Accept", "application/json")
	if err := applyOAuth2AdditionalParams(tokenReq, form, params); err != nil {
		return oauth2TokenResponse{}, nil, err
	}
	if cfg.CredentialsPlacement == "" {
		cfg.CredentialsPlacement = "basic_auth_header"
	}
	if cfg.CredentialsPlacement == "basic_auth_header" {
		tokenReq.SetBasicAuth(cfg.ClientID, cfg.ClientSecret)
	} else {
		form.Set("client_id", cfg.ClientID)
		if strings.TrimSpace(cfg.ClientSecret) != "" {
			form.Set("client_secret", cfg.ClientSecret)
		}
	}
	setRequestBodyString(tokenReq, form.Encode())
	timelineStart := time.Now()
	// US-017: shared client. Posture unchanged (verified TLS, environment
	// proxy) — an OAuth2 client-secret exchange must not inherit the user's
	// proxy or a "disable SSL verification" toggle.
	res, err := sharedCredentialHTTPClient().Do(tokenReq)
	duration := time.Since(timelineStart).Milliseconds()
	if err != nil {
		timelineEntry := oauth2TimelineItemFromRequest(tokenReq, nil, duration, err)
		return oauth2TokenResponse{}, &timelineEntry, err
	}
	defer func() { _ = res.Body.Close() }()
	body, readErr := io.ReadAll(res.Body)
	if readErr != nil {
		timelineEntry := oauth2TimelineItemFromRequest(tokenReq, res, duration, readErr)
		return oauth2TokenResponse{}, &timelineEntry, readErr
	}
	timelineEntry := oauth2TimelineItemFromRequest(tokenReq, res, duration, nil)
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		err := fmt.Errorf("OAuth2 token request failed with %s: %s", res.Status, strings.TrimSpace(string(body)))
		timelineEntry.Error = err.Error()
		timelineEntry.Message = fmt.Sprintf("%s %s -> %d", timelineEntry.Method, timelineEntry.URL, res.StatusCode)
		return oauth2TokenResponse{}, &timelineEntry, err
	}
	payload := map[string]interface{}{}
	if err := json.Unmarshal(body, &payload); err != nil {
		parseErr := fmt.Errorf("parse OAuth2 token response: %w", err)
		timelineEntry.Error = parseErr.Error()
		return oauth2TokenResponse{}, &timelineEntry, parseErr
	}
	response := parseOAuth2TokenResponse(payload, time.Now())
	if response.AccessToken == "" {
		err := errors.New("OAuth2 token response did not include access_token")
		timelineEntry.Error = err.Error()
		return oauth2TokenResponse{}, &timelineEntry, err
	}
	if oauth2TokenValue(response, cfg.TokenSource) == "" {
		err := fmt.Errorf("OAuth2 token response did not include %s", firstNonEmpty(cfg.TokenSource, "access_token"))
		timelineEntry.Error = err.Error()
		return oauth2TokenResponse{}, &timelineEntry, err
	}
	return response, &timelineEntry, nil
}

func oauth2CodeVerifier() (string, error) {
	data := make([]byte, 32)
	if _, err := rand.Read(data); err != nil {
		return "", fmt.Errorf("generate OAuth2 PKCE verifier: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func oauth2CodeChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func oauth2LoopbackHost(host string) bool {
	normalized := strings.Trim(strings.ToLower(host), "[]")
	return normalized == "localhost" || normalized == "127.0.0.1" || normalized == "::1"
}

func oauth2TokenPayloadFromValues(values url.Values) map[string]interface{} {
	payload := map[string]interface{}{}
	for key, allValues := range values {
		if strings.TrimSpace(key) == "" || len(allValues) == 0 {
			continue
		}
		payload[key] = allValues[0]
	}
	return payload
}

func optionalTimelineEntry(entry *TimelineItem) []TimelineItem {
	if entry == nil {
		return nil
	}
	return []TimelineItem{*entry}
}

func oauth2TimelineItemFromRequest(req *http.Request, res *http.Response, duration int64, requestErr error) TimelineItem {
	method := http.MethodPost
	targetURL := ""
	if req != nil {
		method = strings.ToUpper(firstNonEmpty(req.Method, http.MethodPost))
		if req.URL != nil {
			targetURL = req.URL.String()
		}
	}
	entry := TimelineItem{
		At:       time.Now(),
		Duration: duration,
		Method:   method,
		URL:      targetURL,
	}
	if res != nil {
		entry.Status = res.StatusCode
		entry.StatusText = cleanStatusText(res.StatusCode, res.Status)
	}
	if requestErr != nil {
		entry.Error = requestErr.Error()
		if entry.StatusText == "" {
			entry.StatusText = "Error"
		}
	}
	statusLabel := entry.StatusText
	if entry.Status > 0 {
		statusLabel = strconv.Itoa(entry.Status)
	}
	if strings.TrimSpace(statusLabel) == "" {
		statusLabel = "-"
	}
	entry.Message = strings.TrimSpace(fmt.Sprintf("%s %s -> %s", entry.Method, entry.URL, statusLabel))
	return entry
}

func oauth2AdditionalParamsForGrant(cfg OAuth2Auth, grantType string) []OAuth2AdditionalParam {
	if grantType == "refresh_token" {
		return cfg.RefreshAdditionalParams
	}
	return cfg.TokenAdditionalParams
}

func legacyOAuth2AdditionalParams(params []KeyValue) []OAuth2AdditionalParam {
	out := make([]OAuth2AdditionalParam, 0, len(params))
	for _, param := range params {
		out = append(out, OAuth2AdditionalParam{
			Name:        param.Name,
			Value:       param.Value,
			SendIn:      "body",
			Enabled:     param.Enabled,
			Secret:      param.Secret,
			Description: param.Description,
		})
	}
	return out
}

func applyOAuth2AdditionalParams(req *http.Request, form url.Values, params []OAuth2AdditionalParam) error {
	for _, param := range params {
		if !param.Enabled || strings.TrimSpace(param.Name) == "" {
			continue
		}
		switch normalizeOAuth2AdditionalPlacement(param.SendIn) {
		case "headers":
			req.Header.Set(param.Name, param.Value)
		case "queryparams":
			query := req.URL.Query()
			query.Add(param.Name, param.Value)
			req.URL.RawQuery = query.Encode()
		case "body":
			form.Set(param.Name, param.Value)
		default:
			return fmt.Errorf("unsupported OAuth2 additional parameter placement %s", param.SendIn)
		}
	}
	return nil
}

func parseOAuth2TokenResponse(payload map[string]interface{}, now time.Time) oauth2TokenResponse {
	values := map[string]interface{}{}
	for key, value := range payload {
		values[key] = value
	}
	values["created_at"] = now.UnixMilli()
	response := oauth2TokenResponse{
		AccessToken:  stringMapValue(payload, "access_token"),
		IDToken:      stringMapValue(payload, "id_token"),
		RefreshToken: stringMapValue(payload, "refresh_token"),
		Values:       values,
		CreatedAt:    now,
	}
	if expiresIn, ok := numberMapValue(payload, "expires_in"); ok {
		response.ExpiresAt = now.Add(time.Duration(expiresIn * float64(time.Second)))
		response.Values["expires_at"] = response.ExpiresAt.UnixMilli()
	}
	return response
}

func (response oauth2TokenResponse) credentialValues() map[string]interface{} {
	values := map[string]interface{}{}
	for key, value := range response.Values {
		values[key] = value
	}
	if response.AccessToken != "" {
		values["access_token"] = response.AccessToken
	}
	if response.IDToken != "" {
		values["id_token"] = response.IDToken
	}
	if response.RefreshToken != "" {
		values["refresh_token"] = response.RefreshToken
	}
	if !response.CreatedAt.IsZero() {
		values["created_at"] = response.CreatedAt.UnixMilli()
	}
	if !response.ExpiresAt.IsZero() {
		values["expires_at"] = response.ExpiresAt.UnixMilli()
	}
	return values
}

func oauth2CacheCredentialsID(cacheKey string) string {
	index := strings.LastIndex(cacheKey, "|")
	if index < 0 {
		return "credentials"
	}
	return firstNonEmpty(cacheKey[index+1:], "credentials")
}

func (a *App) resetOAuth2Credential(credentialsID string) error {
	credentialsID = strings.TrimSpace(credentialsID)
	if credentialsID == "" {
		return errors.New("credentialId must be a non-empty string")
	}
	a.oauth2Mu.Lock()
	defer a.oauth2Mu.Unlock()
	for cacheKey := range a.oauth2 {
		if oauth2CacheCredentialsID(cacheKey) == credentialsID {
			delete(a.oauth2, cacheKey)
		}
	}
	return nil
}

func oauth2TokenValue(response oauth2TokenResponse, source string) string {
	if source == "id_token" {
		return response.IDToken
	}
	return response.AccessToken
}

func stringMapValue(values map[string]interface{}, key string) string {
	if raw, ok := values[key]; ok {
		switch value := raw.(type) {
		case string:
			return value
		case fmt.Stringer:
			return value.String()
		default:
			return fmt.Sprint(value)
		}
	}
	return ""
}

func numberMapValue(values map[string]interface{}, key string) (float64, bool) {
	raw, ok := values[key]
	if !ok {
		return 0, false
	}
	switch value := raw.(type) {
	case float64:
		return value, true
	case float32:
		return float64(value), true
	case int:
		return float64(value), true
	case int64:
		return float64(value), true
	case json.Number:
		parsed, err := value.Float64()
		return parsed, err == nil
	case string:
		parsed, err := strconv.ParseFloat(value, 64)
		return parsed, err == nil
	default:
		return 0, false
	}
}

func interpolateOAuth2Auth(auth OAuth2Auth, vars map[string]string) OAuth2Auth {
	out := OAuth2Auth{
		GrantType:            interpolate(auth.GrantType, vars),
		CallbackURL:          interpolate(auth.CallbackURL, vars),
		AuthorizationURL:     interpolate(auth.AuthorizationURL, vars),
		AccessTokenURL:       interpolate(auth.AccessTokenURL, vars),
		RefreshTokenURL:      interpolate(auth.RefreshTokenURL, vars),
		Username:             interpolate(auth.Username, vars),
		Password:             interpolate(auth.Password, vars),
		ClientID:             interpolate(auth.ClientID, vars),
		ClientSecret:         interpolate(auth.ClientSecret, vars),
		Scope:                interpolate(auth.Scope, vars),
		State:                interpolate(auth.State, vars),
		PKCE:                 auth.PKCE,
		CredentialsPlacement: interpolate(auth.CredentialsPlacement, vars),
		CredentialsID:        interpolate(auth.CredentialsID, vars),
		TokenSource:          interpolate(auth.TokenSource, vars),
		TokenPlacement:       interpolate(auth.TokenPlacement, vars),
		TokenHeaderPrefix:    interpolate(auth.TokenHeaderPrefix, vars),
		TokenQueryKey:        interpolate(auth.TokenQueryKey, vars),
		AutoFetchToken:       auth.AutoFetchToken,
		AutoRefreshToken:     auth.AutoRefreshToken,
	}
	out.AuthorizationAdditionalParams = interpolateOAuth2AdditionalParams(auth.AuthorizationAdditionalParams, vars)
	out.TokenAdditionalParams = interpolateOAuth2AdditionalParams(auth.TokenAdditionalParams, vars)
	out.RefreshAdditionalParams = interpolateOAuth2AdditionalParams(auth.RefreshAdditionalParams, vars)
	for _, param := range auth.AdditionalParams {
		out.AdditionalParams = append(out.AdditionalParams, KeyValue{
			Name:        interpolate(param.Name, vars),
			Value:       interpolate(param.Value, vars),
			Enabled:     param.Enabled,
			Secret:      param.Secret,
			Description: param.Description,
		})
	}
	return out
}

func interpolateOAuth2AdditionalParams(params []OAuth2AdditionalParam, vars map[string]string) []OAuth2AdditionalParam {
	out := make([]OAuth2AdditionalParam, 0, len(params))
	for _, param := range params {
		out = append(out, OAuth2AdditionalParam{
			Name:        interpolate(param.Name, vars),
			Value:       interpolate(param.Value, vars),
			SendIn:      interpolate(param.SendIn, vars),
			Enabled:     param.Enabled,
			Secret:      param.Secret,
			Description: param.Description,
		})
	}
	return out
}

func applyOAuth2Token(req *http.Request, auth OAuth2Auth, token string, vars map[string]string) {
	placement := strings.TrimSpace(interpolate(auth.TokenPlacement, vars))
	if placement == "" {
		placement = "header"
	}
	if placement == "url" || placement == "query" {
		key := firstNonEmpty(interpolate(auth.TokenQueryKey, vars), "access_token")
		q := req.URL.Query()
		q.Set(key, token)
		req.URL.RawQuery = q.Encode()
		return
	}
	prefix := interpolate(auth.TokenHeaderPrefix, vars)
	if prefix == "" {
		prefix = "Bearer"
	}
	req.Header.Set("Authorization", strings.TrimSpace(prefix+" "+token))
}
