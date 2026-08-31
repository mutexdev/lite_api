package core

// The tests for inspect_request.
//
// They exist to answer one question the external review put sharply: can an
// agent handed this server author a correct call unaided? get_request could not
// tell it what a run would actually send, because a request that carries no
// headers, no auth and no variables of its own can still send all three. So the
// assertions here are about ATTRIBUTION and COMPLETENESS as much as redaction —
// that the inherited pieces are present, that each one names the level that
// contributed it, and that a name the chosen environment does not define is
// reported as unresolved rather than discovered from a failed run.
//
// The leak discipline of mcp_backend_test.go still applies and is not weakened:
// sentinels are planted at the FOLDER and COLLECTION levels too, precisely
// because those are the levels this tool newly reports, and the marshalled
// output is searched for them.

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mutexdev/lite_api/internal/mcpserver"
	"github.com/mutexdev/lite_api/internal/types"
)

const (
	// Planted at the two levels inspect_request newly reaches. The existing
	// request-level sentinels live in mcp_backend_test.go.
	sentinelFolderHeader     = "SENTINEL-FOLDER-HEADER"
	sentinelCollectionHeader = "SENTINEL-COLLECTION-HEADER"
	sentinelEnvSecret        = "SENTINEL-ENV-SECRET"
	sentinelFolderSecretVar  = "SENTINEL-FOLDER-SECRET-VAR"
)

type mcpInspectFixture struct {
	app           *App
	backend       *mcpBackend
	collectionID  string
	requestID     string
	environmentID string
}

