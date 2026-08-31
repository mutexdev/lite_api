package core

// The flow runner.
//
// IT RUNS THE APP'S OWN ENGINE, ONE STEP AT A TIME. Every step goes through
// sendRequestWithControlsContextProvenance (app_send.go) — the same function
// SendRequest and run_request call — so a flow step is a UI Send in every
// respect that matters: pre/post scripts, tests, TLS posture, client
// certificates, cookies, OAuth2 refresh, history and the response store all
// happen because this is literally the user's send path. That is what makes the
// document's promise ("the same flow runs identically from the app's Flow tab
// and from run_flow") true by construction rather than by inspection.
//
// FLOW SCOPE IS NOT THE ENVIRONMENT, and the asymmetry is the whole safety
// argument. A step's `vars` values are interpolated against flow scope ALONE —
// the declared inputs plus what earlier steps extracted — before they become
// overrides. So `{"token": "{{apiToken}}"}` in a step var does NOT resolve the
// environment's secret and paste it into a value the flow can read back; the
// braces travel through literally, and the only thing that ever resolves
// {{apiToken}} is the send path itself, at send time, inside LiteAPI. A flow can
// therefore aim a secret at a request without ever holding one.
//
// HOW OVERRIDES REACH THE REQUEST: as runner.Iteration.Data, the data-file row
// seam — exactly the seam run_request uses, for exactly the reasons
// mcpValidatedOverrides sets out at length. Precedence comes free (Data beats
// the environment and loses to a script's bru.setVar during the same send) and
// so does non-persistence (ApplyScriptVariableContextToState never writes the
// Data scope back), so "for this step only" is a property of the seam rather
// than a promise this file has to keep.
//
// LOCKING. Nothing here holds a.mu across a send. The plan is copied out under
// the lock once, and the loop below then touches only its own locals.

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/mutexdev/lite_api/internal/interp"
	"github.com/mutexdev/lite_api/internal/runner"
	"github.com/mutexdev/lite_api/internal/scripting"
	"github.com/mutexdev/lite_api/internal/types"
)

// flowStepGuard is called immediately before each step's request is sent, with
// the step's index, the request it is about to run and the overrides it will
// run with. A non-nil error stops the step and, with it, the flow.
//
// THIS IS THE SEAM THE MCP TIER USES. Phase 2's new-host guard (rule 4) asks
// the user before a secret is resolved into a request aimed at a host it has
// never been sent to; run_flow needs the same check per step, and it needs it
// before the send rather than after. Passing it in as a function rather than
// branching on "is this an agent run" keeps this file free of the MCP tier's
// vocabulary and keeps the guard testable on its own — a test can install a
// guard that refuses step 2 and observe that step 3 never runs, without an MCP
// server anywhere in the picture.
//
// A UI-initiated run passes nil: see RunFlow for why there is nothing to ask.
type flowStepGuard func(stepIndex int, requestID string, overrides map[string]string) error

// flowRunPlan is everything a run needs, copied out from under the state lock
// before the first request goes anywhere.
type flowRunPlan struct {
	collectionID  string
	environmentID string
	flow          types.Flow
}

