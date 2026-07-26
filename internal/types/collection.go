// Collections, their watch results and folder config.
//
// US-060. Moved verbatim from app.go; see internal/types/proxy.go for why the
// aliases left behind in package main are a Go shim and not a Wails one.
package types

import "time"

type Collection struct {
	ID                 string                    `json:"id"`
	Name               string                    `json:"name"`
	Version            string                    `json:"version,omitempty"`
	Path               string                    `json:"path"`
	Format             string                    `json:"format"`
	Remote             string                    `json:"remote,omitempty"`
	NotFoundLocally    bool                      `json:"notFoundLocally,omitempty"`
	Scratch            bool                      `json:"scratch,omitempty"`
	Items              []RequestItem             `json:"items"`
	Folders            []FolderConfig            `json:"folders"`
	Environments       []Environment             `json:"environments"`
	Variables          []Variable                `json:"variables"`
	RuntimeVariables   []Variable                `json:"runtimeVariables,omitempty"`
	ResVariables       []Variable                `json:"resVariables"`
	Headers            []KeyValue                `json:"headers"`
	Auth               AuthConfig                `json:"auth"`
	Proxy              ProxyConfig               `json:"proxy"`
	ClientCertificates []ClientCertificateConfig `json:"clientCertificates"`
	Presets            CollectionPresets         `json:"presets"`
	Protobuf           CollectionProtobufConfig  `json:"protobuf"`
	SecurityConfig     CollectionSecurityConfig  `json:"securityConfig"`
	OpenAPI            []OpenAPISyncConfig       `json:"openapi,omitempty"`
	PreScript          string                    `json:"preScript"`
	PostScript         string                    `json:"postScript"`
	Tests              string                    `json:"tests"`
	Docs               string                    `json:"docs"`
	Tags               []string                  `json:"tags"`
	CreatedAt          time.Time                 `json:"createdAt"`
	UpdatedAt          time.Time                 `json:"updatedAt"`
}

type CollectionWatchRefreshResult struct {
	State        AppState `json:"state"`
	Changed      bool     `json:"changed"`
	Refreshed    []string `json:"refreshed,omitempty"`
	SkippedDirty []string `json:"skippedDirty,omitempty"`
	Missing      []string `json:"missing,omitempty"`
	Errors       []string `json:"errors,omitempty"`
}

type FolderConfig struct {
	Path         string     `json:"path"`
	DisplayPath  string     `json:"displayPath"`
	Name         string     `json:"name"`
	Seq          int        `json:"seq"`
	Headers      []KeyValue `json:"headers"`
	Variables    []Variable `json:"variables"`
	ResVariables []Variable `json:"resVariables"`
	Auth         AuthConfig `json:"auth"`
	PreScript    string     `json:"preScript"`
	PostScript   string     `json:"postScript"`
	Tests        string     `json:"tests"`
	Docs         string     `json:"docs"`
}
