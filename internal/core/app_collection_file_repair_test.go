package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeCollectionFileLocked skips a write when the content it would produce
// matches a fingerprint it has already recorded for that path. The fingerprint
// is held in memory and says only "the last thing we wrote here had these
// bytes" — it is NOT evidence that the file is still there.
//
// Collections are plain files in a directory the user owns, and this app ships
// a file watcher precisely because they get edited and removed outside it. Once
// a file has been deleted externally, every later save producing identical
// bytes was skipped, so the file stayed missing while the app's state kept
// showing the request.
//
// Found by testing ResetDemoData: with the default collection's directory
// deleted, the reset recreated the directory and wrote nothing into it.

func collectionDirFor(t *testing.T, state AppState) (string, string) {
	t.Helper()
	for _, collection := range state.Workspaces[0].Collections {
		if !collection.Scratch && strings.TrimSpace(collection.Path) != "" {
			return collection.ID, collection.Path
		}
	}
	t.Fatal("the default workspace has no ordinary collection with a path")
	return "", ""
}

func fileNamesIn(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

// A collection file deleted from outside the app is restored by the next save,
// even though its content has not changed. Before the fix the fingerprint
// matched, the write was skipped, and the file stayed gone.
func TestSavingRestoresACollectionFileDeletedFromOutside(t *testing.T) {
	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collectionID, path := collectionDirFor(t, state)

	// The COLLECTION METADATA file, because the save used below is a collection
	// rename and that is the file it writes. Deleting a request file instead
	// proves nothing here: a rename does not rewrite those, so the file would
	// still be missing afterwards for a reason that has nothing to do with the
	// fingerprint cache.
	victim := filepath.Join(path, collectionMetadataName(t, path))
	if _, err := os.Stat(victim); err != nil {
		t.Fatalf("expected collection metadata at %s: %v", victim, err)
	}
	if err := os.Remove(victim); err != nil {
		t.Fatal(err)
	}

	// A save that produces byte-identical content. Renaming the collection to
	// its current name is the smallest way to ask for one.
	collection := state.Workspaces[0].Collections[0]
	if _, err := app.RenameCollection(collectionID, collection.Name); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(victim); err != nil {
		t.Errorf("%s was deleted outside the app and the next save did not restore it: %v",
			filepath.Base(victim), err)
	}
}

// collectionMetadataName finds the collection's own descriptor file, whose
// name depends on the collection format.
func collectionMetadataName(t *testing.T, dir string) string {
	t.Helper()
	for _, candidate := range []string{"opencollection.yml", "bruno.json", "collection.bru"} {
		if _, err := os.Stat(filepath.Join(dir, candidate)); err == nil {
			return candidate
		}
	}
	t.Fatalf("no collection metadata file in %s; it holds %v", dir, fileNamesIn(t, dir))
	return ""
}

// The whole directory is the same case, and it is the one ResetDemoData hits:
// recreating the directory without its files leaves state pointing at requests
// that are not on disk.
func TestResetRestoresEveryFileOfADeletedCollectionDirectory(t *testing.T) {
	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	_, path := collectionDirFor(t, state)

	before := fileNamesIn(t, path)
	if len(before) == 0 {
		t.Fatal("the default collection wrote no files")
	}
	if err := os.RemoveAll(path); err != nil {
		t.Fatal(err)
	}

	if _, err := app.ResetDemoData(); err != nil {
		t.Fatal(err)
	}

	after := fileNamesIn(t, path)
	if len(after) == 0 {
		t.Fatalf("the reset recreated %s but wrote none of its %d files", path, len(before))
	}
	for _, name := range before {
		found := false
		for _, candidate := range after {
			if candidate == name {
				found = true
			}
		}
		if !found {
			t.Errorf("%s was not restored; the directory now holds %v", name, after)
		}
	}
}

// The skip itself is still worth having, and must survive the fix: a save that
// changes nothing and whose file IS present must not rewrite it. Rewriting on
// every save would touch mtime constantly and wake the file watcher, which is
// the reason the fingerprint cache exists.
func TestSavingSkipsTheWriteWhenTheFileIsPresentAndUnchanged(t *testing.T) {
	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collectionID, path := collectionDirFor(t, state)
	names := fileNamesIn(t, path)
	if len(names) == 0 {
		t.Fatal("the default collection wrote no files")
	}
	target := filepath.Join(path, names[0])

	// A sentinel that a rewrite would destroy. The fingerprint cache says the
	// content is already correct, so a save must leave these bytes alone.
	if err := os.WriteFile(target, []byte("sentinel: untouched\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	collection := state.Workspaces[0].Collections[0]
	if _, err := app.RenameCollection(collectionID, collection.Name); err != nil {
		t.Fatal(err)
	}

	content, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("the file was removed by a no-op save: %v", err)
	}
	if string(content) != "sentinel: untouched\n" {
		t.Error("a save with unchanged content rewrote a file that was already present")
	}
}

// A save that CHANGES content must reach disk after a restart, which is the
// other half of the fix above: the existence check must not turn into a reason
// to skip a genuine change.
//
// It takes the CACHED branch, not the uncached one, and that is worth writing
// down because I assumed otherwise. Startup writes the collection's files and
// so warms the fingerprint cache before any user action, meaning a restarted
// App has the path cached by the time anything is saved.
//
// The uncached branch — first write to a path this App has not written, where
// the file already exists — is reached 123 times by this package's suite, 95 of
// them with differing content, and inverting its comparison is caught. Measured
// by instrumenting the branch and logging to a file, after a first attempt
// using stderr reported zero: non-verbose `go test` discards the output of a
// passing package, so the instrument was silently thrown away.
func TestTheFirstSaveAfterARestartReachesDisk(t *testing.T) {
	dir := t.TempDir()
	app := newAppInDirForTest(t, dir)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collectionID, path := collectionDirFor(t, state)
	metadata := filepath.Join(path, collectionMetadataName(t, path))
	flushPersistForTest(t, app)

	original, err := os.ReadFile(metadata)
	if err != nil {
		t.Fatal(err)
	}

	// A FRESH App over the same directory: its fingerprint cache is empty, so
	// the save below takes the uncached branch.
	restarted := newAppInDirForTest(t, dir)
	if _, err := restarted.GetState(); err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.RenameCollection(collectionID, "Renamed After Restart"); err != nil {
		t.Fatal(err)
	}
	flushPersistForTest(t, restarted)

	updated, err := os.ReadFile(metadata)
	if err != nil {
		t.Fatalf("the collection metadata is gone after a rename: %v", err)
	}
	if string(updated) == string(original) {
		t.Error("the first save after a restart did not reach disk; the file still holds the pre-rename content")
	}
	if !strings.Contains(string(updated), "Renamed After Restart") {
		t.Errorf("the new name is not in the written file:\n%s", updated)
	}
}