// newMCPInspectFixture builds a request that inherits from BOTH a folder and
// the collection, which is the only shape that can distinguish "reports
// inheritance" from "reports the collection and calls it inheritance".
//
// The folder chain is widgets -> widgets/admin, and each level contributes
// something that has to be attributed correctly:
//
//	collection    Accept, X-Collection-Token (a literal credential), baseUrl,
//	              region, legacyUrl, apiRoot, a pre-request script, bearer auth
//	widgets       X-Outer, region (shadowed below)
//	widgets/admin X-Tenant, X-Folder-Token (a literal credential), region
//	              (wins), a folder secret var, a pre-request script
//	environment   baseUrl (wins over the collection's), apiToken (secret)
//	request       Accept (shadows the collection's), Content-Type, tenant
func newMCPInspectFixture(t *testing.T) mcpInspectFixture {
	t.Helper()
	app := newAppForTest(t)
	if _, err := app.GetState(); err != nil {
		t.Fatalf("GetState: %v", err)
	}

	app.mu.Lock()
	workspace := &app.state.Workspaces[0]
	collection := &workspace.Collections[0]

	collection.Headers = []KeyValue{
		{Name: "Accept", Value: "application/json", Enabled: true},
		{Name: "X-Collection-Token", Value: sentinelCollectionHeader, Enabled: true},
		{Name: "X-Disabled", Value: "never-sent", Enabled: false},
	}
	collection.Variables = []Variable{
		{ID: "col-base", Name: "baseUrl", Value: "https://collection.example.com", Enabled: true},
		{ID: "col-region", Name: "region", Value: "collection-region", Enabled: true},
		// A variable whose own value references a name nothing defines. The
		// request never mentions legacyHost; only the transitive walk finds it.
		{ID: "col-legacy", Name: "legacyUrl", Value: "{{legacyHost}}/old", Enabled: true},
	}
	collection.Auth = AuthConfig{Mode: "bearer", Token: "{{apiToken}}"}
	collection.PreScript = "// collection pre"
	collection.PostScript = "// collection post"
	collection.Folders = []types.FolderConfig{
		{
			Path:      "widgets",
			Name:      "widgets",
			Headers:   []KeyValue{{Name: "X-Outer", Value: "outer", Enabled: true}},
			Variables: []Variable{{ID: "outer-region", Name: "region", Value: "outer-region", Enabled: true}},
		},
		{
			Path: "widgets/admin",
			Name: "admin",
			Headers: []KeyValue{
				{Name: "X-Tenant", Value: "{{tenant}}", Enabled: true},
				{Name: "X-Folder-Token", Value: sentinelFolderHeader, Enabled: true},
			},
			Variables: []Variable{
				{ID: "inner-region", Name: "region", Value: "inner-region", Enabled: true},
				{ID: "inner-secret", Name: "folderSecret", Value: sentinelFolderSecretVar, Enabled: true, Secret: true},
			},
			PreScript: "// admin folder pre",
		},
	}
	collection.Environments = append(collection.Environments, Environment{
		ID:   "env-inspect",
		Name: "Staging",
		Variables: []Variable{
			{ID: "env-base", Name: "baseUrl", Value: "https://staging.example.com", Enabled: true},
			{ID: "env-token", Name: "apiToken", Value: sentinelEnvSecret, Enabled: true, Secret: true},
		},
	})
	workspace.GlobalEnvironments = append(workspace.GlobalEnvironments, Environment{
		ID:        "env-inspect-global",
		Name:      "Shared",
		Variables: []Variable{{ID: "global-only", Name: "globalOnly", Value: "shared-value", Enabled: true}},
	})
	workspace.ActiveGlobalEnvironmentID = "env-inspect-global"

	item := types.NewRequestItem("Query store", "graphql", 1)
	item.Method = "POST"
	item.FolderPath = "widgets/admin"
	// missingId is referenced and defined nowhere; region and baseUrl are both
	// inherited, from different levels.
	item.URL = "{{baseUrl}}/widgets/{{missingId}}?region={{region}}"
	item.Headers = []KeyValue{
		{Name: "Accept", Value: "application/vnd.custom", Enabled: true},
		{Name: "Content-Type", Value: "application/json", Enabled: true},
		{Name: "X-Trace", Value: "{{$timestamp}}", Enabled: true},
		{Name: "X-Approver", Value: "{{?approver}}", Enabled: true},
		{Name: "X-Machine", Value: "{{process.env.LITEAPI_TEST_HOST}}", Enabled: true},
	}
	item.Params = []KeyValue{{Name: "legacy", Value: "{{legacyUrl}}", Enabled: true}}
	// Auth left inheriting, so the collection's bearer applies.
	item.Auth = AuthConfig{Mode: "inherit"}
	item.Vars.Req = []Variable{{ID: "req-tenant", Name: "tenant", Value: "acme", Enabled: true}}
	item.Body.Mode = "graphql"
	item.Body.GraphQLQuery = "query Store($code: String!) { store(code: $code) { id } }"
	item.Body.GraphQLVariables = "{\"code\": \"{{storeCode}}\"}"
	item.Settings.VerifyTLS = true
	item.Settings.FollowRedirects = true
	item.Settings.MaxRedirects = 5
	collection.Items = append(collection.Items, item)

	fixture := mcpInspectFixture{
		app:           app,
		backend:       &mcpBackend{app: app},
		collectionID:  collection.ID,
		requestID:     item.ID,
		environmentID: "env-inspect",
	}
	app.mu.Unlock()

	// The negative control, exactly as mcp_backend_test.go's: every leak
	// assertion below is "the sentinel is absent", and they all pass for free
	// if the fixture never held it.
	app.mu.Lock()
	state, err := json.Marshal(app.state)
	app.mu.Unlock()
	if err != nil {
		t.Fatalf("marshal state: %v", err)
	}
	for _, sentinel := range mcpInspectSentinels() {
		if !strings.Contains(string(state), sentinel) {
			t.Fatalf("%s is not in the fixture state; the leak assertions would pass by measuring nothing", sentinel)
		}
	}
	return fixture
}

func mcpInspectSentinels() []string {
	return []string{sentinelFolderHeader, sentinelCollectionHeader, sentinelEnvSecret, sentinelFolderSecretVar}
}

