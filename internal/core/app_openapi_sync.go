package core

import (
	"errors"
	"strings"
	"time"

	"github.com/mutexdev/lite_api/internal/openapisync"
)

func (a *App) ConnectOpenAPISync(collectionID string, options OpenAPISyncOptions) (AppState, error) {
	collection, content, sourceURL, groupBy, err := a.openAPISyncInputs(collectionID, options)
	if err != nil {
		return AppState{}, err
	}
	hash, _, err := openapisync.ValidateOpenAPISyncSpec(content)
	if err != nil {
		return AppState{}, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return AppState{}, err
	}
	current, err := a.findCollectionLocked(collection.ID)
	if err != nil {
		return AppState{}, err
	}
	current.OpenAPI = []OpenAPISyncConfig{newOpenAPISyncConfig(sourceURL, groupBy, hash)}
	if err := a.saveOpenAPISyncSpecLocked(current.Path, sourceURL, content); err != nil {
		return AppState{}, err
	}
	if err := a.writeCollectionFilesLocked(current); err != nil {
		return AppState{}, err
	}
	a.notify("success", "OpenAPI sync connected")
	return a.state, a.markDirty(persistScopeState)
}

func (a *App) CheckOpenAPISync(collectionID string, options OpenAPISyncOptions) (OpenAPISyncResult, error) {
	collection, content, sourceURL, groupBy, err := a.openAPISyncInputs(collectionID, options)
	if err != nil {
		return OpenAPISyncResult{}, err
	}
	specCollection, hash, doc, err := openapisync.OpenAPISyncCollectionFromContent(content, collection.Name, groupBy)
	if err != nil {
		return OpenAPISyncResult{}, err
	}
	return openapisync.BuildOpenAPISyncResult(collection, specCollection, sourceURL, groupBy, hash, doc), nil
}

func (a *App) CheckOpenAPIUpdates(collectionID string) (OpenAPISyncUpdateCheckResult, error) {
	collection, content, sourceURL, _, err := a.openAPISyncInputs(collectionID, OpenAPISyncOptions{})
	if err != nil {
		return OpenAPISyncUpdateCheckResult{}, err
	}
	remoteHash, _, err := openapisync.ValidateOpenAPISyncSpec(content)
	if err != nil {
		return OpenAPISyncUpdateCheckResult{}, err
	}
	config := firstOpenAPISyncConfig(collection)
	return OpenAPISyncUpdateCheckResult{
		SourceURL:      sourceURL,
		StoredSpecHash: config.SpecHash,
		RemoteSpecHash: remoteHash,
		HasUpdates:     strings.TrimSpace(config.SpecHash) == "" || config.SpecHash != remoteHash,
		CheckedAt:      time.Now().UTC().Format(time.RFC3339),
	}, nil
}

func (a *App) GetOpenAPISyncSpec(collectionID string) (OpenAPISyncSpecViewResult, error) {
	a.mu.Lock()
	if err := a.ensureReadyLocked(); err != nil {
		a.mu.Unlock()
		return OpenAPISyncSpecViewResult{}, err
	}
	collection, err := a.findCollectionLocked(collectionID)
	if err != nil {
		a.mu.Unlock()
		return OpenAPISyncSpecViewResult{}, err
	}
	if len(collection.OpenAPI) == 0 {
		a.mu.Unlock()
		return OpenAPISyncSpecViewResult{}, errors.New("OpenAPI sync is not connected")
	}
	config := firstOpenAPISyncConfig(*collection)
	collectionPath := collection.Path
	sourceURL := config.SourceURL
	content, noStoredSpec, err := a.loadOpenAPISyncSpecLocked(collection.Path)
	client := a.httpClient
	a.mu.Unlock()
	if err != nil {
		return OpenAPISyncSpecViewResult{}, err
	}
	result := OpenAPISyncSpecViewResult{
		SourceURL:    sourceURL,
		Content:      content,
		FromCache:    !noStoredSpec,
		NoStoredSpec: noStoredSpec,
	}
	if !noStoredSpec {
		return result, nil
	}
	if strings.TrimSpace(sourceURL) == "" {
		return result, errors.New("spec file not found; sync your collection first")
	}
	fetched, err := fetchOpenAPISyncContent(collectionPath, sourceURL, client)
	if err != nil {
		return result, err
	}
	if _, _, err := openapisync.ValidateOpenAPISyncSpec(fetched); err != nil {
		return result, err
	}
	result.Content = fetched
	result.Fetched = true
	return result, nil
}

