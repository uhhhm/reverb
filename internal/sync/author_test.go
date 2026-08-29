package sync_test

import (
	"context"
	"crypto/ed25519"
	"testing"

	syncpkg "github.com/uhhhm/reverb/internal/sync"
)

// AuthorDeviceID must return the local device, not the server device. Only the
// local device has a signing key, so a change authored under the server device
// goes out unsigned, is refused by every peer as unverifiable, and is resent on
// every anti-entropy round forever.
func TestAuthorDeviceIDPrefersLocal(t *testing.T) {
	ctx := context.Background()
	q := newTestStoreSync(t).Q()

	serverID, err := syncpkg.EnsureServerDevice(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	localID, err := syncpkg.EnsureLocalDevice(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	if serverID == localID {
		t.Fatal("test setup: server and local device are the same row")
	}

	got, err := syncpkg.AuthorDeviceID(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	if got != localID {
		t.Errorf("AuthorDeviceID = %s, want the local device %s (got the server device: %v)",
			got, localID, got == serverID)
	}
}

// The end-to-end property the bug violated: a change authored under the author
// identity is signed, and that signature verifies against the key peers hold.
func TestChangeAuthoredUnderAuthorIdentityIsVerifiable(t *testing.T) {
	ctx := context.Background()
	q := newTestStoreSync(t).Q()
	if _, err := syncpkg.EnsureServerDevice(ctx, q); err != nil {
		t.Fatal(err)
	}
	localID, err := syncpkg.EnsureLocalDevice(ctx, q)
	if err != nil {
		t.Fatal(err)
	}

	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	store := syncpkg.NewSyncStore(q)
	store.SetSigner(priv, localID)
	if err := store.RecordDeviceKey(ctx, localID, syncpkg.EncodePublicKey(pub)); err != nil {
		t.Fatal(err)
	}

	author, err := syncpkg.AuthorDeviceID(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendChange(ctx, author, syncpkg.SyncChange{
		EntityType: "track",
		EntityID:   "trk_1",
		Field:      "title",
		Value:      "Hello",
		UpdatedAt:  1000,
	}); err != nil {
		t.Fatal(err)
	}

	changes, err := store.ListSinceVector(ctx, nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 {
		t.Fatalf("got %d changes, want 1", len(changes))
	}
	if changes[0].Sig == "" {
		t.Fatal("change went out unsigned; a peer will refuse it as unverifiable forever")
	}
	if err := store.VerifyChangeAuthorship(ctx, changes[0]); err != nil {
		t.Fatalf("a peer would refuse this change: %v", err)
	}
}
