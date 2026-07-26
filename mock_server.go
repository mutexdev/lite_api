package main

// US-072 — the per-collection mock server.
//
// A local HTTP listener that answers requests from a collection's saved
// response examples. Matching is method + path against the request tree, and
// the reply is the example's stored status, headers and body.
//
// THE LISTENER BINDS TO LOOPBACK ONLY, and that is not a default to be
// overridden later. A mock replays whatever the user recorded, which routinely
// includes tokens, internal hostnames and customer data captured from a real
// API. Binding 0.0.0.0 would publish all of it to every machine on the network
// — a coffee-shop Wi-Fi away from being a data leak — from a feature whose
// entire purpose is local development.
//
// Path matching deliberately does NOT interpolate variables. An example's URL
// is stored with {{baseUrl}} still in it, and resolving that per request would
// make which mock answers depend on the currently selected environment — so
// the same request would hit a different mock after switching environments,
// with nothing to explain why. Only the path portion is compared.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// mockSelectionHeader lets a caller ask for a specific example by name.
// Postman uses x-mock-response-name; matching it means a collection's existing
// tests keep working.
const mockSelectionHeader = "x-mock-response-name"

// mockShutdownGrace bounds how long Stop waits for in-flight requests.
const mockShutdownGrace = 2 * time.Second

// MockServerStatus is what the UI reads.
type MockServerStatus struct {
	CollectionID string `json:"collectionId"`
	Running      bool   `json:"running"`
	Port         int    `json:"port"`
	// URL is the loopback address to send to, empty when not running.
	URL string `json:"url,omitempty"`
	// Routes is how many method+path pairs are answerable.
	Routes int    `json:"routes"`
	Error  string `json:"error,omitempty"`
}

type mockServer struct {
	collectionID string
	listener     net.Listener
	server       *http.Server
	port         int
	// US-073. Calls are reported to the app so they land in the same DevTools
	// network panel as real requests. A function rather than an *App so the
	// server stays testable without one, and so the handler cannot reach into
	// app state on a goroutine it does not own the locks for.
	record func(NetworkLog)

	mu     sync.RWMutex
	routes map[string][]ResponseExample
}

// mockRouteKey normalises a method and path into a lookup key.
//
// The path is normalised so that "/users", "users" and "/users/" all match: a
// trailing slash is a formatting accident in a saved example, not a different
// endpoint, and leaving them distinct produces a 404 the user cannot see the
// cause of.
func mockRouteKey(method, path string) string {
	normalizedMethod := strings.ToUpper(strings.TrimSpace(method))
	if normalizedMethod == "" {
		normalizedMethod = http.MethodGet
	}
	normalizedPath := "/" + strings.Trim(strings.TrimSpace(path), "/")
	return normalizedMethod + " " + normalizedPath
}

// mockPathFromURL extracts the path from a stored example URL.
//
// The URL routinely still contains {{baseUrl}} and other placeholders, which
// url.Parse will not accept as a host. Rather than interpolate — see the file
// comment for why that would make routing depend on the selected environment —
// the placeholder prefix is stripped and whatever follows is treated as the
// path.
func mockPathFromURL(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "/"
	}
	if parsed, err := url.Parse(trimmed); err == nil && parsed.Host != "" {
		return "/" + strings.Trim(parsed.Path, "/")
	}

	// No parseable host: drop any scheme, then take everything from the first
	// slash that follows a placeholder or bare host.
	withoutScheme := trimmed
	if index := strings.Index(withoutScheme, "://"); index >= 0 {
		withoutScheme = withoutScheme[index+3:]
	}
	if index := strings.Index(withoutScheme, "?"); index >= 0 {
		withoutScheme = withoutScheme[:index]
	}
	slash := strings.Index(withoutScheme, "/")
	if slash < 0 {
		return "/"
	}
	return "/" + strings.Trim(withoutScheme[slash:], "/")
}

// buildMockRoutes collects every answerable route from a collection.
//
// An item with no saved examples contributes nothing: a mock that answered a
// request it had no recorded response for would have to invent one, and an
// invented 200 is worse than a 404 the user can act on.
func buildMockRoutes(collection Collection) map[string][]ResponseExample {
	routes := map[string][]ResponseExample{}
	for _, item := range collection.Items {
		if item.Type != "" && item.Type != "http" && item.Type != "graphql" {
			continue
		}
		for _, example := range item.Examples {
			method := firstNonEmpty(example.Request.Method, item.Method, http.MethodGet)
			path := mockPathFromURL(firstNonEmpty(example.Request.URL, item.URL))
			key := mockRouteKey(method, path)
			routes[key] = append(routes[key], example)
		}
	}
	return routes
}

