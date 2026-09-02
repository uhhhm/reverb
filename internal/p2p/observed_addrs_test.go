package p2p

import (
	"testing"

	"github.com/multiformats/go-multiaddr"
)

func mustAddrs(t *testing.T, raw ...string) []multiaddr.Multiaddr {
	t.Helper()
	out := make([]multiaddr.Multiaddr, 0, len(raw))
	for _, r := range raw {
		a, err := multiaddr.NewMultiaddr(r)
		if err != nil {
			t.Fatalf("multiaddr %q: %v", r, err)
		}
		out = append(out, a)
	}
	return out
}

// The accepting side of a connection sees the dialer's ephemeral source port,
// which dies with that connection. Persisting it would fill the peer's bounded
// address list with addresses that can never be dialed, and the acceptor would
// never learn a usable one. Its advertised listen address is used instead.
func TestSelectObservedAddrsPrefersListenAddrOverInboundSourcePort(t *testing.T) {
	suffix := mustAddrs(t, "/p2p/"+peerA)[0]
	listen := mustAddrs(t, "/ip4/10.8.0.2/tcp/4331")

	got := selectObservedAddrs(nil, false, listen, suffix)
	want := "/ip4/10.8.0.2/tcp/4331/p2p/" + peerA
	if len(got) != 1 || got[0] != want {
		t.Fatalf("inbound-only = %v, want [%s]", got, want)
	}
}

// An outbound connection's remote address is by construction one we dialed, so
// it is exactly what should be stored.
func TestSelectObservedAddrsUsesOutboundRemote(t *testing.T) {
	suffix := mustAddrs(t, "/p2p/"+peerA)[0]
	out := mustAddrs(t, "/ip4/10.8.0.2/tcp/4331")
	stale := mustAddrs(t, "/ip4/10.8.0.2/tcp/51234")

	got := selectObservedAddrs(out, true, stale, suffix)
	want := "/ip4/10.8.0.2/tcp/4331/p2p/" + peerA
	if len(got) != 1 || got[0] != want {
		t.Fatalf("outbound = %v, want [%s]", got, want)
	}
}

func TestSelectObservedAddrsDropsUndialable(t *testing.T) {
	suffix := mustAddrs(t, "/p2p/"+peerA)[0]
	got := selectObservedAddrs(mustAddrs(t, "/ip4/127.0.0.1/tcp/4331", "/ip4/0.0.0.0/tcp/4331"), true, nil, suffix)
	if len(got) != 0 {
		t.Fatalf("got %v, want none", got)
	}
}
