package core

// The run-time approval for a flow step var that reaches a secret.
//
// WHAT CHANGED AND WHY THESE TESTS EXIST. A stored flow records nothing about
// who wrote it, so while the write tier is on LiteAPI cannot tell an
// agent-authored step var from the user's own. That used to be answered with an
// outright refusal, and the refusal had a real cost: the canonical POS chain in
// our own docs aims {{apiToken}} at a request through a step var, so turning
// writes on stopped the user's own flows running through MCP. The ambiguity is
// now ASKED about — once, narrowly keyed, remembered on request.
//
// THE AUTHORING DOOR DID NOT CHANGE, and TestCreateFlowStepVarValueIsRefusedFor-
// AnAgentAuthor (mcp_flows_adversarial_test.go) still pins that: create_flow and
// update_flow refuse the shape outright, with no approval path. The two halves
// answer different questions — the agent's own channel versus a stored value
// with no recoverable author — and the difference is deliberate.
//
// WHAT EACH TEST HERE MEASURES. The prompt exists to be narrow, so most of these
// are mutation checks: an approval given for one (flow, step, var, secret,
// environment) must not answer for any other. They are written the way the
// destination key's own tests are (mcp_approvals_test.go): approve once,
// re-run to prove the memory works, then change ONE component and prove the
// user is asked again.

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mutexdev/lite_api/internal/mcpserver"
	"github.com/mutexdev/lite_api/internal/types"
)

// --- fixture helpers ---------------------------------------------------------

// enableWriteTier turns on the preference that makes a stored step var
// ambiguous. Every prompt in this file exists only while it is on.
func (f *mcpFlowFixture) enableWriteTier() {
	f.t.Helper()
	f.app.mu.Lock()
	defer f.app.mu.Unlock()
	f.app.state.Preferences.MCP.WriteTierEnabled = true
}

// answerApprovals makes the fixture's emitter ANSWER every prompt it receives,
// synchronously, on the goroutine that raised it.
//
// SYNCHRONOUS IS SAFE AND DELIBERATE. awaitMCPApproval registers the pending
// entry BEFORE it emits, and the result channel is buffered, so resolving from
// inside the emit callback delivers the answer to a waiter that is guaranteed to
// be listening. A goroutine and a sleep would measure the same thing, flakily.
func (f *mcpFlowFixture) answerApprovals(approve, remember bool) {
	f.app.mcpApprovalEmit = func(request types.MCPApprovalRequest) {
		f.mu.Lock()
		f.approvals = append(f.approvals, request)
		f.mu.Unlock()
		if err := f.app.ResolveMCPApproval(request.ID, approve, remember); err != nil {
			f.t.Errorf("ResolveMCPApproval: %v", err)
		}
	}
}

func (f *mcpFlowFixture) prompts() []types.MCPApprovalRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]types.MCPApprovalRequest{}, f.approvals...)
}

func (f *mcpFlowFixture) forgetPrompts() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.approvals = nil
}

// replaceFlow overwrites the stored flow with the same id, so a test can change
// exactly one component of the approval key without also changing the flow id.
func (f *mcpFlowFixture) replaceFlow(flow types.Flow) {
	f.t.Helper()
	f.app.mu.Lock()
	defer f.app.mu.Unlock()
	collection := &f.app.state.Workspaces[0].Collections[0]
	for index := range collection.Flows {
		if collection.Flows[index].ID == flow.ID {
			collection.Flows[index] = types.CloneFlow(flow)
			return
		}
	}
	f.t.Fatalf("no flow with id %q to replace", flow.ID)
}

// runIn is f.run with an environment, since the environment is one of the key's
// components and one test has to vary it.
func (f *mcpFlowFixture) runIn(flowID, environmentID string) (mcpserver.FlowRunOutcome, error) {
	f.t.Helper()
	return f.backend.RunFlow(context.Background(), mcpserver.RunFlowParams{
		CollectionID:  f.collectionID,
		FlowID:        flowID,
		EnvironmentID: environmentID,
	})
}

