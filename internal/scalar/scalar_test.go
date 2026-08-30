// The shared primitives.
//
// internal/scalar is the leaf that almost every other package depends on, and
// it had no tests of its own. Negative control found ShellSingleQuote entirely
// unverified: replacing its escaping with a plain pair of quotes failed nothing.
//
// That one matters beyond correctness. It quotes values into the curl and
// grpcurl commands the app generates for the user to paste into a shell, so a
// value containing a quote does not merely render oddly -- it ends the quoted
// string and the rest is read by the shell as syntax.
package scalar

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestShellSingleQuoteContainsEmbeddedQuotes(t *testing.T) {
	// The POSIX idiom: close the quote, emit an escaped quote, reopen.
	if got, want := ShellSingleQuote("it's"), `'it'"'"'s'`; got != want {
		t.Fatalf("ShellSingleQuote(%q) = %s, want %s", "it's", got, want)
	}
}

// The value a user could actually supply: a header or filename crafted to end
// the quoted argument and append a command.
//
// The assertion is a ROUND TRIP, not a substring search. My first attempt
// checked that the result did not contain "'; rm" -- but the escape sequence
// legitimately contains those bytes, so the test failed on correct code. What
// matters is what a shell would parse the result back into, so the test parses
// it the way a shell would.
func TestShellSingleQuoteNeutralisesAnInjectionAttempt(t *testing.T) {
	for _, payload := range []string{
		`'; rm -rf /; echo '`,
		`" ; touch /tmp/x ; "`,
		"$(whoami)",
		"`whoami`",
		"a'b",
		"''",
	} {
		if got := shellUnquote(ShellSingleQuote(payload)); got != payload {
			t.Errorf("ShellSingleQuote(%q) parses back as %q", payload, got)
		}
	}
}

// shellUnquote parses a POSIX shell word made only of quoted and bare segments,
// which is all ShellSingleQuote can produce. It is deliberately not built from
// anything in this package.
func shellUnquote(word string) string {
	var out strings.Builder
	for i := 0; i < len(word); i++ {
		switch word[i] {
		case '\'':
			i++
			for i < len(word) && word[i] != '\'' {
				out.WriteByte(word[i])
				i++
			}
		case '"':
			i++
			for i < len(word) && word[i] != '"' {
				out.WriteByte(word[i])
				i++
			}
		default:
			out.WriteByte(word[i])
		}
	}
	return out.String()
}

func TestShellSingleQuoteLeavesOrdinaryValuesAlone(t *testing.T) {
	for _, value := range []string{"plain", "with spaces", `{"a":1}`, "héllo 世界", ""} {
		got := ShellSingleQuote(value)
		if got != "'"+value+"'" {
			t.Errorf("ShellSingleQuote(%q) = %s, want %q", value, got, "'"+value+"'")
		}
	}
}

func TestSanitizeFilenameRemovesPathAndReservedCharacters(t *testing.T) {
	for input, want := range map[string]string{
		"normal":           "normal",
		"a/b":              "a-b",
		`a\b`:              "a-b",
		"a:b":              "a-b",
		"a*b":              "a-b",
		"a|b":              "a-b",
		`a?b"c<d>e`:        "abcde",
		"  spaced  ":       "spaced",
		"..":               "untitled",
		"":                 "untitled",
		"   ":              "untitled",
		"../../etc/passwd": "-..-etc-passwd",
	} {
		if got := SanitizeFilename(input); got != want {
			t.Errorf("SanitizeFilename(%q) = %q, want %q", input, got, want)
		}
	}
}

// A name that sanitises to nothing must not yield an empty path segment, which
// would write to the containing directory instead of a file inside it.
func TestSanitizeFilenameNeverReturnsEmpty(t *testing.T) {
	for _, input := range []string{"", " ", ".", "..", "...", `?"<>`, " . . "} {
		if got := SanitizeFilename(input); got == "" {
			t.Errorf("SanitizeFilename(%q) returned an empty name", input)
		}
	}
}

func TestDeterministicIDIsStableAndInputDependent(t *testing.T) {
	first := DeterministicID("req", "some/path")
	if first != DeterministicID("req", "some/path") {
		t.Fatal("the same input produced two different ids")
	}
	if first == DeterministicID("req", "other/path") {
		t.Fatal("different inputs produced the same id")
	}
	if !strings.HasPrefix(first, "req-") {
		t.Fatalf("id %q does not carry its prefix", first)
	}
}

func TestFirstNonEmptySkipsBlanksNotJustEmpties(t *testing.T) {
	if got := FirstNonEmpty("", "   ", "\t\n", "value", "later"); got != "value" {
		t.Fatalf("got %q", got)
	}
	if got := FirstNonEmpty("", "  "); got != "" {
		t.Fatalf("all-blank input should give %q, got %q", "", got)
	}
}

