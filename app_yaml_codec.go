package main

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/mutexdev/lite_api/internal/store/bru"
	"github.com/mutexdev/lite_api/internal/transport"
)

func stringifyYAMLCollection(collection Collection) string {
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
			"version": firstNonEmpty(collection.Version, "1"),
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
		request["variables"] = yamlVariables(collection.Variables)
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
				"variables": yamlVariables(env.Variables),
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
	if hasCollectionPresets(collection.Presets) {
		config["presets"] = yamlCollectionPresets(collection.Presets)
	}
	if hasCollectionProtobuf(collection.Protobuf) {
		config["protobuf"] = yamlCollectionProtobuf(collection.Protobuf)
	}
	if len(config) > 0 {
		root["config"] = config
	}
	data, _ := yaml.Marshal(root)
	return string(data)
}

func stringifyYAMLFolder(folder FolderConfig) string {
	info := map[string]interface{}{
		"name": firstNonEmpty(folder.Name, filepath.Base(filepath.FromSlash(folder.Path))),
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
		request["variables"] = yamlVariables(folder.Variables)
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

func stringifyYAMLRequest(item RequestItem) (string, error) {
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
			runtime["variables"] = yamlVariables(item.Vars.Req)
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

func addCommonYAMLRequestFields(section map[string]interface{}, item RequestItem) {
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

func yamlBody(body RequestBody) map[string]interface{} {
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

func yamlFileBody(body RequestBody) []map[string]interface{} {
	entries := fileBodyEntries(body)
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

func yamlBodyText(body RequestBody) string {
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

func yamlWSMessage(item RequestItem) interface{} {
	messages := bru.WsMessagesForStorage(item)
	if len(messages) == 0 {
		return nil
	}
	if len(messages) == 1 {
		message := messages[0]
		if strings.TrimSpace(message.Name) == "" && !message.Selected {
			return map[string]interface{}{
				"type": normalizeWSMessageType(message.Type),
				"data": message.Content,
			}
		}
	}
	return yamlWSMessages(messages)
}

func yamlWSMessages(messages []WSMessage) []map[string]interface{} {
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
				"type": normalizeWSMessageType(message.Type),
				"data": message.Content,
			},
		})
	}
	return result
}

func yamlKeyValues(values []KeyValue) []map[string]interface{} {
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

func yamlGrpcMetadata(values []KeyValue) []map[string]interface{} {
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

func yamlGrpcMessages(messages []GrpcMessage) []map[string]interface{} {
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

func yamlOAuth2AdditionalParameters(auth OAuth2Auth) map[string]interface{} {
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

func yamlOAuth2AdditionalParams(params []OAuth2AdditionalParam) []map[string]interface{} {
	result := []map[string]interface{}{}
	for _, param := range params {
		if strings.TrimSpace(param.Name) == "" {
			continue
		}
		entry := map[string]interface{}{
			"name":    param.Name,
			"value":   param.Value,
			"sendIn":  normalizeOAuth2AdditionalPlacement(param.SendIn),
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

func parseYAMLProxyConfig(raw interface{}) (ProxyConfig, bool) {
	valueMap, ok := mapValue(raw)
	if !ok {
		return ProxyConfig{}, false
	}
	proxy := ProxyConfig{
		Inherit:  boolValue(valueMap["inherit"], false),
		Disabled: boolValue(valueMap["disabled"], false),
	}
	configMap, _ := mapValue(valueMap["config"])
	if configMap == nil {
		configMap = valueMap
	}
	proxy.Protocol = firstNonEmpty(firstYAMLString(configMap, "protocol"), "http")
	proxy.Hostname = firstYAMLString(configMap, "hostname", "host")
	proxy.Port = strings.TrimSpace(yamlScalarString(configMap["port"]))
	proxy.BypassProxy = firstYAMLString(configMap, "bypassProxy", "bypass_proxy", "noProxy", "no_proxy")
	if authMap, ok := mapValue(configMap["auth"]); ok {
		proxy.Auth.Username = firstYAMLString(authMap, "username", "user")
		proxy.Auth.Password = firstYAMLString(authMap, "password", "pass")
		proxy.Auth.Disabled = boolValue(authMap["disabled"], false)
		if enabled, ok := boolValueOK(authMap["enabled"]); ok {
			proxy.Auth.Disabled = !enabled
		}
	}
	return transport.NormalizeProxyConfig(proxy), true
}

func parseJSONProxyConfig(raw interface{}) (ProxyConfig, bool) {
	valueMap, ok := mapValue(raw)
	if !ok {
		return ProxyConfig{}, false
	}
	if _, hasNewConfig := valueMap["config"]; hasNewConfig {
		return parseYAMLProxyConfig(valueMap)
	}
	proxy := ProxyConfig{
		Inherit:     false,
		Disabled:    !boolValue(valueMap["enabled"], false),
		Protocol:    firstNonEmpty(firstYAMLString(valueMap, "protocol"), "http"),
		Hostname:    firstYAMLString(valueMap, "hostname", "host"),
		Port:        strings.TrimSpace(yamlScalarString(valueMap["port"])),
		BypassProxy: firstYAMLString(valueMap, "bypassProxy", "bypass_proxy", "noProxy", "no_proxy"),
	}
	if mode := strings.ToLower(firstYAMLString(valueMap, "mode", "source")); mode != "" {
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
	if authMap, ok := mapValue(valueMap["auth"]); ok {
		proxy.Auth.Username = firstYAMLString(authMap, "username", "user")
		proxy.Auth.Password = firstYAMLString(authMap, "password", "pass")
		proxy.Auth.Disabled = !boolValue(authMap["enabled"], true)
		if disabled, ok := boolValueOK(authMap["disabled"]); ok {
			proxy.Auth.Disabled = disabled
		}
	}
	return transport.NormalizeProxyConfig(proxy), true
}

func yamlProxyConfig(proxy ProxyConfig) map[string]interface{} {
	proxy = transport.NormalizeProxyConfig(proxy)
	config := map[string]interface{}{
		"protocol":    firstNonEmpty(proxy.Protocol, "http"),
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

func jsonProxyConfig(proxy ProxyConfig) map[string]interface{} {
	return yamlProxyConfig(proxy)
}

func parseYAMLClientCertificates(raw interface{}) ([]ClientCertificateConfig, bool) {
	values, ok := listValue(raw)
	if !ok {
		return nil, false
	}
	certs := make([]ClientCertificateConfig, 0, len(values))
	for _, value := range values {
		valueMap, ok := mapValue(value)
		if !ok {
			continue
		}
		cert := ClientCertificateConfig{
			Domain:     firstYAMLString(valueMap, "domain"),
			Type:       firstYAMLString(valueMap, "type"),
			Passphrase: firstYAMLString(valueMap, "passphrase"),
		}
		switch strings.ToLower(strings.TrimSpace(cert.Type)) {
		case "pem", "cert", "":
			cert.Type = "cert"
			cert.CertFilePath = firstYAMLString(valueMap, "certificateFilePath", "certFilePath", "cert")
			cert.KeyFilePath = firstYAMLString(valueMap, "privateKeyFilePath", "keyFilePath", "key")
		case "pkcs12", "pfx":
			cert.Type = "pfx"
			cert.PFXFilePath = firstYAMLString(valueMap, "pkcs12FilePath", "pfxFilePath", "pfx")
		default:
			cert.CertFilePath = firstYAMLString(valueMap, "certificateFilePath", "certFilePath", "cert")
			cert.KeyFilePath = firstYAMLString(valueMap, "privateKeyFilePath", "keyFilePath", "key")
			cert.PFXFilePath = firstYAMLString(valueMap, "pkcs12FilePath", "pfxFilePath", "pfx")
		}
		certs = append(certs, cert)
	}
	return transport.NormalizeClientCertificates(certs), true
}

func parseJSONClientCertificates(raw interface{}) ([]ClientCertificateConfig, bool) {
	valueMap, ok := mapValue(raw)
	if ok {
		return parseYAMLBrunoClientCertificateList(valueMap["certs"])
	}
	return parseYAMLBrunoClientCertificateList(raw)
}

func parseYAMLBrunoClientCertificateList(raw interface{}) ([]ClientCertificateConfig, bool) {
	values, ok := listValue(raw)
	if !ok {
		return nil, false
	}
	certs := make([]ClientCertificateConfig, 0, len(values))
	for _, value := range values {
		valueMap, ok := mapValue(value)
		if !ok {
			continue
		}
		certs = append(certs, ClientCertificateConfig{
			Domain:       firstYAMLString(valueMap, "domain"),
			Type:         firstNonEmpty(firstYAMLString(valueMap, "type"), "cert"),
			CertFilePath: firstYAMLString(valueMap, "certFilePath", "certificateFilePath", "cert"),
			KeyFilePath:  firstYAMLString(valueMap, "keyFilePath", "privateKeyFilePath", "key"),
			PFXFilePath:  firstYAMLString(valueMap, "pfxFilePath", "pkcs12FilePath", "pfx"),
			Passphrase:   firstYAMLString(valueMap, "passphrase"),
		})
	}
	return transport.NormalizeClientCertificates(certs), true
}

func yamlClientCertificates(certs []ClientCertificateConfig) []map[string]interface{} {
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

func jsonClientCertificates(certs []ClientCertificateConfig) map[string]interface{} {
	normalized := transport.NormalizeClientCertificates(certs)
	entries := make([]map[string]interface{}, 0, len(normalized))
	for _, cert := range normalized {
		entry := map[string]interface{}{
			"domain": cert.Domain,
			"type":   firstNonEmpty(cert.Type, "cert"),
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

func parseCollectionPresets(raw interface{}) (CollectionPresets, bool) {
	valueMap, ok := mapValue(raw)
	if !ok {
		return CollectionPresets{}, false
	}
	presets := CollectionPresets{
		RequestType: firstYAMLString(valueMap, "requestType", "request_type", "type"),
		RequestURL:  firstYAMLString(valueMap, "requestUrl", "requestURL", "request_url", "url"),
	}
	return normalizeCollectionPresets(presets), true
}

func yamlCollectionPresets(presets CollectionPresets) map[string]interface{} {
	return jsonCollectionPresets(presets)
}

func jsonCollectionPresets(presets CollectionPresets) map[string]interface{} {
	normalized := normalizeCollectionPresets(presets)
	return map[string]interface{}{
		"requestType": brunoPresetRequestType(normalized.RequestType),
		"requestUrl":  normalized.RequestURL,
	}
}

func parseCollectionProtobuf(raw interface{}) (CollectionProtobufConfig, bool) {
	valueMap, ok := mapValue(raw)
	if !ok {
		return CollectionProtobufConfig{}, false
	}
	result := CollectionProtobufConfig{}
	if values, ok := listValue(valueMap["protoFiles"]); ok {
		result.ProtoFiles = make([]CollectionProtoFile, 0, len(values))
		for _, value := range values {
			if valueMap, ok := mapValue(value); ok {
				path := firstYAMLString(valueMap, "path", "filePath", "protoFilePath", "proto_file_path")
				if path == "" {
					continue
				}
				result.ProtoFiles = append(result.ProtoFiles, CollectionProtoFile{
					Path:   path,
					Type:   firstNonEmpty(firstYAMLString(valueMap, "type"), "file"),
					Exists: boolValue(valueMap["exists"], false),
				})
				continue
			}
			if path := strings.TrimSpace(yamlScalarString(value)); path != "" {
				result.ProtoFiles = append(result.ProtoFiles, CollectionProtoFile{Path: path, Type: "file"})
			}
		}
	}
	if values, ok := listValue(valueMap["importPaths"]); ok {
		result.ImportPaths = make([]CollectionProtoImportPath, 0, len(values))
		for _, value := range values {
			if valueMap, ok := mapValue(value); ok {
				path := firstYAMLString(valueMap, "path", "directoryPath", "directory", "dir")
				if path == "" {
					continue
				}
				enabled := true
				if parsed, ok := boolValueOK(valueMap["enabled"]); ok {
					enabled = parsed
				} else if disabled, ok := boolValueOK(valueMap["disabled"]); ok {
					enabled = !disabled
				}
				result.ImportPaths = append(result.ImportPaths, CollectionProtoImportPath{
					Path:    path,
					Enabled: enabled,
					Exists:  boolValue(valueMap["exists"], false),
				})
				continue
			}
			if path := strings.TrimSpace(yamlScalarString(value)); path != "" {
				result.ImportPaths = append(result.ImportPaths, CollectionProtoImportPath{Path: path, Enabled: true})
			}
		}
	}
	return result, true
}

func yamlCollectionProtobuf(protobuf CollectionProtobufConfig) map[string]interface{} {
	normalized := normalizeCollectionProtobuf("", protobuf)
	result := map[string]interface{}{}
	if len(normalized.ProtoFiles) > 0 {
		protoFiles := make([]map[string]interface{}, 0, len(normalized.ProtoFiles))
		for _, protoFile := range normalized.ProtoFiles {
			entry := map[string]interface{}{
				"path": protoFile.Path,
				"type": firstNonEmpty(protoFile.Type, "file"),
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

func jsonCollectionProtobuf(protobuf CollectionProtobufConfig) map[string]interface{} {
	return yamlCollectionProtobuf(protobuf)
}

func parseOpenAPISyncConfigs(raw interface{}) []OpenAPISyncConfig {
	values, ok := listValue(raw)
	if !ok {
		if valueMap, mapOK := mapValue(raw); mapOK {
			values = []interface{}{valueMap}
			ok = true
		}
	}
	if !ok {
		return nil
	}
	configs := make([]OpenAPISyncConfig, 0, len(values))
	for _, value := range values {
		valueMap, ok := mapValue(value)
		if !ok {
			continue
		}
		config := OpenAPISyncConfig{
			SourceURL:         firstYAMLString(valueMap, "sourceUrl", "sourceURL", "source_url", "url"),
			GroupBy:           firstYAMLString(valueMap, "groupBy", "group_by"),
			LastSyncDate:      firstYAMLString(valueMap, "lastSyncDate", "last_sync_date"),
			SpecHash:          firstYAMLString(valueMap, "specHash", "spec_hash"),
			AutoCheck:         boolValue(valueMap["autoCheck"], true),
			AutoCheckInterval: intValue(valueMap["autoCheckInterval"], 5),
		}
		config = normalizeOpenAPISyncConfig(config)
		if strings.TrimSpace(config.SourceURL) == "" && strings.TrimSpace(config.SpecHash) == "" {
			continue
		}
		configs = append(configs, config)
	}
	return configs
}

func yamlOpenAPISyncConfigs(configs []OpenAPISyncConfig) []map[string]interface{} {
	result := make([]map[string]interface{}, 0, len(configs))
	for _, config := range configs {
		normalized := normalizeOpenAPISyncConfig(config)
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

func jsonOpenAPISyncConfigs(configs []OpenAPISyncConfig) []map[string]interface{} {
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

func yamlParams(queryParams, pathParams []KeyValue) []map[string]interface{} {
	result := yamlParamsOfType(queryParams, "query")
	result = append(result, yamlParamsOfType(pathParams, "path")...)
	return result
}

func yamlParamsOfType(values []KeyValue, paramType string) []map[string]interface{} {
	result := yamlKeyValues(values)
	for _, value := range result {
		value["type"] = paramType
	}
	return result
}

func yamlMultipart(values []FormPart) []map[string]interface{} {
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

func yamlPostResponseActions(values []Variable) []map[string]interface{} {
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

func yamlAuth(auth AuthConfig) interface{} {
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
			"accessKeyId":     firstNonEmpty(auth.AWSV4.AccessKeyID, auth.AWSV4.AccessKey),
			"secretAccessKey": firstNonEmpty(auth.AWSV4.SecretAccessKey, auth.AWSV4.SecretKey),
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

func parseYAMLKeyValues(raw interface{}, queryOnly bool) []KeyValue {
	return bru.ParseYAMLKeyValues(raw, queryOnly)
}
