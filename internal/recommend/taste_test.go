package recommend_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/uhhhm/reverb/internal/core"
	"github.com/uhhhm/reverb/internal/recommend"
	"github.com/uhhhm/reverb/internal/search"
)

// tasteInputs is the household's history, recording which play cursors the
// profile asked for.
type tasteInputs struct {
	plays   []recommend.TastePlay
	signals []recommend.TasteSignal
	afters  []int64
}

func (in *tasteInputs) PlaysAfter(_ context.Context, after int64, limit int) ([]recommend.TastePlay, error) {
	in.afters = append(in.afters, after)
	var out []recommend.TastePlay
	for _, p := range in.plays {
		if p.Seq > after {
			out = append(out, p)
			if len(out) == limit {
				break
			}
		}
	}
	return out, nil
}
func (in *tasteInputs) PlayCount(context.Context) (int64, error) { return int64(len(in.plays)), nil }
func (in *tasteInputs) PlayIDAt(_ context.Context, seq int64) (string, error) {
	for _, p := range in.plays {
		if p.Seq == seq {
			return p.ID, nil
		}
	}
	return "", nil
}
func (in *tasteInputs) Signals(context.Context) ([]recommend.TasteSignal, error) {
	return in.signals, nil
}

func play(seq int64, artist, title string, completed bool) recommend.TastePlay {
	return recommend.TastePlay{
		ID: fmt.Sprintf("%d:%s:%s", seq, artist, title), Seq: seq, Artist: artist, Title: title,
		PlayedAt: 1_700_000_000 + seq*60, Completed: completed,
	}
}

// catalogFor makes every candidate playable from a search source.
func catalogFor(cands []recommend.TrackCandidate) *trackSource {
	src := &trackSource{plainSource: plainSource{name: "deezer"}}
	for i, c := range cands {
		src.tracks = append(src.tracks, deezerTrack(fmt.Sprint("d", i), c.Title, c.Artist))
	}
	return src
}

func tasteService(cands []recommend.TrackCandidate, lib libraryMatcher, opts ...recommend.Option) *recommend.Service {
	options := append([]recommend.Option{
		recommend.WithTrackSource(seededSimilarity{"Seed": cands}),
		recommend.WithMatcher(func() recommend.Matcher { return lib }),
		recommend.WithTimeout(time.Second),
		recommend.WithSleep(func(context.Context, time.Duration) error { return nil }),
	}, opts...)
	catalog := catalogFor(cands)
	return recommend.New(func() []search.SearchSource { return []search.SearchSource{catalog} }, options...)
}

var seed = recommend.TrackSeed{Artist: "Seed Artist", Title: "Seed"}

func TestSimilarTracksRankAFamiliarArtistAboveSourceOrder(t *testing.T) {
	cands := []recommend.TrackCandidate{{Artist: "Stranger", Title: "Alpha"}, {Artist: "Loved", Title: "Bravo"}}
	in := &tasteInputs{plays: []recommend.TastePlay{
		play(1, "Loved", "Another Song", true), play(2, "Loved", "Another Song", true), play(3, "Loved", "Third Song", true),
	}}
	got := tasteService(cands, libraryMatcher{}, recommend.WithTaste(in)).SimilarTracksFor(context.Background(), seed)
	if want := []string{"Bravo", "Alpha"}; !reflect.DeepEqual(titles(got.Tracks), want) {
		t.Fatalf("got %v, want %v", titles(got.Tracks), want)
	}
}

func TestSimilarTracksRankATrackTheHouseholdKeepsLeavingBelowAFreshOne(t *testing.T) {
	cands := []recommend.TrackCandidate{{Artist: "Artist A", Title: "Alpha"}, {Artist: "Artist B", Title: "Bravo"}}
	in := &tasteInputs{plays: []recommend.TastePlay{play(1, "Artist A", "Alpha", false), play(2, "Artist A", "Alpha", false)}}
	got := tasteService(cands, libraryMatcher{}, recommend.WithTaste(in)).SimilarTracksFor(context.Background(), seed)
	if want := []string{"Bravo", "Alpha"}; !reflect.DeepEqual(titles(got.Tracks), want) {
		t.Fatalf("got %v, want %v", titles(got.Tracks), want)
	}
}

