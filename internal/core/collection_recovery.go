package core

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mutexdev/lite_api/internal/recovery"
)

// DeleteRequestRecoverable deletes a request only after a private, durable
// collection snapshot has been staged. It intentionally removes all request
// and response-example tabs for the request; RestoreRecoveryEntry puts them
// back with their pane state.
func (a *App) DeleteRequestRecoverable(collectionID, itemID string) (recovery.RecoverableDeleteResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return recovery.RecoverableDeleteResult{}, err
	}
	wi, ci, ws, collection, err := a.recoveryCollectionLocked(collectionID)
	if err != nil {
		return recovery.RecoverableDeleteResult{}, err
	}
	if collection.NotFoundLocally {
		return recovery.RecoverableDeleteResult{}, errors.New("collection is not cloned locally")
	}
	if strings.TrimSpace(collection.Path) == "" {
		return recovery.RecoverableDeleteResult{}, errors.New("collection path is empty")
	}
	index, err := findItemIndex(collection, itemID)
	if err != nil {
		return recovery.RecoverableDeleteResult{}, err
	}
	item := collection.Items[index]
	oldFile, err := collectionRequestFilesystemPath(collection, item)
	if err != nil {
		return recovery.RecoverableDeleteResult{}, err
	}
	if info, statErr := os.Stat(oldFile); statErr != nil {
		if errors.Is(statErr, os.ErrNotExist) {
			return recovery.RecoverableDeleteResult{}, errors.New("the file does not exist")
		}
		return recovery.RecoverableDeleteResult{}, statErr
	} else if info.IsDir() {
		return recovery.RecoverableDeleteResult{}, fmt.Errorf("%s is not a request file", oldFile)
	}

	snapshot, err := a.stageCollectionRecoveryLocked(recovery.KindRequest, wi, ci, ws, collection, item.Name, []string{item.ID})
	if err != nil {
		return recovery.RecoverableDeleteResult{}, err
	}
	rollback := func(cause error) (recovery.RecoverableDeleteResult, error) {
		a.rollbackCollectionRecoveryLocked(snapshot)
		_ = recovery.RemoveEntry(a.dataDir, snapshot.Entry.WorkspaceID, snapshot.Entry.ID)
		return recovery.RecoverableDeleteResult{}, cause
	}
	if err := os.Remove(oldFile); err != nil {
		return rollback(err)
	}
	parentPath, parentDisplayPath := recoveryParentPaths(collection, item)
	collection.Items = append(collection.Items[:index], collection.Items[index+1:]...)
	if err := a.resequenceCollectionSiblingsLocked(collection, parentPath, parentDisplayPath); err != nil {
		return rollback(err)
	}
	a.seedCollectionWatchFingerprintLocked(collection.Path)
	a.removeRecoveryTabsLocked(collection.ID, map[string]bool{item.ID: true}, false)
	now := time.Now()
	collection.UpdatedAt = now
	ws.UpdatedAt = now
	if collection.Scratch {
		if err := a.writeScratchCollectionMetadataLocked(collection); err != nil {
			return rollback(err)
		}
	}
	if err := a.persistLocked(); err != nil {
		return rollback(err)
	}
	if err := a.ensureReadyLocked(); err != nil {
		return rollback(err)
	}
	if err := a.commitCollectionRecoveryLocked(&snapshot); err != nil {
		return rollback(err)
	}
	entry, err := recovery.MarkEntryRestorable(a.dataDir, snapshot.Entry.WorkspaceID, snapshot.Entry.ID)
	if err != nil {
		return rollback(err)
	}
	snapshot.Entry = entry
	return recovery.RecoverableDeleteResult{State: a.state, Entry: snapshot.Entry}, nil
}

