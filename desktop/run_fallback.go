//go:build !desktop

package main

import (
	"context"
	"log"
	"net/http"
	"time"
)

// quitApp stops the fallback server, which ends runApp and the process with
// it. The native build closes the window instead.
func quitApp(app *App) {
	if app == nil || app.srv == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_ = app.srv.Shutdown(ctx)
}

// runApp serves plain HTTP when built without the desktop tag (no Wails, no
// cgo). Open the printed URL in a browser.
func runApp(app *App) error {
	log.Printf("desktop fallback HTTP serving on http://127.0.0.1:%d", app.port)
	if err := app.srv.Serve(app.ln); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}
