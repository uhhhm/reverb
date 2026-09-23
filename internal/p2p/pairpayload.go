package p2p

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multiaddr"

	"github.com/uhhhm/reverb/internal/sync"
)

// PairPayloadVersion is the pairing QR payload format this build writes and
// the only one it reads. A later format bumps it; an older build then says the
// payload needs a newer Reverb rather than misreading it.
const PairPayloadVersion = 1

// MaxPairPayloadAddrs bounds the addresses a pairing link carries. A link can
// come from anyone, so a reader refuses one that would have it dial more; a
// writer keeps the first this many, which still covers a LAN and a VPN on
// both transports and IPv6.
const MaxPairPayloadAddrs = 16

var (
	// ErrPairPayloadMalformed is a payload that is not a Reverb pairing link,
	// or one missing what pairing needs.
	ErrPairPayloadMalformed = errors.New("not a Reverb pairing code")
	// ErrPairPayloadVersion is a pairing link in a format this build does not
	// read.
	ErrPairPayloadVersion = errors.New("this pairing code needs a newer version of Reverb")
	// ErrPairPayloadExpired is a pairing link whose code has lapsed.
	ErrPairPayloadExpired = errors.New("this pairing code has expired; show a new one on the other device")
)

// PairPayload is what a pairing QR code carries: the one-time code, when it
// lapses, and the responder's reachable addresses (LAN and VPN), each a full
// dial string ending in /p2p/<peerID>.
//
// The addresses are why it exists. mDNS does not cross a VPN, and a phone may
// not be allowed to multicast at all, so a scanned code has to say where the
// other device is. The code is still never sent: the redeemer dials those
// addresses and pairs through the same challenge-response a typed code uses.
type PairPayload struct {
	Code      string
	ExpiresAt int64
	Addrs     []string
}

// EncodePairPayload writes p as a reverb://pair link, which is what the QR
// code holds. The peer ID is written once and each address without it, which
// roughly halves the link and keeps the code sparse enough to scan off a
// screen from across a desk.
func EncodePairPayload(p PairPayload) (string, error) {
	if len(p.Addrs) == 0 {
		return "", fmt.Errorf("pairing payload: no address to dial")
	}
	v := url.Values{}
	v.Set("v", strconv.Itoa(PairPayloadVersion))
	v.Set("code", sync.NormalizePairingCode(p.Code))
	v.Set("exp", strconv.FormatInt(p.ExpiresAt, 10))
	var pid peer.ID
	addrs := p.Addrs
	if len(addrs) > MaxPairPayloadAddrs {
		addrs = addrs[:MaxPairPayloadAddrs]
	}
	for _, a := range addrs {
		target, err := ParsePeerTarget(a)
		if err != nil || len(target.Addrs) != 1 {
			return "", fmt.Errorf("pairing payload: address %q is not a dial string", a)
		}
		if pid != "" && target.ID != pid {
			return "", fmt.Errorf("pairing payload: addresses name more than one device")
		}
		pid = target.ID
		v.Add("addr", target.Addrs[0].String())
	}
	v.Set("peer", pid.String())
	return "reverb://pair?" + v.Encode(), nil
}

// ParsePairPayload reads a pairing link and returns it with the addresses to
// dial. Every address must name the same peer: a payload pointing at two
// devices cannot say which one holds the code. now is compared with the
// expiry, so a lapsed code is refused before anything is dialled.
func ParsePairPayload(raw string, now time.Time) (PairPayload, peer.AddrInfo, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "reverb" || u.Host != "pair" {
		return PairPayload{}, peer.AddrInfo{}, ErrPairPayloadMalformed
	}
	q := u.Query()
	if q.Get("v") != strconv.Itoa(PairPayloadVersion) {
		return PairPayload{}, peer.AddrInfo{}, ErrPairPayloadVersion
	}
	p := PairPayload{Code: sync.NormalizePairingCode(q.Get("code")), Addrs: q["addr"]}
	if p.Code == "" || len(p.Addrs) == 0 || len(p.Addrs) > MaxPairPayloadAddrs {
		return PairPayload{}, peer.AddrInfo{}, ErrPairPayloadMalformed
	}
	if p.ExpiresAt, err = strconv.ParseInt(q.Get("exp"), 10, 64); err != nil {
		return PairPayload{}, peer.AddrInfo{}, ErrPairPayloadMalformed
	}
	pid, err := peer.Decode(q.Get("peer"))
	if err != nil {
		return PairPayload{}, peer.AddrInfo{}, fmt.Errorf("%w: no device named", ErrPairPayloadMalformed)
	}
	pi := peer.AddrInfo{ID: pid}
	for i, a := range p.Addrs {
		// An address that already names a peer must name this one; the full
		// form is accepted so a hand-built link still reads.
		full := a
		if !strings.Contains(a, "/p2p/") {
			full = a + "/p2p/" + pid.String()
		}
		target, err := ParsePeerTarget(full)
		if err != nil || target.ID != pid || len(target.Addrs) == 0 {
			return PairPayload{}, peer.AddrInfo{}, fmt.Errorf("%w: address %q", ErrPairPayloadMalformed, a)
		}
		p.Addrs[i] = target.Addrs[0].String() + "/p2p/" + pid.String()
		pi.Addrs = append(pi.Addrs, target.Addrs...)
	}
	if !now.Before(time.Unix(p.ExpiresAt, 0)) {
		return PairPayload{}, peer.AddrInfo{}, ErrPairPayloadExpired
	}
	return p, pi, nil
}

// cgnat is 100.64.0.0/10, the shared address space Tailscale and other
// overlay VPNs number their devices from.
var cgnat = &net.IPNet{IP: net.IPv4(100, 64, 0, 0).To4(), Mask: net.CIDRMask(10, 32)}

// PairAddrIsLocal reports whether a pairing address points into the owner's
// own networks: loopback, private, link-local, or CGNAT (a LAN or a VPN).
// Anything else, including a DNS name, could be anywhere on the internet, so
// the phone warns before pairing with a device reachable only that way. Only
// the leading host is judged, since that is what is dialled; a relayed
// address counts as public whatever follows p2p-circuit, which the link's
// author chooses and the dial only passes on as a hint.
func PairAddrIsLocal(addr string) bool {
	ma, err := multiaddr.NewMultiaddr(addr)
	if err != nil || len(ma) == 0 {
		return false
	}
	if _, err := ma.ValueForProtocol(multiaddr.P_CIRCUIT); err == nil {
		return false
	}
	first := ma[0]
	if code := first.Protocol().Code; code != multiaddr.P_IP4 && code != multiaddr.P_IP6 {
		return false
	}
	ip := net.ParseIP(first.Value())
	if ip == nil {
		return false
	}
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || cgnat.Contains(ip)
}
