package core

// The scoped destination policy — §4.1, §4.2, §4.3 and §4.5 of the Phase 6
// design.
//
// ADDITIVE ONLY IN THIS WAVE. Nothing here is called by the engine yet; the
// shipped host guard (mcp_guard.go) is still the enforcing boundary and stays
// that way until the final wave. What this file provides is the authority
// object every later wave attaches to: one policy per MCP-initiated execution,
// carrying the scope stack, the approval callbacks and the execution overlay.
//
// THE ONE IDEA. Authority is keyed by (definition scope, egress kind) and is
// never unioned across scopes. A flow's step A cannot borrow step B's origins; a
// main request cannot borrow its own OAuth token endpoint's origin; a nested
// bru.runRequest's origins vanish when it returns. That is the whole
// confused-deputy story, and it is enforced by the shape of the stack rather
// than by remembering to clear something: SetScope REPLACES the top (flow steps
// are siblings, not ancestors) and Push/Pop nest LIFO with every Push paired to
// an immediate `defer PopScope()`.
//
// THE ONE LOCKING RULE, AND IT IS LOAD-BEARING (§4.2). p.mu is RELEASED before
// any callback that can block — the persisted-approval read (file I/O), the
// approval prompt (up to 60 seconds waiting on a human), the non-blocking
// notify, and the audit hook — and re-acquired only to record the outcome. An
// approval prompt is the longest-lived thing in this system; holding the policy
// lock across it would freeze every concurrent egress check, every PopScope on
// another goroutine, and the audit log, for a minute. TestMCPPolicyPromptDoesNot-
// HoldTheMutex pins this under -race.
//
// FAIL-CLOSED, EVERYWHERE. No active scope, an unresolvable origin, an unknown
// egress kind, an approval store that errors, no prompt callback (headless):
// every one of them denies. "Nobody was there to say no" is not consent —
// mcp_approvals.go's header already makes this argument for the prompt, and it
// applies unchanged to every other uncertain path.
//
// A NIL POLICY IS PERMISSIVE. That is not a hole: a nil policy means "this is
// not an MCP-initiated execution", i.e. a UI send, which §1.2(4) exempts by
// design. Provenance is what distinguishes the two, and provenance is explicit
// at the roots (§4.5) rather than inferred from a missing policy — see
// sendProvenance below.

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/mutexdev/lite_api/internal/mcpserver"
	"github.com/mutexdev/lite_api/internal/types"
)

// --- the definition site --------------------------------------------------

// mcpDefinitionSite is "Site of S" from §1.1: the full identity an approval is
// scoped to.
//
// EVERY FIELD NARROWS, AND THAT IS THE POINT. An approval remembered for one
// request never authorizes another; one remembered under the dev environment
// never authorizes the same request under production, because production
// resolves {{baseUrl}} to somewhere else and the user's "yes" was about the
// destination they were shown, not about the request.
//
// THE GLOBAL ENVIRONMENTS ARE A LIST even though
// scripting.ActiveGlobalEnvironmentsForWorkspace yields at most one today. If a
// future multi-active model lands, a scalar field would silently let approvals
// made under one configuration authorize a wider one; a list changes the key
// instead, and the cost of that is a conservative re-prompt.
type mcpDefinitionSite struct {
	workspacePath string
	// collectionID and requestID name the stored definition whose send is
	// executing — the main request, the current flow step, or the current
	// nested bru.runRequest target.
	collectionID string
	requestID    string
	// environmentID is the SELECTED COLLECTION environment; "" means none.
	environmentID string
	// globalEnvironmentIDs is the ordered list of active global environments.
	// Ordering cannot widen authority: a reordered but equivalent list produces
	// a different key and therefore a conservative re-prompt, never a match
	// that should not have been one.
	globalEnvironmentIDs []string
}

// The persisted approval key's separators (§6). Held as constants here because
// the session key below MUST be byte-identical in shape to the persisted one —
// two keys that differ would mean an allow-once grant and an
// allow-and-remember grant covered different things, which is exactly the kind
// of drift no test would notice until it mattered.
const (
	mcpSiteKeySeparator           = "\x00"
	mcpGlobalEnvironmentSeparator = "\x1f"
)

