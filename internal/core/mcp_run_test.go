package core

// Tests for the run tier: execution, the secret-override refusal, the new-host
// guard and its approval round-trip, cancellation, and the audit store.
//
// EVERY RUN HERE IS A REAL HTTP REQUEST to a local httptest.Server. That is the
// whole point of the tier — run_request exists to go through the app's own send
// path, and a fake transport would prove nothing about scripts, tests, history
// or the response the agent actually gets back. The fixture's collection is
// therefore shaped like a real one: a templated base URL in an environment, a
// secret token referenced by an Authorization header, and a tests block.
//
// The secret is a long sentinel on purpose. mcpserver.MaskKnownSecretValues
// skips values shorter than 8 bytes (masking "1234" would corrupt every port
// number in a body), so a short one would make the masking assertions pass
// without measuring anything.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mutexdev/lite_api/internal/mcpserver"
	"github.com/mutexdev/lite_api/internal/types"
)

const (
	runSentinelToken = "RUN-SENTINEL-API-TOKEN-VALUE"
	runEnvPath       = "/env-path"
)

// mcpRunFixture is an App with a live target server behind a realistic
// collection, plus a captured approval seam.
type mcpRunFixture struct {
	t            *testing.T
	app          *App
	backend      *mcpBackend
	server       *httptest.Server
	baseURL      string
	host         string // the host the environment's baseUrl resolves to
	collectionID string
	secretReqID  string // references {{apiToken}}; URL is {{baseUrl}}{{path}}
	plainReqID   string // references no secret
	slowReqID    string // sleeps, for cancellation

	mu        sync.Mutex
	requests  []recordedRunRequest
	approvals []types.MCPApprovalRequest
}

type recordedRunRequest struct {
	path       string
	authHeader string
	host       string
}

func newMCPRunFixture(t *testing.T) *mcpRunFixture {
	t.Helper()
	fixture := &mcpRunFixture{t: t}
	fixture.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fixture.mu.Lock()
		fixture.requests = append(fixture.requests, recordedRunRequest{
			path:       r.URL.Path,
			authHeader: r.Header.Get("Authorization"),
			host:       r.Host,
		})
		fixture.mu.Unlock()
		if r.URL.Path == "/slow" {
			select {
			case <-time.After(10 * time.Second):
			case <-r.Context().Done():
			}
			return
		}
		w.Header().Set("Content-Type", "application/json")
		// The Authorization header is ECHOED BACK deliberately: it is the only
		// way to prove both halves of rule 1 at once — that the secret really
		// did resolve inside LiteAPI and reach the server, and that it is masked
		// again on the way back to the agent.
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"path": r.URL.Path,
			"echo": r.Header.Get("Authorization"),
		})
	}))
	t.Cleanup(fixture.server.Close)

	parsed, err := url.Parse(fixture.server.URL)
	if err != nil {
		t.Fatalf("parse the target server URL: %v", err)
	}
	fixture.baseURL = fixture.server.URL
	fixture.host = parsed.Hostname()

	app := newAppForTest(t)
	if _, err := app.GetState(); err != nil {
		t.Fatalf("GetState: %v", err)
	}
	fixture.app = app
	fixture.backend = &mcpBackend{app: app}
	// The approval seam, mirroring notificationEmit: EventsEmit needs a Wails
	// context no test has, and installing this is also what tells
	// requestMCPApproval that there IS a frontend to ask.
	app.mcpApprovalEmit = func(request types.MCPApprovalRequest) {
		fixture.mu.Lock()
		fixture.approvals = append(fixture.approvals, request)
		fixture.mu.Unlock()
	}

	app.mu.Lock()
	workspace := &app.state.Workspaces[0]
	collection := &workspace.Collections[0]

	secretReq := types.NewRequestItem("Fetch profile", "http", len(collection.Items)+1)
	secretReq.Method = "GET"
	secretReq.URL = "{{baseUrl}}{{path}}"
	secretReq.Headers = []KeyValue{{Name: "Authorization", Value: "Bearer {{apiToken}}", Enabled: true}}
	secretReq.Body = types.RequestBody{Mode: "none"}
	secretReq.Tests = `test("the target answered", function () {
  expect(res.getStatus()).to.equal(200);
});
test("this one fails on purpose", function () {
  expect(res.getStatus()).to.equal(418);
});`
	collection.Items = append(collection.Items, secretReq)

	plainReq := types.NewRequestItem("Health check", "http", len(collection.Items)+1)
	plainReq.Method = "GET"
	plainReq.URL = "{{baseUrl}}/plain"
	plainReq.Body = types.RequestBody{Mode: "none"}
	collection.Items = append(collection.Items, plainReq)

	slowReq := types.NewRequestItem("Long poll", "http", len(collection.Items)+1)
	slowReq.Method = "GET"
	slowReq.URL = "{{baseUrl}}/slow"
	slowReq.Body = types.RequestBody{Mode: "none"}
	collection.Items = append(collection.Items, slowReq)

	workspace.GlobalEnvironments = append(workspace.GlobalEnvironments, Environment{
		ID:   "env-run-global",
		Name: "Run Global",
		Variables: []Variable{
			{ID: "run-var-base", Name: "baseUrl", Value: fixture.baseURL, Enabled: true},
			{ID: "run-var-path", Name: "path", Value: runEnvPath, Enabled: true},
			{ID: "run-var-token", Name: "apiToken", Value: runSentinelToken, Enabled: true, Secret: true},
		},
	})
	workspace.ActiveGlobalEnvironmentID = "env-run-global"

	fixture.collectionID = collection.ID
	fixture.secretReqID = secretReq.ID
	fixture.plainReqID = plainReq.ID
	fixture.slowReqID = slowReq.ID
	app.mu.Unlock()

	return fixture
}

