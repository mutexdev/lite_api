package main

// US-053 — Postman export fidelity.
//
// The criterion that does the work is IDEMPOTENCE of import -> export -> import,
// and it is worth being precise about why that is the right property to demand
// rather than "export equals input".
//
// A collection model here is richer than Postman's in places (three script
// slots against Postman's two events) and poorer in others. A single
// export -> import cycle can therefore legitimately lose something. What must
// never happen is DRIFT: each cycle losing a little more, so a collection
// shared back and forth degrades. Idempotence from the first import onward is
// exactly the guarantee that rules that out, and it is what these tests pin.
//
// The individual field tests exist because idempotence alone is satisfied by an
// exporter that drops everything: an empty collection round-trips perfectly.

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mutexdev/lite_api/internal/importers"
	"github.com/mutexdev/lite_api/internal/scripting"
)

const fidelityPostmanCollection = `{
  "info": {"name": "fidelity", "schema": "https://schema.getpostman.com/json/collection/v2.1.0/collection.json"},
  "auth": {"type": "bearer", "bearer": [{"key": "token", "value": "{{collectionToken}}", "type": "string"}]},
  "variable": [
    {"key": "baseUrl", "value": "https://api.example.test"},
    {"key": "apiVersion", "value": "v3"}
  ],
  "event": [
    {"listen": "prerequest", "script": {"type": "text/javascript", "exec": ["pm.environment.set('collectionPre', '1');"]}},
    {"listen": "test", "script": {"type": "text/javascript", "exec": ["pm.test('collection level', function () {});"]}}
  ],
  "item": [
    {
      "name": "Reports",
      "auth": {"type": "basic", "basic": [{"key": "username", "value": "folderUser", "type": "string"}, {"key": "password", "value": "folderPass", "type": "string"}]},
      "event": [
        {"listen": "prerequest", "script": {"type": "text/javascript", "exec": ["pm.environment.set('folderPre', '1');"]}}
      ],
      "item": [
        {
          "name": "Get report",
          "event": [
            {"listen": "prerequest", "script": {"type": "text/javascript", "exec": ["pm.environment.set('token', 'abc');", "console.log('two lines');"]}},
            {"listen": "test", "script": {"type": "text/javascript", "exec": ["pm.test('status ok', function () {", "  pm.response.to.have.status(200);", "});"]}}
          ],
          "request": {
            "method": "GET",
            "description": "Fetch a single report by id",
            "url": {
              "raw": "{{baseUrl}}/reports/:id?verbose=true&draft=false",
              "query": [
                {"key": "verbose", "value": "true"},
                {"key": "draft", "value": "false", "disabled": true}
              ],
              "variable": [{"key": "id", "value": "42"}]
            },
            "header": [{"key": "Accept", "value": "application/json"}],
            "auth": {"type": "apikey", "apikey": [{"key": "key", "value": "X-Key", "type": "string"}, {"key": "value", "value": "secret", "type": "string"}, {"key": "in", "value": "header", "type": "string"}]}
          }
        }
      ]
    },
    {
      "name": "Create report",
      "event": [
        {"listen": "test", "script": {"type": "text/javascript", "exec": ["pm.test('created', function () {});"]}}
      ],
      "request": {
        "method": "POST",
        "url": {"raw": "{{baseUrl}}/reports"},
        "header": [{"key": "Content-Type", "value": "application/json"}],
        "body": {"mode": "raw", "raw": "{\"title\":\"Q3\"}", "options": {"raw": {"language": "json"}}}
      }
    },
    {
      "name": "Search graph",
      "request": {
        "method": "POST",
        "url": {"raw": "{{baseUrl}}/graphql"},
        "body": {"mode": "graphql", "graphql": {"query": "query Reports { reports { id } }", "variables": "{\"limit\":5}"}}
      }
    }
  ]
}`

