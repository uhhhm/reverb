package app

import (
	"net/http"
	"testing"
	"time"

	"github.com/uhhhm/reverb/internal/core"
	"github.com/uhhhm/reverb/internal/download/spotdl/spotdltest"
)

// A desktop with no Spotify search provider still imports, syncs and copies
// Spotify playlists: it reads them through its bundled spotDL. The playlist
// mixes a track the library owns with missing ones, and changes on Spotify
// between syncs; a private playlist is refused without leaving a playlist
// behind.
func TestSpotifyPlaylistImportWithoutSpotifyProvider(t *testing.T) {
	py, sp := spotdltest.Host(t)
	album := "Record"
	sp.Set(map[string]spotdltest.Playlist{
		"roadtrip": {Name: "Road Trip", CoverURL: "https://i.scdn.co/image/roadtrip", Tracks: []spotdltest.Track{
			// fakeSubsonic owns "Desktop Only" by Band, 180s.
			{ID: "sp-owned", Name: "Desktop Only", Artists: []string{"Band"}, Album: &album, Duration: 180},
			{ID: "sp-duet", Name: "Duet", Artists: []string{"Lead", "Feature"}, Duration: 225},
			{ID: "sp-cafe", Name: "Café del Mar", Artists: []string{"Énergie"}, Duration: 61},
		}},
		"secret": {Error: "Playlist is private or does not exist"},
	})
	d := newSyncDevice(t, "desktop", func(d *syncDevice) {
		d.python = py
		// Queued downloads fail here instead of reaching YouTube.
		d.env = map[string]string{"REVERB_SPOTDL_PATH": "/nonexistent/spotdl", "REVERB_YTDLP_PATH": "/nonexistent/yt-dlp"}
	})

	var synced core.SyncedPlaylistDetail
	d.must(http.MethodPost, "/playlists/import-synced", map[string]any{
		"url": "https://open.spotify.com/playlist/roadtrip?si=share", "downloadMissing": true,
	}, &synced, http.StatusOK)
	if synced.Name != "Road Trip" || synced.CoverURL != "https://i.scdn.co/image/roadtrip" || synced.Mode != "synced" {
		t.Fatalf("imported %+v", synced.SyncedPlaylist)
	}
	assertTracks(t, synced, []wantTrack{
		{"Desktop Only", "Band", core.CoverageFull, ""},
		{"Duet", "Lead", core.CoverageNone, "sp-duet"},
		{"Café del Mar", "Énergie", core.CoverageNone, "sp-cafe"},
	})
	if synced.Tracks[1].DurationMs != 225000 {
		t.Fatalf("Duet lasts %dms, want 225000", synced.Tracks[1].DurationMs)
	}
	// Download missing now queues exactly the two missing tracks, by Spotify id.
	queued := waitForDownloads(t, d, "sp-duet", "sp-cafe")
	if _, ok := queued["sp-owned"]; ok {
		t.Fatal("the owned track was queued for download")
	}

	// The playlist changes on Spotify; a sync follows it.
	sp.Set(map[string]spotdltest.Playlist{
		"roadtrip": {Name: "Road Trip (2026)", Tracks: []spotdltest.Track{
			{ID: "sp-owned", Name: "Desktop Only", Artists: []string{"Band"}, Album: &album, Duration: 180},
			{ID: "sp-new", Name: "New Song", Artists: []string{"Newcomer"}, Duration: 199},
			{ID: "sp-duet", Name: "Duet", Artists: []string{"Lead", "Feature"}, Duration: 225},
		}},
		"mixtape": {Name: "Mixtape", Tracks: []spotdltest.Track{
			{ID: "sp-owned", Name: "Desktop Only", Artists: []string{"Band"}, Album: &album, Duration: 180},
			{ID: "sp-tape", Name: "Tape Hiss", Artists: []string{"Cassette"}, Duration: 140},
		}},
		"secret": {Error: "Playlist is private or does not exist"},
	})
	var resynced core.SyncedPlaylistDetail
	d.must(http.MethodPost, "/playlists/"+synced.ID+"/sync", nil, &resynced, http.StatusOK)
	if resynced.Name != "Road Trip (2026)" {
		t.Fatalf("synced name %q", resynced.Name)
	}
	assertTracks(t, resynced, []wantTrack{
		{"Desktop Only", "Band", core.CoverageFull, ""},
		{"New Song", "Newcomer", core.CoverageNone, "sp-new"},
		{"Duet", "Lead", core.CoverageNone, "sp-duet"},
	})

	// A one-time import is an editable snapshot, and queues what is missing.
	var copied core.SyncedPlaylistDetail
	d.must(http.MethodPost, "/playlists/import", map[string]any{"url": "spotify:playlist:mixtape"}, &copied, http.StatusOK)
	if copied.Mode != "once" || copied.Name != "Mixtape" {
		t.Fatalf("one-time import: mode %q, name %q", copied.Mode, copied.Name)
	}
	assertTracks(t, copied, []wantTrack{
		{"Desktop Only", "Band", core.CoverageFull, ""},
		{"Tape Hiss", "Cassette", core.CoverageNone, "sp-tape"},
	})
	queued = waitForDownloads(t, d, "sp-duet", "sp-cafe", "sp-tape")

	// Spotify refusing a playlist is the user's to fix, and leaves nothing.
	var before []core.SyncedPlaylist
	d.must(http.MethodGet, "/playlists", nil, &before, http.StatusOK)
	status, raw := d.do(http.MethodPost, "/playlists/import-synced", map[string]any{"url": "https://open.spotify.com/playlist/secret"}, nil)
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("private playlist: status %d: %s", status, raw)
	}
	var after []core.SyncedPlaylist
	d.must(http.MethodGet, "/playlists", nil, &after, http.StatusOK)
	if len(after) != len(before) {
		t.Fatalf("a refused import changed the playlists: %d -> %d", len(before), len(after))
	}

	writeE2EArtifact(t, "spotify-playlist-via-spotdl.json", map[string]any{
		"imported": synced,
		"resynced": resynced,
		"oneTime":  copied,
		"queued":   queued,
		"refused":  string(raw),
	})
}

