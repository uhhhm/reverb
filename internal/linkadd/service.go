// Package linkadd owns the add-from-link flow: resolving a pasted URL,
// minting a stable catalog entity, emitting sync changes, and planning the
// download requests (including chapter expansion). The HTTP handlers in
// internal/api are thin shims over this service.
package linkadd

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/uhhhm/reverb/internal/core"
	"github.com/uhhhm/reverb/internal/linkresolve"
	"github.com/uhhhm/reverb/internal/store/db"
	reverbsync "github.com/uhhhm/reverb/internal/sync"
	"golang.org/x/sync/errgroup"
)

// LinkStore is the persistence slice the planner needs.
type LinkStore interface {
	InsertCatalogEntity(ctx context.Context, arg db.InsertCatalogEntityParams) error
	GetCatalogEntity(ctx context.Context, id string) (db.CatalogEntity, error)
	GetSyncedPlaylist(ctx context.Context, id string) (db.SyncedPlaylist, error)
}

// SyncStore emits durable sync changes.
type SyncStore interface {
	AppendChange(ctx context.Context, deviceID string, ch reverbsync.SyncChange) (int64, error)
}

// Downloader enqueues downloads.
type Downloader interface {
	Enqueue(ctx context.Context, req core.DownloadRequest) (core.DownloadJob, error)
}

// ChapterLister is optionally implemented by the Downloader for chapter splits.
type ChapterLister interface {
	ListChapters(ctx context.Context, url string) ([]core.Chapter, error)
}

// TrackLookup enriches Spotify resolves with real metadata.
type TrackLookup interface {
	GetTrack(ctx context.Context, source, externalID string) (core.ExternalResult, error)
}

// Collections lists what an album or playlist link holds. *search.Aggregator,
// through the composition root, fits.
type Collections interface {
	GetAlbum(ctx context.Context, source, id string) (core.ExternalAlbum, error)
	GetPlaylist(ctx context.Context, source, id string) (core.ExternalPlaylist, error)
}

// AddOptions is one link's user intent.
type AddOptions struct {
	URL           string
	PlaylistID    *string // nil means library only
	Download      *bool   // nil means true
	Quality       string
	StartTime     string
	EndTime       string
	SplitChapters bool
	InitiatedBy   string // user ID, set server-side
}

// AddResult is the outcome for one link.
type AddResult struct {
	URL           string                     `json:"url"`
	Resolve       *linkresolve.ResolveResult `json:"resolve"`
	CatalogID     string                     `json:"catalogId"`
	PlaylistID    string                     `json:"playlistId,omitempty"`
	Job           *core.DownloadJob          `json:"job,omitempty"`
	Jobs          []core.DownloadJob         `json:"jobs,omitempty"`
	DownloadError string                     `json:"downloadError,omitempty"`
}

// BatchItemResult is the per-link outcome for the batch endpoint, including errors.
type BatchItemResult struct {
	URL           string                     `json:"url"`
	Resolve       *linkresolve.ResolveResult `json:"resolve,omitempty"`
	CatalogID     string                     `json:"catalogId,omitempty"`
	PlaylistID    string                     `json:"playlistId,omitempty"`
	Job           *core.DownloadJob          `json:"job,omitempty"`
	Jobs          []core.DownloadJob         `json:"jobs,omitempty"`
	DownloadError string                     `json:"downloadError,omitempty"`
	Error         string                     `json:"error,omitempty"`
}

// Service owns the planning.
type Service struct {
	store         LinkStore
	syncStore     SyncStore
	downloader    Downloader
	chapterLister ChapterLister
	lookup        TrackLookup
	collections   Collections
	deviceID      func(context.Context) (string, error)
	now           func() time.Time
	mu            sync.RWMutex
}

// Option configures the Service.
type Option func(*Service)

func WithTrackLookup(l TrackLookup) Option { return func(s *Service) { s.lookup = l } }
func WithNow(fn func() time.Time) Option   { return func(s *Service) { s.now = fn } }

