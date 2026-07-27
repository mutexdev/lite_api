// Package scripting is the goja runtime the app exposes to pre- and
// post-request scripts: the pm.* and bru.* API surfaces, the Node shims, the
// test and assertion vocabulary, and the sandbox around them.
//
// US-068, the largest story in the plan. Every function here was already free
// of *App -- the methods it does define are on its own types, which move with
// their receivers -- and that is what made a block this size movable.
package scripting

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
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

	"github.com/mutexdev/lite_api/internal/cookiejar"
	"github.com/mutexdev/lite_api/internal/interp"
	"github.com/mutexdev/lite_api/internal/scalar"
	"github.com/mutexdev/lite_api/internal/types"
	"github.com/mutexdev/lite_api/internal/urlbuild"

	jwtlib "github.com/golang-jwt/jwt/v5"
	googleuuid "github.com/google/uuid"

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

type VariableContext struct {
	Runtime    map[string]interface{}
	Env        map[string]interface{}
	Global     map[string]interface{}
	Collection map[string]interface{}
	Folder     map[string]interface{}
	Request    map[string]interface{}
	// Data is the current iteration's row from a runner data file (US-046).
	Data       map[string]interface{}
	Prompt     map[string]interface{}
	ProcessEnv map[string]string
	Combined   map[string]string

	RuntimeDirty    bool
	EnvDirty        bool
	GlobalDirty     bool
	CollectionDirty bool
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

func selectedScriptLogs(logs []*[]types.ScriptLog) *[]types.ScriptLog {
	if len(logs) == 0 {
		return nil
	}
	return logs[0]
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

// Compiled-once program caches for the built-in shim sources. Every one of
// these is reached during NewScriptRuntimeWithMeta -- the eight developer-only
// entries via installScriptRequire when the collection runs in "developer"
// sandbox mode, the rest unconditionally -- so caching them removes the whole
// per-runtime parse cost. Caching changes nothing about which shims get
// installed in which sandbox mode: the mode gates remain where they were, and a
// cache slot is only consulted from the call site that already ran that source.
//
// The user script itself, require()'d user module files, and dynamically built
// assertion expressions are deliberately absent: their sources are not fixed,
// so they are not cacheable by this mechanism.
var (
	scriptConsoleModuleShim  = newScriptShimProgram("console")
	scriptBufferShim         = newScriptShimProgram("buffer")
	scriptTimersPromisesShim = newScriptShimProgram("timers/promises")
	scriptAssertShim         = newScriptShimProgram("assert")
	scriptUtilShim           = newScriptShimProgram("util")
	scriptAjvShim            = newScriptShimProgram("ajv")
	scriptAxiosShim          = newScriptShimProgram("axios")
	scriptLodashShim         = newScriptShimProgram("lodash")
	scriptQueryStringShim    = newScriptShimProgram("querystring")
	scriptZlibShim           = newScriptShimProgram("zlib")
	scriptDNSShim            = newScriptShimProgram("dns")
	scriptHTTPShim           = newScriptShimProgram("http")
	scriptEventsShim         = newScriptShimProgram("events")
	scriptStreamShim         = newScriptShimProgram("stream")
	scriptStreamPromisesShim = newScriptShimProgram("stream/promises")
	scriptURLShim            = newScriptShimProgram("url")
	scriptMomentShim         = newScriptShimProgram("moment")
	scriptCryptoJSShim       = newScriptShimProgram("crypto-js")
	scriptEventTargetShim    = newScriptShimProgram("EventTarget")
	scriptEncodingShim       = newScriptShimProgram("TextEncoder/TextDecoder")
	scriptFetchShim          = newScriptShimProgram("fetch")
)

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

func newScriptConsoleObject(runtime *goja.Runtime, logs *[]types.ScriptLog) *goja.Object {
	console := runtime.NewObject()
	for _, level := range []string{"log", "debug", "info", "warn", "error"} {
		level := level
		_ = console.Set(level, func(call goja.FunctionCall) goja.Value {
			appendScriptLog(logs, level, call.Arguments)
			return goja.Undefined()
		})
	}
	return console
}

func newScriptConsoleModuleObject(runtime *goja.Runtime) goja.Value {
	script := `(function () {
  const globalConsole = globalThis.console || {};

  function formatValue(value) {
    if (typeof value === "string") return value;
    if (value instanceof Error) return value.name + ": " + value.message;
    try {
      const json = JSON.stringify(value);
      if (json !== undefined) return json;
    } catch (_) {}
    return String(value);
  }

  function formatArgs(args) {
    return Array.prototype.map.call(args, formatValue).join(" ");
  }

  function writeLine(stream, args, fallbackLevel) {
    if (stream && typeof stream.write === "function") {
      stream.write(formatArgs(args) + "\n");
      return;
    }
    const fallback = typeof globalConsole[fallbackLevel] === "function" ? globalConsole[fallbackLevel] : globalConsole.log;
    if (typeof fallback === "function") {
      fallback.apply(globalConsole, args);
    }
  }

  function Console(stdout, stderr) {
    if (!(this instanceof Console)) {
      return new Console(stdout, stderr);
    }
    this._stdout = stdout || null;
    this._stderr = stderr || stdout || null;
  }

  Console.prototype.log = function () {
    writeLine(this._stdout, arguments, "log");
  };
  Console.prototype.info = function () {
    writeLine(this._stdout, arguments, "info");
  };
  Console.prototype.debug = function () {
    writeLine(this._stdout, arguments, "debug");
  };
  Console.prototype.warn = function () {
    writeLine(this._stderr, arguments, "warn");
  };
  Console.prototype.error = function () {
    writeLine(this._stderr, arguments, "error");
  };
  Console.prototype.dir = Console.prototype.log;

  const module = { Console };
  for (const level of ["log", "debug", "info", "warn", "error"]) {
    module[level] = function () {
      const fn = typeof globalConsole[level] === "function" ? globalConsole[level] : globalConsole.log;
      if (typeof fn === "function") {
        return fn.apply(globalConsole, arguments);
      }
    };
  }
  module.dir = module.log;
  module.default = module;
  return module;
})()`
	value, err := runtime.RunProgram(scriptConsoleModuleShim.compiled(script))
	if err != nil {
		panic(runtime.NewGoError(err))
	}
	return value
}

func appendScriptLog(logs *[]types.ScriptLog, level string, values []goja.Value) {
	if logs == nil {
		return
	}
	args := make([]string, 0, len(values))
	for _, value := range values {
		args = append(args, scriptLogValueString(value))
	}
	*logs = append(*logs, types.ScriptLog{
		Level:   level,
		Message: strings.Join(args, " "),
		Args:    args,
	})
}

func StringMapHasKey(values map[string]string, key string) bool {
	for name := range values {
		if strings.EqualFold(name, key) {
			return true
		}
	}
	return false
}

func installScriptRequire(runtime *goja.Runtime, collectionPath, sandboxMode string) {
	modules := map[string]goja.Value{}
	moduleCache := map[string]goja.Value{}
	developerMode := NormalizeJSSandboxMode(sandboxMode) == "developer"
	jwtObject := newScriptJWTObject(runtime)
	modules["jsonwebtoken"] = jwtObject
	modules["jwt"] = jwtObject
	lodashObject := newScriptLodashObject(runtime)
	modules["lodash"] = lodashObject
	modules["underscore"] = lodashObject
	installScriptLodashSubpathModules(runtime, modules, lodashObject)
	uuidObject := newScriptUUIDObject(runtime)
	modules["uuid"] = uuidObject
	nanoidObject := newScriptNanoIDObject(runtime)
	modules["nanoid"] = nanoidObject
	pathObject := newScriptPathObject(runtime)
	modules["path"] = pathObject
	modules["node:path"] = pathObject
	if developerMode {
		posixPathObject := pathObject
		win32PathObject := newScriptWin32PathObject(runtime)
		linkScriptPathVariants(posixPathObject, win32PathObject)
		modules["path/posix"] = posixPathObject
		modules["node:path/posix"] = posixPathObject
		modules["path/win32"] = win32PathObject
		modules["node:path/win32"] = win32PathObject
	}
	urlObject := newScriptURLObject(runtime)
	modules["url"] = urlObject
	modules["node:url"] = urlObject
	queryStringObject := newScriptQueryStringObject(runtime)
	modules["querystring"] = queryStringObject
	modules["node:querystring"] = queryStringObject
	osObject := newScriptOSObject(runtime)
	modules["os"] = osObject
	modules["node:os"] = osObject
	eventsObject := newScriptEventsObject(runtime)
	modules["events"] = eventsObject
	modules["node:events"] = eventsObject
	streamObject := newScriptStreamObject(runtime)
	modules["stream"] = streamObject
	modules["node:stream"] = streamObject
	if developerMode {
		streamPromisesObject := newScriptStreamPromisesObject(runtime, streamObject)
		_ = streamObject.ToObject(runtime).Set("promises", streamPromisesObject)
		modules["stream/promises"] = streamPromisesObject
		modules["node:stream/promises"] = streamPromisesObject
	}
	zlibObject := newScriptZlibObject(runtime)
	modules["zlib"] = zlibObject
	modules["node:zlib"] = zlibObject
	atob := runtime.ToValue(func(value string) (string, error) {
		decoded, err := decodeScriptBase64(value)
		if err != nil {
			return "", err
		}
		return scriptBinaryStringFromBytes(decoded), nil
	})
	btoa := runtime.ToValue(func(value string) (string, error) {
		bytes, err := scriptBytesFromBinaryString(value)
		if err != nil {
			return "", err
		}
		return base64.StdEncoding.EncodeToString(bytes), nil
	})
	modules["atob"] = atob
	modules["btoa"] = btoa
	_ = runtime.Set("jwt", jwtObject)
	_ = runtime.Set("_", lodashObject)
	_ = runtime.Set("atob", atob)
	_ = runtime.Set("btoa", btoa)
	bufferModule := installScriptBuffer(runtime)
	modules["buffer"] = bufferModule
	modules["node:buffer"] = bufferModule
	if developerMode {
		processObject := runtime.Get("process")
		modules["process"] = processObject
		modules["node:process"] = processObject
		timersPromisesObject := newScriptTimersPromisesObject(runtime)
		timersObject := newScriptTimersObject(runtime, timersPromisesObject)
		modules["timers"] = timersObject
		modules["node:timers"] = timersObject
		modules["timers/promises"] = timersPromisesObject
		modules["node:timers/promises"] = timersPromisesObject
		consoleModule := newScriptConsoleModuleObject(runtime)
		modules["console"] = consoleModule
		modules["node:console"] = consoleModule
		assertObject := newScriptAssertObject(runtime)
		modules["assert"] = assertObject
		modules["node:assert"] = assertObject
		assertStrictObject := assertObject.ToObject(runtime).Get("strict")
		modules["assert/strict"] = assertStrictObject
		modules["node:assert/strict"] = assertStrictObject
		fsObject := newScriptFSObject(runtime, collectionPath, sandboxMode)
		modules["fs"] = fsObject
		modules["node:fs"] = fsObject
		fsPromisesObject := fsObject.Get("promises")
		modules["fs/promises"] = fsPromisesObject
		modules["node:fs/promises"] = fsPromisesObject
		dnsObject := newScriptDNSObject(runtime)
		modules["dns"] = dnsObject
		modules["node:dns"] = dnsObject
		dnsPromisesObject := dnsObject.ToObject(runtime).Get("promises")
		modules["dns/promises"] = dnsPromisesObject
		modules["node:dns/promises"] = dnsPromisesObject
		httpObject := newScriptHTTPObject(runtime, eventsObject, false)
		modules["http"] = httpObject
		modules["node:http"] = httpObject
		httpsObject := newScriptHTTPObject(runtime, eventsObject, true)
		modules["https"] = httpsObject
		modules["node:https"] = httpsObject
	}
	utilObject := newScriptUtilObject(runtime)
	modules["util"] = utilObject
	modules["node:util"] = utilObject
	if developerMode {
		utilTypesObject := utilObject.ToObject(runtime).Get("types")
		modules["util/types"] = utilTypesObject
		modules["node:util/types"] = utilTypesObject
	}
	cryptoObject := newScriptCryptoObject(runtime)
	modules["crypto"] = cryptoObject
	modules["node:crypto"] = cryptoObject
	_ = runtime.Set("crypto", cryptoObject)
	cryptoJSObject := newScriptCryptoJSObject(runtime)
	modules["crypto-js"] = cryptoJSObject
	_ = runtime.Set("CryptoJS", cryptoJSObject)
	xmlFormatter := newScriptXMLFormatterObject(runtime)
	modules["xml-formatter"] = xmlFormatter
	cheerioObject := newScriptCheerioObject(runtime)
	modules["cheerio"] = cheerioObject
	xml2jsObject := newScriptXML2JSObject(runtime)
	modules["xml2js"] = xml2jsObject
	yamlObject := newScriptYAMLObject(runtime)
	modules["yaml"] = yamlObject
	momentObject := newScriptMomentObject(runtime)
	modules["moment"] = momentObject
	_ = runtime.Set("moment", momentObject)
	tv4Object := newScriptTV4Object(runtime)
	modules["tv4"] = tv4Object
	_ = runtime.Set("tv4", tv4Object)
	ajvConstructor := installScriptAjv(runtime)
	modules["ajv"] = ajvConstructor
	addFormats := runtime.ToValue(func(goja.Value) goja.Value {
		return goja.Undefined()
	})
	modules["ajv-formats"] = addFormats
	_ = runtime.Set("Ajv", ajvConstructor)
	_ = runtime.Set("addFormats", addFormats)
	nodeFetchObject := newScriptNodeFetchModule(runtime)
	modules["node-fetch"] = nodeFetchObject
	modules["node-fetch/commonjs"] = nodeFetchObject
	modules["axios"] = installScriptAxios(runtime)
	chaiObject := runtime.NewObject()
	_ = chaiObject.Set("expect", runtime.Get("expect"))
	_ = chaiObject.Set("assert", runtime.Get("assert"))
	modules["chai"] = chaiObject
	loadingModules := map[string]*goja.Object{}
	var loadLocalModule func(parentDir, name string) (goja.Value, error)
	var loadNodeModule func(parentDir, name string) (goja.Value, error)
	loadLocalModule = func(parentDir, name string) (goja.Value, error) {
		modulePath, err := resolveScriptLocalModule(collectionPath, parentDir, name, sandboxMode)
		if err != nil {
			return nil, err
		}
		if cached, ok := moduleCache[modulePath]; ok {
			return cached, nil
		}
		if strings.EqualFold(filepath.Ext(modulePath), ".json") {
			content, err := os.ReadFile(modulePath)
			if err != nil {
				return nil, err
			}
			var data interface{}
			if err := json.Unmarshal(content, &data); err != nil {
				return nil, err
			}
			value := runtime.ToValue(data)
			moduleCache[modulePath] = value
			return value, nil
		}
		if loading, ok := loadingModules[modulePath]; ok {
			return loading.Get("exports"), nil
		}
		content, err := os.ReadFile(modulePath)
		if err != nil {
			return nil, err
		}
		moduleObject := runtime.NewObject()
		exportsObject := runtime.NewObject()
		_ = moduleObject.Set("exports", exportsObject)
		loadingModules[modulePath] = moduleObject
		moduleDir := filepath.Dir(modulePath)
		localRequire := func(requiredName string) goja.Value {
			if module, ok := modules[requiredName]; ok {
				return module
			}
			if developerMode {
				if module, err := loadNodeModule(moduleDir, requiredName); err == nil {
					return module
				}
			}
			if !scriptModuleIsLocalPath(requiredName) {
				panic(runtime.NewGoError(fmt.Errorf("Cannot find module %q", requiredName)))
			}
			module, err := loadLocalModule(moduleDir, requiredName)
			if err != nil {
				panic(runtime.NewGoError(fmt.Errorf("Cannot find module %q", requiredName)))
			}
			return module
		}
		wrapped, err := runtime.RunString("(function(require, module, exports, __filename, __dirname) {\n" + string(content) + "\n})")
		if err != nil {
			delete(loadingModules, modulePath)
			return nil, err
		}
		fn, ok := goja.AssertFunction(wrapped)
		if !ok {
			delete(loadingModules, modulePath)
			return nil, errors.New("local module wrapper is not callable")
		}
		if _, err := fn(goja.Undefined(), runtime.ToValue(localRequire), moduleObject, exportsObject, runtime.ToValue(modulePath), runtime.ToValue(moduleDir)); err != nil {
			delete(loadingModules, modulePath)
			return nil, err
		}
		exports := moduleObject.Get("exports")
		moduleCache[modulePath] = exports
		delete(loadingModules, modulePath)
		return exports, nil
	}
	loadNodeModule = func(parentDir, name string) (goja.Value, error) {
		if !developerMode {
			return nil, fmt.Errorf("Cannot find module %q", name)
		}
		moduleName, subpath, ok := scriptNodeModuleParts(name)
		if !ok {
			return nil, fmt.Errorf("Cannot find module %q", name)
		}
		for _, modulesDir := range scriptNodeModuleSearchDirs(collectionPath, parentDir) {
			candidate := filepath.Join(modulesDir, filepath.FromSlash(moduleName))
			if subpath != "" {
				candidate = filepath.Join(candidate, filepath.FromSlash(subpath))
			}
			modulePath, err := resolveScriptLocalModule(collectionPath, "", candidate, sandboxMode)
			if err == nil {
				return loadLocalModule(filepath.Dir(modulePath), modulePath)
			}
		}
		return nil, fmt.Errorf("Cannot find module %q", name)
	}
	_ = runtime.Set("require", func(name string) goja.Value {
		if module, ok := modules[name]; ok {
			return module
		}
		if scriptModuleIsLocalPath(name) {
			module, err := loadLocalModule(collectionPath, name)
			if err == nil {
				return module
			}
		} else if developerMode {
			module, err := loadNodeModule(collectionPath, name)
			if err == nil {
				return module
			}
		}
		panic(runtime.NewGoError(fmt.Errorf("Cannot find module %q", name)))
	})
}

func installScriptBuffer(runtime *goja.Runtime) goja.Value {
	script := `(function () {
  const marker = "__liteApiBuffer";
  function normalizeEncoding(encoding) {
    return String(encoding || "utf8").toLowerCase().replace(/[-_]/g, "");
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
  function utf8String(bytes) {
    let encoded = "";
    for (let i = 0; i < bytes.length; i++) {
      const value = bytes[i];
      if (value < 0x80) {
        encoded += String.fromCharCode(value);
      } else {
        encoded += "%" + value.toString(16).padStart(2, "0").toUpperCase();
      }
    }
    try {
      return decodeURIComponent(encoded);
    } catch (_) {
      let fallback = "";
      for (let i = 0; i < bytes.length; i++) {
        fallback += String.fromCharCode(bytes[i]);
      }
      return fallback;
    }
  }
  function binaryString(bytes) {
    let out = "";
    for (let i = 0; i < bytes.length; i++) {
      out += String.fromCharCode(bytes[i]);
    }
    return out;
  }
  function bytesFromString(value, encoding) {
    const enc = normalizeEncoding(encoding);
    const text = String(value);
    if (enc === "hex") {
      const out = [];
      for (let i = 0; i + 1 < text.length; i += 2) {
        const parsed = parseInt(text.slice(i, i + 2), 16);
        if (Number.isNaN(parsed)) {
          break;
        }
        out.push(parsed);
      }
      return out;
    }
    if (enc === "base64" || enc === "base64url") {
      const binary = atob(text.replace(/-/g, "+").replace(/_/g, "/"));
      const out = [];
      for (let i = 0; i < binary.length; i++) {
        out.push(binary.charCodeAt(i) & 255);
      }
      return out;
    }
    if (enc === "latin1" || enc === "binary" || enc === "ascii") {
      const out = [];
      for (let i = 0; i < text.length; i++) {
        out.push(text.charCodeAt(i) & 255);
      }
      return out;
    }
    return utf8Bytes(text);
  }
  function bytesFromValue(value, encoding) {
    if (Buffer.isBuffer(value)) {
      return Array.from(value);
    }
    if (typeof value === "string") {
      return bytesFromString(value, encoding);
    }
    if (value instanceof ArrayBuffer) {
      return Array.from(new Uint8Array(value));
    }
    if (ArrayBuffer.isView(value)) {
      return Array.from(new Uint8Array(value.buffer, value.byteOffset, value.byteLength));
    }
    if (Array.isArray(value)) {
      return value.map((item) => Number(item) & 255);
    }
    throw new TypeError("Buffer.from input must be a string, array, ArrayBuffer, or typed array");
  }
  class Buffer extends Uint8Array {
    constructor(value) {
      super(value);
      Object.defineProperty(this, marker, { value: true, enumerable: false });
    }
    static from(value, encoding) {
      return new Buffer(bytesFromValue(value, encoding));
    }
    static alloc(size, fill, encoding) {
      const length = Number(size);
      if (!Number.isFinite(length) || length < 0) {
        throw new RangeError("Buffer size must be a non-negative number");
      }
      const buffer = new Buffer(Math.trunc(length));
      if (fill !== undefined) {
        if (typeof fill === "number") {
          buffer.fill(fill & 255);
        } else {
          const pattern = bytesFromValue(fill, encoding);
          if (pattern.length > 0) {
            for (let i = 0; i < buffer.length; i++) {
              buffer[i] = pattern[i % pattern.length];
            }
          }
        }
      }
      return buffer;
    }
    static concat(list, totalLength) {
      if (!Array.isArray(list)) {
        throw new TypeError("Buffer.concat list must be an array");
      }
      const chunks = list.map((item) => Buffer.isBuffer(item) ? item : Buffer.from(item));
      const length = totalLength === undefined ? chunks.reduce((sum, item) => sum + item.length, 0) : Math.trunc(Number(totalLength));
      const out = Buffer.alloc(Math.max(0, length));
      let offset = 0;
      for (const chunk of chunks) {
        const remaining = out.length - offset;
        if (remaining <= 0) {
          break;
        }
        out.set(chunk.subarray(0, remaining), offset);
        offset += Math.min(chunk.length, remaining);
      }
      return out;
    }
    static isBuffer(value) {
      return !!(value && value[marker] === true);
    }
    static byteLength(value, encoding) {
      return bytesFromValue(value, encoding).length;
    }
    toString(encoding, start, end) {
      const enc = normalizeEncoding(encoding);
      const begin = start === undefined ? 0 : Math.max(0, Math.trunc(Number(start)));
      const finish = end === undefined ? this.length : Math.min(this.length, Math.max(begin, Math.trunc(Number(end))));
      const bytes = Array.from(Uint8Array.prototype.subarray.call(this, begin, finish));
      if (enc === "hex") {
        return bytes.map((value) => value.toString(16).padStart(2, "0")).join("");
      }
      if (enc === "base64") {
        return btoa(binaryString(bytes));
      }
      if (enc === "latin1" || enc === "binary" || enc === "ascii") {
        return binaryString(bytes);
      }
      return utf8String(bytes);
    }
    subarray(start, end) {
      const length = this.length;
      let begin = start === undefined ? 0 : Math.trunc(Number(start));
      let finish = end === undefined ? length : Math.trunc(Number(end));
      if (begin < 0) {
        begin = Math.max(length + begin, 0);
      } else {
        begin = Math.min(begin, length);
      }
      if (finish < 0) {
        finish = Math.max(length + finish, 0);
      } else {
        finish = Math.min(finish, length);
      }
      if (finish < begin) {
        finish = begin;
      }
      const out = [];
      for (let i = begin; i < finish; i++) {
        out.push(this[i]);
      }
      return new Buffer(out);
    }
    slice(start, end) {
      return this.subarray(start, end);
    }
    toJSON() {
      return { type: "Buffer", data: Array.from(this) };
    }
  }
  globalThis.Buffer = Buffer;
  return { Buffer };
})()`
	value, err := runtime.RunProgram(scriptBufferShim.compiled(script))
	if err != nil {
		panic(runtime.NewGoError(err))
	}
	return value
}

func newScriptAssertObject(runtime *goja.Runtime) goja.Value {
	script := `(function () {
  function inspect(value) {
    if (typeof value === "string") return JSON.stringify(value);
    if (typeof value === "function") return "[Function" + (value.name ? ": " + value.name : "") + "]";
    if (value instanceof Error) return value.name + ": " + value.message;
    try {
      return JSON.stringify(value);
    } catch (_) {
      return String(value);
    }
  }

  function sorted(value, seen) {
    if (value === null || typeof value !== "object") return value;
    seen = seen || [];
    if (seen.indexOf(value) !== -1) return "[Circular]";
    if (typeof Buffer !== "undefined" && Buffer.isBuffer(value)) return { type: "Buffer", data: Array.from(value) };
    if (typeof ArrayBuffer !== "undefined" && ArrayBuffer.isView(value)) return { type: value.constructor && value.constructor.name || "TypedArray", data: Array.from(value) };
    if (Array.isArray(value)) return value.map((item) => sorted(item, seen.concat([value])));
    if (value instanceof Date) return { type: "Date", value: value.toISOString() };
    if (value instanceof RegExp) return { type: "RegExp", value: String(value) };
    const out = {};
    for (const key of Object.keys(value).sort()) {
      out[key] = sorted(value[key], seen.concat([value]));
    }
    return out;
  }

  function deepEqual(actual, expected) {
    if (Object.is(actual, expected)) return true;
    return JSON.stringify(sorted(actual)) === JSON.stringify(sorted(expected));
  }

  class AssertionError extends Error {
    constructor(options) {
      options = options || {};
      const actual = options.actual;
      const expected = options.expected;
      const operator = options.operator || "fail";
      const message = options.message === undefined || options.message === null
        ? "Expected values to be " + operator + ":\n\n" + inspect(actual) + "\n\n" + inspect(expected)
        : String(options.message);
      super(message);
      this.name = "AssertionError";
      this.code = "ERR_ASSERTION";
      this.actual = actual;
      this.expected = expected;
      this.operator = operator;
      this.generatedMessage = options.message === undefined || options.message === null;
    }
  }

  function fail(actual, expected, message, operator) {
    if (arguments.length <= 1) {
      throw new AssertionError({ message: actual, operator: "fail" });
    }
    throw new AssertionError({ actual, expected, message, operator: operator || "fail" });
  }

  function assert(value, message) {
    if (!value) {
      throw new AssertionError({ actual: value, expected: true, message, operator: "==" });
    }
  }

  function ok(value, message) {
    return assert(value, message);
  }

  function equal(actual, expected, message) {
    if (actual != expected) {
      throw new AssertionError({ actual, expected, message, operator: "==" });
    }
  }

  function notEqual(actual, expected, message) {
    if (actual == expected) {
      throw new AssertionError({ actual, expected, message, operator: "!=" });
    }
  }

  function strictEqual(actual, expected, message) {
    if (!Object.is(actual, expected)) {
      throw new AssertionError({ actual, expected, message, operator: "strictEqual" });
    }
  }

  function notStrictEqual(actual, expected, message) {
    if (Object.is(actual, expected)) {
      throw new AssertionError({ actual, expected, message, operator: "notStrictEqual" });
    }
  }

  function deepEqualAssert(actual, expected, message) {
    if (!deepEqual(actual, expected)) {
      throw new AssertionError({ actual, expected, message, operator: "deepEqual" });
    }
  }

  function notDeepEqualAssert(actual, expected, message) {
    if (deepEqual(actual, expected)) {
      throw new AssertionError({ actual, expected, message, operator: "notDeepEqual" });
    }
  }

  function matchesExpected(error, expected) {
    if (expected === undefined || expected === null) return true;
    if (expected instanceof RegExp) return expected.test(error && (error.message || String(error)));
    if (typeof expected === "function") {
      if (error instanceof expected) return true;
      return expected(error) === true;
    }
    if (typeof expected === "object") {
      for (const key of Object.keys(expected)) {
        if (!deepEqual(error && error[key], expected[key])) return false;
      }
      return true;
    }
    return String(error && (error.message || error)).includes(String(expected));
  }

  function throws(fn, expected, message) {
    if (typeof fn !== "function") {
      throw new TypeError("The \"fn\" argument must be of type function");
    }
    let thrown;
    try {
      fn();
    } catch (err) {
      thrown = err;
    }
    if (!thrown) {
      throw new AssertionError({ actual: undefined, expected, message, operator: "throws" });
    }
    if (!matchesExpected(thrown, expected)) {
      throw thrown;
    }
    return thrown;
  }

  function doesNotThrow(fn, expected, message) {
    if (typeof fn !== "function") {
      throw new TypeError("The \"fn\" argument must be of type function");
    }
    try {
      fn();
    } catch (err) {
      if (!matchesExpected(err, expected)) throw err;
      throw new AssertionError({ actual: err, expected, message, operator: "doesNotThrow" });
    }
  }

  function ifError(value) {
    if (value) {
      throw value instanceof Error ? value : new AssertionError({ actual: value, expected: null, operator: "ifError" });
    }
  }

  function match(value, regexp, message) {
    if (!(regexp instanceof RegExp)) throw new TypeError("The \"regexp\" argument must be a RegExp");
    if (!regexp.test(String(value))) {
      throw new AssertionError({ actual: value, expected: regexp, message, operator: "match" });
    }
  }

  function doesNotMatch(value, regexp, message) {
    if (!(regexp instanceof RegExp)) throw new TypeError("The \"regexp\" argument must be a RegExp");
    if (regexp.test(String(value))) {
      throw new AssertionError({ actual: value, expected: regexp, message, operator: "doesNotMatch" });
    }
  }

  function rejects(promise, expected, message) {
    const source = typeof promise === "function" ? promise() : promise;
    return Promise.resolve(source).then(function () {
      throw new AssertionError({ actual: undefined, expected, message, operator: "rejects" });
    }, function (err) {
      if (!matchesExpected(err, expected)) throw err;
      return err;
    });
  }

  function doesNotReject(promise, expected, message) {
    const source = typeof promise === "function" ? promise() : promise;
    return Promise.resolve(source).catch(function (err) {
      if (!matchesExpected(err, expected)) throw err;
      throw new AssertionError({ actual: err, expected, message, operator: "doesNotReject" });
    });
  }

  Object.assign(assert, {
    AssertionError,
    fail,
    ok,
    equal,
    notEqual,
    deepEqual: deepEqualAssert,
    notDeepEqual: notDeepEqualAssert,
    deepStrictEqual: deepEqualAssert,
    notDeepStrictEqual: notDeepEqualAssert,
    strictEqual,
    notStrictEqual,
    throws,
    doesNotThrow,
    ifError,
    match,
    doesNotMatch,
    rejects,
    doesNotReject
  });

  function strict(value, message) {
    return ok(value, message);
  }
  Object.assign(strict, assert, {
    equal: strictEqual,
    deepEqual: deepEqualAssert,
    notEqual: notStrictEqual,
    notDeepEqual: notDeepEqualAssert
  });
  assert.strict = strict;
  assert.default = assert;
  return assert;
})()`
	value, err := runtime.RunProgram(scriptAssertShim.compiled(script))
	if err != nil {
		panic(runtime.NewGoError(err))
	}
	return value
}

func newScriptUtilObject(runtime *goja.Runtime) goja.Value {
	script := `(function () {
  const objectToString = Object.prototype.toString;

  function isArrayBuffer(value) {
    return objectToString.call(value) === "[object ArrayBuffer]";
  }

  function isArrayBufferView(value) {
    return value && typeof value === "object" && typeof ArrayBuffer !== "undefined" && ArrayBuffer.isView(value);
  }

  function isUint8Array(value) {
    return objectToString.call(value) === "[object Uint8Array]" || (typeof Buffer !== "undefined" && Buffer.isBuffer(value));
  }

  function isTypedArray(value) {
    return isArrayBufferView(value) && !(typeof DataView !== "undefined" && value instanceof DataView);
  }

  function quoteString(value) {
    return "'" + String(value).replace(/\\/g, "\\\\").replace(/'/g, "\\'").replace(/\n/g, "\\n").replace(/\r/g, "\\r") + "'";
  }

  function inspect(value, options) {
    const maxDepth = options && typeof options.depth === "number" ? options.depth : 2;
    const seen = [];

    function inspectValue(input, depth) {
      if (input === null) {
        return "null";
      }
      if (input === undefined) {
        return "undefined";
      }
      const kind = typeof input;
      if (kind === "string") {
        return quoteString(input);
      }
      if (kind === "number" || kind === "boolean" || kind === "bigint") {
        return String(input);
      }
      if (kind === "symbol") {
        return input.toString();
      }
      if (kind === "function") {
        return "[Function" + (input.name ? ": " + input.name : "") + "]";
      }
      if (typeof Buffer !== "undefined" && Buffer.isBuffer(input)) {
        return "<Buffer " + Array.from(input).map((item) => item.toString(16).padStart(2, "0")).join(" ") + ">";
      }
      if (input instanceof Date) {
        return isNaN(input.getTime()) ? "Invalid Date" : input.toISOString();
      }
      if (input instanceof RegExp) {
        return input.toString();
      }
      if (seen.indexOf(input) !== -1) {
        return "[Circular]";
      }
      if (depth < 0) {
        return Array.isArray(input) ? "[Array]" : "[Object]";
      }
      seen.push(input);
      try {
        if (Array.isArray(input) || isTypedArray(input)) {
          const values = Array.prototype.slice.call(input).map((item) => inspectValue(item, depth - 1));
          return "[" + values.join(", ") + "]";
        }
        if (isArrayBuffer(input)) {
          return "ArrayBuffer { byteLength: " + input.byteLength + " }";
        }
        const keys = Object.keys(input);
        const entries = keys.map((key) => key + ": " + inspectValue(input[key], depth - 1));
        return "{ " + entries.join(", ") + " }";
      } finally {
        seen.pop();
      }
    }

    return inspectValue(value, maxDepth);
  }

  function jsonStringify(value) {
    const seen = [];
    try {
      return JSON.stringify(value, function (_, item) {
        if (item && typeof item === "object") {
          if (seen.indexOf(item) !== -1) {
            throw new TypeError("circular");
          }
          seen.push(item);
        }
        return item;
      });
    } catch (_) {
      return "[Circular]";
    }
  }

  function format(first) {
    const args = Array.prototype.slice.call(arguments, 1);
    if (typeof first !== "string") {
      return Array.prototype.slice.call(arguments).map((item) => inspect(item)).join(" ");
    }
    let index = 0;
    const out = first.replace(/%[sdifjoOc%]/g, function (token) {
      if (token === "%%") {
        return "%";
      }
      if (index >= args.length) {
        return token;
      }
      const value = args[index++];
      if (token === "%s") {
        return String(value);
      }
      if (token === "%d") {
        return String(Number(value));
      }
      if (token === "%i") {
        return String(parseInt(value, 10));
      }
      if (token === "%f") {
        return String(parseFloat(value));
      }
      if (token === "%j") {
        return jsonStringify(value);
      }
      if (token === "%c") {
        return "";
      }
      return inspect(value);
    });
    const extras = args.slice(index).map((item) => inspect(item));
    return extras.length > 0 ? out + " " + extras.join(" ") : out;
  }

  function isDeepStrictEqual(left, right) {
    const seen = [];

    function compare(a, b) {
      if (Object.is(a, b)) {
        return true;
      }
      if (typeof a !== typeof b || a === null || b === null) {
        return false;
      }
      if (typeof a !== "object") {
        return false;
      }
      if (a.constructor !== b.constructor) {
        return false;
      }
      if (a instanceof Date || b instanceof Date) {
        return a instanceof Date && b instanceof Date && a.getTime() === b.getTime();
      }
      if (a instanceof RegExp || b instanceof RegExp) {
        return a instanceof RegExp && b instanceof RegExp && String(a) === String(b);
      }
      if (isArrayBuffer(a) || isArrayBuffer(b)) {
        if (!isArrayBuffer(a) || !isArrayBuffer(b) || a.byteLength !== b.byteLength) {
          return false;
        }
        const aa = new Uint8Array(a);
        const bb = new Uint8Array(b);
        for (let i = 0; i < aa.length; i++) {
          if (aa[i] !== bb[i]) {
            return false;
          }
        }
        return true;
      }
      if (isArrayBufferView(a) || isArrayBufferView(b)) {
        if (!isArrayBufferView(a) || !isArrayBufferView(b) || a.constructor !== b.constructor || a.length !== b.length) {
          return false;
        }
        for (let i = 0; i < a.length; i++) {
          if (!compare(a[i], b[i])) {
            return false;
          }
        }
        return true;
      }
      const pair = [a, b];
      for (const item of seen) {
        if (item[0] === a && item[1] === b) {
          return true;
        }
      }
      seen.push(pair);
      const aKeys = Object.keys(a);
      const bKeys = Object.keys(b);
      if (aKeys.length !== bKeys.length) {
        return false;
      }
      aKeys.sort();
      bKeys.sort();
      for (let i = 0; i < aKeys.length; i++) {
        if (aKeys[i] !== bKeys[i] || !compare(a[aKeys[i]], b[bKeys[i]])) {
          return false;
        }
      }
      return true;
    }

    return compare(left, right);
  }

  function promisify(fn) {
    if (typeof fn !== "function") {
      throw new TypeError("The \"original\" argument must be of type Function");
    }
    return function () {
      const self = this;
      const args = Array.prototype.slice.call(arguments);
      return new Promise(function (resolve, reject) {
        fn.apply(self, args.concat(function (err) {
          if (err) {
            reject(err);
            return;
          }
          const values = Array.prototype.slice.call(arguments, 1);
          resolve(values.length > 1 ? values : values[0]);
        }));
      });
    };
  }

  function callbackify(fn) {
    if (typeof fn !== "function") {
      throw new TypeError("The \"original\" argument must be of type Function");
    }
    return function () {
      const self = this;
      const args = Array.prototype.slice.call(arguments);
      const callback = args.pop();
      if (typeof callback !== "function") {
        throw new TypeError("The last argument must be of type Function");
      }
      Promise.resolve(fn.apply(self, args)).then(function (value) {
        callback(null, value);
      }, function (err) {
        callback(err);
      });
    };
  }

  return {
    format,
    inspect,
    isDeepStrictEqual,
    promisify,
    callbackify,
    types: {
      isArrayBuffer,
      isArrayBufferView,
      isTypedArray,
      isUint8Array,
      isDate: function (value) { return value instanceof Date; },
      isRegExp: function (value) { return value instanceof RegExp; },
      isNativeError: function (value) { return value instanceof Error; },
      isMap: function (value) { return typeof Map !== "undefined" && value instanceof Map; },
      isSet: function (value) { return typeof Set !== "undefined" && value instanceof Set; },
      isPromise: function (value) { return !!(value && typeof value.then === "function"); }
    }
  };
})()`
	value, err := runtime.RunProgram(scriptUtilShim.compiled(script))
	if err != nil {
		panic(runtime.NewGoError(err))
	}
	return value
}

func installScriptAxios(runtime *goja.Runtime) goja.Value {
	script := `(function () {
  function cleanConfig(config) {
    if (typeof config === "string") {
      return { url: config };
    }
    if (!config) {
      return {};
    }
    return Object.assign({}, config);
  }
  function request(config) {
    return bru.sendRequest(cleanConfig(config));
  }
  function withUrl(method, url, config) {
    const next = cleanConfig(config);
    next.method = method;
    next.url = url;
    return request(next);
  }
  function withBody(method, url, data, config) {
    const next = cleanConfig(config);
    next.method = method;
    next.url = url;
    if (data !== undefined) {
      next.data = data;
    }
    return request(next);
  }
  function axios(config) {
    return request(config);
  }
  axios.request = request;
  axios.get = function(url, config) { return withUrl("GET", url, config); };
  axios.delete = function(url, config) { return withUrl("DELETE", url, config); };
  axios.head = function(url, config) { return withUrl("HEAD", url, config); };
  axios.options = function(url, config) { return withUrl("OPTIONS", url, config); };
  axios.post = function(url, data, config) { return withBody("POST", url, data, config); };
  axios.put = function(url, data, config) { return withBody("PUT", url, data, config); };
  axios.patch = function(url, data, config) { return withBody("PATCH", url, data, config); };
  axios.default = axios;
  return axios;
})()`
	value, err := runtime.RunProgram(scriptAxiosShim.compiled(script))
	if err != nil {
		panic(runtime.NewGoError(err))
	}
	return value
}

func scriptNodeModuleParts(name string) (string, string, bool) {
	normalized := strings.TrimSpace(strings.ReplaceAll(name, "\\", "/"))
	if normalized == "" || strings.HasPrefix(normalized, ".") || strings.HasPrefix(normalized, "/") || strings.Contains(normalized, "\x00") {
		return "", "", false
	}
	parts := strings.Split(normalized, "/")
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return "", "", false
		}
	}
	if strings.HasPrefix(parts[0], "@") {
		if len(parts) < 2 || parts[0] == "@" {
			return "", "", false
		}
		moduleName := parts[0] + "/" + parts[1]
		return moduleName, strings.Join(parts[2:], "/"), true
	}
	return parts[0], strings.Join(parts[1:], "/"), true
}

