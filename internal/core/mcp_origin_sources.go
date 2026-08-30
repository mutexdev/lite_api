package core

// Base(S, k) — where a definition scope's authority comes from (§4.1, §1.1).
//
// THE RULE THAT MAKES THIS SAFE IS WHAT IS *NOT* HERE. Base is resolved with the
// run's SINGLE agent-free variable context: the exact
// scripting.NewScriptVariableContext construction mcpRunPlan performs, holding
// the selected collection environment and the currently active global
// environments, with
//
//   - no run overrides,
//   - no flow inputs,
//   - no flow-extracted values,
//   - no execution-overlay values (§3 makes that one structural rather than a
//     rule anyone has to remember: the overlay is a policy field that never
//     reaches AppState, and Base derives only from AppState reads).
//
// AND IT IS NEVER A UNION OVER ENVIRONMENTS. This is the deliberate divergence
// from the shipped host guard, which resolves each candidate request under every
// environment its collection defines (mcp_guard.go's mcpKnownHostsForSecret).
// That widening is right for "which hosts does this credential already serve";
// it is wrong here. A run holding PRODUCTION credentials has exactly
// production's origins, and a dev-only origin prompts — because sending
// production's credential to the dev host is precisely the mistake the boundary
// exists to catch, and unioning the environments would authorize it silently.
//
// AN UNRESOLVED DESTINATION CONTRIBUTES NOTHING. A URL still carrying
// {{templates}}, or one with no scheme, yields no origin — so it is not in Base,
// so it prompts. Fail-closed: nothing about it resolved, so nothing about it was
// checked.

import (
	"net/url"
	"strconv"
	"strings"

	"github.com/mutexdev/lite_api/internal/auth/awsv4"
	"github.com/mutexdev/lite_api/internal/codegen"
	"github.com/mutexdev/lite_api/internal/interp"
	"github.com/mutexdev/lite_api/internal/transport"
	"github.com/mutexdev/lite_api/internal/types"
)

// mcpDefinitionOriginsInput is everything one definition scope contributes.
//
// A PURE FUNCTION WITH AN EXPLICIT INPUT, not a method on *App, and that is a
// deliberate reading of "call the existing helpers, don't duplicate them": the
// two things this needs from the App — the effective request and the proxy
// resolution — are produced by helpers the CALLER already runs under the state
// lock (scripting.EffectiveRequest at the mcpRunPlan copy point, and
// a.collectionProxyResolution, which takes a.mu.RLock and therefore must not be
// called from anywhere holding it). Taking them as inputs keeps the locking
// decision at the call site, where the rest of mcpRunPlan's locking already
// lives, and leaves this testable without an App.
type mcpDefinitionOriginsInput struct {
	site mcpDefinitionSite
	// effective is scripting.EffectiveRequest's output for the request whose
	// send this scope governs — folder/collection headers and inherited auth
	// already merged, exactly as the send path sees it.
	effective types.RequestItem
	// vars is the run's single agent-free variable context (VariableContext's
	// Combined). Nothing agent-supplied may be in it; that is the caller's
	// contract and the whole basis of the property.
	vars map[string]string
	// proxy is the collection's agent-free proxy resolution
	// (a.collectionProxyResolution). Mode "manual" contributes an origin; "pac"
	// is refused upstream (§2 row 4) and contributes nothing; "system" and "off"
	// are trusted-proxy egress (§1.1) and contribute nothing.
	proxy transport.Resolution
}

