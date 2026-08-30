package export

// What a Postman export leaves behind.
//
// Every test here pins a way the exporter used to lose content SILENTLY: the
// file was valid, the summary counted what the user expected, and the missing
// part was discovered by whoever opened the collection somewhere else.

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mutexdev/lite_api/internal/scripting"
	"github.com/mutexdev/lite_api/internal/types"
)

func exportedDocument(t *testing.T, collection types.Collection) (map[string]interface{}, PostmanExport) {
	t.Helper()
	result, err := BuildPostmanExport(collection)
	if err != nil {
		t.Fatalf("BuildPostmanExport: %v", err)
	}
	var document map[string]interface{}
	if err := json.Unmarshal([]byte(result.Content), &document); err != nil {
		t.Fatalf("export is not valid JSON: %v", err)
	}
	return document, result
}

// exportedRequestPaths walks the emitted tree and returns "Folder/Request" for
// every request, so a test can assert both presence AND placement, and see a
// request emitted twice.
func exportedRequestPaths(document map[string]interface{}) []string {
	paths := []string{}
	var walk func(entries []interface{}, prefix string)
	walk = func(entries []interface{}, prefix string) {
		for _, value := range entries {
			entry, ok := value.(map[string]interface{})
			if !ok {
				continue
			}
			name, _ := entry["name"].(string)
			if children, ok := entry["item"].([]interface{}); ok {
				walk(children, strings.TrimPrefix(prefix+"/"+name, "/"))
				continue
			}
			paths = append(paths, strings.TrimPrefix(prefix+"/"+name, "/"))
		}
	}
	items, _ := document["item"].([]interface{})
	walk(items, "")
	return paths
}

func exportItem(name, folderPath string, seq int) types.RequestItem {
	item := types.NewRequestItem(name, "http", seq)
	item.URL = "https://example.test/" + strings.ToLower(name)
	item.FolderPath = folderPath
	return item
}

// THE ONE THAT MATTERS. A request whose folder has no FolderConfig row was
// emitted nowhere, counted nowhere, and reported nowhere. A partial import
// produces exactly that shape, and so does any intermediate folder without a
// config of its own.
func TestPostmanExportEmitsRequestsWhoseFolderHasNoConfig(t *testing.T) {
	collection := types.Collection{
		Name: "Orphans",
		// "A/B" has a config; its parent "A" does not. The old walk asked for the
		// children of "", found nothing whose parent was "", and stopped.
		Folders: []types.FolderConfig{{Path: "A/B", DisplayPath: "A/B", Name: "B", Seq: 1}},
		Items: []types.RequestItem{
			exportItem("Nested", "A/B", 1),
			exportItem("Homeless", "Ghost/Deeper", 2),
			exportItem("Root", "", 3),
		},
	}

	document, result := exportedDocument(t, collection)
	paths := exportedRequestPaths(document)

	for _, want := range []string{"A/B/Nested", "Ghost/Deeper/Homeless", "Root"} {
		found := false
		for _, got := range paths {
			if got == want {
				found = true
			}
		}
		if !found {
			t.Errorf("%q was not exported anywhere; emitted %v", want, paths)
		}
	}
	if len(paths) != len(collection.Items) {
		t.Errorf("exported %d requests from %d items: %v", len(paths), len(collection.Items), paths)
	}
	if result.RequestCount != len(collection.Items) {
		t.Errorf("RequestCount = %d, want %d — the summary told the user more was exported than the file holds", result.RequestCount, len(collection.Items))
	}
	// Two synthesised (A, Ghost, Ghost/Deeper) plus the configured one.
	if result.FolderCount != 4 {
		t.Errorf("FolderCount = %d, want 4", result.FolderCount)
	}
}

// A FolderConfig can carry a Path that differs from its DisplayPath, and items
// carry whichever the store wrote. Matching on DisplayPath alone dropped every
// request under a folder whose on-disk name differed by so much as its case.
func TestPostmanExportPlacesItemsStoredUnderTheFolderPath(t *testing.T) {
	collection := types.Collection{
		Name:    "Aliases",
		Folders: []types.FolderConfig{{Path: "users", DisplayPath: "Users", Name: "Users", Seq: 1}},
		Items: []types.RequestItem{
			exportItem("ByPath", "users", 1),
			exportItem("ByDisplayPath", "Users", 2),
			exportItem("Deeper", "users/admins", 3),
		},
	}

	document, result := exportedDocument(t, collection)
	paths := exportedRequestPaths(document)

	want := map[string]bool{"Users/ByPath": true, "Users/ByDisplayPath": true, "Users/admins/Deeper": true}
	for _, got := range paths {
		if !want[got] {
			t.Errorf("request emitted at %q; the on-disk path and the display path did not meet on one folder (all: %v)", got, paths)
		}
	}
	if result.RequestCount != 3 || len(paths) != 3 {
		t.Errorf("got %d requests (%v), want 3", len(paths), paths)
	}
}

