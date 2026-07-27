// Package scripting is the goja runtime the app exposes to pre- and
// post-request scripts: the pm.* and bru.* API surfaces, the Node shims, the
// test and assertion vocabulary, and the sandbox around them.
//
// US-068, the largest story in the plan. Every function here was already free
// of *App -- the methods it does define are on its own types, which move with
// their receivers -- and that is what made a block this size movable.
package scripting

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	goruntime "runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mutexdev/lite_api/internal/interp"
	"github.com/mutexdev/lite_api/internal/scalar"
	"github.com/mutexdev/lite_api/internal/types"
	"github.com/mutexdev/lite_api/internal/urlbuild"

	"github.com/dop251/goja"
)

type runtimeScripts struct {
	Pre   string
	Post  string
	Tests string
}

type Controls struct {
	SkipRequest     bool
	StopExecution   bool
	NextRequestSet  bool
	NextRequestName *string
	// US-058. Carried through the same Merge as the other controls so a
	// visualizer set in ANY phase reaches the response, with a later phase
	// winning — a tests script refining what the post-response script set is
	// the normal case, not a conflict.
	Visualizer *types.VisualizerPayload
}

type ScriptRuntimeMeta struct {
	CollectionName            string
	CollectionPath            string
	EnvironmentName           string
	JSSandboxMode             string
	Variables                 *VariableContext
	OAuth2CredentialVariables func() map[string]interface{}
	ResetOAuth2Credential     func(string) error
	RunRequest                func(string) (types.Response, *types.TimelineItem, error)
	RecordTimeline            func(types.TimelineItem)
	TimelinePhase             string
	// US-039. Iteration position for pm.info. IterationCount is 0 outside a
	// collection run; IterationIndex is 1-based here and converted to
	// Postman's 0-based pm.info.iteration at the boundary.
	IterationIndex int
	IterationCount int
}

var scriptRuntimeEventLoops sync.Map

type scriptEventLoop struct {
	runtime      *goja.Runtime
	nextID       int64
	timers       map[int64]*scriptTimer
	pendingTests int
}

func (controls *Controls) Merge(state *RequestState) {
	if state == nil {
		return
	}
	controls.SkipRequest = controls.SkipRequest || state.SkipRequest
	controls.StopExecution = controls.StopExecution || state.stopExecution
	if state.nextRequestSet {
		controls.NextRequestSet = true
		controls.NextRequestName = state.nextRequestName
	}
	if state.visualizer != nil {
		controls.Visualizer = state.visualizer
	}
}

func MergedRuntimeScripts(collection types.Collection, item types.RequestItem) runtimeScripts {
	folders := FolderChain(collection, item)
	pre := []string{collection.PreScript}
	for _, folder := range folders {
		pre = append(pre, folder.PreScript)
	}
	pre = append(pre, item.PreScript)

	post := []string{item.PostScript}
	tests := []string{item.Tests}
	for i := len(folders) - 1; i >= 0; i-- {
		post = append(post, folders[i].PostScript)
		tests = append(tests, folders[i].Tests)
	}
	post = append(post, collection.PostScript)
	tests = append(tests, collection.Tests)

	return runtimeScripts{
		Pre:   joinScripts(pre...),
		Post:  joinScripts(post...),
		Tests: joinScripts(tests...),
	}
}

func joinScripts(parts ...string) string {
	joined := []string{}
	for _, part := range parts {
		if strings.TrimSpace(part) != "" {
			joined = append(joined, strings.TrimRight(part, "\n"))
		}
	}
	return strings.Join(joined, "\n")
}

func EvaluateRuntimeTestsWithJarStateMeta(script string, response types.Response, item types.RequestItem, vars map[string]string, jar *CookieJar, meta ScriptRuntimeMeta, logs ...*[]types.ScriptLog) ([]types.TestResult, *RequestState) {
	results := EvaluateScriptTests(script, response)
	js := javascriptFromTests(script)
	if strings.TrimSpace(js) == "" {
		return results, &RequestState{headers: types.CloneKeyValues(item.Headers)}
	}
	jsResults := []types.TestResult{}
	runtime, _, reqState, _ := NewScriptRuntimeWithMeta(item, response, vars, &jsResults, selectedScriptLogs(logs), jar, meta)
	if err := runGojaScript(runtime, js, meta.JSSandboxMode); err != nil {
		jsResults = append(jsResults, types.TestResult{Name: "tests script", Passed: false, Message: err.Error()})
	}
	return append(results, jsResults...), reqState
}

