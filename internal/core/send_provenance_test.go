package core

// Provenance, explicit at the roots — §4.5 of the Phase 6 design.
//
// WHAT THESE TESTS ARE FOR. Provenance used to be inferred from the context: a
// policy meant "agent", its absence meant "user". That inference is what made
// the strict flag useless, because a future engine path that dropped its policy
// was not detectable as unlabeled — it was indistinguishable from a UI send and
// skipped every refusal on its way to the network. §4.5 replaces the inference
// with a required argument of an unexported type with two constructors, so the
// two questions this file asks are:
//
//  1. can a send happen without provenance? (no — both roots refuse a zero
//     value before they touch state, let alone a socket)
//  2. does the ARGUMENT decide, rather than whatever happens to be on the
//     context? (yes — the roots stamp the context from the argument, never the
//     other way round)
//
// The migration delegates get their own section at the bottom, which is also
// the grep target for the wave that deletes them.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/mutexdev/lite_api/internal/mcpserver"
	"github.com/mutexdev/lite_api/internal/runner"
	"github.com/mutexdev/lite_api/internal/types"
)

// --- 1. a zero value is refused, before any work ---------------------------

// requireProvenanceBug asserts the error is the roots' internal-bug refusal
// rather than anything the run itself produced.
func requireProvenanceBug(t *testing.T, err error, what string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: an unprovenanced send was allowed to run", what)
	}
	if !errors.Is(err, mcpserver.ErrDenied) {
		t.Fatalf("%s: the refusal is not ErrDenied-class: %v", what, err)
	}
	if !strings.Contains(err.Error(), "no send provenance") || !strings.Contains(err.Error(), "bug in LiteAPI") {
		t.Fatalf("%s: the refusal does not say what went wrong: %v", what, err)
	}
}

// A zero-value provenance is not a panic and not a silent UI default. It is a
// refusal, and it happens before the send path resolves anything: the ids below
// are nonsense, and the error is still about provenance rather than about them.
func TestSendPathRefusesAZeroValueProvenanceBeforeAnyWork(t *testing.T) {
	fixture := newSendFixture(t)
	requestID := fixture.addRequest("Never sent", "{{baseUrl}}/never", nil)

	_, _, response, err := fixture.app.sendRequestWithControlsContextProvenance(
		context.Background(), sendProvenance{}, fixture.collectionID, requestID, fixture.environmentID, nil, nil,
		runner.Iteration{},
	)
	requireProvenanceBug(t, err, "the send path")
	if response != nil {
		t.Fatalf("the refused send produced a response: %#v", response)
	}
	if got := len(fixture.received()); got != 0 {
		t.Fatalf("an unprovenanced send reached the server %d times", got)
	}

	// The ids are unresolvable. A check that ran after the lookups would report
	// them; this one reports the missing provenance, which is how "before any
	// work" is pinned rather than asserted.
	_, _, _, err = fixture.app.sendRequestWithControlsContextProvenance(
		context.Background(), sendProvenance{}, "collection_nope", "item_nope", "", nil, nil,
		runner.Iteration{},
	)
	requireProvenanceBug(t, err, "the send path with unresolvable ids")
	if strings.Contains(err.Error(), "collection_nope") {
		t.Fatalf("the provenance check ran after the collection lookup: %v", err)
	}
}

