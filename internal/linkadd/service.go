// Package linkadd owns the add-from-link flow: resolving a pasted URL,
// minting its catalog entity, adding it to a managed playlist, and planning the
// download requests (including chapter expansion). The HTTP handlers in
// internal/api are thin shims over this service.
//
// It writes nothing to the change log itself. A track is minted through the
// catalog, which publishes the entity, and joins a playlist through the
// playlist module, which publishes the membership, like every other edit.
package linkadd

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/uhhhm/reverb/internal/catalog"
	"github.com/uhhhm/reverb/internal/core"
	"github.com/uhhhm/reverb/internal/linkresolve"
	"github.com/uhhhm/reverb/internal/store/db"
	"golang.org/x/sync/errgroup"
)

// LinkStore is the persistence slice the planner needs.
type LinkStore interface {
	InsertCatalogEntity(ctx context.Context, arg db.InsertCatalogEntityParams) error
	GetCatalogEntity(ctx context.Context, id string) (db.CatalogEntity, error)
	GetSyncedPlaylist(ctx context.Context, id string) (db.SyncedPlaylist, error)
}

// CatalogMinter resolves or mints a track's or album's catalog id through its
// aliases, so a later download of the same source track finds the same id.
// *catalog.Service fits.
type CatalogMinter interface {
	CanonicalFor(ctx context.Context, id catalog.Identity) (string, error)
}

// Playlists adds entries to a managed playlist and publishes the change.
// *playlistsync.Service fits.
type Playlists interface {
	AddTracks(ctx context.Context, id string, entries []core.ExternalResult, download bool) (core.SyncedPlaylistDetail, int, error)
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
	store      LinkStore
	catalog    CatalogMinter
	playlists  func() Playlists
	downloader func() Downloader
	lookup     TrackLookup
	// collections expands album and playlist links into their tracks for
	// everything: catalog, playlist and downloads.
	collections Collections
	// lister lists a collection's tracks only to add them to a playlist; the
	// link is still downloaded as one.
	lister Collections
	now    func() time.Time
}

// Option configures the Service.
type Option func(*Service)

// WithDownloaderProvider reads the live manager once per Add or Plan. Configure
// it at construction; a nil result means downloads are currently unavailable.
func WithDownloaderProvider(get func() Downloader) Option {
	return func(s *Service) { s.downloader = get }
}

func WithTrackLookup(l TrackLookup) Option { return func(s *Service) { s.lookup = l } }

// WithCollections expands album and playlist links into their tracks: each
// track is catalogued, joins the playlist, and is downloaded on its own. A
// device whose downloader fetches one track at a time (a phone's yt-dlp)
// needs it; without it the link is one download, as spotDL takes it.
func WithCollections(c Collections) Option { return func(s *Service) { s.collections = c } }

// WithCollectionListing lists an album or playlist link's tracks when the link
// is added to a playlist, so the playlist holds those tracks, while the link
// is still downloaded as one. Without it or WithCollections, such a link
// cannot be added to a playlist.
func WithCollectionListing(c Collections) Option { return func(s *Service) { s.lister = c } }

// WithCatalog mints catalog ids through the catalog's aliases.
func WithCatalog(m CatalogMinter) Option { return func(s *Service) { s.catalog = m } }

// WithPlaylists reads the live playlist module once per Add. A nil result
// means playlists are currently unavailable.
func WithPlaylists(get func() Playlists) Option { return func(s *Service) { s.playlists = get } }

// New builds a Service. store may be nil (no catalog rows, no playlist
// validation); dl may be nil (download unavailable: Add returns
// ErrNoDownloader).
func New(store LinkStore, dl Downloader, opts ...Option) *Service {
	s := &Service{
		store:      store,
		downloader: func() Downloader { return dl },
		now:        time.Now,
	}
	for _, o := range opts {
		o(s)
	}
	if s.now == nil {
		s.now = time.Now
	}
	return s
}

func (s *Service) getDownloader() (Downloader, ChapterLister) {
	if s.downloader == nil {
		return nil, nil
	}
	dl := s.downloader()
	cl, _ := dl.(ChapterLister)
	return dl, cl
}

