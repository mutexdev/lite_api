// Notifications and network log entries.
//
// US-060. Moved verbatim from app.go; see internal/types/proxy.go for why the
// aliases left behind in internal/core are a Go shim and not a Wails one.
package types

import "time"

type Notification struct {
	ID          string    `json:"id"`
	Level       string    `json:"level"`
	Type        string    `json:"type,omitempty"`
	Title       string    `json:"title,omitempty"`
	Message     string    `json:"message"`
	Description string    `json:"description,omitempty"`
	Color       string    `json:"color,omitempty"`
	Read        bool      `json:"read"`
	At          time.Time `json:"at"`
}

type NetworkLog struct {
	ID string `json:"id"`
	// US-073. Where the entry came from. Empty for an ordinary outgoing
	// request, "mock" for a call the built-in mock server answered. Without it
	// the app's request to its own mock and the mock's handling of it are two
	// rows with the same URL and no way to tell apart — which is exactly the
	// confusion someone debugging a mock is in.
	Source          string            `json:"source,omitempty"`
	Method          string            `json:"method"`
	URL             string            `json:"url"`
	Status          int               `json:"status"`
	StatusText      string            `json:"statusText"`
	DurationMs      int64             `json:"durationMs"`
	Size            int               `json:"size"`
	At              time.Time         `json:"at"`
	Error           string            `json:"error"`
	RequestHeaders  map[string]string `json:"requestHeaders,omitempty"`
	RequestBody     string            `json:"requestBody,omitempty"`
	ResponseHeaders map[string]string `json:"responseHeaders,omitempty"`
	ResponseBody    string            `json:"responseBody,omitempty"`
}
