package core

// US-048 — send history, stored outside state.json.
//
// "Outside state.json" is the criterion, and the reason is mechanical rather
// than tidiness: state.json is rewritten in full on every mutation, and history
// grows by one entry per send forever. Keeping it there would make every
// keystroke-triggered save carry the entire history, so the cost of a feature
// nobody is looking at would land on the hot path of the one they are.
//
// The file is JSONL and APPEND-ONLY on the hot path. A send costs one short
// write at the end of the file rather than a rewrite of everything, which is
// the whole reason to accept a line-oriented format over a JSON array. The cap
// is enforced by compacting when the file has drifted well past it, so the
// rewrite happens once every few hundred sends instead of on every one.
//
// BODIES ARE NOT STORED INLINE. They go through the US-009 response store,
// which is content-addressed: a hundred sends of the same request store one
// copy of the body. Inlining them would put megabytes into a file that is read
// end-to-end at startup.

import (
	"errors"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mutexdev/lite_api/internal/history"
	"github.com/mutexdev/lite_api/internal/mcpserver"
	"github.com/mutexdev/lite_api/internal/responsestore"
)

func (a *App) history() *history.Store {
	a.historyOnce.Do(func() {
		a.historyStore = history.NewStore(filepath.Join(a.dataDir, "history.jsonl"))
	})
	return a.historyStore
}

// --- bindings ------------------------------------------------------------

// ListHistory returns the newest entries, optionally filtered.
func (a *App) ListHistory(query history.HistoryQuery) ([]history.HistoryEntry, error) {
	return a.history().List(query)
}

// GetHistoryEntry returns one entry by id.
func (a *App) GetHistoryEntry(id string) (history.HistoryEntry, error) {
	if strings.TrimSpace(id) == "" {
		return history.HistoryEntry{}, errors.New("history id is required")
	}
	return a.history().Get(id)
}

// GetHistoryBody returns the recorded body for an entry.
//
// Read through the response store rather than stored on the entry, so a
// hundred sends of the same request cost one copy of the body.
func (a *App) GetHistoryBody(id string) (string, error) {
	entry, err := a.GetHistoryEntry(id)
	if err != nil {
		return "", err
	}
	if entry.BodyHandle == "" {
		return "", nil
	}
	store, err := a.responseStore()
	if err != nil {
		return "", err
	}
	body, err := store.Get(responsestore.Handle(entry.BodyHandle))
	if err != nil {
		// The store is pruned independently of history, so a missing body is
		// expected for old entries rather than an error worth failing on.
		return "", nil
	}
	return string(body), nil
}

// ClearHistory removes every entry.
func (a *App) ClearHistory() error {
	return a.history().Clear()
}

// recordSendHistory writes one entry for a completed send.
//
// Best-effort by design: the error is returned for tests but the send path
// ignores it. A request that reached the server must not report failure
// because its history line could not be written.
//
// The failure is logged once per session before the error is handed back, so an
// unwritable history file is not simply invisible. See reportHistoryFailure.
func (a *App) recordSendHistory(collectionID string, item RequestItem, response *Response) error {
	return a.recordSendHistoryWithMCPProjection(collectionID, item, response, nil)
}

