package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/uhhhm/reverb/internal/core"
	"github.com/uhhhm/reverb/internal/linkadd"
)

// fakeCollections answers album and playlist lookups from fixed tracks.
type fakeCollections struct {
	album    core.ExternalAlbum
	playlist core.ExternalPlaylist
}

func (f *fakeCollections) GetAlbum(_ context.Context, source, id string) (core.ExternalAlbum, error) {
	if source != "spotify" || id != f.album.ExternalID {
		return core.ExternalAlbum{}, errors.New("not found")
	}
	return f.album, nil
}

func (f *fakeCollections) GetPlaylist(_ context.Context, source, id string) (core.ExternalPlaylist, error) {
	if source != "spotify" || id != f.playlist.ExternalID {
		return core.ExternalPlaylist{}, errors.New("not found")
	}
	return f.playlist, nil
}

// collectionLinkServer is linkTestServer with a phone's link planner, which
// expands albums and playlists into their tracks.
func collectionLinkServer(t *testing.T) (*Server, *http.Cookie, *fakeManager, *recordingPlaylists) {
	t.Helper()
	cols := &fakeCollections{
		album: core.ExternalAlbum{Source: "spotify", ExternalID: "4aawyAB9vmqN3uQ7FjRGTy", Name: "Discovery", Artist: "Daft Punk", Tracks: []core.ExternalResult{
			{Source: "spotify", ExternalID: "trk1", Title: "One More Time", Artist: "Daft Punk", ISRC: "GBDUW0000053"},
			{Source: "spotify", ExternalID: "trk2", Title: "Aerodynamic", Artist: "Daft Punk"},
		}},
		playlist: core.ExternalPlaylist{Source: "spotify", ExternalID: "37i9dQZF1DXcBWIGoYBM5M", Name: "Hits", Tracks: []core.ExternalResult{
			{Source: "spotify", ExternalID: "trk3", Title: "Song", Artist: "Singer", Album: "LP"},
		}},
	}
	srv, st, cookie, fake, playlists := linkTestServerWith(t, nil, linkadd.WithCollections(cols))
	seedPlaylist(t, st, "pl-phone")
	return srv, cookie, fake, playlists
}

// memberIDs is each playlist entry's source id, in order.
func memberIDs(adds []playlistAdd) []string {
	var out []string
	for _, a := range adds {
		for _, e := range a.entries {
			out = append(out, e.ExternalID)
		}
	}
	return out
}

