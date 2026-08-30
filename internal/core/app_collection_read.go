package core

// Reading a collection back off disk, including its folder metadata.
//
// Split out by AST: declarations are identified by the parser and copied
// verbatim from their source offsets.

import (
	"encoding/json"
	"fmt"
	"github.com/mutexdev/lite_api/internal/types"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/mutexdev/lite_api/internal/scalar"
	"github.com/mutexdev/lite_api/internal/store/bru"
	"github.com/mutexdev/lite_api/internal/store/yamlstore"
	"github.com/mutexdev/lite_api/internal/transport"
)

func readCollectionFromDisk(collectionPath string) (Collection, error) {
	info, err := os.Stat(collectionPath)
	if err != nil {
		return Collection{}, err
	}
	if !info.IsDir() {
		return Collection{}, fmt.Errorf("%s is not a directory", collectionPath)
	}
	configName := filepath.Join(collectionPath, "bruno.json")
	name := filepath.Base(collectionPath)
	rootConfigHasName := false
	version := "1"
	format := "bru"
	if configData, err := os.ReadFile(configName); err == nil {
		var config map[string]interface{}
		if err := json.Unmarshal(configData, &config); err != nil {
			return Collection{}, fmt.Errorf("parse bruno.json: %w", err)
		}
		if configValue, ok := config["name"].(string); ok && strings.TrimSpace(configValue) != "" {
			name = configValue
			rootConfigHasName = true
		}
		if configValue := strings.TrimSpace(yamlScalarString(config["version"])); configValue != "" {
			version = configValue
		}
	}
	openCollectionConfig := filepath.Join(collectionPath, "opencollection.yml")
	if _, err := os.Stat(openCollectionConfig); err != nil {
		openCollectionConfig = filepath.Join(collectionPath, "opencollection.yaml")
	}
	if _, err := os.Stat(openCollectionConfig); err == nil {
		format = "yml"
		if root, err := parseYAMLMapFile(openCollectionConfig); err == nil {
			if value, ok := nestedString(root, "info", "name"); ok {
				name = value
				rootConfigHasName = true
			} else if value, ok := nestedString(root, "info", "title"); ok {
				name = value
				rootConfigHasName = true
			} else if value, ok := root["name"].(string); ok && strings.TrimSpace(value) != "" {
				name = value
				rootConfigHasName = true
			}
		}
	}
	collection := Collection{
		ID:             deterministicID("collection", filepath.Clean(collectionPath)),
		Name:           name,
		Version:        version,
		Path:           filepath.Clean(collectionPath),
		Format:         format,
		Auth:           AuthConfig{Mode: "none"},
		SecurityConfig: CollectionSecurityConfig{JSSandboxMode: "safe"},
		CreatedAt:      info.ModTime(),
		UpdatedAt:      time.Now(),
	}
	ignorePatterns := collectionIgnorePatterns(collectionPath)
	if configData, err := os.ReadFile(configName); err == nil {
		var config map[string]interface{}
		if err := json.Unmarshal(configData, &config); err == nil {
			if proxy, ok := yamlstore.ParseJSONProxyConfig(config["proxy"]); ok {
				collection.Proxy = transport.NormalizeProxyConfig(proxy)
			}
			if certs, ok := yamlstore.ParseJSONClientCertificates(config["clientCertificates"]); ok {
				collection.ClientCertificates = transport.NormalizeClientCertificates(certs)
			}
			if presets, ok := yamlstore.ParseCollectionPresets(config["presets"]); ok {
				collection.Presets = types.NormalizeCollectionPresets(presets)
			}
			if protobuf, ok := yamlstore.ParseCollectionProtobuf(config["protobuf"]); ok {
				collection.Protobuf = types.NormalizeCollectionProtobuf(collection.Path, protobuf)
			}
			if openAPI := yamlstore.ParseOpenAPISyncConfigs(config["openapi"]); len(openAPI) > 0 {
				collection.OpenAPI = openAPI
			}
			// Flows in a bru-format collection live in bruno.json, which is the
			// only root config that format has. The yml side reads them in
			// hydrateYAMLCollectionMetadata.
			if flows := yamlstore.ParseFlows(config["flows"]); len(flows) > 0 {
				collection.Flows = flows
			}
		}
	}
	if format == "yml" {
		if err := hydrateYAMLCollectionMetadata(&collection, openCollectionConfig); err != nil {
			return Collection{}, err
		}
	}
	if rootPath := filepath.Join(collectionPath, "collection.bru"); !collectionPathIgnored(collectionPath, rootPath, ignorePatterns) {
		if content, err := os.ReadFile(rootPath); err == nil {
			rootName := collection.Name
			if err := bru.ParseCollectionMetadata(&collection, string(content)); err != nil {
				return Collection{}, err
			}
			if rootConfigHasName {
				collection.Name = rootName
			}
		}
	}
	if environments, err := readCollectionEnvironments(collectionPath, ignorePatterns); err != nil {
		return Collection{}, err
	} else if len(environments) > 0 {
		collection.Environments = bru.MergeEnvironments(collection.Environments, environments)
	}
	folderMap, folders := readFolderConfigs(collectionPath, ignorePatterns)
	collection.Folders = folders
	err = filepath.WalkDir(collectionPath, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path != collectionPath && collectionPathIgnored(collectionPath, path, ignorePatterns) {
				return filepath.SkipDir
			}
			base := entry.Name()
			if base == "environments" {
				return filepath.SkipDir
			}
			return nil
		}
		if collectionPathIgnored(collectionPath, path, ignorePatterns) {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		base := strings.ToLower(filepath.Base(path))
		if ext == ".bru" && base == "collection.bru" {
			return nil
		}
		if (ext == ".yml" || ext == ".yaml") && base == "opencollection.yml" {
			return nil
		}
		if isFolderMetadataFile(base) {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var item RequestItem
		switch ext {
		case ".bru":
			item, err = bru.Parse(string(content))
			if err != nil {
				return fmt.Errorf("parse %s: %w", path, err)
			}
		case ".yml", ".yaml":
			item, err = yamlstore.ParseRequest(string(content))
			if err != nil {
				return fmt.Errorf("parse %s: %w", path, err)
			}
		default:
			return nil
		}
		if item.Type == "" || item.Type == "http" || item.Type == "graphql" || item.Type == "websocket" || item.Type == "grpc" {
			item.ID = deterministicID("request", filepath.Clean(path))
			item.FilePath = filepath.Clean(path)
			item.FolderPath = folderDisplayPath(collectionPath, path, folderMap)
			assignExampleIDs(&item)
			item.CreatedAt = info.ModTime()
			item.UpdatedAt = info.ModTime()
			collection.Items = append(collection.Items, item)
		}
		return nil
	})
	if err != nil {
		return Collection{}, err
	}
	sort.SliceStable(collection.Items, func(i, j int) bool {
		if collection.Items[i].Seq != collection.Items[j].Seq {
			return collection.Items[i].Seq < collection.Items[j].Seq
		}
		return strings.ToLower(collection.Items[i].Name) < strings.ToLower(collection.Items[j].Name)
	})
	return collection, nil
}

