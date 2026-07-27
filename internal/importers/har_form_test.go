package importers

import (
	"testing"
)

// harFormFields sat at 37.5%. It reads a recorded form submission back into a
// request, and recorders DISAGREE about how they record one: some populate
// `params`, some only the raw `text`, some both. Reading only one of them loses
// the body for every recorder that used the other.

func fieldsFor(t *testing.T, postData *harPostData) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, field := range harFormFields(postData) {
		out[field.Name] = field.Value
	}
	return out
}

// The parsed params are preferred: they are already split, so they cannot be
// mis-split by a value that contains an ampersand.
func TestParsedParamsArePreferred(t *testing.T) {
	fields := fieldsFor(t, &harPostData{
		Params: []harNameValue{{Name: "a", Value: "1"}, {Name: "b", Value: "2"}},
		Text:   "ignored=yes",
	})
	if len(fields) != 2 || fields["a"] != "1" || fields["b"] != "2" {
		t.Errorf("got %#v", fields)
	}
	if _, leaked := fields["ignored"]; leaked {
		t.Error("the raw text was parsed as well as the params")
	}
}

// THE FALLBACK IS THE HALF THAT WAS UNTESTED. A recorder that fills only `text`
// is common, and without this the imported request has an empty body while the
// HAR plainly contains one.
func TestRawTextIsParsedWhenThereAreNoParams(t *testing.T) {
	fields := fieldsFor(t, &harPostData{Text: "user=alice&role=admin"})
	if fields["user"] != "alice" || fields["role"] != "admin" {
		t.Errorf("got %#v", fields)
	}
}

// Percent-encoding is undone on BOTH sides in both paths. A name left encoded
// does not match the field the server expects, and a value left encoded is sent
// double-escaped once the request is re-encoded on the way out.
func TestPercentEncodingIsDecodedInBothPaths(t *testing.T) {
	fromParams := fieldsFor(t, &harPostData{
		Params: []harNameValue{{Name: "q", Value: "a%20b"}},
	})
	if fromParams["q"] != "a b" {
		t.Errorf("params path: got %q", fromParams["q"])
	}

	fromText := fieldsFor(t, &harPostData{Text: "full%20name=a%20b"})
	if _, ok := fromText["full name"]; !ok {
		t.Errorf("text path did not decode the name: %#v", fromText)
	}
	if fromText["full name"] != "a b" {
		t.Errorf("text path: got %q", fromText["full name"])
	}
}

// A value that will not decode is kept AS WRITTEN rather than dropped. A stray
// "%" in recorded data is ordinary, and losing the field would silently change
// what the request sends.
func TestAnUndecodableValueIsKeptVerbatim(t *testing.T) {
	fromParams := fieldsFor(t, &harPostData{
		Params: []harNameValue{{Name: "raw", Value: "100%"}},
	})
	if fromParams["raw"] != "100%" {
		t.Errorf("params path: got %q, want the value unchanged", fromParams["raw"])
	}

	fromText := fieldsFor(t, &harPostData{Text: "raw=100%"})
	if fromText["raw"] != "100%" {
		t.Errorf("text path: got %q", fromText["raw"])
	}
}

// A nameless field cannot be sent as anything, and an empty name in the table
// renders as a blank row the user has to notice and delete.
func TestNamelessFieldsAreSkipped(t *testing.T) {
	fields := harFormFields(&harPostData{
		Params: []harNameValue{{Name: "", Value: "orphan"}, {Name: "  ", Value: "also"}, {Name: "kept", Value: "1"}},
	})
	if len(fields) != 1 || fields[0].Name != "kept" {
		t.Errorf("got %#v", fields)
	}
}

func TestNamelessFieldsAreSkippedInTheTextPath(t *testing.T) {
	fields := harFormFields(&harPostData{Text: "=orphan&kept=1&&"})
	if len(fields) != 1 || fields[0].Name != "kept" {
		t.Errorf("got %#v", fields)
	}
}

// A pair with no "=" is a name with an empty value, which is what a recorded
// checkbox or flag looks like.
func TestAPairWithNoEqualsIsANameWithNoValue(t *testing.T) {
	fields := harFormFields(&harPostData{Text: "flag"})
	if len(fields) != 1 {
		t.Fatalf("got %#v", fields)
	}
	if fields[0].Name != "flag" || fields[0].Value != "" {
		t.Errorf("got %#v", fields[0])
	}
}

// Every imported field arrives ENABLED. A field imported switched off is one
// the user has to find and tick before the request behaves as recorded.
func TestImportedFieldsAreEnabled(t *testing.T) {
	for _, postData := range []*harPostData{
		{Params: []harNameValue{{Name: "a", Value: "1"}}},
		{Text: "a=1"},
	} {
		for _, field := range harFormFields(postData) {
			if !field.Enabled {
				t.Errorf("%#v arrived disabled", field)
			}
		}
	}
}

func TestAnEmptyPostDataYieldsNoFields(t *testing.T) {
	if fields := harFormFields(&harPostData{}); len(fields) != 0 {
		t.Errorf("got %#v", fields)
	}
}

// End to end: a HAR whose form body is only in `text` must import with a
// populated body, which is the failure the fallback exists to prevent.
func TestHARWithTextOnlyFormBodyImportsItsFields(t *testing.T) {
	const har = `{
      "log": {
        "version": "1.2",
        "entries": [
          {
            "request": {
              "method": "POST",
              "url": "https://api.test/login",
              "headers": [{"name": "Content-Type", "value": "application/x-www-form-urlencoded"}],
              "postData": {
                "mimeType": "application/x-www-form-urlencoded",
                "text": "user=alice&role=admin"
              }
            }
          }
        ]
      }
    }`
	collection, _, err := ImportHAR(har, "recorded")
	if err != nil {
		t.Fatal(err)
	}
	if len(collection.Items) != 1 {
		t.Fatalf("got %d items", len(collection.Items))
	}
	body := collection.Items[0].Body
	if len(body.FormURLEncoded) == 0 {
		t.Fatalf("the form body was lost on import: %#v", body)
	}
	found := map[string]string{}
	for _, row := range body.FormURLEncoded {
		found[row.Name] = row.Value
	}
	if found["user"] != "alice" || found["role"] != "admin" {
		t.Errorf("got %#v", found)
	}
}
