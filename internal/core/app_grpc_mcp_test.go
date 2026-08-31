package core

// The gRPC half of the MCP destination boundary — §4.7's target allowlist,
// §4.3's pre-dial checkpoint, §4.4's client-certificate contract, and §1.1's
// trusted-proxy path 3.
//
// Every case here is a bypass if it goes the other way, so each test names the
// bypass rather than restating the code.

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/interop/grpc_testing"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/reflection"

	"github.com/mutexdev/lite_api/internal/types"
)

// --- fixtures --------------------------------------------------------------

// mcpGRPCItem is a gRPC request that resolves its method over server
// reflection, so the fixture needs no .proto on disk.
func mcpGRPCItem(targetURL string) RequestItem {
	item := types.NewRequestItem("Reflected unary", "grpc", 1)
	item.URL = targetURL
	item.Method = "grpc.testing.TestService/UnaryCall"
	item.ProtoPath = ""
	item.GrpcMessages = []GrpcMessage{{Name: "message 1", Content: `{"fillUsername": true}`}}
	item.Settings.TimeoutMs = 10000
	// The test servers below use self-signed certificates.
	item.Settings.VerifyTLS = false
	return item
}

// mcpGRPCScope is a definition scope whose main set holds exactly the origin of
// definitionTarget, resolved through §4.7's grammar — the same function the
// egress itself goes through, so "what the definition points at" and "what was
// dialed" are compared on identical terms.
func mcpGRPCScope(t *testing.T, requestID, definitionTarget string, baseVars map[string]string) mcpScopeOrigins {
	t.Helper()
	scope := mcpScopeOrigins{site: testSite(requestID), mainURL: definitionTarget, baseVars: baseVars}
	if strings.TrimSpace(definitionTarget) == "" {
		return scope
	}
	_, origin, err := mcpValidateGRPCTarget(definitionTarget)
	if err != nil {
		t.Fatalf("fixture target %q is not a valid gRPC target: %v", definitionTarget, err)
	}
	scope.add(egressKindMain, origin)
	return scope
}

// mcpGRPCPolicy is a policy with one scope and no approval paths at all: an
// origin outside Base is denied rather than prompted, which is what a headless
// run does anyway (§4.2).
func mcpGRPCPolicy(t *testing.T, definitionTarget string, baseVars map[string]string) *mcpEgressPolicy {
	t.Helper()
	policy := newMCPEgressPolicy()
	policy.SetScope(mcpGRPCScope(t, "req_grpc", definitionTarget, baseVars))
	return policy
}

// acceptCounter is the socket watcher §10 asks for: a listener that accepts and
// immediately closes, counting every connection that reached it. A refused
// target must leave this at zero.
type acceptCounter struct {
	listener net.Listener
	count    atomic.Int32
}

func newAcceptCounter(t *testing.T) *acceptCounter {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	watcher := &acceptCounter{listener: listener}
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			watcher.count.Add(1)
			_ = conn.Close()
		}
	}()
	t.Cleanup(func() { _ = listener.Close() })
	return watcher
}

func (w *acceptCounter) addr() string { return w.listener.Addr().String() }

// --- §4.7: the allowlist and the pin ---------------------------------------

