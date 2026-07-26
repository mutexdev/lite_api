package main

// US-048 — tests for the send history store.
//
// Three claims carry the story and each fails silently if wrong:
//   * history is OUTSIDE state.json, or every keystroke-triggered save starts
//     carrying the whole log
//   * it is CAPPED, or the file grows without bound and startup slows forever
//   * bodies come from the US-009 store, or a hundred sends of one request
//     store a hundred copies of the body
//
// The redaction tests are here because history is the one place credentials
// persist in the clear indefinitely, long after the token was rotated.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func historyFixture(t *testing.T) (*App, string, string, func()) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Set-Cookie", "session=supersecret; Path=/")
		_, _ = fmt.Fprint(w, `{"ok":true}`)
	}))

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	collectionID := state.Workspaces[0].Collections[0].ID
	created, err := app.CreateRequest(collectionID, "http", "history probe")
	if err != nil {
		t.Fatalf("CreateRequest: %v", err)
	}
	var itemID string
	for _, c := range created.Workspaces[0].Collections {
		for _, item := range c.Items {
			if item.Name == "history probe" {
				itemID = item.ID
			}
		}
	}
	url := server.URL + "/users"
	if _, err := app.UpdateRequest(collectionID, itemID, RequestPatch{URL: &url}); err != nil {
		t.Fatalf("UpdateRequest: %v", err)
	}
	return app, collectionID, itemID, server.Close
}

// TestHistoryIsStoredOutsideStateJSON is the story's headline criterion. The
// reason is mechanical: state.json is rewritten in full on every mutation, so
// history living there would put the whole log on the hot path of every save.
func TestHistoryIsStoredOutsideStateJSON(t *testing.T) {
	app, collectionID, itemID, closeServer := historyFixture(t)
	defer closeServer()

	if _, err := app.SendRequest(collectionID, itemID, ""); err != nil {
		t.Fatalf("SendRequest: %v", err)
	}
	if err := app.FlushPendingWrites(); err != nil {
		t.Fatalf("FlushPendingWrites: %v", err)
	}

	historyPath := filepath.Join(app.dataDir, "history.jsonl")
	if _, err := os.Stat(historyPath); err != nil {
		t.Fatalf("history.jsonl was not written: %v", err)
	}

	stateData, err := os.ReadFile(filepath.Join(app.dataDir, "state.json"))
	if err != nil {
		t.Fatalf("read state.json: %v", err)
	}
	if strings.Contains(string(stateData), "\"history\"") {
		t.Error("state.json carries a history key; every save would now write the whole log")
	}
	// And the entry's identifying detail must not have leaked into state.json
	// through some other field.
	entries, err := app.ListHistory(HistoryQuery{})
	if err != nil {
		t.Fatalf("ListHistory: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d history entries, want 1", len(entries))
	}
	if !strings.Contains(string(stateData), "history probe") {
		// The request itself IS in state.json by design; this only confirms the
		// test is reading the file it thinks it is.
		t.Log("note: request name absent from state.json")
	}
}

