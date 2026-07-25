package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const scopedStateSecretSentinel = "workspace-secret-sentinel"

func TestWorkspaceMigrationPlanProjectsIndependentScrubbedState(t *testing.T) {
	dir := t.TempDir()
	state := workspaceStateFixture()

	registry, plan, err := BuildWorkspaceMigrationPlan(dir, state)
	if err != nil {
		t.Fatal(err)
	}
	if len(registry.Workspaces) != 3 || len(plan.States) != 3 || plan.Complete {
		t.Fatalf("registry=%+v plan=%+v", registry, plan)
	}
	if registry.Workspaces[2].ID != "empty" || plan.States[2].Workspace.ID != "empty" {
		t.Fatalf("empty workspace was not retained: %+v %+v", registry, plan)
	}

	scoped := plan.States[0]
	if len(scoped.Workspace.Collections) != 1 || scoped.Workspace.Collections[0].ID != "ca" {
		t.Fatalf("scratch collection leaked into projection: %+v", scoped.Workspace.Collections)
	}
	if len(scoped.OpenTabs) != 1 || scoped.OpenTabs[0].ID != "a-open" || len(scoped.ClosedTabs) != 1 || scoped.ClosedTabs[0].ID != "a-closed" || scoped.ActiveTabID != "a-open" {
		t.Fatalf("workspace tabs were not isolated: %+v", scoped)
	}
	encodedScoped, err := EncodeWorkspaceScopedState(scoped)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encodedScoped), scopedStateSecretSentinel) || strings.Contains(string(encodedScoped), "region-us") {
		t.Fatalf("scoped JSON retained collection or environment state: %s", encodedScoped)
	}

	// Mutating the lightweight projection must never mutate source metadata;
	// environments are deliberately not represented in the persisted DTO.
	scoped.Workspace.Name = "mutated"
	scoped.Workspace.Collections[0].Name = "mutated collection"
	if state.Workspaces[0].Name != "A" || state.Workspaces[0].Collections[0].Name != "Source Collection" || state.Workspaces[0].GlobalEnvironments[0].Variables[1].Value != "region-us" || state.Workspaces[0].Collections[0].Environments[0].Variables[1].Value != "region-us" {
		t.Fatalf("projection mutated source state: %+v", state.Workspaces[0])
	}
	if err := WriteWorkspaceRegistry(dir, registry); err != nil {
		t.Fatal(err)
	}
	registryJSON, err := os.ReadFile(workspaceRegistryPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(registryJSON), scopedStateSecretSentinel) {
		t.Fatalf("registry JSON contains environment secret: %s", registryJSON)
	}
	got, err := ReadWorkspaceRegistry(dir)
	if err != nil || got.Workspaces[1].ID != "b" {
		t.Fatalf("registry=%+v err=%v", got, err)
	}
	info, err := os.Stat(workspaceRegistryPath(dir))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("registry permissions: info=%+v err=%v", info, err)
	}
	if _, err := os.ReadFile(filepath.Join(dir, "state.json")); !os.IsNotExist(err) {
		t.Fatal("migration must not write legacy state")
	}
}

func TestMergeWorkspaceScopedStateReplacesOnlySelectedWorkspaceTabs(t *testing.T) {
	state := workspaceStateFixture()
	scoped, err := ProjectWorkspaceState(state, "a")
	if err != nil {
		t.Fatal(err)
	}
	scoped.Workspace.Name = "A restored"
	scoped.Workspace.Collections[0].Name = "Restored Collection"
	scoped.OpenTabs = []OpenTab{{ID: "a-restored", CollectionID: "ca"}}
	scoped.ClosedTabs = []OpenTab{{ID: "a-closed-restored", CollectionID: "ca"}}
	scoped.ActiveTabID = "a-restored"

	merged, err := MergeWorkspaceScopedState(state, scoped)
	if err != nil {
		t.Fatal(err)
	}
	if got := tabIDs(merged.OpenTabs); got != "b-open,a-restored" {
		t.Fatalf("open tabs=%s", got)
	}
	if got := tabIDs(merged.ClosedTabs); got != "b-closed,a-closed-restored" {
		t.Fatalf("closed tabs=%s", got)
	}
	if merged.ActiveTabID != "a-restored" || merged.Workspaces[0].Name != "A restored" {
		t.Fatalf("invalid merge result: %+v", merged)
	}

	// The merge result owns all copied slices. Its mutations cannot affect the
	// caller's input state or the scoped payload supplied by the caller.
	merged.Workspaces[0].Collections[0].Name = "changed"
	merged.OpenTabs[0].ID = "changed"
	if state.Workspaces[0].Collections[0].Name != "Source Collection" || state.OpenTabs[1].ID != "b-open" || scoped.Workspace.Collections[0].Name != "Restored Collection" {
		t.Fatalf("merge aliases its inputs: state=%+v scoped=%+v", state, scoped)
	}
	if got := merged.Workspaces[0]; got.ScratchCollectionID != "scratch" || got.ScratchTempDirectory != "/tmp/scratch" || got.GlobalEnvironments[0].Variables[0].Value != scopedStateSecretSentinel || got.Collections[0].Items[0].Auth.Token != scopedStateSecretSentinel || got.Collections[0].Auth.APIKey != scopedStateSecretSentinel || got.Collections[0].Environments[0].Variables[0].Value != scopedStateSecretSentinel || got.Collections[0].ClientCertificates[0].Passphrase != scopedStateSecretSentinel {
		t.Fatalf("merge erased authoritative workspace payload: %+v", got)
	}
}

