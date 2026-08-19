package yamlstore

import (
	"testing"
)

// parseYAMLTypedValue decides whether a variable comes back as the NUMBER 42 or
// the STRING "42". That is not cosmetic: the value is interpolated into request
// bodies, so the wrong one turns {"count":42} into {"count":"42"} — a payload
// many servers reject, and some silently accept as a different thing.
//
// It sat at 45.5% with no test of its own, reached only incidentally through
// environment parsing.

func typed(t *testing.T, raw interface{}) (interface{}, string) {
	t.Helper()
	value, dataType := parseYAMLTypedValue(raw)
	return value, dataType
}

// The explicit form: {type: number, data: 42}. This is what the writer emits,
// so it is the shape almost every stored variable actually has.
func TestTypedNumberParsesAsANumber(t *testing.T) {
	value, dataType := typed(t, map[string]interface{}{"type": "number", "data": "42"})
	if dataType != "number" {
		t.Errorf("dataType = %q, want number", dataType)
	}
	if got, ok := value.(int); !ok || got != 42 {
		t.Errorf("value = %#v, want int 42 — a string here becomes a quoted JSON field", value)
	}
}

// A decimal or exponent must not go through Atoi, which would reject it and
// leave the value as text.
func TestTypedNumberKeepsItsFractionalPart(t *testing.T) {
	for _, raw := range []string{"1.5", "1e3", "2E-2"} {
		value, dataType := typed(t, map[string]interface{}{"type": "number", "data": raw})
		if dataType != "number" {
			t.Errorf("%q: dataType = %q", raw, dataType)
		}
		if _, ok := value.(float64); !ok {
			t.Errorf("%q: value = %#v, want a float64", raw, value)
		}
	}
}

// An integer must stay an INTEGER rather than becoming a float. 42 and 42.0
// serialise differently in JSON, and an id field rendered as 42.0 is not the
// same request.
func TestTypedIntegerDoesNotBecomeAFloat(t *testing.T) {
	value, _ := typed(t, map[string]interface{}{"type": "number", "data": "42"})
	if _, isFloat := value.(float64); isFloat {
		t.Errorf("an integer came back as a float; 42 would serialise as 42.0")
	}
}

// A value declared as a number that cannot be parsed KEEPS THE DECLARED TYPE
// and returns the raw text. Falling back to "string" would silently reclassify
// the variable, so the editor would stop offering the number controls for a
// value the user explicitly typed as one — the fix for a typo would be hidden
// behind changing the type back.
func TestAnUnparseableNumberKeepsItsDeclaredType(t *testing.T) {
	value, dataType := typed(t, map[string]interface{}{"type": "number", "data": "not a number"})
	if dataType != "number" {
		t.Errorf("dataType = %q, want number — the declared type was discarded", dataType)
	}
	if value != "not a number" {
		t.Errorf("value = %#v, want the raw text", value)
	}
}

func TestTypedBooleanParses(t *testing.T) {
	for raw, want := range map[string]bool{"true": true, "false": false, "1": true, "0": false} {
		value, dataType := typed(t, map[string]interface{}{"type": "boolean", "data": raw})
		if dataType != "boolean" {
			t.Errorf("%q: dataType = %q", raw, dataType)
		}
		if got, ok := value.(bool); !ok || got != want {
			t.Errorf("%q: value = %#v, want %v", raw, value, want)
		}
	}
}

func TestAnUnparseableBooleanKeepsItsDeclaredType(t *testing.T) {
	value, dataType := typed(t, map[string]interface{}{"type": "boolean", "data": "maybe"})
	if dataType != "boolean" {
		t.Errorf("dataType = %q, want boolean", dataType)
	}
	if value != "maybe" {
		t.Errorf("value = %#v", value)
	}
}

// An object is kept as TEXT rather than being parsed into a map. The value is
// interpolated into a body as written, so re-serialising it would reorder keys
// and reformat whitespace the user chose.
func TestAnObjectIsKeptAsText(t *testing.T) {
	value, dataType := typed(t, map[string]interface{}{"type": "object", "data": `{"b":1,"a":2}`})
	if dataType != "object" {
		t.Errorf("dataType = %q", dataType)
	}
	if value != `{"b":1,"a":2}` {
		t.Errorf("value = %#v, want the text unchanged and unreordered", value)
	}
}

// A missing type reads as a string. A file written before the type field
// existed has none, and defaulting to anything else would reinterpret every
// variable in it.
func TestAnAbsentTypeReadsAsString(t *testing.T) {
	value, dataType := typed(t, map[string]interface{}{"data": "42"})
	if dataType != "string" {
		t.Errorf("dataType = %q, want string", dataType)
	}
	if value != "42" {
		t.Errorf("value = %#v, want the text", value)
	}
}

// A type this build does not know is PASSED THROUGH rather than collapsed to
// string. A collection written by a newer build keeps its declared type, so
// opening it in an older one and saving does not silently downgrade it.
func TestAnUnknownTypeIsPreserved(t *testing.T) {
	value, dataType := typed(t, map[string]interface{}{"type": "duration", "data": "30s"})
	if dataType != "duration" {
		t.Errorf("dataType = %q, want duration — an unknown type was discarded", dataType)
	}
	if value != "30s" {
		t.Errorf("value = %#v", value)
	}
}

// The type is matched case-insensitively and trimmed, because YAML written by
// hand carries both.
func TestTheDeclaredTypeIsTrimmedAndLowercased(t *testing.T) {
	value, dataType := typed(t, map[string]interface{}{"type": "  NUMBER  ", "data": "7"})
	if dataType != "number" {
		t.Errorf("dataType = %q, want number", dataType)
	}
	if got, ok := value.(int); !ok || got != 7 {
		t.Errorf("value = %#v, want int 7", value)
	}
}

// The bare form: a scalar with no wrapping map, which is what hand-written YAML
// produces. Its type comes from the YAML scalar itself.
func TestABareScalarTakesItsTypeFromYAML(t *testing.T) {
	if value, dataType := typed(t, true); dataType != "boolean" || value != true {
		t.Errorf("bare bool: %#v/%q", value, dataType)
	}
	if value, dataType := typed(t, 42); dataType != "number" || value != 42 {
		t.Errorf("bare int: %#v/%q", value, dataType)
	}
	if value, dataType := typed(t, 1.5); dataType != "number" || value != 1.5 {
		t.Errorf("bare float: %#v/%q", value, dataType)
	}
	if value, dataType := typed(t, "text"); dataType != "string" || value != "text" {
		t.Errorf("bare string: %#v/%q", value, dataType)
	}
}

// A null value becomes an EMPTY STRING, not the text "null" and not a nil that
// would panic on interpolation. An unset variable in YAML is written as an
// empty key, and it must read back as empty.
func TestANullValueReadsAsAnEmptyString(t *testing.T) {
	value, dataType := typed(t, nil)
	if dataType != "string" {
		t.Errorf("dataType = %q, want string", dataType)
	}
	if value != "" {
		t.Errorf("value = %#v, want an empty string — %q would be interpolated literally", value, value)
	}
}
