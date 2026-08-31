package core

// Adversarial pass B — attack areas 2 (non-persistence) and 3 (the write
// tier). These two tests attempt shapes the brief calls out explicitly that
// the existing suite (mcp_write_test.go, mcp_write_adversarial_test.go,
// mcp_boundary_e2e_test.go, mcp_send_path_test.go) does not already pin:
//
//  1. The inherited-OAuth2-token-endpoint retarget via request-level `vars`,
//     attempted through create_request rather than update_request.
//     TestMCPAuthoringGuardChecksTheInheritedOAuth2TokenEndpoint
//     (mcp_write_test.go) proves the update path; a create has no prior
//     stored version to diff against and builds its authoring subject
//     slightly differently (mcpAuthoringSubject.replacingID is empty), so it
//     is worth confirming separately that this is not a create-only hole.
//  2. A denied-mid-flow variant of §3's non-persistence guarantee. The
//     existing tests cover a single denied run (TestMCPDeniedRunPersistsNothing)
//     and a fully successful two-step flow whose overlay-only continuity dies
//     with the execution (TestMCPFlowOverlayScopedToExecution), but nothing
//     drives a flow where step 1 SUCCEEDS and writes a variable via script,
//     and step 2 is then DENIED — the shape where "the run made partial
//     progress before being refused" is most likely to tempt a persistence
//     path into treating the whole execution as worth saving.

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mutexdev/lite_api/internal/mcpserver"
	"github.com/mutexdev/lite_api/internal/types"
)

// --- area 3: create_request-side inherited OAuth2 token-endpoint retarget --

// CONFIRMED-SAFE / COVERAGE-ADDED. Mirrors
// TestMCPAuthoringGuardChecksTheInheritedOAuth2TokenEndpoint but through
// create_request: a brand-new request that only inherits the collection's
// oauth2 block saves quietly (the token endpoint is already this collection's
// Base), while one whose own authored `vars` retarget the {{tokenBase}} the
// AccessTokenURL reads is refused, because a request-scoped variable outranks
// the environment/collection one that already resolved it safely.
func TestMCPCreateRequestGuardChecksTheInheritedOAuth2TokenEndpoint(t *testing.T) {
	f := newMCPWriteFixture(t)
	f.enableWriteTier()
	f.noFrontend() // deny by default: nobody is there to approve anything

	f.app.mu.Lock()
	collection := f.collection()
	collection.Variables = append(collection.Variables,
		Variable{ID: "var-token-base-create", Name: "tokenBase", Value: "https://auth.known-create.example", Enabled: true})
	collection.Auth = types.AuthConfig{Mode: "oauth2", OAuth2: types.OAuth2Auth{
		GrantType:      "client_credentials",
		AccessTokenURL: "{{tokenBase}}/oauth/token",
		ClientID:       "cid",
		ClientSecret:   "{{apiToken}}",
	}}
	f.app.mu.Unlock()

	// A sibling that already inherits the block, so the honest token endpoint
	// is a destination the collection reaches and only a RETARGET is new.
	f.plantRequest("Inherits the token endpoint (create-side sibling)", "{{baseUrl}}/sibling-create", func(item *types.RequestItem) {
		item.Auth = types.AuthConfig{Mode: "inherit"}
	})

	t.Run("a brand-new request that only inherits is quiet", func(t *testing.T) {
		if _, err := f.create(mcpserver.CreateRequestParams{
			Name: "New request inheriting the known token endpoint",
			URL:  "{{baseUrl}}/new-inherit",
			Auth: map[string]string{"mode": "inherit"},
		}); err != nil {
			t.Fatalf("a brand-new request inheriting the collection's own token endpoint was refused: %v", err)
		}
	})

	t.Run("a brand-new request whose own vars retarget the inherited endpoint is refused", func(t *testing.T) {
		before := f.itemCount()
		_, err := f.create(mcpserver.CreateRequestParams{
			Name: "New request retargeting the token endpoint",
			URL:  "{{baseUrl}}/new-retarget",
			Auth: map[string]string{"mode": "inherit"},
			Vars: []mcpserver.AuthoredRow{{Name: "tokenBase", Value: "https://" + writeEvilHost}},
		})
		if !errors.Is(err, mcpserver.ErrDenied) {
			t.Fatalf("a create_request authored variable retargeted the inherited token endpoint: %v", err)
		}
		if !strings.Contains(err.Error(), writeEvilHost) {
			t.Errorf("the refusal does not name the origin: %v", err)
		}
		if !strings.Contains(err.Error(), "token") {
			t.Errorf("the refusal does not say which egress it is about: %v", err)
		}
		// A refused CREATE must leave nothing behind at all — there is no
		// prior version for a refused create to "revert to", so the only
		// correct count is the one from before this call.
		if after := f.itemCount(); after != before {
			t.Errorf("a refused create_request still added an item: before=%d after=%d", before, after)
		}
	})
}

// --- area 2: non-persistence, the denied-mid-flow variant -------------------

