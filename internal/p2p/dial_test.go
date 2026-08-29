package p2p

import (
	"context"
	"testing"
)

func TestParsePeerTargetBareID(t *testing.T) {
	pi, err := ParsePeerTarget(peerA)
	if err != nil {
		t.Fatalf("ParsePeerTarget: %v", err)
	}
	if pi.ID.String() != peerA {
		t.Errorf("ID = %s, want %s", pi.ID, peerA)
	}
	if len(pi.Addrs) != 0 {
		t.Errorf("Addrs = %v, want none", pi.Addrs)
	}
}

func TestParsePeerTargetMultiaddr(t *testing.T) {
	pi, err := ParsePeerTarget("/ip4/10.8.0.2/tcp/4331/p2p/" + peerA)
	if err != nil {
		t.Fatalf("ParsePeerTarget: %v", err)
	}
	if pi.ID.String() != peerA {
		t.Errorf("ID = %s, want %s", pi.ID, peerA)
	}
	if len(pi.Addrs) != 1 || pi.Addrs[0].String() != "/ip4/10.8.0.2/tcp/4331" {
		t.Errorf("Addrs = %v, want [/ip4/10.8.0.2/tcp/4331]", pi.Addrs)
	}
}

func TestParsePeerTargetRejectsGarbage(t *testing.T) {
	for _, in := range []string{
		"",
		"not-a-peer-id",
		"/ip4/10.8.0.2/tcp/4331", // no /p2p component, so no peer to dial
		"/nonsense/1",
	} {
		if _, err := ParsePeerTarget(in); err == nil {
			t.Errorf("ParsePeerTarget(%q) = nil error, want an error", in)
		}
	}
}

// A paired peer must stay reachable when discovery is dead, which is the whole
// point of persisting addresses: over a VPN neither mDNS nor the DHT ever fills
// the peerstore, so the stored address is the only route back.
func TestRememberAddrsRoundTrip(t *testing.T) {
	ctx := context.Background()
	q := newTrustStore(t)
	g := NewGuard(q)
	pid := mustPeerID(t, peerA)
	addr := "/ip4/10.8.0.2/tcp/4331/p2p/" + peerA

	if err := g.Trust(ctx, pid, "", "vpn peer"); err != nil {
		t.Fatal(err)
	}
	if err := g.RememberAddrs(ctx, pid, []string{addr}); err != nil {
		t.Fatal(err)
	}
	got, err := g.AddrsFor(ctx, pid)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != addr {
		t.Fatalf("AddrsFor = %v, want [%s]", got, addr)
	}
}

// Re-pairing an already-known peer must not wipe the addresses that are how we
// reach it: TrustPeer's upsert deliberately leaves the column alone.
func TestTrustPreservesStoredAddrs(t *testing.T) {
	ctx := context.Background()
	q := newTrustStore(t)
	g := NewGuard(q)
	pid := mustPeerID(t, peerA)
	addr := "/ip4/10.8.0.2/tcp/4331/p2p/" + peerA

	if err := g.Trust(ctx, pid, "", "vpn peer"); err != nil {
		t.Fatal(err)
	}
	if err := g.RememberAddrs(ctx, pid, []string{addr}); err != nil {
		t.Fatal(err)
	}
	if err := g.Trust(ctx, pid, "", "vpn peer renamed"); err != nil {
		t.Fatal(err)
	}
	got, _ := g.AddrsFor(ctx, pid)
	if len(got) != 1 || got[0] != addr {
		t.Fatalf("AddrsFor after re-trust = %v, want [%s]", got, addr)
	}
}

// Newly observed addresses go in front of older ones, and the list is bounded
// so a peer that roams between networks cannot grow the row without limit.
func TestRememberAddrsOrdersAndBounds(t *testing.T) {
	ctx := context.Background()
	q := newTrustStore(t)
	g := NewGuard(q)
	pid := mustPeerID(t, peerA)
	if err := g.Trust(ctx, pid, "", "peer"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < maxStoredAddrs+4; i++ {
		a := "/ip4/10.8.0." + string(rune('a'+i)) + "/tcp/4331/p2p/" + peerA
		if err := g.RememberAddrs(ctx, pid, []string{a}); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := g.AddrsFor(ctx, pid)
	if len(got) != maxStoredAddrs {
		t.Fatalf("len(AddrsFor) = %d, want %d", len(got), maxStoredAddrs)
	}
	newest := "/ip4/10.8.0." + string(rune('a'+maxStoredAddrs+3)) + "/tcp/4331/p2p/" + peerA
	if got[0] != newest {
		t.Errorf("AddrsFor[0] = %s, want the most recent %s", got[0], newest)
	}
}

func TestAddrsForUnknownPeerIsEmpty(t *testing.T) {
	g := NewGuard(newTrustStore(t))
	got, err := g.AddrsFor(context.Background(), mustPeerID(t, peerB))
	if err != nil {
		t.Fatalf("AddrsFor: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("AddrsFor = %v, want empty", got)
	}
}
