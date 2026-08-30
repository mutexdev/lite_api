package core

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"

	"github.com/mutexdev/lite_api/internal/scripting"
	"github.com/mutexdev/lite_api/internal/transport"
	"github.com/mutexdev/lite_api/internal/wsexec"
)

func websocketSessionKey(collectionID, itemID string) string {
	return collectionID + "\x00" + itemID
}

func (a *App) websocketRequestContext(collectionID, itemID, environmentID string, promptValues map[string]string) (RequestItem, map[string]string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return RequestItem{}, nil, err
	}
	ws, collection, err := a.findCollectionWithWorkspaceLocked(collectionID)
	if err != nil {
		return RequestItem{}, nil, err
	}
	item, err := findItem(collection, itemID)
	if err != nil {
		return RequestItem{}, nil, err
	}
	requestCopy := scripting.EffectiveRequest(*collection, *item)
	scriptVariables := scripting.NewScriptVariableContext(scripting.ActiveGlobalEnvironmentsForWorkspace(*ws), collection, environmentID, requestCopy, promptValues, ws.Path)
	return requestCopy, scriptVariables.Combined, nil
}

func (a *App) replaceWebSocketSession(key string, session *websocketSession) {
	a.websocketMu.Lock()
	previous := a.websocketSessions[key]
	a.websocketSessions[key] = session
	a.websocketMu.Unlock()
	if previous != nil {
		previous.close("reconnected")
	}
}

func (a *App) popWebSocketSession(key string) *websocketSession {
	a.websocketMu.Lock()
	session := a.websocketSessions[key]
	delete(a.websocketSessions, key)
	a.websocketMu.Unlock()
	return session
}

func (a *App) removeWebSocketSessionIfSame(key string, session *websocketSession) {
	a.websocketMu.Lock()
	if a.websocketSessions[key] == session {
		delete(a.websocketSessions, key)
	}
	a.websocketMu.Unlock()
}

func (session *websocketSession) close(reason string) {
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.closed {
		return
	}
	session.closed = true
	session.closeReason = strings.TrimSpace(reason)
	session.lastActivityAt = time.Now()
	if session.done != nil && !session.doneClosed {
		close(session.done)
		session.doneClosed = true
	}
	session.appendEventLocked(websocketSessionEvent{
		Direction: "system",
		Type:      "close",
		Data:      session.closeReason,
		At:        session.lastActivityAt,
	})
	_ = session.conn.Close()
}

func (session *websocketSession) startKeepAlive() {
	if session.keepAliveEvery <= 0 || session.done == nil {
		return
	}
	ticker := time.NewTicker(session.keepAliveEvery)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-session.done:
				return
			case at := <-ticker.C:
				session.mu.Lock()
				if session.closed {
					session.mu.Unlock()
					return
				}
				deadline := at.Add(5 * time.Second)
				if err := session.conn.WriteControl(websocket.PingMessage, nil, deadline); err != nil {
					session.closed = true
					session.closeReason = err.Error()
					if !session.doneClosed {
						close(session.done)
						session.doneClosed = true
					}
					_ = session.conn.Close()
					session.appendEventLocked(websocketSessionEvent{
						Direction: "system",
						Type:      "ping",
						Error:     err.Error(),
						At:        at,
					})
					session.lastActivityAt = at
					session.mu.Unlock()
					return
				}
				session.appendEventLocked(websocketSessionEvent{
					Direction: "system",
					Type:      "ping",
					Data:      "keep-alive",
					At:        at,
				})
				session.lastActivityAt = at
				session.mu.Unlock()
			}
		}
	}()
}

