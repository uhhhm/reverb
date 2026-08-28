// Package app is Reverb's composition root. Both entry points — the server
// (cmd/reverb) and the desktop app (desktop) — build their services here, so a
// dependency is wired once rather than once per binary. They previously each
// hand-assembled an api.Deps, and the copies drifted: the desktop build silently
// lacked live adapter reload and external streaming, with no compile error to
// catch it.
//
// Entry points keep what is genuinely theirs: how they listen, and how they
// shut down.
package app

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/uhhhm/reverb/internal/api"
	"github.com/uhhhm/reverb/internal/auth"
	"github.com/uhhhm/reverb/internal/catalog"
	"github.com/uhhhm/reverb/internal/download"
	"github.com/uhhhm/reverb/internal/download/lidarr"
	"github.com/uhhhm/reverb/internal/download/spotdl"
	"github.com/uhhhm/reverb/internal/download/ytdlp"
	"github.com/uhhhm/reverb/internal/events"
	"github.com/uhhhm/reverb/internal/extstream"
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
	reverbsync "github.com/uhhhm/reverb/internal/sync"
	"github.com/uhhhm/reverb/internal/wiring"
)

// syncInterval is how often the playlist-sync scheduler ticks.
const syncInterval = 15 * time.Minute

// Options is what genuinely differs between the two entry points.
type Options struct {
	DBPath     string
	Version    string
	UpdateRepo string
	Dev        bool
	// Desktop marks the Wails build, which the SPA uses to enable desktop-only
	// affordances.
	Desktop bool
	// Getenv is the environment source, injected so tests need no real env.
	Getenv func(string) string
}

// Runtime is the built application: everything an entry point needs to serve
// requests, start background work, and shut down cleanly.
type Runtime struct {
	Deps     api.Deps
	Bundle   wiring.ServiceBundle
	Store    *store.Store
	Reloader *ServiceReloader
	Scrobble *scrobble.Service
}

// Build opens the store, runs migrations, constructs every service, and returns
// them wired into an api.Deps. It starts nothing — see StartBackground — so that
// constructing the root has no side effects, which is what lets a test build it
// without spawning a second Navidrome on the fixed 4533 port.
//
// On error the store is closed; on success the caller owns it via Runtime.Close.
func Build(ctx context.Context, opts Options) (*Runtime, error) {
	if opts.Getenv == nil {
		return nil, fmt.Errorf("app: Getenv is required")
	}

	st, err := store.Open(opts.DBPath)
	if err != nil {
		return nil, err
	}
	rt, err := build(ctx, opts, st)
	if err != nil {
		st.Close()
		return nil, err
	}
	return rt, nil
}

