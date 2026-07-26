// Package importers converts third-party collection formats into LiteAPI's own.
//
// US-071. The story asked for importers/openapi, importers/postman and
// importers/insomnia as three packages. They are three FILES in one package
// instead: the three formats share exactly three helpers, and splitting them
// would have required a fourth package existing only to hold those, which is
// more structure than the coupling justifies.
//
// Every function here was already a free function -- 123 of them, none a method
// on *App -- which is why this could move at all.
package importers

import (
	"LiteAPI/internal/scalar"
	"LiteAPI/internal/types"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// OpenAPI 3.x specs, including callbacks, webhooks, response examples and links.

func ImportOpenAPI(content, fallbackName, groupBy string) (types.Collection, error) {
	var raw map[string]interface{}
	if err := yaml.Unmarshal([]byte(content), &raw); err != nil {
		return types.Collection{}, fmt.Errorf("parse openapi: %w", err)
	}
	if _, ok := raw["info"]; !ok {
		return types.Collection{}, errors.New("openapi info is required")
	}
	var doc OpenAPIDoc
	if err := yaml.Unmarshal([]byte(content), &doc); err != nil {
		return types.Collection{}, fmt.Errorf("parse openapi: %w", err)
	}
	name := strings.TrimSpace(doc.Info.Title)
	if name == "" {
		name = fallbackName
	}
	if name == "" {
		name = "Untitled types.Collection"
	}
	variables := []types.Variable{{ID: scalar.NewID("var"), Name: "baseUrl", Value: "{{baseUrl}}", DataType: "string", Enabled: true}}
	if len(doc.Servers) > 0 && doc.Servers[0].URL != "" {
		variables = openAPIServerVariables(doc.Servers[0])
	}
	collection := types.Collection{
		ID:             scalar.NewID("collection"),
		Name:           name,
		Format:         "openapi",
		Variables:      variables,
		Auth:           types.AuthConfig{Mode: "none"},
		SecurityConfig: types.CollectionSecurityConfig{JSSandboxMode: "safe"},
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	methods := []string{"get", "post", "put", "patch", "delete", "head", "options", "trace"}
	paths := make([]string, 0, len(doc.Paths))
	for path := range doc.Paths {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	seq := 1
	for _, pathName := range paths {
		pathItem := doc.Paths[pathName]
		for _, method := range methods {
			operation, ok := pathItem.Operation(method)
			if !ok {
				continue
			}
			operations := append([]openAPIOperation{operation}, operation.BrunoVariants...)
			for _, operation := range operations {
				item := openAPIRequestItem(doc, raw, pathItem, operation, method, pathName, groupBy, "", "", nil, seq)
				collection.Items = append(collection.Items, item)
				seq++
				openAPIAppendCallbackItems(&collection, doc, raw, operation, item, groupBy, &seq)
			}
		}
	}
	openAPIAppendWebhookItems(&collection, doc, raw, groupBy, &seq)
	if len(collection.Items) == 0 {
		return types.Collection{}, errors.New("openapi document contains no supported operations")
	}
	return collection, nil
}

type OpenAPIDoc struct {
	OpenAPI  string                 `json:"openapi" yaml:"openapi"`
	Swagger  string                 `json:"swagger" yaml:"swagger"`
	Info     openAPIInfo            `json:"info" yaml:"info"`
	Servers  []openAPIServer        `json:"servers" yaml:"servers"`
	Paths    map[string]openAPIPath `json:"paths" yaml:"paths"`
	Webhooks map[string]openAPIPath `json:"webhooks" yaml:"webhooks"`
	Security []map[string][]string  `json:"security" yaml:"security"`
}

type openAPIInfo struct {
	Title   string `json:"title" yaml:"title"`
	Version string `json:"version" yaml:"version"`
}

type openAPIServer struct {
	URL       string                           `json:"url" yaml:"url"`
	Variables map[string]openAPIServerVariable `json:"variables" yaml:"variables"`
}

type openAPIServerVariable struct {
	Default interface{}   `json:"default" yaml:"default"`
	Enum    []interface{} `json:"enum" yaml:"enum"`
}

type openAPIPath struct {
	Parameters []openAPIParameter `json:"parameters" yaml:"parameters"`
	Servers    []openAPIServer    `json:"servers" yaml:"servers"`
	Get        *openAPIOperation  `json:"get" yaml:"get"`
	Post       *openAPIOperation  `json:"post" yaml:"post"`
	Put        *openAPIOperation  `json:"put" yaml:"put"`
	Patch      *openAPIOperation  `json:"patch" yaml:"patch"`
	Delete     *openAPIOperation  `json:"delete" yaml:"delete"`
	Head       *openAPIOperation  `json:"head" yaml:"head"`
	Options    *openAPIOperation  `json:"options" yaml:"options"`
	Trace      *openAPIOperation  `json:"trace" yaml:"trace"`
}

func (p openAPIPath) Operation(method string) (openAPIOperation, bool) {
	var op *openAPIOperation
	switch strings.ToLower(method) {
	case "get":
		op = p.Get
	case "post":
		op = p.Post
	case "put":
		op = p.Put
	case "patch":
		op = p.Patch
	case "delete":
		op = p.Delete
	case "head":
		op = p.Head
	case "options":
		op = p.Options
	case "trace":
		op = p.Trace
	}
	if op == nil {
		return openAPIOperation{}, false
	}
	return *op, true
}

func openAPIRequestItem(doc OpenAPIDoc, root map[string]interface{}, pathItem openAPIPath, operation openAPIOperation, method, pathName, groupBy, folderPrefix, urlOverride string, extraVars []types.Variable, seq int) types.RequestItem {
	item := types.NewRequestItem(openAPIOperationName(operation, method, pathName), "http", seq)
	item.Method = strings.ToUpper(method)
	item.URL = scalar.FirstNonEmpty(strings.TrimSpace(urlOverride), "{{baseUrl}}"+convertOpenAPIPath(pathName))
	if serverVars := openAPIOperationServerVariables(operation, pathItem); len(serverVars) > 0 {
		item.Vars.Req = append(item.Vars.Req, serverVars...)
	}
	item.Vars.Req = append(item.Vars.Req, extraVars...)
	item.Tags = sanitizeOpenAPITags(operation.Tags)
	item.FolderPath = openAPIJoinFolderPath(folderPrefix, openAPIFolderPath(operation, pathName, groupBy))
	params := mergeOpenAPIParams(pathItem.Parameters, operation.Parameters)
	item.PathParams = openAPIParams(params, "path")
	item.Params = openAPIParams(params, "query")
	item.Headers = openAPIParams(params, "header")
	if auth, ok := openAPIAuth(operation, doc, root); ok {
		item.Auth = auth
		item.Headers, item.Params = openAPIVisibleAuthRows(item.Auth, item.Headers, item.Params)
	}
	item.Docs = operation.Description
	if item.Docs == "" {
		item.Docs = operation.Summary
	}
	if operation.RequestBody != nil {
		if body, contentType, ok := openAPIRequestBody(operation.RequestBody, root); ok {
			item.Body = body
			if contentType != "" {
				item.Headers = appendHeaderIfMissing(item.Headers, "Content-Type", contentType)
			}
		}
	}
	if linkScript := openAPIResponseLinkScript(operation, root); linkScript != "" {
		item.PostScript = scalar.AppendScript(item.PostScript, linkScript)
	}
	item.Examples = openAPIResponseExamples(operation, item, root)
	return item
}

func openAPIAppendCallbackItems(collection *types.Collection, doc OpenAPIDoc, root map[string]interface{}, operation openAPIOperation, parent types.RequestItem, groupBy string, seq *int) {
	if len(operation.Callbacks) == 0 {
		return
	}
	methods := []string{"get", "post", "put", "patch", "delete", "head", "options", "trace"}
	names := make([]string, 0, len(operation.Callbacks))
	for name := range operation.Callbacks {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		expressions := operation.Callbacks[name]
		keys := make([]string, 0, len(expressions))
		for expression := range expressions {
			keys = append(keys, expression)
		}
		sort.Strings(keys)
		for _, expression := range keys {
			pathItem := expressions[expression]
			urlValue, vars := openAPIEventURL(expression)
			folderPrefix := openAPIJoinFolderPath(parent.FolderPath, "Callbacks", scalar.SanitizeFilename(scalar.FirstNonEmpty(name, "callback")))
			for _, method := range methods {
				callbackOperation, ok := pathItem.Operation(method)
				if !ok {
					continue
				}
				item := openAPIRequestItem(doc, root, pathItem, callbackOperation, method, expression, groupBy, folderPrefix, urlValue, vars, *seq)
				collection.Items = append(collection.Items, item)
				*seq = *seq + 1
			}
		}
	}
}

func openAPIAppendWebhookItems(collection *types.Collection, doc OpenAPIDoc, root map[string]interface{}, groupBy string, seq *int) {
	if len(doc.Webhooks) == 0 {
		return
	}
	methods := []string{"get", "post", "put", "patch", "delete", "head", "options", "trace"}
	names := make([]string, 0, len(doc.Webhooks))
	for name := range doc.Webhooks {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		pathItem := doc.Webhooks[name]
		urlValue, vars := openAPIWebhookURL(name)
		folderName := scalar.SanitizeFilename(strings.TrimPrefix(name, "/"))
		if folderName == "" || folderName == "untitled" {
			folderName = "webhook"
		}
		folderPrefix := openAPIJoinFolderPath("Webhooks", folderName)
		pathName := name
		if !strings.HasPrefix(strings.TrimSpace(pathName), "/") {
			pathName = "/webhooks/" + pathName
		}
		for _, method := range methods {
			operation, ok := pathItem.Operation(method)
			if !ok {
				continue
			}
			item := openAPIRequestItem(doc, root, pathItem, operation, method, pathName, groupBy, folderPrefix, urlValue, vars, *seq)
			collection.Items = append(collection.Items, item)
			*seq = *seq + 1
		}
	}
}

func openAPIJoinFolderPath(parts ...string) string {
	cleaned := []string{}
	for _, part := range parts {
		part = strings.Trim(strings.TrimSpace(part), "/")
		if part == "" {
			continue
		}
		cleaned = append(cleaned, part)
	}
	return strings.Join(cleaned, "/")
}

func openAPIEventURL(expression string) (string, []types.Variable) {
	value := strings.TrimSpace(expression)
	if value == "" {
		return "{{callbackUrl}}", []types.Variable{openAPIEventURLVariable("callbackUrl", expression)}
	}
	if strings.HasPrefix(value, "http://") || strings.HasPrefix(value, "https://") || strings.HasPrefix(value, "{{") {
		return value, nil
	}
	if strings.HasPrefix(value, "/") {
		return "{{baseUrl}}" + convertOpenAPIPath(value), nil
	}
	if name := openAPIEventExpressionVariableName(value); name != "" {
		return "{{" + name + "}}", []types.Variable{openAPIEventURLVariable(name, expression)}
	}
	return "{{" + sanitizeVariableName(scalar.FirstNonEmpty(value, "callbackUrl")) + "}}", []types.Variable{openAPIEventURLVariable(sanitizeVariableName(scalar.FirstNonEmpty(value, "callbackUrl")), expression)}
}

func openAPIWebhookURL(name string) (string, []types.Variable) {
	value := strings.TrimSpace(name)
	if value == "" {
		return "{{baseUrl}}/webhooks/webhook", nil
	}
	if strings.HasPrefix(value, "http://") || strings.HasPrefix(value, "https://") || strings.HasPrefix(value, "{{") {
		return value, nil
	}
	if strings.HasPrefix(value, "/") {
		return "{{baseUrl}}" + convertOpenAPIPath(value), nil
	}
	return "{{baseUrl}}" + convertOpenAPIPath("/webhooks/"+value), nil
}

func openAPIEventExpressionVariableName(expression string) string {
	inner := strings.TrimSpace(expression)
	if strings.HasPrefix(inner, "{") && strings.HasSuffix(inner, "}") {
		inner = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(inner, "{"), "}"))
	}
	if inner == "" {
		return ""
	}
	if strings.Contains(inner, "#/") {
		_, pointer, _ := strings.Cut(inner, "#/")
		if name := sanitizeVariableName(lastPathToken(pointer)); name != "" {
			return name
		}
	}
	if strings.Contains(inner, ".") {
		if name := sanitizeVariableName(lastPathToken(strings.ReplaceAll(inner, ".", "/"))); name != "" {
			return name
		}
	}
	return sanitizeVariableName(inner)
}

func lastPathToken(value string) string {
	parts := strings.FieldsFunc(value, func(r rune) bool {
		return r == '/' || r == '.' || r == '[' || r == ']' || r == '\'' || r == '"' || r == '#'
	})
	for index := len(parts) - 1; index >= 0; index-- {
		if strings.TrimSpace(parts[index]) != "" {
			return parts[index]
		}
	}
	return ""
}

func sanitizeVariableName(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	var b strings.Builder
	upperNext := false
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
			if b.Len() == 0 && r >= '0' && r <= '9' {
				b.WriteString("value")
			}
			if upperNext && r >= 'a' && r <= 'z' {
				r = r - ('a' - 'A')
			}
			b.WriteRune(r)
			upperNext = false
			continue
		}
		upperNext = b.Len() > 0
	}
	if b.Len() == 0 {
		return ""
	}
	return b.String()
}

