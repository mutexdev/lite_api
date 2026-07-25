package main

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
)

func benchFixtureOptions() largeWorkspaceOptions {
	return defaultLargeWorkspaceOptions()
}

// BenchmarkPersistLocked measures the keystroke path described in
// improvement_v2.md §2.1.B: MarshalIndent of the entire AppState followed by a
// synchronous, non-atomic os.WriteFile. Target of US-012 (async coalesced
// persistence) and US-009 (response body store).
func BenchmarkPersistLocked(b *testing.B) {
	app := newLargeWorkspaceApp(b.TempDir(), benchFixtureOptions())

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
	app := NewAppWithDir(b.TempDir())

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
	app := newLargeWorkspaceApp(b.TempDir(), benchFixtureOptions())

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
	app := newLargeWorkspaceApp(b.TempDir(), benchFixtureOptions())

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
		rt, _, _, _ := newScriptRuntimeWithMeta(item, response, vars, &testResults, &scriptLogs, nil, scriptRuntimeMeta{})
		if rt == nil {
			b.Fatal("newScriptRuntimeWithMeta returned a nil runtime")
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

	app := NewAppWithDir(b.TempDir())
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
