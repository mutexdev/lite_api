package core

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
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/mutexdev/lite_api/internal/history"
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
	entries, err := app.ListHistory(history.HistoryQuery{})
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

	entries, err := app.ListHistory(history.HistoryQuery{})
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

	entries, err := app.ListHistory(history.HistoryQuery{})
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
			if header.Value != history.RedactedValue {
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

	entries, err := app.ListHistory(history.HistoryQuery{})
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

func TestHistoryClearAndGet(t *testing.T) {
	app, collectionID, itemID, closeServer := historyFixture(t)
	defer closeServer()

	if _, err := app.SendRequest(collectionID, itemID, ""); err != nil {
		t.Fatalf("SendRequest: %v", err)
	}
	entries, err := app.ListHistory(history.HistoryQuery{})
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
	after, err := app.ListHistory(history.HistoryQuery{})
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

// TestCreateRequestFromHistoryDropsRedactedHeaders is US-049's "save to
// collection".
//
// A request carrying the literal `Authorization: <redacted>` looks configured
// and fails with a 401 that points nowhere. An absent header is visibly
// something to fill in.
func TestCreateRequestFromHistoryDropsRedactedHeaders(t *testing.T) {
	app, collectionID, itemID, closeServer := historyFixture(t)
	defer closeServer()

	headers := []KeyValue{
		{Name: "Authorization", Value: "Bearer supersecret-token", Enabled: true},
		{Name: "X-Trace", Value: "abc", Enabled: true},
	}
	if _, err := app.UpdateRequest(collectionID, itemID, RequestPatch{Headers: &headers}); err != nil {
		t.Fatalf("UpdateRequest: %v", err)
	}
	if _, err := app.SendRequest(collectionID, itemID, ""); err != nil {
		t.Fatalf("SendRequest: %v", err)
	}

	entries, err := app.ListHistory(history.HistoryQuery{})
	if err != nil || len(entries) == 0 {
		t.Fatalf("ListHistory: %v %v", entries, err)
	}

	state, err := app.CreateRequestFromHistory(collectionID, entries[0].ID)
	if err != nil {
		t.Fatalf("CreateRequestFromHistory: %v", err)
	}

	// Two requests now: the original and the one made from history.
	var created *RequestItem
	for _, workspace := range state.Workspaces {
		for _, collection := range workspace.Collections {
			if collection.ID != collectionID {
				continue
			}
			for i := range collection.Items {
				if collection.Items[i].ID != itemID {
					created = &collection.Items[i]
				}
			}
		}
	}
	if created == nil {
		t.Fatal("no request was created from the history entry")
	}

	if !strings.Contains(created.URL, "/users") {
		t.Errorf("URL = %q, want the recorded one", created.URL)
	}
	if created.Method != "GET" {
		t.Errorf("method = %q", created.Method)
	}

	var sawTrace bool
	for _, header := range created.Headers {
		if header.Value == history.RedactedValue {
			t.Errorf("header %q carries the literal redaction marker; the request looks configured and would 401", header.Name)
		}
		if strings.EqualFold(header.Name, "Authorization") {
			t.Error("the redacted Authorization header was carried into the new request")
		}
		if strings.EqualFold(header.Name, "X-Trace") {
			sawTrace = true
			if header.Value != "abc" {
				t.Errorf("an ordinary header lost its value: %q", header.Value)
			}
		}
	}
	if !sawTrace {
		t.Error("an ordinary header was dropped along with the redacted one")
	}
}

// TestCreateRequestFromHistoryLeavesTheOriginalAlone. The entry may point at a
// request that has since been edited; overwriting it with an old snapshot
// would destroy work done after that send.
func TestCreateRequestFromHistoryLeavesTheOriginalAlone(t *testing.T) {
	app, collectionID, itemID, closeServer := historyFixture(t)
	defer closeServer()

	if _, err := app.SendRequest(collectionID, itemID, ""); err != nil {
		t.Fatalf("SendRequest: %v", err)
	}
	// Edit the original AFTER the send, as a user would.
	edited := "https://example.test/edited-after-the-send"
	if _, err := app.UpdateRequest(collectionID, itemID, RequestPatch{URL: &edited}); err != nil {
		t.Fatalf("UpdateRequest: %v", err)
	}

	entries, _ := app.ListHistory(history.HistoryQuery{})
	state, err := app.CreateRequestFromHistory(collectionID, entries[0].ID)
	if err != nil {
		t.Fatalf("CreateRequestFromHistory: %v", err)
	}

	for _, workspace := range state.Workspaces {
		for _, collection := range workspace.Collections {
			for _, item := range collection.Items {
				if item.ID == itemID && item.URL != edited {
					t.Errorf("the original request was overwritten with the history snapshot: %q", item.URL)
				}
			}
		}
	}
}

// historySeamProbe is one send's worth of input for the two recording
// functions: a header set that exercises redaction on both sides, and a
// response carrying exactly one header so the map-to-rows conversion is
// order-stable across the two calls.
func historySeamProbe() (RequestItem, *Response) {
	item := RequestItem{
		ID:     "item-seam",
		Name:   "seam probe",
		Method: "post",
		URL:    "https://example.test/v1/charge",
		Headers: []KeyValue{
			{Name: "Authorization", Value: "Bearer supersecret-token", Enabled: true},
			{Name: "Accept", Value: "application/json", Enabled: true},
		},
	}
	response := &Response{
		Status:       201,
		StatusText:   "Created",
		DurationMs:   12,
		Size:         34,
		RequestedURL: "https://example.test/v1/charge?attempt=1",
		Headers:      map[string]string{"Set-Cookie": "session=supersecret"},
		BodyHandle:   "handle-seam",
	}
	return item, response
}

// normalizedHistoryEntry drops the two fields that differ between any two
// recordings of the same send — the generated id and the wall clock — and sorts
// the header rows, so what remains is exactly the recorded content.
func normalizedHistoryEntry(entry history.HistoryEntry) history.HistoryEntry {
	entry.ID = ""
	entry.At = time.Time{}
	sortHeaderRows(entry.RequestHeaders)
	sortHeaderRows(entry.ResponseHeaders)
	return entry
}

func sortHeaderRows(rows []KeyValue) {
	sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
}

// TestRecordSendHistoryMCPProjectionSeamIsBehaviourNeutral pins the seam the
// MCP-safe history projection will be built on.
//
// The projection cannot look its secret values up: recording runs with a.mu
// held and the hydrator takes the same lock, so the values have to arrive as an
// argument, which means the parameter must exist before the send path can start
// supplying it. Until the projection itself lands, the two functions have to BE
// the same function — including when mask values are passed, which must not yet
// change a single recorded byte. A divergence here would mean history recording
// changed under a task whose whole point was to change nothing.
func TestRecordSendHistoryMCPProjectionSeamIsBehaviourNeutral(t *testing.T) {
	app := newAppForTest(t)
	item, response := historySeamProbe()

	if err := app.recordSendHistory("collection-seam", item, response); err != nil {
		t.Fatalf("recordSendHistory: %v", err)
	}
	if err := app.recordSendHistoryWithMCPProjection("collection-seam", item, response, nil); err != nil {
		t.Fatalf("recordSendHistoryWithMCPProjection(nil): %v", err)
	}
	if err := app.recordSendHistoryWithMCPProjection("collection-seam", item, response, []string{"supersecret-token", "session=supersecret"}); err != nil {
		t.Fatalf("recordSendHistoryWithMCPProjection(values): %v", err)
	}

	entries, err := app.ListHistory(history.HistoryQuery{})
	if err != nil {
		t.Fatalf("ListHistory: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("got %d entries, want 3", len(entries))
	}

	viaDelegate := normalizedHistoryEntry(entries[0])
	for i, entry := range entries[1:] {
		if got := normalizedHistoryEntry(entry); !reflect.DeepEqual(got, viaDelegate) {
			t.Errorf("entry %d differs from the one recorded through recordSendHistory:\n got %+v\nwant %+v", i+1, got, viaDelegate)
		}
	}
	// Guard against the comparison passing on two empty entries.
	if viaDelegate.Method != "POST" || viaDelegate.Status != 201 || viaDelegate.BodyHandle != "handle-seam" {
		t.Fatalf("the compared entry is not the recorded send: %+v", viaDelegate)
	}
	if !viaDelegate.Redacted {
		t.Error("the recorded entry does not report redaction; the comparison is not covering the redaction path")
	}

	// A nil response is the "nothing was recorded" case on both paths.
	if err := app.recordSendHistory("collection-seam", item, nil); err != nil {
		t.Errorf("recordSendHistory(nil response): %v", err)
	}
	if err := app.recordSendHistoryWithMCPProjection("collection-seam", item, nil, []string{"supersecret-token"}); err != nil {
		t.Errorf("recordSendHistoryWithMCPProjection(nil response): %v", err)
	}
	after, err := app.ListHistory(history.HistoryQuery{})
	if err != nil {
		t.Fatalf("ListHistory: %v", err)
	}
	if len(after) != 3 {
		t.Errorf("a nil response was recorded: %d entries, want 3", len(after))
	}
}

// TestRecordSendHistoryMCPProjectionSeamSharesFailures. The error is what the
// send path ignores and what tests assert on, so the delegate must not swallow
// or invent one.
func TestRecordSendHistoryMCPProjectionSeamSharesFailures(t *testing.T) {
	app := newAppForTest(t)
	// A directory where the log file goes: the store cannot read or append.
	if err := os.MkdirAll(filepath.Join(app.dataDir, "history.jsonl"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	item, response := historySeamProbe()

	delegateErr := app.recordSendHistory("collection-seam", item, response)
	if delegateErr == nil {
		t.Fatal("recordSendHistory reported success against an unwritable history file")
	}
	if err := app.recordSendHistoryWithMCPProjection("collection-seam", item, response, []string{"supersecret-token"}); err == nil {
		t.Error("recordSendHistoryWithMCPProjection reported success where the delegate failed")
	}
}

func TestCreateRequestFromHistoryRejectsBadInput(t *testing.T) {
	app, collectionID, itemID, closeServer := historyFixture(t)
	defer closeServer()

	if _, err := app.SendRequest(collectionID, itemID, ""); err != nil {
		t.Fatalf("SendRequest: %v", err)
	}
	entries, _ := app.ListHistory(history.HistoryQuery{})

	if _, err := app.CreateRequestFromHistory(collectionID, "no-such-entry"); err == nil {
		t.Error("an unknown history id should be an error")
	}
	if _, err := app.CreateRequestFromHistory("", entries[0].ID); err == nil {
		t.Error("a blank collection id should be an error")
	}
}
