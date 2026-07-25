package main

import (
	"context"
	"embed"
	"os"
	"strings"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	app, err := NewProductionApp(os.Args[1:])
	if err != nil {
		println("Error:", err.Error())
		return
	}

	// Create application with options
	title := "LiteAPI"
	if len(app.state.Workspaces) == 1 && strings.TrimSpace(app.state.Workspaces[0].Name) != "" {
		title += " — " + app.state.Workspaces[0].Name
	}
	err = wails.Run(&options.App{
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
		OnBeforeClose: app.beforeClose,
		OnShutdown:    app.shutdown,
		Mac: &mac.Options{
			About:     &mac.AboutInfo{Title: "LiteAPI"},
			OnUrlOpen: app.handleOpenURL,
		},
		Bind: []interface{}{
			app,
		},
	})

	if err != nil {
		println("Error:", err.Error())
	}
}
