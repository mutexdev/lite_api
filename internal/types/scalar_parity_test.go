package types

import (
	"strings"
	"testing"

	"github.com/mutexdev/lite_api/internal/scalar"
)

// This package carries its own copies of three scalar helpers on purpose:
// types is the leaf everything else depends on, and importing scalar for a few
// lines would put a package between it and the rest. The copies' comments say
// they "mirror" the originals, and until now nothing checked that they still
// did.
//
// Drift here is silent and not local. deterministicIDLocal in particular feeds
// the stable ids that identify examples across a reload or a re-import; if the
// two implementations ever disagree, the SAME entity gets two different
// "deterministic" ids depending on which package computed it, and the identity
// matching that depends on them stops matching without anything failing.
//
// The import below is test-only, so the production dependency graph is
// unchanged and the layering argument still holds — scalar does not import
// types, so there is no cycle either way.

func TestFirstNonEmptyMatchesScalar(t *testing.T) {
	cases := [][]string{
		{},
		{""},
		{"", "  ", "\t"},
		{"", "value"},
		{"first", "second"},
		{"  ", "  padded  ", "later"},
		{"\n", " ", "x"},
		{"0"},
	}
	for _, values := range cases {
		local := firstNonEmpty(values...)
		original := scalar.FirstNonEmpty(values...)
		if local != original {
			t.Errorf("firstNonEmpty(%q) = %q but scalar.FirstNonEmpty = %q", values, local, original)
		}
	}
}

// The ids must be equal for the same input, not merely similarly shaped: they
// are compared against each other, so a differing prefix separator or digest
// length is a mismatch even though both would still "look like" an id.
func TestDeterministicIDMatchesScalar(t *testing.T) {
	prefixes := []string{"example", "req", "", "folder-nested"}
	inputs := []string{
		"",
		"a",
		"collection#example#0",
		"collection#example#1",
		strings.Repeat("x", 4096),
		"unicode é中文 \U0001f600",
		"with\x00a null byte",
	}
	for _, prefix := range prefixes {
		for _, input := range inputs {
			local := deterministicIDLocal(prefix, input)
			original := scalar.DeterministicID(prefix, input)
			if local != original {
				t.Errorf("deterministicIDLocal(%q, %q) = %q but scalar.DeterministicID = %q",
					prefix, input, local, original)
			}
		}
	}
}

// Distinct inputs must still give distinct ids — a parity test alone would pass
// if BOTH implementations degenerated to a constant.
func TestDeterministicIDDistinguishesItsInputs(t *testing.T) {
	seen := map[string]string{}
	for _, input := range []string{"a", "b", "collection#example#0", "collection#example#1"} {
		id := deterministicIDLocal("example", input)
		if previous, clash := seen[id]; clash {
			t.Errorf("inputs %q and %q both produced id %q", previous, input, id)
		}
		seen[id] = input
	}
	if got := deterministicIDLocal("example", "a"); got != deterministicIDLocal("example", "a") {
		t.Error("the same input produced two different ids, so it is not deterministic")
	}
}

// newIDLocal is time-based, so the two cannot be compared by value. What must
// agree is the SHAPE, since both feed the same id space: the prefix, the
// separator, and a decimal nanosecond timestamp.
func TestNewIDMatchesScalarInShape(t *testing.T) {
	for _, prefix := range []string{"var", "global-env", ""} {
		local := newIDLocal(prefix)
		original := scalar.NewID(prefix)

		// Split at the LAST separator: a prefix may itself contain one
		// ("global-env"), and cutting at the first would attribute part of the
		// prefix to the timestamp.
		localPrefix, localSuffix, localOK := cutLast(local, "-")
		originalPrefix, originalSuffix, originalOK := cutLast(original, "-")
		if !localOK || !originalOK {
			t.Fatalf("neither id should be missing its separator: %q and %q", local, original)
		}
		if prefix != "" && (localPrefix != prefix || originalPrefix != prefix) {
			t.Errorf("prefixes differ: local %q, original %q, want %q", localPrefix, originalPrefix, prefix)
		}
		if localPrefix != originalPrefix {
			t.Errorf("prefix handling differs: local %q against original %q", localPrefix, originalPrefix)
		}
		if !isDecimalDigits(localSuffix) || !isDecimalDigits(originalSuffix) {
			t.Errorf("suffixes should both be decimal timestamps: local %q, original %q", localSuffix, originalSuffix)
		}
		if len(localSuffix) != len(originalSuffix) {
			t.Errorf("timestamp widths differ: local %q against original %q", localSuffix, originalSuffix)
		}
	}
}

func isDecimalDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// cutLast is strings.Cut anchored at the final separator.
func cutLast(value, sep string) (string, string, bool) {
	index := strings.LastIndex(value, sep)
	if index < 0 {
		return value, "", false
	}
	return value[:index], value[index+len(sep):], true
}
