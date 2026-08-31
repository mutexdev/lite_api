package mcpserver

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// These tests pin the protocol against the MCP streamable-HTTP spec rather
// than against this implementation: an initialize handshake, a notification, a
// ping, a tool listing, and the four ways a request can be refused. If the
// dispatcher is rewritten they should still pass unchanged.

// testToken stands in for the per-install pairing token. It is deliberately
// distinctive so the leak gate below can grep for it in a response body and
// know that a hit is the real thing.
const testToken = "SENTINEL-BEARER-TOKEN-4d91c7"

// testResponse decodes a JSON-RPC reply with the result left raw, so each test
// asserts on the shape it actually cares about.
type testResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *rpcError       `json:"error"`
}

// newTestServer builds the whole stack over httptest. Options are forwarded to
// New, so a test that cares about auditing installs a recorder the same way
// internal/core does.
func newTestServer(t *testing.T, backend Backend, options ...Option) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(New(backend, testToken, 0, options...).handler())
	t.Cleanup(server.Close)
	return server
}

// auditLog collects entries for assertion. The recorder runs on the HTTP
// handler's goroutine while the test reads from its own, so the mutex is what
// makes these tests meaningful under -race rather than merely green.
type auditLog struct {
	mu      sync.Mutex
	entries []AuditEntry
}

func (log *auditLog) record(entry AuditEntry) {
	log.mu.Lock()
	defer log.mu.Unlock()
	log.entries = append(log.entries, entry)
}

func (log *auditLog) all() []AuditEntry {
	log.mu.Lock()
	defer log.mu.Unlock()
	return append([]AuditEntry(nil), log.entries...)
}

// only returns the single entry the test expects, failing loudly otherwise —
// "exactly one call was recorded" is half of what most of these assert.
func (log *auditLog) only(t *testing.T) AuditEntry {
	t.Helper()
	entries := log.all()
	if len(entries) != 1 {
		t.Fatalf("recorded %d entries, want exactly 1: %+v", len(entries), entries)
	}
	return entries[0]
}

// newAuditedServer pairs a server with the log recording it.
func newAuditedServer(t *testing.T, backend Backend) (*httptest.Server, *auditLog) {
	t.Helper()
	log := &auditLog{}
	return newTestServer(t, backend, WithAuditRecorder(log.record)), log
}

// doRequest drives one raw HTTP exchange and returns the status and body.
//
// Every response body passes the leak gate here rather than in one dedicated
// test: that way a future test that adds a new code path gets the check for
// free, and no reply can start echoing the pairing token unnoticed.
func doRequest(t *testing.T, server *httptest.Server, method, path, body string, mutate ...func(*http.Request)) (int, string) {
	t.Helper()
	request, err := http.NewRequest(method, server.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+testToken)
	for _, apply := range mutate {
		apply(request)
	}
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer func() { _ = response.Body.Close() }()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	text := string(raw)
	if strings.Contains(text, testToken) {
		t.Fatalf("response to %s %s echoed the bearer token: %s", method, path, text)
	}
	return response.StatusCode, text
}

// rpcPost sends one JSON-RPC message to the endpoint.
func rpcPost(t *testing.T, server *httptest.Server, body string, mutate ...func(*http.Request)) (int, string) {
	t.Helper()
	return doRequest(t, server, http.MethodPost, "/mcp", body, mutate...)
}

// rpcCall sends a message and decodes the reply, failing when the status is
// not the expected one.
func rpcCall(t *testing.T, server *httptest.Server, body string, wantStatus int) testResponse {
	t.Helper()
	status, text := rpcPost(t, server, body)
	if status != wantStatus {
		t.Fatalf("status = %d, want %d (body %s)", status, wantStatus, text)
	}
	var decoded testResponse
	if err := json.Unmarshal([]byte(text), &decoded); err != nil {
		t.Fatalf("decode response %q: %v", text, err)
	}
	if decoded.JSONRPC != "2.0" {
		t.Fatalf("jsonrpc = %q, want 2.0", decoded.JSONRPC)
	}
	return decoded
}

func withHeader(name, value string) func(*http.Request) {
	return func(request *http.Request) { request.Header.Set(name, value) }
}

