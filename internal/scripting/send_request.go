package scripting

// bru.sendRequest(): issuing a request from inside a script.
//
// Split out by AST: declarations are identified by the parser and copied
// verbatim from their source offsets.

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/mutexdev/lite_api/internal/interp"
	"github.com/mutexdev/lite_api/internal/scalar"
	"github.com/mutexdev/lite_api/internal/types"

	"github.com/dop251/goja"
)

type scriptSendRequestConfig struct {
	Method  string
	URL     string
	Headers map[string]string
	Params  map[string]string
	Body    interface{}
	HasBody bool

	// Set when the body arrived as a Postman body definition and has already
	// been serialised — see postman_send_body.go. Body is left holding the
	// original definition so the timeline still shows what the script asked
	// for.
	BodyText        string
	BodyContentType string
	BodyEncoded     bool
}

func scriptSendRequest(runtime *goja.Runtime, dialect scriptSendDialect, configValue goja.Value, vars map[string]string) (goja.Value, goja.Value, *types.TimelineItem, error) {
	config, err := scriptSendRequestConfigFromValue(runtime, dialect, configValue, vars)
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
		bodyText, contentType := config.BodyText, config.BodyContentType
		if !config.BodyEncoded {
			bodyText, contentType = scriptSendRequestBody(config.Body)
		}
		bodyReader = strings.NewReader(bodyText)
		if contentType != "" && scriptSendRequestWantsContentType(config.Headers, contentType) {
			scriptSendRequestSetContentType(config.Headers, contentType)
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
	scriptAttachPostmanResponseShape(runtime, responseObject, res.StatusCode,
		scalar.CleanStatusText(res.StatusCode, res.Status), bodyText, data)
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

func scriptSendRequestConfigFromValue(runtime *goja.Runtime, dialect scriptSendDialect, value goja.Value, vars map[string]string) (scriptSendRequestConfig, error) {
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
		config.URL = interp.Interpolate(scriptSendRequestURLString(runtime, targetURL), vars)
	}
	// `headers` is Bruno's/axios's spelling and `header` is Postman's. Reading
	// only `headers` meant a pm.sendRequest written the ordinary Postman way
	// had every header silently dropped — including the Content-Type that
	// decides how the server parses the body. `headers` is read second so it
	// wins if a script somehow sets both.
	for _, name := range []string{"header", "headers"} {
		headers := object.Get(name)
		if headers == nil || goja.IsUndefined(headers) || goja.IsNull(headers) {
			continue
		}
		for key, value := range scriptSendRequestStringMap(runtime, headers, vars) {
			config.Headers[key] = value
		}
	}
	if params := object.Get("params"); params != nil && !goja.IsUndefined(params) && !goja.IsNull(params) {
		config.Params = scriptSendRequestStringMap(runtime, params, vars)
	}
	// Under the Postman dialect a `body` may be a request-body DEFINITION rather
	// than a payload; under Bruno's it never is. `data` is axios's key and has
	// no Postman meaning, so it stays a payload in both.
	//
	// The mode check still runs, but it is no longer deciding which API this is
	// — only whether this particular body is a definition. A pm.sendRequest
	// payload with no `mode` is JSON exactly as before, which is what keeps
	// scripts already written against LiteAPI working.
	postmanBody := false
	if data := object.Get("data"); data != nil && !goja.IsUndefined(data) && !goja.IsNull(data) {
		config.Body = data.Export()
		config.HasBody = true
	} else if body := object.Get("body"); body != nil && !goja.IsUndefined(body) && !goja.IsNull(body) {
		config.Body = body.Export()
		config.HasBody = true
		postmanBody = dialect == dialectPostman && scriptIsPostmanRequestBody(config.Body)
	}
	if config.HasBody {
		// Interpolation runs BEFORE serialisation so that {{tokenUrl}} inside a
		// urlencoded row is substituted in the row's value rather than in the
		// finished, percent-encoded body — where the braces would already have
		// been escaped past recognition.
		config.Body = interpolateScriptSendRequestBody(config.Body, vars)
	}
	if postmanBody {
		text, contentType, err := scriptPostmanRequestBody(config.Body)
		if err != nil {
			return config, err
		}
		config.BodyText = text
		config.BodyContentType = contentType
		config.BodyEncoded = true
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

// scriptSendRequestStringMap reads a header or param collection.
//
// Two shapes reach here and they are not interchangeable. `{'Content-Type': 'x'}`
// is the object form; `[{key: 'Content-Type', value: 'x'}]` is the array form
// Postman uses everywhere, and it is what a script gets back from
// pm.request.headers. Reading the array form as an object produced keys "0",
// "1", … with the value "[object Object]" — a request carrying garbage headers
// and no sign of why.
func scriptSendRequestStringMap(runtime *goja.Runtime, value goja.Value, vars map[string]string) map[string]string {
	out := map[string]string{}
	if rows, ok := value.Export().([]interface{}); ok {
		for _, row := range rows {
			// The header vocabulary is Postman's key/value/disabled; the row
			// reader also accepts name/enabled, which is what LiteAPI's own
			// KeyValue rows look like.
			key, rowValue, enabled := scriptPostmanBodyRow(row)
			if !enabled || strings.TrimSpace(key) == "" {
				continue
			}
			out[interp.Interpolate(key, vars)] = interp.Interpolate(rowValue, vars)
		}
		return out
	}
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

// scriptSendRequestURLString reads the `url` field.
//
// Postman's own request definitions carry a parsed URL object rather than a
// string, and a script that forwards one — `pm.sendRequest({url: pm.request.url})`
// is the usual way it happens — used to send a request to the literal text
// "[object Object]".
func scriptSendRequestURLString(runtime *goja.Runtime, value goja.Value) string {
	if exported, ok := value.Export().(map[string]interface{}); ok {
		if raw, ok := exported["raw"].(string); ok {
			return raw
		}
	}
	_ = runtime
	return value.String()
}

// scriptSendRequestWantsContentType reports whether the mode's Content-Type
// should be written.
//
// Postman only fills in a Content-Type the script did not set, and that rule is
// kept — with one exception it has to have. A hand-written
// `Content-Type: multipart/form-data` names no boundary, so honouring it would
// send parts no server can find. There the generated header replaces it.
func scriptSendRequestWantsContentType(headers map[string]string, contentType string) bool {
	if !StringMapHasKey(headers, "Content-Type") {
		return true
	}
	if !strings.HasPrefix(strings.ToLower(contentType), "multipart/") {
		return false
	}
	for name, value := range headers {
		if strings.EqualFold(name, "Content-Type") {
			return strings.HasPrefix(strings.ToLower(strings.TrimSpace(value)), "multipart/") &&
				!scriptContentTypeHasBoundary(value)
		}
	}
	return true
}

// Writes Content-Type under whatever casing the script already used, so a
// replacement does not leave two headers differing only in case.
func scriptSendRequestSetContentType(headers map[string]string, contentType string) {
	for name := range headers {
		if strings.EqualFold(name, "Content-Type") {
			headers[name] = contentType
			return
		}
	}
	headers["Content-Type"] = contentType
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

// scriptAttachPostmanResponseShape adds the accessors a Postman script expects
// on a sendRequest response.
//
// Postman's Response (postman-collection 5.3.1 `response.js`) names things
// differently from axios: `code` is the number, `status` is the reason phrase,
// and the body is read through `json()` / `text()` rather than `.data`. So the
// ordinary Postman idiom
//
//	if (res.code === 200) { const body = res.json() }
//
// used to fail twice over — `code` was undefined, and `json` was not a
// function. Neither failure says anything about the client it is running in.
//
// Added under BOTH dialects, because every field here is new: nothing that
// already worked changes meaning. `status` is the one Postman name already
// taken — it is the number here, as it is in axios, fetch and Bruno — and it is
// deliberately left alone. Redefining it as the reason phrase would silently
// break every script that already compares it to a number, to fix an idiom
// (`res.status === 'OK'`) that nobody writes. `code` and `reason()` give a
// Postman script both halves under their own names.
func scriptAttachPostmanResponseShape(runtime *goja.Runtime, response *goja.Object, code int, reason, bodyText string, data interface{}) {
	_ = response.Set("code", code)
	_ = response.Set("reason", func(goja.FunctionCall) goja.Value {
		return runtime.ToValue(reason)
	})
	_ = response.Set("text", func(goja.FunctionCall) goja.Value {
		return runtime.ToValue(bodyText)
	})
	_ = response.Set("json", func(goja.FunctionCall) goja.Value {
		// Postman throws on a body that is not JSON rather than returning the
		// raw text, and a script's own try/catch is written expecting that.
		// Returning the string instead would turn a parse failure into a
		// TypeError further down, somewhere unrelated.
		if _, ok := data.(string); ok {
			if _, valid := responseJSONValue(bodyText); !valid {
				panic(runtime.NewTypeError("response.json(): the response body is not JSON"))
			}
		}
		return runtime.ToValue(data)
	})
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
