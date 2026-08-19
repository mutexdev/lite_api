package core

import (
	"context"
	"embed"
	"fmt"
	"strings"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
)

// Run builds the App and hands it to Wails. It lives here rather than in
// package main so that main.go can hold nothing but the embedded assets, which
// are the one thing that CANNOT move: a //go:embed path is resolved relative to
// the file declaring it and may not escape its own directory.
//
// It is a package-level function on purpose. Wails binds every EXPORTED METHOD
// of the struct passed to Bind, so exporting startup, beforeClose, shutdown or
// handleOpenURL to reach them from main would have added four methods to the
// frontend's API surface as a side effect of moving a file. A plain function
// is not a method and is not bound.
func Run(assets embed.FS, args []string) error {
	app, err := NewProductionApp(args)
	if err != nil {
		return err
	}

	title := "LiteAPI"
	if len(app.state.Workspaces) == 1 && strings.TrimSpace(app.state.Workspaces[0].Name) != "" {
		title += " — " + app.state.Workspaces[0].Name
	}

	return wails.Run(&options.App{
		Title:  title,
		Width:  1024,
		Height: 768,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: &options.RGBA{R: 27, G: 38, B: 54, A: 1},
		DragAndDrop: &options.DragAndDrop{
			EnableFileDrop:     true,
			DisableWebViewDrop: true,
			CSSDropProperty:    "--wails-drop-target",
			CSSDropValue:       "drop",
		},
		Menu: buildNativeMenu(app),
		OnStartup: func(ctx context.Context) {
			app.startup(ctx)
			installNativeApplicationMenu(app)
		},
		OnBeforeClose: func(ctx context.Context) (prevent bool) {
			if app.beforeClose(ctx) {
				return true
			}
			// Persistence is coalesced (US-012), so a mutation made in the
			// last ~250 ms may still be in memory only. Anything that lets the
			// window close must force it out first, and a failure here is a
			// refusal to quit rather than silent data loss.
			if err := app.flushPersist(); err != nil {
				showNativeCloseError(ctx, fmt.Errorf("save workspace state: %w", err))
				return true
			}
			return false
		},
		OnShutdown: app.shutdown,
		Mac: &mac.Options{
			About:     &mac.AboutInfo{Title: "LiteAPI"},
			OnUrlOpen: app.handleOpenURL,
		},
		Bind: []interface{}{
			app,
		},
	})
}
