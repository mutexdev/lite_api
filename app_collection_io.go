package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
	"gopkg.in/yaml.v3"

	"github.com/mutexdev/lite_api/internal/importers"
	"github.com/mutexdev/lite_api/internal/openapisync"
	"github.com/mutexdev/lite_api/internal/scalar"
	"github.com/mutexdev/lite_api/internal/store/bru"
	"github.com/mutexdev/lite_api/internal/store/yamlstore"
	"github.com/mutexdev/lite_api/internal/transport"
)

// writeCollectionFileLocked writes one collection file, skipping the write when
// the bytes on disk already match (US-015).
//
// Saving a single request previously rewrote bruno.json, collection.bru, every
// environment file and every request file in the collection — 4.16 ms/op for
// one 50-request collection at the Phase 0 baseline, on every save.
//
// The gate is a fingerprint of what THIS App last wrote, falling back to
// reading the file when it has no record. Two consequences worth stating:
//
//   - The first save after startup pays one read per file and then nothing.
//   - A file edited outside LiteAPI is left alone when our content has not
//     changed. That is a deliberate improvement, not an oversight: the previous
//     behaviour silently clobbered an external edit whenever the user saved any
//     unrelated request. The collection watcher is what brings external edits
//     back into the app, and it is unaffected — skipping a write leaves the
//     bytes on disk exactly as the watcher last saw them.
//
// a.mu must be held.
func (a *App) writeCollectionFileLocked(path string, data []byte) error {
	fingerprint := fmt.Sprintf("%x", sha256.Sum256(data))
	if a.collectionFileFingerprints == nil {
		a.collectionFileFingerprints = map[string]string{}
	}
	if known, ok := a.collectionFileFingerprints[path]; ok {
		if known == fingerprint {
			return nil
		}
	} else if existing, err := os.ReadFile(path); err == nil && bytes.Equal(existing, data) {
		a.collectionFileFingerprints[path] = fingerprint
		return nil
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		// Do not record on failure; the next save must retry rather than
		// conclude the file is already correct.
		return err
	}
	a.collectionFileFingerprints[path] = fingerprint
	return nil
}

func (a *App) writeCollectionFilesLocked(collection *Collection) error {
	if collection.Path == "" {
		return errors.New("collection path is empty")
	}
	if err := os.MkdirAll(collection.Path, 0o755); err != nil {
		return err
	}
	ensureRequestFilePaths(collection, requestFileExtensionForCollection(*collection))
	if err := a.storeCollectionEnvironmentSecretsLocked(collection); err != nil {
		return err
	}
	if collection.Format == "yml" {
		if err := a.writeCollectionFileLocked(filepath.Join(collection.Path, "opencollection.yml"), []byte(stringifyYAMLCollection(*collection))); err != nil {
			return err
		}
		for _, item := range collection.Items {
			content, err := stringifyYAMLRequest(item)
			if err != nil {
				return err
			}
			target := requestFilePath(*collection, item, ".yml")
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			if err := a.writeCollectionFileLocked(target, []byte(content)); err != nil {
				return err
			}
		}
		a.seedCollectionWatchFingerprintLocked(collection.Path)
		return nil
	}
	config := map[string]interface{}{
		"name":   collection.Name,
		"type":   "collection",
		"ignore": []string{"node_modules", ".git"},
	}
	config["version"] = firstNonEmpty(collection.Version, "1")
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
	configData, _ := json.MarshalIndent(config, "", "  ")
	if err := a.writeCollectionFileLocked(filepath.Join(collection.Path, "bruno.json"), configData); err != nil {
		return err
	}
	if err := a.writeCollectionFileLocked(filepath.Join(collection.Path, "collection.bru"), []byte(bru.StringifyBruCollection(*collection))); err != nil {
		return err
	}
	if len(collection.Environments) > 0 {
		envPath := filepath.Join(collection.Path, "environments")
		if err := os.MkdirAll(envPath, 0o755); err != nil {
			return err
		}
		for _, env := range collection.Environments {
			filename := sanitizeFilename(env.Name)
			if filename == "" {
				filename = env.ID
			}
			if err := a.writeCollectionFileLocked(filepath.Join(envPath, filename+".bru"), []byte(bru.StringifyBruEnvironment(env))); err != nil {
				return err
			}
		}
	}
	for _, item := range collection.Items {
		content := bru.StringifyBru(item)
		target := requestFilePath(*collection, item, ".bru")
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := a.writeCollectionFileLocked(target, []byte(content)); err != nil {
			return err
		}
	}
	a.seedCollectionWatchFingerprintLocked(collection.Path)
	return nil
}

func (a *App) writeCollectionNameMetadataLocked(collection *Collection) error {
	if collection.Path == "" {
		return errors.New("collection path is empty")
	}
	if err := os.MkdirAll(collection.Path, 0o755); err != nil {
		return err
	}
	var err error
	if strings.EqualFold(collection.Format, "yml") || strings.EqualFold(collection.Format, "yaml") || fileExists(filepath.Join(collection.Path, "opencollection.yml")) {
		err = writeYAMLCollectionNameMetadata(collection)
	} else {
		err = writeBruCollectionNameMetadata(collection)
	}
	if err == nil {
		a.seedCollectionWatchFingerprintLocked(collection.Path)
	}
	return err
}

