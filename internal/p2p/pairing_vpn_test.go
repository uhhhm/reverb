package p2p

import (
	"context"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/uhhhm/reverb/internal/store/db"
)

// stubPairing is the minimal PairingService: it hands out one fixed code and
// accepts it once.
type stubPairing struct {
	code     string
	deviceID string
}

func (s *stubPairing) GenerateCode(context.Context) (string, int64, error) {
	return s.code, time.Now().Add(time.Minute).Unix(), nil
}

func (s *stubPairing) Redeem(_ context.Context, rawCode, _ string) (string, string, error) {
	if rawCode != s.code {
		return "", "", errWrongCode
	}
	return s.deviceID, "tok_" + s.deviceID, nil
}

var errWrongCode = errStr("invalid pairing code")

type errStr string

func (e errStr) Error() string { return string(e) }

// newIsolatedHost builds a host with no discovery at all: no mDNS, no DHT, no
// bootstrap peers. It stands in for a VPN, where mDNS multicast does not cross
// the tunnel and the DHT knows nothing of these hosts.
func newIsolatedHost(t *testing.T) host.Host {
	t.Helper()
	h, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	if err != nil {
		t.Fatalf("new host: %v", err)
	}
	t.Cleanup(func() { _ = h.Close() })
	return h
}

// The Phase 1 acceptance case: with discovery unavailable, pairing must still
// succeed when the user supplies the responder's full multiaddr, and must fail
// when given only its peer ID.
func TestRedeemViaPeerWithMultiaddrNoDiscovery(t *testing.T) {
	ctx := context.Background()
	server, client := newIsolatedHost(t), newIsolatedHost(t)

	serverQ := newTrustStore(t)
	if err := serverQ.CreateDevice(ctx, db.CreateDeviceParams{ID: "dev_b", Name: "b", TokenHash: "h"}); err != nil {
		t.Fatal(err)
	}
	pairing := &stubPairing{code: "AB12-CD34", deviceID: "dev_b"}
	RegisterPairingHandler(server, pairing, NewGuard(serverQ), serverQ, nil)

	clientGuard := NewGuard(newTrustStore(t))
	serverAddr := server.Addrs()[0].String() + "/p2p/" + server.ID().String()

	dialCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	deviceID, token, err := RedeemViaPeer(dialCtx, client, clientGuard, nil, serverAddr, "AB12-CD34", "laptop")
	if err != nil {
		t.Fatalf("RedeemViaPeer with multiaddr: %v", err)
	}
	if deviceID != "dev_b" || token != "tok_dev_b" {
		t.Fatalf("got (%q, %q), want (dev_b, tok_dev_b)", deviceID, token)
	}

	// The address must have been persisted, or the next restart is back to
	// having no way to reach this peer.
	stored, err := clientGuard.AddrsFor(ctx, server.ID())
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) == 0 {
		t.Fatal("pairing did not persist the peer address")
	}
}

func TestRedeemViaPeerBarePeerIDFailsWithoutDiscovery(t *testing.T) {
	ctx := context.Background()
	server, client := newIsolatedHost(t), newIsolatedHost(t)
	serverQ := newTrustStore(t)
	RegisterPairingHandler(server, &stubPairing{code: "AB12-CD34", deviceID: "dev_b"}, NewGuard(serverQ), serverQ, nil)

	dialCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, _, err := RedeemViaPeer(dialCtx, client, NewGuard(newTrustStore(t)), nil, server.ID().String(), "AB12-CD34", "laptop")
	if err == nil {
		t.Fatal("bare peer ID resolved without discovery; the multiaddr path is not being exercised")
	}
}

// EnsureConnected is what reconnects a paired peer after a restart, when the
// peerstore starts empty and only the stored address remains.
func TestEnsureConnectedUsesStoredAddrs(t *testing.T) {
	ctx := context.Background()
	server, client := newIsolatedHost(t), newIsolatedHost(t)
	guard := NewGuard(newTrustStore(t))
	if err := guard.Trust(ctx, server.ID(), "", "peer"); err != nil {
		t.Fatal(err)
	}
	addr := server.Addrs()[0].String() + "/p2p/" + server.ID().String()
	if err := guard.RememberAddrs(ctx, server.ID(), []string{addr}); err != nil {
		t.Fatal(err)
	}

	if EnsureConnected(ctx, client, nil, server.ID()) {
		t.Fatal("connected with an empty peerstore and no stored addresses")
	}
	if !EnsureConnected(ctx, client, guard, server.ID()) {
		t.Fatal("EnsureConnected did not fall back to the stored address")
	}
}