func build(ctx context.Context, opts Options, st *store.Store) (*Runtime, error) {
	if err := st.Migrate(); err != nil {
		return nil, err
	}

	authSvc := auth.NewService(st.Q(), time.Now)
	// The single local user row is the FK target for download_jobs.initiated_by
	// and synced_playlists.owner_user_id. Idempotent.
	if err := authSvc.EnsureSeed(ctx); err != nil {
		return nil, fmt.Errorf("seed identity: %w", err)
	}

	// spotDL ships with both builds, so present it as a configured downloader out
	// of the box when none exists yet.
	SeedBundledDownloader(ctx, st.Q(), opts.Getenv)

	if serverID, err := reverbsync.EnsureServerDevice(ctx, st.Q()); err != nil {
		logf("WARNING: ensure server device: %v", err)
	} else {
		logf("server device %s ready", serverID)
	}

	// Registries — explicit registration at the composition root, no init()
	// side-effects.
	libraryReg := registry.NewRegistry("library")
	libraryReg.Register("subsonic", func() registry.Plugin { return subsonic.New() })
	searchReg := registry.NewRegistry("search")
	searchReg.Register("spotify", func() registry.Plugin { return spotify.New() })
	searchReg.Register("deezer", func() registry.Plugin { return deezer.New() })
	downloaderReg := registry.NewRegistry("downloader")
	downloaderReg.Register("spotdl", func() registry.Plugin { return spotdl.New() })
	downloaderReg.Register("lidarr", func() registry.Plugin { return lidarr.New() })
	downloaderReg.Register("ytdlp", func() registry.Plugin { return ytdlp.New() })
	// Surfaces the async capability to the admin UI (/adapters/available).
	registry.RegisterCapability("async", func(p registry.Plugin) bool {
		_, ok := p.(download.AsyncDownloader)
		return ok
	})

	// EventBus backs both the WS endpoint and the Manager's typed events.
	bus := events.New()
	dirty := &AtomicDirty{}

	builder := wiring.NewBuilder(
		libraryReg, searchReg, downloaderReg,
		st.Q(), st, bus, download.RealClock{}, opts.Getenv,
		filepath.Dir(opts.DBPath),
	)

	// Construction order: reloader → resolver → SetResolverProvider → Build.
	//
	// The reloader owns the live-matcher holder, so it is created BEFORE Build and
	// the resolver singleton is constructed against it (the provider reads the
	// holder per-resolve; it is empty until PublishMatcher below, which is fine —
	// live services only Resolve at runtime).
	//
	// SetResolverProvider must precede the first Build so download.Manager and
	// playlistsync.Service, both constructed inside Build, receive the resolver.
	reloader := NewServiceReloader(builder)
	resolverSvc := resolver.NewService(st.Q(), reloader.MatcherProvider(), time.Now)
	builder.SetResolverProvider(func() wiring.BindingResolver { return resolverSvc })

	// catalogSvc is backend-independent (store + time + uuid), so it is built
	// before Build and injected into the Manager after. SetCanonicalMinter must
	// precede Build so BuildSyncService picks it up.
	catalogSvc := catalog.NewService(st.Q(), time.Now, uuid.NewString)
	builder.SetCanonicalMinter(catalogSvc)

	bundle, err := builder.Build(ctx)
	if err != nil {
		return nil, err
	}

	// Publish the boot matcher and search aggregator (either may be nil) so the
	// long-lived resolver and stream service see them on their first call.
	reloader.PublishMatcher(bundle.Matcher)
	if bundle.Aggregator != nil {
		reloader.PublishTrackLookup(bundle.Aggregator)
	}

	if bundle.Manager != nil {
		bundle.Manager.SetCanonicalMinter(catalogSvc)
	}

	playSvc := play.NewService(st.Q(), catalogSvc, time.Now, uuid.NewString)
	statsSvc := play.NewStats(st.Q())

	// cfg() reads the app key/secret from settings on every call, so an admin
	// change takes effect without a restart.
	scrobbleCfg := func() scrobble.Creds {
		key, _ := st.Q().GetSetting(context.Background(), "scrobble:lastfm:api_key")
		secret, _ := st.Q().GetSetting(context.Background(), "scrobble:lastfm:api_secret")
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
		ConfigDirty:   dirty,
		Reload:        reloader,
		Dev:           opts.Dev,
		Desktop:       opts.Desktop,
		Version:       opts.Version,
		UpdateRepo:    opts.UpdateRepo,
		DataDir:       filepath.Dir(opts.DBPath),
		Resolver:      resolverSvc,
		// Plays a search result that is not in the library by streaming it from
		// the source instead of downloading it. Reads the LIVE aggregator so it
		// survives adapter hot-reloads.
		ExternalStream: extstream.NewFromEnv(
			ProviderLookup{Get: reloader.TrackLookupProvider()},
			opts.Getenv,
		),
		Overrides: override.New(st.Q()),
		Play:      playSvc,
		Stats:     statsSvc,
		Scrobble:  scrobbleSvc,
		Lyrics: &lyrics.Service{
			Store: st.Q(),
			Client: &lyrics.LRCLibClient{
				UserAgent: "Reverb/" + opts.Version + " (https://github.com/uhhhm/reverb)",
			},
		},
		Pairing:      bundle.Pairing,
		SyncStore:    bundle.SyncStore,
		PairingStore: st.Q(),
		PairingDB:    st.DB(),
		OfflineSet:   st.Q(),
		LinkStore:    st.Q(),
	}
	if deps.Pairing == nil {
		deps.Pairing = reverbsync.NewPairingService(st.Q())
	}
	if deps.SyncStore == nil {
		deps.SyncStore = reverbsync.NewSyncStore(st.Q())
	}
	// Only set an interface field when the concrete service is present, or it
	// becomes a non-nil interface wrapping a nil pointer.
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
		// Boot-bound: backend-mode changes are restart-only, so the bundle is
		// immutable after wiring and the unsynchronised bundle.Library read is safe.
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

	return &Runtime{
		Deps:     deps,
		Bundle:   bundle,
		Store:    st,
		Reloader: reloader,
		Scrobble: scrobbleSvc,
	}, nil
}

// StartBackground starts the long-running work Build only wired up: the bundled
// Navidrome, the download manager, the playlist-sync scheduler and its one-time
// library-playlist migration, and the scrobble worker. Kept separate from Build
// so constructing the root stays side-effect free.
func (r *Runtime) StartBackground(ctx context.Context) {
	if r.Bundle.Supervisor != nil {
		r.Bundle.Supervisor.Start()
	}
	if r.Bundle.Manager != nil {
		r.Bundle.Manager.Start()
	}
	// The bundled library reports ready once Navidrome is serving. Re-running the
	// backfill then heals the boot race, where the backfill at Start() fired
	// before Navidrome was up.
	if r.Bundle.Supervisor != nil && r.Bundle.Manager != nil {
		go WaitReadyThenBackfill(ctx, r.Bundle.Supervisor.Ready, r.Bundle.Manager.BackfillUnlinked)
	}
	if r.Bundle.Sync != nil {
		go playlistsync.NewScheduler(r.Bundle.Sync, syncInterval).Run(ctx)
		// Background so startup is not blocked; guarded by a settings flag.
		go func() {
			if err := r.Bundle.Sync.MigrateLibraryPlaylists(ctx); err != nil {
				logf("WARNING: library playlist migration: %v", err)
			}
		}()
	}
	if r.Scrobble != nil {
		go r.Scrobble.RunWorker(ctx, 30*time.Second)
	}
}

// Close stops the download manager and closes the store. The HTTP server and the
// library supervisor are shut down by the entry point, which owns their
// lifecycles.
func (r *Runtime) Close() {
	if r.Bundle.Manager != nil {
		r.Bundle.Manager.Stop()
	}
	if r.Store != nil {
		_ = r.Store.Close()
	}
}
