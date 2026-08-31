package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/mutexdev/lite_api/internal/cookiejar"
	"github.com/mutexdev/lite_api/internal/envsecrets"
	"github.com/mutexdev/lite_api/internal/grpcexec"
	"github.com/mutexdev/lite_api/internal/mcpserver"
	"github.com/mutexdev/lite_api/internal/prefs"
	"github.com/mutexdev/lite_api/internal/responsestore"
	"github.com/mutexdev/lite_api/internal/runner"
	"github.com/mutexdev/lite_api/internal/scripting"
)

func (a *App) SendRequest(collectionID, itemID, environmentID string) (AppState, error) {
	state, _, err := a.sendRequestWithControls(collectionID, itemID, environmentID, nil)
	return state, err
}

func (a *App) SendRequestWithPromptValues(collectionID, itemID, environmentID string, promptValues map[string]string) (AppState, error) {
	state, _, err := a.sendRequestWithControls(collectionID, itemID, environmentID, promptValues)
	return state, err
}

// sendRequestWithControls is a UI send: the user pressed Send. §1.2(4).
func (a *App) sendRequestWithControls(collectionID, itemID, environmentID string, promptValues map[string]string) (AppState, scripting.Controls, error) {
	state, controls, _, err := a.sendRequestWithControlsContextProvenance(context.Background(), uiSendProvenance(), collectionID, itemID, environmentID, promptValues, nil, runner.Iteration{})
	return state, controls, err
}

