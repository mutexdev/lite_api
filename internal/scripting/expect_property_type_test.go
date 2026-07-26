// expect(...).to.have.property(...) and .to.be.a(...), the two lowest-covered
// assertion helpers after the contain() bug.
//
// Given that expect().to.contain() was silently passing for any two objects,
// the rest of this family deserved the same scrutiny rather than an assumption
// that the bug was isolated.
package scripting

import (
	"testing"

	"github.com/dop251/goja"
)

func TestExpectPropertyFindsPresentAndAbsentKeys(t *testing.T) {
	vm := goja.New()
	obj, err := vm.RunString(`({ id: 1, name: "ada", nested: { a: 1 } })`)
	if err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"id", "name", "nested"} {
		if _, ok := expectProperty(vm, obj, name); !ok {
			t.Errorf("property %q was reported missing", name)
		}
	}
	if _, ok := expectProperty(vm, obj, "missing"); ok {
		t.Error("a missing property was reported present")
	}
}

// A property explicitly set to undefined IS present. Reporting it absent makes
// expect(body).to.have.property('x') fail on a response that genuinely carries
// x — the loop after the undefined check exists precisely for this.
func TestExpectPropertyDistinguishesUndefinedValueFromMissingKey(t *testing.T) {
	vm := goja.New()
	obj, err := vm.RunString(`({ present: undefined })`)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := expectProperty(vm, obj, "present"); !ok {
		t.Error("a key whose value is undefined must still count as present")
	}
	if _, ok := expectProperty(vm, obj, "absent"); ok {
		t.Error("a key that does not exist must not count as present")
	}
}

// null and undefined have no properties, and asking must not panic — a script
// doing expect(body.maybe).to.have.property('x') on a missing field reaches here.
func TestExpectPropertyOnNullAndUndefinedIsSafe(t *testing.T) {
	vm := goja.New()
	for name, value := range map[string]goja.Value{
		"undefined": goja.Undefined(),
		"null":      goja.Null(),
	} {
		if _, ok := expectProperty(vm, value, "anything"); ok {
			t.Errorf("%s reported having a property", name)
		}
	}
}

func TestExpectTypeCoversTheJavaScriptTypes(t *testing.T) {
	vm := goja.New()
	mk := func(src string) goja.Value {
		v, err := vm.RunString(src)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}

	for name, tc := range map[string]struct {
		value    goja.Value
		expected string
		want     bool
	}{
		"string is string":     {mk(`"a"`), "string", true},
		"number is not string": {mk(`1`), "string", false},
		"number is number":     {mk(`1.5`), "number", true},
		"string is not number": {mk(`"1"`), "number", false},
		"bool is boolean":      {mk(`true`), "boolean", true},
		"array is array":       {mk(`[1,2]`), "array", true},
		"object is not array":  {mk(`({})`), "array", false},
		"object is object":     {mk(`({})`), "object", true},
		"function is function": {mk(`(function(){})`), "function", true},

		// Case and padding come from user-written assertions: .to.be.a('String')
		// must behave the same as .to.be.a('string').
		"type name is case-insensitive": {mk(`"a"`), "String", true},
		"type name is trimmed":          {mk(`"a"`), "  string  ", true},

		// An unknown type name must NOT match, or a typo in .to.be.a('strng')
		// asserts nothing while looking specific.
		"unknown type name": {mk(`"a"`), "strng", false},
	} {
		if got := expectType(vm, tc.value, tc.expected); got != tc.want {
			t.Errorf("%s: expectType(..., %q) = %v, want %v", name, tc.expected, got, tc.want)
		}
	}
}

// expectArray and the object/array boundary in expectType, the two lowest
// remaining in the family.
//
// The distinction they draw is the one JavaScript makes hardest: an array IS an
// object to typeof, so `.to.be.an('object')` must exclude arrays or every array
// assertion in a suite also passes as an object, and a response shape check
// stops discriminating between [] and {}.
func TestExpectArrayUsesArrayIsArray(t *testing.T) {
	vm := goja.New()
	mk := func(src string) goja.Value {
		v, err := vm.RunString(src)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}

	for src, want := range map[string]bool{
		`[]`:             true,
		`[1, 2, 3]`:      true,
		`new Array(3)`:   true,
		`({})`:           false,
		`({length: 3})`:  false, // array-LIKE is not an array
		`"abc"`:          false, // indexable, still not an array
		`null`:           false,
		`undefined`:      false,
		`(function(){})`: false,
	} {
		if got := expectArray(vm, mk(src)); got != want {
			t.Errorf("expectArray(%s) = %v, want %v", src, got, want)
		}
	}
}

// The boundary itself: arrays and functions are objects to typeof, and treating
// them as such here would make `.to.be.an('object')` accept both.
func TestExpectTypeObjectExcludesArraysFunctionsAndNull(t *testing.T) {
	vm := goja.New()
	mk := func(src string) goja.Value {
		v, err := vm.RunString(src)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}

	if !expectType(vm, mk(`({a: 1})`), "object") {
		t.Error("a plain object must be an object")
	}
	for _, src := range []string{`[]`, `[1,2]`, `(function(){})`, `null`, `undefined`, `"s"`, `1`, `true`} {
		if expectType(vm, mk(src), "object") {
			t.Errorf("%s was accepted as an object; the check would stop discriminating response shapes", src)
		}
	}
}