func javascriptFromTests(script string) string {
	lines := []string{}
	for _, line := range strings.Split(script, "\n") {
		if isLegacyExpectLine(line) {
			continue
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func isLegacyExpectLine(line string) bool {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "expect ") {
		return false
	}
	parts := strings.Fields(line)
	return len(parts) >= 4 && parts[1] == "status"
}

// ScriptShimProgram caches the compiled form of one built-in JavaScript shim.
//
// Every shim source is a compile-time-constant raw string literal, but each new
// goja.Runtime used to re-parse and re-compile all of them: goja's
// Runtime.RunString(src) is Runtime.RunScript("", src), which calls
// compile(name, src, strict=false, inGlobal=true) and then RunProgram on the
// result (goja runtime.go, RunString/RunScript). Parsing dominated
// NewScriptRuntimeWithMeta, and NewScriptRuntimeWithMeta runs up to four times
// per request.
//
// goja.Compile's own documentation states the returned *Program "is not linked
// to a runtime in any way and can be run in multiple runtimes (possibly at the
// same time)", so one compilation can be shared by every runtime process-wide.
// A *Program is immutable bytecode; all mutable state a shim creates lives in
// the goja.Runtime that runs it, and goja.Runtime is emphatically not shared.
type ScriptShimProgram struct {
	name string
	once sync.Once
	prog *goja.Program
}

func newScriptShimProgram(name string) *ScriptShimProgram {
	return &ScriptShimProgram{name: name}
}

// compiled compiles src on first use and returns the same *goja.Program on
// every later call. src must be the same constant string for a given
// ScriptShimProgram; the parameter exists only so the shim sources can stay
// beside the Go code that installs them.
//
// The compile arguments deliberately mirror RunString exactly: an empty program
// name (so any stack trace a shim produces keeps the anonymous source name it
// had before) and strict=false (RunString hard-codes strict=false, and
// compiling these shims in strict mode would change their semantics).
//
// A compile failure here is a programmer error, not a runtime condition: the
// sources are constants, so either they all compile or the very first runtime
// construction in the process fails. There is no caller-recoverable outcome and
// no per-call context to attach, so this panics with an identifying message
// rather than threading an error that can never be non-nil in a correct build.
// Runtime errors thrown by a shim are unaffected -- RunProgram still returns
// those to the existing error handling at each call site.
func (s *ScriptShimProgram) compiled(src string) *goja.Program {
	s.once.Do(func() {
		program, err := goja.Compile("", src, false)
		if err != nil {
			panic(fmt.Errorf("liteapi: built-in script shim %q failed to compile: %w", s.name, err))
		}
		s.prog = program
	})
	return s.prog
}

func NewScriptRuntimeWithMeta(item types.RequestItem, response types.Response, vars map[string]string, testResults *[]types.TestResult, scriptLogs *[]types.ScriptLog, jar *CookieJar, meta ScriptRuntimeMeta) (*goja.Runtime, *goja.Object, *RequestState, *goja.Object) {
	runtime := goja.New()
	sandboxMode := NormalizeJSSandboxMode(meta.JSSandboxMode)
	loop := installScriptEventLoop(runtime, sandboxMode)
	installScriptEncoding(runtime)
	installScriptEventTarget(runtime)
	if jar == nil {
		jar = NewScriptCookieJar(nil)
	}
	scriptVars := meta.Variables
	if scriptVars == nil {
		scriptVars = NewFlatScriptVariableContext(vars)
	}
	installScriptProcess(runtime, loop, meta.CollectionPath, scriptVars.ProcessEnv, sandboxMode)
	vars = scriptVars.Combined
	installScriptFetch(runtime, vars)
	reqState := &RequestState{headers: types.CloneKeyValues(item.Headers), runtime: runtime}
	reqObject := runtime.NewObject()
	_ = reqObject.Set("method", strings.ToUpper(scalar.FirstNonEmpty(item.Method, http.MethodGet)))
	_ = reqObject.Set("url", item.URL)
	_ = reqObject.Set("body", types.RequestBodySnapshot(item.Body))
	_ = reqObject.Set("timeout", item.Settings.TimeoutMs)
	_ = reqObject.Set("name", item.Name)
	_ = reqObject.Set("pathParams", scriptPathParams(item.PathParams))
	_ = reqObject.Set("tags", append([]string(nil), item.Tags...))
	currentRequestURL := func() string {
		rawURL := reqObject.Get("url")
		if rawURL == nil || goja.IsUndefined(rawURL) || goja.IsNull(rawURL) {
			return ""
		}
		return urlbuild.RequestURLWithParams(rawURL.String(), item.Params, item.PathParams, vars)
	}
	syncRequestHeaders := func() {
		_ = reqObject.Set("headers", keyValuesToMap(reqState.headers))
	}
	_ = reqObject.Set("getUrl", func() string { return currentRequestURL() })
	_ = reqObject.Set("setUrl", func(value string) { _ = reqObject.Set("url", value) })
	_ = reqObject.Set("getMethod", func() string { return reqObject.Get("method").String() })
	_ = reqObject.Set("setMethod", func(value string) { _ = reqObject.Set("method", strings.ToUpper(value)) })
	_ = reqObject.Set("getName", func() string { return item.Name })
	_ = reqObject.Set("getTags", func() []string { return append([]string(nil), item.Tags...) })
	_ = reqObject.Set("getPathParams", func() []map[string]interface{} { return scriptPathParams(item.PathParams) })
	_ = reqObject.Set("getHost", func() string {
		parsed, err := url.Parse(currentRequestURL())
		if err != nil {
			return ""
		}
		return parsed.Host
	})
	_ = reqObject.Set("getPath", func() string {
		parsed, err := url.Parse(currentRequestURL())
		if err != nil {
			return ""
		}
		return parsed.Path
	})
	_ = reqObject.Set("getQueryString", func() string {
		parsed, err := url.Parse(currentRequestURL())
		if err != nil {
			return ""
		}
		return parsed.RawQuery
	})
	_ = reqObject.Set("getAuthMode", func() string { return scalar.FirstNonEmpty(item.Auth.Mode, "none") })
	_ = reqObject.Set("getBody", func(call goja.FunctionCall) goja.Value {
		raw := false
		options := call.Argument(0)
		if options != nil && !goja.IsUndefined(options) && !goja.IsNull(options) {
			raw = options.ToObject(runtime).Get("raw").ToBoolean()
		}
		return runtime.ToValue(scriptRequestBody(item, reqState, vars, raw))
	})
	_ = reqObject.Set("getHeader", func(name string) string { return types.GetKeyValue(reqState.headers, name) })
	_ = reqObject.Set("getHeaders", func() map[string]string { return keyValuesToMap(reqState.headers) })
	_ = reqObject.Set("setHeader", func(name, value string) {
		reqState.headers = types.SetKeyValue(reqState.headers, name, value)
		syncRequestHeaders()
	})
	_ = reqObject.Set("setHeaders", func(value goja.Value) {
		reqState.headers = scriptHeadersToKeyValues(runtime, value)
		syncRequestHeaders()
	})
	_ = reqObject.Set("deleteHeader", func(name string) {
		reqState.headers = deleteKeyValue(reqState.headers, name)
		syncRequestHeaders()
	})
	_ = reqObject.Set("deleteHeaders", func(call goja.FunctionCall) goja.Value {
		for _, name := range scriptStringList(call.Argument(0)) {
			reqState.headers = deleteKeyValue(reqState.headers, name)
		}
		syncRequestHeaders()
		return goja.Undefined()
	})
	_ = reqObject.Set("setMaxRedirects", func(value int) {
		if value < 0 {
			value = 0
		}
		reqState.maxRedirectsSet = true
		reqState.maxRedirects = value
	})
	_ = reqObject.Set("getTimeout", func() int {
		if reqState.timeoutSet {
			return reqState.timeoutMs
		}
		return item.Settings.TimeoutMs
	})
	_ = reqObject.Set("setTimeout", func(value int) {
		if value < 0 {
			value = 0
		}
		reqState.timeoutSet = true
		reqState.timeoutMs = value
		_ = reqObject.Set("timeout", value)
	})
	_ = reqObject.Set("disableParsingResponseJson", func() {
		reqState.disableParsingResponseJSON = true
	})
	_ = reqObject.Set("getExecutionMode", func() string { return "single" })
	_ = reqObject.Set("setBody", func(call goja.FunctionCall) goja.Value {
		value := call.Argument(0)
		raw := false
		options := call.Argument(1)
		if options != nil && !goja.IsUndefined(options) && !goja.IsNull(options) {
			raw = options.ToObject(runtime).Get("raw").ToBoolean()
		}
		reqState.bodySet = true
		if !raw && scriptBodyIsFormURLEncoded(item, reqState.headers) {
			reqState.bodyValue = scriptFormURLEncodedValue(runtime, value)
			_ = reqObject.Set("body", reqState.bodyValue)
			return goja.Undefined()
		}
		reqState.bodyValue = value.Export()
		_ = reqObject.Set("body", value)
		return goja.Undefined()
	})
	_ = reqObject.Set("onFail", func(call goja.FunctionCall) goja.Value {
		if _, ok := goja.AssertFunction(call.Argument(0)); ok {
			reqState.onFail = call.Argument(0)
		}
		return goja.Undefined()
	})
	syncRequestHeaders()
	_ = reqObject.Set("headerList", newScriptHeaderListObject(runtime, func() []types.KeyValue { return reqState.headers }, func(next []types.KeyValue) {
		reqState.headers = next
		syncRequestHeaders()
	}, false))
	_ = runtime.Set("req", reqObject)

	responseHeaderRows := KeyValuesFromHeaders(response.Headers)
	responseHeaders := scriptResponseHeaders(response.Headers)
	responseBody := scriptResponseBody(response, item.Settings.DisableParsingResponseJSON)
	responseDataBuffer := scriptByteArrayValue(runtime, responseDataBytes(response))
	resValue := runtime.ToValue(func(call goja.FunctionCall) goja.Value {
		value, ok := scriptResponseQuery(runtime, responseBody, call)
		if !ok {
			return goja.Undefined()
		}
		return runtime.ToValue(value)
	})
	resObject := resValue.ToObject(runtime)
	_ = resObject.Set("status", response.Status)
	_ = resObject.Set("statusCode", response.Status)
	_ = resObject.Set("statusText", scalar.CleanStatusText(response.Status, response.StatusText))
	_ = resObject.Set("body", response.Body)
	_ = resObject.Set("data", responseBody)
	_ = resObject.Set("dataBuffer", responseDataBuffer)
	_ = resObject.Set("responseTime", response.DurationMs)
	_ = resObject.Set("url", response.RequestedURL)
	_ = resObject.Set("headers", responseHeaders)
	_ = resObject.Set("getHeader", func(name string) string { return getHeaderValue(response.Headers, name) })
	_ = resObject.Set("getHeaders", func() map[string]string { return responseHeaders })
	_ = resObject.Set("getStatus", func() int { return response.Status })
	_ = resObject.Set("getStatusText", func() string { return scalar.CleanStatusText(response.Status, response.StatusText) })
	_ = resObject.Set("getBody", func() interface{} { return responseBody })
	_ = resObject.Set("getResponseTime", func() int64 { return response.DurationMs })
	_ = resObject.Set("getUrl", func() string { return response.RequestedURL })
	_ = resObject.Set("getSize", func() map[string]int { return scriptResponseSize(response, responseHeaders) })
	_ = resObject.Set("getDataBuffer", func() goja.Value { return responseDataBuffer })
	_ = resObject.Set("jq", func(expr string) goja.Value {
		value, ok := scriptResponseJQ(responseBody, expr)
		if !ok {
			return goja.Null()
		}
		return runtime.ToValue(value)
	})
	_ = resObject.Set("setBody", func(value goja.Value) {
		responseBody = scriptBodyValue(value)
		bodyText := scriptRawBody(responseBody)
		bodyBytes := []byte(bodyText)
		response.Body = bodyText
		response.BodyBase64 = base64.StdEncoding.EncodeToString(bodyBytes)
		response.Size = len(bodyBytes)
		responseDataBuffer = scriptByteArrayValue(runtime, bodyBytes)
		_ = resObject.Set("data", responseBody)
		_ = resObject.Set("body", bodyText)
		_ = resObject.Set("dataBuffer", responseDataBuffer)
		if jsonValue, ok := responseJSONValue(bodyText); ok {
			_ = resObject.Set("json", jsonValue)
		} else {
			_ = resObject.Delete("json")
		}
	})
	_ = resObject.Set("headerList", newScriptHeaderListObject(runtime, func() []types.KeyValue { return responseHeaderRows }, nil, true))
	if !item.Settings.DisableParsingResponseJSON {
		if jsonValue, ok := responseJSONValue(response.Body); ok {
			_ = resObject.Set("json", jsonValue)
		}
	}
	_ = runtime.Set("res", resValue)

	bruObject := runtime.NewObject()
	scriptScopedValue := func(value interface{}) goja.Value {
		if value == nil {
			return goja.Undefined()
		}
		if text, ok := value.(string); ok {
			return runtime.ToValue(interp.Interpolate(text, vars))
		}
		return runtime.ToValue(value)
	}
	scriptGetScopedVar := func(scope map[string]interface{}, name string) goja.Value {
		if value, ok := scope[name]; ok {
			return scriptScopedValue(value)
		}
		return goja.Undefined()
	}
	scriptGetOAuth2CredentialVar := func(name string) goja.Value {
		if meta.OAuth2CredentialVariables == nil {
			return goja.Undefined()
		}
		return scriptGetScopedVar(meta.OAuth2CredentialVariables(), name)
	}
	scriptSetScopedVar := func(scope map[string]interface{}, name string, value goja.Value, dirty *bool, emptyMessage string) {
		if strings.TrimSpace(name) == "" {
			panic(runtime.NewGoError(errors.New(emptyMessage)))
		}
		scope[name] = scriptBodyValue(value)
		*dirty = true
		scriptVars.Recompute()
	}
	scriptDeleteScopedVar := func(scope map[string]interface{}, name string, dirty *bool) {
		if _, ok := scope[name]; ok {
			delete(scope, name)
			*dirty = true
			scriptVars.Recompute()
		}
	}
	scriptHasScopedVar := func(scope map[string]interface{}, name string) bool {
		_, ok := scope[name]
		return ok
	}
	scriptAllScopedVars := func(scope map[string]interface{}) map[string]interface{} {
		out := map[string]interface{}{}
		for name, value := range scope {
			out[name] = value
		}
		return out
	}
	scriptDeleteAllScopedVars := func(scope map[string]interface{}, dirty *bool) {
		if len(scope) == 0 {
			return
		}
		for name := range scope {
			delete(scope, name)
		}
		*dirty = true
		scriptVars.Recompute()
	}
	scriptMetaValue := func(value string) goja.Value {
		if value == "" {
			return goja.Undefined()
		}
		return runtime.ToValue(value)
	}
	_ = bruObject.Set("cwd", func() goja.Value { return scriptMetaValue(meta.CollectionPath) })
	_ = bruObject.Set("getEnvName", func() goja.Value { return scriptMetaValue(meta.EnvironmentName) })
	_ = bruObject.Set("getCollectionName", func() goja.Value { return scriptMetaValue(meta.CollectionName) })
	_ = bruObject.Set("isSafeMode", func() bool { return sandboxMode != "developer" })
	_ = bruObject.Set("hasVar", func(name string) bool { return scriptHasScopedVar(scriptVars.Runtime, name) })
	_ = bruObject.Set("getVar", func(name string) goja.Value { return scriptGetScopedVar(scriptVars.Runtime, name) })
	_ = bruObject.Set("setVar", func(name string, value goja.Value) {
		scriptSetScopedVar(scriptVars.Runtime, name, value, &scriptVars.RuntimeDirty, "Creating a variable without specifying a name is not allowed.")
	})
	_ = bruObject.Set("deleteVar", func(name string) { scriptDeleteScopedVar(scriptVars.Runtime, name, &scriptVars.RuntimeDirty) })
	_ = bruObject.Set("deleteAllVars", func() { scriptDeleteAllScopedVars(scriptVars.Runtime, &scriptVars.RuntimeDirty) })
	_ = bruObject.Set("getAllVars", func() map[string]interface{} { return scriptAllScopedVars(scriptVars.Runtime) })
	_ = bruObject.Set("getProcessEnv", func(name string) goja.Value {
		if value, ok := scriptVars.ProcessEnv[name]; ok {
			return runtime.ToValue(value)
		}
		return goja.Undefined()
	})
	_ = bruObject.Set("hasEnvVar", func(name string) bool { return scriptHasScopedVar(scriptVars.Env, name) })
	_ = bruObject.Set("getEnvVar", func(name string) goja.Value { return scriptGetScopedVar(scriptVars.Env, name) })
	_ = bruObject.Set("setEnvVar", func(name string, value goja.Value) {
		scriptSetScopedVar(scriptVars.Env, name, value, &scriptVars.EnvDirty, "Creating a env variable without specifying a name is not allowed.")
	})
	_ = bruObject.Set("deleteEnvVar", func(name string) { scriptDeleteScopedVar(scriptVars.Env, name, &scriptVars.EnvDirty) })
	_ = bruObject.Set("getAllEnvVars", func() map[string]interface{} { return scriptAllScopedVars(scriptVars.Env) })
	_ = bruObject.Set("deleteAllEnvVars", func() { scriptDeleteAllScopedVars(scriptVars.Env, &scriptVars.EnvDirty) })
	_ = bruObject.Set("hasGlobalEnvVar", func(name string) bool { return scriptHasScopedVar(scriptVars.Global, name) })
	_ = bruObject.Set("getGlobalEnvVar", func(name string) goja.Value { return scriptGetScopedVar(scriptVars.Global, name) })
	_ = bruObject.Set("setGlobalEnvVar", func(name string, value goja.Value) {
		scriptSetScopedVar(scriptVars.Global, name, value, &scriptVars.GlobalDirty, "Creating a env variable without specifying a name is not allowed.")
	})
	_ = bruObject.Set("deleteGlobalEnvVar", func(name string) { scriptDeleteScopedVar(scriptVars.Global, name, &scriptVars.GlobalDirty) })
	_ = bruObject.Set("getAllGlobalEnvVars", func() map[string]interface{} { return scriptAllScopedVars(scriptVars.Global) })
	_ = bruObject.Set("deleteAllGlobalEnvVars", func() { scriptDeleteAllScopedVars(scriptVars.Global, &scriptVars.GlobalDirty) })
	_ = bruObject.Set("hasCollectionVar", func(name string) bool { return scriptHasScopedVar(scriptVars.Collection, name) })
	_ = bruObject.Set("getCollectionVar", func(name string) goja.Value { return scriptGetScopedVar(scriptVars.Collection, name) })
	_ = bruObject.Set("setCollectionVar", func(name string, value goja.Value) {
		scriptSetScopedVar(scriptVars.Collection, name, value, &scriptVars.CollectionDirty, "Creating a variable without specifying a name is not allowed.")
	})
	_ = bruObject.Set("deleteCollectionVar", func(name string) { scriptDeleteScopedVar(scriptVars.Collection, name, &scriptVars.CollectionDirty) })
	_ = bruObject.Set("deleteAllCollectionVars", func() { scriptDeleteAllScopedVars(scriptVars.Collection, &scriptVars.CollectionDirty) })
	_ = bruObject.Set("getAllCollectionVars", func() map[string]interface{} { return scriptAllScopedVars(scriptVars.Collection) })
	_ = bruObject.Set("getFolderVar", func(name string) goja.Value { return scriptGetScopedVar(scriptVars.Folder, name) })
	_ = bruObject.Set("getRequestVar", func(name string) goja.Value { return scriptGetScopedVar(scriptVars.Request, name) })
	_ = bruObject.Set("getSecretVar", func(name string) goja.Value { return scriptGetScopedVar(scriptVars.CombinedInterface(), name) })
	_ = bruObject.Set("getOauth2CredentialVar", scriptGetOAuth2CredentialVar)
	_ = bruObject.Set("resetOauth2Credential", func(credentialsID string) {
		if meta.ResetOAuth2Credential == nil {
			return
		}
		if err := meta.ResetOAuth2Credential(credentialsID); err != nil {
			panic(runtime.NewGoError(err))
		}
	})
	_ = bruObject.Set("interpolate", func(value string) string { return interp.Interpolate(value, vars) })
	utilsObject := runtime.NewObject()
	_ = utilsObject.Set("minifyJson", func(call goja.FunctionCall) goja.Value {
		return scriptMinifyJSON(runtime, call.Argument(0))
	})
	_ = utilsObject.Set("minifyXml", func(call goja.FunctionCall) goja.Value {
		return scriptMinifyXML(runtime, call.Argument(0))
	})
	_ = bruObject.Set("utils", utilsObject)
	_ = bruObject.Set("sleep", func(ms int) goja.Value {
		if ms < 0 {
			return scriptResolvedPromise(runtime, goja.Undefined())
		}
		if ms > 1000 {
			ms = 1000
		}
		time.Sleep(time.Duration(ms) * time.Millisecond)
		return scriptResolvedPromise(runtime, goja.Undefined())
	})
	_ = bruObject.Set("sendRequest", func(call goja.FunctionCall) goja.Value {
		responseValue, errorValue, timelineEntry, err := scriptSendRequest(runtime, call.Argument(0), vars)
		if timelineEntry != nil && meta.RecordTimeline != nil {
			entry := *timelineEntry
			entry.ID = scalar.NewID("timeline")
			entry.Kind = "scripted-request"
			entry.Source = "sendRequest"
			entry.Phase = scalar.FirstNonEmpty(meta.TimelinePhase, "pre-request")
			entry.RequestID = item.ID
			entry.SourceFile = TimelineSourceFileForItem(meta.CollectionPath, item)
			if entry.Message == "" {
				statusLabel := entry.StatusText
				if entry.Status > 0 {
					statusLabel = fmt.Sprintf("%d", entry.Status)
				}
				entry.Message = strings.TrimSpace(fmt.Sprintf("%s %s -> %s", entry.Method, entry.URL, statusLabel))
			}
			meta.RecordTimeline(entry)
		}
		callback, hasCallback := goja.AssertFunction(call.Argument(1))
		if hasCallback {
			if err != nil {
				_, callbackErr := callback(goja.Undefined(), runtime.NewGoError(err), goja.Null())
				if callbackErr != nil {
					panic(callbackErr)
				}
				return goja.Undefined()
			}
			if errorValue != nil {
				_, callbackErr := callback(goja.Undefined(), errorValue, goja.Null())
				if callbackErr != nil {
					panic(callbackErr)
				}
				return goja.Undefined()
			}
			_, callbackErr := callback(goja.Undefined(), goja.Null(), responseValue)
			if callbackErr != nil {
				panic(callbackErr)
			}
			return responseValue
		}
		if err != nil {
			panic(runtime.NewGoError(err))
		}
		if errorValue != nil {
			panic(errorValue)
		}
		return responseValue
	})
	if meta.RunRequest != nil {
		_ = bruObject.Set("runRequest", func(call goja.FunctionCall) goja.Value {
			target := ""
			if len(call.Arguments) > 0 && !goja.IsUndefined(call.Argument(0)) && !goja.IsNull(call.Argument(0)) {
				target = call.Argument(0).String()
			}
			response, timelineEntry, err := meta.RunRequest(target)
			if timelineEntry != nil && meta.RecordTimeline != nil {
				entry := *timelineEntry
				entry.ID = scalar.NewID("timeline")
				entry.Kind = "scripted-request"
				entry.Source = "runRequest"
				entry.Phase = scalar.FirstNonEmpty(meta.TimelinePhase, "pre-request")
				if entry.Message == "" {
					statusLabel := entry.StatusText
					if entry.Status > 0 {
						statusLabel = fmt.Sprintf("%d", entry.Status)
					}
					entry.Message = strings.TrimSpace(fmt.Sprintf("%s %s -> %s", entry.Method, entry.URL, statusLabel))
				}
				meta.RecordTimeline(entry)
			}
			if err != nil {
				return scriptRejectedPromise(runtime, map[string]interface{}{"message": err.Error()})
			}
			return scriptResolvedPromise(runtime, scriptResponseObject(runtime, response))
		})
	}
	scriptSetNextRequest := func(value goja.Value) {
		if goja.IsUndefined(value) {
			reqState.nextRequestSet = false
			reqState.nextRequestName = nil
			return
		}
		reqState.nextRequestSet = true
		if goja.IsNull(value) {
			reqState.nextRequestName = nil
			return
		}
		nextRequestName := fmt.Sprint(value.Export())
		reqState.nextRequestName = &nextRequestName
	}
	_ = bruObject.Set("setNextRequest", func(call goja.FunctionCall) goja.Value {
		scriptSetNextRequest(call.Argument(0))
		return goja.Undefined()
	})
	runnerObject := runtime.NewObject()
	_ = runnerObject.Set("skipRequest", func() {
		reqState.SkipRequest = true
	})
	_ = runnerObject.Set("stopExecution", func() {
		reqState.stopExecution = true
	})
	_ = runnerObject.Set("setNextRequest", func(call goja.FunctionCall) goja.Value {
		scriptSetNextRequest(call.Argument(0))
		return goja.Undefined()
	})
	_ = bruObject.Set("runner", runnerObject)
	_ = bruObject.Set("cookies", newScriptCookiesObject(runtime, jar, currentRequestURL, func(value string) string {
		return interp.Interpolate(value, vars)
	}))
	_ = runtime.Set("bru", bruObject)

	_ = runtime.Set("console", newScriptConsoleObject(runtime, scriptLogs))
	_ = runtime.Set("expect", newExpectFactory(runtime))
	assert := runtime.NewObject()
	_ = assert.Set("equal", func(call goja.FunctionCall) goja.Value {
		actual := call.Argument(0)
		expected := call.Argument(1)
		if !actual.StrictEquals(expected) {
			panic(runtime.NewGoError(fmt.Errorf("expected %s to equal %s", actual.String(), expected.String())))
		}
		return runtime.ToValue(true)
	})
	_ = assert.Set("ok", func(call goja.FunctionCall) goja.Value {
		if !call.Argument(0).ToBoolean() {
			panic(runtime.NewGoError(errors.New("expected value to be truthy")))
		}
		return runtime.ToValue(true)
	})
	_ = runtime.Set("assert", assert)
	installScriptRequire(runtime, meta.CollectionPath, sandboxMode)
	_ = runtime.Set("test", func(call goja.FunctionCall) goja.Value {
		name := strings.TrimSpace(call.Argument(0).String())
		if name == "" {
			name = "script test"
		}
		fn, ok := goja.AssertFunction(call.Argument(1))
		if !ok {
			if testResults != nil {
				*testResults = append(*testResults, types.TestResult{Name: name, Passed: false, Message: "test body is not a function"})
			}
			return goja.Undefined()
		}
		result, err := fn(goja.Undefined())
		if err != nil {
			if testResults != nil {
				*testResults = append(*testResults, types.TestResult{Name: name, Passed: false, Message: err.Error()})
				return goja.Undefined()
			}
			panic(err)
		}
		if scriptAttachAsyncTestResult(runtime, result, testResults, name) {
			return goja.Undefined()
		}
		if testResults != nil {
			*testResults = append(*testResults, types.TestResult{Name: name, Passed: true, Message: "passed"})
			return goja.Undefined()
		}
		return goja.Undefined()
	})
	installPostmanScriptAPI(runtime, bruObject, reqObject, resObject, scriptVars, reqState, response, item, meta)
	return runtime, reqObject, reqState, resObject
}

func scriptMinifyJSON(runtime *goja.Runtime, value goja.Value) goja.Value {
	if goja.IsUndefined(value) || goja.IsNull(value) {
		panic(runtime.NewGoError(errors.New("failed to minify")))
	}

	if text, ok := value.Export().(string); ok {
		trimmed := strings.TrimSpace(text)
		if trimmed == "" {
			return runtime.ToValue(trimmed)
		}
		var decoded interface{}
		if err := json.Unmarshal([]byte(trimmed), &decoded); err != nil {
			panic(runtime.NewGoError(fmt.Errorf("failed to minify: %s", err.Error())))
		}
		raw, err := json.Marshal(decoded)
		if err != nil {
			panic(runtime.NewGoError(fmt.Errorf("failed to minify: %s", err.Error())))
		}
		return runtime.ToValue(string(raw))
	}

	exportType := value.ExportType()
	if exportType != nil {
		switch exportType.Kind() {
		case reflect.Array, reflect.Map, reflect.Slice, reflect.Struct:
			raw, err := json.Marshal(value.Export())
			if err != nil {
				panic(runtime.NewGoError(fmt.Errorf("failed to minify: %s", err.Error())))
			}
			return runtime.ToValue(string(raw))
		}
	}

	panic(runtime.NewTypeError("minifyJson expects a string or object"))
}

func scriptStringSliceContains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func (counter *scriptYAMLLineCounter) setContent(content string) {
	counter.content = content
	counter.offsets = scriptYAMLLineOffsets(content)
}

func (counter *scriptYAMLLineCounter) linePos(offset int) (int, int) {
	if len(counter.offsets) == 0 {
		counter.offsets = []int{0}
	}
	if offset < 0 {
		offset = 0
	}
	if offset > len(counter.content) {
		offset = len(counter.content)
	}
	lineIndex := sort.Search(len(counter.offsets), func(index int) bool {
		return counter.offsets[index] > offset
	}) - 1
	if lineIndex < 0 {
		lineIndex = 0
	}
	return lineIndex + 1, offset - counter.offsets[lineIndex] + 1
}

func StringMapHasKey(values map[string]string, key string) bool {
	for name := range values {
		if strings.EqualFold(name, key) {
			return true
		}
	}
	return false
}

func scriptCallStringArgs(call goja.FunctionCall) []string {
	parts := make([]string, 0, len(call.Arguments))
	for _, arg := range call.Arguments {
		parts = append(parts, arg.String())
	}
	return parts
}

func firstNonEmptyEnv(names ...string) string {
	for _, name := range names {
		if value := os.Getenv(name); value != "" {
			return value
		}
	}
	return ""
}

func newScriptHTTPObject(runtime *goja.Runtime, eventsObject goja.Value, secure bool) goja.Value {
	_ = runtime.Set("__liteApiHTTPEventEmitter", eventsObject)
	_ = runtime.Set("__liteApiHTTPDefaultProtocol", map[bool]string{true: "https:", false: "http:"}[secure])
	script := `(function () {
  const Events = globalThis.__liteApiHTTPEventEmitter;
  const EventEmitter = Events.EventEmitter || Events;
  const defaultProtocol = globalThis.__liteApiHTTPDefaultProtocol;
  const STATUS_CODES = {
    100: "Continue", 101: "Switching Protocols", 102: "Processing",
    200: "OK", 201: "Created", 202: "Accepted", 203: "Non-Authoritative Information", 204: "No Content", 205: "Reset Content", 206: "Partial Content",
    300: "Multiple Choices", 301: "Moved Permanently", 302: "Found", 303: "See Other", 304: "Not Modified", 307: "Temporary Redirect", 308: "Permanent Redirect",
    400: "Bad Request", 401: "Unauthorized", 403: "Forbidden", 404: "Not Found", 405: "Method Not Allowed", 408: "Request Timeout", 409: "Conflict", 410: "Gone", 415: "Unsupported Media Type", 418: "I'm a Teapot", 429: "Too Many Requests",
    500: "Internal Server Error", 501: "Not Implemented", 502: "Bad Gateway", 503: "Service Unavailable", 504: "Gateway Timeout"
  };

  function isURL(value) {
    return value && typeof value === "object" && typeof value.href === "string" && typeof value.protocol === "string";
  }

  function cloneOptions(value) {
    if (!value || typeof value !== "object" || isURL(value)) return {};
    const out = {};
    for (const key of Object.keys(value)) out[key] = value[key];
    return out;
  }

  function normalizeHeaders(headers) {
    const out = {};
    if (!headers) return out;
    if (Array.isArray(headers)) {
      for (const pair of headers) {
        if (pair && pair.length >= 2) out[String(pair[0]).toLowerCase()] = String(pair[1]);
      }
      return out;
    }
    for (const key of Object.keys(headers)) {
      const value = headers[key];
      if (Array.isArray(value)) out[String(key).toLowerCase()] = value.map((item) => String(item)).join(", ");
      else if (value !== undefined && value !== null) out[String(key).toLowerCase()] = String(value);
    }
    return out;
  }

  function headersFromResponse(headers) {
    const out = {};
    if (!headers) return out;
    if (typeof headers.forEach === "function") {
      headers.forEach((value, key) => {
        out[String(key).toLowerCase()] = String(value);
      });
    } else if (typeof headers === "object") {
      for (const key of Object.keys(headers)) out[String(key).toLowerCase()] = String(headers[key]);
    }
    return out;
  }

  function rawHeaders(headers) {
    const out = [];
    for (const key of Object.keys(headers || {})) {
      out.push(key, String(headers[key]));
    }
    return out;
  }

  function normalizeURL(input, options) {
    const optionSource = cloneOptions(options);
    let baseOptions = {};
    let url;
    if (typeof input === "string" || isURL(input)) {
      url = new URL(String(input));
      baseOptions = cloneOptions(options);
    } else {
      baseOptions = cloneOptions(input);
      if (baseOptions.href || baseOptions.url) {
        url = new URL(String(baseOptions.href || baseOptions.url));
      }
    }
    const merged = Object.assign({}, baseOptions, optionSource);
    if (!url) {
      const protocol = merged.protocol ? String(merged.protocol) : defaultProtocol;
      const rawHost = merged.hostname || merged.host || "localhost";
      let hostname = String(rawHost);
      let port = merged.port === undefined || merged.port === null ? "" : String(merged.port);
      if (!merged.hostname && hostname.includes(":") && !hostname.startsWith("[")) {
        const parts = hostname.split(":");
        hostname = parts.shift();
        if (!port) port = parts.join(":");
      }
      const path = merged.path || ((merged.pathname || "/") + (merged.search || ""));
      const host = port ? hostname + ":" + port : hostname;
      url = new URL(protocol + "//" + host + String(path || "/"));
    }
    if (merged.protocol) url.protocol = String(merged.protocol);
    if (merged.hostname) url.hostname = String(merged.hostname);
    if (merged.host && !merged.hostname) {
      const parsed = new URL(url.protocol + "//" + String(merged.host));
      url.hostname = parsed.hostname;
      url.port = parsed.port;
    }
    if (merged.port !== undefined && merged.port !== null) url.port = String(merged.port);
    if (merged.path !== undefined) {
      const path = String(merged.path || "/");
      const queryIndex = path.indexOf("?");
      url.pathname = queryIndex === -1 ? path : path.slice(0, queryIndex);
      url.search = queryIndex === -1 ? "" : path.slice(queryIndex);
    } else {
      if (merged.pathname !== undefined) url.pathname = String(merged.pathname || "/");
      if (merged.search !== undefined) url.search = String(merged.search || "");
    }
    if (merged.auth) {
      const auth = String(merged.auth);
      const index = auth.indexOf(":");
      url.username = index === -1 ? auth : auth.slice(0, index);
      url.password = index === -1 ? "" : auth.slice(index + 1);
    }
    return {
      url,
      method: String(merged.method || "GET").toUpperCase(),
      headers: normalizeHeaders(merged.headers),
      timeout: merged.timeout
    };
  }

  function bodyChunk(value, encoding) {
    if (value === undefined || value === null) return Buffer.alloc(0);
    if (Buffer.isBuffer(value)) return value;
    if (value instanceof ArrayBuffer || ArrayBuffer.isView(value) || Array.isArray(value)) return Buffer.from(value);
    return Buffer.from(String(value), encoding || "utf8");
  }

  class IncomingMessage extends EventEmitter {
    constructor(response, body) {
      super();
      this.statusCode = Number(response.status || 0);
      this.statusMessage = response.statusText || STATUS_CODES[this.statusCode] || "";
      this.headers = headersFromResponse(response.headers);
      this.rawHeaders = rawHeaders(this.headers);
      this.url = response.url || "";
      this.method = "";
      this.complete = false;
      this.readable = true;
      this.readableEnded = false;
      this._body = Buffer.from(body || "");
      this._encoding = null;
    }
    setEncoding(encoding) {
      this._encoding = encoding || "utf8";
      return this;
    }
    _emitBody() {
      if (this._body.length > 0) {
        this.emit("data", this._encoding ? this._body.toString(this._encoding) : Buffer.from(this._body));
      }
      this.complete = true;
      this.readableEnded = true;
      this.readable = false;
      this.emit("end");
      this.emit("close");
    }
  }

  class ClientRequest extends EventEmitter {
    constructor(config, callback) {
      super();
      this.method = config.method;
      this.protocol = config.url.protocol;
      this.host = config.url.host;
      this.path = config.url.pathname + config.url.search;
      this.destroyed = false;
      this.finished = false;
      this.writableEnded = false;
      this._url = config.url;
      this._headers = Object.assign({}, config.headers);
      this._chunks = [];
      this._timeout = config.timeout;
      if (typeof callback === "function") this.on("response", callback);
    }
    setHeader(name, value) {
      this._headers[String(name).toLowerCase()] = String(value);
      return this;
    }
    getHeader(name) {
      return this._headers[String(name).toLowerCase()];
    }
    getHeaders() {
      return Object.assign({}, this._headers);
    }
    hasHeader(name) {
      return Object.prototype.hasOwnProperty.call(this._headers, String(name).toLowerCase());
    }
    removeHeader(name) {
      delete this._headers[String(name).toLowerCase()];
    }
    write(chunk, encoding, callback) {
      if (typeof encoding === "function") {
        callback = encoding;
        encoding = undefined;
      }
      this._chunks.push(bodyChunk(chunk, encoding));
      if (typeof callback === "function") callback();
      return true;
    }
    end(chunk, encoding, callback) {
      if (typeof chunk === "function") {
        callback = chunk;
        chunk = undefined;
        encoding = undefined;
      } else if (typeof encoding === "function") {
        callback = encoding;
        encoding = undefined;
      }
      if (chunk !== undefined && chunk !== null) this.write(chunk, encoding);
      this.finished = true;
      this.writableEnded = true;
      this.emit("finish");
      const requestBody = this._chunks.length === 0 ? undefined : Buffer.concat(this._chunks);
      const fetchConfig = {
        method: this.method,
        headers: this._headers
      };
      if (requestBody !== undefined && requestBody.length > 0) fetchConfig.body = requestBody;
      fetch(this._url.toString(), fetchConfig)
        .then((response) => response.text().then((body) => ({ response, body })))
        .then(({ response, body }) => {
          if (this.destroyed) return;
          const incoming = new IncomingMessage(response, body);
          incoming.method = this.method;
          this.emit("response", incoming);
          incoming._emitBody();
          if (typeof callback === "function") callback();
          this.emit("close");
        })
        .catch((err) => {
          if (this.destroyed) return;
          this.emit("error", err);
          this.emit("close");
        });
      return this;
    }
    abort() {
      this.destroy();
    }
    destroy(error) {
      if (this.destroyed) return this;
      this.destroyed = true;
      if (error) this.emit("error", error);
      this.emit("close");
      return this;
    }
    setTimeout(ms, callback) {
      this._timeout = ms;
      if (typeof callback === "function") this.once("timeout", callback);
      return this;
    }
    flushHeaders() {
      return this;
    }
  }

  function request(input, options, callback) {
    if (typeof options === "function") {
      callback = options;
      options = undefined;
    }
    const config = normalizeURL(input, options);
    return new ClientRequest(config, callback);
  }

  function get(input, options, callback) {
    const req = request(input, options, callback);
    req.end();
    return req;
  }

  function validateHeaderName(name) {
    const value = String(name || "");
    if (!/^[!#$%&'*+\-.^_` + "`" + `|~0-9A-Za-z]+$/.test(value)) {
      throw new TypeError("Header name must be a valid HTTP token");
    }
  }

  function validateHeaderValue(name, value) {
    if (value === undefined) {
      throw new TypeError('Invalid value "' + value + '" for header "' + name + '"');
    }
  }

  return {
    request,
    get,
    ClientRequest,
    IncomingMessage,
    Agent: class Agent {},
    globalAgent: {},
    METHODS: ["GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS", "TRACE", "CONNECT"],
    STATUS_CODES,
    validateHeaderName,
    validateHeaderValue,
    createServer: function () { throw new Error("http.createServer is not available in LiteAPI scripts"); }
  };
})()`
	value, err := runtime.RunProgram(scriptHTTPShim.compiled(script))
	if err != nil {
		panic(runtime.NewGoError(err))
	}
	_ = runtime.Set("__liteApiHTTPEventEmitter", goja.Undefined())
	_ = runtime.Set("__liteApiHTTPDefaultProtocol", goja.Undefined())
	return value
}

func newScriptMomentObject(runtime *goja.Runtime) goja.Value {
	script := `(function () {
  const monthNames = ["January", "February", "March", "April", "May", "June", "July", "August", "September", "October", "November", "December"];
  const shortMonthNames = monthNames.map(function (name) { return name.slice(0, 3); });
  const dayNames = ["Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"];
  const shortDayNames = dayNames.map(function (name) { return name.slice(0, 3); });

  function pad(value, size) {
    let text = String(Math.abs(Math.trunc(Number(value) || 0)));
    while (text.length < size) {
      text = "0" + text;
    }
    return text;
  }

  function normalizeUnit(unit) {
    const raw = String(unit || "millisecond");
    if (raw === "M") return "month";
    const value = raw.toLowerCase();
    if (value === "years" || value === "year" || value === "y") return "year";
    if (value === "months" || value === "month" || value === "mth") return "month";
    if (value === "weeks" || value === "week" || value === "w") return "week";
    if (value === "days" || value === "day" || value === "d") return "day";
    if (value === "hours" || value === "hour" || value === "h") return "hour";
    if (value === "minutes" || value === "minute" || value === "mins" || value === "min" || value === "m") return "minute";
    if (value === "seconds" || value === "second" || value === "secs" || value === "sec" || value === "s") return "second";
    return "millisecond";
  }

  function durationMilliseconds(amount, unit) {
    const value = Number(amount) || 0;
    switch (normalizeUnit(unit)) {
      case "year": return value * 365 * 24 * 60 * 60 * 1000;
      case "month": return value * 30 * 24 * 60 * 60 * 1000;
      case "week": return value * 7 * 24 * 60 * 60 * 1000;
      case "day": return value * 24 * 60 * 60 * 1000;
      case "hour": return value * 60 * 60 * 1000;
      case "minute": return value * 60 * 1000;
      case "second": return value * 1000;
      default: return value;
    }
  }

  function parseDate(input, utc) {
    if (input instanceof Moment) {
      return new Date(input.valueOf());
    }
    if (input instanceof Date) {
      return new Date(input.getTime());
    }
    if (Array.isArray(input)) {
      const values = input.map(function (item) { return Number(item) || 0; });
      const year = values[0] || 0;
      const month = values.length > 1 ? values[1] : 0;
      const day = values.length > 2 ? values[2] : 1;
      const hour = values.length > 3 ? values[3] : 0;
      const minute = values.length > 4 ? values[4] : 0;
      const second = values.length > 5 ? values[5] : 0;
      const millisecond = values.length > 6 ? values[6] : 0;
      return utc ? new Date(Date.UTC(year, month, day, hour, minute, second, millisecond)) : new Date(year, month, day, hour, minute, second, millisecond);
    }
    if (input === undefined || input === null) {
      return new Date();
    }
    if (typeof input === "number") {
      return new Date(input);
    }
    const parsed = Date.parse(String(input));
    return Number.isNaN(parsed) ? new Date(NaN) : new Date(parsed);
  }

  function getPart(date, utc, part) {
    const prefix = utc ? "getUTC" : "get";
    return date[prefix + part]();
  }

  function setPart(date, utc, part, value) {
    const prefix = utc ? "setUTC" : "set";
    date[prefix + part](value);
  }

  function zoneOffset(date, utc, compact) {
    if (utc) {
      return compact ? "+0000" : "+00:00";
    }
    const offset = -date.getTimezoneOffset();
    const sign = offset >= 0 ? "+" : "-";
    const abs = Math.abs(offset);
    const text = pad(Math.floor(abs / 60), 2) + (compact ? "" : ":") + pad(abs % 60, 2);
    return sign + text;
  }

  function formatMoment(instance, format) {
    if (!instance.isValid()) {
      return "Invalid date";
    }
    const date = instance._d;
    const utc = instance._utc;
    const year = getPart(date, utc, "FullYear");
    const month = getPart(date, utc, "Month");
    const day = getPart(date, utc, "Date");
    const hour = getPart(date, utc, "Hours");
    const minute = getPart(date, utc, "Minutes");
    const second = getPart(date, utc, "Seconds");
    const millisecond = getPart(date, utc, "Milliseconds");
    const weekday = getPart(date, utc, "Day");
    const literals = [];
    let pattern = format || "YYYY-MM-DDTHH:mm:ssZ";
    pattern = pattern.replace(/\[([^\]]*)\]/g, function (_, literal) {
      literals.push(literal);
      return "\u0000" + (literals.length - 1) + "\u0000";
    });
    const replacements = {
      YYYY: pad(year, 4),
      YY: pad(year % 100, 2),
      MMMM: monthNames[month],
      MMM: shortMonthNames[month],
      MM: pad(month + 1, 2),
      M: String(month + 1),
      DD: pad(day, 2),
      D: String(day),
      HH: pad(hour, 2),
      H: String(hour),
      hh: pad(((hour + 11) % 12) + 1, 2),
      h: String(((hour + 11) % 12) + 1),
      mm: pad(minute, 2),
      m: String(minute),
      ss: pad(second, 2),
      s: String(second),
      SSS: pad(millisecond, 3),
      dddd: dayNames[weekday],
      ddd: shortDayNames[weekday],
      A: hour < 12 ? "AM" : "PM",
      a: hour < 12 ? "am" : "pm",
      ZZ: zoneOffset(date, utc, true),
      Z: zoneOffset(date, utc, false),
      X: String(Math.floor(date.getTime() / 1000)),
      x: String(date.getTime())
    };
    pattern = pattern.replace(/YYYY|YY|MMMM|MMM|MM|M|DD|D|HH|H|hh|h|mm|m|ss|s|SSS|dddd|ddd|A|a|ZZ|Z|X|x/g, function (token) {
      return replacements[token];
    });
    return pattern.replace(/\u0000(\d+)\u0000/g, function (_, index) {
      return literals[Number(index)];
    });
  }

  function addCalendar(date, utc, amount, unit) {
    const value = Number(amount) || 0;
    switch (normalizeUnit(unit)) {
      case "year":
        setPart(date, utc, "FullYear", getPart(date, utc, "FullYear") + value);
        return;
      case "month":
        setPart(date, utc, "Month", getPart(date, utc, "Month") + value);
        return;
      default:
        date.setTime(date.getTime() + durationMilliseconds(value, unit));
    }
  }

  function Moment(input, options) {
    this._isAMomentObject = true;
    this._utc = !!(options && options.utc);
    this._d = parseDate(input, this._utc);
  }

  Moment.prototype.clone = function () {
    return new Moment(this, { utc: this._utc });
  };
  Moment.prototype.isValid = function () {
    return this._d instanceof Date && !Number.isNaN(this._d.getTime());
  };
  Moment.prototype.utc = function () {
    this._utc = true;
    return this;
  };
  Moment.prototype.local = function () {
    this._utc = false;
    return this;
  };
  Moment.prototype.format = function (format) {
    return formatMoment(this, format);
  };
  Moment.prototype.toISOString = function () {
    return this.isValid() ? this._d.toISOString() : null;
  };
  Moment.prototype.toJSON = Moment.prototype.toISOString;
  Moment.prototype.toDate = function () {
    return new Date(this._d.getTime());
  };
  Moment.prototype.valueOf = function () {
    return this._d.getTime();
  };
  Moment.prototype.unix = function () {
    return Math.floor(this.valueOf() / 1000);
  };
  Moment.prototype.add = function (amount, unit) {
    if (amount && typeof amount === "object") {
      for (const key of Object.keys(amount)) {
        addCalendar(this._d, this._utc, amount[key], key);
      }
      return this;
    }
    addCalendar(this._d, this._utc, amount, unit);
    return this;
  };
  Moment.prototype.subtract = function (amount, unit) {
    if (amount && typeof amount === "object") {
      const inverse = {};
      for (const key of Object.keys(amount)) {
        inverse[key] = -Number(amount[key] || 0);
      }
      return this.add(inverse);
    }
    return this.add(-Number(amount || 0), unit);
  };
  Moment.prototype.startOf = function (unit) {
    const normalized = normalizeUnit(unit);
    if (normalized === "year") {
      setPart(this._d, this._utc, "Month", 0);
      setPart(this._d, this._utc, "Date", 1);
    }
    if (normalized === "month") {
      setPart(this._d, this._utc, "Date", 1);
    }
    if (normalized === "year" || normalized === "month" || normalized === "day") {
      setPart(this._d, this._utc, "Hours", 0);
    }
    if (normalized === "year" || normalized === "month" || normalized === "day" || normalized === "hour") {
      setPart(this._d, this._utc, "Minutes", 0);
    }
    if (normalized !== "millisecond") {
      setPart(this._d, this._utc, "Seconds", 0);
      setPart(this._d, this._utc, "Milliseconds", 0);
    }
    return this;
  };
  Moment.prototype.endOf = function (unit) {
    return this.startOf(unit).add(1, unit).subtract(1, "millisecond");
  };
  Moment.prototype.diff = function (other, unit, floating) {
    const delta = this.valueOf() - moment(other).valueOf();
    const divisor = durationMilliseconds(1, unit || "millisecond");
    const result = divisor === 0 ? delta : delta / divisor;
    return floating ? result : (result < 0 ? Math.ceil(result) : Math.floor(result));
  };
  Moment.prototype.isBefore = function (other) {
    return this.valueOf() < moment(other).valueOf();
  };
  Moment.prototype.isAfter = function (other) {
    return this.valueOf() > moment(other).valueOf();
  };
  Moment.prototype.isSame = function (other) {
    return this.valueOf() === moment(other).valueOf();
  };
  Moment.prototype.year = function (value) {
    if (value === undefined) return getPart(this._d, this._utc, "FullYear");
    setPart(this._d, this._utc, "FullYear", Number(value));
    return this;
  };
  Moment.prototype.month = function (value) {
    if (value === undefined) return getPart(this._d, this._utc, "Month");
    setPart(this._d, this._utc, "Month", Number(value));
    return this;
  };
  Moment.prototype.date = function (value) {
    if (value === undefined) return getPart(this._d, this._utc, "Date");
    setPart(this._d, this._utc, "Date", Number(value));
    return this;
  };
  Moment.prototype.hour = function (value) {
    if (value === undefined) return getPart(this._d, this._utc, "Hours");
    setPart(this._d, this._utc, "Hours", Number(value));
    return this;
  };
  Moment.prototype.minute = function (value) {
    if (value === undefined) return getPart(this._d, this._utc, "Minutes");
    setPart(this._d, this._utc, "Minutes", Number(value));
    return this;
  };
  Moment.prototype.second = function (value) {
    if (value === undefined) return getPart(this._d, this._utc, "Seconds");
    setPart(this._d, this._utc, "Seconds", Number(value));
    return this;
  };
  Moment.prototype.millisecond = function (value) {
    if (value === undefined) return getPart(this._d, this._utc, "Milliseconds");
    setPart(this._d, this._utc, "Milliseconds", Number(value));
    return this;
  };

  function Duration(value, unit) {
    if (value && typeof value === "object") {
      let total = 0;
      for (const key of Object.keys(value)) {
        total += durationMilliseconds(value[key], key);
      }
      this._ms = total;
    } else {
      this._ms = durationMilliseconds(value || 0, unit);
    }
  }
  Duration.prototype.asMilliseconds = function () { return this._ms; };
  Duration.prototype.asSeconds = function () { return this._ms / 1000; };
  Duration.prototype.asMinutes = function () { return this._ms / 60000; };
  Duration.prototype.asHours = function () { return this._ms / 3600000; };
  Duration.prototype.asDays = function () { return this._ms / 86400000; };
  Duration.prototype.milliseconds = function () { return this._ms % 1000; };
  Duration.prototype.seconds = function () { return Math.floor(this._ms / 1000) % 60; };
  Duration.prototype.minutes = function () { return Math.floor(this._ms / 60000) % 60; };
  Duration.prototype.hours = function () { return Math.floor(this._ms / 3600000) % 24; };
  Duration.prototype.days = function () { return Math.floor(this._ms / 86400000); };
  Duration.prototype.humanize = function () {
    const abs = Math.abs(this._ms);
    if (abs < 45000) return "a few seconds";
    if (abs < 90 * 60000) return "a minute";
    if (abs < 36 * 3600000) return Math.round(abs / 3600000) + " hours";
    return Math.round(abs / 86400000) + " days";
  };

  function moment(input) {
    return new Moment(input, { utc: false });
  }
  moment.utc = function (input) {
    return new Moment(input, { utc: true });
  };
  moment.unix = function (seconds) {
    return new Moment(Number(seconds) * 1000, { utc: false });
  };
  moment.duration = function (value, unit) {
    return new Duration(value, unit);
  };
  moment.isMoment = function (value) {
    return value instanceof Moment || !!(value && value._isAMomentObject);
  };
  moment.isDate = function (value) {
    return value instanceof Date;
  };
  moment.ISO_8601 = "ISO_8601";
  moment.version = "2.29.4-liteapi";
  moment.fn = Moment.prototype;
  return moment;
})()`
	value, err := runtime.RunProgram(scriptMomentShim.compiled(script))
	if err != nil {
		panic(runtime.NewGoError(err))
	}
	return value
}

func scriptOptionalArgument(args []goja.Value, index int) goja.Value {
	if index < 0 || index >= len(args) {
		return goja.Undefined()
	}
	return args[index]
}

func scriptOptionalCallback(args []goja.Value) (goja.Callable, int) {
	for index := 2; index < len(args); index++ {
		if fn, ok := goja.AssertFunction(args[index]); ok {
			return fn, index
		}
	}
	return nil, -1
}

func callScriptCallback(runtime *goja.Runtime, callback goja.Callable, callbackErr error, value goja.Value) {
	if callbackErr != nil {
		_, err := callback(goja.Undefined(), runtime.NewGoError(callbackErr), goja.Undefined())
		if err != nil {
			panic(err)
		}
		return
	}
	_, err := callback(goja.Undefined(), goja.Null(), value)
	if err != nil {
		panic(err)
	}
}

func decodeScriptBase64(value string) ([]byte, error) {
	normalized := strings.TrimSpace(value)
	if decoded, err := base64.StdEncoding.DecodeString(normalized); err == nil {
		return decoded, nil
	}
	if decoded, err := base64.RawStdEncoding.DecodeString(normalized); err == nil {
		return decoded, nil
	}
	if decoded, err := base64.RawURLEncoding.DecodeString(normalized); err == nil {
		return decoded, nil
	}
	return base64.URLEncoding.DecodeString(normalized)
}

func scriptBinaryStringFromBytes(bytes []byte) string {
	chars := make([]rune, len(bytes))
	for index, value := range bytes {
		chars[index] = rune(value)
	}
	return string(chars)
}

func scriptBytesFromBinaryString(value string) ([]byte, error) {
	bytes := make([]byte, 0, len(value))
	for _, char := range value {
		if char > 255 {
			return nil, fmt.Errorf("btoa character code %d is outside the Latin-1 range", char)
		}
		bytes = append(bytes, byte(char))
	}
	return bytes, nil
}

func scriptLogValueString(value goja.Value) string {
	if value == nil || goja.IsUndefined(value) {
		return "undefined"
	}
	if goja.IsNull(value) {
		return "null"
	}
	exported := value.Export()
	switch typed := exported.(type) {
	case string:
		return typed
	case fmt.Stringer:
		return typed.String()
	}
	if encoded, err := json.Marshal(exported); err == nil {
		return string(encoded)
	}
	return value.String()
}

func scriptCallbackThis(value goja.Value) goja.Value {
	if value == nil || goja.IsUndefined(value) {
		return goja.Undefined()
	}
	return value
}

func scriptValueIsCallable(value goja.Value) bool {
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		return false
	}
	_, ok := goja.AssertFunction(value)
	return ok
}

func scriptValueString(value goja.Value) string {
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		return ""
	}
	return value.String()
}

