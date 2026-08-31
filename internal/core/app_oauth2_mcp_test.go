package core

// The OAuth2 half of the MCP destination boundary (§2 row 5, §4.3 item 2,
// §5 rows 4 and 5 of the Phase 6 design).
//
// Three properties are pinned here, and each one fails in a different direction
// if it is wrong:
//
//   - A RETARGETED TOKEN ENDPOINT IS BLOCKED BEFORE THE POST. Not "the response
//     is discarded", not "the error mentions it" — the endpoint must receive
//     zero requests, because the request itself carries the client secret. Every
//     block assertion below counts requests at the listener rather than
//     inspecting the error alone.
//   - A BROWSER GRANT IS REFUSED WITHOUT OPENING ANYTHING, while the cached and
//     refreshable paths still serve an agent run silently. That second half is
//     the one a naive implementation breaks: refusing at the top of the fetch
//     would also refuse the run that has a perfectly good token sitting in the
//     cache, which is exactly the situation the refusal message tells the user
//     to create.
//   - THE OLD NAMES STILL BEHAVE AS BEFORE. They pass context.Background(), which
//     carries no policy, and a nil policy is permissive by construction. If that
//     ever stops being true, a UI send starts getting denied.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mutexdev/lite_api/internal/mcpserver"
)

// --- fixtures --------------------------------------------------------------

// oauth2TokenEndpoint is a token server that counts what reaches it. The count
// is the assertion: "blocked" means this stayed at zero.
type oauth2TokenEndpoint struct {
	server *httptest.Server
	hits   atomic.Int32
}

