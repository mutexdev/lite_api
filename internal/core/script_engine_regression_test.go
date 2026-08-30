// End-to-end regressions for the script-engine defects, asserted at the two
// places the user actually observes them: the bytes that reach the server, and
// the test rows on the response.
package core

import (
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/http/httptest"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// capturedRequest is one request exactly as the server saw it.
type capturedRequest struct {
	Method  string
	Headers map[string]string
	Body    string
}

var multipartBoundaryExpr = regexp.MustCompile(`[0-9a-f]{40,}`)

// normalize removes the one thing that legitimately differs between two
// identical multipart sends: the random boundary.
func (captured capturedRequest) normalize() capturedRequest {
	out := capturedRequest{Method: captured.Method, Headers: map[string]string{}}
	out.Body = multipartBoundaryExpr.ReplaceAllString(captured.Body, "BOUNDARY")
	for name, value := range captured.Headers {
		out.Headers[name] = multipartBoundaryExpr.ReplaceAllString(value, "BOUNDARY")
	}
	return out
}

func (captured capturedRequest) String() string {
	names := make([]string, 0, len(captured.Headers))
	for name := range captured.Headers {
		names = append(names, name)
	}
	sort.Strings(names)
	lines := []string{captured.Method}
	for _, name := range names {
		lines = append(lines, name+": "+captured.Headers[name])
	}
	return strings.Join(lines, "\n") + "\n\n" + captured.Body
}

// captureFixture stands up a server that records the wire form of whatever
// reaches it, and a request pointed at it.
func captureFixture(t *testing.T) (app *App, collectionID, itemID string, captured *capturedRequest, closeServer func()) {
	t.Helper()
	captured = &capturedRequest{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		headers := map[string]string{}
		for name, values := range r.Header {
			// Connection-management headers say nothing about the request the
			// app built, and Go may or may not reuse a connection between the
			// two sends.
			switch strings.ToLower(name) {
			case "accept-encoding", "connection", "user-agent":
				continue
			}
			headers[name] = strings.Join(values, ", ")
		}
		headers["Content-Length"] = fmt.Sprint(r.ContentLength)
		*captured = capturedRequest{Method: r.Method, Headers: headers, Body: string(body)}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"ok":true}`)
	}))

	app = newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	collection := state.Workspaces[0].Collections[0]
	created, err := app.CreateRequest(collection.ID, "http", "wire capture probe")
	if err != nil {
		t.Fatalf("CreateRequest: %v", err)
	}
	for _, c := range created.Workspaces[0].Collections {
		for _, item := range c.Items {
			if item.Name == "wire capture probe" {
				itemID = item.ID
			}
		}
	}
	if itemID == "" {
		t.Fatal("could not find the probe request")
	}
	url := server.URL
	if _, err := app.UpdateRequest(collection.ID, itemID, RequestPatch{URL: &url}); err != nil {
		t.Fatalf("UpdateRequest: %v", err)
	}
	return app, collection.ID, itemID, captured, server.Close
}

// FIX 3, at the wire. A pre-request script that touches nothing must produce
// byte-identical requests to having no script at all — method, headers,
// Content-Type, Content-Length and body — in every body mode.
//
// Live-verified before the fix: a formUrlEncoded body went out raw and
// unencoded, and a GET with no body picked up a Content-Type of text/plain, both
// triggered by the mere presence of `bru.setVar("x", 1)`.
func TestPreRequestScriptDoesNotChangeTheBytesOnTheWire(t *testing.T) {
	cases := map[string]struct {
		method string
		body   RequestBody
	}{
		"none": {"GET", RequestBody{Mode: "none"}},
		"raw json": {"POST", RequestBody{
			Mode: "json",
			JSON: `{"name":"value with spaces & symbols"}`,
		}},
		"formUrlEncoded": {"POST", RequestBody{
			Mode: "formUrlEncoded",
			FormURLEncoded: []KeyValue{
				{Name: "field1", Value: "value with spaces & symbols", Enabled: true},
				{Name: "field2", Value: "b+c=d", Enabled: true},
			},
		}},
		"multipart": {"POST", RequestBody{
			Mode: "multipartForm",
			Multipart: []FormPart{
				{Name: "part1", Value: "value with spaces & symbols", Enabled: true},
				{Name: "part2", Value: "second", Enabled: true},
			},
		}},
		"graphql": {"POST", RequestBody{
			Mode:             "graphql",
			GraphQLQuery:     "query { thing { id } }",
			GraphQLVariables: `{"id":1}`,
		}},
	}

	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			app, collectionID, itemID, captured, closeServer := captureFixture(t)
			defer closeServer()

			body := testCase.body
			method := testCase.method
			noScript := ""
			if _, err := app.UpdateRequest(collectionID, itemID, RequestPatch{
				Method: &method, Body: &body, PreScript: &noScript,
			}); err != nil {
				t.Fatalf("UpdateRequest: %v", err)
			}
			if _, err := app.SendRequest(collectionID, itemID, ""); err != nil {
				t.Fatalf("SendRequest: %v", err)
			}
			baseline := captured.normalize()

			script := `bru.setVar("x", 1)`
			if _, err := app.UpdateRequest(collectionID, itemID, RequestPatch{PreScript: &script}); err != nil {
				t.Fatalf("UpdateRequest: %v", err)
			}
			if _, err := app.SendRequest(collectionID, itemID, ""); err != nil {
				t.Fatalf("SendRequest: %v", err)
			}
			withScript := captured.normalize()

			if !reflect.DeepEqual(baseline, withScript) {
				t.Fatalf("a no-op pre-request script changed the request on the wire.\n--- without script ---\n%s\n--- with script ---\n%s",
					baseline.String(), withScript.String())
			}
		})
	}
}

// The two specific corruptions the audit saw, stated on their own so a
// regression names itself rather than showing a diff.
func TestFormUrlEncodedBodyStaysEncodedWhenAScriptIsPresent(t *testing.T) {
	app, collectionID, itemID, captured, closeServer := captureFixture(t)
	defer closeServer()

	method := "POST"
	script := `bru.setVar("x", 1)`
	body := RequestBody{
		Mode: "formUrlEncoded",
		FormURLEncoded: []KeyValue{
			{Name: "field1", Value: "value with spaces & symbols", Enabled: true},
		},
	}
	if _, err := app.UpdateRequest(collectionID, itemID, RequestPatch{
		Method: &method, Body: &body, PreScript: &script,
	}); err != nil {
		t.Fatalf("UpdateRequest: %v", err)
	}
	if _, err := app.SendRequest(collectionID, itemID, ""); err != nil {
		t.Fatalf("SendRequest: %v", err)
	}
	if captured.Body != "field1=value+with+spaces+%26+symbols" {
		t.Fatalf("body = %q, want the form-encoded form", captured.Body)
	}
	mediaType, _, _ := mime.ParseMediaType(captured.Headers["Content-Type"])
	if mediaType != "application/x-www-form-urlencoded" {
		t.Fatalf("Content-Type = %q", captured.Headers["Content-Type"])
	}
}

func TestBodylessGetGainsNoContentTypeWhenAScriptIsPresent(t *testing.T) {
	app, collectionID, itemID, captured, closeServer := captureFixture(t)
	defer closeServer()

	method := "GET"
	script := `bru.setVar("x", 1)`
	body := RequestBody{Mode: "none"}
	if _, err := app.UpdateRequest(collectionID, itemID, RequestPatch{
		Method: &method, Body: &body, PreScript: &script,
	}); err != nil {
		t.Fatalf("UpdateRequest: %v", err)
	}
	if _, err := app.SendRequest(collectionID, itemID, ""); err != nil {
		t.Fatalf("SendRequest: %v", err)
	}
	if got := captured.Headers["Content-Type"]; got != "" {
		t.Fatalf("a bodyless GET gained Content-Type %q because a script existed", got)
	}
	if captured.Body != "" {
		t.Fatalf("a bodyless GET gained a body %q", captured.Body)
	}
}

// FIX 1, end to end. Three pm.tests in the post-response script must be three
// rows on the response.
func TestPostResponseScriptTestsReachTheResponse(t *testing.T) {
	app, collectionID, itemID, closeServer := scriptProbeFixture(t)
	defer closeServer()

	state := sendWithScripts(t, app, collectionID, itemID, "", `
pm.test("one", function () { pm.expect(1).to.equal(1) })
pm.test("two fails", function () { pm.expect(1).to.equal(2) })
pm.test("three", function () { pm.expect(3).to.equal(3) })
`, "")

	results := testResultsFor(t, state, collectionID, itemID)
	if len(results) != 3 {
		t.Fatalf("got %d rows, want 3: %+v", len(results), results)
	}
	if !results[0].Passed || results[1].Passed || !results[2].Passed {
		t.Fatalf("wrong outcomes: %+v", results)
	}
}

// FIX 2, end to end. A failing assertion in the PRE-request script must record a
// failed row AND leave the request sent — before this the request was replaced
// by a script-error response and never reached the server.
func TestPreRequestTestFailureStillSendsTheRequest(t *testing.T) {
	app, collectionID, itemID, captured, closeServer := captureFixture(t)
	defer closeServer()

	script := `pm.test("pre fails", function () { pm.expect(1).to.equal(2) })`
	if _, err := app.UpdateRequest(collectionID, itemID, RequestPatch{PreScript: &script}); err != nil {
		t.Fatalf("UpdateRequest: %v", err)
	}
	state, err := app.SendRequest(collectionID, itemID, "")
	if err != nil {
		t.Fatalf("SendRequest: %v", err)
	}
	if captured.Method == "" {
		t.Fatal("the request was never sent: a failed pre-request assertion cancelled it")
	}
	item, ok := findItemInState(state, collectionID, itemID)
	if !ok || item.Response == nil {
		t.Fatal("no response recorded")
	}
	if item.Response.Status != http.StatusOK {
		t.Fatalf("status = %d, want 200 (error: %q)", item.Response.Status, item.Response.Error)
	}
	if len(item.Response.TestResults) != 1 {
		t.Fatalf("got %d rows, want the one failed pre-request test: %+v", len(item.Response.TestResults), item.Response.TestResults)
	}
	if item.Response.TestResults[0].Passed || item.Response.TestResults[0].Name != "pre fails" {
		t.Fatalf("row = %+v, want the named test recorded as failed", item.Response.TestResults[0])
	}
}

// Pre-request rows come first, then post-response, then tests — the order the
// scripts ran in.
func TestTestRowsAreOrderedByPhase(t *testing.T) {
	app, collectionID, itemID, closeServer := scriptProbeFixture(t)
	defer closeServer()

	state := sendWithScripts(t, app, collectionID, itemID,
		`pm.test("from pre", function () {})`,
		`pm.test("from post", function () {})`,
		`test("from tests", function () {})`)

	results := testResultsFor(t, state, collectionID, itemID)
	got := make([]string, 0, len(results))
	for _, result := range results {
		got = append(got, result.Name)
	}
	want := []string{"from pre", "from post", "from tests"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("rows = %v, want %v", got, want)
	}
}

// FIX 2's other half at the send path: an uncaught throw in the pre-request
// script must still stop the request.
func TestPreRequestThrowStillCancelsTheSend(t *testing.T) {
	app, collectionID, itemID, captured, closeServer := captureFixture(t)
	defer closeServer()

	script := `throw new Error("the pre-request script is broken")`
	if _, err := app.UpdateRequest(collectionID, itemID, RequestPatch{PreScript: &script}); err != nil {
		t.Fatalf("UpdateRequest: %v", err)
	}
	state, err := app.SendRequest(collectionID, itemID, "")
	if err != nil {
		t.Fatalf("SendRequest: %v", err)
	}
	if captured.Method != "" {
		t.Fatal("a broken pre-request script still sent the request")
	}
	item, ok := findItemInState(state, collectionID, itemID)
	if !ok || item.Response == nil {
		t.Fatal("no response recorded")
	}
	if !strings.Contains(item.Response.Error, "the pre-request script is broken") {
		t.Fatalf("response error = %q, want the thrown message", item.Response.Error)
	}
}
