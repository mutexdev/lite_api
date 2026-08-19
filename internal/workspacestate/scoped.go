package workspacestate

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/mutexdev/lite_api/internal/types"
)

const WorkspaceScopedStateVersion = 1

type WorkspaceScopedState struct {
	Version     int                      `json:"version"`
	Workspace   WorkspaceScopedReference `json:"workspace"`
	OpenTabs    []types.OpenTab          `json:"openTabs"`
	ClosedTabs  []types.OpenTab          `json:"closedTabs,omitempty"`
	ActiveTabID string                   `json:"activeTabId,omitempty"`
	UpdatedAt   time.Time                `json:"updatedAt"`
}

// WorkspaceScopedReference is intentionally not types.Workspace. It describes how
// to locate authoritative workspace/collection files without duplicating their
// request payloads, auth values, environments or client credentials.
type WorkspaceScopedReference struct {
	ID                        string                      `json:"id"`
	Name                      string                      `json:"name"`
	Path                      string                      `json:"path"`
	Docs                      string                      `json:"docs,omitempty"`
	ActiveGlobalEnvironmentID string                      `json:"activeGlobalEnvironmentId,omitempty"`
	Collections               []CollectionScopedReference `json:"collections"`
	CreatedAt                 time.Time                   `json:"createdAt"`
	UpdatedAt                 time.Time                   `json:"updatedAt"`
}

type CollectionScopedReference struct {
	ID              string    `json:"id"`
	Name            string    `json:"name"`
	Path            string    `json:"path"`
	Format          string    `json:"format"`
	Remote          string    `json:"remote,omitempty"`
	NotFoundLocally bool      `json:"notFoundLocally,omitempty"`
	CreatedAt       time.Time `json:"createdAt"`
	UpdatedAt       time.Time `json:"updatedAt"`
}

type WorkspaceMigrationPlan struct {
	Version         int                    `json:"version"`
	LegacyStatePath string                 `json:"legacyStatePath"`
	MarkerPath      string                 `json:"markerPath"`
	States          []WorkspaceScopedState `json:"states"`
	Complete        bool                   `json:"complete"`
}

// WorkspaceScopedStatePath derives a filesystem-safe, collision-resistant name
// from the exact workspace ID. types.Workspace IDs are user-controlled state and must
// not be normalized through a lossy filename sanitizer.
func WorkspaceScopedStatePath(dataDir, id string) string {
	digest := sha256.Sum256([]byte(id))
	return filepath.Join(dataDir, "workspace-state", hex.EncodeToString(digest[:])+".json")
}

func ProjectWorkspaceState(state types.AppState, workspaceID string) (WorkspaceScopedState, error) {
	if err := validateGlobalCollectionIDs(state); err != nil {
		return WorkspaceScopedState{}, err
	}
	var workspace *types.Workspace
	for i := range state.Workspaces {
		if state.Workspaces[i].ID == workspaceID {
			workspace = &state.Workspaces[i]
			break
		}
	}
	if workspace == nil {
		return WorkspaceScopedState{}, errors.New("workspace not found")
	}

	projectedWorkspace := workspaceScopedWorkspaceReference(*workspace)
	allowedCollections := make(map[string]bool, len(workspace.Collections))
	for _, collection := range workspace.Collections {
		if collection.Scratch {
			continue
		}
		allowedCollections[collection.ID] = true
	}
	filterTabs := func(tabs []types.OpenTab) []types.OpenTab {
		filtered := make([]types.OpenTab, 0, len(tabs))
		for _, tab := range tabs {
			if allowedCollections[tab.CollectionID] {
				filtered = append(filtered, tab)
			}
		}
		return filtered
	}

	openTabs := filterTabs(state.OpenTabs)
	activeTabID := state.ActiveTabID
	if !TabIDPresent(openTabs, activeTabID) {
		activeTabID = ""
		if len(openTabs) > 0 {
			activeTabID = openTabs[0].ID
		}
	}

	return CloneWorkspaceScopedState(WorkspaceScopedState{
		Version:     WorkspaceScopedStateVersion,
		Workspace:   projectedWorkspace,
		OpenTabs:    openTabs,
		ClosedTabs:  filterTabs(state.ClosedTabs),
		ActiveTabID: activeTabID,
		UpdatedAt:   time.Now().UTC(),
	})
}