// selectMockExample picks which example answers a request.
//
// A named selection that does not exist is an ERROR rather than a silent
// fallback to the first example. Someone asking for "not found" and being
// handed the 200 would see their test pass for the wrong reason.
func selectMockExample(examples []ResponseExample, requestedName string) (ResponseExample, error) {
	if len(examples) == 0 {
		return ResponseExample{}, errors.New("no examples for this route")
	}

	wanted := strings.TrimSpace(requestedName)
	if wanted == "" {
		return examples[0], nil
	}
	for _, example := range examples {
		if strings.EqualFold(strings.TrimSpace(example.Name), wanted) {
			return example, nil
		}
	}

	names := make([]string, 0, len(examples))
	for _, example := range examples {
		names = append(names, example.Name)
	}
	sort.Strings(names)
	return ResponseExample{}, fmt.Errorf("no example named %q; this route has: %s", wanted, strings.Join(names, ", "))
}

func (m *mockServer) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		// Read under a limit rather than with ReadAll: the body is only used
		// for the log, and an unbounded read lets one client hand the app
		// however much memory it likes.
		requestBody, _ := io.ReadAll(io.LimitReader(r.Body, networkLogBodyLimit))

		// The status and size are captured by the closure below so one log
		// entry covers every exit path, including the 404 and 400 branches
		// that return early.
		status := 0
		var responseBody string
		defer func() {
			if m.record == nil {
				return
			}
			m.record(NetworkLog{
				ID:              newID("network"),
				Source:          "mock",
				Method:          r.Method,
				URL:             fmt.Sprintf("http://127.0.0.1:%d%s", m.port, r.URL.RequestURI()),
				Status:          status,
				StatusText:      http.StatusText(status),
				DurationMs:      time.Since(started).Milliseconds(),
				Size:            len(responseBody),
				At:              started,
				RequestHeaders:  mockHeaderMap(r.Header),
				RequestBody:     string(requestBody),
				ResponseHeaders: mockHeaderMap(w.Header()),
				ResponseBody:    responseBody,
			})
		}()

		m.mu.RLock()
		examples := m.routes[mockRouteKey(r.Method, r.URL.Path)]
		routeCount := len(m.routes)
		m.mu.RUnlock()

		if len(examples) == 0 {
			// The available routes are listed in the body. A bare 404 from a
			// mock is indistinguishable from a typo in the request, and the
			// user cannot see the routing table any other way.
			w.Header().Set("Content-Type", "application/json")
			status = http.StatusNotFound
			responseBody = fmt.Sprintf(`{"error":"no saved example matches %s %s","routes":%d}`,
				r.Method, r.URL.Path, routeCount)
			w.WriteHeader(status)
			_, _ = io.WriteString(w, responseBody)
			return
		}

		example, err := selectMockExample(examples, r.Header.Get(mockSelectionHeader))
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			status = http.StatusBadRequest
			responseBody = fmt.Sprintf(`{"error":%q}`, err.Error())
			w.WriteHeader(status)
			_, _ = io.WriteString(w, responseBody)
			return
		}

		for _, header := range example.Response.Headers {
			name := strings.TrimSpace(header.Name)
			if name == "" || !header.Enabled {
				continue
			}
			// Content-Length is recomputed by net/http from what is actually
			// written; a recorded one that no longer matches an edited body
			// makes the client read a truncated response.
			if strings.EqualFold(name, "content-length") {
				continue
			}
			w.Header().Add(name, header.Value)
		}

		status = example.Response.Status
		if status <= 0 {
			status = http.StatusOK
		}
		responseBody = example.Response.Body
		w.WriteHeader(status)
		_, _ = io.WriteString(w, responseBody)
	})
}

// startMockServer binds a loopback listener and serves the collection.
//
// Port 0 asks the OS for a free port, which is the sane default: a fixed port
// collides with whatever else the user is running and fails at bind time with
// an error they then have to diagnose.
// mockHeaderMap flattens an http.Header for the network log, which stores a
// single value per name.
func mockHeaderMap(header http.Header) map[string]string {
	out := make(map[string]string, len(header))
	for name, values := range header {
		if len(values) > 0 {
			out[name] = strings.Join(values, ", ")
		}
	}
	return out
}

func startMockServer(collection Collection, port int, record func(NetworkLog)) (*mockServer, error) {
	if port < 0 || port > 65535 {
		return nil, fmt.Errorf("port %d is out of range", port)
	}

	// Loopback explicitly. See the file comment: a mock replays recorded data
	// that routinely includes credentials, and binding all interfaces would
	// publish it to the network.
	listener, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return nil, fmt.Errorf("mock server could not bind: %w", err)
	}

	mock := &mockServer{
		collectionID: collection.ID,
		listener:     listener,
		port:         listener.Addr().(*net.TCPAddr).Port,
		routes:       buildMockRoutes(collection),
		record:       record,
	}
	mock.server = &http.Server{
		Handler: mock.handler(),
		// A mock is a local convenience, but an unbounded read timeout still
		// lets one stuck client hold a connection open forever.
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() { _ = mock.server.Serve(listener) }()
	return mock, nil
}