// An unsupported request type must be REPORTED, not dropped. This is the one
// path where an item legitimately does not reach the file.
func TestPostmanExportReportsUnsupportedRequestTypes(t *testing.T) {
	websocket := types.NewRequestItem("Socket", "websocket", 1)
	grpc := types.NewRequestItem("Greeter", "grpc", 2)
	collection := types.Collection{Name: "Mixed", Items: []types.RequestItem{websocket, grpc, exportItem("Fine", "", 3)}}

	_, result := exportedDocument(t, collection)

	if strings.Join(result.SkippedTypes, ",") != "WebSocket,gRPC" {
		t.Errorf("SkippedTypes = %v, want [WebSocket gRPC]", result.SkippedTypes)
	}
	if result.RequestCount != 1 {
		t.Errorf("RequestCount = %d, want 1", result.RequestCount)
	}
}

func authModeOf(t *testing.T, auth types.AuthConfig) map[string]interface{} {
	t.Helper()
	item := exportItem("Auth", "", 1)
	item.Auth = auth
	document, _ := exportedDocument(t, types.Collection{Name: "Auth", Items: []types.RequestItem{item}})
	items, _ := document["item"].([]interface{})
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1", len(items))
	}
	entry, _ := items[0].(map[string]interface{})
	request, _ := entry["request"].(map[string]interface{})
	exported, _ := request["auth"].(map[string]interface{})
	return exported
}

// Five of the eight modes exported nothing at all.
func TestPostmanExportCarriesEveryAuthMode(t *testing.T) {
	cases := []struct {
		mode string
		auth types.AuthConfig
		want string
	}{
		{"basic", types.AuthConfig{Mode: "basic", Username: "u", Password: "p"}, "basic"},
		{"bearer", types.AuthConfig{Mode: "bearer", Token: "t"}, "bearer"},
		{"apikey", types.AuthConfig{Mode: "apikey", APIKey: "k", APIValue: "v", APILocation: "query"}, "apikey"},
		{"digest", types.AuthConfig{Mode: "digest", Username: "u", Password: "p"}, "digest"},
		{"awsv4", types.AuthConfig{Mode: "awsv4", AWSV4: types.AWSV4Auth{AccessKeyID: "AK", SecretAccessKey: "SK", Region: "eu-west-1", Service: "s3"}}, "awsv4"},
		{"oauth1", types.AuthConfig{Mode: "oauth1", OAuth1: types.OAuth1Auth{ConsumerKey: "ck", ConsumerSecret: "cs"}}, "oauth1"},
		{"oauth2", types.AuthConfig{Mode: "oauth2", OAuth2: types.OAuth2Auth{GrantType: "client_credentials", ClientID: "id"}}, "oauth2"},
	}
	for _, testCase := range cases {
		t.Run(testCase.mode, func(t *testing.T) {
			exported := authModeOf(t, testCase.auth)
			if exported == nil {
				t.Fatalf("%s exported no auth block at all", testCase.mode)
			}
			if exported["type"] != testCase.want {
				t.Errorf("type = %v, want %s", exported["type"], testCase.want)
			}
			values, _ := exported[testCase.want].([]interface{})
			if len(values) == 0 {
				t.Errorf("%s exported an empty credential list", testCase.mode)
			}
		})
	}
}

// THE DANGEROUS ONE. No auth block means INHERIT in Postman, so exporting
// nothing for mode "none" handed the collection credential to an endpoint that
// had deliberately opted out of it.
func TestPostmanExportWritesNoauthForModeNone(t *testing.T) {
	exported := authModeOf(t, types.AuthConfig{Mode: "none"})
	if exported == nil || exported["type"] != "noauth" {
		t.Errorf("auth = %v, want an explicit noauth block; an absent one re-imports as inherit and leaks the collection credential", exported)
	}
	if inherited := authModeOf(t, types.AuthConfig{Mode: "inherit"}); inherited != nil {
		t.Errorf("mode inherit exported %v, want no auth block", inherited)
	}
}