type wantTrack struct {
	title, artist string
	state         core.CoverageState
	externalID    string // the Spotify id of a missing track
}

func assertTracks(t *testing.T, det core.SyncedPlaylistDetail, want []wantTrack) {
	t.Helper()
	if len(det.Tracks) != len(want) {
		t.Fatalf("%d tracks, want %d: %+v", len(det.Tracks), len(want), det.Tracks)
	}
	for i, w := range want {
		got := det.Tracks[i]
		if got.Title != w.title || got.Artist != w.artist || got.State != w.state {
			t.Errorf("track %d = %q by %q (%s), want %q by %q (%s)", i, got.Title, got.Artist, got.State, w.title, w.artist, w.state)
		}
		if w.externalID != "" && (got.ExternalRef == nil || got.ExternalRef.Source != "spotify" || got.ExternalRef.ExternalID != w.externalID) {
			t.Errorf("track %d external ref = %+v, want spotify %s", i, got.ExternalRef, w.externalID)
		}
	}
}

// waitForDownloads waits until a download job exists for each Spotify id and
// returns every Spotify job by id.
func waitForDownloads(t *testing.T, d *syncDevice, ids ...string) map[string]string {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		var jobs []core.DownloadJob
		d.must(http.MethodGet, "/downloads", nil, &jobs, http.StatusOK)
		got := map[string]string{}
		for _, j := range jobs {
			if j.Source == "spotify" {
				got[j.ExternalID] = j.Title
			}
		}
		missing := ""
		for _, id := range ids {
			if _, ok := got[id]; !ok {
				missing = id
			}
		}
		if missing == "" {
			return got
		}
		if time.Now().After(deadline) {
			t.Fatalf("no download job for %s; jobs: %v", missing, got)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
