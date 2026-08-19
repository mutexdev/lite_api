package core

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/mutexdev/lite_api/internal/interp"
	"github.com/mutexdev/lite_api/internal/scripting"
	"github.com/mutexdev/lite_api/internal/store/bru"
	"github.com/mutexdev/lite_api/internal/store/yamlstore"
)

func (a *App) CreateEnvironment(collectionID, name string) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return AppState{}, err
	}
	collection, err := a.findCollectionLocked(collectionID)
	if err != nil {
		return AppState{}, err
	}
	env := Environment{
		ID:    newID("env"),
		Name:  strings.TrimSpace(name),
		Color: "#2f8cff",
		Variables: []Variable{
			{ID: newID("var"), Name: "host", Value: "https://httpbin.org", DataType: "string", Enabled: true},
			{ID: newID("var"), Name: "token", Value: "secret-token", DataType: "string", Enabled: true, Secret: true},
		},
	}
	if env.Name == "" {
		env.Name = "Development"
	}
	collection.Environments = append(collection.Environments, env)
	collection.UpdatedAt = time.Now()
	if collection.Format != "yml" {
		if err := a.writeCollectionFilesLocked(collection); err != nil {
			return AppState{}, err
		}
	}
	a.notify("success", "Environment created: "+env.Name)
	return a.state, a.markDirty(persistScopeState)
}

func (a *App) CreateGlobalEnvironment(workspaceID, name string) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return AppState{}, err
	}
	ws, err := a.findWorkspaceLocked(workspaceID)
	if err != nil {
		return AppState{}, err
	}
	env := Environment{
		ID:        newID("global-env"),
		Name:      strings.TrimSpace(name),
		Color:     "#2f8cff",
		Variables: []Variable{},
	}
	if env.Name == "" {
		env.Name = "Global"
	}
	env.Name = scripting.UniqueEnvironmentName(ws.GlobalEnvironments, env.Name)
	ws.GlobalEnvironments = append(ws.GlobalEnvironments, env)
	ws.ActiveGlobalEnvironmentID = env.ID
	ws.UpdatedAt = time.Now()
	if err := a.writeWorkspaceGlobalEnvironmentFilesLocked(ws); err != nil {
		return AppState{}, err
	}
	a.notify("success", "Global environment created: "+env.Name)
	return a.state, a.markDirty(persistScopeState)
}

func (a *App) SetActiveGlobalEnvironment(workspaceID, environmentID string) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return AppState{}, err
	}
	ws, err := a.findWorkspaceLocked(workspaceID)
	if err != nil {
		return AppState{}, err
	}
	if environmentID != "" && !scripting.WorkspaceHasGlobalEnvironment(*ws, environmentID) {
		return AppState{}, fmt.Errorf("global environment %s not found", environmentID)
	}
	ws.ActiveGlobalEnvironmentID = environmentID
	ws.UpdatedAt = time.Now()
	return a.state, a.markDirty(persistScopeState)
}

func (a *App) UpdateGlobalEnvironment(workspaceID, environmentID, name, color string) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return AppState{}, err
	}
	ws, err := a.findWorkspaceLocked(workspaceID)
	if err != nil {
		return AppState{}, err
	}
	for index := range ws.GlobalEnvironments {
		if ws.GlobalEnvironments[index].ID != environmentID {
			continue
		}
		if strings.TrimSpace(name) != "" {
			ws.GlobalEnvironments[index].Name = strings.TrimSpace(name)
		}
		if strings.TrimSpace(color) != "" {
			ws.GlobalEnvironments[index].Color = strings.TrimSpace(color)
		}
		ws.UpdatedAt = time.Now()
		if err := a.writeWorkspaceGlobalEnvironmentFilesLocked(ws); err != nil {
			return AppState{}, err
		}
		return a.state, a.markDirty(persistScopeState)
	}
	return AppState{}, fmt.Errorf("global environment %s not found", environmentID)
}

func (a *App) UpdateGlobalEnvironmentVariables(workspaceID, environmentID string, vars []Variable) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return AppState{}, err
	}
	ws, err := a.findWorkspaceLocked(workspaceID)
	if err != nil {
		return AppState{}, err
	}
	for index := range ws.GlobalEnvironments {
		if ws.GlobalEnvironments[index].ID != environmentID {
			continue
		}
		ws.GlobalEnvironments[index].Variables = vars
		ws.UpdatedAt = time.Now()
		if err := a.writeWorkspaceGlobalEnvironmentFilesLocked(ws); err != nil {
			return AppState{}, err
		}
		return a.state, a.markDirty(persistScopeState)
	}
	return AppState{}, fmt.Errorf("global environment %s not found", environmentID)
}

