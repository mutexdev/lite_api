package core

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/dynamicpb"

	"github.com/mutexdev/lite_api/internal/grpcexec"
	"github.com/mutexdev/lite_api/internal/transport"
)

// grpcMethodBinding and grpcDialConfig moved to internal/grpcexec.
type grpcMethodBinding = grpcexec.MethodBinding

type grpcDialConfig = grpcexec.DialConfig

func (a *App) executeGRPC(parent context.Context, collection Collection, item RequestItem, vars map[string]string) Response {
	start := time.Now()
	result := Response{SentAt: start, Headers: map[string]string{}, PreviewMode: "json"}
	targetURL := interpolate(item.URL, vars)
	result.RequestedURL = targetURL

	dialConfig, err := a.grpcDialConfigForRequest(collection, item, targetURL, vars)
	if err != nil {
		result.Error = err.Error()
		result.DurationMs = time.Since(start).Milliseconds()
		return result
	}
	timeout := requestTimeoutMilliseconds(item.Settings.TimeoutMs, a.appTLSSettingsSnapshot().Request)
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, time.Duration(timeout)*time.Millisecond)
	defer cancel()

	conn, err := grpc.NewClient(dialConfig.Target, dialConfig.DialOptions()...)
	if err != nil {
		result.Error = err.Error()
		result.DurationMs = time.Since(start).Milliseconds()
		return result
	}
	defer func() { _ = conn.Close() }()

	ctx, err = grpcexec.OutgoingContext(ctx, item, vars, a.fetchOAuth2Token)
	if err != nil {
		result.Error = err.Error()
		result.DurationMs = time.Since(start).Milliseconds()
		return result
	}
	binding, err := grpcexec.ResolveMethod(ctx, conn, item, collection, vars)
	if err != nil {
		result.Error = err.Error()
		result.DurationMs = time.Since(start).Milliseconds()
		return result
	}
	if binding.Descriptor.IsStreamingClient() || binding.Descriptor.IsStreamingServer() {
		executeGRPCStream(&result, conn, binding, item, vars, ctx, start)
		return result
	}

	req := dynamicpb.NewMessage(binding.Descriptor.Input())
	content := grpcexec.RequestContent(item, vars)
	if err := (protojson.UnmarshalOptions{DiscardUnknown: false}).Unmarshal([]byte(content), req); err != nil {
		result.Error = "parse gRPC request JSON: " + err.Error()
		result.DurationMs = time.Since(start).Milliseconds()
		return result
	}
	res := dynamicpb.NewMessage(binding.Descriptor.Output())
	var headers metadata.MD
	var trailers metadata.MD
	err = conn.Invoke(ctx, binding.FullMethod, req, res, grpc.Header(&headers), grpc.Trailer(&trailers))
	grpcexec.AddMetadata(result.Headers, "", headers)
	grpcexec.AddMetadata(result.Headers, "trailer-", trailers)
	result.Metadata = grpcexec.MetadataRows(headers)
	result.Trailers = grpcexec.MetadataRows(trailers)
	if err != nil {
		st := status.Convert(err)
		result.Status = int(st.Code())
		result.StatusText = st.Code().String()
		result.Error = st.Message()
		result.Headers["grpc-status"] = strconv.Itoa(int(st.Code()))
		result.DurationMs = time.Since(start).Milliseconds()
		return result
	}

	body, err := (protojson.MarshalOptions{Multiline: true, Indent: "  ", EmitUnpopulated: true}).Marshal(res)
	if err != nil {
		result.Error = "format gRPC response JSON: " + err.Error()
		result.DurationMs = time.Since(start).Milliseconds()
		return result
	}
	result.Status = http.StatusOK
	result.StatusText = "OK"
	result.Headers["grpc-status"] = "0"
	result.Headers["grpc-method"] = binding.FullMethod
	result.Headers["grpc-stream"] = "unary"
	result.Headers["grpc-request-count"] = "1"
	result.Headers["grpc-response-count"] = "1"
	result.Body = string(body)
	result.BodyBase64 = base64.StdEncoding.EncodeToString(body)
	result.Size = len(body)
	result.DurationMs = time.Since(start).Milliseconds()
	result.Assertions = evaluateAssertions(item.Assertions, result)
	return result
}

func applyGRPCError(result *Response, err error, start time.Time) {
	st := status.Convert(err)
	result.Status = int(st.Code())
	result.StatusText = st.Code().String()
	result.Error = st.Message()
	if result.Error == "" {
		result.Error = err.Error()
	}
	result.Headers["grpc-status"] = strconv.Itoa(int(st.Code()))
	result.DurationMs = time.Since(start).Milliseconds()
}