func firstScriptObjectValue(object *goja.Object, keys ...string) goja.Value {
	for _, key := range keys {
		value := object.Get(key)
		if value != nil && !goja.IsUndefined(value) && !goja.IsNull(value) {
			return value
		}
	}
	return goja.Undefined()
}

func scriptMapString(data map[string]interface{}, key string) string {
	value, ok := data[key]
	if !ok || value == nil {
		return ""
	}
	return fmt.Sprint(value)
}

func scriptMapBool(data map[string]interface{}, key string) bool {
	value, ok := data[key]
	return ok && truthyInterface(value)
}

func truthyInterface(value interface{}) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		return strings.EqualFold(typed, "true")
	default:
		return fmt.Sprint(value) == "true"
	}
}

func newExpectFactory(runtime *goja.Runtime) func(goja.Value) *goja.Object {
	return func(actual goja.Value) *goja.Object {
		return expectMatcher(runtime, actual, false)
	}
}

func isJSONBodyObjectArgument(value goja.Value) bool {
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		return false
	}
	exported := value.Export()
	if exported == nil {
		return false
	}
	kind := reflect.ValueOf(exported).Kind()
	return kind == reflect.Map || kind == reflect.Slice || kind == reflect.Array
}

func isJSONObjectOrArray(value interface{}) bool {
	if value == nil {
		return false
	}
	kind := reflect.ValueOf(value).Kind()
	return kind == reflect.Map || kind == reflect.Slice || kind == reflect.Array
}