// DeleteFolderRecoverable is the recoverable equivalent of DeleteFolder.
func (a *App) DeleteFolderRecoverable(collectionID, folderPath string) (recovery.RecoverableDeleteResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return recovery.RecoverableDeleteResult{}, err
	}
	wi, ci, ws, collection, err := a.recoveryCollectionLocked(collectionID)
	if err != nil {
		return recovery.RecoverableDeleteResult{}, err
	}
	if collection.NotFoundLocally {
		return recovery.RecoverableDeleteResult{}, errors.New("collection is not cloned locally")
	}
	folderIndex, err := findFolderConfigIndex(collection, folderPath)
	if err != nil {
		return recovery.RecoverableDeleteResult{}, err
	}
	folder := collection.Folders[folderIndex]
	oldPath := normalizeFolderPathKey(folder.Path)
	oldDisplayPath := normalizeFolderPathKey(firstNonEmpty(folder.DisplayPath, folder.Name, folder.Path))
	if oldPath == "" {
		return recovery.RecoverableDeleteResult{}, errors.New("folder path is required")
	}
	if err := a.ensureCollectionDirectoryForWriteLocked(collection); err != nil {
		return recovery.RecoverableDeleteResult{}, err
	}
	targetDir := filepath.Join(collection.Path, filepath.FromSlash(oldPath))
	if !pathInside(collection.Path, targetDir) {
		return recovery.RecoverableDeleteResult{}, fmt.Errorf("folder path %s escapes collection", folderPath)
	}
	if info, statErr := os.Stat(targetDir); statErr != nil {
		if errors.Is(statErr, os.ErrNotExist) {
			return recovery.RecoverableDeleteResult{}, errors.New("the directory does not exist")
		}
		return recovery.RecoverableDeleteResult{}, statErr
	} else if !info.IsDir() {
		return recovery.RecoverableDeleteResult{}, fmt.Errorf("%s is not a directory", targetDir)
	}

	removedIDs := recoveryFolderRequestIDs(collection, oldPath, oldDisplayPath, targetDir)
	affected := make([]string, 0, len(removedIDs))
	for id := range removedIDs {
		affected = append(affected, id)
	}
	snapshot, err := a.stageCollectionRecoveryLocked(recovery.KindFolder, wi, ci, ws, collection, firstNonEmpty(folder.Name, pathBaseSlash(oldDisplayPath), pathBaseSlash(oldPath)), affected)
	if err != nil {
		return recovery.RecoverableDeleteResult{}, err
	}
	rollback := func(cause error) (recovery.RecoverableDeleteResult, error) {
		a.rollbackCollectionRecoveryLocked(snapshot)
		_ = recovery.RemoveEntry(a.dataDir, snapshot.Entry.WorkspaceID, snapshot.Entry.ID)
		return recovery.RecoverableDeleteResult{}, cause
	}

	remainingItems := collection.Items[:0]
	for _, item := range collection.Items {
		if !removedIDs[item.ID] {
			remainingItems = append(remainingItems, item)
		}
	}
	collection.Items = remainingItems
	remainingFolders := collection.Folders[:0]
	for _, candidate := range collection.Folders {
		candidatePath := normalizeFolderPathKey(candidate.Path)
		candidateDisplayPath := normalizeFolderPathKey(firstNonEmpty(candidate.DisplayPath, candidate.Name, candidate.Path))
		if !folderPathHasPrefix(candidatePath, oldPath) && !folderPathHasPrefix(candidateDisplayPath, oldDisplayPath) {
			remainingFolders = append(remainingFolders, candidate)
		}
	}
	collection.Folders = remainingFolders
	sortFoldersLikeBruno(collection.Folders)
	if err := os.RemoveAll(targetDir); err != nil {
		return rollback(err)
	}
	if err := a.resequenceCollectionSiblingsLocked(collection, normalizeFolderPathKey(parentFolderDisplayPath(oldPath)), normalizeFolderPathKey(parentFolderDisplayPath(oldDisplayPath))); err != nil {
		return rollback(err)
	}
	a.seedCollectionWatchFingerprintLocked(collection.Path)
	a.removeRecoveryTabsLocked(collection.ID, removedIDs, false)
	now := time.Now()
	collection.UpdatedAt = now
	ws.UpdatedAt = now
	if err := a.persistLocked(); err != nil {
		return rollback(err)
	}
	if err := a.ensureReadyLocked(); err != nil {
		return rollback(err)
	}
	if err := a.commitCollectionRecoveryLocked(&snapshot); err != nil {
		return rollback(err)
	}
	entry, err := recovery.MarkEntryRestorable(a.dataDir, snapshot.Entry.WorkspaceID, snapshot.Entry.ID)
	if err != nil {
		return rollback(err)
	}
	snapshot.Entry = entry
	return recovery.RecoverableDeleteResult{State: a.state, Entry: snapshot.Entry}, nil
}

