package mcpserver

// The write tier and describe_usage, tested against the fake Backend.
//
// WHAT THIS PACKAGE CAN AND CANNOT PROVE. The refusals that matter — the tier
// gate, the script rule, the secret-definition rule, the authoring host guard —
// are enforced in internal/core, because that is where the preference and the
// collections live, and they are proved there (mcp_write_test.go). What this
// file proves is the half that is this package's: that a call's arguments reach
// the Backend intact and in the right shape, that a bad argument is refused
// with a message an agent can act on, that a denial is audited as a denial
// rather than as an error, and that describe_usage tells an agent the truth
// about a server it has never seen.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// writeFixtureState is the write half of fixtureBackend, declared here next to
// the tests that read it, exactly as flowFixtureState is.
type writeFixtureState struct {
	// enabled stands in for the user's preference. The fixture refuses like
	// the real Backend does — an ErrDenied-wrapped error naming Settings — so
	// the audit outcome for a gated call can be measured without a real App.
	enabled     bool
	writeErr    error
	tierErr     error
	summary     RequestSummary
	flowSummary FlowSummary

	createCalls     int
	updateCalls     int
	createFlowCalls int
	updateFlowCalls int
	tierCalls       int

	lastCreate     CreateRequestParams
	lastUpdate     UpdateRequestParams
	lastCreateFlow CreateFlowParams
	lastUpdateFlow UpdateFlowParams

	// lastWriteHadDeadline records whether the context the tool handed over
	// carried one: an authoring call can block on a user's approval, so the
	// handler must not pass a context that never ends.
	lastWriteHadDeadline bool
	lastWriteTimeout     time.Duration
}

func (backend *fixtureBackend) WriteTierEnabled() (bool, error) {
	backend.writes.tierCalls++
	if backend.writes.tierErr != nil {
		return false, backend.writes.tierErr
	}
	return backend.writes.enabled, nil
}

// writeGate is the fixture's stand-in for internal/core's tier gate.
func (backend *fixtureBackend) writeGate(tool string) error {
	if err := backend.gate(); err != nil {
		return err
	}
	if !backend.writes.enabled {
		return fmt.Errorf("%w: %s needs LiteAPI's write tier, which is off. Ask the user to turn it on in Settings → AI access", ErrDenied, tool)
	}
	return backend.writes.writeErr
}

func (backend *fixtureBackend) noteWriteContext(ctx context.Context) {
	if deadline, ok := ctx.Deadline(); ok {
		backend.writes.lastWriteHadDeadline = true
		backend.writes.lastWriteTimeout = time.Until(deadline)
	}
}

func (backend *fixtureBackend) CreateRequest(ctx context.Context, params CreateRequestParams) (RequestSummary, error) {
	backend.writes.createCalls++
	backend.writes.lastCreate = params
	backend.noteWriteContext(ctx)
	if err := backend.writeGate("create_request"); err != nil {
		return RequestSummary{}, err
	}
	return backend.writes.summary, nil
}

func (backend *fixtureBackend) UpdateRequest(ctx context.Context, params UpdateRequestParams) (RequestSummary, error) {
	backend.writes.updateCalls++
	backend.writes.lastUpdate = params
	backend.noteWriteContext(ctx)
	if err := backend.writeGate("update_request"); err != nil {
		return RequestSummary{}, err
	}
	return backend.writes.summary, nil
}

func (backend *fixtureBackend) CreateFlow(params CreateFlowParams) (FlowSummary, error) {
	backend.writes.createFlowCalls++
	backend.writes.lastCreateFlow = params
	if err := backend.writeGate("create_flow"); err != nil {
		return FlowSummary{}, err
	}
	return backend.writes.flowSummary, nil
}

func (backend *fixtureBackend) UpdateFlow(params UpdateFlowParams) (FlowSummary, error) {
	backend.writes.updateFlowCalls++
	backend.writes.lastUpdateFlow = params
	if err := backend.writeGate("update_flow"); err != nil {
		return FlowSummary{}, err
	}
	return backend.writes.flowSummary, nil
}

