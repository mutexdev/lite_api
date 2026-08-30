package yamlstore

// The paired reader and writer for a collection's flows.
//
// ONE SHAPE SERVES BOTH FORMATS. Flows live in the collection's root config
// file — `flows:` in opencollection.yml, `"flows"` in bruno.json — and the maps
// this file builds marshal identically through yaml.Marshal and
// json.MarshalIndent. That is the same arrangement yamlProxyConfig /
// JSONProxyConfig arrived at, and it is why there is one writer here rather
// than two that have to be kept in step.
//
// FLOWS ARE NOT A BRUNO CONCEPT. They are LiteAPI-native, which is exactly why
// they go in the root config rather than into per-request files: a Bruno or
// OpenCollection reader that does not know the key ignores it and the rest of
// the collection still reads, while a flow written into a .bru request file
// would corrupt a file another tool owns.

import (
	"strings"

	"github.com/mutexdev/lite_api/internal/scalar"
	"github.com/mutexdev/lite_api/internal/types"
)

// YAMLFlows renders flows for a collection's root config file.
//
// Empty collections are written as absent rather than as an empty list: a
// collection with no flows should look on disk exactly as it did before flows
// existed, so that adopting this feature adds no diff to anyone's working tree
// until they author one.
func YAMLFlows(flows []types.Flow) []map[string]interface{} {
	result := make([]map[string]interface{}, 0, len(flows))
	for _, flow := range flows {
		entry := map[string]interface{}{
			"id":   flow.ID,
			"name": flow.Name,
		}
		if strings.TrimSpace(flow.Description) != "" {
			entry["description"] = flow.Description
		}
		if len(flow.Inputs) > 0 {
			inputs := make([]map[string]interface{}, 0, len(flow.Inputs))
			for _, input := range flow.Inputs {
				row := map[string]interface{}{"name": input.Name}
				if input.Required {
					row["required"] = true
				}
				if strings.TrimSpace(input.Description) != "" {
					row["description"] = input.Description
				}
				inputs = append(inputs, row)
			}
			entry["inputs"] = inputs
		}
		steps := make([]map[string]interface{}, 0, len(flow.Steps))
		for _, step := range flow.Steps {
			row := map[string]interface{}{
				"id":        step.ID,
				"requestId": step.RequestID,
			}
			if len(step.Vars) > 0 {
				vars := make(map[string]interface{}, len(step.Vars))
				for name, value := range step.Vars {
					vars[name] = value
				}
				row["vars"] = vars
			}
			if len(step.Extract) > 0 {
				extracts := make([]map[string]interface{}, 0, len(step.Extract))
				for _, extract := range step.Extract {
					extractRow := map[string]interface{}{
						"name": extract.Name,
						"from": extract.From,
					}
					if strings.TrimSpace(extract.Path) != "" {
						extractRow["path"] = extract.Path
					}
					extracts = append(extracts, extractRow)
				}
				row["extract"] = extracts
			}
			if len(step.Assert) > 0 {
				assertions := make([]map[string]interface{}, 0, len(step.Assert))
				for _, assertion := range step.Assert {
					assertRow := map[string]interface{}{"type": assertion.Type}
					if assertion.Equals != nil {
						assertRow["equals"] = assertion.Equals
					}
					if len(assertion.In) > 0 {
						assertRow["in"] = assertion.In
					}
					if strings.TrimSpace(assertion.Path) != "" {
						assertRow["path"] = assertion.Path
					}
					if strings.TrimSpace(assertion.Contains) != "" {
						assertRow["contains"] = assertion.Contains
					}
					if assertion.Exists {
						assertRow["exists"] = true
					}
					assertions = append(assertions, assertRow)
				}
				row["assert"] = assertions
			}
			steps = append(steps, row)
		}
		entry["steps"] = steps
		if len(flow.Outputs) > 0 {
			outputs := make([]map[string]interface{}, 0, len(flow.Outputs))
			for _, output := range flow.Outputs {
				outputs = append(outputs, map[string]interface{}{"name": output.Name, "value": output.Value})
			}
			entry["outputs"] = outputs
		}
		result = append(result, entry)
	}
	return result
}

// JSONFlows is YAMLFlows for the bruno.json side of the same collection.
func JSONFlows(flows []types.Flow) []map[string]interface{} {
	return YAMLFlows(flows)
}

