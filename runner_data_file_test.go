package main

// US-046 — tests for runner data files.
//
// The assertion that carries the story is the end-to-end one: a row's columns
// must reach the WIRE through {{var}} interpolation. Everything else here is
// parsing, and parsing that produces correct-looking maps nobody substitutes
// would pass a unit test while shipping requests with {{userId}} in the URL.

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func writeDataFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}

func TestRunnerDataRowsParsesCSV(t *testing.T) {
	path := writeDataFile(t, "users.csv", "userId,name\n1,Ada\n2,Grace\n3,Alan\n")
	rows, err := runnerDataRows(path)
	if err != nil {
		t.Fatalf("runnerDataRows: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("got %d rows, want 3", len(rows))
	}
	if rows[0]["userId"] != "1" || rows[0]["name"] != "Ada" {
		t.Errorf("row 0 = %v", rows[0])
	}
	if rows[2]["name"] != "Alan" {
		t.Errorf("row 2 = %v", rows[2])
	}
}

// TestRunnerDataRowsToleratesRaggedAndBlankCSV. Hand-edited CSV routinely has a
// trailing blank line or a short final row; refusing the whole file over it
// helps nobody, and a missing cell is exactly the empty string the header
// promised.
func TestRunnerDataRowsToleratesRaggedAndBlankCSV(t *testing.T) {
	path := writeDataFile(t, "ragged.csv", "a,b,c\n1,2,3\n4,5\n\n6,7,8\n")
	rows, err := runnerDataRows(path)
	if err != nil {
		t.Fatalf("runnerDataRows: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("got %d rows, want 3 (the blank line dropped)", len(rows))
	}
	if rows[1]["c"] != "" {
		t.Errorf("short row's missing cell = %q, want empty", rows[1]["c"])
	}
}

// TestRunnerDataRowsStripsTheUTF8BOM. Excel writes one, and without stripping
// it the first column is named "\ufeffuserId" — so {{userId}} silently never
// resolves and every request goes out with the literal placeholder.
func TestRunnerDataRowsStripsTheUTF8BOM(t *testing.T) {
	path := writeDataFile(t, "bom.csv", "\ufeffuserId,name\n1,Ada\n")
	rows, err := runnerDataRows(path)
	if err != nil {
		t.Fatalf("runnerDataRows: %v", err)
	}
	if _, ok := rows[0]["userId"]; !ok {
		t.Errorf("BOM was not stripped from the header: %v", rows[0])
	}
}

func TestRunnerDataRowsParsesJSON(t *testing.T) {
	path := writeDataFile(t, "users.json", `[
		{"userId": 1, "name": "Ada", "active": true, "note": null},
		{"userId": 1000000, "name": "Grace", "tags": ["a","b"]}
	]`)
	rows, err := runnerDataRows(path)
	if err != nil {
		t.Fatalf("runnerDataRows: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}
	if rows[0]["userId"] != "1" {
		t.Errorf("userId = %q, want \"1\"", rows[0]["userId"])
	}
	if rows[0]["active"] != "true" {
		t.Errorf("active = %q, want \"true\"", rows[0]["active"])
	}
	if rows[0]["note"] != "" {
		t.Errorf("null = %q, want empty", rows[0]["note"])
	}
	// The one that bites: %v renders 1e+06 and sends the request to the wrong
	// resource. strconv keeps the integer.
	if rows[1]["userId"] != "1000000" {
		t.Errorf("large id = %q, want \"1000000\" — a scientific-notation id is a request to the wrong resource", rows[1]["userId"])
	}
	if rows[1]["tags"] != `["a","b"]` {
		t.Errorf("array = %q, want its JSON form", rows[1]["tags"])
	}
}

func TestRunnerDataRowsRejectsBadInput(t *testing.T) {
	cases := []struct{ name, file, content string }{
		{"unknown extension", "data.txt", "a,b\n1,2\n"},
		{"json that is not an array", "data.json", `{"a":1}`},
		{"empty csv", "data.csv", ""},
		{"header only", "data.csv", "a,b\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeDataFile(t, tc.file, tc.content)
			if _, err := runnerDataRows(path); err == nil {
				t.Error("expected an error")
			}
		})
	}

	if _, err := runnerDataRows(filepath.Join(t.TempDir(), "missing.csv")); err == nil {
		t.Error("a missing file should be an error")
	}
	// No data file is not an error — it is the default.
	rows, err := runnerDataRows("")
	if err != nil || rows != nil {
		t.Errorf("empty path returned (%v, %v), want (nil, nil)", rows, err)
	}
}

// TestRunnerIterationPlanClampsToTheRowCount. Padding instead of clamping is
// the failure this guards: an iteration with no row behind it sends
// {{userId}} unresolved to the server, which looks like a broken collection
// rather than a misconfigured run.
func TestRunnerIterationPlanClampsToTheRowCount(t *testing.T) {
	rows := make([]map[string]string, 3)

	if got := runnerIterationPlan(rows, 0); got != 3 {
		t.Errorf("unspecified iterations = %d, want the 3 rows", got)
	}
	if got := runnerIterationPlan(rows, 2); got != 2 {
		t.Errorf("fewer iterations than rows = %d, want 2", got)
	}
	if got := runnerIterationPlan(rows, 10); got != 3 {
		t.Errorf("more iterations than rows = %d, want 3 — an iteration with no row must not run", got)
	}
	if got := runnerIterationPlan(nil, 5); got != 5 {
		t.Errorf("no data file = %d, want the requested 5", got)
	}
	if got := runnerIterationPlan(nil, 0); got != 1 {
		t.Errorf("no data file, no iterations = %d, want 1", got)
	}
}

// TestDataFileRowsReachTheWire is the story. Parsing that produces correct maps
// nobody substitutes would satisfy every test above while shipping literal
// {{userId}} placeholders.
func TestDataFileRowsReachTheWire(t *testing.T) {
	var mu sync.Mutex
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path+"?"+r.URL.RawQuery)
		mu.Unlock()
		_, _ = fmt.Fprint(w, `{"ok":true}`)
	}))
	defer server.Close()

	dataPath := writeDataFile(t, "users.csv", "userId,label\n11,alpha\n22,beta\n33,gamma\n")

	app, collectionID, ids := iterationFixture(t, server.URL+"/users/{{userId}}?label={{label}}", 1)

	state, err := app.RunCollectionWithOptions(collectionID, "", RunnerOptions{
		SelectedItemIDs: ids,
		DataFile:        dataPath,
	})
	if err != nil {
		t.Fatalf("RunCollectionWithOptions: %v", err)
	}

	if state.Runner.CompletedIterations != 3 {
		t.Errorf("completed %d iterations, want 3 — the row count should drive the run", state.Runner.CompletedIterations)
	}
	if state.Runner.Passed != 3 {
		t.Errorf("Passed = %d, want 3", state.Runner.Passed)
	}

	mu.Lock()
	got := append([]string(nil), paths...)
	mu.Unlock()

	want := []string{"/users/11?label=alpha", "/users/22?label=beta", "/users/33?label=gamma"}
	if len(got) != len(want) {
		t.Fatalf("server saw %d requests %v, want %d", len(got), got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("request %d was %q, want %q", i, got[i], want[i])
		}
	}
}

