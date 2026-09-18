package p2p

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/event"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"

	reverbsync "github.com/uhhhm/reverb/internal/sync"
)

// The code the regression tests pair with, in the form a user types and in the
// stripped form both sides derive their proofs from.
const (
	typedPairCode    = "AB12-CD34"
	strippedPairCode = "AB12CD34"
)

// A peer that advertises the pairing protocol but never proves it holds the
// code must not be trusted and must never be handed the code, even when it
// answers a redeem with exactly the success the pre-proof protocol expected.
func TestPairingRefusesRogueResponderWithoutCode(t *testing.T) {
	ctx := context.Background()
	server, client := newLinkedHosts(t)

	var received bytes.Buffer
	done := make(chan struct{})
	server.SetStreamHandler(pairProtocol, func(s network.Stream) {
		defer close(done)
		defer s.Close()
		_ = s.SetDeadline(time.Now().Add(5 * time.Second))
		dec := json.NewDecoder(io.TeeReader(s, &received))
		var hello map[string]any
		_ = dec.Decode(&hello)
		// What the old redeemer trusted: a well-formed success, no proof.
		_ = json.NewEncoder(s).Encode(pairResponse{
			DeviceID:     "dev_rogue",
			Token:        "tok_rogue",
			PeerDeviceID: "dev_rogue",
		})
		_, _ = io.Copy(&received, s)
	})

	clientGuard := NewGuard(newSyncDB(t, "client.db"))
	_, token, err := RedeemViaPeer(ctx, client, clientGuard, nil, server.ID().String(), typedPairCode, "laptop", "")
	if err == nil {
		t.Fatal("redeemer trusted a peer that never proved possession of the code")
	}
	if token != "" {
		t.Fatalf("rogue responder handed the redeemer a token: %q", token)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("rogue pairing handler did not finish")
	}
	assertNoPairingCodeOnWire(t, received.String())
	assertNoTrustedPeers(t, clientGuard)
}

// Completing the challenge with a well-formed but fabricated proof — including
// reflecting the redeemer's own proof back — must not earn trust, and must not
// make the redeemer disclose the code in the message that carries its proof.
func TestPairingRefusesFabricatedResponderProof(t *testing.T) {
	for _, tc := range []struct {
		name    string
		reflect bool
	}{
		{name: "garbage proof"},
		{name: "reflected redeemer proof", reflect: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			server, client := newLinkedHosts(t)

			var received bytes.Buffer
			server.SetStreamHandler(pairProtocol, func(s network.Stream) {
				answerPairChallenge(t, s, server.ID(), &received, func(pc pairContext, redeemerProof string) pairResponse {
					proof := redeemerProof
					if !tc.reflect {
						wrong, _ := pc.key("ZZZZZZZZ")
						proof = encodePairProof(pc.proof(wrong, pairLabelResponder))
					}
					return pairResponse{
						DeviceID: "dev_rogue",
						Token:    "tok_rogue",
						Proof:    proof,
					}
				})
			})

			clientGuard := NewGuard(newSyncDB(t, "client.db"))
			_, token, err := RedeemViaPeer(ctx, client, clientGuard, nil, server.ID().String(), typedPairCode, "laptop", "")
			if err == nil {
				t.Fatal("redeemer accepted a responder that cannot prove possession of the code")
			}
			if token != "" {
				t.Fatalf("rogue responder handed the redeemer a token: %q", token)
			}
			assertNoPairingCodeOnWire(t, received.String())
			assertNoTrustedPeers(t, clientGuard)
		})
	}
}

// assertNoTrustedPeers fails when a guard was willing to trust any peer.
func assertNoTrustedPeers(t *testing.T, guard *Guard) {
	t.Helper()
	peers, err := guard.TrustedPeers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(peers) != 0 {
		t.Fatalf("peer was trusted without proving possession: %v", peers)
	}
}

// assertNoPairingCodeOnWire fails when bytes a peer received contain the
// pairing code in either the typed or the stripped form.
func assertNoPairingCodeOnWire(t *testing.T, got string) {
	t.Helper()
	if strings.Contains(got, strippedPairCode) || strings.Contains(got, typedPairCode) {
		t.Fatalf("plaintext pairing code crossed the wire: %q", got)
	}
}

