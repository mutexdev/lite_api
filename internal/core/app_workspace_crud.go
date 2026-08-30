package core

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/mutexdev/lite_api/internal/cookiejar"
	"github.com/mutexdev/lite_api/internal/gitworkbench"
	"github.com/mutexdev/lite_api/internal/prefs"
	"github.com/mutexdev/lite_api/internal/store/bru"
)

func (a *App) CreateWorkspace(name string) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return AppState{}, err
	}
	if a.workspaceRuntime != nil {
		return a.createScopedWorkspaceTargetLocked(name)
	}
	if strings.TrimSpace(name) == "" {
		return AppState{}, errors.New("workspace name is required")
	}
	now := time.Now()
	id := newID("workspace")
	wsPath := filepath.Join(a.dataDir, sanitizeFilename(name))
	ws := Workspace{
		ID:        id,
		Name:      strings.TrimSpace(name),
		Path:      wsPath,
		Docs:      "# Workspace notes\n",
		CreatedAt: now,
		UpdatedAt: now,
	}
	a.state.Workspaces = append(a.state.Workspaces, ws)
	if _, err := a.ensureWorkspaceScratchCollectionLocked(&a.state.Workspaces[len(a.state.Workspaces)-1]); err != nil {
		return AppState{}, err
	}
	a.state.ActiveWorkspaceID = id
	a.notify("info", "Workspace created: "+ws.Name)
	return a.state, a.markDirty(persistScopeState)
}

func (a *App) SetActiveWorkspace(workspaceID string) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return AppState{}, err
	}
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return AppState{}, errors.New("workspace id is required")
	}
	if a.workspaceRuntime != nil && workspaceID != a.workspaceRuntime.intent.WorkspaceID {
		return AppState{}, errors.New("scoped workspace windows cannot switch workspaces")
	}
	workspace, err := a.findWorkspaceLocked(workspaceID)
	if err != nil {
		return AppState{}, err
	}
	a.state.ActiveWorkspaceID = workspace.ID
	return a.state, a.markDirty(persistScopeState)
}

func (a *App) CreateCollection(workspaceID, name, format string) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return AppState{}, err
	}
	if strings.TrimSpace(name) == "" {
		return AppState{}, errors.New("collection name is required")
	}
	if format == "" {
		format = "yml"
	}
	ws, err := a.findWorkspaceLocked(workspaceID)
	if err != nil {
		return AppState{}, err
	}
	now := time.Now()
	id := newID("collection")
	collectionRoot := defaultCollectionRoot(a.state.Preferences, ws.Path)
	collection := Collection{
		ID:             id,
		Name:           strings.TrimSpace(name),
		Version:        "1",
		Path:           filepath.Join(collectionRoot, sanitizeFilename(name)),
		Format:         format,
		Variables:      []Variable{{ID: newID("var"), Name: "host", Value: "https://httpbin.org", DataType: "string", Enabled: true}},
		Auth:           AuthConfig{Mode: "none"},
		SecurityConfig: CollectionSecurityConfig{JSSandboxMode: "safe"},
		Docs:           "# Collection notes\n",
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if _, statErr := os.Lstat(collection.Path); statErr == nil {
		return AppState{}, fmt.Errorf("collection path already exists: %s", collection.Path)
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return AppState{}, fmt.Errorf("check collection path: %w", statErr)
	}
	// Empty collections are durable filesystem objects too. Materialise the
	// root metadata before publishing the collection into workspace state so a
	// clean relaunch never points at a directory that was never created.
	if err := a.writeCollectionFilesLocked(&collection); err != nil {
		return AppState{}, fmt.Errorf("create collection files: %w", err)
	}
	if firstScratch := firstScratchCollectionIndex(ws.Collections); firstScratch == 0 && countRegularCollections(ws.Collections) == 0 {
		ws.Collections = append(ws.Collections, Collection{})
		copy(ws.Collections[1:], ws.Collections[0:])
		ws.Collections[0] = collection
	} else {
		ws.Collections = append(ws.Collections, collection)
	}
	ws.UpdatedAt = now
	a.state.ActiveWorkspaceID = ws.ID
	a.notify("info", "Collection created: "+collection.Name)
	return a.state, a.markDirty(persistScopeState)
}

