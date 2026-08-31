package core

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"

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

	if parent == nil {
		parent = context.Background()
	}
	// §4.7 + §4.3: the target is validated and the pinned origin authorized on
	// the PARENT context, before the request timeout is applied. An approval
	// prompt can take up to a minute (mcp_approvals.go), and a 30-second request
	// timeout wrapped around it would turn "the user was still reading the
	// prompt" into "the request timed out".
	dialConfig, err := a.grpcDialConfigForRequestContext(parent, collection, item, targetURL, vars)
	if err != nil {
		result.Error = err.Error()
		result.DurationMs = time.Since(start).Milliseconds()
		return result
	}
	timeout := requestTimeoutMilliseconds(item.Settings.TimeoutMs, a.appTLSSettingsSnapshot().Request)
	ctx, cancel := context.WithTimeout(parent, time.Duration(timeout)*time.Millisecond)
	defer cancel()

	conn, err := grpc.NewClient(dialConfig.Target, dialConfig.DialOptions()...)
	if err != nil {
		result.Error = err.Error()
		result.DurationMs = time.Since(start).Milliseconds()
		return result
	}
	defer func() { _ = conn.Close() }()

	ctx, err = grpcexec.OutgoingContext(ctx, item, vars, a.grpcOAuth2Fetcher(ctx))
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

