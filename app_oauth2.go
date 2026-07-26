package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
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

type oauth2CredentialsFile struct {
	Credentials []oauth2CredentialEntry `json:"credentials"`
}

type oauth2CredentialEntry struct {
	CacheKey string `json:"cacheKey"`
	Data     string `json:"data"`
}

type oauth2TokenStorage struct {
	AccessToken  string                 `json:"accessToken,omitempty"`
	IDToken      string                 `json:"idToken,omitempty"`
	RefreshToken string                 `json:"refreshToken,omitempty"`
	Values       map[string]interface{} `json:"values,omitempty"`
	CreatedAt    int64                  `json:"createdAt,omitempty"`
	ExpiresAt    int64                  `json:"expiresAt,omitempty"`
}

var oauth2CredentialsRemove = os.Remove

func (response oauth2TokenResponse) expired(now time.Time) bool {
	return !response.ExpiresAt.IsZero() && !now.Before(response.ExpiresAt)
}

func (a *App) oauth2CredentialsPath() string {
	return filepath.Join(a.dataDir, "oauth2.json")
}

func (a *App) loadOAuth2Credentials() error {
	path := a.oauth2CredentialsPath()
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return nil
	}
	var store oauth2CredentialsFile
	if err := json.Unmarshal(data, &store); err != nil {
		return fmt.Errorf("parse oauth2.json: %w", err)
	}
	next := map[string]oauth2TokenResponse{}
	for _, entry := range store.Credentials {
		cacheKey := strings.TrimSpace(entry.CacheKey)
		if cacheKey == "" || strings.TrimSpace(entry.Data) == "" {
			continue
		}
		response, err := decryptOAuth2TokenResponse(a.dataDir, entry.Data)
		if err != nil {
			return fmt.Errorf("decrypt OAuth2 credential %s: %w", cacheKey, err)
		}
		next[cacheKey] = response
	}
	a.oauth2Mu.Lock()
	a.oauth2 = next
	a.oauth2Baseline = cloneOAuth2TokenMap(next)
	a.oauth2Mu.Unlock()
	return nil
}

