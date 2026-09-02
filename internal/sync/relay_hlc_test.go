package sync_test

import (
	"context"
	"testing"
	"time"

	syncpkg "github.com/uhhhm/reverb/internal/sync"
)

// authoredChange appends one change on its own store and returns it exactly as
// the wire would carry it: signed, with the author's HLC and seq.
func authoredChange(t *testing.T, ss *syncpkg.SyncStore, deviceID string, ch syncpkg.SyncChange, want int) syncpkg.SyncChange {
	t.Helper()
	ctx := context.Background()
	if _, err := ss.AppendChange(ctx, deviceID, ch); err != nil {
		t.Fatalf("append: %v", err)
	}
	rows, err := ss.ListSince(ctx, 0, 100)
	if err != nil {
		t.Fatalf("ListSince: %v", err)
	}
	if len(rows) != want {
		t.Fatalf("author store holds %d change(s), want %d", len(rows), want)
	}
	return rows[want-1]
}

// newAuthor returns a store that signs as deviceID, plus the public key.
func newAuthor(t *testing.T, deviceID string) (*syncpkg.SyncStore, string) {
	t.Helper()
	st := newTestStoreSync(t)
	ss := syncpkg.NewSyncStore(st.Q())
	createDevice(t, st, deviceID, deviceID, 0)
	priv, pub := newSignerPair(t)
	ss.SetSigner(priv, deviceID)
	if err := ss.RecordDeviceKey(context.Background(), deviceID, pub); err != nil {
		t.Fatal(err)
	}
	return ss, pub
}

// advanceClock pushes a store's HLC past wallMillis by authoring a local edit
// dated then, which is what a receiving device's own recent activity does.
func advanceClock(t *testing.T, ss *syncpkg.SyncStore, deviceID string, wallMillis int64) {
	t.Helper()
	if _, err := ss.AppendChange(context.Background(), deviceID, syncpkg.SyncChange{
		EntityType: "track", EntityID: "cat_local", Field: "title",
		Value: "Local edit", UpdatedAt: wallMillis,
	}); err != nil {
		t.Fatalf("local edit: %v", err)
	}
}

func latestTitle(t *testing.T, ss *syncpkg.SyncStore, entityID string) string {
	t.Helper()
	ch, err := ss.GetLatestForField(context.Background(), "track", entityID, "title")
	if err != nil {
		t.Fatalf("GetLatestForField: %v", err)
	}
	if ch == nil {
		t.Fatal("no stored change for title")
	}
	s, _ := ch.Value.(string)
	return s
}

// A relayed change must be stored with the HLC its author signed. Raising it to
// the receiver's clock breaks the signature, so the next device in the chain
// drops the change and three-device sync never converges.
func TestReconcileKeepsAuthorHLCSoRelayVerifies(t *testing.T) {
	ctx := context.Background()
	authorSS, authorPub := newAuthor(t, "dev_a")

	base := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC).UnixMilli()
	sent := authoredChange(t, authorSS, "dev_a", syncpkg.SyncChange{
		EntityType: "track", EntityID: "cat_1", Field: "title",
		Value: "Original", UpdatedAt: base,
	}, 1)
	if sent.Sig == "" {
		t.Fatal("author produced an unsigned change")
	}

	// B is a device whose own clock is well ahead of the change it receives.
	relayStore := newTestStoreSync(t)
	relaySS := syncpkg.NewSyncStore(relayStore.Q())
	createDevice(t, relayStore, "dev_a", "a", 0)
	createDevice(t, relayStore, "dev_b", "b", 0)
	if err := relaySS.RecordDeviceKey(ctx, "dev_a", authorPub); err != nil {
		t.Fatal(err)
	}
	advanceClock(t, relaySS, "dev_b", base+2*60*1000)

	if _, _, rejected, err := relaySS.Reconcile(ctx, "dev_a", syncpkg.NoOutbound, []syncpkg.SyncChange{sent}); err != nil {
		t.Fatalf("Reconcile: %v", err)
	} else if len(rejected) != 0 {
		t.Fatalf("relay rejected the change: %+v", rejected)
	}

	stored := storedChangeFor(t, relaySS, "dev_a", "cat_1")
	if stored.HLC != sent.HLC {
		t.Fatalf("relay rewrote the HLC: stored %d, author signed %d", stored.HLC, sent.HLC)
	}
	if err := relaySS.VerifyChangeAuthorship(ctx, stored); err != nil {
		t.Fatalf("stored change no longer verifies: %v", err)
	}

	// C accepts it only because B kept the row the author signed.
	thirdStore := newTestStoreSync(t)
	thirdSS := syncpkg.NewSyncStore(thirdStore.Q())
	createDevice(t, thirdStore, "dev_a", "a", 0)
	createDevice(t, thirdStore, "dev_b", "b", 0)
	createDevice(t, thirdStore, "dev_c", "c", 0)
	if err := thirdSS.RecordDeviceKey(ctx, "dev_a", authorPub); err != nil {
		t.Fatal(err)
	}
	if err := thirdSS.VerifyChangeAuthorship(ctx, stored); err != nil {
		t.Fatalf("third device refused the relayed change: %v", err)
	}
	if _, _, rejected, err := thirdSS.Reconcile(ctx, "dev_b", syncpkg.NoOutbound, []syncpkg.SyncChange{stored}); err != nil {
		t.Fatalf("third Reconcile: %v", err)
	} else if len(rejected) != 0 {
		t.Fatalf("third device rejected the relayed change: %+v", rejected)
	}
	if got := latestTitle(t, thirdSS, "cat_1"); got != "Original" {
		t.Fatalf("third device holds %q, want %q", got, "Original")
	}
}

