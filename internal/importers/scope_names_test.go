// OAuth2 scope names read from an OpenAPI security scheme.
//
// Coverage found this at 0%. It turns the `scopes` object of an OAuth2 flow
// into the space-separated scope string a token request sends. Get it wrong and
// an imported request asks for the wrong scopes — the token endpoint either
// refuses, or worse issues a token missing a permission, and the failure shows
// up later as a 403 on one endpoint rather than an auth error.
//
// The sort is the part worth pinning. Go map iteration is RANDOM, so without it
// the scope string would differ between two imports of the same spec: the
// request would be non-deterministic on disk and every save would produce a
// spurious diff.
package importers

import (
	"strings"
	"testing"
)

func TestOpenAPIScopeNamesAreSortedForDeterminism(t *testing.T) {
	raw := map[string]interface{}{
		"write:pets": "modify pets",
		"read:pets":  "read pets",
		"admin":      "everything",
	}

	first := strings.Join(openAPIScopeNames(raw), " ")
	if first != "admin read:pets write:pets" {
		t.Fatalf("got %q, want the scopes in sorted order", first)
	}

	// Repeat: Go map iteration order is randomised per range, so an unsorted
	// implementation would eventually disagree with itself here.
	for i := 0; i < 50; i++ {
		if got := strings.Join(openAPIScopeNames(raw), " "); got != first {
			t.Fatalf("iteration %d produced %q, want %q — the scope string must not depend on map order", i, got, first)
		}
	}
}

func TestOpenAPIScopeNamesHandlesEmptyAndNonMaps(t *testing.T) {
	if got := openAPIScopeNames(map[string]interface{}{}); len(got) != 0 {
		t.Errorf("an empty scopes object should give no names, got %v", got)
	}
	if got := openAPIScopeNames(nil); got != nil {
		t.Errorf("nil should give nil, got %v", got)
	}
	if got := openAPIScopeNames("not a map"); got != nil {
		t.Errorf("a non-map should give nil, got %v", got)
	}
}

// YAML decoders hand nested mappings back as map[interface{}]interface{}, which
// a plain type assertion misses. A spec loaded from YAML rather than JSON would
// otherwise import with no scopes at all.
func TestOpenAPIScopeNamesAcceptsYAMLDecodedMaps(t *testing.T) {
	got := openAPIScopeNames(map[interface{}]interface{}{
		"read:pets":  "read",
		"write:pets": "write",
	})
	if strings.Join(got, " ") != "read:pets write:pets" {
		t.Fatalf("got %v; a YAML-decoded scopes map must be read too", got)
	}
}