// runFlowProvenance executes one flow. It is the flow root, and the ONLY way
// into it: a migration delegate under the old name (runFlow) carried unmigrated
// callers for one wave and was deleted once a grep proved it had none.
//
// THE ERROR AND THE RESULT ANSWER DIFFERENT QUESTIONS, and both are returned.
// A non-nil error means the run was REFUSED — unknown ids, a flow that no
// longer validates, a missing required input, a name that shadows a secret, or
// a guard that said no. A nil error with OK false means the flow RAN and failed
// its own checks: an assertion did not hold, an extraction found nothing, a
// request never reached its server. The second is an ordinary outcome a caller
// renders, not an exception, and collapsing the two would make "the flow says
// the API is broken" indistinguishable from "LiteAPI would not start the flow".
//
// The result is populated as far as the run got in both cases, so a guard that
// stops step 3 still hands back what steps 1 and 2 did.
//
// PROVENANCE IS AN ARGUMENT: see
// sendRequestWithControlsContextProvenance for why.
//
// THE FLOW'S OWN PROVENANCE REACHES EVERY STEP, unchanged and by hand. A flow is
// one execution: its steps share the policy (and therefore the execution
// overlay that carries a setVar from step 1 to step 3), and the alternative —
// each step reconstructing provenance from what it can see — is how a step ends
// up classified differently from the run it belongs to.
func (a *App) runFlowProvenance(ctx context.Context, prov sendProvenance, collectionID, flowID, environmentID string, inputs map[string]string, stepGuard flowStepGuard) (types.FlowRunResult, error) {
	// BEFORE THE PLAN, so an unprovenanced flow does not even read state.
	if err := mcpRequireSendProvenance(prov, "the flow runner"); err != nil {
		return types.FlowRunResult{}, err
	}
	ctx = mcpContextWithSendProvenance(ctx, prov)
	plan, err := a.flowRunPlan(collectionID, flowID, environmentID)
	if err != nil {
		return types.FlowRunResult{}, err
	}
	scope, err := flowInitialScope(plan.flow, inputs)
	if err != nil {
		return types.FlowRunResult{}, err
	}

	result := types.FlowRunResult{FlowID: plan.flow.ID, Steps: []types.FlowStepResult{}}
	for index, step := range plan.flow.Steps {
		a.emitFlowProgress(types.FlowProgress{
			CollectionID: plan.collectionID,
			FlowID:       plan.flow.ID,
			StepID:       step.ID,
			StepIndex:    index,
			StepCount:    len(plan.flow.Steps),
			State:        "running",
		})
		stepResult := types.FlowStepResult{StepID: step.ID, RequestID: step.RequestID}
		overrides := flowStepOverrides(step, scope)

		if stepGuard != nil {
			if guardErr := stepGuard(index, step.RequestID, overrides); guardErr != nil {
				stepResult.Error = guardErr.Error()
				result.Steps = append(result.Steps, stepResult)
				result.Error = fmt.Sprintf("step %q was not allowed to run: %s", step.ID, guardErr)
				a.emitFlowStepSettled(plan, step.ID, index, false)
				// The guard's own error is returned unwrapped so that a caller
				// matching on it — mcpserver.ErrDenied, say — still can.
				return result, guardErr
			}
		}

		// THE FLOW'S OWN PROVENANCE, passed through rather than re-derived: a
		// step of an agent-initiated flow is an agent-initiated send, and a step
		// of the user's own run is the user's own send.
		_, _, response, sendErr := a.sendRequestWithControlsContextProvenance(
			ctx, prov, plan.collectionID, step.RequestID, plan.environmentID, nil, nil,
			runner.Iteration{Data: overrides},
		)
		if sendErr != nil {
			stepResult.Error = sendErr.Error()
		} else if response == nil {
			stepResult.Error = "the step produced no response; this is a bug in LiteAPI, not something to retry"
		} else {
			stepResult.Status = response.Status
			stepResult.DurationMs = response.DurationMs
			switch {
			case response.Cancelled:
				stepResult.Error = "the flow was cancelled before this step completed"
			case response.Status == 0 && strings.TrimSpace(response.Error) != "":
				stepResult.Error = fmt.Sprintf("the request could not be completed: %s", response.Error)
			default:
				// ASSERTIONS BEFORE EXTRACTION, deliberately. A step that got a
				// 500 fails its `status equals 200` assertion with exactly that
				// sentence; run the other way round, the same step fails with
				// "$.data.store.id is not present in the response body", which
				// is true, useless, and blames the flow for the server's answer.
				stepResult.Assertions = flowEvaluateAssertions(step, *response)
				if detail, failed := flowFirstFailedAssertion(stepResult.Assertions); failed {
					stepResult.Error = fmt.Sprintf("assertion failed: %s", detail)
					break
				}
				extracted, extractErr := flowExtractValues(step, *response)
				if extractErr != nil {
					stepResult.Error = extractErr.Error()
					break
				}
				stepResult.Extracted = extracted
				for name, value := range extracted {
					scope[name] = value
				}
			}
		}

		passed := stepResult.Error == ""
		result.Steps = append(result.Steps, stepResult)
		a.emitFlowStepSettled(plan, step.ID, index, passed)
		if !passed {
			// FAIL-FAST. Later steps are not attempted, because a chain exists
			// precisely because step N+1 needs what step N produced: running it
			// anyway would send a request built from values nobody computed.
			result.Error = fmt.Sprintf("step %q failed: %s", step.ID, stepResult.Error)
			return result, nil
		}
	}

	result.OK = true
	result.Outputs = flowResolveOutputs(plan.flow, scope)
	return result, nil
}

