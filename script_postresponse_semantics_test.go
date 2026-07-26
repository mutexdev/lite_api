// US-019 — what the post-response phase's three runtimes can see of each other.
//
// The story wants those three collapsed into one, and the measurement in
// progress.txt says that is worth ~1.5 MB and ~17,000 allocations per request.
// The reason it has not been done is that the three runtimes are built from
// different variable maps, so sharing one CHANGES what a post-response script
// and a tests script can see. That is a product decision about script
// compatibility, not a refactoring detail.
//
// These tests pin the CURRENT answers, so that decision stops being invisible.
// Today they document isolation; if someone consolidates the runtimes they will
// fail, and the diff will state exactly which visibility rules changed rather
// than letting user scripts discover it in the field.
//
// They are deliberately written to be readable as a specification: each name is
// the sentence the test asserts.
package main

import "testing"

func TestPostResponseGlobalIsNotVisibleToTests(t *testing.T) {
	app, collectionID, itemID, closeServer := scriptProbeFixture(t)
	defer closeServer()

	state := sendWithScripts(t, app, collectionID, itemID,
		"",
		`plantedInPostResponse = "from post script"`,
		`test("post-response global is not visible to tests", function () {
			expect(typeof plantedInPostResponse).to.equal("undefined")
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

// The counterpart that keeps the rule above from reading as "nothing crosses".
// bru.setVar is the SUPPORTED channel between the two, and it must keep working
// whatever happens to the runtimes — a consolidation that broke this while
// making the isolation test pass would be a regression disguised as a success.
func TestPostResponseSetVarIsVisibleToTests(t *testing.T) {
	app, collectionID, itemID, closeServer := scriptProbeFixture(t)
	defer closeServer()

	state := sendWithScripts(t, app, collectionID, itemID,
		"",
		`bru.setVar("handoff", "carried")`,
		`test("setVar from the post script reaches tests", function () {
			expect(bru.getVar("handoff")).to.equal("carried")
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

// Ordering. Today the three phases run in separate runtimes, so the order is
// unobservable to scripts except through bru vars. Sharing a runtime makes it
// observable, which is why the order is worth stating now rather than
// discovering later: response variables, then post-response script, then tests.
func TestPostResponsePhasesRunInAKnownOrder(t *testing.T) {
	app, collectionID, itemID, closeServer := scriptProbeFixture(t)
	defer closeServer()

	state := sendWithScripts(t, app, collectionID, itemID,
		"",
		`bru.setVar("order", (bru.getVar("order") || "") + "post;")`,
		`test("tests run after the post-response script", function () {
			expect(bru.getVar("order")).to.equal("post;")
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

// A tests-script global must not survive into the NEXT request's tests script.
// The existing suite pins this for pre-request globals; the tests runtime is the
// one a consolidation would merge, so it needs its own statement.
func TestTestsScriptGlobalDoesNotLeakIntoTheNextRequest(t *testing.T) {
	app, collectionID, itemID, closeServer := scriptProbeFixture(t)
	defer closeServer()

	sendWithScripts(t, app, collectionID, itemID, "", "",
		`plantedByTests = "first request"
		 test("plant", function () { expect(1).to.equal(1) })`)

	state := sendWithScripts(t, app, collectionID, itemID, "", "",
		`test("a tests global does not survive into the next request", function () {
			expect(typeof plantedByTests).to.equal("undefined")
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
