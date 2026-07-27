package main

import (
	"os"
	"testing"
)

// SaveUnsavedDrafts is what the close/quit dialog calls when the user answers
// "Save" to the prompt about unsaved work, and it was at 0%.
//
// That places it at the exact moment the user is least able to notice a
// failure: the window is going away, and anything not written is gone. Its
// stated contract is all-or-nothing on VALIDATION — every reference is checked
// before any draft is written — so a stale entry in the list must stop the
// whole save rather than half-complete it.

func draftIn(t *testing.T, app *App, collectionID, name string) RequestItem {
	t.Helper()
	state, err := app.CreateRequest(collectionID, "http", name)
	if err != nil {
		t.Fatal(err)
	}
	item := findRequestByNameForTest(t, state, collectionID, name)
	if !item.Draft {
		t.Fatalf("a newly created request should be a draft: %#v", item)
	}
	return item
}

func itemFromState(t *testing.T, app *App, collectionID, itemID string) RequestItem {
	t.Helper()
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	item, ok := findItemInState(state, collectionID, itemID)
	if !ok {
		t.Fatalf("item %s is gone from collection %s", itemID, collectionID)
	}
	return item
}

func firstCollectionID(t *testing.T, app *App) string {
	t.Helper()
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	return state.Workspaces[0].Collections[0].ID
}

// Saving clears the draft flag AND writes the file. Clearing the flag without
// writing would tell the user their work was saved while leaving the disk
// untouched — the worst of the possible outcomes here.
func TestSaveUnsavedDraftsWritesTheFileAndClearsTheFlag(t *testing.T) {
	app := newAppForTest(t)
	collectionID := firstCollectionID(t, app)
	draft := draftIn(t, app, collectionID, "Save me")

	if _, err := os.Stat(draft.FilePath); !os.IsNotExist(err) {
		t.Fatalf("a never-saved draft should have no file yet: %v", err)
	}

	if _, err := app.SaveUnsavedDrafts([]UnsavedDraft{{CollectionID: collectionID, ItemID: draft.ID}}); err != nil {
		t.Fatal(err)
	}

	saved := itemFromState(t, app, collectionID, draft.ID)
	if saved.Draft {
		t.Error("the item is still marked as a draft after saving")
	}
	if saved.Transient {
		t.Error("a saved request in an ordinary collection is still marked transient")
	}
	if _, err := os.Stat(draft.FilePath); err != nil {
		t.Errorf("the draft was marked saved but its file does not exist: %v", err)
	}
}

// THE CONTRACT: validation covers the whole list before anything is written.
// A stale reference — a request deleted in another window between the dialog
// opening and the click — must stop the save, not save the good ones and
// report failure for the rest, which would leave the user with no idea which
// of their files made it.
func TestSaveUnsavedDraftsWritesNothingWhenAnyReferenceIsBad(t *testing.T) {
	app := newAppForTest(t)
	collectionID := firstCollectionID(t, app)
	good := draftIn(t, app, collectionID, "Good draft")

	cases := map[string][]UnsavedDraft{
		"unknown item": {
			{CollectionID: collectionID, ItemID: good.ID},
			{CollectionID: collectionID, ItemID: "req-does-not-exist"},
		},
		"unknown collection": {
			{CollectionID: collectionID, ItemID: good.ID},
			{CollectionID: "col-does-not-exist", ItemID: good.ID},
		},
	}
	for name, drafts := range cases {
		if _, err := app.SaveUnsavedDrafts(drafts); err == nil {
			t.Errorf("%s: the save was accepted", name)
		}
		still := itemFromState(t, app, collectionID, good.ID)
		if !still.Draft {
			t.Errorf("%s: the valid draft was saved anyway, so the save was not all-or-nothing", name)
		}
		if _, err := os.Stat(good.FilePath); !os.IsNotExist(err) {
			t.Errorf("%s: a file was written despite the refusal", name)
		}
	}
}

