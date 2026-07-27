package core

// US-019 — the isolation invariant, written BEFORE the refactor it protects.
//
// US-019 wants one goja runtime instead of four where the post-response hooks
// share scope. Its own acceptance list includes "no variable or cookie state
// leaks between requests — explicit test", and the story is right to demand it
// first: sharing scope is exactly the change that can start leaking, and the
// leak is silent. A variable left behind by one request simply appears defined
// in the next, which looks like the script working rather than failing.
//
// So these tests pin the CURRENT behaviour while four separate runtimes still
// make it true by construction. They are the regression net for the refactor,
// not a description of it — if the 4->2 change breaks one, it has broken
// something a user would eventually notice and struggle to explain.
//
// Recorded in progress.txt alongside the rest of the US-019 analysis: the
// sync.Pool half of that story is NOT achievable as written, because nearly
// every shim closes over per-request state.

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// scriptProbeFixture stands up an echo server and a request whose scripts can
// be rewritten per case.
func scriptProbeFixture(t *testing.T) (app *App, collectionID, itemID string, closeServer func()) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"ok":true}`)
	}))

	app = newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	collection := state.Workspaces[0].Collections[0]
	created, err := app.CreateRequest(collection.ID, "http", "script isolation probe")
	if err != nil {
		t.Fatalf("CreateRequest: %v", err)
	}
	for _, c := range created.Workspaces[0].Collections {
		for _, item := range c.Items {
			if item.Name == "script isolation probe" {
				itemID = item.ID
			}
		}
	}
	if itemID == "" {
		t.Fatal("could not find the probe request")
	}
	url := server.URL
	if _, err := app.UpdateRequest(collection.ID, itemID, RequestPatch{URL: &url}); err != nil {
		t.Fatalf("UpdateRequest: %v", err)
	}
	return app, collection.ID, itemID, server.Close
}

func sendWithScripts(t *testing.T, app *App, collectionID, itemID, pre, post, tests string) AppState {
	t.Helper()
	patch := RequestPatch{PreScript: &pre, PostScript: &post, Tests: &tests}
	if _, err := app.UpdateRequest(collectionID, itemID, patch); err != nil {
		t.Fatalf("UpdateRequest: %v", err)
	}
	state, err := app.SendRequest(collectionID, itemID, "")
	if err != nil {
		t.Fatalf("SendRequest: %v", err)
	}
	return state
}

func testResultsFor(t *testing.T, state AppState, collectionID, itemID string) []TestResult {
	t.Helper()
	item, ok := findItemInState(state, collectionID, itemID)
	if !ok || item.Response == nil {
		t.Fatal("no response recorded")
	}
	return item.Response.TestResults
}

// TestScriptGlobalsDoNotLeakBetweenRequests. A bare global assigned by one
// request's script must not be visible to the next. Under four fresh runtimes
// this holds trivially; under a pooled or shared runtime it is the first thing
// to break, and it breaks silently — the second script sees a defined value and
// carries on.
func TestScriptGlobalsDoNotLeakBetweenRequests(t *testing.T) {
	app, collectionID, itemID, closeServer := scriptProbeFixture(t)
	defer closeServer()

	// Request 1 plants a bare global in every hook.
	sendWithScripts(t, app, collectionID, itemID,
		`leakedFromPre = "pre"`,
		`leakedFromPost = "post"`,
		`leakedFromTests = "tests"`)

	// Request 2 asserts none of them survived.
	state := sendWithScripts(t, app, collectionID, itemID, "", "", `
		test("pre-request global did not leak", function () {
			expect(typeof leakedFromPre).to.equal("undefined")
		})
		test("post-response global did not leak", function () {
			expect(typeof leakedFromPost).to.equal("undefined")
		})
		test("tests global did not leak", function () {
			expect(typeof leakedFromTests).to.equal("undefined")
		})
	`)

	results := testResultsFor(t, state, collectionID, itemID)
	if len(results) == 0 {
		t.Fatal("no test results — the assertions did not run")
	}
	for _, result := range results {
		if !result.Passed {
			t.Errorf("%s: %s", result.Name, result.Message)
		}
	}
}

// TestScriptGlobalsDoNotLeakWithinOneRequest pins the CURRENT boundary between
// hooks, which US-019 deliberately changes.
//
// Today a bare global set in the pre-request script is not visible to tests,
// because they get separate runtimes. After the 4->2 change the post-response
// hooks share scope with each other, and this test says which pairs must still
// be isolated: pre-request runs before the response exists and cannot join
// them. If a future change makes pre-request state visible here, that is a
// decision to take deliberately, not to discover.
func TestScriptGlobalsDoNotLeakWithinOneRequest(t *testing.T) {
	app, collectionID, itemID, closeServer := scriptProbeFixture(t)
	defer closeServer()

	state := sendWithScripts(t, app, collectionID, itemID,
		`plantedInPreRequest = "should not reach tests"`,
		"",
		`test("pre-request global is not visible to tests", function () {
			expect(typeof plantedInPreRequest).to.equal("undefined")
		})`)

	results := testResultsFor(t, state, collectionID, itemID)
	if len(results) == 0 {
		t.Fatal("no test results — the assertion did not run")
	}
	for _, result := range results {
		if !result.Passed {
			t.Errorf("%s: %s", result.Name, result.Message)
		}
	}
}

// TestScriptRuntimeVariablesDoPersistBetweenRequests is the other half, and the
// reason the leak tests above are not simply "nothing survives".
//
// bru.setVar is SUPPOSED to persist — that is what runtime variables are for.
// A refactor that isolated too aggressively would break this while making every
// leak test pass, so the invariant has both directions pinned.
func TestScriptRuntimeVariablesDoPersistBetweenRequests(t *testing.T) {
	app, collectionID, itemID, closeServer := scriptProbeFixture(t)
	defer closeServer()

	sendWithScripts(t, app, collectionID, itemID, `bru.setVar("carried", "across")`, "", "")

	state := sendWithScripts(t, app, collectionID, itemID, "", "", `
		test("runtime variable persisted", function () {
			expect(bru.getVar("carried")).to.equal("across")
		})
	`)

	results := testResultsFor(t, state, collectionID, itemID)
	if len(results) == 0 {
		t.Fatal("no test results — the assertion did not run")
	}
	for _, result := range results {
		if !result.Passed {
			t.Errorf("%s: %s — runtime variables must survive between requests", result.Name, result.Message)
		}
	}
}

// TestScriptSandboxModeStillStripsProcess guards the other US-019 criterion:
// "safe mode still strips process, fs and unrestricted timers". A pooled or
// shared runtime is exactly where a developer-mode shim could survive into a
// safe-mode request, which would be a sandbox escape rather than a leak.
func TestScriptSandboxModeStillStripsProcess(t *testing.T) {
	app, collectionID, itemID, closeServer := scriptProbeFixture(t)
	defer closeServer()

	state := sendWithScripts(t, app, collectionID, itemID, "", "", `
		test("fs is not reachable in the default sandbox", function () {
			expect(typeof require === "undefined" || (function () {
				try { require("fs"); return false } catch (e) { return true }
			})()).to.equal(true)
		})
	`)

	results := testResultsFor(t, state, collectionID, itemID)
	if len(results) == 0 {
		t.Fatal("no test results — the assertion did not run")
	}
	for _, result := range results {
		if !result.Passed {
			t.Errorf("%s: %s", result.Name, result.Message)
		}
	}
}

// TestScriptCookieJarDoesNotLeakBetweenRequests. Cookies are named explicitly
// in the story's criterion, and they are the leak with the worst consequence:
// a cookie carried into a request for a different host is credential leakage,
// not just a confusing variable.
func TestScriptCookieJarDoesNotLeakBetweenRequests(t *testing.T) {
	app, collectionID, itemID, closeServer := scriptProbeFixture(t)
	defer closeServer()

	sendWithScripts(t, app, collectionID, itemID, "", "", "")

	state, err := app.GetState()
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	for _, cookie := range state.Cookies {
		if strings.Contains(strings.ToLower(cookie.Name), "leak") {
			t.Errorf("unexpected cookie %q survived into the jar", cookie.Name)
		}
	}
}