// THE ALLOWLIST IS THE WHOLE gRPC BOUNDARY. A gRPC target is a resolver
// expression, not a URL: unix sockets, abstract sockets and xds control planes
// are all spellable, and none of them has an origin this boundary can check. So
// the grammar admits exactly a TCP authority and the pin is generated
// explicitly, and both halves are asserted here — an accepted target that
// pinned to the wrong string would dial somewhere the checkpoint never saw.
func TestMCPGRPCTargetAllowlist(t *testing.T) {
	t.Run("accepted", func(t *testing.T) {
		cases := []struct {
			raw        string
			wantPinned string
			wantOrigin Origin
		}{
			{"localhost:50051", "dns:///localhost:50051", Origin{Scheme: "http", Host: "localhost", Port: 50051}},
			{"[::1]:50051", "dns:///[::1]:50051", Origin{Scheme: "http", Host: "::1", Port: 50051}},
			// No port written down means 443 — for plaintext too. This is
			// grpc-go's DNS-resolver default, NOT the http/https 80/443 rule,
			// and the pin must never be left implicit: dns:///[::1] would let
			// grpc-go pick the port after the checkpoint ran.
			{"[::1]", "dns:///[::1]:443", Origin{Scheme: "http", Host: "::1", Port: 443}},
			{"grpc://h:80", "dns:///h:80", Origin{Scheme: "http", Host: "h", Port: 80}},
			{"grpcs://[::1]:443", "dns:///[::1]:443", Origin{Scheme: "https", Host: "::1", Port: 443}},
			{"api.example.com", "dns:///api.example.com:443", Origin{Scheme: "http", Host: "api.example.com", Port: 443}},
			{"127.0.0.1:8080", "dns:///127.0.0.1:8080", Origin{Scheme: "http", Host: "127.0.0.1", Port: 8080}},
			// The scheme is case-insensitive and the host is not identity, so
			// both are normalized before they can produce two origins for one
			// destination.
			{"GRPCS://API.Example.COM", "dns:///api.example.com:443", Origin{Scheme: "https", Host: "api.example.com", Port: 443}},
			{"  localhost:50051  ", "dns:///localhost:50051", Origin{Scheme: "http", Host: "localhost", Port: 50051}},
			// Every spelling of one IPv6 address is one destination.
			{"[::0001]:50051", "dns:///[::1]:50051", Origin{Scheme: "http", Host: "::1", Port: 50051}},
		}
		for _, testCase := range cases {
			pinned, origin, err := mcpValidateGRPCTarget(testCase.raw)
			if err != nil {
				t.Errorf("mcpValidateGRPCTarget(%q) refused a valid TCP authority: %v", testCase.raw, err)
				continue
			}
			if pinned != testCase.wantPinned {
				t.Errorf("mcpValidateGRPCTarget(%q) pinned %q, want %q", testCase.raw, pinned, testCase.wantPinned)
			}
			if origin != testCase.wantOrigin {
				t.Errorf("mcpValidateGRPCTarget(%q) origin = %+v, want %+v", testCase.raw, origin, testCase.wantOrigin)
			}
		}
	})

	t.Run("refused", func(t *testing.T) {
		cases := []string{
			// The non-TCP resolvers. Each one is an egress with no origin.
			"unix:/socket",
			"unix:sock",
			"unix://socket",
			"unix:///var/run/socket",
			"grpc+unix:///var/run/socket",
			"unix-abstract:name",
			"dns:///h",
			"dns://h",
			"xds://h",
			"xds:///h",
			"passthrough:///h",
			// Not gRPC schemes at all.
			"http://h",
			"https://h",
			"grpc+tls://h",
			// The port rule.
			"host:",
			"host:0",
			"host:70000",
			"host:abc",
			"host:-1",
			"host:065535",
			"a:b:c",
			"::1",
			"[::1]:0",
			"[::1]:abc",
			// Delimiters, every one of them.
			"h/x",
			"h\\x",
			"h?x",
			"h#x",
			"user@h",
			"h%20ost",
			"h ost",
			"h\tost",
			"h\nost",
			"h\x00ost",
			"h ost",
			// Bracket shapes.
			"[::1",
			"[::1]]:1",
			"[not-an-ip]",
			"[::1]x",
			"[]",
			// Host shapes.
			"-bad.example",
			"bad-.example",
			"h..x",
			"h.x.",
			"",
			"   ",
			"grpc://",
			"grpcs://",
			"grpc://unix:/x",
		}
		for _, raw := range cases {
			pinned, origin, err := mcpValidateGRPCTarget(raw)
			if err == nil {
				t.Errorf("mcpValidateGRPCTarget(%q) accepted a target that is not a plain TCP authority (pinned %q, origin %+v)", raw, pinned, origin)
				continue
			}
			denied(t, err)
			if pinned != "" || origin != (Origin{}) {
				t.Errorf("mcpValidateGRPCTarget(%q) refused but still produced pinned=%q origin=%+v", raw, pinned, origin)
			}
		}
	})

	// A REFUSAL MUST COST ZERO BYTES. The refusal happens inside
	// grpcDialConfigForRequestContext, which returns an error — so grpc.NewClient
	// is never constructed and no resolver is instantiated. The listener proves
	// the consequence: these targets all name a live socket, spelled in a shape
	// the grammar refuses, and nothing arrives.
	t.Run("refusal never dials", func(t *testing.T) {
		watcher := newAcceptCounter(t)
		app := newAppForTest(t)
		collection := Collection{ID: "col_grpc", Name: "gRPC", Path: t.TempDir()}
		for _, raw := range []string{
			"http://" + watcher.addr(),
			"dns:///" + watcher.addr(),
			"passthrough:///" + watcher.addr(),
			"unix:/tmp/liteapi-not-a-socket",
		} {
			item := mcpGRPCItem(raw)
			policy := mcpGRPCPolicy(t, "127.0.0.1:1", nil)
			ctx := mcpContextWithPolicy(context.Background(), policy)
			response := app.executeGRPC(ctx, collection, item, nil)
			if response.Error == "" {
				t.Fatalf("target %q was executed instead of refused: %#v", raw, response)
			}
			if !strings.Contains(response.Error, "not a plain TCP authority") {
				t.Fatalf("target %q failed for the wrong reason: %q", raw, response.Error)
			}
		}
		if got := watcher.count.Load(); got != 0 {
			t.Fatalf("%d connection(s) reached the listener; a refused gRPC target must dial nothing", got)
		}
	})
}

