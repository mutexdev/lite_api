package core

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/mutexdev/lite_api/internal/auth/awsv4"
	"github.com/mutexdev/lite_api/internal/mcpserver"
	"github.com/mutexdev/lite_api/internal/prefs"
	"github.com/mutexdev/lite_api/internal/scripting"
	xport "github.com/mutexdev/lite_api/internal/transport"
)

// US-016 — keyed HTTP transport cache.
//
// Before this file every outbound request rebuilt its *http.Transport with
// Clone(). A cloned transport starts with an EMPTY connection pool, so a
// clone-per-request means a fresh TCP + TLS handshake per request even when a
// hundred sends go to the same host with the same settings. Because the proxy
// stage cloned unconditionally (xport.WithoutProxy still clones), this hit
// every request, not just the proxy/mTLS ones.
//
// The fix is to build each distinct transport once and hand the same pointer
// back for every request that shares its security posture.
//
// THE CORRECTNESS CONSTRAINT. Two requests may share a transport only if every
// input that can change its TLS or proxy behaviour is identical. Handing a
// verify-on request a transport built with InsecureSkipVerify would be a silent
// downgrade that no status-code assertion could catch. The key below therefore
// enumerates every such input; see xport.Spec's field comments for the
// completeness argument.
//
// LOCK ORDER. xport.Cache.mu is a leaf: nothing inside this file takes
// App.mu, and (a *App).requestTransport releases App.mu (via
// collectionClientCertificateConfig / collectionProxyResolution) before it
// touches the cache. The only path that holds both is UpdatePreferences /
// ClearSSLSessionCache, which take App.mu then flush(), i.e. App.mu -> cache.mu.
// That is the same direction as US-012's App.mu -> persistMu, so no cycle.

// requestTransport resolves this request's whole security posture — app TLS
// settings, custom CA, matched client certificate, proxy — and returns the
// shared transport for it. It replaces the three-clone chain that used to run
// per request in executeHTTP.
//
// Order of work, and therefore of reported errors, is unchanged: custom CA,
// then client certificate, then proxy.
//
// THE SECOND RESULT IS THE CERTIFICATE CONFINEMENT (§4.4). It is the zero
// Origin on every path except one: an MCP-initiated send whose transport ended
// up carrying a client certificate. The caller wraps the transport so that
// every hop off that origin is refused, because the certificate sits in
// TLSClientConfig.Certificates for every host the transport dials and Go offers
// no per-host withholding.
//
// UNDER MCP PROVENANCE THE INPUTS CHANGE, not the shape. `targetURL` and `vars`
// are agent-shaped: an agent picks the run's variable overrides, which choose
// which client certificate matches and which proxy the request is resolved
// against. So the MCP branch below builds the whole posture from the active
// scope's agent-free main destination and variables instead, and consults
// `targetURL` for exactly one thing — deciding whether the certificate may be
// presented at all (§2 row 9).
func (a *App) requestTransport(ctx context.Context, base http.RoundTripper, settings appTLSSettings, verifyTLS bool, collectionID, targetURL string, vars map[string]string) (http.RoundTripper, Origin, error) {
	spec := xport.Spec{
		Source:          xport.Source(base),
		VerifyTLS:       verifyTLS,
		CustomCAEnabled: settings.Request.CustomCaCertificate.Enabled,
		KeepDefaultCAs:  prefs.BoolPtrValue(settings.Request.KeepDefaultCaCertificates.Enabled, true),
		SessionCache:    settings.ClientSessionCache,
	}
	// The custom root store is only consulted when verification is on; with
	// verification off the pre-US-016 chain skipped it entirely.
	if verifyTLS && spec.CustomCAEnabled {
		certPath, certPEM, err := xport.ReadCustomCACertificatePEM(settings.Request)
		if err != nil {
			return nil, Origin{}, err
		}
		spec.CustomCAPath = certPath
		spec.CustomCAPEM = certPEM
	}
	if policy := mcpPolicyFromContext(ctx); policy != nil {
		posture, err := a.mcpTransportPosture(ctx, policy, collectionID, targetURL)
		if err != nil {
			return nil, Origin{}, err
		}
		posture.applyToSpec(&spec, targetURL)
		built, err := a.transportCache.TransportFor(spec)
		if err != nil {
			return nil, Origin{}, err
		}
		return built, posture.certOrigin, nil
	}
	if collectionPath, certs, ok := a.collectionClientCertificateConfig(collectionID); ok {
		certificate, matched, err := xport.MatchingTLSClientCertificate(collectionPath, certs, targetURL, vars)
		if err != nil {
			return nil, Origin{}, err
		}
		if matched {
			spec.ClientCert = &certificate
			spec.ClientCertDigest = xport.ClientCertificateDigest(certificate)
		}
	}
	if err := spec.ApplyProxyResolution(a.collectionProxyResolution(collectionID), targetURL, vars); err != nil {
		return nil, Origin{}, err
	}
	built, err := a.transportCache.TransportFor(spec)
	if err != nil {
		return nil, Origin{}, err
	}
	return built, Origin{}, nil
}

