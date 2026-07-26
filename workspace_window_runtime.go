package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mutexdev/lite_api/internal/workspacestate"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

var (
	workspaceScreenGetAll      = wailsruntime.ScreenGetAll
	workspaceWindowGetPosition = wailsruntime.WindowGetPosition
	workspaceWindowGetSize     = wailsruntime.WindowGetSize
	workspaceWindowSetSize     = wailsruntime.WindowSetSize
	workspaceWindowSetPosition = wailsruntime.WindowSetPosition
)

type WorkspaceWindowTarget struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Path string `json:"path"`
}

type workspaceWindowRuntime struct {
	intent         workspacestate.WindowLaunchIntent
	owner          WorkspaceWindowOwner
	session        workspacestate.WindowSession
	sharedBaseline SharedAppState
	locks          WorkspaceWindowLockStore
	stop           chan struct{}
	once           sync.Once
	mu             sync.Mutex
}

// NewProductionApp is the process entry point. Unlike NewAppWithDir (kept for
// established test callers), it migrates once then loads only this window's
// scoped state, its session, and the shared state.
func NewProductionApp(args []string) (*App, error) {
	// Child workspace windows already carry a strict --data-dir as part of
	// their validated launch intent. A main window may also receive --data-dir
	// directly; this makes isolated packaged launches reliable even when macOS
	// app activation does not preserve a shell environment variable.
	if hasWorkspaceWindowArgs(args) {
		intent, err := workspacestate.ParseWindowLaunchIntent(args)
		if err != nil {
			return nil, err
		}
		return newProductionApp(intent.DataDir, args)
	}
	dataDir, remaining, err := productionDataDirFromArgs(args, defaultDataDir())
	if err != nil {
		return nil, err
	}
	return newProductionApp(dataDir, remaining)
}

func productionDataDirFromArgs(args []string, fallback string) (string, []string, error) {
	dataDir := filepath.Clean(fallback)
	remaining := make([]string, 0, len(args))
	found := false
	for index := 0; index < len(args); index += 1 {
		if args[index] != "--data-dir" {
			remaining = append(remaining, args[index])
			continue
		}
		if found || index+1 >= len(args) || strings.TrimSpace(args[index+1]) == "" {
			return "", nil, errors.New("main window --data-dir requires exactly one non-empty path")
		}
		found = true
		dataDir = filepath.Clean(args[index+1])
		index += 1
	}
	return dataDir, remaining, nil
}

func newProductionApp(dataDir string, args []string) (*App, error) {
	dataDir = filepath.Clean(dataDir)
	if hasWorkspaceWindowArgs(args) {
		intent, err := workspacestate.ParseWindowLaunchIntent(args)
		if err != nil {
			return nil, err
		}
		if filepath.Clean(intent.DataDir) != dataDir {
			return nil, errors.New("production data directory does not match launch intent")
		}
		if err := validatePrivateRegularArtifact(workspaceMigrationMarkerPath(dataDir)); err != nil {
			return nil, errors.New("workspace migration artifacts are invalid; refusing to overwrite scoped state from legacy data")
		}
		marker, err := readWorkspaceMigrationMarker(dataDir)
		if err != nil || validateMutableWorkspaceArtifacts(dataDir, marker) != nil {
			return nil, errors.New("workspace migration artifacts are invalid; refusing to overwrite scoped state from legacy data")
		}
		app := newAppBase(dataDir)
		if err := app.loadWorkspaceWindow(intent); err != nil {
			return nil, err
		}
		return app, nil
	}
	if markerInfo, markerStatErr := os.Lstat(workspaceMigrationMarkerPath(dataDir)); markerStatErr == nil {
		if markerInfo.Mode()&os.ModeSymlink != 0 || !markerInfo.Mode().IsRegular() || markerInfo.Mode().Perm() != 0o600 {
			return nil, errors.New("workspace migration marker is invalid; refusing to overwrite scoped state from legacy data")
		}
		marker, err := readWorkspaceMigrationMarker(dataDir)
		if err != nil || validateMutableWorkspaceArtifacts(dataDir, marker) != nil {
			return nil, errors.New("workspace migration marker is invalid; refusing to overwrite scoped state from legacy data")
		}
		session, err := workspacestate.ReadWindowSession(defaultWorkspaceSessionPath(dataDir, marker.DefaultSessionID))
		if err != nil {
			return nil, err
		}
		app := newAppBase(dataDir)
		if err := app.loadWorkspaceWindow(workspacestate.WindowLaunchIntent{SessionID: session.ID, WorkspaceID: session.WorkspaceID, DataDir: dataDir}); err != nil {
			return nil, err
		}
		return app, nil
	} else if !errors.Is(markerStatErr, os.ErrNotExist) {
		return nil, markerStatErr
	}

	legacy := NewAppWithDir(dataDir)
	if err := legacy.ensureReady(); err != nil {
		return nil, err
	}
	defaultSessionID := "main-window"
	if err := ExecuteWorkspaceMigration(dataDir, legacy.state, defaultSessionID); err != nil {
		return nil, fmt.Errorf("migrate workspace state: %w", err)
	}
	if len(legacy.state.Workspaces) == 0 {
		return nil, errors.New("workspace migration did not produce a workspace")
	}
	intent := workspacestate.WindowLaunchIntent{SessionID: defaultSessionID, WorkspaceID: legacy.state.ActiveWorkspaceID, DataDir: dataDir}
	if intent.WorkspaceID == "" {
		intent.WorkspaceID = legacy.state.Workspaces[0].ID
	}
	app := newAppBase(dataDir)
	if err := app.loadWorkspaceWindow(intent); err != nil {
		return nil, err
	}
	return app, nil
}

