package core

// The new-host approval round-trip: the app-side half of rule 4.
//
// mcp_guard.go decides that a run would send a secret somewhere the collections
// have never sent it. This file is what happens next: a prompt is raised in the
// app, the Backend goroutine blocks on a channel, and the user's answer — or the
// timeout — comes back as a plain bool.
//
// THE SHAPE IS startOAuth2AuthorizationWaiter's, on purpose (app_oauth2_browser.go:279
// and CompleteOAuth2Callback at :382). The problem is identical: a goroutine deep
// in a Go call path needs a decision only the frontend can make, the frontend
// answers through a separate binding on a different goroutine, and the wait must
// end on a timeout rather than never. Inventing a second mechanism for the same
// problem would mean two lifetimes to reason about; this is the one this codebase
// already reviews.
//
// DENY IS THE DEFAULT, AND EVERY UNCERTAIN PATH TAKES IT. No frontend to ask, a
// timeout, a resolver that never arrives: all of them deny. The guard exists to
// stop a credential travelling somewhere new, and "nobody was there to say no"
// is not consent.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/mutexdev/lite_api/internal/atomicfile"
	"github.com/mutexdev/lite_api/internal/types"
)

// mcpApprovalEvent is the Wails event the frontend listens on.
const mcpApprovalEvent = "mcp:approval"

// mcpApprovalDefaultTimeout bounds one prompt.
//
// Sixty seconds is the shape of the decision, not a guess: the user is being
// asked a yes/no question about a host and a credential they recognise, with the
// app already in front of them (the guard only fires for a run an agent asked
// for while the app is running). Longer would pin an MCP handler goroutine on a
// dialog the user walked away from; shorter would deny an answer that was on its
// way. The agent is told to ask the user and try again, which is a recoverable
// outcome either way.
const mcpApprovalDefaultTimeout = 60 * time.Second

// mcpApprovalsFileName is the remembered half, under the data directory next to
// the audit log.
const mcpApprovalsFileName = "mcp-approvals.json"

// mcpPendingApproval is one prompt awaiting an answer. The host and secret names
// are held here rather than passed back through the resolver so that "remember
// this" can persist the exact pairs the prompt asked about — the frontend
// answers with an id and a bool, and must not be trusted to restate what it was
// asked.
type mcpPendingApproval struct {
	result      chan bool
	host        string
	secretNames []string
}

// requestMCPApproval raises the prompt and waits for the answer. It returns
// true only for an explicit approval.
//
// ctx is the run's context, so a client that gave up (or the run timeout in
// mcpserver.RunTimeout) also ends the wait — a prompt outliving the run it
// guards would leave the user answering a question about nothing.
func (a *App) requestMCPApproval(ctx context.Context, request types.MCPApprovalRequest) bool {
	// Nobody to ask. a.ctx is nil until Wails calls startup and in every test,
	// and wailsruntime.EventsEmit dereferences it — so this check is both the
	// crash guard and the honest answer: with no window open there is no user to
	// approve anything, and the run is denied rather than left to time out.
	if a.mcpApprovalEmit == nil && a.ctx == nil {
		return false
	}

	id := newID("mcp-approval")
	request.ID = id
	pending := &mcpPendingApproval{
		// Buffered, so a resolver never blocks even if the waiter has already
		// given up on a timeout and stopped receiving.
		result:      make(chan bool, 1),
		host:        request.Host,
		secretNames: append([]string{}, request.SecretNames...),
	}
	a.mcpApprovalMu.Lock()
	if a.mcpApprovals == nil {
		a.mcpApprovals = map[string]*mcpPendingApproval{}
	}
	a.mcpApprovals[id] = pending
	a.mcpApprovalMu.Unlock()

	// Always clean up the registration, whichever way the wait ends. Without
	// this a timed-out prompt would sit in the map forever and a later
	// ResolveMCPApproval for it would "succeed" against a run that is long gone.
	defer func() {
		a.mcpApprovalMu.Lock()
		delete(a.mcpApprovals, id)
		a.mcpApprovalMu.Unlock()
	}()

	a.emitMCPApproval(request)

	timeout := a.mcpApprovalTimeout
	if timeout <= 0 {
		timeout = mcpApprovalDefaultTimeout
	}
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	select {
	case approved := <-pending.result:
		return approved
	case <-waitCtx.Done():
		return false
	}
}

// emitMCPApproval pushes the prompt to the frontend, through the same test seam
// as pushNotification (a.notificationEmit at app.go:69): EventsEmit needs a
// Wails context that no test has.
func (a *App) emitMCPApproval(request types.MCPApprovalRequest) {
	if a.mcpApprovalEmit != nil {
		a.mcpApprovalEmit(request)
		return
	}
	if a.ctx == nil {
		return
	}
	wailsruntime.EventsEmit(a.ctx, mcpApprovalEvent, request)
}

// --- bindings ------------------------------------------------------------

