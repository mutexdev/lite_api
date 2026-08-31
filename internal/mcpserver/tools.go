// The tool registry: what an agent can call, what each call promises, and the
// hand-written validation that stands between a client's arguments and the
// Backend.
//
// The registry is a table of (name, description, schema, handler). The
// dispatcher in protocol.go knows nothing about any individual tool, so the
// later run and write tiers add entries here and change nothing else.
//
// The descriptions are written for an AI reader rather than a human browsing
// docs: each one says what comes back, states plainly that secrets are masked
// and {{templates}} are unresolved, and names the tool to call next. An agent
// that reads only tools/list should be able to use this server correctly, and
// should not waste a call discovering that it cannot read a secret.
//
// Validation is hand-written for the same reason the protocol is: the schemas
// are a handful of shallow objects of strings and integers, and a JSON Schema library
// would be a dependency whose error messages we do not control. The messages
// matter — an agent recovers from "missing required argument collectionId"
// and stalls on "does not match schema".
package mcpserver

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

// RunTimeout bounds one run_request call. It is generous because the request
// being run is the user's own — a slow staging host or a long-polling endpoint
// is a legitimate answer, not a hang — and short enough that a wedged run
// cannot pin a handler goroutine for the life of the process.
const RunTimeout = 120 * time.Second

// FlowRunTimeout bounds one run_flow call. A flow is SEVERAL requests through
// the same send path, run one after another, so the budget that fits a single
// request would fail a perfectly healthy three-step chain against a slow
// staging host. It is a ceiling on the whole flow rather than per step, because
// the guarantee worth making to the handler goroutine is that the call ends.
const FlowRunTimeout = 300 * time.Second

// toolArgs is one call's arguments as decoded from JSON, so values are the
// usual encoding/json shapes: string, float64, bool, map, slice.
type toolArgs map[string]any

// toolHandler runs one tool. The returned value is marshalled to compact JSON
// and delivered as the call's single text content block; the returned error
// becomes an isError result whose text the agent reads.
type toolHandler func(backend Backend, args toolArgs) (any, error)

// toolEntry is one row of the registry. Handler is skipped by the marshaller
// so an entry serialises straight into a tools/list element.
type toolEntry struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	InputSchema inputSchema `json:"inputSchema"`
	Handler     toolHandler `json:"-"`
}

// inputSchema is the JSON Schema subset the tools declare: an object with
// typed, described properties and a required list. Required is never nil, so
// a no-argument tool advertises [] rather than null.
type inputSchema struct {
	Type       string                    `json:"type"`
	Properties map[string]schemaProperty `json:"properties"`
	Required   []string                  `json:"required"`
}

type schemaProperty struct {
	Type        string `json:"type"`
	Description string `json:"description"`
	// AdditionalProperties declares the value type of an object property, and
	// is the whole of JSON Schema's map vocabulary this package needs. It is
	// both documentation for the agent composing the call and the rule
	// validate enforces, so the two cannot drift.
	AdditionalProperties *schemaProperty `json:"additionalProperties,omitempty"`
	// Items declares the element type of an array property. Like
	// AdditionalProperties it is documentation and rule at once: checkArgType
	// enforces exactly what it says, and nothing deeper. The write tier's row
	// arrays are objects whose own fields are checked when they are decoded,
	// with messages that can name the offending row — which a schema walker
	// could not do.
	Items *schemaProperty `json:"items,omitempty"`
}

// rowArray describes an array of authored rows: the {name, value, enabled}
// shape get_request already returns, so an agent can read a request's headers,
// change one, and write them straight back.
func rowArray(description string) schemaProperty {
	return schemaProperty{
		Type:        "array",
		Description: description,
		Items: &schemaProperty{
			Type:        "object",
			Description: "A row: {\"name\":\"Accept\",\"value\":\"application/json\"}. enabled defaults to true. secret is refused — agents may reference a secret variable by name but never define one.",
		},
	}
}

// stringValuedObject describes an object whose every value must be a string.
func stringValuedObject(description string) schemaProperty {
	return schemaProperty{
		Type:                 "object",
		Description:          description,
		AdditionalProperties: &schemaProperty{Type: "string"},
	}
}

// objectSchema builds a schema with the empty-not-nil guarantees the wire
// format needs.
func objectSchema(properties map[string]schemaProperty, required ...string) inputSchema {
	if properties == nil {
		properties = map[string]schemaProperty{}
	}
	if required == nil {
		required = []string{}
	}
	return inputSchema{Type: "object", Properties: properties, Required: required}
}

// limitProperty is the shared description of the optional cap on list length,
// which behaves the same way wherever it appears.
var limitProperty = schemaProperty{
	Type:        "integer",
	Description: "Maximum number of rows to return. Omit it to accept LiteAPI's own default.",
}

