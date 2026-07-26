// Assertion evaluation.
//
// Found by negative control: making endsWith behave as startsWith failed no
// test, so the individual operators were unverified. In an API testing tool
// that is the worst shape of bug available -- a user's assertion reports PASSED
// when the response did not satisfy it, and there is nothing to notice. A tool
// that reports green wrongly is worse than one that crashes.
package main

import "testing"

func TestCompareAssertionOperators(t *testing.T) {
	for _, tc := range []struct {
		actual, operator, expected string
		want                       bool
	}{
		{"200", "equals", "200", true},
		{"200", "equals", "201", false},
		{"200", "==", "200", true},
		{"200", "notEquals", "201", true},
		{"200", "notEquals", "200", false},
		{"200", "!=", "201", true},

		{"hello world", "contains", "lo wo", true},
		{"hello world", "contains", "absent", false},

		// The three that a control proved interchangeable. Each pair below
		// distinguishes one operator from the others: "hello" starts with
		// "hel" but does not end with it, and vice versa.
		{"hello", "startsWith", "hel", true},
		{"hello", "startsWith", "llo", false},
		{"hello", "endsWith", "llo", true},
		{"hello", "endsWith", "hel", false},

		// An unrecognised operator must fail, not pass. Defaulting to true
		// would make a typo in the operator field report success forever.
		{"anything", "matches", "anything", false},
		{"anything", "", "anything", false},
		{"", "greaterThan", "", false},
	} {
		if got := compareAssertion(tc.actual, tc.operator, tc.expected); got != tc.want {
			t.Errorf("compareAssertion(%q, %q, %q) = %v, want %v", tc.actual, tc.operator, tc.expected, got, tc.want)
		}
	}
}

func TestEvaluateAssertionsReadsStatusBodyAndHeaders(t *testing.T) {
	response := Response{
		Status:  201,
		Body:    `{"ok":true}`,
		Headers: map[string]string{"Content-Type": "application/json"},
	}
	assertions := []Assertion{
		{Expression: "res.status", Operator: "equals", Value: "201", Enabled: true},
		{Expression: "res.body", Operator: "contains", Value: `"ok"`, Enabled: true},
		{Expression: "res.headers.Content-Type", Operator: "endsWith", Value: "json", Enabled: true},
		{Expression: "res.status", Operator: "equals", Value: "500", Enabled: true},
	}

	got := evaluateAssertions(assertions, response)
	for i, want := range []bool{true, true, true, false} {
		if got[i].Passed != want {
			t.Errorf("assertion %d (%s %s %q): passed=%v, want %v",
				i, got[i].Expression, got[i].Operator, got[i].Value, got[i].Passed, want)
		}
	}
}

// A disabled assertion is passed through untouched. Marking it passed would
// make a row the user switched off count towards a green run.
func TestEvaluateAssertionsLeavesDisabledRowsAlone(t *testing.T) {
	assertions := []Assertion{
		{Expression: "res.status", Operator: "equals", Value: "999", Enabled: false},
	}
	got := evaluateAssertions(assertions, Response{Status: 200})

	if len(got) != 1 {
		t.Fatalf("a disabled assertion must still be returned, got %d rows", len(got))
	}
	if got[0].Passed {
		t.Error("a disabled assertion must not be marked passed")
	}
	if got[0].Message != "" {
		t.Errorf("a disabled assertion should carry no result message, got %q", got[0].Message)
	}
}

// An unknown expression yields an empty actual value, which must FAIL against a
// non-empty expectation rather than silently comparing "" to "".
func TestEvaluateAssertionsFailsOnAnUnknownExpression(t *testing.T) {
	assertions := []Assertion{
		{Expression: "res.nonsense", Operator: "equals", Value: "200", Enabled: true},
	}
	got := evaluateAssertions(assertions, Response{Status: 200})
	if got[0].Passed {
		t.Error("an unrecognised expression must not report passed")
	}
}

func TestEvaluateAssertionsExplainsAFailure(t *testing.T) {
	got := evaluateAssertions(
		[]Assertion{{Expression: "res.status", Operator: "equals", Value: "500", Enabled: true}},
		Response{Status: 200},
	)
	if got[0].Message != `expected "200" equals "500"` {
		t.Fatalf("failure message = %q; it has to name what was seen and what was wanted", got[0].Message)
	}
}