// approvalKey is the §6 key:
//
//	workspacePath \x00 collectionID \x00 requestID \x00 environmentID \x00
//	join(globalEnvironmentIDs, "\x1f") \x00 origin \x00 kindClass
func (s mcpDefinitionSite) approvalKey(o Origin, class string) string {
	return strings.Join([]string{
		s.workspacePath,
		s.collectionID,
		s.requestID,
		s.environmentID,
		strings.Join(s.globalEnvironmentIDs, mcpGlobalEnvironmentSeparator),
		o.String(),
		class,
	}, mcpSiteKeySeparator)
}

// environmentLabel is how the site's environment reads in an error or a prompt.
func (s mcpDefinitionSite) environmentLabel() string {
	if strings.TrimSpace(s.environmentID) == "" {
		return "(no environment)"
	}
	return s.environmentID
}

// sessionKey identifies one in-execution allow-once grant. It is a string type
// rather than a struct because mcpDefinitionSite carries a slice and is not
// comparable — and because deriving it from approvalKey is what guarantees the
// "identical full-site shape" §6 requires.
type sessionKey string

func newSessionKey(site mcpDefinitionSite, o Origin, class string) sessionKey {
	return sessionKey(site.approvalKey(o, class))
}

// --- the scope ------------------------------------------------------------

// mcpScopeOrigins is one definition scope's agent-free authority: what its
// stored definition points at, resolved under the run's single agent-free
// variable context.
type mcpScopeOrigins struct {
	site mcpDefinitionSite
	// perKind is Base(S, k). Populated per kind by mcpDefinitionOrigins; the
	// redirect and script kinds are filled with the main set at CONSTRUCTION
	// rather than unioned at lookup, so that Authorize can consult exactly one
	// set and no lookup-time rule can widen it.
	perKind map[egressKind]map[Origin]bool
	// dnsHosts is the hostnames of every origin above — what a script's DNS
	// shim is checked against, since a lookup has no port or scheme.
	dnsHosts map[string]bool
	// baseVars is the agent-free variable map, kept so transport construction
	// (§4.4) can resolve client certificates and the manual proxy without ever
	// consulting an agent-supplied value.
	baseVars map[string]string
	// mainURL is the agent-free-resolved main destination. Client-certificate
	// matching sees THIS rather than the runtime target, which is what makes
	// certificate selection agent-free (§4.4).
	mainURL string
}

// allows reports whether this scope's Base already covers (o, k).
func (s mcpScopeOrigins) allows(o Origin, k egressKind) bool {
	if !o.valid() {
		return false
	}
	return s.perKind[k][o]
}

// --- the policy -----------------------------------------------------------

// mcpPromptOutcome is what the user answered.
type mcpPromptOutcome int

const (
	// mcpPromptDeny is also what a timeout, a closed window and a headless run
	// produce. Deny is the default of every uncertain path.
	mcpPromptDeny mcpPromptOutcome = iota
	// mcpPromptAllowOnce grants for this execution only (a session grant).
	mcpPromptAllowOnce
	// mcpPromptAllowRemember additionally persists the approval. Persisting is
	// the callback's own job (ResolveMCPApproval writes before releasing the
	// waiter); the policy records the session grant either way, because an
	// approval that was just persisted is also allowed right now.
	mcpPromptAllowRemember
)

// Audit decisions, as the audit hook records them.
const (
	mcpDecisionBase        = "base"
	mcpDecisionApproved    = "approved"
	mcpDecisionSession     = "session"
	mcpDecisionPrompted    = "prompted"
	mcpDecisionDenied      = "denied"
	mcpDecisionUnavailable = "unavailable"
)