// stepVarFlow is one step aiming one secret at the fixture's pre-existing
// "Create terminal" request, which already carries an ordinary non-secret
// {{storeId}} in its body. No new destination is introduced anywhere in this
// file — every test here measures the step-var question and nothing else.
func (f *mcpFlowFixture) stepVarFlow(flowID, stepID, varName, secretName string) types.Flow {
	return types.Flow{
		ID:   flowID,
		Name: "Aims a credential at a request",
		Steps: []types.FlowStep{
			{ID: stepID, RequestID: f.createID, Vars: map[string]string{varName: "{{" + secretName + "}}"}},
		},
	}
}

// newStepVarApprovalFixture is the shape every mutation check below starts from:
// the write tier on, and a user who says "allow and remember" to everything. If
// the key were wider than it claims, that user would be asked once and never
// again — which is exactly what these tests are trying to catch.
func newStepVarApprovalFixture(t *testing.T) *mcpFlowFixture {
	t.Helper()
	f := newMCPFlowFixture(t)
	f.enableWriteTier()
	f.answerApprovals(true, true)
	return f
}

// approveBaseStepVarFlow installs the base flow, runs it once (which must ask),
// and runs it again (which must not). It returns the base flow.
//
// THE SECOND RUN IS NOT A FORMALITY. Without it, a mutation check cannot tell
// "the key is narrow" from "remembering is broken and everything always asks",
// and both would make the same assertion pass.
func approveBaseStepVarFlow(t *testing.T, f *mcpFlowFixture) types.Flow {
	t.Helper()
	base := f.stepVarFlow("flow_stepvar_base", "createTerminal", "storeId", "apiToken")
	f.install(base)

	if _, err := f.runIn(base.ID, ""); err != nil {
		t.Fatalf("the approved run was refused: %v", err)
	}
	if got := len(f.prompts()); got != 1 {
		t.Fatalf("the first run raised %d prompts, want exactly 1", got)
	}
	f.forgetPrompts()

	if _, err := f.runIn(base.ID, ""); err != nil {
		t.Fatalf("the remembered run was refused: %v", err)
	}
	if got := len(f.prompts()); got != 0 {
		t.Fatalf("the remembered approval did not hold: the second run raised %d prompts", got)
	}
	f.forgetPrompts()
	return base
}

// --- 1. the prompt itself ----------------------------------------------------

