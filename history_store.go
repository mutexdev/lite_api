package main

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
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// historyLimit is how many entries are retained.
const historyLimit = 500

// historyCompactAt is the line count that triggers a rewrite. Deliberately well
// above the limit: compacting at exactly the cap would rewrite the file on
// every send once it filled up, which is the cost the append-only format exists
// to avoid.
const historyCompactAt = historyLimit * 2

// historyRedactedHeaders never have their VALUES written to disk.
//
// History persists indefinitely in a plaintext file that no encryption covers.
// A bearer token captured once would sit there long after it was rotated, and
// the user has no reason to expect a request log to be a credential store. The
// header NAME is kept so the entry still shows the request was authenticated —
// dropping the row entirely would misrepresent what was sent.
var historyRedactedHeaders = map[string]bool{
	"authorization":       true,
	"proxy-authorization": true,
	"cookie":              true,
	"set-cookie":          true,
	"x-api-key":           true,
	"x-auth-token":        true,
}

const historyRedactedValue = "<redacted>"

// HistoryEntry is one recorded send.
type HistoryEntry struct {
	ID           string    `json:"id"`
	At           time.Time `json:"at"`
	CollectionID string    `json:"collectionId,omitempty"`
	ItemID       string    `json:"itemId,omitempty"`
	Name         string    `json:"name,omitempty"`
	Method       string    `json:"method"`
	URL          string    `json:"url"`
	Status       int       `json:"status,omitempty"`
	StatusText   string    `json:"statusText,omitempty"`
	DurationMs   int64     `json:"durationMs,omitempty"`
	Size         int       `json:"size,omitempty"`
	Error        string    `json:"error,omitempty"`
	// RequestHeaders and ResponseHeaders have credential values redacted.
	RequestHeaders  []KeyValue `json:"requestHeaders,omitempty"`
	ResponseHeaders []KeyValue `json:"responseHeaders,omitempty"`
	// Redacted reports that at least one header value was withheld, so the UI
	// can say so rather than presenting a doctored request as the real one.
	Redacted bool `json:"redacted,omitempty"`
	// BodyHandle points into the US-009 response store.
	BodyHandle string `json:"bodyHandle,omitempty"`
}

// HistoryQuery filters a listing.
type HistoryQuery struct {
	// Text matches against name, method, URL and status, case-insensitively.
	Text         string `json:"text,omitempty"`
	CollectionID string `json:"collectionId,omitempty"`
	Method       string `json:"method,omitempty"`
	// OnlyFailures restricts to transport errors and >=400 responses.
	OnlyFailures bool `json:"onlyFailures,omitempty"`
	Limit        int  `json:"limit,omitempty"`
}

type historyStore struct {
	mu   sync.Mutex
	path string
	// lines counts what is on disk, so compaction is decided without re-reading
	// the file on every append.
	lines  int
	loaded bool
}

func (a *App) history() *historyStore {
	a.historyOnce.Do(func() {
		a.historyStore = &historyStore{path: filepath.Join(a.dataDir, "history.jsonl")}
	})
	return a.historyStore
}

// redactHeaders copies rows with credential values withheld.
func redactHeaders(rows []KeyValue) ([]KeyValue, bool) {
	if len(rows) == 0 {
		return nil, false
	}
	out := make([]KeyValue, 0, len(rows))
	redacted := false
	for _, row := range rows {
		if historyRedactedHeaders[strings.ToLower(strings.TrimSpace(row.Name))] {
			out = append(out, KeyValue{Name: row.Name, Value: historyRedactedValue, Enabled: row.Enabled})
			redacted = true
			continue
		}
		out = append(out, KeyValue{Name: row.Name, Value: row.Value, Enabled: row.Enabled})
	}
	return out, redacted
}

func headerMapRows(headers map[string]string) []KeyValue {
	rows := make([]KeyValue, 0, len(headers))
	for name, value := range headers {
		rows = append(rows, KeyValue{Name: name, Value: value, Enabled: true})
	}
	return rows
}