// mcpEgressPolicy is one MCP-initiated execution's authority. One per execution,
// created at the root (mcpRunPlan / RunFlow) and carried on the context.
type mcpEgressPolicy struct {
	mu sync.Mutex
	// scopes is the definition-scope STACK. The last element is active; nothing
	// consults any other element, ever.
	scopes []mcpScopeOrigins
	// overlay is §3's execution-scoped variable/cookie store. A pointer rather
	// than the plan's inline value purely because the overlay owns a mutex and
	// an inlined one would make every accidental copy of a policy a vet error
	// waiting to happen.
	overlay *mcpExecutionOverlay
	// approved reads the persisted approvals (§6). nil means "nothing has ever
	// been remembered", which is the state every install starts in.
	approved func(site mcpDefinitionSite, o Origin, class string) (bool, error)
	// prompt raises the blocking approval prompt. nil means headless: there is
	// no user to ask, so every unapproved origin is denied.
	prompt func(ctx context.Context, req types.MCPApprovalRequest) mcpPromptOutcome
	// notify raises a NON-BLOCKING prompt. The transport backstop cannot block
	// inside client.Timeout, so it denies immediately and notifies, letting an
	// approve-and-remember make the agent's retry succeed (§2 row 10).
	notify func(req types.MCPApprovalRequest)
	// session holds in-execution allow-once grants, keyed with the identical
	// full-site shape as the persisted approvals.
	session map[sessionKey]bool
	// audit records every decision. Called with p.mu released.
	audit func(site mcpDefinitionSite, o Origin, kind egressKind, decision string)
}

// newMCPEgressPolicy builds a policy with a fresh execution overlay.
func newMCPEgressPolicy() *mcpEgressPolicy {
	return &mcpEgressPolicy{overlay: newMCPExecutionOverlay()}
}

// SetScope replaces the active scope, leaving the rest of the stack alone. This
// is the flow-step transition: steps are SIBLINGS, so step B must not inherit
// step A's origins, and an append-only model would accumulate exactly that.
func (p *mcpEgressPolicy) SetScope(s mcpScopeOrigins) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.scopes) == 0 {
		p.scopes = append(p.scopes, s)
		return
	}
	p.scopes[len(p.scopes)-1] = s
}

// PushScope enters a nested definition scope (bru.runRequest). Every call site
// pairs it with an immediate `defer p.PopScope()`, so the nested authority
// disappears on the way out however the nested send ends — including a panic.
func (p *mcpEgressPolicy) PushScope(s mcpScopeOrigins) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.scopes = append(p.scopes, s)
}

// PopScope leaves the innermost scope.
//
// AN EMPTY STACK IS A NO-OP RATHER THAN A PANIC. Pop runs from a defer, which
// means it can run while a panic is already unwinding; panicking there would
// replace the real failure with this one. The fail-closed consequence is already
// right: with no scope on the stack, activeScope reports none and every
// subsequent authorization denies.
func (p *mcpEgressPolicy) PopScope() {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.scopes) == 0 {
		return
	}
	p.scopes = p.scopes[:len(p.scopes)-1]
}

// activeScope copies out the top of the stack. Callers must not hold p.mu.
func (p *mcpEgressPolicy) activeScope() (mcpScopeOrigins, bool) {
	if p == nil {
		return mcpScopeOrigins{}, false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.scopes) == 0 {
		return mcpScopeOrigins{}, false
	}
	return p.scopes[len(p.scopes)-1], true
}

// scopeDepth is how deep the stack is. Only tests and diagnostics read it.
func (p *mcpEgressPolicy) scopeDepth() int {
	if p == nil {
		return 0
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.scopes)
}