// sendRequestWithControlsContextProvenance is the send path's root, and the
// ONLY way into it. A migration delegate under the old name
// (sendRequestWithControlsContext) carried unmigrated callers for one wave and
// was deleted once a grep proved it had none.
//
// PROVENANCE IS AN ARGUMENT, NOT AN INFERENCE (§4.5). It used to be derived from
// the context — a policy meant "MCP", its absence meant "UI" — which made the
// two indistinguishable at exactly the moment they differ most: a new engine
// path that forgot to attach a policy was silently reclassified as a user's own
// send and skipped every refusal and every checkpoint. Now the caller that
// knows says so, in a type with two constructors, and a caller that says nothing
// produces the zero value, which is refused below before any work happens.
//
// The context is then stamped from that argument, so the guard transport, the
// checkpoints, the script shims and a nested bru.runRequest all read the same
// answer this root was handed.
//
// It resolves collectionID/itemID twice: once on entry, and again on the tail
// because a.mu is released across the network I/O and the request may have moved
// or gone in that window. index (US-024) is an optional per-run lookup hint that
// makes both resolutions O(1); it is verified against live state on every use,
// and nil restores the plain linear scans.
//
// The fourth return value is the *Response this call stored on the item. The
// collection runner used to re-find the item in the returned state purely to
// read it back, which was another linear scan per request.
func (a *App) sendRequestWithControlsContextProvenance(parent context.Context, prov sendProvenance, collectionID, itemID, environmentID string, promptValues map[string]string, index *runnerLookupIndex, iteration runner.Iteration) (AppState, scripting.Controls, *Response, error) {
	controls := scripting.Controls{}
	// BEFORE THE LOCK, BEFORE ANYTHING. An unprovenanced send does not get to
	// resolve a collection, let alone reach a network.
	if err := mcpRequireSendProvenance(prov, "the send path"); err != nil {
		return AppState{}, controls, nil, err
	}
	parent = mcpContextWithSendProvenance(parent, prov)
	a.mu.Lock()
	if err := a.ensureReadyLocked(); err != nil {
		a.mu.Unlock()
		return AppState{}, controls, nil, err
	}
	ws, collection, err := a.findCollectionWithWorkspaceIndexedLocked(index, collectionID)
	if err != nil {
		a.mu.Unlock()
		return AppState{}, controls, nil, err
	}
	item, err := index.findItemIndexed(collectionID, collection, itemID)
	if err != nil {
		a.mu.Unlock()
		return AppState{}, controls, nil, err
	}
	collectionCopy := *collection
	requestCopy := scripting.EffectiveRequest(collectionCopy, *item)
	// THE ONE POLICY THIS SEND ANSWERS TO, taken from the provenance this call
	// was given. nil means a UI send, and every branch below reads as
	// "unchanged" when it is nil (§1.2(4)).
	mcpPolicy := prov.policy
	// §7. The secret values an agent-visible history projection will have to be
	// masked against, hydrated HERE — under the first lock, which is already
	// held — because the tail records history with a.mu held and the hydrator
	// takes that same lock. Reading them at the tail would deadlock the send.
	var mcpMaskValues []string
	if mcpPolicy != nil {
		mcpMaskValues = mcpSecretValuesLocked(&a.state)
	}
	scriptVariables := scripting.NewScriptVariableContext(scripting.ActiveGlobalEnvironmentsForWorkspace(*ws), collection, environmentID, requestCopy, promptValues, ws.Path)
	// US-046. Applied after construction and before Combined is read, so the
	// row participates in the precedence chain rather than being pasted over
	// the result of it.
	scripting.ApplyIterationDataToContext(scriptVariables, iteration.Data)
	// §3. The execution overlay stands in for the persistence this send is
	// about to skip: a previous step's bru.setVar is laid over THIS send's
	// freshly built context, at the precedence the persisted value would have
	// had, so within-run continuity survives while nothing reaches AppState.
	//
	// APPLIED TO THE DEFINITION-SEEDED CONTEXT, never to a bare one. The
	// extraction at the tail carries a dirty scope WHOLE, matching what the
	// persisted path writes whole — so a context that was not seeded from the
	// definitions would extract a scope missing every stored variable the
	// execution did not touch, and re-applying it would silently drop them.
	if mcpPolicy != nil {
		scripting.ApplyRunOverlayToContext(scriptVariables, mcpPolicy.overlay.variableDeltas())
	}
	vars := scriptVariables.Combined
	scriptLogs := []ScriptLog{}
	scriptTimeline := []TimelineItem{}
	scriptCookieJar := scripting.NewScriptCookieJar(mcpSendCookieSeed(mcpPolicy, a.state.Cookies))
	initialCookies := scriptCookieJar.Snapshot()
	scriptRunDepth := 0
	scriptMeta := scripting.ScriptRuntimeMeta{
		CollectionName:            collection.Name,
		CollectionPath:            collection.Path,
		EnvironmentName:           scripting.SelectedEnvironmentName(collection, environmentID),
		EnvironmentID:             environmentID,
		JSSandboxMode:             collectionJSSandboxMode(*collection),
		Variables:                 scriptVariables,
		OAuth2CredentialVariables: a.oauth2CredentialVariablesSnapshot,
		ResetOAuth2Credential:     a.resetOAuth2Credential,
		IterationIndex:            iteration.Index,
		IterationCount:            iteration.Count,
	}
	scriptMeta.RecordTimeline = func(entry TimelineItem) {
		scriptTimeline = append(scriptTimeline, entry)
	}
	scripts := scripting.MergedRuntimeScripts(*collection, requestCopy)
	preferences := prefs.Normalize(a.state.Preferences)
	shouldStoreCookies := prefs.BoolPtrValue(preferences.Request.StoreCookies, true) && requestCopy.Settings.StoreCookies
	shouldSendCookies := prefs.BoolPtrValue(preferences.Request.SendCookies, true) && requestCopy.Settings.StoreCookies
	a.mu.Unlock()

	// Register before pre-request scripts start so the Wails SendRequest call
	// remains truthfully cancellable for its entire HTTP/GraphQL execution.
	// Scripts are not interruptible, so cancellation is observed at the
	// checkpoints below and prevents any transport that has not started.
	executionContext, finishExecution := a.startCancellableRequestWithParent(parent, collectionID, itemID, requestCopy.Type)
	defer finishExecution()

	// Set AFTER the execution context exists, because both fields need it. The
	// meta is copied by value into the per-phase metas below, all of which are
	// taken further down, so assigning here is the same as assigning at
	// construction — with a context to hand.
	//
	// §5 rows 11 and 12. A nested bru.runRequest inherits the parent execution's
	// context, which is how its own sends land inside the same policy and the
	// same cancellation; and the script sandbox's sendRequest/fetch/DNS shims
	// consult the authorizer before they reach the network.
	scriptMeta.RunRequest = func(target string) (Response, *TimelineItem, error) {
		return a.runScriptedCollectionRequest(executionContext, collectionID, target, environmentID, scriptVariables, scriptCookieJar, &scriptLogs, &scriptRunDepth, scriptMeta.RecordTimeline)
	}
	// THE CONTEXT IS SET FOR EVERY SEND, UI INCLUDED, and only the AUTHORIZER is
	// MCP-only. The script client is one of the three the guard transport wraps
	// (§4.3), and under strict provenance an unlabeled request through it is
	// refused — so a UI send whose script calls out has to carry the UI label,
	// which executionContext already holds because the root stamped it. Leaving
	// it nil would build the script's request on context.Background() and turn
	// every scripted fetch in the app into a refusal. The kind narrowing is
	// inert for a UI send; the authorizer, which is the part that can say no,
	// stays nil, so scripting's "permissive, unchanged" shape is preserved.
	scriptMeta.RequestContext = mcpContextWithEgressKind(executionContext, egressKindScript)
	if mcpPolicy != nil {
		scriptMeta.EgressAuthorizer = mcpScriptEgressAuthorizer(executionContext, mcpPolicy)
	}

	var response Response
	preMeta := scriptMeta
	preMeta.TimelinePhase = "pre-request"
	preState := (*scripting.RequestState)(nil)
	// The pre-request phase's own test registry. It used to have none, so a
	// pm.test there either vanished (passing) or was re-thrown as a script
	// failure that stopped the send (failing). Both are collected here and
	// prepended to the response below, in the order the user's scripts ran.
	preTestResults := []TestResult{}
	if requestContextCancelled(executionContext) {
		response = cancelledRequestResponse(requestCopy, vars)
		response.ScriptLogs = scriptLogs
	} else {
		preState, err = scripting.RunPreRequestScriptSourceMeta(scripts.PreLevels, &requestCopy, vars, scriptCookieJar, preMeta, &preTestResults, &scriptLogs)
		controls.Merge(preState)
	}
	if requestContextCancelled(executionContext) {
		response = cancelledRequestResponse(requestCopy, vars)
		response.ScriptLogs = scriptLogs
	} else if err != nil {
		response = scripting.ScriptErrorResponse("pre-request script", err)
		response.ScriptLogs = scriptLogs
	} else if preState != nil && preState.SkipRequest {
		response = scripting.ScriptSkippedResponse(requestCopy, vars)
		response.ScriptLogs = scriptLogs
	} else {
		requestURL := cookiejar.PreviewRequestURL(requestCopy, vars)
		if shouldSendCookies {
			cookiejar.AttachHeader(&requestCopy, scriptCookieJar.Snapshot(), requestURL)
		}
		response = a.executeHTTP(executionContext, collectionID, collectionCopy, requestCopy, vars, preState, scriptMeta.RecordTimeline)
		controls.Merge(preState)
		if requestContextCancelled(executionContext) {
			markRequestCancelled(&response)
			response.ScriptLogs = scriptLogs
		} else {
			scriptCookieJar.UpsertAll(response.Cookies)
			postVariablesMeta := scriptMeta
			postVariablesMeta.TimelinePhase = "post-response"
			if err := scripting.RunPostResponseVariables(scripting.EffectiveResponseVariables(collectionCopy, requestCopy), requestCopy, &response, scriptVariables, scriptCookieJar, postVariablesMeta, &scriptLogs); err != nil {
				response.TestResults = append(response.TestResults, TestResult{Name: "post-response variables", Passed: false, Message: err.Error()})
			}
			if requestContextCancelled(executionContext) {
				markRequestCancelled(&response)
				response.ScriptLogs = scriptLogs
			} else {
				postMeta := scriptMeta
				postMeta.TimelinePhase = "post-response"
				// Same registry treatment as the pre-request phase: three
				// pm.tests in a post-response script used to produce ONE row
				// (the escaped error), or none at all when they passed.
				postTestResults := []TestResult{}
				postState, err := scripting.RunPostResponseScriptSourceMeta(scripts.PostLevels, requestCopy, &response, vars, scriptCookieJar, postMeta, &postTestResults, &scriptLogs)
				controls.Merge(postState)
				response.TestResults = append(response.TestResults, postTestResults...)
				if err != nil {
					response.TestResults = append(response.TestResults, TestResult{Name: "post-response script", Passed: false, Message: err.Error()})
				}
				if requestContextCancelled(executionContext) {
					markRequestCancelled(&response)
					response.ScriptLogs = scriptLogs
				} else {
					testsMeta := scriptMeta
					testsMeta.TimelinePhase = "tests"
					testResults, testState := scripting.EvaluateRuntimeTestsSourceMeta(scripts.TestsLevels, response, requestCopy, vars, scriptCookieJar, testsMeta, &scriptLogs)
					controls.Merge(testState)
					response.TestResults = append(response.TestResults, testResults...)
					response.ScriptLogs = scriptLogs
					if requestContextCancelled(executionContext) {
						markRequestCancelled(&response)
					}
				}
			}
		}
	}

	// Pre-request tests come FIRST, whatever else happened: they ran before the
	// request did, and on the error path they are the only record that they ran
	// at all.
	if len(preTestResults) > 0 {
		response.TestResults = append(append([]TestResult{}, preTestResults...), response.TestResults...)
	}

	// Close the lifecycle before persistence so cancellation has a single,
	// truthful outcome: either it won and this response is cancelled, or the
	// registry was closed and CancelRequest reports false for this completion.
	if finishExecution() || requestContextCancelled(executionContext) {
		markRequestCancelled(&response)
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	// US-076. Re-resolve the WORKSPACE too, and use it below — do not fall back
	// to the `ws` captured before a.mu was released for the network round trip.
	//
	// ws is a *Workspace pointing into a.state.Workspaces. While the lock was
	// released, anything that appends a workspace past the slice's capacity
	// reallocates that backing array, and the captured pointer then addresses
	// memory nothing reads. scripting.ApplyScriptVariableContextToState would write the
	// script's variable changes into the dead array and report success: silent
	// corruption, not a crash, and invisible until the user notices a variable
	// that did not stick.
	//
	// This line already re-resolved the collection and discarded the workspace
	// with `_`; keeping it is the whole fix.
	liveWorkspace, collection, err := a.findCollectionWithWorkspaceIndexedLocked(index, collectionID)
	if err != nil {
		return AppState{}, controls, nil, err
	}
	item, err = index.findItemIndexed(collectionID, collection, itemID)
	if err != nil {
		return AppState{}, controls, nil, err
	}
	if mcpPolicy != nil {
		// §3, AND IT IS THE WHOLE POINT OF THE SECTION. Neither branch below
		// runs for an agent-initiated send: no cookie reaches a.state.Cookies
		// and no dirty variable scope reaches AppState or disk. That closes the
		// confirmed laundering channel at its root — a script derives a hostname
		// from agent input, persists it, and the NEXT run reads it back as
		// definition state, so it enters Base and the boundary has widened.
		//
		// What would have been written is EXTRACTED instead, into the
		// execution's overlay, where the next send in this same execution can
		// see it and where it dies when the execution does. Extracted from the
		// definition-seeded context this send built, for the reason the apply
		// site above states.
		//
		// The cookie snapshot is recorded whatever shouldStoreCookies says,
		// because the overlay is not storage: it is this execution's own view,
		// and a preference about persisting cookies has nothing to say about
		// what the run's next step should see. Nothing here outlives the run.
		mcpPolicy.overlay.absorbVariableDeltas(scripting.DeltasFromContext(scriptVariables))
		mcpPolicy.overlay.recordCookies(scriptCookieJar.Snapshot())
	} else {
		if shouldStoreCookies {
			a.state.Cookies = cookiejar.MergeScriptJar(a.state.Cookies, initialCookies, scriptCookieJar.Snapshot())
			a.pruneExpiredCookiesLocked()
		}
		scripting.ApplyScriptVariableContextToState(&a.state, liveWorkspace, collection, environmentID, scriptVariables)
	}
	// US-009 step 4. Store the body and record its handle as the response lands
	// in state. Best-effort by design at this step: Body is still populated and
	// still authoritative, so a failed cache write must not fail a request the
	// user just saw succeed. See migrateResponseBodiesLocked for where that
	// contract inverts.
	if controls.Visualizer != nil {
		response.Visualizer = controls.Visualizer
	}
	_ = a.attachResponseBody(&response)
	// US-048. Best-effort and deliberately ignoring the error: a request that
	// reached the server must not be reported as failed because its history
	// line could not be written. Recorded AFTER attachResponseBody so the
	// entry carries the body handle rather than duplicating the body.
	//
	// §7. The projection form, carrying the secret values hydrated at the head
	// of this send. nil for a UI send, which is the same call recordSendHistory
	// makes. App history is identical either way; the values only ever feed the
	// agent-visible sibling artifact.
	_ = a.recordSendHistoryWithMCPProjection(collectionID, requestCopy, &response, mcpMaskValues)
	item.Response = &response
	item.Timeline = append(item.Timeline, scriptTimeline...)
	if controls.SkipRequest {
		item.Timeline = append(item.Timeline, TimelineItem{
			ID:         newID("timeline"),
			Kind:       "script",
			Message:    "Skipped by pre-request script",
			At:         time.Now(),
			Duration:   response.DurationMs,
			RequestID:  item.ID,
			Source:     "sendRequest",
			Phase:      "pre-request",
			StatusText: response.StatusText,
			SourceFile: timelineSourceFileForItem(collection.Path, *item),
		})
	} else {
		item.Timeline = append(item.Timeline, mainRequestTimelineItem(*item, requestCopy, response))
		if requestCopy.Type == "http" || requestCopy.Type == "graphql" {
			item.Timeline = append(item.Timeline, responsestore.TimingTimelineItems(*item, response)...)
		}
		if requestCopy.Type == "grpc" {
			grpcTimelineRequest := requestCopy
			grpcTimelineRequest.ID = item.ID
			item.Timeline = append(item.Timeline, grpcExecutionTimelineItems(grpcTimelineRequest, response, vars)...)
		}
		a.state.NetworkLog = append([]NetworkLog{networkLogEntry(requestCopy, response, vars)}, a.state.NetworkLog...)
		if len(a.state.NetworkLog) > 100 {
			a.state.NetworkLog = a.state.NetworkLog[:100]
		}
	}
	return a.state, controls, item.Response, a.markDirty(persistScopeState)
}

func mainRequestTimelineItem(item RequestItem, requestCopy RequestItem, response Response) TimelineItem {
	entry := TimelineItem{
		ID:         newID("timeline"),
		Kind:       "request",
		Message:    fmt.Sprintf("%s %s -> %d", requestCopy.Method, response.RequestedURL, response.Status),
		At:         time.Now(),
		Duration:   response.DurationMs,
		RequestID:  item.ID,
		Source:     "main",
		Method:     strings.ToUpper(firstNonEmpty(requestCopy.Method, http.MethodGet)),
		URL:        response.RequestedURL,
		Status:     response.Status,
		StatusText: cleanStatusText(response.Status, response.StatusText),
	}
	if requestCopy.Type != "grpc" {
		return entry
	}
	grpcMethod := firstNonEmpty(response.Headers["grpc-method"], requestCopy.Method)
	streamType := firstNonEmpty(response.Headers["grpc-stream"], grpcStreamTypeLabelFromStorage(requestCopy.GrpcMethodType), "unary")
	requestCount := strings.TrimSpace(response.Headers["grpc-request-count"])
	responseCount := strings.TrimSpace(response.Headers["grpc-response-count"])
	detailParts := []string{streamType + " stream"}
	if requestCount != "" {
		detailParts = append(detailParts, "sent "+requestCount)
	}
	if responseCount != "" {
		detailParts = append(detailParts, "received "+responseCount)
	}
	entry.Method = "CALL"
	entry.Message = fmt.Sprintf("CALL %s %s -> %d (%s)", strings.TrimPrefix(grpcMethod, "/"), response.RequestedURL, response.Status, strings.Join(detailParts, ", "))
	return entry
}

func grpcExecutionTimelineItems(item RequestItem, response Response, vars map[string]string) []TimelineItem {
	at := response.SentAt
	if at.IsZero() {
		at = time.Now()
	}
	rows := []TimelineItem{grpcExecutionRequestTimelineItem(item, response, vars, at)}
	messages := grpcexec.GrpcurlRequestMessages(item, vars)
	if len(messages) == 0 {
		messages = []GrpcMessage{{Name: "message 1", Content: "{}"}}
	}
	for index, message := range messages[:grpcExecutionRequestMessageCount(item, response, len(messages))] {
		rows = append(rows, grpcExecutionMessageTimelineItem(item, response, message, index, at.Add(time.Duration(index+1)*time.Millisecond)))
	}
	responseRows, lastResponseAt := grpcExecutionResponseTimelineItems(item, response, at.Add(time.Duration(len(rows)+1)*time.Millisecond))
	rows = append(rows, responseRows...)
	terminalAt := lastResponseAt
	if terminalAt.IsZero() {
		terminalAt = at.Add(time.Duration(len(rows)+1) * time.Millisecond)
	}
	if len(response.Metadata) > 0 {
		rows = append(rows, grpcStreamMetadataTimelineItem(item, response, terminalAt))
	}
	if response.Headers["grpc-status"] != "" || len(response.Trailers) > 0 || response.Error != "" {
		rows = append(rows, grpcStreamStatusTimelineItem(item, response, terminalAt))
	}
	if response.Error != "" {
		rows = append(rows, grpcExecutionErrorTimelineItem(item, response, terminalAt))
	} else {
		rows = append(rows, grpcExecutionEndTimelineItem(item, response, terminalAt))
	}
	return rows
}

func grpcExecutionRequestTimelineItem(item RequestItem, response Response, vars map[string]string, at time.Time) TimelineItem {
	methodName := grpcTimelineMethodName(item, response)
	streamType := grpcTimelineStreamType(item, response)
	payloadParts := []string{"method: " + firstNonEmpty(methodName, strings.TrimSpace(item.Method), "CALL"), "url: " + response.RequestedURL, "stream: " + streamType}
	messages := grpcexec.GrpcurlRequestMessages(item, vars)
	if streamType != "client" && streamType != "bidi" && len(messages) > 0 {
		payloadParts = append(payloadParts, "body:\n"+strings.TrimSpace(grpcexec.GrpcurlMessageContent(messages[0])))
	}
	return grpcExecutionTimelineItem(item, response, "request", "", fmt.Sprintf("gRPC request %s %s (%s stream)", methodName, response.RequestedURL, streamType), strings.Join(payloadParts, "\n"), at)
}

func grpcExecutionMessageTimelineItem(item RequestItem, response Response, message GrpcMessage, index int, at time.Time) TimelineItem {
	name := firstNonEmpty(strings.TrimSpace(message.Name), fmt.Sprintf("message %d", index+1))
	payload := strings.TrimSpace(grpcexec.GrpcurlMessageContent(message))
	if payload == "" {
		payload = "{}"
	}
	return grpcExecutionTimelineItem(item, response, "message", name, fmt.Sprintf("gRPC message %s %s", name, response.RequestedURL), payload, at)
}

func grpcExecutionResponseTimelineItems(item RequestItem, response Response, at time.Time) ([]TimelineItem, time.Time) {
	body := strings.TrimSpace(response.Body)
	if body == "" {
		return nil, time.Time{}
	}
	rawResponses := []json.RawMessage{}
	if strings.HasPrefix(body, "[") {
		if err := json.Unmarshal([]byte(body), &rawResponses); err != nil {
			rawResponses = nil
		}
	}
	if len(rawResponses) == 0 {
		rawResponses = []json.RawMessage{json.RawMessage(body)}
	}
	rows := make([]TimelineItem, 0, len(rawResponses))
	lastAt := at
	for index, raw := range rawResponses {
		rowAt := at.Add(time.Duration(index) * time.Millisecond)
		name := fmt.Sprintf("response %d", index+1)
		rows = append(rows, grpcExecutionTimelineItem(item, response, "response", name, fmt.Sprintf("Response Message #%d %s", index+1, response.RequestedURL), grpcTimelineJSONPayload(raw), rowAt))
		lastAt = rowAt
	}
	return rows, lastAt
}

func grpcExecutionEndTimelineItem(item RequestItem, response Response, at time.Time) TimelineItem {
	streamType := grpcTimelineStreamType(item, response)
	message := fmt.Sprintf("gRPC call ended %s", response.RequestedURL)
	if streamType != "unary" {
		message = fmt.Sprintf("Stream Ended %s", response.RequestedURL)
	}
	payloadParts := []string{}
	if requestCount := strings.TrimSpace(response.Headers["grpc-request-count"]); requestCount != "" {
		payloadParts = append(payloadParts, "sent: "+requestCount)
	}
	if responseCount := strings.TrimSpace(response.Headers["grpc-response-count"]); responseCount != "" {
		payloadParts = append(payloadParts, "received: "+responseCount)
	}
	if statusValue := strings.TrimSpace(response.Headers["grpc-status"]); statusValue != "" {
		payloadParts = append(payloadParts, "grpc-status: "+statusValue)
	}
	return grpcExecutionTimelineItem(item, response, "end", "", message, strings.Join(payloadParts, "\n"), at.Add(3*time.Millisecond))
}

func grpcExecutionErrorTimelineItem(item RequestItem, response Response, at time.Time) TimelineItem {
	row := grpcExecutionTimelineItem(item, response, "error", "", "gRPC error "+response.RequestedURL, firstNonEmpty(response.Error, response.StatusText), at.Add(3*time.Millisecond))
	row.Trailers = response.Trailers
	return row
}

func grpcExecutionTimelineItem(item RequestItem, response Response, eventType, eventName, message, payload string, at time.Time) TimelineItem {
	if at.IsZero() {
		at = time.Now()
	}
	errorText := ""
	if eventType == "error" {
		errorText = firstNonEmpty(response.Error, response.StatusText)
	}
	return TimelineItem{
		ID:         newID("timeline"),
		Kind:       "request",
		EventType:  eventType,
		EventName:  eventName,
		Message:    message,
		At:         at,
		Duration:   response.DurationMs,
		RequestID:  item.ID,
		Source:     "grpc",
		Method:     "CALL",
		URL:        response.RequestedURL,
		Status:     response.Status,
		StatusText: cleanStatusText(response.Status, response.StatusText),
		Error:      errorText,
		Payload:    payload,
	}
}

func grpcExecutionRequestMessageCount(item RequestItem, response Response, messageCount int) int {
	if messageCount <= 0 {
		return 0
	}
	switch grpcTimelineStreamType(item, response) {
	case "client", "bidi":
	default:
		return 0
	}
	if count, ok := grpcTimelineHeaderInt(response.Headers, "grpc-request-count"); ok {
		return clampInt(count, 0, messageCount)
	}
	if response.Error != "" && response.Status == 0 {
		return 0
	}
	return messageCount
}

func grpcTimelineHeaderInt(headers map[string]string, key string) (int, bool) {
	if headers == nil {
		return 0, false
	}
	value := strings.TrimSpace(headers[key])
	if value == "" {
		return 0, false
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, false
	}
	return parsed, true
}

func clampInt(value, minValue, maxValue int) int {
	if value < minValue {
		return minValue
	}
	if value > maxValue {
		return maxValue
	}
	return value
}

func grpcTimelineMethodName(item RequestItem, response Response) string {
	return strings.TrimPrefix(firstNonEmpty(response.Headers["grpc-method"], item.Method), "/")
}

func grpcTimelineStreamType(item RequestItem, response Response) string {
	return firstNonEmpty(response.Headers["grpc-stream"], grpcStreamTypeLabelFromStorage(item.GrpcMethodType), "unary")
}

func grpcTimelineJSONPayload(raw json.RawMessage) string {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return ""
	}
	var value interface{}
	if err := json.Unmarshal([]byte(trimmed), &value); err != nil {
		return trimmed
	}
	body, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return trimmed
	}
	return string(body)
}

