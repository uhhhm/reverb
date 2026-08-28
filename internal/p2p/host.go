package p2p

import (
	"context"
	"fmt"
	"log"
	"sync/atomic"
	"time"

	"github.com/libp2p/go-libp2p"
	dht "github.com/libp2p/go-libp2p-kad-dht"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/p2p/discovery/mdns"
	"github.com/libp2p/go-libp2p/p2p/net/connmgr"
	"github.com/multiformats/go-multiaddr"
)

// Host wraps a libp2p host with discovery.
type Host struct {
	h      host.Host
	d      *dht.IpfsDHT
	mdns   mdns.Service
	closed atomic.Bool
}

// discoveryNotifee is a minimal mdns notifee that connects to discovered peers.
type discoveryNotifee struct {
	h host.Host
}

func (n *discoveryNotifee) HandlePeerFound(pi peer.AddrInfo) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = n.h.Connect(ctx, pi)
}

// mdnsTag is the service tag for local discovery.
const mdnsTag = "_reverb._tcp"

// NewHost creates a libp2p host listening on random ports, with mDNS and DHT.
// priv is this node's persistent identity; it must be stable across restarts or
// every pairing bound to the resulting peer ID is invalidated. See
// LoadOrCreateIdentity.
func NewHost(ctx context.Context, priv crypto.PrivKey) (*Host, error) {
	cm, err := connmgr.NewConnManager(10, 50)
	if err != nil {
		return nil, fmt.Errorf("connmgr: %w", err)
	}
	opts := []libp2p.Option{
		libp2p.ListenAddrStrings(
			"/ip4/0.0.0.0/tcp/0",
			"/ip4/0.0.0.0/udp/0/quic-v1",
			"/ip6/::/tcp/0",
		),
		libp2p.ConnectionManager(cm),
		libp2p.EnableNATService(),
		libp2p.EnableRelay(),
		libp2p.EnableHolePunching(),
	}
	if priv != nil {
		opts = append(opts, libp2p.Identity(priv))
	}
	h, err := libp2p.New(opts...)
	if err != nil {
		return nil, err
	}
	// DHT for WAN discovery (client mode so LAN-only works without bootstrap).
	d, err := dht.New(ctx, h, dht.Mode(dht.ModeClient))
	if err != nil {
		_ = h.Close()
		return nil, fmt.Errorf("dht: %w", err)
	}
	// Best-effort bootstrap in background; don't block NewHost.
	go func() {
		ctx2, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = d.Bootstrap(ctx2)
	}()

	// mDNS for LAN discovery. TXT advertisement of id/hlc/lanPort is not yet
	// implemented (plan 305) — discovery is peerId-only for now.
	ser := mdns.NewMdnsService(h, mdnsTag, &discoveryNotifee{h: h})
	if err := ser.Start(); err != nil {
		// mDNS may fail on some hosts (no multicast route) — log but don't fail.
		log.Printf("WARNING: p2p mdns start failed: %v", err)
	}

	// Set stream handler for /reverb/sync/1.0.0 and /reverb/file/1.0.0 — no-ops for now,
	// concrete handlers registered by Syncer/FileSyncer.
	h.SetStreamHandler("/reverb/sync/1.0.0", func(s network.Stream) { s.Close() })
	h.SetStreamHandler("/reverb/pair/1.0.0", func(s network.Stream) { s.Close() })
	h.SetStreamHandler("/reverb/file/1.0.0", func(s network.Stream) { s.Close() })

	return &Host{h: h, d: d, mdns: ser}, nil
}

func (h *Host) Close() error {
	if h.closed.Swap(true) {
		return nil
	}
	if h.mdns != nil {
		_ = h.mdns.Close()
	}
	if h.d != nil {
		_ = h.d.Close()
	}
	return h.h.Close()
}

func (h *Host) Closed() bool { return h.closed.Load() }

// Addrs returns the host's listen multiaddrs as strings.
func (h *Host) Addrs() []string {
	if h.h == nil {
		return nil
	}
	addrs := h.h.Addrs()
	out := make([]string, 0, len(addrs))
	for _, a := range addrs {
		out = append(out, a.String())
	}
	return out
}

// ID returns the peer ID string.
func (h *Host) ID() string {
	if h.h == nil || h.h.ID() == "" {
		return "stub-peer-id"
	}
	return h.h.ID().String()
}

// Host returns the underlying libp2p host (for advanced use).
func (h *Host) LibHost() host.Host { return h.h }

// Connect dials a peer by multiaddr string (for manual relay addrs).
func (h *Host) Connect(ctx context.Context, addr string) error {
	ma, err := multiaddr.NewMultiaddr(addr)
	if err != nil {
		return err
	}
	pi, err := peer.AddrInfoFromP2pAddr(ma)
	if err != nil {
		return err
	}
	return h.h.Connect(ctx, *pi)
}

func (h *Host) String() string {
	if h.h == nil {
		return fmt.Sprintf("p2p.Host{closed=%v}", h.Closed())
	}
	return fmt.Sprintf("p2p.Host{id=%s closed=%v}", h.ID()[:8], h.Closed())
}