func (f mcpInspectFixture) inspect(t *testing.T, environmentID string) mcpserver.RequestInspection {
	t.Helper()
	inspection, err := f.backend.InspectRequest(f.collectionID, f.requestID, environmentID)
	if err != nil {
		t.Fatalf("InspectRequest(%q): %v", environmentID, err)
	}
	return inspection
}

func mcpInheritedRow(rows []mcpserver.InheritedRow, name string) (mcpserver.InheritedRow, bool) {
	for _, row := range rows {
		if strings.EqualFold(row.Name, name) {
			return row, true
		}
	}
	return mcpserver.InheritedRow{}, false
}

func mcpReference(references []mcpserver.VariableReference, name string) (mcpserver.VariableReference, bool) {
	for _, reference := range references {
		if reference.Name == name {
			return reference, true
		}
	}
	return mcpserver.VariableReference{}, false
}

// --- effective inheritance, with the level named ------------------------------

func TestInspectRequestReportsInheritedHeadersWithTheirLevel(t *testing.T) {
	fixture := newMCPInspectFixture(t)
	inspection := fixture.inspect(t, fixture.environmentID)

	for _, want := range []struct {
		name      string
		level     string
		levelPath string
	}{
		{"X-Collection-Token", mcpserver.LevelCollection, ""},
		{"X-Outer", mcpserver.LevelFolder, "widgets"},
		{"X-Tenant", mcpserver.LevelFolder, "widgets/admin"},
		{"X-Folder-Token", mcpserver.LevelFolder, "widgets/admin"},
		{"Content-Type", mcpserver.LevelRequest, ""},
	} {
		row, found := mcpInheritedRow(inspection.Headers, want.name)
		if !found {
			t.Errorf("the effective headers do not carry %s at all: %+v", want.name, inspection.Headers)
			continue
		}
		if row.Level != want.level || row.LevelPath != want.levelPath {
			t.Errorf("%s is attributed to %q/%q, want %q/%q", want.name, row.Level, row.LevelPath, want.level, want.levelPath)
		}
	}

	// The request's own Accept shadows the collection's, exactly as
	// scripting.EffectiveRequest's merge does — so the name appears once, at
	// level request, with the request's value.
	accept, found := mcpInheritedRow(inspection.Headers, "Accept")
	if !found {
		t.Fatal("Accept is missing entirely")
	}
	if accept.Level != mcpserver.LevelRequest || accept.Value != "application/vnd.custom" {
		t.Errorf("Accept = %+v, want the request's own row to win", accept)
	}
	count := 0
	for _, row := range inspection.Headers {
		if strings.EqualFold(row.Name, "Accept") {
			count++
		}
	}
	if count != 1 {
		t.Errorf("Accept appears %d times; a shadowed inherited row must not also be listed", count)
	}
	if _, found := mcpInheritedRow(inspection.Headers, "X-Disabled"); found {
		t.Error("a disabled collection header reached the effective set; it is never sent")
	}
}

func TestInspectRequestReportsTheWinningLevelForEachVariable(t *testing.T) {
	fixture := newMCPInspectFixture(t)
	inspection := fixture.inspect(t, fixture.environmentID)

	for _, want := range []struct {
		name      string
		level     string
		levelPath string
	}{
		// The environment beats the collection's baseUrl.
		{"baseUrl", mcpserver.LevelEnvironment, "Staging"},
		// The INNERMOST folder beats the outer one and the collection.
		{"region", mcpserver.LevelFolder, "widgets/admin"},
		{"tenant", mcpserver.LevelRequest, ""},
		{"globalOnly", mcpserver.LevelGlobal, "Shared"},
		{"apiToken", mcpserver.LevelEnvironment, "Staging"},
		{"folderSecret", mcpserver.LevelFolder, "widgets/admin"},
		{"legacyUrl", mcpserver.LevelCollection, ""},
	} {
		row, found := mcpInheritedRow(inspection.Variables, want.name)
		if !found {
			t.Errorf("the variable set does not carry %s: %+v", want.name, inspection.Variables)
			continue
		}
		if row.Level != want.level || row.LevelPath != want.levelPath {
			t.Errorf("%s is attributed to %q/%q, want %q/%q", want.name, row.Level, row.LevelPath, want.level, want.levelPath)
		}
	}

	if row, _ := mcpInheritedRow(inspection.Variables, "baseUrl"); row.Value != "https://staging.example.com" {
		t.Errorf("baseUrl = %q, want the environment's value", row.Value)
	}
	if row, _ := mcpInheritedRow(inspection.Variables, "region"); row.Value != "inner-region" {
		t.Errorf("region = %q, want the innermost folder's value", row.Value)
	}
}

