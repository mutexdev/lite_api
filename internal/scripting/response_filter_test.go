// Comparing values in a response filter, and coercing scalars before schema
// validation.
//
// Both of these decide whether something MATCHES, and a comparison that answers
// wrongly is the quietest failure there is: the filter returns no rows, or the
// wrong rows, and looks like the response simply did not contain them.
package scripting

import (
	"encoding/json"
	"testing"
)

func TestJQCompareOrdersNumbers(t *testing.T) {
	for name, tc := range map[string]struct {
		actual   interface{}
		operator string
		expected interface{}
		want     bool
	}{
		"greater":        {10.0, ">", 5.0, true},
		"not greater":    {5.0, ">", 10.0, false},
		"less":           {5.0, "<", 10.0, true},
		"at least equal": {5.0, ">=", 5.0, true},
		"at most equal":  {5.0, "<=", 5.0, true},
		"equal":          {5.0, "=", 5.0, true},
		"double equal":   {5.0, "==", 5.0, true},
		"not equal":      {5.0, "!=", 6.0, true},
		"not equal same": {5.0, "!=", 5.0, false},
	} {
		if got := compareResponseJQValues(tc.actual, tc.operator, tc.expected); got != tc.want {
			t.Errorf("%s: %v %s %v gave %v", name, tc.actual, tc.operator, tc.expected, got)
		}
	}
}

// Numbers reach here as float64 from encoding/json, as json.Number when the
// decoder was told to keep precision, and as plain ints from Go-side callers.
// A type that is not recognised as numeric falls through to string comparison,
// where "10" sorts before "9" — so a "> 9" filter would drop the very rows it
// was written to find.
func TestJQCompareRecognisesEveryNumericType(t *testing.T) {
	for name, actual := range map[string]interface{}{
		"float64":     float64(10),
		"int":         int(10),
		"int64":       int64(10),
		"json.Number": json.Number("10"),
	} {
		if !compareResponseJQValues(actual, ">", 9.0) {
			t.Errorf("%s: 10 > 9 came out false; it was compared as text", name)
		}
	}
	if _, ok := numericInterface(json.Number("not a number")); ok {
		t.Error("an unparseable json.Number reported itself numeric")
	}
	if _, ok := numericInterface("10"); ok {
		t.Error("a string reported itself numeric")
	}
	if _, ok := numericInterface(nil); ok {
		t.Error("nil reported itself numeric")
	}
}

func TestJQCompareFallsBackToTextForEquality(t *testing.T) {
	if !compareResponseJQValues("active", "=", "active") {
		t.Error("equal strings did not match")
	}
	if !compareResponseJQValues("active", "!=", "closed") {
		t.Error("different strings did not report as different")
	}
	if !compareResponseJQValues(true, "=", true) {
		t.Error("booleans compare as text and should still match")
	}
}

// Ordering two strings is not something this filter language does, so it says
// NO rather than inventing a lexicographic answer. A filter like
// status > "active" therefore matches nothing — which is at least consistent,
// where a lexicographic guess would return rows nobody asked for.
func TestJQCompareRefusesToOrderNonNumbers(t *testing.T) {
	for _, operator := range []string{">", "<", ">=", "<="} {
		if compareResponseJQValues("b", operator, "a") {
			t.Errorf("%s ordered two strings", operator)
		}
	}
}

func TestJQCompareRejectsAnUnknownOperator(t *testing.T) {
	if compareResponseJQValues(5.0, "~", 5.0) {
		t.Error("an unknown operator matched numbers")
	}
	if compareResponseJQValues("a", "~", "a") {
		t.Error("an unknown operator matched strings")
	}
}

func TestJQFilterReadsFieldOperatorLiteral(t *testing.T) {
	row := map[string]interface{}{"price": 12.5, "status": "active"}
	for filter, want := range map[string]bool{
		`price > 10`:        true,
		`price > 20`:        false,
		`price >= 12.5`:     true,
		`price <= 12`:       false,
		`status = "active"`: true,
		`status = 'active'`: true,
		`status != "gone"`:  true,
		`status = active`:   true,
	} {
		if got := responseJQMatchesFilter(row, filter); got != want {
			t.Errorf("%s gave %v", filter, got)
		}
	}
}

// ">=" has to be tried before ">", or "price >= 10" splits at the ">" and
// compares against the literal "= 10", which parses as a bare string and
// matches nothing.
func TestJQFilterPrefersTwoCharacterOperators(t *testing.T) {
	row := map[string]interface{}{"n": 10.0}
	for _, filter := range []string{"n >= 10", "n <= 10", "n == 10"} {
		if !responseJQMatchesFilter(row, filter) {
			t.Errorf("%s did not match; the operator was split at its first character", filter)
		}
	}
	if responseJQMatchesFilter(row, "n != 10") {
		t.Error("!= matched an equal value")
	}
}