// newWritableFixture is the fixture with the tier unlocked and a plausible
// answer for each write.
func newWritableFixture() *fixtureBackend {
	backend := newFixtureBackend()
	backend.writes.enabled = true
	backend.writes.summary = RequestSummary{
		ID: "req_new", CollectionID: "col_pos", Name: "Create terminal",
		Type: "http", Method: "POST", URL: "{{baseUrl}}/terminals",
	}
	backend.writes.flowSummary = FlowSummary{ID: "flow_new", Name: "Provision POS terminal", StepCount: 2}
	return backend
}

// --- registry placement -------------------------------------------------------

// The five Phase 4 tools are in tools/list, with schemas that a client can read
// and a placement that puts the write tier last. Placement is not cosmetic: an
// agent reads the list top-down, and the tools it should reach for first — the
// read tier, then describe_usage, then the runs — should be the ones it meets
// first.
func TestToolsListCarriesThePhase4Tools(t *testing.T) {
	server := newTestServer(t, newFixtureBackend())
	response := rpcCall(t, server, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`, 200)
	var payload struct {
		Tools []struct {
			Name        string      `json:"name"`
			Description string      `json:"description"`
			InputSchema inputSchema `json:"inputSchema"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(response.Result, &payload); err != nil {
		t.Fatalf("decode tools/list: %v", err)
	}

	position := map[string]int{}
	for index, tool := range payload.Tools {
		position[tool.Name] = index
	}
	for _, name := range []string{"describe_usage", "create_request", "update_request", "create_flow", "update_flow"} {
		if _, listed := position[name]; !listed {
			t.Fatalf("tools/list does not include %q", name)
		}
	}
	if position["describe_usage"] > position["run_request"] {
		t.Errorf("describe_usage is listed after the run tier; it is read-tier work an agent should meet first")
	}
	for _, name := range []string{"create_request", "update_request", "create_flow", "update_flow"} {
		if position[name] < position["run_flow"] {
			t.Errorf("%q is listed before the run tier; the write tier comes last", name)
		}
	}

	for _, tool := range payload.Tools {
		if tool.InputSchema.Type != "object" || tool.InputSchema.Properties == nil || tool.InputSchema.Required == nil {
			t.Errorf("%s has schema %+v; every tool advertises an object schema with a non-null required list", tool.Name, tool.InputSchema)
		}
		for name, property := range tool.InputSchema.Properties {
			if property.Description == "" {
				t.Errorf("%s.%s has no description; the schema is what an agent composes the call from", tool.Name, name)
			}
			if property.Type == "array" && property.Items == nil {
				t.Errorf("%s.%s is an array with no declared item type", tool.Name, name)
			}
		}
	}

	// The descriptions have to carry the two things an agent cannot discover by
	// trying: that the tier can be off, and that only the user can turn it on.
	for _, tool := range payload.Tools {
		switch tool.Name {
		case "create_request", "update_request", "create_flow", "update_flow":
			if !strings.Contains(tool.Description, "write tier") {
				t.Errorf("%s does not mention the write tier", tool.Name)
			}
			if !strings.Contains(strings.ToLower(tool.Description), "settings") {
				t.Errorf("%s does not tell the agent where the user turns writing on", tool.Name)
			}
		}
	}
}

// --- schema validation --------------------------------------------------------

func TestCreateRequestValidatesItsArguments(t *testing.T) {
	backend := newWritableFixture()
	server := newTestServer(t, backend)

	cases := []struct {
		name    string
		args    map[string]any
		wantSub string
	}{
		{
			name:    "no collectionId",
			args:    map[string]any{"name": "New", "url": "{{baseUrl}}/x"},
			wantSub: `missing required argument "collectionId"`,
		},
		{
			name:    "no url",
			args:    map[string]any{"collectionId": "col_pos", "name": "New"},
			wantSub: `missing required argument "url"`,
		},
		{
			name:    "empty name",
			args:    map[string]any{"collectionId": "col_pos", "name": "  ", "url": "{{baseUrl}}/x"},
			wantSub: `required argument "name" was empty`,
		},
		{
			name:    "headers is not an array",
			args:    map[string]any{"collectionId": "col_pos", "name": "New", "url": "u", "headers": map[string]any{"Accept": "x"}},
			wantSub: `argument "headers" must be an array`,
		},
		{
			name:    "header entry is not an object",
			args:    map[string]any{"collectionId": "col_pos", "name": "New", "url": "u", "headers": []any{"Accept: x"}},
			wantSub: `argument "headers" entry 1 must be an object`,
		},
		{
			name:    "header row has no name",
			args:    map[string]any{"collectionId": "col_pos", "name": "New", "url": "u", "headers": []any{map[string]any{"value": "x"}}},
			wantSub: `argument "headers" entry 1 has no name`,
		},
		{
			name:    "header row has an unknown field",
			args:    map[string]any{"collectionId": "col_pos", "name": "New", "url": "u", "headers": []any{map[string]any{"key": "Accept", "value": "x"}}},
			wantSub: `unknown field "key"`,
		},
		{
			name:    "header row value is a number",
			args:    map[string]any{"collectionId": "col_pos", "name": "New", "url": "u", "headers": []any{map[string]any{"name": "X-Count", "value": 3}}},
			wantSub: `has a value that is a number`,
		},
		{
			name:    "auth is not an object of strings",
			args:    map[string]any{"collectionId": "col_pos", "name": "New", "url": "u", "auth": map[string]any{"mode": true}},
			wantSub: `must be an object of string values`,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			result := callTool(t, server, "create_request", testCase.args)
			if !result.IsError {
				t.Fatalf("the call was accepted: %s", result.Content[0].Text)
			}
			if !strings.Contains(result.Content[0].Text, testCase.wantSub) {
				t.Errorf("error is %q, want it to contain %q", result.Content[0].Text, testCase.wantSub)
			}
		})
	}
	if backend.writes.createCalls != 0 {
		t.Errorf("the Backend was reached %d times; a call that fails validation must never get there", backend.writes.createCalls)
	}
}

