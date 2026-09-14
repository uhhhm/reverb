package recommend_test

import (
	"context"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/uhhhm/reverb/internal/core"
	"github.com/uhhhm/reverb/internal/recommend"
	"github.com/uhhhm/reverb/internal/search"
)

type listenPlay struct {
	artist, title string
	at            time.Time
}

// fakeListening summarises a fixed play history and library.
type fakeListening struct {
	plays   []listenPlay
	library []core.ExternalResult
	// artistErr and trackErr fail TopArtists and TopTracks, as a database
	// error would; with trackErrSpan set, only TopTracks over that span fails.
	artistErr, trackErr error
	trackErrSpan        time.Duration
}

func playsOf(artist, title string, n int, at time.Time) []listenPlay {
	out := make([]listenPlay, n)
	for i := range out {
		out[i] = listenPlay{artist: artist, title: title, at: at}
	}
	return out
}

func countBy(keys []string, names map[string]recommend.PlayedCount, limit int) []recommend.PlayedCount {
	counts := map[string]int{}
	var order []string
	for _, k := range keys {
		if counts[k] == 0 {
			order = append(order, k)
		}
		counts[k]++
	}
	out := make([]recommend.PlayedCount, 0, len(order))
	for _, k := range order {
		c := names[k]
		c.Count = counts[k]
		out = append(out, c)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Count > out[j].Count })
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

func (l *fakeListening) between(since, until time.Time) []listenPlay {
	var out []listenPlay
	for _, p := range l.plays {
		if !p.at.Before(since) && p.at.Before(until) {
			out = append(out, p)
		}
	}
	return out
}

func (l *fakeListening) TopTracks(_ context.Context, since, until time.Time, limit int) ([]recommend.PlayedCount, error) {
	if l.trackErr != nil && (l.trackErrSpan == 0 || until.Sub(since) == l.trackErrSpan) {
		return nil, l.trackErr
	}
	var keys []string
	names := map[string]recommend.PlayedCount{}
	for _, p := range l.between(since, until) {
		k := strings.ToLower(p.artist + "\x1f" + p.title)
		keys = append(keys, k)
		names[k] = recommend.PlayedCount{Artist: p.artist, Title: p.title}
	}
	return countBy(keys, names, limit), nil
}

func (l *fakeListening) TopArtists(_ context.Context, since, until time.Time, limit int) ([]recommend.PlayedCount, error) {
	if l.artistErr != nil {
		return nil, l.artistErr
	}
	var keys []string
	names := map[string]recommend.PlayedCount{}
	for _, p := range l.between(since, until) {
		k := strings.ToLower(p.artist)
		keys = append(keys, k)
		names[k] = recommend.PlayedCount{Artist: p.artist}
	}
	return countBy(keys, names, limit), nil
}

func (l *fakeListening) LibraryArtists(_ context.Context, limit int) ([]recommend.PlayedCount, error) {
	var keys []string
	names := map[string]recommend.PlayedCount{}
	for _, t := range l.library {
		k := strings.ToLower(t.Artist)
		keys = append(keys, k)
		names[k] = recommend.PlayedCount{Artist: t.Artist}
	}
	return countBy(keys, names, limit), nil
}

func (l *fakeListening) LibraryTracks(_ context.Context, artist string, limit int) ([]core.ExternalResult, error) {
	var out []core.ExternalResult
	for _, t := range l.library {
		if strings.EqualFold(t.Artist, artist) && len(out) < limit {
			out = append(out, t)
		}
	}
	return out, nil
}

func libraryTrack(id, title, artist string) core.ExternalResult {
	return core.ExternalResult{Source: "library", ExternalID: id, CanonicalID: "trk_" + id, Title: title, Artist: artist, Type: core.EntityTrack}
}

// syncBackground runs background refreshes inline, so a test sees their result
// on the next read.
func syncBackground(run func()) { run() }

