package core

// The send path under MCP provenance — §3, §4.3, §4.4 and §5 rows 1, 2, 3, 11,
// 12, 13, 15, 16 and 17 of the Phase 6 design.
//
// EVERY TEST HERE DRIVES THE REAL SEND. It builds a policy the way mcpRunPlan
// does, attaches it to a context, and calls
// sendRequestWithControlsContextProvenance with mcpSendProvenance(policy) —
// the same function the Wails binding and the collection runner call. That is
// deliberate: the property is about what the engine does, and a test that
// re-implemented the wiring would pass while the engine did something else.
//
// THE OLD HOST GUARD IS NOT IN THE PICTURE. These tests enter below
// mcpBackend.RunRequest, so the only boundary being exercised is the
// destination policy. That is the point — the two are enforcing side by side
// until the final wave, and a test that ran through both could not say which
// one refused.

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mutexdev/lite_api/internal/auth/awsv4"
	"github.com/mutexdev/lite_api/internal/mcpserver"
	"github.com/mutexdev/lite_api/internal/runner"
	xport "github.com/mutexdev/lite_api/internal/transport"
	"github.com/mutexdev/lite_api/internal/types"
)

// --- the fixture ----------------------------------------------------------

// sendFixture is one workspace, one collection, one environment, and whatever
// requests a test adds. Its policy is built from the app's own mcpRunPlan, so
// Base is whatever the stored definition really resolves to.
type sendFixture struct {
	t             *testing.T
	app           *App
	collectionID  string
	environmentID string
	baseURL       string
	server        *httptest.Server

	mu       sync.Mutex
	requests []*http.Request
	prompts  []types.MCPApprovalRequest
	// answer is what the fake frontend replies. nil means "no answer", which is
	// a closed window: the prompt times out and denies.
	answer *bool
}

func newSendFixture(t *testing.T) *sendFixture {
	t.Helper()
	fixture := &sendFixture{t: t}
	fixture.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fixture.mu.Lock()
		fixture.requests = append(fixture.requests, r.Clone(context.Background()))
		fixture.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"path":%q,"echo":%q}`, r.URL.Path, r.Header.Get("X-Echo"))
	}))
	t.Cleanup(fixture.server.Close)
	fixture.baseURL = fixture.server.URL

	app := newAppForTest(t)
	if _, err := app.GetState(); err != nil {
		t.Fatalf("GetState: %v", err)
	}
	fixture.app = app
	// A short prompt timeout everywhere: several tests deliberately let a
	// prompt go unanswered, and the 60 s default would make them the slowest
	// thing in the package.
	app.mcpApprovalTimeout = 50 * time.Millisecond
	app.mcpApprovalEmit = func(request types.MCPApprovalRequest) {
		fixture.mu.Lock()
		fixture.prompts = append(fixture.prompts, request)
		answer := fixture.answer
		fixture.mu.Unlock()
		if answer == nil {
			return
		}
		go func() { _ = app.ResolveMCPApproval(request.ID, *answer, false) }()
	}

	app.mu.Lock()
	collection := &app.state.Workspaces[0].Collections[0]
	collection.Path = t.TempDir()
	collection.Environments = append(collection.Environments, Environment{
		ID:   "env_send",
		Name: "Send",
		Variables: []Variable{
			{ID: "v-base", Name: "baseUrl", Value: fixture.baseURL, Enabled: true},
		},
	})
	fixture.collectionID = collection.ID
	fixture.environmentID = "env_send"
	app.mu.Unlock()
	return fixture
}

// addRequest appends a request and returns its id. mutate gets the item before
// it is stored, for the tests that need scripts, auth or settings.
func (f *sendFixture) addRequest(name, rawURL string, mutate func(*types.RequestItem)) string {
	f.t.Helper()
	f.app.mu.Lock()
	defer f.app.mu.Unlock()
	collection := &f.app.state.Workspaces[0].Collections[0]
	item := types.NewRequestItem(name, "http", len(collection.Items)+1)
	item.Method = http.MethodGet
	item.URL = rawURL
	item.Body = types.RequestBody{Mode: "none"}
	if mutate != nil {
		mutate(&item)
	}
	collection.Items = append(collection.Items, item)
	return item.ID
}

// setEnvironmentVariable adds or replaces a value in the fixture environment,
// which is part of the DEFINITION and therefore feeds Base.
func (f *sendFixture) setEnvironmentVariable(name, value string) {
	f.t.Helper()
	f.app.mu.Lock()
	defer f.app.mu.Unlock()
	collection := &f.app.state.Workspaces[0].Collections[0]
	for index := range collection.Environments {
		if collection.Environments[index].ID != f.environmentID {
			continue
		}
		environment := &collection.Environments[index]
		for vi := range environment.Variables {
			if environment.Variables[vi].Name == name {
				environment.Variables[vi].Value = value
				return
			}
		}
		environment.Variables = append(environment.Variables, Variable{
			ID: "v-" + name, Name: name, Value: value, Enabled: true,
		})
		return
	}
	f.t.Fatalf("fixture environment %q vanished", f.environmentID)
}

func (f *sendFixture) setProxy(proxy types.ProxyConfig) {
	f.t.Helper()
	f.app.mu.Lock()
	defer f.app.mu.Unlock()
	f.app.state.Workspaces[0].Collections[0].Proxy = proxy
}

// policyFor builds the execution policy the way mcpRunPlan does — same site,
// same single agent-free variable context, same Base.
func (f *sendFixture) policyFor(requestID string) *mcpEgressPolicy {
	f.t.Helper()
	plan, err := f.app.mcpRunPlan(f.collectionID, requestID, f.environmentID)
	if err != nil {
		f.t.Fatalf("mcpRunPlan(%q): %v", requestID, err)
	}
	policy, _ := f.app.mcpEgressPolicyForRun(plan)
	return policy
}

// send runs one request under the given policy. overrides are the AGENT's
// variables, delivered the way run_request delivers them.
// A nil policy here is a UI send, stated with uiSendProvenance rather than
// implied by the absence of a policy (§4.5).
func (f *sendFixture) send(policy *mcpEgressPolicy, requestID string, overrides map[string]string) *Response {
	f.t.Helper()
	ctx := context.Background()
	prov := uiSendProvenance()
	if policy != nil {
		ctx = mcpContextWithPolicy(ctx, policy)
		prov = mcpSendProvenance(policy)
	}
	_, _, response, err := f.app.sendRequestWithControlsContextProvenance(
		ctx, prov, f.collectionID, requestID, f.environmentID, nil, nil,
		runner.Iteration{Data: overrides},
	)
	if err != nil {
		f.t.Fatalf("send(%q): %v", requestID, err)
	}
	if response == nil {
		f.t.Fatalf("send(%q) produced no response", requestID)
	}
	return response
}

// mcpSend is the common case: a fresh policy for this request, then one send.
func (f *sendFixture) mcpSend(requestID string, overrides map[string]string) (*Response, *mcpEgressPolicy) {
	f.t.Helper()
	policy := f.policyFor(requestID)
	return f.send(policy, requestID, overrides), policy
}

func (f *sendFixture) received() []*http.Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*http.Request{}, f.requests...)
}

func (f *sendFixture) promptCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.prompts)
}

// collectionRuntimeVariable reads a persisted runtime variable back out of
// state. The §3 tests are all assertions about this returning nothing.
func (f *sendFixture) collectionRuntimeVariable(name string) (string, bool) {
	f.app.mu.Lock()
	defer f.app.mu.Unlock()
	for _, variable := range f.app.state.Workspaces[0].Collections[0].RuntimeVariables {
		if variable.Name == name {
			return fmt.Sprint(variable.Value), true
		}
	}
	return "", false
}

// otherOrigin is a second listener: a destination nothing in the fixture's
// definitions points at, and the one every "outside Base" test aims at.
func newOtherOrigin(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"reached":true}`))
	}))
	t.Cleanup(server.Close)
	return server, &hits
}

