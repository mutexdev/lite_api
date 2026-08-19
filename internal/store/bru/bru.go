// Package bru reads Bruno's .bru format: the block structure, its scalar and
// key/value maps, and the request, environment and collection documents built
// from them.
//
// US-070. Reading a foreign on-disk format is exactly the kind of thing worth
// isolating -- it has no dependency on the app's state, and every bug in it
// shows up as a request that silently loads wrong rather than as an error.
package bru

import (
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/mutexdev/lite_api/internal/auth/oauth1"
	"github.com/mutexdev/lite_api/internal/codegen"
	"github.com/mutexdev/lite_api/internal/scalar"
	"github.com/mutexdev/lite_api/internal/types"
	"github.com/mutexdev/lite_api/internal/wsmessage"
)

func ParseSections(content string) map[string][]string {
	sections := map[string][]string{}
	active := ""
	closeToken := ""
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if active != "" {
			if isBruSectionClose(line, closeToken) {
				active = ""
				closeToken = ""
				continue
			}
			sections[active] = append(sections[active], line)
			continue
		}
		if name, close, ok := bruSectionOpen(line); ok {
			active = name
			closeToken = close
			if _, ok := sections[active]; !ok {
				sections[active] = []string{}
			}
			continue
		}
		if trimmed == "" {
			continue
		}
	}
	return sections
}

func parseBruNamedBlocks(content, target string) [][]string {
	blocks := [][]string{}
	active := false
	closeToken := ""
	current := []string{}
	for _, line := range strings.Split(content, "\n") {
		if active {
			if isBruSectionClose(line, closeToken) {
				blocks = append(blocks, current)
				active = false
				closeToken = ""
				current = []string{}
				continue
			}
			current = append(current, line)
			continue
		}
		if name, close, ok := bruSectionOpen(line); ok && name == target {
			active = true
			closeToken = close
			current = []string{}
		}
	}
	return blocks
}

func bruSectionOpen(line string) (string, string, bool) {
	if strings.TrimLeft(line, " \t") != line {
		return "", "", false
	}
	trimmed := strings.TrimSpace(line)
	if strings.HasSuffix(trimmed, "{") {
		return strings.TrimSpace(strings.TrimSuffix(trimmed, "{")), "}", true
	}
	if strings.HasSuffix(trimmed, "[") {
		return strings.TrimSpace(strings.TrimSuffix(trimmed, "[")), "]", true
	}
	return "", "", false
}

func isBruSectionClose(line, closeToken string) bool {
	return strings.TrimLeft(line, " \t") == line && strings.TrimSpace(line) == closeToken
}

func OAuth1PrivateKeyValue(auth types.OAuth1Auth) string {
	if auth.PrivateKeyType == "file" && auth.PrivateKey != "" {
		return "@file(" + auth.PrivateKey + ")"
	}
	return auth.PrivateKey
}

func ParseScalarMap(lines []string) map[string]string {
	values := map[string]string{}
	for i := 0; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" || strings.HasPrefix(trimmed, "@") {
			continue
		}
		key, value, ok := strings.Cut(trimmed, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if value == "'''" {
			collected := []string{}
			for i++; i < len(lines); i++ {
				if strings.TrimSpace(lines[i]) == "'''" {
					break
				}
				collected = append(collected, strings.TrimPrefix(lines[i], "    "))
			}
			value = strings.TrimRight(strings.Join(collected, "\n"), "\n")
		}
		values[key] = strings.TrimSuffix(value, ",")
	}
	return values
}

func parseBruScalarMapDedented(lines []string) map[string]string {
	values := map[string]string{}
	for i := 0; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" || strings.HasPrefix(trimmed, "@") || strings.HasSuffix(trimmed, "{") {
			continue
		}
		key, value, ok := strings.Cut(trimmed, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if value == "'''" {
			collected := []string{}
			for i++; i < len(lines); i++ {
				if strings.TrimSpace(lines[i]) == "'''" {
					break
				}
				collected = append(collected, lines[i])
			}
			value = strings.TrimRight(dedentBruLines(collected), "\n")
		}
		values[key] = strings.TrimSuffix(value, ",")
	}
	return values
}

func dedentBruLines(lines []string) string {
	minIndent := -1
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		indent := leadingWhitespace(line)
		if minIndent < 0 || indent < minIndent {
			minIndent = indent
		}
	}
	if minIndent < 0 {
		minIndent = 0
	}
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if len(line) >= minIndent {
			out = append(out, line[minIndent:])
		} else {
			out = append(out, strings.TrimLeft(line, " \t"))
		}
	}
	return strings.Join(out, "\n")
}

func leadingWhitespace(line string) int {
	count := 0
	for count < len(line) && (line[count] == ' ' || line[count] == '\t') {
		count++
	}
	return count
}

