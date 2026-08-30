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
// are six shallow objects of strings and integers, and a JSON Schema library
// would be a dependency whose error messages we do not control. The messages
// matter — an agent recovers from "missing required argument collectionId"
// and stalls on "does not match schema".
package mcpserver

import (
	"fmt"
	"strings"
)

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
			"collection when you roughly know what you want, e.g. \"checkout\" or \"orders\". URLs keep their unresolved {{templates}}. Follow up " +
			"with get_request using the id and collectionId of the best match.",
		InputSchema: objectSchema(map[string]schemaProperty{
			"query": {Type: "string", Description: "Case-insensitive substring to match against request name, method, URL, and folder path."},
			"limit": limitProperty,
		}, "query"),
		Handler: toolSearchRequests,
	},
	{
		Name: "get_request",
		Description: "Returns one request's full definition: method, URL, headers, query and path params, body, auth mode, pre/post scripts, " +
			"and transport settings. Everything is as authored, so {{templates}} are unresolved, and everything is redacted: credential-shaped " +
			"values and auth credentials arrive as \"<masked>\" and no resolved secret value can ever appear here or in any error. Read this to " +
			"learn the shape of a call; to actually execute one, a later tier adds run_request, which runs the stored request inside LiteAPI " +
			"where secrets resolve without crossing this boundary. Call get_history to see what its responses have looked like.",
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
		if err := checkArgType(name, property.Type, value); err != nil {
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
func checkArgType(name, declared string, value any) error {
	switch declared {
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
	}
	return nil
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
