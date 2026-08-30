package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/mutexdev/lite_api/internal/envsecrets"
	"github.com/mutexdev/lite_api/internal/prefs"
	"github.com/mutexdev/lite_api/internal/store/yamlstore"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/mutexdev/lite_api/internal/scripting"
	"github.com/mutexdev/lite_api/internal/store/bru"
)

func (a *App) ensureReady() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.ensureReadyLocked()
}

// ensureReadyLocked requires the WRITE lock on a.mu, on every call, including
// calls against an already-initialised App. It is not a readiness *check*: the
// steady-state path still runs refreshGitCollectionAvailabilityLocked (which
// unconditionally assigns collection.NotFoundLocally for every collection) and
// pruneExpiredCookiesLocked (which unconditionally reassigns a.state.Cookies
// and rewrites its backing array in place), and the scoped-workspace path
// still calls workspaceRuntime.heartbeat, which renews the on-disk ownership
// lease. There is therefore no side-effect-free predicate that could serve as
// the fast path of a double-checked RLock, which is why GetState and every
// other caller of this function hold Lock rather than RLock.
func (a *App) ensureReadyLocked() error {
	if a.workspaceRuntime != nil {
		if len(a.state.Workspaces) != 1 || a.state.ActiveWorkspaceID != a.workspaceRuntime.intent.WorkspaceID || a.state.Workspaces[0].ID != a.workspaceRuntime.intent.WorkspaceID {
			return errors.New("scoped workspace runtime state is invalid")
		}
		if err := a.workspaceRuntime.heartbeat(); err != nil {
			return fmt.Errorf("workspace ownership was lost: %w", err)
		}
		if len(a.state.FeatureLedger) == 0 {
			a.state.FeatureLedger = bru.DefaultFeatures()
		}
		// The scoped runtime does not run normalizeStateLocked, so the orphaned
		// tab prune has to be repeated here rather than inherited.
		a.pruneOrphanedOpenTabsLocked()
		return nil
	}
	if a.dataDir == "" {
		a.dataDir = defaultDataDir()
	}
	if err := os.MkdirAll(a.dataDir, 0o755); err != nil {
		return err
	}
	if len(a.state.Workspaces) == 0 {
		a.state = defaultState(a.dataDir)
		if err := a.writeFreshDefaultCollectionFilesLocked(); err != nil {
			return err
		}
		if _, err := a.ensureScratchCollectionsLocked(); err != nil {
			return err
		}
		return a.persistLocked()
	}
	if len(a.state.FeatureLedger) == 0 {
		a.state.FeatureLedger = bru.DefaultFeatures()
	}
	_, stateFileErr := os.Stat(filepath.Join(a.dataDir, "state.json"))
	freshState := errors.Is(stateFileErr, os.ErrNotExist)
	if stateFileErr != nil && !freshState {
		return stateFileErr
	}
	changed := a.normalizeStateLocked()
	if freshState {
		if err := a.writeFreshDefaultCollectionFilesLocked(); err != nil {
			return err
		}
		changed = true
	}
	scratchChanged, err := a.ensureScratchCollectionsLocked()
	if err != nil {
		return err
	}
	changed = changed || scratchChanged
	envChanged, err := a.prepareWorkspaceGlobalEnvironmentsLocked()
	if err != nil {
		return err
	}
	changed = changed || envChanged
	if err := a.hydrateStateEnvironmentSecretsLocked(); err != nil {
		return err
	}
	if changed {
		if err := a.persistLocked(); err != nil {
			return err
		}
	}
	a.refreshGitCollectionAvailabilityLocked()
	a.pruneExpiredCookiesLocked()
	return nil
}

// NewAppWithDir starts with an in-memory sample so the first frame is useful.
// On a genuinely fresh state directory, materialize that sample before state
// persistence so its SAVED label and filesystem/recovery actions are truthful.
func (a *App) writeFreshDefaultCollectionFilesLocked() error {
	for wi := range a.state.Workspaces {
		for ci := range a.state.Workspaces[wi].Collections {
			collection := &a.state.Workspaces[wi].Collections[ci]
			if collection.Scratch || collection.NotFoundLocally || strings.TrimSpace(collection.Path) == "" || !pathInside(a.dataDir, collection.Path) {
				continue
			}
			if info, err := os.Stat(collection.Path); err == nil {
				if !info.IsDir() {
					return fmt.Errorf("default collection path %s is not a directory", collection.Path)
				}
				continue
			} else if !errors.Is(err, os.ErrNotExist) {
				return err
			}
			if err := a.writeCollectionFilesLocked(collection); err != nil {
				return fmt.Errorf("write default collection %s: %w", collection.Name, err)
			}
			a.seedCollectionWatchFingerprintLocked(collection.Path)
		}
	}
	return nil
}