func openAPIEventURLVariable(name, expression string) types.Variable {
	return types.Variable{
		ID:       scalar.DeterministicID("var", "openapi-event-url:"+name+":"+expression),
		Name:     name,
		Value:    strings.TrimSpace(expression),
		DataType: "string",
		Enabled:  true,
	}
}

type openAPIOperation struct {
	OperationID   string                            `json:"operationId" yaml:"operationId"`
	Summary       string                            `json:"summary" yaml:"summary"`
	Description   string                            `json:"description" yaml:"description"`
	Tags          []string                          `json:"tags" yaml:"tags"`
	Parameters    []openAPIParameter                `json:"parameters" yaml:"parameters"`
	RequestBody   map[string]interface{}            `json:"requestBody" yaml:"requestBody"`
	Responses     map[string]interface{}            `json:"responses" yaml:"responses"`
	Callbacks     map[string]map[string]openAPIPath `json:"callbacks" yaml:"callbacks"`
	Security      []map[string][]string             `json:"security" yaml:"security"`
	Servers       []openAPIServer                   `json:"servers" yaml:"servers"`
	BrunoVariants []openAPIOperation                `json:"x-bruno-variants" yaml:"x-bruno-variants"`
}

type openAPIParameter struct {
	Name        string                 `json:"name" yaml:"name"`
	In          string                 `json:"in" yaml:"in"`
	Required    bool                   `json:"required" yaml:"required"`
	Description string                 `json:"description" yaml:"description"`
	Example     interface{}            `json:"example" yaml:"example"`
	Examples    map[string]interface{} `json:"examples" yaml:"examples"`
	Schema      map[string]interface{} `json:"schema" yaml:"schema"`
}

