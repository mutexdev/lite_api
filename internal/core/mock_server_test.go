package core

// US-072 — tests for the per-collection mock server.
//
// Two assertions carry more weight than the rest. The listener must be
// LOOPBACK-ONLY, because a mock replays recorded traffic that routinely
// contains credentials and binding all interfaces would publish it to the
// network. And a named selection that does not exist must be an ERROR rather
// than a quiet fallback to the first example, because someone asking for the
// 404 case and being handed the 200 sees their test pass for the wrong reason.

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/mutexdev/lite_api/internal/localserver"
)

func mockCollectionFixture() Collection {
	return Collection{
		ID:   "mock-collection",
		Name: "Mock fixture",
		Items: []RequestItem{
			{
				ID:     "list",
				Name:   "List users",
				Type:   "http",
				Method: "GET",
				URL:    "{{baseUrl}}/v1/users",
				Examples: []ResponseExample{
					{
						ID:      "ok",
						Name:    "success",
						Request: ResponseExampleRequest{Method: "GET", URL: "{{baseUrl}}/v1/users"},
						Response: ResponseExamplePayload{
							Status:     200,
							StatusText: "OK",
							Headers:    []KeyValue{{Name: "Content-Type", Value: "application/json", Enabled: true}},
							Body:       `{"users":["ada"]}`,
						},
					},
					{
						ID:      "empty",
						Name:    "no users",
						Request: ResponseExampleRequest{Method: "GET", URL: "{{baseUrl}}/v1/users"},
						Response: ResponseExamplePayload{
							Status:  404,
							Headers: []KeyValue{{Name: "Content-Type", Value: "application/json", Enabled: true}},
							Body:    `{"error":"none"}`,
						},
					},
				},
			},
			{
				ID:     "create",
				Name:   "Create user",
				Type:   "http",
				Method: "POST",
				URL:    "https://api.example.test/v1/users",
				Examples: []ResponseExample{
					{
						ID:       "created",
						Name:     "created",
						Request:  ResponseExampleRequest{Method: "POST", URL: "https://api.example.test/v1/users"},
						Response: ResponseExamplePayload{Status: 201, Body: `{"id":1}`},
					},
				},
			},
			{
				// No examples: must contribute no route rather than an invented one.
				ID: "unrecorded", Name: "Unrecorded", Type: "http", Method: "DELETE", URL: "{{baseUrl}}/v1/users/1",
			},
			{
				// Not an HTTP protocol: skipped.
				ID: "socket", Name: "Socket", Type: "websocket", URL: "{{baseUrl}}/v1/ws",
			},
		},
	}
}

func startFixtureMock(t *testing.T) *localserver.MockServer {
	t.Helper()
	mock, err := localserver.StartMock(mockCollectionFixture(), 0, nil)
	if err != nil {
		t.Fatalf("localserver.StartMock: %v", err)
	}
	t.Cleanup(func() { _ = mock.Stop() })
	return mock
}

// TestMockServerBindsLoopbackOnly is the assertion with the highest stakes in
// this file. A mock replays whatever the user recorded — tokens, internal
// hostnames, customer data — and binding all interfaces publishes it to every
// machine on the network.
func TestMockServerBindsLoopbackOnly(t *testing.T) {
	mock := startFixtureMock(t)

	addr, ok := mock.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("listener address is not TCP: %v", mock.Addr())
	}
	if !addr.IP.IsLoopback() {
		t.Fatalf("mock bound %s, which is reachable from the network; it must be loopback only", addr.IP)
	}
	if mock.Status().URL != fmt.Sprintf("http://127.0.0.1:%d", mock.Status().Port) {
		t.Errorf("advertised URL %q is not the loopback address", mock.Status().URL)
	}
}

func TestMockServerAnswersFromASavedExample(t *testing.T) {
	mock := startFixtureMock(t)

	response, err := http.Get(mock.Status().URL + "/v1/users")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = response.Body.Close() }()

	if response.StatusCode != 200 {
		t.Errorf("status = %d, want 200", response.StatusCode)
	}
	if got := response.Header.Get("Content-Type"); got != "application/json" {
		t.Errorf("content type = %q", got)
	}
	body, _ := io.ReadAll(response.Body)
	if string(body) != `{"users":["ada"]}` {
		t.Errorf("body = %q", body)
	}
}

// TestMockServerSelectsByName covers the x-mock-response-name header, and the
// case that matters more: a name that does not exist.
func TestMockServerSelectsByName(t *testing.T) {
	mock := startFixtureMock(t)

	request, _ := http.NewRequest(http.MethodGet, mock.Status().URL+"/v1/users", nil)
	request.Header.Set(localserver.MockSelectionHeader, "no users")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = response.Body.Close() }()

	if response.StatusCode != 404 {
		t.Errorf("status = %d, want the named example's 404", response.StatusCode)
	}
	body, _ := io.ReadAll(response.Body)
	if !strings.Contains(string(body), `"error":"none"`) {
		t.Errorf("body = %q, want the named example's body", body)
	}
}

