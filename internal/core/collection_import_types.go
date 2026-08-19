package core

// The request and result shapes the collection-import bindings exchange with the frontend.
//
// Split out of collection_import.go by AST: declarations are identified by the parser
// and copied verbatim from their source offsets.

import ()

// CollectionImportSource deliberately accepts either a selected path or advanced
// in-memory content. Content is never returned in previews or error messages.
type CollectionImportSource struct {
	ID              string `json:"id,omitempty"`
	Path            string `json:"path,omitempty"`
	URL             string `json:"url,omitempty"`
	Name            string `json:"name,omitempty"`
	Content         string `json:"content,omitempty"`
	KindOverride    string `json:"kindOverride,omitempty"`
	resolvedContent string
	resolvedName    string
	resolvedError   error
}

type CollectionImportPreviewRequest struct {
	WorkspaceID     string                   `json:"workspaceId,omitempty"`
	DestinationRoot string                   `json:"destinationRoot,omitempty"`
	Sources         []CollectionImportSource `json:"sources"`
}

type CollectionImportPreview struct {
	Rows []CollectionImportPreviewRow `json:"rows"`
}

type CollectionImportPreviewRow struct {
	SourceID        string                               `json:"sourceId"`
	CandidateID     string                               `json:"candidateId"`
	SourceName      string                               `json:"sourceName"`
	SourcePath      string                               `json:"sourcePath,omitempty"`
	DetectedKind    string                               `json:"detectedKind"`
	Confidence      string                               `json:"confidence"`
	CollectionName  string                               `json:"collectionName,omitempty"`
	CollectionID    string                               `json:"collectionId,omitempty"`
	EnvironmentIDs  []string                             `json:"environmentIds,omitempty"`
	FolderIDs       []string                             `json:"folderIds,omitempty"`
	RequestIDs      []string                             `json:"requestIds,omitempty"`
	Environments    []CollectionImportEnvironmentPreview `json:"environments,omitempty"`
	Folders         []CollectionImportFolderPreview      `json:"folders,omitempty"`
	Requests        []CollectionImportRequestPreview     `json:"requests,omitempty"`
	Warnings        []string                             `json:"warnings,omitempty"`
	Losses          []string                             `json:"losses,omitempty"`
	Error           string                               `json:"error,omitempty"`
	DefaultSelect   bool                                 `json:"defaultSelect"`
	ContentHash     string                               `json:"contentHash,omitempty"`
	Conflict        string                               `json:"conflict,omitempty"`
	DestinationPath string                               `json:"destinationPath,omitempty"`
	OpenSemantics   string                               `json:"openSemantics,omitempty"`
	ExistingFolder  bool                                 `json:"existingFolder,omitempty"`
}

// CollectionImportEnvironmentPreview, CollectionImportFolderPreview, and
// CollectionImportRequestPreview contain only display metadata. Their IDs are
// opaque selection tokens, never source contents or parser-local IDs.
type CollectionImportEnvironmentPreview struct {
	SelectionID string `json:"selectionId"`
	Name        string `json:"name"`
}

type CollectionImportFolderPreview struct {
	SelectionID string `json:"selectionId"`
	Name        string `json:"name"`
	Path        string `json:"path"`
	ParentPath  string `json:"parentPath,omitempty"`
}

type CollectionImportRequestPreview struct {
	SelectionID string `json:"selectionId"`
	Name        string `json:"name"`
	FolderPath  string `json:"folderPath,omitempty"`
	Method      string `json:"method,omitempty"`
	Type        string `json:"type,omitempty"`
}

type CollectionImportSelection struct {
	SourceID            string   `json:"sourceId"`
	CandidateID         string   `json:"candidateId"`
	EnvironmentIDs      []string `json:"environmentIds,omitempty"`
	FolderIDs           []string `json:"folderIds,omitempty"`
	RequestIDs          []string `json:"requestIds,omitempty"`
	OutputName          string   `json:"outputName,omitempty"`
	KindOverride        string   `json:"kindOverride,omitempty"`
	ConflictAction      string   `json:"conflictAction,omitempty"` // rename, skip, replace
	ExpectedContentHash string   `json:"expectedContentHash"`
	FilterEnvironments  bool     `json:"filterEnvironments,omitempty"`
	FilterFolders       bool     `json:"filterFolders,omitempty"`
	FilterRequests      bool     `json:"filterRequests,omitempty"`
}

type CollectionImportApplyRequest struct {
	WorkspaceID     string                      `json:"workspaceId"`
	DestinationRoot string                      `json:"destinationRoot,omitempty"`
	Sources         []CollectionImportSource    `json:"sources"`
	Selections      []CollectionImportSelection `json:"selections"`
	// US-044. Opt-in rewriting of pm.* to bru.* for Postman sources. Default
	// false: pm.* is native since US-039-043 and more faithful than a textual
	// rewrite, so an imported script now runs as written.
	TranslatePostmanScripts bool `json:"translatePostmanScripts,omitempty"`
}

type CollectionImportApplyResult struct {
	State   AppState                     `json:"state"`
	Applied []CollectionImportPreviewRow `json:"applied,omitempty"`
	Skipped []CollectionImportPreviewRow `json:"skipped,omitempty"`
	Errors  []CollectionImportPreviewRow `json:"errors,omitempty"`
}

type CollectionImportPickerResult struct {
	Paths     []string `json:"paths"`
	Cancelled bool     `json:"cancelled"`
}
