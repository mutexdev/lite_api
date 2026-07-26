package interp

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestInterpolateCharacterisation pins the observable behaviour of interpolate.
//
// It was written against the pre-US-023 implementation (8 passes x every
// variable x strings.ReplaceAll) and passed unchanged against the single-scan
// rewrite, so it is the safety net for that rewrite rather than a description
// of the new code. Every expectation below was observed from the old
// implementation, including the ones that look like bugs.
func TestInterpolateCharacterisation(t *testing.T) {
	cases := []struct {
		name string
		in   string
		vars map[string]string
		want string
	}{
		// Plain substitution.
		{"simple", "x{{a}}y", map[string]string{"a": "V"}, "xVy"},
		{"adjacent", "{{a}}{{b}}", map[string]string{"a": "1", "b": "2"}, "12"},
		{"repeated", "{{a}}-{{a}}", map[string]string{"a": "V"}, "V-V"},
		{"no tokens", "plain text", map[string]string{"a": "V"}, "plain text"},
		{"nil vars", "x{{a}}y", nil, "x{{a}}y"},

		// An absent variable is left verbatim; it must never become empty.
		{"unknown token", "x{{nope}}y", map[string]string{"a": "1"}, "x{{nope}}y"},
		// An empty value still substitutes: absent is not the same as empty.
		{"empty value", "x{{a}}y", map[string]string{"a": ""}, "xy"},

		// Nested references resolve to a fixed point.
		{"nested chain", "{{a}}", map[string]string{"a": "{{b}}", "b": "{{c}}", "c": "done"}, "done"},
		{"nested inside text", "u={{a}}!", map[string]string{"a": "http://{{host}}", "host": "example.test"}, "u=http://example.test!"},
		// A value that only opens a token never completes one.
		{"half token value", "{{a}}", map[string]string{"a": "{{b", "b": "X"}, "{{b"},
		// A value spliced against the surrounding literal does complete one.
		{"token completed by literal", "{{a}}}}", map[string]string{"a": "{{b", "b": "X"}, "X"},

		// A cycle terminates at the pass limit and leaves a token verbatim.
		// The two-step cycle is order-dependent under the old implementation,
		// so it lives in TestInterpolateCycleTermination instead.
		{"self cycle", "{{a}}", map[string]string{"a": "{{a}}"}, "{{a}}"},

		// Matching is an exact literal "{{" + key + "}}", so whitespace inside
		// the braces is part of the key and is not trimmed.
		{"spaces not trimmed", "{{ a }}", map[string]string{"a": "1"}, "{{ a }}"},
		{"spaces are part of key", "{{ a }}", map[string]string{" a ": "1"}, "1"},
		{"key with spaces", "{{?Script Prompt}}", map[string]string{"?Script Prompt": "v"}, "v"},
		{"key with newline", "{{a\nb}}", map[string]string{"a\nb": "V"}, "V"},

		// Degenerate brace forms.
		{"empty key matches", "x{{}}y", map[string]string{"": "EMPTY"}, "xEMPTYy"},
		{"empty key absent", "x{{}}y", map[string]string{"a": "1"}, "x{{}}y"},
		{"unterminated", "{{a", map[string]string{"a": "V"}, "{{a"},
		{"lone open brace", "{a}", map[string]string{"a": "V"}, "{a}"},
		{"triple braces", "{{{a}}}", map[string]string{"a": "V"}, "{V}"},
		{"triple braces braced key", "{{{a}}}", map[string]string{"{a}": "V"}, "V"},
		{"key ending in brace", "{{a}}}", map[string]string{"a}": "V"}, "V"},
		{"key containing closer", "{{a}}b}}", map[string]string{"a}}b": "V"}, "V"},
		{"empty value joins braces", "X{{{a}}{b}}Y", map[string]string{"a": "", "b": "BB"}, "XBBY"},

		// process.env keys are skipped by plain substitution and resolved only
		// by replaceProcessEnvVariables, which does allow surrounding spaces.
		{"process env", "{{process.env.LITEAPI_CHAR_TEST}}", map[string]string{"process.env.LITEAPI_CHAR_TEST": "PV"}, "PV"},
		{"process env spaces", "{{ process.env.LITEAPI_CHAR_TEST }}", map[string]string{"process.env.LITEAPI_CHAR_TEST": "PV"}, "PV"},
		{"process env tab", "{{\tprocess.env.LITEAPI_CHAR_TEST\t}}", map[string]string{"process.env.LITEAPI_CHAR_TEST": "PV"}, "PV"},
		// \v is not in the regexp \s class, so it is not accepted.
		{"process env vertical tab", "{{\vprocess.env.LITEAPI_CHAR_TEST\v}}", map[string]string{"process.env.LITEAPI_CHAR_TEST": "PV"}, "{{\vprocess.env.LITEAPI_CHAR_TEST\v}}"},
		{"process env unknown", "{{process.env.LITEAPI_CHAR_ABSENT}}", map[string]string{}, "{{process.env.LITEAPI_CHAR_ABSENT}}"},
		{"process env invalid name", "{{process.env.NOT-AN-IDENT}}", map[string]string{"process.env.NOT-AN-IDENT": "PV"}, "{{process.env.NOT-AN-IDENT}}"},
		{"process env empty name", "{{process.env.}}", map[string]string{"process.env.": "PV"}, "{{process.env.}}"},
		{"process env nested in value", "{{a}}", map[string]string{"a": "{{process.env.LITEAPI_CHAR_TEST}}", "process.env.LITEAPI_CHAR_TEST": "PV"}, "PV"},
		{"process env value nests var", "{{process.env.LITEAPI_CHAR_TEST}}", map[string]string{"process.env.LITEAPI_CHAR_TEST": "{{inner}}", "inner": "IV"}, "IV"},
		{"process env inside failed token", "{{x{{process.env.LITEAPI_CHAR_TEST}}", map[string]string{"process.env.LITEAPI_CHAR_TEST": "PV"}, "{{xPV"},
		// A non-prefixed lookalike key is an ordinary variable.
		{"process env lookalike key", "{{process.envX}}", map[string]string{"process.envX": "V"}, "V"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Run repeatedly: the old implementation ranged over the variable
			// map, so anything order-dependent shows up as flakiness here.
			for i := 0; i < 50; i++ {
				if got := Interpolate(tc.in, tc.vars); got != tc.want {
					t.Fatalf("Interpolate(%q) = %q, want %q", tc.in, got, tc.want)
				}
			}
		})
	}
}

