package core

// `liteapi mcp` — the headless stdio MCP mode, phase 5 of
// docs/mcp-agent-interface.md.
//
// WHAT THIS IS. The same App the GUI runs, over the same data directory, with
// the same Backend, guard, audit and preferences, serving MCP on stdin/stdout
// instead of on a loopback port — for a machine or a session where the desktop
// app is not running (a CI box, an SSH session, an agent that would rather
// spawn a process than be handed a URL and a token). It is the app minus the
// window, not a second implementation of anything: the bootstrap below is
// newProductionApp, the same call Run makes before handing the App to Wails.
//
// SINGLE WRITER, ENFORCED BY THE LOCK THAT ALREADY EXISTS. One store may have
// exactly one writer. The GUI takes a per-workspace ownership lease on startup
// (workspacestate.WindowLockStore, acquired inside loadWorkspaceWindow), and
// this path takes it the same way, through the same call — so if the app is
// open, or another `liteapi mcp` is already serving, the bootstrap fails at
// Acquire and the subcommand refuses to start rather than becoming a second
// writer. The refusal is worded for the person who typed the command: it says
// the app is running and points at its HTTP endpoint, which serves the very
// same tools. The heartbeat is started for the same reason the window starts
// it: a lease that stops being renewed goes stale in 30 seconds and the next
// process would be entitled to take it.
//
// HEADLESS SEMANTICS, WHICH ARE THE GUI'S SEMANTICS MINUS A USER TO ASK.
//
//   - The new-host guard still runs, and with no frontend it DENIES. A run that
//     would resolve a secret into a request aimed at a host no open request
//     sends it to is refused with the standard secret-free message, because
//     requestMCPApproval has nobody to prompt (a.ctx and a.mcpApprovalEmit are
//     both nil here, which is exactly the case it already treats as "no user,
//     no approval"). What the GUI approved earlier still counts: remembered
//     approvals are read from mcp-approvals.json in the same data directory, so
//     a host the user approved once widens the allowlist headlessly too.
//   - Every tools/call is recorded in the same audit log the panel reads. The
//     recorder writes synchronously on the call's own goroutine (mcp_audit.go),
//     so there is no buffered tail to flush at exit — a call that returned is a
//     call already on disk.
//   - The write tier honours the same WriteTierEnabled preference, read from
//     the same state, because it is the same Backend reading it.
//   - The MCP enabled/port preferences are IRRELEVANT here and deliberately not
//     consulted. They govern the HTTP listener — a port other software on the
//     machine can reach — and the consent they represent is "publish this".
//     Nothing is published by a pipe the parent process already owns, so
//     invoking the subcommand IS the consent, and no listener is started: this
//     path never calls applyMCPPreferences.

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/mutexdev/lite_api/internal/mcpserver"
	"github.com/mutexdev/lite_api/internal/workspacestate"
)

// HeadlessMCPUsage is the one-line usage the subcommand answers with, and the
// only CLI surface it has: a data directory, because that is the single thing
// the GUI itself accepts on the command line (see productionDataDirFromArgs)
// and the one an agent config may need to point somewhere non-default.
const HeadlessMCPUsage = "usage: liteapi mcp [--data-dir <path>]"

// RunHeadlessMCP is the `liteapi mcp` entry point, called from main.go before
// Wails is given a chance to open a window.
//
// It returns nil for both ordinary endings — the client closing stdin, and
// SIGINT/SIGTERM — so the process exits 0; every refusal to start, and any
// failure to save state on the way out, comes back as an error main.go reports
// on stderr with a non-zero exit.
func RunHeadlessMCP(args []string) error {
	dataDir, remaining, err := productionDataDirFromArgs(args, defaultDataDir())
	if err != nil {
		return fmt.Errorf("%w\n%s", err, HeadlessMCPUsage)
	}
	if len(remaining) > 0 {
		return fmt.Errorf("unrecognised argument %q\n%s", remaining[0], HeadlessMCPUsage)
	}

	headless, err := startHeadlessMCP(dataDir)
	if err != nil {
		return err
	}

	// Installed BEFORE serving and torn down after, so a signal arriving during
	// the session ends the read loop rather than killing the process out from
	// under a state flush.
	ctx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()

	serveErr := headless.serve(ctx, os.Stdin, os.Stdout, os.Stderr)
	// Always stop, whichever way the session ended, and report the serve
	// failure in preference to the shutdown one: the first is what happened,
	// the second is what happened afterwards.
	stopErr := headless.stop()
	if serveErr != nil {
		return serveErr
	}
	return stopErr
}

