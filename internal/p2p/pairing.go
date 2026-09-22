package p2p

import (
	"context"
	"crypto/hmac"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/libp2p/go-libp2p/core/event"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multiaddr"
	"github.com/uhhhm/reverb/internal/sync"
)

// PairingService is the minimal seam needed for p2p pairing.
// *sync.PairingService satisfies it.
type PairingService interface {
	GenerateCode(ctx context.Context) (string, int64, error)
	// ActivePairingCodes lists the unused, unexpired codes in stripped form. A
	// possession proof does not name the code it was computed from, so the
	// responder tests it against every code that could still be the one the
	// user typed.
	ActivePairingCodes(ctx context.Context) ([]string, error)
	// RedeemAs binds the pairing to the device ID the redeemer already authors
	// under. An empty deviceID mints one.
	RedeemAs(ctx context.Context, rawCode, deviceName, deviceID string) (string, string, error)
}

var (
	// ErrPairingRateLimited is returned when the peer refuses another pairing
	// attempt in the current window.
	ErrPairingRateLimited = errors.New("too many pairing attempts; try again later")
	// ErrPairingProofInvalid is returned when the responder does not prove it
	// holds the same code. Nothing the responder says can be trusted then.
	ErrPairingProofInvalid = errors.New("pairing possession proof failed")
)

// remotePairingError restores stable error identities after an error has
// crossed the pairing wire as text. The protocol predates structured errors,
// so only its fixed messages are classified; unknown failures stay opaque.
func remotePairingError(message string) error {
	switch message {
	case sync.ErrCodeInvalid.Error():
		return sync.ErrCodeInvalid
	case "invalid pairing proof":
		return fmt.Errorf("%w: invalid pairing proof", sync.ErrCodeInvalid)
	case sync.ErrCodeExpired.Error():
		return sync.ErrCodeExpired
	case sync.ErrCodeUsed.Error():
		return sync.ErrCodeUsed
	case "too many pairing attempts; try again later":
		return ErrPairingRateLimited
	default:
		return errors.New(message)
	}
}

// pairHello opens the exchange. It carries the redeemer's identity and a fresh
// nonce, and deliberately no code material: the code itself never crosses the
// wire, and the proof derived from it is only sent after the responder's
// challenge nonce, so a peer that merely advertises the protocol learns nothing
// it could redeem.
type pairHello struct {
	DeviceName string `json:"deviceName"`
	// DeviceID is the redeemer's own device ID -- the identity it authors sync
	// changes under. The responder binds the peer to it, so the sync handler's
	// identity check matches what this peer will actually send. Empty from a
	// redeemer that has no sync identity yet, in which case the responder
	// mints one.
	DeviceID string `json:"deviceId,omitempty"`
	// Nonce is the redeemer's half of the possession exchange.
	Nonce string `json:"nonce"`
}

// pairChallenge is the responder's nonce. It carries no code-derived value, so
// a peer that cannot prove possession never receives anything it could attack
// offline.
type pairChallenge struct {
	Nonce string `json:"nonce,omitempty"`
	Error string `json:"error,omitempty"`
}

// pairRedeemProof is the redeemer's proof that it holds the code, computed
// over both nonces and the identities exchanged so far. The code itself never
// crosses the wire.
type pairRedeemProof struct {
	Proof string `json:"proof"`
}

// pairResponse is the responder's final answer, sent only after the redeemer's
// proof has been verified against a live code.
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
	// Proof is the responder's own possession proof for the same code. The
	// redeemer verifies it before believing anything else in this response.
	// The device and token fields above ride the authenticated stream of the
	// very peer whose ID the proof is bound to, so no third party can splice
	// them into it.
	Proof string `json:"proof,omitempty"`
	Error string `json:"error,omitempty"`
}

// pairLimiter throttles pairing attempts per remote peer and globally. Pairing
// is necessarily reachable by unpaired peers, so it is the one handler that
// cannot be gated by Guard and needs its own brute-force bound.
var pairLimiter = newAttemptLimiter(pairAttemptsPerPeer, pairAttemptsGlobal, pairAttemptWindow)

