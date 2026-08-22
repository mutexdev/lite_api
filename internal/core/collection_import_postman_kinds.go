// The Postman documents that are not a v2 collection (US-058).
//
// Postman exports several shapes and the import panel understood one of them.
// An environment, a globals file, a v1 collection and a workspace "data dump"
// all reached the same dead end -- "source is ambiguous; choose an import kind
// manually" -- and nothing in that kind list could import any of them, so the
// advice pointed nowhere. A dump arrives as a .zip, which the file picker
// advertised in its filter and then refused.
package core

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"

	"github.com/mutexdev/lite_api/internal/store/yamlstore"
)

// collectionImportMaxArchiveEntries bounds a ZIP so a crafted archive cannot
// make the preview enumerate forever. A Postman workspace export holds one file
// per collection and environment; a hundred is already an unusual workspace.
const collectionImportMaxArchiveEntries = 256

// postmanEnvironmentDocument reports whether a decoded document is a Postman
// environment or globals file.
//
// The scope marker is what Postman writes, and is checked first. The looser
// shape -- name plus a values list, and no item list -- catches the same file
// after it has been through a tool that dropped the underscore-prefixed keys,
// which several export scripts do.
func postmanEnvironmentDocument(raw map[string]interface{}) bool {
	if scope, ok := raw["_postman_variable_scope"].(string); ok {
		switch strings.ToLower(strings.TrimSpace(scope)) {
		case "environment", "globals":
			return true
		}
	}
	if _, hasItem := raw["item"]; hasItem {
		return false
	}
	if _, hasName := raw["name"]; !hasName {
		return false
	}
	values, ok := raw["values"].([]interface{})
	if !ok {
		return false
	}
	for _, entry := range values {
		if entryMap, ok := mapValue(entry); ok {
			if _, hasKey := entryMap["key"]; hasKey {
				return true
			}
		}
	}
	return len(values) == 0
}

// postmanV1Document reports whether this is a v1 collection, which Postman
// stopped exporting in 2016 but which is still what sits in a great many
// repositories and gists.
func postmanV1Document(raw map[string]interface{}) bool {
	if _, hasItem := raw["item"]; hasItem {
		return false
	}
	if _, hasRequests := raw["requests"]; !hasRequests {
		return false
	}
	_, hasName := raw["name"]
	_, hasOrder := raw["order"]
	_, hasFolders := raw["folders"]
	return hasName || hasOrder || hasFolders
}

// postmanDumpDocument reports whether this is a workspace dump: the envelope
// Postman writes when exporting everything at once.
func postmanDumpDocument(raw map[string]interface{}) bool {
	if _, hasItem := raw["item"]; hasItem {
		return false
	}
	_, hasCollections := raw["collections"]
	_, hasEnvironments := raw["environments"]
	return hasCollections || hasEnvironments
}

// collectionFromPostmanEnvironment reads an environment or globals file into a
// collection carrying nothing but those environments.
//
// They are modelled as a collection because that is what the preview row, the
// selection filter and the content hash are all built around; the apply step
// recognises the kind and puts them in the workspace's global environments
// rather than writing a collection folder. A standalone environment file is not
// attached to any collection, and inventing one to hold it would leave the user
// with an empty collection they did not ask for.
func collectionFromPostmanEnvironment(content, name string) (Collection, error) {
	environments, err := yamlstore.ParseImportedGlobalEnvironments(content)
	if err != nil {
		return Collection{}, err
	}
	if len(environments) == 0 {
		return Collection{}, errors.New("no environments found in this Postman environment file")
	}
	collection := Collection{
		ID:           newID("collection"),
		Name:         firstNonEmpty(strings.TrimSpace(environments[0].Name), strings.TrimSpace(name), "Imported environment"),
		Format:       "postman",
		Auth:         AuthConfig{Mode: "none"},
		Environments: environments,
	}
	return collection, nil
}

