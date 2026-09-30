package download

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/uhhhm/reverb/internal/core"
	"github.com/uhhhm/reverb/internal/events"
	"github.com/uhhhm/reverb/internal/store"
)

// Exercise the entire acquisition/recovery lifecycle against real SQLite faults.
// Unlike the Device scenario this owns publication ordering, simultaneous
// controls, and recovery of an external downloader's existing reference.
func TestCompletionLifecycleE2E(t *testing.T) {
	for _, asyncMode := range []bool{false, true} {
		for _, failedPhase := range []string{"pending", "terminal"} {
			t.Run(fmt.Sprintf("async=%v/fail=%s", asyncMode, failedPhase), func(t *testing.T) {
				ctx := context.Background()
				st, err := store.Open(filepath.Join(t.TempDir(), "jobs.db"))
				if err != nil {
					t.Fatal(err)
				}
				defer st.Close()
				if err := st.Migrate(); err != nil {
					t.Fatal(err)
				}
				s := NewSQLStore(st.Q())
				sql := func(q string) {
					t.Helper()
					if _, err := st.DB().Exec(q); err != nil {
						t.Fatal(err)
					}
				}
				condition := "NEW.completion_pending = 1"
				if failedPhase == "terminal" {
					condition = "NEW.status = 'completed'"
				}
				sql("CREATE TRIGGER fail_job_write BEFORE UPDATE ON download_jobs WHEN " + condition + " BEGIN SELECT RAISE(FAIL, 'job persistence unavailable'); END")
				syncDL := &fakeDL{name: "sync", canDownload: true}
				asyncDL := &fakeAsyncDL{name: "async", submitRef: "existing-album-42", status: AsyncStatus{State: core.DownloadCompleted}}
				var dl Downloader = syncDL
				req := core.DownloadRequest{Source: "spotify", ExternalID: "lifecycle", Title: "Recovery", Artist: "Fixture", InitiatedBy: "local", RecommendationOrigin: core.RecommendationRadio}
				if asyncMode {
					dl = asyncDL
					req.Granularity = core.GranularityAlbum
				}
				bus := events.New()
				completions, unsubscribe := bus.Subscribe(TopicComplete)
				defer unsubscribe()
				scanner := &fakeScanner{}
				build := func() *Manager {
					return NewManager(Config{Workers: 1, ReconcileEvery: 20 * time.Millisecond, DebounceWindow: time.Hour}, wrapDownloaders([]Downloader{dl}), s, bus, scanner, &fakeRematcher{}, &fakeVersion{}, RealClock{}, nil, nil)
				}
				var hooks atomic.Int32
				hook := func(_ context.Context, req core.DownloadRequest, _ string) error {
					hooks.Add(1)
					if req.InitiatedBy != "local" || req.RecommendationOrigin != core.RecommendationRadio || req.CompletionID == "" {
						return fmt.Errorf("lost attribution: %+v", req)
					}
					return nil
				}
				m := build()
				m.SetCompletionHook(hook)
				m.Start()
				defer func() { m.Stop() }()
				job, err := m.Enqueue(ctx, req)
				if err != nil {
					t.Fatal(err)
				}
				// Wait through the public Retry boundary until output ownership is visible:
				// a pending output retries persistence and returns its storage failure.
				deadline := time.Now().Add(3 * time.Second)
				for {
					_, err = m.Retry(ctx, job.ID, "")
					if err != nil {
						break
					}
					if time.Now().After(deadline) {
						t.Fatal("output did not enter recoverable phase")
					}
					time.Sleep(5 * time.Millisecond)
				}
				if err := m.Cancel(ctx, job.ID); err == nil {
					t.Fatal("Cancel discarded pending output")
				}
				if err := m.Clear(ctx, job.ID); err == nil {
					t.Fatal("Clear discarded pending output")
				}
				if ids, err := m.ClearFinished(ctx); err != nil || len(ids) != 0 {
					t.Fatalf("clear %v %v", ids, err)
				}
				if len(completions) != 0 || scanner.count() != 0 {
					t.Fatal("completion or linking preceded durable terminal update")
				}
				if failedPhase == "pending" {
					if hooks.Load() != 0 {
						t.Fatal("bookkeeping ran before output was durable")
					}
					stored, _, _ := s.Get(ctx, job.ID)
					if stored.CompletionPending {
						t.Fatal("claimed restart durability during pending-write outage")
					}
				}
				// Overlapping controls must share the existing output even during the outage.
				var wg sync.WaitGroup
				for i := 0; i < 8; i++ {
					wg.Add(1)
					go func() {
						defer wg.Done()
						if _, err := m.Enqueue(ctx, req); err == nil {
							t.Error("Add reported success while storage was unavailable")
						}
					}()
				}
				wg.Wait()
				m.Stop()
				sql("DROP TRIGGER fail_job_write")
				// Keep the hook failing while we explicitly make the pending row durable.
				// The pending-write outage cannot survive restart until persistence recovers.
				m.SetCompletionHook(func(context.Context, core.DownloadRequest, string) error { return fmt.Errorf("recording unavailable") })
				if _, err := m.Retry(ctx, job.ID, ""); err == nil {
					t.Fatal("recording failure unexpectedly completed")
				}
				stored, _, err := s.Get(ctx, job.ID)
				if err != nil || !stored.CompletionPending || stored.Status != core.DownloadRunning {
					t.Fatalf("pending row %+v %v", stored, err)
				}
				if asyncMode && stored.DownloaderRef != "existing-album-42" {
					t.Fatal("lost asynchronous output reference")
				}
				m = build()
				m.SetCompletionHook(hook)
				m.Start()
				waitForStatus(t, s, job.ID, core.DownloadCompleted)
				// Force concurrent retries after success; publication must stay singular.
				for i := 0; i < 8; i++ {
					wg.Add(1)
					go func() {
						defer wg.Done()
						if _, err := m.Retry(ctx, job.ID, ""); err != nil {
							t.Error(err)
						}
					}()
				}
				wg.Wait()
				if len(completions) != 1 {
					t.Fatalf("completion publications=%d", len(completions))
				}
				if asyncMode {
					asyncDL.mu.Lock()
					calls := asyncDL.submitCalls
					asyncDL.mu.Unlock()
					if calls != 1 {
						t.Fatalf("submissions=%d", calls)
					}
				} else if syncDL.starts() != 1 {
					t.Fatalf("downloads=%d", syncDL.starts())
				}
			})
		}
	}
}