func networkLogEntry(item RequestItem, response Response, vars map[string]string) NetworkLog {
	return NetworkLog{
		ID:              newID("net"),
		Method:          firstNonEmpty(strings.ToUpper(strings.TrimSpace(item.Method)), http.MethodGet),
		URL:             response.RequestedURL,
		Status:          response.Status,
		StatusText:      response.StatusText,
		DurationMs:      response.DurationMs,
		Size:            response.Size,
		At:              time.Now(),
		Error:           response.Error,
		RequestHeaders:  networkLogRequestHeaders(item, vars),
		RequestBody:     networkLogRequestBody(item.Body, vars),
		ResponseHeaders: cloneStringMap(response.Headers),
		ResponseBody:    truncateNetworkLogBody(response.Body),
	}
}

func networkLogRequestHeaders(item RequestItem, vars map[string]string) map[string]string {
	headers := map[string]string{}
	for _, header := range item.Headers {
		if header.Enabled && strings.TrimSpace(header.Name) != "" {
			headers[interpolate(header.Name, vars)] = interpolate(header.Value, vars)
		}
	}
	if contentType := networkLogBodyContentType(item.Body, vars); contentType != "" && !scripting.StringMapHasKey(headers, "Content-Type") {
		headers["Content-Type"] = contentType
	}
	return headers
}