func TestPlaylistTracksAndMarksShapeTheProfile(t *testing.T) {
	cands := []recommend.TrackCandidate{
		{Artist: "Marked", Title: "Alpha"}, {Artist: "Neutral", Title: "Bravo"}, {Artist: "Collected", Title: "Charlie"},
	}
	in := &tasteInputs{signals: []recommend.TasteSignal{
		{Kind: recommend.SignalPlaylist, Artist: "Collected", Title: "Something Else", At: 1_700_000_000},
		{Kind: recommend.SignalNotInterested, Artist: "Marked", Title: "Another", At: 1_700_000_000},
	}}
	got := tasteService(cands, libraryMatcher{}, recommend.WithTaste(in)).SimilarTracksFor(context.Background(), seed)
	if want := []string{"Charlie", "Bravo", "Alpha"}; !reflect.DeepEqual(titles(got.Tracks), want) {
		t.Fatalf("got %v, want %v", titles(got.Tracks), want)
	}
}

func TestSimilarTracksExplainThemselves(t *testing.T) {
	cands := []recommend.TrackCandidate{{Artist: "Artist A", Title: "Alpha"}}
	unplayed := tasteService(cands, libraryMatcher{}, recommend.WithTaste(&tasteInputs{})).SimilarTracksFor(context.Background(), seed)
	want := core.RecommendationReason{Kind: core.ReasonSimilar, Artist: "Seed Artist", Title: "Seed"}
	if r := unplayed.Tracks[0].Reason; r == nil || *r != want {
		t.Fatalf("reason = %+v, want %+v", r, want)
	}
	played := tasteService(cands, libraryMatcher{}, recommend.WithTaste(&tasteInputs{plays: []recommend.TastePlay{play(1, "Seed Artist", "Seed", true)}})).
		SimilarTracksFor(context.Background(), seed)
	want.Kind = core.ReasonPlayed
	if r := played.Tracks[0].Reason; r == nil || *r != want {
		t.Fatalf("reason = %+v, want %+v", r, want)
	}
}

// Devices that hold the same plays stored them in different orders; the
// ranking must not depend on that (ADR 0001).
func TestRankingDoesNotDependOnPlayStorageOrder(t *testing.T) {
	var cands []recommend.TrackCandidate
	var plays []recommend.TastePlay
	words := []string{"Alpha", "Bravo", "Charlie", "Delta", "Echo", "Foxtrot", "Golf", "Hotel"}
	for i, w := range words {
		cands = append(cands, recommend.TrackCandidate{Artist: fmt.Sprint("Artist ", i%4), Title: w})
		for j := 0; j <= i; j++ {
			plays = append(plays, recommend.TastePlay{Artist: fmt.Sprint("Artist ", (i+j)%5), Title: fmt.Sprint("Song ", j), PlayedAt: int64(1_700_000_000 + i*7919 + j*104729), Completed: (i+j)%3 != 0})
		}
	}
	forward, backward := &tasteInputs{}, &tasteInputs{}
	for i := range plays {
		p, q := plays[i], plays[len(plays)-1-i]
		p.Seq, q.Seq = int64(i+1), int64(i+1)
		forward.plays = append(forward.plays, p)
		backward.plays = append(backward.plays, q)
	}
	a := tasteService(cands, libraryMatcher{}, recommend.WithTaste(forward)).SimilarTracksFor(context.Background(), seed)
	b := tasteService(cands, libraryMatcher{}, recommend.WithTaste(backward)).SimilarTracksFor(context.Background(), seed)
	if !reflect.DeepEqual(titles(a.Tracks), titles(b.Tracks)) {
		t.Fatalf("orders differ:\n%v\n%v", titles(a.Tracks), titles(b.Tracks))
	}
}

