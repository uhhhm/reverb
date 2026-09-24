package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/uhhhm/reverb/internal/core"
	"github.com/uhhhm/reverb/internal/linkadd"
	reverbsync "github.com/uhhhm/reverb/internal/sync"
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
func collectionLinkServer(t *testing.T) (*Server, *http.Cookie, *fakeManager, func(string) bool) {
	t.Helper()
	srv, st, cookie, fake := linkTestServer(t, nil)
	cols := &fakeCollections{
		album: core.ExternalAlbum{Source: "spotify", ExternalID: "4aawyAB9vmqN3uQ7FjRGTy", Name: "Discovery", Artist: "Daft Punk", Tracks: []core.ExternalResult{
			{Source: "spotify", ExternalID: "trk1", Title: "One More Time", Artist: "Daft Punk", ISRC: "GBDUW0000053"},
			{Source: "spotify", ExternalID: "trk2", Title: "Aerodynamic", Artist: "Daft Punk"},
		}},
		playlist: core.ExternalPlaylist{Source: "spotify", ExternalID: "37i9dQZF1DXcBWIGoYBM5M", Name: "Hits", Tracks: []core.ExternalResult{
			{Source: "spotify", ExternalID: "trk3", Title: "Song", Artist: "Singer", Album: "LP"},
		}},
	}
	syncStore := reverbsync.NewSyncStore(st.Q())
	srv.deps.LinkAdd = linkadd.New(st.Q(), syncStore, fake, linkadd.WithCollections(cols),
		linkadd.WithDeviceID(func(ctx context.Context) (string, error) { return reverbsync.ServerDeviceID(ctx, st.Q()) }))
	seedPlaylist(t, st, "pl-phone")
	hasCatalog := func(id string) bool {
		_, err := st.Q().GetCatalogEntity(context.Background(), id)
		return err == nil
	}
	return srv, cookie, fake, hasCatalog
}

// On a phone, whose yt-dlp downloads one track at a time, an album link
// becomes one download per track, each named by the source's own metadata,
// and each track joins the playlist.
func TestLinkAddExpandsAnAlbumIntoItsTracks(t *testing.T) {
	srv, cookie, fake, hasCatalog := collectionLinkServer(t)
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
		first.Album != "Discovery" || first.ISRC != "GBDUW0000053" || first.AddToPlaylistID != "pl-phone" {
		t.Fatalf("first request = %+v", first)
	}
	for _, id := range []string{"trk1", "trk2"} {
		if !hasCatalog(linkadd.CatalogID("spotify", "track", id)) {
			t.Fatalf("track %s has no catalog entry", id)
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

// A playlist link adds its tracks without downloading them when asked.
func TestLinkAddExpandsAPlaylistWithoutDownloading(t *testing.T) {
	srv, cookie, fake, hasCatalog := collectionLinkServer(t)
	rec := doLink(t, srv, cookie, http.MethodPost, "/api/v1/links/add",
		`{"url":"https://open.spotify.com/playlist/37i9dQZF1DXcBWIGoYBM5M","playlistId":"pl-phone","download":false}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if len(fake.allReqs) != 0 {
		t.Fatalf("enqueued %d downloads with download false", len(fake.allReqs))
	}
	if !hasCatalog(linkadd.CatalogID("spotify", "track", "trk3")) {
		t.Fatal("the playlist's track has no catalog entry")
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
