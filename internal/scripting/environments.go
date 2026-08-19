package scripting

// Which environments and workspaces a script can see.
//
// Split out of scripting.go by AST: declarations are identified by the parser
// and copied verbatim from their source offsets.

import (
	"fmt"
	"strings"

	"github.com/mutexdev/lite_api/internal/scalar"
	"github.com/mutexdev/lite_api/internal/types"
)

func ActiveGlobalEnvironmentsForWorkspace(workspace types.Workspace) []types.Environment {
	if len(workspace.GlobalEnvironments) == 0 {
		return nil
	}
	if workspace.ActiveGlobalEnvironmentID == "" {
		return nil
	}
	for _, env := range workspace.GlobalEnvironments {
		if env.ID == workspace.ActiveGlobalEnvironmentID {
			return []types.Environment{env}
		}
	}
	return nil
}

func WorkspaceHasGlobalEnvironment(workspace types.Workspace, environmentID string) bool {
	if environmentID == "" {
		return true
	}
	for _, env := range workspace.GlobalEnvironments {
		if env.ID == environmentID {
			return true
		}
	}
	return false
}

func CloneEnvironmentWithNewIDs(env types.Environment, idPrefix string) types.Environment {
	cloned := env
	cloned.ID = scalar.NewID(idPrefix)
	if len(env.Variables) > 0 {
		cloned.Variables = append([]types.Variable(nil), env.Variables...)
		for index := range cloned.Variables {
			cloned.Variables[index].ID = scalar.NewID("var")
		}
	}
	return cloned
}

func UniqueEnvironmentName(environments []types.Environment, desired string) string {
	desired = strings.TrimSpace(desired)
	if desired == "" {
		desired = "Environment"
	}
	used := map[string]bool{}
	for _, env := range environments {
		used[strings.ToLower(strings.TrimSpace(env.Name))] = true
	}
	if !used[strings.ToLower(desired)] {
		return desired
	}
	for index := 1; ; index++ {
		candidate := desired + " copy"
		if index > 1 {
			candidate = fmt.Sprintf("%s copy %d", desired, index)
		}
		if !used[strings.ToLower(candidate)] {
			return candidate
		}
	}
}

func FindWorkspaceForCollection(state *types.AppState, collectionID string) (*types.Workspace, *types.Collection) {
	if state == nil {
		return nil, nil
	}
	for wi := range state.Workspaces {
		for ci := range state.Workspaces[wi].Collections {
			if state.Workspaces[wi].Collections[ci].ID == collectionID {
				return &state.Workspaces[wi], &state.Workspaces[wi].Collections[ci]
			}
		}
	}
	return nil, nil
}

func SelectedEnvironmentName(collection *types.Collection, environmentID string) string {
	if collection == nil || environmentID == "" {
		return ""
	}
	for _, env := range collection.Environments {
		if env.ID == environmentID {
			return env.Name
		}
	}
	return ""
}
