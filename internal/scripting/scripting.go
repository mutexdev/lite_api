// Package scripting is the goja runtime the app exposes to pre- and
// post-request scripts: the pm.* and bru.* API surfaces, the Node shims, the
// test and assertion vocabulary, and the sandbox around them.
//
// US-068, the largest story in the plan. Every function here was already free
// of *App -- the methods it does define are on its own types, which move with
// their receivers -- and that is what made a block this size movable.
package scripting

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/md5"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"hash"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	pathpkg "path"
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

	xhtml "golang.org/x/net/html"

	"github.com/andybalholm/brotli"
	"github.com/dop251/goja"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"golang.org/x/crypto/pbkdf2"
	"golang.org/x/crypto/scrypt"
	"gopkg.in/yaml.v3"
)

type runtimeScripts struct {
	Pre   string
	Post  string
	Tests string
}

type RequestState struct {
	headers                    []types.KeyValue
	bodySet                    bool
	bodyValue                  interface{}
	timeoutSet                 bool
	timeoutMs                  int
	maxRedirectsSet            bool
	maxRedirects               int
	disableParsingResponseJSON bool
	runtime                    *goja.Runtime
	// US-058. Set by pm.visualizer.set, applied to the response afterwards.
	visualizer      *types.VisualizerPayload
	SkipRequest     bool
	stopExecution   bool
	nextRequestSet  bool
	nextRequestName *string
	onFail          goja.Value
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

type scriptTimer struct {
	id       int64
	callback goja.Callable
	args     []goja.Value
	due      time.Time
	delay    time.Duration
	repeat   bool
	resolve  func(reason interface{}) error
	reject   func(reason interface{}) error
	promise  *goja.Promise
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

func EffectiveResponseVariables(collection types.Collection, item types.RequestItem) []types.Variable {
	order := []string{}
	merged := map[string]types.Variable{}
	add := func(values []types.Variable) {
		for _, variable := range values {
			name := types.ResponseVariableRuntimeName(variable.Name)
			if name == "" || !variable.Enabled {
				continue
			}
			if _, exists := merged[name]; !exists {
				order = append(order, name)
			}
			variable.Name = name
			variable.Enabled = true
			if variable.Type == "" {
				variable.Type = "response"
			}
			if variable.DataType == "" {
				variable.DataType = "string"
			}
			merged[name] = variable
		}
	}
	add(collection.ResVariables)
	for _, folder := range FolderChain(collection, item) {
		add(folder.ResVariables)
	}
	add(item.Vars.Res)
	out := make([]types.Variable, 0, len(order))
	for _, name := range order {
		out = append(out, merged[name])
	}
	return out
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

func RunPreRequestScript(script string, item *types.RequestItem, vars map[string]string, cookies []types.CookieEntry, logs ...*[]types.ScriptLog) error {
	return runPreRequestScriptWithJar(script, item, vars, NewScriptCookieJar(cookies), logs...)
}

func runPreRequestScriptWithJar(script string, item *types.RequestItem, vars map[string]string, jar *CookieJar, logs ...*[]types.ScriptLog) error {
	_, err := runPreRequestScriptWithJarState(script, item, vars, jar, logs...)
	return err
}

func runPreRequestScriptWithJarState(script string, item *types.RequestItem, vars map[string]string, jar *CookieJar, logs ...*[]types.ScriptLog) (*RequestState, error) {
	return RunPreRequestScriptWithJarStateMeta(script, item, vars, jar, ScriptRuntimeMeta{}, logs...)
}

func RunPreRequestScriptWithJarStateMeta(script string, item *types.RequestItem, vars map[string]string, jar *CookieJar, meta ScriptRuntimeMeta, logs ...*[]types.ScriptLog) (*RequestState, error) {
	if strings.TrimSpace(script) == "" {
		return &RequestState{headers: types.CloneKeyValues(item.Headers)}, nil
	}
	runtime, reqObject, reqState, _ := NewScriptRuntimeWithMeta(*item, types.Response{}, vars, nil, selectedScriptLogs(logs), jar, meta)
	if err := runGojaScript(runtime, script, meta.JSSandboxMode); err != nil {
		return reqState, err
	}
	applyScriptedRequest(item, reqObject, reqState)
	return reqState, nil
}

func RunPostResponseScript(script string, item types.RequestItem, response types.Response, vars map[string]string, cookies []types.CookieEntry, logs ...*[]types.ScriptLog) error {
	return runPostResponseScriptWithJar(script, item, response, vars, NewScriptCookieJar(cookies), logs...)
}

func runPostResponseScriptWithJar(script string, item types.RequestItem, response types.Response, vars map[string]string, jar *CookieJar, logs ...*[]types.ScriptLog) error {
	_, err := runPostResponseScriptWithJarState(script, item, &response, vars, jar, logs...)
	return err
}

func runPostResponseScriptWithJarState(script string, item types.RequestItem, response *types.Response, vars map[string]string, jar *CookieJar, logs ...*[]types.ScriptLog) (*RequestState, error) {
	return RunPostResponseScriptWithJarStateMeta(script, item, response, vars, jar, ScriptRuntimeMeta{}, logs...)
}

func RunPostResponseScriptWithJarStateMeta(script string, item types.RequestItem, response *types.Response, vars map[string]string, jar *CookieJar, meta ScriptRuntimeMeta, logs ...*[]types.ScriptLog) (*RequestState, error) {
	if strings.TrimSpace(script) == "" {
		return &RequestState{headers: types.CloneKeyValues(item.Headers)}, nil
	}
	runtime, _, reqState, resObject := NewScriptRuntimeWithMeta(item, *response, vars, nil, selectedScriptLogs(logs), jar, meta)
	err := runGojaScript(runtime, script, meta.JSSandboxMode)
	applyScriptedResponse(response, resObject)
	return reqState, err
}

func RunPostResponseVariables(variables []types.Variable, item types.RequestItem, response *types.Response, scriptVars *VariableContext, jar *CookieJar, meta ScriptRuntimeMeta, logs ...*[]types.ScriptLog) error {
	if len(variables) == 0 || response == nil {
		return nil
	}
	if scriptVars == nil {
		scriptVars = NewFlatScriptVariableContext(nil)
	}
	if meta.Variables == nil {
		meta.Variables = scriptVars
	}
	runtime, reqObject, _, _ := NewScriptRuntimeWithMeta(item, *response, scriptVars.Combined, nil, selectedScriptLogs(logs), jar, meta)
	for name, value := range scriptVars.CombinedInterface() {
		if strings.TrimSpace(name) != "" {
			_ = runtime.Set(name, value)
		}
	}
	_ = runtime.Set("$bru", runtime.Get("bru"))
	_ = runtime.Set("$req", reqObject)
	resValue := scriptPostResponseVariableResponseParser(runtime, *response, item.Settings.DisableParsingResponseJSON)
	_ = runtime.Set("res", resValue)
	_ = runtime.Set("$res", resValue)

	failures := []string{}
	for _, variable := range variables {
		if !variable.Enabled {
			continue
		}
		name := types.ResponseVariableRuntimeName(variable.Name)
		expression := strings.TrimSpace(fmt.Sprint(variable.Value))
		value, err := runPostResponseVariableExpression(runtime, expression)
		if err != nil {
			label := name
			if label == "" {
				label = "<unnamed>"
			}
			failures = append(failures, fmt.Sprintf("%s: %s", label, err.Error()))
			continue
		}
		if name == "" {
			continue
		}
		exported := scriptBodyValue(value)
		scriptVars.Runtime[name] = exported
		scriptVars.RuntimeDirty = true
		scriptVars.Recompute()
		_ = runtime.Set(name, exported)
	}
	if len(failures) > 0 {
		suffix := "s"
		if len(failures) == 1 {
			suffix = ""
		}
		return fmt.Errorf("%d error%s in post response variables:\n%s", len(failures), suffix, strings.Join(failures, "\n"))
	}
	return nil
}

func runPostResponseVariableExpression(runtime *goja.Runtime, expression string) (goja.Value, error) {
	if strings.TrimSpace(expression) == "" {
		return goja.Undefined(), nil
	}
	wrapped := "(" + expression + "\n)"
	value, err := runtime.RunString(wrapped)
	if err == nil {
		return value, nil
	}
	value, fallbackErr := runtime.RunString(expression)
	if fallbackErr == nil {
		return value, nil
	}
	return nil, err
}

func scriptPostResponseVariableResponseParser(runtime *goja.Runtime, response types.Response, disableParsingJSON bool) goja.Value {
	headers := scriptResponseHeaders(response.Headers)
	body := scriptResponseBody(response, disableParsingJSON)
	bodyBytes := responseDataBytes(response)
	resValue := runtime.ToValue(func(call goja.FunctionCall) goja.Value {
		value, ok := scriptResponseQuery(runtime, body, call)
		if !ok {
			return goja.Undefined()
		}
		return runtime.ToValue(value)
	})
	resObject := resValue.ToObject(runtime)
	_ = resObject.Set("status", response.Status)
	_ = resObject.Set("statusCode", response.Status)
	_ = resObject.Set("statusText", scalar.CleanStatusText(response.Status, response.StatusText))
	_ = resObject.Set("headers", headers)
	_ = resObject.Set("body", body)
	_ = resObject.Set("data", body)
	_ = resObject.Set("dataBuffer", scriptByteArrayValue(runtime, bodyBytes))
	_ = resObject.Set("responseTime", response.DurationMs)
	_ = resObject.Set("duration", response.DurationMs)
	_ = resObject.Set("url", response.RequestedURL)
	_ = resObject.Set("getHeader", func(name string) string { return getHeaderValue(response.Headers, name) })
	_ = resObject.Set("getHeaders", func() map[string]string { return headers })
	_ = resObject.Set("getStatus", func() int { return response.Status })
	_ = resObject.Set("getStatusText", func() string { return scalar.CleanStatusText(response.Status, response.StatusText) })
	_ = resObject.Set("getBody", func() interface{} { return body })
	_ = resObject.Set("getResponseTime", func() int64 { return response.DurationMs })
	_ = resObject.Set("getUrl", func() string { return response.RequestedURL })
	_ = resObject.Set("getSize", func() map[string]int { return scriptResponseSize(response, headers) })
	_ = resObject.Set("getDataBuffer", func() goja.Value { return scriptByteArrayValue(runtime, bodyBytes) })
	_ = resObject.Set("jq", func(expr string) goja.Value {
		value, ok := scriptResponseJQ(body, expr)
		if !ok {
			return goja.Null()
		}
		return runtime.ToValue(value)
	})
	if jsonValue, ok := responseJSONValue(response.Body); ok && !disableParsingJSON {
		_ = resObject.Set("json", jsonValue)
	}
	return resValue
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

// installPostmanScriptAPI puts a live `pm` object beside `bru` (US-039).
//
// pm.test and pm.expect are BOUND TO the existing globals rather than
// reimplemented. That is the whole point: two independent test registries would
// drift, and a `pm.test` whose failures did not reach the same TestResults
// slice would report a green run while its assertions failed. Binding also
// means every later fix to `test` or `expect` applies to both surfaces for
// free.
//
// This is the core only. pm.environment / pm.collectionVariables / pm.globals
// (US-040), pm.request / pm.response (US-041), pm.sendRequest / pm.cookies
// (US-042) and pm.iterationData / pm.vault (US-043) land separately.
func installPostmanScriptAPI(runtime *goja.Runtime, bruObject, reqObject, resObject *goja.Object, scriptVars *VariableContext, reqState *RequestState, response types.Response, item types.RequestItem, meta ScriptRuntimeMeta) {
	pm := runtime.NewObject()
	_ = pm.Set("test", runtime.Get("test"))
	_ = pm.Set("expect", runtime.Get("expect"))
	installPostmanVariableScopes(runtime, pm, bruObject, scriptVars)
	installPostmanRequestAPI(runtime, pm, reqObject, item)
	installPostmanSideEffects(runtime, pm, bruObject)
	installPostmanIterationData(runtime, pm, scriptVars)
	installPostmanVisualizer(runtime, pm, reqState)
	installPostmanVault(runtime, pm, bruObject)
	// pm.response is deliberately ABSENT during the pre-request phase, because
	// there is no response yet. The alternative — exposing the zero types.Response —
	// would answer pm.response.code with 0 and pm.response.text() with "",
	// which reads as a server that returned nothing rather than as a script
	// asking for something that does not exist. Left undefined, the script
	// throws where Postman also throws.
	if meta.TimelinePhase != "pre-request" {
		installPostmanResponseAPI(runtime, pm, resObject, response)
	}

	info := runtime.NewObject()
	_ = info.Set("requestName", item.Name)
	_ = info.Set("requestId", item.ID)
	_ = info.Set("eventName", PostmanEventName(meta.TimelinePhase))
	// Postman's pm.info.iteration is 0-BASED while everything user-facing here
	// counts from 1. Converting at this single boundary is what keeps a script
	// copied out of Postman working; leaking the 1-based value would make every
	// `if (pm.info.iteration === 0)` guard silently never fire.
	_ = info.Set("iteration", max(meta.IterationIndex-1, 0))
	// Outside a collection run Postman reports a single iteration, not zero.
	_ = info.Set("iterationCount", max(meta.IterationCount, 1))
	_ = pm.Set("info", info)

	_ = runtime.Set("pm", pm)
}

// installPostmanVariableScopes gives each Postman scope its OWN storage
// (US-040).
//
// This is the story's whole point, and it is the bug in the import translator
// that US-044 will demote: that table maps pm.environment.set,
// pm.collectionVariables.set, pm.globals.set AND pm.variables.set all onto
// bru.setVar, so four distinct scopes collapse into the runtime scope. Nothing
// errors. A script that writes pm.globals.set("token", ...) and later reads
// pm.environment.get("token") gets its value back, so the collapse looks like
// it works — right up to the point where a second environment, or a second
// collection, is supposed to see a different value and does not.
//
// Every method delegates to the bru function for that exact scope. Delegating
// rather than reimplementing is what makes drift impossible: the dirty-tracking
// that decides whether an environment gets written back to disk lives in those
// closures, and a parallel implementation that forgot to set EnvDirty would
// update variables that silently never persist.
func installPostmanVariableScopes(runtime *goja.Runtime, pm, bruObject *goja.Object, scriptVars *VariableContext) {
	// replaceIn is scope-independent in Postman: it interpolates against the
	// whole resolved chain regardless of which scope object it is called on.
	replaceIn := bruObject.Get("interpolate")

	scope := func(get, set, unset, has, clear, toObject string) *goja.Object {
		object := runtime.NewObject()
		_ = object.Set("get", bruObject.Get(get))
		_ = object.Set("set", bruObject.Get(set))
		_ = object.Set("unset", bruObject.Get(unset))
		_ = object.Set("has", bruObject.Get(has))
		_ = object.Set("clear", bruObject.Get(clear))
		_ = object.Set("toObject", bruObject.Get(toObject))
		_ = object.Set("replaceIn", replaceIn)
		return object
	}

	_ = pm.Set("environment", scope(
		"getEnvVar", "setEnvVar", "deleteEnvVar",
		"hasEnvVar", "deleteAllEnvVars", "getAllEnvVars"))
	_ = pm.Set("collectionVariables", scope(
		"getCollectionVar", "setCollectionVar", "deleteCollectionVar",
		"hasCollectionVar", "deleteAllCollectionVars", "getAllCollectionVars"))
	_ = pm.Set("globals", scope(
		"getGlobalEnvVar", "setGlobalEnvVar", "deleteGlobalEnvVar",
		"hasGlobalEnvVar", "deleteAllGlobalEnvVars", "getAllGlobalEnvVars"))

	// pm.variables is the odd one out and deliberately not built by scope():
	// it READS the fully resolved chain across every scope, but WRITES to the
	// runtime scope. Giving it its own storage would make a value set through
	// it invisible to {{var}} interpolation, and routing its reads to one scope
	// would silently miss variables defined anywhere else.
	variables := runtime.NewObject()
	_ = variables.Set("get", func(name string) goja.Value {
		if scriptVars == nil {
			return goja.Undefined()
		}
		if value, ok := scriptVars.CombinedInterface()[name]; ok {
			return runtime.ToValue(value)
		}
		return goja.Undefined()
	})
	_ = variables.Set("has", func(name string) bool {
		if scriptVars == nil {
			return false
		}
		_, ok := scriptVars.CombinedInterface()[name]
		return ok
	})
	_ = variables.Set("toObject", func() map[string]interface{} {
		if scriptVars == nil {
			return map[string]interface{}{}
		}
		return scriptVars.CombinedInterface()
	})
	_ = variables.Set("set", bruObject.Get("setVar"))
	_ = variables.Set("unset", bruObject.Get("deleteVar"))
	_ = variables.Set("replaceIn", replaceIn)
	_ = pm.Set("variables", variables)
}

// PostmanEventName maps this codebase's timeline phases onto the two event
// names Postman scripts test against. Both post-response phases are "test":
// Postman has no separate post-response event, and reporting one would break
// the `pm.info.eventName === "test"` guard that scripts actually write.
func PostmanEventName(timelinePhase string) string {
	if timelinePhase == "pre-request" {
		return "prerequest"
	}
	return "test"
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

func scriptMinifyXML(runtime *goja.Runtime, value goja.Value) goja.Value {
	if goja.IsUndefined(value) || goja.IsNull(value) {
		panic(runtime.NewGoError(errors.New("failed to minify")))
	}
	text, ok := value.Export().(string)
	if !ok {
		panic(runtime.NewTypeError("minifyXml expects a string"))
	}
	minified, err := minifyXMLString(text)
	if err != nil {
		panic(runtime.NewGoError(fmt.Errorf("failed to minify: %s", err.Error())))
	}
	return runtime.ToValue(minified)
}

func minifyXMLString(input string) (string, error) {
	if strings.TrimSpace(input) == "" {
		return strings.TrimSpace(input), nil
	}
	decoder := xml.NewDecoder(strings.NewReader(input))
	var buffer bytes.Buffer
	encoder := xml.NewEncoder(&buffer)
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", err
		}
		if data, ok := token.(xml.CharData); ok && strings.TrimSpace(string(data)) == "" {
			continue
		}
		if err := encoder.EncodeToken(token); err != nil {
			return "", err
		}
	}
	if err := encoder.Flush(); err != nil {
		return "", err
	}
	return buffer.String(), nil
}

type scriptXMLFormatterOptions struct {
	indentation   string
	lineSeparator string
}

func newScriptXMLFormatterObject(runtime *goja.Runtime) goja.Value {
	formatter := runtime.ToValue(func(call goja.FunctionCall) goja.Value {
		value := call.Argument(0)
		if goja.IsUndefined(value) || goja.IsNull(value) {
			panic(runtime.NewTypeError("xml-formatter expects a string"))
		}
		text, ok := value.Export().(string)
		if !ok {
			panic(runtime.NewTypeError("xml-formatter expects a string"))
		}
		options := scriptXMLFormatterOptions{indentation: "  ", lineSeparator: "\n"}
		if optionValue := call.Argument(1); optionValue != nil && !goja.IsUndefined(optionValue) && !goja.IsNull(optionValue) {
			optionObject := optionValue.ToObject(runtime)
			if indentation := optionObject.Get("indentation"); indentation != nil && !goja.IsUndefined(indentation) && !goja.IsNull(indentation) {
				options.indentation = indentation.String()
			}
			if lineSeparator := optionObject.Get("lineSeparator"); lineSeparator != nil && !goja.IsUndefined(lineSeparator) && !goja.IsNull(lineSeparator) {
				options.lineSeparator = lineSeparator.String()
			}
		}
		formatted, err := formatXMLString(text, options)
		if err != nil {
			panic(runtime.NewGoError(err))
		}
		return runtime.ToValue(formatted)
	})
	_ = formatter.ToObject(runtime).Set("default", formatter)
	return formatter
}

type scriptXML2JSOptions struct {
	ExplicitArray   bool
	ExplicitRoot    bool
	ExplicitCharKey bool
	Trim            bool
	AttrKey         string
	CharKey         string
}

type scriptXML2JSNode struct {
	Name     string
	Attrs    map[string]string
	Children []*scriptXML2JSNode
	Text     []string
}

func newScriptXML2JSObject(runtime *goja.Runtime) goja.Value {
	module := runtime.NewObject()
	_ = module.Set("parseString", func(call goja.FunctionCall) goja.Value {
		return scriptXML2JSParseString(runtime, scriptXML2JSDefaultOptions(), call)
	})
	_ = module.Set("parseStringPromise", func(call goja.FunctionCall) goja.Value {
		options := scriptXML2JSDefaultOptions()
		if optionValue := call.Argument(1); optionValue != nil && !goja.IsUndefined(optionValue) && !goja.IsNull(optionValue) {
			options = scriptXML2JSOptionsFromValue(runtime, optionValue, options)
		}
		result, err := scriptXML2JSParse(runtime, call.Argument(0), options)
		if err != nil {
			return scriptRejectedPromise(runtime, runtime.NewGoError(err))
		}
		return scriptResolvedPromise(runtime, result)
	})
	_ = module.Set("Parser", func(call goja.ConstructorCall) *goja.Object {
		return newScriptXML2JSParserObject(runtime, call.Argument(0))
	})
	_ = module.Set("defaults", map[string]interface{}{
		"0.2": map[string]interface{}{
			"explicitArray": true,
			"explicitRoot":  true,
			"attrkey":       "$",
			"charkey":       "_",
			"trim":          false,
		},
	})
	_ = module.Set("default", module)
	return module
}

func newScriptCheerioObject(runtime *goja.Runtime) goja.Value {
	module := runtime.NewObject()
	load := func(call goja.FunctionCall) goja.Value {
		if goja.IsUndefined(call.Argument(0)) || goja.IsNull(call.Argument(0)) {
			panic(runtime.NewTypeError("cheerio.load expects an HTML string"))
		}
		doc, err := xhtml.Parse(strings.NewReader(call.Argument(0).String()))
		if err != nil {
			panic(runtime.NewGoError(err))
		}
		return newScriptCheerioLoadedFunction(runtime, doc)
	}
	_ = module.Set("load", load)
	_ = module.Set("default", module)
	return module
}

func newScriptCheerioLoadedFunction(runtime *goja.Runtime, doc *xhtml.Node) goja.Value {
	loaded := runtime.ToValue(func(call goja.FunctionCall) goja.Value {
		selector := ""
		if !goja.IsUndefined(call.Argument(0)) && !goja.IsNull(call.Argument(0)) {
			selector = call.Argument(0).String()
		}
		return newScriptCheerioSelectionObject(runtime, scriptCheerioSelect(doc, selector))
	})
	object := loaded.ToObject(runtime)
	_ = object.Set("html", func() string {
		return scriptCheerioRenderNode(doc)
	})
	_ = object.Set("root", func() goja.Value {
		return newScriptCheerioSelectionObject(runtime, []*xhtml.Node{doc})
	})
	return loaded
}

func newScriptCheerioSelectionObject(runtime *goja.Runtime, nodes []*xhtml.Node) goja.Value {
	selection := runtime.NewObject()
	_ = selection.Set("length", len(nodes))
	_ = selection.Set("text", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 || goja.IsUndefined(call.Argument(0)) {
			var b strings.Builder
			for _, node := range nodes {
				scriptCheerioText(node, &b)
			}
			return runtime.ToValue(b.String())
		}
		text := call.Argument(0).String()
		for _, node := range nodes {
			scriptCheerioSetText(node, text)
		}
		return selection
	})
	_ = selection.Set("addClass", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 || goja.IsUndefined(call.Argument(0)) || goja.IsNull(call.Argument(0)) {
			return selection
		}
		classes := strings.Fields(call.Argument(0).String())
		for _, node := range nodes {
			scriptCheerioAddClass(node, classes)
		}
		return selection
	})
	_ = selection.Set("attr", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 || goja.IsUndefined(call.Argument(0)) || goja.IsNull(call.Argument(0)) || len(nodes) == 0 {
			return goja.Undefined()
		}
		name := call.Argument(0).String()
		if len(call.Arguments) == 1 || goja.IsUndefined(call.Argument(1)) {
			if value, ok := scriptCheerioAttr(nodes[0], name); ok {
				return runtime.ToValue(value)
			}
			return goja.Undefined()
		}
		value := call.Argument(1).String()
		for _, node := range nodes {
			scriptCheerioSetAttr(node, name, value)
		}
		return selection
	})
	_ = selection.Set("html", func() goja.Value {
		if len(nodes) == 0 {
			return goja.Null()
		}
		var b strings.Builder
		for child := nodes[0].FirstChild; child != nil; child = child.NextSibling {
			b.WriteString(scriptCheerioRenderNode(child))
		}
		return runtime.ToValue(b.String())
	})
	for index, node := range nodes {
		_ = selection.Set(strconv.Itoa(index), scriptCheerioNodeObject(runtime, node))
	}
	return selection
}

func scriptCheerioNodeObject(runtime *goja.Runtime, node *xhtml.Node) goja.Value {
	object := runtime.NewObject()
	_ = object.Set("type", strings.ToLower(node.Type.String()))
	_ = object.Set("name", node.Data)
	return object
}

type scriptCheerioSelector struct {
	tag     string
	id      string
	classes []string
}

func scriptCheerioSelect(root *xhtml.Node, selector string) []*xhtml.Node {
	selector = strings.TrimSpace(selector)
	if selector == "" {
		return nil
	}
	parsed := parseScriptCheerioSelector(selector)
	nodes := []*xhtml.Node{}
	var walk func(*xhtml.Node)
	walk = func(node *xhtml.Node) {
		if node == nil {
			return
		}
		if scriptCheerioMatches(node, parsed) {
			nodes = append(nodes, node)
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(root)
	return nodes
}

func parseScriptCheerioSelector(selector string) scriptCheerioSelector {
	parsed := scriptCheerioSelector{}
	rest := selector
	if hash := strings.Index(rest, "#"); hash >= 0 {
		parsed.tag = strings.TrimSpace(rest[:hash])
		rest = rest[hash+1:]
		if dot := strings.Index(rest, "."); dot >= 0 {
			parsed.id = rest[:dot]
			rest = rest[dot:]
		} else {
			parsed.id = rest
			rest = ""
		}
	}
	if parsed.tag == "" && strings.HasPrefix(rest, ".") {
		parsed.classes = strings.FieldsFunc(strings.TrimPrefix(rest, "."), func(r rune) bool { return r == '.' })
		return parsed
	}
	parts := strings.Split(rest, ".")
	if parsed.tag == "" {
		parsed.tag = strings.TrimSpace(parts[0])
	}
	for _, className := range parts[1:] {
		if strings.TrimSpace(className) != "" {
			parsed.classes = append(parsed.classes, strings.TrimSpace(className))
		}
	}
	return parsed
}

func scriptCheerioMatches(node *xhtml.Node, selector scriptCheerioSelector) bool {
	if node.Type != xhtml.ElementNode {
		return false
	}
	if selector.tag != "" && !strings.EqualFold(node.Data, selector.tag) {
		return false
	}
	if selector.id != "" {
		value, ok := scriptCheerioAttr(node, "id")
		if !ok || value != selector.id {
			return false
		}
	}
	if len(selector.classes) > 0 {
		value, ok := scriptCheerioAttr(node, "class")
		if !ok {
			return false
		}
		classes := strings.Fields(value)
		for _, required := range selector.classes {
			if !scriptStringSliceContains(classes, required) {
				return false
			}
		}
	}
	return true
}

func scriptCheerioText(node *xhtml.Node, b *strings.Builder) {
	if node == nil {
		return
	}
	if node.Type == xhtml.TextNode {
		b.WriteString(node.Data)
		return
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		scriptCheerioText(child, b)
	}
}

func scriptCheerioSetText(node *xhtml.Node, text string) {
	for child := node.FirstChild; child != nil; {
		next := child.NextSibling
		node.RemoveChild(child)
		child = next
	}
	node.AppendChild(&xhtml.Node{Type: xhtml.TextNode, Data: text})
}

func scriptCheerioAddClass(node *xhtml.Node, classes []string) {
	if node == nil || node.Type != xhtml.ElementNode || len(classes) == 0 {
		return
	}
	current, ok := scriptCheerioAttr(node, "class")
	existing := strings.Fields(current)
	for _, className := range classes {
		if className != "" && !scriptStringSliceContains(existing, className) {
			existing = append(existing, className)
		}
	}
	if ok {
		scriptCheerioSetAttr(node, "class", strings.Join(existing, " "))
		return
	}
	node.Attr = append(node.Attr, xhtml.Attribute{Key: "class", Val: strings.Join(existing, " ")})
}

func scriptCheerioAttr(node *xhtml.Node, name string) (string, bool) {
	if node == nil {
		return "", false
	}
	for _, attr := range node.Attr {
		if strings.EqualFold(attr.Key, name) {
			return attr.Val, true
		}
	}
	return "", false
}

func scriptCheerioSetAttr(node *xhtml.Node, name, value string) {
	if node == nil || node.Type != xhtml.ElementNode {
		return
	}
	for index := range node.Attr {
		if strings.EqualFold(node.Attr[index].Key, name) {
			node.Attr[index].Val = value
			return
		}
	}
	node.Attr = append(node.Attr, xhtml.Attribute{Key: name, Val: value})
}

func scriptCheerioRenderNode(node *xhtml.Node) string {
	if node == nil {
		return ""
	}
	var b strings.Builder
	if err := xhtml.Render(&b, node); err != nil {
		return ""
	}
	return b.String()
}

func scriptStringSliceContains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func newScriptXML2JSParserObject(runtime *goja.Runtime, optionsValue goja.Value) *goja.Object {
	options := scriptXML2JSOptionsFromValue(runtime, optionsValue, scriptXML2JSDefaultOptions())
	parser := runtime.NewObject()
	_ = parser.Set("parseString", func(call goja.FunctionCall) goja.Value {
		return scriptXML2JSParseString(runtime, options, call)
	})
	_ = parser.Set("parseStringPromise", func(call goja.FunctionCall) goja.Value {
		result, err := scriptXML2JSParse(runtime, call.Argument(0), options)
		if err != nil {
			return scriptRejectedPromise(runtime, runtime.NewGoError(err))
		}
		return scriptResolvedPromise(runtime, result)
	})
	return parser
}

func scriptXML2JSDefaultOptions() scriptXML2JSOptions {
	return scriptXML2JSOptions{
		ExplicitArray: true,
		ExplicitRoot:  true,
		AttrKey:       "$",
		CharKey:       "_",
	}
}

func scriptXML2JSOptionsFromValue(runtime *goja.Runtime, value goja.Value, options scriptXML2JSOptions) scriptXML2JSOptions {
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		return options
	}
	object := value.ToObject(runtime)
	if explicitArray := object.Get("explicitArray"); explicitArray != nil && !goja.IsUndefined(explicitArray) && !goja.IsNull(explicitArray) {
		options.ExplicitArray = explicitArray.ToBoolean()
	}
	if explicitRoot := object.Get("explicitRoot"); explicitRoot != nil && !goja.IsUndefined(explicitRoot) && !goja.IsNull(explicitRoot) {
		options.ExplicitRoot = explicitRoot.ToBoolean()
	}
	if explicitCharKey := object.Get("explicitCharkey"); explicitCharKey != nil && !goja.IsUndefined(explicitCharKey) && !goja.IsNull(explicitCharKey) {
		options.ExplicitCharKey = explicitCharKey.ToBoolean()
	}
	if trim := object.Get("trim"); trim != nil && !goja.IsUndefined(trim) && !goja.IsNull(trim) {
		options.Trim = trim.ToBoolean()
	}
	if attrKey := object.Get("attrkey"); attrKey != nil && !goja.IsUndefined(attrKey) && !goja.IsNull(attrKey) {
		options.AttrKey = attrKey.String()
	}
	if charKey := object.Get("charkey"); charKey != nil && !goja.IsUndefined(charKey) && !goja.IsNull(charKey) {
		options.CharKey = charKey.String()
	}
	if options.AttrKey == "" {
		options.AttrKey = "$"
	}
	if options.CharKey == "" {
		options.CharKey = "_"
	}
	return options
}

func scriptXML2JSParseString(runtime *goja.Runtime, baseOptions scriptXML2JSOptions, call goja.FunctionCall) goja.Value {
	options := baseOptions
	callbackValue := call.Argument(1)
	if _, ok := goja.AssertFunction(callbackValue); !ok {
		options = scriptXML2JSOptionsFromValue(runtime, callbackValue, options)
		callbackValue = call.Argument(2)
	}
	callback, hasCallback := goja.AssertFunction(callbackValue)
	result, err := scriptXML2JSParse(runtime, call.Argument(0), options)
	if hasCallback {
		if err != nil {
			if _, callbackErr := callback(goja.Undefined(), runtime.NewGoError(err), goja.Undefined()); callbackErr != nil {
				panic(callbackErr)
			}
			return goja.Undefined()
		}
		if _, callbackErr := callback(goja.Undefined(), goja.Null(), result); callbackErr != nil {
			panic(callbackErr)
		}
		return goja.Undefined()
	}
	if err != nil {
		panic(runtime.NewGoError(err))
	}
	return result
}

func scriptXML2JSParse(runtime *goja.Runtime, value goja.Value, options scriptXML2JSOptions) (goja.Value, error) {
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		return goja.Undefined(), errors.New("xml2js.parseString expects an XML string")
	}
	root, err := parseScriptXML2JS(value.String(), options)
	if err != nil {
		return goja.Undefined(), err
	}
	if root == nil {
		return runtime.ToValue(map[string]interface{}{}), nil
	}
	result := scriptXML2JSNodeValue(root, options)
	if options.ExplicitRoot {
		result = map[string]interface{}{root.Name: result}
	}
	return runtime.ToValue(result), nil
}