func scriptNodeModuleSearchDirs(collectionPath, parentDir string) []string {
	root := filepath.Clean(collectionPath)
	if root == "." || strings.TrimSpace(root) == "" {
		return nil
	}
	start := parentDir
	if strings.TrimSpace(start) == "" {
		start = root
	}
	if abs, err := filepath.Abs(start); err == nil {
		start = abs
	}
	if abs, err := filepath.Abs(root); err == nil {
		root = abs
	}
	start = filepath.Clean(start)
	root = filepath.Clean(root)
	searchDirs := []string{}
	seen := map[string]bool{}
	for current := start; ; current = filepath.Dir(current) {
		if scriptPathWithinRoot(root, current) {
			modulesDir := filepath.Join(current, "node_modules")
			if !seen[modulesDir] {
				searchDirs = append(searchDirs, modulesDir)
				seen[modulesDir] = true
			}
		}
		if current == root || current == filepath.Dir(current) {
			break
		}
	}
	rootModules := filepath.Join(root, "node_modules")
	if !seen[rootModules] {
		searchDirs = append(searchDirs, rootModules)
	}
	return searchDirs
}

func resolveScriptLocalModule(collectionPath, parentDir, name, sandboxMode string) (string, error) {
	root := filepath.Clean(collectionPath)
	if root == "." || strings.TrimSpace(root) == "" {
		return "", errors.New("collection path is not available")
	}
	base := parentDir
	if strings.TrimSpace(base) == "" {
		base = root
	}
	var candidate string
	if filepath.IsAbs(name) {
		candidate = filepath.Clean(name)
	} else {
		candidate = filepath.Clean(filepath.Join(base, name))
	}
	modulePath, err := resolveScriptLocalModuleFile(root, candidate, sandboxMode)
	if err != nil {
		return "", err
	}
	if !scriptPathWithinRoot(root, modulePath) {
		return "", errors.New("local module path escapes collection")
	}
	return modulePath, nil
}

