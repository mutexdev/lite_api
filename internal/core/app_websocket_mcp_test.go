package core

// The WebSocket half of the MCP destination boundary — §4.3's pre-handshake
// checkpoint and §5 row 10, plus the context fix the checkpoint rides on.

import (
	"bufio"
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/mutexdev/lite_api/internal/types"
)

func mcpWebSocketItem(targetURL string) RequestItem {
	item := types.NewRequestItem("Echo socket", "websocket", 1)
	item.URL = targetURL
	item.WSMessages = []WSMessage{{Name: "message 1", Type: "text", Content: "hello", Selected: true}}
	item.Settings.TimeoutMs = 5000
	return item
}

func mcpWebSocketPolicy(t *testing.T, definitionURL string) *mcpEgressPolicy {
	t.Helper()
	policy := newMCPEgressPolicy()
	policy.SetScope(testScope(t, "req_socket", definitionURL))
	return policy
}

// A WebSocket handshake IS an HTTP request: it carries the request's headers,
// cookies and auth to whatever host the resolved URL names. So a retargeted
// socket is exactly as dangerous as a retargeted request, and it must be
// stopped BEFORE the handshake — once the connection is open the credentials
// have already been sent, and there is nothing left to deny.
//
// The listener is the evidence: zero bytes, not "an error afterwards".
func TestMCPWebSocketRetargetBlockedAtHandshake(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
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
	// The definition points at a socket on a different port. §1.4(9):
	// localhost is not one place.
	policy := mcpWebSocketPolicy(t, "ws://127.0.0.1:1/socket")
	item := mcpWebSocketItem("ws://" + listener.Addr().String() + "/socket")

	response := app.executeWebSocketContext(mcpContextWithPolicy(context.Background(), policy), "col_ws", item, nil)
	if response.Error == "" {
		t.Fatalf("the retargeted handshake was performed: %#v", response)
	}
	if !strings.Contains(response.Error, "denied") {
		t.Fatalf("the refusal did not read as a denial: %q", response.Error)
	}
	if got := accepted.Load(); got != 0 {
		t.Fatalf("%d connection(s) reached the retargeted socket; the checkpoint must precede the handshake", got)
	}
}

// A destination that does not resolve to an origin is one nothing checked, so
// it is denied rather than dialed — fail-closed, per §1.1.
func TestMCPWebSocketUnresolvableTargetIsDenied(t *testing.T) {
	app := newAppForTest(t)
	policy := mcpWebSocketPolicy(t, "wss://sockets.example.com/live")
	item := mcpWebSocketItem("{{socketHost}}/live")

	response := app.executeWebSocketContext(mcpContextWithPolicy(context.Background(), policy), "col_ws", item, nil)
	if response.Error == "" || !strings.Contains(response.Error, "denied") {
		t.Fatalf("an unresolved WebSocket destination was not denied: %#v", response)
	}
}

// The other half of the boundary: the request's OWN socket still connects, and
// the frames ride the connection the checkpoint authorized. If this failed the
// boundary would simply have broken WebSockets for agents.
func TestMCPWebSocketOwnOriginConnectsAndExchangesFrames(t *testing.T) {
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		_, payload, err := conn.ReadMessage()
		if err != nil {
			return
		}
		_ = conn.WriteMessage(websocket.TextMessage, append([]byte("echo: "), payload...))
	}))
	defer server.Close()

	targetURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/socket"
	app := newAppForTest(t)
	policy := mcpWebSocketPolicy(t, targetURL)
	item := mcpWebSocketItem(targetURL)

	response := app.executeWebSocketContext(mcpContextWithPolicy(context.Background(), policy), "col_ws", item, nil)
	if response.Error != "" || response.Status != http.StatusSwitchingProtocols {
		t.Fatalf("the request's own socket was refused: %#v", response)
	}
	if response.Body != "echo: hello" {
		t.Fatalf("frames did not ride the authorized connection: %q", response.Body)
	}
}

// THE BUG THE CHECKPOINT RIDES ON. gorilla's Dial runs the handshake on
// context.Background(): a cancelled send left the dial running to its own
// timeout and no caller could stop it. DialContext fixes that, and this pins
// it — the send returns promptly on a cancelled context instead of waiting out
// the handshake against a listener that never answers.
func TestWebSocketHandshakeHonoursContextCancellation(t *testing.T) {
	// A listener that accepts and then says nothing: the handshake would block
	// until the 5-second handshake timeout if the context were ignored.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			// Hold the connection open without ever replying.
			t.Cleanup(func() { _ = conn.Close() })
		}
	}()

	app := newAppForTest(t)
	item := mcpWebSocketItem("ws://" + listener.Addr().String() + "/socket")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan Response, 1)
	go func() {
		done <- app.executeWebSocketContext(ctx, "col_ws", item, nil)
	}()
	select {
	case response := <-done:
		if response.Error == "" {
			t.Fatal("a cancelled handshake reported success")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the handshake ignored the cancelled context")
	}
}

