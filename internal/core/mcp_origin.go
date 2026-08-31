package core

// Origin identity for the MCP destination boundary — §1.1 of the Phase 6 design.
//
// WHY A NEW IDENTITY RATHER THAN THE HOST GUARD'S. The guard this replaced
// (retired with mcp_guard.go's host half) deliberately dropped the port and the
// scheme: it reasoned about "which operator holds this credential", and
// api.example.com:443 and api.example.com:8443 are the same operator. That was
// the right call for a per-secret allowlist learned from a workspace, and it is
// the wrong call for a destination boundary. Two consequences the old shape
// could not express:
//
//   - LOCALHOST IS NOT ONE PLACE. :3000 and :8080 on a developer's machine are
//     two unrelated services, frequently with different owners and different
//     trust. A boundary that cannot tell them apart authorizes the second the
//     moment the user approves the first (§1.4(9)).
//   - A SCHEME DOWNGRADE IS A DIFFERENT DESTINATION. https://api.example.com and
//     http://api.example.com differ in whether anyone on the path can read the
//     credential. Treating them as one origin would let an approval for the safe
//     one authorize the unsafe one.
//
// So an Origin is (scheme, lowercased host, EFFECTIVE port) — the port always
// present, never implicit, so that "no port written down" and "the default port
// written down" are the same origin and nothing else is.
//
// NORMALIZATION IS THE SECURITY-RELEVANT HALF. Every spelling of one destination
// must reduce to one Origin, or an approval for the spelling the user read would
// not cover the spelling the engine dials — and, worse, a check against the
// spelling the engine dials would not be covered by the definition's spelling.
// Three rules do that work:
//
//   - ws -> http and wss -> https. A WebSocket handshake IS an HTTP request; the
//     scheme is a client-API distinction, not a destination distinction, and
//     keeping them separate would mean a request and its own websocket sibling
//     to the same server were different origins.
//   - Default ports are materialized (http/ws 80, https/wss 443).
//   - IPv6 literals go through net.ParseIP, so [::0001] and [::1] and
//     [0:0:0:0:0:0:0:1] are one host. Brackets are stripped in the Host field and
//     re-applied by String(), so the stored form is canonical and the rendered
//     form is dialable.
//
// GRPC ORIGINS ARE NOT PRODUCED HERE. The gRPC effective port is 443 for
// plaintext and TLS alike (grpc-go's DNS-resolver default), which is a different
// rule from the HTTP 80/443 scheme defaults — so OriginOfURL, which implements
// the HTTP rule, must never see a gRPC target. §4.7's validator owns that
// grammar and calls newOrigin directly with the port it computed. The TYPE
// represents gRPC origins fine: plaintext gRPC is scheme http, TLS gRPC is
// scheme https, per §1.1.

import (
	"net"
	"net/url"
	"strconv"
	"strings"
)

// Origin is (scheme, lowercased host, effective port): the unit the destination
// boundary authorizes. The port is always resolved — there is no "unspecified"
// Origin — so equality is plain struct equality and the type is a valid map key.
type Origin struct {
	// Scheme is "http" or "https" only. ws/wss are normalized into their HTTP
	// equivalents at construction; a plaintext gRPC channel is "http" and a TLS
	// one is "https".
	Scheme string
	// Host is lowercased, never bracketed, and canonicalized through net.ParseIP
	// when it is an IP literal.
	Host string
	// Port is the effective port: the one written down, or the scheme default
	// materialized by the constructor that knew which default applied.
	Port int
}

// OriginOfURL parses an http/https/ws/wss URL into its Origin, applying the
// scheme defaults of §1.1.
//
// A URL WITHOUT AN EXPLICIT SCHEME IS REJECTED, and that is the fail-closed
// direction rather than an oversight. The retired host guard prepended
// "https://" to a schemeless string because it was computing a hint about where
// a secret might go; this function decides whether an egress may happen, and
// guessing a scheme here would mean guessing which of two different destinations
// the user meant. The send path does not guess either — http.NewRequest rejects
// a schemeless URL outright — so a definition whose URL is still full of
// unresolved templates contributes NO origin to Base and prompts, which is
// correct: nothing about it was resolved, so nothing about it was checked.
func OriginOfURL(rawURL string) (Origin, bool) {
	trimmed := strings.TrimSpace(rawURL)
	if trimmed == "" {
		return Origin{}, false
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return Origin{}, false
	}
	return originOfParsedURL(parsed)
}

// originOfParsedURL is OriginOfURL's body for callers that already hold a parsed
// URL — chiefly the guard transport, which must not re-serialize and re-parse
// the request URL it was handed just to learn where it points.
func originOfParsedURL(parsed *url.URL) (Origin, bool) {
	if parsed == nil {
		return Origin{}, false
	}
	scheme := strings.ToLower(strings.TrimSpace(parsed.Scheme))
	var defaultPort int
	switch scheme {
	case "http", "ws":
		scheme, defaultPort = "http", 80
	case "https", "wss":
		scheme, defaultPort = "https", 443
	default:
		return Origin{}, false
	}
	port := defaultPort
	if written := parsed.Port(); written != "" {
		value, err := strconv.Atoi(written)
		if err != nil {
			return Origin{}, false
		}
		port = value
	}
	return newOrigin(scheme, parsed.Hostname(), port)
}

