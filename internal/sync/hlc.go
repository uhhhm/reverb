package sync

import (
	"sync/atomic"
	"time"
)

// maxHLCDrift bounds how far ahead of local wall time an inbound HLC may push
// this device's clock.
//
// Pairing makes a device trusted, but trust covers what a peer *intends*, not
// what its clock says. A phone with a dead RTC, a VM restored from a snapshot
// or a bad NTP config reports a wall time years out. Without a bound, the first
// such value is adopted permanently -- there is no path back down -- so every
// local edit afterwards carries an absurd timestamp, that timestamp replicates
// to every other peer, and the bad device wins every subsequent LWW conflict.
//
// The window is wide enough to absorb ordinary skew between devices that are
// merely unsynchronised, and narrow enough that a broken clock cannot run away.
// Two mechanisms enforce it: withinDrift refuses an inbound change beyond the
// bound, and Observe clamps how far a peer's value may push this device's own
// clock.
const maxHLCDrift = 5 * time.Minute

// HLC is a hybrid logical clock: max(wallMillis, last+1).
// It gives a total order without a central server and tolerates wall skew.
// DeviceID lex breaks ties on equal HLC.
type HLC struct {
	last atomic.Int64
	// now reads local wall time, injectable for tests. A zero-value HLC falls
	// back to the real clock rather than losing the drift bound.
	now func() int64
}

// NewHLC creates a clock starting at 0.
func NewHLC() *HLC {
	return &HLC{now: func() int64 { return time.Now().UnixMilli() }}
}

// wall reads local wall time in millis.
func (h *HLC) wall() int64 {
	if h.now == nil {
		return time.Now().UnixMilli()
	}
	return h.now()
}

// withinDrift reports whether an HLC from a peer is close enough to local wall
// time to be trustworthy. It is the admission test for inbound changes: a value
// beyond the bound is refused outright rather than stored, because a stored row
// keeps its HLC verbatim (the value is covered by the change signature and
// cannot be rewritten) and PickWinner compares those stored values, so one
// poisoned row would out-rank every later edit to that field forever.
func (h *HLC) withinDrift(seen int64) bool {
	return seen <= h.wall()+maxHLCDrift.Milliseconds()
}

// Tick returns the next HLC value for wallMillis (UnixMilli from the caller).
// It is safe for concurrent use.
func (h *HLC) Tick(wallMillis int64) int64 {
	for {
		prev := h.last.Load()
		next := wallMillis
		if prev+1 > next {
			next = prev + 1
		}
		if h.last.CompareAndSwap(prev, next) {
			return next
		}
	}
}

// Observe advances the clock to at least seen (from a remote peer), never past
// maxHLCDrift ahead of local wall time.
//
// This is the second half of the defence, and it covers what withinDrift
// cannot. Refusing an inbound change keeps the poison out of the log, but a
// value already persisted by an earlier build is still read back by
// getMaxHLC at boot, and clamping here means it is not adopted again. The
// ceiling is relative to wall time rather than fixed, so it dissolves on its
// own as the clock catches up.
func (h *HLC) Observe(seen int64) {
	if bound := h.wall() + maxHLCDrift.Milliseconds(); seen > bound {
		seen = bound
	}
	for {
		prev := h.last.Load()
		if seen <= prev {
			return
		}
		if h.last.CompareAndSwap(prev, seen) {
			return
		}
	}
}

// Current returns last issued HLC without ticking.
func (h *HLC) Current() int64 { return h.last.Load() }
