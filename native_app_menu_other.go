//go:build !darwin || !cgo

package main

// installNativeApplicationMenu is intentionally a no-op outside a Cocoa build.
// The shared Wails menu remains the cross-platform implementation.
func installNativeApplicationMenu(app *App) {}
