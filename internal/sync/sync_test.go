package sync_test

import (
	"context"
	"testing"

	"github.com/uhhhm/reverb/internal/store"
	"github.com/uhhhm/reverb/internal/store/db"
	syncpkg "github.com/uhhhm/reverb/internal/sync"
)

func newTestStoreSync(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(t.TempDir() + "/sync.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	return st
}

func createDevice(t *testing.T, st *store.Store, id, name string, isServer int64) {
	t.Helper()
	if err := st.Q().CreateDevice(context.Background(), db.CreateDeviceParams{
		ID:        id,
		Name:      name,
		TokenHash: "hash_" + id,
		IsServer:  isServer,
	}); err != nil {
		t.Fatalf("create device %s: %v", id, err)
	}
}

func TestSyncEmptySince(t *testing.T) {
	st := newTestStoreSync(t)
	ctx := context.Background()
	q := st.Q()
	ss := syncpkg.NewSyncStore(q)
	createDevice(t, st, "dev_server", "server", 1)
	createDevice(t, st, "dev_client", "client", 0)

	// ListSince empty
	changes, err := ss.ListSince(ctx, 0, 10)
	if err != nil {
		t.Fatalf("ListSince: %v", err)
	}
	if len(changes) != 0 {
		t.Fatalf("expected 0 changes, got %d", len(changes))
	}
	maxRev, err := ss.GetMaxRevision(ctx)
	if err != nil {
		t.Fatalf("GetMaxRevision: %v", err)
	}
	if maxRev != 0 {
		t.Fatalf("maxRev %d want 0", maxRev)
	}
	// Reconcile empty
	out, newRev, rejected, err := ss.Reconcile(ctx, "dev_client", 0, nil)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("outbound %d want 0", len(out))
	}
	if newRev != 0 {
		t.Fatalf("newRev %d want 0", newRev)
	}
	if len(rejected) != 0 {
		t.Fatalf("rejected %d want 0", len(rejected))
	}
	cur, err := ss.GetCursor(ctx, "dev_client")
	if err != nil {
		t.Fatalf("GetCursor: %v", err)
	}
	if cur != 0 {
		t.Fatalf("cursor %d want 0", cur)
	}
}

func TestSyncLWWNewerWins(t *testing.T) {
	st := newTestStoreSync(t)
	ctx := context.Background()
	q := st.Q()
	ss := syncpkg.NewSyncStore(q)
	createDevice(t, st, "dev_a", "a", 0)
	createDevice(t, st, "dev_b", "b", 0)

	// seed older
	rev1, err := ss.AppendChange(ctx, "dev_a", syncpkg.SyncChange{
		EntityType: "track", EntityID: "t1", Field: "title", Value: "old", UpdatedAt: 1000,
	})
	if err != nil {
		t.Fatal(err)
	}
	if rev1 != 1 {
		t.Fatalf("rev1 %d want 1", rev1)
	}
	// newer inbound should win
	inbound := []syncpkg.SyncChange{{EntityType: "track", EntityID: "t1", Field: "title", Value: "new", UpdatedAt: 2000, DeviceID: "dev_b"}}
	out, newRev, rejected, err := ss.Reconcile(ctx, "dev_b", 0, inbound)
	if err != nil {
		t.Fatal(err)
	}
	if len(rejected) != 0 {
		t.Fatalf("rejected %d want 0", len(rejected))
	}
	if newRev != 2 {
		t.Fatalf("newRev %d want 2", newRev)
	}
	if len(out) != 2 {
		t.Fatalf("outbound %d want 2", len(out))
	}
	latest, err := ss.GetLatestForField(ctx, "track", "t1", "title")
	if err != nil || latest == nil {
		t.Fatalf("GetLatest: %v %v", err, latest)
	}
	if latest.Value != "new" {
		t.Fatalf("value %v want new", latest.Value)
	}
	if latest.UpdatedAt != 2000 {
		t.Fatalf("updatedAt %d want 2000", latest.UpdatedAt)
	}
}