func (a *App) storeOAuth2Credentials() error {
	a.oauth2Mu.Lock()
	local := map[string]oauth2TokenResponse{}
	for key, value := range a.oauth2 {
		local[key] = value
	}
	baseline := cloneOAuth2TokenMap(a.oauth2Baseline)
	// US-013. This function runs on every state persist, and everything below
	// is expensive: it reads oauth2.json, DECRYPTS every stored credential,
	// re-encrypts every merged credential, and writes the file. None of that
	// can change the result when this App's own OAuth2 state has not moved
	// since the last successful store, because the merge is a function of
	// (baseline, local, disk) and a no-op delta leaves disk exactly as it is.
	//
	// So the gate is on (baseline, local) alone. Deliberately not on disk: a
	// credential another window obtained is already on disk, and refusing to
	// rewrite it is the correct outcome, not a lost update.
	fingerprint, fingerprintErr := oauth2TokenMapFingerprint(baseline, local)
	unchanged := fingerprintErr == nil && fingerprint != "" && fingerprint == a.oauth2Fingerprint
	a.oauth2Mu.Unlock()
	if unchanged {
		return nil
	}
	disk := map[string]oauth2TokenResponse{}
	if data, err := os.ReadFile(a.oauth2CredentialsPath()); err == nil {
		var stored oauth2CredentialsFile
		if err := json.Unmarshal(data, &stored); err != nil {
			return fmt.Errorf("parse oauth2.json: %w", err)
		}
		for _, entry := range stored.Credentials {
			value, err := decryptOAuth2TokenResponse(a.dataDir, entry.Data)
			if err != nil {
				return fmt.Errorf("decrypt OAuth2 credential %s: %w", entry.CacheKey, err)
			}
			disk[entry.CacheKey] = value
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	merged := mergeOAuth2TokenDelta(baseline, local, disk)
	keys := make([]string, 0, len(merged))
	for key := range merged {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	entries := make([]oauth2CredentialEntry, 0, len(keys))
	for _, key := range keys {
		encoded, err := encryptOAuth2TokenResponse(a.dataDir, merged[key])
		if err != nil {
			return err
		}
		entries = append(entries, oauth2CredentialEntry{CacheKey: key, Data: encoded})
	}

	if len(entries) == 0 {
		path := a.oauth2CredentialsPath()
		if err := oauth2CredentialsRemove(path); err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				return err
			}
		} else {
			directory, err := os.Open(filepath.Dir(path))
			if err != nil {
				return err
			}
			syncErr := directory.Sync()
			closeErr := directory.Close()
			if syncErr != nil {
				return syncErr
			}
			if closeErr != nil {
				return closeErr
			}
		}
	} else {
		data, err := json.MarshalIndent(oauth2CredentialsFile{Credentials: entries}, "", "  ")
		if err != nil {
			return err
		}
		if err := writePrivateAtomic(a.oauth2CredentialsPath(), data); err != nil {
			return err
		}
	}
	a.oauth2Mu.Lock()
	a.oauth2 = cloneOAuth2TokenMap(merged)
	a.oauth2Baseline = cloneOAuth2TokenMap(merged)
	// US-013. Recorded only after the write landed, and computed from the maps
	// that were just installed rather than from the ones read at entry — the
	// merge may have pulled in another window's credentials, and the gate must
	// describe the state we actually persisted.
	if next, err := oauth2TokenMapFingerprint(a.oauth2Baseline, a.oauth2); err == nil {
		a.oauth2Fingerprint = next
	} else {
		// Unhashable state simply disables the gate; the next store does the
		// full work rather than skipping on a stale fingerprint.
		a.oauth2Fingerprint = ""
	}
	a.oauth2Mu.Unlock()
	return nil
}

// oauth2TokenMapFingerprint hashes the (baseline, local) pair that determines
// whether storeOAuth2Credentials can change anything on disk.
//
// Both halves are needed: the merge is a delta of local against baseline, so
// two different baselines with the same local map produce different results —
// a credential present in baseline and absent from local is a DELETION, which
// is indistinguishable from "never existed" if only local is hashed.
func oauth2TokenMapFingerprint(baseline, local map[string]oauth2TokenResponse) (string, error) {
	digest := sha256.New()
	for _, values := range []map[string]oauth2TokenResponse{baseline, local} {
		keys := make([]string, 0, len(values))
		for key := range values {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			encoded, err := json.Marshal(values[key])
			if err != nil {
				return "", err
			}
			// Length-prefixed, so that a key ending where a value begins cannot
			// collide with a different split of the same bytes.
			//
			// hash.Hash.Write is documented never to return an error, which is
			// why these are discarded rather than propagated.
			_, _ = fmt.Fprintf(digest, "%d:%s%d:", len(key), key, len(encoded))
			_, _ = digest.Write(encoded)
		}
		// Separator between the baseline and local halves, so that moving a key
		// from one to the other changes the digest.
		_, _ = digest.Write([]byte{0})
	}
	return fmt.Sprintf("%x", digest.Sum(nil)), nil
}

func cloneOAuth2TokenMap(values map[string]oauth2TokenResponse) map[string]oauth2TokenResponse {
	out := map[string]oauth2TokenResponse{}
	for k, v := range values {
		out[k] = v
	}
	return out
}

func mergeOAuth2TokenDelta(base, current, disk map[string]oauth2TokenResponse) map[string]oauth2TokenResponse {
	out := cloneOAuth2TokenMap(disk)
	for k, b := range base {
		c, ok := current[k]
		if !ok {
			delete(out, k)
		} else if !reflect.DeepEqual(b, c) {
			out[k] = c
		}
	}
	for k, c := range current {
		if _, ok := base[k]; !ok {
			out[k] = c
		}
	}
	return out
}

func encryptOAuth2TokenResponse(dataDir string, response oauth2TokenResponse) (string, error) {
	data, err := json.Marshal(oauth2TokenStorageFromResponse(response))
	if err != nil {
		return "", fmt.Errorf("encode OAuth2 credentials: %w", err)
	}
	return encryptEnvironmentSecretString(dataDir, string(data)), nil
}

func decryptOAuth2TokenResponse(dataDir, encoded string) (oauth2TokenResponse, error) {
	plain, err := decryptEnvironmentSecretString(dataDir, encoded)
	if err != nil {
		return oauth2TokenResponse{}, err
	}
	var storage oauth2TokenStorage
	if err := json.Unmarshal([]byte(plain), &storage); err != nil {
		return oauth2TokenResponse{}, fmt.Errorf("parse OAuth2 credentials: %w", err)
	}
	return oauth2TokenResponseFromStorage(storage), nil
}

func oauth2TokenStorageFromResponse(response oauth2TokenResponse) oauth2TokenStorage {
	values := map[string]interface{}{}
	for key, value := range response.Values {
		values[key] = value
	}
	storage := oauth2TokenStorage{
		AccessToken:  response.AccessToken,
		IDToken:      response.IDToken,
		RefreshToken: response.RefreshToken,
		Values:       values,
	}
	if !response.CreatedAt.IsZero() {
		storage.CreatedAt = response.CreatedAt.UnixMilli()
	}
	if !response.ExpiresAt.IsZero() {
		storage.ExpiresAt = response.ExpiresAt.UnixMilli()
	}
	return storage
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

func requestOAuth2AuthorizationCodeTokenWithTimeline(cfg OAuth2Auth, code, codeVerifier, redirectURI string) (oauth2TokenResponse, *TimelineItem, error) {
	if strings.TrimSpace(cfg.AccessTokenURL) == "" {
		return oauth2TokenResponse{}, nil, errors.New("OAuth2 access token URL is required")
	}
	if strings.TrimSpace(cfg.ClientID) == "" {
		return oauth2TokenResponse{}, nil, errors.New("OAuth2 client ID is required")
	}
	if strings.TrimSpace(code) == "" {
		return oauth2TokenResponse{}, nil, errors.New("OAuth2 authorization code is required")
	}
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", redirectURI)
	if cfg.PKCE {
		if strings.TrimSpace(codeVerifier) == "" {
			return oauth2TokenResponse{}, nil, errors.New("OAuth2 PKCE code verifier is required")
		}
		form.Set("code_verifier", codeVerifier)
	}
	params := append([]OAuth2AdditionalParam{}, cfg.TokenAdditionalParams...)
	params = append(params, legacyOAuth2AdditionalParams(cfg.AdditionalParams)...)
	return requestOAuth2TokenFormWithTimeline(cfg, cfg.AccessTokenURL, form, params)
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

type oauth2AuthorizationCallback struct {
	Code     string
	State    string
	Timeline []TimelineItem
}

type oauth2AuthorizationResult struct {
	callback oauth2AuthorizationCallback
	err      error
}

type oauth2AuthorizationWaiter struct {
	CallbackURL string
	Receive     func(context.Context) (oauth2AuthorizationCallback, error)
	Shutdown    func(context.Context) error
}

type oauth2ImplicitCallback struct {
	Tokens   map[string]interface{}
	Timeline []TimelineItem
}

type oauth2ImplicitResult struct {
	callback oauth2ImplicitCallback
	err      error
}

type oauth2ImplicitWaiter struct {
	CallbackURL string
	Receive     func(context.Context) (oauth2ImplicitCallback, error)
	Shutdown    func(context.Context) error
}

// Read-only: normalizePreferences copies its argument and returns a bool.
func (a *App) oauth2ShouldUseSystemBrowser() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return normalizePreferences(a.state.Preferences).OAuth2UseSystemBrowser
}

func (a *App) openOAuth2AuthorizationURL(authorizeURL, callbackURL, grantType string) error {
	if a.oauth2ShouldUseSystemBrowser() {
		opener := a.oauth2OpenURL
		if opener == nil {
			opener = defaultOAuth2OpenURL
		}
		return opener(a.ctx, authorizeURL)
	}
	if a.ctx != nil {
		opener := a.oauth2OpenInAppURL
		if opener == nil {
			opener = defaultOAuth2OpenInAppURL
		}
		return opener(a.ctx, oauth2AuthorizationBrowserRequest{
			AuthorizeURL: authorizeURL,
			CallbackURL:  callbackURL,
			GrantType:    grantType,
		})
	}
	opener := a.oauth2OpenURL
	if opener == nil {
		opener = defaultOAuth2OpenURL
	}
	return opener(a.ctx, authorizeURL)
}

func (a *App) requestOAuth2AuthorizationCodeTokenWithTimeline(cfg OAuth2Auth) (oauth2TokenResponse, []TimelineItem, error) {
	if strings.TrimSpace(cfg.AuthorizationURL) == "" {
		return oauth2TokenResponse{}, nil, errors.New("OAuth2 authorization URL is required")
	}
	if strings.TrimSpace(cfg.AccessTokenURL) == "" {
		return oauth2TokenResponse{}, nil, errors.New("OAuth2 access token URL is required")
	}
	if strings.TrimSpace(cfg.ClientID) == "" {
		return oauth2TokenResponse{}, nil, errors.New("OAuth2 client ID is required")
	}
	codeVerifier := ""
	codeChallenge := ""
	if cfg.PKCE {
		var err error
		codeVerifier, err = oauth2CodeVerifier()
		if err != nil {
			return oauth2TokenResponse{}, nil, err
		}
		codeChallenge = oauth2CodeChallenge(codeVerifier)
	}
	waiter, err := a.startOAuth2AuthorizationWaiter(cfg.CallbackURL)
	if err != nil {
		return oauth2TokenResponse{}, nil, err
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = waiter.Shutdown(ctx)
	}()
	authorizeURL, err := oauth2AuthorizationCodeURL(cfg, waiter.CallbackURL, codeChallenge)
	if err != nil {
		return oauth2TokenResponse{}, nil, err
	}
	if err := a.openOAuth2AuthorizationURL(authorizeURL, waiter.CallbackURL, "authorization_code"); err != nil {
		return oauth2TokenResponse{}, nil, err
	}
	timeout := a.oauth2CallbackTimeout
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	baseCtx := context.Background()
	if a.ctx != nil {
		baseCtx = a.ctx
	}
	ctx, cancel := context.WithTimeout(baseCtx, timeout)
	defer cancel()
	callback, err := waiter.Receive(ctx)
	if err != nil {
		return oauth2TokenResponse{}, nil, err
	}
	response, tokenEntry, err := requestOAuth2AuthorizationCodeTokenWithTimeline(cfg, callback.Code, codeVerifier, waiter.CallbackURL)
	timelineEntries := append([]TimelineItem{}, callback.Timeline...)
	if tokenEntry != nil {
		timelineEntries = append(timelineEntries, *tokenEntry)
	}
	if err != nil {
		return oauth2TokenResponse{}, timelineEntries, err
	}
	return response, timelineEntries, nil
}

func (a *App) requestOAuth2ImplicitTokenWithTimeline(cfg OAuth2Auth) (oauth2TokenResponse, []TimelineItem, error) {
	if strings.TrimSpace(cfg.AuthorizationURL) == "" {
		return oauth2TokenResponse{}, nil, errors.New("OAuth2 authorization URL is required")
	}
	waiter, err := a.startOAuth2ImplicitWaiter(cfg.CallbackURL)
	if err != nil {
		return oauth2TokenResponse{}, nil, err
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = waiter.Shutdown(ctx)
	}()
	authorizeURL, err := oauth2ImplicitAuthorizationURL(cfg, waiter.CallbackURL)
	if err != nil {
		return oauth2TokenResponse{}, nil, err
	}
	if err := a.openOAuth2AuthorizationURL(authorizeURL, waiter.CallbackURL, "implicit"); err != nil {
		return oauth2TokenResponse{}, nil, err
	}
	timeout := a.oauth2CallbackTimeout
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	baseCtx := context.Background()
	if a.ctx != nil {
		baseCtx = a.ctx
	}
	ctx, cancel := context.WithTimeout(baseCtx, timeout)
	defer cancel()
	callback, err := waiter.Receive(ctx)
	if err != nil {
		return oauth2TokenResponse{}, nil, err
	}
	response, err := oauth2ImplicitTokenResponse(callback.Tokens, cfg)
	if err != nil {
		return oauth2TokenResponse{}, callback.Timeline, err
	}
	return response, callback.Timeline, nil
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

func oauth2AuthorizationCodeURL(cfg OAuth2Auth, callbackURL, codeChallenge string) (string, error) {
	authorizeURL, err := url.Parse(strings.TrimSpace(cfg.AuthorizationURL))
	if err != nil {
		return "", fmt.Errorf("parse OAuth2 authorization URL: %w", err)
	}
	query := authorizeURL.Query()
	query.Add("response_type", "code")
	query.Add("client_id", cfg.ClientID)
	if callbackURL != "" {
		query.Add("redirect_uri", callbackURL)
	}
	if cfg.Scope != "" {
		query.Add("scope", cfg.Scope)
	}
	if cfg.PKCE {
		query.Add("code_challenge", codeChallenge)
		query.Add("code_challenge_method", "S256")
	}
	if cfg.State != "" {
		query.Add("state", cfg.State)
	}
	for _, param := range cfg.AuthorizationAdditionalParams {
		if !param.Enabled || strings.TrimSpace(param.Name) == "" {
			continue
		}
		if normalizeOAuth2AdditionalPlacement(param.SendIn) == "queryparams" {
			query.Add(param.Name, param.Value)
		}
	}
	authorizeURL.RawQuery = query.Encode()
	return authorizeURL.String(), nil
}

func oauth2ImplicitAuthorizationURL(cfg OAuth2Auth, callbackURL string) (string, error) {
	authorizeURL, err := url.Parse(strings.TrimSpace(cfg.AuthorizationURL))
	if err != nil {
		return "", fmt.Errorf("parse OAuth2 authorization URL: %w", err)
	}
	query := authorizeURL.Query()
	query.Add("response_type", "token")
	query.Add("client_id", cfg.ClientID)
	if callbackURL != "" {
		query.Add("redirect_uri", callbackURL)
	}
	if cfg.Scope != "" {
		query.Add("scope", cfg.Scope)
	}
	if cfg.State != "" {
		query.Add("state", cfg.State)
	}
	for _, param := range cfg.AuthorizationAdditionalParams {
		if !param.Enabled || strings.TrimSpace(param.Name) == "" {
			continue
		}
		if normalizeOAuth2AdditionalPlacement(param.SendIn) == "queryparams" {
			query.Add(param.Name, param.Value)
		}
	}
	authorizeURL.RawQuery = query.Encode()
	return authorizeURL.String(), nil
}

func (a *App) startOAuth2AuthorizationWaiter(callbackURL string) (oauth2AuthorizationWaiter, error) {
	effectiveCallbackURL := oauth2EffectiveBrowserCallbackURL(callbackURL)
	if oauth2CallbackCanUseLoopback(effectiveCallbackURL) {
		return startOAuth2AuthorizationWaiter(effectiveCallbackURL)
	}
	effectiveURL, err := oauth2NormalizeExternalCallbackURL(effectiveCallbackURL)
	if err != nil {
		return oauth2AuthorizationWaiter{}, err
	}
	resultCh := make(chan oauth2AuthorizationResult, 1)
	a.oauth2PendingMu.Lock()
	a.oauth2Authorization[effectiveURL] = resultCh
	a.oauth2PendingMu.Unlock()
	return oauth2AuthorizationWaiter{
		CallbackURL: effectiveURL,
		Receive: func(ctx context.Context) (oauth2AuthorizationCallback, error) {
			select {
			case result := <-resultCh:
				return result.callback, result.err
			case <-ctx.Done():
				return oauth2AuthorizationCallback{}, fmt.Errorf("OAuth2 authorization callback timed out: %w", ctx.Err())
			}
		},
		Shutdown: func(context.Context) error {
			a.oauth2PendingMu.Lock()
			delete(a.oauth2Authorization, effectiveURL)
			a.oauth2PendingMu.Unlock()
			return nil
		},
	}, nil
}

func (a *App) startOAuth2ImplicitWaiter(callbackURL string) (oauth2ImplicitWaiter, error) {
	effectiveCallbackURL := oauth2EffectiveBrowserCallbackURL(callbackURL)
	if oauth2CallbackCanUseLoopback(effectiveCallbackURL) {
		return startOAuth2ImplicitWaiter(effectiveCallbackURL)
	}
	effectiveURL, err := oauth2NormalizeExternalCallbackURL(effectiveCallbackURL)
	if err != nil {
		return oauth2ImplicitWaiter{}, err
	}
	resultCh := make(chan oauth2ImplicitResult, 1)
	a.oauth2PendingMu.Lock()
	a.oauth2Implicit[effectiveURL] = resultCh
	a.oauth2PendingMu.Unlock()
	return oauth2ImplicitWaiter{
		CallbackURL: effectiveURL,
		Receive: func(ctx context.Context) (oauth2ImplicitCallback, error) {
			select {
			case result := <-resultCh:
				return result.callback, result.err
			case <-ctx.Done():
				return oauth2ImplicitCallback{}, fmt.Errorf("OAuth2 implicit callback timed out: %w", ctx.Err())
			}
		},
		Shutdown: func(context.Context) error {
			a.oauth2PendingMu.Lock()
			delete(a.oauth2Implicit, effectiveURL)
			a.oauth2PendingMu.Unlock()
			return nil
		},
	}, nil
}

func oauth2EffectiveBrowserCallbackURL(callbackURL string) string {
	raw := strings.TrimSpace(callbackURL)
	if raw == "" {
		return brunoOAuth2DefaultCallbackURL
	}
	return raw
}

func oauth2CallbackCanUseLoopback(callbackURL string) bool {
	raw := strings.TrimSpace(callbackURL)
	if raw == "" {
		return false
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "http" {
		return false
	}
	return oauth2LoopbackHost(parsed.Hostname())
}

func oauth2NormalizeExternalCallbackURL(callbackURL string) (string, error) {
	raw := strings.TrimSpace(callbackURL)
	if raw == "" {
		raw = brunoOAuth2DefaultCallbackURL
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("parse OAuth2 hosted callback URL: %w", err)
	}
	if parsed.Scheme == "" {
		return "", errors.New("OAuth2 hosted callback URL requires a URL scheme")
	}
	if (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Hostname() == "" {
		return "", errors.New("OAuth2 hosted callback URL requires a host")
	}
	parsed.Fragment = ""
	return parsed.String(), nil
}

func (a *App) CompleteOAuth2Callback(rawURL string) (bool, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return false, fmt.Errorf("parse OAuth2 callback URL: %w", err)
	}
	a.oauth2PendingMu.Lock()
	for callbackURL, resultCh := range a.oauth2Authorization {
		if !oauth2CallbackMatchesPending(parsed, callbackURL) {
			continue
		}
		delete(a.oauth2Authorization, callbackURL)
		result := oauth2AuthorizationResultFromURL(callbackURL, parsed)
		a.oauth2PendingMu.Unlock()
		resultCh <- result
		return true, result.err
	}
	for callbackURL, resultCh := range a.oauth2Implicit {
		if !oauth2CallbackMatchesPending(parsed, callbackURL) {
			continue
		}
		delete(a.oauth2Implicit, callbackURL)
		result := oauth2ImplicitResultFromURL(callbackURL, parsed)
		a.oauth2PendingMu.Unlock()
		resultCh <- result
		return true, result.err
	}
	a.oauth2PendingMu.Unlock()
	return false, nil
}

func oauth2CallbackMatchesPending(callback *url.URL, expected string) bool {
	return oauth2ExternalCallbackMatches(callback, expected) || oauth2IsAppProtocolCallbackURL(callback)
}

func oauth2ExternalCallbackMatches(callback *url.URL, expected string) bool {
	if callback == nil {
		return false
	}
	expectedURL, err := url.Parse(expected)
	if err != nil {
		return false
	}
	if callback.Scheme != expectedURL.Scheme || !strings.EqualFold(callback.Host, expectedURL.Host) {
		return false
	}
	return strings.HasPrefix(callback.EscapedPath(), expectedURL.EscapedPath())
}

func oauth2IsAppProtocolCallback(rawURL string) bool {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return false
	}
	return oauth2IsAppProtocolCallbackURL(parsed)
}

func oauth2IsAppProtocolCallbackURL(callback *url.URL) bool {
	if callback == nil {
		return false
	}
	scheme := strings.ToLower(callback.Scheme)
	if scheme != "bruno" && scheme != "liteapi" {
		return false
	}
	if !strings.EqualFold(callback.Host, oauth2ProtocolCallbackHost) {
		return false
	}
	return strings.TrimRight(callback.EscapedPath(), "/") == oauth2ProtocolCallbackPath
}

func oauth2AuthorizationResultFromURL(callbackURL string, callback *url.URL) oauth2AuthorizationResult {
	query := callback.Query()
	status := http.StatusOK
	statusText := http.StatusText(status)
	callbackErr := error(nil)
	code := query.Get("code")
	if oauthErr := query.Get("error"); oauthErr != "" {
		status = http.StatusBadRequest
		statusText = http.StatusText(status)
		callbackErr = fmt.Errorf("OAuth2 authorization failed: %s %s", oauthErr, query.Get("error_description"))
	} else if strings.TrimSpace(code) == "" {
		status = http.StatusBadRequest
		statusText = http.StatusText(status)
		callbackErr = errors.New("OAuth2 callback did not include code")
	}
	timelineEntry := TimelineItem{
		At:         time.Now(),
		Method:     http.MethodGet,
		URL:        oauth2ExternalCallbackTimelineURL(callbackURL, callback, true),
		Status:     status,
		StatusText: statusText,
		Message:    fmt.Sprintf("%s %s -> %d", http.MethodGet, oauth2ExternalCallbackTimelineURL(callbackURL, callback, true), status),
	}
	if callbackErr != nil {
		timelineEntry.Error = callbackErr.Error()
	}
	return oauth2AuthorizationResult{
		callback: oauth2AuthorizationCallback{Code: code, State: query.Get("state"), Timeline: []TimelineItem{timelineEntry}},
		err:      callbackErr,
	}
}

func oauth2ImplicitResultFromURL(callbackURL string, callback *url.URL) oauth2ImplicitResult {
	values := callback.Query()
	if callback.Fragment != "" {
		fragmentValues, err := url.ParseQuery(callback.Fragment)
		if err == nil {
			values = fragmentValues
		}
	}
	status := http.StatusOK
	callbackErr := error(nil)
	if oauthErr := values.Get("error"); oauthErr != "" {
		status = http.StatusBadRequest
		callbackErr = fmt.Errorf("OAuth2 authorization failed: %s %s", oauthErr, values.Get("error_description"))
	} else if strings.TrimSpace(values.Get("access_token")) == "" {
		status = http.StatusBadRequest
		callbackErr = errors.New("OAuth2 implicit callback did not include access_token")
	}
	timelineEntry := TimelineItem{
		At:         time.Now(),
		Method:     http.MethodGet,
		URL:        oauth2ExternalCallbackTimelineURL(callbackURL, callback, false),
		Status:     status,
		StatusText: http.StatusText(status),
		Message:    fmt.Sprintf("%s %s -> %d", http.MethodGet, oauth2ExternalCallbackTimelineURL(callbackURL, callback, false), status),
	}
	if callbackErr != nil {
		timelineEntry.Error = callbackErr.Error()
	}
	result := oauth2ImplicitResult{callback: oauth2ImplicitCallback{Timeline: []TimelineItem{timelineEntry}}, err: callbackErr}
	if callbackErr == nil {
		result.callback.Tokens = oauth2TokenPayloadFromValues(values)
	}
	return result
}

func oauth2ExternalCallbackTimelineURL(callbackURL string, callback *url.URL, includeQuery bool) string {
	if oauth2IsAppProtocolCallbackURL(callback) {
		display := *callback
		if !includeQuery {
			display.RawQuery = ""
		}
		display.Fragment = ""
		return display.String()
	}
	parsed, err := url.Parse(callbackURL)
	if err != nil {
		return callbackURL
	}
	if callback != nil {
		parsed.Path = callback.EscapedPath()
		if includeQuery {
			parsed.RawQuery = callback.RawQuery
		} else {
			parsed.RawQuery = ""
		}
	}
	parsed.Fragment = ""
	return parsed.String()
}

func startOAuth2AuthorizationWaiter(callbackURL string) (oauth2AuthorizationWaiter, error) {
	effectiveURL, listenAddress, callbackPath, err := oauth2CallbackListenConfig(callbackURL)
	if err != nil {
		return oauth2AuthorizationWaiter{}, err
	}
	listener, err := net.Listen("tcp", listenAddress)
	if err != nil {
		return oauth2AuthorizationWaiter{}, fmt.Errorf("listen for OAuth2 callback: %w", err)
	}
	effectiveURL = oauth2CallbackURLWithListenerPort(effectiveURL, listener.Addr())
	resultCh := make(chan struct {
		callback oauth2AuthorizationCallback
		err      error
	}, 1)
	server := &http.Server{}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != callbackPath {
			http.NotFound(w, r)
			return
		}
		status := http.StatusOK
		statusText := "OK"
		query := r.URL.Query()
		callbackErr := error(nil)
		code := query.Get("code")
		if oauthErr := query.Get("error"); oauthErr != "" {
			status = http.StatusBadRequest
			statusText = http.StatusText(status)
			callbackErr = fmt.Errorf("OAuth2 authorization failed: %s %s", oauthErr, query.Get("error_description"))
		} else if strings.TrimSpace(code) == "" {
			status = http.StatusBadRequest
			statusText = http.StatusText(status)
			callbackErr = errors.New("OAuth2 callback did not include code")
		}
		callbackURLValue := oauth2CallbackRequestURL(effectiveURL, r)
		timelineEntry := TimelineItem{
			At:         time.Now(),
			Method:     http.MethodGet,
			URL:        callbackURLValue,
			Status:     status,
			StatusText: statusText,
			Message:    fmt.Sprintf("%s %s -> %d", http.MethodGet, callbackURLValue, status),
		}
		if callbackErr != nil {
			timelineEntry.Error = callbackErr.Error()
		}
		select {
		case resultCh <- struct {
			callback oauth2AuthorizationCallback
			err      error
		}{callback: oauth2AuthorizationCallback{Code: code, State: query.Get("state"), Timeline: []TimelineItem{timelineEntry}}, err: callbackErr}:
		default:
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(status)
		if callbackErr != nil {
			_, _ = io.WriteString(w, "<html><body>OAuth2 authorization failed. You can return to LiteAPI.</body></html>")
			return
		}
		_, _ = io.WriteString(w, "<html><body>OAuth2 authorization complete. You can return to LiteAPI.</body></html>")
	})
	server.Handler = mux
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			select {
			case resultCh <- struct {
				callback oauth2AuthorizationCallback
				err      error
			}{err: err}:
			default:
			}
		}
	}()
	return oauth2AuthorizationWaiter{
		CallbackURL: effectiveURL,
		Receive: func(ctx context.Context) (oauth2AuthorizationCallback, error) {
			select {
			case result := <-resultCh:
				return result.callback, result.err
			case <-ctx.Done():
				return oauth2AuthorizationCallback{}, fmt.Errorf("OAuth2 authorization callback timed out: %w", ctx.Err())
			}
		},
		Shutdown: server.Shutdown,
	}, nil
}

