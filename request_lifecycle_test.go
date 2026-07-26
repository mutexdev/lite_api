package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func configureLifecycleHTTPRequest(t *testing.T, app *App, itemID, targetURL string) (Collection, RequestItem) {
	t.Helper()
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	method := http.MethodGet
	if _, err := app.UpdateRequest(collection.ID, itemID, RequestPatch{Method: &method, URL: &targetURL}); err != nil {
		t.Fatal(err)
	}
	state, err = app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection = state.Workspaces[0].Collections[0]
	item, ok := findItemInState(state, collection.ID, itemID)
	if !ok {
		t.Fatalf("request %q was not found", itemID)
	}
	return collection, item
}

func responseForLifecycleRequest(t *testing.T, state AppState, collectionID, requestID string) Response {
	t.Helper()
	item, ok := findItemInState(state, collectionID, requestID)
	if !ok || item.Response == nil {
		t.Fatalf("request response was not recorded: %#v", item)
	}
	return *item.Response
}

func TestCancelRequestCancelsInFlightHTTPTransport(t *testing.T) {
	started := make(chan struct{})
	releaseServer := make(chan struct{})
	serverFinished := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-releaseServer
		close(serverFinished)
	}))
	defer func() {
		select {
		case <-releaseServer:
		default:
			close(releaseServer)
		}
		server.Close()
	}()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection, item := configureLifecycleHTTPRequest(t, app, state.Workspaces[0].Collections[0].Items[0].ID, server.URL)

	type sendResult struct {
		state AppState
		err   error
	}
	completed := make(chan sendResult, 1)
	go func() {
		result, sendErr := app.SendRequest(collection.ID, item.ID, "")
		completed <- sendResult{state: result, err: sendErr}
	}()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("HTTP request did not reach the delayed server")
	}
	if !app.CancelRequest(collection.ID, item.ID) {
		t.Fatal("CancelRequest did not report the active request")
	}
	if app.CancelRequest(collection.ID, item.ID) {
		t.Fatal("second CancelRequest should report an already-cancelled request")
	}

	select {
	case result := <-completed:
		if result.err != nil {
			t.Fatalf("SendRequest returned an error: %v", result.err)
		}
		response := responseForLifecycleRequest(t, result.state, collection.ID, item.ID)
		if !response.Cancelled || response.StatusText != "Cancelled" || response.Error != "request cancelled" {
			t.Fatalf("cancelled response was not explicit: %#v", response)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("HTTP request did not cancel promptly")
	}

	if app.requestLifecycle().activeCount() != 0 {
		t.Fatal("completed HTTP request remained in the cancellation registry")
	}
	close(releaseServer)
	select {
	case <-serverFinished:
	case <-time.After(2 * time.Second):
		t.Fatal("test server did not finish after its bounded release")
	}
}

func TestCancelRequestDuringPreRequestScriptPreventsMainTransport(t *testing.T) {
	transportStarted := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case transportStarted <- struct{}{}:
		default:
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection, item := configureLifecycleHTTPRequest(t, app, state.Workspaces[0].Collections[0].Items[0].ID, server.URL)
	preScript := "await bru.sleep(500);"
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{PreScript: &preScript}); err != nil {
		t.Fatal(err)
	}

	type sendResult struct {
		state AppState
		err   error
	}
	completed := make(chan sendResult, 1)
	go func() {
		result, sendErr := app.SendRequest(collection.ID, item.ID, "")
		completed <- sendResult{state: result, err: sendErr}
	}()

	deadline := time.After(2 * time.Second)
	for app.requestLifecycle().activeCount() != 1 {
		select {
		case <-deadline:
			t.Fatal("request did not become cancellable before its pre-request script")
		case <-time.After(time.Millisecond):
		}
	}
	// The script is awaiting bru.sleep, so this cancellation occurs before the
	// main transport can begin. The runtime is deliberately not interruptible;
	// the send observes cancellation when the script returns.
	time.Sleep(25 * time.Millisecond)
	if !app.CancelRequest(collection.ID, item.ID) {
		t.Fatal("CancelRequest did not accept cancellation during the pre-request script")
	}

	select {
	case result := <-completed:
		if result.err != nil {
			t.Fatalf("SendRequest returned an error: %v", result.err)
		}
		response := responseForLifecycleRequest(t, result.state, collection.ID, item.ID)
		if !response.Cancelled || response.StatusText != "Cancelled" || response.Error != "request cancelled" {
			t.Fatalf("pre-request cancellation was not explicit: %#v", response)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("SendRequest did not complete after its pre-request script returned")
	}

	select {
	case <-transportStarted:
		t.Fatal("main HTTP transport started after cancellation during the pre-request script")
	default:
	}
	if app.requestLifecycle().activeCount() != 0 {
		t.Fatal("cancelled request remained in the cancellation registry")
	}
}

