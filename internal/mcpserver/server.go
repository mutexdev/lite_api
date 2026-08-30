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

	mu       sync.Mutex
	listener net.Listener
	httpSrv  *http.Server
}

// New prepares a server that will authenticate every request against token
// and answer tools against backend. port 0 asks the OS for an ephemeral port;
// read the resolved one from Port after Start.
func New(backend Backend, token string, port int) *Server {
	return &Server{backend: backend, token: token, port: port}
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
	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", s.handleMCP)
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
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

// handleMCP is replaced by the protocol implementation in protocol.go; until
// that lands, the endpoint answers 501 so the lifecycle can be wired and
// tested independently.
func (s *Server) handleMCP(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "mcp protocol not yet available", http.StatusNotImplemented)
}

// Stop shuts down gracefully, escalating to a hard close when in-flight
// requests outlast ShutdownGrace. Idempotent.
func (s *Server) Stop() {
	s.mu.Lock()
	server := s.httpSrv
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
}