func (session *grpcStreamSession) close(reason string) {
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.closed {
		return
	}
	session.closed = true
	session.closeReason = strings.TrimSpace(reason)
	session.lastActivityAt = time.Now()
	if session.cancel != nil {
		session.cancel()
	}
	if session.conn != nil {
		_ = session.conn.Close()
	}
	session.appendEventLocked(grpcStreamSessionEvent{
		Direction: "system",
		Type:      "cancel",
		Data:      session.closeReason,
		At:        session.lastActivityAt,
	})
	if session.status == 0 || session.status == http.StatusOK {
		session.status = 1
		session.statusText = "CANCELLED"
	}
}

func (session *grpcStreamSession) responseLocked(errMessage string) Response {
	headers := cloneStringMap(session.headers)
	if headers == nil {
		headers = map[string]string{}
	}
	for name, value := range session.trailers {
		headers["trailer-"+name] = value
	}
	headers["x-grpc-stream-connected"] = strconv.FormatBool(!session.closed && !session.ended)
	headers["x-grpc-stream-ended"] = strconv.FormatBool(session.ended)
	headers["x-grpc-stream-events"] = strconv.Itoa(len(session.events))
	headers["grpc-method"] = session.binding.FullMethod
	headers["grpc-stream"] = session.streamType
	headers["grpc-request-count"] = strconv.Itoa(session.requestCount)
	headers["grpc-response-count"] = strconv.Itoa(session.responseCount)
	if session.ended && errMessage == "" && headers["grpc-status"] == "" {
		headers["grpc-status"] = "0"
	}
	if session.closeReason != "" {
		headers["x-grpc-stream-close-reason"] = session.closeReason
	}
	// US-022: see the WebSocket equivalent. x-grpc-stream-events above still
	// carries the true total.
	tail, omitted := grpcEventTail(session.events)
	if omitted > 0 {
		headers["x-grpc-stream-events-omitted"] = strconv.Itoa(omitted)
	}
	body, err := json.MarshalIndent(tail, "", "  ")
	if err != nil {
		body = []byte("[]")
		if errMessage == "" {
			errMessage = err.Error()
		}
	}
	statusCode := session.status
	if statusCode == 0 {
		statusCode = http.StatusOK
	}
	statusText := firstNonEmpty(session.statusText, "STREAMING")
	if session.ended && errMessage == "" {
		statusText = "OK"
	}
	return Response{
		Status:       statusCode,
		StatusText:   statusText,
		Headers:      headers,
		Metadata:     grpcexec.MetadataRowsFromMap(session.headers),
		Trailers:     grpcexec.MetadataRowsFromMap(session.trailers),
		Body:         string(body),
		BodyBase64:   base64.StdEncoding.EncodeToString(body),
		Size:         len(body),
		DurationMs:   time.Since(session.openedAt).Milliseconds(),
		Error:        errMessage,
		PreviewMode:  "grpc-stream",
		RequestedURL: session.targetURL,
		SentAt:       session.openedAt,
	}
}

func (session *grpcStreamSession) notifyEventLocked() {
	if session.eventNotify == nil {
		return
	}
	select {
	case session.eventNotify <- struct{}{}:
	default:
	}
}

