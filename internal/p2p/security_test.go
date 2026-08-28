package p2p

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/uhhhm/reverb/internal/store"
	"github.com/uhhhm/reverb/internal/store/db"
)

func newTrustStore(t *testing.T) *db.Queries {
	t.Helper()
	st, err := store.Open(t.TempDir() + "/trust.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	return st.Q()
}

func mustPeerID(t *testing.T, s string) peer.ID {
	t.Helper()
	pid, err := peer.Decode(s)
	if err != nil {
		t.Fatalf("decode peer id: %v", err)
	}
	return pid
}

// Two syntactically valid libp2p peer IDs.
const (
	peerA = "12D3KooWFbYbY7bSSNqvvBcvKzDbcvUsg5CjLYBRWuQ8bHPUYRR9"
	peerB = "12D3KooWH3uVF6wv47WnArKHk5p6cvgCJEb74UTmxztmQDc298L3"
)

func TestGuardRejectsUnpairedPeer(t *testing.T) {
	q := newTrustStore(t)
	g := NewGuard(q)
	if g.Allowed(context.Background(), mustPeerID(t, peerA)) {
		t.Fatal("unpaired peer must not be allowed")
	}
	if _, err := g.DeviceFor(context.Background(), mustPeerID(t, peerA)); err != ErrUntrustedPeer {
		t.Fatalf("want ErrUntrustedPeer, got %v", err)
	}
}

func TestGuardAllowsPairedPeerAndBindsDevice(t *testing.T) {
	ctx := context.Background()
	q := newTrustStore(t)
	if err := q.CreateDevice(ctx, db.CreateDeviceParams{ID: "dev_a", Name: "a", TokenHash: "h1"}); err != nil {
		t.Fatal(err)
	}
	g := NewGuard(q)
	pid := mustPeerID(t, peerA)
	if err := g.Trust(ctx, pid, "dev_a", "laptop"); err != nil {
		t.Fatal(err)
	}
	got, err := g.DeviceFor(ctx, pid)
	if err != nil {
		t.Fatalf("paired peer rejected: %v", err)
	}
	if got != "dev_a" {
		t.Fatalf("want dev_a, got %q", got)
	}
	// A different peer is still untrusted — trust is per libp2p identity.
	if g.Allowed(ctx, mustPeerID(t, peerB)) {
		t.Fatal("unrelated peer must not inherit trust")
	}
}

func TestGuardTrustedPeersFiltersToPairedSet(t *testing.T) {
	ctx := context.Background()
	q := newTrustStore(t)
	if err := q.CreateDevice(ctx, db.CreateDeviceParams{ID: "dev_a", Name: "a", TokenHash: "h1"}); err != nil {
		t.Fatal(err)
	}
	g := NewGuard(q)
	if err := g.Trust(ctx, mustPeerID(t, peerA), "dev_a", "laptop"); err != nil {
		t.Fatal(err)
	}
	set, err := g.TrustedPeers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(set) != 1 {
		t.Fatalf("want 1 trusted peer, got %d", len(set))
	}
	if _, ok := set[mustPeerID(t, peerA)]; !ok {
		t.Fatal("paired peer missing from trusted set")
	}
	if _, ok := set[mustPeerID(t, peerB)]; ok {
		t.Fatal("unpaired peer present in trusted set")
	}
}

func TestGuardInvalidateDropsCache(t *testing.T) {
	ctx := context.Background()
	q := newTrustStore(t)
	if err := q.CreateDevice(ctx, db.CreateDeviceParams{ID: "dev_a", Name: "a", TokenHash: "h1"}); err != nil {
		t.Fatal(err)
	}
	g := NewGuard(q)
	pid := mustPeerID(t, peerA)
	if err := g.Trust(ctx, pid, "dev_a", "laptop"); err != nil {
		t.Fatal(err)
	}
	if !g.Allowed(ctx, pid) {
		t.Fatal("should be trusted")
	}
	if err := q.DeleteTrustedPeer(ctx, pid.String()); err != nil {
		t.Fatal(err)
	}
	g.Invalidate()
	if g.Allowed(ctx, pid) {
		t.Fatal("revoked peer must not remain trusted after Invalidate")
	}
}

func TestDecodeLimitedRejectsOversizeMessage(t *testing.T) {
	var v map[string]string
	big := `{"a":"` + strings.Repeat("x", 4096) + `"}`
	if err := decodeLimited(strings.NewReader(big), 512, &v); err == nil {
		t.Fatal("oversize message must be rejected")
	}
}

func TestDecodeLimitedAcceptsSmallMessage(t *testing.T) {
	var v map[string]string
	if err := decodeLimited(strings.NewReader(`{"a":"b"}`), 512, &v); err != nil {
		t.Fatalf("small message rejected: %v", err)
	}
	if v["a"] != "b" {
		t.Fatalf("bad decode: %v", v)
	}
}

func TestAttemptLimiterPerKeyCap(t *testing.T) {
	l := newAttemptLimiter(3, 100, time.Minute)
	for i := 0; i < 3; i++ {
		if !l.Allow("peer1") {
			t.Fatalf("attempt %d should be allowed", i)
		}
	}
	if l.Allow("peer1") {
		t.Fatal("4th attempt must be throttled")
	}
	// A different key has its own budget.
	if !l.Allow("peer2") {
		t.Fatal("independent key must not be throttled")
	}
}

func TestAttemptLimiterGlobalCap(t *testing.T) {
	l := newAttemptLimiter(10, 4, time.Minute)
	for i := 0; i < 4; i++ {
		if !l.Allow("peer" + string(rune('a'+i))) {
			t.Fatalf("attempt %d should be allowed", i)
		}
	}
	if l.Allow("peerZ") {
		t.Fatal("global cap must throttle a fresh key once exhausted")
	}
}

func TestAttemptLimiterWindowExpiry(t *testing.T) {
	l := newAttemptLimiter(2, 100, time.Minute)
	now := time.Now()
	l.now = func() time.Time { return now }
	if !l.Allow("p") || !l.Allow("p") {
		t.Fatal("first two attempts should pass")
	}
	if l.Allow("p") {
		t.Fatal("third attempt in window must be throttled")
	}
	now = now.Add(2 * time.Minute)
	if !l.Allow("p") {
		t.Fatal("attempt after window expiry should pass")
	}
}

func TestAttemptLimiterResetClearsBudget(t *testing.T) {
	l := newAttemptLimiter(2, 100, time.Minute)
	l.Allow("p")
	l.Allow("p")
	if l.Allow("p") {
		t.Fatal("should be throttled")
	}
	l.Reset("p")
	if !l.Allow("p") {
		t.Fatal("Reset must restore the per-key budget")
	}
}

func TestValidateRelPathRejectsTraversal(t *testing.T) {
	for _, bad := range []string{"", ".", "..", "../etc/passwd", "/etc/passwd", "a/../../b"} {
		if _, err := validateRelPath(bad); err == nil {
			t.Fatalf("validateRelPath(%q) should have failed", bad)
		}
	}
	if got, err := validateRelPath("Artist/Album/01.flac"); err != nil || got == "" {
		t.Fatalf("valid path rejected: %v", err)
	}
}

// The host key must be stable across restarts: a new key means a new peer ID,
// which would silently invalidate every existing pairing.
func TestIdentityIsStableAcrossRestarts(t *testing.T) {
	ctx := context.Background()
	q := newTrustStore(t)
	first, err := LoadOrCreateIdentity(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	second, err := LoadOrCreateIdentity(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Equals(second) {
		t.Fatal("identity changed between loads; peer ID would not survive a restart")
	}
	idA, err := peer.IDFromPrivateKey(first)
	if err != nil {
		t.Fatal(err)
	}
	idB, err := peer.IDFromPrivateKey(second)
	if err != nil {
		t.Fatal(err)
	}
	if idA != idB {
		t.Fatalf("peer ID changed: %s != %s", idA, idB)
	}
}

// An Ed25519 peer ID carries its own public key, which is what lets pairing
// bind a verification key without a separate exchange.
func TestPublicKeyBase64MatchesPeerIdentity(t *testing.T) {
	ctx := context.Background()
	q := newTrustStore(t)
	priv, err := LoadOrCreateIdentity(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := peer.IDFromPrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	got, err := PublicKeyBase64(pid)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := priv.GetPublic().Raw()
	if err != nil {
		t.Fatal(err)
	}
	if got != base64.StdEncoding.EncodeToString(raw) {
		t.Fatal("key derived from peer ID does not match the host key")
	}
}