func TestInitializeHandshakeNegotiatesProtocolVersion(t *testing.T) {
	server := newTestServer(t, newFixtureBackend())

	cases := []struct {
		name     string
		params   string
		wantEcho string
	}{
		{"current revision is echoed", `{"protocolVersion":"2025-06-18"}`, "2025-06-18"},
		{"older supported revision is echoed", `{"protocolVersion":"2025-03-26"}`, "2025-03-26"},
		{"unknown revision falls back to ours", `{"protocolVersion":"2019-01-01"}`, ProtocolVersion},
		{"absent params fall back to ours", `{}`, ProtocolVersion},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":` + testCase.params + `}`
			response := rpcCall(t, server, body, http.StatusOK)
			if response.Error != nil {
				t.Fatalf("initialize failed: %+v", response.Error)
			}
			var result struct {
				ProtocolVersion string         `json:"protocolVersion"`
				Capabilities    map[string]any `json:"capabilities"`
				ServerInfo      serverInfo     `json:"serverInfo"`
			}
			if err := json.Unmarshal(response.Result, &result); err != nil {
				t.Fatalf("decode result: %v", err)
			}
			if result.ProtocolVersion != testCase.wantEcho {
				t.Fatalf("protocolVersion = %q, want %q", result.ProtocolVersion, testCase.wantEcho)
			}
			if _, declared := result.Capabilities["tools"]; !declared {
				t.Fatalf("capabilities missing tools: %v", result.Capabilities)
			}
			if result.ServerInfo.Name != "liteapi" || result.ServerInfo.Version != "0.1.0" {
				t.Fatalf("serverInfo = %+v", result.ServerInfo)
			}
			if string(response.ID) != "1" {
				t.Fatalf("id = %s, want 1", response.ID)
			}
		})
	}
}

func TestInitializeEchoesStringIdsUnchanged(t *testing.T) {
	server := newTestServer(t, newFixtureBackend())
	response := rpcCall(t, server, `{"jsonrpc":"2.0","id":"call-7","method":"initialize","params":{}}`, http.StatusOK)
	if string(response.ID) != `"call-7"` {
		t.Fatalf("id = %s, want \"call-7\"", response.ID)
	}
}

func TestNotificationsAreAcceptedWithNoBody(t *testing.T) {
	server := newTestServer(t, newFixtureBackend())
	for _, body := range []string{
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":1}}`,
		// A notification naming a method we do not implement is still a
		// notification: there is no id to answer on.
		`{"jsonrpc":"2.0","method":"notifications/unheard-of"}`,
	} {
		status, text := rpcPost(t, server, body)
		if status != http.StatusAccepted {
			t.Fatalf("%s: status = %d, want 202", body, status)
		}
		if text != "" {
			t.Fatalf("%s: body = %q, want empty", body, text)
		}
	}
}

func TestPingReturnsAnEmptyResult(t *testing.T) {
	server := newTestServer(t, newFixtureBackend())
	response := rpcCall(t, server, `{"jsonrpc":"2.0","id":9,"method":"ping"}`, http.StatusOK)
	if response.Error != nil {
		t.Fatalf("ping failed: %+v", response.Error)
	}
	if string(response.Result) != "{}" {
		t.Fatalf("result = %s, want {}", response.Result)
	}
}

