package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// The flow tier against a fake Backend: the three things this package is
// responsible for. The arguments reach the Backend intact (ids trimmed, inputs
// an object of strings, the optional environmentId absent rather than invented),
// the schema an agent reads declares the same rules validate enforces, and a
// refusal reaches the agent verbatim and the audit as a refusal.
//
// Whether a flow's extractions are masked, or a denial is the right decision, is
// internal/core's job and is tested there — this package's job is to not undo
// either.

// flowFixtureState is the flow half of fixtureBackend (declared there so the
// methods below have somewhere to record what they saw).
type flowFixtureState struct {
	// summaries and details, when nil, fall back to the defaults below, so a
	// test only sets what it wants to change.
	summaries []FlowSummary
	details   map[string]FlowDetail

	outcome FlowRunOutcome
	// runErr stands in for a refusal (wrapping ErrDenied) or a plain failure.
	runErr error

	listCalls int
	runCalls  int

	lastCollectionID   string
	lastFlowID         string
	lastRunParams      RunFlowParams
	lastRunHadDeadline bool
	lastRunTimeout     time.Duration
}

// The fixture flow is the document's canonical example, already redacted the way
// the contract requires: the step var that names a secret carries the TEMPLATE,
// because flow scope never resolves one.
func defaultFlowSummaries() []FlowSummary {
	return []FlowSummary{
		{
			ID: "flow_provision", Name: "Provision POS terminal",
			Description: "GraphQL lookup -> create terminal -> activate",
			StepCount:   3,
			Inputs:      []FlowInput{{Name: "storeCode", Required: true, Description: "Store short code, e.g. DHK-04"}},
		},
		{ID: "flow_smoke", Name: "Smoke test", StepCount: 1},
	}
}

func defaultFlowDetail() FlowDetail {
	return FlowDetail{
		FlowSummary: defaultFlowSummaries()[0],
		Steps: []FlowStep{
			{
				ID: "lookup", RequestID: "req_graphql_store",
				Vars:    map[string]string{"code": "{{storeCode}}", "token": "{{apiToken}}"},
				Extract: []FlowExtract{{Name: "storeId", From: "body", Path: "$.data.store.id"}},
				Assert:  []FlowAssert{{Type: "status", Equals: float64(200)}},
			},
			{
				ID: "createTerminal", RequestID: "req_create",
				Vars:    map[string]string{"storeId": "{{storeId}}"},
				Extract: []FlowExtract{{Name: "terminalId", From: "body", Path: "$.terminal.id"}},
				Assert:  []FlowAssert{{Type: "status", In: []int{200, 201}}, {Type: "body", Path: "$.terminal.state", Equals: "created"}},
			},
			{ID: "activate", RequestID: "req_activate"},
		},
		Outputs: []FlowOutput{{Name: "terminalId", Value: "{{terminalId}}"}},
	}
}

func defaultFlowOutcome() FlowRunOutcome {
	return FlowRunOutcome{
		OK: true,
		Steps: []FlowStepOutcome{
			{StepID: "lookup", RequestID: "req_graphql_store", Status: 200, DurationMs: 31,
				Extracted:  map[string]string{"storeId": "store_42"},
				Assertions: []FlowAssertionOutcome{{OK: true, Detail: "status equals 200"}}},
			{StepID: "createTerminal", RequestID: "req_create", Status: 201, DurationMs: 44,
				Extracted: map[string]string{"terminalId": "trm_10"}},
			{StepID: "activate", RequestID: "req_activate", Status: 200, DurationMs: 12},
		},
		Outputs: map[string]string{"terminalId": "trm_10"},
	}
}

func (backend *fixtureBackend) ListFlows(collectionID string) ([]FlowSummary, error) {
	if err := backend.gate(); err != nil {
		return nil, err
	}
	backend.flows.listCalls++
	backend.flows.lastCollectionID = collectionID
	if backend.flows.summaries != nil {
		return backend.flows.summaries, nil
	}
	if collectionID != "col_pos" {
		return nil, errors.New("no collection with id " + collectionID + "; call list_collections for the ids that exist")
	}
	return defaultFlowSummaries(), nil
}

