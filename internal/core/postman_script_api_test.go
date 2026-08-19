package core

// US-039 — the live `pm` object.
//
// The trap this story sets is that `pm` is easy to make LOOK right. A pm.test
// that maintains its own results list runs the assertion, reports nothing to
// the runner, and produces a green run whose tests all failed. So the tests
// below never check that pm.test "works" — they check that a pm.test FAILURE
// reaches the same TestResults the runner reads.

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/mutexdev/lite_api/internal/importers"
	"github.com/mutexdev/lite_api/internal/scripting"
)

func TestPostmanEventName(t *testing.T) {
	// Postman has only two events. Both post-response phases must report
	// "test", or the `pm.info.eventName === "test"` guard scripts actually
	// write would silently never fire.
	for phase, want := range map[string]string{
		"pre-request":   "prerequest",
		"post-response": "test",
		"tests":         "test",
		"":              "test",
	} {
		if got := scripting.PostmanEventName(phase); got != want {
			t.Errorf("scripting.PostmanEventName(%q) = %q, want %q", phase, got, want)
		}
	}
}

// TestPmTestFailuresReachTheRunner is the story. A separate registry would pass
// every "does pm.test exist" check while reporting a green run.
func TestPmTestFailuresReachTheRunner(t *testing.T) {
	app, collectionID, itemID, closeServer := scriptProbeFixture(t)
	defer closeServer()

	state := sendWithScripts(t, app, collectionID, itemID, "", "", `
		pm.test("pm assertion that passes", function () {
			pm.expect(1).to.equal(1)
		})
		pm.test("pm assertion that fails", function () {
			pm.expect(1).to.equal(2)
		})
	`)

	results := testResultsFor(t, state, collectionID, itemID)
	if len(results) != 2 {
		t.Fatalf("got %d test results, want 2 — pm.test must report to the same registry as test", len(results))
	}

	byName := map[string]TestResult{}
	for _, result := range results {
		byName[result.Name] = result
	}
	if !byName["pm assertion that passes"].Passed {
		t.Errorf("passing pm.test was reported as failed: %s", byName["pm assertion that passes"].Message)
	}
	failed, present := byName["pm assertion that fails"]
	if !present {
		t.Fatal("the failing pm.test produced no result row at all")
	}
	if failed.Passed {
		t.Error("a failing pm.expect was reported as passed — the run would be green with a broken assertion")
	}
	if failed.Message == "" {
		t.Error("a failed assertion should say what went wrong")
	}
}

// TestPmAndBruTestsShareOneRegistry. Both surfaces are live at once; a script
// mixing them must produce one ordered list, not two.
func TestPmAndBruTestsShareOneRegistry(t *testing.T) {
	app, collectionID, itemID, closeServer := scriptProbeFixture(t)
	defer closeServer()

	state := sendWithScripts(t, app, collectionID, itemID, "", "", `
		test("native first", function () { expect(true).to.equal(true) })
		pm.test("pm second", function () { pm.expect(true).to.equal(true) })
		test("native third", function () { expect(true).to.equal(true) })
	`)

	results := testResultsFor(t, state, collectionID, itemID)
	var names []string
	for _, result := range results {
		names = append(names, result.Name)
	}
	want := "native first,pm second,native third"
	if strings.Join(names, ",") != want {
		t.Errorf("results were %q, want %q — pm.test and test must share one ordered registry", strings.Join(names, ","), want)
	}
}

// TestPmInfoReportsTheRequest covers pm.info outside a collection run, where
// Postman reports a single iteration rather than none.
func TestPmInfoReportsTheRequest(t *testing.T) {
	app, collectionID, itemID, closeServer := scriptProbeFixture(t)
	defer closeServer()

	state := sendWithScripts(t, app, collectionID, itemID, "", "", `
		test("requestName is the item's name", function () {
			expect(pm.info.requestName).to.equal("script isolation probe")
		})
		test("requestId is populated", function () {
			expect(typeof pm.info.requestId).to.equal("string")
			expect(pm.info.requestId.length > 0).to.equal(true)
		})
		test("eventName in the tests phase is test", function () {
			expect(pm.info.eventName).to.equal("test")
		})
		test("a one-off send is iteration 0 of 1", function () {
			expect(pm.info.iteration).to.equal(0)
			expect(pm.info.iterationCount).to.equal(1)
		})
	`)

	for _, result := range testResultsFor(t, state, collectionID, itemID) {
		if !result.Passed {
			t.Errorf("%s: %s", result.Name, result.Message)
		}
	}
}

// TestPmInfoEventNameInThePreRequestPhase. The pre-request phase is the only
// one that reports "prerequest", and it is a different runtime construction —
// checking only the tests phase would leave the mapping half untested.
func TestPmInfoEventNameInThePreRequestPhase(t *testing.T) {
	app, collectionID, itemID, closeServer := scriptProbeFixture(t)
	defer closeServer()

	state := sendWithScripts(t, app, collectionID, itemID,
		`bru.setVar("preEventName", pm.info.eventName)`,
		"",
		`test("the pre-request phase reported prerequest", function () {
			expect(bru.getVar("preEventName")).to.equal("prerequest")
		})`)

	for _, result := range testResultsFor(t, state, collectionID, itemID) {
		if !result.Passed {
			t.Errorf("%s: %s", result.Name, result.Message)
		}
	}
}

