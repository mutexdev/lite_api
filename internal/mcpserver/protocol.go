// The MCP wire protocol: JSON-RPC 2.0 carried over streamable HTTP, pinned to
// the 2025-06-18 revision of the spec.
//
// Hand-rolled, and deliberately so. The subset an embedded read-tier server
// needs is five methods over a single POST endpoint, with no SSE stream, no
// session state, and no sampling — a few hundred lines. Taking an SDK for that
// would add a dependency (and its transitive tree) to a binary that otherwise
// hand-rolls every format it speaks, in exchange for code we would still have
// to read to trust. The spec citations below are what the code is checked
// against.
//
// The server is stateless by design: it keeps no session, never issues an
// Mcp-Session-Id, and ignores one if a client sends it. Every request is
// authenticated on its own merits, which is also why nothing here is allowed
// to depend on a prior initialize having happened.
package mcpserver

import (
	"bytes"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// ProtocolVersion is the MCP revision this server implements, and the version
// it answers with when a client asks for one it does not recognise.
const ProtocolVersion = "2025-06-18"

// ServerName and ServerVersion identify this implementation in the initialize
// handshake. The name is what appears in an agent's tool listing.
const (
	ServerName    = "liteapi"
	ServerVersion = "0.1.0"
)

// supportedProtocolVersions are the revisions whose framing this code is
// compatible with. 2025-03-26 differs from 2025-06-18 only in ways that do not
// touch the subset implemented here (it still permitted batching, which we
// reject either way — see handleMCP).
var supportedProtocolVersions = []string{"2025-03-26", "2025-06-18"}

// maxRequestBytes caps a single inbound message. Tool arguments are small; an
// unbounded read would turn one hostile POST on a loopback port into a memory
// problem.
const maxRequestBytes = 4 << 20

// JSON-RPC 2.0 error codes. These are reserved for protocol faults: a failure
// inside a tool travels as a normal result with isError set, so the agent can
// read the explanation and retry rather than seeing its transport break.
const (
	codeParseError     = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
)

// nullID is the id of a response to a message whose own id could not be read.
var nullID = json.RawMessage("null")

// rpcRequest is one inbound JSON-RPC message. ID is kept raw so it can be
// echoed back byte-for-byte — JSON-RPC allows a string or a number and does
// not permit the server to change which it was. An absent ID makes the message
// a notification, which by definition gets no response.
type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// rpcError is the error member of a JSON-RPC response.
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// rpcResponse is one outbound JSON-RPC message. Result carries omitempty so an
// error response never also carries a null result, which JSON-RPC forbids;
// only a nil interface is dropped, so an empty object result still ships.
type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// initializeParams is the part of the handshake this server reads. The
// client's capabilities and clientInfo are accepted and ignored: nothing here
// is conditional on them.
type initializeParams struct {
	ProtocolVersion string `json:"protocolVersion"`
}

type initializeResult struct {
	ProtocolVersion string             `json:"protocolVersion"`
	Capabilities    serverCapabilities `json:"capabilities"`
	ServerInfo      serverInfo         `json:"serverInfo"`
}

// serverCapabilities advertises tools and nothing else. The empty tools object
// is the spec's way of saying "this server has tools"; listChanged is absent
// because the registry is fixed for the life of the process.
type serverCapabilities struct {
	Tools struct{} `json:"tools"`
}

type serverInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// handleMCP answers the single protocol endpoint.
//
// The checks run origin, then auth, then method. Authenticating before looking
// at the HTTP method means an unauthenticated prober cannot even learn which
// methods the endpoint implements; the origin check runs first because it is a
// browser defence and should not depend on a credential the browser would be
// attaching automatically anyway.
func (s *Server) handleMCP(w http.ResponseWriter, r *http.Request) {
	// DNS-rebinding defence the streamable-HTTP spec requires: a page on a
	// hostile origin can resolve a name to 127.0.0.1 and POST to this port,
	// and the browser will attach the Origin header when it does. A non-browser
	// client sends no Origin at all, which stays allowed.
	if origin := r.Header.Get("Origin"); origin != "" && !isLocalOrigin(origin) {
		http.Error(w, "forbidden: origin not allowed", http.StatusForbidden)
		return
	}
	if !s.authorized(r) {
		// The body says only that credentials were wrong. Echoing what was
		// presented back to whoever presented it would turn a log or an agent
		// transcript into a place tokens end up.
		w.Header().Set("WWW-Authenticate", `Bearer realm="liteapi"`)
		http.Error(w, "unauthorized: valid bearer token required", http.StatusUnauthorized)
		return
	}
	if r.Method != http.MethodPost {
		// No GET: this server never opens a server-initiated SSE stream, and
		// nothing it holds is worth streaming. No DELETE: there is no session
		// to terminate.
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed: POST only", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxRequestBytes))
	if err != nil {
		writeRPC(w, http.StatusBadRequest, errorResponse(nullID, codeParseError, "request body could not be read"))
		return
	}
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		writeRPC(w, http.StatusBadRequest, errorResponse(nullID, codeParseError, "empty request body: expected a single JSON-RPC 2.0 message"))
		return
	}
	// Well-formedness first, meaning second: -32700 is specifically "I could
	// not parse this", and reporting it for a JSON document that simply is not
	// a JSON-RPC message would send the client looking for the wrong bug.
	if !json.Valid(trimmed) {
		writeRPC(w, http.StatusBadRequest, errorResponse(nullID, codeParseError, "request body is not valid JSON"))
		return
	}
	if trimmed[0] == '[' {
		// Batching was removed in the 2025-06-18 revision. Answering the array
		// would be a silent downgrade, so it is refused by name.
		writeRPC(w, http.StatusBadRequest, errorResponse(nullID, codeInvalidRequest, "JSON-RPC batching is not supported in MCP 2025-06-18: send one message per request"))
		return
	}
	if trimmed[0] != '{' {
		writeRPC(w, http.StatusBadRequest, errorResponse(nullID, codeInvalidRequest, "request must be a JSON-RPC 2.0 object"))
		return
	}
	var request rpcRequest
	if err := json.Unmarshal(trimmed, &request); err != nil {
		// Valid JSON whose members have the wrong types: parseable, but not a
		// JSON-RPC message.
		writeRPC(w, http.StatusBadRequest, errorResponse(nullID, codeInvalidRequest, "request is not a JSON-RPC 2.0 message: jsonrpc and method must be strings"))
		return
	}
	if request.JSONRPC != "2.0" || request.Method == "" {
		writeRPC(w, http.StatusBadRequest, errorResponse(request.ID, codeInvalidRequest, `request must carry jsonrpc:"2.0" and a method`))
		return
	}

	// A notification carries no id, so there is nothing to respond to — not
	// even an error. 202 with an empty body is what the spec asks for, and it
	// covers notifications/initialized along with any other the client sends.
	if len(request.ID) == 0 {
		w.WriteHeader(http.StatusAccepted)
		return
	}

	result, rpcErr := s.dispatch(request)
	if rpcErr != nil {
		// A well-formed request that names something we do not have is a
		// successful HTTP exchange reporting a JSON-RPC failure.
		writeRPC(w, http.StatusOK, rpcResponse{JSONRPC: "2.0", ID: request.ID, Error: rpcErr})
		return
	}
	writeRPC(w, http.StatusOK, rpcResponse{JSONRPC: "2.0", ID: request.ID, Result: result})
}

// dispatch routes one request-shaped message to its method.
func (s *Server) dispatch(request rpcRequest) (any, *rpcError) {
	switch request.Method {
	case "initialize":
		return s.handleInitialize(request.Params)
	case "ping":
		// The spec's liveness probe: an empty result object, nothing more.
		return struct{}{}, nil
	case "tools/list":
		return toolsListResult(), nil
	case "tools/call":
		return s.handleToolsCall(request.Params)
	default:
		return nil, &rpcError{Code: codeMethodNotFound, Message: fmt.Sprintf("unknown method %q", request.Method)}
	}
}

// handleInitialize answers the handshake. Absent params are tolerated: the
// only field read is the protocol version, and its absence means the same
// thing as an unknown one.
func (s *Server) handleInitialize(raw json.RawMessage) (any, *rpcError) {
	var params initializeParams
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &params); err != nil {
			return nil, &rpcError{Code: codeInvalidParams, Message: "initialize params must be an object"}
		}
	}
	return initializeResult{
		ProtocolVersion: negotiateProtocolVersion(params.ProtocolVersion),
		ServerInfo:      serverInfo{Name: ServerName, Version: ServerVersion},
	}, nil
}