// The definition an agent composes reaches the Backend as it wrote it: rows keep
// their order, an omitted enabled means enabled, and a row that declares itself
// secret is CARRIED rather than dropped — the Backend has to see it to refuse it
// with an explanation.
func TestCreateRequestHandsTheWholeDefinitionToTheBackend(t *testing.T) {
	backend := newWritableFixture()
	server := newTestServer(t, backend)

	result := callTool(t, server, "create_request", map[string]any{
		"collectionId": "col_pos",
		"name":         "Create terminal",
		"folderPath":   "Terminals",
		"type":         "http",
		"method":       "post",
		"url":          "{{baseUrl}}/terminals",
		"headers": []any{
			map[string]any{"name": "Content-Type", "value": "application/json"},
			map[string]any{"name": "X-Debug", "value": "1", "enabled": false},
		},
		"params":   []any{map[string]any{"name": "region", "value": "{{region}}"}},
		"vars":     []any{map[string]any{"name": "leaked", "value": "x", "secret": true}},
		"bodyType": "json",
		"body":     `{"storeId":"{{storeId}}"}`,
		"auth":     map[string]any{"mode": "bearer", "token": "{{apiToken}}"},
	})
	if result.IsError {
		t.Fatalf("create_request failed: %s", result.Content[0].Text)
	}

	params := backend.writes.lastCreate
	if params.CollectionID != "col_pos" || params.Name != "Create terminal" || params.FolderPath != "Terminals" {
		t.Errorf("identity did not arrive: %+v", params)
	}
	if params.Method != "post" || params.URL != "{{baseUrl}}/terminals" {
		t.Errorf("method/url did not arrive verbatim: %q %q — normalising is the Backend's job", params.Method, params.URL)
	}
	if len(params.Headers) != 2 || params.Headers[0].Name != "Content-Type" || params.Headers[1].Name != "X-Debug" {
		t.Fatalf("headers = %+v", params.Headers)
	}
	if params.Headers[0].Enabled != nil {
		t.Errorf("an omitted enabled arrived as %v, want nil so the Backend can default it to true", *params.Headers[0].Enabled)
	}
	if params.Headers[1].Enabled == nil || *params.Headers[1].Enabled {
		t.Errorf("enabled:false did not survive: %+v", params.Headers[1])
	}
	if len(params.Vars) != 1 || !params.Vars[0].Secret {
		t.Fatalf("the secret flag was dropped before the Backend could refuse it: %+v", params.Vars)
	}
	if params.BodyType != "json" || params.Body != `{"storeId":"{{storeId}}"}` {
		t.Errorf("body did not arrive: %q / %q", params.BodyType, params.Body)
	}
	if params.Auth["mode"] != "bearer" || params.Auth["token"] != "{{apiToken}}" {
		t.Errorf("auth did not arrive: %+v", params.Auth)
	}

	// A write can block on a user approving a host, so the handler must give it
	// a deadline of its own rather than the HTTP request's context.
	if !backend.writes.lastWriteHadDeadline {
		t.Error("the Backend was given a context with no deadline; an authoring call can block on a user")
	}
	if backend.writes.lastWriteTimeout > WriteTimeout || backend.writes.lastWriteTimeout < WriteTimeout/2 {
		t.Errorf("the write deadline is %v, want about %v", backend.writes.lastWriteTimeout, WriteTimeout)
	}

	var summary RequestSummary
	decodePayload(t, result, &summary)
	if summary.ID != "req_new" {
		t.Errorf("the created id did not come back: %+v", summary)
	}
}

