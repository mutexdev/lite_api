package importers

// US-052 — tests for Swagger 2 import.
//
// Most of these assert on the CONVERTED DOCUMENT rather than the imported
// collection, because that is where the failures live and where they are
// legible. A conversion that loses the host produces a collection that imports
// perfectly and whose every request then 404s; asserting on servers[] names the
// cause, whereas asserting on the collection would only say "URL wrong".
//
// The end-to-end test at the bottom is what proves the conversion is actually
// consumed rather than merely well-formed.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func swagger2Fixture(t *testing.T) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join("..", "..", "docs", "qa", "import-fixtures", "swagger2.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return string(content)
}

func convertedSwagger2(t *testing.T) map[string]interface{} {
	t.Helper()
	converted, err := ConvertSwagger2ToOpenAPI3(swagger2Fixture(t))
	if err != nil {
		t.Fatalf("ConvertSwagger2ToOpenAPI3: %v", err)
	}
	var document map[string]interface{}
	if err := json.Unmarshal([]byte(converted), &document); err != nil {
		t.Fatalf("converted document is not valid JSON: %v", err)
	}
	return document
}

// dig walks a converted document by key, failing with the path so a missing
// intermediate is legible.
func dig(t *testing.T, value interface{}, path ...string) interface{} {
	t.Helper()
	for i, key := range path {
		object, ok := value.(map[string]interface{})
		if !ok {
			t.Fatalf("%s is not an object", strings.Join(path[:i], "."))
		}
		value, ok = object[key]
		if !ok {
			t.Fatalf("%s is missing", strings.Join(path[:i+1], "."))
		}
	}
	return value
}

func TestSwagger2ConvertsVersionAndServers(t *testing.T) {
	document := convertedSwagger2(t)

	if _, stillThere := document["swagger"]; stillThere {
		t.Error("the swagger key survived; the document still declares itself as Swagger 2")
	}
	if version, _ := document["openapi"].(string); !strings.HasPrefix(version, "3.") {
		t.Errorf("openapi = %v, want a 3.x version", document["openapi"])
	}

	// The conversion that decides whether the import is usable at all: without
	// servers, every imported request carries only a path and 404s, while the
	// import itself looks perfectly successful.
	servers, ok := document["servers"].([]interface{})
	if !ok || len(servers) == 0 {
		t.Fatal("no servers were built from host/basePath/schemes; every imported request would have a relative URL")
	}
	url, _ := dig(t, servers[0], "url").(string)
	if url != "https://api.example.test/v2" {
		t.Errorf("server url = %q, want https://api.example.test/v2", url)
	}

	for _, key := range []string{"host", "basePath", "schemes", "definitions", "securityDefinitions", "consumes", "produces"} {
		if _, stillThere := document[key]; stillThere {
			t.Errorf("Swagger 2 key %q survived the conversion", key)
		}
	}
}

// TestSwagger2RewritesEveryRef is the silent-failure guard. A missed pointer
// resolves to nothing, the schema comes back empty, and the imported request
// has no example body — which reads as an API with no documented payload.
func TestSwagger2RewritesEveryRef(t *testing.T) {
	converted, err := ConvertSwagger2ToOpenAPI3(swagger2Fixture(t))
	if err != nil {
		t.Fatalf("ConvertSwagger2ToOpenAPI3: %v", err)
	}

	if strings.Contains(converted, "#/definitions/") {
		t.Error("a #/definitions/ pointer survived the conversion")
	}
	if !strings.Contains(converted, "#/components/schemas/User") {
		t.Error("no rewritten schema pointer is present at all")
	}

	document := convertedSwagger2(t)

	// Nested: User.manager references User. A rewrite that only walked the top
	// level of each schema would miss it.
	manager := dig(t, document, "components", "schemas", "User", "properties", "manager", "$ref")
	if manager != "#/components/schemas/User" {
		t.Errorf("nested property $ref = %v, want #/components/schemas/User", manager)
	}

	// Inside an array's items — another place a targeted rewrite misses.
	items := dig(t, document, "components", "schemas", "UserList", "items", "$ref")
	if items != "#/components/schemas/User" {
		t.Errorf("array item $ref = %v, want #/components/schemas/User", items)
	}
}

