// The execution overlay: variable mutations held in memory instead of written
// to state.
//
// The overlay exists so an agent-initiated run can keep its within-run
// semantics — a flow's step 1 bru.setVar reaching step 3 — without any of those
// values landing in AppState, where the next run would read them back as
// definition state. Two functions make that possible, and the property that has
// to hold between them is an EQUIVALENCE, not a resemblance: a context with the
// overlay applied must resolve every variable to exactly what it would have
// resolved to had the same deltas gone through ApplyScriptVariableContextToState
// and been read back out of the collection. That is what the central test here
// asserts, by running both routes and comparing the resolved maps — so a change
// to either the persisted layout or the scope precedence fails here rather than
// as a variable that quietly reads differently under an agent run.
package scripting

import (
	"reflect"
	"testing"

	"github.com/mutexdev/lite_api/internal/types"
)

func overlayVariable(name string, value interface{}) types.Variable {
	return types.Variable{ID: "var-" + name, Name: name, Value: value, Type: "string", DataType: "string", Enabled: true}
}

func overlayCollection() *types.Collection {
	return &types.Collection{
		ID:   "collection-1",
		Name: "Payments",
		Variables: []types.Variable{
			overlayVariable("host", "collection.example"),
			overlayVariable("fromCollection", "c"),
		},
		RuntimeVariables: []types.Variable{overlayVariable("fromRuntime", "r")},
		Environments: []types.Environment{{
			ID:   "env-prod",
			Name: "Production",
			Variables: []types.Variable{
				overlayVariable("host", "env.example"),
				overlayVariable("fromEnv", "e"),
			},
		}},
	}
}

func overlayWorkspace() *types.Workspace {
	return &types.Workspace{
		ID:                        "workspace-1",
		Name:                      "Work",
		ActiveGlobalEnvironmentID: "global-1",
		GlobalEnvironments: []types.Environment{{
			ID:   "global-1",
			Name: "Team",
			Variables: []types.Variable{
				overlayVariable("host", "global.example"),
				overlayVariable("fromGlobal", "g"),
			},
		}},
	}
}

func overlayItem() types.RequestItem {
	return types.RequestItem{
		ID:   "request-1",
		Name: "Charge card",
		Vars: types.RequestVars{Req: []types.Variable{overlayVariable("fromRequest", "q")}},
	}
}

// mutateEveryScope is what a script does to a live context: it writes into the
// scope maps and marks them dirty. bru.setVar hits Runtime, bru.setEnvVar hits
// Env, and so on.
//
// It is applied to a context BUILT FROM THE DEFINITIONS, never to a bare one,
// because that is the only shape the persisted path is ever handed:
// mergeScriptVariablesIntoSlice drops an enabled stored variable that the map
// does not mention, and the map always mentions it — the context was seeded
// from the same definitions. A fixture that skipped the seeding would "prove"
// an equivalence against a persisted route that had silently deleted half the
// collection.
func mutateEveryScope(ctx *VariableContext) {
	ctx.Runtime["host"] = "runtime.example"
	ctx.Runtime["token"] = "from-setVar"
	ctx.RuntimeDirty = true
	ctx.Env["host"] = "envDelta.example"
	ctx.Env["envNew"] = "en"
	ctx.EnvDirty = true
	ctx.Global["host"] = "globalDelta.example"
	ctx.Global["globalNew"] = "gn"
	ctx.GlobalDirty = true
	ctx.Collection["host"] = "collectionDelta.example"
	ctx.Collection["collectionNew"] = "cn"
	ctx.CollectionDirty = true
	ctx.Recompute()
}

