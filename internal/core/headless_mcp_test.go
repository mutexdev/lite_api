package core

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mutexdev/lite_api/internal/mcpserver"
	"github.com/mutexdev/lite_api/internal/types"
)

// These tests cover the bootstrap `liteapi mcp` performs before any protocol is
// spoken: who is allowed to start, what happens when someone else already owns
// the store, and what a headless process can and cannot do once it is serving.
// The transport itself is pinned in internal/mcpserver/stdio_test.go.

// startHeadlessMCPForTest boots a headless instance rooted at the caller's temp
// directory and guarantees it is stopped — which releases the workspace lease
// and retires the background persist writer — before the directory is removed.
//
// The same LIFO ordering rule newAppForTest documents applies here: dir must
// already be (or be under) a t.TempDir() taken earlier in the test, so this
// cleanup runs before that directory's removal.
func startHeadlessMCPForTest(t testing.TB, dir string) (*headlessMCP, error) {
	t.Helper()
	headless, err := startHeadlessMCP(dir)
	t.Cleanup(func() {
		if headless != nil {
			_ = headless.stop()
		}
	})
	return headless, err
}

// TestHeadlessMCPRefusesWhileTheStoreIsOwnedAndServesWhenItIsFree is the
// single-writer rule end to end: with the app holding the workspace lease the
// subcommand must refuse and say so usefully, and once that lease is released
// the same data directory must serve.
func TestHeadlessMCPRefusesWhileTheStoreIsOwnedAndServesWhenItIsFree(t *testing.T) {
	dir := t.TempDir()

	// Stand in for the running GUI: the same constructor Run uses, taking the
	// same lease through the same lock store.
	gui, err := newProductionAppForTest(t, dir, nil)
	if err != nil {
		t.Fatalf("boot the app that owns the store: %v", err)
	}

	if _, err := startHeadlessMCPForTest(t, dir); err == nil {
		t.Fatal("liteapi mcp started as a SECOND writer over a store the app already owns")
	} else {
		message := err.Error()
		// The message is the deliverable here: someone typed a command and has
		// to learn what to do instead.
		for _, want := range []string{"already using this data directory", "claude mcp add --transport http"} {
			if !strings.Contains(message, want) {
				t.Fatalf("refusal does not mention %q: %s", want, message)
			}
		}
	}

	// The app exits: flush, then give up the lease, exactly as shutdown does.
	flushPersistForTest(t, gui)
	gui.workspaceRuntime.release()

	headless, err := startHeadlessMCPForTest(t, dir)
	if err != nil {
		t.Fatalf("liteapi mcp refused a free store: %v", err)
	}
	if headless.app.workspaceRuntime == nil {
		t.Fatal("headless app holds no workspace lease, so nothing stops a second writer")
	}

	// And now IT is the writer: a second headless instance must be refused for
	// the same reason the first one was.
	if _, err := startHeadlessMCPForTest(t, dir); err == nil {
		t.Fatal("a second liteapi mcp started over a store the first one owns")
	}
}