func (backend *fixtureBackend) GetFlow(collectionID, flowID string) (FlowDetail, error) {
	if err := backend.gate(); err != nil {
		return FlowDetail{}, err
	}
	backend.flows.lastCollectionID = collectionID
	backend.flows.lastFlowID = flowID
	if backend.flows.details != nil {
		detail, known := backend.flows.details[collectionID+"/"+flowID]
		if !known {
			return FlowDetail{}, errors.New("no flow with id " + flowID + " in collection " + collectionID)
		}
		return detail, nil
	}
	if flowID != "flow_provision" {
		return FlowDetail{}, fmt.Errorf("no flow with id %q in collection %q; call list_flows for the ids that exist", flowID, collectionID)
	}
	return defaultFlowDetail(), nil
}

// RunFlow records what it was asked to run, including whether the context it was
// given carried a deadline of its own.
func (backend *fixtureBackend) RunFlow(ctx context.Context, params RunFlowParams) (FlowRunOutcome, error) {
	if err := backend.gate(); err != nil {
		return FlowRunOutcome{}, err
	}
	backend.flows.runCalls++
	backend.flows.lastRunParams = params
	if deadline, ok := ctx.Deadline(); ok {
		backend.flows.lastRunHadDeadline = true
		backend.flows.lastRunTimeout = time.Until(deadline)
	}
	if backend.flows.runErr != nil {
		return backend.flows.outcome, backend.flows.runErr
	}
	if backend.flows.outcome.Steps == nil {
		return defaultFlowOutcome(), nil
	}
	return backend.flows.outcome, nil
}

// --- list_flows / get_flow ---------------------------------------------------

func TestListFlowsReturnsRowsWithTheirDeclaredInputs(t *testing.T) {
	backend := newFixtureBackend()
	server := newTestServer(t, backend)

	var flows []FlowSummary
	decodePayload(t, callTool(t, server, "list_flows", map[string]any{"collectionId": "  col_pos  "}), &flows)

	// Trimmed: an id padded by a client is the same id.
	if backend.flows.lastCollectionID != "col_pos" {
		t.Fatalf("backend saw collectionId %q", backend.flows.lastCollectionID)
	}
	if len(flows) != 2 {
		t.Fatalf("got %d flows, want 2: %+v", len(flows), flows)
	}
	if flows[0].ID != "flow_provision" || flows[0].StepCount != 3 {
		t.Fatalf("row lost fields: %+v", flows[0])
	}
	// The inputs are on the LIST row for a reason: an agent composes the
	// run_flow call from it without a second round trip.
	if len(flows[0].Inputs) != 1 || flows[0].Inputs[0].Name != "storeCode" || !flows[0].Inputs[0].Required {
		t.Fatalf("declared inputs did not survive the row: %+v", flows[0].Inputs)
	}
}

func TestListFlowsWithNoFlowsMarshalsAsAnEmptyArray(t *testing.T) {
	backend := newFixtureBackend()
	backend.flows.summaries = []FlowSummary{}
	server := newTestServer(t, backend)

	result := callTool(t, server, "list_flows", map[string]any{"collectionId": "col_pos"})
	if result.IsError {
		t.Fatalf("list_flows failed: %s", result.Content[0].Text)
	}
	if result.Content[0].Text != "[]" {
		t.Fatalf("payload = %q, want []", result.Content[0].Text)
	}
}

