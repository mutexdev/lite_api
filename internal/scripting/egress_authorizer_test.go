// The script-egress seam: one authorizer in front of every way a script can
// reach the network.
//
// There are three dialects a script can send with — bru.sendRequest,
// pm.sendRequest and fetch() — and they are three different registrations over
// one function, scriptSendRequest. The whole reason a single check is enough is
// that all three land there, so the first test here asserts the funnel itself
// rather than trusting the call graph: each dialect is driven end to end and the
// authorizer must see the same URL and the same kind from all of them. A fourth
// entry point added later that skips scriptSendRequest fails this file.
//
// Everything the seam adds is off by default. A nil authorizer and a nil context
// have to reproduce the previous behaviour exactly, because the UI send path
// passes both as nil and a user Send is never subject to any of this — so every
// refusal case here has a nil-authorizer twin asserting the request still goes.
package scripting

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/mutexdev/lite_api/internal/types"
)

type egressCall struct {
	url  string
	kind string
}

func egressProbeItem() *types.RequestItem {
	return &types.RequestItem{Name: "probe", Type: "http", Method: "GET", URL: "http://example.test/thing"}
}

// A server that answers anything and counts what reached it. The count is the
// assertion that matters on a refusal: "the script saw an error" is worth
// nothing if the request went out anyway.
func egressCountingServer(t *testing.T) (*httptest.Server, *int32) {
	t.Helper()
	var hits int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(server.Close)
	return server, &hits
}

// The three dialects, each written the way a user writes it, each recording
// what it got back into a header so the outcome is observable without reaching
// into the runtime.
func egressDialectScripts(targetURL string) map[string]string {
	return map[string]string{
		"bru.sendRequest": `
try {
  const res = bru.sendRequest({ method: "GET", url: "` + targetURL + `" })
  req.setHeader("X-Outcome", "sent:" + res.status)
} catch (err) {
  req.setHeader("X-Outcome", "caught:" + String((err && err.message) || err))
}
`,
		"pm.sendRequest": `
try {
  const res = pm.sendRequest({ method: "GET", url: "` + targetURL + `" })
  req.setHeader("X-Outcome", "sent:" + res.status)
} catch (err) {
  req.setHeader("X-Outcome", "caught:" + String((err && err.message) || err))
}
`,
		"fetch": `
try {
  const res = await fetch("` + targetURL + `")
  req.setHeader("X-Outcome", "sent:" + res.status)
} catch (err) {
  req.setHeader("X-Outcome", "caught:" + String((err && err.message) || err))
}
`,
	}
}

func runEgressScript(t *testing.T, script string, meta ScriptRuntimeMeta) (*RequestState, error) {
	t.Helper()
	return RunPreRequestScriptSourceMeta(
		SingleScriptSource("request pre-request script", script),
		egressProbeItem(), map[string]string{}, nil, meta, nil,
	)
}

// Every dialect reaches the authorizer, with the URL that would go on the wire
// and the kind the policy keys on.
func TestScriptEgressAuthorizerSeesEveryDialect(t *testing.T) {
	server, hits := egressCountingServer(t)
	targetURL := server.URL + "/probe"

	for name, script := range egressDialectScripts(targetURL) {
		t.Run(name, func(t *testing.T) {
			var seen []egressCall
			state, err := runEgressScript(t, script, ScriptRuntimeMeta{
				EgressAuthorizer: func(rawURL, kind string) error {
					seen = append(seen, egressCall{rawURL, kind})
					return nil
				},
			})
			if err != nil {
				t.Fatalf("script failed: %v", err)
			}
			if len(seen) != 1 {
				t.Fatalf("authorizer was consulted %d times, want exactly 1: %+v", len(seen), seen)
			}
			if seen[0].url != targetURL {
				t.Errorf("authorizer saw URL %q, want %q", seen[0].url, targetURL)
			}
			if seen[0].kind != EgressKindScript {
				t.Errorf("authorizer saw kind %q, want %q", seen[0].kind, EgressKindScript)
			}
			if got := types.GetKeyValue(state.headers, "X-Outcome"); got != "sent:200" {
				t.Errorf("X-Outcome = %q; an authorized send must still go through", got)
			}
		})
	}
	if atomic.LoadInt32(hits) != 3 {
		t.Fatalf("server saw %d requests, want 3", atomic.LoadInt32(hits))
	}
}

