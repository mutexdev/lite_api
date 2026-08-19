// JSON Schema type matching, found at 0% by the coverage sweep.
//
// This decides whether a value satisfies a schema's declared type, which is what
// backs pm.response.to.have.jsonSchema and the ajv-style validation the script
// API exposes. Getting it wrong makes a schema assertion report the opposite of
// the truth — the same family of failure as the assertion operators, where
// endsWith could behave as startsWith with nothing noticing.
//
// The subtle case is "integer". JSON has one numeric type, so a decoded
// document hands every number over as float64 and "integer" has to be decided
// on the VALUE, not the Go type. A matcher that answered on type alone would
// call 1.5 an integer.
package scripting

import "testing"

func TestJSONSchemaTypeMatches(t *testing.T) {
	for name, tc := range map[string]struct {
		value      interface{}
		schemaType string
		want       bool
	}{
		"null matches null":     {nil, "null", true},
		"string is not null":    {"", "null", false},
		"bool matches boolean":  {true, "boolean", true},
		"string is not boolean": {"true", "boolean", false},
		"string matches string": {"hi", "string", true},
		"number is not string":  {float64(1), "string", false},
		"float matches number":  {float64(1.5), "number", true},
		"string is not number":  {"1", "number", false},
		"object matches object": {map[string]interface{}{}, "object", true},
		"array is not object":   {[]interface{}{}, "object", false},
		"array matches array":   {[]interface{}{}, "array", true},
		"object is not array":   {map[string]interface{}{}, "array", false},

		// The one that matters: JSON has a single numeric type, so integer-ness
		// is a property of the value.
		"whole float matches integer":    {float64(3), "integer", true},
		"negative whole matches integer": {float64(-7), "integer", true},
		"zero matches integer":           {float64(0), "integer", true},
		"fractional is not integer":      {float64(1.5), "integer", false},
		"string is not integer":          {"3", "integer", false},

		// An unknown type must NOT match. Defaulting to true would make a typo
		// in a schema ("strng") validate everything it was meant to constrain.
		"unknown schema type": {"anything", "strng", false},
		"empty schema type":   {"anything", "", false},
	} {
		if got := jsonSchemaTypeMatches(tc.value, tc.schemaType); got != tc.want {
			t.Errorf("%s: jsonSchemaTypeMatches(%#v, %q) = %v, want %v", name, tc.value, tc.schemaType, got, tc.want)
		}
	}
}

// A Go int is not what a decoded JSON document produces, and the matcher is
// documented against decoded documents. Pinning the current answer so that a
// future change to accept int is a deliberate one rather than an accident.
func TestJSONSchemaIntegerIsDecidedOnDecodedJSONTypes(t *testing.T) {
	if jsonSchemaTypeMatches(3, "integer") {
		t.Error("a Go int matched \"integer\"; decoded JSON yields float64, so accepting int would hide a decoding mistake")
	}
	if !jsonSchemaTypeMatches(float64(3), "integer") {
		t.Error("a whole float64 must match \"integer\" — that is what decoded JSON gives")
	}
}
