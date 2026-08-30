package core

// Collection files are replaced by rename, not rewritten in place.
//
// state.json has been written through internal/atomicfile since US-003, for the
// reason stated there: a process killed mid-write leaves a truncated file, and
// a truncated file read at the next startup is the whole document gone. A
// collection's request, config and environment files carry exactly the same
// risk and were still going out through os.WriteFile, which opens the real file
// with O_TRUNC and writes into it — the window where the user's request is an
// empty or half-written file is real, and it is their request that is lost, not
// a cache.
//
// os.SameFile is what distinguishes the two mechanisms: an in-place write keeps
// the same file, and a rename-into-place replaces it with a different one. That
// is the atomic property itself, not a proxy for it.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCollectionFileWritesReplaceTheFileAtomically(t *testing.T) {
	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	collection := state.Workspaces[0].Collections[0]
	if collection.Path == "" {
		t.Skip("default collection is not filesystem-backed in this fixture")
	}
	itemID := collection.Items[0].ID

	firstURL := "https://example.test/atomic-one"
	if _, err := app.UpdateRequest(collection.ID, itemID, RequestPatch{URL: &firstURL}); err != nil {
		t.Fatalf("UpdateRequest: %v", err)
	}
	if _, err := app.SaveRequest(collection.ID, itemID); err != nil {
		t.Fatalf("SaveRequest: %v", err)
	}
	item, ok := findItemInState(app.state, collection.ID, itemID)
	if !ok || strings.TrimSpace(item.FilePath) == "" {
		t.Fatalf("saved request has no file path: ok=%v item=%#v", ok, item)
	}
	before, err := os.Stat(item.FilePath)
	if err != nil {
		t.Fatalf("stat %s: %v", item.FilePath, err)
	}

	secondURL := "https://example.test/atomic-two"
	if _, err := app.UpdateRequest(collection.ID, itemID, RequestPatch{URL: &secondURL}); err != nil {
		t.Fatalf("UpdateRequest: %v", err)
	}
	if _, err := app.SaveRequest(collection.ID, itemID); err != nil {
		t.Fatalf("SaveRequest: %v", err)
	}

	after, err := os.Stat(item.FilePath)
	if err != nil {
		t.Fatalf("stat %s after the second save: %v", item.FilePath, err)
	}
	if os.SameFile(before, after) {
		t.Fatal("the request file was rewritten in place: a crash mid-write leaves the user's request truncated or empty")
	}

	data, err := os.ReadFile(item.FilePath)
	if err != nil {
		t.Fatalf("read %s: %v", item.FilePath, err)
	}
	if !strings.Contains(string(data), secondURL) {
		t.Fatalf("the atomic write did not land the new content:\n%s", data)
	}

	// No temporary file may be left behind: the collection reader walks this
	// directory, and a stray .bru.tmp-* would be read as a request.
	entries, err := os.ReadDir(filepath.Dir(item.FilePath))
	if err != nil {
		t.Fatalf("read collection dir: %v", err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), ".tmp-") {
			t.Fatalf("the atomic write left a temporary file behind: %s", entry.Name())
		}
	}
}
