package yamlstore

// The paired readers and writers for a collection settings block, in both YAML and the JSON the Bruno format uses.
//
// Split out by AST: declarations are identified by the parser and copied
// verbatim from their source offsets.

import (
	"strconv"
	"strings"

	"github.com/mutexdev/lite_api/internal/openapisync"
	"github.com/mutexdev/lite_api/internal/scalar"
	"github.com/mutexdev/lite_api/internal/transport"
	"github.com/mutexdev/lite_api/internal/types"
)

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