func readFolderConfigs(collectionPath string, ignorePatterns []string) (map[string]FolderConfig, []FolderConfig) {
	folderMap := map[string]FolderConfig{"": {Path: "", DisplayPath: ""}}
	folders := []FolderConfig{}
	_ = filepath.WalkDir(collectionPath, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if !entry.IsDir() {
			return nil
		}
		if path != collectionPath && collectionPathIgnored(collectionPath, path, ignorePatterns) {
			return filepath.SkipDir
		}
		base := entry.Name()
		if base == "environments" {
			return filepath.SkipDir
		}
		rel, err := filepath.Rel(collectionPath, path)
		if err != nil || rel == "." {
			return nil
		}
		rel = filepath.ToSlash(rel)
		parentRel := filepath.ToSlash(filepath.Dir(rel))
		if parentRel == "." {
			parentRel = ""
		}
		config := readFolderConfig(path)
		if config.Name == "" {
			config.Name = filepath.Base(path)
		}
		config.Path = rel
		parentDisplay := folderMap[parentRel].DisplayPath
		if parentDisplay == "" {
			config.DisplayPath = config.Name
		} else {
			config.DisplayPath = parentDisplay + "/" + config.Name
		}
		folderMap[rel] = config
		folders = append(folders, config)
		return nil
	})
	sort.SliceStable(folders, func(i, j int) bool {
		return folders[i].DisplayPath < folders[j].DisplayPath
	})
	return folderMap, folders
}

