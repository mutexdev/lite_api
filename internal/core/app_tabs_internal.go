package core

// The unexported tab bookkeeping the bound methods in app_tabs.go call.
//
// Split out of app.go by AST: declarations are identified by the parser and
// copied verbatim from their source offsets.

import (
	"fmt"
	"strings"
	"time"

	"github.com/mutexdev/lite_api/internal/cookiejar"
)

const closedTabHistoryLimit = 50

func (a *App) SaveAllTabs(collectionID string) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return AppState{}, err
	}
	collectionID = strings.TrimSpace(collectionID)
	now := time.Now()
	seenItems := map[string]bool{}
	collections := map[string]*Collection{}
	collectionOrder := []string{}
	saved := 0
	for _, tab := range a.state.OpenTabs {
		if tab.CollectionID == "" || tab.ItemID == "" {
			continue
		}
		if collectionID != "" && tab.CollectionID != collectionID {
			continue
		}
		key := tab.CollectionID + "\x00" + tab.ItemID
		if seenItems[key] {
			continue
		}
		collection, err := a.findCollectionLocked(tab.CollectionID)
		if err != nil {
			return AppState{}, err
		}
		item, err := findItem(collection, tab.ItemID)
		if err != nil {
			return AppState{}, err
		}
		item.Draft = false
		if collection.Scratch {
			item.Transient = true
			if strings.TrimSpace(item.FilePath) == "" || !pathInside(collection.Path, item.FilePath) {
				item.FilePath = requestFilePath(*collection, *item, requestFileExtensionForCollection(*collection))
			}
		} else {
			item.Transient = false
		}
		item.UpdatedAt = now
		seenItems[key] = true
		saved++
		if _, ok := collections[collection.ID]; !ok {
			collections[collection.ID] = collection
			collectionOrder = append(collectionOrder, collection.ID)
		}
	}
	for _, collectionID := range collectionOrder {
		collection := collections[collectionID]
		if err := a.writeCollectionFilesLocked(collection); err != nil {
			return AppState{}, err
		}
		if collection.Scratch {
			if err := a.writeScratchCollectionMetadataLocked(collection); err != nil {
				return AppState{}, err
			}
		}
	}
	if saved > 0 {
		a.notify("success", fmt.Sprintf("Saved %d tab%s", saved, cookiejar.PluralSuffix(saved)))
	}
	return a.state, a.markDirty(persistScopeState)
}

func (a *App) removeOpenTabsForCollectionLocked(collectionID string) {
	next := a.state.OpenTabs[:0]
	for _, tab := range a.state.OpenTabs {
		if tab.CollectionID != collectionID {
			next = append(next, tab)
		}
	}
	a.state.OpenTabs = next
	a.removeClosedTabsForCollectionLocked(collectionID)
	if len(next) == 0 {
		a.state.ActiveTabID = ""
		return
	}
	found := false
	for _, tab := range next {
		if tab.ID == a.state.ActiveTabID {
			found = true
			break
		}
	}
	if !found {
		a.state.ActiveTabID = next[0].ID
	}
}

func (a *App) removeOpenTabsForRequestIDsLocked(collectionID string, requestIDs map[string]bool) {
	if len(requestIDs) == 0 {
		return
	}
	removedActive := false
	next := a.state.OpenTabs[:0]
	for _, tab := range a.state.OpenTabs {
		if tab.CollectionID == collectionID && requestIDs[tab.ItemID] && tab.Kind != "response-example" {
			if tab.ID == a.state.ActiveTabID {
				removedActive = true
			}
			continue
		}
		next = append(next, tab)
	}
	a.state.OpenTabs = next
	a.removeClosedTabsForRequestIDsLocked(collectionID, requestIDs)
	if !removedActive {
		return
	}
	a.state.ActiveTabID = ""
	if len(a.state.OpenTabs) > 0 {
		a.state.ActiveTabID = a.state.OpenTabs[len(a.state.OpenTabs)-1].ID
	}
}

func (a *App) removeOpenTabsForDeletedRequestIDsLocked(collectionID string, requestIDs map[string]bool) {
	if len(requestIDs) == 0 {
		return
	}
	removedActive := false
	next := a.state.OpenTabs[:0]
	for _, tab := range a.state.OpenTabs {
		if tab.CollectionID == collectionID && requestIDs[tab.ItemID] {
			if tab.ID == a.state.ActiveTabID {
				removedActive = true
			}
			continue
		}
		next = append(next, tab)
	}
	a.state.OpenTabs = next
	nextClosed := a.state.ClosedTabs[:0]
	for _, tab := range a.state.ClosedTabs {
		if tab.CollectionID == collectionID && requestIDs[tab.ItemID] {
			continue
		}
		nextClosed = append(nextClosed, tab)
	}
	a.state.ClosedTabs = nextClosed
	if !removedActive {
		return
	}
	a.state.ActiveTabID = ""
	if len(a.state.OpenTabs) > 0 {
		a.state.ActiveTabID = a.state.OpenTabs[len(a.state.OpenTabs)-1].ID
	}
}

func (a *App) removeClosedTabsForCollectionLocked(collectionID string) {
	next := a.state.ClosedTabs[:0]
	for _, tab := range a.state.ClosedTabs {
		if tab.CollectionID != collectionID {
			next = append(next, tab)
		}
	}
	a.state.ClosedTabs = next
}

func (a *App) removeClosedTabsForRequestIDsLocked(collectionID string, requestIDs map[string]bool) {
	next := a.state.ClosedTabs[:0]
	for _, tab := range a.state.ClosedTabs {
		if tab.CollectionID == collectionID && requestIDs[tab.ItemID] && tab.Kind != "response-example" {
			continue
		}
		next = append(next, tab)
	}
	a.state.ClosedTabs = next
}

