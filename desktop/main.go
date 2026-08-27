package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/uhhhm/reverb/internal/api"
	"github.com/uhhhm/reverb/internal/auth"
	"github.com/uhhhm/reverb/internal/catalog"
	"github.com/uhhhm/reverb/internal/config"
	"github.com/uhhhm/reverb/internal/desktop"
	"github.com/uhhhm/reverb/internal/download"
	"github.com/uhhhm/reverb/internal/download/lidarr"
	"github.com/uhhhm/reverb/internal/download/spotdl"
	"github.com/uhhhm/reverb/internal/download/ytdlp"
	"github.com/uhhhm/reverb/internal/events"
	"github.com/uhhhm/reverb/internal/library/embedded"
	"github.com/uhhhm/reverb/internal/library/lyrics"
	"github.com/uhhhm/reverb/internal/library/subsonic"
	"github.com/uhhhm/reverb/internal/override"
	"github.com/uhhhm/reverb/internal/play"
	"github.com/uhhhm/reverb/internal/playlistsync"
	"github.com/uhhhm/reverb/internal/registry"
	"github.com/uhhhm/reverb/internal/resolver"
	"github.com/uhhhm/reverb/internal/scrobble"
	"github.com/uhhhm/reverb/internal/scrobble/lastfm"
	"github.com/uhhhm/reverb/internal/search/deezer"
	"github.com/uhhhm/reverb/internal/search/spotify"
	"github.com/uhhhm/reverb/internal/store"
	"github.com/uhhhm/reverb/internal/store/db"
	reverbsync "github.com/uhhhm/reverb/internal/sync"
	"github.com/uhhhm/reverb/internal/wiring"
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

	// Open store and run migrations.
	st, err := store.Open(cfg.DBPath)
	if err != nil {
		return nil, err
	}
	if err := st.Migrate(); err != nil {
		st.Close()
		return nil, err
	}

	authSvc := auth.NewService(st.Q(), time.Now)
	if err := authSvc.EnsureSeed(context.Background()); err != nil {
		st.Close()
		return nil, fmt.Errorf("seed identity: %w", err)
	}
	seedBundledDownloader(context.Background(), st.Q(), os.Getenv)
	if serverID, err := reverbsync.EnsureServerDevice(context.Background(), st.Q()); err != nil {
		log.Printf("WARNING: ensure server device: %v", err)
	} else {
		log.Printf("server device %s ready", serverID)
	}

	// Registries (explicit, no init side effects).
	libraryReg := registry.NewRegistry("library")
	libraryReg.Register("subsonic", func() registry.Plugin { return subsonic.New() })
	searchReg := registry.NewRegistry("search")
	searchReg.Register("spotify", func() registry.Plugin { return spotify.New() })
	searchReg.Register("deezer", func() registry.Plugin { return deezer.New() })
	downloaderReg := registry.NewRegistry("downloader")
	downloaderReg.Register("spotdl", func() registry.Plugin { return spotdl.New() })
	downloaderReg.Register("lidarr", func() registry.Plugin { return lidarr.New() })
	downloaderReg.Register("ytdlp", func() registry.Plugin { return ytdlp.New() })
	registry.RegisterCapability("async", func(p registry.Plugin) bool {
		_, ok := p.(download.AsyncDownloader)
		return ok
	})

	bus := events.New()

	builder := wiring.NewBuilder(
		libraryReg, searchReg, downloaderReg,
		st.Q(), st, bus, download.RealClock{}, os.Getenv,
		filepath.Dir(cfg.DBPath),
	)

	// Resolver wiring for live reload.
	// minimal reloader shim for desktop fallback: reuse builder directly
	// without full serviceReloader to keep essential wiring.
	resolverSvc := resolver.NewService(st.Q(), func() resolver.Rematcher { return nil }, time.Now)
	builder.SetResolverProvider(func() wiring.BindingResolver { return resolverSvc })

	catalogSvc := catalog.NewService(st.Q(), time.Now, uuid.NewString)
	builder.SetCanonicalMinter(catalogSvc)

	bundle, err := builder.Build(context.Background())
	if err != nil {
		st.Close()
		return nil, err
	}
	if bundle.Manager != nil {
		bundle.Manager.SetCanonicalMinter(catalogSvc)
	}
	playSvc := play.NewService(st.Q(), catalogSvc, time.Now, uuid.NewString)
	statsSvc := play.NewStats(st.Q())
	scrobbleCfg := func() scrobble.Creds {
		ctx := context.Background()
		key, _ := st.Q().GetSetting(ctx, "scrobble:lastfm:api_key")
		secret, _ := st.Q().GetSetting(ctx, "scrobble:lastfm:api_secret")
		return scrobble.Creds{APIKey: key, APISecret: secret}
	}
	scrobbleSvc := scrobble.NewService(st.Q(), lastfm.New(), scrobbleCfg, time.Now, uuid.NewString)

	deps := api.Deps{
		Auth:          authSvc,
		Library:       bundle.Library,
		Lib:           libraryReg,
		Search:        searchReg,
		Downloader:    downloaderReg,
		Adapters:      st.Q(),
		PlaylistOwner: st.Q(),
		Events:        bus,
		Version:       version,
		UpdateRepo:    cfg.UpdateRepo,
		DataDir:       filepath.Dir(cfg.DBPath),
		Resolver:      resolverSvc,
		Overrides:     override.New(st.Q()),
		Play:          playSvc,
		Stats:         statsSvc,
		Scrobble:      scrobbleSvc,
		Lyrics: &lyrics.Service{
			Store: st.Q(),
			Client: &lyrics.LRCLibClient{
				UserAgent: "Reverb/" + version + " (https://github.com/uhhhm/reverb)",
			},
		},
		Pairing:      bundle.Pairing,
		SyncStore:    bundle.SyncStore,
		PairingStore: st.Q(),
		PairingDB:    st.DB(),
		OfflineSet:   st.Q(),
		LinkStore:    st.Q(),
		Desktop:      true,
	}
	if bundle.Aggregator != nil {
		deps.SearchAggregator = bundle.Aggregator
	}
	if bundle.Coverage != nil {
		deps.Coverage = bundle.Coverage
	}
	if bundle.Manager != nil {
		deps.Downloads = bundle.Manager
	}
	if bundle.Sync != nil {
		deps.Sync = bundle.Sync
	}
	if bundle.Supervisor != nil {
		sup := bundle.Supervisor
		deps.LibraryStatus = func() (string, string) {
			h := sup.Health()
			if h == embedded.HealthExternal {
				if bundle.Library != nil {
					return "external", "ready"
				}
				return "external", "unconfigured"
			}
			return "built-in", string(h)
		}
	}
	if deps.Pairing == nil {
		deps.Pairing = reverbsync.NewPairingService(st.Q())
	}
	if deps.SyncStore == nil {
		deps.SyncStore = reverbsync.NewSyncStore(st.Q())
	}

	// net.Listen on 127.0.0.1:port and http.Server with api.NewServer(deps).Handler()
	addr := fmt.Sprintf("127.0.0.1:%d", cfg.Port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		st.Close()
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

	app := NewApp()
	app.ln = ln
	app.srv = srv
	app.bundle = bundle
	app.deps = deps
	app.port = port
	app.store = st
	app.scrobble = scrobbleSvc
	app.ctx, app.cancel = context.WithCancel(context.Background())

	return app, nil
}

