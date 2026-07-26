// Package yamlstore reads LiteAPI's own YAML request format.
//
// US-070. Named yamlstore rather than yaml on purpose: a package called yaml
// would collide with gopkg.in/yaml.v3 at every call site that imports both,
// which is most of them.
package yamlstore

import (
	"LiteAPI/internal/scalar"
	"LiteAPI/internal/store/bru"
	"LiteAPI/internal/types"
	"LiteAPI/internal/wsexec"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

func ParseRequest(content string) (types.RequestItem, error) {
	var raw map[string]interface{}
	if err := yaml.Unmarshal([]byte(content), &raw); err != nil {
		return types.RequestItem{}, err
	}
	info, ok := scalar.Map(raw["info"])
	if !ok {
		return types.RequestItem{}, errors.New("yaml info is required")
	}
	name := strings.TrimSpace(scalar.YAMLString(info["name"]))
	if name == "" {
		name = "Imported request"
	}
	requestType := strings.ToLower(strings.TrimSpace(scalar.YAMLString(info["type"])))
	if requestType == "" {
		requestType = "http"
	}
	seq := scalar.IntValue(info["seq"], 1)
	item := types.NewRequestItem(name, requestType, seq)
	item.Auth = types.AuthConfig{Mode: "none", APILocation: "header"}

	switch requestType {
	case "graphql":
		if section, ok := scalar.Map(raw["graphql"]); ok {
			applyYAMLGraphQL(&item, section)
		}
	case "websocket":
		if section, ok := scalar.Map(raw["websocket"]); ok {
			applyYAMLWebSocket(&item, section)
		}
	case "grpc":
		if section, ok := scalar.Map(raw["grpc"]); ok {
			applyYAMLGrpc(&item, section)
		}
	default:
		if section, ok := scalar.Map(raw["http"]); ok {
			applyYAMLHTTP(&item, section)
		}
	}

	if runtime, ok := scalar.Map(raw["runtime"]); ok {
		if variables, ok := runtime["variables"]; ok {
			item.Vars.Req = ParseVariables(variables)
		}
		item.Vars.Res = append(item.Vars.Res, ParsePostResponseActions(runtime["actions"])...)
		if scripts, ok := scalar.ListValue(runtime["scripts"]); ok {
			for _, scriptValue := range scripts {
				script, ok := scalar.Map(scriptValue)
				if !ok {
					continue
				}
				code := scalar.YAMLString(script["code"])
				switch strings.ToLower(scalar.YAMLString(script["type"])) {
				case "before-request", "pre-request":
					item.PreScript = scalar.AppendScript(item.PreScript, code)
				case "after-response", "post-response":
					item.PostScript = scalar.AppendScript(item.PostScript, code)
				case "tests", "test":
					item.Tests = scalar.AppendScript(item.Tests, code)
				}
			}
		}
	}
	if settings, ok := scalar.Map(raw["settings"]); ok {
		applyYAMLSettings(&item, settings)
	}
	if docs := strings.TrimSpace(scalar.YAMLString(raw["docs"])); docs != "" {
		item.Docs = docs
	}
	return item, nil
}

func applyYAMLHTTP(item *types.RequestItem, section map[string]interface{}) {
	if method := strings.TrimSpace(scalar.YAMLString(section["method"])); method != "" {
		item.Method = strings.ToUpper(method)
	}
	if targetURL := strings.TrimSpace(scalar.YAMLString(section["url"])); targetURL != "" {
		item.URL = targetURL
	}
	if headers, ok := section["headers"]; ok {
		item.Headers = bru.ParseYAMLKeyValues(headers, false)
	}
	if params, ok := section["params"]; ok {
		item.Params, item.PathParams = parseYAMLParams(params)
	}
	if body, ok := section["body"]; ok {
		item.Body = parseYAMLBody(body)
	}
	if auth, ok := section["auth"]; ok {
		item.Auth = ParseAuth(auth, item.Auth)
	}
}

func applyYAMLGraphQL(item *types.RequestItem, section map[string]interface{}) {
	if method := strings.TrimSpace(scalar.YAMLString(section["method"])); method != "" {
		item.Method = strings.ToUpper(method)
	}
	if targetURL := strings.TrimSpace(scalar.YAMLString(section["url"])); targetURL != "" {
		item.URL = targetURL
	}
	item.Body.Mode = "graphql"
	if body, ok := scalar.Map(section["body"]); ok {
		item.Body.GraphQLQuery = scalar.YAMLString(body["query"])
		item.Body.GraphQLVariables = scalar.YAMLString(body["variables"])
	}
	if headers, ok := section["headers"]; ok {
		item.Headers = bru.ParseYAMLKeyValues(headers, false)
	}
	if auth, ok := section["auth"]; ok {
		item.Auth = ParseAuth(auth, item.Auth)
	}
}

func applyYAMLWebSocket(item *types.RequestItem, section map[string]interface{}) {
	item.Body.Mode = "ws"
	if targetURL := strings.TrimSpace(scalar.YAMLString(section["url"])); targetURL != "" {
		item.URL = targetURL
	}
	if headers, ok := section["headers"]; ok {
		item.Headers = bru.ParseYAMLKeyValues(headers, false)
	}
	if rawMessage := scalar.FirstMapValue(section, "message", "messages"); rawMessage != nil {
		item.WSMessages = parseYAMLWSMessages(rawMessage)
		if len(item.WSMessages) == 0 {
			if message, ok := scalar.Map(rawMessage); ok {
				mode := normalizeYAMLBodyMode(scalar.YAMLString(message["type"]))
				item.Body.Mode = mode
				bru.AssignYAMLBodyData(&item.Body, mode, message["data"])
			}
		}
	}
	if auth, ok := section["auth"]; ok {
		item.Auth = ParseAuth(auth, item.Auth)
	}
}

func applyYAMLGrpc(item *types.RequestItem, section map[string]interface{}) {
	item.Type = "grpc"
	item.Body.Mode = "grpc"
	if method := strings.TrimSpace(scalar.YAMLString(section["method"])); method != "" {
		item.Method = method
	}
	if targetURL := strings.TrimSpace(scalar.YAMLString(section["url"])); targetURL != "" {
		item.URL = targetURL
	}
	item.GrpcMethodType = scalar.FirstYAMLString(section, "methodType", "method_type")
	item.ProtoPath = scalar.FirstYAMLString(section, "protoFilePath", "protoPath", "proto_file_path")
	if metadata, ok := section["metadata"]; ok {
		item.Headers = bru.ParseYAMLKeyValues(metadata, false)
	} else if headers, ok := section["headers"]; ok {
		item.Headers = bru.ParseYAMLKeyValues(headers, false)
	}
	item.GrpcMessages = parseYAMLGrpcMessages(scalar.FirstMapValue(section, "message", "messages"))
	if auth, ok := section["auth"]; ok {
		item.Auth = ParseAuth(auth, item.Auth)
	}
}

func parseYAMLBody(raw interface{}) types.RequestBody {
	body := types.RequestBody{Mode: "none"}
	bodyMap, ok := scalar.Map(raw)
	if !ok {
		body.Mode = "text"
		body.Text = scalar.YAMLString(raw)
		return body
	}
	mode := normalizeYAMLBodyMode(scalar.YAMLString(bodyMap["type"]))
	body.Mode = mode
	bru.AssignYAMLBodyData(&body, mode, bodyMap["data"])
	return body
}

func normalizeYAMLBodyMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "none":
		return "none"
	case "json":
		return "json"
	case "xml":
		return "xml"
	case "sparql":
		return "sparql"
	case "grpc":
		return "grpc"
	case "form-urlencoded", "formurlencoded", "urlencoded", "x-www-form-urlencoded":
		return "formUrlEncoded"
	case "multipart-form", "multipartform", "multipart":
		return "multipartForm"
	case "file", "binary":
		return "file"
	default:
		return "text"
	}
}

