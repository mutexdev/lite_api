package core

// US-045 — runner iterations.
//
// "RunnerOptions gains Iterations; per-iteration rows in RunnerSnapshot" is the
// whole criterion, but the part that actually breaks is the INTERACTION with
// the controls that were already there. Adding an outer loop turns every
// existing `break` into a question — does it end the run, or only this pass? —
// and getting one wrong is silent: a cancelled run that quietly starts
// iteration 3 looks like a run that took a while.

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestNormalizeRunnerIterations(t *testing.T) {
	for _, tc := range []struct{ in, want int }{
		{0, 1}, {-5, 1}, {1, 1}, {7, 7},
		{runnerIterationLimit, runnerIterationLimit},
		{runnerIterationLimit + 1, runnerIterationLimit},
		{1 << 30, runnerIterationLimit},
	} {
		if got := normalizeRunnerIterations(tc.in); got != tc.want {
			t.Errorf("normalizeRunnerIterations(%d) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

// TestRunnerIterationsRepeatEveryRequest is the base case: N iterations send
// each request N times and produce N rows per request.
func TestRunnerIterationsRepeatEveryRequest(t *testing.T) {
	var hits int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hits, 1)
		_, _ = fmt.Fprint(w, `{"ok":true}`)
	}))
	defer server.Close()

	app, collectionID, ids := iterationFixture(t, server.URL, 2)

	state, err := app.RunCollectionWithOptions(collectionID, "", RunnerOptions{
		SelectedItemIDs: ids,
		Iterations:      3,
	})
	if err != nil {
		t.Fatalf("RunCollectionWithOptions: %v", err)
	}

	if got := atomic.LoadInt64(&hits); got != 6 {
		t.Errorf("server saw %d requests, want 6 (2 requests x 3 iterations)", got)
	}
	if len(state.Runner.Results) != 6 {
		t.Fatalf("got %d result rows, want 6", len(state.Runner.Results))
	}
	if state.Runner.Passed != 6 {
		t.Errorf("Passed = %d, want 6", state.Runner.Passed)
	}
	if state.Runner.Iterations != 3 || state.Runner.CompletedIterations != 3 {
		t.Errorf("iterations = %d/%d, want 3/3", state.Runner.CompletedIterations, state.Runner.Iterations)
	}

	// Per-iteration rows: each row must say which pass it belongs to, and the
	// counts per iteration must be equal. Without the stamp the six rows are
	// indistinguishable and "per-iteration rows" is not satisfied.
	perIteration := map[int]int{}
	for _, result := range state.Runner.Results {
		if result.Iteration == 0 {
			t.Fatalf("result %q carries no iteration number", result.Name)
		}
		perIteration[result.Iteration]++
	}
	for iteration := 1; iteration <= 3; iteration++ {
		if perIteration[iteration] != 2 {
			t.Errorf("iteration %d has %d rows, want 2", iteration, perIteration[iteration])
		}
	}
}

// TestSingleIterationRunsAreShapeCompatible. Iteration is omitempty precisely so
// an existing consumer sees no change; a default run that started stamping 1
// would alter every persisted snapshot for no reason.
func TestSingleIterationRunsAreShapeCompatible(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `{"ok":true}`)
	}))
	defer server.Close()

	app, collectionID, ids := iterationFixture(t, server.URL, 2)

	for _, iterations := range []int{0, 1} {
		state, err := app.RunCollectionWithOptions(collectionID, "", RunnerOptions{
			SelectedItemIDs: ids,
			Iterations:      iterations,
		})
		if err != nil {
			t.Fatalf("Iterations=%d: %v", iterations, err)
		}
		if len(state.Runner.Results) != 2 {
			t.Errorf("Iterations=%d produced %d rows, want 2", iterations, len(state.Runner.Results))
		}
		for _, result := range state.Runner.Results {
			if result.Iteration != 0 {
				t.Errorf("Iterations=%d stamped a row with iteration %d; single-iteration runs must stay unstamped", iterations, result.Iteration)
			}
		}
		if state.Runner.Iterations != 0 || state.Runner.CompletedIterations != 0 {
			t.Errorf("Iterations=%d set snapshot iteration fields (%d/%d)", iterations, state.Runner.CompletedIterations, state.Runner.Iterations)
		}
	}
}