func TestCancelCollectionRunCancelsCurrentHTTPTransport(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release
	}))
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
		server.Close()
	}()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection, item := configureLifecycleHTTPRequest(t, app, state.Workspaces[0].Collections[0].Items[0].ID, server.URL)
	completed := make(chan struct {
		state AppState
		err   error
	}, 1)
	go func() {
		result, runErr := app.RunCollection(collection.ID, "")
		completed <- struct {
			state AppState
			err   error
		}{state: result, err: runErr}
	}()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("runner did not start the HTTP request")
	}
	if !app.CancelCollectionRun(collection.ID) {
		t.Fatal("CancelCollectionRun did not acknowledge the active run")
	}
	if app.CancelCollectionRun(collection.ID) {
		t.Fatal("repeated CancelCollectionRun should not re-acknowledge cancellation")
	}

	select {
	case result := <-completed:
		if result.err != nil {
			t.Fatalf("RunCollection returned an error: %v", result.err)
		}
		if result.state.Runner.Cancelled != 1 || result.state.Runner.Failed != 0 || len(result.state.Runner.Results) != 1 {
			t.Fatalf("runner did not record explicit cancellation: %#v", result.state.Runner)
		}
		if got := result.state.Runner.Results[0]; got.Status != "cancelled" || got.ItemID != item.ID {
			t.Fatalf("runner recorded the wrong cancelled result: %#v", got)
		}
		response := responseForLifecycleRequest(t, result.state, collection.ID, item.ID)
		if !response.Cancelled || response.StatusText != "Cancelled" {
			t.Fatalf("child HTTP response was not cancelled: %#v", response)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("collection run did not cancel the active HTTP request")
	}
}

