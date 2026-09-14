package recommend_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/uhhhm/reverb/internal/core"
	"github.com/uhhhm/reverb/internal/recommend"
)

// discoverFixture: two seeds played before Monday 7 September. Xtal's similar
// tracks include three by one artist, an owned track and one already played.
func discoverFixture() (*fakeListening, *tasteInputs, seededSimilarity, *trackSource) {
	before := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	l := &fakeListening{plays: append(append(
		playsOf("Aphex", "Xtal", 3, before),
		playsOf("Boards", "Roygbiv", 2, before)...),
		listenPlay{artist: "Heard", title: "Already", at: before})}
	taste := &tasteInputs{plays: []recommend.TastePlay{
		{Seq: 1, Artist: "Aphex", Title: "Xtal", PlayedAt: before.Unix(), Completed: true},
		{Seq: 2, Artist: "Heard", Title: "Already", PlayedAt: before.Unix(), Completed: true},
	}}
	sim := seededSimilarity{
		"Xtal": {
			{Artist: "Xenon", Title: "Xone"}, {Artist: "Xenon", Title: "Xtwo"}, {Artist: "Xenon", Title: "Xthree"},
			{Artist: "Owner", Title: "Owned Song"}, {Artist: "Heard", Title: "Already"},
		},
		"Roygbiv": {{Artist: "Yarn", Title: "Yone"}},
	}
	deezer := &trackSource{plainSource: plainSource{name: "deezer"}, tracks: []core.ExternalResult{
		deezerTrack("1", "Xone", "Xenon"), deezerTrack("2", "Xtwo", "Xenon"), deezerTrack("3", "Xthree", "Xenon"),
		deezerTrack("4", "Owned Song", "Owner"), deezerTrack("5", "Already", "Heard"), deezerTrack("6", "Yone", "Yarn"),
	}}
	return l, taste, sim, deezer
}

func discoverService(c *clock) *recommend.Service {
	l, taste, sim, deezer := discoverFixture()
	return homeService(c, l, sim, libraryMatcher{"owned song": "lib-1"}, []recommend.Option{recommend.WithTaste(taste)}, deezer)
}

func TestDiscoverWeeklyIsAllNewWithAnArtistCap(t *testing.T) {
	got := discoverService(&clock{t: wednesday}).RefreshMix(context.Background(), recommend.MixDiscoverWeekly)

	if !got.Available || got.Period != "2026-09-07" {
		t.Fatalf("mix %+v, want an available mix for Monday 7 September", got)
	}
	perArtist := map[string]int{}
	for _, tr := range got.Tracks {
		perArtist[tr.Artist]++
		if tr.Source == "library" || tr.Title == "Already" {
			t.Fatalf("%q is not new to the library", tr.Title)
		}
	}
	if perArtist["Xenon"] != 2 || perArtist["Yarn"] != 1 || len(got.Tracks) != 3 {
		t.Fatalf("tracks %v, want two by Xenon and Yone", titles(got.Tracks))
	}
}

func TestDiscoverWeeklyIsDeterministicForAPeriod(t *testing.T) {
	ctx := context.Background()
	a := discoverService(&clock{t: wednesday}).RefreshMix(ctx, recommend.MixDiscoverWeekly)
	// Another device generating the same week later, after more plays.
	c := &clock{t: wednesday.Add(3 * 24 * time.Hour)}
	l, taste, sim, deezer := discoverFixture()
	// Plays after Monday, of a seed-to-be and of a candidate, change nothing.
	l.plays = append(l.plays, playsOf("Late", "After Monday", 9, wednesday)...)
	l.plays = append(l.plays, listenPlay{artist: "Xenon", title: "Xone", at: wednesday})
	taste.plays = append(taste.plays, recommend.TastePlay{Seq: 3, Artist: "Xenon", Title: "Xone", PlayedAt: wednesday.Unix(), Completed: true})
	b := homeService(c, l, sim, libraryMatcher{"owned song": "lib-1"}, []recommend.Option{recommend.WithTaste(taste)}, deezer).
		RefreshMix(ctx, recommend.MixDiscoverWeekly)

	if !reflect.DeepEqual(titles(a.Tracks), titles(b.Tracks)) || a.Period != b.Period {
		t.Fatalf("devices disagree: %v (%s) vs %v (%s)", titles(a.Tracks), a.Period, titles(b.Tracks), b.Period)
	}
}

