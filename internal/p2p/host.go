package p2p

import (
	"context"
	"fmt"
	"log"
	"net"
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

// NewHost creates a libp2p host with mDNS and DHT, listening on port (0 picks a
// random one). A fixed port is what makes a manually entered peer address
// survive a restart; with a random port any address written down goes stale the
// moment the process restarts. If the requested port is already taken -- two Reverb instances on
// one machine, most likely -- it falls back to a random port rather than failing
// to start, since a reachable-but-unstable host beats no host at all.
//
// priv is this node's persistent identity; it must be stable across restarts or
// every pairing bound to the resulting peer ID is invalidated. See
// LoadOrCreateIdentity.
func NewHost(ctx context.Context, priv crypto.PrivKey, port int) (*Host, error) {
	h, err := newLibp2pHost(priv, port)
	if err != nil && port != 0 {
		log.Printf("WARNING: p2p listen on port %d failed (%v); falling back to a random port. "+
			"Manually entered peer addresses will go stale on restart until the conflict is resolved.", port, err)
		h, err = newLibp2pHost(priv, 0)
	}
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

	// mDNS for LAN discovery. It does not traverse a VPN -- multicast is not
	// forwarded over WireGuard or Tailscale -- so peers on a VPN are reached by
	// stored or manually entered addresses instead (see Guard address storage).
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

func newLibp2pHost(priv crypto.PrivKey, port int) (host.Host, error) {
	cm, err := connmgr.NewConnManager(10, 50)
	if err != nil {
		return nil, fmt.Errorf("connmgr: %w", err)
	}
	opts := []libp2p.Option{
		libp2p.ListenAddrStrings(
			fmt.Sprintf("/ip4/0.0.0.0/tcp/%d", port),
			fmt.Sprintf("/ip4/0.0.0.0/udp/%d/quic-v1", port),
			fmt.Sprintf("/ip6/::/tcp/%d", port),
		),
		libp2p.ConnectionManager(cm),
		libp2p.EnableNATService(),
		libp2p.EnableRelay(),
		libp2p.EnableHolePunching(),
	}
	if priv != nil {
		opts = append(opts, libp2p.Identity(priv))
	}
	return libp2p.New(opts...)
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

// DialAddrs returns the addresses another device can actually dial this host
// on, each terminated with /p2p/<peerID> so it is a complete dial string the
// user can copy into the other device's pairing form.
//
// Unspecified (0.0.0.0, ::) and loopback addresses are dropped: the first is a
// listen wildcard rather than a destination, and the second is only reachable
// from this machine. What survives is the set of concrete interface addresses,
// which on a VPN host includes the VPN address -- the one that matters, since
// mDNS multicast does not cross the tunnel and the DHT runs in client mode.
func (h *Host) DialAddrs() []string {
	if h.h == nil {
		return nil
	}
	suffix, err := multiaddr.NewMultiaddr("/p2p/" + h.h.ID().String())
	if err != nil {
		return nil
	}
	seen := make(map[string]bool)
	out := make([]string, 0, len(h.h.Addrs()))
	for _, a := range h.h.Addrs() {
		if !isDialableAddr(a) {
			continue
		}
		full := a.Encapsulate(suffix).String()
		if seen[full] {
			continue
		}
		seen[full] = true
		out = append(out, full)
	}
	if len(out) > 0 {
		return out
	}
	// Fallback: libp2p returned only wildcards (e.g. /ip4/0.0.0.0/tcp/4331).
	// On some platforms Addrs() does not expand the wildcard to per-interface
	// IPs, so synthesize dialable addresses from the host's interface list.
	var tcpPort, udpPort string
	for _, a := range h.h.Addrs() {
		if p, err := a.ValueForProtocol(multiaddr.P_TCP); err == nil && tcpPort == "" {
			tcpPort = p
		}
		if p, err := a.ValueForProtocol(multiaddr.P_UDP); err == nil && udpPort == "" {
			udpPort = p
		}
	}
	if tcpPort == "" && udpPort == "" {
		return out
	}
	ifaces, err := net.InterfaceAddrs()
	if err != nil {
		return out
	}
	for _, ia := range ifaces {
		var ip net.IP
		switch v := ia.(type) {
		case *net.IPNet:
			ip = v.IP
		case *net.IPAddr:
			ip = v.IP
		}
		if ip == nil || ip.IsLoopback() || ip.IsUnspecified() {
			continue
		}
		if ip4 := ip.To4(); ip4 != nil {
			ip = ip4
			if tcpPort != "" {
				if ma, err := multiaddr.NewMultiaddr(fmt.Sprintf("/ip4/%s/tcp/%s", ip.String(), tcpPort)); err == nil && isDialableAddr(ma) {
					full := ma.Encapsulate(suffix).String()
					if !seen[full] {
						seen[full] = true
						out = append(out, full)
					}
				}
			}
			if udpPort != "" {
				if ma, err := multiaddr.NewMultiaddr(fmt.Sprintf("/ip4/%s/udp/%s/quic-v1", ip.String(), udpPort)); err == nil && isDialableAddr(ma) {
					full := ma.Encapsulate(suffix).String()
					if !seen[full] {
						seen[full] = true
						out = append(out, full)
					}
				}
			}
		} else {
			if tcpPort != "" {
				if ma, err := multiaddr.NewMultiaddr(fmt.Sprintf("/ip6/%s/tcp/%s", ip.String(), tcpPort)); err == nil && isDialableAddr(ma) {
					full := ma.Encapsulate(suffix).String()
					if !seen[full] {
						seen[full] = true
						out = append(out, full)
					}
				}
			}
		}
	}
	return out
}

func isDialableAddr(a multiaddr.Multiaddr) bool {
	ipStr, err := a.ValueForProtocol(multiaddr.P_IP4)
	if err != nil {
		ipStr, err = a.ValueForProtocol(multiaddr.P_IP6)
		if err != nil {
			// Not an IP address (a relay or DNS address): keep it, since we
			// cannot judge it and it may well be the only way in.
			return true
		}
	}
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return false
	}
	return !ip.IsUnspecified() && !ip.IsLoopback()
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
