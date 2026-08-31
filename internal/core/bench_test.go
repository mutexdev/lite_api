package core

// US-005 — benchmark baseline.
//
// These are the numbers every Phase 1 performance claim is checked against. The
// repo contained zero `func Benchmark` before this file, so no earlier claim of
// "faster" in this codebase has ever been measured.
//
// Capture with:
//
//	go test -bench=. -benchmem -run=^$ -benchtime=10x . | tee .ralph/baseline/bench.txt
//
// -benchtime=10x rather than the default 1s: BenchmarkPersistLocked serialises
// tens of megabytes and writes them to disk per iteration, so the 1s default
// would run for minutes and thrash the disk without adding signal.
//
// Absolute numbers are only comparable on the same host — see the header in
// .ralph/baseline/bench.txt.

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mutexdev/lite_api/internal/scripting"
)

func benchFixtureOptions() largeWorkspaceOptions {
	return defaultLargeWorkspaceOptions()
}

// BenchmarkPersistLocked measures the keystroke path described in
// improvement_v2.md §2.1.B: MarshalIndent of the entire AppState followed by a
// synchronous, non-atomic os.WriteFile. Target of US-012 (async coalesced
// persistence) and US-009 (response body store).
func BenchmarkPersistLocked(b *testing.B) {
	app := newLargeWorkspaceAppForTest(b, b.TempDir(), benchFixtureOptions())

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		app.mu.Lock()
		err := app.persistLocked()
		app.mu.Unlock()
		if err != nil {
			b.Fatalf("persistLocked: %v", err)
		}
	}
}

// BenchmarkPersistLockedSmallState isolates fixed overhead (secrets and OAuth2
// credential storage) from payload cost. The delta against the benchmark above
// is the part that scales with cached response bodies.
func BenchmarkPersistLockedSmallState(b *testing.B) {
	app := newAppForTest(b)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		app.mu.Lock()
		err := app.persistLocked()
		app.mu.Unlock()
		if err != nil {
			b.Fatalf("persistLocked: %v", err)
		}
	}
}

// BenchmarkMarkDirty measures what the keystroke path actually costs after
// US-012: a mutator now calls markDirty instead of persistLocked. It is the
// number BenchmarkPersistLocked should be read against — persistLocked still
// exists and is still expensive, it just no longer runs per typed character.
func BenchmarkMarkDirty(b *testing.B) {
	app := newLargeWorkspaceAppForTest(b, b.TempDir(), benchFixtureOptions())

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		app.mu.Lock()
		err := app.markDirty(persistScopeState)
		app.mu.Unlock()
		if err != nil {
			b.Fatalf("markDirty: %v", err)
		}
	}
}

// BenchmarkWriteCollectionFilesLocked measures rewriting an entire 50-request
// collection to disk, which is what currently happens when a single request is
// saved. Target of US-015 (dirty-set collection writes).
func BenchmarkWriteCollectionFilesLocked(b *testing.B) {
	app := newLargeWorkspaceAppForTest(b, b.TempDir(), benchFixtureOptions())

	app.mu.Lock()
	if len(app.state.Workspaces) == 0 || len(app.state.Workspaces[0].Collections) == 0 {
		app.mu.Unlock()
		b.Fatal("fixture produced no collections")
	}
	collection := app.state.Workspaces[0].Collections[0]
	app.mu.Unlock()
	collection.Path = b.TempDir()

	// Warm-up, deliberately outside the timer. writeCollectionFilesLocked calls
	// ensureRequestFilePaths, which rewrites every item's FilePath the first time
	// (the fixture's paths are not inside the temp Path, so they are regenerated).
	// Because `collection` is a shallow copy, Items shares its backing array, so
	// that rewrite persists — making iteration 1 do work that iterations 2+ skip.
	// Without this warm-up the first iteration is measurably slower and skews a
	// short -benchtime=10x run by ~10%.
	app.mu.Lock()
	if err := app.writeCollectionFilesLocked(&collection); err != nil {
		app.mu.Unlock()
		b.Fatalf("warm-up writeCollectionFilesLocked: %v", err)
	}
	app.mu.Unlock()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		app.mu.Lock()
		err := app.writeCollectionFilesLocked(&collection)
		app.mu.Unlock()
		if err != nil {
			b.Fatalf("writeCollectionFilesLocked: %v", err)
		}
	}
}