func importFidelityCollection(t *testing.T, content string) Collection {
	t.Helper()
	collection, err := importers.ImportPostman(content, "fidelity", false)
	if err != nil {
		t.Fatalf("importPostman: %v", err)
	}
	return collection
}

func exportFidelityCollection(t *testing.T, collection Collection) string {
	t.Helper()
	content, _, _, err := buildPostmanCollectionExport(collection)
	if err != nil {
		t.Fatalf("buildPostmanCollectionExport: %v", err)
	}
	return content
}

// fidelityFingerprint reduces a collection to the parts that describe what a
// request DOES, ignoring generated ids and timestamps, which differ on every
// import by design and would make any comparison vacuously fail.
func fidelityFingerprint(collection Collection) string {
	var builder strings.Builder

	builder.WriteString("collection " + collection.Name + "\n")
	builder.WriteString("auth " + collection.Auth.Mode + " " + collection.Auth.Token + collection.Auth.Username + collection.Auth.APIKey + "\n")
	builder.WriteString("pre " + strings.TrimSpace(collection.PreScript) + "\n")
	builder.WriteString("post " + strings.TrimSpace(collection.PostScript) + "\n")
	for _, variable := range collection.Variables {
		builder.WriteString("var " + variable.Name + "=" + scripting.ScriptVariableString(variable.Value) + "\n")
	}
	for _, folder := range collection.Folders {
		builder.WriteString("folder " + folder.Name + " auth=" + folder.Auth.Mode +
			" pre=" + strings.TrimSpace(folder.PreScript) +
			" post=" + strings.TrimSpace(folder.PostScript) + "\n")
	}
	for _, item := range collection.Items {
		builder.WriteString("item " + item.Name + " " + item.Type + " " + item.Method + " " + item.URL + "\n")
		builder.WriteString("  docs " + strings.TrimSpace(item.Docs) + "\n")
		builder.WriteString("  auth " + item.Auth.Mode + " " + item.Auth.Token + item.Auth.Username + item.Auth.APIKey + item.Auth.APIValue + "\n")
		builder.WriteString("  pre " + strings.TrimSpace(item.PreScript) + "\n")
		builder.WriteString("  post " + strings.TrimSpace(item.PostScript) + "\n")
		builder.WriteString("  tests " + strings.TrimSpace(item.Tests) + "\n")
		builder.WriteString("  body " + item.Body.Mode + " " + item.Body.Text + item.Body.JSON + item.Body.GraphQLQuery + item.Body.GraphQLVariables + "\n")
		for _, header := range item.Headers {
			builder.WriteString("  header " + header.Name + "=" + header.Value + "\n")
		}
		for _, param := range item.Params {
			builder.WriteString("  param " + param.Name + "=" + param.Value + " enabled=" + boolText(param.Enabled) + "\n")
		}
		for _, param := range item.PathParams {
			builder.WriteString("  pathParam " + param.Name + "=" + param.Value + "\n")
		}
	}
	return builder.String()
}

