// This one stayed in package main when the rest of the interpolation tests moved
// to internal/interp: it asserts the contract BETWEEN scripting.BuildVariableMap and the
// interpolator, and scripting.BuildVariableMap needs collections, environments and the
// workspace. A test that spans two packages belongs with the one that owns the
// state.
package core

import (
	"strings"
	"testing"

	"github.com/mutexdev/lite_api/internal/scripting"

	"github.com/mutexdev/lite_api/internal/interp"
)

func TestInterpolateCycleTermination(t *testing.T) {
	for i := 0; i < 200; i++ {
		if got := interp.Interpolate("{{a}}", map[string]string{"a": "{{b}}", "b": "{{a}}"}); got != "{{a}}" {
			t.Fatalf("two step cycle = %q, want %q", got, "{{a}}")
		}
		if got := interp.Interpolate("{{a}}", map[string]string{"a": "{{a}}"}); got != "{{a}}" {
			t.Fatalf("self cycle = %q, want %q", got, "{{a}}")
		}
		vars := map[string]string{"a": "x{{b}}", "b": "y{{c}}", "c": "z{{a}}"}
		if got := interp.Interpolate("{{a}}", vars); !strings.HasPrefix(got, "xyzxyz") {
			t.Fatalf("growing cycle = %q, want a bounded expansion", got)
		}
	}
}

// TestInterpolatePreservesPrecedence guards the contract with scripting.BuildVariableMap:
// interpolate resolves names against the map it is handed and does no
// precedence work of its own, so the winner is whatever the map says.
func TestInterpolatePreservesPrecedence(t *testing.T) {
	vars := scripting.BuildVariableMap(
		[]Environment{{ID: "g", Name: "global", Variables: []Variable{
			{Name: "only_global", Value: "g-only", Enabled: true},
			{Name: "shared", Value: "g-shared", Enabled: true},
		}}},
		&Collection{
			ID: "c",
			Variables: []Variable{
				{Name: "shared", Value: "c-shared", Enabled: true},
				{Name: "only_collection", Value: "c-only", Enabled: true},
			},
			Environments: []Environment{{ID: "e", Name: "env", Variables: []Variable{
				{Name: "shared", Value: "e-shared", Enabled: true},
			}}},
		},
		"e",
		RequestItem{ID: "r"},
	)
	got := interp.Interpolate("{{shared}}|{{only_global}}|{{only_collection}}", vars)
	want := vars["shared"] + "|" + vars["only_global"] + "|" + vars["only_collection"]
	if got != want {
		t.Fatalf("interpolate = %q, want %q", got, want)
	}
	if vars["shared"] != "e-shared" {
		t.Fatalf("environment variable should win over collection and global, got %q", vars["shared"])
	}
}

// TestInterpolateLargeBodyIsSingleScan is a behavioural check that a body with
// many tokens and a large amount of surrounding text is expanded correctly, and
// that variables not present in the input cost nothing but are still available.
