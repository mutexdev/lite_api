package scripting

// The sandbox fetch, Request and Response shims.
//
// Split out of scripting.go by AST: declarations are identified by the parser
// and copied verbatim from their source offsets.

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/mutexdev/lite_api/internal/cookiejar"
	"github.com/mutexdev/lite_api/internal/interp"
	"github.com/mutexdev/lite_api/internal/scalar"
	"github.com/mutexdev/lite_api/internal/types"

	"github.com/dop251/goja"
)

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
