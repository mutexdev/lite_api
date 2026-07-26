package main

// US-014 — narrow-return mutators.
//
// THE PROBLEM. Every mutator returns the whole AppState, and the frontend
// replaces its copy with it. On a 500-request workspace that is megabytes of
// JSON marshalled in Go, shipped across the Wails bridge, parsed in JavaScript
// and diffed by Svelte — for a single typed character, whose actual effect is
// one field on one RequestItem.
//
// THE FIX. Each hot mutator gains a variant returning only what changed, paired
// with the AppState.Revision that US-008 introduced. The frontend applies the
// small result to its local copy and only refetches the whole state when the
// revision tells it that it missed something.
//
// WHY THE REVISION IS LOAD-BEARING, and not decoration. Applying a narrow
// result locally is only sound if the local copy was correct to begin with.
// Anything that mutates state WITHOUT going through a narrow variant — another
// window, the collection watcher, an unmigrated binding, the readiness
// normalisation in ensureReadyLocked — leaves the frontend's copy stale, and a
// narrow result patched onto a stale copy produces a view that is wrong in a way
// nothing will ever correct. The revision makes that detectable: mutations
// increment it by exactly one, so a result whose revision is not
// (last applied + 1) proves an update was missed, and the frontend answers by
// refetching in full. Wrong-but-detected costs one extra round trip;
// wrong-and-undetected costs the user their trust in what they are looking at.
//
// COMPATIBILITY. The existing full-AppState methods are untouched and still
// work. This is deliberate: the narrow variants are an addition, and a caller
// that has not been migrated keeps behaving exactly as before.
//
// NAMING. The "Narrow" suffix is jargon, but it is honest jargon and it greps
// cleanly. The alternative — naming each variant after its return shape —
// produced names like UpdateOpenTabPanesTabs, which is worse.

// RequestMutation is the result of a request mutator: the revision the mutation
// produced, and the single item it changed.
//
// CollectionID is included because the frontend needs it to find the item, and
// an id alone would force it to search every collection — the O(W×C) scan
// US-024 removed from the Go side.
type RequestMutation struct {
	Revision     int64       `json:"revision"`
	CollectionID string      `json:"collectionId"`
	Item         RequestItem `json:"item"`
}

// TabsMutation is the result of a tab mutator.
//
// It carries the whole OpenTabs slice rather than a delta, and that is a
// deliberate limit on how clever this gets: tab operations are reorderings and
// selections whose deltas are fiddly to express and easy to get subtly wrong,
// while the slice itself is small and bounded by how many tabs a person opens.
// The payload that mattered was never the tab list — it was the workspaces,
// collections, cached response bodies and network log travelling beside it.
type TabsMutation struct {
	Revision    int64     `json:"revision"`
	OpenTabs    []OpenTab `json:"openTabs"`
	ActiveTabID string    `json:"activeTabId"`
}

// tabsMutationLocked snapshots the tab state for a narrow return. a.mu held.
func (a *App) tabsMutationLocked() TabsMutation {
	tabs := make([]OpenTab, len(a.state.OpenTabs))
	copy(tabs, a.state.OpenTabs)
	return TabsMutation{
		Revision:    a.revision,
		OpenTabs:    tabs,
		ActiveTabID: a.state.ActiveTabID,
	}
}

// UpdateRequestNarrow is UpdateRequest returning only the changed item.
//
// This is the keystroke path — UpdateRequest fires on every character typed
// into a URL, header or body field (improvement_v2.md §2.1.B) — so it is the
// one that most needed to stop shipping the workspace.
func (a *App) UpdateRequestNarrow(collectionID, itemID string, patch RequestPatch) (RequestMutation, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	item, err := a.updateRequestLocked(collectionID, itemID, patch)
	if item == nil {
		return RequestMutation{}, err
	}
	// err here is markDirty's parked background-write failure, not a failure of
	// this mutation: the change IS applied. Returning it alongside the result
	// matches what UpdateRequest has always done, so the error still reaches
	// the user exactly once.
	return RequestMutation{Revision: a.revision, CollectionID: collectionID, Item: *item}, err
}

// SetActiveTabNarrow is SetActiveTab returning only the tab state.
func (a *App) SetActiveTabNarrow(tabID string) (TabsMutation, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	found, err := a.setActiveTabLocked(tabID)
	if !found {
		return TabsMutation{}, err
	}
	return a.tabsMutationLocked(), err
}

// UpdateOpenTabPanesNarrow is UpdateOpenTabPanes returning only the tab state.
func (a *App) UpdateOpenTabPanesNarrow(tabID, requestPaneTab, responseTab string) (TabsMutation, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	found, err := a.updateOpenTabPanesLocked(tabID, requestPaneTab, responseTab)
	if !found {
		return TabsMutation{}, err
	}
	return a.tabsMutationLocked(), err
}

// MoveOpenTabNarrow is MoveOpenTab returning only the tab state.
func (a *App) MoveOpenTabNarrow(tabID string, offset int) (TabsMutation, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	found, err := a.moveOpenTabLocked(tabID, offset)
	if !found {
		return TabsMutation{}, err
	}
	return a.tabsMutationLocked(), err
}
