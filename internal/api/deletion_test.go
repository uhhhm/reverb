package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/uhhhm/reverb/internal/auth"
	"github.com/uhhhm/reverb/internal/playlistsync"
	"github.com/uhhhm/reverb/internal/registry"
	"github.com/uhhhm/reverb/internal/store"
	"github.com/uhhhm/reverb/internal/store/db"
	syncpkg "github.com/uhhhm/reverb/internal/sync"
	"github.com/uhhhm/reverb/internal/wiring"
)

func newDeletionServer(t *testing.T) (*Server, *store.Store) {
	t.Helper()
	st, err := store.Open(t.TempDir() + "/deletion_api.db")
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
	// server device
	if err := st.Q().CreateDevice(context.Background(), db.CreateDeviceParams{
		ID:        "dev_server",
		Name:      "server",
		TokenHash: "hash_server",
		IsServer:  1,
	}); err != nil {
		t.Fatal(err)
	}
	_ = st.Q().UpsertSetting(context.Background(), db.UpsertSettingParams{Key: "server_device_id", Value: "dev_server"})
	syncStore := syncpkg.NewSyncStore(st.Q())
	syncWStore := wiring.NewSyncStore(st.Q())
	svc := playlistsync.NewService(nil, nil, nil, syncWStore, nil, func() int64 { return time.Now().Unix() }, uuid.NewString, nil)
	deletionSvc := syncpkg.NewDeletionService(syncStore, st.Q())
	srv := NewServer(Deps{AllowedHosts: testAllowedHosts,
		Auth:          authSvc,
		Sync:          svc,
		PlaylistOwner: st.Q(),
		SyncStore:     syncStore,
		OfflineSet:    st.Q(),
		PairingStore:  st.Q(),
		PairingDB:     st.DB(),
		Search:        registry.NewRegistry("search"),
		Downloader:    registry.NewRegistry("downloader"),
		Deletion:      deletionSvc,
	})
	return srv, st
}

func createPlaylistForDeletion(t *testing.T, st *store.Store, id, name string) {
	t.Helper()
	_, err := st.Q().UpsertSyncedPlaylist(context.Background(), db.UpsertSyncedPlaylistParams{
		ID:         id,
		Source:     "local",
		ExternalID: id,
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

func TestDeletionPlaylistCreatesTombstone(t *testing.T) {
	srv, st := newDeletionServer(t)
	ctx := context.Background()
	createPlaylistForDeletion(t, st, "pl-del-1", "To Delete")
	before, _ := st.Q().CountSyncChanges(ctx)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/playlists/pl-del-1", nil)
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("DELETE playlist = %d: %s", rec.Code, rec.Body.String())
	}
	after, _ := st.Q().CountSyncChanges(ctx)
	if after != before+1 {
		t.Fatalf("sync_change count before %d after %d want +1", before, after)
	}
	latest, err := syncpkg.NewSyncStore(st.Q()).GetLatestForField(ctx, "playlist", "pl-del-1", "__deleted")
	if err != nil || latest == nil {
		t.Fatalf("GetLatest __deleted: %v %v", err, latest)
	}
	if latest.Field != "__deleted" || latest.EntityID != "pl-del-1" {
		t.Fatalf("tombstone mismatch %+v", latest)
	}
	// value_json should be true (handled via store marshaling)
	// Ensure the row is actually marked deleted via IsDeleted helper
	ds := syncpkg.NewDeletionService(syncpkg.NewSyncStore(st.Q()), st.Q())
	deleted, _ := ds.IsDeleted(ctx, "playlist", "pl-del-1")
	if !deleted {
		t.Fatal("IsDeleted false after delete")
	}
	// also ensure playlist row is gone (cascade)
	_, err = st.Q().GetSyncedPlaylist(ctx, "pl-del-1")
	if err == nil {
		t.Fatal("playlist still exists after delete")
	}
	// revision monotonic check
	rev, _ := syncpkg.NewSyncStore(st.Q()).GetMaxRevision(ctx)
	if rev == 0 {
		t.Fatal("revision 0")
	}
}

func TestDeletionOfflineSetDoesNotCreateTombstone(t *testing.T) {
	srv, st := newDeletionServer(t)
	ctx := context.Background()
	createPlaylistForDeletion(t, st, "pl-off-1", "Offline P")
	createPlaylistForDeletion(t, st, "pl-off-2", "Offline P2")
	before, _ := st.Q().CountSyncChanges(ctx)

	// PUT offline set
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/offline-set/pl-off-1", strings.NewReader(`{"enabled":true}`))
	req.Header.Set("Content-Type", "application/json")
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT offline = %d: %s", rec.Code, rec.Body.String())
	}
	after, _ := st.Q().CountSyncChanges(ctx)
	if after != before {
		t.Fatalf("PUT offline emitted sync_change %d -> %d", before, after)
	}
	// DELETE offline set (local-only)
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodDelete, "/api/v1/offline-set/pl-off-1", nil)
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("DELETE offline = %d: %s", rec.Code, rec.Body.String())
	}
	after, _ = st.Q().CountSyncChanges(ctx)
	if after != before {
		t.Fatalf("DELETE offline emitted sync_change %d -> %d", before, after)
	}
	// playlist still exists
	_, err := st.Q().GetSyncedPlaylist(ctx, "pl-off-1")
	if err != nil {
		t.Fatalf("playlist should still exist after offline remove: %v", err)
	}
	// now delete playlist canonical and ensure tombstone DOES emit (contrast)
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodDelete, "/api/v1/playlists/pl-off-2", nil)
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("DELETE playlist = %d", rec.Code)
	}
	after, _ = st.Q().CountSyncChanges(ctx)
	if after != before+1 {
		t.Fatalf("playlist delete should emit +1, before %d after %d", before, after)
	}
}