// BenchmarkInterpolate covers the up-to-8-pass x every-var x ReplaceAll scan.
// Target of US-023, whose acceptance criterion is >=10x improvement at 50 vars —
// so the vars=50 case is the one that story is graded on.
func BenchmarkInterpolate(b *testing.B) {
	for _, n := range []int{10, 50, 200} {
		b.Run(fmt.Sprintf("vars=%d", n), func(b *testing.B) {
			vars := make(map[string]string, n)
			for i := 0; i < n; i++ {
				vars[fmt.Sprintf("var%d", i)] = fmt.Sprintf("value-%d", i)
			}
			// Only some variables actually appear in the input, so the benchmark
			// also measures the cost of scanning for absent variables — which is
			// exactly the cost the current implementation pays.
			var sb strings.Builder
			sb.WriteString(`{"url":"https://{{var0}}.example.test/api/{{var1}}",`)
			for i := 0; i < n; i += 5 {
				fmt.Fprintf(&sb, `"field%d":"{{var%d}}",`, i, i)
			}
			sb.WriteString(`"tail":"done"}`)
			input := sb.String()

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = interpolate(input, vars)
			}
		})
	}
}

// BenchmarkNewScriptRuntimeWithMeta measures building a goja runtime with all 25
// JS shims RunString'd fresh and no *goja.Program cache. Target of US-018
// (>=5x improvement) and US-019 (runtime pool).
//
// Note: newScriptRuntime (no -WithMeta) was dead code removed under US-003. This
// is the live entry point and the one the Phase 1 target is graded on.
func BenchmarkNewScriptRuntimeWithMeta(b *testing.B) {
	item := RequestItem{
		ID:     "bench-req",
		Name:   "bench",
		Type:   "http",
		Method: "GET",
		URL:    "https://fixture.example.test/api",
	}
	response := Response{Status: 200, StatusText: "OK", Body: `{"ok":true}`}
	vars := map[string]string{"token": "abc", "host": "fixture.example.test"}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var testResults []TestResult
		var scriptLogs []ScriptLog
		rt, _, _, _ := scripting.NewScriptRuntimeWithMeta(item, response, vars, &testResults, &scriptLogs, nil, scripting.ScriptRuntimeMeta{})
		if rt == nil {
			b.Fatal("scripting.NewScriptRuntimeWithMeta returned a nil runtime")
		}
	}
}

// BenchmarkExecuteHTTP measures one full request against a loopback server,
// including transport setup. Deliberately uses httptest rather than
// qa/platformfixture so the benchmark stays hermetic with no external process.
// The proxy/mTLS connection-reuse proof belongs to US-016, whose criterion is
// one TCP connection across N sequential sends.
func BenchmarkExecuteHTTP(b *testing.B) {
	payload := "[" + strings.TrimSuffix(strings.Repeat(`{"k":"v"},`, 512), ",") + "]"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(payload))
	}))
	defer server.Close()

	app := newAppForTest(b)
	collection := Collection{ID: "bench-coll", Name: "bench", Format: "bru"}
	item := RequestItem{
		ID:     "bench-req",
		Name:   "bench",
		Type:   "http",
		Method: "GET",
		URL:    server.URL,
		Headers: []KeyValue{
			{Name: "Accept", Value: "application/json", Enabled: true},
		},
	}
	// executeHTTP is not verified to nil-guard recordTimeline, so pass a no-op
	// rather than nil. Passing nil here would be testing a guard, not the path.
	noopTimeline := func(TimelineItem) {}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		resp := app.executeHTTP(b.Context(), collection.ID, collection, item, map[string]string{}, nil, noopTimeline)
		if resp.Error != "" {
			b.Fatalf("executeHTTP: %s", resp.Error)
		}
	}
}

