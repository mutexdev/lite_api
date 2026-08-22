package core

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/mutexdev/lite_api/internal/importers"
	"github.com/mutexdev/lite_api/internal/openapisync"
	"github.com/mutexdev/lite_api/internal/types"

	"gopkg.in/yaml.v3"
)

const (
	collectionImportMaxFiles     = 32
	collectionImportMaxBytes     = 16 << 20
	collectionImportMaxTreeFiles = 4096
	collectionImportMaxTreeBytes = 64 << 20
)

type collectionImportCandidate struct {
	row        CollectionImportPreviewRow
	collection Collection
	source     CollectionImportSource
}

type collectionImportHooks struct {
	write   func(*App, *Collection) error
	rename  func(string, string) error
	remove  func(string) error
	persist func(*App) error
}

type collectionImportMutation struct {
	target string
	backup string
}

func (a *App) PreviewCollectionImport(request CollectionImportPreviewRequest) (CollectionImportPreview, error) {
	sources, err := a.resolveCollectionImportSources(request.Sources)
	if err != nil {
		return CollectionImportPreview{}, err
	}
	request.Sources = sources
	preview, err := previewCollectionImport(request)
	if err != nil || (strings.TrimSpace(request.WorkspaceID) == "" && strings.TrimSpace(request.DestinationRoot) == "") {
		return preview, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return CollectionImportPreview{}, err
	}
	workspaceID := firstNonEmpty(strings.TrimSpace(request.WorkspaceID), a.state.ActiveWorkspaceID)
	workspace, err := a.findWorkspaceLocked(workspaceID)
	if err != nil {
		return CollectionImportPreview{}, err
	}
	root, err := collectionImportDestinationRoot(request.DestinationRoot, workspace.Path)
	if err != nil {
		return CollectionImportPreview{}, errors.New("destination root is invalid")
	}
	for index := range preview.Rows {
		enrichCollectionImportDestination(&preview.Rows[index], workspace, root)
	}
	return preview, nil
}

func previewCollectionImport(request CollectionImportPreviewRequest) (CollectionImportPreview, error) {
	if len(request.Sources) == 0 {
		return CollectionImportPreview{}, errors.New("select an import source")
	}
	if len(request.Sources) > collectionImportMaxFiles {
		return CollectionImportPreview{}, fmt.Errorf("select at most %d import sources", collectionImportMaxFiles)
	}
	seenSourceIDs := make(map[string]struct{}, len(request.Sources))
	for _, source := range request.Sources {
		id := strings.TrimSpace(source.ID)
		if id == "" {
			continue
		}
		if _, exists := seenSourceIDs[id]; exists {
			return CollectionImportPreview{}, fmt.Errorf("duplicate import source ID %q", id)
		}
		seenSourceIDs[id] = struct{}{}
	}
	preview := CollectionImportPreview{Rows: make([]CollectionImportPreviewRow, 0, len(request.Sources))}
	for index, source := range request.Sources {
		candidate := previewCollectionImportSource(source, index)
		preview.Rows = append(preview.Rows, candidate.row)
	}
	return preview, nil
}

func previewCollectionImportSource(source CollectionImportSource, index int) collectionImportCandidate {
	source.ID = firstNonEmpty(strings.TrimSpace(source.ID), fmt.Sprintf("source-%d", index+1))
	row := CollectionImportPreviewRow{SourceID: source.ID, CandidateID: source.ID + ":collection", SourcePath: strings.TrimSpace(source.Path)}
	if strings.TrimSpace(source.URL) != "" {
		row.SourcePath = safeCollectionImportURL(source.URL)
	}
	content, sourceName, hash, folder, err := readCollectionImportSource(source)
	row.SourceName, row.ContentHash = sourceName, hash
	if err != nil {
		row.DetectedKind, row.Confidence, row.Error = "unknown", "none", collectionImportDiagnostic(err)
		return collectionImportCandidate{row: row, source: source}
	}
	if folder {
		collection, err := readCollectionFromDisk(source.Path)
		if err != nil {
			row.DetectedKind, row.Confidence, row.Error = "collection-folder", "high", "folder is not a readable Bruno or OpenCollection collection"
			return collectionImportCandidate{row: row, source: source}
		}
		row = collectionImportRow(row, collection, "collection-folder", "high")
		row.ExistingFolder, row.DefaultSelect = true, true
		return collectionImportCandidate{row: row, collection: collection, source: source}
	}
	kind, collection, warning, err := detectCollectionImport(content, sourceName, source.KindOverride)
	if err != nil {
		row.DetectedKind, row.Confidence, row.Error = kind, "none", collectionImportDiagnostic(err)
		return collectionImportCandidate{row: row, source: source}
	}
	row = collectionImportRow(row, collection, kind, "high")
	row.Warnings = warning
	row.DefaultSelect = true
	return collectionImportCandidate{row: row, collection: collection, source: source}
}