// THE CHANGE, MEASURED. With the write tier on, a step var that reaches a secret
// used to be refused outright; it now asks, and an approval lets the run
// through with the credential resolved inside LiteAPI exactly as the flow tier
// promises.
func TestMCPRunFlowStepVarPromptsAndRunsWhenApproved(t *testing.T) {
	f := newMCPFlowFixture(t)
	f.enableWriteTier()
	f.answerApprovals(true, false) // allow once: the smallest grant that unblocks

	f.install(f.stepVarFlow("flow_stepvar_allow", "createTerminal", "storeId", "apiToken"))
	outcome, err := f.runIn("flow_stepvar_allow", "")
	if err != nil {
		t.Fatalf("an approved step var was still refused: %v", err)
	}
	if !outcome.OK {
		t.Fatalf("the flow did not pass: %+v", outcome)
	}

	prompts := f.prompts()
	if len(prompts) != 1 {
		t.Fatalf("got %d prompts, want exactly 1: %+v", len(prompts), prompts)
	}
	prompt := prompts[0]
	if prompt.Subject != types.MCPApprovalSubjectFlowStepVar {
		t.Errorf("subject = %q, want %q", prompt.Subject, types.MCPApprovalSubjectFlowStepVar)
	}
	// EVERY FIELD THE KEY IS BUILT FROM HAS TO BE ON SCREEN, or the user is
	// answering a narrower question than the one being remembered.
	if prompt.FlowID != "flow_stepvar_allow" || prompt.StepID != "createTerminal" || prompt.VarName != "storeId" {
		t.Errorf("the prompt does not name the flow, step and var: %+v", prompt)
	}
	if len(prompt.SecretNames) != 1 || prompt.SecretNames[0] != "apiToken" {
		t.Errorf("the prompt does not name the secret: %+v", prompt.SecretNames)
	}
	// And the request the var feeds, which is what makes the question weighable.
	if prompt.RequestID != f.createID || prompt.RequestName != "Create terminal" {
		t.Errorf("the prompt does not name the request the var feeds: %+v", prompt)
	}
	if prompt.FlowName != "Aims a credential at a request" {
		t.Errorf("the prompt does not name the flow: %q", prompt.FlowName)
	}
	// NEVER A VALUE. The whole point of aiming a secret through a step var is
	// that nothing outside LiteAPI ever holds it.
	for _, field := range []string{prompt.VarName, prompt.FlowName, prompt.RequestName, strings.Join(prompt.SecretNames, ",")} {
		if strings.Contains(field, mcpFlowSentinelToken) {
			t.Errorf("the prompt quoted the secret VALUE: %q", field)
		}
	}

	// The credential really did resolve at send time — otherwise the approval
	// unblocked nothing and this test measures nothing.
	sawCredential := false
	for _, request := range f.recorded() {
		if request.path == "/terminals" && strings.Contains(request.body, mcpFlowSentinelToken) {
			sawCredential = true
		}
	}
	if !sawCredential {
		t.Error("the approved step var never reached the wire, so the approval unblocked nothing")
	}
}

// Allow-once is exactly once: it unblocks this run and writes nothing, so the
// next run asks again. Otherwise the two buttons would mean the same thing.
func TestMCPRunFlowStepVarAllowOnceIsNotRemembered(t *testing.T) {
	f := newMCPFlowFixture(t)
	f.enableWriteTier()
	f.answerApprovals(true, false)

	f.install(f.stepVarFlow("flow_stepvar_once", "createTerminal", "storeId", "apiToken"))
	for run := 1; run <= 2; run++ {
		f.forgetPrompts()
		if _, err := f.runIn("flow_stepvar_once", ""); err != nil {
			t.Fatalf("run %d was refused: %v", run, err)
		}
		if got := len(f.prompts()); got != 1 {
			t.Fatalf("run %d raised %d prompts, want 1 — allow-once must not persist", run, got)
		}
	}
}

// --- 2. every way of saying no -----------------------------------------------

// A DENIAL IS STILL A DENIAL, with the class the audit reads and a message that
// names the var and the secret but never a value.
func TestMCPRunFlowStepVarDenialRefusesTheRun(t *testing.T) {
	f := newMCPFlowFixture(t)
	f.enableWriteTier()
	f.answerApprovals(false, false)

	f.install(f.stepVarFlow("flow_stepvar_deny", "createTerminal", "storeId", "apiToken"))
	outcome, err := f.runIn("flow_stepvar_deny", "")
	if err == nil {
		t.Fatalf("a denied step var still ran: %+v", outcome)
	}
	if !errors.Is(err, mcpserver.ErrDenied) {
		t.Fatalf("the refusal does not wrap ErrDenied: %v", err)
	}
	if !strings.Contains(err.Error(), `"storeId"`) || !strings.Contains(err.Error(), "apiToken") {
		t.Errorf("the refusal should name the var and the secret: %v", err)
	}
	if strings.Contains(err.Error(), mcpFlowSentinelToken) {
		t.Errorf("the refusal quoted the secret VALUE: %v", err)
	}
	for _, request := range f.recorded() {
		if strings.Contains(request.body, mcpFlowSentinelToken) {
			t.Errorf("the credential reached a server anyway: %+v", request)
		}
	}
}

