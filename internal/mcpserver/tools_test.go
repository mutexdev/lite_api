package mcpserver

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fixtureBackend is a Backend whose answers are fixed and inspectable. It
// records the arguments it was called with so the tests can prove that a
// tool's arguments reach the Backend intact rather than only that a call
// returned something.
//
// The fixture is already redacted, as the contract in backend.go requires of
// every implementation: masking happens in internal/core's adapter, and this
// package's job is to not undo it.
type fixtureBackend struct {
	collections  []CollectionSummary
	requests     map[string][]RequestSummary
	details      map[string]RequestDetail
	environments []EnvironmentSummary
	history      []HistoryRun

	// failWith, when set, is returned by every method — the "the app could not
	// answer" case every tool has to survive.
	failWith error
	// panicWith, when set, makes every method panic, standing in for a bug in
	// a deeper layer.
	panicWith string

	// Recorded arguments.
	lastCollectionID string
	lastRequestID    string
	lastQuery        string
	lastLimit        int
}

func newFixtureBackend() *fixtureBackend {
	return &fixtureBackend{
		collections: []CollectionSummary{
			{ID: "col_pos", Name: "POS API", RequestCount: 2},
			{ID: "col_id", Name: "Identity", RequestCount: 1},
		},
		requests: map[string][]RequestSummary{
			"col_pos": {
				{ID: "req_list", CollectionID: "col_pos", Name: "List terminals", Type: "http", Method: "GET", URL: "{{baseUrl}}/terminals", FolderPath: "Terminals"},
				{ID: "req_create", CollectionID: "col_pos", Name: "Create terminal", Type: "http", Method: "POST", URL: "{{baseUrl}}/terminals"},
			},
		},
		details: map[string]RequestDetail{
			"col_pos/req_create": {
				RequestSummary: RequestSummary{ID: "req_create", CollectionID: "col_pos", Name: "Create terminal", Type: "http", Method: "POST", URL: "{{baseUrl}}/terminals"},
				Headers: []KeyValue{
					{Name: "Content-Type", Value: "application/json", Enabled: true},
					{Name: "Authorization", Value: "Bearer {{apiToken}}", Enabled: true},
					{Name: "X-Api-Key", Value: MaskedValue, Enabled: true},
				},
				Params:   []KeyValue{{Name: "region", Value: "{{region}}", Enabled: true}},
				BodyType: "json",
				Body:     `{"storeId":"{{storeId}}"}`,
				AuthType: "bearer",
				Auth:     []KeyValue{{Name: "token", Value: "{{apiToken}}", Enabled: true}},
				Settings: RequestSettings{VerifyTLS: true, FollowRedirects: true, MaxRedirects: 5},
			},
		},
		environments: []EnvironmentSummary{
			{
				ID: "env_stage", Name: "Staging", Scope: "collection", CollectionID: "col_pos", Active: true,
				Variables: []EnvironmentVariable{
					{Name: "baseUrl", Value: "https://pos.stage.example.test", Enabled: true},
					{Name: "apiToken", Value: "", Secret: true, Enabled: true},
				},
			},
		},
		history: []HistoryRun{
			{ID: "run_2", Method: "POST", URL: "https://pos.stage.example.test/terminals", Status: 201, DurationMs: 84, ExecutedAt: "2026-08-29T10:00:00Z",
				Headers: []KeyValue{{Name: "Content-Type", Value: "application/json", Enabled: true}},
				Body:    `{"terminal":{"id":"trm_9"}}`},
			{ID: "run_1", Method: "POST", URL: "https://pos.stage.example.test/terminals", Status: 500, DurationMs: 12, ExecutedAt: "2026-08-29T09:00:00Z", Body: "boom", Truncated: true},
		},
	}
}

// gate is the shared failure/panic injection every method runs first.
func (backend *fixtureBackend) gate() error {
	if backend.panicWith != "" {
		panic(backend.panicWith)
	}
	return backend.failWith
}

