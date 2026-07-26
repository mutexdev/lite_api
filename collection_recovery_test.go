package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestDeleteRequestRecoverableRestoresBytesTabsExamplesAndRestart(t *testing.T) {
	app, collection, first, requestPath, dataDir := recoveryTestCollection(t)
	state, err := app.OpenRequestTab(collection.ID, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	state, err = app.CreateResponseExample(collection.ID, first.ID, "Recovery example", "restored")
	if err != nil {
		t.Fatal(err)
	}
	state, err = app.OpenRequestTab(collection.ID, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(requestPath)
	if err != nil {
		t.Fatal(err)
	}

	deleted, err := app.DeleteRequestRecoverable(collection.ID, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(requestPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("request should be removed, err=%v", err)
	}
	for _, tab := range deleted.State.OpenTabs {
		if tab.CollectionID == collection.ID && tab.ItemID == first.ID {
			t.Fatalf("recoverable delete left dependent tab open: %#v", tab)
		}
	}
	entryDir, err := recoveryEntryDir(dataDir, deleted.Entry.WorkspaceID, deleted.Entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	snapshotPath, err := recoverySnapshotPath(dataDir, deleted.Entry.WorkspaceID, deleted.Entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath, err := recoveryManifestPath(dataDir, deleted.Entry.WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	assertMode(t, recoveryRoot(dataDir), 0o700)
	assertMode(t, entryDir, 0o700)
	assertMode(t, snapshotPath, 0o600)
	assertMode(t, manifestPath, 0o600)
	stateBytes, err := os.ReadFile(filepath.Join(dataDir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(stateBytes, []byte(deleted.Entry.ID)) {
		t.Fatal("recovery metadata must not be embedded in state.json")
	}

	// A new App instance proves the manifest and payload survive restart.
	restarted := newAppInDirForTest(t, dataDir)
	entries, err := restarted.ListRecoveryEntries()
	if err != nil || len(entries) != 1 || entries[0].ID != deleted.Entry.ID {
		t.Fatalf("durable recovery entry missing after restart: entries=%#v err=%v", entries, err)
	}
	state, err = restarted.RestoreRecoveryEntry(deleted.Entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	if leftovers, err := filepath.Glob(filepath.Join(dataDir, ".recovery-*")); err != nil || len(leftovers) != 0 {
		t.Fatalf("restore left plaintext recovery staging under data dir: %v, err=%v", leftovers, err)
	}
	after, err := os.ReadFile(requestPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("request bytes changed after restore:\nwant %s\n got %s", before, after)
	}
	restored, ok := findItemInState(state, collection.ID, first.ID)
	if !ok || len(restored.Examples) != 1 || restored.Examples[0].Name != "Recovery example" {
		t.Fatalf("request/example state was not restored: %#v", restored)
	}
	foundExample := false
	for _, tab := range state.OpenTabs {
		if tab.CollectionID == collection.ID && tab.ItemID == first.ID && tab.Kind == "response-example" && tab.ResponseTab == "examples" {
			foundExample = true
		}
	}
	if !foundExample {
		t.Fatalf("response-example pane state was not restored: %#v", state.OpenTabs)
	}
	if entries, err := restarted.ListRecoveryEntries(); err != nil || entries == nil || len(entries) != 0 {
		t.Fatalf("restored entry should be consumed as a non-nil empty list: %#v err=%v", entries, err)
	}
}

func TestFreshDefaultRequestIsFileBackedAndRecoverable(t *testing.T) {
	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	var collection Collection
	for _, candidate := range state.Workspaces[0].Collections {
		if !candidate.Scratch {
			collection = candidate
			break
		}
	}
	if collection.ID == "" || len(collection.Items) == 0 {
		t.Fatalf("default collection/request missing: %#v", state.Workspaces[0].Collections)
	}
	request := collection.Items[0]
	requestPath, err := collectionRequestFilesystemPath(&collection, request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(requestPath); err != nil {
		t.Fatalf("default request SAVED state is not file-backed: %v", err)
	}
	deleted, err := app.DeleteRequestRecoverable(collection.ID, request.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.RestoreRecoveryEntry(deleted.Entry.ID); err != nil {
		t.Fatal(err)
	}
}

func TestDeleteRequestRecoverableRestoresExactTabOrderAndActiveTab(t *testing.T) {
	app, collection, first, _, _ := recoveryTestCollection(t)
	state, err := app.OpenRequestTab(collection.ID, collection.Items[1].ID)
	if err != nil {
		t.Fatal(err)
	}
	state, err = app.OpenRequestTab(collection.ID, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	beforeTabs := append([]OpenTab(nil), state.OpenTabs...)
	beforeActiveTabID := state.ActiveTabID

	deleted, err := app.DeleteRequestRecoverable(collection.ID, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := app.RestoreRecoveryEntry(deleted.Entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(restored.OpenTabs, beforeTabs) {
		t.Fatalf("restore changed tab order:\nwant %#v\n got %#v", beforeTabs, restored.OpenTabs)
	}
	if restored.ActiveTabID != beforeActiveTabID {
		t.Fatalf("restore active tab = %q, want %q", restored.ActiveTabID, beforeActiveTabID)
	}
}

func TestRecoveryRestoreRefusesNewerDraftStateAndRetainsEntry(t *testing.T) {
	app, collection, first, _, _ := recoveryTestCollection(t)
	deleted, err := app.DeleteRequestRecoverable(collection.ID, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	newerURL := "https://example.test/newer-draft"
	_, err = app.UpdateRequest(collection.ID, collection.Items[1].ID, RequestPatch{URL: &newerURL})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.RestoreRecoveryEntry(deleted.Entry.ID); err == nil {
		t.Fatal("expected newer collection state to block restore")
	} else {
		var conflict *RestoreConflictError
		if !errors.As(err, &conflict) {
			t.Fatalf("expected typed conflict, got %T: %v", err, err)
		}
	}
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	newer, ok := findItemInState(state, collection.ID, collection.Items[1].ID)
	if !ok || newer.URL != newerURL || !newer.Draft {
		t.Fatalf("newer draft state was overwritten: %#v", newer)
	}
	entries, err := app.ListRecoveryEntries()
	if err != nil || len(entries) != 1 || entries[0].ID != deleted.Entry.ID {
		t.Fatalf("conflict must preserve recovery entry: %#v err=%v", entries, err)
	}
}

func TestDeleteFolderRecoverableRestoresWholeTreeAndOrdering(t *testing.T) {
	root := t.TempDir()
	collectionPath := filepath.Join(root, "Folder recovery")
	if err := os.MkdirAll(filepath.Join(collectionPath, "users"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeRecoveryFile(t, filepath.Join(collectionPath, "bruno.json"), `{"version":"1","name":"Folder recovery","type":"collection"}`)
	writeRecoveryFile(t, filepath.Join(collectionPath, "users", "folder.bru"), "meta {\n  name: Users\n  seq: 1\n}\n")
	writeRecoveryFile(t, filepath.Join(collectionPath, "users", "list.bru"), recoveryBru("List", "https://example.test/list", 1))
	writeRecoveryFile(t, filepath.Join(collectionPath, "root.bru"), recoveryBru("Root", "https://example.test/root", 2))
	before, err := os.ReadFile(filepath.Join(collectionPath, "users", "list.bru"))
	if err != nil {
		t.Fatal(err)
	}
	app := newAppInDirForTest(t, filepath.Join(root, "app"))
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	state, err = app.OpenCollection(state.ActiveWorkspaceID, collectionPath)
	if err != nil {
		t.Fatal(err)
	}
	collection := findTestCollectionByPath(state, collectionPath)
	deleted, err := app.DeleteFolderRecoverable(collection.ID, "Users")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(collectionPath, "users")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("folder should be deleted, err=%v", err)
	}
	if _, err := app.RestoreRecoveryEntry(deleted.Entry.ID); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(filepath.Join(collectionPath, "users", "list.bru"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("folder request bytes changed after restore\nwant %s\n got %s", before, after)
	}
	restored, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection = findTestCollectionByPath(restored, collectionPath)
	if len(collection.Items) != 2 || collection.Items[0].Name != "List" || collection.Items[0].Seq != 1 {
		t.Fatalf("folder request/order was not restored: %#v", collection.Items)
	}
}

func TestRecoveryRestoreRefusesCollisionAndDiscardExpires(t *testing.T) {
	app, collection, first, requestPath, _ := recoveryTestCollection(t)
	deleted, err := app.DeleteRequestRecoverable(collection.ID, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	newer := []byte("newer user content")
	if err := os.WriteFile(requestPath, newer, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := app.RestoreRecoveryEntry(deleted.Entry.ID); err == nil {
		t.Fatal("expected restore conflict")
	} else {
		var conflict *RestoreConflictError
		if !errors.As(err, &conflict) {
			t.Fatalf("expected typed conflict, got %T: %v", err, err)
		}
	}
	content, err := os.ReadFile(requestPath)
	if err != nil || !bytes.Equal(content, newer) {
		t.Fatalf("conflicting restore overwrote newer file: %q err=%v", content, err)
	}
	if ok, err := app.DiscardRecoveryEntry(deleted.Entry.ID); err != nil || !ok {
		t.Fatalf("discard failed: ok=%v err=%v", ok, err)
	}
	if _, err := app.RestoreRecoveryEntry(deleted.Entry.ID); err == nil {
		t.Fatal("discarded entry should not restore")
	}

	entry := newRecoveryEntry(recoveryKindRequest, "expired", "workspace", "collection")
	entry.ExpiresAt = time.Now().UTC().Add(-time.Second)
	if err := stageRecoverySnapshot(t.TempDir(), recoverySnapshot{Entry: entry}, "", false); err != nil {
		t.Fatal(err)
	}
}

func TestRecoveryConflictPreservesEntryAndPersistFailureRollsBackRestore(t *testing.T) {
	app, collection, first, requestPath, dataDir := recoveryTestCollection(t)
	deleted, err := app.DeleteRequestRecoverable(collection.ID, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(requestPath, []byte("newer"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := app.RestoreRecoveryEntry(deleted.Entry.ID); err == nil {
		t.Fatal("expected conflict")
	}
	entries, err := app.ListRecoveryEntries()
	if err != nil || len(entries) != 1 || entries[0].ID != deleted.Entry.ID {
		t.Fatalf("conflict must retain restore entry: %#v err=%v", entries, err)
	}
	if err := os.Remove(requestPath); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(dataDir, "state.json")
	if err := os.Remove(statePath); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(statePath, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := app.RestoreRecoveryEntry(deleted.Entry.ID); err == nil {
		t.Fatal("expected persist failure")
	}
	if _, err := os.Stat(requestPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed restore must roll disk back to post-delete state, err=%v", err)
	}
	state, err := app.GetState()
	if err == nil {
		if _, ok := findItemInState(state, collection.ID, first.ID); ok {
			t.Fatal("failed restore must roll in-memory state back to post-delete state")
		}
	}
}

func TestRecoveryStagingFailureDoesNotMutateCollection(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "collection")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(source, "request.bru")
	before := []byte("original")
	if err := os.WriteFile(path, before, 0o600); err != nil {
		t.Fatal(err)
	}
	badDataDir := filepath.Join(root, "not-a-directory")
	if err := os.WriteFile(badDataDir, []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}
	entry := newRecoveryEntry(recoveryKindRequest, "request", "workspace", "collection")
	if err := stageRecoverySnapshot(badDataDir, recoverySnapshot{Entry: entry}, source, true); err == nil {
		t.Fatal("expected staging failure")
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("staging failure mutated original: %q err=%v", after, err)
	}
}

func TestRemoveCollectionRecoverableRestoresManagedGitIgnoreWithoutTouchingFiles(t *testing.T) {
	app, collection, _, requestPath, _ := recoveryTestCollection(t)
	app.mu.Lock()
	for wi := range app.state.Workspaces {
		for ci := range app.state.Workspaces[wi].Collections {
			if app.state.Workspaces[wi].Collections[ci].ID == collection.ID {
				app.state.Workspaces[wi].Collections[ci].Remote = "git@example.test:org/recovery.git"
			}
		}
	}
	workspacePath := app.state.Workspaces[0].Path
	app.mu.Unlock()
	if err := os.MkdirAll(workspacePath, 0o755); err != nil {
		t.Fatal(err)
	}
	gitIgnore := filepath.Join(workspacePath, ".gitignore")
	beforeIgnore := []byte("*.local\n# user content\n")
	if err := os.WriteFile(gitIgnore, beforeIgnore, 0o600); err != nil {
		t.Fatal(err)
	}
	beforeRequest, err := os.ReadFile(requestPath)
	if err != nil {
		t.Fatal(err)
	}
	removed, err := app.RemoveCollectionRecoverable(collection.ID)
	if err != nil {
		t.Fatal(err)
	}
	if findTestCollectionByPath(removed.State, collection.Path).ID != "" {
		t.Fatal("collection should be absent after removal")
	}
	if _, err := app.RestoreRecoveryEntry(removed.Entry.ID); err != nil {
		t.Fatal(err)
	}
	afterRequest, err := os.ReadFile(requestPath)
	if err != nil || !bytes.Equal(beforeRequest, afterRequest) {
		t.Fatalf("collection removal touched request file: %q err=%v", afterRequest, err)
	}
	afterIgnore, err := os.ReadFile(gitIgnore)
	if err != nil || !bytes.Equal(beforeIgnore, afterIgnore) {
		t.Fatalf("git ignore did not restore exactly: %q err=%v", afterIgnore, err)
	}
}

func TestCollectionRecoveryIgnoresPersistedGitIgnorePathAuthority(t *testing.T) {
	app, collection, _, _, dataDir := recoveryTestCollection(t)
	app.mu.Lock()
	workspacePath := app.state.Workspaces[0].Path
	app.mu.Unlock()
	if err := os.MkdirAll(workspacePath, 0o755); err != nil {
		t.Fatal(err)
	}
	gitIgnore := filepath.Join(workspacePath, ".gitignore")
	wantIgnore := []byte("# workspace-owned\n")
	if err := os.WriteFile(gitIgnore, wantIgnore, 0o600); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.txt")
	wantOutside := []byte("outside-must-not-change")
	if err := os.WriteFile(outside, wantOutside, 0o600); err != nil {
		t.Fatal(err)
	}
	removed, err := app.RemoveCollectionRecoverable(collection.ID)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := readRecoverySnapshot(dataDir, removed.Entry.WorkspaceID, removed.Entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	snapshot.GitIgnorePath = outside
	if err := writeRecoverySnapshot(dataDir, snapshot); err != nil {
		t.Fatal(err)
	}
	if _, err := app.RestoreRecoveryEntry(removed.Entry.ID); err != nil {
		t.Fatal(err)
	}
	gotOutside, err := os.ReadFile(outside)
	if err != nil || !bytes.Equal(gotOutside, wantOutside) {
		t.Fatalf("persisted path modified outside file: %q err=%v", gotOutside, err)
	}
	gotIgnore, err := os.ReadFile(gitIgnore)
	if err != nil || !bytes.Equal(gotIgnore, wantIgnore) {
		t.Fatalf("derived workspace .gitignore was not restored: %q err=%v", gotIgnore, err)
	}
}

func TestCollectionRecoveryRejectsCurrentWorkspaceMismatch(t *testing.T) {
	app, collection, _, _, _ := recoveryTestCollection(t)
	removed, err := app.RemoveCollectionRecoverable(collection.ID)
	if err != nil {
		t.Fatal(err)
	}
	otherRoot := t.TempDir()
	app.mu.Lock()
	app.state.Workspaces = append(app.state.Workspaces, Workspace{ID: "different-workspace", Name: "Different", Path: otherRoot})
	app.state.ActiveWorkspaceID = "different-workspace"
	app.mu.Unlock()
	if _, err := app.RestoreRecoveryEntry(removed.Entry.ID); err == nil {
		t.Fatal("recovery entry was restored from a different active workspace")
	}
	if _, err := os.Stat(filepath.Join(otherRoot, ".gitignore")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("mismatched workspace was modified: %v", err)
	}
}

func TestRecoveryGitIgnoreSymlinkNeverModifiesTarget(t *testing.T) {
	workspace := Workspace{ID: "workspace", Path: t.TempDir()}
	target := filepath.Join(t.TempDir(), "target.txt")
	wantTarget := []byte("symlink-target-must-not-change")
	if err := os.WriteFile(target, wantTarget, 0o600); err != nil {
		t.Fatal(err)
	}
	gitIgnore := filepath.Join(workspace.Path, ".gitignore")
	if err := os.Symlink(target, gitIgnore); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := recoveryGitIgnoreSnapshot(workspace); err == nil {
		t.Fatal("snapshot followed a .gitignore symlink")
	}
	if err := restoreGitIgnore(workspace, true, []byte("attacker-controlled")); err == nil {
		t.Fatal("restore wrote through a .gitignore symlink")
	}
	gotTarget, err := os.ReadFile(target)
	if err != nil || !bytes.Equal(gotTarget, wantTarget) {
		t.Fatalf("restore modified symlink target: %q err=%v", gotTarget, err)
	}
	// The rollback/remove case may unlink the direct child, but must not follow
	// it to the target.
	if err := restoreGitIgnore(workspace, false, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(gitIgnore); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("rollback did not unlink .gitignore symlink: %v", err)
	}
	gotTarget, err = os.ReadFile(target)
	if err != nil || !bytes.Equal(gotTarget, wantTarget) {
		t.Fatalf("rollback modified symlink target: %q err=%v", gotTarget, err)
	}
}

func TestRemoveCollectionRecoverablePostStageGitIgnoreSwapCannotEscapeWorkspace(t *testing.T) {
	app, collection, _, _, _ := recoveryTestCollection(t)
	app.mu.Lock()
	workspacePath := app.state.Workspaces[0].Path
	for wi := range app.state.Workspaces {
		for ci := range app.state.Workspaces[wi].Collections {
			if app.state.Workspaces[wi].Collections[ci].ID == collection.ID {
				app.state.Workspaces[wi].Collections[ci].Remote = "git@example.test:org/recovery.git"
			}
		}
	}
	app.mu.Unlock()
	gitIgnore := filepath.Join(workspacePath, ".gitignore")
	if err := os.WriteFile(gitIgnore, []byte("# user-owned line\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "outside-target.txt")
	wantTarget := []byte("outside-target-must-not-change")
	if err := os.WriteFile(target, wantTarget, 0o600); err != nil {
		t.Fatal(err)
	}
	oldHook := managedGitIgnoreBeforeCommit
	var hookErr error
	var once sync.Once
	managedGitIgnoreBeforeCommit = func() {
		once.Do(func() {
			if err := os.Remove(gitIgnore); err != nil {
				hookErr = err
				return
			}
			hookErr = os.Symlink(target, gitIgnore)
		})
	}
	removed, err := app.RemoveCollectionRecoverable(collection.ID)
	managedGitIgnoreBeforeCommit = oldHook
	if hookErr != nil {
		t.Fatal(hookErr)
	}
	if err != nil {
		t.Fatal(err)
	}
	if findTestCollectionByPath(removed.State, collection.Path).ID != "" {
		t.Fatal("collection should be removed after safe managed .gitignore commit")
	}
	gotTarget, err := os.ReadFile(target)
	if err != nil || !bytes.Equal(gotTarget, wantTarget) {
		t.Fatalf("public removal wrote through swapped symlink: %q err=%v", gotTarget, err)
	}
	info, err := os.Lstat(gitIgnore)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		t.Fatalf("managed writer did not atomically replace swapped link: %v", info.Mode())
	}
}

func TestManagedGitIgnoreRejectsUserSymlinkWorkspaceComponent(t *testing.T) {
	realWorkspace := t.TempDir()
	parent := t.TempDir()
	alias := filepath.Join(parent, "workspace-alias")
	if err := os.Symlink(realWorkspace, alias); err != nil {
		t.Fatal(err)
	}
	collectionPath := filepath.Join(alias, "collection")
	if err := updateManagedGitIgnore(alias, collectionPath, true); err == nil {
		t.Fatal("managed writer followed a user-controlled workspace symlink")
	}
	if _, err := os.Stat(filepath.Join(realWorkspace, ".gitignore")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("symlink target workspace was modified: %v", err)
	}
}

func TestManagedGitIgnoreCanonicalizesTrustedLeadingPlatformAlias(t *testing.T) {
	workspacePath := t.TempDir()
	collectionPath := filepath.Join(workspacePath, "Git Collection")
	if err := os.MkdirAll(collectionPath, 0o755); err != nil {
		t.Fatal(err)
	}
	workspaceCanonical, err := canonicalizeTrustedLeadingPath(workspacePath)
	if err != nil {
		t.Fatal(err)
	}
	collectionCanonical, err := canonicalizeTrustedLeadingPath(collectionPath)
	if err != nil {
		t.Fatal(err)
	}
	if !pathInside(workspaceCanonical, collectionCanonical) {
		t.Fatalf("canonical trusted-alias paths lost containment: workspace=%q collection=%q", workspaceCanonical, collectionCanonical)
	}
	if err := updateManagedGitIgnore(workspacePath, collectionPath, true); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(workspacePath, ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "/Git Collection") {
		t.Fatalf("managed ignore missing canonical collection entry: %q", data)
	}
}

func recoveryTestCollection(t *testing.T) (*App, Collection, RequestItem, string, string) {
	t.Helper()
	root := t.TempDir()
	collectionPath := filepath.Join(root, "Recovery collection")
	if err := os.MkdirAll(collectionPath, 0o755); err != nil {
		t.Fatal(err)
	}
	writeRecoveryFile(t, filepath.Join(collectionPath, "bruno.json"), `{"version":"1","name":"Recovery collection","type":"collection"}`)
	requestPath := filepath.Join(collectionPath, "first.bru")
	writeRecoveryFile(t, requestPath, recoveryBru("First", "https://example.test/first", 1))
	writeRecoveryFile(t, filepath.Join(collectionPath, "second.bru"), recoveryBru("Second", "https://example.test/second", 2))
	dataDir := filepath.Join(root, "app")
	app := newAppInDirForTest(t, dataDir)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	state, err = app.OpenCollection(state.ActiveWorkspaceID, collectionPath)
	if err != nil {
		t.Fatal(err)
	}
	collection := findTestCollectionByPath(state, collectionPath)
	return app, collection, collection.Items[0], requestPath, dataDir
}

func recoveryBru(name, url string, seq int) string {
	return "meta {\n  name: " + name + "\n  type: http\n  seq: " + string(rune('0'+seq)) + "\n}\n\nget {\n  url: " + url + "\n  body: none\n  auth: none\n}\n"
}

func writeRecoveryFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("%s permissions = %o, want %o", path, got, want)
	}
}

func TestRecoveryManifestExpiredEntryIsPruned(t *testing.T) {
	dataDir := t.TempDir()
	entry := newRecoveryEntry(recoveryKindRequest, "expired", "workspace", "collection")
	entry.ExpiresAt = time.Now().UTC().Add(-time.Second)
	if err := stageRecoverySnapshot(dataDir, recoverySnapshot{Entry: entry}, "", false); err != nil {
		t.Fatal(err)
	}
	entries, err := removeExpiredRecoveryEntries(dataDir, entry.WorkspaceID, time.Now().UTC())
	if err != nil || len(entries) != 0 {
		t.Fatalf("expired entry should be pruned: %#v err=%v", entries, err)
	}
	entryDir, err := recoveryEntryDir(dataDir, entry.WorkspaceID, entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(entryDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expired entry payload should be removed, err=%v", err)
	}
}

func TestStagedRecoveryEntryIsHiddenUntilCommitted(t *testing.T) {
	dataDir := t.TempDir()
	entry := newRecoveryEntry(recoveryKindRequest, "pending", "workspace", "collection")
	if entry.Restorable {
		t.Fatal("new recovery entry should be staged, not restorable")
	}
	if err := stageRecoverySnapshot(dataDir, recoverySnapshot{Entry: entry}, "", false); err != nil {
		t.Fatal(err)
	}
	entries, err := removeExpiredRecoveryEntries(dataDir, entry.WorkspaceID, time.Now().UTC())
	if err != nil || len(entries) != 0 {
		t.Fatalf("staged entry must not be exposed: %#v err=%v", entries, err)
	}
	if _, err := findRecoveryEntry(dataDir, entry.WorkspaceID, entry.ID); err != nil {
		t.Fatalf("staged payload should remain available for repair: %v", err)
	}
}

func TestExpiredCleanupRetainsNonexpiredStagedEntry(t *testing.T) {
	dataDir := t.TempDir()
	staged := newRecoveryEntry(recoveryKindRequest, "pending", "workspace", "collection")
	expired := newRecoveryEntry(recoveryKindRequest, "expired", "workspace", "collection")
	expired.ExpiresAt = time.Now().UTC().Add(-time.Second)
	if err := stageRecoverySnapshot(dataDir, recoverySnapshot{Entry: staged}, "", false); err != nil {
		t.Fatal(err)
	}
	if err := stageRecoverySnapshot(dataDir, recoverySnapshot{Entry: expired}, "", false); err != nil {
		t.Fatal(err)
	}
	entries, err := removeExpiredRecoveryEntries(dataDir, staged.WorkspaceID, time.Now().UTC())
	if err != nil || len(entries) != 0 {
		t.Fatalf("staged entry must stay hidden: %#v err=%v", entries, err)
	}
	if _, err := findRecoveryEntry(dataDir, staged.WorkspaceID, staged.ID); err != nil {
		t.Fatalf("expired cleanup removed nonexpired staged entry: %v", err)
	}
	if _, err := findRecoveryEntry(dataDir, expired.WorkspaceID, expired.ID); err == nil {
		t.Fatal("expired entry remained in manifest")
	}
}

func TestRecoveryErrorTextIsReadable(t *testing.T) {
	err := (&RestoreConflictError{EntryID: "abc", Reason: "collection files changed"}).Error()
	if !strings.Contains(err, "cannot be restored safely") || !strings.Contains(err, "collection files changed") {
		t.Fatalf("unexpected conflict text: %q", err)
	}
}
