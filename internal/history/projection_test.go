package history

// The storage half of the Phase 6 §7 projection. What the artifact CONTAINS is
// decided in internal/core, where the secret values live; what is tested here
// is that the file lands in the right place, comes back only when it is really
// usable, and does not outlive the entry it belongs to.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/mutexdev/lite_api/internal/types"
)

func projectionStore(t *testing.T) (*Store, string) {
	t.Helper()
	dir := t.TempDir()
	return NewStore(filepath.Join(dir, "history.jsonl")), dir
}

func TestMCPProjectionRoundTripsAsASiblingArtifact(t *testing.T) {
	store, dir := projectionStore(t)

	entry := HistoryEntry{ID: "history-1", Method: "POST", URL: "https://api.test/things"}
	projection := MCPProjection{
		Method:          "POST",
		URL:             "https://api.test/things?trace=<masked>",
		ResponseHeaders: []types.KeyValue{{Name: "Content-Type", Value: "application/json", Enabled: true}},
		Body:            `{"ok":true}`,
		Truncated:       true,
	}
	if err := store.AppendWithMCPProjection(entry, &projection); err != nil {
		t.Fatalf("AppendWithMCPProjection: %v", err)
	}

	// §7 names the location: a sibling directory of the log, one file per entry
	// id. Asserted as a path rather than through the accessor, because the
	// location is part of the contract with the tests that read it off disk.
	path := filepath.Join(dir, MCPProjectionDir, "history-1")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("no artifact at %s: %v", path, err)
	}
	var onDisk MCPProjection
	if err := json.Unmarshal(raw, &onDisk); err != nil {
		t.Fatalf("the artifact is not readable JSON: %v", err)
	}
	// Stamped by the store, not by the caller: a caller that forgot would
	// otherwise write a version-0 artifact that reads back as unusable.
	if onDisk.Version != MCPProjectionVersion || onDisk.EntryID != "history-1" {
		t.Errorf("artifact is not stamped: version=%d entryId=%q", onDisk.Version, onDisk.EntryID)
	}

	got, ok := store.MCPProjection("history-1")
	if !ok {
		t.Fatal("the projection did not come back")
	}
	if got.URL != projection.URL || got.Body != projection.Body || !got.Truncated {
		t.Errorf("round trip changed the projection: %+v", got)
	}

	// The entry itself is untouched by any of this.
	entries, err := store.List(HistoryQuery{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 1 || entries[0].URL != "https://api.test/things" {
		t.Errorf("the entry was altered by the projection: %+v", entries)
	}
}

// Three different ways of having no usable projection, one answer. The caller's
// only question is "may I serve this", and a maybe would become a fallback to
// the unmasked entry.
func TestMCPProjectionRefusesWhatItCannotVouchFor(t *testing.T) {
	store, dir := projectionStore(t)

	if _, ok := store.MCPProjection("never-recorded"); ok {
		t.Error("a missing projection reported as usable")
	}

	if err := store.AppendWithMCPProjection(HistoryEntry{ID: "corrupt"}, &MCPProjection{Body: "fine"}); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, MCPProjectionDir, "corrupt"), []byte("{not json"), 0o600); err != nil {
		t.Fatalf("corrupt: %v", err)
	}
	if _, ok := store.MCPProjection("corrupt"); ok {
		t.Error("an unparseable projection reported as usable")
	}

	// A version this build does not know was written under a redaction policy
	// this build cannot describe, so its fields mean nothing here.
	future := MCPProjection{Version: MCPProjectionVersion + 1, EntryID: "future", Body: "from a later build"}
	encoded, err := json.Marshal(future)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, MCPProjectionDir, "future"), encoded, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, ok := store.MCPProjection("future"); ok {
		t.Error("a projection from a future version reported as usable")
	}
}

// An entry id read back off a corrupted history line must not be able to
// address anything outside the projection directory.
func TestMCPProjectionRefusesIdsThatEscapeTheDirectory(t *testing.T) {
	store, dir := projectionStore(t)
	for _, id := range []string{"", "  ", ".", "..", "../escape", "sub/entry", `sub\entry`, ".hidden"} {
		if _, ok := store.projectionPathLocked(id); ok {
			t.Errorf("id %q was accepted as a projection filename", id)
		}
	}
	// And a store with no projection directory writes nothing at all rather
	// than dropping a file into the working directory.
	bare := &Store{path: filepath.Join(dir, "bare.jsonl")}
	if _, ok := bare.projectionPathLocked("history-1"); ok {
		t.Error("a store with no projection directory produced a path")
	}
	if err := bare.AppendWithMCPProjection(HistoryEntry{ID: "history-1"}, &MCPProjection{Body: "x"}); err == nil {
		t.Error("a store with no projection directory reported a projection written")
	}
	if entries, err := bare.List(HistoryQuery{}); err != nil || len(entries) != 1 {
		t.Errorf("the entry was lost because its projection could not be written: %v / %v", entries, err)
	}
}