func newOAuth2TokenEndpoint(t *testing.T, accessToken string) *oauth2TokenEndpoint {
	t.Helper()
	endpoint := &oauth2TokenEndpoint{}
	endpoint.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := endpoint.hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"access_token":"%s-%d","refresh_token":"refresh-%d","expires_in":3600}`, accessToken, call, call)
	}))
	t.Cleanup(endpoint.server.Close)
	return endpoint
}

func (e *oauth2TokenEndpoint) url() string { return e.server.URL + "/token" }

func (e *oauth2TokenEndpoint) assertUntouched(t *testing.T, what string) {
	t.Helper()
	if hits := e.hits.Load(); hits != 0 {
		t.Fatalf("%s received %d requests; a blocked OAuth2 exchange must never reach the wire", what, hits)
	}
}

// oauth2MCPRun builds an MCP-provenanced context whose scope authorizes exactly
// the given URLs as TOKEN-class destinations, plus the audit log it wrote.
//
// No prompt callback is installed, which is the headless posture: there is
// nobody to ask, so anything outside the scope is denied rather than pending.
func oauth2MCPRun(t *testing.T, tokenURLs ...string) (context.Context, *mcpEgressPolicy, *[]string) {
	t.Helper()
	scope := mcpScopeOrigins{site: testSite("req_charge")}
	for _, raw := range tokenURLs {
		origin, ok := OriginOfURL(raw)
		if !ok {
			t.Fatalf("fixture token URL %q did not resolve to an origin", raw)
		}
		scope.add(egressKindToken, origin)
	}
	decisions := &[]string{}
	policy := newMCPEgressPolicy()
	policy.audit = func(_ mcpDefinitionSite, o Origin, k egressKind, decision string) {
		*decisions = append(*decisions, fmt.Sprintf("%s %s %s", k, o, decision))
	}
	policy.SetScope(scope)
	return mcpContextWithPolicy(context.Background(), policy), policy, decisions
}

// oauth2ClientCredentials is the non-interactive grant every checkpoint test
// uses: no browser, no callback listener, one POST.
func oauth2ClientCredentials(tokenURLTemplate string) OAuth2Auth {
	return OAuth2Auth{
		GrantType:            "client_credentials",
		AccessTokenURL:       tokenURLTemplate,
		ClientID:             "charge-client",
		ClientSecret:         "s3cr3t",
		CredentialsPlacement: "body",
		CredentialsID:        "charge-credentials",
	}
}

// seedOAuth2Cache writes straight into the process-wide token cache, the way a
// previous fetch (UI or agent — §1.4(12) says the cache is shared) would have.
func seedOAuth2Cache(t *testing.T, a *App, auth OAuth2Auth, vars map[string]string, response oauth2TokenResponse) {
	t.Helper()
	key := oauth2CacheKey(interpolateOAuth2Auth(auth, vars))
	a.oauth2Mu.Lock()
	defer a.oauth2Mu.Unlock()
	a.oauth2[key] = response
}

// noBrowserApp fails the test the moment either opener runs. §1.2(2)'s promise
// for a refused interactive grant is ZERO browser opens, and the only way to
// assert zero is to make one loud.
func noBrowserApp(t *testing.T) (*App, *atomic.Int32) {
	t.Helper()
	app := newAppForTest(t)
	opens := &atomic.Int32{}
	app.oauth2OpenURL = func(context.Context, string) error {
		opens.Add(1)
		t.Error("an agent-initiated run opened the system browser for an OAuth2 grant")
		return errors.New("browser must not open")
	}
	app.oauth2OpenInAppURL = func(context.Context, oauth2AuthorizationBrowserRequest) error {
		opens.Add(1)
		t.Error("an agent-initiated run opened the in-app browser for an OAuth2 grant")
		return errors.New("browser must not open")
	}
	app.oauth2CallbackTimeout = 2 * time.Second
	return app, opens
}

// --- the token/refresh checkpoint (§4.3 item 2, §5 row 4) ------------------

// TestMCPOAuth2TokenEndpointRetargetBlocked is the §10 "OAuth token retarget"
// row. An agent-shaped variable moves the token endpoint off the origin the
// stored definition resolves to under the run's agent-free context; the
// exchange must be refused with the attacker's listener never touched — both
// for the initial fetch and for the refresh, which resolves a URL of its own
// (RefreshTokenURL) and would otherwise be a second, unchecked door.
func TestMCPOAuth2TokenEndpointRetargetBlocked(t *testing.T) {
	t.Run("access token endpoint", func(t *testing.T) {
		legitimate := newOAuth2TokenEndpoint(t, "legit")
		attacker := newOAuth2TokenEndpoint(t, "stolen")

		auth := oauth2ClientCredentials("{{tokenHost}}/token")
		vars := map[string]string{"tokenHost": attacker.server.URL}
		ctx, _, decisions := oauth2MCPRun(t, legitimate.url())

		app := newAppForTest(t)
		_, _, err := app.fetchOAuth2TokenWithTimelineContext(ctx, auth, vars)
		denied(t, err)
		attacker.assertUntouched(t, "the retargeted token endpoint")
		legitimate.assertUntouched(t, "the in-scope token endpoint")
		if !strings.Contains(fmt.Sprint(*decisions), string(egressKindToken)) {
			t.Fatalf("the refusal was not recorded as a token-kind decision: %v", *decisions)
		}
	})

	t.Run("the same endpoint inside the scope is served", func(t *testing.T) {
		legitimate := newOAuth2TokenEndpoint(t, "legit")

		auth := oauth2ClientCredentials("{{tokenHost}}/token")
		vars := map[string]string{"tokenHost": legitimate.server.URL}
		ctx, _, decisions := oauth2MCPRun(t, legitimate.url())

		app := newAppForTest(t)
		token, _, err := app.fetchOAuth2TokenWithTimelineContext(ctx, auth, vars)
		if err != nil {
			t.Fatalf("an in-scope token exchange was refused: %v", err)
		}
		if token != "legit-1" {
			t.Fatalf("unexpected token %q", token)
		}
		if hits := legitimate.hits.Load(); hits != 1 {
			t.Fatalf("the in-scope token endpoint saw %d requests, want 1", hits)
		}
		// The kind matters as much as the allow: a token exchange authorized as
		// `main` would let a request-class approval cover it (§6).
		if got := fmt.Sprint(*decisions); !strings.Contains(got, string(egressKindToken)+" ") || !strings.Contains(got, mcpDecisionBase) {
			t.Fatalf("the exchange was not authorized as a token-kind base egress: %v", *decisions)
		}
	})

	t.Run("refresh endpoint", func(t *testing.T) {
		legitimate := newOAuth2TokenEndpoint(t, "legit")
		attacker := newOAuth2TokenEndpoint(t, "stolen")

		auth := oauth2ClientCredentials(legitimate.url())
		auth.AutoRefreshToken = true
		auth.RefreshTokenURL = "{{refreshHost}}/token"
		vars := map[string]string{"refreshHost": attacker.server.URL}

		// The scope authorizes the ACCESS token endpoint only. The refresh URL
		// is a separate resolution and gets no free ride from it.
		ctx, _, _ := oauth2MCPRun(t, legitimate.url())

		app := newAppForTest(t)
		seedOAuth2Cache(t, app, auth, vars, oauth2TokenResponse{
			AccessToken:  "expired-token",
			RefreshToken: "refresh-me",
			ExpiresAt:    time.Now().Add(-time.Minute),
		})

		_, _, err := app.fetchOAuth2TokenWithTimelineContext(ctx, auth, vars)
		denied(t, err)
		attacker.assertUntouched(t, "the retargeted refresh endpoint")
		legitimate.assertUntouched(t, "the in-scope token endpoint")
	})
}

// TestMCPOAuth2TokenCheckpointIgnoresUISends is the other half of §1.2(4): the
// very configuration refused above must sail through when no policy labels the
// context, which is what every UI send looks like today.
func TestMCPOAuth2TokenCheckpointIgnoresUISends(t *testing.T) {
	anywhere := newOAuth2TokenEndpoint(t, "ui")

	auth := oauth2ClientCredentials("{{tokenHost}}/token")
	vars := map[string]string{"tokenHost": anywhere.server.URL}

	app := newAppForTest(t)
	token, _, err := app.fetchOAuth2TokenWithTimeline(auth, vars)
	if err != nil {
		t.Fatalf("a UI send was subjected to the destination boundary: %v", err)
	}
	if token != "ui-1" || anywhere.hits.Load() != 1 {
		t.Fatalf("unexpected UI fetch: token=%q hits=%d", token, anywhere.hits.Load())
	}
}

// --- interactive grants (§2 row 5, §5 row 5) -------------------------------

// TestMCPBrowserGrantRefusedCachedTokenServes is the §10 "browser zero-open"
// row, and it is deliberately one test with three states rather than three
// tests: the whole point of the design is the RELATIONSHIP between them — the
// same request, the same grant, the same agent run, refused when nothing can
// serve it and served silently when something can.
func TestMCPBrowserGrantRefusedCachedTokenServes(t *testing.T) {
	authorizeServer := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("an agent-initiated run contacted the OAuth2 authorization endpoint")
	}))
	defer authorizeServer.Close()

	browserGrant := func(tokenURL string) OAuth2Auth {
		return OAuth2Auth{
			GrantType:            "authorization_code",
			AuthorizationURL:     authorizeServer.URL + "/authorize",
			AccessTokenURL:       tokenURL,
			CallbackURL:          "http://127.0.0.1:0/browser/callback",
			ClientID:             "charge-client",
			CredentialsPlacement: "body",
			CredentialsID:        "charge-credentials",
		}
	}

	t.Run("no cached token: refused, nothing opens", func(t *testing.T) {
		tokenEndpoint := newOAuth2TokenEndpoint(t, "browser")
		auth := browserGrant(tokenEndpoint.url())
		ctx, _, _ := oauth2MCPRun(t, tokenEndpoint.url())

		app, opens := noBrowserApp(t)
		_, _, err := app.fetchOAuth2TokenWithTimelineContext(ctx, auth, nil)
		denied(t, err)
		if opens.Load() != 0 {
			t.Fatalf("the browser opened %d times", opens.Load())
		}
		tokenEndpoint.assertUntouched(t, "the token endpoint of a refused browser grant")
		// The message has to teach the fix, or the agent retries forever.
		for _, want := range []string{"authorization_code", "browser sign-in", "LiteAPI app", "cached token"} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("the refusal does not mention %q: %v", want, err)
			}
		}
	})

	t.Run("implicit grant is refused the same way", func(t *testing.T) {
		auth := browserGrant("")
		auth.GrantType = "implicit"
		ctx, _, _ := oauth2MCPRun(t)

		app, opens := noBrowserApp(t)
		_, _, err := app.fetchOAuth2TokenWithTimelineContext(ctx, auth, nil)
		denied(t, err)
		if opens.Load() != 0 {
			t.Fatalf("the browser opened %d times", opens.Load())
		}
		if !strings.Contains(err.Error(), "implicit") {
			t.Fatalf("the refusal does not name the implicit grant: %v", err)
		}
	})

	t.Run("valid cached token: served silently", func(t *testing.T) {
		tokenEndpoint := newOAuth2TokenEndpoint(t, "browser")
		auth := browserGrant(tokenEndpoint.url())
		ctx, _, _ := oauth2MCPRun(t, tokenEndpoint.url())

		app, opens := noBrowserApp(t)
		seedOAuth2Cache(t, app, auth, nil, oauth2TokenResponse{
			AccessToken: "cached-browser-token",
			ExpiresAt:   time.Now().Add(time.Hour),
		})

		token, timeline, err := app.fetchOAuth2TokenWithTimelineContext(ctx, auth, nil)
		if err != nil {
			t.Fatalf("an agent run with a valid cached token was refused: %v", err)
		}
		if token != "cached-browser-token" {
			t.Fatalf("unexpected token %q", token)
		}
		if len(timeline) != 0 {
			t.Fatalf("serving the cache produced timeline entries: %#v", timeline)
		}
		if opens.Load() != 0 {
			t.Fatalf("the browser opened %d times", opens.Load())
		}
		tokenEndpoint.assertUntouched(t, "the token endpoint")
	})

	t.Run("expired token with a usable refresh token: refreshed", func(t *testing.T) {
		tokenEndpoint := newOAuth2TokenEndpoint(t, "refreshed")
		auth := browserGrant(tokenEndpoint.url())
		auth.AutoRefreshToken = true
		ctx, _, _ := oauth2MCPRun(t, tokenEndpoint.url())

		app, opens := noBrowserApp(t)
		seedOAuth2Cache(t, app, auth, nil, oauth2TokenResponse{
			AccessToken:  "stale-browser-token",
			RefreshToken: "refresh-me",
			ExpiresAt:    time.Now().Add(-time.Minute),
		})

		token, _, err := app.fetchOAuth2TokenWithTimelineContext(ctx, auth, nil)
		if err != nil {
			t.Fatalf("an agent run with a usable refresh token was refused: %v", err)
		}
		if token != "refreshed-1" {
			t.Fatalf("unexpected token %q", token)
		}
		if opens.Load() != 0 {
			t.Fatalf("the browser opened %d times", opens.Load())
		}
		if hits := tokenEndpoint.hits.Load(); hits != 1 {
			t.Fatalf("the refresh endpoint saw %d requests, want 1", hits)
		}
	})
}

// TestMCPOAuth2FailedRefreshKeepsItsOwnErrorAndNoBrowser pins the branch shape
// that makes the test above meaningful.
//
// The refresh branch RETURNS on failure; it does not break out and continue to
// the grant branches. If it ever did fall through, a transient 500 from the
// token endpoint would surface as "this grant needs a browser sign-in" for an
// agent and as an actual popup for the UI — in both cases hiding the real
// cause. Both provenances are checked, because the two failure modes differ.
func TestMCPOAuth2FailedRefreshKeepsItsOwnErrorAndNoBrowser(t *testing.T) {
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"server_error"}`))
	}))
	defer failing.Close()

	auth := OAuth2Auth{
		GrantType:            "authorization_code",
		AuthorizationURL:     "https://authorize.example.com/authorize",
		AccessTokenURL:       failing.URL + "/token",
		CallbackURL:          "http://127.0.0.1:0/browser/callback",
		ClientID:             "charge-client",
		CredentialsPlacement: "body",
		CredentialsID:        "charge-credentials",
		AutoRefreshToken:     true,
	}
	expired := func() oauth2TokenResponse {
		return oauth2TokenResponse{
			AccessToken:  "stale",
			RefreshToken: "refresh-me",
			ExpiresAt:    time.Now().Add(-time.Minute),
		}
	}

	t.Run("under MCP provenance", func(t *testing.T) {
		ctx, _, _ := oauth2MCPRun(t, auth.AccessTokenURL)
		app, opens := noBrowserApp(t)
		seedOAuth2Cache(t, app, auth, nil, expired())

		_, _, err := app.fetchOAuth2TokenWithTimelineContext(ctx, auth, nil)
		if err == nil {
			t.Fatal("a failing refresh returned no error")
		}
		if errors.Is(err, mcpserver.ErrDenied) {
			t.Fatalf("a failing refresh was reported as a policy refusal: %v", err)
		}
		if !strings.Contains(err.Error(), "500") {
			t.Fatalf("the refresh failure lost its own error: %v", err)
		}
		if opens.Load() != 0 {
			t.Fatalf("a failing refresh fell through to the browser (%d opens)", opens.Load())
		}
	})

	t.Run("under UI provenance", func(t *testing.T) {
		app, opens := noBrowserApp(t)
		seedOAuth2Cache(t, app, auth, nil, expired())

		_, _, err := app.fetchOAuth2TokenWithTimeline(auth, nil)
		if err == nil {
			t.Fatal("a failing refresh returned no error")
		}
		if !strings.Contains(err.Error(), "500") {
			t.Fatalf("the refresh failure lost its own error: %v", err)
		}
		if opens.Load() != 0 {
			t.Fatalf("a failing refresh fell through to the browser (%d opens)", opens.Load())
		}
	})
}