// Authorize decides whether this execution may contact o for egress kind k,
// prompting the user when nothing already says yes.
//
// ORDER (§4.2): base -> persisted -> session -> prompt. Base and the persisted
// store are consulted before the session grants because that is the order the
// design states; the two allow-paths are interchangeable in effect, and the
// literal order keeps this readable against the document.
//
// THE MUTEX IS NOT HELD ACROSS ANY CALLBACK. Each step copies what it needs out
// from under the lock, releases it, and re-acquires only to record.
func (p *mcpEgressPolicy) Authorize(ctx context.Context, o Origin, k egressKind) error {
	if p == nil {
		// Not an MCP execution. §1.2(4): a user-initiated send is never
		// subjected to any of this.
		return nil
	}
	site, class, done, err := p.precheck(o, k)
	if done {
		return err
	}

	if allowed, err := p.consultPersisted(site, o, k, class); err != nil || allowed {
		return err
	}
	if p.hasSessionGrant(site, o, class) {
		p.record(site, o, k, mcpDecisionSession)
		return nil
	}

	p.mu.Lock()
	prompt := p.prompt
	p.mu.Unlock()
	if prompt == nil {
		// Headless, or a window that has not started: there is nobody to ask.
		p.record(site, o, k, mcpDecisionDenied)
		return p.denial(site, o, k)
	}

	// THE LOCK IS RELEASED HERE, across a wait that can last a minute.
	outcome := prompt(ctx, p.approvalRequest(site, o, k, class))
	if outcome == mcpPromptDeny {
		p.record(site, o, k, mcpDecisionDenied)
		return p.denial(site, o, k)
	}
	p.grantSession(site, o, class)
	p.record(site, o, k, mcpDecisionPrompted)
	return nil
}

// AuthorizeNoPrompt is the transport backstop's entry point (§4.3 item 3).
//
// IT CANNOT PROMPT, and that is a property of where it runs rather than a
// simplification: RoundTrip executes inside http.Client.Timeout, so a 60-second
// approval wait would turn every unknown redirect into a timeout instead of a
// decision. So it denies immediately with an actionable error and raises a
// NON-BLOCKING prompt through notify; if the user approves and remembers, the
// agent's retry finds the approval already persisted and succeeds (§2 row 10).
func (p *mcpEgressPolicy) AuthorizeNoPrompt(o Origin, k egressKind) error {
	if p == nil {
		return nil
	}
	site, class, done, err := p.precheck(o, k)
	if done {
		return err
	}
	if allowed, err := p.consultPersisted(site, o, k, class); err != nil || allowed {
		return err
	}
	if p.hasSessionGrant(site, o, class) {
		p.record(site, o, k, mcpDecisionSession)
		return nil
	}
	p.mu.Lock()
	notify := p.notify
	p.mu.Unlock()
	if notify != nil {
		notify(p.approvalRequest(site, o, k, class))
	}
	p.record(site, o, k, mcpDecisionDenied)
	return p.denial(site, o, k)
}

// precheck runs the cheap, lock-local decisions both entry points share: is
// there an active scope, does the origin resolve, does Base already allow this,
// and does the kind have an approvable class. done means the answer is final —
// return err, which is nil when Base allowed it.
func (p *mcpEgressPolicy) precheck(o Origin, k egressKind) (site mcpDefinitionSite, class string, done bool, err error) {
	scope, ok := p.activeScope()
	if !ok {
		// No scope means no authority. This happens if a Push/Pop pairing is
		// broken, and denying is the only safe reading of it.
		p.record(mcpDefinitionSite{}, o, k, mcpDecisionUnavailable)
		return mcpDefinitionSite{}, "", true, fmt.Errorf("%w: this run has no active request scope, so the destination %s could not be checked; this is a bug in LiteAPI — report it rather than retrying",
			mcpserver.ErrDenied, originLabel(o))
	}
	site = scope.site
	if !o.valid() {
		p.record(site, o, k, mcpDecisionDenied)
		return site, "", true, fmt.Errorf("%w: this run's %s destination did not resolve to a usable origin, so it could not be checked; fix the URL or the variables it depends on",
			mcpserver.ErrDenied, k)
	}
	if scope.allows(o, k) {
		p.record(site, o, k, mcpDecisionBase)
		return site, "", true, nil
	}
	class = kindClass(k)
	if class == "" {
		p.record(site, o, k, mcpDecisionDenied)
		return site, "", true, fmt.Errorf("%w: LiteAPI does not know how to authorize a %q egress, so it was refused; this is a bug in LiteAPI — report it rather than retrying",
			mcpserver.ErrDenied, string(k))
	}
	if class == kindClassProxy {
		// §1.1: proxy has no approval path. The effective manual proxy equals
		// the agent-free resolution by construction, so an origin that is not in
		// Base is not something an approval could legitimise — it means the
		// resolution disagreed with the definition, which is a refusal.
		p.record(site, o, k, mcpDecisionDenied)
		return site, "", true, p.denial(site, o, k)
	}
	return site, class, false, nil
}