func openAPIOperationName(operation openAPIOperation, method, pathName string) string {
	if operation.Summary != "" {
		return scalar.NormalizeWhitespace(operation.Summary)
	}
	if operation.OperationID != "" {
		return scalar.NormalizeWhitespace(operation.OperationID)
	}
	if operation.Description != "" {
		return scalar.NormalizeWhitespace(operation.Description)
	}
	return strings.ToUpper(method) + " " + pathName
}

func sanitizeOpenAPITags(tags []string) []string {
	result := make([]string, 0, len(tags))
	seen := map[string]bool{}
	for _, tag := range tags {
		cleaned := scalar.SanitizeFilename(scalar.NormalizeWhitespace(tag))
		if cleaned == "" || cleaned == "untitled" || seen[cleaned] {
			continue
		}
		seen[cleaned] = true
		result = append(result, cleaned)
	}
	return result
}

func openAPIFolderPath(operation openAPIOperation, pathName, groupBy string) string {
	if strings.EqualFold(strings.TrimSpace(groupBy), "path") {
		return openAPIPathFolder(pathName)
	}
	tags := sanitizeOpenAPITags(operation.Tags)
	if len(tags) == 0 {
		return ""
	}
	return tags[0]
}

func openAPIPathFolder(pathName string) string {
	parts := strings.Split(strings.Trim(pathName, "/"), "/")
	cleaned := make([]string, 0, len(parts))
	for _, part := range parts {
		name := scalar.SanitizeFilename(part)
		if name == "" || name == "untitled" {
			continue
		}
		cleaned = append(cleaned, name)
	}
	return strings.Join(cleaned, "/")
}

