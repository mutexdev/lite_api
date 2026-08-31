package core

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
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	xport "github.com/mutexdev/lite_api/internal/transport"
)

// US-016 / US-017 tests.
//
// The security tests come first on purpose: a cache that shares a transport
// across two different security postures would hand a verify-on request a
// transport built with InsecureSkipVerify, and nothing about the response would
// look wrong. The connection-reuse test is the story's acceptance evidence, but
// it is the less important of the two.

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
		// A UI SEND, SAID EXPLICITLY. executeHTTP's client is guard-wrapped, and
		// strict provenance refuses an unlabeled request through it — so a test
		// that drives the engine below its own root has to supply the label its
		// root would have stamped (§4.5).
		return app.executeHTTP(mcpContextWithUIProvenance(t.Context()), collection.ID, collection, item, map[string]string{}, nil, func(TimelineItem) {})
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

func TestSharedOutboundClientsKeepTheirPreviousPosture(t *testing.T) {
	credential := sharedCredentialHTTPClient()
	if credential.Timeout != 30*time.Second {
		t.Fatalf("credential client timeout changed: %s", credential.Timeout)
	}
	// The posture claim is about what DIALS, and the guard does not dial: it
	// reads the request's provenance, lets an unlabeled or UI-labeled request
	// straight through, and delegates. So the assertion unwraps one layer and
	// makes the same claim it always did — http.DefaultTransport underneath,
	// i.e. verified TLS and the environment proxy.
	guard, ok := credential.Transport.(mcpEgressGuardTransport)
	if !ok {
		t.Fatalf("credential client is no longer guard-wrapped, so OAuth2, AWS and script egress is unchecked: %#v", credential.Transport)
	}
	if guard.base != http.DefaultTransport {
		t.Fatalf("credential client no longer uses http.DefaultTransport: %#v", guard.base)
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

func TestClearSSLSessionCacheFlushesTransportCache(t *testing.T) {
	app := newAppForTest(t)
	spec := xport.Spec{VerifyTLS: false, ProxyMode: xport.ProxyOff}
	if _, err := app.transportCache.TransportFor(spec); err != nil {
		t.Fatal(err)
	}
	if app.transportCache.Size() != 1 {
		t.Fatalf("expected a warm cache, size=%d", app.transportCache.Size())
	}
	if _, err := app.ClearSSLSessionCache(); err != nil {
		t.Fatal(err)
	}
	if app.transportCache.Size() != 0 {
		t.Fatalf("ClearSSLSessionCache left %d cached transports", app.transportCache.Size())
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
// Against the pre-US-016 code this fails, because transport.WithClientCertificate
// and transport.WithManualProxy each Clone() the transport per request and a
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
	ctx := httptrace.WithClientTrace(mcpContextWithUIProvenance(context.Background()), trace)

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
	if app.transportCache.Size() != 1 {
		t.Fatalf("expected exactly one cached transport for one posture, got %d", app.transportCache.Size())
	}
}

// TestNoOneOffOutboundClientsRemain is US-017's consolidation guard.
//
// The other tests pin what the shared clients DO. None of them notices a
// seventh call site appearing next week that builds its own
// http.Client{Timeout: 30 * time.Second} again — that client would work
// perfectly, pass every functional test, and quietly opt out of the transport
// cache, which is the entire point of the story. The regression is invisible
// except as connection churn under load.
//
// Scanning source is a blunt instrument, so this deliberately checks only the
// exact construction the story consolidated, and only in app.go. The two
// fallbacks inside the shared constructors themselves live in
// http_transport_cache.go and are not in scope.
func TestNoOneOffOutboundClientsRemain(t *testing.T) {
	source, err := os.ReadFile("app.go")
	if err != nil {
		t.Fatalf("read app.go: %v", err)
	}

	// a.httpClient is the App's own base client, not a one-off: requestTransport
	// derives from its Transport, so it is the cache's INPUT rather than a
	// bypass of it. Everything else matching is a regression.
	const baseClientField = "httpClient:                  &http.Client{Timeout: 30 * time.Second},"

	var offenders []int
	for i, line := range strings.Split(string(source), "\n") {
		if !strings.Contains(line, "&http.Client{") {
			continue
		}
		if strings.TrimSpace(line) == strings.TrimSpace(baseClientField) {
			continue
		}
		offenders = append(offenders, i+1)
	}
	if len(offenders) > 0 {
		t.Errorf("app.go builds a one-off http.Client at line(s) %v — outbound calls must go through sharedCredentialHTTPClient/sharedPACHTTPClient so they share the US-016 transport cache", offenders)
	}
}

// mustParseURL stayed with the App-level tests when the cache tests moved into
// internal/transport; the package has its own copy.
func mustParseURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse %q: %v", raw, err)
	}
	return parsed
}