func parseYAMLParams(raw interface{}) ([]types.KeyValue, []types.KeyValue) {
	values, ok := scalar.ListValue(raw)
	if !ok {
		return nil, nil
	}
	queryParams := []types.KeyValue{}
	pathParams := []types.KeyValue{}
	for _, entry := range values {
		valueMap, ok := scalar.Map(entry)
		if !ok {
			continue
		}
		name := strings.TrimSpace(scalar.YAMLString(valueMap["name"]))
		if name == "" {
			continue
		}
		param := types.KeyValue{
			Name:        name,
			Value:       scalar.YAMLString(valueMap["value"]),
			Enabled:     bru.YAMLEnabled(valueMap),
			Secret:      scalar.BoolValue(valueMap["secret"], false),
			Description: scalar.YAMLString(valueMap["description"]),
		}
		switch strings.ToLower(scalar.YAMLString(valueMap["type"])) {
		case "path":
			pathParams = append(pathParams, param)
		default:
			queryParams = append(queryParams, param)
		}
	}
	return queryParams, pathParams
}

func parseYAMLWSMessages(raw interface{}) []types.WSMessage {
	if raw == nil {
		return nil
	}
	if values, ok := scalar.ListValue(raw); ok {
		result := make([]types.WSMessage, 0, len(values))
		for index, entry := range values {
			message, ok := parseYAMLWSMessage(entry, index)
			if ok {
				result = append(result, message)
			}
		}
		return result
	}
	if message, ok := parseYAMLWSMessage(raw, 0); ok {
		return []types.WSMessage{message}
	}
	return nil
}

