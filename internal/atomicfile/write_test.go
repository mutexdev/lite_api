package atomicfile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Write had no test of its own. It is the function that writes state.json, the
// encrypted environment secrets, the response store and the history file — five
// call sites, every one of them a file whose truncation loses user data — and
// it was covered only incidentally through those callers.

func TestWriteStoresContentAndMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := Write(path, []byte(`{"ok":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"ok":true}` {
		t.Errorf("got %q", got)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("mode %o, want 600", mode)
	}
}

// The mode is a PARAMETER here and fixed at 0600 in WritePrivate. That is one
// of the two reasons both functions exist, so a caller asking for something
// else must actually get it.
func TestWriteHonoursTheRequestedMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shared")
	if err := Write(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o644 {
		t.Errorf("mode %o, want 644 — the requested mode was overridden", mode)
	}
}

// THE OTHER REASON BOTH FUNCTIONS EXIST. WritePrivate creates its parent and
// re-chmods it to 0700; Write does neither, because it is used on directories
// whose mode is already meaningful. Collapsing the two — which is exactly what
// someone deduplicating them would do — would silently tighten the permissions
// of a directory the caller deliberately left open.
func TestWriteLeavesTheParentDirectoryAlone(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "loose")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := Write(filepath.Join(dir, "f"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o755 {
		t.Errorf("parent became %o; Write must not re-chmod a directory whose mode is deliberate", mode)
	}
}

// And it does not CREATE one either. A missing parent is an error rather than
// something to conjure, because a caller writing into a directory that should
// already exist has a bug worth surfacing — not a tree to be silently built.
func TestWriteDoesNotCreateAMissingParent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nope", "f")
	if err := Write(path, []byte("x"), 0o600); err == nil {
		t.Fatal("writing into a missing directory was allowed")
	}
	if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
		t.Error("the missing parent was created")
	}
}

func TestWriteReplacesExistingContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f")
	if err := Write(path, []byte("first"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Write(path, []byte("second"), 0o600); err != nil {
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

// The whole point of the temp-then-rename. A failed write must leave the
// PREVIOUS file complete, which is what a bare os.WriteFile cannot promise —
// it truncates first and can be killed before it finishes.
func TestWriteFailureLeavesThePreviousFileIntact(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	if err := Write(path, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}

	// A directory at the destination: everything up to the rename succeeds, and
	// a rename cannot replace a directory with a file.
	blocked := filepath.Join(dir, "blocked")
	if err := os.Mkdir(blocked, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := Write(blocked, []byte("replacement"), 0o600); err == nil {
		t.Fatal("renaming a file over a directory was allowed")
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "original" {
		t.Errorf("got %q; a failed write damaged an unrelated file", got)
	}
}

// A temp file left behind is a full copy of whatever was being written — the
// app state, or the encrypted secrets — sitting under a dotfile name where
// nothing will ever look for it or clean it up.
func TestWriteLeavesNoTemporaryFile(t *testing.T) {
	dir := t.TempDir()
	if err := Write(filepath.Join(dir, "f"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		var names []string
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		t.Errorf("directory holds %v, want just the target", names)
	}
}

func TestWriteRemovesTheTempFileWhenTheRenameFails(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}

	if err := Write(target, []byte("secret"), 0o600); err == nil {
		t.Fatal("renaming a file over a directory was allowed")
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), ".tmp-") {
			t.Errorf("temporary file %q survived a failed write, holding the content in plaintext", entry.Name())
		}
	}
}

func TestWriteHandlesEmptyContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f")
	if err := Write(path, nil, 0o600); err != nil {
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

// THE ONE BRANCH THESE TESTS CANNOT REACH, recorded so it does not read as an
// oversight.
//
// Write checks the error from f.Close() rather than deferring and ignoring it,
// because US-003 found a truncated copy reported as SUCCESS when that error was
// dropped. Removing the check fails none of the tests above, and it cannot be
// made to fail one: Close on a synced regular file returns nil on every
// filesystem a test can create here, and the only way to make it error — closing
// twice — is not reachable through this function's API.
//
// The branch is still reachable in PRODUCTION. Close is where a filesystem
// reports a deferred write error: a full disk, a quota, an NFS server that
// accepted the bytes and then refused them. Those are exactly the conditions
// under which a truncated state.json gets reported as saved.
//
// The alternative would be an injectable seam for the file handle. That is
// deliberately not done — the restructure plan rejected settable function vars
// as "exactly the shape of a seam production forgets to wire", and a seam
// guarding a guard is worse value than a comment saying why the guard is there.
//
// So this is recorded rather than tested, and the guard stays.
func TestWriteClosePathIsDocumentedRatherThanTested(t *testing.T) {
	// The assertion that CAN be made: a successful write is always complete.
	// It is weaker than the property above, but it is the observable half.
	path := filepath.Join(t.TempDir(), "state.json")
	payload := strings.Repeat("x", 1<<16)
	if err := Write(path, []byte(payload), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(payload) {
		t.Errorf("wrote %d bytes of %d and reported success", len(got), len(payload))
	}
}