func BuildWorkspaceMigrationPlan(dataDir string, state types.AppState) (WorkspaceRegistry, WorkspaceMigrationPlan, error) {
	if err := validateGlobalCollectionIDs(state); err != nil {
		return WorkspaceRegistry{}, WorkspaceMigrationPlan{}, err
	}
	registry := WorkspaceRegistry{Version: workspaceRegistryVersion}
	plan := WorkspaceMigrationPlan{
		Version:         WorkspaceScopedStateVersion,
		LegacyStatePath: filepath.Join(dataDir, "state.json"),
		MarkerPath:      filepath.Join(dataDir, "workspace-migration-v1.json"),
	}
	for _, workspace := range state.Workspaces {
		scoped, err := ProjectWorkspaceState(state, workspace.ID)
		if err != nil {
			return WorkspaceRegistry{}, WorkspaceMigrationPlan{}, err
		}
		// Empty workspaces are meaningful registry entries and need a scoped
		// state file so their identity survives migration.
		registry.Workspaces = append(registry.Workspaces, WorkspaceRegistryEntry{
			ID: workspace.ID, Name: workspace.Name, Path: workspace.Path, UpdatedAt: workspace.UpdatedAt,
		})
		plan.States = append(plan.States, scoped)
	}
	return registry, plan, registry.Validate()
}

func MergeWorkspaceScopedState(state types.AppState, scoped WorkspaceScopedState) (types.AppState, error) {
	if err := validateGlobalCollectionIDs(state); err != nil {
		return types.AppState{}, err
	}
	if scoped.Version != WorkspaceScopedStateVersion || strings.TrimSpace(scoped.Workspace.ID) == "" {
		return types.AppState{}, errors.New("workspace state version is invalid")
	}

	next, err := cloneAppStateForWorkspaceMerge(state)
	if err != nil {
		return types.AppState{}, err
	}
	clonedScoped, err := CloneWorkspaceScopedState(scoped)
	if err != nil {
		return types.AppState{}, err
	}

	workspaceIndex := -1
	workspaceCollectionIDs := make(map[string]bool)
	for i := range state.Workspaces {
		if state.Workspaces[i].ID != clonedScoped.Workspace.ID {
			continue
		}
		workspaceIndex = i
		for _, collection := range state.Workspaces[i].Collections {
			workspaceCollectionIDs[collection.ID] = true
		}
		break
	}
	if workspaceIndex < 0 {
		return types.AppState{}, fmt.Errorf("workspace %s not found", clonedScoped.Workspace.ID)
	}
	for _, collection := range clonedScoped.Workspace.Collections {
		workspaceCollectionIDs[collection.ID] = true
	}
	mergeWorkspaceReferenceIntoAuthoritativeWorkspace(&next.Workspaces[workspaceIndex], clonedScoped.Workspace)
	next.OpenTabs = mergeWorkspaceTabs(next.OpenTabs, clonedScoped.OpenTabs, workspaceCollectionIDs)
	next.ClosedTabs = mergeWorkspaceTabs(next.ClosedTabs, clonedScoped.ClosedTabs, workspaceCollectionIDs)

	// The restored scoped selection wins if valid; otherwise retain an active
	// tab from another workspace, then use the first remaining tab.
	if TabIDPresent(next.OpenTabs, clonedScoped.ActiveTabID) {
		next.ActiveTabID = clonedScoped.ActiveTabID
	} else if TabIDPresent(next.OpenTabs, state.ActiveTabID) {
		next.ActiveTabID = state.ActiveTabID
	} else if len(next.OpenTabs) > 0 {
		next.ActiveTabID = next.OpenTabs[0].ID
	} else {
		next.ActiveTabID = ""
	}
	return next, nil
}

func validateGlobalCollectionIDs(state types.AppState) error {
	seen := map[string]string{}
	for _, workspace := range state.Workspaces {
		for _, collection := range workspace.Collections {
			id := strings.TrimSpace(collection.ID)
			if id == "" {
				continue
			}
			if owner, ok := seen[id]; ok && owner != workspace.ID {
				return fmt.Errorf("collection ID %s is duplicated across workspaces", id)
			}
			seen[id] = workspace.ID
		}
	}
	return nil
}

func EncodeWorkspaceScopedState(scoped WorkspaceScopedState) ([]byte, error) {
	if scoped.Version != WorkspaceScopedStateVersion || ValidateWorkspaceRegistryID(scoped.Workspace.ID) != nil {
		return nil, errors.New("workspace state is invalid")
	}
	if _, err := CanonicalWorkspaceIdentity(scoped.Workspace.Path); err != nil {
		return nil, errors.New("workspace state path is invalid")
	}
	return json.MarshalIndent(scoped, "", "  ")
}