func hasWorkspaceWindowArgs(args []string) bool {
	for _, arg := range args {
		if arg == "--window-session" || arg == "--workspace-id" || arg == "--workspace-path" {
			return true
		}
	}
	return false
}

func (a *App) loadWorkspaceWindow(intent workspacestate.WindowLaunchIntent) error {
	if err := validatePrivateRegularArtifact(workspacestate.WorkspaceRegistryPath(intent.DataDir)); err != nil {
		return fmt.Errorf("read workspace registry: %w", err)
	}
	registry, err := workspacestate.ReadWorkspaceRegistry(intent.DataDir)
	if err != nil {
		return fmt.Errorf("read workspace registry: %w", err)
	}
	target, err := registry.Resolve(intent.WorkspaceID, intent.WorkspacePath)
	if err != nil {
		return err
	}
	sessionPath := defaultWorkspaceSessionPath(intent.DataDir, intent.SessionID)
	if err := validatePrivateRegularArtifact(sessionPath); err != nil {
		return fmt.Errorf("read window session: %w", err)
	}
	session, err := workspacestate.ReadWindowSession(sessionPath)
	if err != nil {
		return fmt.Errorf("read window session: %w", err)
	}
	if session.ID != intent.SessionID || (session.WorkspaceID != "" && session.WorkspaceID != target.ID) {
		return errors.New("window session does not match selected workspace")
	}
	if session.WorkspacePath != "" && !sameCanonicalWorkspacePath(session.WorkspacePath, target.Path) {
		return errors.New("window session path does not match selected workspace")
	}
	locks := NewWorkspaceWindowLockStore(intent.DataDir)
	owner, err := locks.Acquire(target.Path, intent.SessionID, os.Getpid())
	if err != nil {
		return fmt.Errorf("open workspace window: %w", err)
	}
	if err := validatePrivateRegularArtifact(sharedAppStatePath(intent.DataDir)); err != nil {
		_ = locks.Release(owner)
		return err
	}
	shared, err := ReadSharedAppState(intent.DataDir)
	if err != nil {
		_ = locks.Release(owner)
		return err
	}
	if err := validatePrivateRegularArtifact(workspacestate.WorkspaceScopedStatePath(intent.DataDir, target.ID)); err != nil {
		_ = locks.Release(owner)
		return err
	}
	scoped, err := ReadWorkspaceScopedState(intent.DataDir, target.ID)
	if err != nil {
		_ = locks.Release(owner)
		return err
	}
	if scoped.Workspace.ID != target.ID || !sameCanonicalWorkspacePath(scoped.Workspace.Path, target.Path) {
		_ = locks.Release(owner)
		return errors.New("registry and scoped workspace identity do not match")
	}
	session.OpenTabs, session.ClosedTabs, session.ActiveTabID = sanitizeSessionTabsForScopedWorkspace(session, scoped)
	workspace, err := a.hydrateWorkspaceReference(scoped.Workspace)
	if err != nil {
		_ = locks.Release(owner)
		return err
	}
	a.state = AppState{
		Workspaces:         []Workspace{workspace},
		ActiveWorkspaceID:  workspace.ID,
		OpenTabs:           append([]OpenTab(nil), session.OpenTabs...),
		ClosedTabs:         append([]OpenTab(nil), session.ClosedTabs...),
		ActiveTabID:        session.ActiveTabID,
		FeatureLedger:      shared.FeatureLedger,
		GlobalEnvironments: shared.GlobalEnvironments,
		Preferences:        shared.Preferences,
		Notifications:      shared.Notifications,
		Cookies:            shared.Cookies,
	}
	if session.ResponsePaneOrientation != "" {
		a.state.Preferences.Layout.ResponsePaneOrientation = session.ResponsePaneOrientation
	}
	baseline, err := ProjectSharedAppState(AppState{Preferences: shared.Preferences, FeatureLedger: shared.FeatureLedger, GlobalEnvironments: shared.GlobalEnvironments, Notifications: shared.Notifications, Cookies: shared.Cookies}, intent.DataDir)
	if err != nil {
		_ = locks.Release(owner)
		return err
	}
	a.workspaceRuntime = &workspaceWindowRuntime{intent: intent, owner: owner, session: session, sharedBaseline: baseline, locks: locks, stop: make(chan struct{})}
	if err := a.loadOAuth2Credentials(); err != nil {
		_ = locks.Release(owner)
		return err
	}
	return nil
}

