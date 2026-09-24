package p2p

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/uhhhm/reverb/internal/sync"
)

func newPeerID(t *testing.T) string {
	t.Helper()
	priv, _, err := crypto.GenerateEd25519Key(nil)
	if err != nil {
		t.Fatal(err)
	}
	id, err := peer.IDFromPrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	return id.String()
}

var payloadNow = time.Unix(1_800_000_000, 0)

func TestPairPayloadRoundTrips(t *testing.T) {
	payloadPeer := newPeerID(t)
	lanAddr := "/ip4/192.168.1.20/tcp/4331/p2p/" + payloadPeer
	vpnAddr := "/ip4/100.64.0.7/udp/4331/quic-v1/p2p/" + payloadPeer
	enc, err := EncodePairPayload(PairPayload{Code: "ab12-cd34", ExpiresAt: payloadNow.Add(time.Minute).Unix(), Addrs: []string{lanAddr, vpnAddr}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(enc, "reverb://pair?") {
		t.Fatalf("payload %q is not a reverb pairing link", enc)
	}
	got, pi, err := ParsePairPayload(enc, payloadNow)
	if err != nil {
		t.Fatal(err)
	}
	if got.Code != "AB12CD34" || len(got.Addrs) != 2 || got.Addrs[0] != lanAddr || got.Addrs[1] != vpnAddr {
		t.Fatalf("decoded %+v", got)
	}
	if pi.ID.String() != payloadPeer || len(pi.Addrs) != 2 {
		t.Fatalf("addr info %+v", pi)
	}
}

func TestPairPayloadRefusesWhatItCannotUse(t *testing.T) {
	valid := func(mut func(url.Values)) string {
		v := url.Values{}
		v.Set("v", "1")
		v.Set("code", "AB12CD34")
		v.Set("exp", "1800000060")
		v.Set("peer", newPeerID(t))
		v.Add("addr", "/ip4/192.168.1.20/tcp/4331")
		mut(v)
		return "reverb://pair?" + v.Encode()
	}
	otherPeer := "/ip4/10.0.0.2/tcp/4331/p2p/" + newPeerID(t)
	cases := map[string]struct {
		payload string
		want    error
	}{
		"not a link":     {"AB12-CD34", ErrPairPayloadMalformed},
		"another scheme": {strings.Replace(valid(func(url.Values) {}), "reverb://", "https://", 1), ErrPairPayloadMalformed},
		"newer version":  {valid(func(v url.Values) { v.Set("v", "2") }), ErrPairPayloadVersion},
		"no version":     {valid(func(v url.Values) { v.Del("v") }), ErrPairPayloadVersion},
		"no code":        {valid(func(v url.Values) { v.Del("code") }), ErrPairPayloadMalformed},
		"no address":     {valid(func(v url.Values) { v.Del("addr") }), ErrPairPayloadMalformed},
		"no peer":        {valid(func(v url.Values) { v.Del("peer") }), ErrPairPayloadMalformed},
		"not an address": {valid(func(v url.Values) { v.Set("addr", "192.168.1.20:4331") }), ErrPairPayloadMalformed},
		"two peers":      {valid(func(v url.Values) { v.Add("addr", otherPeer) }), ErrPairPayloadMalformed},
		"no expiry":      {valid(func(v url.Values) { v.Del("exp") }), ErrPairPayloadMalformed},
		"expired":        {valid(func(v url.Values) { v.Set("exp", "1799999999") }), ErrPairPayloadExpired},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if _, _, err := ParsePairPayload(c.payload, payloadNow); !errors.Is(err, c.want) {
				t.Fatalf("err = %v, want %v", err, c.want)
			}
		})
	}
}

func TestEncodePairPayloadNeedsAnAddress(t *testing.T) {
	if _, err := EncodePairPayload(PairPayload{Code: "AB12CD34", ExpiresAt: 1}); err == nil {
		t.Fatal("a payload with nowhere to dial was encoded")
	}
}