func collectBruColonBlock(lines []string, name string) []string {
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed != name+": {" && trimmed != name+" {" {
			continue
		}
		indent := leadingWhitespace(line)
		collected := []string{}
		for j := i + 1; j < len(lines); j++ {
			if leadingWhitespace(lines[j]) == indent && strings.TrimSpace(lines[j]) == "}" {
				return collected
			}
			collected = append(collected, lines[j])
		}
		return collected
	}
	return nil
}

func parseBruKeyValues(lines []string) []types.KeyValue {
	result := []types.KeyValue{}
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "@") {
			continue
		}
		key, value, ok := strings.Cut(trimmed, ":")
		if !ok {
			continue
		}
		name := strings.TrimSpace(key)
		enabled := true
		if strings.HasPrefix(name, "~") {
			enabled = false
			name = strings.TrimPrefix(name, "~")
		}
		if name == "" {
			continue
		}
		result = append(result, types.KeyValue{Name: name, Value: strings.TrimSpace(value), Enabled: enabled})
	}
	return result
}

func parseBruMultipartValues(lines []string) []types.FormPart {
	result := []types.FormPart{}
	filePattern := regexp.MustCompile(`@file\(([^)]*)\)`)
	contentTypePattern := regexp.MustCompile(`@contentType\(([^)]*)\)`)
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "@") {
			continue
		}
		key, value, ok := strings.Cut(trimmed, ":")
		if !ok {
			continue
		}
		name := strings.TrimSpace(key)
		enabled := true
		if strings.HasPrefix(name, "~") {
			enabled = false
			name = strings.TrimPrefix(name, "~")
		}
		if name == "" {
			continue
		}
		value = strings.TrimSpace(value)
		part := types.FormPart{Name: name, Enabled: enabled}
		if match := contentTypePattern.FindStringSubmatch(value); len(match) == 2 {
			part.ContentType = strings.TrimSpace(match[1])
			value = strings.TrimSpace(contentTypePattern.ReplaceAllString(value, ""))
		}
		if match := filePattern.FindStringSubmatch(value); len(match) == 2 {
			part.FilePath = strings.TrimSpace(match[1])
		} else {
			part.Value = strings.TrimSpace(value)
		}
		result = append(result, part)
	}
	return result
}

func parseBruFileBody(lines []string) []types.FileBodyEntry {
	parts := parseBruMultipartValues(lines)
	result := make([]types.FileBodyEntry, 0, len(parts))
	for i := range parts {
		if strings.TrimSpace(parts[i].FilePath) == "" && strings.TrimSpace(parts[i].ContentType) == "" {
			continue
		}
		result = append(result, types.FileBodyEntry{
			FilePath:    parts[i].FilePath,
			ContentType: parts[i].ContentType,
			Selected:    parts[i].Enabled,
		})
	}
	return result
}

func parseBruGrpcMessages(content string) []types.GrpcMessage {
	blocks := parseBruNamedBlocks(content, "body:grpc")
	if len(blocks) == 0 {
		return nil
	}
	result := make([]types.GrpcMessage, 0, len(blocks))
	for index, block := range blocks {
		values := ParseScalarMap(block)
		name := strings.TrimSpace(scalar.FirstNonEmpty(values["name"], values["title"]))
		messageContent := values["content"]
		if strings.TrimSpace(name) == "" && strings.TrimSpace(messageContent) == "" {
			continue
		}
		if strings.TrimSpace(name) == "" {
			name = fmt.Sprintf("message %d", index+1)
		}
		result = append(result, types.GrpcMessage{Name: name, Content: messageContent})
	}
	return result
}

func parseBruWSMessages(content string) []types.WSMessage {
	blocks := parseBruNamedBlocks(content, "body:ws")
	if len(blocks) == 0 {
		return nil
	}
	result := make([]types.WSMessage, 0, len(blocks))
	for index, block := range blocks {
		values := ParseScalarMap(block)
		name := strings.TrimSpace(scalar.FirstNonEmpty(values["name"], values["title"]))
		messageContent := scalar.FirstNonEmpty(values["content"], values["data"])
		if strings.TrimSpace(name) == "" && strings.TrimSpace(messageContent) == "" {
			continue
		}
		if strings.TrimSpace(name) == "" && len(blocks) > 1 {
			name = fmt.Sprintf("message %d", index+1)
		}
		result = append(result, types.WSMessage{
			Name:     name,
			Type:     wsmessage.NormalizeMessageType(values["type"]),
			Content:  messageContent,
			Selected: strings.EqualFold(values["selected"], "true"),
		})
	}
	return result
}