// get_flow's whole safety argument is that it resolves nothing: a step var that
// names a secret has to arrive as the literal {{template}} it was authored as.
// This package cannot introduce a resolution, but it can drop or rewrite a
// field, and this is what would notice.
func TestGetFlowReturnsStepsWithTemplatesUnresolved(t *testing.T) {
	backend := newFixtureBackend()
	server := newTestServer(t, backend)

	var detail FlowDetail
	decodePayload(t, callTool(t, server, "get_flow", map[string]any{"collectionId": "col_pos", "flowId": "flow_provision"}), &detail)

	if backend.flows.lastCollectionID != "col_pos" || backend.flows.lastFlowID != "flow_provision" {
		t.Fatalf("backend saw %q/%q", backend.flows.lastCollectionID, backend.flows.lastFlowID)
	}
	if detail.ID != "flow_provision" || detail.StepCount != 3 || len(detail.Steps) != 3 {
		t.Fatalf("detail lost fields: %+v", detail)
	}
	if detail.Steps[0].Vars["token"] != "{{apiToken}}" {
		t.Errorf("the step var naming a secret was rewritten to %q; it must travel as the template", detail.Steps[0].Vars["token"])
	}
	if detail.Steps[0].Vars["code"] != "{{storeCode}}" {
		t.Errorf("the step var naming an input was rewritten to %q", detail.Steps[0].Vars["code"])
	}
	if detail.Steps[0].RequestID != "req_graphql_store" {
		t.Errorf("the step's requestId did not survive: %q — it is what get_request is called with next", detail.Steps[0].RequestID)
	}
	if len(detail.Steps[1].Assert) != 2 || detail.Steps[1].Assert[1].Equals != "created" {
		t.Errorf("assertions did not survive: %+v", detail.Steps[1].Assert)
	}
	if len(detail.Steps[1].Assert[0].In) != 2 {
		t.Errorf("the status-in assertion lost its list: %+v", detail.Steps[1].Assert[0])
	}
	if len(detail.Outputs) != 1 || detail.Outputs[0].Value != "{{terminalId}}" {
		t.Errorf("outputs did not survive: %+v", detail.Outputs)
	}
}

// An unknown id is the Backend's own error and travels as an isError result. It
// has to name the id and the tool that lists the real ones — the agent reading
// it is about to compose the retry.
func TestGetFlowUnknownIdNamesTheIdAndTheWayOut(t *testing.T) {
	server := newTestServer(t, newFixtureBackend())
	result := callTool(t, server, "get_flow", map[string]any{"collectionId": "col_pos", "flowId": "flow_nope"})
	if !result.IsError {
		t.Fatal("an unknown flowId succeeded")
	}
	text := result.Content[0].Text
	if !strings.Contains(text, "flow_nope") || !strings.Contains(text, "list_flows") {
		t.Fatalf("error should name the id and the way out: %s", text)
	}
}

func TestFlowToolsRequireTheirIds(t *testing.T) {
	server := newTestServer(t, newFixtureBackend())
	for _, testCase := range []struct {
		tool      string
		arguments map[string]any
		field     string
	}{
		{"list_flows", map[string]any{}, "collectionId"},
		{"list_flows", map[string]any{"collectionId": "   "}, "collectionId"},
		{"get_flow", map[string]any{"flowId": "flow_provision"}, "collectionId"},
		{"get_flow", map[string]any{"collectionId": "col_pos"}, "flowId"},
		{"get_flow", map[string]any{"collectionId": "col_pos", "flowId": " "}, "flowId"},
		{"run_flow", map[string]any{"flowId": "flow_provision"}, "collectionId"},
		{"run_flow", map[string]any{"collectionId": "col_pos"}, "flowId"},
	} {
		result := callTool(t, server, testCase.tool, testCase.arguments)
		if !result.IsError {
			t.Fatalf("%s with %v succeeded, want isError", testCase.tool, testCase.arguments)
		}
		if !strings.Contains(result.Content[0].Text, testCase.field) {
			t.Fatalf("%s error does not name %q: %s", testCase.tool, testCase.field, result.Content[0].Text)
		}
	}
}

// --- run_flow ----------------------------------------------------------------

func TestRunFlowReturnsThePerStepOutcome(t *testing.T) {
	backend := newFixtureBackend()
	server := newTestServer(t, backend)

	var outcome FlowRunOutcome
	decodePayload(t, callTool(t, server, "run_flow", map[string]any{
		"collectionId": "col_pos", "flowId": "flow_provision",
		"inputs": map[string]any{"storeCode": "DHK-04"},
	}), &outcome)

	if backend.flows.runCalls != 1 {
		t.Fatalf("the backend ran %d times, want 1", backend.flows.runCalls)
	}
	if !outcome.OK || len(outcome.Steps) != 3 {
		t.Fatalf("outcome lost fields: %+v", outcome)
	}
	if outcome.Steps[0].Extracted["storeId"] != "store_42" {
		t.Errorf("extractions did not survive: %+v", outcome.Steps[0])
	}
	if len(outcome.Steps[0].Assertions) != 1 || !outcome.Steps[0].Assertions[0].OK {
		t.Errorf("assertion results did not survive: %+v", outcome.Steps[0].Assertions)
	}
	if outcome.Outputs["terminalId"] != "trm_10" {
		t.Errorf("declared outputs did not survive: %+v", outcome.Outputs)
	}
}

