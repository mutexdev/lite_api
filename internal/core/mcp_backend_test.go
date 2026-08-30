package core

// The leak test for the MCP adapter.
//
// docs/mcp-agent-interface.md rule 1 is absolute: no tool, argument or error
// message may carry a resolved secret. This file is how that is measured rather
// than asserted. Three sentinels are planted in the places a credential really
// lives in this app —
//
//   - a SECRET ENVIRONMENT VARIABLE, decrypted into memory exactly as
//     hydrateStateEnvironmentSecretsLocked leaves it at runtime;
//   - a LITERAL CREDENTIAL HEADER, the `Authorization: Bearer ...` somebody
//     pasted in rather than templating;
//   - an AUTH BLOCK PASSWORD, the field that is a credential by construction.
//
// — every Backend method is then called, every result is marshalled to JSON,
// and the marshalled bytes are searched for all three. Marshalling rather than
// field-by-field assertions is the point: a field added to a DTO later, or a
// struct embedded whole, is caught by a byte search and missed by an assertion
// list that nobody remembered to extend.
//
// The negative controls matter as much. A test that passed because the fixture
// was empty, or because the adapter masked everything indiscriminately, would
// be worthless — so the sentinels are asserted to be present in the state
// before the calls, and a TEMPLATED credential header and a NON-SECRET
// environment value are asserted to survive unmasked.

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mutexdev/lite_api/internal/history"
	"github.com/mutexdev/lite_api/internal/mcpserver"
	"github.com/mutexdev/lite_api/internal/responsestore"
	"github.com/mutexdev/lite_api/internal/types"
)

const (
	sentinelSecretValue = "SENTINEL-SECRET-VALUE"
	sentinelHeader      = "SENTINEL-LITERAL"
	sentinelAuth        = "SENTINEL-AUTH"
	// The template that must SURVIVE. A masked template would be the opposite
	// failure: the agent needs to see which variable a header reads so it can
	// name it, and a {{reference}} is not a credential.
	templatedHeaderValue = "Bearer {{tok}}"
	// A non-secret environment value has to reach the agent as itself. This is
	// the whole reason list_environments exists.
	plainEnvironmentValue = "https://api.example.com"
)

type mcpFixture struct {
	app          *App
	backend      *mcpBackend
	collectionID string
	requestID    string
	historyID    string
}

// newMCPFixture plants the sentinels and returns the adapter over them.
func newMCPFixture(t *testing.T) mcpFixture {
	t.Helper()
	app := newAppForTest(t)
	if _, err := app.GetState(); err != nil {
		t.Fatalf("GetState: %v", err)
	}

	app.mu.Lock()
	workspace := &app.state.Workspaces[0]
	collection := &workspace.Collections[0]

	item := types.NewRequestItem("Create widget", "http", 1)
	item.Method = "POST"
	item.URL = "{{baseUrl}}/widgets"
	item.FolderPath = "widgets/admin"
	item.Headers = []KeyValue{
		{Name: "Authorization", Value: "Bearer " + sentinelHeader, Enabled: true},
		{Name: "X-Auth-Token", Value: templatedHeaderValue, Enabled: true},
		{Name: "Content-Type", Value: "application/json", Enabled: true},
	}
	item.Params = []KeyValue{
		{Name: "api_key", Value: sentinelHeader, Enabled: true},
		{Name: "page", Value: "1", Enabled: true},
	}
	item.PathParams = []KeyValue{{Name: "tenant", Value: "acme", Enabled: true}}
	item.Auth = AuthConfig{Mode: "basic", Username: "service-account", Password: sentinelAuth}
	item.Vars.Req = []Variable{
		{ID: "var-plain", Name: "widgetName", Value: "gizmo", Enabled: true},
		{ID: "var-secret", Name: "widgetSecret", Value: sentinelSecretValue, Enabled: true, Secret: true},
	}
	item.Body.Mode = "json"
	item.Body.JSON = "{\n  \"name\": \"{{widgetName}}\"\n}"
	item.PreScript = "bru.setVar('x', 1)"
	item.Tests = "expect status equals 201"
	collection.Items = append(collection.Items, item)

	// A secret in a collection environment and one in a workspace-global
	// environment: the two scopes ListEnvironments walks.
	collection.Environments = append(collection.Environments, Environment{
		ID:   "env-collection",
		Name: "Staging",
		Variables: []Variable{
			{ID: "env-var-plain", Name: "baseUrl", Value: plainEnvironmentValue, Enabled: true},
			{ID: "env-var-secret", Name: "tok", Value: sentinelSecretValue, Enabled: true, Secret: true},
		},
	})
	workspace.GlobalEnvironments = append(workspace.GlobalEnvironments, Environment{
		ID:   "env-global",
		Name: "Shared",
		Variables: []Variable{
			{ID: "global-var-secret", Name: "globalToken", Value: sentinelSecretValue, Enabled: true, Secret: true},
		},
	})
	workspace.ActiveGlobalEnvironmentID = "env-global"

	collectionID := collection.ID
	requestID := item.ID
	app.mu.Unlock()

	fixture := mcpFixture{
		app:          app,
		backend:      &mcpBackend{app: app},
		collectionID: collectionID,
		requestID:    requestID,
		historyID:    seedMCPHistory(t, app, collectionID, requestID),
	}
	fixture.assertSentinelsArePresent(t)
	return fixture
}

