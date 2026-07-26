package main

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// US-016 / US-017 tests.
//
// The security tests come first on purpose: a cache that shares a transport
// across two different security postures would hand a verify-on request a
// transport built with InsecureSkipVerify, and nothing about the response would
// look wrong. The connection-reuse test is the story's acceptance evidence, but
// it is the less important of the two.

func testTLSClientCertificate(t *testing.T) tls.Certificate {
	t.Helper()
	certPEM, keyPEM, _, _ := testClientCertificate(t)
	certificate, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	return certificate
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

	base := transportSpec{source: sourceA, verifyTLS: true, keepDefaultCAs: true, proxyMode: transportProxyOff}
	withSpec := func(mutate func(*transportSpec)) transportSpec {
		spec := base
		mutate(&spec)
		return spec
	}

	specs := map[string]transportSpec{
		"baseline":   base,
		"verify off": withSpec(func(s *transportSpec) { s.verifyTLS = false }),
		"custom CA on": withSpec(func(s *transportSpec) {
			s.customCAEnabled = true
			s.customCAPath = "/ca.pem"
			s.customCAPEM = []byte("PEM-A")
		}),
		"custom CA other content": withSpec(func(s *transportSpec) {
			s.customCAEnabled = true
			s.customCAPath = "/ca.pem"
			s.customCAPEM = []byte("PEM-B")
		}),
		"custom CA other path": withSpec(func(s *transportSpec) {
			s.customCAEnabled = true
			s.customCAPath = "/other.pem"
			s.customCAPEM = []byte("PEM-A")
		}),
		"custom CA system roots dropped": withSpec(func(s *transportSpec) {
			s.customCAEnabled = true
			s.customCAPath = "/ca.pem"
			s.customCAPEM = []byte("PEM-A")
			s.keepDefaultCAs = false
		}),
		"keep default CAs off": withSpec(func(s *transportSpec) { s.keepDefaultCAs = false }),
		"session cache A":      withSpec(func(s *transportSpec) { s.sessionCache = sessionCacheA }),
		"session cache B":      withSpec(func(s *transportSpec) { s.sessionCache = sessionCacheB }),
		"client certificate A": withSpec(func(s *transportSpec) {
			s.clientCert = &certificateA
			s.clientCertDigest = clientCertificateDigest(certificateA)
		}),
		"client certificate B": withSpec(func(s *transportSpec) {
			s.clientCert = &certificateB
			s.clientCertDigest = clientCertificateDigest(certificateB)
		}),
		"proxy inherited":   withSpec(func(s *transportSpec) { s.proxyMode = transportProxyInherit }),
		"proxy from system": withSpec(func(s *transportSpec) { s.proxyMode = transportProxySystem }),
		"proxy explicit": withSpec(func(s *transportSpec) {
			s.proxyMode = transportProxyExplicit
			s.proxyURL = mustParseURL(t, "http://proxy.invalid:8080")
		}),
		"proxy explicit port": withSpec(func(s *transportSpec) {
			s.proxyMode = transportProxyExplicit
			s.proxyURL = mustParseURL(t, "http://proxy.invalid:8081")
		}),
		"proxy explicit scheme": withSpec(func(s *transportSpec) {
			s.proxyMode = transportProxyExplicit
			s.proxyURL = mustParseURL(t, "socks5://proxy.invalid:8080")
		}),
		"proxy explicit user": withSpec(func(s *transportSpec) {
			s.proxyMode = transportProxyExplicit
			s.proxyURL = mustParseURL(t, "http://alice:secret@proxy.invalid:8080")
		}),
		"proxy explicit other password": withSpec(func(s *transportSpec) {
			s.proxyMode = transportProxyExplicit
			s.proxyURL = mustParseURL(t, "http://alice:other@proxy.invalid:8080")
		}),
		"other source transport": withSpec(func(s *transportSpec) { s.source = sourceB }),
	}

	seen := map[string]string{}
	for name, spec := range specs {
		key := spec.cacheKey()
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
	cache := &httpTransportCache{}
	insecureSpec := transportSpec{verifyTLS: false, keepDefaultCAs: true, proxyMode: transportProxyOff}
	secureSpec := insecureSpec
	secureSpec.verifyTLS = true

	insecure, err := cache.transportFor(insecureSpec)
	if err != nil {
		t.Fatal(err)
	}
	if insecure.TLSClientConfig == nil || !insecure.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("verify-off posture did not produce an InsecureSkipVerify transport")
	}

	secure, err := cache.transportFor(secureSpec)
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
	again, err := cache.transportFor(insecureSpec)
	if err != nil {
		t.Fatal(err)
	}
	if again != insecure {
		t.Fatal("verify-off posture was not served from the cache after a verify-on request")
	}
}

