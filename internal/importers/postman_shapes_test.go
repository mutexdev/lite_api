// The shapes a real Postman export takes, and the tolerance the importer needs
// to read them (US-054).
//
// Every case here came from a document Postman itself writes or accepts. Each
// was either a hard failure of the WHOLE import -- the importer unmarshalled
// into rigid structs, so one saved example with a string status code discarded
// five hundred requests and reported "selected import could not be read safely"
// -- or a silent one, where the collection imported cleanly and the request did
// something other than what it said.
package importers

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mutexdev/lite_api/internal/types"
)

func importPostmanForTest(t *testing.T, content string) (types.Collection, []string) {
	t.Helper()
	collection, warnings, err := ImportPostman(content, "Fixture", false)
	if err != nil {
		t.Fatalf("ImportPostman: %v", err)
	}
	return collection, warnings
}

func findItemByName(t *testing.T, collection types.Collection, name string) types.RequestItem {
	t.Helper()
	for _, item := range collection.Items {
		if item.Name == name {
			return item
		}
	}
	t.Fatalf("no request named %q in %v", name, requestNames(collection))
	return types.RequestItem{}
}

// requestNames reports each request with the folder it landed in, because most
// of what goes wrong here is a request that imported into the wrong place.
func requestNames(collection types.Collection) []string {
	names := make([]string, 0, len(collection.Items))
	for _, item := range collection.Items {
		names = append(names, item.Name+"@"+item.FolderPath)
	}
	return names
}

func TestPostmanHeaderBlockMayBeAString(t *testing.T) {
	collection, _ := importPostmanForTest(t, `{"info":{"name":"A"},"item":[{"name":"r","request":{"method":"GET","url":"https://e.test","header":"Accept: application/json\nX-Trace: on\n"}}]}`)
	headers := findItemByName(t, collection, "r").Headers
	if len(headers) != 2 || headers[0].Name != "Accept" || headers[0].Value != "application/json" || headers[1].Name != "X-Trace" {
		t.Fatalf("headers = %#v", headers)
	}
}

func TestPostmanHeaderValuesMayBeScalars(t *testing.T) {
	collection, _ := importPostmanForTest(t, `{"info":{"name":"A"},"item":[{"name":"r","request":{"method":"GET","url":"https://e.test","header":[{"key":"N","value":5},{"key":"B","value":true},{"key":"Z","value":null}]}}]}`)
	headers := findItemByName(t, collection, "r").Headers
	if len(headers) != 3 || headers[0].Value != "5" || headers[1].Value != "true" || headers[2].Value != "" {
		t.Fatalf("headers = %#v", headers)
	}
}

func TestPostmanRawBodyMayBeAnObject(t *testing.T) {
	collection, _ := importPostmanForTest(t, `{"info":{"name":"A"},"item":[{"name":"r","request":{"method":"POST","url":"https://e.test","body":{"mode":"raw","raw":{"hello":"world"}}}}]}`)
	body := findItemByName(t, collection, "r").Body
	if body.Mode != "json" || !strings.Contains(body.JSON, `"hello"`) {
		t.Fatalf("body = %#v", body)
	}
}

func TestPostmanSavedExampleAcceptsStringCodeAndStringHeaders(t *testing.T) {
	collection, _ := importPostmanForTest(t, `{"info":{"name":"A"},"item":[{"name":"r","request":{"method":"GET","url":"https://e.test"},"response":[{"name":"ok","code":"200","header":"Content-Type: application/json","body":"{}"},{"name":"empty","code":204,"header":null,"body":null}]}]}`)
	examples := findItemByName(t, collection, "r").Examples
	if len(examples) != 2 {
		t.Fatalf("examples = %#v", examples)
	}
	if examples[0].Response.Status != 200 || len(examples[0].Response.Headers) != 1 {
		t.Fatalf("first example = %#v", examples[0].Response)
	}
	if examples[1].Response.Status != 204 {
		t.Fatalf("second example = %#v", examples[1].Response)
	}
}

