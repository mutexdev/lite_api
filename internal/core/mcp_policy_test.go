package core

// The scoped policy: scope isolation, the authorize state machine, the
// mutex-not-held-during-prompt property, and the guard transport.
//
// These tests are the enforcement contract in miniature. Nothing in the engine
// calls the policy yet, so every property the later waves depend on has to be
// pinned here or it will be discovered by the wave that trips over it.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mutexdev/lite_api/internal/mcpserver"
	"github.com/mutexdev/lite_api/internal/types"
)

// --- fixtures --------------------------------------------------------------

func testSite(requestID string) mcpDefinitionSite {
	return mcpDefinitionSite{
		workspacePath:        "/workspaces/payments",
		collectionID:         "col_payments",
		requestID:            requestID,
		environmentID:        "env_production",
		globalEnvironmentIDs: []string{"global_team"},
	}
}

// testScope builds a scope whose main set holds exactly the given URLs.
func testScope(t *testing.T, requestID string, mainURLs ...string) mcpScopeOrigins {
	t.Helper()
	scope := mcpScopeOrigins{site: testSite(requestID)}
	for _, rawURL := range mainURLs {
		origin, ok := OriginOfURL(rawURL)
		if !ok {
			t.Fatalf("fixture URL %q did not resolve to an origin", rawURL)
		}
		scope.add(egressKindMain, origin)
		scope.add(egressKindRedirect, origin)
		scope.add(egressKindScript, origin)
	}
	if len(mainURLs) > 0 {
		scope.mainURL = mainURLs[0]
	}
	return scope
}

func denied(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("the egress was authorized; it must be denied")
	}
	if !errors.Is(err, mcpserver.ErrDenied) {
		t.Fatalf("denial did not wrap mcpserver.ErrDenied: %v", err)
	}
}

// --- scope isolation -------------------------------------------------------

// THE CONFUSED-DEPUTY CASE. A flow's step B points somewhere step A does not.
// While step A is the active scope, B's origin must be denied — otherwise a flow
// grants every step the union of every other step's destinations, and the
// narrowest thing an agent can do is aim step A's credential at step B's host.
func TestMCPPolicyFlowStepScopeIsolation(t *testing.T) {
	policy := newMCPEgressPolicy()
	stepA := testScope(t, "req_charge", "https://payments.example.com/charge")
	stepB := testScope(t, "req_notify", "https://hooks.example.com/notify")

	policy.SetScope(stepA)
	aOrigin := mustOrigin(t, "https://payments.example.com")
	bOrigin := mustOrigin(t, "https://hooks.example.com")

	if err := policy.Authorize(context.Background(), aOrigin, egressKindMain); err != nil {
		t.Fatalf("step A's own destination was denied under step A: %v", err)
	}
	denied(t, policy.Authorize(context.Background(), bOrigin, egressKindMain))

	// SetScope REPLACES rather than accumulates: after moving to step B, step
	// A's origin is gone.
	policy.SetScope(stepB)
	if err := policy.Authorize(context.Background(), bOrigin, egressKindMain); err != nil {
		t.Fatalf("step B's own destination was denied under step B: %v", err)
	}
	denied(t, policy.Authorize(context.Background(), aOrigin, egressKindMain))
	if depth := policy.scopeDepth(); depth != 1 {
		t.Fatalf("SetScope grew the stack to depth %d; flow steps are siblings, not ancestors", depth)
	}
}

// A main request must not be able to use its own OAuth token endpoint's origin,
// and vice versa. Base is per KIND, and the classes are separate too, so neither
// direction can borrow the other's authority.
func TestMCPPolicyMainCannotUseTokenOrigin(t *testing.T) {
	policy := newMCPEgressPolicy()
	scope := testScope(t, "req_charge", "https://payments.example.com/charge")
	tokenOrigin := mustOrigin(t, "https://login.example.com")
	scope.add(egressKindToken, tokenOrigin)
	policy.SetScope(scope)

	if err := policy.Authorize(context.Background(), tokenOrigin, egressKindToken); err != nil {
		t.Fatalf("the token endpoint was denied for a token egress: %v", err)
	}
	denied(t, policy.Authorize(context.Background(), tokenOrigin, egressKindMain))
	denied(t, policy.Authorize(context.Background(), mustOrigin(t, "https://payments.example.com"), egressKindToken))
}

