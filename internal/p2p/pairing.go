package p2p

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multiaddr"
)

// PairingService is the minimal seam needed for p2p pairing.
// *sync.PairingService satisfies it.
type PairingService interface {
	GenerateCode(ctx context.Context) (string, int64, error)
	// RedeemAs binds the pairing to the device ID the redeemer already authors
	// under. An empty deviceID mints one.
	RedeemAs(ctx context.Context, rawCode, deviceName, deviceID string) (string, string, error)
}

// pairRequest mirrors api.redeemRequest for libp2p.
type pairRequest struct {
	Code       string `json:"code"`
	DeviceName string `json:"deviceName"`
	// DeviceID is the redeemer's own device ID -- the identity it authors sync
	// changes under. The responder binds the peer to it, so the sync handler's
	// identity check matches what this peer will actually send. Empty from an
	// older peer, which mints one instead.
	DeviceID string `json:"deviceId,omitempty"`
}

type pairResponse struct {
	DeviceID string `json:"deviceId"`
	Token    string `json:"token"`
	// PeerDeviceID is the responder's own device ID. The redeemer records it
	// against the responder's peer ID so it can later verify which device that
	// peer is entitled to speak for.
	PeerDeviceID string `json:"peerDeviceId,omitempty"`
	// PeerPublicKey is the responder's verification key, so the redeemer can
	// check changes it later authors.
	PeerPublicKey string `json:"peerPublicKey,omitempty"`
	Error         string `json:"error,omitempty"`
}

// pairLimiter throttles pairing attempts per remote peer and globally. Pairing
// is necessarily reachable by unpaired peers, so it is the one handler that
// cannot be gated by Guard and needs its own brute-force bound.
var pairLimiter = newAttemptLimiter(pairAttemptsPerPeer, pairAttemptsGlobal, pairAttemptWindow)

// RegisterPairingHandler mounts /reverb/pair/1.0.0 on the host. This is the
// only handler open to unpaired peers — it is how trust is bootstrapped — so it
// is rate limited and binds the caller's libp2p peer ID to the device row it
// creates. localDeviceID supplies this node's own device ID for the response;
// it may be nil, in which case the peer cannot bind us to a device.
func RegisterPairingHandler(h host.Host, pairing PairingService, guard *Guard, keys DeviceKeyStore, localDeviceID func(context.Context) (string, error)) {
	h.SetStreamHandler(pairProtocol, safeHandler("pair", func(s network.Stream) {
		defer s.Close()
		_ = s.SetDeadline(time.Now().Add(10 * time.Second))
		remote := s.Conn().RemotePeer()
		if !pairLimiter.Allow(remote.String()) {
			_ = json.NewEncoder(s).Encode(pairResponse{Error: "too many pairing attempts; try again later"})
			return
		}
		var req pairRequest
		if err := decodeLimited(s, maxPairRequestBytes, &req); err != nil {
			_ = json.NewEncoder(s).Encode(pairResponse{Error: fmt.Sprintf("decode: %v", err)})
			return
		}
		ctx := context.Background()
		// A peer names the device it already authors under, but it may not name
		// one that is spoken for: device IDs are not secret (they travel in the
		// author field of every change), so a code holder could otherwise claim
		// this node's own identity or another peer's and have its changes
		// indistinguishable from theirs.
		if req.DeviceID != "" {
			var localID string
			if localDeviceID != nil {
				localID, _ = localDeviceID(ctx)
			}
			if taken, why := deviceIDTaken(ctx, guard, localID, remote, req.DeviceID); taken {
				_ = json.NewEncoder(s).Encode(pairResponse{Error: why})
				return
			}
		}
		deviceID, token, err := pairing.RedeemAs(ctx, req.Code, req.DeviceName, req.DeviceID)
		if err != nil {
			_ = json.NewEncoder(s).Encode(pairResponse{Error: err.Error()})
			return
		}
		// Bind the authenticated libp2p identity to the device just created.
		// Without this the sync and file handlers have no way to tell who is
		// calling, and the device ID alone would be the only credential.
		if guard != nil {
			if err := guard.Trust(ctx, remote, deviceID, req.DeviceName); err != nil {
				_ = json.NewEncoder(s).Encode(pairResponse{Error: fmt.Sprintf("trust peer: %v", err)})
				return
			}
			// Remember where this peer dialed from. Whoever initiated had a way
			// to reach us; recording the reverse direction is what lets us dial
			// back later on a network where discovery cannot find them.
			if err := guard.RememberAddrs(ctx, remote, ObservedAddrs(h, remote)); err != nil {
				log.Printf("p2p pair: remember addrs for %s: %v", remote, err)
			}
		}
		// Bind the peer's verification key to the device row. The key is
		// carried inside the Ed25519 peer ID, so pairing needs no separate key
		// exchange, and the binding is what lets us later verify this device's
		// changes when another peer relays them.
		if pubB64, kerr := PublicKeyBase64(remote); kerr == nil && keys != nil {
			if err := RecordPeerDevice(ctx, keys, deviceID, req.DeviceName, pubB64); err != nil {
				log.Printf("p2p pair: record device key for %s: %v", deviceID, err)
			}
		}
		pairLimiter.Reset(remote.String())
		resp := pairResponse{DeviceID: deviceID, Token: token}
		if localDeviceID != nil {
			if id, err := localDeviceID(ctx); err == nil {
				resp.PeerDeviceID = id
				resp.PeerPublicKey, _ = PublicKeyBase64(h.ID())
			}
		}
		_ = json.NewEncoder(s).Encode(resp)
	}))
}

