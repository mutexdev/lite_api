package importers

// Turning OpenAPI examples and schemas into the example bodies a request shows.
//
// Split out by AST: declarations are identified by the parser and copied
// verbatim from their source offsets.

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/mutexdev/lite_api/internal/scalar"
	"github.com/mutexdev/lite_api/internal/types"
)

func openAPIExampleValueFromRaw(raw interface{}) (string, bool) {
	value, ok := openAPIExampleRawValue(raw)
	if !ok {
		return "", false
	}
	return openAPIStringValue(value)
}

func openAPIMediaExample(media map[string]interface{}, root map[string]interface{}) interface{} {
	if value, ok := media["example"]; ok {
		return value
	}
	if value, ok := openAPIExampleRawValue(media["examples"]); ok {
		return value
	}
	if schema, ok := media["schema"]; ok {
		return openAPISchemaTemplate(schema, root, map[string]bool{})
	}
	return nil
}

type openAPINamedExample struct {
	Key         string
	Name        string
	Description string
	Value       interface{}
}

func openAPIResponseExamples(operation openAPIOperation, item types.RequestItem, root map[string]interface{}) []types.ResponseExample {
	if len(operation.Responses) == 0 {
		return nil
	}
	requestExamples := openAPIRequestBodyExampleBodies(operation.RequestBody, root, item.Body.Mode)
	statuses := make([]string, 0, len(operation.Responses))
	for status := range operation.Responses {
		statuses = append(statuses, status)
	}
	sort.Strings(statuses)
	result := []types.ResponseExample{}
	for _, statusCode := range statuses {
		responseMap, ok := openAPIResponseMap(operation.Responses[statusCode], root)
		if !ok {
			continue
		}
		content, _ := scalar.Map(responseMap["content"])
		contentType, media := openAPIPreferredMedia(content)
		if contentType == "" {
			continue
		}
		bodyType := openAPIExampleBodyType(contentType)
		responseHeaders := openAPIResponseExampleHeaders(responseMap, contentType, root)
		examples := openAPIMediaNamedExamples(media, root)
		for index, example := range examples {
			body := openAPIExampleBodyString(example.Value, bodyType)
			if strings.TrimSpace(body) == "" {
				continue
			}
			status := openAPIStatusCode(statusCode)
			name := scalar.FirstNonEmpty(example.Name, fmt.Sprintf("%s %s", statusCode, scalar.CleanStatusText(status, "")))
			if len(examples) > 1 && example.Key != "" && example.Name == "" {
				name = example.Key
			}
			requestBody := types.RequestBodySnapshot(item.Body)
			if matched, ok := requestExamples[example.Key]; ok {
				requestBody = matched
			} else if len(requestExamples) == 1 {
				for _, matched := range requestExamples {
					requestBody = matched
				}
			}
			result = append(result, types.ResponseExample{
				ID:          scalar.DeterministicID("example", item.Name+"#openapi#"+statusCode+"#"+strconv.Itoa(index)+"#"+name),
				Name:        name,
				Description: scalar.FirstNonEmpty(example.Description, scalar.YAMLString(responseMap["description"])),
				Type:        scalar.FirstNonEmpty(item.Type, "http"),
				Request: types.ResponseExampleRequest{
					Method:         strings.ToUpper(scalar.FirstNonEmpty(item.Method, http.MethodGet)),
					URL:            item.URL,
					BodyMode:       scalar.FirstNonEmpty(item.Body.Mode, "none"),
					Body:           requestBody,
					Headers:        types.CloneKeyValues(item.Headers),
					Params:         types.CloneKeyValues(item.Params),
					FormURLEncoded: types.CloneKeyValues(item.Body.FormURLEncoded),
					MultipartForm:  types.CloneFormParts(item.Body.Multipart),
					File:           types.CloneFileBodyEntries(types.FileBodyEntriesOf(item.Body)),
				},
				Response: types.ResponseExamplePayload{
					Status:     status,
					StatusText: scalar.CleanStatusText(status, ""),
					Headers:    types.CloneKeyValues(responseHeaders),
					BodyType:   bodyType,
					Body:       body,
					Size:       len([]byte(body)),
				},
			})
		}
	}
	return result
}