func startOAuth2ImplicitWaiter(callbackURL string) (oauth2ImplicitWaiter, error) {
	effectiveURL, listenAddress, callbackPath, err := oauth2CallbackListenConfig(callbackURL)
	if err != nil {
		return oauth2ImplicitWaiter{}, err
	}
	listener, err := net.Listen("tcp", listenAddress)
	if err != nil {
		return oauth2ImplicitWaiter{}, fmt.Errorf("listen for OAuth2 callback: %w", err)
	}
	effectiveURL = oauth2CallbackURLWithListenerPort(effectiveURL, listener.Addr())
	fragmentURL := oauth2ImplicitFragmentCallbackURL(effectiveURL)
	fragmentParsed, err := url.Parse(fragmentURL)
	if err != nil {
		_ = listener.Close()
		return oauth2ImplicitWaiter{}, fmt.Errorf("parse OAuth2 implicit fragment callback URL: %w", err)
	}
	fragmentPath := fragmentParsed.EscapedPath()
	if fragmentPath == "" {
		fragmentPath = "/"
	}
	resultCh := make(chan struct {
		callback oauth2ImplicitCallback
		err      error
	}, 1)
	server := &http.Server{}
	mux := http.NewServeMux()
	var timelineMu sync.Mutex
	callbackTimeline := []TimelineItem{}
	recordCallbackTimeline := func(entry TimelineItem) []TimelineItem {
		timelineMu.Lock()
		defer timelineMu.Unlock()
		callbackTimeline = append(callbackTimeline, entry)
		return append([]TimelineItem{}, callbackTimeline...)
	}
	currentCallbackTimeline := func(extra TimelineItem) []TimelineItem {
		timelineMu.Lock()
		defer timelineMu.Unlock()
		out := append([]TimelineItem{}, callbackTimeline...)
		out = append(out, extra)
		return out
	}
	complete := func(tokens map[string]interface{}, timeline []TimelineItem, err error) {
		select {
		case resultCh <- struct {
			callback oauth2ImplicitCallback
			err      error
		}{callback: oauth2ImplicitCallback{Tokens: tokens, Timeline: timeline}, err: err}:
		default:
		}
	}
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case callbackPath:
			query := r.URL.Query()
			status := http.StatusOK
			callbackErr := error(nil)
			tokens := map[string]interface{}(nil)
			if oauthErr := query.Get("error"); oauthErr != "" {
				status = http.StatusBadRequest
				callbackErr = fmt.Errorf("OAuth2 authorization failed: %s %s", oauthErr, query.Get("error_description"))
			} else if query.Get("access_token") != "" {
				tokens = oauth2TokenPayloadFromValues(query)
			}
			timelineEntry := oauth2CallbackTimelineItem(effectiveURL, r, status, callbackErr, false)
			timeline := recordCallbackTimeline(timelineEntry)
			if callbackErr != nil || tokens != nil {
				complete(tokens, timeline, callbackErr)
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(status)
			if callbackErr != nil {
				_, _ = io.WriteString(w, "<html><body>OAuth2 authorization failed. You can return to LiteAPI.</body></html>")
				return
			}
			if tokens != nil {
				_, _ = io.WriteString(w, "<html><body>OAuth2 authorization complete. You can return to LiteAPI.</body></html>")
				return
			}
			_, _ = io.WriteString(w, oauth2ImplicitCallbackHTML(fragmentPath))
		case fragmentPath:
			values, parseErr := oauth2ImplicitFragmentValues(r)
			status := http.StatusOK
			callbackErr := parseErr
			if callbackErr == nil {
				if oauthErr := values.Get("error"); oauthErr != "" {
					status = http.StatusBadRequest
					callbackErr = fmt.Errorf("OAuth2 authorization failed: %s %s", oauthErr, values.Get("error_description"))
				} else if strings.TrimSpace(values.Get("access_token")) == "" {
					status = http.StatusBadRequest
					callbackErr = errors.New("OAuth2 implicit callback did not include access_token")
				}
			} else {
				status = http.StatusBadRequest
			}
			timelineEntry := oauth2CallbackTimelineItem(effectiveURL, r, status, callbackErr, false)
			timeline := currentCallbackTimeline(timelineEntry)
			if callbackErr != nil {
				complete(nil, timeline, callbackErr)
			} else {
				complete(oauth2TokenPayloadFromValues(values), timeline, nil)
			}
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(status)
			if callbackErr != nil {
				_, _ = io.WriteString(w, "OAuth2 authorization failed. You can return to LiteAPI.")
				return
			}
			_, _ = io.WriteString(w, "OAuth2 authorization complete. You can return to LiteAPI.")
		default:
			http.NotFound(w, r)
		}
	})
	server.Handler = mux
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			select {
			case resultCh <- struct {
				callback oauth2ImplicitCallback
				err      error
			}{err: err}:
			default:
			}
		}
	}()
	return oauth2ImplicitWaiter{
		CallbackURL: effectiveURL,
		Receive: func(ctx context.Context) (oauth2ImplicitCallback, error) {
			select {
			case result := <-resultCh:
				return result.callback, result.err
			case <-ctx.Done():
				return oauth2ImplicitCallback{}, fmt.Errorf("OAuth2 implicit callback timed out: %w", ctx.Err())
			}
		},
		Shutdown: server.Shutdown,
	}, nil
}

