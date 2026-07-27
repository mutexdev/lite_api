// The whole persisted app state, and the workspace it belongs to.
//
// US-060. Moved verbatim from app.go; see internal/types/proxy.go for why the
// aliases left behind in internal/core are a Go shim and not a Wails one.
package types

import "time"

type AppState struct {
	Workspaces         []Workspace    `json:"workspaces"`
	ActiveWorkspaceID  string         `json:"activeWorkspaceId"`
	OpenTabs           []OpenTab      `json:"openTabs"`
	ClosedTabs         []OpenTab      `json:"closedTabs,omitempty"`
	ActiveTabID        string         `json:"activeTabId"`
	FeatureLedger      []Feature      `json:"featureLedger"`
	GlobalEnvironments []Environment  `json:"globalEnvironments"`
	Preferences        Preferences    `json:"preferences"`
	Notifications      []Notification `json:"notifications"`
	NetworkLog         []NetworkLog   `json:"networkLog"`
	Runner             RunnerSnapshot `json:"runner"`
	Cookies            []CookieEntry  `json:"cookies"`
	// Revision is a monotonic counter, bumped exactly once per mutation by
	// markDirty and never by a read. The frontend compares the revision on a
	// narrow mutator result against the one it last applied; a gap means it
	// missed an update and must refetch the whole AppState (US-014).
	//
	// It is deliberately NOT persisted — stateForStorage zeroes it. The
	// guarantee the frontend needs is monotonicity within one App instance,
	// and restoring a counter from disk cannot provide that: under the
	// multi-window shared-state model a window that reloads state written by
	// another window would see the revision jump backwards. Starting every
	// instance at zero is both monotonic and honest, because the frontend
	// fetches the full state on boot anyway.
	Revision int64 `json:"revision"`
}

type Workspace struct {
	ID                        string        `json:"id"`
	Name                      string        `json:"name"`
	Path                      string        `json:"path"`
	ScratchCollectionID       string        `json:"scratchCollectionId,omitempty"`
	ScratchTempDirectory      string        `json:"scratchTempDirectory,omitempty"`
	Collections               []Collection  `json:"collections"`
	GlobalEnvironments        []Environment `json:"globalEnvironments"`
	ActiveGlobalEnvironmentID string        `json:"activeGlobalEnvironmentId"`
	Docs                      string        `json:"docs"`
	CreatedAt                 time.Time     `json:"createdAt"`
	UpdatedAt                 time.Time     `json:"updatedAt"`
}
