package core

// Flow CRUD and the one validator everything shares.
//
// VALIDATION HAS EXACTLY ONE IMPLEMENTATION, validateFlow, and both authoring
// paths go through it: the UI's Create/UpdateFlow bindings today, and the MCP
// write tier's create_flow/update_flow later. That is the reason it takes a
// collection and a secret-name set rather than reading app state itself — a
// validator that reached for a.state could not be called from the MCP backend's
// copy-out-then-work shape, and a second implementation there would drift into
// accepting flows the UI rejects, which is precisely the failure the write tier
// cannot afford.
//
// PERSISTENCE follows CreateEnvironment (app_environments.go): mutate the
// collection in state, write the collection's files, mark the state dirty. Both
// collection formats carry flows in their root config file — opencollection.yml
// for yml collections, bruno.json for the bru/json ones — so
// writeCollectionFilesLocked is all a flow needs to reach disk, and the US-015
// fingerprint gate applies to it like every other collection file.

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mutexdev/lite_api/internal/scripting"
	"github.com/mutexdev/lite_api/internal/types"
)

// CreateFlow adds a flow to a collection.
func (a *App) CreateFlow(collectionID string, flow types.Flow) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return AppState{}, err
	}
	workspace, collection, err := a.findCollectionWithWorkspaceLocked(collectionID)
	if err != nil {
		return AppState{}, err
	}
	flow = types.CloneFlow(flow)
	flow.ID = strings.TrimSpace(flow.ID)
	if flow.ID == "" {
		flow.ID = newID("flow")
	}
	for _, existing := range collection.Flows {
		if existing.ID == flow.ID {
			return AppState{}, fmt.Errorf("a flow with id %q already exists in collection %q; call UpdateFlow to change it", flow.ID, collectionID)
		}
	}
	if err := validateFlow(*collection, flowSecretNamesInScope(workspace, *collection), flow); err != nil {
		return AppState{}, err
	}
	collection.Flows = append(collection.Flows, flow)
	collection.UpdatedAt = time.Now()
	if err := a.writeCollectionFilesLocked(collection); err != nil {
		return AppState{}, err
	}
	a.notify("success", "Flow created: "+flow.Name)
	return a.state, a.markDirty(persistScopeState)
}

// UpdateFlow replaces a flow by id.
func (a *App) UpdateFlow(collectionID string, flow types.Flow) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return AppState{}, err
	}
	workspace, collection, err := a.findCollectionWithWorkspaceLocked(collectionID)
	if err != nil {
		return AppState{}, err
	}
	flow = types.CloneFlow(flow)
	flow.ID = strings.TrimSpace(flow.ID)
	if flow.ID == "" {
		return AppState{}, errors.New("flow id is required; call CreateFlow to add a new flow")
	}
	index := -1
	for i := range collection.Flows {
		if collection.Flows[i].ID == flow.ID {
			index = i
			break
		}
	}
	if index < 0 {
		return AppState{}, fmt.Errorf("no flow with id %q in collection %q", flow.ID, collectionID)
	}
	if err := validateFlow(*collection, flowSecretNamesInScope(workspace, *collection), flow); err != nil {
		return AppState{}, err
	}
	collection.Flows[index] = flow
	collection.UpdatedAt = time.Now()
	if err := a.writeCollectionFilesLocked(collection); err != nil {
		return AppState{}, err
	}
	a.notify("success", "Flow saved: "+flow.Name)
	return a.state, a.markDirty(persistScopeState)
}

// DeleteFlow removes a flow by id.
func (a *App) DeleteFlow(collectionID, flowID string) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return AppState{}, err
	}
	collection, err := a.findCollectionLocked(collectionID)
	if err != nil {
		return AppState{}, err
	}
	flowID = strings.TrimSpace(flowID)
	index := -1
	for i := range collection.Flows {
		if collection.Flows[i].ID == flowID {
			index = i
			break
		}
	}
	if index < 0 {
		return AppState{}, fmt.Errorf("no flow with id %q in collection %q", flowID, collectionID)
	}
	name := collection.Flows[index].Name
	collection.Flows = append(collection.Flows[:index], collection.Flows[index+1:]...)
	collection.UpdatedAt = time.Now()
	if err := a.writeCollectionFilesLocked(collection); err != nil {
		return AppState{}, err
	}
	a.notify("success", "Flow deleted: "+name)
	return a.state, a.markDirty(persistScopeState)
}