// --- §4.4: transport construction with agent-free values ------------------

// mcpTransportPosture is the certificate-and-proxy half of a transport, decided
// for an MCP-initiated send without consulting a single agent-supplied value.
//
// It is a value rather than a mutated Spec because the same decision has to
// reach two different consumers: the HTTP path applies it to an xport.Spec and
// shares the resulting transport through the cache, while the WebSocket dialer
// applies it to a cloned *http.Transport whose Proxy and TLSClientConfig it
// then lifts out. One decision, two applications, so the two cannot drift.
type mcpTransportPosture struct {
	// cert and certDigest are the client identity, matched from the scope's
	// agent-free main destination and attached only when the actual egress
	// origin equals certOrigin.
	cert       *tls.Certificate
	certDigest string
	// certOrigin is set only when cert is. It is the one origin this transport
	// may talk to, enforced above the transport by mcpCertConfinedTransport.
	certOrigin Origin
	// proxyMode / proxyURL / refusePAC are the resolved proxy disposition, in
	// xport's own vocabulary.
	proxyMode string
	proxyURL  *url.URL
	refusePAC bool
}

// mcpTransportPosture decides §4.4's certificate and proxy questions for one
// MCP send.
//
// CERTIFICATE MATCHING NEVER SEES THE RUNTIME TARGET. The matching seam is
// handed the active scope's `mainURL` and `baseVars`, so which certificate
// matches is a property of the stored definition under the run's one agent-free
// variable context and nothing else. `targetURL` decides only whether the
// matched certificate may be PRESENTED.
//
// AND THE EQUALITY CHECK RUNS BEFORE THE CERTIFICATE IS LOADED, matching the
// gRPC seam's rule: an off-certOrigin send does no key-file I/O, so a broken or
// missing key file cannot fail a send that would never have presented the
// certificate anyway.
func (a *App) mcpTransportPosture(ctx context.Context, policy *mcpEgressPolicy, collectionID, targetURL string) (mcpTransportPosture, error) {
	posture := mcpTransportPosture{proxyMode: xport.ProxyOff}
	scope, ok := policy.activeScope()
	if !ok {
		// Fail closed, exactly as Authorize does with no scope: without one
		// there is no agent-free main destination to build a posture from, and
		// falling back to the runtime target would be falling back to the
		// agent's own value.
		return mcpTransportPosture{}, policy.Refuse(
			"This run has no active request scope, so its transport could not be built from the request's own definition",
			"This is a bug in LiteAPI — report it rather than retrying.")
	}
	actualOrigin, _ := OriginOfURL(targetURL)
	if certOrigin, ok := OriginOfURL(scope.mainURL); ok && actualOrigin == certOrigin {
		if collectionPath, certs, ok := a.collectionClientCertificateConfig(collectionID); ok {
			certificate, matched, err := xport.MatchingTLSClientCertificate(collectionPath, certs, scope.mainURL, scope.baseVars)
			if err != nil {
				return mcpTransportPosture{}, err
			}
			if matched {
				posture.cert = &certificate
				posture.certDigest = xport.ClientCertificateDigest(certificate)
				posture.certOrigin = certOrigin
			}
		}
	}
	if err := a.mcpApplyProxyPosture(ctx, &posture, policy, scope, collectionID, targetURL); err != nil {
		return mcpTransportPosture{}, err
	}
	return posture, nil
}