func TestFlowRunnerRefusesAZeroValueProvenanceBeforeAnyWork(t *testing.T) {
	fixture := newSendFixture(t)
	flow := fixture.installFlow("flow_zero", fixture.addRequest("Step", "{{baseUrl}}/step", nil))

	result, err := fixture.app.runFlowProvenance(
		context.Background(), sendProvenance{}, fixture.collectionID, flow.ID, fixture.environmentID, nil, nil,
	)
	requireProvenanceBug(t, err, "the flow runner")
	if len(result.Steps) != 0 {
		t.Fatalf("the refused flow ran %d steps", len(result.Steps))
	}
	if got := len(fixture.received()); got != 0 {
		t.Fatalf("an unprovenanced flow reached the server %d times", got)
	}

	// Same "before any work" proof: an unknown flow id is not what comes back.
	_, err = fixture.app.runFlowProvenance(
		context.Background(), sendProvenance{}, fixture.collectionID, "flow_nope", "", nil, nil,
	)
	requireProvenanceBug(t, err, "the flow runner with an unknown flow id")
	if strings.Contains(err.Error(), "flow_nope") {
		t.Fatalf("the provenance check ran after the flow lookup: %v", err)
	}
}

// --- 2. the argument decides, not the context ------------------------------

// THE WHOLE POINT OF THE TYPE, as one test. The context carries no policy in
// the first half and the wrong label in the second; both times the root does
// what its ARGUMENT said, and the policy it was handed reaches the checkpoint
// and the guard transport underneath it.
func TestSendProvenanceArgumentDecidesAndCarriesThePolicyEndToEnd(t *testing.T) {
	fixture := newSendFixture(t)
	other, hits := newOtherOrigin(t)

	t.Run("mcp provenance governs the send even on a bare context", func(t *testing.T) {
		requestID := fixture.addRequest("Retargeted", "{{baseUrl}}/ok", nil)
		policy := fixture.policyFor(requestID)

		// context.Background(): nothing on it says MCP. The argument does.
		_, _, response, err := fixture.app.sendRequestWithControlsContextProvenance(
			context.Background(), mcpSendProvenance(policy), fixture.collectionID, requestID, fixture.environmentID, nil, nil,
			runner.Iteration{Data: map[string]string{"baseUrl": other.URL}},
		)
		if err != nil {
			t.Fatalf("send: %v", err)
		}
		requireDenied(t, response, "a retargeted send under argument-only provenance")
		if hits.Load() != 0 {
			t.Fatalf("the refused send reached the other origin %d times", hits.Load())
		}
	})

	t.Run("the same policy still allows what the definition names", func(t *testing.T) {
		requestID := fixture.addRequest("In scope", "{{baseUrl}}/ok", nil)
		policy := fixture.policyFor(requestID)
		_, _, response, err := fixture.app.sendRequestWithControlsContextProvenance(
			context.Background(), mcpSendProvenance(policy), fixture.collectionID, requestID, fixture.environmentID, nil, nil,
			runner.Iteration{},
		)
		if err != nil {
			t.Fatalf("send: %v", err)
		}
		if response.Status != http.StatusOK {
			t.Fatalf("an in-scope MCP send failed: %#v", response.Error)
		}
	})

	t.Run("a UI argument is not overridden by a policy left on the context", func(t *testing.T) {
		before := hits.Load()
		// Prompts are counted from HERE: the retarget subtest above legitimately
		// raised one on its way to being denied.
		promptsBefore := fixture.promptCount()
		requestID := fixture.addRequest("UI anywhere", other.URL+"/ui", nil)
		// A policy whose Base is the fixture server, on the context, while the
		// argument says UI. The root stamps the context from the argument, so
		// nothing below sees the stale policy — provenance is what the caller
		// that knows declared, at one place, once.
		stale := fixture.policyFor(requestID)
		ctx := mcpContextWithPolicy(context.Background(), stale)
		_, _, response, err := fixture.app.sendRequestWithControlsContextProvenance(
			ctx, uiSendProvenance(), fixture.collectionID, requestID, fixture.environmentID, nil, nil,
			runner.Iteration{},
		)
		if err != nil {
			t.Fatalf("send: %v", err)
		}
		if response.Status != http.StatusOK {
			t.Fatalf("a UI send was governed by a policy it never claimed: %#v", response.Error)
		}
		if hits.Load() == before {
			t.Fatal("the UI send never reached its target")
		}
		if got := fixture.promptCount() - promptsBefore; got != 0 {
			t.Fatalf("a UI send raised %d approval prompts", got)
		}
	})
}

