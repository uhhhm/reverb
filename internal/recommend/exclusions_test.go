package recommend_test

import (
	"context"
	"strings"
	"testing"

	"github.com/uhhhm/reverb/internal/core"
	"github.com/uhhhm/reverb/internal/recommend"
	"github.com/uhhhm/reverb/internal/search"
)

// marks is a Not interested set that can change between requests.
type marks struct {
	artists map[string]bool
	tracks  map[string]bool // source:externalId
}

func (m *marks) Artist(name string) bool { return m.artists[strings.ToLower(name)] }
func (m *marks) Track(ext core.ExternalResult) bool {
	return m.Artist(ext.Artist) || m.tracks[ext.Source+":"+ext.ExternalID]
}

func withMarks(m *marks) recommend.Option {
	return recommend.WithExclusions(func(context.Context) (recommend.Exclusions, error) { return m, nil })
}

func TestSimilarArtistsExcludeMarkedArtists(t *testing.T) {
	deezer := &similarSource{plainSource: plainSource{name: "deezer"}, related: relatedN(3)}
	m := &marks{artists: map[string]bool{}}
	svc := recommend.New(func() []search.SearchSource { return []search.SearchSource{deezer} }, withMarks(m))

	if got := svc.SimilarArtists(context.Background(), "deezer", "27"); len(got.Artists) != 3 {
		t.Fatalf("got %d artists before marking, want 3", len(got.Artists))
	}
	// Marked after the result was cached: the mark still applies at once.
	m.artists["artist 1"] = true
	got := svc.SimilarArtists(context.Background(), "deezer", "27")
	if len(got.Artists) != 2 {
		t.Fatalf("got %d artists, want the marked one removed", len(got.Artists))
	}
	for _, a := range got.Artists {
		if a.Name == "Artist 1" {
			t.Fatal("marked artist was recommended")
		}
	}
}

func TestSimilarTracksExcludeMarkedTracksAndArtists(t *testing.T) {
	ts := &similarTracks{cands: []recommend.TrackCandidate{
		{Artist: "Justice", Title: "D.A.N.C.E."},
		{Artist: "Stardust", Title: "Music Sounds Better with You"},
		{Artist: "Cassius", Title: "1999"},
	}}
	deezer := &trackSource{plainSource: plainSource{name: "deezer"}, tracks: []core.ExternalResult{
		deezerTrack("1", "D.A.N.C.E.", "Justice"),
		deezerTrack("2", "Music Sounds Better with You", "Stardust"),
		deezerTrack("3", "1999", "Cassius"),
	}}
	m := &marks{artists: map[string]bool{"stardust": true}, tracks: map[string]bool{"deezer:1": true}}
	svc := recommend.New(func() []search.SearchSource { return []search.SearchSource{deezer} },
		recommend.WithTrackSource(ts), withMarks(m))

	got := svc.SimilarTracks(context.Background(), "Daft Punk", "One More Time")
	if len(got.Tracks) != 1 || got.Tracks[0].ExternalID != "3" {
		t.Fatalf("got %+v, want only the unmarked Cassius track", got.Tracks)
	}
}
