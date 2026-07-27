package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gorilla/websocket"
)

// The WebSocket session map has the same three helpers as the gRPC one, with
// the same reconnect guard, and removeWebSocketSessionIfSame was likewise at
// 0%. The race is identical: replaceWebSocketSession installs a new session
// under the same key and closes the old one, and the old one's teardown must
// not delete the entry the reconnect just installed.
//
// Unlike the gRPC version, websocketSession.close dereferences session.conn
// with NO nil check. That is safe by control flow rather than by luck — the one
// construction site runs only after a successful dial — but it means these
// tests must use REAL connections rather than zero-value sessions. Building
// them against a live server also exercises the rest of close: the done channel
// and the "close" event it appends.

// liveWebSocketSession dials a throwaway echo server and returns a session
// shaped like the one ConnectWebSocket builds.
func liveWebSocketSession(t *testing.T) *websocketSession {
	t.Helper()
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		// Hold the connection until the client closes it.
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				_ = conn.Close()
				return
			}
		}
	}))
	t.Cleanup(server.Close)

	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	session := &websocketSession{
		conn:   conn,
		events: []websocketSessionEvent{},
		done:   make(chan struct{}),
	}
	t.Cleanup(func() { session.close("test cleanup") })
	return session
}

func websocketSessionsFor(t *testing.T) *App {
	t.Helper()
	app := newAppForTest(t)
	if app.websocketSessions == nil {
		t.Fatal("the session map is nil, so every helper here would panic")
	}
	return app
}

func TestRemoveWebSocketSessionRemovesTheSessionItWasGiven(t *testing.T) {
	app := websocketSessionsFor(t)
	session := liveWebSocketSession(t)
	app.replaceWebSocketSession("key", session)

	app.removeWebSocketSessionIfSame("key", session)

	app.websocketMu.Lock()
	_, present := app.websocketSessions["key"]
	app.websocketMu.Unlock()
	if present {
		t.Error("the session was not removed")
	}
}

// THE PROPERTY THE "IfSame" EXISTS FOR, stated for WebSocket as it is for gRPC.
func TestRemoveWebSocketSessionLeavesAReplacementAlone(t *testing.T) {
	app := websocketSessionsFor(t)
	old := liveWebSocketSession(t)
	fresh := liveWebSocketSession(t)

	app.replaceWebSocketSession("key", old)
	app.replaceWebSocketSession("key", fresh) // the reconnect
	app.removeWebSocketSessionIfSame("key", old)

	app.websocketMu.Lock()
	got := app.websocketSessions["key"]
	app.websocketMu.Unlock()
	if got != fresh {
		t.Fatal("the old session's teardown deleted the reconnected session; the next send would report a closed socket")
	}
}

func TestRemoveWebSocketSessionIgnoresAKeyItDoesNotHold(t *testing.T) {
	app := websocketSessionsFor(t)
	live := liveWebSocketSession(t)
	other := liveWebSocketSession(t)
	app.replaceWebSocketSession("key", live)

	app.removeWebSocketSessionIfSame("other key", live)
	app.removeWebSocketSessionIfSame("key", other)

	app.websocketMu.Lock()
	got := app.websocketSessions["key"]
	app.websocketMu.Unlock()
	if got != live {
		t.Error("a removal for a different key or session disturbed the live one")
	}
}

// Replacing closes the session it displaces, and the close is observable three
// ways: the flag, the reason, and the done channel that the keep-alive
// goroutine selects on. A close that set the flag but left done open would
// leak that goroutine for the life of the app.
func TestReplaceWebSocketSessionClosesTheOneItDisplaces(t *testing.T) {
	app := websocketSessionsFor(t)
	old := liveWebSocketSession(t)
	app.replaceWebSocketSession("key", old)

	app.replaceWebSocketSession("key", liveWebSocketSession(t))

	old.mu.Lock()
	closed, reason := old.closed, old.closeReason
	old.mu.Unlock()
	if !closed {
		t.Error("the displaced session was left open")
	}
	if reason != "reconnected" {
		t.Errorf("close reason = %q, want %q", reason, "reconnected")
	}
	select {
	case <-old.done:
	default:
		t.Error("the done channel was left open, so the keep-alive goroutine would never stop")
	}
}

// close is idempotent: the second call must not re-close the done channel,
// which would panic and take the process down.
func TestWebSocketSessionCloseIsIdempotent(t *testing.T) {
	session := liveWebSocketSession(t)
	session.close("first")
	session.close("second")

	session.mu.Lock()
	defer session.mu.Unlock()
	if session.closeReason != "first" {
		t.Errorf("close reason = %q, want the FIRST reason to stand", session.closeReason)
	}
}

func TestReplaceWebSocketSessionClosesNothingOnTheFirstConnect(t *testing.T) {
	app := websocketSessionsFor(t)
	first := liveWebSocketSession(t)
	app.replaceWebSocketSession("key", first)

	first.mu.Lock()
	defer first.mu.Unlock()
	if first.closed {
		t.Error("the first session was closed by its own installation")
	}
}

func TestPopWebSocketSessionRemovesWhateverIsThere(t *testing.T) {
	app := websocketSessionsFor(t)
	session := liveWebSocketSession(t)
	app.replaceWebSocketSession("key", session)

	if got := app.popWebSocketSession("key"); got != session {
		t.Error("pop returned a different session")
	}
	if got := app.popWebSocketSession("key"); got != nil {
		t.Error("a second pop returned something")
	}
	if got := app.popWebSocketSession("never used"); got != nil {
		t.Error("popping an unused key returned something")
	}
}

// pop does NOT close what it removes, matching the gRPC side: callers close
// with their own reason, and closing here would make that reason unreachable.
func TestPopWebSocketSessionDoesNotCloseWhatItRemoves(t *testing.T) {
	app := websocketSessionsFor(t)
	session := liveWebSocketSession(t)
	app.replaceWebSocketSession("key", session)

	popped := app.popWebSocketSession("key")
	popped.mu.Lock()
	defer popped.mu.Unlock()
	if popped.closed {
		t.Error("pop closed the session, so a caller's own close reason would never be recorded")
	}
}

// Run under -race, this says the locking around the map is real. The sessions
// are shared rather than dialled per iteration so the test stays cheap; what is
// under test is the map, not the sockets.
func TestWebSocketSessionHelpersAreSafeConcurrently(t *testing.T) {
	app := websocketSessionsFor(t)
	live := liveWebSocketSession(t)
	other := liveWebSocketSession(t)
	const rounds = 100

	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		for i := 0; i < rounds; i++ {
			app.websocketMu.Lock()
			app.websocketSessions["key"] = live
			app.websocketMu.Unlock()
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < rounds; i++ {
			app.removeWebSocketSessionIfSame("key", other)
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < rounds; i++ {
			app.popWebSocketSession("key")
		}
	}()
	wg.Wait()
}
