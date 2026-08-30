// Regression tests for the script engine defects found by audit.
//
// Every one of these describes something a user could see: a test that ran and
// left no row, a request that was never sent because an assertion failed, a body
// that changed on the wire because a script existed at all, a header named
// "[object Object]", a token fetch killed at two seconds, a `const` in one
// script that broke three others, and a missing global that ended the request.
//
// The existing suite was green through all of it, which is the reason each of
// these is written against the observable outcome rather than the mechanism.
package scripting

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mutexdev/lite_api/internal/types"
)

func regressionItem() *types.RequestItem {
	return &types.RequestItem{
		Name:   "probe",
		Type:   "http",
		Method: "GET",
		URL:    "http://example.test/thing",
	}
}

func passedNames(results []types.TestResult) string {
	rows := make([]string, 0, len(results))
	for _, result := range results {
		rows = append(rows, fmt.Sprintf("%s=%v(%s)", result.Name, result.Passed, result.Message))
	}
	return strings.Join(rows, ", ")
}

// FIX 1. Three pm.tests in a post-response script produced, before this, either
// nothing (they passed, and there was no registry to record into) or a single
// row (the first failure escaped as a script error). All three must be rows, in
// source order, and the script must keep running past the failure.
func TestPostResponseScriptRecordsEveryTestRow(t *testing.T) {
	script := `
pm.test("first passes", function () { pm.expect(1).to.equal(1) })
pm.test("second fails", function () { pm.expect(1).to.equal(2) })
pm.test("third still runs", function () { pm.expect(3).to.equal(3) })
bru.setVar("reachedTheEnd", "yes")
console.log("after the failing test")
`
	response := types.Response{Status: 200, Body: `{"ok":true}`, Headers: map[string]string{}}
	results := []types.TestResult{}
	logs := []types.ScriptLog{}
	vars := NewFlatScriptVariableContext(map[string]string{})
	meta := ScriptRuntimeMeta{Variables: vars, TimelinePhase: "post-response"}

	if _, err := RunPostResponseScriptSourceMeta(
		SingleScriptSource("post-response script", script),
		*regressionItem(), &response, vars.Combined, nil, meta, &results, &logs,
	); err != nil {
		t.Fatalf("a failing pm.test escaped as a script error: %v", err)
	}

	if len(results) != 3 {
		t.Fatalf("got %d test rows, want 3: %s", len(results), passedNames(results))
	}
	want := []struct {
		name   string
		passed bool
	}{{"first passes", true}, {"second fails", false}, {"third still runs", true}}
	for index, expected := range want {
		if results[index].Name != expected.name || results[index].Passed != expected.passed {
			t.Errorf("row %d = %q passed=%v, want %q passed=%v",
				index, results[index].Name, results[index].Passed, expected.name, expected.passed)
		}
	}
	if vars.Runtime["reachedTheEnd"] != "yes" {
		t.Error("execution stopped at the failing test: the bru.setVar after it never ran")
	}
	if len(logs) == 0 {
		t.Error("the console.log after the failing test never ran")
	}
}

