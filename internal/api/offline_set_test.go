package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/uhhhm/reverb/internal/auth"
	"github.com/uhhhm/reverb/internal/registry"
	"github.com/uhhhm/reverb/internal/store"
	"github.com/uhhhm/reverb/internal/store/db"
)

func newOfflineServer(t *testing.T) (*Server, *store.Store) {
	t.Helper()
	st, err := store.Open(t.TempDir() + "/offline_api.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	authSvc := auth.NewService(st.Q(), time.Now)
	if err := authSvc.EnsureSeed(context.Background()); err != nil {
		t.Fatal(err)
	}
	// create server device
	if err := st.Q().CreateDevice(context.Background(), db.CreateDeviceParams{
		ID:        "dev_server",
		Name:      "server",
		TokenHash: "hash_server",
		IsServer:  1,
	}); err != nil {
		t.Fatal(err)
	}
	_ = st.Q().UpsertSetting(context.Background(), db.UpsertSettingParams{Key: "server_device_id", Value: "dev_server"})

	srv := NewServer(Deps{AllowedHosts: testAllowedHosts,
		Auth:       authSvc,
		Search:     registry.NewRegistry("search"),
		Downloader: registry.NewRegistry("downloader"),
		OfflineSet: st.Q(),
	})
	return srv, st
}

func createPlaylistAPI(t *testing.T, st *store.Store, id, name string) {
	t.Helper()
	_, err := st.Q().UpsertSyncedPlaylist(context.Background(), db.UpsertSyncedPlaylistParams{
		ID:         id,
		Source:     "spotify",
		ExternalID: "ext-" + id,
		Name:       name,
		CoverUrl:   "",
		TracksJson: "[]",
		Mode:       "once",
		CreatedAt:  time.Now().Unix(),
	})
	if err != nil {
		t.Fatalf("create playlist %s: %v", id, err)
	}
	_ = st.Q().UpdateSyncedPlaylistTracks(context.Background(), db.UpdateSyncedPlaylistTracksParams{
		Name:         name,
		CoverUrl:     "",
		TracksJson:   "[]",
		LastSyncedAt: time.Now().Unix(),
		ID:           id,
	})
}

func doPUT(t *testing.T, srv *Server, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, path, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func doDELETEOffline(t *testing.T, srv *Server, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, path, nil)
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func TestOfflineSetGetEmpty(t *testing.T) {
	srv, _ := newOfflineServer(t)
	rec := doGET(t, srv, "/api/v1/offline-set", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET empty = %d: %s", rec.Code, rec.Body.String())
	}
	var list []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Fatalf("expected empty list, got %d", len(list))
	}
}

func TestOfflineSetPutAndList(t *testing.T) {
	srv, st := newOfflineServer(t)
	createPlaylistAPI(t, st, "pl1", "My Playlist")
	createPlaylistAPI(t, st, "pl2", "Second")

	// PUT pl1 enabled true
	rec := doPUT(t, srv, "/api/v1/offline-set/pl1", `{"enabled":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT pl1 = %d: %s", rec.Code, rec.Body.String())
	}
	var putResp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &putResp); err != nil {
		t.Fatal(err)
	}
	if putResp["playlistId"] != "pl1" || putResp["enabled"] != true {
		t.Fatalf("putResp %v", putResp)
	}
	if putResp["updatedAt"] == nil {
		t.Fatalf("updatedAt missing")
	}

	// PUT pl2 enabled false
	rec = doPUT(t, srv, "/api/v1/offline-set/pl2", `{"enabled":false}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT pl2 = %d: %s", rec.Code, rec.Body.String())
	}

	// GET list
	rec = doGET(t, srv, "/api/v1/offline-set", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET = %d: %s", rec.Code, rec.Body.String())
	}
	var list []offlineSetListItem
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("list %d want 2", len(list))
	}
	// ordered by playlist_id: pl1, pl2
	if list[0].PlaylistID != "pl1" || list[0].PlaylistName != "My Playlist" || !list[0].Enabled {
		t.Fatalf("list[0] %+v", list[0])
	}
	if list[1].PlaylistID != "pl2" || list[1].PlaylistName != "Second" || list[1].Enabled {
		t.Fatalf("list[1] %+v", list[1])
	}
}

func TestOfflineSetPutNotFound(t *testing.T) {
	srv, st := newOfflineServer(t)
	before, _ := st.Q().CountSyncChanges(context.Background())
	rec := doPUT(t, srv, "/api/v1/offline-set/missing", `{"enabled":true}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("PUT missing = %d want 404: %s", rec.Code, rec.Body.String())
	}
	after, _ := st.Q().CountSyncChanges(context.Background())
	if after != before {
		t.Fatalf("sync_change emitted on 404: before %d after %d", before, after)
	}
}

func TestOfflineSetPutBadBody(t *testing.T) {
	srv, _ := newOfflineServer(t)
	rec := doPUT(t, srv, "/api/v1/offline-set/pl1", `{}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad body = %d want 400: %s", rec.Code, rec.Body.String())
	}
	rec = doPUT(t, srv, "/api/v1/offline-set/pl1", `{"enabled": "yes"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid type = %d want 400: %s", rec.Code, rec.Body.String())
	}
}

func TestOfflineSetDelete(t *testing.T) {
	srv, st := newOfflineServer(t)
	createPlaylistAPI(t, st, "pl1", "P1")
	rec := doPUT(t, srv, "/api/v1/offline-set/pl1", `{"enabled":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT = %d", rec.Code)
	}
	// DELETE
	rec = doDELETEOffline(t, srv, "/api/v1/offline-set/pl1")
	if rec.Code != http.StatusOK {
		t.Fatalf("DELETE = %d: %s", rec.Code, rec.Body.String())
	}
	var del map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &del); err != nil {
		t.Fatal(err)
	}
	if del["ok"] != true {
		t.Fatalf("delete ok %v", del)
	}
	// list empty
	rec = doGET(t, srv, "/api/v1/offline-set", "")
	var list []offlineSetListItem
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	if len(list) != 0 {
		t.Fatalf("after delete list %d want 0", len(list))
	}
}

