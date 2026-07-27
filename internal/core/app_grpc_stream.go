package core

// Streaming gRPC: the live session map, its receiver goroutines and the bound methods that drive them.
//
// Split out of app_grpc.go by AST: declarations are identified by the parser
// and copied verbatim from their source offsets.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"

	"github.com/mutexdev/lite_api/internal/grpcexec"
	"github.com/mutexdev/lite_api/internal/scripting"
)

func executeGRPCStream(result *Response, conn *grpc.ClientConn, binding grpcMethodBinding, item RequestItem, vars map[string]string, ctx context.Context, start time.Time) {
	requests, err := grpcexec.RequestMessages(item, binding, vars)
	if err != nil {
		result.Error = err.Error()
		result.DurationMs = time.Since(start).Milliseconds()
		return
	}
	desc := &grpc.StreamDesc{
		StreamName:    string(binding.Descriptor.Name()),
		ServerStreams: binding.Descriptor.IsStreamingServer(),
		ClientStreams: binding.Descriptor.IsStreamingClient(),
	}
	stream, err := conn.NewStream(ctx, desc, binding.FullMethod)
	if err != nil {
		applyGRPCError(result, err, start)
		return
	}

	send := func() error {
		if !binding.Descriptor.IsStreamingClient() {
			if len(requests) == 0 {
				return nil
			}
			if err := stream.SendMsg(requests[0]); err != nil {
				return err
			}
			return stream.CloseSend()
		}
		for _, req := range requests {
			if err := stream.SendMsg(req); err != nil {
				return err
			}
		}
		return stream.CloseSend()
	}
	if binding.Descriptor.IsStreamingClient() && binding.Descriptor.IsStreamingServer() {
		sendErr := make(chan error, 1)
		go func() { sendErr <- send() }()
		receiveGRPCStream(result, stream, binding, len(requests), item.Assertions, start, true)
		if err := <-sendErr; err != nil && result.Error == "" {
			applyGRPCError(result, err, start)
		}
		return
	}
	if err := send(); err != nil {
		applyGRPCError(result, err, start)
		return
	}
	receiveGRPCStream(result, stream, binding, len(requests), item.Assertions, start, binding.Descriptor.IsStreamingServer())
}

func receiveGRPCStream(result *Response, stream grpc.ClientStream, binding grpcMethodBinding, requestCount int, assertions []Assertion, start time.Time, many bool) {
	rawResponses := []json.RawMessage{}
	for {
		res := dynamicpb.NewMessage(binding.Descriptor.Output())
		err := stream.RecvMsg(res)
		if err == io.EOF {
			break
		}
		if err != nil {
			if headers, headerErr := stream.Header(); headerErr == nil {
				grpcexec.AddMetadata(result.Headers, "", headers)
				result.Metadata = grpcexec.MetadataRows(headers)
			}
			grpcexec.AddMetadata(result.Headers, "trailer-", stream.Trailer())
			result.Trailers = grpcexec.MetadataRows(stream.Trailer())
			applyGRPCError(result, err, start)
			result.Headers["grpc-method"] = binding.FullMethod
			result.Headers["grpc-stream"] = grpcStreamType(binding.Descriptor)
			result.Headers["grpc-request-count"] = strconv.Itoa(requestCount)
			result.Headers["grpc-response-count"] = strconv.Itoa(len(rawResponses))
			if len(rawResponses) > 0 {
				body, marshalErr := json.MarshalIndent(rawResponses, "", "  ")
				if marshalErr == nil {
					result.Body = string(body)
					result.BodyBase64 = base64.StdEncoding.EncodeToString(body)
					result.Size = len(body)
					result.Assertions = evaluateAssertions(assertions, *result)
				}
			}
			return
		}
		body, err := (protojson.MarshalOptions{Multiline: true, Indent: "  ", EmitUnpopulated: true}).Marshal(res)
		if err != nil {
			result.Error = "format gRPC response JSON: " + err.Error()
			result.DurationMs = time.Since(start).Milliseconds()
			return
		}
		rawResponses = append(rawResponses, json.RawMessage(body))
		if !many {
			break
		}
	}
	if headers, err := stream.Header(); err == nil {
		grpcexec.AddMetadata(result.Headers, "", headers)
		result.Metadata = grpcexec.MetadataRows(headers)
	}
	grpcexec.AddMetadata(result.Headers, "trailer-", stream.Trailer())
	result.Trailers = grpcexec.MetadataRows(stream.Trailer())
	body, err := json.MarshalIndent(rawResponses, "", "  ")
	if err != nil {
		result.Error = "format gRPC stream JSON: " + err.Error()
		result.DurationMs = time.Since(start).Milliseconds()
		return
	}
	result.Status = http.StatusOK
	result.StatusText = "OK"
	result.Headers["grpc-status"] = "0"
	result.Headers["grpc-method"] = binding.FullMethod
	result.Headers["grpc-stream"] = grpcStreamType(binding.Descriptor)
	result.Headers["grpc-request-count"] = strconv.Itoa(requestCount)
	result.Headers["grpc-response-count"] = strconv.Itoa(len(rawResponses))
	result.Body = string(body)
	result.BodyBase64 = base64.StdEncoding.EncodeToString(body)
	result.Size = len(body)
	result.DurationMs = time.Since(start).Milliseconds()
	result.Assertions = evaluateAssertions(assertions, *result)
}