func TestRunFlowForwardsIdsEnvironmentAndInputs(t *testing.T) {
	backend := newFixtureBackend()
	server := newTestServer(t, backend)

	if result := callTool(t, server, "run_flow", map[string]any{
		"collectionId":  "  col_pos  ",
		"flowId":        "  flow_provision  ",
		"environmentId": "env_stage",
		"inputs":        map[string]any{"storeCode": "DHK-04", "dryRun": "true"},
	}); result.IsError {
		t.Fatalf("run_flow failed: %s", result.Content[0].Text)
	}

	params := backend.flows.lastRunParams
	if params.CollectionID != "col_pos" || params.FlowID != "flow_provision" || params.EnvironmentID != "env_stage" {
		t.Fatalf("backend saw %+v", params)
	}
	if len(params.Inputs) != 2 || params.Inputs["storeCode"] != "DHK-04" || params.Inputs["dryRun"] != "true" {
		t.Fatalf("inputs = %+v", params.Inputs)
	}
}

// An omitted environmentId means "whichever environment is active", and omitted
// inputs mean "the flow declares none" — both reach the Backend as the empty
// value rather than as a validation failure.
func TestRunFlowWithoutOptionalArgumentsLetsTheBackendDecide(t *testing.T) {
	for _, testCase := range []struct {
		name      string
		arguments map[string]any
	}{
		{"neither optional argument", map[string]any{"collectionId": "col_pos", "flowId": "flow_provision"}},
		{"an empty inputs object", map[string]any{"collectionId": "col_pos", "flowId": "flow_provision", "inputs": map[string]any{}}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			backend := newFixtureBackend()
			server := newTestServer(t, backend)
			if result := callTool(t, server, "run_flow", testCase.arguments); result.IsError {
				t.Fatalf("run_flow failed: %s", result.Content[0].Text)
			}
			if backend.flows.runCalls != 1 {
				t.Fatalf("the backend ran %d times; the call never got past validation", backend.flows.runCalls)
			}
			if backend.flows.lastRunParams.EnvironmentID != "" {
				t.Errorf("environmentId = %q, want empty", backend.flows.lastRunParams.EnvironmentID)
			}
			if backend.flows.lastRunParams.Inputs != nil {
				t.Errorf("inputs = %+v, want nil", backend.flows.lastRunParams.Inputs)
			}
		})
	}
}

func TestRunFlowRejectsNonStringInputValuesNamingTheKey(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		inputs map[string]any
		key    string
		want   string
	}{
		{"a number", map[string]any{"storeCode": 42}, "storeCode", "a number"},
		{"a boolean", map[string]any{"dryRun": true}, "dryRun", "a boolean"},
		{"a nested object", map[string]any{"filter": map[string]any{"a": "b"}}, "filter", "an object"},
		{"an array", map[string]any{"codes": []any{"a", "b"}}, "codes", "an array"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			backend := newFixtureBackend()
			server := newTestServer(t, backend)
			result := callTool(t, server, "run_flow", map[string]any{
				"collectionId": "col_pos", "flowId": "flow_provision", "inputs": testCase.inputs,
			})
			if !result.IsError {
				t.Fatalf("inputs %+v were accepted", testCase.inputs)
			}
			text := result.Content[0].Text
			if !strings.Contains(text, testCase.key) || !strings.Contains(text, "inputs") || !strings.Contains(text, testCase.want) {
				t.Errorf("error = %s", text)
			}
			if backend.flows.runCalls != 0 {
				t.Errorf("the backend ran anyway (%d times)", backend.flows.runCalls)
			}
		})
	}
}

