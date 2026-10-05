package download

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/uhhhm/reverb/internal/core"
	"github.com/uhhhm/reverb/internal/events"
	"github.com/uhhhm/reverb/internal/store"
)

// Competing actions on one Download, each coordinated with a barrier or a
// queue sentinel rather than a timing guess. Every test runs against SQLite,
// which persists only what its UPDATE names, so a field the Manager changes but
// the store drops shows up here and not against the in-memory store.

// recorder keeps every event the Manager publishes, in order.
type recorder struct {
	mu     sync.Mutex
	events []events.Event
}

func (r *recorder) Publish(ev events.Event) {
	r.mu.Lock()
	r.events = append(r.events, ev)
	r.mu.Unlock()
}

// statuses lists the job statuses published for id, in order.
func (r *recorder) statuses(id string) []core.DownloadStatus {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []core.DownloadStatus
	for _, ev := range r.events {
		if p, ok := ev.Payload.(core.DownloadEvent); ok && p.JobID == id {
			out = append(out, p.Status)
		}
	}
	return out
}

func lifecycleManager(t *testing.T, s JobStore, bus Publisher, cfg Config, dls ...Downloader) *Manager {
	t.Helper()
	if cfg.Workers == 0 {
		cfg.Workers = 1
	}
	if cfg.DebounceWindow == 0 {
		cfg.DebounceWindow = time.Hour
	}
	if cfg.ReconcileEvery == 0 {
		cfg.ReconcileEvery = time.Hour
	}
	m := NewManager(cfg, wrapDownloaders(dls), s, bus, &fakeScanner{}, &fakeRematcher{}, &fakeVersion{}, RealClock{}, nil, nil)
	m.Start()
	t.Cleanup(m.Stop)
	return m
}

func openTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(t.TempDir() + "/jobs.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	return st
}

func trackReq(id string) core.DownloadRequest {
	return core.DownloadRequest{Source: "deezer", ExternalID: id, Artist: "Fixture", Title: "Track " + id}
}

func albumReq(id string) core.DownloadRequest {
	return core.DownloadRequest{Source: "deezer", ExternalID: id, Artist: "Fixture", Title: "Album " + id, Granularity: core.GranularityAlbum}
}

func startsFor(dl *fakeDL, externalID string) int {
	n := 0
	for _, r := range dl.requests() {
		if r.ExternalID == externalID {
			n++
		}
	}
	return n
}

func mustGet(t *testing.T, s JobStore, id string) core.DownloadJob {
	t.Helper()
	j, ok, err := s.Get(context.Background(), id)
	if err != nil || !ok {
		t.Fatalf("job %s: ok=%v err=%v", id, ok, err)
	}
	return j
}

// drain enqueues a sentinel behind everything already queued on a one-worker
// Manager and waits for it, so every earlier dispatch has been handled.
func drain(t *testing.T, m *Manager, s JobStore) {
	t.Helper()
	j, err := m.Enqueue(context.Background(), trackReq("sentinel-"+time.Now().Format("150405.000000000")))
	if err != nil {
		t.Fatal(err)
	}
	waitForStatus(t, s, j.ID, core.DownloadCompleted)
}

