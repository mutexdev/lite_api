// A failed import must not leave the state it failed to write.
//
// The collection branch of ApplyCollectionImport already restores state when a
// commit fails. The global-environment branch appended to a live pointer into
// a.state before the fallible write, so a failed import told the caller it had
// failed while leaving the environment sitting in memory -- visible in the UI,
// and written out by the next unrelated save.
package core

import (
	"os"
	"path/filepath"
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
