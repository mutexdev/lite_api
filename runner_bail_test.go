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