func TestRunFlowInputsMustBeAnObject(t *testing.T) {
	server := newTestServer(t, newFixtureBackend())
	result := callTool(t, server, "run_flow", map[string]any{
		"collectionId": "col_pos", "flowId": "flow_provision", "inputs": "storeCode=DHK-04",
	})
	if !result.IsError {
		t.Fatal("a string inputs argument was accepted")
	}
	if !strings.Contains(result.Content[0].Text, "inputs") || !strings.Contains(result.Content[0].Text, "object") {
		t.Fatalf("error = %s", result.Content[0].Text)
	}
}

// A flow is several requests, so its budget is its own — and it must be the
// flow's rather than the HTTP client's, or an agent that disconnects after step
// 2 would cancel a chain that has already changed something real.
func TestRunFlowGivesTheBackendItsOwnAndLongerDeadline(t *testing.T) {
	backend := newFixtureBackend()
	server := newTestServer(t, backend)

	if result := callTool(t, server, "run_flow", map[string]any{"collectionId": "col_pos", "flowId": "flow_provision"}); result.IsError {
		t.Fatalf("run_flow failed: %s", result.Content[0].Text)
	}
	if !backend.flows.lastRunHadDeadline {
		t.Fatal("the context carried no deadline; a wedged flow would pin the handler goroutine forever")
	}
	if backend.flows.lastRunTimeout > FlowRunTimeout || backend.flows.lastRunTimeout < FlowRunTimeout/2 {
		t.Fatalf("remaining time was %s, want roughly FlowRunTimeout (%s)", backend.flows.lastRunTimeout, FlowRunTimeout)
	}
	if FlowRunTimeout <= RunTimeout {
		t.Fatalf("FlowRunTimeout (%s) is not longer than RunTimeout (%s); a chain of requests needs more room than one of them", FlowRunTimeout, RunTimeout)
	}
}

// A denial is not an error to retry around. The Backend's message explains which
// guard fired and what the user would have to approve, so it reaches the agent
// unchanged — and the audit records it as a refusal rather than one more failed
// call, which is what the panel's refusal count is for.
func TestRunFlowDenialPassesTheMessageThroughAndAuditsAsDenied(t *testing.T) {
	const explanation = `denied: this run would send the secret "apiToken" to api.other.example, which no request in the open collections sends it to. Ask the user to approve that host in LiteAPI`
	backend := newFixtureBackend()
	backend.flows.runErr = fmt.Errorf("%w: %s", ErrDenied, explanation)
	server, log := newAuditedServer(t, backend)

	result := callTool(t, server, "run_flow", map[string]any{"collectionId": "col_pos", "flowId": "flow_provision"})
	if !result.IsError {
		t.Fatal("a denied flow came back as a success")
	}
	if !strings.Contains(result.Content[0].Text, explanation) {
		t.Fatalf("the denial's explanation did not reach the agent intact: %s", result.Content[0].Text)
	}
	entry := log.only(t)
	if entry.Tool != "run_flow" || entry.Outcome != outcomeDenied {
		t.Fatalf("audit entry = %+v, want run_flow/%s", entry, outcomeDenied)
	}
}

// A validation failure never reaches the Backend, and is audited as an error —
// the probing an audit exists to show, kept apart from the refusals.
func TestRunFlowValidationFailureAuditsAsError(t *testing.T) {
	backend := newFixtureBackend()
	server, log := newAuditedServer(t, backend)

	result := callTool(t, server, "run_flow", map[string]any{"collectionId": "col_pos"})
	if !result.IsError {
		t.Fatal("run_flow without a flowId succeeded")
	}
	if backend.flows.runCalls != 0 {
		t.Errorf("the backend ran despite the validation failure (%d times)", backend.flows.runCalls)
	}
	entry := log.only(t)
	if entry.Tool != "run_flow" || entry.Outcome != outcomeError {
		t.Fatalf("audit entry = %+v, want run_flow/%s", entry, outcomeError)
	}
}

// A plain failure is not a denial: auditing a network timeout as "denied" would
// make the refusal count meaningless.
func TestRunFlowOrdinaryFailureAuditsAsError(t *testing.T) {
	backend := newFixtureBackend()
	backend.flows.runErr = errors.New("dial tcp: connection refused")
	server, log := newAuditedServer(t, backend)

	result := callTool(t, server, "run_flow", map[string]any{"collectionId": "col_pos", "flowId": "flow_provision"})
	if !result.IsError || !strings.Contains(result.Content[0].Text, "connection refused") {
		t.Fatalf("result = %+v", result)
	}
	if outcome := log.only(t).Outcome; outcome != outcomeError {
		t.Fatalf("outcome = %q, want %q", outcome, outcomeError)
	}
}

