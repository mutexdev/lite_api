package core

// US-024 — ID→index lookups for the collection runner.
//
// # The problem
//
// A collection run resolves the same collection and the same request by ID five
// times per request: findCollectionWithWorkspaceLocked + findItem on entry to
// sendRequestWithControlsContextProvenance, findCollectionLocked + findItem on
// its tail,
// and findItemInState back in the runner loop. Every one of those is a linear
// scan, so a run of N requests over C collections costs O(N * (C + N/C)) in
// lookups alone — quadratic in the number of requests.
//
// # Why this index is scoped and not a field on App
//
// findItem returns &collection.Items[index] and findCollectionWithWorkspaceLocked
// returns pointers into a.state.Workspaces[wi].Collections. A wrong index does
// not fail loudly: it silently hands back a different request, which the caller
// then mutates. Any long-lived index on App would have to be invalidated by
// every site in app.go, collection_import.go, collection_recovery.go,
// draft_guard.go, workspace_service.go and git_workbench.go that appends,
// deletes, reorders or replaces Items/Collections/Workspaces — including sites
// added later, by people who have never heard of this file. There is also no
// AppState.Revision to hang invalidation off: US-008 is not implemented.
//
// A lazily populated field on App has a second, immediate problem. US-020 made
// a.mu an RWMutex and reserved RLock for call sites "whose *entire* dynamic call
// tree is provably read-only"; app.go:9677 and app.go:9707 call
// findCollectionLocked under RLock and say so in a comment. Making that helper
// write to a shared cache would turn those two into data races.
//
// So this index is created by RunCollectionWithOptions, used for that one run,
// and dropped. Nothing else in the codebase has to know it exists, and no
// mutation site anywhere acquires a new obligation.
//
// # Why it also cannot go stale
//
// Scoping alone is not enough here, because a collection run releases a.mu
// around each request's network I/O; another Wails call can add or delete a
// request in that window. So every hit is verified against the live slice
// before it is returned — an entry is used only when the element it points at
// still carries the requested ID. A hint invalidated mid-run therefore costs a
// rescan, never a wrong answer, and these accessors are observationally
// identical to the linear scans they replace. On a miss the index rebuilds from
// the live state, so a mutation costs one extra O(n) pass and then lookups are
// O(1) again.
//
// Confining the change to the runner leaves the other 42 findCollectionLocked
// and 24 findItem call sites on the untouched linear scans. That is deliberate:
// making those O(1) needs an index that outlives a lock release, which is the
// unsafe design above.

// runnerLookupIndex is a lookup hint for one collection run. It is owned by the
// single goroutine driving that run, so its maps need no lock of their own; the
// live state it verifies against is read with a.mu held, as before.
type runnerLookupIndex struct {
	// collections maps a collection ID to its position in state.Workspaces.
	collections map[string]collectionPosition
	// items maps a collection ID to that collection's itemID→index map, built
	// on first use so a run never pays to index collections it will not touch.
	items map[string]map[string]int
}

type collectionPosition struct {
	workspace  int
	collection int
}

// lookupCollectionPosition is the single linear scan over the workspace tree.
// findCollectionWithWorkspaceLocked and the index's miss path share it so there
// is exactly one definition of "first collection with this ID wins".
func lookupCollectionPosition(state *AppState, id string) (collectionPosition, bool) {
	for wi := range state.Workspaces {
		for ci := range state.Workspaces[wi].Collections {
			if state.Workspaces[wi].Collections[ci].ID == id {
				return collectionPosition{workspace: wi, collection: ci}, true
			}
		}
	}
	return collectionPosition{}, false
}

// newRunnerLookupIndex indexes the workspace tree. Item maps are left empty and
// filled on demand. Callers must hold a.mu.
func newRunnerLookupIndex(state *AppState) *runnerLookupIndex {
	index := &runnerLookupIndex{
		collections: map[string]collectionPosition{},
		items:       map[string]map[string]int{},
	}
	for wi := range state.Workspaces {
		for ci := range state.Workspaces[wi].Collections {
			id := state.Workspaces[wi].Collections[ci].ID
			// First match wins, matching lookupCollectionPosition.
			if _, seen := index.collections[id]; !seen {
				index.collections[id] = collectionPosition{workspace: wi, collection: ci}
			}
		}
	}
	return index
}

