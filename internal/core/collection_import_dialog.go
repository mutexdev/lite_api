package core

// The native file and folder pickers behind an import, and the directory it remembers.
//
// Split out by AST: declarations are identified by the parser and copied
// verbatim from their source offsets.

import (
	"errors"
	"fmt"
	"github.com/mutexdev/lite_api/internal/scalar"
	"path/filepath"
	"strings"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

func collectionImportFileDialogOptions() wailsruntime.OpenDialogOptions {
	return wailsruntime.OpenDialogOptions{Title: "Import API collections", Filters: []wailsruntime.FileFilter{
		{DisplayName: "Supported imports", Pattern: "*.json;*.yaml;*.yml;*.bru;*.curl;*.sh;*.txt;*.zip;*.wsdl"},
		{DisplayName: "All Files", Pattern: "*.*"},
	}}
}

func chooseCollectionImportFiles(open func(wailsruntime.OpenDialogOptions) ([]string, error)) (CollectionImportPickerResult, error) {
	paths, err := open(collectionImportFileDialogOptions())
	if err != nil {
		return CollectionImportPickerResult{}, err
	}
	if len(paths) == 0 {
		return CollectionImportPickerResult{Cancelled: true}, nil
	}
	if len(paths) > collectionImportMaxFiles {
		return CollectionImportPickerResult{}, fmt.Errorf("select at most %d import files", collectionImportMaxFiles)
	}
	return CollectionImportPickerResult{Paths: append([]string(nil), paths...)}, nil
}

func chooseCollectionImportFolder(open func(wailsruntime.OpenDialogOptions) (string, error)) (CollectionImportPickerResult, error) {
	path, err := open(wailsruntime.OpenDialogOptions{Title: "Open Bruno or OpenCollection folder"})
	if err != nil {
		return CollectionImportPickerResult{}, err
	}
	if strings.TrimSpace(path) == "" {
		return CollectionImportPickerResult{Cancelled: true}, nil
	}
	return CollectionImportPickerResult{Paths: []string{path}}, nil
}

func (a *App) ChooseCollectionImportFiles() (CollectionImportPickerResult, error) {
	if a == nil || a.ctx == nil {
		return CollectionImportPickerResult{}, errors.New("file dialog is unavailable")
	}
	directory := a.collectionImportDefaultDirectory()
	result, err := chooseCollectionImportFiles(func(options wailsruntime.OpenDialogOptions) ([]string, error) {
		options.DefaultDirectory = directory
		return wailsruntime.OpenMultipleFilesDialog(a.ctx, options)
	})
	if err == nil && !result.Cancelled {
		_ = a.rememberCollectionImportDirectory(result.Paths, false)
	}
	return result, err
}

func (a *App) ChooseCollectionImportFolder() (CollectionImportPickerResult, error) {
	if a == nil || a.ctx == nil {
		return CollectionImportPickerResult{}, errors.New("directory dialog is unavailable")
	}
	directory := a.collectionImportDefaultDirectory()
	result, err := chooseCollectionImportFolder(func(options wailsruntime.OpenDialogOptions) (string, error) {
		options.DefaultDirectory = directory
		return wailsruntime.OpenDirectoryDialog(a.ctx, options)
	})
	if err == nil && !result.Cancelled {
		_ = a.rememberCollectionImportDirectory(result.Paths, true)
	}
	return result, err
}

// Read-only: normalizeCollectionImportDirectory takes a string and only stats
// the filesystem.
func (a *App) collectionImportDefaultDirectory() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return scalar.NormalizeAbsoluteDirectory(a.state.Preferences.General.LastImportDirectory)
}

func (a *App) rememberCollectionImportDirectory(paths []string, folder bool) error {
	if len(paths) == 0 {
		return nil
	}
	directory := paths[0]
	if !folder {
		directory = filepath.Dir(directory)
	}
	directory = scalar.NormalizeAbsoluteDirectory(directory)
	if directory == "" {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return err
	}
	if a.state.Preferences.General.LastImportDirectory == directory {
		return nil
	}
	a.state.Preferences.General.LastImportDirectory = directory
	return a.persistLocked()
}