func oauth2AdditionalParamsFromKeyValues(rows []types.KeyValue, sendIn string) []types.OAuth2AdditionalParam {
	result := make([]types.OAuth2AdditionalParam, 0, len(rows))
	for _, row := range rows {
		result = append(result, types.OAuth2AdditionalParam{
			Name:        row.Name,
			Value:       row.Value,
			SendIn:      types.NormalizeOAuth2AdditionalPlacement(sendIn),
			Enabled:     row.Enabled,
			Secret:      row.Secret,
			Description: row.Description,
		})
	}
	return result
}

func OAuth2KeyValuesFromAdditionalParams(params []types.OAuth2AdditionalParam, sendIn string) []types.KeyValue {
	sendIn = types.NormalizeOAuth2AdditionalPlacement(sendIn)
	result := []types.KeyValue{}
	for _, param := range params {
		if types.NormalizeOAuth2AdditionalPlacement(param.SendIn) != sendIn || strings.TrimSpace(param.Name) == "" {
			continue
		}
		result = append(result, types.KeyValue{
			Name:        param.Name,
			Value:       param.Value,
			Enabled:     param.Enabled,
			Secret:      param.Secret,
			Description: param.Description,
		})
	}
	return result
}

func ParseVariables(lines []string, secret bool) []types.Variable {
	variables := []types.Variable{}
	currentType := "string"
	for i := 0; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "@") && !strings.Contains(trimmed, ":") {
			currentType = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(trimmed, "@")))
			if currentType == "" {
				currentType = "string"
			}
			continue
		}
		if secret {
			name := strings.Trim(strings.TrimSuffix(trimmed, ","), " \t")
			if name == "" {
				continue
			}
			variables = append(variables, types.Variable{ID: scalar.NewID("var"), Name: name, Value: "", DataType: currentType, Type: currentType, Enabled: true, Secret: true})
			currentType = "string"
			continue
		}
		key, value, ok := strings.Cut(trimmed, ":")
		if !ok {
			continue
		}
		name := strings.TrimSpace(key)
		enabled := true
		if strings.HasPrefix(name, "~") {
			enabled = false
			name = strings.TrimPrefix(name, "~")
		}
		name = types.ResponseVariableRuntimeName(name)
		value = strings.TrimSpace(value)
		if value == "'''" {
			collected := []string{}
			for i++; i < len(lines); i++ {
				if strings.TrimSpace(lines[i]) == "'''" {
					break
				}
				collected = append(collected, strings.TrimPrefix(lines[i], "    "))
			}
			value = strings.TrimRight(strings.Join(collected, "\n"), "\n")
		}
		parsed, dataType := parseBruTypedScalar(value, currentType)
		variables = append(variables, types.Variable{ID: scalar.NewID("var"), Name: name, Value: parsed, DataType: dataType, Type: dataType, Enabled: enabled, Secret: false})
		currentType = "string"
	}
	return variables
}

func parseBruTypedScalar(value, dataType string) (interface{}, string) {
	switch dataType {
	case "number":
		if strings.ContainsAny(value, ".eE") {
			if parsed, err := strconv.ParseFloat(value, 64); err == nil {
				return parsed, "number"
			}
		}
		if parsed, err := strconv.Atoi(value); err == nil {
			return parsed, "number"
		}
		return value, "number"
	case "boolean":
		if parsed, err := strconv.ParseBool(value); err == nil {
			return parsed, "boolean"
		}
		return value, "boolean"
	case "object":
		return value, "object"
	default:
		return value, "string"
	}
}

func normalizeBruRequestType(value string) string {
	switch strings.ToLower(value) {
	case "ws", "websocket":
		return "websocket"
	case "graphql":
		return "graphql"
	case "grpc":
		return "grpc"
	default:
		return "http"
	}
}

func NormalizeBodyMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "graphql":
		return "graphql"
	case "ws":
		return "text"
	default:
		return codegen.NormalizedBodyMode(value)
	}
}