// ResolveMCPApproval answers one new-host prompt.
//
// approve runs the request; remember additionally persists the (secret, host)
// pairs so the same combination never asks again. Approving WITHOUT remembering
// is the default the UI should offer: it allows exactly this run, which is the
// smallest thing that unblocks the agent.
//
// An id that is not pending is an error rather than a silent success — it means
// the run already timed out or was answered, and telling the user so is better
// than a dialog that closes as though it did something.
func (a *App) ResolveMCPApproval(id string, approve bool, remember bool) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("approval id is required")
	}
	a.mcpApprovalMu.Lock()
	pending, found := a.mcpApprovals[id]
	if found {
		delete(a.mcpApprovals, id)
	}
	a.mcpApprovalMu.Unlock()
	if !found {
		return fmt.Errorf("no MCP approval is waiting with id %q; it may have already been answered or timed out", id)
	}

	// Persist BEFORE releasing the waiter. The run resumes the moment the
	// channel is read, and it recomputes nothing — but a second run racing
	// behind it should see the remembered pair rather than prompt again.
	if approve && remember {
		if err := a.rememberMCPApprovals(pending.secretNames, pending.host); err != nil {
			// The user said yes; a file that could not be written must not turn
			// that into a denial. Let the run proceed and report the failure to
			// remember, which is the part that actually did not happen.
			pending.result <- true
			return fmt.Errorf("the run was approved, but the choice could not be remembered: %w", err)
		}
	}
	pending.result <- approve
	return nil
}

// --- remembered approvals -------------------------------------------------

// rememberMCPApprovals adds one (secret, host) pair per secret and writes the
// file. Existing pairs are kept and duplicates are not re-added, so approving
// the same host twice does not grow the file.
func (a *App) rememberMCPApprovals(secretNames []string, host string) error {
	host = mcpNormalizeHost(host)
	if host == "" || len(secretNames) == 0 {
		return nil
	}
	a.mcpApprovalFileMu.Lock()
	defer a.mcpApprovalFileMu.Unlock()
	if err := a.loadMCPApprovalsLocked(); err != nil {
		return err
	}
	existing := map[string]bool{}
	for _, approval := range a.mcpApprovalsRemembered {
		existing[mcpApprovalKey(approval.Secret, approval.Host)] = true
	}
	at := time.Now().UTC().Format(time.RFC3339)
	changed := false
	for _, name := range secretNames {
		name = strings.TrimSpace(name)
		if name == "" || existing[mcpApprovalKey(name, host)] {
			continue
		}
		a.mcpApprovalsRemembered = append(a.mcpApprovalsRemembered, types.MCPApproval{
			Secret:     name,
			Host:       host,
			ApprovedAt: at,
		})
		existing[mcpApprovalKey(name, host)] = true
		changed = true
	}
	if !changed {
		return nil
	}
	return a.writeMCPApprovalsLocked()
}

// mcpRememberedHostsForSecret is the persisted half of one secret's allowlist.
func (a *App) mcpRememberedHostsForSecret(secretName string) (map[string]bool, error) {
	a.mcpApprovalFileMu.Lock()
	defer a.mcpApprovalFileMu.Unlock()
	if err := a.loadMCPApprovalsLocked(); err != nil {
		return nil, err
	}
	hosts := map[string]bool{}
	for _, approval := range a.mcpApprovalsRemembered {
		if approval.Secret == secretName {
			hosts[mcpNormalizeHost(approval.Host)] = true
		}
	}
	return hosts, nil
}

// loadMCPApprovalsLocked reads the file once per process.
//
// A missing file is empty rather than an error — no approval has ever been
// remembered, which is the state every install starts in. A CORRUPT file is
// also treated as empty, and deliberately: the failure mode of refusing to
// answer is that every guarded run blocks on a prompt forever, whereas the
// failure mode of forgetting is one extra prompt the user can answer again.
// Nothing here is a credential, so there is nothing to lose by rebuilding it.
//
// a.mcpApprovalFileMu must be held.
func (a *App) loadMCPApprovalsLocked() error {
	if a.mcpApprovalsLoaded {
		return nil
	}
	a.mcpApprovalsLoaded = true
	data, err := os.ReadFile(a.mcpApprovalsPath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var file types.MCPApprovalFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil
	}
	a.mcpApprovalsRemembered = file.Approvals
	return nil
}

// writeMCPApprovalsLocked persists the remembered approvals.
//
// WritePrivate rather than Write: this file names which credential the user has
// allowed to reach which host, which is a map of their infrastructure even
// though it holds no secret value, and it is written next to the token file that
// is already owner-only.
//
// a.mcpApprovalFileMu must be held.
func (a *App) writeMCPApprovalsLocked() error {
	sorted := append([]types.MCPApproval{}, a.mcpApprovalsRemembered...)
	// Sorted so the file has a stable order across writes: an unordered one
	// produces a whole-file diff every time a pair is added, which makes the
	// history of a file the user may well be reviewing useless.
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Secret != sorted[j].Secret {
			return sorted[i].Secret < sorted[j].Secret
		}
		return sorted[i].Host < sorted[j].Host
	})
	encoded, err := json.MarshalIndent(types.MCPApprovalFile{Approvals: sorted}, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.WritePrivate(a.mcpApprovalsPath(), append(encoded, '\n'))
}

func (a *App) mcpApprovalsPath() string {
	return filepath.Join(a.dataDir, mcpApprovalsFileName)
}

func mcpApprovalKey(secret, host string) string {
	return secret + "\x00" + mcpNormalizeHost(host)
}