// toolRegistry is the read tier. Order is the order tools/list reports, which
// is also roughly the order an agent should discover them in.
var toolRegistry = []toolEntry{
	{
		Name: "list_collections",
		Description: "Lists every collection currently open in LiteAPI, each with its id, name, request count, and flow count. " +
			"Start here: the collectionId values it returns are exactly what list_requests, get_request, get_history, and list_flows expect. " +
			"Returns a JSON array, empty when the user has no collections open. Takes no arguments.",
		InputSchema: objectSchema(nil),
		Handler:     toolListCollections,
	},
	{
		Name: "list_requests",
		Description: "Lists the requests in one collection, in the order they appear in LiteAPI's tree, each with id, collectionId, name, " +
			"type (http, graphql, ws, grpc), method, URL, and folder path. URLs come back exactly as the user authored them, so {{templates}} " +
			"are unresolved and must stay that way — call list_environments to see which variable names exist. Call get_request for the full " +
			"definition of any row that looks relevant.",
		InputSchema: objectSchema(map[string]schemaProperty{
			"collectionId": {Type: "string", Description: "Id of the collection to list, from list_collections."},
		}, "collectionId"),
		Handler: toolListRequests,
	},
	{
		Name: "search_requests",
		Description: "Searches every open collection for requests whose name, method, URL, or folder path contains query (case-insensitive " +
			"substring) and returns the same rows as list_requests, with ordering stable across calls. Prefer this over listing every " +
			"collection when you roughly know what you want, e.g. \"checkout\" or \"orders\". Omitting query — or passing an empty one — " +
			"matches everything, listing every request across all collections up to limit, which is the cheapest way to see the whole " +
			"workspace when you do not yet know what to look for. URLs keep their unresolved {{templates}}. Follow up with get_request " +
			"using the id and collectionId of the best match.",
		// "query" is deliberately NOT required. The Backend contract defines an
		// empty query as "match everything", and declaring it required would put
		// validate's empty-string check in front of that behaviour: the one call
		// an agent reaches for to see everything would be the one call that
		// always failed.
		InputSchema: objectSchema(map[string]schemaProperty{
			"query": {Type: "string", Description: "Case-insensitive substring to match against request name, method, URL, and folder path. Omit it, or pass \"\", to match every request."},
			"limit": limitProperty,
		}),
		Handler: toolSearchRequests,
	},
	{
		Name: "get_request",
		Description: "Returns one request's full definition: method, URL, headers, query and path params, body, auth, pre/post scripts, and " +
			"transport settings. Everything is as authored, so {{templates}} are never resolved and a secret variable's value can never reach " +
			"you through this tool. Credential-shaped literals ARE masked to \"<masked>\" in three places: header and param rows, the query " +
			"string of the URL itself, and auth rows (auth values are masked unless the field only addresses a provider, e.g. username or " +
			"clientId). Body content, pre/post scripts and tests pass through EXACTLY AS AUTHORED and are not scanned — so a credential the " +
			"user typed literally into a body or a script is visible here. Treat anything credential-shaped you see as sensitive, never repeat " +
			"it back, and tell the user to move it into a {{variable}}. authType is the EFFECTIVE auth mode: when a request inherits, the " +
			"folder's or collection's mode is reported rather than \"inherit\", and authSource says which level configured it (\"request\", " +
			"\"folder\", \"collection\", or empty when nothing does). Read this to learn the shape of a call; to actually execute one, call " +
			"run_request, which runs the stored request inside LiteAPI where secrets resolve without crossing this boundary. Call " +
			"get_history to see what its responses have looked like.",
		InputSchema: objectSchema(map[string]schemaProperty{
			"collectionId": {Type: "string", Description: "Id of the collection holding the request, from list_collections."},
			"requestId":    {Type: "string", Description: "Id of the request, from list_requests or search_requests."},
		}, "collectionId", "requestId"),
		Handler: toolGetRequest,
	},
	{
		Name: "inspect_request",
		Description: "Returns the EFFECTIVE request — what a run would actually be built from — plus the variable report you need before you can compose a call. " +
			"Call this INSTEAD OF get_request whenever you are about to run, reproduce, or write a request that resembles this one; get_request shows only what is " +
			"written ON the request, and a request that carries no headers, no auth and no variables of its own can still send all three because a folder, the " +
			"collection or an environment supplied them. It returns: request (get_request's whole payload, so you never need both calls, and it now includes " +
			"graphqlVariables for a GraphQL request); headers (the merged set in send order, each row labelled with the level that contributed it — \"request\", " +
			"\"folder\" with its path, or \"collection\" — where a name the request sets itself suppresses the inherited row); variables (every variable in scope, " +
			"one row per name, labelled with the level that actually WINS for it: request, folder, collection, environment, global or runtime); scripts (every " +
			"pre/post/tests level that runs, in execution order, with its level — a request with an empty preScript can still be running two inherited ones); " +
			"references (every {{token}} the request reads, with kind, whether it resolves, where it resolves from, and where in the request it appears); " +
			"unresolvedVariables (the short answer: ordinary variable names nothing in scope defines — pass these in run_request's variables, or pick an " +
			"environment that defines them, rather than discovering them from a failed run); environment (the environment actually in effect); and settings (the " +
			"transport posture a run really uses, which can differ from the stored one because the app's own preferences gate it). " +
			"WHAT IT DOES NOT RESOLVE, and the notResolved field repeats this: nothing is interpolated, so every value is as authored and a {{template}} is a " +
			"reference and never a value; scripts are reported but NOT executed, so a request with scripts can send something no static inspection can show; " +
			"{{process.env.NAME}} references are listed but never checked, because .env is where credentials live; and {{?name}} prompt variables need the USER, " +
			"so a run started from here cannot supply one. Redaction is get_request's, unchanged: secret variables travel as a name with an empty value and a " +
			"secret flag, credential-shaped literals in header, param and auth rows are masked to \"<masked>\", and body content and scripts pass through exactly " +
			"as authored and are not scanned.",
		InputSchema: objectSchema(map[string]schemaProperty{
			"collectionId": {Type: "string", Description: "Id of the collection holding the request, from list_collections."},
			"requestId":    {Type: "string", Description: "Id of the request, from list_requests or search_requests."},
			"environmentId": {Type: "string", Description: "Id of the COLLECTION environment to resolve variable names against, from list_environments — the same argument run_request takes, " +
				"so inspecting with one id and running with it gives the same answer. Omitting it means NO collection environment applies, which is a real " +
				"configuration and not a fallback to whatever is selected in the app's window: that selection is frontend state this server cannot read. The " +
				"workspace's active global environment applies either way and cannot be chosen here."},
		}, "collectionId", "requestId"),
		Handler: toolInspectRequest,
	},
	{
		Name: "list_environments",
		Description: "Lists the global and per-collection environments with their variable names, each variable's secret flag and enabled flag, " +
			"and which environment is active. Non-secret values come back as authored; the value of a secret variable is ALWAYS empty — you can " +
			"refer to a secret by name inside a {{template}}, but you can never read it, and nothing you call will reveal it. Use this to work out " +
			"which variables the {{templates}} in a request refer to. Takes no arguments.",
		InputSchema: objectSchema(nil),
		Handler:     toolListEnvironments,
	},
	{
		Name: "list_flows",
		Description: "Lists the Flows stored in one collection — a Flow is a named, ordered chain of the collection's own requests with data wired from " +
			"each response into the next, assertions, and declared outputs. Each row carries id, name, description, stepCount, and the inputs the flow " +
			"declares (name, whether it is required, what it is for), which is everything run_flow needs. Look here BEFORE stitching several run_request " +
			"calls together by hand: if the user has already written the chain, running theirs uses their wiring and their assertions instead of your " +
			"guess at them. Call get_flow for the steps, run_flow to execute one. Returns a JSON array, empty when the collection has no flows.",
		InputSchema: objectSchema(map[string]schemaProperty{
			"collectionId": {Type: "string", Description: "Id of the collection whose flows to list, from list_collections."},
		}, "collectionId"),
		Handler: toolListFlows,
	},
	{
		Name: "get_flow",
		Description: "Returns one flow's full definition: its declared inputs, every step in order (step id, the requestId it runs, the vars it sets, what " +
			"it extracts from the response, what it asserts), and its declared outputs. Everything is as authored, so {{templates}} are NEVER resolved — a " +
			"step var written as {\"token\":\"{{apiToken}}\"} comes back as exactly that text, and the value it names is resolved only inside LiteAPI at send " +
			"time, if at all. A flow therefore cannot carry a secret value and this tool cannot reveal one. Each step's requestId is accepted by get_request, " +
			"which is how to see what a step actually sends. Read this to understand a chain; call run_flow to execute it.",
		InputSchema: objectSchema(map[string]schemaProperty{
			"collectionId": {Type: "string", Description: "Id of the collection holding the flow, from list_collections."},
			"flowId":       {Type: "string", Description: "Id of the flow, from list_flows."},
		}, "collectionId", "flowId"),
		Handler: toolGetFlow,
	},
	{
		Name: "get_history",
		Description: "Returns recent recorded runs of one request, newest first, each with status, duration, response headers, and the response " +
			"body (bounded — truncated says when it was cut). This is how to learn a real response shape without making a network call. Sensitive " +
			"response headers such as Authorization and Set-Cookie are masked; bodies pass through as recorded. An empty array means the request " +
			"has not been run recently, not that it is broken.",
		InputSchema: objectSchema(map[string]schemaProperty{
			"collectionId": {Type: "string", Description: "Id of the collection holding the request, from list_collections."},
			"requestId":    {Type: "string", Description: "Id of the request, from list_requests or search_requests."},
			"limit":        limitProperty,
		}, "collectionId", "requestId"),
		Handler: toolGetHistory,
	},
	{
		Name: "describe_usage",
		Description: "Returns the machine-readable guide to this server: the safety rules that bind every tool and are enforced inside LiteAPI rather than " +
			"by these descriptions, which tiers exist and whether the write tier is currently unlocked, the full Flow schema with its semantics, the rules " +
			"that govern authoring (no scripts, no secret definitions, and the host approval a new target raises), a worked example, and the conventions " +
			"the tools share. Call it BEFORE authoring anything with create_request, update_request, create_flow or update_flow — it is cheaper than a " +
			"refused call, and it is the only place the flow schema is written out in full. Always available, takes no arguments, and reads nothing of the " +
			"user's: the answer is the same whatever their collections contain, apart from the write tier's live enabled flag.",
		InputSchema: objectSchema(nil),
		Handler:     toolDescribeUsage,
	},
	{
		Name: "run_request",
		Description: "Runs a stored request inside LiteAPI, exactly the way the user's own send button does: their auth, their TLS posture, their " +
			"client certificates, their pre/post scripts and tests. Secrets resolve INSIDE LiteAPI and never appear in what you get back, so you " +
			"can execute a call that needs a credential you are not allowed to read. Prefer this over reconstructing the request yourself — a " +
			"hand-built call cannot see the user's secrets and will not match what the app sends. Pass variables to override NON-SECRET variables " +
			"for this one run only; every value must be a string, nothing is written back to the environment, and an attempt to override a secret " +
			"is refused. A run that would send a secret to a host the collection has never sent that secret to PAUSES for the user's approval in " +
			"the app, and may come back denied: a denial names its reason, and the fix is never to retry or to work around it but to ASK THE USER " +
			"to approve the host (or to point you at the right one). The result carries status and statusText, response headers, the response body " +
			"(bounded — truncated says when it was cut), duration, the executedAt timestamp, the resolved URL with any query-string credentials " +
			"masked, and testResults for the request's scripted tests. Read get_request first if you need to know what the call will send. " +
			"Chaining several requests together is not this tool's job: call list_flows, and run_flow to execute a chain the user has already written.",
		InputSchema: objectSchema(map[string]schemaProperty{
			"collectionId": {Type: "string", Description: "Id of the collection holding the request, from list_collections."},
			"requestId":    {Type: "string", Description: "Id of the request to run, from list_requests or search_requests."},
			// THE TRUTH, not the convenient claim this used to make. Omitting
			// environmentId does NOT fall back to the app's selection: that
			// selection is frontend state, persisted in the WebView and never
			// written to the app state this server reads, so there is nothing
			// to fall back TO. The run resolves with no collection environment
			// at all, which for a collection that keeps its baseUrl in an
			// environment is a materially different call.
			"environmentId": {Type: "string", Description: "Id of the COLLECTION environment to resolve variables against, from list_environments. Omitting it means NO collection environment applies — it does " +
				"NOT pick up whichever environment is selected in the app's window, because that selection is frontend state this server cannot read. If the request " +
				"needs one (its {{baseUrl}} usually lives there), pass the id; call inspect_request to see what a given environment does and does not resolve. The " +
				"workspace's active global environment applies either way and cannot be selected here — passing a global environment's id is refused."},
			"variables": stringValuedObject("Non-secret variable overrides for this run only, as an object of string values, e.g. {\"storeId\":\"str_42\"}. " +
				"Numbers and booleans must be quoted. Overriding a secret variable is refused."),
		}, "collectionId", "requestId"),
		Handler: toolRunRequest,
	},
	{
		Name: "run_flow",
		Description: "Runs a stored Flow inside LiteAPI: every step is executed through the user's own send path, in order, with each step's response feeding " +
			"the next exactly as the flow authored it — the same run the user gets from the app's Flow tab. Prefer this over driving the chain yourself with " +
			"run_request: the flow already carries the wiring, the assertions and the outputs, and reproducing them by hand is where the mistakes are. Pass " +
			"inputs as an object of string values; every key must be an input the flow DECLARES (call list_flows or get_flow to see them), and an undeclared " +
			"name is refused with the declared list named — it is not ignored, because a typo would otherwise run the whole chain against an empty value. " +
			"Flows cannot read secrets: a step var like {{apiToken}} is not resolved into flow scope, it passes through to the send path unresolved and is " +
			"resolved there, inside LiteAPI, or not at all. The new-host guard applies to EVERY step, so a flow that would send a secret to a host the " +
			"collection has never sent it to PAUSES for the user's approval and may come back denied: a denial names its reason, and the fix is never to " +
			"retry or to work around it but to ASK THE USER to approve the host (or to point you at the right one). While the write tier is on, a stored step " +
			"var whose value reaches a secret also pauses for approval — with writes enabled LiteAPI cannot tell the user's own step var from one an agent " +
			"wrote, so it asks once and remembers the answer; a denial there is answered the same way, by asking the user, never by rewriting the flow. " +
			"The result carries ok, per-step outcomes " +
			"(stepId, requestId, status, durationMs, the values extracted with any secret masked, assertion results, and an error when the step failed) and " +
			"the flow's declared outputs. Steps run fail-fast, so a run that stopped at step 2 reports two steps and not three — that is the report that " +
			"step 3 never ran, not a truncated answer.",
		InputSchema: objectSchema(map[string]schemaProperty{
			"collectionId": {Type: "string", Description: "Id of the collection holding the flow, from list_collections."},
			"flowId":       {Type: "string", Description: "Id of the flow to run, from list_flows."},
			// Same correction as run_request's: see the note there.
			"environmentId": {Type: "string", Description: "Id of the COLLECTION environment every step resolves variables against, from list_environments. Omitting it means NO collection environment applies to " +
				"any step — it does NOT pick up whichever environment is selected in the app's window, because that selection is frontend state this server cannot " +
				"read. The workspace's active global environment applies either way and cannot be selected here."},
			"inputs": stringValuedObject("Values for the flow's declared inputs, as an object of string values, e.g. {\"storeCode\":\"DHK-04\"}. " +
				"Numbers and booleans must be quoted. A name the flow does not declare is refused, and so is a missing required input."),
		}, "collectionId", "flowId"),
		Handler: toolRunFlow,
	},

	// The write tier. These four are listed whether or not the user has
	// unlocked it: a tool that vanished when the preference was off would tell
	// an agent the capability does not exist, and it would compose a fragile
	// hand-built substitute instead of asking the user for the one switch that
	// makes the real thing work. They are rejected, not hidden — the refusal
	// says what to ask for.
	{
		Name: "create_request",
		Description: "Authors a new request in one of the user's collections, through the same model and the same file writes the app's own editor uses, and " +
			"returns the created row (its id is accepted by get_request, run_request and every other tool that takes a requestId). REQUIRES the write tier, " +
			"which is off by default: when it is off this call is refused and the only fix is to ASK THE USER to turn on \"Allow AI tools to create and edit " +
			"requests\" in LiteAPI's Settings → AI access. Three things can never be authored, whatever the tier: pre-request scripts, post-response scripts " +
			"and tests (they run inside the user's engine and could retarget a credential past the host guard), any row marked secret (reference a secret by " +
			"name in a {{template}} instead — you may do that freely), and transport settings such as TLS verification (the new request takes LiteAPI's " +
			"defaults). If the request points a secret variable at a host the user's collections have never sent that secret to, LiteAPI pauses for the " +
			"user's approval and the call may come back denied: do not retry, do not route around it, ask the user. Call describe_usage first if you have " +
			"not already.",
		InputSchema: objectSchema(map[string]schemaProperty{
			"collectionId":     {Type: "string", Description: "Id of the collection to author in, from list_collections."},
			"name":             {Type: "string", Description: "Human name for the request; it also names the file on disk, e.g. \"Create terminal\"."},
			"url":              {Type: "string", Description: "URL as authored, with {{templates}} left unresolved, e.g. \"{{baseUrl}}/terminals\". Reuse the variable names list_environments reports."},
			"method":           {Type: "string", Description: "HTTP method. Defaults to GET."},
			"type":             {Type: "string", Description: "Request kind: http (default) or graphql."},
			"folderPath":       {Type: "string", Description: "Folder to create it in, as list_requests reports folderPath, e.g. \"api/v2\". Omit for the collection root. An unknown folder is an error."},
			"headers":          rowArray("Header rows, in get_request's own shape: [{\"name\":\"Authorization\",\"value\":\"Bearer {{apiToken}}\"}]."),
			"params":           rowArray("Query parameter rows."),
			"pathParams":       rowArray("Path parameter rows, for a URL with :placeholders."),
			"vars":             rowArray("Request-level variables. A row marked secret is refused."),
			"bodyType":         {Type: "string", Description: "Body mode: none (default), json, text, xml, form-urlencoded, or graphql."},
			"body":             {Type: "string", Description: "Body content for json, text, xml, or the query document for graphql. Send it as a string, not as a nested object."},
			"graphqlVariables": {Type: "string", Description: "Variables document for a graphql body, as a JSON string."},
			"formData":         rowArray("Field rows for a form-urlencoded body."),
			"auth": stringValuedObject("Auth block as flat strings, e.g. {\"mode\":\"bearer\",\"token\":\"{{apiToken}}\"}. mode is none, inherit, basic, bearer or apikey; " +
				"point credential fields at {{variables}} rather than typing values. Richer modes (oauth2, awsv4, oauth1) are configured by the user in the app."),
			"preScript":  {Type: "string", Description: "Must be empty or omitted. Scripts cannot be authored over MCP; ask the user to write one in the app."},
			"postScript": {Type: "string", Description: "Must be empty or omitted, for the same reason as preScript."},
			"tests":      {Type: "string", Description: "Must be empty or omitted, for the same reason as preScript."},
		}, "collectionId", "name", "url"),
		Handler: toolCreateRequest,
	},
	{
		Name: "update_request",
		Description: "Edits an existing request in place, matched by requestId, and returns the updated row. Only the fields you pass are changed — omit a field " +
			"and the stored one is kept, so you can change a URL without restating a body. REQUIRES the write tier exactly as create_request does, with the " +
			"same refusal and the same fix (ask the user to enable it in Settings → AI access). preScript, postScript and tests are PRESERVED, never edited: " +
			"pass them back byte-for-byte as get_request returned them, or leave them out; anything else is refused, because a script runs inside the user's " +
			"engine and could send a credential somewhere the host guard never checked. A row marked secret is refused. If your edit points a secret variable " +
			"at a host the collections have never sent it to, the save pauses for the user's approval and may be denied — ask the user rather than retrying. " +
			"Renaming and moving a request are the user's actions in the app, so name and folderPath are not editable here.",
		InputSchema: objectSchema(map[string]schemaProperty{
			"collectionId":     {Type: "string", Description: "Id of the collection holding the request, from list_collections."},
			"requestId":        {Type: "string", Description: "Id of the request to edit, from list_requests, search_requests or create_request."},
			"method":           {Type: "string", Description: "New HTTP method."},
			"url":              {Type: "string", Description: "New URL, with {{templates}} unresolved."},
			"headers":          rowArray("Replacement header rows — the whole list, not a delta. Read get_request first and send it back edited."),
			"params":           rowArray("Replacement query parameter rows — the whole list."),
			"pathParams":       rowArray("Replacement path parameter rows — the whole list."),
			"vars":             rowArray("Replacement request-level variables — the whole list. A row marked secret is refused."),
			"bodyType":         {Type: "string", Description: "New body mode: none, json, text, xml, form-urlencoded, or graphql."},
			"body":             {Type: "string", Description: "New body content, as a string."},
			"graphqlVariables": {Type: "string", Description: "New variables document for a graphql body, as a JSON string."},
			"formData":         rowArray("Replacement rows for a form-urlencoded body."),
			"auth":             stringValuedObject("Replacement auth block as flat strings, e.g. {\"mode\":\"bearer\",\"token\":\"{{apiToken}}\"}."),
			"preScript":        {Type: "string", Description: "Only accepted unchanged: pass exactly what get_request returned, or omit it. A different non-empty value is refused."},
			"postScript":       {Type: "string", Description: "Only accepted unchanged, like preScript."},
			"tests":            {Type: "string", Description: "Only accepted unchanged, like preScript."},
		}, "collectionId", "requestId"),
		Handler: toolUpdateRequest,
	},
	{
		Name: "create_flow",
		Description: "Stores a new Flow — a named, ordered chain of the collection's own requests with data wired from each response into the next, assertions, " +
			"and declared outputs — and returns its row, whose id run_flow and get_flow accept. REQUIRES the write tier, which is off by default: when it is " +
			"off this call is refused and the only fix is to ASK THE USER to enable it in LiteAPI's Settings → AI access. Pass the whole flow as one object; call describe_usage for the schema, the semantics and a worked example, and get_flow " +
			"to see one the user already wrote. Validation is the app's own: an unknown requestId, a duplicate step id, an extraction with no path, an " +
			"assertion that checks nothing, or a flow name that shadows a secret variable are all refused with the reason named. A step var written as " +
			"{{apiToken}} stays literal — flow scope never resolves a secret — so a flow cannot carry a credential and needs no host approval to save.",
		InputSchema: objectSchema(map[string]schemaProperty{
			"collectionId": {Type: "string", Description: "Id of the collection to store the flow in, from list_collections."},
			"flow": {Type: "object", Description: "The flow: {name, description?, inputs?, steps, outputs?}. Every step is {id, requestId, vars?, extract?, assert?} and " +
				"every requestId must be a request in this collection. Omit id and one is assigned. Call describe_usage for the full schema."},
		}, "collectionId", "flow"),
		Handler: toolCreateFlow,
	},
	{
		Name: "update_flow",
		Description: "Replaces an existing Flow by id with the definition you pass — the whole flow, not a delta, so read it with get_flow first and send it back " +
			"edited — and returns its row. REQUIRES the write tier, which is off by default: when it is off this call is refused and the only fix is to ASK " +
			"THE USER to enable it in LiteAPI's Settings → AI access. Validation is create_flow's: every error names the step and what to change.",
		InputSchema: objectSchema(map[string]schemaProperty{
			"collectionId": {Type: "string", Description: "Id of the collection holding the flow, from list_collections."},
			"flow":         {Type: "object", Description: "The complete replacement flow, including its id, in the shape get_flow returns. Call describe_usage for the schema."},
		}, "collectionId", "flow"),
		Handler: toolUpdateFlow,
	},
}