// recordSendHistoryWithMCPProjection is recordSendHistory plus the Phase 6 §7
// artifact: an already-redacted, already-value-masked copy of the same send,
// written as a sibling of the history log and served as the ONLY thing MCP's
// get_history ever reads.
//
// WHY THE VALUES ARE A PARAMETER. This runs with a.mu already held, and
// mcpHydratedSecretValues re-acquires that lock through readStateForMCP
// (mcp_backend.go:70) — calling it from here deadlocks the send path outright.
// So the caller hydrates once at the head of the send, under the first lock it
// is already holding, and carries the values down. Those are the values that
// were live for THIS send, which is the whole point: read-time masking against
// whatever the variable holds tomorrow is exactly what rotation defeats.
//
// The record-time walk of a.state below is the same walk under the same
// already-held lock, and it is additive rather than a substitute (see
// mcpHistorySecretMaskValues).
//
// CALLED WITH a.mu HELD.
func (a *App) recordSendHistoryWithMCPProjection(collectionID string, item RequestItem, response *Response, mcpMaskValues []string) error {
	if response == nil {
		return nil
	}
	requestHeaders, requestRedacted := history.RedactHeaders(item.Headers)
	responseHeaders, responseRedacted := history.RedactHeaders(history.HeaderMapRows(response.Headers))

	entry := history.HistoryEntry{
		ID:              newID("history"),
		At:              time.Now(),
		CollectionID:    collectionID,
		ItemID:          item.ID,
		Name:            item.Name,
		Method:          strings.ToUpper(firstNonEmpty(item.Method, "GET")),
		URL:             firstNonEmpty(response.RequestedURL, item.URL),
		Status:          response.Status,
		StatusText:      response.StatusText,
		DurationMs:      response.DurationMs,
		Size:            response.Size,
		Error:           response.Error,
		RequestHeaders:  requestHeaders,
		ResponseHeaders: responseHeaders,
		Redacted:        requestRedacted || responseRedacted,
		BodyHandle:      response.BodyHandle,
	}
	projection := a.mcpHistoryProjectionLocked(entry, response, mcpMaskValues)
	return a.reportHistoryFailure(a.history().AppendWithMCPProjection(entry, &projection))
}

// mcpHistoryProjectionLocked builds the agent-facing view of one recorded send.
//
// Both halves of the §1.3 output boundary are applied HERE, once, and only the
// result is written down:
//
//   - name-based redaction — internal/history has already replaced the values
//     of credential-shaped header names on the way into the entry, and
//     mcpserver's own name heuristics catch the two shapes it does not know
//     about: a credential-shaped query parameter sitting in the resolved URL
//     ("?api_key=sk_live_..."), and a credential-shaped header name outside
//     history's short list;
//   - exact-value masking against every secret value that was live for this
//     send.
//
// The body comes off the response in memory rather than back out of the
// response store: it is the same bytes, and reading it back would put a disk
// round trip on the send path's tail while a.mu is held.
//
// CALLED WITH a.mu HELD (it walks a.state).
func (a *App) mcpHistoryProjectionLocked(entry history.HistoryEntry, response *Response, mcpMaskValues []string) history.MCPProjection {
	values := a.mcpHistorySecretMaskValues(mcpMaskValues)

	// Mask BEFORE truncating (§1.3). The other order lets a secret that
	// straddles the limit be cut in half, and half a credential is a string no
	// masker recognises but a reader can still recombine across two runs.
	body := mcpserver.MaskKnownSecretValues(response.Body, values)
	truncated := false
	if len(body) > mcpHistoryBodyLimit {
		body = truncateAtRuneBoundary(body, mcpHistoryBodyLimit)
		truncated = true
	}

	return history.MCPProjection{
		Method:          entry.Method,
		URL:             mcpserver.MaskKnownSecretValues(mcpserver.RedactURLQueryLiterals(entry.URL), values),
		RequestHeaders:  mcpProjectedHeaderRows(entry.RequestHeaders, values),
		ResponseHeaders: mcpProjectedHeaderRows(entry.ResponseHeaders, values),
		Body:            body,
		Truncated:       truncated,
	}
}

// mcpHistorySecretMaskValues is the head-of-send set UNIONED with a fresh walk
// of state, and the union is deliberate in both directions.
//
// The passed values are the authoritative half: they were hydrated at the head
// of this send, so a variable that changed while the request was in flight is
// still masked out of the record of the send that used it.
//
// The state walk is what makes the artifact safe for a UI send. get_history
// serves the projection for every entry regardless of who caused it, so a UI
// send whose URL carries a secret would otherwise persist that secret in the
// one file whose entire purpose is to be the copy an agent may read. The send
// path only hydrates values under an MCP policy — it has no reason to pay for
// the walk on a UI send — so the record path does it here instead, under the
// lock it is already holding.
//
// CALLED WITH a.mu HELD.
func (a *App) mcpHistorySecretMaskValues(mcpMaskValues []string) []string {
	// mcpSecretValuesLocked is the ONE lock-free walk in this package; the
	// agent-facing reader reaches the same function through
	// mcpHydratedSecretValues, which supplies the lock the reader does not
	// already hold.
	current := mcpSecretValuesLocked(&a.state)
	if len(mcpMaskValues) == 0 {
		return current
	}
	values := make([]string, 0, len(mcpMaskValues)+len(current))
	values = append(values, mcpMaskValues...)
	return append(values, current...)
}

