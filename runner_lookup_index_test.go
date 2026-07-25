package main

import (
	"fmt"
	"testing"
)

// US-024. These tests exist because the failure mode of a wrong ID→index map is
// silent: findItem returns &collection.Items[i], so an index that has gone stale
// hands back a *different* request, which the caller then mutates. Nothing
// errors. So every test here asserts the returned pointer is the same pointer
// the linear scan would have returned, after mutations that shift indices.

func indexTestItem(id, name string) RequestItem {
	return RequestItem{ID: id, Name: name, Type: "http", Method: "GET", URL: "https://example.test/" + id}
}

func newIndexTestApp(t *testing.T) *App {
	t.Helper()
	app := NewAppWithDir(t.TempDir())
	app.state.Workspaces = []Workspace{
		{
			ID:   "ws-1",
			Name: "one",
			Collections: []Collection{
				{ID: "coll-a", Name: "A", Format: "bru", Items: []RequestItem{
					indexTestItem("a-1", "First"),
					indexTestItem("a-2", "Second"),
					indexTestItem("a-3", "Third"),
				}},
				{ID: "coll-b", Name: "B", Format: "bru", Items: []RequestItem{
					indexTestItem("b-1", "Only"),
				}},
			},
		},
		{
			ID:   "ws-2",
			Name: "two",
			Collections: []Collection{
				{ID: "coll-c", Name: "C", Format: "bru", Items: []RequestItem{
					indexTestItem("c-1", "Alpha"),
					indexTestItem("c-2", "Beta"),
				}},
			},
		},
	}
	app.state.ActiveWorkspaceID = "ws-1"
	return app
}

// assertIndexAgreesWithScan checks the indexed lookups against the untouched
// linear scans for every collection and every item currently in state, by
// pointer. Pointer equality is the assertion that matters: two different items
// can compare equal by value, but only one of them is the live element the
// caller is about to write through.
func assertIndexAgreesWithScan(t *testing.T, app *App, index *runnerLookupIndex, stage string) {
	t.Helper()
	for wi := range app.state.Workspaces {
		for ci := range app.state.Workspaces[wi].Collections {
			collectionID := app.state.Workspaces[wi].Collections[ci].ID
			wantWS, wantCollection, wantErr := app.findCollectionWithWorkspaceLocked(collectionID)
			gotWS, gotCollection, gotErr := app.findCollectionWithWorkspaceIndexedLocked(index, collectionID)
			if wantErr != nil || gotErr != nil {
				t.Fatalf("%s: collection %s: scan err %v, indexed err %v", stage, collectionID, wantErr, gotErr)
			}
			if gotWS != wantWS || gotCollection != wantCollection {
				t.Fatalf("%s: collection %s: indexed lookup returned a different element than the scan", stage, collectionID)
			}
			for ii := range wantCollection.Items {
				itemID := wantCollection.Items[ii].ID
				wantItem, wantErr := findItem(wantCollection, itemID)
				gotItem, gotErr := index.findItemIndexed(collectionID, wantCollection, itemID)
				if wantErr != nil || gotErr != nil {
					t.Fatalf("%s: item %s/%s: scan err %v, indexed err %v", stage, collectionID, itemID, wantErr, gotErr)
				}
				if gotItem != wantItem {
					t.Fatalf("%s: item %s/%s: indexed lookup returned %q, scan returned %q", stage, collectionID, itemID, gotItem.Name, wantItem.Name)
				}
			}
		}
	}
}