func sameCanonicalWorkspacePath(a, b string) bool {
	ca, ea := workspacestate.CanonicalWorkspaceIdentity(a)
	cb, eb := workspacestate.CanonicalWorkspaceIdentity(b)
	return ea == nil && eb == nil && ca == cb
}
func sanitizeSessionTabsForScopedWorkspace(session workspacestate.WindowSession, scoped workspacestate.WorkspaceScopedState) ([]OpenTab, []OpenTab, string) {
	allowed := map[string]bool{}
	for _, c := range scoped.Workspace.Collections {
		allowed[c.ID] = true
	}
	filter := func(tabs []OpenTab) []OpenTab {
		out := make([]OpenTab, 0, len(tabs))
		for _, t := range tabs {
			if allowed[t.CollectionID] {
				out = append(out, t)
			}
		}
		return out
	}
	open, closed := filter(session.OpenTabs), filter(session.ClosedTabs)
	active := session.ActiveTabID
	if !workspacestate.TabIDPresent(open, active) {
		active = ""
		if len(open) > 0 {
			active = open[0].ID
		}
	}
	return open, closed, active
}

func (a *App) hydrateWorkspaceReference(reference workspacestate.WorkspaceScopedReference) (Workspace, error) {
	workspace := Workspace{ID: reference.ID, Name: reference.Name, Path: reference.Path, Docs: reference.Docs, ActiveGlobalEnvironmentID: reference.ActiveGlobalEnvironmentID, CreatedAt: reference.CreatedAt, UpdatedAt: reference.UpdatedAt}
	if strings.TrimSpace(workspace.Path) != "" {
		if environments, err := readWorkspaceGlobalEnvironments(workspace.Path); err == nil {
			workspace.GlobalEnvironments = environments
		} else if !errors.Is(err, os.ErrNotExist) {
			return Workspace{}, err
		}
	}
	for _, collectionReference := range reference.Collections {
		collection := Collection{ID: collectionReference.ID, Name: collectionReference.Name, Path: collectionReference.Path, Format: collectionReference.Format, Remote: collectionReference.Remote, NotFoundLocally: collectionReference.NotFoundLocally, CreatedAt: collectionReference.CreatedAt, UpdatedAt: collectionReference.UpdatedAt}
		if !collection.NotFoundLocally && strings.TrimSpace(collection.Path) != "" {
			loaded, err := readCollectionFromDisk(collection.Path)
			if err != nil {
				if errors.Is(err, os.ErrNotExist) && collection.Remote != "" {
					collection.NotFoundLocally = true
				} else {
					return Workspace{}, fmt.Errorf("hydrate collection %s: %w", collection.ID, err)
				}
			} else {
				loaded.ID = collection.ID
				loaded.Name = firstNonEmpty(loaded.Name, collection.Name)
				loaded.Path = collection.Path
				loaded.Format = firstNonEmpty(loaded.Format, collection.Format)
				loaded.Remote = collection.Remote
				loaded.NotFoundLocally = false
				collection = loaded
			}
		}
		workspace.Collections = append(workspace.Collections, collection)
	}
	if err := a.hydrateWorkspaceEnvironmentSecretsLocked(&workspace); err != nil {
		return Workspace{}, err
	}
	for i := range workspace.Collections {
		if err := a.hydrateCollectionEnvironmentSecretsLocked(&workspace.Collections[i]); err != nil {
			return Workspace{}, err
		}
	}
	return workspace, nil
}

func (r *workspaceWindowRuntime) startHeartbeat() {
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-r.stop:
				return
			case <-ticker.C:
				_ = r.heartbeat()
			}
		}
	}()
}

