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
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"
	libp2phost "github.com/libp2p/go-libp2p/core/host"
	"github.com/uhhhm/reverb/internal/api"
	"github.com/uhhhm/reverb/internal/auth"
	"github.com/uhhhm/reverb/internal/catalog"
	"github.com/uhhhm/reverb/internal/core"
	"github.com/uhhhm/reverb/internal/cover"
	"github.com/uhhhm/reverb/internal/crop"
	"github.com/uhhhm/reverb/internal/download"
	"github.com/uhhhm/reverb/internal/download/lidarr"
	"github.com/uhhhm/reverb/internal/download/spotdl"
	"github.com/uhhhm/reverb/internal/download/ytdlp"
	"github.com/uhhhm/reverb/internal/events"
	"github.com/uhhhm/reverb/internal/extstream"
	"github.com/uhhhm/reverb/internal/library/embedded"
	"github.com/uhhhm/reverb/internal/library/localfiles"
	"github.com/uhhhm/reverb/internal/library/lyrics"
	"github.com/uhhhm/reverb/internal/library/subsonic"
	"github.com/uhhhm/reverb/internal/linkadd"
	"github.com/uhhhm/reverb/internal/materialize"
	"github.com/uhhhm/reverb/internal/notinterested"
	"github.com/uhhhm/reverb/internal/offlineset"
	"github.com/uhhhm/reverb/internal/override"
	"github.com/uhhhm/reverb/internal/p2p"
	"github.com/uhhhm/reverb/internal/play"
	"github.com/uhhhm/reverb/internal/player"
	"github.com/uhhhm/reverb/internal/playlistcrdt"
	"github.com/uhhhm/reverb/internal/playlistsync"
	"github.com/uhhhm/reverb/internal/portablemigrate"
	"github.com/uhhhm/reverb/internal/pyrun"
	"github.com/uhhhm/reverb/internal/recommend"
	"github.com/uhhhm/reverb/internal/recommend/listenbrainz"
	"github.com/uhhhm/reverb/internal/recommendationevent"
	"github.com/uhhhm/reverb/internal/registry"
	"github.com/uhhhm/reverb/internal/resolver"
	"github.com/uhhhm/reverb/internal/scrobble"
	"github.com/uhhhm/reverb/internal/scrobble/lastfm"
	listenbrainzupload "github.com/uhhhm/reverb/internal/scrobble/listenbrainz"
	"github.com/uhhhm/reverb/internal/search/deezer"
	"github.com/uhhhm/reverb/internal/search/spotify"
	"github.com/uhhhm/reverb/internal/store"
	reverbsync "github.com/uhhhm/reverb/internal/sync"
	"github.com/uhhhm/reverb/internal/syncemit"
	"github.com/uhhhm/reverb/internal/tastehistory"
	"github.com/uhhhm/reverb/internal/tastesettings"
	"github.com/uhhhm/reverb/internal/wiring"
)

// syncInterval is how often the playlist-sync scheduler ticks.
const syncInterval = 15 * time.Minute

// Profile is the kind of Device the composition root builds.
type Profile string

const (
	// ProfileDesktop is the desktop app and the server: a Navidrome library,
	// bundled or external, and the bundled download tools.
	ProfileDesktop Profile = ""
	// ProfilePhone is a reduced Device (ADR 0003). Its library is a folder of
	// files in its data directory, filled by P2P file sync, and it spawns no
	// executables: there is no Navidrome, and yt-dlp and spotDL run as Python
	// modules through Options.Python, for downloads and external streaming
	// alike. Portable-name migration, a desktop action, is left out.
	// Everything that replicates is wired as on the desktop.
	ProfilePhone Profile = "phone"
)

// phoneMusicDir is the folder a phone keeps its library in, inside its data
// directory. The platform hands the core a data directory and nothing else.
const phoneMusicDir = "music"

// Options is what genuinely differs between the two entry points.
type Options struct {
	DBPath     string
	Version    string
	UpdateRepo string
	// P2PPort is the libp2p listen port. Fixed by default so a peer address
	// entered on another device survives a restart; 0 picks a random port.
	P2PPort int
	Dev     bool
	// AllowedHosts are extra Host header values the API accepts beyond
	// loopback; see api.Deps.AllowedHosts.
	AllowedHosts []string
	// Desktop marks the Wails build, which the SPA uses to enable desktop-only
	// affordances.
	Desktop bool
	// Getenv is the environment source, injected so tests need no real env.
	Getenv func(string) string
	// Profile selects the kind of Device; the zero value is the desktop.
	Profile Profile
	// P2PNoDiscovery starts the libp2p host without mDNS or the DHT, so peers
	// are reached only by stored or supplied addresses; see p2p.HostOptions.
	P2PNoDiscovery bool
	// Python runs yt-dlp and spotDL on a phone, which has no executables to
	// spawn: the platform's embedded interpreter. Nil uses the host's Python
	// (REVERB_PYTHON, else python3), which is how the phone profile runs on
	// Linux. The desktop ignores it.
	Python pyrun.Runner
	// FreeSpace reports the bytes free on the disk holding a directory; a
	// phone stops fetching its offline set before the disk fills. Nil asks the
	// operating system. The desktop ignores it.
	FreeSpace func(dir string) (int64, error)
}

