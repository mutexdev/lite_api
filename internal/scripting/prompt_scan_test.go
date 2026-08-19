// Finding {{?prompt}} variables in a request body.
//
// scanBodyPromptVariables was at 10.5%. It dispatches on body mode and scans
// the fields that mode actually uses. A mode whose fields go unscanned is not an
// error: the user is never prompted, and the request goes out with the literal
// "{{?token}}" in its body. The server rejects it, or worse accepts it, and
// nothing points at the prompt that never appeared.
//
// Every mode is covered here because the bug this guards against is
// per-mode — one missing case in a switch, invisible to any test that only
// exercises JSON.
package scripting

import (
	"testing"

	"github.com/mutexdev/lite_api/internal/types"
)

// collect runs the scanner and returns every string it looked at.
func collect(body types.RequestBody) []string {
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

func contains(haystack []string, needle string) bool {
	for _, item := range haystack {
		if item == needle {
			return true
		}
	}
	return false
}

func TestPromptScanCoversEveryBodyMode(t *testing.T) {
	for name, tc := range map[string]struct {
		body types.RequestBody
		want string
	}{
		"json":          {types.RequestBody{Mode: "json", JSON: `{"t":"{{?tok}}"}`}, `{"t":"{{?tok}}"}`},
		"xml":           {types.RequestBody{Mode: "xml", XML: `<t>{{?tok}}</t>`}, `<t>{{?tok}}</t>`},
		"text":          {types.RequestBody{Mode: "text", Text: "{{?tok}}"}, "{{?tok}}"},
		"sparql":        {types.RequestBody{Mode: "sparql", Text: "{{?tok}}"}, "{{?tok}}"},
		"graphql query": {types.RequestBody{Mode: "graphql", GraphQLQuery: "query {{?tok}}"}, "query {{?tok}}"},
		"file path":     {types.RequestBody{Mode: "file", FilePath: "/tmp/{{?tok}}"}, "/tmp/{{?tok}}"},
	} {
		if got := collect(tc.body); !contains(got, tc.want) {
			t.Errorf("%s: %q was never scanned; the user would not be prompted and the placeholder would go out literally", name, tc.want)
		}
	}
}

// GraphQL carries prompts in its VARIABLES as often as its query, and scanning
// only the query is an easy omission.
func TestPromptScanCoversGraphQLVariables(t *testing.T) {
	got := collect(types.RequestBody{Mode: "graphql", GraphQLQuery: "query", GraphQLVariables: `{"id":"{{?id}}"}`})
	if !contains(got, `{"id":"{{?id}}"}`) {
		t.Error("graphql variables were not scanned")
	}
}

func TestPromptScanCoversFormURLEncodedRows(t *testing.T) {
	got := collect(types.RequestBody{
		Mode:           "formUrlEncoded",
		FormURLEncoded: []types.KeyValue{{Name: "k", Value: "{{?v}}", Enabled: true}},
	})
	if !contains(got, "{{?v}}") {
		t.Error("form-urlencoded values were not scanned")
	}
}

// Multipart scans four fields per part, and a prompt can live in any of them —
// a filename as readily as a value.
func TestPromptScanCoversEveryMultipartField(t *testing.T) {
	got := collect(types.RequestBody{
		Mode: "multipartForm",
		Multipart: []types.FormPart{{
			Name: "{{?n}}", Value: "{{?v}}", FilePath: "{{?p}}", ContentType: "{{?c}}", Enabled: true,
		}},
	})
	for _, want := range []string{"{{?n}}", "{{?v}}", "{{?p}}", "{{?c}}"} {
		if !contains(got, want) {
			t.Errorf("multipart field %q was not scanned", want)
		}
	}
}

// A DISABLED part is not sent, so prompting for it would ask the user to fill in
// a value that goes nowhere.
func TestPromptScanSkipsDisabledMultipartParts(t *testing.T) {
	got := collect(types.RequestBody{
		Mode:      "multipartForm",
		Multipart: []types.FormPart{{Name: "off", Value: "{{?unused}}", Enabled: false}},
	})
	if contains(got, "{{?unused}}") {
		t.Error("a disabled part was scanned; the user would be prompted for a value that is never sent")
	}
}

// An unknown mode must scan nothing rather than guessing a field — guessing
// would prompt for text the request does not contain.
func TestPromptScanIgnoresUnknownModes(t *testing.T) {
	if got := collect(types.RequestBody{Mode: "none", JSON: `{{?tok}}`}); len(got) != 0 {
		t.Errorf("mode none scanned %v", got)
	}
	if got := collect(types.RequestBody{Mode: "something-new", Text: `{{?tok}}`}); len(got) != 0 {
		t.Errorf("an unknown mode scanned %v", got)
	}
}
