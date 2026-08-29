package sync_test

import (
	"context"
	"testing"

	"github.com/uhhhm/reverb/internal/crop"
	"github.com/uhhhm/reverb/internal/materialize"
	"github.com/uhhhm/reverb/internal/override"
	syncpkg "github.com/uhhhm/reverb/internal/sync"
)

// A change is not synced until it is visible. Reconcile writes it into the log;
// the materializer is what turns it into something the app reads back.
func TestReconcileMaterializesTrackMetadata(t *testing.T) {
	st := newTestStoreSync(t)
	ctx := context.Background()
	createDevice(t, st, "dev_local", "This one", 1)
	createDevice(t, st, "dev_peer", "The other one", 0)

	overrides, crops := override.New(st.Q()), crop.New(st.Q())
	ss := syncpkg.NewSyncStore(st.Q())
	ss.SetMaterializer(materialize.New(overrides, crops))

	inbound := []syncpkg.SyncChange{
		{EntityType: "track", EntityID: "cat_1", Field: "title", Value: "Peer Title", UpdatedAt: 2000, DeviceID: "dev_peer"},
		{EntityType: "track", EntityID: "cat_1", Field: "cropStartMs", Value: float64(7000), UpdatedAt: 2000, DeviceID: "dev_peer"},
	}
	if _, _, rejected, err := ss.Reconcile(ctx, "dev_peer", 0, inbound); err != nil {
		t.Fatal(err)
	} else if len(rejected) != 0 {
		t.Fatalf("rejected %v", rejected)
	}

	name, err := overrides.GetByCatalogID(ctx, "cat_1")
	if err != nil {
		t.Fatal(err)
	}
	if name.Title != "Peer Title" {
		t.Fatalf("title = %q, want the peer's rename applied", name.Title)
	}
	points, err := crops.GetByCatalogID(ctx, "cat_1")
	if err != nil {
		t.Fatal(err)
	}
	if points.StartMs != 7000 {
		t.Fatalf("crop start = %d, want 7000", points.StartMs)
	}
}

// A change the merge policy rejects (an older write losing to a newer one) must
// not be materialized — otherwise the loser would overwrite the winner in the
// table while the log correctly kept the winner.
func TestRejectedChangeIsNotMaterialized(t *testing.T) {
	st := newTestStoreSync(t)
	ctx := context.Background()
	createDevice(t, st, "dev_local", "This one", 1)
	createDevice(t, st, "dev_peer", "The other one", 0)

	overrides, crops := override.New(st.Q()), crop.New(st.Q())
	ss := syncpkg.NewSyncStore(st.Q())
	ss.SetMaterializer(materialize.New(overrides, crops))

	newer := []syncpkg.SyncChange{{EntityType: "track", EntityID: "cat_1", Field: "title", Value: "Newer", UpdatedAt: 5000, DeviceID: "dev_peer"}}
	if _, _, _, err := ss.Reconcile(ctx, "dev_peer", 0, newer); err != nil {
		t.Fatal(err)
	}
	older := []syncpkg.SyncChange{{EntityType: "track", EntityID: "cat_1", Field: "title", Value: "Older", UpdatedAt: 1000, DeviceID: "dev_peer"}}
	if _, _, rejected, err := ss.Reconcile(ctx, "dev_peer", 0, older); err != nil {
		t.Fatal(err)
	} else if len(rejected) != 1 {
		t.Fatalf("want the older write rejected, got %v", rejected)
	}

	name, _ := overrides.GetByCatalogID(ctx, "cat_1")
	if name.Title != "Newer" {
		t.Fatalf("title = %q, want the newer write to have survived", name.Title)
	}
}

// A store with no materializer still replicates; it just does not surface what
// it received. Nothing may panic.
func TestReconcileWithoutMaterializer(t *testing.T) {
	st := newTestStoreSync(t)
	ctx := context.Background()
	createDevice(t, st, "dev_peer", "The other one", 0)
	ss := syncpkg.NewSyncStore(st.Q())
	inbound := []syncpkg.SyncChange{{EntityType: "track", EntityID: "cat_1", Field: "title", Value: "x", UpdatedAt: 1000, DeviceID: "dev_peer"}}
	if _, _, _, err := ss.Reconcile(ctx, "dev_peer", 0, inbound); err != nil {
		t.Fatal(err)
	}
}
