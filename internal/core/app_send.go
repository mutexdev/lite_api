package core

import (
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
	"github.com/mutexdev/lite_api/internal/grpcexec"
	"github.com/mutexdev/lite_api/internal/prefs"
	"github.com/mutexdev/lite_api/internal/responsestore"
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

func (a *App) sendRequestWithControls(collectionID, itemID, environmentID string, promptValues map[string]string) (AppState, scripting.Controls, error) {
	state, controls, _, err := a.sendRequestWithControlsContext(context.Background(), collectionID, itemID, environmentID, promptValues, nil, runnerIteration{})
	return state, controls, err
}

// sendRequestWithControlsContext resolves collectionID/itemID twice: once on
// entry, and again on the tail because a.mu is released across the network I/O
// and the request may have moved or gone in that window. index (US-024) is an
// optional per-run lookup hint that makes both resolutions O(1); it is verified
// against live state on every use, and nil restores the plain linear scans.
//
// The fourth return value is the *Response this call stored on the item. The
// collection runner used to re-find the item in the returned state purely to
// read it back, which was another linear scan per request.
func (a *App) sendRequestWithControlsContext(parent context.Context, collectionID, itemID, environmentID string, promptValues map[string]string, index *runnerLookupIndex, iteration runnerIteration) (AppState, scripting.Controls, *Response, error) {
	controls := scripting.Controls{}
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
	scriptVariables := scripting.NewScriptVariableContext(scripting.ActiveGlobalEnvironmentsForWorkspace(*ws), collection, environmentID, requestCopy, promptValues, ws.Path)
	// US-046. Applied after construction and before Combined is read, so the
	// row participates in the precedence chain rather than being pasted over
	// the result of it.
	scripting.ApplyIterationDataToContext(scriptVariables, iteration.Data)
	vars := scriptVariables.Combined
	scriptLogs := []ScriptLog{}
	scriptTimeline := []TimelineItem{}
	scriptCookieJar := scripting.NewScriptCookieJar(scripting.CloneCookieEntries(a.state.Cookies))
	initialCookies := scriptCookieJar.Snapshot()
	scriptRunDepth := 0
	scriptMeta := scripting.ScriptRuntimeMeta{
		CollectionName:            collection.Name,
		CollectionPath:            collection.Path,
		EnvironmentName:           scripting.SelectedEnvironmentName(collection, environmentID),
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
	scriptMeta.RunRequest = func(target string) (Response, *TimelineItem, error) {
		return a.runScriptedCollectionRequest(collectionID, target, environmentID, scriptVariables, scriptCookieJar, &scriptLogs, &scriptRunDepth, scriptMeta.RecordTimeline)
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

	var response Response
	preMeta := scriptMeta
	preMeta.TimelinePhase = "pre-request"
	preState := (*scripting.RequestState)(nil)
	if requestContextCancelled(executionContext) {
		response = cancelledRequestResponse(requestCopy, vars)
		response.ScriptLogs = scriptLogs
	} else {
		preState, err = scripting.RunPreRequestScriptWithJarStateMeta(scripts.Pre, &requestCopy, vars, scriptCookieJar, preMeta, &scriptLogs)
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
				postState, err := scripting.RunPostResponseScriptWithJarStateMeta(scripts.Post, requestCopy, &response, vars, scriptCookieJar, postMeta, &scriptLogs)
				controls.Merge(postState)
				if err != nil {
					response.TestResults = append(response.TestResults, TestResult{Name: "post-response script", Passed: false, Message: err.Error()})
				}
				if requestContextCancelled(executionContext) {
					markRequestCancelled(&response)
					response.ScriptLogs = scriptLogs
				} else {
					testsMeta := scriptMeta
					testsMeta.TimelinePhase = "tests"
					testResults, testState := scripting.EvaluateRuntimeTestsWithJarStateMeta(scripts.Tests, response, requestCopy, vars, scriptCookieJar, testsMeta, &scriptLogs)
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
	if shouldStoreCookies {
		a.state.Cookies = cookiejar.MergeScriptJar(a.state.Cookies, initialCookies, scriptCookieJar.Snapshot())
		a.pruneExpiredCookiesLocked()
	}
	scripting.ApplyScriptVariableContextToState(&a.state, liveWorkspace, collection, environmentID, scriptVariables)
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
	_ = a.recordSendHistory(collectionID, requestCopy, &response)
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
		payload := map[string]string{
			"query":     interpolate(body.GraphQLQuery, vars),
			"variables": interpolate(body.GraphQLVariables, vars),
		}
		data, err := json.MarshalIndent(payload, "", "  ")
		if err != nil {
			return ""
		}
		return truncateNetworkLogBody(string(data))
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

func (a *App) runScriptedCollectionRequest(collectionID, targetRef, environmentID string, parentVariables *scripting.VariableContext, jar *scripting.CookieJar, logs *[]ScriptLog, depth *int, recordTimeline func(TimelineItem)) (Response, *TimelineItem, error) {
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
	collection, err := a.findCollectionLocked(collectionID)
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
	nestedMeta := scripting.ScriptRuntimeMeta{
		CollectionName:            collectionCopy.Name,
		CollectionPath:            collectionCopy.Path,
		EnvironmentName:           scripting.SelectedEnvironmentName(&collectionCopy, environmentID),
		JSSandboxMode:             collectionJSSandboxMode(collectionCopy),
		Variables:                 nestedVariables,
		OAuth2CredentialVariables: a.oauth2CredentialVariablesSnapshot,
		ResetOAuth2Credential:     a.resetOAuth2Credential,
		RecordTimeline:            recordTimeline,
	}
	nestedMeta.RunRequest = func(target string) (Response, *TimelineItem, error) {
		return a.runScriptedCollectionRequest(collectionID, target, environmentID, nestedVariables, jar, logs, depth, recordTimeline)
	}
	a.mu.Unlock()

	var response Response
	preMeta := nestedMeta
	preMeta.TimelinePhase = "pre-request"
	preState, err := scripting.RunPreRequestScriptWithJarStateMeta(scripts.Pre, &requestCopy, nestedVariables.Combined, jar, preMeta, logs)
	if err != nil {
		response = scripting.ScriptErrorResponse("pre-request script", err)
		response.RequestedURL = cookiejar.PreviewRequestURL(requestCopy, nestedVariables.Combined)
		response.ScriptLogs = cloneScriptLogs(logs)
		scripting.ScriptMergeVariableContext(parentVariables, nestedVariables)
		timelineEntry := scriptRunRequestTimelineItem(collectionCopy.Path, requestCopy, response, nestedVariables.Combined)
		return response, &timelineEntry, nil
	}
	if preState != nil && preState.SkipRequest {
		response = scripting.ScriptSkippedResponse(requestCopy, nestedVariables.Combined)
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
		executionContext, finishExecution := a.startCancellableRequest(collectionID, requestCopy.ID, requestCopy.Type)
		defer finishExecution()
		response = a.executeHTTP(executionContext, collectionID, collectionCopy, requestCopy, nestedVariables.Combined, preState, recordTimeline)
	}()
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
	postState, err := scripting.RunPostResponseScriptWithJarStateMeta(scripts.Post, requestCopy, &response, nestedVariables.Combined, jar, postMeta, logs)
	_ = postState
	if err != nil {
		response.TestResults = append(response.TestResults, TestResult{Name: "post-response script", Passed: false, Message: err.Error()})
	}
	testsMeta := nestedMeta
	testsMeta.TimelinePhase = "tests"
	testResults, _ := scripting.EvaluateRuntimeTestsWithJarStateMeta(scripts.Tests, response, requestCopy, nestedVariables.Combined, jar, testsMeta, logs)
	response.TestResults = append(response.TestResults, testResults...)
	response.ScriptLogs = cloneScriptLogs(logs)
	scripting.ScriptMergeVariableContext(parentVariables, nestedVariables)
	timelineEntry := scriptRunRequestTimelineItem(collectionCopy.Path, requestCopy, response, nestedVariables.Combined)
	return response, &timelineEntry, nil
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
