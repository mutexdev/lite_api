package core

// Starting, stopping and failing to start the MCP listener.
//
// The listener is the only part of this feature that owns something outside the
// process's own memory — a TCP port — and every interesting failure here is
// about that port: one left bound after the toggle went off, two bound by two
// windows of the same app, or one that could not be bound at all and said
// nothing about it.
//
// Ports are taken from the OS rather than hardcoded. A fixed test port collides
// with whatever else the developer is running and turns a real pass into a
// flake that looks like this feature's fault.

import (
	"context"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/mutexdev/lite_api/internal/atomicfile"
	"github.com/mutexdev/lite_api/internal/types"
)

// freeMCPPort asks the OS for a port and gives it straight back, so the number
// is one nothing else on this machine has claimed. Racy in principle — another
// process could take it in the gap — and the alternative (a hardcoded port) is
// racy in practice, every time, against the developer's own dev server.
func freeMCPPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve a port: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatalf("release the reserved port: %v", err)
	}
	return port
}

// newMCPControlApp is an App whose notifications are captured rather than
// emitted, and whose listener is stopped however the test ends.
func newMCPControlApp(t *testing.T) (*App, *[]Notification) {
	t.Helper()
	app := newAppForTest(t)
	raised := []Notification{}
	app.notificationEmit = func(notification Notification) {
		raised = append(raised, notification)
	}
	t.Cleanup(app.stopMCPServer)
	return app, &raised
}

// storeMCPPreference records the preference in state the way UpdatePreferences
// would, without applying it. GetMCPStatus reads Enabled from state rather than
// from the running server — deliberately, since the two disagree exactly when a
// bind has failed — so a test that only calls applyMCPPreferences would see
// Enabled:false and be measuring the wrong thing.
func storeMCPPreference(app *App, preferences types.MCPPreferences) {
	app.mu.Lock()
	defer app.mu.Unlock()
	app.state.Preferences.MCP = preferences
}

func TestApplyMCPPreferencesStartsAndStopsTheListener(t *testing.T) {
	app, _ := newMCPControlApp(t)
	port := freeMCPPort(t)

	storeMCPPreference(app, types.MCPPreferences{Enabled: true, Port: port})
	app.applyMCPPreferences(types.MCPPreferences{Enabled: true, Port: port})

	if got := app.mcpRunningPort(); got != port {
		t.Fatalf("the server reports port %d, want the configured %d", got, port)
	}
	assertMCPPortAnswers(t, port)

	status, err := app.GetMCPStatus()
	if err != nil {
		t.Fatalf("GetMCPStatus: %v", err)
	}
	if !status.Running || !status.Enabled || status.Port != port {
		t.Errorf("status is %+v, want enabled and running on %d", status, port)
	}
	if status.LastError != "" {
		t.Errorf("a successful start left LastError=%q", status.LastError)
	}

	// Off again. The port must actually be released, not merely forgotten: a
	// listener left bound outlives the toggle and makes the next start fail
	// with EADDRINUSE against the app's own ghost.
	app.applyMCPPreferences(types.MCPPreferences{Enabled: false, Port: port})
	if got := app.mcpRunningPort(); got != 0 {
		t.Fatalf("the server is still running on port %d after being disabled", got)
	}

	app.applyMCPPreferences(types.MCPPreferences{Enabled: true, Port: port})
	if got := app.mcpRunningPort(); got != port {
		t.Fatalf("re-enabling on the same port gave %d, want %d — the first listener never let go", got, port)
	}
}

// Applying the same preference twice must not restart anything. A restart would
// drop in-flight tool calls every time the user saved an unrelated setting.
func TestApplyMCPPreferencesIsIdempotent(t *testing.T) {
	app, _ := newMCPControlApp(t)
	port := freeMCPPort(t)
	preferences := types.MCPPreferences{Enabled: true, Port: port}

	app.applyMCPPreferences(preferences)
	app.mcpMu.Lock()
	first := app.mcpServer
	app.mcpMu.Unlock()

	app.applyMCPPreferences(preferences)
	app.mcpMu.Lock()
	second := app.mcpServer
	app.mcpMu.Unlock()

	if first == nil || second == nil {
		t.Fatal("no server is running after applying an enabled preference")
	}
	if first != second {
		t.Error("re-applying an unchanged preference replaced the running server")
	}
	if got := app.mcpRunningPort(); got != port {
		t.Errorf("port is %d after the second apply, want %d", got, port)
	}
}

