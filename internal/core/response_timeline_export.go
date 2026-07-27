package core

import (
	"encoding/json"
	"errors"
	"github.com/mutexdev/lite_api/internal/types"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

type ResponseTimelineSaveResult struct {
	Filename  string `json:"filename"`
	Path      string `json:"path,omitempty"`
	ByteCount int    `json:"byteCount"`
	Cancelled bool   `json:"cancelled,omitempty"`
}
type responseTimelineExport struct {
	SchemaVersion int `json:"schemaVersion"`
	Request       struct {
		Name   string `json:"name"`
		Method string `json:"method"`
		URL    string `json:"url"`
	} `json:"request"`
	Response struct {
		Status        int                   `json:"status"`
		StatusText    string                `json:"statusText"`
		DurationMs    int64                 `json:"durationMs"`
		Size          int                   `json:"size"`
		Error         string                `json:"error"`
		Cancelled     bool                  `json:"cancelled"`
		Timings       types.ResponseTimings `json:"timings"`
		HeaderEntries []KeyValue            `json:"headerEntries,omitempty"`
	} `json:"response"`
	ExportedAt time.Time      `json:"exportedAt"`
	Timeline   []TimelineItem `json:"timeline"`
}

func (a *App) SaveResponseTimeline(collectionID, itemID, targetPath string) (ResponseTimelineSaveResult, error) {
	a.mu.Lock()
	if err := a.ensureReadyLocked(); err != nil {
		a.mu.Unlock()
		return ResponseTimelineSaveResult{}, err
	}
	collection, err := a.findCollectionLocked(collectionID)
	if err != nil {
		a.mu.Unlock()
		return ResponseTimelineSaveResult{}, err
	}
	item, err := findItem(collection, itemID)
	if err != nil {
		a.mu.Unlock()
		return ResponseTimelineSaveResult{}, err
	}
	if item.Response == nil && len(item.Timeline) == 0 {
		a.mu.Unlock()
		return ResponseTimelineSaveResult{}, errors.New("request has no response timeline to save")
	}
	doc := responseTimelineExport{SchemaVersion: 1, ExportedAt: time.Now().UTC(), Timeline: append([]TimelineItem(nil), item.Timeline...)}
	doc.Request.Name = item.Name
	doc.Request.Method = item.Method
	doc.Request.URL = item.URL
	if item.Response != nil {
		doc.Response.Status = item.Response.Status
		doc.Response.StatusText = item.Response.StatusText
		doc.Response.DurationMs = item.Response.DurationMs
		doc.Response.Size = item.Response.Size
		doc.Response.Error = item.Response.Error
		doc.Response.Cancelled = item.Response.Cancelled
		doc.Response.Timings = item.Response.Timings
		doc.Response.HeaderEntries = append([]KeyValue(nil), item.Response.HeaderEntries...)
	}
	ctx := a.ctx
	a.mu.Unlock()
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return ResponseTimelineSaveResult{}, err
	}
	data = append(data, '\n')
	filename := sanitizeFilename(doc.Request.Name) + "-timeline.json"
	targetPath = strings.TrimSpace(targetPath)
	if targetPath == "" {
		if ctx == nil {
			return ResponseTimelineSaveResult{}, errors.New("target path is required when the Wails save dialog is unavailable")
		}
		targetPath, err = runtime.SaveFileDialog(ctx, runtime.SaveDialogOptions{Title: "Export Response Timeline", DefaultFilename: filename, DefaultDirectory: a.defaultSaveDialogDirectory(), CanCreateDirectories: true, Filters: []runtime.FileFilter{{DisplayName: "JSON Files (*.json)", Pattern: "*.json"}}})
		if err != nil {
			return ResponseTimelineSaveResult{}, err
		}
		if strings.TrimSpace(targetPath) == "" {
			return ResponseTimelineSaveResult{Filename: filename, Cancelled: true}, nil
		}
	}
	targetPath, err = expandUserExportPath(targetPath)
	if err != nil {
		return ResponseTimelineSaveResult{}, err
	}
	if info, statErr := os.Stat(targetPath); statErr == nil && info.IsDir() {
		targetPath = filepath.Join(targetPath, filename)
	} else if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return ResponseTimelineSaveResult{}, statErr
	}
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		return ResponseTimelineSaveResult{}, err
	}
	if err := os.WriteFile(targetPath, data, 0o600); err != nil {
		return ResponseTimelineSaveResult{}, err
	}
	return ResponseTimelineSaveResult{Filename: filepath.Base(targetPath), Path: targetPath, ByteCount: len(data)}, nil
}