// mcpParseGRPCAuthority's contract on its own, because the pin depends on
// hasPort and a parser that reported a port it did not find would silently
// change the effective-port rule.
func TestMCPParseGRPCAuthorityReportsWhetherAPortWasWritten(t *testing.T) {
	host, port, hasPort, err := mcpParseGRPCAuthority("api.example.com")
	if err != nil || host != "api.example.com" || port != "" || hasPort {
		t.Fatalf("bare host parsed as host=%q port=%q hasPort=%v err=%v", host, port, hasPort, err)
	}
	host, port, hasPort, err = mcpParseGRPCAuthority("api.example.com:8443")
	if err != nil || host != "api.example.com" || port != "8443" || !hasPort {
		t.Fatalf("host:port parsed as host=%q port=%q hasPort=%v err=%v", host, port, hasPort, err)
	}
	host, port, hasPort, err = mcpParseGRPCAuthority("[fe80::1]")
	if err != nil || host != "fe80::1" || port != "" || hasPort {
		t.Fatalf("bracketed IPv6 parsed as host=%q port=%q hasPort=%v err=%v", host, port, hasPort, err)
	}
}

// §2 row 3 / §5 row 9. A unix-socket gRPC target is the case the whole allowlist
// exists for: it is a real egress with no origin at all, so no approval could
// ever be about it. Zero dial, and the socket proves it.
func TestMCPUnixGRPCZeroDial(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "grpc.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	var accepted atomic.Int32
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			accepted.Add(1)
			_ = conn.Close()
		}
	}()

	app := newAppForTest(t)
	collection := Collection{ID: "col_grpc", Name: "gRPC", Path: t.TempDir()}
	for _, raw := range []string{
		"unix://" + socketPath,
		"unix:" + socketPath,
		"grpc+unix://" + socketPath,
		"unix-abstract:liteapi",
	} {
		item := mcpGRPCItem(raw)
		policy := mcpGRPCPolicy(t, "127.0.0.1:1", nil)
		response := app.executeGRPC(mcpContextWithPolicy(context.Background(), policy), collection, item, nil)
		if response.Error == "" || !strings.Contains(response.Error, "not a plain TCP authority") {
			t.Fatalf("unix target %q was not refused: %#v", raw, response)
		}
	}
	if got := accepted.Load(); got != 0 {
		t.Fatalf("%d connection(s) reached the unix socket; the refusal must precede the dial", got)
	}

	// THE UI PATH KEEPS UNIX SUPPORT, at the same seam. §1.2(4): a
	// user-initiated send is never subjected to any of this.
	item := mcpGRPCItem("unix://" + socketPath)
	dialConfig, err := app.grpcDialConfigForRequestContext(context.Background(), collection, item, item.URL, nil)
	if err != nil {
		t.Fatalf("the UI path refused a unix socket: %v", err)
	}
	if dialConfig.Target != "passthrough:///liteapi-unix-socket" || len(dialConfig.Options) == 0 {
		t.Fatalf("the UI unix dial configuration changed: %#v", dialConfig)
	}
}

// --- the pre-dial checkpoint ------------------------------------------------

