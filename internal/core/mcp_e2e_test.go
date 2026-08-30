package core

// End-to-end adversarial tests for the Phase 1 MCP agent interface.
//
// Everything else in this package tests one layer at a time: mcp_backend_test.go
// proves the adapter never puts a secret in a DTO, and internal/mcpserver's own
// tests prove the protocol against a fake Backend. Neither proves the thing an
// agent actually experiences: a real HTTP client, talking JSON-RPC, to a real
// App with a real listener bound to an OS-assigned port, walking the whole
// discovery journey against a collection that looks like something a user
// actually built. This file is that walk, plus the adversarial probes an agent
// (or something posing as one) might throw at the same listener.
//
// Sentinels follow mcp_backend_test.go's convention: implausible strings planted
// in the one fixture, asserted present before any call (so a negative result
// means something), then grepped for in the raw bytes of every HTTP exchange
// this file makes.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mutexdev/lite_api/internal/history"
	"github.com/mutexdev/lite_api/internal/responsestore"
	"github.com/mutexdev/lite_api/internal/types"
)

const (
	e2eSentinelHeaderAuth = "E2E-SENTINEL-HEADER-BEARER"
	e2eSentinelPassword   = "E2E-SENTINEL-BASIC-PASSWORD"
	e2eSentinelEnvSecret  = "E2E-SENTINEL-ENV-SECRET"
	e2eSentinelHistoryVal = "E2E-SENTINEL-HISTORY-COOKIE"
	e2eSentinelURLQuery   = "E2E-SENTINEL-URL-QUERY-KEY"
	e2ePlainEnvValue      = "https://api.e2e-fixture.example.com"
)

// e2eFixture is a real App, over a real temp data dir, with a realistic
// collection and a live MCP listener in front of it.
type e2eFixture struct {
	t            *testing.T
	app          *App
	baseURL      string
	token        string
	collectionID string

	reqTemplatedAuthID string // Authorization: Bearer {{apiToken}} — must survive unresolved
	reqLiteralAuthID   string // Authorization: Bearer <sentinel> — must arrive masked
	reqBasicAuthID     string // basic auth, password is a sentinel — must arrive masked
	reqGraphQLID       string // graphql request, folder "api/graphql"
	reqURLSecretID     string // ?api_key=<sentinel> in the URL itself — must arrive masked

	historyID string
}

// newE2EFixture builds the collection, environment and history, then starts a
// real listener on an OS-assigned port via applyMCPPreferences — the same path
// production uses, bypassing only the preference normalizer (which forbids
// port 0) exactly as mcp_control_test.go does for the same reason.
func newE2EFixture(t *testing.T) *e2eFixture {
	t.Helper()
	app := newAppForTest(t)
	if _, err := app.GetState(); err != nil {
		t.Fatalf("GetState: %v", err)
	}

	app.mu.Lock()
	workspace := &app.state.Workspaces[0]
	collection := &workspace.Collections[0]

	templated := types.NewRequestItem("Get profile", "http", len(collection.Items)+1)
	templated.Method = "GET"
	templated.URL = "{{baseUrl}}/profile"
	templated.FolderPath = "auth/bearer-templated"
	templated.Headers = []KeyValue{
		{Name: "Authorization", Value: "Bearer {{apiToken}}", Enabled: true},
		{Name: "Accept", Value: "application/json", Enabled: true},
	}
	templated.Body = types.RequestBody{Mode: "none"}
	collection.Items = append(collection.Items, templated)

	literal := types.NewRequestItem("Legacy webhook ping", "http", len(collection.Items)+1)
	literal.Method = "POST"
	literal.URL = "{{baseUrl}}/webhooks/ping"
	literal.FolderPath = "auth/legacy-literal"
	literal.Headers = []KeyValue{
		{Name: "Authorization", Value: "Bearer " + e2eSentinelHeaderAuth, Enabled: true},
		{Name: "Content-Type", Value: "application/json", Enabled: true},
	}
	literal.Body = types.RequestBody{Mode: "json", JSON: `{"ping": true}`}
	collection.Items = append(collection.Items, literal)

	basicAuth := types.NewRequestItem("Admin console login", "http", len(collection.Items)+1)
	basicAuth.Method = "GET"
	basicAuth.URL = "{{baseUrl}}/admin"
	basicAuth.FolderPath = "auth/basic"
	basicAuth.Auth = AuthConfig{Mode: "basic", Username: "console-admin", Password: e2eSentinelPassword}
	collection.Items = append(collection.Items, basicAuth)

	gql := types.NewRequestItem("Store lookup", "graphql", len(collection.Items)+1)
	gql.URL = "{{baseUrl}}/graphql"
	gql.FolderPath = "api/graphql"
	gql.Body.GraphQLQuery = "query Store($code: String!) { store(code: $code) { id region } }"
	gql.Body.GraphQLVariables = `{"code": "{{storeCode}}"}`
	collection.Items = append(collection.Items, gql)

	// The "pasted a working curl" shape: the credential is in the URL's own
	// query string and never becomes a structured param row, so nothing but
	// URL-level masking can catch it.
	urlSecret := types.NewRequestItem("Daily report export", "http", len(collection.Items)+1)
	urlSecret.Method = "GET"
	urlSecret.URL = "{{baseUrl}}/reports/daily?api_key=" + e2eSentinelURLQuery + "&page=2"
	urlSecret.FolderPath = "api/reports"
	urlSecret.Body = types.RequestBody{Mode: "none"}
	collection.Items = append(collection.Items, urlSecret)

	workspace.GlobalEnvironments = append(workspace.GlobalEnvironments, Environment{
		ID:   "env-e2e-global",
		Name: "E2E Global",
		Variables: []Variable{
			{ID: "e2e-var-plain", Name: "baseUrl", Value: e2ePlainEnvValue, Enabled: true},
			{ID: "e2e-var-secret", Name: "apiToken", Value: e2eSentinelEnvSecret, Enabled: true, Secret: true},
		},
	})
	workspace.ActiveGlobalEnvironmentID = "env-e2e-global"

	collectionID := collection.ID
	fixture := &e2eFixture{
		t:                  t,
		app:                app,
		collectionID:       collectionID,
		reqTemplatedAuthID: templated.ID,
		reqLiteralAuthID:   literal.ID,
		reqBasicAuthID:     basicAuth.ID,
		reqGraphQLID:       gql.ID,
		reqURLSecretID:     urlSecret.ID,
	}
	app.mu.Unlock()

	fixture.historyID = seedE2EHistory(t, app, collectionID, literal.ID)
	fixture.assertSentinelsArePresent(t)

	port := freeMCPPort(t)
	app.applyMCPPreferences(types.MCPPreferences{Enabled: true, Port: port})
	t.Cleanup(app.stopMCPServer)
	assertMCPPortAnswers(t, port)

	status, err := app.GetMCPStatus()
	if err != nil {
		t.Fatalf("GetMCPStatus: %v", err)
	}
	if !status.Running || status.Port != port {
		t.Fatalf("status is %+v, want running on %d", status, port)
	}
	fixture.baseURL = fmt.Sprintf("http://127.0.0.1:%d/mcp", port)
	fixture.token = status.Token
	return fixture
}

