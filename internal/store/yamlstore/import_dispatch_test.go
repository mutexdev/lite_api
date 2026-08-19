package yamlstore

import (
	"strings"
	"testing"
)

// ParseImportedGlobalEnvironments sat at 50%. It is the entry point for
// "import an environment file": it decides whether the content is JSON or YAML
// and hands off accordingly. Choosing wrong does not fail loudly — it produces
// an environment with the wrong variables, or none.

func TestEmptyImportContentIsAnError(t *testing.T) {
	for _, content := range []string{"", "   ", "\n\t\n"} {
		if _, err := ParseImportedGlobalEnvironments(content); err == nil {
			t.Errorf("%q was accepted; an empty file must not import as an empty environment", content)
		}
	}
}

// TRAILING CONTENT AFTER VALID JSON IS REJECTED. Without the second Decode, a
// file holding two concatenated objects imports the first and SILENTLY DROPS
// the rest — the user sees an environment appear and has no reason to suspect
// half their file was ignored.
func TestTrailingContentAfterJSONIsRejected(t *testing.T) {
	const twoObjects = `{"name":"first","variables":[]}{"name":"second","variables":[]}`
	_, err := ParseImportedGlobalEnvironments(twoObjects)
	if err == nil {
		t.Fatal("a file with two concatenated JSON objects was accepted")
	}
	if !strings.Contains(err.Error(), "trailing") {
		t.Errorf("error does not name the problem: %v", err)
	}
}

// Content that LOOKS like JSON but does not parse must report a JSON error
// rather than falling through to the YAML reader, whose complaint would name a
// line and column in a document the user did not write.
func TestBrokenJSONReportsAJSONError(t *testing.T) {
	for _, content := range []string{`{"name":`, `[{"a":1}`, `{`} {
		_, err := ParseImportedGlobalEnvironments(content)
		if err == nil {
			t.Errorf("%q was accepted", content)
			continue
		}
		if !strings.Contains(err.Error(), "JSON") {
			t.Errorf("%q produced a non-JSON error: %v", content, err)
		}
	}
}

// A valid JSON environment reaches the JSON reader and comes back populated.
func TestAJSONEnvironmentImports(t *testing.T) {
	const content = `{"name":"staging","variables":[{"name":"host","value":"api.test","enabled":true}]}`
	environments, err := ParseImportedGlobalEnvironments(content)
	if err != nil {
		t.Fatal(err)
	}
	if len(environments) != 1 {
		t.Fatalf("got %d environments", len(environments))
	}
	if environments[0].Name != "staging" {
		t.Errorf("name = %q", environments[0].Name)
	}
	if len(environments[0].Variables) != 1 || environments[0].Variables[0].Name != "host" {
		t.Errorf("variables = %#v", environments[0].Variables)
	}
}

// A Postman export is recognised by its id+values pair and imports through the
// same entry point.
func TestAPostmanEnvironmentImports(t *testing.T) {
	const content = `{"id":"abc","name":"prod","values":[{"key":"host","value":"api.test","enabled":true}]}`
	environments, err := ParseImportedGlobalEnvironments(content)
	if err != nil {
		t.Fatal(err)
	}
	if len(environments) != 1 || len(environments[0].Variables) != 1 {
		t.Fatalf("got %#v", environments)
	}
	if environments[0].Variables[0].Name != "host" {
		t.Errorf("variable = %#v", environments[0].Variables[0])
	}
}

// AND THE YAML FALLBACK. Content that is not JSON and does not look like it
// goes to the YAML reader — this is the branch that makes the function a
// dispatcher rather than a JSON parser.
func TestNonJSONContentFallsBackToYAML(t *testing.T) {
	const content = "name: staging\nvariables:\n  - name: host\n    value: api.test\n    enabled: true\n"
	environments, err := ParseImportedGlobalEnvironments(content)
	if err != nil {
		t.Fatalf("the YAML fallback was not taken: %v", err)
	}
	if len(environments) != 1 {
		t.Fatalf("got %d environments", len(environments))
	}
	if len(environments[0].Variables) != 1 || environments[0].Variables[0].Name != "host" {
		t.Errorf("variables = %#v", environments[0].Variables)
	}
}

// A JSON array of environments imports as several.
func TestAJSONArrayImportsEveryEnvironment(t *testing.T) {
	const content = `[{"name":"a","variables":[]},{"name":"b","variables":[]}]`
	environments, err := ParseImportedGlobalEnvironments(content)
	if err != nil {
		t.Fatal(err)
	}
	if len(environments) != 2 {
		t.Fatalf("got %d environments, want 2", len(environments))
	}
}

// UseNumber on the decoder is what keeps an integer an integer all the way
// through. Without it encoding/json decodes every number as float64, the
// json.Number branch in the downstream classifier never runs, and a variable
// stored as 42 imports as a float — which serialises as 42 today and as 42.0
// under some encoders, and loses precision on a large id.
//
// The dispatcher's own tests could not see this until a fixture carried a
// number: removing UseNumber failed nothing.
func TestAnImportedIntegerArrivesAsAnInteger(t *testing.T) {
	// The reader keys the type off "dataType", not "type" — my first fixture
	// used the latter and the value correctly imported as a string.
	const content = `{"name":"staging","variables":[{"name":"port","value":8080,"dataType":"number","enabled":true}]}`
	environments, err := ParseImportedGlobalEnvironments(content)
	if err != nil {
		t.Fatal(err)
	}
	if len(environments) != 1 || len(environments[0].Variables) != 1 {
		t.Fatalf("got %#v", environments)
	}

	value := environments[0].Variables[0].Value
	switch value.(type) {
	case int, int8, int16, int32, int64:
		// as intended
	default:
		t.Errorf("port imported as %#v (%T), want an integer — the decoder is no longer using json.Number", value, value)
	}
}