func TestMixRegeneratesOnlyWhenItsPeriodHasPassed(t *testing.T) {
	c := &clock{t: wednesday}
	svc := discoverService(c)
	ctx := context.Background()

	if first := svc.Mix(ctx, recommend.MixDiscoverWeekly); len(first.Tracks) != 0 {
		t.Fatalf("first read %+v, want nothing before the first generation", first)
	}
	generated := svc.Mix(ctx, recommend.MixDiscoverWeekly)
	if generated.Period != "2026-09-07" || len(generated.Tracks) == 0 {
		t.Fatalf("generated %+v", generated)
	}

	c.advance(2 * 24 * time.Hour)
	if same := svc.Mix(ctx, recommend.MixDiscoverWeekly); same.UpdatedAt != generated.UpdatedAt {
		t.Fatal("regenerated within the same week")
	}

	// Asleep through Monday: the next read regenerates for the new week.
	c.advance(8 * 24 * time.Hour)
	svc.Mix(ctx, recommend.MixDiscoverWeekly)
	if next := svc.Mix(ctx, recommend.MixDiscoverWeekly); next.Period != "2026-09-14" {
		t.Fatalf("period %q after sleeping through Monday, want 2026-09-14", next.Period)
	}
}

func TestDiscoverWeeklyRetriesAfterAFailedRead(t *testing.T) {
	l, taste, sim, deezer := discoverFixture()
	l.trackErr = errors.New("database is locked")
	svc := homeService(&clock{t: wednesday}, l, sim, libraryMatcher{}, []recommend.Option{recommend.WithTaste(taste)}, deezer)

	got := svc.RefreshMix(context.Background(), recommend.MixDiscoverWeekly)
	// No period is recorded, so the next check generates it again.
	if got.Available || got.Period != "" {
		t.Fatalf("mix %+v, want unavailable with no period after a failed read", got)
	}
}

func TestDiscoverWeeklyRetriesWhenRecentPlaysCannotBeRead(t *testing.T) {
	l, taste, sim, deezer := discoverFixture()
	// Seeds read the last 90 days and succeed; the 30-day exclusion read fails.
	l.trackErr, l.trackErrSpan = errors.New("database is locked"), 30*24*time.Hour
	svc := homeService(&clock{t: wednesday}, l, sim, libraryMatcher{}, []recommend.Option{recommend.WithTaste(taste)}, deezer)

	got := svc.RefreshMix(context.Background(), recommend.MixDiscoverWeekly)
	if got.Available || got.Period != "" {
		t.Fatalf("mix %+v, want unavailable with no period when recent plays cannot be read", got)
	}
}

// failingTaste fails every play read, as a database error would.
type failingTaste struct{ tasteInputs }

func (failingTaste) PlaysAfter(context.Context, int64, int) ([]recommend.TastePlay, error) {
	return nil, errors.New("database is locked")
}

func TestDiscoverWeeklyRetriesWhenItsTasteCannotBeRead(t *testing.T) {
	l, _, sim, deezer := discoverFixture()
	svc := homeService(&clock{t: wednesday}, l, sim, libraryMatcher{}, []recommend.Option{recommend.WithTaste(&failingTaste{})}, deezer)

	got := svc.RefreshMix(context.Background(), recommend.MixDiscoverWeekly)
	if got.Available || got.Period != "" {
		t.Fatalf("mix %+v, want unavailable with no period when the taste profile cannot be read", got)
	}
}

func TestDiscoverWeeklyUnavailableOfflineKeepsNothing(t *testing.T) {
	l, taste, sim, deezer := discoverFixture()
	offline := recommend.WithSettings(func(context.Context) (recommend.Settings, error) {
		return recommend.Settings{Adventurousness: 50}, nil
	})
	svc := homeService(&clock{t: wednesday}, l, sim, libraryMatcher{}, []recommend.Option{recommend.WithTaste(taste), offline}, deezer)

	got := svc.RefreshMix(context.Background(), recommend.MixDiscoverWeekly)
	if got.Available || len(got.Tracks) != 0 || !got.Offline {
		t.Fatalf("offline mix %+v, want unavailable and empty", got)
	}
}

// fakePersonal stands in for ListenBrainz's recommendations for the account.
type fakePersonal struct {
	cands []recommend.TrackCandidate
	err   error
}

func (fakePersonal) Name() string                     { return "listenbrainz" }
func (f fakePersonal) Connected(context.Context) bool { return f.err == nil }
func (f fakePersonal) Recommendations(context.Context, int) ([]recommend.TrackCandidate, error) {
	return f.cands, f.err
}