func (backend *fixtureBackend) ListCollections() ([]CollectionSummary, error) {
	if err := backend.gate(); err != nil {
		return nil, err
	}
	return backend.collections, nil
}

func (backend *fixtureBackend) ListRequests(collectionID string) ([]RequestSummary, error) {
	if err := backend.gate(); err != nil {
		return nil, err
	}
	backend.lastCollectionID = collectionID
	requests, known := backend.requests[collectionID]
	if !known {
		return nil, errors.New("collection " + collectionID + " is not open")
	}
	return requests, nil
}

func (backend *fixtureBackend) SearchRequests(query string, limit int) ([]RequestSummary, error) {
	if err := backend.gate(); err != nil {
		return nil, err
	}
	backend.lastQuery = query
	backend.lastLimit = limit
	var matches []RequestSummary
	for _, request := range backend.requests["col_pos"] {
		if strings.Contains(strings.ToLower(request.Name), strings.ToLower(query)) {
			matches = append(matches, request)
		}
	}
	return matches, nil
}

func (backend *fixtureBackend) GetRequest(collectionID, requestID string) (RequestDetail, error) {
	if err := backend.gate(); err != nil {
		return RequestDetail{}, err
	}
	backend.lastCollectionID = collectionID
	backend.lastRequestID = requestID
	detail, known := backend.details[collectionID+"/"+requestID]
	if !known {
		return RequestDetail{}, errors.New("request " + requestID + " is not in collection " + collectionID)
	}
	return detail, nil
}

func (backend *fixtureBackend) ListEnvironments() ([]EnvironmentSummary, error) {
	if err := backend.gate(); err != nil {
		return nil, err
	}
	return backend.environments, nil
}

func (backend *fixtureBackend) GetHistory(collectionID, requestID string, limit int) ([]HistoryRun, error) {
	if err := backend.gate(); err != nil {
		return nil, err
	}
	backend.lastCollectionID = collectionID
	backend.lastRequestID = requestID
	backend.lastLimit = limit
	if limit > 0 && limit < len(backend.history) {
		return backend.history[:limit], nil
	}
	return backend.history, nil
}

// callTool drives one tools/call through the full HTTP path and returns the
// decoded result. It fails the test on a JSON-RPC error, since that is a
// protocol fault and never how a tool reports trouble.
func callTool(t *testing.T, server *httptest.Server, name string, arguments map[string]any) callToolResult {
	t.Helper()
	response := callToolRaw(t, server, name, arguments)
	if response.Error != nil {
		t.Fatalf("tools/call %s returned a JSON-RPC error: %+v", name, response.Error)
	}
	var result callToolResult
	if err := json.Unmarshal(response.Result, &result); err != nil {
		t.Fatalf("decode tool result: %v", err)
	}
	if len(result.Content) != 1 || result.Content[0].Type != "text" {
		t.Fatalf("content = %+v, want exactly one text block", result.Content)
	}
	return result
}

// callToolRaw returns the JSON-RPC envelope without judging it.
func callToolRaw(t *testing.T, server *httptest.Server, name string, arguments map[string]any) testResponse {
	t.Helper()
	params, err := json.Marshal(map[string]any{"name": name, "arguments": arguments})
	if err != nil {
		t.Fatalf("encode params: %v", err)
	}
	body := `{"jsonrpc":"2.0","id":11,"method":"tools/call","params":` + string(params) + `}`
	return rpcCall(t, server, body, http.StatusOK)
}

// decodePayload reads the tool's JSON payload out of its text content block.
func decodePayload(t *testing.T, result callToolResult, target any) {
	t.Helper()
	if result.IsError {
		t.Fatalf("tool failed: %s", result.Content[0].Text)
	}
	if err := json.Unmarshal([]byte(result.Content[0].Text), target); err != nil {
		t.Fatalf("decode payload %q: %v", result.Content[0].Text, err)
	}
}

