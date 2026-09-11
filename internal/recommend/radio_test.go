package recommend_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/uhhhm/reverb/internal/core"
	"github.com/uhhhm/reverb/internal/recommend"
)

// seededSimilarity answers each seed title with its own candidates.
type seededSimilarity map[string][]recommend.TrackCandidate

func (seededSimilarity) Name() string { return "lastfm" }
func (s seededSimilarity) SimilarTracks(_ context.Context, _, title string, _ int) ([]recommend.TrackCandidate, error) {
	return s[title], nil
}

func titles(tracks []core.ExternalResult) []string {
	out := []string{}
	for _, t := range tracks {
		out = append(out, t.Title)
	}
	return out
}

func TestRadioInterleavesSeedsInSourceOrder(t *testing.T) {
	sim := seededSimilarity{
		"Seed A": {{Artist: "Justice", Title: "Genesis"}, {Artist: "Justice", Title: "Phantom"}},
		"Seed B": {{Artist: "Air", Title: "Sexy Boy"}, {Artist: "Air", Title: "Playground Love"}},
	}
	deezer := &trackSource{plainSource: plainSource{name: "deezer"}, tracks: []core.ExternalResult{
		deezerTrack("1", "Genesis", "Justice"),
		deezerTrack("2", "Phantom", "Justice"),
		deezerTrack("3", "Sexy Boy", "Air"),
		deezerTrack("4", "Playground Love", "Air"),
	}}
	svc := newTrackService(sim, libraryMatcher{}, nil, deezer)

	got := svc.Radio(context.Background(), []recommend.Seed{{Artist: "X", Title: "Seed A"}, {Artist: "Y", Title: "Seed B"}})
	want := []string{"Genesis", "Sexy Boy", "Phantom", "Playground Love"}
	if !got.Available || !reflect.DeepEqual(titles(got.Tracks), want) {
		t.Fatalf("got %v (available %v), want %v", titles(got.Tracks), got.Available, want)
	}
}

// Radio keeps owned tracks (it is not a discovery surface) but drops other
// versions and a second copy of one recording.
func TestRadioKeepsOwnedButDropsVersionsAndDuplicates(t *testing.T) {
	sim := seededSimilarity{
		"Seed A": {{Artist: "Justice", Title: "Genesis"}, {Artist: "Justice", Title: "Phantom (Live)"}},
		"Seed B": {{Artist: "Justice", Title: "Genesis - 2017 Remaster"}, {Artist: "Air", Title: "Sexy Boy"}},
	}
	deezer := &trackSource{plainSource: plainSource{name: "deezer"}, tracks: []core.ExternalResult{
		deezerTrack("2", "Phantom (Live)", "Justice"),
		deezerTrack("3", "Genesis - 2017 Remaster", "Justice"),
		deezerTrack("4", "Sexy Boy", "Air"),
	}}
	svc := newTrackService(sim, libraryMatcher{"genesis": "lib-1"}, nil, deezer)

	got := svc.Radio(context.Background(), []recommend.Seed{{Artist: "X", Title: "Seed A"}, {Artist: "Y", Title: "Seed B"}})
	if want := []string{"Genesis", "Sexy Boy"}; !reflect.DeepEqual(titles(got.Tracks), want) {
		t.Fatalf("got %v, want %v", titles(got.Tracks), want)
	}
	if got.Tracks[0].Source != "library" || got.Tracks[0].CanonicalID != "trk_lib-1" {
		t.Fatalf("owned track = %+v, want the library copy", got.Tracks[0])
	}
}

func TestRadioFromAnArtistLeadsWithTheirTracks(t *testing.T) {
	sim := seededSimilarity{
		"One More Time":  {{Artist: "Justice", Title: "Genesis"}},
		"Digital Love":   {{Artist: "Justice", Title: "Genesis"}, {Artist: "Air", Title: "Sexy Boy"}},
		"Aerodynamic":    {},
		"One More Time2": {},
	}
	deezer := &trackSource{plainSource: plainSource{name: "deezer"}, tracks: []core.ExternalResult{
		deezerTrack("d1", "Genesis", "Justice"),
		deezerTrack("d2", "Sexy Boy", "Air"),
	}}
	artistSrc := &artistTrackSource{plainSource: plainSource{name: "deezer"}, byArtist: []core.ExternalResult{
		deezerTrack("a1", "One More Time", "Daft Punk"),
		deezerTrack("a2", "One More Time (Live)", "Daft Punk"),
		deezerTrack("x", "One More Time", "Tribute Band"),
		deezerTrack("a3", "Digital Love", "Daft Punk"),
		deezerTrack("a4", "Aerodynamic", "Daft Punk"),
		deezerTrack("a5", "Voyager", "Daft Punk"),
	}, trackSource: deezer}
	svc := newTrackService(sim, libraryMatcher{}, nil, artistSrc)

	got := svc.Radio(context.Background(), []recommend.Seed{{Artist: "Daft Punk"}})
	want := []string{"One More Time", "Digital Love", "Aerodynamic", "Genesis", "Sexy Boy"}
	if !reflect.DeepEqual(titles(got.Tracks), want) {
		t.Fatalf("got %v, want %v", titles(got.Tracks), want)
	}
}

func TestRadioUnavailableWithoutTrackSource(t *testing.T) {
	got := newTrackService(nil, libraryMatcher{}, nil).Radio(context.Background(), []recommend.Seed{{Artist: "A", Title: "B"}})
	if got.Available || got.Tracks == nil {
		t.Fatalf("got %+v, want unavailable with an empty list", got)
	}
}

// artistTrackSource answers a search for the artist's name with byArtist and
// defers everything else to trackSource.
type artistTrackSource struct {
	plainSource
	byArtist    []core.ExternalResult
	trackSource *trackSource
}

func (s *artistTrackSource) Search(ctx context.Context, q string, t core.EntityType) ([]core.ExternalResult, error) {
	if q == "Daft Punk" {
		return s.byArtist, nil
	}
	return s.trackSource.Search(ctx, q, t)
}
