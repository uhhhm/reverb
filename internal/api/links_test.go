package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/uhhhm/reverb/internal/core"
	"github.com/uhhhm/reverb/internal/linkadd"
	"github.com/uhhhm/reverb/internal/registry"
	"github.com/uhhhm/reverb/internal/store"
	"github.com/uhhhm/reverb/internal/store/db"
	reverbsync "github.com/uhhhm/reverb/internal/sync"
)

func linkTestServer(t *testing.T, mgr DownloadManager) (*Server, *store.Store, *http.Cookie, *fakeManager) {
	t.Helper()
	st, err := store.Open(t.TempDir() + "/links.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	authSvc, tok := seededAuthToken(t, st)
	// Ensure server device for sync emit
	if _, err := reverbsync.EnsureServerDevice(context.Background(), st.Q()); err != nil {
		t.Fatal(err)
	}
	syncStore := reverbsync.NewSyncStore(st.Q())
	if mgr == nil {
		mgr = newFakeManager()
	}
	fake, _ := mgr.(*fakeManager)
	if fake == nil {
		fake = newFakeManager()
		// if mgr was not fake, keep original but fake stays separate for assertions
		// In tests where mgr is fake, this is the same.
	}
	linkAddSvc := linkadd.New(st.Q(), syncStore, mgr, linkadd.WithDeviceID(func(ctx context.Context) (string, error) {
		return reverbsync.ServerDeviceID(ctx, st.Q())
	}))
	srv := NewServer(Deps{
		Auth:          authSvc,
		Downloads:     mgr,
		Search:        registry.NewRegistry("search"),
		Downloader:    registry.NewRegistry("downloader"),
		SyncStore:     syncStore,
		LinkStore:     st.Q(),
		OfflineSet:    st.Q(),
		PairingStore:  st.Q(),
		PlaylistOwner: st.Q(),
		LinkAdd:       linkAddSvc,
	})
	cookie := &http.Cookie{Name: sessionCookie, Value: tok}
	_ = authSvc
	_ = tok
	return srv, st, cookie, fake
}

func doLink(t *testing.T, srv *Server, cookie *http.Cookie, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(cookie)
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func TestLinkResolve(t *testing.T) {
	mgr := newFakeManager()
	srv, st, cookie, fake := linkTestServer(t, mgr)
	_ = st
	_ = fake

	t.Run("resolve valid spotify", func(t *testing.T) {
		rec := doLink(t, srv, cookie, http.MethodPost, "/api/v1/links/resolve", `{"url":"https://open.spotify.com/track/sp123"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
		}
		var res map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
			t.Fatal(err)
		}
		if res["source"] != "spotify" {
			t.Fatalf("source %v", res["source"])
		}
		if res["externalId"] != "sp123" {
			t.Fatalf("externalId %v", res["externalId"])
		}
		if res["kind"] != "track" {
			t.Fatalf("kind %v", res["kind"])
		}
	})

	t.Run("resolve valid youtube watch", func(t *testing.T) {
		rec := doLink(t, srv, cookie, http.MethodPost, "/api/v1/links/resolve", `{"url":"https://www.youtube.com/watch?v=yt123"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
		}
		var res map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
			t.Fatal(err)
		}
		if res["source"] != "youtube" || res["externalId"] != "yt123" {
			t.Fatalf("youtube resolve %v", res)
		}
	})

	t.Run("resolve valid youtu.be", func(t *testing.T) {
		rec := doLink(t, srv, cookie, http.MethodPost, "/api/v1/links/resolve", `{"url":"https://youtu.be/abcDEF"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("status %d", rec.Code)
		}
		var res map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &res)
		if res["source"] != "youtube" || res["externalId"] != "abcDEF" {
			t.Fatalf("youtu.be %v", res)
		}
	})

	t.Run("resolve valid youtube playlist", func(t *testing.T) {
		rec := doLink(t, srv, cookie, http.MethodPost, "/api/v1/links/resolve", `{"url":"https://www.youtube.com/playlist?list=PLxyz123"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
		}
		var res map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &res)
		if res["source"] != "youtube" || res["kind"] != "playlist" || res["externalId"] != "PLxyz123" {
			t.Fatalf("playlist %v", res)
		}
	})

	t.Run("resolve validates url required", func(t *testing.T) {
		rec := doLink(t, srv, cookie, http.MethodPost, "/api/v1/links/resolve", `{"url":""}`)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("want 400, got %d", rec.Code)
		}
		rec2 := doLink(t, srv, cookie, http.MethodPost, "/api/v1/links/resolve", `{}`)
		if rec2.Code != http.StatusBadRequest {
			t.Fatalf("want 400 for missing url, got %d", rec2.Code)
		}
	})

	t.Run("resolve invalid unsupported", func(t *testing.T) {
		rec := doLink(t, srv, cookie, http.MethodPost, "/api/v1/links/resolve", `{"url":"https://example.com/foo"}`)
		if rec.Code != http.StatusUnprocessableEntity && rec.Code != http.StatusBadRequest {
			t.Fatalf("want 422 or 400, got %d", rec.Code)
		}
	})

	t.Run("add with download true default enqueues and creates catalog and emits sync", func(t *testing.T) {
		mgr2 := newFakeManager()
		srv2, st2, cookie2, fake2 := linkTestServer(t, mgr2)
		// No download field => default true should enqueue
		rec := doLink(t, srv2, cookie2, http.MethodPost, "/api/v1/links/add", `{"url":"https://open.spotify.com/track/spDL1"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("add status %d: %s", rec.Code, rec.Body.String())
		}
		var resp map[string]json.RawMessage
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		if _, ok := resp["job"]; !ok {
			t.Fatalf("expected job when download default true, got %s", rec.Body.String())
		}
		if _, ok := resp["catalogId"]; !ok {
			t.Fatalf("expected catalogId")
		}
		var catalogID string
		_ = json.Unmarshal(resp["catalogId"], &catalogID)
		if catalogID == "" {
			t.Fatalf("catalogId empty")
		}
		// catalog_entity exists
		ent, err := st2.Q().GetCatalogEntity(context.Background(), catalogID)
		if err != nil {
			t.Fatalf("catalog not created: %v", err)
		}
		if ent.ExternalID != "spDL1" || ent.Source != "spotify" {
			t.Fatalf("catalog %+v", ent)
		}
		// enqueued
		if fake2.enqueueCalls != 1 {
			t.Fatalf("enqueueCalls %d want 1", fake2.enqueueCalls)
		}
		if fake2.lastReq.Source != "spotify" || fake2.lastReq.ExternalID != "spDL1" {
			t.Fatalf("lastReq %+v", fake2.lastReq)
		}
		// emits sync_change: check at least one track change
		count, err := st2.Q().CountSyncChanges(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if count == 0 {
			t.Fatalf("expected sync_change emitted")
		}
		// verify track sync exists via store helper
		ss := reverbsync.NewSyncStore(st2.Q())
		latest, err := ss.GetLatestForField(context.Background(), "track", catalogID, "title")
		if err != nil {
			t.Fatal(err)
		}
		if latest == nil {
			t.Fatalf("no track title sync_change")
		}
		// second add with same URL should be idempotent (reuse catalog, no error)
		rec2 := doLink(t, srv2, cookie2, http.MethodPost, "/api/v1/links/add", `{"url":"https://open.spotify.com/track/spDL1"}`)
		if rec2.Code != http.StatusOK {
			t.Fatalf("second add %d", rec2.Code)
		}
		var resp2 map[string]json.RawMessage
		_ = json.Unmarshal(rec2.Body.Bytes(), &resp2)
		var catalogID2 string
		_ = json.Unmarshal(resp2["catalogId"], &catalogID2)
		if catalogID2 != catalogID {
			t.Fatalf("idempotent catalogId %q vs %q", catalogID2, catalogID)
		}
		_ = srv2
	})

	t.Run("add with download false does not enqueue but still creates catalog", func(t *testing.T) {
		mgr3 := newFakeManager()
		srv3, st3, cookie3, fake3 := linkTestServer(t, mgr3)
		rec := doLink(t, srv3, cookie3, http.MethodPost, "/api/v1/links/add", `{"url":"https://open.spotify.com/track/spNoDL","download":false}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("status %d", rec.Code)
		}
		var resp map[string]json.RawMessage
		_ = json.Unmarshal(rec.Body.Bytes(), &resp)
		if _, ok := resp["job"]; ok {
			t.Fatalf("should not have job when download false")
		}
		if fake3.enqueueCalls != 0 {
			t.Fatalf("enqueueCalls %d want 0", fake3.enqueueCalls)
		}
		var catalogID string
		_ = json.Unmarshal(resp["catalogId"], &catalogID)
		if _, err := st3.Q().GetCatalogEntity(context.Background(), catalogID); err != nil {
			t.Fatalf("catalog not created despite download false: %v", err)
		}
	})

	t.Run("add with playlistId validates and enqueues with AddToPlaylistID and emits playlist sync", func(t *testing.T) {
		mgr4 := newFakeManager()
		srv4, st4, cookie4, fake4 := linkTestServer(t, mgr4)
		// create a synced playlist
		pl, err := st4.Q().UpsertSyncedPlaylist(context.Background(), db.UpsertSyncedPlaylistParams{
			ID:         "plTest1",
			Source:     "spotify",
			ExternalID: "extPl1",
			Name:       "Test Playlist",
			CoverUrl:   "",
			TracksJson: "[]",
			Mode:       "once",
			CreatedAt:  time.Now().Unix(),
		})
		if err != nil {
			t.Fatal(err)
		}
		_ = pl
		_ = st4.Q().SetSyncedPlaylistOwner(context.Background(), db.SetSyncedPlaylistOwnerParams{
			OwnerUserID: sql.NullString{String: "local", Valid: true},
			ID:          "plTest1",
		})
		rec := doLink(t, srv4, cookie4, http.MethodPost, "/api/v1/links/add", `{"url":"https://open.spotify.com/track/spPL1","playlistId":"plTest1"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("add with playlist %d: %s", rec.Code, rec.Body.String())
		}
		var resp map[string]json.RawMessage
		_ = json.Unmarshal(rec.Body.Bytes(), &resp)
		if _, ok := resp["playlistId"]; !ok {
			t.Fatalf("missing playlistId in resp")
		}
		if fake4.lastReq.AddToPlaylistID != "plTest1" {
			t.Fatalf("AddToPlaylistID %q want plTest1", fake4.lastReq.AddToPlaylistID)
		}
		// emits playlist sync_change
		ss := reverbsync.NewSyncStore(st4.Q())
		// Check any sync change for playlist plTest1
		count, _ := st4.Q().CountSyncChanges(context.Background())
		if count == 0 {
			t.Fatalf("no sync_change")
		}
		// Look for playlist entity
		found := false
		changes, err := ss.ListSince(context.Background(), 0, 100)
		if err != nil {
			t.Fatal(err)
		}
		for _, ch := range changes {
			if ch.EntityType == "playlist" && ch.EntityID == "plTest1" {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected playlist sync_change, got %+v", changes)
		}
	})

	t.Run("add validates url required", func(t *testing.T) {
		rec := doLink(t, srv, cookie, http.MethodPost, "/api/v1/links/add", `{"url":""}`)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("want 400, got %d", rec.Code)
		}
	})

	t.Run("add youtube sets ManualURL source-native", func(t *testing.T) {
		mgr5 := newFakeManager()
		srv5, _, cookie5, fake5 := linkTestServer(t, mgr5)
		rec := doLink(t, srv5, cookie5, http.MethodPost, "/api/v1/links/add", `{"url":"https://www.youtube.com/watch?v=ytManual1"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("yt add %d: %s", rec.Code, rec.Body.String())
		}
		if fake5.lastReq.Source != "youtube" {
			t.Fatalf("source %q", fake5.lastReq.Source)
		}
		if fake5.lastReq.ManualURL != "https://www.youtube.com/watch?v=ytManual1" {
			t.Fatalf("ManualURL %q", fake5.lastReq.ManualURL)
		}
		// ensure no bitrate transcoding field (there is none) — check we didn't set any unexpected
		// Source-native: adapter uses --audio youtube-music youtube, no bitrate. Our request should not contain ffmpeg.
		// Just assert Title not empty
		if fake5.lastReq.Title == "" {
			t.Fatalf("title empty")
		}
	})

	t.Run("add nonexistent playlist returns 404", func(t *testing.T) {
		mgr6 := newFakeManager()
		srv6, _, cookie6, _ := linkTestServer(t, mgr6)
		rec := doLink(t, srv6, cookie6, http.MethodPost, "/api/v1/links/add", `{"url":"https://open.spotify.com/track/spX","playlistId":"nonexistent"}`)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("want 404, got %d: %s", rec.Code, rec.Body.String())
		}
	})
}

// ensure imported symbols not unused
var _ core.DownloadJob
var _ = time.Now

// seedPlaylist creates an owned synced playlist the add-from-link handler will accept.
func seedPlaylist(t *testing.T, st *store.Store, id string) {
	t.Helper()
	if _, err := st.Q().UpsertSyncedPlaylist(context.Background(), db.UpsertSyncedPlaylistParams{
		ID:         id,
		Source:     "spotify",
		ExternalID: "ext-" + id,
		Name:       "Test Playlist",
		CoverUrl:   "",
		TracksJson: "[]",
		Mode:       "once",
		CreatedAt:  time.Now().Unix(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.Q().SetSyncedPlaylistOwner(context.Background(), db.SetSyncedPlaylistOwnerParams{
		OwnerUserID: sql.NullString{String: "local", Valid: true},
		ID:          id,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestLinkAddForwardsTimeRange(t *testing.T) {
	srv, _, cookie, fake := linkTestServer(t, nil)
	rec := doLink(t, srv, cookie, http.MethodPost, "/api/v1/links/add",
		`{"url":"https://www.youtube.com/watch?v=trim1","startTime":"1:30","endTime":"4:00"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if len(fake.allReqs) != 1 {
		t.Fatalf("enqueued %d requests, want 1", len(fake.allReqs))
	}
	if fake.allReqs[0].SectionStart != "1:30" || fake.allReqs[0].SectionEnd != "4:00" {
		t.Fatalf("range not forwarded: %+v", fake.allReqs[0])
	}
}

// Chapter splitting fans out into one download request per chapter, each
// trimmed to that chapter, so every chapter travels the normal one-track path.
func TestLinkAddSplitChaptersFansOut(t *testing.T) {
	mgr := newFakeManager()
	mgr.chapters = []core.Chapter{
		{Title: "Intro", StartSec: 0, EndSec: 30},
		{Title: "Verse", StartSec: 30, EndSec: 90},
		{Title: "Outro", StartSec: 90, EndSec: 150},
	}
	srv, _, cookie, fake := linkTestServer(t, mgr)

	rec := doLink(t, srv, cookie, http.MethodPost, "/api/v1/links/add",
		`{"url":"https://www.youtube.com/watch?v=chap1","splitChapters":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if len(fake.allReqs) != 3 {
		t.Fatalf("enqueued %d requests, want 3", len(fake.allReqs))
	}
	titles := []string{fake.allReqs[0].Title, fake.allReqs[1].Title, fake.allReqs[2].Title}
	for i, want := range []string{"Intro", "Verse", "Outro"} {
		if titles[i] != want {
			t.Fatalf("title[%d] = %q, want %q", i, titles[i], want)
		}
	}
	if fake.allReqs[1].SectionStart != "30" || fake.allReqs[1].SectionEnd != "90" {
		t.Fatalf("chapter bounds: %+v", fake.allReqs[1])
	}
	// All chapters share one album so the split lands as a coherent release.
	if fake.allReqs[0].Album == "" || fake.allReqs[0].Album != fake.allReqs[2].Album {
		t.Fatalf("chapters must share an album: %q vs %q", fake.allReqs[0].Album, fake.allReqs[2].Album)
	}
}

// Every chapter must carry the playlist target, so a split adds them all.
func TestLinkAddSplitChaptersAllJoinPlaylist(t *testing.T) {
	mgr := newFakeManager()
	mgr.chapters = []core.Chapter{
		{Title: "One", StartSec: 0, EndSec: 10},
		{Title: "Two", StartSec: 10, EndSec: 20},
	}
	srv, st, cookie, fake := linkTestServer(t, mgr)
	seedPlaylist(t, st, "pl-chap")

	rec := doLink(t, srv, cookie, http.MethodPost, "/api/v1/links/add",
		`{"url":"https://www.youtube.com/watch?v=chap2","splitChapters":true,"playlistId":"pl-chap"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if len(fake.allReqs) != 2 {
		t.Fatalf("enqueued %d requests, want 2", len(fake.allReqs))
	}
	for i, req := range fake.allReqs {
		if req.AddToPlaylistID != "pl-chap" {
			t.Fatalf("chapter %d missing playlist target: %+v", i, req)
		}
	}
}

func TestLinkAddRejectsRangeAndChaptersTogether(t *testing.T) {
	mgr := newFakeManager()
	mgr.chapters = []core.Chapter{{Title: "One", StartSec: 0, EndSec: 10}}
	srv, _, cookie, fake := linkTestServer(t, mgr)

	rec := doLink(t, srv, cookie, http.MethodPost, "/api/v1/links/add",
		`{"url":"https://www.youtube.com/watch?v=chap3","splitChapters":true,"startTime":"0:10"}`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422: %s", rec.Code, rec.Body.String())
	}
	if len(fake.allReqs) != 0 {
		t.Fatalf("nothing should be enqueued, got %d", len(fake.allReqs))
	}
}

func TestLinkAddSplitChaptersWithNoChapters(t *testing.T) {
	mgr := newFakeManager() // chapters left nil
	srv, _, cookie, fake := linkTestServer(t, mgr)

	rec := doLink(t, srv, cookie, http.MethodPost, "/api/v1/links/add",
		`{"url":"https://www.youtube.com/watch?v=chap4","splitChapters":true}`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422: %s", rec.Code, rec.Body.String())
	}
	if len(fake.allReqs) != 0 {
		t.Fatalf("nothing should be enqueued, got %d", len(fake.allReqs))
	}
}

// Trimming a Spotify link is meaningless — spotDL has no notion of a section.
func TestLinkAddRejectsRangeOnNonYouTube(t *testing.T) {
	srv, _, cookie, _ := linkTestServer(t, nil)
	rec := doLink(t, srv, cookie, http.MethodPost, "/api/v1/links/add",
		`{"url":"https://open.spotify.com/track/sp1","startTime":"0:10"}`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422: %s", rec.Code, rec.Body.String())
	}
}

func TestLinkChaptersEndpoint(t *testing.T) {
	mgr := newFakeManager()
	mgr.chapters = []core.Chapter{{Title: "Intro", StartSec: 0, EndSec: 30}}
	srv, _, cookie, _ := linkTestServer(t, mgr)

	rec := doLink(t, srv, cookie, http.MethodPost, "/api/v1/links/chapters",
		`{"url":"https://www.youtube.com/watch?v=chap5"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var got []core.Chapter
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Title != "Intro" {
		t.Fatalf("chapters: %+v", got)
	}
}

func TestLinkAddBatch(t *testing.T) {
	mgr := newFakeManager()
	srv, st, cookie, fake := linkTestServer(t, mgr)
	seedPlaylist(t, st, "pl-batch")
	// Batch of 3: two valid spotify, one unsupported (will be per-item error).
	body := `{"items":[
		{"url":"https://open.spotify.com/track/spB1","playlistId":"pl-batch"},
		{"url":"https://example.com/bad"},
		{"url":"https://open.spotify.com/track/spB2"}
	]}`
	rec := doLink(t, srv, cookie, http.MethodPost, "/api/v1/links/add-batch", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("batch status %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Results []struct {
			URL       string            `json:"url"`
			CatalogID string            `json:"catalogId"`
			Error     string            `json:"error"`
			Job       *core.DownloadJob `json:"job"`
		} `json:"results"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Results) != 3 {
		t.Fatalf("results len %d want 3", len(resp.Results))
	}
	if resp.Results[0].Error != "" || resp.Results[0].CatalogID == "" {
		t.Fatalf("first should succeed: %+v", resp.Results[0])
	}
	if resp.Results[1].Error == "" {
		t.Fatalf("second should be error for unsupported URL, got %+v", resp.Results[1])
	}
	if resp.Results[2].Error != "" {
		t.Fatalf("third should succeed: %+v", resp.Results[2])
	}
	if fake.enqueueCalls != 2 {
		t.Fatalf("enqueueCalls %d want 2 (two valid links)", fake.enqueueCalls)
	}
	// Verify playlist membership sync for first item (second and third: only first had playlistId)
	// At least one playlist sync_change should exist.
	count, _ := st.Q().CountSyncChanges(context.Background())
	if count == 0 {
		t.Fatalf("expected sync_change for batch")
	}
}

func TestLinkAddBatchChapterSplit(t *testing.T) {
	mgr := newFakeManager()
	mgr.chapters = []core.Chapter{{Title: "Intro", StartSec: 0, EndSec: 10}, {Title: "Verse", StartSec: 10, EndSec: 20}}
	srv, _, cookie, fake := linkTestServer(t, mgr)
	body := `{"items":[{"url":"https://www.youtube.com/watch?v=chapBatch","splitChapters":true}]}`
	rec := doLink(t, srv, cookie, http.MethodPost, "/api/v1/links/add-batch", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("batch status %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Results []struct {
			Jobs  []core.DownloadJob `json:"jobs"`
			Job   *core.DownloadJob  `json:"job"`
			Error string             `json:"error"`
		} `json:"results"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Results) != 1 || resp.Results[0].Error != "" {
		t.Fatalf("batch chapter split: %+v", resp.Results)
	}
	// Chapter split fans out to 2 jobs in the single result's Jobs array.
	if len(resp.Results[0].Jobs) != 2 {
		t.Fatalf("jobs len %d want 2", len(resp.Results[0].Jobs))
	}
	if fake.enqueueCalls != 2 {
		t.Fatalf("enqueueCalls %d want 2", fake.enqueueCalls)
	}
}