// The registry is the whole of what an agent can reach, so the listing is
// pinned by name: a tool appearing here that nobody meant to ship is exactly
// the change this test exists to catch.
//
// The write tier IS listed, and listed unconditionally — rule 5 rejects those
// calls while the user has authoring switched off rather than hiding the tools,
// because a tool that vanished would tell the agent the capability does not
// exist and send it off to build a worse substitute by hand. This fixture has
// the tier off, and the four write tools appear all the same.
func TestToolsListDeclaresExactlyTheShippedTools(t *testing.T) {
	server := newTestServer(t, newFixtureBackend())
	response := rpcCall(t, server, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`, http.StatusOK)
	if response.Error != nil {
		t.Fatalf("tools/list failed: %+v", response.Error)
	}
	var result struct {
		Tools []struct {
			Name        string      `json:"name"`
			Description string      `json:"description"`
			InputSchema inputSchema `json:"inputSchema"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(response.Result, &result); err != nil {
		t.Fatalf("decode result: %v", err)
	}

	want := []string{
		"list_collections", "list_requests", "search_requests", "get_request", "inspect_request", "list_environments",
		"list_flows", "get_flow", "get_history", "describe_usage",
		"run_request", "run_flow",
		"create_request", "update_request", "create_flow", "update_flow",
	}
	if len(result.Tools) != len(want) {
		t.Fatalf("got %d tools, want %d: %+v", len(result.Tools), len(want), result.Tools)
	}
	seen := map[string]bool{}
	for _, tool := range result.Tools {
		seen[tool.Name] = true
		if tool.Description == "" {
			t.Fatalf("tool %q has no description", tool.Name)
		}
		if tool.InputSchema.Type != "object" {
			t.Fatalf("tool %q schema type = %q, want object", tool.Name, tool.InputSchema.Type)
		}
		if tool.InputSchema.Properties == nil || tool.InputSchema.Required == nil {
			t.Fatalf("tool %q schema has a nil properties or required: %+v", tool.Name, tool.InputSchema)
		}
		for _, required := range tool.InputSchema.Required {
			property, declared := tool.InputSchema.Properties[required]
			if !declared {
				t.Fatalf("tool %q requires %q but does not declare it", tool.Name, required)
			}
			if property.Type == "" || property.Description == "" {
				t.Fatalf("tool %q property %q is under-specified: %+v", tool.Name, required, property)
			}
		}
	}
	for _, name := range want {
		if !seen[name] {
			t.Fatalf("tools/list is missing %q", name)
		}
	}

	// Descriptions are the only documentation an agent reads, so the safety
	// facts have to be in them rather than in this repo's docs.
	raw := string(response.Result)
	for _, phrase := range []string{"{{templates}}", "masked", "run_request"} {
		if !strings.Contains(raw, phrase) {
			t.Fatalf("tool descriptions never mention %q", phrase)
		}
	}
}

func TestAuthorizationIsRequiredOnEveryRequest(t *testing.T) {
	server := newTestServer(t, newFixtureBackend())
	const body = `{"jsonrpc":"2.0","id":1,"method":"ping"}`

	status, _ := rpcPost(t, server, body, func(request *http.Request) {
		request.Header.Del("Authorization")
	})
	if status != http.StatusUnauthorized {
		t.Fatalf("missing token: status = %d, want 401", status)
	}

	const wrongToken = "not-the-pairing-token"
	status, text := rpcPost(t, server, body, withHeader("Authorization", "Bearer "+wrongToken))
	if status != http.StatusUnauthorized {
		t.Fatalf("wrong token: status = %d, want 401", status)
	}
	if strings.Contains(text, wrongToken) {
		t.Fatalf("401 body echoed the presented token: %s", text)
	}

	// A token-shaped header that is not a bearer scheme is still no token.
	status, _ = rpcPost(t, server, body, withHeader("Authorization", testToken))
	if status != http.StatusUnauthorized {
		t.Fatalf("bare token without scheme: status = %d, want 401", status)
	}

	status, _ = rpcPost(t, server, body)
	if status != http.StatusOK {
		t.Fatalf("correct token: status = %d, want 200", status)
	}
}

func TestOriginMustBeLoopbackWhenPresent(t *testing.T) {
	server := newTestServer(t, newFixtureBackend())
	const body = `{"jsonrpc":"2.0","id":1,"method":"ping"}`

	cases := []struct {
		origin string
		want   int
	}{
		{"https://evil.example.com", http.StatusForbidden},
		// A hostile origin that merely contains "localhost" must not pass.
		{"https://localhost.evil.example.com", http.StatusForbidden},
		{"file://", http.StatusForbidden},
		{"http://localhost:5173", http.StatusOK},
		{"http://127.0.0.1:34115", http.StatusOK},
		{"https://localhost", http.StatusOK},
	}
	for _, testCase := range cases {
		status, text := rpcPost(t, server, body, withHeader("Origin", testCase.origin))
		if status != testCase.want {
			t.Fatalf("origin %q: status = %d, want %d (body %s)", testCase.origin, status, testCase.want, text)
		}
	}

	// No Origin at all is the normal case: a CLI agent is not a browser.
	if status, _ := rpcPost(t, server, body); status != http.StatusOK {
		t.Fatalf("absent origin: status = %d, want 200", status)
	}
}

func TestOnlyPostCarriesProtocolTraffic(t *testing.T) {
	server := newTestServer(t, newFixtureBackend())
	for _, method := range []string{http.MethodGet, http.MethodDelete} {
		status, _ := doRequest(t, server, method, "/mcp", "")
		if status != http.StatusMethodNotAllowed {
			t.Fatalf("%s /mcp: status = %d, want 405", method, status)
		}
	}
}

func TestNonProtocolPathsAreNotFound(t *testing.T) {
	server := newTestServer(t, newFixtureBackend())
	for _, path := range []string{"/", "/mcp/tools", "/status"} {
		status, _ := doRequest(t, server, http.MethodPost, path, `{"jsonrpc":"2.0","id":1,"method":"ping"}`)
		if status != http.StatusNotFound {
			t.Fatalf("POST %s: status = %d, want 404", path, status)
		}
	}
}

func TestBatchedMessagesAreRejected(t *testing.T) {
	server := newTestServer(t, newFixtureBackend())
	response := rpcCall(t, server, `[{"jsonrpc":"2.0","id":1,"method":"ping"}]`, http.StatusBadRequest)
	if response.Error == nil || response.Error.Code != codeInvalidRequest {
		t.Fatalf("error = %+v, want code %d", response.Error, codeInvalidRequest)
	}
	if string(response.ID) != "null" {
		t.Fatalf("id = %s, want null", response.ID)
	}
}

func TestMalformedJSONIsAParseError(t *testing.T) {
	server := newTestServer(t, newFixtureBackend())
	for _, body := range []string{`{"jsonrpc":"2.0",`, `not json at all`, ``} {
		response := rpcCall(t, server, body, http.StatusBadRequest)
		if response.Error == nil || response.Error.Code != codeParseError {
			t.Fatalf("body %q: error = %+v, want code %d", body, response.Error, codeParseError)
		}
	}
}

func TestNonObjectAndVersionlessMessagesAreInvalidRequests(t *testing.T) {
	server := newTestServer(t, newFixtureBackend())
	for _, body := range []string{`42`, `"ping"`, `{"id":1,"method":"ping"}`, `{"jsonrpc":"1.0","id":1,"method":"ping"}`} {
		response := rpcCall(t, server, body, http.StatusBadRequest)
		if response.Error == nil || response.Error.Code != codeInvalidRequest {
			t.Fatalf("body %q: error = %+v, want code %d", body, response.Error, codeInvalidRequest)
		}
	}
}

func TestUnknownMethodIsMethodNotFound(t *testing.T) {
	server := newTestServer(t, newFixtureBackend())
	response := rpcCall(t, server, `{"jsonrpc":"2.0","id":3,"method":"resources/list"}`, http.StatusOK)
	if response.Error == nil || response.Error.Code != codeMethodNotFound {
		t.Fatalf("error = %+v, want code %d", response.Error, codeMethodNotFound)
	}
	if !strings.Contains(response.Error.Message, "resources/list") {
		t.Fatalf("message does not name the method: %q", response.Error.Message)
	}
}

func TestSessionHeaderIsIgnored(t *testing.T) {
	// The server is stateless: a client that carries a session id from another
	// server, or invents one, must not be treated differently.
	server := newTestServer(t, newFixtureBackend())
	status, _ := rpcPost(t, server, `{"jsonrpc":"2.0","id":1,"method":"ping"}`, withHeader("Mcp-Session-Id", "whatever"))
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
}

func TestResponsesAreJSONEvenWhenTheClientAcceptsEventStream(t *testing.T) {
	server := newTestServer(t, newFixtureBackend())
	request, err := http.NewRequest(http.MethodPost, server.URL+"/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	request.Header.Set("Authorization", "Bearer "+testToken)
	request.Header.Set("Accept", "application/json, text/event-stream")
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer func() { _ = response.Body.Close() }()
	if got := response.Header.Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}
}

// TestBearerTokenNeverAppearsInAnyResponseBody is the leak gate. doRequest
// already checks every exchange these tests make; this walks the paths where a
// naive implementation would echo the credential — the two 401s, the refusals
// that quote what arrived, and a successful listing — so the guarantee is
// stated in one place rather than only implied.
func TestBearerTokenNeverAppearsInAnyResponseBody(t *testing.T) {
	server := newTestServer(t, newFixtureBackend())
	const ping = `{"jsonrpc":"2.0","id":1,"method":"ping"}`

	exchanges := []struct {
		name   string
		method string
		path   string
		body   string
		mutate []func(*http.Request)
	}{
		{name: "missing token", method: http.MethodPost, path: "/mcp", body: ping, mutate: []func(*http.Request){func(r *http.Request) { r.Header.Del("Authorization") }}},
		{name: "wrong token", method: http.MethodPost, path: "/mcp", body: ping, mutate: []func(*http.Request){withHeader("Authorization", "Bearer wrong-"+testToken[:8])}},
		{name: "forbidden origin", method: http.MethodPost, path: "/mcp", body: ping, mutate: []func(*http.Request){withHeader("Origin", "https://evil.example.com")}},
		{name: "method not allowed", method: http.MethodGet, path: "/mcp", body: ""},
		{name: "parse error", method: http.MethodPost, path: "/mcp", body: `{oops`},
		{name: "unknown method", method: http.MethodPost, path: "/mcp", body: `{"jsonrpc":"2.0","id":1,"method":"nope"}`},
		{name: "tools list", method: http.MethodPost, path: "/mcp", body: `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`},
		{name: "tool call", method: http.MethodPost, path: "/mcp", body: `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"list_collections","arguments":{}}}`},
		{name: "unknown tool", method: http.MethodPost, path: "/mcp", body: `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"run_request","arguments":{}}}`},
	}
	for _, exchange := range exchanges {
		t.Run(exchange.name, func(t *testing.T) {
			// doRequest fails the test if the token appears anywhere in the
			// body; reaching here means this path is clean.
			_, text := doRequest(t, server, exchange.method, exchange.path, exchange.body, exchange.mutate...)
			if strings.Contains(strings.ToLower(text), "bearer "+strings.ToLower(testToken)) {
				t.Fatalf("body carried the credential: %s", text)
			}
		})
	}
}
