// Reading loosely-typed values out of a Postman collection.
//
// A .postman_collection.json is not a schema-checked document. The same field is
// a bool in one export, the string "true" in another, and an array of strings in
// a third, because the values came from a UI that never normalised them. These
// helpers absorb that, and each one fails by producing a plausible wrong answer
// rather than an error.
package importers

import (
	"testing"

	"LiteAPI/internal/types"
)

// The auth "values" block is either an object or Postman's list-of-{key,value}
// form, depending on the export's schema version. An importer that reads only
// one form silently loses every auth setting from the other.
func TestPostmanAuthValuesReadBothDocumentShapes(t *testing.T) {
	object := map[string]interface{}{"addTokenTo": "header"}
	list := []interface{}{
		map[string]interface{}{"key": "other", "value": "x"},
		map[string]interface{}{"key": "addTokenTo", "value": "header"},
	}
	for name, values := range map[string]interface{}{"object": object, "list": list} {
		raw, ok := postmanAuthRawValue(values, "addTokenTo")
		if !ok || raw != "header" {
			t.Errorf("%s form: got %v ok=%v", name, raw, ok)
		}
	}
}

func TestPostmanAuthValueMissingKeyIsNotFound(t *testing.T) {
	if _, ok := postmanAuthRawValue(map[string]interface{}{"a": 1}, "b"); ok {
		t.Error("a missing key reported as found")
	}
	if _, ok := postmanAuthRawValue([]interface{}{map[string]interface{}{"key": "a"}}, "b"); ok {
		t.Error("a missing key reported as found in the list form")
	}
	if _, ok := postmanAuthRawValue("not a container", "b"); ok {
		t.Error("a scalar reported as containing the key")
	}
}

// Non-map entries appear in hand-edited exports. Skipping them keeps the rest of
// the auth block; aborting would drop settings that parsed fine.
func TestPostmanAuthValueSkipsMalformedListEntries(t *testing.T) {
	raw, ok := postmanAuthRawValue([]interface{}{"junk", nil, 7, map[string]interface{}{"key": "k", "value": "v"}}, "k")
	if !ok || raw != "v" {
		t.Errorf("got %v ok=%v", raw, ok)
	}
}

// Every spelling of yes and no that has turned up in a real export. Reading one
// of these as its fallback flips a security-relevant switch — "disable URL
// encoding" or "add token to header" — without saying anything.
func TestPostmanAuthBoolAcceptsEverySpellingOfTrueAndFalse(t *testing.T) {
	for _, raw := range []interface{}{true, "true", "TRUE", " true ", "1", 1, "yes", "on", "YES"} {
		if !postmanAuthBoolValue(map[string]interface{}{"k": raw}, "k", false) {
			t.Errorf("%#v did not read as true", raw)
		}
	}
	for _, raw := range []interface{}{false, "false", "FALSE", " false ", "0", 0, "no", "off", "NO"} {
		if postmanAuthBoolValue(map[string]interface{}{"k": raw}, "k", true) {
			t.Errorf("%#v did not read as false", raw)
		}
	}
}

// The fallback is the setting's Postman default. Guessing true or false for an
// unrecognised value would change behaviour the export never asked to change.
func TestPostmanAuthBoolFallsBackWhenAbsentOrUnreadable(t *testing.T) {
	if !postmanAuthBoolValue(map[string]interface{}{}, "missing", true) {
		t.Error("an absent key must take the fallback")
	}
	if postmanAuthBoolValue(map[string]interface{}{}, "missing", false) {
		t.Error("an absent key must take the fallback")
	}
	// A key present but explicitly null is the same case: the export said
	// nothing about the setting, so the default stands.
	if !postmanAuthBoolValue(map[string]interface{}{"k": nil}, "k", true) {
		t.Error("an explicit null must take the fallback")
	}
	if !postmanAuthBoolValue(map[string]interface{}{"k": "maybe"}, "k", true) {
		t.Error("an unreadable value must take the fallback, not a guess")
	}
	if postmanAuthBoolValue(map[string]interface{}{"k": []interface{}{1}}, "k", false) {
		t.Error("an unreadable value must take the fallback, not a guess")
	}
}