func grpcStreamSessionKey(collectionID, itemID string) string {
	return collectionID + "\x00" + itemID
}

func (a *App) grpcStreamRequestContext(collectionID, itemID, environmentID string, promptValues map[string]string) (RequestItem, Collection, map[string]string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return RequestItem{}, Collection{}, nil, err
	}
	ws, collection, err := a.findCollectionWithWorkspaceLocked(collectionID)
	if err != nil {
		return RequestItem{}, Collection{}, nil, err
	}
	item, err := findItem(collection, itemID)
	if err != nil {
		return RequestItem{}, Collection{}, nil, err
	}
	collectionCopy := *collection
	requestCopy := scripting.EffectiveRequest(collectionCopy, *item)
	scriptVariables := scripting.NewScriptVariableContext(scripting.ActiveGlobalEnvironmentsForWorkspace(*ws), collection, environmentID, requestCopy, promptValues, ws.Path)
	return requestCopy, collectionCopy, scriptVariables.Combined, nil
}

func (a *App) replaceGRPCStreamSession(key string, session *grpcStreamSession) {
	a.grpcStreamMu.Lock()
	previous := a.grpcStreamSessions[key]
	a.grpcStreamSessions[key] = session
	a.grpcStreamMu.Unlock()
	if previous != nil {
		previous.close("reconnected")
	}
}

func (a *App) popGRPCStreamSession(key string) *grpcStreamSession {
	a.grpcStreamMu.Lock()
	session := a.grpcStreamSessions[key]
	delete(a.grpcStreamSessions, key)
	a.grpcStreamMu.Unlock()
	return session
}

func (a *App) removeGRPCStreamSessionIfSame(key string, session *grpcStreamSession) {
	a.grpcStreamMu.Lock()
	if a.grpcStreamSessions[key] == session {
		delete(a.grpcStreamSessions, key)
	}
	a.grpcStreamMu.Unlock()
}

func grpcStreamReceiveWait(timeout time.Duration) time.Duration {
	if timeout > 0 && timeout < 500*time.Millisecond {
		return timeout
	}
	return 500 * time.Millisecond
}

func grpcStreamEndWait(timeout time.Duration) time.Duration {
	if timeout > 0 && timeout < 5*time.Second {
		return timeout
	}
	return 5 * time.Second
}

func (a *App) applyGRPCStreamResponse(collectionID, itemID string, response Response, timelines ...TimelineItem) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	collection, err := a.findCollectionLocked(collectionID)
	if err != nil {
		return AppState{}, err
	}
	item, err := findItem(collection, itemID)
	if err != nil {
		return AppState{}, err
	}
	// US-009 step 4. Store the body and record its handle as the response lands
	// in state. Best-effort by design at this step: Body is still populated and
	// still authoritative, so a failed cache write must not fail a request the
	// user just saw succeed. See migrateResponseBodiesLocked for where that
	// contract inverts.
	_ = a.attachResponseBody(&response)
	item.Response = &response
	for _, timeline := range timelines {
		if timeline.ID != "" {
			item.Timeline = append(item.Timeline, timeline)
		}
	}
	return a.state, a.markDirty(persistScopeState)
}