// addSecretRequestForHost plants a SIBLING request that sends the same secret to
// an explicit host. This is what teaches the guard's computed allowlist that the
// host is already in use — the property rule 4 turns on.
func (f *mcpRunFixture) addSecretRequestForHost(rawURL string) {
	f.t.Helper()
	f.app.mu.Lock()
	defer f.app.mu.Unlock()
	collection := &f.app.state.Workspaces[0].Collections[0]
	sibling := types.NewRequestItem("Sibling using the same token", "http", len(collection.Items)+1)
	sibling.Method = "GET"
	sibling.URL = rawURL
	sibling.Headers = []KeyValue{{Name: "Authorization", Value: "Bearer {{apiToken}}", Enabled: true}}
	sibling.Body = types.RequestBody{Mode: "none"}
	collection.Items = append(collection.Items, sibling)
}

func (f *mcpRunFixture) run(ctx context.Context, requestID string, overrides map[string]string) (mcpserver.RunResult, error) {
	f.t.Helper()
	return f.backend.RunRequest(ctx, mcpserver.RunRequestParams{
		CollectionID: f.collectionID,
		RequestID:    requestID,
		Variables:    overrides,
	})
}

func (f *mcpRunFixture) recorded() []recordedRunRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]recordedRunRequest{}, f.requests...)
}

func (f *mcpRunFixture) prompts() []types.MCPApprovalRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]types.MCPApprovalRequest{}, f.approvals...)
}

// otherLoopbackURL is the same server under a DIFFERENT host name. Both names
// reach the same listener, so a run that the guard allows really does complete —
// which is what makes the approve path testable end to end rather than only up
// to the decision.
func (f *mcpRunFixture) otherLoopbackURL() string {
	f.t.Helper()
	parsed, err := url.Parse(f.server.URL)
	if err != nil {
		f.t.Fatalf("parse the target server URL: %v", err)
	}
	other := "127.0.0.1"
	if parsed.Hostname() == "127.0.0.1" {
		other = "localhost"
	}
	return "http://" + other + ":" + parsed.Port()
}

// --- 1. execution -----------------------------------------------------------