func TestCancelCollectionRunDuringDelaySkipsNextTransport(t *testing.T) {
	firstServed := make(chan struct{})
	secondStarted := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/first":
			close(firstServed)
			w.WriteHeader(http.StatusNoContent)
		case "/second":
			select {
			case secondStarted <- struct{}{}:
			default:
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	firstID := collection.Items[0].ID
	collection, first := configureLifecycleHTTPRequest(t, app, firstID, server.URL+"/first")
	state, err = app.CreateRequest(collection.ID, "http", "Second")
	if err != nil {
		t.Fatal(err)
	}
	collection = state.Workspaces[0].Collections[0]
	secondID := collection.Items[len(collection.Items)-1].ID
	collection, second := configureLifecycleHTTPRequest(t, app, secondID, server.URL+"/second")

	completed := make(chan struct {
		state AppState
		err   error
	}, 1)
	go func() {
		result, runErr := app.RunCollectionWithOptions(collection.ID, "", RunnerOptions{DelayMs: int(time.Second.Milliseconds())})
		completed <- struct {
			state AppState
			err   error
		}{state: result, err: runErr}
	}()
	select {
	case <-firstServed:
	case <-time.After(2 * time.Second):
		t.Fatal("runner did not complete its first request")
	}
	// The delay is one second; this gives the first request time to return and
	// places the runner in its cancellation-aware wait before the next send.
	time.Sleep(100 * time.Millisecond)
	if !app.CancelCollectionRun(collection.ID) {
		t.Fatal("CancelCollectionRun did not acknowledge the run during delay")
	}

	select {
	case result := <-completed:
		if result.err != nil {
			t.Fatalf("RunCollectionWithOptions returned an error: %v", result.err)
		}
		if result.state.Runner.Passed != 1 || result.state.Runner.Cancelled != 1 || result.state.Runner.Failed != 0 {
			t.Fatalf("runner delay cancellation counts were wrong: %#v", result.state.Runner)
		}
		if len(result.state.Runner.Results) != 2 || result.state.Runner.Results[0].ItemID != first.ID || result.state.Runner.Results[1].ItemID != second.ID || result.state.Runner.Results[1].Status != "cancelled" {
			t.Fatalf("runner did not record the pending request as cancelled: %#v", result.state.Runner.Results)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("runner did not leave its delay promptly after cancellation")
	}
	select {
	case <-secondStarted:
		t.Fatal("runner started a transport after cancellation during its delay")
	default:
	}
}

func TestCancelRequestDoesNotCancelAnotherRequest(t *testing.T) {
	firstStarted := make(chan struct{})
	secondStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	releaseSecond := make(chan struct{})
	firstFinished := make(chan struct{})
	secondFinished := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/first":
			close(firstStarted)
			<-releaseFirst
			close(firstFinished)
		case "/second":
			close(secondStarted)
			<-releaseSecond
			w.WriteHeader(http.StatusNoContent)
			close(secondFinished)
		default:
			http.NotFound(w, r)
		}
	}))
	defer func() {
		for _, release := range []chan struct{}{releaseFirst, releaseSecond} {
			select {
			case <-release:
			default:
				close(release)
			}
		}
		server.Close()
	}()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	firstID := collection.Items[0].ID
	collection, first := configureLifecycleHTTPRequest(t, app, firstID, server.URL+"/first")

	state, err = app.CreateRequest(collection.ID, "http", "Second")
	if err != nil {
		t.Fatal(err)
	}
	collection = state.Workspaces[0].Collections[0]
	secondID := collection.Items[len(collection.Items)-1].ID
	collection, second := configureLifecycleHTTPRequest(t, app, secondID, server.URL+"/second")

	type sendResult struct {
		state AppState
		err   error
	}
	firstResult := make(chan sendResult, 1)
	secondResult := make(chan sendResult, 1)
	go func() {
		result, sendErr := app.SendRequest(collection.ID, first.ID, "")
		firstResult <- sendResult{state: result, err: sendErr}
	}()
	go func() {
		result, sendErr := app.SendRequest(collection.ID, second.ID, "")
		secondResult <- sendResult{state: result, err: sendErr}
	}()

	for _, started := range []<-chan struct{}{firstStarted, secondStarted} {
		select {
		case <-started:
		case <-time.After(2 * time.Second):
			t.Fatal("expected request did not reach the server")
		}
	}
	if !app.CancelRequest(collection.ID, first.ID) {
		t.Fatal("CancelRequest did not find the first request")
	}

	select {
	case result := <-firstResult:
		if result.err != nil {
			t.Fatalf("first SendRequest returned an error: %v", result.err)
		}
		if response := responseForLifecycleRequest(t, result.state, collection.ID, first.ID); !response.Cancelled {
			t.Fatalf("first request was not marked cancelled: %#v", response)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("first request did not cancel promptly")
	}
	close(releaseFirst)
	select {
	case <-firstFinished:
	case <-time.After(2 * time.Second):
		t.Fatal("first test server handler did not finish after release")
	}

	close(releaseSecond)
	select {
	case result := <-secondResult:
		if result.err != nil {
			t.Fatalf("second SendRequest returned an error: %v", result.err)
		}
		response := responseForLifecycleRequest(t, result.state, collection.ID, second.ID)
		if response.Cancelled || response.Status != http.StatusNoContent {
			t.Fatalf("cancelling the first request affected the second: %#v", response)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("second request did not complete")
	}
	select {
	case <-secondFinished:
	case <-time.After(2 * time.Second):
		t.Fatal("second test server handler did not finish")
	}
}

func TestRequestLifecycleRegistryCleansUpWithoutRemovingNewerExecution(t *testing.T) {
	registry := newRequestLifecycleRegistry()
	firstContext, finishFirst := registry.start("collection", "request")
	secondContext, finishSecond := registry.start("collection", "request")

	select {
	case <-firstContext.Done():
	case <-time.After(time.Second):
		t.Fatal("newer execution did not supersede the older one")
	}
	finishFirst()
	if got := registry.activeCount(); got != 1 {
		t.Fatalf("older cleanup removed the newer execution: %d active", got)
	}
	if !registry.cancel("collection", "request") {
		t.Fatal("registry did not cancel the active execution")
	}
	select {
	case <-secondContext.Done():
	case <-time.After(time.Second):
		t.Fatal("active execution did not receive cancellation")
	}
	if registry.cancel("collection", "request") {
		t.Fatal("repeated registry cancellation should be idempotent")
	}
	finishSecond()
	if got := registry.activeCount(); got != 0 {
		t.Fatalf("registry did not clean up: %d active", got)
	}
}

func TestRequestLifecycleFinishAndCancelAcknowledgeOnlyOneWinner(t *testing.T) {
	registry := newRequestLifecycleRegistry()

	finishedContext, finish := registry.start("collection", "finished")
	if finish() {
		t.Fatal("finish reported cancellation that had not happened")
	}
	if registry.cancel("collection", "finished") {
		t.Fatal("CancelRequest acknowledged an execution after finish won")
	}
	if requestContextCancelled(finishedContext) {
		t.Fatal("finish cancelled an execution")
	}

	cancelledContext, finishCancelled := registry.start("collection", "cancelled")
	if !registry.cancel("collection", "cancelled") {
		t.Fatal("cancel did not acknowledge an active execution")
	}
	if !finishCancelled() {
		t.Fatal("finish did not report that cancellation won")
	}
	select {
	case <-cancelledContext.Done():
	case <-time.After(time.Second):
		t.Fatal("cancelled execution context was not cancelled")
	}
}

func TestRequestLifecycleFinishAndCancelRaceAcknowledgeConsistently(t *testing.T) {
	for attempt := 0; attempt < 100; attempt++ {
		registry := newRequestLifecycleRegistry()
		ctx, finish := registry.start("collection", "request")
		finished := make(chan bool, 1)
		go func() {
			finished <- finish()
		}()
		cancelled := registry.cancel("collection", "request")
		if finishCancelled := <-finished; finishCancelled != cancelled {
			t.Fatalf("attempt %d acknowledged cancellation inconsistently: finish=%t cancel=%t", attempt, finishCancelled, cancelled)
		}
		if requestContextCancelled(ctx) != cancelled {
			t.Fatalf("attempt %d context cancellation did not match acknowledgement", attempt)
		}
		if registry.cancel("collection", "request") {
			t.Fatalf("attempt %d left a completed execution cancellable", attempt)
		}
	}
}

func TestCollectionRunLifecycleFinishAndCancelRaceAcknowledgeConsistently(t *testing.T) {
	for attempt := 0; attempt < 100; attempt++ {
		registry := newCollectionRunLifecycleRegistry()
		ctx, finish := registry.start("collection")
		finished := make(chan bool, 1)
		go func() {
			finished <- finish()
		}()
		cancelled := registry.cancel("collection")
		if finishCancelled := <-finished; finishCancelled != cancelled {
			t.Fatalf("attempt %d acknowledged collection-run cancellation inconsistently: finish=%t cancel=%t", attempt, finishCancelled, cancelled)
		}
		if requestContextCancelled(ctx) != cancelled {
			t.Fatalf("attempt %d collection-run context cancellation did not match acknowledgement", attempt)
		}
		if registry.cancel("collection") {
			t.Fatalf("attempt %d left a completed collection run cancellable", attempt)
		}
	}
}

func TestSendRequestWithoutCancellationStillSucceeds(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection, item := configureLifecycleHTTPRequest(t, app, state.Workspaces[0].Collections[0].Items[0].ID, server.URL)
	state, err = app.SendRequest(collection.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	response := responseForLifecycleRequest(t, state, collection.ID, item.ID)
	if response.Cancelled || response.Status != http.StatusAccepted || response.Error != "" {
		t.Fatalf("normal response changed: %#v", response)
	}
	if app.CancelRequest(collection.ID, item.ID) {
		t.Fatal("completed request remained cancellable")
	}
}