// seedMCPHistory records one run of the fixture request, with a body long
// enough to prove the truncation cap and headers that internal/history has
// already redacted on the way in.
func seedMCPHistory(t *testing.T, app *App, collectionID, requestID string) string {
	t.Helper()
	store, err := app.responseStore()
	if err != nil {
		t.Fatalf("responseStore: %v", err)
	}
	// Deliberately NOT a sentinel: response bodies pass through in full by
	// design (docs rule 3) — they are the data the agent is there for.
	body := strings.Repeat("a", mcpHistoryBodyLimit+1)
	handle, err := store.Put([]byte(body))
	if err != nil {
		t.Fatalf("store response body: %v", err)
	}

	requestHeaders, _ := history.RedactHeaders([]types.KeyValue{
		{Name: "Authorization", Value: "Bearer " + sentinelHeader, Enabled: true},
	})
	responseHeaders, _ := history.RedactHeaders([]types.KeyValue{
		{Name: "Set-Cookie", Value: "session=" + sentinelHeader, Enabled: true},
		{Name: "Content-Type", Value: "application/json", Enabled: true},
	})

	entry := history.HistoryEntry{
		ID:              "history-mcp-1",
		At:              time.Now(),
		CollectionID:    collectionID,
		ItemID:          requestID,
		Name:            "Create widget",
		Method:          "POST",
		URL:             "https://api.example.com/widgets",
		Status:          201,
		DurationMs:      42,
		RequestHeaders:  requestHeaders,
		ResponseHeaders: responseHeaders,
		BodyHandle:      string(handle),
	}
	if err := app.history().Append(entry); err != nil {
		t.Fatalf("append history: %v", err)
	}
	// Proves the handle really resolves; a body the store cannot return would
	// make the truncation assertion below vacuous.
	if _, err := store.Get(responsestore.Handle(handle)); err != nil {
		t.Fatalf("stored body is not readable: %v", err)
	}
	return entry.ID
}

// assertSentinelsArePresent is the negative control. Every assertion in this
// file is of the form "the sentinel is absent from the output"; if the fixture
// never held it, all of them pass while measuring nothing.
func (f mcpFixture) assertSentinelsArePresent(t *testing.T) {
	t.Helper()
	f.app.mu.Lock()
	defer f.app.mu.Unlock()
	state, err := json.Marshal(f.app.state)
	if err != nil {
		t.Fatalf("marshal state: %v", err)
	}
	for _, sentinel := range []string{sentinelSecretValue, sentinelHeader, sentinelAuth} {
		if !strings.Contains(string(state), sentinel) {
			t.Fatalf("%s is not in the fixture state; the leak assertions would pass by measuring nothing", sentinel)
		}
	}
}

// marshalled runs one Backend call and returns its JSON.
func marshalledMCPResult(t *testing.T, name string, value any, err error) string {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal %s: %v", name, err)
	}
	return string(encoded)
}

