package responsestore

import (
	"bytes"
	"crypto/rand"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// The store's own tests: round trip, restart durability, deduplication, and the
// LRU that bounds MEMORY without ever bounding durability. They live here
// rather than in the application package because they set the eviction budget
// and read resident bytes directly — white-box knowledge of the store, which is
// exactly the kind of test that should sit beside the code it knows about.

func newTestResponseStore(t *testing.T) (*Store, string) {
	t.Helper()
	dir := t.TempDir()
	store, err := New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return store, dir
}

func TestResponseStoreRoundTrip(t *testing.T) {
	store, _ := newTestResponseStore(t)
	body := []byte(`{"hello":"world"}`)

	handle, err := store.Put(body)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, err := store.Get(handle)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !bytes.Equal(got, body) {
		t.Errorf("round trip changed the body: got %q want %q", got, body)
	}
}

// TestResponseStoreSurvivesRestart is the point of the whole design. A store
// backed only by memory passes every other test in this file.
func TestResponseStoreSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	first, err := New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	body := []byte("body that must outlive the process")
	handle, err := first.Put(body)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	// A second store over the same directory stands in for a restart: it shares
	// no memory with the first.
	second, err := New(dir)
	if err != nil {
		t.Fatalf("second New: %v", err)
	}
	got, err := second.Get(handle)
	if err != nil {
		t.Fatalf("Get after restart: %v", err)
	}
	if !bytes.Equal(got, body) {
		t.Errorf("body did not survive a restart: got %q", got)
	}
}

// TestResponseStoreEvictsFromMemoryButNotDisk pins the distinction the design
// rests on: the LRU bounds memory, never durability.
func TestResponseStoreEvictsFromMemoryButNotDisk(t *testing.T) {
	store, _ := newTestResponseStore(t)
	store.budget = 4096 // small enough to force eviction with modest bodies

	first := bytes.Repeat([]byte("a"), 3000)
	firstHandle, err := store.Put(first)
	if err != nil {
		t.Fatalf("Put first: %v", err)
	}
	// Push the first entry out.
	for i := range 4 {
		filler := bytes.Repeat([]byte{byte('b' + i)}, 3000)
		if _, err := store.Put(filler); err != nil {
			t.Fatalf("Put filler %d: %v", i, err)
		}
	}

	if store.residentBytes() > store.budget {
		t.Errorf("resident bytes %d exceeds budget %d after eviction", store.residentBytes(), store.budget)
	}
	store.mu.Lock()
	_, stillResident := store.entries[firstHandle]
	store.mu.Unlock()
	if stillResident {
		t.Fatalf("the oldest body was not evicted; the test cannot prove the disk path")
	}

	// Evicted from memory, still readable — which is the whole claim.
	got, err := store.Get(firstHandle)
	if err != nil {
		t.Fatalf("Get after eviction: %v", err)
	}
	if !bytes.Equal(got, first) {
		t.Error("an evicted body did not read back correctly from disk")
	}
}