func (r *workspaceWindowRuntime) restoreGeometry(ctx context.Context) {
	r.mu.Lock()
	geometry := r.session.Geometry
	r.mu.Unlock()
	if geometry.Width < 320 || geometry.Height < 240 || geometry.Width > 10000 || geometry.Height > 10000 {
		return
	}
	screens, err := workspaceScreenGetAll(ctx)
	if err != nil {
		return
	}
	width, height, ok := preferredWorkspaceScreenSize(screens)
	if !ok || width < 320 || height < 240 {
		return
	}
	geometry.Width = min(geometry.Width, width)
	geometry.Height = min(geometry.Height, height)
	geometry.X = max(0, min(geometry.X, width-geometry.Width))
	geometry.Y = max(0, min(geometry.Y, height-geometry.Height))
	workspaceWindowSetSize(ctx, geometry.Width, geometry.Height)
	workspaceWindowSetPosition(ctx, geometry.X, geometry.Y)
}

func preferredWorkspaceScreenSize(screens []wailsruntime.Screen) (int, int, bool) {
	choose := func(screen wailsruntime.Screen) (int, int, bool) {
		// Screen.Width/Height are deprecated. Every v2.10.2 backend (darwin,
		// linux, windows) populates Size, so it is the only source we need.
		width, height := screen.Size.Width, screen.Size.Height
		return width, height, width > 0 && height > 0
	}
	for _, current := range []bool{true, false} {
		for _, screen := range screens {
			if (current && screen.IsCurrent) || (!current && screen.IsPrimary) {
				if width, height, ok := choose(screen); ok {
					return width, height, true
				}
			}
		}
	}
	for _, screen := range screens {
		if width, height, ok := choose(screen); ok {
			return width, height, true
		}
	}
	return 0, 0, false
}

func (r *workspaceWindowRuntime) captureGeometry(ctx context.Context) {
	x, y := workspaceWindowGetPosition(ctx)
	width, height := workspaceWindowGetSize(ctx)
	if width < 320 || height < 240 || width > 10000 || height > 10000 {
		return
	}
	r.mu.Lock()
	r.session.Geometry = workspacestate.WindowGeometry{X: x, Y: y, Width: width, Height: height}
	session := r.session
	r.mu.Unlock()
	_ = withSharedWorkspacePersistenceGuard(r.intent.DataDir, func() error { return writeWorkspaceMigrationSession(r.intent.DataDir, session) })
}

func (r *workspaceWindowRuntime) heartbeat() error {
	r.mu.Lock()
	owner := r.owner
	r.mu.Unlock()
	updated, err := r.locks.Heartbeat(owner)
	if err != nil {
		return err
	}
	r.mu.Lock()
	r.owner = updated
	r.mu.Unlock()
	return nil
}

func (r *workspaceWindowRuntime) release() {
	if r == nil {
		return
	}
	r.once.Do(func() {
		close(r.stop)
		r.mu.Lock()
		owner := r.owner
		r.mu.Unlock()
		_ = r.locks.Release(owner)
	})
}

// Read-only: reads a.dataDir, then parses the on-disk registry into a fresh
// slice. Unlike OpenNewWindow it does not need mutual exclusion, because it
// neither writes App fields nor takes any action whose effect depends on
// another goroutine not doing the same thing concurrently.
func (a *App) ListWorkspaceWindowTargets() ([]WorkspaceWindowTarget, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	registry, err := workspacestate.ReadWorkspaceRegistry(a.dataDir)
	if err != nil {
		return nil, err
	}
	targets := make([]WorkspaceWindowTarget, 0, len(registry.Workspaces))
	for _, workspace := range registry.Workspaces {
		targets = append(targets, WorkspaceWindowTarget{ID: workspace.ID, Name: workspace.Name, Path: workspace.Path})
	}
	sort.Slice(targets, func(i, j int) bool { return targets[i].Name < targets[j].Name })
	return targets, nil
}