// TestExecuteHTTPSeparatesVerifyPosturesEndToEnd proves the same thing through
// the real request path against a server whose certificate is not trusted:
// whichever posture runs first, the verify-on request must still fail.
func TestExecuteHTTPSeparatesVerifyPosturesEndToEnd(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	send := func(t *testing.T, app *App, verifyTLS bool) Response {
		t.Helper()
		collection := Collection{ID: "posture-collection", Name: "posture", Format: "bru"}
		item := RequestItem{ID: "posture-request", Type: "http", Method: http.MethodGet, URL: server.URL}
		item.Settings.VerifyTLS = verifyTLS
		return app.executeHTTP(t.Context(), collection.ID, collection, item, map[string]string{}, nil, func(TimelineItem) {})
	}

	for _, order := range []struct {
		name  string
		first bool
	}{
		{name: "verify off first", first: false},
		{name: "verify on first", first: true},
	} {
		t.Run(order.name, func(t *testing.T) {
			app := newAppForTest(t)
			_ = send(t, app, order.first)

			insecure := send(t, app, false)
			if insecure.Error != "" || insecure.Status != http.StatusOK {
				t.Fatalf("verify-off send failed: status=%d error=%q", insecure.Status, insecure.Error)
			}
			secure := send(t, app, true)
			if secure.Error == "" {
				t.Fatalf("verify-on send succeeded against an untrusted certificate: it was served the verify-off transport (status=%d)", secure.Status)
			}
			if !strings.Contains(secure.Error, "certificate") {
				t.Fatalf("verify-on send failed for the wrong reason: %q", secure.Error)
			}
		})
	}
}

func TestTransportCacheReusesTransportForIdenticalPosture(t *testing.T) {
	cache := &httpTransportCache{}
	spec := transportSpec{verifyTLS: false, proxyMode: transportProxyOff}
	first, err := cache.transportFor(spec)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		next, err := cache.transportFor(spec)
		if err != nil {
			t.Fatal(err)
		}
		if next != first {
			t.Fatalf("iteration %d built a new transport instead of reusing the cached one", i)
		}
	}
	if cache.size() != 1 {
		t.Fatalf("expected 1 cache entry, got %d", cache.size())
	}
}

// TestTransportCachePristineSpecKeepsCallerTransport covers the US-017 posture:
// a spec that changes nothing is answered with the caller's own transport, so
// the credential clients keep http.DefaultTransport's warm pool and the map
// stays empty.
func TestTransportCachePristineSpecKeepsCallerTransport(t *testing.T) {
	cache := &httpTransportCache{}
	transport, err := cache.transportFor(transportSpec{verifyTLS: true, proxyMode: transportProxyInherit})
	if err != nil {
		t.Fatal(err)
	}
	if transport != http.DefaultTransport {
		t.Fatalf("pristine spec did not return http.DefaultTransport, got %p", transport)
	}
	if cache.size() != 0 {
		t.Fatalf("pristine spec should not occupy a cache entry, size=%d", cache.size())
	}
}

func TestSharedOutboundClientsKeepTheirPreviousPosture(t *testing.T) {
	credential := sharedCredentialHTTPClient()
	if credential.Timeout != 30*time.Second {
		t.Fatalf("credential client timeout changed: %s", credential.Timeout)
	}
	if credential.Transport != http.DefaultTransport {
		t.Fatalf("credential client no longer uses http.DefaultTransport: %#v", credential.Transport)
	}
	if credential != sharedCredentialHTTPClient() {
		t.Fatal("credential client is not shared across calls")
	}

	pac := sharedPACHTTPClient()
	if pac.Timeout != 5*time.Second {
		t.Fatalf("PAC client timeout changed: %s", pac.Timeout)
	}
	transport, ok := pac.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("PAC client transport is not an *http.Transport: %#v", pac.Transport)
	}
	if transport.Proxy != nil {
		t.Fatal("PAC client would now fetch through a proxy; it must always go direct")
	}
	if pac != sharedPACHTTPClient() {
		t.Fatal("PAC client is not shared across calls")
	}
}

func TestTransportCacheEvictsIdleEntries(t *testing.T) {
	now := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
	cache := &httpTransportCache{idleTTL: time.Minute, now: func() time.Time { return now }}

	stale, err := cache.transportFor(transportSpec{verifyTLS: false, proxyMode: transportProxyOff})
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Minute)
	fresh, err := cache.transportFor(transportSpec{verifyTLS: true, proxyMode: transportProxyOff})
	if err != nil {
		t.Fatal(err)
	}
	if stale == fresh {
		t.Fatal("two postures collapsed into one transport")
	}
	if cache.size() != 1 {
		t.Fatalf("idle entry was not evicted, size=%d", cache.size())
	}
	// The evicted posture must rebuild rather than come back from the map.
	rebuilt, err := cache.transportFor(transportSpec{verifyTLS: false, proxyMode: transportProxyOff})
	if err != nil {
		t.Fatal(err)
	}
	if rebuilt == stale {
		t.Fatal("evicted transport was still served from the cache")
	}
}