func defaultCollectionRoot(preferences Preferences, workspacePath string) string {
	preferences = prefs.Normalize(preferences)
	root := strings.TrimSpace(preferences.General.DefaultLocation)
	if root == "" {
		root = strings.TrimSpace(preferences.DefaultCollectionPath)
	}
	if root == "" {
		root = workspacePath
	}
	if expanded, err := expandUserPath(root); err == nil && expanded != "" {
		root = expanded
	}
	return filepath.Clean(root)
}

func expandUserPath(pathValue string) (string, error) {
	pathValue = strings.TrimSpace(pathValue)
	if pathValue == "" {
		return "", nil
	}
	if pathValue == "~" || strings.HasPrefix(pathValue, "~/") || strings.HasPrefix(pathValue, `~\`) {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return "", errors.New("home directory is unavailable")
		}
		if pathValue == "~" {
			return home, nil
		}
		return filepath.Join(home, strings.TrimLeft(pathValue[1:], `/\`)), nil
	}
	return pathValue, nil
}

func (a *App) OpenCollection(workspaceID, collectionPath string) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return AppState{}, err
	}
	ws, err := a.findWorkspaceLocked(workspaceID)
	if err != nil {
		return AppState{}, err
	}
	collection, err := a.readCollectionFromDiskCachedLocked(collectionPath)
	if err != nil {
		return AppState{}, err
	}
	if err := a.hydrateCollectionEnvironmentSecretsLocked(&collection); err != nil {
		return AppState{}, err
	}
	for i := range ws.Collections {
		if filepath.Clean(ws.Collections[i].Path) == filepath.Clean(collection.Path) {
			preserveCollectionRuntimeState(ws.Collections[i], &collection)
			collection.Remote = ws.Collections[i].Remote
			collection.NotFoundLocally = false
			ws.Collections[i] = collection
			a.seedCollectionWatchFingerprintLocked(collection.Path)
			a.openFirstCollectionItemLocked(collection)
			a.notify("success", "Refreshed "+collection.Name)
			return a.state, a.markDirty(persistScopeState)
		}
	}
	ws.Collections = append(ws.Collections, collection)
	ws.UpdatedAt = time.Now()
	a.seedCollectionWatchFingerprintLocked(collection.Path)
	a.openFirstCollectionItemLocked(collection)
	a.notify("success", "Opened "+collection.Name)
	return a.state, a.markDirty(persistScopeState)
}

func (a *App) RefreshCollection(collectionID string) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return AppState{}, err
	}
	for wi := range a.state.Workspaces {
		for ci := range a.state.Workspaces[wi].Collections {
			if a.state.Workspaces[wi].Collections[ci].ID != collectionID {
				continue
			}
			refreshed, err := a.readCollectionFromDiskCachedLocked(a.state.Workspaces[wi].Collections[ci].Path)
			if err != nil {
				if a.state.Workspaces[wi].Collections[ci].Remote != "" && errors.Is(err, os.ErrNotExist) {
					a.state.Workspaces[wi].Collections[ci].NotFoundLocally = true
					a.state.Workspaces[wi].Collections[ci].Items = nil
					a.state.Workspaces[wi].Collections[ci].Folders = nil
					a.clearCollectionWatchFingerprintLocked(a.state.Workspaces[wi].Collections[ci].Path)
					a.removeOpenTabsForCollectionLocked(collectionID)
					a.notify("warn", a.state.Workspaces[wi].Collections[ci].Name+" is not cloned locally")
					return a.state, a.markDirty(persistScopeState)
				}
				return AppState{}, err
			}
			if err := a.hydrateCollectionEnvironmentSecretsLocked(&refreshed); err != nil {
				return AppState{}, err
			}
			preserveCollectionRuntimeState(a.state.Workspaces[wi].Collections[ci], &refreshed)
			refreshed.Remote = a.state.Workspaces[wi].Collections[ci].Remote
			refreshed.NotFoundLocally = false
			a.state.Workspaces[wi].Collections[ci] = refreshed
			a.seedCollectionWatchFingerprintLocked(refreshed.Path)
			// The collection just came off disk wholesale, so a tab naming a
			// request the file no longer defines is now an orphan.
			a.pruneOrphanedOpenTabsLocked()
			a.openFirstCollectionItemLocked(refreshed)
			a.notify("success", "Refreshed "+refreshed.Name)
			return a.state, a.markDirty(persistScopeState)
		}
	}
	return AppState{}, fmt.Errorf("collection %s not found", collectionID)
}

func (a *App) RefreshChangedCollections() (CollectionWatchRefreshResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return CollectionWatchRefreshResult{}, err
	}
	if a.collectionWatchFingerprints == nil {
		a.collectionWatchFingerprints = map[string]string{}
	}
	result := CollectionWatchRefreshResult{State: a.state}
	now := time.Now()
	for wi := range a.state.Workspaces {
		ws := &a.state.Workspaces[wi]
		for ci := range ws.Collections {
			collection := &ws.Collections[ci]
			if !collectionWatchCandidate(*collection) {
				continue
			}
			collectionPath := filepath.Clean(collection.Path)
			fingerprint, err := collectionWatchFingerprint(collectionPath)
			if err != nil {
				if errors.Is(err, os.ErrNotExist) {
					a.clearCollectionWatchFingerprintLocked(collectionPath)
					if strings.TrimSpace(collection.Remote) != "" {
						collection.NotFoundLocally = true
						collection.Items = nil
						collection.Folders = nil
						a.removeOpenTabsForCollectionLocked(collection.ID)
						result.Missing = append(result.Missing, collection.Name)
						result.Changed = true
					}
					continue
				}
				result.Errors = append(result.Errors, fmt.Sprintf("%s: %v", collection.Name, err))
				continue
			}
			previous, seen := a.collectionWatchFingerprints[collectionPath]
			if !seen {
				// A process restart begins with an empty in-memory watch map. The
				// first observation establishes a baseline; it is not evidence of
				// an external edit and must not replace live request/tab identity.
				a.collectionWatchFingerprints[collectionPath] = fingerprint
				continue
			}
			if seen && previous == fingerprint {
				continue
			}
			if collectionHasDraftRequests(*collection) {
				// The fingerprint is deliberately NOT advanced. Recording the
				// new one here marked the external edit as seen while refusing
				// to read it, so the next poll compared equal, reported nothing
				// changed, and the edit was never picked up — not after the
				// draft was saved, not after a restart. Leaving the fingerprint
				// at the last version actually loaded keeps the collection
				// reported as dirty-skipped on every poll until the drafts are
				// resolved, and then it refreshes.
				result.SkippedDirty = append(result.SkippedDirty, collection.Name)
				continue
			}
			refreshed, err := a.readCollectionFromDiskCachedLocked(collectionPath)
			if err != nil {
				result.Errors = append(result.Errors, fmt.Sprintf("%s: %v", collection.Name, err))
				continue
			}
			if err := a.hydrateCollectionEnvironmentSecretsLocked(&refreshed); err != nil {
				return CollectionWatchRefreshResult{}, err
			}
			preserveCollectionRuntimeState(*collection, &refreshed)
			removedRequestIDs := missingRequestIDs(collection.Items, refreshed.Items)
			refreshed.Remote = collection.Remote
			refreshed.NotFoundLocally = false
			ws.Collections[ci] = refreshed
			ws.UpdatedAt = now
			a.collectionWatchFingerprints[collectionPath] = fingerprint
			a.removeOpenTabsForDeletedRequestIDsLocked(refreshed.ID, removedRequestIDs)
			result.Refreshed = append(result.Refreshed, refreshed.Name)
			result.Changed = true
		}
	}
	// removeOpenTabsForDeletedRequestIDsLocked above only covers requests that
	// were in the previous in-memory copy of the collection. A tab that was
	// already stale before this refresh — or one whose collection is gone from
	// state entirely — is caught here.
	if a.pruneOrphanedOpenTabsLocked() {
		result.Changed = true
	}
	// A collection this refresh could not read, or one whose directory has gone,
	// was reported only in the result struct — which the poller discards. The
	// user saw a collection quietly stop tracking its files with nothing said.
	//
	// notifyChangedLocked rather than notify: the watcher polls, so a
	// permanently unreadable collection would otherwise raise one notification
	// per poll and evict the 20-entry list within a minute.
	errorMessage := ""
	if len(result.Errors) > 0 {
		errorMessage = fmt.Sprintf("Could not refresh %d collection%s from disk: %s",
			len(result.Errors), cookiejar.PluralSuffix(len(result.Errors)), strings.Join(result.Errors, "; "))
	}
	a.notifyChangedLocked("collection-watch-errors", "error", errorMessage)
	missingMessage := ""
	if len(result.Missing) > 0 {
		missingMessage = fmt.Sprintf("%d collection%s no longer on disk: %s",
			len(result.Missing), cookiejar.PluralSuffix(len(result.Missing)), strings.Join(result.Missing, ", "))
	}
	a.notifyChangedLocked("collection-watch-missing", "error", missingMessage)
	if result.Changed {
		if err := a.markDirty(persistScopeState); err != nil {
			return CollectionWatchRefreshResult{}, err
		}
	}
	result.State = a.state
	return result, nil
}

func (a *App) RevealCollectionInFolder(collectionID string) error {
	a.mu.Lock()
	if err := a.ensureReadyLocked(); err != nil {
		a.mu.Unlock()
		return err
	}
	_, collection, err := a.findCollectionWithWorkspaceLocked(collectionID)
	if err != nil {
		a.mu.Unlock()
		return err
	}
	if collection.NotFoundLocally {
		a.mu.Unlock()
		return errors.New("collection is not cloned locally")
	}
	collectionPath := collection.Path
	a.mu.Unlock()

	if strings.TrimSpace(collectionPath) == "" {
		return errors.New("collection path is required")
	}
	if _, err := os.Stat(collectionPath); err != nil {
		return fmt.Errorf("reveal collection folder: %w", err)
	}
	opener := a.revealInFolder
	if opener == nil {
		opener = defaultRevealInFolder
	}
	return opener(collectionPath)
}

func (a *App) RevealCollectionFolderInFolder(collectionID, folderPath string) error {
	targetPath, err := a.resolveCollectionFolderPath(collectionID, folderPath)
	if err != nil {
		return err
	}
	if info, err := os.Stat(targetPath); err != nil {
		return fmt.Errorf("reveal collection folder: %w", err)
	} else if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", targetPath)
	}
	opener := a.revealInFolder
	if opener == nil {
		opener = defaultRevealInFolder
	}
	return opener(targetPath)
}

func (a *App) RevealRequestInFolder(collectionID, itemID string) error {
	targetPath, err := a.resolveRequestFilePath(collectionID, itemID)
	if err != nil {
		return err
	}
	if info, err := os.Stat(targetPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return errors.New("the file does not exist")
		}
		return fmt.Errorf("reveal request: %w", err)
	} else if info.IsDir() {
		return fmt.Errorf("%s is not a request file", targetPath)
	}
	opener := a.revealInFolder
	if opener == nil {
		opener = defaultRevealInFolder
	}
	return opener(targetPath)
}

func (a *App) ResolveCollectionFolderPath(collectionID, folderPath string) (string, error) {
	return a.resolveCollectionFolderPath(collectionID, folderPath)
}

func (a *App) resolveCollectionFolderPath(collectionID, folderPath string) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return "", err
	}
	_, collection, err := a.findCollectionWithWorkspaceLocked(collectionID)
	if err != nil {
		return "", err
	}
	if collection.NotFoundLocally {
		return "", errors.New("collection is not cloned locally")
	}
	return collectionFolderFilesystemPath(collection, folderPath)
}

func (a *App) resolveRequestFilePath(collectionID, itemID string) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return "", err
	}
	_, collection, err := a.findCollectionWithWorkspaceLocked(collectionID)
	if err != nil {
		return "", err
	}
	if collection.NotFoundLocally {
		return "", errors.New("collection is not cloned locally")
	}
	if strings.TrimSpace(collection.Path) == "" {
		return "", errors.New("collection path is empty")
	}
	item, err := findItem(collection, itemID)
	if err != nil {
		return "", err
	}
	return collectionRequestFilesystemPath(collection, *item)
}

func (a *App) RemoveCollection(collectionID string) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return AppState{}, err
	}
	for wi := range a.state.Workspaces {
		ws := &a.state.Workspaces[wi]
		for ci := range ws.Collections {
			if ws.Collections[ci].ID != collectionID {
				continue
			}
			collection := ws.Collections[ci]
			if collection.Scratch {
				return AppState{}, errors.New("scratch collection cannot be removed")
			}
			if strings.TrimSpace(collection.Remote) != "" && strings.TrimSpace(collection.Path) != "" {
				if err := updateManagedGitIgnore(ws.Path, collection.Path, false); err != nil {
					return AppState{}, err
				}
			}
			a.clearCollectionWatchFingerprintLocked(collection.Path)
			ws.Collections = append(ws.Collections[:ci], ws.Collections[ci+1:]...)
			ws.UpdatedAt = time.Now()
			a.removeOpenTabsForCollectionLocked(collectionID)
			a.notify("success", "Removed collection: "+collection.Name)
			return a.state, a.markDirty(persistScopeState)
		}
	}
	return AppState{}, fmt.Errorf("collection %s not found", collectionID)
}

func (a *App) RenameCollection(collectionID, name string) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return AppState{}, err
	}
	if name == "" {
		return AppState{}, errors.New("collection name is required")
	}
	ws, collection, err := a.findCollectionWithWorkspaceLocked(collectionID)
	if err != nil {
		return AppState{}, err
	}
	if collection.NotFoundLocally {
		return AppState{}, errors.New("collection is not cloned locally")
	}
	previousName := collection.Name
	previousUpdatedAt := collection.UpdatedAt
	previousWorkspaceUpdatedAt := ws.UpdatedAt
	now := time.Now()
	collection.Name = name
	collection.UpdatedAt = now
	ws.UpdatedAt = now
	if err := a.writeCollectionNameMetadataLocked(collection); err != nil {
		collection.Name = previousName
		collection.UpdatedAt = previousUpdatedAt
		ws.UpdatedAt = previousWorkspaceUpdatedAt
		return AppState{}, err
	}
	a.notify("success", "Renamed collection: "+collection.Name)
	return a.state, a.markDirty(persistScopeState)
}

func (a *App) CloneCollection(collectionID, collectionName, collectionFolderName, collectionLocation string) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return AppState{}, err
	}
	if collectionName == "" {
		return AppState{}, errors.New("collection name is required")
	}
	if len([]rune(collectionName)) > 255 {
		return AppState{}, errors.New("collection name must be 255 characters or less")
	}
	ws, collection, err := a.findCollectionWithWorkspaceLocked(collectionID)
	if err != nil {
		return AppState{}, err
	}
	if collection.NotFoundLocally {
		return AppState{}, errors.New("collection is not cloned locally")
	}
	if strings.TrimSpace(collection.Path) == "" {
		return AppState{}, errors.New("collection path is empty")
	}
	sourcePath := filepath.Clean(collection.Path)
	if info, statErr := os.Stat(sourcePath); errors.Is(statErr, os.ErrNotExist) {
		if err := a.writeCollectionFilesLocked(collection); err != nil {
			return AppState{}, err
		}
	} else if statErr != nil {
		return AppState{}, statErr
	} else if !info.IsDir() {
		return AppState{}, fmt.Errorf("%s is not a directory", sourcePath)
	}
	folderName := bru.SanitizeName(collectionFolderName)
	if !bru.ValidateName(folderName) {
		return AppState{}, fmt.Errorf("collection: invalid pathname - %s", filepath.Join(collectionLocation, folderName))
	}
	location := strings.TrimSpace(collectionLocation)
	if location == "" {
		return AppState{}, errors.New("location is required")
	}
	if expanded, err := expandUserPath(location); err == nil && expanded != "" {
		location = expanded
	}
	targetPath := filepath.Clean(filepath.Join(location, folderName))
	if _, err := os.Stat(targetPath); err == nil {
		return AppState{}, fmt.Errorf("collection: %s already exists", targetPath)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return AppState{}, err
	}
	if err := os.MkdirAll(targetPath, 0o755); err != nil {
		return AppState{}, err
	}
	cloned := *collection
	cloned.ID = deterministicID("collection", targetPath)
	cloned.Name = collectionName
	cloned.Path = targetPath
	cloned.Remote = ""
	cloned.NotFoundLocally = false
	cloned.Scratch = false
	now := time.Now()
	cloned.CreatedAt = now
	cloned.UpdatedAt = now
	if err := writeClonedCollectionRootMetadata(collection, &cloned); err != nil {
		return AppState{}, err
	}
	if err := copyCollectionFormatFiles(sourcePath, targetPath, cloned.Format); err != nil {
		return AppState{}, err
	}
	cloned, err = readCollectionFromDisk(targetPath)
	if err != nil {
		return AppState{}, err
	}
	if err := a.hydrateCollectionEnvironmentSecretsLocked(&cloned); err != nil {
		return AppState{}, err
	}
	replaced := false
	for i := range ws.Collections {
		if filepath.Clean(ws.Collections[i].Path) == targetPath {
			ws.Collections[i] = cloned
			replaced = true
			break
		}
	}
	if !replaced {
		ws.Collections = append(ws.Collections, cloned)
	}
	ws.UpdatedAt = now
	a.state.ActiveWorkspaceID = ws.ID
	a.openFirstCollectionItemLocked(cloned)
	a.notify("success", "Collection created!")
	return a.state, a.markDirty(persistScopeState)
}

func (a *App) ConnectCollectionGitRemote(collectionID, remoteURL string) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return AppState{}, err
	}
	remote, err := gitworkbench.NormalizeGitRemoteURL(remoteURL)
	if err != nil {
		return AppState{}, err
	}
	ws, collection, err := a.findCollectionWithWorkspaceLocked(collectionID)
	if err != nil {
		return AppState{}, err
	}
	if _, statErr := os.Stat(collection.Path); errors.Is(statErr, os.ErrNotExist) {
		if err := a.writeCollectionFilesLocked(collection); err != nil {
			return AppState{}, err
		}
	} else if statErr != nil {
		return AppState{}, statErr
	}
	collection.Remote = remote
	collection.NotFoundLocally = false
	collection.UpdatedAt = time.Now()
	ws.UpdatedAt = collection.UpdatedAt
	if err := updateManagedGitIgnore(ws.Path, collection.Path, true); err != nil {
		return AppState{}, err
	}
	a.notify("success", "Connected "+collection.Name+" to Git")
	return a.state, a.markDirty(persistScopeState)
}

func (a *App) DisconnectCollectionGitRemote(collectionID string) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return AppState{}, err
	}
	for wi := range a.state.Workspaces {
		ws := &a.state.Workspaces[wi]
		for ci := range ws.Collections {
			if ws.Collections[ci].ID != collectionID {
				continue
			}
			collection := &ws.Collections[ci]
			if collection.Remote == "" {
				return AppState{}, errors.New("collection is not connected to Git")
			}
			collectionPath := collection.Path
			collectionName := collection.Name
			if err := updateManagedGitIgnore(ws.Path, collectionPath, false); err != nil {
				return AppState{}, err
			}
			if collection.NotFoundLocally {
				ws.Collections = append(ws.Collections[:ci], ws.Collections[ci+1:]...)
				a.removeOpenTabsForCollectionLocked(collectionID)
			} else {
				collection.Remote = ""
				collection.NotFoundLocally = false
				collection.UpdatedAt = time.Now()
			}
			ws.UpdatedAt = time.Now()
			a.notify("info", "Removed Git remote from "+collectionName)
			return a.state, a.markDirty(persistScopeState)
		}
	}
	return AppState{}, fmt.Errorf("collection %s not found", collectionID)
}

func (a *App) GitVersion() (string, error) {
	return gitVersion()
}

func (a *App) ScanGitCollections(rootPath string) ([]GitCollectionCandidate, error) {
	return scanBrunoCollections(rootPath)
}

func (a *App) CloneGitRepository(remoteURL, cloneRoot, repoName string) (GitCloneResult, error) {
	remote, err := gitworkbench.NormalizeGitRemoteURL(remoteURL)
	if err != nil {
		return GitCloneResult{}, err
	}
	a.emitGitCloneProgress("checking", "Checking Git", "")
	version, err := gitVersion()
	if err != nil {
		a.emitGitCloneProgress("error", err.Error(), "")
		return GitCloneResult{}, err
	}
	cloneRoot = strings.TrimSpace(cloneRoot)
	if cloneRoot == "" {
		cloneRoot = filepath.Join(a.dataDir, "Git Clones")
	}
	repoName = strings.TrimSpace(repoName)
	if repoName == "" {
		repoName = deriveGitRepoName(remote)
	}
	if repoName == "" {
		return GitCloneResult{}, errors.New("repository name could not be derived")
	}
	target := filepath.Join(cloneRoot, sanitizeFilename(repoName))
	cleanTarget := filepath.Clean(target)
	a.emitGitCloneProgress("preparing", "Preparing "+cleanTarget, cleanTarget)
	if info, err := os.Stat(target); err == nil {
		if !info.IsDir() {
			a.emitGitCloneProgress("error", fmt.Sprintf("%s already exists and is not a directory", target), cleanTarget)
			return GitCloneResult{}, fmt.Errorf("%s already exists and is not a directory", target)
		}
		entries, err := os.ReadDir(target)
		if err != nil {
			a.emitGitCloneProgress("error", err.Error(), cleanTarget)
			return GitCloneResult{}, err
		}
		if len(entries) > 0 {
			a.emitGitCloneProgress("error", fmt.Sprintf("%s already exists and is not empty", target), cleanTarget)
			return GitCloneResult{}, fmt.Errorf("%s already exists and is not empty", target)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		a.emitGitCloneProgress("error", err.Error(), cleanTarget)
		return GitCloneResult{}, err
	}
	if err := os.MkdirAll(cloneRoot, 0o755); err != nil {
		a.emitGitCloneProgress("error", err.Error(), cleanTarget)
		return GitCloneResult{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "clone", "--", remote, target)
	a.emitGitCloneProgress("cloning", "Cloning "+remote, cleanTarget)
	output, err := a.runGitCloneCommand(ctx, cmd, cleanTarget)
	if err != nil {
		_ = os.RemoveAll(target)
		if ctx.Err() == context.DeadlineExceeded {
			a.emitGitCloneProgress("error", "git clone timed out", cleanTarget)
			return GitCloneResult{}, errors.New("git clone timed out")
		}
		message := strings.TrimSpace(output)
		if message == "" {
			message = err.Error()
		}
		a.emitGitCloneProgress("error", "git clone failed: "+message, cleanTarget)
		return GitCloneResult{}, fmt.Errorf("git clone failed: %s", message)
	}
	a.emitGitCloneProgress("scanning", "Scanning cloned repository", cleanTarget)
	candidates, err := scanBrunoCollections(target)
	if err != nil {
		a.emitGitCloneProgress("error", err.Error(), cleanTarget)
		return GitCloneResult{}, err
	}
	a.emitGitCloneProgress("done", fmt.Sprintf("Found %d collection%s", len(candidates), cookiejar.PluralSuffix(len(candidates))), cleanTarget)
	return GitCloneResult{
		Version:    version,
		TargetPath: cleanTarget,
		Output:     strings.TrimSpace(output),
		Candidates: candidates,
	}, nil
}

func (a *App) runGitCloneCommand(ctx context.Context, cmd *exec.Cmd, targetPath string) (string, error) {
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return "", err
	}
	var outputMu sync.Mutex
	var output strings.Builder
	collect := func(reader io.Reader) {
		scanner := bufio.NewScanner(reader)
		scanner.Split(scanGitProgressToken)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				continue
			}
			outputMu.Lock()
			if output.Len() > 0 {
				output.WriteByte('\n')
			}
			output.WriteString(line)
			outputMu.Unlock()
			a.emitGitCloneProgress("output", line, targetPath)
		}
	}
	if err := cmd.Start(); err != nil {
		return "", err
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		collect(stdout)
	}()
	go func() {
		defer wg.Done()
		collect(stderr)
	}()
	err = cmd.Wait()
	wg.Wait()
	outputMu.Lock()
	result := output.String()
	outputMu.Unlock()
	return result, err
}

func (a *App) OpenGitCollections(workspaceID string, collectionPaths []string, remoteURL string) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return AppState{}, err
	}
	remote, err := gitworkbench.NormalizeGitRemoteURL(remoteURL)
	if err != nil {
		return AppState{}, err
	}
	if len(collectionPaths) == 0 {
		return AppState{}, errors.New("select at least one collection")
	}
	ws, err := a.findWorkspaceLocked(workspaceID)
	if err != nil {
		return AppState{}, err
	}
	opened := 0
	var firstOpened Collection
	haveFirstOpened := false
	for _, collectionPath := range collectionPaths {
		collectionPath = strings.TrimSpace(collectionPath)
		if collectionPath == "" {
			continue
		}
		collection, err := a.readCollectionFromDiskCachedLocked(collectionPath)
		if err != nil {
			return AppState{}, err
		}
		if err := a.hydrateCollectionEnvironmentSecretsLocked(&collection); err != nil {
			return AppState{}, err
		}
		collection.Remote = remote
		collection.NotFoundLocally = false
		replaced := false
		for i := range ws.Collections {
			if filepath.Clean(ws.Collections[i].Path) == filepath.Clean(collection.Path) {
				ws.Collections[i] = collection
				if !haveFirstOpened {
					firstOpened = collection
					haveFirstOpened = true
				}
				replaced = true
				break
			}
		}
		if !replaced {
			ws.Collections = append(ws.Collections, collection)
			if !haveFirstOpened {
				firstOpened = collection
				haveFirstOpened = true
			}
		}
		if err := updateManagedGitIgnore(ws.Path, collection.Path, true); err != nil {
			return AppState{}, err
		}
		opened++
	}
	if opened == 0 {
		return AppState{}, errors.New("select at least one collection")
	}
	ws.UpdatedAt = time.Now()
	if haveFirstOpened {
		a.openFirstCollectionItemLocked(firstOpened)
	}
	a.notify("success", fmt.Sprintf("Opened %d Git collection%s", opened, cookiejar.PluralSuffix(opened)))
	return a.state, a.markDirty(persistScopeState)
}
