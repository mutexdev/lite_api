package core

// The approval round-trip and the remembered-approval store — §6 of the Phase 6
// design.
//
// Something deep in a run decides it may not proceed without the user's word:
// a prompt is raised in the app, the Backend goroutine blocks on a channel, and
// the user's answer — or the timeout — comes back as a plain bool.
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
//
// WHAT AN APPROVAL IS KEYED ON, AND WHY IT IS THIS NARROW (§6). Not (secret,
// host) — which is what shipped, and which is wider than the question the user
// was asked in every direction that matters. The key is
//
//	workspacePath \x00 collectionID \x00 requestID \x00 environmentID \x00
//	join(globalEnvironmentIDs, "\x1f") \x00 origin \x00 kindClass
//
// so an approval for request A never authorizes request B; one remembered under
// dev never authorizes the same request under production; one given for a token
// endpoint never authorizes the request's own destination. That is more prompts,
// and the trade is deliberate: §11 of the design accepts the frequency and
// answers it with session grants and remembered approvals, never with a wider
// key. In-execution allow-once grants use the IDENTICAL key shape (sessionKey in
// mcp_policy.go derives from the same approvalKey), so the two kinds of "yes"
// can never come to mean different things.
//
// MIGRATION FAILS CLOSED AND DESTROYS NOTHING. A file whose Version is not 1, or
// an entry still carrying the old secret/host fields, or one missing the request
// or environment fields, is IGNORED — a pre-v6 approval must not authorize under
// the wider old scope. When anything is ignored the original file is RENAMED to
// mcp-approvals.v0.json.bak (byte for byte; os.Rename moves it, nothing rewrites
// it), a visible warning is raised, and the next remember writes a fresh
// Version 1 file. Load never deletes.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
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
// asked a yes/no question about an origin and a request they recognise, with the
// app already in front of them (a prompt only fires for a run an agent asked
// for while the app is running). Longer would pin an MCP handler goroutine on a
// dialog the user walked away from; shorter would deny an answer that was on its
// way. The agent is told to ask the user and try again, which is a recoverable
// outcome either way.
const mcpApprovalDefaultTimeout = 60 * time.Second

// mcpApprovalsFileName is the remembered half, under the data directory next to
// the audit log.
const mcpApprovalsFileName = "mcp-approvals.json"

// mcpApprovalsBackupFileName is where content this build refuses to interpret is
// moved. One fixed name, not a timestamped series: this is a rescue copy for a
// user who wants to see what they had, not an archive.
const mcpApprovalsBackupFileName = "mcp-approvals.v0.json.bak"

// mcpApprovalStoreVersion is the only version this build reads or writes.
//
// AN UNRECOGNISED VERSION IS IGNORED, NOT GUESSED AT. A version field exists so
// that a future key change can be made without silently reinterpreting old
// entries under the new rules — which, for a store that says "this destination
// is allowed", is the one failure that would matter.
const mcpApprovalStoreVersion = 1

// mcpPendingApproval is one prompt awaiting an answer.
//
// THE SITE IS HELD HERE, NOT TAKEN FROM THE ANSWER. The frontend replies with an
// id and two booleans and must not be trusted to restate what it was asked; if
// "remember this" read its key from the reply, a frontend bug — or anything else
// that can call the binding — could persist an approval for a site the user was
// never shown.
type mcpPendingApproval struct {
	result chan bool
	site   mcpDefinitionSite
	origin Origin
	class  string
}