func TestTransportCacheEvictsLeastRecentlyUsedOverCapacity(t *testing.T) {
	now := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
	cache := &httpTransportCache{maxEntries: 3, idleTTL: time.Hour, now: func() time.Time { return now }}

	specFor := func(port int) transportSpec {
		return transportSpec{
			verifyTLS: true,
			proxyMode: transportProxyExplicit,
			proxyURL:  mustParseURL(t, "http://proxy.invalid:"+strconv.Itoa(port)),
		}
	}
	first, err := cache.transportFor(specFor(9001))
	if err != nil {
		t.Fatal(err)
	}
	for _, port := range []int{9002, 9003, 9004, 9005} {
		now = now.Add(time.Second)
		if _, err := cache.transportFor(specFor(port)); err != nil {
			t.Fatal(err)
		}
	}
	if cache.size() != 3 {
		t.Fatalf("capacity not enforced, size=%d want 3", cache.size())
	}
	now = now.Add(time.Second)
	rebuilt, err := cache.transportFor(specFor(9001))
	if err != nil {
		t.Fatal(err)
	}
	if rebuilt == first {
		t.Fatal("least recently used entry survived eviction")
	}
	if cache.size() != 3 {
		t.Fatalf("capacity not enforced after re-insert, size=%d want 3", cache.size())
	}
}

func TestTransportCacheFlushDropsEveryEntry(t *testing.T) {
	cache := &httpTransportCache{}
	spec := transportSpec{verifyTLS: false, proxyMode: transportProxyOff}
	before, err := cache.transportFor(spec)
	if err != nil {
		t.Fatal(err)
	}
	cache.flush()
	if cache.size() != 0 {
		t.Fatalf("flush left %d entries", cache.size())
	}
	after, err := cache.transportFor(spec)
	if err != nil {
		t.Fatal(err)
	}
	if after == before {
		t.Fatal("flushed transport was served again")
	}
}

func TestClearSSLSessionCacheFlushesTransportCache(t *testing.T) {
	app := newAppForTest(t)
	spec := transportSpec{verifyTLS: false, proxyMode: transportProxyOff}
	if _, err := app.transportCache.transportFor(spec); err != nil {
		t.Fatal(err)
	}
	if app.transportCache.size() != 1 {
		t.Fatalf("expected a warm cache, size=%d", app.transportCache.size())
	}
	if _, err := app.ClearSSLSessionCache(); err != nil {
		t.Fatal(err)
	}
	if app.transportCache.size() != 0 {
		t.Fatalf("ClearSSLSessionCache left %d cached transports", app.transportCache.size())
	}
}

// TestTransportCacheConcurrentPosturesStaySeparate is the -race case: the cache
// is shared mutable state on the request hot path.
func TestTransportCacheConcurrentPosturesStaySeparate(t *testing.T) {
	cache := &httpTransportCache{}
	const goroutines = 64
	results := make([]*http.Transport, goroutines)
	var wait sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < goroutines; i++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			transport, err := cache.transportFor(transportSpec{verifyTLS: index%2 == 0, proxyMode: transportProxyOff})
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

type countingListener struct {
	net.Listener
	accepts atomic.Int64
}

func (l *countingListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err == nil {
		l.accepts.Add(1)
	}
	return conn, err
}

// startCONNECTProxy runs a loopback HTTP proxy that tunnels CONNECT requests,
// counting the TCP connections its clients open. One tunnel per client
// connection, so the accept count is exactly the number of TCP connections the
// transport under test opened.
func startCONNECTProxy(t *testing.T) *countingListener {
	t.Helper()
	base, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	listener := &countingListener{Listener: base}
	var tracked struct {
		sync.Mutex
		conns []net.Conn
	}
	track := func(conns ...net.Conn) {
		tracked.Lock()
		tracked.conns = append(tracked.conns, conns...)
		tracked.Unlock()
	}
	server := &http.Server{
		ReadHeaderTimeout: 10 * time.Second,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodConnect {
				http.Error(w, "this fixture proxy only speaks CONNECT", http.StatusMethodNotAllowed)
				return
			}
			upstream, dialErr := net.Dial("tcp", r.Host)
			if dialErr != nil {
				http.Error(w, dialErr.Error(), http.StatusBadGateway)
				return
			}
			hijacker, ok := w.(http.Hijacker)
			if !ok {
				_ = upstream.Close()
				http.Error(w, "hijack unsupported", http.StatusInternalServerError)
				return
			}
			client, _, hijackErr := hijacker.Hijack()
			if hijackErr != nil {
				_ = upstream.Close()
				return
			}
			track(client, upstream)
			if _, writeErr := client.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n")); writeErr != nil {
				return
			}
			go func() { _, _ = io.Copy(upstream, client) }()
			go func() { _, _ = io.Copy(client, upstream) }()
		}),
	}
	// Serve always returns a non-nil error once the listener is closed.
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() {
		tracked.Lock()
		for _, conn := range tracked.conns {
			_ = conn.Close()
		}
		tracked.Unlock()
		_ = server.Close()
	})
	return listener
}