func (a *App) createScopedWorkspaceTargetLocked(name string) (AppState, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return AppState{}, errors.New("workspace name is required")
	}
	var createdPath string
	err := withSharedWorkspacePersistenceGuard(a.dataDir, func() error {
		registry, err := workspacestate.ReadWorkspaceRegistry(a.dataDir)
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		workspace := Workspace{ID: newID("workspace"), Name: name, Path: filepath.Join(a.dataDir, sanitizeFilename(name)), Docs: "# Workspace notes\n", CreatedAt: now, UpdatedAt: now}
		for _, entry := range registry.Workspaces {
			if sameCanonicalWorkspacePath(entry.Path, workspace.Path) {
				return errors.New("workspace path already exists")
			}
		}
		if _, err := os.Lstat(workspace.Path); err == nil {
			return errors.New("workspace path already exists")
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := os.Mkdir(workspace.Path, 0o700); err != nil {
			return err
		}
		createdPath = workspace.Path
		scoped, err := workspacestate.ProjectWorkspaceState(AppState{Workspaces: []Workspace{workspace}, ActiveWorkspaceID: workspace.ID}, workspace.ID)
		if err != nil {
			return err
		}
		if err := WriteWorkspaceScopedState(a.dataDir, scoped); err != nil {
			return err
		}
		registry.Workspaces = append(registry.Workspaces, workspacestate.WorkspaceRegistryEntry{ID: workspace.ID, Name: workspace.Name, Path: workspace.Path, UpdatedAt: now})
		if err := writeWorkspaceMigrationRegistry(a.dataDir, registry); err != nil {
			_ = os.Remove(workspacestate.WorkspaceScopedStatePath(a.dataDir, workspace.ID))
			return err
		}
		return nil
	})
	if err != nil {
		if createdPath != "" {
			_ = os.Remove(createdPath)
		}
		return AppState{}, err
	}
	return a.state, nil
}

func (a *App) OpenNewWindow() (WorkspaceWindowTarget, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	registry, err := workspacestate.ReadWorkspaceRegistry(a.dataDir)
	if err != nil {
		return WorkspaceWindowTarget{}, err
	}
	current := a.state.ActiveWorkspaceID
	for _, workspace := range registry.Workspaces {
		if workspace.ID == current {
			continue
		}
		if NewWorkspaceWindowLockStore(a.dataDir).Available(workspace.Path) != nil {
			continue
		}
		return a.openWorkspaceInNewWindowLocked(workspace.ID)
	}
	return WorkspaceWindowTarget{}, errors.New("no other workspace is available for a new window")
}

func (a *App) OpenWorkspaceInNewWindow(workspaceID string) (WorkspaceWindowTarget, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.openWorkspaceInNewWindowLocked(workspaceID)
}

func (a *App) openWorkspaceInNewWindowLocked(workspaceID string) (WorkspaceWindowTarget, error) {
	registry, err := workspacestate.ReadWorkspaceRegistry(a.dataDir)
	if err != nil {
		return WorkspaceWindowTarget{}, err
	}
	workspace, err := registry.Resolve(workspaceID, "")
	if err != nil {
		return WorkspaceWindowTarget{}, err
	}
	session, reused, err := findReusableWorkspaceSession(a.dataDir, workspace)
	if err != nil {
		return WorkspaceWindowTarget{}, err
	}
	sessionID := session.ID
	if !reused {
		digest := fmt.Sprintf("%x", sha256.Sum256([]byte(workspace.ID)))
		sessionID = "workspace-" + digest[:20]
		if _, err := os.Lstat(defaultWorkspaceSessionPath(a.dataDir, sessionID)); err == nil {
			return WorkspaceWindowTarget{}, errors.New("deterministic workspace session path already contains invalid state")
		} else if !errors.Is(err, os.ErrNotExist) {
			return WorkspaceWindowTarget{}, err
		}
	}
	if err := validateWorkspaceMigrationSessionID(sessionID); err != nil {
		return WorkspaceWindowTarget{}, err
	}
	if a.workspaceRuntime != nil && a.workspaceRuntime.intent.WorkspaceID == workspace.ID {
		return WorkspaceWindowTarget{}, errors.New("workspace is already open in this window")
	}
	if err := NewWorkspaceWindowLockStore(a.dataDir).Available(workspace.Path); err != nil {
		return WorkspaceWindowTarget{}, err
	}
	if !reused {
		scoped, err := ReadWorkspaceScopedState(a.dataDir, workspace.ID)
		if err != nil {
			return WorkspaceWindowTarget{}, err
		}
		session = workspacestate.WindowSession{Version: workspacestate.WindowSessionVersion, ID: sessionID, WorkspaceID: workspace.ID, OpenTabs: scoped.OpenTabs, ClosedTabs: scoped.ClosedTabs, ActiveTabID: scoped.ActiveTabID, ResponsePaneOrientation: a.state.Preferences.Layout.ResponsePaneOrientation, UpdatedAt: time.Now().UTC()}
		if err := writeWorkspaceMigrationSession(a.dataDir, session); err != nil {
			return WorkspaceWindowTarget{}, err
		}
	}
	cleanupSession := func() {
		if !reused {
			_ = os.Remove(defaultWorkspaceSessionPath(a.dataDir, sessionID))
		}
	}
	executable, err := os.Executable()
	if err != nil {
		cleanupSession()
		return WorkspaceWindowTarget{}, err
	}
	args := []string{"--window-session", sessionID, "--workspace-id", workspace.ID, "--data-dir", filepath.Clean(a.dataDir)}
	if a.workspaceProcessStart == nil {
		cleanupSession()
		return WorkspaceWindowTarget{}, errors.New("workspace window process launcher is unavailable")
	}
	if err := a.workspaceProcessStart(executable, args); err != nil {
		cleanupSession()
		return WorkspaceWindowTarget{}, err
	}
	return WorkspaceWindowTarget{ID: workspace.ID, Name: workspace.Name, Path: workspace.Path}, nil
}