// A nested bru.runRequest's origins must vanish on return. Push/Pop is LIFO and
// the parent scope must come back byte-for-byte.
func TestMCPPolicyNestedScopeIsPoppedAndTheParentRestored(t *testing.T) {
	policy := newMCPEgressPolicy()
	parent := testScope(t, "req_charge", "https://payments.example.com/charge")
	nested := testScope(t, "req_lookup", "https://lookup.example.com/customers")
	policy.SetScope(parent)

	parentOrigin := mustOrigin(t, "https://payments.example.com")
	nestedOrigin := mustOrigin(t, "https://lookup.example.com")
	denied(t, policy.Authorize(context.Background(), nestedOrigin, egressKindMain))

	func() {
		policy.PushScope(nested)
		defer policy.PopScope()
		if err := policy.Authorize(context.Background(), nestedOrigin, egressKindMain); err != nil {
			t.Fatalf("the nested target's own destination was denied inside its scope: %v", err)
		}
		// The PARENT's origin is not visible from the nested scope either:
		// nothing unions across the stack, in either direction.
		denied(t, policy.Authorize(context.Background(), parentOrigin, egressKindMain))
	}()

	if err := policy.Authorize(context.Background(), parentOrigin, egressKindMain); err != nil {
		t.Fatalf("the parent scope was not restored after the pop: %v", err)
	}
	denied(t, policy.Authorize(context.Background(), nestedOrigin, egressKindMain))
}

// The Push/defer-Pop pairing has to survive a PANIC, because a nested script
// send can panic and the whole point of the defer is that the nested authority
// does not outlive the call however it ends.
func TestMCPPolicyPopScopeRunsWhileAPanicUnwinds(t *testing.T) {
	policy := newMCPEgressPolicy()
	policy.SetScope(testScope(t, "req_charge", "https://payments.example.com/charge"))
	nestedOrigin := mustOrigin(t, "https://lookup.example.com")

	func() {
		defer func() {
			if recovered := recover(); recovered == nil {
				t.Error("the fixture did not panic, so this proved nothing")
			}
		}()
		policy.PushScope(testScope(t, "req_lookup", "https://lookup.example.com/customers"))
		defer policy.PopScope()
		panic("a nested script blew up")
	}()

	if depth := policy.scopeDepth(); depth != 1 {
		t.Fatalf("stack depth after the panic is %d, want 1", depth)
	}
	denied(t, policy.Authorize(context.Background(), nestedOrigin, egressKindMain))
}

// An empty stack denies rather than panicking. Pop runs from a defer, possibly
// while a panic is already unwinding, so panicking there would replace the real
// failure with this one — and denying is the right fail-closed reading anyway.
func TestMCPPolicyWithNoScopeDeniesEverything(t *testing.T) {
	policy := newMCPEgressPolicy()
	policy.PopScope() // must not panic on an empty stack
	denied(t, policy.Authorize(context.Background(), mustOrigin(t, "https://payments.example.com"), egressKindMain))
	denied(t, policy.AuthorizeNoPrompt(mustOrigin(t, "https://payments.example.com"), egressKindMain))
}

// --- the authorize state machine -------------------------------------------

