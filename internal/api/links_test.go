package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/uhhhm/reverb/internal/catalog"
	"github.com/uhhhm/reverb/internal/core"
	"github.com/uhhhm/reverb/internal/linkadd"
	"github.com/uhhhm/reverb/internal/registry"
	"github.com/uhhhm/reverb/internal/store"
	"github.com/uhhhm/reverb/internal/store/db"
	reverbsync "github.com/uhhhm/reverb/internal/sync"
	"github.com/uhhhm/reverb/internal/syncemit"
)

// recordingPlaylists stands in for the playlist module: it records each edit
// add-from-link makes, in order.
type recordingPlaylists struct {
	mu   sync.Mutex
	adds []playlistAdd
}

type playlistAdd struct {
	id       string
	entries  []core.ExternalResult
	download bool
}

func (p *recordingPlaylists) AddTracks(_ context.Context, id string, entries []core.ExternalResult, download bool) (core.SyncedPlaylistDetail, int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.adds = append(p.adds, playlistAdd{id: id, entries: entries, download: download})
	return core.SyncedPlaylistDetail{}, len(entries), nil
}

func (p *recordingPlaylists) all() []playlistAdd {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]playlistAdd(nil), p.adds...)
}

func linkTestServer(t *testing.T, mgr DownloadManager) (*Server, *store.Store, *http.Cookie, *fakeManager) {
	srv, st, cookie, fake, _ := linkTestServerWith(t, mgr)
	return srv, st, cookie, fake
}

// linkTestServerWith is linkTestServer that also returns the playlist module
// the links join. Tracks are minted through a real catalog, which publishes
// them to the change log.
func linkTestServerWith(t *testing.T, mgr DownloadManager, opts ...linkadd.Option) (*Server, *store.Store, *http.Cookie, *fakeManager, *recordingPlaylists) {
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
	}
	catalogSvc := catalog.NewService(st.Q(), time.Now, uuid.NewString)
	catalogSvc.WithEmitter(syncemit.New(syncStore, catalogSvc, func(ctx context.Context) string {
		id, _ := reverbsync.ServerDeviceID(ctx, st.Q())
		return id
	}))
	playlists := &recordingPlaylists{}
	linkAddSvc := linkadd.New(st.Q(), mgr, append([]linkadd.Option{
		linkadd.WithCatalog(catalogSvc),
		linkadd.WithPlaylists(func() linkadd.Playlists { return playlists }),
	}, opts...)...)
	srv := NewServer(Deps{AllowedHosts: testAllowedHosts,
		Auth:          authSvc,
		Downloads:     mgr,
		Search:        registry.NewRegistry("search"),
		Downloader:    registry.NewRegistry("downloader"),
		SyncStore:     syncStore,
		OfflineSet:    st.Q(),
		PairingStore:  st.Q(),
		PlaylistOwner: st.Q(),
		LinkAdd:       linkAddSvc,
	})
	cookie := &http.Cookie{Name: sessionCookie, Value: tok}
	return srv, st, cookie, fake, playlists
}

