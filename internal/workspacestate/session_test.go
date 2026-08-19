package workspacestate

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mutexdev/lite_api/internal/types"
)

func TestWindowLaunchIntentStrictValidation(t *testing.T) {
	ok, err := ParseWindowLaunchIntent([]string{"--window-session", "one", "--workspace-id", "ws", "--data-dir", t.TempDir()})
	if err != nil || ok.WorkspaceID != "ws" {
		t.Fatal(ok, err)
	}
	for _, args := range [][]string{{"--window-session", "x", "--data-dir", "d"}, {"--window-session", "x", "--workspace-id", "w", "--workspace-path", "p", "--data-dir", "d"}, {"--window-session", "x", "--workspace-id", "w"}} {
		if _, err := ParseWindowLaunchIntent(args); err == nil {
			t.Fatalf("expected error %v", args)
		}
	}
}
func TestWindowSessionPrivateAtomicAndIsolation(t *testing.T) {
	dir := t.TempDir()
	a := WindowSession{Version: 1, ID: "a", WorkspaceID: "one", UpdatedAt: time.Now()}
	b := WindowSession{Version: 1, ID: "b", WorkspaceID: "two", UpdatedAt: time.Now()}
	pa, pb := filepath.Join(dir, "a.json"), filepath.Join(dir, "b.json")
	if err := WriteWindowSession(pa, a); err != nil {
		t.Fatal(err)
	}
	if err := WriteWindowSession(pb, b); err != nil {
		t.Fatal(err)
	}
	got, err := ReadWindowSession(pa)
	if err != nil || got.WorkspaceID != "one" {
		t.Fatal(got, err)
	}
	info, _ := os.Stat(pa)
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode %o", info.Mode().Perm())
	}
	if err := os.WriteFile(filepath.Join(dir, "bad.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadWindowSession(filepath.Join(dir, "bad.json")); err == nil {
		t.Fatal("expected corrupt error")
	}
}
func TestMigrateDefaultWindowSessionDoesNotMutateState(t *testing.T) {
	state := types.AppState{Workspaces: []types.Workspace{{ID: "w", Path: "/workspace", Collections: []types.Collection{{ID: "c"}}}, {ID: "other", Collections: []types.Collection{{ID: "other-c"}}}}, OpenTabs: []types.OpenTab{{ID: "tab", CollectionID: "c"}, {ID: "foreign", CollectionID: "other-c"}}, ClosedTabs: []types.OpenTab{{ID: "closed", CollectionID: "c"}, {ID: "foreign-closed", CollectionID: "other-c"}}, ActiveTabID: "foreign", Preferences: types.Preferences{Layout: types.LayoutPreferences{ResponsePaneOrientation: "vertical"}}}
	session, err := MigrateDefaultWindowSession("s", "w", "", state)
	if err != nil || len(session.OpenTabs) != 1 || session.OpenTabs[0].ID != "tab" || len(session.ClosedTabs) != 1 || session.ClosedTabs[0].ID != "closed" || session.ActiveTabID != "tab" {
		t.Fatal(session, err)
	}
	session.OpenTabs[0].ID = "changed"
	if state.OpenTabs[0].ID != "tab" {
		t.Fatal("state mutated")
	}
}

func TestWindowSessionRejectsInvalidLayoutAndGeometry(t *testing.T) {
	base := WindowSession{Version: 1, ID: "s", WorkspaceID: "w"}
	for _, session := range []WindowSession{{Version: 1, ID: "s", WorkspaceID: "w", ResponsePaneOrientation: "diagonal"}, {Version: 1, ID: "s", WorkspaceID: "w", Geometry: WindowGeometry{X: 1}}, {Version: 1, ID: "s", WorkspaceID: "w", Geometry: WindowGeometry{Width: 500}}, {Version: 1, ID: "s", WorkspaceID: "w", Geometry: WindowGeometry{Width: 200, Height: 240}}} {
		if err := session.Validate(); err == nil {
			t.Fatalf("expected invalid %#v", session)
		}
	}
	if err := base.Validate(); err != nil {
		t.Fatal(err)
	}
	base.Geometry = WindowGeometry{X: -999, Y: -999, Width: 800, Height: 600}
	if err := base.Validate(); err != nil {
		t.Fatal(err)
	}
}
