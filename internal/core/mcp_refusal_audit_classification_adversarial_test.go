package core

// Adversarial pass B, attack areas 1 (the four refusals) and 6 (the audit).
// The first two tests were one bug seen from two ends; all three pin CLOSED
// VULNERABILITIES and assert the fixed behaviour.
//
// THE BUG THE FIRST TWO FOUND. docs/mcp-agent-interface.md rule 6 promises "a
// refusal recorded as denied rather than as one more failure", and mcp_run.go's
// own comment on mcpClassifyRunFailure says the ErrDenied CLASS has to survive
// the trip through Response.Error so the audit can tell a refusal from an
// ordinary failure. But mcpRunResult turns ANY non-empty response.Error into a
// BRAND NEW, UNWRAPPED error:
//
//	fmt.Errorf("the request could not be completed: %s", masked)
//
// — %s, not %w, so whatever class the original error carried (including
// mcpserver.ErrDenied) is thrown away. The ONLY thing that can put the class
// back is mcpClassifyRunFailure's fallback: if policy.refusedAnyEgress() is
// true, the error is re-classed as denied. That flag used to be set in exactly
// one place, record(), which only runs for a DESTINATION denial that went
// through Authorize/AuthorizeNoPrompt.
//
// None of the §1.2(2) FEATURE refusals do that. Each is detected inside engine
// plumbing that has never heard of the destination policy: credential_process
// inside internal/auth/awsv4's profile walk, a PAC disposition inside
// internal/core's proxy-posture code before requestTransport returns, an
// interactive OAuth grant inside the OAuth2 token-fetch chain, a non-TCP gRPC
// target inside §4.7's validator. They build an ErrDenied-wrapped error, hand it
// to executeHTTP, which does `result.Error = err.Error()` — and the class is
// gone from that line onward. With nothing else in the run having tripped
// record(), mcpClassifyRunFailure had nothing to fall back on, and the agent and
// the audit log saw a plain "error" for what the design calls a refusal.
//
// WHAT NOW HOLDS. Every feature refusal marks the run's policy
// (mcpEgressPolicy.noteRefusal, called by Refuse and by mcpRefuseFeature), which
// is what mcpClassifyRunFailure reads. The mark is the recovery, not the error
// value: the error value cannot survive being stringified into Response.Error,
// which is the whole reason the flag exists. The awsv4 refusal, which cannot
// import mcpserver at all, is re-classed on the core side at the seam where its
// guard closure is installed (mcpClassifyAWSSigningRefusal).
//
// The first two tests drive a real run_request over the wire (the same
// e2eFixture every other end-to-end test in this package uses) so the assertion
// is about what an agent and the audit panel actually observe, not about an
// internal helper's return type. The third pins a different code path entirely —
// create_request's audit ArgsSummary — and has its own comment.

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mutexdev/lite_api/internal/mcpserver"
	"github.com/mutexdev/lite_api/internal/types"
)

// addPlainHTTPRequest plants one GET request in the fixture's own collection,
// pointed at the fixture's own live server — a destination inside Base(main)
// for THIS request under NO environment, so the only thing that can refuse the
// run is whatever the test configures beneath it, never the destination
// boundary itself.
func (f *e2eFixture) addPlainHTTPRequest(t *testing.T, name string, mutate func(*types.RequestItem)) string {
	t.Helper()
	f.app.mu.Lock()
	defer f.app.mu.Unlock()
	collection := &f.app.state.Workspaces[0].Collections[0]
	item := types.NewRequestItem(name, "http", len(collection.Items)+1)
	item.Method = http.MethodGet
	item.URL = f.baseURL // the MCP listener's own base is irrelevant; only used as a stand-in http(s) URL below where noted
	item.Body = types.RequestBody{Mode: "none"}
	if mutate != nil {
		mutate(&item)
	}
	collection.Items = append(collection.Items, item)
	return item.ID
}

// newestAuditEntryFor polls nothing — GetMCPAuditLog is synchronous with the
// call that produced it (mcp_audit.go: the recorder runs on the tool call's
// own goroutine before the HTTP response is written) — and returns the newest
// entry for the named tool.
func newestAuditEntryFor(t *testing.T, f *e2eFixture, tool string) types.MCPAuditEntry {
	t.Helper()
	entries, err := f.app.GetMCPAuditLog(0)
	if err != nil {
		t.Fatalf("GetMCPAuditLog: %v", err)
	}
	for _, entry := range entries {
		if entry.Tool == tool {
			return entry
		}
	}
	t.Fatalf("no audit entry recorded for tool %q (entries=%+v)", tool, entries)
	return types.MCPAuditEntry{}
}

