//go:build mcpenforcement

package core

// The other half of the switch in mcp_enforcement_off_test.go. Built only under
// `-tags mcpenforcement`, which the wave that wires the engine checkpoints turns
// into the default by deleting both files.
const mcpEnforcementWired = true