// TestPmInfoIterationCountsDuringACollectionRun is where the 0-based
// conversion earns its place: a script copied out of Postman guards on
// `pm.info.iteration === 0` for first-iteration setup, and a 1-based value
// would make that guard never fire, with no error anywhere.
//
// The assertions read the response's TestResults, NOT the runner's pass/fail
// tally. A failed assertion does not currently mark a RunResult failed — only
// a transport error or a >=400 status does — so a tally-based check here would
// stay green against a broken pm.info and prove nothing.
func TestPmInfoIterationCountsDuringACollectionRun(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `{"ok":true}`)
	}))
	defer server.Close()

	// The stored response is the LAST iteration's, so the expected 0-based
	// index is one less than the run length.
	for _, tc := range []struct {
		iterations int
		wantIndex  int
		wantCount  int
	}{
		{1, 0, 1},
		{3, 2, 3},
	} {
		t.Run(fmt.Sprintf("%d-iterations", tc.iterations), func(t *testing.T) {
			app, collectionID, ids := iterationFixture(t, server.URL, 1)
			tests := fmt.Sprintf(`
				test("iteration is 0-based", function () {
					expect(pm.info.iteration).to.equal(%d)
				})
				test("iterationCount is the run length", function () {
					expect(pm.info.iterationCount).to.equal(%d)
				})
			`, tc.wantIndex, tc.wantCount)
			if _, err := app.UpdateRequest(collectionID, ids[0], RequestPatch{Tests: &tests}); err != nil {
				t.Fatalf("UpdateRequest: %v", err)
			}

			state, err := app.RunCollectionWithOptions(collectionID, "", RunnerOptions{
				SelectedItemIDs: ids,
				Iterations:      tc.iterations,
			})
			if err != nil {
				t.Fatalf("RunCollectionWithOptions: %v", err)
			}

			results := testResultsFor(t, state, collectionID, ids[0])
			if len(results) != 2 {
				t.Fatalf("got %d test results, want 2 — the tests script did not run", len(results))
			}
			for _, result := range results {
				if !result.Passed {
					t.Errorf("%s: %s", result.Name, result.Message)
				}
			}
		})
	}
}

// TestPmDoesNotDisplaceBru. `pm` is added BESIDE bru, not instead of it; an
// installation that overwrote a global would break every existing script.
func TestPmDoesNotDisplaceBru(t *testing.T) {
	app, collectionID, itemID, closeServer := scriptProbeFixture(t)
	defer closeServer()

	state := sendWithScripts(t, app, collectionID, itemID, "", "", `
		test("bru is still present", function () {
			expect(typeof bru.setVar).to.equal("function")
		})
		test("the global test and expect are still present", function () {
			expect(typeof test).to.equal("function")
			expect(typeof expect).to.equal("function")
		})
		test("pm is present alongside them", function () {
			expect(typeof pm.test).to.equal("function")
			expect(typeof pm.expect).to.equal("function")
			expect(typeof pm.info).to.equal("object")
		})
	`)

	for _, result := range testResultsFor(t, state, collectionID, itemID) {
		if !result.Passed {
			t.Errorf("%s: %s", result.Name, result.Message)
		}
	}
}

// TestPmVariableScopesAreDistinct is US-040, and the criterion the story calls
// out as "the bug in the import table".
//
// The import translator maps pm.environment.set, pm.collectionVariables.set,
// pm.globals.set and pm.variables.set ALL onto bru.setVar. Nothing errors, and
// a script that writes to one scope and reads from another gets its value back
// — so the collapse looks like it works, right up to the point where a second
// environment or collection is supposed to see a different value and does not.
//
// Each assertion below writes to exactly one scope and demands the other three
// stay empty. A collapsed implementation fails every one of them.
func TestPmVariableScopesAreDistinct(t *testing.T) {
	app, collectionID, itemID, closeServer := scriptProbeFixture(t)
	defer closeServer()

	state := sendWithScripts(t, app, collectionID, itemID, "", "", `
		pm.environment.set("scoped", "environment")
		pm.collectionVariables.set("scoped", "collection")
		pm.globals.set("scoped", "global")

		test("the environment scope kept its own value", function () {
			expect(pm.environment.get("scoped")).to.equal("environment")
		})
		test("the collection scope kept its own value", function () {
			expect(pm.collectionVariables.get("scoped")).to.equal("collection")
		})
		test("the global scope kept its own value", function () {
			expect(pm.globals.get("scoped")).to.equal("global")
		})
		test("writing one scope did not write the runtime scope", function () {
			pm.environment.set("envOnly", "e")
			expect(bru.hasVar("envOnly")).to.equal(false)
		})
		test("the collection scope did not leak into the environment", function () {
			pm.collectionVariables.set("collectionOnly", "c")
			expect(pm.environment.has("collectionOnly")).to.equal(false)
			expect(pm.globals.has("collectionOnly")).to.equal(false)
		})
		test("the global scope did not leak into the collection", function () {
			pm.globals.set("globalOnly", "g")
			expect(pm.collectionVariables.has("globalOnly")).to.equal(false)
			expect(pm.environment.has("globalOnly")).to.equal(false)
		})
	`)

	for _, result := range testResultsFor(t, state, collectionID, itemID) {
		if !result.Passed {
			t.Errorf("%s: %s", result.Name, result.Message)
		}
	}
}