// update_request is a PATCH, and the distinction the Backend needs is between
// "not supplied" and "supplied as empty": an omitted headers keeps the stored
// rows, an empty array clears them.
func TestUpdateRequestDistinguishesOmittedFromEmpty(t *testing.T) {
	backend := newWritableFixture()
	server := newTestServer(t, backend)

	result := callTool(t, server, "update_request", map[string]any{
		"collectionId": "col_pos",
		"requestId":    "req_create",
		"url":          "{{baseUrl}}/terminals/v2",
		"params":       []any{},
		"preScript":    "",
	})
	if result.IsError {
		t.Fatalf("update_request failed: %s", result.Content[0].Text)
	}
	params := backend.writes.lastUpdate
	if params.URL == nil || *params.URL != "{{baseUrl}}/terminals/v2" {
		t.Errorf("url did not arrive: %+v", params.URL)
	}
	if params.Headers != nil {
		t.Errorf("an omitted headers arrived as %+v, want nil so the stored rows are kept", *params.Headers)
	}
	if params.Params == nil {
		t.Fatal("an empty params array arrived as nil; clearing the rows and leaving them alone are different requests")
	}
	if len(*params.Params) != 0 {
		t.Errorf("params = %+v, want empty", *params.Params)
	}
	if params.PreScript == nil || *params.PreScript != "" {
		t.Errorf("an empty preScript arrived as %v; the Backend needs to see that it was supplied", params.PreScript)
	}
	if params.Method != nil || params.Body != nil || params.Auth != nil {
		t.Errorf("fields that were never passed arrived non-nil: %+v", params)
	}
}

// --- flows ---------------------------------------------------------------------