// findCollectionWithWorkspaceIndexedLocked is findCollectionWithWorkspaceLocked
// with an O(1) fast path. A nil index falls through to the plain scan, so the
// non-runner callers of the send path keep their existing behaviour exactly.
// Callers must hold a.mu.
func (a *App) findCollectionWithWorkspaceIndexedLocked(index *runnerLookupIndex, id string) (*Workspace, *Collection, error) {
	if index == nil {
		return a.findCollectionWithWorkspaceLocked(id)
	}
	if position, ok := index.collections[id]; ok {
		if workspace, collection, ok := verifyCollectionPosition(&a.state, position, id); ok {
			return workspace, collection, nil
		}
	}
	// Miss: the tree changed under the run. Rescan, repair, and drop the item
	// map, whose indices refer to the collection this entry used to name.
	position, ok := lookupCollectionPosition(&a.state, id)
	if !ok {
		delete(index.collections, id)
		delete(index.items, id)
		return a.findCollectionWithWorkspaceLocked(id)
	}
	index.collections[id] = position
	delete(index.items, id)
	workspace := &a.state.Workspaces[position.workspace]
	return workspace, &workspace.Collections[position.collection], nil
}

// verifyCollectionPosition returns live pointers only when position still names
// a collection carrying id. This is the check that makes a stale hint harmless.
func verifyCollectionPosition(state *AppState, position collectionPosition, id string) (*Workspace, *Collection, bool) {
	if position.workspace < 0 || position.workspace >= len(state.Workspaces) {
		return nil, nil, false
	}
	workspace := &state.Workspaces[position.workspace]
	if position.collection < 0 || position.collection >= len(workspace.Collections) {
		return nil, nil, false
	}
	collection := &workspace.Collections[position.collection]
	if collection.ID != id {
		return nil, nil, false
	}
	return workspace, collection, true
}

// findItemIndexed is findItem with an O(1) fast path. collectionID keys the
// hint; collection is the live collection the pointer must be taken from. A nil
// index falls through to the plain scan.
//
// The returned pointer is always &collection.Items[i] for an i satisfying
// collection.Items[i].ID == itemID, which is exactly findItem's contract.
func (index *runnerLookupIndex) findItemIndexed(collectionID string, collection *Collection, itemID string) (*RequestItem, error) {
	if index == nil {
		return findItem(collection, itemID)
	}
	if byID, ok := index.items[collectionID]; ok {
		if i, ok := byID[itemID]; ok && i >= 0 && i < len(collection.Items) && collection.Items[i].ID == itemID {
			return &collection.Items[i], nil
		}
		// Any miss means the slice moved under us; the whole map is suspect.
		delete(index.items, collectionID)
	}
	item, err := findItem(collection, itemID)
	if err != nil {
		return nil, err
	}
	index.items[collectionID] = buildItemIndex(collection)
	return item, nil
}

// buildItemIndex maps itemID→index, first occurrence winning so it agrees with
// findItemIndex when a collection somehow holds duplicate IDs.
func buildItemIndex(collection *Collection) map[string]int {
	byID := make(map[string]int, len(collection.Items))
	for i := range collection.Items {
		if _, seen := byID[collection.Items[i].ID]; !seen {
			byID[collection.Items[i].ID] = i
		}
	}
	return byID
}

// runnerItemNameIndex maps request name→index over the runner's private,
// already-filtered copy of the item list. That slice is built before the run
// loop starts and is never written to, so unlike the index above this one has
// no way to go stale at all and needs no verification. It replaces the linear
// rescan the bru.setNextRequest jump path did per jump (up to 10,000 jumps).
//
// First occurrence wins, matching the scan it replaces.
func runnerItemNameIndex(items []RequestItem) map[string]int {
	byName := make(map[string]int, len(items))
	for index := range items {
		if _, seen := byName[items[index].Name]; !seen {
			byName[items[index].Name] = index
		}
	}
	return byName
}