func networkLogBodyContentType(body RequestBody, vars map[string]string) string {
	switch body.Mode {
	case "json":
		return "application/json"
	case "xml":
		return "application/xml"
	case "text", "sparql":
		return "text/plain"
	case "formUrlEncoded":
		return "application/x-www-form-urlencoded"
	case "multipartForm":
		return "multipart/form-data"
	case "file":
		if selected, ok := selectedFileBodyEntry(body); ok {
			if contentType := strings.TrimSpace(interpolate(selected.ContentType, vars)); contentType != "" {
				return contentType
			}
		}
		return "application/octet-stream"
	default:
		return ""
	}
}

func networkLogRequestBody(body RequestBody, vars map[string]string) string {
	switch body.Mode {
	case "", "none":
		return ""
	case "json":
		return truncateNetworkLogBody(interpolate(body.JSON, vars))
	case "xml":
		return truncateNetworkLogBody(interpolate(body.XML, vars))
	case "text", "sparql":
		return truncateNetworkLogBody(interpolate(body.Text, vars))
	case "graphql":
		// Indented from the same bytes the request carries, rather than
		// re-encoded from the parts: a log built by a second encoder is a log
		// that can disagree with the request it claims to describe.
		var indented bytes.Buffer
		payload := graphQLRequestPayload(body, vars)
		if err := json.Indent(&indented, []byte(payload), "", "  "); err != nil {
			return truncateNetworkLogBody(payload)
		}
		return truncateNetworkLogBody(indented.String())
	case "formUrlEncoded":
		values := url.Values{}
		for _, field := range body.FormURLEncoded {
			if field.Enabled {
				values.Add(interpolate(field.Name, vars), interpolate(field.Value, vars))
			}
		}
		return truncateNetworkLogBody(values.Encode())
	case "multipartForm":
		lines := []string{}
		for _, part := range body.Multipart {
			if part.Enabled {
				value := interpolate(part.Value, vars)
				if strings.TrimSpace(part.FilePath) != "" {
					value = "@file " + interpolate(part.FilePath, vars)
				}
				lines = append(lines, interpolate(part.Name, vars)+"="+value)
			}
		}
		return truncateNetworkLogBody(strings.Join(lines, "\n"))
	case "file":
		if selected, ok := selectedFileBodyEntry(body); ok && strings.TrimSpace(selected.FilePath) != "" {
			return "@file " + interpolate(selected.FilePath, vars)
		}
		return ""
	default:
		return truncateNetworkLogBody(interpolate(body.Text, vars))
	}
}

func cloneStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return map[string]string{}
	}
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func truncateNetworkLogBody(value string) string {
	if len(value) <= networkLogBodyLimit {
		return value
	}
	return value[:networkLogBodyLimit] + "\n... truncated"
}

// runScriptedCollectionRequest is bru.runRequest: one stored request run from
// inside another request's script.
//
// IT TAKES THE PARENT EXECUTION'S CONTEXT (§5 row 11). It used to start its own
// cancellable request from context.Background(), which dropped two things at
// once: the parent's cancellation, and — once there was one — the parent's
// provenance. A nested send that lost the policy would be an unchecked engine
// egress reachable from a script, which is the shape this whole phase exists to
// close.
//
// AND IT PUSHES ITS OWN DEFINITION SCOPE. The nested target is a DIFFERENT
// stored definition, so it gets a different Base: it may reach where IT points,
// not where its caller points, and its authority disappears when it returns.
// The scope is computed here, lazily, from the stored definition under the
// state lock and with the same single agent-free variable context rule as the
// run's root scope (§4.6) — deliberately NOT from parentVariables, which by
// this point can carry script-set and overlay values.
func (a *App) runScriptedCollectionRequest(ctx context.Context, collectionID, targetRef, environmentID string, parentVariables *scripting.VariableContext, jar *scripting.CookieJar, logs *[]ScriptLog, depth *int, recordTimeline func(TimelineItem)) (Response, *TimelineItem, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(targetRef) == "" {
		return Response{}, nil, errors.New("bru.runRequest requires a request path or name")
	}
	if depth == nil {
		localDepth := 0
		depth = &localDepth
	}
	if *depth >= 10 {
		return Response{}, nil, errors.New("bru.runRequest exceeded nested request limit")
	}
	*depth++
	defer func() { *depth-- }()

	a.mu.Lock()
	if err := a.ensureReadyLocked(); err != nil {
		a.mu.Unlock()
		return Response{}, nil, err
	}
	// The WORKSPACE form, because the nested scope's site needs the workspace
	// path and the active global environments, and its agent-free variable
	// context needs the globals too.
	ws, collection, err := a.findCollectionWithWorkspaceLocked(collectionID)
	if err != nil {
		a.mu.Unlock()
		return Response{}, nil, err
	}
	item, err := findRunRequestItem(collection, targetRef)
	if err != nil {
		a.mu.Unlock()
		return Response{}, nil, err
	}
	if item.Type == "websocket" || item.Type == "grpc" {
		response := scriptRunRequestUnsupportedProtocolResponse(*item)
		timelineEntry := scriptRunRequestTimelineItem(collection.Path, *item, response, nil)
		a.mu.Unlock()
		return response, &timelineEntry, nil
	}
	collectionCopy := *collection
	requestCopy := scripting.EffectiveRequest(collectionCopy, *item)
	scripts := scripting.MergedRuntimeScripts(collectionCopy, requestCopy)
	preferences := prefs.Normalize(a.state.Preferences)
	shouldSendCookies := prefs.BoolPtrValue(preferences.Request.SendCookies, true) && requestCopy.Settings.StoreCookies
	nestedVariables := scripting.ScriptVariableContextForItem(parentVariables, &collectionCopy, environmentID, requestCopy)
	// The nested scope's inputs, taken while the lock is held. Everything
	// agent-supplied is excluded by construction: NewScriptVariableContext with
	// nil prompt values, the collection's own environments, and no overrides,
	// no flow inputs, no overlay (§4.1).
	nestedScope := (*mcpDefinitionOriginsInput)(nil)
	if policy := mcpPolicyFromContext(ctx); policy != nil {
		globals := scripting.ActiveGlobalEnvironmentsForWorkspace(*ws)
		agentFree := scripting.NewScriptVariableContext(globals, &collectionCopy, environmentID, requestCopy, nil, ws.Path)
		nestedScope = &mcpDefinitionOriginsInput{
			site: mcpDefinitionSite{
				workspacePath:        ws.Path,
				collectionID:         collectionID,
				requestID:            item.ID,
				environmentID:        environmentID,
				globalEnvironmentIDs: mcpEnvironmentIDs(globals),
			},
			effective: requestCopy,
			vars:      agentFree.Combined,
		}
	}
	nestedMeta := scripting.ScriptRuntimeMeta{
		CollectionName:            collectionCopy.Name,
		CollectionPath:            collectionCopy.Path,
		EnvironmentName:           scripting.SelectedEnvironmentName(&collectionCopy, environmentID),
		EnvironmentID:             environmentID,
		JSSandboxMode:             collectionJSSandboxMode(collectionCopy),
		Variables:                 nestedVariables,
		OAuth2CredentialVariables: a.oauth2CredentialVariablesSnapshot,
		ResetOAuth2Credential:     a.resetOAuth2Credential,
		RecordTimeline:            recordTimeline,
	}
	nestedMeta.RunRequest = func(target string) (Response, *TimelineItem, error) {
		return a.runScriptedCollectionRequest(ctx, collectionID, target, environmentID, nestedVariables, jar, logs, depth, recordTimeline)
	}
	// UNCONDITIONAL, for the reason the outer send gives: the script client is
	// guard-wrapped, so a nested UI send whose script calls out must carry the
	// UI label too or strict provenance refuses it.
	nestedMeta.RequestContext = mcpContextWithEgressKind(ctx, egressKindScript)
	a.mu.Unlock()

	// PUSHED HERE, WITH THE POP DEFERRED IMMEDIATELY (§4.1). Outside the lock
	// because mcpDefinitionOrigins needs collectionProxyResolution, which takes
	// a.mu.RLock; before the pre-request script, because a script's own egress
	// belongs to the nested definition's authority, not its caller's. The
	// pairing is what makes the nested authority disappear however this returns
	// — early error, refusal, or panic.
	if nestedScope != nil {
		policy := mcpPolicyFromContext(ctx)
		nestedScope.proxy = a.collectionProxyResolution(collectionID)
		policy.PushScope(mcpDefinitionOrigins(*nestedScope))
		defer policy.PopScope()
		nestedMeta.EgressAuthorizer = mcpScriptEgressAuthorizer(ctx, policy)
	}

	var response Response
	preMeta := nestedMeta
	preMeta.TimelinePhase = "pre-request"
	preTestResults := []TestResult{}
	preState, err := scripting.RunPreRequestScriptSourceMeta(scripts.PreLevels, &requestCopy, nestedVariables.Combined, jar, preMeta, &preTestResults, logs)
	if err != nil {
		response = scripting.ScriptErrorResponse("pre-request script", err)
		response.TestResults = append(append([]TestResult{}, preTestResults...), response.TestResults...)
		response.RequestedURL = cookiejar.PreviewRequestURL(requestCopy, nestedVariables.Combined)
		response.ScriptLogs = cloneScriptLogs(logs)
		scripting.ScriptMergeVariableContext(parentVariables, nestedVariables)
		timelineEntry := scriptRunRequestTimelineItem(collectionCopy.Path, requestCopy, response, nestedVariables.Combined)
		return response, &timelineEntry, nil
	}
	if preState != nil && preState.SkipRequest {
		response = scripting.ScriptSkippedResponse(requestCopy, nestedVariables.Combined)
		response.TestResults = append(append([]TestResult{}, preTestResults...), response.TestResults...)
		response.ScriptLogs = cloneScriptLogs(logs)
		scripting.ScriptMergeVariableContext(parentVariables, nestedVariables)
		timelineEntry := scriptRunRequestTimelineItem(collectionCopy.Path, requestCopy, response, nestedVariables.Combined)
		return response, &timelineEntry, nil
	}

	requestURL := cookiejar.PreviewRequestURL(requestCopy, nestedVariables.Combined)
	if shouldSendCookies && jar != nil {
		cookiejar.AttachHeader(&requestCopy, jar.Snapshot(), requestURL)
	}
	func() {
		// The PARENT's context (§5 row 11). This used to be
		// startCancellableRequest, i.e. context.Background(): a nested send
		// neither observed the parent's cancellation nor carried its
		// provenance, so under MCP it would have reached the network with no
		// policy on it at all.
		executionContext, finishExecution := a.startCancellableRequestWithParent(ctx, collectionID, requestCopy.ID, requestCopy.Type)
		defer finishExecution()
		response = a.executeHTTP(executionContext, collectionID, collectionCopy, requestCopy, nestedVariables.Combined, preState, recordTimeline)
	}()
	response.TestResults = append(append([]TestResult{}, preTestResults...), response.TestResults...)
	if jar != nil {
		jar.UpsertAll(response.Cookies)
	}
	postVariablesMeta := nestedMeta
	postVariablesMeta.TimelinePhase = "post-response"
	if err := scripting.RunPostResponseVariables(scripting.EffectiveResponseVariables(collectionCopy, requestCopy), requestCopy, &response, nestedVariables, jar, postVariablesMeta, logs); err != nil {
		response.TestResults = append(response.TestResults, TestResult{Name: "post-response variables", Passed: false, Message: err.Error()})
	}
	postMeta := nestedMeta
	postMeta.TimelinePhase = "post-response"
	postTestResults := []TestResult{}
	postState, err := scripting.RunPostResponseScriptSourceMeta(scripts.PostLevels, requestCopy, &response, nestedVariables.Combined, jar, postMeta, &postTestResults, logs)
	_ = postState
	response.TestResults = append(response.TestResults, postTestResults...)
	if err != nil {
		response.TestResults = append(response.TestResults, TestResult{Name: "post-response script", Passed: false, Message: err.Error()})
	}
	testsMeta := nestedMeta
	testsMeta.TimelinePhase = "tests"
	testResults, _ := scripting.EvaluateRuntimeTestsSourceMeta(scripts.TestsLevels, response, requestCopy, nestedVariables.Combined, jar, testsMeta, logs)
	response.TestResults = append(response.TestResults, testResults...)
	response.ScriptLogs = cloneScriptLogs(logs)
	scripting.ScriptMergeVariableContext(parentVariables, nestedVariables)
	timelineEntry := scriptRunRequestTimelineItem(collectionCopy.Path, requestCopy, response, nestedVariables.Combined)
	return response, &timelineEntry, nil
}