// --- 3. every migrated UI caller still behaves like a UI send --------------

// The four production callers that now say uiSendProvenance out loud:
// SendRequest, SendRequestWithPromptValues, the collection runner and the Flow
// tab's RunFlow. Each one contacts an origin no definition in this workspace
// names, which under a policy would prompt and then deny, and each one is
// expected to just work (§1.2(4)).
func TestUISendsThroughEveryMigratedCallerAreUnaffected(t *testing.T) {
	fixture := newSendFixture(t)
	other, hits := newOtherOrigin(t)

	t.Run("SendRequest", func(t *testing.T) {
		before := hits.Load()
		requestID := fixture.addRequest("Send binding", other.URL+"/send", nil)
		if _, err := fixture.app.SendRequest(fixture.collectionID, requestID, fixture.environmentID); err != nil {
			t.Fatalf("SendRequest: %v", err)
		}
		if hits.Load() == before {
			t.Fatal("SendRequest never reached the target")
		}
	})

	t.Run("SendRequestWithPromptValues", func(t *testing.T) {
		before := hits.Load()
		requestID := fixture.addRequest("Prompted binding", other.URL+"/prompted", nil)
		if _, err := fixture.app.SendRequestWithPromptValues(fixture.collectionID, requestID, fixture.environmentID, map[string]string{"unused": "x"}); err != nil {
			t.Fatalf("SendRequestWithPromptValues: %v", err)
		}
		if hits.Load() == before {
			t.Fatal("SendRequestWithPromptValues never reached the target")
		}
	})

	t.Run("the collection runner", func(t *testing.T) {
		before := hits.Load()
		requestID := fixture.addRequest("Runner item", other.URL+"/runner", nil)
		if _, err := fixture.app.RunCollectionWithOptions(fixture.collectionID, fixture.environmentID, RunnerOptions{
			SelectedItemIDs: []string{requestID},
		}); err != nil {
			t.Fatalf("RunCollectionWithOptions: %v", err)
		}
		if hits.Load() == before {
			t.Fatal("the collection runner never reached the target")
		}
	})

	t.Run("the Flow tab", func(t *testing.T) {
		before := hits.Load()
		flow := fixture.installFlow("flow_ui", fixture.addRequest("Flow step", other.URL+"/flow", nil))
		result, err := fixture.app.RunFlow(fixture.collectionID, flow.ID, fixture.environmentID, nil)
		if err != nil {
			t.Fatalf("RunFlow: %v", err)
		}
		if !result.OK {
			t.Fatalf("the UI flow run failed: %#v", result)
		}
		if hits.Load() == before {
			t.Fatal("the UI flow run never reached the target")
		}
	})

	if fixture.promptCount() != 0 {
		t.Fatalf("UI sends raised %d approval prompts", fixture.promptCount())
	}
}

// --- 4. a flow's provenance reaches every step, and every nested send ------