func (session *websocketSession) responseLocked(errMessage string) Response {
	headers := cloneStringMap(session.headers)
	connected := !session.closed
	headers["x-websocket-connected"] = strconv.FormatBool(connected)
	headers["x-websocket-events"] = strconv.Itoa(len(session.events))
	if session.keepAliveEvery > 0 {
		headers["x-websocket-keep-alive-interval"] = strconv.Itoa(int(session.keepAliveEvery / time.Millisecond))
	}
	if session.closeReason != "" {
		headers["x-websocket-close-reason"] = session.closeReason
	}
	// US-021: marshal the trailing window, not the whole log. x-websocket-events
	// still reports the true total above, so the count a caller sees is the real
	// one and this header says how much of it the body omits.
	tail, omitted := websocketEventTail(session.events)
	if omitted > 0 {
		headers["x-websocket-events-omitted"] = strconv.Itoa(omitted)
	}
	body, err := json.MarshalIndent(tail, "", "  ")
	if err != nil {
		body = []byte("[]")
		if errMessage == "" {
			errMessage = err.Error()
		}
	}
	return Response{
		Status:       session.status,
		StatusText:   session.statusText,
		Headers:      headers,
		Body:         string(body),
		BodyBase64:   base64.StdEncoding.EncodeToString(body),
		Size:         len(body),
		DurationMs:   time.Since(session.openedAt).Milliseconds(),
		SentAt:       session.openedAt,
		RequestedURL: session.targetURL,
		PreviewMode:  "websocket",
		Error:        errMessage,
	}
}

func (a *App) applyWebSocketResponse(collectionID, itemID string, response Response, timeline TimelineItem) (AppState, error) {
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
	if timeline.ID != "" {
		item.Timeline = append(item.Timeline, timeline)
	}
	return a.state, a.markDirty(persistScopeState)
}

func (a *App) ConnectWebSocket(collectionID, itemID, environmentID string) (AppState, error) {
	return a.connectWebSocket(collectionID, itemID, environmentID, nil)
}

func (a *App) ConnectWebSocketWithPromptValues(collectionID, itemID, environmentID string, promptValues map[string]string) (AppState, error) {
	return a.connectWebSocket(collectionID, itemID, environmentID, promptValues)
}

func (a *App) connectWebSocket(collectionID, itemID, environmentID string, promptValues map[string]string) (AppState, error) {
	start := time.Now()
	item, vars, err := a.websocketRequestContext(collectionID, itemID, environmentID, promptValues)
	if err != nil {
		return AppState{}, err
	}
	if item.Type != "websocket" {
		return AppState{}, errors.New("request is not a WebSocket request")
	}
	targetURL := wsexec.TargetURL(item, vars)
	headers := wsexec.Headers(item, vars)
	timeout := requestTimeoutMilliseconds(item.Settings.TimeoutMs, a.appTLSSettingsSnapshot().Request)
	response := Response{SentAt: start, Headers: map[string]string{}, PreviewMode: "websocket", RequestedURL: targetURL}
	// THE UI MARKER (§4.5). This is a Wails binding: a live WebSocket session
	// the user opened from the app, which no MCP run can reach. It is labeled
	// explicitly rather than left to be inferred from the absence of a policy —
	// that inference is exactly what §4.5 exists to prevent — and DialContext
	// with a background context is what Dial is defined as, so the handshake is
	// byte-identical to before.
	dialContext := mcpContextWithUIProvenance(context.Background())
	dialer, err := a.websocketDialer(dialContext, collectionID, item, targetURL, vars, time.Duration(timeout)*time.Millisecond)
	if err != nil {
		response.Error = err.Error()
		response.DurationMs = time.Since(start).Milliseconds()
		return a.applyWebSocketResponse(collectionID, itemID, response, websocketTimelineItem(item, response, "connect"))
	}
	conn, res, err := dialer.DialContext(dialContext, targetURL, headers)
	if res != nil {
		response.Status = res.StatusCode
		response.StatusText = res.Status
		for name, values := range res.Header {
			response.Headers[name] = strings.Join(values, ", ")
		}
	}
	if err != nil {
		response.Error = err.Error()
		response.DurationMs = time.Since(start).Milliseconds()
		return a.applyWebSocketResponse(collectionID, itemID, response, websocketTimelineItem(item, response, "connect"))
	}
	session := &websocketSession{
		conn:           conn,
		targetURL:      targetURL,
		status:         response.Status,
		statusText:     response.StatusText,
		headers:        cloneStringMap(response.Headers),
		timeout:        time.Duration(timeout) * time.Millisecond,
		keepAliveEvery: wsexec.KeepAliveInterval(item.Settings),
		openedAt:       start,
		lastActivityAt: start,
		events:         []websocketSessionEvent{},
		done:           make(chan struct{}),
		emit:           a.websocketEventEmitter(collectionID, itemID),
	}
	session.mu.Lock()
	response = session.responseLocked("")
	session.mu.Unlock()
	a.replaceWebSocketSession(websocketSessionKey(collectionID, itemID), session)
	session.startKeepAlive()
	return a.applyWebSocketResponse(collectionID, itemID, response, websocketTimelineItem(item, response, "connect"))
}

