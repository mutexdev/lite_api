package core

import (
	"strconv"
	"strings"

	"github.com/mutexdev/lite_api/internal/transport"
)

func normalizePreferences(preferences Preferences) Preferences {
	preferences.Theme = normalizeThemeMode(preferences.Theme)
	preferences.ThemeVariantLight = normalizeThemeVariant(preferences.ThemeVariantLight, "light")
	preferences.ThemeVariantDark = normalizeThemeVariant(preferences.ThemeVariantDark, "dark")
	if preferences.KeybindingsEnabled == nil {
		preferences.KeybindingsEnabled = boolPtr(true)
	}
	preferences.KeyBindings = normalizeKeyBindings(preferences.KeyBindings)
	preferences.KeyBindingPreset = normalizeKeyBindingPreset(preferences.KeyBindingPreset)
	if preferences.CodeFontSize <= 0 {
		preferences.CodeFontSize = 13
	}
	preferences.Layout = normalizeLayoutPreferences(preferences.Layout)
	preferences.Display = normalizeDisplayPreferences(preferences.Display)
	preferences.Font = normalizeFontPreferences(preferences.Font, preferences.CodeFontSize)
	preferences.CodeFontSize = preferences.Font.CodeFontSize
	preferences.Request = normalizeRequestPreferences(preferences.Request, preferences.StoreCookies)
	preferences.StoreCookies = boolPtrValue(preferences.Request.StoreCookies, true)
	preferences.General = normalizeGeneralPreferences(preferences.General, preferences.DefaultCollectionPath)
	preferences.DefaultCollectionPath = preferences.General.DefaultLocation
	preferences.AutoSave = normalizeAutoSavePreferences(preferences.AutoSave, preferences.Autosave)
	preferences.Autosave = preferences.AutoSave.Enabled
	preferences.Cache = normalizeCachePreferences(preferences.Cache)
	preferences.DevTools = normalizeDevToolsPreferences(preferences.DevTools)
	preferences.Proxy = normalizeProxyPreferences(preferences.Proxy, preferences.ProxyMode)
	preferences.ProxyMode = preferenceProxyMode(preferences.Proxy)
	return preferences
}

func normalizeLayoutPreferences(preferences LayoutPreferences) LayoutPreferences {
	switch preferences.ResponsePaneOrientation {
	case "horizontal", "vertical":
	default:
		preferences.ResponsePaneOrientation = "horizontal"
	}
	return preferences
}

func normalizeDisplayPreferences(preferences DisplayPreferences) DisplayPreferences {
	if preferences.ZoomPercentage == 0 {
		preferences.ZoomPercentage = 100
	}
	if preferences.ZoomPercentage < 50 {
		preferences.ZoomPercentage = 50
	}
	if preferences.ZoomPercentage > 150 {
		preferences.ZoomPercentage = 150
	}
	return preferences
}

func normalizeFontPreferences(preferences FontPreferences, legacyCodeFontSize int) FontPreferences {
	preferences.CodeFont = strings.TrimSpace(preferences.CodeFont)
	if preferences.CodeFont == "" {
		preferences.CodeFont = "default"
	}
	if preferences.CodeFontSize == 0 && legacyCodeFontSize > 0 {
		preferences.CodeFontSize = legacyCodeFontSize
	}
	if preferences.CodeFontSize == 0 {
		preferences.CodeFontSize = 13
	}
	if preferences.CodeFontSize < 1 {
		preferences.CodeFontSize = 1
	}
	if preferences.CodeFontSize > 32 {
		preferences.CodeFontSize = 32
	}
	return preferences
}

func normalizeRequestPreferences(preferences RequestPreferences, legacyStoreCookies bool) RequestPreferences {
	if preferences.SSLVerification == nil {
		preferences.SSLVerification = boolPtr(true)
	}
	if preferences.StoreCookies == nil {
		preferences.StoreCookies = boolPtr(legacyStoreCookies)
	}
	if preferences.SendCookies == nil {
		preferences.SendCookies = boolPtr(true)
	}
	if preferences.Timeout < 0 {
		preferences.Timeout = 0
	}
	preferences.CustomCaCertificate.FilePath = strings.TrimSpace(preferences.CustomCaCertificate.FilePath)
	if preferences.KeepDefaultCaCertificates.Enabled == nil {
		preferences.KeepDefaultCaCertificates.Enabled = boolPtr(true)
	}
	return preferences
}