func TestMCPBackendLeaksNoSecretFromAnyMethod(t *testing.T) {
	fixture := newMCPFixture(t)
	backend := fixture.backend

	collections, err := backend.ListCollections()
	results := map[string]string{
		"ListCollections": marshalledMCPResult(t, "ListCollections", collections, err),
	}

	requests, err := backend.ListRequests(fixture.collectionID)
	results["ListRequests"] = marshalledMCPResult(t, "ListRequests", requests, err)

	found, err := backend.SearchRequests("widget", 0)
	results["SearchRequests"] = marshalledMCPResult(t, "SearchRequests", found, err)

	detail, err := backend.GetRequest(fixture.collectionID, fixture.requestID)
	results["GetRequest"] = marshalledMCPResult(t, "GetRequest", detail, err)

	environments, err := backend.ListEnvironments()
	results["ListEnvironments"] = marshalledMCPResult(t, "ListEnvironments", environments, err)

	runs, err := backend.GetHistory(fixture.collectionID, fixture.requestID, 0)
	results["GetHistory"] = marshalledMCPResult(t, "GetHistory", runs, err)

	for method, encoded := range results {
		for _, sentinel := range []string{sentinelSecretValue, sentinelHeader, sentinelAuth} {
			if strings.Contains(encoded, sentinel) {
				t.Errorf("%s leaked %s:\n%s", method, sentinel, encoded)
			}
		}
	}

	// Every method must have produced something. An adapter that returned empty
	// slices would pass every assertion above.
	if len(collections) == 0 {
		t.Error("ListCollections returned nothing")
	}
	if len(requests) == 0 {
		t.Error("ListRequests returned nothing")
	}
	if len(found) == 0 {
		t.Error("SearchRequests returned nothing")
	}
	if len(environments) == 0 {
		t.Error("ListEnvironments returned nothing")
	}
	if len(runs) == 0 {
		t.Error("GetHistory returned nothing")
	}
	if detail.ID != fixture.requestID {
		t.Errorf("GetRequest returned request %q, want %q", detail.ID, fixture.requestID)
	}
}

// The masking must be discriminating. Masking everything would pass the leak
// test and make the interface useless.
func TestMCPBackendKeepsTemplatesAndPlainValues(t *testing.T) {
	fixture := newMCPFixture(t)

	detail, err := fixture.backend.GetRequest(fixture.collectionID, fixture.requestID)
	if err != nil {
		t.Fatalf("GetRequest: %v", err)
	}

	authorization := mcpRowValue(detail.Headers, "Authorization")
	if authorization != mcpserver.MaskedValue {
		t.Errorf("the literal Authorization header is %q, want %q", authorization, mcpserver.MaskedValue)
	}
	templated := mcpRowValue(detail.Headers, "X-Auth-Token")
	if templated != templatedHeaderValue {
		t.Errorf("the templated credential header is %q, want it to survive as %q — a {{reference}} is not a secret, and masking it hides which variable the request reads", templated, templatedHeaderValue)
	}
	if contentType := mcpRowValue(detail.Headers, "Content-Type"); contentType != "application/json" {
		t.Errorf("an ordinary header was mangled to %q", contentType)
	}
	if page := mcpRowValue(detail.Params, "page"); page != "1" {
		t.Errorf("an ordinary query param was mangled to %q", page)
	}
	if apiKey := mcpRowValue(detail.Params, "api_key"); apiKey != mcpserver.MaskedValue {
		t.Errorf("a credential-shaped param is %q, want it masked", apiKey)
	}

	// Auth: the mode and the addressing field survive, the credential does not.
	if detail.AuthType != "basic" {
		t.Errorf("AuthType is %q, want basic — the agent needs the mode to reproduce the call", detail.AuthType)
	}
	if username := mcpRowValue(detail.Auth, "username"); username != "service-account" {
		t.Errorf("the username is %q; it addresses the account rather than authenticating, and should survive", username)
	}
	if password := mcpRowValue(detail.Auth, "password"); password != mcpserver.MaskedValue {
		t.Errorf("the auth password is %q, want it masked", password)
	}

	// Request vars: the plain one survives, the secret one is name-only.
	if plain := mcpRowValue(detail.Vars, "widgetName"); plain != "gizmo" {
		t.Errorf("a non-secret request var is %q, want gizmo", plain)
	}
	if secret := mcpRowValue(detail.Vars, "widgetSecret"); secret != "" {
		t.Errorf("a secret request var carried the value %q; it must be name-only", secret)
	}

	// URL and body keep their templates: interpolating either here would be the
	// leak this whole adapter exists to prevent.
	if !strings.Contains(detail.URL, "{{baseUrl}}") {
		t.Errorf("the URL %q lost its template", detail.URL)
	}
	if !strings.Contains(detail.Body, "{{widgetName}}") {
		t.Errorf("the body %q lost its template", detail.Body)
	}
	if detail.FolderPath != "widgets/admin" {
		t.Errorf("FolderPath is %q, want widgets/admin", detail.FolderPath)
	}
	if detail.PreScript == "" || detail.Tests == "" {
		t.Error("scripts were dropped; they travel verbatim")
	}
}

