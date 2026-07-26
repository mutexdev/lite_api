package main

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// US-016 — keyed HTTP transport cache.
//
// Before this file every outbound request rebuilt its *http.Transport with
// Clone(). A cloned transport starts with an EMPTY connection pool, so a
// clone-per-request means a fresh TCP + TLS handshake per request even when a
// hundred sends go to the same host with the same settings. Because the proxy
// stage cloned unconditionally (transportWithoutProxy still clones), this hit
// every request, not just the proxy/mTLS ones.
//
// The fix is to build each distinct transport once and hand the same pointer
// back for every request that shares its security posture.
//
// THE CORRECTNESS CONSTRAINT. Two requests may share a transport only if every
// input that can change its TLS or proxy behaviour is identical. Handing a
// verify-on request a transport built with InsecureSkipVerify would be a silent
// downgrade that no status-code assertion could catch. The key below therefore
// enumerates every such input; see transportSpec's field comments for the
// completeness argument.
//
// LOCK ORDER. httpTransportCache.mu is a leaf: nothing inside this file takes
// App.mu, and (a *App).requestTransport releases App.mu (via
// collectionClientCertificateConfig / collectionProxyResolution) before it
// touches the cache. The only path that holds both is UpdatePreferences /
// ClearSSLSessionCache, which take App.mu then flush(), i.e. App.mu -> cache.mu.
// That is the same direction as US-012's App.mu -> persistMu, so no cycle.

const (
	// transportCacheIdleTTL evicts a transport that has not served a request
	// for this long. Chosen above http.DefaultTransport's 90 s
	// IdleConnTimeout: below it the map would drop entries that still own
	// live idle sockets, forcing a reconnect that the pool could have served.
	transportCacheIdleTTL = 5 * time.Minute

	// transportCacheMaxEntries bounds the map so a workspace with many
	// collections, or a PAC file that returns a different proxy per host,
	// cannot grow it without limit inside one TTL window. Each entry owns up
	// to MaxIdleConnsPerHost sockets, so the cap is a socket budget as much
	// as a memory one.
	transportCacheMaxEntries = 32

	// transportCacheSweepInterval rate-limits the eviction scan on the cache
	// hit path, which is the request hot path. A hit costs one atomic load
	// in the common case.
	transportCacheSweepInterval = 30 * time.Second
)

// Proxy dispositions. manual and PAC modes both resolve, per request, to either
// "no proxy" or one concrete URL, so they collapse into transportProxyOff and
// transportProxyExplicit: two requests that resolve to the same proxy URL are
// entitled to the same transport however they got there.
const (
	// transportProxyInherit leaves the source transport's own Proxy func in
	// place (http.DefaultTransport's ProxyFromEnvironment, for callers that
	// must not be re-routed through the user's proxy settings).
	transportProxyInherit = "inherit"
	// transportProxyOff sets Proxy to nil: no proxy, and env vars ignored.
	transportProxyOff = "off"
	// transportProxyExplicit routes through transportSpec.proxyURL.
	transportProxyExplicit = "explicit"
	// transportProxySystem resolves the proxy per request from the OS.
	transportProxySystem = "system"
)