func resolveScriptLocalModuleFile(root, candidate, sandboxMode string) (string, error) {
	developerMode := NormalizeJSSandboxMode(sandboxMode) == "developer"
	options := []string{candidate}
	if filepath.Ext(candidate) == "" {
		options = append(options, candidate+".js", candidate+".cjs")
		if developerMode {
			options = append(options, candidate+".json")
		}
	}
	for _, option := range options {
		info, err := os.Stat(option)
		if err != nil {
			continue
		}
		if info.IsDir() {
			if developerMode {
				if mainPath, ok := scriptPackageMainPath(option); ok {
					if resolvedMain, err := resolveScriptLocalModuleFile(root, mainPath, sandboxMode); err == nil {
						return resolvedMain, nil
					}
				}
			}
			indexCandidates := []string{filepath.Join(option, "index.js"), filepath.Join(option, "index.cjs")}
			if developerMode {
				indexCandidates = append(indexCandidates, filepath.Join(option, "index.json"))
			}
			for _, indexPath := range indexCandidates {
				if indexInfo, err := os.Stat(indexPath); err == nil && !indexInfo.IsDir() {
					return filepath.Clean(indexPath), nil
				}
			}
			continue
		}
		return filepath.Clean(option), nil
	}
	return "", errors.New("local module not found")
}

func newScriptLodashObject(runtime *goja.Runtime) goja.Value {
	script := `(function () {
  const objectToString = Object.prototype.toString;

  function isObject(value) {
    return value !== null && (typeof value === "object" || typeof value === "function");
  }

  function isPlainObject(value) {
    if (objectToString.call(value) !== "[object Object]") return false;
    const proto = Object.getPrototypeOf(value);
    return proto === null || proto === Object.prototype;
  }

  function parsePath(path) {
    if (Array.isArray(path)) return path.map(String);
    const text = String(path || "");
    const parts = [];
    text.replace(/[^.[\]]+|\[(?:(-?\d+(?:\.\d+)?)|(["'])((?:(?!\2)[^\\]|\\.)*?)\2)\]/g, function (match, number, quote, quoted) {
      if (quote) {
        parts.push(quoted.replace(/\\(\\)?/g, "$1"));
      } else {
        parts.push(number !== undefined ? number : match);
      }
    });
    return parts;
  }

  function get(object, path, defaultValue) {
    const parts = parsePath(path);
    let current = object;
    for (const part of parts) {
      if (current == null) return defaultValue;
      current = current[part];
    }
    return current === undefined ? defaultValue : current;
  }

  function has(object, path) {
    const parts = parsePath(path);
    let current = object;
    for (const part of parts) {
      if (current == null || !Object.prototype.hasOwnProperty.call(Object(current), part)) return false;
      current = current[part];
    }
    return true;
  }

  function set(object, path, value) {
    const parts = parsePath(path);
    if (!parts.length) return object;
    let current = object;
    for (let index = 0; index < parts.length - 1; index++) {
      const part = parts[index];
      if (!isObject(current[part])) {
        current[part] = /^\d+$/.test(parts[index + 1]) ? [] : {};
      }
      current = current[part];
    }
    current[parts[parts.length - 1]] = value;
    return object;
  }

  function unset(object, path) {
    const parts = parsePath(path);
    if (!parts.length) return true;
    let current = object;
    for (let index = 0; index < parts.length - 1; index++) {
      current = current == null ? undefined : current[parts[index]];
      if (current == null) return true;
    }
    return delete current[parts[parts.length - 1]];
  }

  function cloneDeep(value, seen) {
    if (!isObject(value)) return value;
    seen = seen || new Map();
    if (seen.has(value)) return seen.get(value);
    if (value instanceof Date) return new Date(value.getTime());
    if (value instanceof RegExp) return new RegExp(value.source, value.flags);
    if (typeof ArrayBuffer !== "undefined" && ArrayBuffer.isView(value)) return new value.constructor(value);
    if (typeof ArrayBuffer !== "undefined" && value instanceof ArrayBuffer) return value.slice(0);
    const out = Array.isArray(value) ? [] : {};
    seen.set(value, out);
    for (const key of Reflect.ownKeys(value)) {
      out[key] = cloneDeep(value[key], seen);
    }
    return out;
  }

  function isEqual(a, b, seen) {
    if (Object.is(a, b)) return true;
    if (!isObject(a) || !isObject(b)) return false;
    seen = seen || new Map();
    const cached = seen.get(a);
    if (cached && cached === b) return true;
    seen.set(a, b);
    if (objectToString.call(a) !== objectToString.call(b)) return false;
    if (a instanceof Date) return a.getTime() === b.getTime();
    if (a instanceof RegExp) return String(a) === String(b);
    if (typeof ArrayBuffer !== "undefined" && (ArrayBuffer.isView(a) || ArrayBuffer.isView(b))) {
      if (!ArrayBuffer.isView(a) || !ArrayBuffer.isView(b) || a.length !== b.length) return false;
      for (let index = 0; index < a.length; index++) if (a[index] !== b[index]) return false;
      return true;
    }
    const aKeys = Reflect.ownKeys(a);
    const bKeys = Reflect.ownKeys(b);
    if (aKeys.length !== bKeys.length) return false;
    for (const key of aKeys) {
      if (!Object.prototype.hasOwnProperty.call(b, key) || !isEqual(a[key], b[key], seen)) return false;
    }
    return true;
  }

  function toArray(collection) {
    if (collection == null) return [];
    if (Array.isArray(collection)) return collection.slice();
    if (typeof collection === "string") return collection.split("");
    if (typeof collection[Symbol.iterator] === "function") return Array.from(collection);
    return Object.keys(collection).map((key) => collection[key]);
  }

  function iterate(collection, iteratee) {
    if (collection == null) return [];
    if (Array.isArray(collection) || typeof collection === "string") {
      return Array.prototype.map.call(collection, function (value, index) { return iteratee(value, index, collection); });
    }
    return Object.keys(collection).map(function (key) { return iteratee(collection[key], key, collection); });
  }

  function property(path) {
    return function (value) { return get(value, path); };
  }

  function matches(source) {
    return function (value) {
      for (const key of Object.keys(source || {})) {
        if (!isEqual(value == null ? undefined : value[key], source[key])) return false;
      }
      return true;
    };
  }

  function normalizeIteratee(iteratee) {
    if (typeof iteratee === "function") return iteratee;
    if (Array.isArray(iteratee)) return function (value) { return isEqual(get(value, iteratee[0]), iteratee[1]); };
    if (typeof iteratee === "string") return property(iteratee);
    if (iteratee && typeof iteratee === "object") return matches(iteratee);
    return function (value) { return value; };
  }

  function map(collection, iteratee) {
    return iterate(collection, normalizeIteratee(iteratee));
  }

  function forEach(collection, iteratee) {
    iterate(collection, normalizeIteratee(iteratee));
    return collection;
  }

  function filter(collection, predicate) {
    const fn = normalizeIteratee(predicate);
    return iterate(collection, function (value, key, source) {
      return fn(value, key, source) ? value : undefined;
    }).filter(function (value) { return value !== undefined; });
  }

  function find(collection, predicate) {
    const fn = normalizeIteratee(predicate);
    const values = toArray(collection);
    for (let index = 0; index < values.length; index++) {
      if (fn(values[index], index, collection)) return values[index];
    }
    return undefined;
  }

  function reduce(collection, iteratee, accumulator) {
    const values = toArray(collection);
    let index = 0;
    if (arguments.length < 3) {
      accumulator = values[0];
      index = 1;
    }
    for (; index < values.length; index++) {
      accumulator = iteratee(accumulator, values[index], index, collection);
    }
    return accumulator;
  }

  function includes(collection, value) {
    if (typeof collection === "string") return collection.indexOf(String(value)) !== -1;
    return toArray(collection).some(function (item) { return isEqual(item, value); });
  }

  function keys(value) {
    return value == null ? [] : Object.keys(Object(value));
  }

  function values(value) {
    return keys(value).map(function (key) { return value[key]; });
  }

  function pick(object) {
    const out = {};
    const paths = Array.prototype.slice.call(arguments, 1).flat();
    for (const path of paths) {
      if (has(object, path)) set(out, path, get(object, path));
    }
    return out;
  }

  function omit(object) {
    const out = cloneDeep(object || {});
    const paths = Array.prototype.slice.call(arguments, 1).flat();
    for (const path of paths) unset(out, path);
    return out;
  }

  function merge(target) {
    target = target || {};
    for (let sourceIndex = 1; sourceIndex < arguments.length; sourceIndex++) {
      const source = arguments[sourceIndex];
      if (!isObject(source)) continue;
      for (const key of Object.keys(source)) {
        if (isPlainObject(source[key]) && isPlainObject(target[key])) {
          merge(target[key], source[key]);
        } else if (Array.isArray(source[key])) {
          target[key] = source[key].slice();
        } else {
          target[key] = source[key];
        }
      }
    }
    return target;
  }

  function groupBy(collection, iteratee) {
    const fn = normalizeIteratee(iteratee);
    return toArray(collection).reduce(function (out, value, index) {
      const key = String(fn(value, index, collection));
      (out[key] || (out[key] = [])).push(value);
      return out;
    }, {});
  }

  function keyBy(collection, iteratee) {
    const fn = normalizeIteratee(iteratee);
    return toArray(collection).reduce(function (out, value, index) {
      out[String(fn(value, index, collection))] = value;
      return out;
    }, {});
  }

  function sortBy(collection, iteratee) {
    const fn = normalizeIteratee(iteratee);
    return toArray(collection).map(function (value, index) {
      return { value, index, criteria: fn(value, index, collection) };
    }).sort(function (left, right) {
      if (left.criteria < right.criteria) return -1;
      if (left.criteria > right.criteria) return 1;
      return left.index - right.index;
    }).map(function (entry) { return entry.value; });
  }

  function uniq(collection) {
    const out = [];
    for (const value of toArray(collection)) {
      if (!out.some(function (existing) { return isEqual(existing, value); })) out.push(value);
    }
    return out;
  }

  function flatten(collection) {
    return toArray(collection).reduce(function (out, value) {
      return out.concat(Array.isArray(value) ? value : [value]);
    }, []);
  }

  function flattenDeep(collection) {
    return toArray(collection).reduce(function (out, value) {
      return out.concat(Array.isArray(value) ? flattenDeep(value) : [value]);
    }, []);
  }

  function chunk(collection, size) {
    const values = toArray(collection);
    const width = Math.max(1, Number(size) || 1);
    const out = [];
    for (let index = 0; index < values.length; index += width) {
      out.push(values.slice(index, index + width));
    }
    return out;
  }

  function compact(collection) {
    return toArray(collection).filter(Boolean);
  }

  function isEmpty(value) {
    if (value == null) return true;
    if (Array.isArray(value) || typeof value === "string") return value.length === 0;
    if (value instanceof Map || value instanceof Set) return value.size === 0;
    return Object.keys(Object(value)).length === 0;
  }

  function Chain(value) {
    this.__value = value;
  }
  Chain.prototype.value = function () { return this.__value; };
  Chain.prototype.map = function (iteratee) { this.__value = map(this.__value, iteratee); return this; };
  Chain.prototype.filter = function (predicate) { this.__value = filter(this.__value, predicate); return this; };
  Chain.prototype.forEach = function (iteratee) { forEach(this.__value, iteratee); return this; };
  Chain.prototype.reduce = function (iteratee, accumulator) { this.__value = reduce(this.__value, iteratee, accumulator); return this; };
  Chain.prototype.sortBy = function (iteratee) { this.__value = sortBy(this.__value, iteratee); return this; };
  Chain.prototype.uniq = function () { this.__value = uniq(this.__value); return this; };
  Chain.prototype.flatten = function () { this.__value = flatten(this.__value); return this; };
  Chain.prototype.flattenDeep = function () { this.__value = flattenDeep(this.__value); return this; };
  Chain.prototype.compact = function () { this.__value = compact(this.__value); return this; };
  Chain.prototype.get = function (path, defaultValue) { this.__value = get(this.__value, path, defaultValue); return this; };

  function chain(value) {
    return new Chain(value);
  }

  function lodash(value) {
    return chain(value);
  }

  Object.assign(lodash, {
    VERSION: "4.17.21-liteapi",
    chain,
    get,
    set,
    has,
    unset,
    property,
    cloneDeep,
    isEqual,
    isObject,
    isPlainObject,
    isArray: Array.isArray,
    isString: function (value) { return typeof value === "string" || value instanceof String; },
    isNumber: function (value) { return typeof value === "number" || value instanceof Number; },
    isBoolean: function (value) { return value === true || value === false || value instanceof Boolean; },
    isFunction: function (value) { return typeof value === "function"; },
    isNil: function (value) { return value == null; },
    isNull: function (value) { return value === null; },
    isUndefined: function (value) { return value === undefined; },
    isEmpty,
    toArray,
    keys,
    values,
    map,
    each: forEach,
    forEach,
    filter,
    find,
    reduce,
    includes,
    pick,
    omit,
    merge,
    assign: Object.assign,
    groupBy,
    keyBy,
    sortBy,
    uniq,
    flatten,
    flattenDeep,
    chunk,
    compact
  });
  lodash.default = lodash;
  return lodash;
})()`
	value, err := runtime.RunProgram(scriptLodashShim.compiled(script))
	if err != nil {
		panic(runtime.NewGoError(err))
	}
	return value
}

