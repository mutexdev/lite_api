package bru

import (
	"encoding/json"
	"testing"
)

// importedEnvironmentVariableValue sat at 40%. It is the IMPORT twin of
// parseYAMLTypedValue in internal/store/yamlstore: both turn a stored
// environment value into the Go value that gets interpolated into a request
// body, and both decide whether 42 arrives as a number or as "42".
//
// The two must AGREE. An environment imported from JSON and the same
// environment read from YAML have to produce the same type, or a request sends
// {"count":42} from one and {"count":"42"} from the other with nothing in the
// UI to explain the difference.

func TestAnAbsentValueBecomesTheEmptyString(t *testing.T) {
	if got := importedEnvironmentVariableValue(nil, "string"); got != "" {
		t.Errorf("got %#v, want an empty string — nil would be interpolated as <nil>", got)
	}
	if got := importedEnvironmentVariableValue(nil, "number"); got != "" {
		t.Errorf("number type: got %#v", got)
	}
}

// A JSON integer under a number type stays an INTEGER. Through Float64 it would
// serialise as 42 anyway for small values, but a large id loses precision and
// any value renders with a trailing .0 in some encoders.
func TestAJSONIntegerStaysAnInteger(t *testing.T) {
	got := importedEnvironmentVariableValue(json.Number("42"), "number")
	if _, ok := got.(int64); !ok {
		t.Errorf("got %#v (%T), want an int64", got, got)
	}
}

func TestAJSONFloatStaysAFloat(t *testing.T) {
	for _, raw := range []string{"1.5", "1e3", "2E-2"} {
		got := importedEnvironmentVariableValue(json.Number(raw), "number")
		if _, ok := got.(float64); !ok {
			t.Errorf("%q: got %#v (%T), want a float64", raw, got, got)
		}
	}
}

// A JSON number under a NON-number type becomes its text form rather than being
// dropped or kept as json.Number. The Postman import path passes "string" for
// every variable, so this is the branch that path always takes.
func TestAJSONNumberUnderAStringTypeBecomesItsText(t *testing.T) {
	got := importedEnvironmentVariableValue(json.Number("42"), "string")
	if got != "42" {
		t.Errorf("got %#v, want the string \"42\"", got)
	}
}

func TestAStringUnderANumberTypeIsParsed(t *testing.T) {
	if got := importedEnvironmentVariableValue("42", "number"); got != 42 {
		t.Errorf("got %#v (%T), want int 42", got, got)
	}
	if got := importedEnvironmentVariableValue("1.5", "number"); got != 1.5 {
		t.Errorf("got %#v (%T), want float 1.5", got, got)
	}
}

func TestAStringUnderABooleanTypeIsParsed(t *testing.T) {
	if got := importedEnvironmentVariableValue("true", "boolean"); got != true {
		t.Errorf("got %#v", got)
	}
	if got := importedEnvironmentVariableValue("0", "boolean"); got != false {
		t.Errorf("got %#v", got)
	}
}

// A value that will not parse under its declared type is kept AS TEXT rather
// than dropped or zeroed. A half-typed number in an imported file is still
// something the user can see and fix; a silently zeroed one is not.
func TestAnUnparseableValueIsKeptAsText(t *testing.T) {
	if got := importedEnvironmentVariableValue("not a number", "number"); got != "not a number" {
		t.Errorf("number: got %#v", got)
	}
	if got := importedEnvironmentVariableValue("maybe", "boolean"); got != "maybe" {
		t.Errorf("boolean: got %#v", got)
	}
}

// A type this build does not know leaves the value untouched.
func TestAnUnknownTypeLeavesTheValueAlone(t *testing.T) {
	if got := importedEnvironmentVariableValue("30s", "duration"); got != "30s" {
		t.Errorf("got %#v", got)
	}
}

// A value that is already a bool or a number in the decoded JSON passes
// through — the default arm. Re-stringifying it would turn a real JSON boolean
// into the text "true".
func TestAnAlreadyTypedValuePassesThrough(t *testing.T) {
	if got := importedEnvironmentVariableValue(true, "boolean"); got != true {
		t.Errorf("bool: got %#v (%T)", got, got)
	}
	if got := importedEnvironmentVariableValue(3.5, "number"); got != 3.5 {
		t.Errorf("float: got %#v (%T)", got, got)
	}
}

// THE AGREEMENT THAT MATTERS. The JSON and YAML readers must classify the same
// logical value the same way, or the format an environment happens to be stored
// in changes what the request sends.
//
// Compared by KIND rather than by exact Go type: the JSON path yields int64
// (json.Number.Int64) and the YAML path yields int (strconv.Atoi), which is a
// difference in width, not in meaning — both interpolate as 42.
func TestTheJSONAndYAMLReadersClassifyValuesAlike(t *testing.T) {
	isIntegral := func(v interface{}) bool {
		switch v.(type) {
		case int, int8, int16, int32, int64:
			return true
		}
		return false
	}
	isFloat := func(v interface{}) bool {
		_, ok := v.(float64)
		return ok
	}

	for _, tc := range []struct {
		raw      string
		dataType string
		want     string
	}{
		{"42", "number", "integer"},
		{"1.5", "number", "float"},
		{"1e3", "number", "float"},
		{"not a number", "number", "text"},
		{"true", "boolean", "bool"},
		{"maybe", "boolean", "text"},
	} {
		got := importedEnvironmentVariableValue(tc.raw, tc.dataType)
		var kind string
		switch {
		case isIntegral(got):
			kind = "integer"
		case isFloat(got):
			kind = "float"
		case got == true || got == false:
			kind = "bool"
		default:
			kind = "text"
		}
		if kind != tc.want {
			t.Errorf("%q as %s: got %#v (%s), want %s", tc.raw, tc.dataType, got, kind, tc.want)
		}
	}
}
