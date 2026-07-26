package main

// US-051 — tests for HAR import.
//
// The fixture at docs/qa/import-fixtures/session.har is built so that a naive
// importer fails visibly rather than plausibly. It contains an HTTP/2
// pseudo-header, a repeated query key, a recorded content-length, one exact
// duplicate and two requests that differ ONLY in their body.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func harFixture(t *testing.T) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join("docs", "qa", "import-fixtures", "session.har"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return string(content)
}

func harImportedItems(t *testing.T) (Collection, []string) {
	t.Helper()
	collection, warnings, err := importHAR(harFixture(t), "session")
	if err != nil {
		t.Fatalf("importHAR: %v", err)
	}
	return collection, warnings
}

// TestHARImportDropsOnlyExactDuplicates. The fixture has 5 entries: one exact
// repeat, and two POSTs to the same URL differing only in body. Deduping on
// method+URL — the obvious implementation — would collapse those two POSTs and
// silently lose a request, which is the whole reason someone recorded the
// session.
func TestHARImportDropsOnlyExactDuplicates(t *testing.T) {
	collection, _ := harImportedItems(t)

	// 5 entries, exactly one of which is a byte-identical repeat: 4 survive.
	// The two POSTs to /v1/users differ only in body and must both remain.
	if len(collection.Items) != 4 {
		var names []string
		for _, item := range collection.Items {
			names = append(names, item.Name)
		}
		t.Fatalf("got %d items %v, want 4", len(collection.Items), names)
	}

	bodies := map[string]bool{}
	for _, item := range collection.Items {
		if item.Method == "POST" && strings.HasSuffix(item.URL, "/v1/users") {
			bodies[item.Body.JSON] = true
		}
	}
	if len(bodies) != 2 {
		t.Errorf("the two POSTs differing only in body collapsed to %d: %v", len(bodies), bodies)
	}
}

// TestHARImportWarnsAboutCredentials. Importing them is right — stripping would
// make every request 401 for no visible reason — but a collection is written to
// disk and this app can commit one to git, so the user has to be told which
// headers to review.
func TestHARImportWarnsAboutCredentials(t *testing.T) {
	_, warnings := harImportedItems(t)

	joined := strings.Join(warnings, "\n")
	if !strings.Contains(joined, "authorization") || !strings.Contains(joined, "cookie") {
		t.Errorf("the credential warning does not name the headers: %q", joined)
	}
	if !strings.Contains(joined, "duplicate") {
		t.Errorf("the dropped duplicates were not reported: %q", joined)
	}
}

// TestHARImportKeepsCredentialHeaders. The warning is the mitigation; silently
// dropping the header would leave a collection whose every request fails.
func TestHARImportKeepsCredentialHeaders(t *testing.T) {
	collection, _ := harImportedItems(t)

	found := false
	for _, item := range collection.Items {
		for _, header := range item.Headers {
			if strings.EqualFold(header.Name, "authorization") {
				found = true
				if header.Value != "Bearer qa-fixture-token" {
					t.Errorf("authorization value = %q", header.Value)
				}
			}
		}
	}
	if !found {
		t.Error("the authorization header was stripped; the imported request would 401 with no visible cause")
	}
}

// TestHARImportStripsRecordingArtefacts. HTTP/2 pseudo-headers are not valid to
// send, and a recorded content-length that no longer matches an edited body
// makes the server read a truncated payload — a failure that looks like a
// server bug.
func TestHARImportStripsRecordingArtefacts(t *testing.T) {
	collection, _ := harImportedItems(t)

	for _, item := range collection.Items {
		for _, header := range item.Headers {
			lower := strings.ToLower(header.Name)
			if strings.HasPrefix(header.Name, ":") {
				t.Errorf("%s kept the HTTP/2 pseudo-header %q", item.Name, header.Name)
			}
			if lower == "content-length" || lower == "host" {
				t.Errorf("%s kept the recording artefact %q", item.Name, header.Name)
			}
		}
	}
}

