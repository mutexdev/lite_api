package main

import (
	"testing"

	"github.com/wailsapp/wails/v2/pkg/menu"
	"github.com/wailsapp/wails/v2/pkg/menu/keys"
)

func TestNativeMenuCommandDefinitionsAreStableAndUnique(t *testing.T) {
	want := map[string]bool{
		"new-request": true, "save": true, "save-all": true, "close-tab": true,
		"reopen-tab": true, "import": true, "workspace-search": true, "command-palette": true, "toggle-sidebar": true,
		"toggle-devtools": true, "change-orientation": true, "send-or-start": true,
		"cancel-active": true, "open-runner": true, "new-collection": true,
		"open-collection": true, "open-environments": true, "open-git": true,
		"open-preferences": true,
		"open-network":     true, "open-cookies": true, "open-capabilities": true,
		"open-keyboard-shortcuts": true,
		"new-window":              true, "open-workspace-in-new-window": true,
	}
	seen := map[string]bool{}
	for _, definition := range nativeMenuCommandDefinitions() {
		if !want[definition.ID] {
			t.Fatalf("unexpected menu command definition: %#v", definition)
		}
		if seen[definition.ID] {
			t.Fatalf("duplicate menu command definition: %q", definition.ID)
		}
		if definition.Label == "" {
			t.Fatalf("menu command %q has no label", definition.ID)
		}
		seen[definition.ID] = true
	}
	if len(seen) != len(want) {
		t.Fatalf("menu command definitions are incomplete: %#v", seen)
	}
}

func TestNativeMenuSettingsBelongsToTheApplicationMenu(t *testing.T) {
	menuBar := buildNativeMenu(&App{})
	if nativeMenuCommandLabel(menuCommandOpenPreferences) != "Settings…" {
		t.Fatalf("settings label changed: %q", nativeMenuCommandLabel(menuCommandOpenPreferences))
	}

	var settingsItem *menu.MenuItem
	for _, item := range menuBar.Items {
		if item.SubMenu == nil {
			continue
		}
		switch item.Label {
		case "File":
			for _, child := range item.SubMenu.Items {
				if child.Label == nativeMenuCommandLabel(menuCommandOpenPreferences) || child.Label == "Preferences…" {
					t.Fatalf("Settings must be in the native application menu, not File: %q", child.Label)
				}
			}
		case "Help":
			for _, child := range item.SubMenu.Items {
				if child.Label == nativeMenuCommandLabel(menuCommandOpenPreferences) {
					settingsItem = child
				}
			}
		}
	}
	if settingsItem == nil || settingsItem.Click == nil {
		t.Fatal("Settings must start as a Wails callback item so macOS can move it without changing its action")
	}
	if settingsItem.Accelerator == nil || settingsItem.Accelerator.Key != "," || len(settingsItem.Accelerator.Modifiers) != 1 || settingsItem.Accelerator.Modifiers[0] != keys.CmdOrCtrlKey {
		t.Fatalf("Settings must retain Cmd+,: %#v", settingsItem.Accelerator)
	}
}

