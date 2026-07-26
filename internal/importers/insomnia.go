// Insomnia v4 and v5 exports.
package importers

import (
	"LiteAPI/internal/scalar"
	"LiteAPI/internal/types"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Insomnia v4 and v5 exports.

type insomniaExport struct {
	Type         string              `json:"type" yaml:"type"`
	Name         string              `json:"name" yaml:"name"`
	Collection   []insomniaV5Item    `json:"collection" yaml:"collection"`
	Environments insomniaEnvironment `json:"environments" yaml:"environments"`
	Resources    []insomniaResource  `json:"resources" yaml:"resources"`
}

type insomniaResource struct {
	ID             string                 `json:"_id" yaml:"_id"`
	Type           string                 `json:"_type" yaml:"_type"`
	ParentID       string                 `json:"parentId" yaml:"parentId"`
	Name           string                 `json:"name" yaml:"name"`
	Method         string                 `json:"method" yaml:"method"`
	URL            string                 `json:"url" yaml:"url"`
	Headers        []insomniaNameValue    `json:"headers" yaml:"headers"`
	Parameters     []insomniaNameValue    `json:"parameters" yaml:"parameters"`
	PathParameters []insomniaNameValue    `json:"pathParameters" yaml:"pathParameters"`
	Authentication insomniaAuth           `json:"authentication" yaml:"authentication"`
	Body           insomniaBody           `json:"body" yaml:"body"`
	Data           map[string]interface{} `json:"data" yaml:"data"`
}

type insomniaV5Item struct {
	Name           string              `json:"name" yaml:"name"`
	Method         string              `json:"method" yaml:"method"`
	URL            string              `json:"url" yaml:"url"`
	Headers        []insomniaNameValue `json:"headers" yaml:"headers"`
	Parameters     []insomniaNameValue `json:"parameters" yaml:"parameters"`
	PathParameters []insomniaNameValue `json:"pathParameters" yaml:"pathParameters"`
	Authentication insomniaAuth        `json:"authentication" yaml:"authentication"`
	Body           insomniaBody        `json:"body" yaml:"body"`
	Children       []insomniaV5Item    `json:"children" yaml:"children"`
}

type insomniaNameValue struct {
	Name        string      `json:"name" yaml:"name"`
	Value       interface{} `json:"value" yaml:"value"`
	Description string      `json:"description" yaml:"description"`
	Disabled    bool        `json:"disabled" yaml:"disabled"`
}

type insomniaAuth struct {
	Type     string `json:"type" yaml:"type"`
	Username string `json:"username" yaml:"username"`
	Password string `json:"password" yaml:"password"`
	Token    string `json:"token" yaml:"token"`
}

type insomniaBody struct {
	MimeType string              `json:"mimeType" yaml:"mimeType"`
	Text     string              `json:"text" yaml:"text"`
	Params   []insomniaNameValue `json:"params" yaml:"params"`
}

type insomniaEnvironment struct {
	Name            string                 `json:"name" yaml:"name"`
	ParentID        string                 `json:"parentId" yaml:"parentId"`
	Data            map[string]interface{} `json:"data" yaml:"data"`
	SubEnvironments []insomniaEnvironment  `json:"subEnvironments" yaml:"subEnvironments"`
}

func ImportInsomnia(content, fallbackName string) (types.Collection, error) {
	var raw insomniaExport
	if err := yaml.Unmarshal([]byte(content), &raw); err != nil {
		return types.Collection{}, err
	}
	now := time.Now()
	collection := types.Collection{
		ID:             scalar.NewID("collection"),
		Name:           scalar.FirstNonEmpty(strings.TrimSpace(raw.Name), strings.TrimSpace(fallbackName), "Insomnia Collection"),
		Format:         "insomnia",
		Auth:           types.AuthConfig{Mode: "none"},
		SecurityConfig: types.CollectionSecurityConfig{JSSandboxMode: "safe"},
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	seq := 1
	if strings.HasPrefix(raw.Type, "collection.insomnia.rest/5") || len(raw.Collection) > 0 {
		appendInsomniaV5Items(&collection, raw.Collection, "", &seq)
		collection.Environments = insomniaV5Environments(raw.Environments)
		return collection, nil
	}
	workspace := insomniaWorkspace(raw.Resources)
	if workspace.ID == "" {
		return types.Collection{}, errors.New("collection not found inside Insomnia export")
	}
	collection.Name = scalar.FirstNonEmpty(strings.TrimSpace(workspace.Name), collection.Name)
	appendInsomniaV4Items(&collection, raw.Resources, workspace.ID, "", &seq)
	collection.Environments = insomniaV4Environments(raw.Resources, workspace.ID)
	return collection, nil
}

func appendInsomniaV4Items(collection *types.Collection, resources []insomniaResource, parentID, folderPath string, seq *int) {
	for _, folder := range insomniaChildFolders(resources, parentID) {
		childPath := postmanFolderPath(folderPath, folder.Name)
		appendInsomniaFolder(collection, folder.Name, childPath)
		appendInsomniaV4Items(collection, resources, folder.ID, childPath, seq)
	}
	for _, request := range insomniaChildRequests(resources, parentID) {
		item := insomniaRequestItem(insomniaRequestData{
			Name:           request.Name,
			Method:         request.Method,
			URL:            request.URL,
			Headers:        request.Headers,
			Parameters:     request.Parameters,
			PathParameters: request.PathParameters,
			Authentication: request.Authentication,
			Body:           request.Body,
		}, folderPath, *seq)
		collection.Items = append(collection.Items, item)
		*seq = *seq + 1
	}
}

func appendInsomniaV5Items(collection *types.Collection, items []insomniaV5Item, folderPath string, seq *int) {
	for _, raw := range items {
		if len(raw.Children) > 0 {
			childPath := postmanFolderPath(folderPath, raw.Name)
			appendInsomniaFolder(collection, raw.Name, childPath)
			appendInsomniaV5Items(collection, raw.Children, childPath, seq)
			continue
		}
		if strings.TrimSpace(raw.Method) == "" && strings.TrimSpace(raw.URL) == "" {
			continue
		}
		item := insomniaRequestItem(insomniaRequestData{
			Name:           raw.Name,
			Method:         raw.Method,
			URL:            raw.URL,
			Headers:        raw.Headers,
			Parameters:     raw.Parameters,
			PathParameters: raw.PathParameters,
			Authentication: raw.Authentication,
			Body:           raw.Body,
		}, folderPath, *seq)
		collection.Items = append(collection.Items, item)
		*seq = *seq + 1
	}
}

type insomniaRequestData struct {
	Name           string
	Method         string
	URL            string
	Headers        []insomniaNameValue
	Parameters     []insomniaNameValue
	PathParameters []insomniaNameValue
	Authentication insomniaAuth
	Body           insomniaBody
}

func insomniaRequestItem(request insomniaRequestData, folderPath string, seq int) types.RequestItem {
	item := types.NewRequestItem(scalar.FirstNonEmpty(request.Name, "Untitled Request"), "http", seq)
	item.FolderPath = folderPath
	item.Method = strings.ToUpper(scalar.FirstNonEmpty(request.Method, http.MethodGet))
	item.URL = insomniaNormalizeVariables(request.URL)
	item.Headers = insomniaKeyValues(request.Headers, false)
	item.Params = insomniaKeyValues(request.Parameters, false)
	item.PathParams = insomniaPathParams(request.PathParameters)
	item.Auth = insomniaAuthConfig(request.Authentication)
	item.Body = insomniaRequestBody(request.Body)
	if item.Body.Mode == "graphql" {
		item.Type = "graphql"
	}
	return item
}

func appendInsomniaFolder(collection *types.Collection, name, folderPath string) {
	if folderPath == "" {
		return
	}
	collection.Folders = append(collection.Folders, types.FolderConfig{
		Path:        folderPath,
		DisplayPath: folderPath,
		Name:        scalar.FirstNonEmpty(strings.TrimSpace(name), "Untitled Folder"),
		Seq:         len(collection.Folders) + 1,
	})
}

func insomniaWorkspace(resources []insomniaResource) insomniaResource {
	for _, resource := range resources {
		if resource.Type == "workspace" {
			return resource
		}
	}
	return insomniaResource{}
}

func insomniaChildFolders(resources []insomniaResource, parentID string) []insomniaResource {
	result := []insomniaResource{}
	for _, resource := range resources {
		if resource.Type == "request_group" && resource.ParentID == parentID {
			result = append(result, resource)
		}
	}
	return result
}

func insomniaChildRequests(resources []insomniaResource, parentID string) []insomniaResource {
	result := []insomniaResource{}
	for _, resource := range resources {
		if resource.Type == "request" && resource.ParentID == parentID {
			result = append(result, resource)
		}
	}
	return result
}

func insomniaKeyValues(values []insomniaNameValue, forceEnabled bool) []types.KeyValue {
	rows := make([]types.KeyValue, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value.Name) == "" && value.Value == nil {
			continue
		}
		enabled := !value.Disabled
		if forceEnabled {
			enabled = true
		}
		rows = append(rows, types.KeyValue{
			Name:        value.Name,
			Value:       insomniaNormalizeVariables(scalar.YAMLString(value.Value)),
			Enabled:     enabled,
			Description: value.Description,
		})
	}
	return rows
}

func insomniaPathParams(values []insomniaNameValue) []types.KeyValue {
	rows := make([]types.KeyValue, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value.Name) == "" && value.Value == nil {
			continue
		}
		rows = append(rows, types.KeyValue{
			Name:        value.Name,
			Value:       insomniaNormalizeVariables(scalar.YAMLString(value.Value)),
			Enabled:     true,
			Description: value.Description,
		})
	}
	return rows
}

