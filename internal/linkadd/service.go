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
	URL        string                     `json:"url"`
	Resolve    *linkresolve.ResolveResult `json:"resolve"`
	CatalogID  string                     `json:"catalogId"`
	PlaylistID string                     `json:"playlistId,omitempty"`
	Job        *core.DownloadJob          `json:"job,omitempty"`
	Jobs       []core.DownloadJob         `json:"jobs,omitempty"`
}

// BatchResult is the per-link outcome for the batch endpoint, including errors.
type BatchItemResult struct {
	URL        string                     `json:"url"`
	Resolve    *linkresolve.ResolveResult `json:"resolve,omitempty"`
	CatalogID  string                     `json:"catalogId,omitempty"`
	PlaylistID string                     `json:"playlistId,omitempty"`
	Job        *core.DownloadJob          `json:"job,omitempty"`
	Jobs       []core.DownloadJob         `json:"jobs,omitempty"`
	Error      string                     `json:"error,omitempty"`
}

// Service owns the planning.
type Service struct {
	store         LinkStore
	syncStore     SyncStore
	downloader    Downloader
	chapterLister ChapterLister
	lookup        TrackLookup
	deviceID      func(context.Context) (string, error)
	now           func() time.Time
	mu            sync.RWMutex
}

// Option configures the Service.
type Option func(*Service)

func WithTrackLookup(l TrackLookup) Option { return func(s *Service) { s.lookup = l } }
func WithNow(fn func() time.Time) Option   { return func(s *Service) { s.now = fn } }
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
	res, err := linkresolve.ResolveURL(ctx, rawURL)
	if err != nil {
		return nil, err
	}
	if s.lookup != nil && res.Source == "spotify" && res.Kind == "track" {
		if tr, lerr := s.lookup.GetTrack(ctx, res.Source, res.ExternalID); lerr == nil {
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
	return res, nil
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
)

// Add handles one link end-to-end: resolve, catalog, playlist, download planning.
func (s *Service) Add(ctx context.Context, opts AddOptions) (*AddResult, error) {
	rawURL := strings.TrimSpace(opts.URL)
	if rawURL == "" {
		return nil, errors.New("url is required")
	}
	res, err := s.Resolve(ctx, rawURL)
	if err != nil {
		return nil, err
	}

	// Catalog entity handling.
	kind := res.Kind
	if kind == "" {
		kind = "track"
	}
	catalogID := CatalogID(res.Source, kind, res.ExternalID)
	createdNew := false
	if s.store != nil {
		_, gerr := s.store.GetCatalogEntity(ctx, catalogID)
		if errors.Is(gerr, sql.ErrNoRows) {
			now := s.now().Unix()
			ierr := s.store.InsertCatalogEntity(ctx, db.InsertCatalogEntityParams{
				ID:         catalogID,
				Kind:       kind,
				Title:      res.Title,
				Artist:     res.Artist,
				Album:      res.Album,
				DurationMs: 0,
				Isrc:       "",
				Mbid:       "",
				Source:     res.Source,
				ExternalID: res.ExternalID,
				CreatedAt:  now,
			})
			if ierr != nil {
				if strings.Contains(ierr.Error(), "UNIQUE") || strings.Contains(ierr.Error(), "constraint") || strings.Contains(strings.ToLower(ierr.Error()), "primary") {
					createdNew = false
				} else {
					if _, check := s.store.GetCatalogEntity(ctx, catalogID); check == nil {
						createdNew = false
					} else {
						return nil, fmt.Errorf("%w: %v", ErrCatalogCreate, ierr)
					}
				}
			} else {
				createdNew = true
			}
		} else if gerr == nil {
			createdNew = false
		} else {
			return nil, fmt.Errorf("%w: %v", ErrCatalogRead, gerr)
		}

		if createdNew && s.syncStore != nil && s.deviceID != nil {
			if deviceID, derr := s.deviceID(ctx); derr == nil && deviceID != "" {
				ch := reverbsync.SyncChange{
					EntityType: kind,
					EntityID:   catalogID,
					Field:      "title",
					Value:      res.Title,
					UpdatedAt:  s.now().UnixMilli(),
					DeviceID:   deviceID,
				}
				_, _ = s.syncStore.AppendChange(ctx, deviceID, ch)
				ch2 := reverbsync.SyncChange{
					EntityType: kind,
					EntityID:   catalogID,
					Field:      "artist",
					Value:      res.Artist,
					UpdatedAt:  s.now().UnixMilli(),
					DeviceID:   deviceID,
				}
				_, _ = s.syncStore.AppendChange(ctx, deviceID, ch2)
			}
		}
	}

	// Playlist handling.
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
		// Ownership check is performed by the HTTP layer (playlistAccessAllowed);
		// the service only validates existence. Emit sync for membership.
		if s.syncStore != nil && s.deviceID != nil && s.store != nil {
			if deviceID, derr := s.deviceID(ctx); derr == nil && deviceID != "" {
				ch := reverbsync.SyncChange{
					EntityType: "playlist",
					EntityID:   playlistID,
					Field:      "track:" + catalogID,
					Value:      catalogID,
					UpdatedAt:  s.now().UnixMilli(),
					DeviceID:   deviceID,
				}
				_, _ = s.syncStore.AppendChange(ctx, deviceID, ch)
				ch2 := reverbsync.SyncChange{
					EntityType: "playlist",
					EntityID:   playlistID,
					Field:      "tracks",
					Value:      catalogID,
					UpdatedAt:  s.now().UnixMilli(),
					DeviceID:   deviceID,
				}
				_, _ = s.syncStore.AppendChange(ctx, deviceID, ch2)
			}
		}
	}

	shouldDownload := true
	if opts.Download != nil {
		shouldDownload = *opts.Download
	}

	var job *core.DownloadJob
	var jobs []core.DownloadJob
	if shouldDownload {
		dl, _ := s.getDownloader()
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
		reqs, derr := s.planDownloadRequests(ctx, base, res, opts)
		if derr != nil {
			return nil, derr
		}
		for _, req := range reqs {
			j, err := dl.Enqueue(ctx, req)
			if err != nil {
				return nil, err
			}
			jobs = append(jobs, j)
		}
		if len(jobs) > 0 {
			job = &jobs[0]
		}
	}

	result := &AddResult{
		URL:       rawURL,
		Resolve:   res,
		CatalogID: catalogID,
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
				URL:        opts.URL,
				Resolve:    res.Resolve,
				CatalogID:  res.CatalogID,
				PlaylistID: res.PlaylistID,
				Job:        res.Job,
				Jobs:       res.Jobs,
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

// EnqueueForDownload is exported for tests that want to verify request building
// without DB/sync overhead. It is the same logic as planDownloadRequests.
func (s *Service) Plan(ctx context.Context, base core.DownloadRequest, res *linkresolve.ResolveResult, opts AddOptions) ([]core.DownloadRequest, error) {
	return s.planDownloadRequests(ctx, base, res, opts)
}