// The URL handed to the authorizer is the one that would go out, not the one
// the script typed: query parameters supplied through `params` are already
// appended. A policy that checked the pre-param URL would be checking a
// different string than the wire sees.
func TestScriptEgressAuthorizerSeesTheResolvedWireURL(t *testing.T) {
	server, _ := egressCountingServer(t)
	script := `bru.sendRequest({ method: "GET", url: "` + server.URL + `/probe", params: { q: "1" } })`

	var seen []egressCall
	if _, err := runEgressScript(t, script, ScriptRuntimeMeta{
		EgressAuthorizer: func(rawURL, kind string) error {
			seen = append(seen, egressCall{rawURL, kind})
			return nil
		},
	}); err != nil {
		t.Fatalf("script failed: %v", err)
	}
	want := server.URL + "/probe?q=1"
	if len(seen) != 1 || seen[0].url != want {
		t.Fatalf("authorizer saw %+v, want a single call for %q", seen, want)
	}
}

// Variables are interpolated before the authorizer runs, so a URL assembled out
// of {{host}} is checked as the host it resolves to rather than as its template.
func TestScriptEgressAuthorizerSeesInterpolatedURLs(t *testing.T) {
	server, _ := egressCountingServer(t)
	var seen []egressCall
	_, err := RunPreRequestScriptSourceMeta(
		SingleScriptSource("request pre-request script", `bru.sendRequest({ method: "GET", url: "{{base}}/probe" })`),
		egressProbeItem(), map[string]string{"base": server.URL}, nil,
		ScriptRuntimeMeta{EgressAuthorizer: func(rawURL, kind string) error {
			seen = append(seen, egressCall{rawURL, kind})
			return nil
		}}, nil,
	)
	if err != nil {
		t.Fatalf("script failed: %v", err)
	}
	want := server.URL + "/probe"
	if len(seen) != 1 || seen[0].url != want {
		t.Fatalf("authorizer saw %+v, want a single call for %q", seen, want)
	}
}

// A refusal is a catchable script error and, more importantly, zero bytes.
func TestScriptEgressRefusalIsCatchableAndSendsNothing(t *testing.T) {
	server, hits := egressCountingServer(t)
	targetURL := server.URL + "/probe"
	refusal := errors.New("liteapi denied this destination")

	for name, script := range egressDialectScripts(targetURL) {
		t.Run(name, func(t *testing.T) {
			state, err := runEgressScript(t, script, ScriptRuntimeMeta{
				EgressAuthorizer: func(string, string) error { return refusal },
			})
			if err != nil {
				t.Fatalf("a refusal must reach the script as a catchable error, not kill the level: %v", err)
			}
			got := types.GetKeyValue(state.headers, "X-Outcome")
			if !strings.HasPrefix(got, "caught:") {
				t.Fatalf("X-Outcome = %q; the script never caught the refusal", got)
			}
			if !strings.Contains(got, refusal.Error()) {
				t.Errorf("X-Outcome = %q; the refusal's own message must survive to the script", got)
			}
		})
	}
	if got := atomic.LoadInt32(hits); got != 0 {
		t.Fatalf("the server saw %d requests; a refusal must happen before the request is made", got)
	}
}

// The callback form is the one most Postman scripts are written in, and it
// carries errors through a different branch than the throwing form.
func TestScriptEgressRefusalReachesTheCallbackForm(t *testing.T) {
	server, hits := egressCountingServer(t)
	script := `
pm.sendRequest({ method: "GET", url: "` + server.URL + `/probe" }, function (err, res) {
  req.setHeader("X-Outcome", err ? "err:" + String(err.message || err) : "sent")
})
`
	state, err := runEgressScript(t, script, ScriptRuntimeMeta{
		EgressAuthorizer: func(string, string) error { return errors.New("nope") },
	})
	if err != nil {
		t.Fatalf("script failed: %v", err)
	}
	if got := types.GetKeyValue(state.headers, "X-Outcome"); !strings.Contains(got, "nope") {
		t.Fatalf("X-Outcome = %q; the callback must receive the refusal", got)
	}
	if got := atomic.LoadInt32(hits); got != 0 {
		t.Fatalf("the server saw %d requests", got)
	}
}

