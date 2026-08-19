package core

// Finding a request or folder inside a collection, and the folder-path algebra the tree rests on.
//
// Split out of app.go by AST: declarations are identified by the parser and
// copied verbatim from their source offsets.

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/mutexdev/lite_api/internal/types"
)

func findFolderConfig(collection *Collection, folderPath string) (*FolderConfig, error) {
	index, err := findFolderConfigIndex(collection, folderPath)
	if err != nil {
		return nil, err
	}
	return &collection.Folders[index], nil
}

func findFolderConfigIndex(collection *Collection, folderPath string) (int, error) {
	normalized := types.NormalizeFolderPathKey(folderPath)
	if normalized == "" {
		return -1, errors.New("folder path is required")
	}
	for i := range collection.Folders {
		folder := &collection.Folders[i]
		if types.NormalizeFolderPathKey(folder.Path) == normalized || types.NormalizeFolderPathKey(folder.DisplayPath) == normalized {
			return i, nil
		}
	}
	return -1, fmt.Errorf("folder %s not found", folderPath)
}

func collectionFolderParentPaths(collection *Collection, parentFolderPath string) (string, string, error) {
	parentFolderPath = types.NormalizeFolderPathKey(parentFolderPath)
	if parentFolderPath == "" {
		return "", "", nil
	}
	parent, err := findFolderConfig(collection, parentFolderPath)
	if err != nil {
		return "", "", err
	}
	parentPath := types.NormalizeFolderPathKey(parent.Path)
	parentDisplayPath := types.NormalizeFolderPathKey(firstNonEmpty(parent.DisplayPath, parent.Name, parent.Path))
	return parentPath, parentDisplayPath, nil
}

func joinCollectionFolderPath(parent, child string) string {
	parent = types.NormalizeFolderPathKey(parent)
	child = strings.Trim(child, "/")
	if parent == "" {
		return child
	}
	if child == "" {
		return parent
	}
	return parent + "/" + child
}

func collectionHasChildFolder(collection *Collection, parentPath, directoryName string) bool {
	parentPath = types.NormalizeFolderPathKey(parentPath)
	directoryName = strings.TrimSpace(directoryName)
	for _, folder := range collection.Folders {
		folderPath := types.NormalizeFolderPathKey(folder.Path)
		if types.NormalizeFolderPathKey(types.ParentFolderDisplayPath(folderPath)) != parentPath {
			continue
		}
		if strings.TrimSpace(filepath.Base(filepath.FromSlash(folderPath))) == directoryName {
			return true
		}
	}
	return false
}

func nextCollectionFolderSeq(collection Collection, parentPath string) int {
	parentPath = types.NormalizeFolderPathKey(parentPath)
	count := 0
	for _, folder := range collection.Folders {
		if types.NormalizeFolderPathKey(types.ParentFolderDisplayPath(folder.Path)) == parentPath {
			count++
		}
	}
	for _, item := range collection.Items {
		if types.NormalizeFolderPathKey(item.FolderPath) == parentPath {
			count++
		}
	}
	return count + 1
}

// updateCollectionFolderRenameState rewrites a collection's folders and items
// after a folder has been renamed on disk.
//
// The four paths are normalised up front, and only ONE of the four is
// observable: newPath is compared with `==` against a normalised folder path
// further down, and a bare comparison does not normalise anything. The other
// three reach only replaceFolderPathPrefix and pathBaseSlash, both of which
// normalise their own arguments — so deleting those three changes no behaviour,
// and a control confirms it.
//
// They stay anyway. The contract of this function is "give me a path in any
// form", stated once at the top; normalising one of four inputs and trusting
// callees for the rest would be harder to reason about than either extreme, and
// three string cleanups per rename cost nothing.
func updateCollectionFolderRenameState(collection *Collection, oldPath, newPath, oldDisplayPath, newDisplayPath, oldDir, newDir string) {
	oldPath = types.NormalizeFolderPathKey(oldPath)
	newPath = types.NormalizeFolderPathKey(newPath)
	oldDisplayPath = types.NormalizeFolderPathKey(oldDisplayPath)
	newDisplayPath = types.NormalizeFolderPathKey(newDisplayPath)
	for i := range collection.Folders {
		folder := &collection.Folders[i]
		folder.Path = replaceFolderPathPrefix(folder.Path, oldPath, newPath)
		folder.DisplayPath = replaceFolderPathPrefix(firstNonEmpty(folder.DisplayPath, folder.Name, folder.Path), oldDisplayPath, newDisplayPath)
		if types.NormalizeFolderPathKey(folder.Path) == newPath {
			folder.Name = pathBaseSlash(newDisplayPath)
		}
	}
	for i := range collection.Items {
		item := &collection.Items[i]
		item.FolderPath = replaceFolderPathPrefix(item.FolderPath, oldDisplayPath, newDisplayPath)
		if pathInside(oldDir, item.FilePath) {
			rel, err := filepath.Rel(oldDir, item.FilePath)
			if err == nil {
				item.FilePath = filepath.Clean(filepath.Join(newDir, rel))
			}
		}
	}
	types.SortFoldersLikeBruno(collection.Folders)
}

func replaceFolderPathPrefix(value, oldPrefix, newPrefix string) string {
	value = types.NormalizeFolderPathKey(value)
	oldPrefix = types.NormalizeFolderPathKey(oldPrefix)
	newPrefix = types.NormalizeFolderPathKey(newPrefix)
	if oldPrefix == "" {
		return value
	}
	if value == oldPrefix {
		return newPrefix
	}
	if strings.HasPrefix(value, oldPrefix+"/") {
		return joinCollectionFolderPath(newPrefix, strings.TrimPrefix(value, oldPrefix+"/"))
	}
	return value
}

