package bru

// Global environment files: the Bruno export bundle, importing one back, and merging secrets.
//
// Split out by AST: declarations are identified by the parser and copied
// verbatim from their source offsets.

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mutexdev/lite_api/internal/scalar"
	"github.com/mutexdev/lite_api/internal/types"

	"gopkg.in/yaml.v3"
)

func FormatGlobalEnvironmentExportFiles(files []types.GlobalEnvironmentExportFile) string {
	manifest := map[string]interface{}{
		"folder": "bruno-global-environments",
		"files":  files,
	}
	data, _ := json.MarshalIndent(manifest, "", "  ")
	return string(data)
}

func newBrunoEnvironmentExportInfo() brunoEnvironmentExportInfo {
	return brunoEnvironmentExportInfo{
		Type:          "bruno-environment",
		ExportedAt:    time.Now().UTC().Format(time.RFC3339Nano),
		ExportedUsing: "Bruno/v2.0.0",
	}
}

func brunoEnvironmentExportFromEnvironment(env types.Environment) brunoEnvironmentExport {
	payload := brunoEnvironmentExport{
		Name:      env.Name,
		Variables: make([]brunoEnvironmentVariable, 0, len(env.Variables)),
		Color:     env.Color,
	}
	for _, variable := range env.Variables {
		dataType := scalar.FirstNonEmpty(variable.DataType, variable.Type, "string")
		value := variable.Value
		if variable.Secret {
			value = ""
		}
		exported := brunoEnvironmentVariable{
			Name:    variable.Name,
			Value:   value,
			Type:    "text",
			Enabled: variable.Enabled,
			Secret:  variable.Secret,
		}
		if dataType != "" && dataType != "string" && dataType != "text" {
			exported.DataType = dataType
		}
		payload.Variables = append(payload.Variables, exported)
	}
	return payload
}

func SelectedGlobalEnvironments(workspace types.Workspace, environmentIDs []string) ([]types.Environment, error) {
	if len(environmentIDs) == 0 {
		return append([]types.Environment(nil), workspace.GlobalEnvironments...), nil
	}
	byID := map[string]types.Environment{}
	for _, env := range workspace.GlobalEnvironments {
		byID[env.ID] = env
	}
	environments := make([]types.Environment, 0, len(environmentIDs))
	for _, id := range environmentIDs {
		env, ok := byID[id]
		if !ok {
			return nil, fmt.Errorf("global environment %s not found", id)
		}
		environments = append(environments, env)
	}
	return environments, nil
}

func BrunoEnvironmentExportFileName(name string) string {
	name = strings.Trim(brunoExportFilenamePattern.ReplaceAllString(name, "_"), "_")
	if name == "" {
		return "environment"
	}
	return name
}

func uniqueExportFileBaseName(baseName string, used map[string]bool) string {
	if used == nil {
		used = map[string]bool{}
	}
	if !used[baseName] {
		used[baseName] = true
		return baseName
	}
	for counter := 1; ; counter++ {
		candidate := fmt.Sprintf("%s copy", baseName)
		if counter > 1 {
			candidate = fmt.Sprintf("%s copy %d", baseName, counter)
		}
		if !used[candidate] {
			used[candidate] = true
			return candidate
		}
	}
}

func ParseImportedGlobalEnvironmentsJSON(raw interface{}) ([]types.Environment, error) {
	if root, ok := scalar.Map(raw); ok {
		if _, hasID := root["id"]; hasID {
			if _, hasValues := root["values"]; hasValues {
				return []types.Environment{postmanEnvironmentFromJSONMap(root)}, nil
			}
		}
		if values, ok := scalar.ListValue(root["environments"]); ok {
			return brunoEnvironmentsFromJSONList(values)
		}
		env, err := brunoEnvironmentFromJSONMap(root)
		if err != nil {
			return nil, err
		}
		return []types.Environment{env}, nil
	}
	if values, ok := scalar.ListValue(raw); ok {
		return brunoEnvironmentsFromJSONList(values)
	}
	return nil, errors.New("invalid environment import: expected an object or array")
}