// A missing name must NOT silently fall back to the first example: someone
// asking for the 404 case and being handed the 200 sees a test pass for the
// wrong reason.
func TestMockServerRejectsAnUnknownExampleName(t *testing.T) {
	mock := startFixtureMock(t)

	request, _ := http.NewRequest(http.MethodGet, mock.Status().URL+"/v1/users", nil)
	request.Header.Set(localserver.MockSelectionHeader, "does not exist")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = response.Body.Close() }()

	if response.StatusCode == 200 {
		t.Fatal("an unknown example name silently returned the first example")
	}
	if response.StatusCode != 400 {
		t.Errorf("status = %d, want 400", response.StatusCode)
	}
	body, _ := io.ReadAll(response.Body)
	// The available names have to be in the message; without them the user
	// cannot tell whether they mistyped or the example is gone.
	if !strings.Contains(string(body), "success") || !strings.Contains(string(body), "no users") {
		t.Errorf("the error does not list the available names: %q", body)
	}
}

// TestMockServerDoesNotInventResponses. A route with no recorded example must
// 404 rather than fabricate a 200 — an invented success is worse than a miss
// the user can act on.
func TestMockServerDoesNotInventResponses(t *testing.T) {
	mock := startFixtureMock(t)

	for _, target := range []struct {
		method, path string
	}{
		{http.MethodDelete, "/v1/users/1"}, // exists as a request, has no example
		{http.MethodGet, "/v1/ws"},         // websocket, skipped entirely
		{http.MethodGet, "/nothing/here"},  // not in the collection at all
		{http.MethodPut, "/v1/users"},      // right path, wrong method
	} {
		request, _ := http.NewRequest(target.method, mock.Status().URL+target.path, nil)
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatalf("%s %s: %v", target.method, target.path, err)
		}
		body, _ := io.ReadAll(response.Body)
		_ = response.Body.Close()

		if response.StatusCode != 404 {
			t.Errorf("%s %s returned %d, want 404", target.method, target.path, response.StatusCode)
		}
		// The 404 lists the route count, because a bare one is
		// indistinguishable from a typo in the request.
		if !strings.Contains(string(body), "routes") {
			t.Errorf("%s %s: the 404 gives the user nothing to act on: %q", target.method, target.path, body)
		}
	}
}

// TestMockServerDropsRecordedContentLength. net/http recomputes it from what is
// actually written; a stale recorded value makes the client read a truncated
// response.
func TestMockServerDropsRecordedContentLength(t *testing.T) {
	collection := mockCollectionFixture()
	collection.Items[0].Examples[0].Response.Headers = append(
		collection.Items[0].Examples[0].Response.Headers,
		KeyValue{Name: "Content-Length", Value: "99999", Enabled: true},
	)
	mock, err := localserver.StartMock(collection, 0, nil)
	if err != nil {
		t.Fatalf("localserver.StartMock: %v", err)
	}
	defer func() { _ = mock.Stop() }()

	response, err := http.Get(mock.Status().URL + "/v1/users")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("the recorded Content-Length truncated the read: %v", err)
	}
	if string(body) != `{"users":["ada"]}` {
		t.Errorf("body = %q", body)
	}
}

// TestMockServerDisabledHeadersAreNotSent. A header the user turned off in the
// example editor must not appear.
func TestMockServerDisabledHeadersAreNotSent(t *testing.T) {
	collection := mockCollectionFixture()
	collection.Items[0].Examples[0].Response.Headers = append(
		collection.Items[0].Examples[0].Response.Headers,
		KeyValue{Name: "X-Disabled", Value: "should not appear", Enabled: false},
	)
	mock, err := localserver.StartMock(collection, 0, nil)
	if err != nil {
		t.Fatalf("localserver.StartMock: %v", err)
	}
	defer func() { _ = mock.Stop() }()

	response, err := http.Get(mock.Status().URL + "/v1/users")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.Header.Get("X-Disabled") != "" {
		t.Error("a disabled header reached the wire")
	}
}

