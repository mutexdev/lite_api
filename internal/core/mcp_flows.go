package core

// The flow tier of the MCP agent interface: list_flows, get_flow, run_flow.
//
// TWO HALVES, TWO SETS OF RULES. list_flows and get_flow are read-tier work and
// obey mcp_backend.go's rules to the letter: copy out from under the state lock,
// report AS AUTHORED, resolve nothing. That is not a convention here but the
// safety argument for get_flow specifically — a step var written as
// {"token":"{{apiToken}}"} must arrive at the agent as those literal braces,
// because flow scope never resolves it (see flow_run.go's header) and resolving
// it here would invent the leak the runner is built to avoid.
//
// run_flow is run-tier work and reuses Phase 2 verbatim. The runner takes a
// flowStepGuard — a seam built for this file — and what this file installs in it
// is the same pair of calls RunRequest makes, once per step: mcpRunPlan to copy
// the step's request and work out which secrets are in scope, then
// enforceMCPHostGuard to decide whether the credential may go where this step
// would send it. PER STEP rather than once for the flow, because a chain's steps
// are different requests aimed at different hosts, and a guard that checked only
// the first would wave through every later one.
//
// WHAT THE MASK COVERS, and why it is wider than run_request's. A flow reads
// values OUT of live responses — that is what extract is — and a server can echo
// a credential back into its body. So an extracted value, an assertion detail
// quoting a body value, a step error quoting one, and an output built from any
// of them can all carry a secret that no name-based rule would ever flag. All
// four go through MaskKnownSecretValues with the hydrated secret values fetched
// BEFORE the run, exactly as mcpRunResult does.

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mutexdev/lite_api/internal/mcpserver"
	"github.com/mutexdev/lite_api/internal/types"
)

// ListFlows returns one collection's flows in stored order.
func (b *mcpBackend) ListFlows(collectionID string) ([]mcpserver.FlowSummary, error) {
	flows, err := b.app.mcpCollectionFlows(collectionID)
	if err != nil {
		return nil, err
	}
	out := make([]mcpserver.FlowSummary, 0, len(flows))
	for _, flow := range flows {
		out = append(out, mcpFlowSummary(flow))
	}
	return out, nil
}

// GetFlow returns one flow's full definition, as authored.
func (b *mcpBackend) GetFlow(collectionID, flowID string) (mcpserver.FlowDetail, error) {
	flowID = strings.TrimSpace(flowID)
	if strings.TrimSpace(collectionID) == "" || flowID == "" {
		return mcpserver.FlowDetail{}, errors.New("collectionId and flowId are both required")
	}
	flows, err := b.app.mcpCollectionFlows(collectionID)
	if err != nil {
		return mcpserver.FlowDetail{}, err
	}
	for _, flow := range flows {
		if flow.ID != flowID {
			continue
		}
		return mcpserver.FlowDetail{
			FlowSummary: mcpFlowSummary(flow),
			Steps:       mcpFlowSteps(flow.Steps),
			Outputs:     mcpFlowOutputs(flow.Outputs),
		}, nil
	}
	return mcpserver.FlowDetail{}, fmt.Errorf("no flow with id %q in collection %q; call list_flows for the ids that exist", flowID, strings.TrimSpace(collectionID))
}

