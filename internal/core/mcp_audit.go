package core

// The MCP audit log — rule 6 of docs/mcp-agent-interface.md: from Phase 2 every
// tool call an agent makes is recorded and visible in the app.
//
// THIS IS internal/history's DISCIPLINE, SCALED DOWN, and deliberately not a
// reuse of it. The shape is the same because the constraints are the same:
//
//   - Outside state.json. state.json is rewritten in full on every mutation and
//     this file grows by one line per agent call forever. Putting it there would
//     put the audit log on the hot path of every keystroke.
//   - JSONL, APPEND-ONLY on the hot path. One short write at the end of the file
//     per call rather than a rewrite of everything; the cap is enforced by
//     compacting once the file has drifted well past it, so the rewrite happens
//     every few hundred calls instead of on every one. That is the whole reason
//     to accept a line-oriented format over a JSON array — see history.Append,
//     which this mirrors line for line.
//   - A malformed line is SKIPPED, not fatal. A truncated last line (a crash
//     mid-write) must not make the log unreadable.
//
// What it does NOT share with history is the entry type or the query surface.
// history.HistoryEntry carries request/response artifacts and is filtered by
// collection, method and text; an audit entry is five scalars read newest-first
// with a limit, and giving it a query language it has no user for would be
// inventing a second store's worth of behaviour for a panel that lists rows.
//
// LOCKING. The store owns its own mutex and is a leaf: nothing it does takes
// a.mu. That is load-bearing rather than tidy — the recorder runs on the MCP
// server's HTTP goroutines (see mcpserver.AuditRecorder), which own none of this
// App's locks, and blocking one of them on the state lock would make an agent's
// call latency depend on whatever the user is typing. The ONLY path here that
// touches a.mu is the failure notification, and only after the store has already
// returned.

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/mutexdev/lite_api/internal/atomicfile"
	"github.com/mutexdev/lite_api/internal/mcpserver"
	"github.com/mutexdev/lite_api/internal/types"
)

// mcpAuditLimit is how many entries are retained, and mcpAuditCompactAt the line
// count that triggers the rewrite. The gap between them is the point: compacting
// at exactly the cap would rewrite the file on every call once it filled up,
// which is the cost the append-only format exists to avoid.
const (
	mcpAuditLimit     = 500
	mcpAuditCompactAt = 1000
)

// The read limits GetMCPAuditLog applies, following the same "0 means the
// default, above the cap means the cap" convention as every other list surface
// in this package (mcpBoundedLimit).
const (
	mcpAuditDefaultLimit = 50
	mcpAuditMaxLimit     = 200
)

// mcpAuditNotificationChannel keys the notify-on-change machinery, so a store
// that cannot be written — a read-only data directory, a full disk — is reported
// once rather than on every agent call.
const mcpAuditNotificationChannel = "mcp-audit"

// mcpAuditStore is the append-only log at <dataDir>/mcp-audit.jsonl.
type mcpAuditStore struct {
	mu   sync.Mutex
	path string
	// lines counts what is on disk, so compaction is decided without re-reading
	// the file on every append.
	lines  int
	loaded bool
	// failed records that a write failed and a notification was raised for it,
	// so the recorder knows whether there is anything to clear. Atomic rather
	// than under s.mu because it is read on the success path, which must not
	// wait behind a write in progress.
	failed atomic.Bool
}

// mcpAudit lazily builds the store. Lazy for the same reason the response store
// is: every test App and every scoped workspace window runs this constructor,
// and an install that never enables MCP should never create the file.
func (a *App) mcpAudit() *mcpAuditStore {
	a.mcpAuditOnce.Do(func() {
		a.mcpAuditStore = &mcpAuditStore{path: filepath.Join(a.dataDir, "mcp-audit.jsonl")}
	})
	return a.mcpAuditStore
}

// Append writes one entry and compacts when the file has drifted past the
// threshold.
func (s *mcpAuditStore) Append(entry types.MCPAuditEntry) error {
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

	if s.lines > mcpAuditCompactAt {
		return s.compactLocked()
	}
	return nil
}