func TestCreateFlowDecodesTheDefinitionStrictly(t *testing.T) {
	backend := newWritableFixture()
	server := newTestServer(t, backend)

	result := callTool(t, server, "create_flow", map[string]any{
		"collectionId": "col_pos",
		"flow": map[string]any{
			"name":   "Provision POS terminal",
			"inputs": []any{map[string]any{"name": "storeCode", "required": true}},
			"steps": []any{
				map[string]any{
					"id": "lookup", "requestId": "req_list",
					"vars":    map[string]any{"token": "{{apiToken}}"},
					"extract": []any{map[string]any{"name": "storeId", "from": "body", "path": "$.data.store.id"}},
					"assert":  []any{map[string]any{"type": "status", "equals": 200}},
				},
			},
			"outputs": []any{map[string]any{"name": "storeId", "value": "{{storeId}}"}},
		},
	})
	if result.IsError {
		t.Fatalf("create_flow failed: %s", result.Content[0].Text)
	}
	flow := backend.writes.lastCreateFlow.Flow
	if flow.Name != "Provision POS terminal" || len(flow.Steps) != 1 {
		t.Fatalf("flow = %+v", flow)
	}
	if flow.Steps[0].Vars["token"] != "{{apiToken}}" {
		t.Errorf("the step var naming a secret is %q, want the literal template — nothing resolves it here",
			flow.Steps[0].Vars["token"])
	}
	if len(flow.Steps[0].Extract) != 1 || flow.Steps[0].Extract[0].Path != "$.data.store.id" {
		t.Errorf("extract did not survive: %+v", flow.Steps[0].Extract)
	}
	if len(flow.Steps[0].Assert) != 1 || fmt.Sprint(flow.Steps[0].Assert[0].Equals) != "200" {
		t.Errorf("assert did not survive: %+v", flow.Steps[0].Assert)
	}
	if len(flow.Inputs) != 1 || !flow.Inputs[0].Required {
		t.Errorf("inputs did not survive: %+v", flow.Inputs)
	}
}

// A misspelled field inside a flow is refused rather than ignored. "asserts"
// for "assert" would otherwise store a step that checks nothing and reports
// green, which is worse than a failed call.
func TestCreateFlowRejectsAnUnknownFieldByName(t *testing.T) {
	backend := newWritableFixture()
	server := newTestServer(t, backend)

	result := callTool(t, server, "create_flow", map[string]any{
		"collectionId": "col_pos",
		"flow": map[string]any{
			"name":  "Typo",
			"steps": []any{map[string]any{"id": "one", "requestId": "req_list", "asserts": []any{}}},
		},
	})
	if !result.IsError {
		t.Fatalf("the misspelled field was accepted: %s", result.Content[0].Text)
	}
	if !strings.Contains(result.Content[0].Text, "asserts") {
		t.Errorf("the error does not name the offending field: %s", result.Content[0].Text)
	}
	if !strings.Contains(result.Content[0].Text, "describe_usage") {
		t.Errorf("the error does not point at the schema: %s", result.Content[0].Text)
	}
	if backend.writes.createFlowCalls != 0 {
		t.Error("a flow that does not decode reached the Backend")
	}
}

func TestUpdateFlowRequiresTheFlowObject(t *testing.T) {
	server := newTestServer(t, newWritableFixture())
	result := callTool(t, server, "update_flow", map[string]any{"collectionId": "col_pos"})
	if !result.IsError {
		t.Fatalf("update_flow ran without a flow: %s", result.Content[0].Text)
	}
	if !strings.Contains(result.Content[0].Text, "flow") {
		t.Errorf("error = %q, want it to name the missing argument", result.Content[0].Text)
	}
}

// --- the gate, as the audit sees it ---------------------------------------------

// A write refused because the tier is off is audited as DENIED, not as an
// error. That split is what lets the user scan the panel and see a rule holding
// rather than a feature breaking — and it works only because the Backend wraps
// ErrDenied and the protocol layer looks for it.
func TestGatedWriteIsAuditedAsDenied(t *testing.T) {
	backend := newFixtureBackend() // write tier off
	server, log := newAuditedServer(t, backend)

	for _, tool := range []string{"create_request", "update_request", "create_flow", "update_flow"} {
		args := map[string]any{"collectionId": "col_pos"}
		switch tool {
		case "create_request":
			args["name"] = "New"
			args["url"] = "{{baseUrl}}/x"
		case "update_request":
			args["requestId"] = "req_create"
		default:
			args["flow"] = map[string]any{"id": "flow_1", "name": "F", "steps": []any{map[string]any{"id": "s", "requestId": "req_list"}}}
		}
		result := callTool(t, server, tool, args)
		if !result.IsError {
			t.Fatalf("%s ran with the write tier off: %s", tool, result.Content[0].Text)
		}
		text := result.Content[0].Text
		if !strings.Contains(text, "write tier") || !strings.Contains(strings.ToLower(text), "settings") {
			t.Errorf("%s refusal does not tell the agent what to ask for: %s", tool, text)
		}
	}

	entries := log.all()
	if len(entries) != 4 {
		t.Fatalf("audited %d calls, want 4: %+v", len(entries), entries)
	}
	for _, entry := range entries {
		if entry.Outcome != outcomeDenied {
			t.Errorf("%s was audited as %q, want denied", entry.Tool, entry.Outcome)
		}
	}
}

