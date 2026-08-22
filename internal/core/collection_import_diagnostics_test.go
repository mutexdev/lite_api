// Import failures have to say what went wrong (US-055).
//
// collectionImportDiagnostic used to allow-list about twenty literal strings
// and collapse everything else -- every JSON error, every filesystem error --
// into "selected import could not be read safely". The person importing a
// 4 MB Postman export had no way to learn which of its requests was the problem
// short of attaching a debugger, and the console said nothing either.
//
// The redaction those tests protect is real and stays: an import diagnostic
// must never carry a filesystem path. What changes is that redacting a path is
// no longer the same thing as discarding the diagnosis.
package core

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"
	"testing"
)

func TestCollectionImportDiagnosticNamesTheJSONFault(t *testing.T) {
	_, _, _, err := detectCollectionImport("{\"info\":{\"name\":\"A\"},\"item\":[}", "broken.json", "postman")
	if err == nil {
		t.Fatal("malformed JSON imported cleanly")
	}
	message := collectionImportDiagnostic(err)
	if !strings.Contains(message, "line") || !strings.Contains(strings.ToLower(message), "invalid") {
		t.Fatalf("diagnostic does not locate the fault: %q", message)
	}
}

func TestCollectionImportDiagnosticNamesTheOffendingField(t *testing.T) {
	_, _, _, err := detectCollectionImport(`{"info":{"name":"A"},"item":[],"variable":"not a list"}`, "x.json", "postman")
	if err == nil {
		t.Fatal("a variable list of the wrong type imported cleanly")
	}
	message := collectionImportDiagnostic(err)
	if !strings.Contains(message, "variable") {
		t.Fatalf("diagnostic does not name the field: %q", message)
	}
}

