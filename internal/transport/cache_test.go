package transport

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"testing"
	"time"
)

// A self-signed leaf, generated per test rather than shared with package
// main's fixture: the cache keys on the certificate's DER chain digest, so a
// test that needs "a different identity" needs a genuinely different chain.
func testTLSClientCertificate(t *testing.T) tls.Certificate {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: "transport-cache-test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

func mustParseURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

// TestTransportCacheKeySeparatesSecurityPostures enumerates one spec per input
// that can change TLS or proxy behaviour and asserts every pair of them hashes
// differently. If a future edit drops a field from cacheKey, two rows here
// collide and this fails.
func TestTransportCacheKeySeparatesSecurityPostures(t *testing.T) {
	sourceA, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		t.Fatal("http.DefaultTransport is not an *http.Transport")
	}
	sourceB := sourceA.Clone()
	certificateA := testTLSClientCertificate(t)
	certificateB := testTLSClientCertificate(t)
	sessionCacheA := tls.NewLRUClientSessionCache(4)
	sessionCacheB := tls.NewLRUClientSessionCache(4)

	base := Spec{Source: sourceA, VerifyTLS: true, KeepDefaultCAs: true, ProxyMode: ProxyOff}
	withSpec := func(mutate func(*Spec)) Spec {
		spec := base
		mutate(&spec)
		return spec
	}

	specs := map[string]Spec{
		"baseline":   base,
		"verify off": withSpec(func(s *Spec) { s.VerifyTLS = false }),
		"custom CA on": withSpec(func(s *Spec) {
			s.CustomCAEnabled = true
			s.CustomCAPath = "/ca.pem"
			s.CustomCAPEM = []byte("PEM-A")
		}),
		"custom CA other content": withSpec(func(s *Spec) {
			s.CustomCAEnabled = true
			s.CustomCAPath = "/ca.pem"
			s.CustomCAPEM = []byte("PEM-B")
		}),
		"custom CA other path": withSpec(func(s *Spec) {
			s.CustomCAEnabled = true
			s.CustomCAPath = "/other.pem"
			s.CustomCAPEM = []byte("PEM-A")
		}),
		"custom CA system roots dropped": withSpec(func(s *Spec) {
			s.CustomCAEnabled = true
			s.CustomCAPath = "/ca.pem"
			s.CustomCAPEM = []byte("PEM-A")
			s.KeepDefaultCAs = false
		}),
		"keep default CAs off": withSpec(func(s *Spec) { s.KeepDefaultCAs = false }),
		"session cache A":      withSpec(func(s *Spec) { s.SessionCache = sessionCacheA }),
		"session cache B":      withSpec(func(s *Spec) { s.SessionCache = sessionCacheB }),
		"client certificate A": withSpec(func(s *Spec) {
			s.ClientCert = &certificateA
			s.ClientCertDigest = ClientCertificateDigest(certificateA)
		}),
		"client certificate B": withSpec(func(s *Spec) {
			s.ClientCert = &certificateB
			s.ClientCertDigest = ClientCertificateDigest(certificateB)
		}),
		"proxy inherited":   withSpec(func(s *Spec) { s.ProxyMode = ProxyInherit }),
		"proxy from system": withSpec(func(s *Spec) { s.ProxyMode = ProxySystem }),
		"proxy explicit": withSpec(func(s *Spec) {
			s.ProxyMode = ProxyExplicit
			s.ProxyURL = mustParseURL(t, "http://proxy.invalid:8080")
		}),
		"proxy explicit port": withSpec(func(s *Spec) {
			s.ProxyMode = ProxyExplicit
			s.ProxyURL = mustParseURL(t, "http://proxy.invalid:8081")
		}),
		"proxy explicit scheme": withSpec(func(s *Spec) {
			s.ProxyMode = ProxyExplicit
			s.ProxyURL = mustParseURL(t, "socks5://proxy.invalid:8080")
		}),
		"proxy explicit user": withSpec(func(s *Spec) {
			s.ProxyMode = ProxyExplicit
			s.ProxyURL = mustParseURL(t, "http://alice:secret@proxy.invalid:8080")
		}),
		"proxy explicit other password": withSpec(func(s *Spec) {
			s.ProxyMode = ProxyExplicit
			s.ProxyURL = mustParseURL(t, "http://alice:other@proxy.invalid:8080")
		}),
		"other source transport": withSpec(func(s *Spec) { s.Source = sourceB }),
	}

	seen := map[string]string{}
	for name, spec := range specs {
		key := spec.CacheKey()
		if previous, clash := seen[key]; clash {
			t.Fatalf("security postures %q and %q share a cache key; one of them would be served a transport built for the other", previous, name)
		}
		seen[key] = name
	}
	if len(seen) != len(specs) {
		t.Fatalf("expected %d distinct keys, got %d", len(specs), len(seen))
	}
}

