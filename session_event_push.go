package main

// US-021 / US-022 — incremental event push for live WebSocket and gRPC sessions.
//
// THE PROBLEM (improvement_v2.md §2.1, "WS/gRPC live sessions polled").
// Every live-session binding call rebuilt the response body by marshalling the
// session's ENTIRE event log, base64-encoding the result, storing it into
// item.Response inside AppState, and marking the workspace dirty. Message N
// therefore cost O(N), and a session of N messages cost O(N^2) — in CPU, in
// allocation, and in bytes written to state.json.
//
// THE FIX, in two halves:
//
//  1. PUSH. Each event is emitted to the frontend the moment it is appended,
//     as "ws:event" / "grpc:event", carrying just that one event. The frontend
//     appends it to a log it owns. This is O(1) per message and is what makes
//     a long session stay responsive.
//
//  2. BOUND. The response body stops being the full log and becomes its last
//     sessionEventTailLimit entries. Without this the push would be pure
//     addition: the O(N) rebuild would still run on every message, and the
//     whole log would still be re-serialised into state.json.
//
// WHAT (2) CHANGES FOR A USER, stated plainly because it is a real behaviour
// change and not merely an optimisation: reopening a request whose live session
// ran past the limit shows the last sessionEventTailLimit events rather than
// all of them. The full log remains complete in the live UI for as long as the
// session is open, because the frontend accumulates the pushes. The headers
// report the true total and how many were dropped, so nothing is silently
// hidden. This is the same trade §2.1.B makes for cached response bodies: an
// unbounded log inside AppState is re-serialised on every persist, and that
// cost is paid by every subsequent keystroke in the whole application, not just
// by the tab that owns the socket.
//
// ORDERING. Pushes carry Index and Total so the frontend can tell "I have every
// event" from "I missed one". A gap means the log it holds is untrustworthy and
// it should fall back to the body in the next response — the same
// detect-a-gap-and-resync contract AppState.Revision gives US-014.

import wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"

// sessionEventTailLimit is how many trailing events a live-session Response
// body carries.
//
// 200 is chosen against the existing tests and the UI rather than as a round
// number: the longest live-session assertion in the suite involves 4 events, so
// the cap never truncates anything a test looks at, and the response inspector
// renders the event array as a scrolling list where a few hundred rows is
// already more than a user reads. Raising it costs a linear amount of
// re-serialisation per message; lowering it costs history on reopen.
const sessionEventTailLimit = 200

// websocketEventPush is the "ws:event" payload.
type websocketEventPush struct {
	CollectionID string                `json:"collectionId"`
	ItemID       string                `json:"itemId"`
	Index        int                   `json:"index"`
	Total        int                   `json:"total"`
	Event        websocketSessionEvent `json:"event"`
}

// grpcEventPush is the "grpc:event" payload.
type grpcEventPush struct {
	CollectionID string                 `json:"collectionId"`
	ItemID       string                 `json:"itemId"`
	Index        int                    `json:"index"`
	Total        int                    `json:"total"`
	Event        grpcStreamSessionEvent `json:"event"`
}

// websocketEventEmitter builds the emit callback a session uses to push its
// events. It closes over the request identity because a session has no idea
// which request owns it, and the frontend needs that to route the event to the
// right tab.
//
// a.ctx is nil in every test and until Wails calls startup, so the nil check is
// load-bearing rather than defensive: wailsruntime.EventsEmit dereferences the
// context to find the event manager and would panic.
func (a *App) websocketEventEmitter(collectionID, itemID string) func(index, total int, event websocketSessionEvent) {
	return func(index, total int, event websocketSessionEvent) {
		if a.ctx == nil {
			return
		}
		wailsruntime.EventsEmit(a.ctx, "ws:event", websocketEventPush{
			CollectionID: collectionID,
			ItemID:       itemID,
			Index:        index,
			Total:        total,
			Event:        event,
		})
	}
}

func (a *App) grpcEventEmitter(collectionID, itemID string) func(index, total int, event grpcStreamSessionEvent) {
	return func(index, total int, event grpcStreamSessionEvent) {
		if a.ctx == nil {
			return
		}
		wailsruntime.EventsEmit(a.ctx, "grpc:event", grpcEventPush{
			CollectionID: collectionID,
			ItemID:       itemID,
			Index:        index,
			Total:        total,
			Event:        event,
		})
	}
}

// appendEventLocked is the single place a WebSocket session event is recorded.
// Routing every append through it is what guarantees no event can be added
// without being pushed — the failure mode that would leave the frontend's log
// permanently missing an entry with nothing to indicate it.
//
// session.mu must be held. The emit runs under that lock deliberately: the
// callers already hold it across a full network round trip (WriteMessage
// followed by ReadMessage under a read deadline), so a webview notify adds
// nothing to the critical section's shape, and emitting after the unlock would
// let two goroutines interleave and deliver events out of order.
func (session *websocketSession) appendEventLocked(event websocketSessionEvent) {
	session.events = append(session.events, event)
	if session.emit != nil {
		session.emit(len(session.events)-1, len(session.events), event)
	}
}

func (session *grpcStreamSession) appendEventLocked(event grpcStreamSessionEvent) {
	session.events = append(session.events, event)
	if session.emit != nil {
		session.emit(len(session.events)-1, len(session.events), event)
	}
}

// websocketEventTail returns the trailing window of events carried in a
// response body, plus how many older events it omits.
func websocketEventTail(events []websocketSessionEvent) (tail []websocketSessionEvent, omitted int) {
	if len(events) <= sessionEventTailLimit {
		return events, 0
	}
	return events[len(events)-sessionEventTailLimit:], len(events) - sessionEventTailLimit
}

func grpcEventTail(events []grpcStreamSessionEvent) (tail []grpcStreamSessionEvent, omitted int) {
	if len(events) <= sessionEventTailLimit {
		return events, 0
	}
	return events[len(events)-sessionEventTailLimit:], len(events) - sessionEventTailLimit
}
