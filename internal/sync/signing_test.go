package sync_test

import (
	"context"
	"crypto/ed25519"
	"testing"

	syncpkg "github.com/uhhhm/reverb/internal/sync"
)

func newSignerPair(t *testing.T) (ed25519.PrivateKey, string) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	return priv, syncpkg.EncodePublicKey(pub)
}

func TestSignAndVerifyRoundTrip(t *testing.T) {
	priv, pub := newSignerPair(t)
	sig := syncpkg.SignChange(priv, "dev_a", "track", "t1", "title", `"hello"`, 100, 200, 3)
	if sig == "" {
		t.Fatal("empty signature")
	}
	if err := syncpkg.VerifyChange(pub, sig, "dev_a", "track", "t1", "title", `"hello"`, 100, 200, 3); err != nil {
		t.Fatalf("valid signature rejected: %v", err)
	}
}

// Every signed field must be covered: flipping any one of them must invalidate
// the signature, or a relaying peer could rewrite it in flight.
func TestVerifyRejectsAnyTamperedField(t *testing.T) {
	priv, pub := newSignerPair(t)
	sig := syncpkg.SignChange(priv, "dev_a", "track", "t1", "title", `"hello"`, 100, 200, 3)

	cases := []struct {
		name                     string
		dev, et, eid, field, val string
		updatedAt, hlc, seq      int64
	}{
		{"author", "dev_b", "track", "t1", "title", `"hello"`, 100, 200, 3},
		{"entityType", "dev_a", "album", "t1", "title", `"hello"`, 100, 200, 3},
		{"entityID", "dev_a", "track", "t2", "title", `"hello"`, 100, 200, 3},
		{"field", "dev_a", "track", "t1", "artist", `"hello"`, 100, 200, 3},
		{"value", "dev_a", "track", "t1", "title", `"goodbye"`, 100, 200, 3},
		{"updatedAt", "dev_a", "track", "t1", "title", `"hello"`, 101, 200, 3},
		{"hlc", "dev_a", "track", "t1", "title", `"hello"`, 100, 201, 3},
		{"seq", "dev_a", "track", "t1", "title", `"hello"`, 100, 200, 4},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := syncpkg.VerifyChange(pub, sig, c.dev, c.et, c.eid, c.field, c.val, c.updatedAt, c.hlc, c.seq)
			if err == nil {
				t.Fatalf("tampered %s verified", c.name)
			}
		})
	}
}

// Length prefixing must stop adjacent fields being re-split, e.g. entity "ab"
// + field "c" reading the same as entity "a" + field "bc".
func TestSigningPayloadIsUnambiguous(t *testing.T) {
	priv, pub := newSignerPair(t)
	sig := syncpkg.SignChange(priv, "dev_a", "track", "ab", "c", `1`, 1, 1, 1)
	if err := syncpkg.VerifyChange(pub, sig, "dev_a", "track", "a", "bc", `1`, 1, 1, 1); err == nil {
		t.Fatal("field boundary shift verified against the wrong split")
	}
}

func TestVerifyRejectsWrongKey(t *testing.T) {
	priv, _ := newSignerPair(t)
	_, otherPub := newSignerPair(t)
	sig := syncpkg.SignChange(priv, "dev_a", "track", "t1", "title", `"x"`, 1, 2, 3)
	if err := syncpkg.VerifyChange(otherPub, sig, "dev_a", "track", "t1", "title", `"x"`, 1, 2, 3); err == nil {
		t.Fatal("signature verified under an unrelated key")
	}
}

func TestVerifyRejectsMissingKeyOrSignature(t *testing.T) {
	priv, pub := newSignerPair(t)
	sig := syncpkg.SignChange(priv, "dev_a", "track", "t1", "title", `"x"`, 1, 2, 3)
	if err := syncpkg.VerifyChange("", sig, "dev_a", "track", "t1", "title", `"x"`, 1, 2, 3); err != syncpkg.ErrNoAuthorKey {
		t.Fatalf("want ErrNoAuthorKey, got %v", err)
	}
	if err := syncpkg.VerifyChange(pub, "", "dev_a", "track", "t1", "title", `"x"`, 1, 2, 3); err != syncpkg.ErrBadSignature {
		t.Fatalf("want ErrBadSignature, got %v", err)
	}
}