func applyBruAuthSections(auth *types.AuthConfig, sections map[string][]string) {
	if lines, ok := sections["auth"]; ok {
		values := ParseScalarMap(lines)
		if mode := strings.TrimSpace(values["mode"]); mode != "" {
			auth.Mode = mode
		}
	}
	if auth.APILocation == "" {
		auth.APILocation = "header"
	}
	if lines, ok := sections["auth:basic"]; ok {
		values := ParseScalarMap(lines)
		auth.Mode = "basic"
		auth.Username = values["username"]
		auth.Password = values["password"]
	}
	if lines, ok := sections["auth:digest"]; ok {
		values := ParseScalarMap(lines)
		auth.Mode = "digest"
		auth.Username = values["username"]
		auth.Password = values["password"]
	}
	if lines, ok := sections["auth:wsse"]; ok {
		values := ParseScalarMap(lines)
		auth.Mode = "wsse"
		auth.Username = values["username"]
		auth.Password = values["password"]
	}
	if lines, ok := sections["auth:ntlm"]; ok {
		values := ParseScalarMap(lines)
		auth.Mode = "ntlm"
		auth.Username = values["username"]
		auth.Password = values["password"]
		auth.Domain = values["domain"]
	}
	if lines, ok := sections["auth:bearer"]; ok {
		values := ParseScalarMap(lines)
		auth.Mode = "bearer"
		auth.Token = scalar.FirstNonEmpty(values["token"], values["accessToken"])
	}
	if lines, ok := sections["auth:apikey"]; ok {
		values := ParseScalarMap(lines)
		auth.Mode = "apikey"
		auth.APIKey = scalar.FirstNonEmpty(values["key"], values["name"])
		auth.APIValue = values["value"]
		auth.APILocation = scalar.FirstNonEmpty(strings.ToLower(values["placement"]), strings.ToLower(values["location"]), "header")
	}
	if lines, ok := sections["auth:awsv4"]; ok {
		values := ParseScalarMap(lines)
		auth.Mode = "awsv4"
		auth.AWSV4.AccessKeyID = scalar.FirstNonEmpty(values["accessKeyId"], values["accessKey"])
		auth.AWSV4.SecretAccessKey = scalar.FirstNonEmpty(values["secretAccessKey"], values["secretKey"])
		auth.AWSV4.SessionToken = values["sessionToken"]
		auth.AWSV4.Service = values["service"]
		auth.AWSV4.Region = values["region"]
		auth.AWSV4.ProfileName = values["profileName"]
	}
	if lines, ok := sections["auth:oauth1"]; ok {
		values := ParseScalarMap(lines)
		auth.Mode = "oauth1"
		auth.OAuth1.ConsumerKey = values["consumer_key"]
		auth.OAuth1.ConsumerSecret = values["consumer_secret"]
		auth.OAuth1.AccessToken = values["access_token"]
		auth.OAuth1.AccessTokenSecret = values["token_secret"]
		auth.OAuth1.CallbackURL = values["callback_url"]
		auth.OAuth1.Verifier = values["verifier"]
		auth.OAuth1.SignatureMethod = values["signature_method"]
		auth.OAuth1.PrivateKey, auth.OAuth1.PrivateKeyType = oauth1.ParsePrivateKeyValue(values["private_key"])
		auth.OAuth1.Timestamp = values["timestamp"]
		auth.OAuth1.Nonce = values["nonce"]
		auth.OAuth1.Version = values["version"]
		auth.OAuth1.Realm = values["realm"]
		auth.OAuth1.Placement = values["placement"]
		auth.OAuth1.IncludeBodyHash = strings.EqualFold(values["include_body_hash"], "true")
	}
	if lines, ok := sections["auth:oauth2"]; ok {
		values := ParseScalarMap(lines)
		auth.Mode = "oauth2"
		auth.Token = scalar.FirstNonEmpty(values["accessToken"], values["access_token"], values["token"])
		auth.OAuth2.GrantType = scalar.FirstNonEmpty(values["grant_type"], values["grantType"])
		auth.OAuth2.CallbackURL = scalar.FirstNonEmpty(values["callback_url"], values["callbackUrl"])
		auth.OAuth2.AuthorizationURL = scalar.FirstNonEmpty(values["authorization_url"], values["authorizationUrl"])
		auth.OAuth2.AccessTokenURL = scalar.FirstNonEmpty(values["access_token_url"], values["accessTokenUrl"])
		auth.OAuth2.RefreshTokenURL = scalar.FirstNonEmpty(values["refresh_token_url"], values["refreshTokenUrl"])
		auth.OAuth2.Username = values["username"]
		auth.OAuth2.Password = values["password"]
		auth.OAuth2.ClientID = scalar.FirstNonEmpty(values["client_id"], values["clientId"])
		auth.OAuth2.ClientSecret = scalar.FirstNonEmpty(values["client_secret"], values["clientSecret"])
		auth.OAuth2.Scope = values["scope"]
		auth.OAuth2.State = values["state"]
		auth.OAuth2.PKCE = strings.EqualFold(values["pkce"], "true")
		auth.OAuth2.CredentialsPlacement = scalar.FirstNonEmpty(values["credentials_placement"], values["credentialsPlacement"])
		auth.OAuth2.CredentialsID = scalar.FirstNonEmpty(values["credentials_id"], values["credentialsId"])
		auth.OAuth2.TokenSource = scalar.FirstNonEmpty(values["token_source"], values["tokenSource"])
		auth.OAuth2.TokenPlacement = scalar.FirstNonEmpty(values["token_placement"], values["tokenPlacement"])
		auth.OAuth2.TokenHeaderPrefix = scalar.FirstNonEmpty(values["token_header_prefix"], values["tokenHeaderPrefix"])
		auth.OAuth2.TokenQueryKey = scalar.FirstNonEmpty(values["token_query_key"], values["tokenQueryKey"])
		auth.OAuth2.AutoFetchToken = strings.EqualFold(scalar.FirstNonEmpty(values["auto_fetch_token"], values["autoFetchToken"]), "true")
		auth.OAuth2.AutoRefreshToken = strings.EqualFold(scalar.FirstNonEmpty(values["auto_refresh_token"], values["autoRefreshToken"]), "true")
	}
	auth.OAuth2.AuthorizationAdditionalParams = append(
		oauth2AdditionalParamsFromKeyValues(parseBruKeyValues(sections["auth:oauth2:additional_params:auth_req:headers"]), "headers"),
		oauth2AdditionalParamsFromKeyValues(parseBruKeyValues(sections["auth:oauth2:additional_params:auth_req:queryparams"]), "queryparams")...,
	)
	auth.OAuth2.TokenAdditionalParams = append(
		oauth2AdditionalParamsFromKeyValues(parseBruKeyValues(sections["auth:oauth2:additional_params:access_token_req:headers"]), "headers"),
		oauth2AdditionalParamsFromKeyValues(parseBruKeyValues(sections["auth:oauth2:additional_params:access_token_req:queryparams"]), "queryparams")...,
	)
	auth.OAuth2.TokenAdditionalParams = append(
		auth.OAuth2.TokenAdditionalParams,
		oauth2AdditionalParamsFromKeyValues(parseBruKeyValues(sections["auth:oauth2:additional_params:access_token_req:body"]), "body")...,
	)
	auth.OAuth2.RefreshAdditionalParams = append(
		oauth2AdditionalParamsFromKeyValues(parseBruKeyValues(sections["auth:oauth2:additional_params:refresh_token_req:headers"]), "headers"),
		oauth2AdditionalParamsFromKeyValues(parseBruKeyValues(sections["auth:oauth2:additional_params:refresh_token_req:queryparams"]), "queryparams")...,
	)
	auth.OAuth2.RefreshAdditionalParams = append(
		auth.OAuth2.RefreshAdditionalParams,
		oauth2AdditionalParamsFromKeyValues(parseBruKeyValues(sections["auth:oauth2:additional_params:refresh_token_req:body"]), "body")...,
	)
}

