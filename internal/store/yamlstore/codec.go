package yamlstore

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/mutexdev/lite_api/internal/openapisync"
	"github.com/mutexdev/lite_api/internal/scalar"
	"github.com/mutexdev/lite_api/internal/store/bru"
	"github.com/mutexdev/lite_api/internal/transport"
	"github.com/mutexdev/lite_api/internal/types"
	"github.com/mutexdev/lite_api/internal/wsmessage"
)

func StringifyCollection(collection types.Collection) string {
	brunoExtensions := map[string]interface{}{
		"ignore": []string{"node_modules", ".git"},
	}
	if len(collection.OpenAPI) > 0 {
		brunoExtensions["openapi"] = yamlOpenAPISyncConfigs(collection.OpenAPI)
	}
	root := map[string]interface{}{
		"opencollection": "1.0.0",
		"info": map[string]interface{}{
			"name":    collection.Name,
			"version": scalar.FirstNonEmpty(collection.Version, "1"),
		},
		"extensions": map[string]interface{}{
			"bruno": brunoExtensions,
		},
	}
	request := map[string]interface{}{}
	if len(collection.Headers) > 0 {
		request["headers"] = yamlKeyValues(collection.Headers)
	}
	if len(collection.Variables) > 0 {
		request["variables"] = bru.YAMLVariables(collection.Variables)
	}
	if len(collection.ResVariables) > 0 {
		request["actions"] = yamlPostResponseActions(collection.ResVariables)
	}
	if collection.Auth.Mode != "" && collection.Auth.Mode != "none" {
		request["auth"] = yamlAuth(collection.Auth)
	}
	scripts := []map[string]interface{}{}
	if collection.PreScript != "" {
		scripts = append(scripts, map[string]interface{}{"type": "before-request", "code": collection.PreScript})
	}
	if collection.PostScript != "" {
		scripts = append(scripts, map[string]interface{}{"type": "after-response", "code": collection.PostScript})
	}
	if collection.Tests != "" {
		scripts = append(scripts, map[string]interface{}{"type": "tests", "code": collection.Tests})
	}
	if len(scripts) > 0 {
		request["scripts"] = scripts
	}
	if len(request) > 0 {
		root["request"] = request
	}
	if strings.TrimSpace(collection.Docs) != "" {
		root["docs"] = map[string]interface{}{"content": collection.Docs}
	}
	config := map[string]interface{}{}
	if len(collection.Environments) > 0 {
		envs := make([]map[string]interface{}, 0, len(collection.Environments))
		for _, env := range collection.Environments {
			envs = append(envs, map[string]interface{}{
				"name":      env.Name,
				"color":     env.Color,
				"variables": bru.YAMLVariables(env.Variables),
			})
		}
		config["environments"] = envs
	}
	if transport.HasProxyConfig(collection.Proxy) {
		config["proxy"] = yamlProxyConfig(collection.Proxy)
	}
	if transport.HasClientCertificates(collection.ClientCertificates) {
		config["clientCertificates"] = yamlClientCertificates(collection.ClientCertificates)
	}
	if types.HasCollectionPresets(collection.Presets) {
		config["presets"] = yamlCollectionPresets(collection.Presets)
	}
	if types.HasCollectionProtobuf(collection.Protobuf) {
		config["protobuf"] = yamlCollectionProtobuf(collection.Protobuf)
	}
	if len(config) > 0 {
		root["config"] = config
	}
	data, _ := yaml.Marshal(root)
	return string(data)
}

func StringifyFolder(folder types.FolderConfig) string {
	info := map[string]interface{}{
		"name": scalar.FirstNonEmpty(folder.Name, filepath.Base(filepath.FromSlash(folder.Path))),
		"type": "folder",
	}
	if folder.Seq > 0 {
		info["seq"] = folder.Seq
	}
	root := map[string]interface{}{"info": info}
	request := map[string]interface{}{}
	if len(folder.Headers) > 0 {
		request["headers"] = yamlKeyValues(folder.Headers)
	}
	if len(folder.Variables) > 0 {
		request["variables"] = bru.YAMLVariables(folder.Variables)
	}
	if len(folder.ResVariables) > 0 {
		request["actions"] = yamlPostResponseActions(folder.ResVariables)
	}
	if folder.Auth.Mode != "" {
		request["auth"] = yamlAuth(folder.Auth)
	}
	scripts := []map[string]interface{}{}
	if folder.PreScript != "" {
		scripts = append(scripts, map[string]interface{}{"type": "before-request", "code": folder.PreScript})
	}
	if folder.PostScript != "" {
		scripts = append(scripts, map[string]interface{}{"type": "after-response", "code": folder.PostScript})
	}
	if folder.Tests != "" {
		scripts = append(scripts, map[string]interface{}{"type": "tests", "code": folder.Tests})
	}
	if len(scripts) > 0 {
		request["scripts"] = scripts
	}
	if len(request) > 0 {
		root["request"] = request
	}
	if strings.TrimSpace(folder.Docs) != "" {
		root["docs"] = map[string]interface{}{"content": folder.Docs}
	}
	data, _ := yaml.Marshal(root)
	return string(data)
}

