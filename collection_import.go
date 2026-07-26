package main

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
	"time"

	"github.com/mutexdev/lite_api/internal/importers"
	"github.com/mutexdev/lite_api/internal/openapisync"
	"github.com/mutexdev/lite_api/internal/types"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
	"gopkg.in/yaml.v3"
)

const (
	collectionImportMaxFiles     = 32
	collectionImportMaxBytes     = 16 << 20
	collectionImportMaxTreeFiles = 4096
	collectionImportMaxTreeBytes = 64 << 20
)

// CollectionImportSource deliberately accepts either a selected path or advanced
// in-memory content. Content is never returned in previews or error messages.
type CollectionImportSource struct {
	ID              string `json:"id,omitempty"`
	Path            string `json:"path,omitempty"`
	URL             string `json:"url,omitempty"`
	Name            string `json:"name,omitempty"`
	Content         string `json:"content,omitempty"`
	KindOverride    string `json:"kindOverride,omitempty"`
	resolvedContent string
	resolvedName    string
	resolvedError   error
}

type CollectionImportPreviewRequest struct {
	WorkspaceID     string                   `json:"workspaceId,omitempty"`
	DestinationRoot string                   `json:"destinationRoot,omitempty"`
	Sources         []CollectionImportSource `json:"sources"`
}

type CollectionImportPreview struct {
	Rows []CollectionImportPreviewRow `json:"rows"`
}

type CollectionImportPreviewRow struct {
	SourceID        string                               `json:"sourceId"`
	CandidateID     string                               `json:"candidateId"`
	SourceName      string                               `json:"sourceName"`
	SourcePath      string                               `json:"sourcePath,omitempty"`
	DetectedKind    string                               `json:"detectedKind"`
	Confidence      string                               `json:"confidence"`
	CollectionName  string                               `json:"collectionName,omitempty"`
	CollectionID    string                               `json:"collectionId,omitempty"`
	EnvironmentIDs  []string                             `json:"environmentIds,omitempty"`
	FolderIDs       []string                             `json:"folderIds,omitempty"`
	RequestIDs      []string                             `json:"requestIds,omitempty"`
	Environments    []CollectionImportEnvironmentPreview `json:"environments,omitempty"`
	Folders         []CollectionImportFolderPreview      `json:"folders,omitempty"`
	Requests        []CollectionImportRequestPreview     `json:"requests,omitempty"`
	Warnings        []string                             `json:"warnings,omitempty"`
	Losses          []string                             `json:"losses,omitempty"`
	Error           string                               `json:"error,omitempty"`
	DefaultSelect   bool                                 `json:"defaultSelect"`
	ContentHash     string                               `json:"contentHash,omitempty"`
	Conflict        string                               `json:"conflict,omitempty"`
	DestinationPath string                               `json:"destinationPath,omitempty"`
	OpenSemantics   string                               `json:"openSemantics,omitempty"`
	ExistingFolder  bool                                 `json:"existingFolder,omitempty"`
}

// CollectionImportEnvironmentPreview, CollectionImportFolderPreview, and
// CollectionImportRequestPreview contain only display metadata. Their IDs are
// opaque selection tokens, never source contents or parser-local IDs.
type CollectionImportEnvironmentPreview struct {
	SelectionID string `json:"selectionId"`
	Name        string `json:"name"`
}

type CollectionImportFolderPreview struct {
	SelectionID string `json:"selectionId"`
	Name        string `json:"name"`
	Path        string `json:"path"`
	ParentPath  string `json:"parentPath,omitempty"`
}

type CollectionImportRequestPreview struct {
	SelectionID string `json:"selectionId"`
	Name        string `json:"name"`
	FolderPath  string `json:"folderPath,omitempty"`
	Method      string `json:"method,omitempty"`
	Type        string `json:"type,omitempty"`
}

type CollectionImportSelection struct {
	SourceID            string   `json:"sourceId"`
	CandidateID         string   `json:"candidateId"`
	EnvironmentIDs      []string `json:"environmentIds,omitempty"`
	FolderIDs           []string `json:"folderIds,omitempty"`
	RequestIDs          []string `json:"requestIds,omitempty"`
	OutputName          string   `json:"outputName,omitempty"`
	KindOverride        string   `json:"kindOverride,omitempty"`
	ConflictAction      string   `json:"conflictAction,omitempty"` // rename, skip, replace
	ExpectedContentHash string   `json:"expectedContentHash"`
	FilterEnvironments  bool     `json:"filterEnvironments,omitempty"`
	FilterFolders       bool     `json:"filterFolders,omitempty"`
	FilterRequests      bool     `json:"filterRequests,omitempty"`
}