// On a phone, whose yt-dlp downloads one track at a time, an album link
// becomes one download per track, each named by the source's own metadata,
// and each track joins the playlist.
func TestLinkAddExpandsAnAlbumIntoItsTracks(t *testing.T) {
	srv, cookie, fake, playlists := collectionLinkServer(t)
	rec := doLink(t, srv, cookie, http.MethodPost, "/api/v1/links/add",
		`{"url":"https://open.spotify.com/album/4aawyAB9vmqN3uQ7FjRGTy","playlistId":"pl-phone","download":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if len(fake.allReqs) != 2 {
		t.Fatalf("enqueued %d requests, want one per track", len(fake.allReqs))
	}
	first := fake.allReqs[0]
	if first.ExternalID != "trk1" || first.Title != "One More Time" || first.Artist != "Daft Punk" ||
		first.Album != "Discovery" || first.ISRC != "GBDUW0000053" {
		t.Fatalf("first request = %+v", first)
	}
	adds := playlists.all()
	if got := memberIDs(adds); len(adds) != 1 || strings.Join(got, ",") != "trk1,trk2" {
		t.Fatalf("playlist edits = %+v, want the album's tracks in order", adds)
	}
	for _, e := range adds[0].entries {
		if e.CanonicalID == "" || e.Album != "Discovery" {
			t.Fatalf("member %+v has no catalog id or album", e)
		}
	}
	var out struct {
		Resolve struct{ Title, Artist string } `json:"resolve"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Resolve.Title != "Discovery" || out.Resolve.Artist != "Daft Punk" {
		t.Fatalf("resolve = %+v, want the album's own name", out.Resolve)
	}
}

func TestLinkAddReportsPartialDownloadAfterPlaylistWasAdded(t *testing.T) {
	srv, cookie, fake, _ := collectionLinkServer(t)
	fake.enqueueErrAt = 2
	rec := doLink(t, srv, cookie, http.MethodPost, "/api/v1/links/add",
		`{"url":"https://open.spotify.com/album/4aawyAB9vmqN3uQ7FjRGTy","playlistId":"pl-phone","download":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var out struct {
		PlaylistID    string            `json:"playlistId"`
		Job           *core.DownloadJob `json:"job"`
		DownloadError string            `json:"downloadError"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.PlaylistID != "pl-phone" || out.Job == nil || out.Job.ExternalID != "trk1" {
		t.Fatalf("partial result = %+v, want the playlist and first queued job", out)
	}
	if out.DownloadError != "queue database unavailable" {
		t.Fatalf("downloadError = %q", out.DownloadError)
	}
}

// A playlist link adds its tracks without downloading them when asked.
func TestLinkAddExpandsAPlaylistWithoutDownloading(t *testing.T) {
	srv, cookie, fake, playlists := collectionLinkServer(t)
	rec := doLink(t, srv, cookie, http.MethodPost, "/api/v1/links/add",
		`{"url":"https://open.spotify.com/playlist/37i9dQZF1DXcBWIGoYBM5M","playlistId":"pl-phone","download":false}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if len(fake.allReqs) != 0 {
		t.Fatalf("enqueued %d downloads with download false", len(fake.allReqs))
	}
	if got := memberIDs(playlists.all()); strings.Join(got, ",") != "trk3" {
		t.Fatalf("members = %v, want the playlist's track", got)
	}
}

// A phone downloads a Spotify track by searching for its artist and title, so
// a track Spotify could not name is refused rather than searched for as
// "Unknown".
func TestLinkAddRefusesASpotifyTrackItCannotName(t *testing.T) {
	srv, cookie, fake, _ := collectionLinkServer(t)
	rec := doLink(t, srv, cookie, http.MethodPost, "/api/v1/links/add",
		`{"url":"https://open.spotify.com/track/2Foc5Q5nqNiosCNqttzHof","download":true}`)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if len(fake.allReqs) != 0 {
		t.Fatalf("enqueued %+v for an unnamed track", fake.allReqs)
	}
}

// A desktop downloads an album link whole, as spotDL takes it, but the
// playlist it is added to still gets the album's tracks.
func TestLinkAddListsAnAlbumForAPlaylistButDownloadsItWhole(t *testing.T) {
	cols := &fakeCollections{album: core.ExternalAlbum{Source: "spotify", ExternalID: "4aawyAB9vmqN3uQ7FjRGTy", Name: "Discovery", Artist: "Daft Punk", Tracks: []core.ExternalResult{
		{Source: "spotify", ExternalID: "trk1", Title: "One More Time", Artist: "Daft Punk"},
		{Source: "spotify", ExternalID: "trk2", Title: "Aerodynamic", Artist: "Daft Punk"},
	}}}
	srv, st, cookie, fake, playlists := linkTestServerWith(t, nil, linkadd.WithCollectionListing(cols))
	seedPlaylist(t, st, "pl-desk")
	rec := doLink(t, srv, cookie, http.MethodPost, "/api/v1/links/add",
		`{"url":"https://open.spotify.com/album/4aawyAB9vmqN3uQ7FjRGTy","playlistId":"pl-desk"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if len(fake.allReqs) != 1 || fake.allReqs[0].ExternalID != "4aawyAB9vmqN3uQ7FjRGTy" {
		t.Fatalf("downloads = %+v, want the album link once", fake.allReqs)
	}
	if got := memberIDs(playlists.all()); strings.Join(got, ",") != "trk1,trk2" {
		t.Fatalf("members = %v, want the album's tracks", got)
	}

	// Without a way to list it, the link is refused before anything is written.
	srv2, st2, cookie2, fake2, playlists2 := linkTestServerWith(t, nil)
	seedPlaylist(t, st2, "pl-desk")
	rec = doLink(t, srv2, cookie2, http.MethodPost, "/api/v1/links/add",
		`{"url":"https://open.spotify.com/album/4aawyAB9vmqN3uQ7FjRGTy","playlistId":"pl-desk"}`)
	if rec.Code != http.StatusUnprocessableEntity || fake2.enqueueCalls != 0 || len(playlists2.all()) != 0 {
		t.Fatalf("status %d, %d downloads, %+v", rec.Code, fake2.enqueueCalls, playlists2.all())
	}
}
