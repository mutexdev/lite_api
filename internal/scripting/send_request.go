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
