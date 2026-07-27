package localserver

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

	"github.com/mutexdev/lite_api/internal/scalar"
	"github.com/mutexdev/lite_api/internal/types"
)

// NetworkLogBodyLimit caps what a mock server reads from an inbound request
// before recording it. The body is headed for the in-memory network log and
// then a webview, so an unbounded read turns one large upload into a memory
// problem. Mirrors the limit internal/core applies to real responses.
const NetworkLogBodyLimit = 64 * 1024

// MockSelectionHeader lets a caller ask for a specific example by name.
// Postman uses x-mock-response-name; matching it means a collection's existing
// tests keep working.
const MockSelectionHeader = "x-mock-response-name"

// MockShutdownGrace bounds how long Stop waits for in-flight requests.
const MockShutdownGrace = 2 * time.Second

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

type MockServer struct {
	collectionID string
	listener     net.Listener
	server       *http.Server
	port         int
	// US-073. Calls are reported to the app so they land in the same DevTools
	// network panel as real requests. A function rather than an *App so the
	// server stays testable without one, and so the handler cannot reach into
	// app state on a goroutine it does not own the locks for.
	record func(types.NetworkLog)

	mu     sync.RWMutex
	routes map[string][]types.ResponseExample
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
func buildMockRoutes(collection types.Collection) map[string][]types.ResponseExample {
	routes := map[string][]types.ResponseExample{}
	for _, item := range collection.Items {
		if item.Type != "" && item.Type != "http" && item.Type != "graphql" {
			continue
		}
		for _, example := range item.Examples {
			method := scalar.FirstNonEmpty(example.Request.Method, item.Method, http.MethodGet)
			path := mockPathFromURL(scalar.FirstNonEmpty(example.Request.URL, item.URL))
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
func selectMockExample(examples []types.ResponseExample, requestedName string) (types.ResponseExample, error) {
	if len(examples) == 0 {
		return types.ResponseExample{}, errors.New("no examples for this route")
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
	return types.ResponseExample{}, fmt.Errorf("no example named %q; this route has: %s", wanted, strings.Join(names, ", "))
}

func (m *MockServer) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		// Read under a limit rather than with ReadAll: the body is only used
		// for the log, and an unbounded read lets one client hand the app
		// however much memory it likes.
		requestBody, _ := io.ReadAll(io.LimitReader(r.Body, NetworkLogBodyLimit))

		// The status and size are captured by the closure below so one log
		// entry covers every exit path, including the 404 and 400 branches
		// that return early.
		status := 0
		var responseBody string
		defer func() {
			if m.record == nil {
				return
			}
			m.record(types.NetworkLog{
				ID:              scalar.NewID("network"),
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

		example, err := selectMockExample(examples, r.Header.Get(MockSelectionHeader))
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

// StartMock binds a loopback listener and serves the collection.
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

func StartMock(collection types.Collection, port int, record func(types.NetworkLog)) (*MockServer, error) {
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

	mock := &MockServer{
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

func (m *MockServer) Stop() error {
	ctx, cancel := context.WithTimeout(context.Background(), MockShutdownGrace)
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
func (m *MockServer) Update(collection types.Collection) {
	routes := buildMockRoutes(collection)
	m.mu.Lock()
	m.routes = routes
	m.mu.Unlock()
}

func (m *MockServer) Status() MockServerStatus {
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

// Addr reports the address the server actually bound. See DocsServer.Addr —
// a mock answers with whatever the collection's saved examples say, so binding
// it beyond loopback exposes a fake API to the network.
func (m *MockServer) Addr() net.Addr {
	if m == nil || m.listener == nil {
		return nil
	}
	return m.listener.Addr()
}
