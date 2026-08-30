package core

// Tests for the flow runner.
//
// EVERY STEP HERE IS A REAL HTTP REQUEST to a local httptest.Server, for the
// same reason the run tier's tests are: a flow exists to go through the app's
// own send path, and a fake transport would prove nothing about whether the
// overrides really reached the wire or whether the environment's secret really
// resolved. The fixture is the document's canonical example — a GraphQL-shaped
// lookup, a POST that consumes what it found, an activate that consumes what
// THAT returned — because a three-step chain is the smallest one where "step 3
// never ran" is a distinguishable outcome.
//
// The secret is a long sentinel for the same reason it is in mcp_run_test.go:
// short values are not worth masking and would make an assertion pass without
// measuring anything.

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

	"github.com/mutexdev/lite_api/internal/types"
)

const flowSentinelToken = "FLOW-SENTINEL-API-TOKEN-VALUE"

type flowFixture struct {
	t            *testing.T
	app          *App
	server       *httptest.Server
	collectionID string
	lookupID     string
	createID     string
	activateID   string

	mu       sync.Mutex
	requests []recordedFlowRequest
	progress []types.FlowProgress
	// failCreateWith, when set, is the status /terminals answers with.
	failCreateWith int
	// createState is what /terminals reports as the terminal's state.
	createState string
}

type recordedFlowRequest struct {
	path       string
	body       string
	authHeader string
}