// TestResponseStoreDeduplicatesIdenticalBodies. Handles are content hashes, so
// re-running a request that returns the same payload must not cost a second
// copy on disk.
func TestResponseStoreDeduplicatesIdenticalBodies(t *testing.T) {
	store, dir := newTestResponseStore(t)
	body := []byte("identical payload")

	a, err := store.Put(body)
	if err != nil {
		t.Fatalf("Put a: %v", err)
	}
	b, err := store.Put(append([]byte{}, body...))
	if err != nil {
		t.Fatalf("Put b: %v", err)
	}
	if a != b {
		t.Errorf("identical bodies produced different handles: %s vs %s", a, b)
	}

	entries, err := os.ReadDir(filepath.Join(dir, "responses"))
	if err != nil {
		t.Fatalf("read spill dir: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("identical bodies wrote %d files, want 1", len(entries))
	}
}

func TestResponseStoreDistinguishesDifferentBodies(t *testing.T) {
	// The other half: dedup must not collapse genuinely different responses,
	// which would serve a user one request's body under another's.
	store, _ := newTestResponseStore(t)
	a, err := store.Put([]byte("first"))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	b, err := store.Put([]byte("second"))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if a == b {
		t.Fatal("different bodies produced the same handle")
	}
	got, err := store.Get(a)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(got) != "first" {
		t.Errorf("handle a returned %q, want %q", got, "first")
	}
}

func TestResponseStoreHandlesEmptyAndLargeBodies(t *testing.T) {
	store, _ := newTestResponseStore(t)

	empty, err := store.Put(nil)
	if err != nil {
		t.Fatalf("Put empty: %v", err)
	}
	got, err := store.Get(empty)
	if err != nil {
		t.Fatalf("Get empty: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("empty body read back as %d bytes", len(got))
	}

	// A body larger than the whole budget must still be retrievable — the
	// eviction loop deliberately keeps the last entry rather than evicting
	// something it would immediately have to re-read.
	store.budget = 1024
	large := make([]byte, 64*1024)
	if _, err := rand.Read(large); err != nil {
		t.Fatalf("rand: %v", err)
	}
	handle, err := store.Put(large)
	if err != nil {
		t.Fatalf("Put large: %v", err)
	}
	back, err := store.Get(handle)
	if err != nil {
		t.Fatalf("Get large: %v", err)
	}
	if !bytes.Equal(back, large) {
		t.Error("a body larger than the memory budget did not round trip")
	}
}

// TestResponseStoreReportsMissingHandles. A handle with no file is a real
// condition — a state.json referencing a body whose spill file was deleted —
// and it must be an error rather than an empty body silently shown to a user.
func TestResponseStoreReportsMissingHandles(t *testing.T) {
	store, _ := newTestResponseStore(t)
	_, err := store.Get(Handle("0000000000000000000000000000000000000000000000000000000000000000"))
	if err == nil {
		t.Fatal("Get on an unknown handle returned no error")
	}
	if !strings.Contains(err.Error(), "read response body") {
		t.Errorf("error does not identify the operation: %v", err)
	}
}

// TestResponseStoreIsConcurrencySafe. The store will be reached from the
// request path and the persist path at once, so `go test -race` needs an
// interleaving to inspect.
func TestResponseStoreIsConcurrencySafe(t *testing.T) {
	store, _ := newTestResponseStore(t)
	bodies := make([][]byte, 8)
	handles := make([]Handle, 8)
	for i := range bodies {
		bodies[i] = bytes.Repeat([]byte{byte('a' + i)}, 1024)
		h, err := store.Put(bodies[i])
		if err != nil {
			t.Fatalf("Put %d: %v", i, err)
		}
		handles[i] = h
	}

	runConcurrently(t, 30*time.Second,
		func(i int) { _, _ = store.Get(handles[i%len(handles)]) },
		func(i int) { _, _ = store.Get(handles[(i+3)%len(handles)]) },
		func(i int) { _, _ = store.Put(bytes.Repeat([]byte("z"), 512+i)) },
		func(int) { _ = store.residentBytes() },
	)

	// Everything stored up front must still read back correctly.
	for i, h := range handles {
		got, err := store.Get(h)
		if err != nil {
			t.Fatalf("Get %d after concurrency: %v", i, err)
		}
		if !bytes.Equal(got, bodies[i]) {
			t.Errorf("body %d was corrupted by concurrent access", i)
		}
	}
}

// TestAppResponseStoreIsLazyAndRootedInDataDir covers the App wiring.
//
// Laziness is the point: newAppBase runs for every test App and for each
// multi-window runtime, and creating <dataDir>/responses/ eagerly would leave
// an empty directory behind for every App that never stores a response.

// runConcurrently is duplicated from the core test package. Test helpers do not
// cross package boundaries without being exported into the production API, and
// a twenty-line goroutine harness is not worth that.
func runConcurrently(t *testing.T, timeout time.Duration, work ...func(iteration int)) {
	t.Helper()
	const iterations = 40
	var wg sync.WaitGroup
	start := make(chan struct{})
	for _, fn := range work {
		wg.Add(1)
		go func(fn func(int)) {
			defer wg.Done()
			<-start
			for i := range iterations {
				fn(i)
			}
		}(fn)
	}
	close(start)
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(timeout):
		t.Fatalf("concurrent workers did not finish within %s — suspect a lock-ordering deadlock", timeout)
	}
}

// TestConcurrentReadPathsAgainstWriters hammers every call site converted to
// RLock while writers mutate exactly the state those call sites read:
// Preferences (proxy prefs, SSL session cache, OAuth2 browser choice, last
// import directory) and per-collection proxy / client-certificate config.
//
// Under -race, a write performed beneath RLock by any of the read paths is
// reported as a data race against either a concurrent reader or a concurrent
// writer. Without the writers this test would still pass with a broken
// conversion, because concurrent identical writes to the same word are only
// reported when the detector sees at least one of them.