func TestInspectRequestReportsInheritedScriptsInExecutionOrder(t *testing.T) {
	fixture := newMCPInspectFixture(t)
	inspection := fixture.inspect(t, fixture.environmentID)

	var pre, post []string
	for _, script := range inspection.Scripts {
		switch script.Phase {
		case "pre":
			pre = append(pre, script.Level+":"+script.LevelPath)
		case "post":
			post = append(post, script.Level+":"+script.LevelPath)
		}
	}
	// Outermost first going in, which is scripting.MergedRuntimeScripts' order.
	if len(pre) != 2 || pre[0] != "collection:" || pre[1] != "folder:widgets/admin" {
		t.Errorf("pre-request script order = %v, want collection then the admin folder", pre)
	}
	// The request has no post script, so the collection's is the only one, and
	// the request that DOES have no script of its own is exactly the case
	// get_request cannot report.
	if len(post) != 1 || post[0] != "collection:" {
		t.Errorf("post-response script order = %v, want the collection's alone", post)
	}
	if inspection.Request.PreScript != "" {
		t.Errorf("the request itself has a pre script %q; the fixture meant it to have none", inspection.Request.PreScript)
	}
}

func TestInspectRequestReportsTheEffectiveAuthTheRunWouldUse(t *testing.T) {
	fixture := newMCPInspectFixture(t)
	inspection := fixture.inspect(t, fixture.environmentID)

	if inspection.Request.AuthType != "bearer" || inspection.Request.AuthSource != mcpAuthSourceCollection {
		t.Errorf("effective auth = %q from %q, want bearer from the collection", inspection.Request.AuthType, inspection.Request.AuthSource)
	}
}

// --- GraphQL variables --------------------------------------------------------

func TestInspectRequestAndGetRequestBothCarryGraphQLVariables(t *testing.T) {
	fixture := newMCPInspectFixture(t)

	detail, err := fixture.backend.GetRequest(fixture.collectionID, fixture.requestID)
	if err != nil {
		t.Fatalf("GetRequest: %v", err)
	}
	if !strings.Contains(detail.Body, "store(code") {
		t.Errorf("the GraphQL query is missing from the body: %q", detail.Body)
	}
	if !strings.Contains(detail.GraphQLVariables, "{{storeCode}}") {
		t.Errorf("graphqlVariables = %q, want the authored document with its template intact", detail.GraphQLVariables)
	}

	inspection := fixture.inspect(t, fixture.environmentID)
	if inspection.Request.GraphQLVariables != detail.GraphQLVariables {
		t.Errorf("inspect_request reports different graphqlVariables (%q) from get_request (%q)", inspection.Request.GraphQLVariables, detail.GraphQLVariables)
	}
}

// A request whose mode is no longer graphql keeps its old variables document on
// disk. Reporting it would describe a call the request does not make.
func TestGraphQLVariablesAreReportedOnlyForAGraphQLBody(t *testing.T) {
	body := types.RequestBody{Mode: "json", JSON: "{}", GraphQLVariables: "{\"stale\":1}"}
	if got := mcpGraphQLVariables(body); got != "" {
		t.Errorf("a json body reported graphqlVariables %q", got)
	}
	body.Mode = "graphql"
	if got := mcpGraphQLVariables(body); got != "{\"stale\":1}" {
		t.Errorf("a graphql body reported graphqlVariables %q", got)
	}
}