func jsonBodyNestedValue(value interface{}, path string) (interface{}, bool) {
	keys := parseJSONBodyPath(path)
	current := value
	for _, key := range keys {
		switch typed := current.(type) {
		case map[string]interface{}:
			next, ok := typed[key]
			if !ok {
				return nil, false
			}
			current = next
		case []interface{}:
			index, err := strconv.Atoi(key)
			if err != nil || index < 0 || index >= len(typed) {
				return nil, false
			}
			current = typed[index]
		default:
			return nil, false
		}
	}
	return current, true
}

func normalizeJSONValue(value interface{}) (interface{}, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var normalized interface{}
	if err := json.Unmarshal(data, &normalized); err != nil {
		return nil, err
	}
	return normalized, nil
}

func installScriptEventLoop(runtime *goja.Runtime, sandboxMode string) *scriptEventLoop {
	loop := &scriptEventLoop{
		runtime: runtime,
		timers:  map[int64]*scriptTimer{},
	}
	scriptRuntimeEventLoops.Store(runtime, loop)
	setTimeout := func(call goja.FunctionCall) goja.Value {
		return loop.scheduleTimer(call.Argument(0), call.Argument(1), false, call.Arguments[2:]...)
	}
	clearTimeout := func(call goja.FunctionCall) goja.Value {
		loop.clearTimer(call.Argument(0))
		return goja.Undefined()
	}
	setInterval := func(call goja.FunctionCall) goja.Value {
		return loop.scheduleTimer(call.Argument(0), call.Argument(1), true, call.Arguments[2:]...)
	}
	setImmediate := func(call goja.FunctionCall) goja.Value {
		return loop.scheduleTimer(call.Argument(0), runtime.ToValue(0), false, call.Arguments[1:]...)
	}
	queueMicrotask := func(call goja.FunctionCall) goja.Value {
		loop.queueNextTick(call.Argument(0))
		return goja.Undefined()
	}
	_ = runtime.Set("__bruSetTimeout", setTimeout)
	if NormalizeJSSandboxMode(sandboxMode) == "developer" {
		_ = runtime.Set("setTimeout", setTimeout)
		_ = runtime.Set("clearTimeout", clearTimeout)
		_ = runtime.Set("setInterval", setInterval)
		_ = runtime.Set("clearInterval", clearTimeout)
		_ = runtime.Set("setImmediate", setImmediate)
		_ = runtime.Set("clearImmediate", clearTimeout)
		_ = runtime.Set("queueMicrotask", queueMicrotask)
	}
	return loop
}