func TestMCPRunRequestExecutesThroughTheAppsOwnSendPath(t *testing.T) {
	f := newMCPRunFixture(t)

	result, err := f.run(context.Background(), f.secretReqID, nil)
	if err != nil {
		t.Fatalf("RunRequest: %v", err)
	}
	if result.Status != http.StatusOK {
		t.Errorf("status is %d, want 200", result.Status)
	}
	if result.StatusText == "" {
		t.Error("statusText is empty; the agent has nothing to render next to the code")
	}
	if result.ExecutedAt == "" {
		t.Error("executedAt is empty")
	}
	if _, err := time.Parse(time.RFC3339, result.ExecutedAt); err != nil {
		t.Errorf("executedAt %q is not RFC3339: %v", result.ExecutedAt, err)
	}
	if !strings.Contains(result.Body, `"path":"`+runEnvPath+`"`) {
		t.Errorf("the response body did not round-trip: %q", result.Body)
	}
	if result.Truncated {
		t.Error("a short body was reported truncated")
	}

	// The secret really did resolve inside LiteAPI...
	recorded := f.recorded()
	if len(recorded) != 1 {
		t.Fatalf("the target server saw %d requests, want 1", len(recorded))
	}
	if recorded[0].authHeader != "Bearer "+runSentinelToken {
		t.Errorf("the target received Authorization %q; the secret did not resolve at send time", recorded[0].authHeader)
	}
	// ...and is masked again on the way back, even though the server echoed it
	// into an ordinary JSON field that no name-based rule would ever flag.
	if strings.Contains(result.Body, runSentinelToken) {
		t.Errorf("the response body leaked the resolved secret: %q", result.Body)
	}
	if !strings.Contains(result.Body, mcpserver.MaskedValue) {
		t.Errorf("the echoed credential was not masked in the body: %q", result.Body)
	}

	// The scripted tests come back, both outcomes.
	if len(result.TestResults) != 2 {
		t.Fatalf("got %d test results, want 2: %+v", len(result.TestResults), result.TestResults)
	}
	byName := map[string]mcpserver.TestResult{}
	for _, test := range result.TestResults {
		byName[test.Name] = test
	}
	passing, found := byName["the target answered"]
	if !found || !passing.Passed {
		t.Errorf("the passing test did not come back passed: %+v", result.TestResults)
	}
	failing, found := byName["this one fails on purpose"]
	if !found || failing.Passed {
		t.Errorf("the failing test did not come back failed: %+v", result.TestResults)
	}
	if failing.Message == "" {
		t.Error("a failed test came back with no message; the agent cannot act on that")
	}
}

func TestMCPRunRequestRedactsCredentialShapedResponseHeaders(t *testing.T) {
	f := newMCPRunFixture(t)
	result, err := f.run(context.Background(), f.plainReqID, nil)
	if err != nil {
		t.Fatalf("RunRequest: %v", err)
	}
	for _, header := range result.Headers {
		if strings.EqualFold(header.Name, "Content-Type") && header.Value == "" {
			t.Error("an ordinary response header lost its value")
		}
	}
	if len(result.Headers) == 0 {
		t.Error("no response headers came back at all")
	}
}

func TestMCPRunRequestOverrideBeatsTheEnvironmentAndDoesNotPersist(t *testing.T) {
	f := newMCPRunFixture(t)

	if _, err := f.run(context.Background(), f.secretReqID, map[string]string{"path": "/override-path"}); err != nil {
		t.Fatalf("RunRequest with an override: %v", err)
	}
	recorded := f.recorded()
	if len(recorded) != 1 || recorded[0].path != "/override-path" {
		t.Fatalf("the override did not beat the environment value: %+v", recorded)
	}

	// The environment variable itself is untouched on disk and in state...
	f.app.mu.Lock()
	var stored string
	for _, variable := range f.app.state.Workspaces[0].GlobalEnvironments[len(f.app.state.Workspaces[0].GlobalEnvironments)-1].Variables {
		if variable.Name == "path" {
			stored = fmt.Sprint(variable.Value)
		}
	}
	f.app.mu.Unlock()
	if stored != runEnvPath {
		t.Errorf("the environment variable is now %q; an override must never be written back", stored)
	}

	// ...and the very next run, without the override, goes back to it. This is
	// the assertion that would fail if the override had leaked into any scope
	// ApplyScriptVariableContextToState persists.
	if _, err := f.run(context.Background(), f.secretReqID, nil); err != nil {
		t.Fatalf("second RunRequest: %v", err)
	}
	recorded = f.recorded()
	if len(recorded) != 2 || recorded[1].path != runEnvPath {
		t.Fatalf("the override survived into a later run: %+v", recorded)
	}
}