func TestPostmanByteOrderMarkIsStripped(t *testing.T) {
	collection, _ := importPostmanForTest(t, "\ufeff"+`{"info":{"name":"BOM"},"item":[{"name":"r","request":{"method":"GET","url":"https://e.test"}}]}`)
	if collection.Name != "BOM" || len(collection.Items) != 1 {
		t.Fatalf("collection = %q items=%d", collection.Name, len(collection.Items))
	}
}

func TestPostmanItemMayBeASingleObject(t *testing.T) {
	collection, _ := importPostmanForTest(t, `{"info":{"name":"A"},"item":{"name":"r","request":{"method":"GET","url":"https://e.test"}}}`)
	if len(collection.Items) != 1 {
		t.Fatalf("items = %#v", requestNames(collection))
	}
}

func TestPostmanBrokenItemIsWarnedAboutAndNeighboursSurvive(t *testing.T) {
	collection, warnings := importPostmanForTest(t, `{"info":{"name":"A"},"item":[{"name":"good","request":{"method":"GET","url":"https://e.test"}},{"name":"bad","request":{"method":42,"url":"https://e.test"}},{"name":"also good","request":{"method":"GET","url":"https://e.test"}}]}`)
	if len(collection.Items) != 2 {
		t.Fatalf("surviving requests = %v", requestNames(collection))
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "bad") {
		t.Fatalf("warnings = %#v", warnings)
	}
}

func TestPostmanRejectsDocumentsThatAreNotCollections(t *testing.T) {
	for name, content := range map[string]string{
		"arbitrary json": `{"hello":"world"}`,
		"environment":    `{"id":"e","name":"Env","values":[{"key":"a","value":"1"}],"_postman_variable_scope":"environment"}`,
		"empty object":   `{}`,
	} {
		if _, _, err := ImportPostman(content, "X", false); err == nil {
			t.Fatalf("%s: imported as a collection", name)
		}
	}
}

func TestPostmanAcceptsACollectionWithNoRequestsYet(t *testing.T) {
	if _, _, err := ImportPostman(`{"info":{"name":"Empty"},"item":[]}`, "X", false); err != nil {
		t.Fatalf("empty but valid collection rejected: %v", err)
	}
}

// A URL object is allowed to carry no raw at all -- the parts ARE the URL.
// Every such request imported as the literal string "{{host}}" and pointed
// nowhere.
func TestPostmanURLObjectWithoutRawIsRebuilt(t *testing.T) {
	collection, warnings := importPostmanForTest(t, `{"info":{"name":"A"},"item":[{"name":"r","request":{"method":"GET","url":{"protocol":"https","host":["api","example","test"],"port":"8443","path":["v1","users",":userId"],"query":[{"key":"page","value":"2"},{"key":"skip","value":"1","disabled":true}],"hash":"top"}}}]}`)
	item := findItemByName(t, collection, "r")
	// The query lives in the structured params, not in the URL as well: the
	// send path appends the enabled params to whatever the URL already says.
	if item.URL != "https://api.example.test:8443/v1/users/:userId#top" {
		t.Fatalf("rebuilt URL = %q", item.URL)
	}
	if len(item.Params) != 2 || item.Params[0].Name != "page" || item.Params[1].Enabled {
		t.Fatalf("params = %+v", item.Params)
	}
	for _, warning := range warnings {
		if strings.Contains(warning, "{{host}}") {
			t.Fatalf("rebuilt URL still warned: %q", warning)
		}
	}
}