func TestListCollectionsReturnsEveryOpenCollection(t *testing.T) {
	server := newTestServer(t, newFixtureBackend())
	var collections []CollectionSummary
	decodePayload(t, callTool(t, server, "list_collections", nil), &collections)
	if len(collections) != 2 {
		t.Fatalf("got %d collections, want 2: %+v", len(collections), collections)
	}
	if collections[0].ID != "col_pos" || collections[0].Name != "POS API" || collections[0].RequestCount != 2 {
		t.Fatalf("first collection = %+v", collections[0])
	}
}

func TestListRequestsReturnsRowsWithUnresolvedTemplates(t *testing.T) {
	backend := newFixtureBackend()
	server := newTestServer(t, backend)
	var requests []RequestSummary
	decodePayload(t, callTool(t, server, "list_requests", map[string]any{"collectionId": "col_pos"}), &requests)
	if backend.lastCollectionID != "col_pos" {
		t.Fatalf("backend saw collectionId %q", backend.lastCollectionID)
	}
	if len(requests) != 2 {
		t.Fatalf("got %d requests, want 2", len(requests))
	}
	if requests[0].URL != "{{baseUrl}}/terminals" {
		t.Fatalf("URL was rewritten: %q", requests[0].URL)
	}
	if requests[0].Method != "GET" || requests[0].FolderPath != "Terminals" || requests[0].CollectionID != "col_pos" {
		t.Fatalf("row lost fields: %+v", requests[0])
	}
}

func TestSearchRequestsForwardsQueryAndLimit(t *testing.T) {
	backend := newFixtureBackend()
	server := newTestServer(t, backend)
	var requests []RequestSummary
	decodePayload(t, callTool(t, server, "search_requests", map[string]any{"query": "create", "limit": 5}), &requests)
	if backend.lastQuery != "create" || backend.lastLimit != 5 {
		t.Fatalf("backend saw query %q limit %d", backend.lastQuery, backend.lastLimit)
	}
	if len(requests) != 1 || requests[0].ID != "req_create" {
		t.Fatalf("matches = %+v", requests)
	}
}

func TestSearchRequestsWithoutLimitLetsTheBackendDefault(t *testing.T) {
	backend := newFixtureBackend()
	server := newTestServer(t, backend)
	callTool(t, server, "search_requests", map[string]any{"query": "terminal"})
	if backend.lastLimit != 0 {
		t.Fatalf("limit = %d, want 0 so the backend applies its own default", backend.lastLimit)
	}
}

func TestGetRequestReturnsTheRedactedDefinition(t *testing.T) {
	backend := newFixtureBackend()
	server := newTestServer(t, backend)
	var detail RequestDetail
	decodePayload(t, callTool(t, server, "get_request", map[string]any{"collectionId": "col_pos", "requestId": "req_create"}), &detail)
	if backend.lastCollectionID != "col_pos" || backend.lastRequestID != "req_create" {
		t.Fatalf("backend saw %q/%q", backend.lastCollectionID, backend.lastRequestID)
	}
	if detail.Method != "POST" || detail.BodyType != "json" || detail.AuthType != "bearer" {
		t.Fatalf("detail lost fields: %+v", detail)
	}
	if detail.Settings.MaxRedirects != 5 || !detail.Settings.VerifyTLS {
		t.Fatalf("settings lost: %+v", detail.Settings)
	}
	if detail.Headers[1].Value != "Bearer {{apiToken}}" {
		t.Fatalf("templated header was rewritten: %q", detail.Headers[1].Value)
	}
	if detail.Headers[2].Value != MaskedValue {
		t.Fatalf("masked header lost its mask: %q", detail.Headers[2].Value)
	}
}