func parseBruAssertions(lines []string) []types.Assertion {
	assertions := []types.Assertion{}
	for _, line := range lines {
		key, value, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok {
			continue
		}
		parts := strings.Fields(strings.TrimSpace(value))
		if len(parts) < 2 {
			continue
		}
		assertions = append(assertions, types.Assertion{Expression: strings.TrimSpace(key), Operator: bruAssertionOperator(parts[0]), Value: strings.Join(parts[1:], " "), Enabled: true})
	}
	return assertions
}

func bruAssertionOperator(value string) string {
	switch value {
	case "eq":
		return "equals"
	case "neq":
		return "notEquals"
	default:
		return value
	}
}

func applyBruSettings(item *types.RequestItem, values map[string]string) {
	if encodeURL, err := strconv.ParseBool(values["encodeUrl"]); err == nil {
		item.Settings.EncodeURL = encodeURL
	}
	if timeout, err := strconv.Atoi(values["timeout"]); err == nil {
		item.Settings.TimeoutMs = timeout
	}
	if follow, err := strconv.ParseBool(values["followRedirects"]); err == nil {
		item.Settings.FollowRedirects = follow
	}
	if maxRedirects, err := strconv.Atoi(values["maxRedirects"]); err == nil {
		item.Settings.MaxRedirects = maxRedirects
	}
	if storeCookies, err := strconv.ParseBool(values["storeCookies"]); err == nil {
		item.Settings.StoreCookies = storeCookies
	}
	if verifyTLS, err := strconv.ParseBool(values["verifyTls"]); err == nil {
		item.Settings.VerifyTLS = verifyTLS
	}
	if keepAliveInterval, err := strconv.Atoi(values["keepAliveInterval"]); err == nil {
		item.Settings.KeepAliveInterval = keepAliveInterval
	}
}

func joinBruLines(lines []string) string {
	return strings.Join(lines, "\n")
}

func parseBruExamples(content string, item types.RequestItem) []types.ResponseExample {
	blocks := parseBruNamedBlocks(content, "example")
	examples := make([]types.ResponseExample, 0, len(blocks))
	for index, block := range blocks {
		examples = append(examples, parseBruExampleBlock(block, item, index))
	}
	return examples
}