// transportSpec is the complete set of inputs that determine an outbound
// transport's TLS and proxy behaviour. Every field except source, clientCert
// and systemProxyFallbackURL is fed verbatim into cacheKey; those three are
// keyed by identity (pointer, certificate digest) or documented as
// behaviourally irrelevant.
//
// Completeness argument — the transport differs from its source in exactly two
// respects, TLSClientConfig and Proxy, and nothing else in build() varies:
//
//   - TLSClientConfig.InsecureSkipVerify  <- verifyTLS
//   - TLSClientConfig.RootCAs             <- customCAEnabled, customCAPEM,
//     keepDefaultCAs
//   - TLSClientConfig.ClientSessionCache  <- sessionCache
//   - TLSClientConfig.Certificates        <- clientCertDigest
//   - everything else in TLSClientConfig (MinVersion, MaxVersion,
//     CipherSuites, NextProtos, ServerName, ...) is inherited untouched from
//     source, so source's identity covers it
//   - Proxy                               <- proxyMode, proxyURL (whose
//     String() carries scheme, host, port AND proxy credentials)
//   - every other transport field (dialer, timeouts, HTTP/2, limits) is
//     inherited untouched from source, so source's identity covers it
//
// A field is included even when the current build() would ignore it (for
// example sessionCache when verifyTLS is on and no custom CA is configured).
// An over-specific key costs at most one extra entry; an under-specific key is
// a security hole.
type transportSpec struct {
	// source is the transport the clone is taken from. Keyed by pointer:
	// two different base transports must never share a derived transport,
	// because source contributes every field build() does not set. The
	// pointer cannot be recycled behind our back because the cache entry
	// holds its own reference to source, keeping it alive for as long as the
	// key that names it exists.
	source *http.Transport

	// verifyTLS is the resolved verification decision
	// (requestTLSVerificationEnabled), already folding in the request's
	// VerifyTLS setting and the app's SSLVerification preference.
	verifyTLS bool

	// customCAEnabled, customCAPath and customCAPEM describe the custom root
	// store. The PEM *content* is keyed as well as the path, so editing the
	// CA file on disk produces a new key rather than silently reusing a
	// transport built from the old roots.
	customCAEnabled bool
	customCAPath    string
	customCAPEM     []byte

	// keepDefaultCAs decides whether the system roots are kept alongside the
	// custom ones. Same path, same PEM, but system roots dropped, is a
	// materially different trust decision.
	keepDefaultCAs bool

	// sessionCache is the app-wide TLS session cache. Keyed by pointer:
	// ClearSSLSessionCache installs a fresh cache, and resumption tickets
	// must not leak across that boundary.
	sessionCache tls.ClientSessionCache

	// clientCert is the matched client identity, and clientCertDigest is its
	// key contribution: SHA-256 over the DER chain the handshake will
	// present. The chain pins the public key, and both loaders
	// (tls.X509KeyPair and pkcs12.DecodeChain) refuse a private key that
	// does not match it, so the chain digest identifies the whole identity.
	// It also changes when the file on disk changes.
	clientCert       *tls.Certificate
	clientCertDigest string

	// proxyMode and proxyURL are the resolved proxy disposition. The
	// resolution (bypass lists, PAC evaluation, variable interpolation of
	// host/port/credentials) happens per request in
	// applyProxyResolution; only its OUTCOME is keyed, so two requests share
	// a transport exactly when they end up at the same proxy.
	proxyMode string
	proxyURL  *url.URL

	// systemProxyFallbackURL is used only when http.Transport calls the
	// proxy func with a nil request or nil URL, which it never does. It is
	// deliberately NOT part of the key: keying it would defeat sharing for
	// system-proxy users, and the pre-US-016 code already memoised one
	// system-proxy transport per App with whatever URL happened to be first.
	systemProxyFallbackURL string
}

// pristine reports whether build() would produce a transport indistinguishable
// from source. Those callers get source itself, which keeps its warm pool and
// costs no map lookup at all.
func (spec transportSpec) pristine() bool {
	return spec.verifyTLS &&
		!spec.customCAEnabled &&
		spec.clientCert == nil &&
		spec.proxyMode == transportProxyInherit
}

// resolvedSource mirrors cloneHTTPTransport's fallback: a base that is not an
// *http.Transport (a test round-tripper, an auth wrapper) carries no dialer or
// TLS config to inherit, so http.DefaultTransport is the source instead.
func (spec transportSpec) resolvedSource() *http.Transport {
	if spec.source != nil {
		return spec.source
	}
	if fallback, ok := http.DefaultTransport.(*http.Transport); ok && fallback != nil {
		return fallback
	}
	return &http.Transport{}
}

// httpTransportSource resolves an arbitrary RoundTripper to the *http.Transport
// a clone would be taken from, matching cloneHTTPTransport.
func httpTransportSource(base http.RoundTripper) *http.Transport {
	if transport, ok := base.(*http.Transport); ok && transport != nil {
		return transport
	}
	if fallback, ok := http.DefaultTransport.(*http.Transport); ok {
		return fallback
	}
	return nil
}

// cacheKey hashes the spec with a length-prefixed encoding, so no field value
// can be confused with a different split of its neighbours (a proxy password
// containing the separator cannot forge another key). Hashing also keeps proxy
// credentials out of the long-lived map key.
func (spec transportSpec) cacheKey() string {
	digest := sha256.New()
	field := func(value string) {
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(len(value)))
		_, _ = digest.Write(length[:])
		_, _ = digest.Write([]byte(value))
	}
	field(fmt.Sprintf("%p", spec.resolvedSource()))
	field(fmt.Sprintf("%t", spec.verifyTLS))
	field(fmt.Sprintf("%t", spec.customCAEnabled))
	field(spec.customCAPath)
	field(string(spec.customCAPEM))
	field(fmt.Sprintf("%t", spec.keepDefaultCAs))
	field(sessionCacheIdentity(spec.sessionCache))
	field(spec.clientCertDigest)
	field(spec.proxyMode)
	if spec.proxyURL != nil {
		field(spec.proxyURL.String())
	} else {
		field("")
	}
	return hex.EncodeToString(digest.Sum(nil))
}