// The author's second edit must still win on the receiver. Storing the first
// one under the receiver's own later clock made the second look stale, so the
// two devices disagreed forever, each resending its version every round.
func TestReconcileLaterEditFromSameAuthorWins(t *testing.T) {
	authorSS, authorPub := newAuthor(t, "dev_a")

	base := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC).UnixMilli()
	v1 := authoredChange(t, authorSS, "dev_a", syncpkg.SyncChange{
		EntityType: "track", EntityID: "cat_1", Field: "title",
		Value: "First", UpdatedAt: base,
	}, 1)
	v2 := authoredChange(t, authorSS, "dev_a", syncpkg.SyncChange{
		EntityType: "track", EntityID: "cat_1", Field: "title",
		Value: "Second", UpdatedAt: base + 60*1000,
	}, 2)
	if v2.HLC <= v1.HLC {
		t.Fatalf("author HLC did not advance: %d then %d", v1.HLC, v2.HLC)
	}

	// Two rounds: the receiver edited something of its own in between, so its
	// clock is past both of the author's edits when the first one lands.
	t.Run("across rounds", func(t *testing.T) {
		ss := receiverWithKey(t, authorPub)
		advanceClock(t, ss, "dev_b", base+2*60*1000)
		reconcileAll(t, ss, v1)
		reconcileAll(t, ss, v2)
		if got := latestTitle(t, ss, "cat_1"); got != "Second" {
			t.Fatalf("receiver kept %q, want %q", got, "Second")
		}
	})

	// Both edits in one batch behave no differently: the receiver's clock was
	// stamped onto the first row before the second was even looked at.
	t.Run("same batch", func(t *testing.T) {
		ss := receiverWithKey(t, authorPub)
		advanceClock(t, ss, "dev_b", base+2*60*1000)
		reconcileAll(t, ss, v1, v2)
		if got := latestTitle(t, ss, "cat_1"); got != "Second" {
			t.Fatalf("receiver kept %q, want %q", got, "Second")
		}
	})
}

func receiverWithKey(t *testing.T, authorPub string) *syncpkg.SyncStore {
	t.Helper()
	st := newTestStoreSync(t)
	ss := syncpkg.NewSyncStore(st.Q())
	createDevice(t, st, "dev_a", "a", 0)
	createDevice(t, st, "dev_b", "b", 0)
	if err := ss.RecordDeviceKey(context.Background(), "dev_a", authorPub); err != nil {
		t.Fatal(err)
	}
	return ss
}

func reconcileAll(t *testing.T, ss *syncpkg.SyncStore, changes ...syncpkg.SyncChange) {
	t.Helper()
	_, _, rejected, err := ss.Reconcile(context.Background(), "dev_a", syncpkg.NoOutbound, changes)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(rejected) != 0 {
		t.Fatalf("rejected %d change(s): %+v", len(rejected), rejected)
	}
}

// storedChangeFor reads back the row a device stored for one author's entity.
func storedChangeFor(t *testing.T, ss *syncpkg.SyncStore, deviceID, entityID string) syncpkg.SyncChange {
	t.Helper()
	rows, err := ss.ListSince(context.Background(), 0, 100)
	if err != nil {
		t.Fatalf("ListSince: %v", err)
	}
	for _, r := range rows {
		if r.DeviceID == deviceID && r.EntityID == entityID {
			return r
		}
	}
	t.Fatalf("no stored change from %s for %s", deviceID, entityID)
	return syncpkg.SyncChange{}
}
