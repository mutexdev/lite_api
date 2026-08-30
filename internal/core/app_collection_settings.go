package core

// The bound methods that update a collection or folder settings block.
//
// Split out of app.go by AST: declarations are identified by the parser
// and copied verbatim from their source offsets.

import (
	"fmt"
	"time"

	xport "github.com/mutexdev/lite_api/internal/transport"
	"github.com/mutexdev/lite_api/internal/types"
)

func (a *App) UpdateCollectionVariables(collectionID string, vars []Variable) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	collection, err := a.findCollectionLocked(collectionID)
	if err != nil {
		return AppState{}, err
	}
	collection.Variables = vars
	collection.UpdatedAt = time.Now()
	return a.state, a.markDirty(persistScopeState)
}

func (a *App) UpdateEnvironmentVariables(collectionID, environmentID string, vars []Variable) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return AppState{}, err
	}
	collection, err := a.findCollectionLocked(collectionID)
	if err != nil {
		return AppState{}, err
	}
	for index := range collection.Environments {
		if collection.Environments[index].ID != environmentID {
			continue
		}
		collection.Environments[index].Variables = vars
		collection.UpdatedAt = time.Now()
		// See CreateEnvironment: opencollection.yml stores environments too, so
		// exempting yml collections here made every variable edit revert the
		// moment the file was read back.
		if err := a.writeCollectionFilesLocked(collection); err != nil {
			return AppState{}, err
		}
		return a.state, a.markDirty(persistScopeState)
	}
	return AppState{}, fmt.Errorf("environment %s not found", environmentID)
}

func (a *App) UpdateCollectionHeaders(collectionID string, headers []KeyValue) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	collection, err := a.findCollectionLocked(collectionID)
	if err != nil {
		return AppState{}, err
	}
	collection.Headers = headers
	collection.UpdatedAt = time.Now()
	return a.state, a.markDirty(persistScopeState)
}

func (a *App) UpdateCollectionAuth(collectionID string, auth AuthConfig) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	collection, err := a.findCollectionLocked(collectionID)
	if err != nil {
		return AppState{}, err
	}
	collection.Auth = auth
	collection.UpdatedAt = time.Now()
	return a.state, a.markDirty(persistScopeState)
}

func (a *App) UpdateCollectionProxy(collectionID string, proxy ProxyConfig) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	collection, err := a.findCollectionLocked(collectionID)
	if err != nil {
		return AppState{}, err
	}
	collection.Proxy = xport.NormalizeProxyConfig(proxy)
	collection.UpdatedAt = time.Now()
	return a.state, a.markDirty(persistScopeState)
}

func (a *App) UpdateCollectionSecurityConfig(collectionID string, config CollectionSecurityConfig) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	collection, err := a.findCollectionLocked(collectionID)
	if err != nil {
		return AppState{}, err
	}
	collection.SecurityConfig = normalizeCollectionSecurityConfig(config)
	collection.UpdatedAt = time.Now()
	return a.state, a.markDirty(persistScopeState)
}

func (a *App) UpdateCollectionClientCertificates(collectionID string, certs []ClientCertificateConfig) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	collection, err := a.findCollectionLocked(collectionID)
	if err != nil {
		return AppState{}, err
	}
	collection.ClientCertificates = xport.NormalizeClientCertificateRows(certs)
	collection.UpdatedAt = time.Now()
	return a.state, a.markDirty(persistScopeState)
}

func (a *App) UpdateCollectionPresets(collectionID string, presets CollectionPresets) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	collection, err := a.findCollectionLocked(collectionID)
	if err != nil {
		return AppState{}, err
	}
	collection.Presets = types.NormalizeCollectionPresets(presets)
	collection.UpdatedAt = time.Now()
	return a.state, a.markDirty(persistScopeState)
}

func (a *App) UpdateCollectionProtobuf(collectionID string, protobuf CollectionProtobufConfig) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	collection, err := a.findCollectionLocked(collectionID)
	if err != nil {
		return AppState{}, err
	}
	collection.Protobuf = types.NormalizeCollectionProtobuf(collection.Path, protobuf)
	collection.UpdatedAt = time.Now()
	return a.state, a.markDirty(persistScopeState)
}

func (a *App) UpdateCollectionDocs(collectionID string, docs string) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	collection, err := a.findCollectionLocked(collectionID)
	if err != nil {
		return AppState{}, err
	}
	collection.Docs = docs
	collection.UpdatedAt = time.Now()
	return a.state, a.markDirty(persistScopeState)
}

func (a *App) UpdateCollectionScripts(collectionID string, preScript, postScript, tests string) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	collection, err := a.findCollectionLocked(collectionID)
	if err != nil {
		return AppState{}, err
	}
	collection.PreScript = preScript
	collection.PostScript = postScript
	collection.Tests = tests
	collection.UpdatedAt = time.Now()
	return a.state, a.markDirty(persistScopeState)
}

func (a *App) UpdateFolderSettings(collectionID, folderPath string, updated FolderConfig) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return AppState{}, err
	}
	collection, err := a.findCollectionLocked(collectionID)
	if err != nil {
		return AppState{}, err
	}
	folder, err := findFolderConfig(collection, folderPath)
	if err != nil {
		return AppState{}, err
	}
	mergeFolderSettingsUpdate(folder, updated)
	collection.UpdatedAt = time.Now()
	if err := a.writeFolderConfigLocked(collection, *folder); err != nil {
		return AppState{}, err
	}
	a.notify("success", "Saved folder settings for "+firstNonEmpty(folder.DisplayPath, folder.Name, folder.Path))
	return a.state, a.markDirty(persistScopeState)
}

func normalizeCollectionSecurityConfig(config CollectionSecurityConfig) CollectionSecurityConfig {
	config.JSSandboxMode = normalizeJSSandboxMode(config.JSSandboxMode)
	return config
}

func collectionJSSandboxMode(collection Collection) string {
	return normalizeJSSandboxMode(collection.SecurityConfig.JSSandboxMode)
}