// lookupTool finds a registry entry by exact name. Tool names are identifiers,
// not prose, so the match is case-sensitive.
func lookupTool(name string) (toolEntry, bool) {
	for _, entry := range toolRegistry {
		if entry.Name == name {
			return entry, true
		}
	}
	return toolEntry{}, false
}

// toolsListResult is the tools/list payload. No nextCursor: the registry is
// short and fixed, so there is nothing to page through.
func toolsListResult() any {
	return struct {
		Tools []toolEntry `json:"tools"`
	}{Tools: toolRegistry}
}

// The handlers. Each one is a thin adapter: validate has already run, so they
// read arguments, call the Backend, and hand back the DTOs the contract in
// backend.go defines. List results are returned as bare arrays rather than
// wrapped in an object — there is no pagination state to carry, and the extra
// envelope would only cost the agent tokens.

func toolListCollections(backend Backend, _ toolArgs) (any, error) {
	collections, err := backend.ListCollections()
	if err != nil {
		return nil, err
	}
	return nonNil(collections), nil
}

func toolListRequests(backend Backend, args toolArgs) (any, error) {
	requests, err := backend.ListRequests(argString(args, "collectionId"))
	if err != nil {
		return nil, err
	}
	return nonNil(requests), nil
}

func toolSearchRequests(backend Backend, args toolArgs) (any, error) {
	requests, err := backend.SearchRequests(argString(args, "query"), argInt(args, "limit"))
	if err != nil {
		return nil, err
	}
	return nonNil(requests), nil
}

