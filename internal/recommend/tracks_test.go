package recommend_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/uhhhm/reverb/internal/core"
	"github.com/uhhhm/reverb/internal/matching"
	"github.com/uhhhm/reverb/internal/recommend"
	"github.com/uhhhm/reverb/internal/search"
)

// similarTracks is a track similarity source with a fixed answer.
type similarTracks struct {
	name  string
	cands []recommend.TrackCandidate
	err   error
	calls atomic.Int32
}

type localSimilarity struct {
	tracks  []core.ExternalResult
	artists []core.ExternalArtist
	calls   atomic.Int32
}

func (l *localSimilarity) SimilarLocalTracks(context.Context, recommend.TrackSeed, int) ([]core.ExternalResult, error) {
	l.calls.Add(1)
	return l.tracks, nil
}
func (l *localSimilarity) SimilarLocalArtists(context.Context, recommend.ArtistSeed, int) ([]core.ExternalArtist, error) {
	l.calls.Add(1)
	return l.artists, nil
}

func (s *similarTracks) Name() string {
	if s.name != "" {
		return s.name
	}
	return "lastfm"
}
func (s *similarTracks) SimilarTracks(context.Context, recommend.TrackSeed, int) ([]recommend.TrackCandidate, error) {
	s.calls.Add(1)
	return s.cands, s.err
}

type blockingTracks struct{ name string }