func TestSyncOlderLoses(t *testing.T) {
	st := newTestStoreSync(t)
	ctx := context.Background()
	ss := syncpkg.NewSyncStore(st.Q())
	createDevice(t, st, "dev_a", "a", 0)
	createDevice(t, st, "dev_b", "b", 0)

	_, err := ss.AppendChange(ctx, "dev_a", syncpkg.SyncChange{EntityType: "track", EntityID: "t1", Field: "title", Value: "newer", UpdatedAt: 2000})
	if err != nil {
		t.Fatal(err)
	}
	inbound := []syncpkg.SyncChange{{EntityType: "track", EntityID: "t1", Field: "title", Value: "older", UpdatedAt: 1000, DeviceID: "dev_b"}}
	_, _, rejected, err := ss.Reconcile(ctx, "dev_b", 0, inbound)
	if err != nil {
		t.Fatal(err)
	}
	if len(rejected) != 1 {
		t.Fatalf("rejected %d want 1", len(rejected))
	}
	latest, _ := ss.GetLatestForField(ctx, "track", "t1", "title")
	if latest.Value != "newer" {
		t.Fatalf("value %v want newer", latest.Value)
	}
	// outbound should still be 1 row (the original)
	out, _, _, _ := ss.Reconcile(ctx, "dev_b", 0, nil)
	if len(out) != 1 {
		t.Fatalf("out %d want 1", len(out))
	}
}

func TestSyncDeleteWinsOverEdit(t *testing.T) {
	st := newTestStoreSync(t)
	ctx := context.Background()
	ss := syncpkg.NewSyncStore(st.Q())
	createDevice(t, st, "dev_a", "a", 0)
	createDevice(t, st, "dev_b", "b", 0)

	// edit then delete (delete older but should still win)
	_, err := ss.AppendChange(ctx, "dev_a", syncpkg.SyncChange{EntityType: "track", EntityID: "t1", Field: "title", Value: "hello", UpdatedAt: 1000})
	if err != nil {
		t.Fatal(err)
	}
	// incoming delete with older timestamp 900 should still win per spec: delete wins over edit irrespective of UpdatedAt
	inboundDel := []syncpkg.SyncChange{{EntityType: "track", EntityID: "t1", Field: "__deleted", Value: nil, UpdatedAt: 900, DeviceID: "dev_b"}}
	_, _, rejected, err := ss.Reconcile(ctx, "dev_b", 0, inboundDel)
	if err != nil {
		t.Fatal(err)
	}
	if len(rejected) != 0 {
		t.Fatalf("delete should be accepted even if older, rejected %d", len(rejected))
	}
	// now entity is deleted, edit should be rejected even if newer
	inboundEdit := []syncpkg.SyncChange{{EntityType: "track", EntityID: "t1", Field: "title", Value: "resurrect", UpdatedAt: 2000, DeviceID: "dev_a"}}
	_, _, rejected, err = ss.Reconcile(ctx, "dev_a", 0, inboundEdit)
	if err != nil {
		t.Fatal(err)
	}
	if len(rejected) != 1 {
		t.Fatalf("edit after delete should be rejected, rejected %d", len(rejected))
	}
	// also test delete vs delete LWW: second delete newer should win
	inboundDel2 := []syncpkg.SyncChange{{EntityType: "track", EntityID: "t1", Field: "__deleted", Value: nil, UpdatedAt: 1500, DeviceID: "dev_a"}}
	_, _, rejected, err = ss.Reconcile(ctx, "dev_a", 0, inboundDel2)
	if err != nil {
		t.Fatal(err)
	}
	if len(rejected) != 0 {
		t.Fatalf("second delete newer should win, rejected %d", len(rejected))
	}
	// delete vs delete older should lose
	inboundDelOld := []syncpkg.SyncChange{{EntityType: "track", EntityID: "t1", Field: "__deleted", Value: nil, UpdatedAt: 800, DeviceID: "dev_b"}}
	_, _, rejected, err = ss.Reconcile(ctx, "dev_b", 0, inboundDelOld)
	if err != nil {
		t.Fatal(err)
	}
	if len(rejected) != 1 {
		t.Fatalf("older delete should lose, rejected %d", len(rejected))
	}
}

