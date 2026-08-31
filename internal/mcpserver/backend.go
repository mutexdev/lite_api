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
	// InspectRequest returns the EFFECTIVE request — what a run would
	// actually be built from once the folder chain and the collection have
	// contributed — plus the variable report an agent needs before it can
	// compose a call: which {{names}} the request reads, which of them the
	// chosen environment resolves, and which it must supply itself.
	//
	// environmentID selects the collection environment to resolve against.
	// Empty means NO collection environment, which is a real configuration
	// and not "whatever the app has selected": see RequestInspection's
	// Environment field for why the app's own selection is unknowable here.
	//
	// The redaction rules are GetRequest's, unchanged — templates stay
	// unresolved, a secret variable travels as its name with an empty value,
	// credential-shaped literals are masked — and this method must not
	// interpolate anything, for the reason at the top of the internal/core
	// adapter: resolving a template here is exactly how a secret would reach
	// the agent.
	InspectRequest(collectionID, requestID, environmentID string) (RequestInspection, error)
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

	// WriteTierEnabled reports whether the user has unlocked authoring in
	// Settings, read fresh at the moment of the call — the preference can be
	// turned on and off while the server runs.
	//
	// It exists for describe_usage, which tells an agent which tiers are
	// live before it composes a call. It is NOT the gate: the gate is inside
	// the four write methods below, because a check made here and acted on
	// there would be a race, and because a tier that is enforced anywhere
	// but at the point of the write is enforced nowhere.
	WriteTierEnabled() (bool, error)

	// CreateRequest authors a new request in a collection and returns the row
	// it created, whose id is accepted by every tool that takes a requestId.
	//
	// Implementations reject the whole call — with an ErrDenied-wrapped error
	// and nothing written — when the write tier is off, when the definition
	// carries a script or a test, when any authored row declares itself
	// secret, or when the user declines to let a referenced secret travel to
	// a host the collection has never sent it to. ctx bounds the call and
	// the approval prompt it may raise.
	CreateRequest(ctx context.Context, params CreateRequestParams) (RequestSummary, error)

	// UpdateRequest edits an existing request, matched by id, applying only
	// the fields the caller supplied. Scripts and tests are PRESERVED rather
	// than editable: a call may echo them back unchanged or omit them, and
	// anything else is refused. The same denials as CreateRequest apply.
	UpdateRequest(ctx context.Context, params UpdateRequestParams) (RequestSummary, error)

	// CreateFlow stores a new flow, validated against the same rules the
	// app's own Flow editor uses. A flow carries no URL of its own, so no
	// host approval can arise here; every step is guarded at run time.
	CreateFlow(params CreateFlowParams) (FlowSummary, error)

	// UpdateFlow replaces a flow by id, with the same validation.
	UpdateFlow(params UpdateFlowParams) (FlowSummary, error)
}

// AuthoredRow is one header, param, path param, form field or variable as an
// AGENT wrote it — the input counterpart of KeyValue.
//
// It is a separate type from KeyValue precisely because it carries Secret, and
// KeyValue must not: KeyValue is a return DTO, and the contract at the top of
// this file says no field on it may describe a secret. Secret exists here so
// that "an agent tried to DEFINE a secret" is something the Backend can see and
// refuse, rather than something the decoding layer silently drops — a rule
// enforced by dropping the field is a rule with no error message.
//
// Enabled is a pointer so that "omitted" and "false" are distinguishable; an
// omitted enabled means true, which is what an agent listing headers means.
type AuthoredRow struct {
	Name    string `json:"name"`
	Value   string `json:"value"`
	Enabled *bool  `json:"enabled,omitempty"`
	Secret  bool   `json:"secret,omitempty"`
}