func writeYAMLCollectionNameMetadata(collection *Collection) error {
	target := filepath.Join(collection.Path, "opencollection.yml")
	data, err := os.ReadFile(target)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return os.WriteFile(target, []byte(stringifyYAMLCollection(*collection)), 0o600)
		}
		return err
	}
	if strings.TrimSpace(string(data)) == "" {
		return os.WriteFile(target, []byte(stringifyYAMLCollection(*collection)), 0o600)
	}
	var root map[string]interface{}
	if err := yaml.Unmarshal(data, &root); err != nil {
		return fmt.Errorf("parse opencollection.yml: %w", err)
	}
	if root == nil {
		root = map[string]interface{}{}
	}
	info, _ := root["info"].(map[string]interface{})
	if info == nil {
		info = map[string]interface{}{}
		root["info"] = info
	}
	info["name"] = collection.Name
	if _, ok := info["version"]; !ok {
		info["version"] = firstNonEmpty(collection.Version, "1")
	}
	if _, ok := root["opencollection"]; !ok {
		root["opencollection"] = "1.0.0"
	}
	updated, err := yaml.Marshal(root)
	if err != nil {
		return err
	}
	return os.WriteFile(target, updated, 0o600)
}

func writeBruCollectionNameMetadata(collection *Collection) error {
	target := filepath.Join(collection.Path, "bruno.json")
	config := map[string]interface{}{}
	if data, err := os.ReadFile(target); err == nil && strings.TrimSpace(string(data)) != "" {
		if err := json.Unmarshal(data, &config); err != nil {
			return fmt.Errorf("parse bruno.json: %w", err)
		}
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if config == nil {
		config = map[string]interface{}{}
	}
	config["name"] = collection.Name
	if _, ok := config["type"]; !ok {
		config["type"] = "collection"
	}
	if _, ok := config["version"]; !ok {
		config["version"] = firstNonEmpty(collection.Version, "1")
	}
	if _, ok := config["ignore"]; !ok {
		config["ignore"] = []string{"node_modules", ".git"}
	}
	configData, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(target, configData, 0o600)
}

func writeClonedCollectionRootMetadata(source, cloned *Collection) error {
	if strings.EqualFold(cloned.Format, "yml") || strings.EqualFold(cloned.Format, "yaml") || fileExists(filepath.Join(source.Path, "opencollection.yml")) {
		cloned.Format = "yml"
		return writeClonedYAMLCollectionRootMetadata(source, cloned)
	}
	cloned.Format = "bru"
	return writeClonedBruCollectionRootMetadata(source, cloned)
}

func writeClonedYAMLCollectionRootMetadata(source, cloned *Collection) error {
	sourceConfigPath := filepath.Join(source.Path, "opencollection.yml")
	targetConfigPath := filepath.Join(cloned.Path, "opencollection.yml")
	data, err := os.ReadFile(sourceConfigPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return os.WriteFile(targetConfigPath, []byte(stringifyYAMLCollection(*cloned)), 0o600)
		}
		return err
	}
	var root map[string]interface{}
	if err := yaml.Unmarshal(data, &root); err != nil {
		return fmt.Errorf("parse opencollection.yml: %w", err)
	}
	if root == nil {
		root = map[string]interface{}{}
	}
	info, _ := root["info"].(map[string]interface{})
	if info == nil {
		info = map[string]interface{}{}
		root["info"] = info
	}
	info["name"] = cloned.Name
	if _, ok := info["version"]; !ok {
		info["version"] = firstNonEmpty(cloned.Version, "1")
	}
	if _, ok := root["opencollection"]; !ok {
		root["opencollection"] = "1.0.0"
	}
	updated, err := yaml.Marshal(root)
	if err != nil {
		return err
	}
	return os.WriteFile(targetConfigPath, updated, 0o600)
}

func writeClonedBruCollectionRootMetadata(source, cloned *Collection) error {
	sourceConfigPath := filepath.Join(source.Path, "bruno.json")
	targetConfigPath := filepath.Join(cloned.Path, "bruno.json")
	config := map[string]interface{}{}
	if data, err := os.ReadFile(sourceConfigPath); err == nil && strings.TrimSpace(string(data)) != "" {
		if err := json.Unmarshal(data, &config); err != nil {
			return fmt.Errorf("parse bruno.json: %w", err)
		}
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if config == nil {
		config = map[string]interface{}{}
	}
	config["name"] = cloned.Name
	if _, ok := config["type"]; !ok {
		config["type"] = "collection"
	}
	if _, ok := config["version"]; !ok {
		config["version"] = firstNonEmpty(cloned.Version, "1")
	}
	if _, ok := config["ignore"]; !ok {
		config["ignore"] = []string{"node_modules", ".git"}
	}
	configData, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(targetConfigPath, configData, 0o600)
}

func copyCollectionFormatFiles(sourcePath, targetPath, format string) error {
	ext := ".bru"
	rootConfigName := "bruno.json"
	if strings.EqualFold(format, "yml") || strings.EqualFold(format, "yaml") || fileExists(filepath.Join(sourcePath, "opencollection.yml")) {
		ext = ".yml"
		rootConfigName = "opencollection.yml"
	}
	return filepath.WalkDir(sourcePath, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path != sourcePath && pathInside(targetPath, path) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		if strings.ToLower(filepath.Ext(path)) != ext {
			return nil
		}
		if filepath.Dir(path) == sourcePath && filepath.Base(path) == rootConfigName {
			return nil
		}
		rel, err := filepath.Rel(sourcePath, path)
		if err != nil {
			return err
		}
		return copyCollectionFile(path, filepath.Join(targetPath, rel))
	})
}

func copyCollectionFile(sourcePath, targetPath string) (err error) {
	info, statErr := os.Stat(sourcePath)
	if statErr != nil {
		return statErr
	}
	if !info.Mode().IsRegular() {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		return err
	}
	source, err := os.Open(sourcePath)
	if err != nil {
		return err
	}
	// Read-only handle: a failed close cannot lose data.
	defer func() { _ = source.Close() }()
	target, err := os.OpenFile(targetPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return err
	}
	// The write handle's close error must surface: it is where a deferred
	// write failure (ENOSPC, EDQUOT, a failing network filesystem) is reported,
	// and dropping it would report a truncated collection copy as a success.
	defer func() {
		if closeErr := target.Close(); closeErr != nil && err == nil {
			err = closeErr
		}
	}()
	_, err = io.Copy(target, source)
	return err
}