func installScriptLodashSubpathModules(runtime *goja.Runtime, modules map[string]goja.Value, lodashObject goja.Value) {
	lodash := lodashObject.ToObject(runtime)
	aliases := map[string]string{
		"chain":         "chain",
		"get":           "get",
		"set":           "set",
		"has":           "has",
		"unset":         "unset",
		"property":      "property",
		"cloneDeep":     "cloneDeep",
		"isEqual":       "isEqual",
		"isObject":      "isObject",
		"isPlainObject": "isPlainObject",
		"isArray":       "isArray",
		"isString":      "isString",
		"isNumber":      "isNumber",
		"isBoolean":     "isBoolean",
		"isFunction":    "isFunction",
		"isNil":         "isNil",
		"isNull":        "isNull",
		"isUndefined":   "isUndefined",
		"isEmpty":       "isEmpty",
		"toArray":       "toArray",
		"keys":          "keys",
		"values":        "values",
		"map":           "map",
		"each":          "each",
		"forEach":       "forEach",
		"filter":        "filter",
		"find":          "find",
		"reduce":        "reduce",
		"includes":      "includes",
		"pick":          "pick",
		"omit":          "omit",
		"merge":         "merge",
		"assign":        "assign",
		"groupBy":       "groupBy",
		"keyBy":         "keyBy",
		"sortBy":        "sortBy",
		"uniq":          "uniq",
		"flatten":       "flatten",
		"flattenDeep":   "flattenDeep",
		"chunk":         "chunk",
		"compact":       "compact",
	}
	for moduleName, propertyName := range aliases {
		value := lodash.Get(propertyName)
		if goja.IsUndefined(value) {
			continue
		}
		modules["lodash/"+moduleName] = value
		modules["lodash/"+moduleName+".js"] = value
	}
}

func newScriptUUIDObject(runtime *goja.Runtime) *goja.Object {
	uuidObject := runtime.NewObject()
	mustString := func(id googleuuid.UUID, err error) string {
		if err != nil {
			panic(runtime.NewGoError(err))
		}
		return id.String()
	}
	nameBased := func(factory func(googleuuid.UUID, []byte) googleuuid.UUID) func(string, string) string {
		return func(name, namespace string) string {
			space, err := googleuuid.Parse(namespace)
			if err != nil {
				panic(runtime.NewGoError(err))
			}
			return factory(space, []byte(name)).String()
		}
	}
	v3 := runtime.ToValue(nameBased(googleuuid.NewMD5))
	_ = v3.ToObject(runtime).Set("DNS", googleuuid.NameSpaceDNS.String())
	_ = v3.ToObject(runtime).Set("URL", googleuuid.NameSpaceURL.String())
	v5 := runtime.ToValue(nameBased(googleuuid.NewSHA1))
	_ = v5.ToObject(runtime).Set("DNS", googleuuid.NameSpaceDNS.String())
	_ = v5.ToObject(runtime).Set("URL", googleuuid.NameSpaceURL.String())
	_ = uuidObject.Set("NIL", googleuuid.Nil.String())
	_ = uuidObject.Set("MAX", "ffffffff-ffff-ffff-ffff-ffffffffffff")
	_ = uuidObject.Set("v1", func() string { return mustString(googleuuid.NewUUID()) })
	_ = uuidObject.Set("v3", v3)
	_ = uuidObject.Set("v4", func() string { return mustString(googleuuid.NewRandom()) })
	_ = uuidObject.Set("v5", v5)
	_ = uuidObject.Set("v6", func() string { return mustString(googleuuid.NewV6()) })
	_ = uuidObject.Set("v7", func() string { return mustString(googleuuid.NewV7()) })
	_ = uuidObject.Set("v1ToV6", func(value string) string {
		id, err := googleuuid.Parse(value)
		if err != nil {
			panic(runtime.NewGoError(err))
		}
		return scriptUUIDV1ToV6(id).String()
	})
	_ = uuidObject.Set("v6ToV1", func(value string) string {
		id, err := googleuuid.Parse(value)
		if err != nil {
			panic(runtime.NewGoError(err))
		}
		return scriptUUIDV6ToV1(id).String()
	})
	_ = uuidObject.Set("validate", func(value string) bool {
		_, err := googleuuid.Parse(value)
		return err == nil
	})
	_ = uuidObject.Set("version", func(value string) int {
		id, err := googleuuid.Parse(value)
		if err != nil {
			panic(runtime.NewGoError(err))
		}
		return int(id.Version())
	})
	_ = uuidObject.Set("parse", func(value string) goja.Value {
		id, err := googleuuid.Parse(value)
		if err != nil {
			panic(runtime.NewGoError(err))
		}
		return scriptByteArrayValue(runtime, id[:])
	})
	_ = uuidObject.Set("stringify", func(call goja.FunctionCall) goja.Value {
		data, err := scriptCryptoValueBytes(runtime, call.Argument(0), "")
		if err != nil {
			panic(runtime.NewGoError(err))
		}
		if len(data) != 16 {
			panic(runtime.NewGoError(fmt.Errorf("uuid byte array must contain 16 bytes")))
		}
		var id googleuuid.UUID
		copy(id[:], data)
		return runtime.ToValue(id.String())
	})
	return uuidObject
}

func scriptUUIDV1ToV6(id googleuuid.UUID) googleuuid.UUID {
	timestamp := (uint64(binary.BigEndian.Uint16(id[6:8])&0x0fff) << 48) |
		(uint64(binary.BigEndian.Uint16(id[4:6])) << 32) |
		uint64(binary.BigEndian.Uint32(id[0:4]))
	var out googleuuid.UUID
	binary.BigEndian.PutUint64(out[0:8], timestamp)
	out[6] = 0x60 | (out[6] & 0x0f)
	copy(out[8:], id[8:])
	out[8] = 0x80 | (out[8] & 0x3f)
	return out
}

func scriptUUIDV6ToV1(id googleuuid.UUID) googleuuid.UUID {
	ordered := id
	ordered[6] &= 0x0f
	timestamp := binary.BigEndian.Uint64(ordered[0:8])
	var out googleuuid.UUID
	binary.BigEndian.PutUint32(out[0:4], uint32(timestamp))
	binary.BigEndian.PutUint16(out[4:6], uint16(timestamp>>32))
	binary.BigEndian.PutUint16(out[6:8], uint16(timestamp>>48))
	out[6] = 0x10 | (out[6] & 0x0f)
	copy(out[8:], id[8:])
	out[8] = 0x80 | (out[8] & 0x3f)
	return out
}

func newScriptNanoIDObject(runtime *goja.Runtime) *goja.Object {
	nanoidObject := runtime.NewObject()
	_ = nanoidObject.Set("nanoid", func(call goja.FunctionCall) goja.Value {
		size := 21
		if len(call.Arguments) > 0 && !goja.IsUndefined(call.Argument(0)) && !goja.IsNull(call.Argument(0)) {
			size = int(call.Argument(0).ToInteger())
		}
		return runtime.ToValue(scriptNanoID(size))
	})
	return nanoidObject
}

func scriptCallStringArgs(call goja.FunctionCall) []string {
	parts := make([]string, 0, len(call.Arguments))
	for _, arg := range call.Arguments {
		parts = append(parts, arg.String())
	}
	return parts
}

func newScriptQueryStringObject(runtime *goja.Runtime) goja.Value {
	script := `(function () {
  function escape(value) {
    return encodeURIComponent(String(value));
  }

  function unescape(value) {
    try {
      return decodeURIComponent(String(value));
    } catch (_) {
      return String(value);
    }
  }

  function parse(input, sep, eq, options) {
    const separator = sep === undefined ? "&" : String(sep);
    const equals = eq === undefined ? "=" : String(eq);
    const out = {};
    const text = String(input || "");
    if (text === "") {
      return out;
    }
    let maxKeys = 1000;
    if (options && Object.prototype.hasOwnProperty.call(options, "maxKeys")) {
      maxKeys = Number(options.maxKeys);
      if (!Number.isFinite(maxKeys) || maxKeys < 0) {
        maxKeys = 0;
      }
    }
    const parts = separator === "" ? [text] : text.split(separator);
    const limit = maxKeys === 0 ? parts.length : Math.min(parts.length, maxKeys);
    for (let index = 0; index < limit; index++) {
      const part = parts[index];
      if (part === "") {
        continue;
      }
      const eqIndex = equals === "" ? -1 : part.indexOf(equals);
      const rawKey = eqIndex === -1 ? part : part.slice(0, eqIndex);
      const rawValue = eqIndex === -1 ? "" : part.slice(eqIndex + equals.length);
      const key = unescape(rawKey.replace(/\+/g, "%20"));
      const value = unescape(rawValue.replace(/\+/g, "%20"));
      if (Object.prototype.hasOwnProperty.call(out, key)) {
        if (Array.isArray(out[key])) {
          out[key].push(value);
        } else {
          out[key] = [out[key], value];
        }
      } else {
        out[key] = value;
      }
    }
    return out;
  }

  function stringify(input, sep, eq) {
    const separator = sep === undefined ? "&" : String(sep);
    const equals = eq === undefined ? "=" : String(eq);
    if (input === undefined || input === null || typeof input !== "object") {
      return "";
    }
    const parts = [];
    for (const key of Object.keys(input)) {
      const value = input[key];
      const encodedKey = escape(key);
      if (Array.isArray(value)) {
        for (const item of value) {
          parts.push(encodedKey + equals + escape(item == null ? "" : item));
        }
      } else {
        parts.push(encodedKey + equals + escape(value == null ? "" : value));
      }
    }
    return parts.join(separator);
  }

  return { parse, decode: parse, stringify, encode: stringify, escape, unescape };
})()`
	value, err := runtime.RunProgram(scriptQueryStringShim.compiled(script))
	if err != nil {
		panic(runtime.NewGoError(err))
	}
	return value
}

func firstNonEmptyEnv(names ...string) string {
	for _, name := range names {
		if value := os.Getenv(name); value != "" {
			return value
		}
	}
	return ""
}

func newScriptDNSObject(runtime *goja.Runtime) goja.Value {
	_ = runtime.Set("__liteApiDNSLookup", scriptDNSLookup)
	_ = runtime.Set("__liteApiDNSResolve", scriptDNSResolve)
	_ = runtime.Set("__liteApiDNSReverse", scriptDNSReverse)
	_ = runtime.Set("__liteApiDNSLookupService", scriptDNSLookupService)
	script := `(function () {
  const lookupBridge = globalThis.__liteApiDNSLookup;
  const resolveBridge = globalThis.__liteApiDNSResolve;
  const reverseBridge = globalThis.__liteApiDNSReverse;
  const lookupServiceBridge = globalThis.__liteApiDNSLookupService;
  let defaultResultOrder = "verbatim";
  let servers = [];

  function callbackRequired(callback) {
    if (typeof callback !== "function") {
      throw new TypeError("Callback must be a function");
    }
  }

  function normalizeLookupOptions(options) {
    if (typeof options === "number") return { family: options, all: false };
    if (!options || typeof options !== "object") return { family: 0, all: false };
    const family = Number(options.family || 0);
    return { family: family === 4 || family === 6 ? family : 0, all: !!options.all };
  }

  function callNode(callback, producer, spread) {
    callbackRequired(callback);
    try {
      const value = producer();
      if (spread && Array.isArray(value)) callback(null, ...value);
      else callback(null, value);
    } catch (err) {
      callback(err);
    }
  }

  function asPromise(producer) {
    try {
      return Promise.resolve(producer());
    } catch (err) {
      return Promise.reject(err);
    }
  }

  function lookupResult(hostname, options) {
    const normalized = normalizeLookupOptions(options);
    const result = lookupBridge(String(hostname || ""), normalized.family);
    if (normalized.all) return result.addresses || [];
    return { address: result.address, family: result.family };
  }

  function lookup(hostname, options, callback) {
    if (typeof options === "function") {
      callback = options;
      options = undefined;
    }
    callNode(callback, function () {
      const result = lookupResult(hostname, options);
      return Array.isArray(result) ? [result] : [result.address, result.family];
    }, true);
  }

  function resolveResult(hostname, rrtype) {
    return resolveBridge(String(hostname || ""), String(rrtype || "A").toUpperCase());
  }

  function resolve(hostname, rrtype, callback) {
    if (typeof rrtype === "function") {
      callback = rrtype;
      rrtype = "A";
    }
    callNode(callback, function () { return resolveResult(hostname, rrtype || "A"); });
  }

  function resolveTyped(rrtype) {
    return function (hostname, options, callback) {
      if (typeof options === "function") {
        callback = options;
      }
      callNode(callback, function () { return resolveResult(hostname, rrtype); });
    };
  }

  const resolve4 = resolveTyped("A");
  const resolve6 = resolveTyped("AAAA");
  const resolveCname = resolveTyped("CNAME");
  const resolveTxt = resolveTyped("TXT");
  const resolveMx = resolveTyped("MX");
  const resolveNs = resolveTyped("NS");
  const resolveSrv = resolveTyped("SRV");
  const resolvePtr = resolveTyped("PTR");
  const resolveAny = resolveTyped("ANY");

  function reverse(ip, callback) {
    callNode(callback, function () { return reverseBridge(String(ip || "")); });
  }

  function lookupService(address, port, callback) {
    callNode(callback, function () {
      const result = lookupServiceBridge(String(address || ""), Number(port || 0));
      return [result.hostname, result.service];
    }, true);
  }

  function getServers() {
    return servers.slice();
  }

  function setServers(nextServers) {
    if (!Array.isArray(nextServers)) {
      throw new TypeError("servers must be an array");
    }
    servers = nextServers.map(String);
  }

  function getDefaultResultOrder() {
    return defaultResultOrder;
  }

  function setDefaultResultOrder(order) {
    const value = String(order || "");
    if (value !== "ipv4first" && value !== "verbatim") {
      throw new TypeError("dns result order must be 'ipv4first' or 'verbatim'");
    }
    defaultResultOrder = value;
  }

  const promises = {
    lookup: function (hostname, options) {
      return asPromise(function () { return lookupResult(hostname, options); });
    },
    resolve: function (hostname, rrtype) {
      return asPromise(function () { return resolveResult(hostname, rrtype || "A"); });
    },
    resolve4: function (hostname, options) {
      return asPromise(function () { return resolveResult(hostname, "A"); });
    },
    resolve6: function (hostname, options) {
      return asPromise(function () { return resolveResult(hostname, "AAAA"); });
    },
    resolveCname: function (hostname) {
      return asPromise(function () { return resolveResult(hostname, "CNAME"); });
    },
    resolveTxt: function (hostname) {
      return asPromise(function () { return resolveResult(hostname, "TXT"); });
    },
    resolveMx: function (hostname) {
      return asPromise(function () { return resolveResult(hostname, "MX"); });
    },
    resolveNs: function (hostname) {
      return asPromise(function () { return resolveResult(hostname, "NS"); });
    },
    resolveSrv: function (hostname) {
      return asPromise(function () { return resolveResult(hostname, "SRV"); });
    },
    resolvePtr: function (hostname) {
      return asPromise(function () { return resolveResult(hostname, "PTR"); });
    },
    resolveAny: function (hostname) {
      return asPromise(function () { return resolveResult(hostname, "ANY"); });
    },
    reverse: function (ip) {
      return asPromise(function () { return reverseBridge(String(ip || "")); });
    },
    lookupService: function (address, port) {
      return asPromise(function () { return lookupServiceBridge(String(address || ""), Number(port || 0)); });
    },
    getServers,
    setServers
  };

  function Resolver() {}
  Resolver.prototype.lookup = lookup;
  Resolver.prototype.resolve = resolve;
  Resolver.prototype.resolve4 = resolve4;
  Resolver.prototype.resolve6 = resolve6;
  Resolver.prototype.resolveCname = resolveCname;
  Resolver.prototype.resolveTxt = resolveTxt;
  Resolver.prototype.resolveMx = resolveMx;
  Resolver.prototype.resolveNs = resolveNs;
  Resolver.prototype.resolveSrv = resolveSrv;
  Resolver.prototype.resolvePtr = resolvePtr;
  Resolver.prototype.resolveAny = resolveAny;
  Resolver.prototype.reverse = reverse;
  Resolver.prototype.lookupService = lookupService;
  Resolver.prototype.getServers = getServers;
  Resolver.prototype.setServers = setServers;

  return {
    ADDRCONFIG: 32,
    V4MAPPED: 8,
    NODATA: "ENODATA",
    FORMERR: "EFORMERR",
    SERVFAIL: "ESERVFAIL",
    NOTFOUND: "ENOTFOUND",
    NOTIMP: "ENOTIMP",
    REFUSED: "EREFUSED",
    BADQUERY: "EBADQUERY",
    BADNAME: "EBADNAME",
    BADFAMILY: "EBADFAMILY",
    BADRESP: "EBADRESP",
    CONNREFUSED: "ECONNREFUSED",
    TIMEOUT: "ETIMEOUT",
    EOF: "EOF",
    FILE: "EFILE",
    NOMEM: "ENOMEM",
    DESTRUCTION: "EDESTRUCTION",
    BADSTR: "EBADSTR",
    BADFLAGS: "EBADFLAGS",
    NONAME: "ENONAME",
    BADHINTS: "EBADHINTS",
    NOTINITIALIZED: "ENOTINITIALIZED",
    LOADIPHLPAPI: "ELOADIPHLPAPI",
    ADDRGETNETWORKPARAMS: "EADDRGETNETWORKPARAMS",
    CANCELLED: "ECANCELLED",
    lookup,
    lookupService,
    resolve,
    resolve4,
    resolve6,
    resolveCname,
    resolveTxt,
    resolveMx,
    resolveNs,
    resolveSrv,
    resolvePtr,
    resolveAny,
    reverse,
    getServers,
    setServers,
    getDefaultResultOrder,
    setDefaultResultOrder,
    Resolver,
    promises
  };
})()`
	value, err := runtime.RunProgram(scriptDNSShim.compiled(script))
	if err != nil {
		panic(runtime.NewGoError(err))
	}
	_ = runtime.Set("__liteApiDNSLookup", goja.Undefined())
	_ = runtime.Set("__liteApiDNSResolve", goja.Undefined())
	_ = runtime.Set("__liteApiDNSReverse", goja.Undefined())
	_ = runtime.Set("__liteApiDNSLookupService", goja.Undefined())
	return value
}

