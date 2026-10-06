package spotdl_test

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/uhhhm/reverb/internal/core"
	"github.com/uhhhm/reverb/internal/download/spotdl"
	"github.com/uhhhm/reverb/internal/download/spotdl/spotdltest"
	"github.com/uhhhm/reverb/internal/pyrun"
)

func ptr(s string) *string { return &s }

// A playlist read through spotDL is the playlist the Spotify search provider
// returns: the first artist names the track, durations are milliseconds, and
// fields spotDL leaves out are empty rather than an error.
func TestPlaylistReaderMapsSpotDLPlaylist(t *testing.T) {
	host, sp := spotdltest.Host(t)
	sp.Set(map[string]spotdltest.Playlist{
		"0NRK5JvEg44fPmZSSIb5": {Name: "Röad Trip ✨", CoverURL: "https://i.scdn.co/image/cover", Tracks: []spotdltest.Track{
			{ID: "trk1", Name: "Duet", Artists: []string{"Lead", "Feature"}, Album: ptr("Record"), Duration: 225,
				ISRC: ptr("GBAAA0400266"), CoverURL: ptr("https://i.scdn.co/image/album"), AlbumID: ptr("alb1"), ArtistID: ptr("art1")},
			{ID: "trk2", Name: "Café del Mar", Artists: []string{"Énergie"}, Duration: 61},
		}},
	})

	pl, err := spotdl.NewPlaylistReader(host).GetPlaylist(context.Background(), "0NRK5JvEg44fPmZSSIb5")
	if err != nil {
		t.Fatal(err)
	}
	want := core.ExternalPlaylist{
		Source: "spotify", ExternalID: "0NRK5JvEg44fPmZSSIb5", Name: "Röad Trip ✨", CoverURL: "https://i.scdn.co/image/cover",
		Tracks: []core.ExternalResult{
			{Source: "spotify", ExternalID: "trk1", Title: "Duet", Artist: "Lead", Album: "Record", DurationMs: 225000,
				ISRC: "GBAAA0400266", CoverURL: "https://i.scdn.co/image/album", Type: core.EntityTrack,
				ArtistExternalID: "art1", AlbumExternalID: "alb1"},
			{Source: "spotify", ExternalID: "trk2", Title: "Café del Mar", Artist: "Énergie", DurationMs: 61000, Type: core.EntityTrack},
		},
	}
	if fmt.Sprintf("%+v", pl) != fmt.Sprintf("%+v", want) {
		t.Fatalf("playlist =\n%+v\nwant\n%+v", pl, want)
	}
}

// Every track of a long playlist arrives: the output is read line by line, and
// a single line holding the whole playlist would pass the line reader's cap.
func TestPlaylistReaderReadsLongPlaylist(t *testing.T) {
	host, sp := spotdltest.Host(t)
	tracks := make([]spotdltest.Track, 3000)
	for i := range tracks {
		tracks[i] = spotdltest.Track{ID: fmt.Sprintf("t%04d", i), Name: strings.Repeat("long title ", 40), Artists: []string{"Band"}, Duration: 200}
	}
	sp.Set(map[string]spotdltest.Playlist{"long": {Name: "Long", Tracks: tracks}})

	pl, err := spotdl.NewPlaylistReader(host).GetPlaylist(context.Background(), "long")
	if err != nil {
		t.Fatal(err)
	}
	if len(pl.Tracks) != len(tracks) || pl.Tracks[2999].ExternalID != "t2999" {
		t.Fatalf("got %d tracks, last %q; want 3000 ending t2999", len(pl.Tracks), pl.Tracks[len(pl.Tracks)-1].ExternalID)
	}
}

// Spotify's reason for refusing a playlist reaches the caller.
func TestPlaylistReaderReportsSpotDLError(t *testing.T) {
	host, sp := spotdltest.Host(t)
	sp.Set(map[string]spotdltest.Playlist{"private": {Error: "Playlist is private or does not exist"}})

	_, err := spotdl.NewPlaylistReader(host).GetPlaylist(context.Background(), "private")
	if err == nil || !strings.Contains(err.Error(), "Playlist is private or does not exist") {
		t.Fatalf("err = %v, want spotDL's reason", err)
	}
	if errors.Is(err, spotdl.ErrNotInstalled) {
		t.Fatalf("a refused playlist is not a missing spotDL: %v", err)
	}
}

// A Python without spotDL is reported as that, so the caller can say spotDL
// is missing instead of blaming the playlist.
func TestPlaylistReaderReportsMissingSpotDL(t *testing.T) {
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not installed")
	}
	if exec.Command(py, "-c", "import spotdl").Run() == nil {
		t.Skip("host python3 has spotDL installed")
	}
	_, err = spotdl.NewPlaylistReader(pyrun.Host{Python: py}).GetPlaylist(context.Background(), "any")
	if !errors.Is(err, spotdl.ErrNotInstalled) {
		t.Fatalf("err = %v, want ErrNotInstalled", err)
	}
}

// A missing interpreter is a missing spotDL too.
func TestPlaylistReaderReportsMissingPython(t *testing.T) {
	_, err := spotdl.NewPlaylistReader(pyrun.Host{Python: "/nonexistent/python3"}).GetPlaylist(context.Background(), "any")
	if !errors.Is(err, spotdl.ErrNotInstalled) {
		t.Fatalf("err = %v, want ErrNotInstalled", err)
	}
}

// A read that never finishes stops with its context.
func TestPlaylistReaderStopsWithContext(t *testing.T) {
	host, sp := spotdltest.Host(t)
	sp.Set(map[string]spotdltest.Playlist{"stuck": {Hang: true}})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	start := time.Now()
	_, err := spotdl.NewPlaylistReader(host).GetPlaylist(ctx, "stuck")
	if err == nil {
		t.Fatal("a hung read returned no error")
	}
	if d := time.Since(start); d > 10*time.Second {
		t.Fatalf("a hung read took %v to stop", d)
	}
}

// Only a Spotify playlist id reaches spotDL.
func TestPlaylistReaderRejectsMalformedID(t *testing.T) {
	host, _ := spotdltest.Host(t)
	for _, id := range []string{"", "../etc", "abc def", "abc?x=1", "--help"} {
		if _, err := spotdl.NewPlaylistReader(host).GetPlaylist(context.Background(), id); err == nil {
			t.Errorf("id %q was accepted", id)
		}
	}
}