func TestSignChangeWithoutKeyReturnsEmpty(t *testing.T) {
	if got := syncpkg.SignChange(nil, "dev_a", "t", "1", "f", "null", 1, 1, 1); got != "" {
		t.Fatalf("want empty signature without a key, got %q", got)
	}
}

// A locally-authored change must come back out of the store carrying a
// signature other devices can verify.
func TestAppendChangeSignsLocallyAuthoredChange(t *testing.T) {
	ctx := context.Background()
	st := newTestStoreSync(t)
	ss := syncpkg.NewSyncStore(st.Q())
	createDevice(t, st, "dev_local", "local", 0)

	priv, pub := newSignerPair(t)
	ss.SetSigner(priv, "dev_local")
	if err := ss.RecordDeviceKey(ctx, "dev_local", pub); err != nil {
		t.Fatal(err)
	}

	if _, err := ss.AppendChange(ctx, "dev_local", syncpkg.SyncChange{
		EntityType: "track", EntityID: "t1", Field: "title", Value: "Song", UpdatedAt: 5000,
	}); err != nil {
		t.Fatal(err)
	}
	rows, err := ss.ListSince(ctx, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("want 1 change, got %d", len(rows))
	}
	if rows[0].Sig == "" {
		t.Fatal("locally-authored change was not signed")
	}
	if err := ss.VerifyChangeAuthorship(ctx, rows[0]); err != nil {
		t.Fatalf("own change failed verification: %v", err)
	}
}

// The point of signing: a change authored by dev_a verifies on a node that
// never paired with dev_a, as long as it knows dev_a's key.
func TestVerifyChangeAuthorshipAcceptsRelayedChange(t *testing.T) {
	ctx := context.Background()

	// Author node produces a signed change.
	authorStore := newTestStoreSync(t)
	authorSS := syncpkg.NewSyncStore(authorStore.Q())
	createDevice(t, authorStore, "dev_a", "a", 0)
	priv, pub := newSignerPair(t)
	authorSS.SetSigner(priv, "dev_a")
	if err := authorSS.RecordDeviceKey(ctx, "dev_a", pub); err != nil {
		t.Fatal(err)
	}
	if _, err := authorSS.AppendChange(ctx, "dev_a", syncpkg.SyncChange{
		EntityType: "track", EntityID: "t1", Field: "title", Value: "Song", UpdatedAt: 5000,
	}); err != nil {
		t.Fatal(err)
	}
	changes, err := authorSS.ListSince(ctx, 0, 10)
	if err != nil {
		t.Fatal(err)
	}

	// A third node that knows dev_a's key but never paired with it.
	relayTarget := newTestStoreSync(t)
	targetSS := syncpkg.NewSyncStore(relayTarget.Q())
	createDevice(t, relayTarget, "dev_a", "a", 0)
	if err := targetSS.RecordDeviceKey(ctx, "dev_a", pub); err != nil {
		t.Fatal(err)
	}
	if err := targetSS.VerifyChangeAuthorship(ctx, changes[0]); err != nil {
		t.Fatalf("relayed signed change rejected: %v", err)
	}

	// The same change with its value rewritten must not verify.
	forged := changes[0]
	forged.ValueJSON = `"Rewritten"`
	if err := targetSS.VerifyChangeAuthorship(ctx, forged); err == nil {
		t.Fatal("tampered relayed change verified")
	}
}

func TestRecordDeviceKeyRefusesRebind(t *testing.T) {
	ctx := context.Background()
	st := newTestStoreSync(t)
	ss := syncpkg.NewSyncStore(st.Q())
	createDevice(t, st, "dev_a", "a", 0)

	_, pub1 := newSignerPair(t)
	_, pub2 := newSignerPair(t)
	if err := ss.RecordDeviceKey(ctx, "dev_a", pub1); err != nil {
		t.Fatal(err)
	}
	// Re-recording the same key is a no-op, not an error.
	if err := ss.RecordDeviceKey(ctx, "dev_a", pub1); err != nil {
		t.Fatalf("idempotent rebind failed: %v", err)
	}
	// A different key is identity takeover and must be refused.
	if err := ss.RecordDeviceKey(ctx, "dev_a", pub2); err != syncpkg.ErrKeyConflict {
		t.Fatalf("want ErrKeyConflict, got %v", err)
	}
	if got := ss.PublicKeyFor(ctx, "dev_a"); got != pub1 {
		t.Fatal("stored key was replaced")
	}
}
