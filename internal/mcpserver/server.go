// The server lifecycle: bind, serve, stop. The MCP protocol itself (JSON-RPC
// dispatch, the tool registry) lands in protocol.go and tools.go; this file
// only guarantees that a listener exists, that it is loopback-only, and that
// stopping it cannot leave the port bound.
package mcpserver

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// AuditEntry is one recorded tool call. ArgsSummary is a compact, already
// redaction-safe rendering of the arguments (long values truncated) — the
// recorder must be able to persist it verbatim.
type AuditEntry struct {
	At          time.Time
	Tool        string
	ArgsSummary string
	Outcome     string // "ok", "error", or "denied"
	DurationMs  int
}

// AuditRecorder receives one entry per tools/call. It runs on the request's
// goroutine, so implementations queue or write quickly and never block on the
// app's state lock.
type AuditRecorder func(entry AuditEntry)

// Option configures a Server at construction.
type Option func(*Server)

// WithAuditRecorder installs the audit sink. Without one, calls are served but
// not recorded — the Phase 1 read-only posture.
func WithAuditRecorder(recorder AuditRecorder) Option {
	return func(s *Server) { s.audit = recorder }
}

// ShutdownGrace bounds how long Stop waits for in-flight tool calls before
// closing the listener outright. Mirrors internal/localserver's rule: leaving
// the port bound would block the next start.
const ShutdownGrace = 3 * time.Second

// Server is one MCP endpoint bound to loopback. Construct with New, then
// Start; Stop is safe to call at any point and more than once.
type Server struct {
	backend Backend
	token   string
	port    int
	audit   AuditRecorder

	mu       sync.Mutex
	listener net.Listener
	httpSrv  *http.Server
}

// New prepares a server that will authenticate every request against token
// and answer tools against backend. port 0 asks the OS for an ephemeral port;
// read the resolved one from Port after Start.
func New(backend Backend, token string, port int, options ...Option) *Server {
	server := &Server{backend: backend, token: token, port: port}
	for _, option := range options {
		option(server)
	}
	return server
}

// Start binds 127.0.0.1 and begins serving. Loopback-only is deliberate and
// not configurable: tool output includes collection contents, and binding all
// interfaces would publish them to the network.
func (s *Server) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listener != nil {
		return errors.New("mcp server already started")
	}
	listener, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(s.port)))
	if err != nil {
		return err
	}
	server := &http.Server{Handler: s.handler(), ReadHeaderTimeout: 10 * time.Second}
	s.listener = listener
	s.httpSrv = server
	go func() {
		// ErrServerClosed is the normal end of life; anything else already
		// surfaced to the client whose connection failed.
		_ = server.Serve(listener)
	}()
	return nil
}

// Port returns the bound port, or 0 before Start.
func (s *Server) Port() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listener == nil {
		return 0
	}
	if addr, ok := s.listener.Addr().(*net.TCPAddr); ok {
		return addr.Port
	}
	return 0
}

// handler builds the routing surface: one endpoint, /mcp, whose handler lives
// in protocol.go. Anything else is a 404 from the mux, which is the honest
// answer — this server hosts no other resource, not even a status page, since
// an unauthenticated page here would leak that LiteAPI is running.
//
// Split out of Start so tests can drive the whole stack through httptest
// without binding a real port.
func (s *Server) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", s.handleMCP)
	return mux
}

// Stop shuts down gracefully, escalating to a hard close when in-flight
// requests outlast ShutdownGrace. Idempotent.
func (s *Server) Stop() {
	s.mu.Lock()
	server := s.httpSrv
	listener := s.listener
	s.listener = nil
	s.httpSrv = nil
	s.mu.Unlock()
	if server == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), ShutdownGrace)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		_ = server.Close()
	}
	// Shutdown only closes listeners Serve has already registered. If Stop
	// wins the race with the Serve goroutine, Serve later declines to track
	// the listener and nobody closes it — the port would stay bound and block
	// the next Start. Closing it here is idempotent and makes release
	// deterministic, which the control tests rely on.
	if listener != nil {
		_ = listener.Close()
	}
}
