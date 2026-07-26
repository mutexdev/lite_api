package main

import (
	"LiteAPI/internal/cookiejar"
	"context"
	"sync"
	"time"
)

// requestLifecycleRegistry tracks active top-level HTTP and GraphQL sends,
// including their request scripts. CancelRequest cannot interrupt a running
// JavaScript script yet, but it is accepted while that script is running. The
// send checks the context when the script returns, suppresses any main
// transport that has not started, and reports an explicit cancelled response.
type requestLifecycleRegistry struct {
	mu     sync.Mutex
	active map[requestLifecycleKey]*requestLifecycleEntry
}

type requestLifecycleKey struct {
	collectionID string
	requestID    string
}

type requestLifecycleEntry struct {
	cancel    context.CancelFunc
	cancelled bool
}

func newRequestLifecycleRegistry() *requestLifecycleRegistry {
	return &requestLifecycleRegistry{active: map[requestLifecycleKey]*requestLifecycleEntry{}}
}

func (r *requestLifecycleRegistry) start(collectionID, requestID string) (context.Context, func() bool) {
	return r.startWithParent(context.Background(), collectionID, requestID)
}

func (r *requestLifecycleRegistry) startWithParent(parent context.Context, collectionID, requestID string) (context.Context, func() bool) {
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	key := requestLifecycleKey{collectionID: collectionID, requestID: requestID}
	entry := &requestLifecycleEntry{cancel: cancel}

	r.mu.Lock()
	previous := r.active[key]
	r.active[key] = entry
	r.mu.Unlock()

	// A newer execution of the same request supersedes the older one. The
	// identity check in finish prevents the older execution from removing this
	// newer registry entry when it returns.
	if previous != nil {
		previous.cancel()
	}

	return ctx, func() bool {
		return r.finish(key, entry)
	}
}

func (r *requestLifecycleRegistry) cancel(collectionID, requestID string) bool {
	key := requestLifecycleKey{collectionID: collectionID, requestID: requestID}

	r.mu.Lock()
	entry := r.active[key]
	if entry == nil || entry.cancelled {
		r.mu.Unlock()
		return false
	}
	entry.cancelled = true
	cancel := entry.cancel
	r.mu.Unlock()

	cancel()
	return true
}

// finish atomically removes an execution from the registry and reports whether
// cancellation had already won for that execution. Once it returns, a later
// cancel call cannot acknowledge a cancellation for this completed execution.
func (r *requestLifecycleRegistry) finish(key requestLifecycleKey, entry *requestLifecycleEntry) bool {
	r.mu.Lock()
	cancelled := entry.cancelled
	if r.active[key] == entry {
		delete(r.active, key)
	}
	r.mu.Unlock()
	return cancelled
}

func (r *requestLifecycleRegistry) activeCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.active)
}

func (a *App) requestLifecycle() *requestLifecycleRegistry {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.requests == nil {
		a.requests = newRequestLifecycleRegistry()
	}
	return a.requests
}

func (a *App) startCancellableRequest(collectionID, requestID, requestType string) (context.Context, func() bool) {
	return a.startCancellableRequestWithParent(context.Background(), collectionID, requestID, requestType)
}

func (a *App) startCancellableRequestWithParent(parent context.Context, collectionID, requestID, requestType string) (context.Context, func() bool) {
	if requestType != "http" && requestType != "graphql" {
		return parent, func() bool { return false }
	}
	return a.requestLifecycle().startWithParent(parent, collectionID, requestID)
}

// CancelRequest asks the active top-level HTTP or GraphQL send for this
// collection/request pair to stop. It returns true only when it transitioned
// an active send from running to cancellation-requested; false means there was
// no active send (or it had already been cancelled). A currently executing
// JavaScript script completes before the cancellation can take effect.
func (a *App) CancelRequest(collectionID, requestID string) bool {
	return a.requestLifecycle().cancel(collectionID, requestID)
}

func requestContextCancelled(ctx context.Context) bool {
	return ctx != nil && ctx.Err() == context.Canceled
}

func markRequestCancelled(response *Response) {
	if response == nil {
		return
	}
	response.Cancelled = true
	response.StatusText = "Cancelled"
	response.Error = "request cancelled"
}

func cancelledRequestResponse(item RequestItem, vars map[string]string) Response {
	response := Response{
		SentAt:       time.Now(),
		RequestedURL: cookiejar.PreviewRequestURL(item, vars),
		Headers:      map[string]string{},
		PreviewMode:  "raw",
	}
	markRequestCancelled(&response)
	return response
}

type collectionRunLifecycleRegistry struct {
	mu     sync.Mutex
	active map[string]*collectionRunLifecycleEntry
}

type collectionRunLifecycleEntry struct {
	cancel    context.CancelFunc
	cancelled bool
}

func newCollectionRunLifecycleRegistry() *collectionRunLifecycleRegistry {
	return &collectionRunLifecycleRegistry{active: map[string]*collectionRunLifecycleEntry{}}
}

func (r *collectionRunLifecycleRegistry) start(collectionID string) (context.Context, func() bool) {
	ctx, cancel := context.WithCancel(context.Background())
	entry := &collectionRunLifecycleEntry{cancel: cancel}

	r.mu.Lock()
	previous := r.active[collectionID]
	r.active[collectionID] = entry
	r.mu.Unlock()
	if previous != nil {
		previous.cancel()
	}

	return ctx, func() bool {
		r.mu.Lock()
		cancelled := entry.cancelled
		if r.active[collectionID] == entry {
			delete(r.active, collectionID)
		}
		r.mu.Unlock()
		return cancelled
	}
}

func (r *collectionRunLifecycleRegistry) cancel(collectionID string) bool {
	r.mu.Lock()
	entry := r.active[collectionID]
	if entry == nil || entry.cancelled {
		r.mu.Unlock()
		return false
	}
	entry.cancelled = true
	cancel := entry.cancel
	r.mu.Unlock()
	cancel()
	return true
}

func (a *App) collectionRunLifecycle() *collectionRunLifecycleRegistry {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.collectionRuns == nil {
		a.collectionRuns = newCollectionRunLifecycleRegistry()
	}
	return a.collectionRuns
}

func (a *App) startCancellableCollectionRun(collectionID string) (context.Context, func() bool) {
	return a.collectionRunLifecycle().start(collectionID)
}

// CancelCollectionRun asks the active collection runner to stop. It returns
// true only when it transitions that collection's currently active run to
// cancellation-requested. Once the runner commits its final snapshot it first
// removes the lifecycle entry, so later calls return false.
func (a *App) CancelCollectionRun(collectionID string) bool {
	return a.collectionRunLifecycle().cancel(collectionID)
}
