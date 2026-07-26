// Git collection discovery and clone progress.
//
// US-060. Moved verbatim from app.go; see internal/types/proxy.go for why the
// aliases left behind in package main are a Go shim and not a Wails one.
package types

type GitCollectionCandidate struct {
	Name         string `json:"name"`
	Path         string `json:"path"`
	Format       string `json:"format"`
	RequestCount int    `json:"requestCount"`
}

type GitCloneResult struct {
	Version    string                   `json:"version"`
	TargetPath string                   `json:"targetPath"`
	Output     string                   `json:"output"`
	Candidates []GitCollectionCandidate `json:"candidates"`
}

type GitCloneProgress struct {
	Stage      string `json:"stage"`
	Message    string `json:"message"`
	TargetPath string `json:"targetPath"`
	At         string `json:"at"`
}