// WithCollections expands album and playlist links into their tracks: each
// track is catalogued, joins the playlist, and is downloaded on its own. A
// device whose downloader fetches one track at a time (a phone's yt-dlp)
// needs it; without it the link is one entry and one download, as spotDL
// takes it.
func WithCollections(c Collections) Option { return func(s *Service) { s.collections = c } }
func WithDeviceID(fn func(context.Context) (string, error)) Option {
	return func(s *Service) { s.deviceID = fn }
}

// New builds a Service. store and syncStore may be nil (catalog/sync disabled);
// downloader may be nil (download unavailable — Add returns 503).
func New(store LinkStore, syncStore SyncStore, dl Downloader, opts ...Option) *Service {
	s := &Service{
		store:      store,
		syncStore:  syncStore,
		downloader: dl,
		now:        time.Now,
	}
	if dl != nil {
		if cl, ok := dl.(ChapterLister); ok {
			s.chapterLister = cl
		}
	}
	for _, o := range opts {
		o(s)
	}
	if s.now == nil {
		s.now = time.Now
	}
	return s
}

// SetDownloader updates the downloader to the live instance. Called by the API
// layer before each Add so the planner follows hot-reloads (the Manager is swapped
// on adapter reconfiguration without recreating the planner).
func (s *Service) SetDownloader(dl Downloader) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.downloader = dl
	if dl != nil {
		if cl, ok := dl.(ChapterLister); ok {
			s.chapterLister = cl
		} else {
			s.chapterLister = nil
		}
	} else {
		s.chapterLister = nil
	}
}

func (s *Service) getDownloader() (Downloader, ChapterLister) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.downloader, s.chapterLister
}

// CatalogID returns the stable catalog ID for a resolved link. Shared with the
// API fallback so both paths mint identical IDs.
func CatalogID(source, kind, externalID string) string {
	if kind == "" {
		kind = "track"
	}
	var prefix string
	switch kind {
	case "playlist":
		prefix = "pl_link_"
	case "album":
		prefix = "alb_link_"
	default:
		prefix = "trk_link_"
	}
	return prefix + source + "_" + externalID
}

// Resolve parses rawURL and, when a TrackLookup is configured, enriches Spotify
// track metadata with the live source instead of the synthetic placeholder
// ("Spotify track <id>") that linkresolve currently fabricates.
func (s *Service) Resolve(ctx context.Context, rawURL string) (*linkresolve.ResolveResult, error) {
	res, _, err := s.resolve(ctx, rawURL)
	if err != nil {
		return nil, err
	}
	// Named after the collection itself, where the service lists collections.
	if res.Kind == "album" || res.Kind == "playlist" {
		_, _ = s.expand(ctx, res, res.Kind)
	}
	return res, nil
}

// resolve is Resolve without listing a collection, which Add does itself. It
// also reports whether a Spotify track was named by the source.
func (s *Service) resolve(ctx context.Context, rawURL string) (*linkresolve.ResolveResult, bool, error) {
	res, err := linkresolve.ResolveURL(ctx, rawURL)
	if err != nil {
		return nil, false, err
	}
	named := false
	if s.lookup != nil && res.Source == "spotify" && res.Kind == "track" {
		if tr, lerr := s.lookup.GetTrack(ctx, res.Source, res.ExternalID); lerr == nil {
			named = tr.Title != ""
			if tr.Title != "" {
				res.Title = tr.Title
			}
			if tr.Artist != "" {
				res.Artist = tr.Artist
			}
			if tr.Album != "" {
				res.Album = tr.Album
			}
			if tr.CoverURL != "" {
				res.CoverUrl = tr.CoverURL
			}
		}
	}
	return res, named, nil
}

// ErrNoDownloader is returned when a download is requested but no downloader is configured.
var ErrNoDownloader = errors.New("no downloader configured")

// ErrNotFound is returned when a playlist is requested but not found.
var ErrNotFound = errors.New("playlist not found")