// --- the unresolved-variable report -------------------------------------------

func TestInspectRequestNamesTheVariablesTheEnvironmentDoesNotDefine(t *testing.T) {
	fixture := newMCPInspectFixture(t)
	inspection := fixture.inspect(t, fixture.environmentID)

	// missingId is in the URL, storeCode is in the GraphQL variables document,
	// and legacyHost is only reachable through legacyUrl's own value.
	want := []string{"legacyHost", "missingId", "storeCode"}
	if strings.Join(inspection.UnresolvedVariables, ",") != strings.Join(want, ",") {
		t.Fatalf("unresolvedVariables = %v, want %v", inspection.UnresolvedVariables, want)
	}

	missing, found := mcpReference(inspection.References, "missingId")
	if !found || missing.Resolved || missing.Kind != mcpserver.KindVariable {
		t.Fatalf("missingId reference = %+v", missing)
	}
	if len(missing.Where) != 1 || missing.Where[0] != "url" {
		t.Errorf("missingId.where = %v, want [url]", missing.Where)
	}
	if missing.Note == "" {
		t.Error("an unresolved reference carries no note telling the agent what to do")
	}

	// The transitive one: legacyHost is never written in the request. It is
	// found by following legacyUrl's value, and its where says so.
	legacyHost, found := mcpReference(inspection.References, "legacyHost")
	if !found {
		t.Fatal("legacyHost was not found; the transitive walk over variable values is not running")
	}
	if len(legacyHost.Where) != 1 || legacyHost.Where[0] != "variable:legacyUrl" {
		t.Errorf("legacyHost.where = %v, want [variable:legacyUrl]", legacyHost.Where)
	}

	// A reference that DOES resolve says where from.
	baseURL, found := mcpReference(inspection.References, "baseUrl")
	if !found || !baseURL.Resolved || baseURL.Level != mcpserver.LevelEnvironment {
		t.Errorf("baseUrl reference = %+v, want resolved at the environment level", baseURL)
	}
	// A secret reference resolves and is flagged; it never needs supplying.
	token, found := mcpReference(inspection.References, "apiToken")
	if !found || !token.Resolved || !token.Secret {
		t.Errorf("apiToken reference = %+v, want resolved and flagged secret", token)
	}
	if len(token.Where) != 1 || token.Where[0] != "auth:token" {
		t.Errorf("apiToken.where = %v, want [auth:token] — it is reached through the INHERITED collection auth", token.Where)
	}
}

func TestInspectRequestReportsAnEmptyUnresolvedListWhenEverythingResolves(t *testing.T) {
	fixture := newMCPInspectFixture(t)

	// Give the environment the three missing names. Nothing else changes, so a
	// list that is still non-empty is the report being wrong rather than the
	// fixture being incomplete.
	fixture.app.mu.Lock()
	collection := &fixture.app.state.Workspaces[0].Collections[0]
	for index := range collection.Environments {
		if collection.Environments[index].ID != "env-inspect" {
			continue
		}
		collection.Environments[index].Variables = append(collection.Environments[index].Variables,
			Variable{ID: "env-missing", Name: "missingId", Value: "w_1", Enabled: true},
			Variable{ID: "env-store", Name: "storeCode", Value: "DHK-04", Enabled: true},
			Variable{ID: "env-legacy", Name: "legacyHost", Value: "https://legacy.example.com", Enabled: true},
		)
	}
	fixture.app.mu.Unlock()

	inspection := fixture.inspect(t, fixture.environmentID)
	if len(inspection.UnresolvedVariables) != 0 {
		t.Errorf("unresolvedVariables = %v, want empty once the environment defines every name", inspection.UnresolvedVariables)
	}
	// Empty is a JSON [], not null: an agent must be able to iterate it.
	encoded, err := json.Marshal(inspection)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(encoded), `"unresolvedVariables":[]`) {
		t.Errorf("an empty unresolvedVariables did not marshal as []: %s", encoded)
	}
	// And it still says what it does not answer, so an empty list is not read
	// as a promise the call will work.
	if len(inspection.NotResolved) == 0 {
		t.Error("notResolved is empty")
	}
}

