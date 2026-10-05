package download

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/uhhhm/reverb/internal/catalog"
	"github.com/uhhhm/reverb/internal/core"
	"github.com/uhhhm/reverb/internal/events"
	"github.com/uhhhm/reverb/internal/registry"
	"github.com/uhhhm/reverb/internal/resolver"
)

// ---- fakes ----

// fakeDL is a controllable downloader. canDownload gates the fallback chain;
// block lets a test hold a download open (to assert dedup-join while in-flight).
// errOnStart, if non-nil, makes Start return that error immediately.
type fakeDL struct {
	name        string
	canDownload bool
	block       chan struct{} // if non-nil, Start blocks until closed/canceled
	errOnStart  error         // if non-nil, Start returns this error
	mu          sync.Mutex
	startCount  int
	received    []core.DownloadRequest
}

func (d *fakeDL) Type() string { return "downloader" }
func (d *fakeDL) Name() string { return d.name }
func (d *fakeDL) SupportedGranularities() []core.DownloadGranularity {
	return []core.DownloadGranularity{core.GranularityTrack}
}
func (d *fakeDL) ConfigSchema() registry.ConfigSchema  { return registry.ConfigSchema{} }
func (d *fakeDL) Init(map[string]any) error            { return nil }
func (d *fakeDL) TestConnection(context.Context) error { return nil }
func (d *fakeDL) CanDownload(context.Context, core.DownloadRequest) (bool, error) {
	return d.canDownload, nil
}
func (d *fakeDL) Start(ctx context.Context, req core.DownloadRequest, onProgress func(int)) (string, error) {
	d.mu.Lock()
	d.startCount++
	d.received = append(d.received, req)
	err := d.errOnStart
	d.mu.Unlock()
	if err != nil {
		return "", err
	}
	onProgress(50)
	if d.block != nil {
		select {
		case <-d.block:
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	onProgress(100)
	return "/out/" + req.ExternalID + ".mp3", nil
}
func (d *fakeDL) starts() int { d.mu.Lock(); defer d.mu.Unlock(); return d.startCount }
func (d *fakeDL) requests() []core.DownloadRequest {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]core.DownloadRequest(nil), d.received...)
}

// setErrOnStart sets errOnStart under the mutex, safe for use after the
// Manager has started dispatching jobs to this fakeDL (i.e. concurrently
// with worker goroutines calling Start).
func (d *fakeDL) setErrOnStart(err error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.errOnStart = err
}

// fakeScanner records StartScan calls and models the Navidrome scan lifecycle.
//
// By default it reports a completed scan immediately (scanning=false) — the
// already-idle / instantaneous-scan path. When statusSeq is set, ScanStatus
// returns each element in turn (then sticks on the last), letting a test drive the
// realistic false→true→…→false transition that waitForScan must wait through.
type fakeScanner struct {
	mu        sync.Mutex
	scans     int
	statusSeq []bool // scanning values returned in order; nil → always false
	statusIdx int
	statusN   int // number of ScanStatus calls observed
}

func (s *fakeScanner) StartScan(context.Context) error {
	s.mu.Lock()
	s.scans++
	// Reset the status sequence cursor each scan so reused scanners replay it.
	s.statusIdx = 0
	s.mu.Unlock()
	return nil
}
func (s *fakeScanner) ScanStatus(context.Context) (core.ScanStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.statusN++
	scanning := false
	if len(s.statusSeq) > 0 {
		if s.statusIdx >= len(s.statusSeq) {
			scanning = s.statusSeq[len(s.statusSeq)-1]
		} else {
			scanning = s.statusSeq[s.statusIdx]
			s.statusIdx++
		}
	}
	return core.ScanStatus{Scanning: scanning, Count: 1}, nil
}
func (s *fakeScanner) count() int       { s.mu.Lock(); defer s.mu.Unlock(); return s.scans }
func (s *fakeScanner) statusCalls() int { s.mu.Lock(); defer s.mu.Unlock(); return s.statusN }

// fakeRematcher returns a fixed in-library match and records the last ExternalResult it saw.
type fakeRematcher struct {
	trackID    string
	coverArtID string // optional; when set, returned in the MatchResult
	mu         sync.Mutex
	lastReq    core.ExternalResult
}

func (r *fakeRematcher) Match(_ context.Context, ext core.ExternalResult) (core.MatchResult, error) {
	r.mu.Lock()
	r.lastReq = ext
	r.mu.Unlock()
	if r.trackID == "" {
		return core.MatchResult{Status: core.MatchNotInLibrary, Method: core.MatchNone}, nil
	}
	return core.MatchResult{Status: core.MatchInLibrary, LibraryTrackID: r.trackID, CoverArtID: r.coverArtID, Method: core.MatchFuzzy, Confidence: 0.9}, nil
}

func (r *fakeRematcher) getLastReq() core.ExternalResult {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.lastReq
}

// fakeVersion is an in-memory VersionBumper.
type fakeVersion struct {
	mu sync.Mutex
	v  int64
}

func (f *fakeVersion) LibraryVersion(context.Context) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.v == 0 {
		f.v = 1
	}
	return f.v, nil
}
func (f *fakeVersion) SetLibraryVersion(_ context.Context, v int64) error {
	f.mu.Lock()
	f.v = v
	f.mu.Unlock()
	return nil
}
func (f *fakeVersion) get() int64 { f.mu.Lock(); defer f.mu.Unlock(); return f.v }

// memStore is an in-memory JobStore (no SQLite) for fast concurrency tests.
type memStore struct {
	mu   sync.Mutex
	jobs map[string]core.DownloadJob
	reqs map[string]core.DownloadRequest // mirrors request_json
}

type completionWriteStore struct {
	*memStore
	failUpdate atomic.Bool
}

func (s *completionWriteStore) Update(ctx context.Context, job core.DownloadJob) error {
	if s.failUpdate.Load() {
		return errors.New("job database unavailable")
	}
	return s.memStore.Update(ctx, job)
}

func newMemStore() *memStore {
	return &memStore{jobs: map[string]core.DownloadJob{}, reqs: map[string]core.DownloadRequest{}}
}

func (s *memStore) Insert(_ context.Context, j core.DownloadJob, req core.DownloadRequest) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.jobs[j.ID] = j
	s.reqs[j.ID] = req
	return nil
}
func (s *memStore) Get(_ context.Context, id string) (core.DownloadJob, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[id]
	return j, ok, nil
}
func (s *memStore) ActiveByDedup(_ context.Context, dedup string) (core.DownloadJob, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, j := range s.jobs {
		if j.DedupKey == dedup && (j.Status == core.DownloadQueued || j.Status == core.DownloadRunning) {
			return j, true, nil
		}
	}
	return core.DownloadJob{}, false, nil
}
func (s *memStore) GetByDedup(_ context.Context, dedup string) (core.DownloadJob, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, j := range s.jobs {
		if j.DedupKey == dedup {
			return j, true, nil
		}
	}
	return core.DownloadJob{}, false, nil
}
func (s *memStore) List(_ context.Context) ([]core.DownloadJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]core.DownloadJob, 0, len(s.jobs))
	for _, j := range s.jobs {
		out = append(out, j)
	}
	return out, nil
}
func (s *memStore) Update(_ context.Context, j core.DownloadJob) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.jobs[j.ID]; !ok {
		return ErrJobNotFound
	}
	s.jobs[j.ID] = j
	return nil
}

func (s *memStore) UpdateRequest(_ context.Context, id string, req core.DownloadRequest) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reqs[id] = req
	return nil
}

func (s *memStore) Delete(_ context.Context, id string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[id]
	if !ok || j.CompletionPending || (j.Status != core.DownloadCompleted && j.Status != core.DownloadFailed && j.Status != core.DownloadCanceled) {
		return false, nil
	}
	delete(s.jobs, id)
	delete(s.reqs, id)
	return true, nil
}

func (s *memStore) DeleteFinished(_ context.Context) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var ids []string
	for id, j := range s.jobs {
		if j.Status == core.DownloadCompleted || j.Status == core.DownloadFailed || j.Status == core.DownloadCanceled {
			ids = append(ids, id)
			delete(s.jobs, id)
			delete(s.reqs, id)
		}
	}
	return ids, nil
}

func (s *memStore) UpdateProgress(_ context.Context, id string, attempt, progress int) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[id]
	if !ok || j.Status != core.DownloadRunning || j.CompletionPending || j.Attempts != attempt {
		return false, nil
	}
	j.Progress = progress
	s.jobs[id] = j
	return true, nil
}

func (s *memStore) UpdateCanonicalID(_ context.Context, id string, canonicalID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if j, ok := s.jobs[id]; ok {
		j.CanonicalID = canonicalID
		s.jobs[id] = j
	}
	return nil
}

func (s *memStore) getReq(id string) (core.DownloadRequest, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.reqs[id]
	return r, ok
}

func (s *memStore) GetRequest(_ context.Context, id string) (core.DownloadRequest, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.reqs[id]
	return r, ok, nil
}

// wrapDownloaders wraps a []Downloader into []DownloaderEntry using default
// ordering: each granularity in SupportedGranularities() gets order 0 (same
// priority). This mirrors the wiring.BuildDownloaders default.
func wrapDownloaders(downloaders []Downloader) []DownloaderEntry {
	entries := make([]DownloaderEntry, 0, len(downloaders))
	for _, d := range downloaders {
		order := make(map[core.DownloadGranularity]int, len(d.SupportedGranularities()))
		for _, g := range d.SupportedGranularities() {
			order[g] = 0
		}
		entries = append(entries, DownloaderEntry{Downloader: d, Order: order})
	}
	return entries
}

func testManager(t *testing.T, downloaders []Downloader, store JobStore, rematch Rematcher, ver VersionBumper, clk Clock) (*Manager, *events.Bus) {
	t.Helper()
	bus := events.New()
	scanner := &fakeScanner{}
	if rematch == nil {
		rematch = &fakeRematcher{trackID: "t1"}
	}
	if ver == nil {
		ver = &fakeVersion{v: 1}
	}
	if clk == nil {
		clk = RealClock{}
	}
	m := NewManager(Config{Workers: 2, DebounceWindow: 5 * time.Second, ScanPollEvery: time.Millisecond, ScanPollMax: time.Second, ScanSettleMax: 10 * time.Millisecond},
		wrapDownloaders(downloaders), store, bus, scanner, rematch, ver, clk, nil, nil)
	t.Cleanup(m.Stop)
	m.Start()
	return m, bus
}

func TestEnqueuePicksDownloaderViaFallback(t *testing.T) {
	cant := &fakeDL{name: "cant", canDownload: false}
	can := &fakeDL{name: "can", canDownload: true}
	store := newMemStore()
	m, _ := testManager(t, []Downloader{cant, can}, store, nil, nil, nil)

	job, err := m.Enqueue(context.Background(), core.DownloadRequest{Source: "spotify", ExternalID: "e1", Artist: "A", Title: "T", Album: "Al"})
	if err != nil {
		t.Fatal(err)
	}
	if job.DownloaderName != "can" {
		t.Fatalf("fallback should pick 'can', got %q", job.DownloaderName)
	}
}