func requireDenied(t *testing.T, response *Response, what string) {
	t.Helper()
	if response.Status != 0 {
		t.Fatalf("%s: the send completed with status %d instead of being refused", what, response.Status)
	}
	if !strings.Contains(response.Error, mcpserver.ErrDenied.Error()) {
		t.Fatalf("%s: the failure is not a refusal: %q", what, response.Error)
	}
}

// --- §3: nothing an MCP run computes reaches AppState ----------------------

// THE LAUNDERING CHANNEL, CLOSED AT ITS ROOT. A script derives a value from
// agent input and bru.setVars it. On the persisted path that value lands in
// collection.RuntimeVariables, so the NEXT run's mcpRunPlan reads it back as
// definition state and it enters Base — the agent has widened the boundary by
// writing to it.
//
// The assertion is therefore about AppState, not about the response: the run
// itself succeeds, the variable is visible for the rest of the execution, and
// nothing is written.
func TestMCPScriptVariableMutationNeverPersists(t *testing.T) {
	fixture := newSendFixture(t)
	other, _ := newOtherOrigin(t)
	requestID := fixture.addRequest("Launders a host", "{{baseUrl}}/ok", func(item *types.RequestItem) {
		item.PostScript = "bru.setVar('launderedBase', pm.variables.get('agentHost'));"
	})

	response, policy := fixture.mcpSend(requestID, map[string]string{"agentHost": other.URL})
	if response.Status != http.StatusOK {
		t.Fatalf("the run itself failed: status=%d error=%q", response.Status, response.Error)
	}

	// Not in state.
	if value, ok := fixture.collectionRuntimeVariable("launderedBase"); ok {
		t.Fatalf("an agent run's setVar was persisted as %q; the next run's Base would include it", value)
	}
	// In the overlay, where the rest of THIS execution can see it and nothing
	// else can. Absence here would mean the flow regression §3 exists to avoid.
	if got := policy.overlay.variables(mcpOverlayRuntime)["launderedBase"]; fmt.Sprint(got) != other.URL {
		t.Fatalf("the overlay did not capture the setVar: %v", policy.overlay.variables(mcpOverlayRuntime))
	}
}

// The same absence on the DENIED path, which is where the original finding
// lived: persistence used to run even for a send that never happened, so a
// refusal still taught the next run a new host.
func TestMCPDeniedRunPersistsNothing(t *testing.T) {
	fixture := newSendFixture(t)
	other, hits := newOtherOrigin(t)
	requestID := fixture.addRequest("Retargeted and refused", "{{baseUrl}}/ok", func(item *types.RequestItem) {
		item.PreScript = "bru.setVar('launderedBase', pm.variables.get('agentHost'));"
	})

	response, _ := fixture.mcpSend(requestID, map[string]string{
		"agentHost": other.URL,
		"baseUrl":   other.URL,
	})
	requireDenied(t, response, "a retargeted send")

	if hits.Load() != 0 {
		t.Fatalf("the refused send still reached the target: %d requests", hits.Load())
	}
	if value, ok := fixture.collectionRuntimeVariable("launderedBase"); ok {
		t.Fatalf("a REFUSED run persisted %q; a denial that still teaches the next run is not a denial", value)
	}
	// The cookie half of the same rule.
	fixture.app.mu.Lock()
	cookies := len(fixture.app.state.Cookies)
	fixture.app.mu.Unlock()
	if cookies != 0 {
		t.Errorf("a refused agent run wrote %d cookies into state", cookies)
	}
}

// §3's other half: within-run continuity survives. Two sends under ONE policy
// — the shape runFlow produces, since every step calls the same function with
// the same context — and then a THIRD under a fresh policy, which must not see
// it.
func TestMCPFlowOverlayScopedToExecution(t *testing.T) {
	fixture := newSendFixture(t)
	setterID := fixture.addRequest("Step 1 sets a token", "{{baseUrl}}/login", func(item *types.RequestItem) {
		item.PostScript = "bru.setVar('sessionToken', 'from-step-1');"
	})
	readerID := fixture.addRequest("Step 3 reads it", "{{baseUrl}}/use", func(item *types.RequestItem) {
		item.Headers = []KeyValue{{Name: "X-Echo", Value: "{{sessionToken}}", Enabled: true}}
	})

	policy := fixture.policyFor(setterID)
	if response := fixture.send(policy, setterID, nil); response.Status != http.StatusOK {
		t.Fatalf("step 1 failed: %#v", response.Error)
	}
	// A flow replaces the scope per step; the overlay is per EXECUTION and
	// survives that.
	mcpEnterScope(policy, newMCPSiteLabelBook(), fixture.plan(t, readerID))
	if response := fixture.send(policy, readerID, nil); response.Status != http.StatusOK {
		t.Fatalf("step 3 failed: %#v", response.Error)
	}
	received := fixture.received()
	if got := received[len(received)-1].Header.Get("X-Echo"); got != "from-step-1" {
		t.Fatalf("step 3 did not see step 1's setVar (X-Echo=%q); cross-step continuity is the reason the overlay exists", got)
	}

	// A NEW execution starts clean, because the overlay died with the old one
	// and nothing was written to state.
	if _, ok := fixture.collectionRuntimeVariable("sessionToken"); ok {
		t.Fatal("the overlay leaked into AppState")
	}
	fresh := fixture.policyFor(readerID)
	if response := fixture.send(fresh, readerID, nil); response.Status != http.StatusOK {
		t.Fatalf("the fresh run failed: %#v", response.Error)
	}
	received = fixture.received()
	// An unresolved variable stays literal, which is the honest signal that
	// nothing supplied it — the overlay died with the execution that built it.
	if got := received[len(received)-1].Header.Get("X-Echo"); got != "{{sessionToken}}" {
		t.Fatalf("a new execution still saw the previous one's setVar: X-Echo=%q", got)
	}
}

