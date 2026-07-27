package core

import (
	"testing"
)

// The Scratch collection's position in the sidebar is decided by three small
// helpers. Two of them, countRegularCollections and firstScratchCollectionIndex,
// report 100% line coverage — and yet making countRegularCollections count
// scratch collections as regular left the ENTIRE internal/core suite green.
//
// Every line ran; nothing checked what any of them meant. That is the whole
// argument for controlling a mutation rather than reading a coverage figure,
// and it is why these tests exist for functions that were already "covered".

func scratch(name string) Collection            { return Collection{ID: "s-" + name, Name: name, Scratch: true} }
func regular(name string) Collection            { return Collection{ID: "c-" + name, Name: name} }
func collections(in ...Collection) []Collection { return in }

// The defining behaviour: scratch collections are NOT regular. Both callers ask
// "are there any real collections here yet", and an implementation that counted
// everything would answer yes for a workspace holding nothing but Scratch.
func TestCountRegularCollectionsExcludesScratch(t *testing.T) {
	cases := []struct {
		name  string
		input []Collection
		want  int
	}{
		{"empty", nil, 0},
		{"only scratch", collections(scratch("Scratch")), 0},
		{"several scratch", collections(scratch("a"), scratch("b")), 0},
		{"only regular", collections(regular("a"), regular("b")), 2},
		{"mixed", collections(scratch("s"), regular("a"), regular("b")), 2},
		{"regular first", collections(regular("a"), scratch("s")), 1},
	}
	for _, testCase := range cases {
		if got := countRegularCollections(testCase.input); got != testCase.want {
			t.Errorf("%s: got %d, want %d", testCase.name, got, testCase.want)
		}
	}
}

func TestFirstScratchCollectionIndexFindsTheFirstOne(t *testing.T) {
	cases := []struct {
		name  string
		input []Collection
		want  int
	}{
		{"empty", nil, -1},
		{"none", collections(regular("a"), regular("b")), -1},
		{"at the front", collections(scratch("s"), regular("a")), 0},
		{"after a regular one", collections(regular("a"), scratch("s")), 1},
		{"two of them takes the first", collections(regular("a"), scratch("s1"), scratch("s2")), 1},
	}
	for _, testCase := range cases {
		if got := firstScratchCollectionIndex(testCase.input); got != testCase.want {
			t.Errorf("%s: got %d, want %d", testCase.name, got, testCase.want)
		}
	}
}

// Scratch sits SECOND once there is a real collection, so the user's own work
// stays at the top of the sidebar. With nothing but scratch entries it takes
// the position of the first of them rather than being appended, which keeps a
// repair from leaving two scratch collections in different places.
func TestScratchCollectionInsertIndex(t *testing.T) {
	cases := []struct {
		name  string
		input []Collection
		want  int
	}{
		{"empty workspace goes first", nil, 0},
		{"after a single regular collection", collections(regular("a")), 1},
		{"after the first of several", collections(regular("a"), regular("b")), 1},
		{"takes an existing scratch slot", collections(scratch("s")), 0},
		{"takes the slot of a later scratch", collections(scratch("s1"), scratch("s2")), 0},
		{"regular collections win over a scratch slot", collections(scratch("s"), regular("a")), 1},
	}
	for _, testCase := range cases {
		if got := scratchCollectionInsertIndex(testCase.input); got != testCase.want {
			t.Errorf("%s: got %d, want %d", testCase.name, got, testCase.want)
		}
	}
}

// THE USER-VISIBLE CONSEQUENCE, through the public API. A NEWLY CREATED
// workspace holds nothing but Scratch — verified, not assumed — so the first
// real collection the user makes there goes ABOVE it, keeping their own work at
// the top of the sidebar, and later ones are appended after.
//
// This uses a new workspace rather than the default one, which ships with a
// "Sample API" collection and so cannot show the placement. An earlier version
// took the default workspace and SKIPPED, which is a test that reports success
// while checking nothing.
func TestTheFirstRealCollectionIsPlacedAboveScratch(t *testing.T) {
	app := newAppForTest(t)
	state, err := app.CreateWorkspace("Fresh")
	if err != nil {
		t.Fatal(err)
	}
	workspaceID := workspaceIDNamed(t, state, "Fresh")

	if names := collectionNamesIn(t, app, workspaceID); len(names) != 1 || names[0] != "Scratch" {
		t.Fatalf("a new workspace holds %v, want only Scratch — the premise of this test", names)
	}

	if _, err = app.CreateCollection(workspaceID, "First", "bru"); err != nil {
		t.Fatal(err)
	}
	names := collectionNamesIn(t, app, workspaceID)
	if len(names) != 2 || names[0] != "First" || names[1] != "Scratch" {
		t.Fatalf("collections = %v, want [First Scratch] — the new one above Scratch", names)
	}

	if _, err = app.CreateCollection(workspaceID, "Second", "bru"); err != nil {
		t.Fatal(err)
	}
	names = collectionNamesIn(t, app, workspaceID)
	if len(names) != 3 || names[0] != "First" || names[2] != "Second" {
		t.Errorf("collections = %v, want [First Scratch Second] — later ones appended", names)
	}
}

func workspaceIDNamed(t *testing.T, state AppState, name string) string {
	t.Helper()
	for _, ws := range state.Workspaces {
		if ws.Name == name {
			return ws.ID
		}
	}
	t.Fatalf("no workspace named %q", name)
	return ""
}

func collectionNamesIn(t *testing.T, app *App, workspaceID string) []string {
	t.Helper()
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	for _, ws := range state.Workspaces {
		if ws.ID == workspaceID {
			return collectionNames(ws.Collections)
		}
	}
	t.Fatalf("workspace %q is gone", workspaceID)
	return nil
}

func collectionNames(in []Collection) []string {
	names := make([]string, 0, len(in))
	for _, collection := range in {
		names = append(names, collection.Name)
	}
	return names
}