func parseBruExampleBlock(lines []string, item types.RequestItem, index int) types.ResponseExample {
	values := parseBruScalarMapDedented(lines)
	name := strings.TrimSpace(values["name"])
	if name == "" {
		name = fmt.Sprintf("Example %d", index+1)
	}
	example := types.ResponseExample{
		ID:          scalar.DeterministicID("example", scalar.FirstNonEmpty(item.FilePath, item.ID, item.Name)+"#example#"+strconv.Itoa(index)),
		Name:        name,
		Description: values["description"],
		Type:        scalar.FirstNonEmpty(item.Type, "http"),
		Request: types.ResponseExampleRequest{
			Method:   strings.ToUpper(scalar.FirstNonEmpty(item.Method, http.MethodGet)),
			URL:      item.URL,
			BodyMode: "none",
		},
		Response: types.ResponseExamplePayload{
			Status:     http.StatusOK,
			StatusText: http.StatusText(http.StatusOK),
			BodyType:   "text",
		},
	}
	if requestLines := collectBruColonBlock(lines, "request"); len(requestLines) > 0 {
		applyBruExampleRequest(&example, requestLines)
	}
	if responseLines := collectBruColonBlock(lines, "response"); len(responseLines) > 0 {
		applyBruExampleResponse(&example, responseLines)
	}
	return example
}

func applyBruExampleRequest(example *types.ResponseExample, lines []string) {
	values := parseBruScalarMapDedented(lines)
	if method := strings.TrimSpace(values["method"]); method != "" {
		example.Request.Method = strings.ToUpper(method)
	}
	if targetURL := strings.TrimSpace(values["url"]); targetURL != "" {
		example.Request.URL = targetURL
	}
	if mode := NormalizeBodyMode(scalar.FirstNonEmpty(values["mode"], values["body"])); mode != "" {
		example.Request.BodyMode = mode
	}
	if headers := collectBruColonBlock(lines, "headers"); len(headers) > 0 {
		example.Request.Headers = parseBruKeyValues(headers)
	}
	if params := collectBruColonBlock(lines, "params"); len(params) > 0 {
		example.Request.Params = parseBruKeyValues(params)
	}
	if body := collectBruColonBlock(lines, "body"); len(body) > 0 {
		bodyValues := parseBruScalarMapDedented(body)
		example.Request.BodyMode = NormalizeBodyMode(scalar.FirstNonEmpty(bodyValues["type"], example.Request.BodyMode))
		example.Request.Body = bodyValues["content"]
	}
	if body := collectBruColonBlock(lines, "body:form-urlencoded"); len(body) > 0 {
		example.Request.BodyMode = "formUrlEncoded"
		example.Request.FormURLEncoded = parseBruKeyValues(body)
	}
	if body := collectBruColonBlock(lines, "body:multipart-form"); len(body) > 0 {
		example.Request.BodyMode = "multipartForm"
		example.Request.MultipartForm = parseBruMultipartValues(body)
	}
	if body := collectBruColonBlock(lines, "body:file"); len(body) > 0 {
		example.Request.BodyMode = "file"
		example.Request.File = parseBruFileBody(body)
	}
}

func applyBruExampleResponse(example *types.ResponseExample, lines []string) {
	values := parseBruScalarMapDedented(lines)
	if duration, err := strconv.ParseInt(scalar.FirstNonEmpty(values["duration"], values["durationMs"]), 10, 64); err == nil && duration >= 0 {
		example.Response.DurationMs = duration
	}
	if statusText := strings.TrimSpace(values["statusText"]); statusText != "" {
		example.Response.StatusText = statusText
	}
	if statusLines := collectBruColonBlock(lines, "status"); len(statusLines) > 0 {
		statusValues := parseBruScalarMapDedented(statusLines)
		if status, err := strconv.Atoi(scalar.FirstNonEmpty(statusValues["code"], statusValues["status"])); err == nil {
			example.Response.Status = status
		}
		if statusText := strings.TrimSpace(scalar.FirstNonEmpty(statusValues["text"], statusValues["statusText"])); statusText != "" {
			example.Response.StatusText = scalar.CleanStatusText(example.Response.Status, statusText)
		}
	}
	if example.Response.StatusText == "" {
		example.Response.StatusText = http.StatusText(example.Response.Status)
	}
	if headers := collectBruColonBlock(lines, "headers"); len(headers) > 0 {
		example.Response.Headers = parseBruKeyValues(headers)
	}
	if body := collectBruColonBlock(lines, "body"); len(body) > 0 {
		bodyValues := parseBruScalarMapDedented(body)
		example.Response.BodyType = scalar.FirstNonEmpty(bodyValues["type"], "text")
		example.Response.Body = bodyValues["content"]
		example.Response.Size = len([]byte(example.Response.Body))
	}
}

