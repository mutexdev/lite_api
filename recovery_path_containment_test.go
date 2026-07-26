// Recovery paths stay inside the recovery root.
//
// Negative control found validateRecoveryWorkspaceID untested: disabling it
// failed nothing. It turns out that is not a hole -- the workspace id is
// SHA-256 hashed before it becomes a directory name, so a hostile id cannot
// escape regardless of what the validator does.
//
// So the test below pins the PROPERTY (every derived path stays under the
// recovery root) rather than the mechanism (the validator, or the hashing).
// A test of the validator would pass while someone removed the hashing; this
// one fails if containment is ever lost, however it was being achieved.
package main

import (
	"path/filepath"
	"strings"
	"testing"
)

var hostileIDs = []string{
	"../../etc/passwd",
	"../..",
	"/etc/passwd",
	`..\..\windows\system32`,
	"a/../../b",
	"....//....//etc",
	"workspace\nid",
	"ünïcödé",
	strings.Repeat("a", 512),
}

func TestRecoveryPathsStayInsideTheRecoveryRoot(t *testing.T) {
	dataDir := t.TempDir()
	root, err := filepath.Abs(recoveryRoot(dataDir))
	if err != nil {
		t.Fatal(err)
	}

	for _, id := range hostileIDs {
		got, err := recoveryWorkspaceRoot(dataDir, id)
		if err != nil {
			continue // rejected outright is also fine
		}
		abs, err := filepath.Abs(got)
		if err != nil {
			t.Fatal(err)
		}
		rel, err := filepath.Rel(root, abs)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			t.Errorf("workspace id %q escaped the recovery root: %s", id, abs)
		}
	}
}

func TestRecoveryEntryPathsStayInsideTheWorkspaceRoot(t *testing.T) {
	dataDir := t.TempDir()
	const workspace = "workspace-1"
	root, err := recoveryWorkspaceRoot(dataDir, workspace)
	if err != nil {
		t.Fatal(err)
	}
	rootAbs, _ := filepath.Abs(root)

	for _, id := range hostileIDs {
		for _, build := range []func(string, string, string) (string, error){
			recoveryEntryDir, recoverySnapshotPath, recoveryPayloadPath,
		} {
			got, err := build(dataDir, workspace, id)
			if err != nil {
				continue // a non-UUID entry id is rejected, which is the point
			}
			abs, _ := filepath.Abs(got)
			rel, err := filepath.Rel(rootAbs, abs)
			if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				t.Errorf("entry id %q escaped the workspace root: %s", id, abs)
			}
		}
	}
}

// Two different workspaces must never share a directory, or one workspace's
// recovery data would overwrite another's.
func TestDifferentWorkspacesGetDifferentRoots(t *testing.T) {
	dataDir := t.TempDir()
	seen := map[string]string{}
	for _, id := range []string{"a", "b", "workspace-1", "workspace-2", "A", "ünïcödé"} {
		root, err := recoveryWorkspaceRoot(dataDir, id)
		if err != nil {
			t.Fatal(err)
		}
		if other, clash := seen[root]; clash {
			t.Fatalf("workspaces %q and %q share a recovery root: %s", other, id, root)
		}
		seen[root] = id
	}
}

func TestSameWorkspaceIDIsStableAcrossCalls(t *testing.T) {
	dataDir := t.TempDir()
	first, err := recoveryWorkspaceRoot(dataDir, "workspace-1")
	if err != nil {
		t.Fatal(err)
	}
	second, err := recoveryWorkspaceRoot(dataDir, "workspace-1")
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("the same workspace resolved to two roots:\n%s\n%s", first, second)
	}
}

// An empty or NUL-bearing id has no legitimate use and must be refused rather
// than resolved to some default directory.
func TestBlankAndNulWorkspaceIDsAreRejected(t *testing.T) {
	dataDir := t.TempDir()
	for _, id := range []string{"", "   ", "\t", "a\x00b"} {
		if _, err := recoveryWorkspaceRoot(dataDir, id); err == nil {
			t.Errorf("workspace id %q was accepted", id)
		}
	}
}
