package yamlstore

import (
	"strings"
	"testing"

	"github.com/mutexdev/lite_api/internal/types"
)

// yamlMultipart and yamlPostResponseActions write parts of the on-disk .yml
// collection format, and both were at 0%. What they emit is the user's saved
// request: a field dropped here is a field the user loses on the next reload,
// with no error anywhere to say so.
//
// The property worth testing is not the shape of the map but the ROUND TRIP —
// serialise, parse back, and require what comes out to match what went in.
// Asserting the intermediate map would pass just as happily if the reader and
// the writer disagreed about a key.

func yamlRoundTrip(t *testing.T, item types.RequestItem) types.RequestItem {
	t.Helper()
	content, err := StringifyRequest(item)
	if err != nil {
		t.Fatalf("stringify: %v", err)
	}
	parsed, err := ParseRequest(content)
	if err != nil {
		t.Fatalf("parse: %v\n---\n%s", err, content)
	}
	return parsed
}

func multipartRequest(parts ...types.FormPart) types.RequestItem {
	return types.RequestItem{
		Name: "Upload",
		Type: "http",
		// "multipartForm" is the in-memory mode; "multipart-form" is only its
		// on-disk spelling, and yamlBody switches on the former. Using the
		// on-disk name here silently produced a body with no parts at all.
		Body: types.RequestBody{Mode: "multipartForm", Multipart: parts},
	}
}

// A text part and a file part must survive as different KINDS. Collapsing them
// would turn an attached file into a literal string of its path on the next
// load, and the request would silently send the wrong thing.
func TestMultipartTextAndFilePartsSurviveTheRoundTrip(t *testing.T) {
	item := multipartRequest(
		types.FormPart{Name: "caption", Value: "a photo", Enabled: true},
		types.FormPart{Name: "photo", FilePath: "/tmp/photo.png", Enabled: true},
	)
	parts := yamlRoundTrip(t, item).Body.Multipart
	if len(parts) != 2 {
		t.Fatalf("got %d parts, want 2: %+v", len(parts), parts)
	}

	text := parts[0]
	if text.Name != "caption" || text.Value != "a photo" {
		t.Errorf("text part = %+v", text)
	}
	if text.FilePath != "" {
		t.Errorf("the text part came back with a file path %q", text.FilePath)
	}

	file := parts[1]
	if file.Name != "photo" || file.FilePath != "/tmp/photo.png" {
		t.Errorf("file part = %+v", file)
	}
	if file.Value != "" {
		t.Errorf("the file part came back with a text value %q", file.Value)
	}
}

// A DISABLED part is written and marked, not dropped: it is a row the user
// deliberately kept but switched off, and losing it on save is losing their
// work rather than merely their setting.
func TestMultipartDisabledPartsSurviveTheRoundTrip(t *testing.T) {
	item := multipartRequest(
		types.FormPart{Name: "on", Value: "1", Enabled: true},
		types.FormPart{Name: "off", Value: "2", Enabled: false},
	)
	parts := yamlRoundTrip(t, item).Body.Multipart
	if len(parts) != 2 {
		t.Fatalf("got %d parts, want both: %+v", len(parts), parts)
	}
	if !parts[0].Enabled {
		t.Error("the enabled part came back disabled")
	}
	if parts[1].Enabled {
		t.Error("the disabled part came back enabled, so a switched-off row would start sending")
	}
	if parts[1].Name != "off" || parts[1].Value != "2" {
		t.Errorf("the disabled part lost its content: %+v", parts[1])
	}
}

// contentType is only written when set, but must survive when it is — it is
// how a user overrides the type of an uploaded file, and dropping it changes
// what the server receives.
func TestMultipartContentTypeSurvivesWhenSet(t *testing.T) {
	item := multipartRequest(
		types.FormPart{Name: "doc", FilePath: "/tmp/a.bin", ContentType: "application/pdf", Enabled: true},
		types.FormPart{Name: "plain", Value: "x", Enabled: true},
	)
	parts := yamlRoundTrip(t, item).Body.Multipart
	if len(parts) != 2 {
		t.Fatalf("got %d parts: %+v", len(parts), parts)
	}
	if parts[0].ContentType != "application/pdf" {
		t.Errorf("content type = %q, want application/pdf", parts[0].ContentType)
	}
	if parts[1].ContentType != "" {
		t.Errorf("a part with no content type gained one: %q", parts[1].ContentType)
	}
}

// Order is user-visible: the parts table is displayed and sent in this order,
// and a multipart body is not a set.
func TestMultipartKeepsItsOrder(t *testing.T) {
	item := multipartRequest(
		types.FormPart{Name: "first", Value: "1", Enabled: true},
		types.FormPart{Name: "second", Value: "2", Enabled: true},
		types.FormPart{Name: "third", Value: "3", Enabled: true},
	)
	parts := yamlRoundTrip(t, item).Body.Multipart
	if len(parts) != 3 {
		t.Fatalf("got %d parts", len(parts))
	}
	for index, want := range []string{"first", "second", "third"} {
		if parts[index].Name != want {
			t.Fatalf("parts came back as %v, want first/second/third",
				[]string{parts[0].Name, parts[1].Name, parts[2].Name})
		}
	}
}

