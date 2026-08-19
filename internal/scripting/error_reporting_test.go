// How a script failure is reported back to the user.
//
// All three were at 0%. They are the paths that turn a thrown error, a rejected
// promise or an odd form value into something the UI shows — and the first one
// carries a property worth guarding hard: a script that throws must produce a
// FAILED test result.
//
// If that flipped, a broken pre-request script would report as a passing test.
// This is an API testing tool; reporting green wrongly is the one outcome worse
// than crashing, and it is the same failure the assertion operators had.
package scripting

import (
	"errors"
	"strings"
	"testing"

	"github.com/dop251/goja"
)

func TestScriptErrorResponseReportsAFailedTest(t *testing.T) {
	got := ScriptErrorResponse("pre-request script", errors.New("boom"))

	if len(got.TestResults) != 1 {
		t.Fatalf("got %d test results, want 1: %+v", len(got.TestResults), got.TestResults)
	}
	if got.TestResults[0].Passed {
		t.Fatal("a script error produced a PASSING test result — a broken script would report green")
	}
	if got.TestResults[0].Name != "pre-request script" {
		t.Errorf("result name = %q; the user needs to know which script failed", got.TestResults[0].Name)
	}
	if !strings.Contains(got.TestResults[0].Message, "boom") {
		t.Errorf("result message = %q; it must carry the underlying error", got.TestResults[0].Message)
	}
}

// The Error field is what the response pane shows. It must name both the phase
// and the cause, or the user sees "boom" with no idea which script produced it.
func TestScriptErrorResponseNamesThePhaseAndTheCause(t *testing.T) {
	got := ScriptErrorResponse("post-response script", errors.New("ReferenceError: x is not defined"))
	if !strings.Contains(got.Error, "post-response script") {
		t.Errorf("Error = %q, missing the phase", got.Error)
	}
	if !strings.Contains(got.Error, "ReferenceError") {
		t.Errorf("Error = %q, missing the cause", got.Error)
	}
}

// An empty Headers map rather than nil: the response pane and the JSON encoder
// both walk it, and a nil map renders as null where the UI expects an object.
func TestScriptErrorResponseCarriesUsableZeroValues(t *testing.T) {
	got := ScriptErrorResponse("tests", errors.New("x"))
	if got.Headers == nil {
		t.Error("Headers is nil; it must be an empty map")
	}
	if got.PreviewMode != "raw" {
		t.Errorf("PreviewMode = %q, want raw — an error body is not JSON to be pretty-printed", got.PreviewMode)
	}
	if got.SentAt.IsZero() {
		t.Error("SentAt is zero; the timeline would place the failure at the epoch")
	}
}

func TestPromiseRejectionMessagePrefersTheErrorMessage(t *testing.T) {
	vm := goja.New()

	// A rejected Error object: the useful text is .message, not the whole object.
	errValue, err := vm.RunString(`new Error("something failed")`)
	if err != nil {
		t.Fatal(err)
	}
	if got := scriptPromiseRejectionMessage(vm, errValue); got != "something failed" {
		t.Errorf("got %q, want the Error's message", got)
	}

	// A rejected string has no .message and must fall back to its own text.
	strValue, err := vm.RunString(`"plain rejection"`)
	if err != nil {
		t.Fatal(err)
	}
	if got := scriptPromiseRejectionMessage(vm, strValue); got != "plain rejection" {
		t.Errorf("got %q, want the value itself", got)
	}
}

// Rejecting with undefined or null is legal JavaScript and must still yield a
// message. An empty string here shows the user a blank failure.
func TestPromiseRejectionMessageHandlesEmptyRejections(t *testing.T) {
	vm := goja.New()
	for name, value := range map[string]goja.Value{
		"nil":       nil,
		"undefined": goja.Undefined(),
		"null":      goja.Null(),
	} {
		got := scriptPromiseRejectionMessage(vm, value)
		if strings.TrimSpace(got) == "" {
			t.Errorf("%s rejection produced an empty message; the user would see a blank failure", name)
		}
	}
}

func TestFormInterfaceStringRendersValuesAndTreatsNilAsEmpty(t *testing.T) {
	for name, tc := range map[string]struct {
		value interface{}
		want  string
	}{
		"nil is empty": {nil, ""},
		"string":       {"a", "a"},
		"int":          {42, "42"},
		"float":        {1.5, "1.5"},
		"bool":         {true, "true"},
	} {
		if got := scriptFormInterfaceString(tc.value); got != tc.want {
			t.Errorf("%s: got %q, want %q", name, got, tc.want)
		}
	}
}
