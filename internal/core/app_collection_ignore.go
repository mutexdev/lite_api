package core

// Which paths a collection ignores, and whether a changed file affects its tree.
//
// Split out by AST: declarations are identified by the parser and copied
// verbatim from their source offsets.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

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