func insomniaAuthConfig(auth insomniaAuth) types.AuthConfig {
	switch strings.ToLower(strings.TrimSpace(auth.Type)) {
	case "basic":
		return types.AuthConfig{Mode: "basic", Username: insomniaNormalizeVariables(auth.Username), Password: insomniaNormalizeVariables(auth.Password), APILocation: "header"}
	case "bearer":
		return types.AuthConfig{Mode: "bearer", Token: insomniaNormalizeVariables(auth.Token), APILocation: "header"}
	default:
		return types.AuthConfig{Mode: "none", APILocation: "header"}
	}
}

func insomniaRequestBody(body insomniaBody) types.RequestBody {
	result := types.RequestBody{Mode: "none"}
	mimeType := strings.ToLower(strings.TrimSpace(strings.Split(body.MimeType, ";")[0]))
	text := insomniaNormalizeVariables(body.Text)
	switch {
	case mimeType == "application/json":
		result.Mode = "json"
		result.JSON = text
	case mimeType == "application/x-www-form-urlencoded":
		result.Mode = "formUrlEncoded"
		result.FormURLEncoded = insomniaKeyValues(body.Params, false)
	case mimeType == "multipart/form-data":
		result.Mode = "multipartForm"
		result.Multipart = insomniaMultipartValues(body.Params)
	case mimeType == "text/xml" || mimeType == "application/xml":
		result.Mode = "xml"
		result.XML = text
	case mimeType == "application/graphql":
		result.Mode = "graphql"
		result.GraphQLQuery, result.GraphQLVariables = insomniaGraphQLBody(text)
	case mimeType == "text/plain" || (mimeType == "" && strings.TrimSpace(text) != ""):
		result.Mode = "text"
		result.Text = text
	}
	return result
}

