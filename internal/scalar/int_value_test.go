// Reading an integer out of a value that came from JSON or YAML.
//
// The same setting is an int in a hand-written Go default, a float64 after a
// JSON round trip, and a string when it came from a form field or an
// environment variable. IntValueOK absorbs that. Its second return is the whole
// point: a caller that ignores it and takes the zero gets a timeout of 0 or a
// redirect limit of 0, both of which are meaningful settings that nobody chose.
package scalar

import "testing"

func TestIntValueReadsEveryShapeANumberArrivesIn(t *testing.T) {
	for name, raw := range map[string]interface{}{
		"int":           int(42),
		"int64":         int64(42),
		"float64":       float64(42),
		"string":        "42",
		"spaced string": "  42  ",
	} {
		got, ok := IntValueOK(raw)
		if !ok || got != 42 {
			t.Errorf("%s: got %d %v", name, got, ok)
		}
	}
}

// JSON has no integers, so a whole number arrives as float64. A fractional one
// is truncated rather than rejected, matching what the JSON number would have
// meant if it had been written as an int.
func TestIntValueTruncatesFractions(t *testing.T) {
	if got, ok := IntValueOK(float64(42.9)); !ok || got != 42 {
		t.Errorf("got %d %v", got, ok)
	}
	if got, ok := IntValueOK(float64(-42.9)); !ok || got != -42 {
		t.Errorf("got %d %v", got, ok)
	}
}

// Not-ok has to be distinguishable from zero. A caller that treats them alike
// turns "the setting was unreadable" into "the setting is 0", which for a
// timeout means no timeout at all.
func TestIntValueReportsUnreadableValuesRatherThanZero(t *testing.T) {
	for name, raw := range map[string]interface{}{
		"empty string": "",
		"words":        "many",
		"float text":   "4.2",
		"nil":          nil,
		"bool":         true,
		"list":         []interface{}{1},
		"map":          map[string]interface{}{"a": 1},
	} {
		if got, ok := IntValueOK(raw); ok {
			t.Errorf("%s: reported readable as %d", name, got)
		}
	}
	// And a genuine zero is readable.
	if got, ok := IntValueOK("0"); !ok || got != 0 {
		t.Errorf("a real zero was reported unreadable: %d %v", got, ok)
	}
}
