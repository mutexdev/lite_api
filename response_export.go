package main

import (
	"encoding/base64"
	"errors"
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"strings"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

type ResponseBodySaveResult struct {
	Filename    string `json:"filename"`
	Path        string `json:"path,omitempty"`
	ByteCount   int    `json:"byteCount"`
	ContentType string `json:"contentType,omitempty"`
	Cancelled   bool   `json:"cancelled,omitempty"`
}

// SaveResponseBody snapshots response data while locked, then opens the native
// dialog and writes bytes after releasing the application mutex.
func (a *App) SaveResponseBody(collectionID, itemID, targetPath string) (ResponseBodySaveResult, error) {
	a.mu.Lock()
	if err := a.ensureReadyLocked(); err != nil {
		a.mu.Unlock()
		return ResponseBodySaveResult{}, err
	}
	collection, err := a.findCollectionLocked(collectionID)
	if err != nil {
		a.mu.Unlock()
		return ResponseBodySaveResult{}, err
	}
	item, err := findItem(collection, itemID)
	if err != nil {
		a.mu.Unlock()
		return ResponseBodySaveResult{}, err
	}
	if item.Response == nil {
		a.mu.Unlock()
		return ResponseBodySaveResult{}, errors.New("request has no response to save")
	}
	response := *item.Response
	requestName := item.Name
	ctx := a.ctx
	a.mu.Unlock()

	contentType := responseContentType(response)
	bytes, err := responseBodyBytes(response)
	if err != nil {
		return ResponseBodySaveResult{}, err
	}
	filename := responseDownloadFilename(requestName, response, contentType)
	targetPath = strings.TrimSpace(targetPath)
	if targetPath == "" {
		if ctx == nil {
			return ResponseBodySaveResult{}, errors.New("target path is required when the Wails save dialog is unavailable")
		}
		ext := filepath.Ext(filename)
		dialogOptions := runtime.SaveDialogOptions{Title: "Save Response Body", DefaultFilename: filename, CanCreateDirectories: true, DefaultDirectory: a.defaultSaveDialogDirectory()}
		if ext != "" {
			dialogOptions.Filters = []runtime.FileFilter{{DisplayName: "Response body (*" + ext + ")", Pattern: "*" + ext}}
		}
		targetPath, err = runtime.SaveFileDialog(ctx, dialogOptions)
		if err != nil {
			return ResponseBodySaveResult{}, err
		}
		if strings.TrimSpace(targetPath) == "" {
			return ResponseBodySaveResult{Filename: filename, ContentType: contentType, Cancelled: true}, nil
		}
	}
	targetPath, err = expandUserExportPath(targetPath)
	if err != nil {
		return ResponseBodySaveResult{}, err
	}
	if info, statErr := os.Stat(targetPath); statErr == nil && info.IsDir() {
		targetPath = filepath.Join(targetPath, filename)
	} else if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return ResponseBodySaveResult{}, statErr
	}
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		return ResponseBodySaveResult{}, err
	}
	if err := os.WriteFile(targetPath, bytes, 0o600); err != nil {
		return ResponseBodySaveResult{}, err
	}
	return ResponseBodySaveResult{Filename: filepath.Base(targetPath), Path: targetPath, ByteCount: len(bytes), ContentType: contentType}, nil
}

func responseBodyBytes(response Response) ([]byte, error) {
	if strings.TrimSpace(response.BodyBase64) != "" {
		data, err := base64.StdEncoding.DecodeString(response.BodyBase64)
		if err != nil {
			return nil, fmt.Errorf("decode response body base64: %w", err)
		}
		return data, nil
	}
	return []byte(response.Body), nil
}

func responseContentType(response Response) string {
	for name, value := range response.Headers {
		if strings.EqualFold(strings.TrimSpace(name), "content-type") {
			return strings.TrimSpace(strings.Split(value, ";")[0])
		}
	}
	return ""
}

func responseBodyFilename(requestName, contentType string) string {
	return sanitizeFilename(requestName) + "-response" + responseBodyExtension(contentType)
}

func responseDownloadFilename(requestName string, response Response, contentType string) string {
	fallback := boundResponseFilename(responseBodyFilename(requestName, contentType))
	var disposition string
	for name, value := range response.Headers {
		if strings.EqualFold(strings.TrimSpace(name), "content-disposition") {
			disposition = value
			break
		}
	}
	if disposition == "" {
		return fallback
	}
	_, params, err := mime.ParseMediaType(disposition)
	if err != nil {
		return fallback
	}
	name := params["filename*"]
	if name == "" {
		name = params["filename"]
	}
	name = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return -1
		}
		return r
	}, name)
	name = filepath.Base(strings.ReplaceAll(name, "\\", "/"))
	name = sanitizeFilename(name)
	if name == "" || name == "untitled" {
		return fallback
	}
	if ext := filepath.Ext(name); ext == "" || ext == "." {
		name += responseBodyExtension(contentType)
	}
	return boundResponseFilename(name)
}

func boundResponseFilename(name string) string {
	const maxBytes = 255
	if len(name) <= maxBytes {
		return name
	}
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	budget := maxBytes - len(ext)
	if budget <= 0 {
		return "untitled" + ext
	}
	var out strings.Builder
	for _, r := range base {
		encoded := string(r)
		if out.Len()+len(encoded) > budget {
			break
		}
		out.WriteString(encoded)
	}
	if out.Len() == 0 {
		out.WriteString("untitled")
	}
	return out.String() + ext
}

func responseBodyExtension(contentType string) string {
	media, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return ".txt"
	}
	media = strings.ToLower(media)
	if media == "image/svg+xml" {
		return ".svg"
	}
	if strings.HasSuffix(media, "+json") || media == "application/json" {
		return ".json"
	}
	if strings.HasSuffix(media, "+xml") || media == "application/xml" || media == "text/xml" {
		return ".xml"
	}
	switch media {
	case "application/pdf":
		return ".pdf"
	case "application/octet-stream":
		return ".bin"
	case "text/html":
		return ".html"
	case "text/plain":
		return ".txt"
	case "text/csv":
		return ".csv"
	case "application/zip", "application/x-zip-compressed":
		return ".zip"
	case "application/gzip", "application/x-gzip":
		return ".gz"
	case "application/x-tar":
		return ".tar"
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	default:
		return ".txt"
	}
}