func TestMCPPolicyAuthorizeStateMachine(t *testing.T) {
	origin := mustOrigin(t, "https://attacker.example:8443")

	t.Run("base allows without consulting anything", func(t *testing.T) {
		policy := newMCPEgressPolicy()
		policy.approved = func(mcpDefinitionSite, Origin, string) (bool, error) {
			t.Fatal("the persisted store was consulted for an origin already in Base")
			return false, nil
		}
		policy.prompt = func(context.Context, types.MCPApprovalRequest) mcpPromptOutcome {
			t.Fatal("the user was prompted for an origin already in Base")
			return mcpPromptDeny
		}
		policy.SetScope(testScope(t, "req_charge", "https://attacker.example:8443/x"))
		if err := policy.Authorize(context.Background(), origin, egressKindMain); err != nil {
			t.Fatalf("Base did not allow: %v", err)
		}
	})

	t.Run("a persisted approval allows without prompting", func(t *testing.T) {
		policy := newMCPEgressPolicy()
		var askedClass string
		var askedSite mcpDefinitionSite
		policy.approved = func(site mcpDefinitionSite, o Origin, class string) (bool, error) {
			askedSite, askedClass = site, class
			return o == origin, nil
		}
		policy.prompt = func(context.Context, types.MCPApprovalRequest) mcpPromptOutcome {
			t.Fatal("the user was prompted for an origin the store had already approved")
			return mcpPromptDeny
		}
		policy.SetScope(testScope(t, "req_charge", "https://payments.example.com/charge"))
		if err := policy.Authorize(context.Background(), origin, egressKindMain); err != nil {
			t.Fatalf("a persisted approval did not allow: %v", err)
		}
		// The store is asked about the FULL site and the CLASS, never the kind:
		// that is what makes an approval for one request under one environment
		// unusable anywhere else.
		if askedClass != kindClassRequest {
			t.Errorf("the store was asked about class %q, want %q", askedClass, kindClassRequest)
		}
		if askedSite.requestID != "req_charge" || askedSite.environmentID != "env_production" {
			t.Errorf("the store was asked about site %+v, want the full run site", askedSite)
		}
	})

	t.Run("an allow-once answer grants for the rest of the execution", func(t *testing.T) {
		policy := newMCPEgressPolicy()
		prompts := 0
		policy.prompt = func(context.Context, types.MCPApprovalRequest) mcpPromptOutcome {
			prompts++
			return mcpPromptAllowOnce
		}
		policy.SetScope(testScope(t, "req_charge", "https://payments.example.com/charge"))
		for attempt := 0; attempt < 3; attempt++ {
			if err := policy.Authorize(context.Background(), origin, egressKindMain); err != nil {
				t.Fatalf("attempt %d was denied after an allow-once: %v", attempt, err)
			}
		}
		if prompts != 1 {
			t.Fatalf("the user was prompted %d times; an allow-once grant must serve the rest of the execution", prompts)
		}
		// And the session grant is keyed by CLASS, so it does not leak into the
		// token class.
		policy.prompt = func(context.Context, types.MCPApprovalRequest) mcpPromptOutcome { return mcpPromptDeny }
		denied(t, policy.Authorize(context.Background(), origin, egressKindToken))
	})

	t.Run("a session grant does not cross scopes", func(t *testing.T) {
		policy := newMCPEgressPolicy()
		policy.prompt = func(context.Context, types.MCPApprovalRequest) mcpPromptOutcome { return mcpPromptAllowOnce }
		policy.SetScope(testScope(t, "req_charge", "https://payments.example.com/charge"))
		if err := policy.Authorize(context.Background(), origin, egressKindMain); err != nil {
			t.Fatalf("the prompted allow did not take: %v", err)
		}
		// A different request in the same collection: the site differs, so the
		// session key differs, so the grant does not apply.
		policy.prompt = func(context.Context, types.MCPApprovalRequest) mcpPromptOutcome { return mcpPromptDeny }
		policy.SetScope(testScope(t, "req_notify", "https://hooks.example.com/notify"))
		denied(t, policy.Authorize(context.Background(), origin, egressKindMain))
	})

	t.Run("a denied prompt denies with an actionable error", func(t *testing.T) {
		policy := newMCPEgressPolicy()
		policy.prompt = func(context.Context, types.MCPApprovalRequest) mcpPromptOutcome { return mcpPromptDeny }
		policy.SetScope(testScope(t, "req_charge", "https://payments.example.com/charge"))
		err := policy.Authorize(context.Background(), origin, egressKindMain)
		denied(t, err)
		// The message must name the origin, the scope and the kind, or the agent
		// reading it learns nothing beyond "no".
		for _, needle := range []string{"https://attacker.example:8443", "req_charge", "col_payments", "env_production", string(egressKindMain)} {
			if !strings.Contains(err.Error(), needle) {
				t.Errorf("the denial does not mention %q: %v", needle, err)
			}
		}
	})

	t.Run("headless denies when there is no prompt callback", func(t *testing.T) {
		policy := newMCPEgressPolicy()
		policy.SetScope(testScope(t, "req_charge", "https://payments.example.com/charge"))
		denied(t, policy.Authorize(context.Background(), origin, egressKindMain))
	})

	t.Run("an approval store that errors denies", func(t *testing.T) {
		policy := newMCPEgressPolicy()
		policy.approved = func(mcpDefinitionSite, Origin, string) (bool, error) {
			return false, errors.New("the approvals file is unreadable")
		}
		policy.prompt = func(context.Context, types.MCPApprovalRequest) mcpPromptOutcome {
			t.Fatal("the user was prompted after the store failed; a store that cannot be read has not said yes")
			return mcpPromptAllowRemember
		}
		policy.SetScope(testScope(t, "req_charge", "https://payments.example.com/charge"))
		denied(t, policy.Authorize(context.Background(), origin, egressKindMain))
	})

	t.Run("an unresolved origin is denied, never waved through", func(t *testing.T) {
		policy := newMCPEgressPolicy()
		policy.prompt = func(context.Context, types.MCPApprovalRequest) mcpPromptOutcome { return mcpPromptAllowRemember }
		policy.SetScope(testScope(t, "req_charge", "https://payments.example.com/charge"))
		denied(t, policy.Authorize(context.Background(), Origin{}, egressKindMain))
	})

	t.Run("an unknown egress kind is refused rather than defaulted", func(t *testing.T) {
		policy := newMCPEgressPolicy()
		policy.approved = func(mcpDefinitionSite, Origin, string) (bool, error) { return true, nil }
		policy.SetScope(testScope(t, "req_charge", "https://payments.example.com/charge"))
		denied(t, policy.Authorize(context.Background(), origin, egressKind("invented-later")))
	})
}