// newOrigin builds a normalized Origin from parts. It is the ONE place host
// normalization happens, so §4.7's gRPC validator — which computes its own
// effective port and therefore cannot go through OriginOfURL — still produces
// origins that compare equal to the ones the HTTP path produces for the same
// destination.
//
// The port must be explicit: a caller that does not know the port does not know
// the destination, and defaulting one here would silently pick the HTTP rule for
// a protocol whose default is different.
func newOrigin(scheme, host string, port int) (Origin, bool) {
	scheme = strings.ToLower(strings.TrimSpace(scheme))
	if scheme != "http" && scheme != "https" {
		return Origin{}, false
	}
	host = normalizeOriginHost(host)
	if host == "" {
		return Origin{}, false
	}
	if port < 1 || port > 65535 {
		return Origin{}, false
	}
	return Origin{Scheme: scheme, Host: host, Port: port}, true
}

// normalizeOriginHost lowercases, unbrackets and canonicalizes a host.
//
// net.ParseIP does the IPv6 work: it collapses zero runs and leading zeros, so
// every spelling of one address reduces to one string. A zoned literal
// ("fe80::1%eth0") parses as nil and is kept verbatim — it is not a destination
// the boundary can reason about, and mangling it would be worse than leaving it
// distinct.
func normalizeOriginHost(host string) string {
	host = strings.ToLower(strings.TrimSpace(host))
	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		host = host[1 : len(host)-1]
	}
	if host == "" {
		return ""
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.String()
	}
	return host
}

// String renders the canonical scheme://host:port form — the exact text the
// persisted approval key stores (§6) and the prompt shows the user. IPv6 hosts
// are re-bracketed by net.JoinHostPort, so the result is always dialable.
func (o Origin) String() string {
	if !o.valid() {
		return ""
	}
	return o.Scheme + "://" + net.JoinHostPort(o.Host, strconv.Itoa(o.Port))
}

// valid reports whether this is a real origin rather than the zero value. The
// zero Origin is never authorized: an egress whose destination could not be
// determined is an egress that was not checked.
func (o Origin) valid() bool {
	return o.Scheme != "" && o.Host != "" && o.Port > 0
}

// egressKind names WHICH egress of an execution is being authorized. It is half
// of the authority key — Base is computed per kind, so a request's own
// destination never authorizes its OAuth token endpoint and vice versa.
type egressKind string

const (
	// egressKindMain is the request itself: HTTP/GraphQL, the WebSocket
	// handshake, the gRPC dial, and the digest retry that repeats it.
	egressKindMain egressKind = "main"
	// egressKindRedirect is a hop the HTTP client followed on its own.
	egressKindRedirect egressKind = "redirect"
	// egressKindScript is a send a user-authored script made (pm.sendRequest,
	// bru.sendRequest, fetch).
	egressKindScript egressKind = "script"
	// egressKindScriptDNS is a script's name lookup. It is a kind of its own
	// because it is authorized against hostnames rather than origins — a lookup
	// has no port and no scheme.
	egressKindScriptDNS egressKind = "script-dns"
	// egressKindToken is a non-interactive OAuth2 token or refresh exchange.
	egressKindToken egressKind = "token"
	// egressKindAWS is an AWS credential-resolution endpoint (STS, SSO,
	// SSO-OIDC).
	egressKindAWS egressKind = "aws"
	// egressKindProxy is the user's manually configured proxy. It has no
	// approval path: the effective manual proxy equals the agent-free resolution
	// by construction (§4.4), so there is nothing an approval could add.
	egressKindProxy egressKind = "proxy"
)

// Approval classes. An approval is remembered per class, not per kind, so that
// approving a redirect does not require re-approving the main request it came
// from — while a token-endpoint approval still cannot authorize a request-class
// egress.
const (
	kindClassRequest = "request"
	kindClassToken   = "token"
	kindClassAWS     = "aws"
	kindClassProxy   = "proxy"
)

// kindClass maps an egress kind onto the class its approvals are keyed by.
//
// AN UNKNOWN KIND MAPS TO THE EMPTY CLASS, and callers treat that as "no
// approval can authorize this". Defaulting an unrecognized kind into
// kindClassRequest would mean a kind added later, before its class is decided,
// silently inherits every request-class approval the user has already given.
func kindClass(k egressKind) string {
	switch k {
	case egressKindMain, egressKindRedirect, egressKindScript, egressKindScriptDNS:
		return kindClassRequest
	case egressKindToken:
		return kindClassToken
	case egressKindAWS:
		return kindClassAWS
	case egressKindProxy:
		return kindClassProxy
	default:
		return ""
	}
}
