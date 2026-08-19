package core

// Sibling sequencing and the fingerprints that decide when a collection on disk has changed.
//
// Split out of app.go by AST: declarations are identified by the parser
// and copied verbatim from their source offsets.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mutexdev/lite_api/internal/types"
)

func (a *App) ensureCollectionDirectoryForWriteLocked(collection *Collection) error {
	collectionPath := strings.TrimSpace(collection.Path)
	if collectionPath == "" {
		return errors.New("collection path is empty")
	}
	if info, err := os.Stat(collectionPath); errors.Is(err, os.ErrNotExist) {
		return a.writeCollectionFilesLocked(collection)
	} else if err != nil {
		return err
	} else if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", collectionPath)
	}
	return nil
}

type collectionSibling struct {
	kind        string
	folderIndex int
	itemIndex   int
	name        string
	seq         int
}

func (a *App) resequenceCollectionSiblingsLocked(collection *Collection, parentPath, parentDisplayPath string) error {
	parentPath = types.NormalizeFolderPathKey(parentPath)
	parentDisplayPath = types.NormalizeFolderPathKey(parentDisplayPath)
	siblings := []collectionSibling{}
	for i := range collection.Folders {
		folder := collection.Folders[i]
		folderParentPath := types.NormalizeFolderPathKey(types.ParentFolderDisplayPath(folder.Path))
		folderParentDisplayPath := types.NormalizeFolderPathKey(types.ParentFolderDisplayPath(firstNonEmpty(folder.DisplayPath, folder.Name, folder.Path)))
		if folderParentPath != parentPath && folderParentDisplayPath != parentDisplayPath {
			continue
		}
		siblings = append(siblings, collectionSibling{
			kind:        "folder",
			folderIndex: i,
			name:        firstNonEmpty(folder.Name, pathBaseSlash(folder.DisplayPath), pathBaseSlash(folder.Path)),
			seq:         folder.Seq,
		})
	}
	for i := range collection.Items {
		item := collection.Items[i]
		if types.NormalizeFolderPathKey(item.FolderPath) != parentDisplayPath {
			continue
		}
		siblings = append(siblings, collectionSibling{
			kind:      "request",
			itemIndex: i,
			name:      item.Name,
			seq:       item.Seq,
		})
	}
	if len(siblings) == 0 {
		return nil
	}
	ordered := sortSequencedSiblingsLikeBruno(siblings)
	requestSeqChanged := false
	for index, entry := range ordered {
		nextSeq := index + 1
		switch entry.kind {
		case "folder":
			if collection.Folders[entry.folderIndex].Seq == nextSeq {
				continue
			}
			collection.Folders[entry.folderIndex].Seq = nextSeq
			if err := a.writeFolderConfigLocked(collection, collection.Folders[entry.folderIndex]); err != nil {
				return err
			}
		case "request":
			if collection.Items[entry.itemIndex].Seq == nextSeq {
				continue
			}
			collection.Items[entry.itemIndex].Seq = nextSeq
			collection.Items[entry.itemIndex].UpdatedAt = time.Now()
			requestSeqChanged = true
		}
	}
	types.SortFoldersLikeBruno(collection.Folders)
	if requestSeqChanged {
		return a.writeCollectionFilesLocked(collection)
	}
	return nil
}

func sortSequencedSiblingsLikeBruno(siblings []collectionSibling) []collectionSibling {
	alphabetical := append([]collectionSibling(nil), siblings...)
	sort.SliceStable(alphabetical, func(i, j int) bool {
		return strings.ToLower(alphabetical[i].name) < strings.ToLower(alphabetical[j].name)
	})
	withoutSeq := []collectionSibling{}
	withSeq := []collectionSibling{}
	for _, entry := range alphabetical {
		if entry.seq > 0 {
			withSeq = append(withSeq, entry)
			continue
		}
		withoutSeq = append(withoutSeq, entry)
	}
	sort.SliceStable(withSeq, func(i, j int) bool {
		return withSeq[i].seq < withSeq[j].seq
	})
	ordered := withoutSeq
	for _, entry := range withSeq {
		position := entry.seq - 1
		if position < 0 {
			position = 0
		}
		if position >= len(ordered) {
			ordered = append(ordered, entry)
			continue
		}
		ordered = append(ordered[:position+1], ordered[position:]...)
		ordered[position] = entry
	}
	return ordered
}

func (a *App) refreshGitCollectionAvailabilityLocked() {
	for wi := range a.state.Workspaces {
		for ci := range a.state.Workspaces[wi].Collections {
			collection := &a.state.Workspaces[wi].Collections[ci]
			if collection.Scratch {
				continue
			}
			if strings.TrimSpace(collection.Remote) == "" || strings.TrimSpace(collection.Path) == "" {
				collection.NotFoundLocally = false
				continue
			}
			if info, err := os.Stat(collection.Path); err == nil && info.IsDir() {
				collection.NotFoundLocally = false
				continue
			}
			collection.NotFoundLocally = true
			collection.Items = nil
			collection.Folders = nil
			a.removeOpenTabsForCollectionLocked(collection.ID)
		}
	}
}

func (a *App) seedCollectionWatchFingerprintLocked(collectionPath string) {
	collectionPath = strings.TrimSpace(collectionPath)
	if collectionPath == "" {
		return
	}
	if a.collectionWatchFingerprints == nil {
		a.collectionWatchFingerprints = map[string]string{}
	}
	collectionPath = filepath.Clean(collectionPath)
	fingerprint, err := collectionWatchFingerprint(collectionPath)
	if err != nil {
		return
	}
	a.collectionWatchFingerprints[collectionPath] = fingerprint
}

func (a *App) clearCollectionWatchFingerprintLocked(collectionPath string) {
	if a.collectionWatchFingerprints == nil {
		return
	}
	collectionPath = strings.TrimSpace(collectionPath)
	if collectionPath == "" {
		return
	}
	delete(a.collectionWatchFingerprints, filepath.Clean(collectionPath))
}

func collectionWatchCandidate(collection Collection) bool {
	return !collection.Scratch && !collection.NotFoundLocally && strings.TrimSpace(collection.Path) != ""
}

func preserveCollectionRuntimeState(previous Collection, refreshed *Collection) {
	if refreshed == nil {
		return
	}
	refreshed.SecurityConfig = normalizeCollectionSecurityConfig(previous.SecurityConfig)
	previousItems := map[string]RequestItem{}
	for _, item := range previous.Items {
		previousItems[item.ID] = item
	}
	for i := range refreshed.Items {
		if previousItem, ok := previousItems[refreshed.Items[i].ID]; ok {
			refreshed.Items[i].Response = previousItem.Response
			refreshed.Items[i].Timeline = previousItem.Timeline
		}
	}
}
