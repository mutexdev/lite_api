// Only the reserved name is reserved.
//
// The root-level guard matched any name CONTAINING "environments", so
// "My Environments Archive" was refused with a message about a name the user
// had not typed. Only the literal directory "environments" collides with
// bruno's layout.
package core

import (
	"strings"
	"testing"
)

func TestOnlyTheExactReservedFolderNameIsRefused(t *testing.T) {
	for _, name := range []string{"environments", "Environments", "ENVIRONMENTS", "  environments  "} {
		t.Run("refuses "+name, func(t *testing.T) {
			app := newAppForTest(t)
			state, err := app.GetState()
			if err != nil {
				t.Fatal(err)
			}
			collection := state.Workspaces[0].Collections[0]
			if _, err := app.CreateFolder(collection.ID, "", name, name); err == nil {
				t.Fatalf("the reserved root folder name %q was accepted", name)
			}
		})
	}

	for _, name := range []string{"My Environments Archive", "environments-backup", "OldEnvironmentsDump", "environment"} {
		t.Run("accepts "+name, func(t *testing.T) {
			app := newAppForTest(t)
			state, err := app.GetState()
			if err != nil {
				t.Fatal(err)
			}
			collection := state.Workspaces[0].Collections[0]
			if _, err := app.CreateFolder(collection.ID, "", name, name); err != nil {
				t.Fatalf("the folder name %q is not reserved but was refused: %v", name, err)
			}
		})
	}
}

// The reservation is about the root of the collection, where bruno keeps its
// environments directory. Nested folders do not collide with it.
func TestAReservedNameIsAllowedBelowTheRoot(t *testing.T) {
	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	if _, err := app.CreateFolder(collection.ID, "", "Parent", "Parent"); err != nil {
		t.Fatal(err)
	}
	if _, err := app.CreateFolder(collection.ID, "Parent", "environments", "environments"); err != nil {
		if strings.Contains(err.Error(), "reserved") {
			t.Fatalf("a nested folder was refused by the root reservation: %v", err)
		}
		t.Fatal(err)
	}
}
