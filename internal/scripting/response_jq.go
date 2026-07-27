package scripting

// The jq-style query language a script can run over a response body.
//
// Split out by AST: declarations are identified by the parser and copied
// verbatim from their source offsets.

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/dop251/goja"
)

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