func scriptDNSLookup(hostname string, family int) (map[string]interface{}, error) {
	records, err := scriptDNSLookupRecords(hostname, family)
	if err != nil {
		return nil, err
	}
	if len(records) == 0 {
		return nil, fmt.Errorf("getaddrinfo ENOTFOUND %s", strings.TrimSpace(hostname))
	}
	first := records[0]
	return map[string]interface{}{
		"address":   first["address"],
		"family":    first["family"],
		"addresses": records,
	}, nil
}

func scriptDNSLookupRecords(hostname string, family int) ([]map[string]interface{}, error) {
	hostname = strings.TrimSpace(hostname)
	if hostname == "" {
		return nil, errors.New("hostname is required")
	}
	if family != 0 && family != 4 && family != 6 {
		return nil, fmt.Errorf("invalid DNS address family %d", family)
	}
	ips, err := net.LookupIP(hostname)
	if err != nil {
		return nil, err
	}
	records := make([]map[string]interface{}, 0, len(ips))
	seen := map[string]bool{}
	for _, ip := range ips {
		recordFamily := 6
		address := ip.String()
		if ipv4 := ip.To4(); ipv4 != nil {
			recordFamily = 4
			address = ipv4.String()
		}
		if family != 0 && recordFamily != family {
			continue
		}
		if seen[address] {
			continue
		}
		seen[address] = true
		records = append(records, map[string]interface{}{
			"address": address,
			"family":  recordFamily,
		})
	}
	return records, nil
}

func scriptDNSResolve(hostname, rrtype string) (interface{}, error) {
	hostname = strings.TrimSpace(hostname)
	if hostname == "" {
		return nil, errors.New("hostname is required")
	}
	switch strings.ToUpper(strings.TrimSpace(rrtype)) {
	case "", "A":
		return scriptDNSResolveAddresses(hostname, 4)
	case "AAAA":
		return scriptDNSResolveAddresses(hostname, 6)
	case "CNAME":
		cname, err := net.LookupCNAME(hostname)
		if err != nil {
			return nil, err
		}
		return []string{strings.TrimSuffix(cname, ".")}, nil
	case "TXT":
		records, err := net.LookupTXT(hostname)
		if err != nil {
			return nil, err
		}
		out := make([][]string, 0, len(records))
		for _, record := range records {
			out = append(out, []string{record})
		}
		return out, nil
	case "MX":
		records, err := net.LookupMX(hostname)
		if err != nil {
			return nil, err
		}
		out := make([]map[string]interface{}, 0, len(records))
		for _, record := range records {
			out = append(out, map[string]interface{}{
				"exchange": strings.TrimSuffix(record.Host, "."),
				"priority": record.Pref,
			})
		}
		return out, nil
	case "NS":
		records, err := net.LookupNS(hostname)
		if err != nil {
			return nil, err
		}
		out := make([]string, 0, len(records))
		for _, record := range records {
			out = append(out, strings.TrimSuffix(record.Host, "."))
		}
		return out, nil
	case "SRV":
		service, proto, name := scriptDNSSRVParts(hostname)
		_, records, err := net.LookupSRV(service, proto, name)
		if err != nil {
			return nil, err
		}
		out := make([]map[string]interface{}, 0, len(records))
		for _, record := range records {
			out = append(out, map[string]interface{}{
				"name":     strings.TrimSuffix(record.Target, "."),
				"port":     record.Port,
				"priority": record.Priority,
				"weight":   record.Weight,
			})
		}
		return out, nil
	case "PTR":
		return scriptDNSReverse(hostname)
	case "ANY":
		addresses, err := scriptDNSResolveAddresses(hostname, 0)
		if err != nil {
			return nil, err
		}
		return addresses, nil
	default:
		return nil, fmt.Errorf("unsupported DNS record type %q", rrtype)
	}
}

func scriptDNSResolveAddresses(hostname string, family int) ([]string, error) {
	records, err := scriptDNSLookupRecords(hostname, family)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(records))
	for _, record := range records {
		out = append(out, fmt.Sprint(record["address"]))
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("queryA ENODATA %s", hostname)
	}
	return out, nil
}

func scriptDNSReverse(ip string) ([]string, error) {
	ip = strings.TrimSpace(ip)
	if ip == "" {
		return nil, errors.New("IP address is required")
	}
	records, err := net.LookupAddr(ip)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(records))
	for _, record := range records {
		out = append(out, strings.TrimSuffix(record, "."))
	}
	return out, nil
}

func scriptDNSLookupService(address string, port int) (map[string]interface{}, error) {
	address = strings.TrimSpace(address)
	if address == "" {
		return nil, errors.New("IP address is required")
	}
	if port < 0 || port > 65535 {
		return nil, fmt.Errorf("invalid port %d", port)
	}
	hostnames, err := net.LookupAddr(address)
	if err != nil {
		return nil, err
	}
	hostname := address
	if len(hostnames) > 0 {
		hostname = strings.TrimSuffix(hostnames[0], ".")
	}
	return map[string]interface{}{
		"hostname": hostname,
		"service":  strconv.Itoa(port),
	}, nil
}

func scriptDNSSRVParts(hostname string) (string, string, string) {
	trimmed := strings.Trim(hostname, ".")
	parts := strings.Split(trimmed, ".")
	if len(parts) >= 3 && strings.HasPrefix(parts[0], "_") && strings.HasPrefix(parts[1], "_") {
		return strings.TrimPrefix(parts[0], "_"), strings.TrimPrefix(parts[1], "_"), strings.Join(parts[2:], ".")
	}
	return "", "", hostname
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

func newScriptEventsObject(runtime *goja.Runtime) goja.Value {
	script := `(function () {
  const errorMonitor = typeof Symbol === "function" ? Symbol("events.errorMonitor") : "__bruErrorMonitor";
  const captureRejectionSymbol = typeof Symbol === "function" && Symbol.for ? Symbol.for("nodejs.rejection") : "__bruCaptureRejection";

  function validateListener(listener) {
    if (typeof listener !== "function") {
      throw new TypeError("The listener must be a function");
    }
  }

  function ensureEvents(emitter) {
    if (!Object.prototype.hasOwnProperty.call(emitter, "_events") || !emitter._events) {
      Object.defineProperty(emitter, "_events", { value: Object.create(null), enumerable: false, writable: true, configurable: true });
    }
    return emitter._events;
  }

  function listenersFor(emitter, eventName, create) {
    const events = ensureEvents(emitter);
    if (!events[eventName] && create) {
      events[eventName] = [];
    }
    return events[eventName] || [];
  }

  function unwrap(listener) {
    return listener && listener.listener ? listener.listener : listener;
  }

  function wrapOnce(emitter, eventName, listener) {
    function wrapped() {
      emitter.removeListener(eventName, wrapped);
      return listener.apply(this, arguments);
    }
    Object.defineProperty(wrapped, "listener", { value: listener, enumerable: false, configurable: true });
    return wrapped;
  }

  function addListener(emitter, eventName, listener, prepend, once) {
    validateListener(listener);
    const entry = once ? wrapOnce(emitter, eventName, listener) : listener;
    if (eventName !== "newListener") {
      emitter.emit("newListener", eventName, listener);
    }
    const listeners = listenersFor(emitter, eventName, true);
    if (prepend) {
      listeners.unshift(entry);
    } else {
      listeners.push(entry);
    }
    return emitter;
  }

  class EventEmitter {
    constructor(options) {
      Object.defineProperty(this, "_events", { value: Object.create(null), enumerable: false, writable: true, configurable: true });
      Object.defineProperty(this, "_maxListeners", { value: undefined, enumerable: false, writable: true, configurable: true });
      this.captureRejections = !!(options && options.captureRejections);
    }
    setMaxListeners(n) {
      n = Number(n);
      if (!Number.isFinite(n) || n < 0) {
        throw new RangeError("n must be a non-negative number");
      }
      this._maxListeners = n;
      return this;
    }
    getMaxListeners() {
      return this._maxListeners === undefined ? EventEmitter.defaultMaxListeners : this._maxListeners;
    }
    addListener(eventName, listener) {
      return this.on(eventName, listener);
    }
    on(eventName, listener) {
      return addListener(this, eventName, listener, false, false);
    }
    prependListener(eventName, listener) {
      return addListener(this, eventName, listener, true, false);
    }
    once(eventName, listener) {
      return addListener(this, eventName, listener, false, true);
    }
    prependOnceListener(eventName, listener) {
      return addListener(this, eventName, listener, true, true);
    }
    removeListener(eventName, listener) {
      validateListener(listener);
      const listeners = listenersFor(this, eventName, false);
      for (let index = listeners.length - 1; index >= 0; index--) {
        const candidate = listeners[index];
        if (candidate === listener || candidate.listener === listener) {
          listeners.splice(index, 1);
          if (listeners.length === 0) {
            delete this._events[eventName];
          }
          if (eventName !== "removeListener") {
            this.emit("removeListener", eventName, unwrap(candidate));
          }
          break;
        }
      }
      return this;
    }
    off(eventName, listener) {
      return this.removeListener(eventName, listener);
    }
    removeAllListeners(eventName) {
      if (eventName === undefined) {
        for (const name of Reflect.ownKeys(this._events)) {
          this.removeAllListeners(name);
        }
        return this;
      }
      const listeners = listenersFor(this, eventName, false).slice();
      delete this._events[eventName];
      if (eventName !== "removeListener") {
        for (const listener of listeners) {
          this.emit("removeListener", eventName, unwrap(listener));
        }
      }
      return this;
    }
    emit(eventName) {
      const args = Array.prototype.slice.call(arguments, 1);
      if (eventName === "error") {
        const monitors = listenersFor(this, errorMonitor, false).slice();
        for (const monitor of monitors) {
          monitor.apply(this, args);
        }
      }
      const listeners = listenersFor(this, eventName, false).slice();
      if (listeners.length === 0) {
        if (eventName === "error") {
          const err = args[0];
          if (err instanceof Error) {
            throw err;
          }
          throw new Error("Unhandled error." + (err === undefined ? "" : " (" + String(err) + ")"));
        }
        return false;
      }
      for (const listener of listeners) {
        listener.apply(this, args);
      }
      return true;
    }
    eventNames() {
      return Reflect.ownKeys(this._events).filter((name) => this._events[name] && this._events[name].length > 0);
    }
    listeners(eventName) {
      return this.rawListeners(eventName).map(unwrap);
    }
    rawListeners(eventName) {
      return listenersFor(this, eventName, false).slice();
    }
    listenerCount(eventName, listener) {
      const listeners = listenersFor(this, eventName, false);
      if (listener === undefined) {
        return listeners.length;
      }
      return listeners.filter((candidate) => candidate === listener || candidate.listener === listener).length;
    }
  }

  function once(emitter, eventName) {
    return new Promise(function (resolve, reject) {
      let settled = false;
      function cleanup() {
        if (emitter && typeof emitter.removeListener === "function") {
          emitter.removeListener(eventName, handler);
          if (eventName !== "error") {
            emitter.removeListener("error", errorHandler);
          }
        } else if (emitter && typeof emitter.removeEventListener === "function") {
          emitter.removeEventListener(eventName, eventHandler);
          if (eventName !== "error") {
            emitter.removeEventListener("error", eventErrorHandler);
          }
        }
      }
      function settle(fn, value) {
        if (settled) {
          return;
        }
        settled = true;
        cleanup();
        fn(value);
      }
      function handler() {
        settle(resolve, Array.prototype.slice.call(arguments));
      }
      function errorHandler(err) {
        settle(reject, err);
      }
      function eventHandler(event) {
        settle(resolve, [event]);
      }
      function eventErrorHandler(event) {
        settle(reject, event && event.error ? event.error : event);
      }
      if (emitter && typeof emitter.once === "function") {
        emitter.once(eventName, handler);
        if (eventName !== "error") {
          emitter.once("error", errorHandler);
        }
      } else if (emitter && typeof emitter.addEventListener === "function") {
        emitter.addEventListener(eventName, eventHandler, { once: true });
        if (eventName !== "error") {
          emitter.addEventListener("error", eventErrorHandler, { once: true });
        }
      } else {
        reject(new TypeError("The emitter must be an EventEmitter or EventTarget"));
      }
    });
  }

  function on(emitter, eventName) {
    const queue = [];
    const waiters = [];
    let closed = false;
    function push(args) {
      if (waiters.length > 0) {
        waiters.shift().resolve({ value: args, done: false });
      } else {
        queue.push(args);
      }
    }
    function handler() {
      push(Array.prototype.slice.call(arguments));
    }
    function cleanup() {
      if (emitter && typeof emitter.removeListener === "function") {
        emitter.removeListener(eventName, handler);
      }
      closed = true;
      while (waiters.length > 0) {
        waiters.shift().resolve({ value: undefined, done: true });
      }
    }
    if (!emitter || typeof emitter.on !== "function") {
      throw new TypeError("The emitter must be an EventEmitter");
    }
    emitter.on(eventName, handler);
    return {
      next() {
        if (queue.length > 0) {
          return Promise.resolve({ value: queue.shift(), done: false });
        }
        if (closed) {
          return Promise.resolve({ value: undefined, done: true });
        }
        return new Promise(function (resolve, reject) {
          waiters.push({ resolve, reject });
        });
      },
      return() {
        cleanup();
        return Promise.resolve({ value: undefined, done: true });
      },
      [Symbol.asyncIterator]() {
        return this;
      }
    };
  }

  function getEventListeners(emitter, eventName) {
    if (emitter instanceof EventEmitter) {
      return emitter.listeners(eventName);
    }
    if (emitter && emitter.__bruListeners) {
      return (emitter.__bruListeners[String(eventName)] || []).map(function (entry) { return entry.callback; });
    }
    return [];
  }

  function listenerCount(emitter, eventName) {
    if (emitter && typeof emitter.listenerCount === "function") {
      return emitter.listenerCount(eventName);
    }
    return getEventListeners(emitter, eventName).length;
  }

  function setMaxListeners(n) {
    const emitters = Array.prototype.slice.call(arguments, 1);
    for (const emitter of emitters) {
      if (emitter && typeof emitter.setMaxListeners === "function") {
        emitter.setMaxListeners(n);
      }
    }
  }

  function getMaxListeners(emitter) {
    if (emitter && typeof emitter.getMaxListeners === "function") {
      return emitter.getMaxListeners();
    }
    return EventEmitter.defaultMaxListeners;
  }

  EventEmitter.defaultMaxListeners = 10;
  EventEmitter.EventEmitter = EventEmitter;
  EventEmitter.default = EventEmitter;
  EventEmitter.once = once;
  EventEmitter.on = on;
  EventEmitter.listenerCount = listenerCount;
  EventEmitter.getEventListeners = getEventListeners;
  EventEmitter.setMaxListeners = setMaxListeners;
  EventEmitter.getMaxListeners = getMaxListeners;
  EventEmitter.errorMonitor = errorMonitor;
  EventEmitter.captureRejectionSymbol = captureRejectionSymbol;
  EventEmitter.addAbortListener = function (signal, listener) {
    validateListener(listener);
    if (signal && signal.aborted) {
      listener();
      return { dispose() {} };
    }
    if (signal && typeof signal.addEventListener === "function") {
      signal.addEventListener("abort", listener, { once: true });
      return { dispose() { signal.removeEventListener("abort", listener); } };
    }
    return { dispose() {} };
  };

  return EventEmitter;
})()`
	value, err := runtime.RunProgram(scriptEventsShim.compiled(script))
	if err != nil {
		panic(runtime.NewGoError(err))
	}
	return value
}

func newScriptStreamObject(runtime *goja.Runtime) goja.Value {
	script := `(function () {
  function callListener(listener, self, args) {
    if (typeof listener === "function") {
      listener.apply(self, args);
    }
  }

  class StreamBase {
    constructor() {
      Object.defineProperty(this, "_events", { value: {}, enumerable: false, writable: true });
    }
    on(event, listener) {
      const name = String(event);
      (this._events[name] || (this._events[name] = [])).push(listener);
      return this;
    }
    once(event, listener) {
      const self = this;
      function wrapped() {
        self.off(event, wrapped);
        return listener.apply(this, arguments);
      }
      return this.on(event, wrapped);
    }
    off(event, listener) {
      const listeners = this._events[String(event)];
      if (!listeners) return this;
      for (let index = listeners.length - 1; index >= 0; index--) {
        if (listeners[index] === listener) listeners.splice(index, 1);
      }
      return this;
    }
    removeListener(event, listener) {
      return this.off(event, listener);
    }
    emit(event) {
      const args = Array.prototype.slice.call(arguments, 1);
      const listeners = (this._events[String(event)] || []).slice();
      for (const listener of listeners) callListener(listener, this, args);
      return listeners.length > 0;
    }
    pipe(destination) {
      let chunk;
      while ((chunk = this.read()) !== null) {
        destination.write(chunk);
      }
      if (typeof destination.end === "function") destination.end();
      return destination;
    }
  }

  class Readable extends StreamBase {
    constructor(options) {
      super();
      options = options || {};
      this._readImpl = options.read;
      this._chunks = [];
      this.readable = true;
      this.writable = false;
    }
    static from(iterable) {
      const readable = new Readable();
      readable._chunks = Array.from(iterable || []);
      return readable;
    }
    read() {
      if (this._chunks.length > 0) return this._chunks.shift();
      if (typeof this._readImpl === "function") this._readImpl.call(this);
      return this._chunks.length > 0 ? this._chunks.shift() : null;
    }
    push(chunk) {
      if (chunk === null) {
        this.emit("end");
        return false;
      }
      this._chunks.push(chunk);
      this.emit("data", chunk);
      return true;
    }
  }

  class Writable extends StreamBase {
    constructor(options) {
      super();
      options = options || {};
      this._writeImpl = options.write;
      this._chunks = [];
      this.writable = true;
      this.readable = false;
    }
    write(chunk, encoding, callback) {
      if (typeof encoding === "function") {
        callback = encoding;
        encoding = undefined;
      }
      if (typeof this._writeImpl === "function") {
        this._writeImpl.call(this, chunk, encoding || "utf8", callback || function () {});
      } else {
        this._chunks.push(chunk);
        if (callback) callback();
      }
      this.emit("data", chunk);
      return true;
    }
    end(chunk, encoding, callback) {
      if (typeof chunk === "function") {
        callback = chunk;
        chunk = undefined;
      }
      if (chunk !== undefined && chunk !== null) this.write(chunk, encoding);
      this.emit("finish");
      if (callback) callback();
      return this;
    }
  }

  class Duplex extends Readable {
    constructor(options) {
      super(options);
      options = options || {};
      this._writeImpl = options.write;
      this.writable = true;
    }
    write(chunk, encoding, callback) {
      if (typeof encoding === "function") {
        callback = encoding;
        encoding = undefined;
      }
      if (typeof this._writeImpl === "function") {
        this._writeImpl.call(this, chunk, encoding || "utf8", callback || function () {});
      } else {
        if (callback) callback();
      }
      return true;
    }
    end(chunk, encoding, callback) {
      if (typeof chunk === "function") {
        callback = chunk;
        chunk = undefined;
      }
      if (chunk !== undefined && chunk !== null) this.write(chunk, encoding);
      this.emit("finish");
      if (callback) callback();
      return this;
    }
  }

  class Transform extends Duplex {
    constructor(options) {
      super(options);
      options = options || {};
      this._transformImpl = options.transform;
    }
    write(chunk, encoding, callback) {
      if (typeof encoding === "function") {
        callback = encoding;
        encoding = undefined;
      }
      if (typeof this._transformImpl === "function") {
        const self = this;
        this._transformImpl.call(this, chunk, encoding || "utf8", function (err, data) {
          if (err) {
            self.emit("error", err);
          } else if (data !== undefined && data !== null) {
            self.push(data);
          }
          if (callback) callback(err);
        });
      } else {
        this.push(chunk);
        if (callback) callback();
      }
      return true;
    }
  }

  function pipeline() {
    const args = Array.prototype.slice.call(arguments);
    const callback = typeof args[args.length - 1] === "function" ? args.pop() : null;
    try {
      for (let index = 0; index < args.length - 1; index++) {
        if (args[index] && typeof args[index].pipe === "function") {
          args[index].pipe(args[index + 1]);
        }
      }
      if (callback) callback(null);
    } catch (err) {
      if (callback) callback(err);
      else throw err;
    }
    return args[args.length - 1];
  }

  return { Stream: StreamBase, Readable, Writable, Duplex, Transform, pipeline };
})()`
	value, err := runtime.RunProgram(scriptStreamShim.compiled(script))
	if err != nil {
		panic(runtime.NewGoError(err))
	}
	return value
}

func newScriptStreamPromisesObject(runtime *goja.Runtime, streamObject goja.Value) goja.Value {
	const streamGlobal = "__liteapi_stream_for_promises__"
	global := runtime.GlobalObject()
	_ = global.Set(streamGlobal, streamObject)
	// Removing a global we just set cannot meaningfully fail.
	defer func() { _ = global.Delete(streamGlobal) }()
	script := `(function (stream) {
  function cleanup(stream, listeners) {
    if (!stream) return;
    const remove = typeof stream.off === "function" ? stream.off : stream.removeListener;
    if (typeof remove !== "function") return;
    for (const item of listeners) {
      remove.call(stream, item[0], item[1]);
    }
  }

  function finished(target, options) {
    options = options || {};
    return new Promise(function (resolve, reject) {
      if (!target || typeof target.on !== "function") {
        reject(new TypeError("The \"stream\" argument must be a stream"));
        return;
      }
      let settled = false;
      const listeners = [];
      function settle(fn, value) {
        if (settled) return;
        settled = true;
        cleanup(target, listeners);
        fn(value);
      }
      function add(event, listener) {
        listeners.push([event, listener]);
        if (typeof target.once === "function") {
          target.once(event, listener);
        } else {
          target.on(event, listener);
        }
      }
      add("error", function (err) {
        settle(reject, err || new Error("Stream error"));
      });
      add("finish", function () {
        settle(resolve, undefined);
      });
      add("end", function () {
        settle(resolve, undefined);
      });
      if (options.cleanup !== false) {
        add("close", function () {
          settle(resolve, undefined);
        });
      }
    });
  }

  function pipeline() {
    const args = Array.prototype.slice.call(arguments);
    return new Promise(function (resolve, reject) {
      try {
        stream.pipeline.apply(stream, args.concat(function (err) {
          if (err) reject(err);
          else resolve(undefined);
        }));
      } catch (err) {
        reject(err);
      }
    });
  }

  const promises = { finished, pipeline };
  promises.default = promises;
  return promises;
})(globalThis.__liteapi_stream_for_promises__)`
	value, err := runtime.RunProgram(scriptStreamPromisesShim.compiled(script))
	if err != nil {
		panic(runtime.NewGoError(err))
	}
	return value
}

type scriptAESCBCState struct {
	block       cipher.Block
	iv          []byte
	encrypt     bool
	autoPadding bool
	pending     []byte
	finalized   bool
}

func newScriptAESCBCObject(runtime *goja.Runtime, call goja.FunctionCall, encrypt bool) goja.Value {
	if len(call.Arguments) < 3 {
		panic(runtime.NewTypeError("algorithm, key, and iv are required"))
	}
	keyLength, err := scriptAESCBCKeyLength(call.Argument(0).String())
	if err != nil {
		panic(runtime.NewGoError(err))
	}
	key, err := scriptCryptoValueBytes(runtime, call.Argument(1), "")
	if err != nil {
		panic(runtime.NewGoError(err))
	}
	if len(key) != keyLength {
		panic(runtime.NewTypeError(fmt.Sprintf("Invalid key length: expected %d bytes", keyLength)))
	}
	iv, err := scriptCryptoValueBytes(runtime, call.Argument(2), "")
	if err != nil {
		panic(runtime.NewGoError(err))
	}
	if len(iv) != aes.BlockSize {
		panic(runtime.NewTypeError(fmt.Sprintf("Invalid initialization vector length: expected %d bytes", aes.BlockSize)))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		panic(runtime.NewGoError(err))
	}
	state := &scriptAESCBCState{
		block:       block,
		iv:          append([]byte(nil), iv...),
		encrypt:     encrypt,
		autoPadding: true,
	}
	object := runtime.NewObject()
	_ = object.Set("update", func(call goja.FunctionCall) goja.Value {
		if state.finalized {
			panic(runtime.NewTypeError("cipher already finalized"))
		}
		inputEncoding := ""
		outputEncoding := ""
		if len(call.Arguments) > 1 {
			inputEncoding = call.Argument(1).String()
		}
		if len(call.Arguments) > 2 {
			outputEncoding = call.Argument(2).String()
		}
		data, err := scriptCryptoValueBytes(runtime, call.Argument(0), inputEncoding)
		if err != nil {
			panic(runtime.NewGoError(err))
		}
		state.pending = append(state.pending, data...)
		return scriptCryptoDigestValue(runtime, nil, outputEncoding)
	})
	_ = object.Set("final", func(call goja.FunctionCall) goja.Value {
		if state.finalized {
			panic(runtime.NewTypeError("cipher already finalized"))
		}
		state.finalized = true
		outputEncoding := ""
		if len(call.Arguments) > 0 {
			outputEncoding = call.Argument(0).String()
		}
		var out []byte
		var err error
		if state.encrypt {
			out, err = state.encryptFinal()
		} else {
			out, err = state.decryptFinal()
		}
		if err != nil {
			panic(runtime.NewGoError(err))
		}
		return scriptCryptoDigestValue(runtime, out, outputEncoding)
	})
	_ = object.Set("setAutoPadding", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			state.autoPadding = true
		} else {
			state.autoPadding = call.Argument(0).ToBoolean()
		}
		return object
	})
	return object
}

