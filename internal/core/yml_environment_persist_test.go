package core

// Environment edits in an opencollection.yml collection must reach the file.
//
// CreateEnvironment and UpdateEnvironmentVariables both skipped the write when
// collection.Format was "yml", on the premise that opencollection.yml has no
// environments to write. It does — yamlstore.StringifyCollection serialises
// them and the disk reader reads them back — so the edit lived in memory until
// the next read of the file replaced it with what was still there. The user got
// no error and no environment.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func openYMLCollectionForTest(t *testing.T, app *App) Collection {
	t.Helper()
	root := t.TempDir()
	path := filepath.Join(root, "Env YML")
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "opencollection.yml"), []byte(`opencollection: 1.0.0
info:
  name: Env YML
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "ping.yml"), []byte(`info:
  name: Ping
  type: http
  seq: 1
http:
  method: GET
  url: https://example.test/ping
`), 0o600); err != nil {
		t.Fatal(err)
	}
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	state, err = app.OpenCollection(state.Workspaces[0].ID, path)
	if err != nil {
		t.Fatalf("OpenCollection: %v", err)
	}
	collection := state.Workspaces[0].Collections[len(state.Workspaces[0].Collections)-1]
	if collection.Format != "yml" {
		t.Fatalf("fixture collection is %q, expected yml", collection.Format)
	}
	return collection
}

func TestCreateEnvironmentPersistsInYMLCollections(t *testing.T) {
	app := newAppForTest(t)
	collection := openYMLCollectionForTest(t, app)

	if _, err := app.CreateEnvironment(collection.ID, "Staging"); err != nil {
		t.Fatalf("CreateEnvironment: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(collection.Path, "opencollection.yml"))
	if err != nil {
		t.Fatalf("read opencollection.yml: %v", err)
	}
	if !strings.Contains(string(data), "Staging") {
		t.Fatalf("the new environment never reached opencollection.yml, so the next read of the file discards it:\n%s", data)
	}

	// The reader is the authority on whether the write survives: an edit that
	// does not come back from disk is the failure this is about.
	reread, err := readCollectionFromDisk(collection.Path)
	if err != nil {
		t.Fatalf("readCollectionFromDisk: %v", err)
	}
	if len(reread.Environments) != 1 || reread.Environments[0].Name != "Staging" {
		t.Fatalf("the environment did not survive a round trip through disk: %#v", reread.Environments)
	}
}

func TestUpdateEnvironmentVariablesPersistsInYMLCollections(t *testing.T) {
	app := newAppForTest(t)
	collection := openYMLCollectionForTest(t, app)

	state, err := app.CreateEnvironment(collection.ID, "Staging")
	if err != nil {
		t.Fatalf("CreateEnvironment: %v", err)
	}
	created := collectionInState(t, state, collection.ID)
	environmentID := created.Environments[0].ID

	vars := []Variable{{ID: newID("var"), Name: "host", Value: "https://staging.example.test", DataType: "string", Enabled: true}}
	if _, err := app.UpdateEnvironmentVariables(collection.ID, environmentID, vars); err != nil {
		t.Fatalf("UpdateEnvironmentVariables: %v", err)
	}

	reread, err := readCollectionFromDisk(collection.Path)
	if err != nil {
		t.Fatalf("readCollectionFromDisk: %v", err)
	}
	if len(reread.Environments) != 1 {
		t.Fatalf("expected one environment on disk, got %#v", reread.Environments)
	}
	found := false
	for _, variable := range reread.Environments[0].Variables {
		if variable.Name != "host" {
			continue
		}
		found = true
		if value, _ := variable.Value.(string); value != "https://staging.example.test" {
			t.Fatalf("the variable edit did not reach disk: %#v", variable)
		}
	}
	if !found {
		t.Fatalf("the variable edit did not reach disk at all: %#v", reread.Environments[0].Variables)
	}
}

func collectionInState(t *testing.T, state AppState, collectionID string) Collection {
	t.Helper()
	for _, workspace := range state.Workspaces {
		for _, collection := range workspace.Collections {
			if collection.ID == collectionID {
				return collection
			}
		}
	}
	t.Fatalf("collection %s was not found in state", collectionID)
	return Collection{}
}