func readFolderConfig(folderPath string) FolderConfig {
	config := FolderConfig{Auth: AuthConfig{}}
	if content, err := os.ReadFile(filepath.Join(folderPath, "folder.bru")); err == nil {
		temp := Collection{Auth: AuthConfig{}}
		_ = bru.ParseCollectionMetadata(&temp, string(content))
		config.Headers = temp.Headers
		config.Variables = temp.Variables
		config.ResVariables = temp.ResVariables
		config.Auth = temp.Auth
		config.PreScript = temp.PreScript
		config.PostScript = temp.PostScript
		config.Tests = temp.Tests
		config.Docs = temp.Docs
		if meta, ok := bru.ParseSections(string(content))["meta"]; ok {
			values := bru.ParseScalarMap(meta)
			config.Name = strings.TrimSpace(values["name"])
			if seq, err := strconv.Atoi(values["seq"]); err == nil && seq > 0 {
				config.Seq = seq
			}
		}
		return config
	}
	for _, name := range []string{"folder.yml", "folder.yaml"} {
		path := filepath.Join(folderPath, name)
		if root, err := parseYAMLMapFile(path); err == nil {
			if value, ok := nestedString(root, "info", "name"); ok {
				config.Name = value
			} else if value := strings.TrimSpace(yamlScalarString(root["name"])); value != "" {
				config.Name = value
			}
			if info, ok := mapValue(root["info"]); ok {
				config.Seq = intValue(info["seq"], 0)
			}
			applyYAMLFolderDefaults(&config, root)
			return config
		}
	}
	return config
}

func applyYAMLFolderDefaults(config *FolderConfig, root map[string]interface{}) {
	request, _ := mapValue(root["request"])
	if headers, ok := request["headers"]; ok {
		config.Headers = yamlstore.ParseKeyValues(headers, false)
	}
	if variables, ok := request["variables"]; ok {
		config.Variables = yamlstore.ParseVariables(variables)
	}
	config.ResVariables = append(config.ResVariables, yamlstore.ParsePostResponseActions(request["actions"])...)
	if auth, ok := request["auth"]; ok {
		config.Auth = yamlstore.ParseAuth(auth, config.Auth)
	}
	if scripts, ok := listValue(request["scripts"]); ok {
		for _, scriptValue := range scripts {
			script, ok := mapValue(scriptValue)
			if !ok {
				continue
			}
			code := yamlScalarString(script["code"])
			switch strings.ToLower(yamlScalarString(script["type"])) {
			case "before-request", "pre-request":
				config.PreScript = scalar.AppendScript(config.PreScript, code)
			case "after-response", "post-response":
				config.PostScript = scalar.AppendScript(config.PostScript, code)
			case "tests", "test":
				config.Tests = scalar.AppendScript(config.Tests, code)
			}
		}
	}
	if docsMap, ok := mapValue(root["docs"]); ok {
		config.Docs = yamlScalarString(docsMap["content"])
	} else if docs := yamlScalarString(root["docs"]); strings.TrimSpace(docs) != "" {
		config.Docs = docs
	}
}

