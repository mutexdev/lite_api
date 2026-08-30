package core

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/mutexdev/lite_api/internal/export"
	"github.com/mutexdev/lite_api/internal/store/bru"
)

func (a *App) ExportCollection(collectionID string) (string, error) {
	result, err := a.ExportCollectionWithOptions(collectionID, CollectionExportOptions{Format: "yaml"})
	if err != nil {
		return "", err
	}
	return result.Content, nil
}

func (a *App) ExportCollectionWithOptions(collectionID string, options CollectionExportOptions) (CollectionExportResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return CollectionExportResult{}, err
	}
	collection, err := a.findCollectionLocked(collectionID)
	if err != nil {
		return CollectionExportResult{}, err
	}
	snapshot := export.ShareSnapshot(*collection)
	format := strings.ToLower(strings.TrimSpace(options.Format))
	if format == "" {
		format = "zip"
	}
	switch format {
	case "yaml", "yml":
		content, folderCount, requestCount, err := export.BuildShareYAML(snapshot, time.Now().UTC())
		if err != nil {
			return CollectionExportResult{}, err
		}
		return CollectionExportResult{
			Format:           "yaml",
			Filename:         sanitizeFilename(snapshot.Name) + ".yml",
			Content:          content,
			MimeType:         "application/x-yaml",
			FolderCount:      folderCount,
			RequestCount:     requestCount,
			EnvironmentCount: len(snapshot.Environments),
		}, nil
	case "zip":
		files, folderCount, requestCount, err := export.BuildZipFiles(snapshot)
		if err != nil {
			return CollectionExportResult{}, err
		}
		data, err := export.ZipFiles(files)
		if err != nil {
			return CollectionExportResult{}, err
		}
		return CollectionExportResult{
			Format:           "zip",
			Filename:         sanitizeFilename(snapshot.Name) + ".zip",
			ContentBase64:    base64.StdEncoding.EncodeToString(data),
			MimeType:         "application/zip",
			FolderCount:      folderCount,
			RequestCount:     requestCount,
			EnvironmentCount: len(snapshot.Environments),
		}, nil
	case "postman":
		postman, err := export.BuildPostmanExport(snapshot)
		if err != nil {
			return CollectionExportResult{}, err
		}
		notes := []string{}
		if len(postman.SkippedTypes) > 0 {
			notes = append(notes, fmt.Sprintf("Note: %s requests in this collection will not be exported", strings.Join(postman.SkippedTypes, ", ")))
		}
		notes = append(notes, postman.Warnings...)
		return CollectionExportResult{
			Format:       "postman",
			Filename:     sanitizeFilename(snapshot.Name) + ".json",
			Content:      postman.Content,
			MimeType:     "application/json",
			Warning:      strings.Join(notes, " "),
			SkippedTypes: postman.SkippedTypes,
			// The count comes from the tree the export actually walked, which
			// includes folders that only exist as a request's FolderPath.
			FolderCount:  postman.FolderCount,
			RequestCount: postman.RequestCount,
			// A Postman collection carries no environments. Reporting the
			// collection's count told the user something had been exported that
			// is not in the file.
			EnvironmentCount: 0,
		}, nil
	default:
		return CollectionExportResult{}, fmt.Errorf("unsupported collection export format %q", options.Format)
	}
}

func (a *App) SaveCollectionExport(collectionID string, options CollectionExportOptions, targetPath string) (CollectionSaveResult, error) {
	result, err := a.ExportCollectionWithOptions(collectionID, options)
	if err != nil {
		return CollectionSaveResult{}, err
	}
	targetPath = strings.TrimSpace(targetPath)
	if targetPath == "" {
		if a.ctx == nil {
			return CollectionSaveResult{}, errors.New("target path is required when the Wails save dialog is unavailable")
		}
		dialogOptions := wailsruntime.SaveDialogOptions{
			Title:                "Share Collection",
			DefaultFilename:      result.Filename,
			CanCreateDirectories: true,
		}
		if defaultDir := a.defaultSaveDialogDirectory(); defaultDir != "" {
			dialogOptions.DefaultDirectory = defaultDir
		}
		switch result.Format {
		case "zip":
			dialogOptions.Filters = []wailsruntime.FileFilter{{DisplayName: "Zip Files (*.zip)", Pattern: "*.zip"}}
		case "postman":
			dialogOptions.Filters = []wailsruntime.FileFilter{{DisplayName: "JSON Files (*.json)", Pattern: "*.json"}}
		default:
			dialogOptions.Filters = []wailsruntime.FileFilter{{DisplayName: "YAML Files (*.yml)", Pattern: "*.yml"}}
		}
		targetPath, err = wailsruntime.SaveFileDialog(a.ctx, dialogOptions)
		if err != nil {
			return CollectionSaveResult{}, err
		}
		if strings.TrimSpace(targetPath) == "" {
			return CollectionSaveResult{Format: result.Format, Cancelled: true}, nil
		}
	}
	targetPath, err = expandUserExportPath(targetPath)
	if err != nil {
		return CollectionSaveResult{}, err
	}
	if info, err := os.Stat(targetPath); err == nil && info.IsDir() {
		targetPath = filepath.Join(targetPath, result.Filename)
	}
	data, err := export.Bytes(result)
	if err != nil {
		return CollectionSaveResult{}, err
	}
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		return CollectionSaveResult{}, err
	}
	if err := os.WriteFile(targetPath, data, 0o600); err != nil {
		return CollectionSaveResult{}, err
	}
	return CollectionSaveResult{Format: result.Format, Path: targetPath}, nil
}

func (a *App) GenerateCollectionDocs(collectionID string, options GenerateCollectionDocsOptions) (GenerateCollectionDocsResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return GenerateCollectionDocsResult{}, err
	}
	collection, err := a.findCollectionLocked(collectionID)
	if err != nil {
		return GenerateCollectionDocsResult{}, err
	}
	snapshot := *collection
	snapshot.Items = append([]RequestItem(nil), collection.Items...)
	snapshot.Folders = append([]FolderConfig(nil), collection.Folders...)
	snapshot.Environments = append([]Environment(nil), collection.Environments...)
	yamlContent, folderCount, requestCount, err := export.BuildDocsYAML(snapshot, options.EnvironmentIDs, time.Now().UTC())
	if err != nil {
		return GenerateCollectionDocsResult{}, err
	}
	htmlContent, err := export.BuildDocsHTML(snapshot.Name, yamlContent)
	if err != nil {
		return GenerateCollectionDocsResult{}, err
	}
	return GenerateCollectionDocsResult{
		FileName:     sanitizeFilename(snapshot.Name) + "-documentation.html",
		HTML:         htmlContent,
		YAML:         yamlContent,
		Version:      export.DisplayVersion(snapshot.Version),
		FolderCount:  folderCount,
		RequestCount: requestCount,
	}, nil
}

func (a *App) ParseBru(content string) (RequestItem, error) {
	return bru.Parse(content)
}

func (a *App) StringifyBru(item RequestItem) string {
	return bru.StringifyBru(item)
}

func (a *App) ResetDemoData() (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.workspaceRuntime != nil {
		return AppState{}, errors.New("reset demo data is unavailable from a scoped workspace window")
	}
	a.state = defaultState(a.dataDir)
	if err := a.writeFreshDefaultCollectionFilesLocked(); err != nil {
		return AppState{}, err
	}
	a.oauth2Mu.Lock()
	a.oauth2 = map[string]oauth2TokenResponse{}
	a.oauth2Mu.Unlock()
	return a.state, a.markDirty(persistScopeState)
}