func parseYAMLWSMessage(raw interface{}, index int) (types.WSMessage, bool) {
	valueMap, ok := scalar.Map(raw)
	if !ok {
		content := scalar.YAMLString(raw)
		if strings.TrimSpace(content) == "" {
			return types.WSMessage{}, false
		}
		return types.WSMessage{Name: fmt.Sprintf("message %d", index+1), Type: "text", Content: content}, true
	}
	name := scalar.FirstYAMLString(valueMap, "title", "name")
	selected := scalar.BoolValue(valueMap["selected"], false)
	messageMap := valueMap
	fromVariant := false
	if nested, ok := scalar.Map(valueMap["message"]); ok {
		messageMap = nested
		fromVariant = true
	}
	content := scalar.FirstYAMLString(messageMap, "data", "content", "message", "value")
	messageType := wsexec.NormalizeMessageType(scalar.FirstYAMLString(messageMap, "type"))
	if strings.TrimSpace(name) == "" && strings.TrimSpace(content) == "" {
		return types.WSMessage{}, false
	}
	if strings.TrimSpace(name) == "" && fromVariant {
		name = fmt.Sprintf("message %d", index+1)
	}
	return types.WSMessage{Name: name, Type: messageType, Content: content, Selected: selected}, true
}

func parseYAMLGrpcMessages(raw interface{}) []types.GrpcMessage {
	if raw == nil {
		return nil
	}
	if values, ok := scalar.ListValue(raw); ok {
		result := make([]types.GrpcMessage, 0, len(values))
		for index, entry := range values {
			if valueMap, ok := scalar.Map(entry); ok {
				name := scalar.FirstYAMLString(valueMap, "title", "name")
				content := scalar.FirstYAMLString(valueMap, "message", "content", "value")
				if strings.TrimSpace(name) == "" && strings.TrimSpace(content) == "" {
					continue
				}
				if strings.TrimSpace(name) == "" {
					name = fmt.Sprintf("message %d", index+1)
				}
				result = append(result, types.GrpcMessage{Name: name, Content: content})
				continue
			}
			content := scalar.YAMLString(entry)
			if strings.TrimSpace(content) != "" {
				result = append(result, types.GrpcMessage{Name: fmt.Sprintf("message %d", index+1), Content: content})
			}
		}
		return result
	}
	if valueMap, ok := scalar.Map(raw); ok {
		name := scalar.FirstYAMLString(valueMap, "title", "name")
		content := scalar.FirstYAMLString(valueMap, "message", "content", "value")
		if strings.TrimSpace(name) == "" && strings.TrimSpace(content) == "" {
			return nil
		}
		return []types.GrpcMessage{{Name: name, Content: content}}
	}
	content := scalar.YAMLString(raw)
	if strings.TrimSpace(content) == "" {
		return nil
	}
	return []types.GrpcMessage{{Content: content}}
}