func openAPIParams(params []openAPIParameter, location string) []types.KeyValue {
	rows := []types.KeyValue{}
	for _, param := range params {
		if param.In == location && param.Name != "" {
			rows = append(rows, types.KeyValue{Name: param.Name, Value: openAPIParameterValue(param), Enabled: param.Required, Description: param.Description})
		}
	}
	return rows
}

func openAPIParameterValue(param openAPIParameter) string {
	if value, ok := openAPIStringValue(param.Example); ok {
		return value
	}
	if value, ok := openAPIExampleValue(param.Examples); ok {
		return value
	}
	if value, ok := openAPIStringValue(scalar.FirstMapValue(param.Schema, "default", "example")); ok {
		return value
	}
	if value, ok := openAPIExampleValueFromRaw(param.Schema["examples"]); ok {
		return value
	}
	if value, ok := openAPIStringValue(param.Schema["minimum"]); ok {
		return value
	}
	if strings.EqualFold(scalar.YAMLString(param.Schema["type"]), "array") {
		if items, ok := scalar.Map(param.Schema["items"]); ok {
			if value, ok := openAPIStringValue(scalar.FirstMapValue(items, "default", "example", "minimum")); ok {
				return value
			}
		}
	}
	return ""
}

func openAPIExampleValueFromRaw(raw interface{}) (string, bool) {
	value, ok := openAPIExampleRawValue(raw)
	if !ok {
		return "", false
	}
	return openAPIStringValue(value)
}

