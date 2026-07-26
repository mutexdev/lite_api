package main

// US-015 — saving one request writes one request file, not the whole collection.
//
// Observed through file modification time, which is the only signal that
// distinguishes "wrote identical bytes" from "did not write". Content
// assertions cannot tell those apart, and that is precisely the distinction
// this story is about: the old code produced byte-identical files on every
// save, so nothing about the collection's contents ever looked wrong — it just
// rewrote 50+ files each time.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// collectionFileTimes snapshots the modification time of every file under a
// collection directory.
func collectionFileTimes(t *testing.T, root string) map[string]time.Time {
	t.Helper()
	times := map[string]time.Time{}
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			times[path] = info.ModTime()
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	return times
}

func rewrittenFiles(before, after map[string]time.Time) []string {
	var changed []string
	for path, afterTime := range after {
		beforeTime, existed := before[path]
		if !existed || !afterTime.Equal(beforeTime) {
			changed = append(changed, path)
		}
	}
	return changed
}

// buildCollectionForFileWriteTest returns an app with a filesystem-backed
// collection holding several requests and an environment, all already on disk.
func buildCollectionForFileWriteTest(t *testing.T, requestCount int) (app *App, collectionID string, itemIDs []string, root string) {
	t.Helper()
	app = newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	collection := state.Workspaces[0].Collections[0]
	collectionID = collection.ID
	if collection.Path == "" {
		t.Skip("default collection is not filesystem-backed in this fixture")
	}
	root = collection.Path

	for i := range requestCount {
		created, err := app.CreateRequest(collectionID, "http", "req")
		if err != nil {
			t.Fatalf("CreateRequest #%d: %v", i, err)
		}
		itemIDs = itemIDs[:0]
		for _, workspace := range created.Workspaces {
			for _, c := range workspace.Collections {
				if c.ID != collectionID {
					continue
				}
				for _, item := range c.Items {
					itemIDs = append(itemIDs, item.ID)
				}
			}
		}
	}
	// CreateRequest leaves the request as an unsaved draft; SaveRequest is what
	// materialises it on disk, and therefore what runs writeCollectionFilesLocked.
	for _, id := range itemIDs {
		if _, err := app.SaveRequest(collectionID, id); err != nil {
			t.Fatalf("SaveRequest %s: %v", id, err)
		}
	}
	if err := app.flushPersist(); err != nil {
		t.Fatalf("flushPersist: %v", err)
	}
	if len(itemIDs) == 0 {
		t.Fatalf("collection has no items after creating %d requests", requestCount)
	}
	return app, collectionID, itemIDs, root
}

// TestSavingOneRequestDoesNotRewriteTheWholeCollection is the core US-015
// assertion.
func TestSavingOneRequestDoesNotRewriteTheWholeCollection(t *testing.T) {
	app, collectionID, itemIDs, root := buildCollectionForFileWriteTest(t, 8)

	// Settle: the first write after startup legitimately touches every file,
	// because the App has no fingerprint for any of them yet.
	url := "https://settle.example"
	if _, err := app.UpdateRequest(collectionID, itemIDs[0], RequestPatch{URL: &url}); err != nil {
		t.Fatalf("settling UpdateRequest: %v", err)
	}
	if _, err := app.SaveRequest(collectionID, itemIDs[0]); err != nil {
		t.Fatalf("settling SaveRequest: %v", err)
	}
	if err := app.flushPersist(); err != nil {
		t.Fatalf("flushPersist: %v", err)
	}

	before := collectionFileTimes(t, root)
	if len(before) < 3 {
		t.Fatalf("expected a collection with several files on disk, found %d", len(before))
	}
	// Modification times have coarse resolution on some filesystems; make sure
	// a rewrite would be visible rather than landing in the same tick.
	time.Sleep(20 * time.Millisecond)

	changedURL := "https://changed.example"
	if _, err := app.UpdateRequest(collectionID, itemIDs[len(itemIDs)-1], RequestPatch{URL: &changedURL}); err != nil {
		t.Fatalf("UpdateRequest: %v", err)
	}
	if _, err := app.SaveRequest(collectionID, itemIDs[len(itemIDs)-1]); err != nil {
		t.Fatalf("SaveRequest: %v", err)
	}
	if err := app.flushPersist(); err != nil {
		t.Fatalf("flushPersist: %v", err)
	}

	changed := rewrittenFiles(before, collectionFileTimes(t, root))
	if len(changed) != 1 {
		t.Errorf("changing one request rewrote %d files, want exactly 1:\n  %v\n(total files on disk: %d)",
			len(changed), changed, len(before))
	}
}