func (m *mockServer) stop() error {
	ctx, cancel := context.WithTimeout(context.Background(), mockShutdownGrace)
	defer cancel()
	err := m.server.Shutdown(ctx)
	if err != nil {
		// Shutdown timed out with requests still in flight; Close is the
		// escalation, and leaving the port bound would block the next start.
		_ = m.server.Close()
	}
	return err
}

// update swaps in a new routing table without restarting the listener, so the
// port and any open connections survive an edit to the collection.
func (m *mockServer) update(collection Collection) {
	routes := buildMockRoutes(collection)
	m.mu.Lock()
	m.routes = routes
	m.mu.Unlock()
}

func (m *mockServer) status() MockServerStatus {
	m.mu.RLock()
	routes := len(m.routes)
	m.mu.RUnlock()
	return MockServerStatus{
		CollectionID: m.collectionID,
		Running:      true,
		Port:         m.port,
		URL:          fmt.Sprintf("http://127.0.0.1:%d", m.port),
		Routes:       routes,
	}
}

// --- bindings ------------------------------------------------------------

func (a *App) mocks() map[string]*mockServer {
	a.mockOnce.Do(func() { a.mockServers = map[string]*mockServer{} })
	return a.mockServers
}

// StartMockServer starts (or restarts) the mock for a collection.
func (a *App) StartMockServer(collectionID string, port int) (MockServerStatus, error) {
	a.mu.Lock()
	if err := a.ensureReadyLocked(); err != nil {
		a.mu.Unlock()
		return MockServerStatus{}, err
	}
	collection, err := a.findCollectionLocked(collectionID)
	if err != nil {
		a.mu.Unlock()
		return MockServerStatus{}, err
	}
	// Copied under the lock and used outside it: holding a pointer across the
	// release is the US-076 bug, and binding a socket is not something to do
	// while holding the state lock.
	snapshot := *collection
	a.mu.Unlock()

	a.mockMu.Lock()
	defer a.mockMu.Unlock()

	if existing := a.mocks()[collectionID]; existing != nil {
		if err := existing.stop(); err != nil {
			return MockServerStatus{}, err
		}
		delete(a.mocks(), collectionID)
	}

	mock, err := startMockServer(snapshot, port, a.recordMockNetworkLog)
	if err != nil {
		return MockServerStatus{}, err
	}
	a.mocks()[collectionID] = mock
	return mock.status(), nil
}

// StopMockServer stops the mock for a collection. Stopping one that is not
// running is not an error: the UI's stop button should be idempotent rather
// than reporting a failure for reaching the state the user asked for.
func (a *App) StopMockServer(collectionID string) (MockServerStatus, error) {
	a.mockMu.Lock()
	defer a.mockMu.Unlock()

	mock := a.mocks()[collectionID]
	if mock == nil {
		return MockServerStatus{CollectionID: collectionID}, nil
	}
	err := mock.stop()
	delete(a.mocks(), collectionID)
	return MockServerStatus{CollectionID: collectionID}, err
}

// MockServerStatusFor reports one collection's mock.
func (a *App) MockServerStatusFor(collectionID string) MockServerStatus {
	a.mockMu.Lock()
	defer a.mockMu.Unlock()

	mock := a.mocks()[collectionID]
	if mock == nil {
		return MockServerStatus{CollectionID: collectionID}
	}
	return mock.status()
}

// RefreshMockServer re-reads the collection into a running mock, so saving an
// example takes effect without restarting and changing the port.
func (a *App) RefreshMockServer(collectionID string) (MockServerStatus, error) {
	a.mu.Lock()
	if err := a.ensureReadyLocked(); err != nil {
		a.mu.Unlock()
		return MockServerStatus{}, err
	}
	collection, err := a.findCollectionLocked(collectionID)
	if err != nil {
		a.mu.Unlock()
		return MockServerStatus{}, err
	}
	snapshot := *collection
	a.mu.Unlock()

	a.mockMu.Lock()
	defer a.mockMu.Unlock()

	mock := a.mocks()[collectionID]
	if mock == nil {
		return MockServerStatus{CollectionID: collectionID}, nil
	}
	mock.update(snapshot)
	return mock.status(), nil
}

// recordMockNetworkLog puts a mock call into the same DevTools network panel
// as a real request.
//
// Takes the state lock itself, because it runs on the mock's own goroutine —
// the handler must never assume the caller's locking.
func (a *App) recordMockNetworkLog(entry NetworkLog) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.state.NetworkLog = append([]NetworkLog{entry}, a.state.NetworkLog...)
	if len(a.state.NetworkLog) > 100 {
		a.state.NetworkLog = a.state.NetworkLog[:100]
	}
	_ = a.markDirty(persistScopeState)
}

// stopAllMockServers is called on shutdown. A listener left bound outlives the
// app and blocks the port on the next launch.
func (a *App) stopAllMockServers() {
	a.mockMu.Lock()
	defer a.mockMu.Unlock()
	for id, mock := range a.mocks() {
		_ = mock.stop()
		delete(a.mocks(), id)
	}
}