// The backstop cannot prompt — it runs inside client.Timeout — so it denies and
// raises a NON-BLOCKING notification instead, and a remembered approval makes
// the retry succeed (§2 row 10).
func TestMCPPolicyAuthorizeNoPromptDeniesAndNotifies(t *testing.T) {
	policy := newMCPEgressPolicy()
	policy.prompt = func(context.Context, types.MCPApprovalRequest) mcpPromptOutcome {
		t.Fatal("the backstop blocked on a prompt; it runs inside client.Timeout and must never do that")
		return mcpPromptDeny
	}
	notified := 0
	remembered := false
	policy.notify = func(types.MCPApprovalRequest) { notified++ }
	policy.approved = func(mcpDefinitionSite, Origin, string) (bool, error) { return remembered, nil }
	policy.SetScope(testScope(t, "req_charge", "https://payments.example.com/charge"))

	hop := mustOrigin(t, "https://cdn.elsewhere.example")
	denied(t, policy.AuthorizeNoPrompt(hop, egressKindRedirect))
	if notified != 1 {
		t.Fatalf("notify was called %d times, want 1", notified)
	}

	// The user answered "allow and remember" out of band; the agent's retry now
	// finds the approval in the store.
	remembered = true
	if err := policy.AuthorizeNoPrompt(hop, egressKindRedirect); err != nil {
		t.Fatalf("the retry after a remembered approval was still denied: %v", err)
	}
}

// A redirect hop back to the request's own origin is already in Base: §1.1 makes
// Base(S, redirect) the scope's main set, materialized at construction.
func TestMCPPolicyRedirectToTheRequestsOwnOriginIsInBase(t *testing.T) {
	policy := newMCPEgressPolicy()
	policy.SetScope(testScope(t, "req_charge", "https://payments.example.com/charge"))
	if err := policy.AuthorizeNoPrompt(mustOrigin(t, "https://payments.example.com/other"), egressKindRedirect); err != nil {
		t.Fatalf("a same-origin redirect was denied: %v", err)
	}
}

