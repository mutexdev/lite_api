// pm.sendRequest with a Postman request-body definition.
//
// The bug these pin: `{mode: 'urlencoded', urlencoded: [...]}` was JSON-encoded
// whole and sent as application/json, so an OAuth2 token endpoint received a
// document describing a form instead of the form, and reported the first field
// it could not find. Everything about that failure points at the script.
//
// The expected values are transcribed from postman-runtime 7.56.1
// `lib/requester/core-body-builder.js` and postman-collection 5.3.1
// `lib/collection/request-body.js`. Where a rule looks arbitrary it is Postman's
// rule, and the comment says so — matching Postman is the requirement, not
// producing the body one would design.
package scripting

import (
	"mime"
	"mime/multipart"
	"net/url"
	"strings"
	"testing"

	"github.com/dop251/goja"
)

func exported(t *testing.T, script string) interface{} {
	t.Helper()
	runtime := goja.New()
	value, err := runtime.RunString("(" + script + ")")
	if err != nil {
		t.Fatalf("script %s failed: %v", script, err)
	}
	return value.Export()
}

// THE REPORTED FAILURE. The body a client-credentials token exchange sends.
func TestPostmanUrlencodedBodyIsFormEncoded(t *testing.T) {
	body := exported(t, `{
		mode: 'urlencoded',
		urlencoded: [
			{key: 'grant_type', value: 'client_credentials'},
			{key: 'client_id', value: 'the-client'},
			{key: 'client_secret', value: 's3cret'},
			{key: 'scope', value: 'read write'}
		]
	}`)

	if !scriptIsPostmanRequestBody(body) {
		t.Fatal("a urlencoded body definition was not recognised as one")
	}
	text, contentType, err := scriptPostmanRequestBody(body)
	if err != nil {
		t.Fatalf("serialise: %v", err)
	}
	if contentType != "application/x-www-form-urlencoded" {
		t.Errorf("content type %q", contentType)
	}
	values, err := url.ParseQuery(text)
	if err != nil {
		t.Fatalf("the body did not parse as a form: %v (%q)", err, text)
	}
	// grant_type is the field the server named. The others are here because a
	// body that carries only grant_type fails the same way one field later.
	for name, want := range map[string]string{
		"grant_type":    "client_credentials",
		"client_id":     "the-client",
		"client_secret": "s3cret",
		"scope":         "read write",
	} {
		if got := values.Get(name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
}

// Postman SKIPS disabled rows. A row left in the collection with the checkbox
// cleared is one the user chose not to send.
func TestPostmanUrlencodedBodySkipsDisabledRows(t *testing.T) {
	body := exported(t, `{
		mode: 'urlencoded',
		urlencoded: [
			{key: 'grant_type', value: 'client_credentials'},
			{key: 'audience', value: 'staging', disabled: true},
			{key: 'enabled_form', value: 'yes', enabled: false}
		]
	}`)

	text, _, err := scriptPostmanRequestBody(body)
	if err != nil {
		t.Fatalf("serialise: %v", err)
	}
	if strings.Contains(text, "audience") {
		t.Errorf("a disabled row was sent: %q", text)
	}
	if strings.Contains(text, "enabled_form") {
		t.Errorf("an enabled:false row was sent: %q", text)
	}
	if !strings.Contains(text, "grant_type=client_credentials") {
		t.Errorf("the enabled row was lost: %q", text)
	}
}

// A repeated key is REPEATED, not overwritten — how a script sends two scopes.
func TestPostmanUrlencodedBodyKeepsDuplicateKeys(t *testing.T) {
	body := exported(t, `{
		mode: 'urlencoded',
		urlencoded: [{key: 'scope', value: 'read'}, {key: 'scope', value: 'write'}]
	}`)

	text, _, err := scriptPostmanRequestBody(body)
	if err != nil {
		t.Fatalf("serialise: %v", err)
	}
	values, _ := url.ParseQuery(text)
	if got := values["scope"]; len(got) != 2 || got[0] != "read" || got[1] != "write" {
		t.Errorf("scope = %v, want both values in order", got)
	}
}

// postman-collection accepts an already-encoded string here and parses it.
// Re-encoding it would turn the separators into literal %3D and %26.
func TestPostmanUrlencodedBodyAcceptsAnEncodedString(t *testing.T) {
	body := exported(t, `{mode: 'urlencoded', urlencoded: 'grant_type=client_credentials&scope=read'}`)
	text, _, err := scriptPostmanRequestBody(body)
	if err != nil {
		t.Fatalf("serialise: %v", err)
	}
	if text != "grant_type=client_credentials&scope=read" {
		t.Errorf("got %q, want the string untouched", text)
	}
}

// A raw body is the string, and its Content-Type comes from the raw LANGUAGE —
// which Postman defaults to text/plain even though raw bodies are usually JSON.
// Sending application/json here would mean LiteAPI and Postman disagreeing on
// the wire for the identical script.
func TestPostmanRawBodyDefaultsToTextPlain(t *testing.T) {
	body := exported(t, `{mode: 'raw', raw: '{"a":1}'}`)
	text, contentType, err := scriptPostmanRequestBody(body)
	if err != nil {
		t.Fatalf("serialise: %v", err)
	}
	if text != `{"a":1}` {
		t.Errorf("body %q", text)
	}
	if contentType != "text/plain" {
		t.Errorf("content type %q, want Postman's default", contentType)
	}
}

func TestPostmanRawBodyTakesContentTypeFromLanguage(t *testing.T) {
	body := exported(t, `{mode: 'raw', raw: '{"a":1}', options: {raw: {language: 'json'}}}`)
	_, contentType, err := scriptPostmanRequestBody(body)
	if err != nil {
		t.Fatalf("serialise: %v", err)
	}
	if contentType != "application/json" {
		t.Errorf("content type %q", contentType)
	}
}

func TestPostmanGraphQLBodyIsAQueryDocument(t *testing.T) {
	body := exported(t, `{
		mode: 'graphql',
		graphql: {query: 'query Q($id: ID!) { node(id: $id) { id } }', operationName: 'Q', variables: {id: '7'}}
	}`)
	text, contentType, err := scriptPostmanRequestBody(body)
	if err != nil {
		t.Fatalf("serialise: %v", err)
	}
	if contentType != "application/json" {
		t.Errorf("content type %q", contentType)
	}
	for _, want := range []string{`"query"`, `"operationName":"Q"`, `"variables"`, `"id":"7"`} {
		if !strings.Contains(text, want) {
			t.Errorf("body %q is missing %s", text, want)
		}
	}
}

// `variables` given as a STRING is already-encoded JSON. Encoding it again
// sends the server a quoted string where it expects an object — a 400 whose
// message is about the query, not about the encoding.
func TestPostmanGraphQLStringVariablesAreNotDoubleEncoded(t *testing.T) {
	body := exported(t, `{mode: 'graphql', graphql: {query: '{ me { id } }', variables: '{"id":"7"}'}}`)
	text, _, err := scriptPostmanRequestBody(body)
	if err != nil {
		t.Fatalf("serialise: %v", err)
	}
	if !strings.Contains(text, `"variables":{"id":"7"}`) {
		t.Errorf("body %q double-encoded the variables", text)
	}
}

// An empty variables string is a text editor's idea of "nothing typed" rather
// than a JSON document; splicing it in would produce invalid JSON.
func TestPostmanGraphQLEmptyStringVariablesAreOmitted(t *testing.T) {
	body := exported(t, `{mode: 'graphql', graphql: {query: '{ me { id } }', variables: ''}}`)
	text, _, err := scriptPostmanRequestBody(body)
	if err != nil {
		t.Fatalf("serialise: %v", err)
	}
	if strings.Contains(text, "variables") {
		t.Errorf("body %q kept an empty variables field", text)
	}
}

func TestPostmanFormDataBodyIsMultipart(t *testing.T) {
	body := exported(t, `{
		mode: 'formdata',
		formdata: [
			{key: 'name', value: 'ada'},
			{key: 'skip', value: 'no', disabled: true},
			{key: 'note', value: '<x/>', contentType: 'application/xml'}
		]
	}`)
	text, contentType, err := scriptPostmanRequestBody(body)
	if err != nil {
		t.Fatalf("serialise: %v", err)
	}
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil || mediaType != "multipart/form-data" {
		t.Fatalf("content type %q (%v)", contentType, err)
	}
	// Parsed rather than string-matched, so the test fails if the boundary and
	// the body ever disagree — the way a hand-built multipart body breaks.
	reader := multipart.NewReader(strings.NewReader(text), params["boundary"])
	form, err := reader.ReadForm(1 << 20)
	if err != nil {
		t.Fatalf("the multipart body did not parse: %v", err)
	}
	if got := form.Value["name"]; len(got) != 1 || got[0] != "ada" {
		t.Errorf("name = %v", got)
	}
	if _, present := form.Value["skip"]; present {
		t.Error("a disabled row was sent")
	}
	if got := form.Value["note"]; len(got) != 1 || got[0] != "<x/>" {
		t.Errorf("note = %v", got)
	}
}

// mode 'file' reads a path from disk, which the sandbox cannot do. Postman
// sends an empty body; an empty body reaches the server and fails for a reason
// that names nothing. The error names the limitation and the way around it.
func TestPostmanFileBodyIsRefusedWithAUsefulMessage(t *testing.T) {
	body := exported(t, `{mode: 'file', file: {src: '/tmp/payload.bin'}}`)
	_, _, err := scriptPostmanRequestBody(body)
	if err == nil {
		t.Fatal("a file body was accepted")
	}
	if !strings.Contains(err.Error(), "file") || !strings.Contains(err.Error(), "raw body") {
		t.Errorf("the error does not say what to do instead: %v", err)
	}
}

// THE GUARD ON THE WHOLE FEATURE. bru.sendRequest is axios-shaped and
// pm.sendRequest is Postman-shaped, and they are the same function. An ordinary
// payload must not be reinterpreted just because it has a field called "mode".
func TestAnOrdinaryPayloadIsNotMistakenForABodyDefinition(t *testing.T) {
	for _, script := range []string{
		`{mode: 'raw', message: 'transfer'}`, // a mode with no matching payload
		`{mode: 'urlencoded'}`,               // ditto
		`{mode: 'dark', raw: 'x'}`,           // a payload key with an unknown mode
		`{a: 1, b: 2}`,                       // no mode at all
		`{mode: 7, raw: 'x'}`,                // mode is not a string
	} {
		if scriptIsPostmanRequestBody(exported(t, script)) {
			t.Errorf("%s was read as a Postman body definition", script)
		}
	}
}

func TestABodyDefinitionIsRecognisedInEveryMode(t *testing.T) {
	for _, script := range []string{
		`{mode: 'raw', raw: 'x'}`,
		`{mode: 'urlencoded', urlencoded: []}`,
		`{mode: 'formdata', formdata: []}`,
		`{mode: 'graphql', graphql: {}}`,
		`{mode: 'file', file: {}}`,
	} {
		if !scriptIsPostmanRequestBody(exported(t, script)) {
			t.Errorf("%s was not recognised", script)
		}
	}
}

// --- the config reader ---------------------------------------------------

func configFrom(t *testing.T, dialect scriptSendDialect, script string, vars map[string]string) scriptSendRequestConfig {
	t.Helper()
	runtime := goja.New()
	value, err := runtime.RunString("(" + script + ")")
	if err != nil {
		t.Fatalf("script failed: %v", err)
	}
	config, err := scriptSendRequestConfigFromValue(runtime, dialect, value, vars)
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	return config
}

// The reported script, whole. `header` is singular and the body is a definition
// — both of which used to be dropped, and either one alone breaks the exchange.
func TestSendRequestReadsTheReportedTokenScript(t *testing.T) {
	config := configFrom(t, dialectPostman, `{
		url: 'https://identity.example.test/token',
		method: 'POST',
		header: {'Content-Type': 'application/x-www-form-urlencoded'},
		body: {
			mode: 'urlencoded',
			urlencoded: [
				{key: 'grant_type', value: 'client_credentials'},
				{key: 'client_id', value: 'the-client'}
			]
		}
	}`, nil)

	if config.Headers["Content-Type"] != "application/x-www-form-urlencoded" {
		t.Errorf("the singular `header` key was dropped: %v", config.Headers)
	}
	if !config.BodyEncoded {
		t.Fatal("the body definition was not serialised")
	}
	if !strings.Contains(config.BodyText, "grant_type=client_credentials") {
		t.Errorf("body %q", config.BodyText)
	}
}

// The array form is what pm.request.headers gives a script back. Read as an
// object it produced headers named "0" and "1" whose value was "[object Object]".
func TestSendRequestReadsHeadersGivenAsRows(t *testing.T) {
	config := configFrom(t, dialectPostman, `{
		url: 'https://example.test',
		header: [
			{key: 'Accept', value: 'application/json'},
			{key: 'X-Skipped', value: 'no', disabled: true}
		]
	}`, nil)

	if config.Headers["Accept"] != "application/json" {
		t.Errorf("headers = %v", config.Headers)
	}
	if _, present := config.Headers["X-Skipped"]; present {
		t.Error("a disabled header row was sent")
	}
	for name := range config.Headers {
		if name == "0" || name == "1" {
			t.Errorf("the array was read as an object: %v", config.Headers)
		}
	}
}

// Interpolation has to happen on the ROW, before encoding. Run afterwards it
// would be looking for {{...}} in text where the braces are already %7B%7B.
func TestSendRequestInterpolatesInsideABodyDefinition(t *testing.T) {
	config := configFrom(t, dialectPostman, `{
		url: '{{base}}/token',
		body: {mode: 'urlencoded', urlencoded: [{key: 'client_id', value: '{{clientId}}'}]}
	}`, map[string]string{"base": "https://identity.example.test", "clientId": "the-client"})

	if config.URL != "https://identity.example.test/token" {
		t.Errorf("url %q", config.URL)
	}
	if !strings.Contains(config.BodyText, "client_id=the-client") {
		t.Errorf("body %q", config.BodyText)
	}
}

// THE DIALECT BOUNDARY. The identical config, read as each API.
//
// Bruno has no body modes, so `{mode: …}` under bru.sendRequest is an object
// like any other and JSON is the right answer. Getting this backwards would
// change what every existing Bruno script sends — the failure mode this whole
// change is trying not to become.
func TestOnlyThePostmanDialectReadsABodyDefinition(t *testing.T) {
	const script = `{
		url: 'https://example.test',
		body: {mode: 'urlencoded', urlencoded: [{key: 'grant_type', value: 'client_credentials'}]}
	}`

	postman := configFrom(t, dialectPostman, script, nil)
	if !postman.BodyEncoded || postman.BodyText != "grant_type=client_credentials" {
		t.Errorf("pm.sendRequest: encoded=%v body=%q", postman.BodyEncoded, postman.BodyText)
	}

	bruno := configFrom(t, dialectBruno, script, nil)
	if bruno.BodyEncoded {
		t.Error("bru.sendRequest serialised a Postman body definition")
	}
	text, contentType := scriptSendRequestBody(bruno.Body)
	if contentType != "application/json" || !strings.Contains(text, `"mode":"urlencoded"`) {
		t.Errorf("bru.sendRequest: %q as %q, want the object as JSON", text, contentType)
	}
}

// `data` is axios's key and has no Postman meaning, so it stays a payload in
// both dialects however it is shaped.
func TestDataIsAPayloadInBothDialects(t *testing.T) {
	for name, dialect := range map[string]scriptSendDialect{"bru": dialectBruno, "pm": dialectPostman} {
		config := configFrom(t, dialect, `{
			url: 'https://example.test',
			data: {mode: 'urlencoded', urlencoded: [{key: 'a', value: '1'}]}
		}`, nil)
		if config.BodyEncoded {
			t.Errorf("%s.sendRequest re-read a `data` payload as a body definition", name)
		}
	}
}

// A pm.sendRequest payload with no `mode` is still JSON. Scripts already written
// against LiteAPI depend on this, and the dialect must not change it.
func TestAPostmanPayloadWithoutAModeIsStillJSON(t *testing.T) {
	config := configFrom(t, dialectPostman, `{url: 'https://example.test', body: {user: 'ada'}}`, nil)
	if config.BodyEncoded {
		t.Error("an ordinary object was read as a body definition")
	}
	text, contentType := scriptSendRequestBody(config.Body)
	if contentType != "application/json" || !strings.Contains(text, `"user":"ada"`) {
		t.Errorf("%q as %q", text, contentType)
	}
}

// pm.sendRequest({url: pm.request.url}) forwards a parsed URL object. Stringified
// naively it becomes the literal text "[object Object]".
func TestSendRequestAcceptsAParsedURLObject(t *testing.T) {
	config := configFrom(t, dialectPostman, `{url: {raw: 'https://example.test/a?b=1', host: ['example','test'], path: ['a']}}`, nil)
	if config.URL != "https://example.test/a?b=1" {
		t.Errorf("url %q", config.URL)
	}
}

// --- the Content-Type rule ------------------------------------------------

// Postman fills in a Content-Type only when the script set none.
func TestAnExplicitContentTypeSurvives(t *testing.T) {
	headers := map[string]string{"content-type": "application/json"}
	if scriptSendRequestWantsContentType(headers, "application/x-www-form-urlencoded") {
		t.Error("the mode's content type overrode the script's")
	}
}

// The exception: a hand-written multipart header names no boundary, so honouring
// it sends parts no server can find.
func TestABoundarylessMultipartHeaderIsReplaced(t *testing.T) {
	headers := map[string]string{"Content-Type": "multipart/form-data"}
	generated := "multipart/form-data; boundary=abc123"
	if !scriptSendRequestWantsContentType(headers, generated) {
		t.Fatal("a boundary-less multipart header was kept")
	}
	scriptSendRequestSetContentType(headers, generated)
	if headers["Content-Type"] != generated {
		t.Errorf("headers = %v", headers)
	}
	// Rewritten in place, under the casing the script used — two Content-Type
	// headers differing only in case is a request with an ambiguous body.
	if len(headers) != 1 {
		t.Errorf("a second content-type header was added: %v", headers)
	}
}

func TestAMultipartHeaderWithABoundaryIsLeftAlone(t *testing.T) {
	headers := map[string]string{"Content-Type": "multipart/form-data; boundary=chosen"}
	if scriptSendRequestWantsContentType(headers, "multipart/form-data; boundary=generated") {
		t.Error("a deliberate boundary was overwritten")
	}
}
