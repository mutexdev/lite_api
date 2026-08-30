package history

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/mutexdev/lite_api/internal/atomicfile"
	"github.com/mutexdev/lite_api/internal/types"
)

// Limit is how many entries are retained.
const Limit = 500

// CompactAt is the line count that triggers a rewrite. Deliberately well
// above the limit: compacting at exactly the cap would rewrite the file on
// every send once it filled up, which is the cost the append-only format exists
// to avoid.
const CompactAt = Limit * 2

// RedactedHeaders never have their VALUES written to disk.
//
// History persists indefinitely in a plaintext file that no encryption covers.
// A bearer token captured once would sit there long after it was rotated, and
// the user has no reason to expect a request log to be a credential store. The
// header NAME is kept so the entry still shows the request was authenticated —
// dropping the row entirely would misrepresent what was sent.
var RedactedHeaders = map[string]bool{
	"authorization":       true,
	"proxy-authorization": true,
	"cookie":              true,
	"set-cookie":          true,
	"x-api-key":           true,
	"x-auth-token":        true,
}

const RedactedValue = "<redacted>"

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
	RequestHeaders  []types.KeyValue `json:"requestHeaders,omitempty"`
	ResponseHeaders []types.KeyValue `json:"responseHeaders,omitempty"`
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

type Store struct {
	mu   sync.Mutex
	path string
	// projectionDir holds the agent-facing, already-redacted copy of each
	// entry, one file per entry. See projection.go.
	projectionDir string
	// lines counts what is on disk, so compaction is decided without re-reading
	// the file on every append.
	lines  int
	loaded bool
	// corrupted is how many unreadable lines the last read skipped, and
	// reportedCorrupt makes the log about them once per store rather than once
	// per read. See reportCorrupted.
	corrupted       int
	reportedCorrupt bool
}

// RedactHeaders copies rows with credential values withheld.
func RedactHeaders(rows []types.KeyValue) ([]types.KeyValue, bool) {
	if len(rows) == 0 {
		return nil, false
	}
	out := make([]types.KeyValue, 0, len(rows))
	redacted := false
	for _, row := range rows {
		if RedactedHeaders[strings.ToLower(strings.TrimSpace(row.Name))] {
			out = append(out, types.KeyValue{Name: row.Name, Value: RedactedValue, Enabled: row.Enabled})
			redacted = true
			continue
		}
		out = append(out, types.KeyValue{Name: row.Name, Value: row.Value, Enabled: row.Enabled})
	}
	return out, redacted
}

func HeaderMapRows(headers map[string]string) []types.KeyValue {
	rows := make([]types.KeyValue, 0, len(headers))
	for name, value := range headers {
		rows = append(rows, types.KeyValue{Name: name, Value: value, Enabled: true})
	}
	return rows
}

// recordHistory appends one entry. Failures are returned but callers on the
// send path deliberately ignore them: a request that succeeded must not be
// reported as failed because its history line could not be written.
func (s *Store) Append(entry HistoryEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.appendLocked(entry)
}

func (s *Store) appendLocked(entry HistoryEntry) error {
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

	if s.lines > CompactAt {
		return s.compactLocked()
	}
	return nil
}

func (s *Store) loadCountLocked() error {
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
//
// Skipped is not the same as unnoticed. The count of unreadable lines is
// recorded and logged once per process, because "the entry I was looking for is
// not in the list" and "the log lost 300 lines to a corrupted file" look
// identical from the UI, and only one of them is worth clearing the file over.
func (s *Store) readLocked() ([]HistoryEntry, error) {
	file, err := os.Open(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer func() { _ = file.Close() }()

	var entries []HistoryEntry
	corrupted := 0
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
			corrupted++
			continue
		}
		entries = append(entries, entry)
	}
	s.corrupted = corrupted
	s.reportCorrupted(corrupted)
	if err := scanner.Err(); err != nil {
		return entries, err
	}
	return entries, nil
}

// CorruptedLines is how many unreadable lines the last read skipped.
func (s *Store) CorruptedLines() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.corrupted
}

// reportCorrupted logs the first read that skipped anything.
//
// Once per store rather than once per read: List and Get both re-read the whole
// file, so the history panel alone would log on every keystroke of its search
// box. s.mu is already held by readLocked's callers.
func (s *Store) reportCorrupted(corrupted int) {
	if corrupted == 0 || s.reportedCorrupt {
		return
	}
	s.reportedCorrupt = true
	suffix := "s"
	if corrupted == 1 {
		suffix = ""
	}
	logf("liteapi: skipped %d unreadable line%s in %s", corrupted, suffix, s.path)
}

// logf is the package's log sink, replaceable so a test can assert on what was
// reported without racing the standard logger.
var logf = log.Printf

// compactLocked rewrites the file with only the newest Limit entries.
func (s *Store) compactLocked() error {
	entries, err := s.readLocked()
	if err != nil {
		return err
	}
	if len(entries) > Limit {
		entries = entries[len(entries)-Limit:]
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
	if err := atomicfile.Write(s.path, []byte(buffer.String()), 0o600); err != nil {
		return err
	}
	s.lines = len(entries)
	// The agent-facing artifacts are keyed on entry id, so the ones whose
	// entries just fell off the end are now unreachable garbage.
	s.pruneProjectionsLocked(entries)
	return nil
}

// list returns entries newest first, filtered by query.
func (s *Store) List(query HistoryQuery) ([]HistoryEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	entries, err := s.readLocked()
	if err != nil {
		return nil, err
	}

	limit := query.Limit
	if limit <= 0 || limit > Limit {
		limit = Limit
	}

	out := make([]HistoryEntry, 0, limit)
	// Newest first, walking backwards so the limit applies to the newest
	// entries rather than the oldest.
	for i := len(entries) - 1; i >= 0 && len(out) < limit; i-- {
		if EntryMatches(entries[i], query) {
			out = append(out, entries[i])
		}
	}
	return out, nil
}

func EntryMatches(entry HistoryEntry, query HistoryQuery) bool {
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

func (s *Store) Get(id string) (HistoryEntry, error) {
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

func (s *Store) Clear() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.Remove(s.path); err != nil && !os.IsNotExist(err) {
		return err
	}
	s.lines = 0
	s.loaded = true
	// Clearing history has to clear the agent-facing copies too. Leaving them
	// behind would mean a user who cleared their history to get rid of a
	// recorded run still had it readable through MCP.
	return s.clearProjectionsLocked()
}

// NewStore opens the append-only history log at path. The file is created on
// first write, so a store for a path that does not exist yet is valid.
//
// The agent-facing projection directory is derived from the log's own location
// rather than passed in, so the two artifacts cannot be pointed at different
// places by a caller that only remembered to configure one of them.
func NewStore(path string) *Store {
	return &Store{path: path, projectionDir: filepath.Join(filepath.Dir(path), MCPProjectionDir)}
}