func TestImportPostmanRebuildsURLFromItsParts(t *testing.T) {
	collection, _ := importPostmanForTest(t, `{
	  "info": {"name": "parts"},
	  "item": [
	    {"name": "Parts", "request": {"method": "GET", "url": {
	      "protocol": "https", "host": ["api", "example", "test"], "port": "8443",
	      "path": ["v2", "reports"], "hash": "top"
	    }}},
	    {"name": "Raw wins", "request": {"method": "GET", "url": {"raw": "https://raw.example.test/x", "host": ["ignored"]}}}
	  ]
	}`)

	if got := findItemByName(t, collection, "Parts").URL; got != "https://api.example.test:8443/v2/reports#top" {
		t.Errorf("URL = %q", got)
	}
	if got := findItemByName(t, collection, "Raw wins").URL; got != "https://raw.example.test/x" {
		t.Errorf("raw should still win: %q", got)
	}
}

// THE DOUBLED QUERY. Postman writes the query twice -- inside url.raw and again
// as url.query -- and keeping both sent ?imported=true&imported=true.
func TestImportPostmanDoesNotDoubleQueryParams(t *testing.T) {
	collection, _ := importPostmanForTest(t, `{
	  "info": {"name": "query"},
	  "item": [
	    {"name": "Structured", "request": {"method": "GET", "url": {
	      "raw": "https://example.test/search?imported=true&page=2#frag",
	      "query": [{"key": "imported", "value": "true"}, {"key": "page", "value": "2", "disabled": true}]
	    }}},
	    {"name": "Raw only", "request": {"method": "GET", "url": {"raw": "https://example.test/search?only=1"}}}
	  ]
	}`)

	structured := findItemByName(t, collection, "Structured")
	if structured.URL != "https://example.test/search#frag" {
		t.Errorf("URL = %q; the query string must not survive alongside the structured params", structured.URL)
	}
	if len(structured.Params) != 2 || structured.Params[0].Name != "imported" || structured.Params[1].Enabled {
		t.Errorf("params = %+v", structured.Params)
	}
	// Without url.query there is nothing structured to take over, so the raw
	// query is the only copy and must stay.
	if got := findItemByName(t, collection, "Raw only").URL; got != "https://example.test/search?only=1" {
		t.Errorf("a raw-only query was lost: %q", got)
	}
}

func TestPostmanUnreadableURLFallsBackWithAWarning(t *testing.T) {
	collection, warnings := importPostmanForTest(t, `{"info":{"name":"A"},"item":[{"name":"r","request":{"method":"GET","url":{"nothing":"useful"}}}]}`)
	if findItemByName(t, collection, "r").URL != "{{host}}" {
		t.Fatal("expected the documented fallback")
	}
	if len(warnings) == 0 || !strings.Contains(strings.Join(warnings, "\n"), "r") {
		t.Fatalf("warnings = %#v", warnings)
	}
}

// Postman v2.0 writes the request as a bare URL string and the headers as one
// newline-separated block. Both used to fail the ENTIRE file with a Go
// unmarshal error naming a struct field.
func TestImportPostmanAcceptsV2Dot0Shapes(t *testing.T) {
	collection, warnings := importPostmanForTest(t, `{
	  "info": {"name": "v2.0"},
	  "item": [
	    {"name": "Bare URL", "request": "https://example.test/legacy"},
	    {"name": "String headers", "request": {"method": "POST", "url": "https://example.test/h",
	      "header": "Content-Type: application/json\nX-Trace: abc"}}
	  ]
	}`)

	if len(warnings) != 0 {
		t.Errorf("a readable v2.0 collection produced warnings: %v", warnings)
	}
	bare := findItemByName(t, collection, "Bare URL")
	if bare.URL != "https://example.test/legacy" || bare.Method != "GET" {
		t.Errorf("bare request = %s %s", bare.Method, bare.URL)
	}
	headers := findItemByName(t, collection, "String headers").Headers
	if len(headers) != 2 || headers[0].Name != "Content-Type" || headers[1].Value != "abc" {
		t.Errorf("headers = %+v", headers)
	}
}

