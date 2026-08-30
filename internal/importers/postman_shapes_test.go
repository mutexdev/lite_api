package importers

// The shapes a real Postman file arrives in.
//
// Each of these was either a hard failure of the WHOLE import — one item in a
// dialect this reader did not accept aborted a hundred-request collection with
// a raw Go type error — or a silent one, where the collection imported cleanly
// and the request did something other than what it said.

import (
	"strings"
	"testing"

	"github.com/mutexdev/lite_api/internal/types"
)

func importPostmanForTest(t *testing.T, content string) (types.Collection, []string) {
	t.Helper()
	collection, warnings, err := ImportPostmanWithWarnings(content, "shapes", false)
	if err != nil {
		t.Fatalf("ImportPostmanWithWarnings: %v", err)
	}
	return collection, warnings
}

func itemNamed(t *testing.T, collection types.Collection, name string) types.RequestItem {
	t.Helper()
	for _, item := range collection.Items {
		if item.Name == name {
			return item
		}
	}
	t.Fatalf("request %q was not imported; got %v", name, requestNames(collection))
	return types.RequestItem{}
}

func requestNames(collection types.Collection) []string {
	names := make([]string, 0, len(collection.Items))
	for _, item := range collection.Items {
		names = append(names, item.Name+"@"+item.FolderPath)
	}
	return names
}

// A URL object is allowed to carry no raw at all — the parts ARE the URL. Every
// such request imported as the literal string "{{host}}" and pointed nowhere.
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

	if got := itemNamed(t, collection, "Parts").URL; got != "https://api.example.test:8443/v2/reports#top" {
		t.Errorf("URL = %q", got)
	}
	if got := itemNamed(t, collection, "Raw wins").URL; got != "https://raw.example.test/x" {
		t.Errorf("raw should still win: %q", got)
	}
}

// THE DOUBLED QUERY. Postman writes the query twice — inside url.raw and again
// as url.query — and keeping both sent ?imported=true&imported=true.
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

	structured := itemNamed(t, collection, "Structured")
	if structured.URL != "https://example.test/search#frag" {
		t.Errorf("URL = %q; the query string must not survive alongside the structured params", structured.URL)
	}
	if len(structured.Params) != 2 || structured.Params[0].Name != "imported" || structured.Params[1].Enabled {
		t.Errorf("params = %+v", structured.Params)
	}
	// Without url.query there is nothing structured to take over, so the raw
	// query is the only copy and must stay.
	if got := itemNamed(t, collection, "Raw only").URL; got != "https://example.test/search?only=1" {
		t.Errorf("a raw-only query was lost: %q", got)
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
	bare := itemNamed(t, collection, "Bare URL")
	if bare.URL != "https://example.test/legacy" || bare.Method != "GET" {
		t.Errorf("bare request = %s %s", bare.Method, bare.URL)
	}
	headers := itemNamed(t, collection, "String headers").Headers
	if len(headers) != 2 || headers[0].Name != "Content-Type" || headers[1].Value != "abc" {
		t.Errorf("headers = %+v", headers)
	}
}

// One unreadable item must cost that item, not the collection.
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
	itemNamed(t, collection, "Good")
	itemNamed(t, collection, "Nested")
	if len(warnings) != 2 {
		t.Fatalf("warnings = %v, want one per unreadable item", warnings)
	}
	if !strings.Contains(strings.Join(warnings, " "), "Folder") {
		t.Errorf("a warning does not say where the item was: %v", warnings)
	}
}

func TestImportPostmanReadsTheFileBody(t *testing.T) {
	collection, _ := importPostmanForTest(t, `{
	  "info": {"name": "file"},
	  "item": [{"name": "Upload", "request": {"method": "POST", "url": "https://example.test/upload",
	    "body": {"mode": "file", "file": {"src": "/tmp/payload.bin"}}}}]
	}`)

	body := itemNamed(t, collection, "Upload").Body
	if body.Mode != "file" {
		t.Fatalf("body mode = %q, want file — the request imported as one that sends nothing", body.Mode)
	}
	files := types.FileBodyEntriesOf(body)
	if len(files) != 1 || files[0].FilePath != "/tmp/payload.bin" || !files[0].Selected {
		t.Errorf("file body = %+v", files)
	}
}

// Two sibling folders with the same name, two whose names sanitise to the same
// string, and a folder literally called "untitled" all used to collapse into
// one path — merging unrelated requests, invisibly.
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

	tuned := itemNamed(t, collection, "Tuned")
	if tuned.Docs != "Request documentation" {
		t.Errorf("request docs = %q", tuned.Docs)
	}
	if tuned.Settings.VerifyTLS || tuned.Settings.FollowRedirects || tuned.Settings.MaxRedirects != 12 {
		t.Errorf("settings = %+v", tuned.Settings)
	}
	// THE DEFAULT. Absence of protocolProfileBehavior must leave the request
	// defaults alone rather than importing every request with TLS off.
	fallback := itemNamed(t, collection, "Default")
	if !fallback.Settings.VerifyTLS || !fallback.Settings.FollowRedirects || fallback.Settings.MaxRedirects != 5 {
		t.Errorf("a request with no protocolProfileBehavior lost its defaults: %+v", fallback.Settings)
	}
}