func resVarRequest(vars ...types.Variable) types.RequestItem {
	return types.RequestItem{
		Name: "Extract",
		Type: "http",
		Vars: types.RequestVars{Res: vars},
	}
}

// Post-response variables are what a request hands to the next one in a run.
// The name, the expression and the enabled flag all have to survive, because a
// dropped one breaks the chained request rather than this one — which is where
// it is hardest to trace back.
func TestPostResponseVariablesSurviveTheRoundTrip(t *testing.T) {
	item := resVarRequest(
		types.Variable{Name: "token", Value: "res.body.access_token", Enabled: true},
		types.Variable{Name: "userId", Value: "res.body.id", Enabled: false},
	)
	vars := yamlRoundTrip(t, item).Vars.Res
	if len(vars) != 2 {
		t.Fatalf("got %d variables, want 2: %+v", len(vars), vars)
	}
	if vars[0].Name != "token" || vars[0].Value != "res.body.access_token" {
		t.Errorf("first variable = %+v", vars[0])
	}
	if !vars[0].Enabled {
		t.Error("an enabled variable came back disabled")
	}
	if vars[1].Name != "userId" || vars[1].Value != "res.body.id" {
		t.Errorf("second variable = %+v", vars[1])
	}
	if vars[1].Enabled {
		t.Error("a disabled variable came back enabled, so it would start overwriting again")
	}
}

// The SECRET flag decides whether the value is shown in the UI and included in
// exports. Losing it on a reload silently exposes a value the user marked
// sensitive, and nothing in the app would flag the change.
func TestPostResponseVariableSecrecySurvivesTheRoundTrip(t *testing.T) {
	item := resVarRequest(
		types.Variable{Name: "token", Value: "res.body.token", Enabled: true, Secret: true},
		types.Variable{Name: "plain", Value: "res.body.id", Enabled: true},
	)
	vars := yamlRoundTrip(t, item).Vars.Res
	if len(vars) != 2 {
		t.Fatalf("got %d variables: %+v", len(vars), vars)
	}
	if !vars[0].Secret {
		t.Error("a secret variable came back as an ordinary one")
	}
	if vars[1].Secret {
		t.Error("an ordinary variable came back marked secret")
	}
}

// Variable.Value is an interface{}, and the selector it becomes is a jsonq
// EXPRESSION — text by definition. A non-string value is therefore stringified
// on the way out and comes back as a string. Pinned because it is a type change
// across a save, which is the kind of thing that surprises a reader of the
// struct definition.
func TestPostResponseVariableValuesComeBackAsExpressionText(t *testing.T) {
	item := resVarRequest(
		types.Variable{Name: "count", Value: 42, Enabled: true},
		types.Variable{Name: "flag", Value: true, Enabled: true},
	)
	vars := yamlRoundTrip(t, item).Vars.Res
	if len(vars) != 2 {
		t.Fatalf("got %d variables: %+v", len(vars), vars)
	}
	if vars[0].Value != "42" {
		t.Errorf("numeric value came back as %#v, want the string \"42\"", vars[0].Value)
	}
	if vars[1].Value != "true" {
		t.Errorf("boolean value came back as %#v, want the string \"true\"", vars[1].Value)
	}
}

func TestPostResponseVariablesKeepTheirOrder(t *testing.T) {
	item := resVarRequest(
		types.Variable{Name: "a", Value: "res.body.a", Enabled: true},
		types.Variable{Name: "b", Value: "res.body.b", Enabled: true},
		types.Variable{Name: "c", Value: "res.body.c", Enabled: true},
	)
	vars := yamlRoundTrip(t, item).Vars.Res
	if len(vars) != 3 {
		t.Fatalf("got %d variables", len(vars))
	}
	for index, want := range []string{"a", "b", "c"} {
		if vars[index].Name != want {
			t.Fatalf("variables came back as %v, want a/b/c",
				[]string{vars[0].Name, vars[1].Name, vars[2].Name})
		}
	}
}

// Neither serialiser may emit a bare `null` for an empty list: the parser and
// every other reader of these files treat a missing key and an explicit null
// differently, and the writers build empty slices rather than nil for exactly
// that reason.
func TestEmptyMultipartAndVariablesDoNotEmitNull(t *testing.T) {
	content, err := StringifyRequest(multipartRequest())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(content, "data: null") {
		t.Errorf("an empty multipart body emitted a null:\n%s", content)
	}
	if got := yamlMultipart(nil); got == nil {
		t.Error("yamlMultipart(nil) returned nil rather than an empty slice")
	}
	if got := yamlPostResponseActions(nil); got == nil {
		t.Error("yamlPostResponseActions(nil) returned nil rather than an empty slice")
	}
}
