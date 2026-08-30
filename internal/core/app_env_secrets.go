package core

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mutexdev/lite_api/internal/atomicfile"
	"github.com/mutexdev/lite_api/internal/envsecrets"
	"github.com/mutexdev/lite_api/internal/scripting"
	"github.com/mutexdev/lite_api/internal/store/bru"
)

func (a *App) environmentSecretsPath() string {
	return filepath.Join(a.dataDir, "secrets.json")
}

func (a *App) readEnvironmentSecretsLocked() (envsecrets.File, error) {
	path := a.environmentSecretsPath()
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return envsecrets.File{}, nil
		}
		return envsecrets.File{}, err
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return envsecrets.File{}, nil
	}
	var store envsecrets.File
	if err := json.Unmarshal(data, &store); err != nil {
		return envsecrets.File{}, fmt.Errorf("parse secrets.json: %w", err)
	}
	if store.Collections == nil {
		store.Collections = []envsecrets.CollectionEntry{}
	}
	return store, nil
}

func (a *App) writeEnvironmentSecretsLocked(store envsecrets.File) error {
	if err := os.MkdirAll(a.dataDir, 0o755); err != nil {
		return err
	}
	if store.Collections == nil {
		store.Collections = []envsecrets.CollectionEntry{}
	}
	if store.Workspaces == nil {
		store.Workspaces = []envsecrets.WorkspaceEntry{}
	}
	data, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return err
	}
	path := a.environmentSecretsPath()
	// US-013. Skip unchanged content. persistEnvironmentSecretsLocked runs on
	// the keystroke path, where secrets almost never change.
	//
	// The gate is an in-memory fingerprint of what this App last wrote, not a
	// re-read of the file, so the common case costs a hash rather than a read
	// plus a compare. The file is still read once — when the fingerprint is
	// empty, i.e. before this App has written it — so a process that starts up
	// and persists without touching a secret still does not rewrite the file.
	//
	// Skipping is SAFE UNDER MULTIPLE WINDOWS, and the direction matters: if
	// another window has rewritten secrets.json since our last write, our
	// fingerprint still describes content identical to what we would produce,
	// so skipping leaves their newer file intact. Writing is the operation that
	// could clobber; declining to write cannot.
	fingerprint := fmt.Sprintf("%x", sha256.Sum256(data))
	if a.secretsFingerprint != "" {
		if fingerprint == a.secretsFingerprint {
			return nil
		}
	} else if existing, readErr := os.ReadFile(path); readErr == nil && bytes.Equal(existing, data) {
		a.secretsFingerprint = fingerprint
		return nil
	}
	// Atomic for the same reason state.json is: a half-written secrets.json is
	// every environment secret in the workspace, unrecoverable.
	if err := atomicfile.Write(path, data, 0o600); err != nil {
		// Leave the fingerprint alone on failure. Recording it here would make
		// the next call skip a write that never landed.
		return err
	}
	a.secretsFingerprint = fingerprint
	return nil
}

func (a *App) prepareWorkspaceGlobalEnvironmentsLocked() (bool, error) {
	changed := false
	if len(a.state.Workspaces) == 0 {
		return false, nil
	}
	if len(a.state.GlobalEnvironments) > 0 {
		ws, err := a.findWorkspaceLocked(a.state.ActiveWorkspaceID)
		if err != nil {
			ws = &a.state.Workspaces[0]
		}
		if len(ws.GlobalEnvironments) == 0 {
			ws.GlobalEnvironments = append([]Environment(nil), a.state.GlobalEnvironments...)
			if ws.ActiveGlobalEnvironmentID == "" && len(ws.GlobalEnvironments) > 0 {
				ws.ActiveGlobalEnvironmentID = ws.GlobalEnvironments[0].ID
			}
		}
		a.state.GlobalEnvironments = nil
		changed = true
	}
	for wi := range a.state.Workspaces {
		ws := &a.state.Workspaces[wi]
		if strings.TrimSpace(ws.Path) != "" {
			loaded, err := readWorkspaceGlobalEnvironments(ws.Path)
			if err != nil {
				return changed, err
			}
			if len(loaded) > 0 {
				ws.GlobalEnvironments = bru.MergeEnvironments(ws.GlobalEnvironments, loaded)
			}
			if ws.ActiveGlobalEnvironmentID == "" || !scripting.WorkspaceHasGlobalEnvironment(*ws, ws.ActiveGlobalEnvironmentID) {
				migrated, err := migrateWorkspaceActiveGlobalEnvironmentFromConfig(ws)
				if err != nil {
					return changed, err
				}
				if migrated {
					changed = true
				}
			}
		}
		if !scripting.WorkspaceHasGlobalEnvironment(*ws, ws.ActiveGlobalEnvironmentID) {
			ws.ActiveGlobalEnvironmentID = ""
			changed = true
		}
	}
	return changed, nil
}