// TestInterpolateCharacterisationDynamicTokens pins the ordering rule for the
// dynamic tokens: they are substituted after variable expansion, so a variable
// value containing one is still resolved, but their result is never rescanned
// for variables, and a variable may shadow them.
func TestInterpolateCharacterisationDynamicTokens(t *testing.T) {
	before := time.Now().Unix()
	got := Interpolate("{{$timestamp}}", nil)
	after := time.Now().Unix()
	ts, err := strconv.ParseInt(got, 10, 64)
	if err != nil {
		t.Fatalf("{{$timestamp}} = %q, want a unix timestamp: %v", got, err)
	}
	if ts < before || ts > after {
		t.Fatalf("{{$timestamp}} = %d, want within [%d, %d]", ts, before, after)
	}

	iso := Interpolate("{{$isoTimestamp}}", nil)
	if _, err := time.Parse(time.RFC3339, iso); err != nil {
		t.Fatalf("{{$isoTimestamp}} = %q, want RFC3339: %v", iso, err)
	}

	// A variable value carrying a dynamic token is still substituted.
	fromVar := Interpolate("{{a}}", map[string]string{"a": "{{$timestamp}}"})
	if _, err := strconv.ParseInt(fromVar, 10, 64); err != nil {
		t.Fatalf("nested {{$timestamp}} = %q, want a unix timestamp: %v", fromVar, err)
	}

	// A variable of the same name wins, because it is applied first.
	if got := Interpolate("{{$timestamp}}", map[string]string{"$timestamp": "SHADOW"}); got != "SHADOW" {
		t.Fatalf("shadowed {{$timestamp}} = %q, want %q", got, "SHADOW")
	}
	if got := Interpolate("{{$isoTimestamp}}", map[string]string{"$isoTimestamp": "SHADOW"}); got != "SHADOW" {
		t.Fatalf("shadowed {{$isoTimestamp}} = %q, want %q", got, "SHADOW")
	}

	// The dynamic result is never rescanned as a variable name.
	if got := Interpolate("{{$timestamp}}", map[string]string{strconv.FormatInt(time.Now().Unix(), 10): "RESCANNED"}); got == "RESCANNED" {
		t.Fatal("{{$timestamp}} result was rescanned for variables")
	}
}

