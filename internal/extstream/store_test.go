package extstream

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/uhhhm/reverb/internal/core"
	"github.com/uhhhm/reverb/internal/store"
)

func newStore(t *testing.T) Store {
	t.Helper()
	st, err := store.Open(t.TempDir() + "/extstream.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return st.Q()
}

// The signed URL stays valid for hours, but the in-memory cache dies with the
// process. Persisting it is what makes replaying a recent track instant after a
// restart instead of a fresh multi-second resolve.
func TestStoredURLSurvivesANewService(t *testing.T) {
	db := newStore(t)
	now := time.Unix(1_000_000, 0)
	clock := func() time.Time { return now }
	url := fmt.Sprintf("https://rr1.googlevideo.com/a?expire=%d", now.Add(6*time.Hour).Unix())

	first := &fakeRunner{lines: []string{url}}
	l := &fakeLookup{track: core.ExternalResult{Artist: "Air", Title: "Alone in Kyoto"}}
	s := New(l, WithRunner(first), WithStore(db), WithClock(clock))
	if _, err := s.Resolve(context.Background(), "deezer", "123"); err != nil {
		t.Fatal(err)
	}

	// A new Service is a new process: nothing in memory, everything in the store.
	second := &fakeRunner{lines: []string{"https://should-not-run.example/x"}}
	fresh := New(l, WithRunner(second), WithStore(db), WithClock(clock))
	got, err := fresh.Resolve(context.Background(), "deezer", "123")
	if err != nil {
		t.Fatal(err)
	}
	if got != url {
		t.Errorf("url = %q, want the persisted one", got)
	}
	if second.callCount() != 0 || second.searchCount() != 0 {
		t.Errorf("yt-dlp ran (%d search, %d media), want none", second.searchCount(), second.callCount())
	}
}

// A URL close to its expiry must not be handed out: the listener would get audio
// that dies partway through the track.
func TestStoredURLNearingExpiryIsReresolved(t *testing.T) {
	db := newStore(t)
	now := time.Unix(1_000_000, 0)
	clock := func() time.Time { return now }
	l := &fakeLookup{track: core.ExternalResult{Artist: "Air", Title: "Alone in Kyoto"}}

	stale := fmt.Sprintf("https://rr1.googlevideo.com/a?expire=%d", now.Add(2*time.Minute).Unix())
	first := &fakeRunner{lines: []string{stale}}
	if _, err := New(l, WithRunner(first), WithStore(db), WithClock(clock)).
		Resolve(context.Background(), "deezer", "123"); err != nil {
		t.Fatal(err)
	}

	second := &fakeRunner{lines: []string{"https://rr1.googlevideo.com/fresh"}}
	got, err := New(l, WithRunner(second), WithStore(db), WithClock(clock)).
		Resolve(context.Background(), "deezer", "123")
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://rr1.googlevideo.com/fresh" {
		t.Errorf("url = %q, want a fresh resolve", got)
	}
}

// Which upstream track this is never changes, so the search stage — half the
// resolve — must never run twice for the same track, even once the URL has
// lapsed and the process has restarted.
func TestSearchRunsOnceEverPerTrack(t *testing.T) {
	db := newStore(t)
	now := time.Unix(1_000_000, 0)
	clock := func() time.Time { return now }
	l := &fakeLookup{track: core.ExternalResult{Artist: "Air", Title: "Alone in Kyoto"}}

	expired := fmt.Sprintf("https://rr1.googlevideo.com/a?expire=%d", now.Add(time.Minute).Unix())
	first := &fakeRunner{lines: []string{expired}, searchLines: []string{"vidABC"}}
	if _, err := New(l, WithRunner(first), WithStore(db), WithClock(clock)).
		Resolve(context.Background(), "deezer", "123"); err != nil {
		t.Fatal(err)
	}
	if first.searchCount() != 1 {
		t.Fatalf("first resolve searched %d times, want 1", first.searchCount())
	}

	second := &fakeRunner{lines: []string{"https://rr1.googlevideo.com/fresh"}}
	if _, err := New(l, WithRunner(second), WithStore(db), WithClock(clock)).
		Resolve(context.Background(), "deezer", "123"); err != nil {
		t.Fatal(err)
	}
	if second.searchCount() != 0 {
		t.Errorf("re-resolve searched %d times, want 0 — the video id is permanent", second.searchCount())
	}
	if second.callCount() != 1 {
		t.Errorf("re-resolve extracted %d times, want 1", second.callCount())
	}
	if got := second.gotArgs[len(second.gotArgs)-1]; got != watchURL("vidABC") {
		t.Errorf("extracted %q, want the stored video id", got)
	}
}