func (a *App) writeFolderConfigLocked(collection *Collection, folder FolderConfig) error {
	if strings.TrimSpace(collection.Path) == "" {
		return errors.New("collection path is empty")
	}
	folderPath := normalizeFolderPathKey(folder.Path)
	if folderPath == "" {
		return errors.New("folder path is required")
	}
	if strings.HasPrefix(folderPath, "../") || folderPath == ".." || filepath.IsAbs(folderPath) {
		return fmt.Errorf("invalid folder path %s", folder.Path)
	}
	targetDir := filepath.Join(collection.Path, filepath.FromSlash(folderPath))
	if !pathInside(collection.Path, targetDir) {
		return fmt.Errorf("folder path %s escapes collection", folder.Path)
	}
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return err
	}
	target := folderMetadataWritePath(*collection, targetDir)
	var content string
	if collection.Format == "yml" {
		content = stringifyYAMLFolder(folder)
	} else {
		content = bru.StringifyBruFolder(folder)
	}
	if err := os.WriteFile(target, []byte(content), 0o600); err != nil {
		return err
	}
	a.seedCollectionWatchFingerprintLocked(collection.Path)
	return nil
}

func folderMetadataWritePath(collection Collection, targetDir string) string {
	if collection.Format == "yml" {
		for _, name := range []string{"folder.yml", "folder.yaml"} {
			path := filepath.Join(targetDir, name)
			if _, err := os.Stat(path); err == nil {
				return path
			}
		}
		return filepath.Join(targetDir, "folder.yml")
	}
	return filepath.Join(targetDir, "folder.bru")
}

func (a *App) writeWorkspaceGlobalEnvironmentFilesLocked(workspace *Workspace) error {
	if workspace == nil || strings.TrimSpace(workspace.Path) == "" {
		return nil
	}
	envPath := filepath.Join(workspace.Path, "environments")
	if err := os.MkdirAll(envPath, 0o755); err != nil {
		return err
	}
	entries, err := os.ReadDir(envPath)
	if err == nil {
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			ext := strings.ToLower(filepath.Ext(entry.Name()))
			if ext == ".yml" || ext == ".yaml" {
				if err := os.Remove(filepath.Join(envPath, entry.Name())); err != nil && !errors.Is(err, os.ErrNotExist) {
					return err
				}
			}
		}
	}
	for _, env := range workspace.GlobalEnvironments {
		filename := sanitizeFilename(env.Name)
		if filename == "" {
			filename = env.ID
		}
		if err := os.WriteFile(filepath.Join(envPath, filename+".yml"), []byte(bru.StringifyYAMLEnvironment(env)), 0o600); err != nil {
			return err
		}
	}
	return nil
}

func collectionFolderFilesystemPath(collection *Collection, folderPath string) (string, error) {
	if collection == nil {
		return "", errors.New("collection is required")
	}
	if strings.TrimSpace(collection.Path) == "" {
		return "", errors.New("collection path is empty")
	}
	folder, err := findFolderConfig(collection, folderPath)
	if err != nil {
		return "", err
	}
	physicalPath := normalizeFolderPathKey(firstNonEmpty(folder.Path, folder.DisplayPath, folder.Name))
	if physicalPath == "" {
		return "", errors.New("folder path is required")
	}
	targetPath := filepath.Clean(filepath.Join(collection.Path, filepath.FromSlash(physicalPath)))
	if !pathInside(collection.Path, targetPath) {
		return "", fmt.Errorf("folder path %s escapes collection", physicalPath)
	}
	return targetPath, nil
}

func collectionRequestFilesystemPath(collection *Collection, item RequestItem) (string, error) {
	if collection == nil {
		return "", errors.New("collection is required")
	}
	if strings.TrimSpace(collection.Path) == "" {
		return "", errors.New("collection path is empty")
	}
	if pathInside(collection.Path, item.FilePath) {
		return filepath.Clean(item.FilePath), nil
	}
	filename := sanitizeFilename(item.Name)
	if filename == "" {
		filename = item.ID
	}
	folder := filepath.Clean(collection.Path)
	if folderPath := normalizeFolderPathKey(item.FolderPath); folderPath != "" {
		if folderConfig, err := findFolderConfig(collection, folderPath); err == nil {
			folderPath = normalizeFolderPathKey(firstNonEmpty(folderConfig.Path, folderConfig.DisplayPath, folderConfig.Name))
		}
		folder = filepath.Join(collection.Path, filepath.FromSlash(folderPath))
	}
	targetPath := filepath.Clean(filepath.Join(folder, filename+requestFileExtensionForCollection(*collection)))
	if !pathInside(collection.Path, targetPath) {
		return "", fmt.Errorf("request path %s escapes collection", targetPath)
	}
	return targetPath, nil
}

func ensureRequestFilePaths(collection *Collection, defaultExt string) {
	used := map[string]bool{}
	for i := range collection.Items {
		target := ""
		if pathInside(collection.Path, collection.Items[i].FilePath) {
			target = filepath.Clean(collection.Items[i].FilePath)
			if used[target] {
				target = ""
			}
		}
		if target == "" {
			target = uniqueRequestFilePath(*collection, collection.Items[i], defaultExt, used)
		}
		collection.Items[i].FilePath = target
		used[target] = true
	}
}

func uniqueRequestFilePath(collection Collection, item RequestItem, defaultExt string, used map[string]bool) string {
	filename := sanitizeFilename(item.Name)
	if filename == "" {
		filename = item.ID
	}
	folder := filepath.Clean(collection.Path)
	if strings.TrimSpace(item.FolderPath) != "" {
		folder = filepath.Join(folder, filepath.FromSlash(item.FolderPath))
	}
	for index := 0; ; index++ {
		candidateName := filename
		if index > 0 {
			candidateName = fmt.Sprintf("%s %d", filename, index+1)
		}
		candidate := filepath.Clean(filepath.Join(folder, candidateName+defaultExt))
		if !used[candidate] {
			return candidate
		}
	}
}