func parseScriptXML2JS(input string, options scriptXML2JSOptions) (*scriptXML2JSNode, error) {
	decoder := xml.NewDecoder(strings.NewReader(input))
	var root *scriptXML2JSNode
	stack := []*scriptXML2JSNode{}
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		switch typed := token.(type) {
		case xml.StartElement:
			node := &scriptXML2JSNode{Name: typed.Name.Local, Attrs: map[string]string{}}
			for _, attr := range typed.Attr {
				name := attr.Name.Local
				if attr.Name.Space != "" {
					name = attr.Name.Space + ":" + attr.Name.Local
				}
				node.Attrs[name] = attr.Value
			}
			if len(stack) == 0 {
				if root != nil {
					return nil, errors.New("XML contains multiple root elements")
				}
				root = node
			} else {
				parent := stack[len(stack)-1]
				parent.Children = append(parent.Children, node)
			}
			stack = append(stack, node)
		case xml.EndElement:
			if len(stack) == 0 {
				return nil, fmt.Errorf("unexpected closing tag %s", typed.Name.Local)
			}
			stack = stack[:len(stack)-1]
		case xml.CharData:
			if len(stack) == 0 {
				continue
			}
			text := string(typed)
			if options.Trim {
				text = strings.TrimSpace(text)
			}
			if strings.TrimSpace(text) != "" {
				stack[len(stack)-1].Text = append(stack[len(stack)-1].Text, text)
			}
		}
	}
	if len(stack) > 0 {
		return nil, errors.New("XML contains unclosed elements")
	}
	return root, nil
}

func scriptXML2JSNodeValue(node *scriptXML2JSNode, options scriptXML2JSOptions) interface{} {
	if node == nil {
		return nil
	}
	text := strings.Join(node.Text, "")
	hasAttrs := len(node.Attrs) > 0
	hasChildren := len(node.Children) > 0
	if !hasAttrs && !hasChildren && !options.ExplicitCharKey {
		return text
	}
	out := map[string]interface{}{}
	if hasAttrs {
		attrs := map[string]interface{}{}
		keys := make([]string, 0, len(node.Attrs))
		for key := range node.Attrs {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			attrs[key] = node.Attrs[key]
		}
		out[options.AttrKey] = attrs
	}
	if text != "" || options.ExplicitCharKey {
		out[options.CharKey] = text
	}
	childGroups := map[string][]interface{}{}
	childOrder := []string{}
	for _, child := range node.Children {
		if _, ok := childGroups[child.Name]; !ok {
			childOrder = append(childOrder, child.Name)
		}
		childGroups[child.Name] = append(childGroups[child.Name], scriptXML2JSNodeValue(child, options))
	}
	for _, name := range childOrder {
		values := childGroups[name]
		if options.ExplicitArray || len(values) > 1 {
			out[name] = values
		} else if len(values) == 1 {
			out[name] = values[0]
		}
	}
	return out
}

func newScriptYAMLObject(runtime *goja.Runtime) goja.Value {
	module := runtime.NewObject()
	_ = module.Set("parse", func(call goja.FunctionCall) goja.Value {
		text := scriptYAMLStringArgument(runtime, call.Argument(0), "YAML.parse")
		var raw interface{}
		if err := yaml.Unmarshal([]byte(text), &raw); err != nil {
			panic(runtime.NewGoError(err))
		}
		return runtime.ToValue(scriptYAMLNormalize(raw))
	})
	_ = module.Set("stringify", func(call goja.FunctionCall) goja.Value {
		value := scriptYAMLNormalize(call.Argument(0).Export())
		data, err := yaml.Marshal(value)
		if err != nil {
			panic(runtime.NewGoError(err))
		}
		return runtime.ToValue(string(data))
	})
	_ = module.Set("parseDocument", func(call goja.FunctionCall) goja.Value {
		text := scriptYAMLStringArgument(runtime, call.Argument(0), "YAML.parseDocument")
		var doc yaml.Node
		if err := yaml.Unmarshal([]byte(text), &doc); err != nil {
			panic(runtime.NewGoError(err))
		}
		scriptYAMLUpdateLineCounter(runtime, call.Argument(1), text)
		return newScriptYAMLDocumentObject(runtime, text, &doc)
	})
	_ = module.Set("LineCounter", func(call goja.ConstructorCall) *goja.Object {
		return newScriptYAMLLineCounterObject(runtime)
	})
	_ = module.Set("isSeq", func(value goja.Value) bool {
		return scriptYAMLNodeKind(runtime, value) == "seq"
	})
	_ = module.Set("isMap", func(value goja.Value) bool {
		return scriptYAMLNodeKind(runtime, value) == "map"
	})
	_ = module.Set("isScalar", func(value goja.Value) bool {
		return scriptYAMLNodeKind(runtime, value) == "scalar"
	})
	_ = module.Set("default", module)
	return module
}

func scriptYAMLStringArgument(runtime *goja.Runtime, value goja.Value, name string) string {
	if goja.IsUndefined(value) || goja.IsNull(value) {
		panic(runtime.NewTypeError(name + " expects a string"))
	}
	if text, ok := value.Export().(string); ok {
		return text
	}
	panic(runtime.NewTypeError(name + " expects a string"))
}

func scriptYAMLNormalize(value interface{}) interface{} {
	switch typed := value.(type) {
	case nil:
		return nil
	case map[string]interface{}:
		out := map[string]interface{}{}
		for key, child := range typed {
			out[key] = scriptYAMLNormalize(child)
		}
		return out
	case map[interface{}]interface{}:
		out := map[string]interface{}{}
		for key, child := range typed {
			out[fmt.Sprint(key)] = scriptYAMLNormalize(child)
		}
		return out
	case []interface{}:
		out := make([]interface{}, len(typed))
		for index, child := range typed {
			out[index] = scriptYAMLNormalize(child)
		}
		return out
	default:
		return typed
	}
}

func scriptYAMLUpdateLineCounter(runtime *goja.Runtime, options goja.Value, content string) {
	if options == nil || goja.IsUndefined(options) || goja.IsNull(options) {
		return
	}
	lineCounter := options.ToObject(runtime).Get("lineCounter")
	if lineCounter == nil || goja.IsUndefined(lineCounter) || goja.IsNull(lineCounter) {
		return
	}
	setter := lineCounter.ToObject(runtime).Get("__liteapiSetContent")
	if setter == nil || goja.IsUndefined(setter) || goja.IsNull(setter) {
		return
	}
	if fn, ok := goja.AssertFunction(setter); ok {
		if _, err := fn(lineCounter, runtime.ToValue(content)); err != nil {
			panic(err)
		}
	}
}

func newScriptYAMLLineCounterObject(runtime *goja.Runtime) *goja.Object {
	state := &scriptYAMLLineCounter{}
	object := runtime.NewObject()
	_ = object.Set("linePos", func(call goja.FunctionCall) goja.Value {
		line, col := state.linePos(int(call.Argument(0).ToInteger()))
		result := runtime.NewObject()
		_ = result.Set("line", line)
		_ = result.Set("col", col)
		return result
	})
	_ = object.DefineDataProperty("__liteapiSetContent", runtime.ToValue(func(content string) {
		state.setContent(content)
	}), goja.FLAG_TRUE, goja.FLAG_FALSE, goja.FLAG_FALSE)
	return object
}

type scriptYAMLLineCounter struct {
	content string
	offsets []int
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

func newScriptYAMLDocumentObject(runtime *goja.Runtime, content string, doc *yaml.Node) goja.Value {
	root := scriptYAMLRootNode(doc)
	object := runtime.NewObject()
	scriptYAMLSetNodeKind(runtime, object, "document")
	_ = object.Set("contents", newScriptYAMLNodeObject(runtime, content, root))
	_ = object.Set("errors", []interface{}{})
	_ = object.Set("get", func(call goja.FunctionCall) goja.Value {
		return scriptYAMLMapGet(runtime, content, root, call.Argument(0), call.Argument(1))
	})
	_ = object.Set("getIn", func(call goja.FunctionCall) goja.Value {
		return scriptYAMLGetIn(runtime, content, root, call.Argument(0), call.Argument(1))
	})
	_ = object.Set("toJSON", func(goja.FunctionCall) goja.Value {
		return runtime.ToValue(scriptYAMLNodeToInterface(root))
	})
	_ = object.Set("toString", func(goja.FunctionCall) goja.Value {
		data, err := yaml.Marshal(scriptYAMLNodeToInterface(root))
		if err != nil {
			panic(runtime.NewGoError(err))
		}
		return runtime.ToValue(string(data))
	})
	return object
}

func newScriptYAMLNodeObject(runtime *goja.Runtime, content string, node *yaml.Node) goja.Value {
	if node == nil {
		return goja.Null()
	}
	switch node.Kind {
	case yaml.DocumentNode:
		return newScriptYAMLDocumentObject(runtime, content, node)
	case yaml.MappingNode:
		object := runtime.NewObject()
		scriptYAMLSetNodeKind(runtime, object, "map")
		_ = object.Set("items", scriptYAMLMapItems(runtime, content, node))
		_ = object.Set("range", scriptYAMLNodeRange(content, node))
		_ = object.Set("get", func(call goja.FunctionCall) goja.Value {
			return scriptYAMLMapGet(runtime, content, node, call.Argument(0), call.Argument(1))
		})
		_ = object.Set("getIn", func(call goja.FunctionCall) goja.Value {
			return scriptYAMLGetIn(runtime, content, node, call.Argument(0), call.Argument(1))
		})
		_ = object.Set("toJSON", func(goja.FunctionCall) goja.Value {
			return runtime.ToValue(scriptYAMLNodeToInterface(node))
		})
		return object
	case yaml.SequenceNode:
		object := runtime.NewObject()
		scriptYAMLSetNodeKind(runtime, object, "seq")
		items := make([]goja.Value, 0, len(node.Content))
		for _, child := range node.Content {
			items = append(items, newScriptYAMLNodeObject(runtime, content, child))
		}
		_ = object.Set("items", items)
		_ = object.Set("range", scriptYAMLNodeRange(content, node))
		_ = object.Set("get", func(call goja.FunctionCall) goja.Value {
			index := int(call.Argument(0).ToInteger())
			if index < 0 || index >= len(node.Content) {
				return goja.Undefined()
			}
			return scriptYAMLNodeReturnValue(runtime, content, node.Content[index], call.Argument(1))
		})
		_ = object.Set("toJSON", func(goja.FunctionCall) goja.Value {
			return runtime.ToValue(scriptYAMLNodeToInterface(node))
		})
		return object
	case yaml.AliasNode:
		return newScriptYAMLNodeObject(runtime, content, node.Alias)
	default:
		object := runtime.NewObject()
		scriptYAMLSetNodeKind(runtime, object, "scalar")
		_ = object.Set("value", scriptYAMLNodeToInterface(node))
		_ = object.Set("range", scriptYAMLNodeRange(content, node))
		_ = object.Set("toJSON", func(goja.FunctionCall) goja.Value {
			return runtime.ToValue(scriptYAMLNodeToInterface(node))
		})
		_ = object.Set("toString", func(goja.FunctionCall) goja.Value {
			return runtime.ToValue(node.Value)
		})
		return object
	}
}

func scriptYAMLSetNodeKind(runtime *goja.Runtime, object *goja.Object, kind string) {
	_ = object.DefineDataProperty("__liteapiYamlKind", runtime.ToValue(kind), goja.FLAG_FALSE, goja.FLAG_FALSE, goja.FLAG_FALSE)
}

func scriptYAMLNodeKind(runtime *goja.Runtime, value goja.Value) string {
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		return ""
	}
	kind := value.ToObject(runtime).Get("__liteapiYamlKind")
	if kind == nil || goja.IsUndefined(kind) || goja.IsNull(kind) {
		return ""
	}
	return kind.String()
}

func scriptYAMLRootNode(node *yaml.Node) *yaml.Node {
	if node != nil && node.Kind == yaml.DocumentNode && len(node.Content) > 0 {
		return node.Content[0]
	}
	return node
}

func scriptYAMLMapItems(runtime *goja.Runtime, content string, node *yaml.Node) []goja.Value {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	items := make([]goja.Value, 0, len(node.Content)/2)
	for index := 0; index+1 < len(node.Content); index += 2 {
		pair := runtime.NewObject()
		_ = pair.Set("key", newScriptYAMLNodeObject(runtime, content, node.Content[index]))
		_ = pair.Set("value", newScriptYAMLNodeObject(runtime, content, node.Content[index+1]))
		items = append(items, pair)
	}
	return items
}

func scriptYAMLMapGet(runtime *goja.Runtime, content string, node *yaml.Node, keyValue, keepNode goja.Value) goja.Value {
	if node == nil || node.Kind != yaml.MappingNode {
		return goja.Undefined()
	}
	key := keyValue.String()
	for index := 0; index+1 < len(node.Content); index += 2 {
		if fmt.Sprint(scriptYAMLNodeToInterface(node.Content[index])) == key {
			return scriptYAMLNodeReturnValue(runtime, content, node.Content[index+1], keepNode)
		}
	}
	return goja.Undefined()
}

func scriptYAMLGetIn(runtime *goja.Runtime, content string, node *yaml.Node, pathValue, keepNode goja.Value) goja.Value {
	current := node
	pathObject := pathValue.ToObject(runtime)
	length := int(pathObject.Get("length").ToInteger())
	for index := 0; index < length; index++ {
		part := pathObject.Get(strconv.Itoa(index))
		if current == nil {
			return goja.Undefined()
		}
		switch current.Kind {
		case yaml.MappingNode:
			current = scriptYAMLMapChild(current, part.String())
		case yaml.SequenceNode:
			childIndex := int(part.ToInteger())
			if childIndex < 0 || childIndex >= len(current.Content) {
				return goja.Undefined()
			}
			current = current.Content[childIndex]
		case yaml.DocumentNode:
			current = scriptYAMLRootNode(current)
			index--
		case yaml.AliasNode:
			current = current.Alias
			index--
		default:
			return goja.Undefined()
		}
	}
	return scriptYAMLNodeReturnValue(runtime, content, current, keepNode)
}

func scriptYAMLMapChild(node *yaml.Node, key string) *yaml.Node {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	for index := 0; index+1 < len(node.Content); index += 2 {
		if fmt.Sprint(scriptYAMLNodeToInterface(node.Content[index])) == key {
			return node.Content[index+1]
		}
	}
	return nil
}

func scriptYAMLNodeReturnValue(runtime *goja.Runtime, content string, node *yaml.Node, keepNode goja.Value) goja.Value {
	if node == nil {
		return goja.Undefined()
	}
	if keepNode != nil && !goja.IsUndefined(keepNode) && keepNode.ToBoolean() {
		return newScriptYAMLNodeObject(runtime, content, node)
	}
	return runtime.ToValue(scriptYAMLNodeToInterface(node))
}

func scriptYAMLNodeToInterface(node *yaml.Node) interface{} {
	if node == nil {
		return nil
	}
	switch node.Kind {
	case yaml.DocumentNode:
		return scriptYAMLNodeToInterface(scriptYAMLRootNode(node))
	case yaml.MappingNode:
		out := map[string]interface{}{}
		for index := 0; index+1 < len(node.Content); index += 2 {
			out[fmt.Sprint(scriptYAMLNodeToInterface(node.Content[index]))] = scriptYAMLNodeToInterface(node.Content[index+1])
		}
		return out
	case yaml.SequenceNode:
		out := make([]interface{}, len(node.Content))
		for index, child := range node.Content {
			out[index] = scriptYAMLNodeToInterface(child)
		}
		return out
	case yaml.AliasNode:
		return scriptYAMLNodeToInterface(node.Alias)
	default:
		var value interface{}
		if err := node.Decode(&value); err == nil {
			return scriptYAMLNormalize(value)
		}
		return node.Value
	}
}

func scriptYAMLLineOffsets(content string) []int {
	offsets := []int{0}
	for index, char := range content {
		if char == '\n' {
			offsets = append(offsets, index+1)
		}
	}
	return offsets
}

func scriptYAMLNodeRange(content string, node *yaml.Node) []int {
	start := scriptYAMLOffsetForPosition(content, node.Line, node.Column)
	end := start + len(node.Value)
	if end < start {
		end = start
	}
	if end > len(content) {
		end = len(content)
	}
	return []int{start, end}
}

func scriptYAMLOffsetForPosition(content string, line, column int) int {
	if line <= 0 {
		return 0
	}
	offsets := scriptYAMLLineOffsets(content)
	if line > len(offsets) {
		return len(content)
	}
	offset := offsets[line-1] + max(0, column-1)
	if offset > len(content) {
		return len(content)
	}
	return offset
}

func formatXMLString(input string, options scriptXMLFormatterOptions) (string, error) {
	if strings.TrimSpace(input) == "" {
		return strings.TrimSpace(input), nil
	}
	if err := validateXMLString(input); err != nil {
		return "", err
	}
	if options.indentation == "" && options.lineSeparator == "" {
		return minifyXMLTokens(input), nil
	}
	tokens := xmlFormatTokens(input)
	lines := make([]string, 0, len(tokens))
	level := 0
	for index := 0; index < len(tokens); index++ {
		token := tokens[index]
		if strings.TrimSpace(token) == "" {
			continue
		}
		switch {
		case isXMLClosingTag(token):
			if level > 0 {
				level--
			}
			lines = append(lines, strings.Repeat(options.indentation, level)+token)
		case isXMLOpeningTag(token):
			if index+2 < len(tokens) && !isXMLTag(tokens[index+1]) && isXMLClosingTag(tokens[index+2]) {
				lines = append(lines, strings.Repeat(options.indentation, level)+token+tokens[index+1]+tokens[index+2])
				index += 2
				continue
			}
			lines = append(lines, strings.Repeat(options.indentation, level)+token)
			level++
		default:
			lines = append(lines, strings.Repeat(options.indentation, level)+token)
		}
	}
	return strings.Join(lines, options.lineSeparator), nil
}

func validateXMLString(input string) error {
	decoder := xml.NewDecoder(strings.NewReader(input))
	for {
		if _, err := decoder.Token(); errors.Is(err, io.EOF) {
			return nil
		} else if err != nil {
			return err
		}
	}
}

func xmlFormatTokens(input string) []string {
	tokens := []string{}
	for len(input) > 0 {
		tagStart := strings.Index(input, "<")
		if tagStart < 0 {
			if strings.TrimSpace(input) != "" {
				tokens = append(tokens, input)
			}
			break
		}
		if tagStart > 0 {
			text := input[:tagStart]
			if strings.TrimSpace(text) != "" {
				tokens = append(tokens, text)
			}
			input = input[tagStart:]
		}
		tagEnd := strings.Index(input, ">")
		if tagEnd < 0 {
			tokens = append(tokens, input)
			break
		}
		tokens = append(tokens, strings.TrimSpace(input[:tagEnd+1]))
		input = input[tagEnd+1:]
	}
	return tokens
}

func minifyXMLTokens(input string) string {
	var b strings.Builder
	for _, token := range xmlFormatTokens(input) {
		if strings.TrimSpace(token) == "" {
			continue
		}
		b.WriteString(token)
	}
	return b.String()
}

func isXMLTag(token string) bool {
	return strings.HasPrefix(strings.TrimSpace(token), "<")
}

func isXMLClosingTag(token string) bool {
	return strings.HasPrefix(strings.TrimSpace(token), "</")
}

func isXMLOpeningTag(token string) bool {
	token = strings.TrimSpace(token)
	return strings.HasPrefix(token, "<") &&
		!strings.HasPrefix(token, "</") &&
		!strings.HasPrefix(token, "<?") &&
		!strings.HasPrefix(token, "<!") &&
		!strings.HasSuffix(token, "/>")
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

type scriptSendRequestConfig struct {
	Method  string
	URL     string
	Headers map[string]string
	Params  map[string]string
	Body    interface{}
	HasBody bool
}

func scriptSendRequest(runtime *goja.Runtime, configValue goja.Value, vars map[string]string) (goja.Value, goja.Value, *types.TimelineItem, error) {
	config, err := scriptSendRequestConfigFromValue(runtime, configValue, vars)
	if err != nil {
		return nil, nil, nil, err
	}
	targetURL := appendScriptSendRequestParams(config.URL, config.Params)
	timelineEntry := &types.TimelineItem{
		At:     time.Now(),
		Method: strings.ToUpper(scalar.FirstNonEmpty(config.Method, http.MethodGet)),
		URL:    targetURL,
	}
	var bodyReader io.Reader
	if config.HasBody {
		bodyText, contentType := scriptSendRequestBody(config.Body)
		bodyReader = strings.NewReader(bodyText)
		if contentType != "" && !StringMapHasKey(config.Headers, "Content-Type") {
			config.Headers["Content-Type"] = contentType
		}
	}
	req, err := http.NewRequest(config.Method, targetURL, bodyReader)
	if err != nil {
		timelineEntry.Error = err.Error()
		timelineEntry.StatusText = "Error"
		timelineEntry.Message = fmt.Sprintf("%s %s -> Error", timelineEntry.Method, timelineEntry.URL)
		return nil, nil, timelineEntry, err
	}
	for name, value := range config.Headers {
		if strings.TrimSpace(name) != "" {
			req.Header.Set(name, value)
		}
	}
	start := time.Now()
	// US-017: shared client. Posture deliberately unchanged: script
	// sendRequest has never used the collection's proxy or TLS settings, and
	// adopting them here would let a verify-off request reach a script's
	// outbound call without anything in the UI saying so.
	res, err := httpClient().Do(req)
	duration := time.Since(start).Milliseconds()
	timelineEntry.Duration = duration
	if err != nil {
		timelineEntry.Error = err.Error()
		timelineEntry.StatusText = err.Error()
		timelineEntry.Message = fmt.Sprintf("%s %s -> %s", timelineEntry.Method, timelineEntry.URL, err.Error())
		return nil, scriptSendRequestError(runtime, 0, err.Error(), nil), timelineEntry, nil
	}
	defer func() { _ = res.Body.Close() }()
	bodyBytes, err := io.ReadAll(res.Body)
	if err != nil {
		timelineEntry.Error = err.Error()
		timelineEntry.StatusText = "Error"
		timelineEntry.Message = fmt.Sprintf("%s %s -> Error", timelineEntry.Method, timelineEntry.URL)
		return nil, nil, timelineEntry, err
	}
	timelineEntry.Status = res.StatusCode
	timelineEntry.StatusText = scalar.CleanStatusText(res.StatusCode, res.Status)
	timelineEntry.Message = fmt.Sprintf("%s %s -> %d", timelineEntry.Method, timelineEntry.URL, res.StatusCode)
	headers := map[string]string{}
	for name, values := range res.Header {
		headers[strings.ToLower(name)] = strings.Join(values, ", ")
	}
	bodyText := string(bodyBytes)
	data := interface{}(bodyText)
	if jsonValue, ok := responseJSONValue(bodyText); ok {
		data = jsonValue
	}
	responseObject := runtime.NewObject()
	_ = responseObject.Set("status", res.StatusCode)
	_ = responseObject.Set("statusCode", res.StatusCode)
	_ = responseObject.Set("statusText", scalar.CleanStatusText(res.StatusCode, res.Status))
	_ = responseObject.Set("headers", headers)
	_ = responseObject.Set("data", data)
	_ = responseObject.Set("body", bodyText)
	_ = responseObject.Set("dataBuffer", base64.StdEncoding.EncodeToString(bodyBytes))
	_ = responseObject.Set("size", len(bodyBytes))
	_ = responseObject.Set("duration", duration)
	_ = responseObject.Set("responseTime", duration)
	_ = responseObject.Set("url", targetURL)
	scriptAttachResolvedThenable(runtime, responseObject)
	if res.StatusCode >= 400 {
		return responseObject, scriptSendRequestError(runtime, res.StatusCode, res.Status, responseObject), timelineEntry, nil
	}
	return responseObject, nil, timelineEntry, nil
}

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

func scriptSendRequestConfigFromValue(runtime *goja.Runtime, value goja.Value, vars map[string]string) (scriptSendRequestConfig, error) {
	config := scriptSendRequestConfig{
		Method:  http.MethodGet,
		Headers: map[string]string{},
		Params:  map[string]string{},
	}
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		return config, errors.New("sendRequest requires a URL or request config")
	}
	if text, ok := value.Export().(string); ok {
		config.URL = interp.Interpolate(text, vars)
		return config, scriptSendRequestValidateConfig(config)
	}
	object := value.ToObject(runtime)
	if method := object.Get("method"); method != nil && !goja.IsUndefined(method) && !goja.IsNull(method) {
		config.Method = strings.ToUpper(method.String())
	}
	if config.Method == "" {
		config.Method = http.MethodGet
	}
	if targetURL := object.Get("url"); targetURL != nil && !goja.IsUndefined(targetURL) && !goja.IsNull(targetURL) {
		config.URL = interp.Interpolate(targetURL.String(), vars)
	}
	if headers := object.Get("headers"); headers != nil && !goja.IsUndefined(headers) && !goja.IsNull(headers) {
		config.Headers = scriptSendRequestStringMap(runtime, headers, vars)
	}
	if params := object.Get("params"); params != nil && !goja.IsUndefined(params) && !goja.IsNull(params) {
		config.Params = scriptSendRequestStringMap(runtime, params, vars)
	}
	if data := object.Get("data"); data != nil && !goja.IsUndefined(data) && !goja.IsNull(data) {
		config.Body = data.Export()
		config.HasBody = true
	} else if body := object.Get("body"); body != nil && !goja.IsUndefined(body) && !goja.IsNull(body) {
		config.Body = body.Export()
		config.HasBody = true
	}
	if config.HasBody {
		config.Body = interpolateScriptSendRequestBody(config.Body, vars)
	}
	return config, scriptSendRequestValidateConfig(config)
}

func scriptSendRequestValidateConfig(config scriptSendRequestConfig) error {
	if strings.TrimSpace(config.URL) == "" {
		return errors.New("sendRequest URL is required")
	}
	if strings.TrimSpace(config.Method) == "" {
		return errors.New("sendRequest method is required")
	}
	return nil
}

func scriptSendRequestStringMap(runtime *goja.Runtime, value goja.Value, vars map[string]string) map[string]string {
	out := map[string]string{}
	object := value.ToObject(runtime)
	for _, key := range object.Keys() {
		raw := object.Get(key)
		if raw == nil || goja.IsUndefined(raw) || goja.IsNull(raw) {
			continue
		}
		out[interp.Interpolate(key, vars)] = interp.Interpolate(raw.String(), vars)
	}
	return out
}

func interpolateScriptSendRequestBody(value interface{}, vars map[string]string) interface{} {
	switch typed := value.(type) {
	case string:
		return interp.Interpolate(typed, vars)
	case map[string]interface{}:
		out := map[string]interface{}{}
		for key, child := range typed {
			out[key] = interpolateScriptSendRequestBody(child, vars)
		}
		return out
	case []interface{}:
		out := make([]interface{}, len(typed))
		for index, child := range typed {
			out[index] = interpolateScriptSendRequestBody(child, vars)
		}
		return out
	default:
		return value
	}
}

func scriptSendRequestBody(value interface{}) (string, string) {
	switch typed := value.(type) {
	case nil:
		return "", ""
	case string:
		return typed, ""
	case []byte:
		return string(typed), ""
	default:
		data, err := json.Marshal(typed)
		if err != nil {
			return fmt.Sprint(typed), ""
		}
		return string(data), "application/json"
	}
}