// --- the delegates (§4.5) --------------------------------------------------

// TestOAuth2LegacyNamesStillBehaveAsUISends walks every name that other tasks'
// files still call, and asserts each one reaches the network with no policy
// attached. These are one-line delegates, which is exactly why they need a
// test: a one-line delegate is the easiest thing in the world to write wrong
// and the hardest to notice, because the compiler is happy either way.
func TestOAuth2LegacyNamesStillBehaveAsUISends(t *testing.T) {
	endpoint := newOAuth2TokenEndpoint(t, "ui")
	cfg := oauth2ClientCredentials(endpoint.url())

	t.Run("requestOAuth2Token", func(t *testing.T) {
		response, err := requestOAuth2Token(cfg)
		if err != nil || response.AccessToken == "" {
			t.Fatalf("requestOAuth2Token: %v %#v", err, response)
		}
	})

	t.Run("requestOAuth2TokenWithTimeline", func(t *testing.T) {
		response, entry, err := requestOAuth2TokenWithTimeline(cfg)
		if err != nil || response.AccessToken == "" {
			t.Fatalf("requestOAuth2TokenWithTimeline: %v %#v", err, response)
		}
		if entry == nil || entry.Status != http.StatusOK {
			t.Fatalf("the delegate lost its timeline entry: %#v", entry)
		}
	})

	t.Run("requestOAuth2TokenWithGrantTimeline", func(t *testing.T) {
		response, _, err := requestOAuth2TokenWithGrantTimeline(cfg, "client_credentials", "")
		if err != nil || response.AccessToken == "" {
			t.Fatalf("requestOAuth2TokenWithGrantTimeline: %v %#v", err, response)
		}
	})

	t.Run("requestOAuth2RefreshTokenWithTimeline", func(t *testing.T) {
		response, _, err := requestOAuth2RefreshTokenWithTimeline(cfg, "refresh-me")
		if err != nil || response.AccessToken == "" {
			t.Fatalf("requestOAuth2RefreshTokenWithTimeline: %v %#v", err, response)
		}
	})

	t.Run("requestOAuth2TokenFormWithTimeline", func(t *testing.T) {
		form := url.Values{"grant_type": []string{"client_credentials"}}
		response, _, err := requestOAuth2TokenFormWithTimeline(cfg, cfg.AccessTokenURL, form, nil)
		if err != nil || response.AccessToken == "" {
			t.Fatalf("requestOAuth2TokenFormWithTimeline: %v %#v", err, response)
		}
	})

	// The code-for-token exchange the browser flow performs after the callback.
	// Its only production caller now passes a context, so this delegate's whole
	// remaining contract IS "behaves as a UI send" — which is precisely what the
	// assertion checks, and what T10 will consult before deleting it.
	t.Run("requestOAuth2AuthorizationCodeTokenWithTimeline", func(t *testing.T) {
		response, _, err := requestOAuth2AuthorizationCodeTokenWithTimeline(cfg, "auth-code", "", "http://127.0.0.1:1/callback")
		if err != nil || response.AccessToken == "" {
			t.Fatalf("requestOAuth2AuthorizationCodeTokenWithTimeline: %v %#v", err, response)
		}
	})

	t.Run("fetchOAuth2Token (package)", func(t *testing.T) {
		token, err := fetchOAuth2Token(cfg, nil)
		if err != nil || token == "" {
			t.Fatalf("fetchOAuth2Token: %v %q", err, token)
		}
	})

	t.Run("App.fetchOAuth2Token", func(t *testing.T) {
		app := newAppForTest(t)
		token, err := app.fetchOAuth2Token(cfg, nil)
		if err != nil || token == "" {
			t.Fatalf("App.fetchOAuth2Token: %v %q", err, token)
		}
	})

	t.Run("App.fetchOAuth2TokenWithTimeline", func(t *testing.T) {
		app := newAppForTest(t)
		token, timeline, err := app.fetchOAuth2TokenWithTimeline(cfg, nil)
		if err != nil || token == "" {
			t.Fatalf("App.fetchOAuth2TokenWithTimeline: %v %q", err, token)
		}
		if len(timeline) != 1 {
			t.Fatalf("the delegate lost its timeline: %#v", timeline)
		}
	})

	if hits := endpoint.hits.Load(); hits != 9 {
		t.Fatalf("the delegates made %d exchanges, want one each (9)", hits)
	}
}

