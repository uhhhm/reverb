//go:build desktop

package main

import (
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// quitApp closes the window, which runs OnShutdown and exits the process. The
// updater calls it after spawning the successor.
func quitApp(app *App) {
	if app == nil || app.ctx == nil {
		return
	}
	wailsruntime.Quit(app.ctx)
}

// runApp opens the native window. The webview is served by the same
// api.Server handler as the local HTTP listener, so the SPA and /api/v1 are
// same-origin inside the window (cookies and the WebSocket work unchanged).
// The 127.0.0.1 listener still runs — paired devices reach the API there.
func runApp(app *App) error {
	return wails.Run(&options.App{
		Title:         "Reverb",
		Width:         1200,
		Height:        800,
		MinWidth:      800,
		MinHeight:     600,
		AssetServer:   &assetserver.Options{Handler: app.srv.Handler},
		OnStartup:     app.OnStartup,
		OnShutdown:    app.OnShutdown,
		OnBeforeClose: app.OnBeforeClose,
		Bind:          []interface{}{app},
	})
}
