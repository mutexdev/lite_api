package core

// The Settings-panel view of the MCP interface, and the pairing command.

import (
	"fmt"

	"github.com/mutexdev/lite_api/internal/types"
)

// --- bindings ------------------------------------------------------------

// GetMCPStatus reports whether the MCP server is enabled, whether it is
// actually listening, and the one-liner that pairs an agent with it.
//
// The command is assembled here rather than in the frontend so there is exactly
// one definition of it: the port has two sources (the live listener when it
// resolved one, the preference otherwise) and a frontend that picked the wrong
// one would hand the user a command that connects to nothing.
func (a *App) GetMCPStatus() (types.MCPStatus, error) {
	preferences := a.mcpPreferencesSnapshot()

	token, err := a.mcpToken()
	if err != nil {
		return types.MCPStatus{}, err
	}

	a.mcpMu.Lock()
	server := a.mcpServer
	lastError := a.mcpLastError
	a.mcpMu.Unlock()

	status := types.MCPStatus{
		Enabled:   preferences.Enabled,
		Port:      preferences.Port,
		Token:     token,
		LastError: lastError,
	}
	if server != nil {
		if port := server.Port(); port > 0 {
			status.Running = true
			status.Port = port
		}
	}
	status.Command = mcpPairingCommand(status.Port, status.Token)
	return status, nil
}

// mcpPairingCommand is the exact string docs/mcp-agent-interface.md publishes.
// It is quoted verbatim in a test: the header argument's quoting is what makes
// it survive a paste into a shell, and a stray change to it would produce a
// command that runs and then fails authentication with no clue why.
func mcpPairingCommand(port int, token string) string {
	return fmt.Sprintf(
		"claude mcp add --transport http liteapi http://127.0.0.1:%d/mcp --header \"Authorization: Bearer %s\"",
		port, token,
	)
}
