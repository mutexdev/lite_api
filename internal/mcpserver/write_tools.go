// The write tier's handlers and the decoding that stands in front of them.
//
// WHY THE DECODING IS HERE RATHER THAN IN THE SCHEMA. inputSchema checks that
// an argument is an array of objects; it cannot check that every object has a
// non-empty name, and it must not, because the message that matters is "row 3
// of headers has no name" and a generic schema walker cannot write that
// sentence. So the shallow shape is the schema's job and the contents are this
// file's, and both fail the same way: naming the field and the fix.
//
// WHAT THIS FILE DOES NOT DO IS ENFORCE THE TIER. Whether the user has unlocked
// writing is read inside the Backend, per call, at the moment of the write. A
// check here would be a check in the wrong process boundary — the preference
// lives in the app's state, it can change while an agent is mid-task, and a
// tool that decided for itself would be a second answer to a question the app
// already answers.
package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// WriteTimeout bounds one authoring call.
//
// Generously longer than the work, because the work is not the slow part: a
// create or update that points a secret at a new host BLOCKS on a prompt in the
// app's UI, and the user has to read it and decide. That prompt has its own
// (shorter) deadline inside the app, so this is the outer bound that guarantees
// the handler goroutine ends even if a deeper wait misbehaves.
const WriteTimeout = 180 * time.Second

// writeContext is the context every write tool runs under.
//
// Rooted in context.Background() rather than the HTTP request's, for the reason
// toolRunRequest sets out and one more of its own: a write reaches the user's
// disk, and a client that disconnects while the file is being written must not
// leave a half-authored request behind. The call finishes and reports.
func writeContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), WriteTimeout)
}

func toolCreateRequest(backend Backend, args toolArgs) (any, error) {
	params := CreateRequestParams{
		CollectionID:     argString(args, "collectionId"),
		Name:             argString(args, "name"),
		FolderPath:       argString(args, "folderPath"),
		Type:             argString(args, "type"),
		Method:           argString(args, "method"),
		URL:              argString(args, "url"),
		BodyType:         argString(args, "bodyType"),
		Body:             argRawString(args, "body"),
		GraphQLVariables: argRawString(args, "graphqlVariables"),
		Auth:             argStringMap(args, "auth"),
		PreScript:        argRawString(args, "preScript"),
		PostScript:       argRawString(args, "postScript"),
		Tests:            argRawString(args, "tests"),
	}
	var err error
	if params.Headers, err = argRows(args, "headers"); err != nil {
		return nil, err
	}
	if params.Params, err = argRows(args, "params"); err != nil {
		return nil, err
	}
	if params.PathParams, err = argRows(args, "pathParams"); err != nil {
		return nil, err
	}
	if params.Vars, err = argRows(args, "vars"); err != nil {
		return nil, err
	}
	if params.FormData, err = argRows(args, "formData"); err != nil {
		return nil, err
	}
	ctx, cancel := writeContext()
	defer cancel()
	return backend.CreateRequest(ctx, params)
}

func toolUpdateRequest(backend Backend, args toolArgs) (any, error) {
	params := UpdateRequestParams{
		CollectionID:     argString(args, "collectionId"),
		RequestID:        argString(args, "requestId"),
		Method:           argStringPtr(args, "method"),
		URL:              argStringPtr(args, "url"),
		BodyType:         argStringPtr(args, "bodyType"),
		Body:             argStringPtr(args, "body"),
		GraphQLVariables: argStringPtr(args, "graphqlVariables"),
		Auth:             argStringMap(args, "auth"),
		PreScript:        argStringPtr(args, "preScript"),
		PostScript:       argStringPtr(args, "postScript"),
		Tests:            argStringPtr(args, "tests"),
	}
	var err error
	if params.Headers, err = argRowsPtr(args, "headers"); err != nil {
		return nil, err
	}
	if params.Params, err = argRowsPtr(args, "params"); err != nil {
		return nil, err
	}
	if params.PathParams, err = argRowsPtr(args, "pathParams"); err != nil {
		return nil, err
	}
	if params.Vars, err = argRowsPtr(args, "vars"); err != nil {
		return nil, err
	}
	if params.FormData, err = argRowsPtr(args, "formData"); err != nil {
		return nil, err
	}
	ctx, cancel := writeContext()
	defer cancel()
	return backend.UpdateRequest(ctx, params)
}

func toolCreateFlow(backend Backend, args toolArgs) (any, error) {
	flow, err := argFlow(args)
	if err != nil {
		return nil, err
	}
	return backend.CreateFlow(CreateFlowParams{CollectionID: argString(args, "collectionId"), Flow: flow})
}

func toolUpdateFlow(backend Backend, args toolArgs) (any, error) {
	flow, err := argFlow(args)
	if err != nil {
		return nil, err
	}
	return backend.UpdateFlow(UpdateFlowParams{CollectionID: argString(args, "collectionId"), Flow: flow})
}