func resolveOpenAPIRequestBody(raw map[string]interface{}, root map[string]interface{}) map[string]interface{} {
	if ref := scalar.YAMLString(raw["$ref"]); ref != "" {
		if resolved, ok := openAPIResolveRef(root, ref); ok {
			if resolvedMap, ok := scalar.Map(resolved); ok {
				return resolvedMap
			}
		}
	}
	return raw
}

func openAPIParameterValueFromSchema(raw interface{}) string {
	schema, ok := scalar.Map(raw)
	if !ok {
		return ""
	}
	if value, ok := openAPIStringValue(scalar.FirstMapValue(schema, "default", "example")); ok {
		return value
	}
	if value, ok := openAPIExampleValueFromRaw(schema["examples"]); ok {
		return value
	}
	if value, ok := openAPIStringValue(schema["minimum"]); ok {
		return value
	}
	return ""
}

func openAPIAuth(operation openAPIOperation, doc OpenAPIDoc, root map[string]interface{}) (types.AuthConfig, bool) {
	security := operation.Security
	if security == nil {
		security = doc.Security
	}
	if security == nil {
		return types.AuthConfig{}, false
	}
	if len(security) == 0 {
		return types.AuthConfig{Mode: "none", APILocation: "header"}, true
	}
	for _, requirement := range security {
		if len(requirement) == 0 {
			return types.AuthConfig{Mode: "none", APILocation: "header"}, true
		}
		names := make([]string, 0, len(requirement))
		for name := range requirement {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			if auth, ok := openAPISecuritySchemeAuth(name, root); ok {
				return auth, true
			}
		}
	}
	return types.AuthConfig{}, false
}

