package core

// US-074 — tests for the docs preview server.
//
// Two properties carry the story. The listener must be LOOPBACK-ONLY: generated
// docs contain every URL, header name and example body in the collection plus
// whichever environment variables the user chose to include — a more complete
// picture of an internal API than most of the requests themselves.
//
// And the docs must be RE-GENERATED PER REQUEST. A preview that served a
// snapshot from start time would be wrong in the exact situation it exists for:
// editing docs and refreshing to see the change. That failure is silent — the
// page renders perfectly, just with yesterday's content.

import (
	"io"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/mutexdev/lite_api/internal/localserver"
)

func docsFixture(t *testing.T) (*App, string) {
	t.Helper()
	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	collectionID := state.Workspaces[0].Collections[0].ID
	if _, err := app.CreateRequest(collectionID, "http", "docs probe"); err != nil {
		t.Fatalf("CreateRequest: %v", err)
	}
	return app, collectionID
}

func TestDocsServerBindsLoopbackOnly(t *testing.T) {
	server, err := localserver.StartDocs("c", 0, func() (GenerateCollectionDocsResult, error) {
		return GenerateCollectionDocsResult{HTML: "<p>ok</p>"}, nil
	})
	if err != nil {
		t.Fatalf("localserver.StartDocs: %v", err)
	}
	defer func() { _ = server.Stop() }()

	addr, ok := server.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("listener address is not TCP: %v", server.Addr())
	}
	if !addr.IP.IsLoopback() {
		t.Fatalf("docs server bound %s; generated docs describe an internal API and must not be published to the network", addr.IP)
	}
}

// The property the preview exists for. A snapshot captured at start renders
// perfectly and shows yesterday's content.
func TestDocsAreRegeneratedOnEveryRequest(t *testing.T) {
	var renders int64
	body := "<p>first</p>"
	server, err := localserver.StartDocs("c", 0, func() (GenerateCollectionDocsResult, error) {
		atomic.AddInt64(&renders, 1)
		return GenerateCollectionDocsResult{HTML: body}, nil
	})
	if err != nil {
		t.Fatalf("localserver.StartDocs: %v", err)
	}
	defer func() { _ = server.Stop() }()

	first := fetchDocs(t, server.Status().URL+"/")
	if first != "<p>first</p>" {
		t.Fatalf("first fetch = %q", first)
	}

	// Simulate the user editing the collection between refreshes.
	body = "<p>edited</p>"
	second := fetchDocs(t, server.Status().URL+"/")
	if second != "<p>edited</p>" {
		t.Errorf("second fetch = %q; the preview is serving a snapshot from start time", second)
	}
	if atomic.LoadInt64(&renders) != 2 {
		t.Errorf("rendered %d times for 2 requests", renders)
	}
}

func fetchDocs(t *testing.T, url string) string {
	t.Helper()
	response, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer func() { _ = response.Body.Close() }()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read %s: %v", url, err)
	}
	return string(data)
}