// The three kinds that are not ordinary variables each have their own answer,
// and none of them belongs in unresolvedVariables: one is generated, one needs
// the user, and one is deliberately not checked.
func TestInspectRequestClassifiesDynamicPromptAndProcessEnvReferences(t *testing.T) {
	fixture := newMCPInspectFixture(t)
	inspection := fixture.inspect(t, fixture.environmentID)

	dynamic, found := mcpReference(inspection.References, "$timestamp")
	if !found || dynamic.Kind != mcpserver.KindDynamic || !dynamic.Resolved {
		t.Errorf("$timestamp reference = %+v, want a resolved dynamic", dynamic)
	}
	prompt, found := mcpReference(inspection.References, "?approver")
	if !found || prompt.Kind != mcpserver.KindPrompt || prompt.Resolved {
		t.Errorf("?approver reference = %+v, want an unresolved prompt", prompt)
	}
	if !strings.Contains(prompt.Note, "USER") {
		t.Errorf("the prompt note does not say who supplies it: %q", prompt.Note)
	}
	processEnv, found := mcpReference(inspection.References, "process.env.LITEAPI_TEST_HOST")
	if !found || processEnv.Kind != mcpserver.KindProcessEnv || processEnv.Resolved {
		t.Errorf("process.env reference = %+v, want an unchecked processEnv", processEnv)
	}

	for _, name := range []string{"$timestamp", "?approver", "process.env.LITEAPI_TEST_HOST"} {
		for _, unresolved := range inspection.UnresolvedVariables {
			if unresolved == name {
				t.Errorf("%s is listed in unresolvedVariables; it is not supplied the way a variable is", name)
			}
		}
	}
}

// --- the environmentId truth ---------------------------------------------------

// The contradiction this task reconciled. tools.go told an agent that omitting
// environmentId uses the app's active environment; the backend said the
// collection-environment selection is not knowable here. The backend was right:
// that selection lives in the WebView's own storage and never reaches AppState,
// so omitting the argument applies NO collection environment — a materially
// different call, because the collection's own baseUrl wins instead.
func TestInspectRequestWithNoEnvironmentAppliesNoCollectionEnvironment(t *testing.T) {
	fixture := newMCPInspectFixture(t)
	inspection := fixture.inspect(t, "")

	if inspection.Environment.CollectionEnvironmentID != "" || inspection.Environment.CollectionEnvironmentName != "" {
		t.Errorf("a collection environment was reported for an omitted environmentId: %+v", inspection.Environment)
	}
	// The collection's baseUrl wins, not the environment's — which is the whole
	// observable difference, and the reason the old description was harmful.
	row, found := mcpInheritedRow(inspection.Variables, "baseUrl")
	if !found || row.Level != mcpserver.LevelCollection || row.Value != "https://collection.example.com" {
		t.Errorf("baseUrl = %+v, want the COLLECTION's value when no environment is selected", row)
	}
	// The environment's secret is out of scope entirely, so the reference to it
	// is now unresolved rather than quietly resolving from somewhere else.
	if token, found := mcpReference(inspection.References, "apiToken"); !found || token.Resolved {
		t.Errorf("apiToken reference = %+v, want unresolved with no environment selected", token)
	}

	// The global environment applies either way; it is the only selection that
	// IS persisted in app state.
	if len(inspection.Environment.GlobalEnvironmentNames) != 1 || inspection.Environment.GlobalEnvironmentNames[0] != "Shared" {
		t.Errorf("global environments = %+v, want the workspace's active one", inspection.Environment.GlobalEnvironmentNames)
	}
	if row, found := mcpInheritedRow(inspection.Variables, "globalOnly"); !found || row.Level != mcpserver.LevelGlobal {
		t.Errorf("globalOnly = %+v, want it in scope from the global environment", row)
	}

	// And the note says so in words, because an agent reading two empty fields
	// would otherwise infer the opposite.
	note := inspection.Environment.Note
	if !strings.Contains(note, "No collection environment") || !strings.Contains(note, "does NOT") {
		t.Errorf("the environment note does not state the rule: %q", note)
	}
}

