package core

// run_request: the Phase 2 run tier of the MCP agent interface.
//
// THE POINT OF THIS TIER is that an agent can execute a call it could never
// reconstruct. The read tier hands it the definition with {{templates}} intact
// and no secret values, which is exactly enough to understand a request and not
// enough to make it: the credential exists only inside this process. Running it
// HERE resolves the secret at send time, inside LiteAPI, and hands back a
// response with the value masked out again.
//
// IT RUNS THE APP'S OWN ENGINE, NOT A COPY OF IT. This file calls
// sendRequestWithControlsContextProvenance (app_send.go) — the same function
// SendRequest calls, with the same arguments and the same zero-value iteration.
// That is not convenience, it is the correctness argument for the whole tier:
// pre/post scripts, tests, TLS posture, client certificates, cookies, OAuth2
// token refresh, history recording and the response store all happen because
// this is literally the user's send path. A parallel implementation that called
// executeHTTP directly would drift from it silently and would tell the agent
// something the app does not do.
//
// LOCKING. Nothing here holds a.mu across the send: the engine takes and
// releases the state lock itself, twice, around the network I/O (see the US-076
// note in app_send.go). The one thing this file does under the lock is copy the
// definition it needs for validation and for the policy.
//
// TWO BOUNDARIES, ASKING DIFFERENT QUESTIONS. The READ boundary — may the
// agent's own inputs put a credential into this run — is mcp_guard.go's
// secret-injection refusal, run once before anything else. The DESTINATION
// boundary — may this execution contact that origin — is mcp_policy.go, built
// here from the same plan and enforced at every egress the run makes rather than
// once before it. The approval prompt either can raise lives in
// mcp_approvals.go.

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mutexdev/lite_api/internal/history"
	"github.com/mutexdev/lite_api/internal/mcpserver"
	"github.com/mutexdev/lite_api/internal/runner"
	"github.com/mutexdev/lite_api/internal/scripting"
	"github.com/mutexdev/lite_api/internal/types"
)

// mcpRunBodyLimit bounds the response body one run returns.
//
// Larger than the history limit (100000) because this is the body the agent
// asked for by running the request, rather than an incidental artifact of a
// listing — and still bounded, because an agent pays tokens for every byte and a
// paginated endpoint that answers with 40MB would otherwise be delivered in
// full. Truncated says when it was cut, so the agent knows to narrow the query
// rather than assuming it saw everything.
const mcpRunBodyLimit = 300000

// mcpRunPlan is everything the guard and the run need, copied out from under the
// state lock before either does any work.
//
// effective and vars are built the way the send path builds them —
// scripting.EffectiveRequest for the folder/collection merge, and
// NewScriptVariableContext for the resolved variable scope — so "what the guard
// checked" and "what the engine sends" are derived from the same two functions
// rather than from two similar-looking walks.
type mcpRunPlan struct {
	collectionID  string
	requestID     string
	environmentID string
	requestName   string
	effective     types.RequestItem
	vars          map[string]string
	// site is "Site of S" (§1.1): the identity every approval for this run is
	// keyed on. Fixed inside the locked read below, so the environment identity
	// a run is judged under is the one it started with.
	site mcpDefinitionSite
	// labels is the human half of the site — what a prompt shows. Never part of
	// any key (§6).
	//
	// ITS ADVISORY SECRET LIST IS THE REQUEST'S, WHOLE. There used to be a
	// narrowing step (promptLabels) that trimmed it to the secrets a particular
	// prompt was about, which made sense while the prompt came from a guard
	// that had decided ABOUT a credential. The destination boundary decides
	// about an origin and knows nothing about credentials, so the honest
	// advisory line is "this request references these secrets" — every prompt
	// for this site says the same thing, because every prompt is about the same
	// question.
	labels mcpSiteLabels
	// scope is Base(S, k) for this definition scope: the origins the stored
	// definition points at, resolved under the run's SINGLE agent-free variable
	// context. Nothing agent-supplied contributes to it.
	scope mcpScopeOrigins
	// secretsInScope is every variable name that resolves to a secret for this
	// run. It answers both questions this file asks: which overrides must be
	// refused (item 3 of the contract) and which secret names a prompt's
	// advisory list should mention.
	secretsInScope map[string]bool
	// workspacePath is the workspace the run's collection lives in. It is also
	// site.workspacePath; kept as its own field because the variable context is
	// built from it before the site exists.
	workspacePath string
}