// The pinned target — never the raw input — must be what grpc.NewClient
// receives, and an authorized target must still work end to end. If the pin
// were wrong the channel would open somewhere the checkpoint never authorized.
func TestMCPGRPCAuthorizedTargetDialsThePinnedAuthority(t *testing.T) {
	address, stop := startReflectedTestService(t, map[string]string{})
	defer stop()

	app := newAppForTest(t)
	collection := Collection{ID: "col_grpc", Name: "gRPC", Path: t.TempDir()}
	item := mcpGRPCItem(address)
	policy := mcpGRPCPolicy(t, address, nil)

	dialConfig, err := app.grpcDialConfigForRequestContext(mcpContextWithPolicy(context.Background(), policy), collection, item, item.URL, nil)
	if err != nil {
		t.Fatalf("the request's own destination was refused: %v", err)
	}
	if want := "dns:///" + address; dialConfig.Target != want {
		t.Fatalf("dial target = %q, want the pinned %q", dialConfig.Target, want)
	}

	response := app.executeGRPC(mcpContextWithPolicy(context.Background(), policy), collection, item, nil)
	if response.Error != "" || response.Status != http.StatusOK {
		t.Fatalf("an authorized gRPC call failed: %#v", response)
	}
}

// §4.3: an origin outside Base is denied BEFORE the dial, and the one channel
// covers reflection as well as the invoke — so a denied run must not even reach
// the reflection call.
func TestMCPGRPCUnauthorizedTargetBlockedBeforeTheDial(t *testing.T) {
	watcher := newAcceptCounter(t)
	app := newAppForTest(t)
	collection := Collection{ID: "col_grpc", Name: "gRPC", Path: t.TempDir()}
	item := mcpGRPCItem(watcher.addr())
	// The definition points at a different port on the same host. §1.4(9):
	// localhost is not one place.
	policy := mcpGRPCPolicy(t, "127.0.0.1:1", nil)

	response := app.executeGRPC(mcpContextWithPolicy(context.Background(), policy), collection, item, nil)
	if response.Error == "" {
		t.Fatalf("an unauthorized gRPC target was dialed: %#v", response)
	}
	if !strings.Contains(response.Error, "denied") {
		t.Fatalf("the refusal did not read as a denial: %q", response.Error)
	}
	if got := watcher.count.Load(); got != 0 {
		t.Fatalf("%d connection(s) reached the unauthorized target", got)
	}
}

// §1.2(4) and §2's promise that a UI Send keeps every capability: with no
// policy on the context the target is not pinned, not validated, and not
// checked.
func TestUIGRPCSendUnaffectedByTheTargetAllowlist(t *testing.T) {
	address, stop := startReflectedTestService(t, map[string]string{})
	defer stop()

	app := newAppForTest(t)
	collection := Collection{ID: "col_grpc", Name: "gRPC", Path: t.TempDir()}
	item := mcpGRPCItem(address)

	uiCtx := mcpContextWithUIProvenance(context.Background())
	dialConfig, err := app.grpcDialConfigForRequestContext(uiCtx, collection, item, item.URL, nil)
	if err != nil {
		t.Fatalf("the UI path refused a bare authority: %v", err)
	}
	if dialConfig.Target != address {
		t.Fatalf("the UI dial target was rewritten to %q; it must stay the raw authority %q", dialConfig.Target, address)
	}
	response := app.executeGRPC(uiCtx, collection, item, nil)
	if response.Error != "" || response.Status != http.StatusOK {
		t.Fatalf("a UI gRPC send failed: %#v", response)
	}
}

// --- §4.4 / §5 row 17b: the client-certificate contract ---------------------

