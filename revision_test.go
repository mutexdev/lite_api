package main

// US-008 — AppState.Revision is a monotonic mutation counter.
//
// The contract has three halves and each is easy to break silently:
//
//   1. Every mutation bumps it. A mutator that skips the bump is the dangerous
//      failure: the frontend concludes it is in sync when it is not, and shows
//      stale data indefinitely. So the coverage test below drives a broad set of
//      real bindings rather than a hand-picked pair.
//   2. No read bumps it. A read that bumps produces a phantom gap, and the
//      frontend answers a phantom gap with a full AppState refetch — the exact
//      cost US-014 exists to remove.
//   3. It never goes backwards. Several paths restore a whole earlier AppState
//      (the collection-import rollbacks); a counter living inside that value
//      would travel backwards with it, so the authoritative counter lives on
//      the App.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// revisionOf reads the revision through the same binding the frontend uses.
func revisionOf(t *testing.T, app *App) int64 {
	t.Helper()
	state, err := app.GetState()
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	return state.Revision
}

// TestRevisionDoesNotAdvanceOnReads pins criterion 2. GetState calls
// ensureReadyLocked, which mutates unconditionally — it reassigns Cookies via
// pruneExpiredCookiesLocked and rewrites NotFoundLocally via
// refreshGitCollectionAvailabilityLocked — so "this function writes to a.state"
// is not the same question as "this function is a mutation", and only a test
// that repeats a read can tell the two apart.
func TestRevisionDoesNotAdvanceOnReads(t *testing.T) {
	app := newAppForTest(t)

	// Settle first: the very first GetState performs one-time readiness
	// normalisation (default features, scratch collections, environment
	// hydration) that legitimately persists and therefore legitimately bumps.
	settled := revisionOf(t, app)

	for i := range 5 {
		if got := revisionOf(t, app); got != settled {
			t.Fatalf("GetState #%d advanced the revision: %d -> %d (reads must never bump)", i+1, settled, got)
		}
	}

	// Other read-only bindings must be equally quiet.
	if _, err := app.ListWorkspaceWindowTargets(); err == nil {
		if got := revisionOf(t, app); got != settled {
			t.Fatalf("ListWorkspaceWindowTargets advanced the revision: %d -> %d", settled, got)
		}
	}
	_ = app.appTLSSettingsSnapshot()
	_ = app.oauth2ShouldUseSystemBrowser()
	if got := revisionOf(t, app); got != settled {
		t.Fatalf("snapshot readers advanced the revision: %d -> %d", settled, got)
	}
}

