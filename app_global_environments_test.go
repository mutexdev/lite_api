package main

import "testing"

// The three bound global-environment methods — SetActiveGlobalEnvironment,
// UpdateGlobalEnvironment and DeleteGlobalEnvironment — were all at 0%
// coverage. They are reachable only from the frontend, and a global environment
// holds the variables (including secrets) that every request in the workspace
// interpolates against, so choosing the wrong one is not a cosmetic error.

// globalEnvs builds a workspace with the named global environments and returns
// their ids in creation order. Creation makes each new environment ACTIVE, so
// after this the last one is the active one.
func globalEnvs(t *testing.T, app *App, workspaceID string, names ...string) []string {
	t.Helper()
	ids := make([]string, 0, len(names))
	for _, name := range names {
		state, err := app.CreateGlobalEnvironment(workspaceID, name)
		if err != nil {
			t.Fatalf("CreateGlobalEnvironment(%q): %v", name, err)
		}
		ids = append(ids, state.Workspaces[0].ActiveGlobalEnvironmentID)
	}
	return ids
}

func globalEnvNames(t *testing.T, app *App) []string {
	t.Helper()
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(state.Workspaces[0].GlobalEnvironments))
	for _, env := range state.Workspaces[0].GlobalEnvironments {
		names = append(names, env.Name)
	}
	return names
}

func activeGlobalEnv(t *testing.T, app *App) string {
	t.Helper()
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	return state.Workspaces[0].ActiveGlobalEnvironmentID
}

func firstWorkspaceID(t *testing.T, app *App) string {
	t.Helper()
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	return state.Workspaces[0].ID
}

func TestSetActiveGlobalEnvironmentSelectsAnExistingOne(t *testing.T) {
	app := newAppForTest(t)
	ws := firstWorkspaceID(t, app)
	ids := globalEnvs(t, app, ws, "Staging", "Production")

	if _, err := app.SetActiveGlobalEnvironment(ws, ids[0]); err != nil {
		t.Fatal(err)
	}
	if got := activeGlobalEnv(t, app); got != ids[0] {
		t.Errorf("active = %q, want %q", got, ids[0])
	}
}

// The empty id is a legitimate selection meaning "no global environment", and
// must not be mistaken for a missing argument: it is how the user turns global
// variables off without deleting the environment that holds them.
func TestSetActiveGlobalEnvironmentAcceptsTheEmptyIDAsNoSelection(t *testing.T) {
	app := newAppForTest(t)
	ws := firstWorkspaceID(t, app)
	globalEnvs(t, app, ws, "Staging")

	if _, err := app.SetActiveGlobalEnvironment(ws, ""); err != nil {
		t.Fatalf("selecting no environment should be allowed: %v", err)
	}
	if got := activeGlobalEnv(t, app); got != "" {
		t.Errorf("active = %q, want empty", got)
	}
}

// An id the workspace does not have must be REFUSED rather than stored. Storing
// it would leave every request interpolating against no environment at all,
// while the UI still showed a selection.
func TestSetActiveGlobalEnvironmentRefusesAnUnknownID(t *testing.T) {
	app := newAppForTest(t)
	ws := firstWorkspaceID(t, app)
	ids := globalEnvs(t, app, ws, "Staging")

	if _, err := app.SetActiveGlobalEnvironment(ws, "global-env-does-not-exist"); err == nil {
		t.Fatal("expected an error for an unknown environment id")
	}
	if got := activeGlobalEnv(t, app); got != ids[0] {
		t.Errorf("a refused selection changed the active environment to %q", got)
	}
}

// An id belonging to ANOTHER workspace is as wrong as one that exists nowhere,
// and the two are easy to conflate in an implementation that searches globally.
func TestSetActiveGlobalEnvironmentRefusesAnIDFromAnotherWorkspace(t *testing.T) {
	app := newAppForTest(t)
	first := firstWorkspaceID(t, app)
	firstIDs := globalEnvs(t, app, first, "Staging")

	state, err := app.CreateWorkspace("Second")
	if err != nil {
		t.Fatal(err)
	}
	var second string
	for _, ws := range state.Workspaces {
		if ws.ID != first {
			second = ws.ID
		}
	}
	if second == "" {
		t.Fatal("no second workspace was created")
	}

	if _, err := app.SetActiveGlobalEnvironment(second, firstIDs[0]); err == nil {
		t.Fatal("an environment id from another workspace was accepted")
	}
}

func TestUpdateGlobalEnvironmentRenames(t *testing.T) {
	app := newAppForTest(t)
	ws := firstWorkspaceID(t, app)
	ids := globalEnvs(t, app, ws, "Staging")

	if _, err := app.UpdateGlobalEnvironment(ws, ids[0], "Renamed", "#ff0000"); err != nil {
		t.Fatal(err)
	}
	if got := globalEnvNames(t, app); len(got) != 1 || got[0] != "Renamed" {
		t.Errorf("names = %v, want [Renamed]", got)
	}
}

