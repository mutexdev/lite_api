package main

import "fmt"

// These helpers are called only while App.mu is held. Keeping the exported
// App methods unchanged preserves the Wails binding contract.
func (a *App) findWorkspaceLocked(id string) (*Workspace, error) {
	if id == "" {
		id = a.state.ActiveWorkspaceID
	}
	for i := range a.state.Workspaces {
		if a.state.Workspaces[i].ID == id {
			return &a.state.Workspaces[i], nil
		}
	}
	return nil, fmt.Errorf("workspace %s not found", id)
}

func (a *App) findCollectionLocked(id string) (*Collection, error) {
	_, collection, err := a.findCollectionWithWorkspaceLocked(id)
	return collection, err
}

// The scan lives in lookupCollectionPosition (runner_lookup_index.go) so that
// the runner's O(1) index and this fallback cannot disagree about which
// collection wins when IDs repeat. Behaviour here is unchanged: first match, and
// the same error text.
func (a *App) findCollectionWithWorkspaceLocked(id string) (*Workspace, *Collection, error) {
	position, ok := lookupCollectionPosition(&a.state, id)
	if !ok {
		return nil, nil, fmt.Errorf("collection %s not found", id)
	}
	workspace := &a.state.Workspaces[position.workspace]
	return workspace, &workspace.Collections[position.collection], nil
}
