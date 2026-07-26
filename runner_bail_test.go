package main

// US-047 — bail on failure.
//
// The criterion that carries the weight is "unrun requests are marked as such
// in the result rows, distinct from skipped and cancelled". Those three states
// mean genuinely different things to whoever reads the results:
//
//   skipped   the runner decided not to run it — wrong protocol, unresolved
//             prompt variables
//   cancelled the user stopped the run
//   unrun     an earlier failure ended the run before this request had a turn
//
// Collapsing them would leave someone unable to tell a failing suite from an
// abandoned one, which is exactly the question you ask when a run comes back
// red.

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type bailSpec struct{ name, protocol, path string }

// bailFixture builds a collection from specs, in collection order.
//
// Order matters and is not the caller's selection order: the runner walks the
// collection's own item order, so a fixture that relies on the order it passed
// to SelectedItemIDs is testing something else.
func bailFixture(t *testing.T, specs ...bailSpec) (app *App, collectionID string, ids []string, closeServer func()) {
	t.Helper()
	if len(specs) == 0 {
		specs = []bailSpec{
			{"bail first", "http", "/ok"},
			{"bail second", "http", "/fail"},
			{"bail third", "http", "/ok"},
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/fail" {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = fmt.Fprint(w, `{"error":"deliberate"}`)
			return
		}
		_, _ = fmt.Fprint(w, `{"ok":true}`)
	}))

	app = newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	collection := state.Workspaces[0].Collections[0]
	collectionID = collection.ID

	for _, spec := range specs {
		created, err := app.CreateRequest(collectionID, spec.protocol, spec.name)
		if err != nil {
			t.Fatalf("CreateRequest %s: %v", spec.name, err)
		}
		var id string
		for _, c := range created.Workspaces[0].Collections {
			for _, item := range c.Items {
				if item.Name == spec.name {
					id = item.ID
				}
			}
		}
		if id == "" {
			t.Fatalf("could not find %s", spec.name)
		}
		if spec.path != "" {
			url := server.URL + spec.path
			if _, err := app.UpdateRequest(collectionID, id, RequestPatch{URL: &url}); err != nil {
				t.Fatalf("UpdateRequest %s: %v", spec.name, err)
			}
		}
		ids = append(ids, id)
	}
	return app, collectionID, ids, server.Close
}

func resultsByName(state AppState) map[string]RunResult {
	out := map[string]RunResult{}
	for _, r := range state.Runner.Results {
		out[r.Name] = r
	}
	return out
}

// TestRunnerContinuesPastFailureByDefault. Bail is opt-in; the existing
// behaviour must be untouched when it is off, or this story would silently
// change every run.
func TestRunnerContinuesPastFailureByDefault(t *testing.T) {
	app, collectionID, ids, closeServer := bailFixture(t)
	defer closeServer()

	state, err := app.RunCollectionWithOptions(collectionID, "", RunnerOptions{SelectedItemIDs: ids})
	if err != nil {
		t.Fatalf("RunCollectionWithOptions: %v", err)
	}
	byName := resultsByName(state)
	if got := byName["bail second"].Status; got != "failed" {
		t.Errorf("second request status = %q, want failed", got)
	}
	third, ran := byName["bail third"]
	if !ran {
		t.Fatal("the third request did not run — bail must be off by default")
	}
	if third.Status != "passed" {
		t.Errorf("third request status = %q, want passed", third.Status)
	}
}

// TestRunnerBailsAndMarksRemainingUnrun is the story.
func TestRunnerBailsAndMarksRemainingUnrun(t *testing.T) {
	app, collectionID, ids, closeServer := bailFixture(t)
	defer closeServer()

	state, err := app.RunCollectionWithOptions(collectionID, "", RunnerOptions{
		SelectedItemIDs: ids,
		BailOnFailure:   true,
	})
	if err != nil {
		t.Fatalf("RunCollectionWithOptions: %v", err)
	}
	byName := resultsByName(state)

	if got := byName["bail first"].Status; got != "passed" {
		t.Errorf("first request status = %q, want passed", got)
	}
	if got := byName["bail second"].Status; got != "failed" {
		t.Errorf("second request status = %q, want failed", got)
	}

	third, present := byName["bail third"]
	if !present {
		t.Fatal("the request after the failure has no result row at all — it must be reported as unrun, not omitted")
	}
	if third.Status != "unrun" {
		t.Errorf("third request status = %q, want unrun (distinct from skipped and cancelled)", third.Status)
	}
	if third.Error == "" {
		t.Error("an unrun row should say why it did not run")
	}

	// Every selected request must be accounted for. A run that silently drops
	// rows is worse than one that reports them, because the reader cannot tell
	// the suite shrank.
	if len(state.Runner.Results) != len(ids) {
		t.Errorf("got %d result rows for %d requests — every request must be accounted for",
			len(state.Runner.Results), len(ids))
	}
}