func TestNativeMenuAcceleratorsAndHelpCallback(t *testing.T) {
	menuBar := buildNativeMenu(&App{})
	var search, palette, orientation, devTools, help *menu.MenuItem
	for _, item := range menuBar.Items {
		switch item.Label {
		case "View":
			for _, child := range item.SubMenu.Items {
				switch child.Label {
				case nativeMenuCommandLabel(menuCommandWorkspaceSearch):
					search = child
				case nativeMenuCommandLabel(menuCommandCommandPalette):
					palette = child
				case nativeMenuCommandLabel(menuCommandChangeOrientation):
					orientation = child
				case nativeMenuCommandLabel(menuCommandToggleDevTools):
					devTools = child
				}
			}
		case "Help":
			for _, child := range item.SubMenu.Items {
				if child.Label == "LiteAPI Help" {
					help = child
				}
			}
		}
	}
	if search == nil || search.Accelerator == nil || search.Accelerator.Key != "k" || len(search.Accelerator.Modifiers) != 1 || search.Accelerator.Modifiers[0] != keys.CmdOrCtrlKey {
		t.Fatalf("workspace search does not own Cmd+K: %#v", search)
	}
	if palette == nil || palette.Accelerator == nil || palette.Accelerator.Key != "p" || len(palette.Accelerator.Modifiers) != 2 || palette.Accelerator.Modifiers[0] != keys.CmdOrCtrlKey || palette.Accelerator.Modifiers[1] != keys.ShiftKey {
		t.Fatalf("command palette does not use Cmd+Shift+P: %#v", palette)
	}
	if orientation == nil || orientation.Accelerator == nil || orientation.Accelerator.Key != "j" || len(orientation.Accelerator.Modifiers) != 1 || orientation.Accelerator.Modifiers[0] != keys.CmdOrCtrlKey {
		t.Fatalf("change orientation does not own Cmd+J: %#v", orientation)
	}
	if devTools == nil || devTools.Accelerator == nil || devTools.Accelerator.Key != "i" || len(devTools.Accelerator.Modifiers) != 2 || devTools.Accelerator.Modifiers[0] != keys.CmdOrCtrlKey || devTools.Accelerator.Modifiers[1] != keys.OptionOrAltKey {
		t.Fatalf("developer tools does not use Cmd+Option+I: %#v", devTools)
	}
	if help == nil || help.Click == nil {
		t.Fatalf("LiteAPI Help must dispatch a menu command: %#v", help)
	}
}

func TestNativeMenuIncludesMacStandardRolesAndConventionalSections(t *testing.T) {
	menuBar := buildNativeMenu(&App{})
	if menuBar == nil {
		t.Fatal("native menu was nil")
	}
	roles := map[menu.Role]bool{}
	labels := map[string]bool{}
	for _, item := range menuBar.Items {
		roles[item.Role] = true
		labels[item.Label] = true
	}
	for _, role := range []menu.Role{menu.AppMenuRole, menu.EditMenuRole, menu.WindowMenuRole} {
		if !roles[role] {
			t.Fatalf("native menu omitted standard role %d", role)
		}
	}
	for _, label := range []string{"File", "View", "Request", "Collection", "Environment", "Git", "Help"} {
		if !labels[label] {
			t.Fatalf("native menu omitted %q section", label)
		}
	}
}

func TestNativeMenuRelocatedDestinationsRemainDiscoverable(t *testing.T) {
	menuBar := buildNativeMenu(&App{})
	want := map[string][]string{
		"View": {
			nativeMenuCommandLabel(menuCommandWorkspaceSearch),
			nativeMenuCommandLabel(menuCommandCommandPalette),
			nativeMenuCommandLabel(menuCommandOpenNetwork),
			nativeMenuCommandLabel(menuCommandToggleDevTools),
		},
		"Request": {
			nativeMenuCommandLabel(menuCommandOpenCookies),
		},
		"Collection": {
			nativeMenuCommandLabel(menuCommandOpenRunner),
		},
		"Environment": {
			nativeMenuCommandLabel(menuCommandOpenEnvironments),
		},
		"Help": {
			nativeMenuCommandLabel(menuCommandOpenCapabilities),
			nativeMenuCommandLabel(menuCommandOpenKeybindings),
		},
	}

	for section, labels := range want {
		var submenu *menu.Menu
		for _, item := range menuBar.Items {
			if item.Label == section {
				submenu = item.SubMenu
				break
			}
		}
		if submenu == nil {
			t.Fatalf("native menu omitted %q submenu", section)
		}
		seen := map[string]bool{}
		for _, item := range submenu.Items {
			seen[item.Label] = true
		}
		for _, label := range labels {
			if !seen[label] {
				t.Errorf("native %s menu omitted relocated destination %q", section, label)
			}
		}
	}
}

func TestNativeMenuCommandsAreSafeBeforeStartup(t *testing.T) {
	app := &App{}
	app.emitNativeMenuCommand(menuCommandSendOrStart)
}