func (a *App) GetOpenAPISyncSpecDiff(collectionID string, options OpenAPISyncOptions) (OpenAPISyncSpecDiffResult, error) {
	collection, newContent, sourceURL, groupBy, err := a.openAPISyncInputs(collectionID, options)
	if err != nil {
		return OpenAPISyncSpecDiffResult{}, err
	}
	if len(collection.OpenAPI) == 0 {
		return OpenAPISyncSpecDiffResult{}, errors.New("OpenAPI sync is not connected")
	}
	specCollection, newHash, doc, err := openapisync.OpenAPISyncCollectionFromContent(newContent, collection.Name, groupBy)
	if err != nil {
		return OpenAPISyncSpecDiffResult{}, err
	}
	a.mu.Lock()
	storedContent, noStoredSpec, err := a.loadOpenAPISyncSpecLocked(collection.Path)
	a.mu.Unlock()
	if err != nil {
		return OpenAPISyncSpecDiffResult{}, err
	}
	storedHash := firstOpenAPISyncConfig(collection).SpecHash
	if strings.TrimSpace(storedContent) != "" {
		if hash, _, err := openapisync.ValidateOpenAPISyncSpec(storedContent); err == nil {
			storedHash = hash
		}
	}
	check := openapisync.BuildOpenAPISyncResult(collection, specCollection, sourceURL, groupBy, newHash, doc)
	return OpenAPISyncSpecDiffResult{
		SourceURL:      sourceURL,
		StoredContent:  storedContent,
		NewContent:     newContent,
		NoStoredSpec:   noStoredSpec,
		StoredSpecHash: storedHash,
		NewSpecHash:    newHash,
		Added:          check.Added,
		Updated:        check.Updated,
		Removed:        check.Removed,
		Unchanged:      check.Unchanged,
		Changes:        check.Changes,
		Lines:          openapisync.BuildOpenAPISpecDiffLines(storedContent, newContent),
	}, nil
}

func (a *App) UpdateOpenAPISyncConfig(collectionID string, config OpenAPISyncConfig) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return AppState{}, err
	}
	ws, collection, err := a.findCollectionWithWorkspaceLocked(collectionID)
	if err != nil {
		return AppState{}, err
	}
	if collection.NotFoundLocally {
		return AppState{}, errors.New("collection is not cloned locally")
	}
	if strings.TrimSpace(config.SourceURL) == "" {
		return AppState{}, errors.New("OpenAPI source URL or file path is required")
	}
	if err := validateOpenAPISyncSource(config.SourceURL); err != nil {
		return AppState{}, err
	}
	existing := firstOpenAPISyncConfig(*collection)
	existing.SourceURL = strings.TrimSpace(config.SourceURL)
	if strings.TrimSpace(config.GroupBy) != "" {
		existing.GroupBy = normalizeOpenAPISyncGroupBy(config.GroupBy)
	}
	existing.AutoCheck = config.AutoCheck
	existing.AutoCheckInterval = normalizeOpenAPISyncAutoCheckInterval(config.AutoCheckInterval)
	collection.OpenAPI = []OpenAPISyncConfig{normalizeOpenAPISyncConfig(existing)}
	now := time.Now()
	collection.UpdatedAt = now
	ws.UpdatedAt = now
	if err := a.writeCollectionFilesLocked(collection); err != nil {
		return AppState{}, err
	}
	a.notify("success", "OpenAPI sync settings saved")
	return a.state, a.markDirty(persistScopeState)
}

