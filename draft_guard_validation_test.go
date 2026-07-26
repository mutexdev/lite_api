// Draft-guard validation and de-duplication.
//
// Found by negative control: uniqueUnsavedDrafts and validateUnsavedDraftsLocked
// were both unverified — disabling either failed no test.
//
// These sit on the path that runs when the user closes the app with unsaved
// work. Their failure mode is data loss the user cannot see coming: a draft
// listed twice gets written twice, and a draft that fails validation silently
// is a request whose edits never reach disk while the app reports a clean save.
package main

import (
	"strings"
	"testing"
)

func TestUniqueUnsavedDraftsCollapsesRepeats(t *testing.T) {
	drafts := []UnsavedDraft{
		{CollectionID: "c1", ItemID: "i1"},
		{CollectionID: "c1", ItemID: "i1"},
		{CollectionID: "c1", ItemID: "i2"},
		{CollectionID: "c2", ItemID: "i1"},
		{CollectionID: "c1", ItemID: "i1"},
	}
	got := uniqueUnsavedDrafts(drafts)

	if len(got) != 3 {
		t.Fatalf("expected 3 distinct drafts, got %d: %+v", len(got), got)
	}
	// Order matters: the first occurrence wins, so the save order stays the
	// order the user's tabs are in rather than being reshuffled by a map.
	if got[0] != (UnsavedDraft{CollectionID: "c1", ItemID: "i1"}) ||
		got[1] != (UnsavedDraft{CollectionID: "c1", ItemID: "i2"}) ||
		got[2] != (UnsavedDraft{CollectionID: "c2", ItemID: "i1"}) {
		t.Fatalf("first-occurrence order not preserved: %+v", got)
	}
}

// The same item id in two collections is two different drafts. A key built from
// the item id alone would drop one of them, losing that request's edits.
func TestUniqueUnsavedDraftsKeysOnBothIDs(t *testing.T) {
	got := uniqueUnsavedDrafts([]UnsavedDraft{
		{CollectionID: "c1", ItemID: "same"},
		{CollectionID: "c2", ItemID: "same"},
	})
	if len(got) != 2 {
		t.Fatalf("drafts in different collections must not collapse, got %+v", got)
	}
}

func TestUniqueUnsavedDraftsHandlesEmptyInput(t *testing.T) {
	if got := uniqueUnsavedDrafts(nil); len(got) != 0 {
		t.Fatalf("got %+v", got)
	}
}

func TestValidateUnsavedDraftsRejectsBlankIDs(t *testing.T) {
	app := newAppForTest(t)
	for _, draft := range []UnsavedDraft{
		{CollectionID: "", ItemID: "i1"},
		{CollectionID: "c1", ItemID: ""},
		{CollectionID: "   ", ItemID: "i1"},
		{CollectionID: "c1", ItemID: "\t"},
	} {
		if _, err := app.validateUnsavedDraftsLocked([]UnsavedDraft{draft}); err == nil {
			t.Errorf("draft %+v was accepted; a blank id cannot identify a request", draft)
		}
	}
}

func TestValidateUnsavedDraftsRejectsUnknownCollectionOrItem(t *testing.T) {
	app := newAppForTest(t)
	if _, err := app.validateUnsavedDraftsLocked([]UnsavedDraft{{CollectionID: "no-such", ItemID: "i1"}}); err == nil {
		t.Error("an unknown collection was accepted")
	}

	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	if _, err := app.validateUnsavedDraftsLocked([]UnsavedDraft{{CollectionID: collection.ID, ItemID: "no-such"}}); err == nil {
		t.Error("an unknown request was accepted")
	}
}

// A request with no pending edits must be refused. Accepting it would let a
// save path rewrite a file the user never touched.
func TestValidateUnsavedDraftsRejectsARequestWithNoDraft(t *testing.T) {
	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	if len(collection.Items) == 0 {
		t.Skip("fixture collection has no requests")
	}
	item := collection.Items[0]
	if item.Draft {
		t.Skip("fixture request already has a draft")
	}

	_, err = app.validateUnsavedDraftsLocked([]UnsavedDraft{{CollectionID: collection.ID, ItemID: item.ID}})
	if err == nil {
		t.Fatal("a request with no unsaved draft was accepted")
	}
	if !strings.Contains(err.Error(), "no unsaved draft") {
		t.Fatalf("error should say why, got %q", err)
	}
}

func TestValidateUnsavedDraftsAcceptsAnEmptyList(t *testing.T) {
	app := newAppForTest(t)
	collections, err := app.validateUnsavedDraftsLocked(nil)
	if err != nil {
		t.Fatalf("an empty list is not an error: %v", err)
	}
	if len(collections) != 0 {
		t.Fatalf("got %d collections", len(collections))
	}
}
