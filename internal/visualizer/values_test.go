// The value semantics of the visualizer template language.
//
// These three decide what a user's {{value}}, {{#if}} and {{#each}} actually
// do with the data a script handed over. All three take interface{} straight
// out of encoding/json, so every value is nil, bool, float64, string,
// []interface{} or map[string]interface{} — there are no ints, and a test that
// passes one is testing a case the renderer never sees.
package visualizer

import "testing"

// Rendering is the whole output. A number shown in scientific notation is
// simply wrong on screen, and an object rendered as Go's %v syntax is
// unreadable where JSON would have been fine.
func TestValueStringRendersEachJSONKind(t *testing.T) {
	for name, tc := range map[string]struct {
		value interface{}
		want  string
	}{
		"nil":      {nil, ""},
		"string":   {"hello", "hello"},
		"empty":    {"", ""},
		"true":     {true, "true"},
		"false":    {false, "false"},
		"integer":  {float64(42), "42"},
		"decimal":  {1.5, "1.5"},
		"zero":     {float64(0), "0"},
		"negative": {-7.25, "-7.25"},
	} {
		if got := visualizerValueString(tc.value); got != tc.want {
			t.Errorf("%s: got %q, want %q", name, got, tc.want)
		}
	}
}

// %v renders 1000000 as 1e+06. An id or a byte count shown that way is wrong,
// and the user cannot tell it came from the renderer rather than the data.
func TestValueStringNeverUsesScientificNotation(t *testing.T) {
	for _, value := range []float64{1000000, 1e9, 1e21, 0.000001} {
		got := visualizerValueString(value)
		if got == "" {
			t.Fatalf("%v rendered empty", value)
		}
		for _, bad := range []byte{'e', 'E'} {
			for i := 0; i < len(got); i++ {
				if got[i] == bad {
					t.Errorf("%v rendered as %q, which is scientific notation", value, got)
					break
				}
			}
		}
	}
}

// A whole number must not grow a decimal point: an id is "42", not "42.0".
func TestValueStringKeepsWholeNumbersWhole(t *testing.T) {
	if got := visualizerValueString(float64(42)); got != "42" {
		t.Errorf("got %q", got)
	}
	if got := visualizerValueString(float64(1000000)); got != "1000000" {
		t.Errorf("got %q", got)
	}
}

// Structured values render as JSON rather than Go syntax, because a template
// that interpolates an object is usually debugging and JSON is what it expects.
func TestValueStringRendersContainersAsJSON(t *testing.T) {
	if got := visualizerValueString(map[string]interface{}{"a": float64(1)}); got != `{"a":1}` {
		t.Errorf("map gave %q", got)
	}
	if got := visualizerValueString([]interface{}{float64(1), "x"}); got != `[1,"x"]` {
		t.Errorf("slice gave %q", got)
	}
	if got := visualizerValueString([]interface{}{}); got != `[]` {
		t.Errorf("empty slice gave %q", got)
	}
}

// Truthiness decides whether an {{#if}} block renders at all, so an empty
// string or an empty list counting as true would show a section the data does
// not support.
func TestTruthyFollowsJavaScriptForEveryJSONKind(t *testing.T) {
	for name, tc := range map[string]struct {
		value interface{}
		want  bool
	}{
		"nil":          {nil, false},
		"true":         {true, true},
		"false":        {false, false},
		"empty string": {"", false},
		"string":       {"x", true},
		"zero":         {float64(0), false},
		"number":       {float64(1), true},
		"negative":     {-1.0, true},
		"empty list":   {[]interface{}{}, false},
		"list":         {[]interface{}{float64(1)}, true},
		"empty object": {map[string]interface{}{}, false},
		"object":       {map[string]interface{}{"a": nil}, true},
	} {
		if got := visualizerTruthy(tc.value); got != tc.want {
			t.Errorf("%s: got %v, want %v", name, got, tc.want)
		}
	}
}

// "false" the string is a non-empty string and therefore true — the same
// surprise JavaScript has, kept deliberately so a template behaves the way its
// author expects from the browser.
func TestTruthyTreatsTheStringFalseAsTrue(t *testing.T) {
	if !visualizerTruthy("false") {
		t.Error(`the string "false" is non-empty and must be truthy, as it is in JavaScript`)
	}
	if !visualizerTruthy("0") {
		t.Error(`the string "0" is non-empty and must be truthy`)
	}
}

// {{#each}} over a single object iterates once rather than rendering nothing.
// A response that returns one object where a list was expected is common, and
// silently showing an empty section would look like the data was missing.
func TestIterableTreatsANonListAsOneItem(t *testing.T) {
	object := map[string]interface{}{"id": float64(1)}
	items := visualizerIterable(object)
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1", len(items))
	}
	if got, ok := items[0].(map[string]interface{}); !ok || got["id"] != float64(1) {
		t.Errorf("the single item is not the original object: %#v", items[0])
	}

	if got := visualizerIterable("text"); len(got) != 1 || got[0] != "text" {
		t.Errorf("a scalar should iterate once, got %#v", got)
	}
}

// nil iterates zero times. Wrapping it as one item would render the block once
// against nothing, printing empty rows for data that was never there.
func TestIterableOfNilIsEmpty(t *testing.T) {
	if got := visualizerIterable(nil); len(got) != 0 {
		t.Errorf("got %#v, want no items", got)
	}
}

func TestIterableOfAListIsThatList(t *testing.T) {
	list := []interface{}{float64(1), float64(2)}
	got := visualizerIterable(list)
	if len(got) != 2 || got[0] != float64(1) || got[1] != float64(2) {
		t.Errorf("got %#v", got)
	}
	if empty := visualizerIterable([]interface{}{}); len(empty) != 0 {
		t.Errorf("an empty list must iterate zero times, got %#v", empty)
	}
}
