package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/uhhhm/reverb/internal/api"
	"github.com/uhhhm/reverb/internal/app"
	"github.com/uhhhm/reverb/internal/config"
	"github.com/uhhhm/reverb/internal/desktop"
)

var version = "dev"

func main() {
	app, err := boot(os.Args[1:])
	if err != nil {
		log.Fatal(err)
	}
	app.StartServices()

	// runApp is build-tag dispatched: the native Wails window under -tags
	// desktop (frontend.go), plain HTTP otherwise (run_fallback.go).
	if err := runApp(app); err != nil {
		log.Fatal(err)
	}
}

// boot builds the desktop composition root: filesystem contract, bundled-tool
// environment, config, store, registries, wiring, API deps and the 127.0.0.1
// listener. It stops short of starting the window, so the smoke test can boot
// the very same wiring the app runs rather than a hand-assembled lookalike.
// args are the CLI flags (os.Args[1:] in main; nil under test, where os.Args
// carries the test binary's own flags).
func boot(args []string) (*App, error) {
	// Desktop filesystem contract: XDG DB, Music dir, legacy migration.
	desktopDB := desktop.ResolveDesktopDB()
	downloadDir := desktop.ResolveDesktopDownloadDir()
	dataDir := desktop.ResolveDesktopDataDir()
	if err := desktop.MaybeMigrateLegacyDB(); err != nil {
		log.Printf("desktop: legacy DB migration: %v", err)
	}
	if os.Getenv("REVERB_DB") == "" {
		_ = os.Setenv("REVERB_DB", desktopDB)
	}
	if os.Getenv("REVERB_DOWNLOAD_DIR") == "" {
		_ = os.Setenv("REVERB_DOWNLOAD_DIR", downloadDir)
	}
	_ = os.MkdirAll(dataDir, 0755)
	_ = os.MkdirAll(downloadDir, 0755)

	// Point the services at the bundled navidrome/spotdl/yt-dlp/ffmpeg before
	// config.Load and wiring read the environment.
	ApplyBundledToolEnv()

	cfg, err := config.Load(args, os.Getenv)
	if err != nil {
		return nil, err
	}
	// Override Port=0 (random) unless --port arg or REVERB_PORT is set.
	hasPortArg := false
	for _, arg := range args {
		if arg == "--port" || strings.HasPrefix(arg, "--port=") || arg == "-port" || strings.HasPrefix(arg, "-port=") {
			hasPortArg = true
			break
		}
	}
	if !hasPortArg && os.Getenv("REVERB_PORT") == "" {
		cfg.Port = 0
	}

	rt, err := app.Build(context.Background(), app.Options{
		DBPath:     cfg.DBPath,
		Version:    version,
		UpdateRepo: cfg.UpdateRepo,
		P2PPort:    cfg.P2PPort,
		Dev:        cfg.Dev,
		Desktop:    true,
		Getenv:     os.Getenv,
	})
	if err != nil {
		return nil, err
	}
	deps := rt.Deps

	// net.Listen on 127.0.0.1:port and http.Server with api.NewServer(deps).Handler()
	addr := fmt.Sprintf("127.0.0.1:%d", cfg.Port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		rt.Close()
		return nil, err
	}
	port := ln.Addr().(*net.TCPAddr).Port
	log.Printf("desktop reverb listening on 127.0.0.1:%d (dev=%v)", port, cfg.Dev)

	// The window's page is served by the Wails AssetServer, which cannot carry a
	// WebSocket upgrade. Publish the real listener port so the SPA dials it
	// directly for realtime updates.
	deps.LocalAPIPort = port

	srv := &http.Server{
		Handler:           api.NewServer(deps).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}

	a := NewApp()
	a.ln = ln
	a.srv = srv
	a.runtime = rt
	a.deps = deps
	a.port = port
	a.ctx, a.cancel = context.WithCancel(context.Background())

	return a, nil
}

// StartServices starts the long-running background work boot() only wired up.
// It is separate from boot() so constructing the composition root has no side
// effects — notably, booting it in a test must not spawn a second Navidrome on
// the fixed 4533 port.
func (a *App) StartServices() {
	a.runtime.StartBackground(context.Background())
}