// List returns entries newest first, capped at limit.
func (s *mcpAuditStore) List(limit int) ([]types.MCPAuditEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	entries, err := s.readLocked()
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > mcpAuditLimit {
		limit = mcpAuditLimit
	}
	out := make([]types.MCPAuditEntry, 0, limit)
	// Walking backwards so the limit applies to the NEWEST entries rather than
	// the oldest — the same reasoning as history.List.
	for index := len(entries) - 1; index >= 0 && len(out) < limit; index-- {
		out = append(out, entries[index])
	}
	return out, nil
}

func (s *mcpAuditStore) loadCountLocked() error {
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

// readLocked returns every entry on disk, oldest first. A malformed line is
// skipped: one truncated line from a crash mid-write must not make the whole
// log unreadable.
func (s *mcpAuditStore) readLocked() ([]types.MCPAuditEntry, error) {
	file, err := os.Open(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer func() { _ = file.Close() }()

	var entries []types.MCPAuditEntry
	scanner := bufio.NewScanner(file)
	// A long ArgsSummary can exceed bufio's default 64 KiB line cap, and the
	// default would silently stop the scan at that point.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var entry types.MCPAuditEntry
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

// compactLocked rewrites the file with only the newest mcpAuditLimit entries.
// Atomic for the same reason history's is: a half-written file read at the next
// startup would lose every entry after the tear.
func (s *mcpAuditStore) compactLocked() error {
	entries, err := s.readLocked()
	if err != nil {
		return err
	}
	if len(entries) > mcpAuditLimit {
		entries = entries[len(entries)-mcpAuditLimit:]
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
	if err := atomicfile.Write(s.path, []byte(buffer.String()), 0o600); err != nil {
		return err
	}
	s.lines = len(entries)
	return nil
}

// recordMCPAudit is the mcpserver.AuditRecorder this App installs on its server.
//
// It runs on the tool call's own goroutine, so it does exactly one short file
// write and returns. The failure path is the only place a.mu appears, and it is
// reached after the store has released its own lock: the two are never held
// together, in either order.
func (a *App) recordMCPAudit(entry mcpserver.AuditEntry) {
	store := a.mcpAudit()
	err := store.Append(types.MCPAuditEntry{
		At:          entry.At.UTC(),
		Tool:        entry.Tool,
		ArgsSummary: entry.ArgsSummary,
		Outcome:     entry.Outcome,
		DurationMs:  entry.DurationMs,
	})
	if err == nil {
		// THE ORDINARY PATH TAKES NO LOCK BUT THE STORE'S OWN. Clearing the
		// notification channel means taking a.mu, and doing that on every
		// successful call would put the state lock — the one every binding in
		// the app contends for — on the hot path of an agent's tool calls. The
		// channel only needs clearing when something was actually raised on it,
		// which the flag below records without a lock.
		if store.failed.CompareAndSwap(true, false) {
			a.clearMCPAuditFailure()
		}
		return
	}
	store.failed.Store(true)
	a.mu.Lock()
	defer a.mu.Unlock()
	// notifyChangedLocked, not notify: a data directory that cannot be written
	// fails on every single call, and the 20-entry notification list would be
	// nothing but copies of one message within a minute of an agent working.
	a.notifyChangedLocked(mcpAuditNotificationChannel, "error",
		fmt.Sprintf("An MCP tool call could not be recorded in the audit log: %v", err))
}

// clearMCPAuditFailure resets the channel so a failure that goes away and comes
// back is reported again rather than suppressed forever as a repeat.
func (a *App) clearMCPAuditFailure() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.notifyChangedLocked(mcpAuditNotificationChannel, "error", "")
}

// --- bindings ------------------------------------------------------------

// GetMCPAuditLog returns the newest recorded tool calls, newest first.
//
// limit follows the convention the MCP list tools use: 0 or negative takes the
// default, anything above the ceiling takes the ceiling. The panel showing this
// is a list, not a data export — an agent session that made ten thousand calls
// is answered with the last two hundred, and the file itself holds the rest.
func (a *App) GetMCPAuditLog(limit int) ([]types.MCPAuditEntry, error) {
	entries, err := a.mcpAudit().List(mcpBoundedLimit(limit, mcpAuditDefaultLimit, mcpAuditMaxLimit))
	if err != nil {
		return nil, err
	}
	if entries == nil {
		entries = []types.MCPAuditEntry{}
	}
	return entries, nil
}
