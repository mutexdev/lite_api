package core

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptrace"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Azure/go-ntlmssp"

	"github.com/mutexdev/lite_api/internal/auth/digest"
	"github.com/mutexdev/lite_api/internal/codegen"
	"github.com/mutexdev/lite_api/internal/prefs"
	"github.com/mutexdev/lite_api/internal/responsestore"
	"github.com/mutexdev/lite_api/internal/scripting"
	"github.com/mutexdev/lite_api/internal/transport"
)

// executeHTTP must be called WITHOUT a.mu held.
//
// It takes no lock in its own body; its helpers take their own — for instance
// appTLSSettingsSnapshot does RLock/RUnlock and then Lock, and
// collectionProxyResolution does RLock/defer RUnlock. Calling it under a.mu
// therefore self-deadlocks on the first helper that needs the write lock.
//
// Nothing in the name says so: there is no Locked suffix to carry the
// convention, and until this file existed the invariant was expressed only by
// the fact that the two callers sat a few hundred lines above. They are now
// ~3,000 lines away in app.go (sendRequestWithControlsContext and
// runScriptedCollectionRequest), so the invariant is written down instead.
func (a *App) executeHTTP(ctx context.Context, collectionID string, collection Collection, item RequestItem, vars map[string]string, onFailState *scripting.RequestState, recordTimeline func(TimelineItem)) Response {
	if ctx == nil {
		ctx = context.Background()
	}
	start := time.Now()
	result := Response{SentAt: start, Headers: map[string]string{}, PreviewMode: "auto"}
	if item.Type == "websocket" {
		// The context form (§5 row 10). It carries the send's provenance to the
		// pre-handshake checkpoint and makes the handshake itself cancellable;
		// the context-free delegate now has no engine caller left.
		return a.executeWebSocketContext(ctx, collectionID, item, vars)
	}
	if item.Type == "grpc" {
		response := a.executeGRPC(ctx, collection, item, vars)
		if requestContextCancelled(ctx) {
			markRequestCancelled(&response)
		}
		return response
	}
	if item.Type != "http" && item.Type != "graphql" {
		result.Error = item.Type + " execution is registered for UI parity but not wired to a protocol client yet"
		result.DurationMs = time.Since(start).Milliseconds()
		return result
	}

	targetURL := codegen.RequestURLWithParams(item.URL, item.Params, item.PathParams, vars)
	if item.Settings.EncodeURL {
		targetURL = scripting.EncodeRequestURL(targetURL)
	}
	result.RequestedURL = targetURL
	method := strings.ToUpper(item.Method)
	if method == "" {
		method = http.MethodGet
	}
	bodyReader, contentType, err := buildBody(item.Body, vars, collection.Path)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	if item.Type == "graphql" && bodyReader == nil {
		method = http.MethodPost
		bodyReader = strings.NewReader(graphQLRequestPayload(item.Body, vars))
		contentType = "application/json"
	}
	req, err := http.NewRequestWithContext(ctx, method, targetURL, bodyReader)
	if err != nil {
		result.Error = err.Error()
		result.DurationMs = time.Since(start).Milliseconds()
		return result
	}
	timingTrace := responsestore.NewTimingTrace(start)
	req = req.WithContext(httptrace.WithClientTrace(req.Context(), timingTrace.Trace()))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	for _, header := range item.Headers {
		if header.Enabled && header.Name != "" {
			req.Header.Set(interpolate(header.Name, vars), interpolate(header.Value, vars))
		}
	}
	if err := a.applyAuth(ctx, req, collection.Path, &item, vars, recordTimeline); err != nil {
		result.Error = err.Error()
		result.DurationMs = time.Since(start).Milliseconds()
		return result
	}

	client := *a.httpClient
	redirectCookies := scripting.NewScriptCookieJar(nil)
	baseTransport := client.Transport
	if baseTransport == nil {
		baseTransport = http.DefaultTransport
	}
	tlsSettings := a.appTLSSettingsSnapshot()
	verifyTLS := requestTLSVerificationEnabled(tlsSettings.Request, item.Settings.VerifyTLS)
	// US-016: one shared transport per security posture, so sequential sends
	// reuse the connection instead of handshaking into a fresh empty pool.
	//
	// Under MCP provenance this is also where §2's transport-level refusals fire
	// — a PAC proxy disposition, or a client certificate that would meet an
	// https proxy — and where the certificate confinement comes back.
	var transportErr error
	var certOrigin Origin
	baseTransport, certOrigin, transportErr = a.requestTransport(ctx, baseTransport, tlsSettings, verifyTLS, collectionID, targetURL, vars)
	if transportErr != nil {
		result.Error = transportErr.Error()
		result.DurationMs = time.Since(start).Milliseconds()
		return result
	}
	// The backstop, innermost so it sees NTLM's own round trips as well as the
	// redirect hops and the digest retry (§4.3 item 3).
	baseTransport = mcpSendTransport(baseTransport, certOrigin)
	if strings.EqualFold(item.Auth.Mode, "ntlm") {
		baseTransport = ntlmssp.Negotiator{RoundTripper: baseTransport}
	}
	client.Transport = cookieCapturingTransport{base: baseTransport, jar: redirectCookies}
	timeout := requestTimeoutMilliseconds(item.Settings.TimeoutMs, tlsSettings.Request)
	client.Timeout = time.Duration(timeout) * time.Millisecond
	if !item.Settings.FollowRedirects {
		client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
			timingTrace.Redirect()
			return http.ErrUseLastResponse
		}
	} else if item.Settings.MaxRedirects > 0 {
		max := item.Settings.MaxRedirects
		client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
			timingTrace.Redirect()
			if len(via) >= max {
				return http.ErrUseLastResponse
			}
			attachCookiesToHTTPRequest(req, redirectCookies.Snapshot())
			return nil
		}
	} else {
		client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
			timingTrace.Redirect()
			attachCookiesToHTTPRequest(req, redirectCookies.Snapshot())
			return nil
		}
	}
	// THE MAIN CHECKPOINT (§4.3 item 2, §5 row 1). Immediately before the send,
	// against the URL that is about to be dialed rather than the one the
	// definition spelled — the two differ whenever a variable resolved, which is
	// the whole reason the check exists.
	//
	// Blocking, so it can prompt: nothing here runs inside client.Timeout yet,
	// unlike the transport backstop below it. A nil policy means a UI send and
	// Authorize returns nil without doing anything (§1.2(4)).
	if policy := mcpPolicyFromContext(ctx); policy != nil {
		// An unresolvable URL yields the zero Origin, which Authorize denies
		// with its own "did not resolve" wording: an egress whose destination
		// could not be determined is one that was not checked.
		origin, _ := OriginOfURL(targetURL)
		if err := policy.Authorize(ctx, origin, egressKindMain); err != nil {
			result.Error = err.Error()
			result.DurationMs = time.Since(start).Milliseconds()
			result.Timings = timingTrace.Finalize(time.Now())
			return result
		}
	}
	res, err := client.Do(req)
	if err != nil {
		if requestContextCancelled(ctx) {
			markRequestCancelled(&result)
		} else {
			result.Error = requestFailureMessage(err, targetURL)
		}
		if onFailErr := scripting.RunRequestOnFail(onFailState, err); onFailErr != nil {
			result.Error = result.Error + "; onFail: " + onFailErr.Error()
		}
		result.DurationMs = time.Since(start).Milliseconds()
		result.Timings = timingTrace.Finalize(time.Now())
		return result
	}
	if digest.ShouldRetry(res, item.Auth) {
		challenge := res.Header.Get("WWW-Authenticate")
		_ = res.Body.Close()
		retryReq, retryErr := digest.CloneRequest(req, item.Auth, vars, challenge)
		if retryErr != nil {
			result.Error = retryErr.Error()
			result.DurationMs = time.Since(start).Milliseconds()
			result.Timings = timingTrace.Finalize(time.Now())
			return result
		}
		attachCookiesToHTTPRequest(retryReq, redirectCookies.Snapshot())
		res, err = client.Do(retryReq)
		if err != nil {
			if requestContextCancelled(ctx) {
				markRequestCancelled(&result)
			} else {
				result.Error = requestFailureMessage(err, targetURL)
			}
			if onFailErr := scripting.RunRequestOnFail(onFailState, err); onFailErr != nil {
				result.Error = result.Error + "; onFail: " + onFailErr.Error()
			}
			result.DurationMs = time.Since(start).Milliseconds()
			result.Timings = timingTrace.Finalize(time.Now())
			return result
		}
	}
	defer func() { _ = res.Body.Close() }()
	// US-011. Bounded, not io.ReadAll: the size of this allocation is chosen by
	// the remote server, so an endpoint streaming gigabytes would otherwise take
	// the process down with it.
	body, bodyTruncated, err := readResponseBodyLimited(res.Body, responseBodyReadLimit(a.appTLSSettingsSnapshot().Request))
	if err != nil {
		if requestContextCancelled(ctx) {
			markRequestCancelled(&result)
		} else {
			result.Error = err.Error()
		}
	}
	result.Status = res.StatusCode
	result.StatusText = res.Status
	if result.Cancelled {
		result.StatusText = "Cancelled"
	}
	result.Size = len(body)
	result.Body = string(body)
	// US-011: truncation must be visible. The header is what the response
	// inspector can surface without any new plumbing, and it travels with the
	// response into saved examples and exports, so a truncated body is never
	// mistaken for a complete one later.
	if bodyTruncated {
		if result.Headers == nil {
			result.Headers = map[string]string{}
		}
		result.Headers["x-liteapi-body-truncated"] = "true"
		result.Headers["x-liteapi-body-limit"] = strconv.FormatInt(responseBodyReadLimit(a.appTLSSettingsSnapshot().Request), 10)
	}
	result.BodyBase64 = base64.StdEncoding.EncodeToString(body)
	result.DurationMs = time.Since(start).Milliseconds()
	result.Timings = timingTrace.Finalize(time.Now())
	for name, values := range res.Header {
		result.Headers[name] = strings.Join(values, ", ")
		for _, value := range values {
			result.HeaderEntries = append(result.HeaderEntries, KeyValue{Name: name, Value: value, Enabled: true})
		}
	}
	result.Cookies = redirectCookies.Snapshot()
	result.PreviewMode = previewModeFromHeaders(result.Headers)
	result.Assertions = evaluateAssertions(item.Assertions, result)
	return result
}