// RunRequest executes one stored request through the app's own send path.
func (b *mcpBackend) RunRequest(ctx context.Context, params mcpserver.RunRequestParams) (mcpserver.RunResult, error) {
	plan, err := b.app.mcpRunPlan(params.CollectionID, params.RequestID, params.EnvironmentID)
	if err != nil {
		return mcpserver.RunResult{}, err
	}
	overrides, err := mcpValidatedOverrides(params.Variables, plan.secretsInScope)
	if err != nil {
		return mcpserver.RunResult{}, err
	}
	// The known secret VALUES, fetched before the guard so the mapping below
	// cannot forget to scrub. Post-interpolation artifacts — the resolved URL,
	// response headers, the body — are where name-based masking cannot help: a
	// resolved secret sits under whatever name the user chose, and only an exact
	// value match finds it. Same argument as GetHistory's. The guard needs them
	// too, for the backstop that catches an override resolving to a credential
	// no name walk could have reached.
	secretValues, err := b.app.mcpHydratedSecretValues()
	if err != nil {
		return mcpserver.RunResult{}, err
	}
	// The destination policy for this execution (§4.6), attached to the context
	// the send path will carry. It IS the boundary: every egress this run makes
	// is authorized against the scope built here, at the egress itself.
	policy, _ := b.app.mcpEgressPolicyForRun(plan)
	ctx = mcpContextWithPolicy(ctx, policy)

	// The read boundary, before any destination question. EVERY override is
	// agent-supplied here: run_request's whole variables map is the agent's own
	// input, which is why both fields carry it.
	if err := b.app.enforceMCPSecretInjection(plan, mcpGuardInput{
		overrides:    overrides,
		agentValues:  overrides,
		secretValues: secretValues,
	}); err != nil {
		return mcpserver.RunResult{}, err
	}

	// promptValues nil, index nil: an agent cannot answer a pm.prompt and has no
	// per-run lookup index to offer. iteration carries ONLY the overrides — see
	// mcpValidatedOverrides for why that field is the right seam.
	//
	// mcpSendProvenance(policy) SAYS WHAT THIS IS (§4.5). The context still
	// carries the policy — the checkpoints, the guard transport and the script
	// shims read it there — but the send path is TOLD, by argument, that this is
	// an agent-initiated run governed by this policy. Dropping the label can no
	// longer downgrade the run to a UI send; it fails the root's check instead.
	_, _, response, err := b.app.sendRequestWithControlsContextProvenance(
		ctx, mcpSendProvenance(policy), plan.collectionID, plan.requestID, plan.environmentID, nil, nil,
		runner.Iteration{Data: overrides},
	)
	if err != nil {
		return mcpserver.RunResult{}, err
	}
	if response == nil {
		return mcpserver.RunResult{}, errors.New("the run produced no response; this is a bug in LiteAPI, not something to retry")
	}
	result, runResultErr := mcpRunResult(*response, secretValues)
	return result, mcpClassifyRunFailure(runResultErr, policy)
}