// TestRunnerLookupIndexSurvivesItemMutation is the staleness proof the story
// requires: build the index, warm it, then add / delete / reorder / replace
// requests and confirm every lookup still resolves to the right element.
func TestRunnerLookupIndexSurvivesItemMutation(t *testing.T) {
	app := newIndexTestApp(t)
	index := newRunnerLookupIndex(&app.state)
	assertIndexAgreesWithScan(t, app, index, "initial")

	collection := &app.state.Workspaces[0].Collections[0]

	// Append: existing indices stay valid, the new one is not in the map yet.
	collection.Items = append(collection.Items, indexTestItem("a-4", "Fourth"))
	assertIndexAgreesWithScan(t, app, index, "after append")

	// Prepend: every existing index shifts by one. This is the case a naive
	// cache gets silently wrong.
	collection.Items = append([]RequestItem{indexTestItem("a-0", "Zeroth")}, collection.Items...)
	assertIndexAgreesWithScan(t, app, index, "after prepend")

	// Delete from the middle: indices after it shift down.
	collection.Items = append(collection.Items[:2], collection.Items[3:]...)
	assertIndexAgreesWithScan(t, app, index, "after delete")

	// Reorder: length and identity are unchanged, so only a value check can
	// catch this. Reverse the slice.
	for i, j := 0, len(collection.Items)-1; i < j; i, j = i+1, j-1 {
		collection.Items[i], collection.Items[j] = collection.Items[j], collection.Items[i]
	}
	assertIndexAgreesWithScan(t, app, index, "after reorder")

	// Replace the whole slice with a fresh backing array of different length.
	collection.Items = []RequestItem{indexTestItem("a-9", "Ninth"), indexTestItem("a-1", "First")}
	assertIndexAgreesWithScan(t, app, index, "after replace")

	// Empty it entirely: no lookup may panic on an out-of-range hint.
	collection.Items = nil
	if _, err := index.findItemIndexed("coll-a", collection, "a-1"); err == nil {
		t.Fatal("lookup into an emptied collection should fail, not resolve")
	}
	assertIndexAgreesWithScan(t, app, index, "after clear")
}

// TestRunnerLookupIndexSurvivesCollectionMutation covers the other half: the
// workspace tree itself moving under a run.
func TestRunnerLookupIndexSurvivesCollectionMutation(t *testing.T) {
	app := newIndexTestApp(t)
	index := newRunnerLookupIndex(&app.state)
	assertIndexAgreesWithScan(t, app, index, "initial")

	workspace := &app.state.Workspaces[0]

	// Insert a collection ahead of the indexed ones.
	workspace.Collections = append([]Collection{
		{ID: "coll-new", Name: "New", Format: "bru", Items: []RequestItem{indexTestItem("n-1", "New One")}},
	}, workspace.Collections...)
	assertIndexAgreesWithScan(t, app, index, "after collection insert")

	// Reorder collections.
	workspace.Collections[0], workspace.Collections[2] = workspace.Collections[2], workspace.Collections[0]
	assertIndexAgreesWithScan(t, app, index, "after collection reorder")

	// Prepend a workspace, shifting every workspace index.
	app.state.Workspaces = append([]Workspace{{ID: "ws-0", Name: "zero"}}, app.state.Workspaces...)
	assertIndexAgreesWithScan(t, app, index, "after workspace prepend")

	// Remove the workspace holding coll-c: its ID must now be reported missing,
	// with the same error the plain scan produces.
	app.state.Workspaces = app.state.Workspaces[:len(app.state.Workspaces)-1]
	_, _, wantErr := app.findCollectionWithWorkspaceLocked("coll-c")
	_, _, gotErr := app.findCollectionWithWorkspaceIndexedLocked(index, "coll-c")
	if wantErr == nil || gotErr == nil || wantErr.Error() != gotErr.Error() {
		t.Fatalf("removed collection: scan err %v, indexed err %v", wantErr, gotErr)
	}
	assertIndexAgreesWithScan(t, app, index, "after workspace removal")

	// And it must resolve again if it comes back, rather than staying poisoned.
	app.state.Workspaces = append(app.state.Workspaces, Workspace{
		ID: "ws-2", Name: "two",
		Collections: []Collection{{ID: "coll-c", Name: "C", Format: "bru", Items: []RequestItem{indexTestItem("c-1", "Alpha")}}},
	})
	assertIndexAgreesWithScan(t, app, index, "after collection returns")
}

// TestRunnerLookupIndexNilFallsBackToScan pins the nil-index contract, since
// every non-runner caller of the send path passes nil.
func TestRunnerLookupIndexNilFallsBackToScan(t *testing.T) {
	app := newIndexTestApp(t)
	var index *runnerLookupIndex

	wantWS, wantCollection, err := app.findCollectionWithWorkspaceLocked("coll-a")
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	gotWS, gotCollection, err := app.findCollectionWithWorkspaceIndexedLocked(index, "coll-a")
	if err != nil {
		t.Fatalf("indexed: %v", err)
	}
	if gotWS != wantWS || gotCollection != wantCollection {
		t.Fatal("nil index must return exactly what the scan returns")
	}

	wantItem, err := findItem(wantCollection, "a-2")
	if err != nil {
		t.Fatalf("scan item: %v", err)
	}
	gotItem, err := index.findItemIndexed("coll-a", wantCollection, "a-2")
	if err != nil {
		t.Fatalf("indexed item: %v", err)
	}
	if gotItem != wantItem {
		t.Fatal("nil index must return exactly the item the scan returns")
	}

	if _, err := index.findItemIndexed("coll-a", wantCollection, "missing"); err == nil {
		t.Fatal("nil index must still report a missing item")
	}
}