// expandCollectionImportSource splits a source that holds several documents.
//
// Returns nil when the source is a single document, which is the common case
// and leaves the existing path untouched.
func expandCollectionImportSource(content, name string) ([]collectionImportChild, error) {
	// Content, not the extension: a file named .zip that holds a Postman
	// collection is a real thing people have, and it must stay rescuable by
	// choosing a kind manually rather than dying as an unreadable archive.
	if looksLikeZipArchive(content) {
		return expandZipImportSource(content)
	}
	var raw map[string]interface{}
	if err := json.Unmarshal([]byte(trimImportJSONPrefix(content)), &raw); err != nil || raw == nil {
		return nil, nil
	}
	if !postmanDumpDocument(raw) {
		return nil, nil
	}
	children := []collectionImportChild{}
	children = append(children, postmanDumpChildren(raw["collections"], "collection")...)
	children = append(children, postmanDumpChildren(raw["environments"], "environment")...)
	if len(children) == 0 {
		return nil, errors.New("this Postman data dump holds no collections or environments")
	}
	return children, nil
}

// postmanDumpChildren re-serialises each entry so it can travel through the
// same detection every other source goes through, rather than being parsed by a
// second implementation that would drift from the first.
func postmanDumpChildren(value interface{}, label string) []collectionImportChild {
	entries, ok := value.([]interface{})
	if !ok {
		return nil
	}
	children := make([]collectionImportChild, 0, len(entries))
	for index, entry := range entries {
		data, err := json.Marshal(entry)
		if err != nil {
			continue
		}
		children = append(children, collectionImportChild{
			name:    postmanDumpChildName(entry, label, index),
			content: string(data),
		})
	}
	return children
}

func postmanDumpChildName(entry interface{}, label string, index int) string {
	entryMap, ok := mapValue(entry)
	if ok {
		if info, ok := mapValue(entryMap["info"]); ok {
			if name := strings.TrimSpace(yamlScalarString(info["name"])); name != "" {
				return name
			}
		}
		if name := strings.TrimSpace(yamlScalarString(entryMap["name"])); name != "" {
			return name
		}
	}
	return fmt.Sprintf("Untitled %s %d", label, index+1)
}

func looksLikeZipArchive(content string) bool {
	return strings.HasPrefix(content, "PK\x03\x04") || strings.HasPrefix(content, "PK\x05\x06")
}

// expandZipImportSource reads the importable documents out of a ZIP.
//
// A Postman workspace export is a ZIP of ordinary JSON files, so nothing here
// needs to understand the archive beyond finding them. Entries that are not
// importable -- the readme, an image -- are skipped rather than reported, since
// their presence is normal and an error per file would bury the ones that
// matter.
func expandZipImportSource(content string) ([]collectionImportChild, error) {
	data := []byte(content)
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, errors.New("this ZIP could not be read")
	}
	if len(reader.File) > collectionImportMaxArchiveEntries {
		return nil, fmt.Errorf("this ZIP holds more than %d files", collectionImportMaxArchiveEntries)
	}
	children := []collectionImportChild{}
	var total int64
	for _, entry := range reader.File {
		if entry.FileInfo().IsDir() || !importableArchiveEntry(entry.Name) {
			continue
		}
		// Checked before reading: the declared size is what makes a zip bomb
		// cheap to refuse and expensive to decompress.
		if entry.UncompressedSize64 > uint64(collectionImportMaxBytes) {
			continue
		}
		file, err := entry.Open()
		if err != nil {
			continue
		}
		body, err := io.ReadAll(io.LimitReader(file, collectionImportMaxBytes+1))
		closeErr := file.Close()
		if err != nil || closeErr != nil || len(body) > collectionImportMaxBytes {
			continue
		}
		total += int64(len(body))
		if total > collectionImportMaxTreeBytes {
			return nil, errors.New("this ZIP expands beyond the import size limit")
		}
		children = append(children, collectionImportChild{name: path.Base(entry.Name), content: string(body)})
	}
	if len(children) == 0 {
		return nil, errors.New("this ZIP holds nothing importable")
	}
	return children, nil
}

func importableArchiveEntry(name string) bool {
	base := strings.ToLower(path.Base(name))
	if strings.HasPrefix(base, ".") || strings.HasPrefix(name, "__MACOSX/") {
		return false
	}
	for _, extension := range []string{".json", ".yaml", ".yml", ".har", ".bru"} {
		if strings.HasSuffix(base, extension) {
			return true
		}
	}
	return false
}