func (a *App) DeleteGlobalEnvironment(workspaceID, environmentID string) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return AppState{}, err
	}
	ws, err := a.findWorkspaceLocked(workspaceID)
	if err != nil {
		return AppState{}, err
	}
	next := ws.GlobalEnvironments[:0]
	removed := false
	for _, env := range ws.GlobalEnvironments {
		if env.ID == environmentID {
			removed = true
			continue
		}
		next = append(next, env)
	}
	if !removed {
		return AppState{}, fmt.Errorf("global environment %s not found", environmentID)
	}
	ws.GlobalEnvironments = next
	if ws.ActiveGlobalEnvironmentID == environmentID {
		ws.ActiveGlobalEnvironmentID = ""
		if len(ws.GlobalEnvironments) > 0 {
			ws.ActiveGlobalEnvironmentID = ws.GlobalEnvironments[0].ID
		}
	}
	ws.UpdatedAt = time.Now()
	if err := a.writeWorkspaceGlobalEnvironmentFilesLocked(ws); err != nil {
		return AppState{}, err
	}
	a.notify("info", "Global environment deleted")
	return a.state, a.markDirty(persistScopeState)
}

func (a *App) CopyGlobalEnvironment(workspaceID, environmentID string) (AppState, error) {
	return a.CopyGlobalEnvironmentAs(workspaceID, environmentID, "")
}

func (a *App) CopyGlobalEnvironmentAs(workspaceID, environmentID, name string) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return AppState{}, err
	}
	ws, err := a.findWorkspaceLocked(workspaceID)
	if err != nil {
		return AppState{}, err
	}
	for _, env := range ws.GlobalEnvironments {
		if env.ID != environmentID {
			continue
		}
		copyEnv := scripting.CloneEnvironmentWithNewIDs(env, "global-env")
		copyName := strings.TrimSpace(name)
		if copyName == "" {
			copyName = env.Name + " - Copy"
		}
		copyEnv.Name = scripting.UniqueEnvironmentName(ws.GlobalEnvironments, copyName)
		copyEnv.Color = ""
		ws.GlobalEnvironments = append(ws.GlobalEnvironments, copyEnv)
		ws.ActiveGlobalEnvironmentID = copyEnv.ID
		ws.UpdatedAt = time.Now()
		if err := a.writeWorkspaceGlobalEnvironmentFilesLocked(ws); err != nil {
			return AppState{}, err
		}
		a.notify("success", "Copied global environment: "+copyEnv.Name)
		return a.state, a.markDirty(persistScopeState)
	}
	return AppState{}, fmt.Errorf("global environment %s not found", environmentID)
}

func (a *App) ExportGlobalEnvironment(workspaceID, environmentID string) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return "", err
	}
	ws, err := a.findWorkspaceLocked(workspaceID)
	if err != nil {
		return "", err
	}
	for _, env := range ws.GlobalEnvironments {
		if env.ID == environmentID {
			return bru.StringifyBrunoEnvironmentExport(env)
		}
	}
	return "", fmt.Errorf("global environment %s not found", environmentID)
}

func (a *App) ExportGlobalEnvironments(workspaceID string, environmentIDs []string, exportFormat string) (GlobalEnvironmentExportResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return GlobalEnvironmentExportResult{}, err
	}
	ws, err := a.findWorkspaceLocked(workspaceID)
	if err != nil {
		return GlobalEnvironmentExportResult{}, err
	}
	environments, err := bru.SelectedGlobalEnvironments(*ws, environmentIDs)
	if err != nil {
		return GlobalEnvironmentExportResult{}, err
	}
	if len(environments) == 0 {
		return GlobalEnvironmentExportResult{}, errors.New("no global environments selected for export")
	}
	switch strings.TrimSpace(exportFormat) {
	case "", "single-object":
		if len(environments) != 1 {
			return GlobalEnvironmentExportResult{}, errors.New("single-object export requires exactly one environment")
		}
		content, err := bru.StringifyBrunoEnvironmentExport(environments[0])
		if err != nil {
			return GlobalEnvironmentExportResult{}, err
		}
		return GlobalEnvironmentExportResult{
			Format:   "single-object",
			Filename: bru.BrunoEnvironmentExportFileName(environments[0].Name) + ".json",
			Content:  content,
		}, nil
	case "single-file":
		content, err := bru.StringifyBrunoEnvironmentExportBundle(environments)
		if err != nil {
			return GlobalEnvironmentExportResult{}, err
		}
		return GlobalEnvironmentExportResult{
			Format:   "single-file",
			Filename: "bruno-global-environments.json",
			Content:  content,
		}, nil
	case "folder":
		files, err := bru.BrunoEnvironmentExportFiles(environments)
		if err != nil {
			return GlobalEnvironmentExportResult{}, err
		}
		return GlobalEnvironmentExportResult{
			Format:   "folder",
			Filename: "bruno-global-environments",
			Files:    files,
			Content:  bru.FormatGlobalEnvironmentExportFiles(files),
		}, nil
	default:
		return GlobalEnvironmentExportResult{}, fmt.Errorf("unsupported global environment export format %q", exportFormat)
	}
}