// RegisterPairingHandler mounts /reverb/pair/2.0.0 on the host. This is the
// only handler open to unpaired peers — it is how trust is bootstrapped — so it
// is rate limited and both sides must prove they hold the pairing code before
// either trusts the other or a session token changes hands:
//
//  1. the redeemer opens with a nonce, never the code;
//  2. the responder answers with its own nonce;
//  3. the redeemer proves possession with a MAC under a stretched code key;
//  4. the responder verifies that proof against its live codes, redeems the
//     matching one, and proves its own possession in the response.
//
// A peer that cannot complete the proof is refused. In particular, a peer that
// speaks the old one-shot protocol, which offered the plaintext code and
// accepted a bare success, is not trusted.
//
// localDeviceID supplies this node's own device ID for the response; it may be
// nil, in which case the peer cannot bind us to a device.
func RegisterPairingHandler(h host.Host, pairing PairingService, guard *Guard, keys DeviceKeyStore, localDeviceID func(context.Context) (string, error)) {
	h.SetStreamHandler(pairProtocol, safeHandler("pair", func(s network.Stream) {
		defer s.Close()
		_ = s.SetDeadline(time.Now().Add(10 * time.Second))
		remote := s.Conn().RemotePeer()
		if !pairLimiter.Allow(remote.String()) {
			_ = json.NewEncoder(s).Encode(pairChallenge{Error: "too many pairing attempts; try again later"})
			return
		}
		ctx := context.Background()
		var hello pairHello
		if err := decodeLimited(s, maxPairRequestBytes, &hello); err != nil {
			_ = json.NewEncoder(s).Encode(pairChallenge{Error: fmt.Sprintf("decode: %v", err)})
			return
		}
		// Fail closed on a peer that does not open with a challenge nonce. A
		// pre-proof redeemer offers the plaintext code here; that request is
		// not evidence of possession and must not be redeemed.
		if err := validPairNonce(hello.Nonce); err != nil {
			_ = json.NewEncoder(s).Encode(pairChallenge{Error: "pairing requires a code-possession challenge"})
			return
		}
		// A peer names the device it already authors under, but it may not name
		// one that is spoken for: device IDs are not secret (they travel in the
		// author field of every change), so a code holder could otherwise claim
		// this node's own identity or another peer's and have its changes
		// indistinguishable from theirs.
		if hello.DeviceID != "" {
			var localID string
			if localDeviceID != nil {
				localID, _ = localDeviceID(ctx)
			}
			if taken, why := deviceIDTaken(ctx, guard, localID, remote, hello.DeviceID); taken {
				_ = json.NewEncoder(s).Encode(pairChallenge{Error: why})
				return
			}
		}
		serverNonce, err := newPairNonce()
		if err != nil {
			_ = json.NewEncoder(s).Encode(pairChallenge{Error: "could not start pairing"})
			return
		}
		if err := json.NewEncoder(s).Encode(pairChallenge{Nonce: serverNonce}); err != nil {
			return
		}

		var presented pairRedeemProof
		if err := decodeLimited(s, maxPairRequestBytes, &presented); err != nil {
			_ = json.NewEncoder(s).Encode(pairResponse{Error: fmt.Sprintf("decode proof: %v", err)})
			return
		}
		proof, err := decodePairProof(presented.Proof)
		if err != nil {
			_ = json.NewEncoder(s).Encode(pairResponse{Error: "invalid pairing proof"})
			return
		}
		pairCtx := newPairContext(hello, serverNonce, pairPeers{redeemer: remote.String(), responder: h.ID().String()})
		code, key, ok := matchPairProof(ctx, pairing, pairCtx, proof)
		if !ok {
			_ = json.NewEncoder(s).Encode(pairResponse{Error: "invalid pairing code"})
			return
		}
		deviceID, token, err := pairing.RedeemAs(ctx, code, hello.DeviceName, hello.DeviceID)
		if err != nil {
			_ = json.NewEncoder(s).Encode(pairResponse{Error: err.Error()})
			return
		}
		// Bind the authenticated libp2p identity to the device just created.
		// Without this the sync and file handlers have no way to tell who is
		// calling, and the device ID alone would be the only credential.
		if guard != nil {
			if err := guard.Trust(ctx, remote, deviceID, hello.DeviceName); err != nil {
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
			if err := RecordPeerDevice(ctx, keys, deviceID, hello.DeviceName, pubB64); err != nil {
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
		resp.Proof = encodePairProof(pairCtx.proof(key, pairLabelResponder))
		_ = json.NewEncoder(s).Encode(resp)
	}))
}

// matchPairProof returns the live code the presented redeemer proof was
// computed from, and the session key derived from it, so the caller can answer
// with its own proof. Every comparison is constant time.
func matchPairProof(ctx context.Context, pairing PairingService, pairCtx pairContext, presented []byte) (code string, sessionKey []byte, ok bool) {
	codes, err := pairing.ActivePairingCodes(ctx)
	if err != nil {
		log.Printf("p2p pair: list active codes: %v", err)
		return "", nil, false
	}
	for _, code := range codes {
		key, err := pairCtx.key(code)
		if err != nil {
			continue
		}
		if hmac.Equal(presented, pairCtx.proof(key, pairLabelRedeemer)) {
			return code, key, true
		}
	}
	return "", nil, false
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
// The code never travels. The redeemer answers the responder's challenge with a
// proof derived from the code, and only trusts the peer, its token and the
// device ID it reports after the responder has proved possession of the same
// code; a peer that cannot is refused.
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
	return RedeemViaAddrs(ctx, h, guard, keys, pi, code, deviceName, localDeviceID)
}

// RedeemViaAddrs is RedeemViaPeer against a peer whose addresses are already
// known, such as the several a pairing QR payload carries. Every address is
// seeded before the dial, so whichever one this network can reach is used,
// and all of them are remembered for later reconnects.
func RedeemViaAddrs(ctx context.Context, h host.Host, guard *Guard, keys DeviceKeyStore, pi peer.AddrInfo, code, deviceName, localDeviceID string) (string, string, error) {
	if h == nil {
		return "", "", fmt.Errorf("host is nil")
	}
	pid := pi.ID
	clientNonce, err := newPairNonce()
	if err != nil {
		return "", "", err
	}
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
	hello := pairHello{DeviceName: deviceName, DeviceID: localDeviceID, Nonce: clientNonce}
	if err := json.NewEncoder(s).Encode(hello); err != nil {
		return "", "", err
	}
	var challenge pairChallenge
	if err := decodeLimited(s, maxPairRequestBytes, &challenge); err != nil {
		return "", "", err
	}
	if challenge.Error != "" {
		return "", "", remotePairingError(challenge.Error)
	}
	if err := validPairNonce(challenge.Nonce); err != nil {
		return "", "", fmt.Errorf("responder sent no pairing challenge")
	}
	pairCtx := newPairContext(hello, challenge.Nonce, pairPeers{redeemer: h.ID().String(), responder: pid.String()})
	key, err := pairCtx.key(sync.NormalizePairingCode(code))
	if err != nil {
		return "", "", err
	}
	if err := json.NewEncoder(s).Encode(pairRedeemProof{Proof: encodePairProof(pairCtx.proof(key, pairLabelRedeemer))}); err != nil {
		return "", "", err
	}
	// Close write side to signal EOF for some transports.
	_ = s.CloseWrite()
	var resp pairResponse
	if err := decodeLimited(s, maxPairRequestBytes, &resp); err != nil {
		return "", "", err
	}
	if resp.Error != "" {
		return "", "", remotePairingError(resp.Error)
	}
	// The responder proves possession before anything it says is believed:
	// otherwise a rogue that answers "success" would be trusted and its token
	// used, which is exactly how pairing was forged before the proof existed.
	responderProof, err := decodePairProof(resp.Proof)
	if err != nil || !hmac.Equal(responderProof, pairCtx.proof(key, pairLabelResponder)) {
		return "", "", fmt.Errorf("%w: responder did not prove possession of the pairing code", ErrPairingProofInvalid)
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

// pairProtocol is the stream protocol the pairing handler is mounted on. It is
// versioned because the exchange is not compatible with 1.0.0, which sent the
// code and trusted a bare success.
const pairProtocol = "/reverb/pair/2.0.0"

// RedeemViaDiscoveredPeers redeems code against whichever connected Reverb
// device accepts it, for the common case where the user has only the code.
//
// On a LAN, mDNS has already connected this host to every other Reverb on the
// network, and identify has told us which of those speak the pairing protocol;
// the code itself is single-use and bound to one device, so trying each
// candidate in turn is safe: a peer that does not hold the code cannot answer
// the challenge and the holder pairs. A refusal on a wrong device costs one of
// that device's pairing attempts for this peer, which a household-sized
// network never exhausts.
//
// A connection exists before identify has reported what the other end speaks,
// so an empty candidate list is only believed once every connected peer has
// been identified, or a short grace has passed.
//
// Nothing is tried over a VPN, where discovery does not work and no peer is
// connected to wait for: the caller then needs the other device's address.
func RedeemViaDiscoveredPeers(ctx context.Context, h host.Host, guard *Guard, keys DeviceKeyStore, code, deviceName, localDeviceID string) (string, string, error) {
	if h == nil {
		return "", "", fmt.Errorf("host is nil")
	}
	candidates := awaitPairablePeers(ctx, h)
	if ctx.Err() != nil {
		// The caller gave up while we waited. Saying no device was found would
		// diagnose the network for what was actually an abandoned request.
		return "", "", ctx.Err()
	}
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

// identifyGrace bounds how long a redeem waits for connected peers to finish
// identifying. It is an upper bound rather than a delay: the wait ends the
// moment a candidate appears, or as soon as every connected peer has been
// identified and none speaks the pairing protocol.
const identifyGrace = 3 * time.Second

// awaitPairablePeers waits for a connected-but-unidentified peer to finish
// libp2p's identify exchange, returning the pairable set as soon as one
// appears. It returns immediately — reporting no candidates — when there is
// nothing to wait for, so a genuinely empty network still fails fast with an
// error the owner can act on rather than stalling for the full grace.
func awaitPairablePeers(ctx context.Context, h host.Host) []peer.ID {
	// Every event that can change the answer, not just the happy one. Identify
	// failing settles a peer that would otherwise look like it is still being
	// identified, and a connectedness change both retires that verdict and
	// wakes the loop to re-read the connected set; without them a peer that
	// never identifies would stall for the whole grace. Subscribing before the
	// first check closes the gap in which an event could land after the check
	// and before the wait.
	sub, err := h.EventBus().Subscribe([]any{
		new(event.EvtPeerIdentificationCompleted),
		new(event.EvtPeerIdentificationFailed),
		new(event.EvtPeerConnectednessChanged),
	})
	if err != nil {
		return pairablePeers(h)
	}
	defer sub.Close()

	// libp2p writes nothing to the peerstore when identify fails, so by
	// peerstore state alone a peer that will never be identified looks exactly
	// like one still being identified. Remembering the failures we observe
	// lets the wait end as soon as the answer is final. A failure that landed
	// before we subscribed is not replayed and cannot be recovered from the
	// public host API, so that peer is waited on until the grace — which is
	// what the grace is for.
	failed := make(map[peer.ID]bool)

	waitCtx, cancel := context.WithTimeout(ctx, identifyGrace)
	defer cancel()
	for {
		if candidates := pairablePeers(h); len(candidates) > 0 {
			return candidates
		}
		if !hasUnidentifiedPeer(h, failed) {
			return nil
		}
		select {
		case ev := <-sub.Out():
			switch e := ev.(type) {
			case event.EvtPeerIdentificationFailed:
				failed[e.Peer] = true
			case event.EvtPeerIdentificationCompleted:
				// A retry succeeded; the peerstore is authoritative again.
				delete(failed, e.Peer)
			case event.EvtPeerConnectednessChanged:
				// Any change of connectedness retires the old verdict: the
				// connection it was about is either gone or replaced by one
				// that identifies afresh, so the peer is worth waiting for
				// again.
				delete(failed, e.Peer)
			}
		case <-waitCtx.Done():
			if ctx.Err() != nil {
				// The caller gave up rather than the grace expiring. Handing
				// back candidates would only dial them with a dead context.
				return nil
			}
			return pairablePeers(h)
		}
	}
}

// hasUnidentifiedPeer reports whether any connected peer may still have
// something to tell us about what it speaks. The peerstore holds no protocols
// at all for such a peer, which is what separates "we do not know yet" from an
// identified peer that simply is not a Reverb device. Peers in failed are
// settled too: their identify has already reported, and reported nothing.
func hasUnidentifiedPeer(h host.Host, failed map[peer.ID]bool) bool {
	for _, pid := range connectedPeers(h) {
		if failed[pid] {
			continue
		}
		protos, err := h.Peerstore().GetProtocols(pid)
		if err == nil && len(protos) == 0 {
			return true
		}
	}
	return false
}

// connectedPeers lists the peers this host holds a connection to, never
// including itself.
func connectedPeers(h host.Host) []peer.ID {
	peers := h.Network().Peers()
	out := make([]peer.ID, 0, len(peers))
	for _, pid := range peers {
		if pid == h.ID() {
			continue
		}
		out = append(out, pid)
	}
	return out
}

// pairablePeers lists the connected peers that advertise the pairing protocol,
// in a stable order. Connections come from mDNS and the DHT alike, and the
// DHT's public nodes must not be asked to pair, so the protocol filter is what
// narrows the set to Reverb devices.
func pairablePeers(h host.Host) []peer.ID {
	var out []peer.ID
	for _, pid := range connectedPeers(h) {
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