// CreateRequestParams is a whole authored request.
//
// TRANSPORT SETTINGS ARE ABSENT ON PURPOSE. VerifyTLS, redirect policy and
// timeouts decide how safely a request travels, they are the user's posture
// rather than the agent's, and a new request takes LiteAPI's own defaults. An
// agent that needs a different posture asks the user for it.
type CreateRequestParams struct {
	CollectionID string
	Name         string
	// FolderPath places the request in the collection tree ("api/v2"); empty
	// is the collection root. An unknown folder is an error, not a silent
	// demotion to the root.
	FolderPath string
	// Type is the request kind: http or graphql. The socket kinds (ws, grpc)
	// are not authorable here — they carry per-kind message state an agent
	// has no way to fill in.
	Type   string
	Method string
	URL    string

	Headers    []AuthoredRow
	Params     []AuthoredRow
	PathParams []AuthoredRow
	Vars       []AuthoredRow

	// BodyType is the body mode (none, json, text, xml, form-urlencoded,
	// graphql); Body is its content, and FormData the rows for
	// form-urlencoded.
	BodyType string
	Body     string
	// GraphQLVariables is the variables document of a graphql body.
	GraphQLVariables string
	FormData         []AuthoredRow

	// Auth is a flat block: {"mode":"bearer","token":"{{apiToken}}"}. An
	// agent may point a field at a secret VARIABLE; it can never read one.
	Auth map[string]string

	// PreScript, PostScript and Tests exist here so the refusal can be
	// explicit. A non-empty value is denied: see the Backend contract.
	PreScript  string
	PostScript string
	Tests      string
}

// UpdateRequestParams is a patch: a nil field is left as it is on the stored
// request, which is what lets an agent change a URL without restating a body it
// never read.
type UpdateRequestParams struct {
	CollectionID string
	RequestID    string

	Method *string
	URL    *string

	Headers    *[]AuthoredRow
	Params     *[]AuthoredRow
	PathParams *[]AuthoredRow
	Vars       *[]AuthoredRow

	BodyType         *string
	Body             *string
	GraphQLVariables *string
	FormData         *[]AuthoredRow

	Auth map[string]string

	// PreScript, PostScript and Tests are compared against what is stored
	// rather than applied: equal or empty passes and changes nothing,
	// anything else is refused. An agent that echoes get_request's output
	// back therefore succeeds, and one that edits a script does not.
	PreScript  *string
	PostScript *string
	Tests      *string
}

// FlowDefinition is a flow as an agent authors it — the same shape get_flow
// returns, minus the derived stepCount, so a definition can be read, edited and
// written back without reshaping.
type FlowDefinition struct {
	ID          string       `json:"id,omitempty"`
	Name        string       `json:"name"`
	Description string       `json:"description,omitempty"`
	Inputs      []FlowInput  `json:"inputs,omitempty"`
	Steps       []FlowStep   `json:"steps"`
	Outputs     []FlowOutput `json:"outputs,omitempty"`
}

// CreateFlowParams stores one new flow. An empty Flow.ID is assigned by the
// implementation and returned in the summary.
type CreateFlowParams struct {
	CollectionID string
	Flow         FlowDefinition
}

// UpdateFlowParams replaces the flow named by Flow.ID, which is required.
type UpdateFlowParams struct {
	CollectionID string
	Flow         FlowDefinition
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
	AuthType   string     `json:"authType,omitempty"`
	AuthSource string     `json:"authSource,omitempty"`
	Auth       []KeyValue `json:"auth,omitempty"`
	Vars       []KeyValue `json:"vars,omitempty"`
	// GraphQLVariables is the variables document of a graphql body, as
	// authored. Body carries only the QUERY for a graphql request, so without
	// this field an agent reading a GraphQL request could not see which
	// variables the query declares — it would have to infer them from the
	// query text and would miss the defaults the user wrote. Like Body it is
	// unscanned and unresolved: a {{template}} inside it arrives literal.
	GraphQLVariables string          `json:"graphqlVariables,omitempty"`
	PreScript        string          `json:"preScript,omitempty"`
	PostScript       string          `json:"postScript,omitempty"`
	Tests            string          `json:"tests,omitempty"`
	Settings         RequestSettings `json:"settings"`
}

// The levels that can contribute a piece of an effective request, and the
// scopes a variable can resolve from. They are the vocabulary of
// InheritedRow.Level, InheritedScript.Level and VariableReference.Level, and
// they are exported because an agent reads them and a test asserts them.
//
// The first three are the inheritance chain proper — a header, a script or a
// variable written on the request, on a folder in its path, or on the
// collection. The rest only ever appear on a variable, because only variables
// have scopes beyond the tree: the selected collection environment, the
// workspace's active global environments, and the runtime values a script
// persisted with bru.setVar.
const (
	LevelRequest     = "request"
	LevelFolder      = "folder"
	LevelCollection  = "collection"
	LevelEnvironment = "environment"
	LevelGlobal      = "global"
	LevelRuntime     = "runtime"
)