type CollectionImportApplyRequest struct {
	WorkspaceID     string                      `json:"workspaceId"`
	DestinationRoot string                      `json:"destinationRoot,omitempty"`
	Sources         []CollectionImportSource    `json:"sources"`
	Selections      []CollectionImportSelection `json:"selections"`
	// US-044. Opt-in rewriting of pm.* to bru.* for Postman sources. Default
	// false: pm.* is native since US-039-043 and more faithful than a textual
	// rewrite, so an imported script now runs as written.
	TranslatePostmanScripts bool `json:"translatePostmanScripts,omitempty"`
}

type CollectionImportApplyResult struct {
	State   AppState                     `json:"state"`
	Applied []CollectionImportPreviewRow `json:"applied,omitempty"`
	Skipped []CollectionImportPreviewRow `json:"skipped,omitempty"`
	Errors  []CollectionImportPreviewRow `json:"errors,omitempty"`
}

type CollectionImportPickerResult struct {
	Paths     []string `json:"paths"`
	Cancelled bool     `json:"cancelled"`
}

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

func collectionImportFileDialogOptions() wailsruntime.OpenDialogOptions {
	return wailsruntime.OpenDialogOptions{Title: "Import API collections", Filters: []wailsruntime.FileFilter{
		{DisplayName: "Supported imports", Pattern: "*.json;*.yaml;*.yml;*.bru;*.curl;*.sh;*.txt;*.zip;*.wsdl"},
		{DisplayName: "All Files", Pattern: "*.*"},
	}}
}

func chooseCollectionImportFiles(open func(wailsruntime.OpenDialogOptions) ([]string, error)) (CollectionImportPickerResult, error) {
	paths, err := open(collectionImportFileDialogOptions())
	if err != nil {
		return CollectionImportPickerResult{}, err
	}
	if len(paths) == 0 {
		return CollectionImportPickerResult{Cancelled: true}, nil
	}
	if len(paths) > collectionImportMaxFiles {
		return CollectionImportPickerResult{}, fmt.Errorf("select at most %d import files", collectionImportMaxFiles)
	}
	return CollectionImportPickerResult{Paths: append([]string(nil), paths...)}, nil
}

func chooseCollectionImportFolder(open func(wailsruntime.OpenDialogOptions) (string, error)) (CollectionImportPickerResult, error) {
	path, err := open(wailsruntime.OpenDialogOptions{Title: "Open Bruno or OpenCollection folder"})
	if err != nil {
		return CollectionImportPickerResult{}, err
	}
	if strings.TrimSpace(path) == "" {
		return CollectionImportPickerResult{Cancelled: true}, nil
	}
	return CollectionImportPickerResult{Paths: []string{path}}, nil
}

func (a *App) ChooseCollectionImportFiles() (CollectionImportPickerResult, error) {
	if a == nil || a.ctx == nil {
		return CollectionImportPickerResult{}, errors.New("file dialog is unavailable")
	}
	directory := a.collectionImportDefaultDirectory()
	result, err := chooseCollectionImportFiles(func(options wailsruntime.OpenDialogOptions) ([]string, error) {
		options.DefaultDirectory = directory
		return wailsruntime.OpenMultipleFilesDialog(a.ctx, options)
	})
	if err == nil && !result.Cancelled {
		_ = a.rememberCollectionImportDirectory(result.Paths, false)
	}
	return result, err
}

func (a *App) ChooseCollectionImportFolder() (CollectionImportPickerResult, error) {
	if a == nil || a.ctx == nil {
		return CollectionImportPickerResult{}, errors.New("directory dialog is unavailable")
	}
	directory := a.collectionImportDefaultDirectory()
	result, err := chooseCollectionImportFolder(func(options wailsruntime.OpenDialogOptions) (string, error) {
		options.DefaultDirectory = directory
		return wailsruntime.OpenDirectoryDialog(a.ctx, options)
	})
	if err == nil && !result.Cancelled {
		_ = a.rememberCollectionImportDirectory(result.Paths, true)
	}
	return result, err
}