func (a *App) SaveGlobalEnvironmentExport(workspaceID string, environmentIDs []string, exportFormat, targetPath string) (GlobalEnvironmentSaveResult, error) {
	result, err := a.ExportGlobalEnvironments(workspaceID, environmentIDs, exportFormat)
	if err != nil {
		return GlobalEnvironmentSaveResult{}, err
	}
	targetPath = strings.TrimSpace(targetPath)
	if targetPath == "" {
		if a.ctx == nil {
			return GlobalEnvironmentSaveResult{}, errors.New("target path is required when the Wails save dialog is unavailable")
		}
		options := wailsruntime.SaveDialogOptions{
			Title:                "Save Global Environment Export",
			DefaultFilename:      result.Filename,
			CanCreateDirectories: true,
		}
		if defaultDir := a.defaultSaveDialogDirectory(); defaultDir != "" {
			options.DefaultDirectory = defaultDir
		}
		if result.Format != "folder" {
			options.Filters = []wailsruntime.FileFilter{{DisplayName: "JSON Files (*.json)", Pattern: "*.json"}}
		}
		targetPath, err = wailsruntime.SaveFileDialog(a.ctx, options)
		if err != nil {
			return GlobalEnvironmentSaveResult{}, err
		}
		if strings.TrimSpace(targetPath) == "" {
			return GlobalEnvironmentSaveResult{Format: result.Format, Cancelled: true}, nil
		}
	}
	targetPath, err = expandUserExportPath(targetPath)
	if err != nil {
		return GlobalEnvironmentSaveResult{}, err
	}
	files, err := writeGlobalEnvironmentExportResult(result, targetPath)
	if err != nil {
		return GlobalEnvironmentSaveResult{}, err
	}
	return GlobalEnvironmentSaveResult{
		Format: result.Format,
		Path:   targetPath,
		Files:  files,
	}, nil
}

func (a *App) ImportGlobalEnvironment(workspaceID, content string) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return AppState{}, err
	}
	ws, err := a.findWorkspaceLocked(workspaceID)
	if err != nil {
		return AppState{}, err
	}
	environments, err := yamlstore.ParseImportedGlobalEnvironments(content)
	if err != nil {
		return AppState{}, err
	}
	if len(environments) == 0 {
		return AppState{}, errors.New("no global environments found to import")
	}
	for _, env := range environments {
		env.ID = newID("global-env")
		env.Name = scripting.UniqueEnvironmentName(ws.GlobalEnvironments, env.Name)
		for index := range env.Variables {
			if env.Variables[index].ID == "" {
				env.Variables[index].ID = newID("var")
			}
		}
		ws.GlobalEnvironments = append(ws.GlobalEnvironments, env)
		ws.ActiveGlobalEnvironmentID = env.ID
	}
	ws.UpdatedAt = time.Now()
	if err := a.writeWorkspaceGlobalEnvironmentFilesLocked(ws); err != nil {
		return AppState{}, err
	}
	if len(environments) == 1 {
		a.notify("success", "Imported global environment: "+environments[0].Name)
	} else {
		a.notify("success", fmt.Sprintf("Imported %d global environments", len(environments)))
	}
	return a.state, a.markDirty(persistScopeState)
}

func (a *App) ListDotEnvFiles(workspaceID, collectionID string) ([]DotEnvFile, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return nil, err
	}
	ws, collection, err := a.dotEnvContextLocked(workspaceID, collectionID)
	if err != nil {
		return nil, err
	}
	return scripting.DotEnvFilesForContext(ws, collection)
}

func (a *App) ResolveProcessEnvValues(collectionID string, names []string) (map[string]string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return nil, err
	}
	ws, collection, err := a.findCollectionWithWorkspaceLocked(collectionID)
	if err != nil {
		return nil, err
	}
	processEnv := scripting.ProcessEnvForCollection(collection, ws.Path)
	values := map[string]string{}
	for _, requested := range names {
		key := strings.TrimSpace(requested)
		name, ok := strings.CutPrefix(key, interp.ProcessEnvPrefix)
		if !ok || strings.TrimSpace(name) == "" {
			continue
		}
		values[key] = processEnv[name]
	}
	return values, nil
}

func (a *App) SaveDotEnvFile(workspaceID, collectionID, scope, name, content string) ([]DotEnvFile, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return nil, err
	}
	ws, collection, err := a.dotEnvContextLocked(workspaceID, collectionID)
	if err != nil {
		return nil, err
	}
	basePath, err := scripting.DotEnvScopePath(ws, collection, scope)
	if err != nil {
		return nil, err
	}
	filename, err := scripting.NormalizeDotEnvFilename(name)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(basePath, 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(basePath, filename), []byte(content), 0o600); err != nil {
		return nil, err
	}
	a.notify("success", "Saved "+filename)
	return scripting.DotEnvFilesForContext(ws, collection)
}

func (a *App) DeleteDotEnvFile(workspaceID, collectionID, scope, name string) ([]DotEnvFile, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return nil, err
	}
	ws, collection, err := a.dotEnvContextLocked(workspaceID, collectionID)
	if err != nil {
		return nil, err
	}
	basePath, err := scripting.DotEnvScopePath(ws, collection, scope)
	if err != nil {
		return nil, err
	}
	filename, err := scripting.NormalizeDotEnvFilename(name)
	if err != nil {
		return nil, err
	}
	if err := os.Remove(filepath.Join(basePath, filename)); err != nil {
		return nil, err
	}
	a.notify("info", "Deleted "+filename)
	return scripting.DotEnvFilesForContext(ws, collection)
}
