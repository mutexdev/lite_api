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
	"github.com/mutexdev/lite_api/internal/scripting"
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
	// Recorded WITH its Phase 6 §7 projection, through the production builder:
	// get_history serves only the projection, so a fixture that appended a bare
	// entry would be measuring the placeholder rather than the redaction.
	seedHistoryProjection(t, app, entry, body)
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

// --- URL query literals ------------------------------------------------------

// The fourth place a credential hides, after headers, params and auth blocks:
// the URL's own query string. "?api_key=sk_live_..." pasted from a working curl
// never becomes a structured Params row, so RedactRows never sees it — masking
// has to happen where the URL itself is built. That is mcpRequestSummary, which
// every tool that reports a URL goes through, so this asserts all three.
func TestMCPBackendMasksURLQueryLiteralsInEveryToolThatReportsAURL(t *testing.T) {
	fixture := newMCPFixture(t)
	const (
		literalURL   = "https://api.example.test/d?api_key=sk_live_x&page=2"
		templatedURL = "https://api.example.test/d?api_key={{key}}&page=2"
	)
	literalID := appendMCPFixtureItem(t, fixture, "Curl paste", func(item *types.RequestItem) {
		item.Method = "GET"
		item.URL = literalURL
	})
	templatedID := appendMCPFixtureItem(t, fixture, "Curl paste templated", func(item *types.RequestItem) {
		item.Method = "GET"
		item.URL = templatedURL
	})

	const wantLiteral = "https://api.example.test/d?api_key=" + mcpserver.MaskedValue + "&page=2"

	// list_requests and search_requests both build rows through
	// mcpRequestSummary; get_request embeds one. All three are checked because
	// the masking would be just as absent from any of them if it were applied
	// at a call site rather than in the shared builder.
	listed, err := fixture.backend.ListRequests(fixture.collectionID)
	if err != nil {
		t.Fatalf("ListRequests: %v", err)
	}
	found, err := fixture.backend.SearchRequests("api.example.test", 0)
	if err != nil {
		t.Fatalf("SearchRequests: %v", err)
	}
	for _, source := range []struct {
		name string
		rows []mcpserver.RequestSummary
	}{{"ListRequests", listed}, {"SearchRequests", found}} {
		urls := map[string]string{}
		for _, row := range source.rows {
			urls[row.ID] = row.URL
		}
		if got := urls[literalID]; got != wantLiteral {
			t.Errorf("%s reported %q, want the credential-shaped query value masked to %q", source.name, got, wantLiteral)
		}
		if got := urls[templatedID]; got != templatedURL {
			t.Errorf("%s reported %q, want the templated URL byte-for-byte — a {{reference}} is not a secret", source.name, got)
		}
	}

	detail, err := fixture.backend.GetRequest(fixture.collectionID, literalID)
	if err != nil {
		t.Fatalf("GetRequest: %v", err)
	}
	if detail.URL != wantLiteral {
		t.Errorf("GetRequest reported %q, want %q", detail.URL, wantLiteral)
	}
	// The negative control for this one is inside the URL rather than beside it:
	// masking the whole string would also pass a "the secret is absent" check.
	if !strings.Contains(detail.URL, "page=2") {
		t.Errorf("the non-credential query parameter was lost: %q", detail.URL)
	}
	if strings.Contains(detail.URL, "sk_live_x") {
		t.Errorf("the literal query credential survived: %q", detail.URL)
	}

	templatedDetail, err := fixture.backend.GetRequest(fixture.collectionID, templatedID)
	if err != nil {
		t.Fatalf("GetRequest(templated): %v", err)
	}
	if templatedDetail.URL != templatedURL {
		t.Errorf("GetRequest rewrote a templated URL to %q, want %q", templatedDetail.URL, templatedURL)
	}
}

// --- effective auth ----------------------------------------------------------