func TestProfileFoldsInOnlyNewPlaysAndRebuildsAfterARemoval(t *testing.T) {
	in := &tasteInputs{plays: []recommend.TastePlay{play(1, "A", "One", true), play(2, "A", "Two", true)}}
	svc := tasteService([]recommend.TrackCandidate{{Artist: "B", Title: "Alpha"}}, libraryMatcher{}, recommend.WithTaste(in))
	ctx := context.Background()

	svc.SimilarTracksFor(ctx, seed)
	in.plays = append(in.plays, play(3, "A", "Three", true))
	svc.SimilarTracksFor(ctx, seed)
	in.plays = in.plays[1:]
	svc.SimilarTracksFor(ctx, seed)

	if want := []int64{0, 2, 3, 0}; !reflect.DeepEqual(in.afters, want) {
		t.Fatalf("cursors = %v, want %v (everything, then only newer plays, then a rebuild)", in.afters, want)
	}
}

// SQLite reuses a rowid once the row holding it is gone, so deleting the newest
// play and recording another gives the new play the removed one's sequence
// number. Neither the fold's high-water mark nor the stored count moves, so the
// profile has to notice the substitution some other way.
func TestProfileNoticesAPlayThatReusedARemovedSequenceNumber(t *testing.T) {
	cands := []recommend.TrackCandidate{{Artist: "Stranger", Title: "Alpha"}, {Artist: "Loved", Title: "Bravo"}}
	in := &tasteInputs{plays: []recommend.TastePlay{play(1, "Stranger", "One", true)}}
	svc := tasteService(cands, libraryMatcher{}, recommend.WithTaste(in))
	ctx := context.Background()

	if got := titles(svc.SimilarTracksFor(ctx, seed).Tracks); !reflect.DeepEqual(got, []string{"Alpha", "Bravo"}) {
		t.Fatalf("before the substitution got %v, want [Alpha Bravo]", got)
	}

	// The only play is removed and another recorded, reusing its sequence number.
	in.plays = []recommend.TastePlay{play(1, "Loved", "Two", true)}

	if got := titles(svc.SimilarTracksFor(ctx, seed).Tracks); !reflect.DeepEqual(got, []string{"Bravo", "Alpha"}) {
		t.Fatalf("after the substitution got %v, want [Bravo Alpha] (the new play must shape the profile)", got)
	}
}

// The replacement need not be the newest play. Recording several plays after a
// removal puts one in the freed slot and the rest above it, so the count
// balances out and the fold's cursor moves past the substitution unless the
// cursor is checked before the newer plays are folded in.
func TestProfileNoticesAReusedSequenceNumberBelowNewerPlays(t *testing.T) {
	cands := []recommend.TrackCandidate{{Artist: "Stranger", Title: "Alpha"}, {Artist: "Loved", Title: "Bravo"}}
	in := &tasteInputs{plays: []recommend.TastePlay{play(1, "Stranger", "One", true)}}
	svc := tasteService(cands, libraryMatcher{}, recommend.WithTaste(in))
	ctx := context.Background()

	if got := titles(svc.SimilarTracksFor(ctx, seed).Tracks); !reflect.DeepEqual(got, []string{"Alpha", "Bravo"}) {
		t.Fatalf("before the substitution got %v, want [Alpha Bravo]", got)
	}

	// The only play is removed and three more recorded: the first takes the
	// freed sequence number, the others sit above it. More plays are stored
	// than were folded in, so the count alone gives nothing away.
	in.plays = []recommend.TastePlay{
		play(1, "Loved", "Two", true), play(2, "Other", "Three", true), play(3, "Other", "Four", true),
	}

	if got := titles(svc.SimilarTracksFor(ctx, seed).Tracks); !reflect.DeepEqual(got, []string{"Bravo", "Alpha"}) {
		t.Fatalf("after the substitution got %v, want [Bravo Alpha] (the play in the reused slot must count, the removed one must not)", got)
	}
}

func TestProfileOfTwentyThousandPlaysBuildsQuickly(t *testing.T) {
	if raceEnabled {
		t.Skip("timing does not apply under the race detector")
	}
	in := &tasteInputs{}
	for i := range 20_000 {
		in.plays = append(in.plays, play(int64(i+1), fmt.Sprint("Artist ", i%1500), fmt.Sprint("Song ", i%9000), i%4 != 0))
	}
	svc := tasteService([]recommend.TrackCandidate{{Artist: "B", Title: "Alpha"}}, libraryMatcher{}, recommend.WithTaste(in))
	start := time.Now()
	svc.SimilarTracksFor(context.Background(), seed)
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("building the profile took %v", d)
	}
}