func findReusableWorkspaceSession(dataDir string, workspace workspacestate.WorkspaceRegistryEntry) (workspacestate.WindowSession, bool, error) {
	dir := filepath.Join(dataDir, "window-sessions")
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return workspacestate.WindowSession{}, false, nil
	}
	if err != nil {
		return workspacestate.WindowSession{}, false, err
	}
	var best workspacestate.WindowSession
	found := false
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		if err := validatePrivateRegularArtifact(path); err != nil {
			continue
		}
		session, err := workspacestate.ReadWindowSession(path)
		if err != nil || defaultWorkspaceSessionPath(dataDir, session.ID) != path {
			continue
		}
		matches := session.WorkspaceID == workspace.ID
		if session.WorkspaceID == "" {
			matches = session.WorkspacePath != "" && sameCanonicalWorkspacePath(session.WorkspacePath, workspace.Path)
		}
		if !matches {
			continue
		}
		if !found || session.UpdatedAt.After(best.UpdatedAt) || (session.UpdatedAt.Equal(best.UpdatedAt) && session.ID < best.ID) {
			best, found = session, true
		}
	}
	return best, found, nil
}

func (a *App) persistWorkspaceRuntimeLocked() error {
	if a.workspaceRuntime == nil || len(a.state.Workspaces) != 1 {
		return errors.New("workspace runtime persistence requires one selected workspace")
	}
	if err := a.workspaceRuntime.heartbeat(); err != nil {
		return fmt.Errorf("workspace ownership was lost: %w", err)
	}
	return withSharedWorkspacePersistenceGuard(a.dataDir, func() error {
		if err := a.storeStateEnvironmentSecretsLocked(); err != nil {
			return err
		}
		if err := a.storeOAuth2Credentials(); err != nil {
			return err
		}
		registry, err := workspacestate.ReadWorkspaceRegistry(a.dataDir)
		if err != nil {
			return err
		}
		workspace := a.state.Workspaces[0]
		updated := false
		for i := range registry.Workspaces {
			if registry.Workspaces[i].ID == workspace.ID {
				registry.Workspaces[i] = workspacestate.WorkspaceRegistryEntry{ID: workspace.ID, Name: workspace.Name, Path: workspace.Path, UpdatedAt: workspace.UpdatedAt}
				updated = true
			}
		}
		if !updated {
			registry.Workspaces = append(registry.Workspaces, workspacestate.WorkspaceRegistryEntry{ID: workspace.ID, Name: workspace.Name, Path: workspace.Path, UpdatedAt: workspace.UpdatedAt})
		}
		if err := writeWorkspaceMigrationRegistry(a.dataDir, registry); err != nil {
			return err
		}
		scoped, err := workspacestate.ProjectWorkspaceState(a.state, workspace.ID)
		if err != nil {
			return err
		}
		if err := ensureScopedCollectionIDsUnique(a.dataDir, registry, scoped); err != nil {
			return err
		}
		if err := WriteWorkspaceScopedState(a.dataDir, scoped); err != nil {
			return err
		}
		a.workspaceRuntime.mu.Lock()
		baseline := a.workspaceRuntime.sharedBaseline
		a.workspaceRuntime.mu.Unlock()
		sessionOrientation := a.state.Preferences.Layout.ResponsePaneOrientation
		sharedProjectionState := a.state
		// Pane orientation is window-session state. Keeping the shared baseline
		// value here makes it a no-op in the shared three-way merge.
		sharedProjectionState.Preferences.Layout.ResponsePaneOrientation = baseline.Preferences.Layout.ResponsePaneOrientation
		shared, err := ProjectSharedAppState(sharedProjectionState, a.dataDir)
		if err != nil {
			return err
		}
		if existing, err := ReadSharedAppState(a.dataDir); err == nil {
			existingStored, err := ProjectSharedAppState(AppState{Preferences: existing.Preferences, FeatureLedger: existing.FeatureLedger, GlobalEnvironments: existing.GlobalEnvironments, Notifications: existing.Notifications, Cookies: existing.Cookies}, a.dataDir)
			if err != nil {
				return err
			}
			shared, err = mergeWorkspaceSharedDelta(baseline, shared, existingStored)
			if err != nil {
				return err
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := WriteSharedAppState(a.dataDir, shared); err != nil {
			return err
		}
		if refreshed, err := ReadSharedAppState(a.dataDir); err == nil {
			a.state.Preferences, a.state.FeatureLedger = refreshed.Preferences, refreshed.FeatureLedger
			a.state.Preferences.Layout.ResponsePaneOrientation = sessionOrientation
			a.state.GlobalEnvironments, a.state.Notifications, a.state.Cookies = refreshed.GlobalEnvironments, refreshed.Notifications, refreshed.Cookies
			stored, err := ProjectSharedAppState(AppState{Preferences: refreshed.Preferences, FeatureLedger: refreshed.FeatureLedger, GlobalEnvironments: refreshed.GlobalEnvironments, Notifications: refreshed.Notifications, Cookies: refreshed.Cookies}, a.dataDir)
			if err != nil {
				return err
			}
			a.workspaceRuntime.mu.Lock()
			a.workspaceRuntime.sharedBaseline = stored
			a.workspaceRuntime.mu.Unlock()
		} else {
			return err
		}
		session := workspacestate.WindowSession{Version: workspacestate.WindowSessionVersion, ID: a.workspaceRuntime.intent.SessionID, WorkspaceID: workspace.ID, OpenTabs: scoped.OpenTabs, ClosedTabs: scoped.ClosedTabs, ActiveTabID: scoped.ActiveTabID, ResponsePaneOrientation: sessionOrientation, UpdatedAt: time.Now().UTC()}
		a.workspaceRuntime.mu.Lock()
		session.Geometry = a.workspaceRuntime.session.Geometry
		a.workspaceRuntime.session = session
		a.workspaceRuntime.mu.Unlock()
		if err := writeWorkspaceMigrationSession(a.dataDir, session); err != nil {
			return err
		}
		return nil
	})
}

func mergeWorkspaceSharedDelta(base, current, existing SharedAppState) (SharedAppState, error) {
	result := existing
	preferences, err := mergePreferencesDelta(base.Preferences, current.Preferences, existing.Preferences)
	if err != nil {
		return SharedAppState{}, err
	}
	result.Preferences = preferences
	result.FeatureLedger = mergeSharedSlice(base.FeatureLedger, current.FeatureLedger, existing.FeatureLedger, func(value Feature) string { return value.ID })
	result.Notifications = mergeSharedSlice(base.Notifications, current.Notifications, existing.Notifications, func(value Notification) string { return value.ID })
	result.Cookies = mergeSharedSlice(base.Cookies, current.Cookies, existing.Cookies, func(value CookieEntry) string { return value.ID })
	result.GlobalEnvironments, err = mergeEnvironmentDelta(base.GlobalEnvironments, current.GlobalEnvironments, existing.GlobalEnvironments)
	if err != nil {
		return SharedAppState{}, err
	}
	result.Version = current.Version
	result.UpdatedAt = time.Now().UTC()
	return result, nil
}

func mergePreferencesDelta(base, current, disk Preferences) (Preferences, error) {
	encode := func(value Preferences) (map[string]any, error) {
		data, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		var out map[string]any
		err = json.Unmarshal(data, &out)
		return out, err
	}
	var b, c, d map[string]any
	var err error
	if b, err = encode(base); err != nil {
		return Preferences{}, err
	}
	if c, err = encode(current); err != nil {
		return Preferences{}, err
	}
	if d, err = encode(disk); err != nil {
		return Preferences{}, err
	}
	data, err := json.Marshal(mergeJSONDelta(b, c, d))
	if err != nil {
		return Preferences{}, err
	}
	var result Preferences
	err = json.Unmarshal(data, &result)
	return result, err
}

func mergeJSONDelta(base, current, disk map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range disk {
		out[k] = v
	}
	keys := map[string]bool{}
	for k := range base {
		keys[k] = true
	}
	for k := range current {
		keys[k] = true
	}
	for k := range keys {
		b, bok := base[k]
		c, cok := current[k]
		d := disk[k]
		if bok && !cok {
			delete(out, k)
			continue
		}
		if !bok && cok {
			out[k] = c
			continue
		}
		bm, bmok := b.(map[string]any)
		cm, cmok := c.(map[string]any)
		dm, dmok := d.(map[string]any)
		if bmok && cmok && dmok {
			out[k] = mergeJSONDelta(bm, cm, dm)
			continue
		}
		if !reflect.DeepEqual(b, c) {
			out[k] = c
		}
	}
	return out
}

func mergeEnvironmentDelta(base, current, disk []Environment) ([]Environment, error) {
	merged := mergeSharedSlice(base, current, disk, func(value Environment) string { return firstNonEmpty(value.ID, value.Name) })
	baseBy := map[string]Environment{}
	curBy := map[string]Environment{}
	diskBy := map[string]Environment{}
	for _, v := range base {
		baseBy[firstNonEmpty(v.ID, v.Name)] = v
	}
	for _, v := range current {
		curBy[firstNonEmpty(v.ID, v.Name)] = v
	}
	for _, v := range disk {
		diskBy[firstNonEmpty(v.ID, v.Name)] = v
	}
	for i := range merged {
		k := firstNonEmpty(merged[i].ID, merged[i].Name)
		b, ok := baseBy[k]
		c, cok := curBy[k]
		d, dok := diskBy[k]
		if ok && cok && dok {
			metadata, err := mergeEnvironmentMetadata(b, c, d)
			if err != nil {
				return nil, err
			}
			metadata.Variables = mergeSharedSlice(b.Variables, c.Variables, d.Variables, func(v Variable) string { return firstNonEmpty(v.ID, v.Name) })
			merged[i] = metadata
		}
	}
	return merged, nil
}

func mergeEnvironmentMetadata(base, current, disk Environment) (Environment, error) {
	encode := func(value Environment) (map[string]any, error) {
		data, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		var result map[string]any
		if err := json.Unmarshal(data, &result); err != nil {
			return nil, err
		}
		delete(result, "variables")
		return result, nil
	}
	b, err := encode(base)
	if err != nil {
		return Environment{}, err
	}
	c, err := encode(current)
	if err != nil {
		return Environment{}, err
	}
	d, err := encode(disk)
	if err != nil {
		return Environment{}, err
	}
	data, err := json.Marshal(mergeJSONDelta(b, c, d))
	if err != nil {
		return Environment{}, err
	}
	var result Environment
	if err := json.Unmarshal(data, &result); err != nil {
		return Environment{}, err
	}
	return result, nil
}

func mergeSharedSlice[T any](base, current, disk []T, key func(T) string) []T {
	b := map[string]T{}
	c := map[string]T{}
	out := map[string]T{}
	for _, v := range base {
		b[key(v)] = v
	}
	for _, v := range current {
		c[key(v)] = v
	}
	for _, v := range disk {
		out[key(v)] = v
	}
	for k, v := range b {
		if next, ok := c[k]; !ok {
			delete(out, k)
		} else if !reflect.DeepEqual(v, next) {
			out[k] = next
		}
	}
	for k, v := range c {
		if _, ok := b[k]; !ok {
			out[k] = v
		}
	}
	keys := make([]string, 0, len(out))
	for k := range out {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	result := make([]T, 0, len(keys))
	for _, k := range keys {
		result = append(result, out[k])
	}
	return result
}

func ensureScopedCollectionIDsUnique(dataDir string, registry workspacestate.WorkspaceRegistry, scoped workspacestate.WorkspaceScopedState) error {
	owned := map[string]bool{}
	for _, collection := range scoped.Workspace.Collections {
		owned[collection.ID] = true
	}
	for _, workspace := range registry.Workspaces {
		if workspace.ID == scoped.Workspace.ID {
			continue
		}
		other, err := ReadWorkspaceScopedState(dataDir, workspace.ID)
		if err != nil {
			return err
		}
		for _, collection := range other.Workspace.Collections {
			if owned[collection.ID] {
				return fmt.Errorf("collection ID %s is already owned by workspace %s", collection.ID, workspace.ID)
			}
		}
	}
	return nil
}

func withSharedWorkspacePersistenceGuard(dataDir string, fn func() error) error {
	guardPath := filepath.Join(filepath.Clean(dataDir), "workspace-shared-state.guard")
	if err := os.MkdirAll(filepath.Dir(guardPath), 0o700); err != nil {
		return err
	}
	file, err := os.OpenFile(guardPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	unlockFile, err := lockFileExclusive(file)
	if err != nil {
		return err
	}
	// Best-effort: the deferred file close already releases this lock.
	defer unlockFile()
	return fn()
}
