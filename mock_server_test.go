package main

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
	"testing"
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

func startFixtureMock(t *testing.T) *mockServer {
	t.Helper()
	mock, err := startMockServer(mockCollectionFixture(), 0)
	if err != nil {
		t.Fatalf("startMockServer: %v", err)
	}
	t.Cleanup(func() { _ = mock.stop() })
	return mock
}

// TestMockServerBindsLoopbackOnly is the assertion with the highest stakes in
// this file. A mock replays whatever the user recorded — tokens, internal
// hostnames, customer data — and binding all interfaces publishes it to every
// machine on the network.
func TestMockServerBindsLoopbackOnly(t *testing.T) {
	mock := startFixtureMock(t)

	addr, ok := mock.listener.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("listener address is not TCP: %v", mock.listener.Addr())
	}
	if !addr.IP.IsLoopback() {
		t.Fatalf("mock bound %s, which is reachable from the network; it must be loopback only", addr.IP)
	}
	if mock.status().URL != fmt.Sprintf("http://127.0.0.1:%d", mock.port) {
		t.Errorf("advertised URL %q is not the loopback address", mock.status().URL)
	}
}

func TestMockServerAnswersFromASavedExample(t *testing.T) {
	mock := startFixtureMock(t)

	response, err := http.Get(mock.status().URL + "/v1/users")
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

	request, _ := http.NewRequest(http.MethodGet, mock.status().URL+"/v1/users", nil)
	request.Header.Set(mockSelectionHeader, "no users")
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

	request, _ := http.NewRequest(http.MethodGet, mock.status().URL+"/v1/users", nil)
	request.Header.Set(mockSelectionHeader, "does not exist")
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
		request, _ := http.NewRequest(target.method, mock.status().URL+target.path, nil)
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

// TestMockPathIgnoresUnresolvedVariables. Example URLs are stored with
// {{baseUrl}} intact. Interpolating per request would make which mock answers
// depend on the selected environment, so only the path is compared.
func TestMockPathFromURL(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"{{baseUrl}}/v1/users", "/v1/users"},
		{"https://api.example.test/v1/users", "/v1/users"},
		{"https://api.example.test/v1/users/", "/v1/users"},
		{"{{protocol}}://{{host}}/a/b", "/a/b"},
		{"{{baseUrl}}/v1/users?page=2", "/v1/users"},
		{"{{baseUrl}}", "/"},
		{"", "/"},
		{"https://api.example.test", "/"},
	} {
		if got := mockPathFromURL(tc.in); got != tc.want {
			t.Errorf("mockPathFromURL(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// A trailing slash is a formatting accident in a saved example, not a
// different endpoint.
func TestMockRouteKeyNormalises(t *testing.T) {
	base := mockRouteKey("GET", "/v1/users")
	for _, variant := range []struct{ method, path string }{
		{"get", "/v1/users"},
		{"GET", "v1/users"},
		{"GET", "/v1/users/"},
		{" GET ", " /v1/users "},
	} {
		if got := mockRouteKey(variant.method, variant.path); got != base {
			t.Errorf("mockRouteKey(%q, %q) = %q, want %q", variant.method, variant.path, got, base)
		}
	}
	if mockRouteKey("", "/x") != mockRouteKey("GET", "/x") {
		t.Error("an empty method should default to GET")
	}
	if mockRouteKey("POST", "/x") == mockRouteKey("GET", "/x") {
		t.Error("different methods must not collide")
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
	mock, err := startMockServer(collection, 0)
	if err != nil {
		t.Fatalf("startMockServer: %v", err)
	}
	defer func() { _ = mock.stop() }()

	response, err := http.Get(mock.status().URL + "/v1/users")
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
	mock, err := startMockServer(collection, 0)
	if err != nil {
		t.Fatalf("startMockServer: %v", err)
	}
	defer func() { _ = mock.stop() }()

	response, err := http.Get(mock.status().URL + "/v1/users")
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
	port := mock.port

	updated := mockCollectionFixture()
	updated.Items[0].Examples[0].Response.Body = `{"users":["grace"]}`
	mock.update(updated)

	if mock.port != port {
		t.Errorf("the port changed from %d to %d on a routing update", port, mock.port)
	}
	response, err := http.Get(mock.status().URL + "/v1/users")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = response.Body.Close() }()
	body, _ := io.ReadAll(response.Body)
	if string(body) != `{"users":["grace"]}` {
		t.Errorf("body = %q, want the updated example", body)
	}
}

func TestSelectMockExample(t *testing.T) {
	examples := []ResponseExample{{Name: "first"}, {Name: "second"}}

	if got, err := selectMockExample(examples, ""); err != nil || got.Name != "first" {
		t.Errorf("no name should select the first: %v %v", got.Name, err)
	}
	if got, err := selectMockExample(examples, "SECOND"); err != nil || got.Name != "second" {
		t.Errorf("name matching should be case-insensitive: %v %v", got.Name, err)
	}
	if got, err := selectMockExample(examples, "  second  "); err != nil || got.Name != "second" {
		t.Errorf("a padded name should still match: %v %v", got.Name, err)
	}
	if _, err := selectMockExample(examples, "missing"); err == nil {
		t.Error("an unknown name must be an error, not a fallback")
	}
	if _, err := selectMockExample(nil, ""); err == nil {
		t.Error("no examples must be an error")
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
	if _, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", firstPort), mockShutdownGrace); err == nil && firstPort != restarted.Port {
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
	if _, err := startMockServer(mockCollectionFixture(), -1); err == nil {
		t.Error("a negative port should be rejected")
	}
	if _, err := startMockServer(mockCollectionFixture(), 70000); err == nil {
		t.Error("a port above the range should be rejected")
	}
}

func TestStartMockServerRejectsAnUnknownCollection(t *testing.T) {
	app := newAppForTest(t)
	if _, err := app.StartMockServer("no-such-collection", 0); err == nil {
		t.Error("an unknown collection should be an error")
	}
}
