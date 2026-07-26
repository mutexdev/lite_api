package main

import (
	"fmt"
	"strings"
)

func (a *App) SetActiveTab(tabID string) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	found, err := a.setActiveTabLocked(tabID)
	if !found {
		return AppState{}, err
	}
	return a.state, err
}

// setActiveTabLocked is shared with SetActiveTabNarrow (US-014). found=false
// means no such tab and err says so; found=true with a non-nil err is a parked
// background-write failure, not a failure to switch tabs.
func (a *App) setActiveTabLocked(tabID string) (bool, error) {
	for _, tab := range a.state.OpenTabs {
		if tab.ID == tabID {
			a.state.ActiveTabID = tabID
			return true, a.markDirty(persistScopeState)
		}
	}
	return false, fmt.Errorf("tab %s not found", tabID)
}

func (a *App) UpdateOpenTabPanes(tabID, requestPaneTab, responseTab string) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	found, err := a.updateOpenTabPanesLocked(tabID, requestPaneTab, responseTab)
	if !found {
		return AppState{}, err
	}
	return a.state, err
}

// updateOpenTabPanesLocked is shared with UpdateOpenTabPanesNarrow (US-014).
// A rejected pane name reports found=false, because nothing was mutated.
func (a *App) updateOpenTabPanesLocked(tabID, requestPaneTab, responseTab string) (bool, error) {
	for i := range a.state.OpenTabs {
		if a.state.OpenTabs[i].ID != tabID {
			continue
		}
		if requestPaneTab != "" && !validRequestPaneTab(requestPaneTab) {
			return false, fmt.Errorf("invalid request pane tab %q", requestPaneTab)
		}
		if responseTab != "" && !validResponsePaneTab(responseTab) {
			return false, fmt.Errorf("invalid response pane tab %q", responseTab)
		}
		if requestPaneTab != "" {
			a.state.OpenTabs[i].RequestPaneTab = requestPaneTab
		}
		if responseTab != "" {
			a.state.OpenTabs[i].ResponseTab = responseTab
		}
		return true, a.markDirty(persistScopeState)
	}
	return false, fmt.Errorf("tab %s not found", tabID)
}

func (a *App) OpenRequestTab(collectionID, itemID string) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return AppState{}, err
	}
	collection, err := a.findCollectionLocked(collectionID)
	if err != nil {
		return AppState{}, err
	}
	if _, err := findItem(collection, itemID); err != nil {
		return AppState{}, err
	}
	a.openTabLocked(collectionID, itemID, "request")
	return a.state, a.markDirty(persistScopeState)
}

func (a *App) OpenResponseExampleTab(collectionID, itemID, exampleID string) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return AppState{}, err
	}
	collection, err := a.findCollectionLocked(collectionID)
	if err != nil {
		return AppState{}, err
	}
	item, err := findItem(collection, itemID)
	if err != nil {
		return AppState{}, err
	}
	example, _, err := findResponseExample(item, exampleID)
	if err != nil {
		return AppState{}, err
	}
	a.openResponseExampleTabLocked(collectionID, itemID, *example)
	return a.state, a.markDirty(persistScopeState)
}

func (a *App) CloseTab(tabID string) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	next := a.state.OpenTabs[:0]
	found := false
	var closed OpenTab
	for _, tab := range a.state.OpenTabs {
		if tab.ID != tabID {
			next = append(next, tab)
		} else {
			found = true
			closed = tab
		}
	}
	if !found {
		return AppState{}, fmt.Errorf("tab %s not found", tabID)
	}
	a.rememberClosedTabLocked(closed)
	a.state.OpenTabs = next
	if a.state.ActiveTabID == tabID {
		a.state.ActiveTabID = ""
		if len(a.state.OpenTabs) > 0 {
			a.state.ActiveTabID = a.state.OpenTabs[len(a.state.OpenTabs)-1].ID
		}
	}
	return a.state, a.markDirty(persistScopeState)
}

func (a *App) CloseAllTabs() (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, tab := range a.state.OpenTabs {
		a.rememberClosedTabLocked(tab)
	}
	a.state.OpenTabs = []OpenTab{}
	a.state.ActiveTabID = ""
	return a.state, a.markDirty(persistScopeState)
}

func (a *App) ReopenLastClosedTab(collectionID string) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	collectionID = strings.TrimSpace(collectionID)
	for {
		index := a.lastClosedTabIndexLocked(collectionID)
		if index < 0 {
			break
		}
		tab := a.state.ClosedTabs[index]
		a.state.ClosedTabs = append(a.state.ClosedTabs[:index], a.state.ClosedTabs[index+1:]...)
		if !a.openTabIsRestorableLocked(tab) {
			continue
		}
		for i := range a.state.OpenTabs {
			if a.state.OpenTabs[i].ID == tab.ID {
				a.state.ActiveTabID = tab.ID
				return a.state, a.markDirty(persistScopeState)
			}
		}
		if tab.RequestPaneTab == "" {
			tab.RequestPaneTab = "params"
		}
		if tab.ResponseTab == "" {
			tab.ResponseTab = "response"
		}
		a.state.OpenTabs = append(a.state.OpenTabs, tab)
		a.state.ActiveTabID = tab.ID
		return a.state, a.markDirty(persistScopeState)
	}
	return a.state, a.markDirty(persistScopeState)
}

func (a *App) MoveOpenTab(tabID string, offset int) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	found, err := a.moveOpenTabLocked(tabID, offset)
	if !found {
		return AppState{}, err
	}
	return a.state, err
}

// moveOpenTabLocked is shared with MoveOpenTabNarrow (US-014).
//
// A no-op move — offset 0, or a move that clamps back to where the tab already
// is — reports found=true with no error and does NOT mark state dirty. That
// matches the original behaviour and is worth preserving explicitly: marking
// dirty here would bump the revision for a mutation that changed nothing, and
// the narrow callers use revision continuity to decide whether they are in
// sync.
func (a *App) moveOpenTabLocked(tabID string, offset int) (bool, error) {
	index := -1
	for i, tab := range a.state.OpenTabs {
		if tab.ID == tabID {
			index = i
			break
		}
	}
	if index < 0 {
		return false, fmt.Errorf("tab %s not found", tabID)
	}
	if offset == 0 {
		return true, nil
	}
	target := clampInt(index+offset, 0, len(a.state.OpenTabs)-1)
	if target == index {
		return true, nil
	}
	tab := a.state.OpenTabs[index]
	if target < index {
		copy(a.state.OpenTabs[target+1:index+1], a.state.OpenTabs[target:index])
	} else {
		copy(a.state.OpenTabs[index:target], a.state.OpenTabs[index+1:target+1])
	}
	a.state.OpenTabs[target] = tab
	return true, a.markDirty(persistScopeState)
}