// A port somebody else holds. The failure has to be visible: recorded for the
// Settings panel and raised as a notification, with no panic and nothing left
// half-started.
func TestApplyMCPPreferencesReportsABindConflict(t *testing.T) {
	app, raised := newMCPControlApp(t)

	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("occupy a port: %v", err)
	}
	defer func() { _ = occupied.Close() }()
	port := occupied.Addr().(*net.TCPAddr).Port

	storeMCPPreference(app, types.MCPPreferences{Enabled: true, Port: port})
	app.applyMCPPreferences(types.MCPPreferences{Enabled: true, Port: port})

	if got := app.mcpRunningPort(); got != 0 {
		t.Errorf("a server is running on %d despite the bind failing", got)
	}
	status, err := app.GetMCPStatus()
	if err != nil {
		t.Fatalf("GetMCPStatus: %v", err)
	}
	if status.Running {
		t.Error("status reports Running after a failed bind")
	}
	if !status.Enabled {
		t.Error("status reports Disabled; the preference is on and only the bind failed — the user needs to see both")
	}
	if status.LastError == "" {
		t.Error("LastError is empty after a failed bind; the failure would be invisible")
	}
	if len(*raised) == 0 {
		t.Fatal("no notification was raised for the failed bind")
	}
	notification := (*raised)[len(*raised)-1]
	if notification.Level != "error" {
		t.Errorf("the notification level is %q, want error", notification.Level)
	}
	if !strings.Contains(notification.Message, fmt.Sprintf("%d", port)) {
		t.Errorf("the notification does not name the port: %q", notification.Message)
	}

	// The same failure repeated must not fill the notification list: it is
	// raised through the changed-channel machinery for exactly this reason.
	before := len(*raised)
	app.applyMCPPreferences(types.MCPPreferences{Enabled: true, Port: port})
	if len(*raised) != before {
		t.Errorf("the same bind failure was raised again (%d then %d notifications)", before, len(*raised))
	}

	// And once the port frees up, the next apply succeeds and clears the error.
	if err := occupied.Close(); err != nil {
		t.Fatalf("release the occupied port: %v", err)
	}
	app.applyMCPPreferences(types.MCPPreferences{Enabled: true, Port: port})
	if got := app.mcpRunningPort(); got != port {
		t.Fatalf("the retry did not start the server; port is %d", got)
	}
	status, err = app.GetMCPStatus()
	if err != nil {
		t.Fatalf("GetMCPStatus: %v", err)
	}
	if status.LastError != "" {
		t.Errorf("LastError survived a successful start: %q", status.LastError)
	}
}

// A secondary workspace window is a separate process against the same store. If
// it started a server too, every window after the first would fail to bind the
// one fixed port and report the feature as broken.
func TestApplyMCPPreferencesNeverStartsInAScopedWorkspaceWindow(t *testing.T) {
	app, raised := newMCPControlApp(t)
	app.workspaceRuntime = &workspaceWindowRuntime{}
	port := freeMCPPort(t)

	app.applyMCPPreferences(types.MCPPreferences{Enabled: true, Port: port})

	if got := app.mcpRunningPort(); got != 0 {
		t.Errorf("a scoped workspace window started a listener on port %d", got)
	}
	if len(*raised) != 0 {
		t.Errorf("a scoped window raised %d notifications; not starting is the normal case, not a failure", len(*raised))
	}
	// The port is genuinely untouched, so the primary process can still bind it.
	assertMCPPortIsFree(t, port)
}

// The whole wiring, through the binding the frontend actually calls.
func TestUpdatePreferencesStartsAndStopsTheMCPServer(t *testing.T) {
	app, _ := newMCPControlApp(t)
	port := freeMCPPort(t)

	state, err := app.GetState()
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	preferences := state.Preferences
	preferences.MCP = types.MCPPreferences{Enabled: true, Port: port}

	if _, err := app.UpdatePreferences(preferences); err != nil {
		t.Fatalf("UpdatePreferences: %v", err)
	}
	if got := app.mcpRunningPort(); got != port {
		t.Fatalf("the server is on port %d after enabling through UpdatePreferences, want %d", got, port)
	}

	preferences.MCP.Enabled = false
	if _, err := app.UpdatePreferences(preferences); err != nil {
		t.Fatalf("UpdatePreferences (disable): %v", err)
	}
	if got := app.mcpRunningPort(); got != 0 {
		t.Fatalf("the server is still running on %d after disabling", got)
	}
	assertMCPPortIsFree(t, port)
}

