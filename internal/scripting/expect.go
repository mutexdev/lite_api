package scripting

// The expect/assertion matchers the sandbox exposes to tests.
//
// Split out of scripting.go by AST: declarations are identified by the parser
// and copied verbatim from their source offsets.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"strconv"
	"strings"

	"github.com/dop251/goja"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

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