func TestNormalizeWhitespaceCollapsesRuns(t *testing.T) {
	for input, want := range map[string]string{
		"  a   b  ": "a b",
		"a\t\tb":    "a b",
		"a\n\nb":    "a b",
		"single":    "single",
		"   ":       "",
	} {
		if got := NormalizeWhitespace(input); got != want {
			t.Errorf("NormalizeWhitespace(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestLooksLikeJSONChecksTheFirstMeaningfulCharacter(t *testing.T) {
	for input, want := range map[string]bool{
		`{"a":1}`:    true,
		"  [1,2]":    true,
		"\n\t{}":     true,
		"plain text": false,
		"":           false,
		`"a string"`: false,
	} {
		if got := LooksLikeJSON(input); got != want {
			t.Errorf("LooksLikeJSON(%q) = %v, want %v", input, got, want)
		}
	}
}

// Decoded YAML hands back map[interface{}]interface{} for nested mappings,
// which a plain type assertion to map[string]interface{} misses entirely.
func TestMapAcceptsBothDecodedYAMLShapes(t *testing.T) {
	if _, ok := Map(map[string]interface{}{"a": 1}); !ok {
		t.Error("a string-keyed map should be accepted")
	}
	out, ok := Map(map[interface{}]interface{}{"a": 1, 2: "b"})
	if !ok {
		t.Fatal("an interface-keyed map should be accepted and converted")
	}
	if out["a"] != 1 || out["2"] != "b" {
		t.Fatalf("keys were not stringified: %#v", out)
	}
	if _, ok := Map("not a map"); ok {
		t.Error("a non-map should be rejected")
	}
}

func TestFirstMapValueTakesTheFirstPresentKey(t *testing.T) {
	raw := map[string]interface{}{"second": 2, "third": 3}
	if got := FirstMapValue(raw, "first", "second", "third"); got != 2 {
		t.Fatalf("got %v, want 2", got)
	}
	if got := FirstMapValue(raw, "missing"); got != nil {
		t.Fatalf("got %v, want nil", got)
	}
}

func TestFirstYAMLStringSkipsBlankValues(t *testing.T) {
	raw := map[string]interface{}{"a": "  ", "b": "value"}
	if got := FirstYAMLString(raw, "a", "b"); got != "value" {
		t.Fatalf("got %q, want %q", got, "value")
	}
}

// US-057. A request name long enough to exceed the filesystem's per-component
// limit failed the entire import batch with "selected imports could not be
// committed", because SanitizeFilename passed the name through at any length.
// Around 85 CJK characters is enough on ext4 and APFS alike, and a Postman
// collection whose request names are full sentences reaches that easily.
func TestSanitizeFilenameFitsWithinAFilesystemComponent(t *testing.T) {
	for _, input := range []string{
		strings.Repeat("n", 400),
		strings.Repeat("集", 200),
		strings.Repeat("é", 150),
	} {
		got := SanitizeFilename(input)
		if len(got) > SanitizeFilenameMaxBytes {
			t.Errorf("SanitizeFilename(%d chars) returned %d bytes", len([]rune(input)), len(got))
		}
		if !utf8.ValidString(got) {
			t.Errorf("SanitizeFilename(%d chars) cut a rune in half: %q", len([]rune(input)), got)
		}
	}
}

func TestSanitizeFilenameKeepsLongNamesDistinct(t *testing.T) {
	base := strings.Repeat("n", 300)
	first, second := SanitizeFilename(base+"alpha"), SanitizeFilename(base+"beta")
	if first == second {
		t.Fatalf("two long names collapsed to one filename: %q", first)
	}
	if SanitizeFilename(base+"alpha") != first {
		t.Fatal("truncation is not deterministic")
	}
}

func TestSanitizeFilenameLeavesShortNamesUntouched(t *testing.T) {
	for _, input := range []string{"Get user", "list-items", "集合"} {
		if got := SanitizeFilename(input); got != input {
			t.Errorf("SanitizeFilename(%q) = %q", input, got)
		}
	}
}

// Windows refuses to create a file named for a DOS device, whatever the
// extension. A Postman collection with a request named "CON" imports on Linux
// and then fails to open on the Windows build of the same app.
func TestSanitizeFilenameAvoidsWindowsDeviceNames(t *testing.T) {
	for _, input := range []string{"CON", "con", "PRN", "AUX", "NUL", "COM1", "lpt9"} {
		if got := SanitizeFilename(input); strings.EqualFold(got, input) {
			t.Errorf("SanitizeFilename(%q) = %q, still a reserved device name", input, got)
		}
	}
	if got := SanitizeFilename("CONTENT"); got != "CONTENT" {
		t.Errorf("SanitizeFilename(%q) = %q, a name that merely starts with one", "CONTENT", got)
	}
}