// Read-only: normalizeCollectionImportDirectory takes a string and only stats
// the filesystem.
func (a *App) collectionImportDefaultDirectory() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return normalizeCollectionImportDirectory(a.state.Preferences.General.LastImportDirectory)
}

func (a *App) rememberCollectionImportDirectory(paths []string, folder bool) error {
	if len(paths) == 0 {
		return nil
	}
	directory := paths[0]
	if !folder {
		directory = filepath.Dir(directory)
	}
	directory = normalizeCollectionImportDirectory(directory)
	if directory == "" {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return err
	}
	if a.state.Preferences.General.LastImportDirectory == directory {
		return nil
	}
	a.state.Preferences.General.LastImportDirectory = directory
	return a.persistLocked()
}

func normalizeCollectionImportDirectory(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || !filepath.IsAbs(value) {
		return ""
	}
	resolved, err := filepath.EvalSymlinks(value)
	if err == nil {
		value = resolved
	}
	info, err := os.Stat(value)
	if err != nil || !info.IsDir() {
		return ""
	}
	return filepath.Clean(value)
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
		collection, err := collectionFromImport(ImportPayload{Kind: kind, Name: strings.TrimSuffix(name, filepath.Ext(name)), Content: content})
		return kind, collection, nil, err
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
			collection, err := collectionFromImport(ImportPayload{Kind: "postman", Name: strings.TrimSuffix(name, filepath.Ext(name)), Content: content})
			return "postman", collection, nil, err
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

func collectionImportDiagnostic(err error) string {
	if err == nil {
		return ""
	}
	message := err.Error()
	// These messages are authored by this importer and contain no source
	// content or filesystem paths. Everything else is intentionally collapsed:
	// WalkDir, open, and parser errors can otherwise disclose a selected path.
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
	return "selected import could not be read safely"
}

func (a *App) ApplyCollectionImport(request CollectionImportApplyRequest) (CollectionImportApplyResult, error) {
	sources, err := a.resolveCollectionImportSources(request.Sources)
	if err != nil {
		return CollectionImportApplyResult{}, err
	}
	request.Sources = sources
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return CollectionImportApplyResult{}, err
	}
	workspace, err := a.findWorkspaceLocked(request.WorkspaceID)
	if err != nil {
		return CollectionImportApplyResult{}, err
	}
	_, err = previewCollectionImport(CollectionImportPreviewRequest{Sources: request.Sources})
	if err != nil {
		return CollectionImportApplyResult{}, err
	}
	if err := validateCollectionImportSelections(request.Selections); err != nil {
		return CollectionImportApplyResult{}, err
	}
	candidates := make(map[string]collectionImportCandidate, len(request.Sources))
	for index, source := range request.Sources {
		candidate := previewCollectionImportSource(source, index)
		candidates[candidate.row.CandidateID] = candidate
	}
	result := CollectionImportApplyResult{State: a.state}
	before, err := cloneCollectionImportState(a.state)
	if err != nil {
		return CollectionImportApplyResult{}, err
	}
	mutations := []collectionImportMutation{}
	watchFingerprints := map[string]string{}
	for key, value := range a.collectionWatchFingerprints {
		watchFingerprints[key] = value
	}
	for _, selection := range request.Selections {
		candidate, ok := candidates[selection.CandidateID]
		if !ok || candidate.row.SourceID != selection.SourceID {
			result.Errors = append(result.Errors, CollectionImportPreviewRow{SourceID: selection.SourceID, CandidateID: selection.CandidateID, Error: "selected import source is unavailable"})
			continue
		}
		if strings.TrimSpace(selection.ExpectedContentHash) == "" || selection.ExpectedContentHash != candidate.row.ContentHash {
			candidate.row.Error = "selected source changed since preview"
			result.Errors = append(result.Errors, candidate.row)
			continue
		}
		collection := candidate.collection
		// US-044. Candidates are parsed during DETECTION, before the apply
		// request — and therefore before this flag — exists. A Postman
		// candidate must be re-imported here or the opt-in would silently do
		// nothing for every normally-detected file, leaving the toggle
		// apparently working and demonstrably ineffective.
		if request.TranslatePostmanScripts && strings.EqualFold(strings.TrimSpace(candidate.row.DetectedKind), "postman") && strings.TrimSpace(selection.KindOverride) == "" {
			content, _, _, folder, readErr := readCollectionImportSource(candidate.source)
			if readErr == nil && !folder {
				if translated, translateErr := collectionFromImport(ImportPayload{
					Kind:                    "postman",
					Name:                    candidate.row.CollectionName,
					Content:                 content,
					TranslatePostmanScripts: true,
				}); translateErr == nil {
					collection = translated
				}
				// A failure here is deliberately not fatal: the untranslated
				// candidate is already valid and imports correctly, so falling
				// back to it beats failing an import over an optional rewrite.
			}
		}
		if strings.TrimSpace(selection.KindOverride) != "" {
			content, _, _, folder, readErr := readCollectionImportSource(candidate.source)
			if readErr != nil || folder {
				candidate.row.Error = "manual import kind could not read the selected source"
				result.Errors = append(result.Errors, candidate.row)
				continue
			}
			collection, err = collectionFromImport(ImportPayload{Kind: selection.KindOverride, Name: candidate.row.CollectionName, Content: content, TranslatePostmanScripts: request.TranslatePostmanScripts})
			if err != nil {
				candidate.row.Error = "manual import kind could not be parsed"
				result.Errors = append(result.Errors, candidate.row)
				continue
			}
			candidate.row.Error = ""
			candidate.row = collectionImportRow(candidate.row, collection, strings.ToLower(strings.TrimSpace(selection.KindOverride)), "manual")
		} else if candidate.row.Error != "" {
			result.Errors = append(result.Errors, candidate.row)
			continue
		}
		if candidate.row.ExistingFolder {
			if selection.FilterEnvironments || selection.FilterFolders || selection.FilterRequests {
				candidate.row.Error = "existing collection folders can only be opened as complete collections"
				result.Errors = append(result.Errors, candidate.row)
				continue
			}
			if err := a.applyOpenedImportFolderLocked(workspace, candidate.collection); err != nil {
				candidate.row.Error = "collection folder could not be opened"
				result.Errors = append(result.Errors, candidate.row)
			} else {
				result.Applied = append(result.Applied, candidate.row)
			}
			continue
		}
		collection = filterImportedCollection(collection, selection)
		if strings.TrimSpace(selection.OutputName) != "" {
			collection.Name = strings.TrimSpace(selection.OutputName)
		}
		root, err := collectionImportDestinationRoot(request.DestinationRoot, workspace.Path)
		if err != nil {
			candidate.row.Error = "destination root is invalid"
			result.Errors = append(result.Errors, candidate.row)
			continue
		}
		if err := a.applyImportedCollectionLocked(workspace, &collection, root, selection.ConflictAction, &mutations); err != nil {
			if errors.Is(err, errCollectionImportSkipped) {
				result.Skipped = append(result.Skipped, candidate.row)
			} else {
				rollbackCollectionImportMutations(a, mutations)
				a.collectionWatchFingerprints = watchFingerprints
				a.state = before
				return CollectionImportApplyResult{}, errors.New("selected imports could not be committed")
			}
			continue
		}
		candidate.row = collectionImportRow(candidate.row, collection, candidate.row.DetectedKind, candidate.row.Confidence)
		result.Applied = append(result.Applied, candidate.row)
	}
	if len(result.Applied) > 0 {
		persist := a.persistLocked
		if a.collectionImportHooks != nil && a.collectionImportHooks.persist != nil {
			persist = func() error { return a.collectionImportHooks.persist(a) }
		}
		if err := persist(); err != nil {
			rollbackCollectionImportMutations(a, mutations)
			a.collectionWatchFingerprints = watchFingerprints
			a.state = before
			return CollectionImportApplyResult{}, err
		}
		for _, mutation := range mutations {
			if mutation.backup != "" {
				_ = removeCollectionImportPath(a, mutation.backup)
			}
		}
	}
	result.State = a.state
	return result, nil
}