func readCollectionImportSource(source CollectionImportSource) (content, name, hash string, folder bool, err error) {
	if strings.TrimSpace(source.URL) != "" {
		if source.resolvedError != nil {
			return "", "", "", false, source.resolvedError
		}
		if source.resolvedContent != "" || source.resolvedName != "" {
			return source.resolvedContent, source.resolvedName, hashCollectionImportBytes([]byte(source.resolvedContent)), false, nil
		}
		content, name, err = fetchCollectionImportURL(source.URL, nil)
		if err != nil {
			return "", "", "", false, err
		}
		return content, name, hashCollectionImportBytes([]byte(content)), false, nil
	}
	if strings.TrimSpace(source.Path) == "" {
		content = source.Content
		if len(content) > collectionImportMaxBytes {
			return "", "", "", false, errors.New("source exceeds the 16 MiB import limit")
		}
		name = firstNonEmpty(strings.TrimSpace(source.Name), "Advanced import")
		return content, name, hashCollectionImportBytes([]byte(content)), false, nil
	}
	info, err := os.Lstat(source.Path)
	if err != nil {
		return "", "", "", false, errors.New("selected source is unavailable")
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", "", "", false, errors.New("symbolic-link import sources are not supported")
	}
	name = filepath.Base(source.Path)
	if info.IsDir() {
		if !collectionImportFolderLooksSupported(source.Path) {
			return "", name, "", true, errors.New("selected folder is not a Bruno or OpenCollection collection")
		}
		hash, err = hashCollectionImportFolder(source.Path)
		return "", name, hash, true, err
	}
	if !info.Mode().IsRegular() || info.Size() > collectionImportMaxBytes {
		return "", name, "", false, errors.New("selected file exceeds the 16 MiB import limit or is not a regular file")
	}
	data, err := os.ReadFile(source.Path)
	if err != nil {
		return "", name, "", false, errors.New("selected source cannot be read")
	}
	return string(data), name, hashCollectionImportBytes(data), false, nil
}

func safeCollectionImportURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed == nil {
		return "Remote import"
	}
	return parsed.Scheme + "://" + parsed.Host + parsed.EscapedPath()
}

func fetchCollectionImportURL(raw string, client *http.Client) (string, string, error) {
	parsed, err := url.ParseRequestURI(strings.TrimSpace(raw))
	if err != nil || parsed == nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil {
		return "", "", errors.New("remote import URL must be an http or https URL without credentials")
	}
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	redirects := 0
	copyClient := *client
	copyClient.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		redirects++
		if redirects > 3 || request.URL.User != nil || (request.URL.Scheme != "http" && request.URL.Scheme != "https") {
			return errors.New("remote import redirect is not allowed")
		}
		return nil
	}
	response, err := copyClient.Get(parsed.String())
	if err != nil {
		return "", "", errors.New("remote import could not be fetched")
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", "", errors.New("remote import returned an unsuccessful status")
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, collectionImportMaxBytes+1))
	if err != nil || len(data) > collectionImportMaxBytes {
		return "", "", errors.New("remote import exceeds the 16 MiB limit or could not be read")
	}
	name := filepath.Base(parsed.Path)
	if name == "." || name == "/" || name == "" {
		name = "Remote import"
	}
	return string(data), name, nil
}