// THE FLOW IS ONE EXECUTION AND ONE CLASSIFICATION. The flow root is told once
// who asked for the run, and every step — plus every bru.runRequest a step's
// script starts — is that same kind of send. The nested half is the one worth
// proving: it does not receive provenance as an argument, it inherits the
// context the root stamped, through startCancellableRequestWithParent.
//
// The MCP run comes FIRST and the UI run second, deliberately: an MCP run
// persists nothing, so it cannot teach the UI run anything, while the reverse
// order would let the UI run's setVar land in state and widen the agent run's
// Base. That ordering is itself §3 working.
func TestFlowProvenanceReachesEveryStepAndNestedRunRequest(t *testing.T) {
	fixture := newSendFixture(t)
	other, hits := newOtherOrigin(t)
	fixture.setEnvironmentVariable("nestedBase", fixture.baseURL)
	fixture.addRequest("Nested target", "{{nestedBase}}/nested", nil)

	stepOne := fixture.addRequest("Step one", "{{baseUrl}}/step-one", nil)
	// Step two retargets the NESTED request through a setVar and runs it. Under
	// the flow's own provenance that nested send is checked against the nested
	// definition's Base, which names only the fixture server.
	stepTwo := fixture.addRequest("Step two", "{{baseUrl}}/step-two", func(item *types.RequestItem) {
		item.PreScript = `
bru.setVar("nestedBase", "` + other.URL + `");
const res = await bru.runRequest("Nested target");
bru.setVar("nestedError", String(res.error || ""));
bru.setVar("nestedURL", String(res.url || ""));
`
	})
	flow := fixture.installFlow("flow_prov", stepOne, stepTwo)

	// The MCP run, wired the way mcp_flows.go wires it: one policy for the
	// execution, and a guard that makes each step's own definition the active
	// scope.
	policy, book := fixture.app.newMCPExecutionPolicy()
	guard := func(_ int, requestID string, _ map[string]string) error {
		mcpEnterScope(policy, book, fixture.plan(t, requestID))
		return nil
	}
	result, err := fixture.app.runFlowProvenance(
		context.Background(), mcpSendProvenance(policy), fixture.collectionID, flow.ID, fixture.environmentID, nil, guard,
	)
	if err != nil {
		t.Fatalf("the agent flow run was refused outright: %v", err)
	}
	if !result.OK {
		t.Fatalf("the agent flow run failed: %#v", result)
	}
	if got := fmt.Sprint(policy.overlay.variables(mcpOverlayRuntime)["nestedURL"]); !strings.HasPrefix(got, other.URL) {
		t.Fatalf("the nested send was not actually retargeted (url=%q), so this test proved nothing", got)
	}
	nestedError := fmt.Sprint(policy.overlay.variables(mcpOverlayRuntime)["nestedError"])
	if !strings.Contains(nestedError, mcpserver.ErrDenied.Error()) {
		t.Fatalf("a nested send inside an agent flow was not checked: error=%q", nestedError)
	}
	if hits.Load() != 0 {
		t.Fatalf("the retargeted nested send reached the other origin %d times", hits.Load())
	}
	// Both steps really ran through the send path, so the refusal above is the
	// nested send being checked and not the flow stopping early.
	paths := map[string]bool{}
	for _, request := range fixture.received() {
		paths[request.URL.Path] = true
	}
	if !paths["/step-one"] || !paths["/step-two"] {
		t.Fatalf("not every step reached the server: %v", paths)
	}

	// THE CONTROL. The same flow under uiSendProvenance: the nested retarget is
	// nobody's business, and it goes through. Without this half, a nested send
	// that was simply broken would pass the assertions above.
	uiResult, err := fixture.app.runFlowProvenance(
		context.Background(), uiSendProvenance(), fixture.collectionID, flow.ID, fixture.environmentID, nil, nil,
	)
	if err != nil {
		t.Fatalf("the UI flow run was refused: %v", err)
	}
	if !uiResult.OK {
		t.Fatalf("the UI flow run failed: %#v", uiResult)
	}
	if hits.Load() == 0 {
		t.Fatal("the UI flow's nested send was blocked; the agent-run assertions above prove nothing")
	}
}

// --- 5. the migration delegates --------------------------------------------

