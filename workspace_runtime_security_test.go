package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestProductionRejectsSymlinkMutableArtifact(t *testing.T) {
	dir := t.TempDir()
	legacy := AppState{Workspaces: []Workspace{{ID: "a", Name: "A", Path: filepath.Join(dir, "a")}}, ActiveWorkspaceID: "a"}
	if err := ExecuteWorkspaceMigration(dir, legacy, "main-window"); err != nil {
		t.Fatal(err)
	}
	external := filepath.Join(dir, "external-shared.json")
	data, err := os.ReadFile(sharedAppStatePath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(external, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(sharedAppStatePath(dir)); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, sharedAppStatePath(dir)); err != nil {
		t.Fatal(err)
	}
	if _, err := newProductionApp(dir, nil); err == nil {
		t.Fatal("symlinked shared state was followed")
	}
}

func TestOAuthCorruptionPropagatesWithoutOverwritingEvidence(t *testing.T) {
	dir := t.TempDir()
	legacy := AppState{Workspaces: []Workspace{{ID: "a", Name: "A", Path: filepath.Join(dir, "a")}}, ActiveWorkspaceID: "a"}
	if err := ExecuteWorkspaceMigration(dir, legacy, "main-window"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "oauth2.json")
	original := []byte("{")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := newProductionApp(dir, nil); err == nil {
		t.Fatal("production load ignored corrupt OAuth evidence")
	}
	app := newAppBase(dir)
	if err := app.storeOAuth2Credentials(); err == nil {
		t.Fatal("OAuth store overwrote corrupt evidence")
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != string(original) {
		t.Fatalf("corrupt evidence changed: %q err=%v", got, err)
	}
}

func TestOAuthEmptyDeleteFailurePreservesCacheAndBaseline(t *testing.T) {
	dir := t.TempDir()
	app := newAppBase(dir)
	token := oauth2TokenResponse{AccessToken: "token"}
	encoded, err := encryptOAuth2TokenResponse(dir, token)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(oauth2CredentialsFile{Credentials: []oauth2CredentialEntry{{CacheKey: "key", Data: encoded}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(app.oauth2CredentialsPath(), data, 0o600); err != nil {
		t.Fatal(err)
	}
	app.oauth2 = map[string]oauth2TokenResponse{}
	app.oauth2Baseline = map[string]oauth2TokenResponse{"key": token}
	baselineBefore := cloneOAuth2TokenMap(app.oauth2Baseline)
	cacheBefore := cloneOAuth2TokenMap(app.oauth2)
	originalRemove := oauth2CredentialsRemove
	oauth2CredentialsRemove = func(string) error { return errors.New("remove denied") }
	defer func() { oauth2CredentialsRemove = originalRemove }()
	if err := app.storeOAuth2Credentials(); err == nil {
		t.Fatal("OAuth delete failure was ignored")
	}
	if !reflect.DeepEqual(app.oauth2, cacheBefore) || !reflect.DeepEqual(app.oauth2Baseline, baselineBefore) {
		t.Fatalf("OAuth cache advanced after failed delete: cache=%+v baseline=%+v", app.oauth2, app.oauth2Baseline)
	}
	if _, err := os.Stat(app.oauth2CredentialsPath()); err != nil {
		t.Fatalf("OAuth evidence was lost: %v", err)
	}
}
