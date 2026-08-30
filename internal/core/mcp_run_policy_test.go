package core

// Policy construction for an MCP-initiated execution — §4.1, §4.6 and §9's T2.
//
// THE PROPERTY UNDER TEST IS AN ABSENCE. Base(S, k) is built from the run's
// SINGLE agent-free variable context, and what makes that safe is what is NOT in
// it: no overrides, no flow inputs, and — the case these tests exist for — no
// other environment. A union over the collection's environments would put the
// dev host in a production run's Base, so a production credential could be sent
// to the dev host with no prompt at all. That failure is silent by construction:
// the run just succeeds.

import (
	"context"
	"testing"

	"github.com/mutexdev/lite_api/internal/mcpserver"
	"github.com/mutexdev/lite_api/internal/types"
)

const (
	policyProdBase = "https://prod.example.com"
	policyDevBase  = "http://dev.example.invalid:8080"
)

// twoEnvironmentApp is one collection, one request, and two environments whose
// {{baseUrl}} point at DIFFERENT places. Everything below turns on that
// difference.
type twoEnvironmentApp struct {
	app           *App
	workspacePath string
	collectionID  string
	requestID     string
	prodEnvID     string
	devEnvID      string
	globalEnvID   string
}

func newTwoEnvironmentApp(t *testing.T) twoEnvironmentApp {
	t.Helper()
	app := newAppForTest(t)
	if _, err := app.GetState(); err != nil {
		t.Fatalf("GetState: %v", err)
	}

	app.mu.Lock()
	defer app.mu.Unlock()
	workspace := &app.state.Workspaces[0]
	collection := &workspace.Collections[0]

	item := types.NewRequestItem("Charge card", "http", len(collection.Items)+1)
	item.Method = "POST"
	item.URL = "{{baseUrl}}/charge"
	item.Headers = []KeyValue{{Name: "Authorization", Value: "Bearer {{apiToken}}", Enabled: true}}
	item.Body = types.RequestBody{Mode: "none"}
	collection.Items = append(collection.Items, item)

	collection.Environments = append(collection.Environments,
		Environment{
			ID:   "env_production",
			Name: "Production",
			Variables: []Variable{
				{ID: "v-prod-base", Name: "baseUrl", Value: policyProdBase, Enabled: true},
				{ID: "v-prod-token", Name: "apiToken", Value: "PROD-SENTINEL-TOKEN-VALUE", Enabled: true, Secret: true},
			},
		},
		Environment{
			ID:   "env_dev",
			Name: "Dev",
			Variables: []Variable{
				{ID: "v-dev-base", Name: "baseUrl", Value: policyDevBase, Enabled: true},
				{ID: "v-dev-token", Name: "apiToken", Value: "DEV-SENTINEL-TOKEN-VALUE", Enabled: true, Secret: true},
			},
		},
	)

	workspace.GlobalEnvironments = append(workspace.GlobalEnvironments, Environment{
		ID:        "env_global_team",
		Name:      "Team-Globals",
		Variables: []Variable{{ID: "v-global-trace", Name: "trace", Value: "1", Enabled: true}},
	})
	workspace.ActiveGlobalEnvironmentID = "env_global_team"

	return twoEnvironmentApp{
		app:           app,
		workspacePath: workspace.Path,
		collectionID:  collection.ID,
		requestID:     item.ID,
		prodEnvID:     "env_production",
		devEnvID:      "env_dev",
		globalEnvID:   "env_global_team",
	}
}

func (f twoEnvironmentApp) plan(t *testing.T, environmentID string) mcpRunPlan {
	t.Helper()
	plan, err := f.app.mcpRunPlan(f.collectionID, f.requestID, environmentID)
	if err != nil {
		t.Fatalf("mcpRunPlan(%q): %v", environmentID, err)
	}
	return plan
}

// --- Base is environment-exact ----------------------------------------------

