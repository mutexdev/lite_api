package scripting

// Postman request-body definitions passed to pm.sendRequest().
//
// bru.sendRequest and pm.sendRequest are THE SAME FUNCTION (see postman.go —
// pm.sendRequest is an alias, deliberately, so that the timeline and the
// recursion-depth limit cannot be bypassed by using the other name). That
// aliasing is right, but the two APIs do not describe a request body the same
// way, and until this file existed only Bruno's way was understood:
//
//	bru.sendRequest({ data: { a: 1 } })            // axios — JSON-encode it
//	pm.sendRequest({ body: { mode: 'urlencoded',   // Postman — a body DEFINITION
//	                        urlencoded: [ ... ] } })
//
// The second one used to be JSON.stringify'd wholesale and sent as
// application/json, so a token endpoint received
//
//	{"mode":"urlencoded","urlencoded":[{"key":"grant_type",...}]}
//
// and answered "the request body must contain the parameter 'grant_type'" —
// which is true, and says nothing about the actual mistake. The failure is
// silent in the only way that matters: the request is well-formed, it is sent,
// and it comes back 400 from a server that is behaving correctly.
//
// The conversions below are transcribed from postman-runtime 7.56.1
// `lib/requester/core-body-builder.js`, read rather than remembered, because
// each mode has a rule that is not guessable:
//
//   - urlencoded and formdata SKIP rows marked `disabled`, and a repeated key
//     is sent repeatedly rather than overwriting.
//   - raw takes its Content-Type from `options.raw.language`, defaulting to
//     text/plain — NOT application/json, even though raw bodies are usually
//     JSON.
//   - graphql serialises exactly {query, operationName, variables} and nothing
//     else, and a `variables` that is already a string is spliced in as
//     pre-encoded JSON rather than being double-encoded.
//
// In every mode Postman sets Content-Type only when the script did not, so an
// explicit header always wins. That is preserved here.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"mime/multipart"
	"net/textproto"
	"net/url"
	"strings"
)

// The five modes postman-collection defines (RequestBody.MODES). `file` is
// listed so that it can be REFUSED with a useful message rather than falling
// through to the JSON encoder — see scriptPostmanRequestBody.
var scriptPostmanBodyModes = map[string]bool{
	"raw":        true,
	"urlencoded": true,
	"formdata":   true,
	"graphql":    true,
	"file":       true,
}

// postman-runtime's CONTENT_TYPE_LANGUAGE map, verbatim. A language it does not
// know falls back to text/plain, which is also the default when the body
// carries no options at all.
var scriptPostmanRawLanguages = map[string]string{
	"html":       "text/html",
	"text":       "text/plain",
	"json":       "application/json",
	"javascript": "application/javascript",
	"xml":        "application/xml",
}

// scriptIsPostmanRequestBody reports whether a value is a Postman body
// definition rather than an ordinary payload to be JSON-encoded.
//
// The test is deliberately narrow: a `mode` string naming one of the five known
// modes AND the matching payload key present on the same object. Requiring both
// is what keeps an ordinary JSON body that happens to have a field called
// "mode" from being reinterpreted — `{mode: "raw"}` alone stays a JSON body,
// because a Postman body definition that omits its own payload is not something
// a script writes on purpose.
func scriptIsPostmanRequestBody(value interface{}) bool {
	object, ok := value.(map[string]interface{})
	if !ok {
		return false
	}
	mode, ok := object["mode"].(string)
	if !ok {
		return false
	}
	mode = strings.ToLower(strings.TrimSpace(mode))
	if !scriptPostmanBodyModes[mode] {
		return false
	}
	_, hasPayload := object[mode]
	return hasPayload
}

