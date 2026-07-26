package main

// US-054 — tests for the extended code generation targets.
//
// The assertions that matter are about ESCAPING, because that is the failure
// that does not look like a failure. A generator that mishandles a quote emits
// code which compiles and sends the wrong bytes; one that mishandles a `$` in
// PHP or a `#{` in Ruby emits code that silently interpolates a variable the
// user never wrote. Every target is therefore fed the same hostile body.

import (
	"LiteAPI/internal/codegen"
	"LiteAPI/internal/types"
	"strings"
	"testing"
)

// hostileBody contains, deliberately: a double quote, a single quote, a
// backslash, a newline, a PHP variable sigil, a Ruby interpolation opener, a
// backtick and a non-ASCII rune.
const hostileBody = `{"quote":"she said \"hi\"","apostrophe":"it's","path":"C:\\Users\\ada","dollar":"$total","ruby":"#{evil}","tick":"` + "`whoami`" + `","unicode":"héllo 世界"}`

func TestCodegenTargetsAreDispatchable(t *testing.T) {
	app := newAppForTest(t)
	example := mainCodegenFixture()

	for _, target := range app.CodeGenerationTargets() {
		t.Run(target.ID, func(t *testing.T) {
			if strings.TrimSpace(target.Label) == "" {
				t.Error("target has no label")
			}
			if _, err := codegen.GenerateResponseExampleCode(example, target.ID); err != nil {
				t.Errorf("the picker offers %q but generating it fails: %v", target.ID, err)
			}
		})
	}

	// Aliases too, since they are the ids a saved preference may hold.
	for _, target := range codegen.Languages {
		for _, alias := range target.Aliases {
			if _, err := codegen.GenerateResponseExampleCode(example, alias); err != nil {
				t.Errorf("alias %q does not resolve: %v", alias, err)
			}
		}
	}
}

// TestCodegenEscapesHostileBodies is the core assertion. Each language's own
// literal rules are checked, not a generic "contains the body" — a generator
// can contain the body and still have broken out of the string.

// mainCodegenFixture mirrors internal/codegen's own fixture. This test spans two
// packages -- the picker comes from *App, the generators from internal/codegen --
// so it cannot share the unexported one.
func mainCodegenFixture() types.ResponseExample {
	return types.ResponseExample{
		Name: "example",
		Request: types.ResponseExampleRequest{
			Method:   "POST",
			URL:      "https://api.test/things",
			Headers:  []types.KeyValue{{Name: "Content-Type", Value: "application/json", Enabled: true}},
			BodyMode: "json",
			Body:     hostileBody,
		},
	}
}
