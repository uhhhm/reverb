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
	n, err := receiver.RecoverInvalidRemoteChanges(ctx)
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
	if n, err := receiver.RecoverInvalidRemoteChanges(ctx); err != nil || n != 0 {
		t.Fatalf("repeat repair = %d, %v", n, err)
	}
	// A missing key is not evidence of corruption; preserve unverifiable unknown
	// authors until announcements arrive, and never quarantine unsigned history.
	createDevice(t, st, "unknown", "unknown", 0)
	if _, err := st.Q().AppendSyncChangeWithHLC(ctx, db.AppendSyncChangeWithHLCParams{DeviceID: "unknown", EntityType: "track", EntityID: "unknown", Field: "title", ValueJson: `"unknown"`, Sig: "invalid"}); err != nil {
		t.Fatal(err)
	}
	if n, err := receiver.RecoverInvalidRemoteChanges(ctx); err != nil || n != 0 {
		t.Fatalf("unknown key quarantined = %d, %v", n, err)
	}
}