// deviceIDTaken reports whether want is an identity the peer on the other end of
// this pairing must not be bound to: this node's own, or one already bound to a
// different peer. Both halves of the exchange check it — the responder against
// the device ID the redeemer announces, the redeemer against the one the
// responder reports for itself — because the binding is only worth anything if
// neither side can claim an identity the other already signs for. localID is
// this node's device ID, or "" when it has none yet.
func deviceIDTaken(ctx context.Context, guard *Guard, localID string, remote peer.ID, want string) (bool, string) {
	if localID != "" && localID == want {
		return true, "device id is already this node's own"
	}
	if guard == nil {
		return false, ""
	}
	peers, err := guard.TrustedPeers(ctx)
	if err != nil {
		return false, ""
	}
	for pid, dev := range peers {
		if dev == want && pid != remote {
			return true, "device id already belongs to another paired peer"
		}
	}
	return false, ""
}

// RedeemViaPeer dials target via the host and redeems a pairing code over
// libp2p. It is the remote counterpart to HTTP POST /pairing/redeem.
//
// target is either a bare peer ID or a full multiaddr ending in /p2p/<peerID>.
// The bare form works only where discovery has already found the peer, which in
// practice means the same LAN via mDNS. Over a VPN the caller must give the full
// multiaddr, since multicast does not cross the tunnel and the DHT advertises
// addresses that are not routable there.
//
// localDeviceID is this node's own device ID, the one its syncer sends on every
// round. The responder binds the peer connection to it, so pushes from here are
// recognised rather than refused as a mismatched identity.
//
// On success the responding peer is added to the local trust set, bound to the
// device ID it reported for itself, and its address is persisted so later
// reconnects need no discovery. This is the redeemer half of the mutual binding
// the pairing handler performs on the other side.
func RedeemViaPeer(ctx context.Context, h host.Host, guard *Guard, keys DeviceKeyStore, target, code, deviceName, localDeviceID string) (string, string, error) {
	if h == nil {
		return "", "", fmt.Errorf("host is nil")
	}
	pi, err := ParsePeerTarget(target)
	if err != nil {
		return "", "", err
	}
	pid := pi.ID
	// Seed before dialing: with no addresses in the peerstore NewStream has
	// nothing to resolve the peer ID to and fails without ever touching the
	// network.
	SeedAddrs(h, pi)
	s, err := h.NewStream(ctx, pid, pairProtocol)
	if err != nil {
		return "", "", fmt.Errorf("open stream: %w", err)
	}
	defer s.Close()
	_ = s.SetDeadline(time.Now().Add(10 * time.Second))
	if err := json.NewEncoder(s).Encode(pairRequest{Code: code, DeviceName: deviceName, DeviceID: localDeviceID}); err != nil {
		return "", "", err
	}
	// Close write side to signal EOF for some transports.
	_ = s.CloseWrite()
	var resp pairResponse
	if err := decodeLimited(s, maxPairRequestBytes, &resp); err != nil {
		return "", "", err
	}
	if resp.Error != "" {
		return "", "", fmt.Errorf("%s", resp.Error)
	}
	if resp.DeviceID == "" || resp.Token == "" {
		return "", "", fmt.Errorf("invalid pair response")
	}
	// The responder names the device it authors under, and it may not name one
	// that is spoken for here. Without this a hostile responder could claim this
	// node's own device ID: the sync handler would then read its pushes as
	// self-authored, accept them unsigned, and signatureFor would sign them with
	// this device's key, so every other peer would accept them as ours.
	if resp.PeerDeviceID != "" {
		if taken, why := deviceIDTaken(ctx, guard, localDeviceID, pid, resp.PeerDeviceID); taken {
			return "", "", fmt.Errorf("peer claimed a device id that is not free: %s", why)
		}
	}
	// Record the responder as a known device with its verification key. Its
	// key is derivable from the peer ID we dialed, so a lying response cannot
	// substitute a different one.
	if resp.PeerDeviceID != "" && keys != nil {
		pubB64, kerr := PublicKeyBase64(pid)
		if kerr == nil {
			if resp.PeerPublicKey != "" && resp.PeerPublicKey != pubB64 {
				return "", "", fmt.Errorf("peer announced a key that does not match its peer ID")
			}
			if err := RecordPeerDevice(ctx, keys, resp.PeerDeviceID, deviceName, pubB64); err != nil {
				return "", "", fmt.Errorf("record peer device: %w", err)
			}
		}
	}
	if guard != nil {
		if err := guard.Trust(ctx, pid, resp.PeerDeviceID, deviceName); err != nil {
			return "", "", fmt.Errorf("trust peer: %w", err)
		}
		// Prefer the address the user supplied over the one observed on the
		// connection: it is the one known to work from this network, whereas an
		// observed address may be a transient port on a NAT.
		addrs := append(addrStrings(pi), ObservedAddrs(h, pid)...)
		if err := guard.RememberAddrs(ctx, pid, addrs); err != nil {
			log.Printf("p2p pair: remember addrs for %s: %v", pid, err)
		}
	}
	return resp.DeviceID, resp.Token, nil
}