func scriptNodePlatform() string {
	if goruntime.GOOS == "windows" {
		return "win32"
	}
	return goruntime.GOOS
}

func scriptNodeArch() string {
	switch goruntime.GOARCH {
	case "amd64":
		return "x64"
	case "386":
		return "ia32"
	default:
		return goruntime.GOARCH
	}
}

func installScriptEncoding(runtime *goja.Runtime) {
	script := `(function () {
  function normalizeEncoding(label) {
    return String(label || "utf-8").toLowerCase().replace(/[_\s]/g, "-");
  }
  function utf8Bytes(value) {
    const encoded = encodeURIComponent(String(value));
    const out = [];
    for (let i = 0; i < encoded.length; i++) {
      const ch = encoded[i];
      if (ch === "%") {
        out.push(parseInt(encoded.slice(i + 1, i + 3), 16));
        i += 2;
      } else {
        out.push(ch.charCodeAt(0));
      }
    }
    return out;
  }
  function utf8String(bytes, fatal) {
    let encoded = "";
    for (let i = 0; i < bytes.length; i++) {
      const value = Number(bytes[i]) & 255;
      if (value < 0x80) {
        encoded += String.fromCharCode(value);
      } else {
        encoded += "%" + value.toString(16).padStart(2, "0").toUpperCase();
      }
    }
    try {
      return decodeURIComponent(encoded);
    } catch (err) {
      if (fatal) {
        throw new TypeError("The encoded data was not valid utf-8");
      }
      let fallback = "";
      for (let i = 0; i < bytes.length; i++) {
        fallback += String.fromCharCode(Number(bytes[i]) & 255);
      }
      return fallback;
    }
  }
  function bytesFromValue(value) {
    if (value === undefined) {
      return [];
    }
    if (value instanceof ArrayBuffer) {
      return Array.from(new Uint8Array(value));
    }
    if (ArrayBuffer.isView(value)) {
      return Array.from(new Uint8Array(value.buffer, value.byteOffset, value.byteLength));
    }
    throw new TypeError("TextDecoder.decode input must be an ArrayBuffer or ArrayBufferView");
  }
  class TextEncoder {
    constructor() {
      Object.defineProperty(this, "encoding", { value: "utf-8", enumerable: true });
    }
    encode(input) {
      return new Uint8Array(utf8Bytes(input === undefined ? "" : input));
    }
    encodeInto(input, destination) {
      if (!ArrayBuffer.isView(destination)) {
        throw new TypeError("TextEncoder.encodeInto destination must be an ArrayBufferView");
      }
      const text = String(input === undefined ? "" : input);
      let read = 0;
      let written = 0;
      for (let index = 0; index < text.length;) {
        const codePoint = text.codePointAt(index);
        const chunk = String.fromCodePoint(codePoint);
        const bytes = utf8Bytes(chunk);
        if (written + bytes.length > destination.length) {
          break;
        }
        for (let i = 0; i < bytes.length; i++) {
          destination[written + i] = bytes[i];
        }
        written += bytes.length;
        index += chunk.length;
        read = index;
      }
      return { read, written };
    }
  }
  class TextDecoder {
    constructor(label, options) {
      const encoding = normalizeEncoding(label);
      if (encoding !== "utf-8" && encoding !== "utf8") {
        throw new RangeError("Unsupported TextDecoder encoding: " + label);
      }
      options = options || {};
      Object.defineProperty(this, "encoding", { value: "utf-8", enumerable: true });
      Object.defineProperty(this, "fatal", { value: !!options.fatal, enumerable: true });
      Object.defineProperty(this, "ignoreBOM", { value: !!options.ignoreBOM, enumerable: true });
    }
    decode(input) {
      return utf8String(bytesFromValue(input), this.fatal);
    }
  }
  globalThis.TextEncoder = TextEncoder;
  globalThis.TextDecoder = TextDecoder;
})()`
	if _, err := runtime.RunProgram(scriptEncodingShim.compiled(script)); err != nil {
		panic(runtime.NewGoError(err))
	}
}

