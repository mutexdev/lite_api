package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Creating a request INSIDE a folder, including a folder nested in another.
//
// The sidebar has always been able to create folders under folders — CreateFolder
// takes a parent path — but every request landed at the collection root, because
// CreateRequest had nowhere to put a folder. So a nested tree could be built and
// then not filled in, which is the half of the feature users actually notice.
//
// The file path is the part worth asserting rather than just the field:
// uniqueRequestFilePath already joins item.FolderPath onto the collection path,
// so setting the field is what puts the .bru/.yml on disk in the right
// directory. Checking only FolderPath would pass even if the write went to the
// root.

func createFolderForTest(t *testing.T, app *App, collectionID, parent, name string) {
	t.Helper()
	if _, err := app.CreateFolder(collectionID, parent, name, name); err != nil {
		t.Fatalf("create folder %q under %q: %v", name, parent, err)
	}
}

func TestCreateRequestInFolderPlacesItemAndFileInsideTheFolder(t *testing.T) {
	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]

	createFolderForTest(t, app, collection.ID, "", "auth")

	state, err = app.CreateRequestInFolder(collection.ID, "http", "Login", "auth")
	if err != nil {
		t.Fatal(err)
	}
	collection = state.Workspaces[0].Collections[0]
	item := collection.Items[len(collection.Items)-1]

	if item.FolderPath != "auth" {
		t.Fatalf("FolderPath = %q, want %q", item.FolderPath, "auth")
	}

	wantDir := filepath.Join(collection.Path, "auth")
	if gotDir := filepath.Dir(item.FilePath); gotDir != wantDir {
		t.Fatalf("request file landed in %q, want %q", gotDir, wantDir)
	}
}

// A FOLDER INSIDE A FOLDER, with a request at the bottom. This is the case the
// user asked for in as many words, and the one that exercises the path join
// twice rather than once.
func TestCreateRequestInNestedFolder(t *testing.T) {
	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]

	createFolderForTest(t, app, collection.ID, "", "api")
	createFolderForTest(t, app, collection.ID, "api", "v2")

	state, err = app.CreateRequestInFolder(collection.ID, "http", "List Users", "api/v2")
	if err != nil {
		t.Fatal(err)
	}
	collection = state.Workspaces[0].Collections[0]
	item := collection.Items[len(collection.Items)-1]

	if item.FolderPath != "api/v2" {
		t.Fatalf("FolderPath = %q, want %q", item.FolderPath, "api/v2")
	}
	wantDir := filepath.Join(collection.Path, "api", "v2")
	if gotDir := filepath.Dir(item.FilePath); gotDir != wantDir {
		t.Fatalf("request file landed in %q, want %q", gotDir, wantDir)
	}
	if _, statErr := os.Stat(wantDir); statErr != nil {
		t.Fatalf("nested directory was not created on disk: %v", statErr)
	}
}

// An unknown folder is REFUSED rather than silently demoted to the root. A
// request that quietly appears somewhere other than where it was asked for is
// worse than an error, because nothing tells the user to go looking.
func TestCreateRequestInFolderRejectsAnUnknownFolder(t *testing.T) {
	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	before := len(collection.Items)

	if _, err := app.CreateRequestInFolder(collection.ID, "http", "Nowhere", "does/not/exist"); err == nil {
		t.Fatal("expected an error for a folder that does not exist")
	}

	state, err = app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	if got := len(state.Workspaces[0].Collections[0].Items); got != before {
		t.Fatalf("a failed create still added an item: %d -> %d", before, got)
	}
}

// The old three-argument entry point keeps meaning exactly what it meant: create
// at the root. 100+ call sites depend on that and none of them were touched.
func TestCreateRequestStillCreatesAtTheRoot(t *testing.T) {
	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]

	createFolderForTest(t, app, collection.ID, "", "auth")

	state, err = app.CreateRequest(collection.ID, "http", "Root Level")
	if err != nil {
		t.Fatal(err)
	}
	collection = state.Workspaces[0].Collections[0]
	item := collection.Items[len(collection.Items)-1]

	if strings.TrimSpace(item.FolderPath) != "" {
		t.Fatalf("FolderPath = %q, want empty", item.FolderPath)
	}
	if gotDir := filepath.Dir(item.FilePath); gotDir != filepath.Clean(collection.Path) {
		t.Fatalf("request file landed in %q, want the collection root %q", gotDir, collection.Path)
	}
}