func toolGetRequest(backend Backend, args toolArgs) (any, error) {
	return backend.GetRequest(argString(args, "collectionId"), argString(args, "requestId"))
}

func toolInspectRequest(backend Backend, args toolArgs) (any, error) {
	return backend.InspectRequest(
		argString(args, "collectionId"),
		argString(args, "requestId"),
		argString(args, "environmentId"),
	)
}

func toolListEnvironments(backend Backend, _ toolArgs) (any, error) {
	environments, err := backend.ListEnvironments()
	if err != nil {
		return nil, err
	}
	return nonNil(environments), nil
}

func toolGetHistory(backend Backend, args toolArgs) (any, error) {
	runs, err := backend.GetHistory(argString(args, "collectionId"), argString(args, "requestId"), argInt(args, "limit"))
	if err != nil {
		return nil, err
	}
	return nonNil(runs), nil
}

// toolRunRequest executes one stored request.
//
// The context is rooted in context.Background() rather than the HTTP request's
// own, deliberately. A run is not a read: it reaches a real host and may create
// or change something there. If the context were the request's, an agent that
// disconnected — a client crash, a user hitting Ctrl-C, a proxy timing out —
// would cancel a POST that had already been sent, and the app would never
// record what came back. A run that has started should finish and land in
// history, so cancellation is bounded by RunTimeout alone. The handler goroutine
// therefore outlives its client by at most that long, which is the trade we
// want; the Backend still honours cancellation, so Stop's shutdown grace and
// this deadline both remain effective.
func toolRunRequest(backend Backend, args toolArgs) (any, error) {
	params := RunRequestParams{
		CollectionID:  argString(args, "collectionId"),
		RequestID:     argString(args, "requestId"),
		EnvironmentID: argString(args, "environmentId"),
		Variables:     argStringMap(args, "variables"),
	}
	ctx, cancel := context.WithTimeout(context.Background(), RunTimeout)
	defer cancel()
	return backend.RunRequest(ctx, params)
}