func (a *App) load() error {
	data, err := os.ReadFile(filepath.Join(a.dataDir, "state.json"))
	if err != nil {
		return err
	}
	var state AppState
	if err := json.Unmarshal(data, &state); err != nil {
		return err
	}
	if len(state.FeatureLedger) == 0 {
		state.FeatureLedger = bru.DefaultFeatures()
	}
	state.Cookies = envsecrets.DecryptCookieValues(a.dataDir, state.Cookies)
	a.state = state
	// US-009 step 3. Backfill body handles for responses loaded from a
	// state.json written before the store existed. Deliberately best-effort and
	// deliberately not part of `changed`: it must not fail a load, and it must
	// not force a rewrite of state.json on every startup. See the function.
	a.migrateResponseBodiesLocked()
	changed := a.normalizeStateLocked()
	scratchChanged, err := a.ensureScratchCollectionsLocked()
	if err != nil {
		return err
	}
	changed = changed || scratchChanged
	envChanged, err := a.prepareWorkspaceGlobalEnvironmentsLocked()
	if err != nil {
		return err
	}
	changed = changed || envChanged
	if err := a.hydrateStateEnvironmentSecretsLocked(); err != nil {
		return err
	}
	if changed {
		if err := a.persistLocked(); err != nil {
			return err
		}
	}
	a.refreshGitCollectionAvailabilityLocked()
	a.pruneExpiredCookiesLocked()
	return nil
}

func (a *App) normalizeStateLocked() bool {
	changed := false
	normalizedPreferences := prefs.Normalize(a.state.Preferences)
	if !reflect.DeepEqual(a.state.Preferences, normalizedPreferences) {
		a.state.Preferences = normalizedPreferences
		changed = true
	}
	normalizedNotifications := normalizeNotifications(a.state.Notifications)
	if !reflect.DeepEqual(a.state.Notifications, normalizedNotifications) {
		a.state.Notifications = normalizedNotifications
		changed = true
	}
	for wi := range a.state.Workspaces {
		for ci := range a.state.Workspaces[wi].Collections {
			collection := &a.state.Workspaces[wi].Collections[ci]
			normalizedSecurity := normalizeCollectionSecurityConfig(collection.SecurityConfig)
			if !reflect.DeepEqual(collection.SecurityConfig, normalizedSecurity) {
				collection.SecurityConfig = normalizedSecurity
				changed = true
			}
		}
	}
	// A state file can name requests that no longer exist — deleted outside the
	// app, or gone from a collection that has moved on since state.json was
	// written. See pruneOrphanedOpenTabsLocked for what those tabs render.
	if a.pruneOrphanedOpenTabsLocked() {
		changed = true
	}
	return changed
}

func normalizeNotifications(notifications []Notification) []Notification {
	if len(notifications) == 0 {
		return []Notification{}
	}
	limit := len(notifications)
	if limit > 50 {
		limit = 50
	}
	out := make([]Notification, 0, limit)
	for _, notification := range notifications[:limit] {
		notification.ID = strings.TrimSpace(notification.ID)
		if notification.ID == "" {
			notification.ID = newID("notification")
		}
		notification.Level = strings.ToLower(strings.TrimSpace(notification.Level))
		if notification.Level == "" {
			notification.Level = "info"
		}
		notification.Message = strings.TrimSpace(notification.Message)
		notification.Title = strings.TrimSpace(notification.Title)
		notification.Description = strings.TrimSpace(notification.Description)
		notification.Type = strings.TrimSpace(notification.Type)
		notification.Color = strings.TrimSpace(notification.Color)
		if notification.Message == "" && notification.Description != "" {
			notification.Message = notification.Description
		}
		if notification.Description == "" {
			notification.Description = notification.Message
		}
		if notification.Title == "" {
			notification.Title = notificationTitle(notification.Level, notification.Message)
		}
		if notification.Type == "" {
			notification.Type = notificationType(notification.Level)
		}
		if notification.Color == "" {
			notification.Color = notificationColor(notification.Level)
		}
		if notification.At.IsZero() {
			notification.At = time.Now()
		}
		out = append(out, notification)
	}
	return out
}

