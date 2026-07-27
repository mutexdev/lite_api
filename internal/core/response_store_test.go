package core

// US-009, step 1 — tests for the response body store.
//
// The property that matters most is DURABILITY, not speed. A cached response
// outlives the process that fetched it, so a store that merely caches would
// silently lose bodies across a restart — and the loss would look like a
// perfectly ordinary empty response body, which is exactly the kind of failure
// nobody reports as a bug.

import (
	"encoding/base64"
	"encoding/json"
	"github.com/mutexdev/lite_api/internal/responsestore"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"
)

func TestAppResponseStoreIsLazyAndRootedInDataDir(t *testing.T) {
	dir := t.TempDir()
	app := newAppInDirForTest(t, dir)

	responsesDir := filepath.Join(dir, "responses")
	if _, err := os.Stat(responsesDir); !os.IsNotExist(err) {
		t.Errorf("responses/ exists before any body was stored (err=%v)", err)
	}

	store, err := app.responseStore()
	if err != nil {
		t.Fatalf("responsestore.Store: %v", err)
	}
	if _, err := os.Stat(responsesDir); err != nil {
		t.Errorf("responses/ was not created on first use: %v", err)
	}

	// The same store must come back: two stores over one directory would each
	// keep their own memory front, doubling residency for no benefit.
	again, err := app.responseStore()
	if err != nil {
		t.Fatalf("second responsestore.Store: %v", err)
	}
	if store != again {
		t.Error("responsestore.Store returned a different instance on the second call")
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
	seen := map[*responsestore.Store]struct{}{}

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
		t.Fatalf("responsestore.Store: %v", err)
	}
	stored, err := store.Get(responsestore.Handle(response.BodyHandle))
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

// TestMigrateResponseBodiesBackfillsAPreMigrationStateFile is the step-3
// acceptance: a state.json written before US-009 — bodies inline, no handles —
// must load, keep every body intact, and come back with handles attached.
//
// The fixture is built by hand rather than by running the current code, because
// the whole question is whether the OLD shape still loads. Generating it from
// today's structs would test nothing.
func TestMigrateResponseBodiesBackfillsAPreMigrationStateFile(t *testing.T) {
	dir := t.TempDir()

	// Seed a real workspace, then rewrite its state.json into the pre-migration
	// shape: a response with body/bodyBase64 and no bodyHandle/bodyHead.
	seed := newAppInDirForTest(t, dir)
	state, err := seed.GetState()
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	collectionID := state.Workspaces[0].Collections[0].ID
	created, err := seed.CreateRequest(collectionID, "http", "cached response")
	if err != nil {
		t.Fatalf("CreateRequest: %v", err)
	}
	var itemID string
	for _, c := range created.Workspaces[0].Collections {
		for _, item := range c.Items {
			if item.Name == "cached response" {
				itemID = item.ID
			}
		}
	}
	if itemID == "" {
		t.Fatal("could not find the seeded request")
	}
	if err := seed.flushPersist(); err != nil {
		t.Fatalf("flushPersist: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatalf("read state.json: %v", err)
	}
	var onDisk map[string]any
	if err := json.Unmarshal(raw, &onDisk); err != nil {
		t.Fatalf("parse state.json: %v", err)
	}
	const legacyBody = `{"legacy":"body stored before US-009"}`
	workspaces := onDisk["workspaces"].([]any)
	collections := workspaces[0].(map[string]any)["collections"].([]any)
	for _, c := range collections {
		cm := c.(map[string]any)
		if cm["id"] != collectionID {
			continue
		}
		for _, it := range cm["items"].([]any) {
			im := it.(map[string]any)
			if im["id"] != itemID {
				continue
			}
			im["response"] = map[string]any{
				"status": 200, "statusText": "OK",
				"body":       legacyBody,
				"bodyBase64": base64.StdEncoding.EncodeToString([]byte(legacyBody)),
				"size":       len(legacyBody),
				// deliberately NO bodyHandle / bodyHead
			}
		}
	}
	patched, err := json.Marshal(onDisk)
	if err != nil {
		t.Fatalf("marshal patched state: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "state.json"), patched, 0o600); err != nil {
		t.Fatalf("write patched state.json: %v", err)
	}

	// Load it the way a restart would.
	reopened := newAppInDirForTest(t, dir)
	loaded, err := reopened.GetState()
	if err != nil {
		t.Fatalf("GetState after reload: %v", err)
	}
	item, ok := findItemInState(loaded, collectionID, itemID)
	if !ok || item.Response == nil {
		t.Fatal("the pre-migration response did not survive the load")
	}
	if item.Response.Body != legacyBody {
		t.Errorf("body changed across migration: got %q want %q", item.Response.Body, legacyBody)
	}
	if item.Response.BodyHandle == "" {
		t.Fatal("migration did not attach a handle")
	}
	if item.Response.BodyHead != legacyBody {
		t.Errorf("head should hold a short body whole: got %q", item.Response.BodyHead)
	}

	store, err := reopened.responseStore()
	if err != nil {
		t.Fatalf("responsestore.Store: %v", err)
	}
	stored, err := store.Get(responsestore.Handle(item.Response.BodyHandle))
	if err != nil {
		t.Fatalf("Get migrated body: %v", err)
	}
	if string(stored) != legacyBody {
		t.Errorf("stored body differs from the original: %q", stored)
	}
}

// TestMigrateResponseBodiesDoesNotFailLoadWhenTheStoreIsUnwritable pins the
// best-effort contract. At this step Body is still authoritative, so an
// unwritable store must degrade to "no handles" rather than "workspace will not
// open". This stops being acceptable at step 5, when Body is deleted.
func TestMigrateResponseBodiesDoesNotFailLoadWhenTheStoreIsUnwritable(t *testing.T) {
	dir := t.TempDir()
	seed := newAppInDirForTest(t, dir)
	if _, err := seed.GetState(); err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if err := seed.flushPersist(); err != nil {
		t.Fatalf("flushPersist: %v", err)
	}

	// Occupy responses/ with a regular file so MkdirAll must fail.
	if err := os.WriteFile(filepath.Join(dir, "responses"), []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("block responses dir: %v", err)
	}

	reopened := newAppInDirForTest(t, dir)
	state, err := reopened.GetState()
	if err != nil {
		t.Fatalf("load failed because the response store was unwritable: %v", err)
	}
	if len(state.Workspaces) == 0 {
		t.Error("workspace did not load")
	}
	if _, err := reopened.responseStore(); err == nil {
		t.Error("responsestore.Store should report the unwritable directory")
	}
}
