// What the Settings panel shows for the MCP agent interface.
//
// See docs/mcp-agent-interface.md; the preference half of this lives on
// Preferences.MCP in preferences.go.
package types

// MCPStatus is the pairing view: whether the server is meant to run, whether it
// actually is, and the one command that connects an agent to it.
//
// TOKEN IS PRESENT HERE ON PURPOSE, and it is the one place it appears. This
// struct crosses the Wails boundary into the app's own Settings window, which
// is how the user learns the credential they are about to paste into their
// agent's configuration — the pairing flow has to show it once or there is no
// way to pair at all. It is never returned over MCP itself: nothing in
// mcpserver.Backend has a field that could carry it.
type MCPStatus struct {
	// Enabled is the user's preference; Running is the observed listener. They
	// disagree while a bind is failing, which is exactly when the user needs to
	// see both rather than one derived "on/off".
	Enabled bool `json:"enabled"`
	Running bool `json:"running"`
	// Port is the resolved port when running, and the configured one otherwise,
	// so the command below always names something.
	Port      int    `json:"port"`
	Token     string `json:"token"`
	Command   string `json:"command"`
	LastError string `json:"lastError,omitempty"`
}