func boolText(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

// TestPostmanRoundTripIsIdempotent is the story's criterion. Import, export,
// import again — the two collections must describe the same requests.
//
// Note this compares the two IMPORTED collections, not the two exported files.
// Comparing files would fail on generated ids alone and prove nothing about
// behaviour.
func TestPostmanRoundTripIsIdempotent(t *testing.T) {
	first := importFidelityCollection(t, fidelityPostmanCollection)
	exported := exportFidelityCollection(t, first)
	second := importFidelityCollection(t, exported)

	if got, want := fidelityFingerprint(second), fidelityFingerprint(first); got != want {
		t.Errorf("the round trip is not idempotent\n--- first import ---\n%s\n--- after export and re-import ---\n%s", want, got)
	}

	// And a third cycle, because a two-cycle test passes on an exporter that
	// loses something on every pass at a steady rate.
	third := importFidelityCollection(t, exportFidelityCollection(t, second))
	if got, want := fidelityFingerprint(third), fidelityFingerprint(second); got != want {
		t.Errorf("the collection drifts on repeated cycles\n--- cycle 2 ---\n%s\n--- cycle 3 ---\n%s", want, got)
	}
}

// TestPostmanExportCarriesEventBlocks. Idempotence alone is satisfied by an
// exporter that drops everything — an empty collection round-trips perfectly —
// so the content has to be asserted directly.
func TestPostmanExportCarriesEventBlocks(t *testing.T) {
	collection := importFidelityCollection(t, fidelityPostmanCollection)
	exported := exportFidelityCollection(t, collection)

	var document map[string]interface{}
	if err := json.Unmarshal([]byte(exported), &document); err != nil {
		t.Fatalf("export is not valid JSON: %v", err)
	}

	if _, ok := document["event"]; !ok {
		t.Error("the collection's own event block was dropped; its scripts would silently stop running")
	}
	if _, ok := document["auth"]; !ok {
		t.Error("the collection auth was dropped; every request inheriting it would start failing")
	}
	variables, _ := document["variable"].([]interface{})
	if len(variables) != 2 {
		t.Errorf("got %d collection variables, want 2 — {{baseUrl}} would resolve to nothing", len(variables))
	}

	if !strings.Contains(exported, "status ok") {
		t.Error("a request's test script did not reach the export")
	}
	if !strings.Contains(exported, "folderPre") {
		t.Error("the folder's pre-request script did not reach the export")
	}
	if !strings.Contains(exported, "collectionPre") {
		t.Error("the collection's pre-request script did not reach the export")
	}
}

// TestPostmanExportWritesExecAsLines. A single joined string is accepted by
// most readers but diffs as one enormous line, which makes an exported
// collection unreviewable in version control.
func TestPostmanExportWritesExecAsLines(t *testing.T) {
	collection := importFidelityCollection(t, fidelityPostmanCollection)
	exported := exportFidelityCollection(t, collection)

	var document map[string]interface{}
	if err := json.Unmarshal([]byte(exported), &document); err != nil {
		t.Fatalf("export is not valid JSON: %v", err)
	}

	items, _ := document["item"].([]interface{})
	var found bool
	var walk func(entries []interface{})
	walk = func(entries []interface{}) {
		for _, value := range entries {
			entry, ok := value.(map[string]interface{})
			if !ok {
				continue
			}
			if children, ok := entry["item"].([]interface{}); ok {
				walk(children)
			}
			events, ok := entry["event"].([]interface{})
			if !ok {
				continue
			}
			for _, eventValue := range events {
				event, ok := eventValue.(map[string]interface{})
				if !ok {
					continue
				}
				script, ok := event["script"].(map[string]interface{})
				if !ok {
					continue
				}
				exec, ok := script["exec"].([]interface{})
				if !ok {
					t.Errorf("exec is not a line array: %#v", script["exec"])
					continue
				}
				if len(exec) > 1 {
					found = true
				}
			}
		}
	}
	walk(items)

	if !found {
		t.Error("no multi-line exec array was produced; scripts are being written as one joined line")
	}
}

// TestPostmanExportCarriesPathParams. A path param exported as nothing means a
// :id placeholder comes back with no value — the request imports looking
// complete and sends a literal ":id" to the server.
func TestPostmanExportCarriesPathParams(t *testing.T) {
	first := importFidelityCollection(t, fidelityPostmanCollection)
	second := importFidelityCollection(t, exportFidelityCollection(t, first))

	var target *RequestItem
	for i := range second.Items {
		if second.Items[i].Name == "Get report" {
			target = &second.Items[i]
		}
	}
	if target == nil {
		t.Fatal("the request was not re-imported")
	}
	if len(target.PathParams) != 1 {
		t.Fatalf("got %d path params, want 1", len(target.PathParams))
	}
	if target.PathParams[0].Name != "id" || target.PathParams[0].Value != "42" {
		t.Errorf("path param round-tripped as %s=%s, want id=42", target.PathParams[0].Name, target.PathParams[0].Value)
	}
}

// TestPostmanExportPreservesDisabledParams. A disabled query param that comes
// back enabled changes what the request sends, silently.
func TestPostmanExportPreservesDisabledParams(t *testing.T) {
	first := importFidelityCollection(t, fidelityPostmanCollection)
	second := importFidelityCollection(t, exportFidelityCollection(t, first))

	for _, item := range second.Items {
		if item.Name != "Get report" {
			continue
		}
		for _, param := range item.Params {
			if param.Name == "draft" && param.Enabled {
				t.Error("a disabled param came back enabled; the request now sends a value it did not before")
			}
		}
	}
}

// TestPostmanExportRoundTripsGraphQL. GraphQL is named in the story alongside
// HTTP, and its body lives in a different Postman shape entirely.
func TestPostmanExportRoundTripsGraphQL(t *testing.T) {
	first := importFidelityCollection(t, fidelityPostmanCollection)
	second := importFidelityCollection(t, exportFidelityCollection(t, first))

	var target *RequestItem
	for i := range second.Items {
		if second.Items[i].Name == "Search graph" {
			target = &second.Items[i]
		}
	}
	if target == nil {
		t.Fatal("the GraphQL request was not re-imported")
	}
	if target.Type != "graphql" {
		t.Errorf("type = %q, want graphql", target.Type)
	}
	if !strings.Contains(target.Body.GraphQLQuery, "query Reports") {
		t.Errorf("the query was lost: %q", target.Body.GraphQLQuery)
	}
	if !strings.Contains(target.Body.GraphQLVariables, "limit") {
		t.Errorf("the variables were lost: %q", target.Body.GraphQLVariables)
	}
}

// TestPostmanExportRoundTripsAuthAtEveryLevel. Collection, folder and request
// auth are three separate places, and a level that silently reverts to inherit
// produces requests that authenticate as something else.
func TestPostmanExportRoundTripsAuthAtEveryLevel(t *testing.T) {
	first := importFidelityCollection(t, fidelityPostmanCollection)
	second := importFidelityCollection(t, exportFidelityCollection(t, first))

	if second.Auth.Mode != "bearer" || second.Auth.Token != "{{collectionToken}}" {
		t.Errorf("collection auth = %+v, want bearer with the token", second.Auth)
	}

	if len(second.Folders) != 1 {
		t.Fatalf("got %d folders, want 1", len(second.Folders))
	}
	if second.Folders[0].Auth.Mode != "basic" || second.Folders[0].Auth.Username != "folderUser" {
		t.Errorf("folder auth = %+v, want basic folderUser", second.Folders[0].Auth)
	}

	for _, item := range second.Items {
		if item.Name != "Get report" {
			continue
		}
		if item.Auth.Mode != "apikey" || item.Auth.APIKey != "X-Key" || item.Auth.APIValue != "secret" {
			t.Errorf("request auth = %+v, want the apikey config", item.Auth)
		}
	}
}

// TestPostmanExportRoundTripsDescription. The exporter previously wrote no
// description and the importer read none, so a documented request lost its
// documentation on every export.
func TestPostmanExportRoundTripsDescription(t *testing.T) {
	first := importFidelityCollection(t, fidelityPostmanCollection)
	if docsFor(first, "Get report") == "" {
		t.Fatal("the description was not imported in the first place")
	}
	second := importFidelityCollection(t, exportFidelityCollection(t, first))
	if got := docsFor(second, "Get report"); got != "Fetch a single report by id" {
		t.Errorf("description round-tripped as %q", got)
	}
}

func docsFor(collection Collection, name string) string {
	for _, item := range collection.Items {
		if item.Name == name {
			return strings.TrimSpace(item.Docs)
		}
	}
	return ""
}
