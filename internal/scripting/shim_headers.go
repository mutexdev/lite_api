package scripting

// The sandbox Headers / HeaderList shim.
//
// Split out of scripting.go by AST: declarations are identified by the parser
// and copied verbatim from their source offsets.

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/mutexdev/lite_api/internal/types"

	"github.com/dop251/goja"
)

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

func getHeaderValue(headers map[string]string, name string) string {
	for key, value := range headers {
		if strings.EqualFold(key, name) {
			return value
		}
	}
	return ""
}

func scriptResponseHeaders(headers map[string]string) map[string]string {
	result := map[string]string{}
	for name, value := range headers {
		result[strings.ToLower(name)] = value
	}
	return result
}

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