// mcpClassifyRunFailure re-attaches the ErrDenied CLASS to a run that failed
// because the destination policy refused something.
//
// WHY IT IS NEEDED. The engine's own checkpoints — the main HTTP one and the
// guard transport under it — live inside executeHTTP, which reports every
// failure by writing a string into Response.Error. By the time the refusal
// reaches here it is text, and §1.2's promise that a denial arrives as "an
// ErrDenied-class error" would silently stop holding for exactly the egresses
// this phase added. The policy remembers that it refused, so the class can be
// restored without inspecting the message.
//
// WHY NOT fmt.Errorf("%w: …", ErrDenied). The refusal has already been rendered
// once, "denied:" and all; wrapping would print it twice. mcpDeniedRunError
// carries the class and leaves the message alone.
func mcpClassifyRunFailure(err error, policy *mcpEgressPolicy) error {
	if err == nil || errors.Is(err, mcpserver.ErrDenied) || !policy.refusedAnyEgress() {
		return err
	}
	return mcpDeniedRunError{err: err}
}

// mcpClassifyFlowDenial is the flow's half of the same problem, and it exists
// because a flow reports a refused step differently from a refused run.
//
// WHAT GOES WRONG WITHOUT IT. RunFlow's contract, stated on RunFlow itself, is
// that a non-nil error means the run was REFUSED and a nil error with OK false
// means the flow RAN and failed its own checks. A step whose egress the policy
// blocked is a refusal, but the flow runner sees only a step that could not
// complete: it records the text on the step, marks the run not-OK, and returns
// no error. The agent would then be told "your flow failed an assertion" about a
// boundary decision, the audit would record `error` rather than `denied`, and
// §1.2's promise that a denial arrives as an ErrDenied-class error would hold
// for run_request and quietly not for run_flow.
//
// THE POLICY IS THE WITNESS, not the message. Matching on the text would break
// the first time a refusal was reworded; the policy remembers that it refused.
// Fail-fast means at most one step can have been refused, so the flow's own
// top-level error — which quotes that step — is the right text to carry.
func mcpClassifyFlowDenial(result types.FlowRunResult, policy *mcpEgressPolicy) error {
	if result.OK || !policy.refusedAnyEgress() {
		return nil
	}
	message := strings.TrimSpace(result.Error)
	if message == "" {
		message = "the flow was stopped because one of its steps would have contacted an origin this run is not authorized to reach"
	}
	return mcpDeniedRunError{err: errors.New(message)}
}

// mcpDeniedRunError is a refusal whose message was produced elsewhere: Error()
// is the text the agent reads, Unwrap() is what errors.Is matches.
type mcpDeniedRunError struct{ err error }

func (e mcpDeniedRunError) Error() string { return e.err.Error() }

func (e mcpDeniedRunError) Unwrap() error { return mcpserver.ErrDenied }