// mcpApplyProxyPosture resolves the proxy half of §4.4.
//
// THE SYSTEM BRANCH SPLITS ON WHETHER A CERTIFICATE IS ATTACHED, and the split
// is the whole design:
//
//   - CERT-FREE transports keep the per-request closure, because a lazily
//     resolved system proxy is trusted-proxy egress (§1.1 path 1) and nothing
//     about it is agent-selectable. The closure refuses on discovering a PAC
//     URL, on every dial, before any fetch or evaluation.
//   - CERT-BEARING transports FREEZE. Discovery runs here, eagerly, before
//     TransportFor; a PAC or an https-scheme proxy refuses; anything else is
//     frozen into the spec as ProxyOff or ProxyExplicit so no lazy closure
//     survives on a transport that carries the user's identity. The freeze
//     removes the "OS settings changed between the check and the dial" race
//     categorically rather than narrowing it, and it makes the transport's
//     disposition equal to the one that was checked. Safety does not rest on
//     the cache: every later send re-discovers and re-refuses before the cache
//     is consulted, and reuse requires both the certificate digest and the
//     frozen disposition to match (they are both in the spec key).
func (a *App) mcpApplyProxyPosture(ctx context.Context, posture *mcpTransportPosture, policy *mcpEgressPolicy, scope mcpScopeOrigins, collectionID, targetURL string) error {
	resolution := a.collectionProxyResolution(collectionID)
	switch strings.ToLower(strings.TrimSpace(resolution.Mode)) {
	case "pac":
		return mcpPACProxyRefusal(ctx)
	case "manual":
		// Resolved with the scope's agent-free variables, so the effective
		// proxy IS the agent-free one by construction and the authorization
		// below can only agree with Base (§4.4). The BYPASS list is evaluated
		// against the runtime target on purpose: it decides whether this egress
		// goes through the proxy at all, and answering that about a different
		// URL than the one being sent would describe a request nobody made.
		var resolved xport.Spec
		if err := resolved.ApplyProxyResolution(resolution, targetURL, scope.baseVars); err != nil {
			return err
		}
		posture.proxyMode, posture.proxyURL = resolved.ProxyMode, resolved.ProxyURL
		if posture.proxyMode != xport.ProxyExplicit || posture.proxyURL == nil {
			return nil
		}
		if posture.cert != nil && strings.EqualFold(posture.proxyURL.Scheme, "https") {
			return mcpCertificateHTTPSProxyRefusal(ctx)
		}
		origin, ok := mcpProxyOrigin(posture.proxyURL)
		if !ok {
			return policy.Refuse(
				"This request's manual proxy did not resolve to a usable address",
				"Fix the proxy hostname and port in the app, or run this request there.")
		}
		return policy.Authorize(ctx, origin, egressKindProxy)
	case "system":
		if posture.cert == nil {
			posture.proxyMode = xport.ProxySystem
			posture.refusePAC = true
			return nil
		}
		proxyURL, pacURL, err := xport.DiscoverSystemProxy(targetURL)
		if err != nil {
			return err
		}
		if pacURL != "" {
			return mcpPACProxyRefusal(ctx)
		}
		if proxyURL == nil {
			posture.proxyMode = xport.ProxyOff
			return nil
		}
		if strings.EqualFold(proxyURL.Scheme, "https") {
			return mcpCertificateHTTPSProxyRefusal(ctx)
		}
		posture.proxyMode, posture.proxyURL = xport.ProxyExplicit, proxyURL
		return nil
	default:
		posture.proxyMode = xport.ProxyOff
		return nil
	}
}

// applyToSpec writes the posture into the transport spec the cache is keyed on.
// Both the certificate digest and the frozen proxy disposition are spec fields,
// so a cached transport can never be reused across two different security
// postures (§4.4).
func (p mcpTransportPosture) applyToSpec(spec *xport.Spec, targetURL string) {
	spec.ClientCert = p.cert
	spec.ClientCertDigest = p.certDigest
	spec.ProxyMode = p.proxyMode
	spec.ProxyURL = p.proxyURL
	spec.RefuseSystemPAC = p.refusePAC
	if p.proxyMode == xport.ProxySystem {
		spec.SystemProxyFallbackURL = targetURL
	}
}

// applyToTransport writes the posture onto a cloned transport, for the
// WebSocket dialer, which reads Proxy and TLSClientConfig off a transport
// rather than issuing requests through one. It mirrors xport.Spec.Build's own
// two steps — prepend the certificate, then set Proxy — so the two paths cannot
// disagree about what a posture means.
func (p mcpTransportPosture) applyToTransport(t *http.Transport, targetURL string) {
	if t == nil {
		return
	}
	if p.cert != nil {
		tlsConfig := t.TLSClientConfig
		if tlsConfig == nil {
			tlsConfig = &tls.Config{}
		} else {
			tlsConfig = tlsConfig.Clone()
		}
		tlsConfig.Certificates = append([]tls.Certificate{*p.cert}, tlsConfig.Certificates...)
		t.TLSClientConfig = tlsConfig
	}
	switch p.proxyMode {
	case xport.ProxyExplicit:
		t.Proxy = http.ProxyURL(p.proxyURL)
	case xport.ProxySystem:
		t.Proxy = xport.SystemProxyFunc(targetURL, p.refusePAC)
	default:
		t.Proxy = nil
	}
}

