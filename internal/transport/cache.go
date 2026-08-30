package transport

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mutexdev/lite_api/internal/interp"
	"github.com/mutexdev/lite_api/internal/types"
)

const (
	// CacheIdleTTL evicts a transport that has not served a request
	// for this long. Chosen above http.DefaultTransport's 90 s
	// IdleConnTimeout: below it the map would drop entries that still own
	// live idle sockets, forcing a reconnect that the pool could have served.
	CacheIdleTTL = 5 * time.Minute

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
// "no proxy" or one concrete URL, so they collapse into ProxyOff and
// ProxyExplicit: two requests that resolve to the same proxy URL are
// entitled to the same transport however they got there.
const (
	// ProxyInherit leaves the Source transport's own Proxy func in
	// place (http.DefaultTransport's ProxyFromEnvironment, for callers that
	// must not be re-routed through the user's proxy settings).
	ProxyInherit = "inherit"
	// ProxyOff sets Proxy to nil: no proxy, and env vars ignored.
	ProxyOff = "off"
	// ProxyExplicit routes through Spec.ProxyURL.
	ProxyExplicit = "explicit"
	// ProxySystem resolves the proxy per request from the OS.
	ProxySystem = "system"
)

// Spec is the complete set of inputs that determine an outbound
// transport's TLS and proxy behaviour. Every field except Source, clientCert
// and SystemProxyFallbackURL is fed verbatim into cacheKey; those three are
// keyed by identity (pointer, certificate digest) or documented as
// behaviourally irrelevant.
//
// Completeness argument — the transport differs from its Source in exactly two
// respects, TLSClientConfig and Proxy, and nothing else in build() varies:
//
//   - TLSClientConfig.InsecureSkipVerify  <- verifyTLS
//   - TLSClientConfig.RootCAs             <- CustomCAEnabled, CustomCAPEM,
//     keepDefaultCAs
//   - TLSClientConfig.ClientSessionCache  <- sessionCache
//   - TLSClientConfig.Certificates        <- clientCertDigest
//   - everything else in TLSClientConfig (MinVersion, MaxVersion,
//     CipherSuites, NextProtos, ServerName, ...) is inherited untouched from
//     Source, so source's identity covers it
//   - Proxy                               <- ProxyMode, ProxyURL (whose
//     String() carries scheme, host, port AND proxy credentials)
//   - every other transport field (dialer, timeouts, HTTP/2, limits) is
//     inherited untouched from Source, so source's identity covers it
//
// A field is included even when the current build() would ignore it (for
// example SessionCache when VerifyTLS is on and no custom CA is configured).
// An over-specific key costs at most one extra entry; an under-specific key is
// a security hole.
type Spec struct {
	// Source is the transport the clone is taken from. Keyed by pointer:
	// two different base transports must never share a derived transport,
	// because Source contributes every field build() does not set. The
	// pointer cannot be recycled behind our back because the cache entry
	// holds its own reference to Source, keeping it alive for as long as the
	// key that names it exists.
	Source *http.Transport

	// VerifyTLS is the resolved verification decision
	// (requestTLSVerificationEnabled), already folding in the request's
	// VerifyTLS setting and the app's SSLVerification preference.
	VerifyTLS bool

	// CustomCAEnabled, CustomCAPath and CustomCAPEM describe the custom root
	// store. The PEM *content* is keyed as well as the path, so editing the
	// CA file on disk produces a new key rather than silently reusing a
	// transport built from the old roots.
	CustomCAEnabled bool
	CustomCAPath    string
	CustomCAPEM     []byte

	// KeepDefaultCAs decides whether the system roots are kept alongside the
	// custom ones. Same path, same PEM, but system roots dropped, is a
	// materially different trust decision.
	KeepDefaultCAs bool

	// SessionCache is the app-wide TLS session cache. Keyed by pointer:
	// ClearSSLSessionCache installs a fresh cache, and resumption tickets
	// must not leak across that boundary.
	SessionCache tls.ClientSessionCache

	// ClientCert is the matched client identity, and ClientCertDigest is its
	// key contribution: SHA-256 over the DER chain the handshake will
	// present. The chain pins the public key, and both loaders
	// (tls.X509KeyPair and pkcs12.DecodeChain) refuse a private key that
	// does not match it, so the chain digest identifies the whole identity.
	// It also changes when the file on disk changes.
	ClientCert       *tls.Certificate
	ClientCertDigest string

	// ProxyMode and ProxyURL are the resolved proxy disposition. The
	// resolution (bypass lists, PAC evaluation, variable interpolation of
	// host/port/credentials) happens per request in
	// ApplyProxyResolution; only its OUTCOME is keyed, so two requests share
	// a transport exactly when they end up at the same proxy.
	ProxyMode string
	ProxyURL  *url.URL

	// SystemProxyFallbackURL is used only when http.Transport calls the
	// proxy func with a nil request or nil URL, which it never does. It is
	// deliberately NOT part of the key: keying it would defeat sharing for
	// system-proxy users, and the pre-US-016 code already memoised one
	// system-proxy transport per App with whatever URL happened to be first.
	SystemProxyFallbackURL string

	// RefuseSystemPAC makes a ProxySystem transport refuse the dial whenever
	// discovery reports that this machine's answer for the request is a PAC
	// script, instead of fetching and running it.
	//
	// IT IS PART OF THE KEY, and that is the whole reason it is a Spec field
	// rather than an argument to Build. Two ProxySystem transports that differ
	// only in this flag differ in a SECURITY POSTURE: one will fetch and
	// execute a remote JavaScript program to pick its proxy, the other refuses
	// to. Without the flag in the key an agent-initiated send and a UI send
	// would hash to the same entry and the first one to arrive would decide
	// which behaviour the other got — either handing the UI a transport that
	// refuses the user's own PAC, or handing an agent run the one that runs it.
	RefuseSystemPAC bool
}

// pristine reports whether build() would produce a transport indistinguishable
// from source. Those callers get Source itself, which keeps its warm pool and
// costs no map lookup at all.
func (spec Spec) Pristine() bool {
	return spec.VerifyTLS &&
		!spec.CustomCAEnabled &&
		spec.ClientCert == nil &&
		spec.ProxyMode == ProxyInherit
}

// resolvedSource mirrors CloneHTTPTransport's fallback: a base that is not an
// *http.Transport (a test round-tripper, an auth wrapper) carries no dialer or
// TLS config to inherit, so http.DefaultTransport is the Source instead.
func (spec Spec) resolvedSource() *http.Transport {
	if spec.Source != nil {
		return spec.Source
	}
	if fallback, ok := http.DefaultTransport.(*http.Transport); ok && fallback != nil {
		return fallback
	}
	return &http.Transport{}
}

// Source resolves an arbitrary RoundTripper to the *http.Transport
// a clone would be taken from, matching CloneHTTPTransport.
func Source(base http.RoundTripper) *http.Transport {
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
func (spec Spec) CacheKey() string {
	digest := sha256.New()
	field := func(value string) {
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(len(value)))
		_, _ = digest.Write(length[:])
		_, _ = digest.Write([]byte(value))
	}
	field(fmt.Sprintf("%p", spec.resolvedSource()))
	field(fmt.Sprintf("%t", spec.VerifyTLS))
	field(fmt.Sprintf("%t", spec.CustomCAEnabled))
	field(spec.CustomCAPath)
	field(string(spec.CustomCAPEM))
	field(fmt.Sprintf("%t", spec.KeepDefaultCAs))
	field(SessionCacheIdentity(spec.SessionCache))
	field(spec.ClientCertDigest)
	field(spec.ProxyMode)
	if spec.ProxyURL != nil {
		field(spec.ProxyURL.String())
	} else {
		field("")
	}
	field(fmt.Sprintf("%t", spec.RefuseSystemPAC))
	return hex.EncodeToString(digest.Sum(nil))
}

func SessionCacheIdentity(cache tls.ClientSessionCache) string {
	if cache == nil {
		return ""
	}
	return fmt.Sprintf("%p", cache)
}

// ClientCertificateDigest identifies a client identity by the DER chain it
// presents. Returns "" for the empty certificate so "no certificate" and "some
// certificate" can never collide.
func ClientCertificateDigest(certificate tls.Certificate) string {
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
// (transportWithAppTLSSettings -> WithClientCertificate ->
// WithProxyResolution), including its quirks: the TLS config is left
// untouched when verification is on and no custom CA is configured, which is
// also why the app-wide session cache is not installed on that path.
func (spec Spec) Build() (*http.Transport, error) {
	transport := spec.resolvedSource().Clone()
	tlsTouched := !spec.VerifyTLS || spec.CustomCAEnabled
	if tlsTouched || spec.ClientCert != nil {
		tlsConfig := transport.TLSClientConfig
		if tlsConfig == nil {
			tlsConfig = &tls.Config{}
		} else {
			tlsConfig = tlsConfig.Clone()
		}
		if tlsTouched {
			if !spec.VerifyTLS {
				tlsConfig.InsecureSkipVerify = true
			} else if err := ApplyCustomRootCAPEM(tlsConfig, spec.CustomCAPath, spec.CustomCAPEM, spec.KeepDefaultCAs); err != nil {
				return nil, err
			}
			if spec.SessionCache != nil {
				tlsConfig.ClientSessionCache = spec.SessionCache
			}
		}
		if spec.ClientCert != nil {
			tlsConfig.Certificates = append([]tls.Certificate{*spec.ClientCert}, tlsConfig.Certificates...)
		}
		transport.TLSClientConfig = tlsConfig
	}
	switch spec.ProxyMode {
	case ProxyInherit:
		// Keep the source's own Proxy func.
	case ProxySystem:
		transport.Proxy = SystemProxyFunc(spec.SystemProxyFallbackURL, spec.RefuseSystemPAC)
	case ProxyExplicit:
		transport.Proxy = http.ProxyURL(spec.ProxyURL)
	default:
		transport.Proxy = nil
	}
	return transport, nil
}

// ErrSystemPACRefused is returned by a PAC-refusing system-proxy func instead
// of fetching and evaluating the PAC script this machine names.
//
// A SENTINEL RATHER THAN A MESSAGE, because the caller that installed the
// refusal is the one that knows how to phrase it. This package cannot say "run
// this request in the LiteAPI app" — it does not know an app exists — so it
// says the one fact it owns, and internal/core matches on it (through the
// *url.Error http.Transport wraps a proxy-func error in) to raise the §2 row 4
// refusal with the wording and the ErrDenied class the agent needs.
var ErrSystemPACRefused = errors.New("the effective system proxy configuration selects a PAC script")

// SystemProxyFunc builds the per-request proxy func a ProxySystem transport
// uses. fallbackURL answers the (never-taken) nil-request case, matching Spec's
// documentation of that field.
//
// With refusePAC false this is exactly SystemProxyURLForRequest, i.e. discovery
// plus PAC evaluation — the shipped behaviour, unchanged.
//
// With refusePAC true it stops at DISCOVERY. The distinction is the point: a
// PAC file is a remote JavaScript program with its own fetch and its own DNS
// (proxy.go), so "did this machine name a PAC" has to be answerable WITHOUT
// running one, and the refusal has to happen before the first byte moves.
// Discovery never both names a PAC and returns a static proxy, and a PAC
// selection never carries an error, so the three outcomes below are exhaustive.
func SystemProxyFunc(fallbackURL string, refusePAC bool) func(*http.Request) (*url.URL, error) {
	return func(req *http.Request) (*url.URL, error) {
		target := fallbackURL
		if req != nil && req.URL != nil {
			target = req.URL.String()
		}
		if !refusePAC {
			return SystemProxyURLForRequest(target)
		}
		proxyURL, pacURL, err := DiscoverSystemProxy(target)
		if err != nil {
			return nil, err
		}
		if pacURL != "" {
			return nil, ErrSystemPACRefused
		}
		return proxyURL, nil
	}
}

// ApplyCustomRootCAPEM is applyCustomRootCAsToTLSConfig's body, restated
// against PEM bytes that were already read (the read happens per request so the
// key reflects the file's current content). Same error strings.
func ApplyCustomRootCAPEM(tlsConfig *tls.Config, filePath string, certPEM []byte, KeepDefaultCAs bool) error {
	// Gated on the PATH being empty, not the PEM. An empty path is "no custom
	// CA configured", which ReadCustomCACertificatePEM reports for both the
	// disabled feature and a blank setting. An empty PEM is a configured file
	// that turned out to hold nothing, and that is an error — falling through
	// to a nil RootCAs would mean the SYSTEM pool, so a request configured to
	// trust only a custom CA would silently trust every CA on the machine.
	if tlsConfig == nil || filePath == "" {
		return nil
	}
	var roots *x509.CertPool
	if KeepDefaultCAs {
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

// ReadCustomCACertificatePEM reads the configured custom CA file. Returns an
// empty path and nil bytes when the feature is on but no path is set, matching
// applyCustomRootCAsToTLSConfig's early return.
func ReadCustomCACertificatePEM(preferences types.RequestPreferences) (string, []byte, error) {
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

// ApplyProxyResolution resolves a Resolution against this request's URL
// and variables, storing only the outcome on the spec. Mirrors
// WithProxyResolution, including its deliberate swallowing of PAC
// errors (an unreachable or broken PAC file means "go direct", not "fail").
func (spec *Spec) ApplyProxyResolution(resolution Resolution, requestURL string, vars map[string]string) error {
	switch strings.ToLower(strings.TrimSpace(resolution.Mode)) {
	case "manual":
		if !ShouldUseManualProxy(requestURL, interp.Interpolate(resolution.Config.BypassProxy, vars)) {
			spec.ProxyMode = ProxyOff
			return nil
		}
		proxyURL, err := ManualProxyURL(resolution.Config, vars)
		if err != nil {
			return err
		}
		spec.ProxyMode = ProxyExplicit
		spec.ProxyURL = proxyURL
		return nil
	case "system":
		spec.ProxyMode = ProxySystem
		spec.SystemProxyFallbackURL = requestURL
		return nil
	case "pac":
		proxyURL, ok, err := ResolvePACProxyURL(interp.Interpolate(resolution.PACSource, vars), requestURL)
		if err != nil || !ok {
			spec.ProxyMode = ProxyOff
			return nil
		}
		spec.ProxyMode = ProxyExplicit
		spec.ProxyURL = proxyURL
		return nil
	default:
		spec.ProxyMode = ProxyOff
		return nil
	}
}

type cacheEntry struct {
	transport *http.Transport
	// Source is held only to keep the address named by the cache key alive,
	// so the allocator cannot recycle it under a different transport.
	source   *http.Transport
	lastUsed atomic.Int64
}

// Cache maps a security posture to the one transport that serves
// it. The zero value is usable; the map is created on first insert.
//
// Callers MUST NOT mutate a returned transport: it is shared with every other
// request of the same posture, and may be http.DefaultTransport itself.
type Cache struct {
	mu        sync.RWMutex
	entries   map[string]*cacheEntry
	nextSweep atomic.Int64

	// Test seams; zero means "use the constant above".
	now        func() time.Time
	idleTTL    time.Duration
	maxEntries int
}

func (c *Cache) clock() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

func (c *Cache) ttl() time.Duration {
	if c.idleTTL > 0 {
		return c.idleTTL
	}
	return CacheIdleTTL
}

func (c *Cache) capacity() int {
	if c.maxEntries > 0 {
		return c.maxEntries
	}
	return transportCacheMaxEntries
}

// transportFor returns the shared transport for spec, building it on first use.
// The hot path is a read lock and a map lookup; the build happens outside the
// lock so a slow CA-pool construction never blocks other requests.
func (c *Cache) TransportFor(spec Spec) (*http.Transport, error) {
	if spec.Pristine() {
		return spec.resolvedSource(), nil
	}
	key := spec.CacheKey()
	now := c.clock()

	c.mu.RLock()
	hit, found := c.entries[key]
	c.mu.RUnlock()
	if found {
		hit.lastUsed.Store(now.UnixNano())
		c.sweepIfDue(now)
		return hit.transport, nil
	}

	built, err := spec.Build()
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
		c.entries = map[string]*cacheEntry{}
	}
	entry := &cacheEntry{transport: built, source: spec.resolvedSource()}
	entry.lastUsed.Store(now.UnixNano())
	c.entries[key] = entry
	evicted := c.evictLocked(now)
	c.nextSweep.Store(now.Add(transportCacheSweepInterval).UnixNano())
	c.mu.Unlock()

	CloseTransports(evicted)
	return built, nil
}

// sweepIfDue runs the eviction scan at most once per
// transportCacheSweepInterval. On the hit path the common case is one atomic
// load.
//
// The deadline is checked TWICE, and the two checks are one mechanism rather
// than two guards. The first avoids taking the lock at all, which is what keeps
// a cache hit off the mutex; the second covers two goroutines that both passed
// the first before either swept. NEITHER IS OBSERVABLE ON ITS OWN — remove
// either and the behaviour is identical, remove both and every hit scans the
// whole map. The tests pin that property rather than either line, which is why
// a control deleting one of them correctly fails nothing.
func (c *Cache) sweepIfDue(now time.Time) {
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
	CloseTransports(evicted)
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
func (c *Cache) evictLocked(now time.Time) []*http.Transport {
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
func (c *Cache) Flush() {
	c.mu.Lock()
	entries := c.entries
	c.entries = nil
	c.nextSweep.Store(0)
	c.mu.Unlock()
	for _, entry := range entries {
		entry.transport.CloseIdleConnections()
	}
}

func (c *Cache) Size() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.entries)
}

func CloseTransports(transports []*http.Transport) {
	for _, transport := range transports {
		transport.CloseIdleConnections()
	}
}