func (a *App) resolveCollectionImportSources(sources []CollectionImportSource) ([]CollectionImportSource, error) {
	// Read-only: the guarded region is a single field read.
	a.mu.RLock()
	client := a.httpClient
	a.mu.RUnlock()
	resolved := append([]CollectionImportSource(nil), sources...)
	for index := range resolved {
		if strings.TrimSpace(resolved[index].URL) == "" {
			continue
		}
		content, name, err := fetchCollectionImportURL(resolved[index].URL, client)
		if err != nil {
			resolved[index].resolvedError = err
			continue
		}
		resolved[index].resolvedContent = content
		resolved[index].resolvedName = name
	}
	return resolved, nil
}

func collectionImportFolderLooksSupported(path string) bool {
	for _, name := range []string{"bruno.json", "collection.bru", "opencollection.yml", "opencollection.yaml"} {
		if info, err := os.Lstat(filepath.Join(path, name)); err == nil && info.Mode().IsRegular() {
			return true
		}
	}
	return false
}

func hashCollectionImportBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func hashCollectionImportFolder(root string) (string, error) {
	hash := sha256.New()
	var fileCount int
	var totalBytes int64
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("symbolic-link content is not supported")
		}
		if entry.IsDir() {
			if entry.Name() == ".git" || entry.Name() == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Name() == ".DS_Store" {
			return nil
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Size() > collectionImportMaxBytes {
			return errors.New("folder contains a file larger than 16 MiB")
		}
		fileCount++
		totalBytes += info.Size()
		if fileCount > collectionImportMaxTreeFiles || totalBytes > collectionImportMaxTreeBytes {
			return errors.New("folder exceeds the import file-count or size limit")
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		_, _ = io.WriteString(hash, filepath.ToSlash(rel)+"\x00")
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(hash, file)
		closeErr := file.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func detectCollectionImport(content, name, override string) (string, Collection, []string, error) {
	kind := strings.ToLower(strings.TrimSpace(override))
	if kind != "" {
		if kind == "har" {
			collection, warnings, err := importers.ImportHAR(content, strings.TrimSuffix(name, filepath.Ext(name)))
			return kind, collection, warnings, err
		}
		if kind == "curl" {
			collection, warnings, err := collectionFromCurlImport(content, strings.TrimSuffix(name, filepath.Ext(name)))
			return kind, collection, warnings, err
		}
		collection, warnings, err := collectionFromImportDetailed(ImportPayload{Kind: kind, Name: strings.TrimSuffix(name, filepath.Ext(name)), Content: content})
		return kind, collection, warnings, err
	}
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(content)), "curl ") || strings.TrimSpace(content) == "curl" {
		collection, warnings, err := collectionFromCurlImport(content, strings.TrimSuffix(name, filepath.Ext(name)))
		return "curl", collection, warnings, err
	}
	lowerName := strings.ToLower(name)
	if strings.HasSuffix(lowerName, ".zip") {
		return "zip", Collection{}, nil, errors.New("ZIP imports are not supported yet")
	}
	if strings.HasSuffix(lowerName, ".wsdl") {
		return "wsdl", Collection{}, nil, errors.New("WSDL imports are not supported yet")
	}
	if strings.Contains(lowerName, "yaak") {
		return "yaak", Collection{}, nil, errors.New("imports from Yaak are not supported yet")
	}
	if strings.HasSuffix(lowerName, ".bru") {
		collection, err := collectionFromImport(ImportPayload{Kind: "bru", Name: strings.TrimSuffix(name, ".bru"), Content: content})
		return "bru", collection, nil, err
	}
	if strings.HasSuffix(lowerName, ".har") {
		collection, warnings, err := importers.ImportHAR(content, strings.TrimSuffix(name, filepath.Ext(name)))
		return "har", collection, warnings, err
	}
	var raw map[string]interface{}
	if err := yaml.Unmarshal([]byte(content), &raw); err != nil || raw == nil {
		return "unknown", Collection{}, nil, errors.New("source is not a supported JSON or YAML import")
	}
	// US-051. Shape detection as well as extension: HAR files are routinely
	// saved as plain .json from a browser's network panel, and a HAR that
	// reached the generic fallbacks below would be reported as "ambiguous"
	// rather than imported.
	if log, ok := raw["log"].(map[string]interface{}); ok {
		if _, hasEntries := log["entries"]; hasEntries {
			collection, warnings, err := importers.ImportHAR(content, strings.TrimSuffix(name, filepath.Ext(name)))
			return "har", collection, warnings, err
		}
	}
	if importers.IsSwagger2Document(raw) {
		// US-052. Converted to OpenAPI 3 and handed to that importer, rather
		// than taught as a second dialect: the OpenAPI 3 path already handles
		// servers, parameters, request bodies, security schemes and $ref
		// resolution, and a parallel implementation would drift from it.
		converted, err := importers.ConvertSwagger2ToOpenAPI3(content)
		if err != nil {
			return "swagger-2", Collection{}, nil, err
		}
		collection, err := collectionFromImport(ImportPayload{Kind: "openapi", Name: strings.TrimSuffix(name, filepath.Ext(name)), Content: converted})
		return "swagger-2", collection, nil, err
	}
	if _, ok := raw["swagger"]; ok {
		return "swagger-2", Collection{}, nil, errors.New("only Swagger 2.0 can be converted; this document declares an older version")
	}
	if _, ok := raw["openapi"]; ok {
		if _, _, err := openapisync.ValidateOpenAPISyncSpec(content); err != nil {
			return "openapi", Collection{}, nil, errors.New("OpenAPI document is invalid or unsupported")
		}
		collection, err := collectionFromImport(ImportPayload{Kind: "openapi", Name: strings.TrimSuffix(name, filepath.Ext(name)), Content: content})
		return "openapi", collection, nil, err
	}
	if _, ok := raw["opencollection"]; ok {
		return "opencollection-bundle", Collection{}, nil, errors.New("bundled OpenCollection YAML is ambiguous; open its collection folder instead")
	}
	if _, ok := raw["resources"]; ok {
		collection, err := collectionFromImport(ImportPayload{Kind: "insomnia", Name: strings.TrimSuffix(name, filepath.Ext(name)), Content: content})
		return "insomnia", collection, nil, err
	}
	if kindValue, _ := raw["type"].(string); strings.HasPrefix(kindValue, "collection.insomnia") || strings.Contains(strings.ToLower(kindValue), "yaak") {
		if strings.Contains(strings.ToLower(kindValue), "yaak") {
			return "yaak", Collection{}, nil, errors.New("imports from Yaak are not supported yet")
		}
		collection, err := collectionFromImport(ImportPayload{Kind: "insomnia", Name: strings.TrimSuffix(name, filepath.Ext(name)), Content: content})
		return "insomnia", collection, nil, err
	}
	if _, hasInfo := raw["info"]; hasInfo {
		if _, hasItems := raw["item"]; hasItems {
			collection, warnings, err := collectionFromImportDetailed(ImportPayload{Kind: "postman", Name: strings.TrimSuffix(name, filepath.Ext(name)), Content: content})
			return "postman", collection, warnings, err
		}
	}
	if _, hasItems := raw["items"]; hasItems {
		jsonContent, err := json.Marshal(raw)
		if err != nil {
			return "bruno-json", Collection{}, nil, err
		}
		collection, err := collectionFromImport(ImportPayload{Kind: "bruno-json", Name: strings.TrimSuffix(name, filepath.Ext(name)), Content: string(jsonContent)})
		return "bruno-json", collection, nil, err
	}
	return "unknown", Collection{}, nil, errors.New("source is ambiguous; choose an import kind manually")
}

func collectionFromCurlImport(content, fallbackName string) (Collection, []string, error) {
	args, err := splitCurlImportArgs(content)
	if err != nil || len(args) == 0 || strings.ToLower(args[0]) != "curl" {
		return Collection{}, nil, errors.New("cURL command could not be parsed")
	}
	item := types.NewRequestItem("cURL request", "http", 1)
	item.Auth = AuthConfig{Mode: "none", APILocation: "header"}
	warnings := []string{}
	var endpoint string
	for index := 1; index < len(args); index++ {
		arg := args[index]
		next := func() (string, bool) {
			if index+1 >= len(args) {
				return "", false
			}
			index++
			return args[index], true
		}
		switch arg {
		case "-X", "--request":
			if value, ok := next(); ok {
				item.Method = strings.ToUpper(value)
			}
		case "-H", "--header":
			if value, ok := next(); ok {
				if key, raw, found := strings.Cut(value, ":"); found {
					item.Headers = append(item.Headers, KeyValue{Name: strings.TrimSpace(key), Value: strings.TrimSpace(raw), Enabled: true})
				}
			}
		case "-d", "--data", "--data-raw", "--data-binary", "--data-ascii":
			if value, ok := next(); ok {
				item.Body = RequestBody{Mode: "text", Text: value}
				if item.Method == "GET" {
					item.Method = "POST"
				}
			}
		case "-F", "--form":
			if value, ok := next(); ok {
				item.Body.Mode = "multipartForm"
				key, raw, _ := strings.Cut(value, "=")
				item.Body.Multipart = append(item.Body.Multipart, FormPart{Name: strings.TrimSpace(key), Value: raw, Enabled: true})
				if item.Method == "GET" {
					item.Method = "POST"
				}
			}
		case "-u", "--user":
			if value, ok := next(); ok {
				user, password, _ := strings.Cut(value, ":")
				item.Auth = AuthConfig{Mode: "basic", Username: user, Password: password, APILocation: "header"}
			}
		case "--url":
			if value, ok := next(); ok {
				endpoint = value
			}
		default:
			if strings.HasPrefix(arg, "http://") || strings.HasPrefix(arg, "https://") {
				endpoint = arg
			} else if strings.HasPrefix(arg, "-") {
				warnings = append(warnings, "Some cURL switches were not imported")
			}
		}
	}
	if strings.TrimSpace(endpoint) == "" {
		return Collection{}, nil, errors.New("cURL command needs an http or https URL")
	}
	item.URL = endpoint
	collection := Collection{ID: newID("collection"), Name: firstNonEmpty(strings.TrimSpace(fallbackName), "cURL import"), Format: "curl", Auth: AuthConfig{Mode: "none"}, SecurityConfig: CollectionSecurityConfig{JSSandboxMode: "safe"}, Items: []RequestItem{item}, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	return collection, uniqueCollectionImportWarnings(warnings), nil
}

func splitCurlImportArgs(value string) ([]string, error) {
	args := []string{}
	var current strings.Builder
	var quote rune
	escaped := false
	for _, char := range strings.TrimSpace(value) {
		if escaped {
			current.WriteRune(char)
			escaped = false
			continue
		}
		if char == '\\' && quote != '\'' {
			escaped = true
			continue
		}
		if quote != 0 {
			if char == quote {
				quote = 0
			} else {
				current.WriteRune(char)
			}
			continue
		}
		if char == '\'' || char == '"' {
			quote = char
			continue
		}
		if char == ' ' || char == '\t' || char == '\n' {
			if current.Len() > 0 {
				args = append(args, current.String())
				current.Reset()
			}
			continue
		}
		current.WriteRune(char)
	}
	if quote != 0 || escaped {
		return nil, errors.New("cURL quoting is incomplete")
	}
	if current.Len() > 0 {
		args = append(args, current.String())
	}
	return args, nil
}

func uniqueCollectionImportWarnings(values []string) []string {
	seen := map[string]bool{}
	result := []string{}
	for _, value := range values {
		if !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	return result
}

func collectionImportRow(row CollectionImportPreviewRow, collection Collection, kind, confidence string) CollectionImportPreviewRow {
	row.DetectedKind, row.Confidence = kind, confidence
	row.CollectionName, row.CollectionID = collection.Name, collection.ID
	row.EnvironmentIDs = nil
	row.FolderIDs = nil
	row.RequestIDs = nil
	row.Environments = nil
	row.Folders = nil
	row.Requests = nil
	for index, environment := range collection.Environments {
		selectionID := collectionImportEnvironmentSelectionID(row.SourceID, environment, index)
		row.EnvironmentIDs = append(row.EnvironmentIDs, selectionID)
		row.Environments = append(row.Environments, CollectionImportEnvironmentPreview{SelectionID: selectionID, Name: firstNonEmpty(strings.TrimSpace(environment.Name), "Unnamed environment")})
	}
	for index, folder := range collection.Folders {
		selectionID := collectionImportFolderSelectionID(row.SourceID, folder, index)
		row.FolderIDs = append(row.FolderIDs, selectionID)
		folderPath := strings.TrimSpace(folder.Path)
		row.Folders = append(row.Folders, CollectionImportFolderPreview{
			SelectionID: selectionID,
			Name:        firstNonEmpty(strings.TrimSpace(folder.Name), filepath.Base(folderPath), "Unnamed folder"),
			Path:        folderPath,
			ParentPath:  filepath.ToSlash(filepath.Dir(filepath.FromSlash(folderPath))),
		})
		if row.Folders[len(row.Folders)-1].ParentPath == "." {
			row.Folders[len(row.Folders)-1].ParentPath = ""
		}
	}
	for index, request := range collection.Items {
		selectionID := collectionImportRequestSelectionID(row.SourceID, request, index)
		row.RequestIDs = append(row.RequestIDs, selectionID)
		row.Requests = append(row.Requests, CollectionImportRequestPreview{
			SelectionID: selectionID,
			Name:        firstNonEmpty(strings.TrimSpace(request.Name), "Unnamed request"),
			FolderPath:  strings.TrimSpace(request.FolderPath),
			Method:      strings.ToUpper(strings.TrimSpace(request.Method)),
			Type:        strings.TrimSpace(request.Type),
		})
	}
	return row
}

func enrichCollectionImportDestination(row *CollectionImportPreviewRow, workspace *Workspace, root string) {
	if row == nil || workspace == nil || row.Error != "" {
		return
	}
	if row.ExistingFolder {
		row.DestinationPath = row.SourcePath
		row.OpenSemantics = "open-existing-folder-whole-collection"
		for _, collection := range workspace.Collections {
			if filepath.Clean(collection.Path) == filepath.Clean(row.SourcePath) {
				row.Conflict = "already-open"
				return
			}
		}
		row.Conflict = "open-folder"
		return
	}
	row.DestinationPath = filepath.Join(root, sanitizeFilename(firstNonEmpty(strings.TrimSpace(row.CollectionName), "Imported Collection")))
	info, err := os.Lstat(row.DestinationPath)
	if errors.Is(err, os.ErrNotExist) {
		row.Conflict = "none"
		return
	}
	if err != nil || info.Mode()&os.ModeSymlink != 0 {
		row.Conflict = "unavailable"
		return
	}
	for _, collection := range workspace.Collections {
		if filepath.Clean(collection.Path) == filepath.Clean(row.DestinationPath) {
			row.Conflict = "already-open"
			return
		}
	}
	row.Conflict = "exists"
}

func collectionImportSelectionID(sourceID, kind string, values ...string) string {
	data := append([]string{sourceID, kind}, values...)
	sum := sha256.Sum256([]byte(strings.Join(data, "\x00")))
	return "import-" + kind + "-" + hex.EncodeToString(sum[:12])
}

func collectionImportEnvironmentSelectionID(sourceID string, environment Environment, index int) string {
	return collectionImportSelectionID(sourceID, "environment", environment.Name, fmt.Sprint(index))
}

func collectionImportFolderSelectionID(sourceID string, folder FolderConfig, index int) string {
	return collectionImportSelectionID(sourceID, "folder", folder.Path, folder.Name, fmt.Sprint(index))
}

func collectionImportRequestSelectionID(sourceID string, request RequestItem, index int) string {
	return collectionImportSelectionID(sourceID, "request", request.Name, request.Type, request.Method, request.URL, request.FolderPath, fmt.Sprint(index))
}

// collectionImportDiagnosticMaxBytes bounds what reaches the preview row. A
// parser handed a 16 MiB file can produce an error carrying a large excerpt of
// it, and the row is one line of a table.
const collectionImportDiagnosticMaxBytes = 240

// collectionImportDiagnostic renders an import failure for the preview row.
//
// US-055. This used to allow-list about twenty authored strings and collapse
// everything else -- every JSON syntax error, every type mismatch, every
// filesystem failure -- into "selected import could not be read safely". The
// redaction it was protecting is real: WalkDir, os.Open and parser errors all
// embed the path the user selected, and an import row is a place a screenshot
// gets taken. But redacting a path is not the same thing as discarding the
// diagnosis, and discarding it left the console as the only place to look --
// which for a packaged desktop build is nowhere at all.
//
// So the authored messages still pass through verbatim, and everything else is
// scrubbed of anything path-shaped and then kept.
func collectionImportDiagnostic(err error) string {
	if err == nil {
		return ""
	}
	message := err.Error()
	// These messages are authored by this importer and contain no source
	// content or filesystem paths.
	for _, safe := range []string{
		"ZIP imports are not supported yet",
		"WSDL imports are not supported yet",
		"imports from Yaak are not supported yet",
		"imports from Swagger 2 are not supported; provide OpenAPI 3",
		"OpenAPI document is invalid or unsupported",
		"bundled OpenCollection YAML is ambiguous; open its collection folder instead",
		"source is not a supported JSON or YAML import",
		"source is ambiguous; choose an import kind manually",
		"symbolic-link import sources are not supported",
		"symbolic-link content is not supported",
		"selected source is unavailable",
		"selected source cannot be read",
		"selected file exceeds the 16 MiB import limit or is not a regular file",
		"source exceeds the 16 MiB import limit",
		"folder contains a file larger than 16 MiB",
		"folder exceeds the import file-count or size limit",
		"remote import URL must be an http or https URL without credentials",
		"remote import could not be fetched",
		"remote import redirect is not allowed",
		"remote import returned an unsuccessful status",
		"remote import exceeds the 16 MiB limit or could not be read",
	} {
		if message == safe {
			return safe
		}
	}
	if described := describeCollectionImportError(err); described != "" {
		message = described
	}
	scrubbed := scrubCollectionImportDiagnostic(message)
	if strings.TrimSpace(scrubbed) == "" {
		return "selected import could not be read safely"
	}
	return scrubbed
}

// describeCollectionImportError turns a machine error into the sentence the
// person importing the file can act on. Positions come from the importer, which
// still has the document; here only the shape of the failure is known.
func describeCollectionImportError(err error) string {
	var typeError *json.UnmarshalTypeError
	if errors.As(err, &typeError) {
		field := strings.TrimPrefix(typeError.Field, ".")
		if field == "" {
			field = "value"
		}
		return fmt.Sprintf("field %q is a %s where a %s is required", field, typeError.Value, typeError.Type.String())
	}
	var syntaxError *json.SyntaxError
	if errors.As(err, &syntaxError) {
		return "invalid JSON: " + syntaxError.Error()
	}
	var yamlTypeError *yaml.TypeError
	if errors.As(err, &yamlTypeError) && len(yamlTypeError.Errors) > 0 {
		return "invalid YAML: " + yamlTypeError.Errors[0]
	}
	if errors.Is(err, syscall.ENAMETOOLONG) {
		return "a request or folder name is too long for this filesystem"
	}
	if errors.Is(err, syscall.ENOSPC) {
		return "there is no space left on the destination disk"
	}
	if errors.Is(err, os.ErrPermission) {
		return "the destination is not writable"
	}
	return ""
}

// scrubCollectionImportDiagnostic removes anything path-shaped. Tokens holding
// a path separator, a home marker or a drive letter are replaced wholesale
// rather than trimmed, because a partially redacted path is still a path.
func scrubCollectionImportDiagnostic(message string) string {
	message = strings.Join(strings.Fields(message), " ")
	fields := strings.Split(message, " ")
	kept := make([]string, 0, len(fields))
	for _, field := range fields {
		if collectionImportFieldLooksLikeAPath(field) {
			kept = append(kept, "[path]")
			continue
		}
		kept = append(kept, field)
	}
	scrubbed := strings.Join(kept, " ")
	if len(scrubbed) > collectionImportDiagnosticMaxBytes {
		cut := collectionImportDiagnosticMaxBytes - len("\u2026")
		for cut > 0 && !utf8.RuneStart(scrubbed[cut]) {
			cut--
		}
		scrubbed = strings.TrimSpace(scrubbed[:cut]) + "\u2026"
	}
	return scrubbed
}

func collectionImportFieldLooksLikeAPath(field string) bool {
	if strings.ContainsAny(field, "/\\~") {
		return true
	}
	// A bare Windows drive reference, "C:" or "C:x".
	if len(field) >= 2 && field[1] == ':' && ((field[0] >= 'A' && field[0] <= 'Z') || (field[0] >= 'a' && field[0] <= 'z')) {
		return true
	}
	return false
}
