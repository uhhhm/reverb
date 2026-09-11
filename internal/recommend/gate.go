package recommend

import (
	"context"
	"errors"
	"sync"
	"time"
)

const (
	// minSourceInterval spaces out calls to one rate-limited source. Last.fm
	// asks clients to stay under five requests a second.
	minSourceInterval = 250 * time.Millisecond
	backoffBase       = 30 * time.Second
	backoffMax        = 15 * time.Minute
)

var errBackingOff = errors.New("recommend: source is backing off after errors")

// gate spaces out calls to one source and, after it fails, stops calling it
// for a while that doubles with each consecutive failure. A failing source is
// usually a rate limit or an outage, and asking again at once makes both worse.
type gate struct {
	mu           sync.Mutex
	next         time.Time
	failures     int
	blockedUntil time.Time
}

// wait blocks until the source may be called, or returns errBackingOff.
func (g *gate) wait(ctx context.Context, now func() time.Time, sleep func(context.Context, time.Duration) error) error {
	g.mu.Lock()
	t := now()
	if t.Before(g.blockedUntil) {
		g.mu.Unlock()
		return errBackingOff
	}
	start := t
	if g.next.After(start) {
		start = g.next
	}
	g.next = start.Add(minSourceInterval)
	g.mu.Unlock()
	if d := start.Sub(t); d > 0 {
		return sleep(ctx, d)
	}
	return nil
}

func (g *gate) failed(now time.Time) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.failures++
	d := backoffBase << (g.failures - 1)
	if d > backoffMax || d <= 0 {
		d = backoffMax
	}
	g.blockedUntil = now.Add(d)
}

func (g *gate) succeeded() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.failures = 0
}

func sleepContext(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
