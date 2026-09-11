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
	cands []recommend.TrackCandidate
	err   error
	calls atomic.Int32
}

func (s *similarTracks) Name() string { return "lastfm" }
func (s *similarTracks) SimilarTracks(context.Context, string, string, int) ([]recommend.TrackCandidate, error) {
	s.calls.Add(1)
	return s.cands, s.err
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
	if c == nil {
		c = &clock{t: time.Unix(1_700_000_000, 0)}
	}
	return recommend.New(
		func() []search.SearchSource { return sources },
		recommend.WithTrackSource(ts),
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
	)
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
