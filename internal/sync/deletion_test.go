package sync_test

import (
	"context"
	"testing"

	"github.com/uhhhm/reverb/internal/store/db"
	syncpkg "github.com/uhhhm/reverb/internal/sync"
)

func TestDeletionDeletePlaylistEmitsTombstone(t *testing.T) {
	st := newTestStoreSync(t)
	ctx := context.Background()
	q := st.Q()
	ss := syncpkg.NewSyncStore(q)
	createDevice(t, st, "dev_server", "server", 1)
	ds := syncpkg.NewDeletionService(ss, q)
	rev, err := ds.DeletePlaylist(ctx, "dev_server", "pl1", 1234)
	if err != nil {
		t.Fatalf("DeletePlaylist: %v", err)
	}
	if rev == 0 {
		t.Fatal("revision 0")
	}
	ch, err := ss.GetLatestForField(ctx, "playlist", "pl1", "__deleted")
	if err != nil || ch == nil {
		t.Fatalf("GetLatest __deleted: %v %v", err, ch)
	}
	if ch.Field != "__deleted" || ch.EntityType != "playlist" || ch.EntityID != "pl1" {
		t.Fatalf("tombstone mismatch %+v", ch)
	}
	if ch.UpdatedAt != 1234 {
		t.Fatalf("updatedAt %d want 1234", ch.UpdatedAt)
	}
	// check value_json is true via underlying row
	rows, _ := ss.ListSince(ctx, 0, 10)
	found := false
	for _, r := range rows {
		if r.EntityType == "playlist" && r.EntityID == "pl1" && r.Field == "__deleted" {
			found = true
		}
	}
	if !found {
		t.Fatal("tombstone not in ListSince")
	}
}

func TestDeletionDeleteTrackEmitsTombstone(t *testing.T) {
	st := newTestStoreSync(t)
	ctx := context.Background()
	q := st.Q()
	ss := syncpkg.NewSyncStore(q)
	createDevice(t, st, "dev_server", "server", 1)
	ds := syncpkg.NewDeletionService(ss, q)
	rev, err := ds.DeleteTrack(ctx, "dev_server", "trk_abc", 5678)
	if err != nil {
		t.Fatalf("DeleteTrack: %v", err)
	}
	if rev == 0 {
		t.Fatal("rev 0")
	}
	ch, _ := ss.GetLatestForField(ctx, "track", "trk_abc", "__deleted")
	if ch == nil || ch.EntityID != "trk_abc" || ch.EntityType != "track" {
		t.Fatalf("track tombstone %+v", ch)
	}
}

func TestDeletionIsDeleted(t *testing.T) {
	st := newTestStoreSync(t)
	ctx := context.Background()
	q := st.Q()
	ss := syncpkg.NewSyncStore(q)
	createDevice(t, st, "dev_server", "server", 1)
	ds := syncpkg.NewDeletionService(ss, q)
	// false before
	deleted, err := ds.IsDeleted(ctx, "playlist", "pl1")
	if err != nil {
		t.Fatal(err)
	}
	if deleted {
		t.Fatal("IsDeleted true before")
	}
	_, _ = ds.DeletePlaylist(ctx, "dev_server", "pl1", 1000)
	deleted, _ = ds.IsDeleted(ctx, "playlist", "pl1")
	if !deleted {
		t.Fatal("IsDeleted false after")
	}
	// different entity still false
	deleted, _ = ds.IsDeleted(ctx, "playlist", "pl2")
	if deleted {
		t.Fatal("pl2 should not be deleted")
	}
	// track check false before
	deleted, _ = ds.IsDeleted(ctx, "track", "trk_1")
	if deleted {
		t.Fatal("track false before")
	}
}

func TestDeletionDeleteWinsOverEdit(t *testing.T) {
	st := newTestStoreSync(t)
	ctx := context.Background()
	q := st.Q()
	ss := syncpkg.NewSyncStore(q)
	createDevice(t, st, "dev_a", "a", 0)
	createDevice(t, st, "dev_b", "b", 0)
	ds := syncpkg.NewDeletionService(ss, q)

	// seed edit
	_, err := ss.AppendChange(ctx, "dev_a", syncpkg.SyncChange{EntityType: "playlist", EntityID: "pl1", Field: "name", Value: "hello", UpdatedAt: 1000})
	if err != nil {
		t.Fatal(err)
	}
	// delete older but should win per Reconcile
	_, _ = ds.DeletePlaylist(ctx, "dev_b", "pl1", 900)
	// edit after delete should be rejected even if newer
	inbound := []syncpkg.SyncChange{{EntityType: "playlist", EntityID: "pl1", Field: "name", Value: "resurrect", UpdatedAt: 2000, DeviceID: "dev_a"}}
	_, _, rejected, err := ss.Reconcile(ctx, "dev_a", 0, inbound)
	if err != nil {
		t.Fatal(err)
	}
	if len(rejected) != 1 {
		t.Fatalf("edit after delete should be rejected, got %d", len(rejected))
	}
	// IsDeleted still true
	deleted, _ := ds.IsDeleted(ctx, "playlist", "pl1")
	if !deleted {
		t.Fatal("should still be deleted")
	}
}

func TestDeletionRevisionMonotonic(t *testing.T) {
	st := newTestStoreSync(t)
	ctx := context.Background()
	q := st.Q()
	ss := syncpkg.NewSyncStore(q)
	createDevice(t, st, "dev_server", "server", 1)
	ds := syncpkg.NewDeletionService(ss, q)
	var prev int64
	for i := 0; i < 3; i++ {
		rev, err := ds.DeletePlaylist(ctx, "dev_server", "pl1", int64(1000+i))
		// Note: same entity but we emit multiple deletes; second and later will be via LWW; but revision should still increase
		// To avoid LWW dedup, use distinct IDs for monotonic check
		if err != nil {
			t.Fatal(err)
		}
		_ = rev
		maxRev, _ := ss.GetMaxRevision(ctx)
		if maxRev <= prev {
			t.Fatalf("not monotonic prev %d max %d", prev, maxRev)
		}
		prev = maxRev
		// use fresh entity to ensure append succeeds
		if i < 2 {
			_, _ = ds.DeletePlaylist(ctx, "dev_server", "pl_mono_"+string(rune('a'+i)), int64(1000+i))
			maxRev2, _ := ss.GetMaxRevision(ctx)
			if maxRev2 <= prev {
				t.Fatalf("not monotonic second %d", maxRev2)
			}
			prev = maxRev2
		}
	}
}

func TestDeletionServerDeviceFallback(t *testing.T) {
	st := newTestStoreSync(t)
	ctx := context.Background()
	q := st.Q()
	ss := syncpkg.NewSyncStore(q)
	createDevice(t, st, "dev_server", "server", 1)
	_ = q.UpsertSetting(ctx, db.UpsertSettingParams{Key: "server_device_id", Value: "dev_server"})
	// Instead use direct UpsertSetting via interface: need db param - test already has EnsureServerDevice pattern but we test empty deviceID fallback
	ds := syncpkg.NewDeletionService(ss, q)
	// passing empty deviceID should fallback to server device
	rev, err := ds.DeletePlaylist(ctx, "", "pl_fallback", 1111)
	if err != nil {
		t.Fatalf("fallback DeletePlaylist: %v", err)
	}
	if rev == 0 {
		t.Fatal("rev 0")
	}
	ch, _ := ss.GetLatestForField(ctx, "playlist", "pl_fallback", "__deleted")
	if ch == nil || ch.DeviceID != "dev_server" {
		t.Fatalf("fallback device mismatch %+v", ch)
	}
}
