// Where a script is allowed to read and write.
//
// resolveScriptFSPath is the containment boundary for the fs API exposed to
// pre-request and post-response scripts, and coverage found it at 26%. In safe
// mode a script may only touch files inside its own collection; in developer
// mode the user has explicitly opted out and the whole filesystem is reachable.
//
// A hole here is not a crash. A script from an imported collection — someone
// else's file, opened out of curiosity — reads ~/.ssh/id_rsa or writes to a
// startup directory, and the run looks completely normal.
//
// The interesting part is that confinement CANNOT be done by string prefix:
// symlinks are resolved on both sides first, so a link inside the collection
// pointing out of it is caught. Testing that needs real files, so these use a
// temp dir rather than string fixtures.
package scripting

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSafeModeConfinesScriptsToTheCollection(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "inside.txt"), []byte("ok"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Compare against the SYMLINK-RESOLVED root. On macOS t.TempDir() hands back
	// a path under /var, which is a symlink to /private/var — and the function
	// resolves symlinks on purpose. My first draft compared against the
	// unresolved path and failed on correct code.
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}

	got, err := resolveScriptFSPath(root, "inside.txt", "safe")
	if err != nil {
		t.Fatalf("a file inside the collection was refused: %v", err)
	}
	if !strings.HasPrefix(got, resolvedRoot) {
		t.Fatalf("resolved to %q, outside %q", got, resolvedRoot)
	}

	// A nested path is fine too.
	if _, err := resolveScriptFSPath(root, "sub/dir/file.txt", "safe"); err != nil {
		t.Fatalf("a nested path was refused: %v", err)
	}
}

func TestSafeModeRejectsTraversalAndAbsolutePaths(t *testing.T) {
	root := t.TempDir()

	for _, name := range []string{
		"../outside.txt",
		"../../etc/passwd",
		"sub/../../outside.txt",
		"/etc/passwd",
		filepath.Join(filepath.Dir(root), "sibling.txt"),
	} {
		if got, err := resolveScriptFSPath(root, name, "safe"); err == nil {
			t.Errorf("safe mode accepted %q, resolving to %q — a script could read outside its collection", name, got)
		}
	}
}

// The case a string-prefix check would miss: a symlink INSIDE the collection
// pointing outside it. Both sides are symlink-resolved before comparison, which
// is why this is caught.
func TestSafeModeFollowsSymlinksBeforeDecidingContainment(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(secret, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link.txt")
	if err := os.Symlink(secret, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	if got, err := resolveScriptFSPath(root, "link.txt", "safe"); err == nil {
		t.Fatalf("a symlink out of the collection was accepted, resolving to %q — string-prefix confinement is not enough", got)
	}
}

// A collection with no path cannot confine anything, so safe mode must refuse
// rather than fall back to the process working directory.
func TestSafeModeRefusesWhenTheCollectionHasNoPath(t *testing.T) {
	for _, root := range []string{"", "   "} {
		if _, err := resolveScriptFSPath(root, "any.txt", "safe"); err == nil {
			t.Errorf("an empty collection path was accepted; the sandbox would resolve against the process cwd")
		}
	}
}

// Developer mode is an explicit opt-out, so the same paths must be ALLOWED —
// otherwise the setting does nothing and users would work around it.
func TestDeveloperModeAllowsPathsOutsideTheCollection(t *testing.T) {
	root := t.TempDir()

	for _, name := range []string{"../outside.txt", "/etc/hosts", "sub/file.txt"} {
		got, err := resolveScriptFSPath(root, name, "developer")
		if err != nil {
			t.Errorf("developer mode refused %q: %v", name, err)
			continue
		}
		if !filepath.IsAbs(got) {
			t.Errorf("developer mode returned a relative path %q; callers open it directly", got)
		}
	}

	// And with no collection path at all it still resolves, against the cwd.
	if _, err := resolveScriptFSPath("", "file.txt", "developer"); err != nil {
		t.Errorf("developer mode with no collection path: %v", err)
	}
}

// An unrecognised mode must fall back to SAFE. Defaulting to developer would
// mean a typo, or a collection written by an older version with no mode at all,
// silently opens the whole filesystem.
func TestUnknownSandboxModeIsTreatedAsSafe(t *testing.T) {
	root := t.TempDir()
	// "Developer " is NOT in this list: NormalizeJSSandboxMode trims and
	// lowercases, so it is a recognised mode and opening up is correct. I had it
	// here at first and the code was right to reject the test, not the input.
	for _, mode := range []string{"", "  ", "unknown", "safe", "SAFE", "dev", "developer-ish"} {
		if _, err := resolveScriptFSPath(root, "../outside.txt", mode); err == nil {
			t.Errorf("mode %q allowed traversal; an unrecognised mode must confine, not open up", mode)
		}
	}
}
