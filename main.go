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
	"os"

	"github.com/mutexdev/lite_api/internal/core"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	if err := core.Run(assets, os.Args[1:]); err != nil {
		println("Error:", err.Error())
	}
}
