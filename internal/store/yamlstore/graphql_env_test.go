// Loading a GraphQL request, and loading an environment file.
//
// Both were at 0%. They are the last two file-loading paths in this package
// with no tests, and file loading fails the same way every time: the request or
// environment appears in the sidebar, looks right, and is missing something the
// user typed.
package yamlstore

import (
	"strings"
	"testing"

	"LiteAPI/internal/types"
)

func TestGraphQLSectionLoadsQueryVariablesAndMode(t *testing.T) {
	item := types.RequestItem{}
	applyYAMLGraphQL(&item, map[string]interface{}{
		"method": "post",
		"url":    "https://api.test/graphql",
		"body": map[string]interface{}{
			"query":     "query Q($id: ID!) { node(id: $id) { id } }",
			"variables": `{"id":"1"}`,
		},
		"headers": []interface{}{
			map[string]interface{}{"name": "Authorization", "value": "Bearer t", "enabled": true},
		},
	})

	if item.Body.Mode != "graphql" {
		t.Errorf("body mode = %q, want graphql — a GraphQL request loaded as something else sends the wrong body", item.Body.Mode)
	}
	if !strings.Contains(item.Body.GraphQLQuery, "node(id: $id)") {
		t.Errorf("query lost: %q", item.Body.GraphQLQuery)
	}
	if item.Body.GraphQLVariables != `{"id":"1"}` {
		t.Errorf("variables lost: %q — the query would run with no arguments", item.Body.GraphQLVariables)
	}
	// Method is upper-cased: "post" in a hand-edited file must still send POST.
	if item.Method != "POST" {
		t.Errorf("method = %q, want POST", item.Method)
	}
	if item.URL != "https://api.test/graphql" {
		t.Errorf("url = %q", item.URL)
	}
	if len(item.Headers) != 1 || item.Headers[0].Name != "Authorization" {
		t.Errorf("headers lost: %+v", item.Headers)
	}
}

// The mode is set unconditionally, before the body is read. A file with no body
// block still has to load AS a GraphQL request, or the editor opens it as raw
// text and the user's query is one save away from being discarded.
func TestGraphQLModeIsSetEvenWithNoBodyBlock(t *testing.T) {
	item := types.RequestItem{}
	applyYAMLGraphQL(&item, map[string]interface{}{"url": "https://api.test/graphql"})
	if item.Body.Mode != "graphql" {
		t.Fatalf("body mode = %q with no body block, want graphql", item.Body.Mode)
	}
}

// Absent keys must not clobber what the item already carries.
func TestGraphQLSectionLeavesAbsentFieldsAlone(t *testing.T) {
	item := types.RequestItem{Method: "PUT", URL: "https://kept.test"}
	applyYAMLGraphQL(&item, map[string]interface{}{})
	if item.Method != "PUT" || item.URL != "https://kept.test" {
		t.Fatalf("absent keys overwrote existing values: %q %q", item.Method, item.URL)
	}
}

func TestEnvironmentContentLoadsNameVariablesAndColor(t *testing.T) {
	got, err := ParseYAMLEnvironmentContent(`
name: Staging
color: "#ff0000"
variables:
  - name: host
    value: https://staging.test
    enabled: true
  - name: token
    value: secret-value
    secret: true
`, "fallback")
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Staging" {
		t.Errorf("name = %q", got.Name)
	}
	if got.Color != "#ff0000" {
		t.Errorf("color = %q", got.Color)
	}
	if len(got.Variables) != 2 {
		t.Fatalf("got %d variables, want 2: %+v", len(got.Variables), got.Variables)
	}
	if got.Variables[0].Name != "host" {
		t.Errorf("first variable = %+v", got.Variables[0])
	}
	if !got.Variables[1].Secret {
		t.Error("secret flag lost — the value would render unmasked in the UI")
	}
}

// The fallback name comes from the FILENAME. An environment file with no name
// key must not load as a blank entry in the picker.
func TestEnvironmentContentUsesTheFallbackName(t *testing.T) {
	got, err := ParseYAMLEnvironmentContent("variables: []\n", "Production")
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Production" {
		t.Fatalf("name = %q, want the fallback %q", got.Name, "Production")
	}
}

// With neither a name nor a fallback the environment still needs a label. This
// pins the literal, which is exactly the string the package extraction
// corrupted into "types.Environment" and shipped.
func TestEnvironmentContentWithNoNameAtAllGetsALiteralLabel(t *testing.T) {
	got, err := ParseYAMLEnvironmentContent("variables: []\n", "")
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Environment" {
		t.Fatalf("name = %q, want %q — a package qualifier here means a rewrite hit a string literal", got.Name, "Environment")
	}
}

func TestEnvironmentContentRejectsMalformedYAML(t *testing.T) {
	if _, err := ParseYAMLEnvironmentContent("name: [unclosed\n", "x"); err == nil {
		t.Fatal("malformed YAML must be an error, not a silently empty environment")
	}
}