// A BLANK field means "leave this one alone", not "clear it". The dialog sends
// both fields on every save, so treating blank as a value would wipe the colour
// whenever only the name was edited — and would let a stray save blank the name
// out entirely, leaving an unidentifiable entry in the picker.
func TestUpdateGlobalEnvironmentTreatsABlankFieldAsUnchanged(t *testing.T) {
	app := newAppForTest(t)
	ws := firstWorkspaceID(t, app)
	ids := globalEnvs(t, app, ws, "Staging")

	if _, err := app.UpdateGlobalEnvironment(ws, ids[0], "Renamed", "#ff0000"); err != nil {
		t.Fatal(err)
	}
	// Each half is checked BEFORE the next update runs. Checking only at the end
	// cannot see a blank colour being written, because the following
	// colour-only update sets it again and erases the damage — the first
	// version of this test was blind to exactly that.

	// Name only: the colour must survive.
	if _, err := app.UpdateGlobalEnvironment(ws, ids[0], "Renamed Again", "   "); err != nil {
		t.Fatal(err)
	}
	if env := onlyGlobalEnv(t, app); env.Color != "#ff0000" {
		t.Errorf("colour = %q, want %q — a blank colour overwrote it", env.Color, "#ff0000")
	} else if env.Name != "Renamed Again" {
		t.Errorf("name = %q, want %q", env.Name, "Renamed Again")
	}

	// Colour only: the name must survive.
	if _, err := app.UpdateGlobalEnvironment(ws, ids[0], "", "#00ff00"); err != nil {
		t.Fatal(err)
	}
	env := onlyGlobalEnv(t, app)
	if env.Name != "Renamed Again" {
		t.Errorf("name = %q, want %q — a blank name overwrote it", env.Name, "Renamed Again")
	}
	if env.Color != "#00ff00" {
		t.Errorf("colour = %q, want %q", env.Color, "#00ff00")
	}
}

func onlyGlobalEnv(t *testing.T, app *App) Environment {
	t.Helper()
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	envs := state.Workspaces[0].GlobalEnvironments
	if len(envs) != 1 {
		t.Fatalf("expected exactly one global environment, got %d", len(envs))
	}
	return envs[0]
}

func TestUpdateGlobalEnvironmentTrimsSurroundingSpace(t *testing.T) {
	app := newAppForTest(t)
	ws := firstWorkspaceID(t, app)
	ids := globalEnvs(t, app, ws, "Staging")

	if _, err := app.UpdateGlobalEnvironment(ws, ids[0], "  Padded  ", ""); err != nil {
		t.Fatal(err)
	}
	if got := globalEnvNames(t, app); got[0] != "Padded" {
		t.Errorf("name = %q, want %q", got[0], "Padded")
	}
}

func TestUpdateGlobalEnvironmentRefusesAnUnknownID(t *testing.T) {
	app := newAppForTest(t)
	ws := firstWorkspaceID(t, app)
	globalEnvs(t, app, ws, "Staging")

	if _, err := app.UpdateGlobalEnvironment(ws, "global-env-nope", "Renamed", ""); err == nil {
		t.Fatal("expected an error for an unknown environment id")
	}
	if got := globalEnvNames(t, app); got[0] != "Staging" {
		t.Errorf("a refused update renamed something: %v", got)
	}
}

func TestDeleteGlobalEnvironmentRemovesJustThatOne(t *testing.T) {
	app := newAppForTest(t)
	ws := firstWorkspaceID(t, app)
	ids := globalEnvs(t, app, ws, "Alpha", "Beta", "Gamma")

	if _, err := app.DeleteGlobalEnvironment(ws, ids[1]); err != nil {
		t.Fatal(err)
	}
	got := globalEnvNames(t, app)
	want := []string{"Alpha", "Gamma"}
	if len(got) != len(want) {
		t.Fatalf("names = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("names = %v, want %v — order was not preserved", got, want)
		}
	}
}

// Deleting the environment that is CURRENTLY SELECTED must leave a valid
// selection behind. Leaving the deleted id in place would point the workspace
// at an environment that no longer exists, so every request would silently
// resolve its global variables to nothing.
func TestDeleteGlobalEnvironmentPromotesAnotherWhenTheActiveOneGoes(t *testing.T) {
	app := newAppForTest(t)
	ws := firstWorkspaceID(t, app)
	ids := globalEnvs(t, app, ws, "Alpha", "Beta")
	if activeGlobalEnv(t, app) != ids[1] {
		t.Fatalf("precondition: the most recently created environment should be active")
	}

	if _, err := app.DeleteGlobalEnvironment(ws, ids[1]); err != nil {
		t.Fatal(err)
	}
	if got := activeGlobalEnv(t, app); got != ids[0] {
		t.Errorf("active = %q, want the remaining environment %q", got, ids[0])
	}
}