func scriptEventLoopForRuntime(runtime *goja.Runtime) *scriptEventLoop {
	if runtime == nil {
		return nil
	}
	value, ok := scriptRuntimeEventLoops.Load(runtime)
	if !ok {
		return nil
	}
	loop, _ := value.(*scriptEventLoop)
	return loop
}

func (loop *scriptEventLoop) timerHandle(id int64, promise *goja.Promise) goja.Value {
	handle := loop.runtime.NewObject()
	promiseObject := loop.runtime.ToValue(promise).ToObject(loop.runtime)
	_ = handle.Set("__bruTimerID", id)
	_ = handle.Set("id", id)
	_ = handle.Set("hasRef", func() bool { return true })
	_ = handle.Set("ref", func() goja.Value { return handle })
	_ = handle.Set("unref", func() goja.Value { return handle })
	_ = handle.Set("valueOf", func() int64 { return id })
	_ = handle.Set("toString", func() string { return strconv.FormatInt(id, 10) })
	_ = handle.Set("then", func(call goja.FunctionCall) goja.Value {
		then, ok := goja.AssertFunction(promiseObject.Get("then"))
		if !ok {
			return goja.Undefined()
		}
		result, err := then(promiseObject, call.Arguments...)
		if err != nil {
			panic(err)
		}
		return result
	})
	_ = handle.Set("catch", func(call goja.FunctionCall) goja.Value {
		catchFn, ok := goja.AssertFunction(promiseObject.Get("catch"))
		if !ok {
			return goja.Undefined()
		}
		result, err := catchFn(promiseObject, call.Arguments...)
		if err != nil {
			panic(err)
		}
		return result
	})
	_ = handle.Set("finally", func(call goja.FunctionCall) goja.Value {
		finallyFn, ok := goja.AssertFunction(promiseObject.Get("finally"))
		if !ok {
			return goja.Undefined()
		}
		result, err := finallyFn(promiseObject, call.Arguments...)
		if err != nil {
			panic(err)
		}
		return result
	})
	return handle
}