func TestWorkspaceScopedStatePathUsesExactIDHash(t *testing.T) {
	dir := t.TempDir()
	first := workspaceScopedStatePath(dir, "a/b")
	second := workspaceScopedStatePath(dir, "a-b")
	traversal := workspaceScopedStatePath(dir, "../outside")
	stateDir := filepath.Join(dir, "workspace-state")
	if first == second || filepath.Dir(first) != stateDir || filepath.Dir(traversal) != stateDir || strings.Contains(filepath.Base(traversal), "outside") {
		t.Fatalf("unsafe or colliding paths: first=%q second=%q traversal=%q", first, second, traversal)
	}
}

func TestWorkspaceRegistryRejectsCorruptVersionsAndCollisions(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(workspaceRegistryPath(dir), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadWorkspaceRegistry(dir); err == nil {
		t.Fatal("corrupt registry accepted")
	}
	registry := WorkspaceRegistry{Version: workspaceRegistryVersion, Workspaces: []WorkspaceRegistryEntry{{ID: "x", Path: "/x"}, {ID: "x", Path: "/y"}}}
	if err := registry.Validate(); err == nil {
		t.Fatal("duplicate registry ID accepted")
	}
	if _, err := EncodeWorkspaceScopedState(WorkspaceScopedState{Version: 2}); err == nil {
		t.Fatal("unsupported scoped version accepted")
	}
}

func TestWorkspaceProjectionRejectsDuplicateCollectionIDsAcrossWorkspaces(t *testing.T) {
	state := AppState{Workspaces: []Workspace{{ID: "a", Collections: []Collection{{ID: "duplicate"}}}, {ID: "b", Collections: []Collection{{ID: "duplicate"}}}}}
	if _, err := ProjectWorkspaceState(state, "a"); err == nil {
		t.Fatal("ambiguous cross-workspace collection IDs accepted")
	}
}

func workspaceStateFixture() AppState {
	secretAndPublic := func() []Environment {
		return []Environment{{ID: "env", Name: "shared", Variables: []Variable{
			{ID: "secret", Name: "token", Value: scopedStateSecretSentinel, Secret: true, Enabled: true},
			{ID: "public", Name: "region", Value: "region-us", Enabled: true},
		}}}
	}
	return AppState{
		Workspaces: []Workspace{
			{ID: "a", Name: "A", Path: "/a", ScratchCollectionID: "scratch", ScratchTempDirectory: "/tmp/scratch", GlobalEnvironments: secretAndPublic(), Collections: []Collection{
				{ID: "ca", Name: "Source Collection", Environments: secretAndPublic(), Items: []RequestItem{{ID: "request", Auth: AuthConfig{Token: scopedStateSecretSentinel}}}, Auth: AuthConfig{APIKey: scopedStateSecretSentinel}, ClientCertificates: []ClientCertificateConfig{{Passphrase: scopedStateSecretSentinel}}},
				{ID: "scratch", Scratch: true},
			}},
			{ID: "b", Name: "B", Path: "/b", Collections: []Collection{{ID: "cb"}}},
			{ID: "empty", Name: "Empty", Path: "/empty"},
		},
		OpenTabs: []OpenTab{
			{ID: "a-open", CollectionID: "ca"},
			{ID: "b-open", CollectionID: "cb"},
			{ID: "scratch-open", CollectionID: "scratch"},
		},
		ClosedTabs:  []OpenTab{{ID: "a-closed", CollectionID: "ca"}, {ID: "b-closed", CollectionID: "cb"}},
		ActiveTabID: "b-open",
	}
}

func tabIDs(tabs []OpenTab) string {
	ids := make([]string, len(tabs))
	for i := range tabs {
		ids[i] = tabs[i].ID
	}
	return strings.Join(ids, ",")
}
