package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/mutexdev/lite_api/internal/workspacestate"
)

func TestWorkspaceWindowRuntimeLaunchesStrictChildAndHydratesOnlySelectedWorkspace(t *testing.T) {
	dir := t.TempDir()
	parent := newAppInDirForTest(t, dir)
	if err := parent.ensureReady(); err != nil {
		t.Fatal(err)
	}
	if err := ExecuteWorkspaceMigration(dir, parent.state, "main-window"); err != nil {
		t.Fatal(err)
	}
	var executable string
	var args []string
	parent.workspaceProcessStart = func(name string, got []string) error {
		executable, args = name, append([]string(nil), got...)
		return nil
	}
	target, err := parent.OpenWorkspaceInNewWindow(parent.state.ActiveWorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if executable == "" || target.ID == "" {
		t.Fatalf("child launch missing target: executable=%q target=%+v", executable, target)
	}
	if len(args) != 6 || args[0] != "--window-session" || args[2] != "--workspace-id" || args[3] != target.ID || args[4] != "--data-dir" || args[5] != filepath.Clean(dir) {
		t.Fatalf("child launch arguments are not strict: %v", args)
	}
	child := newAppBase(dir)
	if err := child.loadWorkspaceWindow(workspacestate.WindowLaunchIntent{SessionID: args[1], WorkspaceID: target.ID, DataDir: dir}); err != nil {
		t.Fatal(err)
	}
	defer child.workspaceRuntime.release()
	if len(child.state.Workspaces) != 1 || child.state.Workspaces[0].ID != target.ID || len(child.state.Workspaces[0].Collections) != 1 || len(child.state.Workspaces[0].Collections[0].Items) == 0 {
		t.Fatalf("selected workspace was not hydrated from authoritative files: %+v", child.state)
	}
	if got, err := child.ListWorkspaceWindowTargets(); err != nil || len(got) != 1 || !reflect.DeepEqual(got[0], target) {
		t.Fatalf("workspace targets=%+v err=%v", got, err)
	}
}

func TestWorkspaceWindowLaunchPreflightAndSpawnCleanup(t *testing.T) {
	dir := t.TempDir()
	app := newAppInDirForTest(t, dir)
	if err := app.ensureReady(); err != nil {
		t.Fatal(err)
	}
	if err := ExecuteWorkspaceMigration(dir, app.state, "main-window"); err != nil {
		t.Fatal(err)
	}
	ws := app.state.Workspaces[0]
	owner, err := NewWorkspaceWindowLockStore(dir).Acquire(ws.Path, "live", os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	called := false
	app.workspaceProcessStart = func(string, []string) error { called = true; return nil }
	if _, err := app.OpenWorkspaceInNewWindow(ws.ID); err == nil || called {
		t.Fatalf("live owner was not refused before spawn: err=%v called=%v", err, called)
	}
	if err := NewWorkspaceWindowLockStore(dir).Release(owner); err != nil {
		t.Fatal(err)
	}
	app.workspaceProcessStart = func(string, []string) error { return errors.New("spawn failed") }
	if _, err := app.OpenWorkspaceInNewWindow(ws.ID); err == nil {
		t.Fatal("spawn failure accepted")
	}
	entries, err := os.ReadDir(filepath.Join(dir, "window-sessions"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("failed spawn orphaned session: %v", entries)
	}
}

func TestProductionMarkerLaunchRejectsStaleLegacyAndCorruptMarker(t *testing.T) {
	dir := t.TempDir()
	legacy := newAppInDirForTest(t, dir)
	if err := legacy.ensureReady(); err != nil {
		t.Fatal(err)
	}
	legacy.state.Workspaces[0].Name = "scoped truth"
	if err := ExecuteWorkspaceMigration(dir, legacy.state, "main-window"); err != nil {
		t.Fatal(err)
	}
	stale := defaultState(dir)
	stale.Workspaces[0].Name = "stale legacy"
	data, _ := json.Marshal(stale)
	if err := os.WriteFile(filepath.Join(dir, "state.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	app, err := newProductionAppForTest(t, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if app.state.Workspaces[0].Name != "scoped truth" {
		t.Fatalf("relaunch read stale legacy: %+v", app.state.Workspaces[0])
	}
	// Mirrors shutdown: release() gives up the ownership lease that
	// persistWorkspaceRuntimeLocked needs, so pending state must land first.
	flushPersistForTest(t, app)
	app.workspaceRuntime.release()
	if err := os.WriteFile(workspaceMigrationMarkerPath(dir), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := newProductionAppForTest(t, dir, nil); err == nil {
		t.Fatal("corrupt marker silently remigrated")
	}
}

func TestProductionRelaunchAcceptsMutableArtifactsButRejectsCorruption(t *testing.T) {
	dir := t.TempDir()
	legacy := newAppInDirForTest(t, dir)
	if err := legacy.ensureReady(); err != nil {
		t.Fatal(err)
	}
	if err := ExecuteWorkspaceMigration(dir, legacy.state, "main-window"); err != nil {
		t.Fatal(err)
	}
	app, err := newProductionAppForTest(t, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	app.state.Workspaces[0].Name = "Edited Workspace"
	app.state.Preferences.Theme = "dark"
	app.state.OpenTabs = []OpenTab{{ID: "edited-tab", CollectionID: app.state.Workspaces[0].Collections[0].ID}}
	app.state.ActiveTabID = "edited-tab"
	if err := app.persistWorkspaceRuntimeLocked(); err != nil {
		t.Fatal(err)
	}
	// Mirrors shutdown: release() gives up the ownership lease that
	// persistWorkspaceRuntimeLocked needs, so pending state must land first.
	flushPersistForTest(t, app)
	app.workspaceRuntime.release()
	reloaded, err := newProductionAppForTest(t, dir, nil)
	if err != nil {
		t.Fatalf("valid mutable artifacts rejected: %v", err)
	}
	if reloaded.state.Workspaces[0].Name != "Edited Workspace" || reloaded.state.Preferences.Theme != "dark" || reloaded.state.ActiveTabID != "edited-tab" {
		t.Fatalf("mutable state did not reload: %+v", reloaded.state)
	}
	// Mirrors shutdown: release() gives up the ownership lease that
	// persistWorkspaceRuntimeLocked needs, so pending state must land first.
	flushPersistForTest(t, reloaded)
	reloaded.workspaceRuntime.release()
	if err := os.WriteFile(sharedAppStatePath(dir), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := newProductionAppForTest(t, dir, nil); err == nil {
		t.Fatal("corrupt mutable artifact accepted")
	}
}

func TestProductionRelaunchHydratesNewEmptyCollections(t *testing.T) {
	for _, format := range []string{"yml", "bru"} {
		t.Run(format, func(t *testing.T) {
			dir := t.TempDir()
			app, err := newProductionAppForTest(t, dir, nil)
			if err != nil {
				t.Fatal(err)
			}
			state, err := app.GetState()
			if err != nil {
				t.Fatal(err)
			}
			name := "Empty " + format + " Collection"
			state, err = app.CreateCollection(state.ActiveWorkspaceID, name, format)
			if err != nil {
				t.Fatal(err)
			}
			var created *Collection
			for collectionIndex := range state.Workspaces[0].Collections {
				if state.Workspaces[0].Collections[collectionIndex].Name == name {
					created = &state.Workspaces[0].Collections[collectionIndex]
					break
				}
			}
			if created == nil {
				t.Fatalf("created collection missing from state: %+v", state.Workspaces[0].Collections)
			}
			rootMetadata := "bruno.json"
			if format == "yml" {
				rootMetadata = "opencollection.yml"
			}
			if info, err := os.Stat(filepath.Join(created.Path, rootMetadata)); err != nil || !info.Mode().IsRegular() {
				t.Fatalf("empty collection root was not materialized: path=%s info=%v err=%v", created.Path, info, err)
			}
			// Mirrors shutdown: release() gives up the ownership lease that
			// persistWorkspaceRuntimeLocked needs, so pending state must land first.
			flushPersistForTest(t, app)
			app.workspaceRuntime.release()

			reloaded, err := newProductionAppForTest(t, dir, nil)
			if err != nil {
				t.Fatalf("production relaunch rejected empty collection: %v", err)
			}
			defer reloaded.workspaceRuntime.release()
			reloadedState, err := reloaded.GetState()
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, collection := range reloadedState.Workspaces[0].Collections {
				if collection.Name == name && filepath.Clean(collection.Path) == filepath.Clean(created.Path) {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("empty collection did not hydrate after relaunch: %+v", reloadedState.Workspaces[0].Collections)
			}
		})
	}
}

func TestScopedRuntimePersistenceKeepsOtherWorkspaceAndLegacyUntouched(t *testing.T) {
	dir := t.TempDir()
	legacy := AppState{Workspaces: []Workspace{{ID: "a", Name: "A", Path: "/workspace/a"}, {ID: "b", Name: "B", Path: "/workspace/b"}}, ActiveWorkspaceID: "a"}
	legacyBytes, _ := json.Marshal(legacy)
	legacyPath := filepath.Join(dir, "state.json")
	if err := os.WriteFile(legacyPath, legacyBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ExecuteWorkspaceMigration(dir, legacy, "main-window"); err != nil {
		t.Fatal(err)
	}
	beforeB, err := os.ReadFile(workspacestate.WorkspaceScopedStatePath(dir, "b"))
	if err != nil {
		t.Fatal(err)
	}
	legacyHash := fileChecksum(legacyBytes)
	locks := NewWorkspaceWindowLockStore(dir)
	owner, err := locks.Acquire("/workspace/a", "a-session", os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	app := newAppBase(dir)
	app.state = AppState{Workspaces: []Workspace{{ID: "a", Name: "A changed", Path: "/workspace/a"}}, ActiveWorkspaceID: "a"}
	app.workspaceRuntime = &workspaceWindowRuntime{intent: workspacestate.WindowLaunchIntent{SessionID: "a-session", WorkspaceID: "a", DataDir: dir}, owner: owner, locks: locks, stop: make(chan struct{})}
	if err := app.persistWorkspaceRuntimeLocked(); err != nil {
		t.Fatal(err)
	}
	// Mirrors shutdown: release() gives up the ownership lease that
	// persistWorkspaceRuntimeLocked needs, so pending state must land first.
	flushPersistForTest(t, app)
	app.workspaceRuntime.release()
	afterB, err := os.ReadFile(workspacestate.WorkspaceScopedStatePath(dir, "b"))
	if err != nil || fileChecksum(beforeB) != fileChecksum(afterB) {
		t.Fatalf("A persistence changed B: err=%v", err)
	}
	if data, err := os.ReadFile(legacyPath); err != nil || fileChecksum(data) != legacyHash {
		t.Fatalf("A persistence changed legacy: err=%v", err)
	}
	a, err := ReadWorkspaceScopedState(dir, "a")
	if err != nil || a.Workspace.Name != "A changed" {
		t.Fatalf("A did not reload: %+v err=%v", a, err)
	}
	b, err := ReadWorkspaceScopedState(dir, "b")
	if err != nil || b.Workspace.Name != "B" {
		t.Fatalf("B did not reload: %+v err=%v", b, err)
	}
}

func TestReplacedWorkspaceOwnerCannotHeartbeatPersistOrReleaseReplacement(t *testing.T) {
	dir := t.TempDir()
	legacy := AppState{Workspaces: []Workspace{{ID: "a", Name: "A", Path: "/workspace/a"}}, ActiveWorkspaceID: "a"}
	if err := ExecuteWorkspaceMigration(dir, legacy, "main-window"); err != nil {
		t.Fatal(err)
	}
	alive := map[int]bool{1: true, 2: true}
	locks := NewWorkspaceWindowLockStore(dir)
	locks.ProcessAlive = func(pid int) bool { return alive[pid] }
	first, err := locks.Acquire("/workspace/a", "first", 1)
	if err != nil {
		t.Fatal(err)
	}
	alive[1] = false
	replacement, err := locks.Acquire("/workspace/a", "second", 2)
	if err != nil {
		t.Fatal(err)
	}
	app := newAppBase(dir)
	app.state = AppState{Workspaces: []Workspace{{ID: "a", Name: "A", Path: "/workspace/a"}}, ActiveWorkspaceID: "a"}
	app.workspaceRuntime = &workspaceWindowRuntime{intent: workspacestate.WindowLaunchIntent{SessionID: "first", WorkspaceID: "a", DataDir: dir}, owner: first, locks: locks, stop: make(chan struct{})}
	if err := app.workspaceRuntime.heartbeat(); err == nil {
		t.Fatal("replaced owner heartbeated")
	}
	if err := app.persistWorkspaceRuntimeLocked(); err == nil {
		t.Fatal("replaced owner persisted")
	}
	// Mirrors shutdown: release() gives up the ownership lease that
	// persistWorkspaceRuntimeLocked needs, so pending state must land first.
	flushPersistForTest(t, app)
	app.workspaceRuntime.release()
	if _, err := locks.Heartbeat(replacement); err != nil {
		t.Fatalf("release removed replacement: %v", err)
	}
}

func TestWorkspaceSessionReuseRestoresWorkspaceStateAndMainSession(t *testing.T) {
	dir := t.TempDir()
	legacy := AppState{Workspaces: []Workspace{
		{ID: "a", Name: "A", Path: filepath.Join(dir, "a"), Collections: []Collection{{ID: "ca", Name: "CA", NotFoundLocally: true}}},
		{ID: "b", Name: "B", Path: filepath.Join(dir, "b"), Collections: []Collection{{ID: "cb", Name: "CB", NotFoundLocally: true}}},
	}, ActiveWorkspaceID: "a"}
	if err := ExecuteWorkspaceMigration(dir, legacy, "main-window"); err != nil {
		t.Fatal(err)
	}
	a, err := newProductionAppForTest(t, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer a.workspaceRuntime.release()
	var firstArgs, secondArgs []string
	a.workspaceProcessStart = func(_ string, args []string) error {
		if firstArgs == nil {
			firstArgs = append([]string(nil), args...)
		} else {
			secondArgs = append([]string(nil), args...)
		}
		return nil
	}
	if _, err := a.OpenWorkspaceInNewWindow("b"); err != nil {
		t.Fatal(err)
	}
	bSession, err := workspacestate.ReadWindowSession(defaultWorkspaceSessionPath(dir, firstArgs[1]))
	if err != nil {
		t.Fatal(err)
	}
	bSession.OpenTabs = []OpenTab{{ID: "b-tab", CollectionID: "cb"}}
	bSession.ActiveTabID = "b-tab"
	bSession.ResponsePaneOrientation = "vertical"
	bSession.Geometry = workspacestate.WindowGeometry{X: 44, Y: 55, Width: 900, Height: 700}
	bSession.UpdatedAt = time.Now().UTC().Add(time.Minute)
	if err := writeWorkspaceMigrationSession(dir, bSession); err != nil {
		t.Fatal(err)
	}
	if _, err := a.OpenWorkspaceInNewWindow("b"); err != nil {
		t.Fatal(err)
	}
	if firstArgs[1] != secondArgs[1] {
		t.Fatalf("workspace session was not reused: first=%v second=%v", firstArgs, secondArgs)
	}
	b := newAppBase(dir)
	if err := b.loadWorkspaceWindow(workspacestate.WindowLaunchIntent{SessionID: bSession.ID, WorkspaceID: "b", DataDir: dir}); err != nil {
		t.Fatal(err)
	}
	if b.state.ActiveTabID != "b-tab" || b.state.Preferences.Layout.ResponsePaneOrientation != "vertical" || b.workspaceRuntime.session.Geometry != bSession.Geometry {
		t.Fatalf("reused session did not restore: state=%+v session=%+v", b.state, b.workspaceRuntime.session)
	}
	b.workspaceProcessStart = func(_ string, args []string) error {
		secondArgs = append([]string(nil), args...)
		return nil
	}
	// Mirrors shutdown: release() gives up the ownership lease that
	// persistWorkspaceRuntimeLocked needs, so pending state must land first.
	flushPersistForTest(t, a)
	a.workspaceRuntime.release()
	if _, err := b.OpenWorkspaceInNewWindow("a"); err != nil {
		t.Fatal(err)
	}
	if secondArgs[1] != "main-window" {
		t.Fatalf("main workspace session was not reused: %v", secondArgs)
	}
	// Mirrors shutdown: release() gives up the ownership lease that
	// persistWorkspaceRuntimeLocked needs, so pending state must land first.
	flushPersistForTest(t, b)
	b.workspaceRuntime.release()
}

func TestWorkspaceSessionIdentityFilteringAndOrientationRestore(t *testing.T) {
	dir := t.TempDir()
	legacy := AppState{Workspaces: []Workspace{{ID: "a", Name: "A", Path: filepath.Join(dir, "a"), Collections: []Collection{{ID: "allowed", NotFoundLocally: true}}}}, ActiveWorkspaceID: "a"}
	if err := ExecuteWorkspaceMigration(dir, legacy, "main-window"); err != nil {
		t.Fatal(err)
	}
	session, err := workspacestate.ReadWindowSession(defaultWorkspaceSessionPath(dir, "main-window"))
	if err != nil {
		t.Fatal(err)
	}
	session.OpenTabs = []OpenTab{{ID: "foreign", CollectionID: "other"}, {ID: "allowed-tab", CollectionID: "allowed"}}
	session.ClosedTabs = []OpenTab{{ID: "foreign-closed", CollectionID: "other"}}
	session.ActiveTabID = "foreign"
	session.ResponsePaneOrientation = "vertical"
	if err := writeWorkspaceMigrationSession(dir, session); err != nil {
		t.Fatal(err)
	}
	app, err := newProductionAppForTest(t, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(app.state.OpenTabs) != 1 || app.state.ActiveTabID != "allowed-tab" || len(app.state.ClosedTabs) != 0 || app.state.Preferences.Layout.ResponsePaneOrientation != "vertical" {
		t.Fatalf("session was not sanitized/restored: %+v", app.state)
	}
	// Mirrors shutdown: release() gives up the ownership lease that
	// persistWorkspaceRuntimeLocked needs, so pending state must land first.
	flushPersistForTest(t, app)
	app.workspaceRuntime.release()
	session.WorkspaceID = ""
	session.WorkspacePath = filepath.Join(dir, "wrong")
	if err := writeWorkspaceMigrationSession(dir, session); err != nil {
		t.Fatal(err)
	}
	if _, err := newProductionAppForTest(t, dir, nil); err == nil {
		t.Fatal("workspace-path mismatch accepted")
	}
}

func TestReusableWorkspaceSessionDoesNotPathMatchDifferentID(t *testing.T) {
	dir := t.TempDir()
	workspace := workspacestate.WorkspaceRegistryEntry{ID: "target", Path: filepath.Join(dir, "workspace")}
	session := workspacestate.WindowSession{Version: 1, ID: "wrong-id", WorkspaceID: "different", WorkspacePath: "", UpdatedAt: time.Now().UTC()}
	if err := writeWorkspaceMigrationSession(dir, session); err != nil {
		t.Fatal(err)
	}
	// A valid session cannot carry both identities. Write a path-selected
	// session separately to prove that path reuse remains supported.
	pathSession := workspacestate.WindowSession{Version: 1, ID: "path-id", WorkspacePath: workspace.Path, UpdatedAt: time.Now().UTC().Add(-time.Minute)}
	if err := writeWorkspaceMigrationSession(dir, pathSession); err != nil {
		t.Fatal(err)
	}
	got, reused, err := findReusableWorkspaceSession(dir, workspace)
	if err != nil || !reused || got.ID != "path-id" {
		t.Fatalf("session matching crossed workspace IDs: got=%+v reused=%v err=%v", got, reused, err)
	}
}

func TestWorkspaceGeometryRestoreClampsToVisibleScreen(t *testing.T) {
	originalScreens, originalSize, originalPosition := workspaceScreenGetAll, workspaceWindowSetSize, workspaceWindowSetPosition
	defer func() {
		workspaceScreenGetAll, workspaceWindowSetSize, workspaceWindowSetPosition = originalScreens, originalSize, originalPosition
	}()
	var gotSize, gotPosition workspacestate.WindowGeometry
	workspaceScreenGetAll = func(context.Context) ([]wailsruntime.Screen, error) {
		// Size's type lives in an internal Wails package and cannot be named
		// here, so populate it field-by-field.
		primary := wailsruntime.Screen{IsPrimary: true}
		primary.Size.Width, primary.Size.Height = 1280, 720
		return []wailsruntime.Screen{primary}, nil
	}
	workspaceWindowSetSize = func(_ context.Context, width, height int) { gotSize.Width, gotSize.Height = width, height }
	workspaceWindowSetPosition = func(_ context.Context, x, y int) { gotPosition.X, gotPosition.Y = x, y }
	runtime := &workspaceWindowRuntime{session: workspacestate.WindowSession{Geometry: workspacestate.WindowGeometry{X: -999, Y: 4000, Width: 1600, Height: 900}}}
	runtime.restoreGeometry(context.Background())
	if gotSize.Width != 1280 || gotSize.Height != 720 || gotPosition.X != 0 || gotPosition.Y != 0 {
		t.Fatalf("unsafe geometry was not clamped: size=%+v position=%+v", gotSize, gotPosition)
	}
	gotSize, gotPosition = workspacestate.WindowGeometry{}, workspacestate.WindowGeometry{}
	workspaceScreenGetAll = func(context.Context) ([]wailsruntime.Screen, error) { return nil, errors.New("no screens") }
	runtime.restoreGeometry(context.Background())
	if gotSize != (workspacestate.WindowGeometry{}) || gotPosition != (workspacestate.WindowGeometry{}) {
		t.Fatalf("geometry applied without valid screen: size=%+v position=%+v", gotSize, gotPosition)
	}
}

func TestWorkspaceGeometryCaptureKeepsOnlyValidDimensions(t *testing.T) {
	dir := t.TempDir()
	session := workspacestate.WindowSession{Version: 1, ID: "capture", WorkspaceID: "a", Geometry: workspacestate.WindowGeometry{X: 1, Y: 2, Width: 800, Height: 600}}
	if err := writeWorkspaceMigrationSession(dir, session); err != nil {
		t.Fatal(err)
	}
	originalPosition, originalSize := workspaceWindowGetPosition, workspaceWindowGetSize
	defer func() { workspaceWindowGetPosition, workspaceWindowGetSize = originalPosition, originalSize }()
	workspaceWindowGetPosition = func(context.Context) (int, int) { return -20, 30 }
	workspaceWindowGetSize = func(context.Context) (int, int) { return 100, 100 }
	runtime := &workspaceWindowRuntime{intent: workspacestate.WindowLaunchIntent{DataDir: dir}, session: session}
	runtime.captureGeometry(context.Background())
	if runtime.session.Geometry != session.Geometry {
		t.Fatalf("invalid dimensions replaced stored geometry: %+v", runtime.session.Geometry)
	}
	workspaceWindowGetSize = func(context.Context) (int, int) { return 900, 700 }
	runtime.captureGeometry(context.Background())
	if runtime.session.Geometry != (workspacestate.WindowGeometry{X: -20, Y: 30, Width: 900, Height: 700}) {
		t.Fatalf("valid dimensions were not captured: %+v", runtime.session.Geometry)
	}
}

func TestWindowOrientationRemainsSessionPrivateAcrossRuntimes(t *testing.T) {
	dir := t.TempDir()
	legacy := AppState{Preferences: Preferences{Layout: LayoutPreferences{ResponsePaneOrientation: "horizontal"}}, Workspaces: []Workspace{
		{ID: "a", Name: "A", Path: filepath.Join(dir, "a")},
		{ID: "b", Name: "B", Path: filepath.Join(dir, "b")},
	}, ActiveWorkspaceID: "a"}
	if err := ExecuteWorkspaceMigration(dir, legacy, "a-session"); err != nil {
		t.Fatal(err)
	}
	bSession := workspacestate.WindowSession{Version: 1, ID: "b-session", WorkspaceID: "b", ResponsePaneOrientation: "vertical", UpdatedAt: time.Now().UTC()}
	if err := writeWorkspaceMigrationSession(dir, bSession); err != nil {
		t.Fatal(err)
	}
	a := newAppBase(dir)
	if err := a.loadWorkspaceWindow(workspacestate.WindowLaunchIntent{SessionID: "a-session", WorkspaceID: "a", DataDir: dir}); err != nil {
		t.Fatal(err)
	}
	defer a.workspaceRuntime.release()
	b := newAppBase(dir)
	if err := b.loadWorkspaceWindow(workspacestate.WindowLaunchIntent{SessionID: "b-session", WorkspaceID: "b", DataDir: dir}); err != nil {
		t.Fatal(err)
	}
	defer b.workspaceRuntime.release()
	if err := b.persistWorkspaceRuntimeLocked(); err != nil {
		t.Fatal(err)
	}
	if b.state.Preferences.Layout.ResponsePaneOrientation != "vertical" {
		t.Fatalf("B orientation collapsed after shared refresh: %+v", b.state.Preferences.Layout)
	}
	if err := a.persistWorkspaceRuntimeLocked(); err != nil {
		t.Fatal(err)
	}
	shared, err := ReadSharedAppState(dir)
	if err != nil || shared.Preferences.Layout.ResponsePaneOrientation != "horizontal" {
		t.Fatalf("session orientation leaked to shared state: %+v err=%v", shared.Preferences.Layout, err)
	}
	aStored, err := workspacestate.ReadWindowSession(defaultWorkspaceSessionPath(dir, "a-session"))
	if err != nil {
		t.Fatal(err)
	}
	bStored, err := workspacestate.ReadWindowSession(defaultWorkspaceSessionPath(dir, "b-session"))
	if err != nil {
		t.Fatal(err)
	}
	if aStored.ResponsePaneOrientation != "horizontal" || bStored.ResponsePaneOrientation != "vertical" {
		t.Fatalf("session orientations collapsed: A=%q B=%q", aStored.ResponsePaneOrientation, bStored.ResponsePaneOrientation)
	}
}

func TestScopedCreateWorkspaceDoesNotSwitchAndCanLaunchNewTarget(t *testing.T) {
	dir := t.TempDir()
	legacy := AppState{Workspaces: []Workspace{{ID: "a", Name: "A", Path: filepath.Join(dir, "a")}}, ActiveWorkspaceID: "a"}
	if err := ExecuteWorkspaceMigration(dir, legacy, "main-window"); err != nil {
		t.Fatal(err)
	}
	app, err := newProductionAppForTest(t, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer app.workspaceRuntime.release()
	before := app.state.ActiveWorkspaceID
	state, err := app.CreateWorkspace("Second")
	if err != nil {
		t.Fatal(err)
	}
	if state.ActiveWorkspaceID != before || len(state.Workspaces) != 1 {
		t.Fatalf("scoped create switched current state: %+v", state)
	}
	targets, err := app.ListWorkspaceWindowTargets()
	if err != nil || len(targets) != 2 {
		t.Fatalf("targets=%+v err=%v", targets, err)
	}
	var args []string
	app.workspaceProcessStart = func(_ string, got []string) error { args = append([]string(nil), got...); return nil }
	target, err := app.OpenNewWindow()
	if err != nil {
		t.Fatal(err)
	}
	if target.ID == before || len(args) != 6 || args[3] != target.ID {
		t.Fatalf("new target was not launched: target=%+v args=%v", target, args)
	}
	preexisting := filepath.Join(dir, "Existing")
	if err := os.Mkdir(preexisting, 0o700); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(preexisting, "keep.txt")
	if err := os.WriteFile(sentinel, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := app.CreateWorkspace("Existing"); err == nil {
		t.Fatal("preexisting unregistered workspace path accepted")
	}
	if data, err := os.ReadFile(sentinel); err != nil || string(data) != "keep" {
		t.Fatalf("preexisting workspace data changed: %q err=%v", data, err)
	}
	originalWrite := workspacePersistenceWriteAtomic
	workspacePersistenceWriteAtomic = func(path string, data []byte) error {
		if path == workspacestate.WorkspaceRegistryPath(dir) {
			return errors.New("injected registry failure")
		}
		return originalWrite(path, data)
	}
	if _, err := app.CreateWorkspace("Rolled Back"); err == nil {
		t.Fatal("injected registry failure was ignored")
	}
	workspacePersistenceWriteAtomic = originalWrite
	if _, err := os.Lstat(filepath.Join(dir, "Rolled Back")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("newly-created workspace directory was not rolled back: %v", err)
	}
}

func TestReplacedOwnerCannotMutateRequestBeforeSideEffects(t *testing.T) {
	dir := t.TempDir()
	collectionPath := filepath.Join(dir, "collection")
	if err := os.Mkdir(collectionPath, 0o700); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(collectionPath, "request.bru")
	if err := os.WriteFile(sentinel, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	state := AppState{Workspaces: []Workspace{{ID: "a", Name: "A", Path: filepath.Join(dir, "a"), Collections: []Collection{{ID: "c", Name: "C", Path: collectionPath, Items: []RequestItem{{ID: "r", Name: "R", FilePath: sentinel}}}}}}, ActiveWorkspaceID: "a"}
	if err := ExecuteWorkspaceMigration(dir, state, "main-window"); err != nil {
		t.Fatal(err)
	}
	locks := NewWorkspaceWindowLockStore(dir)
	alive := map[int]bool{1: true, 2: true}
	locks.ProcessAlive = func(pid int) bool { return alive[pid] }
	first, err := locks.Acquire(state.Workspaces[0].Path, "first", 1)
	if err != nil {
		t.Fatal(err)
	}
	alive[1] = false
	if _, err := locks.Acquire(state.Workspaces[0].Path, "second", 2); err != nil {
		t.Fatal(err)
	}
	app := newAppBase(dir)
	app.state = state
	app.workspaceRuntime = &workspaceWindowRuntime{intent: workspacestate.WindowLaunchIntent{SessionID: "first", WorkspaceID: "a", DataDir: dir}, owner: first, locks: locks, stop: make(chan struct{})}
	before, _ := json.Marshal(app.state)
	if _, err := app.RenameRequest("c", "r", "Changed", "changed"); err == nil {
		t.Fatal("replaced owner mutated request")
	}
	if _, err := app.DeleteRequest("c", "r"); err == nil {
		t.Fatal("replaced owner deleted request")
	}
	after, _ := json.Marshal(app.state)
	data, readErr := os.ReadFile(sentinel)
	if string(before) != string(after) || readErr != nil || string(data) != "original" {
		t.Fatalf("mutation happened before owner gate: stateChanged=%v data=%q err=%v", string(before) != string(after), data, readErr)
	}
}