func cloneCollectionImportState(state AppState) (AppState, error) {
	data, err := json.Marshal(state)
	if err != nil {
		return AppState{}, err
	}
	var clone AppState
	if err := json.Unmarshal(data, &clone); err != nil {
		return AppState{}, err
	}
	return clone, nil
}

func rollbackCollectionImportMutations(app *App, mutations []collectionImportMutation) {
	rename := os.Rename
	if app.collectionImportHooks != nil && app.collectionImportHooks.rename != nil {
		rename = app.collectionImportHooks.rename
	}
	for index := len(mutations) - 1; index >= 0; index-- {
		mutation := mutations[index]
		if err := removeCollectionImportPath(app, mutation.target); err != nil {
			continue
		}
		if mutation.backup != "" {
			_ = rename(mutation.backup, mutation.target)
		}
	}
}

func removeCollectionImportPath(app *App, path string) error {
	remove := os.RemoveAll
	if app.collectionImportHooks != nil && app.collectionImportHooks.remove != nil {
		remove = app.collectionImportHooks.remove
	}
	var err error
	for attempt := 0; attempt < 2; attempt++ {
		if err = remove(path); err == nil || errors.Is(err, os.ErrNotExist) {
			return nil
		}
	}
	return err
}

var errCollectionImportSkipped = errors.New("collection import skipped")

