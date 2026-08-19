package atomicfile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The file this writes holds OAuth2 tokens, encrypted environment secrets and
// the workspace registry. A mode that lets another local account read it is the
// whole failure, and it produces no error and no visible symptom.
func TestWritePrivateGivesTheFileAndItsParentOwnerOnlyModes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "secrets.json")
	if err := WritePrivate(path, []byte("token")); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("file mode %o, want 600", mode)
	}
	dirInfo, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if mode := dirInfo.Mode().Perm(); mode != 0o700 {
		t.Errorf("parent mode %o, want 700", mode)
	}
}

// MkdirAll is a no-op on a directory that already exists, so without the
// explicit Chmod a pre-existing world-readable parent keeps its mode and
// exposes every secret written into it afterwards.
func TestWritePrivateTightensAnAlreadyOpenParent(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "loose")
	if err := os.MkdirAll(dir, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o777); err != nil {
		t.Fatal(err)
	}

	if err := WritePrivate(filepath.Join(dir, "f"), []byte("x")); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o700 {
		t.Errorf("parent stayed %o; a pre-existing loose directory must be tightened", mode)
	}
}

func TestWritePrivateReplacesExistingContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f")
	if err := WritePrivate(path, []byte("first")); err != nil {
		t.Fatal(err)
	}
	if err := WritePrivate(path, []byte("second")); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "second" {
		t.Errorf("got %q", got)
	}
}

// The temp file must not survive a successful write. One left behind in the
// data directory is a plaintext copy of whatever was just written privately.
func TestWritePrivateLeavesNoTemporaryFile(t *testing.T) {
	dir := t.TempDir()
	if err := WritePrivate(filepath.Join(dir, "f"), []byte("x")); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".tmp-") {
			t.Errorf("temporary file %q survived the write", entry.Name())
		}
	}
	if len(entries) != 1 {
		t.Errorf("directory holds %d entries, want just the target", len(entries))
	}
}

// The temp cleanup matters on the FAILURE path, not the success path — on
// success the temp has already been renamed away, so the deferred Remove is a
// no-op there. A write that gets as far as creating and filling the temp file
// and then fails to rename it leaves a full plaintext copy of the secret behind
// under a dotfile name, where nothing will ever look for it.
//
// Testing only the success path missed this: deleting the cleanup entirely
// failed no test until this one existed.
func TestWritePrivateRemovesTheTempFileWhenTheRenameFails(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	// A directory at the destination: everything up to the rename succeeds, and
	// the rename cannot replace a directory with a file.
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}

	if err := WritePrivate(target, []byte("secret")); err == nil {
		t.Fatal("renaming a file over a directory was allowed")
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".tmp-") {
			t.Errorf("temporary file %q survived a failed write, holding the content in plaintext", entry.Name())
		}
	}
}

// A failed write must not damage what is already on disk. Writing in place
// would leave the previous secrets truncated when the new ones cannot be
// written.
//
// The fault has to be one WritePrivate cannot undo. Making the parent
// unwritable does not work — the function chmods its parent to 0700 on the way
// in, which is exactly what TestWritePrivateTightensAnAlreadyOpenParent
// asserts, so it cheerfully repairs the setup and succeeds. I had that wrong
// first and the test skipped every run, which is worth no more than no test.
//
// A parent that is a regular FILE cannot be repaired: MkdirAll fails and the
// write aborts before creating anything.
func TestWritePrivateFailureLeavesNeighboursIntact(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "keep")
	if err := WritePrivate(blocker, []byte("original")); err != nil {
		t.Fatal(err)
	}

	// "keep" is a file, so "keep/child" has a non-directory parent.
	if err := WritePrivate(filepath.Join(blocker, "child"), []byte("replacement")); err == nil {
		t.Fatal("writing through a regular file as a directory was allowed")
	}

	got, err := os.ReadFile(blocker)
	if err != nil {
		t.Fatalf("the existing file is gone: %v", err)
	}
	if string(got) != "original" {
		t.Errorf("got %q; a failed write damaged an existing file", got)
	}
	info, err := os.Stat(blocker)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("mode became %o; a failed write changed an existing file's permissions", mode)
	}
}

func TestWritePrivateHandlesEmptyContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f")
	if err := WritePrivate(path, nil); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("got %q, want empty", got)
	}
}