func TestListEnvironmentsKeepsSecretValuesEmpty(t *testing.T) {
	server := newTestServer(t, newFixtureBackend())
	var environments []EnvironmentSummary
	decodePayload(t, callTool(t, server, "list_environments", nil), &environments)
	if len(environments) != 1 || environments[0].ID != "env_stage" || !environments[0].Active {
		t.Fatalf("environments = %+v", environments)
	}
	secret := environments[0].Variables[1]
	if !secret.Secret || secret.Value != "" {
		t.Fatalf("secret variable carried a value: %+v", secret)
	}
	if environments[0].Variables[0].Value != "https://pos.stage.example.test" {
		t.Fatalf("non-secret value was dropped: %+v", environments[0].Variables[0])
	}
}

func TestGetHistoryReturnsRunsNewestFirst(t *testing.T) {
	backend := newFixtureBackend()
	server := newTestServer(t, backend)
	var runs []HistoryRun
	decodePayload(t, callTool(t, server, "get_history", map[string]any{"collectionId": "col_pos", "requestId": "req_create", "limit": 1}), &runs)
	if backend.lastLimit != 1 {
		t.Fatalf("limit = %d, want 1", backend.lastLimit)
	}
	if len(runs) != 1 || runs[0].ID != "run_2" || runs[0].Status != 201 {
		t.Fatalf("runs = %+v", runs)
	}
	if runs[0].Body != `{"terminal":{"id":"trm_9"}}` {
		t.Fatalf("body was altered: %q", runs[0].Body)
	}
}

func TestMissingRequiredArgumentIsAToolErrorNamingTheField(t *testing.T) {
	server := newTestServer(t, newFixtureBackend())
	cases := []struct {
		tool      string
		arguments map[string]any
		field     string
	}{
		{"list_requests", map[string]any{}, "collectionId"},
		{"search_requests", map[string]any{"limit": 3}, "query"},
		{"get_request", map[string]any{"collectionId": "col_pos"}, "requestId"},
		{"get_history", map[string]any{"requestId": "req_create"}, "collectionId"},
		// An argument that is present but blank is the same mistake wearing a
		// different hat, and gets the same answer.
		{"list_requests", map[string]any{"collectionId": "   "}, "collectionId"},
	}
	for _, testCase := range cases {
		result := callTool(t, server, testCase.tool, testCase.arguments)
		if !result.IsError {
			t.Fatalf("%s with %v succeeded, want isError", testCase.tool, testCase.arguments)
		}
		if !strings.Contains(result.Content[0].Text, testCase.field) {
			t.Fatalf("%s error does not name %q: %s", testCase.tool, testCase.field, result.Content[0].Text)
		}
	}
}

func TestWrongArgumentTypeIsAToolErrorNamingTheField(t *testing.T) {
	server := newTestServer(t, newFixtureBackend())
	result := callTool(t, server, "search_requests", map[string]any{"query": "pos", "limit": "twenty"})
	if !result.IsError || !strings.Contains(result.Content[0].Text, "limit") {
		t.Fatalf("result = %+v", result)
	}
	result = callTool(t, server, "list_requests", map[string]any{"collectionId": 7})
	if !result.IsError || !strings.Contains(result.Content[0].Text, "collectionId") {
		t.Fatalf("result = %+v", result)
	}
	// A whole-number float is how JSON delivers an integer, and must pass.
	if got := callTool(t, server, "search_requests", map[string]any{"query": "pos", "limit": 20}); got.IsError {
		t.Fatalf("integral limit rejected: %s", got.Content[0].Text)
	}
	if got := callTool(t, server, "search_requests", map[string]any{"query": "pos", "limit": 2.5}); !got.IsError {
		t.Fatal("fractional limit accepted")
	}
}