// negotiateProtocolVersion echoes the client's requested revision when this
// server speaks it, and otherwise names the one it does. The client then
// decides whether to continue — that choice is the client's, which is why an
// unsupported version is answered rather than refused.
func negotiateProtocolVersion(requested string) string {
	for _, supported := range supportedProtocolVersions {
		if requested == supported {
			return requested
		}
	}
	return ProtocolVersion
}

// handleToolsCall validates the envelope and hands off to the registry.
//
// An unknown tool name is a JSON-RPC -32602 (invalid params), not an isError
// result. That is the split the MCP spec draws and it matches what the two
// mean to a caller: naming a tool that does not exist is the client having
// used the protocol wrong, while a tool that ran and failed is information the
// agent should read and act on.
// It is also the one place that sees everything an audit entry needs — the
// tool name, the arguments, the outcome, and the wall time around the handler —
// so every tools/call is recorded here and nowhere else. The -32602 paths are
// recorded too, with outcome "error": a client naming tools that do not exist
// is exactly the probing an audit exists to show, and an audit that only
// records the calls that worked would report a clean history of an attack.
//
// tools/list, initialize and ping are not recorded. They are discovery, every
// client makes them on connect, and burying the calls that touched the user's
// data under that noise would make the panel useless.
func (s *Server) handleToolsCall(raw json.RawMessage) (any, *rpcError) {
	started := time.Now()
	var params struct {
		Name      string   `json:"name"`
		Arguments toolArgs `json:"arguments"`
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &params); err != nil {
			s.recordCall(started, "", nil, outcomeError)
			return nil, &rpcError{Code: codeInvalidParams, Message: "tools/call params must be an object with a name and an arguments object"}
		}
	}
	if params.Name == "" {
		s.recordCall(started, "", params.Arguments, outcomeError)
		return nil, &rpcError{Code: codeInvalidParams, Message: "tools/call requires a tool name; call tools/list for the available tools"}
	}
	entry, found := lookupTool(params.Name)
	if !found {
		s.recordCall(started, params.Name, params.Arguments, outcomeError)
		return nil, &rpcError{Code: codeInvalidParams, Message: fmt.Sprintf("unknown tool %q; call tools/list for the available tools", params.Name)}
	}
	result, outcome := runTool(entry, s.backend, params.Arguments)
	s.recordCall(started, params.Name, params.Arguments, outcome)
	return result, nil
}

