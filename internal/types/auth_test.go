package types

import "testing"

// Where an API key actually goes.
//
// Both send paths — the HTTP builder and the WebSocket executor — compared
// APILocation to the bare string "query", so every other spelling fell through
// to the header branch. The folder-level auth editor stored "queryparams", so a
// folder configured to put its key in the query string sent it as a HEADER: no
// error, no warning, nothing in the app disagreeing with what the user had
// typed, and a 401 from the server as the only symptom.
//
// This is the kind of bug a UI audit finds and a unit test never would, because
// each half is self-consistent. It is pinned here so the two vocabularies
// cannot drift apart again.
func TestAPIKeyInQueryAcceptsEverySpellingThatHasBeenStored(t *testing.T) {
	// Every value in this list has appeared either in a stored collection or in
	// an importer's output.
	for _, location := range []string{"query", "queryparams", "queryparam", "url", "params"} {
		if !APIKeyInQuery(location) {
			t.Errorf("APIKeyInQuery(%q) = false, so a key meant for the query string would be sent as a header", location)
		}
	}
}

func TestAPIKeyInQueryIgnoresCaseAndSurroundingSpace(t *testing.T) {
	// Hand-edited .bru and .yaml files reach this field directly.
	for _, location := range []string{"Query", "QUERYPARAMS", "  query  ", "\tUrl\n"} {
		if !APIKeyInQuery(location) {
			t.Errorf("APIKeyInQuery(%q) = false", location)
		}
	}
}

func TestAPIKeyDefaultsToTheHeaderForAnythingElse(t *testing.T) {
	// The default matters, and it is deliberately the header: a key in a header
	// that the server ignores is a failed request, while a key appended to a URL
	// travels into access logs, referrers and browser history. An unrecognised
	// value must never be guessed into the query string.
	for _, location := range []string{"", "header", "headers", "body", "cookie", "somethingNew"} {
		if APIKeyInQuery(location) {
			t.Errorf("APIKeyInQuery(%q) = true, putting a key in the URL on an unrecognised value", location)
		}
	}
}