// --- the locking property (§4.2) -------------------------------------------

// THE MUTEX MUST NOT BE HELD ACROSS THE PROMPT. An approval prompt waits up to
// 60 seconds on a human. If p.mu were held for that, every concurrent egress
// check, every PopScope on another goroutine and the audit hook would block
// behind it — which in a flow means the run cannot even be cancelled.
//
// The proof: block inside the prompt callback and, from that same callback,
// require PopScope, an Authorize on another goroutine and the audit hook to all
// complete. If the lock were held, this test deadlocks and the harness times
// out. Run under -race.
func TestMCPPolicyPromptDoesNotHoldTheMutex(t *testing.T) {
	policy := newMCPEgressPolicy()
	policy.audit = func(mcpDefinitionSite, Origin, egressKind, string) {}
	policy.SetScope(testScope(t, "req_charge", "https://payments.example.com/charge"))
	policy.PushScope(testScope(t, "req_lookup", "https://lookup.example.com/customers"))

	// Resolved on the test goroutine: mustOrigin calls t.Fatalf, which is not
	// legal from the goroutines the prompt callback spawns.
	parentOrigin := mustOrigin(t, "https://payments.example.com")
	strangerOrigin := mustOrigin(t, "https://attacker.example")

	release := make(chan struct{})
	progressed := make(chan struct{})
	policy.prompt = func(context.Context, types.MCPApprovalRequest) mcpPromptOutcome {
		// Everything below runs WHILE the prompt is outstanding.
		var wait sync.WaitGroup
		wait.Add(3)
		go func() { defer wait.Done(); policy.PopScope() }()
		go func() {
			defer wait.Done()
			// A concurrent check on another goroutine: it may allow or deny
			// depending on which scope won the race, and either is fine — what
			// matters is that it RETURNS.
			_ = policy.AuthorizeNoPrompt(parentOrigin, egressKindMain)
		}()
		go func() { defer wait.Done(); _ = policy.scopeDepth() }()
		wait.Wait()
		close(progressed)
		<-release
		return mcpPromptDeny
	}

	done := make(chan error, 1)
	go func() {
		done <- policy.Authorize(context.Background(), strangerOrigin, egressKindMain)
	}()

	select {
	case <-progressed:
	case <-time.After(10 * time.Second):
		t.Fatal("a concurrent PopScope/Authorize/audit could not run while an approval prompt was outstanding: the policy mutex is held across the prompt")
	}
	close(release)
	select {
	case err := <-done:
		denied(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("Authorize never returned after the prompt answered")
	}
}

// Concurrent authorizations must not corrupt the session map or the stack. This
// is the -race companion to the test above.
func TestMCPPolicyConcurrentAuthorizeIsRaceFree(t *testing.T) {
	policy := newMCPEgressPolicy()
	policy.prompt = func(context.Context, types.MCPApprovalRequest) mcpPromptOutcome { return mcpPromptAllowOnce }
	policy.audit = func(mcpDefinitionSite, Origin, egressKind, string) {}
	policy.SetScope(testScope(t, "req_charge", "https://payments.example.com/charge"))

	var wait sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		wait.Add(1)
		go func(worker int) {
			defer wait.Done()
			origin, ok := OriginOfURL(fmt.Sprintf("https://host-%d.example", worker%3))
			if !ok {
				return
			}
			for round := 0; round < 20; round++ {
				_ = policy.Authorize(context.Background(), origin, egressKindMain)
				_ = policy.scopeDepth()
			}
		}(worker)
	}
	wait.Wait()
}

// --- refusals (§2) ----------------------------------------------------------

