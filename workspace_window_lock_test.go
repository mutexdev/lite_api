package main

import (
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/mutexdev/lite_api/internal/workspacestate"
)

func TestWorkspaceWindowLockOwnershipStaleAndReplacementSafety(t *testing.T) {
	now := time.Now().UTC()
	alive := map[int]bool{1: true, 2: true}
	s := NewWorkspaceWindowLockStore(t.TempDir())
	s.Now = func() time.Time { return now }
	s.ProcessAlive = func(pid int) bool { return alive[pid] }
	first, err := s.Acquire("/work/a", "one", 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Acquire("/work/a", "two", 2); err == nil {
		t.Fatal("expected exclusion")
	}
	if _, err := s.Heartbeat(WorkspaceWindowOwner{Workspace: first.Workspace, SessionID: "wrong", PID: 1, AcquiredAt: first.AcquiredAt}); err == nil {
		t.Fatal("expected owner check")
	}
	alive[1] = false
	second, err := s.Acquire("/work/a", "two", 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Release(first); err == nil {
		t.Fatal("must not remove replacement")
	}
	if err := s.Release(second); err != nil {
		t.Fatal(err)
	}
}

func TestWorkspaceWindowLockConcurrentAcquireHasOneWinner(t *testing.T) {
	s := NewWorkspaceWindowLockStore(t.TempDir())
	// The contenders use synthetic PIDs; model them as live so the test checks
	// lock exclusion rather than production process-liveness replacement.
	s.ProcessAlive = func(int) bool { return true }
	var wg sync.WaitGroup
	successes := 0
	var mu sync.Mutex
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if _, err := s.Acquire("workspace", "session-"+strconv.Itoa(i), i+1); err == nil {
				mu.Lock()
				successes++
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()
	if successes != 1 {
		t.Fatalf("successes=%d", successes)
	}
}
func TestWorkspaceWindowLockRejectsCorruptTraversalAndPrivateRecord(t *testing.T) {
	s := NewWorkspaceWindowLockStore(t.TempDir())
	if _, err := s.Acquire("../escape", "s", 1); err == nil {
		t.Fatal("expected traversal")
	}
	path, _ := s.lockPath("workspace")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Acquire("workspace", "s", 1); err == nil {
		t.Fatal("expected corrupt")
	}
	_ = os.Remove(path)
	owner, err := s.Acquire("workspace", "s", 1)
	if err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o600 {
		t.Fatal(info.Mode())
	}
	if err := s.Release(owner); err != nil {
		t.Fatal(err)
	}
}

func TestWorkspaceWindowLockAndRegistryCanonicalizeSymlinkAliases(t *testing.T) {
	dir := t.TempDir()
	realRoot := filepath.Join(dir, "real")
	if err := os.Mkdir(realRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	aliasRoot := filepath.Join(dir, "alias")
	if err := os.Symlink(realRoot, aliasRoot); err != nil {
		t.Fatal(err)
	}
	realWorkspace := filepath.Join(realRoot, "new-workspace")
	aliasWorkspace := filepath.Join(aliasRoot, "new-workspace")
	locks := NewWorkspaceWindowLockStore(dir)
	locks.ProcessAlive = func(int) bool { return true }
	owner, err := locks.Acquire(realWorkspace, "real", 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := locks.Acquire(aliasWorkspace, "alias", 2); err == nil {
		t.Fatal("symlink alias acquired a second workspace lock")
	}
	if err := locks.Release(owner); err != nil {
		t.Fatal(err)
	}
	registry := workspacestate.WorkspaceRegistry{Version: 1, Workspaces: []workspacestate.WorkspaceRegistryEntry{{ID: "real", Path: realWorkspace}, {ID: "alias", Path: aliasWorkspace}}}
	if err := registry.Validate(); err == nil {
		t.Fatal("registry accepted duplicate physical workspace paths")
	}
}