func (f *sendFixture) plan(t *testing.T, requestID string) mcpRunPlan {
	t.Helper()
	plan, err := f.app.mcpRunPlan(f.collectionID, requestID, f.environmentID)
	if err != nil {
		t.Fatalf("mcpRunPlan(%q): %v", requestID, err)
	}
	return plan
}

// --- §2 row 4 / §5 row 14: PAC is refused before anything is fetched -------

func TestMCPPACRefusedBeforeFetch(t *testing.T) {
	var pacFetches atomic.Int32
	pac := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pacFetches.Add(1)
		_, _ = w.Write([]byte("function FindProxyForURL(u, h) { return 'PROXY 127.0.0.1:1'; }"))
	}))
	defer pac.Close()

	fixture := newSendFixture(t)
	fixture.app.mu.Lock()
	fixture.app.state.Preferences.ProxyMode = "on"
	fixture.app.state.Preferences.Proxy.Source = "pac"
	fixture.app.state.Preferences.Proxy.PAC.Source = pac.URL
	fixture.app.mu.Unlock()

	requestID := fixture.addRequest("Through a PAC", "{{baseUrl}}/ok", nil)
	response, _ := fixture.mcpSend(requestID, nil)

	if response.Status != 0 {
		t.Fatalf("the send completed through a PAC proxy: status=%d", response.Status)
	}
	if !strings.Contains(response.Error, "PAC") {
		t.Fatalf("the refusal does not name PAC: %q", response.Error)
	}
	// THE ASSERTION THAT MATTERS. A PAC file is a remote JavaScript program with
	// its own fetch and its own DNS; refusing after loading it would refuse
	// nothing.
	if pacFetches.Load() != 0 {
		t.Fatalf("the PAC file was fetched %d times before the refusal", pacFetches.Load())
	}
	// And a UI send of the same request still evaluates it — the refusal is
	// provenance-conditioned, not a global switch.
	if response := fixture.send(nil, requestID, nil); strings.Contains(response.Error, "Agent-initiated") {
		t.Fatalf("a UI send was refused by an agent-only rule: %q", response.Error)
	}
	if pacFetches.Load() == 0 {
		t.Fatal("the UI send did not consult the PAC file, so this test proved nothing about provenance")
	}
}

// --- §4.4: transport construction with agent-free values -------------------

// certFixture is a collection holding one client certificate whose domain is a
// TEMPLATE. Which host that template resolves to is the whole question: the
// agent-free context says one thing, the runtime context another.
func (f *sendFixture) addClientCertificate(domain string) {
	f.t.Helper()
	certPEM, keyPEM, _, _ := testClientCertificate(f.t)
	f.app.mu.Lock()
	defer f.app.mu.Unlock()
	collection := &f.app.state.Workspaces[0].Collections[0]
	if err := os.WriteFile(filepath.Join(collection.Path, "client.pem"), certPEM, 0o600); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(collection.Path, "client.key"), keyPEM, 0o600); err != nil {
		f.t.Fatal(err)
	}
	collection.ClientCertificates = []ClientCertificateConfig{{
		Domain:       domain,
		Type:         "cert",
		CertFilePath: "client.pem",
		KeyFilePath:  "client.key",
	}}
}

// posture is the §4.4 decision for one (scope, runtime target) pair, taken
// through the engine's own seam.
func (f *sendFixture) posture(policy *mcpEgressPolicy, targetURL string) (mcpTransportPosture, error) {
	f.t.Helper()
	ctx := mcpContextWithPolicy(context.Background(), policy)
	return f.app.mcpTransportPosture(ctx, policy, f.collectionID, targetURL)
}

// Cert test 1. The agent supplies the variable the certificate's domain
// template reads. It must not change which certificate matches.
func TestMCPClientCertSelectionIgnoresAgentVariables(t *testing.T) {
	fixture := newSendFixture(t)
	fixture.setEnvironmentVariable("certHost", "api.example.com")
	fixture.addClientCertificate("{{certHost}}")
	requestID := fixture.addRequest("mTLS", "https://api.example.com/charge", nil)

	policy := fixture.policyFor(requestID)
	// The agent's own value is not in baseVars, so the scope cannot see it. The
	// posture is computed against the real target either way.
	posture, err := fixture.posture(policy, "https://api.example.com/charge")
	if err != nil {
		t.Fatalf("posture: %v", err)
	}
	if posture.cert == nil {
		t.Fatal("the agent-free context resolved the domain template to the request's own host, so a certificate should have matched")
	}
	if posture.certOrigin != mustOrigin(t, "https://api.example.com") {
		t.Fatalf("certOrigin is %v, want the agent-free main destination", posture.certOrigin)
	}
}

// Cert test 2. The matching seam is handed the SCOPE's main URL, never the
// runtime target — so a template that resolves to the runtime target and
// nothing else matches nothing.
func TestMCPClientCertMatchingSeesOnlyTheAgentFreeMainURL(t *testing.T) {
	fixture := newSendFixture(t)
	fixture.setEnvironmentVariable("certHost", "attacker.example")
	fixture.addClientCertificate("{{certHost}}")
	requestID := fixture.addRequest("mTLS", "https://api.example.com/charge", nil)

	policy := fixture.policyFor(requestID)
	posture, err := fixture.posture(policy, "https://api.example.com/charge")
	if err != nil {
		t.Fatalf("posture: %v", err)
	}
	// The definition points at api.example.com; the certificate is configured
	// for attacker.example. Matching against the agent-free MAIN URL therefore
	// finds nothing, which is the correct answer: the certificate belongs to a
	// host this request does not contact.
	if posture.cert != nil {
		t.Fatal("a certificate configured for another host was matched against the request's own destination")
	}
}