func toolListFlows(backend Backend, args toolArgs) (any, error) {
	flows, err := backend.ListFlows(argString(args, "collectionId"))
	if err != nil {
		return nil, err
	}
	return nonNil(flows), nil
}

func toolGetFlow(backend Backend, args toolArgs) (any, error) {
	return backend.GetFlow(argString(args, "collectionId"), argString(args, "flowId"))
}

// toolRunFlow executes one stored flow.
//
// The context is rooted in context.Background() rather than the HTTP request's
// own, for exactly the reason toolRunRequest sets out — and more so here. A flow
// is several real requests against real hosts, and a client that disconnects
// after step 2 has already created whatever step 2 created; cancelling the chain
// mid-way would leave the user's system in the half-finished state the flow
// exists to avoid, with nothing recorded about how it got there. A flow that has
// started should finish and land in history, so cancellation is bounded by
// FlowRunTimeout alone.
func toolRunFlow(backend Backend, args toolArgs) (any, error) {
	params := RunFlowParams{
		CollectionID:  argString(args, "collectionId"),
		FlowID:        argString(args, "flowId"),
		EnvironmentID: argString(args, "environmentId"),
		Inputs:        argStringMap(args, "inputs"),
	}
	ctx, cancel := context.WithTimeout(context.Background(), FlowRunTimeout)
	defer cancel()
	return backend.RunFlow(ctx, params)
}

