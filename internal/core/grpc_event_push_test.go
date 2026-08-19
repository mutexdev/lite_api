// US-022 — every appended gRPC stream event is pushed to the frontend.
//
// This was the one criterion left owed from the US-021/022 pair: the WebSocket
// half was verified long ago, and the gRPC half was recorded as "needs a local
// gRPC streaming server". It does not — the repo already runs one
// (startReflectedTestService, with a real FullDuplexCall bidi handler), so the
// blocker was a missing test rather than missing infrastructure.
//
// What matters here is the same property the WebSocket test pins: an event that
// reaches session.events but never reaches the frontend leaves the stream log
// permanently missing an entry, with nothing on screen to say so. Contiguous
// indices are what let the frontend notice it missed one.
package core

import (
	"strings"
	"sync"
	"testing"
)

func liveGRPCStreamFixture(t *testing.T) (app *App, collectionID, itemID string, stop func()) {
	t.Helper()
	address, stopServer := startReflectedTestService(t, map[string]string{})

	app = newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	state, err = app.CreateRequest(collection.ID, "grpc", "Bidi Push")
	if err != nil {
		t.Fatal(err)
	}
	collection = state.Workspaces[0].Collections[0]
	item := collection.Items[len(collection.Items)-1]

	targetURL := "grpc://" + address
	method := "grpc.testing.TestService/FullDuplexCall"
	messages := []GrpcMessage{
		{Name: "one", Content: `{"payload":{"body":"b25l"}}`},
		{Name: "two", Content: `{"payload":{"body":"dHdv"}}`},
	}
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{
		URL: &targetURL, Method: &method, GrpcMessages: &messages,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := app.ConnectGRPCStream(collection.ID, item.ID, ""); err != nil {
		stopServer()
		t.Fatalf("connect gRPC stream: %v", err)
	}
	return app, collection.ID, item.ID, func() {
		_, _ = app.CancelGRPCStream(collection.ID, item.ID)
		stopServer()
	}
}

func TestGRPCStreamEmitsEveryAppendedEvent(t *testing.T) {
	app, collectionID, itemID, stop := liveGRPCStreamFixture(t)
	defer stop()

	key := grpcStreamSessionKey(collectionID, itemID)
	app.grpcStreamMu.Lock()
	session := app.grpcStreamSessions[key]
	app.grpcStreamMu.Unlock()
	if session == nil {
		t.Fatal("no live gRPC session after connect")
	}

	var mu sync.Mutex
	var pushed []grpcEventPush
	session.mu.Lock()
	baseline := len(session.events)
	session.emit = func(index, total int, event grpcStreamSessionEvent) {
		mu.Lock()
		defer mu.Unlock()
		pushed = append(pushed, grpcEventPush{Index: index, Total: total, Event: event})
	}
	session.mu.Unlock()

	const sends = 3
	for i := 0; i < sends; i++ {
		if _, err := app.SendGRPCStreamMessage(collectionID, itemID, "", 0); err != nil {
			t.Fatalf("send #%d: %v", i, err)
		}
	}
	if _, err := app.EndGRPCStream(collectionID, itemID); err != nil {
		t.Fatalf("end stream: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(pushed) < sends {
		t.Fatalf("got %d pushes for %d sends; every appended event must be pushed", len(pushed), sends)
	}
	// Anchored to the real slice position, so a gap is detectable downstream.
	for i, push := range pushed {
		wantIndex := baseline + i
		if push.Index != wantIndex {
			t.Fatalf("push %d has index %d, want %d — indices must be contiguous", i, push.Index, wantIndex)
		}
		if push.Total != push.Index+1 {
			t.Fatalf("push %d has total %d for index %d; total must be the log length after the append", i, push.Total, push.Index)
		}
	}
	if pushed[0].Event.Direction != "sent" {
		t.Errorf("first push is %q, want sent — order is not preserved", pushed[0].Event.Direction)
	}
	if !strings.Contains(pushed[0].Event.Data, "b25l") {
		t.Errorf("first push carries %q, which is not the message that was sent", pushed[0].Event.Data)
	}
}

// The push count and the recorded log must agree. If emit ran without the event
// being appended, or vice versa, the frontend and the timeline would disagree
// about what happened on the wire.
func TestGRPCStreamPushCountMatchesRecordedEvents(t *testing.T) {
	app, collectionID, itemID, stop := liveGRPCStreamFixture(t)
	defer stop()

	key := grpcStreamSessionKey(collectionID, itemID)
	app.grpcStreamMu.Lock()
	session := app.grpcStreamSessions[key]
	app.grpcStreamMu.Unlock()

	var mu sync.Mutex
	pushes := 0
	session.mu.Lock()
	baseline := len(session.events)
	session.emit = func(int, int, grpcStreamSessionEvent) {
		mu.Lock()
		pushes++
		mu.Unlock()
	}
	session.mu.Unlock()

	for i := 0; i < 2; i++ {
		if _, err := app.SendGRPCStreamMessage(collectionID, itemID, "", 0); err != nil {
			t.Fatalf("send #%d: %v", i, err)
		}
	}
	if _, err := app.EndGRPCStream(collectionID, itemID); err != nil {
		t.Fatalf("end stream: %v", err)
	}

	session.mu.Lock()
	appended := len(session.events) - baseline
	session.mu.Unlock()
	mu.Lock()
	defer mu.Unlock()
	if pushes != appended {
		t.Fatalf("%d events appended but %d pushed; every append must push exactly once", appended, pushes)
	}
}