func openAPISecuritySchemeAuth(name string, root map[string]interface{}) (types.AuthConfig, bool) {
	components, ok := scalar.Map(root["components"])
	if !ok {
		return types.AuthConfig{}, false
	}
	schemes, ok := scalar.Map(components["securitySchemes"])
	if !ok {
		return types.AuthConfig{}, false
	}
	scheme, ok := scalar.Map(schemes[name])
	if !ok {
		return types.AuthConfig{}, false
	}
	switch strings.ToLower(scalar.YAMLString(scheme["type"])) {
	case "apikey":
		location := strings.ToLower(scalar.FirstNonEmpty(scalar.YAMLString(scheme["in"]), "header"))
		if location != "query" {
			location = "header"
		}
		return types.AuthConfig{Mode: "apikey", APIKey: scalar.YAMLString(scheme["name"]), APILocation: location}, true
	case "http":
		switch strings.ToLower(scalar.YAMLString(scheme["scheme"])) {
		case "bearer":
			return types.AuthConfig{Mode: "bearer", APILocation: "header"}, true
		case "basic":
			return types.AuthConfig{Mode: "basic", APILocation: "header"}, true
		case "digest":
			return types.AuthConfig{Mode: "digest", APILocation: "header"}, true
		}
	case "oauth2", "openidconnect":
		auth := types.AuthConfig{Mode: "oauth2", APILocation: "header"}
		if flows, ok := scalar.Map(scheme["flows"]); ok {
			if flow, ok := scalar.Map(scalar.FirstMapValue(flows, "clientCredentials", "client_credentials", "password", "authorizationCode", "authorization_code")); ok {
				auth.OAuth2.AccessTokenURL = scalar.FirstYAMLString(flow, "tokenUrl", "token_url")
				auth.OAuth2.Scope = strings.Join(openAPIScopeNames(flow["scopes"]), " ")
			}
		}
		return auth, true
	}
	return types.AuthConfig{}, false
}

func openAPIVisibleAuthRows(auth types.AuthConfig, headers, params []types.KeyValue) ([]types.KeyValue, []types.KeyValue) {
	if auth.Mode != "apikey" || strings.TrimSpace(auth.APIKey) == "" {
		return headers, params
	}
	row := types.KeyValue{Name: auth.APIKey, Value: auth.APIValue, Enabled: true}
	if strings.EqualFold(auth.APILocation, "query") {
		return headers, appendKeyValueIfMissing(params, row)
	}
	return appendKeyValueIfMissing(headers, row), params
}

func appendKeyValueIfMissing(rows []types.KeyValue, row types.KeyValue) []types.KeyValue {
	for _, existing := range rows {
		if strings.EqualFold(existing.Name, row.Name) {
			return rows
		}
	}
	return append(rows, row)
}