// RunFlow executes one flow with the new-host guard enforced before every step.
//
// THE OUTCOME AND THE ERROR ANSWER DIFFERENT QUESTIONS, and both are returned —
// the same split runFlow itself draws. A non-nil error means the run was
// REFUSED (unknown ids, an undeclared input, a missing required one, a guard
// that said no); a nil error with OK false means the flow RAN and failed its own
// checks. The outcome is populated as far as the run got either way, so a caller
// in this package can see that step 1 completed even when step 2 was denied. An
// MCP client sees only the error text for a refusal — that is mcpserver's split
// between a result and an isError — which is why the guard's message has to be
// self-contained, and it is (mcp_guard.go writes it for the agent that reads it).
//
// The guard's error is returned with errors.Is(err, mcpserver.ErrDenied) still
// holding, so the audit records "denied" rather than "error".
func (b *mcpBackend) RunFlow(ctx context.Context, params mcpserver.RunFlowParams) (mcpserver.FlowRunOutcome, error) {
	// Fetched BEFORE the run, so the mapping below cannot forget to scrub and
	// cannot be handed a value that was written to the environment mid-run.
	// Same argument as RunRequest's.
	secretValues, err := b.app.mcpHydratedSecretValues()
	if err != nil {
		return mcpserver.FlowRunOutcome{}, err
	}

	guard := func(_ int, requestID string, overrides map[string]string) error {
		// The step's own plan: its effective request, its resolved variable
		// scope, and the secrets in scope for it. Nothing here is flow-specific —
		// it is the same copy-out RunRequest does, which is the point, because
		// "what the guard checked" must not drift between the two tiers.
		plan, planErr := b.app.mcpRunPlan(params.CollectionID, requestID, params.EnvironmentID)
		if planErr != nil {
			return planErr
		}
		// The step's vars arrive here already interpolated against flow scope,
		// and they are what the guard resolves the target host WITH — so a step
		// that retargets {{baseUrl}} is caught exactly as a run_request override
		// would be.
		return b.app.enforceMCPHostGuard(ctx, plan, overrides)
	}

	result, runErr := b.app.runFlow(ctx, params.CollectionID, params.FlowID, params.EnvironmentID, params.Inputs, guard)
	outcome := mcpFlowRunOutcome(result, secretValues)
	if runErr != nil {
		// A refusal names variables and hosts, never values — but it is masked
		// anyway. The scrub is free, and the one thing that must not happen is a
		// path where a message reaches an agent without passing through it.
		return outcome, mcpMaskedError(runErr, secretValues)
	}
	return outcome, nil
}

// mcpCollectionFlows copies one collection's flows out from under the state
// lock. The clone is types.CloneFlows because a flow carries maps and slices and
// nothing that leaves the lock may alias state — the rule the top of
// mcp_backend.go states for every method here.
func (a *App) mcpCollectionFlows(collectionID string) ([]types.Flow, error) {
	collectionID = strings.TrimSpace(collectionID)
	if collectionID == "" {
		return nil, errors.New("collectionId is required")
	}
	var flows []types.Flow
	found := false
	if err := a.readStateForMCP(func(state *AppState) {
		for wi := range state.Workspaces {
			for ci := range state.Workspaces[wi].Collections {
				collection := &state.Workspaces[wi].Collections[ci]
				if collection.ID != collectionID {
					continue
				}
				found = true
				flows = types.CloneFlows(collection.Flows)
				return
			}
		}
	}); err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("no collection with id %q; call list_collections for the ids that exist", collectionID)
	}
	return flows, nil
}

func mcpFlowSummary(flow types.Flow) mcpserver.FlowSummary {
	return mcpserver.FlowSummary{
		ID:          flow.ID,
		Name:        flow.Name,
		Description: flow.Description,
		StepCount:   len(flow.Steps),
		Inputs:      mcpFlowInputs(flow.Inputs),
	}
}

func mcpFlowInputs(inputs []types.FlowInput) []mcpserver.FlowInput {
	if len(inputs) == 0 {
		return nil
	}
	out := make([]mcpserver.FlowInput, 0, len(inputs))
	for _, input := range inputs {
		out = append(out, mcpserver.FlowInput{
			Name:        input.Name,
			Required:    input.Required,
			Description: input.Description,
		})
	}
	return out
}

// mcpFlowSteps narrows each step to the contract's fields. The narrowing is the
// same argument mcpKeyValueRows makes: there is no field on mcpserver.FlowStep
// that anything else could ride along in, and vars are copied verbatim because
// verbatim is what makes {{apiToken}} arrive unresolved.
func mcpFlowSteps(steps []types.FlowStep) []mcpserver.FlowStep {
	out := make([]mcpserver.FlowStep, 0, len(steps))
	for _, step := range steps {
		row := mcpserver.FlowStep{
			ID:        step.ID,
			RequestID: step.RequestID,
			Extract:   mcpFlowExtracts(step.Extract),
			Assert:    mcpFlowAsserts(step.Assert),
		}
		if len(step.Vars) > 0 {
			row.Vars = make(map[string]string, len(step.Vars))
			for name, value := range step.Vars {
				row.Vars[name] = value
			}
		}
		out = append(out, row)
	}
	return out
}

func mcpFlowExtracts(extracts []types.FlowExtract) []mcpserver.FlowExtract {
	if len(extracts) == 0 {
		return nil
	}
	out := make([]mcpserver.FlowExtract, 0, len(extracts))
	for _, extract := range extracts {
		out = append(out, mcpserver.FlowExtract{Name: extract.Name, From: extract.From, Path: extract.Path})
	}
	return out
}