func scriptAESCBCKeyLength(algorithm string) (int, error) {
	switch normalizeScriptCryptoAlgorithm(algorithm) {
	case "aes128cbc":
		return 16, nil
	case "aes192cbc":
		return 24, nil
	case "aes256cbc":
		return 32, nil
	default:
		return 0, fmt.Errorf("unsupported cipher algorithm: %s", algorithm)
	}
}

func (state *scriptAESCBCState) encryptFinal() ([]byte, error) {
	data := append([]byte(nil), state.pending...)
	if state.autoPadding {
		data = ScriptPKCS7Pad(data, state.block.BlockSize())
	} else if len(data)%state.block.BlockSize() != 0 {
		return nil, errors.New("data is not a multiple of block length")
	}
	out := make([]byte, len(data))
	cipher.NewCBCEncrypter(state.block, state.iv).CryptBlocks(out, data)
	return out, nil
}

func (state *scriptAESCBCState) decryptFinal() ([]byte, error) {
	if len(state.pending)%state.block.BlockSize() != 0 {
		return nil, errors.New("encrypted data is not a multiple of block length")
	}
	out := make([]byte, len(state.pending))
	cipher.NewCBCDecrypter(state.block, state.iv).CryptBlocks(out, state.pending)
	if state.autoPadding {
		return ScriptPKCS7Unpad(out, state.block.BlockSize())
	}
	return out, nil
}

func scriptRandomUUID() string {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		panic(err)
	}
	bytes[6] = (bytes[6] & 0x0f) | 0x40
	bytes[8] = (bytes[8] & 0x3f) | 0x80
	hexed := hex.EncodeToString(bytes)
	return hexed[0:8] + "-" + hexed[8:12] + "-" + hexed[12:16] + "-" + hexed[16:20] + "-" + hexed[20:32]
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

func ScriptPKCS7Pad(data []byte, blockSize int) []byte {
	padding := blockSize - len(data)%blockSize
	if padding == 0 {
		padding = blockSize
	}
	out := make([]byte, len(data)+padding)
	copy(out, data)
	for index := len(data); index < len(out); index++ {
		out[index] = byte(padding)
	}
	return out
}

func ScriptPKCS7Unpad(data []byte, blockSize int) ([]byte, error) {
	if len(data) == 0 || len(data)%blockSize != 0 {
		return nil, errors.New("invalid PKCS#7 data")
	}
	padding := int(data[len(data)-1])
	if padding == 0 || padding > blockSize || padding > len(data) {
		return nil, errors.New("invalid PKCS#7 padding")
	}
	for _, value := range data[len(data)-padding:] {
		if int(value) != padding {
			return nil, errors.New("invalid PKCS#7 padding")
		}
	}
	return data[:len(data)-padding], nil
}

func scriptNanoID(size int) string {
	if size < 0 {
		size = 0
	}
	const alphabet = "_-0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
	bytes := make([]byte, size)
	if _, err := rand.Read(bytes); err != nil {
		panic(err)
	}
	var builder strings.Builder
	builder.Grow(size)
	for _, value := range bytes {
		builder.WriteByte(alphabet[int(value)%len(alphabet)])
	}
	return builder.String()
}

func newScriptJWTObject(runtime *goja.Runtime) *goja.Object {
	jwtObject := runtime.NewObject()
	_ = jwtObject.Set("sign", func(call goja.FunctionCall) goja.Value {
		callback, callbackIndex := scriptOptionalCallback(call.Arguments)
		optionsIndex := 2
		if callbackIndex == 2 {
			optionsIndex = -1
		}
		token, err := scriptJWTSign(call.Argument(0), call.Argument(1), scriptOptionalArgument(call.Arguments, optionsIndex))
		if callback != nil {
			callScriptCallback(runtime, callback, err, runtime.ToValue(token))
			return goja.Undefined()
		}
		if err != nil {
			panic(runtime.NewGoError(err))
		}
		return runtime.ToValue(token)
	})
	_ = jwtObject.Set("verify", func(call goja.FunctionCall) goja.Value {
		callback, callbackIndex := scriptOptionalCallback(call.Arguments)
		optionsIndex := 2
		if callbackIndex == 2 {
			optionsIndex = -1
		}
		decoded, err := scriptJWTVerify(call.Argument(0), call.Argument(1), scriptOptionalArgument(call.Arguments, optionsIndex))
		if callback != nil {
			callScriptCallback(runtime, callback, err, runtime.ToValue(decoded))
			return goja.Undefined()
		}
		if err != nil {
			panic(runtime.NewGoError(err))
		}
		return runtime.ToValue(decoded)
	})
	_ = jwtObject.Set("decode", func(call goja.FunctionCall) goja.Value {
		decoded, err := scriptJWTDecode(call.Argument(0), call.Argument(1))
		if err != nil {
			panic(runtime.NewGoError(err))
		}
		return runtime.ToValue(decoded)
	})
	return jwtObject
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

func scriptJWTSign(payloadValue, secretValue, optionsValue goja.Value) (string, error) {
	secret, err := scriptJWTSecret(secretValue)
	if err != nil {
		return "", err
	}
	options := scriptJWTOptionsFromValue(optionsValue)
	claims, err := scriptJWTClaims(payloadValue)
	if err != nil {
		return "", err
	}
	now := time.Now()
	if _, ok := claims["iat"]; !ok && !options.NoTimestamp {
		claims["iat"] = float64(now.Unix())
	}
	if options.ExpiresIn != 0 {
		claims["exp"] = float64(now.Add(options.ExpiresIn).Unix())
	}
	if options.NotBefore != 0 {
		claims["nbf"] = float64(now.Add(options.NotBefore).Unix())
	}
	if options.Issuer != "" {
		claims["iss"] = options.Issuer
	}
	if options.Subject != "" {
		claims["sub"] = options.Subject
	}
	if options.Audience != nil {
		claims["aud"] = options.Audience
	}
	method, err := scriptJWTSigningMethod(options.Algorithm)
	if err != nil {
		return "", err
	}
	token := jwtlib.NewWithClaims(method, jwtlib.MapClaims(claims))
	return token.SignedString([]byte(secret))
}

func scriptJWTVerify(tokenValue, secretValue, optionsValue goja.Value) (map[string]interface{}, error) {
	secret, err := scriptJWTSecret(secretValue)
	if err != nil {
		return nil, err
	}
	tokenText := tokenValue.String()
	if strings.Count(tokenText, ".") != 2 {
		return nil, errors.New("jwt malformed")
	}
	options := scriptJWTOptionsFromValue(optionsValue)
	parserOptions := []jwtlib.ParserOption{jwtlib.WithValidMethods(options.Algorithms)}
	if options.IgnoreExpiration {
		parserOptions = append(parserOptions, jwtlib.WithoutClaimsValidation())
	}
	parser := jwtlib.NewParser(parserOptions...)
	claims := jwtlib.MapClaims{}
	token, err := parser.ParseWithClaims(tokenText, claims, func(token *jwtlib.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwtlib.SigningMethodHMAC); !ok {
			return nil, errors.New("invalid algorithm")
		}
		return []byte(secret), nil
	})
	if err != nil {
		return nil, scriptJWTError(err)
	}
	if token == nil || !token.Valid {
		return nil, errors.New("invalid token")
	}
	normalized, err := normalizeJSONValue(map[string]interface{}(claims))
	if err != nil {
		return nil, err
	}
	result, _ := normalized.(map[string]interface{})
	return result, nil
}

func scriptJWTDecode(tokenValue, optionsValue goja.Value) (interface{}, error) {
	tokenText := tokenValue.String()
	if strings.Count(tokenText, ".") != 2 {
		return nil, errors.New("jwt malformed")
	}
	token, _, err := jwtlib.NewParser().ParseUnverified(tokenText, jwtlib.MapClaims{})
	if err != nil {
		return nil, scriptJWTError(err)
	}
	claims, _ := token.Claims.(jwtlib.MapClaims)
	normalizedClaims, err := normalizeJSONValue(map[string]interface{}(claims))
	if err != nil {
		return nil, err
	}
	options := scriptJWTOptionsFromValue(optionsValue)
	if options.Complete {
		header, err := normalizeJSONValue(token.Header)
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{"header": header, "payload": normalizedClaims, "signature": strings.Split(tokenText, ".")[2]}, nil
	}
	return normalizedClaims, nil
}

type scriptJWTOptions struct {
	Algorithm        string
	Algorithms       []string
	ExpiresIn        time.Duration
	NotBefore        time.Duration
	Issuer           string
	Subject          string
	Audience         interface{}
	IgnoreExpiration bool
	NoTimestamp      bool
	Complete         bool
}

func scriptJWTOptionsFromValue(value goja.Value) scriptJWTOptions {
	options := scriptJWTOptions{Algorithm: "HS256", Algorithms: []string{"HS256", "HS384", "HS512"}}
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) || value.Export() == nil {
		return options
	}
	exported, ok := value.Export().(map[string]interface{})
	if !ok {
		return options
	}
	if algorithm, ok := exported["algorithm"].(string); ok && strings.TrimSpace(algorithm) != "" {
		options.Algorithm = algorithm
	}
	if algorithms, ok := exported["algorithms"].([]interface{}); ok && len(algorithms) > 0 {
		options.Algorithms = make([]string, 0, len(algorithms))
		for _, algorithm := range algorithms {
			options.Algorithms = append(options.Algorithms, fmt.Sprint(algorithm))
		}
	}
	if expiresIn, ok := exported["expiresIn"]; ok {
		options.ExpiresIn = parseScriptJWTDuration(expiresIn)
	}
	if notBefore, ok := exported["notBefore"]; ok {
		options.NotBefore = parseScriptJWTDuration(notBefore)
	}
	if issuer, ok := exported["issuer"].(string); ok {
		options.Issuer = issuer
	}
	if subject, ok := exported["subject"].(string); ok {
		options.Subject = subject
	}
	if audience, ok := exported["audience"]; ok {
		options.Audience = audience
	}
	if ignoreExpiration, ok := exported["ignoreExpiration"].(bool); ok {
		options.IgnoreExpiration = ignoreExpiration
	}
	if noTimestamp, ok := exported["noTimestamp"].(bool); ok {
		options.NoTimestamp = noTimestamp
	}
	if complete, ok := exported["complete"].(bool); ok {
		options.Complete = complete
	}
	return options
}

