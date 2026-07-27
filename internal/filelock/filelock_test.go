package filelock

import (
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// The lock coordinates LiteAPI windows with each other so two of them cannot
// rewrite one workspace's recovery store at once. It is advisory, which is the
// whole requirement — it is not defending against an unrelated process.

func openTemp(t *testing.T) *os.File {
	t.Helper()
	f, err := os.Create(filepath.Join(t.TempDir(), "lock"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}

func TestExclusiveLocksAndReleases(t *testing.T) {
	f := openTemp(t)
	release, err := Exclusive(f)
	if err != nil {
		t.Fatal(err)
	}
	if release == nil {
		t.Fatal("no release was returned")
	}
	release()
}

// The release is safe to call more than once. Callers defer both the release
// and the file close, and the order between them is deliberately not something
// every site has to reason about.
func TestReleaseIsSafeToCallTwice(t *testing.T) {
	f := openTemp(t)
	release, err := Exclusive(f)
	if err != nil {
		t.Fatal(err)
	}
	release()
	release() // must not panic
}

// A release is returned even on failure, so a caller that defers it
// unconditionally — which is the ordinary shape — does not nil-panic on the
// error path.
func TestAReleaseIsReturnedEvenWhenLockingFails(t *testing.T) {
	f := openTemp(t)
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	release, err := Exclusive(f)
	if release == nil {
		t.Fatal("no release was returned on the error path; a deferred call would panic")
	}
	release()
	if err == nil {
		t.Log("locking a closed descriptor did not fail on this platform; the nil check above is the point")
	}
}

// THE PROPERTY THE LOCK EXISTS FOR: while one holder has it, a second attempt
// on a separate descriptor for the same file does not proceed. Separate
// descriptors matter — flock is per open-file-description, so re-locking the
// SAME descriptor would succeed and prove nothing.
func TestASecondHolderWaitsUntilTheFirstReleases(t *testing.T) {
	path := filepath.Join(t.TempDir(), "contended")
	first, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = first.Close() }()
	second, err := os.OpenFile(path, os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = second.Close() }()

	releaseFirst, err := Exclusive(first)
	if err != nil {
		t.Fatal(err)
	}

	var acquired atomic.Bool
	done := make(chan struct{})
	go func() {
		defer close(done)
		release, err := Exclusive(second)
		if err != nil {
			return
		}
		acquired.Store(true)
		release()
	}()

	// The second holder must still be waiting while the first holds the lock.
	time.Sleep(50 * time.Millisecond)
	if acquired.Load() {
		t.Error("a second holder acquired the lock while the first still held it")
	}

	releaseFirst()
	select {
	case <-done:
		if !acquired.Load() {
			t.Error("the second holder never acquired the lock after it was released")
		}
	case <-time.After(5 * time.Second):
		t.Error("the second holder did not acquire the lock within 5s of its release")
	}
}

// Sequential holders on separate descriptors each get the lock in turn, which
// is what the window-coordination case actually looks like.
func TestHoldersTakeTurns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "turns")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	var concurrent, peak atomic.Int32
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			f, err := os.OpenFile(path, os.O_RDWR, 0o600)
			if err != nil {
				return
			}
			defer func() { _ = f.Close() }()
			release, err := Exclusive(f)
			if err != nil {
				return
			}
			defer release()
			now := concurrent.Add(1)
			for {
				high := peak.Load()
				if now <= high || peak.CompareAndSwap(high, now) {
					break
				}
			}
			time.Sleep(10 * time.Millisecond)
			concurrent.Add(-1)
		}()
	}
	wg.Wait()
	if peak.Load() > 1 {
		t.Errorf("%d holders were inside the lock at once", peak.Load())
	}
}
