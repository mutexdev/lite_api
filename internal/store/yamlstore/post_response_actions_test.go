// Loading post-response variable extraction, at 10% coverage.
//
// This is the "capture a token out of the response into a variable" feature. It
// filters hard — the action must be type set-variable, its phase must be one of
// two spellings, and it must name something — and every one of those filters
// DROPS SILENTLY. An action that fails a filter simply is not there: no error,
// no warning, and the next request sends an empty token while the user looks at
// their auth configuration.
package yamlstore

import (
	"testing"

	"github.com/mutexdev/lite_api/internal/types"
)

func action(over map[string]interface{}) map[string]interface{} {
	base := map[string]interface{}{
		"type":  "set-variable",
		"phase": "after-response",
		"name":  "token",
		"value": "res.body.token",
	}
	for k, v := range over {
		base[k] = v
	}
	return base
}

func TestPostResponseActionsReadNameValueAndFlags(t *testing.T) {
	got := ParsePostResponseActions([]interface{}{
		action(nil),
		action(map[string]interface{}{"name": "secretToken", "secret": true}),
	})
	if len(got) != 2 {
		t.Fatalf("got %d variables, want 2: %+v", len(got), got)
	}
	if got[0].Name != "token" {
		t.Errorf("name = %q, want token", got[0].Name)
	}
	if !got[1].Secret {
		t.Error("secret flag lost — the captured token would render unmasked")
	}
	if !got[0].Enabled {
		t.Error("an action with no enabled key must load enabled")
	}
}

// Both phase spellings are in the wild. Accepting only one drops every action
// written with the other, and the user sees no extraction and no error.
func TestPostResponseActionsAcceptBothPhaseSpellings(t *testing.T) {
	for _, phase := range []string{"after-response", "post-response", "After-Response", "  POST-RESPONSE  "} {
		got := ParsePostResponseActions([]interface{}{action(map[string]interface{}{"phase": phase})})
		if len(got) != 1 {
			t.Errorf("phase %q produced %d variables, want 1", phase, len(got))
		}
	}
}

func TestPostResponseActionsSkipOtherPhasesAndTypes(t *testing.T) {
	got := ParsePostResponseActions([]interface{}{
		action(map[string]interface{}{"phase": "before-request"}),
		action(map[string]interface{}{"type": "log"}),
		action(map[string]interface{}{"name": "  "}),
		"not a map",
		action(nil), // the only one that should survive
	})
	if len(got) != 1 {
		t.Fatalf("got %d variables, want 1: %+v", len(got), got)
	}
}

// The nested `variable` block overrides the top-level name and secret. Ignoring
// it captures into the WRONG VARIABLE — the request succeeds, and a different
// variable than the user named holds the token.
func TestPostResponseActionsHonourTheNestedVariableBlock(t *testing.T) {
	got := ParsePostResponseActions([]interface{}{
		action(map[string]interface{}{
			"name":     "outer",
			"secret":   false,
			"variable": map[string]interface{}{"name": "inner", "secret": true},
		}),
	})
	if len(got) != 1 {
		t.Fatalf("got %+v", got)
	}
	if got[0].Name != types.ResponseVariableRuntimeName("inner") {
		t.Errorf("name = %q; the nested variable block must win, or the token lands in the wrong variable", got[0].Name)
	}
	if !got[0].Secret {
		t.Error("the nested secret flag must win")
	}
}

// A nested block with no name must NOT blank the outer one.
func TestPostResponseActionsKeepTheOuterNameWhenTheNestedOneIsBlank(t *testing.T) {
	got := ParsePostResponseActions([]interface{}{
		action(map[string]interface{}{"name": "outer", "variable": map[string]interface{}{"secret": true}}),
	})
	if len(got) != 1 || got[0].Name != types.ResponseVariableRuntimeName("outer") {
		t.Fatalf("got %+v; a nested block without a name must not discard the outer one", got)
	}
	if !got[0].Secret {
		t.Error("the nested secret flag should still apply")
	}
}

// A selector expression replaces the value — that is how a JSONPath-style
// capture is written. Reading `value` instead captures the wrong thing.
func TestPostResponseActionsPreferTheSelectorExpression(t *testing.T) {
	got := ParsePostResponseActions([]interface{}{
		action(map[string]interface{}{
			"value":    "ignored",
			"selector": map[string]interface{}{"expression": "res.body.data.token"},
		}),
	})
	if len(got) != 1 {
		t.Fatalf("got %+v", got)
	}
	if got[0].Value != "res.body.data.token" {
		t.Errorf("value = %v; the selector expression must win over the plain value", got[0].Value)
	}
}

func TestPostResponseActionsRejectNonLists(t *testing.T) {
	if got := ParsePostResponseActions("not a list"); got != nil {
		t.Fatalf("got %+v, want nil", got)
	}
}

// The runtime name is NORMALISED: a leading ~ or @ marks the variable in the
// file format and must not survive into the name a script looks up.
//
// My first draft asserted against ResponseVariableRuntimeName("token") — the
// same function the loader calls — and for a plain name that function is the
// identity, so the assertion was vacuous. A control caught it: dropping the
// normalisation failed nothing. Using a name where the function actually does
// something is what makes the test discriminate.
func TestPostResponseActionsNormaliseThePrefixedName(t *testing.T) {
	for _, raw := range []string{"~token", "@token", "  ~token  "} {
		got := ParsePostResponseActions([]interface{}{action(map[string]interface{}{"name": raw})})
		if len(got) != 1 {
			t.Fatalf("%q produced %d variables", raw, len(got))
		}
		if got[0].Name != "token" {
			t.Errorf("name %q loaded as %q; the marker prefix must be stripped or the script cannot find the variable", raw, got[0].Name)
		}
	}
}
