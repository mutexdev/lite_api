package main

import (
	"testing"
)

// Undoing a collection delete must put the collection back WHERE IT WAS.
//
// restoreCollectionRemovalLocked carries the clamp-and-shift that decides this,
// and it was covered only incidentally: replacing the whole computation with
// `index := len(ws.Collections)` — always append — left the ENTIRE package main
// suite green. So did removing the negative-index clamp. The sidebar order is
// the user's own arrangement, and an undo that quietly reorders it looks like
// they misremembered rather than like a bug.
//
// This exercises the public path a user takes: remove, then restore.

func workspaceCollectionNames(t *testing.T, state AppState) []string {
	t.Helper()
	names := make([]string, 0, len(state.Workspaces[0].Collections))
	for _, collection := range state.Workspaces[0].Collections {
		names = append(names, collection.Name)
	}
	return names
}

func sameOrder(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Restoring must reproduce the order exactly, and the middle position is the
// one that distinguishes "put it back" from "append it".
func TestRestoringARemovedCollectionKeepsTheSidebarOrder(t *testing.T) {
	for _, target := range []string{"Alpha", "Beta", "Gamma"} {
		app := newAppForTest(t)
		state, err := app.GetState()
		if err != nil {
			t.Fatal(err)
		}
		workspaceID := state.Workspaces[0].ID
		for _, name := range []string{"Alpha", "Beta", "Gamma"} {
			if state, err = app.CreateCollection(workspaceID, name, "bru"); err != nil {
				t.Fatal(err)
			}
		}
		before := workspaceCollectionNames(t, state)

		var targetID string
		for _, collection := range state.Workspaces[0].Collections {
			if collection.Name == target {
				targetID = collection.ID
			}
		}
		if targetID == "" {
			t.Fatalf("could not find the collection named %q among %v", target, before)
		}

		removed, err := app.RemoveCollectionRecoverable(targetID)
		if err != nil {
			t.Fatalf("%s: %v", target, err)
		}
		afterRemoval := workspaceCollectionNames(t, removed.State)
		if len(afterRemoval) != len(before)-1 {
			t.Fatalf("%s: removal left %v, want one fewer than %v", target, afterRemoval, before)
		}

		restoredState, err := app.RestoreRecoveryEntry(removed.Entry.ID)
		if err != nil {
			t.Fatalf("%s: restore: %v", target, err)
		}
		after := workspaceCollectionNames(t, restoredState)
		if !sameOrder(after, before) {
			t.Errorf("restoring %q gave the order %v, want %v", target, after, before)
		}
	}
}

// The same property must hold after a restart, because the index travels in the
// on-disk snapshot rather than in memory. A restore performed by a fresh App is
// the case where a wrong or missing index would show up.
func TestRestoringARemovedCollectionKeepsTheOrderAcrossARestart(t *testing.T) {
	dir := t.TempDir()
	app := newAppInDirForTest(t, dir)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	workspaceID := state.Workspaces[0].ID
	for _, name := range []string{"Alpha", "Beta", "Gamma"} {
		if state, err = app.CreateCollection(workspaceID, name, "bru"); err != nil {
			t.Fatal(err)
		}
	}
	before := workspaceCollectionNames(t, state)

	var betaID string
	for _, collection := range state.Workspaces[0].Collections {
		if collection.Name == "Beta" {
			betaID = collection.ID
		}
	}
	removed, err := app.RemoveCollectionRecoverable(betaID)
	if err != nil {
		t.Fatal(err)
	}

	restarted := newAppInDirForTest(t, dir)
	restoredState, err := restarted.RestoreRecoveryEntry(removed.Entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after := workspaceCollectionNames(t, restoredState); !sameOrder(after, before) {
		t.Errorf("after a restart the order came back as %v, want %v", after, before)
	}
}

// Removing the LAST collection and restoring it exercises the boundary where
// the insertion index equals the length — the case an off-by-one in the shift
// would corrupt or panic on.
func TestRestoringTheLastCollectionDoesNotDisturbTheRest(t *testing.T) {
	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	workspaceID := state.Workspaces[0].ID
	for _, name := range []string{"Alpha", "Omega"} {
		if state, err = app.CreateCollection(workspaceID, name, "bru"); err != nil {
			t.Fatal(err)
		}
	}
	before := workspaceCollectionNames(t, state)
	last := state.Workspaces[0].Collections[len(state.Workspaces[0].Collections)-1]

	removed, err := app.RemoveCollectionRecoverable(last.ID)
	if err != nil {
		t.Fatal(err)
	}
	restoredState, err := app.RestoreRecoveryEntry(removed.Entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after := workspaceCollectionNames(t, restoredState); !sameOrder(after, before) {
		t.Errorf("order = %v, want %v", after, before)
	}
}

// A snapshot carrying an OUT-OF-RANGE index must not take the app down. The
// index is written by whatever build performed the delete, so a snapshot left
// by an older or buggier one can name a position this workspace does not have —
// and a recovery entry survives restarts, so it can outlive the build that
// wrote it.
//
// Without the clamp this is not a wrong position but a PANIC: at index -1,
// ws.Collections[index+1:] is fine while ws.Collections[index:] is out of
// range, and the process dies during the user's undo.
//
// Snapshots are encrypted at rest, so the corrupted index is produced through
// the writer rather than by editing bytes — which is also what an older build
// would have done.
func TestRestoringToleratesAnOutOfRangeIndexInTheSnapshot(t *testing.T) {
	for _, corruptIndex := range []int{-1, -99, 42} {
		dir := t.TempDir()
		app := newAppInDirForTest(t, dir)
		state, err := app.GetState()
		if err != nil {
			t.Fatal(err)
		}
		workspaceID := state.Workspaces[0].ID
		for _, name := range []string{"Alpha", "Beta"} {
			if state, err = app.CreateCollection(workspaceID, name, "bru"); err != nil {
				t.Fatal(err)
			}
		}
		expected := len(state.Workspaces[0].Collections)

		var betaID string
		for _, collection := range state.Workspaces[0].Collections {
			if collection.Name == "Beta" {
				betaID = collection.ID
			}
		}
		removed, err := app.RemoveCollectionRecoverable(betaID)
		if err != nil {
			t.Fatal(err)
		}

		snapshot, err := readRecoverySnapshot(dir, removed.Entry.WorkspaceID, removed.Entry.ID)
		if err != nil {
			t.Fatal(err)
		}
		snapshot.CollectionIndex = corruptIndex
		if err := writeRecoverySnapshot(dir, snapshot); err != nil {
			t.Fatal(err)
		}

		restoredState, err := app.RestoreRecoveryEntry(removed.Entry.ID)
		if err != nil {
			t.Fatalf("index %d: restore failed: %v", corruptIndex, err)
		}
		names := workspaceCollectionNames(t, restoredState)
		if len(names) != expected {
			t.Errorf("index %d: collections = %v, want %d of them", corruptIndex, names, expected)
		}
		found := false
		for _, name := range names {
			if name == "Beta" {
				found = true
			}
		}
		if !found {
			t.Errorf("index %d: the restored collection is missing from %v", corruptIndex, names)
		}
	}
}
