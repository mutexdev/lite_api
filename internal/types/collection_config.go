// Per-collection configuration: presets, protobuf, security and OpenAPI sync.
//
// US-060. Moved verbatim from app.go; see internal/types/proxy.go for why the
// aliases left behind in package main are a Go shim and not a Wails one.
package types

import (
	"os"
	"path/filepath"
	"strings"
)

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

// Normalisation and the "is anything set" predicates for the two config blocks
// above.
//
// Here rather than in the application package because the YAML codec needs the
// same answers when deciding whether to emit a section, and it cannot import
// the application. They are pure functions over this package's own structs, so
// this is where they belonged regardless.

func NormalizeCollectionPresets(presets CollectionPresets) CollectionPresets {
	presets.RequestType = NormalizePresetRequestType(presets.RequestType)
	presets.RequestURL = strings.TrimSpace(presets.RequestURL)
	return presets
}

func NormalizePresetRequestType(requestType string) string {
	switch strings.ToLower(strings.TrimSpace(requestType)) {
	case "http", "http-request":
		return "http"
	case "graphql", "graphql-request":
		return "graphql"
	case "grpc", "grpc-request":
		return "grpc"
	case "ws", "websocket", "ws-request", "websocket-request":
		return "websocket"
	default:
		return ""
	}
}

func BrunoPresetRequestType(requestType string) string {
	switch NormalizePresetRequestType(requestType) {
	case "websocket":
		return "ws"
	case "graphql":
		return "graphql"
	case "grpc":
		return "grpc"
	default:
		return "http"
	}
}

func HasCollectionPresets(presets CollectionPresets) bool {
	presets = NormalizeCollectionPresets(presets)
	return presets.RequestURL != "" || (presets.RequestType != "" && presets.RequestType != "http")
}

func NormalizeCollectionProtobuf(collectionPath string, protobuf CollectionProtobufConfig) CollectionProtobufConfig {
	result := CollectionProtobufConfig{
		ProtoFiles:  make([]CollectionProtoFile, 0, len(protobuf.ProtoFiles)),
		ImportPaths: make([]CollectionProtoImportPath, 0, len(protobuf.ImportPaths)),
	}
	seenFiles := map[string]bool{}
	for _, protoFile := range protobuf.ProtoFiles {
		protoFile.Path = strings.TrimSpace(protoFile.Path)
		protoFile.Type = strings.ToLower(strings.TrimSpace(protoFile.Type))
		if protoFile.Type == "" {
			protoFile.Type = "file"
		}
		if protoFile.Path == "" {
			continue
		}
		key := protoFile.Type + "\x00" + protoFile.Path
		if seenFiles[key] {
			continue
		}
		seenFiles[key] = true
		protoFile.Exists = collectionProtobufPathExists(collectionPath, protoFile.Path, false)
		result.ProtoFiles = append(result.ProtoFiles, protoFile)
	}
	seenImportPaths := map[string]bool{}
	for _, importPath := range protobuf.ImportPaths {
		importPath.Path = strings.TrimSpace(importPath.Path)
		if importPath.Path == "" {
			continue
		}
		if seenImportPaths[importPath.Path] {
			continue
		}
		seenImportPaths[importPath.Path] = true
		importPath.Exists = collectionProtobufPathExists(collectionPath, importPath.Path, true)
		result.ImportPaths = append(result.ImportPaths, importPath)
	}
	return result
}

func HasCollectionProtobuf(protobuf CollectionProtobufConfig) bool {
	protobuf = NormalizeCollectionProtobuf("", protobuf)
	return len(protobuf.ProtoFiles) > 0 || len(protobuf.ImportPaths) > 0
}

func collectionProtobufPathExists(collectionPath, rawPath string, wantDir bool) bool {
	path := strings.TrimSpace(rawPath)
	if path == "" {
		return false
	}
	resolved := path
	if !filepath.IsAbs(resolved) && strings.TrimSpace(collectionPath) != "" {
		resolved = filepath.Join(collectionPath, resolved)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return false
	}
	if wantDir {
		return info.IsDir()
	}
	return !info.IsDir()
}
