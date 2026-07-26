package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/mutexdev/lite_api/internal/atomicfile"
)

const migrationSecretSentinel = "migration-environment-secret-sentinel"
const migrationCookieSentinel = "migration-cookie-secret-sentinel"

func TestExecuteWorkspaceMigrationWritesVerifiedPrivateIdempotentArtifacts(t *testing.T) {
	dir := t.TempDir()
	legacy := migrationLegacyState()
	legacyPath := filepath.Join(dir, "state.json")
	legacyBytes := []byte(`{"legacy":"` + migrationSecretSentinel + `"}`)
	if err := os.WriteFile(legacyPath, legacyBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	legacyHash := fileChecksum(legacyBytes)

	if err := ExecuteWorkspaceMigration(dir, legacy, "default-session"); err != nil {
		t.Fatal(err)
	}
	marker, err := readWorkspaceMigrationMarker(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !marker.Complete || len(marker.ArtifactChecksums) != 5 {
		t.Fatalf("unexpected marker: %+v", marker)
	}
	if data, err := os.ReadFile(legacyPath); err != nil || fileChecksum(data) != legacyHash {
		t.Fatalf("legacy state changed: %q err=%v", data, err)
	}

	artifactPaths := make([]string, 0, len(marker.ArtifactChecksums)+1)
	for relativePath := range marker.ArtifactChecksums {
		artifactPaths = append(artifactPaths, filepath.Join(dir, filepath.FromSlash(relativePath)))
	}
	artifactPaths = append(artifactPaths, workspaceMigrationMarkerPath(dir))
	for _, path := range artifactPaths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), migrationSecretSentinel) || strings.Contains(string(data), migrationCookieSentinel) {
			t.Fatalf("plaintext secret leaked to %s", path)
		}
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("artifact permissions %s: info=%+v err=%v", path, info, err)
		}
	}
	assertWorkspaceReferenceSchema(t, dir, "a")
	for _, path := range []string{workspaceMigrationMarkerPath(dir), defaultWorkspaceSessionPath(dir, "default-session")} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("private file permissions %s: info=%+v err=%v", path, info, err)
		}
	}

	firstMarker, err := os.ReadFile(workspaceMigrationMarkerPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if err := ExecuteWorkspaceMigration(dir, legacy, "default-session"); err != nil {
		t.Fatal(err)
	}
	secondMarker, err := os.ReadFile(workspaceMigrationMarkerPath(dir))
	if err != nil || string(firstMarker) != string(secondMarker) {
		t.Fatalf("idempotent migration rewrote marker: err=%v", err)
	}

	scoped, err := ReadWorkspaceScopedState(dir, "a")
	if err != nil {
		t.Fatal(err)
	}
	scoped.Workspace.Collections[0].Name = "changed"
	reloaded, err := ReadWorkspaceScopedState(dir, "a")
	if err != nil || reloaded.Workspace.Collections[0].Name != "Collection A" {
		t.Fatalf("scoped reader aliases persisted state: %+v err=%v", reloaded, err)
	}

	shared, err := ReadSharedAppState(dir)
	if err != nil {
		t.Fatal(err)
	}
	if shared.Cookies[0].Value != migrationCookieSentinel {
		t.Fatalf("shared runtime cookie was not decrypted with the canonical data dir: %q", shared.Cookies[0].Value)
	}
}

func assertWorkspaceReferenceSchema(t *testing.T, dir, workspaceID string) {
	t.Helper()
	data, err := os.ReadFile(workspaceScopedStatePath(dir, workspaceID))
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	workspace, ok := document["workspace"].(map[string]any)
	if !ok {
		t.Fatalf("workspace reference missing: %v", document)
	}
	collections, ok := workspace["collections"].([]any)
	if !ok || len(collections) != 1 {
		t.Fatalf("collection references missing: %v", workspace)
	}
	collection, ok := collections[0].(map[string]any)
	if !ok {
		t.Fatalf("invalid collection reference: %v", collections[0])
	}
	allowed := map[string]bool{"id": true, "name": true, "path": true, "format": true, "remote": true, "notFoundLocally": true, "createdAt": true, "updatedAt": true}
	for key := range collection {
		if !allowed[key] {
			t.Fatalf("workspace-state collection retained non-reference field %q: %v", key, collection)
		}
	}
}

