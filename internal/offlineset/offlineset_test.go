package offlineset_test

import (
	"context"
	"testing"
	"time"

	"github.com/maxjb-xyz/reverb/internal/offlineset"
	"github.com/maxjb-xyz/reverb/internal/store"
	"github.com/maxjb-xyz/reverb/internal/store/db"
)

func newTestStoreOff(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(t.TempDir() + "/offline.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	return st
}

func createDeviceOff(t *testing.T, st *store.Store, id string, isServer int64) {
	t.Helper()
	if err := st.Q().CreateDevice(context.Background(), db.CreateDeviceParams{
		ID:        id,
		Name:      id,
		TokenHash: "hash_" + id,
		IsServer:  isServer,
	}); err != nil {
		t.Fatalf("create device %s: %v", id, err)
	}
}

func createPlaylistOff(t *testing.T, st *store.Store, id, name string) {
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
	// stamp last_synced_at
	_ = st.Q().UpdateSyncedPlaylistTracks(context.Background(), db.UpdateSyncedPlaylistTracksParams{
		Name:         name,
		CoverUrl:     "",
		TracksJson:   "[]",
		LastSyncedAt: time.Now().Unix(),
		ID:           id,
	})
}

func TestOfflineSetSetAndGet(t *testing.T) {
	st := newTestStoreOff(t)
	ctx := context.Background()
	q := st.Q()
	svc := offlineset.NewService(q)
	createDeviceOff(t, st, "dev1", 1)
	createPlaylistOff(t, st, "pl1", "Playlist One")

	if err := svc.Set(ctx, "dev1", "pl1", true); err != nil {
		t.Fatalf("Set: %v", err)
	}
	e, err := svc.Get(ctx, "dev1", "pl1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if e.DeviceID != "dev1" || e.PlaylistID != "pl1" || !e.Enabled {
		t.Fatalf("entry mismatch: %+v", e)
	}
	if e.UpdatedAt == 0 {
		t.Fatalf("updatedAt zero")
	}
	// tolerance: within 5s
	if delta := time.Now().UnixMilli() - e.UpdatedAt; delta < 0 || delta > 5000 {
		t.Fatalf("updatedAt delta %d out of range", delta)
	}
	// toggle disabled
	if err := svc.Set(ctx, "dev1", "pl1", false); err != nil {
		t.Fatalf("Set false: %v", err)
	}
	e2, err := svc.Get(ctx, "dev1", "pl1")
	if err != nil {
		t.Fatalf("Get after toggle: %v", err)
	}
	if e2.Enabled {
		t.Fatalf("expected disabled, got %+v", e2)
	}
}

func TestOfflineSetListForDevice(t *testing.T) {
	st := newTestStoreOff(t)
	ctx := context.Background()
	svc := offlineset.NewService(st.Q())
	createDeviceOff(t, st, "dev1", 1)
	createPlaylistOff(t, st, "plA", "A")
	createPlaylistOff(t, st, "plB", "B")
	createPlaylistOff(t, st, "plC", "C")

	if err := svc.Set(ctx, "dev1", "plB", true); err != nil {
		t.Fatal(err)
	}
	if err := svc.Set(ctx, "dev1", "plA", true); err != nil {
		t.Fatal(err)
	}
	// plC not added

	list, err := svc.ListForDevice(ctx, "dev1")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("list len %d want 2", len(list))
	}
	// ordered by playlist_id
	if list[0].PlaylistID != "plA" || list[1].PlaylistID != "plB" {
		t.Fatalf("order %v", list)
	}
	// empty device
	empty, err := svc.ListForDevice(ctx, "dev-unknown")
	if err != nil {
		t.Fatal(err)
	}
	if len(empty) != 0 {
		t.Fatalf("expected empty, got %d", len(empty))
	}
}