func sessionCacheIdentity(cache tls.ClientSessionCache) string {
	if cache == nil {
		return ""
	}
	return fmt.Sprintf("%p", cache)
}

// clientCertificateDigest identifies a client identity by the DER chain it
// presents. Returns "" for the empty certificate so "no certificate" and "some
// certificate" can never collide.
func clientCertificateDigest(certificate tls.Certificate) string {
	if len(certificate.Certificate) == 0 {
		return ""
	}
	digest := sha256.New()
	for _, der := range certificate.Certificate {
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(len(der)))
		_, _ = digest.Write(length[:])
		_, _ = digest.Write(der)
	}
	return hex.EncodeToString(digest.Sum(nil))
}

// build produces the transport the spec describes. It reproduces, in one step,
// exactly what the previous three-clone chain produced
// (transportWithAppTLSSettings -> transportWithClientCertificate ->
// transportWithProxyResolution), including its quirks: the TLS config is left
// untouched when verification is on and no custom CA is configured, which is
// also why the app-wide session cache is not installed on that path.
func (spec transportSpec) build() (*http.Transport, error) {
	transport := spec.resolvedSource().Clone()
	tlsTouched := !spec.verifyTLS || spec.customCAEnabled
	if tlsTouched || spec.clientCert != nil {
		tlsConfig := transport.TLSClientConfig
		if tlsConfig == nil {
			tlsConfig = &tls.Config{}
		} else {
			tlsConfig = tlsConfig.Clone()
		}
		if tlsTouched {
			if !spec.verifyTLS {
				tlsConfig.InsecureSkipVerify = true
			} else if err := applyCustomRootCAPEM(tlsConfig, spec.customCAPath, spec.customCAPEM, spec.keepDefaultCAs); err != nil {
				return nil, err
			}
			if spec.sessionCache != nil {
				tlsConfig.ClientSessionCache = spec.sessionCache
			}
		}
		if spec.clientCert != nil {
			tlsConfig.Certificates = append([]tls.Certificate{*spec.clientCert}, tlsConfig.Certificates...)
		}
		transport.TLSClientConfig = tlsConfig
	}
	switch spec.proxyMode {
	case transportProxyInherit:
		// Keep the source's own Proxy func.
	case transportProxySystem:
		fallbackURL := spec.systemProxyFallbackURL
		transport.Proxy = func(req *http.Request) (*url.URL, error) {
			target := fallbackURL
			if req != nil && req.URL != nil {
				target = req.URL.String()
			}
			return systemProxyURLForRequest(target)
		}
	case transportProxyExplicit:
		transport.Proxy = http.ProxyURL(spec.proxyURL)
	default:
		transport.Proxy = nil
	}
	return transport, nil
}