func (a *App) ApplyOpenAPISync(collectionID string, options OpenAPISyncOptions) (AppState, error) {
	collection, content, sourceURL, groupBy, err := a.openAPISyncInputs(collectionID, options)
	if err != nil {
		return AppState{}, err
	}
	specCollection, hash, _, err := openapisync.OpenAPISyncCollectionFromContent(content, collection.Name, groupBy)
	if err != nil {
		return AppState{}, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return AppState{}, err
	}
	current, err := a.findCollectionLocked(collectionID)
	if err != nil {
		return AppState{}, err
	}
	removedIDs := openapisync.ApplyOpenAPISyncToCollection(current, specCollection, options)
	a.removeOpenTabsForRequestIDsLocked(collectionID, removedIDs)
	current.OpenAPI = []OpenAPISyncConfig{newOpenAPISyncConfigPreservingSettings(sourceURL, groupBy, hash, firstOpenAPISyncConfig(*current))}
	if err := a.saveOpenAPISyncSpecLocked(current.Path, sourceURL, content); err != nil {
		return AppState{}, err
	}
	if err := a.writeCollectionFilesLocked(current); err != nil {
		return AppState{}, err
	}
	a.notify("success", "OpenAPI sync applied")
	return a.state, a.markDirty(persistScopeState)
}

func (a *App) CheckOpenAPILocalDrift(collectionID string) (OpenAPILocalDriftResult, error) {
	collection, content, config, noStoredSpec, err := a.openAPILocalDriftInputs(collectionID)
	if err != nil {
		return OpenAPILocalDriftResult{}, err
	}
	result := OpenAPILocalDriftResult{
		SourceURL:    config.SourceURL,
		GroupBy:      normalizeOpenAPISyncGroupBy(config.GroupBy),
		LastSyncDate: config.LastSyncDate,
		NoStoredSpec: noStoredSpec,
	}
	if noStoredSpec {
		return result, nil
	}
	specCollection, _, _, err := openapisync.OpenAPISyncCollectionFromContent(content, collection.Name, result.GroupBy)
	if err != nil {
		return OpenAPILocalDriftResult{}, err
	}
	return openapisync.BuildOpenAPILocalDriftResult(collection, specCollection, config), nil
}

func (a *App) ApplyOpenAPILocalDrift(collectionID string, options OpenAPILocalDriftOptions) (AppState, error) {
	collection, content, config, noStoredSpec, err := a.openAPILocalDriftInputs(collectionID)
	if err != nil {
		return AppState{}, err
	}
	if noStoredSpec {
		return AppState{}, errors.New("OpenAPI stored spec is missing")
	}
	specCollection, _, _, err := openapisync.OpenAPISyncCollectionFromContent(content, collection.Name, config.GroupBy)
	if err != nil {
		return AppState{}, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return AppState{}, err
	}
	current, err := a.findCollectionLocked(collectionID)
	if err != nil {
		return AppState{}, err
	}
	removedIDs, err := openapisync.ApplyOpenAPILocalDriftToCollection(current, specCollection, options)
	if err != nil {
		return AppState{}, err
	}
	a.removeOpenTabsForRequestIDsLocked(collectionID, removedIDs)
	if err := a.writeCollectionFilesLocked(current); err != nil {
		return AppState{}, err
	}
	a.notify("success", "OpenAPI collection changes applied")
	return a.state, a.markDirty(persistScopeState)
}

func (a *App) DisconnectOpenAPISync(collectionID string) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return AppState{}, err
	}
	collection, err := a.findCollectionLocked(collectionID)
	if err != nil {
		return AppState{}, err
	}
	a.cleanupOpenAPISyncSpecLocked(collection.Path)
	collection.OpenAPI = nil
	if err := a.writeCollectionFilesLocked(collection); err != nil {
		return AppState{}, err
	}
	a.notify("success", "OpenAPI sync disconnected")
	return a.state, a.markDirty(persistScopeState)
}
