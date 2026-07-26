// Reading assertions out of a .bru file.
//
// Coverage found parseBruAssertions and bruAssertionOperator at 0%. They sit on
// a contract nothing checks: whatever operator this parser emits has to be one
// the evaluator understands, or the assertion silently evaluates FALSE.
//
// That is a real user-visible path — a Bruno collection imported into LiteAPI
// carries assertions written in Bruno's vocabulary, and an operator that
// survives the parse but means nothing to the evaluator turns into a test the
// user wrote that always fails, with no indication it was never understood.
package bru

import (
	"testing"

	"LiteAPI/internal/types"
)

// The operators the evaluator (scripting.CompareAssertion) accepts. Duplicated
// here on purpose rather than imported: bru must not depend on scripting, and a
// copy that drifts is exactly what the last test below is for.
var evaluatorOperators = map[string]bool{
	"equals": true, "==": true,
	"notEquals": true, "!=": true,
	"contains":   true,
	"startsWith": true,
	"endsWith":   true,
}

func TestBruAssertionOperatorTranslatesBrunoShorthand(t *testing.T) {
	for input, want := range map[string]string{
		"eq":  "equals",
		"neq": "notEquals",
		// Already in the evaluator's vocabulary, so passed through untouched.
		"contains":   "contains",
		"startsWith": "startsWith",
		"endsWith":   "endsWith",
	} {
		if got := bruAssertionOperator(input); got != want {
			t.Errorf("bruAssertionOperator(%q) = %q, want %q", input, got, want)
		}
	}
}

// The contract. Every operator this parser can emit for a shorthand it claims
// to translate must be one the evaluator understands; otherwise the translation
// is decorative and the assertion always fails.
func TestTranslatedOperatorsAreUnderstoodByTheEvaluator(t *testing.T) {
	for _, shorthand := range []string{"eq", "neq", "contains", "startsWith", "endsWith"} {
		got := bruAssertionOperator(shorthand)
		if !evaluatorOperators[got] {
			t.Errorf("bruAssertionOperator(%q) = %q, which the evaluator does not accept — the assertion would always fail", shorthand, got)
		}
	}
}