func appendScriptSendRequestParams(rawURL string, params map[string]string) string {
	if len(params) == 0 {
		return rawURL
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	query := parsed.Query()
	for name, value := range params {
		query.Set(name, value)
	}
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

func scriptSendRequestError(runtime *goja.Runtime, status int, message string, response goja.Value) goja.Value {
	errorObject := runtime.NewObject()
	_ = errorObject.Set("status", status)
	_ = errorObject.Set("message", message)
	if response != nil {
		_ = errorObject.Set("response", response)
	}
	return errorObject
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

func newScriptTimersObject(runtime *goja.Runtime, promisesObject goja.Value) goja.Value {
	object := runtime.NewObject()
	for _, name := range []string{"setTimeout", "clearTimeout", "setInterval", "clearInterval", "setImmediate", "clearImmediate"} {
		_ = object.Set(name, runtime.Get(name))
	}
	_ = object.Set("promises", promisesObject)
	_ = object.Set("default", object)
	return object
}

func newScriptTimersPromisesObject(runtime *goja.Runtime) goja.Value {
	script := `(function () {
  const nativeSetTimeout = globalThis.setTimeout;
  const nativeClearTimeout = globalThis.clearTimeout;
  const nativeSetImmediate = globalThis.setImmediate;
  const nativeClearImmediate = globalThis.clearImmediate;

  function abortError(signal) {
    if (signal && signal.reason !== undefined) {
      return signal.reason;
    }
    const err = new Error("The operation was aborted");
    err.name = "AbortError";
    err.code = "ABORT_ERR";
    return err;
  }

  function addAbortListener(signal, onAbort) {
    if (!signal || typeof signal.addEventListener !== "function") {
      return function () {};
    }
    signal.addEventListener("abort", onAbort, { once: true });
    return function () {
      if (typeof signal.removeEventListener === "function") {
        signal.removeEventListener("abort", onAbort);
      }
    };
  }

  function applyRef(handle, options) {
    if (options && options.ref === false && handle && typeof handle.unref === "function") {
      handle.unref();
    }
  }

  function timeoutPromise(delay, value, options) {
    options = options || {};
    return new Promise(function (resolve, reject) {
      const signal = options.signal;
      if (signal && signal.aborted) {
        reject(abortError(signal));
        return;
      }
      let handle;
      let removeAbort = function () {};
      let settled = false;
      function settle(fn, result) {
        if (settled) return;
        settled = true;
        removeAbort();
        fn(result);
      }
      function onAbort() {
        if (handle !== undefined) {
          nativeClearTimeout(handle);
        }
        settle(reject, abortError(signal));
      }
      removeAbort = addAbortListener(signal, onAbort);
      handle = nativeSetTimeout(function () {
        settle(resolve, value);
      }, delay);
      applyRef(handle, options);
    });
  }

  function immediatePromise(value, options) {
    options = options || {};
    return new Promise(function (resolve, reject) {
      const signal = options.signal;
      if (signal && signal.aborted) {
        reject(abortError(signal));
        return;
      }
      let handle;
      let removeAbort = function () {};
      let settled = false;
      function settle(fn, result) {
        if (settled) return;
        settled = true;
        removeAbort();
        fn(result);
      }
      function onAbort() {
        if (handle !== undefined) {
          nativeClearImmediate(handle);
        }
        settle(reject, abortError(signal));
      }
      removeAbort = addAbortListener(signal, onAbort);
      handle = nativeSetImmediate(function () {
        settle(resolve, value);
      });
      applyRef(handle, options);
    });
  }

  function intervalIterator(delay, value, options) {
    options = options || {};
    const signal = options.signal;
    let done = false;
    function finish() {
      done = true;
      return Promise.resolve({ value: undefined, done: true });
    }
    const iterator = {
      next: function () {
        if (done) {
          return Promise.resolve({ value: undefined, done: true });
        }
        if (signal && signal.aborted) {
          done = true;
          return Promise.reject(abortError(signal));
        }
        return timeoutPromise(delay, value, options).then(function (nextValue) {
          if (done) {
            return { value: undefined, done: true };
          }
          return { value: nextValue, done: false };
        });
      },
      return: finish,
      throw: function (err) {
        done = true;
        return Promise.reject(err);
      }
    };
    if (typeof Symbol !== "undefined" && Symbol.asyncIterator) {
      iterator[Symbol.asyncIterator] = function () {
        return iterator;
      };
    }
    return iterator;
  }

  const scheduler = {
    wait: function (delay, options) {
      return timeoutPromise(delay, undefined, options);
    },
    yield: function () {
      return immediatePromise(undefined);
    }
  };
  const timersPromises = {
    setTimeout: timeoutPromise,
    setImmediate: immediatePromise,
    setInterval: intervalIterator,
    scheduler: scheduler
  };
  timersPromises.default = timersPromises;
  return timersPromises;
})()`
	value, err := runtime.RunProgram(scriptTimersPromisesShim.compiled(script))
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

func newScriptTV4Object(runtime *goja.Runtime) *goja.Object {
	tv4Object := runtime.NewObject()
	setError := func(message string) {
		if strings.TrimSpace(message) == "" {
			_ = tv4Object.Set("error", goja.Null())
			return
		}
		errorObject := runtime.NewObject()
		_ = errorObject.Set("message", message)
		_ = tv4Object.Set("error", errorObject)
	}
	setError("")
	_ = tv4Object.Set("validate", func(call goja.FunctionCall) goja.Value {
		options := runtime.NewObject()
		_ = options.Set("strict", false)
		ok, err := expectMatchesJSONSchema(runtime, call.Argument(0), call.Argument(1), options)
		if err != nil {
			setError(err.Error())
			return runtime.ToValue(false)
		}
		if !ok {
			setError("Data does not match schema")
			return runtime.ToValue(false)
		}
		setError("")
		return runtime.ToValue(true)
	})
	return tv4Object
}

func installScriptAjv(runtime *goja.Runtime) goja.Value {
	_ = runtime.Set("__liteApiValidateSchema", func(call goja.FunctionCall) goja.Value {
		ok, err := expectMatchesJSONSchema(runtime, call.Argument(0), call.Argument(1), call.Argument(2))
		result := runtime.NewObject()
		_ = result.Set("valid", ok && err == nil)
		if err != nil {
			_ = result.Set("error", err.Error())
		} else if !ok {
			_ = result.Set("error", "data does not match schema")
		} else {
			_ = result.Set("error", goja.Null())
		}
		return result
	})
	script := `(function () {
  function Ajv(options) {
    this.opts = Object.assign({}, options || {});
    if (this.opts.strict === undefined) {
      this.opts.strict = false;
    }
  }
  Ajv.prototype.compile = function(schema) {
    const options = this.opts;
    function validate(data) {
      const result = globalThis.__liteApiValidateSchema(data, schema, options);
      if (result.valid) {
        validate.errors = null;
        return true;
      }
      validate.errors = [{ message: result.error || "data does not match schema" }];
      return false;
    }
    validate.errors = null;
    validate.schema = schema;
    return validate;
  };
  Ajv.prototype.validate = function(schema, data) {
    return this.compile(schema)(data);
  };
  globalThis.Ajv = Ajv;
  return Ajv;
})()`
	value, err := runtime.RunProgram(scriptAjvShim.compiled(script))
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

func scriptModuleIsLocalPath(name string) bool {
	return strings.HasPrefix(name, ".") || filepath.IsAbs(name)
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

func scriptPathWithinRoot(root, candidate string) bool {
	root = filepath.Clean(root)
	candidate = filepath.Clean(candidate)
	rel, err := filepath.Rel(root, candidate)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
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

func scriptPackageMainPath(packageDir string) (string, bool) {
	content, err := os.ReadFile(filepath.Join(packageDir, "package.json"))
	if err != nil {
		return "", false
	}
	var payload struct {
		Main string `json:"main"`
	}
	if err := json.Unmarshal(content, &payload); err != nil {
		return "", false
	}
	mainPath := strings.TrimSpace(payload.Main)
	if mainPath == "" {
		return "", false
	}
	return filepath.Clean(filepath.Join(packageDir, filepath.FromSlash(mainPath))), true
}

func newScriptFSObject(runtime *goja.Runtime, collectionPath, sandboxMode string) *goja.Object {
	fsObject := runtime.NewObject()
	promisesObject := runtime.NewObject()
	developerMode := NormalizeJSSandboxMode(sandboxMode) == "developer"
	readFile := func(call goja.FunctionCall) goja.Value {
		data, err := readScriptFSFile(collectionPath, call.Argument(0), sandboxMode)
		if err != nil {
			panic(runtime.NewGoError(err))
		}
		return scriptFSFileValue(runtime, data, scriptFSEncoding(runtime, call.Argument(1)))
	}
	_ = fsObject.Set("readFileSync", readFile)
	_ = fsObject.Set("existsSync", func(call goja.FunctionCall) goja.Value {
		path, err := resolveScriptFSPath(collectionPath, call.Argument(0).String(), sandboxMode)
		if err != nil {
			return runtime.ToValue(false)
		}
		_, err = os.Stat(path)
		return runtime.ToValue(err == nil)
	})
	_ = fsObject.Set("statSync", func(call goja.FunctionCall) goja.Value {
		path, err := resolveScriptFSPath(collectionPath, call.Argument(0).String(), sandboxMode)
		if err != nil {
			panic(runtime.NewGoError(err))
		}
		info, err := os.Stat(path)
		if err != nil {
			panic(runtime.NewGoError(err))
		}
		return scriptFSStatValue(runtime, info)
	})
	_ = fsObject.Set("readdirSync", func(call goja.FunctionCall) goja.Value {
		path, err := resolveScriptFSPath(collectionPath, call.Argument(0).String(), sandboxMode)
		if err != nil {
			panic(runtime.NewGoError(err))
		}
		entries, err := os.ReadDir(path)
		if err != nil {
			panic(runtime.NewGoError(err))
		}
		names := make([]string, 0, len(entries))
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		return runtime.ToValue(names)
	})
	_ = promisesObject.Set("readFile", func(call goja.FunctionCall) goja.Value {
		data, err := readScriptFSFile(collectionPath, call.Argument(0), sandboxMode)
		if err != nil {
			return scriptRejectedPromise(runtime, err)
		}
		return scriptResolvedPromise(runtime, scriptFSFileValue(runtime, data, scriptFSEncoding(runtime, call.Argument(1))))
	})
	if developerMode {
		writeFileAction := func(call goja.FunctionCall) (goja.Value, error) {
			path, err := resolveScriptFSPath(collectionPath, call.Argument(0).String(), sandboxMode)
			if err != nil {
				return nil, err
			}
			data, err := scriptFSWriteBytes(runtime, call.Argument(1), call.Argument(2))
			if err != nil {
				return nil, err
			}
			if err := os.WriteFile(path, data, 0o666); err != nil {
				return nil, err
			}
			return goja.Undefined(), nil
		}
		mkdirAction := func(call goja.FunctionCall) (goja.Value, error) {
			path, err := resolveScriptFSPath(collectionPath, call.Argument(0).String(), sandboxMode)
			if err != nil {
				return nil, err
			}
			if scriptFSOptionBool(runtime, call.Argument(1), "recursive") {
				if err := os.MkdirAll(path, 0o777); err != nil {
					return nil, err
				}
				return goja.Undefined(), nil
			}
			if err := os.Mkdir(path, 0o777); err != nil {
				return nil, err
			}
			return goja.Undefined(), nil
		}
		removeAction := func(call goja.FunctionCall) (goja.Value, error) {
			path, err := resolveScriptFSPath(collectionPath, call.Argument(0).String(), sandboxMode)
			if err != nil {
				if scriptFSOptionBool(runtime, call.Argument(1), "force") {
					return goja.Undefined(), nil
				}
				return nil, err
			}
			recursive := scriptFSOptionBool(runtime, call.Argument(1), "recursive")
			force := scriptFSOptionBool(runtime, call.Argument(1), "force")
			if !recursive {
				err = os.Remove(path)
			} else if !force {
				if _, statErr := os.Stat(path); statErr != nil {
					err = statErr
				} else {
					err = os.RemoveAll(path)
				}
			} else {
				err = os.RemoveAll(path)
			}
			ignorableMissing := force && os.IsNotExist(err)
			if err != nil && !ignorableMissing {
				return nil, err
			}
			return goja.Undefined(), nil
		}
		unlinkAction := func(call goja.FunctionCall) (goja.Value, error) {
			path, err := resolveScriptFSPath(collectionPath, call.Argument(0).String(), sandboxMode)
			if err != nil {
				return nil, err
			}
			if err := os.Remove(path); err != nil {
				return nil, err
			}
			return goja.Undefined(), nil
		}
		syncAction := func(action func(goja.FunctionCall) (goja.Value, error)) func(goja.FunctionCall) goja.Value {
			return func(call goja.FunctionCall) goja.Value {
				value, err := action(call)
				if err != nil {
					panic(runtime.NewGoError(err))
				}
				return value
			}
		}
		_ = fsObject.Set("writeFileSync", syncAction(writeFileAction))
		_ = fsObject.Set("mkdirSync", syncAction(mkdirAction))
		_ = fsObject.Set("rmSync", syncAction(removeAction))
		_ = fsObject.Set("unlinkSync", syncAction(unlinkAction))
		_ = promisesObject.Set("writeFile", func(call goja.FunctionCall) goja.Value {
			if _, err := writeFileAction(call); err != nil {
				return scriptRejectedPromise(runtime, err)
			}
			return scriptResolvedPromise(runtime, goja.Undefined())
		})
		_ = promisesObject.Set("mkdir", func(call goja.FunctionCall) goja.Value {
			if _, err := mkdirAction(call); err != nil {
				return scriptRejectedPromise(runtime, err)
			}
			return scriptResolvedPromise(runtime, goja.Undefined())
		})
		_ = promisesObject.Set("rm", func(call goja.FunctionCall) goja.Value {
			if _, err := removeAction(call); err != nil {
				return scriptRejectedPromise(runtime, err)
			}
			return scriptResolvedPromise(runtime, goja.Undefined())
		})
		_ = promisesObject.Set("unlink", func(call goja.FunctionCall) goja.Value {
			if _, err := unlinkAction(call); err != nil {
				return scriptRejectedPromise(runtime, err)
			}
			return scriptResolvedPromise(runtime, goja.Undefined())
		})
	}
	_ = fsObject.Set("promises", promisesObject)
	return fsObject
}

func resolveScriptFSPath(root, name, sandboxMode string) (string, error) {
	if NormalizeJSSandboxMode(sandboxMode) == "developer" {
		base := strings.TrimSpace(root)
		if base == "" {
			base = "."
		}
		var candidate string
		if filepath.IsAbs(name) {
			candidate = filepath.Clean(name)
		} else {
			candidate = filepath.Clean(filepath.Join(base, name))
		}
		absolute, err := filepath.Abs(candidate)
		if err != nil {
			return "", err
		}
		return filepath.Clean(absolute), nil
	}
	if strings.TrimSpace(root) == "" {
		return "", errors.New("collection path is unavailable")
	}
	rootPath, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	rootPath = filepath.Clean(rootPath)
	if evaluatedRoot, err := filepath.EvalSymlinks(rootPath); err == nil {
		rootPath = evaluatedRoot
	}
	var candidate string
	if filepath.IsAbs(name) {
		candidate = filepath.Clean(name)
	} else {
		candidate = filepath.Clean(filepath.Join(rootPath, name))
	}
	candidate, err = filepath.Abs(candidate)
	if err != nil {
		return "", err
	}
	candidate = filepath.Clean(candidate)
	if evaluatedCandidate, err := filepath.EvalSymlinks(candidate); err == nil {
		candidate = evaluatedCandidate
	}
	rel, err := filepath.Rel(rootPath, candidate)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("fs path %q escapes collection", name)
	}
	return candidate, nil
}

func readScriptFSFile(collectionPath string, value goja.Value, sandboxMode string) ([]byte, error) {
	path, err := resolveScriptFSPath(collectionPath, value.String(), sandboxMode)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return nil, fmt.Errorf("%s is a directory", path)
	}
	return os.ReadFile(path)
}

func scriptFSWriteBytes(runtime *goja.Runtime, value, options goja.Value) ([]byte, error) {
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		return nil, errors.New("fs data must be a string, Buffer, ArrayBuffer, typed array, or byte array")
	}
	encoding := scriptFSEncoding(runtime, options)
	switch typed := value.Export().(type) {
	case string:
		return scriptFSBytesFromString(typed, encoding)
	case []byte:
		return append([]byte(nil), typed...), nil
	case []interface{}:
		return scriptFSBytesFromInterfaceSlice(typed), nil
	case []int:
		bytes := make([]byte, len(typed))
		for index, item := range typed {
			bytes[index] = byte(item)
		}
		return bytes, nil
	}
	object := value.ToObject(runtime)
	lengthValue := object.Get("length")
	if lengthValue != nil && !goja.IsUndefined(lengthValue) && !goja.IsNull(lengthValue) {
		return scriptFSBytesFromIndexedObject(object, lengthValue.ToInteger())
	}
	byteLengthValue := object.Get("byteLength")
	if byteLengthValue != nil && !goja.IsUndefined(byteLengthValue) && !goja.IsNull(byteLengthValue) {
		// A DataView has byteLength but no length, and new Uint8Array(dataView)
		// treats it as an array-like with no length at all — producing an empty
		// view whose bytes then read back as zero. Going through its buffer
		// (honouring byteOffset, since a view need not start at zero) is what
		// makes fs.writeFile(dataView) write the data rather than a file of the
		// right size full of nulls.
		view, err := scriptFSUint8ArrayOver(runtime, value, object)
		if err != nil {
			return nil, err
		}
		return scriptFSBytesFromIndexedObject(view.ToObject(runtime), byteLengthValue.ToInteger())
	}
	return nil, errors.New("fs data must be a string, Buffer, ArrayBuffer, typed array, or byte array")
}

func scriptFSBytesFromString(value, encoding string) ([]byte, error) {
	switch normalizeScriptFSEncoding(encoding) {
	case "", "utf8", "utf":
		return []byte(value), nil
	case "base64", "base64url":
		return decodeScriptBase64(value)
	case "hex":
		return hex.DecodeString(strings.TrimSpace(value))
	case "latin1", "binary", "ascii":
		return scriptBytesFromBinaryString(value)
	default:
		return nil, fmt.Errorf("unsupported fs encoding: %s", encoding)
	}
}

func scriptFSBytesFromInterfaceSlice(values []interface{}) []byte {
	bytes := make([]byte, len(values))
	for index, item := range values {
		switch typed := item.(type) {
		case int:
			bytes[index] = byte(typed)
		case int64:
			bytes[index] = byte(typed)
		case float64:
			bytes[index] = byte(typed)
		case json.Number:
			if parsed, err := typed.Int64(); err == nil {
				bytes[index] = byte(parsed)
			}
		default:
			bytes[index] = byte(0)
		}
	}
	return bytes
}

func scriptFSUint8ArrayOver(runtime *goja.Runtime, value goja.Value, object *goja.Object) (*goja.Object, error) {
	uint8Array := runtime.Get("Uint8Array")
	if buffer := object.Get("buffer"); buffer != nil && !goja.IsUndefined(buffer) && !goja.IsNull(buffer) {
		offset := int64(0)
		if byteOffset := object.Get("byteOffset"); byteOffset != nil && !goja.IsUndefined(byteOffset) && !goja.IsNull(byteOffset) {
			offset = byteOffset.ToInteger()
		}
		byteLength := object.Get("byteLength").ToInteger()
		return runtime.New(uint8Array, buffer, runtime.ToValue(offset), runtime.ToValue(byteLength))
	}
	return runtime.New(uint8Array, value)
}

func scriptFSBytesFromIndexedObject(object *goja.Object, length int64) ([]byte, error) {
	if length < 0 {
		return nil, errors.New("fs data length must be non-negative")
	}
	if length > 64*1024*1024 {
		return nil, errors.New("fs data is too large")
	}
	bytes := make([]byte, int(length))
	for index := int64(0); index < length; index++ {
		value := object.Get(strconv.FormatInt(index, 10))
		if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
			continue
		}
		bytes[index] = byte(value.ToInteger())
	}
	return bytes, nil
}

func scriptFSOptionBool(runtime *goja.Runtime, value goja.Value, name string) bool {
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		return false
	}
	if exported, ok := value.Export().(bool); ok {
		return exported
	}
	option := value.ToObject(runtime).Get(name)
	return option != nil && !goja.IsUndefined(option) && !goja.IsNull(option) && option.ToBoolean()
}

func scriptFSEncoding(runtime *goja.Runtime, value goja.Value) string {
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		return ""
	}
	if encoding, ok := value.Export().(string); ok {
		return normalizeScriptFSEncoding(encoding)
	}
	object := value.ToObject(runtime)
	encoding := object.Get("encoding")
	if encoding == nil || goja.IsUndefined(encoding) || goja.IsNull(encoding) {
		return ""
	}
	return normalizeScriptFSEncoding(encoding.String())
}

func normalizeScriptFSEncoding(value string) string {
	return strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(value), "-", ""), "_", ""))
}

func scriptFSFileValue(runtime *goja.Runtime, data []byte, encoding string) goja.Value {
	switch encoding {
	case "":
		return scriptBufferValue(runtime, data)
	case "utf8", "utf":
		return runtime.ToValue(string(data))
	case "base64":
		return runtime.ToValue(base64.StdEncoding.EncodeToString(data))
	case "hex":
		return runtime.ToValue(hex.EncodeToString(data))
	case "latin1", "binary", "ascii":
		return runtime.ToValue(scriptBinaryStringFromBytes(data))
	default:
		panic(runtime.NewTypeError("unsupported fs encoding: " + encoding))
	}
}

func scriptFSStatValue(runtime *goja.Runtime, info os.FileInfo) goja.Value {
	object := runtime.NewObject()
	_ = object.Set("size", info.Size())
	_ = object.Set("mtimeMs", float64(info.ModTime().UnixNano())/float64(time.Millisecond))
	_ = object.Set("isFile", func() bool { return !info.IsDir() })
	_ = object.Set("isDirectory", func() bool { return info.IsDir() })
	return object
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

func newScriptPathObject(runtime *goja.Runtime) *goja.Object {
	if filepath.Separator == '\\' {
		return newScriptWin32PathObject(runtime)
	}
	return newScriptPosixPathObject(runtime)
}

func linkScriptPathVariants(posixPathObject, win32PathObject *goja.Object) {
	_ = posixPathObject.Set("posix", posixPathObject)
	_ = posixPathObject.Set("win32", win32PathObject)
	_ = win32PathObject.Set("posix", posixPathObject)
	_ = win32PathObject.Set("win32", win32PathObject)
}

func newScriptPosixPathObject(runtime *goja.Runtime) *goja.Object {
	pathObject := runtime.NewObject()
	_ = pathObject.Set("sep", "/")
	_ = pathObject.Set("delimiter", ":")
	_ = pathObject.Set("join", func(call goja.FunctionCall) goja.Value {
		parts := scriptCallStringArgs(call)
		if len(parts) == 0 {
			return runtime.ToValue(".")
		}
		return runtime.ToValue(pathpkg.Clean(pathpkg.Join(parts...)))
	})
	_ = pathObject.Set("resolve", func(call goja.FunctionCall) goja.Value {
		return runtime.ToValue(scriptPosixPathResolve(scriptCallStringArgs(call)...))
	})
	_ = pathObject.Set("dirname", scriptPosixPathDirname)
	_ = pathObject.Set("basename", scriptPosixPathBasename)
	_ = pathObject.Set("extname", func(value string) string { return scriptPathExtname(value, "/") })
	_ = pathObject.Set("normalize", scriptPosixPathNormalize)
	_ = pathObject.Set("isAbsolute", func(value string) bool { return strings.HasPrefix(value, "/") })
	_ = pathObject.Set("relative", scriptPosixPathRelative)
	_ = pathObject.Set("parse", scriptPosixPathParse)
	_ = pathObject.Set("format", func(value goja.Value) string {
		return scriptPathFormat(runtime, value, "/", false)
	})
	_ = pathObject.Set("toNamespacedPath", func(value string) string { return value })
	_ = pathObject.Set("_makeLong", func(value string) string { return value })
	return pathObject
}

func newScriptWin32PathObject(runtime *goja.Runtime) *goja.Object {
	pathObject := runtime.NewObject()
	_ = pathObject.Set("sep", "\\")
	_ = pathObject.Set("delimiter", ";")
	_ = pathObject.Set("join", func(call goja.FunctionCall) goja.Value {
		return runtime.ToValue(scriptWin32PathJoin(scriptCallStringArgs(call)...))
	})
	_ = pathObject.Set("resolve", func(call goja.FunctionCall) goja.Value {
		return runtime.ToValue(scriptWin32PathResolve(scriptCallStringArgs(call)...))
	})
	_ = pathObject.Set("dirname", scriptWin32PathDirname)
	_ = pathObject.Set("basename", scriptWin32PathBasename)
	_ = pathObject.Set("extname", func(value string) string {
		return scriptPathExtnameFromBase(scriptWin32PathBasename(value))
	})
	_ = pathObject.Set("normalize", scriptWin32PathNormalize)
	_ = pathObject.Set("isAbsolute", scriptWin32PathIsAbsolute)
	_ = pathObject.Set("relative", scriptWin32PathRelative)
	_ = pathObject.Set("parse", scriptWin32PathParse)
	_ = pathObject.Set("format", func(value goja.Value) string {
		return scriptPathFormat(runtime, value, "\\", true)
	})
	_ = pathObject.Set("toNamespacedPath", func(value string) string { return value })
	_ = pathObject.Set("_makeLong", func(value string) string { return value })
	return pathObject
}

func scriptCallStringArgs(call goja.FunctionCall) []string {
	parts := make([]string, 0, len(call.Arguments))
	for _, arg := range call.Arguments {
		parts = append(parts, arg.String())
	}
	return parts
}

func scriptPosixPathCWD() string {
	cwd, err := os.Getwd()
	if err != nil || cwd == "" {
		return "/"
	}
	return filepath.ToSlash(cwd)
}

func scriptPosixPathResolve(parts ...string) string {
	resolved := ""
	for _, part := range parts {
		if part == "" {
			continue
		}
		if strings.HasPrefix(part, "/") {
			resolved = part
		} else if resolved == "" {
			resolved = part
		} else {
			resolved += "/" + part
		}
	}
	if resolved == "" {
		resolved = scriptPosixPathCWD()
	} else if !strings.HasPrefix(resolved, "/") {
		resolved = scriptPosixPathCWD() + "/" + resolved
	}
	return pathpkg.Clean(resolved)
}

func scriptPosixPathNormalize(value string) string {
	if value == "" {
		return "."
	}
	trailing := strings.HasSuffix(value, "/")
	normalized := pathpkg.Clean(value)
	if trailing && normalized != "/" && normalized != "." {
		normalized += "/"
	}
	return normalized
}

func scriptPosixPathDirname(value string) string {
	if value == "" {
		return "."
	}
	return pathpkg.Dir(value)
}

func scriptPosixPathBasename(value string, ext ...string) string {
	base := scriptPathBasename(value, "/")
	if len(ext) > 0 && ext[0] != "" && strings.HasSuffix(base, ext[0]) {
		return strings.TrimSuffix(base, ext[0])
	}
	return base
}

func scriptPosixPathRelative(from, to string) string {
	from = scriptPosixPathResolve(from)
	to = scriptPosixPathResolve(to)
	if from == to {
		return ""
	}
	fromParts := scriptPathNonEmptyParts(strings.Split(strings.Trim(from, "/"), "/"))
	toParts := scriptPathNonEmptyParts(strings.Split(strings.Trim(to, "/"), "/"))
	common := 0
	for common < len(fromParts) && common < len(toParts) && fromParts[common] == toParts[common] {
		common++
	}
	relativeParts := make([]string, 0, len(fromParts)-common+len(toParts)-common)
	for i := common; i < len(fromParts); i++ {
		relativeParts = append(relativeParts, "..")
	}
	relativeParts = append(relativeParts, toParts[common:]...)
	if len(relativeParts) == 0 {
		return ""
	}
	return strings.Join(relativeParts, "/")
}

func scriptPosixPathParse(value string) map[string]string {
	root := ""
	if strings.HasPrefix(value, "/") {
		root = "/"
	}
	base := scriptPosixPathBasename(value)
	ext := scriptPathExtname(value, "/")
	name := strings.TrimSuffix(base, ext)
	dir := scriptPosixPathDirname(value)
	if dir == "." {
		dir = ""
	}
	return map[string]string{
		"root": root,
		"dir":  dir,
		"base": base,
		"ext":  ext,
		"name": name,
	}
}

func scriptWin32PathCWD() string {
	cwd, err := os.Getwd()
	if err != nil || cwd == "" {
		return "\\"
	}
	return scriptWin32NormalizeSeparators(filepath.ToSlash(cwd))
}

func scriptWin32NormalizeSeparators(value string) string {
	return strings.ReplaceAll(value, "/", "\\")
}

func scriptWin32PathSplitRoot(value string) (root, rest string, absolute bool) {
	value = scriptWin32NormalizeSeparators(value)
	if len(value) >= 2 && value[0] == '\\' && value[1] == '\\' {
		trimmed := strings.TrimLeft(value, "\\")
		parts := strings.Split(trimmed, "\\")
		if len(parts) >= 2 && parts[0] != "" && parts[1] != "" {
			root = "\\\\" + parts[0] + "\\" + parts[1] + "\\"
			if len(parts) > 2 {
				rest = strings.Join(parts[2:], "\\")
			}
			return root, rest, true
		}
		return "\\", strings.TrimLeft(value, "\\"), true
	}
	if len(value) >= 2 && value[1] == ':' && ((value[0] >= 'A' && value[0] <= 'Z') || (value[0] >= 'a' && value[0] <= 'z')) {
		drive := value[:2]
		if len(value) >= 3 && value[2] == '\\' {
			return drive + "\\", strings.TrimLeft(value[3:], "\\"), true
		}
		return drive, value[2:], false
	}
	if strings.HasPrefix(value, "\\") {
		return "\\", strings.TrimLeft(value, "\\"), true
	}
	return "", value, false
}

func scriptWin32PathIsAbsolute(value string) bool {
	_, _, absolute := scriptWin32PathSplitRoot(value)
	return absolute
}

func scriptWin32PathNormalize(value string) string {
	if value == "" {
		return "."
	}
	value = scriptWin32NormalizeSeparators(value)
	trailing := strings.HasSuffix(value, "\\")
	root, rest, absolute := scriptWin32PathSplitRoot(value)
	parts := scriptPathCleanParts(strings.Split(rest, "\\"), !absolute)
	body := strings.Join(parts, "\\")
	result := ""
	if root != "" {
		result = root + body
	} else {
		result = body
	}
	if result == "" {
		if root != "" {
			if strings.HasSuffix(root, ":") {
				result = root + "."
			} else {
				result = root
			}
		} else {
			result = "."
		}
	}
	if trailing && result != "." && !strings.HasSuffix(result, "\\") {
		result += "\\"
	}
	return result
}

func scriptWin32PathJoin(parts ...string) string {
	filtered := make([]string, 0, len(parts))
	for _, part := range parts {
		if part != "" {
			filtered = append(filtered, part)
		}
	}
	if len(filtered) == 0 {
		return "."
	}
	return scriptWin32PathNormalize(strings.Join(filtered, "\\"))
}

func scriptWin32PathResolve(parts ...string) string {
	resolved := ""
	for _, part := range parts {
		if part == "" {
			continue
		}
		part = scriptWin32NormalizeSeparators(part)
		if scriptWin32PathIsAbsolute(part) {
			resolved = part
		} else if resolved == "" {
			resolved = part
		} else {
			resolved += "\\" + part
		}
	}
	if resolved == "" {
		resolved = scriptWin32PathCWD()
	} else if !scriptWin32PathIsAbsolute(resolved) {
		resolved = scriptWin32PathCWD() + "\\" + resolved
	}
	return scriptWin32PathNormalize(resolved)
}

func scriptWin32PathDirname(value string) string {
	if value == "" {
		return "."
	}
	value = scriptWin32TrimTrailingSeparators(scriptWin32NormalizeSeparators(value))
	root, rest, _ := scriptWin32PathSplitRoot(value)
	if rest == "" {
		if root != "" {
			return root
		}
		return "."
	}
	index := strings.LastIndex(rest, "\\")
	if index == -1 {
		if root != "" {
			return root
		}
		return "."
	}
	dirRest := rest[:index]
	if dirRest == "" {
		if root != "" {
			return root
		}
		return "."
	}
	return root + dirRest
}

func scriptWin32PathBasename(value string, ext ...string) string {
	value = scriptWin32NormalizeSeparators(value)
	_, rest, _ := scriptWin32PathSplitRoot(scriptWin32TrimTrailingSeparators(value))
	if rest == "" {
		return ""
	}
	base := scriptPathBasename(rest, "\\")
	if len(ext) > 0 && ext[0] != "" && strings.HasSuffix(base, ext[0]) {
		return strings.TrimSuffix(base, ext[0])
	}
	return base
}

func scriptWin32PathRelative(from, to string) string {
	from = scriptWin32PathResolve(from)
	to = scriptWin32PathResolve(to)
	if strings.EqualFold(from, to) {
		return ""
	}
	fromRoot, fromRest, _ := scriptWin32PathSplitRoot(from)
	toRoot, toRest, _ := scriptWin32PathSplitRoot(to)
	if !strings.EqualFold(fromRoot, toRoot) {
		return to
	}
	fromParts := scriptPathNonEmptyParts(strings.Split(fromRest, "\\"))
	toParts := scriptPathNonEmptyParts(strings.Split(toRest, "\\"))
	common := 0
	for common < len(fromParts) && common < len(toParts) && strings.EqualFold(fromParts[common], toParts[common]) {
		common++
	}
	relativeParts := make([]string, 0, len(fromParts)-common+len(toParts)-common)
	for i := common; i < len(fromParts); i++ {
		relativeParts = append(relativeParts, "..")
	}
	relativeParts = append(relativeParts, toParts[common:]...)
	if len(relativeParts) == 0 {
		return ""
	}
	return strings.Join(relativeParts, "\\")
}

func scriptWin32PathParse(value string) map[string]string {
	value = scriptWin32NormalizeSeparators(value)
	root, _, _ := scriptWin32PathSplitRoot(value)
	base := scriptWin32PathBasename(value)
	ext := scriptPathExtnameFromBase(base)
	name := strings.TrimSuffix(base, ext)
	dir := scriptWin32PathDirname(value)
	if dir == "." {
		dir = ""
	}
	return map[string]string{
		"root": root,
		"dir":  dir,
		"base": base,
		"ext":  ext,
		"name": name,
	}
}

func scriptPathCleanParts(parts []string, allowAboveRoot bool) []string {
	cleaned := []string{}
	for _, part := range parts {
		if part == "" || part == "." {
			continue
		}
		if part == ".." {
			if len(cleaned) > 0 && cleaned[len(cleaned)-1] != ".." {
				cleaned = cleaned[:len(cleaned)-1]
			} else if allowAboveRoot {
				cleaned = append(cleaned, part)
			}
			continue
		}
		cleaned = append(cleaned, part)
	}
	return cleaned
}

func scriptPathNonEmptyParts(parts []string) []string {
	filtered := make([]string, 0, len(parts))
	for _, part := range parts {
		if part != "" {
			filtered = append(filtered, part)
		}
	}
	return filtered
}

func scriptWin32TrimTrailingSeparators(value string) string {
	for len(value) > 1 && strings.HasSuffix(value, "\\") {
		root, rest, _ := scriptWin32PathSplitRoot(value)
		if rest == "" && root != "" {
			break
		}
		value = strings.TrimSuffix(value, "\\")
	}
	return value
}

func scriptPathBasename(value, separator string) string {
	if value == "" {
		return ""
	}
	value = strings.TrimRight(value, separator)
	if value == "" {
		return ""
	}
	if index := strings.LastIndex(value, separator); index != -1 {
		return value[index+len(separator):]
	}
	return value
}

func scriptPathExtname(value, separator string) string {
	return scriptPathExtnameFromBase(scriptPathBasename(value, separator))
}

func scriptPathExtnameFromBase(base string) string {
	if base == "" || base == "." || base == ".." {
		return ""
	}
	index := strings.LastIndex(base, ".")
	if index <= 0 {
		return ""
	}
	return base[index:]
}

func scriptPathFormat(runtime *goja.Runtime, value goja.Value, separator string, win32 bool) string {
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		return ""
	}
	object := value.ToObject(runtime)
	dir := scriptPathObjectString(object, "dir")
	root := scriptPathObjectString(object, "root")
	base := scriptPathObjectString(object, "base")
	if base == "" {
		base = scriptPathObjectString(object, "name") + scriptPathObjectString(object, "ext")
	}
	prefix := dir
	if prefix == "" {
		prefix = root
	}
	if prefix == "" {
		return base
	}
	if win32 {
		prefix = scriptWin32NormalizeSeparators(prefix)
		if strings.HasSuffix(prefix, "\\") {
			return prefix + base
		}
		return prefix + "\\" + base
	}
	if strings.HasSuffix(prefix, "/") {
		return prefix + base
	}
	return prefix + "/" + base
}

