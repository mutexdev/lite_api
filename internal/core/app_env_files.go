package core

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/mutexdev/lite_api/internal/store/bru"
	"github.com/mutexdev/lite_api/internal/store/yamlstore"
)

func readCollectionEnvironments(collectionPath string, ignorePatterns []string) ([]Environment, error) {
	envPath := filepath.Join(collectionPath, "environments")
	info, err := os.Stat(envPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	if !info.IsDir() {
		return nil, nil
	}
	entries, err := os.ReadDir(envPath)
	if err != nil {
		return nil, err
	}
	environments := []Environment{}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		path := filepath.Join(envPath, entry.Name())
		if collectionPathIgnored(collectionPath, path, ignorePatterns) {
			continue
		}
		ext := strings.ToLower(filepath.Ext(path))
		switch ext {
		case ".bru":
			content, err := os.ReadFile(path)
			if err != nil {
				return nil, err
			}
			environments = append(environments, parseBruEnvironmentFile(strings.TrimSuffix(entry.Name(), ext), string(content)))
		case ".yml", ".yaml":
			env, err := parseYAMLEnvironmentFile(path)
			if err != nil {
				return nil, err
			}
			environments = append(environments, env)
		}
	}
	sort.SliceStable(environments, func(i, j int) bool {
		return strings.ToLower(environments[i].Name) < strings.ToLower(environments[j].Name)
	})
	return environments, nil
}

func readWorkspaceGlobalEnvironments(workspacePath string) ([]Environment, error) {
	envPath := filepath.Join(workspacePath, "environments")
	info, err := os.Stat(envPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	if !info.IsDir() {
		return nil, nil
	}
	entries, err := os.ReadDir(envPath)
	if err != nil {
		return nil, err
	}
	environments := []Environment{}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		path := filepath.Join(envPath, entry.Name())
		ext := strings.ToLower(filepath.Ext(path))
		switch ext {
		case ".bru":
			content, err := os.ReadFile(path)
			if err != nil {
				return nil, err
			}
			env := parseBruEnvironmentFile(strings.TrimSuffix(entry.Name(), ext), string(content))
			env.ID = deterministicID("global-env", filepath.ToSlash(path))
			environments = append(environments, env)
		case ".yml", ".yaml":
			env, err := parseYAMLEnvironmentFile(path)
			if err != nil {
				return nil, err
			}
			env.ID = deterministicID("global-env", filepath.ToSlash(path))
			environments = append(environments, env)
		}
	}
	sort.SliceStable(environments, func(i, j int) bool {
		return strings.ToLower(environments[i].Name) < strings.ToLower(environments[j].Name)
	})
	return environments, nil
}

func migrateWorkspaceActiveGlobalEnvironmentFromConfig(workspace *Workspace) (bool, error) {
	if workspace == nil || strings.TrimSpace(workspace.Path) == "" {
		return false, nil
	}
	configPath := filepath.Join(workspace.Path, "workspace.yml")
	data, err := os.ReadFile(configPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return false, nil
	}
	root := yamlDocumentMappingRoot(&doc)
	legacyUID, ok := yamlMappingScalar(root, "activeEnvironmentUid")
	if !ok || strings.TrimSpace(legacyUID) == "" {
		return false, nil
	}
	if environmentID, ok, err := workspaceGlobalEnvironmentIDForBrunoUID(workspace.Path, strings.TrimSpace(legacyUID)); err != nil {
		return false, err
	} else if ok {
		workspace.ActiveGlobalEnvironmentID = environmentID
	}
	if yamlRemoveMappingKey(root, "activeEnvironmentUid") {
		updated, err := yaml.Marshal(&doc)
		if err != nil {
			return false, err
		}
		if err := os.WriteFile(configPath, updated, 0o600); err != nil {
			return false, err
		}
	}
	return true, nil
}

func workspaceGlobalEnvironmentIDForBrunoUID(workspacePath, uid string) (string, bool, error) {
	envPath := filepath.Join(workspacePath, "environments")
	entries, err := os.ReadDir(envPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", false, nil
		}
		return "", false, err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		path := filepath.Join(envPath, entry.Name())
		ext := strings.ToLower(filepath.Ext(path))
		if ext != ".bru" && ext != ".yml" && ext != ".yaml" {
			continue
		}
		if bru.BrunoWorkspaceEnvironmentUIDForPath(path) == uid {
			return deterministicID("global-env", filepath.ToSlash(path)), true, nil
		}
	}
	return "", false, nil
}

