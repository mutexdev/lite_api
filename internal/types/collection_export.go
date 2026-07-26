// Collection export and save results.
//
// US-060. Moved verbatim from app.go; see internal/types/proxy.go for why the
// aliases left behind in package main are a Go shim and not a Wails one.
package types

type CollectionExportOptions struct {
	Format string `json:"format"`
}

type CollectionExportResult struct {
	Format           string   `json:"format"`
	Filename         string   `json:"filename"`
	Content          string   `json:"content,omitempty"`
	ContentBase64    string   `json:"contentBase64,omitempty"`
	MimeType         string   `json:"mimeType"`
	Warning          string   `json:"warning,omitempty"`
	SkippedTypes     []string `json:"skippedTypes,omitempty"`
	FolderCount      int      `json:"folderCount"`
	RequestCount     int      `json:"requestCount"`
	EnvironmentCount int      `json:"environmentCount"`
}

type CollectionSaveResult struct {
	Format    string `json:"format"`
	Path      string `json:"path"`
	Cancelled bool   `json:"cancelled,omitempty"`
}
