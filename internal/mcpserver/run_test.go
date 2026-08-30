package mcpserver

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// The run tier: the one tool that reaches outside LiteAPI. These tests hold the
// line on the three things that make it safe to hand to an agent — the
// arguments reach the Backend intact, a refusal reaches the agent verbatim and
// the audit as a refusal, and the run gets a deadline of its own rather than
// the HTTP client's.

func TestRunRequestReturnsTheRunResult(t *testing.T) {
	backend := newFixtureBackend()
	server := newTestServer(t, backend)

	var result RunResult
	decodePayload(t, callTool(t, server, "run_request", map[string]any{
		"collectionId": "col_pos",
		"requestId":    "req_create",
	}), &result)

	if backend.runCalls != 1 {
		t.Fatalf("the backend ran %d times, want 1", backend.runCalls)
	}
	if result.Status != 201 || result.StatusText != "201 Created" || result.DurationMs != 91 {
		t.Fatalf("result lost fields: %+v", result)
	}
	if result.Body != `{"terminal":{"id":"trm_10"}}` {
		t.Errorf("body was altered: %q", result.Body)
	}
	if len(result.Headers) != 1 || result.Headers[0].Name != "Content-Type" {
		t.Errorf("headers = %+v", result.Headers)
	}
	// The masking is the adapter's job; what this package must not do is undo
	// it on the way out.
	if !strings.Contains(result.URL, MaskedValue) {
		t.Errorf("URL lost its mask: %q", result.URL)
	}
	if len(result.TestResults) != 2 || result.TestResults[0].Name != "status is 201" || result.TestResults[1].Passed {
		t.Errorf("scripted test results did not survive: %+v", result.TestResults)
	}
}

func TestRunRequestForwardsIdsEnvironmentAndVariables(t *testing.T) {
	backend := newFixtureBackend()
	server := newTestServer(t, backend)

	if result := callTool(t, server, "run_request", map[string]any{
		"collectionId":  "  col_pos  ",
		"requestId":     "req_create",
		"environmentId": "env_stage",
		"variables":     map[string]any{"storeId": "str_42", "region": "eu-west"},
	}); result.IsError {
		t.Fatalf("run_request failed: %s", result.Content[0].Text)
	}

	params := backend.lastRunParams
	// Ids are trimmed: an id padded by a client is the same id.
	if params.CollectionID != "col_pos" || params.RequestID != "req_create" || params.EnvironmentID != "env_stage" {
		t.Fatalf("backend saw %+v", params)
	}
	if len(params.Variables) != 2 || params.Variables["storeId"] != "str_42" || params.Variables["region"] != "eu-west" {
		t.Fatalf("variables = %+v", params.Variables)
	}
}

// An omitted environmentId means "whichever environment is active", and an
// omitted variables object means "no overrides" — both have to reach the
// Backend as the empty value rather than as a validation failure.
func TestRunRequestWithoutOptionalArgumentsLetsTheBackendDecide(t *testing.T) {
	backend := newFixtureBackend()
	server := newTestServer(t, backend)

	for _, testCase := range []struct {
		name      string
		arguments map[string]any
	}{
		{"neither optional argument", map[string]any{"collectionId": "col_pos", "requestId": "req_create"}},
		{"an empty variables object", map[string]any{"collectionId": "col_pos", "requestId": "req_create", "variables": map[string]any{}}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			backend.runCalls = 0
			if result := callTool(t, server, "run_request", testCase.arguments); result.IsError {
				t.Fatalf("run_request failed: %s", result.Content[0].Text)
			}
			if backend.runCalls != 1 {
				t.Fatalf("the backend ran %d times; the call never got past validation", backend.runCalls)
			}
			if backend.lastRunParams.EnvironmentID != "" {
				t.Errorf("environmentId = %q, want empty", backend.lastRunParams.EnvironmentID)
			}
			if backend.lastRunParams.Variables != nil {
				t.Errorf("variables = %+v, want nil", backend.lastRunParams.Variables)
			}
		})
	}
}