// §4.1: "This is never a union over environments: a run holding production
// credentials has exactly production's origins, and a dev-only origin is outside
// Base and prompts."
func TestMCPRunPlanBaseIsEnvironmentExact(t *testing.T) {
	fixture := newTwoEnvironmentApp(t)

	production := fixture.plan(t, fixture.prodEnvID)
	prodOrigin := mustOrigin(t, policyProdBase)
	devOrigin := mustOrigin(t, policyDevBase)

	if !production.scope.allows(prodOrigin, egressKindMain) {
		t.Fatalf("Base under production does not hold its own origin: %v", originStrings(production.scope.perKind[egressKindMain]))
	}
	// THE ASSERTION THIS FILE EXISTS FOR. The dev host is reachable only under
	// the dev environment; a production run must not have it in Base.
	if production.scope.allows(devOrigin, egressKindMain) {
		t.Error("Base built under production holds the dev environment's origin; approvals would be unioned across environments")
	}

	// And symmetrically, so the result is not an artefact of which environment
	// happened to be listed first.
	dev := fixture.plan(t, fixture.devEnvID)
	if !dev.scope.allows(devOrigin, egressKindMain) {
		t.Errorf("Base under dev does not hold its own origin: %v", originStrings(dev.scope.perKind[egressKindMain]))
	}
	if dev.scope.allows(prodOrigin, egressKindMain) {
		t.Error("Base built under dev holds the production origin")
	}

	// Redirect and script inherit the SCOPE'S main set, not the union of every
	// environment's — same-origin totality (§1.4(2)) is about one origin, not
	// about one collection.
	if production.scope.allows(devOrigin, egressKindRedirect) || production.scope.allows(devOrigin, egressKindScript) {
		t.Error("the redirect/script sets leaked the other environment's origin")
	}
}

// With no collection environment selected, {{baseUrl}} resolves to nothing at
// all — so Base is EMPTY and every destination prompts. Fail-closed: nothing
// resolved, so nothing was checked.
func TestMCPRunPlanBaseIsEmptyWhenNothingResolves(t *testing.T) {
	fixture := newTwoEnvironmentApp(t)
	plan := fixture.plan(t, "")

	if len(plan.scope.perKind[egressKindMain]) != 0 {
		t.Errorf("Base with no environment selected holds %v; an unresolved URL must contribute no origin",
			originStrings(plan.scope.perKind[egressKindMain]))
	}
}

// --- the site ----------------------------------------------------------------

// The site is fixed from the run's own locked read: the selected collection
// environment and the ORDERED list of active global environments. Every field
// is one an approval is keyed on, so a wrong one here is an approval keyed to
// the wrong thing.
func TestMCPRunPlanFixesTheRunsEnvironmentIdentity(t *testing.T) {
	fixture := newTwoEnvironmentApp(t)
	plan := fixture.plan(t, fixture.prodEnvID)

	if plan.site.workspacePath != fixture.workspacePath {
		t.Errorf("site workspace = %q, want %q", plan.site.workspacePath, fixture.workspacePath)
	}
	if plan.site.collectionID != fixture.collectionID {
		t.Errorf("site collection = %q, want %q", plan.site.collectionID, fixture.collectionID)
	}
	if plan.site.requestID != fixture.requestID {
		t.Errorf("site request = %q, want %q", plan.site.requestID, fixture.requestID)
	}
	if plan.site.environmentID != fixture.prodEnvID {
		t.Errorf("site environment = %q, want %q", plan.site.environmentID, fixture.prodEnvID)
	}
	if len(plan.site.globalEnvironmentIDs) != 1 || plan.site.globalEnvironmentIDs[0] != fixture.globalEnvID {
		t.Errorf("site global environments = %v, want [%s]", plan.site.globalEnvironmentIDs, fixture.globalEnvID)
	}

	// The same request under a different environment is a different key. This is
	// the whole point of putting the environment in the site.
	other := fixture.plan(t, fixture.devEnvID)
	origin := mustOrigin(t, policyProdBase)
	if plan.site.approvalKey(origin, kindClassRequest) == other.site.approvalKey(origin, kindClassRequest) {
		t.Error("the same request under two environments produced one approval key")
	}
}

