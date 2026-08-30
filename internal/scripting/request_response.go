package scripting

// The request and response objects a script sees, and the Postman-compatible
// API installed over them.
//
// Split out of scripting.go by AST: declarations are identified by the parser
// and copied verbatim from their source offsets.

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mutexdev/lite_api/internal/cookiejar"
	"github.com/mutexdev/lite_api/internal/interp"
	"github.com/mutexdev/lite_api/internal/scalar"
	"github.com/mutexdev/lite_api/internal/types"

	"github.com/dop251/goja"
)

func scriptResponseObject(runtime *goja.Runtime, response types.Response) *goja.Object {
	headers := scriptResponseHeaders(response.Headers)
	bodyBytes := responseDataBytes(response)
	data := scriptResponseBody(response, false)
	status := interface{}(response.Status)
	if ScriptRunRequestResponseIsSkipped(response) {
		status = "skipped"
		data = nil
	}
	responseObject := runtime.NewObject()
	_ = responseObject.Set("status", status)
	_ = responseObject.Set("statusCode", response.Status)
	_ = responseObject.Set("statusText", scalar.CleanStatusText(response.Status, response.StatusText))
	_ = responseObject.Set("headers", headers)
	_ = responseObject.Set("data", data)
	_ = responseObject.Set("body", response.Body)
	_ = responseObject.Set("dataBuffer", scriptByteArrayValue(runtime, bodyBytes))
	_ = responseObject.Set("size", response.Size)
	_ = responseObject.Set("duration", response.DurationMs)
	_ = responseObject.Set("responseTime", response.DurationMs)
	_ = responseObject.Set("url", response.RequestedURL)
	_ = responseObject.Set("error", response.Error)
	return responseObject
}

func ScriptRunRequestResponseIsSkipped(response types.Response) bool {
	return response.Status == 0 && strings.HasPrefix(response.StatusText, "bru.runRequest does not support ")
}

func applyScriptedRequest(item *types.RequestItem, reqObject *goja.Object, state *RequestState) {
	if value := strings.TrimSpace(reqObject.Get("url").String()); value != "" {
		item.URL = value
	}
	if value := strings.TrimSpace(reqObject.Get("method").String()); value != "" {
		item.Method = strings.ToUpper(value)
	}
	item.Headers = state.headers
	if state.timeoutSet {
		item.Settings.TimeoutMs = state.timeoutMs
	}
	if state.maxRedirectsSet {
		item.Settings.MaxRedirects = state.maxRedirects
		item.Settings.FollowRedirects = state.maxRedirects != 0
	}
	if state.disableParsingResponseJSON {
		item.Settings.DisableParsingResponseJSON = true
	}
	if state.bodySet {
		applyScriptedBody(item, state.bodyValue, state.headers)
		return
	}
	bodyValue := reqObject.Get("body")
	if bodyValue == nil || goja.IsUndefined(bodyValue) || goja.IsNull(bodyValue) {
		return
	}
	exported := bodyValue.Export()
	if exported == nil {
		return
	}
	// req.body is seeded with types.RequestBodySnapshot — a FLAT, human-readable
	// rendering of whatever mode the body is in. Writing that back unconditionally
	// destroyed the body of every request that merely had a script attached:
	//
	//   formUrlEncoded  ->  mode "text", raw "a=value with spaces & symbols"
	//                       (unencoded, and Content-Type forced on)
	//   multipart       ->  mode "text", "name=value" lines, no boundary
	//   none            ->  mode "text", "" plus a Content-Type: text/plain
	//
	// A GET with no body acquired a body; `bru.setVar("x", 1)` changed what went
	// on the wire. The snapshot is only a view, so it is applied only when the
	// script actually replaced it.
	if text, ok := exported.(string); ok && text == state.bodySnapshot {
		return
	}
	applyScriptedBody(item, exported, state.headers)
}

func ScriptErrorResponse(label string, err error) types.Response {
	return types.Response{
		SentAt:      time.Now(),
		Headers:     map[string]string{},
		Error:       label + ": " + err.Error(),
		PreviewMode: "raw",
		TestResults: []types.TestResult{{Name: label, Passed: false, Message: err.Error()}},
	}
}