// A prompt nobody answers denies, the same as an explicit no. The fixture's
// default emitter records prompts and never resolves them, which is precisely a
// user who walked away.
func TestMCPRunFlowStepVarTimeoutRefusesTheRun(t *testing.T) {
	f := newMCPFlowFixture(t)
	f.enableWriteTier()
	f.app.mcpApprovalTimeout = 50 * time.Millisecond

	f.install(f.stepVarFlow("flow_stepvar_timeout", "createTerminal", "storeId", "apiToken"))
	_, err := f.runIn("flow_stepvar_timeout", "")
	if !errors.Is(err, mcpserver.ErrDenied) {
		t.Fatalf("an unanswered prompt did not deny: %v", err)
	}
	if got := len(f.prompts()); got != 1 {
		t.Fatalf("got %d prompts, want 1 — the timeout must follow a prompt that was actually raised", got)
	}
	for _, request := range f.recorded() {
		if strings.Contains(request.body, mcpFlowSentinelToken) {
			t.Errorf("the credential reached a server anyway: %+v", request)
		}
	}
}

// NOBODY TO ASK IS NOT CONSENT. `liteapi mcp` runs with no window: there is no
// frontend to emit to, so the run is denied immediately rather than left to time
// out for a minute against a dialog that will never appear.
func TestMCPRunFlowStepVarHeadlessDeniesWithNobodyToAsk(t *testing.T) {
	f := newMCPFlowFixture(t)
	f.enableWriteTier()
	// The headless shape: no emit seam and no Wails context.
	f.app.mcpApprovalEmit = nil
	if f.app.ctx != nil {
		t.Fatal("this test needs an app with nobody to prompt")
	}

	f.install(f.stepVarFlow("flow_stepvar_headless", "createTerminal", "storeId", "apiToken"))
	started := time.Now()
	_, err := f.runIn("flow_stepvar_headless", "")
	if !errors.Is(err, mcpserver.ErrDenied) {
		t.Fatalf("a headless run was not denied: %v", err)
	}
	// Denied rather than waited out: the 60s default would be the failure here.
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Errorf("the headless denial took %s; it should not wait for a dialog that cannot appear", elapsed)
	}
	if got := len(f.prompts()); got != 0 {
		t.Errorf("something was emitted to a frontend that does not exist: %d", got)
	}
}

// --- 3. the write tier off ---------------------------------------------------

// THE UNCHANGED HALF. With writes off the agent has no authoring channel at all,
// so the step var is provably the user's and runs SILENTLY — not "prompts and is
// approved", which would train the user to click through a question that has
// only one right answer.
func TestMCPRunFlowStepVarIsSilentWhileTheWriteTierIsOff(t *testing.T) {
	f := newMCPFlowFixture(t)
	// Deliberately an emitter that would answer NO if anything asked, so a
	// prompt raised here fails the run rather than passing unnoticed.
	f.answerApprovals(false, false)

	f.install(f.stepVarFlow("flow_stepvar_writes_off", "createTerminal", "storeId", "apiToken"))
	outcome, err := f.runIn("flow_stepvar_writes_off", "")
	if err != nil {
		t.Fatalf("a user-authored step var was refused with the write tier off: %v", err)
	}
	if !outcome.OK {
		t.Fatalf("the flow did not pass: %+v", outcome)
	}
	if got := len(f.prompts()); got != 0 {
		t.Errorf("the write-tier-off path asked the user %d times; it must not ask at all", got)
	}
	sawCredential := false
	for _, request := range f.recorded() {
		if request.path == "/terminals" && strings.Contains(request.body, mcpFlowSentinelToken) {
			sawCredential = true
		}
	}
	if !sawCredential {
		t.Error("the credential never resolved, so this test measures nothing")
	}
}

// --- 4. the key is as narrow as it claims ------------------------------------

