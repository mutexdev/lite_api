package core

import "github.com/mutexdev/lite_api/internal/localserver"

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

// --- bindings ------------------------------------------------------------

func (a *App) docs() map[string]*localserver.DocsServer {
	a.docsOnce.Do(func() { a.docsServers = map[string]*localserver.DocsServer{} })
	return a.docsServers
}

// StartDocsServer starts (or restarts) the docs preview for a collection.
func (a *App) StartDocsServer(collectionID string, port int, options GenerateCollectionDocsOptions) (localserver.DocsServerStatus, error) {
	// Generated once up front so a collection that cannot produce docs fails
	// HERE, with the error in front of the user, rather than starting a server
	// that returns 500 to a browser tab they then have to go and read.
	if _, err := a.GenerateCollectionDocs(collectionID, options); err != nil {
		return localserver.DocsServerStatus{}, err
	}

	a.docsMu.Lock()
	defer a.docsMu.Unlock()

	if existing := a.docs()[collectionID]; existing != nil {
		if err := existing.Stop(); err != nil {
			return localserver.DocsServerStatus{}, err
		}
		delete(a.docs(), collectionID)
	}

	server, err := localserver.StartDocs(collectionID, port, func() (GenerateCollectionDocsResult, error) {
		return a.GenerateCollectionDocs(collectionID, options)
	})
	if err != nil {
		return localserver.DocsServerStatus{}, err
	}
	a.docs()[collectionID] = server
	return server.Status(), nil
}

// StopDocsServer stops the preview. Stopping one that is not running is not an
// error: the UI's stop button should be idempotent.
func (a *App) StopDocsServer(collectionID string) (localserver.DocsServerStatus, error) {
	a.docsMu.Lock()
	defer a.docsMu.Unlock()

	server := a.docs()[collectionID]
	if server == nil {
		return localserver.DocsServerStatus{CollectionID: collectionID}, nil
	}
	err := server.Stop()
	delete(a.docs(), collectionID)
	return localserver.DocsServerStatus{CollectionID: collectionID}, err
}

// DocsServerStatusFor reports one collection's preview server.
func (a *App) DocsServerStatusFor(collectionID string) localserver.DocsServerStatus {
	a.docsMu.Lock()
	defer a.docsMu.Unlock()

	server := a.docs()[collectionID]
	if server == nil {
		return localserver.DocsServerStatus{CollectionID: collectionID}
	}
	return server.Status()
}

// stopAllDocsServers runs on shutdown, for the same reason the mock servers do:
// a listener left bound blocks its own port on the next launch.
func (a *App) stopAllDocsServers() {
	a.docsMu.Lock()
	defer a.docsMu.Unlock()
	for id, server := range a.docs() {
		_ = server.Stop()
		delete(a.docs(), id)
	}
}