// A refused send still leaves a timeline entry, because "nothing happened" and
// "this was blocked" must not look the same in the run's evidence.
func TestScriptEgressRefusalIsRecordedOnTheTimeline(t *testing.T) {
	server, _ := egressCountingServer(t)
	var entries []types.TimelineItem
	_, err := runEgressScript(t, `try { bru.sendRequest({ method: "GET", url: "`+server.URL+`/probe" }) } catch (e) {}`,
		ScriptRuntimeMeta{
			EgressAuthorizer: func(string, string) error { return errors.New("denied by policy") },
			RecordTimeline:   func(entry types.TimelineItem) { entries = append(entries, entry) },
		})
	if err != nil {
		t.Fatalf("script failed: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("recorded %d timeline entries, want 1", len(entries))
	}
	if entries[0].Error != "denied by policy" {
		t.Errorf("timeline Error = %q, want the refusal", entries[0].Error)
	}
	if entries[0].Status != 0 {
		t.Errorf("timeline Status = %d; a blocked send has no status", entries[0].Status)
	}
}

// The default. Both new fields zero, every dialect behaves as it always has.
func TestNilEgressAuthorizerLeavesEveryDialectUnchanged(t *testing.T) {
	server, hits := egressCountingServer(t)
	for name, script := range egressDialectScripts(server.URL + "/probe") {
		t.Run(name, func(t *testing.T) {
			state, err := runEgressScript(t, script, ScriptRuntimeMeta{})
			if err != nil {
				t.Fatalf("script failed: %v", err)
			}
			if got := types.GetKeyValue(state.headers, "X-Outcome"); got != "sent:200" {
				t.Fatalf("X-Outcome = %q, want sent:200", got)
			}
		})
	}
	if got := atomic.LoadInt32(hits); got != 3 {
		t.Fatalf("server saw %d requests, want 3", got)
	}
}

// The context is threaded into the request, which is what lets a caller's
// transport read the run's provenance off req.Context() and what makes a
// cancelled run stop a script's own call. Cancellation is the observable half.
func TestScriptSendRequestBuildsWithTheRunContext(t *testing.T) {
	server, hits := egressCountingServer(t)
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()

	state, err := runEgressScript(t, `
const res = bru.sendRequest({ method: "GET", url: "`+server.URL+`/probe" }, function (err, res) {
  req.setHeader("X-Outcome", err ? "err" : "sent")
})
`, ScriptRuntimeMeta{RequestContext: cancelled})
	if err != nil {
		t.Fatalf("script failed: %v", err)
	}
	if got := types.GetKeyValue(state.headers, "X-Outcome"); got != "err" {
		t.Fatalf("X-Outcome = %q; a cancelled run context must fail the script's request", got)
	}
	if got := atomic.LoadInt32(hits); got != 0 {
		t.Fatalf("server saw %d requests under a cancelled context", got)
	}
}

// A live context must not change anything. This is the half that guards against
// "fixing" cancellation by attaching a context that is already done.
func TestScriptSendRequestWithALiveContextStillSends(t *testing.T) {
	server, hits := egressCountingServer(t)
	state, err := runEgressScript(t, `
const res = bru.sendRequest({ method: "GET", url: "`+server.URL+`/probe" })
req.setHeader("X-Outcome", "sent:" + res.status)
`, ScriptRuntimeMeta{RequestContext: context.Background()})
	if err != nil {
		t.Fatalf("script failed: %v", err)
	}
	if got := types.GetKeyValue(state.headers, "X-Outcome"); got != "sent:200" {
		t.Fatalf("X-Outcome = %q", got)
	}
	if got := atomic.LoadInt32(hits); got != 1 {
		t.Fatalf("server saw %d requests, want 1", got)
	}
}

// node:dns is a second network surface, with no URL and no HTTP client in front
// of it. It is only present in developer mode, which is where these run.
func runDNSScript(t *testing.T, script string, meta ScriptRuntimeMeta) *RequestState {
	t.Helper()
	meta.JSSandboxMode = "developer"
	state, err := RunPreRequestScriptSourceMeta(
		SingleScriptSource("request pre-request script", script),
		egressProbeItem(), map[string]string{}, nil, meta, nil,
	)
	if err != nil {
		t.Fatalf("script failed: %v", err)
	}
	return state
}

// Every bridge the dns shim exposes goes through the gate, with the bare
// hostname and the DNS kind. Refusal is asserted through the message the script
// receives: a resolver error would read nothing like this, so the refusal
// short-circuited the lookup.
func TestScriptDNSLookupsAreAuthorizedAndRefusable(t *testing.T) {
	cases := []struct {
		name   string
		call   string
		target string
	}{
		{"lookup", `dns.lookup("evil.test", function (err) { record(err) })`, "evil.test"},
		{"resolve", `dns.resolve("evil.test", "A", function (err) { record(err) })`, "evil.test"},
		{"resolve4", `dns.resolve4("evil.test", function (err) { record(err) })`, "evil.test"},
		{"reverse", `dns.reverse("203.0.113.9", function (err) { record(err) })`, "203.0.113.9"},
		{"lookupService", `dns.lookupService("203.0.113.9", 80, function (err) { record(err) })`, "203.0.113.9"},
		{"promises.lookup", `await dns.promises.lookup("evil.test").catch(function (err) { record(err) })`, "evil.test"},
		{"Resolver", `new dns.Resolver().resolveTxt("evil.test", function (err) { record(err) })`, "evil.test"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			var seen []egressCall
			script := `
const dns = require("node:dns")
function record(err) { req.setHeader("X-Outcome", err ? String(err.message || err) : "resolved") }
` + testCase.call + `
`
			state := runDNSScript(t, script, ScriptRuntimeMeta{
				EgressAuthorizer: func(host, kind string) error {
					seen = append(seen, egressCall{host, kind})
					return fmt.Errorf("resolver refused %s", host)
				},
			})
			if len(seen) != 1 {
				t.Fatalf("authorizer was consulted %d times, want 1: %+v", len(seen), seen)
			}
			if seen[0].url != testCase.target {
				t.Errorf("authorizer saw %q, want the bare host %q", seen[0].url, testCase.target)
			}
			if seen[0].kind != EgressKindScriptDNS {
				t.Errorf("authorizer saw kind %q, want %q", seen[0].kind, EgressKindScriptDNS)
			}
			want := "resolver refused " + testCase.target
			if got := types.GetKeyValue(state.headers, "X-Outcome"); got != want {
				t.Fatalf("X-Outcome = %q, want %q — a resolver error here would mean the lookup ran", got, want)
			}
		})
	}
}