// Postman stores a multi-file form field's src as an ARRAY of paths. Reading it
// with a plain string conversion yields Go's "[a b]" rendering, which becomes a
// file path that cannot exist.
func TestPostmanFormDataStringJoinsArraySources(t *testing.T) {
	got := postmanFormDataString([]interface{}{"/tmp/a", "/tmp/b"})
	if got != "/tmp/a/tmp/b" {
		t.Errorf("got %q", got)
	}
	if got == "[/tmp/a /tmp/b]" {
		t.Fatal("rendered the slice with Go syntax; that path cannot exist")
	}
}

func TestPostmanFormDataStringHandlesScalarsAndNil(t *testing.T) {
	if got := postmanFormDataString("/tmp/a"); got != "/tmp/a" {
		t.Errorf("string gave %q", got)
	}
	if got := postmanFormDataString(nil); got != "" {
		t.Errorf("nil gave %q", got)
	}
}

// For a REQUEST body the editor language wins over the header: it is what the
// user picked in Postman's body editor, and it is common for the Content-Type
// header to be absent or left on a stale value.
func TestPostmanRequestBodyModePrefersTheEditorLanguage(t *testing.T) {
	headers := []types.KeyValue{{Name: "Content-Type", Value: "text/plain", Enabled: true}}
	if got := postmanRawBodyMode(headers, "json", `{"a":1}`); got != "json" {
		t.Errorf("got %q, want the editor language to win", got)
	}
	// The raw body here is valid JSON on purpose. With an empty body every
	// language ends up at "text" through the sniffing fallback, so the test
	// would pass whether or not the language was recognised at all — I had it
	// that way first and the control proved it measured nothing.
	for language, want := range map[string]string{"json": "json", "xml": "xml", "text": "text", "html": "text", "javascript": "text", "JSON": "json"} {
		if got := postmanRawBodyMode(nil, language, `{"a":1}`); got != want {
			t.Errorf("%s -> %q, want %q", language, got, want)
		}
	}
}

func TestPostmanRequestBodyModeFallsBackToTheHeaderThenTheContent(t *testing.T) {
	headers := []types.KeyValue{{Name: "Content-Type", Value: "application/json", Enabled: true}}
	if got := postmanRawBodyMode(headers, "", "anything"); got != "json" {
		t.Errorf("header: got %q", got)
	}
	if got := postmanRawBodyMode(nil, "", `{"a":1}`); got != "json" {
		t.Errorf("json sniff: got %q", got)
	}
	if got := postmanRawBodyMode(nil, "", "  <root/>  "); got != "xml" {
		t.Errorf("xml sniff: got %q", got)
	}
	// Not "json": an empty body is not a JSON document, and calling it json
	// would make the editor open in a mode the request has no content for.
	if got := postmanRawBodyMode(nil, "", ""); got != "text" {
		t.Errorf("empty: got %q", got)
	}
	if got := postmanRawBodyMode(nil, "", "hello"); got != "text" {
		t.Errorf("plain: got %q", got)
	}
}

// For a RESPONSE the header wins, which is the opposite order. That is
// deliberate: Content-Type is what the server actually declared, while the
// preview language is only how Postman chose to display it.
func TestPostmanResponseBodyTypePrefersTheServersContentType(t *testing.T) {
	headers := []types.KeyValue{{Name: "Content-Type", Value: "application/xml", Enabled: true}}
	if got := postmanResponseBodyType(headers, "json", `{"a":1}`); got != "xml" {
		t.Errorf("got %q, want the declared content type to win for a response", got)
	}
}

func TestPostmanResponseBodyTypeFallsBackToPreviewThenContent(t *testing.T) {
	for previewLanguage, want := range map[string]string{"json": "json", "xml": "xml", "JSON": "json", "html": "text"} {
		if got := postmanResponseBodyType(nil, previewLanguage, "plain"); got != want {
			t.Errorf("%s -> %q, want %q", previewLanguage, got, want)
		}
	}
	if got := postmanResponseBodyType(nil, "", `{"a":1}`); got != "json" {
		t.Errorf("json sniff: got %q", got)
	}
	if got := postmanResponseBodyType(nil, "", "<root/>"); got != "xml" {
		t.Errorf("xml sniff: got %q", got)
	}
	if got := postmanResponseBodyType(nil, "", ""); got != "text" {
		t.Errorf("empty: got %q", got)
	}
}