// Every §2 refusal reads the same way — feature, why it is unavailable to
// agent-initiated runs, the action — and every one of them is ErrDenied-class so
// the MCP server records it as "denied" rather than "error".
func TestRefuseProducesTheUniformRefusal(t *testing.T) {
	var policy *mcpEgressPolicy // a nil receiver is tolerated: refusal sites may not hold one
	err := policy.Refuse(
		`AWS profile "prod" uses credential_process, which runs an external program`,
		"Run this request in the LiteAPI app, or switch the profile to static keys or SSO.",
	)
	denied(t, err)
	want := `denied: AWS profile "prod" uses credential_process, which runs an external program. ` +
		`Agent-initiated runs cannot use it. Run this request in the LiteAPI app, or switch the profile to static keys or SSO.`
	if err.Error() != want {
		t.Errorf("refusal text is\n  %q\nwant\n  %q", err.Error(), want)
	}
	// A feature already ending in a period does not produce a doubled one.
	if got := mcpRefusal("PAC proxies are not evaluated.", "Switch the proxy setting to manual or system."); strings.Contains(got.Error(), "..") {
		t.Errorf("refusal doubled the sentence break: %v", got)
	}
}

// --- provenance and the context (§4.5) --------------------------------------

// The zero provenance is INVALID, and that is the whole design: a path that
// forgets to thread provenance must not silently behave as a UI send.
func TestSendProvenanceZeroValueIsInvalid(t *testing.T) {
	var zero sendProvenance
	if zero.valid() {
		t.Fatal("the zero sendProvenance reported itself valid; an unlabeled path would then pass as a UI send")
	}
	if !uiSendProvenance().valid() {
		t.Error("uiSendProvenance() produced an invalid provenance")
	}
	policy := newMCPEgressPolicy()
	prov := mcpSendProvenance(policy)
	if !prov.valid() || prov.policy != policy {
		t.Error("mcpSendProvenance() did not carry the policy")
	}
	// An MCP provenance with no policy is a bug, not a permissive UI send.
	if mcpSendProvenance(nil).valid() {
		t.Error("mcpSendProvenance(nil) produced a valid provenance")
	}
}

func TestPolicyContextRoundTrip(t *testing.T) {
	policy := newMCPEgressPolicy()
	ctx := mcpContextWithPolicy(context.Background(), policy)
	if got := mcpPolicyFromContext(ctx); got != policy {
		t.Fatalf("mcpPolicyFromContext returned %p, want %p", got, policy)
	}
	// A UI context is LABELED but carries no policy, and the two are
	// distinguishable from an unlabeled one.
	uiCtx := mcpContextWithUIProvenance(context.Background())
	if mcpPolicyFromContext(uiCtx) != nil {
		t.Error("a UI context produced a policy")
	}
	if _, labeled := mcpProvenanceFromContext(uiCtx); !labeled {
		t.Error("a UI context reported itself unlabeled")
	}
	if _, labeled := mcpProvenanceFromContext(context.Background()); labeled {
		t.Error("a bare context reported itself labeled")
	}
	if mcpPolicyFromContext(context.Background()) != nil {
		t.Error("a bare context produced a policy")
	}
	// A nil context is a real defensive branch: an engine path that never got
	// one must not crash the checkpoint.
	var absent context.Context
	if mcpPolicyFromContext(absent) != nil {
		t.Error("a nil context produced a policy")
	}
	if mcpBackstopEgressKind(absent) != egressKindMain {
		t.Error("a nil context did not fall back to the main backstop kind")
	}
	if mcpContextWithPolicy(absent, policy) == nil || mcpContextWithUIProvenance(absent) == nil {
		t.Error("labeling a nil context produced no context")
	}
	if mcpContextWithEgressKind(absent, egressKindAWS) == nil {
		t.Error("narrowing the kind on a nil context produced no context")
	}

	// The backstop kind defaults to main and is narrowed explicitly by the
	// paths whose class genuinely differs.
	if got := mcpBackstopEgressKind(ctx); got != egressKindMain {
		t.Errorf("the default backstop kind is %q, want %q", got, egressKindMain)
	}
	if got := mcpBackstopEgressKind(mcpContextWithEgressKind(ctx, egressKindToken)); got != egressKindToken {
		t.Errorf("the narrowed backstop kind is %q, want %q", got, egressKindToken)
	}
}

// --- the guard transport (§4.3 item 3) --------------------------------------