// The labels are the human half and are never in the key: renaming a collection
// must not invalidate an approval.
func TestMCPRunPlanCarriesDisplayLabels(t *testing.T) {
	fixture := newTwoEnvironmentApp(t)
	plan := fixture.plan(t, fixture.prodEnvID)

	if plan.labels.requestName != "Charge card" {
		t.Errorf("request name = %q", plan.labels.requestName)
	}
	if plan.labels.environmentName != "Production" {
		t.Errorf("environment name = %q", plan.labels.environmentName)
	}
	if len(plan.labels.globalEnvironmentNames) != 1 || plan.labels.globalEnvironmentNames[0] != "Team-Globals" {
		t.Errorf("global environment names = %v", plan.labels.globalEnvironmentNames)
	}
	if len(plan.labels.advisorySecretNames) != 1 || plan.labels.advisorySecretNames[0] != "apiToken" {
		t.Errorf("advisory secret names = %v, want [apiToken]", plan.labels.advisorySecretNames)
	}
	// Nothing about the names is in the key.
	origin := mustOrigin(t, policyProdBase)
	before := plan.site.approvalKey(origin, kindClassRequest)
	renamed := plan
	renamed.labels.collectionName = "Something else entirely"
	if renamed.site.approvalKey(origin, kindClassRequest) != before {
		t.Error("a display name changed the approval key")
	}
}

// --- the policy ---------------------------------------------------------------

// The policy a run carries: one scope (its own), a fresh overlay, and a prompt
// payload that names the whole site.
func TestMCPEgressPolicyForRunCarriesTheRunsScope(t *testing.T) {
	fixture := newTwoEnvironmentApp(t)
	plan := fixture.plan(t, fixture.prodEnvID)

	policy, _ := fixture.app.mcpEgressPolicyForRun(plan)
	if depth := policy.scopeDepth(); depth != 1 {
		t.Fatalf("the policy has %d scopes, want exactly 1", depth)
	}
	scope, ok := policy.activeScope()
	if !ok {
		t.Fatal("the policy has no active scope")
	}
	if mcpSiteLabelKey(scope.site) != mcpSiteLabelKey(plan.site) {
		t.Error("the active scope is not the run's own")
	}
	// An overlay per execution (§3), and a fresh one — two runs must not share
	// variable deltas.
	if policy.overlay == nil {
		t.Error("the policy carries no execution overlay")
	}
	second, _ := fixture.app.mcpEgressPolicyForRun(plan)
	if second.overlay == policy.overlay {
		t.Error("two runs share one execution overlay")
	}

	// Base is allowed with no prompt at all; anything else asks.
	asked := 0
	policy.prompt = func(context.Context, types.MCPApprovalRequest) mcpPromptOutcome {
		asked++
		return mcpPromptDeny
	}
	if err := policy.Authorize(context.Background(), mustOrigin(t, policyProdBase), egressKindMain); err != nil {
		t.Errorf("the run's own destination was not in Base: %v", err)
	}
	if asked != 0 {
		t.Error("the run's own destination raised a prompt")
	}
	if err := policy.Authorize(context.Background(), mustOrigin(t, policyDevBase), egressKindMain); err == nil {
		t.Error("the other environment's origin was authorized without an approval")
	}
	if asked != 1 {
		t.Errorf("the unknown origin prompted %d times, want 1", asked)
	}
}

// The prompt the policy raises names every dimension its approval is keyed on
// (§6). A prompt that named less would ask a narrower question than the answer
// it produces.
func TestMCPPolicyPromptNamesTheWholeSite(t *testing.T) {
	fixture := newTwoEnvironmentApp(t)
	plan := fixture.plan(t, fixture.prodEnvID)
	policy, _ := fixture.app.mcpEgressPolicyForRun(plan)

	var seen types.MCPApprovalRequest
	policy.prompt = func(_ context.Context, request types.MCPApprovalRequest) mcpPromptOutcome {
		seen = request
		return mcpPromptDeny
	}
	_ = policy.Authorize(context.Background(), mustOrigin(t, "https://attacker.example:8443"), egressKindMain)

	if seen.Origin != "https://attacker.example:8443" {
		t.Errorf("the prompt origin is %q", seen.Origin)
	}
	if seen.RequestID != fixture.requestID || seen.RequestName != "Charge card" {
		t.Errorf("the prompt does not name the request: %+v", seen)
	}
	if seen.EnvironmentID != fixture.prodEnvID || seen.EnvironmentName != "Production" {
		t.Errorf("the prompt does not name the environment: %+v", seen)
	}
	if len(seen.GlobalEnvironmentNames) != 1 || seen.GlobalEnvironmentNames[0] != "Team-Globals" {
		t.Errorf("the prompt does not name the active globals: %+v", seen)
	}
	if seen.Kind != string(egressKindMain) || seen.KindClass != kindClassRequest {
		t.Errorf("the prompt does not name the egress kind: %+v", seen)
	}
	if seen.CollectionID != fixture.collectionID {
		t.Errorf("the prompt does not name the collection: %+v", seen)
	}
}