func workspaceScopedWorkspaceReference(workspace types.Workspace) WorkspaceScopedReference {
	reference := WorkspaceScopedReference{
		ID:                        workspace.ID,
		Name:                      workspace.Name,
		Path:                      workspace.Path,
		Docs:                      workspace.Docs,
		ActiveGlobalEnvironmentID: workspace.ActiveGlobalEnvironmentID,
		CreatedAt:                 workspace.CreatedAt,
		UpdatedAt:                 workspace.UpdatedAt,
	}
	for _, collection := range workspace.Collections {
		if collection.Scratch {
			continue
		}
		reference.Collections = append(reference.Collections, workspaceScopedCollectionReference(collection))
	}
	return reference
}

func workspaceScopedCollectionReference(collection types.Collection) CollectionScopedReference {
	return CollectionScopedReference{
		ID:              collection.ID,
		Name:            collection.Name,
		Path:            collection.Path,
		Format:          collection.Format,
		Remote:          collection.Remote,
		NotFoundLocally: collection.NotFoundLocally,
		CreatedAt:       collection.CreatedAt,
		UpdatedAt:       collection.UpdatedAt,
	}
}

func mergeWorkspaceReferenceIntoAuthoritativeWorkspace(workspace *types.Workspace, reference WorkspaceScopedReference) {
	// References intentionally omit operational workspace fields. Preserve them
	// here and only reconcile the metadata that the scoped representation owns.
	workspace.Name = reference.Name
	workspace.Path = reference.Path
	workspace.Docs = reference.Docs
	workspace.ActiveGlobalEnvironmentID = reference.ActiveGlobalEnvironmentID
	workspace.CreatedAt = reference.CreatedAt
	workspace.UpdatedAt = reference.UpdatedAt

	references := make(map[string]CollectionScopedReference, len(reference.Collections))
	for _, collection := range reference.Collections {
		references[collection.ID] = collection
	}
	seen := make(map[string]bool, len(references))
	for i := range workspace.Collections {
		reference, ok := references[workspace.Collections[i].ID]
		if !ok {
			// A missing reference may be a local scratch or a collection that was
			// added after the scoped snapshot. Retaining it is the safe choice.
			continue
		}
		mergeCollectionReferenceIntoAuthoritativeCollection(&workspace.Collections[i], reference)
		seen[reference.ID] = true
	}
	for _, reference := range reference.Collections {
		if seen[reference.ID] {
			continue
		}
		// New references have no authoritative payload in this process yet.
		// Append only their metadata; a later file hydration step owns content.
		workspace.Collections = append(workspace.Collections, types.Collection{
			ID: reference.ID, Name: reference.Name, Path: reference.Path, Format: reference.Format, Remote: reference.Remote,
			NotFoundLocally: reference.NotFoundLocally, CreatedAt: reference.CreatedAt, UpdatedAt: reference.UpdatedAt,
		})
	}
}

func mergeCollectionReferenceIntoAuthoritativeCollection(collection *types.Collection, reference CollectionScopedReference) {
	collection.Name = reference.Name
	collection.Path = reference.Path
	collection.Format = reference.Format
	collection.Remote = reference.Remote
	collection.NotFoundLocally = reference.NotFoundLocally
	collection.CreatedAt = reference.CreatedAt
	collection.UpdatedAt = reference.UpdatedAt
}

func CloneWorkspaceScopedState(scoped WorkspaceScopedState) (WorkspaceScopedState, error) {
	data, err := json.Marshal(scoped)
	if err != nil {
		return WorkspaceScopedState{}, fmt.Errorf("clone workspace scoped state: %w", err)
	}
	var cloned WorkspaceScopedState
	if err := json.Unmarshal(data, &cloned); err != nil {
		return WorkspaceScopedState{}, fmt.Errorf("clone workspace scoped state: %w", err)
	}
	return cloned, nil
}

func cloneAppStateForWorkspaceMerge(state types.AppState) (types.AppState, error) {
	data, err := json.Marshal(state)
	if err != nil {
		return types.AppState{}, fmt.Errorf("clone app state for workspace merge: %w", err)
	}
	var cloned types.AppState
	if err := json.Unmarshal(data, &cloned); err != nil {
		return types.AppState{}, fmt.Errorf("clone app state for workspace merge: %w", err)
	}
	return cloned, nil
}

func mergeWorkspaceTabs(existing, scoped []types.OpenTab, workspaceCollectionIDs map[string]bool) []types.OpenTab {
	merged := make([]types.OpenTab, 0, len(existing)+len(scoped))
	for _, tab := range existing {
		if !workspaceCollectionIDs[tab.CollectionID] {
			merged = append(merged, tab)
		}
	}
	return append(merged, scoped...)
}

func TabIDPresent(tabs []types.OpenTab, id string) bool {
	if id == "" {
		return false
	}
	for _, tab := range tabs {
		if tab.ID == id {
			return true
		}
	}
	return false
}