func mcpFlowAsserts(assertions []types.FlowAssert) []mcpserver.FlowAssert {
	if len(assertions) == 0 {
		return nil
	}
	out := make([]mcpserver.FlowAssert, 0, len(assertions))
	for _, assertion := range assertions {
		row := mcpserver.FlowAssert{
			Type:     assertion.Type,
			Equals:   assertion.Equals,
			Path:     assertion.Path,
			Contains: assertion.Contains,
			Exists:   assertion.Exists,
		}
		if assertion.In != nil {
			row.In = append([]int(nil), assertion.In...)
		}
		out = append(out, row)
	}
	return out
}

func mcpFlowOutputs(outputs []types.FlowOutput) []mcpserver.FlowOutput {
	if len(outputs) == 0 {
		return nil
	}
	out := make([]mcpserver.FlowOutput, 0, len(outputs))
	for _, output := range outputs {
		out = append(out, mcpserver.FlowOutput{Name: output.Name, Value: output.Value})
	}
	return out
}

// mcpFlowRunOutcome maps a run report onto the contract, masking every field a
// live response could have reached.
func mcpFlowRunOutcome(result types.FlowRunResult, secretValues []string) mcpserver.FlowRunOutcome {
	outcome := mcpserver.FlowRunOutcome{
		OK: result.OK,
		// The top-level error quotes the failing step's own error, which quotes
		// an assertion detail, which quotes a body value. Three hops from a
		// response body, and every one of them is why this is masked.
		Error: mcpserver.MaskKnownSecretValues(result.Error, secretValues),
		Steps: make([]mcpserver.FlowStepOutcome, 0, len(result.Steps)),
	}
	for _, step := range result.Steps {
		row := mcpserver.FlowStepOutcome{
			StepID:     step.StepID,
			RequestID:  step.RequestID,
			Status:     step.Status,
			DurationMs: int(step.DurationMs),
			Error:      mcpserver.MaskKnownSecretValues(step.Error, secretValues),
		}
		if len(step.Extracted) > 0 {
			// THE FIELD THIS TIER EXISTS TO MASK. An extraction is a value read
			// out of a live response body by JSONPath: an endpoint that echoes
			// the credential it was called with puts a real secret here under
			// whatever name the flow chose, and only an exact-value match finds
			// it.
			row.Extracted = make(map[string]string, len(step.Extracted))
			for name, value := range step.Extracted {
				row.Extracted[name] = mcpserver.MaskKnownSecretValues(value, secretValues)
			}
		}
		for _, assertion := range step.Assertions {
			row.Assertions = append(row.Assertions, mcpserver.FlowAssertionOutcome{
				OK: assertion.OK,
				// A failed body assertion renders the value it actually found.
				Detail: mcpserver.MaskKnownSecretValues(assertion.Detail, secretValues),
			})
		}
		outcome.Steps = append(outcome.Steps, row)
	}
	if len(result.Outputs) > 0 {
		outcome.Outputs = make(map[string]string, len(result.Outputs))
		for name, value := range result.Outputs {
			// An output is an interpolation of flow scope, and flow scope is
			// where extractions land, so it inherits their exposure.
			outcome.Outputs[name] = mcpserver.MaskKnownSecretValues(value, secretValues)
		}
	}
	return outcome
}

// mcpMaskedError scrubs an error's TEXT while keeping the error itself
// matchable.
//
// The wrapper is what makes both halves possible at once: errors.Is still walks
// through to mcpserver.ErrDenied, so the protocol layer audits a denial as
// "denied", while the message the agent reads has been through the same scrub as
// every other string this tier returns. Building a new error with fmt.Errorf
// would preserve the chain too, but only by prefixing text onto a message the
// guard wrote deliberately for its reader.
func mcpMaskedError(err error, secretValues []string) error {
	if err == nil {
		return nil
	}
	masked := mcpserver.MaskKnownSecretValues(err.Error(), secretValues)
	if masked == err.Error() {
		return err
	}
	return maskedError{cause: err, message: masked}
}

type maskedError struct {
	cause   error
	message string
}

func (e maskedError) Error() string { return e.message }

func (e maskedError) Unwrap() error { return e.cause }
