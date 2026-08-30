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

	"github.com/mutexdev/lite_api/internal/history"
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
	return a.reportHistoryFailure(a.history().Append(entry))
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