// TestHARImportKeepsRepeatedQueryKeys. url.ParseQuery returns a map, which
// loses both order and repeats — and repeated keys are ordinary in recorded
// traffic. Losing one changes what the request asks for.
func TestHARImportKeepsRepeatedQueryKeys(t *testing.T) {
	collection, _ := harImportedItems(t)

	var target *RequestItem
	for i := range collection.Items {
		if collection.Items[i].Method == "GET" {
			target = &collection.Items[i]
		}
	}
	if target == nil {
		t.Fatal("the GET request was not imported")
	}

	if strings.Contains(target.URL, "?") {
		t.Errorf("the query string was frozen into the URL rather than split into params: %q", target.URL)
	}

	var sorts []string
	for _, param := range target.Params {
		if param.Name == "sort" {
			sorts = append(sorts, param.Value)
		}
	}
	if len(sorts) != 2 {
		t.Fatalf("got %d sort params %v, want both", len(sorts), sorts)
	}
	if sorts[0] != "name" || sorts[1] != "age" {
		t.Errorf("repeated query keys lost their order: %v", sorts)
	}
}

func TestHARImportMapsBodyModes(t *testing.T) {
	collection, _ := harImportedItems(t)

	modes := map[string]string{}
	for _, item := range collection.Items {
		modes[item.Name] = item.Body.Mode
	}

	var jsonSeen, formSeen bool
	for _, item := range collection.Items {
		switch item.Body.Mode {
		case "json":
			jsonSeen = true
			if !strings.Contains(item.Body.JSON, "\"name\"") {
				t.Errorf("json body was not carried: %q", item.Body.JSON)
			}
		case "formUrlEncoded":
			formSeen = true
			values := map[string]string{}
			for _, field := range item.Body.FormURLEncoded {
				values[field.Name] = field.Value
			}
			if values["username"] != "ada" || values["password"] != "hunter2" {
				t.Errorf("form fields were not carried: %v", values)
			}
		case "none":
			if item.Method != "GET" {
				t.Errorf("%s has no body but is a %s", item.Name, item.Method)
			}
		}
	}
	if !jsonSeen {
		t.Errorf("no JSON body was detected: %v", modes)
	}
	if !formSeen {
		t.Errorf("no form body was detected: %v", modes)
	}
}

// TestHARImportNamesRequestsDistinctly. A HAR import is typically dozens of
// requests; "GET /" thirty times is an unusable list.
func TestHARImportNamesRequestsDistinctly(t *testing.T) {
	collection, _ := harImportedItems(t)
	for _, item := range collection.Items {
		if !strings.HasPrefix(item.Name, item.Method) {
			t.Errorf("name %q does not lead with the method", item.Name)
		}
		if strings.Contains(item.Name, "http") {
			t.Errorf("name %q is the whole URL rather than a scannable label", item.Name)
		}
	}
}

func TestHARImportRejectsBadInput(t *testing.T) {
	for _, tc := range []struct{ name, content string }{
		{"not json", "this is not a HAR"},
		{"no entries", `{"log":{"version":"1.2","entries":[]}}`},
		{"entries without urls", `{"log":{"version":"1.2","entries":[{"request":{"method":"GET","url":""}}]}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := importHAR(tc.content, "bad"); err == nil {
				t.Error("expected an error")
			}
		})
	}
}

// TestHARIsDetectedByShapeNotOnlyExtension. Browsers routinely save a HAR as
// plain .json from the network panel; one that reached the generic fallbacks
// would be reported as "ambiguous" rather than imported.
func TestHARIsDetectedByShapeNotOnlyExtension(t *testing.T) {
	content := harFixture(t)

	for _, name := range []string{"session.har", "network-log.json"} {
		t.Run(name, func(t *testing.T) {
			kind, collection, warnings, err := detectCollectionImport(content, name, "")
			if err != nil {
				t.Fatalf("detectCollectionImport: %v", err)
			}
			if kind != "har" {
				t.Errorf("detected kind = %q, want har", kind)
			}
			if len(collection.Items) != 4 {
				t.Errorf("got %d items, want 4", len(collection.Items))
			}
			if len(warnings) == 0 {
				t.Error("detection dropped the importer's warnings; the credential notice would never reach the user")
			}
		})
	}
}

// TestHARManualOverrideCarriesWarnings. The override path is a separate branch
// from detection, and warnings are easy to drop there — the import would work
// and the credential notice would silently vanish.
func TestHARManualOverrideCarriesWarnings(t *testing.T) {
	kind, collection, warnings, err := detectCollectionImport(harFixture(t), "anything.txt", "har")
	if err != nil {
		t.Fatalf("detectCollectionImport: %v", err)
	}
	if kind != "har" {
		t.Errorf("kind = %q, want har", kind)
	}
	if len(collection.Items) != 4 {
		t.Errorf("got %d items, want 4", len(collection.Items))
	}
	if len(warnings) == 0 {
		t.Error("the manual override path dropped the warnings")
	}
}
