package yamlstore

// The flow codec, both directions.
//
// The core-side test proves flows survive a real collection reaching disk; this
// one holds the codec to the shape the document publishes, so that a flow
// written by LiteAPI and a flow typed into opencollection.yml by hand are the
// same thing.

import (
	"encoding/json"
	"reflect"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/mutexdev/lite_api/internal/types"
)

func codecFixtureFlow() types.Flow {
	return types.Flow{
		ID:          "flow_8f3k",
		Name:        "Provision POS terminal",
		Description: "GraphQL lookup -> create terminal on API B -> activate on API C",
		Inputs:      []types.FlowInput{{Name: "storeCode", Required: true, Description: "Store short code, e.g. DHK-04"}},
		Steps: []types.FlowStep{
			{
				ID:        "lookup",
				RequestID: "req_graphql_store",
				Vars:      map[string]string{"code": "{{storeCode}}"},
				Extract: []types.FlowExtract{
					{Name: "storeId", From: "body", Path: "$.data.store.id"},
					{Name: "region", From: "body", Path: "$.data.store.region"},
				},
				Assert: []types.FlowAssert{{Type: "status", Equals: 200}},
			},
			{
				ID:        "createTerminal",
				RequestID: "req_apib_create_terminal",
				Vars:      map[string]string{"storeId": "{{storeId}}", "region": "{{region}}"},
				Extract:   []types.FlowExtract{{Name: "terminalId", From: "body", Path: "$.terminal.id"}},
				Assert: []types.FlowAssert{
					{Type: "status", In: []int{200, 201}},
					{Type: "body", Path: "$.terminal.state", Equals: "created"},
					{Type: "body", Path: "$.terminal.id", Exists: true},
					{Type: "body", Path: "$.terminal.label", Contains: "POS"},
				},
			},
		},
		Outputs: []types.FlowOutput{{Name: "terminalId", Value: "{{terminalId}}"}},
	}
}

func TestFlowsRoundTripThroughYAML(t *testing.T) {
	flow := codecFixtureFlow()
	data, err := yaml.Marshal(map[string]interface{}{"flows": YAMLFlows([]types.Flow{flow})})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var root map[string]interface{}
	if err := yaml.Unmarshal(data, &root); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	flows := ParseFlows(root["flows"])
	if len(flows) != 1 {
		t.Fatalf("parsed %d flows:\n%s", len(flows), data)
	}
	if !reflect.DeepEqual(flows[0], flow) {
		t.Errorf("the flow changed through YAML:\n got %#v\nwant %#v\nyaml:\n%s", flows[0], flow, data)
	}
}

// The same maps go into bruno.json, where the decoder turns every number into a
// float64. Normalising on the way in is what stops a reload from rewriting the
// file with 200 spelled differently.
func TestFlowsRoundTripThroughJSON(t *testing.T) {
	flow := codecFixtureFlow()
	data, err := json.Marshal(map[string]interface{}{"flows": JSONFlows([]types.Flow{flow})})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var root map[string]interface{}
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	flows := ParseFlows(root["flows"])
	if len(flows) != 1 {
		t.Fatalf("parsed %d flows:\n%s", len(flows), data)
	}
	if !reflect.DeepEqual(flows[0], flow) {
		t.Errorf("the flow changed through JSON:\n got %#v\nwant %#v\njson:\n%s", flows[0], flow, data)
	}
}

func TestStringifyCollectionCarriesFlowsAndOmitsThemWhenThereAreNone(t *testing.T) {
	collection := types.Collection{Name: "With flows", Flows: []types.Flow{codecFixtureFlow()}}
	var root map[string]interface{}
	if err := yaml.Unmarshal([]byte(StringifyCollection(collection)), &root); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(ParseFlows(root["flows"])) != 1 {
		t.Errorf("StringifyCollection dropped the flow:\n%s", StringifyCollection(collection))
	}

	var bare map[string]interface{}
	if err := yaml.Unmarshal([]byte(StringifyCollection(types.Collection{Name: "Bare"})), &bare); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := bare["flows"]; ok {
		t.Error("a collection with no flows still wrote a flows key")
	}
}

// A hand-edited file is the case this parser exists for. Rows that cannot run
// are dropped; rows that are merely WRONG are kept, so core.validateFlow can
// explain them in the app rather than the file silently losing what was typed.
func TestParseFlowsKeepsWhatIsWrongAndDropsWhatCannotRun(t *testing.T) {
	const source = `
flows:
  - id: flow_ok
    name: Kept
    steps:
      - id: only
        requestId: req_1
        extract:
          - name: id
            from: teleport
            path: $.items[*].id
        assert:
          - type: duration
            equals: fast
  - id: flow_no_steps
    name: Dropped
  - name: not a map
`
	var root map[string]interface{}
	if err := yaml.Unmarshal([]byte(source), &root); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	flows := ParseFlows(root["flows"])
	if len(flows) != 1 {
		t.Fatalf("parsed %d flows, want only the one with steps: %#v", len(flows), flows)
	}
	step := flows[0].Steps[0]
	if step.Extract[0].From != "teleport" || step.Extract[0].Path != "$.items[*].id" {
		t.Errorf("the parser silently repaired an extraction: %#v", step.Extract[0])
	}
	if step.Assert[0].Type != "duration" || step.Assert[0].Equals != "fast" {
		t.Errorf("the parser silently repaired an assertion: %#v", step.Assert[0])
	}
}

func TestParseFlowsAcceptsNothingItCannotRead(t *testing.T) {
	for _, raw := range []interface{}{nil, "flows", 7, map[string]interface{}{"id": "x"}} {
		if flows := ParseFlows(raw); flows != nil {
			t.Errorf("ParseFlows(%#v) = %#v, want nil", raw, flows)
		}
	}
}