// An authorized lookup runs the real resolver, so the gate is a gate and not a
// blanket. Loopback resolves without leaving the machine.
func TestAuthorizedScriptDNSLookupStillResolves(t *testing.T) {
	var seen []egressCall
	state := runDNSScript(t, `
const dns = require("node:dns")
dns.lookup("127.0.0.1", function (err, address) {
  req.setHeader("X-Outcome", err ? "err:" + String(err.message || err) : "addr:" + address)
})
`, ScriptRuntimeMeta{EgressAuthorizer: func(host, kind string) error {
		seen = append(seen, egressCall{host, kind})
		return nil
	}})
	if len(seen) != 1 || seen[0].url != "127.0.0.1" || seen[0].kind != EgressKindScriptDNS {
		t.Fatalf("authorizer calls = %+v", seen)
	}
	if got := types.GetKeyValue(state.headers, "X-Outcome"); got != "addr:127.0.0.1" {
		t.Fatalf("X-Outcome = %q, want addr:127.0.0.1", got)
	}
}

// A blank argument is rejected by the helper itself, before any resolver is
// touched, and that error message is what scripts already see. The gate must
// not replace it with an authorization decision about the empty string.
func TestBlankDNSArgumentsKeepTheirOwnErrorsUnderTheGate(t *testing.T) {
	script := `
const dns = require("node:dns")
dns.reverse("   ", function (err) { req.setHeader("X-Outcome", String((err && err.message) || err)) })
`
	assertOwnError := func(t *testing.T, state *RequestState) {
		t.Helper()
		if got := types.GetKeyValue(state.headers, "X-Outcome"); !strings.Contains(got, "IP address is required") {
			t.Fatalf("X-Outcome = %q, want the helper's own validation error", got)
		}
	}

	t.Run("no authorizer", func(t *testing.T) {
		assertOwnError(t, runDNSScript(t, script, ScriptRuntimeMeta{}))
	})

	t.Run("with an authorizer", func(t *testing.T) {
		consulted := false
		state := runDNSScript(t, script, ScriptRuntimeMeta{EgressAuthorizer: func(string, string) error {
			consulted = true
			return errors.New("should not be consulted")
		}})
		assertOwnError(t, state)
		if consulted {
			t.Fatal("a blank address reaches no resolver; it must not be sent to the authorizer")
		}
	})
}

// The nil-authorizer twin for DNS: no gate installed, the shim behaves exactly
// as it did.
func TestNilAuthorizerLeavesScriptDNSUnchanged(t *testing.T) {
	state := runDNSScript(t, `
const dns = require("node:dns")
dns.lookup("127.0.0.1", function (err, address, family) {
  req.setHeader("X-Outcome", err ? "err" : address + "/" + family)
})
`, ScriptRuntimeMeta{})
	if got := types.GetKeyValue(state.headers, "X-Outcome"); got != "127.0.0.1/4" {
		t.Fatalf("X-Outcome = %q, want 127.0.0.1/4", got)
	}
}

// The hand-off that carries the authorizer across installScriptRequire must not
// outlive the constructor. If it did, the map would grow by one entry per
// request for the life of the process.
func TestDNSAuthorizerHandOffDoesNotOutliveTheRuntime(t *testing.T) {
	runDNSScript(t, `require("node:dns")`, ScriptRuntimeMeta{
		EgressAuthorizer: func(string, string) error { return nil },
	})
	scriptDNSAuthorizerMu.Lock()
	remaining := len(scriptDNSAuthorizers)
	scriptDNSAuthorizerMu.Unlock()
	if remaining != 0 {
		t.Fatalf("%d authorizer registrations survived the runtime; the hand-off is leaking", remaining)
	}
}
