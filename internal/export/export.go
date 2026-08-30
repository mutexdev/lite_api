package export

import (
	"archive/zip"
	"bytes"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/mutexdev/lite_api/internal/scalar"
	"github.com/mutexdev/lite_api/internal/store/yamlstore"
	"github.com/mutexdev/lite_api/internal/types"
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

// File is one entry of an exported collection: a path inside the archive and
// its bytes.
type File struct {
	Name    string
	Content []byte
}

func ShareSnapshot(collection types.Collection) types.Collection {
	snapshot := collection
	snapshot.Remote = ""
	snapshot.NotFoundLocally = false
	snapshot.RuntimeVariables = nil
	snapshot.Items = make([]types.RequestItem, 0, len(collection.Items))
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
	snapshot.Folders = append([]types.FolderConfig(nil), collection.Folders...)
	snapshot.Environments = make([]types.Environment, 0, len(collection.Environments))
	for _, env := range collection.Environments {
		env.Variables = append([]types.Variable(nil), env.Variables...)
		for index := range env.Variables {
			if env.Variables[index].Secret {
				env.Variables[index].Value = ""
			}
		}
		snapshot.Environments = append(snapshot.Environments, env)
	}
	return snapshot
}

func BuildShareYAML(collection types.Collection, generatedAt time.Time) (string, int, int, error) {
	content, folderCount, requestCount, err := BuildDocsYAML(collection, nil, generatedAt)
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

func BuildZipFiles(collection types.Collection) ([]File, int, int, error) {
	files := []File{}
	used := map[string]bool{}
	exportRoot := filepath.Join(os.TempDir(), "liteapi-export-root")
	collection.Path = exportRoot
	format := strings.ToLower(strings.TrimSpace(collection.Format))
	if format == "" {
		format = "yml"
	}
	if format == "yml" || format == "yaml" {
		root, err := yamlMapFromString(yamlstore.StringifyCollection(collection))
		if err != nil {
			return nil, 0, 0, err
		}
		root["bundled"] = false
		extensions, _ := scalar.Map(root["extensions"])
		if extensions == nil {
			extensions = map[string]interface{}{}
		}
		bruno, _ := scalar.Map(extensions["bruno"])
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
				addCollectionExportFile(&files, used, filepath.ToSlash(filepath.Join(filepath.FromSlash(folderPath), "folder.yml")), []byte(yamlstore.StringifyFolder(folder)))
			}
		}
		types.EnsureRequestFilePaths(&collection, ".yml")
		for _, item := range collection.Items {
			content, err := yamlstore.StringifyRequest(item)
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
		"version": scalar.FirstNonEmpty(collection.Version, "1"),
		"ignore":  []string{"node_modules", ".git"},
	}
	if transport.HasProxyConfig(collection.Proxy) {
		config["proxy"] = yamlstore.JSONProxyConfig(collection.Proxy)
	}
	if transport.HasClientCertificates(collection.ClientCertificates) {
		config["clientCertificates"] = yamlstore.JSONClientCertificates(collection.ClientCertificates)
	}
	if types.HasCollectionPresets(collection.Presets) {
		config["presets"] = yamlstore.JSONCollectionPresets(collection.Presets)
	}
	if types.HasCollectionProtobuf(collection.Protobuf) {
		config["protobuf"] = yamlstore.JSONCollectionProtobuf(collection.Protobuf)
	}
	if len(collection.OpenAPI) > 0 {
		config["openapi"] = yamlstore.JSONOpenAPISyncConfigs(collection.OpenAPI)
	}
	// Flows travel with a LiteAPI export in both formats — the yml branch above
	// gets them through StringifyCollection, and bruno.json is where the bru
	// format keeps them. A zip is a copy of the collection, so leaving them out
	// would mean an export that re-imports as a collection missing work.
	if len(collection.Flows) > 0 {
		config["flows"] = yamlstore.JSONFlows(collection.Flows)
	}
	configData, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return nil, 0, 0, err
	}
	addCollectionExportFile(&files, used, "bruno.json", configData)
	addCollectionExportFile(&files, used, "collection.bru", []byte(bru.StringifyBruCollection(collection)))
	for _, env := range collection.Environments {
		name := scalar.SanitizeFilename(env.Name)
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
	types.EnsureRequestFilePaths(&collection, ".bru")
	for _, item := range collection.Items {
		rel, ok := exportRelativePath(collection.Path, item.FilePath)
		if !ok {
			continue
		}
		addCollectionExportFile(&files, used, rel, []byte(bru.StringifyBru(item)))
	}
	return files, len(collection.Folders), len(collection.Items), nil
}

func addCollectionExportFile(files *[]File, used map[string]bool, name string, content []byte) {
	name = cleanExportArchivePath(name)
	if name == "" {
		return
	}
	name = uniqueCollectionExportPath(name, used)
	used[name] = true
	*files = append(*files, File{Name: name, Content: append([]byte(nil), content...)})
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

func exportFolderPath(folder types.FolderConfig) string {
	folderPath := types.NormalizeFolderPathKey(scalar.FirstNonEmpty(folder.DisplayPath, folder.Path))
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

func ZipFiles(files []File) ([]byte, error) {
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

func Bytes(result types.CollectionExportResult) ([]byte, error) {
	if strings.TrimSpace(result.ContentBase64) != "" {
		return base64.StdEncoding.DecodeString(result.ContentBase64)
	}
	return []byte(result.Content), nil
}

// PostmanExport is what a Postman export produced: the document, what it
// contains, and what it could not carry.
type PostmanExport struct {
	Content      string
	FolderCount  int
	RequestCount int
	SkippedTypes []string
	Warnings     []string
}

// postmanExportState collects everything the export cannot represent. Two
// lists, because they read differently to a user: SkippedTypes names request
// kinds Postman has no concept of, Warnings names content that was dropped or
// rewritten inside requests that DID export.
type postmanExportState struct {
	skipped     []string
	skippedSeen map[string]bool
	warnings    []string
	warningSeen map[string]bool
}

func newPostmanExportState() *postmanExportState {
	return &postmanExportState{
		skipped:     []string{},
		skippedSeen: map[string]bool{},
		warningSeen: map[string]bool{},
	}
}

func (s *postmanExportState) skip(label string) {
	if label == "" || s.skippedSeen[label] {
		return
	}
	s.skippedSeen[label] = true
	s.skipped = append(s.skipped, label)
}

func (s *postmanExportState) warn(message string) {
	if message == "" || s.warningSeen[message] {
		return
	}
	s.warningSeen[message] = true
	s.warnings = append(s.warnings, message)
}

// BuildPostmanCollection is the narrow form kept for callers that only need the
// document and the skipped request kinds.
func BuildPostmanCollection(collection types.Collection) (string, int, []string, error) {
	result, err := BuildPostmanExport(collection)
	if err != nil {
		return "", 0, nil, err
	}
	return result.Content, result.RequestCount, result.SkippedTypes, nil
}

// BuildPostmanExport renders a collection as a Postman v2.1 collection.
//
// FLOWS ARE DELIBERATELY NOT CARRIED. collection.Flows is LiteAPI-native and
// has no counterpart in the Postman schema: there is no shape to put a step
// chain, its extractions or its assertions into that Postman would read back,
// and inventing one under a `_liteapi` key would produce a file that neither
// tool round-trips. A LiteAPI zip export (BuildZipFiles) is the export that
// keeps them. This comment exists because everything else on the collection is
// mapped here, so their absence would otherwise read as an oversight.
func BuildPostmanExport(collection types.Collection) (PostmanExport, error) {
	state := newPostmanExportState()
	items, folderCount, requestCount := postmanCollectionItems(collection, state)
	info := map[string]interface{}{
		"name":   collection.Name,
		"schema": "https://schema.getpostman.com/json/collection/v2.1.0/collection.json",
		// Postman writes an id on every collection and uses it to recognise a
		// re-import as the same collection. It is derived rather than random so
		// re-exporting an unchanged collection produces an unchanged file.
		"_postman_id": postmanCollectionIdentifier(collection),
	}
	if description := strings.TrimSpace(collection.Docs); description != "" {
		info["description"] = description
	}
	payload := map[string]interface{}{
		"info": info,
		"item": items,
	}
	// US-053. types.Collection-level state was previously dropped entirely, so an
	// export round trip silently lost every collection variable, the
	// collection auth every request inherits, and the collection scripts that
	// run before each one. The result imported cleanly and behaved differently.
	if events := sharePostmanEvents(state, collection.PreScript, collection.PostScript, collection.Tests); len(events) > 0 {
		payload["event"] = events
	}
	if auth := sharePostmanAuth(collection.Auth); auth != nil {
		payload["auth"] = auth
	}
	if variables := sharePostmanVariables(collection.Variables); len(variables) > 0 {
		payload["variable"] = variables
	}
	// Postman has no collection-level or folder-level header. Merging them into
	// each request would change what those requests send on re-import, so they
	// are reported instead of silently folded in or silently dropped.
	if len(collection.Headers) > 0 {
		state.warn("Collection headers were not exported: Postman has no collection-level headers.")
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return PostmanExport{}, err
	}
	return PostmanExport{
		Content:      string(data),
		FolderCount:  folderCount,
		RequestCount: requestCount,
		SkippedTypes: state.skipped,
		Warnings:     state.warnings,
	}, nil
}

// postmanCollectionIdentifier derives a stable UUID from the collection's
// identity, so the same collection exports with the same _postman_id every time.
func postmanCollectionIdentifier(collection types.Collection) string {
	seed := scalar.FirstNonEmpty(strings.TrimSpace(collection.ID), strings.TrimSpace(collection.Name), "liteapi")
	sum := sha1.Sum([]byte("liteapi-postman-collection:" + seed))
	var uuid [16]byte
	copy(uuid[:], sum[:16])
	uuid[6] = (uuid[6] & 0x0f) | 0x50
	uuid[8] = (uuid[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", uuid[0:4], uuid[4:6], uuid[6:8], uuid[8:10], uuid[10:16])
}

func postmanCollectionItems(collection types.Collection, state *postmanExportState) ([]interface{}, int, int) {
	tree := buildPostmanFolderTree(collection)
	items, count := postmanFolderNodeItems(tree.root, state)
	return items, tree.count(), count
}

func postmanFolderNodeItems(node *postmanFolderNode, state *postmanExportState) ([]interface{}, int) {
	out := []interface{}{}
	count := 0
	for _, child := range node.folders {
		children, childCount := postmanFolderNodeItems(child, state)
		entry := map[string]interface{}{
			"name": child.label(),
			"item": children,
		}
		if child.config != nil {
			folder := *child.config
			if events := sharePostmanEvents(state, folder.PreScript, folder.PostScript, folder.Tests); len(events) > 0 {
				entry["event"] = events
			}
			if auth := sharePostmanAuth(folder.Auth); auth != nil {
				entry["auth"] = auth
			}
			if description := strings.TrimSpace(folder.Docs); description != "" {
				entry["description"] = description
			}
			if len(folder.Headers) > 0 {
				state.warn("Folder headers were not exported: Postman has no folder-level headers.")
			}
		}
		out = append(out, entry)
		count += childCount
	}
	for _, item := range node.items {
		switch item.Type {
		case "", "http", "graphql":
			out = append(out, sharePostmanRequestItem(item, state))
			count++
		case "grpc":
			state.skip("gRPC")
		case "websocket":
			state.skip("WebSocket")
		default:
			state.skip(item.Type)
		}
	}
	return out, count
}

func sharePostmanRequestItem(item types.RequestItem, state *postmanExportState) map[string]interface{} {
	method := strings.ToUpper(scalar.FirstNonEmpty(item.Method, http.MethodGet))
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
		// Per-request transport settings. Without these a request that opted
		// out of TLS verification, or out of redirects, came back on re-import
		// with the defaults and behaved differently with nothing saying so.
		"protocolProfileBehavior": sharePostmanProtocolProfileBehavior(item.Settings),
	}
	if events := sharePostmanEvents(state, item.PreScript, item.PostScript, item.Tests); len(events) > 0 {
		entry["event"] = events
	}
	if responses := sharePostmanResponses(item); len(responses) > 0 {
		entry["response"] = responses
	}
	return entry
}

func sharePostmanProtocolProfileBehavior(settings types.RequestSettings) map[string]interface{} {
	return map[string]interface{}{
		"strictSSL":       settings.VerifyTLS,
		"followRedirects": settings.FollowRedirects,
		"maxRedirects":    settings.MaxRedirects,
	}
}

// sharePostmanResponses exports saved response examples. The importer builds
// them from Postman's response array, so an export that wrote none destroyed
// every example on a round trip.
func sharePostmanResponses(item types.RequestItem) []interface{} {
	out := make([]interface{}, 0, len(item.Examples))
	for _, example := range item.Examples {
		entry := map[string]interface{}{
			"name":            example.Name,
			"originalRequest": sharePostmanExampleRequest(item, example),
			"status":          example.Response.StatusText,
			"code":            example.Response.Status,
			"header":          sharePostmanKeyValues(example.Response.Headers, "key"),
			"body":            example.Response.Body,
		}
		if language := sharePostmanPreviewLanguage(example.Response.BodyType); language != "" {
			entry["_postman_previewlanguage"] = language
		}
		out = append(out, entry)
	}
	return out
}

func sharePostmanExampleRequest(item types.RequestItem, example types.ResponseExample) map[string]interface{} {
	url := map[string]interface{}{"raw": scalar.FirstNonEmpty(example.Request.URL, item.URL)}
	if query := sharePostmanKeyValues(example.Request.Params, "key"); len(query) > 0 {
		url["query"] = query
	}
	request := map[string]interface{}{
		"method": strings.ToUpper(scalar.FirstNonEmpty(example.Request.Method, item.Method, http.MethodGet)),
		"url":    url,
	}
	if headers := sharePostmanKeyValues(example.Request.Headers, "key"); len(headers) > 0 {
		request["header"] = headers
	}
	if body := sharePostmanBody(item); body != nil {
		request["body"] = body
	}
	return request
}

func sharePostmanPreviewLanguage(bodyType string) string {
	switch strings.ToLower(strings.TrimSpace(bodyType)) {
	case "json":
		return "json"
	case "xml":
		return "xml"
	case "html":
		return "html"
	case "":
		return ""
	default:
		return "text"
	}
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
//
// The tests slot also accepts the legacy plain-English assertion DSL, which is
// a syntax error in Postman's JavaScript-only event. It is translated on the
// way out; see postman_assertions.go.
func sharePostmanEvents(state *postmanExportState, preScript, postScript, tests string) []interface{} {
	var events []interface{}
	warn := func(string) {}
	if state != nil {
		warn = state.warn
	}
	tests = translatePostmanTests(tests, warn)
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
func sharePostmanVariables(variables []types.Variable) []interface{} {
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

func sharePostmanURL(item types.RequestItem) map[string]interface{} {
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

func sharePostmanBody(item types.RequestItem) map[string]interface{} {
	// A GraphQL body is reachable two ways, and this used to honour only one.
	//
	// `item.Type == "graphql"` is what the Postman IMPORTER sets, so imported
	// requests exported fine. But the Body mode dropdown sets `body.mode` and
	// leaves the type alone, so a GraphQL body added to an ordinary HTTP request
	// — type "http", mode "graphql" — fell past this check into the switch
	// below, which had no graphql case, and returned nil. The export was a valid
	// collection with `"body": null`: the query and variables simply gone, with
	// nothing anywhere saying so.
	//
	// Everything else in the app keys off body.Mode — buildBody sends it, the
	// .bru store persists it — so the mode is the authority here too, with the
	// type folded in the way store/bru does it, for a graphql-typed request
	// whose mode was never set.
	mode := item.Body.Mode
	if item.Type == "graphql" {
		mode = "graphql"
	}
	switch mode {
	case "graphql":
		return map[string]interface{}{
			"mode": "graphql",
			"graphql": map[string]interface{}{
				"query":     item.Body.GraphQLQuery,
				"variables": item.Body.GraphQLVariables,
			},
		}
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

func sharePostmanKeyValues(values []types.KeyValue, keyName string) []map[string]interface{} {
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

func sharePostmanFormData(values []types.FormPart) []map[string]interface{} {
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

func shareSelectedFileBodyEntry(body types.RequestBody) *types.FileBodyEntry {
	for _, file := range types.FileBodyEntriesOf(body) {
		if file.Selected && strings.TrimSpace(file.FilePath) != "" {
			copy := file
			return &copy
		}
	}
	return nil
}

// sharePostmanAuth maps one auth config onto Postman's auth block.
//
// Five of the eight modes exported nothing at all before. Two of those are
// actively dangerous rather than merely lossy: a request whose mode is "none"
// exported no auth block, and no auth block means INHERIT in Postman — so an
// endpoint that had deliberately opted out of the collection credential came
// back sending it. "none" is therefore an explicit noauth block, and only
// "inherit" is the absent one.
//
// The key names mirror what internal/importers/postman.go reads, so a mode
// survives the round trip instead of exporting into a shape nothing reads back.
func sharePostmanAuth(auth types.AuthConfig) map[string]interface{} {
	switch strings.ToLower(strings.TrimSpace(auth.Mode)) {
	case "none":
		return map[string]interface{}{"type": "noauth"}
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
			{"key": "in", "value": scalar.FirstNonEmpty(auth.APILocation, "header"), "type": "string"},
		}}
	case "digest":
		return map[string]interface{}{"type": "digest", "digest": []map[string]interface{}{
			{"key": "username", "value": auth.Username, "type": "string"},
			{"key": "password", "value": auth.Password, "type": "string"},
		}}
	case "awsv4":
		return map[string]interface{}{"type": "awsv4", "awsv4": []map[string]interface{}{
			{"key": "accessKey", "value": auth.AWSV4.AccessKeyID, "type": "string"},
			{"key": "secretKey", "value": auth.AWSV4.SecretAccessKey, "type": "string"},
			{"key": "sessionToken", "value": auth.AWSV4.SessionToken, "type": "string"},
			{"key": "service", "value": auth.AWSV4.Service, "type": "string"},
			{"key": "region", "value": auth.AWSV4.Region, "type": "string"},
		}}
	case "oauth1":
		return map[string]interface{}{"type": "oauth1", "oauth1": []map[string]interface{}{
			{"key": "consumerKey", "value": auth.OAuth1.ConsumerKey, "type": "string"},
			{"key": "consumerSecret", "value": auth.OAuth1.ConsumerSecret, "type": "string"},
			{"key": "token", "value": auth.OAuth1.AccessToken, "type": "string"},
			{"key": "tokenSecret", "value": auth.OAuth1.AccessTokenSecret, "type": "string"},
			{"key": "callback", "value": auth.OAuth1.CallbackURL, "type": "string"},
			{"key": "verifier", "value": auth.OAuth1.Verifier, "type": "string"},
			{"key": "signatureMethod", "value": scalar.FirstNonEmpty(auth.OAuth1.SignatureMethod, "HMAC-SHA1"), "type": "string"},
			{"key": "privateKey", "value": auth.OAuth1.PrivateKey, "type": "string"},
			{"key": "timestamp", "value": auth.OAuth1.Timestamp, "type": "string"},
			{"key": "nonce", "value": auth.OAuth1.Nonce, "type": "string"},
			{"key": "version", "value": scalar.FirstNonEmpty(auth.OAuth1.Version, "1.0"), "type": "string"},
			{"key": "realm", "value": auth.OAuth1.Realm, "type": "string"},
			{"key": "addParamsToHeader", "value": !strings.EqualFold(strings.TrimSpace(auth.OAuth1.Placement), "query"), "type": "boolean"},
			{"key": "includeBodyHash", "value": auth.OAuth1.IncludeBodyHash, "type": "boolean"},
		}}
	case "oauth2":
		return map[string]interface{}{"type": "oauth2", "oauth2": sharePostmanOAuth2(auth.OAuth2)}
	default:
		// "inherit", and anything this app supports that Postman does not, both
		// mean "write no auth block".
		return nil
	}
}

func sharePostmanOAuth2(auth types.OAuth2Auth) []map[string]interface{} {
	grantType := sharePostmanOAuth2GrantType(auth)
	addTokenTo := "header"
	if strings.EqualFold(strings.TrimSpace(auth.TokenPlacement), "url") {
		addTokenTo = "queryParams"
	}
	clientAuthentication := "header"
	if strings.EqualFold(strings.TrimSpace(auth.CredentialsPlacement), "body") {
		clientAuthentication = "body"
	}
	values := []map[string]interface{}{
		{"key": "grant_type", "value": grantType, "type": "string"},
		{"key": "accessTokenUrl", "value": auth.AccessTokenURL, "type": "string"},
		{"key": "refreshTokenUrl", "value": auth.RefreshTokenURL, "type": "string"},
		{"key": "clientId", "value": auth.ClientID, "type": "string"},
		{"key": "clientSecret", "value": auth.ClientSecret, "type": "string"},
		{"key": "scope", "value": auth.Scope, "type": "string"},
		{"key": "state", "value": auth.State, "type": "string"},
		{"key": "addTokenTo", "value": addTokenTo, "type": "string"},
		{"key": "headerPrefix", "value": auth.TokenHeaderPrefix, "type": "string"},
		{"key": "client_authentication", "value": clientAuthentication, "type": "string"},
	}
	switch grantType {
	case "authorization_code", "authorization_code_with_pkce", "implicit":
		values = append(values,
			map[string]interface{}{"key": "authUrl", "value": auth.AuthorizationURL, "type": "string"},
			map[string]interface{}{"key": "redirect_uri", "value": auth.CallbackURL, "type": "string"},
		)
	case "password_credentials":
		values = append(values,
			map[string]interface{}{"key": "username", "value": auth.Username, "type": "string"},
			map[string]interface{}{"key": "password", "value": auth.Password, "type": "string"},
		)
	}
	return values
}

func sharePostmanOAuth2GrantType(auth types.OAuth2Auth) string {
	switch strings.ToLower(strings.TrimSpace(auth.GrantType)) {
	case "authorization_code":
		if auth.PKCE {
			return "authorization_code_with_pkce"
		}
		return "authorization_code"
	case "password":
		return "password_credentials"
	case "implicit":
		return "implicit"
	default:
		return "client_credentials"
	}
}
