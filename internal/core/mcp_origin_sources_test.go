package core

// Base(S, k) per kind — §4.1. Each fixture is one kind's source, and the
// negative half of each is the point: what must NOT end up in Base.

import (
	"sort"
	"testing"

	"github.com/mutexdev/lite_api/internal/transport"
	"github.com/mutexdev/lite_api/internal/types"
)

func originStrings(origins map[Origin]bool) []string {
	out := make([]string, 0, len(origins))
	for origin := range origins {
		out = append(out, origin.String())
	}
	sort.Strings(out)
	return out
}

func assertKindOrigins(t *testing.T, scope mcpScopeOrigins, kind egressKind, want ...string) {
	t.Helper()
	sort.Strings(want)
	got := originStrings(scope.perKind[kind])
	if len(got) != len(want) {
		t.Fatalf("Base(S, %s) = %v, want %v", kind, got, want)
	}
	for index := range got {
		if got[index] != want[index] {
			t.Fatalf("Base(S, %s) = %v, want %v", kind, got, want)
		}
	}
}

// The main destination is resolved with codegen.RequestURLWithParams — the send
// path's own call — so a path parameter or an enabled query row cannot change
// the URL that is dialed without changing the one that was checked.
func TestDefinitionOriginsMainKind(t *testing.T) {
	scope := mcpDefinitionOrigins(mcpDefinitionOriginsInput{
		site: testSite("req_charge"),
		effective: types.RequestItem{
			URL: "{{baseUrl}}/customers/:id/charge",
			PathParams: []types.KeyValue{
				{Name: "id", Value: "{{customerId}}", Enabled: true},
			},
			Params: []types.KeyValue{
				{Name: "trace", Value: "1", Enabled: true},
			},
		},
		vars: map[string]string{
			"baseUrl":    "https://payments.example.com:8443",
			"customerId": "cus_42",
		},
	})

	if scope.mainURL != "https://payments.example.com:8443/customers/cus_42/charge?trace=1" {
		t.Fatalf("mainURL = %q", scope.mainURL)
	}
	assertKindOrigins(t, scope, egressKindMain, "https://payments.example.com:8443")
	// redirect and script ARE the main set, materialized at construction so
	// Authorize consults exactly one set per kind.
	assertKindOrigins(t, scope, egressKindRedirect, "https://payments.example.com:8443")
	assertKindOrigins(t, scope, egressKindScript, "https://payments.example.com:8443")
	// script-dns is authorized against hostnames, since a lookup has no port
	// and no scheme.
	if !scope.dnsHosts["payments.example.com"] || len(scope.dnsHosts) != 1 {
		t.Errorf("dnsHosts = %v", scope.dnsHosts)
	}
	// baseVars is a COPY: the caller's map must not be reachable through the
	// scope, and transport construction reads this one.
	if scope.baseVars["baseUrl"] != "https://payments.example.com:8443" {
		t.Errorf("baseVars = %v", scope.baseVars)
	}
	scope.baseVars["baseUrl"] = "https://attacker.example"
	if again := mcpDefinitionOrigins(mcpDefinitionOriginsInput{
		site:      testSite("req_charge"),
		effective: types.RequestItem{URL: "{{baseUrl}}/x"},
		vars:      map[string]string{"baseUrl": "https://payments.example.com:8443"},
	}); again.baseVars["baseUrl"] != "https://payments.example.com:8443" {
		t.Error("baseVars aliased the caller's map")
	}
}

// A URL that did not resolve contributes NOTHING. Fail-closed: nothing about it
// resolved, so nothing about it was checked, so it prompts rather than passes.
func TestDefinitionOriginsSkipsUnresolvedDestinations(t *testing.T) {
	for _, rawURL := range []string{"", "   ", "{{baseUrl}}/things", "payments.example.com/charge"} {
		scope := mcpDefinitionOrigins(mcpDefinitionOriginsInput{
			site:      testSite("req_charge"),
			effective: types.RequestItem{URL: rawURL},
			vars:      map[string]string{},
		})
		if len(scope.perKind[egressKindMain]) != 0 {
			t.Errorf("URL %q contributed %v to Base", rawURL, originStrings(scope.perKind[egressKindMain]))
		}
		if len(scope.dnsHosts) != 0 {
			t.Errorf("URL %q contributed DNS hosts %v", rawURL, scope.dnsHosts)
		}
	}
}