func TestMCPBackendEnvironmentsExposeNamesNotSecretValues(t *testing.T) {
	fixture := newMCPFixture(t)

	environments, err := fixture.backend.ListEnvironments()
	if err != nil {
		t.Fatalf("ListEnvironments: %v", err)
	}

	var staging, shared *mcpserver.EnvironmentSummary
	for index := range environments {
		switch environments[index].Name {
		case "Staging":
			staging = &environments[index]
		case "Shared":
			shared = &environments[index]
		}
	}
	if staging == nil || shared == nil {
		t.Fatalf("expected both environments, got %+v", environments)
	}

	if staging.Scope != "collection" {
		t.Errorf("the collection environment has scope %q, want collection", staging.Scope)
	}
	if staging.CollectionID != fixture.collectionID {
		t.Errorf("the collection environment reports collection %q, want %q", staging.CollectionID, fixture.collectionID)
	}
	if shared.Scope != "global" {
		t.Errorf("the workspace environment has scope %q, want global", shared.Scope)
	}
	if !shared.Active {
		t.Error("the active global environment is not reported as active")
	}

	for _, variable := range staging.Variables {
		switch variable.Name {
		case "baseUrl":
			if variable.Secret {
				t.Error("baseUrl was reported as secret")
			}
			if variable.Value != plainEnvironmentValue {
				t.Errorf("a non-secret environment value is %q, want %q", variable.Value, plainEnvironmentValue)
			}
		case "tok":
			if !variable.Secret {
				t.Error("a secret variable lost its secret flag; the agent would treat it as readable")
			}
			if variable.Value != "" {
				t.Errorf("a secret variable carried the value %q", variable.Value)
			}
		default:
			t.Errorf("unexpected variable %q", variable.Name)
		}
	}
}