// TestHeadlessMCPServesTheRealCollectionsOverStdio proves the bootstrap is the
// app rather than a fixture: the tools answer from the state the data directory
// actually holds, over the same pipe the subcommand serves.
func TestHeadlessMCPServesTheRealCollectionsOverStdio(t *testing.T) {
	dir := t.TempDir()
	headless, err := startHeadlessMCPForTest(t, dir)
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	var out strings.Builder
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"list_collections","arguments":{}}}`,
	}, "\n") + "\n"
	if err := headless.serve(ctx, strings.NewReader(input), &out, io.Discard); err != nil {
		t.Fatalf("serve: %v", err)
	}

	lines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("wrote %d lines, want 2: %q", len(lines), lines)
	}

	var response struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(lines[1]), &response); err != nil {
		t.Fatalf("decode list_collections: %v", err)
	}
	if response.Result.IsError || len(response.Result.Content) == 0 {
		t.Fatalf("list_collections failed over stdio: %s", lines[1])
	}
	// The default workspace's own collection, read off disk by the same
	// Backend the GUI serves from.
	wanted := headless.app.state.Workspaces[0].Collections[0].ID
	if !strings.Contains(response.Result.Content[0].Text, wanted) {
		t.Fatalf("collection %s missing from %s", wanted, response.Result.Content[0].Text)
	}

	// The call was audited, on the same log the panel reads.
	entries, err := headless.app.GetMCPAuditLog(10)
	if err != nil {
		t.Fatalf("read audit log: %v", err)
	}
	if len(entries) != 1 || entries[0].Tool != "list_collections" || entries[0].Outcome != "ok" {
		t.Fatalf("audit log = %+v", entries)
	}
}

// TestHeadlessMCPHasNobodyToApproveANewHost pins the headless posture of the
// new-host guard. There is no frontend, so a run that would send a secret
// somewhere new cannot raise a prompt — and the guard's answer to "no user" is
// to deny, not to wait or to wave it through.
func TestHeadlessMCPHasNobodyToApproveANewHost(t *testing.T) {
	dir := t.TempDir()
	headless, err := startHeadlessMCPForTest(t, dir)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	app := headless.app
	if app.ctx != nil || app.mcpApprovalEmit != nil {
		t.Fatal("a headless app has an approval channel; the guard would prompt a window that does not exist")
	}
	approved := app.requestMCPApproval(context.Background(), types.MCPApprovalRequest{
		RequestName: "Create terminal",
		Host:        "attacker.example.test",
		SecretNames: []string{"apiToken"},
	})
	if approved {
		t.Fatal("the new-host guard approved a host with no user to approve it")
	}
}

// findCreatedItemIDByName is CreateRequestInFolder's "which one did I just
// create" for tests that only have the app's own bindings to work with (no
// mcpCreatedItemID before/after diff here — this is UI-side plumbing, not the
// MCP write tier).
func findCreatedItemIDByName(t *testing.T, state AppState, collectionID, name string) string {
	t.Helper()
	for _, workspace := range state.Workspaces {
		for _, collection := range workspace.Collections {
			if collection.ID != collectionID {
				continue
			}
			for _, item := range collection.Items {
				if item.Name == name {
					return item.ID
				}
			}
		}
	}
	t.Fatalf("no item named %q in collection %q", name, collectionID)
	return ""
}

// TestHeadlessMCPRememberedApprovalWidensAndNothingElseDoes is attack area 5's
// explicit brief: "confirm remembered approvals from the GUI still widen
// correctly and nothing else does."
//
// TestHeadlessMCPHasNobodyToApproveANewHost already pins that a headless run
// with NO remembered approval denies outright. What it does not cover is the
// design's other headless promise — docs/mcp-agent-interface.md's "Destinations
// already approved in the app still work... because they are keyed on the full
// site... an approval given in the app applies here only to the same request
// under the same environment." This test writes an approval the way the GUI's
// own ResolveMCPApproval(..., remember=true) would (through the same
// a.rememberMCPApproval production call, not a hand-built JSON file), stops
// that "GUI" process, and starts a headless one over the identical data
// directory — the same lease handover TestHeadlessMCPRefusesWhileTheStoreIs-
// OwnedAndServesWhenItIsFree exercises for the lock alone.
//
// ONE request, retargeted by run_request's own `variables` override — the
// SAME shape TestMCPRunRequestApprovedNewHostRuns /
// TestMCPRunRequestRememberedApprovalDoesNotPromptAgain use (mcp_run_test.go)
// — rather than two separately-authored requests each already pointed at its
// own server. That distinction is load-bearing: a request whose STORED URL is
// a literal http(s) address is trivially inside its own Base with no approval
// needed at all (an earlier draft of this test made exactly that mistake and
// "passed" for the wrong reason on one arm and failed for the wrong reason on
// the other). Overriding {{baseUrl}} is what makes the destination
// agent-chosen and therefore genuinely something only Base or an approval can
// admit.
func TestHeadlessMCPRememberedApprovalWidensAndNothingElseDoes(t *testing.T) {
	dir := t.TempDir()
	gui, err := newProductionAppForTest(t, dir, nil)
	if err != nil {
		t.Fatalf("boot the app that stands in for the GUI: %v", err)
	}

	var approvedHits atomic.Int64
	approvedServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		approvedHits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer approvedServer.Close()

	var otherHits atomic.Int64
	otherServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		otherHits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer otherServer.Close()

	state, err := gui.GetState()
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	collectionID := state.Workspaces[0].Collections[0].ID
	workspacePath := state.Workspaces[0].Path

	// The request's OWN stored destination is a template that resolves to
	// nothing agent-free (no environment defines baseUrl), so Base(main) for
	// this request is EMPTY: every destination it ever reaches at run time
	// arrives only through an override, and is checked as one.
	afterCreate, err := gui.CreateRequestInFolder(collectionID, "http", "Retargeted-by-override request", "")
	if err != nil {
		t.Fatalf("CreateRequestInFolder: %v", err)
	}
	requestID := findCreatedItemIDByName(t, afterCreate, collectionID, "Retargeted-by-override request")
	method, url := "GET", "{{baseUrl}}/ok"
	if _, err := gui.UpdateRequest(collectionID, requestID, RequestPatch{Method: &method, URL: &url}); err != nil {
		t.Fatalf("UpdateRequest: %v", err)
	}
	if _, err := gui.SaveRequest(collectionID, requestID); err != nil {
		t.Fatalf("SaveRequest: %v", err)
	}

	// THE GUI REMEMBERS ONE APPROVAL — for THIS request, THIS site (no
	// environment), and ONLY approvedServer's origin — through the exact
	// production call ResolveMCPApproval(..., remember=true) makes, never a
	// hand-written JSON file, so this test cannot pass merely because the
	// loader is lenient about a shape a human typed.
	origin, ok := OriginOfURL(approvedServer.URL)
	if !ok {
		t.Fatalf("OriginOfURL(%q) did not resolve", approvedServer.URL)
	}
	site := mcpDefinitionSite{
		workspacePath: workspacePath,
		collectionID:  collectionID,
		requestID:     requestID,
		environmentID: "",
	}
	if err := gui.rememberMCPApproval(site, origin, kindClassRequest); err != nil {
		t.Fatalf("rememberMCPApproval: %v", err)
	}

	// The "GUI" exits: flush state to disk and give up the lease, exactly as
	// TestHeadlessMCPRefusesWhileTheStoreIsOwnedAndServesWhenItIsFree does.
	flushPersistForTest(t, gui)
	gui.workspaceRuntime.release()

	headless, err := startHeadlessMCPForTest(t, dir)
	if err != nil {
		t.Fatalf("start headless: %v", err)
	}
	if headless.app.ctx != nil || headless.app.mcpApprovalEmit != nil {
		t.Fatal("this test needs a headless app with nobody to prompt, or it would not be measuring the remembered approval")
	}
	backend := &mcpBackend{app: headless.app}

	// THE REMEMBERED ORIGIN: no prompt is possible headlessly, and none is
	// needed — the run must succeed on the strength of the persisted
	// approval alone, retargeted to it by the SAME kind of override
	// run_request always uses to reach a non-Base destination.
	approvedResult, err := backend.RunRequest(context.Background(), mcpserver.RunRequestParams{
		CollectionID: collectionID,
		RequestID:    requestID,
		Variables:    map[string]string{"baseUrl": approvedServer.URL},
	})
	if err != nil {
		t.Fatalf("a remembered GUI approval did not widen a headless run: %v", err)
	}
	if approvedResult.Status != http.StatusOK || approvedHits.Load() != 1 {
		t.Fatalf("the remembered-approval run did not reach its server: status=%d hits=%d", approvedResult.Status, approvedHits.Load())
	}

	// THE OTHER ORIGIN: the IDENTICAL request and site, retargeted instead at
	// a destination the remembered approval never named. If a headless
	// install widened anything beyond the exact (site, origin, class) that
	// was remembered — this request generally, "no environment" in general —
	// this would pass too, which is exactly the failure mode "nothing else
	// does" rules out.
	_, err = backend.RunRequest(context.Background(), mcpserver.RunRequestParams{
		CollectionID: collectionID,
		RequestID:    requestID,
		Variables:    map[string]string{"baseUrl": otherServer.URL},
	})
	if err == nil {
		t.Fatal("an origin nobody approved was let through a headless run with nobody to ask")
	}
	if !errors.Is(err, mcpserver.ErrDenied) {
		t.Fatalf("the refusal does not wrap ErrDenied: %v", err)
	}
	if otherHits.Load() != 0 {
		t.Fatalf("the unapproved origin was still contacted %d times", otherHits.Load())
	}
}

// TestHeadlessMCPIgnoresTheHTTPServerPreference states the consent boundary:
// the Settings toggle governs the loopback listener, and a pipe the parent
// process owns is not that. The subcommand must neither consult it nor bind a
// port.
func TestHeadlessMCPIgnoresTheHTTPServerPreference(t *testing.T) {
	dir := t.TempDir()
	headless, err := startHeadlessMCPForTest(t, dir)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if preferences := headless.app.mcpPreferencesSnapshot(); preferences.Enabled {
		t.Fatal("the MCP HTTP server is enabled by default, so this test cannot tell the two apart")
	}
	if port := headless.app.mcpRunningPort(); port != 0 {
		t.Fatalf("headless mode bound port %d; stdio must publish nothing", port)
	}
	// It serves anyway — invoking the subcommand is the consent.
	var out strings.Builder
	if err := headless.serve(context.Background(), strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`+"\n"), &out, io.Discard); err != nil {
		t.Fatalf("serve: %v", err)
	}
	if !strings.Contains(out.String(), "list_collections") {
		t.Fatalf("tools/list with the HTTP server disabled returned %q", out.String())
	}
}

// TestHeadlessMCPRejectsUnknownArguments keeps the CLI surface to the one flag
// it documents. An unrecognised argument that was silently ignored would let a
// typo'd --data-dir open the wrong store.
func TestHeadlessMCPRejectsUnknownArguments(t *testing.T) {
	for _, args := range [][]string{{"--serve"}, {"--data-dir", t.TempDir(), "extra"}, {"--data-dir"}} {
		if err := RunHeadlessMCP(args); err == nil {
			t.Fatalf("RunHeadlessMCP(%q) was accepted", args)
		} else if !strings.Contains(err.Error(), HeadlessMCPUsage) {
			t.Fatalf("RunHeadlessMCP(%q) error does not state the usage: %v", args, err)
		}
	}
}
