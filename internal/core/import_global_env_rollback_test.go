// A failed import must not leave the state it failed to write.
//
// The collection branch of ApplyCollectionImport already restores state when a
// commit fails. The global-environment branch appended to a live pointer into
// a.state before the fallible write, so a failed import told the caller it had
// failed while leaving the environment sitting in memory -- visible in the UI,
// and written out by the next unrelated save.
package core

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
)

// blockGlobalEnvironmentWrites puts a regular file where the environments
// directory belongs, so the MkdirAll inside the writer fails. This stands in
// for the real cases -- a read-only directory, a full disk -- without needing
// either.
func blockGlobalEnvironmentWrites(t *testing.T, workspacePath string) {
	t.Helper()
	envPath := filepath.Join(workspacePath, "environments")
	if err := os.RemoveAll(envPath); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(envPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(envPath, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestAFailedGlobalEnvironmentImportLeavesNothingBehind(t *testing.T) {
	app := newAppForTest(t)
	app.mu.Lock()
	defer app.mu.Unlock()

	workspace, err := app.findWorkspaceLocked("")
	if err != nil {
		t.Fatal(err)
	}
	blockGlobalEnvironmentWrites(t, workspace.Path)

	before := len(workspace.GlobalEnvironments)
	updatedBefore := workspace.UpdatedAt

	err = app.applyImportedGlobalEnvironmentsLocked(workspace, []Environment{{
		Name:      "Staging",
		Variables: []Variable{{Name: "host", Value: "https://staging.test"}},
	}})
	if err == nil {
		t.Fatal("the write was blocked but the import reported success")
	}
	if got := len(workspace.GlobalEnvironments); got != before {
		t.Fatalf("a failed import left %d environment(s) in memory, want %d", got, before)
	}
	if !workspace.UpdatedAt.Equal(updatedBefore) {
		t.Fatal("a failed import still bumped UpdatedAt, so the workspace looks changed when nothing was")
	}
}

// The rollback must not undo more than its own append. Environments that were
// already there belong to the user, not to this import.
func TestAFailedGlobalEnvironmentImportKeepsTheExistingOnes(t *testing.T) {
	app := newAppForTest(t)
	app.mu.Lock()
	defer app.mu.Unlock()

	workspace, err := app.findWorkspaceLocked("")
	if err != nil {
		t.Fatal(err)
	}
	workspace.GlobalEnvironments = append(workspace.GlobalEnvironments, Environment{ID: "keep-me", Name: "Existing"})
	blockGlobalEnvironmentWrites(t, workspace.Path)

	if err := app.applyImportedGlobalEnvironmentsLocked(workspace, []Environment{{Name: "Staging"}}); err == nil {
		t.Fatal("the write was blocked but the import reported success")
	}
	if len(workspace.GlobalEnvironments) != 1 || workspace.GlobalEnvironments[0].ID != "keep-me" {
		t.Fatalf("rollback disturbed the existing environments: %#v", workspace.GlobalEnvironments)
	}
}

// The successful path has to keep working, or the rollback is just a way of
// never importing anything.
func TestASuccessfulGlobalEnvironmentImportStillLands(t *testing.T) {
	app := newAppForTest(t)
	app.mu.Lock()
	defer app.mu.Unlock()

	workspace, err := app.findWorkspaceLocked("")
	if err != nil {
		t.Fatal(err)
	}
	if err := app.applyImportedGlobalEnvironmentsLocked(workspace, []Environment{{Name: "Staging"}}); err != nil {
		t.Fatal(err)
	}
	if len(workspace.GlobalEnvironments) != 1 || workspace.GlobalEnvironments[0].Name != "Staging" {
		t.Fatalf("environments = %#v", workspace.GlobalEnvironments)
	}
}

// sabotageEnvironmentFile puts a directory where one environment's file needs
// to be written, so that environment's write fails while the ones before it
// have already succeeded. This is the shape of a real partial failure -- a
// permission change, a full disk -- reached deterministically.
func sabotageEnvironmentFile(t *testing.T, workspacePath, environmentName string) {
	t.Helper()
	blocked := filepath.Join(workspacePath, "environments", environmentName+".yml")
	if err := os.MkdirAll(blocked, 0o755); err != nil {
		t.Fatal(err)
	}
}

func environmentFilesOnDisk(t *testing.T, workspacePath string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(workspacePath, "environments"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatal(err)
	}
	names := []string{}
	for _, entry := range entries {
		if !entry.IsDir() && strings.EqualFold(filepath.Ext(entry.Name()), ".yml") {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	return names
}

// The write is not one operation. It clears the directory and then writes each
// environment in turn, so a failure part way through leaves the directory in a
// state that matches neither what was there before nor what was asked for.
//
// Rolling memory back and leaving those files is the worse half: the next
// workspace load merges whatever is on disk back into the state, so an import
// the user was told had failed returns by itself.
func TestAPartlyWrittenGlobalEnvironmentImportLeavesTheDirectoryAsItWas(t *testing.T) {
	app := newAppForTest(t)
	app.mu.Lock()
	defer app.mu.Unlock()

	workspace, err := app.findWorkspaceLocked("")
	if err != nil {
		t.Fatal(err)
	}
	// One environment the user already had, written the way the app writes it.
	workspace.GlobalEnvironments = append(workspace.GlobalEnvironments, Environment{ID: "env-existing", Name: "Alpha"})
	if err := app.writeWorkspaceGlobalEnvironmentFilesLocked(workspace); err != nil {
		t.Fatal(err)
	}
	before := environmentFilesOnDisk(t, workspace.Path)

	sabotageEnvironmentFile(t, workspace.Path, "Broken")
	err = app.applyImportedGlobalEnvironmentsLocked(workspace, []Environment{{Name: "Beta"}, {Name: "Broken"}})
	if err == nil {
		t.Fatal("the second write was blocked but the import reported success")
	}

	if got := environmentFilesOnDisk(t, workspace.Path); !slices.Equal(got, before) {
		t.Fatalf("a failed import left the directory as %v, want %v", got, before)
	}
	if len(workspace.GlobalEnvironments) != 1 || workspace.GlobalEnvironments[0].ID != "env-existing" {
		t.Fatalf("memory = %#v", workspace.GlobalEnvironments)
	}
}

// The same failure must not destroy what was already there. The writer clears
// the directory before rewriting it, so a failure after the clear and before
// the rewrite is how an existing environment disappears.
func TestAPartlyWrittenGlobalEnvironmentImportKeepsTheExistingFileContents(t *testing.T) {
	app := newAppForTest(t)
	app.mu.Lock()
	defer app.mu.Unlock()

	workspace, err := app.findWorkspaceLocked("")
	if err != nil {
		t.Fatal(err)
	}
	workspace.GlobalEnvironments = append(workspace.GlobalEnvironments, Environment{
		ID:        "env-existing",
		Name:      "Alpha",
		Variables: []Variable{{ID: "v1", Name: "host", Value: "https://alpha.test", Enabled: true}},
	})
	if err := app.writeWorkspaceGlobalEnvironmentFilesLocked(workspace); err != nil {
		t.Fatal(err)
	}
	alphaPath := filepath.Join(workspace.Path, "environments", "Alpha.yml")
	before, err := os.ReadFile(alphaPath)
	if err != nil {
		t.Fatal(err)
	}

	sabotageEnvironmentFile(t, workspace.Path, "Broken")
	if err := app.applyImportedGlobalEnvironmentsLocked(workspace, []Environment{{Name: "Broken"}}); err == nil {
		t.Fatal("the write was blocked but the import reported success")
	}

	after, err := os.ReadFile(alphaPath)
	if err != nil {
		t.Fatalf("the environment the user already had was destroyed by a failed import: %v", err)
	}
	if string(after) != string(before) {
		t.Fatalf("the existing environment file changed:\nbefore %s\nafter  %s", before, after)
	}
}

// A batch rolls back as a unit. An environment written by one candidate must
// go with it when another candidate fails the commit, or the import that was
// reported as failed is still on disk -- and the next workspace load merges
// whatever is on disk back into the state, so it returns by itself.
func TestABatchRollbackTakesTheImportedEnvironmentWithIt(t *testing.T) {
	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	workspacePath := state.Workspaces[0].Path

	environmentDocument := `{"id":"env-1","name":"Imported Staging","values":[{"key":"host","value":"https://staging.test","enabled":true}],"_postman_variable_scope":"environment"}`
	collectionDocument := `{"info":{"name":"Batch Collection"},"item":[]}`
	sources := []CollectionImportSource{
		{ID: "env-source", Name: "staging.postman_environment.json", Content: environmentDocument},
		{ID: "collection-source", Name: "batch.json", Content: collectionDocument},
	}
	preview, err := app.PreviewCollectionImport(CollectionImportPreviewRequest{WorkspaceID: state.Workspaces[0].ID, Sources: sources})
	if err != nil {
		t.Fatal(err)
	}
	selections := make([]CollectionImportSelection, 0, len(preview.Rows))
	for _, row := range preview.Rows {
		if row.Error != "" {
			t.Fatalf("preview row failed: %#v", row)
		}
		selections = append(selections, CollectionImportSelection{
			SourceID:            row.SourceID,
			CandidateID:         row.CandidateID,
			ExpectedContentHash: row.ContentHash,
		})
	}

	// The commit fails after both candidates have written, which is the window
	// this is about.
	app.collectionImportHooks = &collectionImportHooks{persist: func(*App) error { return errors.New("persist failed") }}
	if _, err := app.ApplyCollectionImport(CollectionImportApplyRequest{
		WorkspaceID: state.Workspaces[0].ID, Sources: sources, Selections: selections,
	}); err == nil {
		t.Fatal("expected the commit to fail")
	}

	if files := environmentFilesOnDisk(t, workspacePath); len(files) != 0 {
		t.Fatalf("the rolled-back import left %v on disk, where the next load will find it", files)
	}
	after, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	for _, environment := range after.Workspaces[0].GlobalEnvironments {
		if environment.Name == "Imported Staging" {
			t.Fatal("the environment came back after the import was rolled back")
		}
	}
}