type countingRoundTripper struct {
	calls int
	next  http.RoundTripper
}

func (c *countingRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	c.calls++
	return c.next.RoundTrip(req)
}

func TestMCPEgressGuardTransport(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	serverOrigin, ok := OriginOfURL(server.URL)
	if !ok {
		t.Fatalf("the test server URL %q did not resolve to an origin", server.URL)
	}

	newClient := func() (*http.Client, *countingRoundTripper) {
		counter := &countingRoundTripper{next: http.DefaultTransport}
		return &http.Client{Transport: newMCPEgressGuardTransport(counter)}, counter
	}

	get := func(t *testing.T, client *http.Client, ctx context.Context) error {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/thing", nil)
		if err != nil {
			t.Fatalf("building the request: %v", err)
		}
		res, err := client.Do(req)
		if res != nil {
			_ = res.Body.Close()
		}
		return err
	}

	t.Run("a policy context is authorized without prompting", func(t *testing.T) {
		policy := newMCPEgressPolicy()
		policy.prompt = func(context.Context, types.MCPApprovalRequest) mcpPromptOutcome {
			t.Fatal("the backstop prompted; it runs inside client.Timeout and must not")
			return mcpPromptDeny
		}
		scope := testScope(t, "req_charge")
		scope.add(egressKindMain, serverOrigin)
		scope.add(egressKindRedirect, serverOrigin)
		policy.SetScope(scope)

		client, counter := newClient()
		if err := get(t, client, mcpContextWithPolicy(context.Background(), policy)); err != nil {
			t.Fatalf("an in-Base destination was refused by the guard: %v", err)
		}
		if counter.calls != 1 {
			t.Fatalf("the wrapped transport ran %d times, want 1", counter.calls)
		}
	})

	t.Run("a policy context refuses an out-of-scope destination before any bytes move", func(t *testing.T) {
		policy := newMCPEgressPolicy()
		policy.SetScope(testScope(t, "req_charge", "https://payments.example.com/charge"))
		client, counter := newClient()
		err := get(t, client, mcpContextWithPolicy(context.Background(), policy))
		denied(t, err)
		if counter.calls != 0 {
			t.Fatalf("the wrapped transport ran %d times for a refused egress; zero bytes must move", counter.calls)
		}
	})

	t.Run("a UI context passes under both flag values", func(t *testing.T) {
		for _, strict := range []bool{false, true} {
			func() {
				defer restoreStrictEgressProvenance(strict)()
				client, counter := newClient()
				if err := get(t, client, mcpContextWithUIProvenance(context.Background())); err != nil {
					t.Fatalf("strict=%v: a UI send was refused: %v", strict, err)
				}
				if counter.calls != 1 {
					t.Fatalf("strict=%v: the wrapped transport ran %d times, want 1", strict, counter.calls)
				}
			}()
		}
	})

	t.Run("an unlabeled context passes while strict is off and refuses once it flips", func(t *testing.T) {
		func() {
			defer restoreStrictEgressProvenance(false)()
			client, counter := newClient()
			if err := get(t, client, context.Background()); err != nil {
				t.Fatalf("an unlabeled send was refused while strict was off: %v", err)
			}
			if counter.calls != 1 {
				t.Fatalf("the wrapped transport ran %d times, want 1", counter.calls)
			}
		}()
		func() {
			defer restoreStrictEgressProvenance(true)()
			client, counter := newClient()
			err := get(t, client, context.Background())
			denied(t, err)
			if counter.calls != 0 {
				t.Fatalf("the wrapped transport ran %d times for an unlabeled egress under strict; zero bytes must move", counter.calls)
			}
			if !strings.Contains(err.Error(), "no send provenance") {
				t.Errorf("the strict refusal does not say what went wrong: %v", err)
			}
		}()
	})

	t.Run("a destination the boundary cannot identify is refused under a policy", func(t *testing.T) {
		policy := newMCPEgressPolicy()
		policy.SetScope(testScope(t, "req_charge", "https://payments.example.com/charge"))
		client, _ := newClient()
		req, err := http.NewRequestWithContext(mcpContextWithPolicy(context.Background(), policy), http.MethodGet, "ftp://files.example.com/x", nil)
		if err != nil {
			t.Fatalf("building the request: %v", err)
		}
		res, err := client.Do(req)
		if res != nil {
			_ = res.Body.Close()
		}
		denied(t, err)
	})
}

