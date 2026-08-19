// The token-exchange pre-request script, end to end.
//
// The conversion itself is unit-tested in internal/scripting; this is the case
// that was actually reported, run through the whole path — pre-request script,
// pm.sendRequest, the shared HTTP client — against a server that checks the wire
// the way an identity provider does.
//
// It exists because every layer in between had an opinion about the body. The
// script's definition was JSON-encoded by the sender, and the `header` key it
// set was dropped before that, so the request arrived as an application/json
// document at an endpoint that parses forms. The endpoint answered "the request
// body must contain the parameter 'grant_type'" — accurate, and pointing at the
// script rather than at the client.
package core

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// A token endpoint that behaves like one: it parses the form, and it refuses
// anything that is not a form, with the message the user saw.
func tokenEndpoint(t *testing.T) (*httptest.Server, func() (string, string)) {
	t.Helper()
	var (
		mu          sync.Mutex
		contentType string
		rawBody     string
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Read whole, then put it back: ParseForm consumes the body, and a
		// handler that records it afterwards records nothing.
		body, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(body))
		mu.Lock()
		contentType = r.Header.Get("Content-Type")
		rawBody = string(body)
		mu.Unlock()

		if err := r.ParseForm(); err != nil || r.PostForm.Get("grant_type") == "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = fmt.Fprint(w, `{"error":"invalid_request","error_description":"the request body must contain the following parameter: 'grant_type'"}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"access_token":"tok-%s","token_type":"Bearer","scope":%q}`,
			r.PostForm.Get("client_id"), r.PostForm.Get("scope"))
	}))
	return server, func() (string, string) {
		mu.Lock()
		defer mu.Unlock()
		return contentType, rawBody
	}
}

func TestPmSendRequestPostsAUrlencodedTokenRequest(t *testing.T) {
	tokenServer, seen := tokenEndpoint(t)
	defer tokenServer.Close()

	app, collectionID, itemID, closeServer := scriptProbeFixture(t)
	defer closeServer()

	// The reported script, in the shape it was written: singular `header`, and
	// a Postman body definition rather than a payload.
	pre := fmt.Sprintf(`
		var tokenGenerationRequest = {
			url: %q,
			method: 'POST',
			header: {
				'Content-Type': 'application/x-www-form-urlencoded'
			},
			body: {
				mode: 'urlencoded',
				urlencoded: [
					{key: "grant_type", value: "client_credentials"},
					{key: "client_id", value: "the-client"},
					{key: "client_secret", value: "s3cret"},
					{key: "scope", value: "read write"}
				]
			}
		}
		pm.sendRequest(tokenGenerationRequest, function (err, response) {
			if (err) { bru.setVar("tokenError", String(err)); return }
			bru.setVar("tokenStatus", String(response.status))
			bru.setVar("token", (response.data && response.data.access_token) || "")
			bru.setVar("tokenScope", (response.data && response.data.scope) || "")
		})
	`, tokenServer.URL)

	tests := `
		test("the token endpoint accepted the request", function () {
			expect(bru.getVar("tokenError") || null).to.equal(null)
			expect(bru.getVar("tokenStatus")).to.equal("200")
		})
		test("a token came back", function () {
			expect(bru.getVar("token")).to.equal("tok-the-client")
		})
		test("every field survived, not just the first", function () {
			expect(bru.getVar("tokenScope")).to.equal("read write")
		})
	`

	state := sendWithScripts(t, app, collectionID, itemID, pre, "", tests)
	for _, result := range testResultsFor(t, state, collectionID, itemID) {
		if !result.Passed {
			t.Errorf("%s: %s", result.Name, result.Message)
		}
	}

	contentType, body := seen()
	// Asserted on the wire as well as on the outcome: a server lenient enough
	// to parse a JSON body as a form would hide the whole defect.
	if !strings.HasPrefix(contentType, "application/x-www-form-urlencoded") {
		t.Errorf("Content-Type on the wire was %q", contentType)
	}
	if strings.HasPrefix(strings.TrimSpace(body), "{") {
		t.Errorf("the body definition was sent as a document rather than a form: %s", body)
	}
	if !strings.Contains(body, "grant_type=client_credentials") {
		t.Errorf("body on the wire: %s", body)
	}
}

