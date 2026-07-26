// Serialising a scripted body into application/x-www-form-urlencoded.
//
// scriptFormURLEncodedBody was at 8.3%. It runs when a script assigns
// pm.request.body while the content type is form-urlencoded, and it accepts
// whatever JavaScript shape the script happened to produce: a string, an array
// of {name, value} rows, or a plain object.
//
// Nothing here can fail loudly. A wrong serialisation produces a body the server
// parses into the wrong fields, and the request comes back 400 or — worse — 200
// having done something other than what the script asked.
package scripting

import "testing"

func TestScriptFormBodyPassesStringsThrough(t *testing.T) {
	// The script has already encoded it. Re-encoding would turn the separators
	// into literal %3D and %26 and produce one giant field name.
	if got := scriptFormURLEncodedBody("a=1&b=2"); got != "a=1&b=2" {
		t.Errorf("got %q, want the string untouched", got)
	}
}

func TestScriptFormBodyOfNilIsEmpty(t *testing.T) {
	if got := scriptFormURLEncodedBody(nil); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

// The array form is what pm.request.body looks like when a script builds rows
// rather than an object.
func TestScriptFormBodyEncodesNameValueRows(t *testing.T) {
	got := scriptFormURLEncodedBody([]interface{}{
		map[string]interface{}{"name": "user", "value": "ada"},
		map[string]interface{}{"name": "role", "value": "admin"},
	})
	if got != "user=ada&role=admin" {
		t.Errorf("got %q", got)
	}
}

// Rows arrive from JavaScript, so a stray null or number in the array is
// possible. Skipping it keeps the other rows; failing the whole body would lose
// fields the script did set correctly.
func TestScriptFormBodySkipsNonObjectRows(t *testing.T) {
	got := scriptFormURLEncodedBody([]interface{}{
		"not-a-row", nil, 42,
		map[string]interface{}{"name": "k", "value": "v"},
	})
	if got != "k=v" {
		t.Errorf("got %q, want the one valid row", got)
	}
}

func TestScriptFormBodyRowsWithMissingFieldsEncodeAsEmpty(t *testing.T) {
	got := scriptFormURLEncodedBody([]interface{}{map[string]interface{}{"name": "k"}})
	if got != "k=" {
		t.Errorf("got %q, want an empty value rather than a dropped field", got)
	}
}

// Go map iteration is randomised. Without the sort, the same script would
// produce a different body on every run — which breaks request signing, any
// caching keyed on the body, and every test anyone writes against it.
func TestScriptFormBodyOrdersObjectKeysDeterministically(t *testing.T) {
	body := map[string]interface{}{"zebra": "1", "alpha": "2", "middle": "3", "beta": "4"}
	first := scriptFormURLEncodedBody(body)
	if first != "alpha=2&beta=4&middle=3&zebra=1" {
		t.Errorf("got %q, want keys in sorted order", first)
	}
	for i := 0; i < 50; i++ {
		if again := scriptFormURLEncodedBody(body); again != first {
			t.Fatalf("run %d produced %q, first run produced %q", i, again, first)
		}
	}
}

// A repeated key is how a form expresses a multi-value field. Encoding the array
// as one value would send the literal "[a b]" as the field's text.
func TestScriptFormBodyRepeatsTheKeyForArrayValues(t *testing.T) {
	got := scriptFormURLEncodedBody(map[string]interface{}{"tag": []interface{}{"x", "y", "z"}})
	if got != "tag=x&tag=y&tag=z" {
		t.Errorf("got %q", got)
	}
}

func TestScriptFormBodyEncodesNonStringValues(t *testing.T) {
	got := scriptFormURLEncodedBody(map[string]interface{}{"n": 42, "ok": true, "empty": nil})
	if got != "empty=&n=42&ok=true" {
		t.Errorf("got %q", got)
	}
}

// url.QueryEscape writes a space as "+", which is legal in a query string but
// wrong in a body that a server may read either way. %20 is unambiguous.
func TestScriptFormBodyEncodesSpacesAsPercent20(t *testing.T) {
	got := scriptFormURLEncodedBody(map[string]interface{}{"full name": "Ada Lovelace"})
	if got != "full%20name=Ada%20Lovelace" {
		t.Errorf("got %q, want spaces as %%20 rather than +", got)
	}
}

// A value containing the separators must not be able to introduce a field.
func TestScriptFormBodyEscapesSeparatorsInValues(t *testing.T) {
	got := scriptFormURLEncodedBody(map[string]interface{}{"q": "a=1&b=2"})
	if got != "q=a%3D1%26b%3D2" {
		t.Errorf("got %q; an unescaped separator lets a value forge extra fields", got)
	}
}

func TestScriptFormBodyOfEmptyContainersIsEmpty(t *testing.T) {
	if got := scriptFormURLEncodedBody(map[string]interface{}{}); got != "" {
		t.Errorf("empty object gave %q", got)
	}
	if got := scriptFormURLEncodedBody([]interface{}{}); got != "" {
		t.Errorf("empty array gave %q", got)
	}
}

// Anything else — a bare number or boolean assigned to the body — is stringified
// rather than dropped, so the script's intent still reaches the wire.
func TestScriptFormBodyStringifiesOtherScalars(t *testing.T) {
	if got := scriptFormURLEncodedBody(42); got != "42" {
		t.Errorf("got %q", got)
	}
	if got := scriptFormURLEncodedBody(true); got != "true" {
		t.Errorf("got %q", got)
	}
}
