package localserver

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/mutexdev/lite_api/internal/types"
)

const DocsShutdownGrace = 2 * time.Second

// DocsServerStatus is what the UI reads.
type DocsServerStatus struct {
	CollectionID string `json:"collectionId"`
	Running      bool   `json:"running"`
	Port         int    `json:"port"`
	URL          string `json:"url,omitempty"`
	Error        string `json:"error,omitempty"`
}

type DocsServer struct {
	collectionID string
	listener     net.Listener
	server       *http.Server
	port         int
}

// StartDocs binds a loopback listener that renders the collection's docs
// on every request.
//
// `render` is injected rather than the App being captured, so the handler
// cannot reach into app state without going through the one function that
// takes the lock correctly.
func StartDocs(collectionID string, port int, render func() (types.GenerateCollectionDocsResult, error)) (*DocsServer, error) {
	if port < 0 || port > 65535 {
		return nil, fmt.Errorf("port %d is out of range", port)
	}

	listener, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return nil, fmt.Errorf("docs server could not bind: %w", err)
	}

	docs := &DocsServer{
		collectionID: collectionID,
		listener:     listener,
		port:         listener.Addr().(*net.TCPAddr).Port,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Regenerated per request: the whole point of a preview is that a
		// refresh shows the edit.
		result, err := render()
		if err != nil {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusInternalServerError)
			// The error text is the user's own — a malformed collection — so
			// showing it is what lets them fix it.
			_, _ = fmt.Fprintf(w, "could not generate docs: %v", err)
			return
		}

		switch strings.TrimSuffix(r.URL.Path, "/") {
		case "", "/index.html":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			// A preview that a browser caches is a preview that stops updating,
			// which defeats the reload this exists for.
			w.Header().Set("Cache-Control", "no-store")
			_, _ = w.Write([]byte(result.HTML))
		case "/collection.yaml", "/collection.yml":
			w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			_, _ = w.Write([]byte(result.YAML))
		default:
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusNotFound)
			_, _ = fmt.Fprintf(w, "not found: %s\n\nthis server serves / and /collection.yaml", r.URL.Path)
		}
	})

	docs.server = &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = docs.server.Serve(listener) }()
	return docs, nil
}

func (d *DocsServer) Stop() error {
	ctx, cancel := context.WithTimeout(context.Background(), DocsShutdownGrace)
	defer cancel()
	if err := d.server.Shutdown(ctx); err != nil {
		_ = d.server.Close()
		return err
	}
	return nil
}

func (d *DocsServer) Status() DocsServerStatus {
	return DocsServerStatus{
		CollectionID: d.collectionID,
		Running:      true,
		Port:         d.port,
		URL:          fmt.Sprintf("http://127.0.0.1:%d", d.port),
	}
}

// Addr reports the address the server actually bound.
//
// Exposed so callers and tests can verify the binding rather than trust the
// port: generated docs describe an internal API, and the difference between
// 127.0.0.1 and 0.0.0.0 is the difference between a local preview and
// publishing that API to the network. A port number alone cannot tell them
// apart.
func (d *DocsServer) Addr() net.Addr {
	if d == nil || d.listener == nil {
		return nil
	}
	return d.listener.Addr()
}
