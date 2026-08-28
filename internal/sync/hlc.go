package sync

import "sync/atomic"

// HLC is a hybrid logical clock: max(wallMillis, last+1).
// It gives a total order without a central server and tolerates wall skew.
// DeviceID lex breaks ties on equal HLC.
type HLC struct {
	last atomic.Int64
}

// NewHLC creates a clock starting at 0.
func NewHLC() *HLC { return &HLC{} }

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

// Observe advances the clock to at least seen (from a remote peer) and returns it.
func (h *HLC) Observe(seen int64) {
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