// The close dialog builds its list from the UI, which can offer the same
// request twice — once per open tab. Saving it twice must not be an error, and
// must not write it twice.
func TestSaveUnsavedDraftsToleratesTheSameDraftListedTwice(t *testing.T) {
	app := newAppForTest(t)
	collectionID := firstCollectionID(t, app)
	draft := draftIn(t, app, collectionID, "Listed twice")

	reference := UnsavedDraft{CollectionID: collectionID, ItemID: draft.ID}
	if _, err := app.SaveUnsavedDrafts([]UnsavedDraft{reference, reference}); err != nil {
		t.Fatal(err)
	}
	if itemFromState(t, app, collectionID, draft.ID).Draft {
		t.Error("the draft was not saved")
	}
}

// Several drafts at once is the ordinary case on close, and all of them must
// land — this is the whole point of the plural API.
func TestSaveUnsavedDraftsSavesEveryDraftInTheList(t *testing.T) {
	app := newAppForTest(t)
	collectionID := firstCollectionID(t, app)
	first := draftIn(t, app, collectionID, "First")
	second := draftIn(t, app, collectionID, "Second")

	if _, err := app.SaveUnsavedDrafts([]UnsavedDraft{
		{CollectionID: collectionID, ItemID: first.ID},
		{CollectionID: collectionID, ItemID: second.ID},
	}); err != nil {
		t.Fatal(err)
	}
	for _, item := range []RequestItem{first, second} {
		if itemFromState(t, app, collectionID, item.ID).Draft {
			t.Errorf("%q was left as a draft", item.Name)
		}
		if _, err := os.Stat(item.FilePath); err != nil {
			t.Errorf("%q has no file on disk: %v", item.Name, err)
		}
	}
}

// A SCRATCH request stays transient after saving. Scratch is a temporary
// workspace by design: clearing the flag there would promote a throwaway
// request into one the app treats as permanent, and it would stop being cleaned
// up.
func TestSaveUnsavedDraftsKeepsScratchRequestsTransient(t *testing.T) {
	app := newAppForTest(t)
	state, err := app.CreateWorkspace("Scratch host")
	if err != nil {
		t.Fatal(err)
	}
	workspaceID := workspaceIDNamed(t, state, "Scratch host")

	var scratchID string
	for _, ws := range state.Workspaces {
		if ws.ID != workspaceID {
			continue
		}
		for _, collection := range ws.Collections {
			if collection.Scratch {
				scratchID = collection.ID
			}
		}
	}
	if scratchID == "" {
		t.Fatal("the new workspace has no scratch collection")
	}

	// A request CREATED in a scratch collection is already transient and is NOT
	// a draft — verified, not assumed. It becomes one when the user edits it,
	// which is the only way this branch is reached at all.
	created, err := app.CreateRequest(scratchID, "http", "Throwaway")
	if err != nil {
		t.Fatal(err)
	}
	draft := findRequestByNameForTest(t, created, scratchID, "Throwaway")
	if draft.Draft {
		t.Fatal("a new scratch request is expected to start as a non-draft; this test's premise has changed")
	}
	url := "https://example.test/edited"
	if _, err := app.UpdateRequest(scratchID, draft.ID, RequestPatch{URL: &url}); err != nil {
		t.Fatal(err)
	}
	if !itemFromState(t, app, scratchID, draft.ID).Draft {
		t.Fatal("editing a scratch request should make it a draft")
	}

	if _, err := app.SaveUnsavedDrafts([]UnsavedDraft{{CollectionID: scratchID, ItemID: draft.ID}}); err != nil {
		t.Fatal(err)
	}

	saved := itemFromState(t, app, scratchID, draft.ID)
	if saved.Draft {
		t.Error("the scratch draft was not saved")
	}
	if !saved.Transient {
		t.Error("a saved SCRATCH request lost its transient flag, so it would no longer be cleaned up")
	}
}