func ScriptSkippedResponse(item types.RequestItem, vars map[string]string) types.Response {
	return types.Response{
		SentAt:       time.Now(),
		RequestedURL: cookiejar.PreviewRequestURL(item, vars),
		StatusText:   "Skipped",
		Headers:      map[string]string{},
		PreviewMode:  "raw",
	}
}

func scriptRequestBody(item types.RequestItem, state *RequestState, vars map[string]string, raw bool) interface{} {
	if state != nil && state.bodySet {
		if raw {
			return scriptRawBody(state.bodyValue)
		}
		return state.bodyValue
	}
	body := interp.Interpolate(types.RequestBodySnapshot(item.Body), vars)
	if raw {
		return body
	}
	contentType := ""
	if state != nil {
		contentType = types.GetKeyValue(state.headers, "Content-Type")
	}
	if item.Body.Mode == "json" || strings.Contains(strings.ToLower(contentType), "json") {
		if jsonValue, ok := responseJSONValue(body); ok {
			return jsonValue
		}
	}
	return body
}

func applyScriptedResponse(response *types.Response, resObject *goja.Object) {
	if response == nil || resObject == nil {
		return
	}
	bodyValue := resObject.Get("body")
	bodyText := ""
	if bodyValue != nil && !goja.IsUndefined(bodyValue) && !goja.IsNull(bodyValue) {
		bodyText = scriptRawBody(bodyValue.Export())
	}
	response.Body = bodyText
	response.BodyBase64 = base64.StdEncoding.EncodeToString([]byte(bodyText))
	response.Size = len([]byte(bodyText))
	response.PreviewMode = PreviewModeFromHeaders(response.Headers)
	if response.PreviewMode == "auto" && scalar.LooksLikeJSON(bodyText) {
		response.PreviewMode = "json"
	}
}

func scriptResponseBody(response types.Response, disableParsingJSON bool) interface{} {
	if disableParsingJSON {
		return response.Body
	}
	if jsonValue, ok := responseJSONValue(response.Body); ok {
		return jsonValue
	}
	return response.Body
}

func scriptResponseSize(response types.Response, headers map[string]string) map[string]int {
	bodySize := response.Size
	if bodySize == 0 && response.Body != "" {
		bodySize = len([]byte(response.Body))
	}
	statusLine := fmt.Sprintf("HTTP/1.1 %d %s\r\n", response.Status, scalar.CleanStatusText(response.Status, response.StatusText))
	headerSize := len([]byte(statusLine))
	for name, value := range headers {
		headerSize += len([]byte(name + ": " + value + "\r\n"))
	}
	headerSize += len([]byte("\r\n"))
	return map[string]int{
		"header": headerSize,
		"body":   bodySize,
		"total":  headerSize + bodySize,
	}
}

