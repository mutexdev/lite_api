// Writing .bru documents, and importing/exporting environments.
//
// US-070. The write side lives with the read side deliberately: a format has
// exactly one definition of what a block looks like, and splitting the reader
// from the writer is how the two drift until a file this app wrote can no
// longer be read back by it.
package bru

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/mutexdev/lite_api/internal/scalar"
	"github.com/mutexdev/lite_api/internal/types"
	"github.com/mutexdev/lite_api/internal/wsmessage"
)

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
		fmt.Fprintf(b, "  type: %s\n", wsmessage.NormalizeMessageType(message.Type))
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
	if content := wsmessage.MessageBody(item.Body, nil); strings.TrimSpace(content) != "" {
		return []types.WSMessage{{Type: wsmessage.NormalizeMessageType(item.Body.Mode), Content: content}}
	}
	return nil
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