// TestChangingARequestRewritesItsOwnFile keeps the test above honest. A gate
// that never writes anything satisfies every assertion there while losing the
// user's edit — which is the failure mode that matters, since it is silent
// until the app is restarted.
func TestChangingARequestRewritesItsOwnFile(t *testing.T) {
	app, collectionID, itemIDs, root := buildCollectionForFileWriteTest(t, 4)

	url := "https://settle.example"
	if _, err := app.UpdateRequest(collectionID, itemIDs[0], RequestPatch{URL: &url}); err != nil {
		t.Fatalf("settling UpdateRequest: %v", err)
	}
	if _, err := app.SaveRequest(collectionID, itemIDs[0]); err != nil {
		t.Fatalf("settling SaveRequest: %v", err)
	}
	if err := app.flushPersist(); err != nil {
		t.Fatalf("flushPersist: %v", err)
	}
	before := collectionFileTimes(t, root)
	time.Sleep(20 * time.Millisecond)

	marker := "https://unique-marker.example"
	if _, err := app.UpdateRequest(collectionID, itemIDs[0], RequestPatch{URL: &marker}); err != nil {
		t.Fatalf("UpdateRequest: %v", err)
	}
	if _, err := app.SaveRequest(collectionID, itemIDs[0]); err != nil {
		t.Fatalf("SaveRequest: %v", err)
	}
	if err := app.flushPersist(); err != nil {
		t.Fatalf("flushPersist: %v", err)
	}

	changed := rewrittenFiles(before, collectionFileTimes(t, root))
	if len(changed) == 0 {
		t.Fatal("changing a request URL rewrote nothing; the edit would be lost on restart")
	}
	// And the edit must really be in the bytes, not merely have touched mtime.
	found := false
	for _, path := range changed {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if strings.Contains(string(data), marker) {
			found = true
		}
	}
	if !found {
		t.Errorf("no rewritten file contains the new URL %q; rewritten: %v", marker, changed)
	}
}

// TestRepeatedIdenticalSavesWriteNothing covers the auto-save path, where the
// same content is written over and over. Before US-015 each of these rewrote
// every file in the collection.
func TestRepeatedIdenticalSavesWriteNothing(t *testing.T) {
	app, collectionID, itemIDs, root := buildCollectionForFileWriteTest(t, 6)

	url := "https://stable.example"
	if _, err := app.UpdateRequest(collectionID, itemIDs[0], RequestPatch{URL: &url}); err != nil {
		t.Fatalf("UpdateRequest: %v", err)
	}
	if _, err := app.SaveRequest(collectionID, itemIDs[0]); err != nil {
		t.Fatalf("SaveRequest: %v", err)
	}
	if err := app.flushPersist(); err != nil {
		t.Fatalf("flushPersist: %v", err)
	}
	before := collectionFileTimes(t, root)
	time.Sleep(20 * time.Millisecond)

	// Re-save the same content ten times.
	for i := range 10 {
		if _, err := app.UpdateRequest(collectionID, itemIDs[0], RequestPatch{URL: &url}); err != nil {
			t.Fatalf("UpdateRequest #%d: %v", i, err)
		}
		if _, err := app.SaveRequest(collectionID, itemIDs[0]); err != nil {
			t.Fatalf("SaveRequest #%d: %v", i, err)
		}
	}
	if err := app.flushPersist(); err != nil {
		t.Fatalf("flushPersist: %v", err)
	}

	if changed := rewrittenFiles(before, collectionFileTimes(t, root)); len(changed) != 0 {
		t.Errorf("ten identical saves rewrote %d files: %v", len(changed), changed)
	}
}
