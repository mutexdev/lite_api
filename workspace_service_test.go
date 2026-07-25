package main

import "testing"

func TestWorkspaceLookupHelpersPreserveActiveFallbackAndCollectionOwnership(t *testing.T) {
	app := NewAppWithDir(t.TempDir())
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	app.mu.Lock()
	ws, err := app.findWorkspaceLocked("")
	if err != nil || ws.ID != state.ActiveWorkspaceID {
		app.mu.Unlock()
		t.Fatalf("active fallback %#v %v", ws, err)
	}
	collection := ws.Collections[0]
	owner, found, err := app.findCollectionWithWorkspaceLocked(collection.ID)
	app.mu.Unlock()
	if err != nil || owner.ID != ws.ID || found.ID != collection.ID {
		t.Fatalf("collection ownership %#v %#v %v", owner, found, err)
	}
}