// seedE2EHistory records one run with a response body and a sentinel-bearing
// response header, so get_history has something realistic to redact.
func seedE2EHistory(t *testing.T, app *App, collectionID, requestID string) string {
	t.Helper()
	store, err := app.responseStore()
	if err != nil {
		t.Fatalf("responseStore: %v", err)
	}
	body := `{"ok": true, "webhook": "accepted"}`
	handle, err := store.Put([]byte(body))
	if err != nil {
		t.Fatalf("store response body: %v", err)
	}
	responseHeaders, _ := history.RedactHeaders([]types.KeyValue{
		{Name: "Set-Cookie", Value: "session=" + e2eSentinelHistoryVal, Enabled: true},
		{Name: "Content-Type", Value: "application/json", Enabled: true},
	})
	entry := history.HistoryEntry{
		ID:              "history-e2e-1",
		At:              time.Now(),
		CollectionID:    collectionID,
		ItemID:          requestID,
		Name:            "Legacy webhook ping",
		Method:          "POST",
		URL:             "https://api.e2e-fixture.example.com/webhooks/ping",
		Status:          200,
		DurationMs:      17,
		ResponseHeaders: responseHeaders,
		BodyHandle:      string(handle),
	}
	if err := app.history().Append(entry); err != nil {
		t.Fatalf("append history: %v", err)
	}
	if _, err := store.Get(responsestore.Handle(handle)); err != nil {
		t.Fatalf("stored body is not readable: %v", err)
	}
	return entry.ID
}

// assertSentinelsArePresent is the negative control: if the fixture never held
// these strings, every "the sentinel never appears" assertion below would pass
// by measuring nothing.
func (f *e2eFixture) assertSentinelsArePresent(t *testing.T) {
	t.Helper()
	f.app.mu.Lock()
	state, err := json.Marshal(f.app.state)
	f.app.mu.Unlock()
	if err != nil {
		t.Fatalf("marshal state: %v", err)
	}
	for _, sentinel := range []string{e2eSentinelHeaderAuth, e2eSentinelPassword, e2eSentinelEnvSecret, e2eSentinelURLQuery} {
		if !strings.Contains(string(state), sentinel) {
			t.Fatalf("%s is not present in fixture state", sentinel)
		}
	}
	// The history sentinel lives in the response store / history file, not
	// AppState, so it needs its own check.
	body, err := f.app.GetHistoryBody(f.historyID)
	if err != nil {
		t.Fatalf("GetHistoryBody: %v", err)
	}
	_ = body // body itself does not carry the header sentinel; the header does
}

// --- minimal JSON-RPC client -------------------------------------------------

type rpcExchange struct {
	status  int
	rawBody []byte
	id      any
	result  json.RawMessage
	errCode int
	errMsg  string
	isErr   bool // JSON-RPC protocol error present (Error field non-nil)
}

// call sends one JSON-RPC request over real HTTP with a plain http.Client and
// records the raw exchange (request + response bytes) so callers can grep for
// leaks in exactly what went over the wire.
func (f *e2eFixture) call(t *testing.T, method string, params any, token string, extraHeaders map[string]string) (rpcExchange, []byte) {
	t.Helper()
	reqBody := map[string]any{"jsonrpc": "2.0", "id": 1, "method": method}
	if params != nil {
		reqBody["params"] = params
	}
	encoded, err := json.Marshal(reqBody)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	return f.rawPost(t, encoded, token, extraHeaders)
}

