// The Postman files people actually try to import (US-058).
//
// The import panel recognised exactly one of the several documents Postman
// exports. An environment, a globals file, a v1 collection and a "data dump"
// all landed on "source is ambiguous; choose an import kind manually" -- and no
// entry in that kind list could import any of them, so the advice led nowhere.
// A dump is a .zip, which the file picker offered in its filter and then
// refused outright.
package core

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const postmanEnvironmentFixture = `{
  "id": "3f7d0a12",
  "name": "Staging",
  "values": [
    {"key": "base_url", "value": "https://staging.example.test", "enabled": true},
    {"key": "token", "value": "s3cret", "enabled": true, "type": "secret"},
    {"key": "retired", "value": "x", "enabled": false}
  ],
  "_postman_variable_scope": "environment"
}`

const postmanGlobalsFixture = `{
  "id": "9a1",
  "name": "My Workspace Globals",
  "values": [{"key": "shared", "value": "1", "enabled": true}],
  "_postman_variable_scope": "globals"
}`

func TestPostmanEnvironmentIsDetectedRatherThanCalledAmbiguous(t *testing.T) {
	for name, content := range map[string]string{
		"environment": postmanEnvironmentFixture,
		"globals":     postmanGlobalsFixture,
	} {
		kind, _, _, err := detectCollectionImport(content, name+".postman_environment.json", "")
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if kind != "postman-environment" {
			t.Fatalf("%s detected as %q", name, kind)
		}
	}
}

func TestPostmanEnvironmentPreviewNamesItsDestination(t *testing.T) {
	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	source := CollectionImportSource{ID: "env", Name: "staging.postman_environment.json", Content: postmanEnvironmentFixture}
	// The panel always previews against a destination workspace; the
	// destination column is only filled in when one is known.
	preview, err := app.PreviewCollectionImport(CollectionImportPreviewRequest{WorkspaceID: state.Workspaces[0].ID, Sources: []CollectionImportSource{source}})
	if err != nil {
		t.Fatal(err)
	}
	row := preview.Rows[0]
	if row.Error != "" {
		t.Fatalf("row error = %q", row.Error)
	}
	if row.DetectedKind != "postman-environment" || !row.DefaultSelect {
		t.Fatalf("row = %#v", row)
	}
	if len(row.Environments) != 1 || row.Environments[0].Name != "Staging" {
		t.Fatalf("environments = %#v", row.Environments)
	}
	if len(row.RequestIDs) != 0 {
		t.Fatalf("an environment reported requests: %#v", row.RequestIDs)
	}
	if !strings.Contains(strings.ToLower(row.DestinationPath), "global") {
		t.Fatalf("destination does not say where it goes: %q", row.DestinationPath)
	}
}

