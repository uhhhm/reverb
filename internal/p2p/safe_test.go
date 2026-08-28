package p2p

import (
	"context"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
)

// A panic in a stream handler must not escape: libp2p does not recover, so an
// unwrapped panic would take the whole app down when a peer dials us.
func TestSafeHandlerContainsPanic(t *testing.T) {
	a, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	if err != nil {
		t.Fatalf("host a: %v", err)
	}
	defer a.Close()
	b, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	if err != nil {
		t.Fatalf("host b: %v", err)
	}
	defer b.Close()

	const proto = "/reverb/test/1.0.0"
	a.SetStreamHandler(proto, safeHandler("test", func(s network.Stream) {
		panic("boom")
	}))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := b.Connect(ctx, peer.AddrInfo{ID: a.ID(), Addrs: a.Addrs()}); err != nil {
		t.Fatalf("connect: %v", err)
	}
	s, err := b.NewStream(ctx, a.ID(), proto)
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	defer s.Close()
	// The panic is contained; the stream is reset rather than the process dying.
	_ = s.SetDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, 1)
	if _, err := s.Read(buf); err == nil {
		t.Fatal("expected reset stream after handler panic")
	}
}

func TestSafeGoContainsPanic(t *testing.T) {
	done := make(chan struct{})
	SafeGo("test", func() {
		defer close(done)
		panic("boom")
	})
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("goroutine never ran")
	}
}
