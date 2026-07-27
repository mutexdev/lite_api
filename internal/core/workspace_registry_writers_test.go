package core

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mutexdev/lite_api/internal/workspacestate"
)

// TWO FUNCTIONS WRITE THE WORKSPACE REGISTRY, and only one of them runs in
// production.
//
//	workspacestate.WriteWorkspaceRegistry   used by that package's own tests
//	writeWorkspaceMigrationRegistry (here)  used by the migration path
//
// They are the same three steps — Validate, MarshalIndent, atomic write — and
// the duplicate is JUSTIFIED: the migration one goes through the
// workspacePersistenceWriteAtomic seam so a test can inject a failure at the
// third write and check the migration leaves no completion marker. That seam is
// worth having, so these are not collapsed.
//
// What was missing is anything holding them in step. A field added to the
// registry, or a change of indentation, in one and not the other means the
// migration writes a file in a different format from every other write of the
// same file — and the reader would accept both while a human comparing them
// would not.
//
// The repo already used this technique on sameCanonicalWorkspacePath, where two
// copies were "proven identical by a differential test before they were
// collapsed". These stay, and the differential test stays with them.
func TestBothRegistryWritersProduceIdenticalBytes(t *testing.T) {
	// The version constant is unexported, so this is a literal. A version bump
	// will fail this test at Validate, loudly, which is the right moment to
	// revisit whether both writers still agree.
	registry := workspacestate.WorkspaceRegistry{
		Version: 1,
		Workspaces: []workspacestate.WorkspaceRegistryEntry{
			{ID: "ws-1", Name: "First", Path: "/tmp/first"},
			{ID: "ws-2", Name: "Second", Path: "/tmp/second"},
		},
	}

	viaPackage := t.TempDir()
	if err := workspacestate.WriteWorkspaceRegistry(viaPackage, registry); err != nil {
		t.Fatalf("workspacestate writer: %v", err)
	}

	viaMigration := t.TempDir()
	if err := writeWorkspaceMigrationRegistry(viaMigration, registry); err != nil {
		t.Fatalf("migration writer: %v", err)
	}

	read := func(dir string) []byte {
		t.Helper()
		data, err := os.ReadFile(workspacestate.WorkspaceRegistryPath(dir))
		if err != nil {
			t.Fatalf("read %s: %v", dir, err)
		}
		return data
	}

	fromPackage, fromMigration := read(viaPackage), read(viaMigration)
	if string(fromPackage) != string(fromMigration) {
		t.Errorf("the two registry writers disagree.\npackage:\n%s\nmigration:\n%s", fromPackage, fromMigration)
	}

	// And both must land at the same path, with the same owner-only mode — the
	// registry names every workspace on the machine.
	for _, dir := range []string{viaPackage, viaMigration} {
		info, err := os.Stat(workspacestate.WorkspaceRegistryPath(dir))
		if err != nil {
			t.Fatal(err)
		}
		if mode := info.Mode().Perm(); mode != 0o600 {
			t.Errorf("%s: mode %o, want 600", filepath.Base(dir), mode)
		}
	}
}

// Both must reject a registry that does not validate, and reject it BEFORE
// writing anything — a half-written registry is worse than none.
func TestBothRegistryWritersRejectAnInvalidRegistryWithoutWriting(t *testing.T) {
	invalid := workspacestate.WorkspaceRegistry{Version: 1,
		Workspaces: []workspacestate.WorkspaceRegistryEntry{{ID: "", Name: "no id", Path: "/tmp/x"}}}

	for name, write := range map[string]func(string, workspacestate.WorkspaceRegistry) error{
		"workspacestate": workspacestate.WriteWorkspaceRegistry,
		"migration":      writeWorkspaceMigrationRegistry,
	} {
		dir := t.TempDir()
		if err := write(dir, invalid); err == nil {
			t.Errorf("%s: an invalid registry was accepted", name)
		}
		if _, err := os.Stat(workspacestate.WorkspaceRegistryPath(dir)); !os.IsNotExist(err) {
			t.Errorf("%s: a file was written despite validation failing", name)
		}
	}
}