func openAPIScopeNames(raw interface{}) []string {
	scopes, ok := scalar.Map(raw)
	if !ok {
		return nil
	}
	names := make([]string, 0, len(scopes))
	for name := range scopes {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func openAPIRequestBody(raw map[string]interface{}, root map[string]interface{}) (types.RequestBody, string, bool) {
	raw = resolveOpenAPIRequestBody(raw, root)
	content, ok := scalar.Map(raw["content"])
	if !ok || len(content) == 0 {
		return types.RequestBody{}, "", false
	}
	contentType, media := openAPIPreferredMedia(content)
	if contentType == "" {
		return types.RequestBody{}, "", false
	}
	value := openAPIMediaExample(media, root)
	mode := openAPIBodyMode(contentType)
	body := types.RequestBody{Mode: mode}
	switch mode {
	case "json":
		if value == nil {
			value = map[string]interface{}{}
		}
		body.JSON = openAPIJSONString(value)
	case "xml":
		if text, ok := openAPIStringValue(value); ok {
			body.XML = text
		} else {
			body.XML = "<root></root>"
		}
	case "formUrlEncoded":
		body.FormURLEncoded = openAPIFormValues(value)
	case "multipartForm":
		body.Multipart = openAPIMultipartValues(value)
	case "text":
		body.Text, _ = openAPIStringValue(value)
	}
	return body, contentType, true
}

func openAPIPreferredMedia(content map[string]interface{}) (string, map[string]interface{}) {
	preferred := []string{"application/json", "application/*+json", "application/x-www-form-urlencoded", "multipart/form-data", "application/xml", "text/xml", "text/plain"}
	for _, mediaType := range preferred {
		if media, ok := openAPIMedia(content, mediaType); ok {
			return mediaType, media
		}
	}
	keys := make([]string, 0, len(content))
	for key := range content {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if media, ok := scalar.Map(content[key]); ok {
			return key, media
		}
	}
	return "", nil
}

func openAPIMedia(content map[string]interface{}, mediaType string) (map[string]interface{}, bool) {
	if media, ok := scalar.Map(content[mediaType]); ok {
		return media, true
	}
	if !strings.Contains(mediaType, "*") {
		return nil, false
	}
	prefix, suffix, _ := strings.Cut(mediaType, "*")
	keys := make([]string, 0, len(content))
	for key := range content {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if strings.HasPrefix(key, prefix) && strings.HasSuffix(key, suffix) {
			if media, ok := scalar.Map(content[key]); ok {
				return media, true
			}
		}
	}
	return nil, false
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

func openAPIResolveRef(root map[string]interface{}, ref string) (interface{}, bool) {
	if !strings.HasPrefix(ref, "#/") {
		return nil, false
	}
	var current interface{} = root
	for _, part := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
		part = strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
		currentMap, ok := scalar.Map(current)
		if !ok {
			return nil, false
		}
		current, ok = currentMap[part]
		if !ok {
			return nil, false
		}
	}
	return current, true
}

func firstMapValueOK(raw map[string]interface{}, keys ...string) (interface{}, bool) {
	for _, key := range keys {
		if value, ok := raw[key]; ok {
			return value, true
		}
	}
	return nil, false
}

func copyBoolMap(values map[string]bool) map[string]bool {
	out := make(map[string]bool, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func openAPIJSONString(value interface{}) string {
	if text, ok := value.(string); ok && json.Valid([]byte(text)) {
		return text
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return "{}"
	}
	return string(data)
}

func openAPIStringValue(raw interface{}) (string, bool) {
	if raw == nil {
		return "", false
	}
	switch value := raw.(type) {
	case string:
		return value, true
	case []byte:
		return string(value), true
	default:
		data, err := json.Marshal(value)
		if err == nil {
			return string(data), true
		}
		return fmt.Sprint(value), true
	}
}

func openAPIBodyMode(contentType string) string {
	lower := strings.ToLower(contentType)
	switch {
	case strings.Contains(lower, "json"):
		return "json"
	case strings.Contains(lower, "xml"):
		return "xml"
	case strings.Contains(lower, "x-www-form-urlencoded"):
		return "formUrlEncoded"
	case strings.Contains(lower, "multipart/form-data"):
		return "multipartForm"
	default:
		return "text"
	}
}

func openAPIFormValues(value interface{}) []types.KeyValue {
	valueMap, ok := scalar.Map(value)
	if !ok {
		return nil
	}
	names := make([]string, 0, len(valueMap))
	for name := range valueMap {
		names = append(names, name)
	}
	sort.Strings(names)
	result := make([]types.KeyValue, 0, len(names))
	for _, name := range names {
		text, _ := openAPIStringValue(valueMap[name])
		result = append(result, types.KeyValue{Name: name, Value: text, Enabled: true})
	}
	return result
}

func openAPIMultipartValues(value interface{}) []types.FormPart {
	valueMap, ok := scalar.Map(value)
	if !ok {
		return nil
	}
	names := make([]string, 0, len(valueMap))
	for name := range valueMap {
		names = append(names, name)
	}
	sort.Strings(names)
	result := make([]types.FormPart, 0, len(names))
	for _, name := range names {
		text, _ := openAPIStringValue(valueMap[name])
		result = append(result, types.FormPart{Name: name, Value: text, Enabled: true})
	}
	return result
}

func appendHeaderIfMissing(headers []types.KeyValue, name, value string) []types.KeyValue {
	for _, header := range headers {
		if strings.EqualFold(header.Name, name) {
			return headers
		}
	}
	return append(headers, types.KeyValue{Name: name, Value: value, Enabled: true})
}

func mergeOpenAPIParams(pathParams, operationParams []openAPIParameter) []openAPIParameter {
	merged := make([]openAPIParameter, 0, len(pathParams)+len(operationParams))
	seen := map[string]bool{}
	for _, param := range operationParams {
		key := param.In + ":" + param.Name
		seen[key] = true
		merged = append(merged, param)
	}
	for _, param := range pathParams {
		key := param.In + ":" + param.Name
		if !seen[key] {
			merged = append(merged, param)
		}
	}
	return merged
}

func convertOpenAPIPath(pathName string) string {
	parts := strings.Split(pathName, "/")
	for i, part := range parts {
		if strings.HasPrefix(part, "{") && strings.HasSuffix(part, "}") && len(part) > 2 {
			parts[i] = ":" + strings.TrimSuffix(strings.TrimPrefix(part, "{"), "}")
		}
	}
	return strings.Join(parts, "/")
}

func convertOpenAPIServerURL(rawURL string) string {
	var b strings.Builder
	for i := 0; i < len(rawURL); {
		if rawURL[i] == '{' {
			if i+1 < len(rawURL) && rawURL[i+1] == '{' {
				b.WriteByte(rawURL[i])
				i++
				continue
			}
			if endOffset := strings.IndexByte(rawURL[i+1:], '}'); endOffset >= 0 {
				end := i + 1 + endOffset
				b.WriteString("{{")
				b.WriteString(rawURL[i+1 : end])
				b.WriteString("}}")
				i = end + 1
				continue
			}
		}
		b.WriteByte(rawURL[i])
		i++
	}
	return b.String()
}

func openAPIOperationServerVariables(operation openAPIOperation, pathItem openAPIPath) []types.Variable {
	servers := operation.Servers
	if len(servers) == 0 {
		servers = pathItem.Servers
	}
	if len(servers) == 0 {
		return nil
	}
	return openAPIServerVariables(servers[0])
}

func openAPIServerVariables(server openAPIServer) []types.Variable {
	serverURL := strings.TrimRight(strings.TrimSpace(server.URL), "/")
	if serverURL == "" {
		return nil
	}
	variables := []types.Variable{openAPIServerVariableRow("baseUrl", convertOpenAPIServerURL(serverURL))}
	if len(server.Variables) == 0 {
		return variables
	}
	names := make([]string, 0, len(server.Variables))
	for name := range server.Variables {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		variables = append(variables, openAPIServerVariableRow(name, openAPIServerVariableValue(server.Variables[name])))
	}
	return variables
}

func openAPIServerVariableRow(name, value string) types.Variable {
	return types.Variable{ID: scalar.NewID("var"), Name: name, Value: value, DataType: "string", Type: "string", Enabled: true}
}

func openAPIServerVariableValue(variable openAPIServerVariable) string {
	if variable.Default != nil {
		if value, ok := openAPIStringValue(variable.Default); ok {
			return value
		}
	}
	if len(variable.Enum) > 0 {
		if value, ok := openAPIStringValue(variable.Enum[0]); ok {
			return value
		}
	}
	return ""
}