// argFlow decodes the flow object.
//
// UNKNOWN FIELDS ARE REJECTED, which is the opposite of validate's rule for
// top-level arguments and is right for exactly the opposite reason. A client
// attaching metadata to a CALL is normal; a misspelled key inside a flow
// definition ("asserts" for "assert", "request" for "requestId") is an agent
// authoring something that silently does less than it thinks — a step with no
// assertions reports green. The strict decode turns that into a message naming
// the field.
func argFlow(args toolArgs) (FlowDefinition, error) {
	raw, present := args["flow"]
	if !present || raw == nil {
		return FlowDefinition{}, fmt.Errorf("missing required argument %q: pass the whole flow as an object, e.g. {\"name\":\"…\",\"steps\":[…]}; call describe_usage for the schema", "flow")
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return FlowDefinition{}, fmt.Errorf("argument \"flow\" could not be read as JSON: %v", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	var flow FlowDefinition
	if err := decoder.Decode(&flow); err != nil {
		return FlowDefinition{}, fmt.Errorf("argument \"flow\" does not match the flow schema: %v; call describe_usage for the fields and a worked example", err)
	}
	return flow, nil
}

// argRawString reads an optional string argument WITHOUT trimming.
//
// Scripts, bodies and test sources are compared byte-for-byte against what is
// stored (update_request preserves them), so trimming here would turn an
// unchanged echo into a difference and refuse a call that changed nothing.
func argRawString(args toolArgs, name string) string {
	text, _ := args[name].(string)
	return text
}

// argStringPtr distinguishes "not supplied" from "supplied as empty", which is
// the whole of a patch's semantics: a missing url keeps the stored one, and an
// empty url is a caller asking for an empty url (which the Backend then
// refuses, naming the field).
func argStringPtr(args toolArgs, name string) *string {
	value, present := args[name]
	if !present || value == nil {
		return nil
	}
	text, isString := value.(string)
	if !isString {
		// validate already rejected a non-string for a declared string
		// property; treating it as absent here is the safe residue rather than
		// a second error path.
		return nil
	}
	return &text
}

// argRows decodes one row array. A missing argument is no rows, not an error.
func argRows(args toolArgs, name string) ([]AuthoredRow, error) {
	value, present := args[name]
	if !present || value == nil {
		return nil, nil
	}
	elements, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("argument %q must be an array of rows, e.g. [{\"name\":\"Accept\",\"value\":\"application/json\"}]", name)
	}
	rows := make([]AuthoredRow, 0, len(elements))
	for index, element := range elements {
		row, err := decodeRow(name, index, element)
		if err != nil {
			return nil, err
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// argRowsPtr is argRows for a patch: a missing argument is nil (keep what is
// stored), and a supplied empty array is an empty list (clear them).
func argRowsPtr(args toolArgs, name string) (*[]AuthoredRow, error) {
	value, present := args[name]
	if !present || value == nil {
		return nil, nil
	}
	rows, err := argRows(args, name)
	if err != nil {
		return nil, err
	}
	if rows == nil {
		rows = []AuthoredRow{}
	}
	return &rows, nil
}

// rowFields is the exact set of keys a row may carry. Anything else is a
// mistake worth naming: "key" for "name" and "values" for "value" are the two
// an agent actually makes, and both would otherwise produce a row with an empty
// value that looks like it worked.
var rowFields = map[string]bool{"name": true, "value": true, "enabled": true, "secret": true}

func decodeRow(argument string, index int, element any) (AuthoredRow, error) {
	fields, ok := element.(map[string]any)
	if !ok {
		return AuthoredRow{}, fmt.Errorf("argument %q entry %d must be an object like {\"name\":\"Accept\",\"value\":\"application/json\"}, got %s", argument, index+1, jsonTypeName(element))
	}
	for _, key := range sortedKeys(fields) {
		if !rowFields[key] {
			return AuthoredRow{}, fmt.Errorf("argument %q entry %d has an unknown field %q; a row carries name, value, and optionally enabled", argument, index+1, key)
		}
	}
	row := AuthoredRow{}
	name, isString := fields["name"].(string)
	if !isString || name == "" {
		return AuthoredRow{}, fmt.Errorf("argument %q entry %d has no name; every row needs a name, e.g. {\"name\":\"Accept\",\"value\":\"application/json\"}", argument, index+1)
	}
	row.Name = name
	if raw, present := fields["value"]; present && raw != nil {
		text, valueIsString := raw.(string)
		if !valueIsString {
			return AuthoredRow{}, fmt.Errorf("argument %q entry %d (%q) has a value that is %s; quote it — every row value is text", argument, index+1, name, jsonTypeName(raw))
		}
		row.Value = text
	}
	if raw, present := fields["enabled"]; present && raw != nil {
		enabled, isBool := raw.(bool)
		if !isBool {
			return AuthoredRow{}, fmt.Errorf("argument %q entry %d (%q) has an enabled that is %s; pass true or false", argument, index+1, name, jsonTypeName(raw))
		}
		row.Enabled = &enabled
	}
	if raw, present := fields["secret"]; present && raw != nil {
		secret, isBool := raw.(bool)
		if !isBool {
			return AuthoredRow{}, fmt.Errorf("argument %q entry %d (%q) has a secret that is %s; pass true or false", argument, index+1, name, jsonTypeName(raw))
		}
		// Carried rather than dropped: the Backend refuses it by name, which
		// is the only way the agent learns the rule instead of quietly getting
		// a non-secret row.
		row.Secret = secret
	}
	return row, nil
}
