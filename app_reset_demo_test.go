package main

import (
	"os"
	"strings"
	"testing"
)

// ResetDemoData is the most destructive thing bound to the frontend: it
// replaces the entire application state with a fresh default and rewrites the
// default collection's files. It was at 0%.
//
// Two things about it are worth holding down. It must actually reset — a
// partial reset leaves the user with a state they cannot get out of by
// repeating the action. And it must REFUSE from a scoped workspace window,
// because such a window owns one workspace and resetting from there would
// discard the workspaces it cannot see, on behalf of a user who was only
// looking at one of them.

func TestResetDemoDataReplacesTheWorkspaceContents(t *testing.T) {
	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	workspaceID := state.Workspaces[0].ID

	if _, err = app.CreateCollection(workspaceID, "Mine", "bru"); err != nil {
		t.Fatal(err)
	}
	if _, err = app.CreateWorkspace("Second workspace"); err != nil {
		t.Fatal(err)
	}
	before, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	if len(before.Workspaces) < 2 {
		t.Fatalf("precondition: expected a second workspace, got %d", len(before.Workspaces))
	}

	after, err := app.ResetDemoData()
	if err != nil {
		t.Fatal(err)
	}

	if len(after.Workspaces) != 1 {
		t.Errorf("after the reset there are %d workspaces, want the single default one", len(after.Workspaces))
	}
	for _, collection := range after.Workspaces[0].Collections {
		if collection.Name == "Mine" {
			t.Error("a collection created before the reset survived it")
		}
	}
	// The returned state and the stored state must agree, or the UI would
	// render a reset the backend has not actually performed.
	stored, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	if len(stored.Workspaces) != len(after.Workspaces) {
		t.Errorf("stored state has %d workspaces but the reset returned %d",
			len(stored.Workspaces), len(after.Workspaces))
	}
}

// Resetting twice must land in the same place. A reset that is not idempotent
// is one the user cannot rely on to get back to a known state, which is the
// only reason the button exists.
func TestResetDemoDataIsIdempotent(t *testing.T) {
	app := newAppForTest(t)
	if _, err := app.GetState(); err != nil {
		t.Fatal(err)
	}

	first, err := app.ResetDemoData()
	if err != nil {
		t.Fatal(err)
	}
	second, err := app.ResetDemoData()
	if err != nil {
		t.Fatal(err)
	}

	if len(first.Workspaces) != len(second.Workspaces) {
		t.Fatalf("workspace count differs between resets: %d then %d",
			len(first.Workspaces), len(second.Workspaces))
	}
	firstNames := collectionNames(first.Workspaces[0].Collections)
	secondNames := collectionNames(second.Workspaces[0].Collections)
	if !sameOrder(firstNames, secondNames) {
		t.Errorf("collections differ between resets: %v then %v", firstNames, secondNames)
	}
}

// The reset must reach DISK, not just memory — both the default collection's
// files and the persisted state. A reset that only replaced the in-memory state
// would be undone by the next launch, and the user would find their "reset"
// app holding everything they thought they had cleared.
//
// This test distinguishes the two by creating something the default state does
// not contain. An earlier version compared only collection NAMES against a
// freshly reset app, which the default state reproduces anyway — so neither
// skipping the file rewrite nor skipping the persist failed it.
func TestResetDemoDataReachesDiskAndSurvivesARestart(t *testing.T) {
	dir := t.TempDir()
	app := newAppInDirForTest(t, dir)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	workspaceID := state.Workspaces[0].ID

	if _, err = app.CreateCollection(workspaceID, "Made before the reset", "bru"); err != nil {
		t.Fatal(err)
	}
	// A request written into the DEFAULT collection, so the file rewrite has
	// something of its own to undo.
	defaultCollectionID := state.Workspaces[0].Collections[0].ID
	if _, err = app.CreateRequest(defaultCollectionID, "http", "Request before the reset"); err != nil {
		t.Fatal(err)
	}
	flushPersistForTest(t, app)

	if _, err := app.ResetDemoData(); err != nil {
		t.Fatal(err)
	}
	flushPersistForTest(t, app)

	// A fresh App over the same directory reads whatever is actually on disk.
	restarted := newAppInDirForTest(t, dir)
	reloaded, err := restarted.GetState()
	if err != nil {
		t.Fatal(err)
	}

	if len(reloaded.Workspaces) != 1 {
		t.Errorf("after a restart there are %d workspaces, want the single default one", len(reloaded.Workspaces))
	}
	for _, collection := range reloaded.Workspaces[0].Collections {
		if collection.Name == "Made before the reset" {
			t.Error("a collection created before the reset came back after a restart, so the reset was never persisted")
		}
		for _, item := range collection.Items {
			if item.Name == "Request before the reset" {
				t.Error("a request created before the reset came back after a restart, so its file was never rewritten")
			}
		}
	}
}

