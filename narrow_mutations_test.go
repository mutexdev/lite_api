package main

// US-014 — narrow-return mutators.
//
// The risk this file targets is DRIFT. Two entry points now run the same
// mutation, and the failure that matters is not "the narrow one is broken" —
// that shows up immediately — but "the two disagree", where the frontend and
// the backend end up with different ideas of what the user typed and neither
// looks obviously wrong. So the central test runs each pair against identical
// starting states and compares the resulting state, rather than checking each
// variant against hand-written expectations.

import (
	"encoding/json"
	"reflect"
	"testing"
)

// jsonSizeForTest measures a value the way Wails will: as the JSON that
// actually crosses the bridge.
func jsonSizeForTest(t *testing.T, value any) int {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return len(data)
}

func narrowFixture(t *testing.T) (app *App, collectionID, itemID string) {
	t.Helper()
	app = newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	collection := state.Workspaces[0].Collections[0]
	created, err := app.CreateRequest(collection.ID, "http", "narrow probe")
	if err != nil {
		t.Fatalf("CreateRequest: %v", err)
	}
	for _, workspace := range created.Workspaces {
		for _, c := range workspace.Collections {
			if c.ID != collection.ID {
				continue
			}
			for _, item := range c.Items {
				if item.Name == "narrow probe" {
					itemID = item.ID
				}
			}
		}
	}
	if itemID == "" {
		t.Fatalf("could not find the request just created")
	}
	return app, collection.ID, itemID
}

// TestUpdateRequestNarrowMatchesUpdateRequest is the anti-drift test.
//
// Both variants are driven against THE SAME item on THE SAME app, one after the
// other with the same patch. That is what makes the comparison trustworthy: an
// earlier version of this test used two separate apps and had to normalise away
// FilePath and CreatedAt, which differed only because the two temp directories
// and clock readings differed. Every field normalised away is a field where
// real drift could hide, so the fixture is arranged to need only one — the
// UpdatedAt stamp that both variants deliberately set to time.Now().
func TestUpdateRequestNarrowMatchesUpdateRequest(t *testing.T) {
	app, collectionID, itemID := narrowFixture(t)

	url := "https://example.test/narrow"
	method := "POST"
	patch := RequestPatch{URL: &url, Method: &method}

	wideState, err := app.UpdateRequest(collectionID, itemID, patch)
	if err != nil {
		t.Fatalf("UpdateRequest: %v", err)
	}
	afterWide, ok := findItemInState(wideState, collectionID, itemID)
	if !ok {
		t.Fatalf("wide path lost the item")
	}

	result, err := app.UpdateRequestNarrow(collectionID, itemID, patch)
	if err != nil {
		t.Fatalf("UpdateRequestNarrow: %v", err)
	}

	afterWide.UpdatedAt = result.Item.UpdatedAt
	if !reflect.DeepEqual(afterWide, result.Item) {
		t.Errorf("narrow and wide UpdateRequest produced different items:\n wide  = %#v\n narrow= %#v", afterWide, result.Item)
	}

	// The narrow result must also match what the backend now actually holds.
	// Returning a correct-looking item that does not match stored state would
	// leave the frontend confidently displaying something the app does not have.
	state, err := app.GetState()
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	stored, ok := findItemInState(state, collectionID, itemID)
	if !ok {
		t.Fatalf("narrow path lost the item")
	}
	if !reflect.DeepEqual(stored, result.Item) {
		t.Errorf("narrow result does not match stored state:\n stored= %#v\n narrow= %#v", stored, result.Item)
	}

	if result.CollectionID != collectionID {
		t.Errorf("RequestMutation.CollectionID = %q, want %q", result.CollectionID, collectionID)
	}
	if result.Revision == 0 {
		t.Error("RequestMutation.Revision is zero; the frontend cannot detect a gap without it")
	}
}

