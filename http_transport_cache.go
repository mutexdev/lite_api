package main

import (
	"net/http"
	"sync"
	"time"

	"github.com/mutexdev/lite_api/internal/auth/awsv4"
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
func (a *App) requestTransport(base http.RoundTripper, settings appTLSSettings, verifyTLS bool, collectionID, targetURL string, vars map[string]string) (http.RoundTripper, error) {
	spec := xport.Spec{
		Source:          xport.Source(base),
		VerifyTLS:       verifyTLS,
		CustomCAEnabled: settings.Request.CustomCaCertificate.Enabled,
		KeepDefaultCAs:  boolPtrValue(settings.Request.KeepDefaultCaCertificates.Enabled, true),
		SessionCache:    settings.ClientSessionCache,
	}
	// The custom root store is only consulted when verification is on; with
	// verification off the pre-US-016 chain skipped it entirely.
	if verifyTLS && spec.CustomCAEnabled {
		certPath, certPEM, err := xport.ReadCustomCACertificatePEM(settings.Request)
		if err != nil {
			return nil, err
		}
		spec.CustomCAPath = certPath
		spec.CustomCAPEM = certPEM
	}
	if collectionPath, certs, ok := a.collectionClientCertificateConfig(collectionID); ok {
		certificate, matched, err := xport.MatchingTLSClientCertificate(collectionPath, certs, targetURL, vars)
		if err != nil {
			return nil, err
		}
		if matched {
			spec.ClientCert = &certificate
			spec.ClientCertDigest = xport.ClientCertificateDigest(certificate)
		}
	}
	if err := spec.ApplyProxyResolution(a.collectionProxyResolution(collectionID), targetURL, vars); err != nil {
		return nil, err
	}
	return a.transportCache.TransportFor(spec)
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
func sharedCredentialHTTPClient() *http.Client {
	credentialHTTPClientOnce.Do(func() {
		transport, err := packageTransportCache.TransportFor(xport.Spec{VerifyTLS: true, ProxyMode: xport.ProxyInherit})
		if err != nil || transport == nil {
			// Unreachable: a pristine spec is answered from the source with
			// no build step. Fall back to the pre-US-017 construction.
			credentialHTTPClient = &http.Client{Timeout: 30 * time.Second}
			return
		}
		credentialHTTPClient = &http.Client{Timeout: 30 * time.Second, Transport: transport}
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

	// internal/transport defaults to a plain client so it stands alone; inside
	// the app, PAC fetches must go through the shared transport cache. The
	// interpolator seam is gone -- internal/interp exists now, so both packages
	// import it directly instead of being handed a function at startup.
	xport.SetPACHTTPClient(sharedPACHTTPClient)
}
