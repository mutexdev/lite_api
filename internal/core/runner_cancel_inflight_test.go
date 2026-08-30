package core

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestCancelCollectionRunAbortsSlowInFlightRequest pins the property the
// existing cancellation tests only imply: cancelling a run partway through a
// request the server has not answered yet must ABORT that request, not merely
// stop waiting for it.
//
// TestCancelCollectionRunCancelsCurrentHTTPTransport uses a handler that blocks
// forever, which cannot distinguish "the client hung up" from "the client is
// still waiting". This one lets the handler finish on its own after a delay and
// asserts the server observed the disconnect first.
//
// THE HANDLER MUST DRAIN r.Body BEFORE IT WAITS. net/http only starts the
// background read that detects a client disconnect once the request body has
// been consumed (it registers it on the body's EOF), and the sample request
// this fixture uses sends a JSON body. A handler that waits without reading the
// body never has its request context cancelled, and reports a perfectly
// cancelled request as one that ran to completion — which is what makes this
// property easy to measure wrongly.
func TestCancelCollectionRunAbortsSlowInFlightRequest(t *testing.T) {
	const serverDelay = 3 * time.Second

	started := make(chan struct{})
	clientGone := make(chan bool, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		close(started)
		select {
		case <-r.Context().Done():
			clientGone <- true
		case <-time.After(serverDelay):
			clientGone <- false
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer server.Close()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection, item := configureLifecycleHTTPRequest(t, app, state.Workspaces[0].Collections[0].Items[0].ID, server.URL)

	completed := make(chan AppState, 1)
	go func() {
		result, runErr := app.RunCollection(collection.ID, "")
		if runErr != nil {
			t.Errorf("RunCollection returned an error: %v", runErr)
		}
		completed <- result
	}()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("runner did not start the HTTP request")
	}
	cancelledAt := time.Now()
	if !app.CancelCollectionRun(collection.ID) {
		t.Fatal("CancelCollectionRun did not acknowledge the active run")
	}

	select {
	case result := <-completed:
		if elapsed := time.Since(cancelledAt); elapsed >= serverDelay {
			t.Fatalf("the run outlived the server delay (%s): the in-flight request was not aborted", elapsed)
		}
		if result.Runner.Cancelled != 1 {
			t.Fatalf("runner did not record the cancellation: %#v", result.Runner)
		}
		response := responseForLifecycleRequest(t, result, collection.ID, item.ID)
		if !response.Cancelled {
			t.Fatalf("the in-flight response was not marked cancelled: %#v", response)
		}
	case <-time.After(serverDelay):
		t.Fatal("collection run did not return after cancellation")
	}

	select {
	case aborted := <-clientGone:
		if !aborted {
			t.Fatal("the server served the request to completion: cancellation never reached the in-flight HTTP request")
		}
	case <-time.After(serverDelay + time.Second):
		t.Fatal("the test server never finished")
	}
}

// TestCancelRequestAbortsSlowInFlightRequest is the same property for a single
// send rather than a collection run. See the note above about draining r.Body.
func TestCancelRequestAbortsSlowInFlightRequest(t *testing.T) {
	const serverDelay = 3 * time.Second

	started := make(chan struct{})
	clientGone := make(chan bool, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		close(started)
		select {
		case <-r.Context().Done():
			clientGone <- true
		case <-time.After(serverDelay):
			clientGone <- false
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer server.Close()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection, item := configureLifecycleHTTPRequest(t, app, state.Workspaces[0].Collections[0].Items[0].ID, server.URL)

	completed := make(chan AppState, 1)
	go func() {
		result, sendErr := app.SendRequest(collection.ID, item.ID, "")
		if sendErr != nil {
			t.Errorf("SendRequest returned an error: %v", sendErr)
		}
		completed <- result
	}()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("the HTTP request did not reach the server")
	}
	cancelledAt := time.Now()
	if !app.CancelRequest(collection.ID, item.ID) {
		t.Fatal("CancelRequest did not acknowledge the active send")
	}

	select {
	case result := <-completed:
		if elapsed := time.Since(cancelledAt); elapsed >= serverDelay {
			t.Fatalf("the send outlived the server delay (%s): the in-flight request was not aborted", elapsed)
		}
		response := responseForLifecycleRequest(t, result, collection.ID, item.ID)
		if !response.Cancelled {
			t.Fatalf("the in-flight response was not marked cancelled: %#v", response)
		}
	case <-time.After(serverDelay):
		t.Fatal("the send did not return after cancellation")
	}

	select {
	case aborted := <-clientGone:
		if !aborted {
			t.Fatal("the server served the request to completion: cancellation never reached the in-flight HTTP request")
		}
	case <-time.After(serverDelay + time.Second):
		t.Fatal("the test server never finished")
	}
}
