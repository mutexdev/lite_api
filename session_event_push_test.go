package main

// US-021 / US-022 — tests for incremental live-session event push.
//
// The property under test is a COST property, and cost properties are the
// easiest thing in the world to "verify" vacuously. Two rules followed here:
//
//   * Assert on work done, not on wall-clock time. A timing assertion on a
//     loopback WebSocket is dominated by scheduler noise and would either flake
//     or pass regardless. The observable that actually distinguishes O(n) from
//     O(n^2) is how many bytes each message causes to be marshalled, which the
//     response body size reports exactly.
//   * Drive the real bindings against a real httptest server. Constructing a
//     session struct by hand and calling responseLocked would test the tail
//     helper and nothing else — in particular it would not catch an append site
//     that bypasses appendEventLocked and therefore never pushes.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/gorilla/websocket"
)

// liveWebSocketFixture stands up an echo server and a connected request, and
// returns the ids needed to drive it.
func liveWebSocketFixture(t *testing.T) (app *App, collectionID, itemID string, closeServer func()) {
	t.Helper()
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		for {
			kind, payload, err := conn.ReadMessage()
			if err != nil {
				return
			}
			if err := conn.WriteMessage(kind, append([]byte("ack:"), payload...)); err != nil {
				return
			}
		}
	}))

	app = newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	collection := state.Workspaces[0].Collections[0]
	state, err = app.CreateRequest(collection.ID, "websocket", "Push probe")
	if err != nil {
		t.Fatalf("CreateRequest: %v", err)
	}
	collection = state.Workspaces[0].Collections[0]
	item := collection.Items[len(collection.Items)-1]
	targetURL := "ws" + strings.TrimPrefix(server.URL, "http")
	messages := []WSMessage{{Name: "probe", Type: "text", Content: "payload", Selected: true}}
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{URL: &targetURL, WSMessages: &messages}); err != nil {
		t.Fatalf("UpdateRequest: %v", err)
	}
	if _, err := app.ConnectWebSocket(collection.ID, item.ID, ""); err != nil {
		t.Fatalf("ConnectWebSocket: %v", err)
	}
	return app, collection.ID, item.ID, server.Close
}

func responseForItem(t *testing.T, state AppState, collectionID, itemID string) *Response {
	t.Helper()
	item, ok := findItemInState(state, collectionID, itemID)
	if !ok || item.Response == nil {
		t.Fatalf("no response recorded for %s/%s", collectionID, itemID)
	}
	return item.Response
}

// TestWebSocketResponseBodyStaysBoundedOverALongSession is the core US-021
// assertion. Before the change the body grew with every message, so message N
// re-marshalled all N events and re-serialised them into AppState — quadratic.
// After it, the body is capped and per-message cost is flat.
func TestWebSocketResponseBodyStaysBoundedOverALongSession(t *testing.T) {
	app, collectionID, itemID, closeServer := liveWebSocketFixture(t)
	defer closeServer()

	// Each send records two events (sent + received), so this crosses the cap
	// comfortably: 300 sends is 600 events against a 200-event window.
	const sends = 300

	var bodyAtCap int
	var lastState AppState
	for i := range sends {
		state, err := app.SendWebSocketMessage(collectionID, itemID, "", 0)
		if err != nil {
			t.Fatalf("send #%d: %v", i, err)
		}
		lastState = state
		// Sample the body size once the window is full, then again at the end.
		// A body that is still growing between those two points is the
		// quadratic behaviour this story removes.
		if i == sends/2 {
			bodyAtCap = len(responseForItem(t, state, collectionID, itemID).Body)
		}
	}

	final := responseForItem(t, lastState, collectionID, itemID)
	finalSize := len(final.Body)
	if bodyAtCap == 0 {
		t.Fatalf("failed to sample a mid-session body size")
	}
	// Not an exact-equality check, and the reason is worth stating: the payloads
	// are identical but the timestamps are not, and encoding/json renders
	// time.Time with variable fractional-second width, so the same 200 events
	// serialise to a body that wobbles by a few bytes. Measured drift across
	// these 150 messages is ~10 bytes.
	//
	// The tolerance is nonetheless tight enough to be a real test. Doubling the
	// message count doubles the event count, so the pre-US-021 body would have
	// grown by ~100%, against the 1% allowed here — two orders of magnitude of
	// headroom between "timestamp jitter" and "still accumulating".
	if growth := float64(finalSize-bodyAtCap) / float64(bodyAtCap); growth > 0.01 {
		t.Errorf("response body still growing after the cap: %d bytes at message %d, %d bytes at message %d (+%.1f%%)",
			bodyAtCap, sends/2, finalSize, sends, growth*100)
	}

	// The headers must still tell the truth about the full log.
	total, err := strconv.Atoi(final.Headers["x-websocket-events"])
	if err != nil {
		t.Fatalf("x-websocket-events not an integer: %q", final.Headers["x-websocket-events"])
	}
	// 1 connect event is not recorded for WebSocket, so the total is 2 per send.
	if total != sends*2 {
		t.Errorf("x-websocket-events = %d, want %d — the header must report the FULL log, not the window", total, sends*2)
	}
	omitted, err := strconv.Atoi(final.Headers["x-websocket-events-omitted"])
	if err != nil {
		t.Fatalf("x-websocket-events-omitted missing or not an integer: %q", final.Headers["x-websocket-events-omitted"])
	}
	if omitted != total-sessionEventTailLimit {
		t.Errorf("x-websocket-events-omitted = %d, want %d", omitted, total-sessionEventTailLimit)
	}

	var events []websocketSessionEvent
	if err := json.Unmarshal([]byte(final.Body), &events); err != nil {
		t.Fatalf("response body is not a JSON event array: %v", err)
	}
	if len(events) != sessionEventTailLimit {
		t.Errorf("body carries %d events, want exactly the %d-event window", len(events), sessionEventTailLimit)
	}
	// The window must be the NEWEST events. A tail taken from the wrong end
	// would still be the right length and still satisfy every size assertion
	// above, so this is the check that pins direction.
	if got := events[len(events)-1].Direction; got != "received" {
		t.Errorf("last windowed event has direction %q, want %q — the window is not the tail", got, "received")
	}
}