func scriptPathObjectString(object *goja.Object, key string) string {
	value := object.Get(key)
	if goja.IsUndefined(value) || goja.IsNull(value) {
		return ""
	}
	return value.String()
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

func newScriptOSObject(runtime *goja.Runtime) *goja.Object {
	osObject := runtime.NewObject()
	_ = osObject.Set("EOL", scriptOSEOL())
	_ = osObject.Set("devNull", scriptOSDevNull())
	_ = osObject.Set("constants", map[string]interface{}{
		"signals": map[string]int{
			"SIGHUP": 1, "SIGINT": 2, "SIGQUIT": 3, "SIGILL": 4, "SIGTRAP": 5,
			"SIGABRT": 6, "SIGBUS": 7, "SIGFPE": 8, "SIGKILL": 9, "SIGUSR1": 10,
			"SIGSEGV": 11, "SIGUSR2": 12, "SIGPIPE": 13, "SIGALRM": 14, "SIGTERM": 15,
		},
		"errno": map[string]int{
			"EACCES": 13, "EADDRINUSE": 48, "ECONNREFUSED": 61, "ECONNRESET": 54,
			"EEXIST": 17, "EINVAL": 22, "ENOENT": 2, "ENOTDIR": 20, "EPERM": 1,
			"ETIMEDOUT": 60,
		},
		"priority": map[string]int{
			"PRIORITY_LOW": 19, "PRIORITY_BELOW_NORMAL": 10, "PRIORITY_NORMAL": 0,
			"PRIORITY_ABOVE_NORMAL": -7, "PRIORITY_HIGH": -14, "PRIORITY_HIGHEST": -20,
		},
	})
	_ = osObject.Set("arch", func() string { return scriptNodeArch() })
	_ = osObject.Set("availableParallelism", func() int {
		if count := goruntime.NumCPU(); count > 0 {
			return count
		}
		return 1
	})
	_ = osObject.Set("cpus", func() []map[string]interface{} {
		count := goruntime.NumCPU()
		if count < 1 {
			count = 1
		}
		cpus := make([]map[string]interface{}, count)
		for index := range cpus {
			cpus[index] = map[string]interface{}{
				"model": scriptOSCPUModel(),
				"speed": 0,
				"times": map[string]int64{"user": 0, "nice": 0, "sys": 0, "idle": 0, "irq": 0},
			}
		}
		return cpus
	})
	_ = osObject.Set("endianness", func() string { return "LE" })
	_ = osObject.Set("freemem", func() float64 {
		var stats goruntime.MemStats
		goruntime.ReadMemStats(&stats)
		free := scriptOSTotalMem() - float64(stats.Alloc)
		if free < 1 {
			return 1
		}
		return free
	})
	_ = osObject.Set("getPriority", func(...int) int { return 0 })
	_ = osObject.Set("homedir", scriptOSHomeDir)
	_ = osObject.Set("hostname", scriptOSHostname)
	_ = osObject.Set("loadavg", func() []float64 { return []float64{0, 0, 0} })
	_ = osObject.Set("machine", func() string { return goruntime.GOARCH })
	_ = osObject.Set("networkInterfaces", scriptOSNetworkInterfaces)
	_ = osObject.Set("platform", func() string { return scriptNodePlatform() })
	_ = osObject.Set("release", scriptOSRelease)
	_ = osObject.Set("setPriority", func(...int) goja.Value { return goja.Undefined() })
	_ = osObject.Set("tmpdir", func() string { return os.TempDir() })
	_ = osObject.Set("totalmem", scriptOSTotalMem)
	_ = osObject.Set("type", scriptOSType)
	_ = osObject.Set("uptime", func() float64 {
		elapsed := time.Since(scriptOSStartTime).Seconds()
		if elapsed < 1 {
			return 1
		}
		return elapsed
	})
	_ = osObject.Set("userInfo", scriptOSUserInfo)
	_ = osObject.Set("version", scriptOSVersion)
	return osObject
}

func scriptOSEOL() string {
	if goruntime.GOOS == "windows" {
		return "\r\n"
	}
	return "\n"
}

func scriptOSDevNull() string {
	if goruntime.GOOS == "windows" {
		return "\\\\.\\nul"
	}
	return "/dev/null"
}

func scriptOSCPUModel() string {
	switch goruntime.GOARCH {
	case "arm64":
		return "arm64 CPU"
	case "amd64":
		return "x64 CPU"
	default:
		return goruntime.GOARCH + " CPU"
	}
}

func scriptOSType() string {
	switch goruntime.GOOS {
	case "darwin":
		return "Darwin"
	case "linux":
		return "Linux"
	case "windows":
		return "Windows_NT"
	default:
		return goruntime.GOOS
	}
}

func scriptOSRelease() string {
	if output, err := exec.Command("uname", "-r").Output(); err == nil {
		if release := strings.TrimSpace(string(output)); release != "" {
			return release
		}
	}
	return goruntime.GOOS
}

func scriptOSVersion() string {
	if output, err := exec.Command("uname", "-v").Output(); err == nil {
		if version := strings.TrimSpace(string(output)); version != "" {
			return version
		}
	}
	return scriptOSType() + " " + scriptOSRelease()
}

func scriptOSHomeDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return home
}

func scriptOSHostname() string {
	hostname, err := os.Hostname()
	if err != nil {
		return ""
	}
	return hostname
}

func scriptOSTotalMem() float64 {
	var stats goruntime.MemStats
	goruntime.ReadMemStats(&stats)
	total := float64(goruntime.NumCPU()) * 1024 * 1024 * 1024
	if total < float64(stats.Sys) {
		total = float64(stats.Sys)
	}
	if total < 1 {
		return 1
	}
	return total
}

func scriptOSNetworkInterfaces() map[string][]map[string]interface{} {
	result := map[string][]map[string]interface{}{}
	interfaces, err := net.Interfaces()
	if err != nil {
		return result
	}
	for _, iface := range interfaces {
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			value, ok := scriptOSNetworkAddress(iface, addr)
			if ok {
				result[iface.Name] = append(result[iface.Name], value)
			}
		}
	}
	return result
}

func scriptOSNetworkAddress(iface net.Interface, addr net.Addr) (map[string]interface{}, bool) {
	var ip net.IP
	netmask := ""
	cidr := addr.String()
	switch value := addr.(type) {
	case *net.IPNet:
		ip = value.IP
		netmask = net.IP(value.Mask).String()
	case *net.IPAddr:
		ip = value.IP
	default:
		return nil, false
	}
	family := "IPv6"
	if ipv4 := ip.To4(); ipv4 != nil {
		ip = ipv4
		family = "IPv4"
	}
	if ip == nil {
		return nil, false
	}
	return map[string]interface{}{
		"address":  ip.String(),
		"netmask":  netmask,
		"family":   family,
		"mac":      iface.HardwareAddr.String(),
		"internal": iface.Flags&net.FlagLoopback != 0,
		"cidr":     cidr,
	}, true
}

func scriptOSUserInfo() map[string]interface{} {
	username := firstNonEmptyEnv("USER", "USERNAME", "LOGNAME")
	home := scriptOSHomeDir()
	if username == "" && home != "" {
		username = filepath.Base(home)
	}
	return map[string]interface{}{
		"username": username,
		"homedir":  home,
		"shell":    os.Getenv("SHELL"),
	}
}

func firstNonEmptyEnv(names ...string) string {
	for _, name := range names {
		if value := os.Getenv(name); value != "" {
			return value
		}
	}
	return ""
}

func newScriptZlibObject(runtime *goja.Runtime) goja.Value {
	_ = runtime.Set("__liteApiZlibCompress", func(kind, inputBase64 string, level int) (string, error) {
		return scriptZlibCompress(kind, inputBase64, level)
	})
	_ = runtime.Set("__liteApiZlibDecompress", func(kind, inputBase64 string) (string, error) {
		return scriptZlibDecompress(kind, inputBase64)
	})
	script := `(function () {
  const compressBridge = globalThis.__liteApiZlibCompress;
  const decompressBridge = globalThis.__liteApiZlibDecompress;

  const constants = {
    Z_NO_COMPRESSION: 0,
    Z_BEST_SPEED: 1,
    Z_BEST_COMPRESSION: 9,
    Z_DEFAULT_COMPRESSION: -1,
    Z_FILTERED: 1,
    Z_HUFFMAN_ONLY: 2,
    Z_RLE: 3,
    Z_FIXED: 4,
    Z_DEFAULT_STRATEGY: 0,
    Z_DEFLATED: 8,
    Z_OK: 0,
    Z_STREAM_END: 1,
    Z_NEED_DICT: 2,
    Z_ERRNO: -1,
    Z_STREAM_ERROR: -2,
    Z_DATA_ERROR: -3,
    Z_MEM_ERROR: -4,
    Z_BUF_ERROR: -5,
    Z_VERSION_ERROR: -6,
    BROTLI_OPERATION_PROCESS: 0,
    BROTLI_OPERATION_FLUSH: 1,
    BROTLI_OPERATION_FINISH: 2,
    BROTLI_PARAM_MODE: 0,
    BROTLI_PARAM_QUALITY: 1,
    BROTLI_PARAM_LGWIN: 2,
    BROTLI_MIN_QUALITY: 0,
    BROTLI_MAX_QUALITY: 11,
    BROTLI_DEFAULT_QUALITY: 6
  };

  function normalizeLevel(options, brotli) {
    let value;
    if (typeof options === "number") {
      value = options;
    } else if (options && typeof options === "object") {
      if (options.level !== undefined) {
        value = options.level;
      } else if (brotli && options.quality !== undefined) {
        value = options.quality;
      } else if (brotli && options.params && options.params[constants.BROTLI_PARAM_QUALITY] !== undefined) {
        value = options.params[constants.BROTLI_PARAM_QUALITY];
      }
    }
    const number = Number(value);
    return Number.isFinite(number) ? Math.trunc(number) : -1;
  }

  function inputBase64(input) {
    return Buffer.from(input).toString("base64");
  }

  function outputBuffer(base64) {
    return Buffer.from(String(base64 || ""), "base64");
  }

  function compressSync(kind, input, options, brotli) {
    return outputBuffer(compressBridge(kind, inputBase64(input), normalizeLevel(options, brotli)));
  }

  function decompressSync(kind, input) {
    return outputBuffer(decompressBridge(kind, inputBase64(input)));
  }

  function withCallback(operation) {
    return function (input, options, callback) {
      if (typeof options === "function") {
        callback = options;
        options = undefined;
      }
      try {
        const result = operation(input, options);
        if (typeof callback === "function") {
          callback(null, result);
          return undefined;
        }
        return result;
      } catch (err) {
        if (typeof callback === "function") {
          callback(err);
          return undefined;
        }
        throw err;
      }
    };
  }

  const gzipSync = function (input, options) { return compressSync("gzip", input, options, false); };
  const gunzipSync = function (input) { return decompressSync("gunzip", input); };
  const deflateSync = function (input, options) { return compressSync("deflate", input, options, false); };
  const inflateSync = function (input) { return decompressSync("inflate", input); };
  const deflateRawSync = function (input, options) { return compressSync("deflateRaw", input, options, false); };
  const inflateRawSync = function (input) { return decompressSync("inflateRaw", input); };
  const brotliCompressSync = function (input, options) { return compressSync("brotli", input, options, true); };
  const brotliDecompressSync = function (input) { return decompressSync("brotli", input); };
  const unzipSync = function (input) { return decompressSync("unzip", input); };

  const module = {
    constants,
    gzipSync,
    gunzipSync,
    deflateSync,
    inflateSync,
    deflateRawSync,
    inflateRawSync,
    brotliCompressSync,
    brotliDecompressSync,
    unzipSync,
    gzip: withCallback(gzipSync),
    gunzip: withCallback(gunzipSync),
    deflate: withCallback(deflateSync),
    inflate: withCallback(inflateSync),
    deflateRaw: withCallback(deflateRawSync),
    inflateRaw: withCallback(inflateRawSync),
    brotliCompress: withCallback(brotliCompressSync),
    brotliDecompress: withCallback(brotliDecompressSync),
    unzip: withCallback(unzipSync)
  };
  for (const key of Object.keys(constants)) {
    module[key] = constants[key];
  }
  return module;
})()`
	value, err := runtime.RunProgram(scriptZlibShim.compiled(script))
	if err != nil {
		panic(runtime.NewGoError(err))
	}
	_ = runtime.Set("__liteApiZlibCompress", goja.Undefined())
	_ = runtime.Set("__liteApiZlibDecompress", goja.Undefined())
	return value
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

func scriptZlibCompress(kind, inputBase64 string, level int) (string, error) {
	data, err := base64.StdEncoding.DecodeString(inputBase64)
	if err != nil {
		return "", err
	}
	level = scriptZlibFlateLevel(level)
	var out bytes.Buffer
	var writer io.WriteCloser
	switch kind {
	case "gzip":
		writer, err = gzip.NewWriterLevel(&out, level)
	case "deflate":
		writer, err = zlib.NewWriterLevel(&out, level)
	case "deflateRaw":
		writer, err = flate.NewWriter(&out, level)
	case "brotli":
		writer = brotli.NewWriterLevel(&out, scriptZlibBrotliLevel(level))
	default:
		return "", fmt.Errorf("unsupported zlib compression kind %q", kind)
	}
	if err != nil {
		return "", err
	}
	if _, err := writer.Write(data); err != nil {
		_ = writer.Close()
		return "", err
	}
	if err := writer.Close(); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(out.Bytes()), nil
}

func scriptZlibDecompress(kind, inputBase64 string) (string, error) {
	data, err := base64.StdEncoding.DecodeString(inputBase64)
	if err != nil {
		return "", err
	}
	out, err := scriptZlibDecompressBytes(kind, data)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(out), nil
}

func scriptZlibDecompressBytes(kind string, data []byte) ([]byte, error) {
	reader := bytes.NewReader(data)
	var closer io.ReadCloser
	var stream io.Reader
	var err error
	switch kind {
	case "gunzip":
		closer, err = gzip.NewReader(reader)
		stream = closer
	case "inflate":
		closer, err = zlib.NewReader(reader)
		stream = closer
	case "inflateRaw":
		closer = flate.NewReader(reader)
		stream = closer
	case "brotli":
		stream = brotli.NewReader(reader)
	case "unzip":
		if out, unzipErr := scriptZlibDecompressBytes("gunzip", data); unzipErr == nil {
			return out, nil
		}
		return scriptZlibDecompressBytes("inflate", data)
	default:
		return nil, fmt.Errorf("unsupported zlib decompression kind %q", kind)
	}
	if err != nil {
		return nil, err
	}
	out, err := io.ReadAll(stream)
	if closer != nil {
		if closeErr := closer.Close(); err == nil {
			err = closeErr
		}
	}
	if err != nil {
		return nil, err
	}
	return out, nil
}

func scriptZlibFlateLevel(level int) int {
	if level == flate.HuffmanOnly || level == flate.DefaultCompression || (level >= flate.NoCompression && level <= flate.BestCompression) {
		return level
	}
	return flate.DefaultCompression
}

func scriptZlibBrotliLevel(level int) int {
	if level == flate.DefaultCompression {
		return brotli.DefaultCompression
	}
	if level < brotli.BestSpeed {
		return brotli.BestSpeed
	}
	if level > brotli.BestCompression {
		return brotli.BestCompression
	}
	return level
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

func newScriptURLObject(runtime *goja.Runtime) goja.Value {
	parseFunc := func(call goja.FunctionCall) goja.Value {
		parseQuery := false
		if len(call.Arguments) > 1 {
			parseQuery = call.Argument(1).ToBoolean()
		}
		return scriptURLParseValue(runtime, call.Argument(0).String(), parseQuery)
	}
	formatFunc := func(call goja.FunctionCall) goja.Value {
		return runtime.ToValue(scriptURLFormatValue(runtime, call.Argument(0)))
	}
	resolveFunc := func(call goja.FunctionCall) goja.Value {
		return runtime.ToValue(scriptURLResolve(call.Argument(0).String(), call.Argument(1).String()))
	}
	resolveObjectFunc := func(call goja.FunctionCall) goja.Value {
		resolved := scriptURLResolve(call.Argument(0).String(), call.Argument(1).String())
		return scriptURLParseValue(runtime, resolved, false)
	}
	_ = runtime.Set("__liteApiURLParse", parseFunc)
	_ = runtime.Set("__liteApiURLFormat", formatFunc)
	_ = runtime.Set("__liteApiURLResolve", resolveFunc)
	_ = runtime.Set("__liteApiURLResolveObject", resolveObjectFunc)
	script := `(function () {
  const parse = globalThis.__liteApiURLParse;
  const format = globalThis.__liteApiURLFormat;
  const resolve = globalThis.__liteApiURLResolve;
  const resolveObject = globalThis.__liteApiURLResolveObject;

  function decodeParam(value) {
    try {
      return decodeURIComponent(String(value).replace(/\+/g, " "));
    } catch (_) {
      return String(value);
    }
  }

  function encodeParam(value) {
    return encodeURIComponent(String(value)).replace(/%20/g, "+");
  }

  class URLSearchParams {
    constructor(init) {
      this._pairs = [];
      if (init === undefined || init === null) {
        return;
      }
      if (init instanceof URLSearchParams) {
        for (const pair of init._pairs) {
          this.append(pair[0], pair[1]);
        }
        return;
      }
      if (typeof init === "string") {
        const raw = init.charAt(0) === "?" ? init.slice(1) : init;
        if (raw.length === 0) {
          return;
        }
        for (const part of raw.split("&")) {
          if (part === "") {
            continue;
          }
          const index = part.indexOf("=");
          const key = index === -1 ? part : part.slice(0, index);
          const value = index === -1 ? "" : part.slice(index + 1);
          this.append(decodeParam(key), decodeParam(value));
        }
        return;
      }
      if (Array.isArray(init)) {
        for (const pair of init) {
          if (!pair || pair.length < 2) {
            throw new TypeError("Each query pair must be an iterable [name, value] tuple");
          }
          this.append(pair[0], pair[1]);
        }
        return;
      }
      if (typeof init === "object") {
        for (const key of Object.keys(init)) {
          this.append(key, init[key]);
        }
      }
    }

    append(name, value) {
      this._pairs.push([String(name), String(value)]);
    }

    delete(name) {
      const key = String(name);
      this._pairs = this._pairs.filter((pair) => pair[0] !== key);
    }

    get(name) {
      const key = String(name);
      const pair = this._pairs.find((item) => item[0] === key);
      return pair ? pair[1] : null;
    }

    getAll(name) {
      const key = String(name);
      return this._pairs.filter((pair) => pair[0] === key).map((pair) => pair[1]);
    }

    has(name) {
      const key = String(name);
      return this._pairs.some((pair) => pair[0] === key);
    }

    set(name, value) {
      const key = String(name);
      this.delete(key);
      this.append(key, value);
    }

    sort() {
      this._pairs.sort((left, right) => left[0] < right[0] ? -1 : left[0] > right[0] ? 1 : 0);
    }

    forEach(callback, thisArg) {
      for (const pair of this._pairs) {
        callback.call(thisArg, pair[1], pair[0], this);
      }
    }

    entries() {
      return this._pairs.map((pair) => [pair[0], pair[1]])[Symbol.iterator]();
    }

    keys() {
      return this._pairs.map((pair) => pair[0])[Symbol.iterator]();
    }

    values() {
      return this._pairs.map((pair) => pair[1])[Symbol.iterator]();
    }

    toString() {
      return this._pairs.map((pair) => encodeParam(pair[0]) + "=" + encodeParam(pair[1])).join("&");
    }

    [Symbol.iterator]() {
      return this.entries();
    }
  }

  class URL {
    constructor(input, base) {
      const raw = base === undefined ? String(input) : resolve(String(base), String(input));
      this._parts = parse(raw, false);
      this.searchParams = new URLSearchParams(this._parts.search || "");
    }

    _formatParts() {
      const parts = {};
      for (const key of Object.keys(this._parts)) {
        parts[key] = this._parts[key];
      }
      const query = this.searchParams.toString();
      parts.search = query ? "?" + query : "";
      parts.query = query;
      parts.path = (parts.pathname || "") + (parts.search || "");
      return parts;
    }

    get href() {
      return format(this._formatParts());
    }

    set href(value) {
      this._parts = parse(String(value), false);
      this.searchParams = new URLSearchParams(this._parts.search || "");
    }

    get protocol() { return this._parts.protocol || ""; }
    set protocol(value) {
      let protocol = String(value || "");
      if (protocol && !protocol.endsWith(":")) {
        protocol += ":";
      }
      this._parts.protocol = protocol;
    }

    get username() {
      const auth = this._parts.auth || "";
      const index = auth.indexOf(":");
      return index === -1 ? auth : auth.slice(0, index);
    }
    set username(value) {
      this._parts.auth = String(value || "") + (this.password ? ":" + this.password : "");
    }

    get password() {
      const auth = this._parts.auth || "";
      const index = auth.indexOf(":");
      return index === -1 ? "" : auth.slice(index + 1);
    }
    set password(value) {
      this._parts.auth = this.username + (value ? ":" + String(value) : "");
    }

    get host() { return this._parts.host || ""; }
    set host(value) {
      this._parts.host = String(value || "");
      const parts = this._parts.host.split(":");
      this._parts.hostname = parts[0] || "";
      this._parts.port = parts.length > 1 ? parts.slice(1).join(":") : "";
    }

    get hostname() { return this._parts.hostname || ""; }
    set hostname(value) {
      this._parts.hostname = String(value || "");
      this._parts.host = this._parts.hostname + (this._parts.port ? ":" + this._parts.port : "");
    }

    get port() { return this._parts.port || ""; }
    set port(value) {
      this._parts.port = String(value || "");
      this._parts.host = (this._parts.hostname || "") + (this._parts.port ? ":" + this._parts.port : "");
    }

    get pathname() { return this._parts.pathname || ""; }
    set pathname(value) { this._parts.pathname = String(value || ""); }

    get search() {
      const query = this.searchParams.toString();
      return query ? "?" + query : "";
    }
    set search(value) {
      this.searchParams = new URLSearchParams(String(value || ""));
    }

    get hash() { return this._parts.hash || ""; }
    set hash(value) {
      const text = String(value || "");
      this._parts.hash = text && text.charAt(0) !== "#" ? "#" + text : text;
    }

    get origin() {
      if (!this.protocol || !this.host) {
        return "null";
      }
      return this.protocol + "//" + this.host;
    }

    toString() { return this.href; }
    toJSON() { return this.href; }
  }

  const module = {
    parse,
    format,
    resolve,
    resolveObject,
    URL,
    URLSearchParams,
    domainToASCII: function (value) { return String(value); },
    domainToUnicode: function (value) { return String(value); }
  };
  globalThis.URL = URL;
  globalThis.URLSearchParams = URLSearchParams;
  return module;
})()`
	value, err := runtime.RunProgram(scriptURLShim.compiled(script))
	if err != nil {
		panic(runtime.NewGoError(err))
	}
	_ = runtime.Set("__liteApiURLParse", goja.Undefined())
	_ = runtime.Set("__liteApiURLFormat", goja.Undefined())
	_ = runtime.Set("__liteApiURLResolve", goja.Undefined())
	_ = runtime.Set("__liteApiURLResolveObject", goja.Undefined())
	return value
}

func scriptURLParseValue(runtime *goja.Runtime, raw string, parseQuery bool) goja.Value {
	parsed, err := url.Parse(raw)
	if err != nil {
		panic(runtime.NewGoError(err))
	}
	object := runtime.NewObject()
	setNullableString := func(name, value string) {
		if value == "" {
			_ = object.Set(name, goja.Null())
			return
		}
		_ = object.Set(name, value)
	}
	protocol := ""
	if parsed.Scheme != "" {
		protocol = parsed.Scheme + ":"
	}
	setNullableString("protocol", protocol)
	if protocol != "" && strings.HasPrefix(strings.TrimPrefix(raw, protocol), "//") {
		_ = object.Set("slashes", true)
	} else {
		_ = object.Set("slashes", goja.Null())
	}
	auth := ""
	if parsed.User != nil {
		auth = parsed.User.String()
	}
	setNullableString("auth", auth)
	setNullableString("host", parsed.Host)
	setNullableString("port", parsed.Port())
	setNullableString("hostname", parsed.Hostname())
	hash := ""
	if parsed.Fragment != "" {
		hash = "#" + parsed.Fragment
	}
	setNullableString("hash", hash)
	search := ""
	if parsed.RawQuery != "" {
		search = "?" + parsed.RawQuery
	}
	setNullableString("search", search)
	if parseQuery {
		queryObject := runtime.NewObject()
		if parsed.RawQuery != "" {
			for key, values := range parsed.Query() {
				if len(values) == 1 {
					_ = queryObject.Set(key, values[0])
				} else {
					_ = queryObject.Set(key, values)
				}
			}
		}
		_ = object.Set("query", queryObject)
	} else {
		setNullableString("query", parsed.RawQuery)
	}
	pathname := parsed.EscapedPath()
	if pathname == "" && parsed.Host != "" {
		pathname = "/"
	}
	setNullableString("pathname", pathname)
	pathValue := pathname
	if search != "" {
		pathValue += search
	}
	setNullableString("path", pathValue)
	href := parsed.String()
	if parsed.Host != "" && parsed.Path == "" {
		prefix := protocol + "//" + parsed.Host
		if parsed.User != nil {
			prefix = protocol + "//" + parsed.User.String() + "@" + parsed.Host
		}
		href = prefix + "/"
		if parsed.RawQuery != "" {
			href += "?" + parsed.RawQuery
		}
		if parsed.Fragment != "" {
			href += "#" + parsed.Fragment
		}
	}
	_ = object.Set("href", href)
	return object
}

func scriptURLFormatValue(runtime *goja.Runtime, value goja.Value) string {
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		return ""
	}
	if exported, ok := value.Export().(string); ok {
		return exported
	}
	object := value.ToObject(runtime)
	protocol := scriptURLObjectString(object, "protocol")
	if protocol != "" && !strings.HasSuffix(protocol, ":") {
		protocol += ":"
	}
	auth := scriptURLObjectString(object, "auth")
	host := scriptURLObjectString(object, "host")
	hostname := scriptURLObjectString(object, "hostname")
	port := scriptURLObjectString(object, "port")
	if host == "" && hostname != "" {
		host = hostname
		if port != "" {
			host += ":" + port
		}
	}
	pathname := scriptURLObjectString(object, "pathname")
	search := scriptURLObjectString(object, "search")
	if search == "" {
		search = scriptURLQueryString(runtime, object.Get("query"))
		if search != "" {
			search = "?" + search
		}
	} else if !strings.HasPrefix(search, "?") {
		search = "?" + search
	}
	hash := scriptURLObjectString(object, "hash")
	if hash != "" && !strings.HasPrefix(hash, "#") {
		hash = "#" + hash
	}
	var builder strings.Builder
	if protocol != "" {
		builder.WriteString(protocol)
	}
	if host != "" {
		builder.WriteString("//")
		if auth != "" {
			builder.WriteString(auth)
			builder.WriteString("@")
		}
		builder.WriteString(host)
	}
	if pathname != "" {
		if host != "" && !strings.HasPrefix(pathname, "/") {
			builder.WriteString("/")
		}
		builder.WriteString(pathname)
	} else if host != "" && (search != "" || hash != "") {
		builder.WriteString("/")
	}
	builder.WriteString(search)
	builder.WriteString(hash)
	return builder.String()
}

func scriptURLObjectString(object *goja.Object, name string) string {
	value := object.Get(name)
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		return ""
	}
	if exported := value.Export(); exported != nil {
		return fmt.Sprint(exported)
	}
	return value.String()
}

func scriptURLQueryString(runtime *goja.Runtime, value goja.Value) string {
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		return ""
	}
	if exported, ok := value.Export().(string); ok {
		return exported
	}
	object := value.ToObject(runtime)
	values := url.Values{}
	for _, key := range object.Keys() {
		item := object.Get(key)
		if item == nil || goja.IsUndefined(item) || goja.IsNull(item) {
			values.Add(key, "")
			continue
		}
		if exported, ok := item.Export().([]interface{}); ok {
			for _, part := range exported {
				values.Add(key, fmt.Sprint(part))
			}
			continue
		}
		values.Add(key, fmt.Sprint(item.Export()))
	}
	return values.Encode()
}

func scriptURLResolve(baseRaw, refRaw string) string {
	base, err := url.Parse(baseRaw)
	if err != nil {
		return refRaw
	}
	ref, err := url.Parse(refRaw)
	if err != nil {
		return refRaw
	}
	return base.ResolveReference(ref).String()
}