// A yes for `storeId` is not a yes for `region`. Same flow, same step, same
// secret, same environment — one different variable name.
func TestMCPStepVarApprovalDoesNotAuthorizeADifferentVar(t *testing.T) {
	f := newStepVarApprovalFixture(t)
	base := approveBaseStepVarFlow(t, f)

	variant := f.stepVarFlow(base.ID, "createTerminal", "region", "apiToken")
	f.replaceFlow(variant)
	if _, err := f.runIn(base.ID, ""); err != nil {
		t.Fatalf("the variant run failed for an unrelated reason: %v", err)
	}
	if got := len(f.prompts()); got != 1 {
		t.Fatalf("a different var raised %d prompts, want 1: the approval widened past the variable it named", got)
	}
	if name := f.prompts()[0].VarName; name != "region" {
		t.Errorf("the prompt is about %q, want the variant's own var", name)
	}
}

// A yes for one step is not a yes for the next, even in the same flow with the
// same variable. Steps are siblings and each one is its own decision.
func TestMCPStepVarApprovalDoesNotAuthorizeADifferentStep(t *testing.T) {
	f := newStepVarApprovalFixture(t)
	base := approveBaseStepVarFlow(t, f)

	variant := f.stepVarFlow(base.ID, "createTerminalAgain", "storeId", "apiToken")
	f.replaceFlow(variant)
	if _, err := f.runIn(base.ID, ""); err != nil {
		t.Fatalf("the variant run failed for an unrelated reason: %v", err)
	}
	if got := len(f.prompts()); got != 1 {
		t.Fatalf("a different step raised %d prompts, want 1: the approval widened past the step it named", got)
	}
	if id := f.prompts()[0].StepID; id != "createTerminalAgain" {
		t.Errorf("the prompt is about step %q, want the variant's own", id)
	}
}

// A yes in one flow is not a yes in another — which matters most for the flow an
// agent writes tomorrow, reusing a step id the user already approved elsewhere.
func TestMCPStepVarApprovalDoesNotAuthorizeADifferentFlow(t *testing.T) {
	f := newStepVarApprovalFixture(t)
	approveBaseStepVarFlow(t, f)

	other := f.stepVarFlow("flow_stepvar_other", "createTerminal", "storeId", "apiToken")
	f.install(other)
	if _, err := f.runIn(other.ID, ""); err != nil {
		t.Fatalf("the other flow failed for an unrelated reason: %v", err)
	}
	if got := len(f.prompts()); got != 1 {
		t.Fatalf("a different flow raised %d prompts, want 1: the approval widened past the flow it named", got)
	}
	if id := f.prompts()[0].FlowID; id != other.ID {
		t.Errorf("the prompt is about flow %q, want the other flow", id)
	}
}

// A yes for apiToken is not a yes for every credential the same variable might
// later reach. The secrets are IN the key precisely so that widening the reach
// re-asks.
func TestMCPStepVarApprovalDoesNotAuthorizeADifferentSecret(t *testing.T) {
	f := newStepVarApprovalFixture(t)
	f.app.mu.Lock()
	workspace := &f.app.state.Workspaces[0]
	for index := range workspace.GlobalEnvironments {
		if workspace.GlobalEnvironments[index].ID != "env-mcp-flow-global" {
			continue
		}
		workspace.GlobalEnvironments[index].Variables = append(workspace.GlobalEnvironments[index].Variables,
			Variable{ID: "mcp-flow-var-other", Name: "otherToken", Value: "MCP-FLOW-SENTINEL-OTHER-TOKEN", Enabled: true, Secret: true})
	}
	f.app.mu.Unlock()

	base := approveBaseStepVarFlow(t, f)

	variant := f.stepVarFlow(base.ID, "createTerminal", "storeId", "otherToken")
	f.replaceFlow(variant)
	if _, err := f.runIn(base.ID, ""); err != nil {
		t.Fatalf("the variant run failed for an unrelated reason: %v", err)
	}
	if got := len(f.prompts()); got != 1 {
		t.Fatalf("a different secret raised %d prompts, want 1: the approval widened past the credential it named", got)
	}
	if names := f.prompts()[0].SecretNames; len(names) != 1 || names[0] != "otherToken" {
		t.Errorf("the prompt names %v, want the variant's own secret", names)
	}
}

