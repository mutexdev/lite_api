// Command liteapi is the LiteAPI desktop application.
//
// This file is deliberately the ONLY Go file in the repository root. Wails
// builds the main package in the project directory, and //go:embed resolves
// its paths relative to the declaring file and cannot escape that directory —
// so the embedded frontend bundle pins this one file here. Everything else
// lives under internal/, where it can be organised by domain.
package main

import (
	"embed"
	"fmt"
	"os"

	"github.com/mutexdev/lite_api/internal/core"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	args := os.Args[1:]

	// `liteapi mcp` serves MCP over stdin/stdout and never opens a window, so it
	// has to divert BEFORE wails.Run — which on macOS takes over the main
	// thread and never returns. The dispatch stays here, three lines of it,
	// rather than growing a command framework: everything it does lives in
	// internal/core (see headless_mcp.go), and this file holds nothing but the
	// embedded assets and the choice between the two modes.
	if len(args) > 0 && args[0] == "mcp" {
		if err := core.RunHeadlessMCP(args[1:]); err != nil {
			// STDERR, NEVER STDOUT. In this mode stdout carries the MCP
			// protocol and nothing else; a message printed there would reach
			// the client as a malformed frame rather than as an explanation.
			fmt.Fprintln(os.Stderr, "liteapi mcp: "+err.Error())
			os.Exit(1)
		}
		return
	}

	if err := core.Run(assets, args); err != nil {
		println("Error:", err.Error())
	}
}