func Parse(content string) (types.RequestItem, error) {
	sections := ParseSections(content)
	item := types.NewRequestItem("Imported request", "http", 1)
	if meta, ok := sections["meta"]; ok {
		values := ParseScalarMap(meta)
		if name := strings.TrimSpace(values["name"]); name != "" {
			item.Name = name
		}
		if requestType := strings.ToLower(strings.TrimSpace(values["type"])); requestType != "" {
			item.Type = normalizeBruRequestType(requestType)
		}
		if seq, err := strconv.Atoi(values["seq"]); err == nil && seq > 0 {
			item.Seq = seq
		}
	}
	if item.Type == "graphql" {
		item.Body.Mode = "graphql"
	}
	if item.Type == "websocket" {
		item.Method = "CONNECT"
		item.Body.Mode = "ws"
	}
	if item.Type == "grpc" {
		item.Method = "CALL"
		item.Body.Mode = "grpc"
		item.URL = "grpc://localhost:50051"
	}
	for _, method := range []string{"get", "post", "put", "patch", "delete", "head", "options", "trace"} {
		lines, ok := sections[method]
		if !ok {
			continue
		}
		values := ParseScalarMap(lines)
		item.Method = strings.ToUpper(method)
		if targetURL := strings.TrimSpace(values["url"]); targetURL != "" {
			item.URL = targetURL
		}
		if mode := strings.TrimSpace(values["body"]); mode != "" {
			item.Body.Mode = NormalizeBodyMode(mode)
		}
		if auth := strings.TrimSpace(values["auth"]); auth != "" {
			item.Auth.Mode = auth
		}
	}
	if lines, ok := sections["ws"]; ok {
		values := ParseScalarMap(lines)
		item.Type = "websocket"
		item.Method = "CONNECT"
		if targetURL := strings.TrimSpace(values["url"]); targetURL != "" {
			item.URL = targetURL
		}
		if auth := strings.TrimSpace(values["auth"]); auth != "" {
			item.Auth.Mode = auth
		}
	}
	if lines, ok := sections["grpc"]; ok {
		values := ParseScalarMap(lines)
		item.Type = "grpc"
		item.Body.Mode = "grpc"
		if method := scalar.FirstNonEmpty(values["method"], values["service"]); strings.TrimSpace(method) != "" {
			item.Method = method
		}
		if targetURL := strings.TrimSpace(values["url"]); targetURL != "" {
			item.URL = targetURL
		}
		if mode := strings.TrimSpace(values["body"]); mode != "" {
			item.Body.Mode = NormalizeBodyMode(mode)
		}
		if auth := strings.TrimSpace(values["auth"]); auth != "" {
			item.Auth.Mode = auth
		}
		item.GrpcMethodType = strings.TrimSpace(values["methodType"])
		item.ProtoPath = strings.TrimSpace(values["protoPath"])
	}
	if lines, ok := sections["headers"]; ok {
		item.Headers = parseBruKeyValues(lines)
	}
	if item.Type == "grpc" {
		if lines, ok := sections["metadata"]; ok {
			item.Headers = parseBruKeyValues(lines)
		}
		item.GrpcMessages = parseBruGrpcMessages(content)
		if len(item.GrpcMessages) > 0 {
			item.Body.Mode = "grpc"
		}
	}
	if lines, ok := sections["params:query"]; ok {
		item.Params = parseBruKeyValues(lines)
	}
	if lines, ok := sections["params:path"]; ok {
		item.PathParams = parseBruKeyValues(lines)
	}
	if lines, ok := sections["body:json"]; ok {
		item.Body.Mode = "json"
		item.Body.JSON = strings.TrimRight(joinBruLines(lines), "\n")
	}
	if lines, ok := sections["body:xml"]; ok {
		item.Body.Mode = "xml"
		item.Body.XML = strings.TrimRight(joinBruLines(lines), "\n")
	}
	if lines, ok := sections["body:text"]; ok {
		item.Body.Mode = "text"
		item.Body.Text = strings.TrimRight(joinBruLines(lines), "\n")
	}
	if lines, ok := sections["body:sparql"]; ok {
		item.Body.Mode = "sparql"
		item.Body.Text = strings.TrimRight(joinBruLines(lines), "\n")
	}
	if lines, ok := sections["body:graphql"]; ok {
		item.Type = "graphql"
		item.Body.Mode = "graphql"
		item.Body.GraphQLQuery = strings.TrimRight(joinBruLines(lines), "\n")
	}
	if lines, ok := sections["body:graphql:vars"]; ok {
		item.Type = "graphql"
		item.Body.Mode = "graphql"
		item.Body.GraphQLVariables = strings.TrimRight(joinBruLines(lines), "\n")
	}
	if lines, ok := sections["body:form-urlencoded"]; ok {
		item.Body.Mode = "formUrlEncoded"
		item.Body.FormURLEncoded = parseBruKeyValues(lines)
	}
	if lines, ok := sections["body:multipart-form"]; ok {
		item.Body.Mode = "multipartForm"
		item.Body.Multipart = parseBruMultipartValues(lines)
	}
	if lines, ok := sections["body:file"]; ok {
		item.Body.Mode = "file"
		item.Body.Files = parseBruFileBody(lines)
		item.Body.FilePath, item.Body.FileContentType = types.SelectedFileBodyFields(item.Body.Files)
	}
	if lines, ok := sections["body:ws"]; ok {
		item.Type = "websocket"
		item.Method = "CONNECT"
		item.Body.Mode = "ws"
		item.WSMessages = parseBruWSMessages(content)
		if len(item.WSMessages) == 0 {
			values := ParseScalarMap(lines)
			mode := NormalizeBodyMode(values["type"])
			if mode == "none" {
				mode = "text"
			}
			item.Body.Mode = mode
			AssignYAMLBodyData(&item.Body, mode, scalar.FirstNonEmpty(values["content"], values["data"]))
		}
	}
	applyBruAuthSections(&item.Auth, sections)
	if lines, ok := sections["vars:pre-request"]; ok {
		item.Vars.Req = ParseVariables(lines, false)
	}
	if lines, ok := sections["vars:post-response"]; ok {
		item.Vars.Res = ParseVariables(lines, false)
	}
	if lines, ok := sections["script:pre-request"]; ok {
		item.PreScript = strings.TrimRight(joinBruLines(lines), "\n")
	}
	if lines, ok := sections["script:post-response"]; ok {
		item.PostScript = strings.TrimRight(joinBruLines(lines), "\n")
	}
	if lines, ok := sections["tests"]; ok {
		item.Tests = strings.TrimRight(joinBruLines(lines), "\n")
	}
	if lines, ok := sections["docs"]; ok {
		item.Docs = strings.TrimSpace(joinBruLines(lines))
	}
	if lines, ok := sections["assert"]; ok {
		item.Assertions = parseBruAssertions(lines)
	}
	if lines, ok := sections["settings"]; ok {
		applyBruSettings(&item, ParseScalarMap(lines))
	}
	item.Examples = parseBruExamples(content, item)
	if item.Name == "" {
		return types.RequestItem{}, errors.New("bru meta.name is required")
	}
	return item, nil
}