func normalizeGitRemoteURL(raw string) (string, error) {
	remote := strings.TrimSpace(raw)
	if remote == "" {
		return "", errors.New("git remote URL is required")
	}
	if strings.ContainsAny(remote, "\r\n\t ") {
		return "", errors.New("git remote URL cannot contain whitespace")
	}
	if strings.Contains(remote, "://") {
		parsed, err := url.Parse(remote)
		if err != nil {
			return "", fmt.Errorf("invalid Git remote URL: %w", err)
		}
		switch parsed.Scheme {
		case "https", "http", "ssh", "git":
			if parsed.Host == "" || parsed.Path == "" || parsed.Path == "/" {
				return "", errors.New("git remote URL must include host and repository path")
			}
		case "file":
			if parsed.Path == "" || parsed.Path == "/" {
				return "", errors.New("file Git remote URL must include a repository path")
			}
		default:
			return "", fmt.Errorf("unsupported Git remote URL scheme %q", parsed.Scheme)
		}
		if parsed.User != nil {
			return "", errors.New("git remote URL must not embed credentials")
		}
		return remote, nil
	}
	if strings.HasPrefix(remote, "/") || strings.HasPrefix(remote, "./") || strings.HasPrefix(remote, "../") {
		return "", errors.New("local Git remotes must use file:// URLs")
	}
	colon := strings.Index(remote, ":")
	if colon <= 0 || colon == len(remote)-1 {
		return "", errors.New("git remote URL must be https://, ssh://, file://, or git@host:path")
	}
	userHost := remote[:colon]
	repoPath := remote[colon+1:]
	at := strings.Index(userHost, "@")
	if at <= 0 || at == len(userHost)-1 || strings.Contains(repoPath, ":") || strings.HasPrefix(repoPath, "/") {
		return "", errors.New("git remote URL must be https://, ssh://, file://, or git@host:path")
	}
	return remote, nil
}

func gitVersion() (string, error) {
	if _, err := exec.LookPath("git"); err != nil {
		return "", errors.New("git is not installed or not on PATH")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, "git", "--version").CombinedOutput()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return "", errors.New("git --version timed out")
		}
		return "", fmt.Errorf("git --version failed: %s", strings.TrimSpace(string(output)))
	}
	return strings.TrimSpace(string(output)), nil
}

func (a *App) emitGitCloneProgress(stage, message, targetPath string) {
	if a == nil || a.ctx == nil {
		return
	}
	message = strings.TrimSpace(message)
	if message == "" {
		return
	}
	wailsruntime.EventsEmit(a.ctx, "git:clone:progress", GitCloneProgress{
		Stage:      stage,
		Message:    message,
		TargetPath: targetPath,
		At:         time.Now().Format(time.RFC3339Nano),
	})
}

func scanGitProgressToken(data []byte, atEOF bool) (advance int, token []byte, err error) {
	for index, b := range data {
		if b == '\n' || b == '\r' {
			return index + 1, bytes.TrimSpace(data[:index]), nil
		}
	}
	if atEOF && len(data) > 0 {
		return len(data), bytes.TrimSpace(data), nil
	}
	return 0, nil, nil
}

func deriveGitRepoName(remote string) string {
	remote = strings.TrimSpace(remote)
	if parsed, err := url.Parse(remote); err == nil && parsed.Scheme != "" {
		base := strings.TrimSuffix(pathBase(parsed.Path), ".git")
		return sanitizeFilename(base)
	}
	if colon := strings.LastIndex(remote, ":"); colon >= 0 && colon < len(remote)-1 {
		return sanitizeFilename(strings.TrimSuffix(pathBase(remote[colon+1:]), ".git"))
	}
	return sanitizeFilename(strings.TrimSuffix(pathBase(remote), ".git"))
}

func pathBase(value string) string {
	value = strings.TrimRight(strings.TrimSpace(value), "/")
	if value == "" {
		return ""
	}
	index := strings.LastIndex(value, "/")
	if index >= 0 {
		return value[index+1:]
	}
	return value
}

func scanBrunoCollections(rootPath string) ([]GitCollectionCandidate, error) {
	rootPath = strings.TrimSpace(rootPath)
	if rootPath == "" {
		return nil, errors.New("scan path is required")
	}
	info, err := os.Stat(rootPath)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", rootPath)
	}
	rootPath = filepath.Clean(rootPath)
	candidates := []GitCollectionCandidate{}
	err = filepath.WalkDir(rootPath, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() {
			return nil
		}
		base := entry.Name()
		if path != rootPath && (base == ".git" || base == "node_modules" || base == "environments") {
			return filepath.SkipDir
		}
		if !looksLikeCollectionDir(path) {
			return nil
		}
		collection, err := readCollectionFromDisk(path)
		if err != nil {
			return err
		}
		candidates = append(candidates, GitCollectionCandidate{
			Name:         collection.Name,
			Path:         filepath.Clean(path),
			Format:       collection.Format,
			RequestCount: len(collection.Items),
		})
		if path != rootPath {
			return filepath.SkipDir
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		return strings.ToLower(candidates[i].Path) < strings.ToLower(candidates[j].Path)
	})
	return candidates, nil
}

func looksLikeCollectionDir(path string) bool {
	for _, name := range []string{"bruno.json", "opencollection.yml", "opencollection.yaml"} {
		if info, err := os.Stat(filepath.Join(path, name)); err == nil && !info.IsDir() {
			return true
		}
	}
	return false
}

func updateManagedGitIgnore(workspacePath, collectionPath string, add bool) error {
	return updateManagedGitIgnoreSecure(workspacePath, collectionPath, add)
}

