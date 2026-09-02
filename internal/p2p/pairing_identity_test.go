package p2p

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/uhhhm/reverb/internal/store"
	"github.com/uhhhm/reverb/internal/store/db"
	reverbsync "github.com/uhhhm/reverb/internal/sync"
)

func newSyncDB(t *testing.T, name string) *db.Queries {
	t.Helper()
	st, err := store.Open(t.TempDir() + "/" + name)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	return st.Q()
}

// A redeeming device keeps syncing under its own local device ID. If pairing
// binds the peer to a freshly minted row instead, every sync round that device
// pushes is refused as a mismatched identity and its half of replication is
// silently dead.
func TestPairingBindsRedeemersOwnDeviceID(t *testing.T) {
	ctx := context.Background()
	server, client := newLinkedHosts(t)

	sq := newSyncDB(t, "server.db")
	pairing := reverbsync.NewPairingService(sq)
	code, _, err := pairing.GenerateCode(ctx)
	if err != nil {
		t.Fatal(err)
	}
	serverGuard := NewGuard(sq)
	RegisterPairingHandler(server, pairing, serverGuard, sq, func(c context.Context) (string, error) {
		return reverbsync.EnsureLocalDevice(c, sq)
	})
	RegisterSyncHandler(server, reverbsync.NewSyncStore(sq), serverGuard, sq)

	cq := newSyncDB(t, "client.db")
	clientLocal, err := reverbsync.EnsureLocalDevice(ctx, cq)
	if err != nil {
		t.Fatal(err)
	}

	deviceID, token, err := RedeemViaPeer(ctx, client, NewGuard(cq), cq,
		server.ID().String(), code, "laptop", clientLocal)
	if err != nil {
		t.Fatalf("RedeemViaPeer: %v", err)
	}
	if deviceID != clientLocal {
		t.Fatalf("pairing bound %q, but this device authors as %q", deviceID, clientLocal)
	}
	if token == "" {
		t.Fatal("pairing returned no sync token")
	}

	// The sync round the syncer performs, under the same local ID.
	s, err := client.NewStream(ctx, server.ID(), "/reverb/sync/1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_ = s.SetDeadline(time.Now().Add(10 * time.Second))
	if err := json.NewEncoder(s).Encode(reverbsync.SyncRequest{DeviceID: clientLocal}); err != nil {
		t.Fatal(err)
	}
	_ = s.CloseWrite()
	var resp reverbsync.SyncResponse
	if err := json.NewDecoder(s).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Error != "" {
		t.Fatalf("sync round after pairing was refused: %s", resp.Error)
	}
}

// An older peer sends no device ID; pairing must still mint one for it.
func TestPairingMintsDeviceIDWhenPeerOffersNone(t *testing.T) {
	ctx := context.Background()
	server, client := newLinkedHosts(t)

	sq := newSyncDB(t, "server.db")
	pairing := reverbsync.NewPairingService(sq)
	code, _, err := pairing.GenerateCode(ctx)
	if err != nil {
		t.Fatal(err)
	}
	RegisterPairingHandler(server, pairing, NewGuard(sq), sq, nil)

	cq := newSyncDB(t, "client.db")
	deviceID, token, err := RedeemViaPeer(ctx, client, NewGuard(cq), cq,
		server.ID().String(), code, "laptop", "")
	if err != nil {
		t.Fatalf("RedeemViaPeer: %v", err)
	}
	if deviceID == "" || token == "" {
		t.Fatalf("redeem without a device ID returned (%q, %q)", deviceID, token)
	}
}

// Device IDs are not secret -- they travel in the author field of every change
// -- so a code holder must not be able to pair into this node's own identity
// and have its changes read as ours.
func TestPairingRefusesLocalDeviceID(t *testing.T) {
	ctx := context.Background()
	server, client := newLinkedHosts(t)

	sq := newSyncDB(t, "server.db")
	pairing := reverbsync.NewPairingService(sq)
	code, _, err := pairing.GenerateCode(ctx)
	if err != nil {
		t.Fatal(err)
	}
	serverLocal, err := reverbsync.EnsureLocalDevice(ctx, sq)
	if err != nil {
		t.Fatal(err)
	}
	RegisterPairingHandler(server, pairing, NewGuard(sq), sq, func(c context.Context) (string, error) {
		return serverLocal, nil
	})

	before, err := sq.GetDeviceByID(ctx, serverLocal)
	if err != nil {
		t.Fatal(err)
	}

	cq := newSyncDB(t, "client.db")
	_, _, err = RedeemViaPeer(ctx, client, NewGuard(cq), cq,
		server.ID().String(), code, "impostor", serverLocal)
	if err == nil {
		t.Fatal("a peer paired into the responder's own device identity")
	}
	dev, gerr := sq.GetDeviceByID(ctx, serverLocal)
	if gerr != nil {
		t.Fatal(gerr)
	}
	if dev.TokenHash != before.TokenHash {
		t.Fatal("the refused pairing still rewrote the local device row")
	}
}