// Cert test 3. Matched, but the actual egress is somewhere else: the
// certificate is not attached, and no key file is even opened.
func TestMCPClientCertNotAttachedOffCertOrigin(t *testing.T) {
	fixture := newSendFixture(t)
	fixture.setEnvironmentVariable("certHost", "api.example.com")
	fixture.addClientCertificate("{{certHost}}")
	requestID := fixture.addRequest("mTLS", "https://api.example.com/charge", nil)
	policy := fixture.policyFor(requestID)

	// Break the key file. If the equality check ran AFTER the load, this would
	// surface as an error instead of a clean cert-free posture — which is the
	// point of checking first: an off-certOrigin send must not be failed by a
	// certificate it would never present.
	fixture.app.mu.Lock()
	collectionPath := fixture.app.state.Workspaces[0].Collections[0].Path
	fixture.app.mu.Unlock()
	if err := os.WriteFile(filepath.Join(collectionPath, "client.key"), []byte("not a key"), 0o600); err != nil {
		t.Fatal(err)
	}

	posture, err := fixture.posture(policy, "https://elsewhere.example/charge")
	if err != nil {
		t.Fatalf("an off-certOrigin send was failed by a certificate it would never present: %v", err)
	}
	if posture.cert != nil {
		t.Fatal("the client certificate was attached to a transport dialing a different origin")
	}
	if posture.certOrigin.valid() {
		t.Fatalf("a cert-free posture still reported a confinement origin: %v", posture.certOrigin)
	}
}

// Cert test 4. §2 row 6: a client certificate plus an https proxy is refused,
// because CONNECT to an https proxy is itself a TLS handshake and the
// certificate is in the transport's config for every dial it makes.
func TestMCPClientCertWithHTTPSProxyRefused(t *testing.T) {
	fixture := newSendFixture(t)
	fixture.setEnvironmentVariable("certHost", "api.example.com")
	fixture.addClientCertificate("{{certHost}}")
	requestID := fixture.addRequest("mTLS", "https://api.example.com/charge", nil)
	fixture.setProxy(types.ProxyConfig{
		Protocol: "https",
		Hostname: "proxy.example",
		Port:     "8443",
		Auth:     types.ProxyAuthConfig{Disabled: true},
	})
	policy := fixture.policyFor(requestID)

	_, err := fixture.posture(policy, "https://api.example.com/charge")
	if err == nil {
		t.Fatal("a client certificate was allowed through an HTTPS proxy")
	}
	if !errors.Is(err, mcpserver.ErrDenied) {
		t.Errorf("the refusal does not wrap ErrDenied: %v", err)
	}
	if !strings.Contains(err.Error(), "HTTPS proxy") {
		t.Errorf("the refusal does not name the reason: %v", err)
	}

	// The control: WITHOUT a certificate the same https proxy is fine — it is
	// the combination that is refused, not the proxy.
	fixture.app.mu.Lock()
	fixture.app.state.Workspaces[0].Collections[0].ClientCertificates = nil
	fixture.app.mu.Unlock()
	if _, err := fixture.posture(fixture.policyFor(requestID), "https://api.example.com/charge"); err != nil {
		t.Fatalf("a cert-free send through an https proxy was refused: %v", err)
	}
}

// §4.4's freeze. A cert-bearing MCP transport discovers its system proxy
// EAGERLY and freezes the answer into the spec; no lazy closure survives, so a
// later change to the machine's configuration cannot reach a transport that
// carries the user's identity — and the two dispositions this phase refuses
// are refused before the spec is built at all.
func TestMCPCertTransportProxyDispositionFrozen(t *testing.T) {
	var pacFetches atomic.Int32
	pac := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pacFetches.Add(1)
		_, _ = w.Write([]byte("function FindProxyForURL(u, h) { return 'PROXY 127.0.0.1:1'; }"))
	}))
	defer pac.Close()

	fixture := newSendFixture(t)
	fixture.setEnvironmentVariable("certHost", "api.example.com")
	fixture.addClientCertificate("{{certHost}}")
	requestID := fixture.addRequest("mTLS", "https://api.example.com/charge", nil)
	// System mode: no collection proxy, no manual preference.
	fixture.app.mu.Lock()
	fixture.app.state.Preferences.ProxyMode = "on"
	fixture.app.state.Preferences.Proxy.Source = "system"
	fixture.app.mu.Unlock()
	policy := fixture.policyFor(requestID)
	target := "https://api.example.com/charge"

	// SAFE DISCOVERY: a plain http proxy for https traffic, i.e. the ordinary
	// CONNECT case. Set through the environment so the answer is deterministic
	// on every platform — DiscoverSystemProxy consults it before anything else.
	t.Setenv("LITEAPI_SYSTEM_PAC_URL", "")
	t.Setenv("NO_PROXY", "")
	t.Setenv("HTTPS_PROXY", "http://proxy.local:3128")
	frozen, err := fixture.posture(policy, target)
	if err != nil {
		t.Fatalf("posture under safe discovery: %v", err)
	}
	if frozen.cert == nil {
		t.Fatal("the certificate did not attach, so this test is not about a cert-bearing transport")
	}
	if frozen.proxyMode != xport.ProxyExplicit || frozen.proxyURL == nil || frozen.proxyURL.Host != "proxy.local:3128" {
		t.Fatalf("the discovered proxy was not frozen into the spec: mode=%q url=%v", frozen.proxyMode, frozen.proxyURL)
	}
	if frozen.refusePAC {
		t.Error("a frozen posture asked for the per-dial PAC refusal, which means a lazy closure survived")
	}

	// The freeze reaches the CACHE KEY. Both the certificate digest and the
	// disposition are in it, so a transport built under one posture can never
	// be handed to a send under another.
	var frozenSpec, otherSpec xport.Spec
	frozen.applyToSpec(&frozenSpec, target)
	otherPosture := frozen
	otherPosture.proxyMode, otherPosture.proxyURL = xport.ProxyOff, nil
	otherPosture.applyToSpec(&otherSpec, target)
	if frozenSpec.ProxyMode == xport.ProxySystem {
		t.Error("the frozen spec still carries a per-request system-proxy closure")
	}
	if frozenSpec.ClientCertDigest == "" {
		t.Error("the certificate digest is not in the spec, so two identities would share a transport")
	}
	if frozenSpec.CacheKey() == otherSpec.CacheKey() {
		t.Error("two different proxy dispositions hash to the same cache key")
	}

	// FLIP THE MACHINE TO PAC. The next send re-discovers and refuses; the PAC
	// file is never fetched and never evaluated.
	t.Setenv("HTTPS_PROXY", "")
	t.Setenv("LITEAPI_SYSTEM_PAC_URL", pac.URL)
	if _, err := fixture.posture(policy, target); err == nil {
		t.Fatal("a PAC disposition was accepted for a cert-bearing transport")
	} else if !strings.Contains(err.Error(), "PAC") {
		t.Errorf("the refusal does not name PAC: %v", err)
	}
	if pacFetches.Load() != 0 {
		t.Fatalf("the PAC file was fetched %d times", pacFetches.Load())
	}

	// FLIP THE MACHINE TO AN HTTPS PROXY. The certificate must never cross one.
	t.Setenv("LITEAPI_SYSTEM_PAC_URL", "")
	t.Setenv("HTTPS_PROXY", "https://proxy.local:8443")
	if _, err := fixture.posture(policy, target); err == nil {
		t.Fatal("a cert-bearing transport was pointed at an https proxy")
	} else if !strings.Contains(err.Error(), "HTTPS proxy") {
		t.Errorf("the refusal does not name the reason: %v", err)
	}
}

