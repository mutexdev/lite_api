package main

// US-009, step 1 — tests for the response body store.
//
// The property that matters most is DURABILITY, not speed. A cached response
// outlives the process that fetched it, so a store that merely caches would
// silently lose bodies across a restart — and the loss would look like a
// perfectly ordinary empty response body, which is exactly the kind of failure
// nobody reports as a bug.

import (
	"bytes"
	"crypto/rand"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"
)

func newTestResponseStore(t *testing.T) (*responseStore, string) {
	t.Helper()
	dir := t.TempDir()
	store, err := newResponseStore(dir)
	if err != nil {
		t.Fatalf("newResponseStore: %v", err)
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
	first, err := newResponseStore(dir)
	if err != nil {
		t.Fatalf("newResponseStore: %v", err)
	}
	body := []byte("body that must outlive the process")
	handle, err := first.Put(body)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	// A second store over the same directory stands in for a restart: it shares
	// no memory with the first.
	second, err := newResponseStore(dir)
	if err != nil {
		t.Fatalf("second newResponseStore: %v", err)
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
	_, err := store.Get(responseHandle("0000000000000000000000000000000000000000000000000000000000000000"))
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
	handles := make([]responseHandle, 8)
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
func TestAppResponseStoreIsLazyAndRootedInDataDir(t *testing.T) {
	dir := t.TempDir()
	app := newAppInDirForTest(t, dir)

	responsesDir := filepath.Join(dir, "responses")
	if _, err := os.Stat(responsesDir); !os.IsNotExist(err) {
		t.Errorf("responses/ exists before any body was stored (err=%v)", err)
	}

	store, err := app.responseStore()
	if err != nil {
		t.Fatalf("responseStore: %v", err)
	}
	if _, err := os.Stat(responsesDir); err != nil {
		t.Errorf("responses/ was not created on first use: %v", err)
	}

	// The same store must come back: two stores over one directory would each
	// keep their own memory front, doubling residency for no benefit.
	again, err := app.responseStore()
	if err != nil {
		t.Fatalf("second responseStore: %v", err)
	}
	if store != again {
		t.Error("responseStore returned a different instance on the second call")
	}

	handle, err := store.Put([]byte("stored through the app"))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if _, err := os.Stat(filepath.Join(responsesDir, string(handle))); err != nil {
		t.Errorf("body was not written under the app's data dir: %v", err)
	}
}

// TestAppResponseStoreIsConcurrencySafe. The lazy constructor is reached from
// the request path, so two goroutines can race to create it.
func TestAppResponseStoreIsConcurrencySafe(t *testing.T) {
	app := newAppForTest(t)
	var mu sync.Mutex
	seen := map[*responseStore]struct{}{}

	runConcurrently(t, 30*time.Second, func(int) {
		store, err := app.responseStore()
		if err != nil {
			return
		}
		mu.Lock()
		seen[store] = struct{}{}
		mu.Unlock()
	}, func(int) {
		store, err := app.responseStore()
		if err != nil {
			return
		}
		mu.Lock()
		seen[store] = struct{}{}
		mu.Unlock()
	})

	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 1 {
		t.Errorf("concurrent callers got %d distinct stores, want 1", len(seen))
	}
}

// TestAttachResponseBodyIsAdditive — step 2b must change nothing that already
// works. Body and BodyBase64 stay exactly as they were; the new fields are
// alongside them, and nothing reads them yet.
func TestAttachResponseBodyIsAdditive(t *testing.T) {
	app := newAppForTest(t)
	body := `{"result":"ok"}`
	response := &Response{Status: 200, Body: body, BodyBase64: "cHJlc2VydmVk"}

	if err := app.attachResponseBody(response); err != nil {
		t.Fatalf("attachResponseBody: %v", err)
	}
	if response.Body != body {
		t.Errorf("Body was modified: got %q", response.Body)
	}
	if response.BodyBase64 != "cHJlc2VydmVk" {
		t.Errorf("BodyBase64 was modified: got %q", response.BodyBase64)
	}
	if response.BodyHandle == "" {
		t.Fatal("BodyHandle was not set")
	}
	if response.BodyHead != body {
		t.Errorf("BodyHead should hold a short body whole: got %q", response.BodyHead)
	}

	store, err := app.responseStore()
	if err != nil {
		t.Fatalf("responseStore: %v", err)
	}
	stored, err := store.Get(responseHandle(response.BodyHandle))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(stored) != body {
		t.Errorf("stored body differs from Response.Body: %q vs %q", stored, body)
	}
}

func TestAttachResponseBodySkipsEmptyAndRepeatCalls(t *testing.T) {
	app := newAppForTest(t)

	empty := &Response{Status: 204}
	if err := app.attachResponseBody(empty); err != nil {
		t.Fatalf("attachResponseBody(empty): %v", err)
	}
	if empty.BodyHandle != "" {
		t.Error("an empty body should not get a handle")
	}

	// Re-attaching must not re-store: the request path may pass the same
	// response through more than once.
	response := &Response{Body: "payload"}
	if err := app.attachResponseBody(response); err != nil {
		t.Fatalf("first attach: %v", err)
	}
	first := response.BodyHandle
	response.Body = "changed after the fact"
	if err := app.attachResponseBody(response); err != nil {
		t.Fatalf("second attach: %v", err)
	}
	if response.BodyHandle != first {
		t.Error("a response that already has a handle was re-stored")
	}
}

// TestResponseBodyHeadTruncatesOnRuneBoundary. Slicing a byte count out of a
// string can split a multi-byte rune; encoding/json then rewrites the fragment
// as U+FFFD, so a CJK or emoji body would come back visibly corrupted.
func TestResponseBodyHeadTruncatesOnRuneBoundary(t *testing.T) {
	short := "small body"
	if got := responseBodyHead(short); got != short {
		t.Errorf("a short body should be returned whole: got %q", got)
	}

	// Build a body of 3-byte runes so the 8 KiB limit lands mid-rune.
	body := strings.Repeat("世", responseBodyHeadLimit)
	head := responseBodyHead(body)
	if len(head) > responseBodyHeadLimit {
		t.Errorf("head is %d bytes, over the %d limit", len(head), responseBodyHeadLimit)
	}
	if !utf8.ValidString(head) {
		t.Error("head is not valid UTF-8 — a rune was split")
	}
	if !strings.HasPrefix(body, head) {
		t.Error("head is not a prefix of the body")
	}
	// And it must not have thrown away more than one rune's worth.
	if responseBodyHeadLimit-len(head) >= 3 {
		t.Errorf("head lost %d bytes to boundary alignment, expected under 3", responseBodyHeadLimit-len(head))
	}
}