func TestPostmanEnvironmentImportsIntoWorkspaceGlobals(t *testing.T) {
	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	source := CollectionImportSource{ID: "env", Name: "staging.postman_environment.json", Content: postmanEnvironmentFixture}
	preview, err := app.PreviewCollectionImport(CollectionImportPreviewRequest{Sources: []CollectionImportSource{source}})
	if err != nil {
		t.Fatal(err)
	}
	before := len(state.Workspaces[0].GlobalEnvironments)
	result, err := app.ApplyCollectionImport(CollectionImportApplyRequest{
		WorkspaceID: state.Workspaces[0].ID,
		Sources:     []CollectionImportSource{source},
		Selections:  []CollectionImportSelection{{SourceID: "env", CandidateID: "env:collection", ExpectedContentHash: preview.Rows[0].ContentHash}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Applied) != 1 || len(result.Errors) != 0 {
		t.Fatalf("result = %#v", result)
	}
	environments := result.State.Workspaces[0].GlobalEnvironments
	if len(environments) != before+1 {
		t.Fatalf("global environments = %#v", environments)
	}
	imported := environments[len(environments)-1]
	if imported.Name != "Staging" || len(imported.Variables) != 3 {
		t.Fatalf("imported = %#v", imported)
	}
	if imported.Variables[2].Enabled {
		t.Fatal("a disabled Postman value imported enabled")
	}
	// No collection folder should have appeared for an environment file.
	if len(result.State.Workspaces[0].Collections) != len(state.Workspaces[0].Collections) {
		t.Fatal("importing an environment created a collection")
	}
	restarted := newAppInDirForTest(t, app.dataDir)
	restored, err := restarted.GetState()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, env := range restored.Workspaces[0].GlobalEnvironments {
		if env.Name == "Staging" {
			found = true
		}
	}
	if !found {
		t.Fatal("the imported environment did not survive a relaunch")
	}
}

func TestPostmanEnvironmentNameCollisionDoesNotOverwrite(t *testing.T) {
	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	workspaceID := state.Workspaces[0].ID
	apply := func(id string) CollectionImportApplyResult {
		t.Helper()
		source := CollectionImportSource{ID: id, Name: "staging.postman_environment.json", Content: postmanEnvironmentFixture}
		preview, err := app.PreviewCollectionImport(CollectionImportPreviewRequest{Sources: []CollectionImportSource{source}})
		if err != nil {
			t.Fatal(err)
		}
		result, err := app.ApplyCollectionImport(CollectionImportApplyRequest{
			WorkspaceID: workspaceID,
			Sources:     []CollectionImportSource{source},
			Selections:  []CollectionImportSelection{{SourceID: id, CandidateID: id + ":collection", ExpectedContentHash: preview.Rows[0].ContentHash}},
		})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	apply("first")
	result := apply("second")
	names := map[string]int{}
	for _, env := range result.State.Workspaces[0].GlobalEnvironments {
		names[env.Name]++
	}
	if names["Staging"] != 1 {
		t.Fatalf("the second import overwrote or duplicated the first: %#v", names)
	}
	total := 0
	for _, count := range names {
		total += count
	}
	if total < 2 {
		t.Fatalf("the second import vanished: %#v", names)
	}
}

func TestPostmanV1CollectionSaysWhatToDoAboutIt(t *testing.T) {
	content := `{"id":"a","name":"Old","order":["1"],"requests":[{"id":"1","name":"r","url":"https://example.test","method":"GET"}]}`
	kind, _, _, err := detectCollectionImport(content, "old.json", "")
	if kind != "postman-v1" || err == nil {
		t.Fatalf("kind=%q err=%v", kind, err)
	}
	message := collectionImportDiagnostic(err)
	if !strings.Contains(message, "v1") || !strings.Contains(strings.ToLower(message), "v2.1") {
		t.Fatalf("message does not say what to do: %q", message)
	}
}

func TestPostmanDataDumpExpandsIntoItsContents(t *testing.T) {
	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	dump := `{"collections":[
	  {"info":{"name":"Alpha"},"item":[{"name":"a","request":{"method":"GET","url":"https://a.test"}}]},
	  {"info":{"name":"Beta"},"item":[{"name":"b","request":{"method":"GET","url":"https://b.test"}}]}
	],"environments":[` + postmanEnvironmentFixture + `]}`
	source := CollectionImportSource{ID: "dump", Name: "backup.postman_dump.json", Content: dump}
	preview, err := app.PreviewCollectionImport(CollectionImportPreviewRequest{Sources: []CollectionImportSource{source}})
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Rows) != 3 {
		t.Fatalf("dump produced %d rows: %#v", len(preview.Rows), preview.Rows)
	}
	names := map[string]bool{}
	for _, row := range preview.Rows {
		if row.Error != "" {
			t.Fatalf("row %q errored: %s", row.CollectionName, row.Error)
		}
		names[row.CollectionName] = true
	}
	if !names["Alpha"] || !names["Beta"] {
		t.Fatalf("dump rows = %#v", names)
	}
	selections := make([]CollectionImportSelection, 0, len(preview.Rows))
	for _, row := range preview.Rows {
		selections = append(selections, CollectionImportSelection{SourceID: row.SourceID, CandidateID: row.CandidateID, ExpectedContentHash: row.ContentHash})
	}
	result, err := app.ApplyCollectionImport(CollectionImportApplyRequest{
		WorkspaceID: state.Workspaces[0].ID,
		Sources:     []CollectionImportSource{source},
		Selections:  selections,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Applied) != 3 || len(result.Errors) != 0 {
		t.Fatalf("result = %#v", result)
	}
}

func writePostmanZip(t *testing.T, entries map[string]string) string {
	t.Helper()
	var buffer bytes.Buffer
	archive := zip.NewWriter(&buffer)
	for name, content := range entries {
		writer, err := archive.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "export.zip")
	if err := os.WriteFile(path, buffer.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestPostmanZipExportImportsItsCollections(t *testing.T) {
	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	path := writePostmanZip(t, map[string]string{
		"Alpha.postman_collection.json":    `{"info":{"name":"Alpha"},"item":[{"name":"a","request":{"method":"GET","url":"https://a.test"}}]}`,
		"Staging.postman_environment.json": postmanEnvironmentFixture,
		"readme.txt":                       "not an import",
	})
	source := CollectionImportSource{ID: "zip", Path: path}
	preview, err := app.PreviewCollectionImport(CollectionImportPreviewRequest{Sources: []CollectionImportSource{source}})
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Rows) != 2 {
		t.Fatalf("zip produced %d rows: %#v", len(preview.Rows), preview.Rows)
	}
	selections := make([]CollectionImportSelection, 0, len(preview.Rows))
	for _, row := range preview.Rows {
		if row.Error != "" {
			t.Fatalf("row %q errored: %s", row.SourceName, row.Error)
		}
		selections = append(selections, CollectionImportSelection{SourceID: row.SourceID, CandidateID: row.CandidateID, ExpectedContentHash: row.ContentHash})
	}
	result, err := app.ApplyCollectionImport(CollectionImportApplyRequest{
		WorkspaceID: state.Workspaces[0].ID,
		Sources:     []CollectionImportSource{source},
		Selections:  selections,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Applied) != 2 {
		t.Fatalf("result = %#v", result)
	}
}

func TestZipWithNothingImportableSaysSo(t *testing.T) {
	app := newAppForTest(t)
	path := writePostmanZip(t, map[string]string{"notes.txt": "hello", "image.png": "\x89PNG"})
	preview, err := app.PreviewCollectionImport(CollectionImportPreviewRequest{Sources: []CollectionImportSource{{ID: "zip", Path: path}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Rows) != 1 || preview.Rows[0].Error == "" {
		t.Fatalf("rows = %#v", preview.Rows)
	}
	if !strings.Contains(strings.ToLower(preview.Rows[0].Error), "zip") {
		t.Fatalf("error = %q", preview.Rows[0].Error)
	}
}

// The detection matrix used to cover four kinds. Every branch of
// detectCollectionImport is exercised here so a new branch cannot be added
// without a case, and an existing one cannot be quietly reordered away.
const harMatrixFixture = `{"log":{"version":"1.2","entries":[{"request":{"method":"GET","url":"https://e.test/x","headers":[],"queryString":[]},"response":{"status":200,"headers":[],"content":{}}}]}}`

func TestCollectionImportDetectionCoversEveryKind(t *testing.T) {
	cases := []struct {
		name, fileName, content, wantKind string
		wantErr                           bool
	}{
		{"postman", "c.json", `{"info":{"name":"P"},"item":[{"name":"r","request":{"method":"GET","url":"https://e.test"}}]}`, "postman", false},
		{"postman environment", "e.json", postmanEnvironmentFixture, "postman-environment", false},
		{"postman globals", "g.json", postmanGlobalsFixture, "postman-environment", false},
		{"postman v1", "v1.json", `{"id":"a","name":"Old","requests":[{"id":"1","name":"r","url":"https://e.test","method":"GET"}]}`, "postman-v1", true},
		{"insomnia", "i.json", `{"_type":"export","resources":[{"_id":"wrk_1","_type":"workspace","name":"W"},{"_id":"req_1","_type":"request","parentId":"wrk_1","name":"r","method":"GET","url":"https://e.test"}]}`, "insomnia", false},
		{"openapi", "o.yaml", "openapi: 3.0.0\ninfo: {title: P, version: 1}\npaths: {/pets: {get: {responses: {'200': {description: ok}}}}}\n", "openapi", false},
		{"swagger 2", "s.json", `{"swagger":"2.0","info":{"title":"t","version":"1"},"paths":{"/pets":{"get":{"responses":{"200":{"description":"ok"}}}}}}`, "swagger-2", false},
		{"har", "s.har", harMatrixFixture, "har", false},
		{"har as json", "s.json", harMatrixFixture, "har", false},
		{"curl", "c.txt", "curl https://example.test", "curl", false},
		{"bru", "r.bru", "meta {\n  name: r\n  type: http\n}\n\nget {\n  url: https://e.test\n}\n", "bru", false},
		{"wsdl", "s.wsdl", "<definitions/>", "wsdl", true},
		{"yaak", "yaak-export.json", `{"type":"yaak"}`, "yaak", true},
		{"unknown", "x.json", `{"hello":"world"}`, "unknown", true},
		{"not structured", "x.json", "just some prose", "unknown", true},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			kind, _, _, err := detectCollectionImport(testCase.content, testCase.fileName, "")
			if kind != testCase.wantKind || (err != nil) != testCase.wantErr {
				t.Fatalf("kind=%q err=%v", kind, err)
			}
		})
	}
}