// answerPairChallenge plays the responder up to the final answer: it reads the
// redeemer's hello, sends a nonce challenge, then reads the proof and hands the
// shared context and the proof as presented to final, which builds the response
// the test means to exercise. received, when non-nil, collects every byte the
// redeemer sent, including the proof message, for tests that assert what did or
// did not cross the wire.
func answerPairChallenge(t *testing.T, s network.Stream, responder peer.ID, received io.Writer, final func(pc pairContext, redeemerProof string) pairResponse) {
	t.Helper()
	defer s.Close()
	_ = s.SetDeadline(time.Now().Add(5 * time.Second))
	var from io.Reader = s
	if received != nil {
		from = io.TeeReader(s, received)
	}
	dec := json.NewDecoder(from)
	var hello pairHello
	if err := dec.Decode(&hello); err != nil {
		t.Errorf("pair responder: decode hello: %v", err)
		return
	}
	nonce, err := newPairNonce()
	if err != nil {
		t.Errorf("pair responder: new nonce: %v", err)
		return
	}
	if err := json.NewEncoder(s).Encode(pairChallenge{Nonce: nonce}); err != nil {
		t.Errorf("pair responder: encode challenge: %v", err)
		return
	}
	var presented pairRedeemProof
	if err := dec.Decode(&presented); err != nil {
		t.Errorf("pair responder: decode proof: %v", err)
		return
	}
	pc := newPairContext(hello, nonce, pairPeers{redeemer: s.Conn().RemotePeer().String(), responder: responder.String()})
	if err := json.NewEncoder(s).Encode(final(pc, presented.Proof)); err != nil {
		t.Errorf("pair responder: encode final response: %v", err)
	}
}

// A peer speaking the pre-proof protocol offers its code in the first message
// and expects success in return. It must be refused, and the code must survive
// unused: the request itself is not proof of possession.
func TestPairingRefusesLegacyRedeemerFormat(t *testing.T) {
	ctx := context.Background()
	server, client := newLinkedHosts(t)

	sq := newSyncDB(t, "server.db")
	svc := reverbsync.NewPairingService(sq)
	code, _, err := svc.GenerateCode(ctx)
	if err != nil {
		t.Fatal(err)
	}
	guard := NewGuard(sq)
	RegisterPairingHandler(server, svc, guard, sq, nil)

	s, err := client.NewStream(ctx, server.ID(), pairProtocol)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_ = s.SetDeadline(time.Now().Add(10 * time.Second))
	if err := json.NewEncoder(s).Encode(map[string]string{"code": code, "deviceName": "legacy"}); err != nil {
		t.Fatal(err)
	}
	_ = s.CloseWrite()
	var resp pairResponse
	if err := json.NewDecoder(s).Decode(&resp); err != nil {
		t.Fatalf("decode refusal: %v", err)
	}
	if resp.Error == "" {
		t.Fatal("legacy redeemer was not refused")
	}
	if resp.Token != "" || resp.DeviceID != "" {
		t.Fatalf("legacy redeemer got a device or token from an unproven request: %+v", resp)
	}
	pc, err := sq.GetPairingCode(ctx, reverbsync.NormalizePairingCode(code))
	if err != nil {
		t.Fatal(err)
	}
	if pc.UsedAt.Valid {
		t.Fatal("an unproven request consumed the pairing code")
	}
	assertNoTrustedPeers(t, guard)
}

// A redeemer that does not hold the code cannot obtain a token or trust, and
// the real code stays redeemable.
func TestRedeemWrongCodeGetsNoTokenOrTrust(t *testing.T) {
	ctx := context.Background()
	server, client := newLinkedHosts(t)

	sq := newSyncDB(t, "server.db")
	svc := reverbsync.NewPairingService(sq)
	code, _, err := svc.GenerateCode(ctx)
	if err != nil {
		t.Fatal(err)
	}
	guard := NewGuard(sq)
	RegisterPairingHandler(server, svc, guard, sq, nil)

	_, token, err := RedeemViaPeer(ctx, client, NewGuard(newSyncDB(t, "client.db")), nil, server.ID().String(), "ZZZZ-ZZZZ", "laptop", "")
	if err == nil {
		t.Fatal("redeemer paired without the code")
	}
	if token != "" {
		t.Fatalf("redeemer without the code obtained a token: %q", token)
	}
	assertNoTrustedPeers(t, guard)
	pc, err := sq.GetPairingCode(ctx, reverbsync.NormalizePairingCode(code))
	if err != nil {
		t.Fatal(err)
	}
	if pc.UsedAt.Valid {
		t.Fatal("a wrong code consumed the real code")
	}
}

