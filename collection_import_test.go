package main

import (
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

func TestCollectionImportPickerHelpersHandleCancelAndLimits(t *testing.T) {
	cancelled, err := chooseCollectionImportFiles(func(wailsruntime.OpenDialogOptions) ([]string, error) { return nil, nil })
	if err != nil || !cancelled.Cancelled || len(cancelled.Paths) != 0 {
		t.Fatalf("cancelled picker = %#v, %v", cancelled, err)
	}
	failure := errors.New("dialog unavailable")
	if _, err := chooseCollectionImportFolder(func(wailsruntime.OpenDialogOptions) (string, error) { return "", failure }); !errors.Is(err, failure) {
		t.Fatalf("folder error = %v", err)
	}
	paths := make([]string, collectionImportMaxFiles+1)
	if _, err := chooseCollectionImportFiles(func(wailsruntime.OpenDialogOptions) ([]string, error) { return paths, nil }); err == nil {
		t.Fatal("expected max-file rejection")
	}
}

func TestCollectionImportDetectionMatrix(t *testing.T) {
	cases := []struct {
		name, content, wantKind string
		wantErr                 bool
	}{
		{"postman", `{"info":{"name":"Postman"},"item":[{"name":"Ping","request":{"method":"GET","url":"https://example.test"}}]}`, "postman", false},
		{"openapi yaml", "openapi: 3.0.0\ninfo: {title: Pets, version: 1}\npaths: {/pets: {get: {responses: {'200': {description: ok}}}}}\n", "openapi", false},
		{"swagger", `{"swagger":"2.0","info":{"title":"old"}}`, "swagger-2", true},
		{"unknown", `{"hello":"world"}`, "unknown", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			kind, _, _, err := detectCollectionImport(tc.content, tc.name+".json", "")
			if kind != tc.wantKind || (err != nil) != tc.wantErr {
				t.Fatalf("kind=%q err=%v", kind, err)
			}
		})
	}
}

func TestCollectionImportRejectsDuplicateSourceIDs(t *testing.T) {
	sources := []CollectionImportSource{
		{ID: "same", Name: "one.json", Content: `{"info":{"name":"One"},"item":[]}`},
		{ID: "same", Name: "two.json", Content: `{"info":{"name":"Two"},"item":[]}`},
	}
	if _, err := previewCollectionImport(CollectionImportPreviewRequest{Sources: sources}); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("preview error = %v", err)
	}
	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.ApplyCollectionImport(CollectionImportApplyRequest{WorkspaceID: state.Workspaces[0].ID, Sources: sources}); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("apply error = %v", err)
	}
}

func TestCollectionImportManualOverrideRescuesDetectionError(t *testing.T) {
	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	source := CollectionImportSource{ID: "misnamed", Name: "postman-backup.zip", Content: `{"info":{"name":"Rescued"},"item":[{"name":"Ping","request":{"method":"GET","url":"https://example.test"}}]}`}
	preview, err := app.PreviewCollectionImport(CollectionImportPreviewRequest{Sources: []CollectionImportSource{source}})
	if err != nil || len(preview.Rows) != 1 || preview.Rows[0].DetectedKind != "zip" || preview.Rows[0].Error == "" {
		t.Fatalf("preview=%#v err=%v", preview, err)
	}
	result, err := app.ApplyCollectionImport(CollectionImportApplyRequest{WorkspaceID: state.Workspaces[0].ID, Sources: []CollectionImportSource{source}, Selections: []CollectionImportSelection{{SourceID: source.ID, CandidateID: source.ID + ":collection", ExpectedContentHash: preview.Rows[0].ContentHash, KindOverride: "postman"}}})
	if err != nil || len(result.Applied) != 1 || len(result.Errors) != 0 {
		t.Fatalf("override result=%#v err=%v", result, err)
	}
	if result.Applied[0].DetectedKind != "postman" || len(result.Applied[0].Requests) != 1 {
		t.Fatalf("override did not publish Postman summary: %#v", result.Applied[0])
	}
}