// TestOAuth2BrowserDelegatesAreNotRefused proves the interactive-grant refusal
// is provenance-conditioned all the way down. Both browser entry points carry
// their own copy of the check; called through the old names they must fall
// through it and fail on their ordinary validation instead, which is what a UI
// send with an incomplete configuration has always done.
//
// (The full UI browser dance — listener, opener, callback, exchange — is
// already covered end to end by TestOAuth2BrowserGrantUsesConfiguredOpener in
// app_test.go, which calls the same delegate.)
func TestOAuth2BrowserDelegatesAreNotRefused(t *testing.T) {
	app, _ := noBrowserApp(t)

	_, _, err := app.requestOAuth2AuthorizationCodeTokenWithTimeline(OAuth2Auth{})
	if err == nil || errors.Is(err, mcpserver.ErrDenied) {
		t.Fatalf("the authorization_code delegate was treated as an agent run: %v", err)
	}
	if !strings.Contains(err.Error(), "authorization URL is required") {
		t.Fatalf("unexpected authorization_code delegate error: %v", err)
	}

	_, _, err = app.requestOAuth2ImplicitTokenWithTimeline(OAuth2Auth{})
	if err == nil || errors.Is(err, mcpserver.ErrDenied) {
		t.Fatalf("the implicit delegate was treated as an agent run: %v", err)
	}
	if !strings.Contains(err.Error(), "authorization URL is required") {
		t.Fatalf("unexpected implicit delegate error: %v", err)
	}
}