// StartServices starts the long-running background work boot() only wired up:
// the bundled Navidrome, the download manager, playlist sync and the scrobble
// worker. It is separate from boot() so constructing the composition root has
// no side effects — notably, booting it in a test must not spawn a second
// Navidrome on the fixed 4533 port.
func (a *App) StartServices() {
	if a.bundle.Supervisor != nil {
		a.bundle.Supervisor.Start()
	}
	if a.bundle.Manager != nil {
		a.bundle.Manager.Start()
	}
	if a.bundle.Sync != nil {
		go playlistsync.NewScheduler(a.bundle.Sync, 15*time.Minute).Run(context.Background())
		go func() {
			if err := a.bundle.Sync.MigrateLibraryPlaylists(context.Background()); err != nil {
				log.Printf("WARNING: library playlist migration: %v", err)
			}
		}()
	}
	if a.scrobble != nil {
		go a.scrobble.RunWorker(context.Background(), 30*time.Second)
	}
}

func seedBundledDownloader(ctx context.Context, q *db.Queries, getenv func(string) string) {
	instances, err := q.ListAdapterInstances(ctx)
	if err != nil {
		return
	}
	for _, inst := range instances {
		if inst.Type == "downloader" {
			return
		}
	}
	dir := getenv("REVERB_DOWNLOAD_DIR")
	if dir == "" {
		dir = "./downloads"
	}
	cfg, _ := json.Marshal(map[string]any{"output_dir": dir})
	if err := q.CreateAdapterInstance(ctx, db.CreateAdapterInstanceParams{
		ID:         uuid.NewString(),
		Type:       "downloader",
		Name:       "spotdl",
		Enabled:    1,
		Priority:   0,
		ConfigJson: string(cfg),
	}); err != nil {
		log.Printf("could not seed bundled spotdl downloader: %v", err)
		return
	}
	log.Printf("seeded bundled spotdl downloader (output_dir=%s)", dir)
}
