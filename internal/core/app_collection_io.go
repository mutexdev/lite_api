package core

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/mutexdev/lite_api/internal/types"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/mutexdev/lite_api/internal/importers"
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
		// A recorded fingerprint says what we last WROTE here, not that the
		// file is still there. Collections are plain files in a directory the
		// user owns — this app ships a watcher because they get edited and
		// removed outside it — so the skip must confirm the file exists.
		// Without that check, once a file had been deleted externally every
		// later save producing identical bytes was skipped and the file stayed
		// missing while the app went on showing the request.
		if known == fingerprint {
			if _, err := os.Stat(path); err == nil {
				return nil
			}
			// Unreadable for any reason, including simply gone: fall through
			// and write, which reports the real error if there is one.
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
	types.EnsureRequestFilePaths(collection, requestFileExtensionForCollection(*collection))
	if err := a.storeCollectionEnvironmentSecretsLocked(collection); err != nil {
		return err
	}
	if collection.Format == "yml" {
		if err := a.writeCollectionFileLocked(filepath.Join(collection.Path, "opencollection.yml"), []byte(yamlstore.StringifyCollection(*collection))); err != nil {
			return err
		}
		for _, item := range collection.Items {
			content, err := yamlstore.StringifyRequest(item)
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
			return os.WriteFile(target, []byte(yamlstore.StringifyCollection(*collection)), 0o600)
		}
		return err
	}
	if strings.TrimSpace(string(data)) == "" {
		return os.WriteFile(target, []byte(yamlstore.StringifyCollection(*collection)), 0o600)
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
			return os.WriteFile(targetConfigPath, []byte(yamlstore.StringifyCollection(*cloned)), 0o600)
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
	folderPath := types.NormalizeFolderPathKey(folder.Path)
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
		content = yamlstore.StringifyFolder(folder)
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
	physicalPath := types.NormalizeFolderPathKey(firstNonEmpty(folder.Path, folder.DisplayPath, folder.Name))
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
	if folderPath := types.NormalizeFolderPathKey(item.FolderPath); folderPath != "" {
		if folderConfig, err := findFolderConfig(collection, folderPath); err == nil {
			folderPath = types.NormalizeFolderPathKey(firstNonEmpty(folderConfig.Path, folderConfig.DisplayPath, folderConfig.Name))
		}
		folder = filepath.Join(collection.Path, filepath.FromSlash(folderPath))
	}
	targetPath := filepath.Clean(filepath.Join(folder, filename+requestFileExtensionForCollection(*collection)))
	if !pathInside(collection.Path, targetPath) {
		return "", fmt.Errorf("request path %s escapes collection", targetPath)
	}
	return targetPath, nil
}

func collectionFromImport(payload ImportPayload) (Collection, error) {
	collection, _, err := collectionFromImportDetailed(payload)
	return collection, err
}

// collectionFromImportDetailed also returns the warnings an importer raised.
// US-054: a Postman collection can now import with a request skipped or a URL
// reconstructed, and the preview row has to be able to say so.
func collectionFromImportDetailed(payload ImportPayload) (Collection, []string, error) {
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
			return Collection{}, nil, err
		}
		imported.ID = collection.ID
		if imported.Name == "" {
			imported.Name = name
		}
		imported.SecurityConfig = normalizeCollectionSecurityConfig(imported.SecurityConfig)
		imported.CreatedAt = now
		imported.UpdatedAt = now
		return imported, nil, nil
	case "bru":
		item, err := bru.Parse(payload.Content)
		if err != nil {
			return Collection{}, nil, err
		}
		collection.Items = []RequestItem{item}
		return collection, nil, nil
	case "postman":
		return importers.ImportPostman(payload.Content, name, payload.TranslatePostmanScripts)
	case "har":
		collection, warnings, err := importers.ImportHAR(payload.Content, name)
		return collection, warnings, err
	case "insomnia":
		collection, err := importers.ImportInsomnia(payload.Content, name)
		return collection, nil, err
	case "swagger-2", "swagger2", "swagger":
		converted, err := importers.ConvertSwagger2ToOpenAPI3(payload.Content)
		if err != nil {
			return Collection{}, nil, err
		}
		collection, err := importers.ImportOpenAPI(converted, name, payload.GroupBy)
		return collection, nil, err
	case "openapi":
		collection, err := importers.ImportOpenAPI(payload.Content, name, payload.GroupBy)
		return collection, nil, err
	case "curl":
		collection, warnings, err := collectionFromCurlImport(payload.Content, name)
		return collection, warnings, err
	default:
		return Collection{}, nil, fmt.Errorf("unsupported import kind %q", payload.Kind)
	}
}