func TestRetryAttemptsAndRequestSurviveRestart(t *testing.T) {
	ctx := context.Background()
	s := newSQLStore(t)
	dl := &fakeDL{name: "sync", canDownload: true, errOnStart: errors.New("no match")}
	m := lifecycleManager(t, s, nil, Config{}, dl)
	req := trackReq("retried")
	req.Quality = core.QualityBest
	req.SectionStart, req.SectionEnd = "1:00", "2:00"
	req.RecommendationOrigin, req.InitiatedBy = core.RecommendationRadio, "local"
	job, err := m.Enqueue(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	waitForStatus(t, s, job.ID, core.DownloadFailed)
	if _, err := m.Retry(ctx, job.ID, ""); err != nil {
		t.Fatal(err)
	}
	waitForStatus(t, s, job.ID, core.DownloadFailed)
	if _, err := m.Retry(ctx, job.ID, "https://example.test/watch?v=manual"); err != nil {
		t.Fatal(err)
	}
	waitForStatus(t, s, job.ID, core.DownloadFailed)
	m.Stop()

	if got := mustGet(t, s, job.ID).Attempts; got != 2 {
		t.Fatalf("attempts = %d after two retries, want 2", got)
	}
	// The next manager retries with everything the original asked for.
	dl.setErrOnStart(nil)
	m = lifecycleManager(t, s, nil, Config{}, dl)
	if _, err := m.Retry(ctx, job.ID, "https://example.test/watch?v=manual"); err != nil {
		t.Fatal(err)
	}
	waitForStatus(t, s, job.ID, core.DownloadCompleted)
	if got := mustGet(t, s, job.ID).Attempts; got != 3 {
		t.Fatalf("attempts = %d after three retries, want 3", got)
	}
	reqs := dl.requests()
	last := reqs[len(reqs)-1]
	if last.ManualURL == "" || last.Quality != req.Quality || last.SectionStart != "1:00" || last.SectionEnd != "2:00" ||
		last.RecommendationOrigin != core.RecommendationRadio || last.InitiatedBy != "local" {
		t.Fatalf("retry lost its request: %+v", last)
	}
}

func TestAutomaticRetriesStopAtTheLimit(t *testing.T) {
	s := newSQLStore(t)
	dl := &fakeDL{name: "sync", canDownload: true, errOnStart: ClassifiedError{Class: ClassRateLimited, Err: errors.New("HTTP 429")}}
	m := lifecycleManager(t, s, nil, Config{MaxAutoRetries: 2, PacingCooldown: 20 * time.Millisecond, PacingThreshold: 100}, dl)
	job, err := m.Enqueue(context.Background(), trackReq("limited"))
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for startsFor(dl, "limited") < 3 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	waitForStatus(t, s, job.ID, core.DownloadFailed)
	// Ten cooldowns: a fourth attempt would have started by now.
	time.Sleep(200 * time.Millisecond)
	if n, j := startsFor(dl, "limited"), mustGet(t, s, job.ID); n != 3 || j.Attempts != 2 || j.Status != core.DownloadFailed {
		t.Fatalf("starts=%d attempts=%d status=%s, want 3 starts, 2 attempts, failed", n, j.Attempts, j.Status)
	}
}

// A queued job canceled and retried is on the worker channel twice. The older
// dispatch must not run the job again once the retry has completed it.
func TestStaleDispatchDoesNotRerunAFinishedJob(t *testing.T) {
	ctx := context.Background()
	s := newSQLStore(t)
	dl := &fakeDL{name: "sync", canDownload: true}
	rec := &recorder{}
	m := lifecycleManager(t, s, rec, Config{}, dl)
	m.Pause()
	job, err := m.Enqueue(ctx, trackReq("twice"))
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Cancel(ctx, job.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Retry(ctx, job.ID, ""); err != nil {
		t.Fatal(err)
	}
	m.Resume()
	drain(t, m, s)
	if n := startsFor(dl, "twice"); n != 1 {
		t.Fatalf("downloaded %d times, want once", n)
	}
	if j := mustGet(t, s, job.ID); j.Status != core.DownloadCompleted || j.Attempts != 1 {
		t.Fatalf("job %+v, want completed on attempt 1", j)
	}
}

// gatedStore holds the first read of one job until released, then returns
// what it read, so a test can act between a worker reading a job and acting
// on what it read.
type gatedStore struct {
	JobStore
	mu      sync.Mutex
	id      string
	entered chan struct{}
	release chan struct{}
}

func (s *gatedStore) arm(id string) {
	s.mu.Lock()
	s.id, s.entered, s.release = id, make(chan struct{}), make(chan struct{})
	s.mu.Unlock()
}

func (s *gatedStore) Get(ctx context.Context, id string) (core.DownloadJob, bool, error) {
	s.mu.Lock()
	hold := s.id != "" && s.id == id
	var entered, release chan struct{}
	if hold {
		s.id = ""
		entered, release = s.entered, s.release
	}
	s.mu.Unlock()
	j, ok, err := s.JobStore.Get(ctx, id)
	if hold {
		// The caller gets what it read before the test acted.
		close(entered)
		<-release
	}
	return j, ok, err
}

func TestWorkerHoldingAStaleReadDoesNotStartACanceledJob(t *testing.T) {
	ctx := context.Background()
	s := &gatedStore{JobStore: newSQLStore(t)}
	dl := &fakeDL{name: "sync", canDownload: true}
	rec := &recorder{}
	m := lifecycleManager(t, s, rec, Config{}, dl)
	m.Pause()
	job, err := m.Enqueue(ctx, trackReq("held"))
	if err != nil {
		t.Fatal(err)
	}
	s.arm(job.ID)
	m.Resume()
	<-s.entered // the worker has the job id and is reading it
	if err := m.Cancel(ctx, job.ID); err != nil {
		t.Fatal(err)
	}
	close(s.release)
	drain(t, m, s)
	if n := startsFor(dl, "held"); n != 0 {
		t.Fatalf("a canceled job was downloaded %d time(s)", n)
	}
	if j := mustGet(t, s, job.ID); j.Status != core.DownloadCanceled {
		t.Fatalf("status %s, want canceled", j.Status)
	}
	published := rec.statuses(job.ID)
	if last := published[len(published)-1]; last != core.DownloadCanceled || slices.Contains(published, core.DownloadCompleted) {
		t.Fatalf("published %v, want it to end canceled", published)
	}
}

// heldSubmit is an asynchronous downloader whose Submit waits for the test.
type heldSubmit struct {
	*fakeAsyncDL
	entered chan struct{}
	release chan struct{}
	mu      sync.Mutex
	held    bool
	revoked []string
}

func newHeldSubmit(ref string) *heldSubmit {
	return &heldSubmit{fakeAsyncDL: &fakeAsyncDL{name: "async", submitRef: ref, status: AsyncStatus{State: core.DownloadRunning}},
		entered: make(chan struct{}), release: make(chan struct{})}
}

// Submit holds only the first submission; later ones answer at once.
func (d *heldSubmit) Submit(ctx context.Context, req core.DownloadRequest) (string, error) {
	d.mu.Lock()
	first := !d.held
	d.held = true
	d.mu.Unlock()
	if first {
		close(d.entered)
		<-d.release
	}
	return d.fakeAsyncDL.Submit(ctx, req)
}

func (d *heldSubmit) CancelAsync(ctx context.Context, ref string) error {
	d.mu.Lock()
	d.revoked = append(d.revoked, ref)
	d.mu.Unlock()
	return d.fakeAsyncDL.CancelAsync(ctx, ref)
}

func (d *heldSubmit) revokedRefs() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.revoked...)
}