func TestPostmanExportCarriesRequestSettings(t *testing.T) {
	item := exportItem("Settings", "", 1)
	item.Settings.VerifyTLS = false
	item.Settings.FollowRedirects = false
	item.Settings.MaxRedirects = 9

	document, _ := exportedDocument(t, types.Collection{Name: "Settings", Items: []types.RequestItem{item}})
	items, _ := document["item"].([]interface{})
	entry, _ := items[0].(map[string]interface{})
	behavior, ok := entry["protocolProfileBehavior"].(map[string]interface{})
	if !ok {
		t.Fatalf("no protocolProfileBehavior was exported: %v", entry)
	}
	if behavior["strictSSL"] != false || behavior["followRedirects"] != false || behavior["maxRedirects"] != float64(9) {
		t.Errorf("protocolProfileBehavior = %v", behavior)
	}
}

func TestPostmanExportCarriesResponseExamples(t *testing.T) {
	item := exportItem("Examples", "", 1)
	item.Examples = []types.ResponseExample{{
		ID:   "example-1",
		Name: "Success",
		Type: "http",
		Request: types.ResponseExampleRequest{
			Method:  "GET",
			URL:     "https://example.test/examples",
			Headers: []types.KeyValue{{Name: "Accept", Value: "application/json", Enabled: true}},
		},
		Response: types.ResponseExamplePayload{
			Status:     201,
			StatusText: "Created",
			Headers:    []types.KeyValue{{Name: "Content-Type", Value: "application/json", Enabled: true}},
			BodyType:   "json",
			Body:       `{"ok":true}`,
		},
	}}

	document, _ := exportedDocument(t, types.Collection{Name: "Examples", Items: []types.RequestItem{item}})
	items, _ := document["item"].([]interface{})
	entry, _ := items[0].(map[string]interface{})
	responses, ok := entry["response"].([]interface{})
	if !ok || len(responses) != 1 {
		t.Fatalf("got %v response examples, want 1", entry["response"])
	}
	response, _ := responses[0].(map[string]interface{})
	if response["name"] != "Success" || response["code"] != float64(201) || response["status"] != "Created" {
		t.Errorf("example header fields = %v", response)
	}
	if response["body"] != `{"ok":true}` {
		t.Errorf("example body = %v", response["body"])
	}
	if _, ok := response["originalRequest"].(map[string]interface{}); !ok {
		t.Errorf("the example carries no originalRequest: %v", response)
	}
}

// The collection and folder Tests slots were passed to the exporter as the
// empty string, so a collection-wide test simply stopped running.
func TestPostmanExportCarriesCollectionAndFolderTests(t *testing.T) {
	collection := types.Collection{
		Name:    "Tests",
		Tests:   "pm.test('collection wide', function () {});",
		Folders: []types.FolderConfig{{Path: "F", DisplayPath: "F", Name: "F", Seq: 1, Tests: "pm.test('folder wide', function () {});"}},
		Items:   []types.RequestItem{exportItem("One", "F", 1)},
	}

	_, result := exportedDocument(t, collection)

	if !strings.Contains(result.Content, "collection wide") {
		t.Error("the collection Tests slot did not reach the export")
	}
	if !strings.Contains(result.Content, "folder wide") {
		t.Error("the folder Tests slot did not reach the export")
	}
}

// Postman has no collection- or folder-level header. Folding them into each
// request would change what those requests send; dropping them in silence is
// what used to happen.
func TestPostmanExportReportsHeadersItCannotCarry(t *testing.T) {
	collection := types.Collection{
		Name:    "Headers",
		Headers: []types.KeyValue{{Name: "X-Collection", Value: "1", Enabled: true}},
		Folders: []types.FolderConfig{{Path: "F", DisplayPath: "F", Name: "F", Seq: 1, Headers: []types.KeyValue{{Name: "X-Folder", Value: "1", Enabled: true}}}},
		Items:   []types.RequestItem{exportItem("One", "F", 1)},
	}

	_, result := exportedDocument(t, collection)

	if len(result.Warnings) != 2 {
		t.Fatalf("warnings = %v, want one for the collection headers and one for the folder headers", result.Warnings)
	}
	if strings.Contains(result.Content, "X-Collection") || strings.Contains(result.Content, "X-Folder") {
		t.Error("a collection or folder header was merged into a request; the exported requests now send something they did not")
	}
}