func BenchmarkProfileTwentyThousandPlays(b *testing.B) {
	in := &tasteInputs{}
	for i := range 20_000 {
		in.plays = append(in.plays, play(int64(i+1), fmt.Sprint("Artist ", i%1500), fmt.Sprint("Song ", i%9000), i%4 != 0))
	}
	cands := []recommend.TrackCandidate{{Artist: "B", Title: "Alpha"}}
	for b.Loop() {
		tasteService(cands, libraryMatcher{}, recommend.WithTaste(in)).SimilarTracksFor(context.Background(), seed)
	}
}

func settingsOf(st recommend.Settings, err error) recommend.Option {
	return recommend.WithSettings(func(context.Context) (recommend.Settings, error) { return st, err })
}

// Three owned and three new candidates, in alternating source order.
var mixCands = []recommend.TrackCandidate{
	{Artist: "Artist A", Title: "Alpha"}, {Artist: "Artist D", Title: "Delta"},
	{Artist: "Artist B", Title: "Bravo"}, {Artist: "Artist E", Title: "Echo"},
	{Artist: "Artist C", Title: "Charlie"}, {Artist: "Artist F", Title: "Foxtrot"},
}
var owned = libraryMatcher{"delta": "lib-d", "echo": "lib-e", "foxtrot": "lib-f"}

func newCount(tracks []core.ExternalResult) int {
	n := 0
	for _, t := range tracks {
		if t.Source != "library" {
			n++
		}
	}
	return n
}

func TestAdventurousnessSetsRadiosShareOfNewMusic(t *testing.T) {
	for _, tc := range []struct {
		adventurousness int
		wantNew         int
		wantTotal       int
	}{
		{0, 0, 3},   // only known music
		{50, 3, 6},  // Radio's default: half new
		{100, 3, 3}, // only new music
	} {
		svc := tasteService(mixCands, owned, settingsOf(recommend.Settings{Adventurousness: tc.adventurousness, Online: true}, nil))
		got := svc.Radio(context.Background(), []recommend.Seed{{Artist: "Seed Artist", Title: "Seed"}})
		if n := newCount(got.Tracks); n != tc.wantNew || len(got.Tracks) != tc.wantTotal {
			t.Fatalf("adventurousness %d: %d new of %v, want %d new of %d", tc.adventurousness, n, titles(got.Tracks), tc.wantNew, tc.wantTotal)
		}
	}
}

func TestRadioAtDefaultAdventurousnessKeepsNewAndKnownWithinOneTrack(t *testing.T) {
	cands := append([]recommend.TrackCandidate{{Artist: "Artist G", Title: "Golf"}, {Artist: "Artist H", Title: "Hotel"}}, mixCands...)
	svc := tasteService(cands, owned, settingsOf(recommend.Settings{Adventurousness: 50, Online: true}, nil))
	got := svc.Radio(context.Background(), []recommend.Seed{{Artist: "Seed Artist", Title: "Seed"}})
	if len(got.Tracks) != 8 {
		t.Fatalf("got %v, want all 8", titles(got.Tracks))
	}
	// Five new and three known: after six tracks the known ones have run out
	// and the new ones fill the rest.
	for i := 1; i <= 6; i++ {
		n := newCount(got.Tracks[:i])
		if d := float64(n) - 0.5*float64(i); d <= -1 || d >= 1 {
			t.Fatalf("first %d tracks hold %d new: %v", i, n, titles(got.Tracks))
		}
	}
}

// countingArtists is a dedicated artist-similarity source that counts calls.
type countingArtists struct{ calls atomic.Int32 }

func (*countingArtists) Name() string { return "listenbrainz" }
func (s *countingArtists) SimilarArtists(context.Context, recommend.ArtistSeed, int) ([]recommend.ArtistCandidate, error) {
	s.calls.Add(1)
	return nil, nil
}