// flowRunPlan validates the ids, re-validates the flow, and copies what the run
// needs out from under the state lock.
//
// THE FLOW IS VALIDATED AGAIN HERE, not only when it was authored. Collections
// are plain files the user owns and edits — that is why this app ships a
// watcher — so a flow can reach a run with a step naming a request that has
// since been deleted, or a path that was hand-typed into the YAML. Validating
// before the first request is what turns that into a message instead of a
// half-finished chain of real calls against a real API.
func (a *App) flowRunPlan(collectionID, flowID, environmentID string) (flowRunPlan, error) {
	collectionID = strings.TrimSpace(collectionID)
	flowID = strings.TrimSpace(flowID)
	environmentID = strings.TrimSpace(environmentID)
	if collectionID == "" || flowID == "" {
		return flowRunPlan{}, errors.New("collectionId and flowId are both required")
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return flowRunPlan{}, err
	}
	workspace, collection, err := a.findCollectionWithWorkspaceLocked(collectionID)
	if err != nil {
		return flowRunPlan{}, err
	}
	if environmentID != "" {
		found := false
		for _, environment := range collection.Environments {
			if environment.ID == environmentID {
				found = true
				break
			}
		}
		if !found {
			return flowRunPlan{}, fmt.Errorf("no environment with id %q in collection %q; omit environmentId to use the active one", environmentID, collectionID)
		}
	}

	index := -1
	for i := range collection.Flows {
		if collection.Flows[i].ID == flowID {
			index = i
			break
		}
	}
	if index < 0 {
		return flowRunPlan{}, fmt.Errorf("no flow with id %q in collection %q", flowID, collectionID)
	}
	flow := types.CloneFlow(collection.Flows[index])

	items := make([]types.RequestItem, 0, len(flow.Steps))
	for _, step := range flow.Steps {
		for i := range collection.Items {
			if collection.Items[i].ID == step.RequestID {
				items = append(items, collection.Items[i])
				break
			}
		}
	}
	globals := scripting.ActiveGlobalEnvironmentsForWorkspace(*workspace)
	secrets := flowRunSecretNames(globals, *collection, environmentID, items)
	// This is where the shadowing refusal lands at run start: validateFlow
	// checks every input, extract and step-var name against the secrets that
	// resolve for THIS run. The inversion argument is mcpValidatedOverrides's —
	// a name that cannot be read but can be written decides what the request
	// sends in place of the credential, and that is not a legitimate use of a
	// flow variable whether an agent or the user wrote it.
	//
	// uiFlowAuthoring HERE IS NOT A CLAIM ABOUT WHO WROTE THE FLOW. This is not
	// an authoring act at all: the flow is already stored, and a stored flow
	// carries no record of its author, so the one clause validateFlow conditions
	// on authorship (a step var whose VALUE reaches a secret) has nothing to
	// decide on and is skipped. Passing the agent form would refuse the flow
	// tier's own documented shape — the canonical POS chain aims {{apiToken}} at
	// a request through a step var — for the USER'S OWN runs as well as the
	// agent's, since this plan is shared. The MCP run tier screens step vars
	// itself, where the provenance is known (mcp_flows.go).
	if err := validateFlow(*collection, secrets, flow, uiFlowAuthoring()); err != nil {
		return flowRunPlan{}, err
	}

	return flowRunPlan{collectionID: collectionID, environmentID: environmentID, flow: flow}, nil
}

// flowInitialScope builds the flow's variable scope from its declared inputs.
//
// AN UNDECLARED INPUT IS AN ERROR, not a value quietly ignored. `storeCode` for
// `storeCoden` is the shape of typo both a person and an agent make, and a flow
// that accepts it runs every step against an empty value and reports whatever
// the API says about nothing.
func flowInitialScope(flow types.Flow, inputs map[string]string) (map[string]string, error) {
	declared := make(map[string]bool, len(flow.Inputs))
	for _, input := range flow.Inputs {
		declared[strings.TrimSpace(input.Name)] = true
	}
	scope := make(map[string]string, len(flow.Inputs))
	unknown := []string{}
	for name, value := range inputs {
		trimmed := strings.TrimSpace(name)
		if trimmed == "" {
			continue
		}
		if !declared[trimmed] {
			unknown = append(unknown, trimmed)
			continue
		}
		scope[trimmed] = value
	}
	if len(unknown) > 0 {
		return nil, fmt.Errorf("flow %q does not declare the input %s; its inputs are %s",
			flow.Name, flowJoinNames(flowSortedNames(unknown)), flowDeclaredInputList(flow))
	}
	missing := []string{}
	for _, input := range flow.Inputs {
		name := strings.TrimSpace(input.Name)
		if !input.Required {
			continue
		}
		if strings.TrimSpace(scope[name]) == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("flow %q requires the input %s", flow.Name, flowJoinNames(flowSortedNames(missing)))
	}
	return scope, nil
}