// Compaction drops entries; the artifacts of the dropped ones have to go with
// them. Otherwise the directory grows by one file per send forever while
// history itself stays capped, and nothing ever reads or removes them again.
func TestMCPProjectionsArePrunedWithTheirEntries(t *testing.T) {
	store, dir := projectionStore(t)

	// Only four entries get an artifact: two at the front, which compaction
	// will drop, and two at the back, which it will keep. Projecting all of
	// them would be a thousand fsyncs to prove the same thing.
	total := CompactAt + 1
	projected := map[int]bool{0: true, 1: true, total - 2: true, total - 1: true}
	for i := 0; i < total; i++ {
		id := "entry-" + strconv.Itoa(i)
		entry := HistoryEntry{ID: id, Method: "GET"}
		var err error
		if projected[i] {
			err = store.AppendWithMCPProjection(entry, &MCPProjection{Body: id})
		} else {
			err = store.Append(entry)
		}
		if err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}

	files, err := os.ReadDir(filepath.Join(dir, MCPProjectionDir))
	if err != nil {
		t.Fatalf("read the projection directory: %v", err)
	}
	if len(files) != 2 {
		t.Errorf("%d artifacts remain, want the 2 whose entries survived compaction", len(files))
	}
	// The prune kept the same end of the log compaction kept.
	for _, id := range []string{"entry-" + strconv.Itoa(total-1), "entry-" + strconv.Itoa(total-2)} {
		if _, ok := store.MCPProjection(id); !ok {
			t.Errorf("%s's projection was pruned while its entry survived", id)
		}
	}
	for _, id := range []string{"entry-0", "entry-1"} {
		if _, ok := store.MCPProjection(id); ok {
			t.Errorf("%s was compacted away but kept its projection", id)
		}
	}
}

// Clearing history has to clear the agent-facing copies. A user who cleared
// their history to be rid of a run must not leave it readable through MCP.
func TestClearRemovesProjections(t *testing.T) {
	store, dir := projectionStore(t)
	if err := store.AppendWithMCPProjection(HistoryEntry{ID: "history-1"}, &MCPProjection{Body: "secretish"}); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := store.Clear(); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, MCPProjectionDir)); !os.IsNotExist(err) {
		t.Errorf("the projection directory survived Clear (stat error: %v)", err)
	}
	if _, ok := store.MCPProjection("history-1"); ok {
		t.Error("a cleared projection is still readable")
	}
}

// The artifact is owner-only on both the file and the directory. It holds a
// redacted copy, but "redacted" is best-effort and the log beside it is not
// world-readable either.
func TestProjectionArtifactsAreOwnerOnly(t *testing.T) {
	store, dir := projectionStore(t)
	if err := store.AppendWithMCPProjection(HistoryEntry{ID: "history-1"}, &MCPProjection{Body: "x"}); err != nil {
		t.Fatalf("append: %v", err)
	}
	directory, err := os.Stat(filepath.Join(dir, MCPProjectionDir))
	if err != nil {
		t.Fatalf("stat directory: %v", err)
	}
	if mode := directory.Mode().Perm(); mode != 0o700 {
		t.Errorf("projection directory mode is %o, want 700", mode)
	}
	file, err := os.Stat(filepath.Join(dir, MCPProjectionDir, "history-1"))
	if err != nil {
		t.Fatalf("stat artifact: %v", err)
	}
	if mode := file.Mode().Perm(); mode != 0o600 {
		t.Errorf("projection file mode is %o, want 600", mode)
	}
}

// A projection write that fails must not cost the user their history entry.
func TestAppendKeepsTheEntryWhenTheProjectionCannotBeWritten(t *testing.T) {
	store, dir := projectionStore(t)
	// A FILE where the projection directory goes: MkdirAll cannot create it.
	if err := os.WriteFile(filepath.Join(dir, MCPProjectionDir), []byte("in the way"), 0o600); err != nil {
		t.Fatalf("plant the blocker: %v", err)
	}
	err := store.AppendWithMCPProjection(HistoryEntry{ID: "history-1", Method: "GET"}, &MCPProjection{Body: "x"})
	if err == nil {
		t.Error("a failed projection write was reported as success")
	}
	entries, listErr := store.List(HistoryQuery{})
	if listErr != nil {
		t.Fatalf("List: %v", listErr)
	}
	if len(entries) != 1 || entries[0].ID != "history-1" {
		t.Errorf("the entry was dropped because its projection failed: %+v", entries)
	}
	if _, ok := store.MCPProjection("history-1"); ok {
		t.Error("an unwritten projection reported as usable")
	}
	if !strings.Contains(err.Error(), MCPProjectionDir) {
		t.Errorf("the error does not name what failed: %v", err)
	}
}
