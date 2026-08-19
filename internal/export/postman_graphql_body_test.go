// Exporting a GraphQL body to Postman.
//
// The bug: the export keyed off `item.Type == "graphql"`, while everything else
// in the app keys off `item.Body.Mode`. The two disagree by the most ordinary
// route there is — create an HTTP request, pick "graphql" in the Body mode
// dropdown, which sets the mode and leaves the type alone. Such a request sends
// correctly and round-trips through the .bru store with its query intact, and
// then exports as:
//
//	"body": null
//
// A valid collection, importable anywhere, with the query and the variables
// gone. Nothing fails; the loss is only visible to whoever opens the export
// looking for a query that is no longer in it.
package export

import (
	"testing"

	"github.com/mutexdev/lite_api/internal/types"
)

func graphQLItem(itemType string) types.RequestItem {
	return types.RequestItem{
		Name:   "Heroes",
		Type:   itemType,
		Method: "POST",
		URL:    "https://api.example.test/graphql",
		Body: types.RequestBody{
			Mode:             "graphql",
			GraphQLQuery:     "query Heroes { heroes { id name } }",
			GraphQLVariables: `{"limit":5}`,
		},
	}
}

// The type is what the Postman importer sets, so imported requests were never
// affected — which is exactly why this survived.
func TestGraphQLBodyExportsForAGraphQLTypedRequest(t *testing.T) {
	assertGraphQLBody(t, sharePostmanBody(graphQLItem("graphql")))
}

// THE REPORTED CASE. An HTTP request whose body mode is graphql.
func TestGraphQLBodyExportsForAnHTTPTypedRequest(t *testing.T) {
	assertGraphQLBody(t, sharePostmanBody(graphQLItem("http")))
}

// A request created before the type field carried anything. Same body, same
// export.
func TestGraphQLBodyExportsForAnUntypedRequest(t *testing.T) {
	assertGraphQLBody(t, sharePostmanBody(graphQLItem("")))
}

// The type still wins when it is the only thing set — a graphql-typed request
// whose mode was never written. store/bru resolves this the same way, and the
// two must not disagree about the same file.
func TestAGraphQLTypedRequestExportsEvenWithNoBodyMode(t *testing.T) {
	item := graphQLItem("graphql")
	item.Body.Mode = ""
	assertGraphQLBody(t, sharePostmanBody(item))
}

func assertGraphQLBody(t *testing.T, body map[string]interface{}) {
	t.Helper()
	if body == nil {
		t.Fatal(`the graphql body exported as null — the query and variables were dropped`)
	}
	if body["mode"] != "graphql" {
		t.Errorf("mode = %v", body["mode"])
	}
	graphql, ok := body["graphql"].(map[string]interface{})
	if !ok {
		t.Fatalf("graphql payload = %#v", body["graphql"])
	}
	if graphql["query"] != "query Heroes { heroes { id name } }" {
		t.Errorf("query = %v", graphql["query"])
	}
	if graphql["variables"] != `{"limit":5}` {
		t.Errorf("variables = %v", graphql["variables"])
	}
}

// The neighbouring modes still export as themselves. The fix moved the graphql
// branch into the same switch they live in, which is the kind of edit that can
// swallow a case.
func TestTheOtherBodyModesStillExport(t *testing.T) {
	cases := map[string]struct {
		body     types.RequestBody
		wantMode string
	}{
		"json":           {types.RequestBody{Mode: "json", JSON: `{"a":1}`}, "raw"},
		"xml":            {types.RequestBody{Mode: "xml", XML: "<a/>"}, "raw"},
		"text":           {types.RequestBody{Mode: "text", Text: "hello"}, "raw"},
		"formUrlEncoded": {types.RequestBody{Mode: "formUrlEncoded", FormURLEncoded: []types.KeyValue{{Name: "a", Value: "1", Enabled: true}}}, "urlencoded"},
		"multipartForm":  {types.RequestBody{Mode: "multipartForm", Multipart: []types.FormPart{{Name: "a", Value: "1", Enabled: true}}}, "formdata"},
	}
	for name, testCase := range cases {
		item := types.RequestItem{Name: name, Type: "http", Method: "POST", Body: testCase.body}
		exported := sharePostmanBody(item)
		if exported == nil {
			t.Errorf("%s exported as null", name)
			continue
		}
		if exported["mode"] != testCase.wantMode {
			t.Errorf("%s exported with mode %v, want %s", name, exported["mode"], testCase.wantMode)
		}
	}
}

// An empty body still exports as nothing rather than as an empty graphql
// document — "none" must not be dragged into the graphql branch.
func TestAnEmptyBodyExportsAsNothing(t *testing.T) {
	for _, mode := range []string{"", "none"} {
		item := types.RequestItem{Name: "empty", Type: "http", Method: "GET", Body: types.RequestBody{Mode: mode}}
		if exported := sharePostmanBody(item); exported != nil {
			t.Errorf("body mode %q exported as %#v", mode, exported)
		}
	}
}