// restoreStrictEgressProvenance sets the flag and returns the restore func. The
// flag is a package-level var, so these subtests cannot be parallel.
func restoreStrictEgressProvenance(value bool) func() {
	previous := mcpStrictEgressProvenance
	mcpStrictEgressProvenance = value
	return func() { mcpStrictEgressProvenance = previous }
}

// --- the execution overlay (§3) ---------------------------------------------

// The overlay is where an MCP run's variable mutations live INSTEAD of
// AppState. Structure only in this wave; what is pinned here is that it hands
// out copies rather than its live maps, since the send path reads it on one
// goroutine while a script writes it on another.
func TestMCPExecutionOverlayHoldsDeltasWithoutSharingItsMaps(t *testing.T) {
	overlay := newMCPExecutionOverlay()
	if got := overlay.variables(mcpOverlayRuntime); got != nil {
		t.Errorf("a fresh overlay reported runtime deltas: %v", got)
	}
	overlay.mergeVariables(mcpOverlayRuntime, map[string]interface{}{"stepOneToken": "abc"})
	overlay.mergeVariables(mcpOverlayRuntime, map[string]interface{}{"stepTwoID": 7})
	overlay.mergeVariables(mcpOverlayEnv, map[string]interface{}{"baseUrl": "https://payments.example.com"})

	runtime := overlay.variables(mcpOverlayRuntime)
	if len(runtime) != 2 || runtime["stepOneToken"] != "abc" || runtime["stepTwoID"] != 7 {
		t.Fatalf("runtime deltas = %v", runtime)
	}
	// Mutating the returned map must not reach the overlay.
	runtime["stepOneToken"] = "tampered"
	if again := overlay.variables(mcpOverlayRuntime); again["stepOneToken"] != "abc" {
		t.Error("the overlay handed out its live map")
	}
	if env := overlay.variables(mcpOverlayEnv); env["baseUrl"] != "https://payments.example.com" {
		t.Errorf("env deltas = %v", env)
	}
	if overlay.variables(mcpOverlayGlobal) != nil || overlay.variables(mcpOverlayCollection) != nil {
		t.Error("scopes nothing wrote to reported deltas")
	}

	// An EMPTY jar is a real state a send can produce, and it must be
	// distinguishable from "no send has recorded one yet".
	if _, recorded := overlay.cookieSnapshot(); recorded {
		t.Error("a fresh overlay reported a recorded cookie jar")
	}
	overlay.recordCookies(nil)
	if cookies, recorded := overlay.cookieSnapshot(); !recorded || len(cookies) != 0 {
		t.Errorf("an empty recorded jar came back as (%v, %v)", cookies, recorded)
	}
	overlay.recordCookies([]types.CookieEntry{{Name: "session", Domain: "payments.example.com"}})
	cookies, recorded := overlay.cookieSnapshot()
	if !recorded || len(cookies) != 1 || cookies[0].Name != "session" {
		t.Fatalf("cookie snapshot = (%v, %v)", cookies, recorded)
	}
}

// --- the nil-policy cost -----------------------------------------------------

// A UI send carries no policy, and the checkpoints the later waves add run on
// that path too. This benchmark is the evidence that they cost nothing: a nil
// policy must not allocate and must not lock.
func BenchmarkNilPolicyAuthorize(b *testing.B) {
	var policy *mcpEgressPolicy
	origin := Origin{Scheme: "https", Host: "payments.example.com", Port: 443}
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := policy.Authorize(ctx, origin, egressKindMain); err != nil {
			b.Fatalf("a nil policy denied: %v", err)
		}
		if err := policy.AuthorizeNoPrompt(origin, egressKindMain); err != nil {
			b.Fatalf("a nil policy denied: %v", err)
		}
	}
}
