package core

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

	"github.com/mutexdev/lite_api/internal/scripting"
)

// writeDataFile is duplicated from internal/runner's tests. Test helpers do not
// cross a package boundary without being exported into the production API, and
// a seven-line file writer is not worth that.
func writeDataFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}

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
	ctx := scripting.NewFlatScriptVariableContext(map[string]string{})
	ctx.Env["token"] = "from-environment"
	ctx.Env["only-env"] = "environment"
	ctx.Runtime["token"] = "from-setVar"
	ctx.Recompute()

	scripting.ApplyIterationDataToContext(ctx, map[string]string{"token": "from-data", "userId": "42"})

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
	scripting.ApplyIterationDataToContext(ctx, map[string]string{"token": "from-data"})
	if ctx.Combined["token"] != "from-data" {
		t.Errorf("token = %q, want the data row to beat the environment", ctx.Combined["token"])
	}
}

// TestNoDataFileLeavesTheDataScopeEmpty. Every run that predates US-046 must
// behave identically, so an absent row is a no-op rather than a clear.
func TestNoDataFileLeavesTheDataScopeEmpty(t *testing.T) {
	ctx := scripting.NewFlatScriptVariableContext(map[string]string{"a": "1"})
	before := ctx.Combined["a"]
	scripting.ApplyIterationDataToContext(ctx, nil)
	scripting.ApplyIterationDataToContext(ctx, map[string]string{})
	if ctx.Combined["a"] != before {
		t.Errorf("an absent data row changed the variables: %q -> %q", before, ctx.Combined["a"])
	}
	if len(ctx.Data) != 0 {
		t.Errorf("Data scope is not empty: %v", ctx.Data)
	}
}