func oauth2CallbackListenConfig(callbackURL string) (string, string, string, error) {
	raw := strings.TrimSpace(callbackURL)
	if raw == "" {
		raw = "http://127.0.0.1:0/oauth2/callback"
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", "", "", fmt.Errorf("parse OAuth2 callback URL: %w", err)
	}
	if parsed.Scheme != "http" {
		return "", "", "", errors.New("OAuth2 browser flow requires an http:// loopback callback URL")
	}
	host := parsed.Hostname()
	if host == "" {
		host = "127.0.0.1"
	}
	if !oauth2LoopbackHost(host) {
		return "", "", "", errors.New("OAuth2 browser flow requires a localhost or 127.0.0.1 callback URL")
	}
	port := parsed.Port()
	if port == "" {
		port = "0"
	}
	path := parsed.EscapedPath()
	if path == "" {
		path = "/"
	}
	listenHost := host
	if strings.EqualFold(listenHost, "localhost") {
		listenHost = "127.0.0.1"
	}
	parsed.Host = net.JoinHostPort(host, port)
	parsed.Path = path
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), net.JoinHostPort(listenHost, port), path, nil
}

func oauth2LoopbackHost(host string) bool {
	normalized := strings.Trim(strings.ToLower(host), "[]")
	return normalized == "localhost" || normalized == "127.0.0.1" || normalized == "::1"
}

