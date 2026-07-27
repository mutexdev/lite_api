package main

import (
	"testing"
	"time"
)

// These two run when an undo FAILS PART-WAY: the app has already begun undoing
// a delete or an overwrite, something went wrong, and the rollback puts the
// state back. Both were at 0%.
//
// That makes them the last line before data loss, and they are reached only on
// an error path — precisely the code least likely to have been exercised by
// hand. A rollback that restores a collection to the wrong position, or drops
// it entirely, turns a recoverable failure into a permanent one.
//
// Snapshots here use recoveryKindCollection so the filesystem branch of
// rollbackCollectionRecoveryLocked is skipped; what is under test is the
// in-memory state restoration, which is the part that decides what the user
// still has.

func rollbackApp(t *testing.T, collectionNames ...string) (*App, string) {
	t.Helper()
	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	workspaceID := state.Workspaces[0].ID

	app.mu.Lock()
	ws := &app.state.Workspaces[0]
	ws.Collections = nil
	for _, name := range collectionNames {
		ws.Collections = append(ws.Collections, Collection{ID: "col-" + name, Name: name})
	}
	app.mu.Unlock()
	return app, workspaceID
}

func collectionNamesOf(t *testing.T, app *App) []string {
	t.Helper()
	app.mu.Lock()
	defer app.mu.Unlock()
	names := make([]string, 0, len(app.state.Workspaces[0].Collections))
	for _, collection := range app.state.Workspaces[0].Collections {
		names = append(names, collection.Name)
	}
	return names
}

func assertNames(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("collections = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("collections = %v, want %v", got, want)
		}
	}
}

func removalSnapshot(workspaceID, name string, index int) recoverySnapshot {
	return recoverySnapshot{
		Entry: RecoveryEntry{
			Kind:         recoveryKindCollection,
			WorkspaceID:  workspaceID,
			CollectionID: "col-" + name,
		},
		CollectionIndex: index,
		Collection:      Collection{ID: "col-" + name, Name: name},
	}
}

// A deleted collection comes back WHERE IT WAS. Restoring it to the end instead
// silently reorders the user's sidebar as a side effect of an undo, and nothing
// would tell them the order changed rather than that they had misremembered it.
func TestRollbackCollectionRemovalRestoresTheOriginalPosition(t *testing.T) {
	for _, index := range []int{0, 1, 2} {
		app, workspaceID := rollbackApp(t, "alpha", "beta", "gamma")
		app.mu.Lock()
		ws := &app.state.Workspaces[0]
		removed := ws.Collections[index].Name
		ws.Collections = append(ws.Collections[:index:index], ws.Collections[index+1:]...)
		app.rollbackCollectionRemovalLocked(removalSnapshot(workspaceID, removed, index))
		app.mu.Unlock()

		assertNames(t, collectionNamesOf(t, app), []string{"alpha", "beta", "gamma"})
	}
}

// The recorded index can be stale — the workspace may have lost collections
// between the snapshot and the rollback. Out of range must append rather than
// panic, because a panic here loses the collection entirely.
func TestRollbackCollectionRemovalClampsAnOutOfRangeIndex(t *testing.T) {
	for _, index := range []int{-1, -99, 5, 1000} {
		app, workspaceID := rollbackApp(t, "alpha", "beta")
		app.mu.Lock()
		app.rollbackCollectionRemovalLocked(removalSnapshot(workspaceID, "restored", index))
		app.mu.Unlock()

		got := collectionNamesOf(t, app)
		if len(got) != 3 {
			t.Fatalf("index %d: collections = %v, want three", index, got)
		}
		if got[2] != "restored" {
			t.Errorf("index %d: collections = %v, want the restored one appended", index, got)
		}
	}
}