// RemoveCollectionRecoverable only removes the collection from LiteAPI state.
// It never stages, removes, or rewrites any collection file.
func (a *App) RemoveCollectionRecoverable(collectionID string) (recovery.RecoverableDeleteResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return recovery.RecoverableDeleteResult{}, err
	}
	wi, ci, ws, collection, err := a.recoveryCollectionLocked(collectionID)
	if err != nil {
		return recovery.RecoverableDeleteResult{}, err
	}
	if collection.Scratch {
		return recovery.RecoverableDeleteResult{}, errors.New("scratch collection cannot be removed")
	}
	snapshot, err := a.stageCollectionRecoveryMetadataLocked(wi, ci, ws, collection)
	if err != nil {
		return recovery.RecoverableDeleteResult{}, err
	}
	rollback := func(cause error) (recovery.RecoverableDeleteResult, error) {
		a.rollbackCollectionRemovalLocked(snapshot)
		_ = recovery.RemoveEntry(a.dataDir, snapshot.Entry.WorkspaceID, snapshot.Entry.ID)
		return recovery.RecoverableDeleteResult{}, cause
	}
	if strings.TrimSpace(collection.Remote) != "" && strings.TrimSpace(collection.Path) != "" {
		if err := updateManagedGitIgnore(ws.Path, collection.Path, false); err != nil {
			return rollback(err)
		}
	}
	if _, exists, content, err := recovery.GitIgnoreSnapshot(*ws); err != nil {
		return rollback(err)
	} else {
		snapshot.PostGitIgnoreExists, snapshot.PostGitIgnoreContent = exists, content
	}
	a.clearCollectionWatchFingerprintLocked(collection.Path)
	ws.Collections = append(ws.Collections[:ci], ws.Collections[ci+1:]...)
	ws.UpdatedAt = time.Now()
	a.removeRecoveryTabsLocked(collection.ID, nil, true)
	if err := recovery.WriteSnapshot(a.dataDir, snapshot); err != nil {
		return rollback(err)
	}
	if err := a.persistLocked(); err != nil {
		return rollback(err)
	}
	snapshot.PostOpenTabs = append([]OpenTab(nil), a.state.OpenTabs...)
	snapshot.PostClosedTabs = append([]OpenTab(nil), a.state.ClosedTabs...)
	snapshot.PostActiveTabID = a.state.ActiveTabID
	if err := recovery.WriteSnapshot(a.dataDir, snapshot); err != nil {
		return rollback(err)
	}
	entry, err := recovery.MarkEntryRestorable(a.dataDir, snapshot.Entry.WorkspaceID, snapshot.Entry.ID)
	if err != nil {
		return rollback(err)
	}
	snapshot.Entry = entry
	return recovery.RecoverableDeleteResult{State: a.state, Entry: snapshot.Entry}, nil
}

func (a *App) ListRecoveryEntries() ([]recovery.Entry, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return nil, err
	}
	return recovery.RemoveExpiredEntries(a.dataDir, a.state.ActiveWorkspaceID, time.Now().UTC())
}

func (a *App) DiscardRecoveryEntry(entryID string) (bool, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return false, err
	}
	if _, err := recovery.FindEntry(a.dataDir, a.state.ActiveWorkspaceID, entryID); err != nil {
		return false, err
	}
	if err := recovery.RemoveEntry(a.dataDir, a.state.ActiveWorkspaceID, entryID); err != nil {
		return false, err
	}
	return true, nil
}

