package history

// Phase 6 §7 — the record-time, MCP-safe projection of one recorded send.
//
// THE PROBLEM. History is recorded AFTER interpolation, so a request templated
// as `?key={{secret}}` has the resolved credential sitting in its recorded URL.
// The agent-facing reader used to compensate by masking with the secret values
// the process holds RIGHT NOW. That works exactly until the variable changes:
// rotate it, or delete it, and yesterday's recorded value is no longer in the
// mask set, so the old credential comes straight back out to the agent.
//
// THE FIX, and why it is a separate artifact. The masking is moved to RECORD
// time, when the values that were live for that send are still known, and the
// already-redacted result is persisted alongside the entry. An artifact that
// never contained the value cannot leak it later, whatever happens to the
// variable afterwards. The app's own history.jsonl is untouched — this is an
// ADDITION, not a replacement, because the app's history panel is a local view
// for the user who owns the credentials and the agent-facing copy is not.
//
// WHAT IS NOT DONE HERE. The plaintext secret values are never written down.
// Persisting them as a per-entry "redaction set" would let a reader do the
// masking at read time and would be far simpler — and it would turn the history
// directory into a credential store that outlives every rotation. The masking
// is applied once, at record time, and only its output is kept.
//
// One file per entry rather than a column on the JSONL line: history.jsonl is
// read end-to-end at startup and on every List, and a body prefix per entry
// would put megabytes on that path for a view almost no session opens.

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/mutexdev/lite_api/internal/atomicfile"
	"github.com/mutexdev/lite_api/internal/types"
)

// MCPProjectionDir is the sibling directory the projections live in, next to
// history.jsonl.
const MCPProjectionDir = "history-mcp"

// MCPProjectionVersion stamps each artifact. A reader that meets a version it
// does not understand treats the entry as unprojected and serves the
// placeholder, which is the safe direction: the alternative is guessing at the
// meaning of fields written by a different redaction policy.
const MCPProjectionVersion = 1

// MCPProjection is the agent-facing view of one recorded send, already
// redacted and already value-masked at the moment it was written.
//
// Every string field in here has been through both halves of §1.3's output
// boundary. Nothing on this struct is raw.
type MCPProjection struct {
	Version int    `json:"version"`
	EntryID string `json:"entryId"`
	Method  string `json:"method,omitempty"`
	URL     string `json:"url,omitempty"`
	// RequestHeaders is not served by GetHistory today. It is recorded because
	// the projection is written once and read for the life of the entry: a
	// later tool that wants the request side must be able to have it WITHOUT a
	// migration that cannot retroactively mask anything.
	RequestHeaders  []types.KeyValue `json:"requestHeaders,omitempty"`
	ResponseHeaders []types.KeyValue `json:"responseHeaders,omitempty"`
	Body            string           `json:"body,omitempty"`
	// Truncated reports that the body was cut to the agent-facing limit. The
	// cut happens AFTER masking (§1.3, mask-before-truncate), so a secret that
	// straddles the boundary is replaced before it can be sliced in half into
	// something no masker would recognise.
	Truncated bool `json:"truncated,omitempty"`
}

// AppendWithMCPProjection appends the entry and writes its agent-facing
// projection under the same lock.
//
// The projection is written FIRST so that no window exists in which an entry
// is listable but its projection is not — a reader that hit that window would
// report a fresh send as "recorded before agent-safe history existed", which is
// both wrong and alarming.
//
// A projection write that fails does NOT suppress the entry. History is the
// user's own record and losing it because a sibling file could not be written
// would be the worse failure; the reader degrades to the placeholder, which is
// safe. Both errors are joined so the caller sees either.
func (s *Store) AppendWithMCPProjection(entry HistoryEntry, projection *MCPProjection) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var projectionErr error
	if projection != nil {
		stamped := *projection
		stamped.Version = MCPProjectionVersion
		stamped.EntryID = entry.ID
		projectionErr = s.writeProjectionLocked(stamped)
	}
	return errors.Join(projectionErr, s.appendLocked(entry))
}

// MCPProjection returns the projection for one entry.
//
// The bool is "there is a usable projection", not "the read succeeded". A
// missing file (the entry predates this artifact), an unparseable one (a crash
// mid-write) and a version this build does not know are all the same answer to
// the only question the caller has, and all three must produce the placeholder
// rather than a fallback to the raw entry.
func (s *Store) MCPProjection(entryID string) (MCPProjection, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	path, ok := s.projectionPathLocked(entryID)
	if !ok {
		return MCPProjection{}, false
	}
	// path is projectionPathLocked's: a validated single component under this
	// store's own directory, never a caller's string.
	raw, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			logf("liteapi: could not read the agent-facing history projection %s: %v", path, err)
		}
		return MCPProjection{}, false
	}
	var projection MCPProjection
	if err := json.Unmarshal(raw, &projection); err != nil {
		logf("liteapi: the agent-facing history projection %s is unreadable: %v", path, err)
		return MCPProjection{}, false
	}
	if projection.Version != MCPProjectionVersion {
		return MCPProjection{}, false
	}
	return projection, true
}

func (s *Store) writeProjectionLocked(projection MCPProjection) error {
	path, ok := s.projectionPathLocked(projection.EntryID)
	if !ok {
		return errors.New("history: the entry id is not usable as a projection filename")
	}
	encoded, err := json.Marshal(projection)
	if err != nil {
		return err
	}
	// WritePrivate, not a plain write: owner-only on both the file AND the
	// directory, and atomic, so a torn artifact is never read as a whole one.
	return atomicfile.WritePrivate(path, encoded)
}

// projectionPathLocked turns an entry id into a path inside the projection
// directory, or refuses.
//
// Entry ids are generated by this process, so the validation is not defending
// against a hostile caller so much as against a corrupted history line: ids are
// read back off disk, and an id of "../../state.json" reaching a write or a
// remove would be a very bad day for a check nobody wrote.
func (s *Store) projectionPathLocked(entryID string) (string, bool) {
	// A Store built as a struct literal — several tests in this package do —
	// has no projection directory, and filepath.Join("", id) is a RELATIVE path
	// that would drop artifacts into the working directory. Refuse rather than
	// write somewhere nobody will ever look.
	if s.projectionDir == "" {
		return "", false
	}
	id := strings.TrimSpace(entryID)
	if id == "" || id == "." || id == ".." {
		return "", false
	}
	if strings.ContainsAny(id, `/\`) || strings.ContainsRune(id, os.PathSeparator) {
		return "", false
	}
	if id != filepath.Base(id) || strings.HasPrefix(id, ".") {
		return "", false
	}
	return filepath.Join(s.projectionDir, id), true
}

// pruneProjectionsLocked deletes the artifacts of entries compaction just
// dropped.
//
// Without this the directory would grow by one file per send forever while
// history itself stays capped at Limit — the projections would outlive the
// entries that name them, and nothing would ever read or remove them again.
// Best-effort: a projection that cannot be deleted is garbage, not a reason to
// fail a compaction that already succeeded.
func (s *Store) pruneProjectionsLocked(retained []HistoryEntry) {
	directory, err := os.ReadDir(s.projectionDir)
	if err != nil {
		return
	}
	keep := make(map[string]bool, len(retained))
	for _, entry := range retained {
		keep[entry.ID] = true
	}
	for _, file := range directory {
		if file.IsDir() || keep[file.Name()] {
			continue
		}
		_ = os.Remove(filepath.Join(s.projectionDir, file.Name()))
	}
}

// clearProjectionsLocked removes every artifact. Clear() means clear.
func (s *Store) clearProjectionsLocked() error {
	if err := os.RemoveAll(s.projectionDir); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