// A remembered approval for the run's own site is honoured with no prompt; one
// remembered under the OTHER environment is not. This is the persisted half of
// the same property the store tests measure, reached through the policy the run
// actually carries.
func TestMCPPolicyHonoursOnlyThisEnvironmentsRememberedApproval(t *testing.T) {
	fixture := newTwoEnvironmentApp(t)
	origin := mustOrigin(t, "https://reports.example.com")

	devPlan := fixture.plan(t, fixture.devEnvID)
	if err := fixture.app.rememberMCPApproval(devPlan.site, origin, kindClassRequest); err != nil {
		t.Fatalf("rememberMCPApproval: %v", err)
	}

	prodPolicy, _ := fixture.app.mcpEgressPolicyForRun(fixture.plan(t, fixture.prodEnvID))
	asked := 0
	prodPolicy.prompt = func(context.Context, types.MCPApprovalRequest) mcpPromptOutcome {
		asked++
		return mcpPromptDeny
	}
	if err := prodPolicy.Authorize(context.Background(), origin, egressKindMain); err == nil {
		t.Error("an approval remembered under dev authorized a production run")
	}
	if asked != 1 {
		t.Errorf("the production run prompted %d times, want 1", asked)
	}

	devPolicy, _ := fixture.app.mcpEgressPolicyForRun(devPlan)
	devPolicy.prompt = func(context.Context, types.MCPApprovalRequest) mcpPromptOutcome {
		t.Error("the dev run prompted for an origin it had already remembered")
		return mcpPromptDeny
	}
	if err := devPolicy.Authorize(context.Background(), origin, egressKindMain); err != nil {
		t.Errorf("the remembered approval did not hold for the run that made it: %v", err)
	}
}

// --- flows ---------------------------------------------------------------------

// Attaching a policy to a flow must not change what the flow DOES. This wave
// adds the authority object and nothing consults it yet; a flow that started
// failing here would mean the policy had leaked into the run's behaviour early.
func TestMCPFlowStillRunsWithAPolicyAttached(t *testing.T) {
	fixture := newMCPFlowFixture(t)
	flow := fixture.install(fixture.provisionFlow())

	outcome, err := fixture.backend.RunFlow(context.Background(), mcpserver.RunFlowParams{
		CollectionID: fixture.collectionID,
		FlowID:       flow.ID,
		Inputs:       map[string]string{"storeCode": "DHK-04"},
	})
	if err != nil {
		t.Fatalf("RunFlow: %v", err)
	}
	if !outcome.OK {
		t.Fatalf("the flow did not pass: %+v", outcome)
	}
	if len(outcome.Steps) != 3 {
		t.Fatalf("the flow ran %d steps, want 3", len(outcome.Steps))
	}
	// Three steps, three different stored requests — which is why the scope has
	// to move with them rather than being set once for the flow.
	seen := map[string]bool{}
	for _, step := range outcome.Steps {
		seen[step.RequestID] = true
	}
	if len(seen) != 3 {
		t.Errorf("the three steps report %d distinct request ids: %+v", len(seen), outcome.Steps)
	}
}