// TestPmScopesDelegateToBru. Each pm scope must be the SAME storage as its bru
// counterpart, not a parallel copy — the dirty tracking that decides whether an
// environment is written back to disk lives in the bru closures, and a parallel
// implementation would update variables that silently never persist.
func TestPmScopesDelegateToBru(t *testing.T) {
	app, collectionID, itemID, closeServer := scriptProbeFixture(t)
	defer closeServer()

	state := sendWithScripts(t, app, collectionID, itemID, "", "", `
		bru.setEnvVar("viaBru", "1")
		pm.environment.set("viaPm", "2")
		test("pm reads what bru wrote", function () {
			expect(pm.environment.get("viaBru")).to.equal("1")
		})
		test("bru reads what pm wrote", function () {
			expect(bru.getEnvVar("viaPm")).to.equal("2")
		})

		bru.setCollectionVar("cBru", "1")
		pm.collectionVariables.set("cPm", "2")
		test("collection scope is shared with bru", function () {
			expect(pm.collectionVariables.get("cBru")).to.equal("1")
			expect(bru.getCollectionVar("cPm")).to.equal("2")
		})

		bru.setGlobalEnvVar("gBru", "1")
		pm.globals.set("gPm", "2")
		test("global scope is shared with bru", function () {
			expect(pm.globals.get("gBru")).to.equal("1")
			expect(bru.getGlobalEnvVar("gPm")).to.equal("2")
		})
	`)

	for _, result := range testResultsFor(t, state, collectionID, itemID) {
		if !result.Passed {
			t.Errorf("%s: %s", result.Name, result.Message)
		}
	}
}

// TestPmVariablesReadsTheResolvedChain. pm.variables is the one scope that is
// not its own storage: it reads across everything and writes to the runtime
// scope. Giving it private storage would make a value set through it invisible
// to {{var}} interpolation.
func TestPmVariablesReadsTheResolvedChain(t *testing.T) {
	app, collectionID, itemID, closeServer := scriptProbeFixture(t)
	defer closeServer()

	state := sendWithScripts(t, app, collectionID, itemID, "", "", `
		pm.environment.set("fromEnvironment", "e")
		pm.collectionVariables.set("fromCollection", "c")
		pm.globals.set("fromGlobal", "g")
		bru.setVar("fromRuntime", "r")

		test("pm.variables sees every scope", function () {
			expect(pm.variables.get("fromEnvironment")).to.equal("e")
			expect(pm.variables.get("fromCollection")).to.equal("c")
			expect(pm.variables.get("fromGlobal")).to.equal("g")
			expect(pm.variables.get("fromRuntime")).to.equal("r")
		})
		test("pm.variables.has agrees with get", function () {
			expect(pm.variables.has("fromCollection")).to.equal(true)
			expect(pm.variables.has("definitelyNotSet")).to.equal(false)
		})
		test("an unset name is undefined, not empty string", function () {
			expect(typeof pm.variables.get("definitelyNotSet")).to.equal("undefined")
		})
		test("pm.variables.set writes the runtime scope", function () {
			pm.variables.set("written", "w")
			expect(bru.getVar("written")).to.equal("w")
			expect(pm.variables.get("written")).to.equal("w")
		})
		test("replaceIn interpolates against the resolved chain", function () {
			expect(pm.variables.replaceIn("{{fromEnvironment}}/{{fromCollection}}")).to.equal("e/c")
		})
	`)

	for _, result := range testResultsFor(t, state, collectionID, itemID) {
		if !result.Passed {
			t.Errorf("%s: %s", result.Name, result.Message)
		}
	}
}

// TestPmScopeMethodsAreAllPresent. Postman scripts use unset, clear and
// toObject as much as get and set; a missing one is a TypeError mid-run rather
// than a failed assertion.
func TestPmScopeMethodsAreAllPresent(t *testing.T) {
	app, collectionID, itemID, closeServer := scriptProbeFixture(t)
	defer closeServer()

	state := sendWithScripts(t, app, collectionID, itemID, "", "", `
		["environment", "collectionVariables", "globals"].forEach(function (name) {
			["get", "set", "unset", "has", "clear", "toObject", "replaceIn"].forEach(function (method) {
				test("pm." + name + "." + method + " is a function", function () {
					expect(typeof pm[name][method]).to.equal("function")
				})
			})
		})
		test("unset removes only from its own scope", function () {
			pm.environment.set("temp", "1")
			pm.collectionVariables.set("temp", "2")
			pm.environment.unset("temp")
			expect(pm.environment.has("temp")).to.equal(false)
			expect(pm.collectionVariables.get("temp")).to.equal("2")
		})
		test("toObject returns that scope's contents", function () {
			pm.globals.set("inGlobals", "yes")
			expect(pm.globals.toObject().inGlobals).to.equal("yes")
			expect(pm.environment.toObject().inGlobals).to.equal(undefined)
		})
		test("clear empties only its own scope", function () {
			pm.environment.set("survivor", "1")
			pm.collectionVariables.set("survivor", "2")
			pm.environment.clear()
			expect(pm.environment.has("survivor")).to.equal(false)
			expect(pm.collectionVariables.get("survivor")).to.equal("2")
		})
	`)

	results := testResultsFor(t, state, collectionID, itemID)
	if len(results) < 24 {
		t.Fatalf("only %d assertions ran, want at least 24 — the method sweep did not execute", len(results))
	}
	for _, result := range results {
		if !result.Passed {
			t.Errorf("%s: %s", result.Name, result.Message)
		}
	}
}