func brunoEnvironmentsFromJSONList(values []interface{}) ([]types.Environment, error) {
	environments := make([]types.Environment, 0, len(values))
	for index, entry := range values {
		valueMap, ok := scalar.Map(entry)
		if !ok {
			return nil, fmt.Errorf("invalid environment at index %d: expected an object", index)
		}
		env, err := brunoEnvironmentFromJSONMap(valueMap)
		if err != nil {
			return nil, fmt.Errorf("invalid environment at index %d: %w", index, err)
		}
		environments = append(environments, env)
	}
	return environments, nil
}

func brunoEnvironmentFromJSONMap(root map[string]interface{}) (types.Environment, error) {
	variableValues, ok := scalar.ListValue(root["variables"])
	if !ok {
		return types.Environment{}, errors.New("missing or invalid variables array")
	}
	name := strings.TrimSpace(scalar.YAMLString(root["name"]))
	if name == "" {
		name = "Imported Environment"
	}
	env := types.Environment{
		ID:        scalar.NewID("env"),
		Name:      name,
		Color:     scalar.YAMLString(root["color"]),
		Variables: make([]types.Variable, 0, len(variableValues)),
	}
	for index, entry := range variableValues {
		valueMap, ok := scalar.Map(entry)
		if !ok {
			return types.Environment{}, fmt.Errorf("invalid variable at index %d: expected an object", index)
		}
		name := strings.TrimSpace(scalar.YAMLString(valueMap["name"]))
		if name == "" {
			return types.Environment{}, fmt.Errorf("invalid variable at index %d: missing or invalid name", index)
		}
		env.Variables = append(env.Variables, brunoVariableFromJSONMap(valueMap, true))
	}
	return env, nil
}

func postmanEnvironmentFromJSONMap(root map[string]interface{}) types.Environment {
	name := strings.TrimSpace(scalar.YAMLString(root["name"]))
	if name == "" {
		name = "Imported Environment"
	}
	values, _ := scalar.ListValue(root["values"])
	env := types.Environment{ID: scalar.NewID("env"), Name: name, Variables: make([]types.Variable, 0, len(values))}
	for _, entry := range values {
		valueMap, ok := scalar.Map(entry)
		if !ok {
			continue
		}
		if valueMap["key"] == nil && valueMap["value"] == nil {
			continue
		}
		name := invalidPostmanVariableCharacterPattern.ReplaceAllString(scalar.YAMLString(valueMap["key"]), "_")
		if name == "" {
			continue
		}
		secret := strings.EqualFold(strings.TrimSpace(scalar.YAMLString(valueMap["type"])), "secret")
		env.Variables = append(env.Variables, types.Variable{
			ID:       scalar.NewID("var"),
			Name:     name,
			Value:    importedEnvironmentVariableValue(valueMap["value"], "string"),
			Type:     "string",
			DataType: "string",
			Enabled:  scalar.BoolValue(valueMap["enabled"], true),
			Secret:   secret,
		})
	}
	return env
}

func importedEnvironmentVariableValue(raw interface{}, dataType string) interface{} {
	if raw == nil {
		return ""
	}
	switch value := raw.(type) {
	case json.Number:
		if strings.EqualFold(dataType, "number") {
			if strings.ContainsAny(value.String(), ".eE") {
				if parsed, err := value.Float64(); err == nil {
					return parsed
				}
			}
			if parsed, err := value.Int64(); err == nil {
				return parsed
			}
		}
		return value.String()
	case string:
		switch dataType {
		case "number":
			if strings.ContainsAny(value, ".eE") {
				if parsed, err := strconv.ParseFloat(value, 64); err == nil {
					return parsed
				}
			}
			if parsed, err := strconv.Atoi(value); err == nil {
				return parsed
			}
		case "boolean":
			if parsed, err := strconv.ParseBool(value); err == nil {
				return parsed
			}
		}
		return value
	default:
		return value
	}
}

func StringifyYAMLEnvironment(env types.Environment) string {
	env = ScrubEnvironmentSecretValues([]types.Environment{env})[0]
	root := map[string]interface{}{
		"name":      env.Name,
		"variables": YAMLVariables(env.Variables),
	}
	if strings.TrimSpace(env.Color) != "" {
		root["color"] = env.Color
	}
	data, _ := yaml.Marshal(root)
	return string(data)
}