// 1 of 3, a CLOSED VULNERABILITY: a PAC proxy disposition, refused under §2 row
// 4, was audited as "error" rather than "denied".
//
// The refusal fires inside a.requestTransport (app_execute_http.go), which
// returns BEFORE the main destination checkpoint ever runs — so this run's
// policy recorded no denial through Authorize/AuthorizeNoPrompt, and
// mcpClassifyRunFailure had nothing to promote. The request's own destination is
// otherwise entirely unremarkable and well within Base, which is the point:
// nothing else about this call is refusable, so whatever outcome the audit shows
// is entirely a verdict on the PAC refusal's own classification.
//
// WHAT NOW HOLDS: mcpPACProxyRefusal takes the context and goes through
// mcpRefuseFeature, so the refusal marks the policy on its way out and the run
// root re-classes it. All three PAC sites are covered: the resolution-mode check
// this test drives, the cert-bearing frozen discovery, and
// requestFailureMessage's restatement of the cert-free closure's refusal — the
// one site that fires from INSIDE the transport, which is why that function had
// to grow a context to mark anything at all.
func TestMCPRunRequestPACRefusalAuditedAsDenied(t *testing.T) {
	f := newE2EFixture(t)

	requestID := f.addPlainHTTPRequest(t, "PAC-refused run", func(item *types.RequestItem) {
		item.URL = "{{baseUrl}}/pac-target" // resolves under Base(main); reachability never matters, the refusal fires first
	})

	f.app.mu.Lock()
	f.app.state.Preferences.ProxyMode = "on"
	f.app.state.Preferences.Proxy.Source = "pac"
	// Deliberately a bogus, unreachable PAC source. If the refusal ever let a
	// fetch happen, this would surface as a DIFFERENT failure (a fetch error)
	// rather than the PAC-specific one — which would itself disprove the
	// "before any fetch" half of clause 2, so the test is a two-way trap.
	f.app.state.Preferences.Proxy.PAC.Source = "http://127.0.0.1:1/unreachable-pac.js"
	f.app.mu.Unlock()

	text, isError, _ := f.callTool(t, "run_request", map[string]any{
		"collectionId": f.collectionID,
		"requestId":    requestID,
	})
	if !isError {
		t.Fatalf("a PAC-proxied run_request succeeded outright: %s", text)
	}
	if !strings.Contains(text, "PAC") {
		t.Fatalf("the refusal text does not name PAC, so this call may have failed for an unrelated reason: %s", text)
	}

	entry := newestAuditEntryFor(t, f, "run_request")
	// THE ASSERTION THAT MATTERS. Per docs/mcp-agent-interface.md rule 6, "a
	// refusal [is] recorded as denied rather than as one more failure." A PAC
	// disposition is one of the four features §1.2(2) refuses outright, and
	// its refusal message says so explicitly (checked above) — so if the
	// audit does not also say "denied", the outcome column is lying about
	// what happened, and an operator or a script watching the audit panel for
	// "denied" rows to see what an agent tried to reach would miss this one
	// entirely, filed instead among ordinary failures.
	if entry.Outcome != "denied" {
		t.Errorf("a PAC refusal was audited as %q, not \"denied\" (ArgsSummary=%q) — "+
			"mcpRunResult (mcp_run.go) rebuilds response.Error as a fresh unwrapped error, so the "+
			"only thing that can restore the ErrDenied class is the mark the refusal leaves on this "+
			"run's policy (mcpEgressPolicy.noteRefusal, reached through mcpRefuseFeature). An "+
			"\"error\" here means a §2 row 4 refusal stopped marking it",
			entry.Outcome, entry.ArgsSummary)
	}
}

