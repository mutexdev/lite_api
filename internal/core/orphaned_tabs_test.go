package core

// An open tab that names a request nothing answers to is worse than a broken
// tab: the frontend resolves a tab by looking its request up and falling back,
// so the tab rendered a DIFFERENT request's content under the missing request's
// name, and anything typed there was saved against whatever it had fallen back
// to. Nothing revalidated the (collectionId, itemId) pairs, so the tab survived
// a state reload and a collection refresh alike.

import (
	"os"
	"path/filepath"
	"testing"
)

func twoRequestCollectionFixture(t *testing.T) (app *App, collection Collection, secondPath string) {
	t.Helper()
	root := t.TempDir()
	collectionPath := filepath.Join(root, "Orphan Tabs")
	if err := os.MkdirAll(collectionPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(collectionPath, "bruno.json"), []byte(`{"version":"1","name":"Orphan Tabs","type":"collection"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	writeWatcherRequestFile(t, filepath.Join(collectionPath, "first.bru"), "First", "https://example.test/first", 1)
	secondPath = filepath.Join(collectionPath, "second.bru")
	writeWatcherRequestFile(t, secondPath, "Second", "https://example.test/second", 2)

	app = newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	state, err = app.OpenCollection(state.Workspaces[0].ID, collectionPath)
	if err != nil {
		t.Fatal(err)
	}
	collection = state.Workspaces[0].Collections[len(state.Workspaces[0].Collections)-1]
	if len(collection.Items) != 2 {
		t.Fatalf("fixture expected two requests, got %d", len(collection.Items))
	}
	return app, collection, secondPath
}

func openTabIDs(state AppState) []string {
	ids := make([]string, 0, len(state.OpenTabs))
	for _, tab := range state.OpenTabs {
		ids = append(ids, tab.ID)
	}
	return ids
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestRefreshCollectionPrunesTabsForRequestsThatAreGone(t *testing.T) {
	app, collection, secondPath := twoRequestCollectionFixture(t)

	first, second := collection.Items[0], collection.Items[1]
	if _, err := app.OpenRequestTab(collection.ID, first.ID); err != nil {
		t.Fatalf("OpenRequestTab(first): %v", err)
	}
	state, err := app.OpenRequestTab(collection.ID, second.ID)
	if err != nil {
		t.Fatalf("OpenRequestTab(second): %v", err)
	}
	secondTabID := collection.ID + ":" + second.ID
	if !containsString(openTabIDs(state), secondTabID) {
		t.Fatalf("fixture did not open the second request's tab: %v", openTabIDs(state))
	}
	if state.ActiveTabID != secondTabID {
		t.Fatalf("fixture expected the second tab to be active, got %q", state.ActiveTabID)
	}

	if err := os.Remove(secondPath); err != nil {
		t.Fatalf("remove the second request file: %v", err)
	}

	state, err = app.RefreshCollection(collection.ID)
	if err != nil {
		t.Fatalf("RefreshCollection: %v", err)
	}
	if containsString(openTabIDs(state), secondTabID) {
		t.Fatalf("the tab for the deleted request survived the refresh and now renders another request: %v", openTabIDs(state))
	}
	if state.ActiveTabID == secondTabID {
		t.Fatalf("the active tab still points at the deleted request: %q", state.ActiveTabID)
	}
	if state.ActiveTabID == "" && len(state.OpenTabs) > 0 {
		t.Fatalf("pruning cleared the active tab while %d tabs remain", len(state.OpenTabs))
	}
	if !containsString(openTabIDs(state), collection.ID+":"+first.ID) {
		t.Fatalf("pruning removed a tab whose request still exists: %v", openTabIDs(state))
	}
}

func TestStateLoadPrunesTabsForRequestsThatAreGone(t *testing.T) {
	app, collection, _ := twoRequestCollectionFixture(t)

	first := collection.Items[0]
	if _, err := app.OpenRequestTab(collection.ID, first.ID); err != nil {
		t.Fatalf("OpenRequestTab: %v", err)
	}

	// A tab naming a request that is not in the collection is exactly what a
	// state file written before an external edit contains.
	orphanTabID := collection.ID + ":request-that-no-longer-exists"
	app.mu.Lock()
	app.state.OpenTabs = append(app.state.OpenTabs, OpenTab{
		ID:             orphanTabID,
		CollectionID:   collection.ID,
		ItemID:         "request-that-no-longer-exists",
		Kind:           "request",
		RequestPaneTab: "params",
		ResponseTab:    "response",
	})
	app.state.ActiveTabID = orphanTabID
	app.mu.Unlock()

	state, err := app.GetState()
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if containsString(openTabIDs(state), orphanTabID) {
		t.Fatalf("the orphaned tab survived the load: %v", openTabIDs(state))
	}
	if state.ActiveTabID == orphanTabID {
		t.Fatalf("the active tab still points at the missing request: %q", state.ActiveTabID)
	}
	if state.ActiveTabID == "" && len(state.OpenTabs) > 0 {
		t.Fatalf("pruning cleared the active tab while %d tabs remain", len(state.OpenTabs))
	}
}

// A response-example tab whose example is gone is the same orphan, and the
// example rules are what makes it a separate check: the request still exists.
func TestStateLoadPrunesResponseExampleTabsWhoseExampleIsGone(t *testing.T) {
	app, collection, _ := twoRequestCollectionFixture(t)
	first := collection.Items[0]

	orphanTabID := responseExampleTabID(collection.ID, first.ID, "example-that-never-existed")
	app.mu.Lock()
	app.state.OpenTabs = append(app.state.OpenTabs, OpenTab{
		ID:             orphanTabID,
		CollectionID:   collection.ID,
		ItemID:         first.ID,
		Kind:           "response-example",
		ExampleID:      "example-that-never-existed",
		RequestPaneTab: "params",
		ResponseTab:    "examples",
	})
	app.mu.Unlock()

	state, err := app.GetState()
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if containsString(openTabIDs(state), orphanTabID) {
		t.Fatalf("the orphaned response-example tab survived the load: %v", openTabIDs(state))
	}
}