func (a *App) ConnectGRPCStream(collectionID, itemID, environmentID string) (AppState, error) {
	return a.connectGRPCStream(collectionID, itemID, environmentID, nil)
}

func (a *App) ConnectGRPCStreamWithPromptValues(collectionID, itemID, environmentID string, promptValues map[string]string) (AppState, error) {
	return a.connectGRPCStream(collectionID, itemID, environmentID, promptValues)
}

func (a *App) connectGRPCStream(collectionID, itemID, environmentID string, promptValues map[string]string) (AppState, error) {
	start := time.Now()
	item, collection, vars, err := a.grpcStreamRequestContext(collectionID, itemID, environmentID, promptValues)
	if err != nil {
		return AppState{}, err
	}
	if item.Type != "grpc" {
		return AppState{}, errors.New("request is not a gRPC request")
	}
	targetURL := interpolate(item.URL, vars)
	response := Response{SentAt: start, Headers: map[string]string{}, PreviewMode: "grpc-stream", RequestedURL: targetURL}
	dialConfig, err := a.grpcDialConfigForRequest(collection, item, targetURL, vars)
	if err != nil {
		response.Error = err.Error()
		response.DurationMs = time.Since(start).Milliseconds()
		return a.applyGRPCStreamResponse(collectionID, itemID, response, grpcStreamTimelineItem(item, response, "start"))
	}
	timelineEvents := []grpcStreamSessionEvent{}
	timeout := requestTimeoutMilliseconds(item.Settings.TimeoutMs, a.appTLSSettingsSnapshot().Request)
	ctx, cancel := context.WithCancel(context.Background())
	conn, err := grpc.NewClient(dialConfig.Target, dialConfig.DialOptions()...)
	if err != nil {
		cancel()
		response.Error = err.Error()
		response.DurationMs = time.Since(start).Milliseconds()
		return a.applyGRPCStreamResponse(collectionID, itemID, response, grpcStreamTimelineItem(item, response, "start"))
	}
	outgoingCtx, err := grpcexec.OutgoingContext(ctx, item, vars, a.fetchOAuth2Token)
	if err != nil {
		cancel()
		_ = conn.Close()
		response.Error = err.Error()
		response.DurationMs = time.Since(start).Milliseconds()
		return a.applyGRPCStreamResponse(collectionID, itemID, response, grpcStreamTimelineItem(item, response, "start"))
	}
	binding, err := grpcexec.ResolveMethod(outgoingCtx, conn, item, collection, vars)
	if err != nil {
		cancel()
		_ = conn.Close()
		response.Error = err.Error()
		response.DurationMs = time.Since(start).Milliseconds()
		return a.applyGRPCStreamResponse(collectionID, itemID, response, grpcStreamTimelineItem(item, response, "start"))
	}
	if !binding.Descriptor.IsStreamingClient() && !binding.Descriptor.IsStreamingServer() {
		cancel()
		_ = conn.Close()
		response.Error = "gRPC method is not streaming"
		response.DurationMs = time.Since(start).Milliseconds()
		return a.applyGRPCStreamResponse(collectionID, itemID, response, grpcStreamTimelineItem(item, response, "start"))
	}
	desc := &grpc.StreamDesc{
		StreamName:    string(binding.Descriptor.Name()),
		ServerStreams: binding.Descriptor.IsStreamingServer(),
		ClientStreams: binding.Descriptor.IsStreamingClient(),
	}
	stream, err := conn.NewStream(outgoingCtx, desc, binding.FullMethod)
	if err != nil {
		cancel()
		_ = conn.Close()
		response.Error = err.Error()
		response.DurationMs = time.Since(start).Milliseconds()
		return a.applyGRPCStreamResponse(collectionID, itemID, response, grpcStreamTimelineItem(item, response, "start"))
	}
	session := &grpcStreamSession{
		conn:           conn,
		stream:         stream,
		binding:        binding,
		cancel:         cancel,
		targetURL:      targetURL,
		streamType:     grpcStreamType(binding.Descriptor),
		status:         http.StatusOK,
		statusText:     "STREAMING",
		headers:        map[string]string{},
		trailers:       map[string]string{},
		timeout:        time.Duration(timeout) * time.Millisecond,
		openedAt:       start,
		lastActivityAt: start,
		events:         []grpcStreamSessionEvent{{Direction: "system", Type: "start", Data: binding.FullMethod, At: start}},
		eventNotify:    make(chan struct{}, 1),
		emit:           a.grpcEventEmitter(collectionID, itemID),
	}
	if binding.Descriptor.IsStreamingServer() && !binding.Descriptor.IsStreamingClient() {
		message, req, err := grpcOutboundMessageAt(item, binding, vars, 0)
		session.mu.Lock()
		if err != nil {
			session.closed = true
			session.statusText = "ERROR"
			session.closeReason = err.Error()
			session.appendEventLocked(grpcStreamSessionEvent{Direction: "system", Type: "error", Error: err.Error(), At: time.Now()})
		} else if err := stream.SendMsg(req); err != nil {
			session.closed = true
			session.statusText = "ERROR"
			session.closeReason = err.Error()
			session.appendEventLocked(grpcStreamSessionEvent{Direction: "system", Type: "error", Error: err.Error(), At: time.Now()})
		} else {
			session.requestCount++
			session.appendEventLocked(grpcStreamSessionEvent{Direction: "sent", Name: message.Name, Type: "json", Data: message.Content, At: time.Now()})
			_ = stream.CloseSend()
			session.receiveAvailableLocked()
		}
		response = session.responseLocked(session.closeReason)
		timelineEvents = append(timelineEvents, session.events...)
		session.mu.Unlock()
		if session.conn != nil {
			_ = session.conn.Close()
		}
	} else {
		if binding.Descriptor.IsStreamingClient() && binding.Descriptor.IsStreamingServer() {
			session.startReceiver()
		}
		session.mu.Lock()
		response = session.responseLocked("")
		timelineEvents = append(timelineEvents, session.events...)
		session.mu.Unlock()
		a.replaceGRPCStreamSession(grpcStreamSessionKey(collectionID, itemID), session)
	}
	return a.applyGRPCStreamResponse(collectionID, itemID, response, grpcStreamTimelineItems(item, response, timelineEvents, "start")...)
}