// 2 of 3, a CLOSED VULNERABILITY: an AWS credential_process refusal, under §2
// row 2, was audited as "error" rather than "denied" — the identical bug reached
// through a completely different refusal site.
//
// internal/auth/awsv4's profile walk returns *awsv4.CredentialProcessRefusedError
// with no mcpserver.ErrDenied wrapping at all, and it cannot add one: awsv4 does
// not import mcpserver, deliberately — the guard it takes is an interface
// precisely so the auth package stays ignorant of what "authorized" means.
//
// The AWS credential-endpoint checkpoint (mcpAWSEgressGuard,
// app_request_build.go) never runs for this profile: it authorizes the STS
// call, and this profile refuses BEFORE any STS call is attempted (the
// resolver refuses at the credential_process branch itself, prior to ever
// building a request). So exactly as with PAC, nothing in this run marked the
// policy before the refusal returned.
//
// WHAT NOW HOLDS: the refusal is re-classed on the CORE side, at the seam where
// core installs the guard closure in the first place —
// mcpClassifyAWSSigningRefusal wraps the awsv4.Sign call, recognises the typed
// error with errors.As, marks the policy and gives the error back its class
// without touching awsv4's own wording.
func TestMCPRunRequestAWSCredentialProcessRefusalAuditedAsDenied(t *testing.T) {
	f := newE2EFixture(t)

	// A minimal ~/.aws/config naming a profile that can only ever resolve
	// through credential_process, isolated to this test via env vars exactly
	// as internal/auth/awsv4's own tests do it.
	dir := t.TempDir()
	markerPath := filepath.Join(dir, "spawned.marker")
	scriptPath := filepath.Join(dir, "helper.sh")
	script := "#!/bin/sh\n" +
		"printf 'spawned' > " + markerPath + "\n" +
		`printf '{"Version":1,"AccessKeyId":"SHOULD-NEVER-RUN","SecretAccessKey":"SHOULD-NEVER-RUN"}'` + "\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o700); err != nil {
		t.Fatalf("write credential_process helper: %v", err)
	}
	configPath := filepath.Join(dir, "config")
	configBody := "[profile mcp-adversarial]\ncredential_process = " + scriptPath + "\n"
	if err := os.WriteFile(configPath, []byte(configBody), 0o600); err != nil {
		t.Fatalf("write AWS config: %v", err)
	}
	t.Setenv("AWS_CONFIG_FILE", configPath)
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(dir, "credentials-does-not-exist"))
	t.Setenv("AWS_ACCESS_KEY_ID", "")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "")
	t.Setenv("AWS_PROFILE", "")

	requestID := f.addPlainHTTPRequest(t, "credential_process-refused run", func(item *types.RequestItem) {
		item.URL = "{{baseUrl}}/aws-target"
		item.Auth = AuthConfig{Mode: "awsv4", AWSV4: AWSV4Auth{
			ProfileName: "mcp-adversarial",
			Service:     "execute-api",
			Region:      "eu-west-1",
		}}
	})

	text, isError, _ := f.callTool(t, "run_request", map[string]any{
		"collectionId": f.collectionID,
		"requestId":    requestID,
	})
	if !isError {
		t.Fatalf("a credential_process-backed run_request succeeded outright: %s", text)
	}
	if !strings.Contains(text, "credential_process") {
		t.Fatalf("the refusal text does not name credential_process, so this call may have failed for an unrelated reason: %s", text)
	}

	entry := newestAuditEntryFor(t, f, "run_request")
	if entry.Outcome != "denied" {
		t.Errorf("an AWS credential_process refusal was audited as %q, not \"denied\" (ArgsSummary=%q) — "+
			"*awsv4.CredentialProcessRefusedError never wraps mcpserver.ErrDenied and never reaches "+
			"policy.record(), so by the time executeHTTP has stringified it into response.Error the only "+
			"thing that can recognise it as a denial is the mark mcpClassifyAWSSigningRefusal leaves on "+
			"this run's policy. An \"error\" here means that re-class stopped firing",
			entry.Outcome, entry.ArgsSummary)
	}
	// Never spawned. §1.2(2)'s stronger guarantee — zero subprocess exec — is
	// pinned in depth elsewhere (internal/auth/awsv4/guard_test.go); this is
	// this test's OWN negative control, so a helper that somehow did run
	// could not silently make the assertions above pass by producing the
	// right-looking refusal text for the wrong reason (or worse, by actually
	// running and leaking the "credentials" it prints).
	if _, err := os.Stat(markerPath); !os.IsNotExist(err) {
		t.Fatal("the credential_process helper ran and left its marker file — the refusal did not fire before the spawn")
	}
}

