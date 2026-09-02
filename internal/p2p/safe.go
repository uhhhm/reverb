package p2p

import (
	"context"
	"log"
	"runtime/debug"
	"time"

	"github.com/libp2p/go-libp2p/core/network"
)

// safeHandler wraps a stream handler so a panic inside it cannot take the
// process down.
//
// libp2p runs every inbound stream in its own goroutine and does not recover,
// so an unhandled panic in any handler is fatal to the whole app — a peer
// coming online and dialing us would kill an otherwise healthy instance. The
// stream is reset so the caller sees a failed exchange rather than a hang, and
// the next anti-entropy round retries.
func safeHandler(name string, h network.StreamHandler) network.StreamHandler {
	return func(s network.Stream) {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("p2p %s handler panic from %s: %v\n%s", name, s.Conn().RemotePeer(), r, debug.Stack())
				_ = s.Reset()
			}
		}()
		h(s)
	}
}

// SafeGo runs fn in a goroutine whose panic is logged rather than fatal. The
// background p2p loops (anti-entropy, file scanning) run for the life of the
// process; a panic in one should cost that loop, not the app.
func SafeGo(name string, fn func()) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("p2p %s goroutine panic: %v\n%s", name, r, debug.Stack())
			}
		}()
		fn()
	}()
}

// SafeRun runs fn on the calling goroutine and contains a panic. Use it inside
// a goroutine that is not started by SafeGo -- a per-peer worker in a
// WaitGroup, say, where the goroutine has to stay a plain `go func` so the
// group still gets its Done.
func SafeRun(name string, fn func()) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("p2p %s panic: %v\n%s", name, r, debug.Stack())
		}
	}()
	fn()
}

// safeLoopRestartDelayForTest spaces out restarts so a loop panicking on every
// iteration cannot spin the CPU. A variable only so tests need not wait it out.
var safeLoopRestartDelayForTest = 5 * time.Second

// SafeGoLoop runs a long-lived loop in a goroutine and restarts it after a
// panic, until ctx is done. SafeGo alone would contain the panic but leave the
// loop dead for the life of the process: anti-entropy would stop silently, and
// the app would go on looking healthy while no longer syncing.
func SafeGoLoop(ctx context.Context, name string, fn func()) {
	go func() {
		for ctx.Err() == nil {
			panicked := func() (panicked bool) {
				defer func() {
					if r := recover(); r != nil {
						panicked = true
						log.Printf("p2p %s loop panic, restarting: %v\n%s", name, r, debug.Stack())
					}
				}()
				fn()
				return false
			}()
			// A clean return means the loop finished on its own terms --
			// ctx cancelled, or nothing left to do. Only a panic is worth
			// restarting for.
			if !panicked || ctx.Err() != nil {
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(safeLoopRestartDelayForTest):
			}
		}
	}()
}