func StringifyRequest(item types.RequestItem) (string, error) {
	requestType := item.Type
	if requestType == "" {
		requestType = "http"
	}
	root := map[string]interface{}{
		"info": map[string]interface{}{
			"name": item.Name,
			"type": requestType,
			"seq":  item.Seq,
		},
	}
	switch requestType {
	case "graphql":
		section := map[string]interface{}{
			"method": item.Method,
			"url":    item.URL,
			"body": map[string]interface{}{
				"query":     item.Body.GraphQLQuery,
				"variables": item.Body.GraphQLVariables,
			},
		}
		addCommonYAMLRequestFields(section, item)
		root["graphql"] = section
	case "websocket":
		section := map[string]interface{}{
			"url": item.URL,
		}
		if message := yamlWSMessage(item); message != nil {
			section["message"] = message
		}
		addCommonYAMLRequestFields(section, item)
		root["websocket"] = section
	case "grpc":
		section := map[string]interface{}{
			"url":    item.URL,
			"method": item.Method,
		}
		if strings.TrimSpace(item.GrpcMethodType) != "" {
			section["methodType"] = item.GrpcMethodType
		}
		if strings.TrimSpace(item.ProtoPath) != "" {
			section["protoFilePath"] = item.ProtoPath
		}
		if len(item.Headers) > 0 {
			section["metadata"] = yamlGrpcMetadata(item.Headers)
		}
		if len(item.GrpcMessages) > 0 {
			section["message"] = yamlGrpcMessages(item.GrpcMessages)
		}
		if item.Auth.Mode != "" && item.Auth.Mode != "none" {
			section["auth"] = yamlAuth(item.Auth)
		}
		root["grpc"] = section
	default:
		section := map[string]interface{}{
			"method": item.Method,
			"url":    item.URL,
		}
		addCommonYAMLRequestFields(section, item)
		if body := yamlBody(item.Body); len(body) > 0 {
			section["body"] = body
		}
		root["http"] = section
	}
	if len(item.Vars.Req) > 0 || len(item.Vars.Res) > 0 || item.PreScript != "" || item.PostScript != "" || item.Tests != "" {
		runtime := map[string]interface{}{}
		if len(item.Vars.Req) > 0 {
			runtime["variables"] = bru.YAMLVariables(item.Vars.Req)
		}
		if len(item.Vars.Res) > 0 {
			runtime["actions"] = yamlPostResponseActions(item.Vars.Res)
		}
		scripts := []map[string]interface{}{}
		if item.PreScript != "" {
			scripts = append(scripts, map[string]interface{}{"type": "before-request", "code": item.PreScript})
		}
		if item.PostScript != "" {
			scripts = append(scripts, map[string]interface{}{"type": "after-response", "code": item.PostScript})
		}
		if item.Tests != "" {
			scripts = append(scripts, map[string]interface{}{"type": "tests", "code": item.Tests})
		}
		if len(scripts) > 0 {
			runtime["scripts"] = scripts
		}
		root["runtime"] = runtime
	}
	root["settings"] = map[string]interface{}{
		"encodeUrl":         item.Settings.EncodeURL,
		"timeout":           item.Settings.TimeoutMs,
		"followRedirects":   item.Settings.FollowRedirects,
		"maxRedirects":      item.Settings.MaxRedirects,
		"storeCookies":      item.Settings.StoreCookies,
		"verifyTls":         item.Settings.VerifyTLS,
		"keepAliveInterval": item.Settings.KeepAliveInterval,
	}
	if strings.TrimSpace(item.Docs) != "" {
		root["docs"] = item.Docs
	}
	data, err := yaml.Marshal(root)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func addCommonYAMLRequestFields(section map[string]interface{}, item types.RequestItem) {
	if len(item.Headers) > 0 {
		section["headers"] = yamlKeyValues(item.Headers)
	}
	if len(item.Params) > 0 || len(item.PathParams) > 0 {
		section["params"] = yamlParams(item.Params, item.PathParams)
	}
	if item.Auth.Mode != "" && item.Auth.Mode != "none" {
		section["auth"] = yamlAuth(item.Auth)
	}
}

func yamlBody(body types.RequestBody) map[string]interface{} {
	mode := yamlBodyType(body.Mode)
	if mode == "" || mode == "none" {
		return nil
	}
	result := map[string]interface{}{"type": mode}
	switch body.Mode {
	case "formUrlEncoded":
		result["data"] = yamlKeyValues(body.FormURLEncoded)
	case "multipartForm":
		result["data"] = yamlMultipart(body.Multipart)
	case "file":
		result["data"] = yamlFileBody(body)
	default:
		result["data"] = yamlBodyText(body)
	}
	return result
}

func yamlFileBody(body types.RequestBody) []map[string]interface{} {
	entries := types.FileBodyEntriesOf(body)
	if len(entries) == 0 {
		return nil
	}
	result := make([]map[string]interface{}, 0, len(entries))
	for _, file := range entries {
		if strings.TrimSpace(file.FilePath) == "" && strings.TrimSpace(file.ContentType) == "" {
			continue
		}
		entry := map[string]interface{}{
			"filePath": file.FilePath,
			"selected": file.Selected,
		}
		if strings.TrimSpace(file.ContentType) != "" {
			entry["contentType"] = file.ContentType
		}
		result = append(result, entry)
	}
	return result
}

func yamlBodyText(body types.RequestBody) string {
	switch body.Mode {
	case "json":
		return body.JSON
	case "xml":
		return body.XML
	default:
		return body.Text
	}
}

func yamlBodyType(mode string) string {
	switch mode {
	case "formUrlEncoded":
		return "form-urlencoded"
	case "multipartForm":
		return "multipart-form"
	case "":
		return "none"
	default:
		return mode
	}
}

func yamlWSMessage(item types.RequestItem) interface{} {
	messages := bru.WsMessagesForStorage(item)
	if len(messages) == 0 {
		return nil
	}
	if len(messages) == 1 {
		message := messages[0]
		if strings.TrimSpace(message.Name) == "" && !message.Selected {
			return map[string]interface{}{
				"type": wsmessage.NormalizeMessageType(message.Type),
				"data": message.Content,
			}
		}
	}
	return yamlWSMessages(messages)
}

func yamlWSMessages(messages []types.WSMessage) []map[string]interface{} {
	result := make([]map[string]interface{}, 0, len(messages))
	for index, message := range messages {
		if strings.TrimSpace(message.Name) == "" && strings.TrimSpace(message.Content) == "" {
			continue
		}
		title := message.Name
		if strings.TrimSpace(title) == "" {
			title = fmt.Sprintf("message %d", index+1)
		}
		result = append(result, map[string]interface{}{
			"title":    title,
			"selected": message.Selected,
			"message": map[string]interface{}{
				"type": wsmessage.NormalizeMessageType(message.Type),
				"data": message.Content,
			},
		})
	}
	return result
}

func yamlKeyValues(values []types.KeyValue) []map[string]interface{} {
	result := make([]map[string]interface{}, 0, len(values))
	for _, value := range values {
		entry := map[string]interface{}{
			"name":    value.Name,
			"value":   value.Value,
			"enabled": value.Enabled,
		}
		if value.Secret {
			entry["secret"] = true
		}
		if value.Description != "" {
			entry["description"] = value.Description
		}
		result = append(result, entry)
	}
	return result
}

func yamlGrpcMetadata(values []types.KeyValue) []map[string]interface{} {
	result := make([]map[string]interface{}, 0, len(values))
	for _, value := range values {
		entry := map[string]interface{}{
			"name":  value.Name,
			"value": value.Value,
		}
		if !value.Enabled {
			entry["disabled"] = true
		}
		if value.Description != "" {
			entry["description"] = value.Description
		}
		result = append(result, entry)
	}
	return result
}

func yamlGrpcMessages(messages []types.GrpcMessage) []map[string]interface{} {
	result := make([]map[string]interface{}, 0, len(messages))
	for index, message := range messages {
		if strings.TrimSpace(message.Name) == "" && strings.TrimSpace(message.Content) == "" {
			continue
		}
		title := message.Name
		if strings.TrimSpace(title) == "" {
			title = fmt.Sprintf("message %d", index+1)
		}
		result = append(result, map[string]interface{}{
			"title":   title,
			"message": message.Content,
		})
	}
	return result
}

func yamlOAuth2AdditionalParameters(auth types.OAuth2Auth) map[string]interface{} {
	result := map[string]interface{}{}
	if rows := yamlOAuth2AdditionalParams(auth.AuthorizationAdditionalParams); len(rows) > 0 {
		result["authorization"] = rows
	}
	if rows := yamlOAuth2AdditionalParams(auth.TokenAdditionalParams); len(rows) > 0 {
		result["token"] = rows
	}
	if rows := yamlOAuth2AdditionalParams(auth.RefreshAdditionalParams); len(rows) > 0 {
		result["refresh"] = rows
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func yamlOAuth2AdditionalParams(params []types.OAuth2AdditionalParam) []map[string]interface{} {
	result := []map[string]interface{}{}
	for _, param := range params {
		if strings.TrimSpace(param.Name) == "" {
			continue
		}
		entry := map[string]interface{}{
			"name":    param.Name,
			"value":   param.Value,
			"sendIn":  types.NormalizeOAuth2AdditionalPlacement(param.SendIn),
			"enabled": param.Enabled,
		}
		if param.Secret {
			entry["secret"] = true
		}
		if param.Description != "" {
			entry["description"] = param.Description
		}
		result = append(result, entry)
	}
	return result
}

func ParseProxyConfig(raw interface{}) (types.ProxyConfig, bool) {
	valueMap, ok := scalar.Map(raw)
	if !ok {
		return types.ProxyConfig{}, false
	}
	proxy := types.ProxyConfig{
		Inherit:  scalar.BoolValue(valueMap["inherit"], false),
		Disabled: scalar.BoolValue(valueMap["disabled"], false),
	}
	configMap, _ := scalar.Map(valueMap["config"])
	if configMap == nil {
		configMap = valueMap
	}
	proxy.Protocol = scalar.FirstNonEmpty(scalar.FirstYAMLString(configMap, "protocol"), "http")
	proxy.Hostname = scalar.FirstYAMLString(configMap, "hostname", "host")
	proxy.Port = strings.TrimSpace(scalar.YAMLString(configMap["port"]))
	proxy.BypassProxy = scalar.FirstYAMLString(configMap, "bypassProxy", "bypass_proxy", "noProxy", "no_proxy")
	if authMap, ok := scalar.Map(configMap["auth"]); ok {
		proxy.Auth.Username = scalar.FirstYAMLString(authMap, "username", "user")
		proxy.Auth.Password = scalar.FirstYAMLString(authMap, "password", "pass")
		proxy.Auth.Disabled = scalar.BoolValue(authMap["disabled"], false)
		if enabled, ok := scalar.BoolValueOK(authMap["enabled"]); ok {
			proxy.Auth.Disabled = !enabled
		}
	}
	return transport.NormalizeProxyConfig(proxy), true
}

func ParseJSONProxyConfig(raw interface{}) (types.ProxyConfig, bool) {
	valueMap, ok := scalar.Map(raw)
	if !ok {
		return types.ProxyConfig{}, false
	}
	if _, hasNewConfig := valueMap["config"]; hasNewConfig {
		return ParseProxyConfig(valueMap)
	}
	proxy := types.ProxyConfig{
		Inherit:     false,
		Disabled:    !scalar.BoolValue(valueMap["enabled"], false),
		Protocol:    scalar.FirstNonEmpty(scalar.FirstYAMLString(valueMap, "protocol"), "http"),
		Hostname:    scalar.FirstYAMLString(valueMap, "hostname", "host"),
		Port:        strings.TrimSpace(scalar.YAMLString(valueMap["port"])),
		BypassProxy: scalar.FirstYAMLString(valueMap, "bypassProxy", "bypass_proxy", "noProxy", "no_proxy"),
	}
	if mode := strings.ToLower(scalar.FirstYAMLString(valueMap, "mode", "source")); mode != "" {
		switch mode {
		case "off", "disabled":
			proxy.Disabled = true
		case "system", "inherit":
			proxy.Inherit = true
			proxy.Disabled = false
		case "on", "manual":
			proxy.Inherit = false
			proxy.Disabled = false
		}
	}
	if authMap, ok := scalar.Map(valueMap["auth"]); ok {
		proxy.Auth.Username = scalar.FirstYAMLString(authMap, "username", "user")
		proxy.Auth.Password = scalar.FirstYAMLString(authMap, "password", "pass")
		proxy.Auth.Disabled = !scalar.BoolValue(authMap["enabled"], true)
		if disabled, ok := scalar.BoolValueOK(authMap["disabled"]); ok {
			proxy.Auth.Disabled = disabled
		}
	}
	return transport.NormalizeProxyConfig(proxy), true
}

func yamlProxyConfig(proxy types.ProxyConfig) map[string]interface{} {
	proxy = transport.NormalizeProxyConfig(proxy)
	config := map[string]interface{}{
		"protocol":    scalar.FirstNonEmpty(proxy.Protocol, "http"),
		"hostname":    proxy.Hostname,
		"port":        proxyPortValue(proxy.Port),
		"auth":        map[string]interface{}{"username": proxy.Auth.Username, "password": proxy.Auth.Password},
		"bypassProxy": proxy.BypassProxy,
	}
	if proxy.Auth.Disabled {
		config["auth"].(map[string]interface{})["disabled"] = true
	}
	result := map[string]interface{}{"inherit": proxy.Inherit, "config": config}
	if proxy.Disabled {
		result["disabled"] = true
	}
	return result
}

func JSONProxyConfig(proxy types.ProxyConfig) map[string]interface{} {
	return yamlProxyConfig(proxy)
}

func ParseClientCertificates(raw interface{}) ([]types.ClientCertificateConfig, bool) {
	values, ok := scalar.ListValue(raw)
	if !ok {
		return nil, false
	}
	certs := make([]types.ClientCertificateConfig, 0, len(values))
	for _, value := range values {
		valueMap, ok := scalar.Map(value)
		if !ok {
			continue
		}
		cert := types.ClientCertificateConfig{
			Domain:     scalar.FirstYAMLString(valueMap, "domain"),
			Type:       scalar.FirstYAMLString(valueMap, "type"),
			Passphrase: scalar.FirstYAMLString(valueMap, "passphrase"),
		}
		switch strings.ToLower(strings.TrimSpace(cert.Type)) {
		case "pem", "cert", "":
			cert.Type = "cert"
			cert.CertFilePath = scalar.FirstYAMLString(valueMap, "certificateFilePath", "certFilePath", "cert")
			cert.KeyFilePath = scalar.FirstYAMLString(valueMap, "privateKeyFilePath", "keyFilePath", "key")
		case "pkcs12", "pfx":
			cert.Type = "pfx"
			cert.PFXFilePath = scalar.FirstYAMLString(valueMap, "pkcs12FilePath", "pfxFilePath", "pfx")
		default:
			cert.CertFilePath = scalar.FirstYAMLString(valueMap, "certificateFilePath", "certFilePath", "cert")
			cert.KeyFilePath = scalar.FirstYAMLString(valueMap, "privateKeyFilePath", "keyFilePath", "key")
			cert.PFXFilePath = scalar.FirstYAMLString(valueMap, "pkcs12FilePath", "pfxFilePath", "pfx")
		}
		certs = append(certs, cert)
	}
	return transport.NormalizeClientCertificates(certs), true
}

func ParseJSONClientCertificates(raw interface{}) ([]types.ClientCertificateConfig, bool) {
	valueMap, ok := scalar.Map(raw)
	if ok {
		return parseYAMLBrunoClientCertificateList(valueMap["certs"])
	}
	return parseYAMLBrunoClientCertificateList(raw)
}

func parseYAMLBrunoClientCertificateList(raw interface{}) ([]types.ClientCertificateConfig, bool) {
	values, ok := scalar.ListValue(raw)
	if !ok {
		return nil, false
	}
	certs := make([]types.ClientCertificateConfig, 0, len(values))
	for _, value := range values {
		valueMap, ok := scalar.Map(value)
		if !ok {
			continue
		}
		certs = append(certs, types.ClientCertificateConfig{
			Domain:       scalar.FirstYAMLString(valueMap, "domain"),
			Type:         scalar.FirstNonEmpty(scalar.FirstYAMLString(valueMap, "type"), "cert"),
			CertFilePath: scalar.FirstYAMLString(valueMap, "certFilePath", "certificateFilePath", "cert"),
			KeyFilePath:  scalar.FirstYAMLString(valueMap, "keyFilePath", "privateKeyFilePath", "key"),
			PFXFilePath:  scalar.FirstYAMLString(valueMap, "pfxFilePath", "pkcs12FilePath", "pfx"),
			Passphrase:   scalar.FirstYAMLString(valueMap, "passphrase"),
		})
	}
	return transport.NormalizeClientCertificates(certs), true
}

func yamlClientCertificates(certs []types.ClientCertificateConfig) []map[string]interface{} {
	normalized := transport.NormalizeClientCertificates(certs)
	result := make([]map[string]interface{}, 0, len(normalized))
	for _, cert := range normalized {
		entry := map[string]interface{}{
			"domain": cert.Domain,
		}
		if cert.Type == "pfx" {
			entry["type"] = "pkcs12"
			entry["pkcs12FilePath"] = cert.PFXFilePath
		} else {
			entry["type"] = "pem"
			entry["certificateFilePath"] = cert.CertFilePath
			entry["privateKeyFilePath"] = cert.KeyFilePath
		}
		if cert.Passphrase != "" {
			entry["passphrase"] = cert.Passphrase
		}
		result = append(result, entry)
	}
	return result
}

func JSONClientCertificates(certs []types.ClientCertificateConfig) map[string]interface{} {
	normalized := transport.NormalizeClientCertificates(certs)
	entries := make([]map[string]interface{}, 0, len(normalized))
	for _, cert := range normalized {
		entry := map[string]interface{}{
			"domain": cert.Domain,
			"type":   scalar.FirstNonEmpty(cert.Type, "cert"),
		}
		if cert.Type == "pfx" {
			entry["pfxFilePath"] = cert.PFXFilePath
		} else {
			entry["certFilePath"] = cert.CertFilePath
			entry["keyFilePath"] = cert.KeyFilePath
		}
		if cert.Passphrase != "" {
			entry["passphrase"] = cert.Passphrase
		}
		entries = append(entries, entry)
	}
	return map[string]interface{}{"enabled": true, "certs": entries}
}

func ParseCollectionPresets(raw interface{}) (types.CollectionPresets, bool) {
	valueMap, ok := scalar.Map(raw)
	if !ok {
		return types.CollectionPresets{}, false
	}
	presets := types.CollectionPresets{
		RequestType: scalar.FirstYAMLString(valueMap, "requestType", "request_type", "type"),
		RequestURL:  scalar.FirstYAMLString(valueMap, "requestUrl", "requestURL", "request_url", "url"),
	}
	return types.NormalizeCollectionPresets(presets), true
}

func yamlCollectionPresets(presets types.CollectionPresets) map[string]interface{} {
	return JSONCollectionPresets(presets)
}

func JSONCollectionPresets(presets types.CollectionPresets) map[string]interface{} {
	normalized := types.NormalizeCollectionPresets(presets)
	return map[string]interface{}{
		"requestType": types.BrunoPresetRequestType(normalized.RequestType),
		"requestUrl":  normalized.RequestURL,
	}
}

func ParseCollectionProtobuf(raw interface{}) (types.CollectionProtobufConfig, bool) {
	valueMap, ok := scalar.Map(raw)
	if !ok {
		return types.CollectionProtobufConfig{}, false
	}
	result := types.CollectionProtobufConfig{}
	if values, ok := scalar.ListValue(valueMap["protoFiles"]); ok {
		result.ProtoFiles = make([]types.CollectionProtoFile, 0, len(values))
		for _, value := range values {
			if valueMap, ok := scalar.Map(value); ok {
				path := scalar.FirstYAMLString(valueMap, "path", "filePath", "protoFilePath", "proto_file_path")
				if path == "" {
					continue
				}
				result.ProtoFiles = append(result.ProtoFiles, types.CollectionProtoFile{
					Path:   path,
					Type:   scalar.FirstNonEmpty(scalar.FirstYAMLString(valueMap, "type"), "file"),
					Exists: scalar.BoolValue(valueMap["exists"], false),
				})
				continue
			}
			if path := strings.TrimSpace(scalar.YAMLString(value)); path != "" {
				result.ProtoFiles = append(result.ProtoFiles, types.CollectionProtoFile{Path: path, Type: "file"})
			}
		}
	}
	if values, ok := scalar.ListValue(valueMap["importPaths"]); ok {
		result.ImportPaths = make([]types.CollectionProtoImportPath, 0, len(values))
		for _, value := range values {
			if valueMap, ok := scalar.Map(value); ok {
				path := scalar.FirstYAMLString(valueMap, "path", "directoryPath", "directory", "dir")
				if path == "" {
					continue
				}
				enabled := true
				if parsed, ok := scalar.BoolValueOK(valueMap["enabled"]); ok {
					enabled = parsed
				} else if disabled, ok := scalar.BoolValueOK(valueMap["disabled"]); ok {
					enabled = !disabled
				}
				result.ImportPaths = append(result.ImportPaths, types.CollectionProtoImportPath{
					Path:    path,
					Enabled: enabled,
					Exists:  scalar.BoolValue(valueMap["exists"], false),
				})
				continue
			}
			if path := strings.TrimSpace(scalar.YAMLString(value)); path != "" {
				result.ImportPaths = append(result.ImportPaths, types.CollectionProtoImportPath{Path: path, Enabled: true})
			}
		}
	}
	return result, true
}

func yamlCollectionProtobuf(protobuf types.CollectionProtobufConfig) map[string]interface{} {
	normalized := types.NormalizeCollectionProtobuf("", protobuf)
	result := map[string]interface{}{}
	if len(normalized.ProtoFiles) > 0 {
		protoFiles := make([]map[string]interface{}, 0, len(normalized.ProtoFiles))
		for _, protoFile := range normalized.ProtoFiles {
			entry := map[string]interface{}{
				"path": protoFile.Path,
				"type": scalar.FirstNonEmpty(protoFile.Type, "file"),
			}
			protoFiles = append(protoFiles, entry)
		}
		result["protoFiles"] = protoFiles
	}
	if len(normalized.ImportPaths) > 0 {
		importPaths := make([]map[string]interface{}, 0, len(normalized.ImportPaths))
		for _, importPath := range normalized.ImportPaths {
			importPaths = append(importPaths, map[string]interface{}{
				"path":    importPath.Path,
				"enabled": importPath.Enabled,
			})
		}
		result["importPaths"] = importPaths
	}
	return result
}

func JSONCollectionProtobuf(protobuf types.CollectionProtobufConfig) map[string]interface{} {
	return yamlCollectionProtobuf(protobuf)
}

func ParseOpenAPISyncConfigs(raw interface{}) []types.OpenAPISyncConfig {
	values, ok := scalar.ListValue(raw)
	if !ok {
		if valueMap, mapOK := scalar.Map(raw); mapOK {
			values = []interface{}{valueMap}
			ok = true
		}
	}
	if !ok {
		return nil
	}
	configs := make([]types.OpenAPISyncConfig, 0, len(values))
	for _, value := range values {
		valueMap, ok := scalar.Map(value)
		if !ok {
			continue
		}
		config := types.OpenAPISyncConfig{
			SourceURL:         scalar.FirstYAMLString(valueMap, "sourceUrl", "sourceURL", "source_url", "url"),
			GroupBy:           scalar.FirstYAMLString(valueMap, "groupBy", "group_by"),
			LastSyncDate:      scalar.FirstYAMLString(valueMap, "lastSyncDate", "last_sync_date"),
			SpecHash:          scalar.FirstYAMLString(valueMap, "specHash", "spec_hash"),
			AutoCheck:         scalar.BoolValue(valueMap["autoCheck"], true),
			AutoCheckInterval: scalar.IntValue(valueMap["autoCheckInterval"], 5),
		}
		config = openapisync.NormalizeConfig(config)
		if strings.TrimSpace(config.SourceURL) == "" && strings.TrimSpace(config.SpecHash) == "" {
			continue
		}
		configs = append(configs, config)
	}
	return configs
}

func yamlOpenAPISyncConfigs(configs []types.OpenAPISyncConfig) []map[string]interface{} {
	result := make([]map[string]interface{}, 0, len(configs))
	for _, config := range configs {
		normalized := openapisync.NormalizeConfig(config)
		entry := map[string]interface{}{
			"sourceUrl":         normalized.SourceURL,
			"groupBy":           normalized.GroupBy,
			"autoCheck":         normalized.AutoCheck,
			"autoCheckInterval": normalized.AutoCheckInterval,
		}
		if strings.TrimSpace(normalized.LastSyncDate) != "" {
			entry["lastSyncDate"] = normalized.LastSyncDate
		}
		if strings.TrimSpace(normalized.SpecHash) != "" {
			entry["specHash"] = normalized.SpecHash
		}
		result = append(result, entry)
	}
	return result
}

func JSONOpenAPISyncConfigs(configs []types.OpenAPISyncConfig) []map[string]interface{} {
	return yamlOpenAPISyncConfigs(configs)
}

func proxyPortValue(port string) interface{} {
	port = strings.TrimSpace(port)
	if port == "" {
		return ""
	}
	if parsed, err := strconv.Atoi(port); err == nil {
		return parsed
	}
	return port
}

func yamlParams(queryParams, pathParams []types.KeyValue) []map[string]interface{} {
	result := yamlParamsOfType(queryParams, "query")
	result = append(result, yamlParamsOfType(pathParams, "path")...)
	return result
}

func yamlParamsOfType(values []types.KeyValue, paramType string) []map[string]interface{} {
	result := yamlKeyValues(values)
	for _, value := range result {
		value["type"] = paramType
	}
	return result
}

func yamlMultipart(values []types.FormPart) []map[string]interface{} {
	result := make([]map[string]interface{}, 0, len(values))
	for _, part := range values {
		entry := map[string]interface{}{
			"name":    part.Name,
			"enabled": part.Enabled,
		}
		if part.FilePath != "" {
			entry["type"] = "file"
			entry["value"] = part.FilePath
		} else {
			entry["type"] = "text"
			entry["value"] = part.Value
		}
		if strings.TrimSpace(part.ContentType) != "" {
			entry["contentType"] = part.ContentType
		}
		result = append(result, entry)
	}
	return result
}

func yamlPostResponseActions(values []types.Variable) []map[string]interface{} {
	result := make([]map[string]interface{}, 0, len(values))
	for _, value := range values {
		action := map[string]interface{}{
			"type":  "set-variable",
			"phase": "after-response",
			"selector": map[string]interface{}{
				"expression": fmt.Sprint(value.Value),
				"method":     "jsonq",
			},
			"variable": map[string]interface{}{
				"name":  value.Name,
				"scope": "runtime",
			},
		}
		if !value.Enabled {
			action["disabled"] = true
		}
		if value.Secret {
			action["variable"].(map[string]interface{})["secret"] = true
		}
		result = append(result, action)
	}
	return result
}

func yamlAuth(auth types.AuthConfig) interface{} {
	switch auth.Mode {
	case "inherit", "none":
		return auth.Mode
	case "basic", "digest", "wsse":
		return map[string]interface{}{"mode": auth.Mode, "username": auth.Username, "password": auth.Password}
	case "ntlm":
		return map[string]interface{}{"mode": "ntlm", "username": auth.Username, "password": auth.Password, "domain": auth.Domain}
	case "bearer", "oauth2":
		if auth.Mode == "oauth2" {
			result := map[string]interface{}{
				"mode":                 "oauth2",
				"token":                auth.Token,
				"grantType":            auth.OAuth2.GrantType,
				"callbackUrl":          auth.OAuth2.CallbackURL,
				"authorizationUrl":     auth.OAuth2.AuthorizationURL,
				"accessTokenUrl":       auth.OAuth2.AccessTokenURL,
				"refreshTokenUrl":      auth.OAuth2.RefreshTokenURL,
				"username":             auth.OAuth2.Username,
				"password":             auth.OAuth2.Password,
				"clientId":             auth.OAuth2.ClientID,
				"clientSecret":         auth.OAuth2.ClientSecret,
				"scope":                auth.OAuth2.Scope,
				"state":                auth.OAuth2.State,
				"pkce":                 auth.OAuth2.PKCE,
				"credentialsPlacement": auth.OAuth2.CredentialsPlacement,
				"credentialsId":        auth.OAuth2.CredentialsID,
				"tokenSource":          auth.OAuth2.TokenSource,
				"tokenPlacement":       auth.OAuth2.TokenPlacement,
				"tokenHeaderPrefix":    auth.OAuth2.TokenHeaderPrefix,
				"tokenQueryKey":        auth.OAuth2.TokenQueryKey,
				"autoFetchToken":       auth.OAuth2.AutoFetchToken,
				"autoRefreshToken":     auth.OAuth2.AutoRefreshToken,
			}
			if additional := yamlOAuth2AdditionalParameters(auth.OAuth2); len(additional) > 0 {
				result["additionalParameters"] = additional
			}
			return result
		}
		return map[string]interface{}{"mode": auth.Mode, "token": auth.Token}
	case "apikey":
		return map[string]interface{}{"mode": "apikey", "key": auth.APIKey, "value": auth.APIValue, "location": auth.APILocation}
	case "awsv4":
		return map[string]interface{}{
			"mode":            "awsv4",
			"accessKeyId":     scalar.FirstNonEmpty(auth.AWSV4.AccessKeyID, auth.AWSV4.AccessKey),
			"secretAccessKey": scalar.FirstNonEmpty(auth.AWSV4.SecretAccessKey, auth.AWSV4.SecretKey),
			"sessionToken":    auth.AWSV4.SessionToken,
			"service":         auth.AWSV4.Service,
			"region":          auth.AWSV4.Region,
			"profileName":     auth.AWSV4.ProfileName,
		}
	case "oauth1":
		return map[string]interface{}{
			"mode":              "oauth1",
			"consumerKey":       auth.OAuth1.ConsumerKey,
			"consumerSecret":    auth.OAuth1.ConsumerSecret,
			"accessToken":       auth.OAuth1.AccessToken,
			"accessTokenSecret": auth.OAuth1.AccessTokenSecret,
			"callbackUrl":       auth.OAuth1.CallbackURL,
			"verifier":          auth.OAuth1.Verifier,
			"signatureMethod":   auth.OAuth1.SignatureMethod,
			"privateKey":        auth.OAuth1.PrivateKey,
			"privateKeyType":    auth.OAuth1.PrivateKeyType,
			"timestamp":         auth.OAuth1.Timestamp,
			"nonce":             auth.OAuth1.Nonce,
			"version":           auth.OAuth1.Version,
			"realm":             auth.OAuth1.Realm,
			"placement":         auth.OAuth1.Placement,
			"includeBodyHash":   auth.OAuth1.IncludeBodyHash,
		}
	default:
		return auth.Mode
	}
}

func ParseKeyValues(raw interface{}, queryOnly bool) []types.KeyValue {
	return bru.ParseYAMLKeyValues(raw, queryOnly)
}