// consultPersisted asks the approval store. Called with p.mu released; the
// callback does file I/O and must never run under the policy lock.
func (p *mcpEgressPolicy) consultPersisted(site mcpDefinitionSite, o Origin, k egressKind, class string) (bool, error) {
	p.mu.Lock()
	approved := p.approved
	p.mu.Unlock()
	if approved == nil {
		return false, nil
	}
	ok, err := approved(site, o, class)
	if err != nil {
		p.record(site, o, k, mcpDecisionUnavailable)
		// Fail closed: an approval store that cannot be read has not said yes.
		return false, fmt.Errorf("%w: the remembered approvals could not be read, so contacting %s could not be authorized: %v",
			mcpserver.ErrDenied, originLabel(o), err)
	}
	if ok {
		p.record(site, o, k, mcpDecisionApproved)
	}
	return ok, nil
}

func (p *mcpEgressPolicy) hasSessionGrant(site mcpDefinitionSite, o Origin, class string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.session[newSessionKey(site, o, class)]
}

func (p *mcpEgressPolicy) grantSession(site mcpDefinitionSite, o Origin, class string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.session == nil {
		p.session = map[sessionKey]bool{}
	}
	p.session[newSessionKey(site, o, class)] = true
}

// record calls the audit hook with p.mu released.
func (p *mcpEgressPolicy) record(site mcpDefinitionSite, o Origin, k egressKind, decision string) {
	p.mu.Lock()
	audit := p.audit
	p.mu.Unlock()
	if audit != nil {
		audit(site, o, k, decision)
	}
}

// approvalRequest builds the prompt payload.
//
// Only the fields types.MCPApprovalRequest has TODAY are filled. §6 widens that
// struct to carry the full site (collection, request, environment, origin, kind
// class) and the wave that lands it also enriches this constructor; until then
// the callbacks receive the origin, which is the one thing they cannot derive.
// The site, kind and class are already parameters so that widening is an edit
// to this body alone.
func (p *mcpEgressPolicy) approvalRequest(_ mcpDefinitionSite, o Origin, _ egressKind, _ string) types.MCPApprovalRequest {
	return types.MCPApprovalRequest{Host: o.String()}
}

// denial is the error an unauthorized egress produces. It names the origin, the
// definition scope and the egress kind, and it says what the fix is — an agent
// that reads "denied" and retries has learned nothing.
func (p *mcpEgressPolicy) denial(site mcpDefinitionSite, o Origin, k egressKind) error {
	return fmt.Errorf("%w: this run would contact %s as its %s destination, and nothing in request %q's definition (collection %q, environment %s) points there. Ask the user to approve that origin in LiteAPI — the app raises an approval prompt while it is open — and do not retry or work around it",
		mcpserver.ErrDenied, originLabel(o), k, site.requestID, site.collectionID, site.environmentLabel())
}

// originLabel renders an origin for a message, including the zero value.
func originLabel(o Origin) string {
	if rendered := o.String(); rendered != "" {
		return rendered
	}
	return "an unresolved destination"
}

// Refuse produces the uniform refusal of §2 — *feature — why it is unavailable
// to agent-initiated runs — the action* — for the capabilities an MCP run does
// not have at all (credential_process, non-TCP gRPC, PAC, interactive OAuth,
// cert-plus-https-proxy).
//
// A METHOD ON THE POLICY, with a nil receiver tolerated, because the refusal
// sites are all inside engine code that already holds (or does not hold) a
// policy, and reading `policy.Refuse(...)` at the site says WHY it is refusing:
// because this is an MCP run.
func (p *mcpEgressPolicy) Refuse(feature, detail string) error {
	return mcpRefusal(feature, detail)
}

