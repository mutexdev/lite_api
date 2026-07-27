package scripting

// Querying a response body from a script: res.json(), jq-style paths and filters.
//
// Split out of scripting.go by AST: declarations are identified by the parser
// and copied verbatim from their source offsets.

import (
	"encoding/base64"
	"encoding/json"
	"strings"

	"github.com/mutexdev/lite_api/internal/types"

	"github.com/dop251/goja"
)

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

func responseDataBytes(response types.Response) []byte {
	if strings.TrimSpace(response.BodyBase64) != "" {
		if decoded, err := base64.StdEncoding.DecodeString(response.BodyBase64); err == nil {
			return decoded
		}
	}
	return []byte(response.Body)
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