func oauth2CallbackURLWithListenerPort(callbackURL string, addr net.Addr) string {
	parsed, err := url.Parse(callbackURL)
	if err != nil {
		return callbackURL
	}
	_, port, err := net.SplitHostPort(addr.String())
	if err != nil || port == "" {
		return callbackURL
	}
	parsed.Host = net.JoinHostPort(parsed.Hostname(), port)
	return parsed.String()
}

func oauth2CallbackRequestURL(callbackURL string, r *http.Request) string {
	parsed, err := url.Parse(callbackURL)
	if err != nil {
		return callbackURL
	}
	parsed.Path = r.URL.Path
	parsed.RawQuery = r.URL.RawQuery
	parsed.Fragment = ""
	return parsed.String()
}

func oauth2CallbackRequestURLWithoutQuery(callbackURL string, r *http.Request) string {
	parsed, err := url.Parse(callbackURL)
	if err != nil {
		return callbackURL
	}
	parsed.Path = r.URL.Path
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}

func oauth2CallbackTimelineItem(callbackURL string, r *http.Request, status int, callbackErr error, includeQuery bool) TimelineItem {
	requestURL := oauth2CallbackRequestURLWithoutQuery(callbackURL, r)
	if includeQuery {
		requestURL = oauth2CallbackRequestURL(callbackURL, r)
	}
	statusText := cleanStatusText(status, fmt.Sprintf("%d %s", status, http.StatusText(status)))
	entry := TimelineItem{
		At:         time.Now(),
		Method:     strings.ToUpper(firstNonEmpty(r.Method, http.MethodGet)),
		URL:        requestURL,
		Status:     status,
		StatusText: statusText,
		Message:    fmt.Sprintf("%s %s -> %d", strings.ToUpper(firstNonEmpty(r.Method, http.MethodGet)), requestURL, status),
	}
	if callbackErr != nil {
		entry.Error = callbackErr.Error()
	}
	return entry
}