// §1.2(4): a user-initiated send is never subjected to any of this. The
// context-free delegate keeps today's behaviour exactly — no policy, no
// checkpoint, same handshake.
func TestUIWebSocketSendUnaffectedByThePolicy(t *testing.T) {
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		_, payload, err := conn.ReadMessage()
		if err != nil {
			return
		}
		_ = conn.WriteMessage(websocket.TextMessage, append([]byte("echo: "), payload...))
	}))
	defer server.Close()

	app := newAppForTest(t)
	item := mcpWebSocketItem("ws" + strings.TrimPrefix(server.URL, "http") + "/socket")

	response := app.executeWebSocket("col_ws", item, nil)
	if response.Error != "" || response.Status != http.StatusSwitchingProtocols {
		t.Fatalf("the UI WebSocket send failed: %#v", response)
	}
	if response.Body != "echo: hello" {
		t.Fatalf("unexpected UI WebSocket body: %q", response.Body)
	}
}

// CONFIRMED-SAFE / COVERAGE-ADDED, attack area 5. websocketDialer's MCP branch
// (app_websocket.go) lifts Proxy and TLSClientConfig from mcpTransportPosture
// straight onto a bare websocket.Dialer with NetDialContext set directly to
// the cloned transport's dialer — there is no mcpCertConfinedTransport-style
// wrapper here at all, unlike the HTTP path in executeHTTP. That is only safe
// because a WebSocket handshake has no redirect concept for such a wrapper to
// defend against: this is the wire-level proof of that premise. The primary
// listener answers the handshake with a 302 to a SECOND ("attacker") raw TCP
// listener; if gorilla's Dialer ever followed a handshake redirect on its own
// — a library upgrade, or a future reimplementation of websocketDialer that
// added retry/redirect handling without adding a confinement wrapper to match
// — a client certificate loaded for the request's own origin would ride along
// to wherever Location pointed. Zero bytes reaching the attacker listener is
// what this test measures, not merely an error being returned.
func TestMCPWebSocketDoesNotFollowAHandshakeRedirectToAnotherHost(t *testing.T) {
	attackerListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = attackerListener.Close() }()
	var attackerConnections atomic.Int32
	go func() {
		for {
			conn, err := attackerListener.Accept()
			if err != nil {
				return
			}
			attackerConnections.Add(1)
			_ = conn.Close()
		}
	}()

	primaryListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = primaryListener.Close() }()
	go func() {
		conn, err := primaryListener.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		reader := bufio.NewReader(conn)
		// Read the raw handshake request off the wire until the blank line
		// that ends the headers, rather than parsing it — this listener only
		// needs to know when the client has finished asking.
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			if line == "\r\n" || line == "\n" {
				break
			}
		}
		location := "http://" + attackerListener.Addr().String() + "/exfil"
		response := "HTTP/1.1 302 Found\r\nLocation: " + location + "\r\nContent-Length: 0\r\n\r\n"
		_, _ = conn.Write([]byte(response))
	}()

	app := newAppForTest(t)
	targetURL := "ws://" + primaryListener.Addr().String() + "/socket"
	// The request's OWN destination — no retargeting, so the checkpoint has
	// nothing to say no to; whatever happens next is the dialer's own
	// behaviour, which is exactly what this test is about.
	policy := mcpWebSocketPolicy(t, targetURL)
	item := mcpWebSocketItem(targetURL)

	response := app.executeWebSocketContext(mcpContextWithPolicy(context.Background(), policy), "col_ws", item, nil)
	if response.Error == "" {
		t.Fatalf("a 302 handshake response was treated as a successful upgrade: %#v", response)
	}
	// A generous window for a follower that does not exist to have dialed
	// anyway, so this measures absence rather than a race against it.
	time.Sleep(200 * time.Millisecond)
	if got := attackerConnections.Load(); got != 0 {
		t.Fatalf("%d connection(s) reached the redirect target; the WebSocket dialer followed a handshake redirect off the checked origin", got)
	}
}