func TestSyncTieServerWins(t *testing.T) {
	st := newTestStoreSync(t)
	ctx := context.Background()
	ss := syncpkg.NewSyncStore(st.Q())
	createDevice(t, st, "dev_server", "server", 1)
	createDevice(t, st, "dev_client", "client", 0)

	// client writes first at 1000
	_, err := ss.AppendChange(ctx, "dev_client", syncpkg.SyncChange{EntityType: "playlist", EntityID: "p1", Field: "name", Value: "clientVal", UpdatedAt: 1000})
	if err != nil {
		t.Fatal(err)
	}
	// server writes same millis, should win due to server tie-break
	inbound := []syncpkg.SyncChange{{EntityType: "playlist", EntityID: "p1", Field: "name", Value: "serverVal", UpdatedAt: 1000, DeviceID: "dev_server"}}
	_, _, rejected, err := ss.Reconcile(ctx, "dev_server", 0, inbound)
	if err != nil {
		t.Fatal(err)
	}
	if len(rejected) != 0 {
		t.Fatalf("server tie should win, rejected %d", len(rejected))
	}
	latest, _ := ss.GetLatestForField(ctx, "playlist", "p1", "name")
	if latest.Value != "serverVal" {
		t.Fatalf("server should have won, got %v", latest.Value)
	}
	// reverse: server existing, client incoming same millis should lose
	st2 := newTestStoreSync(t)
	ss2 := syncpkg.NewSyncStore(st2.Q())
	createDevice(t, st2, "dev_server", "server", 1)
	createDevice(t, st2, "dev_client", "client", 0)
	_, err = ss2.AppendChange(ctx, "dev_server", syncpkg.SyncChange{EntityType: "playlist", EntityID: "p1", Field: "name", Value: "serverVal", UpdatedAt: 1000})
	if err != nil {
		t.Fatal(err)
	}
	inbound2 := []syncpkg.SyncChange{{EntityType: "playlist", EntityID: "p1", Field: "name", Value: "clientVal", UpdatedAt: 1000, DeviceID: "dev_client"}}
	_, _, rejected, err = ss2.Reconcile(ctx, "dev_client", 0, inbound2)
	if err != nil {
		t.Fatal(err)
	}
	if len(rejected) != 1 {
		t.Fatalf("client tie vs server should lose, rejected %d", len(rejected))
	}
	latest, _ = ss2.GetLatestForField(ctx, "playlist", "p1", "name")
	if latest.Value != "serverVal" {
		t.Fatalf("server value should remain, got %v", latest.Value)
	}
	// also test direct LWWPolicy
	policy := syncpkg.LWWPolicy{}
	// set lookup to simulate server
	syncpkg.SetDeviceIsServerLookup(func(id string) bool { return id == "dev_server" })
	defer syncpkg.SetDeviceIsServerLookup(nil)
	existing := syncpkg.SyncChange{EntityType: "t", EntityID: "x", Field: "f", UpdatedAt: 1000, DeviceID: "dev_client"}
	incoming := syncpkg.SyncChange{EntityType: "t", EntityID: "x", Field: "f", UpdatedAt: 1000, DeviceID: "dev_server"}
	if !policy.PickWinner(existing, incoming) {
		t.Fatalf("LWWPolicy server should win tie")
	}
	if policy.PickWinner(incoming, existing) {
		t.Fatalf("LWWPolicy server should win, client should not")
	}
}