// mcpPACProxyRefusal is §2 row 4. It is raised at three distinct places — the
// mode check here, the cert-free closure's discovery, and the cert-bearing
// frozen construction — and reads identically at all three, because to the user
// they are one fact about their configuration.
func mcpPACProxyRefusal(ctx context.Context) error {
	return mcpRefuseFeature(ctx,
		"The effective proxy configuration uses a PAC file, which is a remote script LiteAPI would have to fetch and run",
		"Run this request in the app, or switch the proxy setting to manual or system.")
}

// mcpCertificateHTTPSProxyRefusal is §2 row 6. A CONNECT through an https proxy
// is itself a TLS handshake with the proxy, and the certificate lives in
// TLSClientConfig for every dial the transport makes — so the proxy could ask
// for the user's client identity and get it.
func mcpCertificateHTTPSProxyRefusal(ctx context.Context) error {
	return mcpRefuseFeature(ctx,
		"This request combines a client certificate with an HTTPS proxy, so the certificate could be presented to the proxy",
		"Run it in the LiteAPI app.")
}

// mcpCertConfinedTransport refuses every request that leaves certOrigin (§2 row
// 7, §4.4).
//
// WHY A WRAPPER AND NOT A POLICY KIND. This is not an authorization question, so
// there is nothing for an approval to say yes to: the certificate is already in
// the transport's TLS config, Go presents it to whoever asks during a handshake,
// and the only way to not present it to a redirect target is to not make the
// connection. A user who wants that redirect followed with the certificate in
// play can run the request in the app.
type mcpCertConfinedTransport struct {
	base       http.RoundTripper
	certOrigin Origin
}

func (t mcpCertConfinedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	origin, ok := originOfParsedURL(req.URL)
	if !ok || origin != t.certOrigin {
		return nil, fmt.Errorf("%w: blocked a request to %s: this request carries a client certificate, which %s could ask for. Run it in the LiteAPI app",
			mcpserver.ErrDenied, originLabel(origin), originLabel(origin))
	}
	return base.RoundTrip(req)
}

// mcpSendTransport is the wrapping order every engine send uses.
//
// THE GUARD GOES INNERMOST, directly around the shared transport, and that is
// deliberate rather than incidental. http.Client calls the client's transport
// once per redirect hop and the digest retry is a second Do, so a guard at
// either level sees both — but NTLM's negotiate/authenticate exchange
// (ntlmssp.Negotiator) issues its own round trips BELOW the client, and only the
// innermost position sees those. One wrap, every round trip.
func mcpSendTransport(base http.RoundTripper, certOrigin Origin) http.RoundTripper {
	if certOrigin.valid() {
		base = mcpCertConfinedTransport{base: base, certOrigin: certOrigin}
	}
	return newMCPEgressGuardTransport(base)
}

// US-017 — the one-off clients.
//
// Six call sites built `http.Client{Timeout: 30 * time.Second}` per call, plus
// one 5 s client for PAC fetches. Five of the six exchange credentials (OAuth2
// token endpoints, AWS STS, AWS SSO/OIDC) and the sixth is the script
// runtime's sendRequest.
//
// They are consolidated onto shared clients WITHOUT adopting the user's proxy
// or TLS settings, and that is deliberate. A nil Transport means
// http.DefaultTransport, i.e. always-verified TLS and ProxyFromEnvironment.
// Routing a client-secret exchange through a user-configured proxy it
// previously bypassed, or letting a "disable SSL verification" toggle reach a
// token endpoint, would be a security regression dressed up as a refactor. So
// the credential clients take the pristine posture, which transportFor answers
// with http.DefaultTransport itself — one shared, already-pooling transport.
//
// packageTransportCache serves the package-level helpers, which have no *App.
var packageTransportCache xport.Cache