func (a *App) RestoreRecoveryEntry(entryID string) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return AppState{}, err
	}
	entry, err := recovery.FindEntry(a.dataDir, a.state.ActiveWorkspaceID, entryID)
	if err != nil {
		return AppState{}, err
	}
	if !entry.ExpiresAt.After(time.Now().UTC()) {
		_ = recovery.RemoveEntry(a.dataDir, a.state.ActiveWorkspaceID, entryID)
		return AppState{}, fmt.Errorf("recovery entry %s has expired", entryID)
	}
	if !entry.Restorable {
		return AppState{}, fmt.Errorf("recovery entry %s is still being committed", entryID)
	}
	snapshot, err := recovery.ReadSnapshot(a.dataDir, a.state.ActiveWorkspaceID, entryID)
	if err != nil {
		return AppState{}, err
	}
	if snapshot.Entry.ID != entry.ID {
		return AppState{}, fmt.Errorf("recovery entry %s is invalid", entryID)
	}
	if entry.Kind == recovery.KindCollection {
		return a.restoreCollectionRemovalLocked(snapshot)
	}
	return a.restoreCollectionTreeLocked(snapshot)
}

func (a *App) restoreCollectionTreeLocked(snapshot recovery.Snapshot) (AppState, error) {
	ws, err := a.recoveryWorkspaceLocked(snapshot.Entry.WorkspaceID)
	if err != nil {
		return AppState{}, &recovery.RestoreConflictError{EntryID: snapshot.Entry.ID, Reason: "the workspace is no longer available"}
	}
	ci := findRecoveryCollectionIndex(ws.Collections, snapshot.Entry.CollectionID)
	if ci < 0 {
		return AppState{}, &recovery.RestoreConflictError{EntryID: snapshot.Entry.ID, Reason: "the collection is no longer open"}
	}
	current := &ws.Collections[ci]
	if current.ID != snapshot.PostCollection.ID || filepath.Clean(current.Path) != filepath.Clean(snapshot.PostCollection.Path) {
		return AppState{}, &recovery.RestoreConflictError{EntryID: snapshot.Entry.ID, Reason: "the collection identity changed after deletion"}
	}
	if !recoveryCollectionSemanticEqual(*current, snapshot.PostCollection) {
		return AppState{}, &recovery.RestoreConflictError{EntryID: snapshot.Entry.ID, Reason: "collection state changed after deletion"}
	}
	if !recoveryTabsEqual(a.state.OpenTabs, snapshot.PostOpenTabs) || !recoveryTabsEqual(a.state.ClosedTabs, snapshot.PostClosedTabs) || a.state.ActiveTabID != snapshot.PostActiveTabID {
		return AppState{}, &recovery.RestoreConflictError{EntryID: snapshot.Entry.ID, Reason: "tab state changed after deletion"}
	}
	fingerprint, err := recovery.CollectionFingerprint(current.Path)
	if err != nil {
		return AppState{}, err
	}
	if fingerprint != snapshot.PostFingerprint {
		return AppState{}, &recovery.RestoreConflictError{EntryID: snapshot.Entry.ID, Reason: "collection files changed after deletion"}
	}
	rollbackPath, err := os.MkdirTemp("", "liteapi-recovery-restore-")
	if err != nil {
		return AppState{}, err
	}
	// Best-effort cleanup of a staging temp dir; nothing can act on a failure here.
	defer func() { _ = os.RemoveAll(rollbackPath) }()
	if err := recovery.CopyTree(current.Path, rollbackPath); err != nil {
		return AppState{}, fmt.Errorf("stage restore rollback: %w", err)
	}
	postCollection := cloneRecoveryCollection(*current)
	postWorkspaceUpdatedAt := ws.UpdatedAt
	postOpenTabs := append([]OpenTab(nil), a.state.OpenTabs...)
	postClosedTabs := append([]OpenTab(nil), a.state.ClosedTabs...)
	postActiveTabID := a.state.ActiveTabID
	restoreSource, err := os.MkdirTemp("", "liteapi-recovery-payload-")
	if err != nil {
		return AppState{}, err
	}
	// Best-effort cleanup of a staging temp dir; nothing can act on a failure here.
	defer func() { _ = os.RemoveAll(restoreSource) }()
	if err := recovery.RestorePayload(a.dataDir, snapshot.Entry.WorkspaceID, snapshot.Entry.ID, restoreSource); err != nil {
		return AppState{}, err
	}
	if err := recovery.ReplaceTree(restoreSource, current.Path); err != nil {
		return AppState{}, err
	}
	ws.Collections[ci] = cloneRecoveryCollection(snapshot.Collection)
	ws.UpdatedAt = snapshot.WorkspaceUpdatedAt
	a.state.OpenTabs = append([]OpenTab(nil), snapshot.OpenTabs...)
	a.state.ClosedTabs = append([]OpenTab(nil), snapshot.ClosedTabs...)
	a.state.ActiveTabID = snapshot.ActiveTabID
	a.seedCollectionWatchFingerprintLocked(snapshot.Collection.Path)
	if err := a.persistLocked(); err != nil {
		_ = recovery.ReplaceTree(rollbackPath, current.Path)
		ws.Collections[ci] = postCollection
		ws.UpdatedAt = postWorkspaceUpdatedAt
		a.state.OpenTabs = postOpenTabs
		a.state.ClosedTabs = postClosedTabs
		a.state.ActiveTabID = postActiveTabID
		a.seedCollectionWatchFingerprintLocked(postCollection.Path)
		_ = a.persistLocked()
		return AppState{}, err
	}
	_ = os.RemoveAll(rollbackPath)
	// The state and files are already durable. Leaving a stale entry is safe:
	// a second restore conflicts because the collection is present, so cleanup
	// failure must not make this successful restore look like a failure.
	_ = recovery.RemoveEntry(a.dataDir, snapshot.Entry.WorkspaceID, snapshot.Entry.ID)
	return a.state, nil
}

