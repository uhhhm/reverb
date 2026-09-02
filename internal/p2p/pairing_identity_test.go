package p2p

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/network"

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

// The binding has to hold in both directions. A hostile responder that names
// the redeemer's own device ID would have its pushes read as self-authored:
// accepted unsigned, then signed with the redeemer's key on relay, so every
// other peer takes them for the victim's own changes.
func TestRedeemRefusesResponderClaimingOurDeviceID(t *testing.T) {
	ctx := context.Background()
	server, client := newLinkedHosts(t)

	cq := newSyncDB(t, "client.db")
	clientLocal, err := reverbsync.EnsureLocalDevice(ctx, cq)
	if err != nil {
		t.Fatal(err)
	}

	// A responder that hands back a valid-looking pairing but claims the
	// redeemer's identity for itself.
	server.SetStreamHandler("/reverb/pair/1.0.0", func(s network.Stream) {
		defer s.Close()
		var req pairRequest
		_ = json.NewDecoder(s).Decode(&req)
		_ = json.NewEncoder(s).Encode(pairResponse{
			DeviceID:     "minted-for-the-laptop",
			Token:        "token",
			PeerDeviceID: clientLocal,
		})
	})

	clientGuard := NewGuard(cq)
	_, _, err = RedeemViaPeer(ctx, client, clientGuard, cq,
		server.ID().String(), "123456", "desktop", clientLocal)
	if err == nil {
		t.Fatal("redeemer accepted a responder claiming its own device identity")
	}
	peers, perr := clientGuard.TrustedPeers(ctx)
	if perr != nil {
		t.Fatal(perr)
	}
	if len(peers) != 0 {
		t.Fatalf("the refused peer was trusted anyway: %v", peers)
	}
}

// Nor may a responder claim an identity already bound to a different peer:
// that peer's changes and this one's would be indistinguishable.
func TestRedeemRefusesResponderClaimingAnotherPeersDeviceID(t *testing.T) {
	ctx := context.Background()
	server, client := newLinkedHosts(t)

	cq := newSyncDB(t, "client.db")
	clientLocal, err := reverbsync.EnsureLocalDevice(ctx, cq)
	if err != nil {
		t.Fatal(err)
	}
	clientGuard := NewGuard(cq)

	// An unrelated peer already paired under its own device row.
	other, _ := newLinkedHosts(t)
	otherKey, err := PublicKeyBase64(other.ID())
	if err != nil {
		t.Fatal(err)
	}
	if err := RecordPeerDevice(ctx, cq, "phone-device", "phone", otherKey); err != nil {
		t.Fatal(err)
	}
	if err := clientGuard.Trust(ctx, other.ID(), "phone-device", "phone"); err != nil {
		t.Fatal(err)
	}

	server.SetStreamHandler("/reverb/pair/1.0.0", func(s network.Stream) {
		defer s.Close()
		var req pairRequest
		_ = json.NewDecoder(s).Decode(&req)
		_ = json.NewEncoder(s).Encode(pairResponse{
			DeviceID:     "minted-for-the-laptop",
			Token:        "token",
			PeerDeviceID: "phone-device",
		})
	})

	if _, _, err := RedeemViaPeer(ctx, client, clientGuard, cq,
		server.ID().String(), "123456", "desktop", clientLocal); err == nil {
		t.Fatal("redeemer accepted a responder claiming another peer's device identity")
	}
}
