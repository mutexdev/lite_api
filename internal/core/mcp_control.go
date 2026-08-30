package core

// Starting and stopping the embedded MCP server as the preference changes.
//
// LOCKING. Everything here runs under a.mcpMu and NEVER under a.mu. Binding a
// socket while holding the state lock is a known failure mode in this codebase
// — see the note at the top of mock_server.go and the shape StartMockServer was
// restructured into — because net.Listen can block on a busy port and would
// take every binding in the app down with it. The preference values are read
// out from under a.mu by the caller and arrive here as a plain value; the only
// place this file touches a.mu is the notification tail, after mcpMu is
// released.
//
// ONE LISTENER PER INSTALL, NOT PER WINDOW. A secondary workspace window is a
// separate OS process running the same binary against a scoped runtime
// (workspace_window_runtime.go), so "start the server when enabled" would have
// every open window race for the same fixed port and all but the first fail
// with EADDRINUSE — reported to the user as a broken feature. Only the primary
// process (workspaceRuntime == nil) starts a server. The scoped windows share
// the same collections through the same store, so nothing is lost by their
// staying quiet.

import (
	"fmt"

	"github.com/mutexdev/lite_api/internal/mcpserver"
	"github.com/mutexdev/lite_api/internal/types"
)

// mcpNotificationChannel keys the notify-on-change machinery. A bind failure
// re-reported on every preference save would otherwise fill the 20-entry
// notification list with copies of one message; notifyChangedLocked raises it
// once and stays quiet until the message changes or clears.
const mcpNotificationChannel = "mcp"

// applyMCPPreferences brings the running server in line with the preference.
//
// Idempotent: called with a preference that already matches what is running, it
// does nothing, so callers do not have to work out whether something changed.
// Never returns an error — there is no caller who could act on one. A failure
// to bind is recorded in mcpLastError (which GetMCPStatus surfaces in Settings,
// where the port lives) and raised as a notification.
func (a *App) applyMCPPreferences(preferences types.MCPPreferences) {
	// Read outside mcpMu: this is a file read and possibly a key generation,
	// and neither belongs under a lock that a status call also wants. Only
	// fetched when the server is actually wanted, so an install that never
	// enables MCP never writes a token file.
	wanted := preferences.Enabled && a.workspaceRuntime == nil
	token := ""
	if wanted {
		generated, err := a.mcpToken()
		if err != nil {
			a.recordMCPFailure(fmt.Sprintf("The MCP server could not read its access token: %v", err))
			return
		}
		token = generated
	}

	// mcpMu is held across the stop AND the start, deliberately. Releasing it
	// between them — to keep a status call from waiting out Stop's shutdown
	// grace — would let a second apply bind the port while the first is still
	// tearing its listener down, and the loser reports EADDRINUSE for a port
	// nothing is really using. A status call that waits is the cheaper failure.
	failure := ""
	a.mcpMu.Lock()
	switch {
	case !wanted:
		if a.mcpServer != nil {
			a.mcpServer.Stop()
			a.mcpServer = nil
		}
		a.mcpLastError = ""
	case a.mcpServer != nil && a.mcpServer.Port() == preferences.Port:
		// Already serving what was asked for.
	default:
		// Either nothing is running, or the port moved. Stop first: the old
		// listener holds nothing the new one needs, but leaving it bound would
		// keep answering on a port the user has stopped advertising.
		if a.mcpServer != nil {
			a.mcpServer.Stop()
			a.mcpServer = nil
		}
		// The audit recorder (rule 6) is installed at construction rather than
		// switched on later, so there is no window in which a served call goes
		// unrecorded. a.recordMCPAudit runs on the tool call's own goroutine and
		// writes through the audit store's own lock; it reaches a.mu only to
		// raise a notification when the write FAILED, and only after that lock
		// has been released. Nothing in it can block on the state lock, which is
		// what makes it safe to call from a handler that owns none of this App's
		// locks — and safe to construct here, under mcpMu.
		server := mcpserver.New(&mcpBackend{app: a}, token, preferences.Port,
			mcpserver.WithAuditRecorder(a.recordMCPAudit))
		if err := server.Start(); err != nil {
			failure = fmt.Sprintf("The MCP server could not start on port %d: %v", preferences.Port, err)
			a.mcpLastError = err.Error()
			break
		}
		a.mcpServer = server
		a.mcpLastError = ""
	}
	a.mcpMu.Unlock()

	if failure != "" {
		a.recordMCPFailure(failure)
		return
	}
	a.clearMCPFailure()
}

// stopMCPServer shuts the listener down. Called on shutdown alongside the mock
// and docs servers, and for the same reason: a listener left bound outlives the
// process on some platforms and blocks its own port on the next launch, which
// reads as "the MCP server is broken" rather than "the last one still has the
// socket".
func (a *App) stopMCPServer() {
	a.mcpMu.Lock()
	defer a.mcpMu.Unlock()
	if a.mcpServer == nil {
		return
	}
	a.mcpServer.Stop()
	a.mcpServer = nil
}

// mcpRunningPort is the resolved port of the live listener, or 0 when nothing
// is running. Resolved rather than configured: a server started on port 0 in a
// test binds an ephemeral port, and the pairing command has to name the port
// the client can actually reach.
func (a *App) mcpRunningPort() int {
	a.mcpMu.Lock()
	server := a.mcpServer
	a.mcpMu.Unlock()
	if server == nil {
		return 0
	}
	return server.Port()
}

// recordMCPFailure raises the failure where the user will see it. Takes a.mu
// for the notification alone, and only after mcpMu has been released: the two
// locks are never held together, in either order.
func (a *App) recordMCPFailure(message string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.notifyChangedLocked(mcpNotificationChannel, "error", message)
}

// clearMCPFailure resets the channel so a failure that goes away and comes back
// is reported again rather than suppressed forever as a repeat.
func (a *App) clearMCPFailure() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.notifyChangedLocked(mcpNotificationChannel, "error", "")
}

// mcpPreferencesSnapshot copies the MCP preference out from under the state
// lock, so the caller can act on it without holding one.
func (a *App) mcpPreferencesSnapshot() types.MCPPreferences {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.state.Preferences.MCP
}
