package main

// US-052 — Swagger 2 import, by conversion to OpenAPI 3.
//
// Swagger 2 was rejected outright before this. Converting rather than teaching
// the importer a second dialect is the story's instruction and the right call:
// the OpenAPI 3 importer already handles servers, parameters, request bodies,
// security schemes and $ref resolution, and a parallel Swagger 2 path would
// have to reimplement all of it and then drift.
//
// The conversions that actually change what request gets built — as opposed to
// what merely satisfies a validator — are:
//
//   host + basePath + schemes  ->  servers[]        (without this every
//                                                    imported request has a
//                                                    relative URL and 404s)
//   in: body parameter         ->  requestBody      (otherwise the body is
//                                                    imported as a header)
//   in: formData parameters    ->  requestBody      (same)
//   type/format on a parameter ->  schema.type      (OpenAPI 3 moved these)
//   #/definitions/X            ->  #/components/schemas/X
//
// The $ref rewrite is the one that fails silently if incomplete: a missed
// pointer resolves to nothing, the schema comes back empty, and the imported
// request simply has no example body — which looks like an API with no
// documented payload rather than a broken conversion.

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// swagger2RefPrefixes maps Swagger 2's top-level definition sections onto their
// OpenAPI 3 homes under components.
var swagger2RefPrefixes = []struct {
	from string
	to   string
	// section is the Swagger 2 top-level key, componentKey its components home.
	section      string
	componentKey string
}{
	{"#/definitions/", "#/components/schemas/", "definitions", "schemas"},
	{"#/parameters/", "#/components/parameters/", "parameters", "parameters"},
	{"#/responses/", "#/components/responses/", "responses", "responses"},
}

// isSwagger2Document reports whether raw is a Swagger 2 document.
//
// Checks the version value, not merely the presence of the key: a Swagger 1.x
// document also has "swagger", and converting one as if it were 2.0 would
// produce a plausible-looking document with the wrong shape rather than a clear
// rejection.
func isSwagger2Document(raw map[string]interface{}) bool {
	version, ok := raw["swagger"]
	if !ok {
		return false
	}
	return strings.HasPrefix(strings.TrimSpace(fmt.Sprintf("%v", version)), "2.")
}

// convertSwagger2ToOpenAPI3 returns an equivalent OpenAPI 3 document as JSON.
func convertSwagger2ToOpenAPI3(content string) (string, error) {
	var raw map[string]interface{}
	if err := yaml.Unmarshal([]byte(content), &raw); err != nil {
		return "", fmt.Errorf("swagger 2 document is not valid JSON or YAML: %w", err)
	}
	if raw == nil {
		return "", errors.New("swagger 2 document is empty")
	}
	if !isSwagger2Document(raw) {
		return "", errors.New("document does not declare swagger: 2.0")
	}

	// yaml.v3 produces map[string]interface{} for JSON-compatible input, but
	// nested maps from some sources arrive as map[interface{}]interface{}.
	// Normalising once here means every helper below can assume string keys.
	document, ok := normalizeYAMLMaps(raw).(map[string]interface{})
	if !ok {
		return "", errors.New("swagger 2 document is not an object")
	}

	delete(document, "swagger")
	document["openapi"] = "3.0.3"

	if servers := swagger2Servers(document); len(servers) > 0 {
		document["servers"] = servers
	}
	delete(document, "host")
	delete(document, "basePath")
	delete(document, "schemes")

	globalConsumes := swagger2StringSlice(document["consumes"])
	globalProduces := swagger2StringSlice(document["produces"])
	delete(document, "consumes")
	delete(document, "produces")

	components := swagger2Components(document)

	if paths, ok := document["paths"].(map[string]interface{}); ok {
		for _, pathValue := range paths {
			pathItem, ok := pathValue.(map[string]interface{})
			if !ok {
				continue
			}
			for method, operationValue := range pathItem {
				if !swagger2IsHTTPMethod(method) {
					continue
				}
				operation, ok := operationValue.(map[string]interface{})
				if !ok {
					continue
				}
				swagger2ConvertOperation(operation, globalConsumes, globalProduces)
			}
			// Path-level parameters follow the same rules as operation ones.
			if params, ok := pathItem["parameters"].([]interface{}); ok {
				converted, body, form := swagger2SplitParameters(params)
				pathItem["parameters"] = converted
				// A body or formData parameter at path level is legal Swagger 2
				// but has no OpenAPI 3 equivalent at that level; dropping it
				// silently would lose the payload, so it is not dropped — it is
				// promoted onto every operation under the path.
				if body != nil || len(form) > 0 {
					swagger2PromotePathBody(pathItem, body, form, globalConsumes)
				}
			}
		}
	}

	if len(components) > 0 {
		document["components"] = components
	}

	// The rewrite runs LAST, over the whole converted document, so pointers
	// created during operation conversion are rewritten too.
	rewritten := swagger2RewriteRefs(document)

	encoded, err := json.Marshal(rewritten)
	if err != nil {
		return "", fmt.Errorf("converted document could not be encoded: %w", err)
	}
	return string(encoded), nil
}

