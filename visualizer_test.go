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
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestVisualizerSandboxNeverAllowsSameOrigin is the single most important
// assertion in this file.
//
// allow-scripts alone gives the frame an opaque origin. Adding
// allow-same-origin puts it in the app's origin, where it can read
// localStorage, reach document.cookie and script the parent — at which point
// the sandbox attribute buys nothing at all, while still LOOKING like a
// sandbox in the markup.
func TestVisualizerSandboxNeverAllowsSameOrigin(t *testing.T) {
	if !strings.Contains(VisualizerSandbox, "allow-scripts") {
		t.Errorf("sandbox = %q, but the template must be able to run scripts", VisualizerSandbox)
	}
	if strings.Contains(VisualizerSandbox, "allow-same-origin") {
		t.Fatalf("sandbox = %q — allow-same-origin puts the template in the app's origin and voids the containment", VisualizerSandbox)
	}
	for _, forbidden := range []string{"allow-top-navigation", "allow-popups", "allow-modals", "allow-downloads", "allow-forms"} {
		if strings.Contains(VisualizerSandbox, forbidden) {
			t.Errorf("sandbox = %q grants %s, which the visualizer does not need", VisualizerSandbox, forbidden)
		}
	}
}

// TestVisualizerDocumentCarriesAStrictCSP. Without default-src 'none' a
// template can exfiltrate the response it was handed by encoding it into an
// image URL — and the sandbox does not stop that, because an opaque origin can
// still make requests.
func TestVisualizerDocumentCarriesAStrictCSP(t *testing.T) {
	document := buildVisualizerDocument(VisualizerPayload{Template: "<p>hello</p>"})

	if !strings.Contains(document, "Content-Security-Policy") {
		t.Fatal("the document carries no CSP at all")
	}
	if !strings.Contains(document, "default-src &#39;none&#39;") && !strings.Contains(document, "default-src 'none'") {
		t.Errorf("the CSP does not deny by default:\n%s", document)
	}
	for _, directive := range []string{"form-action", "base-uri"} {
		if !strings.Contains(document, directive) {
			t.Errorf("the CSP omits %s", directive)
		}
	}
	// No remote scheme may be reachable.
	for _, scheme := range []string{"https:", "http:", "*"} {
		policyStart := strings.Index(document, "Content-Security-Policy")
		policyEnd := strings.Index(document[policyStart:], ">") + policyStart
		if strings.Contains(document[policyStart:policyEnd], scheme) {
			t.Errorf("the CSP allows %q, so a template could reach the network: %s", scheme, document[policyStart:policyEnd])
		}
	}
}

// TestVisualizerEscapesResponseData is the third layer. Containment is not a
// licence to let the frame's own DOM be hijacked: the template author's markup
// and the server's data are different trust levels.
func TestVisualizerEscapesResponseData(t *testing.T) {
	data, err := json.Marshal(map[string]interface{}{
		"name": `</td><script>alert(1)</script>`,
		"attr": `" onload="alert(2)`,
	})
	if err != nil {
		t.Fatal(err)
	}

	document := buildVisualizerDocument(VisualizerPayload{
		Template: `<table><tr><td>{{name}}</td><td title="{{attr}}">x</td></tr></table>`,
		Data:     string(data),
	})

	if strings.Contains(document, "<script>alert(1)</script>") {
		t.Errorf("response data closed its cell and injected a script tag:\n%s", document)
	}
	if strings.Contains(document, `onload="alert(2)`) {
		t.Errorf("response data broke out of an attribute:\n%s", document)
	}
	// It must still be VISIBLE, just inert.
	if !strings.Contains(document, "&lt;script&gt;") {
		t.Errorf("the value was dropped rather than escaped:\n%s", document)
	}
}

// TestVisualizerTripleBraceIsDeliberatelyRaw. Handlebars' {{{x}}} means "do not
// escape", and a template author asking for raw HTML is making a choice inside
// a sandboxed frame. The test exists so the distinction is deliberate rather
// than an accident of the parser.
func TestVisualizerTripleBraceIsDeliberatelyRaw(t *testing.T) {
	data := `{"markup":"<b>bold</b>"}`
	escaped := buildVisualizerDocument(VisualizerPayload{Template: `{{markup}}`, Data: data})
	raw := buildVisualizerDocument(VisualizerPayload{Template: `{{{markup}}}`, Data: data})

	if !strings.Contains(escaped, "&lt;b&gt;bold&lt;/b&gt;") {
		t.Errorf("the double brace did not escape:\n%s", escaped)
	}
	if !strings.Contains(raw, "<b>bold</b>") {
		t.Errorf("the triple brace did not pass raw markup through:\n%s", raw)
	}
}

