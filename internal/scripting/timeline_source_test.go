package scripting

import (
	"path/filepath"
	"testing"

	"github.com/mutexdev/lite_api/internal/types"
)

// TimelineSourceFileForItem sat at 28.6%. It produces the source label attached
// to every timeline entry — and SaveResponseTimeline writes those entries to a
// file the user chooses and shares.
//
// That makes the relative-path branch a PRIVACY property, not just a
// readability one: an absolute path carries the user's account name and
// directory layout into an exported artifact.

// A request inside the collection is labelled RELATIVE TO IT. Without this the
// exported timeline reads /Users/alice/work/private-client/auth/login.bru,
// which names the user and the client to anyone the file is sent to.
func TestAPathInsideTheCollectionIsRelative(t *testing.T) {
	collection := filepath.Join("/Users", "alice", "work", "acme")
	item := types.RequestItem{FilePath: filepath.Join(collection, "auth", "login.bru")}

	got := TimelineSourceFileForItem(collection, item)
	if got != "auth/login.bru" {
		t.Errorf("got %q, want auth/login.bru — the exported timeline would carry the absolute path", got)
	}
}

// A file outside the collection has no meaningful relative form, so it is shown
// as it is rather than as a chain of "..".
func TestAPathOutsideTheCollectionIsLeftAbsolute(t *testing.T) {
	collection := filepath.Join("/Users", "alice", "work", "acme")
	outside := filepath.Join("/Users", "alice", "elsewhere", "req.bru")

	got := TimelineSourceFileForItem(collection, types.RequestItem{FilePath: outside})
	if got != filepath.ToSlash(outside) {
		t.Errorf("got %q, want the path unchanged", got)
	}
}

// With no collection path there is nothing to be relative to.
func TestWithNoCollectionPathTheItemPathIsUsedAsIs(t *testing.T) {
	item := types.RequestItem{FilePath: filepath.Join("/tmp", "req.bru")}
	if got := TimelineSourceFileForItem("", item); got != "/tmp/req.bru" {
		t.Errorf("got %q", got)
	}
	if got := TimelineSourceFileForItem("   ", item); got != "/tmp/req.bru" {
		t.Errorf("blank collection path: got %q", got)
	}
}

// THE SEPARATOR IS ALWAYS A FORWARD SLASH. A timeline recorded on Windows and
// one recorded on macOS must read the same, or exported files cannot be diffed
// or matched against each other.
//
// A CAVEAT WORTH STATING: on Unix filepath.ToSlash is the IDENTITY function, so
// removing every one of those calls fails nothing here — the branch is only
// observable on Windows, where Join produces backslashes. This suite runs on
// macOS locally and ubuntu in CI, so no test in this repo can pin it.
//
// The Windows cross-compile gate in ci.yml proves this code COMPILES there, not
// that it behaves. That gap is recorded rather than papered over, because the
// alternative — asserting on a hand-built backslash path — would test
// filepath.ToSlash rather than this function.
func TestTheSeparatorIsAlwaysAForwardSlash(t *testing.T) {
	synthesised := TimelineSourceFileForItem("", types.RequestItem{Name: "login", FolderPath: "auth/v2"})
	if synthesised != "auth/v2/login.yml" {
		t.Errorf("synthesised: got %q, want auth/v2/login.yml", synthesised)
	}

	relative := TimelineSourceFileForItem(
		filepath.Join("/c", "col"),
		types.RequestItem{FilePath: filepath.Join("/c", "col", "a", "b.bru")},
	)
	if relative != "a/b.bru" {
		t.Errorf("relative: got %q, want a/b.bru", relative)
	}
}

// A request that has never been saved has no file path, so the label is
// synthesised. Returning nothing would leave every timeline entry for an
// unsaved request unattributed.
func TestAnUnsavedRequestGetsASynthesisedName(t *testing.T) {
	got := TimelineSourceFileForItem("/anything", types.RequestItem{Name: "Create User"})
	if got != "Create User.yml" {
		t.Errorf("got %q, want Create User.yml", got)
	}
}

// The extension is only added when there is none. A request already named
// "config.json" must not become "config.json.yml".
func TestAnExistingExtensionIsNotDoubled(t *testing.T) {
	got := TimelineSourceFileForItem("", types.RequestItem{Name: "config.json"})
	if got != "config.json" {
		t.Errorf("got %q, want config.json", got)
	}
}

// The folder is trimmed of its separators before joining, so a stored
// "/auth/" does not produce "/auth/login.yml" with a leading slash that reads
// as an absolute path.
func TestTheFolderIsTrimmedBeforeJoining(t *testing.T) {
	for _, folder := range []string{"auth", "/auth", "auth/", "/auth/", "  /auth/  "} {
		got := TimelineSourceFileForItem("", types.RequestItem{Name: "login", FolderPath: folder})
		if got != "auth/login.yml" {
			t.Errorf("folder %q gave %q, want auth/login.yml", folder, got)
		}
	}
}

// A request with neither a path nor a name has nothing to label, and an empty
// string is honest about that — better than a bare ".yml".
func TestAnUnnamedUnsavedRequestHasNoSourceFile(t *testing.T) {
	for _, item := range []types.RequestItem{{}, {Name: "   "}, {FilePath: "  "}} {
		if got := TimelineSourceFileForItem("/col", item); got != "" {
			t.Errorf("%#v gave %q, want empty", item, got)
		}
	}
}

// A file path wins over the name: it is what actually exists on disk, and the
// name can differ from it after a rename that has not been saved.
func TestTheFilePathWinsOverTheName(t *testing.T) {
	item := types.RequestItem{FilePath: "/col/actual.bru", Name: "Renamed But Unsaved"}
	if got := TimelineSourceFileForItem("/col", item); got != "actual.bru" {
		t.Errorf("got %q, want actual.bru", got)
	}
}