func TestCollectionImportPreviewHasNamedHierarchyAndDestination(t *testing.T) {
	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	source := CollectionImportSource{ID: "hierarchy", Name: "hierarchy.json", Content: `{"info":{"name":"Hierarchy"},"item":[{"name":"Users","item":[{"name":"List users","request":{"method":"GET","url":"https://example.test/users"}}]}]}`}
	preview, err := app.PreviewCollectionImport(CollectionImportPreviewRequest{WorkspaceID: state.Workspaces[0].ID, Sources: []CollectionImportSource{source}})
	if err != nil || len(preview.Rows) != 1 {
		t.Fatalf("preview=%#v err=%v", preview, err)
	}
	row := preview.Rows[0]
	if row.Conflict != "none" || row.DestinationPath == "" || len(row.Folders) != 1 || len(row.Requests) != 1 {
		t.Fatalf("destination/hierarchy summary=%#v", row)
	}
	if row.Folders[0].Name != "Users" || row.Requests[0].Name != "List users" || row.Requests[0].Method != "GET" || row.Requests[0].FolderPath == "" || row.Folders[0].SelectionID == "" || row.Requests[0].SelectionID == "" {
		t.Fatalf("named summary=%#v", row)
	}
	result, err := app.ApplyCollectionImport(CollectionImportApplyRequest{WorkspaceID: state.Workspaces[0].ID, Sources: []CollectionImportSource{source}, Selections: []CollectionImportSelection{{SourceID: source.ID, CandidateID: source.ID + ":collection", ExpectedContentHash: row.ContentHash}}})
	if err != nil || len(result.Applied) != 1 {
		t.Fatalf("apply result=%#v err=%v", result, err)
	}
	applied := result.Applied[0]
	if len(applied.FolderIDs) != len(applied.Folders) || len(applied.RequestIDs) != len(applied.Requests) || len(applied.Folders) != 1 || len(applied.Requests) != 1 {
		t.Fatalf("apply duplicated preview summary: %#v", applied)
	}
}

func TestCollectionImportRejectsDuplicateSelectionsAndUnknownConflictAction(t *testing.T) {
	for name, selections := range map[string]func(CollectionImportSource, string) []CollectionImportSelection{
		"duplicate": func(source CollectionImportSource, hash string) []CollectionImportSelection {
			selection := CollectionImportSelection{SourceID: source.ID, CandidateID: source.ID + ":collection", ExpectedContentHash: hash}
			return []CollectionImportSelection{selection, selection}
		},
		"unknown action": func(source CollectionImportSource, hash string) []CollectionImportSelection {
			return []CollectionImportSelection{{SourceID: source.ID, CandidateID: source.ID + ":collection", ExpectedContentHash: hash, ConflictAction: "surprise"}}
		},
	} {
		t.Run(name, func(t *testing.T) {
			app := newAppForTest(t)
			state, err := app.GetState()
			if err != nil {
				t.Fatal(err)
			}
			source := CollectionImportSource{ID: "once", Name: "once.json", Content: `{"info":{"name":"Only once"},"item":[]}`}
			_, err = app.ApplyCollectionImport(CollectionImportApplyRequest{WorkspaceID: state.Workspaces[0].ID, Sources: []CollectionImportSource{source}, Selections: selections(source, hashCollectionImportBytes([]byte(source.Content)))})
			if err == nil {
				t.Fatal("expected pre-mutation validation error")
			}
			after, getErr := app.GetState()
			if getErr != nil || len(after.Workspaces[0].Collections) != len(state.Workspaces[0].Collections) {
				t.Fatalf("invalid selection mutated state: %#v err=%v", after, getErr)
			}
			if _, statErr := os.Stat(filepath.Join(after.Workspaces[0].Path, "Only once")); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("invalid selection created output: %v", statErr)
			}
		})
	}
}