func insomniaMultipartValues(values []insomniaNameValue) []types.FormPart {
	parts := make([]types.FormPart, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value.Name) == "" && value.Value == nil {
			continue
		}
		parts = append(parts, types.FormPart{Name: value.Name, Value: insomniaNormalizeVariables(scalar.YAMLString(value.Value)), Enabled: !value.Disabled})
	}
	return parts
}

func insomniaGraphQLBody(text string) (string, string) {
	payload := map[string]interface{}{}
	if err := json.Unmarshal([]byte(text), &payload); err != nil {
		return "", ""
	}
	query := insomniaNormalizeVariables(scalar.YAMLString(payload["query"]))
	variables := ""
	if rawVars, ok := payload["variables"]; ok {
		if data, err := json.MarshalIndent(rawVars, "", "  "); err == nil {
			variables = string(data)
		}
	}
	return query, variables
}

func insomniaV4Environments(resources []insomniaResource, workspaceID string) []types.Environment {
	envs := []insomniaEnvironment{}
	var base insomniaEnvironment
	for _, resource := range resources {
		if resource.Type != "environment" {
			continue
		}
		env := insomniaEnvironment{Name: resource.Name, ParentID: resource.ParentID, Data: resource.Data}
		if resource.ParentID == workspaceID && base.Data == nil {
			base = env
			envs = append(envs, env)
		}
	}
	baseFlat := flattenInsomniaEnv(base.Data)
	for _, resource := range resources {
		if resource.Type != "environment" || resource.ParentID == workspaceID {
			continue
		}
		merged := map[string]interface{}{}
		for key, value := range baseFlat {
			merged[key] = value
		}
		for key, value := range flattenInsomniaEnv(resource.Data) {
			merged[key] = value
		}
		envs = append(envs, insomniaEnvironment{Name: resource.Name, Data: merged})
	}
	return insomniaEnvironments(envs)
}