// applyCustomRootCAPEM is applyCustomRootCAsToTLSConfig's body, restated
// against PEM bytes that were already read (the read happens per request so the
// key reflects the file's current content). Same error strings.
func applyCustomRootCAPEM(tlsConfig *tls.Config, filePath string, certPEM []byte, keepDefaultCAs bool) error {
	if tlsConfig == nil || len(certPEM) == 0 {
		return nil
	}
	var roots *x509.CertPool
	if keepDefaultCAs {
		systemRoots, err := x509.SystemCertPool()
		if err != nil {
			return fmt.Errorf("load system CA certificates: %w", err)
		}
		roots = systemRoots
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

// readCustomCACertificatePEM reads the configured custom CA file. Returns an
// empty path and nil bytes when the feature is on but no path is set, matching
// applyCustomRootCAsToTLSConfig's early return.
func readCustomCACertificatePEM(preferences RequestPreferences) (string, []byte, error) {
	if !preferences.CustomCaCertificate.Enabled {
		return "", nil, nil
	}
	filePath := strings.TrimSpace(preferences.CustomCaCertificate.FilePath)
	if filePath == "" {
		return "", nil, nil
	}
	certPEM, err := os.ReadFile(filePath)
	if err != nil {
		return filePath, nil, fmt.Errorf("read custom CA certificate: %w", err)
	}
	return filePath, certPEM, nil
}

// applyProxyResolution resolves a proxyResolution against this request's URL
// and variables, storing only the outcome on the spec. Mirrors
// transportWithProxyResolution, including its deliberate swallowing of PAC
// errors (an unreachable or broken PAC file means "go direct", not "fail").
func (spec *transportSpec) applyProxyResolution(resolution proxyResolution, requestURL string, vars map[string]string) error {
	switch strings.ToLower(strings.TrimSpace(resolution.Mode)) {
	case "manual":
		if !shouldUseManualProxy(requestURL, interpolate(resolution.Config.BypassProxy, vars)) {
			spec.proxyMode = transportProxyOff
			return nil
		}
		proxyURL, err := manualProxyURL(resolution.Config, vars)
		if err != nil {
			return err
		}
		spec.proxyMode = transportProxyExplicit
		spec.proxyURL = proxyURL
		return nil
	case "system":
		spec.proxyMode = transportProxySystem
		spec.systemProxyFallbackURL = requestURL
		return nil
	case "pac":
		proxyURL, ok, err := resolvePACProxyURL(interpolate(resolution.PACSource, vars), requestURL)
		if err != nil || !ok {
			spec.proxyMode = transportProxyOff
			return nil
		}
		spec.proxyMode = transportProxyExplicit
		spec.proxyURL = proxyURL
		return nil
	default:
		spec.proxyMode = transportProxyOff
		return nil
	}
}

type transportCacheEntry struct {
	transport *http.Transport
	// source is held only to keep the address named by the cache key alive,
	// so the allocator cannot recycle it under a different transport.
	source   *http.Transport
	lastUsed atomic.Int64
}

// httpTransportCache maps a security posture to the one transport that serves
// it. The zero value is usable; the map is created on first insert.
//
// Callers MUST NOT mutate a returned transport: it is shared with every other
// request of the same posture, and may be http.DefaultTransport itself.
type httpTransportCache struct {
	mu        sync.RWMutex
	entries   map[string]*transportCacheEntry
	nextSweep atomic.Int64

	// Test seams; zero means "use the constant above".
	now        func() time.Time
	idleTTL    time.Duration
	maxEntries int
}

func (c *httpTransportCache) clock() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

func (c *httpTransportCache) ttl() time.Duration {
	if c.idleTTL > 0 {
		return c.idleTTL
	}
	return transportCacheIdleTTL
}

func (c *httpTransportCache) capacity() int {
	if c.maxEntries > 0 {
		return c.maxEntries
	}
	return transportCacheMaxEntries
}

// transportFor returns the shared transport for spec, building it on first use.
// The hot path is a read lock and a map lookup; the build happens outside the
// lock so a slow CA-pool construction never blocks other requests.
func (c *httpTransportCache) transportFor(spec transportSpec) (*http.Transport, error) {
	if spec.pristine() {
		return spec.resolvedSource(), nil
	}
	key := spec.cacheKey()
	now := c.clock()

	c.mu.RLock()
	hit, found := c.entries[key]
	c.mu.RUnlock()
	if found {
		hit.lastUsed.Store(now.UnixNano())
		c.sweepIfDue(now)
		return hit.transport, nil
	}

	built, err := spec.build()
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	if raced, ok := c.entries[key]; ok {
		c.mu.Unlock()
		// Another goroutine built the same posture first. Ours has never
		// dialled, so dropping it leaks nothing, but close anyway to be
		// explicit about who owns the pool.
		built.CloseIdleConnections()
		raced.lastUsed.Store(now.UnixNano())
		return raced.transport, nil
	}
	if c.entries == nil {
		c.entries = map[string]*transportCacheEntry{}
	}
	entry := &transportCacheEntry{transport: built, source: spec.resolvedSource()}
	entry.lastUsed.Store(now.UnixNano())
	c.entries[key] = entry
	evicted := c.evictLocked(now)
	c.nextSweep.Store(now.Add(transportCacheSweepInterval).UnixNano())
	c.mu.Unlock()

	closeTransports(evicted)
	return built, nil
}

// sweepIfDue runs the eviction scan at most once per
// transportCacheSweepInterval. On the hit path the common case is one atomic
// load.
func (c *httpTransportCache) sweepIfDue(now time.Time) {
	if now.UnixNano() < c.nextSweep.Load() {
		return
	}
	c.mu.Lock()
	if now.UnixNano() < c.nextSweep.Load() {
		c.mu.Unlock()
		return
	}
	evicted := c.evictLocked(now)
	c.nextSweep.Store(now.Add(transportCacheSweepInterval).UnixNano())
	c.mu.Unlock()
	closeTransports(evicted)
}

// evictLocked drops entries idle past the TTL, then trims the least recently
// used until the map fits its capacity. It returns the evicted transports
// rather than closing them, so the caller can close outside the lock.
//
// Eviction cannot orphan an in-flight request: the request already holds the
// *http.Transport pointer and keeps using it. Its connections return to the
// evicted transport's own pool and are reaped by that transport's
// IdleConnTimeout, after which the transport is garbage — which is why
// CloseIdleConnections is a hygiene measure, not the only thing standing
// between us and a socket leak.
func (c *httpTransportCache) evictLocked(now time.Time) []*http.Transport {
	if len(c.entries) == 0 {
		return nil
	}
	var evicted []*http.Transport
	ttl := c.ttl()
	for key, entry := range c.entries {
		if now.Sub(time.Unix(0, entry.lastUsed.Load())) > ttl {
			delete(c.entries, key)
			evicted = append(evicted, entry.transport)
		}
	}
	capacity := c.capacity()
	if len(c.entries) <= capacity {
		return evicted
	}
	type agedEntry struct {
		key      string
		lastUsed int64
	}
	ordered := make([]agedEntry, 0, len(c.entries))
	for key, entry := range c.entries {
		ordered = append(ordered, agedEntry{key: key, lastUsed: entry.lastUsed.Load()})
	}
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].lastUsed != ordered[j].lastUsed {
			return ordered[i].lastUsed < ordered[j].lastUsed
		}
		return ordered[i].key < ordered[j].key
	})
	for _, aged := range ordered[:len(ordered)-capacity] {
		entry := c.entries[aged.key]
		delete(c.entries, aged.key)
		evicted = append(evicted, entry.transport)
	}
	return evicted
}