// TestInterpolateReadsProcessEnvFromOS pins that a process.env token falls back
// to the real environment when the variable map carries no override.
func TestInterpolateReadsProcessEnvFromOS(t *testing.T) {
	t.Setenv("LITEAPI_CHAR_OS_ENV", "from-os")
	if got := Interpolate("{{process.env.LITEAPI_CHAR_OS_ENV}}", map[string]string{}); got != "from-os" {
		t.Fatalf("interpolate = %q, want %q", got, "from-os")
	}
	if _, ok := os.LookupEnv("LITEAPI_CHAR_OS_ENV"); !ok {
		t.Fatal("t.Setenv did not take effect")
	}
	// A map override beats the OS value.
	vars := map[string]string{"process.env.LITEAPI_CHAR_OS_ENV": "from-vars"}
	if got := Interpolate("{{process.env.LITEAPI_CHAR_OS_ENV}}", vars); got != "from-vars" {
		t.Fatalf("interpolate = %q, want %q", got, "from-vars")
	}
}

// TestInterpolateNestingDepth pins the substitution depth limit at
// maxPasses substitutions. A chain v0 -> v1 -> ... -> vN where vN
// holds the literal needs N+1 substitutions to collapse.
//
// This is the one place where the single-scan implementation is deliberately
// not a characterisation of the old one. The old implementation ranged over the
// variable map and applied every key to the whole string, so a lucky iteration
// order could collapse an arbitrarily long chain in a single pass and an
// unlucky one could manage a single level; how deep it resolved was not
// defined. This resolves exactly one level per pass, so the limit is exact.
func TestInterpolateNestingDepth(t *testing.T) {
	for depth := 1; depth < maxPasses; depth++ {
		if got := Interpolate("{{v0}}", interpolateChain(depth)); got != "END" {
			t.Fatalf("depth %d: interpolate = %q, want %q", depth, got, "END")
		}
	}
	// One level past the limit stops rather than looping: the token that could
	// not be reached is left verbatim.
	want := fmt.Sprintf("{{v%d}}", maxPasses)
	if got := Interpolate("{{v0}}", interpolateChain(maxPasses)); got != want {
		t.Fatalf("depth %d: interpolate = %q, want %q", maxPasses, got, want)
	}
}

func interpolateChain(depth int) map[string]string {
	vars := make(map[string]string, depth+1)
	for i := 0; i < depth; i++ {
		vars[fmt.Sprintf("v%d", i)] = fmt.Sprintf("{{v%d}}", i+1)
	}
	vars[fmt.Sprintf("v%d", depth)] = "END"
	return vars
}

// TestInterpolateCycleTermination pins that a cyclic reference terminates and
// leaves a token verbatim rather than hanging, recursing, or emptying out.
//
// The old implementation returned "{{a}}" on ~99% of runs and "{{b}}" on the
// rest, depending on the order the variable map happened to range in. The
// single-scan implementation always lands on "{{a}}": it flips the token once
// per pass and maxPasses is even.
func TestInterpolateLargeBodyIsSingleScan(t *testing.T) {
	vars := make(map[string]string, 200)
	for i := 0; i < 200; i++ {
		vars[fmt.Sprintf("var%d", i)] = fmt.Sprintf("value-%d", i)
	}
	var in, want strings.Builder
	for i := 0; i < 200; i += 3 {
		fmt.Fprintf(&in, "%s{{var%d}};", strings.Repeat("x", 40), i)
		fmt.Fprintf(&want, "%svalue-%d;", strings.Repeat("x", 40), i)
	}
	if got := Interpolate(in.String(), vars); got != want.String() {
		t.Fatalf("interpolate mismatch over large body")
	}
}