func yamlDocumentMappingRoot(doc *yaml.Node) *yaml.Node {
	if doc == nil {
		return nil
	}
	root := doc
	if root.Kind == yaml.DocumentNode && len(root.Content) > 0 {
		root = root.Content[0]
	}
	if root.Kind != yaml.MappingNode {
		return nil
	}
	return root
}

func yamlMappingScalar(root *yaml.Node, key string) (string, bool) {
	if root == nil || root.Kind != yaml.MappingNode {
		return "", false
	}
	for index := 0; index+1 < len(root.Content); index += 2 {
		if root.Content[index].Value == key {
			return yamlScalarString(root.Content[index+1].Value), true
		}
	}
	return "", false
}

func yamlRemoveMappingKey(root *yaml.Node, key string) bool {
	if root == nil || root.Kind != yaml.MappingNode {
		return false
	}
	for index := 0; index+1 < len(root.Content); index += 2 {
		if root.Content[index].Value == key {
			root.Content = append(root.Content[:index], root.Content[index+2:]...)
			return true
		}
	}
	return false
}

func parseBruEnvironmentFile(name, content string) Environment {
	sections := bru.ParseSections(content)
	env := Environment{ID: newID("env"), Name: name, Color: bru.ParseBruTopLevelScalar(content, "color"), Variables: []Variable{}}
	if vars, ok := sections["vars"]; ok {
		env.Variables = bru.ParseVariables(vars, false)
	}
	if secrets, ok := sections["vars:secret"]; ok {
		env.Variables = bru.MergeSecretVariables(env.Variables, bru.ParseVariables(secrets, true))
	}
	return env
}

func parseYAMLEnvironmentFile(path string) (Environment, error) {
	root, err := parseYAMLMapFile(path)
	if err != nil {
		return Environment{}, err
	}
	return yamlstore.EnvironmentFromYAMLMap(root, strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))), nil
}

func writeGlobalEnvironmentExportResult(result GlobalEnvironmentExportResult, targetPath string) ([]string, error) {
	targetPath = strings.TrimSpace(targetPath)
	if targetPath == "" {
		return nil, errors.New("export target path is required")
	}
	targetPath = filepath.Clean(targetPath)
	if result.Format == "folder" {
		if len(result.Files) == 0 {
			return nil, errors.New("folder export has no files")
		}
		if err := os.MkdirAll(targetPath, 0o755); err != nil {
			return nil, err
		}
		written := make([]string, 0, len(result.Files))
		for _, file := range result.Files {
			name := filepath.Base(strings.TrimSpace(file.Name))
			if name == "" || name == "." || name == string(filepath.Separator) {
				return nil, errors.New("folder export contains an invalid file name")
			}
			path := filepath.Join(targetPath, name)
			if err := os.WriteFile(path, []byte(file.Content), 0o600); err != nil {
				return nil, err
			}
			written = append(written, path)
		}
		return written, nil
	}
	if result.Content == "" {
		return nil, errors.New("global environment export content is empty")
	}
	if parent := filepath.Dir(targetPath); parent != "" && parent != "." {
		if err := os.MkdirAll(parent, 0o755); err != nil {
			return nil, err
		}
	}
	if err := os.WriteFile(targetPath, []byte(result.Content), 0o600); err != nil {
		return nil, err
	}
	return []string{targetPath}, nil
}

func expandUserExportPath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", errors.New("export target path is required")
	}
	if path == "~" || strings.HasPrefix(path, "~/") || strings.HasPrefix(path, `~\`) {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return "", errors.New("home directory is unavailable")
		}
		if path == "~" {
			return home, nil
		}
		path = filepath.Join(home, strings.TrimLeft(path[1:], `/\`))
	}
	return filepath.Clean(path), nil
}

func (a *App) defaultSaveDialogDirectory() string {
	if a == nil || strings.TrimSpace(a.dataDir) == "" {
		return ""
	}
	info, err := os.Stat(a.dataDir)
	if err != nil || !info.IsDir() {
		return ""
	}
	return a.dataDir
}

func defaultDataDir() string {
	if fromEnv := os.Getenv("LITEAPI_DATA_DIR"); fromEnv != "" {
		return fromEnv
	}
	base, err := os.UserConfigDir()
	if err != nil || base == "" {
		base = os.TempDir()
	}
	return filepath.Join(base, "LiteAPI")
}
