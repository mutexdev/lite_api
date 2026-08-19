// The property that keeps a literal {{?token}} off the wire.
//
// Two things decide what a prompt-bearing request does: the SCANNER, which
// determines what the user is asked for before the request runs, and the
// SENDER, which determines what actually goes out. They are separate code, and
// the failure that matters is one-directional:
//
//	scanner ⊇ sender   the user may be asked for something unused. Annoying.
//	scanner ⊂ sender   a field goes out with a literal {{?name}} in it. The
//	                   server rejects it, or accepts it, and nothing in the UI
//	                   points at the prompt that never appeared.
//
// So the invariant is: EVERY FIELD THE SENDER USES IS A FIELD THE SCANNER
// VISITS. This test states it per body mode with a distinct marker in every
// field, so a mode added to one and not the other fails here rather than in
// someone's request.
package scripting

import (
	"strings"
	"testing"

	"github.com/mutexdev/lite_api/internal/types"
)

// scannedFields returns every string the prompt scanner looked at.
func scannedFields(body types.RequestBody) []string {
	var seen []string
	scanBodyPromptVariables(body,
		func(text string) { seen = append(seen, text) },
		func(rows []types.KeyValue) {
			for _, row := range rows {
				seen = append(seen, row.Name, row.Value)
			}
		})
	return seen
}

func sawAll(t *testing.T, mode string, body types.RequestBody, want ...string) {
	t.Helper()
	seen := strings.Join(scannedFields(body), "\x00")
	for _, field := range want {
		if !strings.Contains(seen, field) {
			t.Errorf("%s: the sender uses %q but the scanner never visits it; the request would go out with a literal prompt",
				mode, field)
		}
	}
}

// Every field the sender interpolates, per mode. Kept as one table so adding a
// mode to the sender without adding it here is visible as an omission.
func TestScannerVisitsEveryFieldTheSenderUses(t *testing.T) {
	sawAll(t, "json", types.RequestBody{Mode: "json", JSON: "{{?j}}"}, "{{?j}}")
	sawAll(t, "xml", types.RequestBody{Mode: "xml", XML: "{{?x}}"}, "{{?x}}")
	sawAll(t, "text", types.RequestBody{Mode: "text", Text: "{{?t}}"}, "{{?t}}")
	sawAll(t, "sparql", types.RequestBody{Mode: "sparql", Text: "{{?s}}"}, "{{?s}}")
	sawAll(t, "graphql", types.RequestBody{Mode: "graphql", GraphQLQuery: "{{?q}}", GraphQLVariables: "{{?v}}"},
		"{{?q}}", "{{?v}}")
	sawAll(t, "formUrlEncoded",
		types.RequestBody{Mode: "formUrlEncoded", FormURLEncoded: []types.KeyValue{{Name: "{{?fn}}", Value: "{{?fv}}", Enabled: true}}},
		"{{?fn}}", "{{?fv}}")

	// The sender interpolates Name, Value and FilePath for an enabled part.
	// ContentType is scanned too, which is the safe direction.
	sawAll(t, "multipartForm",
		types.RequestBody{Mode: "multipartForm", Multipart: []types.FormPart{
			{Name: "{{?mn}}", Value: "{{?mv}}", FilePath: "{{?mp}}", ContentType: "{{?mc}}", Enabled: true},
		}},
		"{{?mn}}", "{{?mv}}", "{{?mp}}", "{{?mc}}")

	// The sender uses the SELECTED entry's FilePath.
	sawAll(t, "file",
		types.RequestBody{Mode: "file", Files: []types.FileBodyEntry{
			{FilePath: "{{?f1}}"},
			{FilePath: "{{?f2}}", ContentType: "{{?c2}}", Selected: true},
		}},
		"{{?f2}}", "{{?c2}}")

	// And the legacy single-file shape.
	sawAll(t, "file legacy",
		types.RequestBody{Mode: "file", FilePath: "{{?lf}}", FileContentType: "{{?lc}}"},
		"{{?lf}}", "{{?lc}}")
}

// The scanner and the sender must agree about which parts count. They use the
// same predicate today — `!part.Enabled` and `if part.Enabled` — and if one
// were relaxed, a disabled part's prompt would be asked for but never sent, or
// worse, an enabled part's prompt would be skipped.
func TestScannerAndSenderAgreeOnWhichPartsCount(t *testing.T) {
	body := types.RequestBody{Mode: "multipartForm", Multipart: []types.FormPart{
		{Name: "on", Value: "{{?sent}}", Enabled: true},
		{Name: "off", Value: "{{?notSent}}"},
	}}
	seen := strings.Join(scannedFields(body), "\x00")

	if !strings.Contains(seen, "{{?sent}}") {
		t.Error("an enabled part's prompt was not scanned; it would be sent literally")
	}
	if strings.Contains(seen, "{{?notSent}}") {
		t.Error("a disabled part's prompt was scanned; the user is asked for a value that goes nowhere")
	}
}

// Every mode the sender knows about must be a mode the scanner knows about.
// A mode present in one switch and absent from the other is the whole failure,
// and it is invisible until someone uses that body type with a prompt.
func TestEverySenderModeIsAScannerMode(t *testing.T) {
	// The modes app_send.go's requestBodySnapshot switches on, plus the
	// aliases NormalizedBodyMode folds into them.
	senderModes := []string{"json", "xml", "text", "sparql", "graphql", "formUrlEncoded", "multipartForm", "file"}

	for _, mode := range senderModes {
		body := types.RequestBody{
			Mode:             mode,
			JSON:             "{{?p}}",
			XML:              "{{?p}}",
			Text:             "{{?p}}",
			GraphQLQuery:     "{{?p}}",
			GraphQLVariables: "{{?p}}",
			FilePath:         "{{?p}}",
			FormURLEncoded:   []types.KeyValue{{Name: "k", Value: "{{?p}}", Enabled: true}},
			Multipart:        []types.FormPart{{Name: "k", Value: "{{?p}}", Enabled: true}},
		}
		if len(scannedFields(body)) == 0 {
			t.Errorf("mode %q is scanned as nothing; a prompt in that body would never be raised", mode)
		}
	}
}

// An unknown mode must scan nothing rather than guessing at a field. The sender
// treats it as text, and asking for prompts from a field the request does not
// carry would block on an irrelevant question.
func TestUnknownModeScansNothing(t *testing.T) {
	for _, mode := range []string{"", "none", "something-new"} {
		body := types.RequestBody{Mode: mode, JSON: "{{?j}}", XML: "{{?x}}"}
		if got := scannedFields(body); len(got) != 0 {
			t.Errorf("mode %q scanned %v", mode, got)
		}
	}
}

func TestScannerHandlesAnEmptyBody(t *testing.T) {
	if got := scannedFields(types.RequestBody{}); len(got) != 0 {
		t.Errorf("an empty body scanned %v", got)
	}
}
