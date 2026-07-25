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

func (a *App) findCollectionWithWorkspaceLocked(id string) (*Workspace, *Collection, error) {
	for wi := range a.state.Workspaces {
		for ci := range a.state.Workspaces[wi].Collections {
			if a.state.Workspaces[wi].Collections[ci].ID == id {
				return &a.state.Workspaces[wi], &a.state.Workspaces[wi].Collections[ci], nil
			}
		}
	}
	return nil, nil, fmt.Errorf("collection %s not found", id)
}