// History bodies are bounded, and the bound is reported rather than silently
// applied: an agent parsing a truncated JSON body needs to know that is what
// happened.
func TestMCPBackendHistoryIsBoundedAndFiltered(t *testing.T) {
	fixture := newMCPFixture(t)

	runs, err := fixture.backend.GetHistory(fixture.collectionID, fixture.requestID, 0)
	if err != nil {
		t.Fatalf("GetHistory: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("got %d runs, want the 1 recorded for this request", len(runs))
	}
	run := runs[0]
	if run.ID != fixture.historyID {
		t.Errorf("run id is %q, want %q", run.ID, fixture.historyID)
	}
	if !run.Truncated {
		t.Error("an over-long body was not reported as truncated")
	}
	if len(run.Body) != mcpHistoryBodyLimit {
		t.Errorf("body is %d bytes, want it cut to %d", len(run.Body), mcpHistoryBodyLimit)
	}
	if run.Status != 201 || run.DurationMs != 42 {
		t.Errorf("run status/duration are %d/%d, want 201/42", run.Status, run.DurationMs)
	}
	if setCookie := mcpRowValue(run.Headers, "Set-Cookie"); setCookie != history.RedactedValue {
		t.Errorf("Set-Cookie is %q, want history's own %q — these rows arrive already redacted", setCookie, history.RedactedValue)
	}

	// A different request in the same collection has no runs. The store filters
	// by collection, so the item filter is this adapter's alone and would fail
	// silently by returning another request's history.
	other, err := fixture.backend.GetHistory(fixture.collectionID, "request-that-never-ran", 0)
	if err != nil {
		t.Fatalf("GetHistory for another request: %v", err)
	}
	if len(other) != 0 {
		t.Errorf("got %d runs for a request that never ran", len(other))
	}
}

// The limits are the tool contract: 0 means the default, anything above the cap
// is the cap. A caller asking for 10000 rows must not get them.
func TestMCPBackendLimitsAreBounded(t *testing.T) {
	cases := []struct {
		name     string
		limit    int
		fallback int
		ceiling  int
		want     int
	}{
		{"zero takes the default", 0, mcpSearchDefaultLimit, mcpSearchMaxLimit, mcpSearchDefaultLimit},
		{"negative takes the default", -5, mcpSearchDefaultLimit, mcpSearchMaxLimit, mcpSearchDefaultLimit},
		{"a sane limit is kept", 7, mcpSearchDefaultLimit, mcpSearchMaxLimit, 7},
		{"above the cap is capped", 10000, mcpSearchDefaultLimit, mcpSearchMaxLimit, mcpSearchMaxLimit},
		{"history has its own cap", 10000, mcpHistoryDefaultLimit, mcpHistoryMaxLimit, mcpHistoryMaxLimit},
	}
	for _, testCase := range cases {
		if got := mcpBoundedLimit(testCase.limit, testCase.fallback, testCase.ceiling); got != testCase.want {
			t.Errorf("%s: limit %d became %d, want %d", testCase.name, testCase.limit, got, testCase.want)
		}
	}

	fixture := newMCPFixture(t)
	// An empty query matches everything, so this measures the limit rather than
	// the predicate.
	found, err := fixture.backend.SearchRequests("", 1)
	if err != nil {
		t.Fatalf("SearchRequests: %v", err)
	}
	if len(found) != 1 {
		t.Errorf("a limit of 1 returned %d results", len(found))
	}
}

// Search matches the four fields an agent can plausibly know, and matches them
// case-insensitively.
func TestMCPBackendSearchMatchesNameMethodURLAndFolder(t *testing.T) {
	fixture := newMCPFixture(t)

	for _, query := range []string{"CREATE WIDGET", "post", "/widgets", "widgets/admin"} {
		found, err := fixture.backend.SearchRequests(query, 0)
		if err != nil {
			t.Fatalf("SearchRequests(%q): %v", query, err)
		}
		matched := false
		for _, row := range found {
			if row.ID == fixture.requestID {
				matched = true
			}
		}
		if !matched {
			t.Errorf("query %q did not find the fixture request", query)
		}
	}

	found, err := fixture.backend.SearchRequests("no-such-request-anywhere", 0)
	if err != nil {
		t.Fatalf("SearchRequests: %v", err)
	}
	if len(found) != 0 {
		t.Errorf("a query matching nothing returned %d rows", len(found))
	}
}

// Unknown ids name the field and the fix rather than returning an empty list,
// which an agent would read as "this collection has no requests".
func TestMCPBackendUnknownIDsAreActionableErrors(t *testing.T) {
	fixture := newMCPFixture(t)

	if _, err := fixture.backend.ListRequests("no-such-collection"); err == nil {
		t.Error("an unknown collection id returned no error")
	} else if !strings.Contains(err.Error(), "no-such-collection") {
		t.Errorf("the error does not name the id: %v", err)
	}
	if _, err := fixture.backend.GetRequest(fixture.collectionID, "no-such-request"); err == nil {
		t.Error("an unknown request id returned no error")
	}
	if _, err := fixture.backend.ListRequests(""); err == nil {
		t.Error("an empty collection id returned no error")
	}
}

// mcpRowValue finds one row by name, case-insensitively.
func mcpRowValue(rows []mcpserver.KeyValue, name string) string {
	for _, row := range rows {
		if strings.EqualFold(row.Name, name) {
			return row.Value
		}
	}
	return ""
}