// headlessMCP is one booted headless instance: the App holding the workspace
// lease, and the stdio server that serves tools against it.
type headlessMCP struct {
	app    *App
	server *mcpserver.Server
}

// startHeadlessMCP boots the App and prepares the server, or refuses.
//
// Nothing is read from stdin and nothing is written to stdout here: a failure
// to start must not put a byte on the protocol stream, because a client that
// launched this process is already listening there.
func startHeadlessMCP(dataDir string) (*headlessMCP, error) {
	app, err := newProductionApp(dataDir, nil)
	if err != nil {
		if busy := headlessLockRefusal(dataDir, err); busy != nil {
			return nil, busy
		}
		return nil, err
	}
	headless := &headlessMCP{app: app}

	// Renew the ownership lease for as long as this process serves. Without it
	// the lease goes stale after 30 seconds and a GUI launched mid-session
	// would be entitled to take the store while this process is still writing
	// to it — the exact thing the lock exists to prevent.
	if app.workspaceRuntime != nil {
		app.workspaceRuntime.startHeartbeat()
	}

	// The same readiness pass startup() runs, and for the same reason: the
	// collections, scratch workspace and environment secrets the tools answer
	// from are only settled once it has run. What is NOT run is
	// applyMCPPreferences — see the note at the top of this file.
	if err := app.ensureReady(); err != nil {
		_ = headless.stop()
		return nil, err
	}

	headless.server = mcpserver.NewStdio(&mcpBackend{app: app},
		mcpserver.WithAuditRecorder(app.recordMCPAudit))
	return headless, nil
}

// serve runs the transport until stdin closes or ctx is cancelled.
func (h *headlessMCP) serve(ctx context.Context, in io.Reader, out io.Writer, logs io.Writer) error {
	return h.server.ServeStdio(ctx, in, out, logs)
}

// stop mirrors App.shutdown for a process that never had a window.
//
// The ordering is shutdown's, and it matters for the same reason it does there:
// the background writer is retired first so the flush below is the last write
// of the process, and the lease is released only after that write has landed —
// persistWorkspaceRuntimeLocked needs the lease it gives up.
//
// What is deliberately absent is everything the GUI shuts down that a headless
// session never opened: mock and docs servers, an HTTP MCP listener, websocket
// and gRPC streams, terminals. Also absent is captureGeometry, which asks the
// Wails runtime about a window through a context this process does not have.
// Calling shutdown(nil) to "reuse" it would dereference that nil context.
func (h *headlessMCP) stop() error {
	if h == nil || h.app == nil {
		return nil
	}
	h.app.stopPersistWriter()
	err := h.app.flushPersist()
	if h.app.workspaceRuntime != nil {
		// Released even when the flush failed: holding the lease past exit
		// would lock the user out of their own store until it went stale.
		h.app.workspaceRuntime.release()
	}
	if err != nil {
		return fmt.Errorf("save workspace state: %w", err)
	}
	return nil
}

// headlessLockRefusal turns a failed bootstrap into the message the user needs
// when the cause is that something else already owns this store.
//
// The REFUSAL ITSELF is not decided here — it already happened, inside
// loadWorkspaceWindow's Acquire, which is the only place that can decide it
// without a race. This only asks the lock store afterwards whether the data
// directory is owned, so that "the app is already running" can be said plainly
// instead of surfacing a message about a workspace lease to someone who typed a
// command. When nothing is owned, the original error is the honest one and this
// returns nil so the caller reports it unchanged.
func headlessLockRefusal(dataDir string, cause error) error {
	registry, err := workspacestate.ReadWorkspaceRegistry(dataDir)
	if err != nil {
		return nil
	}
	locks := workspacestate.NewWindowLockStore(dataDir)
	for _, workspace := range registry.Workspaces {
		if locks.Available(workspace.Path) == nil {
			continue
		}
		return fmt.Errorf(`LiteAPI is already using this data directory (%s), so `+
			`there is nothing to serve here: one store gets one writer, and a second `+
			`one would corrupt it.

Connect to the running app's MCP endpoint instead. Settings -> AI access shows the
one-liner, with that install's port and token:

  claude mcp add --transport http liteapi http://127.0.0.1:<port>/mcp --header "Authorization: Bearer <token>"

If the app is NOT running, its lease expires within 30 seconds of the process
exiting; try again after that (%w)`, dataDir, cause)
	}
	return nil
}