// These are lazily built through plain functions rather than package-level
// sync.OnceValue variables: the PAC client's spec reaches xport.SystemProxyURLForRequest,
// which reaches xport.LoadPACSource, which uses the PAC client — a variable
// initialisation cycle the compiler rejects. A function body does not
// participate in initialisation ordering, so this also keeps the clients out of
// process start-up.
var (
	credentialHTTPClientOnce sync.Once
	credentialHTTPClient     *http.Client
	pacHTTPClientOnce        sync.Once
	pacHTTPClient            *http.Client
)

// sharedCredentialHTTPClient is the 30 s client used by the OAuth2, AWS STS and
// AWS SSO helpers and by the script runtime's sendRequest. Posture: verified
// TLS, environment proxy — behaviourally what `http.Client{Timeout: 30 *
// time.Second}` (nil Transport, hence http.DefaultTransport) already did.
//
// ITS TRANSPORT IS GUARD-WRAPPED (§4.3 item 3). This is two of the three
// wrapped clients at once: every OAuth2 token exchange, every AWS credential
// call (through awsv4.SetHTTPClient) and every script sendRequest/fetch travels
// through it. The wrap costs a UI send one context lookup and one nil check —
// an unlabeled or UI-labeled request passes straight through — and it is what
// makes those paths checkable at all, since none of them goes through the
// per-send transport in executeHTTP.
func sharedCredentialHTTPClient() *http.Client {
	credentialHTTPClientOnce.Do(func() {
		transport, err := packageTransportCache.TransportFor(xport.Spec{VerifyTLS: true, ProxyMode: xport.ProxyInherit})
		if err != nil || transport == nil {
			// Unreachable: a pristine spec is answered from the source with
			// no build step. Fall back to the pre-US-017 construction.
			credentialHTTPClient = &http.Client{Timeout: 30 * time.Second, Transport: newMCPEgressGuardTransport(nil)}
			return
		}
		credentialHTTPClient = &http.Client{Timeout: 30 * time.Second, Transport: newMCPEgressGuardTransport(transport)}
	})
	return credentialHTTPClient
}

// sharedPACHTTPClient fetches PAC files. Posture: verified TLS and NO proxy —
// a PAC fetch must not be routed through the proxy it is being consulted to
// discover. Previously this cloned a fresh transport per fetch.
func sharedPACHTTPClient() *http.Client {
	pacHTTPClientOnce.Do(func() {
		transport, err := packageTransportCache.TransportFor(xport.Spec{VerifyTLS: true, ProxyMode: xport.ProxyOff})
		if err != nil || transport == nil {
			// Unreachable: this spec loads no CA and no client certificate,
			// so build() cannot fail. Fall back to the pre-US-017
			// construction rather than silently changing the proxy posture.
			pacHTTPClient = &http.Client{Timeout: 5 * time.Second, Transport: xport.WithoutProxy(http.DefaultTransport)}
			return
		}
		pacHTTPClient = &http.Client{Timeout: 5 * time.Second, Transport: transport}
	})
	return pacHTTPClient
}

// internal/auth/awsv4 resolves credentials over the network (STS, SSO, OIDC).
// It defaults to a plain client so the package stands alone, but inside the app
// those calls must go through the shared transport cache like every other
// request -- same verified TLS, same inherited proxy. Wiring it here rather than
// in the package keeps the dependency pointing one way.
func init() {
	awsv4.SetHTTPClient(sharedCredentialHTTPClient)

	// The script sandbox's own fetch/sendRequest client. US-017 already
	// DESCRIBED this client as the script runtime's, but the seam was never
	// wired, so scripting kept its own `http.Client{Timeout: 30s}` — nil
	// Transport, hence http.DefaultTransport, which is exactly what the pristine
	// spec above resolves to. So this changes no posture; what it changes is
	// that script egress now travels through a guard-wrapped transport (§4.3
	// item 3), which is the backstop behind scriptSendRequest's own checkpoint.
	scripting.SetHTTPClient(sharedCredentialHTTPClient)

	// internal/transport defaults to a plain client so it stands alone; inside
	// the app, PAC fetches must go through the shared transport cache. The
	// interpolator seam is gone -- internal/interp exists now, so both packages
	// import it directly instead of being handed a function at startup.
	//
	// The PAC client is DELIBERATELY NOT guard-wrapped: an MCP run never
	// reaches it (§2 row 4 refuses every PAC disposition upstream), and leaving
	// it unwrapped is what lets a strict-mode test prove that by asserting zero
	// PAC bytes move rather than asserting a refusal was formatted nicely.
	xport.SetPACHTTPClient(sharedPACHTTPClient)
}