func managedGitIgnoreEntries(content string) map[string]bool {
	entries := map[string]bool{}
	inBlock := false
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		switch trimmed {
		case "# LiteAPI managed Git-backed collections":
			inBlock = true
			continue
		case "# End LiteAPI managed Git-backed collections":
			inBlock = false
			continue
		}
		if inBlock && trimmed != "" && !strings.HasPrefix(trimmed, "#") {
			entries[trimmed] = true
		}
	}
	return entries
}

func replaceManagedGitIgnoreBlock(content string, entries map[string]bool) string {
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	kept := make([]string, 0, len(lines))
	inBlock := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "# LiteAPI managed Git-backed collections" {
			inBlock = true
			continue
		}
		if trimmed == "# End LiteAPI managed Git-backed collections" {
			inBlock = false
			continue
		}
		if !inBlock {
			kept = append(kept, line)
		}
	}
	for len(kept) > 0 && strings.TrimSpace(kept[len(kept)-1]) == "" {
		kept = kept[:len(kept)-1]
	}
	keys := make([]string, 0, len(entries))
	for entry := range entries {
		keys = append(keys, entry)
	}
	sort.Strings(keys)
	if len(keys) > 0 {
		if len(kept) > 0 {
			kept = append(kept, "")
		}
		kept = append(kept, "# LiteAPI managed Git-backed collections")
		kept = append(kept, keys...)
		kept = append(kept, "# End LiteAPI managed Git-backed collections")
	}
	if len(kept) == 0 {
		return ""
	}
	return strings.Join(kept, "\n") + "\n"
}

func collectionFromImport(payload ImportPayload) (Collection, error) {
	now := time.Now()
	name := strings.TrimSpace(payload.Name)
	if name == "" {
		name = "Imported Collection"
	}
	collection := Collection{
		ID:             newID("collection"),
		Name:           name,
		Format:         "json",
		Auth:           AuthConfig{Mode: "none"},
		SecurityConfig: CollectionSecurityConfig{JSSandboxMode: "safe"},
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	kind := strings.ToLower(payload.Kind)
	switch kind {
	case "bruno-json", "json", "bruno":
		var imported Collection
		if err := json.Unmarshal([]byte(payload.Content), &imported); err != nil {
			return Collection{}, err
		}
		imported.ID = collection.ID
		if imported.Name == "" {
			imported.Name = name
		}
		imported.SecurityConfig = normalizeCollectionSecurityConfig(imported.SecurityConfig)
		imported.CreatedAt = now
		imported.UpdatedAt = now
		return imported, nil
	case "bru":
		item, err := bru.Parse(payload.Content)
		if err != nil {
			return Collection{}, err
		}
		collection.Items = []RequestItem{item}
		return collection, nil
	case "postman":
		return importers.ImportPostman(payload.Content, name, payload.TranslatePostmanScripts)
	case "har":
		collection, _, err := importHAR(payload.Content, name)
		return collection, err
	case "insomnia":
		return importers.ImportInsomnia(payload.Content, name)
	case "swagger-2", "swagger2", "swagger":
		converted, err := convertSwagger2ToOpenAPI3(payload.Content)
		if err != nil {
			return Collection{}, err
		}
		return importers.ImportOpenAPI(converted, name, payload.GroupBy)
	case "openapi":
		return importers.ImportOpenAPI(payload.Content, name, payload.GroupBy)
	case "curl":
		collection, _, err := collectionFromCurlImport(payload.Content, name)
		return collection, err
	default:
		return Collection{}, fmt.Errorf("unsupported import kind %q", payload.Kind)
	}
}

func (a *App) openAPISyncInputs(collectionID string, options OpenAPISyncOptions) (Collection, string, string, string, error) {
	a.mu.Lock()
	if err := a.ensureReadyLocked(); err != nil {
		a.mu.Unlock()
		return Collection{}, "", "", "", err
	}
	collection, err := a.findCollectionLocked(collectionID)
	if err != nil {
		a.mu.Unlock()
		return Collection{}, "", "", "", err
	}
	collectionCopy := *collection
	defaultConfig := firstOpenAPISyncConfig(collectionCopy)
	sourceURL := strings.TrimSpace(options.SourceURL)
	if sourceURL == "" {
		sourceURL = defaultConfig.SourceURL
	}
	groupBy := strings.TrimSpace(options.GroupBy)
	if groupBy == "" {
		groupBy = defaultConfig.GroupBy
	}
	groupBy = normalizeOpenAPISyncGroupBy(groupBy)
	content := options.Content
	client := a.httpClient
	a.mu.Unlock()
	if strings.TrimSpace(content) == "" {
		fetched, err := fetchOpenAPISyncContent(collectionCopy.Path, sourceURL, client)
		if err != nil {
			return Collection{}, "", "", "", err
		}
		content = fetched
	}
	return collectionCopy, content, sourceURL, groupBy, nil
}

func (a *App) openAPILocalDriftInputs(collectionID string) (Collection, string, OpenAPISyncConfig, bool, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return Collection{}, "", OpenAPISyncConfig{}, false, err
	}
	collection, err := a.findCollectionLocked(collectionID)
	if err != nil {
		return Collection{}, "", OpenAPISyncConfig{}, false, err
	}
	if len(collection.OpenAPI) == 0 {
		return Collection{}, "", OpenAPISyncConfig{}, false, errors.New("OpenAPI sync is not connected")
	}
	collectionCopy := *collection
	collectionCopy.Items = append([]RequestItem(nil), collection.Items...)
	collectionCopy.Folders = append([]FolderConfig(nil), collection.Folders...)
	collectionCopy.Variables = append([]Variable(nil), collection.Variables...)
	config := firstOpenAPISyncConfig(collectionCopy)
	content, noStoredSpec, err := a.loadOpenAPISyncSpecLocked(collection.Path)
	if err != nil {
		return Collection{}, "", OpenAPISyncConfig{}, false, err
	}
	return collectionCopy, content, config, noStoredSpec, nil
}