func (a *App) SendGRPCStreamMessage(collectionID, itemID, environmentID string, messageIndex int) (AppState, error) {
	return a.sendGRPCStreamMessage(collectionID, itemID, environmentID, messageIndex, nil)
}

func (a *App) SendGRPCStreamMessageWithPromptValues(collectionID, itemID, environmentID string, messageIndex int, promptValues map[string]string) (AppState, error) {
	return a.sendGRPCStreamMessage(collectionID, itemID, environmentID, messageIndex, promptValues)
}

func (a *App) sendGRPCStreamMessage(collectionID, itemID, environmentID string, messageIndex int, promptValues map[string]string) (AppState, error) {
	item, _, vars, err := a.grpcStreamRequestContext(collectionID, itemID, environmentID, promptValues)
	if err != nil {
		return AppState{}, err
	}
	if item.Type != "grpc" {
		return AppState{}, errors.New("request is not a gRPC request")
	}
	key := grpcStreamSessionKey(collectionID, itemID)
	a.grpcStreamMu.Lock()
	session := a.grpcStreamSessions[key]
	a.grpcStreamMu.Unlock()
	if session == nil {
		return AppState{}, errors.New("gRPC stream is not connected")
	}

	var response Response
	session.mu.Lock()
	if session.closed || session.ended {
		response = session.responseLocked(firstNonEmpty(session.closeReason, "gRPC stream is not connected"))
		session.mu.Unlock()
		a.removeGRPCStreamSessionIfSame(key, session)
		return a.applyGRPCStreamResponse(collectionID, itemID, response, grpcStreamTimelineItem(item, response, "message"))
	}
	if !session.binding.Descriptor.IsStreamingClient() {
		response = session.responseLocked("gRPC stream does not accept client messages")
		session.mu.Unlock()
		return a.applyGRPCStreamResponse(collectionID, itemID, response, grpcStreamTimelineItem(item, response, "message"))
	}
	eventStartIndex := len(session.events)
	message, req, err := grpcOutboundMessageAt(item, session.binding, vars, messageIndex)
	if err != nil {
		response = session.responseLocked(err.Error())
		session.mu.Unlock()
		return a.applyGRPCStreamResponse(collectionID, itemID, response, grpcStreamTimelineItem(item, response, "message"))
	}
	now := time.Now()
	session.appendEventLocked(grpcStreamSessionEvent{Direction: "sent", Name: message.Name, Type: "json", Data: message.Content, At: now})
	session.requestCount++
	session.lastActivityAt = now
	responseCountBeforeSend := session.responseCount
	hasResponseStream := session.binding.Descriptor.IsStreamingServer()
	session.notifyEventLocked()
	if err := session.stream.SendMsg(req); err != nil {
		st := status.Convert(err)
		session.closed = true
		session.status = int(st.Code())
		session.statusText = st.Code().String()
		session.closeReason = firstNonEmpty(st.Message(), err.Error())
		session.appendEventLocked(grpcStreamSessionEvent{Direction: "system", Type: "error", Error: session.closeReason, At: time.Now()})
		if session.conn != nil {
			_ = session.conn.Close()
		}
		response = session.responseLocked(session.closeReason)
		timelineEvents := append([]grpcStreamSessionEvent(nil), session.events[eventStartIndex:]...)
		session.mu.Unlock()
		a.removeGRPCStreamSessionIfSame(key, session)
		return a.applyGRPCStreamResponse(collectionID, itemID, response, grpcStreamTimelineItems(item, response, timelineEvents, "message")...)
	}
	session.mu.Unlock()
	if hasResponseStream {
		session.waitForResponseAfter(responseCountBeforeSend, grpcStreamReceiveWait(session.timeout))
	}
	session.mu.Lock()
	response = session.responseLocked("")
	timelineEvents := append([]grpcStreamSessionEvent(nil), session.events[eventStartIndex:]...)
	session.mu.Unlock()
	return a.applyGRPCStreamResponse(collectionID, itemID, response, grpcStreamTimelineItems(item, response, timelineEvents, "message")...)
}