// TestNarrowMutatorsAdvanceTheRevisionByExactlyOne pins the contract the
// frontend's gap detection depends on. If a mutation could bump by two, every
// narrow call would look like a missed update and the frontend would refetch
// the whole AppState every time — reintroducing precisely the cost this story
// removes, while still appearing to work.
func TestNarrowMutatorsAdvanceTheRevisionByExactlyOne(t *testing.T) {
	app, collectionID, itemID := narrowFixture(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	previous := state.Revision

	url := "https://step.example"
	for i := range 10 {
		url += "/x"
		result, err := app.UpdateRequestNarrow(collectionID, itemID, RequestPatch{URL: &url})
		if err != nil {
			t.Fatalf("UpdateRequestNarrow #%d: %v", i, err)
		}
		if result.Revision != previous+1 {
			t.Fatalf("iteration %d: revision jumped %d -> %d; the frontend would treat every call as a gap",
				i, previous, result.Revision)
		}
		previous = result.Revision
	}
}

// TestTabNarrowMutatorsReturnTabState covers the three tab variants together,
// since they share a return shape and a failure mode.
func TestTabNarrowMutatorsReturnTabState(t *testing.T) {
	app, collectionID, itemID := narrowFixture(t)
	opened, err := app.OpenRequestTab(collectionID, itemID)
	if err != nil {
		t.Fatalf("OpenRequestTab: %v", err)
	}
	if len(opened.OpenTabs) == 0 {
		t.Fatalf("no tabs open after OpenRequestTab")
	}
	tabID := opened.OpenTabs[0].ID

	active, err := app.SetActiveTabNarrow(tabID)
	if err != nil {
		t.Fatalf("SetActiveTabNarrow: %v", err)
	}
	if active.ActiveTabID != tabID {
		t.Errorf("SetActiveTabNarrow returned activeTabId %q, want %q", active.ActiveTabID, tabID)
	}
	if len(active.OpenTabs) != len(opened.OpenTabs) {
		t.Errorf("SetActiveTabNarrow returned %d tabs, want %d", len(active.OpenTabs), len(opened.OpenTabs))
	}
	if active.Revision == 0 {
		t.Error("SetActiveTabNarrow returned revision 0")
	}

	panes, err := app.UpdateOpenTabPanesNarrow(tabID, "headers", "response")
	if err != nil {
		t.Fatalf("UpdateOpenTabPanesNarrow: %v", err)
	}
	if panes.OpenTabs[0].RequestPaneTab != "headers" || panes.OpenTabs[0].ResponseTab != "response" {
		t.Errorf("panes not applied: %#v", panes.OpenTabs[0])
	}
	if panes.Revision <= active.Revision {
		t.Errorf("UpdateOpenTabPanesNarrow did not advance the revision (%d -> %d)", active.Revision, panes.Revision)
	}

	// A move that cannot go anywhere must succeed without advancing the
	// revision — a bump for a no-op would desynchronise the frontend's count.
	moved, err := app.MoveOpenTabNarrow(tabID, -5)
	if err != nil {
		t.Fatalf("MoveOpenTabNarrow: %v", err)
	}
	if moved.Revision != panes.Revision {
		t.Errorf("a no-op tab move advanced the revision %d -> %d", panes.Revision, moved.Revision)
	}
}

// TestNarrowMutatorsRejectUnknownTargets. The zero value of TabsMutation has an
// empty OpenTabs slice, so a caller that ignored the error would replace its
// tab bar with nothing. The error must be present and the result must not be
// mistakable for a successful empty state.
func TestNarrowMutatorsRejectUnknownTargets(t *testing.T) {
	app, collectionID, itemID := narrowFixture(t)

	if _, err := app.SetActiveTabNarrow("no-such-tab"); err == nil {
		t.Error("SetActiveTabNarrow accepted an unknown tab")
	}
	if _, err := app.UpdateOpenTabPanesNarrow("no-such-tab", "headers", ""); err == nil {
		t.Error("UpdateOpenTabPanesNarrow accepted an unknown tab")
	}
	if _, err := app.MoveOpenTabNarrow("no-such-tab", 1); err == nil {
		t.Error("MoveOpenTabNarrow accepted an unknown tab")
	}
	if _, err := app.UpdateRequestNarrow(collectionID, "no-such-item", RequestPatch{}); err == nil {
		t.Error("UpdateRequestNarrow accepted an unknown item")
	}
	if _, err := app.UpdateRequestNarrow("no-such-collection", itemID, RequestPatch{}); err == nil {
		t.Error("UpdateRequestNarrow accepted an unknown collection")
	}

	// An invalid pane name must be rejected too, and must not have partially
	// applied the valid half of the call.
	opened, err := app.OpenRequestTab(collectionID, itemID)
	if err != nil {
		t.Fatalf("OpenRequestTab: %v", err)
	}
	tabID := opened.OpenTabs[0].ID
	before := opened.OpenTabs[0].ResponseTab
	if _, err := app.UpdateOpenTabPanesNarrow(tabID, "not-a-pane", "response"); err == nil {
		t.Error("UpdateOpenTabPanesNarrow accepted an invalid request pane name")
	}
	state, err := app.GetState()
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if state.OpenTabs[0].ResponseTab != before {
		t.Errorf("a rejected pane update still applied its valid half: %q -> %q", before, state.OpenTabs[0].ResponseTab)
	}
}

// TestNarrowMutationIsMuchSmallerThanAppState is the point of the story,
// measured rather than assumed. Comparing serialised sizes is the honest proxy
// for what crosses the Wails bridge, because that is exactly what Wails does
// with these return values.
func TestNarrowMutationIsMuchSmallerThanAppState(t *testing.T) {
	app := newLargeWorkspaceAppForTest(t, t.TempDir(), largeWorkspaceOptions{})
	state, err := app.GetState()
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	var collectionID, itemID string
	for _, workspace := range state.Workspaces {
		for _, collection := range workspace.Collections {
			if len(collection.Items) > 0 {
				collectionID = collection.ID
				itemID = collection.Items[0].ID
			}
		}
	}
	if itemID == "" {
		t.Skip("large workspace fixture produced no requests")
	}

	url := "https://measured.example"
	wide, err := app.UpdateRequest(collectionID, itemID, RequestPatch{URL: &url})
	if err != nil {
		t.Fatalf("UpdateRequest: %v", err)
	}
	narrow, err := app.UpdateRequestNarrow(collectionID, itemID, RequestPatch{URL: &url})
	if err != nil {
		t.Fatalf("UpdateRequestNarrow: %v", err)
	}

	wideSize := jsonSizeForTest(t, wide)
	narrowSize := jsonSizeForTest(t, narrow)
	if narrowSize >= wideSize/10 {
		t.Errorf("narrow result is %d bytes against a %d-byte AppState; expected at least a 10x reduction",
			narrowSize, wideSize)
	}
	t.Logf("US-014 payload for one keystroke: AppState %d bytes -> narrow %d bytes (%.0fx smaller)",
		wideSize, narrowSize, float64(wideSize)/float64(narrowSize))
}