func (a *App) SendWebSocketMessage(collectionID, itemID, environmentID string, messageIndex int) (AppState, error) {
	return a.sendWebSocketMessage(collectionID, itemID, environmentID, messageIndex, nil)
}

func (a *App) SendWebSocketMessageWithPromptValues(collectionID, itemID, environmentID string, messageIndex int, promptValues map[string]string) (AppState, error) {
	return a.sendWebSocketMessage(collectionID, itemID, environmentID, messageIndex, promptValues)
}

func (a *App) sendWebSocketMessage(collectionID, itemID, environmentID string, messageIndex int, promptValues map[string]string) (AppState, error) {
	item, vars, err := a.websocketRequestContext(collectionID, itemID, environmentID, promptValues)
	if err != nil {
		return AppState{}, err
	}
	if item.Type != "websocket" {
		return AppState{}, errors.New("request is not a WebSocket request")
	}
	message, err := wsexec.OutboundMessageAt(item, vars, messageIndex)
	if err != nil {
		return AppState{}, err
	}
	key := websocketSessionKey(collectionID, itemID)
	a.websocketMu.Lock()
	session := a.websocketSessions[key]
	a.websocketMu.Unlock()
	if session == nil {
		return AppState{}, errors.New("WebSocket is not connected")
	}

	var response Response
	var shouldRemove bool
	session.mu.Lock()
	if session.closed {
		errMessage := firstNonEmpty(session.closeReason, "WebSocket is not connected")
		response = session.responseLocked(errMessage)
		shouldRemove = true
	} else {
		now := time.Now()
		frameType, payload := wsexec.FramePayload(message)
		sent := websocketSessionEvent{
			Direction: "sent",
			Name:      message.Name,
			Type:      wsexec.MessageTypeName(frameType),
			Data:      string(payload),
			At:        now,
		}
		if frameType == websocket.BinaryMessage {
			sent.DataBase64 = base64.StdEncoding.EncodeToString(payload)
			sent.DataHex = hex.EncodeToString(payload)
		}
		session.appendEventLocked(sent)
		session.lastActivityAt = now
		if err := session.conn.WriteMessage(frameType, payload); err != nil {
			session.closed = true
			session.closeReason = err.Error()
			_ = session.conn.Close()
			response = session.responseLocked(err.Error())
			shouldRemove = true
		} else {
			_ = session.conn.SetReadDeadline(time.Now().Add(session.timeout))
			responseType, payload, err := session.conn.ReadMessage()
			if err != nil {
				session.closed = true
				session.closeReason = err.Error()
				_ = session.conn.Close()
				response = session.responseLocked(err.Error())
				shouldRemove = true
			} else {
				received := websocketSessionEvent{
					Direction: "received",
					Name:      message.Name,
					Type:      wsexec.MessageTypeName(responseType),
					Data:      string(payload),
					At:        time.Now(),
				}
				if responseType == websocket.BinaryMessage {
					received.DataBase64 = base64.StdEncoding.EncodeToString(payload)
					received.DataHex = hex.EncodeToString(payload)
				}
				session.appendEventLocked(received)
				session.lastActivityAt = received.At
				response = session.responseLocked("")
			}
		}
	}
	session.mu.Unlock()
	if shouldRemove {
		a.removeWebSocketSessionIfSame(key, session)
	}
	return a.applyWebSocketResponse(collectionID, itemID, response, websocketTimelineItem(item, response, "message"))
}

func (a *App) DisconnectWebSocket(collectionID, itemID string) (AppState, error) {
	key := websocketSessionKey(collectionID, itemID)
	session := a.popWebSocketSession(key)
	if session == nil {
		a.mu.Lock()
		defer a.mu.Unlock()
		return a.state, nil
	}
	session.close("disconnected")
	session.mu.Lock()
	response := session.responseLocked("")
	session.mu.Unlock()
	item, _, err := a.websocketRequestContext(collectionID, itemID, "", nil)
	if err != nil {
		return AppState{}, err
	}
	return a.applyWebSocketResponse(collectionID, itemID, response, websocketTimelineItem(item, response, "disconnect"))
}