func (loop *scriptEventLoop) queueNextTick(callbackValue goja.Value, args ...goja.Value) {
	callback, ok := goja.AssertFunction(callbackValue)
	if !ok {
		panic(loop.runtime.NewTypeError("process.nextTick callback must be a function"))
	}
	loop.nextID++
	id := loop.nextID
	loop.timers[id] = &scriptTimer{
		id:       id,
		callback: callback,
		args:     append([]goja.Value(nil), args...),
		due:      time.Now(),
	}
}

func (loop *scriptEventLoop) addPendingTest() {
	loop.pendingTests++
}

func (loop *scriptEventLoop) finishPendingTest() {
	if loop.pendingTests > 0 {
		loop.pendingTests--
	}
}

func runGojaScript(runtime *goja.Runtime, script, sandboxMode string) error {
	deadline := time.Now().Add(2 * time.Second)
	timer := time.AfterFunc(2*time.Second, func() {
		runtime.Interrupt("script timeout")
	})
	defer timer.Stop()
	defer scriptRuntimeEventLoops.Delete(runtime)
	value, err := runtime.RunString(scriptAsyncWrapper(script, sandboxMode))
	if err != nil {
		return err
	}
	return scriptDrainRuntime(runtime, value, deadline)
}

func applyScriptedBody(item *types.RequestItem, value interface{}, headers []types.KeyValue) {
	if scriptBodyIsFormURLEncoded(*item, headers) {
		item.Body.Mode = "text"
		item.Body.Text = scriptFormURLEncodedBody(value)
		if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(types.GetKeyValue(item.Headers, "Content-Type"))), "application/x-www-form-urlencoded") {
			item.Headers = types.SetKeyValue(item.Headers, "Content-Type", "application/x-www-form-urlencoded")
		}
		return
	}
	switch typed := value.(type) {
	case map[string]interface{}, []interface{}:
		if encoded, err := json.Marshal(typed); err == nil {
			item.Body.Mode = "json"
			item.Body.JSON = string(encoded)
		}
	case string:
		switch item.Body.Mode {
		case "json":
			item.Body.JSON = typed
		case "xml":
			item.Body.XML = typed
		default:
			item.Body.Mode = "text"
			item.Body.Text = typed
		}
	default:
		item.Body.Mode = "text"
		item.Body.Text = fmt.Sprint(typed)
	}
}