// A cached preview stops updating, which defeats the reload it exists for.
func TestDocsAreNotCacheable(t *testing.T) {
	server, err := localserver.StartDocs("c", 0, func() (GenerateCollectionDocsResult, error) {
		return GenerateCollectionDocsResult{HTML: "<p>ok</p>", YAML: "name: ok"}, nil
	})
	if err != nil {
		t.Fatalf("localserver.StartDocs: %v", err)
	}
	defer func() { _ = server.Stop() }()

	for _, path := range []string{"/", "/collection.yaml"} {
		response, err := http.Get(server.Status().URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		_ = response.Body.Close()
		if got := response.Header.Get("Cache-Control"); !strings.Contains(got, "no-store") {
			t.Errorf("%s Cache-Control = %q; a cached preview stops updating", path, got)
		}
	}
}

func TestDocsServerServesHTMLAndYAML(t *testing.T) {
	server, err := localserver.StartDocs("c", 0, func() (GenerateCollectionDocsResult, error) {
		return GenerateCollectionDocsResult{HTML: "<h1>Docs</h1>", YAML: "name: fixture"}, nil
	})
	if err != nil {
		t.Fatalf("localserver.StartDocs: %v", err)
	}
	defer func() { _ = server.Stop() }()

	for _, tc := range []struct{ path, want, contentType string }{
		{"/", "<h1>Docs</h1>", "text/html"},
		{"/index.html", "<h1>Docs</h1>", "text/html"},
		{"/collection.yaml", "name: fixture", "application/yaml"},
		{"/collection.yml", "name: fixture", "application/yaml"},
	} {
		response, err := http.Get(server.Status().URL + tc.path)
		if err != nil {
			t.Fatalf("GET %s: %v", tc.path, err)
		}
		data, _ := io.ReadAll(response.Body)
		_ = response.Body.Close()

		if string(data) != tc.want {
			t.Errorf("%s served %q, want %q", tc.path, data, tc.want)
		}
		if got := response.Header.Get("Content-Type"); !strings.Contains(got, tc.contentType) {
			t.Errorf("%s content type = %q, want %s", tc.path, got, tc.contentType)
		}
	}
}

// An unknown path names what IS served. A bare 404 from a local preview leaves
// the user guessing at the routes.
func TestDocsServerUnknownPathNamesWhatItServes(t *testing.T) {
	server, err := localserver.StartDocs("c", 0, func() (GenerateCollectionDocsResult, error) {
		return GenerateCollectionDocsResult{HTML: "<p>ok</p>"}, nil
	})
	if err != nil {
		t.Fatalf("localserver.StartDocs: %v", err)
	}
	defer func() { _ = server.Stop() }()

	response, err := http.Get(server.Status().URL + "/nope")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	data, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()

	if response.StatusCode != 404 {
		t.Errorf("status = %d, want 404", response.StatusCode)
	}
	if !strings.Contains(string(data), "collection.yaml") {
		t.Errorf("the 404 does not say what is served: %q", data)
	}
}

// A generation failure is the user's own malformed collection, so the message
// has to reach them rather than being swallowed into a blank page.
func TestDocsServerShowsAGenerationFailure(t *testing.T) {
	server, err := localserver.StartDocs("c", 0, func() (GenerateCollectionDocsResult, error) {
		return GenerateCollectionDocsResult{}, io.ErrUnexpectedEOF
	})
	if err != nil {
		t.Fatalf("localserver.StartDocs: %v", err)
	}
	defer func() { _ = server.Stop() }()

	response, err := http.Get(server.Status().URL + "/")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	data, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()

	if response.StatusCode != 500 {
		t.Errorf("status = %d, want 500", response.StatusCode)
	}
	if !strings.Contains(string(data), "unexpected EOF") {
		t.Errorf("the failure reason never reached the page: %q", data)
	}
}

// TestDocsServerLifecycle covers the bindings as the UI drives them, including
// the real generator.
func TestDocsServerLifecycle(t *testing.T) {
	app, collectionID := docsFixture(t)

	if app.DocsServerStatusFor(collectionID).Running {
		t.Error("a fresh collection reports a running docs server")
	}

	status, err := app.StartDocsServer(collectionID, 0, GenerateCollectionDocsOptions{})
	if err != nil {
		t.Fatalf("StartDocsServer: %v", err)
	}
	if !status.Running || status.Port == 0 {
		t.Fatalf("status = %+v", status)
	}

	// The real generator's output must actually reach the page.
	page := fetchDocs(t, status.URL+"/")
	if !strings.Contains(strings.ToLower(page), "<html") {
		t.Errorf("the served page is not the generated HTML: %.120q", page)
	}

	if _, err := app.StopDocsServer(collectionID); err != nil {
		t.Fatalf("StopDocsServer: %v", err)
	}
	if app.DocsServerStatusFor(collectionID).Running {
		t.Error("still running after stop")
	}
	// Idempotent stop, as the UI's button needs.
	if _, err := app.StopDocsServer(collectionID); err != nil {
		t.Errorf("stopping an already-stopped server errored: %v", err)
	}
}

// A collection that cannot generate docs must fail at START, with the error in
// front of the user — not start a server that returns 500 into a browser tab
// they have to go and read.
func TestStartDocsServerRejectsAnUnknownCollection(t *testing.T) {
	app := newAppForTest(t)
	if _, err := app.StartDocsServer("no-such-collection", 0, GenerateCollectionDocsOptions{}); err == nil {
		t.Error("an unknown collection should fail at start")
	}
	if app.DocsServerStatusFor("no-such-collection").Running {
		t.Error("a server was started for a collection that cannot generate docs")
	}
}

func TestStartDocsServerRejectsABadPort(t *testing.T) {
	render := func() (GenerateCollectionDocsResult, error) { return GenerateCollectionDocsResult{}, nil }
	if _, err := localserver.StartDocs("c", -1, render); err == nil {
		t.Error("a negative port should be rejected")
	}
	if _, err := localserver.StartDocs("c", 70000, render); err == nil {
		t.Error("a port above the range should be rejected")
	}
}
