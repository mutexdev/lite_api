package core

import (
	"context"
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

	"github.com/mutexdev/lite_api/internal/mcpserver"
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

// THE OAUTH2 FETCH CHAIN COMES IN PAIRS (§4.5 of the Phase 6 design).
//
// Every function below that reaches the network has a *Context variant taking
// the execution's context, and the ORIGINAL NAME SURVIVES as a one-line delegate
// passing context.Background(). That is not politeness towards old code: the
// callers of this chain live in files other tasks own — app_grpc.go:61,
// app_grpc_stream.go:274 and app.go:538 attach the UI marker in their own wave,
// and app_request_build.go's applyAuth threads the send's context in its own —
// so a signature flip here would break files this task must not touch. The
// delegates behave exactly as before, because a context with no provenance
// carries no policy and mcpPolicyFromContext returns nil, which every checkpoint
// below treats as "not an MCP run, do not check anything" (§1.2(4)).
//
// The delegates are deleted in the final wave, once every caller passes a
// context of its own.

func (a *App) fetchOAuth2Token(auth OAuth2Auth, vars map[string]string) (string, error) {
	return a.fetchOAuth2TokenWithContext(context.Background(), auth, vars)
}

func (a *App) fetchOAuth2TokenWithContext(ctx context.Context, auth OAuth2Auth, vars map[string]string) (string, error) {
	token, _, err := a.fetchOAuth2TokenWithTimelineContext(ctx, auth, vars)
	return token, err
}

func (a *App) fetchOAuth2TokenWithTimeline(auth OAuth2Auth, vars map[string]string) (string, []TimelineItem, error) {
	return a.fetchOAuth2TokenWithTimelineContext(context.Background(), auth, vars)
}

// fetchOAuth2TokenWithTimelineContext is the whole grant decision: serve the
// cache, refresh it, or fetch anew.
//
// THE ORDER OF THE FIRST TWO BRANCHES IS LOAD-BEARING FOR AGENT RUNS. An MCP run
// may not open a browser (§2 row 5), but it may absolutely use a token a browser
// grant already produced — that is the entire point of the refusal message,
// which tells the user to fetch the token once in the app. So the cached-token
// check and the auto-refresh both run BEFORE the interactive-grant branches and
// are NOT provenance-conditioned: an agent run with a valid cached token, or
// with an expired one and a usable refresh token, proceeds silently. Only when
// neither can serve does control reach a branch that would open a browser, and
// that is where the refusal sits.
//
// A FAILED REFRESH RETURNS ITS OWN ERROR rather than falling through to the
// browser — the `return` inside the refresh branch, not a `break`. That is
// pre-existing behavior and it is deliberately preserved: falling through would
// turn a transient token-endpoint failure into a browser popup for a UI send,
// and into the interactive-grant refusal for an agent run, hiding the real
// cause in both.
func (a *App) fetchOAuth2TokenWithTimelineContext(ctx context.Context, auth OAuth2Auth, vars map[string]string) (string, []TimelineItem, error) {
	if ctx == nil {
		ctx = context.Background()
	}
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
			refreshed, timelineEntry, err := requestOAuth2RefreshTokenWithTimelineContext(ctx, cfg, cached.RefreshToken)
			if err != nil {
				return "", optionalTimelineEntry(timelineEntry), err
			}
			a.oauth2Mu.Lock()
			// §1.4(12) — SEE THE NOTE BELOW. This write is shared with the UI.
			a.oauth2[key] = refreshed
			a.oauth2Mu.Unlock()
			return oauth2TokenValue(refreshed, cfg.TokenSource), optionalTimelineEntry(timelineEntry), nil
		}
	}
	a.oauth2Mu.Unlock()

	if strings.TrimSpace(cfg.GrantType) == "authorization_code" {
		if err := oauth2InteractiveGrantRefusal(ctx, "authorization_code"); err != nil {
			return "", nil, err
		}
		response, timelineEntries, err := a.requestOAuth2AuthorizationCodeTokenWithTimelineContext(ctx, cfg)
		if err != nil {
			return "", timelineEntries, err
		}
		a.oauth2Mu.Lock()
		a.oauth2[key] = response
		a.oauth2Mu.Unlock()
		return oauth2TokenValue(response, cfg.TokenSource), timelineEntries, nil
	}
	if strings.TrimSpace(cfg.GrantType) == "implicit" {
		if err := oauth2InteractiveGrantRefusal(ctx, "implicit"); err != nil {
			return "", nil, err
		}
		response, timelineEntries, err := a.requestOAuth2ImplicitTokenWithTimelineContext(ctx, cfg)
		if err != nil {
			return "", timelineEntries, err
		}
		a.oauth2Mu.Lock()
		a.oauth2[key] = response
		a.oauth2Mu.Unlock()
		return oauth2TokenValue(response, cfg.TokenSource), timelineEntries, nil
	}

	response, timelineEntry, err := requestOAuth2TokenWithTimelineContext(ctx, cfg)
	if err != nil {
		return "", optionalTimelineEntry(timelineEntry), err
	}
	a.oauth2Mu.Lock()
	// §1.4(12), STATED HERE SO NOBODY LATER READS IT AS A LEAK. a.oauth2 is ONE
	// process-wide cache, not one per execution: a token an MCP run just fetched
	// from an endpoint the policy checked lands here and may later serve a UI
	// send, and a token a UI send fetched may later serve an MCP run. That is a
	// retained, deliberate persistent side effect of an agent run (§3 lists it
	// beside item.Response, item.Timeline and history as the exceptions to
	// non-persistence), and it is sound for two reasons:
	//
	//   - It involves NO NEW EGRESS in either direction. Serving a cached token
	//     contacts nobody; the fetch that filled the cache was authorized as
	//     token-class against the fetching run's own scope before it happened.
	//   - It cannot widen Base. Base derives only from AppState reads (§4.1) and
	//     this cache is neither read by mcpDefinitionOrigins nor written to disk
	//     by it, so no value here can become a future run's authority.
	//
	// The direction that would be a leak — an agent RETARGETING the token
	// endpoint so the cache fills from somewhere new — is closed one layer down,
	// at requestOAuth2TokenFormWithTimelineContext's checkpoint.
	a.oauth2[key] = response
	a.oauth2Mu.Unlock()
	return oauth2TokenValue(response, cfg.TokenSource), optionalTimelineEntry(timelineEntry), nil
}