func openAPIResponseExampleHeaders(responseMap map[string]interface{}, contentType string, root map[string]interface{}) []types.KeyValue {
	headers := []types.KeyValue{}
	if strings.TrimSpace(contentType) != "" {
		headers = append(headers, types.KeyValue{Name: "Content-Type", Value: contentType, Enabled: true})
	}
	headerMap, ok := scalar.Map(responseMap["headers"])
	if !ok || len(headerMap) == 0 {
		return headers
	}
	names := make([]string, 0, len(headerMap))
	for name := range headerMap {
		if strings.TrimSpace(name) == "" || strings.EqualFold(name, "Content-Type") {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		header, ok := openAPIHeaderMap(headerMap[name], root)
		if !ok {
			continue
		}
		headers = append(headers, types.KeyValue{
			Name:        name,
			Value:       openAPIHeaderExampleValue(header, root),
			Enabled:     true,
			Description: scalar.YAMLString(header["description"]),
		})
	}
	return headers
}

func openAPIHeaderMap(raw interface{}, root map[string]interface{}) (map[string]interface{}, bool) {
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

func openAPIHeaderExampleValue(header map[string]interface{}, root map[string]interface{}) string {
	if value, ok := openAPIStringValue(header["example"]); ok {
		return value
	}
	if value, ok := openAPIExampleValueFromRaw(header["examples"]); ok {
		return value
	}
	if value := openAPIParameterValueFromSchema(header["schema"]); value != "" {
		return value
	}
	if template := openAPISchemaTemplate(header["schema"], root, map[string]bool{}); template != nil {
		if value, ok := openAPIStringValue(template); ok {
			return value
		}
	}
	return ""
}

func openAPIResponseMap(raw interface{}, root map[string]interface{}) (map[string]interface{}, bool) {
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

func openAPIMediaNamedExamples(media map[string]interface{}, root map[string]interface{}) []openAPINamedExample {
	if value, ok := media["example"]; ok {
		return []openAPINamedExample{{Key: "example", Name: "Example", Value: value}}
	}
	if examples, ok := scalar.Map(media["examples"]); ok && len(examples) > 0 {
		names := make([]string, 0, len(examples))
		for name := range examples {
			names = append(names, name)
		}
		sort.Strings(names)
		result := make([]openAPINamedExample, 0, len(names))
		for _, key := range names {
			named := openAPINamedExample{Key: key, Name: key, Value: examples[key]}
			if valueMap, ok := scalar.Map(examples[key]); ok {
				if value, ok := valueMap["value"]; ok {
					named.Value = value
				}
				named.Name = scalar.FirstNonEmpty(scalar.YAMLString(valueMap["summary"]), key)
				named.Description = scalar.YAMLString(valueMap["description"])
			}
			result = append(result, named)
		}
		return result
	}
	if schema, ok := media["schema"]; ok {
		return []openAPINamedExample{{Key: "schema", Name: "Schema example", Value: openAPISchemaTemplate(schema, root, map[string]bool{})}}
	}
	return nil
}

func openAPIRequestBodyExampleBodies(raw map[string]interface{}, root map[string]interface{}, mode string) map[string]string {
	if raw == nil {
		return nil
	}
	raw = resolveOpenAPIRequestBody(raw, root)
	content, ok := scalar.Map(raw["content"])
	if !ok || len(content) == 0 {
		return nil
	}
	_, media := openAPIPreferredMedia(content)
	if media == nil {
		return nil
	}
	bodyType := openAPIExampleBodyType(mode)
	result := map[string]string{}
	for _, example := range openAPIMediaNamedExamples(media, root) {
		result[example.Key] = openAPIExampleBodyString(example.Value, bodyType)
	}
	return result
}

func openAPIExampleBodyType(contentTypeOrMode string) string {
	switch openAPIBodyMode(contentTypeOrMode) {
	case "json":
		return "json"
	case "xml":
		return "xml"
	default:
		if contentTypeOrMode == "json" || contentTypeOrMode == "xml" {
			return contentTypeOrMode
		}
		return "text"
	}
}

func openAPIExampleBodyString(value interface{}, bodyType string) string {
	switch bodyType {
	case "json":
		return openAPIJSONString(value)
	case "xml":
		if text, ok := openAPIStringValue(value); ok {
			return text
		}
		return "<root></root>"
	default:
		text, _ := openAPIStringValue(value)
		return text
	}
}

func openAPIStatusCode(value string) int {
	if strings.EqualFold(value, "default") {
		return 0
	}
	status, err := strconv.Atoi(value)
	if err != nil {
		return 0
	}
	return status
}

func openAPIExampleValue(examples map[string]interface{}) (string, bool) {
	value, ok := openAPIExampleRawValue(examples)
	if !ok {
		return "", false
	}
	return openAPIStringValue(value)
}

func openAPIExampleRawValue(raw interface{}) (interface{}, bool) {
	examples, ok := scalar.Map(raw)
	if !ok || len(examples) == 0 {
		return nil, false
	}
	names := make([]string, 0, len(examples))
	for name := range examples {
		names = append(names, name)
	}
	// Map iteration order is random, so pick the lowest-sorting name to make the
	// chosen example deterministic across runs.
	sort.Strings(names)
	first := examples[names[0]]
	if valueMap, ok := scalar.Map(first); ok {
		if value, ok := valueMap["value"]; ok {
			return value, true
		}
	}
	return first, true
}

func openAPISchemaTemplate(raw interface{}, root map[string]interface{}, seen map[string]bool) interface{} {
	schema, ok := scalar.Map(raw)
	if !ok {
		return nil
	}
	if ref := scalar.YAMLString(schema["$ref"]); ref != "" {
		if seen[ref] {
			return nil
		}
		seen[ref] = true
		if resolved, ok := openAPIResolveRef(root, ref); ok {
			return openAPISchemaTemplate(resolved, root, seen)
		}
	}
	if value, ok := firstMapValueOK(schema, "example", "default"); ok {
		return value
	}
	if enumValues, ok := scalar.ListValue(schema["enum"]); ok && len(enumValues) > 0 {
		return enumValues[0]
	}
	if oneOf, ok := scalar.ListValue(scalar.FirstMapValue(schema, "oneOf", "anyOf", "allOf")); ok && len(oneOf) > 0 {
		return openAPISchemaTemplate(oneOf[0], root, seen)
	}
	schemaType := strings.ToLower(scalar.YAMLString(schema["type"]))
	if schemaType == "" && schema["properties"] != nil {
		schemaType = "object"
	}
	switch schemaType {
	case "object":
		result := map[string]interface{}{}
		if properties, ok := scalar.Map(schema["properties"]); ok {
			names := make([]string, 0, len(properties))
			for name := range properties {
				names = append(names, name)
			}
			sort.Strings(names)
			for _, name := range names {
				result[name] = openAPISchemaTemplate(properties[name], root, copyBoolMap(seen))
			}
		}
		return result
	case "array":
		return []interface{}{openAPISchemaTemplate(schema["items"], root, copyBoolMap(seen))}
	case "integer", "number":
		return 0
	case "boolean":
		return false
	case "string":
		return "string"
	default:
		return map[string]interface{}{}
	}
}
