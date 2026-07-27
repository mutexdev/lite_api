package core

import (
	"encoding/json"
	"fmt"
	"github.com/mutexdev/lite_api/internal/store/yamlstore"
	"html"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

func buildCollectionDocsYAML(collection Collection, selectedEnvironmentIDs []string, generatedAt time.Time) (string, int, int, error) {
	root, err := yamlMapFromString(yamlstore.StringifyCollection(collection))
	if err != nil {
		return "", 0, 0, err
	}
	info, _ := mapValue(root["info"])
	if info == nil {
		info = map[string]interface{}{}
	}
	info["name"] = collection.Name
	if strings.TrimSpace(collection.Version) != "" {
		info["version"] = strings.TrimSpace(collection.Version)
	}
	root["info"] = info

	items, folderCount, requestCount, err := collectionDocsItems(collection, "")
	if err != nil {
		return "", 0, 0, err
	}
	root["items"] = items

	config, _ := mapValue(root["config"])
	if config == nil {
		config = map[string]interface{}{}
	}
	config["environments"] = yamlDocsEnvironments(collection.Environments, selectedEnvironmentIDs)
	root["config"] = config

	extensions, _ := mapValue(root["extensions"])
	if extensions == nil {
		extensions = map[string]interface{}{}
	}
	bruno, _ := mapValue(extensions["bruno"])
	if bruno == nil {
		bruno = map[string]interface{}{}
	}
	bruno["exportedAt"] = generatedAt.Format(time.RFC3339)
	bruno["exportedUsing"] = "LiteAPI"
	extensions["bruno"] = bruno
	root["extensions"] = extensions

	data, err := yaml.Marshal(root)
	if err != nil {
		return "", 0, 0, err
	}
	return string(data), folderCount, requestCount, nil
}

func buildCollectionDocsHTML(collectionName, yamlContent string) (string, error) {
	encoded, err := json.Marshal(yamlContent)
	if err != nil {
		return "", err
	}
	escapedYAML := strings.ReplaceAll(string(encoded), `</`, `<\/`)
	title := html.EscapeString(collectionName)
	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>%s - API Documentation</title>
    <style>
        body { margin: 0; padding: 0; }
        #opencollection-container { width: 100vw; height: 100vh; }
    </style>
    <link rel="stylesheet" href="https://cdn.opencollection.com/docs.css">
    <script src="https://cdn.opencollection.com/docs.js"></script>
</head>
<body>
    <div id="opencollection-container"></div>
    <script>
        const collectionData = %s;
        new window.OpenCollection({
            target: document.getElementById('opencollection-container'),
            opencollection: collectionData,
            theme: 'light'
        });
    </script>
</body>
</html>`, title, escapedYAML), nil
}

func yamlMapFromString(content string) (map[string]interface{}, error) {
	var raw map[string]interface{}
	if err := yaml.Unmarshal([]byte(content), &raw); err != nil {
		return nil, err
	}
	if raw == nil {
		raw = map[string]interface{}{}
	}
	return raw, nil
}

func collectionDocsItems(collection Collection, parentPath string) ([]map[string]interface{}, int, int, error) {
	folders := collectionDocsChildFolders(collection.Folders, parentPath)
	requests := collectionDocsChildRequests(collection.Items, parentPath)
	items := make([]map[string]interface{}, 0, len(folders)+len(requests))
	folderCount := 0
	requestCount := 0
	for _, folder := range folders {
		node, err := yamlMapFromString(yamlstore.StringifyFolder(folder))
		if err != nil {
			return nil, 0, 0, err
		}
		children, childFolders, childRequests, err := collectionDocsItems(collection, folder.DisplayPath)
		if err != nil {
			return nil, 0, 0, err
		}
		if len(children) > 0 {
			node["items"] = children
		}
		items = append(items, node)
		folderCount += 1 + childFolders
		requestCount += childRequests
	}
	for _, request := range requests {
		content, err := yamlstore.StringifyRequest(request)
		if err != nil {
			return nil, 0, 0, err
		}
		node, err := yamlMapFromString(content)
		if err != nil {
			return nil, 0, 0, err
		}
		items = append(items, node)
		requestCount++
	}
	return items, folderCount, requestCount, nil
}

func collectionDocsChildFolders(folders []FolderConfig, parentPath string) []FolderConfig {
	parentPath = normalizeFolderPathKey(parentPath)
	children := make([]FolderConfig, 0)
	for _, folder := range folders {
		displayPath := normalizeFolderPathKey(firstNonEmpty(folder.DisplayPath, folder.Path))
		if displayPath == "" {
			continue
		}
		if normalizeFolderPathKey(parentFolderDisplayPath(displayPath)) == parentPath {
			if folder.DisplayPath == "" {
				folder.DisplayPath = displayPath
			}
			children = append(children, folder)
		}
	}
	sortFoldersLikeBruno(children)
	return children
}

func collectionDocsChildRequests(items []RequestItem, parentPath string) []RequestItem {
	parentPath = normalizeFolderPathKey(parentPath)
	children := make([]RequestItem, 0)
	for _, item := range items {
		if item.Transient {
			continue
		}
		if !collectionDocsRequestIsExportable(item) {
			continue
		}
		if normalizeFolderPathKey(item.FolderPath) == parentPath {
			children = append(children, item)
		}
	}
	sort.SliceStable(children, func(i, j int) bool {
		if children[i].Seq != children[j].Seq {
			return children[i].Seq < children[j].Seq
		}
		return strings.ToLower(children[i].Name) < strings.ToLower(children[j].Name)
	})
	return children
}

func collectionDocsRequestIsExportable(item RequestItem) bool {
	return item.Type == "" || item.Type == "http" || item.Type == "graphql" || item.Type == "websocket" || item.Type == "grpc"
}

func sortFoldersLikeBruno(folders []FolderConfig) {
	sort.SliceStable(folders, func(i, j int) bool {
		leftValid := folders[i].Seq > 0
		rightValid := folders[j].Seq > 0
		if leftValid && rightValid && folders[i].Seq != folders[j].Seq {
			return folders[i].Seq < folders[j].Seq
		}
		if leftValid != rightValid {
			return leftValid
		}
		return strings.ToLower(firstNonEmpty(folders[i].Name, folders[i].DisplayPath, folders[i].Path)) < strings.ToLower(firstNonEmpty(folders[j].Name, folders[j].DisplayPath, folders[j].Path))
	})
}

func parentFolderDisplayPath(path string) string {
	path = normalizeFolderPathKey(path)
	parent := filepath.ToSlash(filepath.Dir(path))
	if parent == "." {
		return ""
	}
	return parent
}

func yamlDocsEnvironments(environments []Environment, selectedEnvironmentIDs []string) []map[string]interface{} {
	includeAll := selectedEnvironmentIDs == nil
	selected := map[string]bool{}
	for _, id := range selectedEnvironmentIDs {
		selected[id] = true
	}
	out := make([]map[string]interface{}, 0, len(environments))
	for _, env := range environments {
		if !includeAll && !selected[env.ID] {
			continue
		}
		out = append(out, map[string]interface{}{
			"name":      env.Name,
			"color":     env.Color,
			"variables": yamlVariables(env.Variables),
		})
	}
	return out
}

func collectionDisplayVersion(version string) string {
	version = strings.TrimSpace(strings.TrimPrefix(version, "v"))
	if version == "" {
		return "v1.0.0"
	}
	parts := strings.SplitN(version, "-", 2)
	numbers := strings.Split(parts[0], ".")
	for len(numbers) < 3 {
		numbers = append(numbers, "0")
	}
	for index := range numbers[:3] {
		if _, err := strconv.Atoi(numbers[index]); err != nil {
			return "v1.0.0"
		}
	}
	normalized := strings.Join(numbers[:3], ".")
	if len(parts) == 2 && strings.TrimSpace(parts[1]) != "" {
		normalized += "-" + strings.TrimSpace(parts[1])
	}
	return "v" + normalized
}