func validRequestPaneTab(value string) bool {
	switch value {
	case "params", "body", "headers", "auth", "vars", "script", "assert", "tests", "docs", "app", "settings":
		return true
	default:
		return false
	}
}

func validResponsePaneTab(value string) bool {
	switch value {
	case "response", "headers", "metadata", "trailers", "timeline", "console", "tests", "examples":
		return true
	default:
		return false
	}
}

func (a *App) openTabLocked(collectionID, itemID, kind string) {
	tabID := collectionID + ":" + itemID
	transient := a.isTransientRequestLocked(collectionID, itemID)
	for i := range a.state.OpenTabs {
		if a.state.OpenTabs[i].ID == tabID {
			a.state.OpenTabs[i].Transient = transient
			a.state.ActiveTabID = tabID
			return
		}
	}
	a.state.OpenTabs = append(a.state.OpenTabs, OpenTab{
		ID:             tabID,
		CollectionID:   collectionID,
		ItemID:         itemID,
		Kind:           kind,
		RequestPaneTab: "params",
		ResponseTab:    "response",
		Transient:      transient,
	})
	a.state.ActiveTabID = tabID
}

func responseExampleTabID(collectionID, itemID, exampleID string) string {
	return collectionID + ":" + itemID + ":example:" + exampleID
}

func (a *App) openResponseExampleTabLocked(collectionID, itemID string, example ResponseExample) {
	tabID := responseExampleTabID(collectionID, itemID, example.ID)
	for i := range a.state.OpenTabs {
		if a.state.OpenTabs[i].ID == tabID {
			a.state.OpenTabs[i].CollectionID = collectionID
			a.state.OpenTabs[i].ItemID = itemID
			a.state.OpenTabs[i].Kind = "response-example"
			a.state.OpenTabs[i].ExampleID = example.ID
			a.state.OpenTabs[i].ExampleName = example.Name
			a.state.OpenTabs[i].ResponseTab = "examples"
			if a.state.OpenTabs[i].RequestPaneTab == "" {
				a.state.OpenTabs[i].RequestPaneTab = "params"
			}
			a.state.ActiveTabID = tabID
			return
		}
	}
	a.state.OpenTabs = append(a.state.OpenTabs, OpenTab{
		ID:             tabID,
		CollectionID:   collectionID,
		ItemID:         itemID,
		Kind:           "response-example",
		ExampleID:      example.ID,
		ExampleName:    example.Name,
		RequestPaneTab: "params",
		ResponseTab:    "examples",
	})
	a.state.ActiveTabID = tabID
}

func (a *App) rememberClosedTabLocked(tab OpenTab) {
	if tab.ID == "" || tab.Transient {
		return
	}
	next := a.state.ClosedTabs[:0]
	for _, existing := range a.state.ClosedTabs {
		if existing.ID != tab.ID {
			next = append(next, existing)
		}
	}
	next = append(next, tab)
	if len(next) > closedTabHistoryLimit {
		next = next[len(next)-closedTabHistoryLimit:]
	}
	a.state.ClosedTabs = next
}

func (a *App) lastClosedTabIndexLocked(collectionID string) int {
	for index := len(a.state.ClosedTabs) - 1; index >= 0; index-- {
		if collectionID == "" || a.state.ClosedTabs[index].CollectionID == collectionID {
			return index
		}
	}
	return -1
}

func (a *App) openTabIsRestorableLocked(tab OpenTab) bool {
	if tab.ID == "" || tab.CollectionID == "" || tab.ItemID == "" || tab.Transient {
		return false
	}
	collection, err := a.findCollectionLocked(tab.CollectionID)
	if err != nil {
		return false
	}
	item, err := findItem(collection, tab.ItemID)
	if err != nil {
		return false
	}
	if tab.Kind == "response-example" {
		_, _, err := findResponseExample(item, firstNonEmpty(tab.ExampleID, tab.ExampleName))
		return err == nil
	}
	return true
}

func (a *App) syncResponseExampleTabLocked(collectionID, itemID string, example ResponseExample) {
	tabID := responseExampleTabID(collectionID, itemID, example.ID)
	for i := range a.state.OpenTabs {
		tab := &a.state.OpenTabs[i]
		isSameExampleTab := tab.Kind == "response-example" && tab.CollectionID == collectionID && tab.ItemID == itemID && tab.ExampleID == example.ID
		if tab.ID != tabID && !isSameExampleTab {
			continue
		}
		tab.ID = tabID
		tab.CollectionID = collectionID
		tab.ItemID = itemID
		tab.Kind = "response-example"
		tab.ExampleID = example.ID
		tab.ExampleName = example.Name
		tab.ResponseTab = "examples"
		if tab.RequestPaneTab == "" {
			tab.RequestPaneTab = "params"
		}
	}
}

func (a *App) closeResponseExampleTabLocked(collectionID, itemID, exampleID string) {
	tabID := responseExampleTabID(collectionID, itemID, exampleID)
	removedActive := false
	next := a.state.OpenTabs[:0]
	for _, tab := range a.state.OpenTabs {
		matches := tab.ID == tabID || (tab.Kind == "response-example" && tab.CollectionID == collectionID && tab.ItemID == itemID && tab.ExampleID == exampleID)
		if matches {
			if tab.ID == a.state.ActiveTabID {
				removedActive = true
			}
			continue
		}
		next = append(next, tab)
	}
	a.state.OpenTabs = next
	if !removedActive {
		return
	}
	a.state.ActiveTabID = ""
	if len(a.state.OpenTabs) > 0 {
		a.state.ActiveTabID = a.state.OpenTabs[len(a.state.OpenTabs)-1].ID
	}
}