func oauth2ImplicitFragmentCallbackURL(callbackURL string) string {
	parsed, err := url.Parse(callbackURL)
	if err != nil {
		return callbackURL
	}
	callbackPath := strings.TrimRight(parsed.EscapedPath(), "/")
	if callbackPath == "" {
		callbackPath = ""
	}
	parsed.Path = callbackPath + "/__liteapi_oauth2_fragment"
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}

func oauth2ImplicitCallbackHTML(fragmentPath string) string {
	encodedPath, _ := json.Marshal(fragmentPath)
	return `<!doctype html><html><head><meta charset="utf-8"><title>OAuth2 authorization complete</title></head><body>OAuth2 authorization complete. You can return to LiteAPI.<script>
(function () {
  var payload = window.location.hash ? window.location.hash.slice(1) : (window.location.search ? window.location.search.slice(1) : "");
  fetch(` + string(encodedPath) + `, { method: "POST", headers: { "Content-Type": "application/x-www-form-urlencoded" }, body: payload })
    .catch(function () {});
})();
</script></body></html>`
}

func oauth2ImplicitFragmentValues(r *http.Request) (url.Values, error) {
	switch r.Method {
	case http.MethodGet:
		return r.URL.Query(), nil
	case http.MethodPost:
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			return nil, fmt.Errorf("read OAuth2 implicit callback: %w", err)
		}
		values, err := url.ParseQuery(string(body))
		if err != nil {
			return nil, fmt.Errorf("parse OAuth2 implicit callback: %w", err)
		}
		return values, nil
	default:
		return nil, fmt.Errorf("OAuth2 implicit callback method %s is not supported", r.Method)
	}
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