// The same exchange with the credentials in a raw body, which is the other way
// people write it. Pinned because raw is the mode whose Content-Type Postman
// derives from the body's language rather than from the mode.
func TestPmSendRequestPostsARawBodyWithItsDeclaredContentType(t *testing.T) {
	tokenServer, seen := tokenEndpoint(t)
	defer tokenServer.Close()

	app, collectionID, itemID, closeServer := scriptProbeFixture(t)
	defer closeServer()

	pre := fmt.Sprintf(`
		pm.sendRequest({
			url: %q,
			method: 'POST',
			header: {'Content-Type': 'application/x-www-form-urlencoded'},
			body: {mode: 'raw', raw: 'grant_type=client_credentials&client_id=raw-client'}
		}, function (err, response) {
			bru.setVar("tokenStatus", err ? String(err) : String(response.status))
			bru.setVar("token", (response && response.data && response.data.access_token) || "")
		})
	`, tokenServer.URL)

	tests := `
		test("the raw body reached the endpoint intact", function () {
			expect(bru.getVar("tokenStatus")).to.equal("200")
			expect(bru.getVar("token")).to.equal("tok-raw-client")
		})
	`

	state := sendWithScripts(t, app, collectionID, itemID, pre, "", tests)
	for _, result := range testResultsFor(t, state, collectionID, itemID) {
		if !result.Passed {
			t.Errorf("%s: %s", result.Name, result.Message)
		}
	}

	// The script's own Content-Type wins over the one the mode implies. Had the
	// mode's text/plain been written instead, this endpoint would have refused
	// the request.
	if contentType, _ := seen(); !strings.HasPrefix(contentType, "application/x-www-form-urlencoded") {
		t.Errorf("the script's Content-Type was overwritten: %q", contentType)
	}
}

// Both dialects still record a timeline entry.
//
// This is the assertion `pm.sendRequest === bru.sendRequest` used to stand in
// for, and it is the stronger one. Reference identity proved the two names
// pointed at one function; it could not tell you that function still recorded
// anything. Splitting them into two registrations is exactly the change that
// could drop the recording from one side, so it is checked from both.
func TestBothSendRequestDialectsReachTheTimeline(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"ok":true}`)
	}))
	defer target.Close()

	app, collectionID, itemID, closeServer := scriptProbeFixture(t)
	defer closeServer()

	pre := fmt.Sprintf(`
		bru.sendRequest({url: %q, method: 'GET'}, function () {})
		pm.sendRequest({url: %q, method: 'GET'}, function () {})
	`, target.URL+"/from-bru", target.URL+"/from-pm")

	state := sendWithScripts(t, app, collectionID, itemID, pre, "", "")
	item, ok := findItemInState(state, collectionID, itemID)
	if !ok || item.Response == nil {
		t.Fatal("no response recorded")
	}

	seen := map[string]bool{}
	for _, entry := range item.Timeline {
		if entry.Source == "sendRequest" {
			seen[entry.URL] = true
		}
	}
	for _, want := range []string{target.URL + "/from-bru", target.URL + "/from-pm"} {
		if !seen[want] {
			t.Errorf("no timeline entry for %s — got %#v", want, item.Timeline)
		}
	}
}

// The two surfaces are no longer the same object, and a script can see that.
// Pinned so the split is a stated fact rather than something a reader has to
// infer from two registrations in different files.
func TestSendRequestSurfacesAreDistinctFunctions(t *testing.T) {
	app, collectionID, itemID, closeServer := scriptProbeFixture(t)
	defer closeServer()

	state := sendWithScripts(t, app, collectionID, itemID, "", "", `
		test("pm.sendRequest is not bru.sendRequest", function () {
			expect(pm.sendRequest === bru.sendRequest).to.equal(false)
		})
		test("both are callable", function () {
			expect(typeof pm.sendRequest).to.equal("function")
			expect(typeof bru.sendRequest).to.equal("function")
		})
	`)
	for _, result := range testResultsFor(t, state, collectionID, itemID) {
		if !result.Passed {
			t.Errorf("%s: %s", result.Name, result.Message)
		}
	}
}

// The Postman response accessors. `code` and `json()` are the pair every
// Postman script reaches for, and both were missing.
func TestSendRequestResponseCarriesThePostmanAccessors(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"access_token":"tok"}`)
	}))
	defer target.Close()

	app, collectionID, itemID, closeServer := scriptProbeFixture(t)
	defer closeServer()

	pre := fmt.Sprintf(`
		pm.sendRequest(%q, function (err, res) {
			bru.setVar("code", String(res.code))
			bru.setVar("token", res.json().access_token)
			bru.setVar("text", res.text())
			bru.setVar("reason", res.reason())
			bru.setVar("status", String(res.status))
		})
	`, target.URL)

	tests := `
		test("code is the number", function () {
			expect(bru.getVar("code")).to.equal("200")
		})
		test("json() parses the body", function () {
			expect(bru.getVar("token")).to.equal("tok")
		})
		test("text() is the raw body", function () {
			expect(bru.getVar("text")).to.contain("access_token")
		})
		test("reason() is the phrase", function () {
			expect(bru.getVar("reason")).to.equal("OK")
		})
		// Deliberately NOT Postman's reason phrase. Flipping it would break
		// every script that already compares it to a number.
		test("status stays the number it has always been", function () {
			expect(bru.getVar("status")).to.equal("200")
		})
	`

	state := sendWithScripts(t, app, collectionID, itemID, pre, "", tests)
	for _, result := range testResultsFor(t, state, collectionID, itemID) {
		if !result.Passed {
			t.Errorf("%s: %s", result.Name, result.Message)
		}
	}
}
