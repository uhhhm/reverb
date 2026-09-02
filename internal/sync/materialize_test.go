package sync_test

import (
	"context"
	"testing"
	"time"

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

// cancelOnFirst mimics a sync round whose deadline expires the moment the log
// commits: the first projection cancels the caller's context, and every Apply
// records what its own context saw.
type cancelOnFirst struct {
	cancel context.CancelFunc
	seen   []error
	fields []string
}

func (m *cancelOnFirst) Apply(ctx context.Context, ch syncpkg.SyncChange) error {
	if len(m.seen) == 0 && m.cancel != nil {
		m.cancel()
	}
	m.seen = append(m.seen, ctx.Err())
	m.fields = append(m.fields, ch.Field)
	return nil
}

// Projection must survive the sync round's deadline. The log commits inside
// that deadline and the local vector advances with it, so no peer ever resends
// these rows: if Apply were to fail with the round's context error, the change
// would be in the log and permanently invisible on this device.
func TestMaterializeIsNotBoundToTheCallersDeadline(t *testing.T) {
	st := newTestStoreSync(t)
	createDevice(t, st, "dev_local", "This one", 1)
	createDevice(t, st, "dev_peer", "The other one", 0)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := &cancelOnFirst{cancel: cancel}
	ss := syncpkg.NewSyncStore(st.Q())
	ss.SetMaterializer(m)

	inbound := []syncpkg.SyncChange{
		{EntityType: "track", EntityID: "cat_1", Field: "title", Value: "One", UpdatedAt: 2000, DeviceID: "dev_peer"},
		{EntityType: "track", EntityID: "cat_2", Field: "title", Value: "Two", UpdatedAt: 2000, DeviceID: "dev_peer"},
		{EntityType: "track", EntityID: "cat_3", Field: "title", Value: "Three", UpdatedAt: 2000, DeviceID: "dev_peer"},
	}
	if _, _, rejected, err := ss.Reconcile(ctx, "dev_peer", syncpkg.NoOutbound, inbound); err != nil {
		t.Fatal(err)
	} else if len(rejected) != 0 {
		t.Fatalf("rejected %v", rejected)
	}

	if len(m.fields) != len(inbound) {
		t.Fatalf("materialized %d change(s), want %d", len(m.fields), len(inbound))
	}
	for i, err := range m.seen {
		if err != nil {
			t.Fatalf("projection %d ran under a canceled context: %v", i, err)
		}
	}
}

// blockingMaterializer holds up its first Apply until released, standing in for
// a slow projection (a first sync after BackfillHistory projects thousands of
// plays).
type blockingMaterializer struct {
	entered chan struct{}
	release chan struct{}
	once    bool
}

func (m *blockingMaterializer) Apply(context.Context, syncpkg.SyncChange) error {
	if !m.once {
		m.once = true
		close(m.entered)
		<-m.release
	}
	return nil
}

// Projection must run with the store mutex released. Every local write -- a
// play, a rename, a playlist edit -- takes the same mutex, so holding it across
// materialize would freeze the app for as long as the projection ran.
func TestReconcileDoesNotHoldStoreLockDuringMaterialize(t *testing.T) {
	st := newTestStoreSync(t)
	ctx := context.Background()
	createDevice(t, st, "dev_local", "This one", 1)
	createDevice(t, st, "dev_peer", "The other one", 0)

	m := &blockingMaterializer{entered: make(chan struct{}), release: make(chan struct{})}
	ss := syncpkg.NewSyncStore(st.Q())
	ss.SetMaterializer(m)

	done := make(chan error, 1)
	go func() {
		_, _, _, err := ss.Reconcile(ctx, "dev_peer", 0, []syncpkg.SyncChange{
			{EntityType: "track", EntityID: "cat_1", Field: "title", Value: "Peer Title", UpdatedAt: 2000, DeviceID: "dev_peer"},
		})
		done <- err
	}()

	select {
	case <-m.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("materialize never ran")
	}

	appended := make(chan error, 1)
	go func() {
		_, err := ss.AppendChange(ctx, "dev_local", syncpkg.SyncChange{
			EntityType: "track", EntityID: "cat_2", Field: "title", Value: "Local", UpdatedAt: 3000, DeviceID: "dev_local",
		})
		appended <- err
	}()
	select {
	case err := <-appended:
		if err != nil {
			t.Fatalf("local write during materialize: %v", err)
		}
	case <-time.After(5 * time.Second):
		close(m.release)
		t.Fatal("a local write blocked while a peer's changes were being materialized")
	}

	close(m.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