func folderPathHasPrefix(value, prefix string) bool {
	value = types.NormalizeFolderPathKey(value)
	prefix = types.NormalizeFolderPathKey(prefix)
	if value == "" || prefix == "" {
		return false
	}
	return value == prefix || strings.HasPrefix(value, prefix+"/")
}

func mergeFolderSettingsUpdate(folder *FolderConfig, updated FolderConfig) {
	name := strings.TrimSpace(updated.Name)
	if name == "" {
		name = folder.Name
	}
	if name == "" {
		name = filepath.Base(filepath.FromSlash(folder.Path))
	}
	folder.Name = name
	folder.Headers = updated.Headers
	folder.Variables = updated.Variables
	folder.ResVariables = updated.ResVariables
	folder.Auth = updated.Auth
	folder.PreScript = updated.PreScript
	folder.PostScript = updated.PostScript
	folder.Tests = updated.Tests
	folder.Docs = updated.Docs
}

func (a *App) findItemLocked(collectionID, itemID string) (*RequestItem, error) {
	collection, err := a.findCollectionLocked(collectionID)
	if err != nil {
		return nil, err
	}
	return findItem(collection, itemID)
}

func findItemIndex(collection *Collection, itemID string) (int, error) {
	for i := range collection.Items {
		if collection.Items[i].ID == itemID {
			return i, nil
		}
	}
	return -1, fmt.Errorf("request %s not found", itemID)
}

func findItem(collection *Collection, itemID string) (*RequestItem, error) {
	index, err := findItemIndex(collection, itemID)
	if err != nil {
		return nil, err
	}
	return &collection.Items[index], nil
}

func findRunRequestItem(collection *Collection, targetRef string) (*RequestItem, error) {
	target := normalizeRunRequestRef(targetRef)
	if target == "" {
		return nil, errors.New("bru.runRequest requires a request path or name")
	}
	for i := range collection.Items {
		item := &collection.Items[i]
		for _, candidate := range runRequestItemRefs(*collection, *item) {
			if candidate == target {
				return item, nil
			}
		}
	}
	return nil, fmt.Errorf("bru.runRequest: invalid request path - %s", targetRef)
}

func runRequestItemRefs(collection Collection, item RequestItem) []string {
	refs := []string{item.ID, item.Name}
	if item.FolderPath != "" && item.Name != "" {
		refs = append(refs, filepath.ToSlash(filepath.Join(item.FolderPath, item.Name)))
	}
	if item.FilePath != "" {
		refs = append(refs, item.FilePath)
		if pathInside(collection.Path, item.FilePath) {
			if rel, err := filepath.Rel(collection.Path, item.FilePath); err == nil {
				refs = append(refs, rel)
			}
		}
	}
	normalized := []string{}
	seen := map[string]bool{}
	for _, ref := range refs {
		ref = normalizeRunRequestRef(ref)
		for _, candidate := range runRequestRefVariants(ref) {
			if candidate != "" && !seen[candidate] {
				seen[candidate] = true
				normalized = append(normalized, candidate)
			}
		}
	}
	return normalized
}

func normalizeRunRequestRef(value string) string {
	value = filepath.ToSlash(strings.TrimSpace(value))
	value = strings.TrimPrefix(value, "./")
	value = strings.Trim(value, "/")
	return value
}

func runRequestRefVariants(value string) []string {
	value = normalizeRunRequestRef(value)
	if value == "" {
		return nil
	}
	variants := []string{value}
	ext := strings.ToLower(filepath.Ext(value))
	if ext == ".bru" || ext == ".yml" || ext == ".yaml" {
		variants = append(variants, strings.TrimSuffix(value, filepath.Ext(value)))
	} else {
		variants = append(variants, value+".bru", value+".yml", value+".yaml")
	}
	return variants
}

func findItemInState(state AppState, collectionID, itemID string) (RequestItem, bool) {
	for _, ws := range state.Workspaces {
		for _, collection := range ws.Collections {
			if collection.ID != collectionID {
				continue
			}
			for _, item := range collection.Items {
				if item.ID == itemID {
					return item, true
				}
			}
		}
	}
	return RequestItem{}, false
}

func (a *App) isTransientRequestLocked(collectionID, itemID string) bool {
	for wi := range a.state.Workspaces {
		for ci := range a.state.Workspaces[wi].Collections {
			collection := &a.state.Workspaces[wi].Collections[ci]
			if collection.ID != collectionID {
				continue
			}
			// Tab transience is a persistence concern and belongs only to the
			// ephemeral Scratch collection. A never-saved normal request uses the
			// item Transient flag for discard/export behavior, but its tab and
			// draft must survive a crash/restart until the user resolves it.
			return collection.Scratch
		}
	}
	return false
}

func (a *App) openFirstCollectionItemLocked(collection Collection) {
	if len(collection.Items) == 0 {
		return
	}
	a.openTabLocked(collection.ID, collection.Items[0].ID, "request")
}

func collectionHasDraftRequests(collection Collection) bool {
	for _, item := range collection.Items {
		if item.Draft {
			return true
		}
	}
	return false
}

func missingRequestIDs(previous, refreshed []RequestItem) map[string]bool {
	nextIDs := map[string]bool{}
	for _, item := range refreshed {
		nextIDs[item.ID] = true
	}
	missing := map[string]bool{}
	for _, item := range previous {
		if !nextIDs[item.ID] {
			missing[item.ID] = true
		}
	}
	return missing
}