// Sentinel errors for user-input vs internal failures. Handlers map these to
// HTTP statuses via errors.Is so message wording can change without breaking
// status mapping.
var (
	ErrRangeChapterConflict = errors.New("choose either a time range or chapter splitting, not both")
	ErrRangeNonYouTube      = errors.New("time ranges and chapter splitting only apply to YouTube links")
	ErrNoChapterSupport     = errors.New("the configured downloader cannot read chapters")
	ErrNoChapters           = errors.New("this video has no chapters to split on")
	ErrCatalogRead          = errors.New("could not read catalog")
	ErrCatalogCreate        = errors.New("could not create catalog entry")
	ErrPlaylistValidate     = errors.New("could not validate playlist")
	ErrChaptersRead         = errors.New("could not read chapters")
	ErrSourceLookup         = errors.New("the link could not be looked up at its source")
)

// Add handles one link end-to-end: resolve, catalog, playlist, download planning.
func (s *Service) Add(ctx context.Context, opts AddOptions) (*AddResult, error) {
	rawURL := strings.TrimSpace(opts.URL)
	if rawURL == "" {
		return nil, errors.New("url is required")
	}
	res, named, err := s.resolve(ctx, rawURL)
	if err != nil {
		return nil, err
	}

	kind := res.Kind
	if kind == "" {
		kind = "track"
	}
	// A device that expands links downloads by artist and title, so a Spotify
	// track the source could not name has nothing to download by.
	if s.collections != nil && res.Source == "spotify" && kind == "track" && !named {
		return nil, fmt.Errorf("%w: Spotify did not name track %s", ErrSourceLookup, res.ExternalID)
	}
	// An album or playlist link can stand for its tracks.
	tracks, err := s.expand(ctx, res, kind)
	if err != nil {
		return nil, err
	}
	// Validate the requested playlist and plan every download before writing
	// catalog or playlist state. Predictable request failures must not leave a
	// combined Add operation half-applied.
	var playlistID string
	if opts.PlaylistID != nil {
		playlistID = strings.TrimSpace(*opts.PlaylistID)
	}
	if playlistID != "" {
		if s.store != nil {
			if _, err := s.store.GetSyncedPlaylist(ctx, playlistID); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return nil, ErrNotFound
				}
				return nil, fmt.Errorf("%w: %v", ErrPlaylistValidate, err)
			}
		}
	}

	shouldDownload := true
	if opts.Download != nil {
		shouldDownload = *opts.Download
	}

	var dl Downloader
	var reqs []core.DownloadRequest
	if shouldDownload {
		dl, _ = s.getDownloader()
		if dl == nil {
			return nil, ErrNoDownloader
		}
		base := core.DownloadRequest{
			Source:     res.Source,
			ExternalID: res.ExternalID,
			Artist:     res.Artist,
			Title:      res.Title,
			Album:      res.Album,
			Quality:    core.ParseAudioQuality(opts.Quality, ""),
		}
		if res.Source == "youtube" {
			base.ManualURL = strings.TrimSpace(res.URL)
			base.PreferDownloader = "ytdlp"
		}
		if playlistID != "" {
			base.AddToPlaylistID = playlistID
		}
		if opts.InitiatedBy != "" {
			base.InitiatedBy = opts.InitiatedBy
		}
		if tracks != nil {
			for _, t := range tracks {
				req := base
				req.Source, req.ExternalID = t.Source, t.ExternalID
				req.Title, req.Artist, req.Album, req.ISRC = t.Title, t.Artist, t.Album, t.ISRC
				req.DurationMs = t.DurationMs
				reqs = append(reqs, req)
			}
		} else {
			var derr error
			if reqs, derr = s.planDownloadRequests(ctx, base, res, opts); derr != nil {
				return nil, derr
			}
		}
	}

	catalogID, err := s.ensureCatalog(ctx, kind, res.Source, res.ExternalID, res.Title, res.Artist, res.Album)
	if err != nil {
		return nil, err
	}
	trackIDs := []string{catalogID}
	if tracks != nil {
		trackIDs = trackIDs[:0]
		for _, t := range tracks {
			id, err := s.ensureCatalog(ctx, "track", t.Source, t.ExternalID, t.Title, t.Artist, t.Album)
			if err != nil {
				return nil, err
			}
			trackIDs = append(trackIDs, id)
		}
	}
	if playlistID != "" {
		// Ownership was checked by the HTTP layer; emit durable membership only
		// after every predictable download validation succeeded.
		for _, id := range trackIDs {
			s.emitPlaylistTrack(ctx, playlistID, id)
		}
	}

	var job *core.DownloadJob
	var jobs []core.DownloadJob
	var downloadError string
	for _, req := range reqs {
		j, err := dl.Enqueue(ctx, req)
		if err != nil {
			// Enqueue can still fail on persistence after validation. Preserve and
			// report any successful playlist mutation or earlier jobs instead of
			// claiming the whole operation failed without their identities.
			if playlistID == "" && len(jobs) == 0 {
				return nil, err
			}
			downloadError = err.Error()
			break
		}
		jobs = append(jobs, j)
	}
	if len(jobs) > 0 {
		job = &jobs[0]
	}

	result := &AddResult{
		URL:           rawURL,
		Resolve:       res,
		CatalogID:     catalogID,
		DownloadError: downloadError,
	}
	if playlistID != "" {
		result.PlaylistID = playlistID
	}
	if job != nil {
		result.Job = job
	}
	if len(jobs) > 1 {
		result.Jobs = jobs
	}
	return result, nil
}

