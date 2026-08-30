package importers

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

	"github.com/mutexdev/lite_api/internal/types"
)

func harFixture(t *testing.T) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join("..", "..", "docs", "qa", "import-fixtures", "session.har"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return string(content)
}

func harImportedItems(t *testing.T) (types.Collection, []string) {
	t.Helper()
	collection, warnings, err := ImportHAR(harFixture(t), "session")
	if err != nil {
		t.Fatalf("ImportHAR: %v", err)
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

	var target *types.RequestItem
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

// TestHARImportSeedsSafeRequestSettings guards the fail-open direction of every
// bool in RequestSettings, VerifyTLS above all.
//
// A HAR item built as a bare struct literal comes out with VerifyTLS false,
// and the executor turns that into InsecureSkipVerify — so a recording
// imported for convenience would replay against a real host with certificate
// checking off, and nothing in the UI would say so. The zero values of
// FollowRedirects, EncodeURL and StoreCookies are wrong in the same quiet way,
// and a zero TimeoutMs means the request inherits no timeout of its own.
//
// Asserted for EVERY imported item rather than the first: the bug was in the
// per-item constructor, so a fix that seeded one path and not another would
// still ship the hole.
func TestHARImportSeedsSafeRequestSettings(t *testing.T) {
	collection, _ := harImportedItems(t)

	defaults := types.NewRequestItem("reference", "http", 1).Settings
	if !defaults.VerifyTLS {
		t.Fatal("NewRequestItem no longer defaults VerifyTLS to true; this test's premise is gone")
	}

	for _, item := range collection.Items {
		if !item.Settings.VerifyTLS {
			t.Errorf("%s: VerifyTLS is false — imported requests would run with certificate verification disabled", item.Name)
		}
		if item.Settings.TimeoutMs != defaults.TimeoutMs {
			t.Errorf("%s: TimeoutMs = %d, want the %d default", item.Name, item.Settings.TimeoutMs, defaults.TimeoutMs)
		}
		if !item.Settings.FollowRedirects {
			t.Errorf("%s: FollowRedirects is false, want the default true", item.Name)
		}
		if item.Settings.MaxRedirects != defaults.MaxRedirects {
			t.Errorf("%s: MaxRedirects = %d, want the %d default", item.Name, item.Settings.MaxRedirects, defaults.MaxRedirects)
		}
		if !item.Settings.EncodeURL {
			t.Errorf("%s: EncodeURL is false, want the default true", item.Name)
		}
		if !item.Settings.StoreCookies {
			t.Errorf("%s: StoreCookies is false, want the default true", item.Name)
		}
	}
}

// TestHARImportKeepsRecordedFieldsOverDefaults. Seeding from NewRequestItem
// brings placeholder values with it ("{{host}}/get", GET, an empty body); the
// overlay has to win, or the safety fix would quietly replace the recording.
func TestHARImportKeepsRecordedFieldsOverDefaults(t *testing.T) {
	collection, _ := harImportedItems(t)

	for _, item := range collection.Items {
		if strings.Contains(item.URL, "{{host}}") {
			t.Errorf("%s: URL %q is the constructor placeholder, not the recorded URL", item.Name, item.URL)
		}
		if item.Type != "http" {
			t.Errorf("%s: Type = %q, want http", item.Name, item.Type)
		}
		if item.ID == "" {
			t.Errorf("%s: empty ID", item.Name)
		}
	}

	var post types.RequestItem
	for _, item := range collection.Items {
		if item.Method == "POST" {
			post = item
			break
		}
	}
	if post.ID == "" {
		t.Fatal("fixture has no POST; the method overlay is untested")
	}
	if post.Body.Mode == "none" {
		t.Errorf("POST %s: body mode is none, the recorded body was lost", post.URL)
	}
}

func TestHARImportRejectsBadInput(t *testing.T) {
	for _, tc := range []struct{ name, content string }{
		{"not json", "this is not a HAR"},
		{"no entries", `{"log":{"version":"1.2","entries":[]}}`},
		{"entries without urls", `{"log":{"version":"1.2","entries":[{"request":{"method":"GET","url":""}}]}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := ImportHAR(tc.content, "bad"); err == nil {
				t.Error("expected an error")
			}
		})
	}
}