// The equivalence, end to end. One script's mutations, two routes out of the
// send: persist-then-reread, and hold-then-reapply. The resolved variable maps
// must be identical, key for key.
//
// Every scope the persisted path can write is exercised at once, and each one
// collides with the others on `host`, so the comparison is sensitive to
// precedence and not just to presence.
func TestOverlayResolvesExactlyAsPersistedValuesWould(t *testing.T) {
	// Route A: what the UI does today — the script's context goes into state,
	// and the next send builds a fresh context out of the state that was
	// written.
	persistedCollection := overlayCollection()
	persistedWorkspace := overlayWorkspace()
	mutated := NewScriptVariableContext(
		ActiveGlobalEnvironmentsForWorkspace(*persistedWorkspace),
		persistedCollection, "env-prod", overlayItem(), nil,
	)
	mutateEveryScope(mutated)

	state := &types.AppState{Workspaces: []types.Workspace{*persistedWorkspace}}
	ApplyScriptVariableContextToState(state, persistedWorkspace, persistedCollection, "env-prod", mutated)
	persisted := NewScriptVariableContext(
		ActiveGlobalEnvironmentsForWorkspace(*persistedWorkspace),
		persistedCollection, "env-prod", overlayItem(), nil,
	)

	// Route B: what an MCP run does — the same script's mutations are taken out
	// of the context and laid over a context built from UNTOUCHED definitions.
	held := DeltasFromContext(mutated)
	overlaid := NewScriptVariableContext(
		ActiveGlobalEnvironmentsForWorkspace(*overlayWorkspace()),
		overlayCollection(), "env-prod", overlayItem(), nil,
	)
	ApplyRunOverlayToContext(overlaid, held)

	if !reflect.DeepEqual(overlaid.Combined, persisted.Combined) {
		t.Fatalf("the overlay resolves differently from the persisted path\n overlay:   %v\n persisted: %v",
			overlaid.Combined, persisted.Combined)
	}

	// And spelled out, so a failure says which precedence broke rather than
	// only that two maps differ. Runtime is the top of the four, which is why a
	// bru.setVar survives an environment carrying the same name.
	if overlaid.Combined["host"] != "runtime.example" {
		t.Errorf("host = %q; a runtime delta must outrank the environment, collection and global ones",
			overlaid.Combined["host"])
	}
	for name, want := range map[string]string{
		"token":          "from-setVar",
		"envNew":         "en",
		"globalNew":      "gn",
		"collectionNew":  "cn",
		"fromEnv":        "e",
		"fromGlobal":     "g",
		"fromCollection": "c",
		"fromRuntime":    "r",
		"fromRequest":    "q",
	} {
		if overlaid.Combined[name] != want {
			t.Errorf("%s = %q, want %q", name, overlaid.Combined[name], want)
		}
	}
}

// The scopes an overlay does NOT reach still outrank it, exactly as they outrank
// a persisted value. A request-level var beats a persisted environment value
// today; it must beat an environment delta too, or an agent run would resolve a
// request var differently from a UI send.
func TestOverlayDoesNotOutrankTheScopesAboveIt(t *testing.T) {
	item := overlayItem()
	item.Vars.Req = append(item.Vars.Req, overlayVariable("host", "request.example"))

	ctx := NewScriptVariableContext(
		ActiveGlobalEnvironmentsForWorkspace(*overlayWorkspace()),
		overlayCollection(), "env-prod", item, nil,
	)
	ApplyRunOverlayToContext(ctx, RunVariableDeltas{Env: map[string]interface{}{"host": "envDelta.example"}})

	if ctx.Combined["host"] != "request.example" {
		t.Fatalf("host = %q; an environment delta must not climb above a request var", ctx.Combined["host"])
	}

	// A runtime delta does outrank it, because ctx.Runtime is layered above
	// ctx.Request — the same order a persisted runtime variable enjoys.
	ApplyRunOverlayToContext(ctx, RunVariableDeltas{Runtime: map[string]interface{}{"host": "runtime.example"}})
	if ctx.Combined["host"] != "runtime.example" {
		t.Fatalf("host = %q; a runtime delta must outrank a request var", ctx.Combined["host"])
	}
}