func flowDeclaredInputList(flow types.Flow) string {
	if len(flow.Inputs) == 0 {
		return "none"
	}
	names := make([]string, 0, len(flow.Inputs))
	for _, input := range flow.Inputs {
		names = append(names, strings.TrimSpace(input.Name))
	}
	return flowJoinNames(flowSortedNames(names))
}

// flowStepOverrides interpolates a step's vars against FLOW SCOPE ONLY.
//
// See the file header: a token that flow scope does not define is left verbatim,
// which is what stops a step var from resolving a secret. It is also what makes
// `{"query": "{{storeCode}}"}` work — storeCode is an input, so it resolves —
// while `{"token": "{{apiToken}}"}` stays the literal string "{{apiToken}}" and
// is resolved, if at all, by the send path against the environment.
func flowStepOverrides(step types.FlowStep, scope map[string]string) map[string]string {
	if len(step.Vars) == 0 {
		return nil
	}
	overrides := make(map[string]string, len(step.Vars))
	for name, value := range step.Vars {
		trimmed := strings.TrimSpace(name)
		if trimmed == "" {
			continue
		}
		overrides[trimmed] = interp.Interpolate(value, scope)
	}
	if len(overrides) == 0 {
		return nil
	}
	return overrides
}

// flowExtractValues pulls a step's declared values out of its response.
func flowExtractValues(step types.FlowStep, response Response) (map[string]string, error) {
	if len(step.Extract) == 0 {
		return nil, nil
	}
	extracted := make(map[string]string, len(step.Extract))
	for _, extract := range step.Extract {
		name := strings.TrimSpace(extract.Name)
		switch strings.ToLower(strings.TrimSpace(extract.From)) {
		case "status":
			extracted[name] = strconv.Itoa(response.Status)
		case "header":
			value, ok := flowResponseHeader(response, extract.Path)
			if !ok {
				return nil, fmt.Errorf("step %q could not extract %q: the response has no %s header", step.ID, name, extract.Path)
			}
			extracted[name] = value
		default:
			value, err := flowPathValue(response.Body, extract.Path)
			if err != nil {
				return nil, fmt.Errorf("step %q could not extract %q: %w", step.ID, name, err)
			}
			extracted[name] = value
		}
	}
	return extracted, nil
}

// flowResponseHeader reads one header case-insensitively.
//
// HeaderEntries first when the response carries them: it is the ordered list
// with duplicates preserved, while the Headers map has already collapsed them.
// The FIRST match wins, which is the only answer that makes sense for a value
// about to become a single flow variable.
func flowResponseHeader(response Response, name string) (string, bool) {
	wanted := strings.ToLower(strings.TrimSpace(name))
	if wanted == "" {
		return "", false
	}
	for _, row := range response.HeaderEntries {
		if strings.ToLower(strings.TrimSpace(row.Name)) == wanted {
			return row.Value, true
		}
	}
	for key, value := range response.Headers {
		if strings.ToLower(strings.TrimSpace(key)) == wanted {
			return value, true
		}
	}
	return "", false
}

// flowEvaluateAssertions checks every assertion on the step.
//
// EVERY one, not up to the first failure: the run report shows what held as
// well as what did not, which is the difference between "the status was fine
// but the body was wrong" and a single red line. The flow still stops — see the
// caller — but it stops with the whole picture of the step that broke it.
func flowEvaluateAssertions(step types.FlowStep, response Response) []types.FlowAssertResult {
	if len(step.Assert) == 0 {
		return nil
	}
	results := make([]types.FlowAssertResult, 0, len(step.Assert))
	for _, assertion := range step.Assert {
		if strings.EqualFold(strings.TrimSpace(assertion.Type), "status") {
			results = append(results, flowEvaluateStatusAssert(assertion, response))
			continue
		}
		results = append(results, flowEvaluateBodyAssert(assertion, response))
	}
	return results
}

func flowEvaluateStatusAssert(assertion types.FlowAssert, response Response) types.FlowAssertResult {
	if assertion.Equals != nil {
		wanted, ok := flowStatusEquals(assertion.Equals)
		if !ok {
			return types.FlowAssertResult{Detail: "status equals is not a number"}
		}
		if response.Status != wanted {
			return types.FlowAssertResult{Detail: fmt.Sprintf("status equals %d, but the response status was %d", wanted, response.Status)}
		}
		if len(assertion.In) == 0 {
			return types.FlowAssertResult{OK: true, Detail: fmt.Sprintf("status equals %d", wanted)}
		}
	}
	if len(assertion.In) > 0 {
		for _, candidate := range assertion.In {
			if candidate == response.Status {
				return types.FlowAssertResult{OK: true, Detail: fmt.Sprintf("status %d is in %s", response.Status, flowIntList(assertion.In))}
			}
		}
		return types.FlowAssertResult{Detail: fmt.Sprintf("status is in %s, but the response status was %d", flowIntList(assertion.In), response.Status)}
	}
	return types.FlowAssertResult{Detail: "status assertion says nothing about the status"}
}