func normalizeGeneralPreferences(preferences GeneralPreferences, legacyDefaultCollectionPath string) GeneralPreferences {
	preferences.DefaultLocation = strings.TrimSpace(preferences.DefaultLocation)
	if preferences.DefaultLocation == "" {
		preferences.DefaultLocation = strings.TrimSpace(legacyDefaultCollectionPath)
	}
	if len(preferences.DefaultLocation) > 1024 {
		preferences.DefaultLocation = preferences.DefaultLocation[:1024]
	}
	preferences.DefaultWorkspacePath = strings.TrimSpace(preferences.DefaultWorkspacePath)
	if len(preferences.DefaultWorkspacePath) > 1024 {
		preferences.DefaultWorkspacePath = preferences.DefaultWorkspacePath[:1024]
	}
	preferences.LastImportDirectory = normalizeCollectionImportDirectory(preferences.LastImportDirectory)
	return preferences
}

func normalizeAutoSavePreferences(preferences AutoSavePreferences, legacyEnabled bool) AutoSavePreferences {
	if legacyEnabled && !preferences.Enabled {
		preferences.Enabled = true
	}
	if preferences.Interval == 0 {
		preferences.Interval = 1000
	}
	if preferences.Interval < 500 {
		preferences.Interval = 500
	}
	return preferences
}

func normalizeCachePreferences(preferences CachePreferences) CachePreferences {
	return preferences
}

func normalizeDevToolsPreferences(preferences DevToolsPreferences) DevToolsPreferences {
	if !devToolsTabs[preferences.ActiveTab] {
		preferences.ActiveTab = "console"
	}
	if preferences.DrawerHeight <= 0 {
		preferences.DrawerHeight = devToolsDefaultDrawerHeight
	}
	if preferences.DrawerHeight < 220 {
		preferences.DrawerHeight = 220
	}
	if preferences.DrawerHeight > 720 {
		preferences.DrawerHeight = 720
	}
	if preferences.DetailsPanelWidth <= 0 {
		preferences.DetailsPanelWidth = 400
	}
	if preferences.DetailsPanelWidth < 280 {
		preferences.DetailsPanelWidth = 280
	}
	if preferences.DetailsPanelWidth > 800 {
		preferences.DetailsPanelWidth = 800
	}
	preferences.Network = normalizeDevToolsNetworkPreferences(preferences.Network)
	return preferences
}

func normalizeDevToolsNetworkPreferences(preferences DevToolsNetworkPreferences) DevToolsNetworkPreferences {
	if !devToolsNetworkSortKeys[preferences.SortKey] {
		preferences.SortKey = ""
	}
	switch preferences.SortDirection {
	case "asc", "desc":
	default:
		preferences.SortDirection = ""
	}
	if preferences.SortKey == "" {
		preferences.SortDirection = ""
	}
	if preferences.SortDirection == "" {
		preferences.SortKey = ""
	}
	preferences.ColumnWidths = normalizeDevToolsNetworkColumnWidths(preferences.ColumnWidths)
	return preferences
}

func normalizeDevToolsNetworkColumnWidths(widths []int) []int {
	if len(widths) != len(devToolsNetworkDefaultColumnWidths) {
		return append([]int(nil), devToolsNetworkDefaultColumnWidths...)
	}
	normalized := make([]int, len(widths))
	for i, width := range widths {
		if width < 60 {
			width = 60
		}
		normalized[i] = width
	}
	return normalized
}

func boolPtr(value bool) *bool {
	return &value
}

func boolPtrValue(value *bool, fallback bool) bool {
	if value == nil {
		return fallback
	}
	return *value
}

func normalizeThemeMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "light", "dark", "system":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return "system"
	}
}

func normalizeThemeVariant(value, mode string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	switch mode {
	case "light":
		switch value {
		case "light", "light-monochrome", "light-pastel", "vscode-light", "catppuccin-latte":
			return value
		default:
			return "light"
		}
	case "dark":
		switch value {
		case "dark", "dark-monochrome", "dark-pastel", "vscode-dark", "catppuccin-frappe", "catppuccin-macchiato", "catppuccin-mocha", "nord":
			return value
		default:
			return "dark"
		}
	default:
		return value
	}
}

// normalizeKeyBindingPreset rejects anything but a known preset id.
//
// An unknown id is coerced to "default" rather than preserved: a preset name
// this build does not recognise would otherwise be stored, resolve to no
// overrides, and leave the user looking at a selector showing a preset that
// is not in effect.
func normalizeKeyBindingPreset(value string) string {
	if strings.TrimSpace(strings.ToLower(value)) == "postman" {
		return "postman"
	}
	return ""
}

func normalizeKeyBindings(bindings map[string]KeyBinding) map[string]KeyBinding {
	if len(bindings) == 0 {
		return nil
	}
	known := defaultKeyBindingActions()
	normalized := map[string]KeyBinding{}
	for action, binding := range bindings {
		action = strings.TrimSpace(action)
		if !known[action] {
			continue
		}
		next := KeyBinding{Name: strings.TrimSpace(binding.Name)}
		if next.Name == "" {
			next.Name = knownKeyBindingName(action)
		}
		next.Mac = normalizeKeyBindingCombo(binding.Mac)
		next.Windows = normalizeKeyBindingCombo(binding.Windows)
		if next.Mac == "" && next.Windows == "" {
			continue
		}
		normalized[action] = next
	}
	if len(normalized) == 0 {
		return nil
	}
	return normalized
}

