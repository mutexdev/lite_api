package core

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/mutexdev/lite_api/internal/scripting"
	"github.com/mutexdev/lite_api/internal/store/bru"
	"github.com/mutexdev/lite_api/internal/transport"
)

func collectionShareSnapshot(collection Collection) Collection {
	snapshot := collection
	snapshot.Remote = ""
	snapshot.NotFoundLocally = false
	snapshot.RuntimeVariables = nil
	snapshot.Items = make([]RequestItem, 0, len(collection.Items))
	for _, item := range collection.Items {
		if item.Transient {
			continue
		}
		item.Response = nil
		item.Timeline = nil
		item.Draft = false
		item.Transient = false
		item.FilePath = ""
		snapshot.Items = append(snapshot.Items, item)
	}
	snapshot.Folders = append([]FolderConfig(nil), collection.Folders...)
	snapshot.Environments = make([]Environment, 0, len(collection.Environments))
	for _, env := range collection.Environments {
		env.Variables = append([]Variable(nil), env.Variables...)
		for index := range env.Variables {
			if env.Variables[index].Secret {
				env.Variables[index].Value = ""
			}
		}
		snapshot.Environments = append(snapshot.Environments, env)
	}
	return snapshot
}

func buildCollectionShareYAML(collection Collection, generatedAt time.Time) (string, int, int, error) {
	content, folderCount, requestCount, err := buildCollectionDocsYAML(collection, nil, generatedAt)
	if err != nil {
		return "", 0, 0, err
	}
	root, err := yamlMapFromString(content)
	if err != nil {
		return "", 0, 0, err
	}
	root["bundled"] = true
	data, err := yaml.Marshal(root)
	if err != nil {
		return "", 0, 0, err
	}
	return string(data), folderCount, requestCount, nil
}

func buildCollectionZipExportFiles(collection Collection) ([]collectionExportFile, int, int, error) {
	files := []collectionExportFile{}
	used := map[string]bool{}
	exportRoot := filepath.Join(os.TempDir(), "liteapi-export-root")
	collection.Path = exportRoot
	format := strings.ToLower(strings.TrimSpace(collection.Format))
	if format == "" {
		format = "yml"
	}
	if format == "yml" || format == "yaml" {
		root, err := yamlMapFromString(stringifyYAMLCollection(collection))
		if err != nil {
			return nil, 0, 0, err
		}
		root["bundled"] = false
		extensions, _ := mapValue(root["extensions"])
		if extensions == nil {
			extensions = map[string]interface{}{}
		}
		bruno, _ := mapValue(extensions["bruno"])
		if bruno == nil {
			bruno = map[string]interface{}{}
		}
		bruno["exportedAt"] = time.Now().UTC().Format(time.RFC3339)
		bruno["exportedUsing"] = "LiteAPI"
		extensions["bruno"] = bruno
		root["extensions"] = extensions
		data, err := yaml.Marshal(root)
		if err != nil {
			return nil, 0, 0, err
		}
		addCollectionExportFile(&files, used, "opencollection.yml", data)
		for _, folder := range collection.Folders {
			if folderPath := exportFolderPath(folder); folderPath != "" {
				addCollectionExportFile(&files, used, filepath.ToSlash(filepath.Join(filepath.FromSlash(folderPath), "folder.yml")), []byte(stringifyYAMLFolder(folder)))
			}
		}
		ensureRequestFilePaths(&collection, ".yml")
		for _, item := range collection.Items {
			content, err := stringifyYAMLRequest(item)
			if err != nil {
				return nil, 0, 0, err
			}
			rel, ok := exportRelativePath(collection.Path, item.FilePath)
			if !ok {
				continue
			}
			addCollectionExportFile(&files, used, rel, []byte(content))
		}
		return files, len(collection.Folders), len(collection.Items), nil
	}

	config := map[string]interface{}{
		"name":    collection.Name,
		"type":    "collection",
		"version": firstNonEmpty(collection.Version, "1"),
		"ignore":  []string{"node_modules", ".git"},
	}
	if transport.HasProxyConfig(collection.Proxy) {
		config["proxy"] = jsonProxyConfig(collection.Proxy)
	}
	if transport.HasClientCertificates(collection.ClientCertificates) {
		config["clientCertificates"] = jsonClientCertificates(collection.ClientCertificates)
	}
	if hasCollectionPresets(collection.Presets) {
		config["presets"] = jsonCollectionPresets(collection.Presets)
	}
	if hasCollectionProtobuf(collection.Protobuf) {
		config["protobuf"] = jsonCollectionProtobuf(collection.Protobuf)
	}
	if len(collection.OpenAPI) > 0 {
		config["openapi"] = jsonOpenAPISyncConfigs(collection.OpenAPI)
	}
	configData, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return nil, 0, 0, err
	}
	addCollectionExportFile(&files, used, "bruno.json", configData)
	addCollectionExportFile(&files, used, "collection.bru", []byte(bru.StringifyBruCollection(collection)))
	for _, env := range collection.Environments {
		name := sanitizeFilename(env.Name)
		if name == "" {
			name = env.ID
		}
		addCollectionExportFile(&files, used, filepath.ToSlash(filepath.Join("environments", name+".bru")), []byte(bru.StringifyBruEnvironment(env)))
	}
	for _, folder := range collection.Folders {
		if folderPath := exportFolderPath(folder); folderPath != "" {
			addCollectionExportFile(&files, used, filepath.ToSlash(filepath.Join(filepath.FromSlash(folderPath), "folder.bru")), []byte(bru.StringifyBruFolder(folder)))
		}
	}
	ensureRequestFilePaths(&collection, ".bru")
	for _, item := range collection.Items {
		rel, ok := exportRelativePath(collection.Path, item.FilePath)
		if !ok {
			continue
		}
		addCollectionExportFile(&files, used, rel, []byte(bru.StringifyBru(item)))
	}
	return files, len(collection.Folders), len(collection.Items), nil
}