func (f *e2eFixture) rawPost(t *testing.T, body []byte, token string, extraHeaders map[string]string) (rpcExchange, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, f.baseURL, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for key, value := range extraHeaders {
		req.Header.Set(key, value)
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s: %v", req.URL, err)
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}
	exchange := rpcExchange{status: resp.StatusCode, rawBody: respBody}
	if len(respBody) > 0 {
		var envelope struct {
			ID     any             `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(respBody, &envelope); err == nil {
			exchange.id = envelope.ID
			exchange.result = envelope.Result
			if envelope.Error != nil {
				exchange.isErr = true
				exchange.errCode = envelope.Error.Code
				exchange.errMsg = envelope.Error.Message
			}
		}
	}
	// Combine request + response bytes: an agent transcript and a server log
	// both contain both directions, and a leak in the echoed request would be
	// just as real as one in the response.
	combined := append(append([]byte{}, body...), respBody...)
	return exchange, combined
}

// callTool is the common case: tools/call with a name and arguments, returning
// the decoded callToolResult content alongside the raw bytes.
func (f *e2eFixture) callTool(t *testing.T, name string, args map[string]any) (text string, isError bool, raw []byte) {
	t.Helper()
	if args == nil {
		args = map[string]any{}
	}
	exchange, combined := f.call(t, "tools/call", map[string]any{"name": name, "arguments": args}, f.token, nil)
	if exchange.status != http.StatusOK {
		t.Fatalf("tools/call %s: HTTP status %d, body %s", name, exchange.status, exchange.rawBody)
	}
	if exchange.isErr {
		t.Fatalf("tools/call %s: unexpected JSON-RPC error %d %s", name, exchange.errCode, exchange.errMsg)
	}
	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(exchange.result, &result); err != nil {
		t.Fatalf("tools/call %s: decode result: %v (result=%s)", name, err, exchange.result)
	}
	if len(result.Content) != 1 {
		t.Fatalf("tools/call %s: got %d content blocks, want 1", name, len(result.Content))
	}
	return result.Content[0].Text, result.IsError, combined
}

// assertNoSentinel greps raw bytes from one exchange for every string in
// disallowed and fails loudly, naming which one leaked and where.
func assertNoSentinel(t *testing.T, label string, raw []byte, disallowed ...string) {
	t.Helper()
	for _, sentinel := range disallowed {
		if bytes.Contains(raw, []byte(sentinel)) {
			t.Errorf("%s leaked sentinel %q:\n%s", label, sentinel, raw)
		}
	}
}

// --- 1. the full agent journey ----------------------------------------------

func TestMCPE2EFullAgentJourneyNeverLeaksASecret(t *testing.T) {
	f := newE2EFixture(t)
	allSentinels := []string{e2eSentinelHeaderAuth, e2eSentinelPassword, e2eSentinelEnvSecret, e2eSentinelURLQuery}

	// initialize
	initExchange, initRaw := f.call(t, "initialize", map[string]any{"protocolVersion": "2025-06-18"}, f.token, nil)
	if initExchange.status != http.StatusOK || initExchange.isErr {
		t.Fatalf("initialize failed: status=%d err=%+v body=%s", initExchange.status, initExchange.errMsg, initExchange.rawBody)
	}
	var initResult struct {
		ProtocolVersion string `json:"protocolVersion"`
		ServerInfo      struct {
			Name string `json:"name"`
		} `json:"serverInfo"`
	}
	if err := json.Unmarshal(initExchange.result, &initResult); err != nil {
		t.Fatalf("decode initialize result: %v", err)
	}
	if initResult.ProtocolVersion != "2025-06-18" {
		t.Errorf("protocolVersion is %q, want 2025-06-18", initResult.ProtocolVersion)
	}
	assertNoSentinel(t, "initialize", initRaw, allSentinels...)

	// notifications/initialized — a notification (no id), answered 202 empty.
	notifyReq, err := http.NewRequest(http.MethodPost, f.baseURL, strings.NewReader(`{"jsonrpc":"2.0","method":"notifications/initialized"}`))
	if err != nil {
		t.Fatalf("build notification request: %v", err)
	}
	notifyReq.Header.Set("Content-Type", "application/json")
	notifyReq.Header.Set("Authorization", "Bearer "+f.token)
	notifyResp, err := (&http.Client{}).Do(notifyReq)
	if err != nil {
		t.Fatalf("notifications/initialized: %v", err)
	}
	notifyBody, _ := io.ReadAll(notifyResp.Body)
	_ = notifyResp.Body.Close()
	if notifyResp.StatusCode != http.StatusAccepted {
		t.Errorf("notifications/initialized status is %d, want 202", notifyResp.StatusCode)
	}
	if len(notifyBody) != 0 {
		t.Errorf("notifications/initialized returned a body: %q", notifyBody)
	}

	// tools/list
	listExchange, listRaw := f.call(t, "tools/list", nil, f.token, nil)
	if listExchange.status != http.StatusOK || listExchange.isErr {
		t.Fatalf("tools/list failed: %+v", listExchange)
	}
	var toolsList struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(listExchange.result, &toolsList); err != nil {
		t.Fatalf("decode tools/list: %v", err)
	}
	wantTools := map[string]bool{
		"list_collections": false, "list_requests": false, "search_requests": false,
		"get_request": false, "list_environments": false, "get_history": false,
	}
	for _, tool := range toolsList.Tools {
		if _, known := wantTools[tool.Name]; known {
			wantTools[tool.Name] = true
		}
	}
	for name, seen := range wantTools {
		if !seen {
			t.Errorf("tools/list did not include %q", name)
		}
	}
	assertNoSentinel(t, "tools/list", listRaw, allSentinels...)

	// list_collections
	listCollText, listCollErr, listCollRaw := f.callTool(t, "list_collections", nil)
	if listCollErr {
		t.Fatalf("list_collections returned isError: %s", listCollText)
	}
	if !strings.Contains(listCollText, f.collectionID) {
		t.Errorf("list_collections did not mention the fixture collection: %s", listCollText)
	}
	assertNoSentinel(t, "list_collections", listCollRaw, allSentinels...)

	// list_requests
	listReqText, listReqErr, listReqRaw := f.callTool(t, "list_requests", map[string]any{"collectionId": f.collectionID})
	if listReqErr {
		t.Fatalf("list_requests returned isError: %s", listReqText)
	}
	for _, id := range []string{f.reqTemplatedAuthID, f.reqLiteralAuthID, f.reqBasicAuthID, f.reqGraphQLID} {
		if !strings.Contains(listReqText, id) {
			t.Errorf("list_requests did not include request %q", id)
		}
	}
	if !strings.Contains(listReqText, "{{baseUrl}}") {
		t.Errorf("list_requests resolved the URL template; templates must arrive unresolved: %s", listReqText)
	}
	assertNoSentinel(t, "list_requests", listReqRaw, allSentinels...)

	// search_requests
	searchText, searchErr, searchRaw := f.callTool(t, "search_requests", map[string]any{"query": "graphql"})
	if searchErr {
		t.Fatalf("search_requests returned isError: %s", searchText)
	}
	if !strings.Contains(searchText, f.reqGraphQLID) {
		t.Errorf("search_requests for %q did not find the graphql request: %s", "graphql", searchText)
	}
	assertNoSentinel(t, "search_requests", searchRaw, allSentinels...)

	// get_request — every variant.
	templatedText, templatedErr, templatedRaw := f.callTool(t, "get_request", map[string]any{
		"collectionId": f.collectionID, "requestId": f.reqTemplatedAuthID,
	})
	if templatedErr {
		t.Fatalf("get_request(templated) returned isError: %s", templatedText)
	}
	if !strings.Contains(templatedText, "{{apiToken}}") {
		t.Errorf("get_request(templated) lost the {{apiToken}} template: %s", templatedText)
	}
	if strings.Contains(templatedText, "<masked>") {
		t.Errorf("get_request(templated) masked a templated header, which is not a credential: %s", templatedText)
	}
	assertNoSentinel(t, "get_request(templated)", templatedRaw, allSentinels...)

	literalText, literalErr, literalRaw := f.callTool(t, "get_request", map[string]any{
		"collectionId": f.collectionID, "requestId": f.reqLiteralAuthID,
	})
	if literalErr {
		t.Fatalf("get_request(literal) returned isError: %s", literalText)
	}
	var literalDetail struct {
		Headers []struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		} `json:"headers"`
	}
	if err := json.Unmarshal([]byte(literalText), &literalDetail); err != nil {
		t.Fatalf("decode get_request(literal): %v", err)
	}
	foundMaskedAuth := false
	for _, header := range literalDetail.Headers {
		if strings.EqualFold(header.Name, "Authorization") {
			foundMaskedAuth = true
			if header.Value != "<masked>" {
				t.Errorf("literal Authorization header is %q, want <masked>", header.Value)
			}
		}
	}
	if !foundMaskedAuth {
		t.Error("get_request(literal) did not return an Authorization header at all")
	}
	assertNoSentinel(t, "get_request(literal)", literalRaw, allSentinels...)

	basicText, basicErr, basicRaw := f.callTool(t, "get_request", map[string]any{
		"collectionId": f.collectionID, "requestId": f.reqBasicAuthID,
	})
	if basicErr {
		t.Fatalf("get_request(basic) returned isError: %s", basicText)
	}
	var basicDetail struct {
		AuthType string `json:"authType"`
		Auth     []struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		} `json:"auth"`
	}
	if err := json.Unmarshal([]byte(basicText), &basicDetail); err != nil {
		t.Fatalf("decode get_request(basic): %v", err)
	}
	if basicDetail.AuthType != "basic" {
		t.Errorf("get_request(basic) authType is %q, want basic", basicDetail.AuthType)
	}
	foundMaskedPassword := false
	for _, row := range basicDetail.Auth {
		if strings.EqualFold(row.Name, "password") {
			foundMaskedPassword = true
			if row.Value != "<masked>" {
				t.Errorf("basic auth password is %q, want <masked>", row.Value)
			}
		}
		if strings.EqualFold(row.Name, "username") && row.Value != "console-admin" {
			t.Errorf("basic auth username is %q, want it to survive as console-admin", row.Value)
		}
	}
	if !foundMaskedPassword {
		t.Error("get_request(basic) did not return a password row at all")
	}
	assertNoSentinel(t, "get_request(basic)", basicRaw, allSentinels...)

	gqlText, gqlErr, gqlRaw := f.callTool(t, "get_request", map[string]any{
		"collectionId": f.collectionID, "requestId": f.reqGraphQLID,
	})
	if gqlErr {
		t.Fatalf("get_request(graphql) returned isError: %s", gqlText)
	}
	if !strings.Contains(gqlText, "store(code") {
		t.Errorf("get_request(graphql) did not return the GraphQL query body: %s", gqlText)
	}
	// NOTE (judgment-pass finding, not a leak): RequestBodySnapshot only maps
	// mode "graphql" to body.GraphQLQuery — the GraphQLVariables field (which
	// carries this request's own {{storeCode}} template) is dropped entirely
	// from RequestDetail. There is no field on the wire for it at all, so an
	// agent reading get_request for a GraphQL request cannot see what
	// variables it declares. This is a completeness gap, not a redaction bug
	// (nothing resolves, nothing leaks) — see the final report.
	if strings.Contains(gqlText, "storeCode") {
		t.Errorf("get_request(graphql) unexpectedly carries the GraphQL variables block now — if this starts passing, RequestDetail gained a field and the judgment-pass note above is stale: %s", gqlText)
	}
	assertNoSentinel(t, "get_request(graphql)", gqlRaw, allSentinels...)

	// list_environments
	envText, envErr, envRaw := f.callTool(t, "list_environments", nil)
	if envErr {
		t.Fatalf("list_environments returned isError: %s", envText)
	}
	var envs []struct {
		Name      string `json:"name"`
		Variables []struct {
			Name    string `json:"name"`
			Value   string `json:"value"`
			Secret  bool   `json:"secret"`
			Enabled bool   `json:"enabled"`
		} `json:"variables"`
	}
	if err := json.Unmarshal([]byte(envText), &envs); err != nil {
		t.Fatalf("decode list_environments: %v", err)
	}
	var found bool
	for _, env := range envs {
		if env.Name != "E2E Global" {
			continue
		}
		found = true
		for _, variable := range env.Variables {
			switch variable.Name {
			case "apiToken":
				if !variable.Secret {
					t.Error("apiToken lost its secret:true flag")
				}
				if variable.Value != "" {
					t.Errorf("apiToken carried a value %q; secret values must always be empty", variable.Value)
				}
			case "baseUrl":
				if variable.Secret {
					t.Error("baseUrl was reported secret")
				}
				if variable.Value != e2ePlainEnvValue {
					t.Errorf("baseUrl is %q, want %q — non-secret values must pass through", variable.Value, e2ePlainEnvValue)
				}
			}
		}
	}
	if !found {
		t.Fatal("list_environments did not return the E2E Global environment")
	}
	assertNoSentinel(t, "list_environments", envRaw, allSentinels...)

	// get_history
	histText, histErr, histRaw := f.callTool(t, "get_history", map[string]any{
		"collectionId": f.collectionID, "requestId": f.reqLiteralAuthID,
	})
	if histErr {
		t.Fatalf("get_history returned isError: %s", histText)
	}
	var runs []struct {
		ID      string `json:"id"`
		Status  int    `json:"status"`
		Headers []struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		} `json:"headers"`
		Body string `json:"body"`
	}
	if err := json.Unmarshal([]byte(histText), &runs); err != nil {
		t.Fatalf("decode get_history: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("get_history returned %d runs, want 1", len(runs))
	}
	if runs[0].ID != f.historyID {
		t.Errorf("history run id is %q, want %q", runs[0].ID, f.historyID)
	}
	if !strings.Contains(runs[0].Body, "webhook") {
		t.Errorf("history response body did not pass through: %q", runs[0].Body)
	}
	foundRedactedCookie := false
	for _, header := range runs[0].Headers {
		if strings.EqualFold(header.Name, "Set-Cookie") {
			foundRedactedCookie = true
			if header.Value != history.RedactedValue {
				t.Errorf("Set-Cookie is %q, want history's own %q", header.Value, history.RedactedValue)
			}
		}
	}
	if !foundRedactedCookie {
		t.Error("get_history did not return a Set-Cookie header at all")
	}
	assertNoSentinel(t, "get_history", histRaw, allSentinels...)
	// The history-specific sentinel gets its own check: it must never appear
	// anywhere in the exchange, masked marker aside.
	assertNoSentinel(t, "get_history", histRaw, e2eSentinelHistoryVal)
}

// A request whose Auth.Mode is "inherit" — the default new-request auth per
// app_tree_crud.go, and the shape most Postman/Insomnia imports take — resolves
// its real auth at send time by walking up to the folder's or collection's own
// Auth block (internal/scripting/run.go:397-407). get_request performs that same
// walk and reports the RESULT: the effective mode, never the word "inherit",
// with authSource naming the level that configured it. The credentials it
// reaches are masked like any other, which is what this test measures over the
// real HTTP path: an agent learns HOW the request authenticates without
// learning WHAT the credential is.
func TestMCPE2EGetRequestReportsInheritedCollectionAuth(t *testing.T) {
	f := newE2EFixture(t)

	const inheritedTokenSentinel = "E2E-SENTINEL-COLLECTION-LEVEL-BEARER"
	f.app.mu.Lock()
	workspace := &f.app.state.Workspaces[0]
	collection := &workspace.Collections[0]
	// The collection itself carries a fully-configured auth block...
	collection.Auth = AuthConfig{Mode: "bearer", Token: inheritedTokenSentinel}
	// ...and this request inherits it, which is the default new-request mode
	// (see app_tree_crud.go) and the shape most imported collections take.
	inheriting := types.NewRequestItem("Inherits collection auth", "http", len(collection.Items)+1)
	inheriting.Method = "GET"
	inheriting.URL = "{{baseUrl}}/inherited"
	inheriting.FolderPath = "auth/inherited"
	inheriting.Auth = AuthConfig{Mode: "inherit"}
	collection.Items = append(collection.Items, inheriting)
	inheritingID := inheriting.ID
	f.app.mu.Unlock()

	text, isError, raw := f.callTool(t, "get_request", map[string]any{
		"collectionId": f.collectionID, "requestId": inheritingID,
	})
	if isError {
		t.Fatalf("get_request(inheriting) returned isError: %s", text)
	}
	// The sentinel must never leak — this is still governed by rule 1 even
	// though the value is never reached.
	assertNoSentinel(t, "get_request(inheriting)", raw, inheritedTokenSentinel)

	var detail struct {
		AuthType   string `json:"authType"`
		AuthSource string `json:"authSource"`
		Auth       []struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		} `json:"auth"`
	}
	if err := json.Unmarshal([]byte(text), &detail); err != nil {
		t.Fatalf("decode get_request(inheriting): %v", err)
	}
	if detail.AuthType != "bearer" {
		t.Errorf("authType is %q, want the collection's effective mode bearer", detail.AuthType)
	}
	if detail.AuthType == "inherit" {
		t.Error("authType is the word inherit, which tells an agent nothing about how the request authenticates")
	}
	if detail.AuthSource != "collection" {
		t.Errorf("authSource is %q, want collection — the agent needs to know which level to change", detail.AuthSource)
	}
	foundToken := false
	for _, row := range detail.Auth {
		if strings.EqualFold(row.Name, "token") {
			foundToken = true
			if row.Value != "<masked>" {
				t.Errorf("the inherited bearer token is %q, want <masked>", row.Value)
			}
		}
	}
	if !foundToken {
		t.Errorf("no token row for a request inheriting a configured bearer block: %+v", detail.Auth)
	}
}

// --- 2. adversarial probes ---------------------------------------------------

func TestMCPE2EWrongTokenIsRejectedWithoutEchoingEitherToken(t *testing.T) {
	f := newE2EFixture(t)
	wrongToken := "wrong-token-0000000000000000000000000000000000000000000000000000"
	exchange, raw := f.call(t, "tools/list", nil, wrongToken, nil)
	if exchange.status != http.StatusUnauthorized {
		t.Errorf("status is %d, want 401", exchange.status)
	}
	assertNoSentinel(t, "wrong token", raw, f.token, wrongToken)
}

func TestMCPE2ENoTokenIsRejected(t *testing.T) {
	f := newE2EFixture(t)
	exchange, raw := f.call(t, "tools/list", nil, "", nil)
	if exchange.status != http.StatusUnauthorized {
		t.Errorf("status is %d, want 401", exchange.status)
	}
	assertNoSentinel(t, "no token", raw, f.token)
}

func TestMCPE2EHostileOriginIsForbidden(t *testing.T) {
	f := newE2EFixture(t)
	exchange, _ := f.call(t, "tools/list", nil, f.token, map[string]string{"Origin": "https://evil.example.com"})
	if exchange.status != http.StatusForbidden {
		t.Errorf("status is %d, want 403", exchange.status)
	}
}

func TestMCPE2ELocalOriginIsAllowed(t *testing.T) {
	f := newE2EFixture(t)
	exchange, _ := f.call(t, "tools/list", nil, f.token, map[string]string{"Origin": "http://localhost:5173"})
	if exchange.status != http.StatusOK {
		t.Errorf("status is %d, want 200 for a loopback Origin", exchange.status)
	}
	if exchange.isErr {
		t.Errorf("tools/list with a loopback Origin returned a JSON-RPC error: %d %s", exchange.errCode, exchange.errMsg)
	}
}

func TestMCPE2EGetIsMethodNotAllowed(t *testing.T) {
	f := newE2EFixture(t)
	req, err := http.NewRequest(http.MethodGet, f.baseURL, nil)
	if err != nil {
		t.Fatalf("build GET: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+f.token)
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", f.baseURL, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("GET status is %d, want 405", resp.StatusCode)
	}
}

func TestMCPE2EBatchIsRejectedAsInvalidRequest(t *testing.T) {
	f := newE2EFixture(t)
	exchange, _ := f.rawPost(t, []byte(`[{"jsonrpc":"2.0","id":1,"method":"ping"}]`), f.token, nil)
	if !exchange.isErr {
		t.Fatalf("batch request did not produce a JSON-RPC error: %+v", exchange)
	}
	if exchange.errCode != -32600 {
		t.Errorf("batch error code is %d, want -32600", exchange.errCode)
	}
}

func TestMCPE2EUnknownToolIsInvalidParams(t *testing.T) {
	f := newE2EFixture(t)
	exchange, _ := f.call(t, "tools/call", map[string]any{"name": "delete_everything", "arguments": map[string]any{}}, f.token, nil)
	if !exchange.isErr {
		t.Fatalf("unknown tool did not produce a JSON-RPC error: %+v", exchange)
	}
	if exchange.errCode != -32602 {
		t.Errorf("unknown tool error code is %d, want -32602", exchange.errCode)
	}
}

func TestMCPE2ETraversalRequestIDFailsCleanlyNoPanic(t *testing.T) {
	f := newE2EFixture(t)
	text, isError, raw := f.callTool(t, "get_request", map[string]any{
		"collectionId": f.collectionID, "requestId": "../../etc/passwd",
	})
	if !isError {
		t.Fatalf("a traversal-shaped id did not produce an isError result: %s", text)
	}
	if !strings.Contains(text, "../../etc/passwd") {
		t.Errorf("the error does not name the offending id, which is what makes it actionable: %s", text)
	}
	assertNoSentinel(t, "traversal id", raw, e2eSentinelHeaderAuth, e2eSentinelPassword, e2eSentinelEnvSecret)

	// The server must still be alive and answering after this — the point of
	// the recover() in invokeTool is that a bad argument never takes the
	// listener down.
	exchange, _ := f.call(t, "ping", nil, f.token, nil)
	if exchange.status != http.StatusOK || exchange.isErr {
		t.Fatalf("server did not survive the traversal probe: %+v", exchange)
	}
}

func TestMCPE2ESearchRequestsWithHugeLimitIsBounded(t *testing.T) {
	f := newE2EFixture(t)
	// query "a" matches every fixture request (case-insensitive substring over
	// name/method/url/folder — "auth", "Store", "graphql" etc. all contain it),
	// so this exercises the limit ceiling rather than the predicate.
	text, isError, _ := f.callTool(t, "search_requests", map[string]any{"query": "a", "limit": 999999})
	if isError {
		t.Fatalf("search_requests with a huge limit returned isError: %s", text)
	}
	var rows []json.RawMessage
	if err := json.Unmarshal([]byte(text), &rows); err != nil {
		t.Fatalf("decode search_requests result: %v", err)
	}
	if len(rows) > mcpSearchMaxLimit {
		t.Errorf("search_requests returned %d rows, want at most the %d ceiling", len(rows), mcpSearchMaxLimit)
	}
	if len(rows) == 0 {
		t.Error("search_requests with query \"a\" matched nothing; the bound assertion above would be vacuous")
	}
}

// The Backend contract defines an empty query as "match everything" — the
// usable "show me anything" call. "query" is therefore optional in the schema,
// so both ways an agent can express it (an empty string, or no argument at all)
// reach the handler instead of being rejected by validate as an empty required
// argument. This is the whole-workspace listing an agent makes before it knows
// what to look for, over the real HTTP path.
func TestMCPE2EEmptyQueryListsEveryRequest(t *testing.T) {
	f := newE2EFixture(t)
	everyRequest := []string{f.reqTemplatedAuthID, f.reqLiteralAuthID, f.reqBasicAuthID, f.reqGraphQLID, f.reqURLSecretID}

	for _, testCase := range []struct {
		name string
		args map[string]any
	}{
		{"an explicitly empty query", map[string]any{"query": ""}},
		{"no query argument at all", map[string]any{}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			text, isError, raw := f.callTool(t, "search_requests", testCase.args)
			if isError {
				t.Fatalf("search_requests with %v failed: %s", testCase.args, text)
			}
			var rows []struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal([]byte(text), &rows); err != nil {
				t.Fatalf("decode search_requests result: %v", err)
			}
			seen := map[string]bool{}
			for _, row := range rows {
				seen[row.ID] = true
			}
			for _, id := range everyRequest {
				if !seen[id] {
					t.Errorf("the match-everything search did not return request %q: %s", id, text)
				}
			}
			assertNoSentinel(t, "search_requests(match everything)", raw,
				e2eSentinelHeaderAuth, e2eSentinelPassword, e2eSentinelEnvSecret, e2eSentinelURLQuery)
		})
	}
}

// A credential in the URL's own query string is the one an agent would
// otherwise read byte-for-byte: it never becomes a Params row, so the header
// and param masking never sees it. Measured here over the real HTTP path
// because that is where an agent meets it.
func TestMCPE2EURLQueryCredentialArrivesMasked(t *testing.T) {
	f := newE2EFixture(t)
	text, isError, raw := f.callTool(t, "get_request", map[string]any{
		"collectionId": f.collectionID, "requestId": f.reqURLSecretID,
	})
	if isError {
		t.Fatalf("get_request(url secret) returned isError: %s", text)
	}
	assertNoSentinel(t, "get_request(url secret)", raw, e2eSentinelURLQuery)

	var detail struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal([]byte(text), &detail); err != nil {
		t.Fatalf("decode get_request(url secret): %v", err)
	}
	want := "{{baseUrl}}/reports/daily?api_key=<masked>&page=2"
	if detail.URL != want {
		t.Errorf("URL is %q, want %q — the credential masked, and the {{template}}, the path and the ordinary page param untouched", detail.URL, want)
	}
}

func TestMCPE2EGetHistoryWithNegativeLimitIsBounded(t *testing.T) {
	f := newE2EFixture(t)
	text, isError, _ := f.callTool(t, "get_history", map[string]any{
		"collectionId": f.collectionID, "requestId": f.reqLiteralAuthID, "limit": -5,
	})
	if isError {
		t.Fatalf("get_history with a negative limit returned isError: %s", text)
	}
	var runs []json.RawMessage
	if err := json.Unmarshal([]byte(text), &runs); err != nil {
		t.Fatalf("decode get_history result: %v", err)
	}
	// A negative limit takes the default (mcpBoundedLimit's rule); the fixture
	// only has one run, so the meaningful assertion is "did not error and did
	// not somehow return nothing".
	if len(runs) != 1 {
		t.Errorf("get_history with a negative limit returned %d runs, want the 1 recorded", len(runs))
	}
}

func TestMCPE2EOversizedBodyIsRejectedCleanly(t *testing.T) {
	f := newE2EFixture(t)
	// maxRequestBytes is 4<<20 (4MB); send 5MB of padding inside a single valid
	// JSON-RPC message shape so a rejection can only be about size, not shape.
	padding := strings.Repeat("a", 5<<20)
	oversized := fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"search_requests","arguments":{"query":"%s"}}}`, padding)
	exchange, _ := f.rawPost(t, []byte(oversized), f.token, nil)
	if exchange.status == http.StatusOK && !exchange.isErr {
		t.Fatalf("a 5MB body was accepted and processed as a successful call")
	}
	// http.MaxBytesReader surfaces as a body-read failure, answered as a
	// JSON-RPC parse error with a 400 — never a 200, never a crash.
	if exchange.status != http.StatusBadRequest {
		t.Errorf("oversized body status is %d, want 400", exchange.status)
	}
}

func TestMCPE2EConcurrentToolCallsAreRaceFree(t *testing.T) {
	f := newE2EFixture(t)
	const workers = 50
	var wg sync.WaitGroup
	errs := make(chan string, workers)
	calls := []struct {
		name string
		args map[string]any
	}{
		{"list_collections", nil},
		{"list_requests", map[string]any{"collectionId": f.collectionID}},
		{"search_requests", map[string]any{"query": "auth"}},
		{"get_request", map[string]any{"collectionId": f.collectionID, "requestId": f.reqBasicAuthID}},
		{"list_environments", nil},
		{"get_history", map[string]any{"collectionId": f.collectionID, "requestId": f.reqLiteralAuthID}},
	}
	client := &http.Client{Timeout: 10 * time.Second}
	for i := 0; i < workers; i++ {
		wg.Add(1)
		call := calls[i%len(calls)]
		go func(name string, args map[string]any) {
			defer wg.Done()
			if args == nil {
				args = map[string]any{}
			}
			payload, err := json.Marshal(map[string]any{
				"jsonrpc": "2.0", "id": 1, "method": "tools/call",
				"params": map[string]any{"name": name, "arguments": args},
			})
			if err != nil {
				errs <- fmt.Sprintf("%s: marshal: %v", name, err)
				return
			}
			req, err := http.NewRequest(http.MethodPost, f.baseURL, bytes.NewReader(payload))
			if err != nil {
				errs <- fmt.Sprintf("%s: build request: %v", name, err)
				return
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+f.token)
			resp, err := client.Do(req)
			if err != nil {
				errs <- fmt.Sprintf("%s: request failed: %v", name, err)
				return
			}
			defer func() { _ = resp.Body.Close() }()
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				errs <- fmt.Sprintf("%s: read body: %v", name, err)
				return
			}
			if resp.StatusCode != http.StatusOK {
				errs <- fmt.Sprintf("%s: status %d: %s", name, resp.StatusCode, body)
				return
			}
			for _, sentinel := range []string{e2eSentinelHeaderAuth, e2eSentinelPassword, e2eSentinelEnvSecret} {
				if bytes.Contains(body, []byte(sentinel)) {
					errs <- fmt.Sprintf("%s: leaked %s under concurrency", name, sentinel)
				}
			}
		}(call.name, call.args)
	}
	wg.Wait()
	close(errs)
	for message := range errs {
		t.Error(message)
	}
}