// THE FLOW TIER'S CENTRAL PROMISE, MEASURED AGAINST THE NEW BOUNDARY (§10).
//
// A step var of {"token":"{{apiToken}}"} is the user's own reference, and flow
// scope deliberately never resolves it — the braces travel to the send path and
// the credential is resolved inside LiteAPI. The destination boundary must not
// mistake that for a retarget: the step's own destination is in Base, so the
// send is authorized, and the literal braces are irrelevant to the decision.
//
// Both halves are asserted, because either one alone is satisfiable by breaking
// the other: resolving the braces early would "authorize" everything, and
// refusing the step would keep them literal.
func TestFlowStepVarBracesRemainLiteralWhileSendIsAuthorized(t *testing.T) {
	fixture := newMCPFlowFixture(t)
	flow := fixture.install(fixture.provisionFlow())

	// 1. AS AUTHORED. get_flow reports the braces, unresolved.
	detail, err := fixture.backend.GetFlow(fixture.collectionID, flow.ID)
	if err != nil {
		t.Fatalf("GetFlow: %v", err)
	}
	if got := detail.Steps[0].Vars["token"]; got != "{{apiToken}}" {
		t.Errorf("the step var reads %q; flow scope resolved a reference it must leave alone", got)
	}

	// 2. AUTHORIZED. The step's own destination is in its own Base, so a policy
	// consulting Base allows the send with nothing to approve.
	plan, err := fixture.app.mcpRunPlan(fixture.collectionID, fixture.lookupID, "")
	if err != nil {
		t.Fatalf("mcpRunPlan: %v", err)
	}
	origin := mustOrigin(t, fixture.server.URL)
	if !plan.scope.allows(origin, egressKindMain) {
		t.Fatalf("the step's own destination is not in its Base: %v", originStrings(plan.scope.perKind[egressKindMain]))
	}

	if !mcpEnforcementWired {
		t.Skip("the engine does not consult the destination policy yet; the end-to-end half of this case lands with the checkpoints (build with -tags mcpenforcement)")
	}

	// 3. END TO END, with NO approval path at all: no frontend means every
	// unapproved origin denies, so a flow that completes here completed on Base
	// alone — which is the claim.
	fixture.app.mcpApprovalEmit = nil
	fixture.app.ctx = nil

	outcome, runErr := fixture.backend.RunFlow(context.Background(), mcpserver.RunFlowParams{
		CollectionID: fixture.collectionID,
		FlowID:       flow.ID,
		Inputs:       map[string]string{"storeCode": "DHK-04"},
	})
	if runErr != nil {
		t.Fatalf("a flow whose every destination is in Base was refused: %v", runErr)
	}
	if !outcome.OK {
		t.Fatalf("the flow did not pass: %+v", outcome)
	}
	// The credential really did resolve inside LiteAPI and reach the server —
	// the braces were literal in flow scope, not empty.
	for _, recorded := range fixture.recorded() {
		if recorded.authHeader != "Bearer "+mcpFlowSentinelToken {
			t.Fatalf("the server saw Authorization %q; the secret did not resolve at send time", recorded.authHeader)
		}
	}
}

// Each step's scope is keyed on the STEP'S OWN request id, not the flow's or the
// first step's. Built directly from the same plans RunFlow's guard builds, so
// the property is measured rather than inferred.
func TestMCPFlowStepScopesAreKeyedOnTheirOwnRequest(t *testing.T) {
	fixture := newMCPFlowFixture(t)
	policy, book := fixture.app.newMCPExecutionPolicy()

	for _, requestID := range []string{fixture.lookupID, fixture.createID, fixture.activateID} {
		plan, err := fixture.app.mcpRunPlan(fixture.collectionID, requestID, "")
		if err != nil {
			t.Fatalf("mcpRunPlan(%s): %v", requestID, err)
		}
		mcpEnterScope(policy, book, plan)

		if depth := policy.scopeDepth(); depth != 1 {
			t.Fatalf("after entering a step the policy holds %d scopes, want 1 — steps are siblings, not ancestors", depth)
		}
		scope, ok := policy.activeScope()
		if !ok {
			t.Fatal("no active scope after entering a step")
		}
		if scope.site.requestID != requestID {
			t.Errorf("the active scope names request %q, want the step's own %q", scope.site.requestID, requestID)
		}
		if labels := book.lookup(scope.site); labels.requestName == "" {
			t.Errorf("step %q has no display labels registered", requestID)
		}
	}
}