func newOpenAPISyncConfig(sourceURL, groupBy, hash string) OpenAPISyncConfig {
	return OpenAPISyncConfig{
		SourceURL:         strings.TrimSpace(sourceURL),
		GroupBy:           normalizeOpenAPISyncGroupBy(groupBy),
		LastSyncDate:      time.Now().UTC().Format(time.RFC3339),
		SpecHash:          hash,
		AutoCheck:         true,
		AutoCheckInterval: 5,
	}
}

func newOpenAPISyncConfigPreservingSettings(sourceURL, groupBy, hash string, existing OpenAPISyncConfig) OpenAPISyncConfig {
	next := newOpenAPISyncConfig(sourceURL, groupBy, hash)
	existing = normalizeOpenAPISyncConfig(existing)
	next.AutoCheck = existing.AutoCheck
	next.AutoCheckInterval = normalizeOpenAPISyncAutoCheckInterval(existing.AutoCheckInterval)
	return next
}

func validateOpenAPISyncSource(sourceURL string) error {
	sourceURL = strings.TrimSpace(sourceURL)
	if sourceURL == "" {
		return errors.New("OpenAPI source URL or file path is required")
	}
	if parsed, err := url.Parse(sourceURL); err == nil && parsed.Scheme != "" {
		switch strings.ToLower(parsed.Scheme) {
		case "http", "https", "file":
			return nil
		default:
			return errors.New("invalid source: only http/https URLs and local file paths are allowed")
		}
	}
	return nil
}

func fetchOpenAPISyncContent(collectionPath, sourceURL string, client *http.Client) (string, error) {
	sourceURL = strings.TrimSpace(sourceURL)
	if sourceURL == "" {
		return "", errors.New("OpenAPI source URL or file path is required")
	}
	if err := validateOpenAPISyncSource(sourceURL); err != nil {
		return "", err
	}
	if parsed, err := url.Parse(sourceURL); err == nil && parsed.Scheme != "" {
		switch strings.ToLower(parsed.Scheme) {
		case "http", "https":
			if client == nil {
				client = http.DefaultClient
			}
			fetchURL := sourceURL
			separator := "?"
			if strings.Contains(fetchURL, "?") {
				separator = "&"
			}
			fetchURL += separator + "_=" + strconv.FormatInt(time.Now().UnixMilli(), 10)
			req, err := http.NewRequest(http.MethodGet, fetchURL, nil)
			if err != nil {
				return "", err
			}
			req.Header.Set("Cache-Control", "no-cache, no-store, must-revalidate")
			req.Header.Set("Pragma", "no-cache")
			res, err := client.Do(req)
			if err != nil {
				return "", fmt.Errorf("could not reach %s: %w", sourceURL, err)
			}
			defer func() { _ = res.Body.Close() }()
			if res.StatusCode < 200 || res.StatusCode >= 300 {
				return "", fmt.Errorf("failed to fetch spec: %d %s", res.StatusCode, http.StatusText(res.StatusCode))
			}
			data, err := io.ReadAll(io.LimitReader(res.Body, 16<<20))
			if err != nil {
				return "", err
			}
			return string(data), nil
		case "file":
			sourceURL = parsed.Path
		default:
			return "", errors.New("invalid source: only http/https URLs and local file paths are allowed")
		}
	}
	path := sourceURL
	if !filepath.IsAbs(path) && strings.TrimSpace(collectionPath) != "" {
		path = filepath.Join(collectionPath, path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("spec file not found at: %s", sourceURL)
		}
		return "", err
	}
	return string(data), nil
}

type openAPISpecMetadataEntry struct {
	Filename  string `json:"filename"`
	SourceURL string `json:"sourceUrl"`
}

func (a *App) openAPISyncSpecsDirLocked() string {
	if a.dataDir == "" {
		a.dataDir = defaultDataDir()
	}
	return filepath.Join(a.dataDir, "specs")
}

func (a *App) openAPISyncSpecMetadataPathLocked() string {
	return filepath.Join(a.openAPISyncSpecsDirLocked(), "metadata.json")
}

func (a *App) readOpenAPISyncSpecMetadataLocked() map[string][]openAPISpecMetadataEntry {
	path := a.openAPISyncSpecMetadataPathLocked()
	data, err := os.ReadFile(path)
	if err != nil {
		return map[string][]openAPISpecMetadataEntry{}
	}
	var meta map[string][]openAPISpecMetadataEntry
	if err := json.Unmarshal(data, &meta); err != nil || meta == nil {
		return map[string][]openAPISpecMetadataEntry{}
	}
	return meta
}

func (a *App) writeOpenAPISyncSpecMetadataLocked(meta map[string][]openAPISpecMetadataEntry) error {
	if meta == nil {
		meta = map[string][]openAPISpecMetadataEntry{}
	}
	path := a.openAPISyncSpecMetadataPathLocked()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

func (a *App) saveOpenAPISyncSpecLocked(collectionPath, sourceURL, content string) error {
	if strings.TrimSpace(collectionPath) == "" || strings.TrimSpace(content) == "" {
		return nil
	}
	specsDir := a.openAPISyncSpecsDirLocked()
	if err := os.MkdirAll(specsDir, 0o755); err != nil {
		return err
	}
	meta := a.readOpenAPISyncSpecMetadataLocked()
	key := filepath.Clean(collectionPath)
	entry := openAPISpecMetadataEntry{}
	if entries := meta[key]; len(entries) > 0 {
		entry = entries[0]
	}
	if strings.TrimSpace(entry.Filename) == "" {
		ext := ".json"
		if openapisync.OpenAPISyncSpecLooksYAML(content) {
			ext = ".yaml"
		}
		entry.Filename = newID("spec") + ext
	}
	entry.SourceURL = strings.TrimSpace(sourceURL)
	meta[key] = []openAPISpecMetadataEntry{entry}
	if err := os.WriteFile(filepath.Join(specsDir, entry.Filename), []byte(content), 0o600); err != nil {
		return err
	}
	return a.writeOpenAPISyncSpecMetadataLocked(meta)
}

func (a *App) loadOpenAPISyncSpecLocked(collectionPath string) (string, bool, error) {
	if strings.TrimSpace(collectionPath) == "" {
		return "", true, nil
	}
	meta := a.readOpenAPISyncSpecMetadataLocked()
	entries := meta[filepath.Clean(collectionPath)]
	if len(entries) == 0 || strings.TrimSpace(entries[0].Filename) == "" {
		return "", true, nil
	}
	specsDir := a.openAPISyncSpecsDirLocked()
	target := filepath.Clean(filepath.Join(specsDir, entries[0].Filename))
	if !pathInside(specsDir, target) {
		return "", true, nil
	}
	data, err := os.ReadFile(target)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", true, nil
		}
		return "", false, err
	}
	return string(data), false, nil
}