func TestOnlineRecommendationsOffSendsNoLookups(t *testing.T) {
	for _, tc := range []struct {
		name string
		opt  recommend.Option
	}{
		{"switched off", settingsOf(recommend.Settings{Adventurousness: 50, Online: false}, nil)},
		{"settings unreadable", settingsOf(recommend.Settings{}, errors.New("disk gone"))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tracks := &similarTracks{cands: []recommend.TrackCandidate{{Artist: "A", Title: "Alpha"}}}
			artists := &countingArtists{}
			deezer := &similarSource{plainSource: plainSource{name: "deezer"}, related: relatedN(3)}
			catalog := &trackSource{plainSource: plainSource{name: "spotify"}, tracks: []core.ExternalResult{deezerTrack("1", "Alpha", "A")}}
			svc := recommend.New(func() []search.SearchSource { return []search.SearchSource{deezer, catalog} },
				recommend.WithTrackSource(tracks), recommend.WithArtistSource(artists), tc.opt)
			ctx := context.Background()

			radio := svc.Radio(ctx, []recommend.Seed{{Artist: "Daft Punk"}, {Artist: "Air", Title: "Alpha"}})
			similar := svc.SimilarTracksFor(ctx, recommend.TrackSeed{Artist: "Air", Title: "Alpha"})
			related := svc.SimilarArtists(ctx, "deezer", "27")

			if n := tracks.calls.Load() + artists.calls.Load() + deezer.calls.Load() + catalog.searches.Load(); n != 0 {
				t.Fatalf("%d requests reached online sources", n)
			}
			if radio.Available || len(radio.Tracks) != 0 || similar.Available || len(similar.Tracks) != 0 || related.Available || len(related.Artists) != 0 {
				t.Fatalf("got results with online recommendations off: %+v %+v %+v", radio, similar, related)
			}
		})
	}
}

func TestSimilarArtistsSayWhoseFansLikeThem(t *testing.T) {
	src := artistSimilarity{name: "listenbrainz", related: []recommend.ArtistCandidate{{Source: "deezer", ExternalID: "1", Name: "Justice"}}}
	svc := newServiceWithArtistSources(library{"lib-1": {Name: "Daft Punk"}}, []recommend.ArtistSimilarity{src}, &plainSource{name: "deezer"})
	got := svc.SimilarArtists(context.Background(), "library", "lib-1")
	want := core.RecommendationReason{Kind: core.ReasonFansAlsoLike, Artist: "Daft Punk"}
	if len(got.Artists) != 1 || got.Artists[0].Reason == nil || *got.Artists[0].Reason != want {
		t.Fatalf("artists = %+v, want one with reason %+v", got.Artists, want)
	}
}

func TestRadioReasonNamesTheSeedThatRankedATrackHighest(t *testing.T) {
	sim := seededSimilarity{
		"Seed A": {{Artist: "Justice", Title: "Phantom"}, {Artist: "Justice", Title: "Genesis"}},
		"Seed B": {{Artist: "Justice", Title: "Genesis"}},
	}
	deezer := &trackSource{plainSource: plainSource{name: "deezer"}, tracks: []core.ExternalResult{
		deezerTrack("1", "Genesis", "Justice"), deezerTrack("2", "Phantom", "Justice"),
	}}
	svc := newTrackService(sim, libraryMatcher{}, nil, deezer)
	got := svc.Radio(context.Background(), []recommend.Seed{{Artist: "X", Title: "Seed A"}, {Artist: "Y", Title: "Seed B"}})
	for _, tr := range got.Tracks {
		wantSeed := map[string]string{"Genesis": "Seed B", "Phantom": "Seed A"}[tr.Title]
		if tr.Reason == nil || tr.Reason.Title != wantSeed {
			t.Fatalf("%s reason = %+v, want seed %s", tr.Title, tr.Reason, wantSeed)
		}
	}
	if titles(got.Tracks)[0] != "Genesis" {
		t.Fatalf("got %v, want Genesis first: two seeds propose it", titles(got.Tracks))
	}
}