// normalizeYAMLMaps converts map[interface{}]interface{} to
// map[string]interface{} throughout, so downstream type assertions are simple.
func normalizeYAMLMaps(value interface{}) interface{} {
	switch typed := value.(type) {
	case map[interface{}]interface{}:
		out := make(map[string]interface{}, len(typed))
		for key, nested := range typed {
			out[fmt.Sprintf("%v", key)] = normalizeYAMLMaps(nested)
		}
		return out
	case map[string]interface{}:
		out := make(map[string]interface{}, len(typed))
		for key, nested := range typed {
			out[key] = normalizeYAMLMaps(nested)
		}
		return out
	case []interface{}:
		out := make([]interface{}, len(typed))
		for i, nested := range typed {
			out[i] = normalizeYAMLMaps(nested)
		}
		return out
	default:
		return value
	}
}

func swagger2IsHTTPMethod(name string) bool {
	switch strings.ToLower(name) {
	case "get", "put", "post", "delete", "options", "head", "patch", "trace":
		return true
	}
	return false
}

// swagger2Servers builds the servers array from host, basePath and schemes.
//
// This is the conversion that decides whether the import is usable at all:
// without it every request carries only a path, so the collection imports
// cleanly and then every request fails.
func swagger2Servers(document map[string]interface{}) []interface{} {
	host := strings.TrimSpace(fmt.Sprintf("%v", swagger2String(document["host"])))
	basePath := strings.TrimSpace(swagger2String(document["basePath"]))
	schemes := swagger2StringSlice(document["schemes"])

	if host == "" {
		if basePath == "" || basePath == "/" {
			return nil
		}
		// A basePath with no host is still worth keeping: it is the prefix
		// every path needs, and losing it produces requests to the wrong path.
		return []interface{}{map[string]interface{}{"url": basePath}}
	}
	if len(schemes) == 0 {
		// Swagger 2's default is the scheme the spec itself was served over,
		// which is unknowable here. https is the safer guess: an https URL
		// against an http-only server fails loudly, whereas http against an
		// https server can silently downgrade a credentialed request.
		schemes = []string{"https"}
	}

	servers := make([]interface{}, 0, len(schemes))
	for _, scheme := range schemes {
		url := scheme + "://" + host
		if basePath != "" && basePath != "/" {
			url += "/" + strings.Trim(basePath, "/")
		}
		servers = append(servers, map[string]interface{}{"url": url})
	}
	return servers
}

func swagger2String(value interface{}) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	return fmt.Sprintf("%v", value)
}

func swagger2StringSlice(value interface{}) []string {
	items, ok := value.([]interface{})
	if !ok {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		if text := strings.TrimSpace(swagger2String(item)); text != "" {
			out = append(out, text)
		}
	}
	return out
}

