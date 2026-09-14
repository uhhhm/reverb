package api

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"testing"

	"github.com/uhhhm/reverb/internal/core"
	"github.com/uhhhm/reverb/internal/recommend"
)

func (f *fakeRecommendations) Shelves(context.Context) recommend.Shelves {
	return f.shelves
}

func (f *fakeRecommendations) Mix(_ context.Context, kind recommend.MixKind) recommend.Mix {
	m := f.mixes[kind]
	m.Kind = kind
	if m.Tracks == nil {
		m.Tracks = []core.ExternalResult{}
	}
	return m
}

func (f *fakeRecommendations) PlaylistSuggestions(_ context.Context, playlist []recommend.Seed, page int) recommend.TrackResult {
	f.gotSeeds, f.gotPage = playlist, page
	f.suggestionCalls++
	return f.tracks
}

func TestShelvesEndpoint(t *testing.T) {
	var empty recommend.Shelves
	if code := getJSON(t, recommendationServer(t, nil), "/recommendations/shelves", &empty); code != http.StatusOK || empty.Shelves == nil {
		t.Fatalf("without recommendations: %d %+v", code, empty)
	}
	fake := &fakeRecommendations{shelves: recommend.Shelves{Refreshing: true, Shelves: []recommend.Shelf{{Kind: recommend.ShelfArtistsYouMightLike}}}}
	var got recommend.Shelves
	if code := getJSON(t, recommendationServer(t, fake), "/recommendations/shelves", &got); code != http.StatusOK || !got.Refreshing || len(got.Shelves) != 1 {
		t.Fatalf("status %d body %+v", code, got)
	}
}

func TestMixEndpoints(t *testing.T) {
	fake := &fakeRecommendations{mixes: map[recommend.MixKind]recommend.Mix{
		recommend.MixDiscoverWeekly: {Period: "2026-09-07", Available: true, Tracks: []core.ExternalResult{{Source: "deezer", ExternalID: "1", Title: "Xone", Artist: "Xenon", Type: core.EntityTrack}}},
	}}
	srv := recommendationServer(t, fake)

	var list mixList
	if code := getJSON(t, srv, "/recommendations/mixes", &list); code != http.StatusOK || len(list.Mixes) != len(recommend.MixKinds) {
		t.Fatalf("status %d list %+v", code, list)
	}
	var mix recommend.Mix
	if code := getJSON(t, srv, "/recommendations/mixes/discoverWeekly", &mix); code != http.StatusOK || mix.Period != "2026-09-07" || len(mix.Tracks) != 1 {
		t.Fatalf("status %d mix %+v", code, mix)
	}
	if code := getJSON(t, srv, "/recommendations/mixes/dailyMix9", nil); code != http.StatusNotFound {
		t.Fatalf("unknown mix: status %d, want 404", code)
	}
}

func TestSaveMixCreatesAManagedPlaylistWithEveryTrack(t *testing.T) {
	svc := &fakeSync{createDet: core.SyncedPlaylistDetail{SyncedPlaylist: core.SyncedPlaylist{ID: "pl-1", Mode: "once"}}}
	srv, cookie := syncTestServer(t, svc)
	srv.deps.Recommend = &fakeRecommendations{mixes: map[recommend.MixKind]recommend.Mix{
		recommend.MixDiscoverWeekly: {Tracks: []core.ExternalResult{
			{Source: "library", ExternalID: "lib-1", Title: "Owned", Artist: "A", Type: core.EntityTrack},
			{Source: "deezer", ExternalID: "d-2", Title: "Streamed", Artist: "B", Type: core.EntityTrack},
		}},
	}}

	rec := doAuthedBody(t, srv, http.MethodPost, "/api/v1/recommendations/mixes/discoverWeekly/playlist", `{}`, cookie)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if svc.lastCreateName != "Discover Weekly" || svc.addTrackID != "pl-1" {
		t.Fatalf("created %q, added to %q", svc.lastCreateName, svc.addTrackID)
	}
	// Search-source tracks are saved as they are, to play or download later.
	if e := svc.addTrackEntry; e.Source != "deezer" || e.ExternalID != "d-2" || e.Title != "Streamed" {
		t.Fatalf("last entry %+v", e)
	}
	// One edit holding every track, and nothing downloads.
	if svc.addTracksCount != 2 || svc.addTracksDownload {
		t.Fatalf("added %d tracks with download=%v, want 2 without", svc.addTracksCount, svc.addTracksDownload)
	}

	rec = doAuthedBody(t, srv, http.MethodPost, "/api/v1/recommendations/mixes/releaseRadar/playlist", `{"name":"Fresh"}`, cookie)
	if rec.Code != http.StatusConflict {
		t.Fatalf("empty mix: status %d, want 409", rec.Code)
	}
}

func TestPlaylistSuggestionsOnlyForManagedPlaylists(t *testing.T) {
	managed := core.SyncedPlaylistDetail{
		SyncedPlaylist: core.SyncedPlaylist{ID: "pl-1", Mode: "once"},
		Tracks:         []core.AlbumDetailTrack{{Title: "Xtal", Artist: "Aphex"}, {Title: "Roygbiv", Artist: "Boards"}},
	}
	svc := &fakeSync{detail: managed}
	srv, cookie := syncTestServer(t, svc)
	fake := &fakeRecommendations{tracks: recommend.TrackResult{Available: true, Tracks: []core.ExternalResult{{Source: "deezer", ExternalID: "1", Title: "Kiara", Artist: "Bonobo", Type: core.EntityTrack}}}}
	srv.deps.Recommend = fake

	rec := doAuthedBody(t, srv, http.MethodGet, "/api/v1/recommendations/playlists/pl-1/suggestions?page=2", "", cookie)
	var got recommend.TrackResult
	if err := json.Unmarshal(rec.Body.Bytes(), &got); rec.Code != http.StatusOK || err != nil || len(got.Tracks) != 1 {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	want := []recommend.Seed{{Artist: "Aphex", Title: "Xtal"}, {Artist: "Boards", Title: "Roygbiv"}}
	if !reflect.DeepEqual(fake.gotSeeds, want) || fake.gotPage != 2 {
		t.Fatalf("seeds %+v page %d", fake.gotSeeds, fake.gotPage)
	}

	svc.detail.Mode = "synced"
	rec = doAuthedBody(t, srv, http.MethodGet, "/api/v1/recommendations/playlists/pl-1/suggestions", "", cookie)
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || got.Available || len(got.Tracks) != 0 || fake.suggestionCalls != 1 {
		t.Fatalf("mirrored playlist got %s (%d lookups)", rec.Body.String(), fake.suggestionCalls)
	}
}