func fetchOAuth2Token(auth OAuth2Auth, vars map[string]string) (string, error) {
	return fetchOAuth2TokenWithContext(context.Background(), auth, vars)
}

// fetchOAuth2TokenWithContext is the cache-less package-level fetcher used by
// the non-App applyAuth path. Same pairing rule as the method above.
func fetchOAuth2TokenWithContext(ctx context.Context, auth OAuth2Auth, vars map[string]string) (string, error) {
	cfg := interpolateOAuth2Auth(auth, vars)
	response, err := requestOAuth2TokenContext(ctx, cfg)
	if err != nil {
		return "", err
	}
	return oauth2TokenValue(response, cfg.TokenSource), nil
}

// oauth2InteractiveGrantRefusal is §2 row 5: under MCP provenance the
// authorization_code and implicit grants are refused, BEFORE any browser opens.
//
// It returns nil when there is no policy on the context — a UI send keeps every
// capability (§1.2(4)) — so call sites read as `if err := ...; err != nil`.
//
// WHY REFUSE RATHER THAN PROMPT. A browser grant is not a destination question
// an approval could answer: it hands control to a browser LiteAPI does not
// drive, for a sign-in only the human can complete, and the resulting navigation
// is outside the engine entirely (§1.4(7)). There is nothing for the user to
// approve except "yes, open a window", which the user can do far more directly
// by running the request in the app — which is exactly what the message says.
func oauth2InteractiveGrantRefusal(ctx context.Context, grantType string) error {
	policy := mcpPolicyFromContext(ctx)
	if policy == nil {
		return nil
	}
	grant := strings.TrimSpace(grantType)
	if grant == "" {
		grant = "interactive"
	}
	return policy.Refuse(
		fmt.Sprintf("This request uses the OAuth2 %s grant, which needs a browser sign-in", grant),
		"Open the request in the LiteAPI app and fetch the token once; agent runs will then use the cached token (and its refresh token) automatically.",
	)
}

// authorizeOAuth2TokenEgress is the token/refresh checkpoint (§4.3 item 2,
// §5 row 4). It runs against the RESOLVED URL the request object carries —
// after interpolation and after applyOAuth2AdditionalParams has had its say —
// so what is checked is what is dialed.
//
// The kind is `token`, whose approvals are a class of their own (§6): an
// approval that let this request reach its own API host does NOT let it reach
// that host as a token endpoint, and vice versa.
func authorizeOAuth2TokenEgress(ctx context.Context, tokenURL *url.URL) error {
	policy := mcpPolicyFromContext(ctx)
	if policy == nil {
		return nil
	}
	origin, ok := originOfParsedURL(tokenURL)
	if !ok {
		redacted := "an unresolved destination"
		if tokenURL != nil {
			redacted = tokenURL.Redacted()
		}
		return fmt.Errorf("%w: this run's OAuth2 token endpoint %q is not an http(s) destination LiteAPI can check; fix the token URL or run this request in the LiteAPI app",
			mcpserver.ErrDenied, redacted)
	}
	return policy.Authorize(ctx, origin, egressKindToken)
}