func MergeSecretVariables(existing, secrets []types.Variable) []types.Variable {
	byName := map[string]int{}
	for i, variable := range existing {
		byName[variable.Name] = i
	}
	for _, secret := range secrets {
		if index, ok := byName[secret.Name]; ok {
			existing[index].Secret = true
			if secret.DataType != "" && secret.DataType != "string" {
				existing[index].DataType = secret.DataType
				existing[index].Type = secret.Type
			}
			continue
		}
		existing = append(existing, secret)
	}
	return existing
}

func MergeEnvironments(existing, loaded []types.Environment) []types.Environment {
	byName := map[string]int{}
	for i, env := range existing {
		byName[strings.ToLower(env.Name)] = i
	}
	for _, env := range loaded {
		key := strings.ToLower(env.Name)
		if index, ok := byName[key]; ok {
			if existing[index].ID != "" {
				env.ID = existing[index].ID
			}
			existing[index] = env
			continue
		}
		existing = append(existing, env)
	}
	sort.SliceStable(existing, func(i, j int) bool {
		return strings.ToLower(existing[i].Name) < strings.ToLower(existing[j].Name)
	})
	return existing
}

func StringifyBruEnvironment(env types.Environment) string {
	var b strings.Builder
	if strings.TrimSpace(env.Color) != "" {
		fmt.Fprintf(&b, "color: %s\n\n", env.Color)
	}
	plain := []types.Variable{}
	secrets := []types.Variable{}
	for _, variable := range env.Variables {
		if variable.Secret {
			secrets = append(secrets, variable)
		} else {
			plain = append(plain, variable)
		}
	}
	if len(plain) > 0 {
		writeBruVariables(&b, "vars", plain, false)
	}
	if len(secrets) > 0 {
		writeBruVariables(&b, "vars:secret", secrets, true)
	}
	return b.String()
}

func BrunoWorkspaceEnvironmentUIDForPath(path string) string {
	var hash int32
	for _, char := range filepath.Clean(path) {
		hash = (hash << 5) - hash + int32(char)
	}
	uid := strconv.FormatUint(uint64(uint32(hash)), 36)
	if len(uid) >= 21 {
		return uid
	}
	return uid + strings.Repeat("0", 21-len(uid))
}

type brunoEnvironmentExportInfo struct {
	Type          string `json:"type"`
	ExportedAt    string `json:"exportedAt"`
	ExportedUsing string `json:"exportedUsing"`
}

type brunoEnvironmentExport struct {
	Name      string                      `json:"name"`
	Variables []brunoEnvironmentVariable  `json:"variables"`
	Color     string                      `json:"color,omitempty"`
	Info      *brunoEnvironmentExportInfo `json:"info,omitempty"`
}

type brunoEnvironmentExportBundle struct {
	Info         brunoEnvironmentExportInfo `json:"info"`
	Environments []brunoEnvironmentExport   `json:"environments"`
}

type brunoEnvironmentVariable struct {
	Name     string      `json:"name"`
	Value    interface{} `json:"value"`
	Type     string      `json:"type"`
	Enabled  bool        `json:"enabled"`
	Secret   bool        `json:"secret"`
	DataType string      `json:"dataType,omitempty"`
}

var brunoExportFilenamePattern = regexp.MustCompile(`[^A-Za-z0-9-_]`)

func ScrubEnvironmentSecretValues(environments []types.Environment) []types.Environment {
	environments = append([]types.Environment(nil), environments...)
	for envIndex := range environments {
		if len(environments[envIndex].Variables) == 0 {
			continue
		}
		environments[envIndex].Variables = append([]types.Variable(nil), environments[envIndex].Variables...)
		for variableIndex := range environments[envIndex].Variables {
			if environments[envIndex].Variables[variableIndex].Secret {
				environments[envIndex].Variables[variableIndex].Value = ""
			}
		}
	}
	return environments
}

func StringifyBrunoEnvironmentExport(env types.Environment) (string, error) {
	info := newBrunoEnvironmentExportInfo()
	payload := brunoEnvironmentExportFromEnvironment(env)
	payload.Info = &info
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func StringifyBrunoEnvironmentExportBundle(environments []types.Environment) (string, error) {
	payload := brunoEnvironmentExportBundle{
		Info:         newBrunoEnvironmentExportInfo(),
		Environments: make([]brunoEnvironmentExport, 0, len(environments)),
	}
	for _, env := range environments {
		payload.Environments = append(payload.Environments, brunoEnvironmentExportFromEnvironment(env))
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}
