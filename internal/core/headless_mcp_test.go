package core

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

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
