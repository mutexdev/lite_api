package core

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDiscardRequestDraftRestoresSavedRequest(t *testing.T) {
	app, collection, first, _, _ := recoveryTestCollection(t)
	originalURL := first.URL
	editedURL := "https://example.test/edited"
	state, err := app.UpdateRequest(collection.ID, first.ID, RequestPatch{URL: &editedURL})
	if err != nil {
		t.Fatal(err)
	}
	edited, ok := findItemInState(state, collection.ID, first.ID)
	if !ok || !edited.Draft || edited.URL != editedURL {
		t.Fatalf("request was not marked as an edited draft: %#v", edited)
	}
	drafts, err := app.ListUnsavedDrafts()
	if err != nil || len(drafts) != 1 || drafts[0].ItemID != first.ID {
		t.Fatalf("unsaved draft was not listed: %#v err=%v", drafts, err)
	}

	state, err = app.DiscardRequestDraft(collection.ID, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	restored, ok := findItemInState(state, collection.ID, first.ID)
	if !ok || restored.Draft || restored.URL != originalURL {
		t.Fatalf("discard did not restore the saved request: %#v", restored)
	}
}

func TestDiscardUnsavedScratchRequestDistinguishesSavedAndNeverSaved(t *testing.T) {
	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	scratch := findScratchCollectionForTest(t, state)

	state, err = app.CreateRequest(scratch.ID, "http", "Never saved")
	if err != nil {
		t.Fatal(err)
	}
	neverSaved := findRequestByNameForTest(t, state, scratch.ID, "Never saved")
	neverSavedURL := "https://example.test/never-saved"
	if _, err := app.UpdateRequest(scratch.ID, neverSaved.ID, RequestPatch{URL: &neverSavedURL}); err != nil {
		t.Fatal(err)
	}
	state, err = app.DiscardRequestDraft(scratch.ID, neverSaved.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := findItemInState(state, scratch.ID, neverSaved.ID); ok {
		t.Fatal("discard should remove a never-saved scratch request")
	}
	for _, tab := range state.OpenTabs {
		if tab.CollectionID == scratch.ID && tab.ItemID == neverSaved.ID {
			t.Fatalf("discard left the never-saved scratch tab open: %#v", tab)
		}
	}

	state, err = app.CreateRequest(scratch.ID, "http", "Saved scratch")
	if err != nil {
		t.Fatal(err)
	}
	saved := findRequestByNameForTest(t, state, scratch.ID, "Saved scratch")
	savedURL := "https://example.test/saved-scratch"
	state, err = app.UpdateRequest(scratch.ID, saved.ID, RequestPatch{URL: &savedURL})
	if err != nil {
		t.Fatal(err)
	}
	state, err = app.SaveRequest(scratch.ID, saved.ID)
	if err != nil {
		t.Fatal(err)
	}
	saved = findRequestByNameForTest(t, state, scratch.ID, "Saved scratch")
	if _, err := os.Stat(saved.FilePath); err != nil {
		t.Fatalf("saved scratch request is not file-backed: %v", err)
	}
	editedURL := "https://example.test/temporary-edit"
	if _, err := app.UpdateRequest(scratch.ID, saved.ID, RequestPatch{URL: &editedURL}); err != nil {
		t.Fatal(err)
	}
	state, err = app.DiscardRequestDraft(scratch.ID, saved.ID)
	if err != nil {
		t.Fatal(err)
	}
	reverted, ok := findItemInState(state, scratch.ID, saved.ID)
	if !ok || reverted.Draft || reverted.URL != savedURL {
		t.Fatalf("saved scratch draft should revert, not disappear: %#v", reverted)
	}
}

func TestNewNormalRequestIsTransientDraftUntilSaveOrRename(t *testing.T) {
	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]

	state, err = app.CreateRequest(collection.ID, "http", "Discard me")
	if err != nil {
		t.Fatal(err)
	}
	discarded := findRequestByNameForTest(t, state, collection.ID, "Discard me")
	if !discarded.Draft || !discarded.Transient || discarded.FilePath == "" {
		t.Fatalf("new normal request must be an addressable transient draft: %#v", discarded)
	}
	if _, err := os.Stat(discarded.FilePath); !os.IsNotExist(err) {
		t.Fatalf("new request should not claim a saved file before commit: %v", err)
	}
	state, err = app.DiscardRequestDraft(collection.ID, discarded.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := findItemInState(state, collection.ID, discarded.ID); ok {
		t.Fatal("discard should remove a never-saved normal request")
	}

	state, err = app.CreateRequest(collection.ID, "http", "New Request")
	if err != nil {
		t.Fatal(err)
	}
	created := findRequestByNameForTest(t, state, collection.ID, "New Request")
	oldPath := created.FilePath
	state, err = app.RenameRequest(collection.ID, created.ID, "Recovery Probe", "Recovery Probe")
	if err != nil {
		t.Fatalf("rename should commit a never-saved request: %v", err)
	}
	renamed, ok := findItemInState(state, collection.ID, created.ID)
	if !ok || renamed.Name != "Recovery Probe" || renamed.Draft || renamed.Transient {
		t.Fatalf("rename did not commit the transient request in place: ok=%v item=%#v", ok, renamed)
	}
	if state.ActiveTabID != collection.ID+":"+created.ID {
		t.Fatalf("rename changed the request tab identity: %q", state.ActiveTabID)
	}
	if filepath.Clean(renamed.FilePath) == filepath.Clean(oldPath) {
		t.Fatalf("rename did not update the intended file path: old=%q new=%q", oldPath, renamed.FilePath)
	}
	if _, err := os.Stat(renamed.FilePath); err != nil {
		t.Fatalf("renamed request was not committed to disk: %v", err)
	}
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Fatalf("never-saved source path should remain absent: %v", err)
	}

	state, err = app.CreateRequest(collection.ID, "http", "Save me")
	if err != nil {
		t.Fatal(err)
	}
	saved := findRequestByNameForTest(t, state, collection.ID, "Save me")
	state, err = app.SaveRequest(collection.ID, saved.ID)
	if err != nil {
		t.Fatal(err)
	}
	saved, ok = findItemInState(state, collection.ID, saved.ID)
	if !ok || saved.Draft || saved.Transient {
		t.Fatalf("save did not commit the normal transient request: ok=%v item=%#v", ok, saved)
	}
	if _, err := os.Stat(saved.FilePath); err != nil {
		t.Fatalf("saved normal request is not file-backed: %v", err)
	}
}

func TestDiscardUnsavedDraftPersistFailureRollsBackMemoryAndTabs(t *testing.T) {
	dataDir := t.TempDir()
	app := newAppInDirForTest(t, dataDir)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	scratch := findScratchCollectionForTest(t, state)
	state, err = app.CreateRequest(scratch.ID, "http", "Keep on failure")
	if err != nil {
		t.Fatal(err)
	}
	draft := findRequestByNameForTest(t, state, scratch.ID, "Keep on failure")
	draftURL := "https://example.test/keep-on-failure"
	state, err = app.UpdateRequest(scratch.ID, draft.ID, RequestPatch{URL: &draftURL})
	if err != nil {
		t.Fatal(err)
	}
	originalActiveTabID := state.ActiveTabID

	statePath := filepath.Join(dataDir, "state.json")
	if err := os.Remove(statePath); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(statePath, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := app.DiscardRequestDraft(scratch.ID, draft.ID); err == nil {
		t.Fatal("expected discard persistence failure")
	}

	drafts, err := app.ListUnsavedDrafts()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, candidate := range drafts {
		found = found || candidate.CollectionID == scratch.ID && candidate.ItemID == draft.ID
	}
	if !found {
		t.Fatalf("failed discard lost the unsaved draft in memory: %#v", drafts)
	}
	app.mu.Lock()
	defer app.mu.Unlock()
	if app.state.ActiveTabID != originalActiveTabID {
		t.Fatalf("failed discard changed active tab: got %q want %q", app.state.ActiveTabID, originalActiveTabID)
	}
	tabFound := false
	for _, tab := range app.state.OpenTabs {
		tabFound = tabFound || tab.CollectionID == scratch.ID && tab.ItemID == draft.ID
	}
	if !tabFound {
		t.Fatal("failed discard removed the draft tab")
	}
}

func findScratchCollectionForTest(t *testing.T, state AppState) Collection {
	t.Helper()
	for _, workspace := range state.Workspaces {
		for _, collection := range workspace.Collections {
			if collection.Scratch {
				return collection
			}
		}
	}
	t.Fatal("scratch collection not found")
	return Collection{}
}

func findRequestByNameForTest(t *testing.T, state AppState, collectionID, name string) RequestItem {
	t.Helper()
	for _, workspace := range state.Workspaces {
		for _, collection := range workspace.Collections {
			if collection.ID != collectionID {
				continue
			}
			for _, item := range collection.Items {
				if item.Name == name {
					return item
				}
			}
		}
	}
	t.Fatalf("request %q not found", name)
	return RequestItem{}
}