// expand lists the tracks an album or playlist link holds, when the service
// expands them, naming the result after the collection itself. It returns nil
// for anything else.
func (s *Service) expand(ctx context.Context, res *linkresolve.ResolveResult, kind string) ([]core.ExternalResult, error) {
	if s.collections == nil {
		return nil, nil
	}
	var tracks []core.ExternalResult
	switch kind {
	case "album":
		album, err := s.collections.GetAlbum(ctx, res.Source, res.ExternalID)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrSourceLookup, err)
		}
		res.Title, res.Artist, res.CoverUrl = album.Name, album.Artist, album.CoverURL
		for _, t := range album.Tracks {
			if t.Album == "" {
				t.Album = album.Name
			}
			if t.Artist == "" {
				t.Artist = album.Artist
			}
			tracks = append(tracks, t)
		}
	case "playlist":
		pl, err := s.collections.GetPlaylist(ctx, res.Source, res.ExternalID)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrSourceLookup, err)
		}
		res.Title, res.CoverUrl = pl.Name, pl.CoverURL
		tracks = pl.Tracks
	default:
		return nil, nil
	}
	if tracks == nil {
		tracks = []core.ExternalResult{}
	}
	for i := range tracks {
		if tracks[i].Source == "" {
			tracks[i].Source = res.Source
		}
	}
	return tracks, nil
}

// ensureCatalog records a catalog entity for the entry, once, and publishes
// its title and artist when it is new. It returns the entity's id.
func (s *Service) ensureCatalog(ctx context.Context, kind, source, externalID, title, artist, album string) (string, error) {
	catalogID := CatalogID(source, kind, externalID)
	if s.store == nil {
		return catalogID, nil
	}
	_, gerr := s.store.GetCatalogEntity(ctx, catalogID)
	if gerr == nil {
		return catalogID, nil
	}
	if !errors.Is(gerr, sql.ErrNoRows) {
		return "", fmt.Errorf("%w: %v", ErrCatalogRead, gerr)
	}
	ierr := s.store.InsertCatalogEntity(ctx, db.InsertCatalogEntityParams{
		ID:         catalogID,
		Kind:       kind,
		Title:      title,
		Artist:     artist,
		Album:      album,
		Source:     source,
		ExternalID: externalID,
		CreatedAt:  s.now().Unix(),
	})
	if ierr != nil {
		// Lost a race with another add of the same entry.
		if _, check := s.store.GetCatalogEntity(ctx, catalogID); check == nil {
			return catalogID, nil
		}
		return "", fmt.Errorf("%w: %v", ErrCatalogCreate, ierr)
	}
	if s.syncStore != nil && s.deviceID != nil {
		if deviceID, derr := s.deviceID(ctx); derr == nil && deviceID != "" {
			for field, value := range map[string]string{"title": title, "artist": artist} {
				_, _ = s.syncStore.AppendChange(ctx, deviceID, reverbsync.SyncChange{
					EntityType: kind, EntityID: catalogID, Field: field, Value: value,
					UpdatedAt: s.now().UnixMilli(), DeviceID: deviceID,
				})
			}
		}
	}
	return catalogID, nil
}