// The tools are LISTED whether or not the tier is on (rule 5: rejected, not
// hidden). A tool that disappeared would tell the agent the capability does not
// exist, and it would go and build a worse substitute rather than asking the
// user for the one switch that works.
func TestWriteToolsAreListedEvenWhenTheTierIsOff(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		backend := newFixtureBackend()
		backend.writes.enabled = enabled
		server := newTestServer(t, backend)
		response := rpcCall(t, server, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`, 200)
		for _, name := range []string{"create_request", "update_request", "create_flow", "update_flow"} {
			if !strings.Contains(string(response.Result), `"`+name+`"`) {
				t.Errorf("with the write tier enabled=%v, tools/list omits %q", enabled, name)
			}
		}
	}
}

// --- describe_usage --------------------------------------------------------------

// The guide has to stand on its own: an agent that has read nothing else should
// learn the six rules, which tiers are live, what it may not author, and what a
// flow looks like. These assertions are deliberately about CONTENT rather than
// wording — they check that each subject is covered at all, because the failure
// this guards against is a section quietly going missing.
func TestDescribeUsageIsSelfContained(t *testing.T) {
	backend := newFixtureBackend() // write tier off
	server := newTestServer(t, backend)

	result := callTool(t, server, "describe_usage", nil)
	if result.IsError {
		t.Fatalf("describe_usage failed: %s", result.Content[0].Text)
	}
	var guide usageGuide
	decodePayload(t, result, &guide)

	if len(guide.SafetyRules) != 6 {
		t.Fatalf("the guide carries %d safety rules, want the six from the contract", len(guide.SafetyRules))
	}
	for index, rule := range guide.SafetyRules {
		if rule.Number != index+1 || rule.Title == "" || rule.Rule == "" || rule.ForYou == "" {
			t.Errorf("rule %d is incomplete: %+v", index+1, rule)
		}
	}

	if len(guide.Tiers) != 3 {
		t.Fatalf("tiers = %+v, want read, run and write", guide.Tiers)
	}
	byName := map[string]usageTier{}
	for _, tier := range guide.Tiers {
		byName[tier.Name] = tier
	}
	if !byName["read"].Enabled || !byName["run"].Enabled {
		t.Errorf("the read and run tiers are reported as off: %+v", guide.Tiers)
	}
	if byName["write"].Enabled {
		t.Errorf("the write tier is reported as on while the fixture has it off")
	}
	if !strings.Contains(strings.ToLower(byName["write"].Note), "settings") {
		t.Errorf("the write tier note does not say where the user turns it on: %q", byName["write"].Note)
	}
	for _, tool := range []string{"create_request", "update_request", "create_flow", "update_flow"} {
		if !contains(byName["write"].Tools, tool) {
			t.Errorf("the write tier does not list %q", tool)
		}
	}
	if !contains(byName["read"].Tools, "describe_usage") {
		t.Error("describe_usage does not list itself as read tier")
	}

	// The authoring rules are the ones an agent will otherwise discover by
	// being refused.
	authoring := guide.Authoring
	for name, rule := range map[string]usageAuthoringRule{
		"noScripts":    authoring.NoScripts,
		"noSecrets":    authoring.NoSecrets,
		"hostApproval": authoring.HostApproval,
	} {
		if rule.Rule == "" || rule.Why == "" || rule.OnRefusal == "" {
			t.Errorf("authoring rule %q is incomplete: %+v", name, rule)
		}
	}
	if !strings.Contains(authoring.NoScripts.Why, "guard") {
		t.Errorf("the no-scripts rule does not say why: %q", authoring.NoScripts.Why)
	}
	if !strings.Contains(authoring.NoSecrets.Rule, "secret") {
		t.Errorf("the no-secret-definitions rule does not state itself: %q", authoring.NoSecrets.Rule)
	}

	// The flow schema, with the canonical example from the contract.
	if len(guide.Flows.Fields) < 8 || len(guide.Flows.Semantics) < 4 {
		t.Errorf("the flow schema is thin: %d fields, %d semantics", len(guide.Flows.Fields), len(guide.Flows.Semantics))
	}
	if len(guide.Flows.Example.Steps) != 3 || guide.Flows.Example.Name != "Provision POS terminal" {
		t.Errorf("the worked example is not the canonical POS chain: %+v", guide.Flows.Example)
	}
	if len(guide.Flows.Example.Outputs) != 1 || guide.Flows.Example.Outputs[0].Value != "{{terminalId}}" {
		t.Errorf("the example's outputs are wrong: %+v", guide.Flows.Example.Outputs)
	}
	joinedSemantics := strings.ToLower(strings.Join(guide.Flows.Semantics, " "))
	for _, subject := range []string{"in order", "fail fast", "flow scope", "literal"} {
		if !strings.Contains(joinedSemantics, subject) {
			t.Errorf("the flow semantics never mention %q: %v", subject, guide.Flows.Semantics)
		}
	}
	if len(guide.Conventions) < 4 || len(guide.Errors.Retries) == 0 {
		t.Errorf("the conventions or the error advice are missing: %+v", guide)
	}
	if !strings.Contains(strings.ToLower(guide.Errors.Denied), "denied") {
		t.Errorf("the guide does not explain a denial: %q", guide.Errors.Denied)
	}

	// The example is a real FlowDefinition, so it must survive the same decode
	// create_flow puts an agent's flow through. If this ever fails, the guide is
	// telling agents to send something the tool would refuse.
	encoded, err := json.Marshal(guide.Flows.Example)
	if err != nil {
		t.Fatalf("marshal the example: %v", err)
	}
	var roundTripped map[string]any
	if err := json.Unmarshal(encoded, &roundTripped); err != nil {
		t.Fatalf("decode the example: %v", err)
	}
	if _, err := argFlow(toolArgs{"flow": roundTripped}); err != nil {
		t.Errorf("the worked example does not pass create_flow's own decoder: %v", err)
	}
}

// The tier state is READ, not assumed: an agent told the write tier is on and
// then refused has been misinformed by the one tool whose job is to tell it the
// truth.
func TestDescribeUsageReportsTheLiveWriteTierState(t *testing.T) {
	backend := newWritableFixture()
	server := newTestServer(t, backend)

	result := callTool(t, server, "describe_usage", nil)
	var guide usageGuide
	decodePayload(t, result, &guide)
	for _, tier := range guide.Tiers {
		if tier.Name == "write" && !tier.Enabled {
			t.Fatal("the write tier is unlocked but the guide says it is off")
		}
	}
	if backend.writes.tierCalls == 0 {
		t.Error("describe_usage never asked the Backend for the tier state")
	}
}

// A Backend that cannot answer must not break the guide: it is most useful
// exactly when something else is wrong, and reporting the write tier as off is
// the reading that leads the agent to ask the user rather than to try.
func TestDescribeUsageSurvivesABackendThatCannotAnswer(t *testing.T) {
	backend := newFixtureBackend()
	backend.writes.tierErr = errors.New("state is not ready")
	server := newTestServer(t, backend)

	result := callTool(t, server, "describe_usage", nil)
	if result.IsError {
		t.Fatalf("describe_usage failed when the tier state was unreadable: %s", result.Content[0].Text)
	}
	var guide usageGuide
	decodePayload(t, result, &guide)
	for _, tier := range guide.Tiers {
		if tier.Name == "write" && tier.Enabled {
			t.Error("an unreadable tier state was reported as unlocked")
		}
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