func scriptValueIsArray(value goja.Value) bool {
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		return false
	}
	_, ok := value.Export().([]interface{})
	return ok
}

func keyValuesToMap(values []types.KeyValue) map[string]string {
	result := map[string]string{}
	for _, value := range values {
		if value.Enabled && value.Name != "" {
			result[value.Name] = value.Value
		}
	}
	return result
}

func scriptStringList(value goja.Value) []string {
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		return nil
	}
	exported := value.Export()
	switch typed := exported.(type) {
	case []interface{}:
		result := make([]string, 0, len(typed))
		for _, item := range typed {
			result = append(result, fmt.Sprint(item))
		}
		return result
	case []string:
		return append([]string(nil), typed...)
	case string:
		return []string{typed}
	default:
		return []string{value.String()}
	}
}

func scriptBodyValue(value goja.Value) interface{} {
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		return nil
	}
	return value.Export()
}

func scriptRawBody(value interface{}) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case []byte:
		return string(typed)
	default:
		encoded, err := json.Marshal(typed)
		if err != nil {
			return fmt.Sprint(typed)
		}
		return string(encoded)
	}
}

func deleteKeyValue(values []types.KeyValue, name string) []types.KeyValue {
	next := values[:0]
	for _, value := range values {
		if !strings.EqualFold(value.Name, name) {
			next = append(next, value)
		}
	}
	return next
}

func numericInterface(value interface{}) (float64, bool) {
	switch typed := value.(type) {
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case float64:
		return typed, true
	case json.Number:
		number, err := typed.Float64()
		return number, err == nil
	default:
		return 0, false
	}
}

func scriptByteArrayValue(runtime *goja.Runtime, data []byte) goja.Value {
	bytesCopy := append([]byte(nil), data...)
	buffer := runtime.NewArrayBuffer(bytesCopy)
	value, err := runtime.New(runtime.Get("Uint8Array"), runtime.ToValue(buffer))
	if err != nil {
		panic(err)
	}
	return value
}

func safeDecodeURIComponent(value string) string {
	decoded, err := url.PathUnescape(value)
	if err == nil {
		return decoded
	}
	var b strings.Builder
	b.Grow(len(value))
	for i := 0; i < len(value); i++ {
		if value[i] == '%' && i+2 < len(value) {
			if hi, ok := hexDigit(value[i+1]); ok {
				if lo, ok := hexDigit(value[i+2]); ok {
					decodedByte := byte(hi<<4 | lo)
					if decodedByte < 0x80 {
						b.WriteByte(decodedByte)
					} else {
						b.WriteString(value[i : i+3])
					}
					i += 2
					continue
				}
			}
		}
		b.WriteByte(value[i])
	}
	return b.String()
}

func encodeURIComponent(value string) string {
	var b strings.Builder
	b.Grow(len(value))
	for i := 0; i < len(value); i++ {
		c := value[i]
		if isEncodeURIComponentSafe(c) {
			b.WriteByte(c)
			continue
		}
		fmt.Fprintf(&b, "%%%02X", c)
	}
	return b.String()
}

func isEncodeURIComponentSafe(c byte) bool {
	return (c >= 'a' && c <= 'z') ||
		(c >= 'A' && c <= 'Z') ||
		(c >= '0' && c <= '9') ||
		c == '-' || c == '_' || c == '.' || c == '!' ||
		c == '~' || c == '*' || c == '\'' || c == '(' || c == ')'
}

func hexDigit(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	default:
		return 0, false
	}
}

// ApplyIterationDataToContext puts a data-file row into the Data scope.
//
// A nil or empty row is a no-op rather than a clear: a run without a data file
// must behave exactly as it did before US-046.
func ApplyIterationDataToContext(ctx *VariableContext, row map[string]string) {
	if ctx == nil || len(row) == 0 {
		return
	}
	if ctx.Data == nil {
		ctx.Data = map[string]interface{}{}
	}
	for key, value := range row {
		ctx.Data[key] = value
	}
	ctx.Recompute()
}

func dotEnvFilesInScope(scope, basePath string) ([]types.DotEnvFile, error) {
	if strings.TrimSpace(basePath) == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(basePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	files := []types.DotEnvFile{}
	for _, entry := range entries {
		if entry.IsDir() || !isDotEnvFilename(entry.Name()) {
			continue
		}
		path := filepath.Join(basePath, entry.Name())
		info, err := entry.Info()
		if err != nil {
			return nil, err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		files = append(files, types.DotEnvFile{
			Scope:     scope,
			Name:      entry.Name(),
			Path:      path,
			Content:   string(data),
			Runtime:   entry.Name() == ".env",
			Size:      info.Size(),
			UpdatedAt: info.ModTime(),
		})
	}
	return files, nil
}

func mergeStringMap(target, source map[string]string) {
	for name, value := range source {
		target[name] = value
	}
}

func firstString(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func (ctx *VariableContext) Recompute() {
	if ctx == nil {
		return
	}
	if ctx.Combined == nil {
		ctx.Combined = map[string]string{}
	}
	for key := range ctx.Combined {
		delete(ctx.Combined, key)
	}
	add := func(values map[string]interface{}) {
		for key, value := range values {
			ctx.Combined[key] = ScriptVariableString(value)
		}
	}
	add(ctx.Global)
	add(ctx.Collection)
	add(ctx.Env)
	add(ctx.Folder)
	add(ctx.Request)
	// US-046. Data sits above the environment and collection scopes, matching
	// Postman, but below Runtime and Prompt: a value the script explicitly set
	// with bru.setVar, or one the user was just prompted for, is a deliberate
	// act during THIS iteration and must not be silently overwritten by the
	// row that was chosen before the iteration began.
	add(ctx.Data)
	add(ctx.Runtime)
	add(ctx.Prompt)
	addProcessEnvVars(ctx.Combined, ctx.ProcessEnv)
}

func (ctx *VariableContext) CombinedInterface() map[string]interface{} {
	out := map[string]interface{}{}
	add := func(values map[string]interface{}) {
		for key, value := range values {
			out[key] = value
		}
	}
	add(ctx.Global)
	add(ctx.Collection)
	add(ctx.Env)
	add(ctx.Folder)
	add(ctx.Request)
	add(ctx.Data)
	add(ctx.Runtime)
	add(ctx.Prompt)
	return out
}

func scanOAuth2PromptParams(params []types.OAuth2AdditionalParam, scanText func(string)) {
	for _, param := range params {
		if !param.Enabled {
			continue
		}
		scanText(param.Name)
		scanText(param.Value)
		scanText(param.SendIn)
	}
}

// interpolateMaxPasses bounds how many times the input is rescanned. A variable
// value may itself reference another variable, so substitution is a fixed-point
// iteration; the bound is what makes a cyclic reference terminate instead of
// looping forever. Each pass resolves one level of nesting.

func EvaluateScriptTests(script string, response types.Response) []types.TestResult {
	lines := strings.Split(script, "\n")
	results := []types.TestResult{}
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "expect ") {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) < 4 {
			continue
		}
		name := strings.TrimPrefix(line, "expect ")
		passed := false
		if parts[1] == "status" {
			passed = CompareAssertion(strconv.Itoa(response.Status), parts[2], parts[3])
		}
		results = append(results, types.TestResult{Name: name, Passed: passed, Message: statusMessage(passed)})
	}
	return results
}

func NormalizeJSSandboxMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "developer":
		return "developer"
	default:
		return "safe"
	}
}

func TimelineSourceFileForItem(collectionPath string, item types.RequestItem) string {
	if strings.TrimSpace(item.FilePath) != "" {
		if strings.TrimSpace(collectionPath) != "" && PathInside(collectionPath, item.FilePath) {
			if rel, err := filepath.Rel(collectionPath, item.FilePath); err == nil {
				return filepath.ToSlash(rel)
			}
		}
		return filepath.ToSlash(item.FilePath)
	}
	name := strings.TrimSpace(item.Name)
	if name == "" {
		return ""
	}
	filename := name
	if ext := filepath.Ext(filename); ext == "" {
		filename += ".yml"
	}
	if folder := strings.Trim(strings.TrimSpace(item.FolderPath), "/"); folder != "" {
		return filepath.ToSlash(filepath.Join(folder, filename))
	}
	return filepath.ToSlash(filename)
}

func FolderChain(collection types.Collection, item types.RequestItem) []types.FolderConfig {
	if len(collection.Folders) == 0 {
		return nil
	}
	targetPath := itemFolderPhysicalPath(collection, item)
	if targetPath == "" {
		return nil
	}
	byPath := map[string]types.FolderConfig{}
	for _, folder := range collection.Folders {
		byPath[folder.Path] = folder
	}
	parts := strings.Split(targetPath, "/")
	chain := []types.FolderConfig{}
	for i := range parts {
		path := strings.Join(parts[:i+1], "/")
		if folder, ok := byPath[path]; ok {
			chain = append(chain, folder)
		}
	}
	return chain
}

// httpClient is used by the script sandbox's own fetch/sendRequest paths. It is
// a variable for the same reason awsv4's and transport's are: package main owns
// the shared transport cache, and this package must not depend on it to build.
var httpClient = func() *http.Client { return &http.Client{Timeout: 30 * time.Second} }

// SetHTTPClient installs the client the script sandbox issues requests through.
func SetHTTPClient(get func() *http.Client) {
	if get != nil {
		httpClient = get
	}
}

func NormalizeVisualizerPayload(payload types.VisualizerPayload) (types.VisualizerPayload, error) {
	if len(payload.Template) > VisualizerTemplateLimit {
		return types.VisualizerPayload{}, fmt.Errorf("visualizer template is %d bytes, over the %d byte limit", len(payload.Template), VisualizerTemplateLimit)
	}
	if len(payload.Data) > VisualizerDataLimit {
		return types.VisualizerPayload{}, fmt.Errorf("visualizer data is %d bytes, over the %d byte limit", len(payload.Data), VisualizerDataLimit)
	}
	return payload, nil
}

var dotEnvFilenamePattern = regexp.MustCompile(`^\.env(?:\.[A-Za-z0-9._-]+)?$`)

const VisualizerTemplateLimit = 1 << 20 // 1 MiB
const VisualizerDataLimit = 4 << 20     // 4 MiB

func statusMessage(ok bool) string {
	if ok {
		return "passed"
	}
	return "failed"
}