// --- the send path's MCP seams (§3, §5 rows 12 and 13, §7) ----------------

// variableDeltas and absorbVariableDeltas bridge the overlay's four scope maps
// to scripting's RunVariableDeltas.
//
// TWO TYPES FOR ONE THING, ON PURPOSE. The overlay is a policy field with a
// mutex, held for the length of an execution and read from whichever goroutine
// a flow step runs on; RunVariableDeltas is a plain value scripting hands out
// and takes back. Keeping them separate is what lets the overlay hand out
// copies under its own lock instead of publishing live maps into a
// VariableContext that another send is about to mutate.
func (o *mcpExecutionOverlay) variableDeltas() scripting.RunVariableDeltas {
	if o == nil {
		return scripting.RunVariableDeltas{}
	}
	return scripting.RunVariableDeltas{
		Runtime:    o.variables(mcpOverlayRuntime),
		Env:        o.variables(mcpOverlayEnv),
		Global:     o.variables(mcpOverlayGlobal),
		Collection: o.variables(mcpOverlayCollection),
	}
}

func (o *mcpExecutionOverlay) absorbVariableDeltas(deltas scripting.RunVariableDeltas) {
	if o == nil || deltas.IsEmpty() {
		return
	}
	o.mergeVariables(mcpOverlayRuntime, deltas.Runtime)
	o.mergeVariables(mcpOverlayEnv, deltas.Env)
	o.mergeVariables(mcpOverlayGlobal, deltas.Global)
	o.mergeVariables(mcpOverlayCollection, deltas.Collection)
}

