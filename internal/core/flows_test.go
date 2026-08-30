package core

// Flow CRUD, validation and the round trip through the collection store.
//
// THE ROUND TRIP IS THE POINT of the persistence half. A flow that exists only
// in app state is a flow the user loses to the collection watcher, a restart,
// or a `git pull` — the exact failure CreateEnvironment's comment describes
// having already been shipped once. So these tests write through the real
// binding and read back with readCollectionFromDisk, the same function the app
// uses when it reopens a folder.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mutexdev/lite_api/internal/types"
)

// flowCRUDFixture is an App with one collection holding two saved requests.
type flowCRUDFixture struct {
	t            *testing.T
	app          *App
	collectionID string
	path         string
	firstID      string
	secondID     string
}

func newFlowCRUDFixture(t *testing.T) *flowCRUDFixture {
	t.Helper()
	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	collection := state.Workspaces[0].Collections[0]
	if _, err := app.CreateRequest(collection.ID, "http", "First"); err != nil {
		t.Fatalf("CreateRequest: %v", err)
	}
	state, err = app.CreateRequest(collection.ID, "http", "Second")
	if err != nil {
		t.Fatalf("CreateRequest: %v", err)
	}
	collection = state.Workspaces[0].Collections[0]
	if len(collection.Items) < 2 {
		t.Fatalf("the fixture collection holds %d requests", len(collection.Items))
	}
	fixture := &flowCRUDFixture{
		t:            t,
		app:          app,
		collectionID: collection.ID,
		path:         collection.Path,
	}
	for _, item := range collection.Items {
		switch item.Name {
		case "First":
			fixture.firstID = item.ID
		case "Second":
			fixture.secondID = item.ID
		}
	}
	if fixture.firstID == "" || fixture.secondID == "" {
		t.Fatalf("the fixture requests were not found in %#v", collection.Items)
	}
	return fixture
}

// flow is a valid two-step flow over the fixture's requests, exercising every
// field of the schema so the round trip has something to lose.
func (f *flowCRUDFixture) flow() types.Flow {
	return types.Flow{
		ID:          "flow_fixture",
		Name:        "Provision",
		Description: "two steps",
		Inputs: []types.FlowInput{
			{Name: "storeCode", Required: true, Description: "Store short code"},
			{Name: "note"},
		},
		Steps: []types.FlowStep{
			{
				ID:        "lookup",
				RequestID: f.firstID,
				Vars:      map[string]string{"code": "{{storeCode}}"},
				Extract: []types.FlowExtract{
					{Name: "storeId", From: "body", Path: "$.data.store.id"},
					{Name: "etag", From: "header", Path: "ETag"},
					{Name: "code", From: "status"},
				},
				Assert: []types.FlowAssert{{Type: "status", Equals: 200}},
			},
			{
				ID:        "create",
				RequestID: f.secondID,
				Vars:      map[string]string{"storeId": "{{storeId}}"},
				Assert: []types.FlowAssert{
					{Type: "status", In: []int{200, 201}},
					{Type: "body", Path: "$.terminal.state", Equals: "created"},
					{Type: "body", Path: `$["key with spaces"]`, Contains: "ok"},
					{Type: "body", Path: "$.terminal.id", Exists: true},
				},
			},
		},
		Outputs: []types.FlowOutput{{Name: "storeId", Value: "{{storeId}}"}},
	}
}

func (f *flowCRUDFixture) reloadFromDisk() Collection {
	f.t.Helper()
	if err := f.app.FlushPendingWrites(); err != nil {
		f.t.Fatalf("FlushPendingWrites: %v", err)
	}
	collection, err := readCollectionFromDisk(f.path)
	if err != nil {
		f.t.Fatalf("readCollectionFromDisk: %v", err)
	}
	return collection
}

// --- 1. the round trip ------------------------------------------------------