func (session *grpcStreamSession) startReceiver() {
	session.mu.Lock()
	if session.receiverStarted {
		session.mu.Unlock()
		return
	}
	session.receiverStarted = true
	if session.receiveDone == nil {
		session.receiveDone = make(chan struct{})
	}
	done := session.receiveDone
	session.mu.Unlock()

	go func() {
		defer close(done)
		for {
			res := dynamicpb.NewMessage(session.binding.Descriptor.Output())
			err := session.stream.RecvMsg(res)
			session.mu.Lock()
			if err == io.EOF {
				if !session.ended {
					session.ended = true
					session.closed = true
					session.status = http.StatusOK
					session.statusText = "OK"
					session.lastActivityAt = time.Now()
					grpcexec.AddMetadata(session.headers, "", mustGRPCHeader(session.stream))
					grpcexec.AddMetadata(session.trailers, "", session.stream.Trailer())
					session.appendEventLocked(grpcStreamSessionEvent{Direction: "system", Type: "end", Data: "server stream ended", At: session.lastActivityAt})
					if session.conn != nil {
						_ = session.conn.Close()
					}
					session.notifyEventLocked()
				}
				session.mu.Unlock()
				return
			}
			if err != nil {
				if session.closed && session.closeReason != "" {
					session.notifyEventLocked()
					session.mu.Unlock()
					return
				}
				st := status.Convert(err)
				session.closed = true
				session.status = int(st.Code())
				session.statusText = st.Code().String()
				session.closeReason = firstNonEmpty(st.Message(), err.Error())
				session.lastActivityAt = time.Now()
				session.appendEventLocked(grpcStreamSessionEvent{Direction: "system", Type: "error", Error: session.closeReason, At: session.lastActivityAt})
				if session.conn != nil {
					_ = session.conn.Close()
				}
				session.notifyEventLocked()
				session.mu.Unlock()
				return
			}
			body, err := (protojson.MarshalOptions{Multiline: true, Indent: "  ", EmitUnpopulated: true}).Marshal(res)
			now := time.Now()
			if err != nil {
				session.closed = true
				session.status = int(status.Code(err))
				session.statusText = "ERROR"
				session.closeReason = "format gRPC response JSON: " + err.Error()
				session.appendEventLocked(grpcStreamSessionEvent{Direction: "system", Type: "error", Error: session.closeReason, At: now})
				if session.conn != nil {
					_ = session.conn.Close()
				}
				session.notifyEventLocked()
				session.mu.Unlock()
				return
			}
			session.responseCount++
			session.lastActivityAt = now
			session.appendEventLocked(grpcStreamSessionEvent{
				Direction: "received",
				Name:      fmt.Sprintf("response %d", session.responseCount),
				Type:      "json",
				Data:      string(body),
				At:        now,
			})
			session.notifyEventLocked()
			session.mu.Unlock()
		}
	}()
}