// swagger2Components moves the top-level definition sections under components
// and converts securityDefinitions.
func swagger2Components(document map[string]interface{}) map[string]interface{} {
	components := map[string]interface{}{}
	if existing, ok := document["components"].(map[string]interface{}); ok {
		components = existing
	}

	for _, mapping := range swagger2RefPrefixes {
		section, ok := document[mapping.section].(map[string]interface{})
		if !ok || len(section) == 0 {
			delete(document, mapping.section)
			continue
		}
		if mapping.section == "parameters" {
			for name, value := range section {
				if parameter, ok := value.(map[string]interface{}); ok {
					section[name] = swagger2ConvertParameter(parameter)
				}
			}
		}
		components[mapping.componentKey] = section
		delete(document, mapping.section)
	}

	if schemes, ok := document["securityDefinitions"].(map[string]interface{}); ok && len(schemes) > 0 {
		converted := map[string]interface{}{}
		for name, value := range schemes {
			scheme, ok := value.(map[string]interface{})
			if !ok {
				continue
			}
			converted[name] = swagger2SecurityScheme(scheme)
		}
		if len(converted) > 0 {
			components["securitySchemes"] = converted
		}
	}
	delete(document, "securityDefinitions")

	return components
}

// swagger2SecurityScheme converts one securityDefinitions entry.
//
// basic is the case that matters: OpenAPI 3 replaced `type: basic` with
// `type: http, scheme: basic`, and an unconverted entry is not merely invalid —
// the importer reads no auth from it, so the request imports with auth mode
// none and fails with a 401 that points nowhere.
func swagger2SecurityScheme(scheme map[string]interface{}) map[string]interface{} {
	out := map[string]interface{}{}
	for key, value := range scheme {
		out[key] = value
	}

	switch strings.ToLower(strings.TrimSpace(swagger2String(scheme["type"]))) {
	case "basic":
		out["type"] = "http"
		out["scheme"] = "basic"
	case "oauth2":
		flowName := map[string]string{
			"implicit":    "implicit",
			"password":    "password",
			"application": "clientCredentials",
			"accessCode":  "authorizationCode",
		}[strings.TrimSpace(swagger2String(scheme["flow"]))]
		if flowName == "" {
			flowName = "implicit"
		}
		flow := map[string]interface{}{}
		if url := swagger2String(scheme["authorizationUrl"]); url != "" {
			flow["authorizationUrl"] = url
		}
		if url := swagger2String(scheme["tokenUrl"]); url != "" {
			flow["tokenUrl"] = url
		}
		if scopes, ok := scheme["scopes"]; ok {
			flow["scopes"] = scopes
		} else {
			// scopes is required in OpenAPI 3 even when empty.
			flow["scopes"] = map[string]interface{}{}
		}
		out["flows"] = map[string]interface{}{flowName: flow}
		delete(out, "flow")
		delete(out, "authorizationUrl")
		delete(out, "tokenUrl")
		delete(out, "scopes")
	}
	return out
}

// swagger2ConvertOperation rewrites one operation in place.
func swagger2ConvertOperation(operation map[string]interface{}, globalConsumes, globalProduces []string) {
	consumes := swagger2StringSlice(operation["consumes"])
	if len(consumes) == 0 {
		consumes = globalConsumes
	}
	produces := swagger2StringSlice(operation["produces"])
	if len(produces) == 0 {
		produces = globalProduces
	}
	delete(operation, "consumes")
	delete(operation, "produces")

	if params, ok := operation["parameters"].([]interface{}); ok {
		converted, body, form := swagger2SplitParameters(params)
		if len(converted) > 0 {
			operation["parameters"] = converted
		} else {
			delete(operation, "parameters")
		}
		if requestBody := swagger2RequestBody(body, form, consumes); requestBody != nil {
			operation["requestBody"] = requestBody
		}
	}

	if responses, ok := operation["responses"].(map[string]interface{}); ok {
		for code, value := range responses {
			response, ok := value.(map[string]interface{})
			if !ok {
				continue
			}
			responses[code] = swagger2Response(response, produces)
		}
	}
}