type cookieCapturingTransport struct {
	base http.RoundTripper
	jar  *scripting.CookieJar
}

type appTLSSettings struct {
	Request            RequestPreferences
	ClientSessionCache tls.ClientSessionCache
}

// appTLSSettingsSnapshot runs once per outbound request, so it is on the
// hot path of a parallel collection run. Everything it needs is read-only
// except the lazy construction of the shared TLS session cache, so it uses
// double-checked locking: the fast path reads under RLock and returns; only
// the first call (or the first after ClearSSLSessionCache) drops to the write
// lock. The slow path re-reads preferences and re-tests tlsSessionCache
// because the lock is released between the two sections and another goroutine
// may have created the cache — or disabled it — in the gap.
func (a *App) appTLSSettingsSnapshot() appTLSSettings {
	a.mu.RLock()
	preferences := prefs.Normalize(a.state.Preferences)
	if !preferences.Cache.SSLSession.Enabled {
		a.mu.RUnlock()
		return appTLSSettings{Request: preferences.Request}
	}
	if cache := a.tlsSessionCache; cache != nil {
		a.mu.RUnlock()
		return appTLSSettings{Request: preferences.Request, ClientSessionCache: cache}
	}
	a.mu.RUnlock()

	a.mu.Lock()
	defer a.mu.Unlock()
	preferences = prefs.Normalize(a.state.Preferences)
	var clientSessionCache tls.ClientSessionCache
	if preferences.Cache.SSLSession.Enabled {
		if a.tlsSessionCache == nil {
			a.tlsSessionCache = tls.NewLRUClientSessionCache(100)
		}
		clientSessionCache = a.tlsSessionCache
	}
	return appTLSSettings{
		Request:            preferences.Request,
		ClientSessionCache: clientSessionCache,
	}
}