func ParseCollectionMetadata(collection *types.Collection, content string) error {
	sections := ParseSections(content)
	if meta, ok := sections["meta"]; ok {
		values := ParseScalarMap(meta)
		if name := strings.TrimSpace(values["name"]); name != "" {
			collection.Name = name
		}
	}
	if lines, ok := sections["headers"]; ok {
		collection.Headers = parseBruKeyValues(lines)
	}
	if lines, ok := sections["vars:pre-request"]; ok {
		collection.Variables = ParseVariables(lines, false)
	} else if lines, ok := sections["vars"]; ok {
		collection.Variables = ParseVariables(lines, false)
	}
	if lines, ok := sections["vars:post-response"]; ok {
		collection.ResVariables = ParseVariables(lines, false)
	}
	applyBruAuthSections(&collection.Auth, sections)
	if lines, ok := sections["script:pre-request"]; ok {
		collection.PreScript = strings.TrimRight(joinBruLines(lines), "\n")
	}
	if lines, ok := sections["script:post-response"]; ok {
		collection.PostScript = strings.TrimRight(joinBruLines(lines), "\n")
	}
	if lines, ok := sections["tests"]; ok {
		collection.Tests = strings.TrimRight(joinBruLines(lines), "\n")
	}
	if lines, ok := sections["docs"]; ok {
		collection.Docs = strings.TrimSpace(joinBruLines(lines))
	}
	return nil
}

// AssignYAMLBodyData and ParseYAMLKeyValues live here rather than in
// internal/types because they read YAML-shaped documents, and types is the leaf
// package everything else depends on.
//
// UPDATE, because this comment used to say "when the YAML reader moves to
// internal/store/yaml, this is the pair to move with it" and that move has
// since happened. The reader is internal/store/yamlstore, and the pair did NOT
// move with it. The reason is worth recording rather than leaving as an
// apparently unfinished instruction:
//
// yamlstore reaches into bru for six symbols. Four are YAML helpers
// (ParseYAMLKeyValues, YAMLVariables, YAMLEnabled, AssignYAMLBodyData) and two
// are not (WsMessagesForStorage, ParseImportedGlobalEnvironmentsJSON). Moving
// the YAML four would therefore SHRINK the yamlstore -> bru edge without
// deleting it, while adding a package — and bru itself calls
// AssignYAMLBodyData, so a move to yamlstore would reverse the edge rather than
// remove it. See internal/store/bru/yaml.go, which is where the YAML helpers in
// this package now sit together.
