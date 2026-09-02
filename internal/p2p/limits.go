package p2p

import (
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"time"
)

// Wire size caps. Every decoder on a p2p stream reads through a LimitReader so
// a peer cannot stream unbounded JSON into memory. The caps are generous
// relative to legitimate traffic and exist to bound the worst case, not to
// tune it.
const (
	maxPairRequestBytes = 4 << 10  // pairing: two short strings
	maxFileRequestBytes = 8 << 10  // file request: a rel path and a hash
	maxSyncMessageBytes = 32 << 20 // sync: up to 10k changes in one batch
	maxFileBytes        = 8 << 30  // a single replicated media file
)

// decodeLimited decodes one JSON value from r, reading at most limit bytes.
// It reports a distinct error when the cap is hit so callers can tell a
// malformed message from an oversized one.
func decodeLimited(r io.Reader, limit int64, v any) error {
	lr := &io.LimitedReader{R: r, N: limit + 1}
	dec := json.NewDecoder(lr)
	if err := dec.Decode(v); err != nil {
		if lr.N <= 0 {
			return fmt.Errorf("message exceeds %d byte limit", limit)
		}
		return err
	}
	if lr.N <= 0 {
		return fmt.Errorf("message exceeds %d byte limit", limit)
	}
	return nil
}

// Pairing brute-force limits. A code is 8 chars from a 32-symbol alphabet
// (2^40 keyspace) and lives for 10 minutes, so these bounds put an exhaustive
// search far out of reach while leaving room for a user fumbling the code.
const (
	pairAttemptsPerPeer   = 5
	pairAttemptsGlobal    = 30
	pairAttemptWindow     = 15 * time.Minute
	pairLimiterGlobalKey  = "\x00global"
	pairLimiterGCInterval = 30 * time.Minute
)

type attemptWindow struct {
	stamps []time.Time
}

// attemptLimiter is a fixed-window-per-key attempt counter. Keys are libp2p
// peer IDs for the p2p path and client IPs for the HTTP path.
type attemptLimiter struct {
	mu       sync.Mutex
	windows  map[string]*attemptWindow
	max      int
	globalMx int
	window   time.Duration
	lastGC   time.Time
	now      func() time.Time // injectable for tests
}

func newAttemptLimiter(max, globalMax int, window time.Duration) *attemptLimiter {
	return &attemptLimiter{
		windows:  make(map[string]*attemptWindow),
		max:      max,
		globalMx: globalMax,
		window:   window,
		now:      time.Now,
	}
}

func (l *attemptLimiter) prune(w *attemptWindow, now time.Time) {
	cutoff := now.Add(-l.window)
	keep := w.stamps[:0]
	for _, t := range w.stamps {
		if t.After(cutoff) {
			keep = append(keep, t)
		}
	}
	w.stamps = keep
}

// Allow records an attempt for key and reports whether it may proceed. It
// counts every attempt, not just failures: a legitimate pairing needs one.
func (l *attemptLimiter) Allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()

	if now.Sub(l.lastGC) > pairLimiterGCInterval {
		for k, w := range l.windows {
			l.prune(w, now)
			if len(w.stamps) == 0 && k != pairLimiterGlobalKey {
				delete(l.windows, k)
			}
		}
		l.lastGC = now
	}

	get := func(k string) *attemptWindow {
		w, ok := l.windows[k]
		if !ok {
			w = &attemptWindow{}
			l.windows[k] = w
		}
		l.prune(w, now)
		return w
	}

	gw := get(pairLimiterGlobalKey)
	kw := get(key)
	if len(kw.stamps) >= l.max || len(gw.stamps) >= l.globalMx {
		return false
	}
	kw.stamps = append(kw.stamps, now)
	gw.stamps = append(gw.stamps, now)
	return true
}

// Reset clears the counter for key, called after a successful pairing so a
// paired device is not left throttled. The attempts it recorded are dropped
// from the global window too: peer IDs are free to mint, so leaving them there
// would let failed pairings from anyone burn a budget that a legitimate device
// then cannot spend for a full window.
func (l *attemptLimiter) Reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if key == pairLimiterGlobalKey {
		return
	}
	w, ok := l.windows[key]
	if !ok {
		return
	}
	delete(l.windows, key)
	gw, ok := l.windows[pairLimiterGlobalKey]
	if !ok {
		return
	}
	// Remove one global stamp per attempt this key contributed. Stamps carry no
	// key, so drop the oldest, which is the most likely to fall out anyway.
	drop := len(w.stamps)
	if drop > len(gw.stamps) {
		drop = len(gw.stamps)
	}
	gw.stamps = append(gw.stamps[:0], gw.stamps[drop:]...)
}

// AllowPairAttempt records a pairing attempt for key (a peer ID or client IP)
// and reports whether it may proceed. The HTTP redeem endpoint and the libp2p
// pairing handler share one limiter so the global cap covers both.
func AllowPairAttempt(key string) bool { return pairLimiter.Allow(key) }

// ResetPairAttempts clears the counter for key after a successful pairing.
func ResetPairAttempts(key string) { pairLimiter.Reset(key) }

// ClearAll drops every window, global included.
func (l *attemptLimiter) ClearAll() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.windows = make(map[string]*attemptWindow)
}

// RearmPairAttempts clears the whole attempt budget, called when the owner
// generates a pairing code on this device. Peer IDs are free to mint, so
// without this any unpaired peer that can reach the host could exhaust the
// global budget and lock legitimate pairing out for a full window. Generating
// a code is a local, authenticated action, and the code's own 10-minute TTL
// over a 2^40 keyspace is what bounds guessing -- the counter only has to
// cover the window that one code is live.
func RearmPairAttempts() { pairLimiter.ClearAll() }