func TestExecuteWorkspaceMigrationFailureLeavesNoCompleteMarkerAndRetryRepairs(t *testing.T) {
	dir := t.TempDir()
	legacy := migrationLegacyState()
	legacyPath := filepath.Join(dir, "state.json")
	legacyBytes := []byte("legacy unchanged")
	if err := os.WriteFile(legacyPath, legacyBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	oldWrite := workspacePersistenceWriteAtomic
	writes := 0
	workspacePersistenceWriteAtomic = func(path string, data []byte) error {
		writes++
		if writes == 3 {
			return errors.New("injected write failure")
		}
		return atomicfile.WritePrivate(path, data)
	}
	err := ExecuteWorkspaceMigration(dir, legacy, "retry-session")
	workspacePersistenceWriteAtomic = oldWrite
	if err == nil {
		t.Fatal("expected injected write failure")
	}
	if _, err := os.Stat(workspaceMigrationMarkerPath(dir)); !os.IsNotExist(err) {
		t.Fatalf("complete marker survived failed migration: %v", err)
	}
	if data, err := os.ReadFile(legacyPath); err != nil || fileChecksum(data) != fileChecksum(legacyBytes) {
		t.Fatalf("legacy changed after failed migration: %q err=%v", data, err)
	}
	if err := ExecuteWorkspaceMigration(dir, legacy, "retry-session"); err != nil {
		t.Fatalf("retry did not repair partial output: %v", err)
	}
	if _, err := readWorkspaceMigrationMarker(dir); err != nil {
		t.Fatal(err)
	}

	oldVerify := workspaceMigrationVerifyOutputs
	workspaceMigrationVerifyOutputs = func(string, WorkspaceMigrationMarker) error { return errors.New("injected verification failure") }
	_ = os.Remove(workspaceMigrationMarkerPath(dir))
	err = ExecuteWorkspaceMigration(dir, legacy, "verify-session")
	workspaceMigrationVerifyOutputs = oldVerify
	if err == nil {
		t.Fatal("expected injected verification failure")
	}
	if _, err := os.Stat(workspaceMigrationMarkerPath(dir)); !os.IsNotExist(err) {
		t.Fatalf("marker written despite verification failure: %v", err)
	}
}

func TestExecuteWorkspaceMigrationRejectsCorruptCommittedState(t *testing.T) {
	dir := t.TempDir()
	legacy := migrationLegacyState()
	if err := ExecuteWorkspaceMigration(dir, legacy, "repair-session"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(workspaceMigrationMarkerPath(dir), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ExecuteWorkspaceMigration(dir, legacy, "repair-session"); err == nil {
		t.Fatal("corrupt committed marker was rebuilt from legacy")
	}
	if err := ExecuteWorkspaceMigration(t.TempDir(), legacy, "repair-session"); err != nil {
		t.Fatal(err)
	}
	// Restore a valid commit, then prove mutable corruption is rejected too.
	dir = t.TempDir()
	if err := ExecuteWorkspaceMigration(dir, legacy, "repair-session"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(workspaceScopedStatePath(dir, "a"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ExecuteWorkspaceMigration(dir, legacy, "repair-session"); err == nil {
		t.Fatal("corrupt scoped output was rebuilt from stale legacy")
	}
	if err := ExecuteWorkspaceMigration(dir, legacy, "../invalid-session"); err == nil {
		t.Fatal("traversal-shaped session ID accepted")
	}
}

func TestExecuteWorkspaceMigrationConcurrentRunsLeaveVerifiedMarker(t *testing.T) {
	dir := t.TempDir()
	a, b := migrationLegacyState(), migrationLegacyState()
	b.Workspaces[0].Name = "B"
	var wg sync.WaitGroup
	for _, state := range []AppState{a, b} {
		wg.Add(1)
		go func(state AppState) {
			defer wg.Done()
			if err := ExecuteWorkspaceMigration(dir, state, "main-window"); err != nil {
				t.Errorf("migration: %v", err)
			}
		}(state)
	}
	wg.Wait()
	marker, err := readWorkspaceMigrationMarker(dir)
	if err != nil || workspaceMigrationVerifyOutputs(dir, marker) != nil {
		t.Fatalf("marker/output mismatch: marker=%+v err=%v", marker, err)
	}
	checksumA, _ := workspaceMigrationLegacyChecksum(a, dir)
	checksumB, _ := workspaceMigrationLegacyChecksum(b, dir)
	registry, readErr := ReadWorkspaceRegistry(dir)
	if readErr != nil || len(registry.Workspaces) == 0 {
		t.Fatalf("registry=%+v err=%v", registry, readErr)
	}
	if marker.LegacyChecksum == checksumA && registry.Workspaces[0].Name != a.Workspaces[0].Name {
		t.Fatalf("marker selected A but payload is %q", registry.Workspaces[0].Name)
	}
	if marker.LegacyChecksum == checksumB && registry.Workspaces[0].Name != b.Workspaces[0].Name {
		t.Fatalf("marker selected B but payload is %q", registry.Workspaces[0].Name)
	}
	if marker.LegacyChecksum != checksumA && marker.LegacyChecksum != checksumB {
		t.Fatalf("marker checksum is not a winning payload: %s", marker.LegacyChecksum)
	}
}

func migrationLegacyState() AppState {
	return AppState{
		Workspaces: []Workspace{
			{ID: "a", Name: "A", Path: "/a", GlobalEnvironments: []Environment{{ID: "global", Variables: []Variable{{ID: "secret", Value: migrationSecretSentinel, Secret: true}, {ID: "public", Value: "region-us"}}}}, Collections: []Collection{{ID: "ca", Name: "Collection A", Path: "/a/collection-a", Format: "yml", Environments: []Environment{{ID: "collection", Variables: []Variable{{ID: "secret", Value: migrationSecretSentinel, Secret: true}, {ID: "public", Value: "region-us"}}}}, Items: []RequestItem{{ID: "request", Name: "private request", Headers: []KeyValue{{Name: "Authorization", Value: migrationSecretSentinel}}, Body: RequestBody{JSON: migrationSecretSentinel}, Auth: AuthConfig{Token: migrationSecretSentinel}}}, ClientCertificates: []ClientCertificateConfig{{Passphrase: migrationSecretSentinel}}, Auth: AuthConfig{APIKey: migrationSecretSentinel}}}},
			{ID: "empty", Name: "Empty", Path: "/empty"},
		},
		ActiveWorkspaceID:  "a",
		OpenTabs:           []OpenTab{{ID: "a-tab", CollectionID: "ca"}},
		ClosedTabs:         []OpenTab{{ID: "a-closed", CollectionID: "ca"}},
		ActiveTabID:        "a-tab",
		Preferences:        Preferences{Layout: LayoutPreferences{ResponsePaneOrientation: "vertical"}, Proxy: ProxyPreferences{Config: ProxyConfig{Auth: ProxyAuthConfig{Password: migrationSecretSentinel}}}},
		GlobalEnvironments: []Environment{{ID: "app-global", Variables: []Variable{{ID: "app-secret", Value: migrationSecretSentinel, Secret: true}, {ID: "app-public", Value: "region-us"}}}},
		Cookies:            []CookieEntry{{ID: "cookie", Name: "session", Value: migrationCookieSentinel}},
	}
}