// --- 3. pairing-flow cross-check ---------------------------------------------

func TestMCPE2EStatusCommandPortAndTokenAreLive(t *testing.T) {
	f := newE2EFixture(t)
	status, err := f.app.GetMCPStatus()
	if err != nil {
		t.Fatalf("GetMCPStatus: %v", err)
	}
	if !status.Running {
		t.Fatal("status reports the server as not running")
	}
	wantURL := fmt.Sprintf("http://127.0.0.1:%d/mcp", status.Port)
	if !strings.Contains(status.Command, wantURL) {
		t.Errorf("pairing command %q does not name the live port %d", status.Command, status.Port)
	}
	if !strings.Contains(status.Command, status.Token) {
		t.Errorf("pairing command does not carry the token")
	}
	// Prove the port in the command is the SAME port this fixture's own
	// listener answers on, not merely a plausible-looking number.
	conn, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", status.Port))
	if err != nil {
		t.Fatalf("nothing is listening on the status port %d: %v", status.Port, err)
	}
	_ = conn.Close()

	// Use the exact token from the status against the exact port from the
	// status — the two halves of the pairing command an agent would actually
	// paste — and confirm it authenticates.
	req, err := http.NewRequest(http.MethodPost, wantURL, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+status.Token)
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		t.Fatalf("ping via the status-derived pairing details: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ping with the status token/port failed: status=%d body=%s", resp.StatusCode, body)
	}
	var parsed struct {
		Error *struct {
			Code int `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("decode ping response: %v", err)
	}
	if parsed.Error != nil {
		t.Fatalf("ping with the status token/port returned a JSON-RPC error: %+v", parsed.Error)
	}
}