// RunFlow executes a flow from the app's own Flow tab.
//
// The step guard is nil here ON PURPOSE. The guard exists for the MCP tier,
// where a run is agent-initiated and a secret aimed at a host the user never
// sent it to has to stop for approval (rule 4). A run the user started by
// clicking Run in their own app is the user sending their own credential to
// their own host — the thing the guard is asking permission FOR — so there is
// nothing to ask.
//
// It returns the run report rather than AppState even though a flow mutates
// state (each step stores its response and records history, exactly as a Send
// does). The report is what the caller came for and there is nowhere in
// AppState to put it; the frontend refreshes state the same way it does after
// any push-driven change.
func (a *App) RunFlow(collectionID, flowID, environmentID string, inputs map[string]string) (types.FlowRunResult, error) {
	return a.runFlow(context.Background(), collectionID, flowID, environmentID, inputs, nil)
}

// validateFlow is the single gate every authored flow passes through.
//
// secretNames is the set of variable names that resolve to a secret anywhere in
// this collection's scope. A flow name that collides with one is refused HERE
// as well as at run start, because catching it while the user is editing is the
// only place the message can be acted on cheaply — see runFlow for the
// inversion argument itself.
func validateFlow(collection types.Collection, secretNames map[string]bool, flow types.Flow) error {
	if strings.TrimSpace(flow.Name) == "" {
		return errors.New("flow name is required")
	}
	if len(flow.Steps) == 0 {
		return fmt.Errorf("flow %q has no steps; a flow runs at least one request", flow.Name)
	}

	requestIDs := make(map[string]bool, len(collection.Items))
	for i := range collection.Items {
		requestIDs[collection.Items[i].ID] = true
	}

	inputNames := map[string]bool{}
	for _, input := range flow.Inputs {
		name := strings.TrimSpace(input.Name)
		if name == "" {
			return fmt.Errorf("flow %q has an input with no name", flow.Name)
		}
		if inputNames[name] {
			return fmt.Errorf("flow %q declares the input %q twice; input names are the flow's variable scope and must be unique", flow.Name, name)
		}
		inputNames[name] = true
	}

	stepIDs := map[string]bool{}
	shadowed := []string{}
	for index, step := range flow.Steps {
		stepID := strings.TrimSpace(step.ID)
		if stepID == "" {
			return fmt.Errorf("flow %q step %d has no id; step ids name the step in run reports and in extraction errors", flow.Name, index+1)
		}
		if stepIDs[stepID] {
			return fmt.Errorf("flow %q uses the step id %q twice; step ids must be unique within a flow", flow.Name, stepID)
		}
		stepIDs[stepID] = true
		requestID := strings.TrimSpace(step.RequestID)
		if requestID == "" {
			return fmt.Errorf("flow %q step %q has no requestId", flow.Name, stepID)
		}
		if !requestIDs[requestID] {
			return fmt.Errorf("flow %q step %q names requestId %q, which is not a request in collection %q", flow.Name, stepID, requestID, collection.ID)
		}
		for name := range step.Vars {
			trimmed := strings.TrimSpace(name)
			if trimmed == "" {
				return fmt.Errorf("flow %q step %q has a var with no name", flow.Name, stepID)
			}
			if secretNames[trimmed] {
				shadowed = append(shadowed, trimmed)
			}
		}
		for _, extract := range step.Extract {
			name := strings.TrimSpace(extract.Name)
			if name == "" {
				return fmt.Errorf("flow %q step %q has an extract with no name", flow.Name, stepID)
			}
			if secretNames[name] {
				shadowed = append(shadowed, name)
			}
			switch strings.ToLower(strings.TrimSpace(extract.From)) {
			case "body":
				if _, err := parseFlowPath(extract.Path); err != nil {
					return fmt.Errorf("flow %q step %q extract %q: %w", flow.Name, stepID, name, err)
				}
			case "header":
				if strings.TrimSpace(extract.Path) == "" {
					return fmt.Errorf("flow %q step %q extract %q reads from a header but names no header; set path to the header name", flow.Name, stepID, name)
				}
			case "status":
			default:
				return fmt.Errorf("flow %q step %q extract %q has from %q; it must be body, header or status", flow.Name, stepID, name, extract.From)
			}
		}
		for assertIndex, assertion := range step.Assert {
			if err := validateFlowAssert(flow.Name, stepID, assertIndex, assertion); err != nil {
				return err
			}
		}
	}

	for _, input := range flow.Inputs {
		if secretNames[strings.TrimSpace(input.Name)] {
			shadowed = append(shadowed, strings.TrimSpace(input.Name))
		}
	}
	if len(shadowed) > 0 {
		return fmt.Errorf("flow %q defines %s, which %s a secret variable; rename it — a flow-scoped name that shadows a secret would decide what the request sends in place of the credential",
			flow.Name, flowJoinNames(flowSortedNames(shadowed)), flowIsAre(len(flowSortedNames(shadowed))))
	}

	for _, output := range flow.Outputs {
		if strings.TrimSpace(output.Name) == "" {
			return fmt.Errorf("flow %q has an output with no name", flow.Name)
		}
	}
	return nil
}