func TestCollectionImportDiagnosticStillHidesPaths(t *testing.T) {
	const token = "super-secret-path-token"
	cases := []error{
		errors.New("walk /tmp/" + token + ": permission denied"),
		&os.PathError{Op: "open", Path: "/home/someone/" + token + "/x.json", Err: syscall.EACCES},
		fmt.Errorf("read %s: %w", `C:\Users\someone\`+token+`\x.json`, os.ErrPermission),
		errors.New("failed at ~/Documents/" + token),
	}
	for _, err := range cases {
		message := collectionImportDiagnostic(err)
		if strings.Contains(message, token) {
			t.Fatalf("diagnostic leaked a path: %q", message)
		}
		for _, separator := range []string{"/", "\\", "~"} {
			if strings.Contains(message, separator) {
				t.Fatalf("diagnostic %q still carries a path separator %q", message, separator)
			}
		}
		if strings.TrimSpace(message) == "" {
			t.Fatal("redaction produced an empty diagnostic")
		}
	}
}

func TestCollectionImportDiagnosticKeepsTheAuthoredMessagesVerbatim(t *testing.T) {
	for _, message := range []string{
		"ZIP imports are not supported yet",
		"source is ambiguous; choose an import kind manually",
		"remote import could not be fetched",
	} {
		if got := collectionImportDiagnostic(errors.New(message)); got != message {
			t.Fatalf("authored message rewritten: %q -> %q", message, got)
		}
	}
}

func TestCollectionImportDiagnosticIsBounded(t *testing.T) {
	message := collectionImportDiagnostic(errors.New(strings.Repeat("verbose ", 400)))
	if len(message) > collectionImportDiagnosticMaxBytes {
		t.Fatalf("diagnostic is %d bytes", len(message))
	}
}

func TestCollectionImportNameTooLongIsReportedAsSuch(t *testing.T) {
	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	longName := strings.Repeat("n", 400)
	source := CollectionImportSource{
		ID:      "long",
		Name:    "long.json",
		Content: fmt.Sprintf(`{"info":{"name":"Long"},"item":[{"name":%q,"request":{"method":"GET","url":"https://example.test"}}]}`, longName),
	}
	preview, err := app.PreviewCollectionImport(CollectionImportPreviewRequest{Sources: []CollectionImportSource{source}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := app.ApplyCollectionImport(CollectionImportApplyRequest{
		WorkspaceID: state.Workspaces[0].ID,
		Sources:     []CollectionImportSource{source},
		Selections:  []CollectionImportSelection{{SourceID: "long", CandidateID: "long:collection", ExpectedContentHash: preview.Rows[0].ContentHash}},
	})
	// US-057 caps the filename, so this now succeeds. Should the cap ever be
	// removed, the failure must at least explain itself.
	if err != nil {
		if !strings.Contains(strings.ToLower(err.Error()), "name") {
			t.Fatalf("a name-length failure reported as %q", err)
		}
		return
	}
	if len(result.Applied) != 1 {
		t.Fatalf("result = %#v", result)
	}
	collection := result.State.Workspaces[0].Collections[len(result.State.Workspaces[0].Collections)-1]
	if len(collection.Items) != 1 || !fileExists(collection.Items[0].FilePath) {
		t.Fatalf("long-named request was not written: %#v", collection.Items)
	}
}

func TestCollectionImportCommitFailureExplainsItself(t *testing.T) {
	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	app.collectionImportHooks = &collectionImportHooks{
		write: func(*App, *Collection) error { return errors.New("disk went away") },
	}
	source := CollectionImportSource{ID: "s", Name: "s.json", Content: `{"info":{"name":"S"},"item":[{"name":"r","request":{"method":"GET","url":"https://example.test"}}]}`}
	preview, err := app.PreviewCollectionImport(CollectionImportPreviewRequest{Sources: []CollectionImportSource{source}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = app.ApplyCollectionImport(CollectionImportApplyRequest{
		WorkspaceID: state.Workspaces[0].ID,
		Sources:     []CollectionImportSource{source},
		Selections:  []CollectionImportSelection{{SourceID: "s", CandidateID: "s:collection", ExpectedContentHash: preview.Rows[0].ContentHash}},
	})
	if err == nil {
		t.Fatal("a failing write reported success")
	}
	if !strings.Contains(err.Error(), "disk went away") {
		t.Fatalf("commit failure did not carry its cause: %q", err)
	}
}

func TestCollectionImportPreviewRowCarriesImporterWarnings(t *testing.T) {
	app := newAppForTest(t)
	source := CollectionImportSource{
		ID:      "w",
		Name:    "w.json",
		Content: `{"info":{"name":"W"},"item":[{"name":"good","request":{"method":"GET","url":"https://example.test"}},{"name":"bad","request":{"method":42,"url":"https://example.test"}}]}`,
	}
	preview, err := app.PreviewCollectionImport(CollectionImportPreviewRequest{Sources: []CollectionImportSource{source}})
	if err != nil {
		t.Fatal(err)
	}
	row := preview.Rows[0]
	if row.Error != "" {
		t.Fatalf("one bad request failed the whole source: %q", row.Error)
	}
	if len(row.Warnings) == 0 || !strings.Contains(strings.Join(row.Warnings, "\n"), "bad") {
		t.Fatalf("warnings = %#v", row.Warnings)
	}
}

func TestCollectionImportApplyRaisesANotification(t *testing.T) {
	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	sources := []CollectionImportSource{
		{ID: "ok", Name: "ok.json", Content: `{"info":{"name":"Fine"},"item":[{"name":"r","request":{"method":"GET","url":"https://example.test"}}]}`},
		{ID: "bad", Name: "bad.json", Content: `{"hello":true}`},
	}
	preview, err := app.PreviewCollectionImport(CollectionImportPreviewRequest{Sources: sources})
	if err != nil {
		t.Fatal(err)
	}
	result, err := app.ApplyCollectionImport(CollectionImportApplyRequest{
		WorkspaceID: state.Workspaces[0].ID,
		Sources:     sources,
		Selections: []CollectionImportSelection{
			{SourceID: "ok", CandidateID: "ok:collection", ExpectedContentHash: preview.Rows[0].ContentHash},
			{SourceID: "bad", CandidateID: "bad:collection", ExpectedContentHash: preview.Rows[1].ContentHash},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Applied) != 1 || len(result.Errors) != 1 {
		t.Fatalf("result = %#v", result)
	}
	if len(result.State.Notifications) == 0 {
		t.Fatal("an import that half failed raised no notification")
	}
	message := result.State.Notifications[0].Message
	if !strings.Contains(message, "1 imported") || !strings.Contains(message, "1 failed") {
		t.Fatalf("notification = %q", message)
	}
}