// installPostmanRequestAPI exposes pm.request over the existing `req` object
// (US-041). Every accessor delegates, so a script that mutates the request
// through req and reads it back through pm.request sees its own change.
func installPostmanRequestAPI(runtime *goja.Runtime, pm, reqObject *goja.Object, item types.RequestItem) {
	request := runtime.NewObject()

	// Postman's pm.request.url is an OBJECT, not a string: scripts routinely
	// call pm.request.url.toString() and pm.request.url.getPath(). Exposing a
	// bare string would satisfy toString() by accident and then fail on every
	// other member.
	url := runtime.NewObject()
	_ = url.Set("toString", reqObject.Get("getUrl"))
	_ = url.Set("getHost", reqObject.Get("getHost"))
	_ = url.Set("getPath", reqObject.Get("getPath"))
	_ = url.Set("getQueryString", reqObject.Get("getQueryString"))
	_ = request.Set("url", url)

	// Accessors, not snapshots. The comment above says every member delegates so
	// that a change made through req is visible through pm.request; these three
	// were plain values read at construction, so a pre-request script that did
	// `req.setMethod("POST")` still saw pm.request.method === "GET" — and
	// pm.request.body kept reporting the body the request had before the script
	// rewrote it.
	live := func(name string, read func() goja.Value) {
		_ = request.DefineAccessorProperty(name, runtime.ToValue(func(goja.FunctionCall) goja.Value {
			return read()
		}), nil, goja.FLAG_FALSE, goja.FLAG_TRUE)
	}
	live("method", func() goja.Value { return reqObject.Get("method") })
	live("body", func() goja.Value { return reqObject.Get("body") })
	live("name", func() goja.Value {
		if value := reqObject.Get("name"); value != nil && !goja.IsUndefined(value) {
			return value
		}
		return runtime.ToValue(item.Name)
	})
	_ = request.Set("getHeaders", reqObject.Get("getHeaders"))

	headerList := reqObject.Get("headerList")
	headerListObject := (*goja.Object)(nil)
	if headerList != nil && !goja.IsUndefined(headerList) && !goja.IsNull(headerList) {
		headerListObject = headerList.ToObject(runtime)
	}
	headers := runtime.NewObject()
	_ = headers.Set("get", reqObject.Get("getHeader"))
	// add/upsert are bound to the HEADER LIST, not to req.setHeader(name, value).
	//
	// Postman's own idiom is the object form — pm.request.headers.add({key:
	// "Authorization", value: token}) — and routing that at a two-string Go
	// function stringified the object into the NAME: every such call produced a
	// header literally called "[object Object]", which the server ignored and
	// nothing in the UI explained. The header list already accepts both forms
	// (scriptHeaderFromArgs), so this is a rebinding, not a new implementation.
	if headerListObject != nil {
		_ = headers.Set("add", headerListObject.Get("add"))
		_ = headers.Set("upsert", headerListObject.Get("upsert"))
	} else {
		_ = headers.Set("add", reqObject.Get("setHeader"))
		_ = headers.Set("upsert", reqObject.Get("setHeader"))
	}
	_ = headers.Set("remove", reqObject.Get("deleteHeader"))
	_ = headers.Set("toObject", reqObject.Get("getHeaders"))
	// Delegated rather than reading a captured header map: req.setHeader can
	// add a header during the pre-request script, and a Snapshot taken at
	// construction would report it missing.
	_ = headers.Set("has", func(name string) bool {
		fn, ok := goja.AssertFunction(reqObject.Get("getHeader"))
		if !ok {
			return false
		}
		result, err := fn(goja.Undefined(), runtime.ToValue(name))
		return err == nil && strings.TrimSpace(result.String()) != ""
	})
	_ = request.Set("headers", headers)

	_ = pm.Set("request", request)
}

// installPostmanResponseAPI exposes pm.response over the existing `res` object
// (US-041), including the pm.response.to.have.* assertion chain.
//
// One deliberate divergence from `res`, and it is the kind that is silent if
// got wrong: in Postman, pm.response.status is the status TEXT ("OK") and
// pm.response.code is the NUMBER, whereas this codebase's res.status is the
// number. Inside pm.* Postman's meaning wins, or every script copied across
// that compares pm.response.status to "OK" is quietly always false. res.status
// is untouched for bru scripts.
func installPostmanResponseAPI(runtime *goja.Runtime, pm, resObject *goja.Object, response types.Response) {
	responseObject := runtime.NewObject()
	_ = responseObject.Set("code", response.Status)
	_ = responseObject.Set("status", scalar.CleanStatusText(response.Status, response.StatusText))
	_ = responseObject.Set("responseTime", response.DurationMs)
	_ = responseObject.Set("responseSize", scriptResponseSize(response, scriptResponseHeaders(response.Headers))["body"])
	_ = responseObject.Set("text", func() string { return responseCurrentBody(resObject, response) })
	_ = responseObject.Set("json", func() goja.Value {
		body := responseCurrentBody(resObject, response)
		value, ok := responseJSONValue(body)
		if !ok {
			// Postman throws here rather than returning null, and the throw is
			// the useful behaviour: a test that asked for JSON and got HTML
			// should fail loudly, not compare against null and pass.
			panic(runtime.NewGoError(errors.New("response body is not valid JSON")))
		}
		return runtime.ToValue(value)
	})

	headers := runtime.NewObject()
	_ = headers.Set("get", resObject.Get("getHeader"))
	_ = headers.Set("toObject", resObject.Get("getHeaders"))
	_ = headers.Set("has", func(name string) bool {
		return getHeaderValue(response.Headers, name) != ""
	})
	_ = responseObject.Set("headers", headers)

	installPostmanResponseAssertions(runtime, responseObject, resObject, response)
	_ = pm.Set("response", responseObject)
}