// Cached OAuth2 tokens are cleared. They are credentials keyed to the state
// being discarded, so keeping them would leave tokens for collections that no
// longer exist — and the next collection to reuse one of those keys would
// silently inherit somebody else's token.
func TestResetDemoDataClearsCachedOAuth2Tokens(t *testing.T) {
	app := newAppForTest(t)
	if _, err := app.GetState(); err != nil {
		t.Fatal(err)
	}

	app.oauth2Mu.Lock()
	app.oauth2 = map[string]oauth2TokenResponse{"some-key": {AccessToken: "stale-token"}}
	app.oauth2Mu.Unlock()

	if _, err := app.ResetDemoData(); err != nil {
		t.Fatal(err)
	}

	app.oauth2Mu.Lock()
	remaining := len(app.oauth2)
	app.oauth2Mu.Unlock()
	if remaining != 0 {
		t.Errorf("%d cached OAuth2 token(s) survived the reset", remaining)
	}
}

// THE REFUSAL. A scoped workspace window owns one workspace; resetting from
// there would discard every workspace it cannot see. It must decline, and it
// must leave the state exactly as it found it rather than half-resetting.
func TestResetDemoDataRefusesFromAScopedWorkspaceWindow(t *testing.T) {
	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	workspaceID := state.Workspaces[0].ID
	if _, err = app.CreateCollection(workspaceID, "Keep me", "bru"); err != nil {
		t.Fatal(err)
	}
	before, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	beforeNames := collectionNames(before.Workspaces[0].Collections)

	app.mu.Lock()
	app.workspaceRuntime = &workspaceWindowRuntime{}
	app.mu.Unlock()

	_, resetErr := app.ResetDemoData()

	// The scope is cleared BEFORE the state is read back. GetState refuses
	// while a scoped runtime is attached, so inspecting through it here would
	// report the scope check rather than anything about the reset.
	app.mu.Lock()
	app.workspaceRuntime = nil
	app.mu.Unlock()

	if resetErr == nil {
		t.Fatal("a scoped workspace window was allowed to reset all data")
	}

	after, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	if !sameOrder(collectionNames(after.Workspaces[0].Collections), beforeNames) {
		t.Errorf("the refused reset changed the collections: %v, want %v",
			collectionNames(after.Workspaces[0].Collections), beforeNames)
	}
	if len(after.Workspaces) != len(before.Workspaces) {
		t.Errorf("the refused reset changed the workspace count from %d to %d",
			len(before.Workspaces), len(after.Workspaces))
	}
}

// The file rewrite is a REPAIR, not a rewrite, and the distinction is the whole
// observable behaviour of that step.
//
// writeFreshDefaultCollectionFilesLocked skips any collection whose directory
// already exists, so on an ordinary reset — where it does — that call does
// nothing at all. Removing it entirely was invisible to every other test here,
// and correctly so.
//
// What it is for is the case where the directory is GONE: a user who deleted
// the default collection's folder from disk gets it back by resetting. Without
// this step the reset would leave state pointing at a directory that does not
// exist, and every request in it would fail to load.
func TestResetDemoDataRecreatesADeletedDefaultCollectionDirectory(t *testing.T) {
	dir := t.TempDir()
	app := newAppInDirForTest(t, dir)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}

	var collectionPath string
	for _, collection := range state.Workspaces[0].Collections {
		if !collection.Scratch && strings.TrimSpace(collection.Path) != "" {
			collectionPath = collection.Path
			break
		}
	}
	if collectionPath == "" {
		t.Fatal("the default workspace has no ordinary collection with a path")
	}
	if _, err := os.Stat(collectionPath); err != nil {
		t.Fatalf("the default collection directory should exist to begin with: %v", err)
	}

	if err := os.RemoveAll(collectionPath); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(collectionPath); !os.IsNotExist(err) {
		t.Fatalf("the directory was not removed: %v", err)
	}

	if _, err := app.ResetDemoData(); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(collectionPath)
	if err != nil {
		t.Fatalf("the reset did not recreate the deleted collection directory: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("%s came back as something other than a directory", collectionPath)
	}
	entries, err := os.ReadDir(collectionPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Error("the collection directory was recreated but left empty, so it holds no requests")
	}
}
