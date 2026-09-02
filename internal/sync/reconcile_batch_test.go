package sync_test

import (
	"context"
	"fmt"
	"testing"

	syncpkg "github.com/uhhhm/reverb/internal/sync"
)

// A device that authored more changes than one Reconcile call accepts must
// still replicate: the responder pages outbound above the per-call cap, and a
// refused batch would leave the peer's vector unmoved and repeat forever.
func TestReconcileBatchedAppliesMoreThanOneBatch(t *testing.T) {
	st := newTestStoreSync(t)
	ctx := context.Background()
	ss := syncpkg.NewSyncStore(st.Q())
	createDevice(t, st, "dev_local", "local", 1)
	createDevice(t, st, "dev_peer", "peer", 0)

	total := syncpkg.MaxReconcileBatch + 10
	inbound := make([]syncpkg.SyncChange, 0, total)
	for i := 0; i < total; i++ {
		inbound = append(inbound, syncpkg.SyncChange{
			EntityType: "track",
			EntityID:   fmt.Sprintf("cat_%d", i),
			Field:      "title",
			Value:      "Song",
			UpdatedAt:  int64(1000 + i),
			DeviceID:   "dev_peer",
		})
	}

	if _, _, _, err := ss.Reconcile(ctx, "dev_peer", 0, inbound); err == nil {
		t.Fatal("Reconcile accepted an oversized batch; the cap it guards is gone")
	}

	_, _, rejected, err := ss.ReconcileBatched(ctx, "dev_peer", 0, inbound)
	if err != nil {
		t.Fatalf("ReconcileBatched: %v", err)
	}
	if len(rejected) != 0 {
		t.Fatalf("rejected %d change(s), want 0", len(rejected))
	}
	stored, err := ss.ListSince(ctx, 0, int64(total+100))
	if err != nil {
		t.Fatalf("ListSince: %v", err)
	}
	if len(stored) != total {
		t.Fatalf("stored %d change(s), want %d", len(stored), total)
	}
}

func TestReconcileBatchedWithNoChangesStillReturnsOutbound(t *testing.T) {
	st := newTestStoreSync(t)
	ctx := context.Background()
	ss := syncpkg.NewSyncStore(st.Q())
	createDevice(t, st, "dev_local", "local", 1)
	createDevice(t, st, "dev_peer", "peer", 0)

	if _, err := ss.AppendChange(ctx, "dev_local", syncpkg.SyncChange{
		EntityType: "track", EntityID: "cat_1", Field: "title", Value: "Song", UpdatedAt: 1000,
	}); err != nil {
		t.Fatalf("AppendChange: %v", err)
	}
	outbound, newRev, _, err := ss.ReconcileBatched(ctx, "dev_peer", 0, nil)
	if err != nil {
		t.Fatalf("ReconcileBatched: %v", err)
	}
	if len(outbound) != 1 {
		t.Fatalf("outbound %d, want 1", len(outbound))
	}
	if newRev == 0 {
		t.Fatal("newRev 0, want the local revision")
	}
}

// NoOutbound exists so the p2p syncer, which pulls separately, does not pay
// for a full change-log read it discards.
func TestReconcileNoOutboundSkipsOutbound(t *testing.T) {
	st := newTestStoreSync(t)
	ctx := context.Background()
	ss := syncpkg.NewSyncStore(st.Q())
	createDevice(t, st, "dev_local", "local", 1)
	createDevice(t, st, "dev_peer", "peer", 0)

	inbound := []syncpkg.SyncChange{{
		EntityType: "track",
		EntityID:   "cat_1",
		Field:      "title",
		Value:      "Song",
		UpdatedAt:  1000,
		DeviceID:   "dev_peer",
	}}
	if _, _, _, err := ss.ReconcileBatched(ctx, "dev_peer", 0, inbound); err != nil {
		t.Fatalf("seed: %v", err)
	}

	out, _, _, err := ss.Reconcile(ctx, "dev_peer", 0, nil)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(out) == 0 {
		t.Fatal("expected outbound with sinceRev=0, got none")
	}

	out, rev, _, err := ss.Reconcile(ctx, "dev_peer", syncpkg.NoOutbound, nil)
	if err != nil {
		t.Fatalf("Reconcile NoOutbound: %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("NoOutbound returned %d outbound change(s), want 0", len(out))
	}
	if rev == 0 {
		t.Fatal("NoOutbound should still report the revision")
	}
}