// validate checks one call's arguments against the declared schema.
//
// Unknown keys pass: clients attach their own metadata, and a mistyped
// argument name already surfaces as the required field it failed to supply,
// which is the more useful of the two messages. Every failure names the
// argument and states the fix, because the agent reading it is about to
// compose the retry.
func (schema inputSchema) validate(args toolArgs) error {
	for _, name := range schema.Required {
		value, present := args[name]
		if !present || value == nil {
			return fmt.Errorf("missing required argument %q: %s", name, schema.fixHint(name))
		}
		if text, isString := value.(string); isString && strings.TrimSpace(text) == "" {
			return fmt.Errorf("required argument %q was empty: %s", name, schema.fixHint(name))
		}
	}
	for name, value := range args {
		property, known := schema.Properties[name]
		if !known || value == nil {
			continue
		}
		if err := checkArgType(name, property, value); err != nil {
			return err
		}
	}
	return nil
}

// fixHint turns a property's description into the "and here is what to pass"
// half of an error message.
func (schema inputSchema) fixHint(name string) string {
	if property, known := schema.Properties[name]; known && property.Description != "" {
		return "pass " + name + " — " + property.Description
	}
	return "pass " + name + " in the arguments object and call the tool again"
}

// checkArgType enforces the declared JSON type. Numbers arrive from
// encoding/json as float64, so an integer property accepts a float only when
// it has no fractional part; a plain int is accepted too, for callers that
// build arguments in Go rather than decoding them.
func checkArgType(name string, property schemaProperty, value any) error {
	switch property.Type {
	case "string":
		if _, ok := value.(string); !ok {
			return fmt.Errorf("argument %q must be a string, got %s: quote the value and call the tool again", name, jsonTypeName(value))
		}
	case "integer":
		switch number := value.(type) {
		case int:
			return nil
		case float64:
			if number != float64(int(number)) {
				return fmt.Errorf("argument %q must be a whole number, got %v: round it and call the tool again", name, number)
			}
		default:
			return fmt.Errorf("argument %q must be a number, got %s: pass it unquoted, e.g. 20", name, jsonTypeName(value))
		}
	case "boolean":
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("argument %q must be true or false, got %s", name, jsonTypeName(value))
		}
	case "object":
		fields, ok := value.(map[string]any)
		if !ok {
			return fmt.Errorf("argument %q must be an object, got %s: pass it as {\"name\":\"value\"} and call the tool again", name, jsonTypeName(value))
		}
		return checkObjectValues(name, property, fields)
	case "array":
		elements, ok := value.([]any)
		if !ok {
			return fmt.Errorf("argument %q must be an array, got %s: pass it as [{\"name\":\"…\",\"value\":\"…\"}] and call the tool again", name, jsonTypeName(value))
		}
		return checkArrayElements(name, property, elements)
	}
	return nil
}

