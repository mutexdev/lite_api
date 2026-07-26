// Per-collection configuration: presets, protobuf, security and OpenAPI sync.
//
// US-060. Moved verbatim from app.go; see internal/types/proxy.go for why the
// aliases left behind in package main are a Go shim and not a Wails one.
package types

type CollectionPresets struct {
	RequestType string `json:"requestType"`
	RequestURL  string `json:"requestUrl"`
}

type CollectionProtobufConfig struct {
	ProtoFiles  []CollectionProtoFile       `json:"protoFiles"`
	ImportPaths []CollectionProtoImportPath `json:"importPaths"`
}

type CollectionSecurityConfig struct {
	JSSandboxMode string `json:"jsSandboxMode"`
}

type CollectionProtoFile struct {
	Path   string `json:"path"`
	Type   string `json:"type,omitempty"`
	Exists bool   `json:"exists,omitempty"`
}

type CollectionProtoImportPath struct {
	Path    string `json:"path"`
	Enabled bool   `json:"enabled"`
	Exists  bool   `json:"exists,omitempty"`
}

type OpenAPISyncConfig struct {
	SourceURL         string `json:"sourceUrl" yaml:"sourceUrl"`
	GroupBy           string `json:"groupBy" yaml:"groupBy"`
	LastSyncDate      string `json:"lastSyncDate,omitempty" yaml:"lastSyncDate,omitempty"`
	SpecHash          string `json:"specHash,omitempty" yaml:"specHash,omitempty"`
	AutoCheck         bool   `json:"autoCheck" yaml:"autoCheck"`
	AutoCheckInterval int    `json:"autoCheckInterval" yaml:"autoCheckInterval"`
}