func homeService(c *clock, l *fakeListening, sim recommend.TrackSimilarity, lib libraryMatcher, extra []recommend.Option, sources ...search.SearchSource) *recommend.Service {
	opts := []recommend.Option{
		recommend.WithMatcher(func() recommend.Matcher { return lib }),
		recommend.WithClock(c.now),
		recommend.WithSleep(c.sleep),
		recommend.WithTimeout(time.Second),
		recommend.WithListening(l),
		recommend.WithBackground(syncBackground),
	}
	if sim != nil {
		opts = append(opts, recommend.WithTrackSource(sim))
	}
	opts = append(opts, extra...)
	return recommend.New(func() []search.SearchSource { return sources }, opts...)
}

var wednesday = time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)

// shelfFixture: Xtal by Aphex and Roygbiv by Boards are the recent heavy
// plays; each has a similar track online, and each artist has a deep cut.
func shelfFixture() (*fakeListening, seededSimilarity, *trackSource, recommend.Option) {
	week := wednesday.Add(-7 * 24 * time.Hour)
	l := &fakeListening{plays: append(playsOf("Aphex", "Xtal", 3, week), playsOf("Boards", "Roygbiv", 2, week)...)}
	sim := seededSimilarity{
		"Xtal":    {{Artist: "Squarepusher", Title: "Windowlicker"}, {Artist: "Owner", Title: "Owned Song"}},
		"Roygbiv": {{Artist: "Bonobo", Title: "Kiara"}},
	}
	deezer := &trackSource{plainSource: plainSource{name: "deezer"}, tracks: []core.ExternalResult{
		deezerTrack("1", "Windowlicker", "Squarepusher"),
		deezerTrack("2", "Owned Song", "Owner"),
		deezerTrack("3", "Kiara", "Bonobo"),
		deezerTrack("4", "Aphex Deep", "Aphex"),
		deezerTrack("5", "Boards Deep", "Boards"),
	}}
	related := recommend.WithArtistSource(artistSimilarity{name: "listenbrainz", related: []recommend.ArtistCandidate{
		{Source: "deezer", ExternalID: "sq", Name: "Squarepusher"},
		{Source: "deezer", ExternalID: "ap", Name: "Aphex"},
	}})
	return l, sim, deezer, related
}

func shelfKinds(sh recommend.Shelves) []recommend.ShelfKind {
	var out []recommend.ShelfKind
	for _, s := range sh.Shelves {
		out = append(out, s.Kind)
	}
	return out
}

func TestShelvesHaveSeededKindsWithReasons(t *testing.T) {
	l, sim, deezer, related := shelfFixture()
	svc := homeService(&clock{t: wednesday}, l, sim, libraryMatcher{"owned song": "lib-1"}, []recommend.Option{related}, deezer)

	got := svc.RefreshShelves(context.Background())

	want := []recommend.ShelfKind{recommend.ShelfBecauseYouPlayed, recommend.ShelfBecauseYouPlayed, recommend.ShelfArtistsYouMightLike, recommend.ShelfMoreFromArtists}
	if !equalKinds(shelfKinds(got), want) {
		t.Fatalf("kinds %v, want %v", shelfKinds(got), want)
	}
	first := got.Shelves[0]
	if first.Seed == nil || first.Seed.Title != "Xtal" {
		t.Fatalf("first shelf seed %+v, want the most played track", first.Seed)
	}
	// A discovery shelf leaves out the owned copy.
	if titles := titles(first.Tracks); len(titles) != 1 || titles[0] != "Windowlicker" {
		t.Fatalf("because-you-played tracks %v", titles)
	}
	if a := got.Shelves[2].Artists; len(a) != 1 || a[0].Name != "Squarepusher" || a[0].Reason == nil {
		t.Fatalf("artists %+v, want Squarepusher with a reason and not the loved Aphex", a)
	}
	more := got.Shelves[3].Tracks
	if len(more) != 2 || more[0].Reason == nil || more[0].Reason.Kind != core.ReasonMoreFrom {
		t.Fatalf("more-from tracks %+v", more)
	}
}