// mcpRefusal is Refuse's body, available to sites that have no policy value in
// hand. One implementation, so every refusal reads the same way.
func mcpRefusal(feature, detail string) error {
	feature = strings.TrimRight(strings.TrimSpace(feature), ".")
	detail = strings.TrimSpace(detail)
	if feature == "" {
		feature = "this capability"
	}
	if detail == "" {
		detail = "Run this request in the LiteAPI app."
	}
	return fmt.Errorf("%w: %s. Agent-initiated runs cannot use it. %s",
		mcpserver.ErrDenied, feature, detail)
}

// --- the execution overlay (§3) -------------------------------------------

// mcpExecutionOverlay is where an MCP execution's script- and response-driven
// variable mutations and cookies live INSTEAD of AppState.
//
// WHY IT EXISTS AT ALL. The send tail persists every dirty variable scope and
// merges cookies into state. That is a confirmed laundering channel: a script
// derives a hostname from agent input, persists it, and the NEXT run reads it
// back as definition state — so it enters Base, and the boundary has been
// widened by the agent. Not persisting kills that at the root, structurally:
// Base derives only from AppState reads, and nothing here reaches AppState.
//
// WHY NOT SIMPLY DISCARD. A literal "no persistence" would break flows, where
// step 1's bru.setVar is read by step 3 and cross-step continuity rides on
// persistence today. "The run" is the whole execution, so the deltas live here
// for its duration and die with it.
//
// STRUCTURE ONLY IN THIS WAVE. Applying deltas back into a VariableContext at
// the precedence the persisted values would have had, and extracting them after
// a send, are scripting-package concerns (ApplyRunOverlayToContext /
// DeltasFromContext) wired by a later task.
type mcpExecutionOverlay struct {
	mu sync.Mutex
	// The four scopes ApplyScriptVariableContextToState would have written:
	// runtime, environment, global and collection. Held as the same
	// map[string]interface{} shape VariableContext uses so no conversion sits
	// between what a script set and what the next send reads.
	runtime    map[string]interface{}
	env        map[string]interface{}
	global     map[string]interface{}
	collection map[string]interface{}
	// cookies is the jar snapshot the send tail would have merged into state.
	cookies []types.CookieEntry
	// cookiesRecorded distinguishes "no cookies yet" from "an empty jar", which
	// matters because an empty jar is a real state a send can produce.
	cookiesRecorded bool
}

func newMCPExecutionOverlay() *mcpExecutionOverlay {
	return &mcpExecutionOverlay{}
}

// mcpOverlayScope names which of the four variable scopes a delta belongs to.
type mcpOverlayScope int

const (
	mcpOverlayRuntime mcpOverlayScope = iota
	mcpOverlayEnv
	mcpOverlayGlobal
	mcpOverlayCollection
)

