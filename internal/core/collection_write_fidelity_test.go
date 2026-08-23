// What survives a collection reaching disk (US-060).
//
// The in-memory collection and the files written for it had drifted in two
// places. Folder metadata was never written at all, so a Postman folder's auth
// and scripts existed only in app state -- fine until the collection was
// reopened from its folder, cloned, or handed to somebody through git, at which
// point every request inheriting that auth started inheriting nothing.
// Environments were written to a filename derived from their name with no
// deduplication, so two environments named the same silently became one file.
package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func importPostmanCollectionForTest(t *testing.T, app *App, name, content string) Collection {
	t.Helper()
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	source := CollectionImportSource{ID: name, Name: name + ".json", Content: content}
	preview, err := app.PreviewCollectionImport(CollectionImportPreviewRequest{Sources: []CollectionImportSource{source}})
	if err != nil {
		t.Fatal(err)
	}
	if preview.Rows[0].Error != "" {
		t.Fatalf("preview error: %s", preview.Rows[0].Error)
	}
	result, err := app.ApplyCollectionImport(CollectionImportApplyRequest{
		WorkspaceID: state.Workspaces[0].ID,
		Sources:     []CollectionImportSource{source},
		Selections:  []CollectionImportSelection{{SourceID: name, CandidateID: name + ":collection", ExpectedContentHash: preview.Rows[0].ContentHash}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Applied) != 1 {
		t.Fatalf("result = %#v", result)
	}
	collections := result.State.Workspaces[0].Collections
	return collections[len(collections)-1]
}

func TestImportedFolderAuthAndScriptsReachDisk(t *testing.T) {
	app := newAppForTest(t)
	collection := importPostmanCollectionForTest(t, app, "folders", `{
	  "info": {"name": "Folder metadata"},
	  "item": [{
	    "name": "Admin",
	    "auth": {"type": "bearer", "bearer": [{"key": "token", "value": "folder-token"}]},
	    "event": [{"listen": "prerequest", "script": {"exec": ["console.log('folder pre')"]}}],
	    "item": [{"name": "r", "request": {"method": "GET", "url": "https://example.test"}}]
	  }]
	}`)
	folderFile := filepath.Join(collection.Path, "Admin", "folder.bru")
	data, err := os.ReadFile(folderFile)
	if err != nil {
		t.Fatalf("folder metadata was never written: %v", err)
	}
	body := string(data)
	if !strings.Contains(body, "folder-token") {
		t.Errorf("folder auth missing from %s:\n%s", folderFile, body)
	}
	if !strings.Contains(body, "folder pre") {
		t.Errorf("folder script missing from %s:\n%s", folderFile, body)
	}
}

// The test above proves the bytes are on disk; this one proves they are read
// back, which is the thing that actually matters to a request inheriting them.
func TestImportedFolderMetadataSurvivesReopeningTheFolder(t *testing.T) {
	app := newAppForTest(t)
	imported := importPostmanCollectionForTest(t, app, "reopen", `{
	  "info": {"name": "Reopened"},
	  "item": [{
	    "name": "Admin",
	    "auth": {"type": "bearer", "bearer": [{"key": "token", "value": "folder-token"}]},
	    "item": [{"name": "r", "request": {"method": "GET", "url": "https://example.test"}}]
	  }]
	}`)
	reopened, err := readCollectionFromDisk(imported.Path)
	if err != nil {
		t.Fatal(err)
	}
	if len(reopened.Folders) != 1 {
		t.Fatalf("folders after reopen = %#v", reopened.Folders)
	}
	if reopened.Folders[0].Auth.Mode != "bearer" || reopened.Folders[0].Auth.Token != "folder-token" {
		t.Fatalf("folder auth after reopen = %#v", reopened.Folders[0].Auth)
	}
}

func TestEnvironmentsWithColUnableNamesDoNotOverwriteEachOther(t *testing.T) {
	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	// Two names that sanitise to the same filename, and two that are identical.
	content := `{"name":"Envs","items":[],"environments":[
	  {"name":"a/b","variables":[{"name":"which","value":"first","enabled":true}]},
	  {"name":"a-b","variables":[{"name":"which","value":"second","enabled":true}]},
	  {"name":"Prod","variables":[{"name":"which","value":"third","enabled":true}]},
	  {"name":"Prod","variables":[{"name":"which","value":"fourth","enabled":true}]}
	]}`
	source := CollectionImportSource{ID: "envs", Name: "envs.json", Content: content}
	preview, err := app.PreviewCollectionImport(CollectionImportPreviewRequest{Sources: []CollectionImportSource{source}})
	if err != nil {
		t.Fatal(err)
	}
	if preview.Rows[0].Error != "" {
		t.Fatalf("preview error: %s", preview.Rows[0].Error)
	}
	result, err := app.ApplyCollectionImport(CollectionImportApplyRequest{
		WorkspaceID: state.Workspaces[0].ID,
		Sources:     []CollectionImportSource{source},
		Selections:  []CollectionImportSelection{{SourceID: "envs", CandidateID: "envs:collection", ExpectedContentHash: preview.Rows[0].ContentHash}},
	})
	if err != nil {
		t.Fatal(err)
	}
	collections := result.State.Workspaces[0].Collections
	collection := collections[len(collections)-1]
	if len(collection.Environments) != 4 {
		t.Fatalf("environments in state = %d", len(collection.Environments))
	}
	entries, err := os.ReadDir(filepath.Join(collection.Path, "environments"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 4 {
		names := make([]string, 0, len(entries))
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		t.Fatalf("four environments became %d files: %v", len(entries), names)
	}
	values := map[string]bool{}
	for _, entry := range entries {
		data, err := os.ReadFile(filepath.Join(collection.Path, "environments", entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		for _, which := range []string{"first", "second", "third", "fourth"} {
			if strings.Contains(string(data), which) {
				values[which] = true
			}
		}
	}
	if len(values) != 4 {
		t.Fatalf("an environment was overwritten; found %v", values)
	}
}

// US-060. The content hash guards against the file changing between preview and
// apply. Both the translate and the manual-override paths re-read the source
// after that check and used the new bytes without re-checking, so a file swapped
// in between was imported unverified -- exactly what the guard exists to stop.
func TestOverrideRereadStillHonoursTheContentHash(t *testing.T) {
	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "collection.json")
	original := `{"info":{"name":"Original"},"item":[{"name":"r","request":{"method":"GET","url":"https://original.test"}}]}`
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	source := CollectionImportSource{ID: "swap", Path: path}
	preview, err := app.PreviewCollectionImport(CollectionImportPreviewRequest{Sources: []CollectionImportSource{source}})
	if err != nil {
		t.Fatal(err)
	}
	hash := preview.Rows[0].ContentHash
	swapped := `{"info":{"name":"Swapped"},"item":[{"name":"evil","request":{"method":"GET","url":"https://swapped.test"}}]}`
	if err := os.WriteFile(path, []byte(swapped), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := app.ApplyCollectionImport(CollectionImportApplyRequest{
		WorkspaceID: state.Workspaces[0].ID,
		Sources:     []CollectionImportSource{source},
		Selections:  []CollectionImportSelection{{SourceID: "swap", CandidateID: "swap:collection", ExpectedContentHash: hash, KindOverride: "postman"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Applied) != 0 || len(result.Errors) != 1 {
		t.Fatalf("a source swapped after preview was imported: %#v", result)
	}
	if !strings.Contains(result.Errors[0].Error, "changed") {
		t.Fatalf("error = %q", result.Errors[0].Error)
	}
}