func TestInspectRequestWithAnEnvironmentReportsTheOneInEffect(t *testing.T) {
	fixture := newMCPInspectFixture(t)
	inspection := fixture.inspect(t, fixture.environmentID)

	if inspection.Environment.CollectionEnvironmentID != "env-inspect" || inspection.Environment.CollectionEnvironmentName != "Staging" {
		t.Errorf("environment = %+v, want the selected one named", inspection.Environment)
	}
	if len(inspection.Environment.GlobalEnvironmentIDs) != 1 || inspection.Environment.GlobalEnvironmentIDs[0] != "env-inspect-global" {
		t.Errorf("global environment ids = %v", inspection.Environment.GlobalEnvironmentIDs)
	}
}

// The two errors mcpRunPlan gives, given here for the same reason: a global
// environment id is a real id the agent read from list_environments, so "no
// such environment" would be both wrong and unactionable.
func TestInspectRequestRejectsUnknownAndGlobalEnvironmentIds(t *testing.T) {
	fixture := newMCPInspectFixture(t)

	_, err := fixture.backend.InspectRequest(fixture.collectionID, fixture.requestID, "env-inspect-global")
	if err == nil || !strings.Contains(err.Error(), "global environment") {
		t.Errorf("a global environment id gave %v, want the message that names it as global", err)
	}
	_, err = fixture.backend.InspectRequest(fixture.collectionID, fixture.requestID, "env-nope")
	if err == nil || !strings.Contains(err.Error(), "list_environments") {
		t.Errorf("an unknown environment id gave %v, want the message that names the way out", err)
	}
	_, err = fixture.backend.InspectRequest(fixture.collectionID, "req-nope", "")
	if err == nil || !strings.Contains(err.Error(), "list_requests") {
		t.Errorf("an unknown request id gave %v", err)
	}
	if _, err := fixture.backend.InspectRequest("", fixture.requestID, ""); err == nil {
		t.Error("an empty collectionId was accepted")
	}
}

// --- effective settings ---------------------------------------------------------

// get_request reports the STORED VerifyTLS, which is the right answer to its
// question. inspect_request reports what the send path computes, which ANDs it
// with the app preference — so the two can disagree, and when they do this one
// is what happens.
func TestInspectRequestReportsTheTLSPostureTheSendPathComputes(t *testing.T) {
	fixture := newMCPInspectFixture(t)

	inspection := fixture.inspect(t, fixture.environmentID)
	if !inspection.Settings.VerifyTLS || inspection.Settings.VerifyTLSDisabledBy != "" {
		t.Fatalf("settings = %+v, want verification on with nothing disabling it", inspection.Settings)
	}

	disabled := false
	fixture.app.mu.Lock()
	fixture.app.state.Preferences.Request.SSLVerification = &disabled
	fixture.app.mu.Unlock()

	inspection = fixture.inspect(t, fixture.environmentID)
	if inspection.Settings.VerifyTLS {
		t.Error("the app preference turned verification off but the effective posture still says on")
	}
	if inspection.Settings.VerifyTLSDisabledBy != "appPreference" {
		t.Errorf("verifyTlsDisabledBy = %q, want appPreference — the fix is in a different place from a request-level opt-out", inspection.Settings.VerifyTLSDisabledBy)
	}
	// get_request still reports the request's own stored flag, unchanged.
	detail, err := fixture.backend.GetRequest(fixture.collectionID, fixture.requestID)
	if err != nil {
		t.Fatalf("GetRequest: %v", err)
	}
	if !detail.Settings.VerifyTLS {
		t.Error("get_request's stored flag changed; it reports what is stored, not what is computed")
	}
}