// TestWebSocketEmitsEveryAppendedEvent pins the push half. The danger is an
// append site that writes session.events directly and therefore never emits:
// the frontend's log would be permanently missing that entry, with no gap in
// the index sequence to reveal it, because the index is derived from the slice
// after the append.
//
// Emission is observed by substituting the session's emit callback. In
// production it is wired to wailsruntime.EventsEmit, which needs a Wails
// context that no test has.
func TestWebSocketEmitsEveryAppendedEvent(t *testing.T) {
	app, collectionID, itemID, closeServer := liveWebSocketFixture(t)
	defer closeServer()

	key := websocketSessionKey(collectionID, itemID)
	app.websocketMu.Lock()
	session := app.websocketSessions[key]
	app.websocketMu.Unlock()
	if session == nil {
		t.Fatalf("no live session after connect")
	}

	var mu sync.Mutex
	var pushed []websocketEventPush
	session.mu.Lock()
	baseline := len(session.events)
	session.emit = func(index, total int, event websocketSessionEvent) {
		mu.Lock()
		defer mu.Unlock()
		pushed = append(pushed, websocketEventPush{Index: index, Total: total, Event: event})
	}
	session.mu.Unlock()

	const sends = 5
	for i := range sends {
		if _, err := app.SendWebSocketMessage(collectionID, itemID, "", 0); err != nil {
			t.Fatalf("send #%d: %v", i, err)
		}
	}
	if _, err := app.DisconnectWebSocket(collectionID, itemID); err != nil {
		t.Fatalf("disconnect: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(pushed) < sends*2 {
		t.Fatalf("got %d pushes for %d sends; every appended event must be pushed", len(pushed), sends)
	}
	// Indices must be contiguous and anchored to the real slice position, which
	// is what lets the frontend detect that it missed one.
	for i, push := range pushed {
		wantIndex := baseline + i
		if push.Index != wantIndex {
			t.Fatalf("push %d has index %d, want %d — indices must be contiguous", i, push.Index, wantIndex)
		}
		if push.Total != push.Index+1 {
			t.Fatalf("push %d has total %d for index %d; total must be the log length after the append", i, push.Total, push.Index)
		}
	}
	if pushed[0].Event.Direction != "sent" || pushed[1].Event.Direction != "received" {
		t.Errorf("first two pushes are %q/%q, want sent/received — order is not preserved",
			pushed[0].Event.Direction, pushed[1].Event.Direction)
	}
}

// TestSessionEventTailHelpers covers the boundary arithmetic directly. Off-by-one
// here would either drop a live event or return a window one short, and neither
// is visible in the end-to-end tests above, which sit far from the boundary.
func TestSessionEventTailHelpers(t *testing.T) {
	build := func(n int) []websocketSessionEvent {
		events := make([]websocketSessionEvent, n)
		for i := range events {
			events[i] = websocketSessionEvent{Data: fmt.Sprintf("%d", i)}
		}
		return events
	}

	for _, size := range []int{0, 1, sessionEventTailLimit - 1, sessionEventTailLimit} {
		tail, omitted := websocketEventTail(build(size))
		if len(tail) != size || omitted != 0 {
			t.Errorf("at or below the limit (%d): got %d events / %d omitted, want %d / 0", size, len(tail), omitted, size)
		}
	}

	for _, size := range []int{sessionEventTailLimit + 1, sessionEventTailLimit * 3} {
		tail, omitted := websocketEventTail(build(size))
		if len(tail) != sessionEventTailLimit {
			t.Errorf("above the limit (%d): got %d events, want %d", size, len(tail), sessionEventTailLimit)
		}
		if omitted != size-sessionEventTailLimit {
			t.Errorf("above the limit (%d): got %d omitted, want %d", size, omitted, size-sessionEventTailLimit)
		}
		if want := fmt.Sprintf("%d", size-1); tail[len(tail)-1].Data != want {
			t.Errorf("above the limit (%d): window ends at %q, want %q", size, tail[len(tail)-1].Data, want)
		}
		if want := fmt.Sprintf("%d", size-sessionEventTailLimit); tail[0].Data != want {
			t.Errorf("above the limit (%d): window starts at %q, want %q", size, tail[0].Data, want)
		}
	}
}