// TestMockServerUpdateKeepsThePort. Saving an example must take effect without
// changing the address every open client is pointed at.
func TestMockServerUpdateKeepsThePort(t *testing.T) {
	mock := startFixtureMock(t)
	port := mock.Status().Port

	updated := mockCollectionFixture()
	updated.Items[0].Examples[0].Response.Body = `{"users":["grace"]}`
	mock.Update(updated)

	if mock.Status().Port != port {
		t.Errorf("the port changed from %d to %d on a routing update", port, mock.Status().Port)
	}
	response, err := http.Get(mock.Status().URL + "/v1/users")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = response.Body.Close() }()
	body, _ := io.ReadAll(response.Body)
	if string(body) != `{"users":["grace"]}` {
		t.Errorf("body = %q, want the updated example", body)
	}
}

// TestMockServerLifecycleThroughTheBindings covers start, restart, refresh and
// stop as the UI drives them.
func TestMockServerLifecycleThroughTheBindings(t *testing.T) {
	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	collectionID := state.Workspaces[0].Collections[0].ID

	idle := app.MockServerStatusFor(collectionID)
	if idle.Running {
		t.Error("a fresh collection reports a running mock")
	}

	status, err := app.StartMockServer(collectionID, 0)
	if err != nil {
		t.Fatalf("StartMockServer: %v", err)
	}
	if !status.Running || status.Port == 0 {
		t.Fatalf("status = %+v", status)
	}
	firstPort := status.Port

	// Starting again must not leave the first listener bound.
	restarted, err := app.StartMockServer(collectionID, 0)
	if err != nil {
		t.Fatalf("restart: %v", err)
	}
	if restarted.Port == 0 {
		t.Error("the restarted mock has no port")
	}
	if _, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", firstPort), localserver.MockShutdownGrace); err == nil && firstPort != restarted.Port {
		t.Error("the original listener is still bound after a restart")
	}

	if _, err := app.RefreshMockServer(collectionID); err != nil {
		t.Fatalf("RefreshMockServer: %v", err)
	}

	if _, err := app.StopMockServer(collectionID); err != nil {
		t.Fatalf("StopMockServer: %v", err)
	}
	if app.MockServerStatusFor(collectionID).Running {
		t.Error("the mock still reports running after stop")
	}
	// Stopping twice is the UI's idempotent stop button, not a failure.
	if _, err := app.StopMockServer(collectionID); err != nil {
		t.Errorf("stopping an already-stopped mock errored: %v", err)
	}
	// Refreshing a stopped mock is a no-op rather than an error.
	if _, err := app.RefreshMockServer(collectionID); err != nil {
		t.Errorf("refreshing a stopped mock errored: %v", err)
	}
}

func TestStartMockServerRejectsABadPort(t *testing.T) {
	if _, err := localserver.StartMock(mockCollectionFixture(), -1, nil); err == nil {
		t.Error("a negative port should be rejected")
	}
	if _, err := localserver.StartMock(mockCollectionFixture(), 70000, nil); err == nil {
		t.Error("a port above the range should be rejected")
	}
}

func TestStartMockServerRejectsAnUnknownCollection(t *testing.T) {
	app := newAppForTest(t)
	if _, err := app.StartMockServer("no-such-collection", 0); err == nil {
		t.Error("an unknown collection should be an error")
	}
}