func TestBackendFailureIsAToolErrorNotAProtocolError(t *testing.T) {
	backend := newFixtureBackend()
	backend.failWith = errors.New("collection store is locked by another write")
	server := newTestServer(t, backend)

	for _, tool := range []string{"list_collections", "list_environments"} {
		result := callTool(t, server, tool, nil)
		if !result.IsError {
			t.Fatalf("%s: want isError when the backend fails", tool)
		}
		if !strings.Contains(result.Content[0].Text, "locked by another write") {
			t.Fatalf("%s: error text lost the cause: %s", tool, result.Content[0].Text)
		}
	}

	// An unknown id is the backend's own error and travels the same way.
	result := callTool(t, newTestServer(t, newFixtureBackend()), "get_request", map[string]any{"collectionId": "col_pos", "requestId": "req_nope"})
	if !result.IsError || !strings.Contains(result.Content[0].Text, "req_nope") {
		t.Fatalf("result = %+v", result)
	}
}

func TestPanicInAToolBecomesAToolError(t *testing.T) {
	backend := newFixtureBackend()
	backend.panicWith = "nil map write in the adapter"
	server := newTestServer(t, backend)
	result := callTool(t, server, "list_collections", nil)
	if !result.IsError {
		t.Fatal("panic did not become an isError result")
	}
	if !strings.Contains(result.Content[0].Text, "nil map write") || !strings.Contains(result.Content[0].Text, "list_collections") {
		t.Fatalf("panic text = %s", result.Content[0].Text)
	}
	// The server survived it: the next call still works.
	backend.panicWith = ""
	if got := callTool(t, server, "list_collections", nil); got.IsError {
		t.Fatalf("server did not recover: %s", got.Content[0].Text)
	}
}

func TestEmptyResultsMarshalAsAnEmptyArray(t *testing.T) {
	backend := newFixtureBackend()
	backend.collections = nil
	backend.environments = nil
	backend.history = nil
	server := newTestServer(t, backend)

	for _, testCase := range []struct {
		tool      string
		arguments map[string]any
	}{
		{"list_collections", nil},
		{"list_environments", nil},
		{"get_history", map[string]any{"collectionId": "col_pos", "requestId": "req_create"}},
		{"search_requests", map[string]any{"query": "nothing matches this"}},
	} {
		result := callTool(t, server, testCase.tool, testCase.arguments)
		if result.IsError {
			t.Fatalf("%s failed: %s", testCase.tool, result.Content[0].Text)
		}
		if result.Content[0].Text != "[]" {
			t.Fatalf("%s payload = %q, want []", testCase.tool, result.Content[0].Text)
		}
	}
}

// An unknown tool is a JSON-RPC -32602 rather than an isError result: naming a
// tool that does not exist is the client misusing the protocol, not a tool
// reporting a failure. See handleToolsCall.
func TestUnknownToolIsInvalidParams(t *testing.T) {
	server := newTestServer(t, newFixtureBackend())
	response := callToolRaw(t, server, "delete_everything", map[string]any{})
	if response.Error == nil || response.Error.Code != codeInvalidParams {
		t.Fatalf("error = %+v, want code %d", response.Error, codeInvalidParams)
	}
	if !strings.Contains(response.Error.Message, "delete_everything") || !strings.Contains(response.Error.Message, "tools/list") {
		t.Fatalf("message should name the tool and the way out: %q", response.Error.Message)
	}
}

func TestToolsCallWithoutANameIsInvalidParams(t *testing.T) {
	server := newTestServer(t, newFixtureBackend())
	response := rpcCall(t, server, `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{}}`, http.StatusOK)
	if response.Error == nil || response.Error.Code != codeInvalidParams {
		t.Fatalf("error = %+v, want code %d", response.Error, codeInvalidParams)
	}
}

func TestUnknownArgumentsAreTolerated(t *testing.T) {
	// Clients attach their own metadata; failing the call over a key we do not
	// read would break a working request for no safety gain.
	server := newTestServer(t, newFixtureBackend())
	result := callTool(t, server, "list_requests", map[string]any{"collectionId": "col_pos", "_meta": map[string]any{"trace": "abc"}})
	if result.IsError {
		t.Fatalf("extra argument rejected: %s", result.Content[0].Text)
	}
}