// scriptPostmanRequestBody serialises a Postman body definition to the bytes
// that go on the wire, plus the Content-Type implied by the mode.
//
// The returned Content-Type is advisory: the caller applies it only when the
// script set no Content-Type header of its own, matching Postman.
func scriptPostmanRequestBody(value interface{}) (string, string, error) {
	object, ok := value.(map[string]interface{})
	if !ok {
		return "", "", errors.New("sendRequest body definition must be an object")
	}
	mode := strings.ToLower(strings.TrimSpace(fmt.Sprint(object["mode"])))
	switch mode {
	case "raw":
		return scriptPostmanRawBody(object), scriptPostmanRawContentType(object), nil
	case "urlencoded":
		return scriptPostmanURLEncodedBody(object["urlencoded"]), "application/x-www-form-urlencoded", nil
	case "formdata":
		return scriptPostmanFormDataBody(object["formdata"])
	case "graphql":
		return scriptPostmanGraphQLBody(object["graphql"]), "application/json", nil
	case "file":
		// Postman sends an empty body here, because its sandbox has no access
		// to the file either — the path in `src` is resolved by the app, not by
		// the script. Silently sending nothing is the worst of the options: the
		// request succeeds in reaching the server and fails for a reason that
		// points nowhere. Say so instead.
		return "", "", errors.New("sendRequest cannot send a file body: mode 'file' reads from disk, which a script cannot do — read the file with the fs module and send it as a raw body")
	}
	return "", "", fmt.Errorf("sendRequest body mode %q is not one of raw, urlencoded, formdata, graphql, file", mode)
}

func scriptPostmanRawBody(object map[string]interface{}) string {
	raw := object["raw"]
	if text, ok := raw.(string); ok {
		return text
	}
	if raw == nil {
		return ""
	}
	// postman-runtime JSON.stringify's a non-string raw body. A value it cannot
	// stringify becomes "undefined" there; here the printed form is at least
	// visible in the timeline.
	encoded, err := json.Marshal(raw)
	if err != nil {
		return fmt.Sprint(raw)
	}
	return string(encoded)
}

// The Content-Type a raw body implies, from body.options.raw.language.
//
// Postman defaults this to text/plain, which surprises people writing JSON
// bodies — but changing it here would mean LiteAPI sending a different header
// than Postman for the identical script, which is the whole class of bug this
// file exists to close.
func scriptPostmanRawContentType(object map[string]interface{}) string {
	language := "text"
	if options, ok := object["options"].(map[string]interface{}); ok {
		if rawOptions, ok := options["raw"].(map[string]interface{}); ok {
			if named, ok := rawOptions["language"].(string); ok && strings.TrimSpace(named) != "" {
				language = strings.ToLower(strings.TrimSpace(named))
			}
		}
	}
	if contentType, ok := scriptPostmanRawLanguages[language]; ok {
		return contentType
	}
	return scriptPostmanRawLanguages["text"]
}

// scriptPostmanURLEncodedBody encodes the urlencoded rows.
//
// `urlencoded` may also be given as an already-encoded string — postman-collection
// accepts that and parses it with QueryParam.parse. Passing it through unchanged
// is the same result without the round trip, and matches what
// scriptFormURLEncodedBody does for pm.request.body.
func scriptPostmanURLEncodedBody(value interface{}) string {
	if text, ok := value.(string); ok {
		return text
	}
	rows, ok := value.([]interface{})
	if !ok {
		return ""
	}
	form := url.Values{}
	for _, row := range rows {
		key, rowValue, enabled := scriptPostmanBodyRow(row)
		if !enabled || key == "" {
			continue
		}
		// Add, not Set: Postman accumulates duplicate keys into a repeated
		// parameter, which is how a script sends e.g. two `scope` values.
		form.Add(key, rowValue)
	}
	return form.Encode()
}

// scriptPostmanFormDataBody writes a multipart body and returns it with the
// Content-Type carrying the generated boundary.
//
// A row with type "file" cannot be honoured for the same reason mode 'file'
// cannot — the value is a path the sandbox may not read. Such a row is sent as
// an empty field of that name, which is what Postman does when a file row has
// no value, rather than dropping the field and changing the shape of the form.
func scriptPostmanFormDataBody(value interface{}) (string, string, error) {
	rows, ok := value.([]interface{})
	if !ok {
		rows = nil
	}
	var buffer bytes.Buffer
	writer := multipart.NewWriter(&buffer)
	for _, row := range rows {
		key, rowValue, enabled := scriptPostmanBodyRow(row)
		if !enabled || key == "" {
			continue
		}
		fileName, contentType := scriptPostmanFormDataRowOptions(row)
		if fileName == "" && contentType == "" {
			if err := writer.WriteField(key, rowValue); err != nil {
				return "", "", err
			}
			continue
		}
		headers := textproto.MIMEHeader{}
		disposition := fmt.Sprintf(`form-data; name=%q`, key)
		if fileName != "" {
			disposition += fmt.Sprintf(`; filename=%q`, fileName)
		}
		headers.Set("Content-Disposition", disposition)
		if contentType != "" {
			headers.Set("Content-Type", contentType)
		}
		part, err := writer.CreatePart(headers)
		if err != nil {
			return "", "", err
		}
		if _, err := part.Write([]byte(rowValue)); err != nil {
			return "", "", err
		}
	}
	if err := writer.Close(); err != nil {
		return "", "", err
	}
	return buffer.String(), writer.FormDataContentType(), nil
}