func TestCompletionHookReceivesSuccessfulRequest(t *testing.T) {
	dl := &fakeDL{name: "dl", canDownload: true}
	store := newMemStore()
	m := NewManager(Config{Workers: 1, DebounceWindow: time.Hour}, wrapDownloaders([]Downloader{dl}), store,
		events.New(), &fakeScanner{}, &fakeRematcher{trackID: "t1"}, &fakeVersion{v: 1}, RealClock{}, nil, nil)
	type completion struct {
		req  core.DownloadRequest
		path string
	}
	completed := make(chan completion, 1)
	m.SetCompletionHook(func(_ context.Context, req core.DownloadRequest, path string) error {
		completed <- completion{req, path}
		return nil
	})
	m.Start()
	t.Cleanup(m.Stop)

	_, err := m.Enqueue(context.Background(), core.DownloadRequest{
		Source: "spotify", ExternalID: "e1", Artist: "A", Title: "T",
		RecommendationOrigin: "radio", InitiatedBy: "local",
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-completed:
		if got.req.RecommendationOrigin != "radio" || got.req.InitiatedBy != "local" {
			t.Fatalf("completed request = %+v", got.req)
		}
		if got.path != "/out/e1.mp3" {
			t.Fatalf("completed at %q, want the downloader's output", got.path)
		}
	case <-time.After(time.Second):
		t.Fatal("completion hook was not called")
	}
}

func TestCompletionHookFailureRetriesExistingOutputOnAdd(t *testing.T) {
	dl := &fakeDL{name: "dl", canDownload: true}
	store := newMemStore()
	m := NewManager(Config{Workers: 1, DebounceWindow: time.Hour, ReconcileEvery: time.Hour}, wrapDownloaders([]Downloader{dl}), store,
		events.New(), &fakeScanner{}, &fakeRematcher{trackID: "t1"}, &fakeVersion{v: 1}, RealClock{}, nil, nil)
	var fail atomic.Bool
	fail.Store(true)
	m.SetCompletionHook(func(context.Context, core.DownloadRequest, string) error {
		if fail.Load() {
			return errors.New("pending upload database unavailable")
		}
		return nil
	})
	m.Start()
	t.Cleanup(m.Stop)

	req := core.DownloadRequest{
		Source: "spotify", ExternalID: "e1", Artist: "A", Title: "T",
	}
	job, err := m.Enqueue(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	waitForCompletionError(t, store, job.ID)
	got, _, err := store.Get(context.Background(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != core.DownloadRunning || !strings.Contains(got.Error, "pending upload database unavailable") {
		t.Fatalf("job = %+v, want active with the recording error", got)
	}
	if got.OutputPath != "/out/e1.mp3" {
		t.Fatalf("output path = %q, want the downloaded file retained for retry", got.OutputPath)
	}
	if err := m.Cancel(context.Background(), job.ID); err == nil {
		t.Fatal("Cancel discarded an output awaiting its pending-upload record")
	}
	if ids, err := m.ClearFinished(context.Background()); err != nil || len(ids) != 0 {
		t.Fatalf("ClearFinished removed pending output: ids=%v err=%v", ids, err)
	}
	fail.Store(false)
	joined, err := m.Enqueue(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if joined.ID != job.ID || joined.Status != core.DownloadCompleted || dl.starts() != 1 {
		t.Fatalf("re-Add = %+v, downloader starts = %d; want same completed job and one download", joined, dl.starts())
	}
}

func TestCompletionHookFailureRecoversAfterRestartWithoutRedownload(t *testing.T) {
	store := newMemStore()
	dl := &fakeDL{name: "dl", canDownload: true}
	newManager := func() *Manager {
		return NewManager(Config{Workers: 1, DebounceWindow: time.Hour, ReconcileEvery: time.Hour},
			wrapDownloaders([]Downloader{dl}), store, events.New(), &fakeScanner{},
			&fakeRematcher{trackID: "t1"}, &fakeVersion{v: 1}, RealClock{}, nil, nil)
	}
	m := newManager()
	m.SetCompletionHook(func(context.Context, core.DownloadRequest, string) error {
		return errors.New("pending upload database unavailable")
	})
	m.Start()
	job, err := m.Enqueue(context.Background(), core.DownloadRequest{Source: "spotify", ExternalID: "e1", Artist: "A", Title: "T"})
	if err != nil {
		t.Fatal(err)
	}
	waitForCompletionError(t, store, job.ID)
	m.Stop()

	restarted := newManager()
	restarted.SetCompletionHook(func(_ context.Context, _ core.DownloadRequest, path string) error {
		if path != "/out/e1.mp3" {
			t.Errorf("recovered output path = %q", path)
		}
		return nil
	})
	restarted.Start()
	t.Cleanup(restarted.Stop)
	waitForStatus(t, store, job.ID, core.DownloadCompleted)
	if dl.starts() != 1 {
		t.Fatalf("downloader started %d times, want once", dl.starts())
	}
}

func TestCompletionHookFailureRetainsOutputWhenJobUpdateAlsoFails(t *testing.T) {
	store := &completionWriteStore{memStore: newMemStore()}
	dl := &fakeDL{name: "dl", canDownload: true, block: make(chan struct{})}
	m := NewManager(Config{Workers: 1, DebounceWindow: time.Hour, ReconcileEvery: 10 * time.Millisecond},
		wrapDownloaders([]Downloader{dl}), store, events.New(), &fakeScanner{},
		&fakeRematcher{trackID: "t1"}, &fakeVersion{v: 1}, RealClock{}, nil, nil)
	var failHook atomic.Bool
	failHook.Store(true)
	m.SetCompletionHook(func(context.Context, core.DownloadRequest, string) error {
		if failHook.Load() {
			return errors.New("pending upload database unavailable")
		}
		return nil
	})
	m.Start()
	t.Cleanup(m.Stop)
	job, err := m.Enqueue(context.Background(), core.DownloadRequest{Source: "spotify", ExternalID: "e1", Artist: "A", Title: "T"})
	if err != nil {
		t.Fatal(err)
	}
	waitForStatus(t, store, job.ID, core.DownloadRunning)
	store.failUpdate.Store(true)
	close(dl.block)
	deadline := time.Now().Add(3 * time.Second)
	pending := false
	for time.Now().Before(deadline) {
		m.mu.Lock()
		_, pending = m.unrecorded[job.ID]
		m.mu.Unlock()
		if pending {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !pending {
		t.Fatal("download output was not retained in memory during the store outage")
	}
	stored, _, err := store.Get(context.Background(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.OutputPath != "" {
		t.Fatalf("output path unexpectedly persisted during outage: %q", stored.OutputPath)
	}
	store.failUpdate.Store(false)
	failHook.Store(false)
	waitForStatus(t, store, job.ID, core.DownloadCompleted)
	if dl.starts() != 1 {
		t.Fatalf("downloader started %d times, want once", dl.starts())
	}
}

func waitForCompletionError(t *testing.T, store JobStore, id string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		job, ok, err := store.Get(context.Background(), id)
		if err == nil && ok && job.CompletionPending && job.Error != "" {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("job %s did not retain its completion error", id)
}

func TestEnqueueNoDownloaderAccepts(t *testing.T) {
	cant := &fakeDL{name: "cant", canDownload: false}
	store := newMemStore()
	m, _ := testManager(t, []Downloader{cant}, store, nil, nil, nil)
	_, err := m.Enqueue(context.Background(), core.DownloadRequest{Source: "spotify", ExternalID: "e1", Title: "T"})
	if err == nil {
		t.Fatal("expected error when no downloader accepts")
	}
}

func TestEnqueueFallbackPicksFirstCanDownload(t *testing.T) {
	// With two track downloaders and both able to download, the first (a) wins.
	a := &fakeDL{name: "a", canDownload: true}
	b := &fakeDL{name: "b", canDownload: true}
	store := newMemStore()
	m, _ := testManager(t, []Downloader{a, b}, store, nil, nil, nil)
	job, err := m.Enqueue(context.Background(), core.DownloadRequest{Source: "s", ExternalID: "e", Title: "T"})
	if err != nil {
		t.Fatal(err)
	}
	if job.DownloaderName != "a" {
		t.Fatalf("fallback should pick first CanDownload downloader 'a', got %q", job.DownloaderName)
	}
}

// -- Task 2: Granularity-scoped pick() tests --

// The album downloader is listed first and accepts every request, so only the
// Manager's granularity scoping keeps it away from track requests.
func TestPickScopesDownloadersToRequestGranularity(t *testing.T) {
	for _, tc := range []struct {
		name        string
		granularity core.DownloadGranularity
		want        string
	}{
		{"empty defaults to track", "", "spotdl"},
		{"track", core.GranularityTrack, "spotdl"},
		{"album", core.GranularityAlbum, "lidarr"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			album := &fakeAsyncDL{name: "lidarr", submitRef: "ref"}
			track := &fakeDL{name: "spotdl", canDownload: true}
			m, _ := testManager(t, []Downloader{album, track}, newMemStore(), nil, nil, nil)
			job, err := m.Enqueue(context.Background(), core.DownloadRequest{
				Source: "spotify", ExternalID: "e1", Artist: "A", Title: "T", Album: "Al", Granularity: tc.granularity,
			})
			if err != nil {
				t.Fatal(err)
			}
			if job.DownloaderName != tc.want {
				t.Fatalf("picked %q, want %q", job.DownloaderName, tc.want)
			}
		})
	}
}

func TestPickRefusesAGranularityNoDownloaderServes(t *testing.T) {
	for _, tc := range []struct {
		name        string
		downloader  Downloader
		granularity core.DownloadGranularity
		want        string
	}{
		{"album request, track downloader only", &fakeDL{name: "spotdl", canDownload: true}, core.GranularityAlbum, "no album downloader"},
		{"track request, album downloader only", &fakeAsyncDL{name: "lidarr", submitRef: "ref"}, core.GranularityTrack, "no track downloader"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, _ := testManager(t, []Downloader{tc.downloader}, newMemStore(), nil, nil, nil)
			_, err := m.Enqueue(context.Background(), core.DownloadRequest{
				Source: "spotify", ExternalID: "e1", Artist: "A", Title: "T", Album: "Al", Granularity: tc.granularity,
			})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to contain %q", err, tc.want)
			}
		})
	}
}

func TestDedupJoinWhileInFlight(t *testing.T) {
	block := make(chan struct{})
	dl := &fakeDL{name: "dl", canDownload: true, block: block}
	store := newMemStore()
	m, _ := testManager(t, []Downloader{dl}, store, nil, nil, nil)

	req := core.DownloadRequest{Source: "spotify", ExternalID: "e1", Artist: "A", Title: "T", Album: "Al"}
	j1, err := m.Enqueue(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	// Wait until the worker has actually started the in-flight download.
	deadline := time.After(2 * time.Second)
	for dl.starts() == 0 {
		select {
		case <-deadline:
			t.Fatal("download never started")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	// A second identical request must JOIN the in-flight job, not start a new one.
	j2, err := m.Enqueue(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if j2.ID != j1.ID {
		t.Fatalf("dedup-join failed: j2=%q j1=%q", j2.ID, j1.ID)
	}
	close(block)
	if dl.starts() != 1 {
		t.Fatalf("download should have started exactly once, got %d", dl.starts())
	}
}

func TestEnqueueReturnsExistingTerminalJobByExternalID(t *testing.T) {
	store := newMemStore()
	existing := core.DownloadJob{ID: "failed-job", DedupKey: "old-key", Status: core.DownloadFailed, DownloaderName: "dl", Source: "spotify", ExternalID: "stable-id", Artist: "Old Artist", Title: "Old Title"}
	if err := store.Insert(context.Background(), existing, core.DownloadRequest{Source: "spotify", ExternalID: "stable-id", Artist: "Old Artist", Title: "Old Title"}); err != nil {
		t.Fatal(err)
	}
	dl := &fakeDL{name: "dl", canDownload: true}
	m, _ := testManager(t, []Downloader{dl}, store, nil, nil, nil)

	got, err := m.Enqueue(context.Background(), core.DownloadRequest{Source: "spotify", ExternalID: "stable-id", Artist: "New Artist", Title: "New Display Title", Album: "New Album"})
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != existing.ID {
		t.Fatalf("enqueue = %q, want existing terminal job %q", got.ID, existing.ID)
	}
	if dl.starts() != 0 {
		t.Fatalf("terminal duplicate started %d downloader calls, want 0", dl.starts())
	}
}

func TestConcurrentEnqueueSameKeyOneJob(t *testing.T) {
	block := make(chan struct{})
	dl := &fakeDL{name: "dl", canDownload: true, block: block}
	store := newMemStore()
	m, _ := testManager(t, []Downloader{dl}, store, nil, nil, nil)
	req := core.DownloadRequest{Source: "spotify", ExternalID: "e1", Artist: "A", Title: "T", Album: "Al"}

	const n = 8
	ids := make([]string, n)
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			j, err := m.Enqueue(context.Background(), req)
			if err != nil {
				t.Errorf("enqueue: %v", err)
				return
			}
			ids[i] = j.ID
		}(i)
	}
	wg.Wait()
	close(block)
	for i := 1; i < n; i++ {
		if ids[i] != ids[0] {
			t.Fatalf("concurrent same-key enqueues produced different jobs: %v", ids)
		}
	}
}

// fakeTimer is a scheduled AfterFunc the fakeClock controls.
type fakeTimer struct {
	at      time.Time
	fn      func()
	stopped bool
}

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
	fns []*fakeTimer
}

func newFakeClock() *fakeClock { return &fakeClock{now: time.Unix(1_700_000_000, 0)} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) AfterFunc(d time.Duration, f func()) func() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := &fakeTimer{at: c.now.Add(d), fn: f}
	c.fns = append(c.fns, t)
	return func() bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		if t.stopped {
			return false
		}
		t.stopped = true
		return true
	}
}

func (c *fakeClock) scheduledCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.fns)
}

// Advance moves time forward and fires all timers now due (in order).
func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	var due []*fakeTimer
	for _, t := range c.fns {
		if !t.stopped && !t.at.After(c.now) {
			t.stopped = true
			due = append(due, t)
		}
	}
	c.mu.Unlock()
	for _, t := range due {
		t.fn()
	}
}

func TestCompletionDebouncesIntoOneScan(t *testing.T) {
	clk := newFakeClock()
	dl := &fakeDL{name: "dl", canDownload: true}
	store := newMemStore()
	scanner := &fakeScanner{}
	ver := &fakeVersion{v: 1}
	bus := events.New()
	m := NewManager(Config{Workers: 3, DebounceWindow: 5 * time.Second, ScanPollEvery: time.Millisecond, ScanPollMax: time.Second, ScanSettleMax: 10 * time.Millisecond},
		wrapDownloaders([]Downloader{dl}), store, bus, scanner, &fakeRematcher{trackID: "t1"}, ver, clk, nil, nil)
	t.Cleanup(m.Stop)
	m.Start()

	// Enqueue several distinct jobs; each completes quickly (no block).
	for i := 0; i < 4; i++ {
		_, err := m.Enqueue(context.Background(), core.DownloadRequest{
			Source: "spotify", ExternalID: string(rune('a' + i)), Artist: "A", Title: "T" + string(rune('a'+i)), Album: "Al",
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	// Wait for all 4 downloads to finish (they schedule debounced scans).
	deadline := time.After(2 * time.Second)
	for {
		jobs, _ := store.List(context.Background())
		done := 0
		for _, j := range jobs {
			if j.Status == core.DownloadCompleted || j.Status == core.DownloadFailed {
				done++
			}
		}
		if done == 4 {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("downloads did not complete (done=%d)", done)
		default:
			time.Sleep(time.Millisecond)
		}
	}

	if scanner.count() != 0 {
		t.Fatalf("scan must NOT fire before the debounce window elapses, got %d", scanner.count())
	}
	// Advance past the window: the coalesced completions trigger exactly ONE scan.
	clk.Advance(5 * time.Second)
	// The scan + poll + rematch + version bump runs synchronously in the timer fn.
	if scanner.count() != 1 {
		t.Fatalf("expected exactly 1 coalesced StartScan, got %d", scanner.count())
	}
	if ver.get() != 2 {
		t.Fatalf("library_version must bump from 1 to 2 on scan completion, got %d", ver.get())
	}
}

func TestCompletionSetsLibraryTrackIDAndPublishesComplete(t *testing.T) {
	clk := newFakeClock()
	dl := &fakeDL{name: "dl", canDownload: true}
	store := newMemStore()
	bus := events.New()
	rematcher := &fakeRematcher{trackID: "lib-track-9", coverArtID: "mf-lib-track-9_abc123"}
	m := NewManager(Config{Workers: 1, DebounceWindow: 5 * time.Second, ScanPollEvery: time.Millisecond, ScanPollMax: time.Second, ScanSettleMax: 10 * time.Millisecond},
		wrapDownloaders([]Downloader{dl}), store, bus, &fakeScanner{}, rematcher, &fakeVersion{v: 1}, clk, nil, nil)
	t.Cleanup(m.Stop)
	m.Start()

	// Subscribe to the complete topic BEFORE enqueuing so we don't miss the post-scan event.
	completeCh, unsub := bus.Subscribe(TopicComplete)
	defer unsub()

	// Collect all TopicComplete events in the background.
	var completeEvents []core.DownloadEvent
	var ceMu sync.Mutex
	go func() {
		for ev := range completeCh {
			if de, ok := ev.Payload.(core.DownloadEvent); ok {
				ceMu.Lock()
				completeEvents = append(completeEvents, de)
				ceMu.Unlock()
			}
		}
	}()

	job, err := m.Enqueue(context.Background(), core.DownloadRequest{Source: "spotify", ExternalID: "e1", Artist: "A", Title: "T", Album: "Al"})
	if err != nil {
		t.Fatal(err)
	}

	// Wait until the job is completed in the store (worker finished downloading).
	deadline := time.After(2 * time.Second)
	for {
		cur, _, _ := store.Get(context.Background(), job.ID)
		if cur.Status == core.DownloadCompleted {
			break
		}
		select {
		case <-deadline:
			t.Fatal("job never completed")
		default:
			time.Sleep(time.Millisecond)
		}
	}

	// Advance the fake clock to fire the debounced scan → re-match → set library_track_id.
	// runScan executes synchronously inside Advance, so by the time Advance returns
	// the re-match has run, the store is updated, and publishComplete has been called.
	clk.Advance(5 * time.Second)

	// Regression test (Fix 1): assert the rematcher received real metadata, not an empty
	// ExternalResult. An empty Title means the candidate query has nothing to search
	// → MatchNotInLibrary → library_track_id never set → the loop never closes.
	lastReq := rematcher.getLastReq()
	if lastReq.Title == "" {
		t.Fatal("re-match ExternalResult.Title is empty: manager passed no metadata to the rematcher (regression)")
	}
	if lastReq.Artist == "" {
		t.Fatal("re-match ExternalResult.Artist is empty: manager passed no metadata to the rematcher (regression)")
	}
	if lastReq.Title != "T" {
		t.Fatalf("re-match ExternalResult.Title: got %q want %q", lastReq.Title, "T")
	}
	if lastReq.Artist != "A" {
		t.Fatalf("re-match ExternalResult.Artist: got %q want %q", lastReq.Artist, "A")
	}

	// Assert the store reflects the re-matched library_track_id and cover_art_id.
	cur, _, _ := store.Get(context.Background(), job.ID)
	if cur.LibraryTrackID != "lib-track-9" {
		t.Fatalf("library_track_id not set after re-match, got %q", cur.LibraryTrackID)
	}
	if cur.CoverArtID != "mf-lib-track-9_abc123" {
		t.Fatalf("cover_art_id not set after re-match, got %q (needed for home recently-downloaded covers)", cur.CoverArtID)
	}

	// Allow the goroutine a moment to deliver the event (channel send is non-blocking;
	// goroutine scheduling may not have run yet).
	deadline2 := time.After(time.Second)
	for {
		ceMu.Lock()
		n := len(completeEvents)
		ceMu.Unlock()
		if n >= 2 { // first emit: job completes; second emit: post-scan publishComplete
			break
		}
		select {
		case <-deadline2:
			// Give it one final check before failing.
			goto checkEvents
		default:
			time.Sleep(time.Millisecond)
		}
	}
checkEvents:
	// Find the post-scan complete event that carries libraryTrackId.
	ceMu.Lock()
	evs := make([]core.DownloadEvent, len(completeEvents))
	copy(evs, completeEvents)
	ceMu.Unlock()

	var found *core.DownloadEvent
	for i := range evs {
		if evs[i].JobID == job.ID && evs[i].LibraryTrackID == "lib-track-9" {
			found = &evs[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("no download.complete event with libraryTrackId=%q for job %q; got events: %+v",
			"lib-track-9", job.ID, evs)
		return
	}
	if found.CoverArtID != "mf-lib-track-9_abc123" {
		t.Fatalf("complete event coverArtId: got %q want %q (must be carried on WS event for live recently-downloaded covers)", found.CoverArtID, "mf-lib-track-9_abc123")
	}
	if found.Source != "spotify" {
		t.Fatalf("complete event source: got %q want %q", found.Source, "spotify")
	}
	if found.ExternalID != "e1" {
		t.Fatalf("complete event externalId: got %q want %q", found.ExternalID, "e1")
	}
}

// TestRunScanWaitsForScanToCompleteBeforeRematch is the regression test for the
// scan-start RACE (Bug A): Navidrome's startScan is async, so getScanStatus
// reports scanning=false for a window before the scan engages. The manager must
// NOT re-match during that window (it would search the pre-download index and miss
// the file forever). With a scanner that returns false (settle), then true
// (scanning), then false (done), waitForScan must observe the scanning phase and
// only re-match after it ends — leaving the job linked to its library track.
func TestRunScanWaitsForScanToCompleteBeforeRematch(t *testing.T) {
	clk := newFakeClock()
	dl := &fakeDL{name: "dl", canDownload: true}
	store := newMemStore()
	bus := events.New()
	// false on the first poll (scan not engaged yet) → true (scanning) → false (done).
	// If the manager broke out of the poll on the first false (the bug), it would
	// re-match too early; this sequence asserts it waits through the scanning phase.
	scanner := &fakeScanner{statusSeq: []bool{false, true, true, false}}
	rematcher := &fakeRematcher{trackID: "lib-track-classical"}
	m := NewManager(Config{Workers: 1, DebounceWindow: 5 * time.Second, ScanPollEvery: time.Millisecond, ScanPollMax: time.Second, ScanSettleMax: time.Second},
		wrapDownloaders([]Downloader{dl}), store, bus, scanner, rematcher, &fakeVersion{v: 1}, clk, nil, nil)
	t.Cleanup(m.Stop)
	m.Start()

	job, err := m.Enqueue(context.Background(), core.DownloadRequest{
		Source: "spotify", ExternalID: "cl1", Artist: "Glenn Gould",
		Title: "Goldberg Variations, BWV 988: Aria", Album: "Bach: The Goldberg Variations",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Wait for the download to complete in the store.
	deadline := time.After(2 * time.Second)
	for {
		cur, _, _ := store.Get(context.Background(), job.ID)
		if cur.Status == core.DownloadCompleted {
			break
		}
		select {
		case <-deadline:
			t.Fatal("job never completed")
		default:
			time.Sleep(time.Millisecond)
		}
	}

	// Fire the debounced scan: runScan → waitForScan (settle→drain) → re-match.
	clk.Advance(5 * time.Second)

	// The scanner must have been polled enough to traverse the false→true→false
	// sequence (at least 3 ScanStatus calls), proving the manager waited rather than
	// bailing on the first scanning=false.
	if n := scanner.statusCalls(); n < 3 {
		t.Fatalf("waitForScan polled ScanStatus %d times; expected >=3 (it bailed before the scan engaged — the race)", n)
	}

	cur, _, _ := store.Get(context.Background(), job.ID)
	if cur.LibraryTrackID != "lib-track-classical" {
		t.Fatalf("library_track_id not set after scan-complete re-match, got %q", cur.LibraryTrackID)
	}
}

func TestCancelOrphanedRunningJob(t *testing.T) {
	store := newMemStore()
	job := core.DownloadJob{ID: "orphaned", DedupKey: "dk", Status: core.DownloadRunning, DownloaderName: "dl", Source: "s", ExternalID: "e1"}
	if err := store.Insert(context.Background(), job, core.DownloadRequest{Source: "s", ExternalID: "e1", Artist: "A", Title: "T"}); err != nil {
		t.Fatal(err)
	}
	m := NewManager(Config{}, wrapDownloaders([]Downloader{&fakeDL{name: "dl", canDownload: true}}), store, events.New(), &fakeScanner{}, nil, nil, RealClock{}, nil, nil)

	if err := m.Cancel(context.Background(), job.ID); err != nil {
		t.Fatal(err)
	}
	got, _, _ := store.Get(context.Background(), job.ID)
	if got.Status != core.DownloadCanceled {
		t.Fatalf("orphaned running job status = %q, want canceled", got.Status)
	}
}

func TestStartRecoversInterruptedAndQueuedSyncJobs(t *testing.T) {
	store := newMemStore()
	running := core.DownloadJob{ID: "interrupted", DedupKey: "dk-running", Status: core.DownloadRunning, DownloaderName: "dl", Source: "s", ExternalID: "running"}
	queued := core.DownloadJob{ID: "queued", DedupKey: "dk-queued", Status: core.DownloadQueued, DownloaderName: "dl", Source: "s", ExternalID: "queued"}
	for _, job := range []core.DownloadJob{running, queued} {
		if err := store.Insert(context.Background(), job, core.DownloadRequest{Source: "s", ExternalID: job.ExternalID, Artist: "A", Title: job.ExternalID}); err != nil {
			t.Fatal(err)
		}
	}
	block := make(chan struct{})
	defer close(block)
	dl := &fakeDL{name: "dl", canDownload: true, block: block}
	m, _ := testManager(t, []Downloader{dl}, store, nil, nil, nil)

	interrupted, _, _ := store.Get(context.Background(), running.ID)
	if interrupted.Status != core.DownloadFailed || interrupted.Error != "interrupted by restart" {
		t.Fatalf("interrupted job = %+v, want failed with restart error", interrupted)
	}
	deadline := time.After(2 * time.Second)
	for dl.starts() == 0 {
		select {
		case <-deadline:
			t.Fatal("queued job was not re-dispatched after restart")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	got, _, _ := store.Get(context.Background(), queued.ID)
	if got.Status != core.DownloadRunning {
		t.Fatalf("queued job status = %q, want running after recovery dispatch", got.Status)
	}
	_ = m // retain the manager until t.Cleanup stops its worker
}

func TestRetryResetsFailedJob(t *testing.T) {
	store := newMemStore()
	// Seed a failed job directly.
	failed := core.DownloadJob{ID: "j1", DedupKey: "dk", Status: core.DownloadFailed, DownloaderName: "dl", Attempts: 1, Source: "s", ExternalID: "e1"}
	_ = store.Insert(context.Background(), failed, core.DownloadRequest{Source: "s", ExternalID: "e1", Artist: "A", Title: "T", Album: "Al"})

	dl := &fakeDL{name: "dl", canDownload: true}
	m, _ := testManager(t, []Downloader{dl}, store, nil, nil, nil)

	j, err := m.Retry(context.Background(), "j1", "")
	if err != nil {
		t.Fatal(err)
	}
	if j.Status != core.DownloadQueued {
		t.Fatalf("retry should set queued, got %q", j.Status)
	}
	if j.Attempts != 2 {
		t.Fatalf("retry should bump attempts to 2, got %d", j.Attempts)
	}
}

// A manual URL reaches the downloader on retry, whatever the job's source, and
// is persisted with the request so a restart before dispatch keeps it.
func TestRetryWithManualURLReachesDownloader(t *testing.T) {
	for _, tc := range []struct{ name, source, externalID string }{
		{"spotify", "spotify", "sp1"},
		{"no external id", "youtube", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := newMemStore()
			req := core.DownloadRequest{Source: tc.source, ExternalID: tc.externalID, Artist: "Einaudi", Title: "Una mattina", DurationMs: 215000}
			failed := core.DownloadJob{ID: "j1", DedupKey: "dk1", Status: core.DownloadFailed, DownloaderName: "dl", Attempts: 1,
				Source: tc.source, ExternalID: tc.externalID, Artist: req.Artist, Title: req.Title}
			if err := store.Insert(context.Background(), failed, req); err != nil {
				t.Fatal(err)
			}
			dl := &fakeDL{name: "dl", canDownload: true}
			m, _ := testManager(t, []Downloader{dl}, store, nil, nil, nil)

			const url = "https://www.youtube.com/watch?v=MANUAL"
			if _, err := m.Retry(context.Background(), "j1", url); err != nil {
				t.Fatal(err)
			}
			waitForStatus(t, store, "j1", core.DownloadCompleted)
			got := dl.requests()
			if len(got) != 1 || got[0].ManualURL != url || got[0].DurationMs != req.DurationMs {
				t.Fatalf("downloader received %+v, want the stored request with ManualURL %q", got, url)
			}
			if persisted, _ := store.getReq("j1"); persisted.ManualURL != url {
				t.Fatalf("persisted ManualURL = %q, want %q", persisted.ManualURL, url)
			}
		})
	}
}

// A manual URL applies to the retry it was given with. When that attempt fails,
// a later plain retry must search again rather than reuse the rejected link.
func TestPlainRetryDoesNotReuseAFailedManualURL(t *testing.T) {
	store := newMemStore()
	req := core.DownloadRequest{Source: "spotify", ExternalID: "sp-url", Artist: "Artist", Title: "Track"}
	failed := core.DownloadJob{ID: "jurl", DedupKey: "dkurl", Status: core.DownloadFailed, DownloaderName: "dl", Attempts: 1,
		Source: "spotify", ExternalID: "sp-url", Artist: "Artist", Title: "Track"}
	if err := store.Insert(context.Background(), failed, req); err != nil {
		t.Fatal(err)
	}
	dl := &fakeDL{name: "dl", canDownload: true, errOnStart: errors.New("bad url")}
	m, _ := testManager(t, []Downloader{dl}, store, nil, nil, nil)

	if _, err := m.Retry(context.Background(), "jurl", "https://www.youtube.com/watch?v=MANUAL"); err != nil {
		t.Fatal(err)
	}
	waitForStatus(t, store, "jurl", core.DownloadFailed)
	if _, err := m.Retry(context.Background(), "jurl", ""); err != nil {
		t.Fatal(err)
	}
	waitForStatus(t, store, "jurl", core.DownloadFailed)

	got := dl.requests()
	if len(got) != 2 {
		t.Fatalf("downloader started %d times, want 2", len(got))
	}
	if got[1].ManualURL != "" {
		t.Fatalf("plain retry reused the failed manual URL %q", got[1].ManualURL)
	}
}

// TestBackfillUnlinkedReLinksCompletedJobs asserts that on startup, the manager
// re-matches completed jobs that have no LibraryTrackID (e.g. finished under an
// older matcher) and sets LibraryTrackID + CoverArtID and publishes a complete event.
func TestBackfillUnlinkedReLinksCompletedJobs(t *testing.T) {
	store := newMemStore()

	// Seed a completed job with empty LibraryTrackID — simulates a job that
	// completed before the rematcher could link it.
	seeded := core.DownloadJob{
		ID: "backfill-j1", DedupKey: "dk-backfill", Status: core.DownloadCompleted,
		DownloaderName: "dl", Source: "spotify", ExternalID: "ext-bf1",
		Artist: "Bach", Title: "Goldberg Variations", Album: "Goldberg",
		Progress: 100,
	}
	_ = store.Insert(context.Background(), seeded, core.DownloadRequest{
		Source: "spotify", ExternalID: "ext-bf1", Artist: "Bach", Title: "Goldberg Variations",
	})

	rematcher := &fakeRematcher{trackID: "lib-bf-1", coverArtID: "cover-bf-1"}
	bus := events.New()
	dl := &fakeDL{name: "dl", canDownload: true}
	m := NewManager(Config{Workers: 1, DebounceWindow: 5 * time.Second, ScanPollEvery: time.Millisecond, ScanPollMax: time.Second, ScanSettleMax: 10 * time.Millisecond},
		wrapDownloaders([]Downloader{dl}), store, bus, &fakeScanner{}, rematcher, &fakeVersion{v: 1}, RealClock{}, nil, nil)
	t.Cleanup(m.Stop)

	// Subscribe to complete events BEFORE starting so we don't miss the backfill publish.
	completeCh, unsub := bus.Subscribe(TopicComplete)
	defer unsub()

	var backfillEvents []core.DownloadEvent
	var evMu sync.Mutex
	gotEvent := make(chan struct{}, 1)
	go func() {
		for ev := range completeCh {
			if de, ok := ev.Payload.(core.DownloadEvent); ok && de.JobID == seeded.ID {
				evMu.Lock()
				backfillEvents = append(backfillEvents, de)
				evMu.Unlock()
				select {
				case gotEvent <- struct{}{}:
				default:
				}
			}
		}
	}()

	m.Start()

	// Wait for the backfill goroutine to publish the complete event.
	select {
	case <-gotEvent:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for backfill to publish a complete event")
	}

	// Job must now have LibraryTrackID and CoverArtID set.
	updated, ok, err := store.Get(context.Background(), seeded.ID)
	if err != nil || !ok {
		t.Fatalf("job not found: %v", err)
	}
	if updated.LibraryTrackID != "lib-bf-1" {
		t.Fatalf("backfill LibraryTrackID: got %q, want %q", updated.LibraryTrackID, "lib-bf-1")
	}
	if updated.CoverArtID != "cover-bf-1" {
		t.Fatalf("backfill CoverArtID: got %q, want %q", updated.CoverArtID, "cover-bf-1")
	}

	// The published event must carry the library track id and cover art id.
	evMu.Lock()
	evs := make([]core.DownloadEvent, len(backfillEvents))
	copy(evs, backfillEvents)
	evMu.Unlock()

	var found *core.DownloadEvent
	for i := range evs {
		if evs[i].LibraryTrackID == "lib-bf-1" {
			found = &evs[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("no backfill complete event with libraryTrackId=%q; got: %+v", "lib-bf-1", evs)
		return
	}
	if found.CoverArtID != "cover-bf-1" {
		t.Fatalf("backfill event CoverArtID: got %q, want %q", found.CoverArtID, "cover-bf-1")
	}
}

// TestBackfillPlaylistAdderCalledWhenAddToPlaylistIDSet mirrors
// TestPlaylistAdderCalledOnlyForDownloadsWithAPlaylist but exercises the BACKFILL
// path: a completed, unlinked job whose request carries AddToPlaylistID must have
// AddTracksToPlaylist called when the manager starts and re-links it.
func TestBackfillPlaylistAdderCalledWhenAddToPlaylistIDSet(t *testing.T) {
	store := newMemStore()

	const playlistID = "pl-backfill-123"
	const libTrackID = "lib-backfill-pl-1"

	// Seed a completed, unlinked job with AddToPlaylistID set on the job struct
	// (mirrors how Enqueue stores it: AddToPlaylistID is copied from the request
	// directly onto the job row so BackfillUnlinked can read it from store.List).
	seeded := core.DownloadJob{
		ID: "backfill-pl-j1", DedupKey: "dk-backfill-pl", Status: core.DownloadCompleted,
		DownloaderName: "dl", Source: "spotify", ExternalID: "ext-bf-pl1",
		Artist: "Artist", Title: "Playlist Track", Album: "Album",
		AddToPlaylistID: playlistID,
		Progress:        100,
	}
	_ = store.Insert(context.Background(), seeded, core.DownloadRequest{
		Source: "spotify", ExternalID: "ext-bf-pl1", Artist: "Artist",
		Title: "Playlist Track", Album: "Album", AddToPlaylistID: playlistID,
	})

	rematcher := &fakeRematcher{trackID: libTrackID}
	adder := &fakePlaylistAdder{}
	bus := events.New()
	dl := &fakeDL{name: "dl", canDownload: true}
	m := NewManager(Config{Workers: 1, DebounceWindow: 5 * time.Second, ScanPollEvery: time.Millisecond, ScanPollMax: time.Second, ScanSettleMax: 10 * time.Millisecond},
		wrapDownloaders([]Downloader{dl}), store, bus, &fakeScanner{}, rematcher, &fakeVersion{v: 1}, RealClock{}, adder, nil)
	t.Cleanup(m.Stop)

	// Subscribe before Start so we don't miss the backfill publish.
	completeCh, unsub := bus.Subscribe(TopicComplete)
	defer unsub()
	gotEvent := make(chan struct{}, 1)
	go func() {
		for ev := range completeCh {
			if de, ok := ev.Payload.(core.DownloadEvent); ok && de.JobID == seeded.ID {
				select {
				case gotEvent <- struct{}{}:
				default:
				}
			}
		}
	}()

	m.Start()

	// Wait for the backfill to publish the complete event.
	select {
	case <-gotEvent:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for backfill complete event")
	}

	// Give the playlist add a moment to execute (it runs synchronously inside the
	// backfill loop, so by the time gotEvent fires it should already be called).
	if adder.callCount() != 1 {
		t.Fatalf("expected 1 AddTracksToPlaylist call from backfill, got %d", adder.callCount())
	}
	gotPlaylistID, gotTrackIDs := adder.getCall(0)
	if gotPlaylistID != playlistID {
		t.Fatalf("backfill AddTracksToPlaylist playlistID: got %q, want %q", gotPlaylistID, playlistID)
	}
	if len(gotTrackIDs) != 1 || gotTrackIDs[0] != libTrackID {
		t.Fatalf("backfill AddTracksToPlaylist trackIDs: got %v, want [%q]", gotTrackIDs, libTrackID)
	}
}

// fakePlaylistAdder records AddTracksToPlaylist calls for assertions.
type fakePlaylistAdder struct {
	mu    sync.Mutex
	calls []struct {
		playlistID string
		trackIDs   []string
	}
}

func (f *fakePlaylistAdder) AddTracksToPlaylist(_ context.Context, playlistID string, trackIDs []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, struct {
		playlistID string
		trackIDs   []string
	}{playlistID, trackIDs})
	return nil
}

func (f *fakePlaylistAdder) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func (f *fakePlaylistAdder) getCall(i int) (string, []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := f.calls[i]
	return c.playlistID, c.trackIDs
}

// A download carrying AddToPlaylistID lands in that playlist once the scan links
// it; a download without one, completing in the same scan, lands nowhere.
func TestPlaylistAdderCalledOnlyForDownloadsWithAPlaylist(t *testing.T) {
	clk := newFakeClock()
	dl := &fakeDL{name: "dl", canDownload: true}
	store := newMemStore()
	const libTrackID = "lib-playlist-track-1"
	adder := &fakePlaylistAdder{}
	m := NewManager(Config{Workers: 1, DebounceWindow: 5 * time.Second, ScanPollEvery: time.Millisecond, ScanPollMax: time.Second, ScanSettleMax: 10 * time.Millisecond},
		wrapDownloaders([]Downloader{dl}), store, events.New(), &fakeScanner{}, &fakeRematcher{trackID: libTrackID}, &fakeVersion{v: 1}, clk, adder, nil)
	t.Cleanup(m.Stop)
	m.Start()

	const playlistID = "pl-abc-123"
	for _, req := range []core.DownloadRequest{
		{Source: "spotify", ExternalID: "e-pl-1", Artist: "Artist", Title: "Track", Album: "Album", AddToPlaylistID: playlistID},
		{Source: "spotify", ExternalID: "e-no-pl", Artist: "Artist", Title: "Other", Album: "Album"},
	} {
		job, err := m.Enqueue(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		waitForStatus(t, store, job.ID, core.DownloadCompleted)
	}

	clk.Advance(5 * time.Second)

	if adder.callCount() != 1 {
		t.Fatalf("AddTracksToPlaylist calls = %d, want 1", adder.callCount())
	}
	gotPlaylistID, gotTrackIDs := adder.getCall(0)
	if gotPlaylistID != playlistID || len(gotTrackIDs) != 1 || gotTrackIDs[0] != libTrackID {
		t.Fatalf("AddTracksToPlaylist(%q, %v), want (%q, [%q])", gotPlaylistID, gotTrackIDs, playlistID, libTrackID)
	}
}

func TestClearRemovesTerminalJobAndPublishes(t *testing.T) {
	dl := &fakeDL{name: "dl", canDownload: true}
	store := newMemStore()
	m, bus := testManager(t, []Downloader{dl}, store, nil, nil, nil)

	sub, unsub := bus.Subscribe(TopicRemoved)
	defer unsub()

	// Seed a completed job directly in the store.
	job := core.DownloadJob{ID: "done1", DedupKey: "dk", Status: core.DownloadCompleted, Source: "spotify", ExternalID: "e"}
	if err := store.Insert(context.Background(), job, core.DownloadRequest{}); err != nil {
		t.Fatal(err)
	}

	if err := m.Clear(context.Background(), "done1"); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	if _, ok, _ := store.Get(context.Background(), "done1"); ok {
		t.Fatal("job should be deleted")
	}
	select {
	case ev := <-sub:
		re := ev.Payload.(core.DownloadRemovedEvent)
		if len(re.JobIDs) != 1 || re.JobIDs[0] != "done1" {
			t.Fatalf("removed event = %+v", re)
		}
	case <-time.After(time.Second):
		t.Fatal("expected download.removed event")
	}
}

func TestClearRejectsActiveJob(t *testing.T) {
	store := newMemStore()
	m, _ := testManager(t, []Downloader{&fakeDL{name: "dl", canDownload: true}}, store, nil, nil, nil)
	job := core.DownloadJob{ID: "run1", DedupKey: "dk", Status: core.DownloadRunning}
	if err := store.Insert(context.Background(), job, core.DownloadRequest{}); err != nil {
		t.Fatal(err)
	}
	if err := m.Clear(context.Background(), "run1"); err == nil {
		t.Fatal("Clear of a running job must error")
	}
	if _, ok, _ := store.Get(context.Background(), "run1"); !ok {
		t.Fatal("running job must NOT be deleted")
	}
}

func TestClearFinishedDeletesOnlyTerminal(t *testing.T) {
	store := newMemStore()
	m, _ := testManager(t, []Downloader{&fakeDL{name: "dl", canDownload: true}}, store, nil, nil, nil)
	for _, tc := range []struct{ id, st string }{{"a", "completed"}, {"b", "failed"}, {"c", "queued"}, {"d", "canceled"}} {
		j := core.DownloadJob{ID: tc.id, DedupKey: "dk-" + tc.id, Status: core.DownloadStatus(tc.st)}
		if err := store.Insert(context.Background(), j, core.DownloadRequest{}); err != nil {
			t.Fatal(err)
		}
	}
	ids, err := m.ClearFinished(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 3 {
		t.Fatalf("ClearFinished removed %v, want 3 (a,b,d)", ids)
	}
	if _, ok, _ := store.Get(context.Background(), "c"); !ok {
		t.Fatal("queued job c must survive")
	}
}

func TestPauseGatesDispatchResumeDrains(t *testing.T) {
	dl := &fakeDL{name: "dl", canDownload: true}
	store := newMemStore()
	m, bus := testManager(t, []Downloader{dl}, store, nil, nil, nil)

	sub, unsub := bus.Subscribe(TopicQueueState)
	defer unsub()

	m.Pause()
	if !m.IsPaused() {
		t.Fatal("expected IsPaused() true after Pause")
	}
	select {
	case ev := <-sub:
		if !ev.Payload.(core.QueueStateEvent).Paused {
			t.Fatal("pause event should carry Paused=true")
		}
	case <-time.After(time.Second):
		t.Fatal("expected download.queue event on Pause")
	}

	// More jobs than workers, so some sit buffered behind the pause gate.
	var ids []string
	for _, ext := range []string{"e1", "e2", "e3"} {
		job, err := m.Enqueue(context.Background(), core.DownloadRequest{Source: "spotify", ExternalID: ext, Artist: "A", Title: "T" + ext, Album: "Al"})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, job.ID)
	}

	// While paused, no worker may pick a job up: they all stay queued.
	time.Sleep(80 * time.Millisecond)
	for _, id := range ids {
		if got, _, _ := store.Get(context.Background(), id); got.Status != core.DownloadQueued {
			t.Fatalf("paused: want job %s to stay queued, got %s", id, got.Status)
		}
	}

	m.Resume()
	if m.IsPaused() {
		t.Fatal("expected IsPaused() false after Resume")
	}
	for _, id := range ids {
		waitForStatus(t, store, id, core.DownloadCompleted)
	}
}

// TestStopUnblocksPausedWorkers asserts that calling Stop() while the manager is
// paused does not hang — the paused workers must wake up and exit cleanly.
func TestStopUnblocksPausedWorkers(t *testing.T) {
	dl := &fakeDL{name: "dl", canDownload: true}
	store := newMemStore()
	bus := events.New()
	m := NewManager(
		Config{Workers: 2, DebounceWindow: 5 * time.Second, ScanPollEvery: time.Millisecond, ScanPollMax: time.Second, ScanSettleMax: 10 * time.Millisecond},
		wrapDownloaders([]Downloader{dl}), store, bus, &fakeScanner{}, &fakeRematcher{trackID: "t1"}, &fakeVersion{v: 1}, RealClock{}, nil, nil,
	)
	m.Start()
	m.Pause()

	done := make(chan struct{})
	go func() {
		m.Stop()
		close(done)
	}()

	select {
	case <-done:
		// Stop returned — workers unblocked successfully.
	case <-time.After(2 * time.Second):
		t.Fatal("Stop hung with workers paused")
	}
}

func TestFailedDownloadPublishesFailedEvent(t *testing.T) {
	dlErr := errors.New("network timeout")
	dl := &fakeDL{name: "dl", canDownload: true, errOnStart: dlErr}
	store := newMemStore()
	m, bus := testManager(t, []Downloader{dl}, store, nil, nil, nil)

	// Subscribe to download.failed before enqueuing.
	var failedEvents []core.DownloadEvent
	var evMu sync.Mutex
	gotFailed := make(chan struct{})
	ch, unsub := bus.Subscribe(TopicFailed)
	defer unsub()
	go func() {
		for ev := range ch {
			if de, ok := ev.Payload.(core.DownloadEvent); ok {
				evMu.Lock()
				failedEvents = append(failedEvents, de)
				evMu.Unlock()
				close(gotFailed)
				return
			}
		}
	}()

	job, err := m.Enqueue(context.Background(), core.DownloadRequest{
		Source: "spotify", ExternalID: "fail1", Artist: "A", Title: "T", Album: "Al",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Wait for the failed event (or timeout).
	select {
	case <-gotFailed:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for download.failed event")
	}

	evMu.Lock()
	defer evMu.Unlock()
	if len(failedEvents) == 0 {
		t.Fatal("no download.failed events received")
	}
	fe := failedEvents[0]
	if fe.JobID != job.ID {
		t.Fatalf("failed event job ID mismatch: got %q want %q", fe.JobID, job.ID)
	}
	if fe.Status != core.DownloadFailed {
		t.Fatalf("failed event status: got %v want DownloadFailed", fe.Status)
	}
	if fe.Error != dlErr.Error() {
		t.Fatalf("failed event error: got %q want %q", fe.Error, dlErr.Error())
	}

	// Verify the job is persisted as failed.
	persisted, ok, err := store.Get(context.Background(), job.ID)
	if err != nil || !ok {
		t.Fatalf("job not found in store: %v", err)
	}
	if persisted.Status != core.DownloadFailed {
		t.Fatalf("persisted job status: got %v want DownloadFailed", persisted.Status)
	}
}

// fakeAsyncDL is a fake AsyncDownloader (also a Downloader) for async-lane tests.
type fakeAsyncDL struct {
	mu          sync.Mutex
	name        string
	submitRef   string
	submitErr   error
	submitCalls int
	submitted   core.DownloadRequest
	cancelCalls int
	status      AsyncStatus
}

func (d *fakeAsyncDL) Type() string { return "downloader" }
func (d *fakeAsyncDL) Name() string { return d.name }
func (d *fakeAsyncDL) SupportedGranularities() []core.DownloadGranularity {
	return []core.DownloadGranularity{core.GranularityAlbum}
}
func (d *fakeAsyncDL) ConfigSchema() registry.ConfigSchema  { return registry.ConfigSchema{} }
func (d *fakeAsyncDL) Init(map[string]any) error            { return nil }
func (d *fakeAsyncDL) TestConnection(context.Context) error { return nil }
func (d *fakeAsyncDL) CanDownload(context.Context, core.DownloadRequest) (bool, error) {
	return true, nil
}
func (d *fakeAsyncDL) Start(context.Context, core.DownloadRequest, func(int)) (string, error) {
	return "", fmt.Errorf("fakeAsyncDL.Start should never be called")
}
func (d *fakeAsyncDL) Submit(_ context.Context, req core.DownloadRequest) (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.submitCalls++
	d.submitted = req
	return d.submitRef, d.submitErr
}
func (d *fakeAsyncDL) Poll(_ context.Context, _ string) (AsyncStatus, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.status, nil
}
func (d *fakeAsyncDL) CancelAsync(_ context.Context, _ string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.cancelCalls++
	return nil
}
func (d *fakeAsyncDL) setStatus(s AsyncStatus) { d.mu.Lock(); d.status = s; d.mu.Unlock() }
func (d *fakeAsyncDL) lastSubmitted() core.DownloadRequest {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.submitted
}

func TestEnqueueAsyncSubmitsAndDoesNotPinWorker(t *testing.T) {
	async := &fakeAsyncDL{name: "lidarr", submitRef: "album-42"}
	store := newMemStore()
	m, _ := testManager(t, []Downloader{async}, store, nil, nil, nil)

	job, err := m.Enqueue(context.Background(), core.DownloadRequest{
		Source: "spotify", ExternalID: "e1", Artist: "Daft Punk", Title: "One More Time",
		Album: "Discovery", Granularity: core.GranularityAlbum,
	})
	if err != nil {
		t.Fatal(err)
	}
	if async.submitCalls != 1 {
		t.Fatalf("Submit calls = %d, want 1", async.submitCalls)
	}
	// After submit the job is running, carries the ref, and was NOT pushed to the
	// worker queue (the fake's Start panics if a worker ever ran it).
	got, _, _ := store.Get(context.Background(), job.ID)
	if got.Status != core.DownloadRunning {
		t.Fatalf("status = %s, want running", got.Status)
	}
	if got.DownloaderRef != "album-42" {
		t.Fatalf("ref = %q, want album-42", got.DownloaderRef)
	}
}

func TestEnqueueAsyncSubmitErrorFailsJob(t *testing.T) {
	async := &fakeAsyncDL{name: "lidarr", submitErr: fmt.Errorf("couldn't find album in Lidarr")}
	store := newMemStore()
	m, _ := testManager(t, []Downloader{async}, store, nil, nil, nil)

	job, _ := m.Enqueue(context.Background(), core.DownloadRequest{
		Source: "spotify", ExternalID: "e1", Artist: "X", Title: "Y", Album: "Z", Granularity: core.GranularityAlbum,
	})
	got, _, _ := store.Get(context.Background(), job.ID)
	if got.Status != core.DownloadFailed {
		t.Fatalf("status = %s, want failed", got.Status)
	}
	if got.Error == "" {
		t.Fatal("failed job should carry an error message")
	}
}

func TestCancelAsyncJob(t *testing.T) {
	async := &fakeAsyncDL{name: "lidarr", submitRef: "album-7"}
	store := newMemStore()
	m, _ := testManager(t, []Downloader{async}, store, nil, nil, nil)

	job, _ := m.Enqueue(context.Background(), core.DownloadRequest{
		Source: "spotify", ExternalID: "e1", Artist: "X", Title: "Y", Album: "Z", Granularity: core.GranularityAlbum,
	})
	if err := m.Cancel(context.Background(), job.ID); err != nil {
		t.Fatal(err)
	}
	if async.cancelCalls != 1 {
		t.Fatalf("CancelAsync calls = %d, want 1", async.cancelCalls)
	}
	got, _, _ := store.Get(context.Background(), job.ID)
	if got.Status != core.DownloadCanceled {
		t.Fatalf("status = %s, want canceled", got.Status)
	}
}

func TestReconcileAdvancesProgressThenCompletes(t *testing.T) {
	clk := newFakeClock()
	async := &fakeAsyncDL{name: "lidarr", submitRef: "album-9"}
	store := newMemStore()
	scanner := &fakeScanner{}
	bus := events.New()
	m := NewManager(Config{Workers: 1, DebounceWindow: time.Second, ScanPollEvery: time.Millisecond, ScanPollMax: time.Second, ScanSettleMax: 10 * time.Millisecond},
		wrapDownloaders([]Downloader{async}), store, bus, scanner, &fakeRematcher{trackID: "t1"}, &fakeVersion{v: 1}, clk, nil, nil)
	t.Cleanup(m.Stop)
	m.Start()

	job, _ := m.Enqueue(context.Background(), core.DownloadRequest{
		Source: "spotify", ExternalID: "e1", Artist: "A", Title: "T", Album: "Al", Granularity: core.GranularityAlbum,
	})

	// Downloading at 40%.
	async.setStatus(AsyncStatus{State: core.DownloadRunning, Progress: 40})
	m.reconcileOnce(context.Background())
	if got, _, _ := store.Get(context.Background(), job.ID); got.Progress != 40 {
		t.Fatalf("progress = %d, want 40", got.Progress)
	}

	// Imported → completed, and a scan is scheduled.
	async.setStatus(AsyncStatus{State: core.DownloadCompleted, Progress: 100})
	m.reconcileOnce(context.Background())
	if got, _, _ := store.Get(context.Background(), job.ID); got.Status != core.DownloadCompleted {
		t.Fatalf("status = %s, want completed", got.Status)
	}
	// Fire the debounced scan; the rematcher links the track.
	clk.Advance(time.Second)
	// waitForScan uses wall-clock polling against the fakeScanner (idle → returns fast).
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if got, _, _ := store.Get(context.Background(), job.ID); got.LibraryTrackID == "t1" {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	got, _, _ := store.Get(context.Background(), job.ID)
	t.Fatalf("expected scan rematch to set library_track_id, got %q", got.LibraryTrackID)
}

func TestReconcileFailMapsToFailed(t *testing.T) {
	async := &fakeAsyncDL{name: "lidarr", submitRef: "album-1"}
	store := newMemStore()
	m, _ := testManager(t, []Downloader{async}, store, nil, nil, nil)
	job, _ := m.Enqueue(context.Background(), core.DownloadRequest{Source: "spotify", ExternalID: "e", Artist: "A", Title: "T", Album: "Al", Granularity: core.GranularityAlbum})

	async.setStatus(AsyncStatus{State: core.DownloadFailed, Error: "Lidarr found no release"})
	m.reconcileOnce(context.Background())
	got, _, _ := store.Get(context.Background(), job.ID)
	if got.Status != core.DownloadFailed || got.Error != "Lidarr found no release" {
		t.Fatalf("job = %+v, want failed with reason", got)
	}
}

// -- Task 1: SupportedGranularities + DownloaderEntry + granularity-aware pick --

// When a download fails, the chain falls back in configured order, not
// registration order: hi (0) and lo (1) fail, so mid (2) completes the job.
func TestFallbackFollowsConfiguredOrder(t *testing.T) {
	hi := &fakeDL{name: "hi", canDownload: true, errOnStart: errors.New("hi failed")}
	lo := &fakeDL{name: "lo", canDownload: true, errOnStart: errors.New("lo failed")}
	mid := &fakeDL{name: "mid", canDownload: true}
	store := newMemStore()
	entries := []DownloaderEntry{
		{Downloader: mid, Order: map[core.DownloadGranularity]int{core.GranularityTrack: 2}},
		{Downloader: lo, Order: map[core.DownloadGranularity]int{core.GranularityTrack: 1}},
		{Downloader: hi, Order: map[core.DownloadGranularity]int{core.GranularityTrack: 0}},
	}
	m := NewManager(Config{Workers: 1, DebounceWindow: time.Hour}, entries, store, events.New(), &fakeScanner{},
		&fakeRematcher{trackID: "t1"}, &fakeVersion{v: 1}, RealClock{}, nil, nil)
	t.Cleanup(m.Stop)
	m.Start()

	job, err := m.Enqueue(context.Background(), core.DownloadRequest{Source: "spotify", ExternalID: "e1", Artist: "A", Title: "T"})
	if err != nil {
		t.Fatal(err)
	}
	if job.DownloaderName != "hi" {
		t.Fatalf("first pick = %q, want the lowest order", job.DownloaderName)
	}
	waitForStatus(t, store, job.ID, core.DownloadCompleted)
	if got, _, _ := store.Get(context.Background(), job.ID); got.DownloaderName != "mid" {
		t.Fatalf("completed via %q, want mid", got.DownloaderName)
	}
	if hi.starts() != 1 || lo.starts() != 1 || mid.starts() != 1 {
		t.Fatalf("starts hi=%d lo=%d mid=%d, want each once", hi.starts(), lo.starts(), mid.starts())
	}
}

// -- Task 3: on-failure fallback through the sync downloader chain --

// TestFallbackToNextDownloaderOnStartError asserts that when the first (picked)
// sync downloader's Start returns an error, the worker tries the next downloader in
// the same-granularity chain rather than failing the job immediately.  The job must
// reach DownloadCompleted and its final DownloaderName must be the second downloader.
func TestFallbackToNextDownloaderOnStartError(t *testing.T) {
	d1 := &fakeDL{name: "d1", canDownload: true, errOnStart: errors.New("d1 lookup failed")}
	d2 := &fakeDL{name: "d2", canDownload: true}
	store := newMemStore()
	m, _ := testManager(t, []Downloader{d1, d2}, store, nil, nil, nil)

	job, err := m.Enqueue(context.Background(), core.DownloadRequest{
		Source: "spotify", ExternalID: "fb1", Artist: "A", Title: "T", Album: "Al",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Wait for the job to reach a terminal state.
	deadline := time.After(3 * time.Second)
	for {
		cur, _, _ := store.Get(context.Background(), job.ID)
		if cur.Status == core.DownloadCompleted || cur.Status == core.DownloadFailed {
			if cur.Status != core.DownloadCompleted {
				t.Fatalf("job should complete via d2 fallback, got status=%q error=%q", cur.Status, cur.Error)
			}
			if cur.DownloaderName != "d2" {
				t.Fatalf("final DownloaderName should be %q (fallback), got %q", "d2", cur.DownloaderName)
			}
			break
		}
		select {
		case <-deadline:
			cur2, _, _ := store.Get(context.Background(), job.ID)
			t.Fatalf("job did not reach terminal state (status=%q)", cur2.Status)
		default:
			time.Sleep(time.Millisecond)
		}
	}

	if d1.starts() != 1 {
		t.Fatalf("d1 should have been attempted exactly once, got %d", d1.starts())
	}
	if d2.starts() != 1 {
		t.Fatalf("d2 should have been attempted exactly once (as fallback), got %d", d2.starts())
	}
}

// TestFallbackChainExhaustedReachesDownloadFailed asserts that when all sync
// downloaders in the chain fail on Start, the job ends up DownloadFailed
// (not stuck or panicking).
func TestFallbackChainExhaustedReachesDownloadFailed(t *testing.T) {
	d1 := &fakeDL{name: "d1", canDownload: true, errOnStart: errors.New("d1 error")}
	d2 := &fakeDL{name: "d2", canDownload: true, errOnStart: errors.New("d2 error")}
	store := newMemStore()
	m, _ := testManager(t, []Downloader{d1, d2}, store, nil, nil, nil)

	job, err := m.Enqueue(context.Background(), core.DownloadRequest{
		Source: "spotify", ExternalID: "fb2", Artist: "A", Title: "T", Album: "Al",
	})
	if err != nil {
		t.Fatal(err)
	}

	deadline := time.After(3 * time.Second)
	for {
		cur, _, _ := store.Get(context.Background(), job.ID)
		if cur.Status == core.DownloadCompleted || cur.Status == core.DownloadFailed {
			if cur.Status != core.DownloadFailed {
				t.Fatalf("chain-exhausted job should be DownloadFailed, got %q", cur.Status)
			}
			break
		}
		select {
		case <-deadline:
			cur2, _, _ := store.Get(context.Background(), job.ID)
			t.Fatalf("job did not reach DownloadFailed (status=%q)", cur2.Status)
		default:
			time.Sleep(time.Millisecond)
		}
	}

	if d1.starts() != 1 {
		t.Fatalf("d1 should have been attempted once, got %d", d1.starts())
	}
	if d2.starts() != 1 {
		t.Fatalf("d2 should have been attempted once (after d1 failed), got %d", d2.starts())
	}
}

// ---- album-job timeout tests ----

// -- Task 4: Retry-async routing + granularity recovery --

// Retrying a failed async (album) job re-submits it on the async lane with the
// request recovered from the store, granularity included. fakeAsyncDL.Start
// errors, so a sync-lane dispatch would fail the job instead.
func TestRetryAsyncJobResubmitsTheStoredRequest(t *testing.T) {
	async := &fakeAsyncDL{name: "lidarr", submitRef: "retry-ref-1"}
	store := newMemStore()
	failed := core.DownloadJob{
		ID: "async-retry-j1", DedupKey: "dk-async-retry", Status: core.DownloadFailed,
		DownloaderName: "lidarr", Attempts: 1,
		Source: "spotify", ExternalID: "album-ext-1",
		Artist: "Daft Punk", Title: "Discovery", Album: "Discovery",
	}
	if err := store.Insert(context.Background(), failed, core.DownloadRequest{
		Source: "spotify", ExternalID: "album-ext-1", Artist: "Daft Punk",
		Title: "Discovery", Album: "Discovery", Granularity: core.GranularityAlbum,
	}); err != nil {
		t.Fatal(err)
	}
	m, _ := testManager(t, []Downloader{async}, store, nil, nil, nil)

	if _, err := m.Retry(context.Background(), "async-retry-j1", ""); err != nil {
		t.Fatalf("Retry: %v", err)
	}
	waitForStatus(t, store, "async-retry-j1", core.DownloadRunning)

	if got, _, _ := store.Get(context.Background(), "async-retry-j1"); got.DownloaderRef != "retry-ref-1" {
		t.Fatalf("job DownloaderRef = %q, want retry-ref-1", got.DownloaderRef)
	}
	if req := async.lastSubmitted(); req.Granularity != core.GranularityAlbum || req.Album != "Discovery" {
		t.Fatalf("submitted %+v, want the stored album request", req)
	}
}

// TestRetrySyncJobStillUsesWorker asserts that retrying a FAILED sync job still
// dispatches via the worker channel (calls Start, not Submit). The existing sync
// retry path must be unaffected by the async routing change.
func TestRetrySyncJobStillUsesWorker(t *testing.T) {
	dl := &fakeDL{name: "dl", canDownload: true}
	store := newMemStore()

	failed := core.DownloadJob{
		ID: "sync-retry-j1", DedupKey: "dk-sync-retry", Status: core.DownloadFailed,
		DownloaderName: "dl", Attempts: 1,
		Source: "spotify", ExternalID: "track-ext-1",
		Artist: "A", Title: "T", Album: "Al",
	}
	_ = store.Insert(context.Background(), failed, core.DownloadRequest{
		Source: "spotify", ExternalID: "track-ext-1", Artist: "A",
		Title: "T", Album: "Al", Granularity: core.GranularityTrack,
	})

	m, _ := testManager(t, []Downloader{dl}, store, nil, nil, nil)

	_, err := m.Retry(context.Background(), "sync-retry-j1", "")
	if err != nil {
		t.Fatalf("Retry: %v", err)
	}

	// Wait for the sync job to complete (worker called Start → success).
	deadline := time.After(2 * time.Second)
	for {
		got, _, _ := store.Get(context.Background(), "sync-retry-j1")
		if got.Status == core.DownloadCompleted {
			break
		}
		select {
		case <-deadline:
			got2, _, _ := store.Get(context.Background(), "sync-retry-j1")
			t.Fatalf("sync retry did not complete (status=%q)", got2.Status)
		default:
			time.Sleep(time.Millisecond)
		}
	}

	if dl.starts() != 1 {
		t.Fatalf("sync retry should call Start exactly once, got %d", dl.starts())
	}
}

// Album jobs (a whole Lidarr import) get their own, longer budget: 2h unless
// configured. Track jobs, including unspecified granularity, use JobTimeout.
func TestJobTimeoutByGranularity(t *testing.T) {
	m := &Manager{cfg: Config{JobTimeout: 50 * time.Millisecond}.withDefaults()}
	for g, want := range map[core.DownloadGranularity]time.Duration{
		core.GranularityTrack: 50 * time.Millisecond,
		"":                    50 * time.Millisecond,
		core.GranularityAlbum: 2 * time.Hour,
	} {
		if got := m.jobTimeout(core.DownloadRequest{Granularity: g}); got != want {
			t.Fatalf("jobTimeout(%q) = %v, want %v", g, got, want)
		}
	}
}

// TestStopWithoutStartIsNoOp asserts that calling Stop() on a Manager that was
// never Start()ed returns promptly (no deadlock, no panic). This guards the guard
// we added to Stop() that skips wg.Wait() / close(stopCh) when started==false.
func TestStopWithoutStartIsNoOp(t *testing.T) {
	m := NewManager(Config{}, wrapDownloaders(nil), newMemStore(), nil, &fakeScanner{}, nil, nil, nil, nil, nil)
	done := make(chan struct{})
	go func() {
		m.Stop()
		close(done)
	}()
	select {
	case <-done:
		// returned promptly — pass
	case <-time.After(2 * time.Second):
		t.Fatal("Stop() without Start() did not return within 2s (potential deadlock)")
	}
}

// ctxSensitiveAsyncDL is a fakeAsyncDL variant whose Submit blocks until the test
// signals via readyCh, then checks ctx.Err(). This makes the cancellation test
// deterministic: the test cancels the request context WHILE Submit is blocking,
// so ctx.Err() is guaranteed to be non-nil if the un-detached context was passed.
// With context.WithoutCancel the child ctx is never canceled → Submit returns the
// ref and the job reaches Running.
type ctxSensitiveAsyncDL struct {
	fakeAsyncDL
	readyCh chan struct{} // closed by the test to unblock Submit
}

func (d *ctxSensitiveAsyncDL) Submit(ctx context.Context, req core.DownloadRequest) (string, error) {
	// Block until the test signals (gives it time to call cancel()).
	<-d.readyCh
	// Now check — if the un-detached request ctx was forwarded it will be canceled.
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return d.fakeAsyncDL.Submit(ctx, req)
}

// TestRetryAsyncDetachesRequestContext asserts that Retry's spawned goroutine uses
// context.WithoutCancel so that canceling the caller's request context (which the
// HTTP handler does the instant it returns) does not kill the in-flight Submit.
//
// Fail-without-fix: with the old `go m.submitAsync(ctx, …)` the request ctx is
// forwarded into Submit; canceling it before Submit runs causes Submit to return
// ctx.Err(), which submitAsync treats as a failure → job goes Failed.
// With the fix (context.WithoutCancel(ctx)), Submit receives a non-canceled ctx →
// job goes Running. The test is deterministic: Submit blocks on readyCh until the
// test has called cancel(), guaranteeing the cancellation has propagated.
func TestRetryAsyncDetachesRequestContext(t *testing.T) {
	readyCh := make(chan struct{})
	async := &ctxSensitiveAsyncDL{readyCh: readyCh}
	async.name = "lidarr"
	async.submitRef = "detach-ref-1"
	store := newMemStore()

	failed := core.DownloadJob{
		ID: "async-detach-j1", DedupKey: "dk-async-detach", Status: core.DownloadFailed,
		DownloaderName: "lidarr", Attempts: 1,
		Source: "spotify", ExternalID: "album-detach-ext",
		Artist: "Pink Floyd", Title: "The Wall", Album: "The Wall",
	}
	_ = store.Insert(context.Background(), failed, core.DownloadRequest{
		Source: "spotify", ExternalID: "album-detach-ext", Artist: "Pink Floyd",
		Title: "The Wall", Album: "The Wall", Granularity: core.GranularityAlbum,
	})

	m, _ := testManager(t, []Downloader{async}, store, nil, nil, nil)

	// Simulate an HTTP handler context: the caller cancels it immediately after
	// Retry returns (mimicking the Go HTTP server canceling r.Context() on return).
	reqCtx, cancel := context.WithCancel(context.Background())

	_, err := m.Retry(reqCtx, "async-detach-j1", "")
	if err != nil {
		t.Fatalf("Retry: %v", err)
	}

	// Cancel the request context BEFORE unblocking Submit. This guarantees the
	// cancellation has propagated when Submit checks ctx.Err().
	cancel()
	close(readyCh) // now let Submit proceed and observe the (possibly canceled) ctx

	// The job must reach Running (Submit succeeded with detached ctx), NOT Failed.
	deadline := time.After(2 * time.Second)
	for {
		got, _, _ := store.Get(context.Background(), "async-detach-j1")
		if got.Status == core.DownloadRunning {
			if got.DownloaderRef != "detach-ref-1" {
				t.Fatalf("ref = %q, want detach-ref-1", got.DownloaderRef)
			}
			return // pass
		}
		if got.Status == core.DownloadFailed {
			t.Fatalf("job went Failed after retry — request ctx was NOT detached (context.WithoutCancel fix missing): error=%q", got.Error)
		}
		select {
		case <-deadline:
			got2, _, _ := store.Get(context.Background(), "async-detach-j1")
			t.Fatalf("timed out waiting for Running (status=%q)", got2.Status)
		default:
			time.Sleep(time.Millisecond)
		}
	}
}

// ─── Task 3: CanonicalMinter fakes + tests ──────────────────────────────────

// fakeCanonicalMinter records calls and returns a fixed canonical id.
type fakeCanonicalMinter struct {
	mu     sync.Mutex
	calls  []catalog.Identity
	retID  string
	retErr error
}

func (f *fakeCanonicalMinter) CanonicalFor(_ context.Context, id catalog.Identity) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, id)
	if f.retErr != nil {
		return "", f.retErr
	}
	return f.retID, nil
}

func (f *fakeCanonicalMinter) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func (f *fakeCanonicalMinter) lastCall() (catalog.Identity, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.calls) == 0 {
		return catalog.Identity{}, false
	}
	return f.calls[len(f.calls)-1], true
}

// BackfillUnlinked links and mints only completed jobs without a library track.
// The rematcher matches everything, so an out-of-scope row the pass touched
// would visibly change.
func TestBackfillUnlinkedLinksAndMintsOnlyUnlinkedCompletedJobs(t *testing.T) {
	store := newMemStore()
	ctx := context.Background()
	unlinked := core.DownloadJob{ID: "j-unlinked", DedupKey: "dk1", Status: core.DownloadCompleted, DownloaderName: "dl",
		Source: "spotify", ExternalID: "sp1", Title: "Moon River", Artist: "Audrey Hepburn", Album: "Breakfast", ISRC: "US001", DurationMs: 120000}
	linked := core.DownloadJob{ID: "j-linked", DedupKey: "dk2", Status: core.DownloadCompleted, DownloaderName: "dl",
		LibraryTrackID: "existing-lib-track", CanonicalID: "trk_existing", Source: "spotify", ExternalID: "sp-linked", Title: "Linked", Artist: "A"}
	failed := core.DownloadJob{ID: "j-failed", DedupKey: "dk3", Status: core.DownloadFailed, DownloaderName: "dl",
		Source: "spotify", ExternalID: "sp-failed", Title: "Failed", Artist: "A"}
	queued := core.DownloadJob{ID: "j-queued", DedupKey: "dk4", Status: core.DownloadQueued, DownloaderName: "dl",
		Source: "spotify", ExternalID: "sp-queued", Title: "Queued", Artist: "A"}
	for _, j := range []core.DownloadJob{unlinked, linked, failed, queued} {
		if err := store.Insert(ctx, j, core.DownloadRequest{Source: j.Source, ExternalID: j.ExternalID, Title: j.Title, Artist: j.Artist}); err != nil {
			t.Fatal(err)
		}
	}
	minter := &fakeCanonicalMinter{retID: "trk_aaaa"}
	m := NewManager(Config{Workers: 1, DebounceWindow: time.Millisecond},
		wrapDownloaders(nil), store, nil, &fakeScanner{}, &fakeRematcher{trackID: "libtrack-1", coverArtID: "art-1"}, &fakeVersion{v: 1}, nil, nil, nil)
	m.SetCanonicalMinter(minter)
	var linkedHook []string
	m.SetLinkedHook(func(_ context.Context, cid string) { linkedHook = append(linkedHook, cid) })

	m.BackfillUnlinked()

	if minter.callCount() != 1 {
		t.Fatalf("minter called %d times, want once for the unlinked job", minter.callCount())
	}
	if id, _ := minter.lastCall(); id.Kind != "track" || id.Source != "spotify" || id.ExternalID != "sp1" || id.ISRC != "US001" {
		t.Fatalf("minted identity = %+v, want the unlinked job's", id)
	}
	if len(linkedHook) != 1 || linkedHook[0] != "trk_aaaa" {
		t.Fatalf("linked hook saw %v, want [trk_aaaa]", linkedHook)
	}
	for id, want := range map[string][2]string{
		"j-unlinked": {"libtrack-1", "trk_aaaa"},
		"j-linked":   {"existing-lib-track", "trk_existing"},
		"j-failed":   {"", ""},
		"j-queued":   {"", ""},
	} {
		got, _, _ := store.Get(ctx, id)
		if got.LibraryTrackID != want[0] || got.CanonicalID != want[1] {
			t.Fatalf("%s = (library %q, canonical %q), want (%q, %q)", id, got.LibraryTrackID, got.CanonicalID, want[0], want[1])
		}
	}
}

// Regression for cover rot: a job linked before canonical ids existed has a
// library_track_id but no canonical_id, so BackfillUnlinked and runScan (which
// gate on an empty library_track_id) never mint it. BackfillCanonicalIDs
// converges exactly those completed+linked+unminted rows, once.
func TestBackfillCanonicalIDs_MintsOnlyLegacyLinkedJobs(t *testing.T) {
	store := newMemStore()
	ctx := context.Background()
	for _, j := range []core.DownloadJob{
		{ID: "j-legacy", Status: core.DownloadCompleted, LibraryTrackID: "legacy-backend-track",
			Source: "spotify", ExternalID: "sp-legacy", Title: "Legacy Song", Artist: "L", Album: "M", ISRC: "US-legacy", DurationMs: 200000},
		{ID: "j-unlinked", Status: core.DownloadCompleted, Source: "spotify", ExternalID: "sp-unlinked", Title: "Unlinked"},
		{ID: "j-failed", Status: core.DownloadFailed, LibraryTrackID: "some-track", Source: "spotify", ExternalID: "sp-failed", Title: "Failed"},
		{ID: "j-queued", Status: core.DownloadQueued, Source: "spotify", ExternalID: "sp-queued", Title: "Queued"},
		{ID: "j-minted", Status: core.DownloadCompleted, LibraryTrackID: "track-minted", CanonicalID: "trk_already",
			Source: "spotify", ExternalID: "sp-minted", Title: "Minted"},
	} {
		j.DedupKey, j.DownloaderName = "dk-"+j.ID, "dl"
		if err := store.Insert(ctx, j, core.DownloadRequest{Source: j.Source, ExternalID: j.ExternalID, Title: j.Title}); err != nil {
			t.Fatal(err)
		}
	}
	minter := &fakeCanonicalMinter{retID: "trk_legacy_minted"}
	m := NewManager(Config{Workers: 1, DebounceWindow: time.Millisecond},
		wrapDownloaders(nil), store, nil, &fakeScanner{}, &fakeRematcher{trackID: "x"}, &fakeVersion{v: 1}, nil, nil, nil)
	m.SetCanonicalMinter(minter)

	m.BackfillCanonicalIDs()
	m.BackfillCanonicalIDs()

	if minter.callCount() != 1 {
		t.Fatalf("minted %d times across two runs, want once for the legacy job", minter.callCount())
	}
	if id, _ := minter.lastCall(); id.Source != "spotify" || id.ExternalID != "sp-legacy" || id.ISRC != "US-legacy" {
		t.Fatalf("mint identity built from wrong columns: %+v", id)
	}
	for id, want := range map[string]string{
		"j-legacy": "trk_legacy_minted", "j-unlinked": "", "j-failed": "", "j-queued": "", "j-minted": "trk_already",
	} {
		if got, _, _ := store.Get(ctx, id); got.CanonicalID != want {
			t.Fatalf("%s canonical_id = %q, want %q", id, got.CanonicalID, want)
		}
	}
}

// ─── Task 4: runScan targeted RefreshLinked ──────────────────────────────────

// fakeBindingResolver records RefreshLinked calls for assertions.
type fakeBindingResolver struct {
	mu     sync.Mutex
	calls  [][]string // each call's catalogIDs arg
	retErr error
}

func (f *fakeBindingResolver) Resolve(_ context.Context, _ string) (resolver.Addressing, error) {
	return resolver.Addressing{}, nil
}

func (f *fakeBindingResolver) RefreshLinked(_ context.Context, ids []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := make([]string, len(ids))
	copy(cp, ids)
	f.calls = append(f.calls, cp)
	return f.retErr
}

func (f *fakeBindingResolver) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func (f *fakeBindingResolver) getCall(i int) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[i]
}

// TestRunScan_RefreshLinkedCalledWithLinkedCanonicalIDs asserts that after
// runScan links jobs, RefreshLinked is called with exactly the canonical ids of
// the jobs linked in that scan (jobs that were skipped or got an empty
// canonical_id are excluded).
//
// Setup: enqueue 3 jobs that download and complete inside the debounce window
// (so BackfillUnlinked at Start does not consume them — BackfillUnlinked only
// sees already-completed jobs at startup, and these jobs are in Queued state
// then). The minter returns "" for ext3, so only trk_scan1 + trk_scan2 end up
// in the RefreshLinked call.
func TestRunScan_RefreshLinkedCalledWithLinkedCanonicalIDs(t *testing.T) {
	clk := newFakeClock()
	store := newMemStore()

	// Minter returns deterministic ids keyed by ExternalID; ext3 returns "".
	minterIDs := map[string]string{
		"ext1": "trk_scan1",
		"ext2": "trk_scan2",
		"ext3": "",
	}
	minter := &fakeDynamicMinter{ids: minterIDs}
	res := &fakeBindingResolver{}
	dl := &fakeDL{name: "dl", canDownload: true}
	bus := events.New()
	m := NewManager(
		Config{Workers: 3, DebounceWindow: 5 * time.Second, ScanPollEvery: time.Millisecond, ScanPollMax: time.Second, ScanSettleMax: 10 * time.Millisecond},
		wrapDownloaders([]Downloader{dl}), store, bus, &fakeScanner{}, &fakeRematcher{trackID: "lib-scan-track"}, &fakeVersion{v: 1}, clk, nil,
		func() BindingResolver { return res },
	)
	m.SetCanonicalMinter(minter)
	var linked []string
	m.SetLinkedHook(func(_ context.Context, cid string) { linked = append(linked, cid) })
	ctx := context.Background()
	for _, ext := range []string{"ext1", "ext2", "ext3"} {
		job := core.DownloadJob{
			ID: ext, Source: "spotify", ExternalID: ext, Title: "Song " + ext,
			Artist: "A", Album: "B", Status: core.DownloadCompleted,
		}
		if err := store.Insert(ctx, job, core.DownloadRequest{}); err != nil {
			t.Fatalf("Insert %s: %v", ext, err)
		}
	}

	// Test the scan itself directly. Starting workers here would also start the
	// one-shot startup backfill, which races this test by consuming completed jobs
	// before the debounce callback can scan them.
	m.pending = true
	m.runScan()

	if res.callCount() != 1 {
		t.Fatalf("RefreshLinked called %d times; want 1", res.callCount())
	}
	got := res.getCall(0)
	// Must contain exactly trk_scan1 and trk_scan2 (ext3 returned "" → excluded).
	if len(got) != 2 {
		t.Fatalf("RefreshLinked ids len=%d; want 2; got %v", len(got), got)
	}
	want := map[string]bool{"trk_scan1": true, "trk_scan2": true}
	for _, id := range got {
		if !want[id] {
			t.Fatalf("RefreshLinked got unexpected id %q; want only trk_scan1 and trk_scan2", id)
		}
		delete(want, id)
	}
	if len(want) > 0 {
		t.Fatalf("RefreshLinked missing expected ids: %v", want)
	}
	if len(linked) != 2 {
		t.Fatalf("linked hook saw %v; want the two minted ids", linked)
	}
}

// TestRunScan_NilResolverProviderDoesNotPanic asserts that when m.resolve is nil
// (or returns nil), runScan completes normally without panicking.
func TestRunScan_NilResolverProviderDoesNotPanic(t *testing.T) {
	ctx := context.Background()
	dl := &fakeDL{name: "dl", canDownload: true}
	bus := events.New()

	waitCompleted := func(t *testing.T, st *memStore, id string) {
		t.Helper()
		deadline := time.After(2 * time.Second)
		for {
			j, _, _ := st.Get(ctx, id)
			if j.Status == core.DownloadCompleted {
				return
			}
			select {
			case <-deadline:
				t.Fatal("job never completed")
			default:
				time.Sleep(time.Millisecond)
			}
		}
	}

	// Case 1: nil resolve provider.
	clk1 := newFakeClock()
	store1 := newMemStore()
	m1 := NewManager(
		Config{Workers: 1, DebounceWindow: 5 * time.Second, ScanPollEvery: time.Millisecond, ScanPollMax: time.Second, ScanSettleMax: 10 * time.Millisecond},
		wrapDownloaders([]Downloader{dl}), store1, bus, &fakeScanner{}, &fakeRematcher{trackID: "lib-t1"}, &fakeVersion{v: 1}, clk1, nil, nil,
	)
	t.Cleanup(m1.Stop)
	m1.Start()
	j1, err := m1.Enqueue(ctx, core.DownloadRequest{Source: "spotify", ExternalID: "nr-ext1", Title: "NilRes1", Artist: "A", Album: "B"})
	if err != nil {
		t.Fatal(err)
	}
	waitCompleted(t, store1, j1.ID)
	// Must not panic.
	clk1.Advance(5 * time.Second)

	// Case 2: provider returning nil.
	clk2 := newFakeClock()
	store2 := newMemStore()
	m2 := NewManager(
		Config{Workers: 1, DebounceWindow: 5 * time.Second, ScanPollEvery: time.Millisecond, ScanPollMax: time.Second, ScanSettleMax: 10 * time.Millisecond},
		wrapDownloaders([]Downloader{dl}), store2, bus, &fakeScanner{}, &fakeRematcher{trackID: "lib-t2"}, &fakeVersion{v: 1}, clk2, nil,
		func() BindingResolver { return nil },
	)
	t.Cleanup(m2.Stop)
	m2.Start()
	j2, err := m2.Enqueue(ctx, core.DownloadRequest{Source: "spotify", ExternalID: "nr-ext2", Title: "NilRes2", Artist: "A", Album: "B"})
	if err != nil {
		t.Fatal(err)
	}
	waitCompleted(t, store2, j2.ID)
	// Must not panic.
	clk2.Advance(5 * time.Second)
}

// fakeDynamicMinter returns canonical ids keyed by ExternalID, enabling per-job
// control over what id (or empty string) the minter returns.
type fakeDynamicMinter struct {
	mu  sync.Mutex
	ids map[string]string // ExternalID → canonical id
}

func (f *fakeDynamicMinter) CanonicalFor(_ context.Context, id catalog.Identity) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.ids[id.ExternalID], nil
}

// TestAdaptivePacingPausesAfterConsecutiveRateLimitFailures verifies that once
// a downloader hits Config.PacingThreshold consecutive rate_limited/bot_challenge
// failures, the Manager auto-pauses dispatch and auto-resumes after
// Config.PacingCooldown, without any manual Pause()/Resume() call.
func TestAdaptivePacingPausesAfterConsecutiveRateLimitFailures(t *testing.T) {
	dl := &fakeDL{name: "spotdl", canDownload: true, errOnStart: ClassifiedError{Class: ClassRateLimited, Err: errors.New("429")}}
	store := newMemStore()
	bus := events.New()
	scanner := &fakeScanner{}
	m := NewManager(
		Config{Workers: 1, DebounceWindow: 5 * time.Second, ScanPollEvery: time.Millisecond, ScanPollMax: time.Second, ScanSettleMax: 10 * time.Millisecond,
			PacingThreshold: 2, PacingCooldown: 30 * time.Millisecond},
		wrapDownloaders([]Downloader{dl}), store, bus, scanner, &fakeRematcher{trackID: "t1"}, &fakeVersion{v: 1}, nil, nil, nil,
	)
	t.Cleanup(m.Stop)
	m.Start()

	for i := 0; i < 2; i++ {
		if _, err := m.Enqueue(context.Background(), core.DownloadRequest{Source: "spotify", ExternalID: fmt.Sprintf("e%d", i), Artist: "A", Title: "T"}); err != nil {
			t.Fatal(err)
		}
		time.Sleep(20 * time.Millisecond)
	}

	if !m.IsPaused() {
		t.Fatal("expected Manager to auto-pause after 2 consecutive rate-limited failures")
	}

	// Both failed jobs are also auto-retried (Task 6) after PacingCooldown. Clear
	// errOnStart now so those auto-retries succeed instead of failing again and
	// re-triggering pacing indefinitely — this test is about the pause/resume
	// gate, not about the interaction with unbounded repeated failures.
	dl.setErrOnStart(nil)

	time.Sleep(60 * time.Millisecond) // longer than PacingCooldown
	if m.IsPaused() {
		t.Fatal("expected Manager to auto-resume after PacingCooldown elapses")
	}
}

// TestAdaptivePacingResetsOnSuccess verifies a successful download resets the
// consecutive-failure counter, so an isolated failure afterward doesn't
// immediately re-trigger pacing.
func TestAdaptivePacingResetsOnSuccess(t *testing.T) {
	dl := &fakeDL{name: "spotdl", canDownload: true}
	store := newMemStore()
	m, _ := testManager(t, []Downloader{dl}, store, nil, nil, nil)
	m.cfg.PacingThreshold = 2
	m.cfg.PacingCooldown = 30 * time.Millisecond

	if _, err := m.Enqueue(context.Background(), core.DownloadRequest{Source: "spotify", ExternalID: "e1", Artist: "A", Title: "T"}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)

	dl.setErrOnStart(ClassifiedError{Class: ClassRateLimited, Err: errors.New("429")})
	if _, err := m.Enqueue(context.Background(), core.DownloadRequest{Source: "spotify", ExternalID: "e2", Artist: "A", Title: "T"}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)

	if m.IsPaused() {
		t.Fatal("a single failure after a success should not trigger pacing (threshold=2)")
	}
}

// TestAutoRetryRateLimitedFailureAfterCooldown verifies a job whose ENTIRE
// downloader chain fails with a retryable class (rate_limited/bot_challenge)
// is automatically re-queued after Config.PacingCooldown, without a manual
// Retry() call, up to Config.MaxAutoRetries attempts.
func TestAutoRetryRateLimitedFailureAfterCooldown(t *testing.T) {
	dl := &fakeDL{name: "spotdl", canDownload: true, errOnStart: ClassifiedError{Class: ClassRateLimited, Err: errors.New("429")}}
	store := newMemStore()
	bus := events.New()
	scanner := &fakeScanner{}
	m := NewManager(
		Config{Workers: 1, DebounceWindow: 5 * time.Second, ScanPollEvery: time.Millisecond, ScanPollMax: time.Second, ScanSettleMax: 10 * time.Millisecond,
			PacingThreshold: 100, PacingCooldown: 20 * time.Millisecond, MaxAutoRetries: 5},
		wrapDownloaders([]Downloader{dl}), store, bus, scanner, &fakeRematcher{trackID: "t1"}, &fakeVersion{v: 1}, nil, nil, nil,
	)
	t.Cleanup(m.Stop)
	m.Start()

	job, err := m.Enqueue(context.Background(), core.DownloadRequest{Source: "spotify", ExternalID: "e1", Artist: "A", Title: "T"})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(15 * time.Millisecond)

	got, _, _ := store.Get(context.Background(), job.ID)
	if got.Status != core.DownloadFailed {
		t.Fatalf("job status = %v, want DownloadFailed before the auto-retry fires", got.Status)
	}

	time.Sleep(40 * time.Millisecond) // longer than PacingCooldown
	got, _, _ = store.Get(context.Background(), job.ID)
	if got.Attempts < 1 {
		t.Fatalf("job Attempts = %d, want >= 1 after an automatic retry", got.Attempts)
	}
}

// TestNoAutoRetryForNonRetryableClass verifies a no_match/unavailable/
// spotify_api_error failure is left failed and never auto-retried.
func TestNoAutoRetryForNonRetryableClass(t *testing.T) {
	dl := &fakeDL{name: "spotdl", canDownload: true, errOnStart: ClassifiedError{Class: ClassNoMatch, Err: errors.New("no results")}}
	store := newMemStore()
	m, _ := testManager(t, []Downloader{dl}, store, nil, nil, nil)
	m.cfg.PacingCooldown = 20 * time.Millisecond
	m.cfg.MaxAutoRetries = 5

	job, err := m.Enqueue(context.Background(), core.DownloadRequest{Source: "spotify", ExternalID: "e1", Artist: "A", Title: "T"})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond) // well past PacingCooldown

	got, _, _ := store.Get(context.Background(), job.ID)
	if got.Status != core.DownloadFailed || got.Attempts != 0 {
		t.Fatalf("job = %+v, want DownloadFailed with Attempts=0 (never auto-retried)", got)
	}
}

// TestPickPreferDownloaderWins: PreferDownloader jumps a named downloader ahead of
// the configured order when it accepts the request.
func TestPickPreferDownloaderWins(t *testing.T) {
	first := &fakeDL{name: "spotdl", canDownload: true}
	second := &fakeDL{name: "ytdlp", canDownload: true}
	m, _ := testManager(t, []Downloader{first, second}, newMemStore(), nil, nil, nil)

	job, err := m.Enqueue(context.Background(), core.DownloadRequest{
		Source: "youtube", ExternalID: "yt1", Artist: "A", Title: "T",
		PreferDownloader: "ytdlp",
	})
	if err != nil {
		t.Fatal(err)
	}
	if job.DownloaderName != "ytdlp" {
		t.Fatalf("preferred downloader should win, got %q", job.DownloaderName)
	}
}

// TestPickPreferDownloaderFallsBack: an unconfigured preference is ignored and the
// normal chain still runs.
func TestPickPreferDownloaderFallsBack(t *testing.T) {
	first := &fakeDL{name: "spotdl", canDownload: true}
	m, _ := testManager(t, []Downloader{first}, newMemStore(), nil, nil, nil)

	job, err := m.Enqueue(context.Background(), core.DownloadRequest{
		Source: "youtube", ExternalID: "yt2", Artist: "A", Title: "T",
		PreferDownloader: "ytdlp",
	})
	if err != nil {
		t.Fatal(err)
	}
	if job.DownloaderName != "spotdl" {
		t.Fatalf("should fall back to configured chain, got %q", job.DownloaderName)
	}
}

func TestEnqueueChapterSplitCreatesJobPerChapter(t *testing.T) {
	dl := &fakeDL{name: "dl", canDownload: true}
	store := newMemStore()
	m, _ := testManager(t, []Downloader{dl}, store, nil, nil, nil)
	base := core.DownloadRequest{Source: "youtube", ExternalID: "vid1", Artist: "A", Album: "Video"}

	var ids []string
	for _, ch := range [][2]string{{"0", "120"}, {"120", "240"}, {"240", ""}} {
		req := base
		req.Title = "Chapter " + ch[0]
		req.SectionStart, req.SectionEnd = ch[0], ch[1]
		j, err := m.Enqueue(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, j.ID)
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			t.Fatalf("chapters collapsed into one job: %v", ids)
		}
		seen[id] = true
	}
}

// fakeEnricher records lookups and answers from a fixed table.
type fakeEnricher struct {
	isrc  map[string]string
	err   error
	calls int
}

func (f *fakeEnricher) GetTrack(_ context.Context, source, id string) (core.ExternalResult, error) {
	f.calls++
	if f.err != nil {
		return core.ExternalResult{}, f.err
	}
	return core.ExternalResult{Source: source, ExternalID: id, ISRC: f.isrc[source+":"+id]}, nil
}

// Deezer search payloads carry no ISRC, so without enrichment a Deezer download
// reaches spotDL as a fuzzy "<artist> - <title>" query — ~28s of guessing that an
// isrc: lookup does exactly. One /track/{id} call at enqueue buys that, and the
// ISRC lands on the job row where the post-download rematcher reads it too.
func TestEnqueueEnrichesMissingISRC(t *testing.T) {
	store := newMemStore()
	m := NewManager(Config{}, wrapDownloaders([]Downloader{&fakeDL{name: "dl", canDownload: true}}), store, events.New(), &fakeScanner{}, nil, nil, RealClock{}, nil, nil)
	enr := &fakeEnricher{isrc: map[string]string{"deezer:123": "GBDUW0000059"}}
	m.SetTrackEnricher(enr)

	job, err := m.Enqueue(context.Background(), core.DownloadRequest{Source: "deezer", ExternalID: "123", Artist: "A", Title: "T"})
	if err != nil {
		t.Fatal(err)
	}
	if job.ISRC != "GBDUW0000059" {
		t.Fatalf("job ISRC = %q, want it enriched onto the row", job.ISRC)
	}
}

// An ISRC we already have needs no network call, and enrichment must never
// overwrite one the caller supplied.
func TestEnqueueSkipsEnrichmentWhenISRCPresent(t *testing.T) {
	store := newMemStore()
	m := NewManager(Config{}, wrapDownloaders([]Downloader{&fakeDL{name: "dl", canDownload: true}}), store, events.New(), &fakeScanner{}, nil, nil, RealClock{}, nil, nil)
	enr := &fakeEnricher{isrc: map[string]string{"deezer:123": "OTHER0000000"}}
	m.SetTrackEnricher(enr)

	job, err := m.Enqueue(context.Background(), core.DownloadRequest{Source: "deezer", ExternalID: "123", Artist: "A", Title: "T", ISRC: "GBDUW0000059"})
	if err != nil {
		t.Fatal(err)
	}
	if job.ISRC != "GBDUW0000059" {
		t.Fatalf("job ISRC = %q, want the caller's value untouched", job.ISRC)
	}
	if enr.calls != 0 {
		t.Fatalf("enricher called %d times, want 0", enr.calls)
	}
}

// Enrichment is an optimisation. A search source that is down, rate-limited, or
// simply has no ISRC must not stop the download from being queued.
func TestEnqueueSurvivesEnricherFailure(t *testing.T) {
	store := newMemStore()
	m := NewManager(Config{}, wrapDownloaders([]Downloader{&fakeDL{name: "dl", canDownload: true}}), store, events.New(), &fakeScanner{}, nil, nil, RealClock{}, nil, nil)
	m.SetTrackEnricher(&fakeEnricher{err: errors.New("deezer down")})

	job, err := m.Enqueue(context.Background(), core.DownloadRequest{Source: "deezer", ExternalID: "123", Artist: "A", Title: "T"})
	if err != nil {
		t.Fatalf("enricher failure must not fail the enqueue: %v", err)
	}
	if job.ISRC != "" {
		t.Fatalf("job ISRC = %q, want empty", job.ISRC)
	}
}

// Stop must not wait out the job timeout. A worker only checks stopCh between
// jobs, so without cancelling the in-flight job Stop blocks for up to the
// per-job timeout — a desktop quit that hangs with the single-instance lock
// held, and an adapter save that hangs the HTTP request behind it.
func TestStopCancelsInFlightDownloads(t *testing.T) {
	dl := &fakeDL{name: "dl", canDownload: true, block: make(chan struct{})}
	store := newMemStore()
	m := NewManager(Config{Workers: 1, JobTimeout: time.Hour, DebounceWindow: time.Hour},
		wrapDownloaders([]Downloader{dl}), store, events.New(), &fakeScanner{}, nil, nil, RealClock{}, nil, nil)
	m.Start()

	job, err := m.Enqueue(context.Background(), core.DownloadRequest{Source: "spotify", ExternalID: "e1", Artist: "A", Title: "T"})
	if err != nil {
		t.Fatal(err)
	}
	waitForStatus(t, store, job.ID, core.DownloadRunning)

	done := make(chan struct{})
	go func() { m.Stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Stop blocked on an in-flight download instead of cancelling it")
	}

	// The job did not fail and was not cancelled by the user: it goes back to
	// queued so the next Manager's recovery pass re-dispatches it.
	cur, _, _ := store.Get(context.Background(), job.ID)
	if cur.Status != core.DownloadQueued {
		t.Fatalf("job after shutdown = %q, want queued so it resumes", cur.Status)
	}
}

// syncScanner finishes its scan inside StartScan, like the localfiles library.
type syncScanner struct{ fakeScanner }

func (*syncScanner) ScansSynchronously() bool { return true }

// A scan that is already over when StartScan returns never reports
// Scanning=true, so waiting for it to begin only burns the settle window —
// seconds a phone's newly fetched tracks spend unplayable.
func TestSynchronousScanRematchesWithoutWaitingForTheScanToStart(t *testing.T) {
	clk := newFakeClock()
	store := newMemStore()
	m := NewManager(Config{Workers: 1, DebounceWindow: time.Second, ScanPollEvery: time.Millisecond, ScanSettleMax: time.Hour},
		wrapDownloaders([]Downloader{&fakeDL{name: "dl", canDownload: true}}), store, events.New(), &syncScanner{},
		&fakeRematcher{trackID: "lib-1"}, &fakeVersion{v: 1}, clk, nil, nil)
	t.Cleanup(m.Stop)
	m.Start()
	job, err := m.Enqueue(context.Background(), core.DownloadRequest{Source: "spotify", ExternalID: "e1", Artist: "A", Title: "T"})
	if err != nil {
		t.Fatal(err)
	}
	waitForStatus(t, store, job.ID, core.DownloadCompleted)

	scanned := make(chan struct{})
	go func() { clk.Advance(time.Second); close(scanned) }()
	select {
	case <-scanned:
	case <-time.After(2 * time.Second):
		t.Fatal("rematch waited for a synchronous scan to start")
	}
	if got, _, _ := store.Get(context.Background(), job.ID); got.LibraryTrackID != "lib-1" {
		t.Fatalf("library_track_id = %q, want lib-1", got.LibraryTrackID)
	}
}

// A user cancel is still a cancel — the shutdown path must not swallow it.
func TestCancelStillMarksCanceled(t *testing.T) {
	dl := &fakeDL{name: "dl", canDownload: true, block: make(chan struct{})}
	store := newMemStore()
	m := NewManager(Config{Workers: 1, JobTimeout: time.Hour, DebounceWindow: time.Hour},
		wrapDownloaders([]Downloader{dl}), store, events.New(), &fakeScanner{}, nil, nil, RealClock{}, nil, nil)
	t.Cleanup(m.Stop)
	m.Start()

	job, err := m.Enqueue(context.Background(), core.DownloadRequest{Source: "spotify", ExternalID: "e1", Artist: "A", Title: "T"})
	if err != nil {
		t.Fatal(err)
	}
	waitForStatus(t, store, job.ID, core.DownloadRunning)
	if err := m.Cancel(context.Background(), job.ID); err != nil {
		t.Fatal(err)
	}
	waitForStatus(t, store, job.ID, core.DownloadCanceled)
}

// The incoming Manager runs its recovery pass while the outgoing one is still
// working, so the jobs Stop requeues need a second pass to reach a worker.
func TestRedispatchQueuedResumesRequeuedJobs(t *testing.T) {
	dl := &fakeDL{name: "dl", canDownload: true}
	store := newMemStore()
	m := NewManager(Config{Workers: 1, DebounceWindow: time.Hour},
		wrapDownloaders([]Downloader{dl}), store, events.New(), &fakeScanner{}, nil, nil, RealClock{}, nil, nil)
	t.Cleanup(m.Stop)

	// A queued row left behind by the previous Manager, already past the point
	// where this one's recovery pass would have seen it.
	m.Start()
	job := core.DownloadJob{ID: "left-over", Status: core.DownloadQueued, Source: "spotify",
		ExternalID: "e1", Artist: "A", Title: "T", DownloaderName: "dl"}
	req := core.DownloadRequest{Source: "spotify", ExternalID: "e1", Artist: "A", Title: "T"}
	if err := store.Insert(context.Background(), job, req); err != nil {
		t.Fatal(err)
	}

	m.RedispatchQueued()
	waitForStatus(t, store, job.ID, core.DownloadCompleted)
}

func waitForStatus(t *testing.T, store JobStore, id string, want core.DownloadStatus) {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		cur, _, _ := store.Get(context.Background(), id)
		if cur.Status == want {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("job %s never reached %q (last %q)", id, want, cur.Status)
		default:
			time.Sleep(time.Millisecond)
		}
	}
}