func TestOfflineSetDeleteNotFoundPlaylist(t *testing.T) {
	srv, _ := newOfflineServer(t)
	rec := doDELETEOffline(t, srv, "/api/v1/offline-set/nonexistent")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("DELETE nonexistent playlist = %d want 404: %s", rec.Code, rec.Body.String())
	}
}

func TestOfflineSetNoSyncEmission(t *testing.T) {
	srv, st := newOfflineServer(t)
	createPlaylistAPI(t, st, "pl1", "P1")
	createPlaylistAPI(t, st, "pl2", "P2")
	ctx := context.Background()
	before, _ := st.Q().CountSyncChanges(ctx)

	rec := doPUT(t, srv, "/api/v1/offline-set/pl1", `{"enabled":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT %d", rec.Code)
	}
	after, _ := st.Q().CountSyncChanges(ctx)
	if after != before {
		t.Fatalf("PUT emitted sync_change %d -> %d", before, after)
	}
	rec = doGET(t, srv, "/api/v1/offline-set", "")
	if rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	after, _ = st.Q().CountSyncChanges(ctx)
	if after != before {
		t.Fatalf("GET emitted sync_change")
	}
	rec = doPUT(t, srv, "/api/v1/offline-set/pl1", `{"enabled":false}`)
	if rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	after, _ = st.Q().CountSyncChanges(ctx)
	if after != before {
		t.Fatalf("PUT toggle emitted sync_change")
	}
	rec = doDELETEOffline(t, srv, "/api/v1/offline-set/pl1")
	if rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	after, _ = st.Q().CountSyncChanges(ctx)
	if after != before {
		t.Fatalf("DELETE emitted sync_change")
	}
	// also ensure second put still local
	rec = doPUT(t, srv, "/api/v1/offline-set/pl2", `{"enabled":true}`)
	if rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	after, _ = st.Q().CountSyncChanges(ctx)
	if after != before {
		t.Fatalf("second PUT emitted sync_change")
	}
}

func TestOfflineSetDeletePlaylistCascades(t *testing.T) {
	srv, st := newOfflineServer(t)
	createPlaylistAPI(t, st, "pl1", "P1")
	createPlaylistAPI(t, st, "pl2", "P2")
	_ = doPUT(t, srv, "/api/v1/offline-set/pl1", `{"enabled":true}`)
	_ = doPUT(t, srv, "/api/v1/offline-set/pl2", `{"enabled":true}`)

	// delete pl1 directly via store (simulates canonical deletion)
	if err := st.Q().DeleteSyncedPlaylist(context.Background(), "pl1"); err != nil {
		t.Fatal(err)
	}
	rec := doGET(t, srv, "/api/v1/offline-set", "")
	var list []offlineSetListItem
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].PlaylistID != "pl2" {
		t.Fatalf("after cascade list %v", list)
	}
	// pl1 should be gone, PUT should now 404
	rec = doPUT(t, srv, "/api/v1/offline-set/pl1", `{"enabled":true}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("PUT after playlist deleted = %d want 404", rec.Code)
	}
}

func TestOfflineSetDeviceFallback(t *testing.T) {
	// No server_device_id setting, but device with is_server=1 exists
	st, err := store.Open(t.TempDir() + "/fallback.db")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	authSvc := auth.NewService(st.Q(), time.Now)
	_ = authSvc.EnsureSeed(context.Background())
	_ = st.Q().CreateDevice(context.Background(), db.CreateDeviceParams{
		ID:        "dev_fallback",
		Name:      "fallback",
		TokenHash: "hash_fallback",
		IsServer:  1,
	})
	// intentionally NOT setting server_device_id
	createPlaylistAPI(t, st, "pl1", "Fallback Playlist")

	srv := NewServer(Deps{AllowedHosts: testAllowedHosts,
		Auth:       authSvc,
		Search:     registry.NewRegistry("search"),
		Downloader: registry.NewRegistry("downloader"),
		OfflineSet: st.Q(),
	})

	rec := doPUT(t, srv, "/api/v1/offline-set/pl1", `{"enabled":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("fallback PUT = %d: %s", rec.Code, rec.Body.String())
	}
	rec = doGET(t, srv, "/api/v1/offline-set", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("fallback GET = %d: %s", rec.Code, rec.Body.String())
	}
	var list []offlineSetListItem
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	if len(list) != 1 || list[0].PlaylistID != "pl1" {
		t.Fatalf("fallback list %v", list)
	}
}

func TestOfflineSetRemoveLocalOnly(t *testing.T) {
	srv, st := newOfflineServer(t)
	createPlaylistAPI(t, st, "pl1", "P1")
	ctx := context.Background()
	before, _ := st.Q().CountSyncChanges(ctx)
	_ = doPUT(t, srv, "/api/v1/offline-set/pl1", `{"enabled":true}`)
	_ = doDELETEOffline(t, srv, "/api/v1/offline-set/pl1")
	after, _ := st.Q().CountSyncChanges(ctx)
	if after != before {
		t.Fatalf("Remove local-only violated: before %d after %d", before, after)
	}
	// playlist still exists — removing from offline set is local-only and must not propagate via sync
	_, err := st.Q().GetSyncedPlaylist(ctx, "pl1")
	if err != nil {
		t.Fatalf("playlist should still exist after offline remove: %v", err)
	}
}