func parseYAMLOAuth2AdditionalGroup(raw interface{}) []types.OAuth2AdditionalParam {
	if valueMap, ok := scalar.Map(raw); ok {
		result := []types.OAuth2AdditionalParam{}
		result = append(result, parseYAMLOAuth2AdditionalParams(scalar.FirstMapValue(valueMap, "headers", "header"), "headers")...)
		result = append(result, parseYAMLOAuth2AdditionalParams(scalar.FirstMapValue(valueMap, "queryparams", "queryParams", "query", "params"), "queryparams")...)
		result = append(result, parseYAMLOAuth2AdditionalParams(scalar.FirstMapValue(valueMap, "body", "form", "formData"), "body")...)
		return result
	}
	return parseYAMLOAuth2AdditionalParams(raw, "body")
}

func parseYAMLOAuth2AdditionalParams(raw interface{}, fallbackSendIn string) []types.OAuth2AdditionalParam {
	values, ok := scalar.ListValue(raw)
	if !ok {
		return nil
	}
	result := make([]types.OAuth2AdditionalParam, 0, len(values))
	for _, entry := range values {
		valueMap, ok := scalar.Map(entry)
		if !ok {
			continue
		}
		name := strings.TrimSpace(scalar.YAMLString(valueMap["name"]))
		if name == "" {
			continue
		}
		sendIn := scalar.FirstYAMLString(valueMap, "sendIn", "send_in", "placement", "type")
		if sendIn == "" {
			sendIn = fallbackSendIn
		}
		result = append(result, types.OAuth2AdditionalParam{
			Name:        name,
			Value:       scalar.YAMLString(valueMap["value"]),
			SendIn:      types.NormalizeOAuth2AdditionalPlacement(sendIn),
			Enabled:     bru.YAMLEnabled(valueMap),
			Secret:      scalar.BoolValue(valueMap["secret"], false),
			Description: scalar.YAMLString(valueMap["description"]),
		})
	}
	return result
}

func ParseVariables(raw interface{}) []types.Variable {
	values, ok := scalar.ListValue(raw)
	if !ok {
		return nil
	}
	variables := make([]types.Variable, 0, len(values))
	for _, entry := range values {
		valueMap, ok := scalar.Map(entry)
		if !ok {
			continue
		}
		name := strings.TrimSpace(scalar.YAMLString(valueMap["name"]))
		if name == "" {
			continue
		}
		value, dataType := parseYAMLTypedValue(valueMap["value"])
		if dataType == "string" {
			if topLevelType := strings.ToLower(strings.TrimSpace(scalar.YAMLString(valueMap["type"]))); topLevelType != "" {
				dataType = topLevelType
			}
		}
		variables = append(variables, types.Variable{
			ID:       scalar.NewID("var"),
			Name:     name,
			Value:    value,
			DataType: dataType,
			Type:     dataType,
			Enabled:  bru.YAMLEnabled(valueMap),
			Secret:   scalar.BoolValue(valueMap["secret"], false),
		})
	}
	return variables
}

func ParsePostResponseActions(raw interface{}) []types.Variable {
	actions, ok := scalar.ListValue(raw)
	if !ok {
		return nil
	}
	variables := make([]types.Variable, 0, len(actions))
	for _, actionValue := range actions {
		action, ok := scalar.Map(actionValue)
		if !ok {
			continue
		}
		if !strings.EqualFold(scalar.YAMLString(action["type"]), "set-variable") {
			continue
		}
		phase := strings.ToLower(strings.TrimSpace(scalar.YAMLString(action["phase"])))
		if phase != "after-response" && phase != "post-response" {
			continue
		}
		name := strings.TrimSpace(scalar.YAMLString(action["name"]))
		secret := scalar.BoolValue(action["secret"], false)
		if variable, ok := scalar.Map(action["variable"]); ok {
			if variableName := strings.TrimSpace(scalar.YAMLString(variable["name"])); variableName != "" {
				name = variableName
			}
			secret = scalar.BoolValue(variable["secret"], secret)
		}
		if name == "" {
			continue
		}
		valueRaw := action["value"]
		if selector, ok := scalar.Map(action["selector"]); ok {
			if expression, ok := selector["expression"]; ok {
				valueRaw = expression
			}
		}
		value, dataType := parseYAMLTypedValue(valueRaw)
		if dataType == "string" {
			dataType = "response"
		}
		variables = append(variables, types.Variable{
			ID:       scalar.NewID("var"),
			Name:     types.ResponseVariableRuntimeName(name),
			Value:    value,
			DataType: dataType,
			Type:     dataType,
			Enabled:  bru.YAMLEnabled(action),
			Secret:   secret,
		})
	}
	return variables
}