// TestTransportCacheNeverServesInsecureTransportToVerifiedRequest is the
// concrete form of the constraint: ask for verify-off first so the cache is
// warm with an InsecureSkipVerify transport, then ask for verify-on with every
// other input identical.
func TestTransportCacheNeverServesInsecureTransportToVerifiedRequest(t *testing.T) {
	cache := &Cache{}
	insecureSpec := Spec{VerifyTLS: false, KeepDefaultCAs: true, ProxyMode: ProxyOff}
	secureSpec := insecureSpec
	secureSpec.VerifyTLS = true

	insecure, err := cache.TransportFor(insecureSpec)
	if err != nil {
		t.Fatal(err)
	}
	if insecure.TLSClientConfig == nil || !insecure.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("verify-off posture did not produce an InsecureSkipVerify transport")
	}

	secure, err := cache.TransportFor(secureSpec)
	if err != nil {
		t.Fatal(err)
	}
	if secure == insecure {
		t.Fatal("verify-on request was handed the verify-off transport")
	}
	if secure.TLSClientConfig != nil && secure.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("verify-on request was handed a transport with InsecureSkipVerify")
	}
	// And the warm entry is still the insecure one for insecure callers, so
	// the two postures coexist rather than overwrite each other.
	again, err := cache.TransportFor(insecureSpec)
	if err != nil {
		t.Fatal(err)
	}
	if again != insecure {
		t.Fatal("verify-off posture was not served from the cache after a verify-on request")
	}
}

func TestTransportCacheReusesTransportForIdenticalPosture(t *testing.T) {
	cache := &Cache{}
	spec := Spec{VerifyTLS: false, ProxyMode: ProxyOff}
	first, err := cache.TransportFor(spec)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		next, err := cache.TransportFor(spec)
		if err != nil {
			t.Fatal(err)
		}
		if next != first {
			t.Fatalf("iteration %d built a new transport instead of reusing the cached one", i)
		}
	}
	if cache.Size() != 1 {
		t.Fatalf("expected 1 cache entry, got %d", cache.Size())
	}
}

// TestTransportCachePristineSpecKeepsCallerTransport covers the US-017 posture:
// a spec that changes nothing is answered with the caller's own transport, so
// the credential clients keep http.DefaultTransport's warm pool and the map
// stays empty.
func TestTransportCachePristineSpecKeepsCallerTransport(t *testing.T) {
	cache := &Cache{}
	transport, err := cache.TransportFor(Spec{VerifyTLS: true, ProxyMode: ProxyInherit})
	if err != nil {
		t.Fatal(err)
	}
	if transport != http.DefaultTransport {
		t.Fatalf("pristine spec did not return http.DefaultTransport, got %p", transport)
	}
	if cache.Size() != 0 {
		t.Fatalf("pristine spec should not occupy a cache entry, size=%d", cache.Size())
	}
}