// grpcDialConfigForRequestContext builds the dial configuration, and under MCP
// provenance it is also the whole gRPC half of the destination boundary: §4.7's
// target allowlist, §4.3's pre-dial checkpoint, and §4.4's client-certificate
// contract, all before any caller reaches grpc.NewClient.
//
// THE UI PATH IS THE OLD BODY, UNCHANGED. A nil policy means this is not an
// MCP-initiated execution (§1.2(4)), so the target still goes through
// grpcDialTarget — unix sockets, grpc+unix, bare authorities and all — and the
// certificate still matches against the runtime target with the runtime vars.
func (a *App) grpcDialConfigForRequestContext(ctx context.Context, collection Collection, item RequestItem, targetURL string, vars map[string]string) (grpcDialConfig, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	policy := mcpPolicyFromContext(ctx)

	var (
		dialConfig   grpcDialConfig
		pinnedOrigin Origin
		err          error
	)
	if policy == nil {
		dialConfig, err = grpcDialTarget(targetURL)
		if err != nil {
			return grpcDialConfig{}, err
		}
	} else {
		var pinnedTarget string
		pinnedTarget, pinnedOrigin, err = mcpValidateGRPCTarget(targetURL)
		if err != nil {
			// §2 row 3 is a FEATURE refusal, not a destination denial, so
			// nothing in this run has called Authorize and the ErrDenied class
			// mcpGRPCTargetRefusal built would be lost the moment the error is
			// stringified. Marked here rather than inside the validator because
			// the validator is also used as a silent cert-matching probe below,
			// where a parse failure means "no certificate", not "refused".
			policy.noteRefusal()
			return grpcDialConfig{}, err
		}
		dialConfig = mcpGRPCDialConfig(pinnedTarget, pinnedOrigin)
		// The pre-dial checkpoint (§4.3). ONE authorized channel covers the
		// reflection call, the invoke and every stream opened on it, because
		// they all ride the connection this target opens and none of them can
		// reach a different authority.
		if err := policy.Authorize(ctx, pinnedOrigin, egressKindMain); err != nil {
			return grpcDialConfig{}, err
		}
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
	certificate, ok, err := grpcClientCertificate(policy, collection, targetURL, vars, pinnedOrigin)
	if err != nil {
		return grpcDialConfig{}, err
	}
	if ok {
		tlsConfig.Certificates = append([]tls.Certificate{certificate}, tlsConfig.Certificates...)
	}
	dialConfig.TLSConfig = tlsConfig
	return dialConfig, nil
}

// grpcClientCertificate is §4.4's gRPC branch — a SEPARATE SEAM from
// requestTransport, and one that is easy to miss precisely because it does not
// go through it.
//
// Under MCP the matching seam must not see agent-shaped values. It receives the
// active scope's agent-free mainURL and baseVars, so no override, flow input or
// script-set variable can decide WHICH certificate is selected; and the selected
// certificate is attached only when the §4.7-validated pinned target's origin
// equals certOrigin — the origin of that same agent-free main destination — so
// no retargeting can decide WHERE it is presented.
//
// THE ORIGIN COMPARISON HAPPENS FIRST, before the certificate is even loaded.
// An off-origin dial then does no key-file I/O at all, and a load error for a
// certificate that was never going to be presented cannot fail the send.
//
// THERE IS NO REDIRECT CONCEPT ON A GRPC CHANNEL, which is why this single
// pre-dial equality check is the whole rule here, where the HTTP branch
// additionally needs a per-hop guard.
func grpcClientCertificate(policy *mcpEgressPolicy, collection Collection, targetURL string, vars map[string]string, pinnedOrigin Origin) (tls.Certificate, bool, error) {
	if policy == nil {
		return transport.MatchingTLSClientCertificate(collection.Path, collection.ClientCertificates, targetURL, vars)
	}
	scope, ok := policy.activeScope()
	if !ok {
		// No scope means no agent-free destination to match against. Authorize
		// has already denied this send; returning "no certificate" keeps that
		// failure from being reported as a certificate problem.
		return tls.Certificate{}, false, nil
	}
	// certOrigin is computed with §4.7's grammar rather than OriginOfURL,
	// because the agent-free main destination of a gRPC request is a gRPC
	// target: its effective port is 443 whether or not it is a TLS channel.
	_, certOrigin, err := mcpValidateGRPCTarget(scope.mainURL)
	if err != nil || certOrigin != pinnedOrigin {
		return tls.Certificate{}, false, nil
	}
	return transport.MatchingTLSClientCertificate(collection.Path, collection.ClientCertificates, scope.mainURL, scope.baseVars)
}

// grpcOAuth2Fetcher binds the send's context to the OAuth2 token fetch that
// grpcexec.OutgoingContext may perform while assembling metadata.
//
// WHY A CLOSURE RATHER THAN A GRPCEXEC SIGNATURE CHANGE. grpcexec takes a
// fetcher func and knows nothing about policies; the context it needs is the one
// the caller already holds. Wrapping it here keeps grpcexec unchanged and puts
// the provenance decision in the package that owns it.
//
// THE KIND IS NARROWED TO token. A token exchange is not the request's own
// destination: Base(S, token) is the OAuth2 endpoints, and the backstop must not
// authorize it as if it were the gRPC target (§1.1, kindClass).
func (a *App) grpcOAuth2Fetcher(ctx context.Context) func(OAuth2Auth, map[string]string) (string, error) {
	tokenCtx := mcpGRPCTokenContext(ctx)
	return func(auth OAuth2Auth, vars map[string]string) (string, error) {
		return a.fetchOAuth2TokenForGRPC(tokenCtx, auth, vars)
	}
}

// mcpGRPCTokenContext is the context a gRPC send's OAuth2 token exchange runs
// under: the send's own context, with the backstop kind narrowed to token.
func mcpGRPCTokenContext(ctx context.Context) context.Context {
	return mcpContextWithEgressKind(ctx, egressKindToken)
}

// fetchOAuth2TokenForGRPC runs the token exchange under the send's context, so
// the exchange carries the run's provenance and is checked as a token egress
// rather than travelling unlabeled.
//
// The context is ALSO consulted up front: a cancelled gRPC send must not block
// for a 30-second token fetch whose result it will never use.
func (a *App) fetchOAuth2TokenForGRPC(ctx context.Context, auth OAuth2Auth, vars map[string]string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return a.fetchOAuth2TokenWithContext(ctx, auth, vars)
}

// --- §4.7: the gRPC target allowlist --------------------------------------
//
// WHY AN ALLOWLIST AND NOT A DENYLIST. A gRPC "target" is not a URL — it is a
// grpc-go resolver expression, and the set of things it can mean is open-ended:
// `unix:/var/run/x` dials a filesystem socket, `unix-abstract:name` an abstract
// one, `xds://…` fetches its endpoints from a control plane, `passthrough://`
// and future schemes each bring their own resolver. None of those has an origin
// this boundary can reason about, and a denylist would have to be right about
// every scheme grpc-go ever adds. So: a grammar that admits exactly a TCP
// authority, optionally spelled with grpc:// or grpcs://, and refuses the rest
// (§2 row 3) BEFORE grpcDialTarget and before grpc.NewClient — zero dial, zero
// resolver instantiation.
//
// THE COLON RULE IS THE WHOLE TRICK. Every scheme-spelled target either carries
// a delimiter the parser rejects outright (`unix:/x`, `dns:///h`, `xds://h`,
// `http://h` all contain "/") or leaves a non-numeric "port" after its single
// colon (`unix:sock`, `unix-abstract:name`). Requiring the text after a lone
// colon to be a 1-65535 number therefore rejects the entire scheme family
// without enumerating it, while accepting `localhost:50051`.
//
// THE PIN IS ALWAYS EXPLICIT. The port is materialized (443 when absent, for
// plaintext and TLS alike — grpc-go's DNS-resolver default) and the target is
// generated as dns:///host:port, so what is dialed cannot be re-interpreted by
// grpc-go's scheme sniffing or by a future change to its default port. The
// authority the user's approval named and the authority the channel opens are
// the same string.

// mcpValidateGRPCTarget is §4.7. It returns the explicit dns:/// pin that must
// reach grpc.NewClient — never the raw input — and the origin the checkpoint
// authorizes.
func mcpValidateGRPCTarget(raw string) (string, Origin, error) {
	trimmed := strings.TrimSpace(raw)
	authority := trimmed
	scheme := "http"
	switch {
	case len(trimmed) >= 7 && strings.EqualFold(trimmed[:7], "grpc://"):
		authority, scheme = trimmed[7:], "http"
	case len(trimmed) >= 8 && strings.EqualFold(trimmed[:8], "grpcs://"):
		authority, scheme = trimmed[8:], "https"
	}
	host, port, hasPort, err := mcpParseGRPCAuthority(authority)
	if err != nil {
		return "", Origin{}, mcpGRPCTargetRefusal(trimmed, err)
	}
	// The effective port is 443 with no port written down — for plaintext and
	// TLS alike. This is NOT the http/https 80/443 rule (§1.1): it is grpc-go's
	// DNS-resolver default, and applying the HTTP rule here would authorize
	// :80 while dialing :443.
	effectivePort := 443
	if hasPort {
		// Already validated as 1-65535 by the parser.
		effectivePort, _ = strconv.Atoi(port)
	}
	origin, ok := newOrigin(scheme, host, effectivePort)
	if !ok {
		return "", Origin{}, mcpGRPCTargetRefusal(trimmed, errors.New("the host did not resolve to a usable origin"))
	}
	// net.JoinHostPort re-brackets an IPv6 literal, so [::1] pins to
	// dns:///[::1]:443 and never to the unbracketed form grpc-go would read as
	// a host with several colons. origin.Host is the normalized spelling, so
	// the pinned target and the authorized origin cannot disagree.
	return "dns:///" + net.JoinHostPort(origin.Host, strconv.Itoa(effectivePort)), origin, nil
}

// mcpGRPCTargetRefusal is §2 row 3's uniform refusal.
func mcpGRPCTargetRefusal(target string, reason error) error {
	return mcpRefusal(
		fmt.Sprintf("the gRPC target %q is not a plain TCP authority (%v)", target, reason),
		"Use a host:port, grpc:// or grpcs:// target, or run this request in the LiteAPI app.")
}

// mcpParseGRPCAuthority parses a bare TCP authority per §4.7.
func mcpParseGRPCAuthority(s string) (host, port string, hasPort bool, err error) {
	if s == "" {
		return "", "", false, errors.New("the target is empty")
	}
	// No paths, no userinfo, no percent escapes, no authority-syntax schemes,
	// and nothing that a later parser could read differently from this one.
	for _, r := range s {
		switch {
		case r == '/' || r == '\\':
			return "", "", false, errors.New("it contains a path separator")
		case r == '?' || r == '#':
			return "", "", false, errors.New("it contains a query or fragment delimiter")
		case r == '@':
			return "", "", false, errors.New("it contains userinfo")
		case r == '%':
			return "", "", false, errors.New("it contains a percent escape")
		case unicode.IsSpace(r):
			return "", "", false, errors.New("it contains whitespace")
		case unicode.IsControl(r):
			return "", "", false, errors.New("it contains a control character")
		}
	}

	if strings.HasPrefix(s, "[") {
		closing := strings.Index(s, "]")
		if closing < 0 {
			return "", "", false, errors.New("the bracketed host is not closed")
		}
		if strings.Contains(s[closing+1:], "]") {
			return "", "", false, errors.New("the bracketed host has more than one closing bracket")
		}
		inner := s[1:closing]
		// net.ParseIP rejects a zone suffix, and a "%" would already have been
		// refused above; an IPv6 literal with a zone is not a destination this
		// boundary can compare.
		if net.ParseIP(inner) == nil {
			return "", "", false, errors.New("the bracketed host is not an IP literal")
		}
		host = inner
		rest := s[closing+1:]
		if rest == "" {
			return host, "", false, nil
		}
		if !strings.HasPrefix(rest, ":") {
			return "", "", false, errors.New("the bracketed host is followed by something other than a port")
		}
		port = rest[1:]
		if err := mcpValidateGRPCPort(port); err != nil {
			return "", "", false, err
		}
		return host, port, true, nil
	}

	switch strings.Count(s, ":") {
	case 0:
		host = s
	case 1:
		index := strings.Index(s, ":")
		host, port = s[:index], s[index+1:]
		if err := mcpValidateGRPCPort(port); err != nil {
			return "", "", false, err
		}
		hasPort = true
	default:
		// An unbracketed IPv6 literal, or anything else with several colons.
		// Bracket it and it is accepted; unbracketed, grpc-go and this parser
		// could disagree about where the host ends, and that disagreement is
		// exactly what an allowlist must not permit.
		return "", "", false, errors.New("it has more than one colon and is not a bracketed IPv6 literal")
	}
	if host == "" {
		return "", "", false, errors.New("the host is empty")
	}
	if net.ParseIP(host) == nil && !mcpValidGRPCHostname(host) {
		return "", "", false, errors.New("the host is neither an IP literal nor a hostname")
	}
	return host, port, hasPort, nil
}

// mcpValidateGRPCPort is §4.7's numeric-port rule: non-empty, all ASCII digits,
// at most five of them, and in 1-65535. This is the rule that rejects
// `unix:sock`, `unix-abstract:name`, `host:`, `host:0`, `host:70000` and
// `host:abc` without knowing anything about schemes.
func mcpValidateGRPCPort(port string) error {
	if port == "" {
		return errors.New("the port is empty")
	}
	if len(port) > 5 {
		return errors.New("the port has too many digits")
	}
	for index := 0; index < len(port); index++ {
		if port[index] < '0' || port[index] > '9' {
			return errors.New("the port is not a number")
		}
	}
	value, err := strconv.Atoi(port)
	if err != nil || value < 1 || value > 65535 {
		return errors.New("the port is outside 1-65535")
	}
	return nil
}

// mcpValidGRPCHostname is the RFC-1123 hostname rule: dot-separated labels of
// [A-Za-z0-9-], 1-63 characters each, no leading or trailing hyphen, 253
// characters in total. A trailing dot yields an empty final label and is
// refused — fail-closed, and it costs an approval prompt at worst.
func mcpValidGRPCHostname(host string) bool {
	if host == "" || len(host) > 253 {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 {
			return false
		}
		if label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for index := 0; index < len(label); index++ {
			c := label[index]
			isLetter := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
			isDigit := c >= '0' && c <= '9'
			if !isLetter && !isDigit && c != '-' {
				return false
			}
		}
	}
	return true
}

// mcpGRPCDialConfig builds the dial configuration for a validated target. The
// TLS choice comes from the validated scheme and matches grpcDialTarget's own
// branches: grpcs is TLS, grpc and a bare authority are insecure.
func mcpGRPCDialConfig(pinnedTarget string, origin Origin) grpcDialConfig {
	if origin.Scheme == "https" {
		return grpcDialConfig{Target: pinnedTarget, TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12}}
	}
	return grpcDialConfig{Target: pinnedTarget, Credentials: insecure.NewCredentials()}
}