func TestJQFilterMissesFieldsAndNonObjects(t *testing.T) {
	if responseJQMatchesFilter(map[string]interface{}{"a": 1.0}, "b > 0") {
		t.Error("a filter on an absent field matched")
	}
	// != is the case that distinguishes "the field is missing" from "the field
	// holds something else". Comparing a missing field as nil makes
	// `status != "gone"` match every row that has no status at all, so a filter
	// written to exclude rows starts including ones it never described.
	if responseJQMatchesFilter(map[string]interface{}{"a": 1.0}, `b != "gone"`) {
		t.Error("an absent field satisfied a != filter")
	}
	if responseJQMatchesFilter(map[string]interface{}{"a": 1.0}, "b = 0") {
		t.Error("an absent field satisfied an = filter")
	}
	if responseJQMatchesFilter([]interface{}{1, 2}, "a > 0") {
		t.Error("a filter matched a non-object")
	}
	if responseJQMatchesFilter(map[string]interface{}{"a": 1.0}, "a") {
		t.Error("a filter with no operator matched")
	}
}

func TestJQLiteralsKeepTheirType(t *testing.T) {
	if got := parseResponseJQLiteral(`"10"`); got != "10" {
		t.Errorf(`"10" became %#v; a quoted number is text`, got)
	}
	if got := parseResponseJQLiteral(`10`); got != 10.0 {
		t.Errorf("10 became %#v", got)
	}
	if got := parseResponseJQLiteral(`true`); got != true {
		t.Errorf("true became %#v", got)
	}
	if got := parseResponseJQLiteral(`FALSE`); got != false {
		t.Errorf("FALSE became %#v", got)
	}
	if got := parseResponseJQLiteral(`  active  `); got != "active" {
		t.Errorf("bare text became %#v", got)
	}
}

// A quoted number must stay text, or `id = "10"` would match a numeric id of 10
// — which is exactly the distinction someone writing the quotes is drawing.
func TestJQQuotedNumberDoesNotMatchANumericField(t *testing.T) {
	if !responseJQMatchesFilter(map[string]interface{}{"id": "10"}, `id = "10"`) {
		t.Error("a quoted number did not match a string field")
	}
}

// Schema validation runs against values that came from form fields and
// environment variables, which are always strings. Without coercion, "5"
// against a schema of type integer fails every time and the user is told their
// perfectly good input is the wrong type.
func TestSchemaCoercionParsesStringsIntoTheDeclaredType(t *testing.T) {
	if got := coerceJSONSchemaScalar("5", "integer"); got != float64(5) {
		t.Errorf("integer: got %#v", got)
	}
	if got := coerceJSONSchemaScalar(" 5 ", "integer"); got != float64(5) {
		t.Errorf("integer with spaces: got %#v", got)
	}
	if got := coerceJSONSchemaScalar("5.5", "number"); got != 5.5 {
		t.Errorf("number: got %#v", got)
	}
	for _, text := range []string{"true", "1", "TRUE"} {
		if got := coerceJSONSchemaScalar(text, "boolean"); got != true {
			t.Errorf("boolean %q: got %#v", text, got)
		}
	}
}

// A value that does not parse is returned UNCHANGED so the validator still
// rejects it. Substituting a zero would report the input as valid and send 0 to
// the server.
func TestSchemaCoercionLeavesUnparseableValuesAlone(t *testing.T) {
	for _, tc := range []struct{ value, schemaType string }{
		{"5.5", "integer"},
		{"abc", "integer"},
		{"abc", "number"},
		{"maybe", "boolean"},
		{"", "integer"},
	} {
		if got := coerceJSONSchemaScalar(tc.value, tc.schemaType); got != tc.value {
			t.Errorf("%q as %s became %#v; the validator can no longer reject it", tc.value, tc.schemaType, got)
		}
	}
}

// Going the other way, a number bound for a string field must render as a human
// wrote it. Go's default float formatting gives "5" for 5.0 but switches to
// scientific notation for large values, which would send "1e+21" as a string.
func TestSchemaCoercionFormatsNumbersAsPlainText(t *testing.T) {
	if got := coerceJSONSchemaScalar(float64(5), "string"); got != "5" {
		t.Errorf("got %#v, want no trailing zeros", got)
	}
	if got := coerceJSONSchemaScalar(5.25, "string"); got != "5.25" {
		t.Errorf("got %#v", got)
	}
	if got := coerceJSONSchemaScalar(1e21, "string"); got != "1000000000000000000000" {
		t.Errorf("got %#v, want plain digits rather than scientific notation", got)
	}
	if got := coerceJSONSchemaScalar(true, "string"); got != "true" {
		t.Errorf("got %#v", got)
	}
}

// Anything already of the right type, or of a type with no conversion, passes
// through untouched — coercion is a repair, not a rewrite.
func TestSchemaCoercionIsAPassThroughOtherwise(t *testing.T) {
	if got := coerceJSONSchemaScalar(float64(5), "integer"); got != float64(5) {
		t.Errorf("got %#v", got)
	}
	if got := coerceJSONSchemaScalar("text", "string"); got != "text" {
		t.Errorf("got %#v", got)
	}
	if got := coerceJSONSchemaScalar(nil, "string"); got != nil {
		t.Errorf("got %#v", got)
	}
	if got := coerceJSONSchemaScalar("5", "array"); got != "5" {
		t.Errorf("unknown schema type: got %#v", got)
	}
}