func scriptJWTSecret(value goja.Value) (string, error) {
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) || strings.TrimSpace(value.String()) == "" {
		return "", errors.New("secret or public key must be provided")
	}
	return value.String(), nil
}

func scriptJWTClaims(value goja.Value) (map[string]interface{}, error) {
	exported := value.Export()
	if exported == nil {
		return map[string]interface{}{}, nil
	}
	normalized, err := normalizeJSONValue(exported)
	if err != nil {
		return nil, err
	}
	switch typed := normalized.(type) {
	case map[string]interface{}:
		return typed, nil
	case string:
		return map[string]interface{}{"data": typed}, nil
	default:
		return nil, errors.New("payload must be an object")
	}
}

func scriptJWTSigningMethod(algorithm string) (*jwtlib.SigningMethodHMAC, error) {
	switch strings.ToUpper(scalar.FirstNonEmpty(algorithm, "HS256")) {
	case "HS256":
		return jwtlib.SigningMethodHS256, nil
	case "HS384":
		return jwtlib.SigningMethodHS384, nil
	case "HS512":
		return jwtlib.SigningMethodHS512, nil
	default:
		return nil, fmt.Errorf("algorithm %s is not supported", algorithm)
	}
}

func parseScriptJWTDuration(value interface{}) time.Duration {
	switch typed := value.(type) {
	case int:
		return time.Duration(typed) * time.Second
	case int64:
		return time.Duration(typed) * time.Second
	case float64:
		return time.Duration(typed) * time.Second
	case string:
		text := strings.TrimSpace(typed)
		if text == "" {
			return 0
		}
		if seconds, err := strconv.ParseFloat(text, 64); err == nil {
			return time.Duration(seconds) * time.Second
		}
		unit := text[len(text)-1]
		amount, err := strconv.ParseFloat(strings.TrimSpace(text[:len(text)-1]), 64)
		if err != nil {
			return 0
		}
		switch unit {
		case 's', 'S':
			return time.Duration(amount * float64(time.Second))
		case 'm', 'M':
			return time.Duration(amount * float64(time.Minute))
		case 'h', 'H':
			return time.Duration(amount * float64(time.Hour))
		case 'd', 'D':
			return time.Duration(amount * float64(24*time.Hour))
		}
	}
	return 0
}