// A yes under one environment is not a yes under another. Whether a name
// resolves to a secret — and to WHICH secret — depends on the active
// environments, so an approval given under one configuration must not answer for
// a different one.
func TestMCPStepVarApprovalDoesNotAuthorizeADifferentEnvironment(t *testing.T) {
	f := newStepVarApprovalFixture(t)
	f.app.mu.Lock()
	collection := &f.app.state.Workspaces[0].Collections[0]
	collection.Environments = append(collection.Environments, Environment{
		ID:        "env-mcp-flow-dev",
		Name:      "Dev",
		Variables: []Variable{{ID: "mcp-flow-dev-var", Name: "unused", Value: "x", Enabled: true}},
	})
	f.app.mu.Unlock()

	base := approveBaseStepVarFlow(t, f) // approved with NO collection environment

	if _, err := f.runIn(base.ID, "env-mcp-flow-dev"); err != nil {
		t.Fatalf("the run under the other environment failed for an unrelated reason: %v", err)
	}
	if got := len(f.prompts()); got != 1 {
		t.Fatalf("a different environment raised %d prompts, want 1: the approval widened past the environment it was given under", got)
	}
	if id := f.prompts()[0].EnvironmentID; id != "env-mcp-flow-dev" {
		t.Errorf("the prompt is about environment %q, want the one the run used", id)
	}
}

// --- 5. the store ------------------------------------------------------------

// A remembered step-var approval survives a reload, and it survives it WITHOUT
// widening: the entry is written and read back through the same key builder, so
// a round trip that lost a field would show up as a re-prompt rather than as a
// silent match on a shorter key.
func TestMCPStepVarApprovalRoundTripsThroughTheFile(t *testing.T) {
	dir := t.TempDir()
	first := newAppInDirForTest(t, dir)

	subject := mcpStepVarSubject{
		workspacePath:        "/w",
		collectionID:         "col_1",
		flowID:               "flow_1",
		stepID:               "step_1",
		varName:              "storeId",
		secretNames:          []string{"apiToken"},
		environmentID:        "",
		globalEnvironmentIDs: []string{"global_team"},
	}
	if err := first.rememberMCPStepVarApproval(subject); err != nil {
		t.Fatalf("rememberMCPStepVarApproval: %v", err)
	}

	second := newAppInDirForTest(t, dir)
	approved, err := second.mcpRememberedStepVarApproved(subject)
	if err != nil {
		t.Fatalf("mcpRememberedStepVarApproved: %v", err)
	}
	if !approved {
		t.Fatal("a remembered step-var approval did not survive a reload")
	}

	// And exactly this subject. One field at a time, because a key that matched
	// on a prefix would pass a single-field check by accident.
	for name, mutate := range map[string]func(s *mcpStepVarSubject){
		"workspace":   func(s *mcpStepVarSubject) { s.workspacePath = "/other" },
		"collection":  func(s *mcpStepVarSubject) { s.collectionID = "col_2" },
		"flow":        func(s *mcpStepVarSubject) { s.flowID = "flow_2" },
		"step":        func(s *mcpStepVarSubject) { s.stepID = "step_2" },
		"var":         func(s *mcpStepVarSubject) { s.varName = "region" },
		"secret":      func(s *mcpStepVarSubject) { s.secretNames = []string{"otherToken"} },
		"secret set":  func(s *mcpStepVarSubject) { s.secretNames = []string{"apiToken", "otherToken"} },
		"environment": func(s *mcpStepVarSubject) { s.environmentID = "env_prod" },
		"globals":     func(s *mcpStepVarSubject) { s.globalEnvironmentIDs = []string{"global_other"} },
	} {
		other := subject
		other.secretNames = append([]string{}, subject.secretNames...)
		other.globalEnvironmentIDs = append([]string{}, subject.globalEnvironmentIDs...)
		mutate(&other)
		approved, err := second.mcpRememberedStepVarApproved(other)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if approved {
			t.Errorf("the approval also authorized a different %s", name)
		}
	}
}