func (a *App) storeStateEnvironmentSecretsLocked() error {
	store, err := a.readEnvironmentSecretsLocked()
	if err != nil {
		return err
	}
	for wi := range a.state.Workspaces {
		envsecrets.UpsertWorkspace(&store, a.dataDir, &a.state.Workspaces[wi])
		for ci := range a.state.Workspaces[wi].Collections {
			envsecrets.UpsertCollection(&store, a.dataDir, &a.state.Workspaces[wi].Collections[ci])
		}
	}
	return a.writeEnvironmentSecretsLocked(store)
}

func (a *App) storeCollectionEnvironmentSecretsLocked(collection *Collection) error {
	store, err := a.readEnvironmentSecretsLocked()
	if err != nil {
		return err
	}
	envsecrets.UpsertCollection(&store, a.dataDir, collection)
	return a.writeEnvironmentSecretsLocked(store)
}

func (a *App) hydrateStateEnvironmentSecretsLocked() error {
	for wi := range a.state.Workspaces {
		if err := a.hydrateWorkspaceEnvironmentSecretsLocked(&a.state.Workspaces[wi]); err != nil {
			return err
		}
		for ci := range a.state.Workspaces[wi].Collections {
			if err := a.hydrateCollectionEnvironmentSecretsLocked(&a.state.Workspaces[wi].Collections[ci]); err != nil {
				return err
			}
		}
	}
	return nil
}

func (a *App) hydrateWorkspaceEnvironmentSecretsLocked(workspace *Workspace) error {
	if workspace == nil || strings.TrimSpace(workspace.Path) == "" || len(workspace.GlobalEnvironments) == 0 {
		return nil
	}
	store, err := a.readEnvironmentSecretsLocked()
	if err != nil {
		return err
	}
	workspacePath := envsecrets.NormalizedPath(workspace.Path)
	var stored *envsecrets.WorkspaceEntry
	for index := range store.Workspaces {
		if store.Workspaces[index].Path == workspacePath {
			stored = &store.Workspaces[index]
			break
		}
	}
	if stored == nil {
		return nil
	}
	// Reported rather than returned: hydration completes for every readable
	// secret, and failing the load would take out the whole workspace over one
	// bad entry. An error-level notification is where an unreadable secret
	// becomes visible instead of silently arriving as a blank value.
	if err := envsecrets.Hydrate(a.dataDir, workspace.GlobalEnvironments, stored.Environments); err != nil {
		a.notifyChangedLocked("global-environment-secrets:"+workspace.ID, "error",
			"Global environments in "+workspace.Name+": "+err.Error())
	}
	return nil
}

func (a *App) hydrateCollectionEnvironmentSecretsLocked(collection *Collection) error {
	if collection == nil || strings.TrimSpace(collection.Path) == "" || len(collection.Environments) == 0 {
		return nil
	}
	store, err := a.readEnvironmentSecretsLocked()
	if err != nil {
		return err
	}
	collectionPath := envsecrets.NormalizedPath(collection.Path)
	var stored *envsecrets.CollectionEntry
	for index := range store.Collections {
		if store.Collections[index].Path == collectionPath {
			stored = &store.Collections[index]
			break
		}
	}
	if stored == nil {
		return nil
	}
	// See hydrateWorkspaceEnvironmentSecretsLocked. This one also runs on every
	// collection-watcher refresh, so the channelled notification is what keeps a
	// permanently unreadable secret from filling the notification list.
	if err := envsecrets.Hydrate(a.dataDir, collection.Environments, stored.Environments); err != nil {
		a.notifyChangedLocked("collection-environment-secrets:"+collection.ID, "error",
			"Environments in "+collection.Name+": "+err.Error())
	}
	return nil
}