// TestRunnerBailStopsEveryIteration is the interaction that a naive outer loop
// gets wrong, and gets wrong silently: the bail breaks the inner loop, the
// outer loop starts iteration 2, and the run the user asked to stop keeps
// firing requests at a failing endpoint.
func TestRunnerBailStopsEveryIteration(t *testing.T) {
	var hits int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hits, 1)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = fmt.Fprint(w, `{"error":"deliberate"}`)
	}))
	defer server.Close()

	app, collectionID, ids := iterationFixture(t, server.URL, 2)

	state, err := app.RunCollectionWithOptions(collectionID, "", RunnerOptions{
		SelectedItemIDs: ids,
		Iterations:      4,
		BailOnFailure:   true,
	})
	if err != nil {
		t.Fatalf("RunCollectionWithOptions: %v", err)
	}

	if got := atomic.LoadInt64(&hits); got != 1 {
		t.Errorf("server saw %d requests after a bail on the first one, want 1 — the bail did not stop later iterations", got)
	}
	if state.Runner.Iterations != 4 {
		t.Errorf("Iterations = %d, want the 4 that were requested", state.Runner.Iterations)
	}
	if state.Runner.CompletedIterations != 0 {
		t.Errorf("CompletedIterations = %d, want 0 — iteration 1 did not finish", state.Runner.CompletedIterations)
	}

	// Rows: the failure plus the unrun remainder of iteration 1, all stamped
	// with iteration 1. Nothing from iterations 2-4.
	if len(state.Runner.Results) != 2 {
		t.Fatalf("got %d rows, want 2 (the failure and the unrun remainder of iteration 1)", len(state.Runner.Results))
	}
	for _, result := range state.Runner.Results {
		if result.Iteration != 1 {
			t.Errorf("row %q is stamped iteration %d; nothing past iteration 1 should exist", result.Name, result.Iteration)
		}
	}
}

// TestUnrunResultsAreNotCountedAsFailures. The snapshot's tally had a default
// arm that swept every unknown status into Failed, so US-047's new "unrun"
// status inflated the failure count: a bailed run of N requests reported N-1
// failures for one real failure. Status assertions alone never saw it.
func TestUnrunResultsAreNotCountedAsFailures(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = fmt.Fprint(w, `{"error":"deliberate"}`)
	}))
	defer server.Close()

	app, collectionID, ids := iterationFixture(t, server.URL, 5)

	state, err := app.RunCollectionWithOptions(collectionID, "", RunnerOptions{
		SelectedItemIDs: ids,
		BailOnFailure:   true,
	})
	if err != nil {
		t.Fatalf("RunCollectionWithOptions: %v", err)
	}

	unrun := 0
	for _, result := range state.Runner.Results {
		if result.Status == "unrun" {
			unrun++
		}
	}
	if unrun != 4 {
		t.Fatalf("got %d unrun rows, want 4", unrun)
	}
	if state.Runner.Failed != 1 {
		t.Errorf("Failed = %d, want 1 — the 4 unrun requests must not be counted as failures", state.Runner.Failed)
	}
	if state.Runner.Passed != 0 || state.Runner.Skipped != 0 || state.Runner.Cancelled != 0 {
		t.Errorf("unrun leaked into another tally: passed=%d skipped=%d cancelled=%d",
			state.Runner.Passed, state.Runner.Skipped, state.Runner.Cancelled)
	}
}

// iterationFixture builds a collection of n identical requests against url.
func iterationFixture(t *testing.T, url string, n int) (*App, string, []string) {
	t.Helper()
	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	collectionID := state.Workspaces[0].Collections[0].ID

	var ids []string
	for i := range n {
		name := fmt.Sprintf("iteration probe %d", i)
		created, err := app.CreateRequest(collectionID, "http", name)
		if err != nil {
			t.Fatalf("CreateRequest: %v", err)
		}
		var id string
		for _, c := range created.Workspaces[0].Collections {
			for _, item := range c.Items {
				if item.Name == name {
					id = item.ID
				}
			}
		}
		if id == "" {
			t.Fatalf("could not find %s", name)
		}
		target := url
		if _, err := app.UpdateRequest(collectionID, id, RequestPatch{URL: &target}); err != nil {
			t.Fatalf("UpdateRequest: %v", err)
		}
		ids = append(ids, id)
	}
	return app, collectionID, ids
}
