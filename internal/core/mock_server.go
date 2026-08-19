package core

import "github.com/mutexdev/lite_api/internal/localserver"

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

// --- bindings ------------------------------------------------------------

func (a *App) mocks() map[string]*localserver.MockServer {
	a.mockOnce.Do(func() { a.mockServers = map[string]*localserver.MockServer{} })
	return a.mockServers
}

// StartMockServer starts (or restarts) the mock for a collection.
func (a *App) StartMockServer(collectionID string, port int) (localserver.MockServerStatus, error) {
	a.mu.Lock()
	if err := a.ensureReadyLocked(); err != nil {
		a.mu.Unlock()
		return localserver.MockServerStatus{}, err
	}
	collection, err := a.findCollectionLocked(collectionID)
	if err != nil {
		a.mu.Unlock()
		return localserver.MockServerStatus{}, err
	}
	// Copied under the lock and used outside it: holding a pointer across the
	// release is the US-076 bug, and binding a socket is not something to do
	// while holding the state lock.
	snapshot := *collection
	a.mu.Unlock()

	a.mockMu.Lock()
	defer a.mockMu.Unlock()

	if existing := a.mocks()[collectionID]; existing != nil {
		if err := existing.Stop(); err != nil {
			return localserver.MockServerStatus{}, err
		}
		delete(a.mocks(), collectionID)
	}

	mock, err := localserver.StartMock(snapshot, port, a.recordMockNetworkLog)
	if err != nil {
		return localserver.MockServerStatus{}, err
	}
	a.mocks()[collectionID] = mock
	return mock.Status(), nil
}

// StopMockServer stops the mock for a collection. Stopping one that is not
// running is not an error: the UI's stop button should be idempotent rather
// than reporting a failure for reaching the state the user asked for.
func (a *App) StopMockServer(collectionID string) (localserver.MockServerStatus, error) {
	a.mockMu.Lock()
	defer a.mockMu.Unlock()

	mock := a.mocks()[collectionID]
	if mock == nil {
		return localserver.MockServerStatus{CollectionID: collectionID}, nil
	}
	err := mock.Stop()
	delete(a.mocks(), collectionID)
	return localserver.MockServerStatus{CollectionID: collectionID}, err
}

// MockServerStatusFor reports one collection's mock.
func (a *App) MockServerStatusFor(collectionID string) localserver.MockServerStatus {
	a.mockMu.Lock()
	defer a.mockMu.Unlock()

	mock := a.mocks()[collectionID]
	if mock == nil {
		return localserver.MockServerStatus{CollectionID: collectionID}
	}
	return mock.Status()
}

// RefreshMockServer re-reads the collection into a running mock, so saving an
// example takes effect without restarting and changing the port.
func (a *App) RefreshMockServer(collectionID string) (localserver.MockServerStatus, error) {
	a.mu.Lock()
	if err := a.ensureReadyLocked(); err != nil {
		a.mu.Unlock()
		return localserver.MockServerStatus{}, err
	}
	collection, err := a.findCollectionLocked(collectionID)
	if err != nil {
		a.mu.Unlock()
		return localserver.MockServerStatus{}, err
	}
	snapshot := *collection
	a.mu.Unlock()

	a.mockMu.Lock()
	defer a.mockMu.Unlock()

	mock := a.mocks()[collectionID]
	if mock == nil {
		return localserver.MockServerStatus{CollectionID: collectionID}, nil
	}
	mock.Update(snapshot)
	return mock.Status(), nil
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
		_ = mock.Stop()
		delete(a.mocks(), id)
	}
}