func scriptJWTError(err error) error {
	switch {
	case errors.Is(err, jwtlib.ErrTokenMalformed):
		return errors.New("jwt malformed")
	case errors.Is(err, jwtlib.ErrTokenSignatureInvalid):
		if strings.Contains(strings.ToLower(err.Error()), "signing method") {
			return errors.New("invalid algorithm")
		}
		return errors.New("invalid signature")
	case errors.Is(err, jwtlib.ErrTokenExpired):
		return errors.New("jwt expired")
	case errors.Is(err, jwtlib.ErrTokenNotValidYet):
		return errors.New("jwt not active")
	default:
		return err
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

func (j *CookieJar) Snapshot() []types.CookieEntry {
	if j == nil {
		return nil
	}
	return CloneCookieEntries(j.cookies)
}

func (j *CookieJar) matching(rawURL string) []types.CookieEntry {
	if j == nil {
		return nil
	}
	return cookiejar.ForURL(j.cookies, rawURL)
}

func (j *CookieJar) upsert(cookie types.CookieEntry, sourceHosts ...string) {
	if j == nil || cookie.Name == "" || cookie.Domain == "" {
		return
	}
	sourceHost := ""
	if len(sourceHosts) > 0 {
		sourceHost = sourceHosts[0]
	}
	now := time.Now()
	if cookie.Path == "" {
		cookie.Path = "/"
	}
	if cookie.ID == "" {
		cookie.ID = cookiejar.ID(cookie)
	}
	if err := cookiejar.ValidateForStorage(cookie, sourceHost); err != nil {
		return
	}
	if cookie.CreatedAt.IsZero() {
		cookie.CreatedAt = now
	}
	cookie.UpdatedAt = now
	key := cookiejar.Key(cookie)
	next := j.cookies[:0]
	for _, existing := range j.cookies {
		if cookiejar.Key(existing) == key {
			if !existing.CreatedAt.IsZero() {
				cookie.CreatedAt = existing.CreatedAt
			}
			continue
		}
		next = append(next, existing)
	}
	if cookiejar.Expired(cookie, now) {
		j.cookies = next
		return
	}
	j.cookies = append(next, cookie)
}

func (j *CookieJar) UpsertAll(cookies []types.CookieEntry) {
	for _, cookie := range cookies {
		j.upsert(cookie)
	}
}

func (j *CookieJar) removeMatching(rawURL, name string) {
	if j == nil || strings.TrimSpace(name) == "" {
		return
	}
	removeKeys := map[string]bool{}
	for _, cookie := range j.matching(rawURL) {
		if cookie.Name == name {
			removeKeys[cookiejar.Key(cookie)] = true
		}
	}
	if len(removeKeys) == 0 {
		return
	}
	next := j.cookies[:0]
	for _, cookie := range j.cookies {
		if !removeKeys[cookiejar.Key(cookie)] {
			next = append(next, cookie)
		}
	}
	j.cookies = next
}

func (j *CookieJar) clearMatching(rawURL string) {
	if j == nil {
		return
	}
	removeKeys := map[string]bool{}
	for _, cookie := range j.matching(rawURL) {
		removeKeys[cookiejar.Key(cookie)] = true
	}
	if len(removeKeys) == 0 {
		return
	}
	next := j.cookies[:0]
	for _, cookie := range j.cookies {
		if !removeKeys[cookiejar.Key(cookie)] {
			next = append(next, cookie)
		}
	}
	j.cookies = next
}

func cookieSourceHost(rawURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return ""
	}
	return parsed.Hostname()
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

func installScriptEventTarget(runtime *goja.Runtime) {
	script := `(function () {
  if (typeof globalThis.Event === "function" && typeof globalThis.EventTarget === "function" && typeof globalThis.CustomEvent === "function") {
    return;
  }

  function eventOption(options, name) {
    if (options === undefined || options === null) {
      return false;
    }
    if (typeof options === "boolean") {
      return name === "capture" ? options : false;
    }
    return !!options[name];
  }

  function Event(type, options) {
    if (!(this instanceof Event)) {
      throw new TypeError("Class constructor Event cannot be invoked without 'new'");
    }
    options = options || {};
    this.type = String(type);
    this.bubbles = !!options.bubbles;
    this.cancelable = !!options.cancelable;
    this.composed = !!options.composed;
    this.defaultPrevented = false;
    this.eventPhase = 0;
    this.isTrusted = false;
    this.target = null;
    this.currentTarget = null;
    this.timeStamp = Date.now();
    this.cancelBubble = false;
    this._immediateStopped = false;
  }
  Event.prototype.preventDefault = function () {
    if (this.cancelable) {
      this.defaultPrevented = true;
    }
  };
  Event.prototype.stopPropagation = function () {
    this.cancelBubble = true;
  };
  Event.prototype.stopImmediatePropagation = function () {
    this.cancelBubble = true;
    this._immediateStopped = true;
  };
  Event.prototype.composedPath = function () {
    return this.target ? [this.target] : [];
  };

  function CustomEvent(type, options) {
    if (!(this instanceof CustomEvent)) {
      throw new TypeError("Class constructor CustomEvent cannot be invoked without 'new'");
    }
    Event.call(this, type, options);
    this.detail = options && Object.prototype.hasOwnProperty.call(options, "detail") ? options.detail : null;
  }
  CustomEvent.prototype = Object.create(Event.prototype);
  CustomEvent.prototype.constructor = CustomEvent;

  function EventTarget() {
    if (!(this instanceof EventTarget)) {
      throw new TypeError("Class constructor EventTarget cannot be invoked without 'new'");
    }
    Object.defineProperty(this, "__bruListeners", { value: {}, enumerable: false, configurable: false, writable: false });
  }
  EventTarget.prototype.addEventListener = function (type, callback, options) {
    if (callback === undefined || callback === null) {
      return;
    }
    type = String(type);
    const capture = eventOption(options, "capture");
    const once = eventOption(options, "once");
    const listeners = this.__bruListeners[type] || (this.__bruListeners[type] = []);
    for (const listener of listeners) {
      if (listener.callback === callback && listener.capture === capture) {
        return;
      }
    }
    listeners.push({ callback, capture, once });
  };
  EventTarget.prototype.removeEventListener = function (type, callback, options) {
    type = String(type);
    const capture = eventOption(options, "capture");
    const listeners = this.__bruListeners[type];
    if (!listeners) {
      return;
    }
    for (let index = listeners.length - 1; index >= 0; index--) {
      const listener = listeners[index];
      if (listener.callback === callback && listener.capture === capture) {
        listeners.splice(index, 1);
      }
    }
  };
  EventTarget.prototype.dispatchEvent = function (event) {
    if (!(event instanceof Event)) {
      throw new TypeError("The event must be an Event");
    }
    event.target = event.target || this;
    event.currentTarget = this;
    const listeners = (this.__bruListeners[event.type] || []).slice();
    for (const listener of listeners) {
      if (listener.once) {
        this.removeEventListener(event.type, listener.callback, { capture: listener.capture });
      }
      if (typeof listener.callback === "function") {
        listener.callback.call(this, event);
      } else if (listener.callback && typeof listener.callback.handleEvent === "function") {
        listener.callback.handleEvent.call(listener.callback, event);
      }
      if (event._immediateStopped) {
        break;
      }
    }
    event.currentTarget = null;
    return !event.defaultPrevented;
  };

  globalThis.Event = Event;
  globalThis.CustomEvent = CustomEvent;
  globalThis.EventTarget = EventTarget;
})()`
	if _, err := runtime.RunProgram(scriptEventTargetShim.compiled(script)); err != nil {
		panic(runtime.NewGoError(err))
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

func scriptAsyncWrapper(script, sandboxMode string) string {
	if NormalizeJSSandboxMode(sandboxMode) == "developer" {
		return "(async () => {\nawait bru.sleep(0);\n" + script + "\n})()"
	}
	return "(async () => {\nconst setTimeout = globalThis.__bruSetTimeout;\ntry { delete globalThis.__bruSetTimeout; } catch (_) {}\nawait bru.sleep(0);\n" + script + "\n})()"
}

func scriptPromiseFromValue(value goja.Value) (*goja.Promise, bool) {
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		return nil, false
	}
	promise, ok := value.Export().(*goja.Promise)
	return promise, ok
}

func scriptPromisePendingOrError(runtime *goja.Runtime, value goja.Value) (bool, error) {
	promise, ok := scriptPromiseFromValue(value)
	if !ok {
		return false, nil
	}
	switch promise.State() {
	case goja.PromiseStateRejected:
		return false, errors.New(scriptPromiseRejectionMessage(runtime, promise.Result()))
	case goja.PromiseStatePending:
		return true, nil
	default:
		return false, nil
	}
}

func scriptDrainRuntime(runtime *goja.Runtime, value goja.Value, deadline time.Time) error {
	loop := scriptEventLoopForRuntime(runtime)
	for {
		pendingPromise, err := scriptPromisePendingOrError(runtime, value)
		if err != nil {
			return err
		}
		pendingTests := loop != nil && loop.pendingTests > 0
		pendingTimers := loop != nil && len(loop.timers) > 0
		if !pendingPromise && !pendingTests && !pendingTimers {
			return nil
		}
		if loop == nil || len(loop.timers) == 0 {
			if pendingPromise {
				return errors.New("script promise did not settle")
			}
			return errors.New("script async tests did not settle")
		}
		if time.Now().After(deadline) {
			return errors.New("script timeout")
		}
		if err := loop.runNextTimer(deadline); err != nil {
			return err
		}
	}
}

func scriptPromiseRejectionMessage(runtime *goja.Runtime, value goja.Value) string {
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		return "promise rejected"
	}
	object := value.ToObject(runtime)
	if message := object.Get("message"); message != nil && !goja.IsUndefined(message) && !goja.IsNull(message) {
		return message.String()
	}
	return value.String()
}

func scriptResolvedPromise(runtime *goja.Runtime, value goja.Value) goja.Value {
	if value == nil {
		value = goja.Undefined()
	}
	promise, resolve, _ := runtime.NewPromise()
	if err := resolve(value); err != nil {
		panic(err)
	}
	return runtime.ToValue(promise)
}

func scriptRejectedPromise(runtime *goja.Runtime, reason interface{}) goja.Value {
	promise, _, reject := runtime.NewPromise()
	if err := reject(reason); err != nil {
		panic(err)
	}
	return runtime.ToValue(promise)
}

func scriptAttachResolvedThenable(runtime *goja.Runtime, object *goja.Object) {
	if object == nil {
		return
	}
	plainValue := func() goja.Value {
		clone := runtime.NewObject()
		for _, key := range object.Keys() {
			if key == "then" || key == "catch" || key == "finally" {
				continue
			}
			_ = clone.Set(key, object.Get(key))
		}
		return clone
	}
	then := func(call goja.FunctionCall) goja.Value {
		value := plainValue()
		onFulfilled, ok := goja.AssertFunction(call.Argument(0))
		if !ok {
			return scriptResolvedPromise(runtime, value)
		}
		result, err := onFulfilled(goja.Undefined(), value)
		if err != nil {
			return scriptRejectedPromise(runtime, err)
		}
		return scriptResolvedPromise(runtime, result)
	}
	catchFn := func(goja.FunctionCall) goja.Value {
		return scriptResolvedPromise(runtime, plainValue())
	}
	finallyFn := func(call goja.FunctionCall) goja.Value {
		if callback, ok := goja.AssertFunction(call.Argument(0)); ok {
			if _, err := callback(goja.Undefined()); err != nil {
				return scriptRejectedPromise(runtime, err)
			}
		}
		return scriptResolvedPromise(runtime, plainValue())
	}
	_ = object.DefineDataProperty("then", runtime.ToValue(then), goja.FLAG_TRUE, goja.FLAG_TRUE, goja.FLAG_FALSE)
	_ = object.DefineDataProperty("catch", runtime.ToValue(catchFn), goja.FLAG_TRUE, goja.FLAG_TRUE, goja.FLAG_FALSE)
	_ = object.DefineDataProperty("finally", runtime.ToValue(finallyFn), goja.FLAG_TRUE, goja.FLAG_TRUE, goja.FLAG_FALSE)
}

func scriptAttachAsyncTestResult(runtime *goja.Runtime, value goja.Value, testResults *[]types.TestResult, name string) bool {
	promise, ok := scriptPromiseFromValue(value)
	if !ok {
		return false
	}
	if testResults == nil {
		return true
	}
	loop := scriptEventLoopForRuntime(runtime)
	if loop != nil {
		loop.addPendingTest()
	}
	resultIndex := len(*testResults)
	*testResults = append(*testResults, types.TestResult{Name: name, Passed: false, Message: "pending"})
	setResult := func(passed bool, message string) {
		if resultIndex >= 0 && resultIndex < len(*testResults) {
			(*testResults)[resultIndex] = types.TestResult{Name: name, Passed: passed, Message: message}
		}
	}
	complete := func(passed bool, message string) {
		setResult(passed, message)
		if loop != nil {
			loop.finishPendingTest()
		}
	}
	promiseObject := runtime.ToValue(promise).ToObject(runtime)
	then, ok := goja.AssertFunction(promiseObject.Get("then"))
	if !ok {
		complete(false, "test returned a non-callable promise")
		return true
	}
	onFulfilled := func(goja.FunctionCall) goja.Value {
		complete(true, "passed")
		return goja.Undefined()
	}
	onRejected := func(call goja.FunctionCall) goja.Value {
		complete(false, scriptPromiseRejectionMessage(runtime, call.Argument(0)))
		return goja.Undefined()
	}
	if _, err := then(runtime.ToValue(promise), runtime.ToValue(onFulfilled), runtime.ToValue(onRejected)); err != nil {
		complete(false, err.Error())
	}
	return true
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

func responseJSONValue(body string) (interface{}, bool) {
	var value interface{}
	if err := json.Unmarshal([]byte(body), &value); err != nil {
		return nil, false
	}
	return value, true
}

func responseQueryProperty(values []interface{}, name string) []interface{} {
	result := []interface{}{}
	for _, value := range values {
		switch typed := value.(type) {
		case map[string]interface{}:
			if next, ok := typed[name]; ok {
				result = append(result, next)
			}
		case []interface{}:
			for _, item := range typed {
				if itemMap, ok := item.(map[string]interface{}); ok {
					if next, ok := itemMap[name]; ok {
						result = append(result, next)
					}
				}
			}
		}
	}
	return result
}

func responseQueryFilter(runtime *goja.Runtime, values []interface{}, filter goja.Callable) []interface{} {
	items := []interface{}{}
	for _, value := range values {
		if list, ok := value.([]interface{}); ok {
			items = append(items, list...)
		} else {
			items = append(items, value)
		}
	}
	if filter == nil {
		return items
	}
	result := []interface{}{}
	for _, item := range items {
		matched, err := filter(goja.Undefined(), runtime.ToValue(item))
		if err != nil {
			panic(err)
		}
		if matched.ToBoolean() {
			result = append(result, item)
		}
	}
	return result
}

func responseQueryIndex(values []interface{}, index int) (interface{}, bool) {
	if len(values) == 1 {
		if list, ok := values[0].([]interface{}); ok {
			if index >= 0 && index < len(list) {
				return list[index], true
			}
			return nil, false
		}
	}
	if index >= 0 && index < len(values) {
		return values[index], true
	}
	return nil, false
}

func responseQueryResult(values []interface{}) (interface{}, bool) {
	switch len(values) {
	case 0:
		return nil, false
	case 1:
		return values[0], true
	default:
		return values, true
	}
}

func responseJQProperty(value interface{}, name string) (interface{}, bool) {
	switch typed := value.(type) {
	case map[string]interface{}:
		next, ok := typed[name]
		return next, ok
	case []interface{}:
		result := []interface{}{}
		for _, item := range typed {
			if itemMap, ok := item.(map[string]interface{}); ok {
				if next, ok := itemMap[name]; ok {
					result = append(result, next)
				}
			}
		}
		return result, len(result) > 0
	default:
		return nil, false
	}
}

func responseJQMatchesFilter(value interface{}, filter string) bool {
	operators := []string{">=", "<=", "!=", "==", ">", "<", "="}
	for _, operator := range operators {
		if index := strings.Index(filter, operator); index >= 0 {
			left := strings.TrimSpace(filter[:index])
			right := strings.TrimSpace(filter[index+len(operator):])
			itemMap, ok := value.(map[string]interface{})
			if !ok {
				return false
			}
			actual, ok := itemMap[left]
			if !ok {
				return false
			}
			return compareResponseJQValues(actual, operator, parseResponseJQLiteral(right))
		}
	}
	return false
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

func responseDataBytes(response types.Response) []byte {
	if strings.TrimSpace(response.BodyBase64) != "" {
		if decoded, err := base64.StdEncoding.DecodeString(response.BodyBase64); err == nil {
			return decoded
		}
	}
	return []byte(response.Body)
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

func scriptBufferValue(runtime *goja.Runtime, data []byte) goja.Value {
	byteArray := scriptByteArrayValue(runtime, data)
	bufferValue := runtime.Get("Buffer")
	if bufferValue == nil || goja.IsUndefined(bufferValue) || goja.IsNull(bufferValue) {
		return byteArray
	}
	bufferObject := bufferValue.ToObject(runtime)
	from, ok := goja.AssertFunction(bufferObject.Get("from"))
	if !ok {
		return byteArray
	}
	value, err := from(bufferValue, byteArray)
	if err != nil {
		return byteArray
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

func BuildVariableMap(globalEnvs []types.Environment, collection *types.Collection, environmentID string, item types.RequestItem, workspacePath ...string) map[string]string {
	vars := map[string]string{}
	addVars := func(values []types.Variable) {
		for _, variable := range values {
			if !variable.Enabled || variable.Name == "" {
				continue
			}
			vars[variable.Name] = fmt.Sprint(variable.Value)
		}
	}
	for _, env := range globalEnvs {
		addVars(env.Variables)
	}
	addVars(collection.Variables)
	if environmentID != "" {
		for _, env := range collection.Environments {
			if env.ID == environmentID {
				addVars(env.Variables)
				break
			}
		}
	}
	for _, folder := range FolderChain(*collection, item) {
		addVars(folder.Variables)
	}
	addVars(item.Vars.Req)
	addVars(collection.RuntimeVariables)
	addProcessEnvVars(vars, ProcessEnvForCollection(collection, firstString(workspacePath)))
	return vars
}

func ActiveGlobalEnvironmentsForWorkspace(workspace types.Workspace) []types.Environment {
	if len(workspace.GlobalEnvironments) == 0 {
		return nil
	}
	if workspace.ActiveGlobalEnvironmentID == "" {
		return nil
	}
	for _, env := range workspace.GlobalEnvironments {
		if env.ID == workspace.ActiveGlobalEnvironmentID {
			return []types.Environment{env}
		}
	}
	return nil
}

func WorkspaceHasGlobalEnvironment(workspace types.Workspace, environmentID string) bool {
	if environmentID == "" {
		return true
	}
	for _, env := range workspace.GlobalEnvironments {
		if env.ID == environmentID {
			return true
		}
	}
	return false
}

func CloneEnvironmentWithNewIDs(env types.Environment, idPrefix string) types.Environment {
	cloned := env
	cloned.ID = scalar.NewID(idPrefix)
	if len(env.Variables) > 0 {
		cloned.Variables = append([]types.Variable(nil), env.Variables...)
		for index := range cloned.Variables {
			cloned.Variables[index].ID = scalar.NewID("var")
		}
	}
	return cloned
}

func UniqueEnvironmentName(environments []types.Environment, desired string) string {
	desired = strings.TrimSpace(desired)
	if desired == "" {
		desired = "Environment"
	}
	used := map[string]bool{}
	for _, env := range environments {
		used[strings.ToLower(strings.TrimSpace(env.Name))] = true
	}
	if !used[strings.ToLower(desired)] {
		return desired
	}
	for index := 1; ; index++ {
		candidate := desired + " copy"
		if index > 1 {
			candidate = fmt.Sprintf("%s copy %d", desired, index)
		}
		if !used[strings.ToLower(candidate)] {
			return candidate
		}
	}
}

func NewScriptVariableContext(globalEnvs []types.Environment, collection *types.Collection, environmentID string, item types.RequestItem, promptValues map[string]string, workspacePath ...string) *VariableContext {
	ctx := &VariableContext{
		Runtime:    map[string]interface{}{},
		Env:        map[string]interface{}{},
		Global:     map[string]interface{}{},
		Collection: map[string]interface{}{},
		Folder:     map[string]interface{}{},
		Request:    map[string]interface{}{},
		Data:       map[string]interface{}{},
		Prompt:     promptVariableMap(promptValues),
		ProcessEnv: ProcessEnvForCollection(collection, firstString(workspacePath)),
		Combined:   map[string]string{},
	}
	for _, env := range globalEnvs {
		mergeVariableMap(ctx.Global, env.Variables)
	}
	if collection != nil {
		mergeVariableMap(ctx.Collection, collection.Variables)
		mergeVariableMap(ctx.Runtime, collection.RuntimeVariables)
		for _, folder := range FolderChain(*collection, item) {
			mergeVariableMap(ctx.Folder, folder.Variables)
		}
		if environmentID != "" {
			for _, env := range collection.Environments {
				if env.ID == environmentID {
					mergeVariableMap(ctx.Env, env.Variables)
					break
				}
			}
		}
	}
	mergeVariableMap(ctx.Request, item.Vars.Req)
	ctx.Recompute()
	return ctx
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

func NewFlatScriptVariableContext(vars map[string]string) *VariableContext {
	ctx := &VariableContext{
		Runtime:    map[string]interface{}{},
		Env:        map[string]interface{}{},
		Global:     map[string]interface{}{},
		Collection: map[string]interface{}{},
		Folder:     map[string]interface{}{},
		Request:    map[string]interface{}{},
		Prompt:     map[string]interface{}{},
		ProcessEnv: interp.ProcessEnvFromCombinedVars(vars),
		Combined:   vars,
	}
	for name, value := range vars {
		if strings.HasPrefix(name, interp.ProcessEnvPrefix) {
			continue
		}
		ctx.Runtime[name] = value
	}
	ctx.Recompute()
	return ctx
}

func ScriptVariableContextForItem(parent *VariableContext, collection *types.Collection, environmentID string, item types.RequestItem) *VariableContext {
	if parent == nil {
		return NewScriptVariableContext(nil, collection, environmentID, item, nil)
	}
	ctx := &VariableContext{
		Runtime:         parent.Runtime,
		Env:             parent.Env,
		Global:          parent.Global,
		Collection:      parent.Collection,
		Folder:          map[string]interface{}{},
		Request:         map[string]interface{}{},
		Prompt:          parent.Prompt,
		ProcessEnv:      parent.ProcessEnv,
		Combined:        map[string]string{},
		RuntimeDirty:    parent.RuntimeDirty,
		EnvDirty:        parent.EnvDirty,
		GlobalDirty:     parent.GlobalDirty,
		CollectionDirty: parent.CollectionDirty,
	}
	if collection != nil {
		for _, folder := range FolderChain(*collection, item) {
			mergeVariableMap(ctx.Folder, folder.Variables)
		}
	}
	mergeVariableMap(ctx.Request, item.Vars.Req)
	ctx.Recompute()
	return ctx
}

func ScriptMergeVariableContext(parent, child *VariableContext) {
	if parent == nil || child == nil {
		return
	}
	parent.RuntimeDirty = parent.RuntimeDirty || child.RuntimeDirty
	parent.EnvDirty = parent.EnvDirty || child.EnvDirty
	parent.GlobalDirty = parent.GlobalDirty || child.GlobalDirty
	parent.CollectionDirty = parent.CollectionDirty || child.CollectionDirty
	parent.Recompute()
}

func mergeVariableMap(target map[string]interface{}, values []types.Variable) {
	for _, variable := range values {
		if !variable.Enabled || variable.Name == "" {
			continue
		}
		target[variable.Name] = variable.Value
	}
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

func promptVariableMap(promptValues map[string]string) map[string]interface{} {
	out := map[string]interface{}{}
	for key, value := range promptValues {
		name := strings.TrimSpace(key)
		if name == "" {
			continue
		}
		if !strings.HasPrefix(name, "?") {
			name = "?" + name
		}
		out[name] = value
	}
	return out
}

func scanBodyPromptVariables(body types.RequestBody, scanText func(string), scanKeyValues func([]types.KeyValue)) {
	switch body.Mode {
	case "json":
		scanText(body.JSON)
	case "xml":
		scanText(body.XML)
	case "graphql":
		scanText(body.GraphQLQuery)
		scanText(body.GraphQLVariables)
	case "text", "sparql":
		scanText(body.Text)
	case "formUrlEncoded":
		scanKeyValues(body.FormURLEncoded)
	case "multipartForm":
		for _, part := range body.Multipart {
			if !part.Enabled {
				continue
			}
			scanText(part.Name)
			scanText(part.Value)
			scanText(part.FilePath)
			scanText(part.ContentType)
		}
	case "file":
		scanText(body.FilePath)
		scanText(body.FileContentType)
		for _, file := range types.FileBodyEntriesOf(body) {
			scanText(file.FilePath)
			scanText(file.ContentType)
		}
	}
}

func scanAuthPromptVariables(auth types.AuthConfig, scanText func(string), scanKeyValues func([]types.KeyValue)) {
	scanText(auth.Mode)
	scanText(auth.Username)
	scanText(auth.Password)
	scanText(auth.Domain)
	scanText(auth.Token)
	scanText(auth.APIKey)
	scanText(auth.APIValue)
	scanText(auth.APILocation)

	oauth1 := auth.OAuth1
	scanText(oauth1.ConsumerKey)
	scanText(oauth1.ConsumerSecret)
	scanText(oauth1.AccessToken)
	scanText(oauth1.AccessTokenSecret)
	scanText(oauth1.CallbackURL)
	scanText(oauth1.Verifier)
	scanText(oauth1.SignatureMethod)
	scanText(oauth1.PrivateKey)
	scanText(oauth1.PrivateKeyType)
	scanText(oauth1.Timestamp)
	scanText(oauth1.Nonce)
	scanText(oauth1.Version)
	scanText(oauth1.Realm)
	scanText(oauth1.Placement)

	oauth2 := auth.OAuth2
	scanText(oauth2.GrantType)
	scanText(oauth2.CallbackURL)
	scanText(oauth2.AuthorizationURL)
	scanText(oauth2.AccessTokenURL)
	scanText(oauth2.RefreshTokenURL)
	scanText(oauth2.Username)
	scanText(oauth2.Password)
	scanText(oauth2.ClientID)
	scanText(oauth2.ClientSecret)
	scanText(oauth2.Scope)
	scanText(oauth2.State)
	scanText(oauth2.CredentialsPlacement)
	scanText(oauth2.CredentialsID)
	scanText(oauth2.TokenSource)
	scanText(oauth2.TokenPlacement)
	scanText(oauth2.TokenHeaderPrefix)
	scanText(oauth2.TokenQueryKey)
	scanOAuth2PromptParams(oauth2.AuthorizationAdditionalParams, scanText)
	scanOAuth2PromptParams(oauth2.TokenAdditionalParams, scanText)
	scanOAuth2PromptParams(oauth2.RefreshAdditionalParams, scanText)
	scanKeyValues(oauth2.AdditionalParams)

	awsv4 := auth.AWSV4
	scanText(awsv4.AccessKeyID)
	scanText(awsv4.SecretAccessKey)
	scanText(awsv4.SessionToken)
	scanText(awsv4.Service)
	scanText(awsv4.Region)
	scanText(awsv4.ProfileName)
	scanText(awsv4.AccessKey)
	scanText(awsv4.SecretKey)
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

func RunnerPromptVariableSkipMessage(prompts []string) string {
	if len(prompts) == 0 {
		return "Request has been skipped due to containing prompt variables"
	}
	return fmt.Sprintf("Prompt variables detected in request. Runner execution is not supported for requests with prompt variables. Prompts: %s", strings.Join(prompts, ", "))
}

func ScriptVariableString(value interface{}) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case []byte:
		return string(typed)
	default:
		if encoded, err := json.Marshal(typed); err == nil && (strings.HasPrefix(string(encoded), "{") || strings.HasPrefix(string(encoded), "[")) {
			return string(encoded)
		}
		return fmt.Sprint(typed)
	}
}

func mergeScriptVariablesIntoSlice(existing []types.Variable, values map[string]interface{}) []types.Variable {
	next := []types.Variable{}
	seen := map[string]bool{}
	for _, variable := range existing {
		if variable.Name == "" {
			continue
		}
		value, ok := values[variable.Name]
		if !ok {
			if !variable.Enabled {
				next = append(next, variable)
			}
			continue
		}
		variable.Value = value
		variable.Enabled = true
		variable.DataType = scriptVariableDataType(value)
		if variable.Type == "" {
			variable.Type = variable.DataType
		}
		next = append(next, variable)
		seen[variable.Name] = true
	}
	names := make([]string, 0, len(values))
	for name := range values {
		if !seen[name] {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		dataType := scriptVariableDataType(values[name])
		next = append(next, types.Variable{
			ID:       scalar.NewID("var"),
			Name:     name,
			Value:    values[name],
			Type:     dataType,
			DataType: dataType,
			Enabled:  true,
		})
	}
	return next
}

func scriptVariableDataType(value interface{}) string {
	switch value.(type) {
	case bool:
		return "boolean"
	case int, int64, float32, float64, json.Number:
		return "number"
	case map[string]interface{}, []interface{}:
		return "json"
	default:
		return "string"
	}
}

func ApplyScriptVariableContextToState(state *types.AppState, workspace *types.Workspace, collection *types.Collection, environmentID string, ctx *VariableContext) {
	if state == nil || collection == nil || ctx == nil {
		return
	}
	now := time.Now()
	if ctx.CollectionDirty {
		collection.Variables = mergeScriptVariablesIntoSlice(collection.Variables, ctx.Collection)
		collection.UpdatedAt = now
	}
	if ctx.RuntimeDirty {
		collection.RuntimeVariables = mergeScriptVariablesIntoSlice(collection.RuntimeVariables, ctx.Runtime)
		collection.UpdatedAt = now
	}
	if ctx.EnvDirty && environmentID != "" {
		for index := range collection.Environments {
			if collection.Environments[index].ID == environmentID {
				collection.Environments[index].Variables = mergeScriptVariablesIntoSlice(collection.Environments[index].Variables, ctx.Env)
				collection.UpdatedAt = now
				break
			}
		}
	}
	if ctx.GlobalDirty {
		if workspace == nil {
			workspace, _ = FindWorkspaceForCollection(state, collection.ID)
		}
		if workspace == nil {
			return
		}
		if len(workspace.GlobalEnvironments) == 0 {
			workspace.GlobalEnvironments = append(workspace.GlobalEnvironments, types.Environment{
				ID:    scalar.NewID("global-env"),
				Name:  "Global",
				Color: "#2f8cff",
			})
			workspace.ActiveGlobalEnvironmentID = workspace.GlobalEnvironments[0].ID
		}
		if !WorkspaceHasGlobalEnvironment(*workspace, workspace.ActiveGlobalEnvironmentID) {
			workspace.ActiveGlobalEnvironmentID = workspace.GlobalEnvironments[0].ID
		}
		for index := range workspace.GlobalEnvironments {
			if workspace.GlobalEnvironments[index].ID != workspace.ActiveGlobalEnvironmentID {
				continue
			}
			workspace.GlobalEnvironments[index].Variables = mergeScriptVariablesIntoSlice(workspace.GlobalEnvironments[index].Variables, ctx.Global)
			workspace.UpdatedAt = now
			return
		}
	}
}

func FindWorkspaceForCollection(state *types.AppState, collectionID string) (*types.Workspace, *types.Collection) {
	if state == nil {
		return nil, nil
	}
	for wi := range state.Workspaces {
		for ci := range state.Workspaces[wi].Collections {
			if state.Workspaces[wi].Collections[ci].ID == collectionID {
				return &state.Workspaces[wi], &state.Workspaces[wi].Collections[ci]
			}
		}
	}
	return nil, nil
}

func SelectedEnvironmentName(collection *types.Collection, environmentID string) string {
	if collection == nil || environmentID == "" {
		return ""
	}
	for _, env := range collection.Environments {
		if env.ID == environmentID {
			return env.Name
		}
	}
	return ""
}

// interpolateMaxPasses bounds how many times the input is rescanned. A variable
// value may itself reference another variable, so substitution is a fixed-point
// iteration; the bound is what makes a cyclic reference terminate instead of
// looping forever. Each pass resolves one level of nesting.

// responseCurrentBody reads the body from the live `res` object rather than the
// captured types.Response, so a res.setBody() in an earlier script is visible to
// pm.response.text(). Reading the Snapshot would report the original body and
// make the two surfaces disagree about what the response is.
func responseCurrentBody(resObject *goja.Object, response types.Response) string {
	if resObject != nil {
		if value := resObject.Get("body"); value != nil && !goja.IsUndefined(value) && !goja.IsNull(value) {
			return value.String()
		}
	}
	return response.Body
}

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

var promptVariableInterpolationPattern = regexp.MustCompile(`\{\{\?([^{}\s](?:[^{}]*[^{}\s])?)\}\}`)
var dotEnvFilenamePattern = regexp.MustCompile(`^\.env(?:\.[A-Za-z0-9._-]+)?$`)

func CompareAssertion(actual, operator, expected string) bool {
	switch operator {
	case "equals", "==":
		return actual == expected
	case "notEquals", "!=":
		return actual != expected
	case "contains":
		return strings.Contains(actual, expected)
	case "startsWith":
		return strings.HasPrefix(actual, expected)
	case "endsWith":
		return strings.HasSuffix(actual, expected)
	default:
		return false
	}
}

const VisualizerTemplateLimit = 1 << 20 // 1 MiB
const VisualizerDataLimit = 4 << 20     // 4 MiB

func statusMessage(ok bool) string {
	if ok {
		return "passed"
	}
	return "failed"
}