// TestRunnerLookupIndexErrorsMatchScan keeps the error strings identical, since
// they surface to the frontend.
func TestRunnerLookupIndexErrorsMatchScan(t *testing.T) {
	app := newIndexTestApp(t)
	index := newRunnerLookupIndex(&app.state)

	_, _, wantErr := app.findCollectionWithWorkspaceLocked("nope")
	_, _, gotErr := app.findCollectionWithWorkspaceIndexedLocked(index, "nope")
	if wantErr.Error() != gotErr.Error() {
		t.Fatalf("collection error mismatch: scan %q, indexed %q", wantErr, gotErr)
	}

	collection := &app.state.Workspaces[0].Collections[0]
	_, wantErr = findItem(collection, "nope")
	_, gotErr = index.findItemIndexed("coll-a", collection, "nope")
	if wantErr.Error() != gotErr.Error() {
		t.Fatalf("item error mismatch: scan %q, indexed %q", wantErr, gotErr)
	}
}

// TestRunnerLookupIndexWritesReachLiveState is the end of the argument: the
// pointer the index returns must be the one whose mutation is visible in state.
func TestRunnerLookupIndexWritesReachLiveState(t *testing.T) {
	app := newIndexTestApp(t)
	index := newRunnerLookupIndex(&app.state)

	_, collection, err := app.findCollectionWithWorkspaceIndexedLocked(index, "coll-a")
	if err != nil {
		t.Fatalf("collection: %v", err)
	}
	if _, err := index.findItemIndexed("coll-a", collection, "a-3"); err != nil {
		t.Fatalf("warm: %v", err)
	}

	// Shift every index, then write through the indexed pointer.
	collection.Items = append([]RequestItem{indexTestItem("a-0", "Zeroth")}, collection.Items...)
	_, collection, err = app.findCollectionWithWorkspaceIndexedLocked(index, "coll-a")
	if err != nil {
		t.Fatalf("collection after shift: %v", err)
	}
	item, err := index.findItemIndexed("coll-a", collection, "a-3")
	if err != nil {
		t.Fatalf("item after shift: %v", err)
	}
	marker := &Response{Status: 418, StatusText: "written through the index"}
	item.Response = marker

	for i := range app.state.Workspaces[0].Collections[0].Items {
		live := app.state.Workspaces[0].Collections[0].Items[i]
		switch {
		case live.ID == "a-3" && live.Response != marker:
			t.Fatal("write through the indexed pointer did not reach a-3 in live state")
		case live.ID != "a-3" && live.Response == marker:
			t.Fatalf("write through the indexed pointer landed on the wrong request %s", live.ID)
		}
	}
}

// TestRunnerLookupIndexRepairsAfterEveryMiss guards the performance property as
// well as the correctness one: a mutation must cost one rescan, not turn every
// later lookup back into a scan.
func TestRunnerLookupIndexRepairsAfterEveryMiss(t *testing.T) {
	app := newIndexTestApp(t)
	index := newRunnerLookupIndex(&app.state)
	collection := &app.state.Workspaces[0].Collections[0]

	if _, err := index.findItemIndexed("coll-a", collection, "a-1"); err != nil {
		t.Fatalf("warm: %v", err)
	}
	if got := len(index.items["coll-a"]); got != len(collection.Items) {
		t.Fatalf("item index holds %d entries, want %d", got, len(collection.Items))
	}

	collection.Items = append([]RequestItem{indexTestItem("a-0", "Zeroth")}, collection.Items...)
	if _, err := index.findItemIndexed("coll-a", collection, "a-1"); err != nil {
		t.Fatalf("after shift: %v", err)
	}
	if got := len(index.items["coll-a"]); got != len(collection.Items) {
		t.Fatalf("item index was not rebuilt: holds %d entries, want %d", got, len(collection.Items))
	}
	if got, ok := index.items["coll-a"]["a-1"]; !ok || collection.Items[got].ID != "a-1" {
		t.Fatalf("rebuilt index maps a-1 to %d, which is %q", got, collection.Items[got].ID)
	}
}