func validateCollectionImportSelections(selections []CollectionImportSelection) error {
	seen := make(map[string]struct{}, len(selections))
	for _, selection := range selections {
		key := selection.SourceID + "\x00" + selection.CandidateID
		if _, exists := seen[key]; exists {
			return errors.New("each import source can be selected only once")
		}
		seen[key] = struct{}{}
		switch strings.ToLower(strings.TrimSpace(selection.ConflictAction)) {
		case "", "rename", "skip", "replace":
		default:
			return errors.New("conflict action must be rename, skip, replace, or empty")
		}
	}
	return nil
}

func collectionImportDestinationRoot(value, fallback string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		value = fallback
	}
	expanded, err := expandUserPath(value)
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(expanded) {
		return "", errors.New("destination root must be absolute")
	}
	root := filepath.Clean(expanded)
	existing := root
	for {
		if _, statErr := os.Stat(existing); statErr == nil {
			break
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			return "", errors.New("destination root is unavailable")
		}
		existing = parent
	}
	resolved, err := filepath.EvalSymlinks(existing)
	if err != nil {
		return "", errors.New("destination root is unavailable")
	}
	relative, err := filepath.Rel(existing, root)
	if err != nil || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", errors.New("destination root is invalid")
	}
	return filepath.Join(resolved, relative), nil
}

func filterImportedCollection(collection Collection, selection CollectionImportSelection) Collection {
	selected := func(ids []string) map[string]bool {
		out := map[string]bool{}
		for _, id := range ids {
			out[id] = true
		}
		return out
	}
	if selection.FilterRequests {
		keep := selected(selection.RequestIDs)
		items := []RequestItem{}
		for index, item := range collection.Items {
			if keep[collectionImportRequestSelectionID(selection.SourceID, item, index)] {
				items = append(items, item)
			}
		}
		collection.Items = items
	}
	if selection.FilterEnvironments {
		keep := selected(selection.EnvironmentIDs)
		environments := []Environment{}
		for index, environment := range collection.Environments {
			if keep[collectionImportEnvironmentSelectionID(selection.SourceID, environment, index)] {
				environments = append(environments, environment)
			}
		}
		collection.Environments = environments
	}
	if selection.FilterFolders {
		keep := selected(selection.FolderIDs)
		folders := []FolderConfig{}
		for index, folder := range collection.Folders {
			if keep[collectionImportFolderSelectionID(selection.SourceID, folder, index)] {
				folders = append(folders, folder)
			}
		}
		collection.Folders = folders
	}
	return collection
}

func (a *App) applyOpenedImportFolderLocked(workspace *Workspace, collection Collection) error {
	now := time.Now()
	for index := range workspace.Collections {
		if filepath.Clean(workspace.Collections[index].Path) == filepath.Clean(collection.Path) {
			workspace.Collections[index] = collection
			workspace.UpdatedAt = now
			a.seedCollectionWatchFingerprintLocked(collection.Path)
			a.openFirstCollectionItemLocked(collection)
			return nil
		}
	}
	workspace.Collections = append(workspace.Collections, collection)
	workspace.UpdatedAt = now
	a.seedCollectionWatchFingerprintLocked(collection.Path)
	a.openFirstCollectionItemLocked(collection)
	return nil
}

