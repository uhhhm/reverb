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

// The vector says "everything below this seq has been received", so it may only
// cross a seq that has actually been settled. Batches are applied out of seq
// order -- catalog entities are hoisted, oversized batches are sliced, and the
// p2p syncer reconciles catalog entities in their own phase -- and a vector
// that jumped to a hoisted entity's seq would filter every lower seq out of the
// peer's next round, losing changes a failed later phase never applied.
func TestVectorOnlyAdvancesOverSettledSeqs(t *testing.T) {
	st := newTestStoreSync(t)
	ctx := context.Background()
	ss := syncpkg.NewSyncStore(st.Q())
	createDevice(t, st, "dev_local", "local", 1)
	createDevice(t, st, "dev_peer", "peer", 0)

	change := func(seq int64, entityType, id string) syncpkg.SyncChange {
		return syncpkg.SyncChange{
			EntityType: entityType,
			EntityID:   id,
			Field:      "title",
			Value:      "Song",
			UpdatedAt:  1000 + seq,
			HLC:        1000 + seq,
			Seq:        seq,
			DeviceID:   "dev_peer",
		}
	}
	// Phase one of a round: the peer's catalog entity, seq 5 of 1..5.
	catalog := []syncpkg.SyncChange{change(5, syncpkg.EntityCatalog, "cat_5")}
	if _, _, _, err := ss.ReconcileBatched(ctx, "dev_peer", syncpkg.NoOutbound, catalog); err != nil {
		t.Fatalf("catalog phase: %v", err)
	}
	seq, _, err := ss.GetVector(ctx, "dev_peer")
	if err != nil {
		t.Fatalf("GetVector: %v", err)
	}
	if seq != 0 {
		t.Fatalf("vector at %d after only seq 5 landed; seqs 1-4 would never be resent", seq)
	}
	still, err := ss.ListSinceVector(ctx, map[string]int64{"dev_peer": seq}, 100)
	if err != nil {
		t.Fatalf("ListSinceVector: %v", err)
	}
	if len(still) == 0 {
		t.Fatal("peer would resend nothing, so the missing changes are lost")
	}

	// Phase two lands the rest. The vector may now cross seq 5, which the log
	// already holds, and reach the end of the run.
	rest := []syncpkg.SyncChange{
		change(1, "track", "cat_1"),
		change(2, "track", "cat_2"),
		change(3, "track", "cat_3"),
		change(4, "track", "cat_4"),
	}
	if _, _, _, err := ss.ReconcileBatched(ctx, "dev_peer", syncpkg.NoOutbound, rest); err != nil {
		t.Fatalf("second phase: %v", err)
	}
	seq, _, err = ss.GetVector(ctx, "dev_peer")
	if err != nil {
		t.Fatalf("GetVector: %v", err)
	}
	if seq != 5 {
		t.Fatalf("vector at %d after the whole run landed, want 5", seq)
	}
}