func websocketTimelineItem(item RequestItem, response Response, action string) TimelineItem {
	statusText := cleanStatusText(response.Status, response.StatusText)
	message := fmt.Sprintf("WebSocket %s %s", action, response.RequestedURL)
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
		Source:     "websocket",
		Method:     "CONNECT",
		URL:        response.RequestedURL,
		Status:     response.Status,
		StatusText: statusText,
		Error:      response.Error,
	}
}

// executeWebSocket is the context-free delegate. The send seam still calls this
// name; the caller switch to executeWebSocketContext lands with the send path,
// and until then this preserves today's behaviour exactly — a background
// context, no policy, no checkpoint.
func (a *App) executeWebSocket(collectionID string, item RequestItem, vars map[string]string) Response {
	return a.executeWebSocketContext(context.Background(), collectionID, item, vars)
}

// executeWebSocketContext is the WebSocket send with the request's context
// carried all the way to the handshake.
//
// TWO THINGS CHANGE HERE, and they are the same thing seen from two sides.
//
// THE HANDSHAKE IS NOW CANCELLABLE. gorilla's Dial runs the handshake on
// context.Background(): a cancelled send left the dial running to its own
// timeout, and no caller could stop it. DialContext is the same dial with the
// send's context, so cancellation and deadlines reach the connection attempt.
//
// AND THE HANDSHAKE IS NOW CHECKED. A WebSocket handshake IS an HTTP request —
// it carries the request's headers, cookies and auth to whatever host the
// resolved URL names — so under MCP provenance it goes through the same
// blocking checkpoint (§4.3, §5 row 10) as any other main egress, BEFORE the
// dialer opens a socket. Frames then ride a connection that was authorized, so
// nothing per-frame is needed: a connection cannot change its peer.
func (a *App) executeWebSocketContext(ctx context.Context, collectionID string, item RequestItem, vars map[string]string) Response {
	if ctx == nil {
		ctx = context.Background()
	}
	start := time.Now()
	result := Response{SentAt: start, Headers: map[string]string{}, PreviewMode: "websocket"}
	targetURL := wsexec.TargetURL(item, vars)
	result.RequestedURL = targetURL

	headers := wsexec.Headers(item, vars)

	if policy := mcpPolicyFromContext(ctx); policy != nil {
		// A URL that does not resolve to an origin yields the zero Origin,
		// which Authorize denies with its own "did not resolve" message. That
		// is deliberate: an unresolvable destination is one nothing checked.
		origin, _ := OriginOfURL(targetURL)
		if err := policy.Authorize(ctx, origin, egressKindMain); err != nil {
			result.Error = err.Error()
			result.DurationMs = time.Since(start).Milliseconds()
			return result
		}
	}

	timeout := requestTimeoutMilliseconds(item.Settings.TimeoutMs, a.appTLSSettingsSnapshot().Request)
	dialer, err := a.websocketDialer(ctx, collectionID, item, targetURL, vars, time.Duration(timeout)*time.Millisecond)
	if err != nil {
		result.Error = err.Error()
		result.DurationMs = time.Since(start).Milliseconds()
		return result
	}
	conn, res, err := dialer.DialContext(ctx, targetURL, headers)
	if res != nil {
		result.Status = res.StatusCode
		result.StatusText = res.Status
		for name, values := range res.Header {
			result.Headers[name] = strings.Join(values, ", ")
		}
	}
	if err != nil {
		result.Error = err.Error()
		result.DurationMs = time.Since(start).Milliseconds()
		return result
	}
	defer func() { _ = conn.Close() }()

	messages := wsexec.OutboundMessages(item, vars)
	if len(messages) == 0 {
		result.DurationMs = time.Since(start).Milliseconds()
		return result
	}

	events := make([]map[string]string, 0, len(messages))
	var singlePayload []byte
	var singleResponseType int
	for _, message := range messages {
		frameType, payload := wsexec.FramePayload(message)
		if err := conn.WriteMessage(frameType, payload); err != nil {
			result.Error = err.Error()
			result.DurationMs = time.Since(start).Milliseconds()
			return result
		}
		_ = conn.SetReadDeadline(time.Now().Add(time.Duration(timeout) * time.Millisecond))
		responseType, payload, err := conn.ReadMessage()
		if err != nil {
			result.Error = err.Error()
			result.DurationMs = time.Since(start).Milliseconds()
			return result
		}
		event := map[string]string{
			"name": message.Name,
			"type": wsexec.MessageTypeName(responseType),
			"data": string(payload),
		}
		if responseType == websocket.BinaryMessage {
			event["dataBase64"] = base64.StdEncoding.EncodeToString(payload)
			event["dataHex"] = hex.EncodeToString(payload)
		}
		events = append(events, event)
		singlePayload = payload
		singleResponseType = responseType
	}

	var body []byte
	if len(events) == 1 {
		body = singlePayload
		result.Headers["x-websocket-message-type"] = wsexec.MessageTypeName(singleResponseType)
		if singleResponseType == websocket.BinaryMessage {
			result.Headers["x-websocket-message-base64"] = base64.StdEncoding.EncodeToString(singlePayload)
			result.Headers["x-websocket-message-hex"] = hex.EncodeToString(singlePayload)
		}
	} else {
		data, err := json.MarshalIndent(events, "", "  ")
		if err != nil {
			result.Error = err.Error()
			result.DurationMs = time.Since(start).Milliseconds()
			return result
		}
		body = data
	}
	result.Body = string(body)
	result.BodyBase64 = base64.StdEncoding.EncodeToString(body)
	result.Size = len(body)
	result.Headers["x-websocket-messages-sent"] = strconv.Itoa(len(messages))
	result.DurationMs = time.Since(start).Milliseconds()
	result.Assertions = evaluateAssertions(item.Assertions, result)
	return result
}

