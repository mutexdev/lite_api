package importers

// OpenAPI response links become post-response scripts that chain one request to the next.
//
// Split out by AST: declarations are identified by the parser and copied
// verbatim from their source offsets.

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/mutexdev/lite_api/internal/scalar"
)

func openAPIResponseLinkScript(operation openAPIOperation, root map[string]interface{}) string {
	if len(operation.Responses) == 0 {
		return ""
	}
	statuses := make([]string, 0, len(operation.Responses))
	for status := range operation.Responses {
		statuses = append(statuses, status)
	}
	sort.Strings(statuses)
	lines := []string{}
	for _, statusCode := range statuses {
		responseMap, ok := openAPIResponseMap(operation.Responses[statusCode], root)
		if !ok {
			continue
		}
		links, ok := scalar.Map(responseMap["links"])
		if !ok || len(links) == 0 {
			continue
		}
		status := openAPIStatusCode(statusCode)
		indent := ""
		if status > 0 {
			lines = append(lines, fmt.Sprintf("if (res.status === %d) {", status))
			indent = "  "
		}
		names := make([]string, 0, len(links))
		for name := range links {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			link, ok := openAPIResponseLinkMap(links[name], root)
			if !ok {
				continue
			}
			parameters, ok := scalar.Map(link["parameters"])
			if !ok || len(parameters) == 0 {
				continue
			}
			operationID := scalar.FirstNonEmpty(scalar.YAMLString(link["operationId"]), name)
			paramNames := make([]string, 0, len(parameters))
			for paramName := range parameters {
				paramNames = append(paramNames, paramName)
			}
			sort.Strings(paramNames)
			for _, paramName := range paramNames {
				expression := openAPILinkExpressionToScript(parameters[paramName])
				if expression == "" {
					continue
				}
				variableName := operationID + "_" + paramName
				lines = append(lines, fmt.Sprintf("%sbru.setVar(%s, %s);", indent, JSStringLiteral(variableName), expression))
			}
		}
		if status > 0 {
			lines = append(lines, "}")
		}
	}
	return strings.Join(lines, "\n")
}

func openAPIResponseLinkMap(raw interface{}, root map[string]interface{}) (map[string]interface{}, bool) {
	valueMap, ok := scalar.Map(raw)
	if !ok {
		return nil, false
	}
	if ref := scalar.YAMLString(valueMap["$ref"]); ref != "" {
		if resolved, ok := openAPIResolveRef(root, ref); ok {
			return scalar.Map(resolved)
		}
	}
	return valueMap, true
}

func openAPILinkExpressionToScript(raw interface{}) string {
	switch value := raw.(type) {
	case string:
		return openAPIRuntimeExpressionToScript(value)
	case []byte:
		return openAPIRuntimeExpressionToScript(string(value))
	}
	if valueMap, ok := scalar.Map(raw); ok {
		if data, ok := valueMap["data"]; ok {
			return openAPILinkExpressionToScript(data)
		}
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return ""
	}
	return string(encoded)
}

func openAPIRuntimeExpressionToScript(expression string) string {
	switch {
	case expression == "$response.body":
		return "res.body"
	case strings.HasPrefix(expression, "$response.body#"):
		pointer := strings.TrimPrefix(expression, "$response.body#")
		return "res.json" + openAPIJSONPointerScript(pointer)
	case expression == "$statusCode":
		return "res.status"
	case expression == "$method":
		return "req.method"
	case expression == "$url":
		return "req.url"
	case strings.HasPrefix(expression, "$response.header."):
		return "res.getHeader(" + JSStringLiteral(strings.TrimPrefix(expression, "$response.header.")) + ")"
	case strings.HasPrefix(expression, "$request.header."):
		return "req.getHeader(" + JSStringLiteral(strings.TrimPrefix(expression, "$request.header.")) + ")"
	case expression == "$request.body":
		return "req.body"
	case strings.HasPrefix(expression, "$request.body#"):
		pointer := strings.TrimPrefix(expression, "$request.body#")
		return "req.body" + openAPIJSONPointerScript(pointer)
	default:
		return JSStringLiteral(expression)
	}
}

func openAPIJSONPointerScript(pointer string) string {
	if pointer == "" {
		return ""
	}
	pointer = strings.TrimPrefix(pointer, "#")
	if pointer == "" {
		return ""
	}
	if !strings.HasPrefix(pointer, "/") {
		return ""
	}
	parts := strings.Split(strings.TrimPrefix(pointer, "/"), "/")
	var b strings.Builder
	for _, part := range parts {
		part = strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
		if index, err := strconv.Atoi(part); err == nil && strconv.Itoa(index) == part {
			b.WriteString("[")
			b.WriteString(part)
			b.WriteString("]")
			continue
		}
		b.WriteString("[")
		b.WriteString(JSStringLiteral(part))
		b.WriteString("]")
	}
	return b.String()
}

func JSStringLiteral(value string) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return `""`
	}
	return string(encoded)
}