// The kinds a {{token}} in a request can be. Only KindVariable is answerable
// from the collection: the other three resolve somewhere this adapter
// deliberately does not look.
const (
	// KindVariable is an ordinary {{name}}, resolved from the variable scopes.
	KindVariable = "variable"
	// KindDynamic is {{$timestamp}}, {{$guid}} and the rest of the generated
	// set. LiteAPI produces a fresh value at send time, so it always resolves
	// and never needs supplying.
	KindDynamic = "dynamic"
	// KindPrompt is {{?name}}, which asks the USER for a value in the app. A
	// headless run has nobody to ask, so it is reported and never resolved.
	KindPrompt = "prompt"
	// KindProcessEnv is {{process.env.NAME}}, resolved at send time from the
	// process environment and the collection's .env file. Whether one is set,
	// and what it holds, is deliberately not reported: .env is where
	// credentials live, and even a present/absent answer is an oracle over it.
	KindProcessEnv = "processEnv"
)

// InheritedRow is one row of an EFFECTIVE request — a header or a variable —
// together with the level that contributed it.
//
// Level attribution is the whole point of the type. "Authorization: Bearer
// {{apiToken}}" tells an agent what the call sends; "…and it comes from the
// collection, not from this request" tells it what happens if it authors a
// sibling request, and which file the user has to edit to change it.
//
// LevelPath disambiguates a level that can occur more than once: the folder's
// path for LevelFolder, the environment's name for LevelEnvironment and
// LevelGlobal. It is empty for the levels that are unique.
//
// Value obeys the same rules as KeyValue's: templates unresolved, a secret
// variable's value always empty with Secret set, credential-shaped literals
// masked.
type InheritedRow struct {
	Name      string `json:"name"`
	Value     string `json:"value"`
	Enabled   bool   `json:"enabled"`
	Level     string `json:"level"`
	LevelPath string `json:"levelPath,omitempty"`
	Secret    bool   `json:"secret,omitempty"`
}

// InheritedScript is one script level that runs for this request, in the order
// it runs. Phase is "pre", "post" or "tests".
//
// Scripts travel verbatim, exactly as get_request reports them, and for the
// same reason: they are code the user wrote and an agent may need to read to
// understand what the request actually does. The value of listing them here is
// that a request with an empty preScript can still be running two inherited
// ones, which get_request cannot show.
type InheritedScript struct {
	Phase     string `json:"phase"`
	Level     string `json:"level"`
	LevelPath string `json:"levelPath,omitempty"`
	Script    string `json:"script"`
}

// InspectedEnvironment is the environment configuration this inspection was
// resolved against — and, because the two halves behave differently, it
// reports them separately.
//
// CollectionEnvironment is per call: it is the environmentId argument, and
// omitting that argument means NO collection environment applied, not "the one
// the app has selected". LiteAPI's collection-environment selection is
// frontend state, persisted in the WebView rather than in the app state this
// server reads, so there is no active collection environment for a tool to
// fall back to and nothing here is guessing at one.
//
// Global is per workspace, persisted, and applies to every run whatever
// environmentId says — including when it is omitted.
type InspectedEnvironment struct {
	CollectionEnvironmentID   string `json:"collectionEnvironmentId,omitempty"`
	CollectionEnvironmentName string `json:"collectionEnvironmentName,omitempty"`
	// GlobalEnvironmentNames are the workspace's active global environments,
	// in the order their variables layer.
	GlobalEnvironmentIDs   []string `json:"globalEnvironmentIds,omitempty"`
	GlobalEnvironmentNames []string `json:"globalEnvironmentNames,omitempty"`
	// Note states the selection rule in words, so an agent that reads only
	// this payload is not left to infer it from two empty fields.
	Note string `json:"note"`
}

