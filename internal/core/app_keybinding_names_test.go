package core

import (
	"os"
	"regexp"
	"testing"
)

// knownKeyBindingName was at 0%, and testing it in isolation would have been
// almost worthless: it is a six-case switch returning strings, and asserting
// those strings against themselves proves nothing.
//
// What makes it worth testing is that the SAME mapping exists twice, in two
// languages. Go uses it to normalise persisted preferences
// (app_prefs_normalize.go: `next.Name = knownKeyBindingName(action)`), while
// frontend/src/lib/keybindings.ts carries its own name for each action in the
// table the settings screen renders.
//
// Nothing connected them. If one side is renamed, a user's saved binding
// carries one label and the shortcut table shows another, for the same key.
// Neither build would notice: each side type-checks perfectly against itself.
//
// This is the same shape as the native-menu contract test, and it is read from
// the TypeScript source for the same reason — the alternative is a third copy
// of the list, which is the problem rather than the fix.

// Anchored on the full entry shape, not just `name:`. A looser `(\w+): \{[^}]*`
// also matches the `bindings: {` that OPENS each group, and its span then
// swallows the entries inside it — which silently dropped three of the six
// actions under test and reported them as missing from the frontend.
var keybindingEntryPattern = regexp.MustCompile(
	`(\w+): \{ mac: '[^']*', windows: '[^']*', name: '([^']*)'`)

func frontendKeyBindingNames(t *testing.T) map[string]string {
	t.Helper()
	source, err := os.ReadFile(repoPath(t, "frontend", "src", "lib", "keybindings.ts"))
	if err != nil {
		t.Fatalf("reading the frontend keybinding table: %v", err)
	}
	names := map[string]string{}
	for _, match := range keybindingEntryPattern.FindAllStringSubmatch(string(source), -1) {
		names[match[1]] = match[2]
	}
	// A regex that silently matched nothing would make every assertion below
	// vacuous, so the parse asserts its own result first.
	if len(names) < 10 {
		t.Fatalf("only %d bindings parsed out of keybindings.ts; the pattern has stopped matching", len(names))
	}
	// The group keys are not bindings. Seeing one means the pattern has started
	// matching a container instead of an entry, which is exactly how it failed
	// the first time, and a count alone did not catch it.
	for _, container := range []string{"bindings", "heading"} {
		if name, present := names[container]; present {
			t.Fatalf("the pattern matched the %q container as a binding (name %q)", container, name)
		}
	}
	return names
}

// Every action Go names must carry the SAME name on the frontend.
func TestKeyBindingNamesAgreeWithTheFrontend(t *testing.T) {
	frontend := frontendKeyBindingNames(t)

	// The actions knownKeyBindingName answers for. Listed here rather than
	// derived, because the point is to check the Go switch, and deriving the
	// list from that switch would compare it with itself.
	for _, action := range []string{
		"sendRequest",
		"globalSearch",
		"commandPalette",
		"sidebarSearch",
		"newRequest",
		"save",
	} {
		goName := knownKeyBindingName(action)
		if goName == action {
			t.Errorf("knownKeyBindingName(%q) fell through to its default, so Go has lost this action", action)
			continue
		}
		frontendName, present := frontend[action]
		if !present {
			t.Errorf("Go names %q as %q but the frontend table has no such action", action, goName)
			continue
		}
		if goName != frontendName {
			t.Errorf("action %q: Go says %q, the frontend says %q", action, goName, frontendName)
		}
	}
}

// An action Go has no case for falls through to the raw action id. That is the
// deliberate behaviour for the many bindings the frontend owns alone — Go must
// not invent a label for them — and it is what makes the check above meaningful
// rather than trivially true.
func TestKeyBindingNameFallsThroughForUnknownActions(t *testing.T) {
	for _, action := range []string{"copyItem", "collapseSidebar", "somethingInvented", ""} {
		if got := knownKeyBindingName(action); got != action {
			t.Errorf("knownKeyBindingName(%q) = %q, want the action returned unchanged", action, got)
		}
	}
}

// The matching is exact, not case-insensitive or trimmed: the value is written
// into persisted preferences, and a near-miss would show up as a second entry
// rather than as the same one spelled differently.
func TestKeyBindingNameMatchingIsExact(t *testing.T) {
	for _, action := range []string{"SendRequest", "sendrequest", " sendRequest", "sendRequest "} {
		if got := knownKeyBindingName(action); got != action {
			t.Errorf("knownKeyBindingName(%q) = %q, want no match for a near-miss", action, got)
		}
	}
}