// §5 row 15. The manual proxy is resolved with the scope's agent-free
// variables, so an agent that overrides the variable the proxy host is spelled
// with changes nothing.
func TestMCPManualProxyAgentFree(t *testing.T) {
	fixture := newSendFixture(t)
	fixture.setEnvironmentVariable("proxyHost", "proxy.internal")
	fixture.setProxy(types.ProxyConfig{
		Protocol: "http",
		Hostname: "{{proxyHost}}",
		Port:     "3128",
		Auth:     types.ProxyAuthConfig{Disabled: true},
	})
	requestID := fixture.addRequest("Through the proxy", "https://api.example.com/charge", nil)
	policy := fixture.policyFor(requestID)

	posture, err := fixture.posture(policy, "https://api.example.com/charge")
	if err != nil {
		t.Fatalf("posture: %v", err)
	}
	if posture.proxyURL == nil || posture.proxyURL.Host != "proxy.internal:3128" {
		t.Fatalf("the manual proxy resolved to %v, want the definition's own value", posture.proxyURL)
	}
	// The proxy origin is in Base for kind `proxy`, which is what lets the
	// authorization inside the posture succeed. proxy-class has no approval
	// path, so a disagreement here would be a refusal rather than a prompt.
	if !policy.mustScope(t).allows(mustOrigin(t, "http://proxy.internal:3128"), egressKindProxy) {
		t.Fatal("the agent-free proxy origin is not in Base, so the posture's own authorization was not exercised")
	}
}

func (p *mcpEgressPolicy) mustScope(t *testing.T) mcpScopeOrigins {
	t.Helper()
	scope, ok := p.activeScope()
	if !ok {
		t.Fatal("the policy has no active scope")
	}
	return scope
}

// --- §4.3 item 3: the transport backstop covers hops and retries -----------

