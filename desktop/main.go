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
	"github.com/maxjb-xyz/reverb/internal/api"
	"github.com/maxjb-xyz/reverb/internal/auth"
	"github.com/maxjb-xyz/reverb/internal/catalog"
	"github.com/maxjb-xyz/reverb/internal/config"
	"github.com/maxjb-xyz/reverb/internal/desktop"
	"github.com/maxjb-xyz/reverb/internal/download"
	"github.com/maxjb-xyz/reverb/internal/download/lidarr"
	"github.com/maxjb-xyz/reverb/internal/download/spotdl"
	"github.com/maxjb-xyz/reverb/internal/events"
	"github.com/maxjb-xyz/reverb/internal/library/embedded"
	"github.com/maxjb-xyz/reverb/internal/library/lyrics"
	"github.com/maxjb-xyz/reverb/internal/library/subsonic"
	"github.com/maxjb-xyz/reverb/internal/play"
	"github.com/maxjb-xyz/reverb/internal/playlistsync"
	"github.com/maxjb-xyz/reverb/internal/registry"
	"github.com/maxjb-xyz/reverb/internal/resolver"
	"github.com/maxjb-xyz/reverb/internal/scrobble"
	"github.com/maxjb-xyz/reverb/internal/scrobble/lastfm"
	"github.com/maxjb-xyz/reverb/internal/search/deezer"
	"github.com/maxjb-xyz/reverb/internal/search/spotify"
	"github.com/maxjb-xyz/reverb/internal/store"
	"github.com/maxjb-xyz/reverb/internal/store/db"
	reverbsync "github.com/maxjb-xyz/reverb/internal/sync"
	"github.com/maxjb-xyz/reverb/internal/wiring"
)

var version = "dev"

func main() {
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

	cfg, err := config.Load(os.Args[1:], os.Getenv)
	if err != nil {
		log.Fatal(err)
	}
	// Override Port=0 (random) unless --port arg or REVERB_PORT is set.
	hasPortArg := false
	for _, arg := range os.Args[1:] {
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
		log.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(); err != nil {
		log.Fatal(err)
	}

	authSvc := auth.NewService(st.Q(), time.Now)
	if err := authSvc.EnsureSeed(context.Background()); err != nil {
		log.Fatalf("seed identity: %v", err)
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
		log.Fatal(err)
	}
	if bundle.Supervisor != nil {
		bundle.Supervisor.Start()
	}
	if bundle.Manager != nil {
		bundle.Manager.SetCanonicalMinter(catalogSvc)
		bundle.Manager.Start()
		defer bundle.Manager.Stop()
	}
	if bundle.Sync != nil {
		go playlistsync.NewScheduler(bundle.Sync, 15*time.Minute).Run(context.Background())
		go func() {
			if err := bundle.Sync.MigrateLibraryPlaylists(context.Background()); err != nil {
				log.Printf("WARNING: library playlist migration: %v", err)
			}
		}()
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
	go scrobbleSvc.RunWorker(context.Background(), 30*time.Second)

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
		DataDir:       filepath.Dir(cfg.DBPath),
		Resolver:      resolverSvc,
		Play:          playSvc,
		Stats:         statsSvc,
		Scrobble:      scrobbleSvc,
		Lyrics: &lyrics.Service{
			Store: st.Q(),
			Client: &lyrics.LRCLibClient{
				UserAgent: "Reverb/" + version + " (https://github.com/maxjb-xyz/reverb)",
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
		log.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	log.Printf("desktop reverb listening on 127.0.0.1:%d (dev=%v)", port, cfg.Dev)

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
	app.ctx, app.cancel = context.WithCancel(context.Background())

	// TODO: future Wails Run — when github.com/wailsapp/wails/v2 is added to go.mod,
	// replace the fallback plain HTTP serve below with:
	//   wails.Run(&options.App{
	//     Title: "Reverb", Width: 1200, Height: 800,
	//     AssetServer: &assetserver.Options{Assets: assets},
	//     OnStartup: app.OnStartup, OnShutdown: app.OnShutdown, OnBeforeClose: app.OnBeforeClose,
	//     Bind: []interface{}{app},
	//   })
	// For now, fallback to plain HTTP serve so `go run ./desktop` works without wails.
	log.Printf("desktop fallback HTTP serving on http://127.0.0.1:%d", port)
	if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
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