// checkArrayElements enforces an array property's declared element type. Only
// the element's TYPE is checked here; the fields of a row object are checked
// where the row is decoded, because that is the only place an error can name
// which row and which field went wrong.
func checkArrayElements(name string, property schemaProperty, elements []any) error {
	if property.Items == nil || property.Items.Type == "" {
		return nil
	}
	for index, element := range elements {
		switch property.Items.Type {
		case "object":
			if _, ok := element.(map[string]any); !ok {
				return fmt.Errorf("argument %q entry %d must be an object, got %s: each entry is {\"name\":\"…\",\"value\":\"…\"}", name, index+1, jsonTypeName(element))
			}
		case "string":
			if _, ok := element.(string); !ok {
				return fmt.Errorf("argument %q entry %d must be a string, got %s: quote it and call the tool again", name, index+1, jsonTypeName(element))
			}
		}
	}
	return nil
}

// checkObjectValues enforces an object property's declared value type. Only
// string values are describable, which is all run_request's variables and
// run_flow's inputs need: both are substituted into a request as text, so a
// bare number or boolean is a mistake the agent should fix rather than
// something to coerce silently. The message names the offending key, since an object of a dozen
// variables gives an agent nothing to act on otherwise. Keys are checked in
// sorted order so the same bad call always names the same key — an agent that
// fixes one and retries must not be handed a different complaint each time.
func checkObjectValues(name string, property schemaProperty, fields map[string]any) error {
	if property.AdditionalProperties == nil || property.AdditionalProperties.Type != "string" {
		return nil
	}
	for _, key := range sortedKeys(fields) {
		if _, isString := fields[key].(string); !isString {
			return fmt.Errorf("argument %q must be an object of string values, but %q is %s: quote it, e.g. %q: {%q: \"…\"}, and call the tool again",
				name, key, jsonTypeName(fields[key]), name, key)
		}
	}
	return nil
}