// swagger2SplitParameters separates the parameters that stay parameters from
// the body and formData ones, which become a requestBody.
func swagger2SplitParameters(params []interface{}) (converted []interface{}, body map[string]interface{}, form []map[string]interface{}) {
	for _, value := range params {
		parameter, ok := value.(map[string]interface{})
		if !ok {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(swagger2String(parameter["in"]))) {
		case "body":
			body = parameter
		case "formData":
			form = append(form, parameter)
		case "formdata":
			form = append(form, parameter)
		default:
			converted = append(converted, swagger2ConvertParameter(parameter))
		}
	}
	return converted, body, form
}

// swagger2ConvertParameter moves the inline type keywords into a schema.
//
// OpenAPI 3 moved type, format, items, enum and default off the parameter and
// onto parameter.schema. Left in place they are ignored, so the imported
// request loses its example values — the parameter appears with an empty value
// and the user cannot tell it ever had one.
func swagger2ConvertParameter(parameter map[string]interface{}) map[string]interface{} {
	if _, isRef := parameter["$ref"]; isRef {
		return parameter
	}

	out := map[string]interface{}{}
	schema := map[string]interface{}{}
	if existing, ok := parameter["schema"].(map[string]interface{}); ok {
		schema = existing
	}

	schemaKeys := map[string]bool{
		"type": true, "format": true, "items": true, "enum": true,
		"default": true, "maximum": true, "minimum": true, "maxLength": true,
		"minLength": true, "pattern": true, "maxItems": true, "minItems": true,
		"uniqueItems": true, "multipleOf": true, "example": false,
	}

	for key, value := range parameter {
		if key == "schema" {
			continue
		}
		if belongs, known := schemaKeys[key]; known && belongs {
			schema[key] = value
			continue
		}
		// collectionFormat has no OpenAPI 3 equivalent that this importer
		// reads; carrying it through would put an unknown key in the output.
		if key == "collectionFormat" {
			continue
		}
		out[key] = value
	}

	if len(schema) > 0 {
		out["schema"] = schema
	}
	return out
}

// swagger2RequestBody builds the OpenAPI 3 requestBody from a body parameter
// and/or formData parameters.
func swagger2RequestBody(body map[string]interface{}, form []map[string]interface{}, consumes []string) map[string]interface{} {
	if body == nil && len(form) == 0 {
		return nil
	}

	if body != nil {
		mediaTypes := consumes
		if len(mediaTypes) == 0 {
			mediaTypes = []string{"application/json"}
		}
		content := map[string]interface{}{}
		schema := body["schema"]
		if schema == nil {
			schema = map[string]interface{}{}
		}
		for _, mediaType := range mediaTypes {
			content[mediaType] = map[string]interface{}{"schema": schema}
		}
		requestBody := map[string]interface{}{"content": content}
		if description := swagger2String(body["description"]); description != "" {
			requestBody["description"] = description
		}
		if required, ok := body["required"].(bool); ok && required {
			requestBody["required"] = true
		}
		return requestBody
	}

	// formData: one object schema whose properties are the fields.
	properties := map[string]interface{}{}
	var required []interface{}
	hasFile := false
	for _, field := range form {
		name := swagger2String(field["name"])
		if name == "" {
			continue
		}
		property := map[string]interface{}{}
		for key, value := range field {
			switch key {
			case "name", "in", "required", "collectionFormat":
				continue
			case "type":
				// Swagger 2's `type: file` became `type: string, format: binary`.
				if strings.EqualFold(swagger2String(value), "file") {
					property["type"] = "string"
					property["format"] = "binary"
					hasFile = true
					continue
				}
				property["type"] = value
			default:
				property[key] = value
			}
		}
		properties[name] = property
		if isRequired, ok := field["required"].(bool); ok && isRequired {
			required = append(required, name)
		}
	}

	schema := map[string]interface{}{"type": "object", "properties": properties}
	if len(required) > 0 {
		schema["required"] = required
	}

	mediaType := "application/x-www-form-urlencoded"
	if hasFile {
		// A file field cannot go through urlencoded; picking the wrong media
		// type here produces a request that encodes a binary as a form value.
		mediaType = "multipart/form-data"
	} else {
		for _, candidate := range consumes {
			if strings.HasPrefix(candidate, "multipart/") {
				mediaType = candidate
				break
			}
		}
	}

	return map[string]interface{}{
		"content": map[string]interface{}{
			mediaType: map[string]interface{}{"schema": schema},
		},
	}
}