func newFlowFixture(t *testing.T) *flowFixture {
	t.Helper()
	fixture := &flowFixture{t: t, createState: "created"}
	fixture.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		fixture.mu.Lock()
		fixture.requests = append(fixture.requests, recordedFlowRequest{
			path:       r.URL.Path,
			body:       string(body),
			authHeader: r.Header.Get("Authorization"),
		})
		failWith := fixture.failCreateWith
		state := fixture.createState
		fixture.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/graphql":
			w.Header().Set("X-Store-Etag", "etag-777")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"data": map[string]interface{}{
					"store": map[string]interface{}{"id": "store_42", "region": "apac"},
				},
			})
		case "/terminals":
			if failWith != 0 {
				w.WriteHeader(failWith)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "no capacity"})
				return
			}
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"terminal": map[string]interface{}{"id": "term_7", "state": state},
			})
		case "/activate":
			// The Authorization header is echoed back so a test can prove the
			// environment's secret really did resolve inside LiteAPI on a step
			// whose vars never mention it.
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"activated": true,
				"echo":      r.Header.Get("Authorization"),
			})
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"unknown"}`))
		}
	}))
	t.Cleanup(fixture.server.Close)

	app := newAppForTest(t)
	if _, err := app.GetState(); err != nil {
		t.Fatalf("GetState: %v", err)
	}
	fixture.app = app
	app.flowProgressEmit = func(progress types.FlowProgress) {
		fixture.mu.Lock()
		fixture.progress = append(fixture.progress, progress)
		fixture.mu.Unlock()
	}

	app.mu.Lock()
	workspace := &app.state.Workspaces[0]
	collection := &workspace.Collections[0]

	lookup := types.NewRequestItem("Store lookup", "http", len(collection.Items)+1)
	lookup.Method = http.MethodPost
	lookup.URL = "{{baseUrl}}/graphql"
	lookup.Headers = []KeyValue{{Name: "Authorization", Value: "Bearer {{apiToken}}", Enabled: true}}
	lookup.Body = types.RequestBody{Mode: "json", JSON: `{"query":"query($code:String!){store(code:$code){id region}}","variables":{"code":"{{code}}"}}`}
	collection.Items = append(collection.Items, lookup)

	create := types.NewRequestItem("Create terminal", "http", len(collection.Items)+1)
	create.Method = http.MethodPost
	create.URL = "{{baseUrl}}/terminals"
	create.Headers = []KeyValue{{Name: "Authorization", Value: "Bearer {{apiToken}}", Enabled: true}}
	create.Body = types.RequestBody{Mode: "json", JSON: `{"storeId":"{{storeId}}","region":"{{region}}"}`}
	collection.Items = append(collection.Items, create)

	activate := types.NewRequestItem("Activate terminal", "http", len(collection.Items)+1)
	activate.Method = http.MethodPost
	activate.URL = "{{baseUrl}}/activate"
	activate.Headers = []KeyValue{{Name: "Authorization", Value: "Bearer {{apiToken}}", Enabled: true}}
	activate.Body = types.RequestBody{Mode: "json", JSON: `{"terminalId":"{{terminalId}}"}`}
	collection.Items = append(collection.Items, activate)

	workspace.GlobalEnvironments = append(workspace.GlobalEnvironments, Environment{
		ID:   "env-flow-global",
		Name: "Flow Global",
		Variables: []Variable{
			{ID: "flow-var-base", Name: "baseUrl", Value: fixture.server.URL, Enabled: true},
			{ID: "flow-var-token", Name: "apiToken", Value: flowSentinelToken, Enabled: true, Secret: true},
		},
	})
	workspace.ActiveGlobalEnvironmentID = "env-flow-global"

	fixture.collectionID = collection.ID
	fixture.lookupID = lookup.ID
	fixture.createID = create.ID
	fixture.activateID = activate.ID
	app.mu.Unlock()

	return fixture
}

// provisionFlow is the document's canonical example, wired to the fixture's ids.
func (f *flowFixture) provisionFlow() types.Flow {
	return types.Flow{
		ID:          "flow_provision",
		Name:        "Provision POS terminal",
		Description: "GraphQL lookup -> create terminal -> activate",
		Inputs:      []types.FlowInput{{Name: "storeCode", Required: true, Description: "Store short code"}},
		Steps: []types.FlowStep{
			{
				ID:        "lookup",
				RequestID: f.lookupID,
				Vars:      map[string]string{"code": "{{storeCode}}"},
				Extract: []types.FlowExtract{
					{Name: "storeId", From: "body", Path: "$.data.store.id"},
					{Name: "region", From: "body", Path: "$.data.store.region"},
				},
				Assert: []types.FlowAssert{{Type: "status", Equals: 200}},
			},
			{
				ID:        "createTerminal",
				RequestID: f.createID,
				Vars:      map[string]string{"storeId": "{{storeId}}", "region": "{{region}}"},
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

// install puts a flow on the fixture's collection without going through the
// CRUD binding, so a test can plant a shape CreateFlow would refuse.
func (f *flowFixture) install(flow types.Flow) {
	f.t.Helper()
	f.app.mu.Lock()
	defer f.app.mu.Unlock()
	collection := &f.app.state.Workspaces[0].Collections[0]
	collection.Flows = append(collection.Flows, types.CloneFlow(flow))
}

// run is a UI-initiated flow run: uiSendProvenance, the same thing RunFlow
// passes from the Flow tab (§4.5).
func (f *flowFixture) run(flow types.Flow, inputs map[string]string, guard flowStepGuard) (types.FlowRunResult, error) {
	f.t.Helper()
	return f.app.runFlowProvenance(context.Background(), uiSendProvenance(), f.collectionID, flow.ID, "", inputs, guard)
}

func (f *flowFixture) recorded() []recordedFlowRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]recordedFlowRequest{}, f.requests...)
}

func (f *flowFixture) progressEvents() []types.FlowProgress {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]types.FlowProgress{}, f.progress...)
}

// --- 1. the happy path ------------------------------------------------------

func TestFlowRunChainsThreeStepsThroughTheAppsOwnSendPath(t *testing.T) {
	f := newFlowFixture(t)
	flow := f.provisionFlow()
	f.install(flow)

	result, err := f.run(flow, map[string]string{"storeCode": "DHK-04"}, nil)
	if err != nil {
		t.Fatalf("runFlow: %v", err)
	}
	if !result.OK {
		t.Fatalf("result.OK is false: %s\n%#v", result.Error, result.Steps)
	}
	if len(result.Steps) != 3 {
		t.Fatalf("ran %d steps, want 3", len(result.Steps))
	}

	recorded := f.recorded()
	if len(recorded) != 3 {
		t.Fatalf("the server saw %d requests, want 3: %#v", len(recorded), recorded)
	}
	// Step 1's var came from the flow input.
	if !strings.Contains(recorded[0].body, `"code":"DHK-04"`) {
		t.Errorf("step 1 body did not carry the input: %s", recorded[0].body)
	}
	// Step 2's vars came from step 1's extractions.
	if !strings.Contains(recorded[1].body, `"storeId":"store_42"`) || !strings.Contains(recorded[1].body, `"region":"apac"`) {
		t.Errorf("step 2 body did not carry step 1's extractions: %s", recorded[1].body)
	}
	// Step 3's var came from step 2's extraction.
	if !strings.Contains(recorded[2].body, `"terminalId":"term_7"`) {
		t.Errorf("step 3 body did not carry step 2's extraction: %s", recorded[2].body)
	}
	// The environment's secret resolved on every step, without any step var
	// naming it: that is the send path doing its job, not the flow.
	for index, request := range recorded {
		if request.authHeader != "Bearer "+flowSentinelToken {
			t.Errorf("step %d Authorization = %q, want the resolved secret", index+1, request.authHeader)
		}
	}

	if got := result.Steps[0].Extracted["storeId"]; got != "store_42" {
		t.Errorf("extracted storeId = %q", got)
	}
	if got := result.Steps[1].Status; got != http.StatusCreated {
		t.Errorf("step 2 status = %d, want 201", got)
	}
	if result.Steps[0].DurationMs < 0 {
		t.Errorf("step 1 durationMs = %d", result.Steps[0].DurationMs)
	}
	if got := result.Outputs["terminalId"]; got != "term_7" {
		t.Errorf("outputs = %#v, want terminalId term_7", result.Outputs)
	}
	for _, step := range result.Steps {
		for _, assertion := range step.Assertions {
			if !assertion.OK {
				t.Errorf("step %s assertion failed: %s", step.StepID, assertion.Detail)
			}
		}
	}
}

// --- 2. flow scope is not the environment -----------------------------------

// The pin the whole safety argument rests on: a step var that references a
// SECRET by name is not resolved by the flow. The braces travel through
// literally, and it is the send path — inside LiteAPI, at send time — that
// decides what {{apiToken}} means.
func TestFlowStepVarsResolveAgainstFlowScopeOnlyAndPassSecretsThroughLiterally(t *testing.T) {
	scope := map[string]string{"storeCode": "DHK-04"}
	step := types.FlowStep{
		ID: "lookup",
		Vars: map[string]string{
			"code":      "{{storeCode}}",
			"smuggled":  "{{apiToken}}",
			"mixed":     "{{storeCode}}/{{apiToken}}",
			"untouched": "literal",
		},
	}
	overrides := flowStepOverrides(step, scope)
	if got := overrides["code"]; got != "DHK-04" {
		t.Errorf("code = %q, want the input's value", got)
	}
	if got := overrides["smuggled"]; got != "{{apiToken}}" {
		t.Errorf("smuggled = %q, want the braces passed through verbatim", got)
	}
	if got := overrides["mixed"]; got != "DHK-04/{{apiToken}}" {
		t.Errorf("mixed = %q, want only the flow-scope half resolved", got)
	}
	if got := overrides["untouched"]; got != "literal" {
		t.Errorf("untouched = %q", got)
	}
}

// --- 3. inputs --------------------------------------------------------------

func TestFlowRunRefusesAMissingRequiredInput(t *testing.T) {
	f := newFlowFixture(t)
	flow := f.provisionFlow()
	f.install(flow)

	_, err := f.run(flow, nil, nil)
	if err == nil {
		t.Fatal("a flow with no value for a required input ran")
	}
	if !strings.Contains(err.Error(), "storeCode") {
		t.Errorf("error = %v, want it to name the input", err)
	}
	if len(f.recorded()) != 0 {
		t.Errorf("a request was sent before the input check: %#v", f.recorded())
	}
}

func TestFlowRunRefusesAnUndeclaredInput(t *testing.T) {
	f := newFlowFixture(t)
	flow := f.provisionFlow()
	f.install(flow)

	_, err := f.run(flow, map[string]string{"storeCode": "DHK-04", "storeCoden": "typo"}, nil)
	if err == nil {
		t.Fatal("an undeclared input was accepted")
	}
	if !strings.Contains(err.Error(), "storeCoden") || !strings.Contains(err.Error(), "storeCode") {
		t.Errorf("error = %v, want it to name both the typo and what is declared", err)
	}
}

// --- 4. extraction ----------------------------------------------------------

func TestFlowRunFailsTheStepWhenAnExtractionMisses(t *testing.T) {
	f := newFlowFixture(t)
	flow := f.provisionFlow()
	flow.Steps[0].Extract[0].Path = "$.data.store.missing"
	f.install(flow)

	result, err := f.run(flow, map[string]string{"storeCode": "DHK-04"}, nil)
	if err != nil {
		t.Fatalf("an extraction miss is a run outcome, not a refusal: %v", err)
	}
	if result.OK {
		t.Fatal("the flow reported OK with a failed extraction")
	}
	if len(result.Steps) != 1 {
		t.Fatalf("ran %d steps, want to stop after the first", len(result.Steps))
	}
	stepError := result.Steps[0].Error
	if !strings.Contains(stepError, "lookup") || !strings.Contains(stepError, "$.data.store.missing") {
		t.Errorf("step error = %q, want it to name the step and the path", stepError)
	}
	if len(f.recorded()) != 1 {
		t.Errorf("later steps ran anyway: %#v", f.recorded())
	}
}

func TestFlowRunExtractsFromHeadersAndStatus(t *testing.T) {
	f := newFlowFixture(t)
	flow := f.provisionFlow()
	flow.Steps[0].Extract = append(flow.Steps[0].Extract,
		types.FlowExtract{Name: "etag", From: "header", Path: "x-store-etag"},
		types.FlowExtract{Name: "lookupStatus", From: "status"},
	)
	flow.Outputs = append(flow.Outputs,
		types.FlowOutput{Name: "etag", Value: "{{etag}}"},
		types.FlowOutput{Name: "lookupStatus", Value: "{{lookupStatus}}"},
	)
	f.install(flow)

	result, err := f.run(flow, map[string]string{"storeCode": "DHK-04"}, nil)
	if err != nil {
		t.Fatalf("runFlow: %v", err)
	}
	if !result.OK {
		t.Fatalf("result.OK is false: %s", result.Error)
	}
	if got := result.Outputs["etag"]; got != "etag-777" {
		t.Errorf("etag = %q, want the header value, matched case-insensitively", got)
	}
	if got := result.Outputs["lookupStatus"]; got != "200" {
		t.Errorf("lookupStatus = %q", got)
	}
}

func TestFlowRunFailsTheStepWhenAHeaderIsAbsent(t *testing.T) {
	f := newFlowFixture(t)
	flow := f.provisionFlow()
	flow.Steps[0].Extract = []types.FlowExtract{{Name: "missing", From: "header", Path: "X-Not-Sent"}}
	f.install(flow)

	result, err := f.run(flow, map[string]string{"storeCode": "DHK-04"}, nil)
	if err != nil {
		t.Fatalf("runFlow: %v", err)
	}
	if result.OK {
		t.Fatal("a missing header did not fail the step")
	}
	if !strings.Contains(result.Steps[0].Error, "X-Not-Sent") {
		t.Errorf("step error = %q, want it to name the header", result.Steps[0].Error)
	}
}

// --- 5. assertions ----------------------------------------------------------

func TestFlowRunFailsOnEachAssertionType(t *testing.T) {
	cases := []struct {
		name     string
		mutate   func(*flowFixture, *types.Flow)
		stepID   string
		contains string
	}{
		{
			name: "status equals",
			mutate: func(f *flowFixture, flow *types.Flow) {
				flow.Steps[0].Assert = []types.FlowAssert{{Type: "status", Equals: 418}}
			},
			stepID:   "lookup",
			contains: "status equals 418, but the response status was 200",
		},
		{
			name: "status in",
			mutate: func(f *flowFixture, flow *types.Flow) {
				f.mu.Lock()
				f.failCreateWith = http.StatusServiceUnavailable
				f.mu.Unlock()
			},
			stepID:   "createTerminal",
			contains: "status is in [200, 201], but the response status was 503",
		},
		{
			name: "body equals",
			mutate: func(f *flowFixture, flow *types.Flow) {
				f.mu.Lock()
				f.createState = "pending"
				f.mu.Unlock()
			},
			stepID:   "createTerminal",
			contains: `$.terminal.state equals "created", but it was "pending"`,
		},
		{
			name: "body contains",
			mutate: func(f *flowFixture, flow *types.Flow) {
				flow.Steps[0].Assert = append(flow.Steps[0].Assert,
					types.FlowAssert{Type: "body", Path: "$.data.store.region", Contains: "emea"})
			},
			stepID:   "lookup",
			contains: `$.data.store.region contains "emea", but it was "apac"`,
		},
		{
			name: "body exists",
			mutate: func(f *flowFixture, flow *types.Flow) {
				flow.Steps[0].Assert = append(flow.Steps[0].Assert,
					types.FlowAssert{Type: "body", Path: "$.data.store.absent", Exists: true})
			},
			stepID:   "lookup",
			contains: "$.data.store.absent is not present in the response body",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			f := newFlowFixture(t)
			flow := f.provisionFlow()
			testCase.mutate(f, &flow)
			f.install(flow)

			result, err := f.run(flow, map[string]string{"storeCode": "DHK-04"}, nil)
			if err != nil {
				t.Fatalf("a failed assertion is a run outcome, not a refusal: %v", err)
			}
			if result.OK {
				t.Fatal("the flow reported OK with a failed assertion")
			}
			last := result.Steps[len(result.Steps)-1]
			if last.StepID != testCase.stepID {
				t.Fatalf("failed at step %q, want %q", last.StepID, testCase.stepID)
			}
			if !strings.Contains(last.Error, testCase.contains) {
				t.Errorf("step error = %q, want it to contain %q", last.Error, testCase.contains)
			}
			if !strings.Contains(result.Error, testCase.stepID) {
				t.Errorf("result error = %q, want it to name the step", result.Error)
			}
		})
	}
}

// Every assertion on the failing step is reported, not only the first: the run
// log should say what held as well as what did not.
func TestFlowRunReportsEveryAssertionOnTheFailingStep(t *testing.T) {
	f := newFlowFixture(t)
	flow := f.provisionFlow()
	f.mu.Lock()
	f.createState = "pending"
	f.mu.Unlock()
	f.install(flow)

	result, _ := f.run(flow, map[string]string{"storeCode": "DHK-04"}, nil)
	failing := result.Steps[len(result.Steps)-1]
	if len(failing.Assertions) != 2 {
		t.Fatalf("reported %d assertions, want both: %#v", len(failing.Assertions), failing.Assertions)
	}
	if !failing.Assertions[0].OK {
		t.Errorf("the status assertion should have held: %s", failing.Assertions[0].Detail)
	}
	if failing.Assertions[1].OK {
		t.Error("the body assertion should have failed")
	}
}

// --- 6. fail-fast -----------------------------------------------------------

func TestFlowRunStopsAtTheFailingStepAndNeverRunsTheNextOne(t *testing.T) {
	f := newFlowFixture(t)
	flow := f.provisionFlow()
	f.mu.Lock()
	f.failCreateWith = http.StatusServiceUnavailable
	f.mu.Unlock()
	f.install(flow)

	result, err := f.run(flow, map[string]string{"storeCode": "DHK-04"}, nil)
	if err != nil {
		t.Fatalf("runFlow: %v", err)
	}
	if result.OK {
		t.Fatal("the flow reported OK")
	}
	if len(result.Steps) != 2 {
		t.Fatalf("reported %d steps, want 2 — step 3 must not appear at all", len(result.Steps))
	}
	for _, request := range f.recorded() {
		if request.path == "/activate" {
			t.Fatal("step 3 ran after step 2 failed")
		}
	}
	if len(result.Outputs) != 0 {
		t.Errorf("outputs = %#v, want none: a flow that stopped has no honest value for them", result.Outputs)
	}
}

// --- 7. the secret-shadow refusal -------------------------------------------

func TestFlowRunRefusesAFlowScopeNameThatShadowsASecret(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*types.Flow)
	}{
		{"a step var", func(flow *types.Flow) { flow.Steps[0].Vars["apiToken"] = "agent-chosen" }},
		{"an extraction", func(flow *types.Flow) {
			flow.Steps[0].Extract[0].Name = "apiToken"
		}},
		{"an input", func(flow *types.Flow) {
			flow.Inputs = append(flow.Inputs, types.FlowInput{Name: "apiToken"})
		}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			f := newFlowFixture(t)
			flow := f.provisionFlow()
			testCase.mutate(&flow)
			f.install(flow)

			_, err := f.run(flow, map[string]string{"storeCode": "DHK-04"}, nil)
			if err == nil {
				t.Fatal("a flow-scope name shadowing a secret ran")
			}
			if !strings.Contains(err.Error(), "apiToken") || !strings.Contains(err.Error(), "secret") {
				t.Errorf("error = %v, want it to name the variable and say why", err)
			}
			if strings.Contains(err.Error(), flowSentinelToken) {
				t.Error("the refusal leaked the secret's value")
			}
			if len(f.recorded()) != 0 {
				t.Errorf("the refusal happened after a request went out: %#v", f.recorded())
			}
		})
	}
}

// --- 8. the step guard ------------------------------------------------------

func TestFlowRunCallsTheStepGuardBeforeEachStepWithItsOverrides(t *testing.T) {
	f := newFlowFixture(t)
	flow := f.provisionFlow()
	f.install(flow)

	type guardCall struct {
		index     int
		requestID string
		overrides map[string]string
	}
	calls := []guardCall{}
	guard := func(index int, requestID string, overrides map[string]string) error {
		copied := map[string]string{}
		for name, value := range overrides {
			copied[name] = value
		}
		calls = append(calls, guardCall{index: index, requestID: requestID, overrides: copied})
		return nil
	}

	result, err := f.run(flow, map[string]string{"storeCode": "DHK-04"}, guard)
	if err != nil {
		t.Fatalf("runFlow: %v", err)
	}
	if !result.OK {
		t.Fatalf("result.OK is false: %s", result.Error)
	}
	if len(calls) != 3 {
		t.Fatalf("the guard was called %d times, want once per step", len(calls))
	}
	if calls[0].index != 0 || calls[0].requestID != f.lookupID {
		t.Errorf("call 1 = %#v, want step 0 and the lookup request", calls[0])
	}
	if calls[0].overrides["code"] != "DHK-04" {
		t.Errorf("call 1 overrides = %#v, want the interpolated input", calls[0].overrides)
	}
	// The guard sees the RESOLVED overrides for step 2, which is the point: it
	// has to decide about the request that is actually going to be sent.
	if calls[1].overrides["storeId"] != "store_42" {
		t.Errorf("call 2 overrides = %#v, want step 1's extraction", calls[1].overrides)
	}
	if calls[2].requestID != f.activateID {
		t.Errorf("call 3 = %#v, want the activate request", calls[2])
	}
}

func TestFlowRunStopsWhenTheStepGuardRefuses(t *testing.T) {
	f := newFlowFixture(t)
	flow := f.provisionFlow()
	f.install(flow)

	denied := errors.New("host not approved")
	guard := func(index int, requestID string, overrides map[string]string) error {
		if index == 1 {
			return denied
		}
		return nil
	}

	result, err := f.run(flow, map[string]string{"storeCode": "DHK-04"}, guard)
	if !errors.Is(err, denied) {
		t.Fatalf("err = %v, want the guard's own error so a caller can match on it", err)
	}
	if result.OK {
		t.Fatal("the flow reported OK")
	}
	// The report still carries what ran before the refusal.
	if len(result.Steps) != 2 {
		t.Fatalf("reported %d steps, want the completed one plus the refused one", len(result.Steps))
	}
	if !strings.Contains(result.Steps[1].Error, "host not approved") {
		t.Errorf("step 2 error = %q", result.Steps[1].Error)
	}
	recorded := f.recorded()
	if len(recorded) != 1 || recorded[0].path != "/graphql" {
		t.Errorf("the guarded step was sent anyway: %#v", recorded)
	}
}

// --- 9. progress ------------------------------------------------------------

func TestFlowRunEmitsProgressForEveryStepInOrder(t *testing.T) {
	f := newFlowFixture(t)
	flow := f.provisionFlow()
	f.install(flow)

	if _, err := f.run(flow, map[string]string{"storeCode": "DHK-04"}, nil); err != nil {
		t.Fatalf("runFlow: %v", err)
	}
	events := f.progressEvents()
	want := []struct {
		stepID string
		state  string
	}{
		{"lookup", "running"}, {"lookup", "passed"},
		{"createTerminal", "running"}, {"createTerminal", "passed"},
		{"activate", "running"}, {"activate", "passed"},
	}
	if len(events) != len(want) {
		t.Fatalf("emitted %d events, want %d: %#v", len(events), len(want), events)
	}
	for index, expected := range want {
		if events[index].StepID != expected.stepID || events[index].State != expected.state {
			t.Errorf("event %d = %s/%s, want %s/%s", index, events[index].StepID, events[index].State, expected.stepID, expected.state)
		}
		if events[index].FlowID != flow.ID || events[index].CollectionID != f.collectionID {
			t.Errorf("event %d = %#v, want it to name the flow and collection", index, events[index])
		}
		if events[index].StepCount != 3 {
			t.Errorf("event %d stepCount = %d, want 3", index, events[index].StepCount)
		}
	}
}

func TestFlowRunEmitsAFailedProgressEventAndThenStops(t *testing.T) {
	f := newFlowFixture(t)
	flow := f.provisionFlow()
	f.mu.Lock()
	f.failCreateWith = http.StatusServiceUnavailable
	f.mu.Unlock()
	f.install(flow)

	if _, err := f.run(flow, map[string]string{"storeCode": "DHK-04"}, nil); err != nil {
		t.Fatalf("runFlow: %v", err)
	}
	events := f.progressEvents()
	if len(events) != 4 {
		t.Fatalf("emitted %d events, want 4 — nothing for the step that never ran: %#v", len(events), events)
	}
	if events[3].StepID != "createTerminal" || events[3].State != "failed" {
		t.Errorf("last event = %#v, want createTerminal/failed", events[3])
	}
}

// --- 10. the ids ------------------------------------------------------------

func TestFlowRunNamesWhatIsMissingWhenAnIDIsWrong(t *testing.T) {
	f := newFlowFixture(t)
	flow := f.provisionFlow()
	f.install(flow)

	if _, err := f.app.runFlowProvenance(context.Background(), uiSendProvenance(), f.collectionID, "flow_nope", "", nil, nil); err == nil {
		t.Error("an unknown flow id ran")
	} else if !strings.Contains(err.Error(), "flow_nope") {
		t.Errorf("error = %v, want it to echo the id", err)
	}

	if _, err := f.app.runFlowProvenance(context.Background(), uiSendProvenance(), f.collectionID, flow.ID, "env-nope", map[string]string{"storeCode": "x"}, nil); err == nil {
		t.Error("an unknown environment id ran")
	} else if !strings.Contains(err.Error(), "env-nope") {
		t.Errorf("error = %v, want it to echo the id", err)
	}
}

// A flow whose step names a request that has since been deleted is refused
// before the first request goes out, not part way through the chain.
func TestFlowRunRevalidatesAgainstTheCollectionBeforeSendingAnything(t *testing.T) {
	f := newFlowFixture(t)
	flow := f.provisionFlow()
	flow.Steps[2].RequestID = "req_deleted_yesterday"
	f.install(flow)

	_, err := f.run(flow, map[string]string{"storeCode": "DHK-04"}, nil)
	if err == nil {
		t.Fatal("a flow naming a missing request ran")
	}
	if !strings.Contains(err.Error(), "req_deleted_yesterday") {
		t.Errorf("error = %v, want it to name the request id", err)
	}
	if len(f.recorded()) != 0 {
		t.Errorf("steps ran before validation: %#v", f.recorded())
	}
}

// --- 11. the binding --------------------------------------------------------

func TestRunFlowBindingRunsWithNoGuard(t *testing.T) {
	f := newFlowFixture(t)
	flow := f.provisionFlow()
	f.install(flow)

	result, err := f.app.RunFlow(f.collectionID, flow.ID, "", map[string]string{"storeCode": "DHK-04"})
	if err != nil {
		t.Fatalf("RunFlow: %v", err)
	}
	if !result.OK {
		t.Fatalf("result.OK is false: %s", result.Error)
	}
	if got := result.Outputs["terminalId"]; got != "term_7" {
		t.Errorf("outputs = %#v", result.Outputs)
	}
}
