package sync_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	syncpkg "github.com/uhhhm/reverb/internal/sync"
)

// recordingMaterializer keeps the order changes were projected in.
type recordingMaterializer struct {
	types []string
	ids   []string
	done  chan struct{}
	want  int
}

func (m *recordingMaterializer) Apply(_ context.Context, ch syncpkg.SyncChange) error {
	m.types = append(m.types, ch.EntityType)
	m.ids = append(m.ids, ch.EntityID)
	if m.done != nil && len(m.types) == m.want {
		close(m.done)
	}
	return nil
}

// Splitting an oversized batch must not split a catalog entity away from the
// rows that name it. A play whose entity lands in a later slice fails the
// plays.catalog_id foreign key, and since the log has already committed and
// the vector has already advanced, nothing ever retries it.
func TestReconcileBatchedProjectsCatalogEntitiesFirstAcrossSlices(t *testing.T) {
	st := newTestStoreSync(t)
	ctx := context.Background()
	createDevice(t, st, "dev_local", "local", 1)
	createDevice(t, st, "dev_peer", "peer", 0)

	m := &recordingMaterializer{}
	ss := syncpkg.NewSyncStore(st.Q())
	ss.SetMaterializer(m)

	total := syncpkg.MaxReconcileBatch + 10
	inbound := make([]syncpkg.SyncChange, 0, total+1)
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
	// The entity every one of those tracks is named by arrives last.
	inbound = append(inbound, syncpkg.SyncChange{
		EntityType: syncpkg.EntityCatalog,
		EntityID:   "cat_0",
		Field:      syncpkg.FieldIdentity,
		Value:      map[string]any{"title": "Song"},
		UpdatedAt:  9000,
		DeviceID:   "dev_peer",
	})

	if _, _, rejected, err := ss.ReconcileBatched(ctx, "dev_peer", syncpkg.NoOutbound, inbound); err != nil {
		t.Fatal(err)
	} else if len(rejected) != 0 {
		t.Fatalf("rejected %d change(s), want 0", len(rejected))
	}

	if len(m.types) != len(inbound) {
		t.Fatalf("projected %d change(s), want %d", len(m.types), len(inbound))
	}
	if m.types[0] != syncpkg.EntityCatalog {
		t.Fatalf("first projection was %q, want the catalog entity ahead of the rows naming it", m.types[0])
	}
}

// The reply to a sync round must not wait on projection. The round runs under
// a short network deadline while the projection writes through the domain
// services -- minutes of work on a first sync after BackfillHistory -- so a
// synchronous projection burns the peer's deadline and it resends the whole
// batch next round.
func TestReconcileBatchedAsyncReturnsBeforeProjectionFinishes(t *testing.T) {
	st := newTestStoreSync(t)
	ctx := context.Background()
	createDevice(t, st, "dev_local", "local", 1)
	createDevice(t, st, "dev_peer", "peer", 0)

	blocker := &blockingMaterializer{entered: make(chan struct{}), release: make(chan struct{})}
	ss := syncpkg.NewSyncStore(st.Q())
	ss.SetMaterializer(blocker)

	returned := make(chan error, 1)
	go func() {
		_, _, _, err := ss.ReconcileBatchedAsync(ctx, "dev_peer", syncpkg.NoOutbound, []syncpkg.SyncChange{
			{EntityType: "track", EntityID: "cat_1", Field: "title", Value: "Peer Title", UpdatedAt: 2000, DeviceID: "dev_peer"},
		})
		returned <- err
	}()

	select {
	case <-blocker.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("projection never started")
	}
	select {
	case err := <-returned:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ReconcileBatchedAsync waited for the projection to finish")
	}
	close(blocker.release)
}