// The three outcomes an audit entry can carry. "denied" is kept apart from
// "error" because they mean opposite things about the system: an error is
// something that went wrong, while a denial is a guard doing its job, and a
// user scanning the panel needs to see refusals without reading every row.
const (
	outcomeOK     = "ok"
	outcomeError  = "error"
	outcomeDenied = "denied"
)

// recordCall hands one finished tools/call to the audit sink, if there is one.
//
// It runs on the request's goroutine after the tool has already produced its
// result, so a slow recorder delays the response but cannot change it. Without
// a recorder this is a nil check and nothing else, which is the Phase 1
// read-only posture.
func (s *Server) recordCall(started time.Time, tool string, args toolArgs, outcome string) {
	if s.audit == nil {
		return
	}
	s.audit(AuditEntry{
		At:          time.Now().UTC(),
		Tool:        tool,
		ArgsSummary: summarizeArgs(args),
		Outcome:     outcome,
		DurationMs:  int(time.Since(started).Milliseconds()),
	})
}

// maxAuditValueRunes and maxAuditSummaryRunes bound what one entry can cost.
// Arguments are attacker-shaped input on a loopback port: a single call can
// carry megabytes of body-sized strings, and an audit log that stores them
// verbatim is a way to fill the user's disk from an MCP client. Runes, not
// bytes, so a summary cut mid-character never lands in the panel.
const (
	maxAuditValueRunes   = 200
	maxAuditSummaryRunes = 1000
	truncationMarker     = "…"
)

// summarizeArgs renders one call's arguments as a compact, bounded line for the
// audit panel: `collectionId="col_pos" limit=5`, keys in sorted order so the
// same call always produces the same summary and two entries can be compared.
//
// This is a summary, not a record: values are truncated and the whole line is
// capped, so nothing here should be treated as a faithful copy of what was
// sent. What it does guarantee is that the tool and the ids it was pointed at
// survive, which is what makes a row worth reading.
func summarizeArgs(args toolArgs) string {
	if len(args) == 0 {
		return ""
	}
	var builder strings.Builder
	for _, name := range sortedKeys(args) {
		if builder.Len() > 0 {
			builder.WriteByte(' ')
		}
		builder.WriteString(name)
		builder.WriteByte('=')
		builder.WriteString(truncateRunes(renderArgValue(args[name]), maxAuditValueRunes))
	}
	return truncateRunes(builder.String(), maxAuditSummaryRunes)
}

// renderArgValue renders one argument value compactly.
//
// Strings go through strconv.Quote rather than json.Marshal: both quote and
// escape, but Quote leaves <, > and & alone, and a URL or a JSON body rendered
// with < everywhere is unreadable in the panel the user is scanning.
// Everything else is JSON, which keeps nested objects on one line.
func renderArgValue(value any) string {
	switch typed := value.(type) {
	case string:
		return strconv.Quote(typed)
	case nil:
		return "null"
	default:
		encoded, err := json.Marshal(value)
		if err != nil {
			// Nothing decoded from JSON can land here; a Go-built argument
			// might, and a summary is never worth failing a call over.
			return fmt.Sprintf("%v", value)
		}
		return string(encoded)
	}
}

// truncateRunes cuts text to at most limit runes, marking that it did.
func truncateRunes(text string, limit int) string {
	if len(text) <= limit {
		// Bytes bound runes, so a short string is short in both and the
		// conversion below can be skipped for the overwhelmingly common case.
		return text
	}
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit]) + truncationMarker
}

