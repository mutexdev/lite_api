// Package transport builds the http.RoundTripper a request executes on: TLS
// client certificates, and proxy resolution in all four flavours the app
// supports -- none, manual, system and PAC, including a goja PAC runtime.
//
// US-062 groundwork. Every function here was already free of *App, which is
// what made the region movable.
package transport

import (
	"net/http"
	"path/filepath"
	"strings"

	"github.com/mutexdev/lite_api/internal/types"
)

func ResolveCollectionRelativePath(collectionPath, value string) string {
	value = strings.TrimSpace(value)
	if value == "" || filepath.IsAbs(value) {
		return value
	}
	return filepath.Join(collectionPath, filepath.FromSlash(value))
}

func CloneHTTPTransport(base http.RoundTripper) *http.Transport {
	source, ok := base.(*http.Transport)
	if !ok || source == nil {
		source, _ = http.DefaultTransport.(*http.Transport)
	}
	if source == nil {
		return &http.Transport{}
	}
	return source.Clone()
}

// Resolution is how a request's proxy was decided: which mode won, and the
// manual config or PAC source that goes with it.
type Resolution struct {
	Mode      string
	Config    types.ProxyConfig
	PACSource string
}