func normalizeKeyBindingCombo(value string) string {
	parts := []string{}
	for _, part := range strings.Split(strings.TrimSpace(strings.ToLower(value)), "+bind+") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if !validKeyBindingToken(part) {
			return ""
		}
		parts = append(parts, part)
	}
	if len(parts) < 2 || len(parts) > 4 {
		return ""
	}
	nonModifiers := 0
	hasModifier := false
	for _, part := range parts {
		if isKeyBindingModifier(part) {
			hasModifier = true
			continue
		}
		nonModifiers++
	}
	if !hasModifier || nonModifiers != 1 {
		return ""
	}
	return strings.Join(parts, "+bind+")
}

func isKeyBindingModifier(value string) bool {
	switch value {
	case "ctrl", "command", "alt", "shift":
		return true
	default:
		return false
	}
}

func validKeyBindingToken(value string) bool {
	if isKeyBindingModifier(value) {
		return true
	}
	if len(value) == 1 {
		return (value >= "a" && value <= "z") || (value >= "0" && value <= "9") || strings.Contains("[]\\,.-=/", value)
	}
	switch value {
	case "enter", "backspace", "tab", "delete", "esc", "space", "arrowup", "arrowdown", "arrowleft", "arrowright", "pageup", "pagedown", "home", "end":
		return true
	}
	if strings.HasPrefix(value, "f") {
		number, err := strconv.Atoi(strings.TrimPrefix(value, "f"))
		return err == nil && number >= 1 && number <= 12
	}
	return false
}

func defaultKeyBindingActions() map[string]bool {
	return map[string]bool{
		"closeTab":              true,
		"closeAllTabs":          true,
		"save":                  true,
		"saveAllTabs":           true,
		"reopenLastClosedTab":   true,
		"switchToTabAtPosition": true,
		"switchToLastTab":       true,
		"switchToPreviousTab":   true,
		"switchToNextTab":       true,
		"moveTabLeft":           true,
		"moveTabRight":          true,
		"switchToTab1":          true,
		"switchToTab2":          true,
		"switchToTab3":          true,
		"switchToTab4":          true,
		"switchToTab5":          true,
		"switchToTab6":          true,
		"switchToTab7":          true,
		"switchToTab8":          true,
		"sidebarSearch":         true,
		"copyItem":              true,
		"pasteItem":             true,
		"cloneItem":             true,
		"renameItem":            true,
		"collapseSidebar":       true,
		"sendRequest":           true,
		"changeLayout":          true,
		"importCollection":      true,
		"editEnvironment":       true,
		"newRequest":            true,
		"globalSearch":          true,
		"commandPalette":        true,
		"zoomIn":                true,
		"zoomOut":               true,
		"resetZoom":             true,
		"openTerminal":          true,
		"openPreferences":       true,
		"closeBruno":            true,
	}
}

func knownKeyBindingName(action string) string {
	switch action {
	case "sendRequest":
		return "Send Request"
	case "globalSearch":
		return "Global Search"
	case "commandPalette":
		return "Command Palette"
	case "sidebarSearch":
		return "Search Sidebar"
	case "newRequest":
		return "New Request"
	case "save":
		return "Save"
	default:
		return action
	}
}

func defaultProxyPreferences() ProxyPreferences {
	return ProxyPreferences{
		Source: "inherit",
		PAC:    ProxyPACConfig{},
		Config: transport.NormalizeProxyConfig(ProxyConfig{Protocol: "http"}),
	}
}

func normalizeProxyPreferences(proxy ProxyPreferences, legacyMode string) ProxyPreferences {
	source := strings.ToLower(strings.TrimSpace(proxy.Source))
	if source == "" {
		switch strings.ToLower(strings.TrimSpace(legacyMode)) {
		case "off", "disabled":
			proxy.Disabled = true
			source = "manual"
		case "on", "manual":
			source = "manual"
		case "pac":
			source = "pac"
		default:
			source = "inherit"
		}
	}
	switch source {
	case "manual", "pac", "inherit":
		proxy.Source = source
	default:
		proxy.Source = "inherit"
	}
	proxy.PAC.Source = strings.TrimSpace(proxy.PAC.Source)
	proxy.Config = transport.NormalizeProxyConfig(proxy.Config)
	proxy.Config.Inherit = false
	proxy.Config.Disabled = false
	return proxy
}

func preferenceProxyMode(proxy ProxyPreferences) string {
	proxy = normalizeProxyPreferences(proxy, "")
	if proxy.Disabled {
		return "off"
	}
	switch proxy.Source {
	case "manual":
		return "manual"
	case "pac":
		return "pac"
	default:
		return "system"
	}
}
