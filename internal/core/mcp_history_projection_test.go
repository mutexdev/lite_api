package core

// Phase 6 §7 — the record-time MCP-safe history projection.
//
// THE BUG THESE TESTS EXIST FOR. History is recorded after interpolation, so
// the resolved credential is sitting in the recorded URL, header and body.
// get_history used to compensate by masking against the secret values the
// process holds AT READ TIME — which works right up until the user rotates the
// variable. At that instant the old value stops being in the mask set and the
// recorded copy of it becomes readable to any agent that asks. Deleting the
// variable does the same thing, harder.
//
// The fix is to mask at RECORD time, when the values that were live for that
// send are still known, and to persist only the masked result. So the tests
// here are all variations on one question: after the state has moved on, can
// the old value still be got out?
//
// Every test carries its negative control. The recorded history entry is
// asserted to CONTAIN the plaintext (it does — the app's own history is a local
// view for the user who owns the credentials, and §7 leaves it alone), so a
// test that passed because nothing was ever recorded would be caught.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mutexdev/lite_api/internal/history"
	"github.com/mutexdev/lite_api/internal/mcpserver"
)

const (
	// Both are comfortably over MaskKnownSecretValues' 8-byte floor; a shorter
	// sentinel would be skipped by the masker and the test would measure the
	// floor rather than the projection.
	projectionOldSecret = "ROTATED-OLD-SECRET-0001"
	projectionNewSecret = "ROTATED-NEW-SECRET-0002"
	// The environment the fixture's request resolves under.
	projectionEnvironmentID = "env-projection"
)

// seedHistoryProjection records an entry together with the projection the
// production builder would have written for it.
//
// Shared with newMCPFixture and the e2e fixture, which both plant a history
// entry with a fixed id rather than performing a send. Once get_history serves
// only the projection, a fixture that appends a bare entry is measuring the
// "recorded before agent-safe history" placeholder instead of the redaction it
// meant to exercise — and it goes through mcpHistoryProjectionLocked rather
// than a hand-built artifact so the fixtures cannot drift from production.
func seedHistoryProjection(t *testing.T, app *App, entry history.HistoryEntry, body string) {
	t.Helper()
	response := &Response{
		Status:     entry.Status,
		Body:       body,
		BodyHandle: entry.BodyHandle,
	}
	app.mu.Lock()
	projection := app.mcpHistoryProjectionLocked(entry, response, nil)
	app.mu.Unlock()
	if err := app.history().AppendWithMCPProjection(entry, &projection); err != nil {
		t.Fatalf("append history with projection: %v", err)
	}
}

// projectionFixture is a collection holding one secret environment variable and
// one request that carries it in all three post-interpolation places: the query
// string, a request header, and the body.
//
// The query parameter is called "trace" and the header "X-Trace" DELIBERATELY.
// Neither name is credential-shaped, so mcpserver's name heuristics cannot save
// them — only exact-value masking can, which is the mechanism under test. The
// echo server reflects the header back into the response body so the response
// side carries the value too.
type projectionFixture struct {
	app          *App
	backend      *mcpBackend
	collectionID string
	requestID    string
	serverURL    string
}

func newProjectionFixture(t *testing.T) projectionFixture {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Echoed-Trace", r.Header.Get("X-Trace"))
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, `{"ok":true,"echo":%q}`, r.Header.Get("X-Trace"))
	}))
	t.Cleanup(server.Close)

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	collectionID := state.Workspaces[0].Collections[0].ID
	created, err := app.CreateRequest(collectionID, "http", "projection probe")
	if err != nil {
		t.Fatalf("CreateRequest: %v", err)
	}
	var requestID string
	for _, collection := range created.Workspaces[0].Collections {
		for _, item := range collection.Items {
			if item.Name == "projection probe" {
				requestID = item.ID
			}
		}
	}
	if requestID == "" {
		t.Fatal("the probe request was not created")
	}

	method := http.MethodPost
	url := server.URL + "/things?trace={{apiToken}}"
	headers := []KeyValue{{Name: "X-Trace", Value: "{{apiToken}}", Enabled: true}}
	body := RequestBody{Mode: "json", JSON: `{"token":"{{apiToken}}"}`}
	if _, err := app.UpdateRequest(collectionID, requestID, RequestPatch{
		Method:  &method,
		URL:     &url,
		Headers: &headers,
		Body:    &body,
	}); err != nil {
		t.Fatalf("UpdateRequest: %v", err)
	}

	// Planted straight into state, hydrated, exactly as
	// hydrateStateEnvironmentSecretsLocked leaves a decrypted secret at runtime
	// — the same approach newMCPFixture takes, and the only one that lets the
	// rotation below be a single-field edit.
	setProjectionSecret(t, app, collectionID, projectionOldSecret)

	return projectionFixture{
		app:          app,
		backend:      &mcpBackend{app: app},
		collectionID: collectionID,
		requestID:    requestID,
		serverURL:    server.URL,
	}
}