// mcpSendCookieSeed picks the jar this send starts from.
//
// A UI send starts from AppState, as it always has. An MCP send starts from
// AppState too — on its FIRST send, because the overlay has recorded nothing
// yet — and from the overlay afterwards. That is the cookie half of §3's
// "within-run semantics survive, nothing persists": a login step's Set-Cookie
// is visible to the step that follows it, and is gone when the run ends.
func mcpSendCookieSeed(policy *mcpEgressPolicy, stateCookies []CookieEntry) []CookieEntry {
	if policy != nil {
		if snapshot, recorded := policy.overlay.cookieSnapshot(); recorded {
			return snapshot
		}
	}
	return scripting.CloneCookieEntries(stateCookies)
}

// mcpScriptEgressAuthorizer is the checkpoint behind pm.sendRequest,
// bru.sendRequest, fetch() and the DNS shims (§5 rows 12 and 13).
//
// ONE FUNCTION, TWO KINDS. scripting hands over the kind it is about to perform
// — a send or a name lookup — and they are authorized differently because a
// lookup has no scheme and no port: a hostname is checked against the scope's
// dnsHosts, while a send is checked as a full origin. Treating a lookup as an
// origin would either invent a scheme or refuse every lookup; treating a send
// as a hostname would let :3000 authorize :8080, which §1.4(9) specifically
// rejects.
//
// The context is the SEND's, so a prompt raised from inside a script is the
// same prompt raised from anywhere else and cancelling the run cancels the
// wait.
func mcpScriptEgressAuthorizer(ctx context.Context, policy *mcpEgressPolicy) func(string, string) error {
	if policy == nil {
		return nil
	}
	return func(target, kind string) error {
		if kind == scripting.EgressKindScriptDNS {
			return mcpAuthorizeScriptDNSHost(policy, target)
		}
		origin, ok := OriginOfURL(target)
		if !ok {
			return fmt.Errorf("%w: this run's script tried to contact %q, which is not an http(s) destination LiteAPI can check; use a full http(s) URL, or run this request in the LiteAPI app",
				mcpserver.ErrDenied, target)
		}
		return policy.Authorize(ctx, origin, egressKindScript)
	}
}

