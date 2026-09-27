package playlistsync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/uhhhm/reverb/internal/core"
)

// Ways concurrent edits to one managed playlist can go wrong, each covered below:
//
//  1. Two AddTracks read the same tracklist and each write back only their own
//     addition, so one request's tracks vanish.
//  2. A Rename or SetCover writes back the TracksJSON it read, erasing tracks an
//     AddTracks wrote in between; or an AddTracks writes back the old name.
//  3. Adapter reload builds a new Service while an edit on the old one is still
//     running, so two instances race unless they share the same locks.
//  4. An edit that fails (not found, not editable) keeps its lock and every later
//     edit of that playlist hangs.
//  5. A lock is taken per service instead of per playlist, so a slow edit of one
//     playlist blocks edits of every other one.

// slowStore is a goroutine-safe memStore whose Get pauses before returning, so
// every caller that reads and then writes has a wide window to interleave.
type slowStore struct {
	mu    sync.Mutex
	inner *memStore
	pause time.Duration
}

func newSlowStore() *slowStore {
	return &slowStore{inner: newMemStore(), pause: 200 * time.Microsecond}
}

func (s *slowStore) Upsert(ctx context.Context, p core.SyncedPlaylist, tj string, at int64) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.inner.Upsert(ctx, p, tj, at)
}

func (s *slowStore) Get(ctx context.Context, id string) (SyncedRow, error) {
	s.mu.Lock()
	row, err := s.inner.Get(ctx, id)
	s.mu.Unlock()
	time.Sleep(s.pause)
	return row, err
}

func (s *slowStore) List(ctx context.Context) ([]SyncedRow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.inner.List(ctx)
}

func (s *slowStore) ListDue(ctx context.Context, now int64) ([]SyncedRow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.inner.ListDue(ctx, now)
}

func (s *slowStore) UpdateTracks(ctx context.Context, id, name, cover, tj string, at int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.inner.UpdateTracks(ctx, id, name, cover, tj, at)
}

func (s *slowStore) UpdateSettings(ctx context.Context, id string, enabled bool, interval int, auto bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.inner.UpdateSettings(ctx, id, enabled, interval, auto)
}

func (s *slowStore) Delete(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.inner.Delete(ctx, id)
}

func (s *slowStore) row(t *testing.T, id string) SyncedRow {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	row, err := s.inner.Get(context.Background(), id)
	if err != nil {
		t.Fatalf("Get %s: %v", id, err)
	}
	return row
}

// countingEmitter records how many times each playlist was published.
type countingEmitter struct {
	mu sync.Mutex
	n  map[string]int
}

func (e *countingEmitter) Publish(_ context.Context, id string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.n == nil {
		e.n = map[string]int{}
	}
	e.n[id]++
}

func (e *countingEmitter) count(id string) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.n[id]
}

func newConcurrentSvc(store Store) *Service {
	return NewService(nil, fakeMatcher{}, &fakeDownloader{}, store, nil,
		func() int64 { return 100 }, seqID(), nil)
}

func managed(t *testing.T, svc *Service, name string) string {
	t.Helper()
	det, err := svc.CreateManaged(context.Background(), name)
	if err != nil {
		t.Fatalf("CreateManaged: %v", err)
	}
	return det.ID
}

func tracksOf(t *testing.T, row SyncedRow) []core.ExternalResult {
	t.Helper()
	var tracks []core.ExternalResult
	if err := json.Unmarshal([]byte(row.TracksJSON), &tracks); err != nil {
		t.Fatalf("decode tracks: %v", err)
	}
	return tracks
}

// requireExactly fails unless tracks holds every id in want exactly once and
// nothing else.
func requireExactly(t *testing.T, tracks []core.ExternalResult, want []string) {
	t.Helper()
	seen := map[string]int{}
	for _, tr := range tracks {
		seen[tr.ExternalID]++
	}
	var missing []string
	for _, id := range want {
		if seen[id] != 1 {
			missing = append(missing, fmt.Sprintf("%s(x%d)", id, seen[id]))
		}
		delete(seen, id)
	}
	if len(missing) > 0 || len(seen) > 0 {
		t.Fatalf("tracklist has %d entries; wrong counts %v, unexpected %v", len(tracks), missing, seen)
	}
}