func (session *grpcStreamSession) waitForResponseAfter(responseCount int, wait time.Duration) {
	if wait <= 0 {
		wait = 500 * time.Millisecond
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	for {
		session.mu.Lock()
		done := session.responseCount > responseCount || session.closed || session.ended
		session.mu.Unlock()
		if done {
			return
		}
		select {
		case <-session.eventNotify:
		case <-timer.C:
			return
		}
	}
}

func (session *grpcStreamSession) receiveAvailableLocked() {
	for {
		res := dynamicpb.NewMessage(session.binding.Descriptor.Output())
		err := session.stream.RecvMsg(res)
		if err == io.EOF {
			session.ended = true
			session.closed = true
			session.status = http.StatusOK
			session.statusText = "OK"
			session.lastActivityAt = time.Now()
			grpcexec.AddMetadata(session.headers, "", mustGRPCHeader(session.stream))
			grpcexec.AddMetadata(session.trailers, "", session.stream.Trailer())
			session.appendEventLocked(grpcStreamSessionEvent{Direction: "system", Type: "end", Data: "server stream ended", At: session.lastActivityAt})
			if session.conn != nil {
				_ = session.conn.Close()
			}
			session.notifyEventLocked()
			return
		}
		if err != nil {
			st := status.Convert(err)
			session.closed = true
			session.status = int(st.Code())
			session.statusText = st.Code().String()
			session.closeReason = firstNonEmpty(st.Message(), err.Error())
			session.lastActivityAt = time.Now()
			session.appendEventLocked(grpcStreamSessionEvent{Direction: "system", Type: "error", Error: session.closeReason, At: session.lastActivityAt})
			if session.conn != nil {
				_ = session.conn.Close()
			}
			session.notifyEventLocked()
			return
		}
		body, err := (protojson.MarshalOptions{Multiline: true, Indent: "  ", EmitUnpopulated: true}).Marshal(res)
		now := time.Now()
		if err != nil {
			session.closed = true
			session.status = int(status.Code(err))
			session.statusText = "ERROR"
			session.closeReason = "format gRPC response JSON: " + err.Error()
			session.appendEventLocked(grpcStreamSessionEvent{Direction: "system", Type: "error", Error: session.closeReason, At: now})
			if session.conn != nil {
				_ = session.conn.Close()
			}
			session.notifyEventLocked()
			return
		}
		session.responseCount++
		session.lastActivityAt = now
		session.appendEventLocked(grpcStreamSessionEvent{
			Direction: "received",
			Name:      fmt.Sprintf("response %d", session.responseCount),
			Type:      "json",
			Data:      string(body),
			At:        now,
		})
		session.notifyEventLocked()
	}
}

func mustGRPCHeader(stream grpc.ClientStream) metadata.MD {
	headers, err := stream.Header()
	if err != nil {
		return nil
	}
	return headers
}

func grpcOutboundMessageAt(item RequestItem, binding grpcMethodBinding, vars map[string]string, messageIndex int) (GrpcMessage, proto.Message, error) {
	messages := grpcexec.GrpcurlRequestMessages(item, vars)
	if len(messages) == 0 {
		messages = []GrpcMessage{{Name: "message 1", Content: "{}"}}
	}
	if messageIndex < 0 || messageIndex >= len(messages) {
		return GrpcMessage{}, nil, fmt.Errorf("gRPC message %d not found", messageIndex+1)
	}
	message := messages[messageIndex]
	req := dynamicpb.NewMessage(binding.Descriptor.Input())
	content := strings.TrimSpace(message.Content)
	if content == "" {
		content = "{}"
	}
	if err := (protojson.UnmarshalOptions{DiscardUnknown: false}).Unmarshal([]byte(content), req); err != nil {
		return GrpcMessage{}, nil, fmt.Errorf("parse gRPC request message %d JSON: %w", messageIndex+1, err)
	}
	message.Content = content
	return message, req, nil
}

func grpcResponseNumber(name string) string {
	fields := strings.Fields(strings.TrimSpace(name))
	if len(fields) > 0 {
		last := fields[len(fields)-1]
		if _, err := strconv.Atoi(last); err == nil {
			return last
		}
	}
	return "1"
}

func grpcDialTarget(rawURL string) (grpcDialConfig, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return grpcDialConfig{}, errors.New("gRPC URL is required")
	}
	if !strings.Contains(rawURL, "://") {
		return grpcDialConfig{Target: rawURL, Credentials: insecure.NewCredentials()}, nil
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return grpcDialConfig{}, err
	}
	if strings.EqualFold(parsed.Scheme, "unix") || strings.EqualFold(parsed.Scheme, "grpc+unix") {
		socketPath, err := grpcexec.UnixSocketPath(parsed)
		if err != nil {
			return grpcDialConfig{}, err
		}
		return grpcexec.UnixDialConfig(socketPath), nil
	}
	target := parsed.Host
	if target == "" {
		target = strings.TrimPrefix(parsed.Opaque, "//")
	}
	if target == "" {
		return grpcDialConfig{}, errors.New("gRPC URL host is required")
	}
	switch strings.ToLower(parsed.Scheme) {
	case "grpc":
		return grpcDialConfig{Target: target, Credentials: insecure.NewCredentials()}, nil
	case "grpcs":
		return grpcDialConfig{Target: target, TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12}}, nil
	default:
		return grpcDialConfig{}, fmt.Errorf("unsupported gRPC URL scheme %q", parsed.Scheme)
	}
}

func (a *App) grpcDialConfigForRequest(collection Collection, item RequestItem, targetURL string, vars map[string]string) (grpcDialConfig, error) {
	dialConfig, err := grpcDialTarget(targetURL)
	if err != nil {
		return grpcDialConfig{}, err
	}
	if userAgent := grpcexec.UserAgentFromHeaders(item.Headers, vars); userAgent != "" {
		dialConfig.Options = append(dialConfig.Options, grpc.WithUserAgent(userAgent))
	}
	if dialConfig.TLSConfig == nil {
		return dialConfig, nil
	}
	tlsConfig := dialConfig.TLSConfig.Clone()
	tlsSettings := a.appTLSSettingsSnapshot()
	verifyTLS := requestTLSVerificationEnabled(tlsSettings.Request, item.Settings.VerifyTLS)
	if !verifyTLS {
		tlsConfig.InsecureSkipVerify = true
	} else if err := applyCustomRootCAsToTLSConfig(tlsConfig, tlsSettings.Request); err != nil {
		return grpcDialConfig{}, err
	}
	if tlsSettings.ClientSessionCache != nil {
		tlsConfig.ClientSessionCache = tlsSettings.ClientSessionCache
	}
	certificate, ok, err := transport.MatchingTLSClientCertificate(collection.Path, collection.ClientCertificates, targetURL, vars)
	if err != nil {
		return grpcDialConfig{}, err
	}
	if ok {
		tlsConfig.Certificates = append([]tls.Certificate{certificate}, tlsConfig.Certificates...)
	}
	dialConfig.TLSConfig = tlsConfig
	return dialConfig, nil
}