func (a *App) ensureScratchCollectionsLocked() (bool, error) {
	changed := false
	for wi := range a.state.Workspaces {
		didChange, err := a.ensureWorkspaceScratchCollectionLocked(&a.state.Workspaces[wi])
		if err != nil {
			return changed, err
		}
		changed = changed || didChange
	}
	return changed, nil
}

func (a *App) ensureWorkspaceScratchCollectionLocked(workspace *Workspace) (bool, error) {
	if workspace == nil {
		return false, nil
	}
	if workspace.ScratchCollectionID != "" {
		for ci := range workspace.Collections {
			collection := &workspace.Collections[ci]
			if collection.ID != workspace.ScratchCollectionID {
				continue
			}
			changed := false
			if !collection.Scratch {
				collection.Scratch = true
				changed = true
			}
			if collection.Name == "" {
				collection.Name = "Scratch"
				changed = true
			}
			if workspace.ScratchTempDirectory == "" && collection.Path != "" {
				workspace.ScratchTempDirectory = collection.Path
				changed = true
			}
			if collection.Path == "" {
				path, err := a.newScratchDirectory(workspace)
				if err != nil {
					return changed, err
				}
				collection.Path = path
				workspace.ScratchTempDirectory = path
				changed = true
			}
			if err := a.writeScratchCollectionMetadataLocked(collection); err != nil {
				return changed, err
			}
			return changed, nil
		}
	}
	now := time.Now()
	path, err := a.newScratchDirectory(workspace)
	if err != nil {
		return false, err
	}
	collectionID := newID("scratch")
	collection := Collection{
		ID:             collectionID,
		Name:           "Scratch",
		Path:           path,
		Format:         "yml",
		Scratch:        true,
		Auth:           AuthConfig{Mode: "none"},
		SecurityConfig: CollectionSecurityConfig{JSSandboxMode: "safe"},
		Docs:           "# Scratch\nTemporary requests for this workspace.\n",
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	workspace.ScratchCollectionID = collectionID
	workspace.ScratchTempDirectory = path
	insertAt := scratchCollectionInsertIndex(workspace.Collections)
	workspace.Collections = append(workspace.Collections, Collection{})
	copy(workspace.Collections[insertAt+1:], workspace.Collections[insertAt:])
	workspace.Collections[insertAt] = collection
	workspace.UpdatedAt = now
	if err := a.writeScratchCollectionMetadataLocked(&workspace.Collections[insertAt]); err != nil {
		return false, err
	}
	return true, nil
}

func scratchCollectionInsertIndex(collections []Collection) int {
	if len(collections) == 0 {
		return 0
	}
	if countRegularCollections(collections) == 0 {
		for index, collection := range collections {
			if collection.Scratch {
				return index
			}
		}
		return 0
	}
	return 1
}

func countRegularCollections(collections []Collection) int {
	count := 0
	for _, collection := range collections {
		if !collection.Scratch {
			count++
		}
	}
	return count
}

func firstScratchCollectionIndex(collections []Collection) int {
	for index, collection := range collections {
		if collection.Scratch {
			return index
		}
	}
	return -1
}

func (a *App) newScratchDirectory(workspace *Workspace) (string, error) {
	base := filepath.Join(a.dataDir, "transient")
	if err := os.MkdirAll(base, 0o755); err != nil {
		return "", err
	}
	return os.MkdirTemp(base, "bruno-scratch-")
}

func (a *App) writeScratchCollectionMetadataLocked(collection *Collection) error {
	if collection == nil || !collection.Scratch {
		return nil
	}
	if strings.TrimSpace(collection.Path) == "" {
		return errors.New("scratch collection path is empty")
	}
	if err := os.MkdirAll(collection.Path, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(collection.Path, "opencollection.yml"), []byte(yamlstore.StringifyCollection(*collection)), 0o600); err != nil {
		return err
	}
	metadata := map[string]string{
		"type": "scratch",
	}
	if ws, _ := scripting.FindWorkspaceForCollection(&a.state, collection.ID); ws != nil {
		metadata["workspaceUid"] = ws.ID
		metadata["workspacePath"] = ws.Path
	}
	data, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(collection.Path, "metadata.json"), data, 0o600)
}