func (a *App) cleanupOpenAPISyncSpecLocked(collectionPath string) {
	meta := a.readOpenAPISyncSpecMetadataLocked()
	key := filepath.Clean(collectionPath)
	for _, entry := range meta[key] {
		if strings.TrimSpace(entry.Filename) != "" {
			_ = os.Remove(filepath.Join(a.openAPISyncSpecsDirLocked(), entry.Filename))
		}
	}
	delete(meta, key)
	_ = a.writeOpenAPISyncSpecMetadataLocked(meta)
}

const collectionFileCacheVersion = 1

type collectionFileCacheStore struct {
	Version     int                                 `json:"version"`
	Collections map[string]collectionFileCacheEntry `json:"collections"`
}

type collectionFileCacheEntry struct {
	Fingerprint string     `json:"fingerprint"`
	Collection  Collection `json:"collection"`
	CachedAt    time.Time  `json:"cachedAt"`
}

func (a *App) readCollectionFromDiskCachedLocked(collectionPath string) (Collection, error) {
	preferences := normalizePreferences(a.state.Preferences)
	if !preferences.Cache.File.Enabled {
		return readCollectionFromDisk(collectionPath)
	}
	collectionPath = filepath.Clean(collectionPath)
	fingerprint, err := collectionFileCacheFingerprint(collectionPath)
	if err != nil {
		return Collection{}, err
	}
	store := a.readCollectionFileCacheLocked()
	if entry, ok := store.Collections[collectionPath]; ok && entry.Fingerprint == fingerprint {
		collection := entry.Collection
		collection.Path = collectionPath
		return collection, nil
	}
	collection, err := readCollectionFromDisk(collectionPath)
	if err != nil {
		return Collection{}, err
	}
	store.Collections[collectionPath] = collectionFileCacheEntry{
		Fingerprint: fingerprint,
		Collection:  collection,
		CachedAt:    time.Now(),
	}
	if err := a.writeCollectionFileCacheLocked(store); err != nil {
		return Collection{}, err
	}
	return collection, nil
}

func (a *App) collectionFileCachePathLocked() string {
	if a.dataDir == "" {
		a.dataDir = defaultDataDir()
	}
	return filepath.Join(a.dataDir, "cache", "collections.json")
}

func (a *App) readCollectionFileCacheLocked() collectionFileCacheStore {
	store := collectionFileCacheStore{
		Version:     collectionFileCacheVersion,
		Collections: map[string]collectionFileCacheEntry{},
	}
	data, err := os.ReadFile(a.collectionFileCachePathLocked())
	if err != nil {
		return store
	}
	if err := json.Unmarshal(data, &store); err != nil || store.Version != collectionFileCacheVersion {
		return collectionFileCacheStore{
			Version:     collectionFileCacheVersion,
			Collections: map[string]collectionFileCacheEntry{},
		}
	}
	if store.Collections == nil {
		store.Collections = map[string]collectionFileCacheEntry{}
	}
	return store
}

func (a *App) writeCollectionFileCacheLocked(store collectionFileCacheStore) error {
	store.Version = collectionFileCacheVersion
	if store.Collections == nil {
		store.Collections = map[string]collectionFileCacheEntry{}
	}
	data, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return err
	}
	path := a.collectionFileCachePathLocked()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

func (a *App) fileCacheSizeLocked() (int64, error) {
	info, err := os.Stat(a.collectionFileCachePathLocked())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		return 0, err
	}
	if info.IsDir() {
		return 0, nil
	}
	return info.Size(), nil
}