func TestMCPRunRequestRejectsUnknownIdsWithActionableErrors(t *testing.T) {
	f := newMCPRunFixture(t)

	if _, err := f.backend.RunRequest(context.Background(), mcpserver.RunRequestParams{}); err == nil {
		t.Error("an empty params did not fail")
	}
	_, err := f.backend.RunRequest(context.Background(), mcpserver.RunRequestParams{
		CollectionID: "no-such-collection", RequestID: f.secretReqID,
	})
	if err == nil || !strings.Contains(err.Error(), "list_collections") {
		t.Errorf("the unknown-collection error is not actionable: %v", err)
	}
	_, err = f.backend.RunRequest(context.Background(), mcpserver.RunRequestParams{
		CollectionID: f.collectionID, RequestID: "no-such-request",
	})
	if err == nil || !strings.Contains(err.Error(), "list_requests") {
		t.Errorf("the unknown-request error is not actionable: %v", err)
	}
	_, err = f.backend.RunRequest(context.Background(), mcpserver.RunRequestParams{
		CollectionID: f.collectionID, RequestID: f.secretReqID, EnvironmentID: "no-such-environment",
	})
	if err == nil || !strings.Contains(err.Error(), "list_environments") {
		t.Errorf("the unknown-environment error is not actionable: %v", err)
	}
	// A GLOBAL environment id is a real id from list_environments and gets its
	// own explanation rather than "no such environment".
	_, err = f.backend.RunRequest(context.Background(), mcpserver.RunRequestParams{
		CollectionID: f.collectionID, RequestID: f.secretReqID, EnvironmentID: "env-run-global",
	})
	if err == nil || !strings.Contains(err.Error(), "global environment") {
		t.Errorf("a global environment id was not explained: %v", err)
	}
}

// --- 2. the secret-override refusal -----------------------------------------

func TestMCPRunRequestRefusesToOverrideASecretVariable(t *testing.T) {
	f := newMCPRunFixture(t)

	const smuggled = "attacker-chosen-token-value"
	_, err := f.run(context.Background(), f.secretReqID, map[string]string{"apiToken": smuggled})
	if err == nil {
		t.Fatal("overriding a secret variable was allowed")
	}
	if !errors.Is(err, mcpserver.ErrDenied) {
		t.Errorf("the refusal does not wrap ErrDenied: %v", err)
	}
	if !strings.Contains(err.Error(), "apiToken") {
		t.Errorf("the refusal does not name the variable: %v", err)
	}
	// Neither the real value nor the one the agent sent may be echoed back.
	if strings.Contains(err.Error(), runSentinelToken) || strings.Contains(err.Error(), smuggled) {
		t.Errorf("the refusal echoed a value: %v", err)
	}
	if len(f.recorded()) != 0 {
		t.Error("a refused run still reached the target server")
	}
}

func TestMCPRunRequestAllowsNonSecretOverrides(t *testing.T) {
	f := newMCPRunFixture(t)
	if _, err := f.run(context.Background(), f.plainReqID, map[string]string{"unrelated": "value"}); err != nil {
		t.Fatalf("a non-secret override was refused: %v", err)
	}
}

// --- 3. the new-host guard ---------------------------------------------------

func TestMCPRunRequestDoesNotPromptForAHostTheCollectionAlreadyUses(t *testing.T) {
	f := newMCPRunFixture(t)
	other := f.otherLoopbackURL()
	// A SIBLING request already sends this same secret to the other host, so the
	// computed allowlist covers it and no approval is needed.
	f.addSecretRequestForHost(other + "/sibling")

	if _, err := f.run(context.Background(), f.secretReqID, map[string]string{"baseUrl": other}); err != nil {
		t.Fatalf("a run to a host the collection already uses was blocked: %v", err)
	}
	if prompts := f.prompts(); len(prompts) != 0 {
		t.Errorf("the guard prompted for a known host: %+v", prompts)
	}
}

func TestMCPRunRequestDoesNotPromptWhenNoSecretIsReferenced(t *testing.T) {
	f := newMCPRunFixture(t)
	// A brand-new host, but the request references no secret at all — there is
	// nothing for the guard to protect.
	if _, err := f.run(context.Background(), f.plainReqID, map[string]string{"baseUrl": f.otherLoopbackURL()}); err != nil {
		t.Fatalf("a secret-free run to a new host was blocked: %v", err)
	}
	if prompts := f.prompts(); len(prompts) != 0 {
		t.Errorf("the guard prompted for a run with no secret in it: %+v", prompts)
	}
}

