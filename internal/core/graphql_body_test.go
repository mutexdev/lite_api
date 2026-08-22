// GraphQL bodies must carry `variables` as a JSON object.
//
// The GraphQL spec says variables is a map, not a string. Encoding it as a
// string produces a request most servers reject, and -- worse, because it is
// silent -- a Network Log entry that disagrees with what actually went over the
// wire, so anyone debugging from the log is debugging a different request.
package core

import (
	"encoding/json"
	"testing"
)

const (
	graphQLTestQuery     = "query Foo($id: ID!) { foo(id: $id) { name } }"
	graphQLTestVariables = `{"id":"123"}`
)

// variablesAreAnObject reports whether the encoded payload holds `variables` as
// a nested object rather than a string containing escaped JSON.
func variablesAreAnObject(t *testing.T, encoded string) bool {
	t.Helper()
	var payload map[string]json.RawMessage
	if err := json.Unmarshal([]byte(encoded), &payload); err != nil {
		t.Fatalf("payload is not JSON: %v (%s)", err, encoded)
	}
	raw, ok := payload["variables"]
	if !ok {
		t.Fatalf("payload has no variables: %s", encoded)
	}
	var asString string
	return json.Unmarshal(raw, &asString) != nil
}

func TestNetworkLogGraphQLBodyKeepsVariablesAsAnObject(t *testing.T) {
	body := RequestBody{Mode: "graphql", GraphQLQuery: graphQLTestQuery, GraphQLVariables: graphQLTestVariables}
	logged := networkLogRequestBody(body, map[string]string{})
	if !variablesAreAnObject(t, logged) {
		t.Fatalf("the log recorded variables as a string: %s", logged)
	}
}

// The log exists to show what was sent. If it re-encodes the body itself, the
// two can drift, which is exactly how this bug survived.
func TestNetworkLogGraphQLBodyMatchesWhatIsSent(t *testing.T) {
	body := RequestBody{Mode: "graphql", GraphQLQuery: graphQLTestQuery, GraphQLVariables: graphQLTestVariables}
	logged := networkLogRequestBody(body, map[string]string{})
	sent := graphQLRequestPayload(body, map[string]string{})

	var loggedValue, sentValue any
	if err := json.Unmarshal([]byte(logged), &loggedValue); err != nil {
		t.Fatalf("logged body is not JSON: %v", err)
	}
	if err := json.Unmarshal([]byte(sent), &sentValue); err != nil {
		t.Fatalf("sent body is not JSON: %v", err)
	}
	loggedCanonical, _ := json.Marshal(loggedValue)
	sentCanonical, _ := json.Marshal(sentValue)
	if string(loggedCanonical) != string(sentCanonical) {
		t.Fatalf("log and wire disagree:\n log: %s\nwire: %s", loggedCanonical, sentCanonical)
	}
}

// Reachable by switching the body mode away from graphql on a graphql-typed
// request: buildBody returns nil and this fallback builds the payload instead.
func TestGraphQLFallbackPayloadKeepsVariablesAsAnObject(t *testing.T) {
	body := RequestBody{Mode: "none", GraphQLQuery: graphQLTestQuery, GraphQLVariables: graphQLTestVariables}
	payload := graphQLRequestPayload(body, map[string]string{})
	if !variablesAreAnObject(t, payload) {
		t.Fatalf("the fallback sent variables as a string: %s", payload)
	}
}

// Variables that are not valid JSON must still send something rather than
// failing the request: a half-typed variables block is an ordinary state for a
// request someone is still writing.
func TestGraphQLVariablesThatAreNotJSONStillSend(t *testing.T) {
	body := RequestBody{Mode: "graphql", GraphQLQuery: graphQLTestQuery, GraphQLVariables: `{"id": `}
	payload := graphQLRequestPayload(body, map[string]string{})
	var decoded map[string]any
	if err := json.Unmarshal([]byte(payload), &decoded); err != nil {
		t.Fatalf("malformed variables produced a malformed payload: %v (%s)", err, payload)
	}
	if decoded["query"] != graphQLTestQuery {
		t.Fatalf("the query was lost: %s", payload)
	}
}

func TestGraphQLVariablesInterpolateBeforeEncoding(t *testing.T) {
	body := RequestBody{Mode: "graphql", GraphQLQuery: graphQLTestQuery, GraphQLVariables: `{"id":"{{userId}}"}`}
	payload := graphQLRequestPayload(body, map[string]string{"userId": "42"})
	var decoded struct {
		Variables map[string]any `json:"variables"`
	}
	if err := json.Unmarshal([]byte(payload), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Variables["id"] != "42" {
		t.Fatalf("variables were not interpolated: %s", payload)
	}
}