// setProjectionSecret plants or rotates the secret variable. An empty value
// DELETES it, which is the harsher half of the rotation case: a rotated value
// at least leaves a variable behind, while a deleted one leaves the read-time
// masker with nothing at all to work from.
func setProjectionSecret(t *testing.T, app *App, collectionID, value string) {
	t.Helper()
	app.mu.Lock()
	defer app.mu.Unlock()
	for wi := range app.state.Workspaces {
		for ci := range app.state.Workspaces[wi].Collections {
			collection := &app.state.Workspaces[wi].Collections[ci]
			if collection.ID != collectionID {
				continue
			}
			if value == "" {
				collection.Environments = nil
				return
			}
			collection.Environments = []Environment{{
				ID:   projectionEnvironmentID,
				Name: "Projection",
				Variables: []Variable{
					{ID: "projection-secret", Name: "apiToken", Value: value, Enabled: true, Secret: true},
				},
			}}
			return
		}
	}
	t.Fatalf("collection %q not found", collectionID)
}

// projectionArtifactPath is where §7 says the artifact lives: a sibling of the
// history log, one file per entry id.
func projectionArtifactPath(app *App, entryID string) string {
	return filepath.Join(app.dataDir, history.MCPProjectionDir, entryID)
}

func marshalHistoryRuns(t *testing.T, runs []mcpserver.HistoryRun) string {
	t.Helper()
	encoded, err := json.Marshal(runs)
	if err != nil {
		t.Fatalf("marshal history runs: %v", err)
	}
	return string(encoded)
}

