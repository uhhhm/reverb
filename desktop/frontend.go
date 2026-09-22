//go:build desktop

package main

import (
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/menu"
	"github.com/wailsapp/wails/v2/pkg/menu/keys"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	linuxoptions "github.com/wailsapp/wails/v2/pkg/options/linux"
	windowsoptions "github.com/wailsapp/wails/v2/pkg/options/windows"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// quitApp closes the window, which runs OnShutdown and exits the process. The
// updater calls it after spawning the successor.
func quitApp(app *App) {
	if app != nil {
		app.quitRequested.Store(true)
		if app.stopBackground != nil {
			app.stopBackground()
			return
		}
	}
	if app == nil || app.ctx == nil {
		return
	}
	wailsruntime.Quit(app.ctx)
}

func focusWindow(app *App) {
	if app == nil || app.ctx == nil {
		return
	}
	wailsruntime.WindowUnminimise(app.ctx)
	wailsruntime.WindowShow(app.ctx)
}

// runApp opens the native window. The webview is served by the same
// api.Server handler as the local HTTP listener, so the SPA and /api/v1 are
// same-origin inside the window (cookies and the WebSocket work unchanged).
// The 127.0.0.1 listener still runs — paired devices reach the API there.
func runApp(app *App) error {
	configureWebView()
	app.startBackground = func() error { return spawnBackground(app.dataDir, app.backgroundArgs) }
	appMenu := menu.NewMenu()
	reverbMenu := appMenu.AddSubmenu("Reverb")
	reverbMenu.AddText("Quit Reverb and stop sync", keys.CmdOrCtrl("q"), func(_ *menu.CallbackData) { quitApp(app) })
	appMenu.Append(menu.EditMenu())
	windowsMessages := windowsoptions.DefaultMessages()
	// The default download strategy shows this prompt before fetching WebView2.
	// Keep it explicit: the GUI-subsystem process has no console, so silently
	// waiting for the bootstrapper would look exactly like a failed launch.
	windowsMessages.InstallationRequired = "Reverb needs the Microsoft WebView2 Runtime. Choose OK to download and install it; keep this window open while installation finishes."
	return wails.Run(&options.App{
		Title:         "Reverb",
		Width:         1200,
		Height:        800,
		MinWidth:      800,
		MinHeight:     600,
		Menu:          appMenu,
		AssetServer:   &assetserver.Options{Handler: app.srv.Handler},
		OnStartup:     app.OnStartup,
		OnShutdown:    app.OnShutdown,
		OnBeforeClose: app.OnBeforeClose,
		Bind:          []interface{}{app},
		// The Wayland app ID and X11 WM_CLASS, which the desktop matches to
		// reverb-desktop.desktop to group the window under the launcher's
		// entry and icon. It also names WebKit's storage directory
		// (~/.local/share/reverb-desktop), so changing it loses that storage.
		// Explicit so neither follows the binary's file name.
		Linux:   &linuxoptions.Options{ProgramName: "reverb-desktop"},
		Windows: &windowsoptions.Options{Messages: windowsMessages},
	})
}