func collectionFileCacheFingerprint(collectionPath string) (string, error) {
	info, err := os.Stat(collectionPath)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s is not a directory", collectionPath)
	}
	parts := []string{}
	err = filepath.WalkDir(collectionPath, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			base := entry.Name()
			if path != collectionPath && (base == "node_modules" || base == ".git") {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(collectionPath, path)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		parts = append(parts, fmt.Sprintf("%s:%d:%d:%x", filepath.ToSlash(rel), info.Size(), info.ModTime().UnixNano(), sum[:]))
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(parts)
	hash := sha256.New()
	for _, part := range parts {
		_, _ = io.WriteString(hash, part)
		_, _ = io.WriteString(hash, "\n")
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func collectionWatchFingerprint(collectionPath string) (string, error) {
	info, err := os.Stat(collectionPath)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s is not a directory", collectionPath)
	}
	ignorePatterns := collectionIgnorePatterns(collectionPath)
	parts := []string{}
	err = filepath.WalkDir(collectionPath, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path != collectionPath && collectionPathIgnored(collectionPath, path, ignorePatterns) {
				return filepath.SkipDir
			}
			return nil
		}
		if collectionPathIgnored(collectionPath, path, ignorePatterns) {
			return nil
		}
		if !collectionWatchFileAffectsTree(collectionPath, path) {
			return nil
		}
		rel, err := filepath.Rel(collectionPath, path)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		parts = append(parts, fmt.Sprintf("%s:%d:%d:%x", filepath.ToSlash(rel), info.Size(), info.ModTime().UnixNano(), sum[:]))
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(parts)
	hash := sha256.New()
	for _, part := range parts {
		_, _ = io.WriteString(hash, part)
		_, _ = io.WriteString(hash, "\n")
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func collectionIgnorePatterns(collectionPath string) []string {
	patterns := []string{"node_modules", ".git"}
	if ymlPath, ok := firstExistingCollectionYAMLPath(collectionPath); ok {
		if root, err := parseYAMLMapFile(ymlPath); err == nil {
			if extensions, ok := mapValue(root["extensions"]); ok {
				if bruno, ok := mapValue(extensions["bruno"]); ok {
					patterns = append(patterns, collectionIgnoreStringList(bruno["ignore"])...)
				}
			}
		}
		return normalizeCollectionIgnorePatterns(patterns)
	}
	configPath := filepath.Join(collectionPath, "bruno.json")
	data, err := os.ReadFile(configPath)
	if err != nil {
		return normalizeCollectionIgnorePatterns(patterns)
	}
	var config map[string]interface{}
	if err := json.Unmarshal(data, &config); err != nil {
		return normalizeCollectionIgnorePatterns(patterns)
	}
	patterns = append(patterns, collectionIgnoreStringList(config["ignore"])...)
	return normalizeCollectionIgnorePatterns(patterns)
}

func firstExistingCollectionYAMLPath(collectionPath string) (string, bool) {
	for _, name := range []string{"opencollection.yml", "opencollection.yaml"} {
		path := filepath.Join(collectionPath, name)
		if fileExists(path) {
			return path, true
		}
	}
	return "", false
}

func collectionIgnoreStringList(raw interface{}) []string {
	values, ok := listValue(raw)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if text := strings.TrimSpace(yamlScalarString(value)); text != "" {
			out = append(out, text)
		}
	}
	return out
}

func normalizeCollectionIgnorePatterns(patterns []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(patterns))
	for _, pattern := range patterns {
		pattern = strings.TrimSpace(strings.ReplaceAll(pattern, "\\", "/"))
		pattern = strings.TrimPrefix(pattern, "./")
		pattern = strings.TrimPrefix(pattern, "/")
		pattern = strings.TrimRight(pattern, "/")
		if pattern == "" || seen[pattern] {
			continue
		}
		seen[pattern] = true
		out = append(out, pattern)
	}
	return out
}

func collectionPathIgnored(collectionPath, path string, patterns []string) bool {
	rel, err := filepath.Rel(collectionPath, path)
	if err != nil || rel == "." {
		return false
	}
	rel = filepath.ToSlash(rel)
	if collectionPathHasDefaultIgnoredSegment(rel) {
		return true
	}
	for _, pattern := range patterns {
		if rel == pattern || strings.HasPrefix(rel, pattern) {
			return true
		}
	}
	return false
}

func collectionPathHasDefaultIgnoredSegment(rel string) bool {
	for _, segment := range strings.Split(filepath.ToSlash(rel), "/") {
		if segment == "node_modules" || segment == ".git" {
			return true
		}
	}
	return false
}

func collectionWatchFileAffectsTree(collectionPath, path string) bool {
	base := strings.ToLower(filepath.Base(path))
	if base == ".env" || strings.HasPrefix(base, ".env.") {
		return false
	}
	rel, err := filepath.Rel(collectionPath, path)
	if err != nil {
		return false
	}
	rel = filepath.ToSlash(rel)
	switch base {
	case "bruno.json", "collection.bru", "opencollection.yml", "opencollection.yaml", "folder.bru", "folder.yml", "folder.yaml":
		return true
	}
	ext := strings.ToLower(filepath.Ext(base))
	if strings.HasPrefix(rel, "environments/") {
		return ext == ".yml" || ext == ".yaml"
	}
	return ext == ".bru" || ext == ".yml" || ext == ".yaml"
}

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
			if proxy, ok := parseJSONProxyConfig(config["proxy"]); ok {
				collection.Proxy = transport.NormalizeProxyConfig(proxy)
			}
			if certs, ok := parseJSONClientCertificates(config["clientCertificates"]); ok {
				collection.ClientCertificates = transport.NormalizeClientCertificates(certs)
			}
			if presets, ok := parseCollectionPresets(config["presets"]); ok {
				collection.Presets = normalizeCollectionPresets(presets)
			}
			if protobuf, ok := parseCollectionProtobuf(config["protobuf"]); ok {
				collection.Protobuf = normalizeCollectionProtobuf(collection.Path, protobuf)
			}
			if openAPI := parseOpenAPISyncConfigs(config["openapi"]); len(openAPI) > 0 {
				collection.OpenAPI = openAPI
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
		config.Headers = parseYAMLKeyValues(headers, false)
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
		collection.Headers = parseYAMLKeyValues(headers, false)
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
	if proxy, ok := parseYAMLProxyConfig(config["proxy"]); ok {
		collection.Proxy = transport.NormalizeProxyConfig(proxy)
	}
	if certs, ok := parseYAMLClientCertificates(config["clientCertificates"]); ok {
		collection.ClientCertificates = transport.NormalizeClientCertificates(certs)
	}
	if presets, ok := parseCollectionPresets(config["presets"]); ok {
		collection.Presets = normalizeCollectionPresets(presets)
	}
	if protobuf, ok := parseCollectionProtobuf(config["protobuf"]); ok {
		collection.Protobuf = normalizeCollectionProtobuf(collection.Path, protobuf)
	}
	if extensions, ok := mapValue(root["extensions"]); ok {
		if bruno, ok := mapValue(extensions["bruno"]); ok {
			if openAPI := parseOpenAPISyncConfigs(bruno["openapi"]); len(openAPI) > 0 {
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

func scrubEnvironmentSecretValues(environments []Environment) []Environment {
	return bru.ScrubEnvironmentSecretValues(environments)
}
