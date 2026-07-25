package main

import (
	"github.com/wailsapp/wails/v2/pkg/menu"
	"github.com/wailsapp/wails/v2/pkg/menu/keys"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

const nativeMenuCommandEvent = "liteapi:menu-command"

const (
	menuCommandNewRequest          = "new-request"
	menuCommandSave                = "save"
	menuCommandSaveAll             = "save-all"
	menuCommandCloseTab            = "close-tab"
	menuCommandReopenTab           = "reopen-tab"
	menuCommandImport              = "import"
	menuCommandWorkspaceSearch     = "workspace-search"
	menuCommandCommandPalette      = "command-palette"
	menuCommandToggleSidebar       = "toggle-sidebar"
	menuCommandToggleDevTools      = "toggle-devtools"
	menuCommandChangeOrientation   = "change-orientation"
	menuCommandSendOrStart         = "send-or-start"
	menuCommandCancelActive        = "cancel-active"
	menuCommandOpenRunner          = "open-runner"
	menuCommandNewCollection       = "new-collection"
	menuCommandOpenCollection      = "open-collection"
	menuCommandOpenEnvironments    = "open-environments"
	menuCommandOpenGit             = "open-git"
	menuCommandOpenPreferences     = "open-preferences"
	menuCommandOpenNetwork         = "open-network"
	menuCommandOpenCookies         = "open-cookies"
	menuCommandOpenCapabilities    = "open-capabilities"
	menuCommandOpenKeybindings     = "open-keyboard-shortcuts"
	menuCommandNewWindow           = "new-window"
	menuCommandOpenWorkspaceWindow = "open-workspace-in-new-window"
)

type nativeMenuCommandDefinition struct {
	ID    string
	Label string
}

func nativeMenuCommandDefinitions() []nativeMenuCommandDefinition {
	return []nativeMenuCommandDefinition{
		{ID: menuCommandNewRequest, Label: "New Request"},
		{ID: menuCommandSave, Label: "Save"},
		{ID: menuCommandSaveAll, Label: "Save All"},
		{ID: menuCommandCloseTab, Label: "Close Tab"},
		{ID: menuCommandReopenTab, Label: "Reopen Closed Tab"},
		{ID: menuCommandImport, Label: "Import…"},
		{ID: menuCommandWorkspaceSearch, Label: "Search Workspace…"},
		{ID: menuCommandCommandPalette, Label: "Command Palette…"},
		{ID: menuCommandToggleSidebar, Label: "Toggle Sidebar"},
		{ID: menuCommandToggleDevTools, Label: "Toggle Developer Tools"},
		{ID: menuCommandChangeOrientation, Label: "Change Response Orientation"},
		{ID: menuCommandSendOrStart, Label: "Send or Start"},
		{ID: menuCommandCancelActive, Label: "Cancel Active Request"},
		{ID: menuCommandOpenRunner, Label: "Open Runner"},
		{ID: menuCommandNewCollection, Label: "New Collection…"},
		{ID: menuCommandOpenCollection, Label: "Open Collection…"},
		{ID: menuCommandOpenEnvironments, Label: "Manage Environments…"},
		{ID: menuCommandOpenGit, Label: "Open Git"},
		{ID: menuCommandOpenPreferences, Label: "Settings…"},
		{ID: menuCommandOpenNetwork, Label: "Network Log"},
		{ID: menuCommandOpenCookies, Label: "Cookie Jar"},
		{ID: menuCommandOpenCapabilities, Label: "Capabilities"},
		{ID: menuCommandOpenKeybindings, Label: "Keyboard Shortcuts"},
		{ID: menuCommandNewWindow, Label: "New Window"},
		{ID: menuCommandOpenWorkspaceWindow, Label: "Open Workspace in New Window…"},
	}
}

func nativeMenuCommandLabel(id string) string {
	for _, definition := range nativeMenuCommandDefinitions() {
		if definition.ID == id {
			return definition.Label
		}
	}
	return id
}

func (a *App) emitNativeMenuCommand(command string) {
	if a == nil || a.ctx == nil {
		return
	}
	wailsruntime.EventsEmit(a.ctx, nativeMenuCommandEvent, command)
}

func (a *App) menuCommand(command string, accelerator *keys.Accelerator) *menu.MenuItem {
	return menu.Text(nativeMenuCommandLabel(command), accelerator, func(_ *menu.CallbackData) {
		a.emitNativeMenuCommand(command)
	})
}

func buildNativeMenu(app *App) *menu.Menu {
	root := menu.NewMenu()
	root.Append(menu.AppMenu())

	file := menu.NewMenu()
	file.Append(app.menuCommand(menuCommandNewRequest, keys.CmdOrCtrl("n")))
	file.Append(app.menuCommand(menuCommandNewWindow, keys.Combo("n", keys.CmdOrCtrlKey, keys.ShiftKey)))
	file.Append(app.menuCommand(menuCommandOpenWorkspaceWindow, nil))
	file.Append(app.menuCommand(menuCommandNewCollection, nil))
	file.Append(app.menuCommand(menuCommandOpenCollection, keys.CmdOrCtrl("o")))
	file.Append(app.menuCommand(menuCommandImport, nil))
	file.AddSeparator()
	file.Append(app.menuCommand(menuCommandSave, keys.CmdOrCtrl("s")))
	file.Append(app.menuCommand(menuCommandSaveAll, keys.Combo("s", keys.CmdOrCtrlKey, keys.ShiftKey)))
	file.AddSeparator()
	file.Append(app.menuCommand(menuCommandCloseTab, keys.CmdOrCtrl("w")))
	file.Append(app.menuCommand(menuCommandReopenTab, keys.Combo("t", keys.CmdOrCtrlKey, keys.ShiftKey)))
	root.Append(menu.SubMenu("File", file))

	root.Append(menu.EditMenu())

	view := menu.NewMenu()
	view.Append(app.menuCommand(menuCommandWorkspaceSearch, keys.CmdOrCtrl("k")))
	view.Append(app.menuCommand(menuCommandCommandPalette, keys.Combo("p", keys.CmdOrCtrlKey, keys.ShiftKey)))
	view.Append(app.menuCommand(menuCommandToggleSidebar, keys.CmdOrCtrl("\\")))
	view.Append(app.menuCommand(menuCommandChangeOrientation, keys.CmdOrCtrl("j")))
	view.AddSeparator()
	view.Append(app.menuCommand(menuCommandOpenNetwork, nil))
	view.Append(app.menuCommand(menuCommandToggleDevTools, keys.Combo("i", keys.CmdOrCtrlKey, keys.OptionOrAltKey)))
	root.Append(menu.SubMenu("View", view))

	request := menu.NewMenu()
	request.Append(app.menuCommand(menuCommandSendOrStart, keys.CmdOrCtrl("enter")))
	request.Append(app.menuCommand(menuCommandCancelActive, nil))
	request.AddSeparator()
	request.Append(app.menuCommand(menuCommandOpenCookies, nil))
	root.Append(menu.SubMenu("Request", request))

	collection := menu.NewMenu()
	collection.Append(app.menuCommand(menuCommandOpenRunner, nil))
	root.Append(menu.SubMenu("Collection", collection))

	environment := menu.NewMenu()
	environment.Append(app.menuCommand(menuCommandOpenEnvironments, nil))
	root.Append(menu.SubMenu("Environment", environment))

	gitMenu := menu.NewMenu()
	gitMenu.Append(app.menuCommand(menuCommandOpenGit, nil))
	root.Append(menu.SubMenu("Git", gitMenu))

	root.Append(menu.WindowMenu())

	help := menu.NewMenu()
	help.Append(app.menuCommand(menuCommandOpenCapabilities, nil))
	help.Append(app.menuCommand(menuCommandOpenKeybindings, nil))
	help.AddSeparator()
	help.Append(menu.Text("LiteAPI Help", nil, func(_ *menu.CallbackData) {
		app.emitNativeMenuCommand(menuCommandOpenCapabilities)
	}))
	// macOS moves this exact Wails-created item into the application menu after
	// startup. Keeping it in Help makes Settings reachable on other platforms.
	help.Append(app.menuCommand(menuCommandOpenPreferences, keys.CmdOrCtrl(",")))
	root.Append(menu.SubMenu("Help", help))
	return root
}
