//go:build !desktop

package main

import (
	"log"
	"net/http"
)

// runApp serves plain HTTP when built without the desktop tag (no Wails, no
// cgo). Open the printed URL in a browser.
func runApp(app *App) error {
	log.Printf("desktop fallback HTTP serving on http://127.0.0.1:%d", app.port)
	if err := app.srv.Serve(app.ln); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}