// 3 of 3, a CLOSED VULNERABILITY: create_request's audit ArgsSummary carried a
// credential-shaped literal an agent authored, in plain text, unmasked.
//
// THE HOLE. Rule 1's principle — "credential-shaped literals... are masked
// wherever a request definition can carry one: header and param rows, auth block
// fields... so a pasted ?api_key=sk_live_... arrives as ?api_key=<masked>" — was
// enforced for every READ tool (get_request, list_requests, inspect_request,
// mcp_backend.go's mcpRequestSummary/mcpKeyValueRows/RedactRows) and never
// reached summarizeArgs (internal/mcpserver/protocol.go), which rendered a
// tool's raw arguments verbatim, bounded only by LENGTH (per
// TestSummarizeArgsBoundsWhatOneEntryCanCost), into the same audit log rule 6
// advertises as "visible in the app's audit panel." An agent that authored a
// request the ordinary way — pasting a working curl's header into
// create_request the way a user would type it into the app — wrote that literal
// credential to <dataDir>/mcp-audit.jsonl in the clear.
//
// It was reported with lower confidence than the other two, because §1.3 lists
// what the output boundary covers ("tool results, response bodies and headers,
// test results, history, error text") and does not name the audit ArgsSummary
// explicitly. It was fixed anyway: rule 1's own example IS this shape of
// literal, and there is no reading of "masked wherever a request definition can
// carry one" under which the app's own audit panel is the exception.
//
// WHAT NOW HOLDS: summarizeArgs redacts before it bounds, through
// redactArgumentValue (mcpserver/redact.go), which is the READ TIER'S OWN
// masking called rather than reimplemented — row arrays get RedactRows' rule,
// the auth object gets MaskAuthRows' inverted rule, strings get
// RedactURLQueryLiterals, and bodies stay unscanned exactly as rule 3 says of
// every other surface. Both halves are asserted below: the audit no longer
// carries the literal, and get_request still masks it.
func TestMCPCreateRequestAuditArgsSummaryMasksAnAuthoredCredentialLiteral(t *testing.T) {
	f := newE2EFixture(t)
	f.enableE2EWriteTier(t)

	const pastedAPIKey = "sk_live_MCP-ADVERSARIAL-PASTED-CREDENTIAL-0007"
	text, isError, _ := f.callTool(t, "create_request", map[string]any{
		"collectionId": f.collectionID,
		"name":         "Pasted-credential request",
		"url":          "{{baseUrl}}/whoami",
		"headers": []map[string]any{
			{"name": "X-Api-Key", "value": pastedAPIKey},
		},
	})
	if isError {
		t.Fatalf("create_request with a credential-shaped header literal was refused outright: %s", text)
	}

	entry := newestAuditEntryFor(t, f, "create_request")
	if strings.Contains(entry.ArgsSummary, pastedAPIKey) {
		t.Errorf("create_request's audit ArgsSummary carries the authored credential literal verbatim: %q — "+
			"summarizeArgs (internal/mcpserver/protocol.go) must redact through redactArgumentValue before it "+
			"bounds, or an agent that authors a request the way a user pastes one from a working curl writes "+
			"that credential into mcp-audit.jsonl and the audit panel in the clear, in a form "+
			"get_request/list_requests mask",
			entry.ArgsSummary)
	}
	// The row was summarised at all, and MASKED rather than dropped: a summary
	// that silently omitted the header would also pass the check above while
	// telling the operator less than it should.
	if !strings.Contains(entry.ArgsSummary, "X-Api-Key") || !strings.Contains(entry.ArgsSummary, mcpserver.MaskedValue) {
		t.Errorf("the audit summary should still show the header, with its value masked: %q", entry.ArgsSummary)
	}
	// And the arguments that are not credentials survive untouched — the point
	// of an audit row is that it says what the agent was pointed at.
	if !strings.Contains(entry.ArgsSummary, f.collectionID) {
		t.Errorf("redaction ate an ordinary argument: %q", entry.ArgsSummary)
	}

	// AND THE READ TIER'S OWN PROMISE, for contrast: the request this call
	// just created reports the same literal MASKED when read back — proving
	// the gap above is specifically the audit trail, not a general failure to
	// mask this literal anywhere.
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(text), &created); err != nil || created.ID == "" {
		t.Fatalf("could not read the created request's id back out of %q: %v", text, err)
	}
	readText, readIsError, _ := f.callTool(t, "get_request", map[string]any{
		"collectionId": f.collectionID,
		"requestId":    created.ID,
	})
	if readIsError {
		t.Fatalf("get_request on the request just created failed: %s", readText)
	}
	if strings.Contains(readText, pastedAPIKey) {
		t.Fatalf("get_request itself leaked the pasted credential, which would make this test's contrast meaningless: %s", readText)
	}
}
