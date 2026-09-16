package sync_test

import (
	"context"
	"testing"

	syncpkg "github.com/uhhhm/reverb/internal/sync"
)

// A relay must retain losing edits too: a downstream device needs their author
// sequence numbers to move past a page and fetch the rest of the history.
func TestRelayConflictDoesNotStrandDownstreamVector(t *testing.T) {
	ctx := context.Background()
	newReplica := func() *syncpkg.SyncStore {
		st := newTestStoreSync(t)
		createDevice(t, st, "author", "author", 0)
		createDevice(t, st, "relay", "relay", 0)
		return syncpkg.NewSyncStore(st.Q())
	}
	relay, downstream := newReplica(), newReplica()
	apply := func(ss *syncpkg.SyncStore, changes []syncpkg.SyncChange) {
		t.Helper()
		if _, _, _, err := ss.Reconcile(ctx, "relay", syncpkg.NoOutbound, changes); err != nil {
			t.Fatal(err)
		}
	}
	apply(relay, []syncpkg.SyncChange{{DeviceID: "relay", Seq: 1, HLC: 2000, UpdatedAt: 2000, EntityType: "track", EntityID: "track", Field: "title", Value: "winner"}})
	apply(relay, []syncpkg.SyncChange{
		{DeviceID: "author", Seq: 1, HLC: 1000, UpdatedAt: 1000, EntityType: "track", EntityID: "track", Field: "title", Value: "loser"},
		{DeviceID: "author", Seq: 2, HLC: 1001, UpdatedAt: 1001, EntityType: "play", EntityID: "play1", Field: "record", Value: "one"},
		{DeviceID: "author", Seq: 3, HLC: 1002, UpdatedAt: 1002, EntityType: "play", EntityID: "play2", Field: "record", Value: "two"},
	})
	for i := 0; i < 4; i++ {
		vector, _, err := downstream.GetVectorMap(ctx)
		if err != nil {
			t.Fatal(err)
		}
		page, err := relay.ListSinceVector(ctx, vector, 2)
		if err != nil {
			t.Fatal(err)
		}
		apply(downstream, page)
	}
	seq, _, err := downstream.GetVector(ctx, "author")
	if err != nil || seq != 3 {
		t.Fatalf("downstream author vector = %d, %v; want 3", seq, err)
	}
	winner, err := downstream.GetLatestForField(ctx, "track", "track", "title")
	if err != nil || winner == nil || winner.Value != "winner" {
		t.Fatalf("conflict winner = %+v, %v", winner, err)
	}
}

func TestFailedAppendDoesNotConsumeAuthorSequence(t *testing.T) {
	st := newTestStoreSync(t)
	ctx := context.Background()
	createDevice(t, st, "author", "author", 0)
	ss := syncpkg.NewSyncStore(st.Q())
	if _, err := st.Q().UnderlyingDB().ExecContext(ctx, `CREATE TRIGGER reject_sync BEFORE INSERT ON sync_change BEGIN SELECT RAISE(ABORT, 'disk error'); END`); err != nil {
		t.Fatal(err)
	}
	change := syncpkg.SyncChange{EntityType: "track", EntityID: "track", Field: "title", Value: "title", UpdatedAt: 1000}
	if _, err := ss.AppendChange(ctx, "author", change); err == nil {
		t.Fatal("expected storage failure")
	}
	if _, err := st.Q().UnderlyingDB().ExecContext(ctx, `DROP TRIGGER reject_sync`); err != nil {
		t.Fatal(err)
	}
	if _, err := ss.AppendChange(ctx, "author", change); err != nil {
		t.Fatal(err)
	}
	rows, err := ss.ListSince(ctx, 0, 10)
	if err != nil || len(rows) != 1 || rows[0].Seq != 1 {
		t.Fatalf("failed append left a sequence gap: %+v, %v", rows, err)
	}
}
