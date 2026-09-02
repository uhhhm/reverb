package p2p

import (
	"context"
	"sync/atomic"
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

// A panicking anti-entropy loop must come back. Containing the panic but
// leaving the loop dead would stop syncing for the life of the process while
// the app went on looking healthy.
func TestSafeGoLoopRestartsAfterPanic(t *testing.T) {
	old := safeLoopRestartDelayForTest
	safeLoopRestartDelayForTest = 5 * time.Millisecond
	defer func() { safeLoopRestartDelayForTest = old }()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runs := make(chan int, 4)
	var n int32
	SafeGoLoop(ctx, "test", func() {
		i := atomic.AddInt32(&n, 1)
		runs <- int(i)
		if i < 3 {
			panic("boom")
		}
		<-ctx.Done()
	})
	for want := 1; want <= 3; want++ {
		select {
		case got := <-runs:
			if got != want {
				t.Fatalf("run %d, want %d", got, want)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("loop did not restart (run %d)", want)
		}
	}
}

// A loop that returns on its own -- context cancelled, work done -- is finished,
// not broken, and must not be restarted.
func TestSafeGoLoopDoesNotRestartCleanReturn(t *testing.T) {
	old := safeLoopRestartDelayForTest
	safeLoopRestartDelayForTest = time.Millisecond
	defer func() { safeLoopRestartDelayForTest = old }()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var n int32
	SafeGoLoop(ctx, "test", func() { atomic.AddInt32(&n, 1) })
	time.Sleep(50 * time.Millisecond)
	if got := atomic.LoadInt32(&n); got != 1 {
		t.Fatalf("ran %d times, want 1", got)
	}
}

func TestSafeRunContainsPanic(t *testing.T) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		SafeRun("test", func() { panic("boom") })
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("SafeRun did not return")
	}
}