// requestFailureMessage renders a send failure for the response pane. Only a
// certificate failure is rewritten (US-059) and the PAC refusal restated;
// everything else keeps the wording the transport produced.
//
// THE PAC CASE IS A TRANSLATION, not a second policy decision. The cert-free
// system-proxy closure refuses inside http.Transport's proxy func, so what
// comes back here is transport.ErrSystemPACRefused inside a *url.Error inside
// whatever the client wrapped it in — a sentence about a PAC script with a
// dialer's prefix stapled to the front. internal/transport cannot phrase the
// refusal because it does not know an app exists; this restates it as §2 row 4,
// identically to the two places that refuse before a transport is even built.
func requestFailureMessage(err error, targetURL string) string {
	if errors.Is(err, transport.ErrSystemPACRefused) {
		return mcpPACProxyRefusal().Error()
	}
	if message, ok := describeTLSFailure(err, targetURL); ok {
		return message
	}
	return err.Error()
}

func requestTLSVerificationEnabled(preferences RequestPreferences, requestVerifyTLS bool) bool {
	return requestVerifyTLS && prefs.BoolPtrValue(preferences.SSLVerification, true)
}

func requestTimeoutMilliseconds(itemTimeout int, preferences RequestPreferences) int {
	if preferences.Timeout > 0 {
		return preferences.Timeout
	}
	if itemTimeout > 0 {
		return itemTimeout
	}
	return 30000
}