// Runtime is the built application: everything an entry point needs to serve
// requests, start background work, and shut down cleanly.
type Runtime struct {
	Deps     api.Deps
	Bundle   wiring.ServiceBundle
	Store    *store.Store
	Reloader *ServiceReloader
	Scrobble *scrobble.Service
	// TasteHistory keeps imported Last.fm history current once started.
	TasteHistory *tastehistory.Service
	// Recommend keeps Mixes and Home shelves current once started.
	Recommend *recommend.Service
	// Bus is the in-process event bus backing the WebSocket stream.
	Bus     *events.Bus
	P2PPort int
	P2P     *p2p.Host
	// P2PGuard is the libp2p peer trust set, set once the host starts.
	P2PGuard *p2p.Guard
	// P2PSyncer is the anti-entropy syncer, set once the host starts.
	P2PSyncer *p2p.Syncer
	// P2PPuller copies peers' files here, set once the host starts.
	P2PPuller         *p2p.Puller
	searchCredentials *p2p.Delegator
	Getenv            func(string) string

	// SyncEmit publishes locally-made changes; Playlists projects playlists in
	// both directions. StartBackground uses them for the one-time publish of
	// the history this device had before it could replicate any of it.
	SyncEmit  *syncemit.Service
	Playlists *playlistcrdt.Service
	projector *materialize.Service
	files     *p2p.FileSyncer
	// offline keeps a phone's offline set on it; nil on a desktop.
	offline *offlineset.Keeper
	catalog *catalog.Service
	profile Profile
	// noDiscovery is Options.P2PNoDiscovery, kept for StartBackground.
	noDiscovery bool
	// musicDir is the folder this device's files sync from and into; empty when
	// the library belongs to an external server Reverb must not write to.
	musicDir string

	// bg holds the background loops StartBackground launched, so Close can wait
	// for them to stop rather than returning while they still hold handles.
	bg sync.WaitGroup
}