// tlsGRPCService starts a TLS gRPC server that records what the client
// presented. clientAuth decides whether a certificate is required or merely
// requested — the "requested" form is what proves the ABSENCE of a certificate,
// because the handshake completes either way.
func tlsGRPCService(t *testing.T, clientAuth tls.ClientAuthType) (string, *atomic.Value, func()) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	// presented holds the peer certificate subject seen by the server, or "" if
	// the client presented none.
	presented := &atomic.Value{}
	presented.Store("")
	record := func(ctx context.Context) {
		if peerInfo, ok := peer.FromContext(ctx); ok {
			if tlsInfo, ok := peerInfo.AuthInfo.(credentials.TLSInfo); ok {
				if len(tlsInfo.State.PeerCertificates) > 0 {
					presented.Store(tlsInfo.State.PeerCertificates[0].Subject.CommonName)
					return
				}
			}
		}
		presented.Store("")
	}
	serverCert := testServerTLSCertificate(t)
	server := grpc.NewServer(
		grpc.Creds(credentials.NewTLS(&tls.Config{
			Certificates: []tls.Certificate{serverCert},
			ClientAuth:   clientAuth,
			MinVersion:   tls.VersionTLS12,
		})),
		grpc.UnaryInterceptor(func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
			record(ctx)
			return handler(ctx, req)
		}),
		grpc.StreamInterceptor(func(srv any, stream grpc.ServerStream, _ *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
			record(stream.Context())
			return handler(srv, stream)
		}),
	)
	grpc_testing.RegisterTestServiceServer(server, &reflectedTestService{})
	reflection.Register(server)
	go func() {
		_ = server.Serve(listener)
	}()
	return listener.Addr().String(), presented, func() {
		server.Stop()
		_ = listener.Close()
	}
}

// writeClientCertificateFiles drops a client certificate into a collection
// directory and returns the collection that references it by the given domain.
func collectionWithClientCertificate(t *testing.T, domain string) Collection {
	t.Helper()
	certPEM, keyPEM, _, _ := testClientCertificate(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "client.pem"), certPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "client.key"), keyPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	return Collection{
		ID:   "col_grpc",
		Name: "gRPC",
		Path: dir,
		ClientCertificates: []ClientCertificateConfig{{
			Domain:       domain,
			Type:         "cert",
			CertFilePath: "client.pem",
			KeyFilePath:  "client.key",
		}},
	}
}

// §4.4: WHICH certificate is selected must not depend on an agent-supplied
// value. The certificate's domain is a template; the agent-free context
// resolves it to the server's host, the runtime context resolves it to
// somewhere else. If the matching seam saw the runtime values, nothing would
// match and no certificate would be presented — so the certificate arriving at
// the server IS the proof that the seam saw only agent-free inputs.
func TestMCPGRPCCertSelectionAgentFree(t *testing.T) {
	address, presented, stop := tlsGRPCService(t, tls.RequireAnyClientCert)
	defer stop()

	host, _, err := net.SplitHostPort(address)
	if err != nil {
		t.Fatal(err)
	}
	collection := collectionWithClientCertificate(t, "{{certHost}}")
	target := "grpcs://" + address
	item := mcpGRPCItem(target)

	// The agent-shaped variables point the template somewhere the certificate
	// must not follow; the scope's agent-free variables are the truth.
	runtimeVars := map[string]string{"certHost": "attacker.example"}
	policy := mcpGRPCPolicy(t, target, map[string]string{"certHost": host})

	app := newAppForTest(t)
	response := app.executeGRPC(mcpContextWithPolicy(context.Background(), policy), collection, item, runtimeVars)
	if response.Error != "" || response.Status != http.StatusOK {
		t.Fatalf("the mTLS gRPC call failed: %#v", response)
	}
	if got := presented.Load().(string); got != "liteapi-client" {
		t.Fatalf("the server saw peer certificate %q; the agent-free selection should have presented liteapi-client", got)
	}

	// The control, stated as a fact rather than an assumption: with the runtime
	// values the same configuration matches NOTHING. That is what the MCP
	// branch refuses to consult.
	_, matched, err := grpcClientCertificate(nil, collection, target, runtimeVars, Origin{})
	if err != nil {
		t.Fatal(err)
	}
	if matched {
		t.Fatal("the runtime variables also matched the certificate, so this test proves nothing; choose a different agent-shaped value")
	}
}

