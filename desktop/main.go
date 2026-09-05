package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/uhhhm/reverb/desktop/updater"
	"github.com/uhhhm/reverb/internal/api"
	"github.com/uhhhm/reverb/internal/app"
	"github.com/uhhhm/reverb/internal/config"
	"github.com/uhhhm/reverb/internal/desktop"
)

var version = "dev"

func main() {
	args := os.Args[1:]
	if len(args) > 0 && args[0] == "--background" {
		if err := runBackground(args[1:]); err != nil {
			log.Fatal(err)
		}
		return
	}
	if len(args) > 0 && args[0] == "--stop-background" {
		cfg, err := desktopConfig(args[1:])
		if err != nil {
			log.Fatal(err)
		}
		if err := stopBackgroundSync(filepath.Dir(cfg.DBPath)); err != nil {
			log.Fatal(err)
		}
		return
	}
	cfg, err := desktopConfig(args)
	if err != nil {
		log.Fatal(err)
	}
	if err := stopBackgroundSync(filepath.Dir(cfg.DBPath)); err != nil {
		log.Fatal(err)
	}
	app, err := boot(args)
	if err != nil {
		log.Fatal(err)
	}
	app.StartServices()

	// runApp is build-tag dispatched: the native Wails window under -tags
	// desktop (frontend.go), plain HTTP otherwise (run_fallback.go).
	if err := runApp(app); err != nil {
		app.quitRequested.Store(true)
		app.OnShutdown(context.Background())
		log.Fatal(err)
	}
	app.OnShutdown(context.Background())
}

func desktopConfig(args []string) (config.Config, error) {
	return config.Load(args, func(key string) string {
		if key == "REVERB_DB" {
			return desktop.ResolveDesktopDB()
		}
		return os.Getenv(key)
	})
}

// boot builds the desktop composition root: filesystem contract, bundled-tool
// environment, config, store, registries, wiring, API deps and the 127.0.0.1
// listener. It stops short of starting the window, so the smoke test can boot
// the very same wiring the app runs rather than a hand-assembled lookalike.
// args are the CLI flags (os.Args[1:] in main; nil under test, where os.Args
// carries the test binary's own flags).
func boot(args []string) (*App, error) {
	cfg, err := desktopConfig(args)
	if err != nil {
		return nil, err
	}
	// Desktop filesystem contract: XDG DB, Music dir, legacy migration.
	// Resolve --db before the lock and background socket, so all three identify
	// the same database even when a CLI flag overrides the environment.
	_ = os.Setenv("REVERB_DB", cfg.DBPath)
	downloadDir := desktop.ResolveDesktopDownloadDir()
	dataDir := filepath.Dir(cfg.DBPath)
	if err := desktop.MaybeMigrateLegacyDB(); err != nil {
		log.Printf("desktop: legacy DB migration: %v", err)
	}
	if os.Getenv("REVERB_DOWNLOAD_DIR") == "" {
		_ = os.Setenv("REVERB_DOWNLOAD_DIR", downloadDir)
	}
	_ = os.MkdirAll(dataDir, 0755)
	_ = os.MkdirAll(downloadDir, 0755)

	// When this process was spawned by an instance installing an update, wait
	// for that instance to exit before anything opens the database or starts
	// the bundled Navidrome, which binds a fixed port.
	updater.WaitForPredecessor(dataDir, 30*time.Second)

	// One app per data dir: a second copy would be a second writer on the same
	// SQLite file, a second bind of the fixed p2p port and a second supervised
	// Navidrome on 4533.
	releaseLock, err := AcquireSingleInstanceLock(dataDir)
	if err != nil {
		return nil, err
	}

	// Point the services at the bundled navidrome/spotdl/yt-dlp/ffmpeg before
	// wiring reads the environment.
	ApplyBundledToolEnv()

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
		releaseLock()
		return nil, err
	}
	deps := rt.Deps

	// The updater needs to quit the app once it has spawned the successor, and
	// the App it quits does not exist yet — hence the indirection.
	var appRef *App
	upd := newUpdater(cfg.UpdateRepo, dataDir, rt.Bus, func() { quitApp(appRef) })
	if upd != nil {
		deps.Update = updateAdapter{svc: upd}
	}

	// net.Listen on 127.0.0.1:port and http.Server with api.NewServer(deps).Handler()
	addr := fmt.Sprintf("127.0.0.1:%d", cfg.Port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		rt.Close()
		releaseLock()
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
	a.dataDir = dataDir
	a.releaseLock = releaseLock
	a.updater = upd
	appRef = a
	a.ln = ln
	a.srv = srv
	a.runtime = rt
	a.deps = deps
	a.port = port
	a.ctx, a.cancel = context.WithCancel(context.Background())
	a.backgroundArgs = append([]string(nil), args...)
	if err := a.loadBackgroundPreference(); err != nil {
		a.OnShutdown(context.Background())
		return nil, err
	}

	return a, nil
}

// StartServices starts the long-running background work boot() only wired up.
// It is separate from boot() so constructing the composition root has no side
// effects — notably, booting it in a test must not spawn a second Navidrome on
// the fixed 4533 port.
func (a *App) StartServices() {
	// All workers share a cancellable lifetime, including in headless mode.
	ctx := context.Background()
	if a.ctx != nil {
		ctx = a.ctx
	}
	a.runtime.StartBackground(ctx)
	if a.updater != nil {
		// Discard the binary and payload the previous version left behind
		// before polling for the next one.
		if exe, err := os.Executable(); err == nil {
			updater.CleanupAfterUpdate(a.dataDir, exe, version)
		}
		a.updater.Start(a.ctx)
	}
}
