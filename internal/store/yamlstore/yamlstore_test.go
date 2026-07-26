// YAML body-mode canonicalisation.
//
// WHY THESE EXIST: while extracting this package, a mechanical rewrite pointed
// these call sites at codegen.NormalizedBodyMode, because the two functions had
// near-identical names. They are not the same function. This one canonicalises
// the whole YAML vocabulary -- "" becomes "none", four spellings of urlencoded
// collapse to one, and anything unrecognised becomes "text". codegen's rewrites
// two form modes and passes everything else through.
//
// The redirect COMPILED. It was caught only because the same rewrite also
// renamed the declaration and broke the syntax; had it renamed one fewer thing,
// a request with no body would have started loading with mode "" instead of
// "none", and every test still passed -- I checked, by making that exact change.
package yamlstore

import "testing"

func TestBodyModeCanonicalisation(t *testing.T) {
	for input, want := range map[string]string{
		"":       "none",
		"none":   "none",
		"NONE":   "none",
		" json ": "json",
		"xml":    "xml",
		"sparql": "sparql",
		"grpc":   "grpc",

		// Four spellings reach the same mode. A reader that passes one of them
		// through unchanged produces a request whose body silently does not send.
		"form-urlencoded":       "formUrlEncoded",
		"formurlencoded":        "formUrlEncoded",
		"urlencoded":            "formUrlEncoded",
		"x-www-form-urlencoded": "formUrlEncoded",

		"multipart-form": "multipartForm",
		"multipartform":  "multipartForm",
		"multipart":      "multipartForm",

		"file":   "file",
		"binary": "file",

		// Unrecognised means text, not the raw value: an unknown mode reaching
		// the request builder is a mode nothing knows how to encode.
		"something-new": "text",
		"TEXT":          "text",
	} {
		if got := normalizeYAMLBodyMode(input); got != want {
			t.Errorf("normalizeYAMLBodyMode(%q) = %q, want %q", input, got, want)
		}
	}
}

// The distinction that the near-miss turned on, stated directly so it cannot be
// collapsed again without a test failing.
func TestEmptyBodyModeIsNoneNotEmpty(t *testing.T) {
	if got := normalizeYAMLBodyMode(""); got != "none" {
		t.Fatalf("an absent body mode must canonicalise to %q, got %q", "none", got)
	}
}

func TestUnknownBodyModeFallsBackToText(t *testing.T) {
	if got := normalizeYAMLBodyMode("no-such-mode"); got != "text" {
		t.Fatalf("an unknown body mode must fall back to %q, got %q", "text", got)
	}
}