// pairProtocol is the stream protocol the pairing handler is mounted on.
const pairProtocol = "/reverb/pair/1.0.0"

// RedeemViaDiscoveredPeers redeems code against whichever connected Reverb
// device accepts it, for the common case where the user has only the code.
//
// On a LAN, mDNS has already connected this host to every other Reverb on the
// network, and identify has told us which of those speak the pairing protocol;
// the code itself is single-use and bound to one device, so trying each
// candidate in turn is safe: the wrong ones refuse it and the right one pairs.
// A refusal on a wrong device costs one of that device's pairing attempts for
// this peer, which a household-sized network never exhausts.
//
// Nothing is tried over a VPN, where discovery does not work and the
// candidate list is empty: the caller then needs the other device's address.
func RedeemViaDiscoveredPeers(ctx context.Context, h host.Host, guard *Guard, keys DeviceKeyStore, code, deviceName, localDeviceID string) (string, string, error) {
	if h == nil {
		return "", "", fmt.Errorf("host is nil")
	}
	candidates := pairablePeers(h)
	if len(candidates) == 0 {
		return "", "", fmt.Errorf("no other Reverb device was found on this network; if it is on a VPN, enter its address as well as the code")
	}
	var refusals []string
	for _, pid := range candidates {
		deviceID, token, err := RedeemViaPeer(ctx, h, guard, keys, pid.String(), code, deviceName, localDeviceID)
		if err == nil {
			return deviceID, token, nil
		}
		if ctx.Err() != nil {
			return "", "", err
		}
		refusals = append(refusals, fmt.Sprintf("%s: %v", pid, err))
	}
	return "", "", fmt.Errorf("no device on this network accepted the code (%s); check the code on the other device, or enter its address if it is on a VPN", strings.Join(refusals, "; "))
}

// pairablePeers lists the connected peers that advertise the pairing protocol,
// in a stable order. Connections come from mDNS and the DHT alike, and the
// DHT's public nodes must not be asked to pair, so the protocol filter is what
// narrows the set to Reverb devices.
func pairablePeers(h host.Host) []peer.ID {
	var out []peer.ID
	for _, pid := range h.Network().Peers() {
		if pid == h.ID() {
			continue
		}
		supported, err := h.Peerstore().SupportsProtocols(pid, pairProtocol)
		if err != nil || len(supported) == 0 {
			continue
		}
		out = append(out, pid)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}

// addrStrings renders pi's addresses as full dial strings including /p2p/<id>.
func addrStrings(pi peer.AddrInfo) []string {
	if len(pi.Addrs) == 0 {
		return nil
	}
	suffix, err := multiaddr.NewMultiaddr("/p2p/" + pi.ID.String())
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(pi.Addrs))
	for _, a := range pi.Addrs {
		out = append(out, a.Encapsulate(suffix).String())
	}
	return out
}