func insomniaV5Environments(base insomniaEnvironment) []types.Environment {
	if base.Data == nil && len(base.SubEnvironments) == 0 && strings.TrimSpace(base.Name) == "" {
		return nil
	}
	envs := []insomniaEnvironment{base}
	baseFlat := flattenInsomniaEnv(base.Data)
	for _, sub := range base.SubEnvironments {
		merged := map[string]interface{}{}
		for key, value := range baseFlat {
			merged[key] = value
		}
		for key, value := range flattenInsomniaEnv(sub.Data) {
			merged[key] = value
		}
		envs = append(envs, insomniaEnvironment{Name: sub.Name, Data: merged})
	}
	return insomniaEnvironments(envs)
}

func insomniaEnvironments(values []insomniaEnvironment) []types.Environment {
	result := make([]types.Environment, 0, len(values))
	for index, env := range values {
		flat := flattenInsomniaEnv(env.Data)
		names := make([]string, 0, len(flat))
		for name := range flat {
			names = append(names, name)
		}
		sort.Strings(names)
		variables := make([]types.Variable, 0, len(names))
		for _, name := range names {
			variables = append(variables, types.Variable{
				ID:       scalar.NewID("var"),
				Name:     name,
				Value:    scalar.YAMLString(flat[name]),
				Type:     "text",
				DataType: "string",
				Enabled:  true,
			})
		}
		result = append(result, types.Environment{
			ID:        scalar.NewID("env"),
			Name:      scalar.FirstNonEmpty(strings.TrimSpace(env.Name), fmt.Sprintf("Environment %d", index+1)),
			Variables: variables,
		})
	}
	return result
}

func flattenInsomniaEnv(values map[string]interface{}) map[string]interface{} {
	result := map[string]interface{}{}
	var walk func(prefix string, raw interface{})
	walk = func(prefix string, raw interface{}) {
		if valueMap, ok := scalar.Map(raw); ok {
			keys := make([]string, 0, len(valueMap))
			for key := range valueMap {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				next := key
				if prefix != "" {
					next = prefix + "." + key
				}
				walk(next, valueMap[key])
			}
			return
		}
		if prefix != "" {
			result[prefix] = raw
		}
	}
	walk("", values)
	return result
}

func insomniaNormalizeVariables(value string) string {
	return regexp.MustCompile(`\{\{.*?\}\}`).ReplaceAllStringFunc(value, func(match string) string {
		cleaned := strings.ReplaceAll(match, "_.", "")
		cleaned = strings.ReplaceAll(cleaned, " ", "")
		return cleaned
	})
}