// playlistLinkID is the local catalog id of a playlist link. A catalog holds
// tracks and albums, so a playlist link's entity stays on this device.
func playlistLinkID(source, externalID string) string {
	return "pl_link_" + source + "_" + externalID
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

// ErrNotEditable is returned when the playlist mirrors a source and cannot
// take tracks.
var ErrNotEditable = errors.New("playlist mirrors its source and cannot be edited")

// ErrNoPlaylists is returned when a playlist is requested but the playlist
// module is unavailable.
var ErrNoPlaylists = errors.New("playlists are unavailable")

// ErrCollectionNotListed is returned when an album or playlist link is added
// to a playlist on a device that cannot list the link's tracks.
var ErrCollectionNotListed = errors.New("this device cannot list the tracks of an album or playlist link to add them to a playlist")

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

// planned is one link that has been validated and catalogued, with its
// playlist entries and downloads worked out but not yet applied.
type planned struct {
	url        string
	res        *linkresolve.ResolveResult
	catalogID  string
	playlistID string
	// members join the playlist, in order.
	members []core.ExternalResult
	dl      Downloader
	reqs    []core.DownloadRequest
}

// Add handles one link end-to-end: resolve, catalog, playlist, download planning.
func (s *Service) Add(ctx context.Context, opts AddOptions) (*AddResult, error) {
	p, err := s.plan(ctx, opts)
	if err != nil {
		return nil, err
	}
	if p.playlistID != "" {
		if err := s.join(ctx, p.playlistID, p.members); err != nil {
			return nil, err
		}
	}
	return s.enqueue(ctx, p)
}

// plan resolves the link, validates the requested playlist and downloads, and
// then catalogues the link's tracks. Predictable request failures are all
// found before anything is written, so a combined Add is never half-applied.
func (s *Service) plan(ctx context.Context, opts AddOptions) (*planned, error) {
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
	var playlistID string
	if opts.PlaylistID != nil {
		playlistID = strings.TrimSpace(*opts.PlaylistID)
	}
	if err := s.validatePlaylist(ctx, playlistID); err != nil {
		return nil, err
	}
	// A collection link that is downloaded whole still joins a playlist as
	// its tracks.
	listed := tracks
	if playlistID != "" && tracks == nil && kind != "track" {
		if listed, err = s.list(ctx, res, kind); err != nil {
			return nil, err
		}
	}

	shouldDownload := true
	if opts.Download != nil {
		shouldDownload = *opts.Download
	}

	p := &planned{url: rawURL, res: res, playlistID: playlistID}
	if shouldDownload {
		var cl ChapterLister
		p.dl, cl = s.getDownloader()
		if p.dl == nil {
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
		if opts.InitiatedBy != "" {
			base.InitiatedBy = opts.InitiatedBy
		}
		if tracks != nil {
			for _, t := range tracks {
				req := base
				req.Source, req.ExternalID = t.Source, t.ExternalID
				req.Title, req.Artist, req.Album, req.ISRC = t.Title, t.Artist, t.Album, t.ISRC
				req.DurationMs = t.DurationMs
				p.reqs = append(p.reqs, req)
			}
		} else {
			var derr error
			if p.reqs, derr = planDownloadRequests(ctx, base, res, opts, cl); derr != nil {
				return nil, derr
			}
		}
	}

	if p.catalogID, err = s.ensureCatalog(ctx, kind, core.ExternalResult{
		Source: res.Source, ExternalID: res.ExternalID, Title: res.Title, Artist: res.Artist, Album: res.Album,
	}); err != nil {
		return nil, err
	}
	switch {
	case tracks != nil:
		for _, t := range tracks {
			id, err := s.ensureCatalog(ctx, "track", t)
			if err != nil {
				return nil, err
			}
			t.CanonicalID = id
			p.members = append(p.members, member(t))
		}
	case listed != nil:
		for _, t := range listed {
			p.members = append(p.members, member(t))
		}
	default:
		p.members = []core.ExternalResult{member(core.ExternalResult{
			Source: res.Source, ExternalID: res.ExternalID, Title: res.Title, Artist: res.Artist,
			Album: res.Album, CoverURL: res.CoverUrl, CanonicalID: p.catalogID,
		})}
	}
	return p, nil
}

// member is a track as it joins a playlist.
func member(t core.ExternalResult) core.ExternalResult {
	t.Type = core.EntityTrack
	return t
}

// validatePlaylist checks that a requested playlist exists and can take
// tracks. Ownership is the HTTP layer's check.
func (s *Service) validatePlaylist(ctx context.Context, playlistID string) error {
	if playlistID == "" {
		return nil
	}
	if s.playlists == nil || s.playlists() == nil {
		return ErrNoPlaylists
	}
	if s.store == nil {
		return nil
	}
	row, err := s.store.GetSyncedPlaylist(ctx, playlistID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("%w: %v", ErrPlaylistValidate, err)
	}
	if row.Mode == "synced" {
		return ErrNotEditable
	}
	return nil
}

// join adds members to a managed playlist through the playlist module, which
// publishes the membership to paired devices. Downloads are this service's
// own, so the playlist module queues none.
func (s *Service) join(ctx context.Context, playlistID string, members []core.ExternalResult) error {
	var pl Playlists
	if s.playlists != nil {
		pl = s.playlists()
	}
	if pl == nil {
		return ErrNoPlaylists
	}
	if _, _, err := pl.AddTracks(ctx, playlistID, members, false); err != nil {
		return fmt.Errorf("adding to playlist %s: %w", playlistID, err)
	}
	return nil
}

// enqueue queues a planned link's downloads.
func (s *Service) enqueue(ctx context.Context, p *planned) (*AddResult, error) {
	result := &AddResult{URL: p.url, Resolve: p.res, CatalogID: p.catalogID, PlaylistID: p.playlistID}
	var jobs []core.DownloadJob
	for _, req := range p.reqs {
		j, err := p.dl.Enqueue(ctx, req)
		if err != nil {
			// Enqueue can still fail on persistence after validation. Preserve and
			// report any successful playlist mutation or earlier jobs instead of
			// claiming the whole operation failed without their identities.
			if p.playlistID == "" && len(jobs) == 0 {
				return nil, err
			}
			result.DownloadError = err.Error()
			break
		}
		jobs = append(jobs, j)
	}
	if len(jobs) > 0 {
		result.Job = &jobs[0]
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
	tracks, named, err := listCollection(ctx, s.collections, res, kind)
	if err != nil || tracks == nil {
		return tracks, err
	}
	res.Title, res.Artist, res.CoverUrl = named.Title, named.Artist, named.CoverUrl
	return tracks, nil
}

// list names the tracks an album or playlist link holds, only so they can
// join a playlist; the link is still downloaded as it was resolved.
func (s *Service) list(ctx context.Context, res *linkresolve.ResolveResult, kind string) ([]core.ExternalResult, error) {
	if s.lister == nil {
		return nil, ErrCollectionNotListed
	}
	tracks, _, err := listCollection(ctx, s.lister, res, kind)
	return tracks, err
}

// listCollection lists an album or playlist link's tracks through c, with the
// collection's own name. It returns nil tracks for any other kind.
func listCollection(ctx context.Context, c Collections, res *linkresolve.ResolveResult, kind string) ([]core.ExternalResult, linkresolve.ResolveResult, error) {
	named := *res
	var tracks []core.ExternalResult
	switch kind {
	case "album":
		album, err := c.GetAlbum(ctx, res.Source, res.ExternalID)
		if err != nil {
			return nil, named, fmt.Errorf("%w: %v", ErrSourceLookup, err)
		}
		named.Title, named.Artist, named.CoverUrl = album.Name, album.Artist, album.CoverURL
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
		pl, err := c.GetPlaylist(ctx, res.Source, res.ExternalID)
		if err != nil {
			return nil, named, fmt.Errorf("%w: %v", ErrSourceLookup, err)
		}
		named.Title, named.CoverUrl = pl.Name, pl.CoverURL
		tracks = pl.Tracks
	default:
		return nil, named, nil
	}
	if tracks == nil {
		tracks = []core.ExternalResult{}
	}
	for i := range tracks {
		if tracks[i].Source == "" {
			tracks[i].Source = res.Source
		}
	}
	return tracks, named, nil
}

// ensureCatalog returns the catalog id of a link's track or album, minting it
// through the catalog's aliases. A later download of the same source track
// resolves through the same alias to the same id, and the catalog publishes a
// new entity itself. A playlist link's entity stays local to this device.
func (s *Service) ensureCatalog(ctx context.Context, kind string, t core.ExternalResult) (string, error) {
	if kind == "playlist" {
		return s.ensurePlaylistLink(ctx, t)
	}
	if s.catalog == nil {
		return "", fmt.Errorf("%w: no catalog configured", ErrCatalogCreate)
	}
	id, err := s.catalog.CanonicalFor(ctx, catalog.Identity{
		Kind: kind, Title: t.Title, Artist: t.Artist, Album: t.Album, ISRC: t.ISRC,
		Source: t.Source, ExternalID: t.ExternalID, DurationMs: t.DurationMs,
	})
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrCatalogCreate, err)
	}
	return id, nil
}

// ensurePlaylistLink records a playlist link's local entity, once.
func (s *Service) ensurePlaylistLink(ctx context.Context, t core.ExternalResult) (string, error) {
	id := playlistLinkID(t.Source, t.ExternalID)
	if s.store == nil {
		return id, nil
	}
	_, gerr := s.store.GetCatalogEntity(ctx, id)
	if gerr == nil {
		return id, nil
	}
	if !errors.Is(gerr, sql.ErrNoRows) {
		return "", fmt.Errorf("%w: %v", ErrCatalogRead, gerr)
	}
	ierr := s.store.InsertCatalogEntity(ctx, db.InsertCatalogEntityParams{
		ID: id, Kind: "playlist", Title: t.Title, Artist: t.Artist, Album: t.Album,
		Source: t.Source, ExternalID: t.ExternalID, CreatedAt: s.now().Unix(),
	})
	if ierr != nil {
		// Lost a race with another add of the same link.
		if _, check := s.store.GetCatalogEntity(ctx, id); check == nil {
			return id, nil
		}
		return "", fmt.Errorf("%w: %v", ErrCatalogCreate, ierr)
	}
	return id, nil
}

// AddBatch processes many links, never aborting the batch on a per-link failure.
// Each item yields a BatchItemResult with Error populated on failure. Links are
// planned 10 at a time, so a 500-item batch neither holds the request open for
// sequential lookups nor overwhelms SQLite. Each playlist then takes its links
// in one edit, in the order the batch lists them, before the downloads are
// queued in that order too.
func (s *Service) AddBatch(ctx context.Context, optsList []AddOptions) []BatchItemResult {
	out := make([]BatchItemResult, len(optsList))
	plans := make([]*planned, len(optsList))
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(10)
	for i, opts := range optsList {
		g.Go(func() error {
			p, err := s.plan(gctx, opts)
			if err != nil {
				out[i] = BatchItemResult{URL: opts.URL, Error: err.Error()}
				return nil
			}
			plans[i] = p
			return nil
		})
	}
	_ = g.Wait()

	var order []string
	byPlaylist := map[string][]int{}
	for i, p := range plans {
		if p == nil || p.playlistID == "" {
			continue
		}
		if _, seen := byPlaylist[p.playlistID]; !seen {
			order = append(order, p.playlistID)
		}
		byPlaylist[p.playlistID] = append(byPlaylist[p.playlistID], i)
	}
	for _, id := range order {
		var members []core.ExternalResult
		for _, i := range byPlaylist[id] {
			members = append(members, plans[i].members...)
		}
		if err := s.join(ctx, id, members); err != nil {
			for _, i := range byPlaylist[id] {
				out[i] = BatchItemResult{URL: optsList[i].URL, Error: err.Error()}
				plans[i] = nil
			}
		}
	}

	// Queued in the batch's order, so the downloads run in it too.
	for i, p := range plans {
		if p == nil {
			continue
		}
		res, err := s.enqueue(ctx, p)
		if err != nil {
			out[i] = BatchItemResult{URL: optsList[i].URL, Error: err.Error()}
			continue
		}
		out[i] = BatchItemResult{
			URL:           optsList[i].URL,
			Resolve:       res.Resolve,
			CatalogID:     res.CatalogID,
			PlaylistID:    res.PlaylistID,
			Job:           res.Job,
			Jobs:          res.Jobs,
			DownloadError: res.DownloadError,
		}
	}
	return out
}

// planDownloadRequests expands one link's request into the jobs it implies.
func planDownloadRequests(ctx context.Context, base core.DownloadRequest, res *linkresolve.ResolveResult, opts AddOptions, cl ChapterLister) ([]core.DownloadRequest, error) {
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
	_, cl := s.getDownloader()
	return planDownloadRequests(ctx, base, res, opts, cl)
}