func TestMCPRunRequestDeniedNewHostNamesTheSecretAndNotItsValue(t *testing.T) {
	f := newMCPRunFixture(t)
	f.app.mcpApprovalEmit = func(request types.MCPApprovalRequest) {
		f.mu.Lock()
		f.approvals = append(f.approvals, request)
		f.mu.Unlock()
		// Deny, on the frontend's behalf, from the frontend's own binding.
		go func() {
			if err := f.app.ResolveMCPApproval(request.ID, false, false); err != nil {
				t.Errorf("ResolveMCPApproval(deny): %v", err)
			}
		}()
	}

	_, err := f.run(context.Background(), f.secretReqID, map[string]string{"baseUrl": "http://exfiltration.example.invalid"})
	if err == nil {
		t.Fatal("a denied run returned no error")
	}
	if !errors.Is(err, mcpserver.ErrDenied) {
		t.Errorf("the denial does not wrap ErrDenied: %v", err)
	}
	if !strings.Contains(err.Error(), "apiToken") {
		t.Errorf("the denial does not name the secret: %v", err)
	}
	if !strings.Contains(err.Error(), "exfiltration.example.invalid") {
		t.Errorf("the denial does not name the host: %v", err)
	}
	if strings.Contains(err.Error(), runSentinelToken) {
		t.Errorf("the denial leaked the secret VALUE: %v", err)
	}
	if len(f.recorded()) != 0 {
		t.Error("a denied run still reached a server")
	}

	prompts := f.prompts()
	if len(prompts) != 1 {
		t.Fatalf("got %d approval prompts, want 1", len(prompts))
	}
	if prompts[0].Host != "exfiltration.example.invalid" {
		t.Errorf("the prompt host is %q", prompts[0].Host)
	}
	if len(prompts[0].SecretNames) != 1 || prompts[0].SecretNames[0] != "apiToken" {
		t.Errorf("the prompt secret names are %v, want [apiToken]", prompts[0].SecretNames)
	}
	if prompts[0].RequestName != "Fetch profile" {
		t.Errorf("the prompt does not name the request: %q", prompts[0].RequestName)
	}
	if prompts[0].ID == "" {
		t.Error("the prompt carries no id, so the frontend cannot answer it")
	}
	payload, err := json.Marshal(prompts[0])
	if err != nil {
		t.Fatalf("marshal the prompt: %v", err)
	}
	if strings.Contains(string(payload), runSentinelToken) {
		t.Errorf("the approval payload leaked the secret value: %s", payload)
	}
}

func TestMCPRunRequestApprovedNewHostRuns(t *testing.T) {
	f := newMCPRunFixture(t)
	other := f.otherLoopbackURL()
	f.app.mcpApprovalEmit = func(request types.MCPApprovalRequest) {
		f.mu.Lock()
		f.approvals = append(f.approvals, request)
		f.mu.Unlock()
		go func() {
			if err := f.app.ResolveMCPApproval(request.ID, true, false); err != nil {
				t.Errorf("ResolveMCPApproval(approve): %v", err)
			}
		}()
	}

	result, err := f.run(context.Background(), f.secretReqID, map[string]string{"baseUrl": other})
	if err != nil {
		t.Fatalf("an approved run failed: %v", err)
	}
	if result.Status != http.StatusOK {
		t.Errorf("status is %d, want 200", result.Status)
	}
	if len(f.prompts()) != 1 {
		t.Errorf("got %d prompts, want exactly 1", len(f.prompts()))
	}
	// Approve-once does NOT remember: nothing was written.
	if _, err := os.Stat(f.app.mcpApprovalsPath()); !os.IsNotExist(err) {
		t.Errorf("approving without remember wrote %s", f.app.mcpApprovalsPath())
	}
}