// startup reads the stored preference. An install that had the toggle on when
// it was last closed must come back with the server listening, without the user
// visiting Settings.
func TestStartupStartsTheMCPServerFromStoredPreferences(t *testing.T) {
	app, _ := newMCPControlApp(t)
	port := freeMCPPort(t)

	app.mu.Lock()
	app.state.Preferences.MCP = types.MCPPreferences{Enabled: true, Port: port}
	app.mu.Unlock()

	app.startup(context.Background())
	if got := app.mcpRunningPort(); got != port {
		t.Fatalf("startup left the server on port %d, want %d", got, port)
	}
	assertMCPPortAnswers(t, port)

	// shutdown must give the port back. This is the leaked-listener case that
	// stopAllMockServers exists for, applied to this listener.
	app.shutdown(context.Background())
	if got := app.mcpRunningPort(); got != 0 {
		t.Fatalf("shutdown left a server running on port %d", got)
	}
	assertMCPPortIsFree(t, port)
}

// The pairing command is a contract with the user's shell and with the agent
// that reads it. It is asserted as one exact string — including the quoting
// around the header — because every part of it is load-bearing and a
// reformatting would produce a command that runs and then fails to
// authenticate, with nothing to say why.
func TestGetMCPStatusRendersTheExactPairingCommand(t *testing.T) {
	app, _ := newMCPControlApp(t)

	const token = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	writeMCPTokenFixture(t, app, token)

	app.mu.Lock()
	app.state.Preferences.MCP = types.MCPPreferences{Enabled: false, Port: 43117}
	app.mu.Unlock()

	status, err := app.GetMCPStatus()
	if err != nil {
		t.Fatalf("GetMCPStatus: %v", err)
	}

	want := `claude mcp add --transport http liteapi http://127.0.0.1:43117/mcp --header "Authorization: Bearer ` + token + `"`
	if status.Command != want {
		t.Errorf("pairing command\n got: %s\nwant: %s", status.Command, want)
	}
	if status.Token != token {
		t.Errorf("token is %q, want %q", status.Token, token)
	}
	if status.Running {
		t.Error("status reports Running with nothing started")
	}
	if status.Port != 43117 {
		t.Errorf("port is %d; with nothing running it must fall back to the preference", status.Port)
	}
}

// While the server is up, the command names the RESOLVED port. They differ
// whenever the configured port was 0, and a command naming 0 connects nowhere.
func TestGetMCPStatusUsesTheResolvedPortWhileRunning(t *testing.T) {
	app, _ := newMCPControlApp(t)
	port := freeMCPPort(t)

	app.applyMCPPreferences(types.MCPPreferences{Enabled: true, Port: port})

	status, err := app.GetMCPStatus()
	if err != nil {
		t.Fatalf("GetMCPStatus: %v", err)
	}
	if !status.Running {
		t.Fatal("status reports the server as stopped while it is listening")
	}
	if status.Port != port {
		t.Errorf("status port is %d, want the resolved %d", status.Port, port)
	}
	if !strings.Contains(status.Command, fmt.Sprintf("http://127.0.0.1:%d/mcp", port)) {
		t.Errorf("the command does not name the running port: %s", status.Command)
	}
}

func writeMCPTokenFixture(t *testing.T, app *App, token string) {
	t.Helper()
	// Written the way the App writes it — owner-only, with the trailing newline
	// the reader trims — so this fixture cannot pass against a reader that only
	// handles the shape a test invented.
	if err := atomicfile.WritePrivate(app.mcpTokenPath(), []byte(token+"\n")); err != nil {
		t.Fatalf("write token fixture: %v", err)
	}
}

// assertMCPPortAnswers proves a listener is really bound, rather than trusting
// the App's own bookkeeping about it.
func assertMCPPortAnswers(t *testing.T, port int) {
	t.Helper()
	conn, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatalf("nothing is listening on port %d: %v", port, err)
	}
	_ = conn.Close()
}

// assertMCPPortIsFree proves the opposite: the socket is gone, not merely
// dereferenced.
//
// POLLED RATHER THAN CHECKED ONCE, and the reason is a real (small) window in
// net/http rather than test superstition. Server.Shutdown closes the listeners
// it is TRACKING, and Serve registers its listener on the goroutine that runs
// it; when Stop lands before that goroutine has got going, the close happens on
// Serve's own deferred path instead — after Stop has already returned. Every
// caller in the app tolerates that (nothing rebinds the port in the same
// microsecond); a single-shot assertion here would not.
func assertMCPPortIsFree(t *testing.T, port int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err == nil {
			_ = listener.Close()
			return
		}
		lastErr = err
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("port %d is still bound after 2s: %v", port, lastErr)
}
