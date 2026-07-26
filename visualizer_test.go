package main

// US-058 — tests for the response Visualizer.
//
// The template comes from whoever wrote the collection and the data comes from
// whatever the server returned, so this renders attacker-influenced content.
// The containment tests below are the point of the story; the templating tests
// are supporting.
//
// Each containment layer is tested SEPARATELY rather than through one "is it
// safe" assertion, because they fail independently and each failure is silent:
// a missing CSP still renders correctly, and an unescaped value still renders
// correctly, right up until someone controls the data.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mutexdev/lite_api/internal/visualizer"
)

// TestPmVisualizerSetReachesTheResponse is the end-to-end claim: a script sets
// it and the stored response carries it.
func TestPmVisualizerSetReachesTheResponse(t *testing.T) {
	app, collectionID, itemID, closeServer := scriptProbeFixture(t)
	defer closeServer()

	state := sendWithScripts(t, app, collectionID, itemID, "", "", `
		pm.visualizer.set("<h1>{{title}}</h1>", { title: "from the script" })
	`)

	item, ok := findItemInState(state, collectionID, itemID)
	if !ok || item.Response == nil {
		t.Fatal("no response recorded")
	}
	if item.Response.Visualizer == nil {
		t.Fatal("pm.visualizer.set did not reach the response")
	}
	if item.Response.Visualizer.Template != "<h1>{{title}}</h1>" {
		t.Errorf("template = %q", item.Response.Visualizer.Template)
	}
	if !strings.Contains(item.Response.Visualizer.Data, "from the script") {
		t.Errorf("data = %q", item.Response.Visualizer.Data)
	}

	document, err := app.VisualizerDocument(collectionID, itemID)
	if err != nil {
		t.Fatalf("VisualizerDocument: %v", err)
	}
	if !strings.Contains(document, "<h1>from the script</h1>") {
		t.Errorf("the document did not render the template:\n%s", document)
	}
}

// TestVisualizerSetInThePreRequestPhaseSurvives. The pre-request script runs
// before a response exists, so a visualizer set there has to be carried
// forward rather than written to a response that is not there yet.
func TestVisualizerSetInThePreRequestPhaseSurvives(t *testing.T) {
	app, collectionID, itemID, closeServer := scriptProbeFixture(t)
	defer closeServer()

	state := sendWithScripts(t, app, collectionID, itemID,
		`pm.visualizer.set("<p>set early</p>", {})`, "", "")

	item, ok := findItemInState(state, collectionID, itemID)
	if !ok || item.Response == nil {
		t.Fatal("no response recorded")
	}
	if item.Response.Visualizer == nil {
		t.Fatal("a visualizer set in the pre-request script was lost")
	}
}

// TestLaterPhaseWinsForVisualizer. A tests script refining what the
// post-response script set is the normal case, not a conflict.
func TestLaterPhaseWinsForVisualizer(t *testing.T) {
	app, collectionID, itemID, closeServer := scriptProbeFixture(t)
	defer closeServer()

	state := sendWithScripts(t, app, collectionID, itemID,
		`pm.visualizer.set("<p>pre</p>", {})`,
		"",
		`pm.visualizer.set("<p>tests</p>", {})`)

	item, ok := findItemInState(state, collectionID, itemID)
	if !ok || item.Response == nil || item.Response.Visualizer == nil {
		t.Fatal("no visualizer recorded")
	}
	if item.Response.Visualizer.Template != "<p>tests</p>" {
		t.Errorf("template = %q, want the tests-phase one", item.Response.Visualizer.Template)
	}
}

func TestVisualizerDocumentIsEmptyWithoutAPayload(t *testing.T) {
	app, collectionID, itemID, closeServer := scriptProbeFixture(t)
	defer closeServer()

	sendWithScripts(t, app, collectionID, itemID, "", "", "")

	document, err := app.VisualizerDocument(collectionID, itemID)
	if err != nil {
		t.Fatalf("VisualizerDocument: %v", err)
	}
	if document != "" {
		t.Errorf("a response with no visualizer produced a document:\n%s", document)
	}
}

// TestFrontendSandboxMatchesTheGoConstant. The attribute lives in the Svelte
// component while the constant it must equal lives here. Nothing but this test
// connects them, and a divergence would not fail any build — it would just
// quietly weaken or break the containment.
func TestFrontendSandboxMatchesTheGoConstant(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("frontend", "src", "App.svelte"))
	if err != nil {
		t.Fatalf("read App.svelte: %v", err)
	}
	want := "const visualizerSandboxAttribute = '" + visualizer.Sandbox + "'"
	if !strings.Contains(string(source), want) {
		t.Errorf("App.svelte does not declare %s; the frontend sandbox has diverged from visualizer.Sandbox", want)
	}

	inspector, err := os.ReadFile(filepath.Join("frontend", "src", "lib", "workbench", "ResponseInspector.svelte"))
	if err != nil {
		t.Fatalf("read ResponseInspector.svelte: %v", err)
	}
	// Scoped to the visualizer's own tag, NOT the whole file. The first version
	// of this test searched the file and failed on an unrelated pre-existing
	// PDF-preview iframe that legitimately uses allow-same-origin (it grants no
	// allow-scripts, so nothing can execute in it). A whole-file check would
	// have sent me to "fix" code this story does not own.
	text := string(inspector)
	start := strings.Index(text, `data-testid="response-visualizer"`)
	if start < 0 {
		t.Fatal("the visualizer iframe is not present")
	}
	tagStart := strings.LastIndex(text[:start], "<iframe")
	tagEnd := strings.Index(text[start:], ">") + start
	if tagStart < 0 || tagEnd <= tagStart {
		t.Fatal("could not isolate the visualizer iframe tag")
	}
	tag := text[tagStart : tagEnd+1]

	if !strings.Contains(tag, "sandbox={visualizerSandbox}") {
		t.Errorf("the visualizer iframe does not set sandbox from the prop: %s", tag)
	}
	if strings.Contains(tag, "allow-same-origin") {
		t.Errorf("the visualizer iframe grants allow-same-origin, voiding the containment: %s", tag)
	}
	if !strings.Contains(tag, "srcdoc={visualizerDocument}") {
		t.Errorf("the iframe does not render the document built in Go: %s", tag)
	}
	// A src= would load a real URL into the frame, giving it a real origin and
	// bypassing both the srcdoc and the CSP inside it.
	if strings.Contains(tag, " src=") {
		t.Errorf("the visualizer iframe uses src=, which would give it a real origin: %s", tag)
	}
}