// The companion to the refusal tests: the code holder still pairs end to end,
// and both sides bind the other to the device identity it offered.
func TestPairingWithCodeHolderSucceedsBothWays(t *testing.T) {
	ctx := context.Background()
	server, client := newLinkedHosts(t)

	sq := newSyncDB(t, "server.db")
	svc := reverbsync.NewPairingService(sq)
	code, _, err := svc.GenerateCode(ctx)
	if err != nil {
		t.Fatal(err)
	}
	serverLocal, err := reverbsync.EnsureLocalDevice(ctx, sq)
	if err != nil {
		t.Fatal(err)
	}
	serverGuard := NewGuard(sq)
	RegisterPairingHandler(server, svc, serverGuard, sq, func(context.Context) (string, error) {
		return serverLocal, nil
	})

	cq := newSyncDB(t, "client.db")
	clientGuard := NewGuard(cq)
	clientLocal, err := reverbsync.EnsureLocalDevice(ctx, cq)
	if err != nil {
		t.Fatal(err)
	}
	deviceID, token, err := RedeemViaPeer(ctx, client, clientGuard, cq, server.ID().String(), code, "laptop", clientLocal)
	if err != nil {
		t.Fatalf("RedeemViaPeer: %v", err)
	}
	if deviceID != clientLocal || token == "" {
		t.Fatalf("redeem returned (%q, %q), want the redeemer's own id and a token", deviceID, token)
	}

	bound, err := clientGuard.DeviceFor(ctx, server.ID())
	if err != nil {
		t.Fatalf("redeemer does not trust the responder: %v", err)
	}
	if bound != serverLocal {
		t.Fatalf("redeemer bound the responder to %q, want %q", bound, serverLocal)
	}
	bound, err = serverGuard.DeviceFor(ctx, client.ID())
	if err != nil {
		t.Fatalf("responder does not trust the redeemer: %v", err)
	}
	if bound != clientLocal {
		t.Fatalf("responder bound the redeemer to %q, want %q", bound, clientLocal)
	}
	pc, err := sq.GetPairingCode(ctx, reverbsync.NormalizePairingCode(code))
	if err != nil {
		t.Fatal(err)
	}
	if !pc.UsedAt.Valid {
		t.Fatal("a completed pairing did not consume the code")
	}
}

// The LAN fan-out must reach the code holder without being derailed by a rogue
// device that advertises the protocol and answers success.
func TestRedeemViaDiscoveredPeersIgnoresRogueCandidate(t *testing.T) {
	ctx := context.Background()

	server := newIsolatedHost(t)
	sq := newSyncDB(t, "server.db")
	svc := reverbsync.NewPairingService(sq)
	code, _, err := svc.GenerateCode(ctx)
	if err != nil {
		t.Fatal(err)
	}
	serverLocal, err := reverbsync.EnsureLocalDevice(ctx, sq)
	if err != nil {
		t.Fatal(err)
	}
	serverGuard := NewGuard(sq)
	RegisterPairingHandler(server, svc, serverGuard, sq, func(context.Context) (string, error) {
		return serverLocal, nil
	})

	rogue := newIsolatedHost(t)
	rogue.SetStreamHandler(pairProtocol, func(s network.Stream) {
		defer s.Close()
		_ = s.SetDeadline(time.Now().Add(5 * time.Second))
		var hello map[string]any
		_ = json.NewDecoder(s).Decode(&hello)
		_ = json.NewEncoder(s).Encode(pairResponse{DeviceID: "dev_rogue", Token: "tok_rogue", PeerDeviceID: "dev_rogue"})
	})

	client := newIsolatedHost(t)
	for _, target := range []peer.AddrInfo{
		{ID: server.ID(), Addrs: server.Addrs()},
		{ID: rogue.ID(), Addrs: rogue.Addrs()},
	} {
		if err := client.Connect(ctx, target); err != nil {
			t.Fatalf("connect %s: %v", target.ID, err)
		}
	}
	waitForPairProtocol(t, client, server.ID())
	waitForPairProtocol(t, client, rogue.ID())

	cq := newSyncDB(t, "client.db")
	clientGuard := NewGuard(cq)
	clientLocal, err := reverbsync.EnsureLocalDevice(ctx, cq)
	if err != nil {
		t.Fatal(err)
	}
	deviceID, token, err := RedeemViaDiscoveredPeers(ctx, client, clientGuard, cq, code, "laptop", clientLocal)
	if err != nil {
		t.Fatalf("RedeemViaDiscoveredPeers: %v", err)
	}
	if deviceID != clientLocal || token == "" {
		t.Fatalf("redeem returned (%q, %q), want the redeemer's own id and a token", deviceID, token)
	}
	if _, err := clientGuard.DeviceFor(ctx, rogue.ID()); err == nil {
		t.Fatal("rogue candidate was trusted")
	}
	bound, err := serverGuard.DeviceFor(ctx, client.ID())
	if err != nil || bound != clientLocal {
		t.Fatalf("responder bound the redeemer to %q, %v; want %q", bound, err, clientLocal)
	}
}

