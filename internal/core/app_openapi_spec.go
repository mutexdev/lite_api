package core

// The OpenAPI spec side of collection I/O: sync inputs, spec fetching and metadata.
//
// Split out of app_collection_io.go by AST: declarations are identified by the parser
// and copied verbatim from their source offsets.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mutexdev/lite_api/internal/openapisync"
)

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