func (a *App) EndGRPCStream(collectionID, itemID string) (AppState, error) {
	key := grpcStreamSessionKey(collectionID, itemID)
	session := a.popGRPCStreamSession(key)
	if session == nil {
		a.mu.Lock()
		defer a.mu.Unlock()
		return a.state, nil
	}
	var receiveDone <-chan struct{}
	receiveSynchronously := false
	session.mu.Lock()
	eventStartIndex := len(session.events)
	if !session.closed && !session.ended {
		_ = session.stream.CloseSend()
		session.appendEventLocked(grpcStreamSessionEvent{Direction: "system", Type: "end", Data: "client stream ended", At: time.Now()})
		session.notifyEventLocked()
		if session.receiverStarted {
			receiveDone = session.receiveDone
		} else {
			receiveSynchronously = true
		}
	}
	if receiveSynchronously {
		session.receiveAvailableLocked()
	}
	session.mu.Unlock()
	if receiveDone != nil {
		select {
		case <-receiveDone:
		case <-time.After(grpcStreamEndWait(session.timeout)):
			session.close("end timed out")
		}
	}
	session.mu.Lock()
	response := session.responseLocked(session.closeReason)
	timelineEvents := append([]grpcStreamSessionEvent(nil), session.events[eventStartIndex:]...)
	session.mu.Unlock()
	item, _, _, err := a.grpcStreamRequestContext(collectionID, itemID, "", nil)
	if err != nil {
		return AppState{}, err
	}
	return a.applyGRPCStreamResponse(collectionID, itemID, response, grpcStreamTimelineItems(item, response, timelineEvents, "end")...)
}