// requestMCPApproval raises the prompt and waits for the answer. It returns
// true only for an explicit approval.
//
// ctx is the run's context, so a client that gave up (or the run timeout in
// mcpserver.RunTimeout) also ends the wait — a prompt outliving the run it
// guards would leave the user answering a question about nothing.
//
// THE SIGNATURE STAYS (ctx, request) rather than taking the site separately,
// because the request already carries every field the key is built from and the
// callers that build it are the ones that know. What must not happen is the KEY
// being reconstructed from the frontend's reply, and it is not: the site is
// derived here, once, from the payload actually emitted, and held on the pending
// entry.
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
	site, origin, class := mcpApprovalSubject(request)
	pending := &mcpPendingApproval{
		// Buffered, so a resolver never blocks even if the waiter has already
		// given up on a timeout and stopped receiving.
		result: make(chan bool, 1),
		site:   site,
		origin: origin,
		class:  class,
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

// mcpApprovalSubject reads the site, origin and class back out of the payload
// that is about to be emitted.
//
// AN UNPARSEABLE OR ABSENT ORIGIN YIELDS THE ZERO Origin, and every write path
// below treats that as "there is nothing to remember". That is the fail-closed
// direction: a prompt whose destination could not be canonicalised is a prompt
// whose answer cannot be keyed, and inventing a key for it would remember
// something the user was not shown.
func mcpApprovalSubject(request types.MCPApprovalRequest) (mcpDefinitionSite, Origin, string) {
	site := mcpDefinitionSite{
		workspacePath:        request.WorkspacePath,
		collectionID:         request.CollectionID,
		requestID:            request.RequestID,
		environmentID:        request.EnvironmentID,
		globalEnvironmentIDs: append([]string(nil), request.GlobalEnvironmentIDs...),
	}
	origin, ok := OriginOfURL(request.Origin)
	if !ok {
		return site, Origin{}, ""
	}
	class := strings.TrimSpace(request.KindClass)
	if class == "" {
		class = kindClass(egressKind(strings.TrimSpace(request.Kind)))
	}
	return site, origin, class
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

// ResolveMCPApproval answers one approval prompt.
//
// approve runs the request; remember additionally persists the approval, keyed
// on the site the prompt named, so the same request under the same environment
// never asks about that origin again. Approving WITHOUT remembering is the
// default the UI should offer: it allows exactly this run, which is the smallest
// thing that unblocks the agent.
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
	// behind it should see the remembered approval rather than prompt again.
	if approve && remember {
		if err := a.rememberMCPApproval(pending.site, pending.origin, pending.class); err != nil {
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

// rememberMCPApproval persists one (site, origin, class) approval.
//
// A zero origin or an empty class is a no-op rather than an error: it means the
// prompt that produced this answer did not name a destination the store can key
// on (see mcpApprovalSubject), and writing a partial key would be worse than
// writing nothing — a key with a hole in it matches more than the user agreed
// to.
func (a *App) rememberMCPApproval(site mcpDefinitionSite, origin Origin, class string) error {
	class = strings.TrimSpace(class)
	if !origin.valid() || class == "" {
		return nil
	}
	a.mcpApprovalFileMu.Lock()
	defer a.mcpApprovalFileMu.Unlock()
	if err := a.loadMCPApprovalsLocked(); err != nil {
		return err
	}
	key := site.approvalKey(origin, class)
	for _, approval := range a.mcpApprovalsRemembered {
		if mcpStoredApprovalKey(approval) == key {
			// Already remembered. Approving the same origin twice must not grow
			// the file.
			return nil
		}
	}
	a.mcpApprovalsRemembered = append(a.mcpApprovalsRemembered, types.MCPApproval{
		WorkspacePath: site.workspacePath,
		CollectionID:  site.collectionID,
		RequestID:     site.requestID,
		EnvironmentID: site.environmentID,
		// Never nil: an omitted list would reload as a MISSING environment field
		// and be ignored by the migration rule below.
		GlobalEnvironmentIDs: append([]string{}, site.globalEnvironmentIDs...),
		Origin:               origin.String(),
		KindClass:            class,
		ApprovedAt:           time.Now().UTC(),
	})
	return a.writeMCPApprovalsLocked()
}

// mcpRememberedOriginApproved reports whether the user has already remembered
// this exact (site, origin, class).
//
// EXACT FULL-KEY MATCH, no prefix rule and no wildcard. Every looser comparison
// anyone might reach for — same collection, same origin under any environment,
// same request under any global-environment list — is a widening of an approval
// the user gave about one specific configuration.
func (a *App) mcpRememberedOriginApproved(site mcpDefinitionSite, origin Origin, class string) (bool, error) {
	class = strings.TrimSpace(class)
	if !origin.valid() || class == "" {
		return false, nil
	}
	a.mcpApprovalFileMu.Lock()
	defer a.mcpApprovalFileMu.Unlock()
	if err := a.loadMCPApprovalsLocked(); err != nil {
		return false, err
	}
	key := site.approvalKey(origin, class)
	for _, approval := range a.mcpApprovalsRemembered {
		if mcpStoredApprovalKey(approval) == key {
			return true, nil
		}
	}
	return false, nil
}

// mcpStoredApprovalKey renders a stored entry's key through the SAME function
// that builds a lookup key, so the two can never drift.
//
// The stored origin is re-parsed rather than compared as text: a canonical
// scheme://host:port round-trips exactly, and anything that does not parse
// returns "" and therefore matches nothing — which is the right answer for a
// hand-edited entry nobody can interpret.
func mcpStoredApprovalKey(approval types.MCPApproval) string {
	origin, ok := OriginOfURL(approval.Origin)
	if !ok {
		return ""
	}
	class := strings.TrimSpace(approval.KindClass)
	if class == "" {
		return ""
	}
	site := mcpDefinitionSite{
		workspacePath:        approval.WorkspacePath,
		collectionID:         approval.CollectionID,
		requestID:            approval.RequestID,
		environmentID:        approval.EnvironmentID,
		globalEnvironmentIDs: approval.GlobalEnvironmentIDs,
	}
	return site.approvalKey(origin, class)
}

// mcpRememberedHostsForSecret is what is LEFT of the shipped host guard's
// remembered-approval half, and it deliberately answers "nothing".
//
// WHY IT STILL EXISTS. Two callers outside this task's scope still ask the
// question — the shipped host guard (mcp_guard.go) and the write-tier authoring
// guard (mcp_write.go) — and both keep enforcing until the wave that retires
// them. Deleting the function would be a compile break in files this change does
// not own.
//
// WHY IT IS EMPTY. The store no longer holds (secret, host) pairs; §6 replaced
// them with full-site approvals, and the migration rule below ignores every
// legacy pair precisely so that an approval made under the old, wider scope
// cannot authorize anything under the new one. Honouring them here would
// reintroduce that scope through the back door. The consequence is one extra
// prompt on a path where the user had remembered a pair — strictly the
// fail-closed direction — and the run-tier guard makes it back up by consulting
// the new store directly for the run's own site (enforceMCPHostGuard).
func (a *App) mcpRememberedHostsForSecret(string) (map[string]bool, error) {
	return map[string]bool{}, nil
}

// --- the file ---------------------------------------------------------------

// mcpApprovalFileOnDisk is the file as read: entries stay raw so each can be
// examined for the fields it carries before anything is decoded into the model.
type mcpApprovalFileOnDisk struct {
	Version   int               `json:"version"`
	Approvals []json.RawMessage `json:"approvals"`
}

// mcpApprovalProbe answers "which fields does this entry actually carry".
//
// EVERY FIELD IS A POINTER, because PRESENCE is the question and not emptiness.
// An empty environmentId is a legitimate approval — the user had no collection
// environment selected — while an ABSENT one is an entry written before the
// environment was part of the key, and honouring it would authorize under every
// environment at once. JSON null reads as absent, which is the conservative
// reading of a hand-edited file.
type mcpApprovalProbe struct {
	// The pre-v6 shape. Either one present means this entry is a legacy
	// (secret, host) pair.
	Secret *string `json:"secret"`
	Host   *string `json:"host"`

	WorkspacePath        *string   `json:"workspacePath"`
	CollectionID         *string   `json:"collectionId"`
	RequestID            *string   `json:"requestId"`
	EnvironmentID        *string   `json:"environmentId"`
	GlobalEnvironmentIDs *[]string `json:"globalEnvironmentIds"`
	Origin               *string   `json:"origin"`
	KindClass            *string   `json:"kindClass"`
}

// usable reports whether this entry may be honoured by this build.
func (p mcpApprovalProbe) usable() bool {
	if p.Secret != nil || p.Host != nil {
		return false
	}
	if p.WorkspacePath == nil || p.CollectionID == nil {
		return false
	}
	// The request and the environment are the two dimensions §6 added; an entry
	// missing either was written under a key that spanned them.
	if p.RequestID == nil || strings.TrimSpace(*p.RequestID) == "" {
		return false
	}
	if p.EnvironmentID == nil || p.GlobalEnvironmentIDs == nil {
		return false
	}
	if p.Origin == nil || strings.TrimSpace(*p.Origin) == "" {
		return false
	}
	return p.KindClass != nil && strings.TrimSpace(*p.KindClass) != ""
}

// loadMCPApprovalsLocked reads the file once per process.
//
// A missing file is empty rather than an error — no approval has ever been
// remembered, which is the state every install starts in.
//
// ANYTHING THIS BUILD CANNOT HONOUR IS IGNORED, BACKED UP AND ANNOUNCED, in that
// order and never deleted. "Ignored" is the security half: an approval written
// under a wider key must not authorize under the narrower one, and an
// unparseable file must not be interpreted generously. "Backed up" is the
// honesty half: the user's own record of what they had allowed is moved aside
// byte for byte rather than dropped. "Announced" is what stops it being silent —
// the next run will prompt again, and a prompt the user does not expect is a
// prompt they click through.
//
// A READ ERROR THAT IS NOT "MISSING" IS RETURNED, not swallowed. Permissions or
// an I/O fault mean the store's contents are unknown, and every caller of this
// treats an error as "not approved".
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

	approvals, ignored := mcpDecodeApprovalFile(data)
	a.mcpApprovalsRemembered = approvals
	if !ignored {
		return nil
	}
	a.retireMCPApprovalFile()
	return nil
}

// mcpDecodeApprovalFile splits a file's bytes into the entries this build may
// honour and whether anything at all was refused.
func mcpDecodeApprovalFile(data []byte) (approvals []types.MCPApproval, ignored bool) {
	var file mcpApprovalFileOnDisk
	if err := json.Unmarshal(data, &file); err != nil {
		// Not decodable at all. Every entry it might have held is refused.
		return nil, true
	}
	if file.Version != mcpApprovalStoreVersion {
		// Includes the shipped file, which has no version field at all and
		// therefore reads as 0. Its entries are not examined: interpreting them
		// under this version's rules is the one thing the version field exists to
		// prevent.
		//
		// AN EMPTY ONE IS NOT A MIGRATION. Nothing was refused, so warning the
		// user that their approvals were dropped would be false, and moving an
		// empty file aside would leave a backup of nothing.
		return nil, len(file.Approvals) > 0
	}
	for _, raw := range file.Approvals {
		var probe mcpApprovalProbe
		if err := json.Unmarshal(raw, &probe); err != nil || !probe.usable() {
			ignored = true
			continue
		}
		var approval types.MCPApproval
		if err := json.Unmarshal(raw, &approval); err != nil {
			ignored = true
			continue
		}
		if mcpStoredApprovalKey(approval) == "" {
			// A present but uninterpretable origin or class. It could never
			// match a lookup, so keeping it would only mean writing it back out
			// forever.
			ignored = true
			continue
		}
		approvals = append(approvals, approval)
	}
	return approvals, ignored
}

// retireMCPApprovalFile moves the current file aside and says so.
//
// os.Rename, so the backup is the original's bytes exactly — nothing re-encodes
// it, and a user comparing the two is comparing what they had against what this
// build kept. The file is not rewritten here: the next remember writes a fresh
// Version 1 file, and until then the absence of the file is the honest state.
//
// a.mcpApprovalFileMu must be held.
func (a *App) retireMCPApprovalFile() {
	path := a.mcpApprovalsPath()
	backup := filepath.Join(a.dataDir, mcpApprovalsBackupFileName)
	message := fmt.Sprintf("Some remembered MCP approvals could not be read by this version and were not applied. The old file was kept as %s. Agent-initiated runs will ask again before contacting those destinations.", backup)
	if err := os.Rename(path, backup); err != nil {
		// The rename failing does not change the decision — the entries are
		// still ignored — but the user must not be told a backup exists when it
		// does not.
		message = fmt.Sprintf("Some remembered MCP approvals in %s could not be read by this version and were not applied, and the file could not be moved aside (%v). Agent-initiated runs will ask again before contacting those destinations.", path, err)
	}
	a.warnMCPApprovalStore(message)
}

// warnMCPApprovalStore raises the warning where the user will see it: a
// notification in the app, stderr in the headless `liteapi mcp` mode.
//
// pushNotification rather than notify: notify appends to a.state, which needs
// a.mu, and this runs under the approvals-file lock from an arbitrary caller's
// stack. The push has no such requirement — it reads two fields and emits.
func (a *App) warnMCPApprovalStore(message string) {
	if a.notificationEmit == nil && a.ctx == nil {
		// Headless: no window, no notification centre, and a warning that
		// vanishes is the same as no warning.
		fmt.Fprintln(os.Stderr, "liteapi: "+message)
		return
	}
	a.pushNotification(Notification{
		ID:          newID("notification"),
		Level:       "warning",
		Type:        notificationType("warning"),
		Title:       "Remembered MCP approvals were not applied",
		Message:     message,
		Description: message,
		Color:       notificationColor("warning"),
		At:          time.Now(),
	})
}

// writeMCPApprovalsLocked persists the remembered approvals.
//
// WritePrivate rather than Write: this file names which request the user has
// allowed to reach which origin, which is a map of their infrastructure even
// though it holds no secret value, and it is written next to the token file that
// is already owner-only.
//
// a.mcpApprovalFileMu must be held.
func (a *App) writeMCPApprovalsLocked() error {
	sorted := append([]types.MCPApproval{}, a.mcpApprovalsRemembered...)
	// Sorted by the key itself so the file has a stable order across writes: an
	// unordered one produces a whole-file diff every time an approval is added,
	// which makes the history of a file the user may well be reviewing useless.
	sort.Slice(sorted, func(i, j int) bool {
		return mcpStoredApprovalKey(sorted[i]) < mcpStoredApprovalKey(sorted[j])
	})
	for index := range sorted {
		if sorted[index].GlobalEnvironmentIDs == nil {
			sorted[index].GlobalEnvironmentIDs = []string{}
		}
	}
	encoded, err := json.MarshalIndent(types.MCPApprovalFile{
		Version:   mcpApprovalStoreVersion,
		Approvals: sorted,
	}, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.WritePrivate(a.mcpApprovalsPath(), append(encoded, '\n'))
}

func (a *App) mcpApprovalsPath() string {
	return filepath.Join(a.dataDir, mcpApprovalsFileName)
}

// --- display labels ---------------------------------------------------------

// mcpSiteLabels is the human half of a site: the names a prompt shows, which no
// key is ever built from.
//
// SEPARATE FROM mcpDefinitionSite ON PURPOSE. Renaming a collection must not
// invalidate an approval, so names cannot be in the key; but a prompt that named
// only ids would be unanswerable. Keeping the two apart is what lets both be
// true.
type mcpSiteLabels struct {
	runLabel               string
	collectionName         string
	requestName            string
	environmentName        string
	globalEnvironmentNames []string
	// advisorySecretNames is text for the prompt only. Nothing keys on it and no
	// enforcement consults it (§6).
	advisorySecretNames []string
}

// mcpSiteLabelBook maps sites to their labels for the duration of one execution.
//
// A FLOW IS WHY THIS EXISTS. One policy governs the whole flow and its scope is
// replaced per step, so by the time a prompt is raised the names belong to
// whichever step is running. The step registers its own labels when it sets its
// scope; the prompt looks them up.
type mcpSiteLabelBook struct {
	mu     sync.Mutex
	labels map[string]mcpSiteLabels
}

func newMCPSiteLabelBook() *mcpSiteLabelBook {
	return &mcpSiteLabelBook{labels: map[string]mcpSiteLabels{}}
}

// mcpSiteLabelKey identifies a site without an origin or a class — the same join
// the approval key uses, with those two positions empty, so one function decides
// what "the same site" means.
func mcpSiteLabelKey(site mcpDefinitionSite) string {
	return site.approvalKey(Origin{}, "")
}

func (b *mcpSiteLabelBook) record(site mcpDefinitionSite, labels mcpSiteLabels) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.labels == nil {
		b.labels = map[string]mcpSiteLabels{}
	}
	b.labels[mcpSiteLabelKey(site)] = labels
}

func (b *mcpSiteLabelBook) lookup(site mcpDefinitionSite) mcpSiteLabels {
	if b == nil {
		return mcpSiteLabels{}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.labels[mcpSiteLabelKey(site)]
}

// mcpApprovalRequestFor builds the prompt payload for one (site, origin, kind).
//
// This is what the policy's describe hook installs, and it is also what the
// shipped host guard fills in by hand — one shape, so the dialog renders the
// same sentence whichever boundary raised it.
func mcpApprovalRequestFor(site mcpDefinitionSite, labels mcpSiteLabels, origin Origin, kind egressKind, class string) types.MCPApprovalRequest {
	if strings.TrimSpace(class) == "" {
		class = kindClass(kind)
	}
	return types.MCPApprovalRequest{
		RunLabel:               labels.runLabel,
		WorkspacePath:          site.workspacePath,
		CollectionID:           site.collectionID,
		CollectionName:         labels.collectionName,
		RequestID:              site.requestID,
		RequestName:            labels.requestName,
		EnvironmentID:          site.environmentID,
		EnvironmentName:        labels.environmentName,
		GlobalEnvironmentIDs:   append([]string(nil), site.globalEnvironmentIDs...),
		GlobalEnvironmentNames: append([]string(nil), labels.globalEnvironmentNames...),
		Origin:                 origin.String(),
		Kind:                   string(kind),
		KindClass:              class,
		Host:                   origin.Host,
		SecretNames:            append([]string(nil), labels.advisorySecretNames...),
	}
}