func TestCreateFlowSurvivesAReadBackFromDisk(t *testing.T) {
	f := newFlowCRUDFixture(t)
	flow := f.flow()

	state, err := f.app.CreateFlow(f.collectionID, flow)
	if err != nil {
		t.Fatalf("CreateFlow: %v", err)
	}
	if got := len(state.Workspaces[0].Collections[0].Flows); got != 1 {
		t.Fatalf("the returned state carries %d flows", got)
	}

	reloaded := f.reloadFromDisk()
	if len(reloaded.Flows) != 1 {
		t.Fatalf("the collection on disk carries %d flows", len(reloaded.Flows))
	}
	if !reflect.DeepEqual(reloaded.Flows[0], flow) {
		t.Errorf("the flow changed on the way through disk:\n got %#v\nwant %#v", reloaded.Flows[0], flow)
	}
}

// A collection with no flows must look on disk exactly as it did before flows
// existed: adopting the feature should add nothing to anyone's working tree
// until they author one.
func TestACollectionWithNoFlowsWritesNoFlowsKey(t *testing.T) {
	f := newFlowCRUDFixture(t)
	if err := f.app.FlushPendingWrites(); err != nil {
		t.Fatalf("FlushPendingWrites: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(f.path, "opencollection.yml"))
	if err != nil {
		t.Fatalf("read opencollection.yml: %v", err)
	}
	if strings.Contains(string(data), "flows:") {
		t.Errorf("opencollection.yml carries a flows key with no flows:\n%s", data)
	}
}

// The bru/json format keeps flows in bruno.json, the only root config it has.
func TestFlowsRoundTripThroughABruFormatCollection(t *testing.T) {
	app := newAppForTest(t)
	imported := importPostmanCollectionForTest(t, app, "flows-bru", `{
	  "info": {"name": "Flows in bru"},
	  "item": [{"name": "r", "request": {"method": "GET", "url": "https://example.test"}}]
	}`)
	if strings.EqualFold(imported.Format, "yml") {
		t.Fatalf("this test needs a bru/json collection, got format %q", imported.Format)
	}
	flow := types.Flow{
		ID:    "flow_bru",
		Name:  "Bru flow",
		Steps: []types.FlowStep{{ID: "only", RequestID: imported.Items[0].ID, Assert: []types.FlowAssert{{Type: "status", Equals: 200}}}},
	}
	if _, err := app.CreateFlow(imported.ID, flow); err != nil {
		t.Fatalf("CreateFlow: %v", err)
	}
	if err := app.FlushPendingWrites(); err != nil {
		t.Fatalf("FlushPendingWrites: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(imported.Path, "bruno.json"))
	if err != nil {
		t.Fatalf("read bruno.json: %v", err)
	}
	var config map[string]interface{}
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatalf("parse bruno.json: %v", err)
	}
	if _, ok := config["flows"]; !ok {
		t.Fatalf("bruno.json has no flows key:\n%s", data)
	}

	reloaded, err := readCollectionFromDisk(imported.Path)
	if err != nil {
		t.Fatalf("readCollectionFromDisk: %v", err)
	}
	if len(reloaded.Flows) != 1 || reloaded.Flows[0].ID != "flow_bru" {
		t.Fatalf("flows did not come back: %#v", reloaded.Flows)
	}
	// The int stays an int rather than arriving as a float64 from JSON: a flow
	// that reloads with 200 spelled differently rewrites the file on every save.
	if got := reloaded.Flows[0].Steps[0].Assert[0].Equals; got != 200 {
		t.Errorf("equals came back as %#v (%T), want the int 200", got, got)
	}
}

// --- 2. update and delete ---------------------------------------------------

func TestUpdateFlowReplacesInPlaceAndReachesDisk(t *testing.T) {
	f := newFlowCRUDFixture(t)
	flow := f.flow()
	if _, err := f.app.CreateFlow(f.collectionID, flow); err != nil {
		t.Fatalf("CreateFlow: %v", err)
	}
	flow.Name = "Renamed"
	flow.Steps = flow.Steps[:1]
	if _, err := f.app.UpdateFlow(f.collectionID, flow); err != nil {
		t.Fatalf("UpdateFlow: %v", err)
	}

	reloaded := f.reloadFromDisk()
	if len(reloaded.Flows) != 1 {
		t.Fatalf("update added a flow instead of replacing one: %#v", reloaded.Flows)
	}
	if reloaded.Flows[0].Name != "Renamed" || len(reloaded.Flows[0].Steps) != 1 {
		t.Errorf("the update did not reach disk: %#v", reloaded.Flows[0])
	}
}

func TestDeleteFlowRemovesItFromDisk(t *testing.T) {
	f := newFlowCRUDFixture(t)
	flow := f.flow()
	if _, err := f.app.CreateFlow(f.collectionID, flow); err != nil {
		t.Fatalf("CreateFlow: %v", err)
	}
	if _, err := f.app.DeleteFlow(f.collectionID, flow.ID); err != nil {
		t.Fatalf("DeleteFlow: %v", err)
	}
	if reloaded := f.reloadFromDisk(); len(reloaded.Flows) != 0 {
		t.Errorf("the flow is still on disk: %#v", reloaded.Flows)
	}
}

func TestFlowCRUDNamesWhatIsMissing(t *testing.T) {
	f := newFlowCRUDFixture(t)
	flow := f.flow()
	if _, err := f.app.CreateFlow(f.collectionID, flow); err != nil {
		t.Fatalf("CreateFlow: %v", err)
	}

	if _, err := f.app.CreateFlow(f.collectionID, flow); err == nil {
		t.Error("creating the same flow id twice was accepted")
	} else if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("error = %v", err)
	}

	unknown := f.flow()
	unknown.ID = "flow_nope"
	if _, err := f.app.UpdateFlow(f.collectionID, unknown); err == nil {
		t.Error("updating an unknown flow was accepted")
	} else if !strings.Contains(err.Error(), "flow_nope") {
		t.Errorf("error = %v, want it to echo the id", err)
	}

	if _, err := f.app.DeleteFlow(f.collectionID, "flow_nope"); err == nil {
		t.Error("deleting an unknown flow was accepted")
	}
}

// CreateFlow assigns an id when the caller has none, the way the other create
// bindings do — an authoring client should not have to invent one.
func TestCreateFlowAssignsAnIDWhenTheCallerHasNone(t *testing.T) {
	f := newFlowCRUDFixture(t)
	flow := f.flow()
	flow.ID = ""
	state, err := f.app.CreateFlow(f.collectionID, flow)
	if err != nil {
		t.Fatalf("CreateFlow: %v", err)
	}
	created := state.Workspaces[0].Collections[0].Flows[0]
	if !strings.HasPrefix(created.ID, "flow") {
		t.Errorf("assigned id = %q, want a flow-prefixed id", created.ID)
	}
}

// --- 3. validation ----------------------------------------------------------

func TestCreateFlowRefusesEveryShapeThatCannotRun(t *testing.T) {
	cases := []struct {
		name     string
		mutate   func(*flowCRUDFixture, *types.Flow)
		contains string
	}{
		{"an empty name", func(f *flowCRUDFixture, flow *types.Flow) { flow.Name = "  " }, "name is required"},
		{"no steps", func(f *flowCRUDFixture, flow *types.Flow) { flow.Steps = nil }, "no steps"},
		{"an unknown requestId", func(f *flowCRUDFixture, flow *types.Flow) {
			flow.Steps[1].RequestID = "req_nope"
		}, "req_nope"},
		{"an empty requestId", func(f *flowCRUDFixture, flow *types.Flow) {
			flow.Steps[1].RequestID = ""
		}, "no requestId"},
		{"a duplicate step id", func(f *flowCRUDFixture, flow *types.Flow) {
			flow.Steps[1].ID = flow.Steps[0].ID
		}, "twice"},
		{"an empty step id", func(f *flowCRUDFixture, flow *types.Flow) { flow.Steps[0].ID = "" }, "no id"},
		{"a duplicate input", func(f *flowCRUDFixture, flow *types.Flow) {
			flow.Inputs = append(flow.Inputs, types.FlowInput{Name: "storeCode"})
		}, "twice"},
		{"an unparseable extraction path", func(f *flowCRUDFixture, flow *types.Flow) {
			flow.Steps[0].Extract[0].Path = "$.items[*].id"
		}, "unsupported subscript"},
		{"an unknown extraction source", func(f *flowCRUDFixture, flow *types.Flow) {
			flow.Steps[0].Extract[0].From = "cookies"
		}, "body, header or status"},
		{"a header extraction with no header", func(f *flowCRUDFixture, flow *types.Flow) {
			flow.Steps[0].Extract[1].Path = ""
		}, "names no header"},
		{"an unknown assertion type", func(f *flowCRUDFixture, flow *types.Flow) {
			flow.Steps[0].Assert[0].Type = "duration"
		}, "status or body"},
		{"a status assertion with nothing to check", func(f *flowCRUDFixture, flow *types.Flow) {
			flow.Steps[0].Assert[0] = types.FlowAssert{Type: "status"}
		}, "set equals or in"},
		{"a body assertion with no predicate", func(f *flowCRUDFixture, flow *types.Flow) {
			flow.Steps[1].Assert = []types.FlowAssert{{Type: "body", Path: "$.terminal.state"}}
		}, "set equals, contains or exists"},
		{"an unnamed output", func(f *flowCRUDFixture, flow *types.Flow) {
			flow.Outputs = append(flow.Outputs, types.FlowOutput{Value: "{{storeId}}"})
		}, "output with no name"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			f := newFlowCRUDFixture(t)
			flow := f.flow()
			testCase.mutate(f, &flow)
			_, err := f.app.CreateFlow(f.collectionID, flow)
			if err == nil {
				t.Fatal("the flow was accepted")
			}
			if !strings.Contains(err.Error(), testCase.contains) {
				t.Errorf("error = %v, want it to mention %q", err, testCase.contains)
			}
			if reloaded := f.reloadFromDisk(); len(reloaded.Flows) != 0 {
				t.Errorf("a refused flow reached disk: %#v", reloaded.Flows)
			}
		})
	}
}

// The authoring-time half of the shadow refusal. It uses a secret declared in a
// COLLECTION ENVIRONMENT that is not selected — there is no selected
// environment while editing, so the check is the union over all of them.
func TestCreateFlowRefusesAStepVarThatShadowsASecret(t *testing.T) {
	f := newFlowCRUDFixture(t)
	f.app.mu.Lock()
	collection := &f.app.state.Workspaces[0].Collections[0]
	collection.Environments = append(collection.Environments, Environment{
		ID:        "env-prod",
		Name:      "Production",
		Variables: []Variable{{ID: "v1", Name: "apiToken", Value: "PRODUCTION-SECRET-VALUE", Enabled: true, Secret: true}},
	})
	f.app.mu.Unlock()

	flow := f.flow()
	flow.Steps[0].Vars["apiToken"] = "chosen-by-the-caller"
	_, err := f.app.CreateFlow(f.collectionID, flow)
	if err == nil {
		t.Fatal("a step var shadowing a secret was accepted")
	}
	if !strings.Contains(err.Error(), "apiToken") || !strings.Contains(err.Error(), "secret") {
		t.Errorf("error = %v, want it to name the variable and say why", err)
	}
	if strings.Contains(err.Error(), "PRODUCTION-SECRET-VALUE") {
		t.Error("the refusal leaked the secret's value")
	}
}

// validateFlow is the shared gate: the CRUD bindings and, later, the MCP write
// tier both call it with a collection and a secret-name set and nothing else.
// This pins that it is usable without an App.
func TestValidateFlowIsCallableWithoutAnApp(t *testing.T) {
	collection := types.Collection{ID: "c1", Items: []types.RequestItem{{ID: "r1"}}}
	flow := types.Flow{
		Name:  "ok",
		Steps: []types.FlowStep{{ID: "s1", RequestID: "r1", Assert: []types.FlowAssert{{Type: "status", Equals: 200}}}},
	}
	if err := validateFlow(collection, nil, flow); err != nil {
		t.Fatalf("validateFlow: %v", err)
	}
	if err := validateFlow(collection, map[string]bool{"token": true}, types.Flow{
		Name:  "shadow",
		Steps: []types.FlowStep{{ID: "s1", RequestID: "r1", Vars: map[string]string{"token": "x"}}},
	}); err == nil {
		t.Error("the shared validator did not apply the secret set it was given")
	}
}