// §4.4 / §5 row 17b, THE LEAK THIS CLOSES. Certificate domain matching ignores
// ports (transport.ClientCertificateDomainMatches, deliberately), so a
// certificate configured for 127.0.0.1 matches every service on the machine. A
// retargeted gRPC send would then present the user's identity to a different
// service on a different port. The origin comparison is what stops it, and the
// assertion is made where it cannot be faked: on the server's own
// tls.ConnectionState.PeerCertificates.
func TestMCPGRPCCertNotPresentedOffCertOrigin(t *testing.T) {
	// RequestClientCert rather than RequireAnyClientCert: the handshake must
	// succeed with no certificate so the absence is observable.
	address, presented, stop := tlsGRPCService(t, tls.RequestClientCert)
	defer stop()

	host, _, err := net.SplitHostPort(address)
	if err != nil {
		t.Fatal(err)
	}
	// The certificate matches the HOST — including the running server — but the
	// definition's destination is a different port on that host.
	collection := collectionWithClientCertificate(t, host)
	definitionTarget := "grpcs://" + net.JoinHostPort(host, "1")
	runtimeTarget := "grpcs://" + address

	policy := newMCPEgressPolicy()
	scope := mcpGRPCScope(t, "req_grpc", definitionTarget, nil)
	// The runtime origin is authorized — the user approved it — so the send
	// proceeds. What must NOT follow it is the certificate.
	_, runtimeOrigin, err := mcpValidateGRPCTarget(runtimeTarget)
	if err != nil {
		t.Fatal(err)
	}
	scope.add(egressKindMain, runtimeOrigin)
	policy.SetScope(scope)

	app := newAppForTest(t)
	item := mcpGRPCItem(runtimeTarget)
	response := app.executeGRPC(mcpContextWithPolicy(context.Background(), policy), collection, item, nil)
	if response.Error != "" || response.Status != http.StatusOK {
		t.Fatalf("the authorized off-certOrigin call failed: %#v", response)
	}
	if got := presented.Load().(string); got != "" {
		t.Fatalf("the server received client certificate %q; the pinned origin differs from certOrigin, so no certificate may be presented", got)
	}

	// And the UI path is unchanged: the same configuration, sent by the user,
	// still presents the certificate the domain matches.
	uiResponse := app.executeGRPC(mcpContextWithUIProvenance(context.Background()), collection, item, nil)
	if uiResponse.Error != "" {
		t.Fatalf("the UI gRPC send failed: %#v", uiResponse)
	}
	if got := presented.Load().(string); got != "liteapi-client" {
		t.Fatalf("the UI send presented %q; §1.2(4) says a user-initiated send is never subjected to the boundary", got)
	}
}

// --- §1.1 path 3 / §5 row 8b: grpc-go's own environment proxy ---------------

const (
	grpcProxyChildTargetEnv = "LITEAPI_TEST_GRPC_PROXY_TARGET"
	grpcProxyChildBaseEnv   = "LITEAPI_TEST_GRPC_PROXY_BASE"
	grpcProxyChildExpectEnv = "LITEAPI_TEST_GRPC_PROXY_EXPECT"
)

// connectProxy is a CONNECT proxy that records the authority each client asked
// for and then splices the connection to one fixed backend. Recording the
// AUTHORITY is the point: it is the pinned target, proving the checkpoint and
// the wire agree even though the physical connection goes somewhere else.
type connectProxy struct {
	listener    net.Listener
	backend     string
	mu          sync.Mutex
	authorities []string
}

func newConnectProxy(t *testing.T, backend string) *connectProxy {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	proxy := &connectProxy{listener: listener, backend: backend}
	go proxy.serve()
	t.Cleanup(func() { _ = listener.Close() })
	return proxy
}

func (p *connectProxy) addr() string { return p.listener.Addr().String() }

func (p *connectProxy) recorded() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.authorities...)
}

func (p *connectProxy) serve() {
	for {
		client, err := p.listener.Accept()
		if err != nil {
			return
		}
		go p.handle(client)
	}
}

func (p *connectProxy) handle(client net.Conn) {
	defer func() { _ = client.Close() }()
	buffer := make([]byte, 0, 1024)
	chunk := make([]byte, 256)
	for !strings.Contains(string(buffer), "\r\n\r\n") {
		n, err := client.Read(chunk)
		if n > 0 {
			buffer = append(buffer, chunk[:n]...)
		}
		if err != nil {
			return
		}
		if len(buffer) > 8192 {
			return
		}
	}
	request := string(buffer)
	fields := strings.Fields(request)
	if len(fields) < 2 || !strings.EqualFold(fields[0], "CONNECT") {
		return
	}
	p.mu.Lock()
	p.authorities = append(p.authorities, fields[1])
	p.mu.Unlock()

	backend, err := net.Dial("tcp", p.backend)
	if err != nil {
		_, _ = client.Write([]byte("HTTP/1.1 502 Bad Gateway\r\n\r\n"))
		return
	}
	defer func() { _ = backend.Close() }()
	if _, err := client.Write([]byte("HTTP/1.1 200 Connection established\r\n\r\n")); err != nil {
		return
	}
	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(backend, client)
		close(done)
	}()
	_, _ = io.Copy(client, backend)
	<-done
}