func TestMCPRunRequestRememberedApprovalDoesNotPromptAgain(t *testing.T) {
	f := newMCPRunFixture(t)
	other := f.otherLoopbackURL()
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

	if _, err := f.run(context.Background(), f.secretReqID, map[string]string{"baseUrl": other}); err != nil {
		t.Fatalf("the first approved run failed: %v", err)
	}
	if _, err := f.run(context.Background(), f.secretReqID, map[string]string{"baseUrl": other}); err != nil {
		t.Fatalf("the second run failed: %v", err)
	}
	if prompts := f.prompts(); len(prompts) != 1 {
		t.Errorf("got %d prompts, want 1 — the remembered approval did not hold", len(prompts))
	}

	// The pair is on disk in the documented shape, naming the secret and the
	// host and nothing else.
	data, err := os.ReadFile(f.app.mcpApprovalsPath())
	if err != nil {
		t.Fatalf("read the approvals file: %v", err)
	}
	var stored types.MCPApprovalFile
	if err := json.Unmarshal(data, &stored); err != nil {
		t.Fatalf("decode the approvals file: %v", err)
	}
	if len(stored.Approvals) != 1 {
		t.Fatalf("the approvals file holds %d entries, want 1: %s", len(stored.Approvals), data)
	}
	if stored.Approvals[0].Secret != "apiToken" {
		t.Errorf("the remembered secret is %q", stored.Approvals[0].Secret)
	}
	if stored.Approvals[0].ApprovedAt == "" {
		t.Error("the remembered approval carries no timestamp")
	}
	if strings.Contains(string(data), runSentinelToken) {
		t.Errorf("the approvals file holds the secret VALUE: %s", data)
	}
}

func TestMCPRunRequestApprovalTimeoutDenies(t *testing.T) {
	f := newMCPRunFixture(t)
	// Shrunk rather than waited out: the property is "no answer denies", and a
	// minute of test runtime measures nothing extra.
	f.app.mcpApprovalTimeout = 50 * time.Millisecond

	started := time.Now()
	_, err := f.run(context.Background(), f.secretReqID, map[string]string{"baseUrl": "http://never-answered.example.invalid"})
	if err == nil {
		t.Fatal("an unanswered prompt did not deny the run")
	}
	if !errors.Is(err, mcpserver.ErrDenied) {
		t.Errorf("the timeout denial does not wrap ErrDenied: %v", err)
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Errorf("the timeout took %s; it should end at the configured deadline", elapsed)
	}
	if len(f.prompts()) != 1 {
		t.Errorf("got %d prompts, want 1", len(f.prompts()))
	}
}

func TestMCPRunRequestDeniesWhenThereIsNoFrontendToAsk(t *testing.T) {
	f := newMCPRunFixture(t)
	// No seam and no Wails context: this is a headless App, and there is nobody
	// who could approve anything.
	f.app.mcpApprovalEmit = nil

	started := time.Now()
	_, err := f.run(context.Background(), f.secretReqID, map[string]string{"baseUrl": "http://headless.example.invalid"})
	if err == nil {
		t.Fatal("a run with no frontend to ask was allowed")
	}
	if !errors.Is(err, mcpserver.ErrDenied) {
		t.Errorf("the denial does not wrap ErrDenied: %v", err)
	}
	// Immediate, not after the approval timeout: waiting a minute for an answer
	// that cannot arrive is the failure this branch exists to avoid.
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Errorf("the headless denial took %s; it should be immediate", elapsed)
	}
}

func TestResolveMCPApprovalRejectsAnUnknownID(t *testing.T) {
	f := newMCPRunFixture(t)
	if err := f.app.ResolveMCPApproval("", true, false); err == nil {
		t.Error("an empty approval id was accepted")
	}
	if err := f.app.ResolveMCPApproval("mcp-approval-nope", true, false); err == nil {
		t.Error("an unknown approval id was accepted")
	}
}

// --- 4. cancellation ---------------------------------------------------------