// validateFlowAssert checks one assertion is answerable.
//
// A BODY ASSERTION WITH NO PREDICATE IS REJECTED rather than treated as always
// passing. `{"type":"body","path":"$.state"}` reads like a check and is not one;
// accepting it would put a green tick next to a step nothing verified, which is
// worse than the flow failing to save.
func validateFlowAssert(flowName, stepID string, index int, assertion types.FlowAssert) error {
	switch strings.ToLower(strings.TrimSpace(assertion.Type)) {
	case "status":
		if assertion.Equals == nil && len(assertion.In) == 0 {
			return fmt.Errorf("flow %q step %q assertion %d checks the status but says nothing about it; set equals or in", flowName, stepID, index+1)
		}
		if assertion.Equals != nil {
			if _, ok := flowStatusEquals(assertion.Equals); !ok {
				return fmt.Errorf("flow %q step %q assertion %d has a status equals that is not a number", flowName, stepID, index+1)
			}
		}
	case "body":
		if _, err := parseFlowPath(assertion.Path); err != nil {
			return fmt.Errorf("flow %q step %q assertion %d: %w", flowName, stepID, index+1, err)
		}
		if assertion.Equals == nil && strings.TrimSpace(assertion.Contains) == "" && !assertion.Exists {
			return fmt.Errorf("flow %q step %q assertion %d names a path but no check; set equals, contains or exists", flowName, stepID, index+1)
		}
	default:
		return fmt.Errorf("flow %q step %q assertion %d has type %q; it must be status or body", flowName, stepID, index+1, assertion.Type)
	}
	return nil
}

// flowSecretNamesInScope is every name that resolves to a secret anywhere this
// collection could resolve one.
//
// IT IS THE UNION ACROSS EVERY ENVIRONMENT, not the selected one, which is
// deliberately wider than mcpSecretNamesInScope's per-run set. Authoring has no
// environment: a flow saved today runs against whichever environment is picked
// later, and a step var that shadows a secret only in Production is a flow that
// validates now and is refused at run time in front of the person running it.
func flowSecretNamesInScope(workspace *Workspace, collection types.Collection) map[string]bool {
	secrets := map[string]bool{}
	collect := func(variables []types.Variable) {
		for _, variable := range variables {
			name := strings.TrimSpace(variable.Name)
			if name == "" || !variable.Secret {
				continue
			}
			secrets[name] = true
		}
	}
	if workspace != nil {
		for _, environment := range workspace.GlobalEnvironments {
			collect(environment.Variables)
		}
	}
	collect(collection.Variables)
	for _, environment := range collection.Environments {
		collect(environment.Variables)
	}
	for _, folder := range collection.Folders {
		collect(folder.Variables)
	}
	for i := range collection.Items {
		collect(collection.Items[i].Vars.Req)
	}
	return secrets
}

// flowRunSecretNames is the run-time counterpart: the names that resolve to a
// secret for THIS run, drawn from the same sources in the same order as
// mcpSecretNamesInScope so that "shadows a secret" means one thing across the
// two tiers.
func flowRunSecretNames(globals []types.Environment, collection types.Collection, environmentID string, items []types.RequestItem) map[string]bool {
	secrets := map[string]bool{}
	collect := func(variables []types.Variable) {
		for _, variable := range variables {
			name := strings.TrimSpace(variable.Name)
			if name == "" || !variable.Secret {
				continue
			}
			secrets[name] = true
		}
	}
	for _, environment := range globals {
		collect(environment.Variables)
	}
	collect(collection.Variables)
	if environmentID != "" {
		for _, environment := range collection.Environments {
			if environment.ID == environmentID {
				collect(environment.Variables)
				break
			}
		}
	}
	for _, item := range items {
		for _, folder := range scripting.FolderChain(collection, item) {
			collect(folder.Variables)
		}
		collect(item.Vars.Req)
	}
	return secrets
}

// flowSortedNames sorts and dedupes, so an error naming several variables reads
// the same way on every run rather than in Go's map order.
func flowSortedNames(names []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(names))
	for _, name := range names {
		if seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func flowJoinNames(names []string) string {
	quoted := make([]string, 0, len(names))
	for _, name := range names {
		quoted = append(quoted, fmt.Sprintf("%q", name))
	}
	return strings.Join(quoted, ", ")
}

func flowIsAre(count int) string {
	if count == 1 {
		return "is"
	}
	return "are"
}