// sortedKeys returns a map's keys in a stable order.
func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// jsonTypeName names what actually arrived, in the vocabulary of the schema
// the agent was reading, not Go's.
func jsonTypeName(value any) string {
	switch value.(type) {
	case string:
		return "a string"
	case float64, int:
		return "a number"
	case bool:
		return "a boolean"
	case []any:
		return "an array"
	case map[string]any:
		return "an object"
	case nil:
		return "null"
	default:
		return "an unsupported value"
	}
}

// nonNil turns a nil slice into an empty one so a result marshals as [] rather
// than null. An agent can iterate [] without a special case; null makes it
// guess whether the call half-failed.
func nonNil[T any](items []T) []T {
	if items == nil {
		return []T{}
	}
	return items
}

// argString reads a validated string argument. Surrounding whitespace is
// dropped: an id padded by a client is the same id.
func argString(args toolArgs, name string) string {
	text, _ := args[name].(string)
	return strings.TrimSpace(text)
}

// argStringMap reads a validated object-of-strings argument. A missing or empty
// one yields nil, which the Backend contract reads as "no overrides".
//
// Non-string values cannot reach here — validate rejects the whole call before
// the handler runs (checkObjectValues). They are skipped rather than coerced
// anyway: if that guard ever regressed, dropping the value is the failure that
// makes the Backend complain about a variable it cannot resolve, while
// stringifying it would quietly send fmt's rendering of a JSON object to a real
// host. Names keep their surrounding whitespace: a variable name is matched
// exactly, and trimming one here would map two different names onto one.
func argStringMap(args toolArgs, name string) map[string]string {
	fields, ok := args[name].(map[string]any)
	if !ok || len(fields) == 0 {
		return nil
	}
	overrides := make(map[string]string, len(fields))
	for key, value := range fields {
		if text, isString := value.(string); isString {
			overrides[key] = text
		}
	}
	if len(overrides) == 0 {
		return nil
	}
	return overrides
}

// argInt reads a validated integer argument. A missing one yields 0, which the
// Backend contract reads as "apply your own default".
func argInt(args toolArgs, name string) int {
	switch number := args[name].(type) {
	case int:
		return number
	case float64:
		return int(number)
	default:
		return 0
	}
}