// THE GREP TARGET. This is the only place left that calls
// sendRequestWithControlsContext and runFlow by their old names. The wave that
// flips strict deletes them, legacyUnlabeled, and this test together — the
// first two lines below are what its audit is looking for.
//
// While strict is OFF, an unlabeled send behaves exactly as a UI send: that is
// what keeps the intermediate waves shippable. Note what that means and why it
// is temporary — a policy sitting on the context is NOT consulted, so a caller
// that has not been migrated silently loses the boundary. Flipping strict turns
// that from silence into a refusal.
func TestLegacyUnlabeledProvenanceBehavesAsUIWhileStrictIsOff(t *testing.T) {
	fixture := newSendFixture(t)
	other, hits := newOtherOrigin(t)
	requestID := fixture.addRequest("Unlabeled", other.URL+"/legacy", nil)
	policy := fixture.policyFor(requestID)

	_, _, response, err := fixture.app.sendRequestWithControlsContext(
		mcpContextWithPolicy(context.Background(), policy), fixture.collectionID, requestID, fixture.environmentID, nil, nil,
		runner.Iteration{},
	)
	if err != nil {
		t.Fatalf("the legacy delegate refused a send while strict is off: %v", err)
	}
	if response.Status != http.StatusOK {
		t.Fatalf("the legacy delegate did not behave as a UI send: %#v", response.Error)
	}
	if hits.Load() == 0 {
		t.Fatal("the legacy send never reached its target")
	}

	flow := fixture.installFlow("flow_legacy", requestID)
	result, err := fixture.app.runFlow(context.Background(), fixture.collectionID, flow.ID, fixture.environmentID, nil, nil)
	if err != nil || !result.OK {
		t.Fatalf("the legacy flow delegate did not behave as a UI run: err=%v result=%#v", err, result)
	}
}

// And what the flip does to it. The flag is restored immediately: this test
// says what will happen in the final wave, it does not perform it.
func TestLegacyUnlabeledProvenanceIsRefusedUnderStrict(t *testing.T) {
	defer restoreStrictEgressProvenance(true)()

	fixture := newSendFixture(t)
	requestID := fixture.addRequest("Unlabeled under strict", "{{baseUrl}}/legacy", nil)

	_, _, _, err := fixture.app.sendRequestWithControlsContext(
		context.Background(), fixture.collectionID, requestID, fixture.environmentID, nil, nil,
		runner.Iteration{},
	)
	if err == nil {
		t.Fatal("an unlabeled send was allowed under strict provenance")
	}
	if !errors.Is(err, mcpserver.ErrDenied) || !strings.Contains(err.Error(), "unlabeled migration path") {
		t.Fatalf("the strict refusal does not name the migration path: %v", err)
	}
	if got := len(fixture.received()); got != 0 {
		t.Fatalf("an unlabeled send under strict reached the server %d times", got)
	}

	flow := fixture.installFlow("flow_strict", requestID)
	if _, err := fixture.app.runFlow(context.Background(), fixture.collectionID, flow.ID, fixture.environmentID, nil, nil); err == nil {
		t.Fatal("an unlabeled flow run was allowed under strict provenance")
	}

	// The two real constructors are unaffected by the flag, which is the point:
	// the flip only closes the unlabeled door.
	if err := mcpRequireSendProvenance(uiSendProvenance(), "the send path"); err != nil {
		t.Fatalf("a UI send is refused under strict: %v", err)
	}
	if err := mcpRequireSendProvenance(mcpSendProvenance(newMCPEgressPolicy()), "the send path"); err != nil {
		t.Fatalf("an MCP send is refused under strict: %v", err)
	}
}

// installFlow puts a straight-line flow over the given requests onto the
// fixture's collection, bypassing CreateFlow the way flow_run_test.go's
// fixture does.
func (f *sendFixture) installFlow(id string, requestIDs ...string) types.Flow {
	f.t.Helper()
	flow := types.Flow{ID: id, Name: id, Steps: make([]types.FlowStep, 0, len(requestIDs))}
	for index, requestID := range requestIDs {
		flow.Steps = append(flow.Steps, types.FlowStep{
			ID:        fmt.Sprintf("step%d", index+1),
			RequestID: requestID,
		})
	}
	f.app.mu.Lock()
	defer f.app.mu.Unlock()
	collection := &f.app.state.Workspaces[0].Collections[0]
	collection.Flows = append(collection.Flows, types.CloneFlow(flow))
	return flow
}
