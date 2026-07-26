package main

// US-020 — a.mu is a sync.RWMutex and a small, audited set of provably
// read-only call sites take RLock instead of Lock.
//
// Why this file exists: `go test -race` is only as good as the interleavings
// the tests actually produce. Every pre-existing test drives the App from a
// single goroutine, so an RLock conversion that lets N readers mutate shared
// state concurrently would sail through the whole suite untouched. These tests
// exist to create the interleaving — many readers against many writers, on the
// exact fields the converted call sites touch — so that the race detector has
// something to detect.
//
// They assert almost nothing about return values on purpose. The assertion is
// made by `-race` itself: if a converted read path writes to shared memory, or
// if the RLock/Lock ordering deadlocks, this file fails. The one thing they do
// assert is that the whole fan-out completes inside a deadline, which is what
// catches a lock-ordering deadlock rather than letting the test binary hang
// until the package timeout with no useful output.

import (
	"sync"
	"testing"
	"time"
)

// runConcurrently starts readers+writers goroutines, runs them to completion,
// and fails if they have not finished within timeout. A deadlock introduced by
// an RLock-then-RLock reentrancy, or by inverting the a.mu -> persistMu order,
// shows up here as a timeout with a named culprit rather than as a hung binary.
func runConcurrently(t *testing.T, timeout time.Duration, work ...func(iteration int)) {
	t.Helper()
	const iterations = 40
	var wg sync.WaitGroup
	start := make(chan struct{})
	for _, fn := range work {
		wg.Add(1)
		go func(fn func(int)) {
			defer wg.Done()
			<-start
			for i := range iterations {
				fn(i)
			}
		}(fn)
	}
	close(start)
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(timeout):
		t.Fatalf("concurrent workers did not finish within %s — suspect a lock-ordering deadlock", timeout)
	}
}

// TestConcurrentReadPathsAgainstWriters hammers every call site converted to
// RLock while writers mutate exactly the state those call sites read:
// Preferences (proxy prefs, SSL session cache, OAuth2 browser choice, last
// import directory) and per-collection proxy / client-certificate config.
//
// Under -race, a write performed beneath RLock by any of the read paths is
// reported as a data race against either a concurrent reader or a concurrent
// writer. Without the writers this test would still pass with a broken
// conversion, because concurrent identical writes to the same word are only
// reported when the detector sees at least one of them.
func TestConcurrentReadPathsAgainstWriters(t *testing.T) {
	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if len(state.Workspaces) == 0 || len(state.Workspaces[0].Collections) == 0 {
		t.Fatalf("fixture app has no collection to exercise")
	}
	collectionID := state.Workspaces[0].Collections[0].ID
	basePreferences := state.Preferences

	readers := []func(int){
		// app.go — hot path, called once per outbound request.
		func(int) { _ = app.collectionProxyResolution(collectionID) },
		func(int) { _, _, _ = app.collectionClientCertificateConfig(collectionID) },
		func(int) { _ = app.appTLSSettingsSnapshot() },
		func(int) { _ = app.oauth2ShouldUseSystemBrowser() },
		// Second reader on each of the two double-checked fields, so the
		// fast path and the slow path of appTLSSettingsSnapshot can be taken
		// simultaneously by different goroutines.
		func(int) { _ = app.appTLSSettingsSnapshot() },
		// collection_import.go
		func(int) { _ = app.collectionImportDefaultDirectory() },
		// workspace_window_runtime.go — the registry file need not exist;
		// the error path is as interesting as the success path here.
		func(int) { _, _ = app.ListWorkspaceWindowTargets() },
		// GetState still holds the write lock (ensureReadyLocked mutates), but
		// it must keep interleaving correctly with the RLock holders.
		func(int) { _, _ = app.GetState() },
	}

	writers := []func(int){
		func(i int) {
			next := basePreferences
			next.Cache.SSLSession.Enabled = i%2 == 0
			next.OAuth2UseSystemBrowser = i%3 == 0
			_, _ = app.UpdatePreferences(next)
		},
		func(int) { _, _ = app.ClearSSLSessionCache() },
		func(i int) {
			_, _ = app.UpdateCollectionProxy(collectionID, ProxyConfig{
				Protocol: "http",
				Hostname: "127.0.0.1",
				Port:     "8080",
				Disabled: i%2 == 0,
			})
		},
		func(i int) {
			certs := []ClientCertificateConfig{}
			if i%2 == 0 {
				certs = append(certs, ClientCertificateConfig{Domain: "example.test", Type: "cert", CertFilePath: "/tmp/c.pem"})
			}
			_, _ = app.UpdateCollectionClientCertificates(collectionID, certs)
		},
	}

	runConcurrently(t, 60*time.Second, append(readers, writers...)...)
}

// TestConcurrentTLSSnapshotDoubleCheckedLocking targets the one place where a
// read path was allowed a write: appTLSSettingsSnapshot lazily builds the
// shared TLS session cache. The fast path reads a.tlsSessionCache under RLock;
// the slow path re-acquires as a writer and re-tests before assigning. This
// test keeps the cache oscillating between nil and non-nil so that the slow
// path is entered repeatedly and concurrently, which is the only way to observe
// a missing re-check or a torn read of the interface value.
func TestConcurrentTLSSnapshotDoubleCheckedLocking(t *testing.T) {
	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	preferences := state.Preferences
	preferences.Cache.SSLSession.Enabled = true
	if _, err := app.UpdatePreferences(preferences); err != nil {
		t.Fatalf("UpdatePreferences: %v", err)
	}

	snapshot := func(int) {
		settings := app.appTLSSettingsSnapshot()
		// Touch the returned cache so a half-published tls.ClientSessionCache
		// would be dereferenced rather than merely copied.
		if settings.ClientSessionCache != nil {
			settings.ClientSessionCache.Put("", nil)
		}
	}
	clear := func(int) { _, _ = app.ClearSSLSessionCache() }

	runConcurrently(t, 60*time.Second,
		snapshot, snapshot, snapshot, snapshot, snapshot, snapshot,
		clear, clear,
	)
}

// TestConcurrentGetStateAgainstWriters pins the decision to leave GetState on
// the write lock. ensureReadyLocked mutates on every call — it reassigns
// a.state.Cookies via pruneExpiredCookiesLocked and rewrites
// collection.NotFoundLocally via refreshGitCollectionAvailabilityLocked even
// when nothing has changed — so N concurrent GetState calls under RLock would
// be N concurrent writers to the same memory. If someone later converts
// GetState to RLock without first making ensureReadyLocked side-effect free,
// this test reports the race.
func TestConcurrentGetStateAgainstWriters(t *testing.T) {
	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	collectionID := state.Workspaces[0].Collections[0].ID

	read := func(int) {
		got, err := app.GetState()
		if err != nil {
			return
		}
		// Read through the slices the returned AppState aliases. This is the
		// aliasing hazard documented in the story: the returned value shares
		// backing arrays with live state, so a writer resizing them while this
		// loop runs is exactly what -race must be given a chance to see.
		for _, ws := range got.Workspaces {
			_ = len(ws.Collections)
		}
		_ = len(got.OpenTabs)
		_ = len(got.Cookies)
		_ = len(got.NetworkLog)
	}
	write := func(i int) {
		if i%2 == 0 {
			_, _ = app.CreateRequest(collectionID, "http", "concurrent")
			return
		}
		_, _ = app.SaveCookie(CookieInput{Domain: "example.test", Name: "a", Value: "b", Path: "/"})
	}

	runConcurrently(t, 60*time.Second, read, read, read, read, write, write)
}
