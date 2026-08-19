// pm.request.abort(): the community idiom for "do not send this request".
//
// It is NOT part of Postman's documented API. postman-collection's Request
// carries no abort method and postman-sandbox uses the word only for its own
// internal execution-abort event, so the call throws a TypeError in Postman
// too. It survives in circulation because a pre-request script that throws
// stops that request from being sent — which is precisely what the author
// wanted, so the bug looks like the feature working.
//
// That leaves LiteAPI a choice, because it has a real implementation of the
// intent: pm.execution.skipRequest(). Reproducing Postman's failure gives the
// user "Object has no member 'abort'" — a message that names no script, no
// line, and nothing they could act on — while honouring the call does exactly
// what every author of it meant. The second is the better answer for an app
// whose job is running other people's collections.
//
// So abort() is wired as an alias of skipRequest, and it is an ALIAS rather
// than a reimplementation: the runner's skip machinery is what the send path
// actually consults, and a parallel flag would be a skip the sender never sees.
package scripting

import (
	"testing"

	"github.com/mutexdev/lite_api/internal/types"
)

func TestPostmanRequestAbortIsTheSameFunctionAsSkipRequest(t *testing.T) {
	item := &types.RequestItem{Name: "abort", URL: "http://example.test", Method: "GET"}
	script := `
if (typeof pm.request.abort !== "function") throw new Error("pm.request.abort is missing");
if (pm.request.abort !== pm.execution.skipRequest) throw new Error("abort is not the skipRequest function");
if (pm.request.abort !== bru.runner.skipRequest) throw new Error("abort is not bru.runner.skipRequest");
`
	if err := RunPreRequestScript(script, item, map[string]string{}, nil); err != nil {
		t.Fatalf("pm.request.abort is not wired to the skip machinery: %v", err)
	}
}

// The behaviour that matters: calling it marks the request as skipped, which is
// the flag the send path reads. Asserting the flag rather than the absence of an
// error is the point — an abort() that threw nothing but also skipped nothing
// would pass a weaker test while still sending the request.
func TestPostmanRequestAbortMarksTheRequestSkipped(t *testing.T) {
	item := &types.RequestItem{Name: "abort", URL: "http://example.test", Method: "GET"}
	state, err := RunPreRequestScriptWithJarStateMeta(
		`pm.request.abort();`, item, map[string]string{}, nil, ScriptRuntimeMeta{},
	)
	if err != nil {
		t.Fatalf("pm.request.abort() threw: %v", err)
	}
	if state == nil {
		t.Fatal("no request state was returned")
	}
	if !state.SkipRequest {
		t.Fatal("pm.request.abort() did not mark the request as skipped")
	}
}

// The verbatim shape found in real collections: a guard that aborts on a
// condition. The false branch must leave the request alone — an abort that
// fired unconditionally would silently stop every request in the collection.
func TestPostmanRequestAbortOnlySkipsWhenItIsReached(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		skipFlag string
		want     bool
	}{
		{name: "guard taken", skipFlag: "true", want: true},
		{name: "guard not taken", skipFlag: "false", want: false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			item := &types.RequestItem{Name: "abort", URL: "http://example.test", Method: "GET"}
			vars := map[string]string{"skipRequest": testCase.skipFlag}
			// The collections in the wild write this guard with
			// pm.environment.get. It is pm.variables.get here because the map
			// passed to RunPreRequestScript is the runtime scope, and reaching
			// the environment scope would mean building an Environment — which
			// would make this a test of variable plumbing rather than of
			// whether abort fires only on the branch that reaches it.
			script := `
if (pm.variables.get("skipRequest") === "true") {
    pm.request.abort();
}
`
			state, err := RunPreRequestScriptWithJarStateMeta(script, item, vars, nil, ScriptRuntimeMeta{})
			if err != nil {
				t.Fatalf("script failed: %v", err)
			}
			if state.SkipRequest != testCase.want {
				t.Fatalf("SkipRequest = %v, want %v", state.SkipRequest, testCase.want)
			}
		})
	}
}