// playlistChanges lists every change-log row add-from-link wrote about a
// playlist. The playlist module is the only writer of those.
func playlistChanges(t *testing.T, st *store.Store) []reverbsync.SyncChange {
	t.Helper()
	changes, err := reverbsync.NewSyncStore(st.Q()).ListSince(context.Background(), 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	var out []reverbsync.SyncChange
	for _, ch := range changes {
		if ch.EntityType == "playlist" {
			out = append(out, ch)
		}
	}
	return out
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
		latest, err := ss.GetLatestForField(context.Background(), reverbsync.EntityCatalog, catalogID, reverbsync.FieldIdentity)
		if err != nil {
			t.Fatal(err)
		}
		if latest == nil {
			t.Fatalf("the catalog did not publish the new track")
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

	t.Run("add with playlistId joins through the playlist module", func(t *testing.T) {
		srv4, st4, cookie4, fake4, playlists := linkTestServerWith(t, newFakeManager())
		seedPlaylist(t, st4, "plTest1")
		rec := doLink(t, srv4, cookie4, http.MethodPost, "/api/v1/links/add", `{"url":"https://open.spotify.com/track/spPL1","playlistId":"plTest1"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("add with playlist %d: %s", rec.Code, rec.Body.String())
		}
		var resp struct {
			PlaylistID string `json:"playlistId"`
			CatalogID  string `json:"catalogId"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &resp)
		if resp.PlaylistID != "plTest1" {
			t.Fatalf("playlistId = %q", resp.PlaylistID)
		}
		adds := playlists.all()
		if len(adds) != 1 || adds[0].id != "plTest1" || adds[0].download || len(adds[0].entries) != 1 {
			t.Fatalf("playlist edits = %+v, want one entry into plTest1 with no download of its own", adds)
		}
		if e := adds[0].entries[0]; e.Source != "spotify" || e.ExternalID != "spPL1" || e.CanonicalID != resp.CatalogID || e.Type != core.EntityTrack {
			t.Fatalf("entry = %+v", e)
		}
		// The library backend's playlists are another id space.
		if fake4.lastReq.AddToPlaylistID != "" {
			t.Fatalf("AddToPlaylistID = %q, want none", fake4.lastReq.AddToPlaylistID)
		}
		if ch := playlistChanges(t, st4); len(ch) != 0 {
			t.Fatalf("add-from-link wrote playlist changes itself: %+v", ch)
		}
	})

	t.Run("add to a mirrored playlist is refused before anything is written", func(t *testing.T) {
		srv7, st7, cookie7, fake7, playlists := linkTestServerWith(t, newFakeManager())
		seedPlaylist(t, st7, "plMirror")
		if _, err := st7.DB().Exec(`UPDATE synced_playlists SET mode = 'synced' WHERE id = 'plMirror'`); err != nil {
			t.Fatal(err)
		}
		rec := doLink(t, srv7, cookie7, http.MethodPost, "/api/v1/links/add", `{"url":"https://open.spotify.com/track/spMirror","playlistId":"plMirror"}`)
		if rec.Code != http.StatusConflict {
			t.Fatalf("status %d, want 409: %s", rec.Code, rec.Body.String())
		}
		if fake7.enqueueCalls != 0 || len(playlists.all()) != 0 {
			t.Fatalf("wrote after refusing: %d downloads, %+v", fake7.enqueueCalls, playlists.all())
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

// A chapter split joins a playlist as the video it came from, once: a
// chapter has no source id of its own that a paired device could stream.
func TestLinkAddSplitChaptersJoinsThePlaylistOnce(t *testing.T) {
	mgr := newFakeManager()
	mgr.chapters = []core.Chapter{
		{Title: "One", StartSec: 0, EndSec: 10},
		{Title: "Two", StartSec: 10, EndSec: 20},
	}
	srv, st, cookie, fake, playlists := linkTestServerWith(t, mgr)
	seedPlaylist(t, st, "pl-chap")

	rec := doLink(t, srv, cookie, http.MethodPost, "/api/v1/links/add",
		`{"url":"https://www.youtube.com/watch?v=chap2","splitChapters":true,"playlistId":"pl-chap"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if len(fake.allReqs) != 2 {
		t.Fatalf("enqueued %d requests, want 2", len(fake.allReqs))
	}
	adds := playlists.all()
	if len(adds) != 1 || len(adds[0].entries) != 1 || adds[0].entries[0].ExternalID != "chap2" {
		t.Fatalf("playlist edits = %+v, want the video once", adds)
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
}

// A batch into one playlist is one edit with the links in the batch's order,
// however the concurrent lookups finish; a failed link is left out.
func TestLinkAddBatchJoinsEachPlaylistInBatchOrder(t *testing.T) {
	srv, st, cookie, fake, playlists := linkTestServerWith(t, newFakeManager())
	seedPlaylist(t, st, "pl-a")
	seedPlaylist(t, st, "pl-b")
	var items []string
	var wantA []string
	for i := 0; i < 12; i++ {
		id := fmt.Sprintf("spOrder%02d", i)
		pl := "pl-a"
		if i%4 == 3 {
			pl = "pl-b"
		} else {
			wantA = append(wantA, id)
		}
		items = append(items, fmt.Sprintf(`{"url":"https://open.spotify.com/track/%s","playlistId":%q}`, id, pl))
	}
	items = append(items[:5], append([]string{`{"url":"https://example.com/bad","playlistId":"pl-a"}`}, items[5:]...)...)
	rec := doLink(t, srv, cookie, http.MethodPost, "/api/v1/links/add-batch", `{"items":[`+strings.Join(items, ",")+`]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("batch status %d: %s", rec.Code, rec.Body.String())
	}
	adds := playlists.all()
	if len(adds) != 2 || adds[0].id != "pl-a" || adds[1].id != "pl-b" {
		t.Fatalf("playlist edits = %+v, want one per playlist", adds)
	}
	var gotA []string
	for _, e := range adds[0].entries {
		gotA = append(gotA, e.ExternalID)
	}
	if strings.Join(gotA, ",") != strings.Join(wantA, ",") {
		t.Fatalf("pl-a got %v, want %v", gotA, wantA)
	}
	if len(adds[1].entries) != 3 {
		t.Fatalf("pl-b got %+v", adds[1].entries)
	}
	if fake.enqueueCalls != 12 {
		t.Fatalf("enqueueCalls %d want 12", fake.enqueueCalls)
	}
	if ch := playlistChanges(t, st); len(ch) != 0 {
		t.Fatalf("add-from-link wrote playlist changes itself: %+v", ch)
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

// Links in a batch are planned concurrently. The same track named twice must
// still be minted once: two entities for one track split its history, and
// the second is published to peers with no alias to find it by.
func TestLinkAddBatchMintsARepeatedTrackOnce(t *testing.T) {
	srv, st, cookie, _, _ := linkTestServerWith(t, newFakeManager())
	var items []string
	for i := 0; i < 20; i++ {
		items = append(items, `{"url":"https://open.spotify.com/track/spTwice","download":false}`)
	}
	rec := doLink(t, srv, cookie, http.MethodPost, "/api/v1/links/add-batch", `{"items":[`+strings.Join(items, ",")+`]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("batch status %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Results []struct {
			CatalogID string `json:"catalogId"`
			Error     string `json:"error"`
		} `json:"results"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, r := range resp.Results {
		if r.Error != "" {
			t.Fatalf("item failed: %s", r.Error)
		}
		ids[r.CatalogID] = true
	}
	var entities int
	if err := st.DB().QueryRow(`SELECT count(*) FROM catalog_entity WHERE source = 'spotify' AND external_id = 'spTwice'`).Scan(&entities); err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || entities != 1 {
		t.Fatalf("catalog ids %v, %d entities, want one", ids, entities)
	}
}
