package core

import (
	"os"
	"path/filepath"
	"testing"
)

// Several tests here read files that belong to the REPOSITORY rather than to
// this package: the frontend sources they hold contracts against, the parity
// documents they check, and the import fixtures under docs/.
//
// `go test` runs with the working directory set to the package directory, so
// while this code lived in the repository root those paths resolved by
// accident. Moving the package under internal/ broke nine tests at once, which
// is the good outcome — a relative path that silently resolved to nothing would
// have left them passing while reading an empty file.
//
// repoRoot walks up to the directory holding go.mod rather than counting ".."
// segments, so it keeps working if this package moves again.
func repoRoot(t testing.TB) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("locating the working directory: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("no go.mod found above %s, so the repository root cannot be located", dir)
		}
		dir = parent
	}
}

// repoPath joins parts onto the repository root and fails if the result does
// not exist. The existence check is the point: a test that reads a moved file
// should fail loudly here rather than proceed with empty content and assert
// nothing.
func repoPath(t testing.TB, parts ...string) string {
	t.Helper()
	full := filepath.Join(append([]string{repoRoot(t)}, parts...)...)
	if _, err := os.Stat(full); err != nil {
		t.Fatalf("expected repository file %s: %v", filepath.Join(parts...), err)
	}
	return full
}