// BenchmarkRunnerLookups — US-024.
//
// The runner cannot be benchmarked end to end: RunCollectionWithOptions does a
// real network send plus a markDirty (~574 us) and a persistLocked (~226 ms on
// the fixture) per request, so 500 requests is minutes of I/O per iteration and
// the ID lookups would be invisible under it. This benchmark therefore isolates
// exactly the lookup sequence the runner performs per request, and nothing
// else. It models a full run of every request in the fixture, so the iteration
// count scales with the fixture's request count rather than with one
// collection's share of it.
//
// Two arms, so the complexity claim stays checkable after the story is closed
// rather than resting on numbers captured against deleted code:
//
//	linear  — the pre-US-024 sequence, five linear scans per request:
//	          findCollectionWithWorkspaceLocked + findItem on entry to
//	          sendRequestWithControlsContextProvenance, findCollectionLocked +
//	          findItem on
//	          its tail, and findItemInState back in the runner loop.
//	indexed — what the runner does now: the same two resolutions through the
//	          scoped runnerLookupIndex, and no re-find at all in the loop.
//
// The acceptance evidence is the ns/op RATIO across N, not any single number.
// Every scan in the linear arm is O(C) or O(N/C), so its total work is
// O(N * (C + N/C)) — at C=10, 5x the requests costs well over 5x the time. The
// indexed arm is O(N) once the per-collection maps are built, so it should
// track ~5x.
//
// a.mu is taken once around the timed loop rather than per request. The *Locked
// helpers require it and this is single-goroutine, so per-call locking would
// only add a constant that is not what is being measured.
//
// Run with -benchtime=2000x (see the methodology note in
// .ralph/baseline/bench.txt): one op is 10..800 us here, so the 10x used for
// the slow benchmarks would be pure scheduling noise, while the 1s default
// would spend minutes on the linear arm.
func BenchmarkRunnerLookups(b *testing.B) {
	type target struct{ collectionID, itemID string }

	for _, requests := range []int{100, 250, 500} {
		opts := benchFixtureOptions()
		opts.RequestsPerColl = requests / opts.Collections
		// Cached bodies are irrelevant to ID lookups and cost ~30 MB per
		// sub-benchmark to build. Shape (collections x requests) is what this
		// benchmark varies.
		opts.LargeResponses = 0
		app := newLargeWorkspaceAppForTest(b, b.TempDir(), opts)

		targets := []target{}
		app.mu.Lock()
		for _, collection := range app.state.Workspaces[0].Collections {
			for _, item := range collection.Items {
				targets = append(targets, target{collection.ID, item.ID})
			}
		}
		app.mu.Unlock()
		if len(targets) != requests {
			b.Fatalf("fixture built %d requests, want %d", len(targets), requests)
		}

		b.Run(fmt.Sprintf("mode=linear/requests=%d", requests), func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				app.mu.Lock()
				for _, t := range targets {
					_, collection, err := app.findCollectionWithWorkspaceLocked(t.collectionID)
					if err != nil {
						b.Fatalf("findCollectionWithWorkspaceLocked: %v", err)
					}
					if _, err := findItem(collection, t.itemID); err != nil {
						b.Fatalf("findItem (entry): %v", err)
					}
					collection, err = app.findCollectionLocked(t.collectionID)
					if err != nil {
						b.Fatalf("findCollectionLocked: %v", err)
					}
					if _, err := findItem(collection, t.itemID); err != nil {
						b.Fatalf("findItem (tail): %v", err)
					}
					if _, ok := findItemInState(app.state, t.collectionID, t.itemID); !ok {
						b.Fatalf("findItemInState: %s/%s not found", t.collectionID, t.itemID)
					}
				}
				app.mu.Unlock()
			}
		})

		b.Run(fmt.Sprintf("mode=indexed/requests=%d", requests), func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				app.mu.Lock()
				// Built once per simulated run, exactly as the runner does, so
				// the arm pays for its own index construction.
				index := newRunnerLookupIndex(&app.state)
				for _, t := range targets {
					_, collection, err := app.findCollectionWithWorkspaceIndexedLocked(index, t.collectionID)
					if err != nil {
						b.Fatalf("indexed collection (entry): %v", err)
					}
					if _, err := index.findItemIndexed(t.collectionID, collection, t.itemID); err != nil {
						b.Fatalf("indexed item (entry): %v", err)
					}
					_, collection, err = app.findCollectionWithWorkspaceIndexedLocked(index, t.collectionID)
					if err != nil {
						b.Fatalf("indexed collection (tail): %v", err)
					}
					if _, err := index.findItemIndexed(t.collectionID, collection, t.itemID); err != nil {
						b.Fatalf("indexed item (tail): %v", err)
					}
				}
				app.mu.Unlock()
			}
		})
	}
}