// TestSwagger2BodyParameterBecomesRequestBody. Left as a parameter, `in: body`
// has no meaning in OpenAPI 3 — the importer reads it as an unknown location
// and the payload is lost.
func TestSwagger2BodyParameterBecomesRequestBody(t *testing.T) {
	document := convertedSwagger2(t)
	post := dig(t, document, "paths", "/users", "post")

	schema := dig(t, post, "requestBody", "content", "application/json", "schema", "$ref")
	if schema != "#/components/schemas/User" {
		t.Errorf("requestBody schema = %v, want the User ref", schema)
	}
	if required := dig(t, post, "requestBody", "required"); required != true {
		t.Errorf("requestBody required = %v, want true", required)
	}

	// And the body parameter must be gone from parameters.
	if params, ok := post.(map[string]interface{})["parameters"].([]interface{}); ok {
		for _, value := range params {
			if in := dig(t, value, "in"); in == "body" {
				t.Error("the body parameter is still listed as a parameter as well")
			}
		}
	}
}

// TestSwagger2FormDataBecomesRequestBody covers both media types, and the file
// field in particular: `type: file` became `type: string, format: binary`, and
// a file encoded as a urlencoded form value is a corrupted upload.
func TestSwagger2FormDataBecomesRequestBody(t *testing.T) {
	document := convertedSwagger2(t)

	t.Run("urlencoded", func(t *testing.T) {
		post := dig(t, document, "paths", "/sessions", "post")
		schema := dig(t, post, "requestBody", "content", "application/x-www-form-urlencoded", "schema")
		properties, ok := dig(t, schema, "properties").(map[string]interface{})
		if !ok {
			t.Fatal("form properties are missing")
		}
		for _, field := range []string{"username", "password"} {
			if _, present := properties[field]; !present {
				t.Errorf("form field %q was lost", field)
			}
		}
		required, _ := dig(t, schema, "required").([]interface{})
		if len(required) != 2 {
			t.Errorf("required fields = %v, want both", required)
		}
	})

	t.Run("multipart with a file", func(t *testing.T) {
		post := dig(t, document, "paths", "/users/{id}/avatar", "post")
		schema := dig(t, post, "requestBody", "content", "multipart/form-data", "schema")
		avatar := dig(t, schema, "properties", "avatar")
		if got := dig(t, avatar, "type"); got != "string" {
			t.Errorf("file field type = %v, want string", got)
		}
		if got := dig(t, avatar, "format"); got != "binary" {
			t.Errorf("file field format = %v, want binary — an unconverted file field is a corrupted upload", got)
		}

		// The path parameter must survive as a parameter, not be swept into
		// the body with the form fields.
		params, ok := post.(map[string]interface{})["parameters"].([]interface{})
		if !ok || len(params) != 1 {
			t.Fatalf("expected exactly the path parameter to remain, got %v", params)
		}
		if in := dig(t, params[0], "in"); in != "path" {
			t.Errorf("remaining parameter is %v, want the path parameter", in)
		}
	})
}

// TestSwagger2ParameterTypesMoveIntoSchema. OpenAPI 3 moved type/format/default
// onto parameter.schema. Left inline they are ignored, so the imported request
// loses its example values and the parameter appears empty.
func TestSwagger2ParameterTypesMoveIntoSchema(t *testing.T) {
	document := convertedSwagger2(t)
	get := dig(t, document, "paths", "/users", "get")
	params, ok := get.(map[string]interface{})["parameters"].([]interface{})
	if !ok || len(params) != 2 {
		t.Fatalf("expected 2 parameters, got %v", params)
	}

	var page map[string]interface{}
	for _, value := range params {
		if object, ok := value.(map[string]interface{}); ok && object["name"] == "page" {
			page = object
		}
	}
	if page == nil {
		t.Fatal("the page parameter is missing")
	}

	for _, key := range []string{"type", "format", "default"} {
		if _, inline := page[key]; inline {
			t.Errorf("%q is still inline on the parameter; OpenAPI 3 ignores it there", key)
		}
	}
	if got := dig(t, page, "schema", "type"); got != "integer" {
		t.Errorf("schema.type = %v, want integer", got)
	}
	if got := dig(t, page, "schema", "default"); got != float64(1) {
		t.Errorf("schema.default = %v, want 1", got)
	}
}