func (a *App) restoreCollectionRemovalLocked(snapshot recovery.Snapshot) (AppState, error) {
	ws, err := a.recoveryWorkspaceLocked(snapshot.Entry.WorkspaceID)
	if err != nil {
		return AppState{}, &recovery.RestoreConflictError{EntryID: snapshot.Entry.ID, Reason: "the workspace is no longer available"}
	}
	if _, _, err := a.findCollectionWithWorkspaceLocked(snapshot.Entry.CollectionID); err == nil {
		return AppState{}, &recovery.RestoreConflictError{EntryID: snapshot.Entry.ID, Reason: "a collection with this identity is already open"}
	}
	_, exists, content, err := recovery.GitIgnoreSnapshot(*ws)
	if err != nil {
		return AppState{}, err
	}
	if exists != snapshot.PostGitIgnoreExists || !bytes.Equal(content, snapshot.PostGitIgnoreContent) {
		return AppState{}, &recovery.RestoreConflictError{EntryID: snapshot.Entry.ID, Reason: "the managed Git ignore file changed after removal"}
	}
	if err := recovery.RestoreGitIgnore(*ws, snapshot.GitIgnoreExists, snapshot.GitIgnoreContent); err != nil {
		return AppState{}, err
	}
	postCollections := append([]Collection(nil), ws.Collections...)
	postWorkspaceUpdatedAt := ws.UpdatedAt
	postOpenTabs := append([]OpenTab(nil), a.state.OpenTabs...)
	postClosedTabs := append([]OpenTab(nil), a.state.ClosedTabs...)
	postActiveTabID := a.state.ActiveTabID
	index := snapshot.CollectionIndex
	if index < 0 || index > len(ws.Collections) {
		index = len(ws.Collections)
	}
	ws.Collections = append(ws.Collections, Collection{})
	copy(ws.Collections[index+1:], ws.Collections[index:])
	ws.Collections[index] = cloneRecoveryCollection(snapshot.Collection)
	ws.UpdatedAt = snapshot.WorkspaceUpdatedAt
	a.restoreRecoveryTabsLocked(snapshot)
	a.seedCollectionWatchFingerprintLocked(snapshot.Collection.Path)
	if err := a.persistLocked(); err != nil {
		_ = recovery.RestoreGitIgnore(*ws, snapshot.PostGitIgnoreExists, snapshot.PostGitIgnoreContent)
		ws.Collections = postCollections
		ws.UpdatedAt = postWorkspaceUpdatedAt
		a.state.OpenTabs = postOpenTabs
		a.state.ClosedTabs = postClosedTabs
		a.state.ActiveTabID = postActiveTabID
		a.clearCollectionWatchFingerprintLocked(snapshot.Collection.Path)
		_ = a.persistLocked()
		return AppState{}, err
	}
	_ = recovery.RemoveEntry(a.dataDir, snapshot.Entry.WorkspaceID, snapshot.Entry.ID)
	return a.state, nil
}