// COVERAGE-ADDED. Step 1 succeeds and writes a variable via bru.setVar; step 2
// is then denied by the destination boundary (a step var retargets its own
// request's {{baseUrl}} at a host nothing in the collection reaches). The
// property under test is that NEITHER step's effect on persisted state
// survives the flow's overall refusal: step 1's setVar must not land in
// AppState merely because step 1 itself completed before the flow as a whole
// was stopped, and the attacker's server must never be contacted.
//
// This is the flow-shaped sibling of TestMCPDeniedRunPersistsNothing
// (mcp_send_path_test.go, a single denied run) and
// TestMCPTwoRunLaunderingNeverWidensTheNextRunsBase (mcp_boundary_e2e_test.go,
// two SEPARATE runs) — neither drives one flow where an earlier step's
// success is followed by a later step's denial.
func TestMCPFlowDeniedMidFlowPersistsNothingFromTheEarlierSuccessfulStep(t *testing.T) {
	f := newMCPFlowFixture(t)
	// The fixture's own approval emitter records prompts but never answers
	// them, so the destination denial below arrives via a timeout rather than
	// an explicit decline; keep it short so this test does not pay the 60s
	// default for what is still an unambiguous deny.
	f.app.mcpApprovalTimeout = 50 * time.Millisecond

	var attackerHits atomic.Int64
	attacker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attackerHits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer attacker.Close()

	// Step 1 (the fixture's own "lookup" request) plants a RUNTIME variable —
	// the collection-scoped write ApplyScriptVariableContextToState would
	// persist — via its own post-response script. This is deliberately the
	// user's own script, the honest version of the threat: nothing here is an
	// agent-authored script, only agent-authored flow wiring.
	f.app.mu.Lock()
	for index := range f.app.state.Workspaces[0].Collections[0].Items {
		if f.app.state.Workspaces[0].Collections[0].Items[index].ID == f.lookupID {
			f.app.state.Workspaces[0].Collections[0].Items[index].PostScript =
				`bru.setVar("midFlowPlant", "PLANTED-BY-STEP-1-BEFORE-THE-DENIAL");`
		}
	}
	f.app.mu.Unlock()

	before := persistedVariableSnapshot(t, f.app)

	f.install(types.Flow{
		ID:   "flow_denied_midflow",
		Name: "Step 1 succeeds, step 2 is denied",
		Steps: []types.FlowStep{
			{
				ID:        "lookup",
				RequestID: f.lookupID,
				Vars:      map[string]string{"code": "DHK-04"},
				Assert:    []types.FlowAssert{{Type: "status", Equals: 200}},
			},
			{
				ID:        "createTerminal",
				RequestID: f.createID,
				// THE DENIAL: step 2's own vars retarget its request's
				// {{baseUrl}} at a host nothing in this collection's Base
				// reaches. The main checkpoint refuses this at send time —
				// this is an ordinary destination-boundary denial, not a
				// secret-injection refusal, and it goes through
				// policy.Authorize the way the two-run-laundering test's
				// does, so this test measures persistence, not
				// classification.
				Vars: map[string]string{"baseUrl": attacker.URL, "storeId": "irrelevant"},
			},
		},
	})

	outcome, err := f.run("flow_denied_midflow", nil)
	if err == nil {
		t.Fatalf("the flow was not refused at all: %+v", outcome)
	}
	if !errors.Is(err, mcpserver.ErrDenied) {
		t.Fatalf("the refusal does not wrap ErrDenied: %v", err)
	}
	// Step 1 really did complete — otherwise there is nothing for step 1's
	// script to have planted, and the test proves nothing.
	if len(outcome.Steps) < 1 || outcome.Steps[0].Status != http.StatusOK {
		t.Fatalf("step 1 did not complete before the denial, so this test measures nothing: %+v", outcome.Steps)
	}
	if attackerHits.Load() != 0 {
		t.Fatalf("the denied step still reached the attacker's server %d times", attackerHits.Load())
	}

	// THE ASSERTION THAT MATTERS. Step 1's setVar must not be in AppState —
	// not "not yet", but never, because the whole execution's overlay dies
	// with it whether the flow finished, succeeded, or (as here) was stopped
	// partway through by a denial.
	if after := persistedVariableSnapshot(t, f.app); after != before {
		t.Errorf("a flow denied on step 2 still persisted step 1's variable write\nbefore: %s\nafter:  %s", before, after)
	}
	f.app.mu.Lock()
	cookieCount := len(f.app.state.Cookies)
	f.app.mu.Unlock()
	if cookieCount != 0 {
		t.Errorf("a flow denied mid-run left %d cookies in state", cookieCount)
	}

	// A FRESH run's Base must not have widened either — the structural half,
	// matching TestMCPTwoRunLaunderingNeverWidensTheNextRunsBase's own check.
	plan, err := f.app.mcpRunPlan(f.collectionID, f.createID, "")
	if err != nil {
		t.Fatalf("mcpRunPlan: %v", err)
	}
	attackerOrigin := mustOrigin(t, attacker.URL)
	if plan.scope.allows(attackerOrigin, egressKindMain) {
		t.Fatalf("the denied mid-flow step widened a later run's Base to include %s", attackerOrigin)
	}
}