func folderDisplayPath(collectionPath, requestPath string, folders map[string]FolderConfig) string {
	relDir, err := filepath.Rel(collectionPath, filepath.Dir(requestPath))
	if err != nil || relDir == "." {
		return ""
	}
	relDir = filepath.ToSlash(relDir)
	if config, ok := folders[relDir]; ok && config.DisplayPath != "" {
		return config.DisplayPath
	}
	return relDir
}

func isFolderMetadataFile(base string) bool {
	return base == "folder.bru" || base == "folder.yml" || base == "folder.yaml"
}

func hydrateYAMLCollectionMetadata(collection *Collection, path string) error {
	root, err := parseYAMLMapFile(path)
	if err != nil {
		return err
	}
	request, _ := mapValue(root["request"])
	if version, ok := nestedString(root, "info", "version"); ok && strings.TrimSpace(version) != "" {
		collection.Version = version
	}
	if headers, ok := request["headers"]; ok {
		collection.Headers = yamlstore.ParseKeyValues(headers, false)
	}
	if variables, ok := request["variables"]; ok {
		collection.Variables = yamlstore.ParseVariables(variables)
	}
	collection.ResVariables = append(collection.ResVariables, yamlstore.ParsePostResponseActions(request["actions"])...)
	if auth, ok := request["auth"]; ok {
		collection.Auth = yamlstore.ParseAuth(auth, collection.Auth)
	}
	if scripts, ok := listValue(request["scripts"]); ok {
		for _, scriptValue := range scripts {
			script, ok := mapValue(scriptValue)
			if !ok {
				continue
			}
			code := yamlScalarString(script["code"])
			switch strings.ToLower(yamlScalarString(script["type"])) {
			case "before-request", "pre-request":
				collection.PreScript = scalar.AppendScript(collection.PreScript, code)
			case "after-response", "post-response":
				collection.PostScript = scalar.AppendScript(collection.PostScript, code)
			case "tests", "test":
				collection.Tests = scalar.AppendScript(collection.Tests, code)
			}
		}
	}
	if docsMap, ok := mapValue(root["docs"]); ok {
		collection.Docs = yamlScalarString(docsMap["content"])
	} else if docs := yamlScalarString(root["docs"]); strings.TrimSpace(docs) != "" {
		collection.Docs = docs
	}
	config, _ := mapValue(root["config"])
	if environments, ok := config["environments"]; ok {
		collection.Environments = yamlstore.ParseEnvironments(environments)
	}
	if proxy, ok := yamlstore.ParseProxyConfig(config["proxy"]); ok {
		collection.Proxy = transport.NormalizeProxyConfig(proxy)
	}
	if certs, ok := yamlstore.ParseClientCertificates(config["clientCertificates"]); ok {
		collection.ClientCertificates = transport.NormalizeClientCertificates(certs)
	}
	if presets, ok := yamlstore.ParseCollectionPresets(config["presets"]); ok {
		collection.Presets = types.NormalizeCollectionPresets(presets)
	}
	if protobuf, ok := yamlstore.ParseCollectionProtobuf(config["protobuf"]); ok {
		collection.Protobuf = types.NormalizeCollectionProtobuf(collection.Path, protobuf)
	}
	if flows := yamlstore.ParseFlows(root["flows"]); len(flows) > 0 {
		collection.Flows = flows
	}
	if extensions, ok := mapValue(root["extensions"]); ok {
		if bruno, ok := mapValue(extensions["bruno"]); ok {
			if openAPI := yamlstore.ParseOpenAPISyncConfigs(bruno["openapi"]); len(openAPI) > 0 {
				collection.OpenAPI = openAPI
			}
		}
	}
	return nil
}

func parseYAMLMapFile(path string) (map[string]interface{}, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var raw map[string]interface{}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	return raw, nil
}

func nestedString(raw map[string]interface{}, path ...string) (string, bool) {
	var current interface{} = raw
	for _, key := range path {
		asMap, ok := current.(map[string]interface{})
		if !ok {
			return "", false
		}
		current, ok = asMap[key]
		if !ok {
			return "", false
		}
	}
	value, ok := current.(string)
	return value, ok && strings.TrimSpace(value) != ""
}