func transportWithAppTLSSettings(base http.RoundTripper, settings appTLSSettings, verifyTLS bool) (http.RoundTripper, error) {
	// Keep the caller's shared transport for the normal verified path so HTTP
	// keep-alive reuse remains observable by httptrace.
	if verifyTLS && !settings.Request.CustomCaCertificate.Enabled {
		return base, nil
	}
	transport := transport.CloneHTTPTransport(base)
	tlsConfig := transport.TLSClientConfig
	if tlsConfig == nil {
		tlsConfig = &tls.Config{}
	} else {
		tlsConfig = tlsConfig.Clone()
	}
	if !verifyTLS {
		tlsConfig.InsecureSkipVerify = true
	} else if err := applyCustomRootCAsToTLSConfig(tlsConfig, settings.Request); err != nil {
		return nil, err
	}
	if settings.ClientSessionCache != nil {
		tlsConfig.ClientSessionCache = settings.ClientSessionCache
	}
	transport.TLSClientConfig = tlsConfig
	return transport, nil
}

func applyCustomRootCAsToTLSConfig(tlsConfig *tls.Config, preferences RequestPreferences) error {
	if tlsConfig == nil || !preferences.CustomCaCertificate.Enabled {
		return nil
	}
	filePath := strings.TrimSpace(preferences.CustomCaCertificate.FilePath)
	if filePath == "" {
		return nil
	}
	certPEM, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("read custom CA certificate: %w", err)
	}
	var roots *x509.CertPool
	if prefs.BoolPtrValue(preferences.KeepDefaultCaCertificates.Enabled, true) {
		roots, err = x509.SystemCertPool()
		if err != nil {
			return fmt.Errorf("load system CA certificates: %w", err)
		}
	}
	if roots == nil {
		roots = x509.NewCertPool()
	}
	if !roots.AppendCertsFromPEM(certPEM) {
		return fmt.Errorf("custom CA certificate %q did not contain PEM certificates", filePath)
	}
	tlsConfig.RootCAs = roots
	return nil
}

func tlsSessionPreferencesChanged(previous, next Preferences) bool {
	previous = prefs.Normalize(previous)
	next = prefs.Normalize(next)
	if previous.Cache.SSLSession.Enabled != next.Cache.SSLSession.Enabled {
		return true
	}
	if prefs.BoolPtrValue(previous.Request.SSLVerification, true) != prefs.BoolPtrValue(next.Request.SSLVerification, true) {
		return true
	}
	if previous.Request.CustomCaCertificate != next.Request.CustomCaCertificate {
		return true
	}
	if prefs.BoolPtrValue(previous.Request.KeepDefaultCaCertificates.Enabled, true) != prefs.BoolPtrValue(next.Request.KeepDefaultCaCertificates.Enabled, true) {
		return true
	}
	return false
}

// proxyResolution moved to internal/transport with the code that consumes it.
type proxyResolution = transport.Resolution

// Read-only: findCollectionLocked only walks state, and every normalize*
// helper below takes its argument by value and returns a new value. The
// returned proxyResolution holds ProxyConfig, whose fields are all strings and
// bools, so nothing in the result aliases live state.
func (a *App) collectionProxyResolution(collectionID string) proxyResolution {
	a.mu.RLock()
	defer a.mu.RUnlock()
	collection, err := a.findCollectionLocked(collectionID)
	if err != nil {
		return proxyResolution{Mode: "off"}
	}
	proxy := transport.NormalizeProxyConfig(collection.Proxy)
	if proxy.Disabled {
		return proxyResolution{Mode: "off"}
	}
	if !proxy.Inherit && !transport.ProxyConfigUnset(collection.Proxy) {
		return proxyResolution{Mode: "manual", Config: proxy}
	}
	preferences := prefs.NormalizeProxy(a.state.Preferences.Proxy, a.state.Preferences.ProxyMode)
	if preferences.Disabled {
		return proxyResolution{Mode: "off"}
	}
	switch preferences.Source {
	case "manual":
		return proxyResolution{Mode: "manual", Config: transport.NormalizeProxyConfig(preferences.Config)}
	case "pac":
		return proxyResolution{Mode: "pac", PACSource: preferences.PAC.Source}
	default:
		return proxyResolution{Mode: "system"}
	}
}

// Read-only: the returned slice is freshly allocated and
// ClientCertificateConfig contains only strings, so the copy is deep.
func (a *App) collectionClientCertificateConfig(collectionID string) (string, []ClientCertificateConfig, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	collection, err := a.findCollectionLocked(collectionID)
	if err != nil || len(collection.ClientCertificates) == 0 {
		return "", nil, false
	}
	return collection.Path, append([]ClientCertificateConfig(nil), collection.ClientCertificates...), true
}