func TestSyncTieLexOrder(t *testing.T) {
	st := newTestStoreSync(t)
	ctx := context.Background()
	ss := syncpkg.NewSyncStore(st.Q())
	// two non-server devices with deterministic lex
	createDevice(t, st, "dev_a", "a", 0)
	createDevice(t, st, "dev_b", "b", 0)

	_, err := ss.AppendChange(ctx, "dev_b", syncpkg.SyncChange{EntityType: "track", EntityID: "t1", Field: "title", Value: "bVal", UpdatedAt: 5000})
	if err != nil {
		t.Fatal(err)
	}
	// incoming from lex smaller (dev_a) same timestamp should win
	inbound := []syncpkg.SyncChange{{EntityType: "track", EntityID: "t1", Field: "title", Value: "aVal", UpdatedAt: 5000, DeviceID: "dev_a"}}
	_, _, rejected, err := ss.Reconcile(ctx, "dev_a", 0, inbound)
	if err != nil {
		t.Fatal(err)
	}
	if len(rejected) != 0 {
		t.Fatalf("lex smaller should win, rejected %d", len(rejected))
	}
	latest, _ := ss.GetLatestForField(ctx, "track", "t1", "title")
	if latest.Value != "aVal" {
		t.Fatalf("lex winner aVal expected, got %v", latest.Value)
	}

	// reverse: existing a, incoming b (lex larger) should lose
	st2 := newTestStoreSync(t)
	ss2 := syncpkg.NewSyncStore(st2.Q())
	createDevice(t, st2, "dev_a", "a", 0)
	createDevice(t, st2, "dev_b", "b", 0)
	_, err = ss2.AppendChange(ctx, "dev_a", syncpkg.SyncChange{EntityType: "track", EntityID: "t1", Field: "title", Value: "aVal", UpdatedAt: 5000})
	if err != nil {
		t.Fatal(err)
	}
	inbound2 := []syncpkg.SyncChange{{EntityType: "track", EntityID: "t1", Field: "title", Value: "bVal", UpdatedAt: 5000, DeviceID: "dev_b"}}
	_, _, rejected, err = ss2.Reconcile(ctx, "dev_b", 0, inbound2)
	if err != nil {
		t.Fatal(err)
	}
	if len(rejected) != 1 {
		t.Fatalf("lex larger should lose, rejected %d", len(rejected))
	}
	latest, _ = ss2.GetLatestForField(ctx, "track", "t1", "title")
	if latest.Value != "aVal" {
		t.Fatalf("aVal should remain, got %v", latest.Value)
	}

	// direct policy lex test
	policy := syncpkg.LWWPolicy{}
	syncpkg.SetDeviceIsServerLookup(nil)
	existing := syncpkg.SyncChange{UpdatedAt: 1000, DeviceID: "dev_b"}
	incoming := syncpkg.SyncChange{UpdatedAt: 1000, DeviceID: "dev_a"}
	if !policy.PickWinner(existing, incoming) {
		t.Fatalf("lex smaller should win")
	}
	if policy.PickWinner(incoming, existing) {
		t.Fatalf("lex larger should lose")
	}
}

