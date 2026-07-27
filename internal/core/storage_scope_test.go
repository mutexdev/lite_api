package core

import "testing"

func TestWebStorageScopeIsStablePerDataDirectoryAndIsolatedAcrossDirectories(t *testing.T) {
	firstDir := t.TempDir()
	secondDir := t.TempDir()
	first, err := newProductionAppForTest(t, firstDir, nil)
	if err != nil {
		t.Fatal(err)
	}
	firstScope, err := first.GetWebStorageScope()
	if err != nil {
		t.Fatal(err)
	}
	// Mirrors shutdown: release() gives up the ownership lease that
	// persistWorkspaceRuntimeLocked needs, so pending state must land first.
	flushPersistForTest(t, first)
	first.workspaceRuntime.release()
	reloaded, err := newProductionAppForTest(t, firstDir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer reloaded.workspaceRuntime.release()
	reloadedScope, err := reloaded.GetWebStorageScope()
	if err != nil {
		t.Fatal(err)
	}
	if firstScope == "" || firstScope != reloadedScope {
		t.Fatalf("scope was not stable across same-dir relaunch: first=%q reloaded=%q", firstScope, reloadedScope)
	}

	second, err := newProductionAppForTest(t, secondDir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer second.workspaceRuntime.release()
	secondScope, err := second.GetWebStorageScope()
	if err != nil {
		t.Fatal(err)
	}
	if firstScope == secondScope {
		t.Fatalf("distinct data directories shared browser storage namespace %q", firstScope)
	}
}