// The round trip, in the shape a flow uses it: apply what the run holds, let the
// script dirty something, take the whole lot back out for the next step.
func TestOverlayRoundTripsThroughAContext(t *testing.T) {
	first := NewScriptVariableContext(
		ActiveGlobalEnvironmentsForWorkspace(*overlayWorkspace()),
		overlayCollection(), "env-prod", overlayItem(), nil,
	)
	if got := DeltasFromContext(first); !got.IsEmpty() {
		t.Fatalf("a context nothing has touched yielded deltas: %+v", got)
	}

	// Step 1 sets a variable, the way bru.setVar does.
	first.Runtime["stepOneToken"] = "abc"
	first.RuntimeDirty = true
	first.Recompute()

	held := DeltasFromContext(first)
	if held.Runtime["stepOneToken"] != "abc" {
		t.Fatalf("the dirtied runtime scope did not come out: %+v", held)
	}
	if held.Env != nil || held.Global != nil || held.Collection != nil {
		t.Fatalf("clean scopes must yield nil, not empty maps: %+v", held)
	}

	// Step 2 builds its own fresh context and sees step 1's value.
	second := NewScriptVariableContext(
		ActiveGlobalEnvironmentsForWorkspace(*overlayWorkspace()),
		overlayCollection(), "env-prod", overlayItem(), nil,
	)
	if second.Combined["stepOneToken"] != "" {
		t.Fatal("a fresh context already knows step 1's value; the fixture is not isolated")
	}
	ApplyRunOverlayToContext(second, held)
	if second.Combined["stepOneToken"] != "abc" {
		t.Fatalf("stepOneToken = %q; step 1's setVar must be visible to a later step",
			second.Combined["stepOneToken"])
	}

	// Step 2 adds its own, and BOTH must reach step 3. This is the property the
	// dirty flag on an applied overlay buys: without it the carried value would
	// be dropped on the way out of every step that did not re-set it.
	second.Runtime["stepTwoToken"] = "def"
	second.Recompute()
	grown := DeltasFromContext(second)

	third := NewScriptVariableContext(
		ActiveGlobalEnvironmentsForWorkspace(*overlayWorkspace()),
		overlayCollection(), "env-prod", overlayItem(), nil,
	)
	ApplyRunOverlayToContext(third, grown)
	if third.Combined["stepOneToken"] != "abc" || third.Combined["stepTwoToken"] != "def" {
		t.Fatalf("step 3 saw stepOne=%q stepTwo=%q; both earlier steps must survive",
			third.Combined["stepOneToken"], third.Combined["stepTwoToken"])
	}
}

// The overlay lives in the caller's hands, so what comes out of a context must
// not still be that context's map — the context is about to be mutated further
// or thrown away.
func TestDeltasFromContextReturnsCopies(t *testing.T) {
	ctx := NewFlatScriptVariableContext(map[string]string{"a": "1"})
	ctx.RuntimeDirty = true

	held := DeltasFromContext(ctx)
	ctx.Runtime["a"] = "mutated"

	if held.Runtime["a"] != "1" {
		t.Fatalf("held delta = %v; the caller's copy tracked a later mutation of the context",
			held.Runtime["a"])
	}
}

// The nil and empty cases, because the send path will call these on every send
// including the ones with nothing to carry.
func TestOverlayHelpersHandleTheEmptyCases(t *testing.T) {
	if got := DeltasFromContext(nil); !got.IsEmpty() {
		t.Fatalf("DeltasFromContext(nil) = %+v, want empty", got)
	}
	ApplyRunOverlayToContext(nil, RunVariableDeltas{Runtime: map[string]interface{}{"a": "1"}})

	ctx := NewFlatScriptVariableContext(map[string]string{"a": "1"})
	ApplyRunOverlayToContext(ctx, RunVariableDeltas{})
	if ctx.RuntimeDirty || ctx.EnvDirty || ctx.GlobalDirty || ctx.CollectionDirty {
		t.Fatal("an empty overlay dirtied a scope; a run that changed nothing must carry nothing")
	}
	if ctx.Combined["a"] != "1" {
		t.Fatalf("an empty overlay disturbed the context: %v", ctx.Combined)
	}
}

// A clean scope carries nothing even when it holds values, because
// ApplyScriptVariableContextToState writes nothing for a clean scope either.
// Carrying them would make every run's overlay a copy of the whole environment.
func TestDeltasFromContextIgnoresCleanScopes(t *testing.T) {
	ctx := NewScriptVariableContext(
		ActiveGlobalEnvironmentsForWorkspace(*overlayWorkspace()),
		overlayCollection(), "env-prod", overlayItem(), nil,
	)
	if got := DeltasFromContext(ctx); !got.IsEmpty() {
		t.Fatalf("a context that only read its definitions yielded %+v", got)
	}

	ctx.EnvDirty = true
	got := DeltasFromContext(ctx)
	if got.Env["fromEnv"] != "e" {
		t.Fatalf("the dirty environment scope did not come out whole: %+v", got.Env)
	}
	if got.Runtime != nil || got.Global != nil || got.Collection != nil {
		t.Fatalf("scopes that stayed clean came out anyway: %+v", got)
	}
}