// flush drops every cached transport. Called when a preference change
// invalidates the TLS material the cached transports were built from, so their
// pooled connections do not outlive the settings that authorised them.
func (c *httpTransportCache) flush() {
	c.mu.Lock()
	entries := c.entries
	c.entries = nil
	c.nextSweep.Store(0)
	c.mu.Unlock()
	for _, entry := range entries {
		entry.transport.CloseIdleConnections()
	}
}

func (c *httpTransportCache) size() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.entries)
}

func closeTransports(transports []*http.Transport) {
	for _, transport := range transports {
		transport.CloseIdleConnections()
	}
}

// requestTransport resolves this request's whole security posture — app TLS
// settings, custom CA, matched client certificate, proxy — and returns the
// shared transport for it. It replaces the three-clone chain that used to run
// per request in executeHTTP.
//
// Order of work, and therefore of reported errors, is unchanged: custom CA,
// then client certificate, then proxy.
func (a *App) requestTransport(base http.RoundTripper, settings appTLSSettings, verifyTLS bool, collectionID, targetURL string, vars map[string]string) (http.RoundTripper, error) {
	spec := transportSpec{
		source:          httpTransportSource(base),
		verifyTLS:       verifyTLS,
		customCAEnabled: settings.Request.CustomCaCertificate.Enabled,
		keepDefaultCAs:  boolPtrValue(settings.Request.KeepDefaultCaCertificates.Enabled, true),
		sessionCache:    settings.ClientSessionCache,
	}
	// The custom root store is only consulted when verification is on; with
	// verification off the pre-US-016 chain skipped it entirely.
	if verifyTLS && spec.customCAEnabled {
		certPath, certPEM, err := readCustomCACertificatePEM(settings.Request)
		if err != nil {
			return nil, err
		}
		spec.customCAPath = certPath
		spec.customCAPEM = certPEM
	}
	if collectionPath, certs, ok := a.collectionClientCertificateConfig(collectionID); ok {
		certificate, matched, err := matchingTLSClientCertificate(collectionPath, certs, targetURL, vars)
		if err != nil {
			return nil, err
		}
		if matched {
			spec.clientCert = &certificate
			spec.clientCertDigest = clientCertificateDigest(certificate)
		}
	}
	if err := spec.applyProxyResolution(a.collectionProxyResolution(collectionID), targetURL, vars); err != nil {
		return nil, err
	}
	return a.transportCache.transportFor(spec)
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
var packageTransportCache httpTransportCache

// These are lazily built through plain functions rather than package-level
// sync.OnceValue variables: the PAC client's spec reaches systemProxyURLForRequest,
// which reaches loadPACSource, which uses the PAC client — a variable
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
		transport, err := packageTransportCache.transportFor(transportSpec{verifyTLS: true, proxyMode: transportProxyInherit})
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
		transport, err := packageTransportCache.transportFor(transportSpec{verifyTLS: true, proxyMode: transportProxyOff})
		if err != nil || transport == nil {
			// Unreachable: this spec loads no CA and no client certificate,
			// so build() cannot fail. Fall back to the pre-US-017
			// construction rather than silently changing the proxy posture.
			pacHTTPClient = &http.Client{Timeout: 5 * time.Second, Transport: transportWithoutProxy(http.DefaultTransport)}
			return
		}
		pacHTTPClient = &http.Client{Timeout: 5 * time.Second, Transport: transport}
	})
	return pacHTTPClient
}
