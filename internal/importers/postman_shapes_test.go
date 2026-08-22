// Tolerance for the shapes a real Postman export takes (US-054).
//
// Every case here came from a document Postman itself writes or accepts. The
// importer used to unmarshal into rigid structs, so the first mismatched field
// aborted the whole collection: one saved example with a string status code
// discarded five hundred requests and reported "selected import could not be
// read safely".
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

func requestNames(collection types.Collection) []string {
	names := make([]string, 0, len(collection.Items))
	for _, item := range collection.Items {
		names = append(names, item.Name)
	}
	return names
}

func TestPostmanRequestShorthandStringIsAURL(t *testing.T) {
	collection, _ := importPostmanForTest(t, `{"info":{"name":"A"},"item":[{"name":"r","request":"https://example.test/x"}]}`)
	item := findItemByName(t, collection, "r")
	if item.URL != "https://example.test/x" || item.Method != "GET" {
		t.Fatalf("shorthand request = %q %q", item.Method, item.URL)
	}
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

func TestPostmanURLObjectWithoutRawIsRebuilt(t *testing.T) {
	collection, warnings := importPostmanForTest(t, `{"info":{"name":"A"},"item":[{"name":"r","request":{"method":"GET","url":{"protocol":"https","host":["api","example","test"],"port":"8443","path":["v1","users",":userId"],"query":[{"key":"page","value":"2"},{"key":"skip","value":"1","disabled":true}],"hash":"top"}}}]}`)
	item := findItemByName(t, collection, "r")
	if item.URL != "https://api.example.test:8443/v1/users/:userId?page=2#top" {
		t.Fatalf("rebuilt URL = %q", item.URL)
	}
	for _, warning := range warnings {
		if strings.Contains(warning, "{{host}}") {
			t.Fatalf("rebuilt URL still warned: %q", warning)
		}
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

func TestPostmanFileBodyIsImported(t *testing.T) {
	collection, _ := importPostmanForTest(t, `{"info":{"name":"A"},"item":[{"name":"r","request":{"method":"PUT","url":"https://e.test","body":{"mode":"file","file":{"src":"./payload.bin"}}}}]}`)
	body := findItemByName(t, collection, "r").Body
	if body.Mode != "file" || len(body.Files) != 1 || body.Files[0].FilePath != "./payload.bin" || !body.Files[0].Selected {
		t.Fatalf("file body = %#v", body)
	}
}

func TestPostmanDisabledVariableIsImportedDisabled(t *testing.T) {
	collection, _ := importPostmanForTest(t, `{"info":{"name":"A"},"item":[],"variable":[{"key":"on","value":"1"},{"key":"off","value":"2","disabled":true}]}`)
	if len(collection.Variables) != 2 || !collection.Variables[0].Enabled || collection.Variables[1].Enabled {
		t.Fatalf("variables = %#v", collection.Variables)
	}
}

func TestPostmanDuplicateSiblingFoldersDoNotMerge(t *testing.T) {
	collection, _ := importPostmanForTest(t, `{"info":{"name":"A"},"item":[{"name":"Users","item":[{"name":"a","request":{"method":"GET","url":"https://e.test"}}]},{"name":"Users","item":[{"name":"b","request":{"method":"GET","url":"https://e.test"}}]}]}`)
	if len(collection.Folders) != 2 {
		t.Fatalf("folders = %#v", collection.Folders)
	}
	if collection.Folders[0].Path == collection.Folders[1].Path {
		t.Fatalf("sibling folders share a path: %q", collection.Folders[0].Path)
	}
	paths := map[string]bool{}
	for _, item := range collection.Items {
		paths[item.FolderPath] = true
	}
	if len(paths) != 2 {
		t.Fatalf("requests collapsed into %v", paths)
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
