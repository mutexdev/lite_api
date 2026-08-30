// Package mcpserver embeds an MCP (Model Context Protocol) server in LiteAPI
// so AI tools the user already runs — Claude Code, Codex, anything speaking
// MCP over streamable HTTP — can discover collections and run requests through
// LiteAPI instead of rebuilding every call from scratch.
//
// The package never imports internal/core. Like internal/localserver, it works
// against a narrow injected surface (Backend, below) so the HTTP handlers
// cannot reach into app state on goroutines that do not own the app's locks,
// and so every tool is testable against a fake. internal/core implements
// Backend and starts the server; the dependency points one way.
//
// The DTOs in this file are redacted by construction: there is no field that
// may carry a resolved secret value. A secret variable travels as its name and
// a Secret flag with an empty Value; sensitive header and auth literals are
// masked by the adapter before they reach a DTO (helpers in redact.go). The
// rules are written down in docs/mcp-agent-interface.md and enforced here, not
// in tool descriptions.
package mcpserver

// DefaultPort is the port the server binds when preferences carry no explicit
// choice. Port 0 remains valid and asks the OS for an ephemeral port.
const DefaultPort = 43117

// Backend is the app surface the MCP tools run against. internal/core
// implements it over *App; tests implement it over fixtures. Every method is
// called from HTTP handler goroutines, so implementations do their own
// locking and return copies, never live state.
//
// Every string an implementation places in a return value may be shown to an
// AI client verbatim. Implementations mask secrets BEFORE constructing DTOs;
// the server does not get a second chance.
type Backend interface {
	// ListCollections returns every open collection.
	ListCollections() ([]CollectionSummary, error)
	// ListRequests returns the requests of one collection in tree order.
	ListRequests(collectionID string) ([]RequestSummary, error)
	// SearchRequests matches query case-insensitively against request name,
	// method, URL, and folder path across all collections. limit caps the
	// result; implementations apply their own default when limit <= 0.
	SearchRequests(query string, limit int) ([]RequestSummary, error)
	// GetRequest returns one request's full definition, redacted.
	GetRequest(collectionID, requestID string) (RequestDetail, error)
	// ListEnvironments returns global and collection environments with
	// variable names; secret values are always empty.
	ListEnvironments() ([]EnvironmentSummary, error)
	// GetHistory returns recent runs of a request, newest first, with
	// redacted headers and a bounded body.
	GetHistory(collectionID, requestID string, limit int) ([]HistoryRun, error)
}

// CollectionSummary names a collection an agent can explore.
type CollectionSummary struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	RequestCount int    `json:"requestCount"`
}

// RequestSummary is one request as a list/search row.
type RequestSummary struct {
	ID           string `json:"id"`
	CollectionID string `json:"collectionId"`
	Name         string `json:"name"`
	// Type is the request kind: http, graphql, ws, grpc.
	Type       string `json:"type"`
	Method     string `json:"method"`
	URL        string `json:"url"`
	FolderPath string `json:"folderPath,omitempty"`
}

// KeyValue is a named row (header, param, variable) as authored — values keep
// their {{templates}} unresolved.
type KeyValue struct {
	Name    string `json:"name"`
	Value   string `json:"value"`
	Enabled bool   `json:"enabled"`
}

// RequestSettings carries the per-request transport posture an agent should
// know before running or copying a request.
type RequestSettings struct {
	VerifyTLS       bool `json:"verifyTls"`
	FollowRedirects bool `json:"followRedirects"`
	MaxRedirects    int  `json:"maxRedirects"`
}

// RequestDetail is the full, redacted definition of one request.
type RequestDetail struct {
	RequestSummary
	Headers    []KeyValue `json:"headers"`
	Params     []KeyValue `json:"params"`
	PathParams []KeyValue `json:"pathParams,omitempty"`
	// BodyType names the body mode (json, text, form-urlencoded, multipart,
	// graphql, none...); Body is its authored content.
	BodyType string `json:"bodyType,omitempty"`
	Body     string `json:"body,omitempty"`
	// AuthType is the auth mode (none, inherit, basic, bearer, apikey,
	// oauth2...). Auth rows keep {{template}} values as written; literal
	// credential values arrive masked (see MaskAuthValue in redact.go).
	AuthType   string          `json:"authType,omitempty"`
	Auth       []KeyValue      `json:"auth,omitempty"`
	Vars       []KeyValue      `json:"vars,omitempty"`
	PreScript  string          `json:"preScript,omitempty"`
	PostScript string          `json:"postScript,omitempty"`
	Tests      string          `json:"tests,omitempty"`
	Settings   RequestSettings `json:"settings"`
}

// EnvironmentVariable is a variable an agent may reference by name. When
// Secret is true, Value is always the empty string — the adapter never copies
// a secret value into this struct, hydrated or not.
type EnvironmentVariable struct {
	Name    string `json:"name"`
	Value   string `json:"value"`
	Secret  bool   `json:"secret"`
	Enabled bool   `json:"enabled"`
}

// EnvironmentSummary is one environment and its variables.
type EnvironmentSummary struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Scope is "global" (workspace) or "collection".
	Scope        string                `json:"scope"`
	CollectionID string                `json:"collectionId,omitempty"`
	Active       bool                  `json:"active"`
	Variables    []EnvironmentVariable `json:"variables"`
}

// HistoryRun is one recorded execution of a request. Headers arrive already
// redacted (internal/history's rules); Body is bounded by the adapter and
// Truncated says when it was cut.
type HistoryRun struct {
	ID         string     `json:"id"`
	Method     string     `json:"method"`
	URL        string     `json:"url"`
	Status     int        `json:"status"`
	DurationMs int        `json:"durationMs"`
	ExecutedAt string     `json:"executedAt"`
	Headers    []KeyValue `json:"headers,omitempty"`
	Body       string     `json:"body,omitempty"`
	Truncated  bool       `json:"truncated,omitempty"`
}
