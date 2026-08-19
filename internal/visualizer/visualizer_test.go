package visualizer

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mutexdev/lite_api/internal/scripting"
	"github.com/mutexdev/lite_api/internal/types"
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
	if !strings.Contains(Sandbox, "allow-scripts") {
		t.Errorf("sandbox = %q, but the template must be able to run scripts", Sandbox)
	}
	if strings.Contains(Sandbox, "allow-same-origin") {
		t.Fatalf("sandbox = %q — allow-same-origin puts the template in the app's origin and voids the containment", Sandbox)
	}
	for _, forbidden := range []string{"allow-top-navigation", "allow-popups", "allow-modals", "allow-downloads", "allow-forms"} {
		if strings.Contains(Sandbox, forbidden) {
			t.Errorf("sandbox = %q grants %s, which the visualizer does not need", Sandbox, forbidden)
		}
	}
}

// TestVisualizerDocumentCarriesAStrictCSP. Without default-src 'none' a
// template can exfiltrate the response it was handed by encoding it into an
// image URL — and the sandbox does not stop that, because an opaque origin can
// still make requests.
func TestVisualizerDocumentCarriesAStrictCSP(t *testing.T) {
	document := BuildDocument(types.VisualizerPayload{Template: "<p>hello</p>"})

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

	document := BuildDocument(types.VisualizerPayload{
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
	escaped := BuildDocument(types.VisualizerPayload{Template: `{{markup}}`, Data: data})
	raw := BuildDocument(types.VisualizerPayload{Template: `{{{markup}}}`, Data: data})

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
			_ = BuildDocument(types.VisualizerPayload{Template: tc.template, Data: tc.data})
		})
	}
}

func TestVisualizerRejectsOversizedPayloads(t *testing.T) {
	if _, err := scripting.NormalizeVisualizerPayload(types.VisualizerPayload{Template: strings.Repeat("x", scripting.VisualizerTemplateLimit+1)}); err == nil {
		t.Error("an oversized template should be rejected")
	}
	if _, err := scripting.NormalizeVisualizerPayload(types.VisualizerPayload{Data: strings.Repeat("x", scripting.VisualizerDataLimit+1)}); err == nil {
		t.Error("oversized data should be rejected")
	}
	if _, err := scripting.NormalizeVisualizerPayload(types.VisualizerPayload{Template: "<p>ok</p>", Data: `{"a":1}`}); err != nil {
		t.Errorf("a normal payload was rejected: %v", err)
	}
}