// ParseFlows reads flows back from either format.
//
// A row with no steps is dropped rather than surfaced: it cannot run, and a
// half-written flow in a hand-edited file should not stop the whole collection
// from loading. Everything else that is merely odd — an unknown `from`, a path
// that does not parse — is KEPT, so that core.validateFlow can explain it in
// the app rather than the file silently losing what the user typed.
func ParseFlows(raw interface{}) []types.Flow {
	values, ok := scalar.ListValue(raw)
	if !ok {
		return nil
	}
	flows := make([]types.Flow, 0, len(values))
	for _, value := range values {
		valueMap, ok := scalar.Map(value)
		if !ok {
			continue
		}
		flow := types.Flow{
			ID:          scalar.FirstYAMLString(valueMap, "id"),
			Name:        scalar.FirstYAMLString(valueMap, "name"),
			Description: scalar.FirstYAMLString(valueMap, "description"),
		}
		if inputs, ok := scalar.ListValue(valueMap["inputs"]); ok {
			for _, inputValue := range inputs {
				inputMap, ok := scalar.Map(inputValue)
				if !ok {
					continue
				}
				name := scalar.FirstYAMLString(inputMap, "name")
				if strings.TrimSpace(name) == "" {
					continue
				}
				flow.Inputs = append(flow.Inputs, types.FlowInput{
					Name:        name,
					Required:    scalar.BoolValue(inputMap["required"], false),
					Description: scalar.FirstYAMLString(inputMap, "description"),
				})
			}
		}
		if steps, ok := scalar.ListValue(valueMap["steps"]); ok {
			for _, stepValue := range steps {
				stepMap, ok := scalar.Map(stepValue)
				if !ok {
					continue
				}
				step := types.FlowStep{
					ID:        scalar.FirstYAMLString(stepMap, "id"),
					RequestID: scalar.FirstYAMLString(stepMap, "requestId", "requestID", "request_id"),
				}
				if vars, ok := scalar.Map(stepMap["vars"]); ok && len(vars) > 0 {
					step.Vars = make(map[string]string, len(vars))
					for name, varValue := range vars {
						step.Vars[name] = scalar.YAMLString(varValue)
					}
				}
				if extracts, ok := scalar.ListValue(stepMap["extract"]); ok {
					for _, extractValue := range extracts {
						extractMap, ok := scalar.Map(extractValue)
						if !ok {
							continue
						}
						step.Extract = append(step.Extract, types.FlowExtract{
							Name: scalar.FirstYAMLString(extractMap, "name"),
							From: scalar.FirstYAMLString(extractMap, "from"),
							Path: scalar.FirstYAMLString(extractMap, "path"),
						})
					}
				}
				if assertions, ok := scalar.ListValue(stepMap["assert"]); ok {
					for _, assertValue := range assertions {
						assertMap, ok := scalar.Map(assertValue)
						if !ok {
							continue
						}
						assertion := types.FlowAssert{
							Type:     scalar.FirstYAMLString(assertMap, "type"),
							Equals:   normalizeFlowEquals(assertMap["equals"]),
							Path:     scalar.FirstYAMLString(assertMap, "path"),
							Contains: scalar.FirstYAMLString(assertMap, "contains"),
							Exists:   scalar.BoolValue(assertMap["exists"], false),
						}
						if candidates, ok := scalar.ListValue(assertMap["in"]); ok {
							for _, candidate := range candidates {
								if parsed, ok := scalar.IntValueOK(candidate); ok {
									assertion.In = append(assertion.In, parsed)
								}
							}
						}
						step.Assert = append(step.Assert, assertion)
					}
				}
				flow.Steps = append(flow.Steps, step)
			}
		}
		if len(flow.Steps) == 0 {
			continue
		}
		if outputs, ok := scalar.ListValue(valueMap["outputs"]); ok {
			for _, outputValue := range outputs {
				outputMap, ok := scalar.Map(outputValue)
				if !ok {
					continue
				}
				name := scalar.FirstYAMLString(outputMap, "name")
				if strings.TrimSpace(name) == "" {
					continue
				}
				flow.Outputs = append(flow.Outputs, types.FlowOutput{
					Name:  name,
					Value: scalar.YAMLString(outputMap["value"]),
				})
			}
		}
		flows = append(flows, flow)
	}
	if len(flows) == 0 {
		return nil
	}
	return flows
}

// normalizeFlowEquals collapses the several number shapes an `equals` arrives
// in onto two: an int for whole numbers, a string for everything else.
//
// The field is untyped because the schema is (a status equals 200, a body
// equals "created"), and the decoders disagree about numbers — YAML gives an
// int, JSON a float64, a hand-edited file either. Normalising on the way IN is
// what makes a flow round-trip to the same value it was saved with, so a
// reload does not rewrite the file with 200 spelled differently.
func normalizeFlowEquals(raw interface{}) interface{} {
	if raw == nil {
		return nil
	}
	switch typed := raw.(type) {
	case bool:
		return typed
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		if typed == float64(int(typed)) {
			return int(typed)
		}
		return typed
	case string:
		return typed
	}
	return scalar.YAMLString(raw)
}