// US-041 — pm.request and pm.response.
//
// The assertion chain is where this story can go silently wrong. An assertion
// that returns false instead of throwing lets
// pm.test("status is 200", () => pm.response.to.have.status(200)) pass while
// the status is 500 — a green test suite over a broken API. So every assertion
// below is tested in BOTH directions: it must pass when true and it must
// produce a FAILED TestResult when false.

func TestPmRequestReflectsTheRequest(t *testing.T) {
	app, collectionID, itemID, closeServer := scriptProbeFixture(t)
	defer closeServer()

	state := sendWithScripts(t, app, collectionID, itemID,
		`req.setHeader("X-Added-In-Pre", "yes")`,
		"",
		`
		test("method is exposed", function () {
			expect(pm.request.method).to.equal("GET")
		})
		test("name is exposed", function () {
			expect(pm.request.name).to.equal("script isolation probe")
		})
		test("url is an object with toString", function () {
			expect(typeof pm.request.url).to.equal("object")
			expect(pm.request.url.toString().indexOf("http")).to.equal(0)
		})
		test("url exposes host and path", function () {
			expect(typeof pm.request.url.getHost()).to.equal("string")
			expect(typeof pm.request.url.getPath()).to.equal("string")
		})
		test("headers.get sees a header added in the pre-request script", function () {
			expect(pm.request.headers.get("X-Added-In-Pre")).to.equal("yes")
			expect(pm.request.headers.has("X-Added-In-Pre")).to.equal(true)
			expect(pm.request.headers.has("X-Never-Set")).to.equal(false)
		})
		test("getHeaders returns an object", function () {
			expect(typeof pm.request.getHeaders()).to.equal("object")
		})
	`)

	for _, result := range testResultsFor(t, state, collectionID, itemID) {
		if !result.Passed {
			t.Errorf("%s: %s", result.Name, result.Message)
		}
	}
}

// TestPmResponseUsesPostmanStatusSemantics. In Postman pm.response.status is
// the status TEXT and pm.response.code is the number, while this codebase's
// res.status is the number. Inside pm.* Postman's meaning has to win, or every
// script copied across that compares pm.response.status to "OK" is quietly
// always false.
func TestPmResponseUsesPostmanStatusSemantics(t *testing.T) {
	app, collectionID, itemID, closeServer := scriptProbeFixture(t)
	defer closeServer()

	state := sendWithScripts(t, app, collectionID, itemID, "", "", `
		test("code is the number", function () {
			expect(pm.response.code).to.equal(200)
		})
		test("status is the text, not the number", function () {
			expect(pm.response.status).to.equal("OK")
		})
		test("res.status is still the number for bru scripts", function () {
			expect(res.status).to.equal(200)
		})
		test("responseTime and responseSize are numbers", function () {
			expect(typeof pm.response.responseTime).to.equal("number")
			expect(pm.response.responseSize > 0).to.equal(true)
		})
		test("text and json read the body", function () {
			expect(pm.response.text()).to.equal('{"ok":true}')
			expect(pm.response.json().ok).to.equal(true)
		})
		test("headers.get works", function () {
			expect(pm.response.headers.get("Content-Type")).to.equal("application/json")
			expect(pm.response.headers.has("Content-Type")).to.equal(true)
		})
	`)

	for _, result := range testResultsFor(t, state, collectionID, itemID) {
		if !result.Passed {
			t.Errorf("%s: %s", result.Name, result.Message)
		}
	}
}

func TestPmResponseAssertionsPassWhenTrue(t *testing.T) {
	app, collectionID, itemID, closeServer := scriptProbeFixture(t)
	defer closeServer()

	state := sendWithScripts(t, app, collectionID, itemID, "", "", `
		test("status by code", function () { pm.response.to.have.status(200) })
		test("status by text", function () { pm.response.to.have.status("OK") })
		test("header present", function () { pm.response.to.have.header("Content-Type") })
		test("header value", function () { pm.response.to.have.header("Content-Type", "application/json") })
		test("body present", function () { pm.response.to.have.body() })
		test("body exact", function () { pm.response.to.have.body('{"ok":true}') })
		test("json body", function () { pm.response.to.have.jsonBody() })
		test("to.be.ok", function () { pm.response.to.be.ok })
		test("to.be.success", function () { pm.response.to.be.success })
	`)

	for _, result := range testResultsFor(t, state, collectionID, itemID) {
		if !result.Passed {
			t.Errorf("%s: %s", result.Name, result.Message)
		}
	}
}