// mcpProjectedHeaderRows applies mcpserver's name-based redaction and then the
// exact-value mask to rows internal/history has already redacted once.
//
// Two passes over the same rows because the two heuristics disagree usefully:
// history's list is the short one that governs what goes to disk in the app's
// own log, and mcpserver's is the wider agent-facing one.
//
// A row history ALREADY redacted keeps history's own marker rather than being
// re-marked as mcpserver's. The two markers differ on purpose — see
// mcpserver.MaskedValue's comment — so that a marker found somewhere it should
// not be names the layer that produced it. Letting the second pass overwrite
// the first would destroy exactly that signal, and would say "<masked>" about a
// value that was never written to disk in the first place.
func mcpProjectedHeaderRows(rows []KeyValue, values []string) []KeyValue {
	if len(rows) == 0 {
		return nil
	}
	redacted := mcpserver.RedactRows(mcpKeyValueRows(rows))
	out := make([]KeyValue, 0, len(redacted))
	for index, row := range redacted {
		value := row.Value
		if rows[index].Value == history.RedactedValue {
			value = history.RedactedValue
		}
		out = append(out, KeyValue{
			Name:    row.Name,
			Value:   mcpserver.MaskKnownSecretValues(value, values),
			Enabled: row.Enabled,
		})
	}
	return out
}

// truncateAtRuneBoundary cuts to at most limit bytes without splitting a rune.
//
// Same reason as responseBodyHead: slicing a byte count out of a UTF-8 string
// can leave a partial rune, which encoding/json then rewrites as U+FFFD — so a
// body of CJK text or emoji would reach the agent subtly corrupted.
func truncateAtRuneBoundary(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut]
}

// CreateRequestFromHistory materialises a history entry as a real request in
// the given collection (US-049, "save to collection").
//
// A new request rather than a mutation of the original: the entry may point at
// a request that has since been edited or deleted, and silently overwriting a
// live request with an old snapshot would destroy work the user did after that
// send.
//
// REDACTED HEADERS ARE DROPPED, not carried across as the literal
// "<redacted>". A request carrying `Authorization: <redacted>` looks configured
// and fails with a 401 that points nowhere; an absent header is visibly
// something to fill in. The returned request keeps everything else.
func (a *App) CreateRequestFromHistory(collectionID, historyID string) (AppState, error) {
	entry, err := a.GetHistoryEntry(historyID)
	if err != nil {
		return AppState{}, err
	}
	if strings.TrimSpace(collectionID) == "" {
		return AppState{}, errors.New("collection id is required")
	}

	name := strings.TrimSpace(entry.Name)
	if name == "" {
		name = strings.TrimSpace(entry.Method + " " + entry.URL)
	}
	if name == "" {
		name = "History request"
	}

	state, err := a.CreateRequest(collectionID, "http", name)
	if err != nil {
		return AppState{}, err
	}

	var itemID string
	for _, workspace := range state.Workspaces {
		for _, collection := range workspace.Collections {
			if collection.ID != collectionID {
				continue
			}
			for _, item := range collection.Items {
				if item.Name == name {
					itemID = item.ID
				}
			}
		}
	}
	if itemID == "" {
		return AppState{}, errors.New("the request created from history could not be found")
	}

	headers := make([]KeyValue, 0, len(entry.RequestHeaders))
	for _, header := range entry.RequestHeaders {
		if header.Value == history.RedactedValue {
			continue
		}
		headers = append(headers, header)
	}

	method := entry.Method
	url := entry.URL
	return a.UpdateRequest(collectionID, itemID, RequestPatch{
		Method:  &method,
		URL:     &url,
		Headers: &headers,
	})
}
