package core

// The on-disk cache of OpenAPI specs a synced collection keeps.
//
// Split out of app_collection_io.go by AST: declarations are identified by the parser
// and copied verbatim from their source offsets.

import (
	"errors"
	"path/filepath"
	"strings"
)

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
