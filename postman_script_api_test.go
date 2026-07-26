package main

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
	"testing"
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
		if got := postmanEventName(phase); got != want {
			t.Errorf("postmanEventName(%q) = %q, want %q", phase, got, want)
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