// websocketDialer builds the dialer one handshake travels on.
//
// THE MCP BRANCH IS §4.4 AT ITS SECOND SEAM. A WebSocket handshake reaches the
// network through gorilla's dialer rather than an http.Client, so it never
// touches requestTransport — but it makes the same two decisions from the same
// two agent-shaped inputs (`targetURL`, `vars`), and therefore needs the same
// answer. It gets it from the same function: mcpTransportPosture decides, and
// applyToTransport writes the decision onto the clone whose Proxy and
// TLSClientConfig the dialer lifts out. A refusal — PAC, or a certificate
// meeting an https proxy — comes back as an error and no socket is opened.
//
// The UI branch below is the shipped chain, untouched.
func (a *App) websocketDialer(ctx context.Context, collectionID string, item RequestItem, targetURL string, vars map[string]string, timeout time.Duration) (websocket.Dialer, error) {
	baseTransport := http.RoundTripper(http.DefaultTransport)
	if a.httpClient != nil && a.httpClient.Transport != nil {
		baseTransport = a.httpClient.Transport
	}
	tlsSettings := a.appTLSSettingsSnapshot()
	verifyTLS := requestTLSVerificationEnabled(tlsSettings.Request, item.Settings.VerifyTLS)
	var tlsErr error
	baseTransport, tlsErr = transportWithAppTLSSettings(baseTransport, tlsSettings, verifyTLS)
	if tlsErr != nil {
		return websocket.Dialer{}, tlsErr
	}
	if policy := mcpPolicyFromContext(ctx); policy != nil {
		posture, err := a.mcpTransportPosture(ctx, policy, collectionID, targetURL)
		if err != nil {
			return websocket.Dialer{}, err
		}
		cloned := transport.CloneHTTPTransport(baseTransport)
		posture.applyToTransport(cloned, targetURL)
		return websocket.Dialer{
			HandshakeTimeout: timeout,
			Proxy:            cloned.Proxy,
			TLSClientConfig:  cloned.TLSClientConfig,
			NetDialContext:   cloned.DialContext,
		}, nil
	}
	if collectionPath, certs, ok := a.collectionClientCertificateConfig(collectionID); ok {
		var certErr error
		baseTransport, certErr = transport.WithClientCertificate(baseTransport, collectionPath, certs, targetURL, vars)
		if certErr != nil {
			return websocket.Dialer{}, certErr
		}
	}
	proxyResolution := a.collectionProxyResolution(collectionID)
	var proxyErr error
	baseTransport, proxyErr = transport.WithProxyResolution(baseTransport, proxyResolution, targetURL, vars)
	if proxyErr != nil {
		return websocket.Dialer{}, proxyErr
	}
	transport := transport.CloneHTTPTransport(baseTransport)
	return websocket.Dialer{
		HandshakeTimeout: timeout,
		Proxy:            transport.Proxy,
		TLSClientConfig:  transport.TLSClientConfig,
		NetDialContext:   transport.DialContext,
	}, nil
}