// startHeldSubmit enqueues an album whose submission is held, and returns its
// job id once Submit has been entered.
func startHeldSubmit(t *testing.T, m *Manager, s JobStore, dl *heldSubmit) (string, chan struct{}) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, err := m.Enqueue(context.Background(), albumReq("held-album")); err != nil {
			t.Error(err)
		}
	}()
	<-dl.entered
	jobs, err := s.List(context.Background())
	if err != nil || len(jobs) != 1 {
		t.Fatalf("jobs %+v %v", jobs, err)
	}
	return jobs[0].ID, done
}

func TestLateSubmitDoesNotResurrectACanceledJob(t *testing.T) {
	ctx := context.Background()
	s := newSQLStore(t)
	dl := newHeldSubmit("lidarr-7")
	rec := &recorder{}
	m := lifecycleManager(t, s, rec, Config{}, dl)
	id, done := startHeldSubmit(t, m, s, dl)
	if err := m.Cancel(ctx, id); err != nil {
		t.Fatal(err)
	}
	close(dl.release)
	<-done
	m.reconcileOnce(ctx)
	if j := mustGet(t, s, id); j.Status != core.DownloadCanceled {
		t.Fatalf("status %s after a late submission, want canceled", j.Status)
	}
	if refs := dl.revokedRefs(); len(refs) != 1 || refs[0] != "lidarr-7" {
		t.Fatalf("revoked %v, want the late submission lidarr-7 abandoned", refs)
	}
	for _, st := range rec.statuses(id) {
		if st == core.DownloadRunning {
			t.Fatalf("published %v for a canceled job", rec.statuses(id))
		}
	}
}