func TestOfflineSetRemove(t *testing.T) {
	st := newTestStoreOff(t)
	ctx := context.Background()
	svc := offlineset.NewService(st.Q())
	createDeviceOff(t, st, "dev1", 1)
	createPlaylistOff(t, st, "pl1", "P1")

	if err := svc.Set(ctx, "dev1", "pl1", true); err != nil {
		t.Fatal(err)
	}
	if err := svc.Remove(ctx, "dev1", "pl1"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	_, err := svc.Get(ctx, "dev1", "pl1")
	if err == nil {
		t.Fatalf("expected not found after remove")
	}
	// idempotent remove again
	if err := svc.Remove(ctx, "dev1", "pl1"); err != nil {
		t.Fatalf("second Remove: %v", err)
	}
	list, _ := svc.ListForDevice(ctx, "dev1")
	if len(list) != 0 {
		t.Fatalf("expected 0 after remove, got %d", len(list))
	}
}

func TestOfflineSetPlaylistNotFound(t *testing.T) {
	st := newTestStoreOff(t)
	ctx := context.Background()
	svc := offlineset.NewService(st.Q())
	createDeviceOff(t, st, "dev1", 1)

	err := svc.Set(ctx, "dev1", "nonexistent", true)
	if err == nil {
		t.Fatal("expected error for missing playlist")
	}
	// should be ErrPlaylistNotFound
	if err.Error() != offlineset.ErrPlaylistNotFound.Error() {
		t.Fatalf("expected ErrPlaylistNotFound, got %v", err)
	}
	// Get for missing entry should be ErrEntryNotFound
	_, err = svc.Get(ctx, "dev1", "nonexistent")
	if err == nil {
		t.Fatal("expected Get error")
	}
}

func TestOfflineSetFKCascade(t *testing.T) {
	st := newTestStoreOff(t)
	ctx := context.Background()
	q := st.Q()
	svc := offlineset.NewService(q)
	createDeviceOff(t, st, "dev1", 1)
	createPlaylistOff(t, st, "pl1", "P1")
	createPlaylistOff(t, st, "pl2", "P2")

	if err := svc.Set(ctx, "dev1", "pl1", true); err != nil {
		t.Fatal(err)
	}
	if err := svc.Set(ctx, "dev1", "pl2", true); err != nil {
		t.Fatal(err)
	}
	// delete pl1
	if err := q.DeleteSyncedPlaylist(ctx, "pl1"); err != nil {
		t.Fatalf("DeleteSyncedPlaylist: %v", err)
	}
	list, err := svc.ListForDevice(ctx, "dev1")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].PlaylistID != "pl2" {
		t.Fatalf("after cascade list %v", list)
	}
	_, err = svc.Get(ctx, "dev1", "pl1")
	if err == nil {
		t.Fatalf("expected not found after cascade")
	}
	// pl2 still present
	e, err := svc.Get(ctx, "dev1", "pl2")
	if err != nil || e == nil {
		t.Fatalf("pl2 should remain: %v %v", e, err)
	}
}

func TestOfflineSetNoSyncEmission(t *testing.T) {
	st := newTestStoreOff(t)
	ctx := context.Background()
	q := st.Q()
	svc := offlineset.NewService(q)
	createDeviceOff(t, st, "dev1", 1)
	createPlaylistOff(t, st, "pl1", "P1")
	createPlaylistOff(t, st, "pl2", "P2")

	before, err := q.CountSyncChanges(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if before != 0 {
		t.Fatalf("before %d want 0", before)
	}
	if err := svc.Set(ctx, "dev1", "pl1", true); err != nil {
		t.Fatal(err)
	}
	after, _ := q.CountSyncChanges(ctx)
	if after != before {
		t.Fatalf("Set emitted sync_change: before %d after %d", before, after)
	}
	if err := svc.Set(ctx, "dev1", "pl1", false); err != nil {
		t.Fatal(err)
	}
	after, _ = q.CountSyncChanges(ctx)
	if after != before {
		t.Fatalf("Set toggle emitted sync_change")
	}
	// List should not emit
	_, _ = svc.ListForDevice(ctx, "dev1")
	after, _ = q.CountSyncChanges(ctx)
	if after != before {
		t.Fatalf("List emitted sync_change")
	}
	// Get should not emit
	_, _ = svc.Get(ctx, "dev1", "pl1")
	after, _ = q.CountSyncChanges(ctx)
	if after != before {
		t.Fatalf("Get emitted sync_change")
	}
	if err := svc.Remove(ctx, "dev1", "pl1"); err != nil {
		t.Fatal(err)
	}
	after, _ = q.CountSyncChanges(ctx)
	if after != before {
		t.Fatalf("Remove emitted sync_change: before %d after %d", before, after)
	}
	// also test second playlist
	if err := svc.Set(ctx, "dev1", "pl2", true); err != nil {
		t.Fatal(err)
	}
	after, _ = q.CountSyncChanges(ctx)
	if after != before {
		t.Fatalf("second Set emitted sync_change")
	}
}