// A Version 1 file written before step-var approvals existed simply has none —
// which is the correct reading, and must NOT be treated as content this build
// could not interpret. Backing it up and warning the user would be a lie about
// something that was never lost.
func TestMCPApprovalFileWithoutStepVarsIsNotAMigration(t *testing.T) {
	approvals, stepVars, ignored := mcpDecodeApprovalFile([]byte(`{
	  "version": 1,
	  "approvals": [
	    {
	      "workspacePath": "/w",
	      "collectionId": "col_1",
	      "requestId": "req_1",
	      "environmentId": "",
	      "globalEnvironmentIds": [],
	      "origin": "https://api.example.com:443",
	      "kindClass": "request"
	    }
	  ]
	}`))
	if ignored {
		t.Error("a file with no stepVarApprovals array was treated as unreadable content")
	}
	if len(approvals) != 1 {
		t.Errorf("the destination approvals were lost: %+v", approvals)
	}
	if len(stepVars) != 0 {
		t.Errorf("step-var approvals appeared from nowhere: %+v", stepVars)
	}
}

// FAIL CLOSED, the same way the destination entries do. An entry missing any key
// field was written under a key that spanned that field, and honouring it would
// authorize more than anyone approved.
func TestMCPStepVarApprovalEntriesMissingKeyFieldsAreIgnored(t *testing.T) {
	approvals, stepVars, ignored := mcpDecodeApprovalFile([]byte(`{
	  "version": 1,
	  "approvals": [],
	  "stepVarApprovals": [
	    {"workspacePath": "/w", "collectionId": "c", "flowId": "f", "stepId": "s", "varName": "v", "secretNames": ["t"], "globalEnvironmentIds": []},
	    {"workspacePath": "/w", "collectionId": "c", "flowId": "f", "stepId": "s", "varName": "v", "secretNames": [], "environmentId": "", "globalEnvironmentIds": []},
	    {"workspacePath": "/w", "collectionId": "c", "flowId": "f", "stepId": "s", "secretNames": ["t"], "environmentId": "", "globalEnvironmentIds": []},
	    {"workspacePath": "/w", "collectionId": "c", "flowId": "f", "stepId": "s", "varName": "v", "secretNames": ["t"], "environmentId": "", "globalEnvironmentIds": ["g"]}
	  ]
	}`))
	if len(approvals) != 0 {
		t.Errorf("destination approvals appeared from nowhere: %+v", approvals)
	}
	// Only the last entry carries every field.
	if len(stepVars) != 1 || stepVars[0].VarName != "v" {
		t.Fatalf("got %d usable entries, want exactly the complete one: %+v", len(stepVars), stepVars)
	}
	if !ignored {
		t.Error("entries were dropped without saying so; the user gets no warning and no backup")
	}
}

// --- 6. concurrency ----------------------------------------------------------

// The store is read and written from run goroutines, and two flows can be in
// flight at once. This is a race-detector case: -race is where a missing lock
// around the remembered slices shows up, not in an assertion.
func TestMCPStepVarApprovalStoreIsSafeUnderConcurrentRuns(t *testing.T) {
	app := newAppForTest(t)
	var wait sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		wait.Add(1)
		go func(worker int) {
			defer wait.Done()
			subject := mcpStepVarSubject{
				workspacePath:        "/w",
				collectionID:         "col_1",
				flowID:               "flow_1",
				stepID:               "step_1",
				varName:              "var_" + string(rune('a'+worker)),
				secretNames:          []string{"apiToken"},
				globalEnvironmentIDs: []string{},
			}
			if err := app.rememberMCPStepVarApproval(subject); err != nil {
				t.Errorf("remember: %v", err)
			}
			if _, err := app.mcpRememberedStepVarApproved(subject); err != nil {
				t.Errorf("lookup: %v", err)
			}
		}(worker)
	}
	wait.Wait()
}