func flowEvaluateBodyAssert(assertion types.FlowAssert, response Response) types.FlowAssertResult {
	value, err := flowPathValue(response.Body, assertion.Path)
	if err != nil {
		// A path that does not resolve is a FAILED ASSERTION, not a broken
		// flow: `exists` is asking exactly this question, and `equals` on a
		// missing path is answered "no" rather than "I could not tell".
		return types.FlowAssertResult{Detail: err.Error()}
	}
	if assertion.Equals != nil {
		wanted, renderErr := flowRenderValue(assertion.Equals)
		if renderErr != nil {
			return types.FlowAssertResult{Detail: fmt.Sprintf("%s equals: the expected value could not be read: %v", assertion.Path, renderErr)}
		}
		if value != wanted {
			return types.FlowAssertResult{Detail: fmt.Sprintf("%s equals %q, but it was %q", assertion.Path, wanted, value)}
		}
	}
	if contains := assertion.Contains; strings.TrimSpace(contains) != "" {
		if !strings.Contains(value, contains) {
			return types.FlowAssertResult{Detail: fmt.Sprintf("%s contains %q, but it was %q", assertion.Path, contains, value)}
		}
	}
	return types.FlowAssertResult{OK: true, Detail: flowBodyAssertPassDetail(assertion, value)}
}

func flowBodyAssertPassDetail(assertion types.FlowAssert, value string) string {
	switch {
	case assertion.Equals != nil:
		return fmt.Sprintf("%s equals %q", assertion.Path, value)
	case strings.TrimSpace(assertion.Contains) != "":
		return fmt.Sprintf("%s contains %q", assertion.Path, assertion.Contains)
	default:
		return fmt.Sprintf("%s exists", assertion.Path)
	}
}

func flowFirstFailedAssertion(results []types.FlowAssertResult) (string, bool) {
	for _, result := range results {
		if !result.OK {
			return result.Detail, true
		}
	}
	return "", false
}

// flowStatusEquals reads an authored status, which arrives as an int from the
// UI, an int from YAML and a float64 from JSON depending on which door the flow
// came through.
func flowStatusEquals(raw interface{}) (int, bool) {
	switch typed := raw.(type) {
	case int:
		return typed, true
	case int32:
		return int(typed), true
	case int64:
		return int(typed), true
	case float64:
		if typed != float64(int(typed)) {
			return 0, false
		}
		return int(typed), true
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(typed))
		if err != nil {
			return 0, false
		}
		return parsed, true
	}
	return 0, false
}

func flowIntList(values []int) string {
	parts := make([]string, 0, len(values))
	for _, value := range values {
		parts = append(parts, strconv.Itoa(value))
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// flowResolveOutputs interpolates the flow's declared outputs against the final
// scope. Only a run that completed has them: a flow that stopped at step 2 has
// no honest value for an output built from step 3.
func flowResolveOutputs(flow types.Flow, scope map[string]string) map[string]string {
	if len(flow.Outputs) == 0 {
		return nil
	}
	outputs := make(map[string]string, len(flow.Outputs))
	for _, output := range flow.Outputs {
		outputs[strings.TrimSpace(output.Name)] = interp.Interpolate(output.Value, scope)
	}
	return outputs
}

func (a *App) emitFlowStepSettled(plan flowRunPlan, stepID string, index int, passed bool) {
	state := "failed"
	if passed {
		state = "passed"
	}
	a.emitFlowProgress(types.FlowProgress{
		CollectionID: plan.collectionID,
		FlowID:       plan.flow.ID,
		StepID:       stepID,
		StepIndex:    index,
		StepCount:    len(plan.flow.Steps),
		State:        state,
	})
}

// emitFlowProgress pushes one "flow:progress" event.
//
// The same shape as pushNotification and the live-session pushes: a test seam
// first, because wailsruntime.EventsEmit dereferences a Wails context that no
// test has, and a nil-context guard after it, because a.ctx is nil until Wails
// calls startup.
func (a *App) emitFlowProgress(progress types.FlowProgress) {
	if a.flowProgressEmit != nil {
		a.flowProgressEmit(progress)
		return
	}
	if a.ctx == nil {
		return
	}
	wailsruntime.EventsEmit(a.ctx, "flow:progress", progress)
}