func ParseEnvironments(raw interface{}) []types.Environment {
	values, ok := scalar.ListValue(raw)
	if !ok {
		return nil
	}
	environments := make([]types.Environment, 0, len(values))
	for _, entry := range values {
		valueMap, ok := scalar.Map(entry)
		if !ok {
			continue
		}
		name := strings.TrimSpace(scalar.YAMLString(valueMap["name"]))
		if name == "" {
			continue
		}
		environments = append(environments, types.Environment{
			ID:        scalar.NewID("env"),
			Name:      name,
			Color:     scalar.YAMLString(valueMap["color"]),
			Variables: ParseVariables(valueMap["variables"]),
		})
	}
	return environments
}

func parseYAMLTypedValue(raw interface{}) (interface{}, string) {
	if valueMap, ok := scalar.Map(raw); ok {
		dataType := strings.ToLower(strings.TrimSpace(scalar.YAMLString(valueMap["type"])))
		data := valueMap["data"]
		switch dataType {
		case "number":
			text := scalar.YAMLString(data)
			if strings.ContainsAny(text, ".eE") {
				if parsed, err := strconv.ParseFloat(text, 64); err == nil {
					return parsed, "number"
				}
			}
			if parsed, err := strconv.Atoi(text); err == nil {
				return parsed, "number"
			}
			return text, "number"
		case "boolean":
			if parsed, err := strconv.ParseBool(scalar.YAMLString(data)); err == nil {
				return parsed, "boolean"
			}
			return scalar.YAMLString(data), "boolean"
		case "object":
			return scalar.YAMLString(data), "object"
		case "":
			return scalar.YAMLString(data), "string"
		default:
			return scalar.YAMLString(data), dataType
		}
	}
	switch v := raw.(type) {
	case bool:
		return v, "boolean"
	case int, int8, int16, int32, int64, float32, float64:
		return v, "number"
	case nil:
		return "", "string"
	default:
		return scalar.YAMLString(v), "string"
	}
}