// TestDataFileDoesNotOverrideRuntimeVariables pins the precedence decision.
// A row is chosen before the iteration starts; bru.setVar is a deliberate act
// during it. If the row won, a script could not correct a value for the request
// that follows, and would fail with no error to point at.
func TestDataFileDoesNotOverrideRuntimeVariables(t *testing.T) {
	ctx := newFlatScriptVariableContext(map[string]string{})
	ctx.Env["token"] = "from-environment"
	ctx.Env["only-env"] = "environment"
	ctx.Runtime["token"] = "from-setVar"
	ctx.Recompute()

	applyIterationDataToContext(ctx, map[string]string{"token": "from-data", "userId": "42"})

	if ctx.Combined["token"] != "from-setVar" {
		t.Errorf("token = %q, want the bru.setVar value — data must not overwrite a runtime variable", ctx.Combined["token"])
	}
	if ctx.Combined["userId"] != "42" {
		t.Errorf("userId = %q, want the data row's value", ctx.Combined["userId"])
	}
	if ctx.Combined["only-env"] != "environment" {
		t.Errorf("an unrelated environment variable was disturbed: %q", ctx.Combined["only-env"])
	}

	// And data DOES beat the environment, which is the half that makes the
	// feature useful at all.
	delete(ctx.Runtime, "token")
	ctx.Recompute()
	applyIterationDataToContext(ctx, map[string]string{"token": "from-data"})
	if ctx.Combined["token"] != "from-data" {
		t.Errorf("token = %q, want the data row to beat the environment", ctx.Combined["token"])
	}
}

// TestNoDataFileLeavesTheDataScopeEmpty. Every run that predates US-046 must
// behave identically, so an absent row is a no-op rather than a clear.
func TestNoDataFileLeavesTheDataScopeEmpty(t *testing.T) {
	ctx := newFlatScriptVariableContext(map[string]string{"a": "1"})
	before := ctx.Combined["a"]
	applyIterationDataToContext(ctx, nil)
	applyIterationDataToContext(ctx, map[string]string{})
	if ctx.Combined["a"] != before {
		t.Errorf("an absent data row changed the variables: %q -> %q", before, ctx.Combined["a"])
	}
	if len(ctx.Data) != 0 {
		t.Errorf("Data scope is not empty: %v", ctx.Data)
	}
}