func addCollectionExportFile(files *[]collectionExportFile, used map[string]bool, name string, content []byte) {
	name = cleanExportArchivePath(name)
	if name == "" {
		return
	}
	name = uniqueCollectionExportPath(name, used)
	used[name] = true
	*files = append(*files, collectionExportFile{Name: name, Content: append([]byte(nil), content...)})
}

func cleanExportArchivePath(name string) string {
	name = filepath.ToSlash(filepath.Clean(filepath.FromSlash(strings.TrimSpace(name))))
	if name == "." || name == "" || strings.HasPrefix(name, "../") || name == ".." || strings.HasPrefix(name, "/") || filepath.IsAbs(name) {
		return ""
	}
	parts := strings.Split(name, "/")
	for _, part := range parts {
		if part == ".git" || part == "node_modules" {
			return ""
		}
	}
	return name
}

func uniqueCollectionExportPath(name string, used map[string]bool) string {
	if !used[name] {
		return name
	}
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	for index := 2; ; index++ {
		candidate := fmt.Sprintf("%s %d%s", base, index, ext)
		if !used[candidate] {
			return candidate
		}
	}
}

func exportFolderPath(folder FolderConfig) string {
	folderPath := normalizeFolderPathKey(firstNonEmpty(folder.DisplayPath, folder.Path))
	if folderPath == "" {
		return ""
	}
	return cleanExportArchivePath(folderPath)
}

func exportRelativePath(root, target string) (string, bool) {
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(target))
	if err != nil {
		return "", false
	}
	clean := cleanExportArchivePath(rel)
	return clean, clean != ""
}