// The run must not inherit the HTTP request's context. An agent that
// disconnects mid-run would otherwise cancel a POST that has already reached a
// real host, and the app would never record what came back.
func TestRunRequestGivesTheBackendItsOwnDeadline(t *testing.T) {
	backend := newFixtureBackend()
	server := newTestServer(t, backend)

	if result := callTool(t, server, "run_request", map[string]any{"collectionId": "col_pos", "requestId": "req_create"}); result.IsError {
		t.Fatalf("run_request failed: %s", result.Content[0].Text)
	}
	if !backend.lastRunHadDeadline {
		t.Fatal("the context carried no deadline; a wedged run would pin the handler goroutine forever")
	}
	// Bounded by RunTimeout, and not by something far shorter that a client
	// could have imposed. The lower bound is loose on purpose: the only claim
	// worth making is that the deadline is the run's own.
	if backend.lastRunTimeout > RunTimeout || backend.lastRunTimeout < RunTimeout/2 {
		t.Fatalf("remaining time was %s, want roughly RunTimeout (%s)", backend.lastRunTimeout, RunTimeout)
	}
}

func TestRunRequestRejectsNonStringVariableValuesNamingTheKey(t *testing.T) {
	backend := newFixtureBackend()
	server := newTestServer(t, backend)

	for _, testCase := range []struct {
		name      string
		variables map[string]any
		key       string
		want      string
	}{
		{"a number", map[string]any{"storeId": 42}, "storeId", "a number"},
		{"a boolean", map[string]any{"dryRun": true}, "dryRun", "a boolean"},
		{"a nested object", map[string]any{"filter": map[string]any{"a": "b"}}, "filter", "an object"},
		{"an array", map[string]any{"ids": []any{"a", "b"}}, "ids", "an array"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			backend.runCalls = 0
			result := callTool(t, server, "run_request", map[string]any{
				"collectionId": "col_pos",
				"requestId":    "req_create",
				"variables":    testCase.variables,
			})
			if !result.IsError {
				t.Fatalf("variables %+v were accepted", testCase.variables)
			}
			text := result.Content[0].Text
			// The agent is about to compose the retry: it needs the offending
			// key by name, what arrived, and what to send instead.
			if !strings.Contains(text, testCase.key) {
				t.Errorf("error does not name the offending key %q: %s", testCase.key, text)
			}
			if !strings.Contains(text, "variables") || !strings.Contains(text, testCase.want) {
				t.Errorf("error = %s", text)
			}
			if backend.runCalls != 0 {
				t.Errorf("the backend ran anyway (%d times)", backend.runCalls)
			}
		})
	}
}

// A well-formed object of strings passes, mixed with a bad key it does not:
// validation must not stop at the first key it happens to iterate.
func TestRunRequestVariablesValidationSeesEveryKey(t *testing.T) {
	server := newTestServer(t, newFixtureBackend())
	result := callTool(t, server, "run_request", map[string]any{
		"collectionId": "col_pos",
		"requestId":    "req_create",
		"variables":    map[string]any{"a": "ok", "b": "ok", "c": "ok", "zzz": 1},
	})
	if !result.IsError || !strings.Contains(result.Content[0].Text, "zzz") {
		t.Fatalf("result = %+v", result)
	}
}

func TestRunRequestVariablesMustBeAnObject(t *testing.T) {
	server := newTestServer(t, newFixtureBackend())
	result := callTool(t, server, "run_request", map[string]any{
		"collectionId": "col_pos",
		"requestId":    "req_create",
		"variables":    "storeId=str_42",
	})
	if !result.IsError {
		t.Fatal("a string variables argument was accepted")
	}
	if !strings.Contains(result.Content[0].Text, "variables") || !strings.Contains(result.Content[0].Text, "object") {
		t.Fatalf("error = %s", result.Content[0].Text)
	}
}

func TestRunRequestRequiresBothIds(t *testing.T) {
	server := newTestServer(t, newFixtureBackend())
	for _, testCase := range []struct {
		arguments map[string]any
		field     string
	}{
		{map[string]any{"requestId": "req_create"}, "collectionId"},
		{map[string]any{"collectionId": "col_pos"}, "requestId"},
		{map[string]any{"collectionId": "col_pos", "requestId": "  "}, "requestId"},
	} {
		result := callTool(t, server, "run_request", testCase.arguments)
		if !result.IsError || !strings.Contains(result.Content[0].Text, testCase.field) {
			t.Fatalf("%v: result = %+v", testCase.arguments, result)
		}
	}
}

