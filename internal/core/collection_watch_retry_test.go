package core

// A refresh the watcher declined to do must still be pending afterwards.
//
// When a collection had unsaved drafts the watcher recorded the new
// fingerprint anyway. That is the record of "this is the version we have
// loaded", so the next poll compared equal, concluded nothing had changed, and
// never came back to it — the external edit was lost for good, not deferred:
// not when the draft was saved, not after a restart.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func dirtyWatcherCollectionFixture(t *testing.T) (app *App, collection Collection, requestPath string) {
	t.Helper()
	root := t.TempDir()
	collectionPath := filepath.Join(root, "Retry Watcher")
	requestPath = filepath.Join(collectionPath, "ping.bru")
	if err := os.MkdirAll(collectionPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(collectionPath, "bruno.json"), []byte(`{"version":"1","name":"Retry Watcher","type":"collection"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	writeWatcherRequestFile(t, requestPath, "Ping", "https://example.test/ping", 1)

	app = newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	state, err = app.OpenCollection(state.Workspaces[0].ID, collectionPath)
	if err != nil {
		t.Fatal(err)
	}
	collection = state.Workspaces[0].Collections[len(state.Workspaces[0].Collections)-1]
	return app, collection, requestPath
}

// clearDraftFlagsForTest resolves the drafts without writing to disk, which is
// what a save would do — the point of the test is that the external edit is
// still on disk and still unread.
func clearDraftFlagsForTest(t *testing.T, app *App, collectionID string) {
	t.Helper()
	app.mu.Lock()
	defer app.mu.Unlock()
	collection, err := app.findCollectionLocked(collectionID)
	if err != nil {
		t.Fatalf("findCollectionLocked: %v", err)
	}
	for index := range collection.Items {
		collection.Items[index].Draft = false
	}
}

func TestRefreshChangedCollectionsRetriesADirtySkip(t *testing.T) {
	app, collection, requestPath := dirtyWatcherCollectionFixture(t)

	draftURL := "https://example.test/local-draft"
	if _, err := app.UpdateRequest(collection.ID, collection.Items[0].ID, RequestPatch{URL: &draftURL}); err != nil {
		t.Fatal(err)
	}
	writeWatcherRequestFile(t, requestPath, "Disk Pong", "https://example.test/disk-pong", 1)

	result, err := app.RefreshChangedCollections()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(result.SkippedDirty, ",") != "Retry Watcher" {
		t.Fatalf("expected the dirty collection to be skipped, got %#v", result)
	}

	clearDraftFlagsForTest(t, app, collection.ID)

	result, err = app.RefreshChangedCollections()
	if err != nil {
		t.Fatal(err)
	}
	if !result.Changed || strings.Join(result.Refreshed, ",") != "Retry Watcher" {
		t.Fatalf("the skipped refresh was never retried, so the external edit is lost for good: %#v", result)
	}
	refreshed := collectionInState(t, result.State, collection.ID)
	if len(refreshed.Items) != 1 || refreshed.Items[0].URL != "https://example.test/disk-pong" {
		t.Fatalf("the retried refresh did not pick up the file on disk: %#v", refreshed.Items)
	}
}

func TestRefreshChangedCollectionsNotifiesOnRefreshErrors(t *testing.T) {
	app, collection, _ := dirtyWatcherCollectionFixture(t)

	// A collection directory the process can no longer read is the "Errors"
	// case. The watcher reported it only in the result struct, which the poller
	// discards, so the collection quietly stopped tracking its files.
	info, err := os.Stat(collection.Path)
	if err != nil {
		t.Fatalf("stat collection: %v", err)
	}
	if err := os.Chmod(collection.Path, 0o000); err != nil {
		t.Fatalf("chmod collection: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(collection.Path, info.Mode().Perm()) })
	if entries, err := os.ReadDir(collection.Path); err == nil {
		t.Skipf("the collection directory is still readable (%d entries); this test needs an unprivileged process", len(entries))
	}

	result, err := app.RefreshChangedCollections()
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Errors) == 0 {
		t.Fatalf("expected the unreadable collection to be reported, got %#v", result.Errors)
	}
	if !hasNotificationContaining(result.State.Notifications, "error", "Could not refresh") {
		t.Fatalf("a collection the watcher could not read raised no error notification: %#v", result.State.Notifications)
	}

	// Polling again must not raise a second copy of the same message.
	before := len(result.State.Notifications)
	again, err := app.RefreshChangedCollections()
	if err != nil {
		t.Fatal(err)
	}
	if len(again.State.Notifications) != before {
		t.Fatalf("the poller raised the same notification twice: %d -> %d", before, len(again.State.Notifications))
	}
}

func hasNotificationContaining(notifications []Notification, level, substring string) bool {
	for _, notification := range notifications {
		if notification.Level == level && strings.Contains(notification.Message, substring) {
			return true
		}
	}
	return false
}
