package main

import (
	"context"
	"log"
	"net"
	"net/http"
	"time"

	"github.com/uhhhm/reverb/desktop/updater"
	"github.com/uhhhm/reverb/internal/api"
	"github.com/uhhhm/reverb/internal/app"
)

// App is the Wails application lifecycle. It owns the local HTTP server
// and the wiring bundle so the frontend can reach the API on 127.0.0.1:0.
type App struct {
	ctx    context.Context
	cancel context.CancelFunc
	srv    *http.Server
	ln     net.Listener
	deps   api.Deps
	port   int
	// runtime owns the services, the store and the wiring bundle. boot() builds
	// it via internal/app — the same construction the server binary uses — and
	// hands ownership here; OnShutdown closes it.
	runtime *app.Runtime
	// updater polls for releases and installs them on request; nil when the
	// running binary could not be located. dataDir is where it stages them.
	updater *updater.Service
	dataDir string
}

// NewApp creates a new desktop App.
func NewApp() *App {
	return &App{}
}

// OnStartup is called by the Wails runtime on startup. It starts the local
// HTTP server on 127.0.0.1:0 when not already running.
func (a *App) OnStartup(ctx context.Context) {
	a.ctx, a.cancel = context.WithCancel(ctx)

	if a.ln == nil {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			log.Printf("desktop: listen failed: %v", err)
			return
		}
		a.ln = ln
		if tcp, ok := ln.Addr().(*net.TCPAddr); ok {
			a.port = tcp.Port
		}
	} else {
		if tcp, ok := a.ln.Addr().(*net.TCPAddr); ok {
			a.port = tcp.Port
		}
	}

	if a.srv == nil {
		handler := api.NewServer(a.deps).Handler()
		a.srv = &http.Server{
			Handler:           handler,
			ReadHeaderTimeout: 10 * time.Second,
			ReadTimeout:       30 * time.Second,
			IdleTimeout:       2 * time.Minute,
		}
	}

	go func() {
		if err := a.srv.Serve(a.ln); err != nil && err != http.ErrServerClosed {
			log.Printf("desktop: server error: %v", err)
		}
	}()
}

// OnShutdown is called by the Wails runtime on shutdown. It gracefully
// stops the HTTP server and any wiring services.
func (a *App) OnShutdown(ctx context.Context) {
	if a.cancel != nil {
		a.cancel()
	}
	if a.srv != nil {
		shutCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = a.srv.Shutdown(shutCtx)
	}
	if a.runtime != nil {
		if a.runtime.Bundle.Supervisor != nil {
			shutCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			_ = a.runtime.Bundle.Supervisor.Shutdown(shutCtx)
		}
		// Stops the download manager and closes the store.
		a.runtime.Close()
	}
}

// OnBeforeClose is called when the window is about to close. Returning
// false allows the quit to proceed (close→quit).
func (a *App) OnBeforeClose(ctx context.Context) bool {
	return false
}

// GetPort returns the actual listening port. It is exposed to the frontend
// so it can reach the local API.
func (a *App) GetPort() int {
	return a.port
}
