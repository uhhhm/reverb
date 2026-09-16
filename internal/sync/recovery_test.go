package sync_test

import (
	"context"
	"testing"

	"github.com/uhhhm/reverb/internal/store/db"
	syncpkg "github.com/uhhhm/reverb/internal/sync"
)

func TestRecoveryFetchesAuthenticCopyOfCorruptRemoteChange(t *testing.T) {
	ctx := context.Background()
	author, pub := newAuthor(t, "author")
	original := authoredChange(t, author, "author", syncpkg.SyncChange{EntityType: "track", EntityID: "track", Field: "title", Value: "Authentic", UpdatedAt: 1000}, 1)
	st := newTestStoreSync(t)
	createDevice(t, st, "author", "author", 0)
	createDevice(t, st, "local", "local", 0)
	receiver := syncpkg.NewSyncStore(st.Q())
	priv, _ := newSignerPair(t)
	receiver.SetSigner(priv, "local")
	if err := receiver.RecordDeviceKey(ctx, "author", pub); err != nil {
		t.Fatal(err)
	}
	// Recreate an old receiver that altered the signed HLC after validation.
	corrupt := original
	corrupt.HLC++
	if _, _, _, err := receiver.Reconcile(ctx, "author", 0, []syncpkg.SyncChange{corrupt}); err != nil {
		t.Fatal(err)
	}
	if err := receiver.VerifyChangeAuthorship(ctx, storedChangeFor(t, receiver, "author", "track")); err == nil {
		t.Fatal("fixture must reproduce unverifiable change")
	}
	n, err := receiver.RecoverUnusableChanges(ctx)
	if err != nil || n != 1 {
		t.Fatalf("recovery = %d, %v", n, err)
	}
	vec, _, err := receiver.GetVectorMap(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if vec["author"] != 0 {
		t.Fatalf("vector still acknowledges corrupt row: %v", vec)
	}
	var saved int
	if err := st.Q().UnderlyingDB().QueryRowContext(ctx, "SELECT count(*) FROM sync_quarantine").Scan(&saved); err != nil || saved != 1 {
		t.Fatalf("quarantine = %d, %v", saved, err)
	}
	resent, err := author.ListSinceVector(ctx, vec, 100)
	if err != nil {
		t.Fatal(err)
	}
	accepted, refused := receiver.AuthorizeInbound(ctx, "author", resent)
	if len(accepted) != 1 || len(refused) != 0 {
		t.Fatal("authentic resend did not verify")
	}
	if _, _, _, err := receiver.Reconcile(ctx, "author", 0, accepted); err != nil {
		t.Fatal(err)
	}
	if err := receiver.VerifyChangeAuthorship(ctx, storedChangeFor(t, receiver, "author", "track")); err != nil {
		t.Fatal(err)
	}
	if n, err := receiver.RecoverUnusableChanges(ctx); err != nil || n != 0 {
		t.Fatalf("repeat repair = %d, %v", n, err)
	}
	// A missing key is not evidence of corruption; preserve unverifiable unknown
	// authors until announcements arrive, and never quarantine unsigned history.
	createDevice(t, st, "unknown", "unknown", 0)
	if _, err := st.Q().AppendSyncChangeWithHLC(ctx, db.AppendSyncChangeWithHLCParams{DeviceID: "unknown", EntityType: "track", EntityID: "unknown", Field: "title", ValueJson: `"unknown"`, Sig: "invalid"}); err != nil {
		t.Fatal(err)
	}
	if n, err := receiver.RecoverUnusableChanges(ctx); err != nil || n != 0 {
		t.Fatalf("unknown key quarantined = %d, %v", n, err)
	}
}

// A build that predates the boundary check stored values that do not parse.
// They can never project, so startup recovery drops them and clears their
// pending projection -- otherwise the retry loop reports the same failure every
// 30 seconds forever.
func TestRecoveryDropsUnreadableStoredValueAndClearsItsPendingProjection(t *testing.T) {
	ctx := context.Background()
	st := newTestStoreSync(t)
	createDevice(t, st, "peer", "peer", 0)
	createDevice(t, st, "local", "local", 0)
	receiver := syncpkg.NewSyncStore(st.Q())
	priv, _ := newSignerPair(t)
	receiver.SetSigner(priv, "local")

	rev, err := st.Q().AppendSyncChangeWithHLC(ctx, db.AppendSyncChangeWithHLCParams{
		DeviceID: "peer", EntityType: "play", EntityID: "play_bad", Field: "record",
		ValueJson: `{"catalogId":"trk"`, UpdatedAt: 1000,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Q().MarkSyncProjectionPending(ctx, rev); err != nil {
		t.Fatal(err)
	}
	if err := receiver.SetVector(ctx, "peer", 5, 5); err != nil {
		t.Fatal(err)
	}

	// A row that is not a play record keeps the raw-string fallback, so it is
	// still readable and must survive.
	readable, err := st.Q().AppendSyncChangeWithHLC(ctx, db.AppendSyncChangeWithHLCParams{
		DeviceID: "peer", EntityType: "track", EntityID: "trk_legacy", Field: "title",
		ValueJson: "Renamed", UpdatedAt: 1000,
	})
	if err != nil {
		t.Fatal(err)
	}

	// An empty value is how a legacy row holds a nil value, and it reads back
	// fine, so recovery must leave it alone.
	keep, err := st.Q().AppendSyncChangeWithHLC(ctx, db.AppendSyncChangeWithHLCParams{
		DeviceID: "peer", EntityType: "track", EntityID: "trk_nil", Field: "title", UpdatedAt: 1000,
	})
	if err != nil {
		t.Fatal(err)
	}

	n, err := receiver.RecoverUnusableChanges(ctx)
	if err != nil || n != 1 {
		t.Fatalf("recovery = %d, %v", n, err)
	}
	for _, row := range []struct {
		revision int64
		why      string
	}{
		{keep, "a row whose value is empty, not malformed"},
		{readable, "a legacy unquoted title, which unmarshalValue still reads as a string"},
	} {
		var kept int
		if err := st.Q().UnderlyingDB().QueryRowContext(ctx, "SELECT count(*) FROM sync_change WHERE revision = ?", row.revision).Scan(&kept); err != nil {
			t.Fatal(err)
		}
		if kept != 1 {
			t.Fatalf("%s was removed from the log", row.why)
		}
	}
	if err := receiver.RecoverProjection(ctx); err != nil {
		t.Fatalf("projection recovery still reports a failure: %v", err)
	}
	pending, err := st.Q().ListPendingSyncProjections(ctx, db.ListPendingSyncProjectionsParams{Revision: 0, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Fatalf("pending projections = %d, want the queue cleared", len(pending))
	}
	var quarantined int
	if err := st.Q().UnderlyingDB().QueryRowContext(ctx, "SELECT count(*) FROM sync_quarantine WHERE reason = 'value is not valid JSON'").Scan(&quarantined); err != nil || quarantined != 1 {
		t.Fatalf("quarantined = %d, %v", quarantined, err)
	}
	// The peer's vector is untouched: there is no authentic copy to ask for.
	vec, _, err := receiver.GetVectorMap(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if vec["peer"] != 5 {
		t.Fatalf("peer vector = %d, want it left at 5: there is no authentic copy to refetch", vec["peer"])
	}
}