// A responder that only speaks the pre-proof protocol, as an older build does,
// must be refused rather than silently trusted.
func TestPairingRefusesPreProofResponder(t *testing.T) {
	ctx := context.Background()
	server, client := newLinkedHosts(t)

	server.SetStreamHandler("/reverb/pair/1.0.0", func(s network.Stream) {
		defer s.Close()
		_ = json.NewEncoder(s).Encode(pairResponse{DeviceID: "dev_old", Token: "tok_old"})
	})

	clientGuard := NewGuard(newSyncDB(t, "client.db"))
	_, token, err := RedeemViaPeer(ctx, client, clientGuard, nil, server.ID().String(), typedPairCode, "laptop", "")
	if err == nil {
		t.Fatal("redeemer paired with a peer that cannot prove possession")
	}
	if token != "" {
		t.Fatalf("pre-proof responder handed the redeemer a token: %q", token)
	}
	assertNoTrustedPeers(t, clientGuard)
}

// waitForPeerstore polls client's record for pid until ready reports the state
// the caller needs, so a test is not racing identify. want names the state, for
// the failure message.
func waitForPeerstore(t *testing.T, client host.Host, pid peer.ID, want string, ready func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if ready() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("peer %s: %s", pid, want)
}

// waitForPairProtocol waits until the peerstore knows pid advertises the
// pairing protocol.
func waitForPairProtocol(t *testing.T, client host.Host, pid peer.ID) {
	t.Helper()
	waitForPeerstore(t, client, pid, "never advertised "+pairProtocol, func() bool {
		supported, err := client.Peerstore().SupportsProtocols(pid, pairProtocol)
		return err == nil && len(supported) > 0
	})
}

// waitForIdentify blocks until identify has told client what pid speaks, which
// is the point after which the peerstore entry stops changing on its own.
func waitForIdentify(t *testing.T, client host.Host, pid peer.ID) {
	t.Helper()
	waitForPeerstore(t, client, pid, "identify never completed", func() bool {
		protos, err := client.Peerstore().GetProtocols(pid)
		return err == nil && len(protos) > 0
	})
}

func TestPairTranscriptIsUnambiguous(t *testing.T) {
	if bytes.Equal(pairTranscript("a", "bc"), pairTranscript("ab", "c")) {
		t.Fatal("field boundaries can be shifted through the transcript")
	}
}