func oauth2CacheKey(cfg OAuth2Auth) string {
	return firstNonEmpty(cfg.AccessTokenURL, cfg.AuthorizationURL) + "|" + firstNonEmpty(cfg.CredentialsID, "credentials")
}

func requestOAuth2Token(cfg OAuth2Auth) (oauth2TokenResponse, error) {
	return requestOAuth2TokenContext(context.Background(), cfg)
}

func requestOAuth2TokenContext(ctx context.Context, cfg OAuth2Auth) (oauth2TokenResponse, error) {
	response, _, err := requestOAuth2TokenWithGrantTimelineContext(ctx, cfg, strings.TrimSpace(cfg.GrantType), "")
	return response, err
}

func requestOAuth2TokenWithTimeline(cfg OAuth2Auth) (oauth2TokenResponse, *TimelineItem, error) {
	return requestOAuth2TokenWithTimelineContext(context.Background(), cfg)
}

func requestOAuth2TokenWithTimelineContext(ctx context.Context, cfg OAuth2Auth) (oauth2TokenResponse, *TimelineItem, error) {
	return requestOAuth2TokenWithGrantTimelineContext(ctx, cfg, strings.TrimSpace(cfg.GrantType), "")
}

func requestOAuth2RefreshTokenWithTimeline(cfg OAuth2Auth, refreshToken string) (oauth2TokenResponse, *TimelineItem, error) {
	return requestOAuth2RefreshTokenWithTimelineContext(context.Background(), cfg, refreshToken)
}

// requestOAuth2RefreshTokenWithTimelineContext is the refresh half of the
// checkpoint. It needs no check of its own: the refresh URL it resolves
// (RefreshTokenURL, falling back to AccessTokenURL) flows down the same
// grant/form path as an initial fetch, so the single checkpoint at the form
// layer covers both — one place where the URL is final, rather than two that
// could drift apart.
func requestOAuth2RefreshTokenWithTimelineContext(ctx context.Context, cfg OAuth2Auth, refreshToken string) (oauth2TokenResponse, *TimelineItem, error) {
	response, timelineEntry, err := requestOAuth2TokenWithGrantTimelineContext(ctx, cfg, "refresh_token", refreshToken)
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
	return requestOAuth2TokenWithGrantTimelineContext(context.Background(), cfg, grantType, refreshToken)
}

func requestOAuth2TokenWithGrantTimelineContext(ctx context.Context, cfg OAuth2Auth, grantType, refreshToken string) (oauth2TokenResponse, *TimelineItem, error) {
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
	return requestOAuth2TokenFormWithTimelineContext(ctx, cfg, tokenURL, form, params)
}

func requestOAuth2TokenFormWithTimeline(cfg OAuth2Auth, tokenURL string, form url.Values, params []OAuth2AdditionalParam) (oauth2TokenResponse, *TimelineItem, error) {
	return requestOAuth2TokenFormWithTimelineContext(context.Background(), cfg, tokenURL, form, params)
}

// requestOAuth2TokenFormWithTimelineContext is the bottom of the chain and the
// only place in it that touches the network. Two Phase 6 obligations land here.
//
// THE REQUEST CARRIES THE CONTEXT (http.NewRequestWithContext, where this used
// to call http.NewRequest). That is what lets the guard transport T4 wraps
// around sharedCredentialHTTPClient see this egress at all: the backstop reads
// provenance from req.Context(), and a background-context request would arrive
// unlabeled — invisible while strict is off, and refused once it flips. The
// context is also narrowed to egress kind `token` so the backstop authorizes it
// under the right class; without that narrowing the transport would default to
// `main` (mcp_origin.go) and a request-class approval would silently cover a
// token exchange.
//
// THE CHECKPOINT RUNS IMMEDIATELY BEFORE Do. Not at the top of the function:
// applyOAuth2AdditionalParams can rewrite the query, and although a query cannot
// change an origin, checking the URL object that is about to be dialed rather
// than the string that produced it removes the whole class of "what was checked
// is not quite what was sent" question. A refusal returns before any connection
// is opened — the test asserts the endpoint received zero requests.
func requestOAuth2TokenFormWithTimelineContext(ctx context.Context, cfg OAuth2Auth, tokenURL string, form url.Values, params []OAuth2AdditionalParam) (oauth2TokenResponse, *TimelineItem, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx = mcpContextWithEgressKind(ctx, egressKindToken)
	tokenReq, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, nil)
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
	if err := authorizeOAuth2TokenEgress(ctx, tokenReq.URL); err != nil {
		// No request was made, so there is no timeline entry to report: a
		// refusal is not a failed exchange, and rendering one as a POST that
		// never happened would put a destination in the timeline that LiteAPI
		// specifically did not contact.
		return oauth2TokenResponse{}, nil, err
	}
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