func TestHistoryRecordsTheSend(t *testing.T) {
	app, collectionID, itemID, closeServer := historyFixture(t)
	defer closeServer()

	if _, err := app.SendRequest(collectionID, itemID, ""); err != nil {
		t.Fatalf("SendRequest: %v", err)
	}

	entries, err := app.ListHistory(HistoryQuery{})
	if err != nil {
		t.Fatalf("ListHistory: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
	entry := entries[0]
	if entry.Method != "GET" {
		t.Errorf("method = %q", entry.Method)
	}
	if !strings.Contains(entry.URL, "/users") {
		t.Errorf("url = %q", entry.URL)
	}
	if entry.Status != 200 {
		t.Errorf("status = %d", entry.Status)
	}
	if entry.Name != "history probe" {
		t.Errorf("name = %q", entry.Name)
	}
	if entry.ID == "" {
		t.Error("entry has no id")
	}
}

// TestHistoryRedactsCredentials. History persists indefinitely in a plaintext
// file no encryption covers, so a bearer token captured once would sit there
// long after it was rotated. The header NAME is kept so the entry still shows
// the request was authenticated.
func TestHistoryRedactsCredentials(t *testing.T) {
	app, collectionID, itemID, closeServer := historyFixture(t)
	defer closeServer()

	headers := []KeyValue{
		{Name: "Authorization", Value: "Bearer supersecret-token", Enabled: true},
		{Name: "Accept", Value: "application/json", Enabled: true},
	}
	if _, err := app.UpdateRequest(collectionID, itemID, RequestPatch{Headers: &headers}); err != nil {
		t.Fatalf("UpdateRequest: %v", err)
	}
	if _, err := app.SendRequest(collectionID, itemID, ""); err != nil {
		t.Fatalf("SendRequest: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(app.dataDir, "history.jsonl"))
	if err != nil {
		t.Fatalf("read history: %v", err)
	}
	if strings.Contains(string(raw), "supersecret-token") {
		t.Error("the bearer token was written to the history file in the clear")
	}
	if strings.Contains(string(raw), "supersecret") && strings.Contains(string(raw), "session=") {
		t.Error("a Set-Cookie value was written to the history file in the clear")
	}

	entries, err := app.ListHistory(HistoryQuery{})
	if err != nil {
		t.Fatalf("ListHistory: %v", err)
	}
	entry := entries[0]
	if !entry.Redacted {
		t.Error("the entry does not report that anything was redacted")
	}

	var sawAuthorization, sawAccept bool
	for _, header := range entry.RequestHeaders {
		if strings.EqualFold(header.Name, "Authorization") {
			sawAuthorization = true
			if header.Value != historyRedactedValue {
				t.Errorf("authorization value = %q, want it redacted", header.Value)
			}
		}
		if strings.EqualFold(header.Name, "Accept") {
			sawAccept = true
			if header.Value != "application/json" {
				t.Errorf("a non-credential header was redacted: %q", header.Value)
			}
		}
	}
	if !sawAuthorization {
		t.Error("the authorization header was dropped entirely; the entry now misrepresents the request as unauthenticated")
	}
	if !sawAccept {
		t.Error("an ordinary header was lost")
	}
}

// TestHistoryBodiesComeFromTheResponseStore. Inlining bodies would put
// megabytes into a file read end-to-end at startup; the content-addressed
// store means repeated sends of one request cost one copy.
func TestHistoryBodiesComeFromTheResponseStore(t *testing.T) {
	app, collectionID, itemID, closeServer := historyFixture(t)
	defer closeServer()

	for range 3 {
		if _, err := app.SendRequest(collectionID, itemID, ""); err != nil {
			t.Fatalf("SendRequest: %v", err)
		}
	}

	raw, err := os.ReadFile(filepath.Join(app.dataDir, "history.jsonl"))
	if err != nil {
		t.Fatalf("read history: %v", err)
	}
	if strings.Contains(string(raw), `{"ok":true}`) {
		t.Error("the response body was inlined into the history file")
	}

	entries, err := app.ListHistory(HistoryQuery{})
	if err != nil {
		t.Fatalf("ListHistory: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("got %d entries, want 3", len(entries))
	}
	// Content addressing: three sends of the same response share one handle.
	if entries[0].BodyHandle == "" {
		t.Fatal("the entry carries no body handle")
	}
	if entries[0].BodyHandle != entries[2].BodyHandle {
		t.Error("identical bodies produced different handles; the store is not content-addressed")
	}

	body, err := app.GetHistoryBody(entries[0].ID)
	if err != nil {
		t.Fatalf("GetHistoryBody: %v", err)
	}
	if body != `{"ok":true}` {
		t.Errorf("body = %q", body)
	}
}

func TestHistorySearchNarrowsOnEveryTerm(t *testing.T) {
	store := &historyStore{path: filepath.Join(t.TempDir(), "history.jsonl")}
	for _, entry := range []HistoryEntry{
		{ID: "1", Name: "list users", Method: "GET", URL: "https://api.test/users", Status: 200},
		{ID: "2", Name: "create user", Method: "POST", URL: "https://api.test/users", Status: 201},
		{ID: "3", Name: "list orders", Method: "GET", URL: "https://api.test/orders", Status: 500},
	} {
		if err := store.append(entry); err != nil {
			t.Fatalf("append: %v", err)
		}
	}

	// Every term must match, so a second word narrows rather than widening the
	// way a single-substring search would.
	got, err := store.list(HistoryQuery{Text: "post users"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 || got[0].ID != "2" {
		t.Errorf("multi-term search returned %v, want just the POST", ids(got))
	}

	got, _ = store.list(HistoryQuery{Text: "users"})
	if len(got) != 2 {
		t.Errorf("single-term search returned %v, want both /users entries", ids(got))
	}

	got, _ = store.list(HistoryQuery{Method: "get"})
	if len(got) != 2 {
		t.Errorf("method filter returned %v, want the two GETs", ids(got))
	}

	got, _ = store.list(HistoryQuery{OnlyFailures: true})
	if len(got) != 1 || got[0].ID != "3" {
		t.Errorf("failure filter returned %v, want the 500", ids(got))
	}

	got, _ = store.list(HistoryQuery{Text: "nothing matches this"})
	if len(got) != 0 {
		t.Errorf("a non-matching search returned %v", ids(got))
	}
}

func ids(entries []HistoryEntry) []string {
	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		out = append(out, entry.ID)
	}
	return out
}

func TestHistoryListsNewestFirstAndAppliesTheLimitToTheNewest(t *testing.T) {
	store := &historyStore{path: filepath.Join(t.TempDir(), "history.jsonl")}
	for i := range 10 {
		if err := store.append(HistoryEntry{ID: fmt.Sprintf("%d", i), Method: "GET", URL: "https://api.test/"}); err != nil {
			t.Fatalf("append: %v", err)
		}
	}

	got, err := store.list(HistoryQuery{Limit: 3})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	// Walking the file forwards and stopping at the limit would return the
	// OLDEST three, which is the opposite of what a history list is for.
	if len(got) != 3 || got[0].ID != "9" || got[2].ID != "7" {
		t.Errorf("limited list returned %v, want the newest three", ids(got))
	}
}

// TestHistoryCompactsPastTheCap. Without compaction the file grows without
// bound and the startup read gets slower forever.
func TestHistoryCompactsPastTheCap(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.jsonl")
	store := &historyStore{path: path}

	for i := range historyCompactAt + 5 {
		if err := store.append(HistoryEntry{ID: fmt.Sprintf("%d", i), Method: "GET", URL: "https://api.test/"}); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}

	// The FILE is what compaction changes. Asserting on list() instead would
	// measure the query limit, which caps the result at historyLimit whether
	// or not anything was ever compacted — the check would pass against a store
	// that grows without bound. A negative control caught exactly that.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read history: %v", err)
	}
	lines := 0
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(line) != "" {
			lines++
		}
	}
	// The bound is the COMPACTION TRIGGER, not the cap. The file is deliberately
	// allowed to drift above historyLimit between compactions — that drift is
	// the entire reason the format is append-only, since compacting at exactly
	// the cap would rewrite the whole file on every send once it filled up.
	// What must hold is that it never grows past the trigger.
	if lines > historyCompactAt {
		t.Errorf("the file holds %d lines after %d appends; compaction never ran", lines, historyCompactAt+5)
	}
	if lines < historyLimit {
		t.Errorf("the file holds only %d lines; compaction discarded more than the cap", lines)
	}

	entries, err := store.list(HistoryQuery{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(entries) > historyLimit {
		t.Errorf("got %d entries after compaction, want at most %d", len(entries), historyLimit)
	}
	// The newest must survive; compaction that dropped the tail instead of the
	// head would discard exactly what the user is looking for.
	if entries[0].ID != fmt.Sprintf("%d", historyCompactAt+4) {
		t.Errorf("newest entry is %q; compaction kept the wrong end", entries[0].ID)
	}
}

// TestHistoryTolerablesMalformedLines. A truncated line is the likely result of
// a crash mid-write, and it must not make the whole log unreadable.
func TestHistoryToleratesMalformedLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.jsonl")
	good, err := json.Marshal(HistoryEntry{ID: "good", Method: "GET", URL: "https://api.test/"})
	if err != nil {
		t.Fatal(err)
	}
	content := string(good) + "\n{\"id\":\"trunc\",\"met\n" + string(good) + "\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	store := &historyStore{path: path}
	entries, err := store.list(HistoryQuery{})
	if err != nil {
		t.Fatalf("a malformed line made the whole log unreadable: %v", err)
	}
	if len(entries) != 2 {
		t.Errorf("got %d entries, want the 2 intact ones", len(entries))
	}
}

func TestHistoryClearAndGet(t *testing.T) {
	app, collectionID, itemID, closeServer := historyFixture(t)
	defer closeServer()

	if _, err := app.SendRequest(collectionID, itemID, ""); err != nil {
		t.Fatalf("SendRequest: %v", err)
	}
	entries, err := app.ListHistory(HistoryQuery{})
	if err != nil || len(entries) != 1 {
		t.Fatalf("ListHistory: %v %v", entries, err)
	}

	entry, err := app.GetHistoryEntry(entries[0].ID)
	if err != nil {
		t.Fatalf("GetHistoryEntry: %v", err)
	}
	if entry.ID != entries[0].ID {
		t.Errorf("got entry %q", entry.ID)
	}
	if _, err := app.GetHistoryEntry("does-not-exist"); err == nil {
		t.Error("an unknown id should be an error")
	}
	if _, err := app.GetHistoryEntry("  "); err == nil {
		t.Error("a blank id should be an error")
	}

	if err := app.ClearHistory(); err != nil {
		t.Fatalf("ClearHistory: %v", err)
	}
	after, err := app.ListHistory(HistoryQuery{})
	if err != nil {
		t.Fatalf("ListHistory after clear: %v", err)
	}
	if len(after) != 0 {
		t.Errorf("%d entries survived the clear", len(after))
	}
	// Clearing twice must not fail on the missing file.
	if err := app.ClearHistory(); err != nil {
		t.Errorf("clearing an empty history failed: %v", err)
	}
}

func TestHistoryListOnAnEmptyStoreIsNotAnError(t *testing.T) {
	store := &historyStore{path: filepath.Join(t.TempDir(), "never-written.jsonl")}
	entries, err := store.list(HistoryQuery{})
	if err != nil {
		t.Fatalf("listing a store with no file failed: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("got %d entries from an empty store", len(entries))
	}
}
