// Results of exporting and saving global environments.
//
// US-060. Moved verbatim from app.go; see internal/types/proxy.go for why the
// aliases left behind in package main are a Go shim and not a Wails one.
package types

type GlobalEnvironmentExportResult struct {
	Format   string                        `json:"format"`
	Filename string                        `json:"filename"`
	Content  string                        `json:"content"`
	Files    []GlobalEnvironmentExportFile `json:"files"`
}

type GlobalEnvironmentExportFile struct {
	Name    string `json:"name"`
	Content string `json:"content"`
}

type GlobalEnvironmentSaveResult struct {
	Format    string   `json:"format"`
	Path      string   `json:"path"`
	Files     []string `json:"files"`
	Cancelled bool     `json:"cancelled"`
}