// VariableReference is one {{token}} the effective request reads, with
// everything an agent needs to decide whether it can run the call as it
// stands.
//
// Resolved is the question the reviewer asked for: false means the selected
// environment does not define this name and a run would send the braces
// through literally, so the agent must pass it in run_request's variables (or
// ask the user which environment to use) rather than discovering it from a
// confusing 404.
//
// Where names every place the reference appears — "url", "header:Authorization",
// "body", "auth:token", "variable:baseUrl" for one reached through another
// variable's value — because "storeId is unresolved" and "storeId is unresolved
// and it is in the URL" are different amounts of help.
type VariableReference struct {
	Name string `json:"name"`
	// Kind is variable, dynamic, prompt or processEnv (the Kind* constants).
	Kind     string `json:"kind"`
	Resolved bool   `json:"resolved"`
	// Level and LevelPath say where a resolved variable resolves FROM, in the
	// vocabulary of the Level* constants.
	Level     string `json:"level,omitempty"`
	LevelPath string `json:"levelPath,omitempty"`
	// Secret marks a reference that resolves to a secret variable. It resolves
	// fine — inside LiteAPI — and its value is not readable here or anywhere.
	Secret bool     `json:"secret,omitempty"`
	Where  []string `json:"where,omitempty"`
	// Note explains a kind that cannot simply be looked up.
	Note string `json:"note,omitempty"`
}

// EffectiveSettings is the transport posture a run would actually use, which
// is not the same as the request's stored settings: the app's own preferences
// override or gate several of them.
type EffectiveSettings struct {
	// VerifyTLS is the request's flag ANDed with the app's SSL-verification
	// preference, which is what the send path computes. RequestSettings on
	// RequestDetail reports the stored flag alone, so the two can disagree —
	// and when they do, this one is what happens.
	VerifyTLS bool `json:"verifyTls"`
	// VerifyTLSDisabledBy names which side turned verification off
	// ("request", "appPreference", or empty when it is on), because the fix
	// is in a different place for each.
	VerifyTLSDisabledBy string `json:"verifyTlsDisabledBy,omitempty"`
	FollowRedirects     bool   `json:"followRedirects"`
	MaxRedirects        int    `json:"maxRedirects"`
	// TimeoutMs is the effective timeout: the app preference when it is set,
	// otherwise the request's own, otherwise LiteAPI's 30s default.
	TimeoutMs int `json:"timeoutMs"`
}

// RequestInspection is everything an agent needs to author a correct call
// against one stored request, in one call.
//
// It exists because get_request answers "what is written on this request",
// which is not the question. A request that inherits its auth from the
// collection, its Accept header from a folder and its baseUrl from an
// environment sends none of that from its own definition, and an agent reading
// get_request alone would rebuild the call without any of it. Everything here
// is the EFFECTIVE view, labelled with the level that contributed it.
//
// Request is get_request's own payload, embedded unchanged so that an agent
// never has to make both calls.
type RequestInspection struct {
	Request     RequestDetail        `json:"request"`
	Environment InspectedEnvironment `json:"environment"`
	// Headers are the merged header set in send order: inherited rows first
	// (collection, then outermost folder to innermost), then the request's
	// own. A name the request sets itself suppresses the inherited row
	// entirely, so a name appears at most once.
	Headers []InheritedRow `json:"headers"`
	// Variables are every variable in scope for this request, one row per
	// NAME, carrying the level that actually wins for it. A collection
	// variable shadowed by an environment one appears once, at level
	// "environment" — because that is the value the request will see.
	Variables []InheritedRow `json:"variables"`
	// Scripts are the script levels that run, in execution order.
	Scripts []InheritedScript `json:"scripts,omitempty"`
	// References is every {{token}} the request reads, deduplicated by name.
	References []VariableReference `json:"references"`
	// UnresolvedVariables is the short answer: the names of ordinary
	// variables the request reads that nothing in scope defines. Empty means
	// the request is runnable as it stands.
	UnresolvedVariables []string          `json:"unresolvedVariables"`
	Settings            EffectiveSettings `json:"settings"`
	// NotResolved lists, in words, what this tool deliberately does not
	// answer, so an agent does not read an empty UnresolvedVariables as a
	// promise that the call will succeed.
	NotResolved []string `json:"notResolved"`
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