// --- the schemas an agent reads before composing a call ----------------------

func TestFlowToolSchemasDeclareWhatValidationEnforces(t *testing.T) {
	for _, testCase := range []struct {
		tool     string
		required []string
	}{
		{"list_flows", []string{"collectionId"}},
		{"get_flow", []string{"collectionId", "flowId"}},
		{"run_flow", []string{"collectionId", "flowId"}},
	} {
		entry, known := lookupTool(testCase.tool)
		if !known {
			t.Fatalf("%s is not registered", testCase.tool)
		}
		if len(entry.InputSchema.Required) != len(testCase.required) {
			t.Fatalf("%s required = %v, want %v", testCase.tool, entry.InputSchema.Required, testCase.required)
		}
		for index, name := range testCase.required {
			if entry.InputSchema.Required[index] != name {
				t.Errorf("%s required[%d] = %q, want %q", testCase.tool, index, entry.InputSchema.Required[index], name)
			}
		}
		for _, name := range testCase.required {
			if property, declared := entry.InputSchema.Properties[name]; !declared || property.Description == "" {
				t.Errorf("%s does not describe its %q property", testCase.tool, name)
			}
		}
	}

	entry, _ := lookupTool("run_flow")
	inputs, declared := entry.InputSchema.Properties["inputs"]
	if !declared {
		t.Fatal("run_flow does not declare an inputs property")
	}
	if inputs.Type != "object" || inputs.AdditionalProperties == nil || inputs.AdditionalProperties.Type != "string" {
		t.Errorf("inputs schema = %+v, want an object of strings", inputs)
	}
	if _, declared := entry.InputSchema.Properties["environmentId"]; !declared {
		t.Error("run_flow does not declare an environmentId property")
	}
}

// The description is the agent's only warning about the three things it cannot
// discover by trying: a run can be REFUSED and retrying is not the way out, an
// input the flow does not declare is refused rather than ignored, and a flow
// cannot read a secret.
func TestRunFlowDescriptionWarnsAboutDenialsInputsAndSecrets(t *testing.T) {
	entry, known := lookupTool("run_flow")
	if !known {
		t.Fatal("run_flow is not registered")
	}
	description := strings.ToLower(entry.Description)
	for _, phrase := range []string{"denied", "approve", "never to retry", "declares", "undeclared", "secret", "list_flows"} {
		if !strings.Contains(description, phrase) {
			t.Errorf("run_flow's description never mentions %q", phrase)
		}
	}
}

func TestGetFlowDescriptionSaysTemplatesAreNotResolved(t *testing.T) {
	entry, known := lookupTool("get_flow")
	if !known {
		t.Fatal("get_flow is not registered")
	}
	description := strings.ToLower(entry.Description)
	for _, phrase := range []string{"{{templates}}", "never", "secret", "get_request"} {
		if !strings.Contains(description, phrase) {
			t.Errorf("get_flow's description never mentions %q", phrase)
		}
	}
}

// Discovery order is part of the contract with the agent: the flow read tools
// sit with the other read tools, and run_flow is last because it is the tool you
// reach for once you have read the flow.
func TestFlowToolsAreRegisteredWithTheirTier(t *testing.T) {
	position := map[string]int{}
	for index, entry := range toolRegistry {
		position[entry.Name] = index
	}
	for _, name := range []string{"list_flows", "get_flow", "run_flow"} {
		if _, registered := position[name]; !registered {
			t.Fatalf("%s is not registered", name)
		}
	}
	if position["list_flows"] > position["get_flow"] {
		t.Error("list_flows is listed after get_flow; an agent discovers the list first")
	}
	if position["get_flow"] > position["run_request"] {
		t.Error("the flow read tools are listed after the run tier")
	}
	if position["run_flow"] != len(toolRegistry)-1 {
		t.Errorf("run_flow is at %d of %d; it is the last tool an agent reaches for", position["run_flow"], len(toolRegistry))
	}
}