func (a *App) recoveryCollectionLocked(collectionID string) (int, int, *Workspace, *Collection, error) {
	for wi := range a.state.Workspaces {
		if a.state.Workspaces[wi].ID != a.state.ActiveWorkspaceID {
			continue
		}
		for ci := range a.state.Workspaces[wi].Collections {
			if a.state.Workspaces[wi].Collections[ci].ID == collectionID {
				return wi, ci, &a.state.Workspaces[wi], &a.state.Workspaces[wi].Collections[ci], nil
			}
		}
	}
	return 0, 0, nil, nil, fmt.Errorf("collection %s not found", collectionID)
}

func (a *App) recoveryWorkspaceLocked(workspaceID string) (*Workspace, error) {
	for wi := range a.state.Workspaces {
		if a.state.Workspaces[wi].ID == workspaceID {
			return &a.state.Workspaces[wi], nil
		}
	}
	return nil, fmt.Errorf("workspace %s not found", workspaceID)
}

func (a *App) stageCollectionRecoveryLocked(kind string, wi, ci int, ws *Workspace, collection *Collection, displayName string, affectedIDs []string) (recovery.Snapshot, error) {
	snapshot := recovery.Snapshot{
		Entry:              recovery.NewEntry(kind, displayName, ws.ID, collection.ID),
		WorkspaceIndex:     wi,
		CollectionIndex:    ci,
		Collection:         cloneRecoveryCollection(*collection),
		WorkspaceUpdatedAt: ws.UpdatedAt,
		OpenTabs:           append([]OpenTab(nil), a.state.OpenTabs...),
		ClosedTabs:         append([]OpenTab(nil), a.state.ClosedTabs...),
		ActiveTabID:        a.state.ActiveTabID,
		AffectedRequestIDs: append([]string(nil), affectedIDs...),
	}
	if err := recovery.StageSnapshot(a.dataDir, snapshot, collection.Path, true); err != nil {
		return recovery.Snapshot{}, err
	}
	return snapshot, nil
}

func (a *App) stageCollectionRecoveryMetadataLocked(wi, ci int, ws *Workspace, collection *Collection) (recovery.Snapshot, error) {
	_, exists, content, err := recovery.GitIgnoreSnapshot(*ws)
	if err != nil {
		return recovery.Snapshot{}, err
	}
	snapshot := recovery.Snapshot{
		Entry:              recovery.NewEntry(recovery.KindCollection, collection.Name, ws.ID, collection.ID),
		WorkspaceIndex:     wi,
		CollectionIndex:    ci,
		Collection:         cloneRecoveryCollection(*collection),
		WorkspaceUpdatedAt: ws.UpdatedAt,
		OpenTabs:           append([]OpenTab(nil), a.state.OpenTabs...),
		ClosedTabs:         append([]OpenTab(nil), a.state.ClosedTabs...),
		ActiveTabID:        a.state.ActiveTabID,
		GitIgnoreExists:    exists,
		GitIgnoreContent:   append([]byte(nil), content...),
	}
	if err := recovery.StageSnapshot(a.dataDir, snapshot, "", false); err != nil {
		return recovery.Snapshot{}, err
	}
	return snapshot, nil
}

func (a *App) commitCollectionRecoveryLocked(snapshot *recovery.Snapshot) error {
	_, collection, err := a.findCollectionWithWorkspaceLocked(snapshot.Entry.CollectionID)
	if err != nil {
		return err
	}
	fingerprint, err := recovery.CollectionFingerprint(collection.Path)
	if err != nil {
		return err
	}
	snapshot.PostCollection = cloneRecoveryCollection(*collection)
	snapshot.PostFingerprint = fingerprint
	snapshot.PostOpenTabs = append([]OpenTab(nil), a.state.OpenTabs...)
	snapshot.PostClosedTabs = append([]OpenTab(nil), a.state.ClosedTabs...)
	snapshot.PostActiveTabID = a.state.ActiveTabID
	return recovery.WriteSnapshot(a.dataDir, *snapshot)
}

