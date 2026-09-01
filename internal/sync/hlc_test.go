package sync

import (
	"testing"
	"time"
)

// fixedNow returns a clock function pinned to t, for tests that need to reason
// about the drift bound without racing real time.
func fixedNow(t time.Time) func() int64 {
	return func() int64 { return t.UnixMilli() }
}

func TestObserveAdvancesWithinDrift(t *testing.T) {
	now := time.Now()
	h := NewHLC()
	h.now = fixedNow(now)

	seen := now.Add(30 * time.Second).UnixMilli()
	h.Observe(seen)
	if got := h.Current(); got != seen {
		t.Fatalf("Current() = %d, want %d: a peer slightly ahead must still advance the clock", got, seen)
	}
}

func TestObserveClampsFarFutureHLC(t *testing.T) {
	now := time.Now()
	h := NewHLC()
	h.now = fixedNow(now)

	// A peer with a broken RTC claiming to be a decade ahead.
	h.Observe(now.Add(10 * 365 * 24 * time.Hour).UnixMilli())

	bound := now.UnixMilli() + maxHLCDrift.Milliseconds()
	if got := h.Current(); got > bound {
		t.Fatalf("Current() = %d, want <= %d: a far-future peer HLC must not be adopted", got, bound)
	}
}

func TestTickStaysSaneAfterFarFutureObserve(t *testing.T) {
	now := time.Now()
	h := NewHLC()
	h.now = fixedNow(now)

	h.Observe(now.Add(10 * 365 * 24 * time.Hour).UnixMilli())

	// Local edits made afterwards must not inherit an absurd timestamp, which
	// is the part that outlives the bad peer and poisons every other device.
	got := h.Tick(now.UnixMilli())
	// +1 because Tick keeps the clock strictly monotonic (max(wall, last+1));
	// the point is that it sits at the drift ceiling, not a decade past it.
	bound := now.UnixMilli() + maxHLCDrift.Milliseconds() + 1
	if got > bound {
		t.Fatalf("Tick() = %d, want <= %d: local edits inherited the poisoned clock", got, bound)
	}
}

func TestObserveIgnoresValuesBelowCurrent(t *testing.T) {
	now := time.Now()
	h := NewHLC()
	h.now = fixedNow(now)

	h.Observe(now.UnixMilli())
	h.Observe(now.Add(-time.Hour).UnixMilli())
	if got := h.Current(); got != now.UnixMilli() {
		t.Fatalf("Current() = %d, want %d: a stale peer must not move the clock backwards", got, now.UnixMilli())
	}
}

func TestClockHealsAsWallTimeAdvances(t *testing.T) {
	start := time.Now()
	h := NewHLC()
	cur := start
	h.now = func() int64 { return cur.UnixMilli() }

	h.Observe(start.Add(10 * 365 * 24 * time.Hour).UnixMilli())
	clamped := h.Current()

	// Once wall time passes the clamped value the clock tracks wall again,
	// so the bound is a temporary ceiling rather than a permanent offset.
	cur = start.Add(2 * maxHLCDrift)
	if got := h.Tick(cur.UnixMilli()); got <= clamped {
		t.Fatalf("Tick() = %d, want > %d: clock failed to resume tracking wall time", got, clamped)
	}
}
