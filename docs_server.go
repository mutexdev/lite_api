package main

// US-074 — the local docs preview server.
//
// GenerateCollectionDocs already produces the HTML. This serves it over
// loopback so it can be opened in a real browser, shared with a colleague over
// a screen share, or reloaded as the collection changes — none of which an
// in-app panel does well.
//
// LOOPBACK ONLY, for the same reason as the mock server and then some:
// generated docs contain every URL, header name and example body in the
// collection, and the environment variables the user chose to include. That is
// a more complete picture of an internal API than most of the requests
// themselves. Binding anything else would publish it.
//
// The docs are RE-GENERATED PER REQUEST rather than captured at start. A
// preview that silently served the collection as it was when the server
// started would be wrong in the one situation it exists for — someone editing
// docs and refreshing to see the change.

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const docsShutdownGrace = 2 * time.Second

// DocsServerStatus is what the UI reads.
type DocsServerStatus struct {
	CollectionID string `json:"collectionId"`
	Running      bool   `json:"running"`
	Port         int    `json:"port"`
	URL          string `json:"url,omitempty"`
	Error        string `json:"error,omitempty"`
}

type docsServer struct {
	collectionID string
	listener     net.Listener
	server       *http.Server
	port         int
}

// startDocsServer binds a loopback listener that renders the collection's docs
// on every request.
//
// `render` is injected rather than the App being captured, so the handler
// cannot reach into app state without going through the one function that
// takes the lock correctly.
func startDocsServer(collectionID string, port int, render func() (GenerateCollectionDocsResult, error)) (*docsServer, error) {
	if port < 0 || port > 65535 {
		return nil, fmt.Errorf("port %d is out of range", port)
	}

	listener, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return nil, fmt.Errorf("docs server could not bind: %w", err)
	}

	docs := &docsServer{
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

func (d *docsServer) stop() error {
	ctx, cancel := context.WithTimeout(context.Background(), docsShutdownGrace)
	defer cancel()
	if err := d.server.Shutdown(ctx); err != nil {
		_ = d.server.Close()
		return err
	}
	return nil
}

func (d *docsServer) status() DocsServerStatus {
	return DocsServerStatus{
		CollectionID: d.collectionID,
		Running:      true,
		Port:         d.port,
		URL:          fmt.Sprintf("http://127.0.0.1:%d", d.port),
	}
}

// --- bindings ------------------------------------------------------------

func (a *App) docs() map[string]*docsServer {
	a.docsOnce.Do(func() { a.docsServers = map[string]*docsServer{} })
	return a.docsServers
}

// StartDocsServer starts (or restarts) the docs preview for a collection.
func (a *App) StartDocsServer(collectionID string, port int, options GenerateCollectionDocsOptions) (DocsServerStatus, error) {
	// Generated once up front so a collection that cannot produce docs fails
	// HERE, with the error in front of the user, rather than starting a server
	// that returns 500 to a browser tab they then have to go and read.
	if _, err := a.GenerateCollectionDocs(collectionID, options); err != nil {
		return DocsServerStatus{}, err
	}

	a.docsMu.Lock()
	defer a.docsMu.Unlock()

	if existing := a.docs()[collectionID]; existing != nil {
		if err := existing.stop(); err != nil {
			return DocsServerStatus{}, err
		}
		delete(a.docs(), collectionID)
	}

	server, err := startDocsServer(collectionID, port, func() (GenerateCollectionDocsResult, error) {
		return a.GenerateCollectionDocs(collectionID, options)
	})
	if err != nil {
		return DocsServerStatus{}, err
	}
	a.docs()[collectionID] = server
	return server.status(), nil
}

// StopDocsServer stops the preview. Stopping one that is not running is not an
// error: the UI's stop button should be idempotent.
func (a *App) StopDocsServer(collectionID string) (DocsServerStatus, error) {
	a.docsMu.Lock()
	defer a.docsMu.Unlock()

	server := a.docs()[collectionID]
	if server == nil {
		return DocsServerStatus{CollectionID: collectionID}, nil
	}
	err := server.stop()
	delete(a.docs(), collectionID)
	return DocsServerStatus{CollectionID: collectionID}, err
}

// DocsServerStatusFor reports one collection's preview server.
func (a *App) DocsServerStatusFor(collectionID string) DocsServerStatus {
	a.docsMu.Lock()
	defer a.docsMu.Unlock()

	server := a.docs()[collectionID]
	if server == nil {
		return DocsServerStatus{CollectionID: collectionID}
	}
	return server.status()
}

// stopAllDocsServers runs on shutdown, for the same reason the mock servers do:
// a listener left bound blocks its own port on the next launch.
func (a *App) stopAllDocsServers() {
	a.docsMu.Lock()
	defer a.docsMu.Unlock()
	for id, server := range a.docs() {
		_ = server.stop()
		delete(a.docs(), id)
	}
}