// mcpAWSCredentialEndpointOrigins resolves the AWS credential-resolution
// endpoints an awsv4-authenticated request would contact (STS AssumeRole /
// WebIdentity, SSO GetRoleCredentials, SSO-OIDC refresh).
//
// STILL A SEAM, now pointed at the real resolver. §4.1 names
// awsv4.CredentialEndpointOrigins as the source; it landed in a sibling Wave-1
// task, so this variable started as a fail-closed stub (no origins, therefore
// every AWS endpoint prompts) and the send-path task bound it. It stays a
// variable so tests can substitute a fixture without an ~/.aws directory.
//
// Reimplementing the endpoint rules here was never an option: the resolution
// reads ~/.aws config sections, honours per-profile endpoint overrides and
// regional defaults, and walks source_profile chains — a second copy would
// drift from the one that actually dials, and the drift would show up as an
// origin that was authorized but never contacted, or one contacted but never
// authorized.
//
// The origins come back IN CONTACT ORDER and canonical (scheme://host[:port]),
// so OriginOfURL parses them directly. A static-keys or environment-credentials
// auth yields none, which is right: it makes no network call at all.
var mcpAWSCredentialEndpointOrigins = func(auth types.AWSV4Auth, vars map[string]string) []string {
	return awsv4.CredentialEndpointOrigins(auth, func(value string) string {
		return interp.Interpolate(value, vars)
	})
}

// mcpDefinitionOrigins resolves one definition scope's per-kind authority.
func mcpDefinitionOrigins(in mcpDefinitionOriginsInput) mcpScopeOrigins {
	scope := mcpScopeOrigins{
		site:     in.site,
		perKind:  map[egressKind]map[Origin]bool{},
		dnsHosts: map[string]bool{},
		baseVars: mcpCopyVars(in.vars),
	}

	// main — the request URL resolved under the one agent-free context, built
	// the way executeHTTP builds it (path params substituted, enabled query
	// params appended) so "what was checked" and "what is dialed" come from the
	// same function.
	scope.mainURL = mcpAgentFreeMainURL(in.effective, in.vars)
	if origin, ok := OriginOfURL(scope.mainURL); ok {
		scope.add(egressKindMain, origin)
	}

	// token — the effective OAuth2 access and refresh endpoints. The
	// AUTHORIZATION URL IS NOT HERE: it is a browser navigation, and every
	// interactive grant is refused for MCP runs (§2 row 5), so an origin for it
	// would authorize an egress that can never legitimately happen.
	if strings.EqualFold(strings.TrimSpace(in.effective.Auth.Mode), "oauth2") {
		oauth := in.effective.Auth.OAuth2
		for _, raw := range []string{oauth.AccessTokenURL, oauth.RefreshTokenURL} {
			if origin, ok := OriginOfURL(interp.Interpolate(raw, in.vars)); ok {
				scope.add(egressKindToken, origin)
			}
		}
	}

	// aws — the credential-resolution endpoints, through the seam above.
	if strings.EqualFold(strings.TrimSpace(in.effective.Auth.Mode), "awsv4") {
		for _, raw := range mcpAWSCredentialEndpointOrigins(in.effective.Auth.AWSV4, in.vars) {
			if origin, ok := OriginOfURL(raw); ok {
				scope.add(egressKindAWS, origin)
			}
		}
	}

	// proxy — the manual proxy, resolved agent-free. By construction the
	// resolved proxy origin IS the agent-free one, so this is not so much an
	// allowance as a record of what the user configured.
	if origin, ok := mcpManualProxyOrigin(in.proxy, scope.mainURL, in.vars); ok {
		scope.add(egressKindProxy, origin)
	}

	// redirect and script are the scope's MAIN set, copied at construction
	// rather than unioned at lookup so Authorize consults exactly one set
	// (§4.1). Same-origin totality (§1.4(2)) is what makes this the right
	// shape: an origin the request already reaches may be reached again by a
	// redirect or by a script send to the same place.
	for origin := range scope.perKind[egressKindMain] {
		scope.add(egressKindRedirect, origin)
		scope.add(egressKindScript, origin)
	}

	return scope
}