func ParseAuth(raw interface{}, fallback types.AuthConfig) types.AuthConfig {
	auth := fallback
	if auth.Mode == "" {
		auth.Mode = "none"
	}
	if auth.APILocation == "" {
		auth.APILocation = "header"
	}
	if valueMap, ok := scalar.Map(raw); ok {
		mode := strings.ToLower(scalar.FirstYAMLString(valueMap, "mode", "type", "auth"))
		if mode != "" {
			auth.Mode = mode
		}
		auth.Username = scalar.FirstYAMLString(valueMap, "username", "user")
		auth.Password = scalar.FirstYAMLString(valueMap, "password", "pass")
		auth.Domain = scalar.FirstYAMLString(valueMap, "domain")
		if nested, ok := scalar.Map(valueMap["wsse"]); ok {
			auth.Username = scalar.FirstYAMLString(nested, "username", "user")
			auth.Password = scalar.FirstYAMLString(nested, "password", "pass")
		}
		if nested, ok := scalar.Map(valueMap["ntlm"]); ok {
			auth.Username = scalar.FirstYAMLString(nested, "username", "user")
			auth.Password = scalar.FirstYAMLString(nested, "password", "pass")
			auth.Domain = scalar.FirstYAMLString(nested, "domain")
		}
		auth.Token = scalar.FirstYAMLString(valueMap, "token", "bearerToken", "accessToken")
		auth.APIKey = scalar.FirstYAMLString(valueMap, "key", "apiKey", "name")
		auth.APIValue = scalar.FirstYAMLString(valueMap, "value", "apiValue")
		if location := scalar.FirstYAMLString(valueMap, "location", "placement"); location != "" {
			auth.APILocation = strings.ToLower(location)
		}
		oauth2Map := valueMap
		if nested, ok := scalar.Map(valueMap["oauth2"]); ok {
			oauth2Map = nested
		}
		auth.OAuth2.GrantType = scalar.FirstYAMLString(oauth2Map, "grantType", "grant_type", "flow")
		auth.OAuth2.CallbackURL = scalar.FirstYAMLString(oauth2Map, "callbackUrl", "callback_url")
		auth.OAuth2.AuthorizationURL = scalar.FirstYAMLString(oauth2Map, "authorizationUrl", "authorization_url", "authUrl", "auth_url")
		auth.OAuth2.AccessTokenURL = scalar.FirstYAMLString(oauth2Map, "accessTokenUrl", "access_token_url", "tokenUrl", "token_url")
		auth.OAuth2.RefreshTokenURL = scalar.FirstYAMLString(oauth2Map, "refreshTokenUrl", "refresh_token_url")
		auth.OAuth2.Username = scalar.FirstYAMLString(oauth2Map, "username", "user")
		auth.OAuth2.Password = scalar.FirstYAMLString(oauth2Map, "password", "pass")
		auth.OAuth2.ClientID = scalar.FirstYAMLString(oauth2Map, "clientId", "client_id")
		auth.OAuth2.ClientSecret = scalar.FirstYAMLString(oauth2Map, "clientSecret", "client_secret")
		auth.OAuth2.Scope = scalar.FirstYAMLString(oauth2Map, "scope")
		auth.OAuth2.State = scalar.FirstYAMLString(oauth2Map, "state")
		if pkce, ok := scalar.BoolValueOK(oauth2Map["pkce"]); ok {
			auth.OAuth2.PKCE = pkce
		}
		auth.OAuth2.CredentialsPlacement = scalar.FirstYAMLString(oauth2Map, "credentialsPlacement", "credentials_placement")
		auth.OAuth2.CredentialsID = scalar.FirstYAMLString(oauth2Map, "credentialsId", "credentials_id")
		auth.OAuth2.TokenSource = scalar.FirstYAMLString(oauth2Map, "tokenSource", "token_source")
		auth.OAuth2.TokenPlacement = scalar.FirstYAMLString(oauth2Map, "tokenPlacement", "token_placement")
		auth.OAuth2.TokenHeaderPrefix = scalar.FirstYAMLString(oauth2Map, "tokenHeaderPrefix", "token_header_prefix")
		auth.OAuth2.TokenQueryKey = scalar.FirstYAMLString(oauth2Map, "tokenQueryKey", "token_query_key")
		if autoFetchToken, ok := scalar.BoolValueOK(oauth2Map["autoFetchToken"]); ok {
			auth.OAuth2.AutoFetchToken = autoFetchToken
		} else if autoFetchToken, ok := scalar.BoolValueOK(oauth2Map["auto_fetch_token"]); ok {
			auth.OAuth2.AutoFetchToken = autoFetchToken
		}
		if autoRefreshToken, ok := scalar.BoolValueOK(oauth2Map["autoRefreshToken"]); ok {
			auth.OAuth2.AutoRefreshToken = autoRefreshToken
		} else if autoRefreshToken, ok := scalar.BoolValueOK(oauth2Map["auto_refresh_token"]); ok {
			auth.OAuth2.AutoRefreshToken = autoRefreshToken
		}
		if rows := bru.ParseYAMLKeyValues(scalar.FirstMapValue(oauth2Map, "additionalParams", "additional_params"), false); len(rows) > 0 {
			auth.OAuth2.AdditionalParams = rows
		}
		if additionalMap, ok := scalar.Map(scalar.FirstMapValue(oauth2Map, "additionalParameters", "additional_parameters")); ok {
			auth.OAuth2.AuthorizationAdditionalParams = parseYAMLOAuth2AdditionalGroup(scalar.FirstMapValue(additionalMap, "authorization", "authorizationRequest", "authRequest", "auth_req"))
			auth.OAuth2.TokenAdditionalParams = parseYAMLOAuth2AdditionalGroup(scalar.FirstMapValue(additionalMap, "token", "accessTokenRequest", "access_token_req"))
			auth.OAuth2.RefreshAdditionalParams = parseYAMLOAuth2AdditionalGroup(scalar.FirstMapValue(additionalMap, "refresh", "refreshTokenRequest", "refresh_token_req"))
		}
		awsMap := valueMap
		if nested, ok := scalar.Map(valueMap["awsv4"]); ok {
			awsMap = nested
		}
		auth.AWSV4.AccessKeyID = scalar.FirstYAMLString(awsMap, "accessKeyId", "accessKey")
		auth.AWSV4.SecretAccessKey = scalar.FirstYAMLString(awsMap, "secretAccessKey", "secretKey")
		auth.AWSV4.SessionToken = scalar.FirstYAMLString(awsMap, "sessionToken")
		auth.AWSV4.Service = scalar.FirstYAMLString(awsMap, "service")
		auth.AWSV4.Region = scalar.FirstYAMLString(awsMap, "region")
		auth.AWSV4.ProfileName = scalar.FirstYAMLString(awsMap, "profileName")
		oauth1Map := valueMap
		if nested, ok := scalar.Map(valueMap["oauth1"]); ok {
			oauth1Map = nested
		}
		auth.OAuth1.ConsumerKey = scalar.FirstYAMLString(oauth1Map, "consumerKey", "consumer_key")
		auth.OAuth1.ConsumerSecret = scalar.FirstYAMLString(oauth1Map, "consumerSecret", "consumer_secret")
		auth.OAuth1.AccessToken = scalar.FirstYAMLString(oauth1Map, "accessToken", "access_token")
		auth.OAuth1.AccessTokenSecret = scalar.FirstYAMLString(oauth1Map, "accessTokenSecret", "token_secret")
		auth.OAuth1.CallbackURL = scalar.FirstYAMLString(oauth1Map, "callbackUrl", "callback_url")
		auth.OAuth1.Verifier = scalar.FirstYAMLString(oauth1Map, "verifier")
		auth.OAuth1.SignatureMethod = scalar.FirstYAMLString(oauth1Map, "signatureMethod", "signature_method")
		auth.OAuth1.PrivateKey = scalar.FirstYAMLString(oauth1Map, "privateKey", "private_key")
		auth.OAuth1.PrivateKeyType = scalar.FirstYAMLString(oauth1Map, "privateKeyType", "private_key_type")
		auth.OAuth1.Timestamp = scalar.FirstYAMLString(oauth1Map, "timestamp")
		auth.OAuth1.Nonce = scalar.FirstYAMLString(oauth1Map, "nonce")
		auth.OAuth1.Version = scalar.FirstYAMLString(oauth1Map, "version")
		auth.OAuth1.Realm = scalar.FirstYAMLString(oauth1Map, "realm")
		auth.OAuth1.Placement = scalar.FirstYAMLString(oauth1Map, "placement")
		if includeBodyHash, ok := scalar.BoolValueOK(oauth1Map["includeBodyHash"]); ok {
			auth.OAuth1.IncludeBodyHash = includeBodyHash
		} else if includeBodyHash, ok := scalar.BoolValueOK(oauth1Map["include_body_hash"]); ok {
			auth.OAuth1.IncludeBodyHash = includeBodyHash
		}
		return auth
	}
	mode := strings.ToLower(strings.TrimSpace(scalar.YAMLString(raw)))
	if mode != "" {
		auth.Mode = mode
	}
	return auth
}

func applyYAMLSettings(item *types.RequestItem, settings map[string]interface{}) {
	if encodeURL, ok := scalar.BoolValueOK(settings["encodeUrl"]); ok {
		item.Settings.EncodeURL = encodeURL
	}
	if timeout, ok := scalar.IntValueOK(settings["timeout"]); ok {
		item.Settings.TimeoutMs = timeout
	}
	if follow, ok := scalar.BoolValueOK(settings["followRedirects"]); ok {
		item.Settings.FollowRedirects = follow
	}
	if maxRedirects, ok := scalar.IntValueOK(settings["maxRedirects"]); ok {
		item.Settings.MaxRedirects = maxRedirects
	}
	if storeCookies, ok := scalar.BoolValueOK(settings["storeCookies"]); ok {
		item.Settings.StoreCookies = storeCookies
	}
	if verifyTLS, ok := scalar.BoolValueOK(settings["verifyTls"]); ok {
		item.Settings.VerifyTLS = verifyTLS
	}
	if keepAliveInterval, ok := scalar.IntValueOK(settings["keepAliveInterval"]); ok {
		item.Settings.KeepAliveInterval = keepAliveInterval
	}
}
