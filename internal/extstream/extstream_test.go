package extstream

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/uhhhm/reverb/internal/core"
)

type fakeLookup struct {
	track core.ExternalResult
	err   error
	calls int
}

func (f *fakeLookup) GetTrack(_ context.Context, source, id string) (core.ExternalResult, error) {
	f.calls++
	if f.err != nil {
		return core.ExternalResult{}, f.err
	}
	t := f.track
	t.Source, t.ExternalID = source, id
	return t, nil
}

type fakeRunner struct {
	lines []string
	err   error

	mu      sync.Mutex
	calls   int
	gotArgs []string
	block   chan struct{} // when non-nil, Run waits on it before returning
}

func (f *fakeRunner) Run(ctx context.Context, name string, args []string, onLine func(string)) error {
	f.mu.Lock()
	f.calls++
	f.gotArgs = args
	block := f.block
	f.mu.Unlock()
	if block != nil {
		<-block
	}
	for _, l := range f.lines {
		onLine(l)
	}
	return f.err
}

func (f *fakeRunner) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func newService(t *testing.T, l *fakeLookup, r *fakeRunner, opts ...Option) *Service {
	t.Helper()
	return New(l, append([]Option{WithRunner(r)}, opts...)...)
}

func TestResolveReturnsDirectAudioURL(t *testing.T) {
	l := &fakeLookup{track: core.ExternalResult{Artist: "Air", Title: "Alone in Kyoto"}}
	r := &fakeRunner{lines: []string{"https://rr1.googlevideo.com/audio?x=1"}}
	got, err := newService(t, l, r).Resolve(context.Background(), "deezer", "123")
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://rr1.googlevideo.com/audio?x=1" {
		t.Fatalf("url = %q", got)
	}
	args := strings.Join(r.gotArgs, " ")
	if !strings.Contains(args, "-g") || !strings.Contains(args, "bestaudio") {
		t.Errorf("args %q must ask yt-dlp for a bestaudio URL, not a download", args)
	}
	if !strings.Contains(args, "ytsearch1:Air - Alone in Kyoto") {
		t.Errorf("args %q must search for the looked-up artist/title", args)
	}
}

// yt-dlp prints progress and warnings alongside the URL; only the URL matters.
func TestResolveIgnoresNonURLOutput(t *testing.T) {
	l := &fakeLookup{track: core.ExternalResult{Artist: "Air", Title: "Alone in Kyoto"}}
	r := &fakeRunner{lines: []string{"WARNING: something", "", "https://rr1.googlevideo.com/a"}}
	got, err := newService(t, l, r).Resolve(context.Background(), "deezer", "123")
	if err != nil || got != "https://rr1.googlevideo.com/a" {
		t.Fatalf("got %q, %v", got, err)
	}
}

// Resolving costs seconds, so a repeat play must not spawn another yt-dlp.
func TestResolveCachesUntilTTLExpires(t *testing.T) {
	l := &fakeLookup{track: core.ExternalResult{Artist: "Air", Title: "Alone in Kyoto"}}
	r := &fakeRunner{lines: []string{"https://rr1.googlevideo.com/a"}}
	now := time.Unix(1000, 0)
	s := newService(t, l, r, WithTTL(time.Minute), WithClock(func() time.Time { return now }))

	for i := 0; i < 3; i++ {
		if _, err := s.Resolve(context.Background(), "deezer", "123"); err != nil {
			t.Fatal(err)
		}
	}
	if r.callCount() != 1 {
		t.Fatalf("yt-dlp ran %d times, want 1 (cached)", r.callCount())
	}

	now = now.Add(2 * time.Minute)
	if _, err := s.Resolve(context.Background(), "deezer", "123"); err != nil {
		t.Fatal(err)
	}
	if r.callCount() != 2 {
		t.Fatalf("yt-dlp ran %d times, want a re-resolve after the TTL", r.callCount())
	}
}

// A URL can expire early; Invalidate is how the proxy forces a re-resolve.
func TestInvalidateForcesReresolve(t *testing.T) {
	l := &fakeLookup{track: core.ExternalResult{Artist: "Air", Title: "Alone in Kyoto"}}
	r := &fakeRunner{lines: []string{"https://rr1.googlevideo.com/a"}}
	s := newService(t, l, r)
	if _, err := s.Resolve(context.Background(), "deezer", "123"); err != nil {
		t.Fatal(err)
	}
	s.Invalidate("deezer", "123")
	if _, err := s.Resolve(context.Background(), "deezer", "123"); err != nil {
		t.Fatal(err)
	}
	if r.callCount() != 2 {
		t.Fatalf("yt-dlp ran %d times, want 2", r.callCount())
	}
}

// A player that retries, or two listeners, must not each spawn a yt-dlp.
func TestConcurrentResolvesCollapseToOneRun(t *testing.T) {
	l := &fakeLookup{track: core.ExternalResult{Artist: "Air", Title: "Alone in Kyoto"}}
	r := &fakeRunner{lines: []string{"https://rr1.googlevideo.com/a"}, block: make(chan struct{})}
	s := newService(t, l, r)

	var wg sync.WaitGroup
	errs := make([]error, 5)
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = s.Resolve(context.Background(), "deezer", "123")
		}(i)
	}
	// Let the goroutines pile onto the singleflight before the run completes.
	time.Sleep(20 * time.Millisecond)
	close(r.block)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("caller %d: %v", i, err)
		}
	}
	if r.callCount() != 1 {
		t.Fatalf("yt-dlp ran %d times, want 1", r.callCount())
	}
}

func TestResolveErrors(t *testing.T) {
	for _, tc := range []struct {
		name   string
		lookup *fakeLookup
		runner *fakeRunner
		source string
		id     string
	}{
		{"blank source", &fakeLookup{}, &fakeRunner{}, "", "123"},
		{"blank id", &fakeLookup{}, &fakeRunner{}, "deezer", ""},
		{"lookup fails", &fakeLookup{err: errors.New("deezer down")}, &fakeRunner{}, "deezer", "123"},
		{"no title", &fakeLookup{track: core.ExternalResult{Artist: "Air"}}, &fakeRunner{}, "deezer", "123"},
		{"yt-dlp fails", &fakeLookup{track: core.ExternalResult{Artist: "Air", Title: "T"}}, &fakeRunner{err: errors.New("exit 1")}, "deezer", "123"},
		{"no url printed", &fakeLookup{track: core.ExternalResult{Artist: "Air", Title: "T"}}, &fakeRunner{lines: []string{"ERROR: unavailable"}}, "deezer", "123"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := newService(t, tc.lookup, tc.runner).Resolve(context.Background(), tc.source, tc.id); err == nil {
				t.Fatal("want an error")
			}
		})
	}
}

// A failed resolve must not be cached as success.
func TestFailedResolveIsNotCached(t *testing.T) {
	l := &fakeLookup{track: core.ExternalResult{Artist: "Air", Title: "T"}}
	r := &fakeRunner{err: errors.New("exit 1")}
	s := newService(t, l, r)
	if _, err := s.Resolve(context.Background(), "deezer", "123"); err == nil {
		t.Fatal("want an error")
	}
	r.mu.Lock()
	r.err, r.lines = nil, []string{"https://rr1.googlevideo.com/a"}
	r.mu.Unlock()
	if _, err := s.Resolve(context.Background(), "deezer", "123"); err != nil {
		t.Fatalf("second attempt should re-run: %v", err)
	}
}
