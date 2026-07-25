package main

import (
	"bytes"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSaveResponseBodyWritesUTF8AndBinaryExactly(t *testing.T) {
	app, collection, item := responseExportApp(t)
	app.mu.Lock()
	itemPtr, _ := findItem(collectionPointer(app, collection.ID), item.ID)
	itemPtr.Response = &Response{Body: "héllo", Headers: map[string]string{"Content-Type": "application/json; charset=utf-8"}}
	app.mu.Unlock()
	textPath := filepath.Join(t.TempDir(), "body")
	result, err := app.SaveResponseBody(collection.ID, item.ID, textPath)
	if err != nil {
		t.Fatal(err)
	}
	data, readErr := os.ReadFile(textPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(data) != "héllo" || result.ByteCount != len(data) || result.ContentType != "application/json" {
		t.Fatalf("unexpected text result %#v %q", result, data)
	}
	binary := []byte{0, 255, 3, 0, 7}
	app.mu.Lock()
	itemPtr, _ = findItem(collectionPointer(app, collection.ID), item.ID)
	itemPtr.Response = &Response{Body: "wrong", BodyBase64: base64.StdEncoding.EncodeToString(binary), Headers: map[string]string{"content-type": "image/png"}}
	app.mu.Unlock()
	binPath := filepath.Join(t.TempDir(), "body.bin")
	if _, err := app.SaveResponseBody(collection.ID, item.ID, binPath); err != nil {
		t.Fatal(err)
	}
	data, readErr = os.ReadFile(binPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !bytes.Equal(data, binary) {
		t.Fatalf("binary mismatch %v", data)
	}
}

func TestResponseExportFilenameAndErrors(t *testing.T) {
	if got := responseBodyFilename(`bad:/name?`, "application/json"); got != "bad--name-response.json" {
		t.Fatalf("filename %q", got)
	}
	for contentType, want := range map[string]string{"application/pdf": ".pdf", "image/svg+xml": ".svg", "application/problem+json": ".json", "application/octet-stream": ".bin", "application/vnd.test+xml": ".xml"} {
		if got := responseBodyExtension(contentType); got != want {
			t.Fatalf("%s extension %q want %q", contentType, got, want)
		}
	}
	if got := responseBodyFilename("", "image/svg+xml"); got != "untitled-response.svg" {
		t.Fatalf("empty filename %q", got)
	}
	app, collection, item := responseExportApp(t)
	if _, err := app.SaveResponseBody(collection.ID, item.ID, filepath.Join(t.TempDir(), "x")); err == nil {
		t.Fatal("expected missing response")
	}
	app.mu.Lock()
	ptr, _ := findItem(collectionPointer(app, collection.ID), item.ID)
	ptr.Response = &Response{BodyBase64: "%%%"}
	app.mu.Unlock()
	if _, err := app.SaveResponseBody(collection.ID, item.ID, filepath.Join(t.TempDir(), "x")); err == nil {
		t.Fatal("expected malformed base64")
	}
	if _, err := app.SaveResponseBody(collection.ID, "missing", filepath.Join(t.TempDir(), "x")); err == nil {
		t.Fatal("expected missing request")
	}
}

func TestSaveResponseBodyDirectoryTargetAndMode(t *testing.T) {
	app, collection, item := responseExportApp(t)
	app.mu.Lock()
	ptr, _ := findItem(collectionPointer(app, collection.ID), item.ID)
	ptr.Response = &Response{Body: "body", Headers: map[string]string{"Content-Type": "application/pdf"}}
	app.mu.Unlock()
	dir := t.TempDir()
	result, err := app.SaveResponseBody(collection.ID, item.ID, dir)
	if err != nil {
		t.Fatal(err)
	}
	if result.Path != filepath.Join(dir, "Export Me-response.pdf") {
		t.Fatalf("directory path %q", result.Path)
	}
	info, err := os.Stat(result.Path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode %o", info.Mode().Perm())
	}
}

func TestResponseDownloadFilenameContentDispositionSafety(t *testing.T) {
	cases := []struct{ header, want string }{
		{`attachment; filename="server report.pdf"`, "server report.pdf"},
		{`attachment; filename*=UTF-8''caf%C3%A9.json`, "café.json"},
		{`attachment; filename="../../secret.txt"`, "secret.txt"},
		{`attachment; filename="C:\\temp\\photo.png"`, "photo.png"},
		{`attachment; filename="."`, "untitled-response.pdf"},
		{`attachment; filename="download"`, "download.pdf"},
	}
	for _, tc := range cases {
		response := Response{Headers: map[string]string{"CONTENT-DISPOSITION": tc.header}}
		if got := responseDownloadFilename("untitled", response, "application/pdf"); got != tc.want {
			t.Fatalf("%s: got %q want %q", tc.header, got, tc.want)
		}
	}
	if got := responseDownloadFilename("Request", Response{}, "application/json"); got != "Request-response.json" {
		t.Fatalf("fallback %q", got)
	}
	if got := responseBodyExtension("text/csv"); got != ".csv" {
		t.Fatalf("csv extension %q", got)
	}
	long := responseDownloadFilename("Request", Response{Headers: map[string]string{"Content-Disposition": `attachment; filename="` + strings.Repeat("x", 400) + `.json"`}}, "application/json")
	if len(long) > 255 || filepath.Ext(long) != ".json" {
		t.Fatalf("long filename %d %q", len(long), filepath.Ext(long))
	}
	longFallback := responseDownloadFilename(strings.Repeat("r", 400), Response{}, "application/json")
	if len(longFallback) > 255 || filepath.Ext(longFallback) != ".json" {
		t.Fatalf("long fallback filename %d %q", len(longFallback), filepath.Ext(longFallback))
	}
}

func responseExportApp(t *testing.T) (*App, Collection, RequestItem) {
	t.Helper()
	app := NewAppWithDir(t.TempDir())
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	state, err = app.CreateCollection(state.ActiveWorkspaceID, "Response Export", "bru")
	if err != nil {
		t.Fatal(err)
	}
	collection := findTestCollectionByName(state, "Response Export")
	state, err = app.CreateRequest(collection.ID, "http", "Export Me")
	if err != nil {
		t.Fatal(err)
	}
	collection = findTestCollectionByName(state, "Response Export")
	return app, collection, collection.Items[0]
}

func collectionPointer(app *App, id string) *Collection {
	_, c, _ := app.findCollectionWithWorkspaceLocked(id)
	return c
}