// Rolling back twice must not duplicate the collection. A retry after a partial
// failure is the ordinary way to reach this, and two entries with the same id
// is a state nothing downstream expects.
func TestRollbackCollectionRemovalDoesNotDuplicateAnExistingCollection(t *testing.T) {
	app, workspaceID := rollbackApp(t, "alpha", "beta")
	snapshot := removalSnapshot(workspaceID, "beta", 1)

	app.mu.Lock()
	app.rollbackCollectionRemovalLocked(snapshot)
	app.rollbackCollectionRemovalLocked(snapshot)
	app.mu.Unlock()

	assertNames(t, collectionNamesOf(t, app), []string{"alpha", "beta"})
}

// The tab state travels with the rollback: an undo moves tabs around, so
// putting the collection back without putting the tabs back leaves the user
// looking at tabs for content that has just been replaced underneath them.
func TestRollbackCollectionRemovalRestoresTheTabs(t *testing.T) {
	app, workspaceID := rollbackApp(t, "alpha")
	snapshot := removalSnapshot(workspaceID, "beta", 1)
	snapshot.OpenTabs = []OpenTab{{ID: "tab-1"}, {ID: "tab-2"}}
	snapshot.ClosedTabs = []OpenTab{{ID: "tab-closed"}}
	snapshot.ActiveTabID = "tab-2"

	app.mu.Lock()
	app.state.OpenTabs = []OpenTab{{ID: "wrong"}}
	app.state.ClosedTabs = nil
	app.state.ActiveTabID = "wrong"
	app.rollbackCollectionRemovalLocked(snapshot)
	openTabs, closedTabs, active := app.state.OpenTabs, app.state.ClosedTabs, app.state.ActiveTabID
	app.mu.Unlock()

	if len(openTabs) != 2 || openTabs[0].ID != "tab-1" || openTabs[1].ID != "tab-2" {
		t.Errorf("open tabs = %v", openTabs)
	}
	if len(closedTabs) != 1 || closedTabs[0].ID != "tab-closed" {
		t.Errorf("closed tabs = %v", closedTabs)
	}
	if active != "tab-2" {
		t.Errorf("active tab = %q", active)
	}
}

// The tab slices must be COPIED out of the snapshot. Sharing the backing array
// would let a later tab edit write through into the stored snapshot, so a
// second rollback would restore the already-corrupted state.
func TestRollbackCollectionRemovalCopiesTheTabsOutOfTheSnapshot(t *testing.T) {
	app, workspaceID := rollbackApp(t, "alpha")
	snapshot := removalSnapshot(workspaceID, "beta", 1)
	snapshot.OpenTabs = []OpenTab{{ID: "tab-1"}}

	app.mu.Lock()
	app.rollbackCollectionRemovalLocked(snapshot)
	app.state.OpenTabs[0].ID = "edited later"
	app.mu.Unlock()

	if snapshot.OpenTabs[0].ID != "tab-1" {
		t.Errorf("the snapshot's tabs were written through: %q", snapshot.OpenTabs[0].ID)
	}
}

// The restored collection is a CLONE, so the snapshot stays usable for a
// retry — a rollback that handed out the snapshot's own value would let the
// live state and the stored snapshot drift into each other.
func TestRollbackCollectionRemovalRestoresACloneOfTheSnapshot(t *testing.T) {
	app, workspaceID := rollbackApp(t, "alpha")
	snapshot := removalSnapshot(workspaceID, "beta", 1)
	snapshot.Collection.Items = []RequestItem{{ID: "req-1", Name: "original"}}

	app.mu.Lock()
	app.rollbackCollectionRemovalLocked(snapshot)
	app.state.Workspaces[0].Collections[1].Items[0].Name = "edited later"
	app.mu.Unlock()

	if snapshot.Collection.Items[0].Name != "original" {
		t.Errorf("editing the restored collection reached back into the snapshot: %q",
			snapshot.Collection.Items[0].Name)
	}
}