// Deleting the LAST one leaves no selection rather than a dangling id.
func TestDeleteGlobalEnvironmentClearsTheSelectionWhenNoneRemain(t *testing.T) {
	app := newAppForTest(t)
	ws := firstWorkspaceID(t, app)
	ids := globalEnvs(t, app, ws, "Only")

	if _, err := app.DeleteGlobalEnvironment(ws, ids[0]); err != nil {
		t.Fatal(err)
	}
	if got := activeGlobalEnv(t, app); got != "" {
		t.Errorf("active = %q, want empty once nothing remains", got)
	}
	if got := globalEnvNames(t, app); len(got) != 0 {
		t.Errorf("environments = %v, want none", got)
	}
}

// Deleting an environment that is NOT selected must not disturb the selection —
// the user's active environment changing as a side effect of tidying up an
// unrelated one would redirect every subsequent request.
// THREE environments, with the active one deliberately NOT the first of the
// survivors. With only two, "leave the selection alone" and "promote whatever
// is now first" produce the same id, and the test cannot tell them apart — the
// first version of this one was blind for exactly that reason.
func TestDeleteGlobalEnvironmentLeavesAnUnrelatedSelectionAlone(t *testing.T) {
	app := newAppForTest(t)
	ws := firstWorkspaceID(t, app)
	ids := globalEnvs(t, app, ws, "Alpha", "Beta", "Gamma")
	if _, err := app.SetActiveGlobalEnvironment(ws, ids[2]); err != nil {
		t.Fatal(err)
	}

	// Deleting Alpha leaves [Beta, Gamma]; Gamma must stay selected even though
	// Beta is now the first entry.
	if _, err := app.DeleteGlobalEnvironment(ws, ids[0]); err != nil {
		t.Fatal(err)
	}
	if got := activeGlobalEnv(t, app); got != ids[2] {
		t.Errorf("active = %q, want %q — deleting an unrelated environment moved the selection", got, ids[2])
	}
}

// The filter reuses the existing backing array (`ws.GlobalEnvironments[:0]`),
// which overwrites entries in place as it goes. A refused delete must leave the
// list exactly as it was, not half-rewritten.
func TestDeleteGlobalEnvironmentRefusesAnUnknownIDWithoutDisturbingTheList(t *testing.T) {
	app := newAppForTest(t)
	ws := firstWorkspaceID(t, app)
	ids := globalEnvs(t, app, ws, "Alpha", "Beta", "Gamma")
	before := activeGlobalEnv(t, app)

	if _, err := app.DeleteGlobalEnvironment(ws, "global-env-nope"); err == nil {
		t.Fatal("expected an error for an unknown environment id")
	}

	got := globalEnvNames(t, app)
	want := []string{"Alpha", "Beta", "Gamma"}
	if len(got) != len(want) {
		t.Fatalf("names = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("names = %v, want %v — a refused delete rewrote the list", got, want)
		}
	}
	if activeGlobalEnv(t, app) != before {
		t.Error("a refused delete moved the active environment")
	}
	// The ids must still resolve, which a rewritten backing array would break.
	for _, id := range ids {
		if _, err := app.SetActiveGlobalEnvironment(ws, id); err != nil {
			t.Errorf("environment %q no longer resolves after a refused delete: %v", id, err)
		}
	}
}

// Deleting twice must fail the second time rather than removing a neighbour —
// a double-click on the delete button is the obvious way to reach this.
func TestDeleteGlobalEnvironmentTwiceRemovesOnlyOne(t *testing.T) {
	app := newAppForTest(t)
	ws := firstWorkspaceID(t, app)
	ids := globalEnvs(t, app, ws, "Alpha", "Beta")

	if _, err := app.DeleteGlobalEnvironment(ws, ids[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := app.DeleteGlobalEnvironment(ws, ids[0]); err == nil {
		t.Fatal("the second delete of the same id should fail")
	}
	if got := globalEnvNames(t, app); len(got) != 1 || got[0] != "Beta" {
		t.Errorf("names = %v, want [Beta]", got)
	}
}

// All three refuse an unknown WORKSPACE, not just an unknown environment.
func TestGlobalEnvironmentMethodsRefuseAnUnknownWorkspace(t *testing.T) {
	app := newAppForTest(t)
	ws := firstWorkspaceID(t, app)
	ids := globalEnvs(t, app, ws, "Alpha")

	if _, err := app.SetActiveGlobalEnvironment("ws-nope", ids[0]); err == nil {
		t.Error("SetActiveGlobalEnvironment accepted an unknown workspace")
	}
	if _, err := app.UpdateGlobalEnvironment("ws-nope", ids[0], "X", ""); err == nil {
		t.Error("UpdateGlobalEnvironment accepted an unknown workspace")
	}
	if _, err := app.DeleteGlobalEnvironment("ws-nope", ids[0]); err == nil {
		t.Error("DeleteGlobalEnvironment accepted an unknown workspace")
	}
}
