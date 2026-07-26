// The response-example snapshot: what the request looked like when it ran.
//
// Saving a response example records the request alongside it, and that record
// is what "copy as curl" regenerates from later. Two things have to hold, and
// both fail silently.
//
// It must be INTERPOLATED. A snapshot holding "{{baseUrl}}/users" produces a
// curl command with a literal {{baseUrl}} in it, which the shell then treats as
// a brace expansion — so the user gets a command that neither runs nor obviously
// explains why.
//
// It must not MUTATE the request it snapshots. These functions copy each row
// before interpolating; if they wrote in place, saving an example would bake
// today's variable values into the user's request permanently and lose the
// variables they wrote.
package codegen

import (
	"testing"

	"LiteAPI/internal/types"
)

var vars = map[string]string{"host": "api.test", "token": "secret", "file": "/tmp/x.bin"}

func TestSnapshotInterpolatesEveryKeyValueField(t *testing.T) {
	rows := []types.KeyValue{{Name: "{{host}}-name", Value: "{{token}}", Description: "for {{host}}", Enabled: true}}
	got := interpolatedKeyValues(rows, vars)
	if got[0].Name != "api.test-name" || got[0].Value != "secret" || got[0].Description != "for api.test" {
		t.Errorf("got %+v", got[0])
	}
	if !got[0].Enabled {
		t.Error("Enabled was dropped; the row would be treated as disabled when the example is replayed")
	}
}

// Saving an example must not rewrite the request. If it did, the user's
// {{baseUrl}} would be replaced by whatever the value was at that moment and
// could not be recovered.
func TestSnapshotDoesNotMutateTheRequestItSnapshots(t *testing.T) {
	rows := []types.KeyValue{{Name: "{{host}}", Value: "{{token}}", Enabled: true}}
	interpolatedKeyValues(rows, vars)
	if rows[0].Name != "{{host}}" || rows[0].Value != "{{token}}" {
		t.Fatalf("the caller's rows were rewritten to %+v; the request has lost its variables", rows[0])
	}

	parts := []types.FormPart{{Name: "{{host}}", Value: "{{token}}", FilePath: "{{file}}", ContentType: "{{host}}"}}
	interpolatedFormParts(parts, vars)
	if parts[0].Name != "{{host}}" || parts[0].FilePath != "{{file}}" {
		t.Fatalf("multipart parts were rewritten to %+v", parts[0])
	}

	files := []types.FileBodyEntry{{FilePath: "{{file}}", ContentType: "{{host}}"}}
	interpolatedFileBodyEntries(files, vars)
	if files[0].FilePath != "{{file}}" {
		t.Fatalf("file entries were rewritten to %+v", files[0])
	}
}

func TestSnapshotInterpolatesEveryMultipartField(t *testing.T) {
	got := interpolatedFormParts([]types.FormPart{{
		Name: "{{host}}", Value: "{{token}}", FilePath: "{{file}}", ContentType: "{{host}}", Enabled: true,
	}}, vars)
	if got[0].Name != "api.test" || got[0].Value != "secret" || got[0].FilePath != "/tmp/x.bin" || got[0].ContentType != "api.test" {
		t.Errorf("got %+v", got[0])
	}
	if !got[0].Enabled {
		t.Error("Enabled was dropped")
	}
}

func TestSnapshotInterpolatesFileEntries(t *testing.T) {
	got := interpolatedFileBodyEntries([]types.FileBodyEntry{{FilePath: "{{file}}", ContentType: "{{host}}", Selected: true}}, vars)
	if got[0].FilePath != "/tmp/x.bin" || got[0].ContentType != "api.test" {
		t.Errorf("got %+v", got[0])
	}
	if !got[0].Selected {
		t.Error("Selected was dropped; the example would not know which file was sent")
	}
}

// nil rather than an empty slice, so the saved JSON omits the field instead of
// writing "headers": [].
func TestSnapshotOfNoRowsIsNil(t *testing.T) {
	if interpolatedKeyValues(nil, vars) != nil || interpolatedFormParts(nil, vars) != nil || interpolatedFileBodyEntries(nil, vars) != nil {
		t.Error("empty input should produce nil")
	}
}

func TestBodySnapshotPerMode(t *testing.T) {
	for name, tc := range map[string]struct {
		body types.RequestBody
		want string
	}{
		"json": {types.RequestBody{Mode: "json", JSON: `{"h":"{{host}}"}`}, `{"h":"api.test"}`},
		"xml":  {types.RequestBody{Mode: "xml", XML: `<h>{{host}}</h>`}, `<h>api.test</h>`},
		"text": {types.RequestBody{Mode: "text", Text: "{{host}}"}, "api.test"},
		// An unknown mode falls back to the text body rather than to empty: a
		// snapshot with no body would regenerate a curl that sends nothing.
		"unknown mode": {types.RequestBody{Mode: "sparql", Text: "{{host}}"}, "api.test"},
		"file legacy":  {types.RequestBody{Mode: "file", FilePath: "{{file}}"}, "/tmp/x.bin"},
	} {
		if got := requestCodeBodySnapshot(types.RequestItem{Body: tc.body}, vars); got != tc.want {
			t.Errorf("%s: got %q, want %q", name, got, tc.want)
		}
	}
}

