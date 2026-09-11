package recommend

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/uhhhm/reverb/internal/core"
)

func TestVersionKinds(t *testing.T) {
	cases := []struct {
		title string
		want  []versionKind
	}{
		{"One More Time", nil},
		{"One More Time (Live)", []versionKind{versionLive}},
		{"One More Time - Live", []versionKind{versionLive}},
		{"One More Time [Live at Wembley 1997]", []versionKind{versionLive}},
		{"One More Time - Live from Alive 2007", []versionKind{versionLive}},
		{"One More Time (Recorded Live)", []versionKind{versionLive}},
		// "Live" in the song's own name is not a live version.
		{"Live Forever", nil},
		{"Live and Let Die", nil},
		{"Around the World (Remix)", []versionKind{versionRemix}},
		{"Around the World - Kaytranada Remix", []versionKind{versionRemix}},
		{"Around the World (Todd Terje Extended Mix)", []versionKind{versionRemix}},
		{"Around the World (Radio Edit)", nil},
		{"Around the World (Original Mix)", nil},
		{"Hurt (Cover)", []versionKind{versionCover}},
		{"Hurt - Nine Inch Nails Cover", []versionKind{versionCover}},
		{"Hurt (Originally Performed by Nine Inch Nails)", []versionKind{versionCover, versionKaraoke}},
		{"Hurt (Karaoke Version)", []versionKind{versionKaraoke}},
		{"Hurt (In the Style of Johnny Cash)", []versionKind{versionKaraoke}},
		{"Hurt (Instrumental)", []versionKind{versionInstrumental}},
		{"Hurt - Live Instrumental", []versionKind{versionLive, versionInstrumental}},
		{"Hurt (2011 Remaster)", nil},
	}
	for _, c := range cases {
		if got := versionKinds(c.title).list(); !reflect.DeepEqual(got, c.want) {
			t.Errorf("versionKinds(%q) = %v, want %v", c.title, got, c.want)
		}
	}
}

func TestRecordingKeyCollapsesReissues(t *testing.T) {
	same := [][2]string{
		{"Around the World", "Around the World - 2011 Remaster"},
		{"Around the World", "Around the World (Remastered 2009)"},
		{"Around the World", "Around the World [Remastered]"},
		{"Around the World", "Around the World (Deluxe Edition)"},
		{"Around the World", "Around the World - Deluxe"},
		{"Around the World", "Around the World (Bonus Track)"},
		{"Around the World", "around the world"},
	}
	for _, p := range same {
		if recordingKey(p[0], "Daft Punk") != recordingKey(p[1], "Daft Punk") {
			t.Errorf("%q and %q should collapse", p[0], p[1])
		}
	}
	different := [][2]string{
		{"Around the World", "Around the World (Live)"},
		{"Around the World", "Around the World (Remix)"},
		{"Around the World", "Around the World (Radio Edit)"},
		{"Around the World", "Around the Block"},
	}
	for _, p := range different {
		if recordingKey(p[0], "Daft Punk") == recordingKey(p[1], "Daft Punk") {
			t.Errorf("%q and %q should stay distinct", p[0], p[1])
		}
	}
	if recordingKey("Hurt", "Nine Inch Nails") == recordingKey("Hurt", "Johnny Cash") {
		t.Error("same title by different artists should stay distinct")
	}
	if recordingKey("Hurt", "Nine Inch Nails feat. Someone") != recordingKey("Hurt", "Nine Inch Nails") {
		t.Error("a featured credit should not split one recording")
	}
}

func ext(source, id, title, artist string) core.ExternalResult {
	return core.ExternalResult{Source: source, ExternalID: id, Title: title, Artist: artist, Type: core.EntityTrack}
}

func ids(tracks []core.ExternalResult) []string {
	out := []string{}
	for _, t := range tracks {
		out = append(out, t.ExternalID)
	}
	return out
}

