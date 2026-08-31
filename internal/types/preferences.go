// User preferences: layout, display, fonts, requests, caches and devtools.
//
// US-060. Moved verbatim from app.go; see internal/types/proxy.go for why the
// aliases left behind in internal/core are a Go shim and not a Wails one.
package types

type Preferences struct {
	Theme              string                `json:"theme"`
	ThemeVariantLight  string                `json:"themeVariantLight,omitempty"`
	ThemeVariantDark   string                `json:"themeVariantDark,omitempty"`
	KeybindingsEnabled *bool                 `json:"keybindingsEnabled,omitempty"`
	KeyBindings        map[string]KeyBinding `json:"keyBindings,omitempty"`
	// US-057. Which default set the keybindings layer on top of. User overrides
	// in KeyBindings still win, so switching preset never silently replaces a
	// shortcut somebody deliberately set.
	KeyBindingPreset       string              `json:"keyBindingPreset,omitempty"`
	Layout                 LayoutPreferences   `json:"layout"`
	Display                DisplayPreferences  `json:"display"`
	Font                   FontPreferences     `json:"font"`
	Request                RequestPreferences  `json:"request"`
	General                GeneralPreferences  `json:"general"`
	AutoSave               AutoSavePreferences `json:"autoSave"`
	Cache                  CachePreferences    `json:"cache"`
	DevTools               DevToolsPreferences `json:"devTools"`
	Autosave               bool                `json:"autosave"`
	DefaultCollectionPath  string              `json:"defaultCollectionPath"`
	CodeFontSize           int                 `json:"codeFontSize"`
	StoreCookies           bool                `json:"storeCookies"`
	OAuth2UseSystemBrowser bool                `json:"oauth2UseSystemBrowser"`
	ProxyMode              string              `json:"proxyMode"`
	Proxy                  ProxyPreferences    `json:"proxy"`
	MCP                    MCPPreferences      `json:"mcp"`
}

// MCPPreferences is the user's half of the MCP agent interface
// (docs/mcp-agent-interface.md): whether the local server runs, on which port,
// and whether the write tier is unlocked.
//
// THE BEARER TOKEN IS NOT HERE, and its absence is the design. Preferences live
// in state.json, which is written on every mutation, copied into bug reports,
// and synced by whatever the user points at their data directory. The token
// lives in its own owner-only file instead — see App.mcpToken.
//
// Port carries no "0 means ephemeral" case even though the server accepts one.
// Pairing hands the user a `claude mcp add` command with the port baked into a
// URL, and an ephemeral port would silently invalidate that command on every
// restart; the normaliser therefore coerces 0 to the default rather than
// passing it through.
type MCPPreferences struct {
	Enabled          bool `json:"enabled"`
	Port             int  `json:"port"`
	WriteTierEnabled bool `json:"writeTierEnabled"`
}

type LayoutPreferences struct {
	ResponsePaneOrientation string `json:"responsePaneOrientation,omitempty"`
}

type DisplayPreferences struct {
	ZoomPercentage int `json:"zoomPercentage,omitempty"`
}

type FontPreferences struct {
	CodeFont     string `json:"codeFont,omitempty"`
	CodeFontSize int    `json:"codeFontSize,omitempty"`
}

type RequestPreferences struct {
	SSLVerification           *bool                                `json:"sslVerification,omitempty"`
	CustomCaCertificate       CustomCaCertificatePreferences       `json:"customCaCertificate"`
	KeepDefaultCaCertificates KeepDefaultCaCertificatesPreferences `json:"keepDefaultCaCertificates"`
	StoreCookies              *bool                                `json:"storeCookies,omitempty"`
	SendCookies               *bool                                `json:"sendCookies,omitempty"`
	Timeout                   int                                  `json:"timeout,omitempty"`
	// US-011. Cap on how many bytes of a response body are read into memory.
	// Zero means the default; see responseBodyReadLimit.
	MaxResponseBytes int `json:"maxResponseBytes,omitempty"`
}

type CustomCaCertificatePreferences struct {
	Enabled  bool   `json:"enabled"`
	FilePath string `json:"filePath,omitempty"`
}

type KeepDefaultCaCertificatesPreferences struct {
	Enabled *bool `json:"enabled,omitempty"`
}

type GeneralPreferences struct {
	DefaultLocation      string `json:"defaultLocation,omitempty"`
	DefaultWorkspacePath string `json:"defaultWorkspacePath,omitempty"`
	LastImportDirectory  string `json:"lastImportDirectory,omitempty"`
	// DiscoveryPromptedAt records when the first-run offer to import from
	// another API client was made (US-064). Set once, so an offer that has been
	// seen is not made again on every launch.
	DiscoveryPromptedAt string `json:"discoveryPromptedAt,omitempty"`
}

type AutoSavePreferences struct {
	Enabled  bool `json:"enabled"`
	Interval int  `json:"interval,omitempty"`
}

type CachePreferences struct {
	SSLSession SSLSessionCachePreferences `json:"sslSession"`
	File       FileCachePreferences       `json:"file"`
}

type SSLSessionCachePreferences struct {
	Enabled bool `json:"enabled"`
}

type FileCachePreferences struct {
	Enabled bool `json:"enabled"`
}

type DevToolsPreferences struct {
	Open              bool                       `json:"open"`
	ActiveTab         string                     `json:"activeTab,omitempty"`
	DrawerHeight      int                        `json:"drawerHeight,omitempty"`
	DetailsPanelWidth int                        `json:"detailsPanelWidth,omitempty"`
	Network           DevToolsNetworkPreferences `json:"network"`
}

type DevToolsNetworkPreferences struct {
	SortKey       string `json:"sortKey,omitempty"`
	SortDirection string `json:"sortDirection,omitempty"`
	ColumnWidths  []int  `json:"columnWidths,omitempty"`
}

type KeyBinding struct {
	Name    string `json:"name,omitempty"`
	Mac     string `json:"mac,omitempty"`
	Windows string `json:"windows,omitempty"`
}