func (s blockingTracks) Name() string { return s.name }
func (s blockingTracks) SimilarTracks(ctx context.Context, _ recommend.TrackSeed, _ int) ([]recommend.TrackCandidate, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

// trackSource answers a track search with every result whose title appears in
// the query, and counts its searches.
type trackSource struct {
	plainSource
	tracks   []core.ExternalResult
	searches atomic.Int32
}

func (s *trackSource) Search(_ context.Context, q string, t core.EntityType) ([]core.ExternalResult, error) {
	if t != core.EntityTrack {
		return nil, nil
	}
	s.searches.Add(1)
	var out []core.ExternalResult
	for _, r := range s.tracks {
		if strings.Contains(strings.ToLower(q), strings.ToLower(strings.Fields(r.Title)[0])) {
			out = append(out, r)
		}
	}
	return out, nil
}

// libraryMatcher owns the tracks listed by normalised title.
type libraryMatcher map[string]string

func (m libraryMatcher) Match(_ context.Context, ext core.ExternalResult) (core.MatchResult, error) {
	if id, ok := m[matching.Normalize(ext.Title)]; ok {
		return core.MatchResult{Status: core.MatchInLibrary, LibraryTrackID: id, Method: core.MatchFuzzy}, nil
	}
	return core.MatchResult{Status: core.MatchNotInLibrary}, nil
}

func deezerTrack(id, title, artist string) core.ExternalResult {
	return core.ExternalResult{Source: "deezer", ExternalID: id, Title: title, Artist: artist, Album: "Album", DurationMs: 200000, Type: core.EntityTrack}
}

// clock is a fake time source whose sleeps advance it.
type clock struct {
	mu    sync.Mutex
	t     time.Time
	slept []time.Duration
}

func (c *clock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}
func (c *clock) sleep(_ context.Context, d time.Duration) error {
	c.mu.Lock()
	c.slept = append(c.slept, d)
	c.mu.Unlock()
	c.advance(d)
	return nil
}

func newTrackService(ts recommend.TrackSimilarity, lib libraryMatcher, c *clock, sources ...search.SearchSource) *recommend.Service {
	return newTrackServiceWithSources([]recommend.TrackSimilarity{ts}, lib, c, sources...)
}

func newTrackServiceWithSources(trackSources []recommend.TrackSimilarity, lib libraryMatcher, c *clock, sources ...search.SearchSource) *recommend.Service {
	if c == nil {
		c = &clock{t: time.Unix(1_700_000_000, 0)}
	}
	options := []recommend.Option{
		recommend.WithMatcher(func() recommend.Matcher { return lib }),
		recommend.WithCatalogIDs(func(_ context.Context, ids []string) map[string]string {
			out := map[string]string{}
			for _, id := range ids {
				out[id] = "trk_" + id
			}
			return out
		}),
		recommend.WithClock(c.now),
		recommend.WithSleep(c.sleep),
		recommend.WithTimeout(time.Second),
	}
	for _, trackSource := range trackSources {
		options = append(options, recommend.WithTrackSource(trackSource))
	}
	return recommend.New(
		func() []search.SearchSource { return sources },
		options...,
	)
}

func TestSimilarTracksMergeSourcesAndRankAgreementFirst(t *testing.T) {
	lastfm := &similarTracks{name: "lastfm", cands: []recommend.TrackCandidate{
		{Artist: "Artist A", Title: "Only Last.fm", MBID: "mbid-lastfm"},
		{Artist: "Shared Artist", Title: "Shared Track", MBID: "mbid-shared"},
	}}
	listenbrainz := &similarTracks{name: "listenbrainz", cands: []recommend.TrackCandidate{
		{Artist: "Shared Artist", Title: "Shared Track", MBID: "mbid-shared"},
		{Artist: "Artist B", Title: "Only ListenBrainz", MBID: "mbid-listenbrainz"},
	}}
	catalog := &trackSource{plainSource: plainSource{name: "deezer"}, tracks: []core.ExternalResult{
		{Source: "deezer", ExternalID: "shared", Title: "Shared Track", Artist: "Shared Artist", MBID: "mbid-shared", Type: core.EntityTrack},
		{Source: "deezer", ExternalID: "lfm", Title: "Only Last.fm", Artist: "Artist A", MBID: "mbid-lastfm", Type: core.EntityTrack},
		{Source: "deezer", ExternalID: "lb", Title: "Only ListenBrainz", Artist: "Artist B", MBID: "mbid-listenbrainz", Type: core.EntityTrack},
	}}
	svc := newTrackServiceWithSources([]recommend.TrackSimilarity{lastfm, listenbrainz}, libraryMatcher{}, nil, catalog)

	got := svc.SimilarTracksFor(context.Background(), recommend.TrackSeed{Artist: "Seed Artist", Title: "Seed Track", MBID: "seed-mbid"})
	if len(got.Tracks) != 3 {
		t.Fatalf("got %d tracks, want 3: %+v", len(got.Tracks), got.Tracks)
	}
	if got.Tracks[0].ExternalID != "shared" {
		t.Fatalf("first track = %q, want the candidate supported by both sources", got.Tracks[0].ExternalID)
	}
	if diff := strings.Join(got.Tracks[0].RecommendationSources, ","); diff != "lastfm,listenbrainz" {
		t.Fatalf("shared sources = %q, want lastfm,listenbrainz", diff)
	}
}

func TestSimilarTracksUseNamesWhenOnlyOneCandidateHasAnMBID(t *testing.T) {
	lastfm := &similarTracks{name: "lastfm", cands: []recommend.TrackCandidate{{Artist: "Shared Artist", Title: "Shared Track"}}}
	listenbrainz := &similarTracks{name: "listenbrainz", cands: []recommend.TrackCandidate{{Artist: "Shared Artist", Title: "Shared Track", MBID: "mbid-shared"}}}
	catalog := &trackSource{plainSource: plainSource{name: "deezer"}, tracks: []core.ExternalResult{
		{Source: "deezer", ExternalID: "shared", Title: "Shared Track", Artist: "Shared Artist", MBID: "mbid-shared", Type: core.EntityTrack},
	}}
	svc := newTrackServiceWithSources([]recommend.TrackSimilarity{lastfm, listenbrainz}, libraryMatcher{}, nil, catalog)

	got := svc.SimilarTracks(context.Background(), "Seed Artist", "Seed Track")
	if len(got.Tracks) != 1 || len(got.Tracks[0].RecommendationSources) != 2 {
		t.Fatalf("got %+v, want one name-fallback candidate with two sources", got.Tracks)
	}
}

func TestSimilarTracksKeepHealthySourceWhenPeerFails(t *testing.T) {
	failing := &similarTracks{name: "lastfm", err: errors.New("outage")}
	healthy := &similarTracks{name: "listenbrainz", cands: []recommend.TrackCandidate{{Artist: "Artist A", Title: "Track A", MBID: "mbid-a"}}}
	search := &trackSource{plainSource: plainSource{name: "deezer"}, tracks: []core.ExternalResult{
		{Source: "deezer", ExternalID: "a", Title: "Track A", Artist: "Artist A", MBID: "mbid-a", Type: core.EntityTrack},
	}}
	svc := newTrackServiceWithSources([]recommend.TrackSimilarity{failing, healthy}, libraryMatcher{}, nil, search)

	got := svc.SimilarTracks(context.Background(), "Seed Artist", "Seed Track")
	if !got.Available || len(got.Tracks) != 1 || got.Tracks[0].ExternalID != "a" {
		t.Fatalf("got %+v, want the healthy source result", got)
	}
}

func TestSimilarTracksFallBackToLibraryWhenNetworkFails(t *testing.T) {
	failing := &similarTracks{name: "lastfm", err: errors.New("offline")}
	local := &localSimilarity{tracks: []core.ExternalResult{{Source: "library", ExternalID: "local-1", Title: "Local", Artist: "Band", Type: core.EntityTrack}}}
	svc := recommend.New(func() []search.SearchSource { return nil }, recommend.WithTrackSource(failing), recommend.WithLocalSimilarity(local))

	got := svc.SimilarTracks(context.Background(), "Seed", "Song")
	if !got.Available || !got.Offline || len(got.Tracks) != 1 || got.Tracks[0].Source != "library" {
		t.Fatalf("fallback = %+v", got)
	}
}

func TestSimilarTracksKeepLastGoodCacheWhenRefreshFails(t *testing.T) {
	clock := &clock{t: time.Unix(1_700_000_000, 0)}
	source := &similarTracks{cands: []recommend.TrackCandidate{{Artist: "Artist A", Title: "Track A", MBID: "mbid-a"}}}
	catalog := &trackSource{plainSource: plainSource{name: "deezer"}, tracks: []core.ExternalResult{
		{Source: "deezer", ExternalID: "a", Title: "Track A", Artist: "Artist A", MBID: "mbid-a", Type: core.EntityTrack},
	}}
	svc := newTrackService(source, libraryMatcher{}, clock, catalog)
	if got := svc.SimilarTracks(context.Background(), "Seed", "Song"); len(got.Tracks) != 1 || got.Offline {
		t.Fatalf("initial result = %+v", got)
	}

	clock.advance(25 * time.Hour)
	source.cands = nil
	source.err = errors.New("offline")
	got := svc.SimilarTracks(context.Background(), "Seed", "Song")
	if len(got.Tracks) != 1 || !got.Offline || got.UpdatedAt != 1_700_000_000 {
		t.Fatalf("stale fallback = %+v", got)
	}
}

func TestSimilarTracksUseOnlyLibraryWhenOnlineRecommendationsAreOff(t *testing.T) {
	online := &similarTracks{cands: []recommend.TrackCandidate{{Artist: "Online", Title: "Remote"}}}
	local := &localSimilarity{tracks: []core.ExternalResult{{Source: "library", ExternalID: "local-1", Title: "Local", Artist: "Band", Type: core.EntityTrack}}}
	svc := recommend.New(func() []search.SearchSource { return nil }, recommend.WithTrackSource(online), recommend.WithLocalSimilarity(local),
		recommend.WithSettings(func(context.Context) (recommend.Settings, error) { return recommend.Settings{Online: false}, nil }))

	got := svc.SimilarTracks(context.Background(), "Seed", "Song")
	if !got.Available || len(got.Tracks) != 1 || online.calls.Load() != 0 {
		t.Fatalf("offline result = %+v, online calls = %d", got, online.calls.Load())
	}
}

func TestSimilarTracksKeepHealthySourceWhenPeerConsumesSourceDeadline(t *testing.T) {
	healthy := &similarTracks{name: "listenbrainz", cands: []recommend.TrackCandidate{{Artist: "Artist A", Title: "Track A", MBID: "mbid-a"}}}
	catalog := &trackSource{plainSource: plainSource{name: "deezer"}, tracks: []core.ExternalResult{
		{Source: "deezer", ExternalID: "a", Title: "Track A", Artist: "Artist A", MBID: "mbid-a", Type: core.EntityTrack},
	}}
	svc := recommend.New(
		func() []search.SearchSource { return []search.SearchSource{catalog} },
		recommend.WithMatcher(func() recommend.Matcher { return libraryMatcher{} }),
		recommend.WithTrackSource(blockingTracks{name: "lastfm"}),
		recommend.WithTrackSource(healthy),
		recommend.WithTimeout(20*time.Millisecond),
	)

	got := svc.SimilarTracks(context.Background(), "Seed Artist", "Seed Track")
	if !got.Available || len(got.Tracks) != 1 || got.Tracks[0].ExternalID != "a" {
		t.Fatalf("got %+v, want the healthy source result after the peer timed out", got)
	}
}

func TestSimilarTracksResolveOwnedToLibraryByCatalogID(t *testing.T) {
	ts := &similarTracks{cands: []recommend.TrackCandidate{{Artist: "Daft Punk", Title: "Around the World"}}}
	svc := newTrackService(ts, libraryMatcher{"around the world": "lib-7"}, nil)

	got := svc.SimilarTracks(context.Background(), "Daft Punk", "One More Time")
	if !got.Available || len(got.Tracks) != 1 {
		t.Fatalf("got %+v", got)
	}
	tr := got.Tracks[0]
	if tr.Source != "library" || tr.ExternalID != "lib-7" || tr.CanonicalID != "trk_lib-7" {
		t.Fatalf("owned track = %+v, want library lib-7 under catalog id trk_lib-7", tr)
	}
	if tr.Match == nil || tr.Match.Status != core.MatchInLibrary || tr.Match.LibraryTrackID != "lib-7" {
		t.Fatalf("match = %+v", tr.Match)
	}
}

func TestSimilarTracksResolveOthersToSearchResults(t *testing.T) {
	ts := &similarTracks{cands: []recommend.TrackCandidate{
		{Artist: "Justice", Title: "D.A.N.C.E."},
		{Artist: "Nobody Known", Title: "Unfindable Song"},
		{Artist: "Stardust", Title: "Music Sounds Better with You"},
	}}
	deezer := &trackSource{plainSource: plainSource{name: "deezer"}, tracks: []core.ExternalResult{
		deezerTrack("1", "D.A.N.C.E.", "Justice"),
		deezerTrack("2", "Music Sounds Better With You", "Stardust"),
	}}
	svc := newTrackService(ts, libraryMatcher{}, nil, deezer)

	got := svc.SimilarTracks(context.Background(), "Daft Punk", "One More Time")
	if len(got.Tracks) != 2 {
		t.Fatalf("got %d tracks, want the 2 that matched (unmatched dropped): %+v", len(got.Tracks), got.Tracks)
	}
	if got.Tracks[0].ExternalID != "1" || got.Tracks[1].ExternalID != "2" {
		t.Fatalf("order/ids = %s, %s; want 1, 2", got.Tracks[0].ExternalID, got.Tracks[1].ExternalID)
	}
}

// A live cut or a same-titled song by someone else is not the recommended
// track, even when it is the only search hit.
func TestSimilarTracksDropWrongVersionAndWrongArtist(t *testing.T) {
	ts := &similarTracks{cands: []recommend.TrackCandidate{
		{Artist: "Daft Punk", Title: "Around the World"},
		{Artist: "Justice", Title: "Genesis"},
	}}
	deezer := &trackSource{plainSource: plainSource{name: "deezer"}, tracks: []core.ExternalResult{
		deezerTrack("live", "Around the World (Live)", "Daft Punk"),
		deezerTrack("atc", "Around the World", "ATC"),
		deezerTrack("genesis-live", "Genesis - Live", "Justice"),
		deezerTrack("genesis-other", "Genesis", "Phil Collins Tribute Band"),
	}}
	svc := newTrackService(ts, libraryMatcher{}, nil, deezer)

	if got := svc.SimilarTracks(context.Background(), "Daft Punk", "One More Time"); len(got.Tracks) != 0 {
		t.Fatalf("got %+v, want every wrong version and wrong artist dropped", got.Tracks)
	}
}

func TestSimilarTracksHiddenWithoutConfiguredSource(t *testing.T) {
	if got := newTrackService(nil, libraryMatcher{}, nil).SimilarTracks(context.Background(), "A", "B"); got.Available {
		t.Fatalf("no source: got %+v", got)
	}
	ts := &similarTracks{err: recommend.ErrNotConfigured}
	got := newTrackService(ts, libraryMatcher{}, nil).SimilarTracks(context.Background(), "A", "B")
	if got.Available || got.Tracks == nil {
		t.Fatalf("not configured: got %+v, want unavailable with an empty list", got)
	}
}

func TestSimilarTracksAreCached(t *testing.T) {
	ts := &similarTracks{cands: []recommend.TrackCandidate{{Artist: "Justice", Title: "D.A.N.C.E."}}}
	deezer := &trackSource{plainSource: plainSource{name: "deezer"}, tracks: []core.ExternalResult{deezerTrack("1", "D.A.N.C.E.", "Justice")}}
	svc := newTrackService(ts, libraryMatcher{}, nil, deezer)

	svc.SimilarTracks(context.Background(), "Daft Punk", "One More Time")
	svc.SimilarTracks(context.Background(), "daft punk", "one more time")
	if ts.calls.Load() != 1 || deezer.searches.Load() != 1 {
		t.Fatalf("lastfm %d calls, deezer %d searches; want 1 and 1", ts.calls.Load(), deezer.searches.Load())
	}
}

func TestSimilarTracksBackOffAfterSourceError(t *testing.T) {
	c := &clock{t: time.Unix(1_700_000_000, 0)}
	ts := &similarTracks{err: errors.New("lastfm: error 29: Rate Limit Exceeded")}
	svc := newTrackService(ts, libraryMatcher{}, c)

	if got := svc.SimilarTracks(context.Background(), "A", "B"); !got.Available || len(got.Tracks) != 0 {
		t.Fatalf("failure: got %+v, want available and empty", got)
	}
	svc.SimilarTracks(context.Background(), "C", "D")
	if n := ts.calls.Load(); n != 1 {
		t.Fatalf("source called %d times during backoff, want 1", n)
	}
	c.advance(10 * time.Minute)
	svc.SimilarTracks(context.Background(), "E", "F")
	if n := ts.calls.Load(); n != 2 {
		t.Fatalf("source called %d times after backoff, want 2", n)
	}
}

func TestSimilarTracksSpaceOutSourceCalls(t *testing.T) {
	c := &clock{t: time.Unix(1_700_000_000, 0)}
	ts := &similarTracks{cands: []recommend.TrackCandidate{}}
	svc := newTrackService(ts, libraryMatcher{}, c)

	svc.SimilarTracks(context.Background(), "A", "B")
	svc.SimilarTracks(context.Background(), "C", "D")
	if len(c.slept) != 1 || c.slept[0] <= 0 {
		t.Fatalf("slept %v between back-to-back calls, want one positive wait", c.slept)
	}
}