// mcpRunPlan validates the ids and copies the definition the run needs.
//
// Errors name the field and the fix, matching the other Backend methods: an id
// is the user's own and echoing it back is what makes the error actionable.
func (a *App) mcpRunPlan(collectionID, requestID, environmentID string) (mcpRunPlan, error) {
	collectionID = strings.TrimSpace(collectionID)
	requestID = strings.TrimSpace(requestID)
	environmentID = strings.TrimSpace(environmentID)
	if collectionID == "" || requestID == "" {
		return mcpRunPlan{}, errors.New("collectionId and requestId are both required")
	}

	var collection types.Collection
	var item types.RequestItem
	var globals []types.Environment
	var workspacePath string
	var environmentName string
	collectionFound, itemFound, environmentFound := false, false, environmentID == ""
	globalEnvironmentMatch := false
	if err := a.readStateForMCP(func(state *AppState) {
		for wi := range state.Workspaces {
			workspace := &state.Workspaces[wi]
			for _, environment := range workspace.GlobalEnvironments {
				if environmentID != "" && environment.ID == environmentID {
					globalEnvironmentMatch = true
				}
			}
			for ci := range workspace.Collections {
				if workspace.Collections[ci].ID != collectionID {
					continue
				}
				collectionFound = true
				collection = mcpCollectionCopy(workspace.Collections[ci])
				globals = mcpEnvironmentCopies(scripting.ActiveGlobalEnvironmentsForWorkspace(*workspace))
				workspacePath = workspace.Path
				for ii := range collection.Items {
					if collection.Items[ii].ID == requestID {
						item = collection.Items[ii]
						itemFound = true
						break
					}
				}
				for _, environment := range collection.Environments {
					if environment.ID == environmentID {
						environmentFound = true
						environmentName = environment.Name
						break
					}
				}
				return
			}
		}
	}); err != nil {
		return mcpRunPlan{}, err
	}
	if !collectionFound {
		return mcpRunPlan{}, fmt.Errorf("no collection with id %q; call list_collections for the ids that exist", collectionID)
	}
	if !itemFound {
		return mcpRunPlan{}, fmt.Errorf("no request with id %q in collection %q; call list_requests for the ids that exist", requestID, collectionID)
	}
	if !environmentFound {
		// A GLOBAL environment id gets its own message. It is a real id the
		// agent read from list_environments, so "no such environment" would be
		// both wrong and unactionable — the honest answer is that the global
		// selection is the user's, made in the app, and is applied to every run
		// anyway.
		if globalEnvironmentMatch {
			return mcpRunPlan{}, fmt.Errorf("environmentId %q names a global environment, which cannot be selected per run; the workspace's active global environment already applies. Pass a collection environment from list_environments, or omit environmentId", environmentID)
		}
		// "or omit environmentId to use the active one" is what this used to say,
		// and it was FALSE. The collection-environment selection the user makes
		// in the app lives in the WebView's localStorage
		// (frontend/src/lib/environmentSelection.ts) and never reaches AppState,
		// so this process cannot read it: omitting environmentId applies NO
		// collection environment at all. An agent told otherwise would run
		// against an empty scope believing it had the user's, which is exactly
		// the kind of quiet wrongness §1.4 exists to refuse.
		return mcpRunPlan{}, fmt.Errorf("no environment with id %q in collection %q; call list_environments for the ids that exist, or omit environmentId to run with no collection environment", environmentID, collectionID)
	}

	// THE RUN'S ENVIRONMENT IDENTITY, fixed from the one locked read above: the
	// selected collection environment and the ordered list of global
	// environments that were active when the run started. §1.1 makes the global
	// half a LIST even though ActiveGlobalEnvironmentsForWorkspace yields at
	// most one today, so a future multi-active model changes the key rather than
	// silently widening approvals made under the single-active one.
	site := mcpDefinitionSite{
		workspacePath:        workspacePath,
		collectionID:         collectionID,
		requestID:            requestID,
		environmentID:        environmentID,
		globalEnvironmentIDs: mcpEnvironmentIDs(globals),
	}

	// THE RUN'S SINGLE AGENT-FREE VARIABLE CONTEXT (§4.1). One construction, one
	// environment configuration, no overrides, no flow inputs, no extracted
	// values — and Base is derived from it and from nothing else, which is what
	// makes an approval environment-exact. A union over the collection's
	// environments would authorize a dev host for a run holding production
	// credentials, which is precisely the mistake the boundary exists to catch.
	effective := scripting.EffectiveRequest(collection, item)
	variables := scripting.NewScriptVariableContext(globals, &collection, environmentID, effective, nil, workspacePath)
	secretsInScope := mcpSecretNamesInScope(globals, collection, environmentID, item)

	// RESOLVED OUTSIDE THE LOCKED READ, and it has to be: readStateForMCP holds
	// a.mu for writing, while collectionProxyResolution takes a.mu.RLock, so
	// calling it in there would deadlock. Everything it reads is either a copy
	// taken above or the collection's own proxy configuration, which no agent
	// input can reach.
	scope := mcpDefinitionOrigins(mcpDefinitionOriginsInput{
		site:      site,
		effective: effective,
		vars:      variables.Combined,
		proxy:     a.collectionProxyResolution(collectionID),
	})

	return mcpRunPlan{
		collectionID:  collectionID,
		requestID:     requestID,
		environmentID: environmentID,
		requestName:   item.Name,
		effective:     effective,
		vars:          variables.Combined,
		site:          site,
		labels: mcpSiteLabels{
			runLabel:               item.Name,
			collectionName:         collection.Name,
			requestName:            item.Name,
			environmentName:        environmentName,
			globalEnvironmentNames: mcpEnvironmentNames(globals),
			advisorySecretNames:    mcpReferencedSecrets(effective, secretsInScope),
		},
		scope:          scope,
		secretsInScope: secretsInScope,
		workspacePath:  workspacePath,
	}, nil
}