// §5 row 3. The digest retry is a SECOND client.Do with a cloned request, and
// the clone preserves the context — so wrapping the transport covers it with no
// per-site code. The proof is that the guard saw a third round trip.
func TestMCPDigestRetryCovered(t *testing.T) {
	fixture := newSendFixture(t)
	var challenges atomic.Int32
	digestServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			challenges.Add(1)
			w.Header().Set("WWW-Authenticate", `Digest realm="test", nonce="abc", qop="auth"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer digestServer.Close()
	fixture.setEnvironmentVariable("digestUrl", digestServer.URL)

	requestID := fixture.addRequest("Digest", "{{digestUrl}}/secure", func(item *types.RequestItem) {
		item.Auth = types.AuthConfig{Mode: "digest", Username: "u", Password: "p"}
	})

	policy := fixture.policyFor(requestID)
	authorized := countAuthorizations(policy)
	response := fixture.send(policy, requestID, nil)
	if response.Status != http.StatusOK {
		t.Fatalf("the digest exchange failed: status=%d error=%q", response.Status, response.Error)
	}
	if challenges.Load() != 1 {
		t.Fatalf("the server issued %d challenges; this test needs exactly one retry", challenges.Load())
	}
	// One blocking checkpoint plus two backstop round trips: the original and
	// the retry. Fewer than three would mean the retry travelled unchecked.
	if got := authorized.Load(); got < 3 {
		t.Fatalf("the guard saw %d authorizations; the digest retry was not covered by the transport wrap", got)
	}
}

// countAuthorizations installs an audit hook that counts every decision the
// policy reaches. It is the only way to observe that the BACKSTOP ran as well
// as the checkpoint, since both allow and neither leaves a trace in the
// response.
func countAuthorizations(policy *mcpEgressPolicy) *atomic.Int32 {
	var count atomic.Int32
	policy.audit = func(mcpDefinitionSite, Origin, egressKind, string) { count.Add(1) }
	return &count
}

// §2 row 10 / §5 row 2. A redirect onto a new origin is DENIED rather than
// prompted — RoundTrip runs inside client.Timeout and cannot wait for a human —
// and a non-blocking prompt is raised so that an approve-and-remember makes the
// agent's retry succeed.
func TestMCPRedirectHopDeniedThenRememberable(t *testing.T) {
	fixture := newSendFixture(t)
	elsewhere, elsewhereHits := newOtherOrigin(t)
	fixture.app.mu.Lock()
	fixture.app.state.Workspaces[0].Collections[0].Environments[len(fixture.app.state.Workspaces[0].Collections[0].Environments)-1].Variables = append(
		fixture.app.state.Workspaces[0].Collections[0].Environments[len(fixture.app.state.Workspaces[0].Collections[0].Environments)-1].Variables,
		Variable{ID: "v-elsewhere", Name: "elsewhere", Value: elsewhere.URL, Enabled: true},
	)
	fixture.app.mu.Unlock()

	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL+"/landed", http.StatusFound)
	}))
	defer redirector.Close()
	fixture.setEnvironmentVariable("baseUrl", redirector.URL)

	requestID := fixture.addRequest("Redirects away", "{{baseUrl}}/start", nil)
	policy := fixture.policyFor(requestID)

	response := fixture.send(policy, requestID, nil)
	requireDenied(t, response, "a redirect onto a new origin")
	if elsewhereHits.Load() != 0 {
		t.Fatalf("the redirect target was reached %d times despite the refusal", elsewhereHits.Load())
	}
	// The non-blocking prompt is what makes the denial actionable rather than a
	// dead end. It is raised on its own goroutine, so give it a moment.
	deadline := time.Now().Add(2 * time.Second)
	for fixture.promptCount() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if fixture.promptCount() == 0 {
		t.Fatal("the denial raised no prompt, so an approve-and-remember could never happen")
	}

	// REMEMBERED, and the retry succeeds. The persisted store is the real one:
	// a fresh policy for the same run reads it back through the same callback
	// the engine installs.
	elsewhereOrigin := mustOrigin(t, elsewhere.URL)
	site := fixture.plan(t, requestID).site
	if err := fixture.app.rememberMCPApproval(site, elsewhereOrigin, kindClassRequest); err != nil {
		t.Fatalf("remember the approval: %v", err)
	}
	retry := fixture.send(fixture.policyFor(requestID), requestID, nil)
	if retry.Status != http.StatusOK {
		t.Fatalf("the retry after approve-and-remember failed: status=%d error=%q", retry.Status, retry.Error)
	}
	if elsewhereHits.Load() != 1 {
		t.Fatalf("the approved retry reached the target %d times, want 1", elsewhereHits.Load())
	}
}

// --- §5 rows 12 and 13: script egress ---------------------------------------

func TestMCPScriptSendRequestBlockedOutsideScope(t *testing.T) {
	fixture := newSendFixture(t)
	other, hits := newOtherOrigin(t)
	requestID := fixture.addRequest("Script exfiltrates", "{{baseUrl}}/ok", func(item *types.RequestItem) {
		item.PreScript = `
const res = await bru.sendRequest({ method: "GET", url: "` + other.URL + `/steal" });
bru.setVar("scriptStatus", String(res.status));
`
	})

	response, _ := fixture.mcpSend(requestID, nil)
	if hits.Load() != 0 {
		t.Fatalf("a script send reached an origin outside the scope %d times", hits.Load())
	}
	if !strings.Contains(response.Error, mcpserver.ErrDenied.Error()) &&
		!scriptErrorMentionsDenial(response) {
		t.Fatalf("the script send was blocked but nothing said so: %#v", response)
	}
}

func TestMCPScriptFetchBlockedOutsideScope(t *testing.T) {
	fixture := newSendFixture(t)
	other, hits := newOtherOrigin(t)
	requestID := fixture.addRequest("fetch() exfiltrates", "{{baseUrl}}/ok", func(item *types.RequestItem) {
		item.PreScript = `await fetch("` + other.URL + `/steal");`
	})

	response, _ := fixture.mcpSend(requestID, nil)
	if hits.Load() != 0 {
		t.Fatalf("fetch() reached an origin outside the scope %d times", hits.Load())
	}
	if !strings.Contains(response.Error, mcpserver.ErrDenied.Error()) &&
		!scriptErrorMentionsDenial(response) {
		t.Fatalf("the fetch was blocked but nothing said so: %#v", response)
	}
}

// The control that keeps the three refusals above honest: a script send to the
// request's OWN origin passes, because §1.4(2) makes an origin the request
// already reaches reachable by its script too.
func TestMCPScriptSameOriginSendPasses(t *testing.T) {
	fixture := newSendFixture(t)
	requestID := fixture.addRequest("Script calls its own API", "{{baseUrl}}/ok", func(item *types.RequestItem) {
		item.PreScript = `
const res = await bru.sendRequest({ method: "GET", url: bru.getEnvVar("baseUrl") + "/sibling" });
bru.setVar("scriptStatus", String(res.status));
`
	})

	response, policy := fixture.mcpSend(requestID, nil)
	if response.Status != http.StatusOK {
		t.Fatalf("a same-origin script send broke the run: %#v", response.Error)
	}
	if got := fmt.Sprint(policy.overlay.variables(mcpOverlayRuntime)["scriptStatus"]); got != "200" {
		t.Fatalf("the same-origin script send did not complete: status=%q", got)
	}
	sawSibling := false
	for _, request := range fixture.received() {
		if request.URL.Path == "/sibling" {
			sawSibling = true
		}
	}
	if !sawSibling {
		t.Fatal("the same-origin script send never reached the server")
	}
}

// §5 row 13. A name lookup is authorized against the scope's HOSTNAMES, and
// refused rather than prompted: a lookup has no origin for an approval to be
// keyed on.
func TestMCPScriptDNSLookupBlockedOutsideScope(t *testing.T) {
	fixture := newSendFixture(t)
	// node:dns exists only in developer sandbox mode, which is the only mode
	// where this egress is reachable at all.
	fixture.app.mu.Lock()
	fixture.app.state.Workspaces[0].Collections[0].SecurityConfig.JSSandboxMode = "developer"
	fixture.app.mu.Unlock()
	requestID := fixture.addRequest("Script resolves a name", "{{baseUrl}}/ok", func(item *types.RequestItem) {
		item.PreScript = `
const dns = require("node:dns");
try {
  await dns.promises.lookup("exfiltration.example");
  bru.setVar("dnsOutcome", "resolved");
} catch (e) {
  bru.setVar("dnsOutcome", String(e));
}
`
	})

	_, policy := fixture.mcpSend(requestID, nil)
	outcome := fmt.Sprint(policy.overlay.variables(mcpOverlayRuntime)["dnsOutcome"])
	if outcome == "resolved" {
		t.Fatal("a script resolved a hostname nothing in the request's definition names")
	}
	if !strings.Contains(outcome, mcpserver.ErrDenied.Error()) {
		t.Fatalf("the lookup failed for the wrong reason: %q", outcome)
	}
}

// scriptErrorMentionsDenial looks for the refusal wherever a script failure
// surfaces — the response error on the pre-request path, or a test row.
func scriptErrorMentionsDenial(response *Response) bool {
	if strings.Contains(response.Error, mcpserver.ErrDenied.Error()) {
		return true
	}
	for _, result := range response.TestResults {
		if strings.Contains(result.Message, mcpserver.ErrDenied.Error()) {
			return true
		}
	}
	for _, log := range response.ScriptLogs {
		if strings.Contains(log.Message, mcpserver.ErrDenied.Error()) {
			return true
		}
	}
	return false
}

// --- §5 row 11: nested bru.runRequest ---------------------------------------

// A nested run gets ITS OWN scope, computed from the nested definition. So a
// nested target the agent has retargeted out of that definition's Base is
// blocked — and the parent's Base does not rescue it, because nothing unions
// across scopes.
func TestMCPNestedRunRequestRetargetBlocked(t *testing.T) {
	fixture := newSendFixture(t)
	other, hits := newOtherOrigin(t)
	fixture.setEnvironmentVariable("nestedBase", fixture.baseURL)
	fixture.addRequest("Nested target", "{{nestedBase}}/nested", nil)
	// THE RETARGET IS SCRIPT-DRIVEN, which is the shape that matters: a setVar
	// outranks the environment, and it is visible to the nested send through the
	// inherited runtime scope — so the nested request really is pointed
	// somewhere its definition never named. The nested SCOPE, computed at
	// bru.runRequest entry from the stored definition under the run's one
	// agent-free variable context (§4.6), still holds only the fixture server.
	parentID := fixture.addRequest("Runs the nested one", "{{baseUrl}}/ok", func(item *types.RequestItem) {
		item.PreScript = `
bru.setVar("nestedBase", "` + other.URL + `");
const res = await bru.runRequest("Nested target");
bru.setVar("nestedStatus", String(res.status));
bru.setVar("nestedError", String(res.error || ""));
bru.setVar("nestedURL", String(res.url || ""));
`
	})

	response, policy := fixture.mcpSend(parentID, nil)
	if got := fmt.Sprint(policy.overlay.variables(mcpOverlayRuntime)["nestedURL"]); !strings.HasPrefix(got, other.URL) {
		t.Fatalf("the nested send was not actually retargeted (url=%q), so this test proved nothing", got)
	}
	if hits.Load() != 0 {
		t.Fatalf("the retargeted nested request reached the other origin %d times", hits.Load())
	}
	nestedError := fmt.Sprint(policy.overlay.variables(mcpOverlayRuntime)["nestedError"])
	if !strings.Contains(nestedError, mcpserver.ErrDenied.Error()) {
		t.Fatalf("the nested send was not refused: error=%q response=%#v", nestedError, response.Error)
	}
	// And the scope stack is back where it started: every PushScope is paired
	// with an immediate defer PopScope.
	if depth := policy.scopeDepth(); depth != 1 {
		t.Fatalf("the nested scope was not popped: depth=%d", depth)
	}
}

// The control. A nested request that stays inside its OWN definition's Base
// runs — otherwise the test above would pass with bru.runRequest simply broken.
func TestMCPNestedRunRequestLegitSiblingPasses(t *testing.T) {
	fixture := newSendFixture(t)
	fixture.setEnvironmentVariable("nestedBase", fixture.baseURL)
	fixture.addRequest("Nested target", "{{nestedBase}}/nested", nil)
	parentID := fixture.addRequest("Runs the nested one", "{{baseUrl}}/ok", func(item *types.RequestItem) {
		item.PreScript = `
const res = await bru.runRequest("Nested target");
bru.setVar("nestedStatus", String(res.status));
`
	})

	response, policy := fixture.mcpSend(parentID, nil)
	if response.Status != http.StatusOK {
		t.Fatalf("the parent send failed: %#v", response.Error)
	}
	if got := fmt.Sprint(policy.overlay.variables(mcpOverlayRuntime)["nestedStatus"]); got != "200" {
		t.Fatalf("the legitimate nested send did not complete: status=%q", got)
	}
	sawNested := false
	for _, request := range fixture.received() {
		if request.URL.Path == "/nested" {
			sawNested = true
		}
	}
	if !sawNested {
		t.Fatal("the nested request never reached the server")
	}
}

// --- §1.2(4): a UI send is never subjected to any of this -------------------

// Every capability §2 removes from an agent run is exercised here by a send
// with NO policy on its context, and every one of them still works.
func TestUISendUnaffectedByPolicy(t *testing.T) {
	fixture := newSendFixture(t)
	other, hits := newOtherOrigin(t)

	t.Run("an origin outside any definition is contacted without a prompt", func(t *testing.T) {
		requestID := fixture.addRequest("Anywhere", other.URL+"/ui", nil)
		response := fixture.send(nil, requestID, nil)
		if response.Status != http.StatusOK {
			t.Fatalf("a UI send to a new origin failed: %#v", response.Error)
		}
		if hits.Load() == 0 {
			t.Fatal("the UI send never reached the target")
		}
		if fixture.promptCount() != 0 {
			t.Fatalf("a UI send raised %d approval prompts", fixture.promptCount())
		}
	})

	t.Run("script variables still persist", func(t *testing.T) {
		requestID := fixture.addRequest("Persists", "{{baseUrl}}/ok", func(item *types.RequestItem) {
			item.PostScript = "bru.setVar('uiPersisted', 'yes');"
		})
		if response := fixture.send(nil, requestID, nil); response.Status != http.StatusOK {
			t.Fatalf("the UI send failed: %#v", response.Error)
		}
		if value, ok := fixture.collectionRuntimeVariable("uiPersisted"); !ok || value != "yes" {
			t.Fatalf("a UI send's setVar did not persist: value=%q ok=%v", value, ok)
		}
	})

	t.Run("a script may contact any origin", func(t *testing.T) {
		before := hits.Load()
		requestID := fixture.addRequest("Script anywhere", "{{baseUrl}}/ok", func(item *types.RequestItem) {
			item.PreScript = `await bru.sendRequest({ method: "GET", url: "` + other.URL + `/from-ui-script" });`
		})
		if response := fixture.send(nil, requestID, nil); response.Status != http.StatusOK {
			t.Fatalf("a UI send whose script called elsewhere failed: %#v", response.Error)
		}
		if hits.Load() == before {
			t.Fatal("the UI script's send was blocked")
		}
	})
}

// The cost of the nil-policy path, which every UI send pays: one context
// lookup at the checkpoint and one per round trip in the guard.
func BenchmarkUISendNilPolicyOverhead(b *testing.B) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	ctx := context.Background()
	parsed, err := url.Parse(server.URL)
	if err != nil {
		b.Fatal(err)
	}
	guard := mcpSendTransport(http.DefaultTransport, Origin{})
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if policy := mcpPolicyFromContext(ctx); policy != nil {
			b.Fatal("the benchmark is not measuring the nil-policy path")
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
		if err != nil {
			b.Fatal(err)
		}
		response, err := guard.RoundTrip(request)
		if err != nil {
			b.Fatal(err)
		}
		_ = response.Body.Close()
	}
}

// --- the certificate confinement, as a unit --------------------------------

// §2 row 7. Not approvable, and not a policy question: the certificate is
// already in the transport's TLS config, so the only way to not present it to a
// redirect target is to not make the connection.
func TestMCPCertConfinedTransportRefusesOffOriginHop(t *testing.T) {
	confined := mcpCertConfinedTransport{
		base: roundTripperFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: http.NoBody}, nil
		}),
		certOrigin: mustOrigin(t, "https://api.example.com"),
	}

	onOrigin, err := http.NewRequest(http.MethodGet, "https://api.example.com/charge", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := confined.RoundTrip(onOrigin); err != nil {
		t.Fatalf("a request to certOrigin was refused: %v", err)
	}

	for _, target := range []string{
		"https://api.example.com:8443/charge", // another port is another origin
		"http://api.example.com/charge",       // a downgrade is another origin
		"https://elsewhere.example/charge",
	} {
		offOrigin, err := http.NewRequest(http.MethodGet, target, nil)
		if err != nil {
			t.Fatal(err)
		}
		_, err = confined.RoundTrip(offOrigin)
		if err == nil {
			t.Errorf("a hop to %s was allowed while a client certificate was attached", target)
			continue
		}
		if !errors.Is(err, mcpserver.ErrDenied) {
			t.Errorf("the %s refusal does not wrap ErrDenied: %v", target, err)
		}
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

// A guard against the posture helper silently losing the certificate when it
// writes onto a transport rather than a spec — the WebSocket path's only
// application of it.
func TestMCPTransportPostureAppliesToATransport(t *testing.T) {
	certificate := tls.Certificate{Certificate: [][]byte{{0x30, 0x00}}}
	posture := mcpTransportPosture{
		cert:      &certificate,
		proxyMode: xport.ProxyExplicit,
		proxyURL:  &url.URL{Scheme: "http", Host: "proxy.local:3128"},
	}
	transport := &http.Transport{}
	posture.applyToTransport(transport, "https://api.example.com/charge")

	if transport.TLSClientConfig == nil || len(transport.TLSClientConfig.Certificates) != 1 {
		t.Fatal("the certificate did not reach the WebSocket dialer's TLS config")
	}
	if transport.Proxy == nil {
		t.Fatal("the proxy disposition did not reach the WebSocket dialer")
	}
	request, err := http.NewRequest(http.MethodGet, "https://api.example.com/charge", nil)
	if err != nil {
		t.Fatal(err)
	}
	proxyURL, err := transport.Proxy(request)
	if err != nil || proxyURL == nil || proxyURL.Host != "proxy.local:3128" {
		t.Fatalf("the dialer's proxy resolved to %v (err %v)", proxyURL, err)
	}
}

// The PAC-refusing closure the CERT-FREE system-mode path installs: it refuses
// on every dial, before any fetch or evaluation.
func TestMCPCertFreeSystemProxyClosureRefusesPAC(t *testing.T) {
	var pacFetches atomic.Int32
	pac := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pacFetches.Add(1)
		_, _ = w.Write([]byte("function FindProxyForURL(u, h) { return 'DIRECT'; }"))
	}))
	defer pac.Close()

	t.Setenv("HTTP_PROXY", "")
	t.Setenv("HTTPS_PROXY", "")
	t.Setenv("ALL_PROXY", "")
	t.Setenv("NO_PROXY", "")
	t.Setenv("LITEAPI_SYSTEM_PAC_URL", pac.URL)

	posture := mcpTransportPosture{proxyMode: xport.ProxySystem, refusePAC: true}
	transport := &http.Transport{}
	posture.applyToTransport(transport, "http://api.example.com/charge")
	request, err := http.NewRequest(http.MethodGet, "http://api.example.com/charge", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := transport.Proxy(request); !errors.Is(err, xport.ErrSystemPACRefused) {
		t.Fatalf("the closure did not refuse a PAC disposition: %v", err)
	}
	if pacFetches.Load() != 0 {
		t.Fatalf("the PAC file was fetched %d times before the refusal", pacFetches.Load())
	}
	// And the message the user sees is §2 row 4's, not the sentinel's.
	if !strings.Contains(requestFailureMessage(&url.Error{Op: "Get", Err: xport.ErrSystemPACRefused}, "http://api.example.com"), "PAC") {
		t.Error("the send path does not restate the PAC refusal")
	}
}

// --- §4.3 item 2 / §5 row 6: the awsv4 guard is attached per context --------

// T0b owns the in-package contract (every credential endpoint is asked). This
// is the WIRING half: the send path installs a guard bound to THIS execution's
// policy, and narrows the kind so the backstop authorizes AWS traffic in the
// aws class rather than defaulting to `main` and denying an endpoint the
// in-package checkpoint just allowed.
func TestMCPAWSCredentialResolutionGated(t *testing.T) {
	policy := newMCPEgressPolicy()
	scope := testScope(t, "req_aws", "https://api.example.com")
	scope.add(egressKindAWS, mustOrigin(t, "https://sts.eu-west-1.amazonaws.com"))
	policy.SetScope(scope)

	ctx := mcpContextWithPolicy(context.Background(), policy)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.example.com/charge", nil)
	if err != nil {
		t.Fatal(err)
	}
	signing := mcpAWSSigningRequest(ctx, request)

	// THE OUTGOING REQUEST IS UNTOUCHED. Narrowing its own context to `aws`
	// would make the transport backstop authorize the user's API call as a
	// credential call — the wrong Base, and a denial of a destination that is
	// perfectly in scope.
	if mcpBackstopEgressKind(request.Context()) != egressKindMain {
		t.Fatal("signing narrowed the outgoing request's own egress kind")
	}
	if mcpBackstopEgressKind(signing.Context()) != egressKindAWS {
		t.Fatal("the signing context was not narrowed to the aws kind")
	}
	// The copy shares what Sign writes into: same URL object, same header map.
	if signing.URL != request.URL {
		t.Fatal("the signing copy does not share the request's URL, so a signature would cover a different one")
	}
	signing.Header.Set("X-Signing-Probe", "1")
	if request.Header.Get("X-Signing-Probe") != "1" {
		t.Fatal("the signing copy does not share the request's header map, so Sign's headers would never be sent")
	}
	request.Header.Del("X-Signing-Probe")

	// THE GUARD ITSELF, over the aws class.
	guard := mcpAWSEgressGuard(policy)
	if err := guard.AuthorizeCredentialEndpoint(ctx, "https://sts.eu-west-1.amazonaws.com/?Action=AssumeRole"); err != nil {
		t.Fatalf("an endpoint in Base(aws) was refused: %v", err)
	}
	err = guard.AuthorizeCredentialEndpoint(ctx, "https://sts.us-east-1.amazonaws.com/?Action=AssumeRole")
	if err == nil {
		t.Fatal("an AWS endpoint outside Base was allowed")
	}
	if !errors.Is(err, mcpserver.ErrDenied) {
		t.Errorf("the AWS refusal does not wrap ErrDenied: %v", err)
	}

	// AND THAT IT REALLY REACHED THE CONTEXT, observed through awsv4's own
	// guard-conditioned behaviour rather than a test-only accessor: with a
	// guard present, a profile that fails to resolve no longer falls back to
	// the literal keys the auth also carries (T0b), because a refusal must not
	// become a signed-with-different-credentials success.
	t.Setenv("AWS_CONFIG_FILE", filepath.Join(t.TempDir(), "no-config"))
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(t.TempDir(), "no-credentials"))
	t.Setenv("AWS_ACCESS_KEY_ID", "")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "")
	auth := types.AWSV4Auth{
		AccessKeyID:     "AKIAFALLBACK",
		SecretAccessKey: "fallback-secret",
		Service:         "execute-api",
		Region:          "eu-west-1",
		ProfileName:     "a-profile-that-does-not-exist",
	}
	resolve := func(value string) string { return value }

	uiRequest, err := http.NewRequest(http.MethodGet, "https://api.example.com/charge", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := awsv4.Sign(mcpAWSSigningRequest(context.Background(), uiRequest), auth, time.Now().UTC(), resolve); err != nil {
		t.Fatalf("a UI send lost awsv4's literal-key fallback: %v", err)
	}

	mcpRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.example.com/charge", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := awsv4.Sign(mcpAWSSigningRequest(ctx, mcpRequest), auth, time.Now().UTC(), resolve); err == nil {
		t.Fatal("an agent run fell back to literal keys after profile resolution failed, which means no guard was on the signing context")
	}

	// A UI send installs nothing and pays nothing.
	if plain := mcpAWSSigningRequest(context.Background(), request); plain != request {
		t.Fatal("a UI send's request was rewritten for signing")
	}
}
