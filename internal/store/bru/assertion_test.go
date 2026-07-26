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