// Both spellings reach the same branch. The hyphenated form is what imported
// collections use, and a snapshot that missed it would fall through to the text
// body — which for a form request is empty.
func TestBodySnapshotAcceptsBothFormModeSpellings(t *testing.T) {
	rows := []types.KeyValue{{Name: "u", Value: "{{token}}", Enabled: true}}
	for _, mode := range []string{"formUrlEncoded", "form-url-encoded"} {
		got := requestCodeBodySnapshot(types.RequestItem{Body: types.RequestBody{Mode: mode, FormURLEncoded: rows}}, vars)
		if got != "u=secret" {
			t.Errorf("%s: got %q", mode, got)
		}
	}
	parts := []types.FormPart{{Name: "u", Value: "{{token}}", Enabled: true}}
	for _, mode := range []string{"multipartForm", "multipart-form", "multipart"} {
		got := requestCodeBodySnapshot(types.RequestItem{Body: types.RequestBody{Mode: mode, Multipart: parts}}, vars)
		if got != "u=secret" {
			t.Errorf("%s: got %q", mode, got)
		}
	}
}

// A disabled row was not sent, so recording it would make the example claim a
// field the server never saw.
func TestBodySnapshotOmitsDisabledRows(t *testing.T) {
	got := requestCodeBodySnapshot(types.RequestItem{Body: types.RequestBody{
		Mode: "formUrlEncoded",
		FormURLEncoded: []types.KeyValue{
			{Name: "on", Value: "1", Enabled: true},
			{Name: "off", Value: "2"},
		},
	}}, vars)
	if got != "on=1" {
		t.Errorf("got %q, want only the enabled row", got)
	}

	multipart := requestCodeBodySnapshot(types.RequestItem{Body: types.RequestBody{
		Mode:      "multipartForm",
		Multipart: []types.FormPart{{Name: "on", Value: "1", Enabled: true}, {Name: "off", Value: "2"}},
	}}, vars)
	if multipart != "on=1" {
		t.Errorf("multipart got %q", multipart)
	}
}

// A file part carries no value, so the snapshot shows the path instead —
// otherwise the record reads "avatar=" and loses which file was attached.
func TestBodySnapshotOfAFilePartShowsItsPath(t *testing.T) {
	got := requestCodeBodySnapshot(types.RequestItem{Body: types.RequestBody{
		Mode:      "multipartForm",
		Multipart: []types.FormPart{{Name: "avatar", FilePath: "{{file}}", Enabled: true}},
	}}, vars)
	if got != "avatar=/tmp/x.bin" {
		t.Errorf("got %q", got)
	}
}

func TestBodySnapshotOfMultipleMultipartRowsIsOnePerLine(t *testing.T) {
	got := requestCodeBodySnapshot(types.RequestItem{Body: types.RequestBody{
		Mode:      "multipartForm",
		Multipart: []types.FormPart{{Name: "a", Value: "1", Enabled: true}, {Name: "b", Value: "2", Enabled: true}},
	}}, vars)
	if got != "a=1\nb=2" {
		t.Errorf("got %q", got)
	}
}

// With several attachments, the snapshot records the SELECTED one. Recording
// the first would name a file the request did not send.
func TestBodySnapshotOfAFileBodyUsesTheSelectedEntry(t *testing.T) {
	got := requestCodeBodySnapshot(types.RequestItem{Body: types.RequestBody{
		Mode: "file",
		Files: []types.FileBodyEntry{
			{FilePath: "/tmp/not-this-one"},
			{FilePath: "{{file}}", Selected: true},
		},
	}}, vars)
	if got != "/tmp/x.bin" {
		t.Errorf("got %q, want the selected entry", got)
	}
}

// GraphQL is sent as a JSON envelope, so the snapshot has to be that envelope
// and not the bare query — a curl regenerated from the query alone posts
// something the server rejects as malformed.
func TestGraphQLSnapshotIsTheJSONEnvelope(t *testing.T) {
	got := requestCodeBodySnapshot(types.RequestItem{Body: types.RequestBody{
		Mode:             "graphql",
		GraphQLQuery:     "query { host }",
		GraphQLVariables: `{"h":"{{host}}"}`,
	}}, vars)
	if got != `{"query":"query { host }","variables":{"h":"api.test"}}` {
		t.Errorf("got %q", got)
	}
}

func TestGraphQLSnapshotOmitsEmptyVariables(t *testing.T) {
	got := requestCodeBodySnapshot(types.RequestItem{Body: types.RequestBody{Mode: "graphql", GraphQLQuery: "{ a }", GraphQLVariables: "  "}}, vars)
	if got != `{"query":"{ a }"}` {
		t.Errorf("got %q, want no variables key at all", got)
	}
}

// Variables that are not valid JSON are kept as a string rather than dropped.
// Dropping them would silently send a query with no variables, which returns a
// plausible-looking error about a missing argument.
func TestGraphQLSnapshotKeepsUnparseableVariablesAsText(t *testing.T) {
	got := requestCodeBodySnapshot(types.RequestItem{Body: types.RequestBody{Mode: "graphql", GraphQLQuery: "{ a }", GraphQLVariables: "not json"}}, vars)
	if got != `{"query":"{ a }","variables":"not json"}` {
		t.Errorf("got %q", got)
	}
}
