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
		Description: "Lists every collection currently open in LiteAPI, each with its id, name, and request count. " +
			"Start here: the collectionId values it returns are exactly what list_requests, get_request, and get_history expect. " +
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
		Name: "list_environments",
		Description: "Lists the global and per-collection environments with their variable names, each variable's secret flag and enabled flag, " +
			"and which environment is active. Non-secret values come back as authored; the value of a secret variable is ALWAYS empty — you can " +
			"refer to a secret by name inside a {{template}}, but you can never read it, and nothing you call will reveal it. Use this to work out " +
			"which variables the {{templates}} in a request refer to. Takes no arguments.",
		InputSchema: objectSchema(nil),
		Handler:     toolListEnvironments,
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
			"Chaining several requests together is not this tool's job: run_flow arrives in a later tier.",
		InputSchema: objectSchema(map[string]schemaProperty{
			"collectionId":  {Type: "string", Description: "Id of the collection holding the request, from list_collections."},
			"requestId":     {Type: "string", Description: "Id of the request to run, from list_requests or search_requests."},
			"environmentId": {Type: "string", Description: "Id of the environment to resolve variables against, from list_environments. Omit it to use whichever environment is active in the app."},
			"variables": stringValuedObject("Non-secret variable overrides for this run only, as an object of string values, e.g. {\"storeId\":\"str_42\"}. " +
				"Numbers and booleans must be quoted. Overriding a secret variable is refused."),
		}, "collectionId", "requestId"),
		Handler: toolRunRequest,
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
	}
	return nil
}

// checkObjectValues enforces an object property's declared value type. Only
// string values are describable, which is all run_request's variables need: a
// variable override is substituted into a request as text, so a bare number or
// boolean is a mistake the agent should fix rather than something to coerce
// silently. The message names the offending key, since an object of a dozen
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