// installPostmanResponseAssertions builds pm.response.to.have.* and
// pm.response.to.be.*.
//
// Every failure panics with a GoError, which is what the enclosing pm.test
// catches and records as a failed types.TestResult. An assertion that returned false
// instead would let `pm.test("status is 200", () => pm.response.to.have.
// status(200))` pass while the status was 500 — the exact silent green this
// whole API is supposed to prevent.
func installPostmanResponseAssertions(runtime *goja.Runtime, responseObject, resObject *goja.Object, response types.Response) {
	fail := func(format string, args ...interface{}) {
		panic(runtime.NewGoError(fmt.Errorf(format, args...)))
	}

	have := runtime.NewObject()
	_ = have.Set("status", func(expected goja.Value) {
		// Postman accepts either the code or the status text.
		if expected == nil || goja.IsUndefined(expected) {
			fail("status assertion needs an expected code or status text")
		}
		if number, ok := expected.Export().(int64); ok {
			if response.Status != int(number) {
				fail("expected response to have status %d but got %d", number, response.Status)
			}
			return
		}
		text := scalar.CleanStatusText(response.Status, response.StatusText)
		if !strings.EqualFold(strings.TrimSpace(expected.String()), strings.TrimSpace(text)) {
			fail("expected response to have status %q but got %q", expected.String(), text)
		}
	})
	_ = have.Set("header", func(call goja.FunctionCall) goja.Value {
		name := call.Argument(0).String()
		actual := getHeaderValue(response.Headers, name)
		if actual == "" {
			fail("expected response to have header %q", name)
		}
		if len(call.Arguments) > 1 {
			if expected := call.Argument(1).String(); actual != expected {
				fail("expected header %q to be %q but got %q", name, expected, actual)
			}
		}
		return goja.Undefined()
	})
	_ = have.Set("body", func(call goja.FunctionCall) goja.Value {
		body := responseCurrentBody(resObject, response)
		if len(call.Arguments) == 0 {
			if body == "" {
				fail("expected response to have a body")
			}
			return goja.Undefined()
		}
		if expected := call.Argument(0).String(); body != expected {
			fail("expected body to be %q but got %q", expected, body)
		}
		return goja.Undefined()
	})
	_ = have.Set("jsonBody", func(call goja.FunctionCall) goja.Value {
		body := responseCurrentBody(resObject, response)
		value, ok := responseJSONValue(body)
		if !ok {
			fail("expected response body to be valid JSON")
		}
		if len(call.Arguments) == 0 {
			return goja.Undefined()
		}
		// With a path, assert that path resolves; with a path and a value,
		// assert equality at it.
		path := call.Argument(0).String()
		actual, found := scriptResponseJQ(value, path)
		if !found {
			fail("expected JSON body to have %q", path)
		}
		if len(call.Arguments) > 1 {
			expected := call.Argument(1).Export()
			if fmt.Sprintf("%v", actual) != fmt.Sprintf("%v", expected) {
				fail("expected %q to be %v but got %v", path, expected, actual)
			}
		}
		return goja.Undefined()
	})

	be := runtime.NewObject()
	// These are PROPERTIES in Postman, not calls: `pm.response.to.be.ok`
	// asserts on access. Defined as accessors so the assertion actually runs;
	// a plain value would make every one of them a no-op that reads as a
	// passing test.
	statusClass := func(name string, low, high int) {
		_ = be.DefineAccessorProperty(name, runtime.ToValue(func(goja.FunctionCall) goja.Value {
			if response.Status < low || response.Status > high {
				fail("expected response to be %s but status was %d", name, response.Status)
			}
			return goja.Undefined()
		}), nil, goja.FLAG_FALSE, goja.FLAG_TRUE)
	}
	statusClass("ok", 200, 299)
	statusClass("success", 200, 299)
	statusClass("redirection", 300, 399)
	statusClass("clientError", 400, 499)
	statusClass("serverError", 500, 599)
	_ = be.DefineAccessorProperty("error", runtime.ToValue(func(goja.FunctionCall) goja.Value {
		if response.Status < 400 {
			fail("expected response to be an error but status was %d", response.Status)
		}
		return goja.Undefined()
	}), nil, goja.FLAG_FALSE, goja.FLAG_TRUE)

	to := runtime.NewObject()
	_ = to.Set("have", have)
	_ = to.Set("be", be)
	_ = responseObject.Set("to", to)
}