func oauth2ImplicitTokenResponse(payload map[string]interface{}, cfg OAuth2Auth) (oauth2TokenResponse, error) {
	if payload == nil {
		payload = map[string]interface{}{}
	}
	if strings.TrimSpace(stringMapValue(payload, "token_type")) == "" {
		payload["token_type"] = "Bearer"
	}
	response := parseOAuth2TokenResponse(payload, time.Now())
	if response.AccessToken == "" {
		return oauth2TokenResponse{}, errors.New("no access token received from authorization server")
	}
	if oauth2TokenValue(response, cfg.TokenSource) == "" {
		return oauth2TokenResponse{}, fmt.Errorf("OAuth2 token response did not include %s", firstNonEmpty(cfg.TokenSource, "access_token"))
	}
	return response, nil
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

func oauth2CredentialVariablesFromCache(cache map[string]oauth2TokenResponse) map[string]interface{} {
	variables := map[string]interface{}{}
	for cacheKey, response := range cache {
		credentialsID := oauth2CacheCredentialsID(cacheKey)
		for key, value := range response.credentialValues() {
			variables["$oauth2."+credentialsID+"."+key] = value
		}
	}
	return variables
}

func (a *App) oauth2CredentialVariablesSnapshot() map[string]interface{} {
	a.oauth2Mu.Lock()
	defer a.oauth2Mu.Unlock()
	return oauth2CredentialVariablesFromCache(a.oauth2)
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