func scriptPostmanFormDataRowOptions(row interface{}) (string, string) {
	object, ok := row.(map[string]interface{})
	if !ok {
		return "", ""
	}
	fileName, _ := object["fileName"].(string)
	contentType, _ := object["contentType"].(string)
	return strings.TrimSpace(fileName), strings.TrimSpace(contentType)
}

// scriptPostmanGraphQLBody builds the {query, operationName, variables} payload.
//
// `variables` given as a STRING is already-encoded JSON and is spliced in
// literally; encoding it again would send the query a quoted string where the
// server expects an object. postman-runtime makes the same distinction, and it
// is the one detail of graphql bodies that is invisible until a server rejects
// them.
func scriptPostmanGraphQLBody(value interface{}) string {
	object, ok := value.(map[string]interface{})
	if !ok {
		return "{}"
	}
	if variables, ok := object["variables"].(string); ok {
		fields := []string{}
		if query, ok := object["query"].(string); ok {
			fields = append(fields, `"query":`+scriptJSONString(query))
		}
		if operation, ok := object["operationName"].(string); ok {
			fields = append(fields, `"operationName":`+scriptJSONString(operation))
		}
		// An empty string is a text editor's idea of "nothing typed", not a
		// JSON document, so Postman omits it rather than producing invalid JSON.
		if strings.TrimSpace(variables) != "" {
			fields = append(fields, `"variables":`+variables)
		}
		return "{" + strings.Join(fields, ",") + "}"
	}
	payload := map[string]interface{}{}
	for _, name := range []string{"query", "operationName", "variables"} {
		if held, ok := object[name]; ok && held != nil {
			payload[name] = held
		}
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "{}"
	}
	return string(encoded)
}

func scriptJSONString(value string) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return `""`
	}
	return string(encoded)
}

// scriptPostmanBodyRow reads one {key, value, disabled} row.
//
// `name`/`enabled` are accepted alongside `key`/`disabled` because that is the
// shape LiteAPI's own KeyValue uses, and a script that copies rows out of
// pm.request.body would otherwise silently produce a form of empty fields.
// A row that says nothing about being disabled is enabled — absent means on in
// both vocabularies.
func scriptPostmanBodyRow(row interface{}) (string, string, bool) {
	object, ok := row.(map[string]interface{})
	if !ok {
		return "", "", false
	}
	key := scriptPostmanRowString(object, "key")
	if key == "" {
		key = scriptPostmanRowString(object, "name")
	}
	value := scriptPostmanRowString(object, "value")
	if disabled, ok := object["disabled"].(bool); ok && disabled {
		return key, value, false
	}
	if enabled, ok := object["enabled"].(bool); ok && !enabled {
		return key, value, false
	}
	return key, value, true
}

func scriptPostmanRowString(object map[string]interface{}, name string) string {
	held, ok := object[name]
	if !ok || held == nil {
		return ""
	}
	if text, ok := held.(string); ok {
		return text
	}
	return fmt.Sprint(held)
}

// scriptContentTypeHasBoundary reports whether a multipart Content-Type already
// names a boundary.
//
// A script that sets `Content-Type: multipart/form-data` by hand — a common
// habit, because that is what the browser devtools show — describes a body it
// cannot produce: the boundary is chosen by whatever writes the parts. Honouring
// that header would send a multipart body no server can split. So for formdata
// only, a boundary-less multipart header is replaced rather than kept.
func scriptContentTypeHasBoundary(value string) bool {
	_, params, err := mime.ParseMediaType(value)
	if err != nil {
		return false
	}
	return strings.TrimSpace(params["boundary"]) != ""
}