func TestPairProofBindsDirectionSessionAndIdentities(t *testing.T) {
	base := pairContext{
		clientNonce:   "client-nonce",
		serverNonce:   "server-nonce",
		deviceID:      "dev_client",
		deviceName:    "laptop",
		redeemerPeer:  peerA,
		responderPeer: peerB,
	}
	key, err := base.key(strippedPairCode)
	if err != nil {
		t.Fatal(err)
	}
	want := base.proof(key, pairLabelRedeemer)
	if !bytes.Equal(want, base.proof(key, pairLabelRedeemer)) {
		t.Fatal("proof is not deterministic")
	}
	variants := map[string]pairContext{
		"direction":       base,
		"other nonce":     func() pairContext { c := base; c.serverNonce = "other"; return c }(),
		"other device":    func() pairContext { c := base; c.deviceID = "dev_other"; return c }(),
		"other name":      func() pairContext { c := base; c.deviceName = "desktop"; return c }(),
		"other redeemer":  func() pairContext { c := base; c.redeemerPeer = peerB; return c }(),
		"other responder": func() pairContext { c := base; c.responderPeer = peerA; return c }(),
	}
	for name, c := range variants {
		if name == "direction" {
			if bytes.Equal(want, c.proof(key, pairLabelResponder)) {
				t.Fatal("redeemer and responder proofs are interchangeable")
			}
			continue
		}
		if bytes.Equal(want, c.proof(key, pairLabelRedeemer)) {
			t.Fatalf("%s does not change the proof", name)
		}
	}
	otherKey, err := base.key("ZZZZZZZZ")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(want, base.proof(otherKey, pairLabelRedeemer)) {
		t.Fatal("proof does not depend on the code")
	}
}

func TestPairNonceValidation(t *testing.T) {
	nonce, err := newPairNonce()
	if err != nil {
		t.Fatal(err)
	}
	if err := validPairNonce(nonce); err != nil {
		t.Fatalf("fresh nonce rejected: %v", err)
	}
	short := base64.RawStdEncoding.EncodeToString(make([]byte, pairNonceBytes-1))
	for _, bad := range []string{"", "not base64!!", short, base64.StdEncoding.EncodeToString(make([]byte, pairNonceBytes))} {
		if err := validPairNonce(bad); err == nil {
			t.Fatalf("invalid nonce %q accepted", bad)
		}
	}
}

// A peer that is connected but has not yet been identified is one worth
// waiting for: the redeem must block until identify reports its protocols, not
// conclude the network is empty. This is the state a busy or slow device is in
// when the owner enters the code. Ordering is asserted through the returned
// channel rather than elapsed time, so a loaded machine cannot make it flake.
func TestAwaitPairablePeersWaitsForIdentify(t *testing.T) {
	remote, client := newIsolatedHost(t), newIsolatedHost(t)
	connectHosts(t, client, remote)
	// Let the real identify exchange finish, then return the client to the
	// pre-identify state. Driving the peerstore first would race identify,
	// which would land afterwards and overwrite the state under test.
	waitForIdentify(t, client, remote.ID())
	if err := client.Peerstore().SetProtocols(remote.ID()); err != nil {
		t.Fatal(err)
	}

	done := make(chan []peer.ID, 1)
	go func() { done <- awaitPairablePeers(context.Background(), client) }()

	// The peer is connected but unidentified, so the wait must still be running.
	select {
	case got := <-done:
		t.Fatalf("returned %v while the only peer was still unidentified; it was not waited for", got)
	case <-time.After(150 * time.Millisecond):
	}

	if err := client.Peerstore().SetProtocols(remote.ID(), pairProtocol); err != nil {
		t.Fatal(err)
	}
	emitted := time.Now()
	emit(t, client, event.EvtPeerIdentificationCompleted{Peer: remote.ID()})

	select {
	case got := <-done:
		if len(got) != 1 || got[0] != remote.ID() {
			t.Fatalf("returned %v, want the remote peer once identify completed", got)
		}
		// Without this bound the test also passes on a wait that ignores the
		// event and simply runs out the grace. The bound has to be well under
		// identifyGrace: the grace began before the emit, so a grace-driven
		// wake lands just under it. An event-driven one takes microseconds.
		if elapsed := time.Since(emitted); elapsed >= identifyGrace/2 {
			t.Fatalf("returned %s after the event: the grace elapsed rather than the event waking the wait", elapsed)
		}
	case <-time.After(2 * identifyGrace):
		t.Fatal("never returned after identify completed")
	}
}