// A version-52 fixture has no phase column. Upgrade and reopen twice, retaining
// request attribution and asynchronous output identity without converting other
// running or failed jobs into completed-output candidates.
func TestLegacyCompletionUpgradeE2E(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "legacy.db")
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().Exec(`ALTER TABLE download_jobs DROP COLUMN completion_pending`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().Exec(`DELETE FROM goose_db_version WHERE version_id = 53`); err != nil {
		t.Fatal(err)
	}
	req := core.DownloadRequest{Source: "spotify", ExternalID: "legacy", Title: "Fixture", InitiatedBy: "local", RecommendationOrigin: core.RecommendationRadio, Granularity: core.GranularityAlbum, AddToPlaylistID: "playlist-fixture"}
	for _, j := range []core.DownloadJob{
		{ID: "pending", DedupKey: "pending", Status: core.DownloadRunning, Error: "record completed download: unavailable", OutputPath: "/fixture.mp3", DownloaderName: "async", DownloaderRef: "album-42"},
		{ID: "running", DedupKey: "running", Status: core.DownloadRunning, Error: "downloader unavailable"},
		{ID: "failed", DedupKey: "failed", Status: core.DownloadFailed, Error: "record completed download: misleading display text"},
	} {
		if err := NewSQLStore(st.Q()).Insert(ctx, j, req); err != nil {
			t.Fatal(err)
		}
	}
	st.Close()
	for i := 0; i < 2; i++ {
		st, err = store.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := st.Migrate(); err != nil {
			t.Fatal(err)
		}
		s := NewSQLStore(st.Q())
		pending, _, err := s.Get(ctx, "pending")
		if err != nil || !pending.CompletionPending || pending.OutputPath != "/fixture.mp3" || pending.DownloaderRef != "album-42" {
			t.Fatalf("upgrade lost pending output: %+v %v", pending, err)
		}
		got, ok, err := s.GetRequest(ctx, "pending")
		if err != nil || !ok || got != req {
			t.Fatalf("upgrade lost request: %+v %v", got, err)
		}
		for _, id := range []string{"running", "failed"} {
			j, _, err := s.Get(ctx, id)
			if err != nil || j.CompletionPending {
				t.Fatalf("upgrade converted %s: %+v %v", id, j, err)
			}
		}
		st.Close()
	}
	// The upgrade backup is also a real artifact of the version-52 fixture.
	if _, err := os.Stat(path + ".pre-migrate-v52.bak"); err != nil {
		t.Fatal(err)
	}
}