// swagger2Response moves a response's schema under content.
func swagger2Response(response map[string]interface{}, produces []string) map[string]interface{} {
	out := map[string]interface{}{}
	for key, value := range response {
		if key == "schema" || key == "examples" {
			continue
		}
		out[key] = value
	}
	// description is required in OpenAPI 3.
	if _, ok := out["description"]; !ok {
		out["description"] = ""
	}

	schema, hasSchema := response["schema"]
	if !hasSchema {
		return out
	}

	mediaTypes := produces
	if len(mediaTypes) == 0 {
		mediaTypes = []string{"application/json"}
	}
	content := map[string]interface{}{}
	for _, mediaType := range mediaTypes {
		entry := map[string]interface{}{"schema": schema}
		if examples, ok := response["examples"].(map[string]interface{}); ok {
			if example, ok := examples[mediaType]; ok {
				entry["example"] = example
			}
		}
		content[mediaType] = entry
	}
	out["content"] = content
	return out
}

// swagger2PromotePathBody copies a path-level body or formData parameter onto
// every operation under that path.
//
// Path-level body parameters are legal Swagger 2 and have no OpenAPI 3
// equivalent at that level. Dropping one would silently lose the payload for
// every operation under the path.
func swagger2PromotePathBody(pathItem map[string]interface{}, body map[string]interface{}, form []map[string]interface{}, consumes []string) {
	requestBody := swagger2RequestBody(body, form, consumes)
	if requestBody == nil {
		return
	}
	for method, value := range pathItem {
		if !swagger2IsHTTPMethod(method) {
			continue
		}
		operation, ok := value.(map[string]interface{})
		if !ok {
			continue
		}
		// An operation's own body wins: it is the more specific declaration.
		if _, exists := operation["requestBody"]; !exists {
			operation["requestBody"] = requestBody
		}
	}
}

// swagger2RewriteRefs rewrites every $ref pointer in the document.
//
// Recursive over the whole tree rather than targeted at known locations,
// because a $ref can appear anywhere a schema can — inside allOf, inside an
// array item, inside a nested property. A missed pointer resolves to nothing
// and the schema comes back empty, which reads as an API with no documented
// payload rather than a broken conversion.
func swagger2RewriteRefs(value interface{}) interface{} {
	switch typed := value.(type) {
	case map[string]interface{}:
		out := make(map[string]interface{}, len(typed))
		for key, nested := range typed {
			if key == "$ref" {
				if pointer, ok := nested.(string); ok {
					out[key] = swagger2RewriteRef(pointer)
					continue
				}
			}
			out[key] = swagger2RewriteRefs(nested)
		}
		return out
	case []interface{}:
		out := make([]interface{}, len(typed))
		for i, nested := range typed {
			out[i] = swagger2RewriteRefs(nested)
		}
		return out
	default:
		return value
	}
}

func swagger2RewriteRef(pointer string) string {
	for _, mapping := range swagger2RefPrefixes {
		if strings.HasPrefix(pointer, mapping.from) {
			return mapping.to + strings.TrimPrefix(pointer, mapping.from)
		}
	}
	return pointer
}
