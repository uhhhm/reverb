package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/uhhhm/reverb/internal/core"
	"github.com/uhhhm/reverb/internal/offlineset"
	"github.com/uhhhm/reverb/internal/registry"
	"github.com/uhhhm/reverb/internal/store"
	"github.com/uhhhm/reverb/internal/store/db"
	reverbsync "github.com/uhhhm/reverb/internal/sync"
)

type fakeKeeper struct {
	status  offlineset.Status
	changed int
}

func (f *fakeKeeper) Status(context.Context) (offlineset.Status, error) { return f.status, nil }
func (f *fakeKeeper) Changed()                                          { f.changed++ }

// The phone app decodes these responses with a client generated from OpenAPI,
// so the contract runner validates the real handlers' output against it.
func TestMobileHTTPContract(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(t.TempDir() + "/mobile.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	authSvc, tok := seededAuthToken(t, st)
	if _, err := reverbsync.EnsureServerDevice(ctx, st.Q()); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Q().UpsertSyncedPlaylist(ctx, db.UpsertSyncedPlaylistParams{
		ID: "pl1", Source: "local", ExternalID: "pl1", Name: "Commute", TracksJson: "[]", Mode: "once", CreatedAt: time.Now().Unix(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.Q().InsertCatalogEntity(ctx, db.InsertCatalogEntityParams{
		ID: "trk_remote", Kind: "track", Title: "Remote", Artist: "Band", Album: "Record", DurationMs: 180000,
	}); err != nil {
		t.Fatal(err)
	}
	summary := core.SyncedPlaylist{ID: "pl1", Source: "local", ExternalID: "pl1", Name: "Commute", Mode: "once", TrackCount: 2}
	sync := &fakeSync{
		list: []core.SyncedPlaylist{summary},
		detail: core.SyncedPlaylistDetail{SyncedPlaylist: summary, OwnedCount: 1, TotalCount: 2, Tracks: []core.AlbumDetailTrack{
			{State: core.CoverageFull, Title: "One", Artist: "Band", Album: "Record", TrackNumber: 1,
				LibraryTrack: &core.Track{ID: "tr-1", Title: "One", Artist: "Band", Album: "Record", AlbumID: "al-1", ArtistID: "ar-1", Suffix: "mp3", ContentType: "audio/mpeg"},
				Key:          &core.TrackKey{Source: "deezer", ExternalID: "1"}},
			{State: core.CoverageNone, CanonicalID: "trk_peer", Title: "Two", Artist: "Band", TrackNumber: 2, DurationMs: 180000,
				ExternalRef: &core.ExternalTrackRef{Source: "deezer", ExternalID: "2", Title: "Two", Artist: "Band", DurationMs: 180000},
				Key:         &core.TrackKey{Source: "deezer", ExternalID: "2"}},
		}},
	}
	keeper := &fakeKeeper{status: offlineset.Status{UsedBytes: 4096, AvailableBytes: 1 << 30, Full: true, Playlists: []offlineset.PlaylistStatus{{
		PlaylistID: "pl1", PlaylistName: "Commute", Bytes: 4096, TrackCount: 3, ReadyCount: 1, Tracks: []offlineset.TrackStatus{
			{Title: "One", Artist: "Band", Album: "Record", State: offlineset.StateReady, SizeBytes: 4096, FetchedBytes: 4096},
			{Title: "Two", Artist: "Band", State: offlineset.StateFetching, SizeBytes: 8192, FetchedBytes: 100},
			{Title: "Three", Artist: "Band", State: offlineset.StateNoSpace, SizeBytes: 1 << 40},
		},
	}}}}
	srv := NewServer(Deps{AllowedHosts: testAllowedHosts,
		Auth: authSvc, Sync: sync, PlaylistOwner: st.Q(), OfflineSet: st.Q(), OfflineKeeper: keeper, PairingStore: st.Q(),
		Search: registry.NewRegistry("search"), Downloader: registry.NewRegistry("downloader"),
		DelegatedStream: &fakeDelegatedStream{},
		CatalogBrowse:   st.Q(),
	})
	samples := map[string]json.RawMessage{}
	for _, c := range []struct{ key, method, path, body string }{
		{"health", http.MethodGet, "/health", ""},
		{"devices", http.MethodGet, "/pairing/devices", ""},
		{"catalog", http.MethodGet, "/library/catalog/tracks", ""},
		{"playlists", http.MethodGet, "/playlists", ""},
		{"playlist", http.MethodGet, "/playlists/pl1", ""},
		{"offlinePut", http.MethodPut, "/offline-set/pl1", `{"enabled":true}`},
		{"offlineList", http.MethodGet, "/offline-set", ""},
		{"offlineStatus", http.MethodGet, "/offline-set/status", ""},
		{"offlineDelete", http.MethodDelete, "/offline-set/pl1", ""},
	} {
		req := httptest.NewRequest(c.method, "/api/v1"+c.path, strings.NewReader(c.body))
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: tok})
		if c.body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", c.key, rec.Code, rec.Body.String())
		}
		samples[c.key] = append(json.RawMessage(nil), rec.Body.Bytes()...)
	}
	var devices []deviceDTO
	if err := json.Unmarshal(samples["devices"], &devices); err != nil || len(devices) == 0 {
		t.Fatalf("devices = %s", samples["devices"])
	}
	for _, d := range devices {
		if !d.ThisDevice {
			t.Fatalf("device %s is this device's own row but is not marked as such", d.ID)
		}
	}
	if keeper.changed != 2 {
		t.Fatalf("the keeper heard of %d offline set changes, want 2", keeper.changed)
	}
	var playlist core.SyncedPlaylistDetail
	if err := json.Unmarshal(samples["playlist"], &playlist); err != nil {
		t.Fatal(err)
	}
	if playlist.Tracks[0].Playback != core.PlaybackLocal || playlist.Tracks[1].Playback != core.PlaybackDelegated {
		t.Fatalf("playback states = %q, %q", playlist.Tracks[0].Playback, playlist.Tracks[1].Playback)
	}
	if path := os.Getenv("REVERB_MOBILE_CONTRACT_OUTPUT"); path != "" {
		data, err := json.Marshal(samples)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}