// mcpEnvironmentIDs and mcpEnvironmentNames project the active global
// environments, IN ORDER. Order is preserved rather than sorted because §1.1
// makes the list itself the identity: a reordered but equivalent list produces a
// different key and therefore one conservative re-prompt, which is the safe
// direction, whereas sorting would quietly make two different configurations
// share an approval.
func mcpEnvironmentIDs(environments []types.Environment) []string {
	out := make([]string, 0, len(environments))
	for _, environment := range environments {
		out = append(out, environment.ID)
	}
	return out
}

func mcpEnvironmentNames(environments []types.Environment) []string {
	out := make([]string, 0, len(environments))
	for _, environment := range environments {
		out = append(out, environment.Name)
	}
	return out
}

// mcpEgressPolicyForRun builds the one policy that governs an MCP-initiated
// execution (§4.6): its scope stack, its approval callbacks, and its execution
// overlay.
//
// NOTHING CONSULTS IT YET. This wave attaches the policy to the run's context;
// the engine checkpoints and the guard transport that read it land in the next
// one, and until then the shipped host guard is still the enforcing boundary.
// Building it here regardless is what makes that next wave a wiring change
// rather than a design change — and it means the approval store, the prompt and
// the key are exercised now, by the guard that is enforcing.
//
// The label book is per-execution because a flow replaces its scope per step:
// each step records its own names before setting its scope, so a prompt raised
// during step 3 describes step 3.
func (a *App) mcpEgressPolicyForRun(plan mcpRunPlan) (*mcpEgressPolicy, *mcpSiteLabelBook) {
	policy, book := a.newMCPExecutionPolicy()
	mcpEnterScope(policy, book, plan)
	return policy, book
}

// mcpEnterScope makes one definition scope the active one: its labels are
// recorded first, so a prompt raised the instant the scope becomes active can
// already name it.
//
// SetScope REPLACES rather than pushes, which is what a flow step needs (§4.1):
// steps are siblings, and step B must not inherit step A's origins.
func mcpEnterScope(policy *mcpEgressPolicy, book *mcpSiteLabelBook, plan mcpRunPlan) {
	book.record(plan.site, plan.labels)
	policy.SetScope(plan.scope)
}

// newMCPExecutionPolicy builds the callbacks half — everything that does not
// depend on which definition scope is active. A flow calls this once and then
// sets a scope per step.
func (a *App) newMCPExecutionPolicy() (*mcpEgressPolicy, *mcpSiteLabelBook) {
	policy := newMCPEgressPolicy()
	book := newMCPSiteLabelBook()

	policy.approved = a.mcpRememberedOriginApproved
	policy.describe = func(site mcpDefinitionSite, origin Origin, kind egressKind, class string) types.MCPApprovalRequest {
		return mcpApprovalRequestFor(site, book.lookup(site), origin, kind, class)
	}
	// The blocking prompt. Allow-once is the outcome for any approval, because
	// remembering is ResolveMCPApproval's own job — it persists before releasing
	// the waiter, so by the time this returns the approval is already on disk if
	// the user asked for that.
	policy.prompt = func(ctx context.Context, request types.MCPApprovalRequest) mcpPromptOutcome {
		if a.requestMCPApproval(ctx, request) {
			return mcpPromptAllowOnce
		}
		return mcpPromptDeny
	}
	// The NON-BLOCKING prompt, for the transport backstop that cannot wait
	// inside client.Timeout (§4.2). It raises the same dialog on its own
	// goroutine so an approve-and-remember makes the agent's retry succeed; the
	// egress that triggered it has already been denied by then. Its own context
	// rather than the run's: the run is on its way to failing, and the question
	// the user is being asked outlives it.
	policy.notify = func(request types.MCPApprovalRequest) {
		go func() { _ = a.requestMCPApproval(context.Background(), request) }()
	}
	return policy, book
}