// mergeVariables folds one scope's deltas in. Later writes win, which is what
// the persisted path does too.
func (o *mcpExecutionOverlay) mergeVariables(scope mcpOverlayScope, values map[string]interface{}) {
	if o == nil || len(values) == 0 {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	target := o.scopeMapLocked(scope)
	if target == nil {
		target = map[string]interface{}{}
		o.setScopeMapLocked(scope, target)
	}
	for name, value := range values {
		target[name] = value
	}
}

// variables copies one scope's accumulated deltas out. A copy rather than the
// live map: the caller applies it to a VariableContext on another goroutine's
// send, and handing out the map would put the overlay's contents under no lock
// at all.
func (o *mcpExecutionOverlay) variables(scope mcpOverlayScope) map[string]interface{} {
	if o == nil {
		return nil
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	source := o.scopeMapLocked(scope)
	if len(source) == 0 {
		return nil
	}
	out := make(map[string]interface{}, len(source))
	for name, value := range source {
		out[name] = value
	}
	return out
}

// recordCookies stores the jar snapshot a send produced.
func (o *mcpExecutionOverlay) recordCookies(cookies []types.CookieEntry) {
	if o == nil {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	o.cookies = append([]types.CookieEntry(nil), cookies...)
	o.cookiesRecorded = true
}

// cookieSnapshot returns the jar the next send in this execution should start
// from, and whether one was ever recorded.
func (o *mcpExecutionOverlay) cookieSnapshot() ([]types.CookieEntry, bool) {
	if o == nil {
		return nil, false
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if !o.cookiesRecorded {
		return nil, false
	}
	return append([]types.CookieEntry(nil), o.cookies...), true
}

func (o *mcpExecutionOverlay) scopeMapLocked(scope mcpOverlayScope) map[string]interface{} {
	switch scope {
	case mcpOverlayRuntime:
		return o.runtime
	case mcpOverlayEnv:
		return o.env
	case mcpOverlayGlobal:
		return o.global
	case mcpOverlayCollection:
		return o.collection
	default:
		return nil
	}
}

func (o *mcpExecutionOverlay) setScopeMapLocked(scope mcpOverlayScope, values map[string]interface{}) {
	switch scope {
	case mcpOverlayRuntime:
		o.runtime = values
	case mcpOverlayEnv:
		o.env = values
	case mcpOverlayGlobal:
		o.global = values
	case mcpOverlayCollection:
		o.collection = values
	}
}

// --- provenance, explicit at the roots (§4.5) -----------------------------

// sendProvenance says who asked for a send. It is a REQUIRED argument of the
// send path in the end state, of an unexported type with exactly two
// constructors, so that "which kind of send is this" is answered at the root by
// the code that knows, and never inferred downstream.
//
// THE ZERO VALUE IS INVALID, on purpose. Inferring "UI" from a missing policy is
// the failure mode this type exists to prevent: a new engine path that forgets
// to thread provenance would then silently behave as a UI send and skip every
// refusal and every checkpoint. Instead it produces a zero value, which is
// rejected.
type sendProvenance struct {
	ui     bool
	policy *mcpEgressPolicy
}

// uiSendProvenance labels a user-initiated send: SendRequest, the collection
// runner, and the UI bindings that do engine-adjacent egress outside the send
// seam. §1.2(4) — such a send is never subjected to the destination boundary.
func uiSendProvenance() sendProvenance {
	return sendProvenance{ui: true}
}

// mcpSendProvenance labels an MCP-initiated send and carries its policy.
//
// A nil policy is rejected rather than accepted-as-permissive: an MCP send
// without a policy is a bug, and the whole point of the type is that such a bug
// cannot masquerade as a UI send.
func mcpSendProvenance(p *mcpEgressPolicy) sendProvenance {
	if p == nil {
		return sendProvenance{}
	}
	return sendProvenance{policy: p}
}

// valid reports whether this provenance was produced by one of the two
// constructors. A zero value is not.
func (p sendProvenance) valid() bool {
	return p.ui || p.policy != nil
}

// --- context plumbing -----------------------------------------------------

// mcpPolicyContextKey is the context key for the provenance. An unexported empty
// struct type, so nothing outside this package can set or read it — provenance
// arriving from another package would be provenance this package did not
// establish.
type mcpPolicyContextKey struct{}

// mcpEgressKindContextKey carries the BACKSTOP egress kind: which kind the guard
// transport should authorize a request under when it sees one on this context.
//
// WHY A SECOND KEY. The guard transport sits below http.Client and cannot tell a
// main request from a redirect hop, nor a request from an OAuth token exchange —
// it sees only a URL. The checkpoint that owns an egress knows, and it says so
// here. Main and redirect need no distinction for authority (Base(S, redirect)
// IS the scope's main set, and both are request-class), so the default is
// egressKindMain; the paths where the class genuinely differs — the OAuth token
// exchange and the AWS credential clients — narrow it explicitly.
type mcpEgressKindContextKey struct{}

// mcpContextWithPolicy labels ctx as an MCP-initiated egress governed by p.
func mcpContextWithPolicy(ctx context.Context, p *mcpEgressPolicy) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, mcpPolicyContextKey{}, mcpSendProvenance(p))
}

// mcpContextWithUIProvenance labels ctx as a user-initiated egress. The guard
// transport lets it through untouched; strict mode still requires the label to
// be present, which is what makes an unlabeled path detectable.
func mcpContextWithUIProvenance(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, mcpPolicyContextKey{}, uiSendProvenance())
}

// mcpProvenanceFromContext reads the label. The second result distinguishes "no
// label" from "a label that says UI" — strict mode needs that difference.
func mcpProvenanceFromContext(ctx context.Context) (sendProvenance, bool) {
	if ctx == nil {
		return sendProvenance{}, false
	}
	prov, ok := ctx.Value(mcpPolicyContextKey{}).(sendProvenance)
	if !ok || !prov.valid() {
		return sendProvenance{}, false
	}
	return prov, true
}

// mcpPolicyFromContext returns the governing policy, or nil for a UI send or an
// unlabeled context. A nil policy is permissive by construction (see Authorize),
// so this is safe to call unconditionally at a checkpoint.
func mcpPolicyFromContext(ctx context.Context) *mcpEgressPolicy {
	prov, ok := mcpProvenanceFromContext(ctx)
	if !ok {
		return nil
	}
	return prov.policy
}

// mcpContextWithEgressKind narrows the backstop kind for a sub-path.
func mcpContextWithEgressKind(ctx context.Context, k egressKind) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, mcpEgressKindContextKey{}, k)
}

