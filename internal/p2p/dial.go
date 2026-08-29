package p2p

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/peerstore"
	"github.com/multiformats/go-multiaddr"
)

// dialTimeout bounds one connection attempt to a peer.
const dialTimeout = 15 * time.Second

// ParsePeerTarget accepts either a bare peer ID ("12D3Koo...") or a full
// multiaddr ending in /p2p/<peerID> ("/ip4/10.8.0.2/tcp/4331/p2p/12D3Koo..."),
// and returns the peer ID plus whatever addresses the input carried.
//
// Both forms are accepted because they suit different networks. On a LAN, mDNS
// has already put the peer in the peerstore and the ID alone is enough. Over a
// VPN it is not: multicast does not cross the tunnel, so the caller must supply
// the address, and the full multiaddr is how they do it.
func ParsePeerTarget(target string) (peer.AddrInfo, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return peer.AddrInfo{}, fmt.Errorf("empty peer target")
	}
	if target[0] != '/' {
		pid, err := peer.Decode(target)
		if err != nil {
			return peer.AddrInfo{}, fmt.Errorf("not a peer ID or multiaddr: %w", err)
		}
		return peer.AddrInfo{ID: pid}, nil
	}
	ma, err := multiaddr.NewMultiaddr(target)
	if err != nil {
		return peer.AddrInfo{}, err
	}
	pi, err := peer.AddrInfoFromP2pAddr(ma)
	if err != nil {
		return peer.AddrInfo{}, fmt.Errorf("multiaddr must end in /p2p/<peerID>: %w", err)
	}
	return *pi, nil
}

// EnsureConnected connects to pid if it is not already connected, trying the
// peerstore first and then the addresses guard has stored for it. It reports
// whether a usable connection exists afterwards.
//
// This is what keeps a paired peer reachable across restarts on a network where
// discovery does not work. The peerstore is filled by mDNS and the DHT only, so
// on a VPN it is empty and the stored addresses are the sole route back.
func EnsureConnected(ctx context.Context, h host.Host, guard *Guard, pid peer.ID) bool {
	if h == nil || pid == h.ID() {
		return false
	}
	if len(h.Network().ConnsToPeer(pid)) > 0 {
		return true
	}
	if dialPeer(ctx, h, peer.AddrInfo{ID: pid, Addrs: h.Peerstore().Addrs(pid)}) {
		return true
	}
	if guard == nil {
		return false
	}
	stored, err := guard.AddrsFor(ctx, pid)
	if err != nil || len(stored) == 0 {
		return false
	}
	pi := peer.AddrInfo{ID: pid}
	for _, raw := range stored {
		target, perr := ParsePeerTarget(raw)
		if perr != nil || target.ID != pid {
			continue
		}
		pi.Addrs = append(pi.Addrs, target.Addrs...)
	}
	return dialPeer(ctx, h, pi)
}

func dialPeer(ctx context.Context, h host.Host, pi peer.AddrInfo) bool {
	if len(pi.Addrs) == 0 {
		return false
	}
	dialCtx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()
	return h.Connect(dialCtx, pi) == nil
}

// SeedAddrs puts a peer's addresses into the peerstore so a later dial by peer
// ID alone resolves. PermanentAddrTTL is used deliberately: an address the user
// typed in, or one we stored from a successful pairing, should not expire out
// from under the background syncer the way a discovered address does.
func SeedAddrs(h host.Host, pi peer.AddrInfo) {
	if h == nil || len(pi.Addrs) == 0 {
		return
	}
	h.Peerstore().AddAddrs(pi.ID, pi.Addrs, peerstore.PermanentAddrTTL)
}

// ObservedAddrs returns the addresses of the live connections to pid, as full
// dial strings including /p2p/<peerID>. These are what gets persisted after a
// successful pairing or sync round, so the peer stays reachable once discovery
// is gone.
func ObservedAddrs(h host.Host, pid peer.ID) []string {
	if h == nil {
		return nil
	}
	suffix, err := multiaddr.NewMultiaddr("/p2p/" + pid.String())
	if err != nil {
		return nil
	}
	out := make([]string, 0, 2)
	for _, c := range h.Network().ConnsToPeer(pid) {
		ra := c.RemoteMultiaddr()
		if ra == nil || !isDialableAddr(ra) {
			continue
		}
		out = append(out, ra.Encapsulate(suffix).String())
	}
	return dedupeAddrs(out)
}
