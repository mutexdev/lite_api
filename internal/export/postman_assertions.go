package export

// Translating the legacy assertion DSL on the way out.
//
// The Tests slot accepts two dialects at once: JavaScript, and the plain
// English line `expect status equals 200` that the Assert tab writes. The
// runtime splits them — scripting.EvaluateScriptTests reads the expect lines,
// javascriptFromTests strips them before the JavaScript runs — so both work
// here.
//
// Postman has no such split. Everything in a test event is JavaScript, and an
// exported `expect status equals 200` is a syntax error that fails the whole
// script the moment the collection is opened there. The line is not a comment
// Postman ignores; it stops every real assertion in the same block from
// running.
//
// The grammar is deliberately mirrored rather than imported: it lives in
// unexported helpers in internal/scripting (isLegacyExpectLine, and the operator
// set of CompareAssertion), and this package must not reach into those. It is
// four operators over one subject, and postmanAssertionGrammarIsMirrored in the
// tests pins the mirror against the real evaluator.

import (
	"encoding/json"
	"strings"
)

// postmanAssertionOperators is scripting.CompareAssertion's operator set,
// expressed as the JavaScript that means the same thing about a string.
var postmanAssertionOperators = map[string]func(actual, expected string) string{
	"equals":    func(actual, expected string) string { return "pm.expect(" + actual + ").to.eql(" + expected + ");" },
	"==":        func(actual, expected string) string { return "pm.expect(" + actual + ").to.eql(" + expected + ");" },
	"notEquals": func(actual, expected string) string { return "pm.expect(" + actual + ").to.not.eql(" + expected + ");" },
	"!=":        func(actual, expected string) string { return "pm.expect(" + actual + ").to.not.eql(" + expected + ");" },
	"contains":  func(actual, expected string) string { return "pm.expect(" + actual + ").to.include(" + expected + ");" },
	"startsWith": func(actual, expected string) string {
		return "pm.expect(" + actual + ".startsWith(" + expected + ")).to.be.true;"
	},
	"endsWith": func(actual, expected string) string {
		return "pm.expect(" + actual + ".endsWith(" + expected + ")).to.be.true;"
	},
}

// translatePostmanTests rewrites every legacy expect line as a pm.test block and
// leaves JavaScript untouched. A line that opens with "expect " but is not a
// line the evaluator would run is commented out rather than exported verbatim,
// because verbatim is a syntax error; the caller is told through warn.
func translatePostmanTests(tests string, warn func(string)) string {
	if strings.TrimSpace(tests) == "" {
		return tests
	}
	lines := strings.Split(tests, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "expect ") {
			out = append(out, line)
			continue
		}
		if translated, ok := translatePostmanAssertionLine(trimmed); ok {
			out = append(out, translated)
			continue
		}
		out = append(out, "// "+trimmed)
		if warn != nil {
			warn("Some assertion lines could not be translated to Postman JavaScript and were exported as comments.")
		}
	}
	return strings.Join(out, "\n")
}

// translatePostmanAssertionLine mirrors scripting.EvaluateScriptTests: an
// assertion is `expect <subject> <operator> <value>`, the only subject that
// evaluates is status, and the comparison is made on the status as text.
func translatePostmanAssertionLine(line string) (string, bool) {
	fields := strings.Fields(line)
	if len(fields) < 4 || fields[1] != "status" {
		return "", false
	}
	build, ok := postmanAssertionOperators[fields[2]]
	if !ok {
		return "", false
	}
	expected := strings.TrimSpace(strings.Join(fields[3:], " "))
	name := strings.TrimSpace(strings.TrimPrefix(line, "expect "))
	return "pm.test(" + jsonString(name) + ", function () { " + build("String(pm.response.code)", jsonString(expected)) + " });", true
}

func jsonString(value string) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return `""`
	}
	return string(encoded)
}