func TestLateSubmitDoesNotRecreateAClearedJob(t *testing.T) {
	ctx := context.Background()
	s := newSQLStore(t)
	dl := newHeldSubmit("lidarr-8")
	rec := &recorder{}
	m := lifecycleManager(t, s, rec, Config{}, dl)
	id, done := startHeldSubmit(t, m, s, dl)
	if err := m.Cancel(ctx, id); err != nil {
		t.Fatal(err)
	}
	if err := m.Clear(ctx, id); err != nil {
		t.Fatal(err)
	}
	close(dl.release)
	<-done
	m.reconcileOnce(ctx)
	if _, ok, err := s.Get(ctx, id); ok || err != nil {
		t.Fatalf("cleared job is back: ok=%v err=%v", ok, err)
	}
	if refs := dl.revokedRefs(); len(refs) != 1 || refs[0] != "lidarr-8" {
		t.Fatalf("revoked %v, want the orphaned submission lidarr-8 abandoned", refs)
	}
	for _, st := range rec.statuses(id) {
		if st == core.DownloadRunning {
			t.Fatalf("published %v for a cleared job", rec.statuses(id))
		}
	}
}

// An automatic retry is scheduled for the attempt that failed. If the owner
// retries by hand first, the timer must not drive that later attempt.
func TestStaleAutomaticRetryDoesNotDriveALaterAttempt(t *testing.T) {
	ctx := context.Background()
	s := newSQLStore(t)
	dl := &fakeDL{name: "sync", canDownload: true, errOnStart: ClassifiedError{Class: ClassRateLimited, Err: errors.New("HTTP 429")}}
	m := lifecycleManager(t, s, nil, Config{MaxAutoRetries: 5, PacingCooldown: 100 * time.Millisecond, PacingThreshold: 100}, dl)
	job, err := m.Enqueue(ctx, trackReq("manual"))
	if err != nil {
		t.Fatal(err)
	}
	waitForStatus(t, s, job.ID, core.DownloadFailed)
	dl.setErrOnStart(errors.New("unavailable")) // not retried automatically
	if _, err := m.Retry(ctx, job.ID, ""); err != nil {
		t.Fatal(err)
	}
	waitForStatus(t, s, job.ID, core.DownloadFailed)
	time.Sleep(300 * time.Millisecond) // three cooldowns: the first attempt's timer has fired
	if n, j := startsFor(dl, "manual"), mustGet(t, s, job.ID); n != 2 || j.Attempts != 1 || j.Status != core.DownloadFailed {
		t.Fatalf("starts=%d attempts=%d status=%s, want the manual attempt only", n, j.Attempts, j.Status)
	}
}

func TestAutomaticRetryDoesNothingAfterStop(t *testing.T) {
	s := newSQLStore(t)
	dl := &fakeDL{name: "sync", canDownload: true, errOnStart: ClassifiedError{Class: ClassBotChallenge, Err: errors.New("sign in")}}
	m := lifecycleManager(t, s, nil, Config{MaxAutoRetries: 5, PacingCooldown: 50 * time.Millisecond, PacingThreshold: 100}, dl)
	job, err := m.Enqueue(context.Background(), trackReq("stopped"))
	if err != nil {
		t.Fatal(err)
	}
	waitForStatus(t, s, job.ID, core.DownloadFailed)
	m.Stop()
	time.Sleep(200 * time.Millisecond)
	if j := mustGet(t, s, job.ID); j.Status != core.DownloadFailed || j.Attempts != 0 {
		t.Fatalf("a stopped manager retried: %+v", j)
	}
}