func TestParseBruAssertionsReadsExpressionOperatorAndValue(t *testing.T) {
	got := parseBruAssertions([]string{
		"  res.status: eq 200",
		"  res.body: contains hello world",
		"  res.headers.content-type: neq text/plain",
	})
	if len(got) != 3 {
		t.Fatalf("got %d assertions, want 3: %+v", len(got), got)
	}
	want := []types.Assertion{
		{Expression: "res.status", Operator: "equals", Value: "200", Enabled: true},
		{Expression: "res.body", Operator: "contains", Value: "hello world", Enabled: true},
		{Expression: "res.headers.content-type", Operator: "notEquals", Value: "text/plain", Enabled: true},
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("assertion %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// A multi-word value must survive intact. Splitting on whitespace and keeping
// only the first token would turn `contains hello world` into `contains hello`,
// which passes on a response the user meant to reject.
func TestParseBruAssertionsKeepsMultiWordValues(t *testing.T) {
	got := parseBruAssertions([]string{"  res.body: contains the quick brown fox"})
	if len(got) != 1 {
		t.Fatalf("got %d assertions", len(got))
	}
	if got[0].Value != "the quick brown fox" {
		t.Fatalf("value = %q, want the whole remainder of the line", got[0].Value)
	}
}

func TestParseBruAssertionsSkipsMalformedLines(t *testing.T) {
	got := parseBruAssertions([]string{
		"no colon here",
		"  res.status:",    // no operator or value
		"  res.status: eq", // operator but no value
		"",
		"  res.status: eq 200", // the only valid one
	})
	if len(got) != 1 {
		t.Fatalf("got %d assertions, want 1: %+v", len(got), got)
	}
	if got[0].Expression != "res.status" || got[0].Value != "200" {
		t.Fatalf("got %+v", got[0])
	}
}

// Parsed assertions arrive enabled. A row that loaded disabled would silently
// not run, and the user's collection would report fewer checks than it contains.
func TestParsedAssertionsAreEnabled(t *testing.T) {
	got := parseBruAssertions([]string{"  res.status: eq 200"})
	if len(got) != 1 || !got[0].Enabled {
		t.Fatalf("parsed assertion must be enabled: %+v", got)
	}
}

// Loading multipart form parts back out of a saved request.
//
// Coverage found ParseYAMLMultipart at 0%. The interesting bit is that "type:
// file" changes which FIELD the saved value lands in: a file part carries a
// FilePath, a text part carries a Value. Read the wrong one and the request
// still has a part with the right name, but it sends the path as literal text
// or sends nothing at all.
func TestParseYAMLMultipartSeparatesFilePartsFromTextParts(t *testing.T) {
	got := ParseYAMLMultipart([]interface{}{
		map[string]interface{}{"name": "doc", "type": "file", "filePath": "/home/ada/report.pdf", "contentType": "application/pdf"},
		map[string]interface{}{"name": "title", "value": "Quarterly"},
	})

	if len(got) != 2 {
		t.Fatalf("got %d parts, want 2: %+v", len(got), got)
	}
	if got[0].FilePath != "/home/ada/report.pdf" {
		t.Errorf("file part lost its path: %+v", got[0])
	}
	if got[0].Value != "" {
		t.Errorf("a file part must not also carry a text value: %q", got[0].Value)
	}
	if got[0].ContentType != "application/pdf" {
		t.Errorf("content type lost: %q", got[0].ContentType)
	}
	if got[1].Value != "Quarterly" {
		t.Errorf("text part lost its value: %+v", got[1])
	}
	if got[1].FilePath != "" {
		t.Errorf("a text part must not carry a file path: %q", got[1].FilePath)
	}
}

// A file part may store its path under any of three keys depending on which
// tool wrote the file. Missing one loads a part that points at nothing.
func TestParseYAMLMultipartAcceptsEveryFilePathKey(t *testing.T) {
	for _, key := range []string{"filePath", "path", "value"} {
		got := ParseYAMLMultipart([]interface{}{
			map[string]interface{}{"name": "doc", "type": "file", key: "/tmp/x.bin"},
		})
		if len(got) != 1 || got[0].FilePath != "/tmp/x.bin" {
			t.Errorf("key %q was not read as a file path: %+v", key, got)
		}
	}
}

func TestParseYAMLMultipartSkipsUnnamedAndMalformedRows(t *testing.T) {
	got := ParseYAMLMultipart([]interface{}{
		"not a map",
		map[string]interface{}{"value": "no name"},
		map[string]interface{}{"name": "  "},
		map[string]interface{}{"name": "kept", "value": "v"},
	})
	if len(got) != 1 || got[0].Name != "kept" {
		t.Fatalf("got %+v; only the named row is usable", got)
	}
}

// Same default as everywhere else in this format: absent means enabled.
func TestParseYAMLMultipartDefaultsToEnabled(t *testing.T) {
	got := ParseYAMLMultipart([]interface{}{map[string]interface{}{"name": "a", "value": "v"}})
	if len(got) != 1 || !got[0].Enabled {
		t.Fatalf("a row with no enabled key must load enabled: %+v", got)
	}
	off := ParseYAMLMultipart([]interface{}{map[string]interface{}{"name": "a", "value": "v", "enabled": false}})
	if len(off) != 1 || off[0].Enabled {
		t.Fatalf("an explicitly disabled row must stay disabled: %+v", off)
	}
}

func TestParseYAMLMultipartRejectsNonLists(t *testing.T) {
	if got := ParseYAMLMultipart("not a list"); got != nil {
		t.Fatalf("got %+v, want nil", got)
	}
}