func TestVisualizerRendersTemplateSubset(t *testing.T) {
	data := `{
		"title": "Report",
		"rows": [{"name": "Ada", "score": 10}, {"name": "Grace", "score": 20}],
		"empty": [],
		"flag": true
	}`

	cases := []struct{ name, template, want string }{
		{"path", `<h1>{{title}}</h1>`, "<h1>Report</h1>"},
		{"nested path", `{{rows.0.name}}`, ""}, // numeric index is not supported; left unresolved
		{"each with this", `{{#each rows}}<li>{{name}}:{{score}}</li>{{/each}}`, "<li>Ada:10</li><li>Grace:20</li>"},
		{"each index", `{{#each rows}}<i>{{@index}}</i>{{/each}}`, "<i>0</i><i>1</i>"},
		{"if truthy", `{{#if flag}}yes{{else}}no{{/if}}`, "yes"},
		{"if falsy", `{{#if empty}}yes{{else}}no{{/if}}`, "no"},
		{"missing path renders empty", `[{{nope}}]`, "[]"},
		{"root reachable inside each", `{{#each rows}}{{title}}{{/each}}`, "ReportReport"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := renderVisualizerTemplate(tc.template, decodeVisualizerData(data))
			if tc.want != "" && !strings.Contains(got, tc.want) {
				t.Errorf("template %q rendered %q, want it to contain %q", tc.template, got, tc.want)
			}
		})
	}
}

// TestVisualizerLeavesUnsupportedHelpersVisible. A partial Handlebars that
// silently drops a helper it does not know renders a plausible-but-wrong page;
// leaving the tag in place shows the author it did not resolve.
func TestVisualizerLeavesUnsupportedHelpersVisible(t *testing.T) {
	got := renderVisualizerTemplate(`a{{#with thing}}b{{/with}}c`, map[string]interface{}{})
	if !strings.Contains(got, "{{#with thing}}") {
		t.Errorf("an unsupported helper was silently dropped: %q", got)
	}
}

func TestVisualizerHandlesNestedBlocks(t *testing.T) {
	data := `{"groups":[{"items":["a","b"]},{"items":["c"]}]}`
	got := renderVisualizerTemplate(`{{#each groups}}[{{#each items}}{{this}}{{/each}}]{{/each}}`, decodeVisualizerData(data))
	// An inner {{/each}} must not close the outer block.
	if got != "[ab][c]" {
		t.Errorf("nested each rendered %q, want [ab][c]", got)
	}
}

func TestVisualizerHandlesMalformedInput(t *testing.T) {
	for _, tc := range []struct{ name, template, data string }{
		{"unterminated tag", `hello {{name`, `{}`},
		{"unclosed block", `{{#each rows}}x`, `{"rows":[1]}`},
		{"malformed data", `{{name}}`, `{not json`},
		{"empty template", ``, `{}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// The contract is only that it terminates and returns something;
			// a panic here would take down the whole response view.
			_ = buildVisualizerDocument(VisualizerPayload{Template: tc.template, Data: tc.data})
		})
	}
}

func TestVisualizerRejectsOversizedPayloads(t *testing.T) {
	if _, err := normalizeVisualizerPayload(VisualizerPayload{Template: strings.Repeat("x", visualizerTemplateLimit+1)}); err == nil {
		t.Error("an oversized template should be rejected")
	}
	if _, err := normalizeVisualizerPayload(VisualizerPayload{Data: strings.Repeat("x", visualizerDataLimit+1)}); err == nil {
		t.Error("oversized data should be rejected")
	}
	if _, err := normalizeVisualizerPayload(VisualizerPayload{Template: "<p>ok</p>", Data: `{"a":1}`}); err != nil {
		t.Errorf("a normal payload was rejected: %v", err)
	}
}

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
	want := "const visualizerSandboxAttribute = '" + VisualizerSandbox + "'"
	if !strings.Contains(string(source), want) {
		t.Errorf("App.svelte does not declare %s; the frontend sandbox has diverged from VisualizerSandbox", want)
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