// TestSelfTestSendRequestAgainstOwnMock is the self-test US-073 asks for by
// name: LiteAPI sends a request through its own request path to its own mock
// server and gets the saved example back.
//
// This is the assertion that proves the feature end to end. Every other test
// here drives the mock with net/http, which shares nothing with the app's own
// send path — its transport cache, header handling, interpolation and response
// recording are all untested by those. A mock that answers curl but not the app
// is a mock nobody in this app can use.
func TestSelfTestSendRequestAgainstOwnMock(t *testing.T) {
	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	collectionID := state.Workspaces[0].Collections[0].ID

	// A request with a saved example, which is what the mock will serve.
	created, err := app.CreateRequest(collectionID, "http", "self test target")
	if err != nil {
		t.Fatalf("CreateRequest: %v", err)
	}
	var itemID string
	for _, collection := range created.Workspaces[0].Collections {
		for _, item := range collection.Items {
			if item.Name == "self test target" {
				itemID = item.ID
			}
		}
	}
	targetURL := "{{mockBase}}/self-test"
	if _, err := app.UpdateRequest(collectionID, itemID, RequestPatch{URL: &targetURL}); err != nil {
		t.Fatalf("UpdateRequest: %v", err)
	}

	// Created through the real API rather than assigned to the struct, so the
	// example is stored exactly as the app stores one.
	withExample, err := app.CreateResponseExample(collectionID, itemID, "self test", "")
	if err != nil {
		t.Fatalf("CreateResponseExample: %v", err)
	}
	var exampleID string
	if item, ok := findItemInState(withExample, collectionID, itemID); ok && len(item.Examples) > 0 {
		exampleID = item.Examples[0].ID
	}
	if exampleID == "" {
		t.Fatal("the response example was not created")
	}
	if _, err := app.UpdateResponseExample(collectionID, itemID, exampleID, ResponseExample{
		ID:      exampleID,
		Name:    "self test",
		Type:    "http",
		Request: ResponseExampleRequest{Method: "GET", URL: targetURL},
		Response: ResponseExamplePayload{
			Status:     200,
			StatusText: "OK",
			Headers:    []KeyValue{{Name: "Content-Type", Value: "application/json", Enabled: true}},
			Body:       `{"served":"by the mock"}`,
		},
	}); err != nil {
		t.Fatalf("UpdateResponseExample: %v", err)
	}

	status, err := app.StartMockServer(collectionID, 0)
	if err != nil {
		t.Fatalf("StartMockServer: %v", err)
	}
	defer func() { _, _ = app.StopMockServer(collectionID) }()
	if status.Routes == 0 {
		t.Fatal("the mock built no routes from the saved example")
	}

	// Point the request at the mock through a collection variable, which is how
	// a user would actually do it.
	collectionVars := []Variable{{
		ID: newID("var"), Name: "mockBase", Value: status.URL,
		Type: "string", DataType: "string", Enabled: true,
	}}
	if _, err := app.UpdateCollectionVariables(collectionID, collectionVars); err != nil {
		t.Fatalf("UpdateCollectionVariables: %v", err)
	}

	final, err := app.SendRequest(collectionID, itemID, "")
	if err != nil {
		t.Fatalf("SendRequest: %v", err)
	}

	item, ok := findItemInState(final, collectionID, itemID)
	if !ok || item.Response == nil {
		t.Fatal("no response was recorded")
	}
	if item.Response.Status != 200 {
		t.Errorf("status = %d, want 200 from the mock", item.Response.Status)
	}
	if item.Response.Body != `{"served":"by the mock"}` {
		t.Errorf("body = %q, want the saved example's body", item.Response.Body)
	}
	if got := item.Response.Headers["Content-Type"]; !strings.Contains(got, "application/json") {
		t.Errorf("content type = %q, want the example's", got)
	}

	// US-073 also requires the mock's own calls to appear in the DevTools
	// network panel. Two entries are expected: the app's outgoing request and
	// the mock's handling of it.
	// Matched on Source, not the URL. The app's own outgoing request and the
	// mock's handling of it have the SAME URL, so a URL match cannot tell them
	// apart — a negative control that removed the mock's logging entirely left
	// a URL-based assertion still passing.
	var mockEntries, outgoingEntries int
	for _, entry := range final.NetworkLog {
		if !strings.Contains(entry.URL, "/self-test") {
			continue
		}
		if entry.Source == "mock" {
			mockEntries++
		} else {
			outgoingEntries++
		}
	}
	if mockEntries == 0 {
		t.Error("the mock's call never reached the DevTools network log")
	}
	if outgoingEntries == 0 {
		t.Error("the app's own outgoing request is missing from the network log")
	}
}

// TestMockCallsAreLoggedForEveryExitPath. The 404 and 400 branches return
// early, so a log written only on the success path would leave exactly the
// calls someone is debugging invisible.
func TestMockCallsAreLoggedForEveryExitPath(t *testing.T) {
	var logged []NetworkLog
	var mu sync.Mutex
	mock, err := localserver.StartMock(mockCollectionFixture(), 0, func(entry NetworkLog) {
		mu.Lock()
		logged = append(logged, entry)
		mu.Unlock()
	})
	if err != nil {
		t.Fatalf("localserver.StartMock: %v", err)
	}
	defer func() { _ = mock.Stop() }()

	// Success.
	if _, err := http.Get(mock.Status().URL + "/v1/users"); err != nil {
		t.Fatalf("GET: %v", err)
	}
	// 404, no such route.
	if _, err := http.Get(mock.Status().URL + "/nothing"); err != nil {
		t.Fatalf("GET: %v", err)
	}
	// 400, unknown example name.
	request, _ := http.NewRequest(http.MethodGet, mock.Status().URL+"/v1/users", nil)
	request.Header.Set(localserver.MockSelectionHeader, "missing")
	if _, err := http.DefaultClient.Do(request); err != nil {
		t.Fatalf("GET: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(logged) != 3 {
		t.Fatalf("got %d log entries, want one per call including the error paths", len(logged))
	}

	statuses := map[int]bool{}
	for _, entry := range logged {
		statuses[entry.Status] = true
		if entry.Method == "" || entry.URL == "" {
			t.Errorf("incomplete log entry: %+v", entry)
		}
	}
	for _, want := range []int{200, 404, 400} {
		if !statuses[want] {
			t.Errorf("no log entry for the %d path", want)
		}
	}
}