// emitPlaylistTrack publishes catalogID's membership of playlistID.
func (s *Service) emitPlaylistTrack(ctx context.Context, playlistID, catalogID string) {
	if s.syncStore == nil || s.deviceID == nil || s.store == nil {
		return
	}
	deviceID, derr := s.deviceID(ctx)
	if derr != nil || deviceID == "" {
		return
	}
	for _, ch := range []reverbsync.SyncChange{
		{EntityType: "playlist", EntityID: playlistID, Field: "track:" + catalogID, Value: catalogID},
		{EntityType: "playlist", EntityID: playlistID, Field: "tracks", Value: catalogID},
	} {
		ch.UpdatedAt, ch.DeviceID = s.now().UnixMilli(), deviceID
		_, _ = s.syncStore.AppendChange(ctx, deviceID, ch)
	}
}

// AddBatch processes many links, never aborting the batch on a per-link failure.
// Each item yields a BatchItemResult with Error populated on failure. Work is
// bounded to 10 concurrent items so a 500-item batch does not hold the request
// open for sequential DB+enqueue latency nor overwhelm SQLite.
func (s *Service) AddBatch(ctx context.Context, optsList []AddOptions) []BatchItemResult {
	out := make([]BatchItemResult, len(optsList))
	g, ctx := errgroup.WithContext(ctx)
	g.SetLimit(10)
	for i, opts := range optsList {
		i, opts := i, opts
		g.Go(func() error {
			res, err := s.Add(ctx, opts)
			if err != nil {
				out[i] = BatchItemResult{
					URL:   opts.URL,
					Error: err.Error(),
				}
				return nil
			}
			out[i] = BatchItemResult{
				URL:           opts.URL,
				Resolve:       res.Resolve,
				CatalogID:     res.CatalogID,
				PlaylistID:    res.PlaylistID,
				Job:           res.Job,
				Jobs:          res.Jobs,
				DownloadError: res.DownloadError,
			}
			return nil
		})
	}
	_ = g.Wait()
	return out
}

// planDownloadRequests expands one link's request into the jobs it implies.
func (s *Service) planDownloadRequests(ctx context.Context, base core.DownloadRequest, res *linkresolve.ResolveResult, opts AddOptions) ([]core.DownloadRequest, error) {
	start, end := strings.TrimSpace(opts.StartTime), strings.TrimSpace(opts.EndTime)
	trimmed := start != "" || end != ""
	if opts.SplitChapters && trimmed {
		return nil, ErrRangeChapterConflict
	}
	if (opts.SplitChapters || trimmed) && res.Source != "youtube" {
		return nil, ErrRangeNonYouTube
	}
	if !opts.SplitChapters {
		base.SectionStart, base.SectionEnd = start, end
		return []core.DownloadRequest{base}, nil
	}
	_, cl := s.getDownloader()
	if cl == nil {
		return nil, ErrNoChapterSupport
	}
	chapters, err := cl.ListChapters(ctx, res.URL)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrChaptersRead, err)
	}
	if len(chapters) == 0 {
		return nil, ErrNoChapters
	}
	out := make([]core.DownloadRequest, 0, len(chapters))
	for _, ch := range chapters {
		req := base
		req.Title = ch.Title
		req.Album = res.Title
		req.SectionStart = strconv.FormatFloat(ch.StartSec, 'f', -1, 64)
		if ch.EndSec > ch.StartSec {
			req.SectionEnd = strconv.FormatFloat(ch.EndSec, 'f', -1, 64)
		}
		out = append(out, req)
	}
	return out, nil
}

// Plan is exported for tests that want to verify request building without
// DB/sync overhead. It is the same logic as planDownloadRequests.
func (s *Service) Plan(ctx context.Context, base core.DownloadRequest, res *linkresolve.ResolveResult, opts AddOptions) ([]core.DownloadRequest, error) {
	return s.planDownloadRequests(ctx, base, res, opts)
}
