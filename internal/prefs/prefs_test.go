package prefs

import (
	"testing"

	"github.com/mutexdev/lite_api/internal/mcpserver"
	"github.com/mutexdev/lite_api/internal/types"
)

// These normalisers run on every load of the preferences file, including files
// written by older builds and files a user has edited by hand. What they decide
// is what the app does when a setting is absent, zero, or nonsense — and a zero
// is not the same as an absent value, which is the distinction most of this
// package exists to make.

func TestBoolPtrValueDistinguishesUnsetFromFalse(t *testing.T) {
	if got := BoolPtrValue(nil, true); !got {
		t.Error("an UNSET flag should take the fallback, not read as false")
	}
	if got := BoolPtrValue(nil, false); got {
		t.Error("an unset flag took the wrong fallback")
	}
	if got := BoolPtrValue(BoolPtr(false), true); got {
		t.Error("an explicit false was overridden by the fallback; the user's choice was ignored")
	}
	if got := BoolPtrValue(BoolPtr(true), false); !got {
		t.Error("an explicit true was overridden by the fallback")
	}
}

// BoolPtr must return a pointer to a COPY. Returning the address of a shared
// variable would make every flag set from one call site alias the same bool.
func TestBoolPtrReturnsIndependentPointers(t *testing.T) {
	first, second := BoolPtr(true), BoolPtr(true)
	if first == second {
		t.Fatal("two calls returned the same pointer")
	}
	*second = false
	if !*first {
		t.Error("writing through one pointer changed the other")
	}
}

// TLS verification defaults ON when the setting is absent. A file written
// before the flag existed must not be read as "verification off" — that would
// silently disable certificate checking for every request in it.
func TestRequestPreferencesDefaultToVerifyingTLS(t *testing.T) {
	got := NormalizeRequest(types.RequestPreferences{}, false)
	if got.SSLVerification == nil {
		t.Fatal("SSLVerification is still nil after normalisation")
	}
	if !*got.SSLVerification {
		t.Error("an absent SSLVerification normalised to OFF; older files would stop verifying certificates")
	}
}

// An explicit false survives. The default above must not overwrite a user who
// deliberately turned verification off for a self-signed host.
func TestRequestPreferencesKeepAnExplicitVerificationChoice(t *testing.T) {
	got := NormalizeRequest(types.RequestPreferences{SSLVerification: BoolPtr(false)}, false)
	if got.SSLVerification == nil || *got.SSLVerification {
		t.Error("an explicit SSLVerification=false was overwritten by the default")
	}
}

// The legacy cookie flag is carried into the new field only when the new one is
// absent, which is what makes an upgrade preserve the old behaviour.
func TestRequestPreferencesInheritTheLegacyCookieSetting(t *testing.T) {
	for _, legacy := range []bool{true, false} {
		got := NormalizeRequest(types.RequestPreferences{}, legacy)
		if got.StoreCookies == nil {
			t.Fatal("StoreCookies is still nil")
		}
		if *got.StoreCookies != legacy {
			t.Errorf("legacy=%v produced StoreCookies=%v", legacy, *got.StoreCookies)
		}
	}
	// An explicit new-style value wins over the legacy one.
	got := NormalizeRequest(types.RequestPreferences{StoreCookies: BoolPtr(false)}, true)
	if got.StoreCookies == nil || *got.StoreCookies {
		t.Error("the legacy flag overrode an explicit StoreCookies=false")
	}
}

// Autosave: ZERO means unset and takes the default, while any value below the
// floor is raised to it. An interval of a few milliseconds would rewrite the
// collection continuously and keep the file watcher awake.
func TestAutoSaveIntervalHasADefaultAndAFloor(t *testing.T) {
	cases := []struct {
		name     string
		interval int
		want     int
	}{
		{"zero means unset", 0, 1000},
		{"below the floor is raised", 1, 500},
		{"just below the floor", 499, 500},
		{"at the floor is kept", 500, 500},
		{"above the floor is kept", 5000, 5000},
	}
	for _, testCase := range cases {
		got := NormalizeAutoSave(types.AutoSavePreferences{Interval: testCase.interval}, false)
		if got.Interval != testCase.want {
			t.Errorf("%s: interval %d normalised to %d, want %d",
				testCase.name, testCase.interval, got.Interval, testCase.want)
		}
	}
}

// The legacy autosave switch can only turn the feature ON. Letting it turn the
// feature off would undo a user who had enabled it in the new settings.
func TestTheLegacyAutoSaveSwitchOnlyEnables(t *testing.T) {
	if got := NormalizeAutoSave(types.AutoSavePreferences{}, true); !got.Enabled {
		t.Error("the legacy switch did not enable autosave")
	}
	if got := NormalizeAutoSave(types.AutoSavePreferences{Enabled: true}, false); !got.Enabled {
		t.Error("an explicitly enabled autosave was turned off by the legacy switch being false")
	}
}

// An unrecognised orientation falls back rather than reaching the layout code.
// The value is read straight into a CSS flex-direction, where a stray string
// renders the panes stacked in neither direction.
func TestLayoutOrientationFallsBackToAKnownValue(t *testing.T) {
	for _, value := range []string{"", "sideways", "HORIZONTAL", "vertical-ish"} {
		got := NormalizeLayout(types.LayoutPreferences{ResponsePaneOrientation: value})
		if got.ResponsePaneOrientation != "horizontal" {
			t.Errorf("%q normalised to %q, want horizontal", value, got.ResponsePaneOrientation)
		}
	}
	for _, value := range []string{"horizontal", "vertical"} {
		got := NormalizeLayout(types.LayoutPreferences{ResponsePaneOrientation: value})
		if got.ResponsePaneOrientation != value {
			t.Errorf("the valid value %q was changed to %q", value, got.ResponsePaneOrientation)
		}
	}
}