// runAll starts every fn at once and waits for all, failing on any error.
func runAll(t *testing.T, fns []func() error) {
	t.Helper()
	start := make(chan struct{})
	errs := make(chan error, len(fns))
	var wg sync.WaitGroup
	for _, fn := range fns {
		wg.Add(1)
		go func(fn func() error) {
			defer wg.Done()
			<-start
			errs <- fn()
		}(fn)
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
}

// Case 1: many requests, each adding a batch of distinct tracks, all land.
func TestConcurrentAddTracksKeepsEveryEntry(t *testing.T) {
	store := newSlowStore()
	em := &countingEmitter{}
	svc := newConcurrentSvc(store).WithEmitter(em)
	id := managed(t, svc, "Party")
	publishedAtCreate := em.count(id)

	const requests, perRequest = 24, 3
	var want []string
	var fns []func() error
	for r := 0; r < requests; r++ {
		var batch []core.ExternalResult
		for i := 0; i < perRequest; i++ {
			tid := fmt.Sprintf("r%d-t%d", r, i)
			want = append(want, tid)
			batch = append(batch, track(tid))
		}
		// Every other request also re-adds a track owned by another request, so
		// dedupe runs against a tracklist other goroutines are changing.
		if r > 0 && r%2 == 0 {
			batch = append(batch, track(fmt.Sprintf("r%d-t0", r-1)))
		}
		fns = append(fns, func() error {
			_, _, err := svc.AddTracks(context.Background(), id, batch, false)
			return err
		})
	}
	runAll(t, fns)

	requireExactly(t, tracksOf(t, store.row(t, id)), want)
	if got := em.count(id) - publishedAtCreate; got < 1 || got > requests {
		t.Fatalf("published %d times for %d add requests", got, requests)
	}
}

// Case 2: adds, removes, renames, cover changes and reorders racing on one
// playlist each keep their effect.
func TestConcurrentMixedEditsKeepEveryEffect(t *testing.T) {
	store := newSlowStore()
	svc := newConcurrentSvc(store)
	id := managed(t, svc, "Before")
	if _, _, err := svc.AddTracks(context.Background(), id,
		[]core.ExternalResult{track("keep"), track("drop1"), track("drop2")}, false); err != nil {
		t.Fatal(err)
	}

	var want = []string{"keep"}
	var fns []func() error
	for i := 0; i < 16; i++ {
		tid := fmt.Sprintf("add%d", i)
		want = append(want, tid)
		fns = append(fns, func() error {
			_, err := svc.AddTrack(context.Background(), id, track(tid))
			return err
		})
	}
	for _, drop := range []string{"drop1", "drop2"} {
		fns = append(fns, func() error {
			_, err := svc.RemoveTrack(context.Background(), id, "spotify", drop)
			return err
		})
	}
	fns = append(fns,
		func() error { _, err := svc.Rename(context.Background(), id, "After"); return err },
		func() error { _, err := svc.SetCover(context.Background(), id, "cover://new"); return err },
		func() error {
			_, err := svc.ReorderTracks(context.Background(), id, []core.TrackKey{{Source: "spotify", ExternalID: "keep"}})
			return err
		},
	)
	runAll(t, fns)

	row := store.row(t, id)
	requireExactly(t, tracksOf(t, row), want)
	if row.Name != "After" || row.CoverURL != "cover://new" {
		t.Fatalf("name %q cover %q: a track edit wrote back stale fields", row.Name, row.CoverURL)
	}
}

// Case 3: a Service rebuilt by adapter reload shares its predecessor's locks,
// so edits in flight on both instances do not overwrite each other.
func TestConcurrentAddsAcrossRebuiltServicesSharingLocks(t *testing.T) {
	store := newSlowStore()
	locks := NewEditLocks()
	before := newConcurrentSvc(store).WithEditLocks(locks)
	after := newConcurrentSvc(store).WithEditLocks(locks)
	id := managed(t, before, "Reloaded")

	var want []string
	var fns []func() error
	for i := 0; i < 20; i++ {
		svc := before
		if i%2 == 1 {
			svc = after
		}
		tid := fmt.Sprintf("t%d", i)
		want = append(want, tid)
		fns = append(fns, func() error {
			_, _, err := svc.AddTracks(context.Background(), id, []core.ExternalResult{track(tid)}, false)
			return err
		})
	}
	runAll(t, fns)

	requireExactly(t, tracksOf(t, store.row(t, id)), want)
}

// Case 4: failed edits release the lock.
func TestFailedEditsReleaseTheLock(t *testing.T) {
	store := newSlowStore()
	svc := newConcurrentSvc(store)
	id := managed(t, svc, "Mine")
	syncedID, err := store.Upsert(context.Background(),
		core.SyncedPlaylist{ID: "synced", Source: "spotify", ExternalID: "PL", Name: "Synced", Mode: "synced"}, "[]", 1)
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 3; i++ {
		if _, _, err := svc.AddTracks(context.Background(), syncedID, []core.ExternalResult{track("x")}, false); !errors.Is(err, ErrNotEditable) {
			t.Fatalf("AddTracks on synced: err = %v, want ErrNotEditable", err)
		}
		if _, err := svc.RemoveTrack(context.Background(), "missing", "spotify", "x"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("RemoveTrack on missing: err = %v, want ErrNotFound", err)
		}
	}

	done := make(chan error, 2)
	go func() { _, err := svc.Rename(context.Background(), syncedID, "Renamed"); done <- err }()
	go func() { _, err := svc.AddTrack(context.Background(), id, track("after")); done <- err }()
	for i := 0; i < 2; i++ {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("edit after a failed edit never finished: lock was not released")
		}
	}
}

// Case 5: holding one playlist's lock does not block another playlist.
func TestEditLockIsPerPlaylist(t *testing.T) {
	store := newSlowStore()
	locks := NewEditLocks()
	svc := newConcurrentSvc(store).WithEditLocks(locks)
	a := managed(t, svc, "A")
	b := managed(t, svc, "B")

	unlock := locks.lock(a)
	defer unlock()

	done := make(chan error, 1)
	go func() { _, err := svc.AddTrack(context.Background(), b, track("t")); done <- err }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("edit of B waited on A's lock")
	}
}
