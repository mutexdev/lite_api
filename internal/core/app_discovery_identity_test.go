// Discovered collections need an identity that is not their name.
//
// Two workspaces in another client can share a name -- two Insomnia workspaces
// both left at the default are the ordinary case, not a contrived one. Matching
// the user's selection by name meant ticking one box selected both, and the
// import brought across a collection the user never chose and never saw.
package core

import (
	"os"
	"path/filepath"
	"testing"
)

// writeDuplicateNameDiscoveryFixture writes two Insomnia workspaces that share
// a name, each with its own request, so an import that takes the wrong one is
// visible in the result rather than indistinguishable from the right one.
func writeDuplicateNameDiscoveryFixture(t *testing.T, root string) {
	t.Helper()
	base := filepath.Join(root, "config", "Insomnia")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}
	content := `{"_id":"wrk_1","type":"Workspace","name":"Untitled"}
{"_id":"req_1","type":"Request","parentId":"wrk_1","name":"First request","method":"GET","url":"https://first.test/"}
{"_id":"wrk_2","type":"Workspace","name":"Untitled"}
{"_id":"req_2","type":"Request","parentId":"wrk_2","name":"Second request","method":"GET","url":"https://second.test/"}`
	if err := os.WriteFile(filepath.Join(base, "insomnia.Workspace.db"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestDiscoveredCollectionsCarryDistinctIdentities(t *testing.T) {
	app := newAppForTest(t)
	root := t.TempDir()
	writeDuplicateNameDiscoveryFixture(t, root)
	app.discoveryRootsForTest(root)

	found, err := app.ReadDiscoveredCollections("insomnia")
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 2 {
		t.Fatalf("expected both workspaces, got %#v", found)
	}
	if found[0].ID == "" || found[1].ID == "" {
		t.Fatalf("discovered collections have no id to select by: %#v", found)
	}
	if found[0].ID == found[1].ID {
		t.Fatalf("two collections share an id, so one cannot be chosen without the other: %#v", found)
	}
}

func TestImportingOneOfTwoIdenticallyNamedCollectionsImportsOnlyThatOne(t *testing.T) {
	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	writeDuplicateNameDiscoveryFixture(t, root)
	app.discoveryRootsForTest(root)

	found, err := app.ReadDiscoveredCollections("insomnia")
	if err != nil {
		t.Fatal(err)
	}
	result, err := app.ImportDiscoveredCollections(state.Workspaces[0].ID, "insomnia", []string{found[0].ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Applied) != 1 {
		t.Fatalf("chose one collection, imported %d: %#v", len(result.Applied), result.Applied)
	}
}

// An id that was never offered is an id that came from somewhere other than a
// prompt the user saw.
func TestImportingAnUnknownDiscoveredIdentityIsRefused(t *testing.T) {
	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	writeDuplicateNameDiscoveryFixture(t, root)
	app.discoveryRootsForTest(root)

	if _, err := app.ImportDiscoveredCollections(state.Workspaces[0].ID, "insomnia", []string{"insomnia:99"}); err == nil {
		t.Fatal("importing an identity that was never offered should fail")
	}
}

// The identity has to survive being read twice, or the id the modal shows is
// not the id the import receives.
func TestDiscoveredIdentitiesAreStableAcrossReads(t *testing.T) {
	app := newAppForTest(t)
	root := t.TempDir()
	writeDuplicateNameDiscoveryFixture(t, root)
	app.discoveryRootsForTest(root)

	first, err := app.ReadDiscoveredCollections("insomnia")
	if err != nil {
		t.Fatal(err)
	}
	second, err := app.ReadDiscoveredCollections("insomnia")
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != len(second) {
		t.Fatalf("reads disagree: %d then %d", len(first), len(second))
	}
	for index := range first {
		if first[index].ID != second[index].ID {
			t.Fatalf("id %q became %q between reads", first[index].ID, second[index].ID)
		}
	}
}