// FIX 1, async half. An async pm.test used to be abandoned mid-flight when the
// phase had no registry, taking anything after its await with it.
func TestAsyncTestInPostResponseScriptIsAwaitedAndRecorded(t *testing.T) {
	script := `
pm.test("async passes", async function () {
  await bru.sleep(1)
  pm.expect(1).to.equal(1)
})
pm.test("async fails", async function () {
  await bru.sleep(1)
  pm.expect("a").to.equal("b")
})
`
	response := types.Response{Status: 200, Headers: map[string]string{}}
	results := []types.TestResult{}
	if _, err := RunPostResponseScriptSourceMeta(
		SingleScriptSource("post-response script", script),
		*regressionItem(), &response, map[string]string{}, nil, ScriptRuntimeMeta{}, &results,
	); err != nil {
		t.Fatalf("script failed: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("got %d rows, want 2: %s", len(results), passedNames(results))
	}
	for _, result := range results {
		if result.Message == "pending" {
			t.Fatalf("%q was never awaited: %s", result.Name, passedNames(results))
		}
	}
	if !results[0].Passed || results[1].Passed {
		t.Errorf("wrong outcomes: %s", passedNames(results))
	}
}

// FIX 2. A failing assertion in a PRE-request script must not stop the request.
// Before this it panicked out of pm.test, became a script error, and the send
// path replaced the whole request with a ScriptErrorResponse: the user's request
// was never made because one expectation was wrong.
func TestPreRequestTestFailureRecordsARowAndLetsTheRequestProceed(t *testing.T) {
	item := regressionItem()
	script := `
pm.test("this fails", function () { pm.expect(1).to.equal(2) })
req.setHeader("x-ran-after-the-failure", "yes")
`
	results := []types.TestResult{}
	state, err := RunPreRequestScriptSourceMeta(
		SingleScriptSource("request pre-request script", script),
		item, map[string]string{}, nil, ScriptRuntimeMeta{}, &results,
	)
	if err != nil {
		t.Fatalf("a failed assertion aborted the request: %v", err)
	}
	if len(results) != 1 || results[0].Passed {
		t.Fatalf("want one failed row, got: %s", passedNames(results))
	}
	if types.GetKeyValue(state.headers, "x-ran-after-the-failure") != "yes" {
		t.Error("the script stopped at the failing assertion")
	}
	if types.GetKeyValue(item.Headers, "x-ran-after-the-failure") != "yes" {
		t.Error("the header the script set after the failure did not reach the request")
	}
}

// The other half of FIX 2, and the one that must NOT change: an uncaught throw
// is still a script error, and the send path still refuses to send.
func TestPreRequestUncaughtThrowStillAbortsTheRequest(t *testing.T) {
	results := []types.TestResult{}
	_, err := RunPreRequestScriptSourceMeta(
		SingleScriptSource("request pre-request script", `throw new Error("the script is broken")`),
		regressionItem(), map[string]string{}, nil, ScriptRuntimeMeta{}, &results,
	)
	if err == nil {
		t.Fatal("an uncaught throw did not abort: a broken pre-request script would be sent anyway")
	}
	if !strings.Contains(err.Error(), "the script is broken") {
		t.Errorf("error = %q, want the thrown message", err.Error())
	}
}

// FIX 3. A no-op pre-request script must leave the request EXACTLY as the
// no-script path would have built it, in every body mode.
//
// req.body is seeded with a flat rendering of the body, and that rendering used
// to be written back unconditionally. formUrlEncoded became raw unencoded text;
// a bodyless GET acquired mode "text" and, with it, a Content-Type of
// text/plain. `bru.setVar("x", 1)` was enough to trigger it.
func TestNoOpPreRequestScriptLeavesTheRequestUnchanged(t *testing.T) {
	bodies := map[string]types.RequestBody{
		"none": {Mode: "none"},
		"raw json": {
			Mode: "json",
			JSON: `{"name":"value with spaces & symbols"}`,
		},
		"formUrlEncoded": {
			Mode: "formUrlEncoded",
			FormURLEncoded: []types.KeyValue{
				{Name: "field1", Value: "value with spaces & symbols", Enabled: true},
				{Name: "field2", Value: "b+c=d", Enabled: true},
			},
		},
		"multipart": {
			Mode: "multipartForm",
			Multipart: []types.FormPart{
				{Name: "part1", Value: "value with spaces & symbols", Enabled: true},
			},
		},
		"graphql": {
			Mode:             "graphql",
			GraphQLQuery:     "query { thing { id } }",
			GraphQLVariables: `{"id":1}`,
		},
		"text": {Mode: "text", Text: "plain & simple"},
		"xml":  {Mode: "xml", XML: "<a>b &amp; c</a>"},
	}
	scripts := map[string]string{
		"bru.setVar":  `bru.setVar("x", 1)`,
		"console.log": `console.log("hello")`,
		"empty-ish":   `;`,
		"reads body":  `const seen = req.getBody(); bru.setVar("seen", typeof seen)`,
	}

	for bodyName, body := range bodies {
		for scriptName, script := range scripts {
			t.Run(bodyName+"/"+scriptName, func(t *testing.T) {
				baseline := regressionItem()
				baseline.Body = body
				baseline.Headers = []types.KeyValue{{Name: "X-Fixed", Value: "1", Enabled: true}}

				scripted := regressionItem()
				scripted.Body = body
				scripted.Headers = []types.KeyValue{{Name: "X-Fixed", Value: "1", Enabled: true}}

				if _, err := RunPreRequestScriptSourceMeta(
					SingleScriptSource("request pre-request script", script),
					scripted, map[string]string{}, nil, ScriptRuntimeMeta{}, nil,
				); err != nil {
					t.Fatalf("script failed: %v", err)
				}

				if !reflect.DeepEqual(scripted.Body, baseline.Body) {
					t.Errorf("the body changed because a script existed:\n got  %#v\n want %#v", scripted.Body, baseline.Body)
				}
				if !reflect.DeepEqual(scripted.Headers, baseline.Headers) {
					t.Errorf("the headers changed because a script existed:\n got  %#v\n want %#v", scripted.Headers, baseline.Headers)
				}
				if scripted.Method != baseline.Method || scripted.URL != baseline.URL {
					t.Errorf("method/URL changed: %s %s", scripted.Method, scripted.URL)
				}
			})
		}
	}
}

// The counterpart: a script that DOES set a body must still be honoured, or the
// fix above would have been "ignore the script".
func TestPreRequestScriptCanStillReplaceTheBody(t *testing.T) {
	item := regressionItem()
	item.Body = types.RequestBody{Mode: "json", JSON: `{"a":1}`}
	if _, err := RunPreRequestScriptSourceMeta(
		SingleScriptSource("request pre-request script", `req.setBody({ b: 2 })`),
		item, map[string]string{}, nil, ScriptRuntimeMeta{}, nil,
	); err != nil {
		t.Fatalf("script failed: %v", err)
	}
	if item.Body.Mode != "json" || !strings.Contains(item.Body.JSON, `"b"`) {
		t.Fatalf("req.setBody was ignored: %#v", item.Body)
	}
}

// A formUrlEncoded body that the script rewrites must be ENCODED, not pasted in
// raw — the other half of the same defect.
func TestScriptedFormUrlEncodedBodyIsEncoded(t *testing.T) {
	item := regressionItem()
	item.Method = "POST"
	item.Headers = []types.KeyValue{{Name: "Content-Type", Value: "application/x-www-form-urlencoded", Enabled: true}}
	item.Body = types.RequestBody{
		Mode:           "formUrlEncoded",
		FormURLEncoded: []types.KeyValue{{Name: "field1", Value: "old", Enabled: true}},
	}
	if _, err := RunPreRequestScriptSourceMeta(
		SingleScriptSource("request pre-request script", `req.setBody({ field1: "value with spaces & symbols" })`),
		item, map[string]string{}, nil, ScriptRuntimeMeta{}, nil,
	); err != nil {
		t.Fatalf("script failed: %v", err)
	}
	if strings.Contains(item.Body.Text, " ") || strings.Contains(item.Body.Text, "&& ") {
		t.Fatalf("body was not form-encoded: %q", item.Body.Text)
	}
	if !strings.Contains(item.Body.Text, "field1=value") {
		t.Fatalf("body lost its field: %q", item.Body.Text)
	}
}

// FIX 4. pm.request.headers.add({key, value}) — Postman's own idiom — used to
// stringify the object into the header NAME and send "[object Object]".
func TestPostmanHeadersAddAcceptsTheObjectForm(t *testing.T) {
	for name, script := range map[string]string{
		"add object":     `pm.request.headers.add({ key: "Authorization", value: "Bearer t" })`,
		"upsert object":  `pm.request.headers.upsert({ key: "Authorization", value: "Bearer t" })`,
		"add name/value": `pm.request.headers.add({ name: "Authorization", value: "Bearer t" })`,
	} {
		t.Run(name, func(t *testing.T) {
			item := regressionItem()
			state, err := RunPreRequestScriptSourceMeta(
				SingleScriptSource("request pre-request script", script),
				item, map[string]string{}, nil, ScriptRuntimeMeta{}, nil,
			)
			if err != nil {
				t.Fatalf("script failed: %v", err)
			}
			if got := types.GetKeyValue(state.headers, "Authorization"); got != "Bearer t" {
				t.Fatalf("Authorization = %q, want %q (headers: %#v)", got, "Bearer t", state.headers)
			}
			for _, header := range item.Headers {
				if strings.Contains(header.Name, "object Object") {
					t.Fatalf("the object was stringified into the header name: %#v", item.Headers)
				}
			}
		})
	}
}

// The two-string form Postman also allows must keep working.
func TestPostmanHeadersAddStillAcceptsTheStringForm(t *testing.T) {
	state, err := RunPreRequestScriptSourceMeta(
		SingleScriptSource("request pre-request script", `pm.request.headers.add("X-Two", "strings")`),
		regressionItem(), map[string]string{}, nil, ScriptRuntimeMeta{}, nil,
	)
	if err != nil {
		t.Fatalf("script failed: %v", err)
	}
	if got := types.GetKeyValue(state.headers, "X-Two"); got != "strings" {
		t.Fatalf("X-Two = %q, want %q", got, "strings")
	}
}

// FIX 5. The 2s budget is for RUNNING JavaScript. A token endpoint that takes
// 2.5s to answer used to consume the whole budget and kill the request, so the
// single most common thing a pre-request script does — fetch a token — could not
// be done against any server slower than two seconds.
func TestScriptBudgetExcludesTimeSpentInsideSendRequest(t *testing.T) {
	if testing.Short() {
		t.Skip("this test deliberately waits on a slow endpoint")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2500 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"token":"abc"}`))
	}))
	defer server.Close()

	item := regressionItem()
	script := `
let token = "none"
bru.sendRequest({ method: "GET", url: "` + server.URL + `" }, function (err, res) {
  if (err) { throw new Error("token fetch failed: " + err.message) }
  token = res.data.token
})
req.setHeader("Authorization", "Bearer " + token)
`
	start := time.Now()
	state, err := RunPreRequestScriptSourceMeta(
		SingleScriptSource("request pre-request script", script),
		item, map[string]string{}, nil, ScriptRuntimeMeta{}, nil,
	)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("a 2.5s token fetch killed the script after %s: %v", elapsed, err)
	}
	if got := types.GetKeyValue(state.headers, "Authorization"); got != "Bearer abc" {
		t.Fatalf("Authorization = %q, want %q", got, "Bearer abc")
	}
	if elapsed < 2*time.Second {
		t.Fatalf("the endpoint cannot have been reached in %s", elapsed)
	}
}

// The budget must still bite on a script that burns CPU, or the fix above would
// have been "remove the timeout".
func TestScriptBudgetStillStopsARunawayLoop(t *testing.T) {
	if testing.Short() {
		t.Skip("this test deliberately waits for the script budget")
	}
	_, err := RunPreRequestScriptSourceMeta(
		SingleScriptSource("request pre-request script", `while (true) { Math.sqrt(2) }`),
		regressionItem(), map[string]string{}, nil, ScriptRuntimeMeta{}, nil,
	)
	if err == nil {
		t.Fatal("an infinite loop was not interrupted")
	}
}

// FIX 6. Each level runs in its own scope, so the same top-level `const` at the
// collection, folder and request levels is three declarations, not a
// redeclaration that is a SyntaxError killing all three.
func TestEachScriptLevelGetsItsOwnScope(t *testing.T) {
	source := ScriptSource{Levels: []ScriptLevel{
		{Label: "collection pre-request script", Source: `const token = "collection"; bru.setVar("order", (bru.getVar("order") || "") + "collection;")`},
		{Label: "folder pre-request script (Auth)", Source: `const token = "folder"; bru.setVar("order", (bru.getVar("order") || "") + "folder;")`},
		{Label: "request pre-request script", Source: `const token = "request"; bru.setVar("order", (bru.getVar("order") || "") + "request;")`},
	}}
	vars := NewFlatScriptVariableContext(map[string]string{})
	if _, err := RunPreRequestScriptSourceMeta(
		source, regressionItem(), vars.Combined, nil, ScriptRuntimeMeta{Variables: vars}, nil,
	); err != nil {
		t.Fatalf("a const at more than one level was still a redeclaration: %v", err)
	}
	if got := vars.Runtime["order"]; got != "collection;folder;request;" {
		t.Fatalf("levels ran in the wrong order: %v", got)
	}
}

// State must still flow between levels through the supported channel.
func TestScriptLevelsShareStateThroughBruVars(t *testing.T) {
	source := ScriptSource{Levels: []ScriptLevel{
		{Label: "collection pre-request script", Source: `bru.setVar("handoff", "carried")`},
		{Label: "request pre-request script", Source: `if (bru.getVar("handoff") !== "carried") { throw new Error("the collection script's value did not reach the request script") }`},
	}}
	vars := NewFlatScriptVariableContext(map[string]string{})
	if _, err := RunPreRequestScriptSourceMeta(
		source, regressionItem(), vars.Combined, nil, ScriptRuntimeMeta{Variables: vars}, nil,
	); err != nil {
		t.Fatal(err)
	}
}

// The ordering the audit verified, stated as data: outermost first going in,
// innermost first coming out.
func TestMergedRuntimeScriptsOrdersTheLevels(t *testing.T) {
	collection := types.Collection{
		Name:       "c",
		PreScript:  `collectionPre`,
		PostScript: `collectionPost`,
		Tests:      `collectionTests`,
		Folders: []types.FolderConfig{
			{Path: "outer", Name: "outer", PreScript: `outerPre`, PostScript: `outerPost`, Tests: `outerTests`},
			{Path: "outer/inner", Name: "inner", PreScript: `innerPre`, PostScript: `innerPost`, Tests: `innerTests`},
		},
	}
	item := types.RequestItem{
		Name: "r", FolderPath: "outer/inner",
		PreScript: `requestPre`, PostScript: `requestPost`, Tests: `requestTests`,
	}

	scripts := MergedRuntimeScripts(collection, item)
	sources := func(source ScriptSource) []string {
		out := []string{}
		for _, level := range source.Levels {
			out = append(out, level.Source)
		}
		return out
	}
	if got, want := sources(scripts.PreLevels), []string{"collectionPre", "outerPre", "innerPre", "requestPre"}; !reflect.DeepEqual(got, want) {
		t.Errorf("pre order = %v, want %v", got, want)
	}
	if got, want := sources(scripts.PostLevels), []string{"requestPost", "innerPost", "outerPost", "collectionPost"}; !reflect.DeepEqual(got, want) {
		t.Errorf("post order = %v, want %v", got, want)
	}
	if got, want := sources(scripts.TestsLevels), []string{"requestTests", "innerTests", "outerTests", "collectionTests"}; !reflect.DeepEqual(got, want) {
		t.Errorf("tests order = %v, want %v", got, want)
	}
	// The joined strings the older entry points still take must not have moved.
	if scripts.Pre != "collectionPre\nouterPre\ninnerPre\nrequestPre" {
		t.Errorf("joined Pre = %q", scripts.Pre)
	}
	for _, level := range scripts.PreLevels.Levels {
		if strings.TrimSpace(level.Label) == "" {
			t.Error("a level has no label; its errors would name no script")
		}
	}
	if !strings.Contains(scripts.PreLevels.Levels[1].Label, "outer") {
		t.Errorf("folder label = %q, want the folder's name in it", scripts.PreLevels.Levels[1].Label)
	}
}

// FIX 7. The error must name the script it came from and a line number that
// exists in THAT script — not a line in the wrapped, concatenated document the
// user never sees.
func TestScriptErrorNamesTheLevelAndTheUserVisibleLine(t *testing.T) {
	source := ScriptSource{Levels: []ScriptLevel{
		{Label: "collection pre-request script", Source: "bru.setVar('a', 1)"},
		{Label: "folder pre-request script (Auth)", Source: "const ok = 1\nmissingFunction()"},
	}}
	_, err := RunPreRequestScriptSourceMeta(source, regressionItem(), map[string]string{}, nil, ScriptRuntimeMeta{}, nil)
	if err == nil {
		t.Fatal("the failing level did not report an error")
	}
	message := err.Error()
	if !strings.Contains(message, "folder pre-request script (Auth)") {
		t.Errorf("error = %q, does not name the script that failed", message)
	}
	if !strings.Contains(message, "line 2") {
		t.Errorf("error = %q, want the line number within the user's own script (2)", message)
	}
	if strings.Contains(message, "<eval>") {
		t.Errorf("error = %q, still leaks the runtime's synthetic source name", message)
	}
}

// A SyntaxError at one level must not be reported with the error type twice, and
// its position must be the user's line.
func TestSyntaxErrorIsReportedOnceWithTheUserLine(t *testing.T) {
	source := ScriptSource{Levels: []ScriptLevel{
		{Label: "request pre-request script", Source: "const a = 1\nconst a = 2"},
	}}
	_, err := RunPreRequestScriptSourceMeta(source, regressionItem(), map[string]string{}, nil, ScriptRuntimeMeta{}, nil)
	if err == nil {
		t.Fatal("the redeclaration was not reported")
	}
	message := err.Error()
	if strings.Contains(message, "SyntaxError: SyntaxError:") {
		t.Errorf("error = %q, the type is still printed twice", message)
	}
	if !strings.HasPrefix(message, "SyntaxError:") {
		t.Errorf("error = %q, want it to still say what kind of error it is", message)
	}
	if !strings.Contains(message, "line 2") {
		t.Errorf("error = %q, want the user's line 2", message)
	}
}

// A failing expectation must read as an expectation, not as a Go stack trace.
func TestFailedExpectationMessageCarriesNoGoInternals(t *testing.T) {
	results := []types.TestResult{}
	response := types.Response{Status: 200, Headers: map[string]string{}}
	if _, err := RunPostResponseScriptSourceMeta(
		SingleScriptSource("post-response script", `pm.test("mismatch", function () { pm.expect(1).to.equal(2) })`),
		*regressionItem(), &response, map[string]string{}, nil, ScriptRuntimeMeta{}, &results,
	); err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("want one row, got %s", passedNames(results))
	}
	message := results[0].Message
	if strings.Contains(message, "GoError") || strings.Contains(message, "github.com/") || strings.Contains(message, "(native)") {
		t.Fatalf("message = %q, still carries Go internals", message)
	}
	if !strings.Contains(message, "expected 1 to equal 2") {
		t.Fatalf("message = %q, lost the assertion text", message)
	}
}

func TestCleanScriptErrorMessageStripsTheKnownNoise(t *testing.T) {
	for name, testCase := range map[string]struct{ in, want string }{
		"go error with a native frame": {
			"GoError: expected 1 to equal 2 at github.com/mutexdev/lite_api/internal/scripting.expectMatch.func1 (native)",
			"expected 1 to equal 2",
		},
		"doubled syntax error": {
			"SyntaxError: SyntaxError: Identifier 'a' has already been declared at 4:7",
			"SyntaxError: Identifier 'a' has already been declared",
		},
		"eval frame": {
			"ReferenceError: undefinedFn is not defined at <eval>:3:12(2)",
			"ReferenceError: undefinedFn is not defined",
		},
	} {
		t.Run(name, func(t *testing.T) {
			if got := CleanScriptErrorMessage(fmt.Errorf("%s", testCase.in)); got != testCase.want {
				t.Errorf("got %q, want %q", got, testCase.want)
			}
		})
	}
}

// FIX 8. Every one of these used to be undefined in safe mode — the DEFAULT
// mode — and a missing name is a ReferenceError that ends the whole request.
// They are lexical bindings, not globals, which is the shape safe mode already
// used for setTimeout and the one Bruno's wrapper matches.
func TestSafeModeProvidesTheSchedulingAndConsoleGlobals(t *testing.T) {
	for _, name := range []string{
		"setTimeout", "clearTimeout", "setInterval", "clearInterval",
		"setImmediate", "clearImmediate", "queueMicrotask",
	} {
		t.Run(name, func(t *testing.T) {
			script := fmt.Sprintf(`if (typeof %s !== "function") { throw new Error("%s is not a function") }`, name, name)
			if _, err := RunPreRequestScriptSourceMeta(
				SingleScriptSource("request pre-request script", script),
				regressionItem(), map[string]string{}, nil, ScriptRuntimeMeta{JSSandboxMode: "safe"}, nil,
			); err != nil {
				t.Fatalf("safe mode: %v", err)
			}
		})
	}
	for _, name := range []string{"table", "clear"} {
		t.Run("console."+name, func(t *testing.T) {
			script := fmt.Sprintf(`if (typeof console.%s !== "function") { throw new Error("console.%s is missing") }`, name, name)
			if _, err := RunPreRequestScriptSourceMeta(
				SingleScriptSource("request pre-request script", script),
				regressionItem(), map[string]string{}, nil, ScriptRuntimeMeta{JSSandboxMode: "safe"}, nil,
			); err != nil {
				t.Fatalf("safe mode: %v", err)
			}
		})
	}
}

// Safe mode's globalThis must stay as clean as it was: the primitives arrive as
// lexical bindings, and the holder they arrive in is gone before the first user
// line runs. A script that probes globalThis must see exactly what it saw before.
func TestSafeModeLeavesGlobalThisClean(t *testing.T) {
	script := `
for (const name of ["setTimeout", "clearTimeout", "setInterval", "clearInterval", "setImmediate", "clearImmediate", "queueMicrotask", "__bruTimers", "__bruSetTimeout"]) {
  if (typeof globalThis[name] !== "undefined") { throw new Error("globalThis." + name + " leaked into safe mode") }
}
if (typeof setTimeout !== "function") { throw new Error("the lexical setTimeout is missing") }
`
	if _, err := RunPreRequestScriptSourceMeta(
		SingleScriptSource("request pre-request script", script),
		regressionItem(), map[string]string{}, nil, ScriptRuntimeMeta{JSSandboxMode: "safe"}, nil,
	); err != nil {
		t.Fatal(err)
	}
}

// And every level gets them, not just the first — the holder is consumed by the
// wrapper, so a second script used to find nothing there.
func TestSafeModeTimersReachEveryLevel(t *testing.T) {
	source := ScriptSource{Levels: []ScriptLevel{
		{Label: "collection pre-request script", Source: `if (typeof clearTimeout !== "function") { throw new Error("level 1 has no clearTimeout") }`},
		{Label: "folder pre-request script (Auth)", Source: `if (typeof clearTimeout !== "function") { throw new Error("level 2 has no clearTimeout") }`},
		{Label: "request pre-request script", Source: `if (typeof setTimeout !== "function") { throw new Error("level 3 has no setTimeout") }`},
	}}
	if _, err := RunPreRequestScriptSourceMeta(
		source, regressionItem(), map[string]string{}, nil, ScriptRuntimeMeta{JSSandboxMode: "safe"}, nil,
	); err != nil {
		t.Fatal(err)
	}
}

// The whole point of providing them: the calls have to work, not merely exist.
func TestSafeModeTimersAndConsoleActuallyRun(t *testing.T) {
	logs := []types.ScriptLog{}
	script := `
const handle = setTimeout(function () { bru.setVar("timerRan", "no") }, 5000)
clearTimeout(handle)
const ticker = setInterval(function () {}, 10)
clearInterval(ticker)
clearImmediate(setImmediate(function () {}))
queueMicrotask(function () { bru.setVar("microtask", "ran") })
console.table([{ a: 1 }])
console.clear()
await new Promise(function (resolve) { setTimeout(resolve, 1) })
bru.setVar("finished", "yes")
`
	vars := NewFlatScriptVariableContext(map[string]string{})
	if _, err := RunPreRequestScriptSourceMeta(
		SingleScriptSource("request pre-request script", script),
		regressionItem(), vars.Combined, nil,
		ScriptRuntimeMeta{JSSandboxMode: "safe", Variables: vars}, nil, &logs,
	); err != nil {
		t.Fatalf("safe-mode timers failed: %v", err)
	}
	if vars.Runtime["finished"] != "yes" {
		t.Error("the script did not reach its end")
	}
	if vars.Runtime["timerRan"] != nil {
		t.Error("clearTimeout did not cancel the timer")
	}
	if len(logs) < 2 {
		t.Errorf("console.table/console.clear recorded nothing: %#v", logs)
	}
}

// An interval the script never clears must not turn into a "script timeout" the
// user did not cause, and must not outlive the script either.
func TestUnclearedIntervalDoesNotTimeOutTheScript(t *testing.T) {
	start := time.Now()
	vars := NewFlatScriptVariableContext(map[string]string{})
	if _, err := RunPreRequestScriptSourceMeta(
		SingleScriptSource("request pre-request script", `setInterval(function () {}, 10); bru.setVar("done", "yes")`),
		regressionItem(), vars.Combined, nil, ScriptRuntimeMeta{Variables: vars}, nil,
	); err != nil {
		t.Fatalf("an uncleared setInterval failed the script: %v", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("the script spun for %s waiting on an interval nobody was awaiting", elapsed)
	}
	if vars.Runtime["done"] != "yes" {
		t.Error("the script did not finish")
	}
}

// FIX 9a. Setting an environment variable with no environment selected is
// discarded. Silently, before this.
func TestSetEnvVarWithNoEnvironmentWarnsIntoTheScriptLogs(t *testing.T) {
	logs := []types.ScriptLog{}
	vars := NewFlatScriptVariableContext(map[string]string{})
	if _, err := RunPreRequestScriptSourceMeta(
		SingleScriptSource("request pre-request script", `pm.environment.set("token", "abc")`),
		regressionItem(), vars.Combined, nil, ScriptRuntimeMeta{Variables: vars}, nil, &logs,
	); err != nil {
		t.Fatal(err)
	}
	if len(logs) == 0 {
		t.Fatal("the discarded write produced no warning at all")
	}
	if logs[0].Level != "warn" || !strings.Contains(logs[0].Message, "token") {
		t.Fatalf("warning = %#v, want a warn naming the variable", logs[0])
	}
}

func TestSetEnvVarWithAnEnvironmentSelectedDoesNotWarn(t *testing.T) {
	logs := []types.ScriptLog{}
	vars := NewFlatScriptVariableContext(map[string]string{})
	if _, err := RunPreRequestScriptSourceMeta(
		SingleScriptSource("request pre-request script", `pm.environment.set("token", "abc")`),
		regressionItem(), vars.Combined, nil,
		ScriptRuntimeMeta{Variables: vars, EnvironmentID: "env-1", EnvironmentName: "Dev"}, nil, &logs,
	); err != nil {
		t.Fatal(err)
	}
	if len(logs) != 0 {
		t.Fatalf("a write that WILL persist warned anyway: %#v", logs)
	}
}

// FIX 9b. bru.sleep clamps at a second; a script that asked for five used to get
// one and no indication.
func TestSleepClampWarnsIntoTheScriptLogs(t *testing.T) {
	logs := []types.ScriptLog{}
	if _, err := RunPreRequestScriptSourceMeta(
		SingleScriptSource("request pre-request script", `await bru.sleep(5000)`),
		regressionItem(), map[string]string{}, nil, ScriptRuntimeMeta{}, nil, &logs,
	); err != nil {
		t.Fatal(err)
	}
	if len(logs) == 0 || logs[0].Level != "warn" || !strings.Contains(logs[0].Message, "clamped") {
		t.Fatalf("bru.sleep(5000) did not warn that it was clamped: %#v", logs)
	}
}

func TestSleepWithinTheClampDoesNotWarn(t *testing.T) {
	logs := []types.ScriptLog{}
	if _, err := RunPreRequestScriptSourceMeta(
		SingleScriptSource("request pre-request script", `await bru.sleep(1)`),
		regressionItem(), map[string]string{}, nil, ScriptRuntimeMeta{}, nil, &logs,
	); err != nil {
		t.Fatal(err)
	}
	if len(logs) != 0 {
		t.Fatalf("an unclamped sleep warned anyway: %#v", logs)
	}
}

// FIX 9c. pm.request.method / .name / .body were snapshots taken before the
// script ran, contradicting the file's own contract that every member delegates.
func TestPostmanRequestMembersAreLive(t *testing.T) {
	script := `
req.setMethod("POST")
if (pm.request.method !== "POST") { throw new Error("pm.request.method is stale: " + pm.request.method) }
req.setBody("changed")
if (String(pm.request.body) !== "changed") { throw new Error("pm.request.body is stale: " + pm.request.body) }
if (typeof pm.request.name !== "string") { throw new Error("pm.request.name went missing") }
`
	item := regressionItem()
	item.Body = types.RequestBody{Mode: "text", Text: "original"}
	if _, err := RunPreRequestScriptSourceMeta(
		SingleScriptSource("request pre-request script", script),
		item, map[string]string{}, nil, ScriptRuntimeMeta{}, nil,
	); err != nil {
		t.Fatalf("pm.request members are still construction-time snapshots: %v", err)
	}
}