func (a *App) rollbackCollectionRecoveryLocked(snapshot recovery.Snapshot) {
	if snapshot.Entry.Kind != recovery.KindCollection {
		rollbackSource, err := os.MkdirTemp("", "liteapi-recovery-rollback-")
		if err == nil {
			defer func() { _ = os.RemoveAll(rollbackSource) }()
		}
		if err == nil && recovery.RestorePayload(a.dataDir, snapshot.Entry.WorkspaceID, snapshot.Entry.ID, rollbackSource) == nil {
			_ = recovery.ReplaceTree(rollbackSource, snapshot.Collection.Path)
		}
	}
	if ws, err := a.recoveryWorkspaceLocked(snapshot.Entry.WorkspaceID); err == nil {
		ci := findRecoveryCollectionIndex(ws.Collections, snapshot.Entry.CollectionID)
		if ci >= 0 {
			ws.Collections[ci] = cloneRecoveryCollection(snapshot.Collection)
		}
		ws.UpdatedAt = snapshot.WorkspaceUpdatedAt
	}
	a.state.OpenTabs = append([]OpenTab(nil), snapshot.OpenTabs...)
	a.state.ClosedTabs = append([]OpenTab(nil), snapshot.ClosedTabs...)
	a.state.ActiveTabID = snapshot.ActiveTabID
	a.seedCollectionWatchFingerprintLocked(snapshot.Collection.Path)
	_ = a.persistLocked()
}

func (a *App) rollbackCollectionRemovalLocked(snapshot recovery.Snapshot) {
	if ws, err := a.recoveryWorkspaceLocked(snapshot.Entry.WorkspaceID); err == nil {
		_ = recovery.RestoreGitIgnore(*ws, snapshot.GitIgnoreExists, snapshot.GitIgnoreContent)
		if findRecoveryCollectionIndex(ws.Collections, snapshot.Entry.CollectionID) < 0 {
			index := snapshot.CollectionIndex
			if index < 0 || index > len(ws.Collections) {
				index = len(ws.Collections)
			}
			ws.Collections = append(ws.Collections, Collection{})
			copy(ws.Collections[index+1:], ws.Collections[index:])
			ws.Collections[index] = cloneRecoveryCollection(snapshot.Collection)
		}
		ws.UpdatedAt = snapshot.WorkspaceUpdatedAt
	}
	a.state.OpenTabs = append([]OpenTab(nil), snapshot.OpenTabs...)
	a.state.ClosedTabs = append([]OpenTab(nil), snapshot.ClosedTabs...)
	a.state.ActiveTabID = snapshot.ActiveTabID
	a.seedCollectionWatchFingerprintLocked(snapshot.Collection.Path)
	_ = a.persistLocked()
}

func recoveryParentPaths(collection *Collection, item RequestItem) (string, string) {
	parentPath := normalizeFolderPathKey(item.FolderPath)
	parentDisplayPath := parentPath
	if parentDisplayPath != "" {
		if folder, err := findFolderConfig(collection, parentDisplayPath); err == nil {
			parentPath = normalizeFolderPathKey(folder.Path)
			parentDisplayPath = normalizeFolderPathKey(firstNonEmpty(folder.DisplayPath, folder.Name, folder.Path))
		}
	}
	return parentPath, parentDisplayPath
}

func recoveryFolderRequestIDs(collection *Collection, oldPath, oldDisplayPath, targetDir string) map[string]bool {
	result := map[string]bool{}
	for _, item := range collection.Items {
		itemFolderPath := normalizeFolderPathKey(item.FolderPath)
		if folderPathHasPrefix(itemFolderPath, oldDisplayPath) || folderPathHasPrefix(itemFolderPath, oldPath) || (strings.TrimSpace(item.FilePath) != "" && pathInside(targetDir, item.FilePath)) {
			result[item.ID] = true
		}
	}
	return result
}