type completionRequestOutage struct {
	JobStore
	unavailable atomic.Bool
}

func (s *completionRequestOutage) GetRequest(ctx context.Context, id string) (core.DownloadRequest, bool, error) {
	if s.unavailable.Load() {
		return core.DownloadRequest{}, false, fmt.Errorf("request read unavailable")
	}
	return s.JobStore.GetRequest(ctx, id)
}

func TestCompletionRequestReadFailureE2E(t *testing.T) {
	ctx := context.Background()
	s := &completionRequestOutage{JobStore: newSQLStore(t)}
	dl := &fakeDL{name: "sync", canDownload: true}
	build := func() *Manager {
		return NewManager(Config{Workers: 1, ReconcileEvery: time.Hour, DebounceWindow: time.Hour}, wrapDownloaders([]Downloader{dl}), s, events.New(), &fakeScanner{}, &fakeRematcher{}, &fakeVersion{}, RealClock{}, nil, nil)
	}
	m := build()
	m.SetCompletionHook(func(context.Context, core.DownloadRequest, string) error { return fmt.Errorf("recording unavailable") })
	m.Start()
	j, err := m.Enqueue(ctx, core.DownloadRequest{Source: "spotify", ExternalID: "read-fault", Artist: "Fixture", Title: "Request recovery", RecommendationOrigin: core.RecommendationRadio, InitiatedBy: "local"})
	if err != nil {
		t.Fatal(err)
	}
	waitForCompletionError(t, s, j.ID)
	m.Stop()
	m = build()
	m.SetCompletionHook(func(_ context.Context, req core.DownloadRequest, _ string) error {
		if req.RecommendationOrigin == "" {
			return nil
		} // production hook skips unattributed requests
		if req.InitiatedBy != "local" {
			return fmt.Errorf("lost initiator")
		}
		return nil
	})
	s.unavailable.Store(true)
	got, err := m.Retry(ctx, j.ID, "")
	if err == nil || got.Status == core.DownloadCompleted {
		t.Fatalf("request outage bypassed required recording: job=%+v err=%v", got, err)
	}
	s.unavailable.Store(false)
	m.Start()
	defer m.Stop()
	waitForStatus(t, s, j.ID, core.DownloadCompleted)
	if dl.starts() != 1 {
		t.Fatal("request read recovery downloaded again")
	}
}

type heldCancellation struct {
	*fakeAsyncDL
	entered chan struct{}
	release chan struct{}
}

func (d *heldCancellation) CancelAsync(context.Context, string) error {
	close(d.entered)
	<-d.release
	return nil
}