func TestCollectionImportPreviewApplyAndRelaunch(t *testing.T) {
	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	postman := `{"info":{"name":"Unicode 集合"},"item":[{"name":"Ping","request":{"method":"GET","url":"https://example.test"}}]}`
	request := CollectionImportPreviewRequest{Sources: []CollectionImportSource{{ID: "valid", Name: "集合.postman.json", Content: postman}, {ID: "invalid", Name: "unknown.json", Content: `{"hello":true}`}}}
	preview, err := app.PreviewCollectionImport(request)
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Rows) != 2 || !preview.Rows[0].DefaultSelect || preview.Rows[1].Error == "" || strings.Contains(preview.Rows[1].Error, "hello") {
		t.Fatalf("preview = %#v", preview.Rows)
	}
	result, err := app.ApplyCollectionImport(CollectionImportApplyRequest{WorkspaceID: state.Workspaces[0].ID, Sources: request.Sources, Selections: []CollectionImportSelection{{SourceID: "valid", CandidateID: "valid:collection", ExpectedContentHash: preview.Rows[0].ContentHash}, {SourceID: "invalid", CandidateID: "invalid:collection", ExpectedContentHash: preview.Rows[1].ContentHash}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Applied) != 1 || len(result.Errors) != 1 {
		t.Fatalf("result = %#v", result)
	}
	imported := result.State.Workspaces[0].Collections[len(result.State.Workspaces[0].Collections)-1]
	if !fileExists(filepath.Join(imported.Path, "bruno.json")) || !fileExists(filepath.Join(imported.Path, "Ping.bru")) {
		t.Fatalf("import was not materialized: %#v", imported)
	}
	if len(imported.Items) != 1 || !fileExists(imported.Items[0].FilePath) || !pathInside(imported.Path, imported.Items[0].FilePath) {
		t.Fatalf("staging request path leaked into published state: %#v", imported.Items)
	}
	if _, err := app.SaveRequest(imported.ID, imported.Items[0].ID); err != nil {
		t.Fatalf("immediate save after import failed: %v", err)
	}
	restarted := newAppInDirForTest(t, app.dataDir)
	restored, err := restarted.GetState()
	if err != nil {
		t.Fatal(err)
	}
	if !collectionExists(restored.Workspaces[0], imported.Path) {
		t.Fatalf("materialized import did not survive relaunch: %#v", restored.Workspaces[0].Collections)
	}
	assertNoImportScratchDirs(t, filepath.Dir(imported.Path))
}

func TestCollectionImportFolderPreviewDoesNotCopyOrMutate(t *testing.T) {
	root := t.TempDir()
	folder := filepath.Join(root, "Existing")
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(folder, "bruno.json"), []byte(`{"name":"Existing","type":"collection"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(folder, "Request.bru"), []byte("meta {\n  name: Request\n  type: http\n  seq: 1\n}\nget {\n  url: https://example.test\n}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(folder, "bruno.json"))
	if err != nil {
		t.Fatal(err)
	}
	preview, err := previewCollectionImport(CollectionImportPreviewRequest{Sources: []CollectionImportSource{{Path: folder}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Rows) != 1 || !preview.Rows[0].ExistingFolder || preview.Rows[0].DetectedKind != "collection-folder" {
		t.Fatalf("preview = %#v", preview.Rows)
	}
	after, err := os.ReadFile(filepath.Join(folder, "bruno.json"))
	if err != nil || string(before) != string(after) {
		t.Fatalf("folder mutated: %v", err)
	}
}

func TestCollectionImportRecognizesOpenCollectionYAMLFolder(t *testing.T) {
	folder := t.TempDir()
	if err := os.WriteFile(filepath.Join(folder, "opencollection.yaml"), []byte("info:\n  name: YAML collection\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !collectionImportFolderLooksSupported(folder) {
		t.Fatal("opencollection.yaml folder was not recognized")
	}
	if collection, err := readCollectionFromDisk(folder); err != nil || collection.Format != "yml" {
		t.Fatalf("opencollection.yaml folder read=%#v err=%v", collection, err)
	}
}

func TestCollectionImportFolderHashIgnoresNoiseAndRejectsStaleSelection(t *testing.T) {
	root := t.TempDir()
	folder := filepath.Join(root, "Existing")
	if err := os.MkdirAll(filepath.Join(folder, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(folder, "node_modules", "ignored"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(folder, "bruno.json"), []byte(`{"name":"Existing","type":"collection"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	requestPath := filepath.Join(folder, "Request.bru")
	request := []byte("meta {\n  name: Request\n  type: http\n  seq: 1\n}\nget {\n  url: https://example.test\n}\n")
	if err := os.WriteFile(requestPath, request, 0o600); err != nil {
		t.Fatal(err)
	}
	firstHash, err := hashCollectionImportFolder(folder)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(folder, ".git", "config"), []byte("noise"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(folder, "node_modules", "ignored", "index.js"), []byte("noise"), 0o600); err != nil {
		t.Fatal(err)
	}
	if hash, err := hashCollectionImportFolder(folder); err != nil || hash != firstHash {
		t.Fatalf("noise changed folder hash: hash=%q err=%v", hash, err)
	}
	preview, err := previewCollectionImport(CollectionImportPreviewRequest{Sources: []CollectionImportSource{{ID: "folder", Path: folder}}})
	if err != nil || len(preview.Rows) != 1 || preview.Rows[0].Error != "" {
		t.Fatalf("preview=%#v err=%v", preview, err)
	}
	if err := os.WriteFile(requestPath, append(request, []byte("# changed\n")...), 0o600); err != nil {
		t.Fatal(err)
	}
	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	result, err := app.ApplyCollectionImport(CollectionImportApplyRequest{WorkspaceID: state.Workspaces[0].ID, Sources: []CollectionImportSource{{ID: "folder", Path: folder}}, Selections: []CollectionImportSelection{{SourceID: "folder", CandidateID: "folder:collection", ExpectedContentHash: preview.Rows[0].ContentHash}}})
	if err != nil || len(result.Applied) != 0 || len(result.Errors) != 1 {
		t.Fatalf("stale folder result=%#v err=%v", result, err)
	}
}

func TestCollectionImportDetectsChangedSourceAtApply(t *testing.T) {
	path := filepath.Join(t.TempDir(), "source.json")
	valid := `{"info":{"name":"Before"},"item":[]}`
	if err := os.WriteFile(path, []byte(valid), 0o600); err != nil {
		t.Fatal(err)
	}
	preview, err := previewCollectionImport(CollectionImportPreviewRequest{Sources: []CollectionImportSource{{ID: "source", Path: path}}})
	if err != nil {
		t.Fatal(err)
	}
	if preview.Rows[0].Error != "" {
		t.Fatal(preview.Rows[0].Error)
	}
	if err := os.WriteFile(path, []byte(`{"broken":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	app := newAppForTest(t)
	state, _ := app.GetState()
	result, err := app.ApplyCollectionImport(CollectionImportApplyRequest{WorkspaceID: state.Workspaces[0].ID, Sources: []CollectionImportSource{{ID: "source", Path: path}}, Selections: []CollectionImportSelection{{SourceID: "source", CandidateID: "source:collection", ExpectedContentHash: preview.Rows[0].ContentHash}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Applied) != 0 || len(result.Errors) != 1 {
		t.Fatalf("stale source was applied: %#v", result)
	}
}

func TestCollectionImportConflictActionsAndLegacyMaterialization(t *testing.T) {
	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	source := CollectionImportSource{ID: "source", Name: "source.json", Content: `{"info":{"name":"Conflict"},"item":[{"name":"One","request":{"method":"GET","url":"https://one.test"}}]}`}
	apply := func(action string) CollectionImportApplyResult {
		result, err := app.ApplyCollectionImport(CollectionImportApplyRequest{WorkspaceID: state.Workspaces[0].ID, Sources: []CollectionImportSource{source}, Selections: []CollectionImportSelection{{SourceID: "source", CandidateID: "source:collection", ConflictAction: action, ExpectedContentHash: hashCollectionImportBytes([]byte(source.Content))}}})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	first := apply("")
	if len(first.Applied) != 1 {
		t.Fatalf("first import: %#v", first)
	}
	if skipped := apply("skip"); len(skipped.Skipped) != 1 {
		t.Fatalf("skip: %#v", skipped)
	}
	if renamed := apply("rename"); len(renamed.Applied) != 1 || renamed.Applied[0].CollectionName != "Conflict 2" {
		t.Fatalf("rename: %#v", renamed)
	}
	source.Content = `{"info":{"name":"Conflict"},"item":[{"name":"Replacement","request":{"method":"GET","url":"https://replacement.test"}}]}`
	if replaced := apply("replace"); len(replaced.Applied) != 1 {
		t.Fatalf("replace: %#v", replaced)
	}
	latest, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	var replaced Collection
	for _, collection := range latest.Workspaces[0].Collections {
		if collection.Name == "Conflict" {
			replaced = collection
			break
		}
	}
	if len(replaced.Items) != 1 || replaced.Items[0].Name != "Replacement" {
		t.Fatalf("replacement not published: %#v", replaced)
	}
	assertNoImportScratchDirs(t, filepath.Dir(replaced.Path))

	legacy, err := app.ImportCollection(state.Workspaces[0].ID, ImportPayload{Kind: "postman", Name: "Legacy", Content: `{"info":{"name":"Legacy"},"item":[{"name":"Saved","request":{"method":"GET","url":"https://saved.test"}}]}`})
	if err != nil {
		t.Fatal(err)
	}
	legacyCollection := legacy.Workspaces[0].Collections[len(legacy.Workspaces[0].Collections)-1]
	if !fileExists(filepath.Join(legacyCollection.Path, "bruno.json")) || !fileExists(filepath.Join(legacyCollection.Path, "Saved.bru")) {
		t.Fatalf("legacy import was not durable: %#v", legacyCollection)
	}
}

func TestCollectionImportPersistFailureRollsBackBatch(t *testing.T) {
	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	before, err := cloneCollectionImportState(state)
	if err != nil {
		t.Fatal(err)
	}
	sources := []CollectionImportSource{
		{ID: "rollback-a", Name: "rollback-a.json", Content: `{"info":{"name":"Rollback A"},"item":[]}`},
		{ID: "rollback-b", Name: "rollback-b.json", Content: `{"info":{"name":"Rollback B"},"item":[]}`},
	}
	app.collectionImportHooks = &collectionImportHooks{persist: func(*App) error { return errors.New("persist failed") }}
	_, err = app.ApplyCollectionImport(CollectionImportApplyRequest{
		WorkspaceID: state.Workspaces[0].ID,
		Sources:     sources,
		Selections: []CollectionImportSelection{
			{SourceID: "rollback-a", CandidateID: "rollback-a:collection", ExpectedContentHash: hashCollectionImportBytes([]byte(sources[0].Content))},
			{SourceID: "rollback-b", CandidateID: "rollback-b:collection", ExpectedContentHash: hashCollectionImportBytes([]byte(sources[1].Content))},
		},
	})
	if err == nil {
		t.Fatal("expected persistence failure")
	}
	after, getErr := app.GetState()
	if getErr != nil {
		t.Fatal(getErr)
	}
	if len(after.Workspaces[0].Collections) != len(before.Workspaces[0].Collections) {
		t.Fatalf("state was not restored: before=%d after=%d", len(before.Workspaces[0].Collections), len(after.Workspaces[0].Collections))
	}
	for _, name := range []string{"Rollback A", "Rollback B"} {
		if _, statErr := os.Stat(filepath.Join(after.Workspaces[0].Path, name)); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("batch rollback left %q behind: %v", name, statErr)
		}
	}
	assertNoImportScratchDirs(t, after.Workspaces[0].Path)
}

func TestCollectionImportWriteFailureRollsBackStaging(t *testing.T) {
	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	app.collectionImportHooks = &collectionImportHooks{write: func(*App, *Collection) error { return errors.New("write failed") }}
	source := CollectionImportSource{ID: "write-failure", Name: "write-failure.json", Content: `{"info":{"name":"Write failure"},"item":[]}`}
	if _, err := app.ApplyCollectionImport(CollectionImportApplyRequest{WorkspaceID: state.Workspaces[0].ID, Sources: []CollectionImportSource{source}, Selections: []CollectionImportSelection{{SourceID: source.ID, CandidateID: source.ID + ":collection", ExpectedContentHash: hashCollectionImportBytes([]byte(source.Content))}}}); err == nil {
		t.Fatal("expected write failure")
	}
	after, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Workspaces[0].Collections) != len(state.Workspaces[0].Collections) {
		t.Fatalf("state was mutated after write failure: %#v", after.Workspaces[0].Collections)
	}
	assertNoImportScratchDirs(t, after.Workspaces[0].Path)
}

func TestCollectionImportReplaceRenameFailureRestoresExistingTarget(t *testing.T) {
	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	original := CollectionImportSource{ID: "original", Name: "replace.json", Content: `{"info":{"name":"Replace target"},"item":[{"name":"Original","request":{"method":"GET","url":"https://original.test"}}]}`}
	applySingleImport(t, app, state.Workspaces[0].ID, original, "")
	before, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	before, err = cloneCollectionImportState(before)
	if err != nil {
		t.Fatal(err)
	}
	watchBefore := map[string]string{}
	for key, value := range app.collectionWatchFingerprints {
		watchBefore[key] = value
	}
	replacement := CollectionImportSource{ID: "replacement", Name: "replace.json", Content: `{"info":{"name":"Replace target"},"item":[{"name":"Replacement","request":{"method":"GET","url":"https://replacement.test"}}]}`}
	failedFinalRename := false
	app.collectionImportHooks = &collectionImportHooks{rename: func(from, to string) error {
		if !failedFinalRename && strings.Contains(filepath.Base(from), ".liteapi-import-") && !strings.Contains(from, ".liteapi-import-backup-") {
			failedFinalRename = true
			return errors.New("final rename failed")
		}
		return os.Rename(from, to)
	}}
	if _, err := app.ApplyCollectionImport(CollectionImportApplyRequest{WorkspaceID: before.Workspaces[0].ID, Sources: []CollectionImportSource{replacement}, Selections: []CollectionImportSelection{{SourceID: replacement.ID, CandidateID: replacement.ID + ":collection", ExpectedContentHash: hashCollectionImportBytes([]byte(replacement.Content)), ConflictAction: "replace"}}}); err == nil {
		t.Fatal("expected final rename failure")
	}
	after, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(after, before) || !reflect.DeepEqual(app.collectionWatchFingerprints, watchBefore) {
		t.Fatalf("replace failure changed state=%v watcher=%v beforeWatchers=%#v afterWatchers=%#v", !reflect.DeepEqual(after, before), !reflect.DeepEqual(app.collectionWatchFingerprints, watchBefore), watchBefore, app.collectionWatchFingerprints)
	}
	existing := after.Workspaces[0].Collections[len(after.Workspaces[0].Collections)-1]
	if len(existing.Items) != 1 || existing.Items[0].Name != "Original" {
		t.Fatalf("existing collection was not restored: %#v", existing)
	}
	assertNoImportScratchDirs(t, filepath.Dir(existing.Path))
}

func TestCollectionImportRetriesBackupCleanup(t *testing.T) {
	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	original := CollectionImportSource{ID: "cleanup-original", Name: "cleanup.json", Content: `{"info":{"name":"Cleanup target"},"item":[]}`}
	applySingleImport(t, app, state.Workspaces[0].ID, original, "")
	replacement := CollectionImportSource{ID: "cleanup-replacement", Name: "cleanup.json", Content: `{"info":{"name":"Cleanup target"},"item":[{"name":"New","request":{"method":"GET","url":"https://new.test"}}]}`}
	failedOnce := false
	app.collectionImportHooks = &collectionImportHooks{remove: func(path string) error {
		if strings.Contains(path, ".liteapi-import-backup-") && !failedOnce {
			failedOnce = true
			return errors.New("temporary cleanup failure")
		}
		return os.RemoveAll(path)
	}}
	result, err := app.ApplyCollectionImport(CollectionImportApplyRequest{WorkspaceID: state.Workspaces[0].ID, Sources: []CollectionImportSource{replacement}, Selections: []CollectionImportSelection{{SourceID: replacement.ID, CandidateID: replacement.ID + ":collection", ExpectedContentHash: hashCollectionImportBytes([]byte(replacement.Content)), ConflictAction: "replace"}}})
	if err != nil || len(result.Applied) != 1 || !failedOnce {
		t.Fatalf("replace result=%#v err=%v cleanupRetried=%v", result, err, failedOnce)
	}
	collection := result.State.Workspaces[0].Collections[len(result.State.Workspaces[0].Collections)-1]
	assertNoImportScratchDirs(t, filepath.Dir(collection.Path))
}

func TestCollectionImportDiagnosticsDoNotExposePathTokens(t *testing.T) {
	const token = "super-secret-path-token"
	message := collectionImportDiagnostic(errors.New("walk /tmp/" + token + ": permission denied"))
	if strings.Contains(message, token) || message != "selected import could not be read safely" {
		t.Fatalf("unsafe diagnostic %q", message)
	}
}

func TestCollectionImportOpeningFolderSeedsWatcherAndFirstTab(t *testing.T) {
	root := t.TempDir()
	folder := filepath.Join(root, "Open me")
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(folder, "bruno.json"), []byte(`{"name":"Open me","type":"collection"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(folder, "Request.bru"), []byte("meta {\n  name: Request\n  type: http\n  seq: 1\n}\nget {\n  url: https://example.test\n}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	preview, err := app.PreviewCollectionImport(CollectionImportPreviewRequest{Sources: []CollectionImportSource{{ID: "open-folder", Path: folder}}})
	if err != nil || len(preview.Rows) != 1 {
		t.Fatalf("preview=%#v err=%v", preview, err)
	}
	result, err := app.ApplyCollectionImport(CollectionImportApplyRequest{WorkspaceID: state.Workspaces[0].ID, Sources: []CollectionImportSource{{ID: "open-folder", Path: folder}}, Selections: []CollectionImportSelection{{SourceID: "open-folder", CandidateID: "open-folder:collection", ExpectedContentHash: preview.Rows[0].ContentHash}}})
	if err != nil || len(result.Applied) != 1 {
		t.Fatalf("open folder result=%#v err=%v", result, err)
	}
	collection := result.State.Workspaces[0].Collections[len(result.State.Workspaces[0].Collections)-1]
	if app.collectionWatchFingerprints[filepath.Clean(collection.Path)] == "" || result.State.ActiveTabID == "" || len(result.State.OpenTabs) == 0 {
		t.Fatalf("opened folder did not seed watcher/tab: watchers=%#v state=%#v", app.collectionWatchFingerprints, result.State)
	}
}

func applySingleImport(t *testing.T, app *App, workspaceID string, source CollectionImportSource, action string) CollectionImportApplyResult {
	t.Helper()
	result, err := app.ApplyCollectionImport(CollectionImportApplyRequest{WorkspaceID: workspaceID, Sources: []CollectionImportSource{source}, Selections: []CollectionImportSelection{{SourceID: source.ID, CandidateID: source.ID + ":collection", ExpectedContentHash: hashCollectionImportBytes([]byte(source.Content)), ConflictAction: action}}})
	if err != nil || len(result.Applied) != 1 {
		t.Fatalf("apply source %q: result=%#v err=%v", source.ID, result, err)
	}
	return result
}

func TestCollectionImportExplicitEmptyRequestFilter(t *testing.T) {
	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	source := CollectionImportSource{ID: "filter", Name: "filter.json", Content: `{"info":{"name":"Filtered"},"item":[{"name":"One","request":{"method":"GET","url":"https://one.test"}}]}`}
	result, err := app.ApplyCollectionImport(CollectionImportApplyRequest{WorkspaceID: state.Workspaces[0].ID, Sources: []CollectionImportSource{source}, Selections: []CollectionImportSelection{{SourceID: "filter", CandidateID: "filter:collection", ExpectedContentHash: hashCollectionImportBytes([]byte(source.Content)), FilterRequests: true}}})
	if err != nil || len(result.Applied) != 1 {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	collection := result.State.Workspaces[0].Collections[len(result.State.Workspaces[0].Collections)-1]
	if len(collection.Items) != 0 {
		t.Fatalf("empty request filter kept requests: %#v", collection.Items)
	}
}

func TestCollectionImportManualOverrideHonorsRequestFilter(t *testing.T) {
	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	source := CollectionImportSource{ID: "override", Name: "override.json", Content: `{"info":{"name":"Override"},"item":[{"name":"Keep","request":{"method":"GET","url":"https://keep.test"}},{"name":"Drop","request":{"method":"GET","url":"https://drop.test"}}]}`}
	preview, err := app.PreviewCollectionImport(CollectionImportPreviewRequest{Sources: []CollectionImportSource{source}})
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Rows) != 1 || len(preview.Rows[0].RequestIDs) != 2 {
		t.Fatalf("preview = %#v", preview)
	}
	result, err := app.ApplyCollectionImport(CollectionImportApplyRequest{
		WorkspaceID: state.Workspaces[0].ID,
		Sources:     []CollectionImportSource{source},
		Selections: []CollectionImportSelection{{
			SourceID:            "override",
			CandidateID:         "override:collection",
			ExpectedContentHash: preview.Rows[0].ContentHash,
			KindOverride:        "postman",
			FilterRequests:      true,
			RequestIDs:          []string{preview.Rows[0].RequestIDs[0]},
		}},
	})
	if err != nil || len(result.Applied) != 1 {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	collection := result.State.Workspaces[0].Collections[len(result.State.Workspaces[0].Collections)-1]
	if len(collection.Items) != 1 || collection.Items[0].Name != "Keep" {
		t.Fatalf("override discarded selection: %#v", collection.Items)
	}
}

func TestCollectionImportURLFetchesOncePerApplyAndScrubsQuery(t *testing.T) {
	var calls atomic.Int32
	app := newAppForTest(t)
	app.httpClient = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls.Add(1)
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"info":{"name":"Remote"},"item":[]}`))}, nil
	})}
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	source := CollectionImportSource{ID: "remote", URL: "https://example.test/collection.json?token=never-display"}
	preview, err := app.PreviewCollectionImport(CollectionImportPreviewRequest{WorkspaceID: state.Workspaces[0].ID, Sources: []CollectionImportSource{source}})
	if err != nil || calls.Load() != 1 || len(preview.Rows) != 1 {
		t.Fatalf("preview=%#v calls=%d err=%v", preview, calls.Load(), err)
	}
	if strings.Contains(preview.Rows[0].SourcePath, "token") || strings.Contains(preview.Rows[0].Error, "token") {
		t.Fatalf("URL query leaked in preview: %#v", preview.Rows[0])
	}
	calls.Store(0)
	result, err := app.ApplyCollectionImport(CollectionImportApplyRequest{
		WorkspaceID: state.Workspaces[0].ID,
		Sources:     []CollectionImportSource{source},
		Selections:  []CollectionImportSelection{{SourceID: "remote", CandidateID: "remote:collection", ExpectedContentHash: preview.Rows[0].ContentHash}},
	})
	if err != nil || calls.Load() != 1 || len(result.Applied) != 1 {
		t.Fatalf("apply=%#v calls=%d err=%v", result, calls.Load(), err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return fn(request) }

func TestCollectionImportURLPolicyAndCurlImportDoNotLeakCredentials(t *testing.T) {
	if _, _, err := fetchCollectionImportURL("https://user:secret@example.test/spec.json", nil); err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatalf("unsafe URL error: %v", err)
	}
	kind, collection, warnings, err := detectCollectionImport(`curl -X POST -H 'X-Test: value' -u alice:secret -d '{"safe":true}' https://example.test/v1`, "request.sh", "")
	if err != nil || kind != "curl" || len(collection.Items) != 1 || collection.Items[0].Method != "POST" || collection.Items[0].Auth.Password != "secret" || strings.Contains(strings.Join(warnings, " "), "secret") {
		t.Fatalf("curl import kind=%q collection=%#v warnings=%#v err=%v", kind, collection, warnings, err)
	}
}

func TestCollectionImportURLFailureIsRowScopedAlongsideValidSource(t *testing.T) {
	app := newAppForTest(t)
	state, _ := app.GetState()
	sources := []CollectionImportSource{
		{ID: "remote-failure", URL: "https://user:token@example.test/spec.json"},
		{ID: "valid-file", Name: "valid.json", Content: `{"info":{"name":"Valid"},"item":[]}`},
	}
	preview, err := app.PreviewCollectionImport(CollectionImportPreviewRequest{WorkspaceID: state.Workspaces[0].ID, Sources: sources})
	if err != nil || len(preview.Rows) != 2 || preview.Rows[0].Error != "remote import URL must be an http or https URL without credentials" || strings.Contains(preview.Rows[0].Error, "token") || preview.Rows[1].Error != "" {
		t.Fatalf("mixed preview=%#v err=%v", preview, err)
	}
	result, err := app.ApplyCollectionImport(CollectionImportApplyRequest{WorkspaceID: state.Workspaces[0].ID, Sources: sources, Selections: []CollectionImportSelection{{SourceID: "valid-file", CandidateID: "valid-file:collection", ExpectedContentHash: preview.Rows[1].ContentHash}}})
	if err != nil || len(result.Applied) != 1 {
		t.Fatalf("mixed apply=%#v err=%v", result, err)
	}
}

func TestCollectionImportPreviewConflictStatesAndRememberedDirectory(t *testing.T) {
	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	workspace := state.Workspaces[0]
	source := CollectionImportSource{ID: "conflict", Name: "Conflict.json", Content: `{"info":{"name":"Conflict"},"item":[]}`}
	if err := os.Mkdir(filepath.Join(workspace.Path, "Conflict"), 0o755); err != nil {
		t.Fatal(err)
	}
	preview, err := app.PreviewCollectionImport(CollectionImportPreviewRequest{WorkspaceID: workspace.ID, Sources: []CollectionImportSource{source}})
	if err != nil || preview.Rows[0].Conflict != "exists" {
		t.Fatalf("exists preview=%#v err=%v", preview, err)
	}
	if err := os.Remove(filepath.Join(workspace.Path, "Conflict")); err != nil {
		t.Fatal(err)
	}
	result, err := app.ApplyCollectionImport(CollectionImportApplyRequest{WorkspaceID: workspace.ID, Sources: []CollectionImportSource{source}, Selections: []CollectionImportSelection{{SourceID: source.ID, CandidateID: source.ID + ":collection", ExpectedContentHash: hashCollectionImportBytes([]byte(source.Content))}}})
	if err != nil || len(result.Applied) != 1 {
		t.Fatalf("initial import=%#v err=%v", result, err)
	}
	preview, err = app.PreviewCollectionImport(CollectionImportPreviewRequest{WorkspaceID: workspace.ID, Sources: []CollectionImportSource{source}})
	if err != nil || preview.Rows[0].Conflict != "already-open" {
		t.Fatalf("open preview=%#v err=%v", preview, err)
	}
	unavailable := CollectionImportSource{ID: "unavailable", Name: "Unavailable.json", Content: `{"info":{"name":"Unavailable"},"item":[]}`}
	if err := os.Symlink(t.TempDir(), filepath.Join(workspace.Path, "Unavailable")); err != nil {
		t.Fatal(err)
	}
	preview, err = app.PreviewCollectionImport(CollectionImportPreviewRequest{WorkspaceID: workspace.ID, Sources: []CollectionImportSource{unavailable}})
	if err != nil || preview.Rows[0].Conflict != "unavailable" {
		t.Fatalf("unavailable preview=%#v err=%v", preview, err)
	}
	remembered := t.TempDir()
	file := filepath.Join(remembered, "source.json")
	if err := os.WriteFile(file, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := app.rememberCollectionImportDirectory([]string{file}, false); err != nil || app.collectionImportDefaultDirectory() != normalizeCollectionImportDirectory(remembered) {
		t.Fatalf("remembered directory=%q err=%v", app.collectionImportDefaultDirectory(), err)
	}
}

func TestCollectionImportOverrideRescueWithSubselection(t *testing.T) {
	app := newAppForTest(t)
	state, _ := app.GetState()
	source := CollectionImportSource{ID: "override-subset", Name: "misnamed.zip", Content: `{"info":{"name":"Override subset"},"item":[{"name":"Keep","request":{"method":"GET","url":"https://keep.test"}},{"name":"Drop","request":{"method":"GET","url":"https://drop.test"}}]}`}
	preview, err := app.PreviewCollectionImport(CollectionImportPreviewRequest{Sources: []CollectionImportSource{{ID: source.ID, Name: source.Name, Content: source.Content, KindOverride: "postman"}}})
	if err != nil || len(preview.Rows) != 1 || len(preview.Rows[0].RequestIDs) != 2 {
		t.Fatalf("override preview=%#v err=%v", preview, err)
	}
	result, err := app.ApplyCollectionImport(CollectionImportApplyRequest{
		WorkspaceID: state.Workspaces[0].ID,
		Sources:     []CollectionImportSource{source},
		Selections:  []CollectionImportSelection{{SourceID: source.ID, CandidateID: source.ID + ":collection", ExpectedContentHash: preview.Rows[0].ContentHash, KindOverride: "postman", FilterRequests: true, RequestIDs: []string{preview.Rows[0].RequestIDs[0]}}},
	})
	if err != nil || len(result.Applied) != 1 {
		t.Fatalf("override apply=%#v err=%v", result, err)
	}
	collection := result.State.Workspaces[0].Collections[len(result.State.Workspaces[0].Collections)-1]
	if len(collection.Items) != 1 || collection.Items[0].Name != "Keep" {
		t.Fatalf("subselection lost after override: %#v", collection.Items)
	}
}

func TestLegacyImportPersistenceFailureRollsBack(t *testing.T) {
	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	before, err := cloneCollectionImportState(state)
	if err != nil {
		t.Fatal(err)
	}
	app.collectionImportHooks = &collectionImportHooks{persist: func(*App) error { return errors.New("persist failed") }}
	if _, err := app.ImportCollection(state.Workspaces[0].ID, ImportPayload{Kind: "postman", Name: "Legacy rollback", Content: `{"info":{"name":"Legacy rollback"},"item":[]}`}); err == nil {
		t.Fatal("expected persistence failure")
	}
	after, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Workspaces[0].Collections) != len(before.Workspaces[0].Collections) {
		t.Fatalf("legacy state was not restored: before=%d after=%d", len(before.Workspaces[0].Collections), len(after.Workspaces[0].Collections))
	}
	assertNoImportScratchDirs(t, after.Workspaces[0].Path)
}

func collectionExists(workspace Workspace, path string) bool {
	for _, collection := range workspace.Collections {
		if filepath.Clean(collection.Path) == filepath.Clean(path) {
			return true
		}
	}
	return false
}

func assertNoImportScratchDirs(t *testing.T, root string) {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), ".liteapi-import-") || strings.Contains(entry.Name(), ".liteapi-import-backup-") {
			t.Fatalf("residual import scratch directory: %s", entry.Name())
		}
	}
}