// Invalidate is the proxy reporting that the upstream rejected a URL. The video
// id it was derived from is still the right track, so only the URL goes.
func TestInvalidateKeepsTheVideoID(t *testing.T) {
	db := newStore(t)
	now := time.Unix(1_000_000, 0)
	clock := func() time.Time { return now }
	l := &fakeLookup{track: core.ExternalResult{Artist: "Air", Title: "Alone in Kyoto"}}

	url := fmt.Sprintf("https://rr1.googlevideo.com/a?expire=%d", now.Add(6*time.Hour).Unix())
	r := &fakeRunner{lines: []string{url}, searchLines: []string{"vidABC"}}
	s := New(l, WithRunner(r), WithStore(db), WithClock(clock))
	if _, err := s.Resolve(context.Background(), "deezer", "123"); err != nil {
		t.Fatal(err)
	}
	s.Invalidate("deezer", "123")
	if _, err := s.Resolve(context.Background(), "deezer", "123"); err != nil {
		t.Fatal(err)
	}
	if r.searchCount() != 1 {
		t.Errorf("searched %d times, want 1 — invalidation drops the URL, not the id", r.searchCount())
	}
	if r.callCount() != 2 {
		t.Errorf("extracted %d times, want 2", r.callCount())
	}
}

// A stored id that stops resolving (video pulled, region-locked) must not wedge
// the track forever.
func TestStaleVideoIDFallsBackToSearching(t *testing.T) {
	db := newStore(t)
	now := time.Unix(1_000_000, 0)
	clock := func() time.Time { return now }
	l := &fakeLookup{track: core.ExternalResult{Artist: "Air", Title: "Alone in Kyoto"}}

	expired := fmt.Sprintf("https://rr1.googlevideo.com/a?expire=%d", now.Add(time.Minute).Unix())
	seed := &fakeRunner{lines: []string{expired}, searchLines: []string{"deadVid"}}
	if _, err := New(l, WithRunner(seed), WithStore(db), WithClock(clock)).
		Resolve(context.Background(), "deezer", "123"); err != nil {
		t.Fatal(err)
	}

	dead := &deadIDRunner{good: "https://rr1.googlevideo.com/fresh", newID: "liveVid"}
	got, err := New(l, WithRunner(dead), WithStore(db), WithClock(clock)).
		Resolve(context.Background(), "deezer", "123")
	if err != nil {
		t.Fatalf("a dead stored id must fall back to searching: %v", err)
	}
	if got != "https://rr1.googlevideo.com/fresh" {
		t.Errorf("url = %q", got)
	}
	if dead.searches != 1 {
		t.Errorf("searched %d times, want 1 fallback search", dead.searches)
	}
}

// deadIDRunner fails format extraction for the stored id and succeeds for the
// one a fresh search finds.
type deadIDRunner struct {
	good     string
	newID    string
	searches int
}

func (d *deadIDRunner) Run(_ context.Context, _ string, args []string, onLine func(string)) error {
	for _, a := range args {
		if a == "--flat-playlist" {
			d.searches++
			onLine(d.newID)
			return nil
		}
	}
	if args[len(args)-1] == watchURL(d.newID) {
		onLine(d.good)
		return nil
	}
	return fmt.Errorf("video unavailable")
}
