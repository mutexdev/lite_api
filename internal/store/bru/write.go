// Writing .bru documents, and importing/exporting environments.
//
// US-070. The write side lives with the read side deliberately: a format has
// exactly one definition of what a block looks like, and splitting the reader
// from the writer is how the two drift until a file this app wrote can no
// longer be read back by it.
package bru

import (
	"LiteAPI/internal/scalar"
	"LiteAPI/internal/types"
	"LiteAPI/internal/wsexec"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

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
		name = "Imported types.Environment"
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
		name = "Imported types.Environment"
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

func brunoVariableFromJSONMap(root map[string]interface{}, blankSecret bool) types.Variable {
	secret := scalar.BoolValue(root["secret"], false)
	dataType := strings.ToLower(strings.TrimSpace(scalar.YAMLString(root["dataType"])))
	if dataType == "" || dataType == "text" {
		dataType = "string"
	}
	value := importedEnvironmentVariableValue(root["value"], dataType)
	if secret && blankSecret {
		value = ""
	}
	return types.Variable{
		ID:       scalar.NewID("var"),
		Name:     strings.TrimSpace(scalar.YAMLString(root["name"])),
		Value:    value,
		Type:     dataType,
		DataType: dataType,
		Enabled:  scalar.BoolValue(root["enabled"], true),
		Secret:   secret,
	}
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

func StringifyBruCollection(collection types.Collection) string {
	var b strings.Builder
	if len(collection.Headers) > 0 {
		writeBruKeyValues(&b, "headers", collection.Headers)
	}
	writeBruAuth(&b, collection.Auth, false)
	if len(collection.Variables) > 0 {
		writeBruVariables(&b, "vars:pre-request", collection.Variables, false)
	}
	if len(collection.ResVariables) > 0 {
		writeBruVariables(&b, "vars:post-response", collection.ResVariables, false)
	}
	writeBruBlock(&b, "script:pre-request", collection.PreScript)
	writeBruBlock(&b, "script:post-response", collection.PostScript)
	writeBruBlock(&b, "tests", collection.Tests)
	writeBruBlock(&b, "docs", collection.Docs)
	return b.String()
}

func StringifyBruFolder(folder types.FolderConfig) string {
	var b strings.Builder
	b.WriteString("meta {\n")
	fmt.Fprintf(&b, "  name: %s\n", scalar.FirstNonEmpty(folder.Name, filepath.Base(filepath.FromSlash(folder.Path))))
	if folder.Seq > 0 {
		fmt.Fprintf(&b, "  seq: %d\n", folder.Seq)
	}
	b.WriteString("}\n")
	if len(folder.Headers) > 0 {
		writeBruKeyValues(&b, "headers", folder.Headers)
	}
	if folder.Auth.Mode != "" {
		writeBruAuth(&b, folder.Auth, true)
	}
	if len(folder.Variables) > 0 {
		writeBruVariables(&b, "vars:pre-request", folder.Variables, false)
	}
	if len(folder.ResVariables) > 0 {
		writeBruVariables(&b, "vars:post-response", folder.ResVariables, false)
	}
	writeBruBlock(&b, "script:pre-request", folder.PreScript)
	writeBruBlock(&b, "script:post-response", folder.PostScript)
	writeBruBlock(&b, "tests", folder.Tests)
	writeBruBlock(&b, "docs", folder.Docs)
	return b.String()
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

func writeBruKeyValues(b *strings.Builder, section string, values []types.KeyValue) {
	if len(values) == 0 {
		return
	}
	if b.Len() > 0 {
		b.WriteString("\n")
	}
	fmt.Fprintf(b, "%s {\n", section)
	for _, value := range values {
		prefix := ""
		if !value.Enabled {
			prefix = "~"
		}
		fmt.Fprintf(b, "  %s%s: %s\n", prefix, value.Name, value.Value)
	}
	b.WriteString("}\n")
}

func writeBruMultipartValues(b *strings.Builder, section string, values []types.FormPart) {
	if len(values) == 0 {
		return
	}
	if b.Len() > 0 {
		b.WriteString("\n")
	}
	fmt.Fprintf(b, "%s {\n", section)
	for _, value := range values {
		prefix := ""
		if !value.Enabled {
			prefix = "~"
		}
		rendered := value.Value
		if strings.TrimSpace(value.FilePath) != "" {
			rendered = "@file(" + value.FilePath + ")"
		}
		if strings.TrimSpace(value.ContentType) != "" {
			rendered = strings.TrimSpace(rendered + " @contentType(" + value.ContentType + ")")
		}
		fmt.Fprintf(b, "  %s%s: %s\n", prefix, value.Name, rendered)
	}
	b.WriteString("}\n")
}

func writeBruFileBody(b *strings.Builder, body types.RequestBody) {
	entries := types.FileBodyEntriesOf(body)
	if len(entries) == 0 {
		return
	}
	if b.Len() > 0 {
		b.WriteString("\n")
	}
	b.WriteString("body:file {\n")
	write := func(selected bool) {
		for _, file := range entries {
			if file.Selected != selected {
				continue
			}
			if strings.TrimSpace(file.FilePath) == "" && strings.TrimSpace(file.ContentType) == "" {
				continue
			}
			prefix := ""
			if !file.Selected {
				prefix = "~"
			}
			rendered := "@file(" + file.FilePath + ")"
			if strings.TrimSpace(file.ContentType) != "" {
				rendered += " @contentType(" + file.ContentType + ")"
			}
			fmt.Fprintf(b, "  %sfile: %s\n", prefix, rendered)
		}
	}
	write(true)
	write(false)
	b.WriteString("}\n")
}

func writeBruVariables(b *strings.Builder, section string, values []types.Variable, secret bool) {
	if len(values) == 0 {
		return
	}
	if b.Len() > 0 {
		b.WriteString("\n")
	}
	closeToken := "}"
	if secret {
		closeToken = "]"
		fmt.Fprintf(b, "%s [\n", section)
	} else {
		fmt.Fprintf(b, "%s {\n", section)
	}
	for _, variable := range values {
		dataType := scalar.FirstNonEmpty(variable.DataType, variable.Type, "string")
		if dataType != "string" {
			fmt.Fprintf(b, "  @%s\n", dataType)
		}
		prefix := ""
		if !variable.Enabled {
			prefix = "~"
		}
		if secret {
			fmt.Fprintf(b, "  %s%s\n", prefix, variable.Name)
			continue
		}
		fmt.Fprintf(b, "  %s%s: %s\n", prefix, variable.Name, bruScalarString(variable.Value))
	}
	fmt.Fprintf(b, "%s\n", closeToken)
}

func writeBruAuth(b *strings.Builder, auth types.AuthConfig, includeNone bool) {
	mode := auth.Mode
	if mode == "" {
		mode = "none"
	}
	if mode == "none" && !includeNone {
		return
	}
	if b.Len() > 0 {
		b.WriteString("\n")
	}
	b.WriteString("auth {\n")
	fmt.Fprintf(b, "  mode: %s\n", mode)
	b.WriteString("}\n")
	switch mode {
	case "basic":
		b.WriteString("\nauth:basic {\n")
		fmt.Fprintf(b, "  username: %s\n", auth.Username)
		fmt.Fprintf(b, "  password: %s\n", auth.Password)
		b.WriteString("}\n")
	case "digest":
		b.WriteString("\nauth:digest {\n")
		fmt.Fprintf(b, "  username: %s\n", auth.Username)
		fmt.Fprintf(b, "  password: %s\n", auth.Password)
		b.WriteString("}\n")
	case "wsse":
		b.WriteString("\nauth:wsse {\n")
		fmt.Fprintf(b, "  username: %s\n", auth.Username)
		fmt.Fprintf(b, "  password: %s\n", auth.Password)
		b.WriteString("}\n")
	case "ntlm":
		b.WriteString("\nauth:ntlm {\n")
		fmt.Fprintf(b, "  username: %s\n", auth.Username)
		fmt.Fprintf(b, "  password: %s\n", auth.Password)
		fmt.Fprintf(b, "  domain: %s\n", auth.Domain)
		b.WriteString("}\n")
	case "bearer":
		b.WriteString("\nauth:bearer {\n")
		fmt.Fprintf(b, "  token: %s\n", auth.Token)
		b.WriteString("}\n")
	case "apikey":
		b.WriteString("\nauth:apikey {\n")
		fmt.Fprintf(b, "  key: %s\n", auth.APIKey)
		fmt.Fprintf(b, "  value: %s\n", auth.APIValue)
		fmt.Fprintf(b, "  placement: %s\n", scalar.FirstNonEmpty(auth.APILocation, "header"))
		b.WriteString("}\n")
	case "awsv4":
		b.WriteString("\nauth:awsv4 {\n")
		fmt.Fprintf(b, "  accessKeyId: %s\n", scalar.FirstNonEmpty(auth.AWSV4.AccessKeyID, auth.AWSV4.AccessKey))
		fmt.Fprintf(b, "  secretAccessKey: %s\n", scalar.FirstNonEmpty(auth.AWSV4.SecretAccessKey, auth.AWSV4.SecretKey))
		fmt.Fprintf(b, "  sessionToken: %s\n", auth.AWSV4.SessionToken)
		fmt.Fprintf(b, "  service: %s\n", auth.AWSV4.Service)
		fmt.Fprintf(b, "  region: %s\n", auth.AWSV4.Region)
		fmt.Fprintf(b, "  profileName: %s\n", auth.AWSV4.ProfileName)
		b.WriteString("}\n")
	case "oauth1":
		b.WriteString("\nauth:oauth1 {\n")
		fmt.Fprintf(b, "  consumer_key: %s\n", auth.OAuth1.ConsumerKey)
		fmt.Fprintf(b, "  consumer_secret: %s\n", auth.OAuth1.ConsumerSecret)
		fmt.Fprintf(b, "  access_token: %s\n", auth.OAuth1.AccessToken)
		fmt.Fprintf(b, "  token_secret: %s\n", auth.OAuth1.AccessTokenSecret)
		fmt.Fprintf(b, "  callback_url: %s\n", auth.OAuth1.CallbackURL)
		fmt.Fprintf(b, "  verifier: %s\n", auth.OAuth1.Verifier)
		fmt.Fprintf(b, "  signature_method: %s\n", scalar.FirstNonEmpty(auth.OAuth1.SignatureMethod, "HMAC-SHA1"))
		fmt.Fprintf(b, "  private_key: %s\n", OAuth1PrivateKeyValue(auth.OAuth1))
		fmt.Fprintf(b, "  timestamp: %s\n", auth.OAuth1.Timestamp)
		fmt.Fprintf(b, "  nonce: %s\n", auth.OAuth1.Nonce)
		fmt.Fprintf(b, "  version: %s\n", scalar.FirstNonEmpty(auth.OAuth1.Version, "1.0"))
		fmt.Fprintf(b, "  realm: %s\n", auth.OAuth1.Realm)
		fmt.Fprintf(b, "  placement: %s\n", scalar.FirstNonEmpty(auth.OAuth1.Placement, "header"))
		fmt.Fprintf(b, "  include_body_hash: %s\n", strconv.FormatBool(auth.OAuth1.IncludeBodyHash))
		b.WriteString("}\n")
	case "oauth2":
		b.WriteString("\nauth:oauth2 {\n")
		fmt.Fprintf(b, "  grant_type: %s\n", auth.OAuth2.GrantType)
		fmt.Fprintf(b, "  callback_url: %s\n", auth.OAuth2.CallbackURL)
		fmt.Fprintf(b, "  authorization_url: %s\n", auth.OAuth2.AuthorizationURL)
		fmt.Fprintf(b, "  access_token_url: %s\n", auth.OAuth2.AccessTokenURL)
		fmt.Fprintf(b, "  refresh_token_url: %s\n", auth.OAuth2.RefreshTokenURL)
		fmt.Fprintf(b, "  username: %s\n", auth.OAuth2.Username)
		fmt.Fprintf(b, "  password: %s\n", auth.OAuth2.Password)
		fmt.Fprintf(b, "  client_id: %s\n", auth.OAuth2.ClientID)
		fmt.Fprintf(b, "  client_secret: %s\n", auth.OAuth2.ClientSecret)
		fmt.Fprintf(b, "  scope: %s\n", auth.OAuth2.Scope)
		fmt.Fprintf(b, "  state: %s\n", auth.OAuth2.State)
		fmt.Fprintf(b, "  pkce: %s\n", strconv.FormatBool(auth.OAuth2.PKCE))
		fmt.Fprintf(b, "  credentials_placement: %s\n", scalar.FirstNonEmpty(auth.OAuth2.CredentialsPlacement, "basic_auth_header"))
		fmt.Fprintf(b, "  credentials_id: %s\n", scalar.FirstNonEmpty(auth.OAuth2.CredentialsID, "credentials"))
		fmt.Fprintf(b, "  token_source: %s\n", scalar.FirstNonEmpty(auth.OAuth2.TokenSource, "access_token"))
		fmt.Fprintf(b, "  token_placement: %s\n", scalar.FirstNonEmpty(auth.OAuth2.TokenPlacement, "header"))
		fmt.Fprintf(b, "  token_header_prefix: %s\n", scalar.FirstNonEmpty(auth.OAuth2.TokenHeaderPrefix, "Bearer"))
		fmt.Fprintf(b, "  token_query_key: %s\n", scalar.FirstNonEmpty(auth.OAuth2.TokenQueryKey, "access_token"))
		fmt.Fprintf(b, "  auto_fetch_token: %s\n", strconv.FormatBool(true))
		fmt.Fprintf(b, "  auto_refresh_token: %s\n", strconv.FormatBool(auth.OAuth2.AutoRefreshToken))
		if auth.Token != "" {
			fmt.Fprintf(b, "  access_token: %s\n", auth.Token)
		}
		b.WriteString("}\n")
		writeBruOAuth2AdditionalParams(b, "auth:oauth2:additional_params:auth_req:headers", auth.OAuth2.AuthorizationAdditionalParams, "headers")
		writeBruOAuth2AdditionalParams(b, "auth:oauth2:additional_params:auth_req:queryparams", auth.OAuth2.AuthorizationAdditionalParams, "queryparams")
		writeBruOAuth2AdditionalParams(b, "auth:oauth2:additional_params:access_token_req:headers", auth.OAuth2.TokenAdditionalParams, "headers")
		writeBruOAuth2AdditionalParams(b, "auth:oauth2:additional_params:access_token_req:queryparams", auth.OAuth2.TokenAdditionalParams, "queryparams")
		writeBruOAuth2AdditionalParams(b, "auth:oauth2:additional_params:access_token_req:body", auth.OAuth2.TokenAdditionalParams, "body")
		writeBruOAuth2AdditionalParams(b, "auth:oauth2:additional_params:refresh_token_req:headers", auth.OAuth2.RefreshAdditionalParams, "headers")
		writeBruOAuth2AdditionalParams(b, "auth:oauth2:additional_params:refresh_token_req:queryparams", auth.OAuth2.RefreshAdditionalParams, "queryparams")
		writeBruOAuth2AdditionalParams(b, "auth:oauth2:additional_params:refresh_token_req:body", auth.OAuth2.RefreshAdditionalParams, "body")
	}
}

func writeBruOAuth2AdditionalParams(b *strings.Builder, section string, params []types.OAuth2AdditionalParam, sendIn string) {
	writeBruKeyValues(b, section, OAuth2KeyValuesFromAdditionalParams(params, sendIn))
}

func writeBruBlock(b *strings.Builder, section, content string) {
	content = strings.TrimRight(content, "\n")
	if strings.TrimSpace(content) == "" {
		return
	}
	if b.Len() > 0 {
		b.WriteString("\n")
	}
	fmt.Fprintf(b, "%s {\n", section)
	for _, line := range strings.Split(content, "\n") {
		fmt.Fprintf(b, "  %s\n", line)
	}
	b.WriteString("}\n")
}

func writeBruGrpcMessages(b *strings.Builder, messages []types.GrpcMessage) {
	for _, message := range messages {
		if strings.TrimSpace(message.Name) == "" && strings.TrimSpace(message.Content) == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString("body:grpc {\n")
		if strings.TrimSpace(message.Name) != "" {
			fmt.Fprintf(b, "  name: %s\n", message.Name)
		}
		fmt.Fprintf(b, "  content: %s\n", bruScalarString(message.Content))
		b.WriteString("}\n")
	}
}

func writeBruWSMessages(b *strings.Builder, messages []types.WSMessage) {
	for _, message := range messages {
		if strings.TrimSpace(message.Name) == "" && strings.TrimSpace(message.Content) == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString("body:ws {\n")
		if strings.TrimSpace(message.Name) != "" {
			fmt.Fprintf(b, "  name: %s\n", message.Name)
		}
		fmt.Fprintf(b, "  type: %s\n", wsexec.NormalizeMessageType(message.Type))
		if message.Selected {
			b.WriteString("  selected: true\n")
		}
		fmt.Fprintf(b, "  content: %s\n", bruScalarString(message.Content))
		b.WriteString("}\n")
	}
}

func bruScalarString(value interface{}) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		if strings.Contains(v, "\n") {
			return "'''\n    " + strings.ReplaceAll(v, "\n", "\n    ") + "\n  '''"
		}
		return v
	case bool:
		return strconv.FormatBool(v)
	case int:
		return strconv.Itoa(v)
	case int64:
		return strconv.FormatInt(v, 10)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	default:
		return fmt.Sprint(v)
	}
}

func ParseBruTopLevelScalar(content, key string) string {
	prefix := key + ":"
	for _, line := range strings.Split(content, "\n") {
		if strings.TrimLeft(line, " \t") != line {
			continue
		}
		trimmed := strings.TrimSpace(line)
		if strings.HasSuffix(trimmed, "{") || strings.HasSuffix(trimmed, "[") {
			return ""
		}
		if value, ok := strings.CutPrefix(trimmed, prefix); ok {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func StringifyBru(item types.RequestItem) string {
	var b strings.Builder
	fmt.Fprintf(&b, "meta {\n  name: %s\n  type: %s\n  seq: %d\n}\n\n", item.Name, item.Type, item.Seq)
	if item.Type == "grpc" {
		b.WriteString("grpc {\n")
		fmt.Fprintf(&b, "  url: %s\n", item.URL)
		if strings.TrimSpace(item.Method) != "" {
			fmt.Fprintf(&b, "  method: %s\n", item.Method)
		}
		fmt.Fprintf(&b, "  body: %s\n", scalar.FirstNonEmpty(item.Body.Mode, "grpc"))
		fmt.Fprintf(&b, "  auth: %s\n", scalar.FirstNonEmpty(item.Auth.Mode, "none"))
		if strings.TrimSpace(item.GrpcMethodType) != "" {
			fmt.Fprintf(&b, "  methodType: %s\n", item.GrpcMethodType)
		}
		if strings.TrimSpace(item.ProtoPath) != "" {
			fmt.Fprintf(&b, "  protoPath: %s\n", item.ProtoPath)
		}
		b.WriteString("}\n")
		writeBruAuth(&b, item.Auth, false)
		writeBruKeyValues(&b, "metadata", item.Headers)
		writeBruGrpcMessages(&b, item.GrpcMessages)
		writeBruRequestTail(&b, item)
		return b.String()
	}
	if item.Type == "websocket" {
		b.WriteString("ws {\n")
		fmt.Fprintf(&b, "  url: %s\n", item.URL)
		fmt.Fprintf(&b, "  body: %s\n", scalar.FirstNonEmpty(item.Body.Mode, "ws"))
		fmt.Fprintf(&b, "  auth: %s\n", scalar.FirstNonEmpty(item.Auth.Mode, "none"))
		b.WriteString("}\n")
		writeBruAuth(&b, item.Auth, false)
		writeBruKeyValues(&b, "headers", item.Headers)
		writeBruKeyValues(&b, "params:query", item.Params)
		writeBruKeyValues(&b, "params:path", item.PathParams)
		writeBruWSMessages(&b, WsMessagesForStorage(item))
		writeBruRequestTail(&b, item)
		return b.String()
	}
	method := strings.ToLower(item.Method)
	if method == "" {
		method = "get"
	}
	fmt.Fprintf(&b, "%s {\n  url: %s\n  body: %s\n  auth: %s\n}\n", method, item.URL, item.Body.Mode, item.Auth.Mode)
	writeBruAuth(&b, item.Auth, false)
	if len(item.Headers) > 0 {
		b.WriteString("\nheaders {\n")
		for _, header := range item.Headers {
			if header.Enabled {
				fmt.Fprintf(&b, "  %s: %s\n", header.Name, header.Value)
			}
		}
		b.WriteString("}\n")
	}
	if len(item.Params) > 0 {
		b.WriteString("\nparams:query {\n")
		for _, param := range item.Params {
			if param.Enabled {
				fmt.Fprintf(&b, "  %s: %s\n", param.Name, param.Value)
			}
		}
		b.WriteString("}\n")
	}
	if len(item.PathParams) > 0 {
		b.WriteString("\nparams:path {\n")
		for _, param := range item.PathParams {
			if param.Enabled {
				fmt.Fprintf(&b, "  %s: %s\n", param.Name, param.Value)
			}
		}
		b.WriteString("}\n")
	}
	if item.Body.Mode == "json" && strings.TrimSpace(item.Body.JSON) != "" {
		b.WriteString("\nbody:json {\n")
		for _, line := range strings.Split(strings.TrimRight(item.Body.JSON, "\n"), "\n") {
			b.WriteString("  " + line + "\n")
		}
		b.WriteString("}\n")
	}
	if item.Body.Mode == "formUrlEncoded" {
		writeBruKeyValues(&b, "body:form-urlencoded", item.Body.FormURLEncoded)
	}
	if item.Body.Mode == "multipartForm" {
		writeBruMultipartValues(&b, "body:multipart-form", item.Body.Multipart)
	}
	if item.Body.Mode == "file" {
		writeBruFileBody(&b, item.Body)
	}
	writeBruRequestTail(&b, item)
	return b.String()
}

func writeBruRequestTail(b *strings.Builder, item types.RequestItem) {
	writeBruVariables(b, "vars:pre-request", item.Vars.Req, false)
	writeBruVariables(b, "vars:post-response", item.Vars.Res, false)
	writeBruBlock(b, "script:pre-request", item.PreScript)
	writeBruBlock(b, "script:post-response", item.PostScript)
	writeBruBlock(b, "tests", item.Tests)
	writeBruBlock(b, "docs", item.Docs)
	writeBruSettings(b, item.Settings)
	for _, example := range item.Examples {
		writeBruExample(b, example)
	}
}

func writeBruSettings(b *strings.Builder, settings types.RequestSettings) {
	b.WriteString("\nsettings {\n")
	fmt.Fprintf(b, "  encodeUrl: %t\n", settings.EncodeURL)
	fmt.Fprintf(b, "  timeout: %d\n", settings.TimeoutMs)
	fmt.Fprintf(b, "  followRedirects: %t\n", settings.FollowRedirects)
	fmt.Fprintf(b, "  maxRedirects: %d\n", settings.MaxRedirects)
	fmt.Fprintf(b, "  storeCookies: %t\n", settings.StoreCookies)
	fmt.Fprintf(b, "  verifyTls: %t\n", settings.VerifyTLS)
	fmt.Fprintf(b, "  keepAliveInterval: %d\n", settings.KeepAliveInterval)
	b.WriteString("}\n")
}

func writeBruExample(b *strings.Builder, example types.ResponseExample) {
	if b.Len() > 0 {
		b.WriteString("\n")
	}
	name := strings.TrimSpace(example.Name)
	if name == "" {
		name = "Example"
	}
	b.WriteString("example {\n")
	fmt.Fprintf(b, "  name: %s\n", name)
	if strings.TrimSpace(example.Description) != "" {
		fmt.Fprintf(b, "  description: %s\n", example.Description)
	}
	b.WriteString("\n")
	b.WriteString("  request: {\n")
	if strings.TrimSpace(example.Request.URL) != "" {
		fmt.Fprintf(b, "    url: %s\n", example.Request.URL)
	}
	if strings.TrimSpace(example.Request.Method) != "" {
		fmt.Fprintf(b, "    method: %s\n", strings.ToLower(example.Request.Method))
	}
	requestBodyMode := NormalizeBodyMode(scalar.FirstNonEmpty(example.Request.BodyMode, "none"))
	fmt.Fprintf(b, "    mode: %s\n", requestBodyMode)
	writeIndentedKeyValues(b, "headers", example.Request.Headers, "    ")
	writeIndentedKeyValues(b, "params", example.Request.Params, "    ")
	if requestBodyMode == "formUrlEncoded" && len(example.Request.FormURLEncoded) > 0 {
		writeIndentedKeyValues(b, "body:form-urlencoded", example.Request.FormURLEncoded, "    ")
	} else if requestBodyMode == "multipartForm" && len(example.Request.MultipartForm) > 0 {
		writeIndentedMultipartValues(b, "body:multipart-form", example.Request.MultipartForm, "    ")
	} else if requestBodyMode == "file" && len(example.Request.File) > 0 {
		writeIndentedFileBody(b, "body:file", example.Request.File, "    ")
	} else {
		writeExampleContentBlock(b, "body", scalar.FirstNonEmpty(requestBodyMode, "text"), example.Request.Body, "    ")
	}
	b.WriteString("  }\n\n")
	b.WriteString("  response: {\n")
	b.WriteString("    status: {\n")
	fmt.Fprintf(b, "      code: %d\n", example.Response.Status)
	if strings.TrimSpace(example.Response.StatusText) != "" {
		fmt.Fprintf(b, "      text: %s\n", example.Response.StatusText)
	}
	b.WriteString("    }\n")
	writeIndentedKeyValues(b, "headers", example.Response.Headers, "    ")
	if example.Response.DurationMs > 0 {
		fmt.Fprintf(b, "    duration: %d\n", example.Response.DurationMs)
	}
	writeExampleContentBlock(b, "body", scalar.FirstNonEmpty(example.Response.BodyType, "text"), example.Response.Body, "    ")
	b.WriteString("  }\n")
	b.WriteString("}\n")
}

func writeIndentedKeyValues(b *strings.Builder, section string, values []types.KeyValue, indent string) {
	if len(values) == 0 {
		return
	}
	fmt.Fprintf(b, "%s%s: {\n", indent, section)
	for _, value := range values {
		if value.Name == "" {
			continue
		}
		prefix := ""
		if !value.Enabled {
			prefix = "~"
		}
		fmt.Fprintf(b, "%s  %s%s: %s\n", indent, prefix, value.Name, value.Value)
	}
	fmt.Fprintf(b, "%s}\n", indent)
}

func writeIndentedMultipartValues(b *strings.Builder, section string, values []types.FormPart, indent string) {
	if len(values) == 0 {
		return
	}
	fmt.Fprintf(b, "%s%s: {\n", indent, section)
	for _, value := range values {
		if strings.TrimSpace(value.Name) == "" {
			continue
		}
		prefix := ""
		if !value.Enabled {
			prefix = "~"
		}
		rendered := value.Value
		if strings.TrimSpace(value.FilePath) != "" {
			rendered = "@file(" + value.FilePath + ")"
		}
		if strings.TrimSpace(value.ContentType) != "" {
			rendered = strings.TrimSpace(rendered + " @contentType(" + value.ContentType + ")")
		}
		fmt.Fprintf(b, "%s  %s%s: %s\n", indent, prefix, value.Name, rendered)
	}
	fmt.Fprintf(b, "%s}\n", indent)
}

func writeIndentedFileBody(b *strings.Builder, section string, values []types.FileBodyEntry, indent string) {
	if len(values) == 0 {
		return
	}
	fmt.Fprintf(b, "%s%s: {\n", indent, section)
	write := func(selected bool) {
		for _, file := range values {
			if file.Selected != selected {
				continue
			}
			if strings.TrimSpace(file.FilePath) == "" && strings.TrimSpace(file.ContentType) == "" {
				continue
			}
			prefix := ""
			if !file.Selected {
				prefix = "~"
			}
			rendered := "@file(" + file.FilePath + ")"
			if strings.TrimSpace(file.ContentType) != "" {
				rendered += " @contentType(" + file.ContentType + ")"
			}
			fmt.Fprintf(b, "%s  %sfile: %s\n", indent, prefix, rendered)
		}
	}
	write(true)
	write(false)
	fmt.Fprintf(b, "%s}\n", indent)
}

func writeExampleContentBlock(b *strings.Builder, section, bodyType, body, indent string) {
	if strings.TrimSpace(body) == "" {
		return
	}
	fmt.Fprintf(b, "%s%s: {\n", indent, section)
	fmt.Fprintf(b, "%s  type: %s\n", indent, bodyType)
	fmt.Fprintf(b, "%s  content: '''\n", indent)
	for _, line := range strings.Split(strings.TrimRight(body, "\n"), "\n") {
		fmt.Fprintf(b, "%s    %s\n", indent, line)
	}
	fmt.Fprintf(b, "%s  '''\n", indent)
	fmt.Fprintf(b, "%s}\n", indent)
}

func DefaultFeatures() []types.Feature {
	features := []types.Feature{
		feature("workspace", "Workspaces", "Workspace and app state", "done", "Create, persist, restore, switch workspaces with open tab snapshots and per-tab request/response pane selections, and remove mounted collections from a workspace without deleting files while clearing their open/closed tabs.", []string{"go test ./...", "TestUpdateOpenTabPanesPersistsPaneSelection", "TestRemoveCollectionUnmountsAndPreservesFilesAndTabs", "TestRemoveCollectionRejectsScratchCollection", "UI smoke: workspace visible", "Computer UI smoke: tab pane persistence", "Browser UI smoke: remove collection without deleting files"}),
		feature("collections", "Collections", "Filesystem collections", "partial", "Create collections, create requests, clone collections by creating a new folder with renamed root metadata and format-matching file copies, rename collections by updating root metadata without moving folders, open existing .bru/bruno.json and legacy .yml OpenCollection folders, hydrate collection.bru/opencollection metadata and environments, render folder paths, inherit folder headers/auth/variables/post-response variables, parse and save inline response examples, create blank/manual response examples with Bruno-style suggested names/current request snapshots/200 OK text defaults, show saved response examples as nested sidebar rows/search matches, rename/clone/delete saved response examples with Bruno-style clone names, edit saved example description, request method/URL/query-param sync/params/headers/body with JSON prettify, form-url-encoded rows, multipart rows, and file rows, reorder and bulk-edit response-example request/response table rows with edit-mode drag handles, response status/body with JSON prettify/checkbox-free response headers and Content-Type-to-body-type sync, generate cURL/fetch code snippets from saved response-example request snapshots, open saved response examples as tabs, open newly saved/created/cloned response examples as tabs, restore response-example tabs into the Examples pane, close deleted response-example tabs, refresh from disk, foreground-watch opened collection folders for external Bruno/OpenCollection tree-file changes with Bruno ignore path-prefix suppression, dirty-draft protection, and deleted-request tab cleanup, save Bruno files back to original request paths, and edit collection headers/auth/client certificate metadata/presets/protobuf. Native chokidar-grade watcher refinements and full drag/drop folder tree operations remain gaps.", []string{"TestOpenCollectionFromDiskAndSaveRequest", "TestOpenLegacyYMLCollectionFromDiskAndSaveRequest", "TestOpenNestedFoldersAndPreserveOriginalRequestPath", "TestOpenBruCollectionMetadataAndEnvironmentFromDisk", "TestRenameCollectionWritesYAMLMetadataAndKeepsPath", "TestRenameCollectionWritesBruConfigAndKeepsPath", "TestRenameCollectionRejectsBlankName", "TestRenameCollectionPreservesWhitespaceName", "TestCloneCollectionYAMLCopiesFormatFilesAndKeepsSource", "TestCloneCollectionBruCopiesBruFilesAndWritesConfig", "TestCloneCollectionRejectsExistingTargetAndInvalidFolder", "TestCollectionHeadersAndInheritedAuthAreApplied", "TestFolderHeadersVariablesAndAuthAreInherited", "TestFolderResponseVariablesRunBeforePostScriptsAndTests", "TestRefreshChangedCollectionsReloadsExternalDiskChanges", "TestRefreshChangedCollectionsSkipsDirtyDrafts", "TestOpenCollectionHonorsBrunoConfigIgnore", "TestOpenCollectionHonorsOpenCollectionBrunoIgnore", "TestRefreshChangedCollectionsHonorsBrunoConfigIgnore", "Computer UI smoke: folder response variables", "TestBruResponseExamplesRoundTrip", "TestBruResponseExampleFormURLEncodedRoundTrip", "TestBruResponseExampleMultipartRoundTrip", "TestBruResponseExampleFileBodyRoundTrip", "TestSaveResponseExampleWritesInlineBruExample", "TestSaveResponseExamplePreservesFormURLEncodedRows", "TestSaveResponseExamplePreservesMultipartRows", "TestSaveResponseExamplePreservesFileBodyRows", "TestCreateResponseExampleWritesInlineBruExampleAndOpensTab", "TestResponseExampleRenameCloneDeleteWritesInlineBruExamples", "TestUpdateResponseExampleEditsInlineBruExample", "TestGenerateResponseExampleCodeUsesRequestSnapshot", "TestGenerateResponseExampleCodeBodyModes", "TestGenerateResponseExampleCodeFindsSavedBruExample", "TestOpenResponseExampleTabCreatesTabForSavedExample", "TestCollectionClientCertificateMetadataRoundTrip", "TestCollectionPresetsApplyToNewRequests", "TestCollectionPresetsMetadataRoundTrip", "TestCollectionProtobufMetadataRoundTrip", "UI smoke: open nested collection path", "UI smoke: folder auth inheritance", "UI smoke: save response example", "Browser UI smoke: collection watcher external edit/dirty draft", "Browser UI smoke: collection rename", "Browser UI smoke: collection clone", "Browser UI smoke: response example rename/clone/delete", "Browser UI smoke: response example details editor", "Browser UI smoke: response example request snapshot editor", "Browser UI smoke: response example URL query sync", "Browser UI smoke: response example description editor", "Browser UI smoke: response example JSON prettify", "Browser UI smoke: response example form-url-encoded rows", "Browser UI smoke: response example multipart rows", "Browser UI smoke: response example file rows", "Browser UI smoke: response example generate code", "Browser UI smoke: response example open tab", "Browser UI smoke: sidebar response example rows/search", "Browser UI smoke: response example row reorder", "Computer UI smoke: response example edit-mode drag handles", "Computer UI smoke: response example content-type body-type sync", "Browser UI smoke: response example bulk edit", "Browser UI smoke: response example save/create/clone/delete tab lifecycle", "Computer UI smoke: manual response example create/open tab", "Browser UI smoke: collection presets", "Browser UI smoke: collection protobuf settings"}),
		feature("http", "HTTP requests", "Request execution", "done", "Send HTTP/GraphQL-over-HTTP requests with params, headers, body modes, Bruno-style Body tab form-url-encoded and multipart key/value editors with multiline value cells, multipart file-path/content-type columns, Bruno-style selected file-body table with add/remove/select rows and content-type auto-fill, .bru body:form-urlencoded/body:multipart-form/body:file plus YAML file-body save-back, auth, cookies, redirects, timeout, Bruno-compatible URL Encoding toggle, request TLS verification toggle, collection-level manual proxy with auth/bypass, app-level proxy inheritance/off/manual/system-env/macOS-system/PAC routing, collection-level PEM/PFX client certificate mTLS, TRACE selection, and response metadata.", []string{"httptest request execution", "TestBruFormURLEncodedBodyRoundTrip", "TestBruMultipartBodyRoundTrip", "TestMultipartBodyContentTypeAndFilePathInterpolation", "TestBruFileBodyRoundTrip", "TestBruMultiFileBodyRoundTrip", "TestYAMLFileBodyRoundTrip", "TestFileBodyContentTypeAndPathInterpolation", "TestFileBodyUsesSelectedRowAndCollectionRelativePath", "TestFileBodyWithoutSelectedRowSendsNoBody", "TestEncodeRequestURLMatchesBrunoToggleBehavior", "TestHTTPEncodeURLSettingControlsExecutionURL", "TestHTTPVerifyTLSSettingAllowsSelfSignedServer", "TestRequestSettingsEncodeURLRoundTrip", "TestCollectionManualProxyExecutesHTTPRequest", "TestCollectionManualProxyBypassSkipsProxy", "TestCollectionProxyInheritUsesGlobalManualProxy", "TestCollectionProxyInheritUsesSystemEnvironmentProxy", "TestGlobalProxyOffDisablesEnvironmentProxy", "TestCollectionManualProxyOverridesGlobalProxy", "TestPACProxyRoutesFromFileAndFallsBackDirect", "TestMacOSScutilProxyOutputResolvesProxyAndBypass", "TestCollectionProxyMetadataRoundTrip", "TestCollectionClientCertificateExecutesMTLSRequest", "TestCollectionClientCertificateMetadataRoundTrip", "TestCookieStoreCapturesSendsAndDeletesCookies", "TestCookieStoreCapturesAndSendsRedirectCookies", "UI smoke: Send button", "UI smoke: URL Encoding toggle", "Computer UI smoke: Verify TLS toggle", "Browser UI smoke: collection manual proxy", "Browser UI smoke: app proxy preferences", "Browser UI smoke: collection client certificate mTLS", "Browser UI smoke: form-url-encoded body editor", "Browser UI smoke: multipart body editor", "Browser UI smoke: file body editor", "Browser UI smoke: file body table add/select/remove", "UI smoke: cookie capture/replay"}),
		feature("response", "Response pane", "Request execution", "done", "Status, duration, size, headers, body, raw JSON/text previews, assertions, tests, timeline, saved response examples with create/rename/clone/delete/detail-edit controls for descriptions, request snapshots, responses, URL query-param sync, request/response JSON prettify, form-url-encoded, multipart, and file example request body rows, response-example table row reorder controls, Content-Type-to-body-type sync, and bulk edit for params/headers, cURL/fetch code generation from example request snapshots, saved example tabs, save/create/clone/delete tab lifecycle sync, sidebar child rows, and network log.", []string{"response assertions", "TestSaveResponseExampleWritesInlineBruExample", "TestSaveResponseExamplePreservesFormURLEncodedRows", "TestSaveResponseExamplePreservesMultipartRows", "TestSaveResponseExamplePreservesFileBodyRows", "TestCreateResponseExampleWritesInlineBruExampleAndOpensTab", "TestBruResponseExampleFormURLEncodedRoundTrip", "TestBruResponseExampleMultipartRoundTrip", "TestBruResponseExampleFileBodyRoundTrip", "TestResponseExampleRenameCloneDeleteWritesInlineBruExamples", "TestUpdateResponseExampleEditsInlineBruExample", "TestGenerateResponseExampleCodeUsesRequestSnapshot", "TestGenerateResponseExampleCodeBodyModes", "TestGenerateResponseExampleCodeFindsSavedBruExample", "TestOpenResponseExampleTabCreatesTabForSavedExample", "UI smoke: response tabs", "UI smoke: save response example", "Browser UI smoke: response example rename/clone/delete", "Browser UI smoke: response example details editor", "Browser UI smoke: response example request snapshot editor", "Browser UI smoke: response example URL query sync", "Browser UI smoke: response example description editor", "Browser UI smoke: response example JSON prettify", "Browser UI smoke: response example form-url-encoded rows", "Browser UI smoke: response example multipart rows", "Browser UI smoke: response example file rows", "Browser UI smoke: response example generate code", "Browser UI smoke: response example open tab", "Browser UI smoke: sidebar response example rows/search", "Browser UI smoke: response example row reorder", "Computer UI smoke: response example content-type body-type sync", "Browser UI smoke: response example bulk edit", "Browser UI smoke: response example save/create/clone/delete tab lifecycle", "Computer UI smoke: manual response example create/open tab"}),
		feature("response-timeline", "Response timeline", "Request execution", "partial", "Bruno-style Timeline tab filter chips, newest-first request/pre-request/OAuth rows, OAuth2 token-request plus authorization-code and implicit loopback/hosted/default-hosted protocol callback capture, scripted sendRequest and runRequest timeline capture with method, URL, status, duration, message, source-file details, nested scripted-entry bubbling, skipped runRequest target rows, source-based request/oauth2.0/sendRequest/runRequest badges, expanded gRPC aggregate summaries, and Bruno-style normal Send plus live gRPC Timeline event rows for request/message/response/metadata/status/end/cancel with payload, metadata, and trailer details. Remaining gaps include native OAuth browser-flow Computer smoke.", []string{"TestTimelineCapturesPreRequestSendRequest", "TestTimelineCapturesRunRequestAndBubbledSendRequest", "TestTimelineCapturesSkippedRunRequestTargets", "TestTimelineCapturesOAuth2TokenRequest", "TestOAuth2AuthorizationCodeFetchesTokenWithLoopbackCallback", "TestOAuth2AuthorizationCodeSupportsHostedCallbackBridge", "TestOAuth2AuthorizationCodeUsesHostedDefaultCallbackAndProtocolHandoff", "TestOAuth2ImplicitFetchesTokenWithLoopbackFragmentCallback", "TestOAuth2ImplicitSupportsHostedCallbackBridge", "TestOAuth2ImplicitUsesHostedDefaultCallbackAndProtocolHandoff", "TestGRPCStreamingTimelineCapturesMethodAndCounts", "TestGRPCLiveBidiStreamSendEndRecordsEvents", "Computer UI smoke: scripted sendRequest timeline", "Computer UI smoke: nested runRequest timeline", "Computer UI smoke: OAuth2 token-request timeline", "Browser UI smoke: gRPC bidi stream Timeline counts", "Browser UI smoke: gRPC normal Send event Timeline", "Browser UI smoke: gRPC live stream controls/event log"}),
		feature(
			"variables",
			"Variables and environments",
			"Variables",
			"done",
			"Collection/folder/env/request variables, collection runtime variables from setVar/deleteVar/deleteAllVars, collection/folder/request post-response variables that persist before post-response scripts/tests, .bru/.yml environment files, workspace `.env` plus collection `.env` process-env precedence, Bruno-style workspace/collection `.env Files` listing/create/edit/delete UI with Table/Raw editing, exact `.env` runtime marking, foreground live refresh with dirty-editor protection, Bruno-style collection/global environment Variables and Secrets tab separation with active-tab-scoped search and delete isolation, request variable inspector tooltips with scope, invalid-variable warnings, copy success/revert feedback, latest-value copy, direct and referenced-secret reveal/masking, create-from-tooltip, edit-from-tooltip, request auto-save, plain-Enter save, repeated same-popup edits, blur-save on outside click, read-only process-env values resolved through collection/workspace/OS `.env` precedence, nested reference resolution controls, and Bruno-compatible inline URL/body/header/param/form-url-encoded/multipart value variable spans/popups with edit, copy, success/revert, outside-click close, secret reveal/hide, invalid-name states, prompt-token highlighting without variable popovers, and URL path-param highlighting plus editable Path Param popovers/rows, workspace-level global environment CRUD/selection/copy with custom copy names and Bruno conflict naming, Bruno JSON single-object/single-file/separate-file export payloads and export-to-disk via native save dialog or explicit path, Bruno workspace.yml activeEnvironmentUid migration into per-workspace active state, Bruno/Postman environment JSON import, per-workspace active state, dynamic timestamps, secret flags with Bruno-shaped encrypted collection/global environment secret storage, Bruno legacy `$01` AES secret import hydration, safe empty hydration for `$00` safeStorage values outside Electron, and exported/imported secret scrubbing, typed values in state, Bruno prompt variable dialog values, Bruno folder-over-environment precedence, {{process.env.NAME}} interpolation, bounded recursive interpolation inside scoped variable values, and script-persisted scoped env/global/collection variable values.",
			[]string{"interpolation unit tests", "TestOpenBruCollectionMetadataAndEnvironmentFromDisk", "TestCollectionEnvironmentSecretsStoreRoundTrip", "TestCollectionEnvironmentSecretsHydrateBrunoEncryptedFallbacks", "Computer UI smoke: Bruno legacy encrypted secret hydration", "TestCollectionEnvironmentSecretHydratesForRequestExecution", "TestWorkspaceGlobalEnvironmentSelectionPrecedenceSecretsAndDiskRoundTrip", "TestWorkspaceActiveGlobalEnvironmentUIDMigratesFromWorkspaceYML", "TestGlobalEnvironmentCopyImportExportScrubsSecretsAndRoundTrips", "TestGlobalEnvironmentMultiExportAndCopyNameUsesBrunoConflictStyle", "TestSaveGlobalEnvironmentExportWritesSingleFileAndFolder", "TestDotEnvFileManagerListsSavesDeletesAndRuntimeExactEnvOnly", "TestResolveProcessEnvValuesUsesRuntimePrecedence", "TestFolderHeadersVariablesAndAuthAreInherited", "TestFolderResponseVariablesRunBeforePostScriptsAndTests", "Computer UI smoke: folder response variables", "TestProcessEnvInterpolationAndFolderEnvPrecedence", "TestDotEnvProcessEnvPrecedenceAcrossCollectionWorkspaceAndOS", "TestPromptVariableInterpolation", "TestJavaScriptRuntimePersistsScopedVariableMutations", "TestRunCollectionPersistsRuntimeVariablesAcrossRequests", "UI smoke: env panel", "Browser UI smoke: global environment CRUD/selection", "Browser UI smoke: global environment JSON export/copy/import", "Browser UI smoke: global environment export manager", "Browser UI smoke: global environment export-to-disk", "Browser UI smoke: global environment workspace.yml migration", "Browser UI smoke: Variables/Secrets environment tabs", "Browser UI smoke: active-tab-scoped environment variable search", "Browser UI smoke: Variables/Secrets delete isolation", "Browser UI smoke: request variable inspector secret reveal/copy", "Browser UI smoke: request variable inspector create/edit/nested/process-env/invalid-name", "Browser UI smoke: request variable inspector process-env precedence/copy", "Browser UI smoke: request variable inspector referenced-secret masking", "Browser UI smoke: request variable inspector editor lifecycle", "Browser UI smoke: request variable inspector copy success/latest-value", "Browser UI smoke: inline URL variable spans/popups", "Browser UI smoke: inline body variable spans/popups", "Browser UI smoke: inline request header value variable spans/popups", "Browser UI smoke: inline query-param value variable spans/popups", "Browser UI smoke: inline form-url-encoded body value variable spans/popups", "Browser UI smoke: inline multipart body value variable spans/popups", "Browser UI smoke: inline prompt variable highlighting/no-popover", "Browser UI smoke: inline URL path-param highlighting/Path rows", "Browser UI smoke: .env Files Table editor", "Browser UI smoke: .env Files live refresh", "Browser UI smoke: environment secret editing", "Browser UI smoke: .env process env precedence", "UI smoke: folder variable inheritance", "UI smoke: prompt variable dialog", "UI smoke: runner runtime variables"},
		),
		feature("auth", "Auth modes", "Auth", "partial", "UI models Bruno auth modes; backend executes Basic, Bearer, API key, OAuth2 client-credentials/password/authorization-code/implicit loopback, explicit hosted, and Bruno default-hosted protocol-handoff callback token fetch with in-process cache/refresh, encrypted OAuth2 credential-store persistence/hydration, Bruno-style credential variable export/reset for scripts, header/query/body token request extras, authorization-code browser opening with loopback/hosted callbacks, state/scope/PKCE generation, implicit fragment callback capture without timeline token leaks, Wails `bruno://`/`liteapi://` protocol callback registration, and callback/token timeline rows, preserves OAuth2 authorization-code/implicit callback/auth URL/state/PKCE fields, Digest MD5 challenge retry with Computer UI smoke, WSSE UsernameToken signing with Computer UI smoke, OAuth1 HMAC/RSA/PLAINTEXT signing with Computer UI smoke, AWS SigV4 direct credentials with Computer UI smoke, shared-file static profile signing, credential_process profiles, source_profile and credential_source=Environment AssumeRole profile chains, web_identity_token_file AssumeRoleWithWebIdentity profile chains, legacy and sso-session cached SSO profile credentials, sso-session token refresh, NTLM challenge negotiation with Computer UI smoke, and collection/folder inheritance. AWS MFA profile chains, OAuth2 in-window authorization-browser/system-browser preference parity, and broader auth-mode Computer smoke remain parity gaps.", []string{"basic/bearer/apikey unit tests", "TestOAuth2ClientCredentialsFetchesAndAppliesHeader", "TestTimelineCapturesOAuth2TokenRequest", "TestOAuth2AuthorizationCodeFetchesTokenWithLoopbackCallback", "TestOAuth2AuthorizationCodeSupportsHostedCallbackBridge", "TestOAuth2AuthorizationCodeUsesHostedDefaultCallbackAndProtocolHandoff", "TestOAuth2ImplicitFetchesTokenWithLoopbackFragmentCallback", "TestOAuth2ImplicitSupportsHostedCallbackBridge", "TestOAuth2ImplicitUsesHostedDefaultCallbackAndProtocolHandoff", "TestOAuth2PasswordGrantFetchesIDTokenIntoURL", "TestOAuth2TokenCacheReusesValidToken", "TestOAuth2CredentialStoreEncryptsAndHydrates", "TestJavaScriptRuntimeSupportsOAuth2CredentialVars", "TestOAuth2TokenCacheRefreshesExpiredToken", "TestOAuth2AdditionalParamsApplyToTokenRequest", "TestOAuth2AdditionalParamsApplyToRefreshRequest", "TestOAuth2AuthBruRoundTrip", "TestOAuth2BrowserGrantFieldsRoundTrip", "TestDigestAuthChallengeRetrySucceeds", "TestDigestAuthBruRoundTrip", "Computer UI smoke: Digest auth", "TestNTLMAuthChallengeFlowSucceeds", "TestNTLMAuthBruRoundTrip", "Computer UI smoke: NTLM auth", "TestAWSV4AuthSignsHTTPRequest", "TestAWSV4AuthLoadsProfileCredentials", "TestAWSV4ProfileCredentialsCanLoadFromConfigFile", "TestAWSV4ProfileCredentialsCanLoadFromCredentialProcess", "TestAWSV4AuthLoadsAssumeRoleProfileCredentials", "TestAWSV4ProfileCredentialsCanAssumeRoleFromEnvironmentSource", "TestAWSV4AuthLoadsWebIdentityProfileCredentials", "TestAWSV4AuthLoadsLegacySSOProfileCredentials", "TestAWSV4ProfileCredentialsCanLoadSSOSessionCredentials", "TestAWSV4ProfileCredentialsRefreshesSSOSessionToken", "TestAWSV4AuthBruRoundTrip", "Computer UI smoke: AWS SigV4 auth", "TestWSSEAuthHeaderSucceeds", "TestWSSEAuthBruRoundTrip", "Computer UI smoke: WSSE auth", "TestOAuth1AuthHeaderSignsHTTPRequest", "TestOAuth1RSASignsWithInlineAndFilePrivateKey", "TestOAuth1QueryBodyAndBodyHashPlacement", "TestOAuth1AuthBruRoundTrip", "Computer UI smoke: OAuth1 auth", "TestCollectionHeadersAndInheritedAuthAreApplied", "TestFolderHeadersVariablesAndAuthAreInherited"}),
		feature("runner", "Collection runner", "Runner", "done", "Runs executable requests, records pass/fail/skipped counts, stores per-request result rows, exposes a Bruno-style runner configuration panel for selected requests/select-all/reset/no-selection disabled run and inter-request delay, skips prompt-variable requests with Bruno-style runner skip reporting, carries collection runtime variables between rows, and honors setNextRequest plus runner.SkipRequest/stopExecution/setNextRequest controls.", []string{"runner unit test", "TestRunCollectionSkipsPromptVariableRequests", "TestRunCollectionPersistsRuntimeVariablesAcrossRequests", "TestRunCollectionHonorsStopExecution", "TestRunCollectionHonorsSetNextRequest", "TestRunCollectionWithOptionsSelectsRequestsAndAppliesDelay", "UI smoke: Run collection", "UI smoke: runner prompt skip", "UI smoke: runner runtime variables", "Browser UI smoke: runner configuration panel"}),
		feature("scripting", "Scripts and assertions", "Scripting", "partial", "Assertions, the legacy expect DSL, and a minimal embedded JavaScript runtime are executable across collection/folder/request script order, with merged collection/folder/request post-response variables evaluated before post-response scripts/tests. Pre-request scripts can mutate method, URL, headers, body, form-url-encoded req.setBody object/array/string bodies, timeout/redirect controls, response JSON parsing mode, runtime vars, current-request cookies, cross-URL cookie jar entries, and req.headerList via add/upsert/remove/clear/populate/repopulate/assimilate; post-response scripts can mutate the stored response body with res.setBody; post-response scripts and tests can make core synchronous and awaitable sendRequest HTTP calls with string/object configs, callback success/error paths, success thenables, and catchable awaited failures, run top-level await scripts, await sleep, use Promise/thenable setTimeout handles with fire-and-forget timer drain, clearable setTimeout/setImmediate and repeating setInterval handles that run until cleared, and queueMicrotask, use async test callbacks, run nested HTTP requests with awaitable runRequest by request name/path while sharing runtime variables/cookies and skipping unsupported WebSocket/gRPC targets, read process/env/global/collection variable helper APIs, persist dirty typed env/global/collection/runtime variable mutations back to state, read Bruno-style OAuth2 credential variables with reset support, consume prompt variable values supplied by the request prompt dialog, runtime metadata helpers (cwd/env/collection name/safe mode), Safe Mode getProcessEnv/interpolation without a global process object, Developer Mode process globals (version/versions.node/platform/arch/env/cwd/nextTick/global.process), TextEncoder/TextDecoder UTF-8 globals, Fetch API globals (fetch, Request, Response, Headers, AbortController, FormData, Blob) backed by the HTTP bridge, Event/CustomEvent/EventTarget globals, global crypto random/hash/HMAC/Web Crypto subtle promise helpers, root/bulk runtime variable aliases, and utils minifyJson/minifyXml, require bounded safe shims for chai, jsonwebtoken/jwt, uuid v1/v3/v4/v5/v6/v7/parse/stringify/validate helpers, nanoid, path/node:path, url/node:url plus URL/URLSearchParams, querystring/node:querystring parse/stringify helpers, os/node:os platform/path/CPU/memory/user helpers, events/node:events EventEmitter helpers, stream/node:stream basic constructor/pipeline helpers, zlib/node:zlib synchronous gzip/deflate/Brotli helpers with constants and callback aliases, crypto/node:crypto random/hash/HMAC/KDF/timing-safe/AES-CBC helpers, crypto-js AES/hash/HMAC helpers, moment formatting/arithmetic/duration helpers, global Buffer plus buffer/node:buffer, util/node:util, collection-root fs/node:fs read APIs, atob/btoa, tv4, Ajv/ajv-formats, axios, and local CommonJS files inside the collection root with caching and circular dependency support, read Bruno request/response helper methods, direct req name/tags/timeout/pathParams props, direct res url/responseTime props, callable res(...) and res.jq(...) JSON selectors for common Bruno query forms, res/json/data/dataBuffer, read byte-accurate res.getDataBuffer()/getSize(), read/update/iterate current-request cookies through cookies, use Promise/await and callback-style cookies.jar helpers, see response cookies before tests, use req.onFail, use req.headerList/res.headerList read/iteration methods with callback contexts and toObject options, receive read-only errors for response headerList writes, use test/expect/assert, common Chai-style expect helpers including throw, jsonBody, and Draft-07 jsonSchema validation, use HMAC jwt/jsonwebtoken sign/verify/decode helpers, use runner skip/stop/next-request controls, and forward console.log/debug/info/warn/error rows onto the response. Full Bruno runtime APIs, exact variable precedence, remaining OAuth implicit/browser-flow edges and gRPC scripted-request timeline depth, remaining safe/developer timer-mode differences and broader node: builtin modules, full event-loop Promise scheduling, non-HMAC JWT algorithms, unrestricted developer-mode filesystem access, and exact safe/developer sandbox error-mode parity remain gaps.", []string{"assertion unit test", "TestJavaScriptRuntimeMutatesRequestAndRunsTests", "TestJavaScriptRuntimeCapturesConsoleLogs", "TestJavaScriptRuntimeSupportsChaiLikeExpectHelpers", "TestJavaScriptRuntimeSupportsChaiRequire", "TestJavaScriptRuntimeSupportsJSONSchemaAssertions", "TestJavaScriptRuntimeSupportsJSONBodyAssertions", "TestJavaScriptRuntimeSupportsJWTLibrary", "TestJavaScriptRuntimeSupportsCommonRequireShims", "TestJavaScriptRuntimeSupportsLocalCommonJSRequire", "TestJavaScriptRuntimeExposesBrunoRequestHelpers", "TestJavaScriptRuntimeExposesBrunoResponseHelpers", "TestJavaScriptRuntimeExposesResponseDataBuffer", "TestJavaScriptRuntimeSupportsCallableResponseParser", "TestJavaScriptRuntimeSupportsSendRequest", "TestJavaScriptRuntimeSupportsAsyncAwaitHelpers", "TestJavaScriptRuntimeSupportsSafeSetTimeout", "TestJavaScriptRuntimeSupportsRepeatingSetInterval", "TestJavaScriptRuntimeSupportsProcessShim", "TestJavaScriptRuntimeSupportsTextEncodingGlobals", "TestJavaScriptRuntimeSupportsFetchAPIGlobals", "TestJavaScriptRuntimeSupportsEventTargetGlobals", "TestJavaScriptRuntimeSupportsGlobalCrypto", "TestJavaScriptRuntimeSupportsCryptoJS", "TestJavaScriptRuntimeSupportsMoment", "TestJavaScriptRuntimeSupportsRunRequest", "TestJavaScriptRuntimeSupportsBruVariableHelpers", "TestJavaScriptRuntimeSupportsBruMetadataBulkVarsAndUtils", "TestJavaScriptRuntimePersistsScopedVariableMutations", "TestRunCollectionPersistsRuntimeVariablesAcrossRequests", "TestFolderResponseVariablesRunBeforePostScriptsAndTests", "Computer UI smoke: folder response variables", "TestJavaScriptRuntimeSupportsOAuth2CredentialVars", "TestPromptVariableInterpolation", "TestJavaScriptRuntimeSetBodyFormURLEncodedParity", "TestJavaScriptRuntimeCanMutateResponseBody", "TestJavaScriptRuntimeCanControlRedirectsAndTimeout", "TestJavaScriptRuntimeCanDisableResponseJSONParsing", "TestJavaScriptRuntimeSupportsMutableRequestHeaderList", "TestJavaScriptRuntimeResponseHeaderListIsReadOnly", "TestJavaScriptRuntimeCanSkipRequest", "TestRunCollectionHonorsSetNextRequest", "TestRequestOnFailHandlerRunsForTransportError", "TestRequestOnFailHandlerErrorAugmentsResponseError", "TestJavaScriptRuntimeCanReadRequestCookies", "TestJavaScriptRuntimeCanWriteRequestCookiesBeforeSend", "TestJavaScriptRuntimeCookieJarCanWriteCrossURLCookies", "TestJavaScriptRuntimeCookieJarSupportsCallbacks", "TestJavaScriptRuntimeCookieJarSupportsPromiseAPIs", "TestImportPostmanHeaderListScriptTranslation", "UI smoke: JavaScript pre-request/post-response/tests", "UI smoke: prompt variable dialog", "UI smoke: runner runtime variables", "UI smoke: node:buffer alias", "UI smoke: node:util alias", "UI smoke: node:url alias", "UI smoke: node:fs alias", "UI smoke: node:crypto hash", "UI smoke: node:crypto advanced", "UI smoke: CommonJS circular require", "UI smoke: process shim", "UI smoke: TextEncoder/TextDecoder", "UI smoke: Fetch API globals", "UI smoke: EventTarget globals", "UI smoke: timer globals", "UI smoke: global crypto", "UI smoke: Web Crypto", "UI smoke: uuid expanded", "UI smoke: crypto-js", "UI smoke: moment", "UI smoke: chai require", "UI smoke: querystring", "UI smoke: node:os", "UI smoke: node:events", "UI smoke: node:stream", "UI smoke: node:zlib"}),
		feature("import", "Import and export", "Import/export", "partial", "Bruno JSON, single .bru, Postman nested folder/request import with collection variables, collection/folder/request post-response variables/actions, collection/folder/request Basic/Bearer/API key/Digest/AWS SigV4/OAuth1/OAuth2 auth inheritance, Insomnia V4/V5 folder/request import with environments, Basic/Bearer auth, params, variable normalization, JSON/XML/text/URL-encoded/multipart/GraphQL bodies, collection/folder/request event script preservation plus common pm.* translation for variables/tests/expect/request headers/response status-body-headers/header PropertyList methods/read-only cookies, URL query/path params, raw JSON/XML/text, URL-encoded, multipart, and GraphQL bodies, saved-response examples, and OpenAPI JSON/YAML operation import are wired, including first-tag and path folder grouping, TRACE operations and .bru round-trip, top-level webhooks, operation callbacks, x-bruno-variants, collection/path/operation server variables, path/query/header params, parameter defaults/examples/minimums, referenced and schema/example-derived request bodies, response examples with request-body pairing by example key and response header examples/defaults/refs, response-link post-response variable scripts, visible apiKey header/query rows, and basic apiKey/HTTP bearer/basic/digest/OAuth2 security mapping. Full advanced Insomnia/OpenAPI/WSDL conversion, advanced Postman pm.*/Chai translation, callback-style cookie jars, OAuth2 token/current-token extras, and invalid schema reporting remain gaps.", []string{"TestImportPostmanAndBruRoundTrip", "TestImportPostmanAdvancedAuth", "TestImportPostmanCookieScriptTranslation", "TestImportPostmanHeaderListScriptTranslation", "TestImportInsomniaV4AndV5", "TestImportOpenAPIGeneratesRequests", "TestImportOpenAPITraceAndBrunoVariants", "TestImportOpenAPIWebhooksAndCallbacks", "TestImportOpenAPIServerOverrides", "TestFolderResponseVariablesRunBeforePostScriptsAndTests"}),
		feature("protocols", "GraphQL, WebSocket, gRPC, SSE", "Protocols", "partial", "GraphQL-over-HTTP, Bruno-compatible WebSocket named message storage/execution with Computer UI smoke plus binary response base64/hex metadata and Hex-view smoke, Bruno-style live WebSocket connect/disconnect controls, top Send/per-row send over a persistent socket with auto-connect, collection manual proxy tunneling with browser UI smoke, collection client certificates for wss://, prompt-variable interpolation, keepAliveInterval millisecond settings persistence, UI editing smoke, and ping frames, shutdown cleanup, response-pane sent/received/system event history with structured event-log rendering, SSE stream detection, Bruno-compatible gRPC .bru/YAML storage plus editor fields, proto/reflection-backed unary gRPC execution, collection-level proto file/import-path fallback, TLS grpcs:// verification toggle and collection client-certificate mTLS with browser UI smoke, Unix-socket execution, method discovery/sample body generation with request metadata/auth on reflection, grpcurl command generation with browser UI smoke, User-Agent metadata mapped to the gRPC channel user agent, stream request/response count headers, expanded Timeline stream-count summaries plus normal Send request/message/response/metadata/status/end/error event rows with failed-stream partial payload and trailer detail, server/client/bidi streaming runtime with metadata plus OAuth2/WSSE auth metadata and Computer UI smoke, and Bruno-style live gRPC start/send/end/cancel controls with response-pane sent/received/system event history, receive-before-End updates while streams stay connected, headers, separated Metadata/Trailers tabs, Bruno-style live request/message/response/metadata/status/end Timeline rows, fixture-log UI smoke, and cancel coverage. Remaining gRPC gaps include advanced call credentials and provider-specific credential edge cases.", []string{"TestWebSocketRequestSendsAndReadsMessage", "TestWebSocketLiveConnectionUsesCollectionManualProxy", "TestWebSocketRequestUsesCollectionClientCertificate", "TestWebSocketRequestSendsMultipleSelectedMessages", "TestWebSocketPersistentConnectionSendsMessagesWithoutReconnect", "TestWebSocketKeepAliveIntervalSendsPingFrames", "TestWebSocketBinaryResponsePreservesBase64AndHex", "TestWebSocketBinaryEventArrayIncludesBase64AndHex", "TestWebSocketMessagesBruAndYAMLRoundTrip", "Computer UI smoke: WebSocket editor/executor", "Browser UI smoke: WebSocket binary Hex view", "Browser UI smoke: WebSocket live connect/disconnect", "Browser UI smoke: WebSocket keep-alive setting", "Browser UI smoke: WebSocket collection proxy", "Browser UI smoke: WebSocket structured event log", "TestSSEPreviewModeIsDetected", "TestGrpcBruRoundTripPreservesMetadataProtoPathMethodTypeAndMessages", "TestOpenGrpcBruCollectionAndSavePreservesGrpcBlocks", "TestGrpcYAMLRoundTrip", "TestGRPCUnaryRequestExecutesWithProtoAndMetadata", "TestGRPCUnaryRequestExecutesOverUnixSocket", "TestGRPCUnaryRequestCanUseServerReflection", "TestGRPCUnaryRequestUsesUserAgentHeaderAsDialUserAgent", "TestGRPCUnaryRequestUsesCollectionClientCertificate", "TestGenerateGrpcurlCommandIncludesMetadataProtoAndBody", "TestGenerateGrpcurlCommandUsesCollectionClientCertificate", "TestGenerateGrpcurlCommandUsesHeredocForClientStreaming", "TestGRPCServerStreamingRequestReturnsJSONMessages", "TestGRPCServerStreamingErrorPreservesMetadataTrailersAndPartialResponses", "TestGRPCClientStreamingRequestSendsAllMessages", "TestGRPCBidiStreamingRequestSendsAndReceivesMessages", "TestGRPCStreamingTimelineCapturesMethodAndCounts", "TestGRPCLiveBidiStreamSendEndRecordsEvents", "TestGRPCLiveStreamCancelMarksSessionCancelled", "TestGRPCMethodsFromProtoGenerateSampleMessage", "TestGRPCMethodsFromReflectionGenerateSampleMessage", "TestCollectionProtobufFallbackLoadsImportedMethodsAndSamples", "TestGRPCUnaryRequestUsesCollectionProtobufConfig", "Computer UI smoke: gRPC editor/executor", "Computer UI smoke: gRPC Unix socket", "Browser UI smoke: collection protobuf gRPC method generation", "Browser UI smoke: gRPC collection client certificate mTLS", "Browser UI smoke: gRPC grpcurl command generation", "Browser UI smoke: gRPC bidi stream Timeline counts", "Browser UI smoke: gRPC normal Send event Timeline", "Browser UI smoke: gRPC failed stream metadata/trailer Timeline", "Browser UI smoke: gRPC User-Agent metadata", "Browser UI smoke: gRPC live stream controls/event log", "npm run check", "npm run build", "UI smoke: protocol tabs"}),
		feature("git-remotes", "Git-backed collections", "Workspace and app state", "done", "Workspace collection Git remote metadata, URL validation, connect/remove controls, managed .gitignore entries, missing-local ghost rows, git --version detection with dedicated Git-required modal, clone with Wails event-backed progress streaming, scan, and open-selected collections are wired and UI-smoked.", []string{"TestGitRemoteMetadataManagedIgnoreAndGhostRows", "TestGitCloneScanAndOpenSelectedCollections", "TestGitVersionReportsMissingGit", "Computer UI smoke: Git connect/remove, ghost rows, clone/open selected", "Browser UI smoke: Git clone progress streaming"}),
		feature("preferences", "Preferences and proxy", "App shell", "partial", "Preferences, Bruno-style keybinding preferences with grouped command defaults, enabled/reset/custom shortcut controls, persisted response-pane orientation, app-level proxy modes (Off, On/manual, System Proxy from environment variables plus macOS scutil settings, PAC URL/file source), collection-level proxy inherit/enabled/disabled overrides, collection-level client certificate editor/execution, Bruno-style notification center with unread badge, All/Unread tabs, read/clear controls, support links, Golden Edition licensing modal, Bruno-style Dev Tools surface with Console/Network/Performance/Terminal tabs, persisted bottom-drawer open state/active tab/drawer height/details panel width, Network method filters, persisted Network sort and column widths, sortable request table, Request Details tabs, captured request/response headers and bounded bodies, size/duration network log, Performance CPU/process sampling selector/cards, PTY-backed terminal session create/list/input/resize/kill, Collection overview Open in Terminal action that starts a session at the collection cwd, and platform-labeled Reveal in Finder/Show in Folder collection action are present; non-macOS OS proxy discovery, full PAC JavaScript evaluation, xterm-grade terminal rendering/keyboard control, richer CPU history/graphs, richer per-request timelines, and broader preference panes remain gaps.", []string{"state persistence", "TestPreferencesKeybindingsPersistAndNormalize", "TestPreferencesLayoutPersistAndNormalize", "Computer UI smoke: keybinding preferences", "TestNotificationsMarkReadAndClearPersist", "Computer UI smoke: notification center", "Computer UI smoke: support and Golden Edition links", "TestDevToolsSnapshotReportsRuntimeAndStateCounts", "TestCalculateCPUPercent", "TestSendRequestInterpolatesExecutesAndRecordsResponse", "TestPreferencesDevToolsPersistAndNormalize", "TestTerminalSessionLifecycleRunsShellCommand", "TestRevealInFolderCommandUsesPlatformSelector", "TestRevealCollectionInFolderUsesCollectionPath", "Computer UI smoke: Dev Tools tabs", "Computer UI smoke: Dev Tools CPU/process sampling", "Computer UI smoke: Dev Tools network filters/sort/details", "Computer UI smoke: Dev Tools persisted Network state/columns", "Browser UI smoke: Dev Tools bottom drawer resize persistence", "Computer UI smoke: Dev Tools PTY terminal session", "Browser UI smoke: collection Open in Terminal", "Browser UI smoke: collection Reveal in Finder", "TestCollectionManualProxyExecutesHTTPRequest", "TestCollectionManualProxyBypassSkipsProxy", "TestCollectionProxyInheritUsesGlobalManualProxy", "TestCollectionProxyInheritUsesSystemEnvironmentProxy", "TestGlobalProxyOffDisablesEnvironmentProxy", "TestCollectionManualProxyOverridesGlobalProxy", "TestPACProxyRoutesFromFileAndFallsBackDirect", "TestMacOSScutilProxyOutputResolvesProxyAndBypass", "TestCollectionProxyMetadataRoundTrip", "TestCollectionClientCertificateExecutesMTLSRequest", "TestCollectionClientCertificateMetadataRoundTrip", "TestUpdateCollectionClientCertificatesPreservesBlankEditorRows", "TestGRPCUnaryRequestUsesCollectionClientCertificate", "Browser UI smoke: collection manual proxy", "Browser UI smoke: app proxy preferences", "Browser UI smoke: collection client certificate mTLS", "Browser UI smoke: gRPC collection client certificate mTLS"}),
		feature("cookies", "Cookie manager", "App shell", "done", "Persistent cookie capture from final and redirect responses, matching, replay on direct and redirected requests, manual Cookie header merge, list, row delete, clear-all, add/edit form, raw Set-Cookie import, search, domain grouping, clear-domain controls, trustworthy loopback Secure-cookie matching, __Secure-/__Host- prefix validation, Bruno/tough-cookie-style foreign-domain, public-suffix, and IP-domain rejection, invalid raw/script cookie path normalization, expired Set-Cookie deletion, encrypted cookie value persistence/hydration, current-request cookies script read/write/iteration access, and Promise/await and callback-style cross-URL cookies.jar helpers are wired. Add/import/search/edit/domain grouping/per-domain clear Computer smoke is complete, and encrypted cookie persistence plus cookie validation are backend-tested and Wails browser UI-smoked.", []string{"TestCookieStoreCapturesSendsAndDeletesCookies", "TestCookieStoreCapturesAndSendsRedirectCookies", "TestCookieStoreCapturesRedirectCookieWhenRedirectsDisabled", "TestCookieStoreMergesManualCookieHeader", "TestCookieManagerManualRawAndDomainClear", "TestCookieStoreSendsSecureCookiesToLoopbackHTTP", "TestCookiePrefixValidationMatchesBrunoJarRules", "TestCookiePersistenceEncryptsValuesAndHydrates", "TestCookieRuntimeValidationRejectsForeignPublicSuffixAndNormalizesPaths", "TestJavaScriptRuntimeCanReadRequestCookies", "TestJavaScriptRuntimeCanWriteRequestCookiesBeforeSend", "TestJavaScriptRuntimeCookieJarCanWriteCrossURLCookies", "TestJavaScriptRuntimeCookieJarSupportsCallbacks", "TestJavaScriptRuntimeCookieJarSupportsPromiseAPIs", "TestJavaScriptRuntimeCookieJarRejectsInvalidDomainsAndNormalizesPaths", "UI smoke: cookie manager capture/replay/delete", "UI smoke: cookie manager add/import/search/edit/grouping/domain clear", "Browser UI smoke: encrypted cookie persistence", "Browser UI smoke: cookie validation"}),
		feature("shortcuts", "Shortcuts and editor state", "App shell", "partial", "Cmd/Ctrl+Enter send shortcut, Bruno-style Cmd/Ctrl+K global search with collection/request/folder/url/path results plus Bruno Documentation external result and keyboard navigation, Cmd/Ctrl+J Change Orientation with a response layout toggle and persisted horizontal/vertical workbench state, Cmd/Ctrl+\\ Collapse Sidebar with a titlebar-style toggle button and non-persisted 0px/expanded rail state, customizable keybinding lookup/toggle/reset for implemented app-shell actions, tab state with per-tab request/response pane persistence, opening sidebar requests into tabs, sidebar request search, Save All Tabs scoped to the active collection with duplicate request filename protection, close current/all tabs, scoped/global reopen-last-closed-tab history, switch previous/next/last/tab 1-8, move active tab left/right, and Import/types.Environment/Preferences/Terminal navigation shortcuts are wired; full CodeMirror folding and broader command palette parity remain gaps.", []string{"TestOpenRequestTabCreatesTabForExistingItem", "TestUpdateOpenTabPanesPersistsPaneSelection", "TestOpenTabManagementPersistsOrderAndActiveState", "TestSaveAllTabsWritesOpenRequestTabs", "TestSaveAllTabsAssignsUniquePathsForDuplicateRequestNames", "TestPreferencesKeybindingsPersistAndNormalize", "TestPreferencesLayoutPersistAndNormalize", "UI smoke: keyboard send", "UI smoke: sidebar request search", "Browser UI smoke: global search modal", "Computer UI smoke: global search documentation result", "Computer UI smoke: tab pane persistence", "Computer UI smoke: keybinding preferences", "Browser UI smoke: tab-management shortcuts", "Browser UI smoke: reopen last closed tab shortcut", "Browser UI smoke: save all tabs shortcut", "Browser UI smoke: change orientation shortcut and response layout toggle", "Browser UI smoke: collapse sidebar shortcut and toggle button", "Browser UI smoke: app-navigation shortcuts", "UI smoke: sidebar opens request from Cookies view"}),
	}
	for i := range features {
		switch features[i].ID {
		case "workspace":
			features[i].Description = strings.Replace(features[i].Description, "per-tab request/response pane selections.", "per-tab request/response pane selections plus Bruno-style per-workspace Scratch collection mounting with scratch collection/tab exclusion from persisted snapshots.", 1)
			features[i].Tests = append(features[i].Tests, "TestWorkspaceScratchCollectionIsTransientAndFileBacked", "Computer UI smoke: scratch collection request")
		case "collections":
			features[i].Description = strings.Replace(features[i].Description, "Create collections, create requests", "Create collections, create regular and scratch/transient requests", 1)
			features[i].Description = strings.Replace(features[i].Description, "Create collections, create regular and scratch/transient requests", "Create collections, create regular and scratch/transient requests, create Bruno-style root/nested folders with sanitized filesystem names, duplicate/reserved-name validation, sibling seq, inherited auth folder.bru/folder.yml metadata, and rename folders by updating display metadata or moving filesystem directories with nested folder/request state updates", 1)
			features[i].Description = strings.Replace(features[i].Description, "nested folder/request state updates", "nested folder/request state updates, clone folders recursively with Bruno-style defaults/fresh request ids/no auto-open behavior/WebSocket skip parity, and delete folders recursively with Bruno-style sibling resequence plus request-tab cleanup", 1)
			features[i].Description = strings.Replace(features[i].Description, "clone folders recursively with Bruno-style defaults/fresh request ids/no auto-open behavior/WebSocket skip parity", "clone folders recursively with Bruno-style defaults/fresh request ids/no auto-open behavior/WebSocket skip parity, clone requests with Bruno-style modal defaults, sibling filename validation, fresh request/example ids, request-only seq, disk write, and cloned-tab activation", 1)
			features[i].Description = strings.Replace(features[i].Description, "disk write, and cloned-tab activation", "disk write, cloned-tab activation, and rename requests with Bruno-style name-only versus filesystem rename behavior, draft save, sibling filename validation, request-id/tab preservation, and no resequence", 1)
			features[i].Description = strings.Replace(features[i].Description, "request-id/tab preservation, and no resequence", "request-id/tab preservation, no resequence, and delete requests with Bruno-style file unlink, sibling resequence, request-tab-only close, response-example tab retention, missing-file guard, and no success toast", 1)
			features[i].Description = strings.Replace(features[i].Description, "no success toast", "no success toast, plus Bruno-style sidebar Info modals for folders and requests showing only display and filesystem names, item-level Reveal in Finder/Show in Folder for folders and requests, request-row Generate Code for HTTP/GraphQL current draft requests, and folder-row Open in Terminal sessions rooted at physical folder paths", 1)
			features[i].Description = strings.Replace(features[i].Description, "save Bruno files back to original request paths", "save Bruno files back to original request paths and save scratch requests into bruno-scratch-* temp collection files", 1)
			features[i].Description = strings.Replace(features[i].Description, "edit collection headers/auth/client certificate metadata/presets/protobuf.", "edit collection headers/auth/client certificate metadata/presets/protobuf plus Bruno-style folder settings for headers, pre/post variables, auth, scripts, tests, and docs with folder.bru/folder.yml save-back.", 1)
			features[i].Description = strings.Replace(features[i].Description, "folder.bru/folder.yml save-back.", "folder.bru/folder.yml save-back plus Bruno-style opt-in collection file cache for opened collection parse snapshots with size/clear controls.", 1)
			features[i].Description = strings.Replace(features[i].Description, "size/clear controls.", "size/clear controls plus Bruno-style Generate Documentation standalone HTML export with collection version/counts, OpenCollection YAML payload, sidebar ordering, and environment filtering.", 1)
			features[i].Tests = append(features[i].Tests, "TestCreateFolderYAMLWritesFolderConfigAndNestedFolder", "TestCreateFolderBruWritesBrunoFolderDefaultsAndSequence", "TestCreateFolderRejectsDuplicateReservedInvalidAndExistingDirectory", "TestRenameFolderYAMLMovesDirectoryAndUpdatesNestedState", "TestRenameFolderBruUpdatesMetadataWithoutMovingWhenFilenameSame", "TestRenameFolderRejectsDuplicateReservedInvalidAndBlank", "TestDeleteFolderYAMLRemovesDirectoryNestedItemsAndRequestTabs", "TestDeleteFolderBruRemovesFolderAndKeepsSiblingsResequenced", "TestDeleteFolderRejectsMissingNotFoundLocallyAndMissingDirectory", "TestCloneFolderYAMLCopiesNestedFoldersRequestsAndFreshIDs", "TestCloneFolderBruCopiesExamplesAndSkipsWebSocketLikeBruno", "TestCloneFolderRejectsDuplicateReservedInvalidMissingAndNotFoundLocally", "TestCloneRequestYAMLCopiesRequestOpensTabAndWritesUniqueFile", "TestCloneRequestBruRefreshesExampleIDsAndPreservesExamples", "TestCloneRequestRejectsDuplicateReservedInvalidMissingAndNotFoundLocally", "Browser UI smoke: new folder and nested folder", "Browser UI smoke: rename folder display and filesystem names", "Browser UI smoke: delete folder recursive removal", "Browser UI smoke: clone folder recursive copy", "Browser UI smoke: clone request row/modal/disk", "TestUpdateFolderSettingsWritesFolderBruAndAffectsRequests", "Browser UI smoke: folder settings editor", "TestWorkspaceScratchCollectionIsTransientAndFileBacked", "Computer UI smoke: scratch collection request", "TestCollectionFileCacheCachesInvalidatesAndClears", "Browser UI smoke: cache file preferences", "TestGenerateCollectionDocsBuildsBrunoStyleHTMLAndFiltersEnvironments", "Browser UI smoke: generate collection docs")
			features[i].Tests = append(features[i].Tests, "TestRenameRequestYAMLUpdatesNameKeepsFilePathAndTab", "TestRenameRequestBruMovesFilePreservesIDExamplesAndTab", "TestRenameRequestRejectsDuplicateReservedInvalidMissingAndNotFoundLocally", "Browser UI smoke: rename request row/modal/disk")
			features[i].Tests = append(features[i].Tests, "TestDeleteRequestYAMLRemovesFileResequencesAndClosesOnlyRequestTab", "TestDeleteRequestBruRemovesFileAndKeepsSiblingsResequenced", "TestDeleteRequestRejectsMissingFileAndNotFoundLocally", "Browser UI smoke: delete request row/modal/disk/no-toast")
			features[i].Tests = append(features[i].Tests, "Browser UI smoke: sidebar item Info modal", "TestRevealCollectionItemActionsUseFolderAndRequestPaths", "TestRevealCollectionItemActionsRejectMissingTargets", "Browser UI smoke: sidebar item Reveal in Finder and folder Open in Terminal", "TestGenerateRequestCodeUsesCurrentRequestAndVariables", "TestGenerateRequestCodeSupportsGraphQLAndRejectsInvalidTargets", "Browser UI smoke: request-row Generate Code")
		case "variables":
			features[i].Description = strings.Replace(features[i].Description, "collection/folder/request post-response variables that persist before post-response scripts/tests", "collection/folder/request post-response variables that persist before post-response scripts/tests, editable folder pre-request/post-response variables with folder.bru/folder.yml save-back", 1)
			features[i].Tests = append(features[i].Tests, "TestUpdateFolderSettingsWritesFolderBruAndAffectsRequests", "Browser UI smoke: folder settings editor")
		case "auth":
			features[i].Description = strings.Replace(features[i].Description, "collection/folder inheritance.", "collection/folder inheritance plus folder auth settings UI/save-back.", 1)
			features[i].Description = strings.Replace(features[i].Description, "authorization-code browser opening with loopback/hosted callbacks", "authorization-code browser opening with loopback/hosted callbacks plus Bruno-style in-app authorization modal/system-browser preference routing", 1)
			features[i].Description = strings.Replace(features[i].Description, "sso-session token refresh, NTLM challenge negotiation", "sso-session token refresh, MFA AssumeRole profile chains with env/profile token-code providers, NTLM challenge negotiation", 1)
			features[i].Description = strings.Replace(features[i].Description, "AWS MFA profile chains, OAuth2 in-window authorization-browser/system-browser preference parity", "Interactive AWS MFA token prompts, OAuth2 embedded-window request-header/session interception parity", 1)
			features[i].Tests = append(features[i].Tests, "TestUpdateFolderSettingsWritesFolderBruAndAffectsRequests", "Browser UI smoke: folder settings editor", "TestAWSV4AuthLoadsAssumeRoleProfileCredentialsWithMFA", "TestAWSV4ProfileCredentialsRequireMFATokenCode", "TestPreferencesOAuth2UseSystemBrowserPersists", "TestOAuth2AuthorizationBrowserPreferenceSelectsInAppOrSystemOpener")
		case "import":
			features[i].Description = strings.Replace(features[i].Description, "OpenAPI JSON/YAML operation import are wired", "OpenAPI JSON/YAML operation import plus Bruno-style Share Collection ZIP export, bundled single-file OpenCollection YAML export, explicit save paths, environment secret scrubbing, and Postman HTTP/GraphQL export with WebSocket/gRPC warning are wired", 1)
			features[i].Description = strings.Replace(features[i].Description, "Postman HTTP/GraphQL export with WebSocket/gRPC warning are wired", "Postman HTTP/GraphQL export with WebSocket/gRPC warning, and OpenAPI sync connect/check/apply with HTTP/file fetch/cache, persisted brunoConfig.openapi/OpenCollection metadata, OpenAPI 3.x validation, endpoint diffing, and spec-driven merge while preserving user values/scripts/tests/assertions are wired", 1)
			features[i].Description = strings.Replace(features[i].Description, "spec-driven merge while preserving user values/scripts/tests/assertions are wired", "spec-driven merge while preserving user values/scripts/tests/assertions, Bruno-style endpoint decisions (`keep-mine`/`accept-incoming`), default spec-removed endpoint deletion, and compact review/bulk decision controls are wired", 1)
			features[i].Description = strings.Replace(features[i].Description, "compact review/bulk decision controls are wired", "compact review/bulk decision controls, Collection Changes local drift detection/reset/restore/delete/revert/Open actions with shape-only request comparisons, OpenAPI Connection Settings with Bruno-style auto-check intervals plus lightweight hash polling, View spec cached/source-fallback content viewing, and View Spec Diff current/updated line comparison plus Previous/Next navigation are wired", 1)
			features[i].Tests = append(features[i].Tests, "TestShareCollectionExportsYamlZipPostmanAndSaves", "Browser UI smoke: Share Collection ZIP/YAML/Postman", "TestOpenAPISyncApplyPreservesUserValuesAndScripts", "TestOpenAPISyncApplyHonorsEndpointDecisions", "TestOpenAPISyncApplyCanKeepRemovedEndpoint", "TestUpdateOpenAPISyncConfigPersistsAutoCheckSettings", "TestCheckOpenAPIUpdatesComparesRemoteSpecHash", "TestGetOpenAPISyncSpecReturnsCachedSpec", "TestGetOpenAPISyncSpecFetchesWhenCacheMissing", "TestGetOpenAPISyncSpecDiffComparesStoredAndIncomingSpec", "TestGetOpenAPISyncSpecDiffFetchesConfiguredSource", "TestOpenAPISyncFetchCacheAndRejectsSwagger", "TestOpenAPILocalDriftDetectsAndAppliesCollectionChanges", "TestOpenAPILocalDriftIgnoresPreservedValues", "Browser UI smoke: OpenAPI Sync connect/check/apply", "Browser UI smoke: OpenAPI Sync review decisions", "Browser UI smoke: OpenAPI Collection Changes drift revert", "Browser UI smoke: OpenAPI Connection Settings auto-check", "Browser UI smoke: OpenAPI View spec", "Browser UI smoke: OpenAPI Spec Diff modal", "Browser UI smoke: OpenAPI Spec Diff navigation", "Browser UI smoke: OpenAPI Collection Changes Open")
		case "protocols":
			features[i].Description = strings.Replace(features[i].Description, "Bruno-compatible WebSocket named message storage/execution", "Bruno-compatible WebSocket named message and docs/vars/scripts/tests storage/execution", 1)
			features[i].Description = strings.Replace(features[i].Description, "Bruno-compatible gRPC .bru/YAML storage plus editor fields", "Bruno-compatible gRPC .bru/YAML storage including docs/vars/scripts/tests plus editor fields", 1)
			features[i].Description = strings.Replace(features[i].Description, "response-pane sent/received/system event history, headers, fixture-log UI smoke, and cancel coverage", "response-pane sent/received/system event history, receive-before-End updates while streams stay connected, headers, fixture-log UI smoke, and cancel coverage", 1)
			features[i].Description = strings.Replace(features[i].Description, "server/client/bidi streaming runtime with metadata work and Computer UI smoke", "server/client/bidi streaming runtime with metadata plus OAuth2/WSSE auth metadata and Computer UI smoke", 1)
			features[i].Description = strings.Replace(features[i].Description, "headers, fixture-log UI smoke", "headers, separated Metadata/Trailers tabs, Bruno-style live request/message/response/metadata/status/end Timeline rows, fixture-log UI smoke", 1)
			features[i].Tests = append(features[i].Tests, "Browser UI smoke: gRPC async receive-before-End", "Browser UI smoke: gRPC Metadata/Trailers tabs and event Timeline", "TestGRPCUnaryRequestAppliesOAuth2AndWSSEMetadata", "Browser UI smoke: gRPC WSSE auth metadata", "TestGRPCReflectionUsesRequestMetadataAndAuth", "Browser UI smoke: authenticated gRPC reflection method loading", "TestRequestDocsRoundTripForWebSocketAndGRPCBru")
		}
		if features[i].ID == "scripting" {
			features[i].Description = strings.Replace(features[i].Description, "with merged collection/folder/request post-response variables evaluated before post-response scripts/tests", "with merged collection/folder/request post-response variables evaluated before post-response scripts/tests plus editable folder scripts/tests save-back", 1)
			features[i].Tests = append(features[i].Tests, "TestUpdateFolderSettingsWritesFolderBruAndAffectsRequests", "Browser UI smoke: folder settings editor")
			features[i].Description = strings.Replace(features[i].Description, "Fetch API globals (fetch, Request, Response, Headers, AbortController, FormData, Blob) backed by the HTTP bridge", "Fetch API globals (fetch, Request, Response, Headers, AbortController, FormData, Blob) backed by the HTTP bridge plus node-fetch/node-fetch/commonjs module aliases", 1)
			features[i].Description = strings.Replace(features[i].Description, "uuid v1/v3/v4/v5/v6/v7/parse/stringify/validate helpers, nanoid", "uuid v1/v3/v4/v5/v6/v7/parse/stringify/validate helpers, lodash/underscore get/set/cloneDeep/isEqual/chain/collection helpers plus lodash/<helper> subpath imports, nanoid", 1)
			features[i].Description = strings.Replace(features[i].Description, "nanoid, path/node:path", "nanoid, path/node:path plus Developer Mode path/posix/node:path/posix and path/win32/node:path/win32", 1)
			features[i].Description = strings.Replace(features[i].Description, "stream/node:stream basic constructor/pipeline helpers", "stream/node:stream basic constructor/pipeline helpers plus Developer Mode stream/promises/node:stream/promises pipeline/finished promises", 1)
			features[i].Description = strings.Replace(features[i].Description, "global Buffer plus buffer/node:buffer, util/node:util", "global Buffer plus buffer/node:buffer, util/node:util plus Developer Mode util/types/node:util/types", 1)
			features[i].Description = strings.Replace(features[i].Description, "crypto-js AES/hash/HMAC helpers, moment", "crypto-js AES/hash/HMAC helpers, xml-formatter pretty/minify helpers, moment", 1)
			features[i].Description = strings.Replace(features[i].Description, "xml-formatter pretty/minify helpers, moment", "xml-formatter pretty/minify helpers, yaml parse/stringify/parseDocument helpers, moment", 1)
			features[i].Description = strings.Replace(features[i].Description, "xml-formatter pretty/minify helpers, yaml parse/stringify/parseDocument helpers, moment", "xml-formatter pretty/minify helpers, xml2js parseString/Parser helpers, yaml parse/stringify/parseDocument helpers, moment", 1)
			features[i].Description = strings.Replace(features[i].Description, "xml-formatter pretty/minify helpers, xml2js parseString/Parser helpers, yaml parse/stringify/parseDocument helpers, moment", "xml-formatter pretty/minify helpers, cheerio load/selector/text/class helpers, xml2js parseString/Parser helpers, yaml parse/stringify/parseDocument helpers, moment", 1)
			features[i].Description = strings.Replace(features[i].Description, "top-level await scripts, await sleep, use Promise/thenable setTimeout handles with fire-and-forget timer drain, clearable setTimeout/setImmediate and repeating setInterval handles that run until cleared, and queueMicrotask, use async test callbacks", "top-level await scripts, await sleep, use collection-scoped Safe/Developer JavaScript sandbox mode with isSafeMode(), Safe Mode local Promise/thenable setTimeout handles with fire-and-forget timer drain and hidden global timer helpers, Developer Mode clearable setTimeout/setImmediate and repeating setInterval handles that run until cleared plus queueMicrotask, use async test callbacks", 1)
			features[i].Description = strings.Replace(features[i].Description, "collection-root fs/node:fs read APIs", "Developer Mode fs/node:fs filesystem read/write APIs with Safe Mode fs withheld", 1)
			features[i].Description = strings.Replace(features[i].Description, "Developer Mode fs/node:fs filesystem read/write APIs with Safe Mode fs withheld", "Developer Mode process/node:process module, Developer Mode console/node:console module, Developer Mode timers/node:timers and timers/promises/node:timers/promises modules, Developer Mode assert/node:assert and assert/strict/node:assert/strict modules, Developer Mode dns/node:dns and dns/promises/node:dns/promises lookup/resolve modules, Developer Mode http/node:http and https/node:https client request/get modules, plus Developer Mode fs/node:fs and fs/promises/node:fs/promises filesystem read/write APIs with Safe Mode fs withheld", 1)
			features[i].Description = strings.Replace(features[i].Description, "local CommonJS files inside the collection root with caching and circular dependency support", "local CommonJS files inside the collection root with caching and circular dependency support plus Developer Mode package.json main, JSON modules, scoped packages, nested package dependencies, and collection node_modules package resolution", 1)
			features[i].Description = strings.Replace(features[i].Description, "remaining safe/developer timer-mode differences and broader node: builtin modules", "broader safe/developer sandbox API and node: builtin module differences", 1)
			features[i].Description = strings.Replace(features[i].Description, "unrestricted developer-mode filesystem access, and exact safe/developer sandbox error-mode parity", "developer-mode native module loading and exact safe/developer sandbox error-mode parity", 1)
			features[i].Tests = append(features[i].Tests, "Browser UI smoke: lodash")
			features[i].Tests = append(features[i].Tests, "UI smoke: lodash subpath imports")
			features[i].Tests = append(features[i].Tests, "UI smoke: node-fetch")
			features[i].Tests = append(features[i].Tests, "UI smoke: xml-formatter")
			features[i].Tests = append(features[i].Tests, "UI smoke: cheerio")
			features[i].Tests = append(features[i].Tests, "UI smoke: xml2js")
			features[i].Tests = append(features[i].Tests, "UI smoke: yaml")
			features[i].Tests = append(features[i].Tests, "TestJavaScriptRuntimeSupportsDeveloperTimerGlobals", "TestJavaScriptRuntimeSafeModeHidesProcessGlobal", "TestJavaScriptRuntimeDeveloperModeSupportsFSBuiltin", "TestJavaScriptRuntimeDeveloperModeSupportsFSPromisesBuiltin", "TestJavaScriptRuntimeDeveloperModeSupportsPathSubmodules", "TestJavaScriptRuntimeDeveloperModeSupportsStreamPromisesBuiltin", "TestJavaScriptRuntimeDeveloperModeSupportsUtilTypesBuiltin", "TestJavaScriptRuntimeDeveloperModeSupportsProcessBuiltin", "TestJavaScriptRuntimeDeveloperModeSupportsConsoleBuiltin", "TestJavaScriptRuntimeDeveloperModeSupportsTimersBuiltins", "TestJavaScriptRuntimeDeveloperModeSupportsAssertBuiltin", "TestJavaScriptRuntimeDeveloperModeSupportsDNSBuiltin", "TestJavaScriptRuntimeDeveloperModeSupportsHTTPBuiltins", "TestJavaScriptRuntimeDeveloperModeSupportsPackageRequire", "Browser UI smoke: JavaScript sandbox mode", "Browser UI smoke: Safe/Developer process globals", "Browser UI smoke: Developer Mode fs", "Browser UI smoke: Developer Mode fs/promises", "Browser UI smoke: Developer Mode path submodules", "Browser UI smoke: Developer Mode stream/promises", "Browser UI smoke: Developer Mode util/types", "Browser UI smoke: Developer Mode process builtin", "Browser UI smoke: Developer Mode console", "Browser UI smoke: Developer Mode timers", "Browser UI smoke: Developer Mode assert", "Browser UI smoke: Developer Mode dns", "Browser UI smoke: Developer Mode http/https builtins")
			break
		}
	}
	for i := range features {
		switch features[i].ID {
		case "http":
			features[i].Description = strings.Replace(features[i].Description, "PAC routing", "PAC FindProxyForURL routing", 1)
			features[i].Tests = append(features[i].Tests, "TestPACProxyEvaluatesFindProxyForURLLogic", "Browser UI smoke: PAC FindProxyForURL conditional routing")
		case "preferences":
			features[i].Description = strings.Replace(features[i].Description, "Preferences,", "Preferences with Bruno-style Appearance theme mode (Light/Dark/System), all Bruno light/dark theme variants, persisted effective app styling,", 1)
			features[i].Description = strings.Replace(features[i].Description, "Appearance theme mode", "General custom CA/keep-default CA, SSL verification, request timeout, split store/send cookie controls, default location, auto-save delay, Cache File cache size/clear/toggle controls plus SSL session cache controls, Display Code Editor Font/Font Size controls with live editor styling, Display zoom selector/persistence, Appearance theme mode", 1)
			features[i].Description = strings.Replace(features[i].Description, "keybinding preferences", "keybinding preferences plus Bruno-style OAuth2 Use system browser preference", 1)
			features[i].Description = strings.Replace(features[i].Description, "PAC URL/file source", "PAC URL/file source with FindProxyForURL evaluation and common PAC helpers", 1)
			features[i].Description = strings.Replace(features[i].Description, "full PAC JavaScript evaluation", "advanced PAC helper/cache parity", 1)
			features[i].Tests = append(features[i].Tests, "TestPreferencesThemeModeAndVariantsPersist", "Computer UI smoke: theme preferences", "TestPreferencesDisplayZoomPersistAndNormalize", "TestPreferencesFontPersistAndNormalize", "TestPreferencesGeneralRequestAutoSaveCachePersistAndNormalize", "TestHTTPCustomCaCertificateAllowsSelfSignedServer", "TestSSLSessionCacheEnablesTLSResumption", "TestPreferencesStoreAndSendCookiesAreSeparate", "TestPreferencesRequestTimeoutOverridesHTTPRequest", "TestCollectionFileCacheCachesInvalidatesAndClears", "Browser UI smoke: display zoom preferences", "Browser UI smoke: display code font preferences", "Browser UI smoke: General and Cache preferences", "Browser UI smoke: cache file preferences", "TestPreferencesOAuth2UseSystemBrowserPersists", "TestPACProxyEvaluatesFindProxyForURLLogic", "Browser UI smoke: PAC FindProxyForURL conditional routing")
		case "shortcuts":
			features[i].Description = strings.Replace(features[i].Description, "Cmd/Ctrl+\\ Collapse Sidebar with a titlebar-style toggle button and non-persisted 0px/expanded rail state,", "Cmd/Ctrl+\\ Collapse Sidebar with a titlebar-style toggle button and non-persisted 0px/expanded rail state, Cmd/Ctrl+=, Cmd/Ctrl+-, and Cmd/Ctrl+0 zoom shortcuts with persisted display zoom, Cmd+Q/Ctrl+Shift+Q close app shortcut,", 1)
			features[i].Tests = append(features[i].Tests, "TestPreferencesDisplayZoomPersistAndNormalize", "Browser UI smoke: display zoom preferences and shortcuts", "Browser UI smoke: closeBruno shortcut quits Wails")
		}
	}
	return append(features, PostmanFeatures()...)
}

// postmanFeatures is the Postman-parity half of the ledger (US-059).
//
// Its own function rather than inlined into DefaultFeatures so the Bruno
// ledger it is appended to stays visibly intact — the story requires the Bruno
// ledger retained, and one enormous literal makes "did anything get dropped"
// unanswerable at a glance.
func PostmanFeatures() []types.Feature {
	return []types.Feature{
		postmanFeature("postman-scripts", "pm script API", "partial",
			"Live pm object beside bru: pm.test and pm.expect bound to the same registry the runner reads, pm.info (requestName, requestId, eventName, 0-based iteration, iterationCount), four distinct variable scopes (pm.environment, pm.collectionVariables, pm.globals, pm.variables over the resolved chain), pm.request and pm.response with Postman status semantics and the pm.response.to.have.* assertion chain, pm.sendRequest/pm.cookies/pm.execution delegating to bru, pm.iterationData over the runner data-file row, and async pm.vault reads. pm.vault writes are refused rather than stored, because this runtime cannot mark a value secret and a write would land in the environment in plain text. Postman's remaining sandbox globals and the full Chai surface remain gaps.",
			[]string{"TestPmTestFailuresReachTheRunner", "TestPmAndBruTestsShareOneRegistry", "TestPmInfoReportsTheRequest", "TestPmInfoEventNameInThePreRequestPhase", "TestPmInfoIterationCountsDuringACollectionRun", "TestPmDoesNotDisplaceBru", "TestPmVariableScopesAreDistinct", "TestPmScopesDelegateToBru", "TestPmVariablesReadsTheResolvedChain", "TestPmScopeMethodsAreAllPresent", "TestPmRequestReflectsTheRequest", "TestPmResponseUsesPostmanStatusSemantics", "TestPmResponseAssertionsPassWhenTrue", "TestPmResponseAssertionsFailWhenFalse", "TestPmResponseIsAbsentDuringThePreRequestPhase", "TestPmSideEffectsAreTheSameObjectsAsBru", "TestPmSendRequestPerformsARequest", "TestPmExecutionSetNextRequestDrivesTheRunner", "TestPmCookiesReadTheSameJarAsBru", "TestPmIterationDataReadsOnlyTheDataFile", "TestPmIterationDataIsEmptyWithoutADataFile", "TestPmVaultIsAsync", "TestPmVaultReadsTheSecretsLayer", "TestPmVaultWritesAreRejected", "TestPostmanEventName"}),

		postmanFeature("postman-translator", "Import script translation", "done",
			"pm.* to * rewriting on Postman import is opt-in and off by default, because pm.* is now native and more faithful than a textual rewrite. Its scope collapse is fixed: pm.environment, pm.collectionVariables and pm.globals map to their own bru functions instead of all four families landing on setVar. pm.variables is deliberately not translated, having no exact bru equivalent, and runs on the native object.",
			[]string{"TestPostmanImportKeepsPmByDefault", "TestPostmanTranslatorNoLongerCollapsesScopes", "TestTranslatedScopesReachDistinctStorage", "TestUntranslatedPostmanScriptsRun", "TestApplyCollectionImportHonoursTheTranslateFlag"}),

		postmanFeature("postman-runner", "Collection runner parity", "done",
			"Iterations with per-iteration result rows and Iterations vs CompletedIterations, CSV/JSON data files driving one iteration per row through a Data variable scope that beats the environment but loses to runtime and prompt values, and bail-on-failure marking unrun requests distinctly from skipped and cancelled. A failed assertion now fails the run result, which it did not before: the runner derived status only from the transport and HTTP code, so a suite whose every assertion failed reported green.",
			[]string{"TestRunnerIterationsRepeatEveryRequest", "TestSingleIterationRunsAreShapeCompatible", "TestRunnerBailStopsEveryIteration", "TestUnrunResultsAreNotCountedAsFailures", "TestNormalizeRunnerIterations", "TestRunnerDataRowsParsesCSV", "TestRunnerDataRowsParsesJSON", "TestRunnerDataRowsStripsTheUTF8BOM", "TestRunnerDataRowsToleratesRaggedAndBlankCSV", "TestRunnerIterationPlanClampsToTheRowCount", "TestDataFileRowsReachTheWire", "TestDataFileDoesNotOverrideRuntimeVariables", "TestNoDataFileLeavesTheDataScopeEmpty", "TestRunnerBailsAndMarksRemainingUnrun", "TestRunnerBailDistinguishesUnrunFromSkipped", "TestRunnerContinuesPastFailureByDefault", "TestFailedAssertionsFailTheRunResult", "TestFirstFailedTestResult"}),

		postmanFeature("postman-dynamic-variables", "Dynamic variables", "partial",
			"22 of Postman's dynamic variables resolve, each occurrence independently rather than once per request, with unknown $ names left literal so a typo travels to the wire visibly instead of resolving to empty. Postman's full faker set remains a gap.",
			[]string{"TestDynamicVariablesResolvePerOccurrence", "TestUnknownDynamicVariablesAreLeftLiteral", "TestOrdinaryVariablesAreUntouched", "TestDynamicVariableShapes", "TestEveryStoryNamedVariableResolves", "TestDynamicVariablesResolveThroughInterpolate", "TestUnknownDynamicVariablesSurviveInterpolate"}),

		postmanFeature("postman-import-export", "Import and export fidelity", "done",
			"HAR import with credential-header warnings and exact-duplicate dropping, Swagger 2 import by conversion to OpenAPI 3, and Postman export carrying collection variables, collection/folder/request auth, event blocks, path params and descriptions. Import to export to import is idempotent across repeated cycles.",
			[]string{"TestHARImportDropsOnlyExactDuplicates", "TestHARImportWarnsAboutCredentials", "TestHARImportKeepsCredentialHeaders", "TestHARImportStripsRecordingArtefacts", "TestHARImportKeepsRepeatedQueryKeys", "TestHARImportMapsBodyModes", "TestHARIsDetectedByShapeNotOnlyExtension", "TestSwagger2ConvertsVersionAndServers", "TestSwagger2RewritesEveryRef", "TestSwagger2BodyParameterBecomesRequestBody", "TestSwagger2FormDataBecomesRequestBody", "TestSwagger2ParameterTypesMoveIntoSchema", "TestSwagger2SecuritySchemesConvert", "TestSwagger2ResponseSchemasMoveUnderContent", "TestSwagger2IsDetectedAndImported", "TestPostmanRoundTripIsIdempotent", "TestPostmanExportCarriesEventBlocks", "TestPostmanExportCarriesPathParams", "TestPostmanExportPreservesDisabledParams", "TestPostmanExportRoundTripsGraphQL", "TestPostmanExportRoundTripsAuthAtEveryLevel", "TestPostmanExportRoundTripsDescription", "TestPostmanExportWritesExecAsLines"}),

		postmanFeature("postman-codegen", "Code generation targets", "done",
			"Python requests, Node axios, Go net/http, Java java.net.http, C# HttpClient, PHP cURL, Ruby net/http, HTTPie and PowerShell alongside curl and fetch, all from one normalised request view, with per-language quoting so a body containing quotes, backslashes, $ or #{ cannot break out of its literal or be interpolated.",
			[]string{"TestEveryStoryNamedTargetGenerates", "TestCodegenTargetsAreDispatchable", "TestCodegenEscapesHostileBodies", "TestCodegenRespectsDisabledRows", "TestCodegenHandlesEveryBodyMode", "TestMultipartOmitsAHandWrittenContentType", "TestGeneratedJSONIsStillValidJSON", "TestUnknownCodegenLanguageIsRejected"}),

		postmanFeature("postman-workbench", "Command palette, bulk edit and shortcut preset", "done",
			"Cmd/Ctrl+Shift+P command palette registered in the customizable keybinding system and ranked exact > prefix > word-start > substring > subsequence, kept distinct from the Cmd/Ctrl+K object search; bulk text editing on request headers and params that round-trips disabled state, secret and description; and a selectable Postman keybinding preset layered between the defaults and the user's own overrides, with collisions still rejected.",
			[]string{"TestKeyBindingPresetNormalizes", "TestKeyBindingPresetPersists", "npm test: commandPalette", "npm test: bulkEdit", "npm test: keybindings"}),

		postmanFeature("postman-visualizer", "Response visualizer", "partial",
			"pm.visualizer.set(template, data) and a Visualizer response tab rendering in an iframe sandboxed without allow-same-origin, under a default-src 'none' CSP, with interpolation HTML-escaped. The template engine is a bounded Handlebars subset; unrecognised helpers are left visible rather than dropped. Full Handlebars helper coverage remains a gap.",
			[]string{"TestVisualizerSandboxNeverAllowsSameOrigin", "TestVisualizerDocumentCarriesAStrictCSP", "TestVisualizerEscapesResponseData", "TestVisualizerTripleBraceIsDeliberatelyRaw", "TestVisualizerRendersTemplateSubset", "TestVisualizerLeavesUnsupportedHelpersVisible", "TestVisualizerHandlesNestedBlocks", "TestVisualizerHandlesMalformedInput", "TestVisualizerRejectsOversizedPayloads", "TestPmVisualizerSetReachesTheResponse", "TestVisualizerSetInThePreRequestPhaseSurvives", "TestLaterPhaseWinsForVisualizer", "TestFrontendSandboxMatchesTheGoConstant"}),
	}
}

func feature(id, name, category, status, description string, tests []string) types.Feature {
	return types.Feature{
		ID:          id,
		Name:        name,
		Category:    category,
		Status:      status,
		Description: description,
		Tests:       tests,
		SourceRefs: []string{
			"/Users/mou/Developer/Workspace/bruno/tests",
			"/Users/mou/Developer/Workspace/bruno/packages/bruno-app/src/components",
			"/Users/mou/Developer/Workspace/bruno/packages/bruno-electron/src/ipc",
		},
	}
}

// postmanFeature is feature() with Postman's documentation as the reference
// instead of Bruno's source tree.
//
// A separate constructor rather than a parameter on feature(): the Bruno rows
// cite a checked-out repository that can be read, the Postman rows cite public
// documentation that cannot, and conflating the two would let a Postman row
// claim evidence from a tree that says nothing about Postman. The Bruno ledger
// is untouched (US-059 requires it retained); these rows sit beside it.
func postmanFeature(id, name, status, description string, tests []string) types.Feature {
	return types.Feature{
		ID:          id,
		Name:        name,
		Category:    "Postman parity",
		Status:      status,
		Description: description,
		Tests:       tests,
		SourceRefs: []string{
			"https://learning.postman.com/docs/tests-and-scripts/write-scripts/postman-sandbox-api-reference/",
			"https://learning.postman.com/docs/tests-and-scripts/write-scripts/variables-list/",
			"https://learning.postman.com/docs/getting-started/installation/settings/shortcut-settings",
			"POSTMAN-PARITY.md",
		},
	}
}

func SanitizeName(name string) string {
	name = brunoInvalidFilenameCharacterPattern.ReplaceAllString(name, "-")
	name = brunoLeadingFilenameTrimPattern.ReplaceAllString(name, "")
	name = brunoTrailingFilenameTrimPattern.ReplaceAllString(name, "")
	return name
}

func ValidateName(name string) bool {
	if name == "" {
		return false
	}
	if len([]rune(name)) > 255 {
		return false
	}
	if brunoReservedDeviceNamePattern.MatchString(name) {
		return false
	}
	return brunoFilenameFirstCharacterPattern.MatchString(name) &&
		brunoFilenameMiddleCharactersPattern.MatchString(name) &&
		brunoFilenameLastCharacterPattern.MatchString(name)
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
var brunoInvalidFilenameCharacterPattern = regexp.MustCompile(`[<>:"/\\|?*\x00-\x1F]`)
var invalidPostmanVariableCharacterPattern = regexp.MustCompile(`[^A-Za-z0-9_.-]`)

var brunoFilenameFirstCharacterPattern = regexp.MustCompile(`^[^\s\-<>:"/\\|?*\x00-\x1F]`)
var brunoFilenameLastCharacterPattern = regexp.MustCompile(`[^.\s<>:"/\\|?*\x00-\x1F]$`)
var brunoFilenameMiddleCharactersPattern = regexp.MustCompile(`^[^<>:"/\\|?*\x00-\x1F]*$`)
var brunoLeadingFilenameTrimPattern = regexp.MustCompile(`^[\s-]+`)

var brunoReservedDeviceNamePattern = regexp.MustCompile(`(?i)^(CON|PRN|AUX|NUL|COM[0-9]|LPT[0-9])$`)
var brunoTrailingFilenameTrimPattern = regexp.MustCompile(`[.\s]+$`)

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

func YAMLVariables(values []types.Variable) []map[string]interface{} {
	result := make([]map[string]interface{}, 0, len(values))
	for _, value := range values {
		entry := map[string]interface{}{
			"name":    value.Name,
			"value":   yamlVariableValue(value),
			"enabled": value.Enabled,
		}
		if value.Secret {
			entry["secret"] = true
		}
		result = append(result, entry)
	}
	return result
}

func yamlVariableValue(value types.Variable) interface{} {
	dataType := value.DataType
	if dataType == "" {
		dataType = value.Type
	}
	switch dataType {
	case "number", "boolean", "object":
		return map[string]interface{}{"type": dataType, "data": fmt.Sprint(value.Value)}
	default:
		return value.Value
	}
}
func WsMessagesForStorage(item types.RequestItem) []types.WSMessage {
	if len(item.WSMessages) > 0 {
		return item.WSMessages
	}
	if content := wsexec.MessageBody(item.Body, nil); strings.TrimSpace(content) != "" {
		return []types.WSMessage{{Type: wsexec.NormalizeMessageType(item.Body.Mode), Content: content}}
	}
	return nil
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
func BrunoEnvironmentExportFiles(environments []types.Environment) ([]types.GlobalEnvironmentExportFile, error) {
	files := make([]types.GlobalEnvironmentExportFile, 0, len(environments))
	used := map[string]bool{}
	for _, env := range environments {
		baseName := BrunoEnvironmentExportFileName(env.Name)
		fileBase := uniqueExportFileBaseName(baseName, used)
		content, err := StringifyBrunoEnvironmentExport(env)
		if err != nil {
			return nil, err
		}
		files = append(files, types.GlobalEnvironmentExportFile{Name: fileBase + ".json", Content: content})
	}
	return files, nil
}
