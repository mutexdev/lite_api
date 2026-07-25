package main

import (
	"strings"
	"testing"
)

func TestProductionDataDirectoriesDoNotShareSavedRequestsOrOpenTabs(t *testing.T) {
	firstDir := t.TempDir()
	secondDir := t.TempDir()
	const requestName = "Production isolation sentinel"

	first, err := newProductionApp(firstDir, nil)
	if err != nil {
		t.Fatal(err)
	}
	firstState, err := first.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collectionID := firstState.Workspaces[0].Collections[0].ID
	created, err := first.CreateRequest(collectionID, "grpc", requestName)
	if err != nil {
		t.Fatal(err)
	}
	var requestID string
	for _, request := range created.Workspaces[0].Collections[0].Items {
		if request.Name == requestName {
			requestID = request.ID
			break
		}
	}
	if requestID == "" {
		t.Fatalf("created request missing from first state: %+v", created.Workspaces[0].Collections[0].Items)
	}
	if _, err := first.SaveRequest(collectionID, requestID); err != nil {
		t.Fatal(err)
	}
	// Mirrors shutdown: release() gives up the ownership lease that
	// persistWorkspaceRuntimeLocked needs, so pending state must land first.
	flushPersistForTest(t, first)
	first.workspaceRuntime.release()

	reloadedFirst, err := newProductionApp(firstDir, nil)
	if err != nil {
		t.Fatal(err)
	}
	firstReloadedState, err := reloadedFirst.GetState()
	if err != nil {
		t.Fatal(err)
	}
	if !stateHasRequestNamed(firstReloadedState, requestName) || !stateHasOpenTabForRequest(firstReloadedState, requestID) {
		t.Fatalf("same-directory production relaunch lost request/tab: %+v", firstReloadedState)
	}
	// Mirrors shutdown: release() gives up the ownership lease that
	// persistWorkspaceRuntimeLocked needs, so pending state must land first.
	flushPersistForTest(t, reloadedFirst)
	reloadedFirst.workspaceRuntime.release()

	second, err := newProductionApp(secondDir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer second.workspaceRuntime.release()
	secondState, err := second.GetState()
	if err != nil {
		t.Fatal(err)
	}
	if stateHasRequestNamed(secondState, requestName) || stateHasOpenTabForRequest(secondState, requestID) {
		t.Fatalf("distinct production data directory leaked request or tab: %+v", secondState)
	}
}

func TestProductionMainWindowDataDirArgumentOverridesEnvironmentFallback(t *testing.T) {
	selected := t.TempDir()
	fallback := t.TempDir()
	dir, remaining, err := productionDataDirFromArgs([]string{"--data-dir", selected}, fallback)
	if err != nil || dir != selected || len(remaining) != 0 {
		t.Fatalf("main-window data dir parse failed: dir=%q remaining=%v err=%v", dir, remaining, err)
	}
	if _, _, err := productionDataDirFromArgs([]string{"--data-dir", selected, "--data-dir", fallback}, fallback); err == nil {
		t.Fatal("duplicate main-window data directory was accepted")
	}
}

func stateHasRequestNamed(state AppState, name string) bool {
	for _, workspace := range state.Workspaces {
		for _, collection := range workspace.Collections {
			for _, request := range collection.Items {
				if strings.EqualFold(request.Name, name) {
					return true
				}
			}
		}
	}
	return false
}

func stateHasOpenTabForRequest(state AppState, requestID string) bool {
	for _, tab := range state.OpenTabs {
		if tab.ItemID == requestID {
			return true
		}
	}
	return false
}