func TestSyncConcurrentSameMillis(t *testing.T) {
	st := newTestStoreSync(t)
	ctx := context.Background()
	ss := syncpkg.NewSyncStore(st.Q())
	createDevice(t, st, "dev_a", "a", 0)
	createDevice(t, st, "dev_b", "b", 0)
	createDevice(t, st, "dev_c", "c", 0)

	// concurrent writes with same UpdatedAt but different devices/values
	// First append from dev_c
	_, err := ss.AppendChange(ctx, "dev_c", syncpkg.SyncChange{EntityType: "track", EntityID: "t1", Field: "title", Value: "cVal", UpdatedAt: 7777})
	if err != nil {
		t.Fatal(err)
	}
	// inbound batch with two changes same entity+field same millis, different devices (simulating concurrent)
	// Actually Reconcile processes sequentially, so second inbound in same batch will see first's result
	// We test that handling is deterministic.
	inbound := []syncpkg.SyncChange{
		{EntityType: "track", EntityID: "t1", Field: "title", Value: "aVal", UpdatedAt: 7777, DeviceID: "dev_a"},
		{EntityType: "track", EntityID: "t1", Field: "title", Value: "bVal", UpdatedAt: 7777, DeviceID: "dev_b"},
	}
	_, _, rejected, err := ss.Reconcile(ctx, "dev_a", 0, inbound[:1])
	if err != nil {
		t.Fatal(err)
	}
	// dev_a lex smaller than dev_c, so aVal should win over cVal
	if len(rejected) != 0 {
		t.Fatalf("dev_a vs dev_c same millis, lex smaller should win, rejected %d", len(rejected))
	}
	// now dev_b incoming same millis vs dev_a existing (a is lex smaller), so b should lose
	_, _, rejected, err = ss.Reconcile(ctx, "dev_b", 0, inbound[1:2])
	if err != nil {
		t.Fatal(err)
	}
	if len(rejected) != 1 {
		t.Fatalf("dev_b lex larger should lose, rejected %d", len(rejected))
	}
	latest, _ := ss.GetLatestForField(ctx, "track", "t1", "title")
	if latest.Value != "aVal" {
		t.Fatalf("expected aVal, got %v", latest.Value)
	}
	// revision monotonic check
	maxRev, _ := ss.GetMaxRevision(ctx)
	if maxRev != 2 {
		t.Fatalf("maxRev %d want 2 (c and a)", maxRev)
	}
}

func TestSyncRevisionMonotonic(t *testing.T) {
	st := newTestStoreSync(t)
	ctx := context.Background()
	ss := syncpkg.NewSyncStore(st.Q())
	createDevice(t, st, "dev_a", "a", 0)

	var prev int64
	for i := 0; i < 5; i++ {
		rev, err := ss.AppendChange(ctx, "dev_a", syncpkg.SyncChange{
			EntityType: "track", EntityID: "t1", Field: "title", Value: "v", UpdatedAt: int64(1000 + i),
		})
		if err != nil {
			t.Fatal(err)
		}
		if rev <= prev {
			t.Fatalf("revision not monotonic: prev %d rev %d", prev, rev)
		}
		prev = rev
		maxRev, _ := ss.GetMaxRevision(ctx)
		if maxRev != rev {
			t.Fatalf("maxRev %d != rev %d", maxRev, rev)
		}
	}
	// ensure ListSince ordered
	changes, _ := ss.ListSince(ctx, 0, 10)
	for i := 1; i < len(changes); i++ {
		if changes[i].Revision <= changes[i-1].Revision {
			t.Fatalf("not ordered: %d <= %d at %d", changes[i].Revision, changes[i-1].Revision, i)
		}
	}
}

