package bru

import (
	"testing"

	"github.com/mutexdev/lite_api/internal/codegen"
)

// bru.NormalizeBodyMode and codegen.NormalizedBodyMode differ by ONE LETTER in
// their names and by two inputs in their behaviour. This repo already carries a
// scar from that resemblance: yamlstore_test.go records a mechanical rewrite
// that pointed a third body-mode call site at codegen's version because the
// names were near-identical, which COMPILED and passed every test.
//
// This makes the distinction executable rather than commented, so the next such
// rewrite fails here instead of in a user's collection.

// The two inputs where they must NOT agree.
func TestBruAndCodegenBodyModesDifferWhereTheyShould(t *testing.T) {
	// A "ws" body mode is the WebSocket message form. The executor's vocabulary
	// has no such mode — it sends text — so the bru reader folds it, while
	// codegen leaves it alone because a generated snippet names the mode the
	// user chose.
	if got := NormalizeBodyMode("ws"); got != "text" {
		t.Errorf("bru NormalizeBodyMode(ws) = %q, want text", got)
	}
	if got := codegen.NormalizedBodyMode("ws"); got != "ws" {
		t.Errorf("codegen NormalizedBodyMode(ws) = %q, want ws unchanged", got)
	}
	if NormalizeBodyMode("ws") == codegen.NormalizedBodyMode("ws") {
		t.Error("the two normalisers agreed on ws; one of them has been redirected at the other")
	}

	// bru matches its own cases case-INSENSITIVELY; codegen is case-sensitive.
	// A hand-edited .bru file with "GraphQL" must still load as graphql.
	if got := NormalizeBodyMode("GraphQL"); got != "graphql" {
		t.Errorf("bru NormalizeBodyMode(GraphQL) = %q, want graphql", got)
	}
	if got := codegen.NormalizedBodyMode("GraphQL"); got != "GraphQL" {
		t.Errorf("codegen NormalizedBodyMode(GraphQL) = %q, want it unchanged", got)
	}

	// The case-sensitivity difference has to be asserted on an input where it
	// SHOWS. "GraphQL" passes through codegen unchanged whether or not codegen
	// folds case, because its default arm returns the original either way — so
	// that assertion alone could not catch codegen being made case-insensitive.
	// A form alias can: codegen matches "formUrlEncoded" exactly and leaves
	// "FormUrlEncoded" alone.
	if got := codegen.NormalizedBodyMode("FormUrlEncoded"); got != "FormUrlEncoded" {
		t.Errorf("codegen NormalizedBodyMode(FormUrlEncoded) = %q — codegen is no longer case-sensitive", got)
	}
	if got := codegen.NormalizedBodyMode("formUrlEncoded"); got != "formUrlEncoded" {
		t.Errorf("codegen lost its exact-case alias: %q", got)
	}
}

// Everywhere else they agree, and that is deliberate too: bru DELEGATES to
// codegen for the form-encoding aliases rather than restating them, so a new
// alias added in one place is understood in both. A test that only checked the
// differences would let that delegation be replaced by a copy.
func TestBruDelegatesToCodegenForEverythingElse(t *testing.T) {
	for _, mode := range []string{
		"", "none", "json", "JSON", "text", "sparql", "grpc", "file", "binary",
		"form-url-encoded", "formUrlEncoded", "formurlencoded", "urlencoded",
		"multipart-form", "multipartForm", "multipart", "MULTIPART",
		"x-www-form-urlencoded", "unknown-thing", "  json  ",
	} {
		if got, want := NormalizeBodyMode(mode), codegen.NormalizedBodyMode(mode); got != want {
			t.Errorf("%q: bru = %q, codegen = %q — they diverged outside the two documented cases", mode, got, want)
		}
	}
}

// The form aliases resolve identically through both, which is what the
// delegation buys.
func TestTheFormAliasesResolveThroughBothPaths(t *testing.T) {
	for _, alias := range []string{"form-url-encoded", "formUrlEncoded"} {
		if got := NormalizeBodyMode(alias); got != "formUrlEncoded" {
			t.Errorf("%q resolved to %q", alias, got)
		}
	}
	for _, alias := range []string{"multipart-form", "multipartForm", "multipart"} {
		if got := NormalizeBodyMode(alias); got != "multipartForm" {
			t.Errorf("%q resolved to %q", alias, got)
		}
	}
}