// The legacy assertion DSL is a syntax error in Postman's JavaScript-only test
// event, and one bad line stops every real assertion in the same block.
func TestPostmanExportTranslatesTheAssertionDSL(t *testing.T) {
	item := exportItem("Asserts", "", 1)
	item.Tests = "expect status equals 200\nexpect status notEquals 500\npm.test('js still runs', function () {});"

	_, result := exportedDocument(t, types.Collection{Name: "Asserts", Items: []types.RequestItem{item}})

	for _, want := range []string{
		`pm.test(\"status equals 200\", function () { pm.expect(String(pm.response.code)).to.eql(\"200\"); });`,
		`pm.expect(String(pm.response.code)).to.not.eql(\"500\")`,
		"js still runs",
	} {
		if !strings.Contains(result.Content, want) {
			t.Errorf("the export does not contain %s\n%s", want, result.Content)
		}
	}
	if strings.Contains(result.Content, `"expect status equals 200"`) {
		t.Error("an assertion line was exported verbatim; Postman fails the whole script on it")
	}
	if len(result.Warnings) != 0 {
		t.Errorf("a translatable assertion produced a warning: %v", result.Warnings)
	}
}

func TestPostmanExportCommentsOutUntranslatableAssertions(t *testing.T) {
	item := exportItem("Asserts", "", 1)
	item.Tests = "expect header equals ok"

	_, result := exportedDocument(t, types.Collection{Name: "Asserts", Items: []types.RequestItem{item}})

	if !strings.Contains(result.Content, "// expect header equals ok") {
		t.Errorf("an untranslatable assertion was not commented out:\n%s", result.Content)
	}
	if len(result.Warnings) == 0 {
		t.Error("an untranslatable assertion produced no warning")
	}
}

// postmanAssertionGrammarIsMirrored. The DSL is evaluated by unexported helpers
// in internal/scripting, so the operator table here is a mirror rather than a
// shared parser. This is what fails if the two drift: every operator this
// package claims to translate must be one the evaluator actually implements.
func TestPostmanAssertionGrammarIsMirrored(t *testing.T) {
	// An operator the evaluator does not implement returns false for every
	// input, so one of these pairs matching is proof it exists.
	probes := [][2]string{{"200", "200"}, {"200", "404"}, {"2004", "200"}, {"200x", "200"}, {"x200", "200"}}
	for operator := range postmanAssertionOperators {
		matched := false
		for _, probe := range probes {
			if scripting.CompareAssertion(probe[0], operator, probe[1]) {
				matched = true
			}
		}
		if !matched {
			t.Errorf("the exporter translates %q, but the evaluator does not implement it", operator)
		}
	}
	// And an operator the evaluator does implement must not be silently
	// commented out on export.
	for _, operator := range []string{"equals", "==", "notEquals", "!=", "contains", "startsWith", "endsWith"} {
		if _, ok := translatePostmanAssertionLine("expect status " + operator + " 200"); !ok {
			t.Errorf("%q is a supported assertion operator that the export cannot translate", operator)
		}
	}
}

// The id has to be stable: a random one makes every re-export of an unchanged
// collection a diff, and Postman uses it to recognise a re-import.
func TestPostmanExportWritesADeterministicCollectionID(t *testing.T) {
	collection := types.Collection{ID: "collection-1", Name: "Same", Docs: "Collection docs"}

	first, _ := exportedDocument(t, collection)
	second, _ := exportedDocument(t, collection)

	info, _ := first["info"].(map[string]interface{})
	other, _ := second["info"].(map[string]interface{})
	identifier, _ := info["_postman_id"].(string)
	if identifier == "" || identifier != other["_postman_id"] {
		t.Errorf("_postman_id = %q then %v", identifier, other["_postman_id"])
	}
	if len(strings.Split(identifier, "-")) != 5 {
		t.Errorf("_postman_id %q is not UUID-shaped", identifier)
	}
	if info["description"] != "Collection docs" {
		t.Errorf("the collection description was dropped: %v", info["description"])
	}
}
