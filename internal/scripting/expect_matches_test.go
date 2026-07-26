// expect(...).to.match(...) and the numeric/length helpers beside it.
//
// Continuing through the expect family after four defects in it. match() takes
// two routes: a real JavaScript RegExp (whose .test method is called through
// goja) and a plain string compiled by Go's regexp. Those dialects are not
// identical, and which one runs depends on what the user passed.
package scripting

import (
	"testing"

	"github.com/dop251/goja"
)

func TestExpectMatchesUsesAJavaScriptRegExpsOwnTest(t *testing.T) {
	vm := goja.New()
	re, err := vm.RunString(`/^ab+c$/i`)
	if err != nil {
		t.Fatal(err)
	}

	// The `i` flag is a JavaScript concept. Going through the RegExp's own
	// .test is what honours it; compiling the source with Go's regexp would
	// silently drop the flag and fail a matching response.
	got, err := expectMatches(vm, vm.ToValue("ABBC"), re)
	if err != nil {
		t.Fatal(err)
	}
	if !got {
		t.Error("a case-insensitive JS RegExp must match through its own test method")
	}

	got, err = expectMatches(vm, vm.ToValue("xyz"), re)
	if err != nil {
		t.Fatal(err)
	}
	if got {
		t.Error("a non-matching value must not match")
	}
}

func TestExpectMatchesCompilesAStringPattern(t *testing.T) {
	vm := goja.New()

	got, err := expectMatches(vm, vm.ToValue("hello world"), vm.ToValue(`^hello\s+world$`))
	if err != nil {
		t.Fatal(err)
	}
	if !got {
		t.Error("a string pattern must compile and match")
	}

	got, err = expectMatches(vm, vm.ToValue("goodbye"), vm.ToValue(`^hello`))
	if err != nil {
		t.Fatal(err)
	}
	if got {
		t.Error("a non-matching string pattern must not match")
	}
}

// An invalid pattern must ERROR, not quietly report false. A false would read
// as "the response did not match" and send the user looking at their API
// instead of their typo.
func TestExpectMatchesReportsAnInvalidPatternAsAnError(t *testing.T) {
	vm := goja.New()
	got, err := expectMatches(vm, vm.ToValue("anything"), vm.ToValue(`([unclosed`))
	if err == nil {
		t.Fatal("an uncompilable pattern must be an error, not a silent non-match")
	}
	if got {
		t.Error("an errored match must not also report true")
	}
}

func TestExpectNumberRejectsNonNumbersAndNaN(t *testing.T) {
	vm := goja.New()
	mk := func(src string) goja.Value {
		v, err := vm.RunString(src)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}

	if got, ok := expectNumber(mk(`42`)); !ok || got != 42 {
		t.Errorf("42 gave (%v, %v)", got, ok)
	}
	if got, ok := expectNumber(mk(`1.5`)); !ok || got != 1.5 {
		t.Errorf("1.5 gave (%v, %v)", got, ok)
	}
	// NaN is a number by typeof but useless for a comparison: letting it through
	// makes every > and < assertion against it silently false.
	if _, ok := expectNumber(mk(`NaN`)); ok {
		t.Error("NaN must be rejected, or numeric comparisons against it read as ordinary failures")
	}
	for _, src := range []string{`"42"`, `true`, `null`, `undefined`, `({})`, `[]`} {
		if _, ok := expectNumber(mk(src)); ok {
			t.Errorf("%s was accepted as a number", src)
		}
	}
}