// get_request reports the auth a RUN would use, not the word stored on the
// item. Each case asserts the reported type/source/rows AND that the resolution
// agrees with scripting.EffectiveRequest — the function the send path itself
// calls. The second assertion is the one that keeps this honest: if the send
// path's inheritance rules change, this test fails rather than the adapter
// quietly reporting auth that no longer matches what LiteAPI would send.
func TestMCPBackendGetRequestReportsEffectiveInheritedAuth(t *testing.T) {
	const inheritedToken = "SENTINEL-INHERITED-TOKEN"
	const folderPassword = "SENTINEL-FOLDER-PASSWORD"

	cases := []struct {
		name string
		// setUp configures the collection and returns the item to install.
		setUp      func(collection *types.Collection, item *types.RequestItem)
		wantType   string
		wantSource string
		wantRows   map[string]string
	}{
		{
			name: "an inheriting request reports the collection's auth",
			setUp: func(collection *types.Collection, item *types.RequestItem) {
				collection.Auth = AuthConfig{Mode: "bearer", Token: inheritedToken}
				item.Auth = AuthConfig{Mode: "inherit"}
			},
			wantType:   "bearer",
			wantSource: mcpAuthSourceCollection,
			wantRows:   map[string]string{"token": mcpserver.MaskedValue},
		},
		{
			name: "an empty mode inherits exactly as \"inherit\" does",
			setUp: func(collection *types.Collection, item *types.RequestItem) {
				collection.Auth = AuthConfig{Mode: "bearer", Token: inheritedToken}
				item.Auth = AuthConfig{}
			},
			wantType:   "bearer",
			wantSource: mcpAuthSourceCollection,
			wantRows:   map[string]string{"token": mcpserver.MaskedValue},
		},
		{
			name: "a folder overrides the collection",
			setUp: func(collection *types.Collection, item *types.RequestItem) {
				collection.Auth = AuthConfig{Mode: "bearer", Token: inheritedToken}
				collection.Folders = []types.FolderConfig{
					{Path: "billing", Name: "billing", Auth: AuthConfig{Mode: "basic", Username: "folder-user", Password: folderPassword}},
				}
				item.FolderPath = "billing"
				item.Auth = AuthConfig{Mode: "inherit"}
			},
			wantType:   "basic",
			wantSource: mcpAuthSourceFolder,
			// The username addresses the account and survives; the password does not.
			wantRows: map[string]string{"username": "folder-user", "password": mcpserver.MaskedValue},
		},
		{
			name: "the innermost folder wins over its parent",
			setUp: func(collection *types.Collection, item *types.RequestItem) {
				collection.Auth = AuthConfig{Mode: "bearer", Token: inheritedToken}
				collection.Folders = []types.FolderConfig{
					{Path: "billing", Name: "billing", Auth: AuthConfig{Mode: "bearer", Token: inheritedToken}},
					{Path: "billing/admin", Name: "admin", Auth: AuthConfig{Mode: "basic", Username: "inner-user", Password: folderPassword}},
				}
				item.FolderPath = "billing/admin"
				item.Auth = AuthConfig{Mode: "inherit"}
			},
			wantType:   "basic",
			wantSource: mcpAuthSourceFolder,
			wantRows:   map[string]string{"username": "inner-user", "password": mcpserver.MaskedValue},
		},
		{
			name: "an explicit request mode wins over everything",
			setUp: func(collection *types.Collection, item *types.RequestItem) {
				collection.Auth = AuthConfig{Mode: "bearer", Token: inheritedToken}
				collection.Folders = []types.FolderConfig{
					{Path: "billing", Name: "billing", Auth: AuthConfig{Mode: "basic", Username: "folder-user", Password: folderPassword}},
				}
				item.FolderPath = "billing"
				item.Auth = AuthConfig{Mode: "apikey", APIKey: "X-Api-Key", APIValue: sentinelAuth, APILocation: "header"}
			},
			wantType:   "apikey",
			wantSource: mcpAuthSourceRequest,
			// "key" is the header NAME (addressing); "value" is the credential.
			wantRows: map[string]string{"key": "X-Api-Key", "value": mcpserver.MaskedValue, "addTo": "header"},
		},
		{
			name: "nothing anywhere configures auth",
			setUp: func(collection *types.Collection, item *types.RequestItem) {
				collection.Auth = AuthConfig{}
				item.Auth = AuthConfig{Mode: "inherit"}
			},
			wantType:   "",
			wantSource: "",
			wantRows:   map[string]string{},
		},
		{
			// The send path tests folder.Auth.Mode != "", so a folder that is
			// itself set to "inherit" SHADOWS the collection and ends up applying
			// no auth (applyAuth has no "inherit" case). Reported as configuring
			// nothing, which is what a run does — and never as the word "inherit".
			name: "a folder that itself inherits shadows the collection",
			setUp: func(collection *types.Collection, item *types.RequestItem) {
				collection.Auth = AuthConfig{Mode: "bearer", Token: inheritedToken}
				collection.Folders = []types.FolderConfig{
					{Path: "billing", Name: "billing", Auth: AuthConfig{Mode: "inherit"}},
				}
				item.FolderPath = "billing"
				item.Auth = AuthConfig{Mode: "inherit"}
			},
			wantType:   "",
			wantSource: "",
			wantRows:   map[string]string{},
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			fixture := newMCPFixture(t)
			item := types.NewRequestItem("Inheriting request", "http", 99)
			item.Method = "GET"
			item.URL = "{{baseUrl}}/inherited"

			fixture.app.mu.Lock()
			collection := &fixture.app.state.Workspaces[0].Collections[0]
			testCase.setUp(collection, &item)
			collection.Items = append(collection.Items, item)
			// Snapshot for the cross-check against the send path's own resolver.
			sendPathView := types.Collection{
				Path:    collection.Path,
				Auth:    collection.Auth,
				Folders: collection.Folders,
			}
			fixture.app.mu.Unlock()

			detail, err := fixture.backend.GetRequest(fixture.collectionID, item.ID)
			if err != nil {
				t.Fatalf("GetRequest: %v", err)
			}

			if detail.AuthType != testCase.wantType {
				t.Errorf("authType is %q, want %q", detail.AuthType, testCase.wantType)
			}
			if strings.EqualFold(detail.AuthType, "inherit") {
				t.Errorf("authType is %q; the effective mode is never the word inherit", detail.AuthType)
			}
			if detail.AuthSource != testCase.wantSource {
				t.Errorf("authSource is %q, want %q", detail.AuthSource, testCase.wantSource)
			}
			if len(detail.Auth) != len(testCase.wantRows) {
				t.Errorf("got %d auth rows, want %d: %+v", len(detail.Auth), len(testCase.wantRows), detail.Auth)
			}
			for name, want := range testCase.wantRows {
				if got := mcpRowValue(detail.Auth, name); got != want {
					t.Errorf("auth row %q is %q, want %q", name, got, want)
				}
			}

			// No sentinel may appear anywhere in the marshalled detail. Resolving
			// inheritance means reaching credentials the adapter never touched
			// before, which is exactly when a leak would be introduced.
			encoded := marshalledMCPResult(t, "GetRequest", detail, nil)
			for _, sentinel := range []string{inheritedToken, folderPassword, sentinelAuth} {
				if strings.Contains(encoded, sentinel) {
					t.Errorf("the resolved auth leaked %s:\n%s", sentinel, encoded)
				}
			}

			// The cross-check: the adapter's resolution must be the send path's.
			// One normalization is needed and only one: where nothing applies,
			// EffectiveRequest leaves the literal "inherit"/"" on the request
			// (applyAuth has no case for either, so nothing is sent) while the
			// adapter reports an empty config. Everything else must match field
			// for field.
			normalize := func(auth types.AuthConfig) types.AuthConfig {
				if auth.Mode == "" || strings.EqualFold(auth.Mode, "inherit") {
					return types.AuthConfig{}
				}
				return auth
			}
			wantEffective := normalize(scripting.EffectiveRequest(sendPathView, item).Auth)
			gotEffective, _ := mcpEffectiveAuth(sendPathView, item)
			if gotEffective.Mode != wantEffective.Mode {
				t.Errorf("resolved mode %q, but scripting.EffectiveRequest resolves %q — get_request would describe auth that a run does not use", gotEffective.Mode, wantEffective.Mode)
			}
			if gotEffective.Token != wantEffective.Token || gotEffective.Username != wantEffective.Username || gotEffective.Password != wantEffective.Password {
				t.Errorf("resolved credentials differ from the send path's: got %+v, want %+v", gotEffective, wantEffective)
			}
		})
	}
}

// appendMCPFixtureItem adds one request to the fixture collection and returns
// its id.
func appendMCPFixtureItem(t *testing.T, fixture mcpFixture, name string, mutate func(item *types.RequestItem)) string {
	t.Helper()
	item := types.NewRequestItem(name, "http", 50)
	mutate(&item)
	fixture.app.mu.Lock()
	defer fixture.app.mu.Unlock()
	collection := &fixture.app.state.Workspaces[0].Collections[0]
	collection.Items = append(collection.Items, item)
	return item.ID
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
