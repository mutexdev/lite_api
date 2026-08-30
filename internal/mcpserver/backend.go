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

import (
	"context"
	"errors"
)

// DefaultPort is the port the server binds when preferences carry no explicit
// choice. Port 0 remains valid and asks the OS for an ephemeral port.
const DefaultPort = 43117

// ErrDenied marks a refusal — the new-host guard blocked a run, the user
// declined the approval prompt, or a tier is disabled. Backend implementations
// wrap it (errors.Is must hold) so the server can audit the outcome as
// "denied" rather than "error"; the wrapped message is what the agent reads,
// and like every error it must be secret-free.
var ErrDenied = errors.New("denied")

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
	// RunRequest executes a stored request through the app's own send path —
	// scripts, TLS posture, client certificates, history recording, all of
	// it — with secrets resolving only inside the process. Implementations
	// reject overrides of secret variables, enforce the new-host guard, and
	// wrap ErrDenied for every refusal. ctx bounds the run; cancellation must
	// cancel the underlying request.
	RunRequest(ctx context.Context, params RunRequestParams) (RunResult, error)
	// ListFlows returns the flows of one collection in stored order.
	ListFlows(collectionID string) ([]FlowSummary, error)
	// GetFlow returns one flow's full definition. A flow holds no secret
	// value by construction — a step var that names one travels as the
	// {{template}} it was written as — so the definition is reported as
	// authored, exactly like a request's.
	GetFlow(collectionID, flowID string) (FlowDetail, error)
	// RunFlow executes a flow: every step goes through the same send path a
	// single run does, and the new-host guard is enforced BEFORE each step
	// rather than once for the flow. Implementations wrap ErrDenied for every
	// refusal, mask known secret values in everything a live response could
	// have echoed (extractions, assertion details, step errors, outputs), and
	// return the outcome populated as far as the run got even when the error
	// is non-nil. ctx bounds the whole flow.
	RunFlow(ctx context.Context, params RunFlowParams) (FlowRunOutcome, error)
}

// RunRequestParams identifies what to run and how.
type RunRequestParams struct {
	CollectionID  string
	RequestID     string
	EnvironmentID string
	// Variables layer over the resolved variable context for this run only.
	// A name that resolves to a secret variable in scope is rejected — an
	// override is how an agent would smuggle a secret into a field it can
	// read back.
	Variables map[string]string
}

// TestResult is one scripted test's outcome from the run.
type TestResult struct {
	Name    string `json:"name"`
	Passed  bool   `json:"passed"`
	Message string `json:"message,omitempty"`
}

// RunResult is what the agent gets back from a run: the response, bounded and
// redacted the same way history is (URL query literals masked, known secret
// values scrubbed, credential-shaped headers masked), plus the scripted test
// outcomes.
type RunResult struct {
	Status      int          `json:"status"`
	StatusText  string       `json:"statusText,omitempty"`
	DurationMs  int          `json:"durationMs"`
	ExecutedAt  string       `json:"executedAt"`
	URL         string       `json:"url"`
	Headers     []KeyValue   `json:"headers,omitempty"`
	Body        string       `json:"body,omitempty"`
	Truncated   bool         `json:"truncated,omitempty"`
	TestResults []TestResult `json:"testResults,omitempty"`
}

// CollectionSummary names a collection an agent can explore. Both counts are
// carried because they answer the agent's first question — is there anything
// here worth a second call — for the two kinds of thing a collection holds.
type CollectionSummary struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	RequestCount int    `json:"requestCount"`
	FlowCount    int    `json:"flowCount"`
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
	// AuthType is the EFFECTIVE auth mode (none, basic, bearer, apikey,
	// oauth2...): when the request inherits, the folder's or collection's
	// mode is reported instead of the word "inherit", and AuthSource says
	// which level supplied it ("request", "folder", "collection", or empty
	// when nothing configures auth). Auth rows keep {{template}} values as
	// written; literal credential values arrive masked (MaskAuthRows).
	AuthType   string          `json:"authType,omitempty"`
	AuthSource string          `json:"authSource,omitempty"`
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