// recordHistory appends one entry. Failures are returned but callers on the
// send path deliberately ignore them: a request that succeeded must not be
// reported as failed because its history line could not be written.
func (s *historyStore) append(entry HistoryEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.loadCountLocked(); err != nil {
		return err
	}

	encoded, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(s.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := file.Write(append(encoded, '\n')); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	s.lines++

	if s.lines > historyCompactAt {
		return s.compactLocked()
	}
	return nil
}

func (s *historyStore) loadCountLocked() error {
	if s.loaded {
		return nil
	}
	entries, err := s.readLocked()
	if err != nil {
		return err
	}
	s.lines = len(entries)
	s.loaded = true
	return nil
}

// readLocked returns every entry on disk, oldest first.
//
// A malformed line is SKIPPED rather than failing the read. History is a
// convenience, and one truncated line — the likely result of a crash mid-write
// — must not make the whole log unreadable.
func (s *historyStore) readLocked() ([]HistoryEntry, error) {
	file, err := os.Open(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer func() { _ = file.Close() }()

	var entries []HistoryEntry
	scanner := bufio.NewScanner(file)
	// A long URL or header set can exceed bufio's default 64 KiB line cap, and
	// the default would silently stop the scan at that point.
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var entry HistoryEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		entries = append(entries, entry)
	}
	if err := scanner.Err(); err != nil {
		return entries, err
	}
	return entries, nil
}

// compactLocked rewrites the file with only the newest historyLimit entries.
func (s *historyStore) compactLocked() error {
	entries, err := s.readLocked()
	if err != nil {
		return err
	}
	if len(entries) > historyLimit {
		entries = entries[len(entries)-historyLimit:]
	}

	var buffer strings.Builder
	for _, entry := range entries {
		encoded, err := json.Marshal(entry)
		if err != nil {
			continue
		}
		buffer.Write(encoded)
		buffer.WriteByte('\n')
	}
	// Atomic for the same reason state.json is: a half-written history file
	// read at the next startup would lose every entry after the tear.
	if err := writeFileAtomic(s.path, []byte(buffer.String()), 0o600); err != nil {
		return err
	}
	s.lines = len(entries)
	return nil
}

// list returns entries newest first, filtered by query.
func (s *historyStore) list(query HistoryQuery) ([]HistoryEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	entries, err := s.readLocked()
	if err != nil {
		return nil, err
	}

	limit := query.Limit
	if limit <= 0 || limit > historyLimit {
		limit = historyLimit
	}

	out := make([]HistoryEntry, 0, limit)
	// Newest first, walking backwards so the limit applies to the newest
	// entries rather than the oldest.
	for i := len(entries) - 1; i >= 0 && len(out) < limit; i-- {
		if historyEntryMatches(entries[i], query) {
			out = append(out, entries[i])
		}
	}
	return out, nil
}

func historyEntryMatches(entry HistoryEntry, query HistoryQuery) bool {
	if query.CollectionID != "" && entry.CollectionID != query.CollectionID {
		return false
	}
	if query.Method != "" && !strings.EqualFold(entry.Method, query.Method) {
		return false
	}
	if query.OnlyFailures && entry.Error == "" && entry.Status < 400 {
		return false
	}
	text := strings.TrimSpace(strings.ToLower(query.Text))
	if text == "" {
		return true
	}
	haystack := strings.ToLower(strings.Join([]string{
		entry.Name, entry.Method, entry.URL, entry.StatusText,
		fmt.Sprintf("%d", entry.Status),
	}, " "))
	// Every whitespace-separated term must match, so "post users" narrows
	// rather than widening the way a single-substring search would.
	for _, term := range strings.Fields(text) {
		if !strings.Contains(haystack, term) {
			return false
		}
	}
	return true
}

func (s *historyStore) get(id string) (HistoryEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	entries, err := s.readLocked()
	if err != nil {
		return HistoryEntry{}, err
	}
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].ID == id {
			return entries[i], nil
		}
	}
	return HistoryEntry{}, fmt.Errorf("history entry %s not found", id)
}

func (s *historyStore) clear() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.Remove(s.path); err != nil && !os.IsNotExist(err) {
		return err
	}
	s.lines = 0
	s.loaded = true
	return nil
}

// --- bindings ------------------------------------------------------------

// ListHistory returns the newest entries, optionally filtered.
func (a *App) ListHistory(query HistoryQuery) ([]HistoryEntry, error) {
	return a.history().list(query)
}

// GetHistoryEntry returns one entry by id.
func (a *App) GetHistoryEntry(id string) (HistoryEntry, error) {
	if strings.TrimSpace(id) == "" {
		return HistoryEntry{}, errors.New("history id is required")
	}
	return a.history().get(id)
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
	body, err := store.Get(responseHandle(entry.BodyHandle))
	if err != nil {
		// The store is pruned independently of history, so a missing body is
		// expected for old entries rather than an error worth failing on.
		return "", nil
	}
	return string(body), nil
}

// ClearHistory removes every entry.
func (a *App) ClearHistory() error {
	return a.history().clear()
}

// recordSendHistory writes one entry for a completed send.
//
// Best-effort by design: the error is returned for tests but the send path
// ignores it. A request that reached the server must not report failure
// because its history line could not be written.
func (a *App) recordSendHistory(collectionID string, item RequestItem, response *Response) error {
	if response == nil {
		return nil
	}
	requestHeaders, requestRedacted := redactHeaders(item.Headers)
	responseHeaders, responseRedacted := redactHeaders(headerMapRows(response.Headers))

	entry := HistoryEntry{
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
	return a.history().append(entry)
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
		if header.Value == historyRedactedValue {
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