// mcpValidatedOverrides checks the run's variable overrides and returns the set
// to apply.
//
// AN OVERRIDE OF A SECRET IS REFUSED. It is the one shape that inverts the whole
// boundary: an agent that cannot READ a secret could otherwise WRITE one — set
// {{apiToken}} to a value of its choosing, send the request to a host it
// controls, and learn nothing new, or set it to a value it wants smuggled into a
// field it can read back. Neither is a legitimate use, and the tool description
// says so, but a tool description is not an enforcement point. The check is by
// NAME only, which is the simple and complete rule here: the agent's own
// variables are values it already knows, and the only names whose values it
// cannot legitimately know are the ones marked secret.
//
// HOW OVERRIDES ARE APPLIED (the decision worth reading): they travel as
// runner.Iteration.Data, the data-file row seam (US-046,
// scripting.ApplyIterationDataToContext at scripting.go:2410), which
// the send path applies itself at its head. That seam fits the requirement
// exactly, and it was preferred to layering a map over Combined for two
// reasons:
//
//   - PRECEDENCE IS ALREADY RIGHT. VariableContext.Recompute (scripting.go:2474
//     onward) places Data above Global, Collection, Env, Folder and Request, and
//     below Runtime and Prompt. So an override beats the environment value, which
//     is what the contract asks for, while a value a pre-request script
//     deliberately set with bru.setVar during THIS run still wins — which is also
//     right, and is behaviour a hand-rolled overlay would have got wrong.
//   - IT CANNOT PERSIST. ApplyScriptVariableContextToState (variables.go:366)
//     writes back only the Runtime, Env, Global and Collection scopes, each
//     behind its own dirty flag. Data is not one of them and has no dirty flag,
//     so there is no code path by which an override reaches disk. "For this run
//     only" is therefore a property of the seam rather than a promise this file
//     has to keep.
func mcpValidatedOverrides(variables map[string]string, secretsInScope map[string]bool) (map[string]string, error) {
	if len(variables) == 0 {
		return nil, nil
	}
	overrides := make(map[string]string, len(variables))
	var refused []string
	for name, value := range variables {
		trimmed := strings.TrimSpace(name)
		if trimmed == "" {
			continue
		}
		if secretsInScope[trimmed] {
			refused = append(refused, trimmed)
			continue
		}
		overrides[trimmed] = value
	}
	if len(refused) > 0 {
		// The NAMES, never the value the agent sent — echoing that back would
		// put an agent-chosen string in the audit log and the app's UI, and the
		// name alone is what makes the refusal actionable.
		return nil, fmt.Errorf("%w: %s names a secret variable, and secrets cannot be overridden for a run; reference it by name and let LiteAPI resolve it, or ask the user to change its value",
			mcpserver.ErrDenied, mcpJoinSecretNames(mcpSortedNames(refused)))
	}
	if len(overrides) == 0 {
		return nil, nil
	}
	return overrides, nil
}

