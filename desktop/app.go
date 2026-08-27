package main

import (
	"context"
	"log"
	"net"
	"net/http"
	"time"

	"github.com/maxjb-xyz/reverb/internal/api"
	"github.com/maxjb-xyz/reverb/internal/scrobble"
	"github.com/maxjb-xyz/reverb/internal/store"
	"github.com/maxjb-xyz/reverb/internal/wiring"
)

// App is the Wails application lifecycle. It owns the local HTTP server
// and the wiring bundle so the frontend can reach the API on 127.0.0.1:0.
type App struct {
	ctx    context.Context
	cancel context.CancelFunc
	srv    *http.Server
	ln     net.Listener
	bundle wiring.ServiceBundle
	deps   api.Deps
	port   int
	// store is closed on shutdown. boot() opens it and hands ownership here.
	store *store.Store
	// scrobble is started by StartServices.
	scrobble *scrobble.Service
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
	if a.bundle.Supervisor != nil {
		shutCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = a.bundle.Supervisor.Shutdown(shutCtx)
	}
	if a.bundle.Manager != nil {
		a.bundle.Manager.Stop()
	}
	if a.store != nil {
		_ = a.store.Close()
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