func equalKinds(a, b []recommend.ShelfKind) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestShelvesRenderFromCacheAndRefreshInBackground(t *testing.T) {
	l, sim, deezer, related := shelfFixture()
	c := &clock{t: wednesday}
	var pending []func()
	capture := recommend.WithBackground(func(run func()) { pending = append(pending, run) })
	svc := homeService(c, l, sim, libraryMatcher{}, []recommend.Option{related, capture}, deezer)
	ctx := context.Background()

	first := svc.Shelves(ctx)
	if len(first.Shelves) != 0 || !first.Refreshing || len(pending) != 1 {
		t.Fatalf("first read %+v with %d refreshes, want empty and refreshing", first, len(pending))
	}
	pending[0]()
	pending = nil
	cached := svc.Shelves(ctx)
	if len(cached.Shelves) == 0 || cached.Refreshing || len(pending) != 0 {
		t.Fatalf("cached read %+v, want shelves without a new refresh", cached)
	}

	c.advance(7 * time.Hour)
	stale := svc.Shelves(ctx)
	if len(stale.Shelves) != len(cached.Shelves) || !stale.Refreshing || len(pending) != 1 {
		t.Fatalf("stale read %+v, want the cached shelves while refreshing", stale)
	}
}

func TestShelvesApplyNotInterestedToCachedShelves(t *testing.T) {
	l, sim, deezer, related := shelfFixture()
	m := &marks{artists: map[string]bool{}}
	svc := homeService(&clock{t: wednesday}, l, sim, libraryMatcher{"owned song": "lib-1"}, []recommend.Option{related, withMarks(m)}, deezer)
	ctx := context.Background()
	svc.RefreshShelves(ctx)

	m.artists["squarepusher"] = true
	got := svc.Shelves(ctx)
	for _, sh := range got.Shelves {
		for _, tr := range sh.Tracks {
			if tr.Artist == "Squarepusher" {
				t.Fatalf("marked artist's track on %s", sh.Kind)
			}
		}
		for _, a := range sh.Artists {
			if a.Name == "Squarepusher" {
				t.Fatal("marked artist on artists shelf")
			}
		}
	}
	// Xtal's shelf held only Windowlicker, so it is gone rather than empty.
	if got.Shelves[0].Seed == nil || got.Shelves[0].Seed.Title != "Roygbiv" {
		t.Fatalf("kinds %v, want the emptied shelf dropped", shelfKinds(got))
	}
}

func TestShelvesKeepLastResultsWhenARefreshFindsNothing(t *testing.T) {
	l, sim, deezer, related := shelfFixture()
	c := &clock{t: wednesday}
	svc := homeService(c, l, sim, libraryMatcher{"owned song": "lib-1"}, []recommend.Option{related}, deezer)
	ctx := context.Background()
	first := svc.RefreshShelves(ctx)

	l.plays = nil
	c.advance(7 * time.Hour)
	got := svc.RefreshShelves(ctx)
	if len(got.Shelves) != len(first.Shelves) || got.Offline || got.UpdatedAt != first.UpdatedAt {
		t.Fatalf("got %d shelves (offline %v, updated %d), want the last %d online shelves kept for a retry", len(got.Shelves), got.Offline, got.UpdatedAt, len(first.Shelves))
	}
}

func TestShelvesSeedFromLibraryOnANewInstall(t *testing.T) {
	_, sim, deezer, related := shelfFixture()
	l := &fakeListening{library: []core.ExternalResult{
		libraryTrack("1", "Xtal", "Aphex"),
		libraryTrack("2", "Ageispolis", "Aphex"),
		libraryTrack("3", "Roygbiv", "Boards"),
	}}
	svc := homeService(&clock{t: wednesday}, l, sim, libraryMatcher{}, []recommend.Option{related}, deezer)

	got := svc.RefreshShelves(context.Background())
	if len(got.Shelves) < 3 {
		t.Fatalf("kinds %v, want shelves seeded from the library", shelfKinds(got))
	}
	if got.Shelves[0].Kind != recommend.ShelfSimilarTo || got.Shelves[0].Seed.Title != "Xtal" {
		t.Fatalf("first shelf %+v, want Similar to the library's first track", got.Shelves[0])
	}
}