func (a *App) CancelGRPCStream(collectionID, itemID string) (AppState, error) {
	key := grpcStreamSessionKey(collectionID, itemID)
	session := a.popGRPCStreamSession(key)
	if session == nil {
		a.mu.Lock()
		defer a.mu.Unlock()
		return a.state, nil
	}
	session.mu.Lock()
	eventStartIndex := len(session.events)
	session.mu.Unlock()
	session.close("cancelled")
	session.mu.Lock()
	response := session.responseLocked(firstNonEmpty(session.closeReason, "cancelled"))
	timelineEvents := append([]grpcStreamSessionEvent(nil), session.events[eventStartIndex:]...)
	session.mu.Unlock()
	item, _, _, err := a.grpcStreamRequestContext(collectionID, itemID, "", nil)
	if err != nil {
		return AppState{}, err
	}
	return a.applyGRPCStreamResponse(collectionID, itemID, response, grpcStreamTimelineItems(item, response, timelineEvents, "cancel")...)
}

func grpcStreamTimelineItem(item RequestItem, response Response, action string) TimelineItem {
	statusText := cleanStatusText(response.Status, response.StatusText)
	message := fmt.Sprintf("gRPC stream %s %s", action, response.RequestedURL)
	if method := strings.TrimSpace(response.Headers["grpc-method"]); method != "" {
		message = fmt.Sprintf("gRPC stream %s %s %s", action, strings.TrimPrefix(method, "/"), response.RequestedURL)
	}
	if requestCount := strings.TrimSpace(response.Headers["grpc-request-count"]); requestCount != "" {
		message += " sent " + requestCount
	}
	if responseCount := strings.TrimSpace(response.Headers["grpc-response-count"]); responseCount != "" {
		message += " received " + responseCount
	}
	if response.Error != "" {
		message += " failed"
	}
	return TimelineItem{
		ID:         newID("timeline"),
		Kind:       "request",
		Message:    message,
		At:         time.Now(),
		Duration:   response.DurationMs,
		RequestID:  item.ID,
		Source:     "grpc",
		Method:     "CALL",
		URL:        response.RequestedURL,
		Status:     response.Status,
		StatusText: statusText,
		Error:      response.Error,
	}
}

func grpcStreamTimelineItems(item RequestItem, response Response, events []grpcStreamSessionEvent, action string) []TimelineItem {
	if len(events) == 0 {
		return []TimelineItem{grpcStreamTimelineItem(item, response, action)}
	}
	rows := make([]TimelineItem, 0, len(events)+2)
	terminalAt := time.Time{}
	terminal := false
	for _, event := range events {
		if event.Direction == "system" && event.Type == "end" && strings.Contains(strings.ToLower(event.Data), "client stream ended") {
			continue
		}
		row := grpcStreamTimelineItemForEvent(item, response, event)
		if row.ID == "" {
			continue
		}
		rows = append(rows, row)
		if row.EventType == "end" || row.EventType == "error" || row.EventType == "cancel" {
			terminal = true
			terminalAt = row.At
		}
	}
	if terminal {
		if len(response.Metadata) > 0 {
			rows = append(rows, grpcStreamMetadataTimelineItem(item, response, terminalAt))
		}
		if response.Headers["grpc-status"] != "" || len(response.Trailers) > 0 || response.Error != "" {
			rows = append(rows, grpcStreamStatusTimelineItem(item, response, terminalAt))
		}
	}
	if len(rows) == 0 {
		return []TimelineItem{grpcStreamTimelineItem(item, response, action)}
	}
	return rows
}