// TestPmResponseAssertionsFailWhenFalse is the half that matters. Each of these
// MUST produce a failed TestResult; an assertion that quietly returned false
// would make every one of them pass.
func TestPmResponseAssertionsFailWhenFalse(t *testing.T) {
	app, collectionID, itemID, closeServer := scriptProbeFixture(t)
	defer closeServer()

	state := sendWithScripts(t, app, collectionID, itemID, "", "", `
		test("wrong status code", function () { pm.response.to.have.status(500) })
		test("wrong status text", function () { pm.response.to.have.status("Not Found") })
		test("missing header", function () { pm.response.to.have.header("X-Nope") })
		test("wrong header value", function () { pm.response.to.have.header("Content-Type", "text/plain") })
		test("wrong body", function () { pm.response.to.have.body("something else") })
		test("missing json path", function () { pm.response.to.have.jsonBody("nope") })
		test("to.be.clientError on a 200", function () { pm.response.to.be.clientError })
		test("to.be.error on a 200", function () { pm.response.to.be.error })
	`)

	results := testResultsFor(t, state, collectionID, itemID)
	if len(results) != 8 {
		t.Fatalf("got %d results, want 8", len(results))
	}
	for _, result := range results {
		if result.Passed {
			t.Errorf("%q passed but its assertion is false — the assertion did not throw, so a broken API would report a green suite", result.Name)
		}
	}
}

// TestPmResponseIsAbsentDuringThePreRequestPhase. Exposing the zero Response
// would answer pm.response.code with 0 and text() with "", which reads as a
// server that returned nothing rather than as a script asking for something
// that does not exist yet.
func TestPmResponseIsAbsentDuringThePreRequestPhase(t *testing.T) {
	app, collectionID, itemID, closeServer := scriptProbeFixture(t)
	defer closeServer()

	state := sendWithScripts(t, app, collectionID, itemID,
		`bru.setVar("preResponseType", typeof pm.response)`,
		"",
		`
		test("pm.response was undefined during the pre-request script", function () {
			expect(bru.getVar("preResponseType")).to.equal("undefined")
		})
		test("pm.request was available during the pre-request script", function () {
			expect(typeof pm.request).to.equal("object")
		})
		test("pm.response is present in the tests phase", function () {
			expect(typeof pm.response).to.equal("object")
		})
	`)

	for _, result := range testResultsFor(t, state, collectionID, itemID) {
		if !result.Passed {
			t.Errorf("%s: %s", result.Name, result.Message)
		}
	}
}

// US-042 — pm's side effects.
//
// All three delegate to bru, and the tests are built to fail if any of them
// were reimplemented instead. Each has machinery that is easy to overlook:
// bru.sendRequest records a timeline entry and enforces the recursion depth
// limit, bru.cookies is bound to THIS request's jar and URL, and
// bru.setNextRequest feeds the runner's control flow. A parallel implementation
// would produce requests missing from the timeline, cookies from the wrong
// host, and a setNextRequest the runner never sees — none of which fails
// visibly.

func TestPmSideEffectsAreTheSameObjectsAsBru(t *testing.T) {
	app, collectionID, itemID, closeServer := scriptProbeFixture(t)
	defer closeServer()

	state := sendWithScripts(t, app, collectionID, itemID, "", "", `
		test("pm.sendRequest IS bru.sendRequest", function () {
			expect(pm.sendRequest === bru.sendRequest).to.equal(true)
		})
		test("pm.cookies IS bru.cookies", function () {
			expect(pm.cookies === bru.cookies).to.equal(true)
		})
		test("pm.execution.setNextRequest IS bru.setNextRequest", function () {
			expect(pm.execution.setNextRequest === bru.setNextRequest).to.equal(true)
		})
		test("pm.execution.skipRequest IS bru.runner.skipRequest", function () {
			expect(pm.execution.skipRequest === bru.runner.skipRequest).to.equal(true)
		})
	`)

	for _, result := range testResultsFor(t, state, collectionID, itemID) {
		if !result.Passed {
			t.Errorf("%s: %s", result.Name, result.Message)
		}
	}
}