func TestTransportCacheEvictsIdleEntries(t *testing.T) {
	now := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
	cache := &Cache{idleTTL: time.Minute, now: func() time.Time { return now }}

	stale, err := cache.TransportFor(Spec{VerifyTLS: false, ProxyMode: ProxyOff})
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Minute)
	fresh, err := cache.TransportFor(Spec{VerifyTLS: true, ProxyMode: ProxyOff})
	if err != nil {
		t.Fatal(err)
	}
	if stale == fresh {
		t.Fatal("two postures collapsed into one transport")
	}
	if cache.Size() != 1 {
		t.Fatalf("idle entry was not evicted, size=%d", cache.Size())
	}
	// The evicted posture must rebuild rather than come back from the map.
	rebuilt, err := cache.TransportFor(Spec{VerifyTLS: false, ProxyMode: ProxyOff})
	if err != nil {
		t.Fatal(err)
	}
	if rebuilt == stale {
		t.Fatal("evicted transport was still served from the cache")
	}
}

func TestTransportCacheEvictsLeastRecentlyUsedOverCapacity(t *testing.T) {
	now := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
	cache := &Cache{maxEntries: 3, idleTTL: time.Hour, now: func() time.Time { return now }}

	specFor := func(port int) Spec {
		return Spec{
			VerifyTLS: true,
			ProxyMode: ProxyExplicit,
			ProxyURL:  mustParseURL(t, "http://proxy.invalid:"+strconv.Itoa(port)),
		}
	}
	first, err := cache.TransportFor(specFor(9001))
	if err != nil {
		t.Fatal(err)
	}
	for _, port := range []int{9002, 9003, 9004, 9005} {
		now = now.Add(time.Second)
		if _, err := cache.TransportFor(specFor(port)); err != nil {
			t.Fatal(err)
		}
	}
	if cache.Size() != 3 {
		t.Fatalf("capacity not enforced, size=%d want 3", cache.Size())
	}
	now = now.Add(time.Second)
	rebuilt, err := cache.TransportFor(specFor(9001))
	if err != nil {
		t.Fatal(err)
	}
	if rebuilt == first {
		t.Fatal("least recently used entry survived eviction")
	}
	if cache.Size() != 3 {
		t.Fatalf("capacity not enforced after re-insert, size=%d want 3", cache.Size())
	}
}

func TestTransportCacheFlushDropsEveryEntry(t *testing.T) {
	cache := &Cache{}
	spec := Spec{VerifyTLS: false, ProxyMode: ProxyOff}
	before, err := cache.TransportFor(spec)
	if err != nil {
		t.Fatal(err)
	}
	cache.Flush()
	if cache.Size() != 0 {
		t.Fatalf("flush left %d entries", cache.Size())
	}
	after, err := cache.TransportFor(spec)
	if err != nil {
		t.Fatal(err)
	}
	if after == before {
		t.Fatal("flushed transport was served again")
	}
}

// TestTransportCacheConcurrentPosturesStaySeparate is the -race case: the cache
// is shared mutable state on the request hot path.
func TestTransportCacheConcurrentPosturesStaySeparate(t *testing.T) {
	cache := &Cache{}
	const goroutines = 64
	results := make([]*http.Transport, goroutines)
	var wait sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < goroutines; i++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			transport, err := cache.TransportFor(Spec{VerifyTLS: index%2 == 0, ProxyMode: ProxyOff})
			if err != nil {
				t.Error(err)
				return
			}
			results[index] = transport
		}(i)
	}
	close(start)
	wait.Wait()

	distinct := map[*http.Transport]bool{}
	for index, transport := range results {
		if transport == nil {
			t.Fatalf("goroutine %d produced no transport", index)
		}
		distinct[transport] = true
		if index%2 == 0 {
			if transport.TLSClientConfig != nil && transport.TLSClientConfig.InsecureSkipVerify {
				t.Fatalf("goroutine %d asked to verify and got an insecure transport", index)
			}
		} else if transport.TLSClientConfig == nil || !transport.TLSClientConfig.InsecureSkipVerify {
			t.Fatalf("goroutine %d asked to skip verification and got a verifying transport", index)
		}
	}
	if len(distinct) != 2 {
		t.Fatalf("expected exactly 2 transports for 2 postures, got %d", len(distinct))
	}
}