// TestRunnerLookupIndexDuplicateIDsMatchScan pins behaviour on a state that
// should be impossible: duplicate request IDs. First occurrence wins, in both
// the map build and the scan, so the two cannot diverge.
func TestRunnerLookupIndexDuplicateIDsMatchScan(t *testing.T) {
	app := newIndexTestApp(t)
	collection := &app.state.Workspaces[0].Collections[0]
	collection.Items = []RequestItem{
		indexTestItem("dup", "First"),
		indexTestItem("other", "Other"),
		indexTestItem("dup", "Second"),
	}
	index := newRunnerLookupIndex(&app.state)

	want, err := findItem(collection, "dup")
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	for round := 0; round < 3; round++ {
		got, err := index.findItemIndexed("coll-a", collection, "dup")
		if err != nil {
			t.Fatalf("round %d: %v", round, err)
		}
		if got != want {
			t.Fatalf("round %d: indexed lookup chose %q, scan chose %q", round, got.Name, want.Name)
		}
	}
}

// TestRunnerItemNameIndexMatchesScan covers the jump-target map, which replaced
// a `for ... if candidate.Name == ...` scan whose first match won.
func TestRunnerItemNameIndexMatchesScan(t *testing.T) {
	items := []RequestItem{
		indexTestItem("1", "Login"),
		indexTestItem("2", "Fetch"),
		indexTestItem("3", "Login"),
		indexTestItem("4", ""),
	}
	byName := runnerItemNameIndex(items)

	for _, name := range []string{"Login", "Fetch", "", "Absent"} {
		want := -1
		for i, candidate := range items {
			if candidate.Name == name {
				want = i
				break
			}
		}
		got := -1
		if index, ok := byName[name]; ok {
			got = index
		}
		if got != want {
			t.Fatalf("name %q: index gave %d, scan gave %d", name, got, want)
		}
	}
}

// TestRunnerLookupIndexScalesLinearly is the complexity claim as an assertion
// rather than a benchmark note. It counts ID comparisons instead of timing, so
// it cannot flake on a loaded machine.
func TestRunnerLookupIndexScalesLinearly(t *testing.T) {
	comparisons := func(requests int) int {
		opts := defaultLargeWorkspaceOptions()
		opts.RequestsPerColl = requests / opts.Collections
		opts.LargeResponses = 0
		opts.LargeResponseSize = 1
		opts.SmallResponseSize = 1
		app := NewAppWithDir(t.TempDir())
		app.state = buildLargeWorkspaceState(opts)

		index := newRunnerLookupIndex(&app.state)
		scans := 0
		for wi := range app.state.Workspaces {
			for ci := range app.state.Workspaces[wi].Collections {
				collectionID := app.state.Workspaces[wi].Collections[ci].ID
				for ii := range app.state.Workspaces[wi].Collections[ci].Items {
					itemID := app.state.Workspaces[wi].Collections[ci].Items[ii].ID
					before := len(index.items[collectionID])
					_, collection, err := app.findCollectionWithWorkspaceIndexedLocked(index, collectionID)
					if err != nil {
						t.Fatalf("collection %s: %v", collectionID, err)
					}
					if _, err := index.findItemIndexed(collectionID, collection, itemID); err != nil {
						t.Fatalf("item %s/%s: %v", collectionID, itemID, err)
					}
					if len(index.items[collectionID]) != before {
						scans++
					}
				}
			}
		}
		return scans
	}

	// With no mutation during the walk, each collection is scanned exactly once
	// to build its item map. Total scans therefore track the collection count,
	// not the request count: 5x the requests must not cost 5x the scans.
	for _, requests := range []int{100, 250, 500} {
		if got, want := comparisons(requests), defaultLargeWorkspaceOptions().Collections; got != want {
			t.Fatalf("%d requests: %d item-map rebuilds, want %d (one per collection)", requests, got, want)
		}
	}
}

// TestRunnerLookupIndexAcrossFixtureShapes is a broad equivalence sweep: for
// every request in the fixture at three sizes, the indexed lookup and the scan
// must return the same pointer.
func TestRunnerLookupIndexAcrossFixtureShapes(t *testing.T) {
	for _, requests := range []int{100, 250, 500} {
		t.Run(fmt.Sprintf("requests=%d", requests), func(t *testing.T) {
			opts := defaultLargeWorkspaceOptions()
			opts.RequestsPerColl = requests / opts.Collections
			opts.LargeResponses = 0
			opts.LargeResponseSize = 1
			opts.SmallResponseSize = 1
			app := NewAppWithDir(t.TempDir())
			app.state = buildLargeWorkspaceState(opts)
			index := newRunnerLookupIndex(&app.state)
			assertIndexAgreesWithScan(t, app, index, "fixture")
		})
	}
}