// One unreadable item must cost that item, not the collection -- and the
// warning must name the folder it was in, not the folder above it.
func TestImportPostmanSkipsUnreadableItemsWithAWarning(t *testing.T) {
	collection, warnings := importPostmanForTest(t, `{
	  "info": {"name": "partial"},
	  "item": [
	    {"name": "Good", "request": {"method": "GET", "url": "https://example.test/good"}},
	    ["not an item at all"],
	    {"name": "Folder", "item": [
	      12345,
	      {"name": "Nested", "request": {"method": "GET", "url": "https://example.test/nested"}}
	    ]}
	  ]
	}`)

	if len(collection.Items) != 2 {
		t.Fatalf("got %d requests, want the two readable ones: %v", len(collection.Items), requestNames(collection))
	}
	findItemByName(t, collection, "Good")
	findItemByName(t, collection, "Nested")
	if len(warnings) != 2 {
		t.Fatalf("warnings = %v, want one per unreadable item", warnings)
	}
	if !strings.Contains(strings.Join(warnings, " "), "Folder") {
		t.Errorf("a warning does not say where the item was: %v", warnings)
	}
}

func TestPostmanFileBodyIsImported(t *testing.T) {
	collection, _ := importPostmanForTest(t, `{"info":{"name":"A"},"item":[{"name":"r","request":{"method":"PUT","url":"https://e.test","body":{"mode":"file","file":{"src":"./payload.bin","contentType":"application/octet-stream"}}}}]}`)
	body := findItemByName(t, collection, "r").Body
	if body.Mode != "file" {
		t.Fatalf("body mode = %q, want file -- the request imported as one that sends nothing", body.Mode)
	}
	files := types.FileBodyEntriesOf(body)
	if len(files) != 1 || files[0].FilePath != "./payload.bin" || !files[0].Selected {
		t.Fatalf("file body = %#v", files)
	}
	if files[0].ContentType != "application/octet-stream" {
		t.Errorf("the declared content type was lost: %#v", files[0])
	}
}

// Two sibling folders with the same name, two whose names sanitise to the same
// string, and a folder literally called "untitled" all used to collapse into
// one path -- merging unrelated requests, invisibly.
func TestImportPostmanKeepsSiblingFoldersDistinct(t *testing.T) {
	collection, _ := importPostmanForTest(t, `{
	  "info": {"name": "folders"},
	  "item": [
	    {"name": "Reports", "item": [{"name": "One", "request": {"method": "GET", "url": "https://example.test/1"}}]},
	    {"name": "Reports", "item": [{"name": "Two", "request": {"method": "GET", "url": "https://example.test/2"}}]},
	    {"name": "A/B", "item": [{"name": "Three", "request": {"method": "GET", "url": "https://example.test/3"}}]},
	    {"name": "A-B", "item": [{"name": "Four", "request": {"method": "GET", "url": "https://example.test/4"}}]},
	    {"name": "untitled", "item": [{"name": "Five", "request": {"method": "GET", "url": "https://example.test/5"}}]},
	    {"name": "Empty", "item": []}
	  ]
	}`)

	paths := map[string]bool{}
	for _, folder := range collection.Folders {
		if paths[folder.Path] {
			t.Errorf("two folders claim the path %q", folder.Path)
		}
		paths[folder.Path] = true
	}
	if len(collection.Folders) != 6 {
		t.Fatalf("got %d folders, want 6: %v", len(collection.Folders), paths)
	}
	if !paths["Empty"] {
		t.Error("an empty folder was dropped; the user created it deliberately")
	}
	if !paths["untitled"] {
		t.Errorf("the folder named untitled was hoisted into its parent: %v", paths)
	}
	folders := map[string]string{}
	for _, item := range collection.Items {
		folders[item.Name] = item.FolderPath
	}
	if folders["One"] == folders["Two"] {
		t.Errorf("two same-named sibling folders merged: both requests are in %q", folders["One"])
	}
	if folders["Three"] == folders["Four"] {
		t.Errorf("A/B and A-B collided: both requests are in %q", folders["Three"])
	}
	if folders["Five"] != "untitled" {
		t.Errorf("the request in the untitled folder landed at %q", folders["Five"])
	}
}