// goLoop starts one supervised background loop and enrols it in r.bg.
//
// Enrolment is the point: a loop that is still running holds open handles — the
// file syncer's fsnotify watch on the music directory, a peer fetch's temp
// file. On Unix that is invisible, because a directory can be removed while a
// handle on it is open; on Windows the removal fails outright, so a Close that
// returned before its loops had stopped would leave the music directory
// undeletable and, in a test, fail the whole case at cleanup.
func (r *Runtime) goLoop(ctx context.Context, name string, fn func()) {
	r.bg.Add(1)
	go func() {
		defer r.bg.Done()
		p2p.SafeLoop(ctx, name, fn)
	}()
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

	phone := opts.Profile == ProfilePhone
	dataDir := filepath.Dir(opts.DBPath)
	if phone {
		SeedPhoneSearchSources(ctx, st.Q(), opts.Getenv)
	}
	// spotDL ships with both desktop builds, so present it as a configured
	// downloader out of the box when none exists yet. A phone has no spotDL
	// executable to present.
	if !phone {
		SeedBundledDownloader(ctx, st.Q(), opts.Getenv)
	}

	if serverID, err := reverbsync.EnsureServerDevice(ctx, st.Q()); err != nil {
		logf("WARNING: ensure server device: %v", err)
	} else {
		logf("server device %s ready", serverID)
	}
	if localID, err := reverbsync.EnsureLocalDevice(ctx, st.Q()); err != nil {
		logf("WARNING: ensure local device: %v", err)
	} else {
		logf("local device %s ready", localID)
	}

	// Registries — explicit registration at the composition root, no init()
	// side-effects.
	libraryReg := registry.NewRegistry("library")
	searchReg := registry.NewRegistry("search")
	searchReg.Register("spotify", func() registry.Plugin { return spotify.New() })
	searchReg.Register("deezer", func() registry.Plugin { return deezer.New() })
	downloaderReg := registry.NewRegistry("downloader")
	musicDir := embedded.MusicDir(opts.Getenv)
	python := opts.Python
	if phone {
		musicDir = filepath.Join(dataDir, phoneMusicDir)
		if python == nil {
			python = pyrun.HostFromEnv(opts.Getenv)
		}
		libraryReg.Register(localfiles.Name, func() registry.Plugin { return localfiles.New() })
		// The iPhone bundles yt-dlp but not spotDL (ADR 0003).
		if pyrun.Has(python, pyrun.SpotDL) {
			downloaderReg.Register("spotdl", func() registry.Plugin { return spotdl.NewInProcess(python) })
		}
		downloaderReg.Register("ytdlp", func() registry.Plugin { return ytdlp.NewInProcess(python) })
	} else {
		libraryReg.Register("subsonic", func() registry.Plugin { return subsonic.New() })
		downloaderReg.Register("spotdl", func() registry.Plugin { return spotdl.New() })
		downloaderReg.Register("lidarr", func() registry.Plugin { return lidarr.New() })
		downloaderReg.Register("ytdlp", func() registry.Plugin { return ytdlp.New() })
	}
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
		dataDir,
	)
	if phone {
		builder.SetLocalLibrary(musicDir)
	}

	// Construction order: reloader → resolver → SetResolverProvider → Build.
	//
	// The reloader owns the active snapshot, so it is created BEFORE Build and
	// the resolver singleton is constructed against it (the provider reads the
	// snapshot per-resolve; it is empty until Initialize below, which is fine —
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

	reloader.Initialize(bundle)

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
	// ListenBrainz uploads are opt-in: nothing is queued for it until the
	// owner pastes a user token in Settings.
	scrobbleSvc := scrobble.NewService(st.Q(), lastfm.New(), scrobbleCfg, time.Now, uuid.NewString).
		WithTokenProvider(scrobble.ListenBrainz, listenbrainzupload.New())

	// LinkAdd planner owns the add-from-link flow (resolve, catalog, sync,
	// chapter planning). It reads the LIVE aggregator for Spotify enrichment.
	syncStoreForLink := bundle.SyncStore
	if syncStoreForLink == nil {
		syncStoreForLink = reverbsync.NewSyncStore(st.Q())
	}
	linkOpts := []linkadd.Option{
		linkadd.WithTrackLookup(ProviderLookup{Get: reloader.TrackLookupProvider()}),
		linkadd.WithDeviceID(func(ctx context.Context) (string, error) {
			return reverbsync.AuthorDeviceID(ctx, st.Q())
		}),
	}
	// A phone's yt-dlp downloads one track at a time, so an album or playlist
	// link is added as its tracks.
	if phone {
		linkOpts = append(linkOpts, linkadd.WithCollections(ProviderCollections{Get: reloader.TrackLookupProvider()}))
	}
	linkAddSvc := linkadd.New(st.Q(), syncStoreForLink, bundle.Manager, linkOpts...)

	// Uploaded album and track art lives beside the database rather than in the
	// music library, which Reverb never writes to. Blobs are addressed by content
	// hash, so one image applied to many albums is stored once.
	coverSvc := cover.New(st.Q(), cover.Dir(filepath.Dir(opts.DBPath)))

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
		AllowedHosts:  opts.AllowedHosts,
		Desktop:       opts.Desktop,
		Version:       opts.Version,
		UpdateRepo:    opts.UpdateRepo,
		DataDir:       dataDir,
		MusicDir:      musicDir,
		Resolver:      resolverSvc,
		Catalog:       st.Q(),
		CatalogBrowse: st.Q(),
		Deletion:      bundle.Deletion,
		Overrides:     override.New(st.Q()),
		Entities:      override.NewEntities(st.Q()),
		Covers:        coverSvc,
		TrackQuality:  st.Q(),
		Crop:          crop.New(st.Q()),
		Loudness:      st.Q(),
		Duration:      st.Q(),
		Play:          playSvc,
		Stats:         statsSvc,
		Scrobble:      scrobbleSvc,
		Lyrics: &lyrics.Service{
			Store: st.Q(),
			Client: &lyrics.LRCLibClient{
				UserAgent: "Reverb/" + opts.Version + " (https://github.com/uhhhm/reverb)",
			},
		},
		Pairing:      bundle.Pairing,
		SyncStore:    bundle.SyncStore,
		PairingStore: st.Q(),
		DeviceKeys:   st.Q(),
		PairingDB:    st.DB(),
		OfflineSet:   st.Q(),
		LinkStore:    st.Q(),
		LinkAdd:      linkAddSvc,
		FileStore:    st.Q(),
	}
	// Every player plays the core's queue. Each change is also announced on the
	// event bus (session and revision only), for a client that did not make it.
	deps.Player = player.NewService(func(e player.Event) {
		bus.Publish(events.Event{Topic: player.TopicQueue, Payload: e})
	})
	// Track covers are keyed on the catalog id so they survive a library-backend
	// swap and can name the same track on a paired device.
	coverSvc.SetCatalogResolver(deps.Overrides.CatalogIDsForTracks)
	// Plays a search result that is not in the library by streaming it from the
	// source instead of downloading it. Reads the LIVE aggregator so it survives
	// adapter hot-reloads. Resolves persist: the signed URL stays good for hours,
	// and which upstream track this is never changes at all. The desktop runs
	// its bundled yt-dlp; a phone runs the module through its Python, and asks
	// for audio AVPlayer can open.
	if phone {
		deps.ExternalStream = extstream.New(
			ProviderLookup{Get: reloader.TrackLookupProvider()},
			extstream.WithRunner(pyrun.Module(python, pyrun.YtDlp)),
			extstream.WithFormat(extstream.AppleFormat),
			extstream.WithStore(st.Q()),
		)
	} else {
		deps.ExternalStream = extstream.NewFromEnv(
			ProviderLookup{Get: reloader.TrackLookupProvider()},
			opts.Getenv,
			extstream.WithStore(st.Q()),
		)
	}

	if deps.Pairing == nil {
		deps.Pairing = reverbsync.NewPairingService(st.Q())
	}
	if deps.SyncStore == nil {
		deps.SyncStore = reverbsync.NewSyncStore(st.Q())
	}
	// Everything replicated is keyed on an identity peers can agree on, and the
	// device that authors a change has to be named. Both are resolved here, once,
	// against the store, and handed to the emitters and the projection.
	authorDevice := func(ctx context.Context) string {
		id, err := reverbsync.AuthorDeviceID(ctx, st.Q())
		if err != nil {
			return ""
		}
		return id
	}
	emitter := syncemit.New(deps.SyncStore, catalogSvc, authorDevice)
	catalogSvc.WithEmitter(emitter)
	playSvc.WithEmitter(emitter)
	deps.SyncEmit = emitter
	deps.RecommendationEvents = recommendationevent.New(st.Q(), emitter, time.Now, uuid.NewString)
	// onDownloaded observes where a completed download landed; a phone keeps
	// it pending upload (set once its keeper exists, below).
	var onDownloaded func(ctx context.Context, path string)
	downloadCompletion := func(ctx context.Context, req core.DownloadRequest, path string) {
		if onDownloaded != nil && path != "" {
			onDownloaded(ctx, path)
		}
		if req.RecommendationOrigin == "" {
			return
		}
		if err := deps.RecommendationEvents.Record(ctx, req.InitiatedBy, string(req.RecommendationOrigin), recommendationevent.ActionLibrary); err != nil {
			log.Printf("recommendation library attribution: %v", err)
		}
	}
	builder.SetDownloadCompletionHook(downloadCompletion)
	if bundle.Manager != nil {
		bundle.Manager.SetCompletionHook(downloadCompletion)
	}
	// A download linked to its library track enters household browsing at once,
	// including a track deleted earlier and downloaded again. That holds on a
	// phone too: its Downloads are library, though its offline files are
	// copies, which never pass through the download manager.
	builder.SetDownloadLinkedHook(emitter.EnsureLibraryMembership)
	if bundle.Manager != nil {
		bundle.Manager.SetLinkedHook(emitter.EnsureLibraryMembership)
	}

	// Not interested marks replicate through the change log; the projection
	// below applies a peer's marks without emitting them again.
	marks := notinterested.New(st.Q(), emitter)
	deps.NotInterested = marks
	// Adventurousness and the Online recommendations switch belong to the
	// household's taste profile, so they replicate the same way.
	tasteSettings := tastesettings.New(st.Q(), emitter)
	deps.TasteSettings = tasteSettings
	onlineAllowed := func(ctx context.Context) bool {
		s, err := tasteSettings.Get(ctx)
		return err == nil && s.OnlineRecommendations
	}

	// Linked Last.fm history feeds the taste profile on this device only and
	// never becomes plays. Linking imports it, unlinking removes it, and like
	// every online lookup it is read only while online recommendations are on.
	tasteHistory := tastehistory.New(st.DB(),
		lastfm.NewHistory(lastfm.New(), func() string { return scrobbleCfg().APIKey }), time.Now, onlineAllowed)
	scrobbleSvc.OnLinkChange(tasteHistory.LinkChanged)

	// Recommendations read the LIVE sources, library and matcher, so an adapter
	// reload changes what they can ask without rebuilding the module. Last.fm
	// reuses the scrobbling API key; ListenBrainz's public datasets need no
	// account. The same ListenBrainz instance shares its request limiter across
	// recording and artist lookups. Ranking reads the taste profile's inputs and
	// the settings per request, so a peer's change applies to the next one.
	liveMatcher := reloader.MatcherProvider()
	listenbrainzSource := listenbrainz.New()
	recommender := recommend.New(reloader.SearchSourcesProvider(),
		recommend.WithLibrary(func() recommend.Library {
			if lib := reloader.Current().Library; lib != nil {
				return lib
			}
			return nil
		}),
		recommend.WithTrackSource(lastfm.NewSimilarity(lastfm.New(), func() string { return scrobbleCfg().APIKey })),
		recommend.WithTrackSource(listenbrainzSource),
		recommend.WithArtistSource(listenbrainzSource),
		// A connected ListenBrainz account adds its own recommendations.
		recommend.WithPersonalSource(listenbrainzSource.Personal(func(ctx context.Context) (string, error) {
			links, err := st.Q().ListActiveScrobbleLinksByProvider(ctx, scrobble.ListenBrainz)
			if err != nil || len(links) == 0 {
				return "", err
			}
			return links[0].Username, nil
		})),
		recommend.WithMatcher(func() recommend.Matcher {
			if m := liveMatcher(); m != nil {
				return m
			}
			return nil
		}),
		recommend.WithCatalogIDs(deps.Overrides.CatalogIDsForTracks),
		recommend.WithExclusions(func(ctx context.Context) (recommend.Exclusions, error) {
			return marks.Set(ctx)
		}),
		recommend.WithRecentPlays(func(ctx context.Context, since time.Time) ([]recommend.TrackCandidate, error) {
			rows, err := st.Q().ListPlayedSince(ctx, since.Unix())
			if err != nil {
				return nil, err
			}
			out := make([]recommend.TrackCandidate, len(rows))
			for i, r := range rows {
				out[i] = recommend.TrackCandidate{Artist: r.Artist, Title: r.Title}
			}
			return out, nil
		}),
		recommend.WithLocalSimilarity(recommend.NewLocalSimilarity(st.Q(), func() recommend.LocalLibrary {
			if lib := reloader.Current().Library; lib != nil {
				return lib
			}
			return nil
		})),
		recommend.WithTaste(tasteInputs{q: st.Q(), marks: marks, history: tasteHistory}),
		// Shelves and Mixes are seeded from plays and the library, and kept
		// in the local settings table between launches; they never replicate.
		recommend.WithListening(recommend.NewListening(st.Q())),
		recommend.WithStore(st.Q()),
		recommend.WithSettings(func(ctx context.Context) (recommend.Settings, error) {
			s, err := tasteSettings.Get(ctx)
			return recommend.Settings{Adventurousness: s.Adventurousness, Online: s.OnlineRecommendations}, err
		}),
	)
	deps.Recommend = recommender
	scrobbleSvc.OnLinkChange(func(ctx context.Context, _, provider string, _ bool) {
		if provider == scrobble.ListenBrainz {
			recommender.PersonalSourceChanged(ctx)
		}
	})

	playlistProjection := playlistcrdt.New(deps.SyncStore, wiring.NewSyncStore(st.Q()), authorDevice).
		WithCatalogLookup(catalogSvc.Lookup)
	if bundle.Sync != nil {
		bundle.Sync.WithEmitter(playlistProjection)
	}

	// Without a materializer, everything replicated would land in the change log
	// and stay invisible: nothing would write a peer's rename, playlist or play
	// into the tables the app reads.
	if bundle.Supervisor != nil && bundle.Supervisor.Health() == embedded.HealthExternal {
		musicDir = ""
	}
	localID, _ := reverbsync.LocalDeviceID(ctx, st.Q())
	// scheduleDownloadScan asks the live download manager to re-scan the music
	// directory. The capability is probed rather than declared because the
	// aggregator behind Downloads is hot-reloadable and not every adapter set
	// offers it.
	scheduleDownloadScan := func() {
		if downloader, ok := reloader.Current().Downloads.(interface{ ScheduleScan() }); ok {
			downloader.ScheduleScan()
		}
	}
	files := p2p.NewFileSyncer(st.Q(), localID, musicDir).WithOnChanged(scheduleDownloadScan)
	// A desktop replicates every file. A phone keeps only its offline set,
	// so the keeper decides what it fetches and prunes (ADR 0003).
	var offline *offlineset.Keeper
	if phone && musicDir != "" {
		offline = offlineset.NewKeeper(offlineset.KeeperConfig{
			Store: st.Q(),
			DeviceID: func(c context.Context) (string, error) {
				return reverbsync.ServerDeviceID(c, st.Q())
			},
			FileDeviceID: localID,
			MusicDir:     musicDir,
			FreeSpace:    opts.FreeSpace,
			Rescan: func(c context.Context) error {
				err := files.ScanAndSync(c)
				scheduleDownloadScan()
				return err
			},
		})
		files.WithProgress(offline.Progress)
		deps.OfflineKeeper = offline
		// A Download made here stays until a paired device holds it.
		onDownloaded = func(ctx context.Context, path string) {
			if err := offline.AddPending(ctx, path); err != nil {
				log.Printf("pending upload %s: %v", path, err)
			}
		}
	}
	// Renaming the library's existing files is what lets a Windows device pair
	// with a library built on Linux or macOS. It is exposed as a deliberate
	// action rather than run at startup, because it touches the owner's own
	// music folder.
	//
	// Reconciliation is three steps, and all three are needed: the embedded
	// library has to re-index the moved files or it serves paths that no longer
	// exist; the catalog's backend bindings have to be invalidated so the
	// resolver re-resolves each track to its new backend id; and the file
	// manifest is carried across by the migration itself, which is why it is
	// not repeated here. Listening history, playlists and ratings are keyed on
	// catalog ids, which a rename does not touch, so they follow for free.
	var portableMigration *portablemigrate.Service
	if musicDir != "" && !phone {
		portableMigration = portablemigrate.New(musicDir, func(c context.Context) error {
			scheduleDownloadScan()
			if lib := reloader.Current().Library; lib != nil {
				if err := lib.StartScan(c); err != nil {
					return err
				}
			}
			return resolverSvc.BumpCurrentEpoch(c)
		}).WithManifest(st.Q(), localID)
	}
	if portableMigration != nil {
		deps.PortableMigration = portableMigration
	}

	newMaterializer := func() *materialize.Service {
		return materialize.New(deps.Overrides, deps.Crop).
			WithFiles(files).
			WithCatalog(catalogSvc).
			WithPlaylists(playlistProjection).
			WithTrackStore(st.Q()).
			WithEntities(deps.Entities).
			WithCovers(coverSvc).
			WithNotInterested(marks).
			WithTasteSettings(tasteSettings)
	}
	projector := newMaterializer()
	deps.SyncStore.SetMaterializer(projector)
	notifyProjection := func() {
		bus.Publish(events.Event{Topic: "library.updated", Payload: core.LibraryUpdatedEvent{}})
		// A peer's playlist edit can add a track to, or take one from, the
		// offline set.
		if offline != nil {
			offline.Changed()
		}
	}
	deps.SyncStore.SetAfterProjection(notifyProjection)
	if syncStoreForLink != deps.SyncStore {
		syncStoreForLink.SetMaterializer(newMaterializer())
		syncStoreForLink.SetAfterProjection(notifyProjection)
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
	if phone {
		// The folder library is neither built-in nor external: uploads and
		// library deletion, which write to a library this device owns, stay
		// with the desktop.
		deps.LibraryStatus = func() (string, string) { return "local", "ready" }
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

	rt := &Runtime{
		Deps:         deps,
		Bus:          bus,
		Bundle:       bundle,
		Store:        st,
		Reloader:     reloader,
		Scrobble:     scrobbleSvc,
		TasteHistory: tasteHistory,
		Recommend:    recommender,
		P2PPort:      opts.P2PPort,
		Getenv:       opts.Getenv,

		SyncEmit:    emitter,
		Playlists:   playlistProjection,
		projector:   projector,
		files:       files,
		offline:     offline,
		catalog:     catalogSvc,
		profile:     opts.Profile,
		noDiscovery: opts.P2PNoDiscovery,
		musicDir:    musicDir,
	}
	rt.Deps.P2P = func() *p2p.Host { return rt.P2P }
	rt.Deps.P2PGuard = func() *p2p.Guard { return rt.P2PGuard }
	rt.Deps.P2PSyncer = func() *p2p.Syncer { return rt.P2PSyncer }
	if phone {
		delegator := p2p.NewDelegator(
			func() libp2phost.Host {
				if rt.P2P == nil {
					return nil
				}
				return rt.P2P.LibHost()
			},
			func() *p2p.Guard { return rt.P2PGuard },
			st.Q(),
		)
		rt.Deps.DelegatedStream = delegator
		rt.searchCredentials = delegator
	}
	return rt, nil
}

// StartBackground starts the long-running work Build only wired up: the bundled
// Navidrome, the download manager, the playlist-sync scheduler and its one-time
// library-playlist migration, and the scrobble worker. Kept separate from Build
// so constructing the root stays side-effect free.
func (r *Runtime) StartBackground(ctx context.Context) {
	// P2P host for LAN + WAN discovery (mDNS, DHT, relay). Best-effort; if it
	// fails we log and continue — sync still works via HTTP.
	if r.P2P == nil {
		priv, kerr := p2p.LoadOrCreateIdentity(ctx, r.Store.Q())
		if kerr != nil {
			logf("WARNING: p2p identity: %v", kerr)
		} else if h, err := p2p.NewHostWith(ctx, priv, r.P2PPort, p2p.HostOptions{NoDiscovery: r.noDiscovery}); err != nil {
			logf("WARNING: p2p host: %v", err)
		} else {
			r.P2P = h
			logf("p2p host %s ready addrs=%v", h.ID(), h.DialAddrs())
			// Peer trust set. Every handler except pairing is gated on it:
			// mDNS and DHT connect us to strangers, so a live connection means
			// nothing until a pairing code has been exchanged.
			guard := p2p.NewGuard(r.Store.Q())
			r.P2PGuard = guard
			if r.Deps.Pairing != nil && h.LibHost() != nil {
				p2p.RegisterPairingHandler(h.LibHost(), r.Deps.Pairing, guard, r.Store.Q(), func(c context.Context) (string, error) {
					return reverbsync.LocalDeviceID(c, r.Store.Q())
				})
			}
			// File sync: hash local music dir and keep file_manifest up to date.
			musicDir := r.musicDir
			coverDir := cover.Dir(r.Deps.DataDir)
			if h.LibHost() != nil {
				p2p.RegisterFileHandler(h.LibHost(), musicDir, guard)
				p2p.RegisterCoverHandler(h.LibHost(), coverDir, guard)
				p2p.RegisterDelegatedHandler(h.LibHost(), guard, r.openDelegatedStream, r.openDelegatedCover)
				p2p.RegisterSearchCredentialsHandler(h.LibHost(), guard, r.spotifyCredentials)
			}
			localID, lerr := reverbsync.LocalDeviceID(ctx, r.Store.Q())
			if lerr != nil || localID == "" {
				if id2, err2 := reverbsync.EnsureLocalDevice(ctx, r.Store.Q()); err2 == nil && id2 != "" {
					localID = id2
				} else {
					logf("WARNING: p2p file sync: local device not ready: %v", lerr)
				}
			}
			if localID != "" {
				// Install the signing key for locally-authored changes and
				// publish our own verification key, so peers can verify our
				// changes when they arrive relayed by someone else.
				if priv != nil {
					if raw, rerr := priv.Raw(); rerr == nil && len(raw) == ed25519.PrivateKeySize {
						r.Deps.SyncStore.SetSigner(ed25519.PrivateKey(raw), localID)
						if n, err := r.Deps.SyncStore.RecoverUnusableChanges(ctx); err != nil {
							logf("WARNING: sync: repair unusable changes: %v", err)
						} else if n > 0 {
							logf("sync: quarantined %d unusable change(s); requesting authentic copies where there are any", n)
						}
						if pubB64, perr := p2p.PublicKeyBase64(h.LibHost().ID()); perr == nil {
							if err := r.Deps.SyncStore.RecordDeviceKey(ctx, localID, pubB64); err != nil {
								logf("WARNING: p2p: record local device key: %v", err)
							}
						}
						if n, err := r.Deps.SyncStore.BackfillLocalSignatures(ctx); err != nil {
							logf("WARNING: p2p: backfill local signatures: %v", err)
						} else if n > 0 {
							logf("p2p: backfilled %d local signature(s)", n)
						}
					}
				}
				fs := r.files
				if musicDir != "" {
					r.goLoop(ctx, "file sync", func() { fs.Run(ctx) })
				}
				// Advertise what we hold and pull what paired peers hold.
				if h.LibHost() != nil {
					p2p.RegisterManifestHandler(h.LibHost(), r.Store.Q(), localID, guard)
					puller := p2p.NewPuller(h.LibHost(), r.Store.Q(), fs, guard, localID, musicDir).
						WithCovers(r.Store.Q(), coverDir)
					if r.offline != nil {
						puller.WithSelection(r.offline)
						r.offline.SetKick(puller.Kick)
					}
					r.P2PPuller = puller
					r.goLoop(ctx, "file pull", func() { puller.Run(ctx) })
				}
				// P2P anti-entropy for sync changes over libp2p.
				if r.Deps.SyncStore != nil && h.LibHost() != nil {
					p2p.RegisterSyncHandler(h.LibHost(), r.Deps.SyncStore, guard, r.Store.Q())
					syncer := p2p.NewSyncer(h.LibHost(), r.Deps.SyncStore, guard, r.Store.Q(), localID)
					syncer.SetBus(r.Bus)
					r.P2PSyncer = syncer
					r.goLoop(ctx, "syncer", func() { _ = syncer.Run(ctx) })
				}
			}
		}
	}
	if r.projector != nil {
		if err := r.projector.RecoverPlays(ctx); err != nil {
			logf("sync: recover play history: %v", err)
		}
	}
	if r.Deps.SyncStore != nil {
		r.goLoop(ctx, "projection recovery", func() { r.Deps.SyncStore.RunProjectionRecovery(ctx) })
	}
	if r.Bundle.Supervisor != nil {
		r.Bundle.Supervisor.Start()
	}
	if r.Reloader != nil {
		r.Reloader.Start()
	}
	// The bundled library reports ready once Navidrome is serving. Re-running the
	// backfill then heals the boot race, where the backfill at Start() fired
	// before Navidrome was up.
	if r.Bundle.Supervisor != nil && r.Bundle.Manager != nil {
		go WaitReadyThenBackfill(ctx, r.Bundle.Supervisor.Ready, r.Bundle.Manager.BackfillUnlinked)
	}
	// Replication only ever carried referenced catalog entities before, so a
	// library built up over months would reach a newly paired device incomplete.
	// Publish existing history once, then enumerate library metadata on every
	// boot so files added outside Reverb also become visible to peers; downloads
	// join through the manager's linked hook. Keep the passes serial: both can
	// ensure catalog entities in the sync log. A bundled Navidrome is enumerated
	// only once it is serving, or the boot pass would find nothing.
	if r.SyncEmit != nil {
		go func() {
			r.SyncEmit.BackfillHistory(ctx, r.Store.Q(), r.Playlists)
			if sup := r.Bundle.Supervisor; sup != nil && sup.Health() != embedded.HealthExternal {
				WaitReadyThenBackfill(ctx, sup.Ready, func() { r.publishLibrary(ctx) })
				return
			}
			r.publishLibrary(ctx)
		}()
	}
	if r.Bundle.Sync != nil {
		go playlistsync.NewScheduler(r.Bundle.Sync, syncInterval).Run(ctx)
		// Background so startup is not blocked; guarded by a settings flag. A
		// phone skips it: playlist files that reached its folder from a peer
		// are that peer's, and adopting them would replicate duplicates.
		go func() {
			if r.profile == ProfilePhone {
				return
			}
			if err := r.Bundle.Sync.MigrateLibraryPlaylists(ctx); err != nil {
				logf("WARNING: library playlist migration: %v", err)
			}
		}()
	}
	if r.Scrobble != nil {
		go r.Scrobble.RunWorker(ctx, 30*time.Second)
	}
	if r.TasteHistory != nil {
		go r.TasteHistory.Run(ctx, time.Hour)
	}
	// Mixes refresh at local midnight on their weekday; a device that was
	// asleep then catches up here on launch.
	if r.Recommend != nil {
		go r.Recommend.RunSchedule(ctx)
	}
}

// publishLibrary marks the live library's tracks as household catalogue
// members. The phone's offline files are copies, not library membership.
func (r *Runtime) publishLibrary(ctx context.Context) {
	if r.profile == ProfilePhone {
		return
	}
	library := r.Bundle.Library
	if r.Reloader != nil {
		if current := r.Reloader.Current().Library; current != nil {
			library = current
		}
	}
	if browser, ok := library.(syncemit.LibraryBrowser); ok {
		r.SyncEmit.PublishLibrary(ctx, browser, r.catalog)
	}
}

// CopySpotifyCredentials is called by the embedded iOS core after pairing.
// The response never passes through HTTP, replication, or the SQLite store.
func (r *Runtime) CopySpotifyCredentials(ctx context.Context) (p2p.SearchCredentials, error) {
	if r == nil || r.profile != ProfilePhone || r.searchCredentials == nil {
		return p2p.SearchCredentials{}, errors.New("search credential copy unavailable")
	}
	return r.searchCredentials.CopySearchCredentials(ctx)
}

func (r *Runtime) openDelegatedCover(ctx context.Context, catalogID string, size int) (core.CoverArt, error) {
	if r.Deps.Resolver == nil || r.Bundle.Library == nil {
		return core.CoverArt{}, core.ErrLibraryItemNotFound
	}
	addr, err := r.Deps.Resolver.Resolve(ctx, catalogID)
	if err != nil {
		return core.CoverArt{}, err
	}
	if !addr.Found || addr.CoverArtID == "" {
		return core.CoverArt{}, core.ErrLibraryItemNotFound
	}
	return r.Bundle.Library.CoverArt(ctx, addr.CoverArtID, size)
}

func (r *Runtime) spotifyCredentials(ctx context.Context) (p2p.SearchCredentials, error) {
	// A phone never re-serves a copied secret, but answers so another phone
	// asking it counts as a definite "no" rather than an unreachable device.
	if r.profile == ProfilePhone {
		return p2p.SearchCredentials{}, p2p.ErrNoSearchCredentials
	}
	rows, err := r.Store.Q().ListAdapterInstances(ctx)
	if err != nil {
		return p2p.SearchCredentials{}, err
	}
	for _, row := range rows {
		if row.Type != "search" || row.Name != "spotify" || row.Enabled != 1 {
			continue
		}
		cfg := map[string]any{}
		if row.ConfigJson != "" {
			if err := json.Unmarshal([]byte(row.ConfigJson), &cfg); err != nil {
				return p2p.SearchCredentials{}, errors.New("Spotify source configuration is invalid")
			}
		}
		wiring.ApplySpotifyEnv(cfg, r.Getenv)
		id, _ := cfg["client_id"].(string)
		secret, _ := cfg["client_secret"].(string)
		if id != "" && secret != "" {
			return p2p.SearchCredentials{ClientID: id, ClientSecret: secret}, nil
		}
	}
	return p2p.SearchCredentials{}, p2p.ErrNoSearchCredentials
}

func (r *Runtime) openDelegatedStream(ctx context.Context, catalogID string, opts core.StreamOpts, byteRange string) (core.StreamHandle, error) {
	if r.Deps.Resolver == nil || r.Reloader == nil {
		return core.StreamHandle{}, core.ErrLibraryItemNotFound
	}
	addr, err := r.Deps.Resolver.Resolve(ctx, catalogID)
	if err != nil {
		return core.StreamHandle{}, err
	}
	if !addr.Found || addr.BackendID == "" {
		return core.StreamHandle{}, core.ErrLibraryItemNotFound
	}
	lib := r.Reloader.Current().Library
	if lib == nil {
		return core.StreamHandle{}, core.ErrLibraryItemNotFound
	}
	return lib.Stream(ctx, addr.BackendID, opts, byteRange)
}

// Close stops the download manager and closes the store. The HTTP server and the
// library supervisor are shut down by the entry point, which owns their
// lifecycles.
// Close releases everything StartBackground acquired. The caller cancels the
// context it passed to StartBackground first; Close then waits for the loops
// that context stops, because a loop still running holds handles on the music
// directory and the database that the next thing to touch them — a Windows
// RemoveAll, a reopen of the store — will be refused.
//
// The wait is bounded. A loop wedged in a slow network read must not turn
// quitting the app into a hang; after the grace period Close carries on and
// closes the store anyway, which is the same outcome the old unconditional
// close gave.
func (r *Runtime) Close() {
	if r.P2P != nil {
		_ = r.P2P.Close()
	}
	r.waitForBackground(backgroundStopGrace)
	if r.Reloader != nil {
		r.Reloader.Close()
	}
	if r.Store != nil {
		_ = r.Store.Close()
	}
}

// backgroundStopGrace bounds how long Close waits for the background loops. The
// loops themselves return promptly on cancellation; the grace covers a fetch or
// a scan that is mid-syscall when the cancel lands.
const backgroundStopGrace = 10 * time.Second

func (r *Runtime) waitForBackground(grace time.Duration) {
	done := make(chan struct{})
	go func() {
		r.bg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(grace):
		logf("WARNING: background loops did not stop within %s; closing anyway", grace)
	}
}