// TestMCPGetHistoryRotatedSecretNotResurfaced is the test the whole task exists
// for.
//
// One send under a live secret, then the two ways the state can move on —
// rotated to a different value, and deleted outright — and after each of them
// the old value must not come back out of get_history. It cannot, because the
// only thing get_history reads is an artifact that was masked while the value
// was still known.
func TestMCPGetHistoryRotatedSecretNotResurfaced(t *testing.T) {
	fixture := newProjectionFixture(t)

	if _, err := fixture.app.SendRequest(fixture.collectionID, fixture.requestID, projectionEnvironmentID); err != nil {
		t.Fatalf("SendRequest: %v", err)
	}

	entries, err := fixture.app.ListHistory(history.HistoryQuery{})
	if err != nil {
		t.Fatalf("ListHistory: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d history entries, want 1", len(entries))
	}
	entry := entries[0]

	// NEGATIVE CONTROL, and the reason the projection has to exist at all: the
	// app's own history entry really does hold the resolved credential. §7
	// leaves it that way on purpose — it is the user's local record — so every
	// assertion below is about the agent-facing copy, not about history.
	if !strings.Contains(entry.URL, projectionOldSecret) {
		t.Fatalf("the recorded history URL does not carry the resolved secret (%q); the fixture is not exercising the leak", entry.URL)
	}

	// Before anything is rotated, the projection is already masked. Read-time
	// masking would produce the same answer here, which is exactly why this
	// step cannot be the test on its own.
	runs, err := fixture.backend.GetHistory(fixture.collectionID, fixture.requestID, 0)
	if err != nil {
		t.Fatalf("GetHistory: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("got %d runs, want 1", len(runs))
	}
	live := marshalHistoryRuns(t, runs)
	if strings.Contains(live, projectionOldSecret) {
		t.Errorf("get_history leaked the live secret:\n%s", live)
	}
	// POSITIVE CONTROL. Masking everything, or returning nothing, would satisfy
	// every "does not contain" assertion in this file.
	if !strings.Contains(runs[0].URL, mcpserver.MaskedValue) {
		t.Errorf("the projected URL carries no mask marker, so nothing was masked out of it: %q", runs[0].URL)
	}
	if !strings.Contains(runs[0].Body, mcpserver.MaskedValue) {
		t.Errorf("the projected body carries no mask marker: %q", runs[0].Body)
	}
	if !strings.Contains(runs[0].Body, "\"ok\":true") {
		t.Errorf("the projected body is not the response body: %q", runs[0].Body)
	}

	// THE ARTIFACT ITSELF. Read off disk, not through any accessor: the file
	// must never have contained the plaintext, because everything else is a
	// promise about who reads it rather than about what is written down.
	artifact, err := os.ReadFile(projectionArtifactPath(fixture.app, entry.ID))
	if err != nil {
		t.Fatalf("read the projection artifact: %v", err)
	}
	if strings.Contains(string(artifact), projectionOldSecret) {
		t.Errorf("the persisted projection contains the plaintext secret:\n%s", artifact)
	}
	// Decoded rather than substring-matched, because encoding/json escapes the
	// marker's angle brackets and a raw Contains would silently never match.
	var stored history.MCPProjection
	if err := json.Unmarshal(artifact, &stored); err != nil {
		t.Fatalf("the persisted projection is not readable JSON: %v", err)
	}
	if !strings.Contains(stored.URL, mcpserver.MaskedValue) || !strings.Contains(stored.Body, mcpserver.MaskedValue) {
		t.Errorf("the persisted projection carries no mask marker, so it is not the redacted copy: url=%q body=%q", stored.URL, stored.Body)
	}
	if stored.Version != history.MCPProjectionVersion || stored.EntryID != entry.ID {
		t.Errorf("the projection is not stamped for its entry: version=%d entryId=%q", stored.Version, stored.EntryID)
	}

	// ROTATION. The old value is no longer anywhere in state, so the read-time
	// masker cannot recognise it. Only the record-time artifact can.
	setProjectionSecret(t, fixture.app, fixture.collectionID, projectionNewSecret)
	rotated, err := fixture.backend.GetHistory(fixture.collectionID, fixture.requestID, 0)
	if err != nil {
		t.Fatalf("GetHistory after rotation: %v", err)
	}
	encoded := marshalHistoryRuns(t, rotated)
	if strings.Contains(encoded, projectionOldSecret) {
		t.Errorf("get_history resurfaced the ROTATED-AWAY secret:\n%s", encoded)
	}
	if len(rotated) != 1 || !strings.Contains(rotated[0].Body, "\"ok\":true") {
		t.Errorf("the run stopped being readable after rotation: %+v", rotated)
	}

	// DELETION. Harsher: there is no secret variable left at all, so the
	// read-time mask set for this collection is empty.
	setProjectionSecret(t, fixture.app, fixture.collectionID, "")
	deleted, err := fixture.backend.GetHistory(fixture.collectionID, fixture.requestID, 0)
	if err != nil {
		t.Fatalf("GetHistory after deletion: %v", err)
	}
	encoded = marshalHistoryRuns(t, deleted)
	if strings.Contains(encoded, projectionOldSecret) {
		t.Errorf("get_history resurfaced the DELETED secret:\n%s", encoded)
	}

	// And the entry underneath still holds it — proving the two really are
	// different artifacts and the assertions above were not passing because
	// something wiped the history file.
	after, err := fixture.app.ListHistory(history.HistoryQuery{})
	if err != nil {
		t.Fatalf("ListHistory after deletion: %v", err)
	}
	if len(after) != 1 || !strings.Contains(after[0].URL, projectionOldSecret) {
		t.Error("the app's own history entry lost the value it is supposed to keep; the comparison above is not measuring two artifacts")
	}
}

// A UI send records the projection too.
//
// get_history serves whatever is in the log, and the log does not record who
// caused a send — so if only MCP-initiated runs were projected, an agent would
// read the placeholder for every request the user sent by hand, or, worse, a
// fallback to the raw entry. The artifact has to exist before an agent ever
// asks, which means at every send.
func TestUISendRecordsMCPHistoryProjection(t *testing.T) {
	fixture := newProjectionFixture(t)

	// SendRequest is the Wails binding the UI calls. No MCP policy is on the
	// context, so the send path passes no mask values at all; the record path
	// is what has to make the artifact safe.
	if _, err := fixture.app.SendRequest(fixture.collectionID, fixture.requestID, projectionEnvironmentID); err != nil {
		t.Fatalf("SendRequest: %v", err)
	}
	entries, err := fixture.app.ListHistory(history.HistoryQuery{})
	if err != nil {
		t.Fatalf("ListHistory: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d history entries, want 1", len(entries))
	}

	path := projectionArtifactPath(fixture.app, entries[0].ID)
	artifact, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("a UI send left no projection at %s: %v", path, err)
	}
	if strings.Contains(string(artifact), projectionOldSecret) {
		t.Errorf("a UI send persisted the plaintext secret into the agent-facing artifact:\n%s", artifact)
	}

	runs, err := fixture.backend.GetHistory(fixture.collectionID, fixture.requestID, 0)
	if err != nil {
		t.Fatalf("GetHistory: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("got %d runs, want 1", len(runs))
	}
	if strings.Contains(runs[0].Body, mcpHistoryUnprojectedBody) {
		t.Error("a UI send's run came back as the pre-upgrade placeholder")
	}
	if !strings.Contains(runs[0].Body, "\"ok\":true") {
		t.Errorf("a UI send's projected body is not the response body: %q", runs[0].Body)
	}
}

// An entry recorded before the projection existed returns the placeholder.
//
// This is the whole shape of the fix. There is no way to make an old entry safe
// — it was written post-interpolation and nobody recorded what was live at the
// time — so the only honest options are "withhold it" and "serve a credential
// that may since have been rotated". A fallback to the raw entry "just for old
// runs" would preserve the exact leak this task closes, and preserve it
// silently.
func TestMCPGetHistoryPreProjectionEntryReturnsPlaceholder(t *testing.T) {
	fixture := newProjectionFixture(t)

	// Appended bare, the way every entry written before this change looks.
	entry := history.HistoryEntry{
		ID:           "history-pre-projection",
		At:           time.Now(),
		CollectionID: fixture.collectionID,
		ItemID:       fixture.requestID,
		Name:         "Legacy run",
		Method:       "POST",
		URL:          "https://api.example.com/things?trace=" + projectionOldSecret,
		Status:       200,
		DurationMs:   9,
		ResponseHeaders: []KeyValue{
			{Name: "X-Echoed-Trace", Value: projectionOldSecret, Enabled: true},
		},
	}
	if err := fixture.app.history().Append(entry); err != nil {
		t.Fatalf("append: %v", err)
	}
	// The secret is gone from state, exactly as it would be for a months-old
	// entry, so read-time masking has nothing to match on.
	setProjectionSecret(t, fixture.app, fixture.collectionID, "")

	runs, err := fixture.backend.GetHistory(fixture.collectionID, fixture.requestID, 0)
	if err != nil {
		t.Fatalf("GetHistory: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("got %d runs, want 1", len(runs))
	}
	run := runs[0]
	encoded := marshalHistoryRuns(t, runs)
	if strings.Contains(encoded, projectionOldSecret) {
		t.Errorf("a pre-upgrade entry was served raw and leaked its recorded secret:\n%s", encoded)
	}
	if run.URL != mcpHistoryUnprojectedURL {
		t.Errorf("pre-upgrade URL is %q, want the placeholder %q", run.URL, mcpHistoryUnprojectedURL)
	}
	if run.Body != mcpHistoryUnprojectedBody {
		t.Errorf("pre-upgrade body is %q, want the placeholder", run.Body)
	}
	if len(run.Headers) != 0 {
		t.Errorf("pre-upgrade headers were served: %+v", run.Headers)
	}
	// The run is still reported as having happened. Withholding the content is
	// not the same as pretending the send never occurred, and an agent that
	// cannot see the entry at all has no way to know why its history is short.
	if run.ID != entry.ID || run.Method != "POST" || run.Status != 200 {
		t.Errorf("the placeholder dropped the non-secret facts about the run: %+v", run)
	}
}

// A corrupted artifact degrades to the placeholder, never to the raw entry.
//
// The failure mode this rules out is the tempting one: "the projection could
// not be read, so fall back to what we have". What we have is the unmasked
// record.
func TestMCPGetHistoryUnreadableProjectionFallsBackToPlaceholder(t *testing.T) {
	fixture := newProjectionFixture(t)
	if _, err := fixture.app.SendRequest(fixture.collectionID, fixture.requestID, projectionEnvironmentID); err != nil {
		t.Fatalf("SendRequest: %v", err)
	}
	entries, err := fixture.app.ListHistory(history.HistoryQuery{})
	if err != nil {
		t.Fatalf("ListHistory: %v", err)
	}
	if err := os.WriteFile(projectionArtifactPath(fixture.app, entries[0].ID), []byte("{not json"), 0o600); err != nil {
		t.Fatalf("corrupt the projection: %v", err)
	}

	runs, err := fixture.backend.GetHistory(fixture.collectionID, fixture.requestID, 0)
	if err != nil {
		t.Fatalf("GetHistory: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("got %d runs, want 1", len(runs))
	}
	if runs[0].URL != mcpHistoryUnprojectedURL {
		t.Errorf("an unreadable projection produced %q rather than the placeholder", runs[0].URL)
	}
	if strings.Contains(marshalHistoryRuns(t, runs), projectionOldSecret) {
		t.Error("an unreadable projection fell back to the raw entry")
	}
}

// ClearHistory clears the projections too.
//
// A user who clears their history to get rid of a recorded run has to actually
// be rid of it. Leaving the agent-facing copies behind would mean the run stays
// readable through MCP after the UI says it is gone.
func TestClearHistoryRemovesMCPProjections(t *testing.T) {
	fixture := newProjectionFixture(t)
	if _, err := fixture.app.SendRequest(fixture.collectionID, fixture.requestID, projectionEnvironmentID); err != nil {
		t.Fatalf("SendRequest: %v", err)
	}
	entries, err := fixture.app.ListHistory(history.HistoryQuery{})
	if err != nil {
		t.Fatalf("ListHistory: %v", err)
	}
	path := projectionArtifactPath(fixture.app, entries[0].ID)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("no projection to clear: %v", err)
	}

	if err := fixture.app.ClearHistory(); err != nil {
		t.Fatalf("ClearHistory: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("the projection survived ClearHistory (stat error: %v)", err)
	}
	runs, err := fixture.backend.GetHistory(fixture.collectionID, fixture.requestID, 0)
	if err != nil {
		t.Fatalf("GetHistory after clear: %v", err)
	}
	if len(runs) != 0 {
		t.Errorf("got %d runs after ClearHistory, want none", len(runs))
	}
}

// The record path must not deadlock, and the deadlock it must not have is a
// specific one §7 names.
//
// recordSendHistory runs with a.mu HELD (app_send.go's tail). The function that
// knows the hydrated secret values, mcpHydratedSecretValues, re-acquires a.mu
// through readStateForMCP. If the projection ever looks its own values up
// instead of walking the already-locked state, the send path stops dead — and
// it stops on the success path of a request that already reached the server, so
// the symptom is a frozen app rather than an error anybody can read.
//
// Invoked here the way the send path invokes it — under the lock — while
// readers hammer the same lock from other goroutines, which is also what makes
// this worth running under -race: the record path reads a.state directly.
func TestRecordSendHistoryProjectionUnderStateLockDoesNotDeadlock(t *testing.T) {
	fixture := newProjectionFixture(t)
	item, response := historySeamProbe()
	item.ID = fixture.requestID

	const writers = 4
	const readers = 4
	const rounds = 20

	finished := make(chan struct{})
	go func() {
		defer close(finished)
		var wait sync.WaitGroup
		for w := 0; w < writers; w++ {
			wait.Add(1)
			go func() {
				defer wait.Done()
				for i := 0; i < rounds; i++ {
					// Exactly the send path's tail: take a.mu, record, release.
					fixture.app.mu.Lock()
					_ = fixture.app.recordSendHistoryWithMCPProjection(
						fixture.collectionID, item, response, []string{projectionOldSecret})
					fixture.app.mu.Unlock()
				}
			}()
		}
		for r := 0; r < readers; r++ {
			wait.Add(1)
			go func() {
				defer wait.Done()
				for i := 0; i < rounds; i++ {
					// The other door onto the same walk, which DOES take a.mu.
					if _, err := fixture.app.mcpHydratedSecretValues(); err != nil {
						t.Errorf("mcpHydratedSecretValues: %v", err)
						return
					}
					if _, err := fixture.backend.GetHistory(fixture.collectionID, fixture.requestID, 0); err != nil {
						t.Errorf("GetHistory: %v", err)
						return
					}
				}
			}()
		}
		wait.Wait()
	}()

	select {
	case <-finished:
	case <-time.After(60 * time.Second):
		t.Fatal("the history record path deadlocked under a.mu — the projection is looking its secret values up instead of walking the already-locked state (§7)")
	}

	runs, err := fixture.backend.GetHistory(fixture.collectionID, fixture.requestID, 0)
	if err != nil {
		t.Fatalf("GetHistory: %v", err)
	}
	if len(runs) == 0 {
		t.Fatal("nothing was recorded; the contention loop is not exercising the record path")
	}
	if strings.Contains(marshalHistoryRuns(t, runs), projectionOldSecret) {
		t.Error("the contended record path wrote an unmasked projection")
	}
}