// mcpRunResult maps the engine's Response onto the frozen contract.
func mcpRunResult(response Response, secretValues []string) (mcpserver.RunResult, error) {
	// A cancelled run is not a result. The agent asked for a request that never
	// completed, and returning a zero-status RunResult would read as "the server
	// answered with nothing" — which is a different and much more confusing
	// thing than "this was cancelled".
	if response.Cancelled {
		return mcpserver.RunResult{}, errors.New("the run was cancelled before it completed")
	}
	// Status 0 with an error means the request never reached a server: DNS,
	// TLS, a refused connection, or a pre-request script that failed. A response
	// that DID arrive keeps its status even at 500 — that is a real answer and
	// the agent needs to see it.
	if response.Status == 0 && strings.TrimSpace(response.Error) != "" {
		return mcpserver.RunResult{}, fmt.Errorf("the request could not be completed: %s",
			mcpserver.MaskKnownSecretValues(response.Error, secretValues))
	}

	body := mcpserver.MaskKnownSecretValues(response.Body, secretValues)
	truncated := false
	// Masked BEFORE truncating, deliberately: cutting first could leave the tail
	// of a secret at the boundary as an unmatched fragment that the masker then
	// never sees.
	if len(body) > mcpRunBodyLimit {
		body = body[:mcpRunBodyLimit]
		truncated = true
	}

	result := mcpserver.RunResult{
		Status:     response.Status,
		StatusText: response.StatusText,
		DurationMs: int(response.DurationMs),
		// Now rather than response.SentAt: this stamps when the agent got its
		// answer, in UTC, which is the only clock a client on the other side of
		// an MCP connection can reason about.
		ExecutedAt: time.Now().UTC().Format(time.RFC3339),
		// The RESOLVED URL, so both defences apply: the name-based query-literal
		// masking that protects a pasted ?api_key=..., and the exact-value scrub
		// that catches a {{secret}} that has since been interpolated into it.
		URL:         mcpserver.MaskKnownSecretValues(mcpserver.RedactURLQueryLiterals(response.RequestedURL), secretValues),
		Headers:     mcpRunHeaders(response, secretValues),
		Body:        body,
		Truncated:   truncated,
		TestResults: mcpRunTestResults(response.TestResults, secretValues),
	}
	return result, nil
}

// mcpRunHeaders redacts the response headers exactly as history does, then
// scrubs known secret values on top.
//
// Two layers because they catch different things, the same pairing GetHistory
// uses. history.RedactHeaders masks by NAME (rule 3's Authorization, Cookie,
// Set-Cookie, *api-key*, *token*); the value scrub catches a server that echoed
// the credential back under a header name no heuristic would flag.
//
// HeaderEntries first when the response carries them: it is the ordered list
// with duplicates preserved (several Set-Cookie headers are one response's
// normal shape), while the Headers map has already collapsed them.
func mcpRunHeaders(response Response, secretValues []string) []mcpserver.KeyValue {
	rows := response.HeaderEntries
	if len(rows) == 0 {
		rows = history.HeaderMapRows(response.Headers)
	}
	redacted, _ := history.RedactHeaders(rows)
	return mcpMaskRowValues(mcpKeyValueRows(redacted), secretValues)
}

// mcpRunTestResults maps the scripted test outcomes.
//
// They arrive on the RESPONSE rather than on the item: app_send.go appends every
// phase's results to response.TestResults — pre-request tests first (app_send.go:187),
// then post-response variables and script failures, then the tests block itself
// (app_send.go:174). The item's copy is the same slice, assigned at
// app_send.go:240; reading the response is reading it one step earlier and
// without a second lookup.
func mcpRunTestResults(results []TestResult, secretValues []string) []mcpserver.TestResult {
	if len(results) == 0 {
		return nil
	}
	out := make([]mcpserver.TestResult, 0, len(results))
	for _, result := range results {
		out = append(out, mcpserver.TestResult{
			// Name and Message are value-scrubbed like the body and the URL.
			// Rule 3's exemption for scripts covers their SOURCE — text that
			// has to keep parsing — not their output: a test that read a secret
			// with pm.variables.get and put it in its own name or failure
			// message is runtime string output, and the exact-value scrub is
			// the same non-mangling operation the body already gets. Without
			// it, this was the one field a properly-templated secret could
			// still leak through (found by the run tier's adversarial pass).
			Name:    mcpserver.MaskKnownSecretValues(result.Name, secretValues),
			Passed:  result.Passed,
			Message: mcpserver.MaskKnownSecretValues(result.Message, secretValues),
		})
	}
	return out
}

// mcpSortedNames sorts and dedupes, so an error naming several variables reads
// the same way on every run rather than in Go's map order.
func mcpSortedNames(names []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(names))
	for _, name := range names {
		if seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
