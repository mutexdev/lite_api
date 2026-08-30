package core

// The interactive OAuth2 flows: the callback server, the waiters, and the browser handoff.
//
// Split out of app_oauth2.go by AST: declarations are identified by the parser
// and copied verbatim from their source offsets.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/mutexdev/lite_api/internal/prefs"
)

func requestOAuth2AuthorizationCodeTokenWithTimeline(cfg OAuth2Auth, code, codeVerifier, redirectURI string) (oauth2TokenResponse, *TimelineItem, error) {
	return requestOAuth2AuthorizationCodeTokenWithTimelineContext(context.Background(), cfg, code, codeVerifier, redirectURI)
}

func requestOAuth2AuthorizationCodeTokenWithTimelineContext(ctx context.Context, cfg OAuth2Auth, code, codeVerifier, redirectURI string) (oauth2TokenResponse, *TimelineItem, error) {
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
	return requestOAuth2TokenFormWithTimelineContext(ctx, cfg, cfg.AccessTokenURL, form, params)
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

// Read-only: prefs.Normalize copies its argument and returns a bool.
func (a *App) oauth2ShouldUseSystemBrowser() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return prefs.Normalize(a.state.Preferences).OAuth2UseSystemBrowser
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
	return a.requestOAuth2AuthorizationCodeTokenWithTimelineContext(context.Background(), cfg)
}

// requestOAuth2AuthorizationCodeTokenWithTimelineContext carries a SECOND copy
// of the §2 row 5 refusal, and it is not redundant. The primary refusal is at
// the grant branch in fetchOAuth2TokenWithTimelineContext, where the cached and
// refreshable paths have already had their chance; this one is the belt that
// makes the guarantee structural — the promise is ZERO BROWSER OPENS for an MCP
// run (§1.2(2)), and a future caller reaching this method directly must not be
// able to break that by forgetting the branch above. It sits before
// startOAuth2AuthorizationWaiter, so a refused run also opens no local callback
// listener.
func (a *App) requestOAuth2AuthorizationCodeTokenWithTimelineContext(ctx context.Context, cfg OAuth2Auth) (oauth2TokenResponse, []TimelineItem, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := oauth2InteractiveGrantRefusal(ctx, "authorization_code"); err != nil {
		return oauth2TokenResponse{}, nil, err
	}
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
	// waitCtx, NOT a reassignment of ctx. `ctx, cancel := ...` would rebind the
	// parameter (a short declaration redeclares a name already in the function's
	// own block), and the token exchange below would then run under a context
	// derived from a.ctx — stripping the provenance the caller attached. The
	// callback wait keeps its original base exactly as before.
	waitCtx, cancel := context.WithTimeout(baseCtx, timeout)
	defer cancel()
	callback, err := waiter.Receive(waitCtx)
	if err != nil {
		return oauth2TokenResponse{}, nil, err
	}
	response, tokenEntry, err := requestOAuth2AuthorizationCodeTokenWithTimelineContext(ctx, cfg, callback.Code, codeVerifier, waiter.CallbackURL)
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
	return a.requestOAuth2ImplicitTokenWithTimelineContext(context.Background(), cfg)
}

// requestOAuth2ImplicitTokenWithTimelineContext refuses under MCP provenance for
// the same reason and in the same place as its authorization_code sibling above.
func (a *App) requestOAuth2ImplicitTokenWithTimelineContext(ctx context.Context, cfg OAuth2Auth) (oauth2TokenResponse, []TimelineItem, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := oauth2InteractiveGrantRefusal(ctx, "implicit"); err != nil {
		return oauth2TokenResponse{}, nil, err
	}
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
	// waitCtx rather than a rebound ctx, for the reason spelled out in the
	// authorization_code method above.
	waitCtx, cancel := context.WithTimeout(baseCtx, timeout)
	defer cancel()
	callback, err := waiter.Receive(waitCtx)
	if err != nil {
		return oauth2TokenResponse{}, nil, err
	}
	response, err := oauth2ImplicitTokenResponse(callback.Tokens, cfg)
	if err != nil {
		return oauth2TokenResponse{}, callback.Timeline, err
	}
	return response, callback.Timeline, nil
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