func (a *App) applyImportedCollectionLocked(workspace *Workspace, collection *Collection, root, conflictAction string, mutationTargets ...*[]collectionImportMutation) error {
	var mutations *[]collectionImportMutation
	if len(mutationTargets) > 0 {
		mutations = mutationTargets[0]
	}
	collection.Name = firstNonEmpty(strings.TrimSpace(collection.Name), "Imported Collection")
	collection.Format = "bru"
	target := filepath.Join(root, sanitizeFilename(collection.Name))
	if !pathInside(root, target) {
		return errors.New("destination escapes selected root")
	}
	if info, err := os.Lstat(target); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("destination cannot be a symbolic link")
		}
		switch strings.ToLower(strings.TrimSpace(conflictAction)) {
		case "skip":
			return errCollectionImportSkipped
		case "replace":
			backup := target + ".liteapi-import-backup-" + newID("backup")
			rename := os.Rename
			if a.collectionImportHooks != nil && a.collectionImportHooks.rename != nil {
				rename = a.collectionImportHooks.rename
			}
			if err := rename(target, backup); err != nil {
				return err
			}
			collection.Path = target
			if err := a.materializeImportedCollectionLocked(collection); err != nil {
				_ = rename(backup, target)
				return err
			}
			if mutations != nil {
				*mutations = append(*mutations, collectionImportMutation{target: target, backup: backup})
			} else if err := removeCollectionImportPath(a, backup); err != nil {
				return err
			}
			for index := range workspace.Collections {
				if filepath.Clean(workspace.Collections[index].Path) == filepath.Clean(target) {
					workspace.Collections[index] = *collection
					return nil
				}
			}
			workspace.Collections = append(workspace.Collections, *collection)
			workspace.UpdatedAt = time.Now()
			return nil
		default:
			for ordinal := 2; ; ordinal++ {
				candidate := filepath.Join(root, sanitizeFilename(fmt.Sprintf("%s %d", collection.Name, ordinal)))
				if _, err := os.Lstat(candidate); errors.Is(err, os.ErrNotExist) {
					collection.Name, target = fmt.Sprintf("%s %d", collection.Name, ordinal), candidate
					break
				} else if err != nil {
					return err
				}
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	collection.Path = target
	if err := a.materializeImportedCollectionLocked(collection); err != nil {
		return err
	}
	if mutations != nil {
		*mutations = append(*mutations, collectionImportMutation{target: target})
	}
	workspace.Collections = append(workspace.Collections, *collection)
	workspace.UpdatedAt = time.Now()
	return nil
}

func (a *App) materializeImportedCollectionLocked(collection *Collection) error {
	parent, base := filepath.Dir(collection.Path), filepath.Base(collection.Path)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return err
	}
	staging, err := os.MkdirTemp(parent, "."+base+".liteapi-import-")
	if err != nil {
		return err
	}
	defer func() {
		a.clearCollectionWatchFingerprintLocked(staging)
		_ = removeCollectionImportPath(a, staging)
	}()
	finalPath := collection.Path
	collection.Path = staging
	write := a.writeCollectionFilesLocked
	if a.collectionImportHooks != nil && a.collectionImportHooks.write != nil {
		write = func(next *Collection) error { return a.collectionImportHooks.write(a, next) }
	}
	if err := write(collection); err != nil {
		collection.Path = finalPath
		return err
	}
	remapImportedRequestPaths(collection, staging, finalPath)
	collection.Path = finalPath
	rename := os.Rename
	if a.collectionImportHooks != nil && a.collectionImportHooks.rename != nil {
		rename = a.collectionImportHooks.rename
	}
	if err := rename(staging, finalPath); err != nil {
		return err
	}
	a.seedCollectionWatchFingerprintLocked(finalPath)
	return nil
}

func remapImportedRequestPaths(collection *Collection, staging, destination string) {
	for index := range collection.Items {
		path := collection.Items[index].FilePath
		if path == "" || !pathInside(staging, path) {
			continue
		}
		if relative, err := filepath.Rel(staging, path); err == nil && relative != "." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			collection.Items[index].FilePath = filepath.Join(destination, relative)
		}
	}
}
