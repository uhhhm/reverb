package p2p

import (
	"log"
	"runtime/debug"

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
