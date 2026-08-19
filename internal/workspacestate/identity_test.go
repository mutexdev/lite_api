package workspacestate

import (
	"path/filepath"
	"strings"
	"testing"
)

// A workspace identity names a directory on disk and is used as a filename
// component for the lock and the registry. A traversal sequence reaching either
// would let one workspace's record address another's.
func TestCanonicalIdentityRejectsTraversalAndRubbish(t *testing.T) {
	for name, value := range map[string]string{
		"empty":             "",
		"whitespace only":   "   ",
		"null byte":         "a\x00b",
		"parent segment":    "../other",
		"nested parent":     "a/../../other",
		"windows separator": `a\..\other`,
		"bare parent":       "..",
	} {
		if _, err := CanonicalWorkspaceIdentity(value); err == nil {
			t.Errorf("%s: %q was accepted", name, value)
		}
	}
}

func TestCanonicalIdentityAcceptsOrdinaryPaths(t *testing.T) {
	for _, value := range []string{"/repos/billing", "workspace-1", "a/b/c", " /repos/billing "} {
		if _, err := CanonicalWorkspaceIdentity(value); err != nil {
			t.Errorf("%q was rejected: %v", value, err)
		}
	}
}

// A dot segment is not traversal — "a/./b" is "a/b" — so rejecting it would
// refuse a path that names a perfectly ordinary directory.
func TestCanonicalIdentityAllowsDotSegments(t *testing.T) {
	if _, err := CanonicalWorkspaceIdentity("a/./b"); err != nil {
		t.Errorf("a single-dot segment was rejected: %v", err)
	}
}

// Two spellings of one directory must compare equal, or a second window opens
// the same workspace believing it is a different one and both write to it.
func TestSamePathIsSpellingInsensitive(t *testing.T) {
	if !SameCanonicalWorkspacePath("/repos/billing", " /repos/billing ") {
		t.Error("surrounding whitespace made two spellings of one path differ")
	}
	if !SameCanonicalWorkspacePath("/repos/billing", "/repos/billing") {
		t.Error("a path did not equal itself")
	}
}

func TestSamePathDistinguishesDifferentWorkspaces(t *testing.T) {
	if SameCanonicalWorkspacePath("/repos/billing", "/repos/reporting") {
		t.Error("two different workspaces compared equal")
	}
}

// An unusable identity must not compare equal to anything, including another
// unusable one. Returning true for two rejected values would make every
// malformed path look like the same workspace.
func TestSamePathIsFalseWhenEitherSideIsInvalid(t *testing.T) {
	if SameCanonicalWorkspacePath("", "") {
		t.Error("two invalid identities compared equal; every malformed path would look like one workspace")
	}
	if SameCanonicalWorkspacePath("/repos/billing", "../escape") {
		t.Error("a valid path compared equal to an invalid one")
	}
	if SameCanonicalWorkspacePath("../escape", "/repos/billing") {
		t.Error("argument order changed the answer")
	}
}

// The identity is used as a filename component, so it must not carry a
// separator that would turn it into a nested path.
func TestCanonicalIdentityIsUsableAsAFilenameComponent(t *testing.T) {
	identity, err := CanonicalWorkspaceIdentity("/repos/billing")
	if err != nil {
		t.Fatal(err)
	}
	joined := filepath.Join("/data", identity)
	if !strings.HasPrefix(filepath.Clean(joined), filepath.Clean("/data")) {
		t.Errorf("identity %q escapes its parent when joined", identity)
	}
}