// runGRPCProxyChild runs one gRPC send in a child process, because
// http.ProxyFromEnvironment — which grpc-go calls to decide its own CONNECT
// route — snapshots the environment on FIRST USE, process-wide. Setting
// HTTPS_PROXY inside this test binary would therefore do nothing (some earlier
// test has already primed the snapshot) and would leak into every later test if
// it did. A child process is the only way to make the variable both effective
// and contained.
func runGRPCProxyChild(t *testing.T, env map[string]string) (string, error) {
	t.Helper()
	command := exec.Command(os.Args[0], "-test.run=^TestMCPGRPCEnvProxyChildProcess$", "-test.v")
	command.Env = os.Environ()
	for name, value := range env {
		command.Env = append(command.Env, name+"="+value)
	}
	output, err := command.CombinedOutput()
	return string(output), err
}

// TestMCPGRPCEnvProxyChildProcess is the child half of the two environment-proxy
// tests. It is skipped unless the parent asked for it.
func TestMCPGRPCEnvProxyChildProcess(t *testing.T) {
	target := os.Getenv(grpcProxyChildTargetEnv)
	if target == "" {
		t.Skip("child-process helper for the environment-proxy tests")
	}
	base := os.Getenv(grpcProxyChildBaseEnv)
	wantDenied := os.Getenv(grpcProxyChildExpectEnv) == "denied"

	app := newAppForTest(t)
	collection := Collection{ID: "col_grpc", Name: "gRPC", Path: t.TempDir()}
	item := mcpGRPCItem(target)
	policy := mcpGRPCPolicy(t, base, nil)
	response := app.executeGRPC(mcpContextWithPolicy(context.Background(), policy), collection, item, nil)

	if wantDenied {
		if response.Error == "" || !strings.Contains(response.Error, "denied") {
			t.Fatalf("the unauthorized target was not denied: %#v", response)
		}
		return
	}
	if response.Error != "" || response.Status != http.StatusOK {
		t.Fatalf("the proxied gRPC call failed: %#v", response)
	}
	if !strings.Contains(response.Body, "reflected") {
		t.Fatalf("the proxied gRPC call returned an unexpected body: %q", response.Body)
	}
}

// §1.1 path 3 / §1.4(6): grpc-go reads HTTPS_PROXY and NO_PROXY itself and
// establishes its own CONNECT route, entirely outside LiteAPI's HTTP stack. The
// boundary authorizes the TARGET; the proxy connection that carries it is
// trusted configuration the agent cannot alter through MCP. Both halves are
// asserted: the CONNECT authority equals the pinned target, and NO_PROXY is
// honored.
func TestMCPGRPCEnvProxyTrustedSet(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns child processes")
	}
	address, stop := startReflectedTestService(t, map[string]string{})
	defer stop()
	proxy := newConnectProxy(t, address)

	// A non-loopback name, because http.ProxyFromEnvironment never proxies a
	// loopback address. With a proxy configured grpc-go does not resolve the
	// target itself — it hands the authority to the proxy — so the name never
	// needs to exist.
	target := "target.liteapi.invalid:50051"
	output, err := runGRPCProxyChild(t, map[string]string{
		"HTTPS_PROXY":           "http://" + proxy.addr(),
		"NO_PROXY":              "",
		grpcProxyChildTargetEnv: target,
		grpcProxyChildBaseEnv:   target,
	})
	if err != nil {
		t.Fatalf("the proxied gRPC child failed: %v\n%s", err, output)
	}
	recorded := proxy.recorded()
	if len(recorded) == 0 {
		t.Fatalf("no CONNECT reached the proxy; the call did not use the environment proxy\n%s", output)
	}
	for _, authority := range recorded {
		if authority != target {
			t.Fatalf("CONNECT asked for %q, want the pinned target %q", authority, target)
		}
	}

	// NO_PROXY is honored, and that is a property of the trusted-proxy set
	// rather than of LiteAPI: with the target excluded, nothing at all reaches
	// the proxy. The call then fails on DNS, which is exactly what going direct
	// to a name that does not exist looks like.
	before := len(recorded)
	output, err = runGRPCProxyChild(t, map[string]string{
		"HTTPS_PROXY":           "http://" + proxy.addr(),
		"NO_PROXY":              "target.liteapi.invalid",
		grpcProxyChildTargetEnv: target,
		grpcProxyChildBaseEnv:   target,
	})
	if err == nil {
		t.Fatalf("the direct call to a name that does not resolve unexpectedly succeeded\n%s", output)
	}
	if after := len(proxy.recorded()); after != before {
		t.Fatalf("%d CONNECT(s) were issued despite NO_PROXY", after-before)
	}
}

