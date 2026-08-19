package bru

// YAML helpers that share this package with the .bru parser but read a different format.
//
// Split out by AST: declarations are identified by the parser and copied
// verbatim from their source offsets.

import (
	"strings"

	"github.com/mutexdev/lite_api/internal/scalar"
	"github.com/mutexdev/lite_api/internal/types"
)

func AssignYAMLBodyData(body *types.RequestBody, mode string, data interface{}) {
	switch mode {
	case "json":
		body.JSON = scalar.YAMLString(data)
	case "xml":
		body.XML = scalar.YAMLString(data)
	case "formUrlEncoded":
		body.FormURLEncoded = ParseYAMLKeyValues(data, false)
	case "multipartForm":
		body.Multipart = ParseYAMLMultipart(data)
	case "file":
		body.Files = ParseYAMLFileBody(data)
		body.FilePath, body.FileContentType = types.SelectedFileBodyFields(body.Files)
	case "none", "":
		body.Mode = "none"
	default:
		body.Text = scalar.YAMLString(data)
	}
}

func ParseYAMLKeyValues(raw interface{}, queryOnly bool) []types.KeyValue {
	values, ok := scalar.ListValue(raw)
	if !ok {
		return nil
	}
	result := make([]types.KeyValue, 0, len(values))
	for _, entry := range values {
		valueMap, ok := scalar.Map(entry)
		if !ok {
			continue
		}
		if queryOnly {
			paramType := strings.ToLower(scalar.YAMLString(valueMap["type"]))
			if paramType != "" && paramType != "query" {
				continue
			}
		}
		name := strings.TrimSpace(scalar.YAMLString(valueMap["name"]))
		if name == "" {
			continue
		}
		enabled := YAMLEnabled(valueMap)
		result = append(result, types.KeyValue{
			Name:        name,
			Value:       scalar.YAMLString(valueMap["value"]),
			Enabled:     enabled,
			Secret:      scalar.BoolValue(valueMap["secret"], false),
			Description: scalar.YAMLString(valueMap["description"]),
		})
	}
	return result
}

func ParseYAMLMultipart(raw interface{}) []types.FormPart {
	values, ok := scalar.ListValue(raw)
	if !ok {
		return nil
	}
	parts := make([]types.FormPart, 0, len(values))
	for _, entry := range values {
		valueMap, ok := scalar.Map(entry)
		if !ok {
			continue
		}
		name := strings.TrimSpace(scalar.YAMLString(valueMap["name"]))
		if name == "" {
			continue
		}
		part := types.FormPart{Name: name, Enabled: YAMLEnabled(valueMap)}
		part.ContentType = scalar.FirstYAMLString(valueMap, "contentType", "content_type")
		partType := strings.ToLower(scalar.YAMLString(valueMap["type"]))
		if partType == "file" {
			part.FilePath = scalar.FirstYAMLString(valueMap, "filePath", "path", "value")
		} else {
			part.Value = scalar.YAMLString(valueMap["value"])
		}
		parts = append(parts, part)
	}
	return parts
}

func ParseYAMLFileBody(raw interface{}) []types.FileBodyEntry {
	if values, ok := scalar.ListValue(raw); ok {
		result := make([]types.FileBodyEntry, 0, len(values))
		for _, entry := range values {
			valueMap, ok := scalar.Map(entry)
			if !ok {
				continue
			}
			filePath := scalar.FirstYAMLString(valueMap, "filePath", "path", "value")
			contentType := scalar.FirstYAMLString(valueMap, "contentType", "content_type")
			if strings.TrimSpace(filePath) == "" && strings.TrimSpace(contentType) == "" {
				continue
			}
			result = append(result, types.FileBodyEntry{
				FilePath:    filePath,
				ContentType: contentType,
				Selected:    scalar.BoolValue(valueMap["selected"], false),
			})
		}
		return result
	}
	if filePath := scalar.YAMLString(raw); strings.TrimSpace(filePath) != "" {
		return []types.FileBodyEntry{{FilePath: filePath, Selected: true}}
	}
	return nil
}

func YAMLEnabled(raw map[string]interface{}) bool {
	if enabled, ok := scalar.BoolValueOK(raw["enabled"]); ok {
		return enabled
	}
	if disabled, ok := scalar.BoolValueOK(raw["disabled"]); ok {
		return !disabled
	}
	return true
}
