package core

// Saving a request must not report it as saved when the disk write failed.
//
// The failure is produced by making the collection directory unwritable, which
// is the closest a test gets to the conditions this matters under — a full
// disk, a revoked permission, a network volume that went away.

import (
	"os"
	"testing"
)

// makeCollectionDirReadOnly makes writes inside dir fail for the rest of the
// test. Skipped when the process can write anyway (running as root).
func makeCollectionDirReadOnly(t *testing.T, dir string) {
	t.Helper()
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat %s: %v", dir, err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("chmod %s: %v", dir, err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, info.Mode().Perm()) })
	probe, err := os.CreateTemp(dir, "writable-probe-")
	if err == nil {
		name := probe.Name()
		_ = probe.Close()
		_ = os.Remove(name)
		t.Skip("the collection directory is still writable; this test needs an unprivileged process")
	}
}

func draftItemForSaveFailureTest(t *testing.T, app *App, collectionID, itemID string) RequestItem {
	t.Helper()
	url := "https://example.test/save-failure"
	state, err := app.UpdateRequest(collectionID, itemID, RequestPatch{URL: &url})
	if err != nil {
		t.Fatalf("UpdateRequest: %v", err)
	}
	item, ok := findItemInState(state, collectionID, itemID)
	if !ok {
		t.Fatalf("request %s was not found after the edit", itemID)
	}
	if !item.Draft {
		t.Fatal("an edited request is expected to be a draft before the save")
	}
	return item
}

func TestSaveRequestKeepsDraftWhenTheWriteFails(t *testing.T) {
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
	before := draftItemForSaveFailureTest(t, app, collection.ID, itemID)

	makeCollectionDirReadOnly(t, collection.Path)

	if _, err := app.SaveRequest(collection.ID, itemID); err == nil {
		t.Fatal("SaveRequest reported success against an unwritable collection directory")
	}

	after, ok := findItemInState(app.state, collection.ID, itemID)
	if !ok {
		t.Fatal("the request disappeared from state after the failed save")
	}
	if !after.Draft {
		t.Fatal("the failed save left the request marked saved: its edits are only in memory, the unsaved marker is gone, and the watcher is free to overwrite them from the stale file")
	}
	if !after.UpdatedAt.Equal(before.UpdatedAt) {
		t.Fatalf("the failed save advanced UpdatedAt: %s -> %s", before.UpdatedAt, after.UpdatedAt)
	}
	if after.Transient != before.Transient {
		t.Fatalf("the failed save changed Transient: %v -> %v", before.Transient, after.Transient)
	}
}

func TestSaveAllTabsKeepsDraftsOfTheCollectionThatFailed(t *testing.T) {
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
	before := draftItemForSaveFailureTest(t, app, collection.ID, itemID)
	if _, err := app.OpenRequestTab(collection.ID, itemID); err != nil {
		t.Fatalf("OpenRequestTab: %v", err)
	}

	makeCollectionDirReadOnly(t, collection.Path)

	if _, err := app.SaveAllTabs(collection.ID); err == nil {
		t.Fatal("SaveAllTabs reported success against an unwritable collection directory")
	}

	after, ok := findItemInState(app.state, collection.ID, itemID)
	if !ok {
		t.Fatal("the request disappeared from state after the failed save")
	}
	if !after.Draft {
		t.Fatal("the failed save-all left the request marked saved with nothing on disk")
	}
	if !after.UpdatedAt.Equal(before.UpdatedAt) {
		t.Fatalf("the failed save-all advanced UpdatedAt: %s -> %s", before.UpdatedAt, after.UpdatedAt)
	}
}