// A denial is not an error the agent should retry around. Its message is the
// Backend's explanation of which guard fired and what the user would have to
// approve, so it reaches the agent unchanged — and the audit records it as a
// refusal rather than as one more failed call.
func TestRunRequestDenialPassesTheMessageThroughAndAuditsAsDenied(t *testing.T) {
	const explanation = "denied: this request would send {{apiToken}} to api.other.example, a host this collection has never sent it to; ask the user to approve the host in LiteAPI"
	backend := newFixtureBackend()
	backend.runErr = fmt.Errorf("%w: %s", ErrDenied, explanation)
	server, log := newAuditedServer(t, backend)

	result := callTool(t, server, "run_request", map[string]any{"collectionId": "col_pos", "requestId": "req_create"})
	if !result.IsError {
		t.Fatal("a denied run came back as a success")
	}
	if !strings.Contains(result.Content[0].Text, explanation) {
		t.Fatalf("the denial's explanation did not reach the agent intact: %s", result.Content[0].Text)
	}

	entry := log.only(t)
	if entry.Outcome != outcomeDenied {
		t.Fatalf("outcome = %q, want %q", entry.Outcome, outcomeDenied)
	}
	if entry.Tool != "run_request" {
		t.Errorf("tool = %q", entry.Tool)
	}
}

// A denial wrapped several layers deep is still a denial: the contract says
// errors.Is must hold, not that ErrDenied is the immediate cause.
func TestRunRequestDenialIsRecognisedThroughWrapping(t *testing.T) {
	backend := newFixtureBackend()
	backend.runErr = fmt.Errorf("running %q: %w", "Create terminal", fmt.Errorf("new-host guard: %w", ErrDenied))
	server, log := newAuditedServer(t, backend)

	if result := callTool(t, server, "run_request", map[string]any{"collectionId": "col_pos", "requestId": "req_create"}); !result.IsError {
		t.Fatal("want isError")
	}
	if outcome := log.only(t).Outcome; outcome != outcomeDenied {
		t.Fatalf("outcome = %q, want %q", outcome, outcomeDenied)
	}
}

// A plain failure is not a denial. Auditing a network timeout as "denied" would
// make the panel's refusal count meaningless.
func TestRunRequestOrdinaryFailureAuditsAsError(t *testing.T) {
	backend := newFixtureBackend()
	backend.runErr = errors.New("dial tcp: connection refused")
	server, log := newAuditedServer(t, backend)

	result := callTool(t, server, "run_request", map[string]any{"collectionId": "col_pos", "requestId": "req_create"})
	if !result.IsError || !strings.Contains(result.Content[0].Text, "connection refused") {
		t.Fatalf("result = %+v", result)
	}
	if outcome := log.only(t).Outcome; outcome != outcomeError {
		t.Fatalf("outcome = %q, want %q", outcome, outcomeError)
	}
}

// The schema is what an agent reads before composing a call, so it has to
// declare the same rules validate enforces.
func TestRunRequestSchemaDeclaresWhatValidationEnforces(t *testing.T) {
	entry, known := lookupTool("run_request")
	if !known {
		t.Fatal("run_request is not registered")
	}
	if len(entry.InputSchema.Required) != 2 || entry.InputSchema.Required[0] != "collectionId" || entry.InputSchema.Required[1] != "requestId" {
		t.Errorf("required = %v", entry.InputSchema.Required)
	}
	variables, declared := entry.InputSchema.Properties["variables"]
	if !declared {
		t.Fatal("run_request does not declare a variables property")
	}
	if variables.Type != "object" || variables.AdditionalProperties == nil || variables.AdditionalProperties.Type != "string" {
		t.Errorf("variables schema = %+v, want an object of strings", variables)
	}
	if _, declared := entry.InputSchema.Properties["environmentId"]; !declared {
		t.Error("run_request does not declare an environmentId property")
	}
	// The description is the agent's only warning that a run can be refused and
	// that retrying is not the way out.
	description := strings.ToLower(entry.Description)
	for _, phrase := range []string{"denied", "approval", "secret", "run_flow"} {
		if !strings.Contains(description, phrase) {
			t.Errorf("description never mentions %q", phrase)
		}
	}
}

// Discovery order is part of the contract with the agent: run_request is the
// tool you reach for after you have read the request, so it comes after the
// whole read tier — of which get_history is the last member. run_flow follows it
// as the other half of the run tier (Phase 3).
func TestRunRequestIsRegisteredAfterGetHistory(t *testing.T) {
	names := make([]string, len(toolRegistry))
	for index, entry := range toolRegistry {
		names[index] = entry.Name
	}
	if len(names) < 3 {
		t.Fatalf("registry order = %v", names)
	}
	tail := names[len(names)-3:]
	if tail[0] != "get_history" || tail[1] != "run_request" || tail[2] != "run_flow" {
		t.Fatalf("registry order = %v, want the run tier last: get_history, run_request, run_flow", names)
	}
}