// Two siblings that differ only in case would still be one directory on the
// filesystems this ships on.
func TestImportPostmanKeepsCaseInsensitiveSiblingFoldersDistinct(t *testing.T) {
	collection, _ := importPostmanForTest(t, `{
	  "info": {"name": "case"},
	  "item": [
	    {"name": "Users", "item": [{"name": "One", "request": {"method": "GET", "url": "https://example.test/1"}}]},
	    {"name": "users", "item": [{"name": "Two", "request": {"method": "GET", "url": "https://example.test/2"}}]}
	  ]
	}`)
	if len(collection.Folders) != 2 {
		t.Fatalf("folders = %#v", collection.Folders)
	}
	if strings.EqualFold(collection.Folders[0].Path, collection.Folders[1].Path) {
		t.Fatalf("Users and users share a directory: %q", collection.Folders[0].Path)
	}
}

func TestPostmanEmptyFolderIsKept(t *testing.T) {
	collection, _ := importPostmanForTest(t, `{"info":{"name":"A"},"item":[{"name":"Nothing here","item":[]}]}`)
	if len(collection.Folders) != 1 || collection.Folders[0].Name != "Nothing here" {
		t.Fatalf("folders = %#v", collection.Folders)
	}
}

func TestPostmanStrictSSLBecomesTheVerifyTLSSetting(t *testing.T) {
	collection, _ := importPostmanForTest(t, `{"info":{"name":"A"},"protocolProfileBehavior":{"strictSSL":false},"item":[{"name":"inherits","request":{"method":"GET","url":"https://e.test"}},{"name":"overrides","protocolProfileBehavior":{"strictSSL":true},"request":{"method":"GET","url":"https://e.test"}}]}`)
	if findItemByName(t, collection, "inherits").Settings.VerifyTLS {
		t.Fatal("collection strictSSL:false did not reach the request")
	}
	if !findItemByName(t, collection, "overrides").Settings.VerifyTLS {
		t.Fatal("request strictSSL:true did not override the collection")
	}
}

func TestPostmanCollectionDescriptionBecomesDocs(t *testing.T) {
	object, _ := importPostmanForTest(t, `{"info":{"name":"A","description":{"content":"# Title","type":"text/markdown"}},"item":[]}`)
	if object.Docs != "# Title" {
		t.Fatalf("object description = %q", object.Docs)
	}
	plain, _ := importPostmanForTest(t, `{"info":{"name":"A","description":"plain"},"item":[]}`)
	if plain.Docs != "plain" {
		t.Fatalf("string description = %q", plain.Docs)
	}
}

func TestPostmanRequestDescriptionObjectBecomesDocs(t *testing.T) {
	collection, _ := importPostmanForTest(t, `{"info":{"name":"A"},"item":[{"name":"r","request":{"method":"GET","url":"https://e.test","description":{"content":"how to","type":"text/markdown"}}}]}`)
	if findItemByName(t, collection, "r").Docs != "how to" {
		t.Fatalf("docs = %q", findItemByName(t, collection, "r").Docs)
	}
}