// TestPmSendRequestPerformsARequest. Identity alone would be satisfied by two
// equally broken references, so this actually sends one.
func TestPmSendRequestPerformsARequest(t *testing.T) {
	var hits int64
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hits, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"from":"pm.sendRequest"}`)
	}))
	defer target.Close()

	app, collectionID, itemID, closeServer := scriptProbeFixture(t)
	defer closeServer()

	tests := fmt.Sprintf(`
		let seen = null
		let failure = null
		pm.sendRequest(%q, function (err, response) {
			if (err) { failure = String(err); return }
			seen = response
		})
		test("pm.sendRequest called back with a response", function () {
			expect(failure).to.equal(null)
			expect(seen === null).to.equal(false)
		})
		test("the response carries the target's body", function () {
			expect(JSON.stringify(seen.body || seen.data)).to.contain("pm.sendRequest")
		})
	`, target.URL)

	state := sendWithScripts(t, app, collectionID, itemID, "", "", tests)
	for _, result := range testResultsFor(t, state, collectionID, itemID) {
		if !result.Passed {
			t.Errorf("%s: %s", result.Name, result.Message)
		}
	}
	if got := atomic.LoadInt64(&hits); got != 1 {
		t.Errorf("the target server saw %d requests, want 1", got)
	}
}

// TestPmExecutionSetNextRequestDrivesTheRunner is the assertion that a
// reimplementation would fail: the runner has to actually observe the jump.
func TestPmExecutionSetNextRequestDrivesTheRunner(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `{"ok":true}`)
	}))
	defer server.Close()

	app, collectionID, ids := iterationFixture(t, server.URL, 3)

	// The first request jumps straight to the third, so the second must never
	// appear in the results.
	jump := `pm.execution.setNextRequest("iteration probe 2")`
	if _, err := app.UpdateRequest(collectionID, ids[0], RequestPatch{Tests: &jump}); err != nil {
		t.Fatalf("UpdateRequest: %v", err)
	}

	state, err := app.RunCollectionWithOptions(collectionID, "", RunnerOptions{SelectedItemIDs: ids})
	if err != nil {
		t.Fatalf("RunCollectionWithOptions: %v", err)
	}

	byName := resultsByName(state)
	if _, ran := byName["iteration probe 0"]; !ran {
		t.Error("the first request did not run")
	}
	if _, ran := byName["iteration probe 2"]; !ran {
		t.Error("the jump target did not run — the runner never saw pm.execution.setNextRequest")
	}
	if _, ran := byName["iteration probe 1"]; ran {
		t.Error("the skipped request ran anyway — the jump had no effect on the runner")
	}
}

// TestPmCookiesReadTheSameJarAsBru. A parallel cookie implementation bound to
// the wrong URL would return an empty list and every cookie assertion would
// quietly pass as "not present".
func TestPmCookiesReadTheSameJarAsBru(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "session", Value: "abc123", Path: "/"})
		_, _ = fmt.Fprint(w, `{"ok":true}`)
	}))
	defer server.Close()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	collectionID := state.Workspaces[0].Collections[0].ID
	created, err := app.CreateRequest(collectionID, "http", "cookie probe")
	if err != nil {
		t.Fatalf("CreateRequest: %v", err)
	}
	var itemID string
	for _, c := range created.Workspaces[0].Collections {
		for _, item := range c.Items {
			if item.Name == "cookie probe" {
				itemID = item.ID
			}
		}
	}
	url := server.URL
	if _, err := app.UpdateRequest(collectionID, itemID, RequestPatch{URL: &url}); err != nil {
		t.Fatalf("UpdateRequest: %v", err)
	}

	// First send populates the jar; the second reads it from both surfaces.
	sendWithScripts(t, app, collectionID, itemID, "", "", "")
	final := sendWithScripts(t, app, collectionID, itemID, "", "", `
		test("pm.cookies sees the jar bru sees", function () {
			expect(pm.cookies.has("session")).to.equal(bru.cookies.has("session"))
		})
		test("the cookie set by the server is visible", function () {
			expect(pm.cookies.has("session")).to.equal(true)
			expect(String(pm.cookies.get("session"))).to.contain("abc123")
		})
	`)

	for _, result := range testResultsFor(t, final, collectionID, itemID) {
		if !result.Passed {
			t.Errorf("%s: %s", result.Name, result.Message)
		}
	}
}

// US-043 — pm.iterationData and pm.vault.

// TestPmIterationDataReadsOnlyTheDataFile is the distinction that matters.
// pm.iterationData means "what the data file said for THIS iteration". Reading
// the merged chain instead would report environment and collection variables
// as iteration data, so a script guarding on
// pm.iterationData.has("userId") would take the data-driven branch on a run
// with no data file at all.
func TestPmIterationDataReadsOnlyTheDataFile(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `{"ok":true}`)
	}))
	defer server.Close()

	dataPath := writeDataFile(t, "rows.csv", "userId,label\n11,alpha\n22,beta\n")
	app, collectionID, ids := iterationFixture(t, server.URL, 1)

	tests := `
		pm.environment.set("notFromData", "environment")
		test("iteration data carries the row's columns", function () {
			expect(pm.iterationData.get("userId")).to.equal("22")
			expect(pm.iterationData.get("label")).to.equal("beta")
			expect(pm.iterationData.has("userId")).to.equal(true)
		})
		test("iteration data does NOT include other scopes", function () {
			expect(pm.iterationData.has("notFromData")).to.equal(false)
			expect(typeof pm.iterationData.get("notFromData")).to.equal("undefined")
		})
		test("toObject returns just the row", function () {
			var keys = Object.keys(pm.iterationData.toObject()).sort()
			expect(keys.join(",")).to.equal("label,userId")
		})
	`
	if _, err := app.UpdateRequest(collectionID, ids[0], RequestPatch{Tests: &tests}); err != nil {
		t.Fatalf("UpdateRequest: %v", err)
	}

	state, err := app.RunCollectionWithOptions(collectionID, "", RunnerOptions{
		SelectedItemIDs: ids,
		DataFile:        dataPath,
	})
	if err != nil {
		t.Fatalf("RunCollectionWithOptions: %v", err)
	}

	results := testResultsFor(t, state, collectionID, ids[0])
	if len(results) != 3 {
		t.Fatalf("got %d results, want 3 — the tests script did not run", len(results))
	}
	for _, result := range results {
		if !result.Passed {
			t.Errorf("%s: %s", result.Name, result.Message)
		}
	}
}

// TestPmIterationDataIsEmptyWithoutADataFile. The guard scripts actually write
// is `if (pm.iterationData.has(...))`, so a run with no data file must answer
// false rather than falling through to another scope.
func TestPmIterationDataIsEmptyWithoutADataFile(t *testing.T) {
	app, collectionID, itemID, closeServer := scriptProbeFixture(t)
	defer closeServer()

	state := sendWithScripts(t, app, collectionID, itemID, "", "", `
		pm.environment.set("anything", "1")
		test("no data file means no iteration data", function () {
			expect(pm.iterationData.has("anything")).to.equal(false)
			expect(Object.keys(pm.iterationData.toObject()).length).to.equal(0)
		})
	`)

	for _, result := range testResultsFor(t, state, collectionID, itemID) {
		if !result.Passed {
			t.Errorf("%s: %s", result.Name, result.Message)
		}
	}
}

// TestPmIterationDataToObjectIsACopy. toObject is a read in Postman; handing
// out the live scope would let a script mutate the iteration's variables
// through it.
func TestPmIterationDataToObjectIsACopy(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `{"ok":true}`)
	}))
	defer server.Close()

	dataPath := writeDataFile(t, "rows.csv", "userId\n7\n")
	app, collectionID, ids := iterationFixture(t, server.URL, 1)

	tests := `
		var snapshot = pm.iterationData.toObject()
		snapshot.userId = "tampered"
		test("mutating the snapshot does not change the iteration data", function () {
			expect(pm.iterationData.get("userId")).to.equal("7")
		})
	`
	if _, err := app.UpdateRequest(collectionID, ids[0], RequestPatch{Tests: &tests}); err != nil {
		t.Fatalf("UpdateRequest: %v", err)
	}
	state, err := app.RunCollectionWithOptions(collectionID, "", RunnerOptions{
		SelectedItemIDs: ids,
		DataFile:        dataPath,
	})
	if err != nil {
		t.Fatalf("RunCollectionWithOptions: %v", err)
	}
	for _, result := range testResultsFor(t, state, collectionID, ids[0]) {
		if !result.Passed {
			t.Errorf("%s: %s", result.Name, result.Message)
		}
	}
}

// TestPmVaultIsAsync. Postman's vault API returns promises and scripts are
// written as `await pm.vault.get(...)`. A bare value would satisfy await by
// accident and then break on .then().
func TestPmVaultIsAsync(t *testing.T) {
	app, collectionID, itemID, closeServer := scriptProbeFixture(t)
	defer closeServer()

	state := sendWithScripts(t, app, collectionID, itemID, "", "", `
		bru.setEnvVar("vaultedToken", "s3cret")
		test("get returns a thenable", function () {
			var result = pm.vault.get("vaultedToken")
			expect(typeof result.then).to.equal("function")
		})
		test("has returns a thenable", function () {
			expect(typeof pm.vault.has("vaultedToken").then).to.equal("function")
		})
	`)

	for _, result := range testResultsFor(t, state, collectionID, itemID) {
		if !result.Passed {
			t.Errorf("%s: %s", result.Name, result.Message)
		}
	}
}

// TestPmVaultReadsTheSecretsLayer exercises the await path end to end.
func TestPmVaultReadsTheSecretsLayer(t *testing.T) {
	app, collectionID, itemID, closeServer := scriptProbeFixture(t)
	defer closeServer()

	state := sendWithScripts(t, app, collectionID, itemID, "", "", `
		bru.setEnvVar("vaultedToken", "s3cret")
		test("await reads the value", async function () {
			var value = await pm.vault.get("vaultedToken")
			expect(String(value)).to.equal("s3cret")
		})
		test("has reports presence", async function () {
			expect(await pm.vault.has("vaultedToken")).to.equal(true)
			expect(await pm.vault.has("neverSet")).to.equal(false)
		})
	`)

	results := testResultsFor(t, state, collectionID, itemID)
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2 — the async assertions did not resolve", len(results))
	}
	for _, result := range results {
		if !result.Passed {
			t.Errorf("%s: %s", result.Name, result.Message)
		}
	}
}

// TestPmVaultWritesAreRejected. The runtime cannot mark a value as secret —
// scripting.VariableContext holds plain maps with no Secret flag — so a
// pm.vault.set would land the value in the environment as an ordinary variable
// and get written to disk in the clear. A script storing a token would believe
// it was vaulted while leaking it. The rejection has to be explicit.
func TestPmVaultWritesAreRejected(t *testing.T) {
	app, collectionID, itemID, closeServer := scriptProbeFixture(t)
	defer closeServer()

	state := sendWithScripts(t, app, collectionID, itemID, "", "", `
		test("set rejects rather than storing in the clear", async function () {
			var rejected = false
			var message = ""
			try {
				await pm.vault.set("leaked", "token")
			} catch (e) {
				rejected = true
				message = String(e)
			}
			expect(rejected).to.equal(true)
			expect(message.indexOf("plain text") >= 0).to.equal(true)
		})
		test("the value was not written to any scope", function () {
			expect(bru.hasEnvVar("leaked")).to.equal(false)
			expect(bru.hasVar("leaked")).to.equal(false)
		})
		test("unset rejects too", async function () {
			var rejected = false
			try {
				await pm.vault.unset("leaked")
			} catch (e) {
				rejected = true
			}
			expect(rejected).to.equal(true)
		})
	`)

	results := testResultsFor(t, state, collectionID, itemID)
	if len(results) != 3 {
		t.Fatalf("got %d results, want 3", len(results))
	}
	for _, result := range results {
		if !result.Passed {
			t.Errorf("%s: %s", result.Name, result.Message)
		}
	}
}

// US-044 — fix and demote the import translator.

const postmanScopeCollection = `{
  "info": {"name": "scope translator", "schema": "https://schema.getpostman.com/json/collection/v2.1.0/collection.json"},
  "item": [{
    "name": "scoped",
    "event": [{
      "listen": "prerequest",
      "script": {"exec": [
        "pm.environment.set('e', '1');",
        "pm.collectionVariables.set('c', '2');",
        "pm.globals.set('g', '3');",
        "pm.variables.get('anything');",
        "pm.test('x', function () {});"
      ]}
    }],
    "request": {"method": "GET", "url": "https://example.test/"}
  }]
}`

func importedScopeScript(t *testing.T, translate bool) string {
	t.Helper()
	collection, err := importers.ImportPostman(postmanScopeCollection, "scope translator", translate)
	if err != nil {
		t.Fatalf("importPostman: %v", err)
	}
	if len(collection.Items) != 1 {
		t.Fatalf("got %d items, want 1", len(collection.Items))
	}
	return collection.Items[0].PreScript
}

// TestPostmanImportKeepsPmByDefault is the demotion. Until US-039-043 there was
// no live pm object, so rewriting was the only way an imported script could run
// at all. Now pm.* is native and strictly more faithful than a textual rewrite,
// so the default must leave the script alone.
func TestPostmanImportKeepsPmByDefault(t *testing.T) {
	script := importedScopeScript(t, false)
	for _, expected := range []string{
		"pm.environment.set", "pm.collectionVariables.set",
		"pm.globals.set", "pm.variables.get", "pm.test",
	} {
		if !strings.Contains(script, expected) {
			t.Errorf("default import rewrote %q away; it should run natively:\n%s", expected, script)
		}
	}
	if strings.Contains(script, "bru.") {
		t.Errorf("default import introduced bru.* calls:\n%s", script)
	}
}

// TestPostmanTranslatorNoLongerCollapsesScopes is the fix. Every one of these
// mapped to bru.setVar before, collapsing four distinct scopes into the runtime
// scope — and it never errored, so a script that wrote one scope and read
// another got its value back and the collapse looked like it worked.
func TestPostmanTranslatorNoLongerCollapsesScopes(t *testing.T) {
	script := importedScopeScript(t, true)

	for _, want := range []string{
		"bru.setEnvVar('e'",
		"bru.setCollectionVar('c'",
		"bru.setGlobalEnvVar('g'",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("expected %q in the translated script:\n%s", want, script)
		}
	}

	// The collapse: if any scope still routed to the generic runtime setter,
	// this would appear.
	if strings.Contains(script, "bru.setVar(") {
		t.Errorf("a scope still collapses onto the runtime scope:\n%s", script)
	}

	// pm.variables has no exact bru equivalent — bru.getVar reads only the
	// runtime scope, which was half the collapse — so it stays native.
	if !strings.Contains(script, "pm.variables.get") {
		t.Errorf("pm.variables was translated; it has no exact bru equivalent and must stay native:\n%s", script)
	}

	// The parts that DO map exactly still convert.
	if !strings.Contains(script, "test(") || strings.Contains(script, "pm.test") {
		t.Errorf("pm.test was not translated:\n%s", script)
	}
}

// TestTranslatedScopesReachDistinctStorage proves the fix at runtime, not just
// textually: a rewrite that produced the right strings against the wrong
// functions would satisfy the test above.
func TestTranslatedScopesReachDistinctStorage(t *testing.T) {
	app, collectionID, itemID, closeServer := scriptProbeFixture(t)
	defer closeServer()

	translated := importers.TranslateScript(
		"pm.environment.set('scoped', 'environment');\n" +
			"pm.collectionVariables.set('scoped', 'collection');\n" +
			"pm.globals.set('scoped', 'global');\n")

	state := sendWithScripts(t, app, collectionID, itemID, "", "", translated+`
		test("the translated writes landed in three distinct scopes", function () {
			expect(bru.getEnvVar("scoped")).to.equal("environment")
			expect(bru.getCollectionVar("scoped")).to.equal("collection")
			expect(bru.getGlobalEnvVar("scoped")).to.equal("global")
		})
		test("nothing landed in the runtime scope", function () {
			expect(bru.hasVar("scoped")).to.equal(false)
		})
	`)

	for _, result := range testResultsFor(t, state, collectionID, itemID) {
		if !result.Passed {
			t.Errorf("%s: %s", result.Name, result.Message)
		}
	}
}

// TestUntranslatedPostmanScriptsRun is the claim the demotion rests on: an
// imported script left verbatim must actually work.
func TestUntranslatedPostmanScriptsRun(t *testing.T) {
	app, collectionID, itemID, closeServer := scriptProbeFixture(t)
	defer closeServer()

	state := sendWithScripts(t, app, collectionID, itemID, "", "", `
		pm.environment.set("fromPm", "yes")
		pm.test("an untranslated Postman script runs as written", function () {
			pm.expect(pm.response.code).to.equal(200)
			pm.response.to.have.status(200)
			pm.expect(pm.environment.get("fromPm")).to.equal("yes")
		})
	`)

	results := testResultsFor(t, state, collectionID, itemID)
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1 — the verbatim Postman script did not run", len(results))
	}
	if !results[0].Passed {
		t.Errorf("%s: %s", results[0].Name, results[0].Message)
	}
}
