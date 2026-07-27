package importers

import (
	"encoding/json"
	"testing"
)

// swagger2PromotePathBody was at 0.0% coverage. Its own comment says dropping a
// path-level body "would silently lose the payload for every operation under
// the path" — and nothing verified that it does not.
//
// A path-level body parameter is legal Swagger 2 and has no OpenAPI 3
// equivalent at that level, so the conversion has to move it somewhere or lose
// it. These tests go through the public converter rather than calling the
// unexported function, because the promotion only matters if the real import
// path reaches it.

func convert(t *testing.T, document string) map[string]interface{} {
	t.Helper()
	out, err := ConvertSwagger2ToOpenAPI3(document)
	if err != nil {
		t.Fatalf("ConvertSwagger2ToOpenAPI3: %v", err)
	}
	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("result is not JSON: %v\n%s", err, out)
	}
	return parsed
}

func operation(t *testing.T, converted map[string]interface{}, path, method string) map[string]interface{} {
	t.Helper()
	paths, ok := converted["paths"].(map[string]interface{})
	if !ok {
		t.Fatalf("no paths in %#v", converted)
	}
	item, ok := paths[path].(map[string]interface{})
	if !ok {
		t.Fatalf("no path %q", path)
	}
	op, ok := item[method].(map[string]interface{})
	if !ok {
		t.Fatalf("no %s on %q", method, path)
	}
	return op
}

const pathLevelBody = `{
  "swagger": "2.0",
  "info": {"title": "t", "version": "1"},
  "consumes": ["application/json"],
  "paths": {
    "/things": {
      "parameters": [
        {"name": "payload", "in": "body", "required": true,
         "schema": {"type": "object", "properties": {"id": {"type": "string"}}}}
      ],
      "post": {"responses": {"200": {"description": "ok"}}},
      "put":  {"responses": {"200": {"description": "ok"}}}
    }
  }
}`

// THE PROPERTY THE FUNCTION EXISTS FOR. Without the promotion both operations
// convert with no requestBody at all, and the imported requests send nothing.
func TestPathLevelBodyReachesEveryOperationUnderThePath(t *testing.T) {
	converted := convert(t, pathLevelBody)
	for _, method := range []string{"post", "put"} {
		op := operation(t, converted, "/things", method)
		if _, ok := op["requestBody"]; !ok {
			t.Errorf("%s lost the path-level body; the imported request would send nothing", method)
		}
	}
}

// An operation's own body is the more specific declaration and must win.
// Overwriting it would replace a documented per-operation payload with the
// path's generic one.
func TestAnOperationsOwnBodyIsNotOverwritten(t *testing.T) {
	const document = `{
      "swagger": "2.0",
      "info": {"title": "t", "version": "1"},
      "consumes": ["application/json"],
      "paths": {
        "/things": {
          "parameters": [
            {"name": "shared", "in": "body",
             "schema": {"type": "object", "properties": {"fromPath": {"type": "string"}}}}
          ],
          "post": {
            "parameters": [
              {"name": "own", "in": "body",
               "schema": {"type": "object", "properties": {"fromOperation": {"type": "string"}}}}
            ],
            "responses": {"200": {"description": "ok"}}
          },
          "put": {"responses": {"200": {"description": "ok"}}}
        }
      }
    }`
	converted := convert(t, document)

	post := operation(t, converted, "/things", "post")
	body, err := json.Marshal(post["requestBody"])
	if err != nil {
		t.Fatal(err)
	}
	if !contains(string(body), "fromOperation") {
		t.Errorf("the operation's own body was replaced by the path's: %s", body)
	}

	// And the operation that declared none still receives the path's.
	put := operation(t, converted, "/things", "put")
	if _, ok := put["requestBody"]; !ok {
		t.Error("put declared no body of its own and did not inherit the path's")
	}
}

// Only HTTP methods are operations. A path item also holds vendor extensions,
// which are OBJECTS — attaching a requestBody to one produces a document that no
// longer validates, and an importer reading it back would find a phantom
// operation.
//
// The fixture needs an extension with an OBJECT value. My first version relied
// on "parameters", which is an ARRAY: the type assertion skips it in the code
// AND in the test, so removing the method check failed nothing.
func TestOnlyHTTPMethodsReceiveThePromotedBody(t *testing.T) {
	const document = `{
      "swagger": "2.0",
      "info": {"title": "t", "version": "1"},
      "consumes": ["application/json"],
      "paths": {
        "/things": {
          "x-metadata": {"owner": "team-a"},
          "parameters": [
            {"name": "payload", "in": "body", "schema": {"type": "object"}}
          ],
          "post": {"responses": {"200": {"description": "ok"}}}
        }
      }
    }`
	converted := convert(t, document)
	paths := converted["paths"].(map[string]interface{})
	item := paths["/things"].(map[string]interface{})

	if _, ok := operation(t, converted, "/things", "post")["requestBody"]; !ok {
		t.Fatal("the real operation did not receive the body")
	}

	extension, ok := item["x-metadata"].(map[string]interface{})
	if !ok {
		t.Fatalf("the vendor extension did not survive conversion: %#v", item["x-metadata"])
	}
	if _, has := extension["requestBody"]; has {
		t.Error("a vendor extension received a requestBody and is now a phantom operation")
	}
}

// A path-level formData parameter is the other half of the same rule: it also
// has no OpenAPI 3 equivalent at path level and is also lost if not promoted.
func TestPathLevelFormDataIsPromotedToo(t *testing.T) {
	const document = `{
      "swagger": "2.0",
      "info": {"title": "t", "version": "1"},
      "consumes": ["application/x-www-form-urlencoded"],
      "paths": {
        "/upload": {
          "parameters": [
            {"name": "field", "in": "formData", "type": "string"}
          ],
          "post": {"responses": {"200": {"description": "ok"}}}
        }
      }
    }`
	op := operation(t, convert(t, document), "/upload", "post")
	if _, ok := op["requestBody"]; !ok {
		t.Error("a path-level formData parameter was dropped")
	}
}

// A path with no body parameter must not gain an empty one, which would import
// as a request that sends an empty payload where it should send none.
func TestAPathWithNoBodyParameterGainsNothing(t *testing.T) {
	const document = `{
      "swagger": "2.0",
      "info": {"title": "t", "version": "1"},
      "paths": {
        "/things": {
          "parameters": [{"name": "q", "in": "query", "type": "string"}],
          "get": {"responses": {"200": {"description": "ok"}}}
        }
      }
    }`
	op := operation(t, convert(t, document), "/things", "get")
	if _, ok := op["requestBody"]; ok {
		t.Error("a path with only a query parameter produced a requestBody")
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}

// The nil guard inside swagger2PromotePathBody is unreachable through the
// converter, because the one call site performs the same check three lines
// before calling it. It is still the function's own contract — it is a distinct
// unit that must be safe to call with nothing to promote — so it is exercised
// directly rather than left as a branch that looks untested.
func TestPromotingNothingLeavesTheOperationsAlone(t *testing.T) {
	pathItem := map[string]interface{}{
		"post":       map[string]interface{}{"responses": map[string]interface{}{}},
		"parameters": []interface{}{},
	}

	swagger2PromotePathBody(pathItem, nil, nil, []string{"application/json"})

	post := pathItem["post"].(map[string]interface{})
	if _, ok := post["requestBody"]; ok {
		t.Error("an empty promotion added a requestBody, which would import as a request sending an empty payload")
	}
}