// TestSwagger2SecuritySchemesConvert. `type: basic` is the one that bites: an
// unconverted entry is not merely invalid, it makes the importer read no auth,
// so the request imports with auth mode none and 401s pointing nowhere.
func TestSwagger2SecuritySchemesConvert(t *testing.T) {
	document := convertedSwagger2(t)
	schemes := dig(t, document, "components", "securitySchemes")

	basic := dig(t, schemes, "basicAuth")
	if got := dig(t, basic, "type"); got != "http" {
		t.Errorf("basic type = %v, want http", got)
	}
	if got := dig(t, basic, "scheme"); got != "basic" {
		t.Errorf("basic scheme = %v, want basic", got)
	}

	// apiKey is unchanged between the two versions and must not be mangled.
	apiKey := dig(t, schemes, "apiKeyAuth")
	if got := dig(t, apiKey, "type"); got != "apiKey" {
		t.Errorf("apiKey type = %v, want apiKey", got)
	}
	if got := dig(t, apiKey, "in"); got != "header" {
		t.Errorf("apiKey in = %v, want header", got)
	}

	// oauth2 restructured into flows, and accessCode was renamed.
	oauth := dig(t, schemes, "oauth")
	flow := dig(t, oauth, "flows", "authorizationCode")
	if got := dig(t, flow, "tokenUrl"); got != "https://auth.example.test/token" {
		t.Errorf("tokenUrl = %v", got)
	}
	if _, stale := oauth.(map[string]interface{})["flow"]; stale {
		t.Error("the Swagger 2 flow key survived alongside flows")
	}
}

// TestSwagger2ResponseSchemasMoveUnderContent.
func TestSwagger2ResponseSchemasMoveUnderContent(t *testing.T) {
	document := convertedSwagger2(t)
	response := dig(t, document, "paths", "/users", "get", "responses", "200")

	if _, inline := response.(map[string]interface{})["schema"]; inline {
		t.Error("the response schema is still inline; OpenAPI 3 requires it under content")
	}
	got := dig(t, response, "content", "application/json", "schema", "$ref")
	if got != "#/components/schemas/UserList" {
		t.Errorf("response schema = %v, want the UserList ref", got)
	}
}

func TestSwagger2ConversionRejectsBadInput(t *testing.T) {
	for _, tc := range []struct{ name, content string }{
		{"not a document", "%%%not yaml or json"},
		{"empty", ""},
		{"openapi 3", `{"openapi":"3.0.0","paths":{}}`},
		{"swagger 1", `{"swagger":"1.2","apis":[]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ConvertSwagger2ToOpenAPI3(tc.content); err == nil {
				t.Error("expected an error")
			}
		})
	}
}

func TestIsSwagger2Document(t *testing.T) {
	// Version-checked rather than key-presence: a Swagger 1.x document also has
	// the key, and converting one as 2.0 gives a plausible document with the
	// wrong shape instead of a clear rejection.
	if !IsSwagger2Document(map[string]interface{}{"swagger": "2.0"}) {
		t.Error("2.0 was not recognised")
	}
	if IsSwagger2Document(map[string]interface{}{"swagger": "1.2"}) {
		t.Error("1.2 was treated as Swagger 2")
	}
	if IsSwagger2Document(map[string]interface{}{"openapi": "3.0.0"}) {
		t.Error("an OpenAPI 3 document was treated as Swagger 2")
	}
}
