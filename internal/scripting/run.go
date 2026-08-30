package scripting

// The package entry points: running a pre-request or post-response script and collecting what it produced.
//
// Split out by AST: declarations are identified by the parser and copied
// verbatim from their source offsets.

import (
	"fmt"
	"strings"

	"github.com/mutexdev/lite_api/internal/scalar"
	"github.com/mutexdev/lite_api/internal/types"

	"github.com/dop251/goja"
)

type RequestState struct {
	headers []types.KeyValue
	bodySet bool
	// bodySnapshot is req.body exactly as the runtime seeded it. Compared on the
	// way out so an untouched body is left alone rather than round-tripped
	// through its flattened rendering. See applyScriptedRequest.
	bodySnapshot               string
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
	return RunPreRequestScriptSourceMeta(SingleScriptSource("pre-request script", script), item, vars, jar, meta, nil, logs...)
}

// RunPreRequestScriptSourceMeta runs the pre-request levels and records every
// pm.test/test into testResults.
//
// testResults is what makes a failing assertion a RESULT rather than a crash.
// Passing nil here — which the send path used to do for both the pre-request
// and post-response slots — meant a passing pm.test vanished with nothing to
// record it into, and a failing one was re-thrown: the rest of the script never
// ran, and in this phase the request was never sent. The Tests slot always
// passed a registry, which is why the same three lines behaved like a test
// framework there and like a landmine everywhere else.
//
// An uncaught throw that is NOT a test failure still returns an error, and the
// caller still refuses to send. That distinction is the point: a broken script
// stops the request, a failed expectation reports and carries on.
func RunPreRequestScriptSourceMeta(source ScriptSource, item *types.RequestItem, vars map[string]string, jar *CookieJar, meta ScriptRuntimeMeta, testResults *[]types.TestResult, logs ...*[]types.ScriptLog) (*RequestState, error) {
	if source.IsEmpty() {
		return &RequestState{headers: types.CloneKeyValues(item.Headers)}, nil
	}
	runtime, reqObject, reqState, _ := NewScriptRuntimeWithMeta(*item, types.Response{}, vars, testResults, selectedScriptLogs(logs), jar, meta)
	if err := runGojaScriptLevels(runtime, source, meta.JSSandboxMode); err != nil {
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
	return RunPostResponseScriptSourceMeta(SingleScriptSource("post-response script", script), item, response, vars, jar, meta, nil, logs...)
}

// RunPostResponseScriptSourceMeta runs the post-response levels, recording every
// pm.test/test into testResults. See RunPreRequestScriptSourceMeta for why the
// registry is not optional on the send path.
func RunPostResponseScriptSourceMeta(source ScriptSource, item types.RequestItem, response *types.Response, vars map[string]string, jar *CookieJar, meta ScriptRuntimeMeta, testResults *[]types.TestResult, logs ...*[]types.ScriptLog) (*RequestState, error) {
	if source.IsEmpty() {
		return &RequestState{headers: types.CloneKeyValues(item.Headers)}, nil
	}
	runtime, _, reqState, resObject := NewScriptRuntimeWithMeta(item, *response, vars, testResults, selectedScriptLogs(logs), jar, meta)
	err := runGojaScriptLevels(runtime, source, meta.JSSandboxMode)
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
	// This phase evaluates bare expressions rather than running a script, so it
	// never reaches runGojaScriptLevels — the one place that releases the event
	// loop registered for a runtime. Without this the sync.Map keyed by
	// *goja.Runtime kept every runtime this phase ever built alive, one per
	// request, for the life of the process.
	defer scriptRuntimeEventLoops.Delete(runtime)
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
