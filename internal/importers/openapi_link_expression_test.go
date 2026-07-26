// Turning an OpenAPI link's runtime expression into JavaScript.
//
// A link says "the id in this response feeds that request's path parameter",
// and the importer renders it as a bru.setVar line in a post-response script.
// The expression is what the generated JavaScript actually reads.
//
// Getting it wrong is quiet in the worst way. An expression the converter does
// not recognise falls through to a QUOTED STRING LITERAL, so the generated
// script becomes
//
//	bru.setVar("getUser_id", "$response.body#/id");
//
// which runs without error and sets the variable to the text of the expression.
// The next request then asks for /users/$response.body%23%2Fid and 404s, with
// nothing connecting that to the import.
package importers

import "testing"

func TestOpenAPIRuntimeExpressionsBecomeRealAccessors(t *testing.T) {
	for expression, want := range map[string]string{
		"$response.body":         "res.body",
		"$request.body":          "req.body",
		"$statusCode":            "res.status",
		"$method":                "req.method",
		"$url":                   "req.url",
		"$response.body#/id":     `res.json["id"]`,
		"$request.body#/user/id": `req.body["user"]["id"]`,
		"$response.header.X-Id":  `res.getHeader("X-Id")`,
		"$request.header.Accept": `req.getHeader("Accept")`,
	} {
		if got := openAPIRuntimeExpressionToScript(expression); got != want {
			t.Errorf("%s -> %q, want %q", expression, got, want)
		}
	}
}

// The body pointer reads res.JSON, not res.body: res.body is the raw text, and
// indexing a string by "id" yields undefined rather than the field.
func TestOpenAPIResponseBodyPointerReadsParsedJSON(t *testing.T) {
	got := openAPIRuntimeExpressionToScript("$response.body#/id")
	if got == `res.body["id"]` {
		t.Fatal("indexed the raw body text; a string indexed by a field name is undefined")
	}
	if got != `res.json["id"]` {
		t.Errorf("got %q", got)
	}
}

// A numeric pointer segment must index an array position, not a property name:
// arr["0"] happens to work in JavaScript, but "0" as a property of an object is
// a different lookup, and the distinction matters for /items/0/id.
func TestOpenAPIPointerNumbersIndexArrayPositions(t *testing.T) {
	got := openAPIRuntimeExpressionToScript("$response.body#/items/0/id")
	if got != `res.json["items"][0]["id"]` {
		t.Errorf("got %q, want a numeric index for the array position", got)
	}
}

// RFC 6901 escapes: ~1 is a slash, ~0 a tilde. Left unescaped, a field named
// "a/b" reads as two nested lookups and returns undefined.
func TestOpenAPIPointerUnescapesRFC6901(t *testing.T) {
	if got := openAPIRuntimeExpressionToScript("$response.body#/a~1b"); got != `res.json["a/b"]` {
		t.Errorf("~1 gave %q", got)
	}
	if got := openAPIRuntimeExpressionToScript("$response.body#/a~0b"); got != `res.json["a~b"]` {
		t.Errorf("~0 gave %q", got)
	}
}

// The generated line is source code. A quote in a header or field name that is
// not escaped ends the string literal early and the whole script fails to
// parse — taking every other link in the same response down with it.
func TestOpenAPIExpressionsEscapeQuotesInNames(t *testing.T) {
	got := openAPIRuntimeExpressionToScript(`$response.header.X"; bru.setVar("owned`)
	if got != `res.getHeader("X\"; bru.setVar(\"owned")` {
		t.Errorf("got %s, want the quotes escaped so the generated script still parses", got)
	}
	pointer := openAPIRuntimeExpressionToScript(`$response.body#/a"b`)
	if pointer != `res.json["a\"b"]` {
		t.Errorf("pointer gave %s", pointer)
	}
}

// An unrecognised expression is quoted rather than emitted raw. Emitting it raw
// would put "$response.foo" into the script as an identifier and throw a
// ReferenceError, killing the rest of the generated script.
func TestOpenAPIUnknownExpressionBecomesAQuotedLiteral(t *testing.T) {
	if got := openAPIRuntimeExpressionToScript("$response.foo"); got != `"$response.foo"` {
		t.Errorf("got %s, want a quoted literal rather than a bare identifier", got)
	}
	if got := openAPIRuntimeExpressionToScript("literal-value"); got != `"literal-value"` {
		t.Errorf("got %s", got)
	}
}

// A pointer with no leading slash is not a JSON Pointer. Dropping it yields
// res.json — the whole body — which is at least the parsed document rather than
// a fabricated lookup.
func TestOpenAPIMalformedPointersDegradeToTheWholeBody(t *testing.T) {
	for _, expression := range []string{"$response.body#", "$response.body#id", "$response.body#no-slash"} {
		if got := openAPIRuntimeExpressionToScript(expression); got != "res.json" {
			t.Errorf("%s -> %q, want the whole parsed body", expression, got)
		}
	}
}

// Links come out of YAML, so the parameter value is not always a string: it can
// be a byte slice, or the {"data": ...} wrapper some documents use.
func TestOpenAPILinkExpressionAcceptsNonStringShapes(t *testing.T) {
	if got := openAPILinkExpressionToScript("$statusCode"); got != "res.status" {
		t.Errorf("string gave %q", got)
	}
	if got := openAPILinkExpressionToScript([]byte("$statusCode")); got != "res.status" {
		t.Errorf("[]byte gave %q", got)
	}
	if got := openAPILinkExpressionToScript(map[string]interface{}{"data": "$statusCode"}); got != "res.status" {
		t.Errorf("wrapped gave %q", got)
	}
}

// A constant parameter value is legal in a link. It has to reach the script as
// valid JavaScript, since the line it lands in is source code.
func TestOpenAPILinkExpressionEncodesConstantValues(t *testing.T) {
	if got := openAPILinkExpressionToScript(42); got != "42" {
		t.Errorf("number gave %q", got)
	}
	if got := openAPILinkExpressionToScript(true); got != "true" {
		t.Errorf("bool gave %q", got)
	}
	if got := openAPILinkExpressionToScript(map[string]interface{}{"a": 1}); got != `{"a":1}` {
		t.Errorf("object gave %q", got)
	}
}