// add records one origin under one kind and remembers its hostname for the DNS
// shim.
func (s *mcpScopeOrigins) add(k egressKind, o Origin) {
	if !o.valid() {
		return
	}
	if s.perKind == nil {
		s.perKind = map[egressKind]map[Origin]bool{}
	}
	if s.perKind[k] == nil {
		s.perKind[k] = map[Origin]bool{}
	}
	s.perKind[k][o] = true
	if s.dnsHosts == nil {
		s.dnsHosts = map[string]bool{}
	}
	// script-dns is authorized against HOSTNAMES: a lookup has no port and no
	// scheme, so the set is every host any kind's origin names (§1.1).
	s.dnsHosts[o.Host] = true
}

// mcpAgentFreeMainURL resolves the request's destination with the agent-free
// variable context.
//
// codegen.RequestURLWithParams is executeHTTP's own call (app_execute_http.go:60)
// rather than a lookalike, so a path parameter or an enabled query row cannot
// change the checked URL without changing the sent one. EncodeRequestURL is
// deliberately NOT applied: percent-encoding the path cannot change the
// authority, and this value is used for origin identity and client-certificate
// matching, not as a wire URL.
func mcpAgentFreeMainURL(effective types.RequestItem, vars map[string]string) string {
	raw := strings.TrimSpace(effective.URL)
	if raw == "" {
		return ""
	}
	return strings.TrimSpace(codegen.RequestURLWithParams(raw, effective.Params, effective.PathParams, vars))
}

// mcpManualProxyOrigin resolves the user's manual proxy to an origin.
//
// The bypass list is honoured (transport.ShouldUseManualProxy is the same check
// ApplyProxyResolution makes), so a request that would go direct does not
// contribute a proxy origin it will never use. Only "manual" resolves here:
// "pac" is refused upstream before any fetch or evaluation, and "system"/"off"
// are trusted-proxy egress that no approval keys on.
func mcpManualProxyOrigin(resolution transport.Resolution, requestURL string, vars map[string]string) (Origin, bool) {
	if !strings.EqualFold(strings.TrimSpace(resolution.Mode), "manual") {
		return Origin{}, false
	}
	if requestURL != "" && !transport.ShouldUseManualProxy(requestURL, interp.Interpolate(resolution.Config.BypassProxy, vars)) {
		return Origin{}, false
	}
	proxyURL, err := transport.ManualProxyURL(resolution.Config, vars)
	if err != nil || proxyURL == nil {
		return Origin{}, false
	}
	return mcpProxyOrigin(proxyURL)
}

// mcpProxyOrigin is OriginOfURL's proxy-flavoured sibling.
//
// It exists because a proxy URL can be socks5, which OriginOfURL rightly
// rejects: §1.1's Origin grammar is about HTTP-family DESTINATIONS, and quietly
// teaching it a fourth scheme would let a socks5 URL slip into a request-class
// comparison. A proxy is a different thing being authorized under a different
// kind, so it gets its own narrow parser here.
func mcpProxyOrigin(proxyURL *url.URL) (Origin, bool) {
	if proxyURL == nil {
		return Origin{}, false
	}
	if !strings.EqualFold(strings.TrimSpace(proxyURL.Scheme), "socks5") {
		return originOfParsedURL(proxyURL)
	}
	// A socks5 proxy is recorded as the plain TCP authority it is — scheme
	// "http" — with 1080 when the user wrote a bare hostname. Collapsing socks5
	// and http onto one scheme cannot widen anything: proxy-kind origins have no
	// approval path at all (§1.1), so the set holds exactly the one authority
	// the user configured and the comparison is against that same resolution.
	port := 1080
	if written := proxyURL.Port(); written != "" {
		parsed, err := strconv.Atoi(written)
		if err != nil {
			return Origin{}, false
		}
		port = parsed
	}
	return newOrigin("http", proxyURL.Hostname(), port)
}

// mcpCopyVars copies the agent-free variable map so a scope's baseVars cannot be
// mutated by whatever else holds the original.
func mcpCopyVars(vars map[string]string) map[string]string {
	if len(vars) == 0 {
		return map[string]string{}
	}
	out := make(map[string]string, len(vars))
	for name, value := range vars {
		out[name] = value
	}
	return out
}