// An empty list is what the dialog sends when the user had nothing unsaved but
// clicked Save anyway. It must succeed quietly rather than erroring on the way
// out of the app.
func TestSaveUnsavedDraftsAcceptsAnEmptyList(t *testing.T) {
	app := newAppForTest(t)
	if _, err := app.SaveUnsavedDrafts(nil); err != nil {
		t.Errorf("a nil list should be a no-op, got %v", err)
	}
	if _, err := app.SaveUnsavedDrafts([]UnsavedDraft{}); err != nil {
		t.Errorf("an empty list should be a no-op, got %v", err)
	}
}

// Saving a draft must not disturb the OTHER drafts in the same collection —
// the user may deliberately save some and discard the rest.
func TestSaveUnsavedDraftsLeavesUnlistedDraftsAlone(t *testing.T) {
	app := newAppForTest(t)
	collectionID := firstCollectionID(t, app)
	saved := draftIn(t, app, collectionID, "Saved")
	untouched := draftIn(t, app, collectionID, "Untouched")

	if _, err := app.SaveUnsavedDrafts([]UnsavedDraft{{CollectionID: collectionID, ItemID: saved.ID}}); err != nil {
		t.Fatal(err)
	}
	if itemFromState(t, app, collectionID, saved.ID).Draft {
		t.Error("the listed draft was not saved")
	}
	if !itemFromState(t, app, collectionID, untouched.ID).Draft {
		t.Error("an unlisted draft was saved too")
	}
}

// Deduplication is shared by SaveUnsavedDrafts and DiscardUnsavedDrafts, and
// only the DISCARD side can observe it. Saving twice is idempotent — the second
// pass clears an already-clear flag and rewrites the same file — so removing
// uniqueUnsavedDrafts from the save path fails nothing, and that was confirmed
// by mutation rather than assumed.
//
// Discarding twice is not idempotent: the first pass REMOVES a never-saved
// request, and the second cannot find it and returns an error. So a close
// dialog that listed one request once per open tab — which is how it builds its
// list — would fail on the way out of the app, with the user's work already
// discarded and an error box as the last thing they see.
//
// Removing the dedup from the discard path was invisible to the entire package
// main suite before this test.
func TestDiscardUnsavedDraftsToleratesTheSameDraftListedTwice(t *testing.T) {
	app := newAppForTest(t)
	collectionID := firstCollectionID(t, app)
	draft := draftIn(t, app, collectionID, "Listed twice for discard")

	reference := UnsavedDraft{CollectionID: collectionID, ItemID: draft.ID}
	state, err := app.DiscardUnsavedDrafts([]UnsavedDraft{reference, reference})
	if err != nil {
		t.Fatalf("discarding the same never-saved draft twice failed: %v", err)
	}
	if _, ok := findItemInState(state, collectionID, draft.ID); ok {
		t.Error("the never-saved draft was not removed")
	}
}

// The same, with several distinct drafts each listed twice, since the close
// dialog can repeat more than one of them.
func TestDiscardUnsavedDraftsToleratesSeveralRepeatedDrafts(t *testing.T) {
	app := newAppForTest(t)
	collectionID := firstCollectionID(t, app)
	first := draftIn(t, app, collectionID, "Repeat one")
	second := draftIn(t, app, collectionID, "Repeat two")

	firstRef := UnsavedDraft{CollectionID: collectionID, ItemID: first.ID}
	secondRef := UnsavedDraft{CollectionID: collectionID, ItemID: second.ID}
	state, err := app.DiscardUnsavedDrafts([]UnsavedDraft{firstRef, secondRef, firstRef, secondRef})
	if err != nil {
		t.Fatalf("discarding repeated drafts failed: %v", err)
	}
	if _, ok := findItemInState(state, collectionID, first.ID); ok {
		t.Error("the first draft was not removed")
	}
	if _, ok := findItemInState(state, collectionID, second.ID); ok {
		t.Error("the second draft was not removed")
	}
}