// TestRevisionAdvancesOnEveryMutation pins criterion 1 across a wide spread of
// bindings, deliberately covering both persistence routes: markDirty (the
// deferred path most mutators take) and persistLocked (the synchronous path
// imports, recovery and draft-guard saves take). US-008 has to bump on both, and
// a test that only exercised markDirty would miss half the mutators.
func TestRevisionAdvancesOnEveryMutation(t *testing.T) {
	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if len(state.Workspaces) == 0 || len(state.Workspaces[0].Collections) == 0 {
		t.Fatalf("fixture app has no collection to mutate")
	}
	collectionID := state.Workspaces[0].Collections[0].ID
	preferences := state.Preferences

	created, err := app.CreateRequest(collectionID, "http", "revision probe")
	if err != nil {
		t.Fatalf("CreateRequest: %v", err)
	}
	requestID := ""
	for _, workspace := range created.Workspaces {
		for _, collection := range workspace.Collections {
			if collection.ID != collectionID {
				continue
			}
			for _, item := range collection.Items {
				if item.Name == "revision probe" {
					requestID = item.ID
				}
			}
		}
	}
	if requestID == "" {
		t.Fatalf("could not locate the request just created")
	}

	mutations := []struct {
		name string
		run  func() error
	}{
		{"CreateRequest", func() error {
			_, err := app.CreateRequest(collectionID, "http", "another")
			return err
		}},
		{"SaveCookie", func() error {
			_, err := app.SaveCookie(CookieInput{Domain: "example.test", Name: "a", Value: "b", Path: "/"})
			return err
		}},
		{"UpdatePreferences", func() error {
			next := preferences
			next.OAuth2UseSystemBrowser = !next.OAuth2UseSystemBrowser
			preferences = next
			_, err := app.UpdatePreferences(next)
			return err
		}},
		{"UpdateCollectionProxy", func() error {
			_, err := app.UpdateCollectionProxy(collectionID, ProxyConfig{
				Protocol: "http", Hostname: "127.0.0.1", Port: "8080",
			})
			return err
		}},
		{"UpdateCollectionClientCertificates", func() error {
			_, err := app.UpdateCollectionClientCertificates(collectionID, []ClientCertificateConfig{
				{Domain: "example.test", Type: "cert", CertFilePath: "/tmp/c.pem"},
			})
			return err
		}},
		// A deletion, not just a create/update: the bump must not be tied to
		// state growing.
		{"ClearCookies", func() error {
			_, err := app.ClearCookies()
			return err
		}},
	}

	previous := revisionOf(t, app)
	for _, mutation := range mutations {
		if err := mutation.run(); err != nil {
			t.Fatalf("%s: %v", mutation.name, err)
		}
		got := revisionOf(t, app)
		if got <= previous {
			t.Errorf("%s did not advance the revision: stayed at %d", mutation.name, got)
		}
		previous = got
	}
}

// TestRevisionIsMonotonicAcrossManyMutations is the blunt property check: over a
// long run of mixed mutations and reads the sequence must be non-decreasing, and
// must strictly increase whenever a mutation happened.
func TestRevisionIsMonotonicAcrossManyMutations(t *testing.T) {
	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	collectionID := state.Workspaces[0].Collections[0].ID

	previous := revisionOf(t, app)
	for i := range 25 {
		if _, err := app.CreateRequest(collectionID, "http", "monotonic"); err != nil {
			t.Fatalf("CreateRequest #%d: %v", i, err)
		}
		afterWrite := revisionOf(t, app)
		if afterWrite <= previous {
			t.Fatalf("iteration %d: revision did not advance past %d (got %d)", i, previous, afterWrite)
		}
		// Interleave reads; they must hold the line exactly.
		if afterRead := revisionOf(t, app); afterRead != afterWrite {
			t.Fatalf("iteration %d: an interleaved read moved the revision %d -> %d", i, afterWrite, afterRead)
		}
		previous = afterWrite
	}
}

// TestRevisionIsNotPersisted pins the design decision recorded on
// AppState.Revision: the counter is per-App-instance and must not survive a
// restart. Under the multi-window shared-state model a window that reloaded a
// revision written by another window would see the number jump backwards, which
// is worse than starting from zero — the frontend fetches the full state on boot
// regardless.
func TestRevisionIsNotPersisted(t *testing.T) {
	dir := t.TempDir()
	app := newAppInDirForTest(t, dir)
	state, err := app.GetState()
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	collectionID := state.Workspaces[0].Collections[0].ID
	for range 3 {
		if _, err := app.CreateRequest(collectionID, "http", "persisted?"); err != nil {
			t.Fatalf("CreateRequest: %v", err)
		}
	}
	if err := app.flushPersist(); err != nil {
		t.Fatalf("flushPersist: %v", err)
	}
	if live := revisionOf(t, app); live == 0 {
		t.Fatalf("revision should be non-zero in memory after three mutations")
	}

	data, err := os.ReadFile(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatalf("read state.json: %v", err)
	}
	var stored AppState
	if err := json.Unmarshal(data, &stored); err != nil {
		t.Fatalf("parse state.json: %v", err)
	}
	if stored.Revision != 0 {
		t.Errorf("state.json carries revision %d; it must be scrubbed by stateForStorage", stored.Revision)
	}
}
