package sync_test

import (
	"context"
	"testing"
	"time"

	syncpkg "github.com/uhhhm/reverb/internal/sync"
)

// A peer whose clock is years ahead must not be able to win conflicts with it.
// Clamping the local clock is not enough on its own: the row is stored with the
// HLC the peer sent, and PickWinner compares those stored values, so an
// unclamped row would out-rank every later local edit forever.
func TestReconcileRejectsFarFutureHLC(t *testing.T) {
	st := newTestStoreSync(t)
	ctx := context.Background()
	ss := syncpkg.NewSyncStore(st.Q())
	createDevice(t, st, "dev_a", "a", 0)
	createDevice(t, st, "dev_b", "b", 0)

	now := time.Now().UnixMilli()
	if _, err := ss.AppendChange(ctx, "dev_a", syncpkg.SyncChange{
		EntityType: "track", EntityID: "t1", Field: "title", Value: "local", UpdatedAt: now, HLC: now,
	}); err != nil {
		t.Fatal(err)
	}

	future := time.Now().Add(10 * 365 * 24 * time.Hour).UnixMilli()
	inbound := []syncpkg.SyncChange{{
		EntityType: "track", EntityID: "t1", Field: "title", Value: "poisoned",
		UpdatedAt: future, HLC: future, DeviceID: "dev_b",
	}}
	_, _, rejected, err := ss.Reconcile(ctx, "dev_b", 0, inbound)
	if err != nil {
		t.Fatal(err)
	}
	if len(rejected) != 1 {
		t.Fatalf("rejected %d, want 1: a change beyond the drift bound must not be stored", len(rejected))
	}

	latest, err := ss.GetLatestForField(ctx, "track", "t1", "title")
	if err != nil || latest == nil {
		t.Fatalf("GetLatestForField: %v %v", err, latest)
	}
	if latest.Value != "local" {
		t.Fatalf("value = %v, want local: the far-future change won the merge", latest.Value)
	}
}

// The rejected row must not linger and beat later edits either.
func TestLaterLocalEditWinsAfterFarFutureRejected(t *testing.T) {
	st := newTestStoreSync(t)
	ctx := context.Background()
	ss := syncpkg.NewSyncStore(st.Q())
	createDevice(t, st, "dev_a", "a", 0)
	createDevice(t, st, "dev_b", "b", 0)

	future := time.Now().Add(10 * 365 * 24 * time.Hour).UnixMilli()
	if _, _, _, err := ss.Reconcile(ctx, "dev_b", 0, []syncpkg.SyncChange{{
		EntityType: "track", EntityID: "t1", Field: "title", Value: "poisoned",
		UpdatedAt: future, HLC: future, DeviceID: "dev_b",
	}}); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UnixMilli()
	if _, err := ss.AppendChange(ctx, "dev_a", syncpkg.SyncChange{
		EntityType: "track", EntityID: "t1", Field: "title", Value: "later", UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	latest, err := ss.GetLatestForField(ctx, "track", "t1", "title")
	if err != nil || latest == nil {
		t.Fatalf("GetLatestForField: %v %v", err, latest)
	}
	if latest.Value != "later" {
		t.Fatalf("value = %v, want later: a rejected far-future row still dominates", latest.Value)
	}
}

// A peer that is merely a little fast is normal and must keep working.
func TestReconcileAcceptsHLCWithinDrift(t *testing.T) {
	st := newTestStoreSync(t)
	ctx := context.Background()
	ss := syncpkg.NewSyncStore(st.Q())
	createDevice(t, st, "dev_a", "a", 0)
	createDevice(t, st, "dev_b", "b", 0)

	now := time.Now().UnixMilli()
	if _, err := ss.AppendChange(ctx, "dev_a", syncpkg.SyncChange{
		EntityType: "track", EntityID: "t1", Field: "title", Value: "local", UpdatedAt: now, HLC: now,
	}); err != nil {
		t.Fatal(err)
	}
	ahead := time.Now().Add(30 * time.Second).UnixMilli()
	_, _, rejected, err := ss.Reconcile(ctx, "dev_b", 0, []syncpkg.SyncChange{{
		EntityType: "track", EntityID: "t1", Field: "title", Value: "slightly ahead",
		UpdatedAt: ahead, HLC: ahead, DeviceID: "dev_b",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(rejected) != 0 {
		t.Fatalf("rejected %d, want 0: ordinary skew must not be refused", len(rejected))
	}
	latest, err := ss.GetLatestForField(ctx, "track", "t1", "title")
	if err != nil || latest == nil {
		t.Fatalf("GetLatestForField: %v %v", err, latest)
	}
	if latest.Value != "slightly ahead" {
		t.Fatalf("value = %v, want the newer change", latest.Value)
	}
}

// A change with HLC 0 is a legacy row: PickWinner falls back to its UpdatedAt,
// and prepareHLCVector feeds that same UpdatedAt to the local clock. Both paths
// have to honour the drift bound, or a peer can poison the clock and win every
// later conflict just by omitting the HLC.
func TestReconcileRejectsFarFutureUpdatedAtWithZeroHLC(t *testing.T) {
	st := newTestStoreSync(t)
	ctx := context.Background()
	ss := syncpkg.NewSyncStore(st.Q())
	createDevice(t, st, "dev_a", "a", 0)
	createDevice(t, st, "dev_b", "b", 0)

	now := time.Now().UnixMilli()
	if _, err := ss.AppendChange(ctx, "dev_a", syncpkg.SyncChange{
		EntityType: "track", EntityID: "t1", Field: "title", Value: "local", UpdatedAt: now, HLC: now,
	}); err != nil {
		t.Fatal(err)
	}

	future := time.Now().Add(10 * 365 * 24 * time.Hour).UnixMilli()
	inbound := []syncpkg.SyncChange{{
		EntityType: "track", EntityID: "t1", Field: "title", Value: "poisoned",
		UpdatedAt: future, HLC: 0, DeviceID: "dev_b",
	}}
	if _, _, rejected, err := ss.Reconcile(ctx, "dev_b", 0, inbound); err != nil {
		t.Fatal(err)
	} else if len(rejected) != 1 {
		t.Fatalf("rejected %d, want 1: a legacy change beyond the drift bound must not be stored", len(rejected))
	}

	// The local clock must still track wall time, so a later local edit outranks
	// anything the peer sent.
	if _, err := ss.AppendChange(ctx, "dev_a", syncpkg.SyncChange{
		EntityType: "track", EntityID: "t1", Field: "title", Value: "later", UpdatedAt: time.Now().UnixMilli(),
	}); err != nil {
		t.Fatal(err)
	}
	latest, err := ss.GetLatestForField(ctx, "track", "t1", "title")
	if err != nil || latest == nil {
		t.Fatalf("GetLatestForField: %v %v", err, latest)
	}
	if latest.Value != "later" {
		t.Fatalf("value = %v, want later: the poisoned clock outlived the change", latest.Value)
	}
	if bound := time.Now().Add(time.Hour).UnixMilli(); latest.HLC > bound {
		t.Fatalf("local HLC = %d, want <= %d: the peer's UpdatedAt poisoned the clock", latest.HLC, bound)
	}
}