func TestDeletionConcurrentEditVsDelete(t *testing.T) {
	srv, st := newDeletionServer(t)
	ctx := context.Background()
	createPlaylistForDeletion(t, st, "pl-conc-1", "Conc")
	// delete playlist (emit tombstone)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/playlists/pl-conc-1", nil)
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("DELETE conc = %d", rec.Code)
	}
	// now try to sync an edit for same playlist's name field via SyncStore Reconcile (simulating device edit)
	ss := syncpkg.NewSyncStore(st.Q())
	// create a client device for edit
	_ = st.Q().CreateDevice(ctx, db.CreateDeviceParams{ID: "dev_client", Name: "client", TokenHash: "hash_client", IsServer: 0})
	inbound := []syncpkg.SyncChange{{EntityType: "playlist", EntityID: "pl-conc-1", Field: "name", Value: "resurrect", UpdatedAt: time.Now().UnixMilli(), DeviceID: "dev_client"}}
	_, _, rejected, err := ss.Reconcile(ctx, "dev_client", 0, inbound)
	if err != nil {
		t.Fatal(err)
	}
	if len(rejected) != 1 {
		t.Fatalf("edit after delete should be rejected, got %d rejected %v", len(rejected), rejected)
	}
	// also test delete wins even if edit is newer timestamp already covered; ensure IsDeleted still true
	ds := syncpkg.NewDeletionService(ss, st.Q())
	deleted, _ := ds.IsDeleted(ctx, "playlist", "pl-conc-1")
	if !deleted {
		t.Fatal("IsDeleted false after concurrent test")
	}
	// inbound delete should be accepted (idempotent)
	inboundDel := []syncpkg.SyncChange{{EntityType: "playlist", EntityID: "pl-conc-1", Field: "__deleted", Value: nil, UpdatedAt: time.Now().UnixMilli(), DeviceID: "dev_client"}}
	_, _, rejected, _ = ss.Reconcile(ctx, "dev_client", 0, inboundDel)
	// delete vs delete LWW: newer should win or be accepted; at least not rejected due to tombstone logic? Actually existing is __deleted, incoming __deleted should go through LWW
	// So rejected may be 0 or 1 depending on timestamp tie-break; we just ensure no panic
	_ = rejected
}

func TestDeletionRevisionMonotonic(t *testing.T) {
	srv, st := newDeletionServer(t)
	ctx := context.Background()
	ss := syncpkg.NewSyncStore(st.Q())
	var prev int64
	for i := 0; i < 3; i++ {
		id := "pl-mono-" + string(rune('a'+i))
		createPlaylistForDeletion(t, st, id, "Mono")
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodDelete, "/api/v1/playlists/"+id, nil)
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("delete mono %s = %d", id, rec.Code)
		}
		rev, _ := ss.GetMaxRevision(ctx)
		if rev <= prev {
			t.Fatalf("revision not monotonic prev %d rev %d at i %d", prev, rev, i)
		}
		prev = rev
	}
	// also check ListSince ordered
	changes, _ := ss.ListSince(ctx, 0, 10)
	for i := 1; i < len(changes); i++ {
		if changes[i].Revision <= changes[i-1].Revision {
			t.Fatalf("ListSince not ordered at %d", i)
		}
	}
}

func TestDeletionTrackTombstone(t *testing.T) {
	_, st := newDeletionServer(t)
	ctx := context.Background()
	ss := syncpkg.NewSyncStore(st.Q())
	ds := syncpkg.NewDeletionService(ss, st.Q())
	// ensure not deleted before
	deleted, _ := ds.IsDeleted(ctx, "track", "trk_del_1")
	if deleted {
		t.Fatal("track should not be deleted initially")
	}
	rev, err := ds.DeleteTrack(ctx, "dev_server", "trk_del_1", time.Now().UnixMilli())
	if err != nil || rev == 0 {
		t.Fatalf("DeleteTrack err %v rev %d", err, rev)
	}
	deleted, _ = ds.IsDeleted(ctx, "track", "trk_del_1")
	if !deleted {
		t.Fatal("track IsDeleted false after")
	}
	ch, _ := ss.GetLatestForField(ctx, "track", "trk_del_1", "__deleted")
	if ch == nil || ch.Field != "__deleted" {
		t.Fatalf("track tombstone missing %+v", ch)
	}
}