// The token kind is the OAuth2 access and refresh endpoints, and NOT the
// authorization URL: that one is a browser navigation, every interactive grant
// is refused for MCP runs (§2 row 5), and an origin for it would authorize an
// egress that can never legitimately happen.
func TestDefinitionOriginsTokenKind(t *testing.T) {
	item := types.RequestItem{
		URL: "https://payments.example.com/charge",
		Auth: types.AuthConfig{
			Mode: "oauth2",
			OAuth2: types.OAuth2Auth{
				GrantType:        "client_credentials",
				AccessTokenURL:   "{{authBase}}/oauth/token",
				RefreshTokenURL:  "https://refresh.example.com/oauth/refresh",
				AuthorizationURL: "https://browser.example.com/authorize",
			},
		},
	}
	scope := mcpDefinitionOrigins(mcpDefinitionOriginsInput{
		site:      testSite("req_charge"),
		effective: item,
		vars:      map[string]string{"authBase": "https://login.example.com"},
	})
	assertKindOrigins(t, scope, egressKindToken, "https://login.example.com:443", "https://refresh.example.com:443")
	assertKindOrigins(t, scope, egressKindMain, "https://payments.example.com:443")
	for origin := range scope.perKind[egressKindToken] {
		if origin.Host == "browser.example.com" {
			t.Fatal("the OAuth2 authorization URL reached Base; interactive grants are refused, not authorized")
		}
	}
	// The token endpoints' hostnames are in the DNS set too — a script lookup
	// of a host the run legitimately contacts is not the boundary's business.
	for _, host := range []string{"payments.example.com", "login.example.com", "refresh.example.com"} {
		if !scope.dnsHosts[host] {
			t.Errorf("dnsHosts is missing %q: %v", host, scope.dnsHosts)
		}
	}

	// A request whose auth is not oauth2 contributes no token origins even if
	// the block is still populated — the send path would not use it either.
	item.Auth.Mode = "bearer"
	quiet := mcpDefinitionOrigins(mcpDefinitionOriginsInput{site: testSite("req_charge"), effective: item, vars: map[string]string{"authBase": "https://login.example.com"}})
	if len(quiet.perKind[egressKindToken]) != 0 {
		t.Errorf("a bearer-auth request contributed token origins: %v", originStrings(quiet.perKind[egressKindToken]))
	}
}

// The AWS kind comes through a seam until awsv4.CredentialEndpointOrigins lands
// (its owning task is a sibling of this one). What is pinned here is the wiring:
// the seam is consulted for awsv4 auth, its results become aws-kind origins, and
// it is NOT consulted for anything else.
func TestDefinitionOriginsAWSKind(t *testing.T) {
	previous := mcpAWSCredentialEndpointOrigins
	defer func() { mcpAWSCredentialEndpointOrigins = previous }()

	calls := 0
	mcpAWSCredentialEndpointOrigins = func(auth types.AWSV4Auth, vars map[string]string) []string {
		calls++
		if auth.ProfileName != "prod" || vars["region"] != "eu-west-1" {
			t.Errorf("the seam received auth %+v / vars %v", auth, vars)
		}
		return []string{
			"https://sts.eu-west-1.amazonaws.com",
			"https://portal.sso.eu-west-1.amazonaws.com",
			"not a url",
		}
	}

	item := types.RequestItem{
		URL:  "https://payments.example.com/charge",
		Auth: types.AuthConfig{Mode: "awsv4", AWSV4: types.AWSV4Auth{ProfileName: "prod"}},
	}
	scope := mcpDefinitionOrigins(mcpDefinitionOriginsInput{
		site:      testSite("req_charge"),
		effective: item,
		vars:      map[string]string{"region": "eu-west-1"},
	})
	if calls != 1 {
		t.Fatalf("the AWS seam was consulted %d times, want 1", calls)
	}
	assertKindOrigins(t, scope, egressKindAWS,
		"https://sts.eu-west-1.amazonaws.com:443", "https://portal.sso.eu-west-1.amazonaws.com:443")
	// An aws-kind origin is NOT a main-kind origin: the classes are separate,
	// so an STS endpoint cannot serve as the request's own destination.
	assertKindOrigins(t, scope, egressKindMain, "https://payments.example.com:443")

	item.Auth.Mode = "basic"
	calls = 0
	if quiet := mcpDefinitionOrigins(mcpDefinitionOriginsInput{site: testSite("req_charge"), effective: item, vars: map[string]string{}}); len(quiet.perKind[egressKindAWS]) != 0 || calls != 0 {
		t.Error("the AWS seam was consulted for a request that does not use awsv4 auth")
	}
}