// mcpBackstopEgressKind is the kind the guard transport authorizes under.
func mcpBackstopEgressKind(ctx context.Context) egressKind {
	if ctx == nil {
		return egressKindMain
	}
	if k, ok := ctx.Value(mcpEgressKindContextKey{}).(egressKind); ok && k != "" {
		return k
	}
	return egressKindMain
}

// --- the guard transport (§4.3 item 3) ------------------------------------

// mcpStrictEgressProvenance flips unlabeled egress from "allowed" to "refused".
//
// OFF UNTIL THE FINAL WAVE, deliberately. Every intermediate wave leaves some
// engine path unlabeled, and refusing those before they are labeled would break
// the app rather than harden it. Flipping it last turns "a path nobody
// remembered to label" from an invisible hole into a loud, test-visible refusal.
var mcpStrictEgressProvenance = false

// mcpEgressGuardTransport is the backstop wrapped around exactly the three
// clients that carry engine egress: the per-send copy in executeHTTP, the shared
// credential client (OAuth2 and, through awsv4.SetHTTPClient, all AWS credential
// HTTP), and the script runtime's client.
//
// WHY A ROUNDTRIPPER AND NOT MORE CHECKPOINTS. http.Client invokes RoundTrip
// once per REDIRECT HOP, and the digest retry is a second Do whose cloned
// request preserves the context — so wrapping the transport covers hops and
// retries with no per-site code, and covers paths that do not exist yet. It is a
// backstop, not the primary check: the blocking checkpoints run first and can
// prompt, which this cannot.
type mcpEgressGuardTransport struct {
	base http.RoundTripper
}

// newMCPEgressGuardTransport wraps base. A nil base means http.DefaultTransport,
// matching http.Client's own rule.
func newMCPEgressGuardTransport(base http.RoundTripper) http.RoundTripper {
	return mcpEgressGuardTransport{base: base}
}

func (t mcpEgressGuardTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	prov, labeled := mcpProvenanceFromContext(req.Context())
	switch {
	case labeled && prov.policy != nil:
		origin, ok := originOfParsedURL(req.URL)
		if !ok {
			return nil, fmt.Errorf("%w: this run tried to contact %q, which is not an http(s) or ws(s) destination LiteAPI can check; run it in the LiteAPI app",
				mcpserver.ErrDenied, req.URL.Redacted())
		}
		if err := prov.policy.AuthorizeNoPrompt(origin, mcpBackstopEgressKind(req.Context())); err != nil {
			return nil, err
		}
	case labeled:
		// A UI send. §1.2(4): never subjected to any of this.
	case mcpStrictEgressProvenance:
		return nil, fmt.Errorf("%w: an engine request to %q carried no send provenance, so LiteAPI could not tell whether it belonged to an agent run; this is a bug in LiteAPI — report it rather than retrying",
			mcpserver.ErrDenied, req.URL.Redacted())
	}
	return base.RoundTrip(req)
}
