package main

import (
	"sync"
	"testing"
)

// The three helpers that own the live gRPC stream map — replaceGRPCStreamSession,
// popGRPCStreamSession and removeGRPCStreamSessionIfSame — guard a reconnect
// race, and removeGRPCStreamSessionIfSame was at 0%.
//
// The race is concrete. replaceGRPCStreamSession swaps a new session in under
// the same key and closes the old one. The old one's teardown then removes
// itself from the map. If that removal did not check WHICH session it is
// deleting, it would delete the entry the reconnect just installed, and the
// user's very next message would report "not connected" immediately after a
// successful reconnect — intermittently, and only when the teardown lost the
// race.

func grpcSessionsFor(t *testing.T) *App {
	t.Helper()
	app := newAppForTest(t)
	if app.grpcStreamSessions == nil {
		t.Fatal("the session map is nil, so every helper here would panic")
	}
	return app
}

func TestRemoveGRPCStreamSessionRemovesTheSessionItWasGiven(t *testing.T) {
	app := grpcSessionsFor(t)
	session := &grpcStreamSession{}
	app.replaceGRPCStreamSession("key", session)

	app.removeGRPCStreamSessionIfSame("key", session)

	app.grpcStreamMu.Lock()
	_, present := app.grpcStreamSessions["key"]
	app.grpcStreamMu.Unlock()
	if present {
		t.Error("the session was not removed")
	}
}

// THE PROPERTY THE "IfSame" EXISTS FOR. After a reconnect the key holds a NEW
// session; the old one tearing down must leave that entry alone.
func TestRemoveGRPCStreamSessionLeavesAReplacementAlone(t *testing.T) {
	app := grpcSessionsFor(t)
	old := &grpcStreamSession{}
	fresh := &grpcStreamSession{}

	app.replaceGRPCStreamSession("key", old)
	app.replaceGRPCStreamSession("key", fresh) // the reconnect
	app.removeGRPCStreamSessionIfSame("key", old)

	app.grpcStreamMu.Lock()
	got := app.grpcStreamSessions["key"]
	app.grpcStreamMu.Unlock()
	if got != fresh {
		t.Fatal("the old session's teardown deleted the reconnected session; the next message would report \"not connected\"")
	}
}

func TestRemoveGRPCStreamSessionIgnoresAKeyItDoesNotHold(t *testing.T) {
	app := grpcSessionsFor(t)
	live := &grpcStreamSession{}
	app.replaceGRPCStreamSession("key", live)

	app.removeGRPCStreamSessionIfSame("other key", live)
	app.removeGRPCStreamSessionIfSame("key", &grpcStreamSession{})
	app.removeGRPCStreamSessionIfSame("absent", &grpcStreamSession{})

	app.grpcStreamMu.Lock()
	got := app.grpcStreamSessions["key"]
	app.grpcStreamMu.Unlock()
	if got != live {
		t.Error("a removal for a different key or session disturbed the live one")
	}
}

// Replacing closes the session it displaces. Leaving it open would leak the
// connection and its goroutines for as long as the app runs, and a reconnect is
// the ordinary way to reach this.
func TestReplaceGRPCStreamSessionClosesTheOneItDisplaces(t *testing.T) {
	app := grpcSessionsFor(t)
	old := &grpcStreamSession{}
	app.replaceGRPCStreamSession("key", old)

	old.mu.Lock()
	closedBefore := old.closed
	old.mu.Unlock()
	if closedBefore {
		t.Fatal("the session was closed before anything displaced it")
	}

	app.replaceGRPCStreamSession("key", &grpcStreamSession{})

	old.mu.Lock()
	defer old.mu.Unlock()
	if !old.closed {
		t.Error("the displaced session was left open")
	}
	if old.closeReason != "reconnected" {
		t.Errorf("close reason = %q, want %q", old.closeReason, "reconnected")
	}
}

// The FIRST session under a key has nothing to displace, so nothing is closed.
func TestReplaceGRPCStreamSessionClosesNothingOnTheFirstConnect(t *testing.T) {
	app := grpcSessionsFor(t)
	first := &grpcStreamSession{}
	app.replaceGRPCStreamSession("key", first)

	first.mu.Lock()
	defer first.mu.Unlock()
	if first.closed {
		t.Error("the first session was closed by its own installation")
	}
}

// pop is the unconditional counterpart: it removes whatever is there and hands
// it back, which is what an explicit disconnect wants.
func TestPopGRPCStreamSessionRemovesWhateverIsThere(t *testing.T) {
	app := grpcSessionsFor(t)
	session := &grpcStreamSession{}
	app.replaceGRPCStreamSession("key", session)

	if got := app.popGRPCStreamSession("key"); got != session {
		t.Error("pop returned a different session")
	}
	if got := app.popGRPCStreamSession("key"); got != nil {
		t.Error("a second pop returned something")
	}
	if got := app.popGRPCStreamSession("never used"); got != nil {
		t.Error("popping an unused key returned something")
	}
}

// pop does NOT close what it removes — its callers close explicitly, and
// closing here as well would make the reason they set unreachable.
func TestPopGRPCStreamSessionDoesNotCloseWhatItRemoves(t *testing.T) {
	app := grpcSessionsFor(t)
	session := &grpcStreamSession{}
	app.replaceGRPCStreamSession("key", session)

	popped := app.popGRPCStreamSession("key")
	popped.mu.Lock()
	defer popped.mu.Unlock()
	if popped.closed {
		t.Error("pop closed the session, so a caller's own close reason would never be recorded")
	}
}

// The three helpers are the only writers of this map and are called from
// request goroutines. Run under -race, this is what says the locking is real
// rather than incidental.
func TestGRPCStreamSessionHelpersAreSafeConcurrently(t *testing.T) {
	app := grpcSessionsFor(t)
	const keys = 4
	const rounds = 50

	var wg sync.WaitGroup
	for k := 0; k < keys; k++ {
		key := string(rune('a' + k))
		wg.Add(3)
		go func() {
			defer wg.Done()
			for i := 0; i < rounds; i++ {
				app.replaceGRPCStreamSession(key, &grpcStreamSession{})
			}
		}()
		go func() {
			defer wg.Done()
			for i := 0; i < rounds; i++ {
				app.removeGRPCStreamSessionIfSame(key, &grpcStreamSession{})
			}
		}()
		go func() {
			defer wg.Done()
			for i := 0; i < rounds; i++ {
				app.popGRPCStreamSession(key)
			}
		}()
	}
	wg.Wait()
}