// TestRunnerBailDistinguishesUnrunFromSkipped. Both mean "did not execute", and
// an implementation that reused "skipped" would pass every other assertion here
// while destroying the distinction the story asks for.
func TestRunnerBailDistinguishesUnrunFromSkipped(t *testing.T) {
	// Collection order: the websocket must come BEFORE the failure, or it would
	// be unrun for the ordinary reason and prove nothing.
	app, collectionID, ids, closeServer := bailFixture(t,
		bailSpec{"bail websocket", "websocket", ""},
		bailSpec{"bail second", "http", "/fail"},
		bailSpec{"bail third", "http", "/ok"},
	)
	defer closeServer()

	state, err := app.RunCollectionWithOptions(collectionID, "", RunnerOptions{
		SelectedItemIDs: ids,
		BailOnFailure:   true,
	})
	if err != nil {
		t.Fatalf("RunCollectionWithOptions: %v", err)
	}
	byName := resultsByName(state)

	if got := byName["bail websocket"].Status; got != "skipped" {
		t.Errorf("websocket status = %q, want skipped", got)
	}
	if got := byName["bail third"].Status; got != "unrun" {
		t.Errorf("post-failure status = %q, want unrun", got)
	}
	if byName["bail websocket"].Status == byName["bail third"].Status {
		t.Error("skipped and unrun collapsed into one status — the distinction the story requires is gone")
	}
}

// TestFailedAssertionsFailTheRunResult closes a gap US-047 shipped with.
//
// Its criterion names "a failed assertion or transport error", but the runner
// only ever derived status from the transport and the HTTP code. A collection
// whose every assertion failed against 200 responses reported a fully green
// run — and BailOnFailure could never trigger on an assertion, which is the
// case the story leads with.
func TestFailedAssertionsFailTheRunResult(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `{"ok":true}`)
	}))
	defer server.Close()

	app, collectionID, ids := iterationFixture(t, server.URL, 3)

	// The middle request asserts something false against a 200 response.
	failing := `test("deliberately false", function () { expect(1).to.equal(2) })`
	if _, err := app.UpdateRequest(collectionID, ids[1], RequestPatch{Tests: &failing}); err != nil {
		t.Fatalf("UpdateRequest: %v", err)
	}

	state, err := app.RunCollectionWithOptions(collectionID, "", RunnerOptions{SelectedItemIDs: ids})
	if err != nil {
		t.Fatalf("RunCollectionWithOptions: %v", err)
	}
	byName := resultsByName(state)
	middle := byName["iteration probe 1"]
	if middle.Status != "failed" {
		t.Errorf("a request whose assertion failed reported %q, want failed", middle.Status)
	}
	if middle.Code != 200 {
		t.Errorf("code = %d, want 200 — the response itself was fine", middle.Code)
	}
	if !strings.Contains(middle.Error, "deliberately false") {
		t.Errorf("error %q does not name the failing assertion", middle.Error)
	}
	if state.Runner.Failed != 1 || state.Runner.Passed != 2 {
		t.Errorf("tally was passed=%d failed=%d, want 2/1", state.Runner.Passed, state.Runner.Failed)
	}

	// And now the case US-047's criterion actually leads with: bail on a
	// failed assertion.
	state, err = app.RunCollectionWithOptions(collectionID, "", RunnerOptions{
		SelectedItemIDs: ids,
		BailOnFailure:   true,
	})
	if err != nil {
		t.Fatalf("RunCollectionWithOptions: %v", err)
	}
	if got := resultsByName(state)["iteration probe 2"].Status; got != "unrun" {
		t.Errorf("the request after a failed assertion reported %q, want unrun — bail did not trigger on an assertion", got)
	}
}

func TestFirstFailedTestResult(t *testing.T) {
	if got := firstFailedTestResult(nil); got != "" {
		t.Errorf("no results = %q, want empty", got)
	}
	if got := firstFailedTestResult([]TestResult{{Name: "a", Passed: true}}); got != "" {
		t.Errorf("all passing = %q, want empty", got)
	}
	// The FIRST failure, not the last: a run result has one error line, and the
	// assertion that broke first is what someone acts on.
	got := firstFailedTestResult([]TestResult{
		{Name: "ok", Passed: true},
		{Name: "first bad", Passed: false, Message: "boom"},
		{Name: "second bad", Passed: false, Message: "later"},
	})
	if !strings.Contains(got, "first bad") || strings.Contains(got, "second bad") {
		t.Errorf("got %q, want the first failure only", got)
	}
	if !strings.Contains(got, "boom") {
		t.Errorf("got %q, want the message included", got)
	}
}