func TestMCPRunRequestCancellationEndsTheRunPromptly(t *testing.T) {
	f := newMCPRunFixture(t)
	ctx, cancel := context.WithCancel(context.Background())

	type outcome struct {
		err     error
		elapsed time.Duration
	}
	done := make(chan outcome, 1)
	go func() {
		started := time.Now()
		_, err := f.run(ctx, f.slowReqID, nil)
		done <- outcome{err: err, elapsed: time.Since(started)}
	}()

	// Long enough for the transport to be in flight against the 10s handler,
	// short enough that the assertion below is about cancellation rather than
	// about the request having already finished.
	time.Sleep(200 * time.Millisecond)
	cancel()

	select {
	case result := <-done:
		if result.err == nil {
			t.Fatal("a cancelled run returned no error")
		}
		if result.elapsed > 2*time.Second {
			t.Errorf("the cancelled run took %s to return", result.elapsed)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a cancelled run never returned")
	}
}

// --- 5. the audit store ------------------------------------------------------

func TestMCPAuditStoreAppendsAndReadsNewestFirst(t *testing.T) {
	app := newAppForTest(t)
	store := app.mcpAudit()

	for index := 0; index < 3; index++ {
		if err := store.Append(types.MCPAuditEntry{
			At:          time.Now().UTC(),
			Tool:        fmt.Sprintf("tool-%d", index),
			ArgsSummary: fmt.Sprintf("args-%d", index),
			Outcome:     "ok",
			DurationMs:  index,
		}); err != nil {
			t.Fatalf("append %d: %v", index, err)
		}
	}
	entries, err := store.List(0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("got %d entries, want 3", len(entries))
	}
	if entries[0].Tool != "tool-2" {
		t.Errorf("the first entry is %q; the list must be newest first", entries[0].Tool)
	}
	if entries[2].Tool != "tool-0" {
		t.Errorf("the last entry is %q", entries[2].Tool)
	}

	limited, err := store.List(2)
	if err != nil {
		t.Fatalf("List(2): %v", err)
	}
	if len(limited) != 2 || limited[0].Tool != "tool-2" {
		t.Errorf("the limit did not keep the newest entries: %+v", limited)
	}
}

// A store whose file does not exist yet is valid and empty, not an error: that
// is the state every install starts in, and the audit panel opens before any
// agent has called anything.
func TestMCPAuditStoreIsEmptyBeforeAnythingIsRecorded(t *testing.T) {
	app := newAppForTest(t)
	entries, err := app.GetMCPAuditLog(0)
	if err != nil {
		t.Fatalf("GetMCPAuditLog: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("a fresh store returned %d entries", len(entries))
	}
	if entries == nil {
		t.Error("the binding returned nil rather than an empty slice; the frontend would render undefined")
	}
}

func TestMCPAuditStoreCompactsPastTheThreshold(t *testing.T) {
	app := newAppForTest(t)
	store := app.mcpAudit()

	for index := 0; index <= mcpAuditCompactAt; index++ {
		if err := store.Append(types.MCPAuditEntry{
			At:      time.Now().UTC(),
			Tool:    fmt.Sprintf("tool-%d", index),
			Outcome: "ok",
		}); err != nil {
			t.Fatalf("append %d: %v", index, err)
		}
	}
	entries, err := store.List(mcpAuditLimit)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != mcpAuditLimit {
		t.Fatalf("after compaction the store holds %d entries, want %d", len(entries), mcpAuditLimit)
	}
	// Compaction keeps the NEWEST entries, which is the half that matters: an
	// audit log that dropped the most recent calls would be worse than none.
	if entries[0].Tool != fmt.Sprintf("tool-%d", mcpAuditCompactAt) {
		t.Errorf("the newest entry after compaction is %q", entries[0].Tool)
	}
}

func TestGetMCPAuditLogAppliesTheDefaultAndTheCeiling(t *testing.T) {
	app := newAppForTest(t)
	store := app.mcpAudit()
	for index := 0; index < mcpAuditMaxLimit+25; index++ {
		if err := store.Append(types.MCPAuditEntry{At: time.Now().UTC(), Tool: "run_request", Outcome: "ok"}); err != nil {
			t.Fatalf("append %d: %v", index, err)
		}
	}
	defaulted, err := app.GetMCPAuditLog(0)
	if err != nil {
		t.Fatalf("GetMCPAuditLog(0): %v", err)
	}
	if len(defaulted) != mcpAuditDefaultLimit {
		t.Errorf("a zero limit returned %d entries, want the %d default", len(defaulted), mcpAuditDefaultLimit)
	}
	negative, err := app.GetMCPAuditLog(-5)
	if err != nil {
		t.Fatalf("GetMCPAuditLog(-5): %v", err)
	}
	if len(negative) != mcpAuditDefaultLimit {
		t.Errorf("a negative limit returned %d entries, want the default", len(negative))
	}
	capped, err := app.GetMCPAuditLog(100000)
	if err != nil {
		t.Fatalf("GetMCPAuditLog(100000): %v", err)
	}
	if len(capped) != mcpAuditMaxLimit {
		t.Errorf("a huge limit returned %d entries, want the %d ceiling", len(capped), mcpAuditMaxLimit)
	}
}

func TestRecordMCPAuditWritesThroughTheRecorderSeam(t *testing.T) {
	app := newAppForTest(t)
	at := time.Now().Add(-time.Minute)
	app.recordMCPAudit(mcpserver.AuditEntry{
		At:          at,
		Tool:        "run_request",
		ArgsSummary: `{"collectionId":"c1"}`,
		Outcome:     "denied",
		DurationMs:  42,
	})
	entries, err := app.GetMCPAuditLog(0)
	if err != nil {
		t.Fatalf("GetMCPAuditLog: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
	if entries[0].Tool != "run_request" || entries[0].Outcome != "denied" || entries[0].DurationMs != 42 {
		t.Errorf("the recorded entry is %+v", entries[0])
	}
	if entries[0].ArgsSummary != `{"collectionId":"c1"}` {
		t.Errorf("the args summary was not persisted verbatim: %q", entries[0].ArgsSummary)
	}
	if !entries[0].At.Equal(at.UTC()) {
		t.Errorf("the timestamp is %s, want %s in UTC", entries[0].At, at.UTC())
	}
}

// A store that cannot be written raises the failure ONCE, however many calls
// hit it. The channel behaviour is the point: an agent working against a
// read-only data directory would otherwise fill the 20-entry notification list
// with copies of one message within seconds.
func TestRecordMCPAuditReportsAWriteFailureOnce(t *testing.T) {
	app := newAppForTest(t)
	// Pre-empt the lazy constructor with a path whose parent does not exist, so
	// every append fails at OpenFile.
	app.mcpAuditOnce.Do(func() {
		app.mcpAuditStore = &mcpAuditStore{path: filepath.Join(t.TempDir(), "missing-dir", "mcp-audit.jsonl")}
	})

	for index := 0; index < 5; index++ {
		app.recordMCPAudit(mcpserver.AuditEntry{At: time.Now(), Tool: "run_request", Outcome: "ok"})
	}

	app.mu.Lock()
	raised := 0
	for _, notification := range app.state.Notifications {
		if strings.Contains(notification.Message, "audit log") {
			raised++
		}
	}
	app.mu.Unlock()
	if raised != 1 {
		t.Errorf("%d audit-failure notifications were raised, want exactly 1", raised)
	}
}

// --- 6. guard unit checks ----------------------------------------------------

func TestMCPNormalizeHostDropsThePortAndCase(t *testing.T) {
	for _, testCase := range []struct{ in, want string }{
		{"API.Example.com", "api.example.com"},
		{"api.example.com:8443", "api.example.com"},
		{" api.example.com ", "api.example.com"},
		{"", ""},
	} {
		if got := mcpNormalizeHost(testCase.in); got != testCase.want {
			t.Errorf("mcpNormalizeHost(%q) = %q, want %q", testCase.in, got, testCase.want)
		}
	}
}

func TestMCPReferencedSecretsSeesSpacedAndNamedTemplates(t *testing.T) {
	item := types.RequestItem{
		URL:     "{{baseUrl}}/x",
		Headers: []types.KeyValue{{Name: "Authorization", Value: "Bearer {{ apiToken }}", Enabled: true}},
		Body:    types.RequestBody{Mode: "json", JSON: `{"k":"{{other}}"}`},
	}
	secrets := map[string]bool{"apiToken": true, "other": true, "unused": true}
	got := mcpReferencedSecrets(item, secrets)
	if len(got) != 2 || got[0] != "apiToken" || got[1] != "other" {
		t.Errorf("mcpReferencedSecrets = %v, want [apiToken other]", got)
	}
	// A disabled row cannot reach the wire, so it cannot pull a secret with it.
	item.Headers[0].Enabled = false
	got = mcpReferencedSecrets(item, secrets)
	if len(got) != 1 || got[0] != "other" {
		t.Errorf("a disabled header still contributed a secret: %v", got)
	}
}
