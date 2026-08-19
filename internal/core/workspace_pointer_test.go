package core

// US-076 — the runner must not write through a *Workspace captured before a.mu
// was released for network I/O.
//
// THE BUG, which was pre-existing and is not a race the detector can find.
// sendRequestWithControlsContext resolved `ws` (a pointer INTO the
// a.state.Workspaces backing array), released a.mu for the round trip,
// re-acquired it, and then handed that same pointer to
// scripting.ApplyScriptVariableContextToState. Anything that appended a workspace past
// the slice's capacity while the lock was free reallocated the backing array,
// after which the captured pointer addressed memory nothing reads. The write
// succeeded, reported no error, and vanished.
//
// -race cannot catch this. Both accesses are correctly serialised under a.mu;
// the pointer is simply stale. Only a test that forces the reallocation inside
// the I/O window and then looks for the value can see it, which is what this
// file does.

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// readAppSourceForTest returns every non-test source file in this package,
// concatenated, so a test can assert on a property of the code itself. Used
// sparingly and only where a behavioural test cannot pin the property on its
// own.
//
// It reads the working directory rather than a named path, which is why moving
// the whole package out of the repository root and into internal/core did not
// disturb it — the same reason the file-motion guard below exists.
//
// It used to read app.go alone. That made it fail the moment
// sendRequestWithControlsContext moved to app_send.go during the file split —
// reporting a US-076 regression when nothing about the property had changed.
// A guard that fires on code motion is a guard people learn to edit away.
//
// Scanning the whole package keeps it exactly as strict about the thing it
// actually protects (the call must exist, and must not pass the pre-I/O
// pointer) while saying nothing about which file that code lives in.
func readAppSourceForTest(t *testing.T) string {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package directory: %v", err)
	}
	var combined strings.Builder
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		combined.Write(data)
		combined.WriteByte('\n')
	}
	if combined.Len() == 0 {
		t.Fatal("no package sources found; the guard would pass vacuously")
	}
	return combined.String()
}

// globalVariableValue returns the value of a named global variable in the given
// workspace, or "" when absent. Variable.Value is interface{}, so it is
// rendered rather than asserted to a string: a value stored as a non-string
// should read as a mismatch, not panic the test.
func globalVariableValue(state AppState, workspaceID, name string) string {
	for _, workspace := range state.Workspaces {
		if workspace.ID != workspaceID {
			continue
		}
		for _, environment := range workspace.GlobalEnvironments {
			for _, variable := range environment.Variables {
				if variable.Name == name {
					return fmt.Sprintf("%v", variable.Value)
				}
			}
		}
	}
	return ""
}

// TestRunnerGlobalVariableSurvivesWorkspaceReallocation forces the exact
// interleaving the bug needs.
//
// The workspace pointer is only dereferenced when a script marks GLOBAL
// variables dirty (scripting.ApplyScriptVariableContextToState touches `workspace` under
// ctx.GlobalDirty and nowhere else), so the request runs a post-response script
// that calls bru.setGlobalEnvVar. The httptest handler runs while a.mu is
// released, and creates enough workspaces to guarantee the Workspaces slice
// outgrows its capacity and moves.
func TestRunnerGlobalVariableSurvivesWorkspaceReallocation(t *testing.T) {
	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	collection := state.Workspaces[0].Collections[0]
	originalWorkspaceID := state.Workspaces[0].ID

	// The handler runs on the server goroutine while the runner has released
	// a.mu. Appending workspaces here is what reallocates the backing array
	// that the captured pointer refers to.
	reallocated := make(chan int, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		created := 0
		// Go grows a slice by doubling, so a handful of appends is enough to
		// cross any starting capacity this fixture could have.
		for i := range 8 {
			if _, err := app.CreateWorkspace(fmt.Sprintf("realloc %d", i)); err != nil {
				break
			}
			created++
		}
		reallocated <- created
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	created, err := app.CreateRequest(collection.ID, "http", "global setter")
	if err != nil {
		t.Fatalf("CreateRequest: %v", err)
	}
	var itemID string
	for _, c := range created.Workspaces[0].Collections {
		if c.ID != collection.ID {
			continue
		}
		for _, item := range c.Items {
			if item.Name == "global setter" {
				itemID = item.ID
			}
		}
	}
	if itemID == "" {
		t.Fatalf("could not find the request just created")
	}

	url := server.URL
	script := `bru.setGlobalEnvVar("us076", "survived")`
	if _, err := app.UpdateRequest(collection.ID, itemID, RequestPatch{URL: &url, PostScript: &script}); err != nil {
		t.Fatalf("UpdateRequest: %v", err)
	}

	finalState, err := app.SendRequest(collection.ID, itemID, "")
	if err != nil {
		t.Fatalf("SendRequest: %v", err)
	}
	if n := <-reallocated; n == 0 {
		t.Fatalf("the handler created no workspaces; the reallocation window never opened")
	}

	// The variable must be in the workspace that owns the collection, read out
	// of live state rather than out of anything the runner returned by pointer.
	found := globalVariableValue(finalState, originalWorkspaceID, "us076")
	if found != "survived" {
		t.Errorf("global variable set by the post-response script did not reach live state (got %q); "+
			"the runner wrote through a workspace pointer that the reallocation invalidated", found)
	}

	// And it must still be there on a fresh read, not merely in the returned
	// value — the returned AppState aliases live state, so a write into a dead
	// array could in principle still show up in one and not the other.
	reread, err := app.GetState()
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if found = globalVariableValue(reread, originalWorkspaceID, "us076"); found != "survived" {
		t.Errorf("global variable is absent on re-read (got %q)", found)
	}
}

// TestRunnerResolvesWorkspaceAfterReacquiringTheLock is a source-level guard.
//
// The behavioural test above only fails when the reallocation actually happens,
// which depends on slice capacity and could stop being true if the fixture
// changes shape — leaving the bug uncovered and the test still green. This one
// fails the moment anyone reintroduces the captured pointer at the call site,
// regardless of fixture geometry.
func TestRunnerResolvesWorkspaceAfterReacquiringTheLock(t *testing.T) {
	source := readAppSourceForTest(t)
	const call = "scripting.ApplyScriptVariableContextToState(&a.state, liveWorkspace, collection, environmentID, scriptVariables)"
	if !strings.Contains(source, call) {
		t.Errorf("sendRequestWithControlsContext no longer passes the re-resolved workspace to "+
			"scripting.ApplyScriptVariableContextToState.\nExpected to find:\n  %s\n"+
			"If this call was legitimately restructured, keep the property: the workspace must be "+
			"resolved AFTER a.mu is re-acquired, never captured across the release.", call)
	}
	if strings.Contains(source, "scripting.ApplyScriptVariableContextToState(&a.state, ws,") {
		t.Error("sendRequestWithControlsContext passes the pre-I/O `ws` pointer again (US-076 regression)")
	}
}