// RunFlowParams identifies which flow to run and with what.
type RunFlowParams struct {
	CollectionID  string
	FlowID        string
	EnvironmentID string
	// Inputs are the flow's DECLARED inputs. An undeclared name is refused by
	// the implementation rather than ignored: a flow that accepted a typo
	// would run every step against an empty value and report whatever the API
	// said about nothing. Inputs are flow scope, not the environment — they
	// cannot name a secret variable, and a flow that declared one is refused
	// when it is saved and again when it runs.
	Inputs map[string]string
}

// FlowInput is one value a flow's caller supplies.
type FlowInput struct {
	Name        string `json:"name"`
	Required    bool   `json:"required,omitempty"`
	Description string `json:"description,omitempty"`
}

// FlowSummary is one flow as a list row: enough to decide whether to run it
// (what it is called, what it does, how long it is) and what to pass when you
// do, without a second call for the definition.
type FlowSummary struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	StepCount   int    `json:"stepCount"`
	// Inputs are the declared inputs, so the run_flow call can be composed
	// straight from a list row.
	Inputs []FlowInput `json:"inputs,omitempty"`
}

// FlowExtract pulls one value out of a step's response into flow scope. From
// is "body" (Path is a JSONPath subset), "header" (Path is the header name) or
// "status" (Path is unused).
type FlowExtract struct {
	Name string `json:"name"`
	From string `json:"from"`
	Path string `json:"path,omitempty"`
}

// FlowAssert is one check against a step's response. Type is "status" (Equals
// or In) or "body" (Path plus Equals, Contains or Exists).
//
// Equals is untyped because the schema it mirrors is: {"type":"status",
// "equals":200} and {"type":"body","equals":"created"} are both legal.
type FlowAssert struct {
	Type     string `json:"type"`
	Equals   any    `json:"equals,omitempty"`
	In       []int  `json:"in,omitempty"`
	Path     string `json:"path,omitempty"`
	Contains string `json:"contains,omitempty"`
	Exists   bool   `json:"exists,omitempty"`
}

// FlowStep is one request in the chain, as authored.
//
// Vars ARE NOT RESOLVED, and that is a safety property rather than an omission:
// a step var is interpolated against flow scope alone at run time, so
// {"token":"{{apiToken}}"} stays those literal braces here and is resolved, if
// at all, by the send path inside LiteAPI. There is no field on this struct
// that a secret value could travel in.
type FlowStep struct {
	ID        string            `json:"id"`
	RequestID string            `json:"requestId"`
	Vars      map[string]string `json:"vars,omitempty"`
	Extract   []FlowExtract     `json:"extract,omitempty"`
	Assert    []FlowAssert      `json:"assert,omitempty"`
}

// FlowOutput names what the flow hands back to its caller. Value is a template
// resolved against the final flow scope, and travels here unresolved.
type FlowOutput struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// FlowDetail is one flow's full definition. It embeds the summary for the same
// reason RequestDetail does: an agent that already listed flows sees the same
// fields in the same shape, and a step's requestId is accepted by get_request.
type FlowDetail struct {
	FlowSummary
	Steps   []FlowStep   `json:"steps"`
	Outputs []FlowOutput `json:"outputs,omitempty"`
}

// FlowAssertionOutcome reports one assertion. Detail reads the same way whether
// it passed or failed, so a run report can show every check rather than only
// the broken one.
type FlowAssertionOutcome struct {
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
}

// FlowStepOutcome is one step's result. Extracted, Assertions and Error can all
// carry text taken from a live response body, so every one of them is masked by
// the implementation before it gets here.
type FlowStepOutcome struct {
	StepID     string                 `json:"stepId"`
	RequestID  string                 `json:"requestId"`
	Status     int                    `json:"status"`
	DurationMs int                    `json:"durationMs"`
	Extracted  map[string]string      `json:"extracted,omitempty"`
	Assertions []FlowAssertionOutcome `json:"assertions,omitempty"`
	Error      string                 `json:"error,omitempty"`
}

// FlowRunOutcome is one execution of a flow.
//
// OK is the question a caller asks first: every step ran, every assertion held,
// every extraction found its path. Steps is as complete as the run got — a
// fail-fast flow that stopped at step 2 carries two step outcomes and not
// three, which is itself the report that step 3 never ran.
type FlowRunOutcome struct {
	OK      bool              `json:"ok"`
	Error   string            `json:"error,omitempty"`
	Steps   []FlowStepOutcome `json:"steps"`
	Outputs map[string]string `json:"outputs,omitempty"`
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
