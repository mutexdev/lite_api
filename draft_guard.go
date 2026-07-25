package main

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

// UnsavedDraft is a Wails-safe reference used by close/quit UI. A draft is
// persisted in LiteAPI state, but has not necessarily been written back to its
// collection file yet.
type UnsavedDraft struct {
	CollectionID string `json:"collectionId"`
	ItemID       string `json:"itemId"`
	Name         string `json:"name"`
	Transient    bool   `json:"transient"`
	Scratch      bool   `json:"scratch"`
}

func (a *App) ListUnsavedDrafts() ([]UnsavedDraft, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return nil, err
	}
	var drafts []UnsavedDraft
	for _, ws := range a.state.Workspaces {
		for _, collection := range ws.Collections {
			for _, item := range collection.Items {
				if item.Draft {
					drafts = append(drafts, UnsavedDraft{CollectionID: collection.ID, ItemID: item.ID, Name: item.Name, Transient: item.Transient, Scratch: collection.Scratch})
				}
			}
		}
	}
	return drafts, nil
}

// DiscardRequestDraft restores a normal request from its saved collection file.
// A never-saved transient scratch request is removed instead.
func (a *App) DiscardRequestDraft(collectionID, itemID string) (AppState, error) {
	return a.DiscardUnsavedDrafts([]UnsavedDraft{{CollectionID: collectionID, ItemID: itemID}})
}

// SaveUnsavedDrafts validates all references before saving. It is intentionally
// a sequential convenience API: callers needing an all-or-nothing close flow
// must save one draft at a time and surface any failure before discarding.
func (a *App) SaveUnsavedDrafts(drafts []UnsavedDraft) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return AppState{}, err
	}
	if _, err := a.validateUnsavedDraftsLocked(drafts); err != nil {
		return AppState{}, err
	}
	now := time.Now()
	for _, draft := range uniqueUnsavedDrafts(drafts) {
		collection, _ := a.findCollectionLocked(draft.CollectionID)
		item, _ := findItem(collection, draft.ItemID)
		original := *item
		item.Draft = false
		if collection.Scratch {
			item.Transient = true
		} else {
			item.Transient = false
		}
		item.UpdatedAt = now
		if err := a.writeCollectionFilesLocked(collection); err != nil {
			*item = original
			return AppState{}, err
		}
		if collection.Scratch {
			if err := a.writeScratchCollectionMetadataLocked(collection); err != nil {
				*item = original
				return AppState{}, err
			}
		}
		if err := a.persistLocked(); err != nil {
			*item = original
			return AppState{}, err
		}
	}
	return a.state, nil
}

// DiscardUnsavedDrafts validates all selections before mutation. It is a
// sequential convenience API; saved requests are reloaded from disk, while a
// never-saved transient request is removed when it has no saved file.
func (a *App) DiscardUnsavedDrafts(drafts []UnsavedDraft) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return AppState{}, err
	}
	if _, err := a.validateUnsavedDraftsLocked(drafts); err != nil {
		return AppState{}, err
	}
	for _, draft := range uniqueUnsavedDrafts(drafts) {
		ws, collection, err := a.findCollectionWithWorkspaceLocked(draft.CollectionID)
		if err != nil {
			return AppState{}, err
		}
		index, err := findItemIndex(collection, draft.ItemID)
		if err != nil {
			return AppState{}, err
		}
		item := collection.Items[index]
		beforeCollection := cloneRecoveryCollection(*collection)
		beforeOpenTabs := append([]OpenTab(nil), a.state.OpenTabs...)
		beforeClosedTabs := append([]OpenTab(nil), a.state.ClosedTabs...)
		beforeActiveTabID := a.state.ActiveTabID
		beforeWorkspaceUpdatedAt := ws.UpdatedAt
		rollback := func() {
			*collection = beforeCollection
			a.state.OpenTabs = beforeOpenTabs
			a.state.ClosedTabs = beforeClosedTabs
			a.state.ActiveTabID = beforeActiveTabID
			ws.UpdatedAt = beforeWorkspaceUpdatedAt
		}
		itemPath, pathErr := collectionRequestFilesystemPath(collection, item)
		if item.Transient && (pathErr != nil || !fileExists(itemPath)) {
			collection.Items = append(collection.Items[:index], collection.Items[index+1:]...)
			a.removeRecoveryTabsLocked(collection.ID, map[string]bool{item.ID: true}, false)
			ws.UpdatedAt = time.Now()
			if err := a.persistLocked(); err != nil {
				rollback()
				return AppState{}, err
			}
			continue
		}
		fromDisk, err := readCollectionFromDisk(collection.Path)
		if err != nil {
			return AppState{}, fmt.Errorf("reload saved request %s: %w", item.Name, err)
		}
		var saved *RequestItem
		for i := range fromDisk.Items {
			if filepath.Clean(fromDisk.Items[i].FilePath) == filepath.Clean(item.FilePath) || fromDisk.Items[i].ID == item.ID {
				saved = &fromDisk.Items[i]
				break
			}
		}
		if saved == nil {
			return AppState{}, errors.New("saved request file no longer exists")
		}
		restored := *saved
		// File formats do not persist LiteAPI's runtime identity. Keep the live
		// identity and response context so existing tabs continue to target the
		// request while only its editable request content is reverted.
		restored.ID = item.ID
		restored.CreatedAt = item.CreatedAt
		restored.UpdatedAt = item.UpdatedAt
		restored.Transient = item.Transient
		restored.Response = item.Response
		restored.Timeline = item.Timeline
		restored.Draft = false
		collection.Items[index] = restored
		ws.UpdatedAt = time.Now()
		if err := a.persistLocked(); err != nil {
			rollback()
			return AppState{}, err
		}
	}
	return a.state, nil
}

func (a *App) validateUnsavedDraftsLocked(drafts []UnsavedDraft) ([]*Collection, error) {
	if len(drafts) == 0 {
		return nil, nil
	}
	seen := map[string]bool{}
	collections := map[string]*Collection{}
	order := []string{}
	for _, draft := range uniqueUnsavedDrafts(drafts) {
		if strings.TrimSpace(draft.CollectionID) == "" || strings.TrimSpace(draft.ItemID) == "" {
			return nil, errors.New("draft collection and request are required")
		}
		collection, err := a.findCollectionLocked(draft.CollectionID)
		if err != nil {
			return nil, err
		}
		item, err := findItem(collection, draft.ItemID)
		if err != nil {
			return nil, err
		}
		if !item.Draft {
			return nil, fmt.Errorf("request %s has no unsaved draft", item.Name)
		}
		key := draft.CollectionID + "\x00" + draft.ItemID
		if !seen[key] {
			seen[key] = true
			if collections[collection.ID] == nil {
				collections[collection.ID] = collection
				order = append(order, collection.ID)
			}
		}
	}
	result := make([]*Collection, 0, len(order))
	for _, id := range order {
		result = append(result, collections[id])
	}
	return result, nil
}

func uniqueUnsavedDrafts(drafts []UnsavedDraft) []UnsavedDraft {
	seen := map[string]bool{}
	result := make([]UnsavedDraft, 0, len(drafts))
	for _, draft := range drafts {
		key := draft.CollectionID + "\x00" + draft.ItemID
		if !seen[key] {
			seen[key] = true
			result = append(result, draft)
		}
	}
	return result
}