// toolContent is one block of a tool result. Only text blocks are produced:
// every tool here answers with structured data, which travels as compact JSON
// inside the text so an agent can parse it without a second round trip.
type toolContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// callToolResult is the tools/call payload. IsError is always emitted rather
// than omitted when false, so a client never has to distinguish "absent" from
// "false" to know whether the call worked.
type callToolResult struct {
	Content []toolContent `json:"content"`
	IsError bool          `json:"isError"`
}

// runTool validates, runs, and encodes one tool call, returning the result and
// the outcome the audit should record.
//
// Everything that can go wrong from here on is the tool's own failure —
// arguments that do not fit the schema, a backend that could not answer, a
// panic — and all of it comes back as a normal result with isError set. That
// is the MCP split: JSON-RPC errors mean the protocol broke and the client
// should stop, while an isError result is a message addressed to the agent,
// which can read the explanation and try again with better arguments.
//
// A denial is one of those failures and reaches the agent the same way, as an
// isError result carrying the message unchanged. The Backend contract makes
// that message the explanation — which guard fired, and what the user would
// have to approve — so rewriting or prefixing it here would only bury the one
// sentence the agent needs in order to stop retrying and ask. The outcome is
// what differs: the audit records "denied", so a refusal shows up in the panel
// as a refusal rather than as one more failed call.
func runTool(entry toolEntry, backend Backend, args toolArgs) (callToolResult, string) {
	payload, err := invokeTool(entry, backend, args)
	if err != nil {
		if errors.Is(err, ErrDenied) {
			return toolFailure(err.Error()), outcomeDenied
		}
		return toolFailure(err.Error()), outcomeError
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return toolFailure(fmt.Sprintf("tool %q produced a result that could not be encoded as JSON: %v", entry.Name, err)), outcomeError
	}
	return callToolResult{Content: []toolContent{{Type: "text", Text: string(encoded)}}, IsError: false}, outcomeOK
}

// invokeTool runs validation and the handler under a recover.
//
// A panic in a tool must not take the server down with it: the handler goroutine
// belongs to an HTTP request whose client is an agent in the middle of a task,
// and killing the process would also kill the desktop app around it.
func invokeTool(entry toolEntry, backend Backend, args toolArgs) (result any, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			result = nil
			err = fmt.Errorf("tool %q failed unexpectedly: %v", entry.Name, recovered)
		}
	}()
	if validationErr := entry.InputSchema.validate(args); validationErr != nil {
		return nil, validationErr
	}
	return entry.Handler(backend, args)
}

// toolFailure wraps an explanation as a failed tool result.
func toolFailure(message string) callToolResult {
	return callToolResult{Content: []toolContent{{Type: "text", Text: message}}, IsError: true}
}

// authorized reports whether the request presented the pairing token. The
// comparison is constant time so that a client on the loopback interface
// cannot recover the token one byte at a time from response timing.
func (s *Server) authorized(r *http.Request) bool {
	// An unset token means the server was constructed without a credential.
	// Treating that as "everything matches" would silently publish the user's
	// collections to anything that can reach the port, so it denies instead.
	if s.token == "" {
		return false
	}
	const prefix = "Bearer "
	header := r.Header.Get("Authorization")
	if len(header) <= len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return false
	}
	presented := strings.TrimSpace(header[len(prefix):])
	return subtle.ConstantTimeCompare([]byte(presented), []byte(s.token)) == 1
}

// isLocalOrigin reports whether a browser Origin belongs to this machine.
// ::1 is included alongside 127.0.0.1 because a loopback origin is a loopback
// origin whichever address family the browser resolved.
func isLocalOrigin(origin string) bool {
	parsed, err := url.Parse(origin)
	if err != nil {
		return false
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return false
	}
	switch parsed.Hostname() {
	case "localhost", "127.0.0.1", "::1":
		return true
	default:
		return false
	}
}

// errorResponse builds a JSON-RPC error reply, defaulting an unreadable id to
// null as JSON-RPC requires.
func errorResponse(id json.RawMessage, code int, message string) rpcResponse {
	if len(id) == 0 {
		id = nullID
	}
	return rpcResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: message}}
}

// writeRPC sends one JSON-RPC message.
//
// The content type is always application/json. The spec lets a server answer
// a POST with either a JSON object or an SSE stream regardless of what the
// client's Accept header offered, and this server has nothing to stream.
func writeRPC(w http.ResponseWriter, status int, response rpcResponse) {
	encoded, err := json.Marshal(response)
	if err != nil {
		// Nothing built here contains a value encoding/json can refuse, so
		// this is a bug in this package rather than anything the client did.
		http.Error(w, "internal error encoding response", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(encoded)
}
