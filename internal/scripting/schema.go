package scripting

// JSON Schema validation exposed to sandbox tests.
//
// Split out of scripting.go by AST: declarations are identified by the parser
// and copied verbatim from their source offsets.

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/dop251/goja"
)

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

type scriptJSONSchemaOptions struct {
	CoerceTypes bool
	Strict      bool
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