func TestCancellationOverlappingCompletionE2E(t *testing.T) {
	ctx := context.Background()
	s := newSQLStore(t)
	dl := &heldCancellation{fakeAsyncDL: &fakeAsyncDL{name: "async", submitRef: "album-42", status: AsyncStatus{State: core.DownloadRunning}}, entered: make(chan struct{}), release: make(chan struct{})}
	m := NewManager(Config{Workers: 1, ReconcileEvery: 10 * time.Millisecond, DebounceWindow: time.Hour}, wrapDownloaders([]Downloader{dl}), s, events.New(), &fakeScanner{}, &fakeRematcher{}, &fakeVersion{}, RealClock{}, nil, nil)
	m.SetCompletionHook(func(context.Context, core.DownloadRequest, string) error { return fmt.Errorf("recording unavailable") })
	m.Start()
	defer m.Stop()
	j, err := m.Enqueue(ctx, core.DownloadRequest{Source: "spotify", ExternalID: "cancel-fixture", Granularity: core.GranularityAlbum})
	if err != nil {
		t.Fatal(err)
	}
	waitForStatus(t, s, j.ID, core.DownloadRunning)
	canceled := make(chan error, 1)
	go func() { canceled <- m.Cancel(ctx, j.ID) }()
	select {
	case <-dl.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("cancel did not reach downloader")
	}
	dl.setStatus(AsyncStatus{State: core.DownloadCompleted})
	waitForCompletionError(t, s, j.ID)
	close(dl.release)
	if err := <-canceled; err == nil {
		t.Fatal("stale Cancel overwrote pending output")
	}
	if ids, err := m.ClearFinished(ctx); err != nil || len(ids) != 0 {
		t.Fatalf("clearing discarded pending output: %v %v", ids, err)
	}
	stored, ok, err := s.Get(ctx, j.ID)
	if err != nil || !ok || !stored.CompletionPending || stored.DownloaderRef != "album-42" {
		t.Fatalf("pending output lost: %+v %v", stored, err)
	}
}

type heldCompletedPoll struct {
	*fakeAsyncDL
	entered      chan struct{}
	release      chan struct{}
	cancelCalled chan struct{}
	once         sync.Once
}

func (d *heldCompletedPoll) Poll(context.Context, string) (AsyncStatus, error) {
	d.once.Do(func() { close(d.entered); <-d.release })
	return AsyncStatus{State: core.DownloadCompleted}, nil
}
func (d *heldCompletedPoll) CancelAsync(context.Context, string) error {
	close(d.cancelCalled)
	return nil
}

func TestClearingWhileCompletionPollReturnsE2E(t *testing.T) {
	ctx := context.Background()
	s := newSQLStore(t)
	dl := &heldCompletedPoll{fakeAsyncDL: &fakeAsyncDL{name: "async", submitRef: "album-42"}, entered: make(chan struct{}), release: make(chan struct{}), cancelCalled: make(chan struct{})}
	m := NewManager(Config{Workers: 1, ReconcileEvery: 10 * time.Millisecond, DebounceWindow: time.Hour}, wrapDownloaders([]Downloader{dl}), s, events.New(), &fakeScanner{}, &fakeRematcher{}, &fakeVersion{}, RealClock{}, nil, nil)
	m.SetCompletionHook(func(context.Context, core.DownloadRequest, string) error { return fmt.Errorf("recording unavailable") })
	m.Start()
	defer m.Stop()
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(dl.release) }) }
	defer release()
	j, err := m.Enqueue(ctx, core.DownloadRequest{Source: "spotify", ExternalID: "poll-fixture", Granularity: core.GranularityAlbum})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-dl.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("poll did not reach downloader")
	}
	canceled := make(chan error, 1)
	go func() { canceled <- m.Cancel(ctx, j.ID) }()
	select {
	case <-dl.cancelCalled:
	case <-time.After(3 * time.Second):
		t.Fatal("cancel did not reach downloader")
	}
	// Cancellation may ask the downloader while its poll runs, but its durable
	// mutation must wait for that poll's confirmed output handoff.
	select {
	case err := <-canceled:
		t.Fatalf("cancellation committed while completion poll was in flight: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if ids, err := m.ClearFinished(ctx); err != nil || len(ids) != 0 {
		t.Fatalf("clearing deleted in-flight output: %v %v", ids, err)
	}
	release()
	waitForCompletionError(t, s, j.ID)
	select {
	case err := <-canceled:
		if err == nil {
			t.Fatal("cancellation discarded confirmed output")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancellation never resumed")
	}
	if ids, err := m.ClearFinished(ctx); err != nil || len(ids) != 0 {
		t.Fatalf("clearing deleted confirmed output: %v %v", ids, err)
	}
	stored, ok, err := s.Get(ctx, j.ID)
	if err != nil || !ok || !stored.CompletionPending || stored.DownloaderRef != "album-42" {
		t.Fatalf("output ownership lost: %+v %v", stored, err)
	}
}