// A terminal transition the store refused is not published: the job list
// would contradict it.
func TestUnpersistedFailureIsNotPublished(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	s := NewSQLStore(st.Q())
	if _, err := st.DB().Exec(`CREATE TRIGGER refuse_failed BEFORE UPDATE ON download_jobs WHEN NEW.status = 'failed' BEGIN SELECT RAISE(FAIL, 'job database unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	dl := &fakeDL{name: "sync", canDownload: true, errOnStart: errors.New("no match")}
	rec := &recorder{}
	m := lifecycleManager(t, s, rec, Config{}, dl)
	m.Pause()
	job, err := m.Enqueue(ctx, trackReq("unpersisted"))
	if err != nil {
		t.Fatal(err)
	}
	m.Resume()
	deadline := time.Now().Add(3 * time.Second)
	for startsFor(dl, "unpersisted") == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	dl.setErrOnStart(nil)
	drain(t, m, s)
	stored := mustGet(t, s, job.ID)
	for _, status := range rec.statuses(job.ID) {
		if status == core.DownloadFailed && stored.Status != core.DownloadFailed {
			t.Fatalf("published failed while the store holds %s", stored.Status)
		}
	}
}

// A retried asynchronous job gets its own age limit, not the first attempt's.
func TestRetriedAsyncJobIsTimedFromItsOwnStart(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	s := NewSQLStore(st.Q())
	dl := &fakeAsyncDL{name: "async", submitRef: "lidarr-9", status: AsyncStatus{State: core.DownloadRunning}}
	m := lifecycleManager(t, s, nil, Config{AsyncMaxAge: time.Minute}, dl)
	job, err := m.Enqueue(ctx, albumReq("aged"))
	if err != nil {
		t.Fatal(err)
	}
	dl.setStatus(AsyncStatus{State: core.DownloadFailed, Error: "release not found"})
	m.reconcileOnce(ctx)
	waitForStatus(t, s, job.ID, core.DownloadFailed)
	// The first attempt started an hour ago.
	if _, err := st.DB().Exec(`UPDATE download_jobs SET started_at = started_at - 3600 WHERE id = ?`, job.ID); err != nil {
		t.Fatal(err)
	}
	dl.setStatus(AsyncStatus{State: core.DownloadRunning})
	if _, err := m.Retry(ctx, job.ID, ""); err != nil {
		t.Fatal(err)
	}
	waitForStatus(t, s, job.ID, core.DownloadRunning)
	m.reconcileOnce(ctx)
	if j := mustGet(t, s, job.ID); j.Status != core.DownloadRunning {
		t.Fatalf("retried job %+v was timed out by its first attempt", j)
	}
}

// Lidarr's ref is the album, so a retry can be handed the same ref its stale
// predecessor gets back. Abandoning the stale submission must leave the
// retry's request alone.
func TestLateSubmitDoesNotAbandonARetryWithTheSameRef(t *testing.T) {
	ctx := context.Background()
	s := newSQLStore(t)
	dl := newHeldSubmit("album-5")
	m := lifecycleManager(t, s, nil, Config{}, dl)
	id, done := startHeldSubmit(t, m, s, dl)
	if err := m.Cancel(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Retry(ctx, id, ""); err != nil {
		t.Fatal(err)
	}
	waitForStatus(t, s, id, core.DownloadRunning)
	close(dl.release)
	<-done
	if refs := dl.revokedRefs(); len(refs) != 0 {
		t.Fatalf("revoked %v: the stale submission canceled the retry's album", refs)
	}
	if j := mustGet(t, s, id); j.Status != core.DownloadRunning || j.DownloaderRef != "album-5" || j.Attempts != 1 {
		t.Fatalf("retry %+v, want running attempt 1 on album-5", j)
	}
}

// heldPoll is an asynchronous downloader whose Poll waits for the test.
type heldPoll struct {
	*fakeAsyncDL
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (d *heldPoll) Poll(ctx context.Context, ref string) (AsyncStatus, error) {
	d.once.Do(func() { close(d.entered); <-d.release })
	return d.fakeAsyncDL.Poll(ctx, ref)
}

// A slow external poll holds up only the job it polls.
func TestSlowPollDoesNotHoldUpOtherJobs(t *testing.T) {
	ctx := context.Background()
	s := newSQLStore(t)
	async := &heldPoll{fakeAsyncDL: &fakeAsyncDL{name: "async", submitRef: "album-3", status: AsyncStatus{State: core.DownloadRunning}},
		entered: make(chan struct{}), release: make(chan struct{})}
	sync := &fakeDL{name: "sync", canDownload: true}
	m := lifecycleManager(t, s, nil, Config{}, async, sync)
	album, err := m.Enqueue(ctx, albumReq("slow"))
	if err != nil {
		t.Fatal(err)
	}
	polled := make(chan struct{})
	go func() { defer close(polled); m.reconcileOnce(ctx) }()
	<-async.entered
	defer func() { close(async.release); <-polled }()
	track, err := m.Enqueue(ctx, trackReq("meanwhile"))
	if err != nil {
		t.Fatal(err)
	}
	waitForStatus(t, s, track.ID, core.DownloadCompleted)
	if j := mustGet(t, s, album.ID); j.Status != core.DownloadRunning {
		t.Fatalf("album %+v", j)
	}
}