func grpcStreamTimelineItemForEvent(item RequestItem, response Response, event grpcStreamSessionEvent) TimelineItem {
	eventType := ""
	message := ""
	payload := event.Data
	statusText := cleanStatusText(response.Status, response.StatusText)
	methodName := strings.TrimPrefix(firstNonEmpty(response.Headers["grpc-method"], item.Method), "/")
	streamType := firstNonEmpty(response.Headers["grpc-stream"], grpcStreamTypeLabelFromStorage(item.GrpcMethodType), "stream")
	switch {
	case event.Direction == "system" && event.Type == "start":
		eventType = "request"
		message = fmt.Sprintf("gRPC request %s %s (%s stream)", methodName, response.RequestedURL, streamType)
	case event.Direction == "sent":
		eventType = "message"
		message = fmt.Sprintf("gRPC message %s %s", firstNonEmpty(event.Name, "message"), response.RequestedURL)
	case event.Direction == "received":
		eventType = "response"
		message = fmt.Sprintf("Response Message #%s %s", grpcResponseNumber(event.Name), response.RequestedURL)
	case event.Direction == "system" && event.Type == "end":
		eventType = "end"
		message = fmt.Sprintf("Stream Ended %s", response.RequestedURL)
		if responseCount := strings.TrimSpace(response.Headers["grpc-response-count"]); responseCount != "" {
			message += " received " + responseCount
		}
	case event.Direction == "system" && event.Type == "cancel":
		eventType = "cancel"
		message = fmt.Sprintf("Stream Cancelled %s", response.RequestedURL)
		payload = firstNonEmpty(event.Data, event.Error, response.Error)
	case event.Direction == "system" && event.Type == "error":
		eventType = "error"
		message = fmt.Sprintf("gRPC error %s", response.RequestedURL)
		payload = firstNonEmpty(event.Error, event.Data, response.Error)
	default:
		return TimelineItem{}
	}
	if event.At.IsZero() {
		event.At = time.Now()
	}
	return TimelineItem{
		ID:         newID("timeline"),
		Kind:       "request",
		EventType:  eventType,
		EventName:  event.Name,
		Message:    message,
		At:         event.At,
		Duration:   response.DurationMs,
		RequestID:  item.ID,
		Source:     "grpc",
		Method:     "CALL",
		URL:        response.RequestedURL,
		Status:     response.Status,
		StatusText: statusText,
		Error:      event.Error,
		Payload:    payload,
	}
}

func grpcStreamMetadataTimelineItem(item RequestItem, response Response, at time.Time) TimelineItem {
	if at.IsZero() {
		at = time.Now()
	}
	return TimelineItem{
		ID:         newID("timeline"),
		Kind:       "request",
		EventType:  "metadata",
		Message:    "gRPC response metadata " + response.RequestedURL,
		At:         at.Add(time.Millisecond),
		Duration:   response.DurationMs,
		RequestID:  item.ID,
		Source:     "grpc",
		Method:     "CALL",
		URL:        response.RequestedURL,
		Status:     response.Status,
		StatusText: cleanStatusText(response.Status, response.StatusText),
		Metadata:   response.Metadata,
	}
}

func grpcStreamStatusTimelineItem(item RequestItem, response Response, at time.Time) TimelineItem {
	if at.IsZero() {
		at = time.Now()
	}
	statusValue := firstNonEmpty(response.Headers["grpc-status"], strconv.Itoa(response.Status))
	statusText := cleanStatusText(response.Status, response.StatusText)
	payload := "grpc-status: " + statusValue
	if statusText != "" {
		payload += "\nstatus-text: " + statusText
	}
	return TimelineItem{
		ID:         newID("timeline"),
		Kind:       "request",
		EventType:  "status",
		Message:    "gRPC status " + statusValue + " " + response.RequestedURL,
		At:         at.Add(2 * time.Millisecond),
		Duration:   response.DurationMs,
		RequestID:  item.ID,
		Source:     "grpc",
		Method:     "CALL",
		URL:        response.RequestedURL,
		Status:     response.Status,
		StatusText: statusText,
		Payload:    payload,
		Trailers:   response.Trailers,
	}
}

func grpcStreamType(method protoreflect.MethodDescriptor) string {
	switch {
	case method.IsStreamingClient() && method.IsStreamingServer():
		return "bidi"
	case method.IsStreamingClient():
		return "client"
	case method.IsStreamingServer():
		return "server"
	default:
		return "unary"
	}
}

func grpcStreamTypeLabelFromStorage(methodType string) string {
	switch strings.TrimSpace(strings.ToLower(methodType)) {
	case "client-streaming":
		return "client"
	case "server-streaming":
		return "server"
	case "bidi-streaming":
		return "bidi"
	default:
		return "unary"
	}
}
