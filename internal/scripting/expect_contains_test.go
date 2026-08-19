// expect(...).to.contain(...), at 8% coverage.
//
// This is an assertion helper, which is the family where a defect is worst: a
// wrong answer here makes a user's test report the opposite of the truth, with
// nothing to indicate it. The assertion operators earlier in this work had
// exactly that shape — endsWith behaving as startsWith, passing everything.
//
// "contains" is unusually broad in Postman's vocabulary: it means substring for
// a string, membership for an array, and KEY PRESENCE for an object. Each of
// those is a separate branch, and only the first was exercised.
package scripting

import (
	"testing"

	"github.com/dop251/goja"
)

func TestExpectContainsSubstring(t *testing.T) {
	vm := goja.New()
	v := vm.ToValue

	if !expectContains(vm, v("hello world"), v("lo wo")) {
		t.Error("a substring must match")
	}
	if expectContains(vm, v("hello"), v("absent")) {
		t.Error("a missing substring must not match")
	}
}

func TestExpectContainsArrayMembership(t *testing.T) {
	vm := goja.New()
	arr, err := vm.RunString(`["alpha", "beta", "gamma"]`)
	if err != nil {
		t.Fatal(err)
	}
	if !expectContains(vm, arr, vm.ToValue("beta")) {
		t.Error("an array must contain its member")
	}
	if expectContains(vm, arr, vm.ToValue("delta")) {
		t.Error("an array must not contain a value it does not hold")
	}
}

// Numbers arrive as float64 from goja, so an array of numbers only matches if
// the comparison does not depend on the Go type.
func TestExpectContainsNumericArrayMembers(t *testing.T) {
	vm := goja.New()
	arr, err := vm.RunString(`[1, 2, 3]`)
	if err != nil {
		t.Fatal(err)
	}
	if !expectContains(vm, arr, vm.ToValue(2)) {
		t.Error("a numeric array member must match despite goja's numeric typing")
	}
	if expectContains(vm, arr, vm.ToValue(9)) {
		t.Error("a number not in the array must not match")
	}
}

// Objects match on KEY presence, not on value. That is Postman's meaning, and
// asserting on the value instead would silently change what every
// `expect(body).to.contain('id')` test checks.
func TestExpectContainsObjectKeyPresence(t *testing.T) {
	vm := goja.New()
	obj, err := vm.RunString(`({ id: 1, name: "ada" })`)
	if err != nil {
		t.Fatal(err)
	}
	if !expectContains(vm, obj, vm.ToValue("id")) {
		t.Error("an object must contain its key")
	}
	if expectContains(vm, obj, vm.ToValue("missing")) {
		t.Error("an object must not contain a key it lacks")
	}
	// A VALUE is not a key: matching on values would make the assertion mean
	// something different from what its users wrote.
	if expectContains(vm, obj, vm.ToValue("ada")) {
		t.Error("object containment is key presence, not value presence")
	}
}

// Objects nested in an array compare structurally, so a response body checked
// against a literal matches even though the two are different goja objects.
func TestExpectContainsStructurallyEqualObjectsInAnArray(t *testing.T) {
	vm := goja.New()
	arr, err := vm.RunString(`[{ id: 1 }, { id: 2 }]`)
	if err != nil {
		t.Fatal(err)
	}
	needle, err := vm.RunString(`({ id: 2 })`)
	if err != nil {
		t.Fatal(err)
	}
	if !expectContains(vm, arr, needle) {
		t.Error("a structurally equal object must be found in the array")
	}
	other, err := vm.RunString(`({ id: 9 })`)
	if err != nil {
		t.Fatal(err)
	}
	if expectContains(vm, arr, other) {
		t.Error("a structurally different object must not match")
	}
}

func TestExpectContainsEmptyCases(t *testing.T) {
	vm := goja.New()
	empty, err := vm.RunString(`[]`)
	if err != nil {
		t.Fatal(err)
	}
	if expectContains(vm, empty, vm.ToValue("anything")) {
		t.Error("an empty array contains nothing")
	}
	// Every string contains the empty string; changing that would break the
	// JavaScript convention the assertion inherits.
	if !expectContains(vm, vm.ToValue("abc"), vm.ToValue("")) {
		t.Error("every string contains the empty string")
	}
}