func TestDiscoverWeeklyDrawsOnAConnectedPersonalSource(t *testing.T) {
	l, taste, sim, deezer := discoverFixture()
	deezer.tracks = append(deezer.tracks, deezerTrack("7", "Zed", "Zulu"))
	personal := recommend.WithPersonalSource(fakePersonal{cands: []recommend.TrackCandidate{
		{Artist: "Zulu", Title: "Zed"}, {Artist: "Heard", Title: "Already"},
	}})
	svc := homeService(&clock{t: wednesday}, l, sim, libraryMatcher{"owned song": "lib-1"}, []recommend.Option{recommend.WithTaste(taste), personal}, deezer)

	got := svc.RefreshMix(context.Background(), recommend.MixDiscoverWeekly)
	var zed *core.ExternalResult
	for i, tr := range got.Tracks {
		if tr.Title == "Already" {
			t.Fatal("a played track came back from the personal source")
		}
		if tr.Title == "Zed" {
			zed = &got.Tracks[i]
		}
	}
	if zed == nil {
		t.Fatalf("tracks %v, want the personal recommendation Zed", titles(got.Tracks))
	}
	if zed.Reason == nil || zed.Reason.Kind != core.ReasonPersonal || len(zed.RecommendationSources) != 1 || zed.RecommendationSources[0] != "listenbrainz" {
		t.Fatalf("Zed reason %+v, sources %v", zed.Reason, zed.RecommendationSources)
	}
}

func TestDiscoverWeeklyWithoutAPersonalAccountIsUnchanged(t *testing.T) {
	ctx := context.Background()
	want := discoverService(&clock{t: wednesday}).RefreshMix(ctx, recommend.MixDiscoverWeekly)
	l, taste, sim, deezer := discoverFixture()
	personal := recommend.WithPersonalSource(fakePersonal{err: recommend.ErrNotConfigured})
	got := homeService(&clock{t: wednesday}, l, sim, libraryMatcher{"owned song": "lib-1"}, []recommend.Option{recommend.WithTaste(taste), personal}, deezer).
		RefreshMix(ctx, recommend.MixDiscoverWeekly)

	if !reflect.DeepEqual(titles(got.Tracks), titles(want.Tracks)) || got.Available != want.Available {
		t.Fatalf("with a disconnected personal source %v, want %v", titles(got.Tracks), titles(want.Tracks))
	}
}

// switchablePersonal is a personal source whose account can be connected
// after the Mix was generated.
type switchablePersonal struct {
	connected bool
	cands     []recommend.TrackCandidate
}

func (*switchablePersonal) Name() string                     { return "listenbrainz" }
func (p *switchablePersonal) Connected(context.Context) bool { return p.connected }
func (p *switchablePersonal) Recommendations(context.Context, int) ([]recommend.TrackCandidate, error) {
	if !p.connected {
		return nil, recommend.ErrNotConfigured
	}
	return p.cands, nil
}

func TestConnectingAPersonalAccountRegeneratesDiscoverWeekly(t *testing.T) {
	ctx := context.Background()
	l, taste, sim, deezer := discoverFixture()
	deezer.tracks = append(deezer.tracks, deezerTrack("7", "Zed", "Zulu"))
	src := &switchablePersonal{cands: []recommend.TrackCandidate{{Artist: "Zulu", Title: "Zed"}}}
	svc := homeService(&clock{t: wednesday}, l, sim, libraryMatcher{"owned song": "lib-1"},
		[]recommend.Option{recommend.WithTaste(taste), recommend.WithPersonalSource(src)}, deezer)
	svc.Mix(ctx, recommend.MixDiscoverWeekly) // generates this week's Mix
	if before := titles(svc.Mix(ctx, recommend.MixDiscoverWeekly).Tracks); reflect.DeepEqual(before, []string{}) || contains(before, "Zed") {
		t.Fatalf("before connecting %v", before)
	}

	src.connected = true
	svc.PersonalSourceChanged(ctx)
	if after := titles(svc.Mix(ctx, recommend.MixDiscoverWeekly).Tracks); !contains(after, "Zed") {
		t.Fatalf("after connecting %v, want Zed", after)
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func TestDisconnectingHidesPersonalTracksFromAMixKeptOffline(t *testing.T) {
	ctx := context.Background()
	l, taste, sim, deezer := discoverFixture()
	deezer.tracks = append(deezer.tracks, deezerTrack("7", "Zed", "Zulu"))
	src := &switchablePersonal{connected: true, cands: []recommend.TrackCandidate{{Artist: "Zulu", Title: "Zed"}}}
	svc := homeService(&clock{t: wednesday}, l, sim, libraryMatcher{"owned song": "lib-1"},
		[]recommend.Option{recommend.WithTaste(taste), recommend.WithPersonalSource(src)}, deezer)
	svc.Mix(ctx, recommend.MixDiscoverWeekly)
	if got := titles(svc.Mix(ctx, recommend.MixDiscoverWeekly).Tracks); !contains(got, "Zed") {
		t.Fatalf("connected mix %v, want Zed", got)
	}

	// The link breaks; no regeneration has happened yet.
	src.connected = false
	if got := titles(svc.Mix(ctx, recommend.MixDiscoverWeekly).Tracks); contains(got, "Zed") || len(got) == 0 {
		t.Fatalf("after disconnecting %v, want the rest without Zed", got)
	}
}