func TestSyncCursorUpdate(t *testing.T) {
	st := newTestStoreSync(t)
	ctx := context.Background()
	ss := syncpkg.NewSyncStore(st.Q())
	createDevice(t, st, "dev_client", "client", 0)

	// initially 0
	cur, _ := ss.GetCursor(ctx, "dev_client")
	if cur != 0 {
		t.Fatalf("initial cursor %d want 0", cur)
	}
	// append and reconcile should update cursor
	_, err := ss.AppendChange(ctx, "dev_client", syncpkg.SyncChange{EntityType: "track", EntityID: "t1", Field: "title", Value: "v", UpdatedAt: 1000})
	if err != nil {
		t.Fatal(err)
	}
	maxRev, _ := ss.GetMaxRevision(ctx)
	if maxRev != 1 {
		t.Fatalf("maxRev %d want 1", maxRev)
	}
	// reconcile with since 0 should set cursor to max
	out, newRev, _, err := ss.Reconcile(ctx, "dev_client", 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if newRev != 1 {
		t.Fatalf("newRev %d want 1", newRev)
	}
	if len(out) != 1 {
		t.Fatalf("out %d want 1", len(out))
	}
	cur, _ = ss.GetCursor(ctx, "dev_client")
	if cur != 1 {
		t.Fatalf("cursor %d want 1", cur)
	}
	// add another, reconcile with since 1 should only return new
	_, err = ss.AppendChange(ctx, "dev_client", syncpkg.SyncChange{EntityType: "track", EntityID: "t1", Field: "title", Value: "v2", UpdatedAt: 2000})
	if err != nil {
		t.Fatal(err)
	}
	out, newRev, _, err = ss.Reconcile(ctx, "dev_client", 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 {
		t.Fatalf("out %d want 1 since 1", len(out))
	}
	if out[0].Revision != 2 {
		t.Fatalf("revision %d want 2", out[0].Revision)
	}
	if newRev != 2 {
		t.Fatalf("newRev %d want 2", newRev)
	}
	cur, _ = ss.GetCursor(ctx, "dev_client")
	if cur != 2 {
		t.Fatalf("cursor %d want 2", cur)
	}
	// SetCursor direct
	if err := ss.SetCursor(ctx, "dev_client", 5); err != nil {
		t.Fatal(err)
	}
	cur, _ = ss.GetCursor(ctx, "dev_client")
	if cur != 5 {
		t.Fatalf("cursor after Set %d want 5", cur)
	}
}

func TestSyncInboundAcceptedCount(t *testing.T) {
	st := newTestStoreSync(t)
	ctx := context.Background()
	ss := syncpkg.NewSyncStore(st.Q())
	createDevice(t, st, "dev_a", "a", 0)
	createDevice(t, st, "dev_b", "b", 0)

	// seed one
	_, err := ss.AppendChange(ctx, "dev_a", syncpkg.SyncChange{EntityType: "track", EntityID: "t1", Field: "title", Value: "orig", UpdatedAt: 1000})
	if err != nil {
		t.Fatal(err)
	}
	// inbound batch: one newer (should accept), one older (should reject), one new entity (accept)
	inbound := []syncpkg.SyncChange{
		{EntityType: "track", EntityID: "t1", Field: "title", Value: "newer", UpdatedAt: 2000, DeviceID: "dev_b"},     // accept
		{EntityType: "track", EntityID: "t1", Field: "title", Value: "older", UpdatedAt: 500, DeviceID: "dev_b"},      // reject (older than both)
		{EntityType: "track", EntityID: "t2", Field: "title", Value: "newEntity", UpdatedAt: 1000, DeviceID: "dev_b"}, // accept
	}
	// Note: second inbound is for same field t1 title but after first inbound accepted, its timestamp 500 is older than new winner 2000, so still reject
	out, newRev, rejected, err := ss.Reconcile(ctx, "dev_b", 0, inbound)
	if err != nil {
		t.Fatal(err)
	}
	if len(rejected) != 1 {
		t.Fatalf("rejected %d want 1, got %v", len(rejected), rejected)
	}
	accepted := len(inbound) - len(rejected)
	if accepted != 2 {
		t.Fatalf("accepted %d want 2", accepted)
	}
	if newRev != 3 { // initial 1 + 2 accepted
		t.Fatalf("newRev %d want 3", newRev)
	}
	if len(out) != 3 {
		t.Fatalf("out %d want 3", len(out))
	}
	// check SyncResponse accepted/rejected via Reconcile directly matches counts
	// also test empty accepted
	_, _, rejected2, _ := ss.Reconcile(ctx, "dev_b", 0, []syncpkg.SyncChange{
		{EntityType: "track", EntityID: "t1", Field: "title", Value: "evenOlder", UpdatedAt: 100, DeviceID: "dev_b"},
	})
	if len(rejected2) != 1 {
		t.Fatalf("older should be rejected")
	}
}

func TestSyncGetLatestForField(t *testing.T) {
	st := newTestStoreSync(t)
	ctx := context.Background()
	ss := syncpkg.NewSyncStore(st.Q())
	createDevice(t, st, "dev_a", "a", 0)

	// none
	latest, err := ss.GetLatestForField(ctx, "track", "nonexist", "title")
	if err != nil {
		t.Fatal(err)
	}
	if latest != nil {
		t.Fatalf("expected nil, got %v", latest)
	}
	// append
	_, err = ss.AppendChange(ctx, "dev_a", syncpkg.SyncChange{EntityType: "track", EntityID: "t1", Field: "title", Value: "v1", UpdatedAt: 1000})
	if err != nil {
		t.Fatal(err)
	}
	latest, _ = ss.GetLatestForField(ctx, "track", "t1", "title")
	if latest == nil || latest.Value != "v1" {
		t.Fatalf("got %v", latest)
	}
	// different field should not interfere (per-field)
	_, err = ss.AppendChange(ctx, "dev_a", syncpkg.SyncChange{EntityType: "track", EntityID: "t1", Field: "artist", Value: "artist1", UpdatedAt: 1000})
	if err != nil {
		t.Fatal(err)
	}
	latest, _ = ss.GetLatestForField(ctx, "track", "t1", "title")
	if latest.Value != "v1" {
		t.Fatalf("per-field isolation failed, got %v", latest.Value)
	}
	latest, _ = ss.GetLatestForField(ctx, "track", "t1", "artist")
	if latest.Value != "artist1" {
		t.Fatalf("artist %v", latest.Value)
	}
}

func TestSyncAppendAndListSince(t *testing.T) {
	st := newTestStoreSync(t)
	ctx := context.Background()
	ss := syncpkg.NewSyncStore(st.Q())
	createDevice(t, st, "dev_a", "a", 0)

	for i := 0; i < 3; i++ {
		_, err := ss.AppendChange(ctx, "dev_a", syncpkg.SyncChange{
			EntityType: "playlist", EntityID: "p1", Field: "name", Value: "v", UpdatedAt: int64(1000 + i),
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	all, _ := ss.ListSince(ctx, 0, 10)
	if len(all) != 3 {
		t.Fatalf("all %d want 3", len(all))
	}
	since1, _ := ss.ListSince(ctx, 1, 10)
	if len(since1) != 2 {
		t.Fatalf("since1 %d want 2", len(since1))
	}
	if since1[0].Revision != 2 || since1[1].Revision != 3 {
		t.Fatalf("revisions %v", since1)
	}
	// limit
	limited, _ := ss.ListSince(ctx, 0, 2)
	if len(limited) != 2 {
		t.Fatalf("limited %d want 2", len(limited))
	}
}

func TestSyncReconcileOutboundIncludesNewlyAppended(t *testing.T) {
	st := newTestStoreSync(t)
	ctx := context.Background()
	ss := syncpkg.NewSyncStore(st.Q())
	createDevice(t, st, "dev_a", "a", 0)
	createDevice(t, st, "dev_b", "b", 0)

	// dev_a creates 1
	_, err := ss.AppendChange(ctx, "dev_a", syncpkg.SyncChange{EntityType: "track", EntityID: "t1", Field: "title", Value: "v1", UpdatedAt: 1000})
	if err != nil {
		t.Fatal(err)
	}
	// dev_b syncs with since 0 and also sends its own change
	inbound := []syncpkg.SyncChange{{EntityType: "track", EntityID: "t2", Field: "title", Value: "v2", UpdatedAt: 1000, DeviceID: "dev_b"}}
	out, newRev, _, err := ss.Reconcile(ctx, "dev_b", 0, inbound)
	if err != nil {
		t.Fatal(err)
	}
	if newRev != 2 {
		t.Fatalf("newRev %d want 2", newRev)
	}
	if len(out) != 2 {
		t.Fatalf("out %d want 2 (both)", len(out))
	}
	// sinceRev should be respected: next reconcile with since 1 should only get rev 2
	out2, _, _, _ := ss.Reconcile(ctx, "dev_b", 1, nil)
	if len(out2) != 1 || out2[0].EntityID != "t2" {
		t.Fatalf("out2 %v", out2)
	}
}