func TestImportPostmanReadsSettingsDescriptionsAndDisabledVariables(t *testing.T) {
	collection, _ := importPostmanForTest(t, `{
	  "info": {"name": "details", "description": "What this collection is for"},
	  "variable": [
	    {"key": "live", "value": "1"},
	    {"key": "parked", "value": "2", "disabled": true}
	  ],
	  "item": [
	    {"name": "Docs", "description": {"content": "Folder documentation"}, "item": [
	      {"name": "Tuned", "protocolProfileBehavior": {"strictSSL": false, "followRedirects": false, "maxRedirects": 12},
	       "request": {"method": "GET", "url": "https://example.test/tuned", "description": "Request documentation"}},
	      {"name": "Default", "request": {"method": "GET", "url": "https://example.test/default"}}
	    ]}
	  ]
	}`)

	if collection.Docs != "What this collection is for" {
		t.Errorf("collection docs = %q", collection.Docs)
	}
	if len(collection.Folders) != 1 || collection.Folders[0].Docs != "Folder documentation" {
		t.Errorf("folder docs = %+v", collection.Folders)
	}
	if len(collection.Variables) != 2 || !collection.Variables[0].Enabled || collection.Variables[1].Enabled {
		t.Errorf("variables = %+v; a disabled one that imports enabled starts resolving placeholders again", collection.Variables)
	}

	tuned := findItemByName(t, collection, "Tuned")
	if tuned.Docs != "Request documentation" {
		t.Errorf("request docs = %q", tuned.Docs)
	}
	if tuned.Settings.VerifyTLS || tuned.Settings.FollowRedirects || tuned.Settings.MaxRedirects != 12 {
		t.Errorf("settings = %+v", tuned.Settings)
	}
	// THE DEFAULT. Absence of protocolProfileBehavior must leave the request
	// defaults alone rather than importing every request with TLS off.
	fallback := findItemByName(t, collection, "Default")
	if !fallback.Settings.VerifyTLS || !fallback.Settings.FollowRedirects || fallback.Settings.MaxRedirects != 5 {
		t.Errorf("a request with no protocolProfileBehavior lost its defaults: %+v", fallback.Settings)
	}
}

func TestPostmanRealWorldFixtureImportsCompletely(t *testing.T) {
	content, err := os.ReadFile(filepath.Join("..", "..", "docs", "qa", "import-fixtures", "postman-realworld.json"))
	if err != nil {
		t.Fatal(err)
	}
	collection, warnings, err := ImportPostman(string(content), "Fixture", false)
	if err != nil {
		t.Fatalf("the fixture must import: %v", err)
	}
	if collection.Name != "Real World Export" {
		t.Fatalf("name = %q", collection.Name)
	}
	// Every item except the deliberately broken one.
	wantRequests := []string{
		"Shorthand string request", "String header block", "Scalar header values", "Object raw body",
		"URL object without raw", "File body", "Strict SSL on for this one", "Saved examples in odd shapes",
		"List", "Nested leaf", "Duplicate sibling folder request", "GraphQL",
	}
	got := map[string]bool{}
	for _, item := range collection.Items {
		got[item.Name] = true
	}
	for _, name := range wantRequests {
		if !got[name] {
			t.Errorf("fixture lost request %q; imported %v", name, requestNames(collection))
		}
	}
	if len(collection.Items) != len(wantRequests) {
		t.Errorf("imported %d requests, wanted %d: %v", len(collection.Items), len(wantRequests), requestNames(collection))
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "Broken item") {
		t.Errorf("warnings = %#v", warnings)
	}
	if collection.Auth.Mode != "bearer" || collection.Auth.Token != "{{access_token}}" {
		t.Errorf("collection auth = %#v", collection.Auth)
	}
	if collection.PreScript == "" || collection.PostScript == "" {
		t.Errorf("collection scripts = %q / %q", collection.PreScript, collection.PostScript)
	}
	if len(collection.Variables) != 3 || collection.Variables[1].Enabled || collection.Variables[2].Value != "8443" {
		t.Errorf("variables = %#v", collection.Variables)
	}
	if findItemByName(t, collection, "Strict SSL on for this one").Settings.VerifyTLS != true {
		t.Error("per-request strictSSL:true lost")
	}
	if findItemByName(t, collection, "Shorthand string request").Settings.VerifyTLS != false {
		t.Error("collection strictSSL:false lost")
	}
	if len(collection.Folders) != 4 {
		t.Errorf("folders = %#v", collection.Folders)
	}
}