func TestFilterCollapsesDuplicatesPreferringTheLibraryCopy(t *testing.T) {
	in := []core.ExternalResult{
		ext("deezer", "1", "Around the World", "Daft Punk"),
		ext("deezer", "2", "Harder, Better, Faster, Stronger", "Daft Punk"),
		ext("deezer", "3", "Around the World - 2011 Remaster", "Daft Punk"),
		ext("library", "lib-1", "Around the World (Deluxe Edition)", "Daft Punk"),
	}
	got := filterTracks(in, nil, surfacePolicy{}, nil)
	if want := []string{"lib-1", "2"}; !reflect.DeepEqual(ids(got), want) {
		t.Fatalf("got %v, want %v (duplicates collapsed, library copy kept in the first slot)", ids(got), want)
	}
}

func TestFilterExcludesOtherVersionsUnlessTheSeedIsOne(t *testing.T) {
	in := []core.ExternalResult{
		ext("deezer", "studio", "Genesis", "Justice"),
		ext("deezer", "live", "Phantom - Live", "Justice"),
		ext("deezer", "remix", "Stress (Remix)", "Justice"),
		ext("deezer", "karaoke", "D.A.N.C.E. (Karaoke Version)", "Justice"),
	}
	if got := ids(filterTracks(in, []Seed{{Artist: "Justice", Title: "DVNO"}}, surfacePolicy{}, nil)); !reflect.DeepEqual(got, []string{"studio"}) {
		t.Fatalf("studio seed: got %v, want only the studio track", got)
	}
	if got := ids(filterTracks(in, []Seed{{Artist: "Justice", Title: "DVNO (Live)"}}, surfacePolicy{}, nil)); !reflect.DeepEqual(got, []string{"studio", "live"}) {
		t.Fatalf("live seed: got %v, want studio and live", got)
	}
}

func TestFilterDropsTheSeedItself(t *testing.T) {
	in := []core.ExternalResult{
		ext("deezer", "seed", "One More Time - Remastered", "Daft Punk"),
		ext("deezer", "other", "Digital Love", "Daft Punk"),
	}
	got := filterTracks(in, []Seed{{Artist: "Daft Punk", Title: "One More Time"}}, surfacePolicy{}, nil)
	if !reflect.DeepEqual(ids(got), []string{"other"}) {
		t.Fatalf("got %v, want the seed (and its reissue) dropped", ids(got))
	}
}

func TestDiscoverySurfacesExcludeOwnedAndRecentlyPlayed(t *testing.T) {
	in := []core.ExternalResult{
		ext("library", "lib-1", "Digital Love", "Daft Punk"),
		ext("deezer", "played", "Veridis Quo (2001 Remaster)", "Daft Punk"),
		ext("deezer", "fresh", "Voyager", "Daft Punk"),
	}
	recent := map[string]bool{recordingKey("Veridis Quo", "Daft Punk"): true}

	got := filterTracks(in, nil, surfacePolicy{discovery: true}, recent)
	if !reflect.DeepEqual(ids(got), []string{"fresh"}) {
		t.Fatalf("discovery: got %v, want owned and recently played excluded", ids(got))
	}
	got = filterTracks(in, nil, surfacePolicy{}, recent)
	if !reflect.DeepEqual(ids(got), []string{"lib-1", "played", "fresh"}) {
		t.Fatalf("non-discovery: got %v, want everything kept", ids(got))
	}
}

func TestRecentlyPlayedReadsTheWindow(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	var since time.Time
	s := New(nil,
		WithClock(func() time.Time { return now }),
		WithRecentPlays(func(_ context.Context, from time.Time) ([]TrackCandidate, error) {
			since = from
			return []TrackCandidate{{Artist: "Daft Punk", Title: "Voyager"}}, nil
		}),
	)
	got := s.recentlyPlayed(context.Background())
	if !got[recordingKey("Voyager", "Daft Punk")] {
		t.Fatalf("recent = %v", got)
	}
	if want := now.Add(-recentPlayWindow); !since.Equal(want) {
		t.Fatalf("asked from %v, want %v", since, want)
	}
}
