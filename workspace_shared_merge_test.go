package main

import (
	"encoding/json"
	"testing"
)

func cloneSharedMergeTestState(t *testing.T, state SharedAppState) SharedAppState {
	t.Helper()
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	var cloned SharedAppState
	if err := json.Unmarshal(data, &cloned); err != nil {
		t.Fatal(err)
	}
	return cloned
}

func TestWorkspaceSharedDeltaPreservesUnrelatedUpdatesAndDeletes(t *testing.T) {
	base := SharedAppState{Version: 1, Preferences: Preferences{Theme: "system"}, Cookies: []CookieEntry{{ID: "remove", Value: "1"}}, Notifications: []Notification{{ID: "notice", Message: "old"}}, GlobalEnvironments: []Environment{{ID: "env", Name: "old"}}}
	current := base
	current.Cookies = nil
	current.Notifications = []Notification{{ID: "notice", Message: "mine"}}
	current.GlobalEnvironments = []Environment{{ID: "env", Name: "mine"}}
	disk := base
	disk.Preferences.Theme = "dark"
	disk.Cookies = []CookieEntry{{ID: "remove", Value: "1"}, {ID: "other", Value: "2"}}
	disk.Notifications = []Notification{{ID: "notice", Message: "old"}, {ID: "other", Message: "keep"}}
	disk.GlobalEnvironments = []Environment{{ID: "env", Name: "old"}, {ID: "other", Name: "keep"}}
	merged, err := mergeWorkspaceSharedDelta(base, current, disk)
	if err != nil {
		t.Fatal(err)
	}
	if merged.Preferences.Theme != "dark" || len(merged.Cookies) != 1 || merged.Cookies[0].ID != "other" || len(merged.Notifications) != 2 || merged.Notifications[0].Message != "mine" || len(merged.GlobalEnvironments) != 2 || merged.GlobalEnvironments[0].Name != "mine" {
		t.Fatalf("bad delta merge: %+v", merged)
	}
	current.Preferences.Theme = "light"
	merged, err = mergeWorkspaceSharedDelta(base, current, disk)
	if err != nil {
		t.Fatal(err)
	}
	if merged.Preferences.Theme != "light" {
		t.Fatal("local preference update lost")
	}
	base.GlobalEnvironments = []Environment{{ID: "one", Name: "one"}, {ID: "two", Name: "two"}}
	current = base
	current.GlobalEnvironments = []Environment{{ID: "one", Name: "one-local"}}
	disk = base
	disk.GlobalEnvironments = []Environment{{ID: "one", Name: "one"}, {ID: "two", Name: "two-disk"}, {ID: "three", Name: "three-disk"}}
	merged, err = mergeWorkspaceSharedDelta(base, current, disk)
	if err != nil {
		t.Fatal(err)
	}
	if len(merged.GlobalEnvironments) != 2 || merged.GlobalEnvironments[0].Name != "one-local" || merged.GlobalEnvironments[1].Name != "three-disk" {
		t.Fatalf("environment delta failed: %+v", merged.GlobalEnvironments)
	}
}

func TestOAuth2DeltaKeepsOtherProcessChangesAndDeletes(t *testing.T) {
	base := map[string]oauth2TokenResponse{"a": {AccessToken: "old"}, "delete": {AccessToken: "gone"}}
	current := map[string]oauth2TokenResponse{"a": {AccessToken: "mine"}, "new": {AccessToken: "new"}}
	disk := map[string]oauth2TokenResponse{"a": {AccessToken: "other"}, "delete": {AccessToken: "gone"}, "other": {AccessToken: "keep"}}
	merged := mergeOAuth2TokenDelta(base, current, disk)
	if merged["a"].AccessToken != "mine" || merged["new"].AccessToken != "new" || merged["other"].AccessToken != "keep" {
		t.Fatalf("oauth merge lost updates: %+v", merged)
	}
	if _, ok := merged["delete"]; ok {
		t.Fatal("oauth delete resurrected")
	}
}

func TestWorkspaceSharedDeltaMergesNestedPreferencesAndEnvironmentVariables(t *testing.T) {
	base := SharedAppState{Version: 1, Preferences: Preferences{Theme: "system", Layout: LayoutPreferences{ResponsePaneOrientation: "horizontal"}}, GlobalEnvironments: []Environment{{ID: "env", Variables: []Variable{{ID: "one", Value: "1"}, {ID: "two", Value: "2"}}}}}
	a := cloneSharedMergeTestState(t, base)
	a.Preferences.Theme = "dark"
	a.GlobalEnvironments[0].Variables[0].Value = "A"
	b := cloneSharedMergeTestState(t, base)
	b.Preferences.Layout.ResponsePaneOrientation = "vertical"
	b.GlobalEnvironments[0].Variables[1].Value = "B"
	merged, err := mergeWorkspaceSharedDelta(base, a, b)
	if err != nil {
		t.Fatal(err)
	}
	if merged.Preferences.Theme != "dark" || merged.Preferences.Layout.ResponsePaneOrientation != "vertical" || merged.GlobalEnvironments[0].Variables[0].Value != "A" || merged.GlobalEnvironments[0].Variables[1].Value != "B" {
		t.Fatalf("nested merge failed: %+v", merged)
	}
}

func TestWorkspaceSharedDeltaMergesEnvironmentMetadataAndVariablesIndependently(t *testing.T) {
	base := SharedAppState{Version: 1, GlobalEnvironments: []Environment{{ID: "env", Name: "old", Color: "red", Variables: []Variable{{ID: "token", Value: "old"}}}}}
	a := cloneSharedMergeTestState(t, base)
	a.GlobalEnvironments[0].Name = "renamed"
	a.GlobalEnvironments[0].Color = "blue"
	b := cloneSharedMergeTestState(t, base)
	b.GlobalEnvironments[0].Variables[0].Value = "new-token"
	merged, err := mergeWorkspaceSharedDelta(base, a, b)
	if err != nil {
		t.Fatal(err)
	}
	env := merged.GlobalEnvironments[0]
	if env.Name != "renamed" || env.Color != "blue" || env.Variables[0].Value != "new-token" {
		t.Fatalf("environment metadata/variable merge failed: %+v", env)
	}
}