// The manual proxy resolves agent-free — with the same helpers the transport
// uses, so the resolved proxy origin IS the agent-free one by construction. PAC
// is refused upstream and system mode is trusted-proxy egress, so neither
// contributes.
func TestDefinitionOriginsProxyKind(t *testing.T) {
	item := types.RequestItem{URL: "https://payments.example.com/charge"}
	vars := map[string]string{"proxyHost": "proxy.corp.example"}

	manual := mcpDefinitionOrigins(mcpDefinitionOriginsInput{
		site:      testSite("req_charge"),
		effective: item,
		vars:      vars,
		proxy: transport.Resolution{Mode: "manual", Config: types.ProxyConfig{
			Protocol: "http",
			Hostname: "{{proxyHost}}",
			Port:     "3128",
		}},
	})
	assertKindOrigins(t, manual, egressKindProxy, "http://proxy.corp.example:3128")

	// The bypass list is honoured: a request that goes direct contributes no
	// proxy origin, because it will never use one.
	bypassed := mcpDefinitionOrigins(mcpDefinitionOriginsInput{
		site:      testSite("req_charge"),
		effective: item,
		vars:      vars,
		proxy: transport.Resolution{Mode: "manual", Config: types.ProxyConfig{
			Protocol:    "http",
			Hostname:    "{{proxyHost}}",
			Port:        "3128",
			BypassProxy: "payments.example.com",
		}},
	})
	if len(bypassed.perKind[egressKindProxy]) != 0 {
		t.Errorf("a bypassed request contributed a proxy origin: %v", originStrings(bypassed.perKind[egressKindProxy]))
	}

	for _, mode := range []string{"system", "pac", "off", ""} {
		scope := mcpDefinitionOrigins(mcpDefinitionOriginsInput{
			site:      testSite("req_charge"),
			effective: item,
			vars:      vars,
			proxy:     transport.Resolution{Mode: mode, PACSource: "https://pac.corp.example/proxy.pac"},
		})
		if len(scope.perKind[egressKindProxy]) != 0 {
			t.Errorf("proxy mode %q contributed %v", mode, originStrings(scope.perKind[egressKindProxy]))
		}
	}
}

// A socks5 proxy is a plain TCP authority. 1080 is assumed when the user wrote a
// bare hostname, matching every socks client.
func TestProxyOriginHandlesSOCKS5(t *testing.T) {
	scope := mcpDefinitionOrigins(mcpDefinitionOriginsInput{
		site:      testSite("req_charge"),
		effective: types.RequestItem{URL: "https://payments.example.com/charge"},
		vars:      map[string]string{},
		proxy: transport.Resolution{Mode: "manual", Config: types.ProxyConfig{
			Protocol: "socks5",
			Hostname: "socks.corp.example",
		}},
	})
	assertKindOrigins(t, scope, egressKindProxy, "http://socks.corp.example:1080")

	withPort := mcpDefinitionOrigins(mcpDefinitionOriginsInput{
		site:      testSite("req_charge"),
		effective: types.RequestItem{URL: "https://payments.example.com/charge"},
		vars:      map[string]string{},
		proxy: transport.Resolution{Mode: "manual", Config: types.ProxyConfig{
			Protocol: "socks5",
			Hostname: "socks.corp.example",
			Port:     "9050",
		}},
	})
	assertKindOrigins(t, withPort, egressKindProxy, "http://socks.corp.example:9050")

	if origin, ok := mcpProxyOrigin(nil); ok {
		t.Errorf("a nil proxy URL produced %+v", origin)
	}
}