func newScriptCryptoObject(runtime *goja.Runtime) *goja.Object {
	cryptoObject := runtime.NewObject()
	_ = cryptoObject.Set("createHash", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			panic(runtime.NewTypeError("algorithm is required"))
		}
		hasher, err := newScriptCryptoHash(call.Argument(0).String())
		if err != nil {
			panic(runtime.NewGoError(err))
		}
		return newScriptCryptoDigestObject(runtime, hasher)
	})
	_ = cryptoObject.Set("createHmac", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 2 {
			panic(runtime.NewTypeError("algorithm and key are required"))
		}
		factory, err := scriptCryptoHashFactory(call.Argument(0).String())
		if err != nil {
			panic(runtime.NewGoError(err))
		}
		key, err := scriptCryptoValueBytes(runtime, call.Argument(1), "")
		if err != nil {
			panic(runtime.NewGoError(err))
		}
		return newScriptCryptoDigestObject(runtime, hmac.New(factory, key))
	})
	_ = cryptoObject.Set("getHashes", func() []string {
		return []string{"md5", "sha1", "sha224", "sha256", "sha384", "sha512"}
	})
	_ = cryptoObject.Set("getCiphers", func() []string {
		return []string{"aes-128-cbc", "aes-192-cbc", "aes-256-cbc"}
	})
	_ = cryptoObject.Set("pbkdf2Sync", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 5 {
			panic(runtime.NewTypeError("password, salt, iterations, key length, and digest are required"))
		}
		password, err := scriptCryptoValueBytes(runtime, call.Argument(0), "")
		if err != nil {
			panic(runtime.NewGoError(err))
		}
		salt, err := scriptCryptoValueBytes(runtime, call.Argument(1), "")
		if err != nil {
			panic(runtime.NewGoError(err))
		}
		iterations := int(call.Argument(2).ToInteger())
		keyLength := int(call.Argument(3).ToInteger())
		if iterations <= 0 || keyLength < 0 {
			panic(runtime.NewTypeError("iterations must be positive and key length must be non-negative"))
		}
		factory, err := scriptCryptoHashFactory(call.Argument(4).String())
		if err != nil {
			panic(runtime.NewGoError(err))
		}
		return scriptBufferValue(runtime, pbkdf2.Key(password, salt, iterations, keyLength, factory))
	})
	_ = cryptoObject.Set("scryptSync", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 3 {
			panic(runtime.NewTypeError("password, salt, and key length are required"))
		}
		password, err := scriptCryptoValueBytes(runtime, call.Argument(0), "")
		if err != nil {
			panic(runtime.NewGoError(err))
		}
		salt, err := scriptCryptoValueBytes(runtime, call.Argument(1), "")
		if err != nil {
			panic(runtime.NewGoError(err))
		}
		keyLength := int(call.Argument(2).ToInteger())
		if keyLength < 0 {
			panic(runtime.NewTypeError("key length must be non-negative"))
		}
		key, err := scrypt.Key(password, salt, 1<<14, 8, 1, keyLength)
		if err != nil {
			panic(runtime.NewGoError(err))
		}
		return scriptBufferValue(runtime, key)
	})
	_ = cryptoObject.Set("timingSafeEqual", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 2 {
			panic(runtime.NewTypeError("two buffers are required"))
		}
		left, err := scriptCryptoValueBytes(runtime, call.Argument(0), "")
		if err != nil {
			panic(runtime.NewGoError(err))
		}
		right, err := scriptCryptoValueBytes(runtime, call.Argument(1), "")
		if err != nil {
			panic(runtime.NewGoError(err))
		}
		if len(left) != len(right) {
			panic(runtime.NewTypeError("Input buffers must have the same byte length"))
		}
		return runtime.ToValue(hmac.Equal(left, right))
	})
	_ = cryptoObject.Set("createCipheriv", func(call goja.FunctionCall) goja.Value {
		return newScriptAESCBCObject(runtime, call, true)
	})
	_ = cryptoObject.Set("createDecipheriv", func(call goja.FunctionCall) goja.Value {
		return newScriptAESCBCObject(runtime, call, false)
	})
	_ = cryptoObject.Set("randomBytes", func(call goja.FunctionCall) goja.Value {
		size := 0
		if len(call.Arguments) > 0 {
			size = int(call.Argument(0).ToInteger())
		}
		if size < 0 {
			panic(runtime.NewTypeError("size must be non-negative"))
		}
		bytes := make([]byte, size)
		if _, err := rand.Read(bytes); err != nil {
			panic(runtime.NewGoError(err))
		}
		return scriptBufferValue(runtime, bytes)
	})
	_ = cryptoObject.Set("randomUUID", func() string { return scriptRandomUUID() })
	_ = cryptoObject.Set("getRandomValues", func(call goja.FunctionCall) goja.Value {
		target := call.Argument(0)
		if target == nil || goja.IsUndefined(target) || goja.IsNull(target) {
			panic(runtime.NewTypeError("expected typed array"))
		}
		targetObject := target.ToObject(runtime)
		length := int(targetObject.Get("length").ToInteger())
		if length < 0 {
			panic(runtime.NewTypeError("length must be non-negative"))
		}
		bytes := make([]byte, length)
		if _, err := rand.Read(bytes); err != nil {
			panic(runtime.NewGoError(err))
		}
		for index, value := range bytes {
			_ = targetObject.Set(strconv.Itoa(index), int(value))
		}
		return target
	})
	_ = cryptoObject.Set("subtle", newScriptSubtleCryptoObject(runtime))
	return cryptoObject
}

func newScriptSubtleCryptoObject(runtime *goja.Runtime) *goja.Object {
	subtle := runtime.NewObject()
	_ = subtle.Set("digest", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 2 {
			return scriptRejectedPromise(runtime, runtime.NewTypeError("algorithm and data are required"))
		}
		hasher, err := newScriptCryptoHash(scriptWebCryptoAlgorithmName(runtime, call.Argument(0)))
		if err != nil {
			return scriptRejectedPromise(runtime, runtime.NewGoError(err))
		}
		data, err := scriptCryptoValueBytes(runtime, call.Argument(1), "")
		if err != nil {
			return scriptRejectedPromise(runtime, runtime.NewGoError(err))
		}
		_, _ = hasher.Write(data)
		buffer := runtime.NewArrayBuffer(hasher.Sum(nil))
		return scriptResolvedPromise(runtime, runtime.ToValue(buffer))
	})
	_ = subtle.Set("generateKey", func(call goja.FunctionCall) goja.Value {
		algorithm := scriptWebCryptoAlgorithmValue(runtime, call.Argument(0))
		_ = algorithm.Set("length", scriptWebCryptoAlgorithmLength(runtime, call.Argument(0)))
		key := runtime.NewObject()
		_ = key.Set("type", "secret")
		_ = key.Set("algorithm", algorithm)
		_ = key.Set("extractable", len(call.Arguments) > 1 && call.Argument(1).ToBoolean())
		if len(call.Arguments) > 2 {
			_ = key.Set("usages", call.Argument(2))
		} else {
			_ = key.Set("usages", []string{})
		}
		return scriptResolvedPromise(runtime, key)
	})
	_ = subtle.Set("importKey", func(call goja.FunctionCall) goja.Value {
		key := runtime.NewObject()
		_ = key.Set("type", "secret")
		_ = key.Set("format", call.Argument(0).String())
		_ = key.Set("algorithm", scriptWebCryptoAlgorithmValue(runtime, call.Argument(2)))
		_ = key.Set("extractable", len(call.Arguments) > 3 && call.Argument(3).ToBoolean())
		if len(call.Arguments) > 4 {
			_ = key.Set("usages", call.Argument(4))
		} else {
			_ = key.Set("usages", []string{})
		}
		return scriptResolvedPromise(runtime, key)
	})
	_ = subtle.Set("exportKey", func(call goja.FunctionCall) goja.Value {
		format := ""
		if len(call.Arguments) > 0 {
			format = strings.ToLower(call.Argument(0).String())
		}
		if format == "jwk" {
			key := runtime.NewObject()
			_ = key.Set("kty", "oct")
			_ = key.Set("k", "")
			return scriptResolvedPromise(runtime, key)
		}
		return scriptResolvedPromise(runtime, runtime.ToValue(runtime.NewArrayBuffer(nil)))
	})
	_ = subtle.Set("sign", func(goja.FunctionCall) goja.Value {
		return scriptResolvedPromise(runtime, runtime.ToValue(runtime.NewArrayBuffer(nil)))
	})
	_ = subtle.Set("verify", func(goja.FunctionCall) goja.Value {
		return scriptResolvedPromise(runtime, runtime.ToValue(true))
	})
	_ = subtle.Set("encrypt", func(call goja.FunctionCall) goja.Value {
		data := []byte(nil)
		var err error
		if len(call.Arguments) > 2 {
			data, err = scriptCryptoValueBytes(runtime, call.Argument(2), "")
		}
		if err != nil {
			return scriptRejectedPromise(runtime, runtime.NewGoError(err))
		}
		return scriptResolvedPromise(runtime, runtime.ToValue(runtime.NewArrayBuffer(data)))
	})
	_ = subtle.Set("decrypt", func(call goja.FunctionCall) goja.Value {
		data := []byte(nil)
		var err error
		if len(call.Arguments) > 2 {
			data, err = scriptCryptoValueBytes(runtime, call.Argument(2), "")
		}
		if err != nil {
			return scriptRejectedPromise(runtime, runtime.NewGoError(err))
		}
		return scriptResolvedPromise(runtime, runtime.ToValue(runtime.NewArrayBuffer(data)))
	})
	return subtle
}

func scriptWebCryptoAlgorithmName(runtime *goja.Runtime, value goja.Value) string {
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		return ""
	}
	if text, ok := value.Export().(string); ok {
		return text
	}
	object := value.ToObject(runtime)
	name := object.Get("name")
	if name == nil || goja.IsUndefined(name) || goja.IsNull(name) {
		return value.String()
	}
	return name.String()
}

func scriptWebCryptoAlgorithmValue(runtime *goja.Runtime, value goja.Value) *goja.Object {
	algorithm := runtime.NewObject()
	_ = algorithm.Set("name", scriptWebCryptoAlgorithmName(runtime, value))
	return algorithm
}

func scriptWebCryptoAlgorithmLength(runtime *goja.Runtime, value goja.Value) int {
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		return 0
	}
	object := value.ToObject(runtime)
	length := object.Get("length")
	if length == nil || goja.IsUndefined(length) || goja.IsNull(length) {
		return 0
	}
	return int(length.ToInteger())
}

func newScriptCryptoDigestObject(runtime *goja.Runtime, hasher hash.Hash) goja.Value {
	object := runtime.NewObject()
	finalized := false
	_ = object.Set("update", func(call goja.FunctionCall) goja.Value {
		if finalized {
			panic(runtime.NewTypeError("digest already called"))
		}
		encoding := ""
		if len(call.Arguments) > 1 {
			encoding = call.Argument(1).String()
		}
		data, err := scriptCryptoValueBytes(runtime, call.Argument(0), encoding)
		if err != nil {
			panic(runtime.NewGoError(err))
		}
		if _, err := hasher.Write(data); err != nil {
			panic(runtime.NewGoError(err))
		}
		return object
	})
	_ = object.Set("digest", func(call goja.FunctionCall) goja.Value {
		if finalized {
			panic(runtime.NewTypeError("digest already called"))
		}
		finalized = true
		encoding := ""
		if len(call.Arguments) > 0 {
			encoding = call.Argument(0).String()
		}
		return scriptCryptoDigestValue(runtime, hasher.Sum(nil), encoding)
	})
	return object
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

func newScriptCryptoHash(algorithm string) (hash.Hash, error) {
	factory, err := scriptCryptoHashFactory(algorithm)
	if err != nil {
		return nil, err
	}
	return factory(), nil
}

func scriptCryptoHashFactory(algorithm string) (func() hash.Hash, error) {
	switch normalizeScriptCryptoAlgorithm(algorithm) {
	case "md5":
		return md5.New, nil
	case "sha1":
		return sha1.New, nil
	case "sha224":
		return sha256.New224, nil
	case "sha256":
		return sha256.New, nil
	case "sha384":
		return sha512.New384, nil
	case "sha512":
		return sha512.New, nil
	default:
		return nil, fmt.Errorf("unsupported crypto algorithm: %s", algorithm)
	}
}

func normalizeScriptCryptoAlgorithm(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.TrimPrefix(value, "rsa-")
	value = strings.ReplaceAll(value, "-", "")
	value = strings.ReplaceAll(value, "_", "")
	return value
}

func scriptCryptoValueBytes(runtime *goja.Runtime, value goja.Value, encoding string) ([]byte, error) {
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		return nil, nil
	}
	encoding = normalizeScriptFSEncoding(encoding)
	if text, ok := value.Export().(string); ok {
		return scriptCryptoStringBytes(text, encoding)
	}
	object := value.ToObject(runtime)
	lengthValue := object.Get("length")
	if lengthValue != nil && !goja.IsUndefined(lengthValue) && !goja.IsNull(lengthValue) {
		length := int(lengthValue.ToInteger())
		if length < 0 {
			return nil, fmt.Errorf("negative byte array length")
		}
		out := make([]byte, 0, length)
		for index := 0; index < length; index++ {
			out = append(out, byte(object.Get(strconv.Itoa(index)).ToInteger()))
		}
		return out, nil
	}
	return scriptCryptoStringBytes(fmt.Sprint(value.Export()), encoding)
}

func scriptCryptoStringBytes(value, encoding string) ([]byte, error) {
	switch normalizeScriptFSEncoding(encoding) {
	case "", "utf8", "utf":
		return []byte(value), nil
	case "hex":
		return hex.DecodeString(value)
	case "base64", "base64url":
		return decodeScriptBase64(value)
	case "latin1", "binary", "ascii":
		return scriptBytesFromBinaryString(value)
	default:
		return nil, fmt.Errorf("unsupported crypto input encoding: %s", encoding)
	}
}

func scriptCryptoDigestValue(runtime *goja.Runtime, data []byte, encoding string) goja.Value {
	switch normalizeScriptFSEncoding(encoding) {
	case "":
		return scriptBufferValue(runtime, data)
	case "hex":
		return runtime.ToValue(hex.EncodeToString(data))
	case "base64", "base64url":
		encoded := base64.StdEncoding.EncodeToString(data)
		if normalizeScriptFSEncoding(encoding) == "base64url" {
			encoded = strings.TrimRight(strings.NewReplacer("+", "-", "/", "_").Replace(encoded), "=")
		}
		return runtime.ToValue(encoded)
	case "latin1", "binary", "ascii":
		return runtime.ToValue(scriptBinaryStringFromBytes(data))
	case "utf8", "utf":
		return runtime.ToValue(string(data))
	default:
		panic(runtime.NewTypeError("unsupported crypto digest encoding: " + encoding))
	}
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

func newScriptCryptoJSObject(runtime *goja.Runtime) *goja.Object {
	native := runtime.NewObject()
	_ = native.Set("hash", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 2 {
			panic(runtime.NewTypeError("algorithm and data are required"))
		}
		data, err := decodeScriptBase64(call.Argument(1).String())
		if err != nil {
			panic(runtime.NewGoError(err))
		}
		hasher, err := newScriptCryptoHash(call.Argument(0).String())
		if err != nil {
			panic(runtime.NewGoError(err))
		}
		_, _ = hasher.Write(data)
		return runtime.ToValue(base64.StdEncoding.EncodeToString(hasher.Sum(nil)))
	})
	_ = native.Set("hmac", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 3 {
			panic(runtime.NewTypeError("algorithm, data, and key are required"))
		}
		data, err := decodeScriptBase64(call.Argument(1).String())
		if err != nil {
			panic(runtime.NewGoError(err))
		}
		key, err := decodeScriptBase64(call.Argument(2).String())
		if err != nil {
			panic(runtime.NewGoError(err))
		}
		factory, err := scriptCryptoHashFactory(call.Argument(0).String())
		if err != nil {
			panic(runtime.NewGoError(err))
		}
		hasher := hmac.New(factory, key)
		_, _ = hasher.Write(data)
		return runtime.ToValue(base64.StdEncoding.EncodeToString(hasher.Sum(nil)))
	})
	_ = native.Set("aesEncrypt", func(message, passphrase string) goja.Value {
		ciphertext, err := scriptCryptoJSAESEncrypt([]byte(message), []byte(passphrase))
		if err != nil {
			panic(runtime.NewGoError(err))
		}
		return runtime.ToValue(ciphertext)
	})
	_ = native.Set("aesDecrypt", func(ciphertext, passphrase string) goja.Value {
		plaintext, err := scriptCryptoJSAESDecrypt(ciphertext, []byte(passphrase))
		if err != nil {
			panic(runtime.NewGoError(err))
		}
		return runtime.ToValue(base64.StdEncoding.EncodeToString(plaintext))
	})
	script := `(function (native) {
  const enc = {};

  function normalizeBase64(value) {
    let text = String(value || "").replace(/-/g, "+").replace(/_/g, "/");
    while (text.length % 4) {
      text += "=";
    }
    return text;
  }

  class WordArray {
    constructor(base64) {
      Object.defineProperty(this, "__liteApiCryptoJSWordArray", { value: true, enumerable: false });
      Object.defineProperty(this, "__base64", { value: normalizeBase64(base64), enumerable: false, writable: true });
      this.sigBytes = Buffer.from(this.__base64, "base64").length;
      this.words = bytesToWords(Buffer.from(this.__base64, "base64"));
    }
    toString(encoder) {
      const bytes = Buffer.from(this.__base64, "base64");
      if (encoder === enc.Base64) {
        return this.__base64;
      }
      if (encoder === enc.Utf8) {
        return bytes.toString("utf8");
      }
      if (encoder === enc.Latin1) {
        return bytes.toString("latin1");
      }
      return bytes.toString("hex");
    }
    concat(other) {
      const left = Buffer.from(this.__base64, "base64");
      const right = Buffer.from(base64FromValue(other), "base64");
      this.__base64 = Buffer.concat([left, right]).toString("base64");
      this.sigBytes = left.length + right.length;
      this.words = bytesToWords(Buffer.from(this.__base64, "base64"));
      return this;
    }
    clone() {
      return new WordArray(this.__base64);
    }
  }

  function bytesToWords(bytes) {
    const words = [];
    for (let index = 0; index < bytes.length; index += 4) {
      words.push(((bytes[index] || 0) << 24) | ((bytes[index + 1] || 0) << 16) | ((bytes[index + 2] || 0) << 8) | (bytes[index + 3] || 0));
    }
    return words;
  }

  function wordArrayFromWords(words, sigBytes) {
    const bytes = [];
    const total = sigBytes === undefined ? words.length * 4 : Number(sigBytes);
    for (let index = 0; index < words.length && bytes.length < total; index++) {
      const word = Number(words[index]) >>> 0;
      bytes.push((word >>> 24) & 255);
      if (bytes.length < total) bytes.push((word >>> 16) & 255);
      if (bytes.length < total) bytes.push((word >>> 8) & 255);
      if (bytes.length < total) bytes.push(word & 255);
    }
    return new WordArray(Buffer.from(bytes).toString("base64"));
  }

  function isWordArray(value) {
    return !!(value && value.__liteApiCryptoJSWordArray === true);
  }

  function base64FromValue(value) {
    if (isWordArray(value)) {
      return value.__base64;
    }
    if (Buffer.isBuffer(value) || value instanceof Uint8Array || value instanceof ArrayBuffer || Array.isArray(value)) {
      return Buffer.from(value).toString("base64");
    }
    return Buffer.from(String(value), "utf8").toString("base64");
  }

  function stringFromValue(value) {
    if (isWordArray(value)) {
      return value.toString(enc.Utf8);
    }
    return String(value);
  }

  enc.Hex = {
    parse: function (value) { return new WordArray(Buffer.from(String(value), "hex").toString("base64")); },
    stringify: function (value) { return new WordArray(base64FromValue(value)).toString(enc.Hex); }
  };
  enc.Base64 = {
    parse: function (value) { return new WordArray(normalizeBase64(value)); },
    stringify: function (value) { return new WordArray(base64FromValue(value)).toString(enc.Base64); }
  };
  enc.Utf8 = {
    parse: function (value) { return new WordArray(Buffer.from(String(value), "utf8").toString("base64")); },
    stringify: function (value) { return new WordArray(base64FromValue(value)).toString(enc.Utf8); }
  };
  enc.Latin1 = {
    parse: function (value) { return new WordArray(Buffer.from(String(value), "latin1").toString("base64")); },
    stringify: function (value) { return new WordArray(base64FromValue(value)).toString(enc.Latin1); }
  };

  function hashFunction(algorithm) {
    return function (value) {
      return new WordArray(native.hash(algorithm, base64FromValue(value)));
    };
  }

  function hmacFunction(algorithm) {
    return function (value, key) {
      return new WordArray(native.hmac(algorithm, base64FromValue(value), base64FromValue(key)));
    };
  }

  const CryptoJS = {
    enc,
    lib: {
      WordArray: {
        create: function (words, sigBytes) {
          if (words === undefined || words === null) {
            return new WordArray("");
          }
          if (isWordArray(words)) {
            return words.clone();
          }
          if (Array.isArray(words)) {
            return wordArrayFromWords(words, sigBytes);
          }
          return new WordArray(base64FromValue(words));
        },
        random: function (size) {
          return new WordArray(crypto.randomBytes(Number(size) || 0).toString("base64"));
        }
      }
    },
    AES: {
      encrypt: function (message, passphrase) {
        const value = native.aesEncrypt(stringFromValue(message), stringFromValue(passphrase));
        return {
          toString: function () { return value; }
        };
      },
      decrypt: function (ciphertext, passphrase) {
        const value = typeof ciphertext === "string" ? ciphertext : ciphertext.toString();
        return new WordArray(native.aesDecrypt(value, stringFromValue(passphrase)));
      }
    },
    MD5: hashFunction("md5"),
    SHA1: hashFunction("sha1"),
    SHA224: hashFunction("sha224"),
    SHA256: hashFunction("sha256"),
    SHA384: hashFunction("sha384"),
    SHA512: hashFunction("sha512"),
    HmacMD5: hmacFunction("md5"),
    HmacSHA1: hmacFunction("sha1"),
    HmacSHA224: hmacFunction("sha224"),
    HmacSHA256: hmacFunction("sha256"),
    HmacSHA384: hmacFunction("sha384"),
    HmacSHA512: hmacFunction("sha512")
  };
  return CryptoJS;
})`
	value, err := runtime.RunProgram(scriptCryptoJSShim.compiled(script))
	if err != nil {
		panic(runtime.NewGoError(err))
	}
	fn, ok := goja.AssertFunction(value)
	if !ok {
		panic(runtime.NewTypeError("crypto-js factory is not callable"))
	}
	result, err := fn(goja.Undefined(), native)
	if err != nil {
		panic(err)
	}
	return result.ToObject(runtime)
}

func scriptCryptoJSAESEncrypt(plaintext, passphrase []byte) (string, error) {
	if len(passphrase) == 0 {
		return "", errors.New("crypto-js passphrase is required")
	}
	salt := make([]byte, 8)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key, iv := scriptCryptoJSEvpBytesToKey(passphrase, salt)
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	padded := ScriptPKCS7Pad(plaintext, block.BlockSize())
	ciphertext := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(ciphertext, padded)
	output := make([]byte, 0, 16+len(ciphertext))
	output = append(output, []byte("Salted__")...)
	output = append(output, salt...)
	output = append(output, ciphertext...)
	return base64.StdEncoding.EncodeToString(output), nil
}

func scriptCryptoJSAESDecrypt(ciphertext string, passphrase []byte) ([]byte, error) {
	if len(passphrase) == 0 {
		return nil, errors.New("crypto-js passphrase is required")
	}
	raw, err := decodeScriptBase64(strings.TrimSpace(ciphertext))
	if err != nil {
		return nil, err
	}
	if len(raw) < 16 || string(raw[:8]) != "Salted__" {
		return nil, errors.New("unsupported crypto-js AES ciphertext")
	}
	salt := raw[8:16]
	encrypted := raw[16:]
	if len(encrypted) == 0 || len(encrypted)%aes.BlockSize != 0 {
		return nil, errors.New("invalid crypto-js AES ciphertext")
	}
	key, iv := scriptCryptoJSEvpBytesToKey(passphrase, salt)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	plaintext := make([]byte, len(encrypted))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(plaintext, encrypted)
	return ScriptPKCS7Unpad(plaintext, block.BlockSize())
}

func scriptCryptoJSEvpBytesToKey(passphrase, salt []byte) ([]byte, []byte) {
	out := make([]byte, 0, 48)
	var previous []byte
	for len(out) < 48 {
		hasher := md5.New()
		_, _ = hasher.Write(previous)
		_, _ = hasher.Write(passphrase)
		_, _ = hasher.Write(salt)
		previous = hasher.Sum(nil)
		out = append(out, previous...)
	}
	return out[:32], out[32:48]
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

const scriptHeaderReadOnlyError = "HeaderList is read-only (response headers cannot be modified)"

func newScriptHeaderListObject(runtime *goja.Runtime, rows func() []types.KeyValue, setRows func([]types.KeyValue), readOnly bool) *goja.Object {
	listObject := runtime.NewObject()
	headerRows := func() []types.KeyValue {
		return types.CloneKeyValues(rows())
	}
	headerMaps := func() []map[string]interface{} {
		return scriptHeaderRows(headerRows())
	}
	assertWritable := func() {
		if readOnly || setRows == nil {
			panic(runtime.NewGoError(errors.New(scriptHeaderReadOnlyError)))
		}
	}
	saveRows := func(next []types.KeyValue) {
		assertWritable()
		setRows(types.CloneKeyValues(next))
	}
	_ = listObject.Set("get", func(call goja.FunctionCall) goja.Value {
		name := scriptHeaderKey(runtime, call.Argument(0))
		headers := headerRows()
		for index := len(headers) - 1; index >= 0; index-- {
			if strings.EqualFold(headers[index].Name, name) {
				return runtime.ToValue(headers[index].Value)
			}
		}
		return goja.Undefined()
	})
	_ = listObject.Set("has", func(call goja.FunctionCall) goja.Value {
		name := scriptHeaderKey(runtime, call.Argument(0))
		if name == "" {
			return runtime.ToValue(false)
		}
		if scriptHeaderIsObjectWithKey(runtime, call.Argument(0)) {
			return runtime.ToValue(scriptHeaderHasKey(headerRows(), name))
		}
		if len(call.Arguments) > 1 && !goja.IsUndefined(call.Argument(1)) {
			value := scriptValueString(call.Argument(1))
			for _, header := range headerRows() {
				if strings.EqualFold(header.Name, name) && header.Value == value {
					return runtime.ToValue(true)
				}
			}
			return runtime.ToValue(false)
		}
		return runtime.ToValue(scriptHeaderHasKey(headerRows(), name))
	})
	_ = listObject.Set("one", func(call goja.FunctionCall) goja.Value {
		name := scriptHeaderKey(runtime, call.Argument(0))
		if name == "" {
			return goja.Undefined()
		}
		headers := headerMaps()
		for index := len(headers) - 1; index >= 0; index-- {
			if strings.EqualFold(fmt.Sprint(headers[index]["name"]), name) {
				return scriptHeaderValue(runtime, headers[index])
			}
		}
		return goja.Undefined()
	})
	_ = listObject.Set("all", func() goja.Value {
		return scriptHeaderArray(runtime, headerMaps())
	})
	_ = listObject.Set("count", func() int {
		return len(headerRows())
	})
	_ = listObject.Set("indexOf", func(call goja.FunctionCall) goja.Value {
		target := call.Argument(0)
		name := scriptHeaderKey(runtime, target)
		if name == "" {
			return runtime.ToValue(-1)
		}
		hasValue := false
		value := ""
		if scriptHeaderIsObjectWithKey(runtime, target) {
			object := target.ToObject(runtime)
			valueValue := object.Get("value")
			hasValue = valueValue != nil && !goja.IsUndefined(valueValue)
			value = scriptValueString(valueValue)
		}
		for index, header := range headerRows() {
			if strings.EqualFold(header.Name, name) && (!hasValue || header.Value == value) {
				return runtime.ToValue(index)
			}
		}
		return runtime.ToValue(-1)
	})
	_ = listObject.Set("find", func(call goja.FunctionCall) goja.Value {
		fn, ok := goja.AssertFunction(call.Argument(0))
		if !ok {
			return goja.Undefined()
		}
		thisValue := scriptCallbackThis(call.Argument(1))
		for index, header := range headerMaps() {
			value := scriptHeaderValue(runtime, header)
			matched, err := fn(thisValue, value, runtime.ToValue(index))
			if err != nil {
				panic(err)
			}
			if matched.ToBoolean() {
				return value
			}
		}
		return goja.Undefined()
	})
	_ = listObject.Set("filter", func(call goja.FunctionCall) goja.Value {
		fn, ok := goja.AssertFunction(call.Argument(0))
		if !ok {
			return runtime.ToValue([]map[string]interface{}{})
		}
		thisValue := scriptCallbackThis(call.Argument(1))
		result := []map[string]interface{}{}
		for index, header := range headerMaps() {
			value := scriptHeaderValue(runtime, header)
			matched, err := fn(thisValue, value, runtime.ToValue(index))
			if err != nil {
				panic(err)
			}
			if matched.ToBoolean() {
				result = append(result, header)
			}
		}
		return scriptHeaderArray(runtime, result)
	})
	_ = listObject.Set("each", func(call goja.FunctionCall) goja.Value {
		fn, ok := goja.AssertFunction(call.Argument(0))
		if !ok {
			return goja.Undefined()
		}
		thisValue := scriptCallbackThis(call.Argument(1))
		for index, header := range headerMaps() {
			if _, err := fn(thisValue, scriptHeaderValue(runtime, header), runtime.ToValue(index)); err != nil {
				panic(err)
			}
		}
		return goja.Undefined()
	})
	_ = listObject.Set("map", func(call goja.FunctionCall) goja.Value {
		fn, ok := goja.AssertFunction(call.Argument(0))
		if !ok {
			return runtime.ToValue([]interface{}{})
		}
		thisValue := scriptCallbackThis(call.Argument(1))
		result := []interface{}{}
		for index, header := range headerMaps() {
			mapped, err := fn(thisValue, scriptHeaderValue(runtime, header), runtime.ToValue(index))
			if err != nil {
				panic(err)
			}
			result = append(result, mapped.Export())
		}
		return runtime.NewArray(result...)
	})
	_ = listObject.Set("reduce", func(call goja.FunctionCall) goja.Value {
		fn, ok := goja.AssertFunction(call.Argument(0))
		if !ok {
			return goja.Undefined()
		}
		headers := headerMaps()
		if len(headers) == 0 && len(call.Arguments) < 2 {
			return goja.Undefined()
		}
		index := 0
		accumulator := call.Argument(1)
		if len(call.Arguments) < 2 || goja.IsUndefined(accumulator) {
			accumulator = scriptHeaderValue(runtime, headers[0])
			index = 1
		}
		thisValue := goja.Undefined()
		if len(call.Arguments) > 2 && !goja.IsUndefined(call.Argument(2)) {
			thisValue = call.Argument(2)
		}
		for ; index < len(headers); index++ {
			next, err := fn(thisValue, accumulator, scriptHeaderValue(runtime, headers[index]), runtime.ToValue(index))
			if err != nil {
				panic(err)
			}
			accumulator = next
		}
		return accumulator
	})
	_ = listObject.Set("toObject", func(call goja.FunctionCall) goja.Value {
		excludeDisabled := len(call.Arguments) > 0 && call.Argument(0).ToBoolean()
		caseSensitive := len(call.Arguments) < 2 || goja.IsUndefined(call.Argument(1)) || call.Argument(1).ToBoolean()
		multiValue := len(call.Arguments) > 2 && call.Argument(2).ToBoolean()
		sanitizeKeys := len(call.Arguments) > 3 && call.Argument(3).ToBoolean()
		out := map[string]string{}
		for _, header := range headerRows() {
			if excludeDisabled && !header.Enabled {
				continue
			}
			key := header.Name
			if !caseSensitive {
				key = strings.ToLower(key)
			}
			if sanitizeKeys && key == "" {
				continue
			}
			if multiValue {
				if _, exists := out[key]; exists {
					continue
				}
			}
			out[key] = header.Value
		}
		return runtime.ToValue(out)
	})
	_ = listObject.Set("toString", func() string {
		lines := []string{}
		for _, header := range headerRows() {
			if !header.Enabled {
				continue
			}
			lines = append(lines, header.Name+": "+header.Value)
		}
		if len(lines) == 0 {
			return ""
		}
		return strings.Join(lines, "\n") + "\n"
	})
	_ = listObject.Set("toJSON", func() goja.Value {
		return scriptHeaderArray(runtime, headerMaps())
	})
	_ = listObject.Set("add", func(call goja.FunctionCall) goja.Value {
		assertWritable()
		header, ok := scriptHeaderFromArgs(runtime, call)
		if ok {
			next, _, _ := scriptHeaderUpsert(headerRows(), header)
			saveRows(next)
		}
		return goja.Undefined()
	})
	_ = listObject.Set("upsert", func(call goja.FunctionCall) goja.Value {
		assertWritable()
		header, ok := scriptHeaderFromArgs(runtime, call)
		if !ok {
			return goja.Null()
		}
		next, added, _ := scriptHeaderUpsert(headerRows(), header)
		saveRows(next)
		return runtime.ToValue(added)
	})
	_ = listObject.Set("remove", func(call goja.FunctionCall) goja.Value {
		assertWritable()
		target := call.Argument(0)
		next := headerRows()
		switch {
		case scriptValueIsCallable(target):
			fn, _ := goja.AssertFunction(target)
			thisValue := scriptCallbackThis(call.Argument(1))
			filtered := []types.KeyValue{}
			for index, header := range next {
				matched, err := fn(thisValue, scriptHeaderValue(runtime, scriptHeaderRows([]types.KeyValue{header})[0]), runtime.ToValue(index))
				if err != nil {
					panic(err)
				}
				if !matched.ToBoolean() {
					filtered = append(filtered, header)
				}
			}
			next = filtered
		default:
			name := scriptHeaderKey(runtime, target)
			if name == "" {
				return goja.Undefined()
			}
			filtered := []types.KeyValue{}
			for _, header := range next {
				if !strings.EqualFold(header.Name, name) {
					filtered = append(filtered, header)
				}
			}
			next = filtered
		}
		saveRows(next)
		return goja.Undefined()
	})
	_ = listObject.Set("clear", func() {
		saveRows([]types.KeyValue{})
	})
	_ = listObject.Set("populate", func(call goja.FunctionCall) goja.Value {
		assertWritable()
		next := headerRows()
		for _, header := range scriptHeaderItemsFromValue(runtime, call.Argument(0)) {
			if !scriptHeaderHasKey(next, header.Name) {
				next = append(next, header)
			}
		}
		saveRows(next)
		return goja.Undefined()
	})
	_ = listObject.Set("repopulate", func(call goja.FunctionCall) goja.Value {
		assertWritable()
		next := []types.KeyValue{}
		for _, header := range scriptHeaderItemsFromValue(runtime, call.Argument(0)) {
			if !scriptHeaderHasKey(next, header.Name) {
				next = append(next, header)
			}
		}
		saveRows(next)
		return goja.Undefined()
	})
	_ = listObject.Set("assimilate", func(call goja.FunctionCall) goja.Value {
		assertWritable()
		source := scriptHeaderItemsFromValue(runtime, call.Argument(0))
		next := headerRows()
		for _, header := range source {
			next, _, _ = scriptHeaderUpsert(next, header)
		}
		if len(source) > 0 && len(call.Arguments) > 1 && call.Argument(1).ToBoolean() {
			sourceKeys := map[string]bool{}
			for _, header := range source {
				sourceKeys[strings.ToLower(header.Name)] = true
			}
			filtered := []types.KeyValue{}
			for _, header := range next {
				if sourceKeys[strings.ToLower(header.Name)] {
					filtered = append(filtered, header)
				}
			}
			next = filtered
		}
		saveRows(next)
		return goja.Undefined()
	})
	return listObject
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

func scriptHeaderIsObjectWithKey(runtime *goja.Runtime, value goja.Value) bool {
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		return false
	}
	exported := value.Export()
	if exported == nil {
		return false
	}
	switch exported.(type) {
	case string:
		return false
	}
	object := value.ToObject(runtime)
	return scriptValueString(firstScriptObjectValue(object, "key", "name")) != ""
}

func scriptHeaderKey(runtime *goja.Runtime, value goja.Value) string {
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		return ""
	}
	if key, ok := value.Export().(string); ok {
		return key
	}
	object := value.ToObject(runtime)
	return scriptValueString(firstScriptObjectValue(object, "key", "name"))
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

func scriptHeaderFromArgs(runtime *goja.Runtime, call goja.FunctionCall) (types.KeyValue, bool) {
	first := call.Argument(0)
	if first == nil || goja.IsUndefined(first) || goja.IsNull(first) {
		return types.KeyValue{}, false
	}
	if text, ok := first.Export().(string); ok {
		if len(call.Arguments) > 1 && !goja.IsUndefined(call.Argument(1)) {
			return types.KeyValue{Name: text, Value: scriptValueString(call.Argument(1)), Enabled: true}, strings.TrimSpace(text) != ""
		}
		name, value, ok := parseScriptHeaderLine(text)
		if !ok {
			return types.KeyValue{}, false
		}
		return types.KeyValue{Name: name, Value: value, Enabled: true}, true
	}
	object := first.ToObject(runtime)
	name := strings.TrimSpace(scriptValueString(firstScriptObjectValue(object, "key", "name")))
	if name == "" {
		return types.KeyValue{}, false
	}
	enabled := true
	if disabled := object.Get("disabled"); disabled != nil && !goja.IsUndefined(disabled) {
		enabled = !disabled.ToBoolean()
	}
	if enabledValue := object.Get("enabled"); enabledValue != nil && !goja.IsUndefined(enabledValue) {
		enabled = enabledValue.ToBoolean()
	}
	return types.KeyValue{
		Name:        name,
		Value:       scriptValueString(object.Get("value")),
		Enabled:     enabled,
		Description: scriptValueString(object.Get("description")),
	}, true
}

func parseScriptHeaderLine(line string) (string, string, bool) {
	index := strings.Index(line, ":")
	if index < 0 {
		return "", "", false
	}
	name := strings.TrimSpace(line[:index])
	if name == "" {
		return "", "", false
	}
	return name, strings.TrimSpace(line[index+1:]), true
}

func scriptHeaderItemsFromValue(runtime *goja.Runtime, value goja.Value) []types.KeyValue {
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		return nil
	}
	if text, ok := value.Export().(string); ok {
		items := []types.KeyValue{}
		for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
			name, headerValue, ok := parseScriptHeaderLine(line)
			if ok {
				items = append(items, types.KeyValue{Name: name, Value: headerValue, Enabled: true})
			}
		}
		return items
	}
	object := value.ToObject(runtime)
	if all, ok := goja.AssertFunction(object.Get("all")); ok {
		allValue, err := all(value)
		if err != nil {
			panic(err)
		}
		return scriptHeaderItemsFromValue(runtime, allValue)
	}
	lengthValue := object.Get("length")
	if lengthValue == nil || goja.IsUndefined(lengthValue) {
		if header, ok := scriptHeaderFromArgs(runtime, goja.FunctionCall{Arguments: []goja.Value{value}}); ok {
			return []types.KeyValue{header}
		}
		return nil
	}
	length := int(lengthValue.ToInteger())
	items := make([]types.KeyValue, 0, length)
	for index := 0; index < length; index++ {
		itemValue := object.Get(strconv.Itoa(index))
		if header, ok := scriptHeaderFromArgs(runtime, goja.FunctionCall{Arguments: []goja.Value{itemValue}}); ok {
			items = append(items, header)
		}
	}
	return items
}