func zipCollectionExportFiles(files []collectionExportFile) ([]byte, error) {
	var buffer bytes.Buffer
	archive := zip.NewWriter(&buffer)
	for _, file := range files {
		name := cleanExportArchivePath(file.Name)
		if name == "" {
			continue
		}
		header := &zip.FileHeader{Name: name, Method: zip.Deflate}
		header.SetMode(0o600)
		writer, err := archive.CreateHeader(header)
		if err != nil {
			_ = archive.Close()
			return nil, err
		}
		if _, err := writer.Write(file.Content); err != nil {
			_ = archive.Close()
			return nil, err
		}
	}
	if err := archive.Close(); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

func collectionExportBytes(result CollectionExportResult) ([]byte, error) {
	if strings.TrimSpace(result.ContentBase64) != "" {
		return base64.StdEncoding.DecodeString(result.ContentBase64)
	}
	return []byte(result.Content), nil
}

func buildPostmanCollectionExport(collection Collection) (string, int, []string, error) {
	skipped := []string{}
	skippedSeen := map[string]bool{}
	items, count := postmanCollectionItems(collection, "", &skipped, skippedSeen)
	payload := map[string]interface{}{
		"info": map[string]interface{}{
			"name":   collection.Name,
			"schema": "https://schema.getpostman.com/json/collection/v2.1.0/collection.json",
		},
		"item": items,
	}
	// US-053. Collection-level state was previously dropped entirely, so an
	// export round trip silently lost every collection variable, the
	// collection auth every request inherits, and the collection scripts that
	// run before each one. The result imported cleanly and behaved differently.
	if events := sharePostmanEvents(collection.PreScript, collection.PostScript, ""); len(events) > 0 {
		payload["event"] = events
	}
	if auth := sharePostmanAuth(collection.Auth); auth != nil {
		payload["auth"] = auth
	}
	if variables := sharePostmanVariables(collection.Variables); len(variables) > 0 {
		payload["variable"] = variables
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return "", 0, nil, err
	}
	return string(data), count, skipped, nil
}

func postmanCollectionItems(collection Collection, parentPath string, skipped *[]string, skippedSeen map[string]bool) ([]interface{}, int) {
	out := []interface{}{}
	count := 0
	for _, folder := range collectionDocsChildFolders(collection.Folders, parentPath) {
		children, childCount := postmanCollectionItems(collection, folder.DisplayPath, skipped, skippedSeen)
		entry := map[string]interface{}{
			"name": firstNonEmpty(folder.Name, filepath.Base(filepath.FromSlash(folder.DisplayPath)), filepath.Base(filepath.FromSlash(folder.Path))),
			"item": children,
		}
		if events := sharePostmanEvents(folder.PreScript, folder.PostScript, ""); len(events) > 0 {
			entry["event"] = events
		}
		if auth := sharePostmanAuth(folder.Auth); auth != nil {
			entry["auth"] = auth
		}
		out = append(out, entry)
		count += childCount
	}
	for _, item := range collectionDocsChildRequests(collection.Items, parentPath) {
		switch item.Type {
		case "", "http", "graphql":
			out = append(out, sharePostmanRequestItem(item))
			count++
		case "grpc":
			addSkippedCollectionExportType(skipped, skippedSeen, "gRPC")
		case "websocket":
			addSkippedCollectionExportType(skipped, skippedSeen, "WebSocket")
		}
	}
	return out, count
}

func addSkippedCollectionExportType(skipped *[]string, seen map[string]bool, label string) {
	if seen[label] {
		return
	}
	seen[label] = true
	*skipped = append(*skipped, label)
}

func sharePostmanRequestItem(item RequestItem) map[string]interface{} {
	method := strings.ToUpper(firstNonEmpty(item.Method, http.MethodGet))
	request := map[string]interface{}{
		"method": method,
		"url":    sharePostmanURL(item),
	}
	if headers := sharePostmanKeyValues(item.Headers, "key"); len(headers) > 0 {
		request["header"] = headers
	}
	if body := sharePostmanBody(item); body != nil {
		request["body"] = body
	}
	if auth := sharePostmanAuth(item.Auth); auth != nil {
		request["auth"] = auth
	}
	if description := strings.TrimSpace(item.Docs); description != "" {
		request["description"] = description
	}
	entry := map[string]interface{}{
		"name":    item.Name,
		"request": request,
	}
	if events := sharePostmanEvents(item.PreScript, item.PostScript, item.Tests); len(events) > 0 {
		entry["event"] = events
	}
	return entry
}

// sharePostmanEvents builds the event blocks a Postman collection carries.
//
// Postman has TWO events, prerequest and test, while this model has THREE
// script slots: PreScript, PostScript and Tests. PostScript and Tests are
// therefore joined into the single test event.
//
// That merge is lossless for the collections that matter here. The importer
// maps Postman's test event onto PostScript and never populates Tests, so a
// Postman-origin collection round-trips byte-for-byte. A natively authored
// collection using both slots collapses them into one on the way out — and
// once collapsed it stays collapsed, which is exactly what makes
// import -> export -> import idempotent rather than drifting on every cycle.
func sharePostmanEvents(preScript, postScript, tests string) []interface{} {
	var events []interface{}
	add := func(listen, script string) {
		if strings.TrimSpace(script) == "" {
			return
		}
		events = append(events, map[string]interface{}{
			"listen": listen,
			"script": map[string]interface{}{
				"type": "text/javascript",
				// exec is a line array, which is how Postman writes it. A
				// single string is accepted by most readers but diffs as one
				// enormous line, making an exported collection unreviewable in
				// version control.
				"exec": strings.Split(script, "\n"),
			},
		})
	}
	add("prerequest", preScript)

	post := strings.TrimRight(postScript, "\n")
	if strings.TrimSpace(tests) != "" {
		if strings.TrimSpace(post) != "" {
			post += "\n"
		}
		post += tests
	}
	add("test", post)
	return events
}

// sharePostmanVariables exports collection variables.
func sharePostmanVariables(variables []Variable) []interface{} {
	out := make([]interface{}, 0, len(variables))
	for _, variable := range variables {
		name := strings.TrimSpace(variable.Name)
		if name == "" {
			continue
		}
		entry := map[string]interface{}{
			"key":   name,
			"value": scripting.ScriptVariableString(variable.Value),
		}
		if !variable.Enabled {
			entry["disabled"] = true
		}
		out = append(out, entry)
	}
	return out
}

func sharePostmanURL(item RequestItem) map[string]interface{} {
	url := map[string]interface{}{"raw": item.URL}
	if len(item.Params) > 0 {
		query := []map[string]interface{}{}
		for _, param := range item.Params {
			if strings.TrimSpace(param.Name) == "" {
				continue
			}
			entry := map[string]interface{}{"key": param.Name, "value": param.Value}
			if !param.Enabled {
				entry["disabled"] = true
			}
			query = append(query, entry)
		}
		if len(query) > 0 {
			url["query"] = query
		}
	}
	// US-053. Path params are a separate Postman key from query params, and
	// omitting them meant a :id placeholder round-tripped with no value — the
	// request imported looking complete and sent a literal ":id" to the server.
	if len(item.PathParams) > 0 {
		variables := []map[string]interface{}{}
		for _, param := range item.PathParams {
			if strings.TrimSpace(param.Name) == "" {
				continue
			}
			variables = append(variables, map[string]interface{}{"key": param.Name, "value": param.Value})
		}
		if len(variables) > 0 {
			url["variable"] = variables
		}
	}
	return url
}

func sharePostmanBody(item RequestItem) map[string]interface{} {
	if item.Type == "graphql" {
		return map[string]interface{}{
			"mode": "graphql",
			"graphql": map[string]interface{}{
				"query":     item.Body.GraphQLQuery,
				"variables": item.Body.GraphQLVariables,
			},
		}
	}
	switch item.Body.Mode {
	case "json":
		return sharePostmanRawBody(item.Body.JSON, "json")
	case "xml":
		return sharePostmanRawBody(item.Body.XML, "xml")
	case "text", "sparql":
		return sharePostmanRawBody(item.Body.Text, "text")
	case "formUrlEncoded":
		return map[string]interface{}{"mode": "urlencoded", "urlencoded": sharePostmanKeyValues(item.Body.FormURLEncoded, "key")}
	case "multipartForm":
		return map[string]interface{}{"mode": "formdata", "formdata": sharePostmanFormData(item.Body.Multipart)}
	case "file":
		if file := shareSelectedFileBodyEntry(item.Body); file != nil {
			return map[string]interface{}{"mode": "file", "file": map[string]interface{}{"src": file.FilePath}}
		}
	}
	return nil
}

func sharePostmanRawBody(body, language string) map[string]interface{} {
	if strings.TrimSpace(body) == "" {
		return nil
	}
	return map[string]interface{}{
		"mode": "raw",
		"raw":  body,
		"options": map[string]interface{}{
			"raw": map[string]interface{}{"language": language},
		},
	}
}

func sharePostmanKeyValues(values []KeyValue, keyName string) []map[string]interface{} {
	out := []map[string]interface{}{}
	for _, value := range values {
		if strings.TrimSpace(value.Name) == "" {
			continue
		}
		entry := map[string]interface{}{keyName: value.Name, "value": value.Value}
		if !value.Enabled {
			entry["disabled"] = true
		}
		out = append(out, entry)
	}
	return out
}

func sharePostmanFormData(values []FormPart) []map[string]interface{} {
	out := []map[string]interface{}{}
	for _, value := range values {
		if strings.TrimSpace(value.Name) == "" {
			continue
		}
		entry := map[string]interface{}{
			"key":   value.Name,
			"value": value.Value,
			"type":  "text",
		}
		if strings.TrimSpace(value.FilePath) != "" {
			entry["type"] = "file"
			entry["src"] = value.FilePath
			delete(entry, "value")
		}
		if !value.Enabled {
			entry["disabled"] = true
		}
		out = append(out, entry)
	}
	return out
}

func shareSelectedFileBodyEntry(body RequestBody) *FileBodyEntry {
	for _, file := range fileBodyEntries(body) {
		if file.Selected && strings.TrimSpace(file.FilePath) != "" {
			copy := file
			return &copy
		}
	}
	return nil
}

func sharePostmanAuth(auth AuthConfig) map[string]interface{} {
	switch strings.ToLower(strings.TrimSpace(auth.Mode)) {
	case "basic":
		return map[string]interface{}{"type": "basic", "basic": []map[string]interface{}{
			{"key": "username", "value": auth.Username, "type": "string"},
			{"key": "password", "value": auth.Password, "type": "string"},
		}}
	case "bearer":
		return map[string]interface{}{"type": "bearer", "bearer": []map[string]interface{}{
			{"key": "token", "value": auth.Token, "type": "string"},
		}}
	case "apikey":
		return map[string]interface{}{"type": "apikey", "apikey": []map[string]interface{}{
			{"key": "key", "value": auth.APIKey, "type": "string"},
			{"key": "value", "value": auth.APIValue, "type": "string"},
			{"key": "in", "value": firstNonEmpty(auth.APILocation, "header"), "type": "string"},
		}}
	default:
		return nil
	}
}