// TestExecuteHTTPReusesOneConnectionThroughProxyAndClientCertificate is US-016's
// acceptance evidence: with a proxy AND a matching client certificate
// configured, N sequential sends open ONE TCP connection.
//
// Against the pre-US-016 code this fails, because transportWithClientCertificate
// and transportWithManualProxy each Clone() the transport per request and a
// clone starts with an empty connection pool: it reports 5 connections and 0
// reuses.
func TestExecuteHTTPReusesOneConnectionThroughProxyAndClientCertificate(t *testing.T) {
	const sends = 5

	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
			http.Error(w, "client certificate required", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"mtls":true}`))
	}))
	serverListener := &countingListener{Listener: server.Listener}
	server.Listener = serverListener
	server.TLS = &tls.Config{ClientAuth: tls.RequireAnyClientCert}
	server.StartTLS()
	defer server.Close()

	proxy := startCONNECTProxy(t)
	proxyHost, proxyPort, err := net.SplitHostPort(proxy.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	targetURL := mustParseURL(t, server.URL)

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	item := collection.Items[0]

	certPEM, keyPEM, _, _ := testClientCertificate(t)
	certDir := filepath.Join(collection.Path, "certs")
	if err := os.MkdirAll(certDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(certDir, "client.pem"), certPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(certDir, "client.key"), keyPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := app.UpdateCollectionClientCertificates(collection.ID, []ClientCertificateConfig{{
		Domain:       targetURL.Host,
		Type:         "cert",
		CertFilePath: "certs/client.pem",
		KeyFilePath:  "certs/client.key",
	}}); err != nil {
		t.Fatal(err)
	}
	state, err = app.UpdateCollectionProxy(collection.ID, ProxyConfig{
		Protocol: "http",
		Hostname: proxyHost,
		Port:     proxyPort,
	})
	if err != nil {
		t.Fatal(err)
	}
	collection = state.Workspaces[0].Collections[0]

	item.Type = "http"
	item.Method = http.MethodGet
	item.URL = server.URL
	item.Body.Mode = "none"
	item.Settings.VerifyTLS = false

	var connects, reused atomic.Int64
	trace := &httptrace.ClientTrace{
		ConnectStart: func(string, string) { connects.Add(1) },
		GotConn: func(info httptrace.GotConnInfo) {
			if info.Reused {
				reused.Add(1)
			}
		},
	}
	ctx := httptrace.WithClientTrace(context.Background(), trace)

	for i := 0; i < sends; i++ {
		response := app.executeHTTP(ctx, collection.ID, collection, item, map[string]string{}, nil, func(TimelineItem) {})
		if response.Error != "" || response.Status != http.StatusOK {
			t.Fatalf("send %d failed: status=%d error=%q body=%q", i, response.Status, response.Error, response.Body)
		}
		if !strings.Contains(response.Body, `"mtls":true`) {
			t.Fatalf("send %d did not reach the mTLS target: %q", i, response.Body)
		}
	}

	if got := proxy.accepts.Load(); got != 1 {
		t.Fatalf("%d sends opened %d TCP connections to the proxy, want 1 (clone-per-request rebuilds an empty connection pool)", sends, got)
	}
	if got := serverListener.accepts.Load(); got != 1 {
		t.Fatalf("%d sends opened %d TCP connections to the mTLS target, want 1", sends, got)
	}
	if got := connects.Load(); got != 1 {
		t.Fatalf("client dialled %d times for %d sends, want 1", got, sends)
	}
	if got := reused.Load(); got != sends-1 {
		t.Fatalf("client reused a pooled connection %d times, want %d", got, sends-1)
	}
	if app.transportCache.size() != 1 {
		t.Fatalf("expected exactly one cached transport for one posture, got %d", app.transportCache.size())
	}
}