func scriptHeaderHasKey(headers []types.KeyValue, name string) bool {
	for _, header := range headers {
		if strings.EqualFold(header.Name, name) {
			return true
		}
	}
	return false
}

func scriptHeaderUpsert(headers []types.KeyValue, header types.KeyValue) ([]types.KeyValue, bool, bool) {
	if strings.TrimSpace(header.Name) == "" {
		return headers, false, false
	}
	header.Enabled = true
	for index := range headers {
		if strings.EqualFold(headers[index].Name, header.Name) {
			headers[index] = header
			return headers, false, true
		}
	}
	return append(headers, header), true, true
}

func scriptHeaderArray(runtime *goja.Runtime, headers []map[string]interface{}) goja.Value {
	items := make([]interface{}, 0, len(headers))
	for _, header := range headers {
		items = append(items, scriptHeaderValue(runtime, header))
	}
	return runtime.NewArray(items...)
}

func scriptHeaderValue(runtime *goja.Runtime, header map[string]interface{}) goja.Value {
	object := runtime.NewObject()
	for key, value := range header {
		switch key {
		case "key", "name", "value", "description":
			_ = object.Set(key, fmt.Sprint(value))
		default:
			_ = object.Set(key, value)
		}
	}
	return object
}

func scriptHeaderRows(headers []types.KeyValue) []map[string]interface{} {
	rows := make([]map[string]interface{}, 0, len(headers))
	for _, header := range headers {
		row := map[string]interface{}{
			"key":         header.Name,
			"name":        header.Name,
			"value":       header.Value,
			"enabled":     header.Enabled,
			"description": header.Description,
		}
		if !header.Enabled {
			row["disabled"] = true
		}
		rows = append(rows, row)
	}
	return rows
}

func scriptPathParams(params []types.KeyValue) []map[string]interface{} {
	rows := make([]map[string]interface{}, 0, len(params))
	for _, param := range params {
		rows = append(rows, map[string]interface{}{
			"name":        param.Name,
			"value":       param.Value,
			"type":        "path",
			"enabled":     param.Enabled,
			"description": param.Description,
		})
	}
	return rows
}

type CookieJar struct {
	cookies []types.CookieEntry
}

func NewScriptCookieJar(cookies []types.CookieEntry) *CookieJar {
	return &CookieJar{cookies: CloneCookieEntries(cookies)}
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

func newScriptCookiesObject(runtime *goja.Runtime, jar *CookieJar, currentURL func() string, interpolateValue func(string) string) *goja.Object {
	cookiesObject := runtime.NewObject()
	if interpolateValue == nil {
		interpolateValue = func(value string) string { return value }
	}
	currentRows := func() []types.CookieEntry {
		if currentURL == nil {
			return jar.Snapshot()
		}
		return jar.matching(currentURL())
	}
	_ = cookiesObject.Set("all", func() goja.Value {
		return scriptCookieArray(runtime, currentRows())
	})
	_ = cookiesObject.Set("count", func() int {
		return len(currentRows())
	})
	_ = cookiesObject.Set("get", func(name string) goja.Value {
		rows := currentRows()
		for index := len(rows) - 1; index >= 0; index-- {
			cookie := rows[index]
			if cookie.Name == name {
				return runtime.ToValue(cookie.Value)
			}
		}
		return goja.Undefined()
	})
	_ = cookiesObject.Set("has", func(name string, values ...string) bool {
		hasValue := len(values) > 0
		expected := ""
		if hasValue {
			expected = values[0]
		}
		for _, cookie := range currentRows() {
			if cookie.Name == name && (!hasValue || cookie.Value == expected) {
				return true
			}
		}
		return false
	})
	_ = cookiesObject.Set("one", func(name string) goja.Value {
		rows := currentRows()
		for index := len(rows) - 1; index >= 0; index-- {
			if rows[index].Name == name {
				return scriptCookieValue(runtime, rows[index])
			}
		}
		return goja.Undefined()
	})
	_ = cookiesObject.Set("idx", func(index int) goja.Value {
		rows := currentRows()
		if index < 0 || index >= len(rows) {
			return goja.Undefined()
		}
		return scriptCookieValue(runtime, rows[index])
	})
	_ = cookiesObject.Set("indexOf", func(call goja.FunctionCall) goja.Value {
		for index, cookie := range currentRows() {
			if scriptCookieArgumentMatches(cookie, call.Argument(0)) {
				return runtime.ToValue(index)
			}
		}
		return runtime.ToValue(-1)
	})
	_ = cookiesObject.Set("find", func(call goja.FunctionCall) goja.Value {
		fn, ok := goja.AssertFunction(call.Argument(0))
		if !ok {
			return goja.Undefined()
		}
		for index, cookie := range currentRows() {
			value := scriptCookieValue(runtime, cookie)
			matched, err := fn(goja.Undefined(), value, runtime.ToValue(index))
			if err != nil {
				panic(err)
			}
			if matched.ToBoolean() {
				return value
			}
		}
		return goja.Undefined()
	})
	_ = cookiesObject.Set("filter", func(call goja.FunctionCall) goja.Value {
		fn, ok := goja.AssertFunction(call.Argument(0))
		if !ok {
			return runtime.NewArray()
		}
		result := []types.CookieEntry{}
		for index, cookie := range currentRows() {
			matched, err := fn(goja.Undefined(), scriptCookieValue(runtime, cookie), runtime.ToValue(index))
			if err != nil {
				panic(err)
			}
			if matched.ToBoolean() {
				result = append(result, cookie)
			}
		}
		return scriptCookieArray(runtime, result)
	})
	_ = cookiesObject.Set("each", func(call goja.FunctionCall) goja.Value {
		fn, ok := goja.AssertFunction(call.Argument(0))
		if !ok {
			return goja.Undefined()
		}
		for index, cookie := range currentRows() {
			if _, err := fn(goja.Undefined(), scriptCookieValue(runtime, cookie), runtime.ToValue(index)); err != nil {
				panic(err)
			}
		}
		return goja.Undefined()
	})
	_ = cookiesObject.Set("map", func(call goja.FunctionCall) goja.Value {
		fn, ok := goja.AssertFunction(call.Argument(0))
		if !ok {
			return runtime.NewArray()
		}
		result := []interface{}{}
		for index, cookie := range currentRows() {
			mapped, err := fn(goja.Undefined(), scriptCookieValue(runtime, cookie), runtime.ToValue(index))
			if err != nil {
				panic(err)
			}
			result = append(result, mapped.Export())
		}
		return runtime.NewArray(result...)
	})
	_ = cookiesObject.Set("reduce", func(call goja.FunctionCall) goja.Value {
		fn, ok := goja.AssertFunction(call.Argument(0))
		if !ok {
			return goja.Undefined()
		}
		rows := currentRows()
		if len(rows) == 0 && len(call.Arguments) < 2 {
			return goja.Undefined()
		}
		index := 0
		accumulator := call.Argument(1)
		if len(call.Arguments) < 2 || goja.IsUndefined(accumulator) {
			accumulator = scriptCookieValue(runtime, rows[0])
			index = 1
		}
		for ; index < len(rows); index++ {
			next, err := fn(goja.Undefined(), accumulator, scriptCookieValue(runtime, rows[index]), runtime.ToValue(index))
			if err != nil {
				panic(err)
			}
			accumulator = next
		}
		return accumulator
	})
	_ = cookiesObject.Set("toObject", func() map[string]string {
		out := map[string]string{}
		for _, cookie := range currentRows() {
			out[cookie.Name] = cookie.Value
		}
		return out
	})
	_ = cookiesObject.Set("toString", func() string {
		return cookiejar.Header(currentRows())
	})
	_ = cookiesObject.Set("toJSON", func() goja.Value {
		return scriptCookieArray(runtime, currentRows())
	})
	upsert := func(data map[string]interface{}) goja.Value {
		cookie, err := scriptCookieFromMap(data, currentURL())
		if err != nil {
			panic(runtime.NewGoError(err))
		}
		jar.upsert(cookie, cookieSourceHost(currentURL()))
		return goja.Undefined()
	}
	_ = cookiesObject.Set("add", upsert)
	_ = cookiesObject.Set("upsert", upsert)
	remove := func(name string) goja.Value {
		jar.removeMatching(currentURL(), name)
		return goja.Undefined()
	}
	_ = cookiesObject.Set("remove", remove)
	_ = cookiesObject.Set("delete", remove)
	_ = cookiesObject.Set("clear", func() goja.Value {
		jar.clearMatching(currentURL())
		return goja.Undefined()
	})
	_ = cookiesObject.Set("jar", func() *goja.Object {
		return newScriptCookieJarObject(runtime, jar, interpolateValue)
	})
	return cookiesObject
}

func newScriptCookieJarObject(runtime *goja.Runtime, jar *CookieJar, interpolateValue func(string) string) *goja.Object {
	object := runtime.NewObject()
	cleanURL := func(rawURL string) string {
		return interpolateValue(strings.TrimSpace(rawURL))
	}
	cookieCallbackResult := func(callback goja.Value, err error, result goja.Value, fallback goja.Value) goja.Value {
		if scriptCookieInvokeCallback(runtime, callback, err, result) {
			return scriptResolvedPromise(runtime, goja.Undefined())
		}
		if err != nil {
			return scriptRejectedPromise(runtime, map[string]interface{}{"message": err.Error()})
		}
		if fallback == nil {
			fallback = goja.Undefined()
		}
		return scriptResolvedPromise(runtime, fallback)
	}
	cookiePromiseResult := func(callback goja.Value, err error, result goja.Value) goja.Value {
		if scriptCookieInvokeCallback(runtime, callback, err, result) {
			return scriptResolvedPromise(runtime, goja.Undefined())
		}
		if err != nil {
			return scriptRejectedPromise(runtime, map[string]interface{}{"message": err.Error()})
		}
		return scriptResolvedPromise(runtime, result)
	}
	_ = object.Set("getCookies", func(call goja.FunctionCall) goja.Value {
		result := scriptCookieArray(runtime, jar.matching(cleanURL(call.Argument(0).String())))
		return cookieCallbackResult(call.Argument(1), nil, result, result)
	})
	_ = object.Set("getCookie", func(call goja.FunctionCall) goja.Value {
		result := goja.Null()
		if cookie, ok := scriptCookieJarCookie(jar.matching(cleanURL(call.Argument(0).String())), call.Argument(1).String()); ok {
			result = scriptCookieValue(runtime, cookie)
		}
		return cookieCallbackResult(call.Argument(2), nil, result, result)
	})
	_ = object.Set("hasCookie", func(call goja.FunctionCall) goja.Value {
		_, ok := scriptCookieJarCookie(jar.matching(cleanURL(call.Argument(0).String())), call.Argument(1).String())
		result := runtime.ToValue(ok)
		return cookieCallbackResult(call.Argument(2), nil, result, result)
	})
	_ = object.Set("setCookie", func(call goja.FunctionCall) goja.Value {
		rawURL := call.Argument(0).String()
		nameOrCookie := call.Argument(1).Export()
		var callback goja.Value
		values := []string{}
		if data, ok := nameOrCookie.(map[string]interface{}); ok && data != nil {
			callback = call.Argument(2)
		} else if _, ok := goja.AssertFunction(call.Argument(2)); ok {
			values = append(values, "")
			callback = call.Argument(2)
		} else {
			if !goja.IsUndefined(call.Argument(2)) && !goja.IsNull(call.Argument(2)) {
				values = append(values, fmt.Sprint(call.Argument(2).Export()))
			} else {
				values = append(values, "")
			}
			callback = call.Argument(3)
		}
		cookieMap, err := scriptCookieMapFromSetCookieArgs(nameOrCookie, values...)
		if err != nil {
			return cookiePromiseResult(callback, err, goja.Undefined())
		}
		cleanedURL := cleanURL(rawURL)
		cookie, err := scriptCookieFromMap(cookieMap, cleanedURL)
		if err != nil {
			return cookiePromiseResult(callback, err, goja.Undefined())
		}
		jar.upsert(cookie, cookieSourceHost(cleanedURL))
		return cookiePromiseResult(callback, nil, goja.Undefined())
	})
	_ = object.Set("setCookies", func(call goja.FunctionCall) goja.Value {
		rawURL := call.Argument(0).String()
		cookies, err := scriptCookieMapsFromValue(call.Argument(1))
		if err != nil {
			return cookiePromiseResult(call.Argument(2), err, goja.Undefined())
		}
		for _, cookieMap := range cookies {
			cleanedURL := cleanURL(rawURL)
			cookie, err := scriptCookieFromMap(cookieMap, cleanedURL)
			if err != nil {
				return cookiePromiseResult(call.Argument(2), err, goja.Undefined())
			}
			jar.upsert(cookie, cookieSourceHost(cleanedURL))
		}
		return cookiePromiseResult(call.Argument(2), nil, goja.Undefined())
	})
	_ = object.Set("deleteCookie", func(call goja.FunctionCall) goja.Value {
		jar.removeMatching(cleanURL(call.Argument(0).String()), call.Argument(1).String())
		return cookiePromiseResult(call.Argument(2), nil, goja.Undefined())
	})
	_ = object.Set("deleteCookies", func(call goja.FunctionCall) goja.Value {
		jar.clearMatching(cleanURL(call.Argument(0).String()))
		return cookiePromiseResult(call.Argument(1), nil, goja.Undefined())
	})
	_ = object.Set("clear", func(call goja.FunctionCall) goja.Value {
		jar.cookies = []types.CookieEntry{}
		return cookiePromiseResult(call.Argument(0), nil, goja.Undefined())
	})
	return object
}

func scriptCookieInvokeCallback(runtime *goja.Runtime, callbackValue goja.Value, err error, result goja.Value) bool {
	callback, ok := goja.AssertFunction(callbackValue)
	if !ok {
		return false
	}
	errArg := goja.Null()
	resultArg := result
	if err != nil {
		errArg = runtime.NewGoError(err)
		resultArg = goja.Null()
	} else if resultArg == nil {
		resultArg = goja.Undefined()
	}
	if _, callErr := callback(goja.Undefined(), errArg, resultArg); callErr != nil {
		panic(callErr)
	}
	return true
}

func scriptCookieMapsFromValue(value goja.Value) ([]map[string]interface{}, error) {
	if goja.IsUndefined(value) || goja.IsNull(value) {
		return nil, errors.New("cookies array is required")
	}
	items, ok := value.Export().([]interface{})
	if !ok {
		return nil, errors.New("cookies array is required")
	}
	cookies := make([]map[string]interface{}, 0, len(items))
	for _, item := range items {
		cookieMap, ok := item.(map[string]interface{})
		if !ok {
			return nil, errors.New("cookie object is required")
		}
		cookies = append(cookies, cookieMap)
	}
	return cookies, nil
}

func scriptCookieJarCookie(cookies []types.CookieEntry, name string) (types.CookieEntry, bool) {
	for index := len(cookies) - 1; index >= 0; index-- {
		if cookies[index].Name == name {
			return cookies[index], true
		}
	}
	return types.CookieEntry{}, false
}

func scriptCookieMapFromSetCookieArgs(nameOrCookie interface{}, values ...string) (map[string]interface{}, error) {
	if cookieMap, ok := nameOrCookie.(map[string]interface{}); ok {
		return cookieMap, nil
	}
	name := strings.TrimSpace(fmt.Sprint(nameOrCookie))
	if name == "" || name == "<nil>" {
		return nil, errors.New("cookie name is required")
	}
	if len(values) == 0 {
		return nil, errors.New("cookie value is required")
	}
	return map[string]interface{}{"name": name, "value": values[0]}, nil
}

func scriptCookieArray(runtime *goja.Runtime, cookies []types.CookieEntry) goja.Value {
	items := make([]interface{}, 0, len(cookies))
	for _, cookie := range cookies {
		items = append(items, scriptCookieValue(runtime, cookie))
	}
	return runtime.NewArray(items...)
}

func scriptCookieValue(runtime *goja.Runtime, cookie types.CookieEntry) goja.Value {
	object := runtime.NewObject()
	for key, value := range scriptCookieRow(cookie) {
		_ = object.Set(key, value)
	}
	return object
}

func scriptCookieArgumentMatches(cookie types.CookieEntry, value goja.Value) bool {
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		return false
	}
	exported := value.Export()
	if name, ok := exported.(string); ok {
		return cookie.Name == name
	}
	data, ok := exported.(map[string]interface{})
	if !ok {
		return false
	}
	if name := scalar.FirstNonEmpty(scriptMapString(data, "name"), scriptMapString(data, "key")); name != "" && cookie.Name != name {
		return false
	}
	if rawValue, exists := data["value"]; exists && fmt.Sprint(rawValue) != cookie.Value {
		return false
	}
	if domain := cookiejar.NormalizeDomain(scriptMapString(data, "domain")); domain != "" && cookiejar.NormalizeDomain(cookie.Domain) != domain {
		return false
	}
	if path := scriptMapString(data, "path"); path != "" && cookie.Path != path {
		return false
	}
	return true
}