func (a *App) removeRecoveryTabsLocked(collectionID string, requestIDs map[string]bool, wholeCollection bool) {
	filter := func(tab OpenTab) bool {
		if tab.CollectionID != collectionID {
			return false
		}
		return wholeCollection || requestIDs[tab.ItemID]
	}
	open := a.state.OpenTabs[:0]
	for _, tab := range a.state.OpenTabs {
		if !filter(tab) {
			open = append(open, tab)
		}
	}
	a.state.OpenTabs = open
	closed := a.state.ClosedTabs[:0]
	for _, tab := range a.state.ClosedTabs {
		if !filter(tab) {
			closed = append(closed, tab)
		}
	}
	a.state.ClosedTabs = closed
	if !openTabIDExists(a.state.OpenTabs, a.state.ActiveTabID) {
		a.state.ActiveTabID = ""
		if len(a.state.OpenTabs) > 0 {
			a.state.ActiveTabID = a.state.OpenTabs[len(a.state.OpenTabs)-1].ID
		}
	}
}

func (a *App) restoreRecoveryTabsLocked(snapshot recovery.Snapshot) {
	ids := map[string]bool{}
	for _, id := range snapshot.AffectedRequestIDs {
		ids[id] = true
	}
	affected := func(tab OpenTab) bool {
		return tab.CollectionID == snapshot.Entry.CollectionID && (snapshot.Entry.Kind == recovery.KindCollection || ids[tab.ItemID])
	}
	for _, tab := range snapshot.OpenTabs {
		if affected(tab) && !openTabIDExists(a.state.OpenTabs, tab.ID) {
			a.state.OpenTabs = append(a.state.OpenTabs, tab)
		}
	}
	for _, tab := range snapshot.ClosedTabs {
		if affected(tab) && !openTabIDExists(a.state.ClosedTabs, tab.ID) {
			a.state.ClosedTabs = append(a.state.ClosedTabs, tab)
		}
	}
	if affected(OpenTab{CollectionID: snapshot.Entry.CollectionID, ItemID: activeTabItemID(snapshot.OpenTabs, snapshot.ActiveTabID)}) && openTabIDExists(a.state.OpenTabs, snapshot.ActiveTabID) {
		a.state.ActiveTabID = snapshot.ActiveTabID
	}
}

func activeTabItemID(tabs []OpenTab, id string) string {
	for _, tab := range tabs {
		if tab.ID == id {
			return tab.ItemID
		}
	}
	return ""
}

func openTabIDExists(tabs []OpenTab, id string) bool {
	for _, tab := range tabs {
		if tab.ID == id {
			return true
		}
	}
	return false
}

func recoveryTabsEqual(left, right []OpenTab) bool {
	if len(left) == 0 && len(right) == 0 {
		return true
	}
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

// Collection loading normalizes nil and empty nested slices differently across
// formats. Compare the durable identity/order/draft semantics separately from
// the filesystem fingerprint, which covers every saved byte.
func recoveryCollectionSemanticEqual(left, right Collection) bool {
	if left.ID != right.ID || left.Name != right.Name || left.Version != right.Version || filepath.Clean(left.Path) != filepath.Clean(right.Path) || left.Format != right.Format || left.Remote != right.Remote || left.NotFoundLocally != right.NotFoundLocally || left.Scratch != right.Scratch || len(left.Items) != len(right.Items) || len(left.Folders) != len(right.Folders) {
		return false
	}
	for i := range left.Items {
		if left.Items[i].ID != right.Items[i].ID || left.Items[i].Draft != right.Items[i].Draft || left.Items[i].Seq != right.Items[i].Seq || left.Items[i].FilePath != right.Items[i].FilePath {
			return false
		}
	}
	for i := range left.Folders {
		if left.Folders[i].Path != right.Folders[i].Path || left.Folders[i].DisplayPath != right.Folders[i].DisplayPath || left.Folders[i].Seq != right.Folders[i].Seq {
			return false
		}
	}
	return true
}

func findRecoveryCollectionIndex(collections []Collection, id string) int {
	for i := range collections {
		if collections[i].ID == id {
			return i
		}
	}
	return -1
}

func cloneRecoveryCollection(value Collection) Collection {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	var clone Collection
	if err := json.Unmarshal(data, &clone); err != nil {
		panic(err)
	}
	return clone
}