func TestRollbackCollectionRemovalRestoresTheWorkspaceTimestamp(t *testing.T) {
	app, workspaceID := rollbackApp(t, "alpha")
	snapshot := removalSnapshot(workspaceID, "beta", 1)
	snapshot.WorkspaceUpdatedAt = time.Date(2020, 3, 4, 5, 6, 7, 0, time.UTC)

	app.mu.Lock()
	app.rollbackCollectionRemovalLocked(snapshot)
	got := app.state.Workspaces[0].UpdatedAt
	app.mu.Unlock()

	if !got.Equal(snapshot.WorkspaceUpdatedAt) {
		t.Errorf("UpdatedAt = %v, want %v", got, snapshot.WorkspaceUpdatedAt)
	}
}

// A snapshot naming a workspace that is gone must not take the rollback down,
// and must still restore the tabs — those live on the root state, not on the
// workspace, so there is no reason to lose them too.
func TestRollbackCollectionRemovalSurvivesAMissingWorkspace(t *testing.T) {
	app, _ := rollbackApp(t, "alpha")
	snapshot := removalSnapshot("ws-does-not-exist", "beta", 0)
	snapshot.OpenTabs = []OpenTab{{ID: "tab-1"}}
	snapshot.ActiveTabID = "tab-1"

	app.mu.Lock()
	app.rollbackCollectionRemovalLocked(snapshot)
	names := len(app.state.Workspaces[0].Collections)
	openTabs, active := app.state.OpenTabs, app.state.ActiveTabID
	app.mu.Unlock()

	if names != 1 {
		t.Errorf("a rollback for an unknown workspace changed the collections of a real one")
	}
	if len(openTabs) != 1 || openTabs[0].ID != "tab-1" || active != "tab-1" {
		t.Errorf("the tabs were not restored: %v / %q", openTabs, active)
	}
}

// The recovery rollback REPLACES a collection that is still present, rather
// than inserting one. It must not create a second entry when the id is absent —
// that is the removal rollback's job, and doing both here would resurrect a
// collection the user had deliberately deleted.
func TestRollbackCollectionRecoveryReplacesInPlaceAndInsertsNothing(t *testing.T) {
	app, workspaceID := rollbackApp(t, "alpha", "beta", "gamma")

	snapshot := removalSnapshot(workspaceID, "beta", 1)
	snapshot.Collection.Name = "beta restored"

	app.mu.Lock()
	app.rollbackCollectionRecoveryLocked(snapshot)
	app.mu.Unlock()
	assertNames(t, collectionNamesOf(t, app), []string{"alpha", "beta restored", "gamma"})

	// Now with an id the workspace does not hold: nothing may be added.
	absent := removalSnapshot(workspaceID, "delta", 1)
	app.mu.Lock()
	app.rollbackCollectionRecoveryLocked(absent)
	app.mu.Unlock()
	assertNames(t, collectionNamesOf(t, app), []string{"alpha", "beta restored", "gamma"})
}

func TestRollbackCollectionRecoveryRestoresTheTabs(t *testing.T) {
	app, workspaceID := rollbackApp(t, "alpha")
	snapshot := removalSnapshot(workspaceID, "alpha", 0)
	snapshot.OpenTabs = []OpenTab{{ID: "tab-1"}}
	snapshot.ClosedTabs = []OpenTab{{ID: "tab-closed"}}
	snapshot.ActiveTabID = "tab-1"

	app.mu.Lock()
	app.state.OpenTabs = []OpenTab{{ID: "wrong"}}
	app.state.ActiveTabID = "wrong"
	app.rollbackCollectionRecoveryLocked(snapshot)
	openTabs, closedTabs, active := app.state.OpenTabs, app.state.ClosedTabs, app.state.ActiveTabID
	app.mu.Unlock()

	if len(openTabs) != 1 || openTabs[0].ID != "tab-1" {
		t.Errorf("open tabs = %v", openTabs)
	}
	if len(closedTabs) != 1 || closedTabs[0].ID != "tab-closed" {
		t.Errorf("closed tabs = %v", closedTabs)
	}
	if active != "tab-1" {
		t.Errorf("active tab = %q", active)
	}
}