// --- redaction ------------------------------------------------------------------

// The same measurement mcp_backend_test.go makes, extended to the levels this
// tool newly reaches: marshal the whole result and search the bytes, so a field
// added later is caught by the search rather than missed by an assertion list
// nobody remembered to extend.
func TestInspectRequestLeaksNoSentinelFromAnyLevel(t *testing.T) {
	fixture := newMCPInspectFixture(t)

	for _, environmentID := range []string{"", fixture.environmentID} {
		inspection := fixture.inspect(t, environmentID)
		encoded, err := json.Marshal(inspection)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		for _, sentinel := range mcpInspectSentinels() {
			if strings.Contains(string(encoded), sentinel) {
				t.Errorf("InspectRequest(environmentId=%q) leaked %s:\n%s", environmentID, sentinel, encoded)
			}
		}
	}
}

func TestInspectRequestMasksLiteralsAndDropsSecretValues(t *testing.T) {
	fixture := newMCPInspectFixture(t)
	inspection := fixture.inspect(t, fixture.environmentID)

	// Credential-shaped header literals are masked at the FOLDER and
	// COLLECTION levels exactly as they are on the request itself.
	for _, name := range []string{"X-Collection-Token", "X-Folder-Token"} {
		row, found := mcpInheritedRow(inspection.Headers, name)
		if !found {
			t.Errorf("%s is missing", name)
			continue
		}
		if row.Value != mcpserver.MaskedValue {
			t.Errorf("%s = %q, want it masked", name, row.Value)
		}
	}
	// A secret variable is a name and a flag, at every level.
	for _, name := range []string{"apiToken", "folderSecret"} {
		row, found := mcpInheritedRow(inspection.Variables, name)
		if !found {
			t.Errorf("%s is missing from the variable set", name)
			continue
		}
		if !row.Secret || row.Value != "" {
			t.Errorf("%s = %+v, want secret with an empty value", name, row)
		}
	}

	// The masking must stay discriminating: templates and ordinary values
	// survive, or the tool is useless.
	if row, _ := mcpInheritedRow(inspection.Headers, "X-Tenant"); row.Value != "{{tenant}}" {
		t.Errorf("a templated folder header is %q, want the template intact", row.Value)
	}
	if row, _ := mcpInheritedRow(inspection.Headers, "X-Outer"); row.Value != "outer" {
		t.Errorf("an ordinary folder header was mangled to %q", row.Value)
	}
	if row, _ := mcpInheritedRow(inspection.Variables, "region"); row.Value != "inner-region" {
		t.Errorf("an ordinary folder variable was mangled to %q", row.Value)
	}
}

// Nothing is interpolated: the tool reports references, never values. A
// resolved reference to a secret proves the point — it is reported as resolved
// and the value is nowhere in the payload.
func TestInspectRequestNeverInterpolates(t *testing.T) {
	fixture := newMCPInspectFixture(t)
	inspection := fixture.inspect(t, fixture.environmentID)

	if !strings.Contains(inspection.Request.URL, "{{baseUrl}}") {
		t.Errorf("the URL %q was interpolated", inspection.Request.URL)
	}
	encoded, err := json.Marshal(inspection)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// The environment's baseUrl is reported as a VARIABLE row, which is
	// correct, but it must not have been substituted into the URL.
	if strings.Contains(inspection.Request.URL, "staging.example.com") {
		t.Errorf("the URL carries a resolved value: %q", inspection.Request.URL)
	}
	if !strings.Contains(string(encoded), "{{storeCode}}") {
		t.Error("the GraphQL variables document lost its template")
	}
}