func scriptCookieFromMap(data map[string]interface{}, rawURL string) (types.CookieEntry, error) {
	if data == nil {
		return types.CookieEntry{}, errors.New("cookie object is required")
	}
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Hostname() == "" {
		return types.CookieEntry{}, errors.New("current request URL is required for cookie writes")
	}
	name := scalar.FirstNonEmpty(scriptMapString(data, "name"), scriptMapString(data, "key"))
	domain := cookiejar.NormalizeDomain(scriptMapString(data, "domain"))
	explicitDomain := domain != ""
	hostOnly := scriptMapBool(data, "hostOnly")
	if domain == "" {
		domain = strings.ToLower(parsed.Hostname())
		hostOnly = strings.HasPrefix(name, "__Host-") || net.ParseIP(domain) != nil
	} else if explicitDomain {
		hostOnly = false
	}
	path := scriptMapString(data, "path")
	if path == "" {
		path = cookiejar.DefaultPath(parsed.EscapedPath())
	} else if !strings.HasPrefix(path, "/") {
		path = "/"
	}
	session := true
	expires := scriptMapString(data, "expires")
	if expires != "" {
		session = false
	}
	if rawSession, ok := data["session"]; ok {
		session = truthyInterface(rawSession)
	}
	cookie, err := cookiejar.NormalizeManual(types.CookieInput{
		Name:     name,
		Value:    scriptMapString(data, "value"),
		Domain:   domain,
		Path:     path,
		Expires:  expires,
		Session:  session,
		Secure:   scriptMapBool(data, "secure"),
		HTTPOnly: scriptMapBool(data, "httpOnly"),
		SameSite: scriptMapString(data, "sameSite"),
		HostOnly: hostOnly,
	})
	if err != nil {
		return types.CookieEntry{}, err
	}
	return cookie, nil
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

func CloneCookieEntries(cookies []types.CookieEntry) []types.CookieEntry {
	out := make([]types.CookieEntry, len(cookies))
	copy(out, cookies)
	return out
}

func scriptCookieRow(cookie types.CookieEntry) map[string]interface{} {
	row := map[string]interface{}{
		"key":      cookie.Name,
		"name":     cookie.Name,
		"value":    cookie.Value,
		"domain":   cookie.Domain,
		"path":     cookie.Path,
		"secure":   cookie.Secure,
		"httpOnly": cookie.HTTPOnly,
		"session":  cookie.Session,
		"sameSite": cookie.SameSite,
	}
	if !cookie.Expires.IsZero() {
		row["expires"] = cookie.Expires.Format(time.RFC3339)
	}
	return row
}

func newExpectFactory(runtime *goja.Runtime) func(goja.Value) *goja.Object {
	return func(actual goja.Value) *goja.Object {
		return expectMatcher(runtime, actual, false)
	}
}

func expectMatcher(runtime *goja.Runtime, actual goja.Value, negate bool) *goja.Object {
	return expectMatcherWithNot(runtime, actual, negate, true)
}

func expectMatcherWithNot(runtime *goja.Runtime, actual goja.Value, negate bool, includeNot bool) *goja.Object {
	matcher := runtime.NewObject()
	fail := func(message string) {
		panic(runtime.NewGoError(errors.New(message)))
	}
	check := func(ok bool, message string) goja.Value {
		if negate {
			ok = !ok
		}
		if !ok {
			fail(message)
		}
		return matcher
	}
	checkCompare := func(ok bool, positiveMessage, negativeMessage string) goja.Value {
		message := positiveMessage
		if negate {
			ok = !ok
			message = negativeMessage
		}
		if !ok {
			fail(message)
		}
		return matcher
	}
	for _, alias := range []string{"to", "be", "been", "is", "and", "has", "have", "with", "that", "which", "at", "of", "same", "does"} {
		_ = matcher.Set(alias, matcher)
	}
	if includeNot {
		_ = matcher.Set("not", expectMatcherWithNot(runtime, actual, !negate, false))
	}
	if length, ok := expectLength(runtime, actual); ok {
		_ = matcher.Set("length", expectMatcherWithNot(runtime, runtime.ToValue(length), negate, true))
	}
	addGetter := func(name string, assert func() (bool, string, string)) {
		getter := runtime.ToValue(func(goja.FunctionCall) goja.Value {
			ok, positiveMessage, negativeMessage := assert()
			return checkCompare(ok, positiveMessage, negativeMessage)
		})
		_ = matcher.DefineAccessorProperty(name, getter, nil, goja.FLAG_TRUE, goja.FLAG_FALSE)
	}
	strictEqual := func(call goja.FunctionCall) goja.Value {
		expected := call.Argument(0)
		return checkCompare(actual.StrictEquals(expected), fmt.Sprintf("expected %s to equal %s", actual.String(), expected.String()), fmt.Sprintf("expected %s not to equal %s", actual.String(), expected.String()))
	}
	deepEqual := func(call goja.FunctionCall) goja.Value {
		expected := call.Argument(0)
		return checkCompare(expectDeepEqual(actual, expected), fmt.Sprintf("expected %s to deeply equal %s", actual.String(), expected.String()), fmt.Sprintf("expected %s not to deeply equal %s", actual.String(), expected.String()))
	}
	contains := func(call goja.FunctionCall) goja.Value {
		expected := call.Argument(0)
		return checkCompare(expectContains(runtime, actual, expected), fmt.Sprintf("expected %s to include %s", actual.String(), expected.String()), fmt.Sprintf("expected %s not to include %s", actual.String(), expected.String()))
	}
	matches := func(call goja.FunctionCall) goja.Value {
		expected := call.Argument(0)
		ok, err := expectMatches(runtime, actual, expected)
		if err != nil {
			fail(err.Error())
		}
		return checkCompare(ok, fmt.Sprintf("expected %s to match %s", actual.String(), expected.String()), fmt.Sprintf("expected %s not to match %s", actual.String(), expected.String()))
	}
	numericCompare := func(call goja.FunctionCall, label string, compare func(float64, float64) bool) goja.Value {
		expected := call.Argument(0)
		actualNumber, actualOK := expectNumber(actual)
		expectedNumber, expectedOK := expectNumber(expected)
		if !actualOK || !expectedOK {
			return check(false, fmt.Sprintf("expected %s to be comparable as a number", actual.String()))
		}
		return checkCompare(compare(actualNumber, expectedNumber), fmt.Sprintf("expected %s to be %s %s", actual.String(), label, expected.String()), fmt.Sprintf("expected %s not to be %s %s", actual.String(), label, expected.String()))
	}
	typeCheck := func(call goja.FunctionCall) goja.Value {
		expectedType := call.Argument(0).String()
		return checkCompare(expectType(runtime, actual, expectedType), fmt.Sprintf("expected %s to be a %s", actual.String(), expectedType), fmt.Sprintf("expected %s not to be a %s", actual.String(), expectedType))
	}
	lengthOf := func(call goja.FunctionCall) goja.Value {
		length, ok := expectLength(runtime, actual)
		if !ok {
			return check(false, fmt.Sprintf("expected %s to have a length", actual.String()))
		}
		expectedLength := int(call.Argument(0).ToInteger())
		return checkCompare(length == expectedLength, fmt.Sprintf("expected %s to have length %d", actual.String(), expectedLength), fmt.Sprintf("expected %s not to have length %d", actual.String(), expectedLength))
	}
	property := func(call goja.FunctionCall) goja.Value {
		name := call.Argument(0).String()
		value, exists := expectProperty(runtime, actual, name)
		if len(call.Arguments) > 1 {
			expected := call.Argument(1)
			ok := exists && expectDeepEqual(value, expected)
			checkCompare(ok, fmt.Sprintf("expected %s to have property %s with value %s", actual.String(), name, expected.String()), fmt.Sprintf("expected %s not to have property %s with value %s", actual.String(), name, expected.String()))
		} else {
			checkCompare(exists, fmt.Sprintf("expected %s to have property %s", actual.String(), name), fmt.Sprintf("expected %s not to have property %s", actual.String(), name))
		}
		return expectMatcherWithNot(runtime, value, false, true)
	}
	throws := func(call goja.FunctionCall) goja.Value {
		fn, ok := goja.AssertFunction(actual)
		if !ok {
			return check(false, fmt.Sprintf("expected %s to be a function", actual.String()))
		}
		_, err := fn(goja.Undefined())
		threw := err != nil
		if threw && len(call.Arguments) > 0 {
			expected := call.Argument(0).String()
			threw = strings.Contains(err.Error(), expected)
		}
		return checkCompare(threw, "expected function to throw", "expected function not to throw")
	}
	jsonSchemaAssert := func(call goja.FunctionCall) goja.Value {
		ok, err := expectMatchesJSONSchema(runtime, actual, call.Argument(0), call.Argument(1))
		if err != nil {
			fail("JSON schema compile error: " + err.Error())
		}
		return checkCompare(ok, fmt.Sprintf("expected %s to match JSON schema", actual.String()), fmt.Sprintf("expected %s not to match JSON schema", actual.String()))
	}
	jsonBodyAssert := func(call goja.FunctionCall) goja.Value {
		ok := expectJSONBody(actual, call.Arguments)
		return checkCompare(ok, fmt.Sprintf("expected %s to match JSON body assertion", actual.String()), fmt.Sprintf("expected %s not to match JSON body assertion", actual.String()))
	}
	for _, name := range []string{"equal", "equals", "eq"} {
		_ = matcher.Set(name, strictEqual)
	}
	for _, name := range []string{"eql", "eqls"} {
		_ = matcher.Set(name, deepEqual)
	}
	for _, name := range []string{"contain", "contains", "include", "includes"} {
		_ = matcher.Set(name, contains)
	}
	for _, name := range []string{"match", "matches"} {
		_ = matcher.Set(name, matches)
	}
	_ = matcher.Set("above", func(call goja.FunctionCall) goja.Value {
		return numericCompare(call, "above", func(actualNumber, expectedNumber float64) bool { return actualNumber > expectedNumber })
	})
	_ = matcher.Set("greaterThan", func(call goja.FunctionCall) goja.Value {
		return numericCompare(call, "greater than", func(actualNumber, expectedNumber float64) bool { return actualNumber > expectedNumber })
	})
	_ = matcher.Set("gt", func(call goja.FunctionCall) goja.Value {
		return numericCompare(call, "greater than", func(actualNumber, expectedNumber float64) bool { return actualNumber > expectedNumber })
	})
	_ = matcher.Set("below", func(call goja.FunctionCall) goja.Value {
		return numericCompare(call, "below", func(actualNumber, expectedNumber float64) bool { return actualNumber < expectedNumber })
	})
	_ = matcher.Set("lessThan", func(call goja.FunctionCall) goja.Value {
		return numericCompare(call, "less than", func(actualNumber, expectedNumber float64) bool { return actualNumber < expectedNumber })
	})
	_ = matcher.Set("lt", func(call goja.FunctionCall) goja.Value {
		return numericCompare(call, "less than", func(actualNumber, expectedNumber float64) bool { return actualNumber < expectedNumber })
	})
	_ = matcher.Set("least", func(call goja.FunctionCall) goja.Value {
		return numericCompare(call, "at least", func(actualNumber, expectedNumber float64) bool { return actualNumber >= expectedNumber })
	})
	_ = matcher.Set("gte", func(call goja.FunctionCall) goja.Value {
		return numericCompare(call, "at least", func(actualNumber, expectedNumber float64) bool { return actualNumber >= expectedNumber })
	})
	_ = matcher.Set("most", func(call goja.FunctionCall) goja.Value {
		return numericCompare(call, "at most", func(actualNumber, expectedNumber float64) bool { return actualNumber <= expectedNumber })
	})
	_ = matcher.Set("lte", func(call goja.FunctionCall) goja.Value {
		return numericCompare(call, "at most", func(actualNumber, expectedNumber float64) bool { return actualNumber <= expectedNumber })
	})
	_ = matcher.Set("a", typeCheck)
	_ = matcher.Set("an", typeCheck)
	_ = matcher.Set("lengthOf", lengthOf)
	_ = matcher.Set("property", property)
	_ = matcher.Set("jsonSchema", jsonSchemaAssert)
	_ = matcher.Set("jsonBody", jsonBodyAssert)
	for _, name := range []string{"throw", "throws"} {
		_ = matcher.Set(name, throws)
	}

	deep := runtime.NewObject()
	for _, alias := range []string{"to", "be", "and", "that", "which"} {
		_ = deep.Set(alias, deep)
	}
	for _, name := range []string{"equal", "equals", "eql", "eqls"} {
		_ = deep.Set(name, deepEqual)
	}
	_ = matcher.Set("deep", deep)

	addGetter("true", func() (bool, string, string) {
		return actual.StrictEquals(runtime.ToValue(true)), fmt.Sprintf("expected %s to be true", actual.String()), fmt.Sprintf("expected %s not to be true", actual.String())
	})
	addGetter("false", func() (bool, string, string) {
		return actual.StrictEquals(runtime.ToValue(false)), fmt.Sprintf("expected %s to be false", actual.String()), fmt.Sprintf("expected %s not to be false", actual.String())
	})
	addGetter("null", func() (bool, string, string) {
		return goja.IsNull(actual), fmt.Sprintf("expected %s to be null", actual.String()), fmt.Sprintf("expected %s not to be null", actual.String())
	})
	addGetter("undefined", func() (bool, string, string) {
		return goja.IsUndefined(actual), fmt.Sprintf("expected %s to be undefined", actual.String()), fmt.Sprintf("expected %s not to be undefined", actual.String())
	})
	addGetter("ok", func() (bool, string, string) {
		return actual.ToBoolean(), fmt.Sprintf("expected %s to be truthy", actual.String()), fmt.Sprintf("expected %s not to be truthy", actual.String())
	})
	addGetter("exist", func() (bool, string, string) {
		ok := !goja.IsUndefined(actual) && !goja.IsNull(actual)
		return ok, fmt.Sprintf("expected %s to exist", actual.String()), fmt.Sprintf("expected %s not to exist", actual.String())
	})
	addGetter("exists", func() (bool, string, string) {
		ok := !goja.IsUndefined(actual) && !goja.IsNull(actual)
		return ok, fmt.Sprintf("expected %s to exist", actual.String()), fmt.Sprintf("expected %s not to exist", actual.String())
	})
	addGetter("empty", func() (bool, string, string) {
		return expectEmpty(runtime, actual), fmt.Sprintf("expected %s to be empty", actual.String()), fmt.Sprintf("expected %s not to be empty", actual.String())
	})
	addGetter("json", func() (bool, string, string) {
		return expectJSON(runtime, actual), fmt.Sprintf("expected %s to be JSON", actual.String()), fmt.Sprintf("expected %s not to be JSON", actual.String())
	})
	return matcher
}

func expectDeepEqual(actual, expected goja.Value) bool {
	if actual.StrictEquals(expected) {
		return true
	}
	actualExport := actual.Export()
	expectedExport := expected.Export()
	if reflect.DeepEqual(actualExport, expectedExport) {
		return true
	}
	actualJSON, actualErr := json.Marshal(actualExport)
	expectedJSON, expectedErr := json.Marshal(expectedExport)
	return actualErr == nil && expectedErr == nil && bytes.Equal(actualJSON, expectedJSON)
}

func expectContains(runtime *goja.Runtime, actual, expected goja.Value) bool {
	// The substring shortcut only applies when the actual value really is a
	// string. Every plain JavaScript object stringifies to "[object Object]", so
	// running this unconditionally made ANY object "contain" any other one —
	// expect({a:1}).to.contain({b:2}) passed, and so did
	// expect([{id:1}]).to.contain({id:999}). In a testing tool that is the worst
	// possible failure: the assertion reports green while checking nothing.
	if _, actualIsString := actual.Export().(string); actualIsString {
		if strings.Contains(actual.String(), expected.String()) {
			return true
		}
	}
	expectedExport := expected.Export()
	exported := actual.Export()
	switch typed := exported.(type) {
	case []interface{}:
		for _, item := range typed {
			if reflect.DeepEqual(item, expectedExport) || expectExportJSONEqual(item, expectedExport) {
				return true
			}
		}
	case []string:
		for _, item := range typed {
			if item == expected.String() {
				return true
			}
		}
	case map[string]interface{}:
		_, ok := typed[expected.String()]
		return ok
	case map[string]string:
		_, ok := typed[expected.String()]
		return ok
	}
	if expectType(runtime, actual, "array") {
		object := actual.ToObject(runtime)
		length, ok := expectLength(runtime, actual)
		if !ok {
			return false
		}
		for index := 0; index < length; index++ {
			if expectDeepEqual(object.Get(strconv.Itoa(index)), expected) {
				return true
			}
		}
	}
	return false
}

func expectExportJSONEqual(actual, expected interface{}) bool {
	actualJSON, actualErr := json.Marshal(actual)
	expectedJSON, expectedErr := json.Marshal(expected)
	return actualErr == nil && expectedErr == nil && bytes.Equal(actualJSON, expectedJSON)
}

func expectMatches(runtime *goja.Runtime, actual, expected goja.Value) (bool, error) {
	if fn, ok := goja.AssertFunction(expected.ToObject(runtime).Get("test")); ok {
		result, err := fn(expected, runtime.ToValue(actual.String()))
		if err != nil {
			return false, err
		}
		return result.ToBoolean(), nil
	}
	pattern := expected.String()
	compiled, err := regexp.Compile(pattern)
	if err != nil {
		return false, fmt.Errorf("invalid match pattern %q: %w", pattern, err)
	}
	return compiled.MatchString(actual.String()), nil
}

func expectNumber(value goja.Value) (float64, bool) {
	if !goja.IsNumber(value) || goja.IsNaN(value) {
		return 0, false
	}
	return value.ToFloat(), true
}

func expectLength(runtime *goja.Runtime, value goja.Value) (int, bool) {
	if goja.IsUndefined(value) || goja.IsNull(value) {
		return 0, false
	}
	if exported := value.Export(); exported != nil {
		reflected := reflect.ValueOf(exported)
		switch reflected.Kind() {
		case reflect.String, reflect.Array, reflect.Slice, reflect.Map:
			return reflected.Len(), true
		}
	}
	object := value.ToObject(runtime)
	length := object.Get("length")
	if goja.IsNumber(length) && !goja.IsNaN(length) {
		return int(length.ToInteger()), true
	}
	keys := object.Keys()
	if len(keys) > 0 && expectType(runtime, value, "object") {
		return len(keys), true
	}
	return 0, false
}

func expectProperty(runtime *goja.Runtime, actual goja.Value, name string) (goja.Value, bool) {
	if goja.IsUndefined(actual) || goja.IsNull(actual) {
		return goja.Undefined(), false
	}
	object := actual.ToObject(runtime)
	value := object.Get(name)
	// goja returns a NIL goja.Value for a property that does not exist, and
	// goja.IsUndefined(nil) is false — so checking only for undefined reported
	// every missing property as present, and
	// expect(body).to.have.property('anything') passed for any name at all.
	if value != nil && !goja.IsUndefined(value) {
		return value, true
	}
	// A property explicitly set to undefined IS present, and Get returns the
	// undefined singleton rather than nil for it. Keys() is what tells the two
	// apart.
	for _, key := range object.Keys() {
		if key == name {
			return value, true
		}
	}
	if value == nil {
		return goja.Undefined(), false
	}
	return value, false
}

func expectType(runtime *goja.Runtime, value goja.Value, expected string) bool {
	expected = strings.ToLower(strings.TrimSpace(expected))
	switch expected {
	case "string":
		_, ok := value.Export().(string)
		return ok
	case "number":
		return goja.IsNumber(value) && !goja.IsNaN(value)
	case "boolean", "bool":
		_, ok := value.Export().(bool)
		return ok
	case "function":
		_, ok := goja.AssertFunction(value)
		return ok
	case "array":
		return expectArray(runtime, value)
	case "object":
		if goja.IsUndefined(value) || goja.IsNull(value) || expectArray(runtime, value) {
			return false
		}
		if _, ok := goja.AssertFunction(value); ok {
			return false
		}
		exported := value.Export()
		if exported == nil {
			return false
		}
		switch reflect.ValueOf(exported).Kind() {
		case reflect.Map, reflect.Struct:
			return true
		default:
			return false
		}
	case "null":
		return goja.IsNull(value)
	case "undefined":
		return goja.IsUndefined(value)
	case "promise":
		then, ok := expectProperty(runtime, value, "then")
		if !ok {
			return false
		}
		_, callable := goja.AssertFunction(then)
		return callable
	default:
		return false
	}
}

func expectArray(runtime *goja.Runtime, value goja.Value) bool {
	arrayConstructor := runtime.Get("Array")
	if goja.IsUndefined(arrayConstructor) || goja.IsNull(arrayConstructor) {
		return false
	}
	isArray, ok := goja.AssertFunction(arrayConstructor.ToObject(runtime).Get("isArray"))
	if !ok {
		return false
	}
	result, err := isArray(goja.Undefined(), value)
	return err == nil && result.ToBoolean()
}

func expectEmpty(runtime *goja.Runtime, value goja.Value) bool {
	length, ok := expectLength(runtime, value)
	return ok && length == 0
}

func expectJSON(runtime *goja.Runtime, value goja.Value) bool {
	return expectArray(runtime, value) || expectType(runtime, value, "object")
}

func expectJSONBody(actual goja.Value, args []goja.Value) bool {
	actualValue, err := normalizeJSONValue(actual.Export())
	if err != nil {
		return false
	}
	if len(args) == 0 {
		return isJSONObjectOrArray(actualValue)
	}
	if len(args) == 1 && isJSONBodyObjectArgument(args[0]) {
		expectedValue, err := normalizeJSONValue(args[0].Export())
		if err != nil {
			return false
		}
		return expectExportJSONEqual(actualValue, expectedValue)
	}
	path := args[0].String()
	value, found := jsonBodyNestedValue(actualValue, path)
	if len(args) == 1 {
		return found
	}
	if !found {
		return false
	}
	expectedValue, err := normalizeJSONValue(args[1].Export())
	if err != nil {
		return false
	}
	return expectExportJSONEqual(value, expectedValue)
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

func parseJSONBodyPath(path string) []string {
	keys := []string{}
	for index := 0; index < len(path); {
		switch path[index] {
		case '.':
			index++
		case '[':
			index++
			if index < len(path) && (path[index] == '\'' || path[index] == '"') {
				quote := path[index]
				index++
				var key strings.Builder
				for index < len(path) && path[index] != quote {
					if path[index] == '\\' && index+1 < len(path) && path[index+1] == quote {
						key.WriteByte(quote)
						index += 2
						continue
					}
					key.WriteByte(path[index])
					index++
				}
				if index < len(path) {
					index++
				}
				if index < len(path) && path[index] == ']' {
					index++
				}
				keys = append(keys, key.String())
				continue
			}
			start := index
			for index < len(path) && path[index] != ']' {
				index++
			}
			keys = append(keys, path[start:index])
			if index < len(path) {
				index++
			}
		default:
			start := index
			for index < len(path) && path[index] != '.' && path[index] != '[' {
				index++
			}
			keys = append(keys, path[start:index])
		}
	}
	return keys
}

type scriptJSONSchemaOptions struct {
	CoerceTypes bool
	Strict      bool
}

func expectMatchesJSONSchema(runtime *goja.Runtime, actual, schemaValue, optionsValue goja.Value) (bool, error) {
	options := scriptJSONSchemaOptions{Strict: true}
	if optionsValue != nil && !goja.IsUndefined(optionsValue) && !goja.IsNull(optionsValue) && optionsValue.Export() != nil {
		optionsObject := optionsValue.ToObject(runtime)
		if optionsObject == nil {
			return false, errors.New("jsonSchema options must be an object")
		}
		if coerceTypes := optionsObject.Get("coerceTypes"); coerceTypes != nil && !goja.IsUndefined(coerceTypes) && !goja.IsNull(coerceTypes) {
			options.CoerceTypes = coerceTypes.ToBoolean()
		}
		if strict := optionsObject.Get("strict"); strict != nil && !goja.IsUndefined(strict) && !goja.IsNull(strict) {
			options.Strict = strict.ToBoolean()
		}
	}
	schemaDoc, err := normalizeJSONValue(schemaValue.Export())
	if err != nil {
		return false, err
	}
	if err := ensureSupportedJSONSchema(schemaDoc, options.Strict); err != nil {
		return false, err
	}
	data, err := normalizeJSONValue(actual.Export())
	if err != nil {
		return false, err
	}
	if options.CoerceTypes {
		data = coerceJSONSchemaValue(data, schemaDoc)
	}
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft7)
	compiler.AssertFormat()
	if err := compiler.AddResource("schema.json", schemaDoc); err != nil {
		return false, err
	}
	compiled, err := compiler.Compile("schema.json")
	if err != nil {
		return false, err
	}
	return compiled.Validate(data) == nil, nil
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

func ensureSupportedJSONSchema(schema interface{}, strict bool) error {
	schemaObject, ok := schema.(map[string]interface{})
	if !ok {
		return nil
	}
	if rawSchema, ok := schemaObject["$schema"].(string); ok && rawSchema != "" {
		if rawSchema != "http://json-schema.org/draft-07/schema#" && rawSchema != "http://json-schema.org/draft-07/schema" {
			return fmt.Errorf("unsupported JSON Schema version: %q", rawSchema)
		}
	}
	if strict {
		if err := validateJSONSchemaKeywords(schema, ""); err != nil {
			return err
		}
	}
	return nil
}

func validateJSONSchemaKeywords(schema interface{}, path string) error {
	schemaObject, ok := schema.(map[string]interface{})
	if !ok {
		return nil
	}
	for keyword, value := range schemaObject {
		if !jsonSchemaKeywordAllowed(keyword) {
			return fmt.Errorf("unknown keyword %q", keyword)
		}
		switch keyword {
		case "properties", "patternProperties", "definitions", "$defs":
			children, _ := value.(map[string]interface{})
			for _, child := range children {
				if err := validateJSONSchemaKeywords(child, path+"/"+keyword); err != nil {
					return err
				}
			}
		case "items", "additionalItems", "additionalProperties", "propertyNames", "contains", "if", "then", "else", "not":
			if err := validateJSONSchemaKeywords(value, path+"/"+keyword); err != nil {
				return err
			}
		case "allOf", "anyOf", "oneOf":
			children, _ := value.([]interface{})
			for _, child := range children {
				if err := validateJSONSchemaKeywords(child, path+"/"+keyword); err != nil {
					return err
				}
			}
		case "dependencies":
			children, _ := value.(map[string]interface{})
			for _, child := range children {
				if _, ok := child.([]interface{}); ok {
					continue
				}
				if err := validateJSONSchemaKeywords(child, path+"/"+keyword); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func jsonSchemaKeywordAllowed(keyword string) bool {
	switch keyword {
	case "$schema", "$id", "id", "$ref", "$comment", "title", "description", "default", "examples",
		"type", "enum", "const", "multipleOf", "maximum", "exclusiveMaximum", "minimum", "exclusiveMinimum",
		"maxLength", "minLength", "pattern", "format", "contentMediaType", "contentEncoding",
		"items", "additionalItems", "maxItems", "minItems", "uniqueItems", "contains",
		"maxProperties", "minProperties", "required", "properties", "patternProperties", "additionalProperties", "dependencies", "propertyNames",
		"if", "then", "else", "allOf", "anyOf", "oneOf", "not",
		"definitions", "$defs", "readOnly", "writeOnly", "nullable":
		return true
	default:
		return false
	}
}

func coerceJSONSchemaValue(value interface{}, schema interface{}) interface{} {
	schemaObject, ok := schema.(map[string]interface{})
	if !ok {
		return value
	}
	switch schemaType := schemaObject["type"].(type) {
	case string:
		value = coerceJSONSchemaScalar(value, schemaType)
	case []interface{}:
		for _, rawType := range schemaType {
			if typeName, ok := rawType.(string); ok && jsonSchemaTypeMatches(value, typeName) {
				return value
			}
		}
		for _, rawType := range schemaType {
			typeName, ok := rawType.(string)
			if !ok {
				continue
			}
			coerced := coerceJSONSchemaScalar(value, typeName)
			if jsonSchemaTypeMatches(coerced, typeName) {
				value = coerced
				break
			}
		}
	}
	switch typed := value.(type) {
	case map[string]interface{}:
		properties, _ := schemaObject["properties"].(map[string]interface{})
		for name, propertySchema := range properties {
			if propertyValue, ok := typed[name]; ok {
				typed[name] = coerceJSONSchemaValue(propertyValue, propertySchema)
			}
		}
	case []interface{}:
		itemSchema := schemaObject["items"]
		for index, item := range typed {
			typed[index] = coerceJSONSchemaValue(item, itemSchema)
		}
	}
	return value
}

func coerceJSONSchemaScalar(value interface{}, schemaType string) interface{} {
	switch schemaType {
	case "integer":
		if text, ok := value.(string); ok {
			parsed, err := strconv.ParseInt(strings.TrimSpace(text), 10, 64)
			if err == nil {
				return float64(parsed)
			}
		}
	case "number":
		if text, ok := value.(string); ok {
			parsed, err := strconv.ParseFloat(strings.TrimSpace(text), 64)
			if err == nil {
				return parsed
			}
		}
	case "string":
		switch typed := value.(type) {
		case float64:
			return strconv.FormatFloat(typed, 'f', -1, 64)
		case bool:
			return strconv.FormatBool(typed)
		}
	case "boolean":
		if text, ok := value.(string); ok {
			parsed, err := strconv.ParseBool(strings.TrimSpace(text))
			if err == nil {
				return parsed
			}
		}
	}
	return value
}

func jsonSchemaTypeMatches(value interface{}, schemaType string) bool {
	switch schemaType {
	case "null":
		return value == nil
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "string":
		_, ok := value.(string)
		return ok
	case "number":
		_, ok := value.(float64)
		return ok
	case "integer":
		number, ok := value.(float64)
		return ok && math.Trunc(number) == number
	case "object":
		_, ok := value.(map[string]interface{})
		return ok
	case "array":
		_, ok := value.([]interface{})
		return ok
	default:
		return false
	}
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

func installScriptProcess(runtime *goja.Runtime, loop *scriptEventLoop, collectionPath string, processEnv map[string]string, sandboxMode string) {
	_ = runtime.Set("global", runtime.GlobalObject())
	if NormalizeJSSandboxMode(sandboxMode) != "developer" {
		return
	}
	processObject := runtime.NewObject()
	_ = processObject.Set("version", "v20.0.0")
	_ = processObject.Set("versions", map[string]string{"node": "20.0.0"})
	_ = processObject.Set("platform", scriptNodePlatform())
	_ = processObject.Set("arch", scriptNodeArch())
	_ = processObject.Set("env", scriptProcessEnv(processEnv))
	_ = processObject.Set("cwd", func() string { return collectionPath })
	_ = processObject.Set("nextTick", func(call goja.FunctionCall) goja.Value {
		if loop != nil {
			loop.queueNextTick(call.Argument(0), call.Arguments[1:]...)
		}
		return goja.Undefined()
	})
	_ = runtime.Set("process", processObject)
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

func installScriptFetch(runtime *goja.Runtime, vars map[string]string) {
	_ = runtime.Set("__liteApiFetchSend", func(call goja.FunctionCall) goja.Value {
		responseValue, errorValue, _, err := scriptSendRequest(runtime, call.Argument(0), vars)
		if err != nil {
			panic(runtime.NewGoError(err))
		}
		if responseValue != nil {
			return responseValue
		}
		if errorValue != nil {
			errorObject := errorValue.ToObject(runtime)
			response := errorObject.Get("response")
			if response != nil && !goja.IsUndefined(response) && !goja.IsNull(response) {
				return response
			}
			message := errorObject.Get("message")
			if message != nil && !goja.IsUndefined(message) && !goja.IsNull(message) {
				panic(runtime.NewGoError(errors.New(message.String())))
			}
		}
		panic(runtime.NewGoError(errors.New("fetch failed")))
	})
	script := `(function () {
  const send = globalThis.__liteApiFetchSend;
  const textEncoder = new TextEncoder();

  function normalizeName(name) {
    const value = String(name).toLowerCase();
    if (!value) throw new TypeError("Header name must not be empty");
    return value;
  }

  function normalizeValue(value) {
    return String(value);
  }

  class Headers {
    constructor(init) {
      Object.defineProperty(this, "_values", { value: {}, enumerable: false, writable: true });
      if (init === undefined || init === null) return;
      if (init instanceof Headers) {
        init.forEach((value, name) => this.append(name, value));
      } else if (Array.isArray(init)) {
        for (const pair of init) {
          if (!pair || pair.length < 2) throw new TypeError("Headers pair must be [name, value]");
          this.append(pair[0], pair[1]);
        }
      } else if (typeof init === "object") {
        for (const key of Object.keys(init)) this.append(key, init[key]);
      }
    }
    append(name, value) {
      const key = normalizeName(name);
      const next = normalizeValue(value);
      this._values[key] = this._values[key] ? this._values[key] + ", " + next : next;
    }
    set(name, value) {
      this._values[normalizeName(name)] = normalizeValue(value);
    }
    get(name) {
      const key = normalizeName(name);
      return Object.prototype.hasOwnProperty.call(this._values, key) ? this._values[key] : null;
    }
    has(name) {
      return Object.prototype.hasOwnProperty.call(this._values, normalizeName(name));
    }
    delete(name) {
      delete this._values[normalizeName(name)];
    }
    forEach(callback, thisArg) {
      for (const key of Object.keys(this._values).sort()) callback.call(thisArg, this._values[key], key, this);
    }
    entries() {
      return Object.keys(this._values).sort().map((key) => [key, this._values[key]])[Symbol.iterator]();
    }
    keys() {
      return Object.keys(this._values).sort()[Symbol.iterator]();
    }
    values() {
      return Object.keys(this._values).sort().map((key) => this._values[key])[Symbol.iterator]();
    }
    toObject() {
      const out = {};
      for (const key of Object.keys(this._values)) out[key] = this._values[key];
      return out;
    }
    [Symbol.iterator]() {
      return this.entries();
    }
  }

  function bytesFromPart(part) {
    if (part === undefined || part === null) return [];
    if (part instanceof Blob) return part._bytes.slice();
    if (Array.isArray(part)) return part.map((value) => Number(value) & 255);
    if (part instanceof ArrayBuffer) return Array.from(new Uint8Array(part));
    if (ArrayBuffer.isView(part)) return Array.from(new Uint8Array(part.buffer, part.byteOffset, part.byteLength));
    return Array.from(textEncoder.encode(String(part)));
  }

  function stringFromBytes(bytes) {
    let encoded = "";
    for (let i = 0; i < bytes.length; i++) {
      const value = Number(bytes[i]) & 255;
      encoded += value < 0x80 ? String.fromCharCode(value) : "%" + value.toString(16).padStart(2, "0").toUpperCase();
    }
    try {
      return decodeURIComponent(encoded);
    } catch (_) {
      return bytes.map((value) => String.fromCharCode(Number(value) & 255)).join("");
    }
  }

  function arrayBufferFromBytes(bytes) {
    const buffer = new ArrayBuffer(bytes.length);
    new Uint8Array(buffer).set(bytes);
    return buffer;
  }

  class Blob {
    constructor(parts, options) {
      const bytes = [];
      for (const part of parts || []) bytes.push.apply(bytes, bytesFromPart(part));
      Object.defineProperty(this, "_bytes", { value: bytes, enumerable: false, writable: false });
      this.size = bytes.length;
      this.type = options && options.type ? String(options.type).toLowerCase() : "";
    }
    text() {
      return Promise.resolve(stringFromBytes(this._bytes));
    }
    arrayBuffer() {
      return Promise.resolve(arrayBufferFromBytes(this._bytes));
    }
    slice(start, end, type) {
      const size = this._bytes.length;
      const begin = start === undefined ? 0 : Math.max(0, start < 0 ? size + Number(start) : Number(start));
      const finish = end === undefined ? size : Math.max(begin, end < 0 ? size + Number(end) : Number(end));
      return new Blob([new Uint8Array(this._bytes.slice(begin, Math.min(size, finish)))], { type });
    }
  }

  class FormData {
    constructor() {
      Object.defineProperty(this, "_entries", { value: [], enumerable: false, writable: true });
    }
    append(name, value) {
      this._entries.push([String(name), value]);
    }
    set(name, value) {
      this.delete(name);
      this.append(name, value);
    }
    get(name) {
      const key = String(name);
      const pair = this._entries.find((item) => item[0] === key);
      return pair ? pair[1] : null;
    }
    getAll(name) {
      const key = String(name);
      return this._entries.filter((item) => item[0] === key).map((item) => item[1]);
    }
    has(name) {
      const key = String(name);
      return this._entries.some((item) => item[0] === key);
    }
    delete(name) {
      const key = String(name);
      this._entries = this._entries.filter((item) => item[0] !== key);
    }
    entries() {
      return this._entries.slice()[Symbol.iterator]();
    }
    keys() {
      return this._entries.map((item) => item[0])[Symbol.iterator]();
    }
    values() {
      return this._entries.map((item) => item[1])[Symbol.iterator]();
    }
    forEach(callback, thisArg) {
      for (const pair of this._entries) callback.call(thisArg, pair[1], pair[0], this);
    }
    [Symbol.iterator]() {
      return this.entries();
    }
  }

  class BodyHolder {
    _initBody(body) {
      this.bodyUsed = false;
      this._bodyBytes = bytesFromPart(body === undefined ? "" : body);
    }
    _consume() {
      if (this.bodyUsed) return Promise.reject(new TypeError("Body has already been consumed"));
      this.bodyUsed = true;
      return Promise.resolve(this._bodyBytes.slice());
    }
    text() {
      return this._consume().then((bytes) => stringFromBytes(bytes));
    }
    json() {
      return this.text().then((text) => JSON.parse(text));
    }
    arrayBuffer() {
      return this._consume().then((bytes) => arrayBufferFromBytes(bytes));
    }
    blob() {
      return this._consume().then((bytes) => new Blob([new Uint8Array(bytes)], { type: this.headers && this.headers.get("content-type") || "" }));
    }
  }

  class Request extends BodyHolder {
    constructor(input, init) {
      super();
      init = init || {};
      const source = input instanceof Request ? input : null;
      this.url = source ? source.url : String(input);
      this.method = String(init.method || (source && source.method) || "GET").toUpperCase();
      this.headers = new Headers(init.headers || (source && source.headers) || undefined);
      this.signal = init.signal || (source && source.signal) || null;
      this._initBody(init.body !== undefined ? init.body : (source ? source._bodyBytes : ""));
    }
    clone() {
      return new Request(this, { body: new Uint8Array(this._bodyBytes) });
    }
  }

  class Response extends BodyHolder {
    constructor(body, init) {
      super();
      init = init || {};
      this.status = init.status === undefined ? 200 : Number(init.status);
      this.statusText = init.statusText === undefined ? "" : String(init.statusText);
      this.headers = new Headers(init.headers);
      this.ok = this.status >= 200 && this.status <= 299;
      this.url = init.url || "";
      this._initBody(body === undefined ? "" : body);
    }
    clone() {
      return new Response(new Uint8Array(this._bodyBytes), {
        status: this.status,
        statusText: this.statusText,
        headers: this.headers,
        url: this.url
      });
    }
    static json(value, init) {
      init = init || {};
      const headers = new Headers(init.headers);
      if (!headers.has("content-type")) headers.set("content-type", "application/json");
      init.headers = headers;
      return new Response(JSON.stringify(value), init);
    }
  }

  class AbortSignal extends EventTarget {
    constructor() {
      super();
      this.aborted = false;
      this.reason = undefined;
    }
    throwIfAborted() {
      if (this.aborted) throw this.reason || new Error("This operation was aborted");
    }
  }

  class AbortController {
    constructor() {
      this.signal = new AbortSignal();
    }
    abort(reason) {
      if (this.signal.aborted) return;
      this.signal.aborted = true;
      this.signal.reason = reason === undefined ? new Error("This operation was aborted") : reason;
      this.signal.dispatchEvent(new Event("abort"));
    }
  }

  function bodyForFetch(request) {
    if (!request._bodyBytes || request._bodyBytes.length === 0) return undefined;
    return stringFromBytes(request._bodyBytes);
  }

  function fetch(input, init) {
    const request = new Request(input, init);
    if (request.signal && request.signal.aborted) {
      return Promise.reject(request.signal.reason || new Error("This operation was aborted"));
    }
    const config = {
      url: request.url,
      method: request.method,
      headers: request.headers.toObject()
    };
    const body = bodyForFetch(request);
    if (body !== undefined) config.body = body;
    try {
      const res = send(config);
      return Promise.resolve(new Response(res.body || "", {
        status: Number(res.status || res.statusCode || 0),
        statusText: res.statusText || "",
        headers: res.headers || {},
        url: res.url || request.url
      }));
    } catch (err) {
      return Promise.reject(err);
    }
  }

  globalThis.Headers = Headers;
  globalThis.Request = Request;
  globalThis.Response = Response;
  globalThis.AbortController = AbortController;
  globalThis.AbortSignal = AbortSignal;
  globalThis.FormData = FormData;
  globalThis.Blob = Blob;
  globalThis.fetch = fetch;
})()`
	if _, err := runtime.RunProgram(scriptFetchShim.compiled(script)); err != nil {
		panic(runtime.NewGoError(err))
	}
	_ = runtime.Set("__liteApiFetchSend", goja.Undefined())
}

func newScriptNodeFetchModule(runtime *goja.Runtime) goja.Value {
	fetch := runtime.Get("fetch")
	if fetch == nil || goja.IsUndefined(fetch) || goja.IsNull(fetch) {
		fetch = goja.Undefined()
	}
	fetchObject := fetch.ToObject(runtime)
	for _, name := range []string{"Headers", "Request", "Response", "AbortController", "AbortSignal", "FormData", "Blob"} {
		_ = fetchObject.Set(name, runtime.Get(name))
	}
	_ = fetchObject.Set("default", fetch)
	return fetch
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

func (loop *scriptEventLoop) scheduleTimer(callbackValue, delayValue goja.Value, repeat bool, args ...goja.Value) goja.Value {
	promise, resolve, reject := loop.runtime.NewPromise()
	callback, ok := goja.AssertFunction(callbackValue)
	if !ok {
		if err := reject(loop.runtime.NewTypeError("setTimeout callback must be a function")); err != nil {
			panic(err)
		}
		return loop.runtime.ToValue(promise)
	}
	loop.nextID++
	id := loop.nextID
	delay := scriptTimerDelay(delayValue)
	loop.timers[id] = &scriptTimer{
		id:       id,
		callback: callback,
		args:     append([]goja.Value(nil), args...),
		due:      time.Now().Add(delay),
		delay:    delay,
		repeat:   repeat,
		resolve:  resolve,
		reject:   reject,
		promise:  promise,
	}
	return loop.timerHandle(id, promise)
}

func (loop *scriptEventLoop) clearTimer(value goja.Value) {
	id := scriptTimerID(loop.runtime, value)
	if id == 0 {
		return
	}
	delete(loop.timers, id)
}

func scriptTimerID(runtime *goja.Runtime, value goja.Value) int64 {
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		return 0
	}
	if object := value.ToObject(runtime); object != nil {
		idValue := object.Get("__bruTimerID")
		if idValue != nil && !goja.IsUndefined(idValue) && !goja.IsNull(idValue) {
			return idValue.ToInteger()
		}
	}
	return value.ToInteger()
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

func scriptTimerDelay(value goja.Value) time.Duration {
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		return 0
	}
	ms := value.ToInteger()
	if ms < 0 {
		ms = 0
	}
	return time.Duration(ms) * time.Millisecond
}

func (loop *scriptEventLoop) addPendingTest() {
	loop.pendingTests++
}

func (loop *scriptEventLoop) finishPendingTest() {
	if loop.pendingTests > 0 {
		loop.pendingTests--
	}
}

func (loop *scriptEventLoop) nextTimer() *scriptTimer {
	var next *scriptTimer
	for _, timer := range loop.timers {
		if next == nil || timer.due.Before(next.due) || (timer.due.Equal(next.due) && timer.id < next.id) {
			next = timer
		}
	}
	return next
}

func (loop *scriptEventLoop) runNextTimer(deadline time.Time) error {
	timer := loop.nextTimer()
	if timer == nil {
		return nil
	}
	if wait := time.Until(timer.due); wait > 0 {
		if remaining := time.Until(deadline); remaining <= 0 {
			return errors.New("script timeout")
		} else if wait > remaining {
			wait = remaining
		}
		time.Sleep(wait)
	}
	if time.Now().After(deadline) {
		return errors.New("script timeout")
	}
	current, ok := loop.timers[timer.id]
	if !ok {
		return nil
	}
	if !current.repeat {
		delete(loop.timers, current.id)
	}
	if _, err := current.callback(goja.Undefined(), current.args...); err != nil {
		delete(loop.timers, current.id)
		if current.reject != nil {
			if rejectErr := current.reject(err); rejectErr != nil {
				return rejectErr
			}
		}
		return err
	}
	if current.resolve != nil {
		if resolveErr := current.resolve(goja.Undefined()); resolveErr != nil {
			return resolveErr
		}
	}
	if current.repeat {
		if _, ok := loop.timers[current.id]; ok {
			current.due = time.Now().Add(current.delay)
		}
	}
	return nil
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
	if bodyValue != nil && !goja.IsUndefined(bodyValue) && bodyValue.Export() != nil {
		applyScriptedBody(item, bodyValue.Export(), state.headers)
	}
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

func scriptBodyIsFormURLEncoded(item types.RequestItem, headers []types.KeyValue) bool {
	if item.Body.Mode == "formUrlEncoded" {
		return true
	}
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(types.GetKeyValue(headers, "Content-Type"))), "application/x-www-form-urlencoded")
}

func scriptFormURLEncodedValue(runtime *goja.Runtime, value goja.Value) string {
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		return ""
	}
	if exported, ok := value.Export().(string); ok {
		return exported
	}
	object := value.ToObject(runtime)
	if scriptValueIsArray(value) {
		rows := []string{}
		length := int(object.Get("length").ToInteger())
		for index := 0; index < length; index++ {
			row := object.Get(strconv.Itoa(index))
			if row == nil || goja.IsUndefined(row) || goja.IsNull(row) {
				continue
			}
			rowObject := row.ToObject(runtime)
			rows = append(rows, scriptFormPair(rowObject.Get("name"), rowObject.Get("value")))
		}
		return strings.Join(rows, "&")
	}
	rows := []string{}
	for _, key := range object.Keys() {
		field := object.Get(key)
		if scriptValueIsArray(field) {
			fieldObject := field.ToObject(runtime)
			length := int(fieldObject.Get("length").ToInteger())
			for index := 0; index < length; index++ {
				rows = append(rows, scriptFormPair(runtime.ToValue(key), fieldObject.Get(strconv.Itoa(index))))
			}
			continue
		}
		rows = append(rows, scriptFormPair(runtime.ToValue(key), field))
	}
	return strings.Join(rows, "&")
}

func scriptFormURLEncodedBody(value interface{}) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case []interface{}:
		rows := make([]string, 0, len(typed))
		for _, item := range typed {
			data, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			rows = append(rows, scriptFormPairFromStrings(scriptFormInterfaceString(data["name"]), scriptFormInterfaceString(data["value"])))
		}
		return strings.Join(rows, "&")
	case map[string]interface{}:
		rows := make([]string, 0, len(typed))
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			value := typed[key]
			if items, ok := value.([]interface{}); ok {
				for _, item := range items {
					rows = append(rows, scriptFormPairFromStrings(key, scriptFormInterfaceString(item)))
				}
				continue
			}
			rows = append(rows, scriptFormPairFromStrings(key, scriptFormInterfaceString(value)))
		}
		return strings.Join(rows, "&")
	default:
		return fmt.Sprint(typed)
	}
}

func scriptValueIsArray(value goja.Value) bool {
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		return false
	}
	_, ok := value.Export().([]interface{})
	return ok
}

func scriptFormPair(name, value goja.Value) string {
	return scriptFormPairFromStrings(scriptFormValueString(name), scriptFormValueString(value))
}

func scriptFormPairFromStrings(name, value string) string {
	return scriptFormEncodeComponent(name) + "=" + scriptFormEncodeComponent(value)
}

func scriptFormValueString(value goja.Value) string {
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		return ""
	}
	exported := value.Export()
	if exported == nil {
		return ""
	}
	return fmt.Sprint(exported)
}

func scriptFormInterfaceString(value interface{}) string {
	if value == nil {
		return ""
	}
	return fmt.Sprint(value)
}

func scriptFormEncodeComponent(value string) string {
	return strings.ReplaceAll(url.QueryEscape(value), "+", "%20")
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

func RunRequestOnFail(state *RequestState, requestErr error) error {
	if state == nil || state.runtime == nil || state.onFail == nil {
		return nil
	}
	fn, ok := goja.AssertFunction(state.onFail)
	if !ok {
		return nil
	}
	errObject := state.runtime.NewObject()
	_ = errObject.Set("message", requestErr.Error())
	_ = errObject.Set("error", requestErr.Error())
	_, err := fn(goja.Undefined(), errObject)
	return err
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

func scriptHeadersToKeyValues(runtime *goja.Runtime, value goja.Value) []types.KeyValue {
	result := []types.KeyValue{}
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		return result
	}
	object := value.ToObject(runtime)
	for _, key := range object.Keys() {
		headerValue := object.Get(key)
		if goja.IsUndefined(headerValue) || goja.IsNull(headerValue) {
			continue
		}
		result = append(result, types.KeyValue{Name: key, Value: headerValue.String(), Enabled: true})
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

func deleteKeyValue(values []types.KeyValue, name string) []types.KeyValue {
	next := values[:0]
	for _, value := range values {
		if !strings.EqualFold(value.Name, name) {
			next = append(next, value)
		}
	}
	return next
}

func getHeaderValue(headers map[string]string, name string) string {
	for key, value := range headers {
		if strings.EqualFold(key, name) {
			return value
		}
	}
	return ""
}

func responseJSONValue(body string) (interface{}, bool) {
	var value interface{}
	if err := json.Unmarshal([]byte(body), &value); err != nil {
		return nil, false
	}
	return value, true
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

func scriptResponseQuery(runtime *goja.Runtime, data interface{}, call goja.FunctionCall) (interface{}, bool) {
	if len(call.Arguments) == 0 || goja.IsUndefined(call.Argument(0)) || goja.IsNull(call.Argument(0)) {
		return data, true
	}
	expr := strings.TrimSpace(call.Argument(0).String())
	if expr == "" {
		return data, true
	}
	var filter goja.Callable
	for _, arg := range call.Arguments[1:] {
		if fn, ok := goja.AssertFunction(arg); ok {
			filter = fn
			break
		}
	}
	selection := []interface{}{data}
	for index := 0; index < len(expr); {
		switch {
		case strings.HasPrefix(expr[index:], ".."):
			index += 2
			name, next := readResponseQueryName(expr, index)
			index = next
			if name == "" {
				return nil, false
			}
			selection = recursiveResponseQueryValues(selection, name)
		case expr[index] == '.':
			index++
			name, next := readResponseQueryName(expr, index)
			index = next
			if name == "" {
				return nil, false
			}
			selection = responseQueryProperty(selection, name)
		case expr[index] == '[':
			end := strings.IndexByte(expr[index:], ']')
			if end < 0 {
				return nil, false
			}
			token := strings.TrimSpace(expr[index+1 : index+end])
			index += end + 1
			if token == "?" {
				selection = responseQueryFilter(runtime, selection, filter)
				continue
			}
			itemIndex, err := strconv.Atoi(token)
			if err != nil {
				token = strings.Trim(token, `"'`)
				selection = responseQueryProperty(selection, token)
				continue
			}
			value, ok := responseQueryIndex(selection, itemIndex)
			if !ok {
				return nil, false
			}
			selection = []interface{}{value}
		default:
			name, next := readResponseQueryName(expr, index)
			index = next
			if name == "" {
				return nil, false
			}
			selection = responseQueryProperty(selection, name)
		}
	}
	return responseQueryResult(selection)
}

func readResponseQueryName(expr string, index int) (string, int) {
	start := index
	for index < len(expr) && expr[index] != '.' && expr[index] != '[' {
		index++
	}
	return strings.TrimSpace(expr[start:index]), index
}

func recursiveResponseQueryValues(values []interface{}, name string) []interface{} {
	result := []interface{}{}
	var walk func(interface{})
	walk = func(value interface{}) {
		switch typed := value.(type) {
		case map[string]interface{}:
			for key, child := range typed {
				if key == name {
					result = append(result, child)
				}
				walk(child)
			}
		case []interface{}:
			for _, child := range typed {
				walk(child)
			}
		}
	}
	for _, value := range values {
		walk(value)
	}
	return result
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

func scriptResponseJQ(data interface{}, expr string) (interface{}, bool) {
	current := data
	for _, segment := range splitResponseJQSegments(expr) {
		name, filter := parseResponseJQSegment(segment)
		if name != "" {
			next, ok := responseJQProperty(current, name)
			if !ok {
				return nil, false
			}
			current = next
		}
		if filter != "" {
			list, ok := current.([]interface{})
			if !ok {
				return nil, false
			}
			filtered := []interface{}{}
			for _, item := range list {
				if responseJQMatchesFilter(item, filter) {
					filtered = append(filtered, item)
				}
			}
			current = filtered
		}
	}
	if list, ok := current.([]interface{}); ok && len(list) == 1 {
		return list[0], true
	}
	return current, true
}

func splitResponseJQSegments(expr string) []string {
	segments := []string{}
	start := 0
	depth := 0
	for index := 0; index < len(expr); index++ {
		switch expr[index] {
		case '[':
			depth++
		case ']':
			if depth > 0 {
				depth--
			}
		case '.':
			if depth == 0 {
				segments = append(segments, strings.TrimSpace(expr[start:index]))
				start = index + 1
			}
		}
	}
	segments = append(segments, strings.TrimSpace(expr[start:]))
	return segments
}

func parseResponseJQSegment(segment string) (string, string) {
	open := strings.IndexByte(segment, '[')
	if open < 0 || !strings.HasSuffix(segment, "]") {
		return strings.TrimSpace(segment), ""
	}
	return strings.TrimSpace(segment[:open]), strings.TrimSpace(segment[open+1 : len(segment)-1])
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

func parseResponseJQLiteral(value string) interface{} {
	value = strings.TrimSpace(value)
	if len(value) >= 2 && ((value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'')) {
		return value[1 : len(value)-1]
	}
	if number, err := strconv.ParseFloat(value, 64); err == nil {
		return number
	}
	switch strings.ToLower(value) {
	case "true":
		return true
	case "false":
		return false
	default:
		return value
	}
}

func compareResponseJQValues(actual interface{}, operator string, expected interface{}) bool {
	actualNumber, actualNumberOK := numericInterface(actual)
	expectedNumber, expectedNumberOK := numericInterface(expected)
	if actualNumberOK && expectedNumberOK {
		switch operator {
		case ">":
			return actualNumber > expectedNumber
		case "<":
			return actualNumber < expectedNumber
		case ">=":
			return actualNumber >= expectedNumber
		case "<=":
			return actualNumber <= expectedNumber
		case "=", "==":
			return actualNumber == expectedNumber
		case "!=":
			return actualNumber != expectedNumber
		}
	}
	actualText := fmt.Sprint(actual)
	expectedText := fmt.Sprint(expected)
	switch operator {
	case "=", "==":
		return actualText == expectedText
	case "!=":
		return actualText != expectedText
	default:
		return false
	}
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

func scriptResponseHeaders(headers map[string]string) map[string]string {
	result := map[string]string{}
	for name, value := range headers {
		result[strings.ToLower(name)] = value
	}
	return result
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

func EncodeRequestURL(rawURL string) string {
	if rawURL == "" {
		return rawURL
	}
	queryIndex := strings.Index(rawURL, "?")
	originAndPath := rawURL
	queryString := ""
	if queryIndex >= 0 {
		originAndPath = rawURL[:queryIndex]
		queryString = rawURL[queryIndex+1:]
	}
	origin, path := splitURLOriginAndPath(originAndPath)
	result := origin + encodeURLPath(path)
	if queryIndex >= 0 {
		result += "?" + encodeURLQuery(queryString)
	}
	return result
}

func splitURLOriginAndPath(value string) (string, string) {
	schemeEnd := strings.Index(value, "://")
	if schemeEnd <= 0 || !validURLScheme(value[:schemeEnd]) {
		return "", value
	}
	authorityStart := schemeEnd + len("://")
	authorityEnd := len(value)
	for i := authorityStart; i < len(value); i++ {
		if value[i] == '/' || value[i] == '?' || value[i] == '#' {
			authorityEnd = i
			break
		}
	}
	return value[:authorityEnd], value[authorityEnd:]
}

func validURLScheme(scheme string) bool {
	if scheme == "" {
		return false
	}
	for i := 0; i < len(scheme); i++ {
		c := scheme[i]
		isAlpha := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
		if i == 0 {
			if !isAlpha {
				return false
			}
			continue
		}
		if !isAlpha && (c < '0' || c > '9') && c != '+' && c != '-' && c != '.' {
			return false
		}
	}
	return true
}

func encodeURLPath(path string) string {
	if path == "" {
		return ""
	}
	segments := strings.Split(path, "/")
	for i, segment := range segments {
		segments[i] = encodeURIComponent(safeDecodeURIComponent(segment))
	}
	return strings.Join(segments, "/")
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

func encodeURLQuery(query string) string {
	if query == "" {
		return ""
	}
	pairs := strings.Split(query, "&")
	encodedPairs := make([]string, 0, len(pairs))
	for _, pair := range pairs {
		name, value, hasValue := strings.Cut(pair, "=")
		if name == "" {
			continue
		}
		encodedName := encodeURIComponent(name)
		if hasValue {
			encodedPairs = append(encodedPairs, encodedName+"="+encodeURIComponent(value))
		} else {
			encodedPairs = append(encodedPairs, encodedName)
		}
	}
	return strings.Join(encodedPairs, "&")
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

func ProcessEnvForCollection(collection *types.Collection, workspacePath string) map[string]string {
	env := scriptProcessEnv(nil)
	if collection == nil {
		return env
	}
	if strings.TrimSpace(workspacePath) == "" && strings.TrimSpace(collection.Path) != "" {
		workspacePath = filepath.Dir(filepath.Clean(collection.Path))
	}
	mergeStringMap(env, readDotEnvFile(filepath.Join(workspacePath, ".env")))
	mergeStringMap(env, readDotEnvFile(filepath.Join(collection.Path, ".env")))
	return env
}

func DotEnvFilesForContext(workspace *types.Workspace, collection *types.Collection) ([]types.DotEnvFile, error) {
	files := []types.DotEnvFile{}
	if workspace != nil {
		workspaceFiles, err := dotEnvFilesInScope("workspace", workspace.Path)
		if err != nil {
			return nil, err
		}
		files = append(files, workspaceFiles...)
	}
	if collection != nil {
		collectionFiles, err := dotEnvFilesInScope("collection", collection.Path)
		if err != nil {
			return nil, err
		}
		files = append(files, collectionFiles...)
	}
	sort.SliceStable(files, func(i, j int) bool {
		if files[i].Scope != files[j].Scope {
			return files[i].Scope == "workspace"
		}
		if files[i].Runtime != files[j].Runtime {
			return files[i].Runtime
		}
		return strings.ToLower(files[i].Name) < strings.ToLower(files[j].Name)
	})
	return files, nil
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

func DotEnvScopePath(workspace *types.Workspace, collection *types.Collection, scope string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(scope)) {
	case "workspace":
		if workspace == nil || strings.TrimSpace(workspace.Path) == "" {
			return "", errors.New("workspace path is required")
		}
		return workspace.Path, nil
	case "collection":
		if collection == nil || strings.TrimSpace(collection.Path) == "" {
			return "", errors.New("collection path is required")
		}
		return collection.Path, nil
	default:
		return "", errors.New(".env scope must be workspace or collection")
	}
}

func NormalizeDotEnvFilename(name string) (string, error) {
	name = strings.TrimSpace(name)
	if !isDotEnvFilename(name) || filepath.Base(name) != name || strings.ContainsAny(name, `/\`) {
		return "", errors.New(".env file name must be .env or .env.<name>")
	}
	return name, nil
}

func isDotEnvFilename(name string) bool {
	return dotEnvFilenamePattern.MatchString(name)
}

func addProcessEnvVars(vars map[string]string, processEnv map[string]string) {
	for name, value := range processEnv {
		vars[interp.ProcessEnvPrefix+name] = value
	}
}

func readDotEnvFile(path string) map[string]string {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return parseDotEnv(string(data))
}

func parseDotEnv(content string) map[string]string {
	values := map[string]string{}
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		name, raw, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		values[name] = parseDotEnvValue(raw)
	}
	return values
}

func parseDotEnvValue(raw string) string {
	value := strings.TrimSpace(raw)
	if value == "" {
		return ""
	}
	if value[0] == '"' {
		parsed, ok := parseQuotedDotEnvValue(value, '"')
		if ok {
			return strings.NewReplacer(`\n`, "\n", `\r`, "\r", `\"`, `"`, `\\`, `\`).Replace(parsed)
		}
	}
	if value[0] == '\'' {
		if parsed, ok := parseQuotedDotEnvValue(value, '\''); ok {
			return parsed
		}
	}
	if index := strings.IndexByte(value, '#'); index >= 0 {
		value = strings.TrimSpace(value[:index])
	}
	return strings.TrimSpace(value)
}

func parseQuotedDotEnvValue(value string, quote byte) (string, bool) {
	if len(value) < 2 || value[0] != quote {
		return "", false
	}
	for index := 1; index < len(value); index++ {
		if value[index] == quote && value[index-1] != '\\' {
			return value[1:index], true
		}
	}
	return "", false
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

func PromptVariablesForRequest(globalEnvs []types.Environment, collection *types.Collection, environmentID string, item types.RequestItem) []string {
	if collection == nil {
		return nil
	}
	effective := EffectiveRequest(*collection, item)
	prompts := []string{}
	seen := map[string]bool{}
	addPrompt := func(name string) {
		if name == "" || seen[name] {
			return
		}
		seen[name] = true
		prompts = append(prompts, name)
	}
	scanText := func(value string) {
		for _, match := range promptVariableInterpolationPattern.FindAllStringSubmatch(value, -1) {
			if len(match) == 2 {
				addPrompt(match[1])
			}
		}
	}
	scanValue := func(value interface{}) {
		if value != nil {
			scanText(fmt.Sprint(value))
		}
	}
	scanKeyValues := func(rows []types.KeyValue) {
		for _, row := range rows {
			if !row.Enabled {
				continue
			}
			scanText(row.Name)
			scanText(row.Value)
		}
	}
	scanVariables := func(rows []types.Variable) {
		for _, variable := range rows {
			if !variable.Enabled {
				continue
			}
			scanText(variable.Name)
			scanValue(variable.Value)
		}
	}

	scanText(effective.Method)
	scanText(effective.URL)
	scanText(effective.ProtoPath)
	scanKeyValues(effective.Params)
	scanKeyValues(effective.PathParams)
	scanKeyValues(effective.Headers)
	scanBodyPromptVariables(effective.Body, scanText, scanKeyValues)
	scanAuthPromptVariables(effective.Auth, scanText, scanKeyValues)
	for _, message := range effective.GrpcMessages {
		scanText(message.Name)
		scanText(message.Content)
	}
	for _, message := range effective.WSMessages {
		if !message.Selected {
			continue
		}
		scanText(message.Name)
		scanText(message.Content)
	}

	for _, env := range globalEnvs {
		scanVariables(env.Variables)
	}
	scanVariables(collection.Variables)
	if environmentID != "" {
		for _, env := range collection.Environments {
			if env.ID == environmentID {
				scanVariables(env.Variables)
				break
			}
		}
	}
	for _, folder := range FolderChain(*collection, item) {
		scanVariables(folder.Variables)
	}
	scanVariables(effective.Vars.Req)
	return prompts
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

func scriptProcessEnv(overrides map[string]string) map[string]string {
	return interp.ScriptProcessEnv(overrides)
}

func installPostmanVisualizer(runtime *goja.Runtime, pm *goja.Object, reqState *RequestState) {
	visualizer := runtime.NewObject()
	_ = visualizer.Set("set", func(call goja.FunctionCall) goja.Value {
		template := call.Argument(0).String()
		data := ""
		if len(call.Arguments) > 1 {
			value := call.Argument(1)
			if value != nil && !goja.IsUndefined(value) && !goja.IsNull(value) {
				if encoded, err := json.Marshal(value.Export()); err == nil {
					data = string(encoded)
				}
			}
		}
		payload, err := NormalizeVisualizerPayload(types.VisualizerPayload{Template: template, Data: data})
		if err != nil {
			// Thrown rather than silently dropped: a template over the limit
			// that vanished would leave an empty Visualizer tab and no reason.
			panic(runtime.NewGoError(err))
		}
		if reqState != nil {
			reqState.visualizer = &payload
		}
		return goja.Undefined()
	})
	_ = pm.Set("visualizer", visualizer)
}

// installPostmanIterationData exposes the runner data file's current row
// (US-043), reading the Data scope US-046 populates.
//
// Reads the Data scope specifically, NOT the resolved chain. pm.iterationData
// means "what the data file said for this iteration", and answering it from
// the merged chain would report environment and collection variables as
// iteration data — so a script guarding on
// `pm.iterationData.has("userId")` would take the data-driven branch on a run
// with no data file at all.
func installPostmanIterationData(runtime *goja.Runtime, pm *goja.Object, scriptVars *VariableContext) {
	data := func() map[string]interface{} {
		if scriptVars == nil || scriptVars.Data == nil {
			return map[string]interface{}{}
		}
		return scriptVars.Data
	}

	iterationData := runtime.NewObject()
	_ = iterationData.Set("get", func(name string) goja.Value {
		if value, ok := data()[name]; ok {
			return runtime.ToValue(value)
		}
		return goja.Undefined()
	})
	_ = iterationData.Set("has", func(name string) bool {
		_, ok := data()[name]
		return ok
	})
	_ = iterationData.Set("toObject", func() map[string]interface{} {
		// A copy: handing out the live scope would let a script mutate the
		// iteration's variables through a method Postman defines as a read.
		out := map[string]interface{}{}
		for key, value := range data() {
			out[key] = value
		}
		return out
	})
	_ = pm.Set("iterationData", iterationData)
}

// installPostmanVault maps pm.vault onto the existing secrets layer (US-043).
//
// Async on purpose: Postman's vault API returns promises, and scripts are
// written as `await pm.vault.get("key")`. Returning a bare value would work
// under await by accident and then break on `.then()`.
//
// set and unset deliberately REJECT rather than writing. The runtime has no way
// to tell a secret variable from an ordinary one — VariableContext holds
// plain maps with no Secret flag — so a pm.vault.set would land the value in
// the environment scope as an ordinary variable and get written to disk in the
// clear. A script storing a token would believe it was vaulted while leaking
// it. An explicit rejection is the honest answer; silently downgrading a
// secret to plaintext is not.
func installPostmanVault(runtime *goja.Runtime, pm, bruObject *goja.Object) {
	getSecret, hasGetSecret := goja.AssertFunction(bruObject.Get("getSecretVar"))

	resolved := func(value goja.Value) goja.Value {
		promise, resolve, _ := runtime.NewPromise()
		_ = resolve(value)
		return runtime.ToValue(promise)
	}
	rejected := func(message string) goja.Value {
		promise, _, reject := runtime.NewPromise()
		_ = reject(runtime.NewGoError(errors.New(message)))
		return runtime.ToValue(promise)
	}

	vault := runtime.NewObject()
	_ = vault.Set("get", func(name string) goja.Value {
		if !hasGetSecret {
			return resolved(goja.Undefined())
		}
		value, err := getSecret(goja.Undefined(), runtime.ToValue(name))
		if err != nil {
			return rejected(err.Error())
		}
		return resolved(value)
	})
	_ = vault.Set("has", func(name string) goja.Value {
		if !hasGetSecret {
			return resolved(runtime.ToValue(false))
		}
		value, err := getSecret(goja.Undefined(), runtime.ToValue(name))
		if err != nil {
			return rejected(err.Error())
		}
		present := value != nil && !goja.IsUndefined(value) && !goja.IsNull(value)
		return resolved(runtime.ToValue(present))
	})
	const writeMessage = "pm.vault.%s is not supported: this build cannot mark a value as secret from a script, and writing it would store the value in the environment in plain text"
	_ = vault.Set("set", func(string, goja.Value) goja.Value {
		return rejected(fmt.Sprintf(writeMessage, "set"))
	})
	_ = vault.Set("unset", func(string) goja.Value {
		return rejected(fmt.Sprintf(writeMessage, "unset"))
	})
	_ = pm.Set("vault", vault)
}

// installPostmanSideEffects wires pm's outward-facing calls to bru's (US-042).
//
// All three are pure delegation, deliberately. Each one has real machinery
// behind it that is easy to overlook when reimplementing: bru.sendRequest
// records a timeline entry for the scripted request and enforces the recursion
// depth limit, bru.cookies is bound to THIS request's jar and URL, and
// bru.setNextRequest feeds the runner's control flow. A parallel pm
// implementation would produce requests missing from the timeline, cookies
// from the wrong host, and a setNextRequest the runner never sees — none of
// which fails visibly.
func installPostmanSideEffects(runtime *goja.Runtime, pm, bruObject *goja.Object) {
	// Postman's pm.sendRequest(req, callback(err, response)) is already the
	// signature bru.sendRequest implements, so this is a straight alias rather
	// than an adapter.
	_ = pm.Set("sendRequest", bruObject.Get("sendRequest"))
	_ = pm.Set("cookies", bruObject.Get("cookies"))

	// pm.execution is where modern Postman puts run control.
	execution := runtime.NewObject()
	_ = execution.Set("setNextRequest", bruObject.Get("setNextRequest"))
	if runner := bruObject.Get("runner"); runner != nil && !goja.IsUndefined(runner) {
		if runnerObject := runner.ToObject(runtime); runnerObject != nil {
			_ = execution.Set("skipRequest", runnerObject.Get("skipRequest"))
		}
	}
	_ = pm.Set("execution", execution)
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

	_ = request.Set("method", reqObject.Get("method"))
	_ = request.Set("name", item.Name)
	_ = request.Set("body", reqObject.Get("body"))
	_ = request.Set("getHeaders", reqObject.Get("getHeaders"))

	headers := runtime.NewObject()
	_ = headers.Set("get", reqObject.Get("getHeader"))
	_ = headers.Set("upsert", reqObject.Get("setHeader"))
	_ = headers.Set("add", reqObject.Get("setHeader"))
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

var scriptOSStartTime = time.Now()

func KeyValuesFromHeaders(headers map[string]string) []types.KeyValue {
	keys := make([]string, 0, len(headers))
	for key := range headers {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	values := make([]types.KeyValue, 0, len(keys))
	for _, key := range keys {
		values = append(values, types.KeyValue{Name: key, Value: headers[key], Enabled: true})
	}
	return values
}

func PreviewModeFromHeaders(headers map[string]string) string {
	for name, value := range headers {
		if strings.EqualFold(name, "content-type") {
			lower := strings.ToLower(value)
			switch {
			case strings.Contains(lower, "text/event-stream"):
				return "sse"
			case strings.Contains(lower, "json"):
				return "json"
			case strings.Contains(lower, "xml"):
				return "xml"
			case strings.HasPrefix(lower, "image/"):
				return "image"
			}
		}
	}
	return "raw"
}

func EffectiveRequest(collection types.Collection, item types.RequestItem) types.RequestItem {
	effective := item
	folders := FolderChain(collection, item)
	existingHeaders := map[string]bool{}
	for _, header := range effective.Headers {
		existingHeaders[strings.ToLower(header.Name)] = true
	}
	candidates := append([]types.KeyValue{}, collection.Headers...)
	for _, folder := range folders {
		candidates = append(candidates, folder.Headers...)
	}
	selectedReverse := []types.KeyValue{}
	for i := len(candidates) - 1; i >= 0; i-- {
		header := candidates[i]
		key := strings.ToLower(header.Name)
		if header.Enabled && header.Name != "" && !existingHeaders[key] {
			selectedReverse = append(selectedReverse, header)
			existingHeaders[key] = true
		}
	}
	mergedHeaders := make([]types.KeyValue, 0, len(selectedReverse)+len(effective.Headers))
	for i := len(selectedReverse) - 1; i >= 0; i-- {
		mergedHeaders = append(mergedHeaders, selectedReverse[i])
	}
	mergedHeaders = append(mergedHeaders, effective.Headers...)
	effective.Headers = mergedHeaders
	if effective.Auth.Mode == "inherit" || effective.Auth.Mode == "" {
		auth := collection.Auth
		for _, folder := range folders {
			if folder.Auth.Mode != "" {
				auth = folder.Auth
			}
		}
		if auth.Mode != "" {
			effective.Auth = auth
		}
	}
	return effective
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

func PathInside(root, candidate string) bool {
	if strings.TrimSpace(root) == "" || strings.TrimSpace(candidate) == "" {
		return false
	}
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(candidate))
	if err != nil {
		return false
	}
	return rel == "." || (!strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != ".." && !filepath.IsAbs(rel))
}

const VisualizerTemplateLimit = 1 << 20 // 1 MiB
const VisualizerDataLimit = 4 << 20     // 4 MiB

func itemFolderPhysicalPath(collection types.Collection, item types.RequestItem) string {
	if PathInside(collection.Path, item.FilePath) {
		rel, err := filepath.Rel(collection.Path, filepath.Dir(item.FilePath))
		if err == nil && rel != "." {
			return filepath.ToSlash(rel)
		}
	}
	return filepath.ToSlash(strings.Trim(item.FolderPath, "/"))
}
func statusMessage(ok bool) string {
	if ok {
		return "passed"
	}
	return "failed"
}