// The proxy does not become a bypass. An unauthorized target is denied before
// the dial, so grpc-go never gets the chance to open its CONNECT route — the
// destination boundary sits ABOVE the proxy decision, not beside it.
func TestMCPGRPCEnvProxyUnauthorizedTargetStillBlocked(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns child processes")
	}
	address, stop := startReflectedTestService(t, map[string]string{})
	defer stop()
	proxy := newConnectProxy(t, address)

	output, err := runGRPCProxyChild(t, map[string]string{
		"HTTPS_PROXY":           "http://" + proxy.addr(),
		"NO_PROXY":              "",
		grpcProxyChildTargetEnv: "attacker.liteapi.invalid:50051",
		grpcProxyChildBaseEnv:   "target.liteapi.invalid:50051",
		grpcProxyChildExpectEnv: "denied",
	})
	if err != nil {
		t.Fatalf("the child did not report a denial: %v\n%s", err, output)
	}
	if recorded := proxy.recorded(); len(recorded) != 0 {
		t.Fatalf("%d CONNECT(s) were issued for a denied target: %v", len(recorded), recorded)
	}
}

// --- the OAuth2 fetcher seam ------------------------------------------------

// §4.5: the fetcher grpcexec is handed must be BOUND TO THE SEND'S CONTEXT,
// because that context is what carries the provenance and the policy. A
// context-free fetcher would make the token exchange an unlabeled egress —
// invisible to the boundary and, once strict flips, refused.
//
// The property is asserted the only way it is observable today: a cancelled
// send must not start a token fetch. A fetcher that ignored the context would
// happily run one.
func TestGRPCOAuth2FetcherIsBoundToTheSendContext(t *testing.T) {
	app := newAppForTest(t)
	cancelled, cancel := context.WithCancel(mcpContextWithPolicy(context.Background(), newMCPEgressPolicy()))
	cancel()

	fetcher := app.grpcOAuth2Fetcher(cancelled)
	if fetcher == nil {
		t.Fatal("grpcOAuth2Fetcher returned no fetcher")
	}
	auth := OAuth2Auth{GrantType: "client_credentials", AccessTokenURL: "http://127.0.0.1:1/token", ClientID: "agent"}
	if _, err := fetcher(auth, nil); err == nil {
		t.Fatal("the fetcher ran a token exchange on a cancelled send")
	}
}

// §1.1 / kindClass: the token exchange a gRPC send performs while assembling
// metadata is a TOKEN egress, not the request's own destination. Riding the
// main kind would let the backstop authorize an OAuth2 endpoint against the
// gRPC target's Base — precisely the confused deputy §4.1 exists to prevent.
func TestGRPCTokenContextNarrowsTheEgressKind(t *testing.T) {
	sendCtx := mcpContextWithPolicy(context.Background(), newMCPEgressPolicy())
	if got := mcpBackstopEgressKind(sendCtx); got != egressKindMain {
		t.Fatalf("the send context's backstop kind is %q, want %q", got, egressKindMain)
	}
	if got := mcpBackstopEgressKind(mcpGRPCTokenContext(sendCtx)); got != egressKindToken {
		t.Fatalf("the token context's backstop kind is %q, want %q", got, egressKindToken)
	}
	if kindClass(egressKindToken) == kindClass(egressKindMain) {
		t.Fatal("token and main share an approval class, so narrowing the kind would authorize nothing extra")
	}
}
