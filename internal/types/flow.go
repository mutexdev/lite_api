// Flows: LiteAPI's native multi-step request chains.
//
// A Flow is a first-class entity stored alongside requests in the collection —
// an ordered chain of requests with data wiring, assertions and declared
// outputs. See docs/mcp-agent-interface.md, "Flows", for the canonical example
// and the semantics these types encode.
//
// THE SCHEMA IS THE WIRE FORMAT, in three directions at once: it is what the
// UI's Flow tab edits, what opencollection.yml / bruno.json persist, and what
// the MCP `get_flow` / `create_flow` tools exchange with an agent. The JSON tags
// are therefore load-bearing rather than incidental, and match the document's
// example byte for byte.
package types

// Flow is one named chain of steps.
type Flow struct {
	ID          string       `json:"id"`
	Name        string       `json:"name"`
	Description string       `json:"description,omitempty"`
	Inputs      []FlowInput  `json:"inputs,omitempty"`
	Steps       []FlowStep   `json:"steps"`
	Outputs     []FlowOutput `json:"outputs,omitempty"`
}

// FlowInput declares a value the caller supplies when the flow runs.
type FlowInput struct {
	Name        string `json:"name"`
	Required    bool   `json:"required,omitempty"`
	Description string `json:"description,omitempty"`
}

// FlowStep is one request in the chain.
//
// Vars are flow-scoped variable overrides for THIS step's resolution: their
// values are interpolated against flow scope (the inputs plus everything
// earlier steps extracted) and never against the environment, so a step var
// cannot reach a secret. See core.runFlowProvenance for why that asymmetry is
// the point.
type FlowStep struct {
	ID        string            `json:"id"`
	RequestID string            `json:"requestId"`
	Vars      map[string]string `json:"vars,omitempty"`
	Extract   []FlowExtract     `json:"extract,omitempty"`
	Assert    []FlowAssert      `json:"assert,omitempty"`
}

// FlowExtract pulls one value out of a step's response into flow scope.
//
// From is "body" (Path is a JSONPath subset — see core/flowpath.go), "header"
// (Path is the header name) or "status" (Path is unused).
type FlowExtract struct {
	Name string `json:"name"`
	From string `json:"from"`
	Path string `json:"path,omitempty"`
}

// FlowAssert is one check against a step's response.
//
// Type is "status" (Equals or In) or "body" (Path plus Equals, Contains or
// Exists).
//
// EQUALS IS UNTYPED because the schema it mirrors is: `{"type":"status",
// "equals":200}` and `{"type":"body","equals":"created"}` are both legal, and
// the document's example uses each. It is compared by rendering both sides the
// way the path evaluator renders a result, so a status of 200 matches an
// authored 200 whether YAML decoded it as an int or JSON as a float64. Same
// reason types.Variable.Value is untyped.
type FlowAssert struct {
	Type     string      `json:"type"`
	Equals   interface{} `json:"equals,omitempty"`
	In       []int       `json:"in,omitempty"`
	Path     string      `json:"path,omitempty"`
	Contains string      `json:"contains,omitempty"`
	Exists   bool        `json:"exists,omitempty"`
}

// FlowOutput names what the flow hands back to its caller. Value is
// interpolated against the final flow scope.
type FlowOutput struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// FlowRunResult is one execution of a flow.
//
// OK is the single question a caller asks first: every step ran, every
// assertion held, every extraction found its path. Steps is as complete as the
// run got — fail-fast means a flow that failed at step 2 carries two step
// results and not three, which is itself the report that step 3 never ran.
type FlowRunResult struct {
	FlowID  string            `json:"flowId"`
	OK      bool              `json:"ok"`
	Steps   []FlowStepResult  `json:"steps"`
	Outputs map[string]string `json:"outputs,omitempty"`
	Error   string            `json:"error,omitempty"`
}

// FlowStepResult is one step's outcome.
type FlowStepResult struct {
	StepID     string             `json:"stepId"`
	RequestID  string             `json:"requestId"`
	Status     int                `json:"status"`
	DurationMs int64              `json:"durationMs"`
	Extracted  map[string]string  `json:"extracted,omitempty"`
	Assertions []FlowAssertResult `json:"assertions,omitempty"`
	Error      string             `json:"error,omitempty"`
}

// FlowAssertResult reports one assertion. Detail reads the same whether it
// passed or failed, so a run log can show every check rather than only the
// broken one.
type FlowAssertResult struct {
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
}

// FlowProgress is the "flow:progress" event payload, emitted once as each step
// starts and once as it settles, so the UI can render a run while it happens
// rather than only when it returns.
//
// State is "running", "passed" or "failed".
type FlowProgress struct {
	CollectionID string `json:"collectionId"`
	FlowID       string `json:"flowId"`
	StepID       string `json:"stepId"`
	StepIndex    int    `json:"stepIndex"`
	StepCount    int    `json:"stepCount"`
	State        string `json:"state"`
}

// CloneFlows deep-copies a flow list.
//
// Flows carry maps and slices, and they are copied out from under the state
// lock before a run touches the network. A shallow copy would leave the runner
// reading a map the UI can mutate mid-run — the same aliasing rule the
// CloneVariables family exists for.
func CloneFlows(values []Flow) []Flow {
	if values == nil {
		return nil
	}
	out := make([]Flow, 0, len(values))
	for _, value := range values {
		out = append(out, CloneFlow(value))
	}
	return out
}

// CloneFlow deep-copies one flow.
func CloneFlow(flow Flow) Flow {
	clone := flow
	if flow.Inputs != nil {
		clone.Inputs = append([]FlowInput(nil), flow.Inputs...)
	}
	if flow.Outputs != nil {
		clone.Outputs = append([]FlowOutput(nil), flow.Outputs...)
	}
	if flow.Steps != nil {
		clone.Steps = make([]FlowStep, 0, len(flow.Steps))
		for _, step := range flow.Steps {
			stepClone := step
			if step.Vars != nil {
				stepClone.Vars = make(map[string]string, len(step.Vars))
				for name, value := range step.Vars {
					stepClone.Vars[name] = value
				}
			}
			if step.Extract != nil {
				stepClone.Extract = append([]FlowExtract(nil), step.Extract...)
			}
			if step.Assert != nil {
				stepClone.Assert = make([]FlowAssert, 0, len(step.Assert))
				for _, assertion := range step.Assert {
					assertClone := assertion
					if assertion.In != nil {
						assertClone.In = append([]int(nil), assertion.In...)
					}
					stepClone.Assert = append(stepClone.Assert, assertClone)
				}
			}
			clone.Steps = append(clone.Steps, stepClone)
		}
	}
	return clone
}