func TestRemotePairingErrorsKeepStableIdentities(t *testing.T) {
	cases := []struct {
		message string
		want    error
	}{
		{sync.ErrCodeInvalid.Error(), sync.ErrCodeInvalid},
		{sync.ErrCodeExpired.Error(), sync.ErrCodeExpired},
		{sync.ErrCodeUsed.Error(), sync.ErrCodeUsed},
		{"too many pairing attempts; try again later", ErrPairingRateLimited},
	}
	for _, c := range cases {
		if err := remotePairingError(c.message); !errors.Is(err, c.want) {
			t.Errorf("remotePairingError(%q) = %v, want %v", c.message, err, c.want)
		}
	}
	if err := remotePairingError("trust peer: disk full"); errors.Is(err, sync.ErrCodeInvalid) {
		t.Fatalf("unknown remote failure was classified as invalid code: %v", err)
	}
}

// A pairing link can come from anyone, so the addresses it makes the phone
// dial are bounded; the device minting a code writes no more than it reads.
func TestPairPayloadCapsItsAddresses(t *testing.T) {
	pid := newPeerID(t)
	addrs := make([]string, MaxPairPayloadAddrs+1)
	for i := range addrs {
		addrs[i] = fmt.Sprintf("/ip4/192.168.1.%d/tcp/4331/p2p/%s", i+1, pid)
	}
	enc, err := EncodePairPayload(PairPayload{Code: "AB12CD34", ExpiresAt: payloadNow.Add(time.Minute).Unix(), Addrs: addrs})
	if err != nil {
		t.Fatal(err)
	}
	got, _, err := ParsePairPayload(enc, payloadNow)
	if err != nil {
		t.Fatalf("an encoded payload does not parse: %v", err)
	}
	if len(got.Addrs) != MaxPairPayloadAddrs || got.Addrs[0] != addrs[0] {
		t.Fatalf("encoded %d addresses starting %q, want the first %d", len(got.Addrs), got.Addrs[0], MaxPairPayloadAddrs)
	}

	v := url.Values{}
	v.Set("v", "1")
	v.Set("code", "AB12CD34")
	v.Set("exp", "1800000060")
	v.Set("peer", pid)
	for i := 0; i <= MaxPairPayloadAddrs; i++ {
		v.Add("addr", fmt.Sprintf("/ip4/10.0.0.%d/tcp/4331", i+1))
	}
	if _, _, err := ParsePairPayload("reverb://pair?"+v.Encode(), payloadNow); !errors.Is(err, ErrPairPayloadMalformed) {
		t.Fatalf("a payload with %d addresses: err = %v, want malformed", MaxPairPayloadAddrs+1, err)
	}
}

func TestPairAddrIsLocal(t *testing.T) {
	pid := newPeerID(t)
	cases := map[string]bool{
		"/ip4/127.0.0.1/tcp/4331":               true,
		"/ip4/192.168.1.20/tcp/4331":            true,
		"/ip4/10.1.2.3/udp/4331/quic-v1":        true,
		"/ip4/172.16.0.9/tcp/4331":              true,
		"/ip4/169.254.10.1/tcp/4331":            true,
		"/ip4/100.64.0.7/tcp/4331":              true, // CGNAT: Tailscale and friends
		"/ip4/100.127.255.254/tcp/4331":         true,
		"/ip6/::1/tcp/4331":                     true,
		"/ip6/fe80::1/tcp/4331":                 true,
		"/ip6/fd7a:115c:a1e0::1/tcp/4331":       true,
		"/ip4/100.128.0.1/tcp/4331":             false,
		"/ip4/203.0.113.9/tcp/4331":             false,
		"/ip6/2001:db8::1/tcp/4331":             false,
		"/dns4/evil.example/tcp/4331":           false,
		"/ip4/192.168.1.20/tcp/4331/p2p/" + pid: true,
		"not an address":                        false,
		// A relay is dialled at its own address; the one after p2p-circuit is
		// only a hint, so it cannot make the path local.
		"/dns4/relay.example/tcp/4001/p2p/" + pid + "/p2p-circuit/ip4/192.168.1.10/tcp/4331": false,
		"/ip6/2001:db8::1/tcp/4001/p2p/" + pid + "/p2p-circuit/ip4/10.0.0.1/tcp/4331":        false,
		"/ip4/192.168.1.2/tcp/4001/p2p/" + pid + "/p2p-circuit/ip4/192.168.1.10/tcp/4331":    false,
	}
	for addr, want := range cases {
		if got := PairAddrIsLocal(addr); got != want {
			t.Errorf("PairAddrIsLocal(%q) = %v, want %v", addr, got, want)
		}
	}
}