// A WebSocket request's handshake origin is the HTTP one: the boundary must see
// a request and its websocket sibling to the same server as ONE destination.
func TestDefinitionOriginsNormalizesWebSocketSchemes(t *testing.T) {
	scope := mcpDefinitionOrigins(mcpDefinitionOriginsInput{
		site:      testSite("req_stream"),
		effective: types.RequestItem{Type: "websocket", URL: "wss://payments.example.com/stream"},
		vars:      map[string]string{},
	})
	assertKindOrigins(t, scope, egressKindMain, "https://payments.example.com:443")
}

// The one rule that makes Base safe: it is resolved with the run's SINGLE
// agent-free variable context, never a union over environments. A run holding
// production credentials has exactly production's origins — the dev host is
// outside Base and prompts.
func TestDefinitionOriginsNeverUnionsEnvironments(t *testing.T) {
	item := types.RequestItem{URL: "{{baseUrl}}/charge"}
	production := mcpDefinitionOrigins(mcpDefinitionOriginsInput{
		site:      testSite("req_charge"),
		effective: item,
		vars:      map[string]string{"baseUrl": "https://payments.example.com"},
	})
	development := mcpDefinitionOrigins(mcpDefinitionOriginsInput{
		site:      testSite("req_charge"),
		effective: item,
		vars:      map[string]string{"baseUrl": "http://localhost:3000"},
	})
	assertKindOrigins(t, production, egressKindMain, "https://payments.example.com:443")
	assertKindOrigins(t, development, egressKindMain, "http://localhost:3000")

	policy := newMCPEgressPolicy()
	policy.SetScope(production)
	denied(t, policy.Authorize(t.Context(), mustOrigin(t, "http://localhost:3000"), egressKindMain))
}

// The session key must be byte-identical in shape to §6's persisted key, or an
// allow-once grant and an allow-and-remember grant would cover different things.
func TestSessionKeyMatchesThePersistedKeyShape(t *testing.T) {
	site := mcpDefinitionSite{
		workspacePath:        "/w",
		collectionID:         "c",
		requestID:            "r",
		environmentID:        "e",
		globalEnvironmentIDs: []string{"g1", "g2"},
	}
	origin := mustOrigin(t, "https://payments.example.com")
	want := "/w\x00c\x00r\x00e\x00g1\x1fg2\x00https://payments.example.com:443\x00request"
	if got := string(newSessionKey(site, origin, kindClassRequest)); got != want {
		t.Fatalf("session key = %q, want %q", got, want)
	}

	// Every component of the site narrows: change any one and the key changes,
	// so no approval crosses a request, an environment or a global-env list.
	base := newSessionKey(site, origin, kindClassRequest)
	for name, mutate := range map[string]func(mcpDefinitionSite) mcpDefinitionSite{
		"workspace":   func(s mcpDefinitionSite) mcpDefinitionSite { s.workspacePath = "/other"; return s },
		"collection":  func(s mcpDefinitionSite) mcpDefinitionSite { s.collectionID = "other"; return s },
		"request":     func(s mcpDefinitionSite) mcpDefinitionSite { s.requestID = "other"; return s },
		"environment": func(s mcpDefinitionSite) mcpDefinitionSite { s.environmentID = "other"; return s },
		"globals":     func(s mcpDefinitionSite) mcpDefinitionSite { s.globalEnvironmentIDs = []string{"g2", "g1"}; return s },
	} {
		if newSessionKey(mutate(site), origin, kindClassRequest) == base {
			t.Errorf("changing the %s did not change the key; that approval would cross scopes", name)
		}
	}
	// And so do the origin and the class.
	if newSessionKey(site, mustOrigin(t, "https://payments.example.com:8443"), kindClassRequest) == base {
		t.Error("a different port produced the same key")
	}
	if newSessionKey(site, origin, kindClassToken) == base {
		t.Error("a different class produced the same key")
	}
	if label := (mcpDefinitionSite{}).environmentLabel(); label != "(no environment)" {
		t.Errorf("an unset environment reads as %q", label)
	}
}