// mcpAuthorizeScriptDNSHost checks a script's name lookup against the active
// scope's hostnames (§5 row 13).
//
// IT REFUSES RATHER THAN PROMPTS, which is a narrower rule than the one for
// sends and deliberately so. A lookup has no origin — no scheme, no port — so
// there is nothing an origin-keyed approval could be granted for, and §1.2(1)
// excludes resolver traffic from the guarantee precisely because a hostname
// reaching a resolver is not an application-layer egress LiteAPI can stand
// behind. The tight rule is therefore the honest one: a script may resolve the
// names its own definition already points at, and nothing else. A script that
// needs another name can be run in the app.
func mcpAuthorizeScriptDNSHost(policy *mcpEgressPolicy, host string) error {
	scope, ok := policy.activeScope()
	if !ok {
		return fmt.Errorf("%w: this run has no active request scope, so the name lookup for %q could not be checked; this is a bug in LiteAPI — report it rather than retrying",
			mcpserver.ErrDenied, host)
	}
	// Normalized through the same function origins use, so [::1], [::0001] and
	// ::1 are one host on both sides of the comparison.
	if normalized := normalizeOriginHost(host); normalized != "" && scope.dnsHosts[normalized] {
		return nil
	}
	return fmt.Errorf("%w: this run's script tried to resolve %q, and nothing in request %q's definition (collection %q, environment %s) names that host. Run this request in the LiteAPI app if the lookup is intended",
		mcpserver.ErrDenied, host, scope.site.requestID, scope.site.collectionID, scope.site.environmentLabel())
}

// mcpSecretValuesLocked collects every secret variable's resolved value from
// state. CALLED WITH a.mu ALREADY HELD (§7).
//
// A SECOND WALK RATHER THAN A SHARED ONE, and the duplication is deliberate
// rather than overlooked. mcpHydratedSecretValues does the same walk through
// readStateForMCP, which TAKES a.mu — so the send path, which holds that lock
// across its whole head section and again across its tail, cannot call it
// without deadlocking. §7 is explicit that the values are hydrated at the head
// under the already-held first lock and carried down. The two walks are pinned
// to the same shape by their shared caller in the tests; the follow-up that
// owns mcp_backend.go can collapse them into one locked helper with two entry
// points.
func mcpSecretValuesLocked(state *AppState) []string {
	if state == nil {
		return nil
	}
	var values []string
	collect := func(variables []Variable) {
		for _, variable := range variables {
			if !variable.Secret {
				continue
			}
			if value := envsecrets.ValueToString(variable.Value); value != "" {
				values = append(values, value)
			}
		}
	}
	for wi := range state.Workspaces {
		workspace := &state.Workspaces[wi]
		for ei := range workspace.GlobalEnvironments {
			collect(workspace.GlobalEnvironments[ei].Variables)
		}
		for ci := range workspace.Collections {
			collection := &workspace.Collections[ci]
			collect(collection.Variables)
			for ei := range collection.Environments {
				collect(collection.Environments[ei].Variables)
			}
			for ii := range collection.Items {
				collect(collection.Items[ii].Vars.Req)
				collect(collection.Items[ii].Vars.Res)
			}
		}
	}
	return values
}

func scriptRunRequestTimelineItem(collectionPath string, item RequestItem, response Response, vars map[string]string) TimelineItem {
	method := strings.ToUpper(firstNonEmpty(item.Method, http.MethodGet))
	targetURL := firstNonEmpty(response.RequestedURL, cookiejar.PreviewRequestURL(item, vars), item.URL)
	statusText := cleanStatusText(response.Status, response.StatusText)
	errorText := strings.TrimSpace(response.Error)
	if scripting.ScriptRunRequestResponseIsSkipped(response) {
		errorText = response.StatusText
		statusText = "Skipped"
	} else if response.Status == 0 && errorText != "" {
		statusText = "Error"
	}
	statusLabel := statusText
	if response.Status > 0 {
		statusLabel = strconv.Itoa(response.Status)
	}
	if strings.TrimSpace(statusLabel) == "" {
		statusLabel = "-"
	}
	at := response.SentAt
	if at.IsZero() {
		at = time.Now()
	}
	return TimelineItem{
		Message:    strings.TrimSpace(fmt.Sprintf("%s %s -> %s", method, targetURL, statusLabel)),
		At:         at,
		Duration:   response.DurationMs,
		RequestID:  item.ID,
		Method:     method,
		URL:        targetURL,
		Status:     response.Status,
		StatusText: statusText,
		Error:      errorText,
		SourceFile: timelineSourceFileForItem(collectionPath, item),
	}
}

func cloneScriptLogs(logs *[]ScriptLog) []ScriptLog {
	if logs == nil || len(*logs) == 0 {
		return nil
	}
	return append([]ScriptLog(nil), (*logs)...)
}

func scriptRunRequestUnsupportedProtocolResponse(item RequestItem) Response {
	label := "protocol"
	switch item.Type {
	case "websocket":
		label = "WebSocket"
	case "grpc":
		label = "gRPC"
	}
	return Response{
		StatusText:  fmt.Sprintf("bru.runRequest does not support %s requests", label),
		Headers:     map[string]string{},
		PreviewMode: "auto",
		SentAt:      time.Now(),
	}
}