// Identify can fail, and libp2p records nothing in the peerstore when it does.
// The wait must still end on the event, because a peer that will never be
// identified is not a peer worth waiting for.
func TestAwaitPairablePeersStopsWaitingWhenIdentifyFails(t *testing.T) {
	remote, client := newIsolatedHost(t), newIsolatedHost(t)
	connectHosts(t, client, remote)
	waitForIdentify(t, client, remote.ID())
	if err := client.Peerstore().SetProtocols(remote.ID()); err != nil {
		t.Fatal(err)
	}

	done := make(chan []peer.ID, 1)
	go func() { done <- awaitPairablePeers(context.Background(), client) }()
	select {
	case got := <-done:
		t.Fatalf("returned %v before identify resolved either way", got)
	case <-time.After(150 * time.Millisecond):
	}

	emitted := time.Now()
	emit(t, client, event.EvtPeerIdentificationFailed{Peer: remote.ID()})

	select {
	case got := <-done:
		if len(got) != 0 {
			t.Fatalf("returned %v, want none: identify failed for the only peer", got)
		}
		// The failure event is what must end the wait. Without this bound the
		// test also passes on code that ignores the event and waits out the
		// grace, which is the regression it exists to catch. The bound is well
		// under identifyGrace because the grace began before the emit.
		if elapsed := time.Since(emitted); elapsed >= identifyGrace/2 {
			t.Fatalf("returned %s after the failure: the grace elapsed rather than the event ending the wait", elapsed)
		}
	case <-time.After(2 * identifyGrace):
		t.Fatal("never returned after identify failed")
	}
}

// Waiting is bounded by what can still change. Once every connected peer has
// been identified and none speaks the pairing protocol, a longer wait cannot
// produce a candidate, so the answer must come back at once.
func TestAwaitPairablePeersDoesNotWaitOnIdentifiedNonPeers(t *testing.T) {
	remote, client := newIsolatedHost(t), newIsolatedHost(t)
	connectHosts(t, client, remote)
	waitForIdentify(t, client, remote.ID())
	if err := client.Peerstore().SetProtocols(remote.ID(), "/not-reverb/1.0.0"); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	got := awaitPairablePeers(context.Background(), client)
	if len(got) != 0 {
		t.Fatalf("returned %v, want none: the only peer is not a Reverb device", got)
	}
	if elapsed := time.Since(start); elapsed >= identifyGrace {
		t.Fatalf("waited %s on a peer whose protocols are already known", elapsed)
	}
}

// connectHosts dials to from and fails the test if the connection cannot be
// established.
func connectHosts(t *testing.T, from, to host.Host) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := from.Connect(ctx, peer.AddrInfo{ID: to.ID(), Addrs: to.Addrs()}); err != nil {
		t.Fatalf("connect: %v", err)
	}
}

// emit publishes ev on h's event bus, standing in for the identify exchange
// reaching a conclusion at a moment the test chooses.
func emit(t *testing.T, h host.Host, ev any) {
	t.Helper()
	// The bus types an emitter from a pointer to the event type, but emits the
	// value itself.
	em, err := h.EventBus().Emitter(reflect.New(reflect.TypeOf(ev)).Interface())
	if err != nil {
		t.Fatal(err)
	}
	defer em.Close()
	if err := em.Emit(ev); err != nil {
		t.Fatal(err)
	}
}

// Waiting for identify must not turn a genuinely empty network into a stall:
// with nothing connected there is nothing to wait for, so the owner gets the
// actionable error immediately rather than after the grace elapses.
func TestRedeemViaDiscoveredPeersFailsFastWithNoPeers(t *testing.T) {
	client := newIsolatedHost(t)
	cq := newSyncDB(t, "client.db")

	start := time.Now()
	_, _, err := RedeemViaDiscoveredPeers(context.Background(), client, NewGuard(cq), cq, "ABC123", "laptop", "dev_local")
	if err == nil {
		t.Fatal("redeem succeeded with no peers connected")
	}
	if elapsed := time.Since(start); elapsed >= identifyGrace {
		t.Fatalf("redeem took %s with no peers connected; it must not wait out the %s grace", elapsed, identifyGrace)
	}
	if !strings.Contains(err.Error(), "no other Reverb device was found") {
		t.Fatalf("error %q does not tell the owner what to do", err)
	}
}