// Normalising twice must equal normalising once. These run on every load, so a
// normaliser that kept changing its own output would rewrite the preferences
// file on every start and make it a permanent diff.
func TestNormalizeIsIdempotent(t *testing.T) {
	once := Normalize(types.Preferences{})
	twice := Normalize(once)

	if once.Theme != twice.Theme {
		t.Errorf("Theme changed on the second pass: %q then %q", once.Theme, twice.Theme)
	}
	if once.AutoSave.Interval != twice.AutoSave.Interval {
		t.Errorf("AutoSave.Interval changed: %d then %d", once.AutoSave.Interval, twice.AutoSave.Interval)
	}
	if once.Layout.ResponsePaneOrientation != twice.Layout.ResponsePaneOrientation {
		t.Errorf("orientation changed: %q then %q",
			once.Layout.ResponsePaneOrientation, twice.Layout.ResponsePaneOrientation)
	}
	if BoolPtrValue(once.Request.SSLVerification, false) != BoolPtrValue(twice.Request.SSLVerification, false) {
		t.Error("SSLVerification changed on the second pass")
	}
}

// A zero-value Preferences is what a brand new install and a corrupted file
// both produce. Normalising it must yield something usable rather than
// something that merely parses.
func TestNormalizeTurnsAZeroValueIntoUsableDefaults(t *testing.T) {
	got := Normalize(types.Preferences{})

	if got.Layout.ResponsePaneOrientation == "" {
		t.Error("orientation is empty, which reaches CSS as no direction at all")
	}
	if got.AutoSave.Interval < 500 {
		t.Errorf("autosave interval %d is below the floor", got.AutoSave.Interval)
	}
	if got.Request.SSLVerification == nil {
		t.Error("SSLVerification is nil, so every caller must guess the default")
	}
}

// The MCP port is the pairing contract: the user copies a `claude mcp add`
// command with the port written into a URL, so the port has to be the same on
// the next launch as it was when they copied it. Zero is the dangerous value —
// net.Listen accepts it and hands back a different ephemeral port every time,
// which would break the pasted command silently rather than loudly.
func TestMCPPortNeverNormalizesToAnEphemeralZero(t *testing.T) {
	cases := []struct {
		name string
		port int
		want int
	}{
		{"zero would be an ephemeral port", 0, mcpserver.DefaultPort},
		{"negative is not a port", -1, mcpserver.DefaultPort},
		{"above the 16-bit range", 65536, mcpserver.DefaultPort},
		{"far above the range", 1 << 20, mcpserver.DefaultPort},
		{"the lowest real port is kept", 1, 1},
		{"the highest real port is kept", 65535, 65535},
		{"a chosen port is kept", 40000, 40000},
	}
	for _, testCase := range cases {
		got := NormalizeMCP(types.MCPPreferences{Port: testCase.port})
		if got.Port != testCase.want {
			t.Errorf("%s: port %d normalised to %d, want %d",
				testCase.name, testCase.port, got.Port, testCase.want)
		}
	}
}

// The two switches are the user's and must survive normalisation untouched.
// Coercing the port must not become an excuse to reset anything else.
func TestMCPNormalizationLeavesTheSwitchesAlone(t *testing.T) {
	got := NormalizeMCP(types.MCPPreferences{Enabled: true, WriteTierEnabled: true, Port: 0})
	if !got.Enabled {
		t.Error("Enabled was cleared by normalisation")
	}
	if !got.WriteTierEnabled {
		t.Error("WriteTierEnabled was cleared; the write tier would silently turn itself off")
	}
	if got.Port != mcpserver.DefaultPort {
		t.Errorf("port %d, want the default", got.Port)
	}
}

// Normalize must reach MCP too. A normaliser that exists but is never called
// from the entry point is the same as no normaliser at all — and the zero-value
// Preferences below is what a fresh install and a corrupted file both produce.
func TestNormalizeSettlesTheMCPPreferences(t *testing.T) {
	got := Normalize(types.Preferences{})
	if got.MCP.Port != mcpserver.DefaultPort {
		t.Errorf("MCP port is %d after Normalize; NormalizeMCP is not wired into Normalize", got.MCP.Port)
	}
	if got.MCP.Enabled {
		t.Error("MCP defaulted to ENABLED; the server must be opt-in")
	}
	if got.MCP.WriteTierEnabled {
		t.Error("the MCP write tier defaulted to on; it must be an explicit Settings action")
	}
	// Idempotence, checked here rather than only in the shared test above,
	// because a second pass over an already-valid port is exactly where an
	// off-by-one in the range check would show.
	if twice := Normalize(got); twice.MCP != got.MCP {
		t.Errorf("a second Normalize changed the MCP preferences: %+v then %+v", got.MCP, twice.MCP)
	}
}

// The devtools defaults moved here with the normaliser that applies them, and
// the column widths are read by name from the application's tests. Both must
// stay non-empty, since an empty width list renders a table with no columns.
func TestDevToolsColumnWidthsAreNonEmpty(t *testing.T) {
	if len(DevToolsNetworkDefaultColumnWidths) == 0 {
		t.Fatal("no default column widths")
	}
	for index, width := range DevToolsNetworkDefaultColumnWidths {
		if width <= 0 {
			t.Errorf("column %d has width %d", index, width)
		}
	}
}
