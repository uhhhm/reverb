package download

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/uhhhm/reverb/internal/catalog"
	"github.com/uhhhm/reverb/internal/core"
	"github.com/uhhhm/reverb/internal/events"
	"github.com/uhhhm/reverb/internal/resolver"
)

// shortID trims a job UUID for compact log lines.
func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// closedChan returns an already-closed channel — receiving from it returns
// immediately. Used as the "running" (open-gate) state of the pause gate.
func closedChan() chan struct{} {
	c := make(chan struct{})
	close(c)
	return c
}

// JobStore is the persistence slice the Manager needs. *db.Queries does NOT
// satisfy this directly (it speaks db.DownloadJob); the composition root adapts
// it via a thin sqlStore wrapper (Task 7). The in-memory test store satisfies it.
type JobStore interface {
	// Insert persists a new job. The originating request is passed alongside so
	// the sqlStore can marshal the FULL core.DownloadRequest into request_json
	// (artist/title/album/source/externalId/isrc/playWhenReady/downloader),
	// giving a job loaded back from SQLite enough to run.
	Insert(ctx context.Context, j core.DownloadJob, req core.DownloadRequest) error
	Get(ctx context.Context, id string) (core.DownloadJob, bool, error)
	ActiveByDedup(ctx context.Context, dedupKey string) (core.DownloadJob, bool, error)
	// GetByDedup returns any job (active or terminal) with dedupKey.
	// Used as the terminal guard: a coverage page remains "missing" until a scan,
	// and repeated clicks must not create another terminal job for the same identity.
	GetByDedup(ctx context.Context, dedupKey string) (core.DownloadJob, bool, error)
	List(ctx context.Context) ([]core.DownloadJob, error)
	// Update persists the whole job row: status, progress, error, output,
	// completion phase, attempts, downloader and its ref, and timestamps. It
	// returns ErrJobNotFound when the row has been removed, and never recreates it.
	Update(ctx context.Context, j core.DownloadJob) error
	// UpdateProgress records a progress sample for one attempt, only while that
	// attempt is still running. It reports whether the sample was recorded, so
	// a late sample can never overwrite a later transition.
	UpdateProgress(ctx context.Context, id string, attempt, progress int) (bool, error)
	// UpdateRequest re-persists the originating DownloadRequest for job id into
	// request_json so that ManualURL (and any other late-added field) survives a
	// server restart between a Retry call and the worker picking up the job.
	UpdateRequest(ctx context.Context, id string, req core.DownloadRequest) error
	// Delete hard-removes a single finished (completed/failed/canceled) job row
	// and reports whether it did. A job that is active again, retried since the
	// caller read it, is kept.
	Delete(ctx context.Context, id string) (bool, error)
	// DeleteFinished hard-removes every terminal (completed/failed/canceled) job
	// and returns the deleted ids so the Manager can publish a removal event.
	DeleteFinished(ctx context.Context) ([]string, error)
	// GetRequest retrieves the originating DownloadRequest (from request_json) for
	// the given job id. Returns (req, true, nil) on hit, (zero, false, nil) if the
	// job exists but has no persisted request, and (zero, false, err) on error.
	// Used by the !haveReq reconstruction to recover Granularity after a restart.
	GetRequest(ctx context.Context, id string) (core.DownloadRequest, bool, error)
	// UpdateCanonicalID persists the minted catalog entity id on the job row.
	// Called at link time (BackfillUnlinked / runScan) after a successful match.
	UpdateCanonicalID(ctx context.Context, id string, canonicalID string) error
}

// ErrJobNotFound means the job row no longer exists: it was cleared.
var ErrJobNotFound = errors.New("download job not found")

// ScanController is the library slice the Manager needs (StartScan + ScanStatus).
type ScanController interface {
	StartScan(ctx context.Context) error
	ScanStatus(ctx context.Context) (core.ScanStatus, error)
}

// SynchronousScanner is a ScanController whose StartScan returns only once the
// scan has finished, so the Manager rematches straight away instead of waiting
// for a scan to begin. The localfiles library is one; Navidrome is not.
type SynchronousScanner interface {
	ScansSynchronously() bool
}

// Rematcher re-resolves an external result after a scan. *matching.Service fits.
type Rematcher interface {
	Match(ctx context.Context, ext core.ExternalResult) (core.MatchResult, error)
}

// Publisher is the EventBus slice the Manager needs. *events.Bus fits.
type Publisher interface {
	Publish(ev events.Event)
}

// BindingResolver is the narrow catalog-resolution seam the Manager will use in
// Tasks 3-5 to resolve catalog IDs to backend addressing. *resolver.Service
// satisfies this interface (Go structural typing). Declared here (consumer-side)
// so the resolver package never needs to import download, keeping the dependency
// direction one-way (download→resolver, not the reverse).
type BindingResolver interface {
	Resolve(ctx context.Context, catalogID string) (resolver.Addressing, error)
	RefreshLinked(ctx context.Context, catalogIDs []string) error
}

// CanonicalMinter mints or resolves a stable catalog entity id. *catalog.Service
// satisfies this interface. Declared here (consumer-side) so the catalog package
// never imports download (dependency direction: download→catalog, not the reverse).
// Nil-safe: callers guard with "if m.canonicalMinter != nil".
type CanonicalMinter interface {
	CanonicalFor(ctx context.Context, id catalog.Identity) (string, error)
}

// VersionBumper reads and bumps library_version. *store.Store fits.
type VersionBumper interface {
	LibraryVersion(ctx context.Context) (int64, error)
	SetLibraryVersion(ctx context.Context, v int64) error
}

// PlaylistAdder adds tracks to a library playlist. *subsonic.LibraryAdapter satisfies it.
// Used by the one-time import path: after a download is matched, the track is appended
// to the target playlist. May be nil when no library is configured.
type PlaylistAdder interface {
	AddTracksToPlaylist(ctx context.Context, playlistID string, trackIDs []string) error
}

// TrackEnricher fetches a durable source track by id. *search.Aggregator fits.
// Used at enqueue only, to recover an ISRC the search payload omitted; may be
// nil when no search source is configured.
type TrackEnricher interface {
	GetTrack(ctx context.Context, source, externalID string) (core.ExternalResult, error)
}

// enrichTimeout bounds the enqueue-time ISRC lookup. Enqueue is on the request
// path, and the lookup is an optimisation — a slow source must not hold up
// queueing the job.
const enrichTimeout = 5 * time.Second

// Config tunes the Manager. Zero values are replaced with safe defaults.
type Config struct {
	Workers        int
	DebounceWindow time.Duration
	ScanPollEvery  time.Duration
	ScanPollMax    time.Duration
	// ScanSettleMax bounds how long waitForScan waits for Navidrome to actually
	// BEGIN scanning (flip getScanStatus.scanning → true) after StartScan. startScan
	// is async: getScanStatus reports scanning=false for a brief window before the
	// scan engages. Without this grace the poll loop saw scanning=false on the first
	// tick and returned immediately — re-matching against the PRE-download index, so
	// the freshly-downloaded file was never found and library_track_id stayed empty
	// forever. A short settle window lets the scan engage before we wait for it to end.
	ScanSettleMax time.Duration
	// JobTimeout caps how long a single track-granularity download may run before
	// it is killed and marked failed — so a stuck/rate-limited downloader (e.g.
	// spotDL backing off for 24h) can't pin a worker forever.
	JobTimeout time.Duration
	// AlbumJobTimeout caps how long an album-granularity sync download may run.
	// Album downloads (e.g. spotDL fetching a full album) are expected to take
	// significantly longer than a single track, so they get a separate, larger
	// timeout. Async/Lidarr jobs run on the reconciler lane (bounded by AsyncMaxAge)
	// and are NOT affected by this setting.
	AlbumJobTimeout time.Duration
	// ReconcileEvery is the poll cadence for async (e.g. Lidarr) jobs.
	ReconcileEvery time.Duration
	// AsyncMaxAge bounds how long an async job may stay in-flight before it's
	// failed (Lidarr never found/imported a release).
	AsyncMaxAge time.Duration
	// PacingThreshold is how many CONSECUTIVE rate_limited/bot_challenge
	// failures from the same downloader trigger an automatic pause (see
	// PacingCooldown). Other failure classes (unavailable, no_match,
	// spotify_api_error, unknown) never count toward this — they aren't signs
	// of being rate-limited.
	PacingThreshold int
	// PacingCooldown is how long dispatch stays auto-paused after
	// PacingThreshold is reached, before automatically resuming.
	PacingCooldown time.Duration
	// MaxAutoRetries bounds how many times a job may be automatically retried
	// after a rate_limited/bot_challenge terminal failure before it's left
	// failed for manual intervention. Counts against the same Attempts field a
	// manual Retry() increments.
	MaxAutoRetries int
}

func (c Config) withDefaults() Config {
	if c.Workers <= 0 {
		c.Workers = 2
	}
	if c.DebounceWindow <= 0 {
		c.DebounceWindow = 5 * time.Second
	}
	if c.ScanPollEvery <= 0 {
		c.ScanPollEvery = 500 * time.Millisecond
	}
	if c.ScanPollMax <= 0 {
		c.ScanPollMax = 30 * time.Second
	}
	if c.ScanSettleMax <= 0 {
		c.ScanSettleMax = 5 * time.Second
	}
	if c.JobTimeout <= 0 {
		c.JobTimeout = 15 * time.Minute
	}
	if c.AlbumJobTimeout <= 0 {
		c.AlbumJobTimeout = 2 * time.Hour
	}
	if c.ReconcileEvery <= 0 {
		c.ReconcileEvery = 10 * time.Second
	}
	if c.AsyncMaxAge <= 0 {
		c.AsyncMaxAge = 7 * 24 * time.Hour
	}
	if c.PacingThreshold <= 0 {
		c.PacingThreshold = 3
	}
	if c.PacingCooldown <= 0 {
		c.PacingCooldown = 5 * time.Minute
	}
	if c.MaxAutoRetries <= 0 {
		c.MaxAutoRetries = 5
	}
	return c
}

// Manager owns the download queue, a bounded worker pool, dedup-join, the
// fallback chain, scan-debounce, cancel/retry, and EventBus publication.
type Manager struct {
	cfg             Config
	downloaders     []DownloaderEntry
	store           JobStore
	bus             Publisher
	scanner         ScanController
	rematcher       Rematcher
	version         VersionBumper
	clock           Clock
	playlists       PlaylistAdder                                             // optional; non-nil only when a library is configured
	resolve         func() BindingResolver                                    // optional provider; Tasks 3-5 add call sites
	canonicalMinter CanonicalMinter                                           // optional; mints catalog IDs at link time (Task 3)
	qualityFn       func(context.Context) core.AudioQuality                   // optional; supplies the configured default tier
	trackEnricher   TrackEnricher                                             // optional; recovers a missing ISRC at enqueue
	completionHook  func(context.Context, core.DownloadRequest, string) error // optional; validates first successful completion
	transitionMu    sync.Mutex                                                // serializes every lifecycle transition; see lifecycle.go
	linkedHook      func(context.Context, string)                             // optional; observes a job linked to its library track

	queue chan string // job IDs to process

	mu              sync.Mutex
	cancels         map[string]context.CancelFunc // in-flight job cancel funcs
	reqs            map[string]core.DownloadRequest
	unrecorded      map[string]core.DownloadJob // output whose completion row could not be persisted yet
	polls           map[string]chan struct{}    // async jobs whose poll is in flight; closed when it is applied
	consecutiveFail map[string]int              // downloader name -> consecutive rate/bot-challenge failures
	debounce        func() bool                 // active debounce timer stop (or nil)
	pending         bool                        // a completion is awaiting the scan window
	paused          bool                        // dispatch gate: workers stop pulling NEW jobs while true
	resumeCh        chan struct{}               // closed when running; a fresh OPEN channel while paused

	wg       sync.WaitGroup
	stopOnce sync.Once
	stopCh   chan struct{}
	stopping bool // set by Stop() before it cancels: a cancel from here is a shutdown, not the user
	started  bool // set to true by Start(); guards Stop() against double-close on an unstarted Manager
}

// jobTimeout returns the per-job context timeout for the given request.
// Album-granularity sync jobs use AlbumJobTimeout (default 2h) so that a full
// album download is not prematurely killed by the much-shorter track JobTimeout.
// All other (track / empty) granularities use JobTimeout.
func (m *Manager) jobTimeout(req core.DownloadRequest) time.Duration {
	if req.Granularity == core.GranularityAlbum {
		return m.cfg.AlbumJobTimeout
	}
	return m.cfg.JobTimeout
}

// NewManager constructs the Manager. Call Start() to launch workers.
// playlists may be nil; when non-nil, completed downloads whose request carries
// AddToPlaylistID will have the matched library track appended to that playlist.
// resolve is an optional provider func() BindingResolver — nil or returning nil
// means "no resolver available yet" (no panic). Tasks 3-5 add the actual Resolve
// and RefreshLinked call sites; this Task (1) only stores the dep.
func NewManager(cfg Config, downloaders []DownloaderEntry, store JobStore, bus Publisher,
	scanner ScanController, rematcher Rematcher, version VersionBumper, clock Clock,
	playlists PlaylistAdder, resolve func() BindingResolver) *Manager {
	if clock == nil {
		clock = RealClock{}
	}
	cfg = cfg.withDefaults()
	return &Manager{
		cfg:             cfg,
		downloaders:     downloaders,
		store:           store,
		bus:             bus,
		scanner:         scanner,
		rematcher:       rematcher,
		version:         version,
		clock:           clock,
		playlists:       playlists,
		resolve:         resolve,
		queue:           make(chan string, 256),
		cancels:         map[string]context.CancelFunc{},
		reqs:            map[string]core.DownloadRequest{},
		unrecorded:      map[string]core.DownloadJob{},
		polls:           map[string]chan struct{}{},
		consecutiveFail: map[string]int{},
		stopCh:          make(chan struct{}),
		resumeCh:        closedChan(),
	}
}

// SetQualityResolver injects the source of the configured default audio quality
// (the download_quality setting), applied to any request that does not name a
// tier of its own. Nil-safe: without it, requests fall back to
// core.DefaultAudioQuality.
func (m *Manager) SetQualityResolver(fn func(context.Context) core.AudioQuality) {
	m.qualityFn = fn
}

// resolveQuality fills in an unset tier from the configured default.
func (m *Manager) resolveQuality(ctx context.Context, q core.AudioQuality) core.AudioQuality {
	if q.Valid() {
		return q
	}
	if m.qualityFn != nil {
		if got := m.qualityFn(ctx); got.Valid() {
			return got
		}
	}
	return core.DefaultAudioQuality
}

// SetCanonicalMinter injects the catalog minter after construction. Called by the
// composition root (cmd/reverb/main.go) so the singleton catalogSvc is available
// before Build runs. Nil-safe: if never called, minting is silently skipped.
func (m *Manager) SetCanonicalMinter(minter CanonicalMinter) {
	m.canonicalMinter = minter
}

// SetTrackEnricher injects the search-side track lookup after construction (the
// aggregator is built alongside the manager at the composition root). Nil-safe:
// if never called, enrichment is silently skipped.
func (m *Manager) SetTrackEnricher(e TrackEnricher) {
	m.trackEnricher = e
}

// SetCompletionHook installs a completion gate. The persisted request is
// supplied so attribution survives a manager restart, with the downloader's
// output: the file it wrote, or the directory when it names none, or "" from an
// async downloader. Returning an error keeps the job from being reported
// complete; this lets a device make a required durable record of the output
// before completion is published.
func (m *Manager) SetCompletionHook(fn func(context.Context, core.DownloadRequest, string) error) {
	m.completionHook = fn
}

// SetLinkedHook installs an observer called with the catalog id of each
// completed job once the post-download scan links it to a library track, the
// moment that track is known to be in the library.
func (m *Manager) SetLinkedHook(fn func(context.Context, string)) {
	m.linkedHook = fn
}

func (m *Manager) notifyCompletion(ctx context.Context, req core.DownloadRequest, outputPath string) error {
	if m.completionHook != nil {
		return m.completionHook(ctx, req, outputPath)
	}
	return nil
}

// deferCompletion keeps the output attached to an active job. A failed
// pending-upload write must not turn the downloaded bytes into an untracked
// terminal job that ClearFinished can remove before the write is retried.
func (m *Manager) deferCompletion(ctx context.Context, job core.DownloadJob, cause error) {
	job.CompletionPending = true
	job.Status = core.DownloadRunning
	job.Error = cause.Error()
	m.mu.Lock()
	m.unrecorded[job.ID] = job
	m.mu.Unlock()
	if err := m.store.Update(ctx, job); err != nil {
		log.Printf("download completion: retain output for job %s: %v", shortID(job.ID), err)
	}
	m.publishEvent(TopicProgress, job, job.Error)
}

// completeOutput takes ownership of bytes before invoking any completion hook.
// Memory retains that ownership during a store outage; a successful pending-row
// write makes it recoverable across restart. Only retryCompletion publishes.
func (m *Manager) completeOutput(ctx context.Context, job core.DownloadJob) (core.DownloadJob, error) {
	m.transitionMu.Lock()
	defer m.transitionMu.Unlock()
	return m.completeOutputLocked(ctx, job)
}

// completeOutputLocked is the handoff used when async polling already owns the
// transition lock. No cancellation can interleave the poll and this handoff.
func (m *Manager) completeOutputLocked(ctx context.Context, job core.DownloadJob) (core.DownloadJob, error) {
	job.Status = core.DownloadRunning
	job.CompletionPending = true
	m.mu.Lock()
	m.unrecorded[job.ID] = job
	m.mu.Unlock()
	return m.retryCompletionLocked(ctx, job)
}

// retryCompletion records an existing output without starting its downloader
// again. The hook may have succeeded while the following job update failed, so
// it must be safe to call more than once.
func (m *Manager) retryCompletion(ctx context.Context, job core.DownloadJob) (core.DownloadJob, error) {
	m.transitionMu.Lock()
	defer m.transitionMu.Unlock()
	return m.retryCompletionLocked(ctx, job)
}

func (m *Manager) retryCompletionLocked(ctx context.Context, job core.DownloadJob) (core.DownloadJob, error) {
	current, ok, err := m.store.Get(ctx, job.ID)
	if err != nil {
		return job, err
	}
	if !ok || current.Status == core.DownloadCompleted {
		m.mu.Lock()
		delete(m.unrecorded, job.ID)
		m.mu.Unlock()
		return current, nil
	}
	m.mu.Lock()
	if pending, exists := m.unrecorded[job.ID]; exists {
		job = pending
	} else {
		job = current
	}
	m.mu.Unlock()
	if job.Status != core.DownloadRunning || !job.CompletionPending {
		return current, nil
	}
	// Persist the output path before the hook. If the store is temporarily
	// unavailable, unrecorded retains it for another attempt in this process.
	if err := m.store.Update(ctx, job); err != nil {
		m.deferCompletion(ctx, job, err)
		return job, err
	}
	req, err := m.loadRequest(ctx, job)
	if err != nil {
		m.deferCompletion(ctx, job, err)
		return job, err
	}
	req.CompletionID = job.ID
	if err := m.notifyCompletion(ctx, req, job.OutputPath); err != nil {
		m.deferCompletion(ctx, job, err)
		return job, err
	}
	pendingJob := job
	job.Status = core.DownloadCompleted
	job.CompletionPending = false
	job.Progress = 100
	job.Error = ""
	job.FinishedAt = m.clock.Now().Unix()
	if err := m.store.Update(ctx, job); err != nil {
		job = pendingJob
		m.deferCompletion(ctx, job, err)
		return job, err
	}
	m.mu.Lock()
	delete(m.unrecorded, job.ID)
	delete(m.reqs, job.ID)
	m.mu.Unlock()
	m.publishEvent(TopicComplete, job, "")
	m.scheduleScan(job.ID)
	return job, nil
}

func (m *Manager) joinExisting(ctx context.Context, job core.DownloadJob) (core.DownloadJob, error) {
	m.mu.Lock()
	_, inMemory := m.unrecorded[job.ID]
	m.mu.Unlock()
	if inMemory || (job.Status == core.DownloadRunning && job.CompletionPending) {
		return m.retryCompletion(ctx, job)
	}
	return job, nil
}

func (m *Manager) retryIncompleteCompletions() {
	ctx := context.Background()
	jobs := map[string]core.DownloadJob{}
	if stored, err := m.store.List(ctx); err == nil {
		for _, job := range stored {
			if job.Status == core.DownloadRunning && job.CompletionPending {
				jobs[job.ID] = job
			}
		}
	}
	m.mu.Lock()
	for id, job := range m.unrecorded {
		jobs[id] = job
	}
	m.mu.Unlock()
	for _, job := range jobs {
		select {
		case <-m.stopCh:
			return
		default:
		}
		if _, err := m.retryCompletion(ctx, job); err != nil {
			log.Printf("download completion: retry job %s: %v", shortID(job.ID), err)
		}
	}
}

func (m *Manager) completionLoop() {
	defer m.wg.Done()
	m.retryIncompleteCompletions()
	ticker := time.NewTicker(m.cfg.ReconcileEvery)
	defer ticker.Stop()
	for {
		select {
		case <-m.stopCh:
			return
		case <-ticker.C:
			m.retryIncompleteCompletions()
		}
	}
}

// enrichISRC fills in an ISRC the search payload omitted — Deezer returns one
// from /track/{id} but never from /search/track, so every Deezer download would
// otherwise reach spotDL as a fuzzy text query (~28s of guessing that an "isrc:"
// lookup does exactly). The ISRC also lands on the job row, where the
// post-download rematcher uses it instead of fuzzy metadata.
//
// Best-effort throughout: a nil enricher, an error, or a source with no ISRC all
// leave req unchanged rather than failing the enqueue.
func (m *Manager) enrichISRC(ctx context.Context, req core.DownloadRequest) core.DownloadRequest {
	if m.trackEnricher == nil || req.ISRC != "" || req.Source == "" || req.ExternalID == "" {
		return req
	}
	lookupCtx, cancel := context.WithTimeout(ctx, enrichTimeout)
	defer cancel()
	res, err := m.trackEnricher.GetTrack(lookupCtx, req.Source, req.ExternalID)
	if err != nil {
		log.Printf("download: ISRC lookup for %s:%s failed (continuing without): %v", req.Source, req.ExternalID, err)
		return req
	}
	req.ISRC = res.ISRC
	return req
}

// recordingOf describes the recording job j produced, for matching it to the
// library and minting its catalog entity. A trimmed job (a time range, or one
// chapter of a split) is a different recording from its source, so it drops
// the source's external address and ISRC: both name the whole source, and
// matching or minting by them would fold every section into one match-cache
// row and one catalog entity. The section is told apart by its metadata instead,
// with its own length as the duration. An open-ended trim of a source whose
// length is unknown keeps the job's duration.
func (m *Manager) recordingOf(ctx context.Context, j core.DownloadJob) core.ExternalResult {
	rec := core.ExternalResult{
		Source: j.Source, ExternalID: j.ExternalID, Type: core.EntityTrack,
		Title: j.Title, Artist: j.Artist, Album: j.Album, ISRC: j.ISRC,
		DurationMs: j.DurationMs,
	}
	req := m.requestForJob(ctx, j)
	if !sectioned(req) {
		return rec
	}
	rec.Source, rec.ExternalID, rec.ISRC = "", "", ""
	if ms := sectionDurationMs(req, j.DurationMs); ms > 0 {
		rec.DurationMs = ms
	}
	return rec
}

// mintAndStoreCanonicalID mints (or resolves) a catalog entity id for job j and
// stores it on the row. Called ONLY at link time (linkJob) for
// jobs that are newly matched — never for archived, unlinked, or already-minted jobs.
// Nil-safe: a nil canonicalMinter silently skips minting (no panic, no error).
// Returns the minted canonical id, or "" if none (nil minter, minter error, or empty id).
func (m *Manager) mintAndStoreCanonicalID(ctx context.Context, j core.DownloadJob) string {
	if m.canonicalMinter == nil {
		return ""
	}
	rec := m.recordingOf(ctx, j)
	id := catalog.Identity{
		Kind:       "track",
		Source:     rec.Source,
		ExternalID: rec.ExternalID,
		ISRC:       rec.ISRC,
		Title:      rec.Title,
		Artist:     rec.Artist,
		Album:      rec.Album,
		DurationMs: rec.DurationMs,
	}
	cid, err := m.canonicalMinter.CanonicalFor(ctx, id)
	if err != nil {
		log.Printf("download: mint canonical id for job %s failed: %v", shortID(j.ID), err)
		return ""
	}
	if cid == "" {
		return ""
	}
	if err := m.store.UpdateCanonicalID(ctx, j.ID, cid); err != nil {
		log.Printf("download: store canonical id for job %s failed: %v", shortID(j.ID), err)
	}
	return cid
}

// Start launches the worker pool and kicks off a one-shot startup backfill (in a
// goroutine) that re-matches any completed job whose LibraryTrackID is still empty.
// This handles jobs that finished under an older/weaker matcher before the post-scan
// rematch path was in place, or whose scan-window closed before the file was indexed.
// The backfill runs once at startup and never retries still-unmatchable jobs.
func (m *Manager) Start() {
	m.mu.Lock()
	m.started = true
	m.mu.Unlock()
	for i := 0; i < m.cfg.Workers; i++ {
		m.wg.Add(1)
		go m.worker()
	}
	log.Printf("download manager: %d worker(s) started, %d downloader(s) available", m.cfg.Workers, len(m.downloaders))
	// A sync downloader process cannot survive a Reverb restart. Without this
	// recovery pass, its persisted "running" row has no process or cancel func
	// behind it forever, and persisted queued rows are never put back on the
	// in-memory worker channel. Repair both states before accepting new work.
	m.recoverAfterRestart()
	if m.completionHook != nil {
		m.wg.Add(1)
		go m.completionLoop()
	}
	if m.hasAsync() {
		m.wg.Add(1)
		go m.reconcileLoop()
		log.Printf("download manager: async reconciler started (every %s)", m.cfg.ReconcileEvery)
	}
	go m.BackfillUnlinked()
	// Converge legacy completed+linked jobs (canonical_id=='') onto the canonical
	// path so retiring the clear-dance doesn't rot their covers on a backend swap.
	go m.BackfillCanonicalIDs()
}

// recoverAfterRestart repairs jobs that were in flight when this process stopped
// and re-dispatches jobs that were queued in SQLite. Async jobs with a downloader
// ref are deliberately left running: their external downloader can still be
// reconciled after Reverb restarts. A synchronous job has no such durable process,
// so it is failed rather than falsely left "running" forever.
func (m *Manager) recoverAfterRestart() {
	ctx := context.Background()
	jobs, err := m.store.List(ctx)
	if err != nil {
		log.Printf("download recovery: list jobs failed: %v", err)
		return
	}
	for _, job := range jobs {
		switch job.Status {
		case core.DownloadRunning:
			if job.CompletionPending {
				continue // output exists; completionLoop retries its durable record
			}
			if job.DownloaderRef != "" {
				continue // async job; reconcileLoop resumes observing it
			}
			if _, ok := m.fail(ctx, job.ID, job.Attempts, "interrupted by restart"); ok {
				log.Printf("download recovery: marked interrupted job %s as failed", shortID(job.ID))
			}
		case core.DownloadQueued:
			if async := m.asyncFor(job.DownloaderName); async != nil {
				req := m.requestForJob(ctx, job)
				go m.submitAsync(ctx, job, req, async)
				continue
			}
			select {
			case m.queue <- job.ID:
				log.Printf("download recovery: re-dispatched queued job %s", shortID(job.ID))
			case <-m.stopCh:
				return
			}
		}
	}
}

// BackfillUnlinked is a one-shot pass that re-matches every completed job whose
// LibraryTrackID is empty. A job that still can't be matched is left alone (no
// retry loop). Jobs that now match get LibraryTrackID + CoverArtID set and a
// download.complete event published so the FE updates live.
//
// Called automatically at Start() and also by waitReadyThenBackfill (cmd/reverb)
// after the bundled Navidrome reports ready, so the boot-race case (backfill ran
// before Navidrome was serving) is re-resolved.
func (m *Manager) BackfillUnlinked() {
	if m.rematcher == nil {
		return
	}
	ctx := context.Background()
	jobs, err := m.store.List(ctx)
	if err != nil {
		log.Printf("download backfill: list jobs failed: %v", err)
		return
	}
	matched := 0
	for _, j := range jobs {
		if j.Status != core.DownloadCompleted || j.LibraryTrackID != "" {
			continue
		}
		res, merr := m.rematcher.Match(ctx, m.recordingOf(ctx, j))
		if merr != nil || res.Status != core.MatchInLibrary {
			continue
		}
		if _, ok := m.linkJob(ctx, j, res); !ok {
			continue
		}
		matched++
		log.Printf("download backfill: re-linked job %s -> library track %s", shortID(j.ID), res.LibraryTrackID)
	}
	if matched > 0 {
		log.Printf("download backfill: re-linked %d previously unmatched completed job(s)", matched)
	}
}

// linkJob records that completed job j is the library track res matched, and
// carries out everything that follows from the link. Both linking triggers —
// the startup backfill and the post-download scan — go through here.
//
// A link that cannot be persisted has no consequences: the job stays unlinked,
// so the next trigger retries it rather than announcing it a second time. Once
// persisted, the canonical id is minted, the job joins its target playlist,
// and only then is the completion published, so whoever hears it finds the
// track already in its playlist and can address it canonically. It reports
// the minted canonical id ("" when none) and whether the link was persisted.
func (m *Manager) linkJob(ctx context.Context, j core.DownloadJob, res core.MatchResult) (string, bool) {
	j.LibraryTrackID = res.LibraryTrackID
	j.CoverArtID = res.CoverArtID
	if err := m.store.Update(ctx, j); err != nil {
		log.Printf("download: link job %s to library track %s failed: %v", shortID(j.ID), res.LibraryTrackID, err)
		return "", false
	}
	// Mint only at link time, for newly linked jobs: never for archived jobs,
	// never on a browse.
	cid := m.mintAndStoreCanonicalID(ctx, j)
	if cid != "" {
		j.CanonicalID = cid
		if m.linkedHook != nil {
			m.linkedHook(ctx, cid)
		}
	}
	// AddToPlaylistID is carried on the job (mirrored from request_json by
	// toCoreFlatRow / Enqueue), so no extra store read is needed.
	if m.playlists != nil && j.AddToPlaylistID != "" {
		if err := m.playlists.AddTracksToPlaylist(ctx, j.AddToPlaylistID, []string{res.LibraryTrackID}); err != nil {
			log.Printf("download: add track %s to playlist %s failed for job %s: %v", res.LibraryTrackID, j.AddToPlaylistID, shortID(j.ID), err)
		}
	}
	m.publishComplete(j, res.LibraryTrackID)
	return cid, true
}

// BackfillCanonicalIDs is a one-shot, bounded, idempotent pass that mints a stable
// canonical_id for every completed+LINKED job that predates Task 3 (library_track_id
// set, canonical_id still empty). It closes the cover-rot hole left by retiring the
// clear-dance: BackfillUnlinked/runScan only mint jobs they NEWLY link (they gate on
// library_track_id==""), so a job linked BEFORE Task 3 would otherwise never get a
// canonical_id and — with the dance gone — fall back to a stale raw cover on a swap.
//
// Scope: ONLY status==completed && library_track_id != "" && canonical_id == "".
// Once a row is minted it no longer matches, so a second run is a no-op (idempotent).
// Never touches unlinked, archived (failed/canceled), or already-minted rows.
// Nil-minter-safe: mintAndStoreCanonicalID skips silently when no minter is wired.
// Fired on boot alongside BackfillUnlinked.
func (m *Manager) BackfillCanonicalIDs() {
	if m.canonicalMinter == nil {
		return // nothing to mint into; nil-safe no-op
	}
	ctx := context.Background()
	jobs, err := m.store.List(ctx)
	if err != nil {
		log.Printf("download canonical backfill: list jobs failed: %v", err)
		return
	}
	minted := 0
	for _, j := range jobs {
		if j.Status != core.DownloadCompleted || j.LibraryTrackID == "" || j.CanonicalID != "" {
			continue
		}
		_ = m.mintAndStoreCanonicalID(ctx, j)
		minted++
	}
	if minted > 0 {
		log.Printf("download canonical backfill: minted canonical_id for %d legacy linked job(s)", minted)
	}
}

// stopGrace bounds how long Stop waits for cancelled workers to unwind.
const stopGrace = 10 * time.Second

// isStopping reports whether Stop has begun, so a cancelled job can tell a
// shutdown from a user cancel.
func (m *Manager) isStopping() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.stopping
}

// Stop signals workers to drain, aborts whatever is in flight, and waits for
// them. It ALSO cancels any pending scan-debounce timer (and clears pending) so
// a real-clock test cannot have runScan fire against fakes after the test ends.
// Idempotent.
//
// Cancelling the in-flight jobs is what makes Stop finish: a worker only checks
// stopCh between jobs, so waiting alone blocks until the running job's own
// timeout — 15 minutes for a track, 2 hours for an album. That is a desktop quit
// that hangs with the single-instance lock still held, and an adapter save that
// hangs the HTTP request behind it. A job cancelled this way is persisted as
// queued rather than canceled (see process), so the next manager re-dispatches it.
//
// Ordering rationale: close stopCh first so workers exit their select loops and
// cancel in flight work, then wg.Wait() until every worker (and any scheduleScan
// it calls) has fully finished, then cancel the debounce timer. This guarantees
// we cancel the LAST timer armed by any worker — if we cancelled before Wait, a
// worker still in process() could call scheduleScan() and re-arm a new timer
// after we cleared it. No deadlock risk: wg.Wait() holds no lock, and workers
// only acquire m.mu briefly inside callbacks (never blocking on Stop's lock).
//
// The wait is bounded: a downloader that ignores its context must not be able to
// hold a quit open forever. Past the grace period we log and move on, and a
// straggler's later store writes fail harmlessly against the closing DB.
func (m *Manager) Stop() {
	m.mu.Lock()
	started := m.started
	m.mu.Unlock()
	if !started {
		return // Start() was never called — no workers, no channel to drain
	}
	m.stopOnce.Do(func() { close(m.stopCh) })
	m.mu.Lock()
	m.stopping = true
	cancels := make([]context.CancelFunc, 0, len(m.cancels))
	for _, c := range m.cancels {
		cancels = append(cancels, c)
	}
	m.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
	done := make(chan struct{})
	go func() {
		m.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(stopGrace):
		log.Printf("download manager: %d worker(s) still running after %s; continuing shutdown", m.cfg.Workers, stopGrace)
	}
	m.mu.Lock()
	if m.debounce != nil {
		m.debounce() // stop the AfterFunc/clock timer
		m.debounce = nil
	}
	m.pending = false
	m.mu.Unlock()
}

// RedispatchQueued puts every persisted queued job back on the worker channel.
//
// It exists for the reload path: the outgoing Manager requeues whatever it was
// running when it was stopped, but by then the incoming Manager has already run
// its own recovery pass, so without this those jobs sit queued until the next
// boot. Dispatching a job that is already in flight is harmless — process()
// drops it.
func (m *Manager) RedispatchQueued() {
	ctx := context.Background()
	jobs, err := m.store.List(ctx)
	if err != nil {
		log.Printf("download redispatch: list jobs failed: %v", err)
		return
	}
	for _, job := range jobs {
		if job.Status != core.DownloadQueued {
			continue
		}
		select {
		case m.queue <- job.ID:
			log.Printf("download redispatch: re-dispatched queued job %s", shortID(job.ID))
		case <-m.stopCh:
			return
		default:
			return // queue full; recovery on the next boot picks the rest up
		}
	}
}

// asyncFor returns the AsyncDownloader registered under name, or nil if that
// downloader isn't registered or isn't async.
func (m *Manager) asyncFor(name string) AsyncDownloader {
	for _, e := range m.downloaders {
		if e.Downloader.Name() == name {
			if a, ok := e.Downloader.(AsyncDownloader); ok {
				return a
			}
		}
	}
	return nil
}

// hasAsync reports whether any configured downloader is an AsyncDownloader.
func (m *Manager) hasAsync() bool {
	for _, e := range m.downloaders {
		if _, ok := e.Downloader.(AsyncDownloader); ok {
			return true
		}
	}
	return false
}

// reconcileOnce polls every in-flight async job (running, with a ref) once: it
// updates progress, completes (and schedules the scan) on import, fails on error,
// and gives up on jobs older than AsyncMaxAge. Safe to call from a test.
func (m *Manager) reconcileOnce(ctx context.Context) {
	jobs, err := m.store.List(ctx)
	if err != nil {
		return
	}
	for _, j := range jobs {
		if j.Status == core.DownloadRunning && j.DownloaderRef != "" {
			m.reconcileJob(ctx, j.ID)
		}
	}
}

// reconcileJob polls one async job and applies the answer as a transition.
// The poll itself runs outside transitionMu, so a slow external downloader
// never holds up other jobs' transitions; a Cancel of this job waits for the
// poll instead (see cancelStoredJob), so it cannot commit a stale terminal row
// while a completed output is being confirmed, which would let ClearFinished
// remove its recovery record.
func (m *Manager) reconcileJob(ctx context.Context, id string) {
	j, ok, err := m.store.Get(ctx, id)
	if err != nil || !ok {
		return
	}
	if j.Status != core.DownloadRunning || j.DownloaderRef == "" {
		return
	}
	m.mu.Lock()
	_, pending := m.unrecorded[j.ID]
	m.mu.Unlock()
	if pending || j.CompletionPending {
		return // completionLoop owns this output
	}
	async := m.asyncFor(j.DownloaderName)
	if async == nil {
		return
	}
	done := m.beginPoll(id)
	defer done()
	// The poll answers for this ref; another submission makes it stale.
	sameSubmission := func(cur core.DownloadJob) bool {
		return runningAttempt(cur, j.Attempts) && cur.DownloaderRef == j.DownloaderRef
	}
	end := func(reason string) {
		_, _, err := m.transition(ctx, id, func(cur *core.DownloadJob) (publication, bool) {
			if !sameSubmission(*cur) {
				return publication{}, false
			}
			cur.Status, cur.Error = core.DownloadFailed, reason
			return publication{TopicFailed, reason}, true
		})
		m.logTransitionError(id, "fail async", err)
	}
	now := m.clock.Now().Unix()
	if j.StartedAt > 0 && m.cfg.AsyncMaxAge > 0 && now-j.StartedAt > int64(m.cfg.AsyncMaxAge.Seconds()) {
		end("timed out waiting for the downloader to finish")
		return
	}
	st, perr := async.Poll(ctx, j.DownloaderRef)
	if perr != nil {
		return // transient — retry next tick
	}
	switch st.State {
	case core.DownloadCompleted:
		// Async output belongs to its durable external reference.
		m.transitionMu.Lock()
		defer m.transitionMu.Unlock()
		cur, found, err := m.store.Get(ctx, id)
		if err != nil || !found || !sameSubmission(cur) {
			return
		}
		if _, err := m.completeOutputLocked(ctx, cur); err != nil {
			log.Printf("download completion: async job %s: %v", shortID(j.ID), err)
		}
	case core.DownloadFailed:
		end(st.Error)
	default: // still running — publish progress changes
		_, _, err := m.transition(ctx, id, func(cur *core.DownloadJob) (publication, bool) {
			if !sameSubmission(*cur) || cur.Progress == st.Progress {
				return publication{}, false
			}
			cur.Progress = st.Progress
			return publication{topic: TopicProgress}, true
		})
		m.logTransitionError(id, "async progress", err)
	}
}

// beginPoll marks a poll of id in flight until the returned func is called.
func (m *Manager) beginPoll(id string) (done func()) {
	ch := make(chan struct{})
	m.mu.Lock()
	m.polls[id] = ch
	m.mu.Unlock()
	return func() {
		m.mu.Lock()
		if m.polls[id] == ch {
			delete(m.polls, id)
		}
		m.mu.Unlock()
		close(ch)
	}
}

// reconcileLoop ticks reconcileOnce until Stop. Launched by Start only when an
// async downloader is configured.
func (m *Manager) reconcileLoop() {
	defer m.wg.Done()
	t := time.NewTicker(m.cfg.ReconcileEvery)
	defer t.Stop()
	for {
		select {
		case <-m.stopCh:
			return
		case <-t.C:
			m.reconcileOnce(context.Background())
		}
	}
}

// submitAsync hands a queued attempt to an async downloader. On success it
// records the ref and flips the job to running (progress -1 = searching); the
// reconciler then advances it. On error it fails the job. Runs outside m.mu.
//
// Submit is a network call, and the job may be canceled, retried or cleared
// while it runs. The result then belongs to no current attempt: the external
// request it placed is abandoned rather than left running unobserved.
func (m *Manager) submitAsync(ctx context.Context, job core.DownloadJob, req core.DownloadRequest, async AsyncDownloader) {
	attempt := job.Attempts
	ref, err := async.Submit(ctx, req)
	queuedAttempt := func(j core.DownloadJob) bool { return j.Status == core.DownloadQueued && j.Attempts == attempt }
	if err != nil {
		reason := err.Error()
		_, failed, terr := m.transition(ctx, job.ID, func(j *core.DownloadJob) (publication, bool) {
			if !queuedAttempt(*j) {
				return publication{}, false
			}
			j.Status, j.Error = core.DownloadFailed, reason
			return publication{TopicFailed, reason}, true
		})
		m.logTransitionError(job.ID, "fail submission", terr)
		if failed {
			log.Printf("download submit failed: %q via %s — %v", job.Title, job.DownloaderName, err)
		}
		return
	}
	cur, submitted, terr := m.transition(ctx, job.ID, func(j *core.DownloadJob) (publication, bool) {
		if !queuedAttempt(*j) {
			return publication{}, false
		}
		j.Status, j.DownloaderRef, j.Progress, j.Error, j.StartedAt = core.DownloadRunning, ref, -1, "", 0
		return publication{topic: TopicProgress}, true
	})
	m.logTransitionError(job.ID, "record submission", terr)
	if !submitted {
		m.abandonSubmission(ctx, job, ref, async)
		return
	}
	log.Printf("download submitted to %s: %q (job %s, ref %s)", job.DownloaderName, cur.Title, shortID(cur.ID), ref)
}

// abandonSubmission cancels an external request no current attempt owns. A
// downloader may hand a later attempt the same ref (Lidarr's is the album), and
// that attempt's request is left alone.
func (m *Manager) abandonSubmission(ctx context.Context, job core.DownloadJob, ref string, async AsyncDownloader) {
	m.transitionMu.Lock()
	defer m.transitionMu.Unlock()
	if cur, found, err := m.store.Get(ctx, job.ID); err == nil && found && cur.DownloaderRef == ref {
		return
	}
	if err := async.CancelAsync(context.WithoutCancel(ctx), ref); err != nil {
		log.Printf("download: abandon late submission %s of job %s: %v", ref, shortID(job.ID), err)
	}
	log.Printf("download: job %s was canceled, retried or cleared while %s placed it; abandoned %s", shortID(job.ID), job.DownloaderName, ref)
}

// sortedEntries returns a copy of m.downloaders filtered to entries whose Order
// map contains granularity g, sorted ascending by Order[g] (stable — input order
// is the tiebreaker). An empty req.Granularity is treated as GranularityTrack.
func (m *Manager) sortedEntries(g core.DownloadGranularity) []DownloaderEntry {
	var filtered []DownloaderEntry
	for _, e := range m.downloaders {
		if _, ok := e.Order[g]; ok {
			filtered = append(filtered, e)
		}
	}
	sort.SliceStable(filtered, func(i, j int) bool {
		return filtered[i].Order[g] < filtered[j].Order[g]
	})
	return filtered
}

// pick chooses the first entry (by ascending Order[g]) whose CanDownload returns
// true for the request's granularity. An empty req.Granularity defaults to track.
func (m *Manager) pick(ctx context.Context, req core.DownloadRequest) (Downloader, error) {
	g := req.Granularity
	if g == "" {
		g = core.GranularityTrack
	}
	entries := m.sortedEntries(g)
	if name := req.PreferDownloader; name != "" {
		for _, e := range entries {
			if e.Downloader.Name() != name {
				continue
			}
			if ok, err := e.Downloader.CanDownload(ctx, req); err == nil && ok {
				return e.Downloader, nil
			}
		}
	}
	for _, e := range entries {
		ok, err := e.Downloader.CanDownload(ctx, req)
		if err != nil {
			continue
		}
		if ok {
			return e.Downloader, nil
		}
	}
	return nil, fmt.Errorf("no %s downloader can fetch %q by %q", g, req.Title, req.Artist)
}

// pickAfter returns the first entry in the same granularity chain that comes
// AFTER the one named afterName (in ascending Order[g] sort) and whose CanDownload
// accepts req. It is used by the sync worker to fall back through the chain when a
// downloader's Start fails.
//
// Note: fallback is intentionally limited to the sync (track) lane. Async
// downloaders (e.g. Lidarr) run on their own reconciler lane and are not subject
// to this retry logic.
func (m *Manager) pickAfter(ctx context.Context, req core.DownloadRequest, afterName string) (Downloader, error) {
	g := req.Granularity
	if g == "" {
		g = core.GranularityTrack
	}
	skip := true
	for _, e := range m.sortedEntries(g) {
		if skip {
			if e.Downloader.Name() == afterName {
				skip = false
			}
			continue
		}
		// Intentionally uses the background ctx (not jctx) so a fallback candidate
		// isn't pre-poisoned by the prior attempt's timeout or cancellation.
		ok, err := e.Downloader.CanDownload(ctx, req)
		if err != nil {
			continue
		}
		if ok {
			return e.Downloader, nil
		}
	}
	return nil, fmt.Errorf("no further %s downloader after %q for %q", g, afterName, req.Title)
}

// Enqueue persists a new job (or JOINS an active one with the same dedup key) and
// pushes it to the worker pool. Concurrency-safe: simultaneous same-key enqueues
// return the single existing job.
func (m *Manager) Enqueue(ctx context.Context, req core.DownloadRequest) (core.DownloadJob, error) {
	req.Quality = m.resolveQuality(ctx, req.Quality)
	// Before DedupKey purely for readability — DedupKey does not read ISRC, so
	// enrichment cannot shift a job onto a different key.
	req = m.enrichISRC(ctx, req)
	dedup := DedupKey(req)

	// Serialize the dedup-check + insert so two same-key callers can't both create.
	m.mu.Lock()

	if existing, ok, err := m.store.ActiveByDedup(ctx, dedup); err != nil {
		m.mu.Unlock()
		return core.DownloadJob{}, err
	} else if ok {
		m.mu.Unlock()
		return m.joinExisting(ctx, existing) // dedup-join: no second dispatch
	}
	// Terminal guard: same dedup identity already exists as a terminal job
	// (completed/failed). Re-clicking a coverage page's bulk action must not
	// create another job for the same track; retry is explicit via Retry.
	// ForceOverwrite (quality upgrade) bypasses this by design — its dedup
	// includes the upgrade tier and therefore never collides.
	if source, externalID := strings.TrimSpace(req.Source), strings.TrimSpace(req.ExternalID); source != "" && externalID != "" && !req.ForceOverwrite {
		if existing, ok, err := m.store.GetByDedup(ctx, dedup); err != nil {
			m.mu.Unlock()
			return core.DownloadJob{}, err
		} else if ok {
			m.mu.Unlock()
			return m.joinExisting(ctx, existing)
		}
		// Legacy fallback: rows inserted before dedup included section/quality
		// (or with a mismatched dedup due to test fixture "old-key") are not
		// found via the indexed lookup. Fall back to the field-by-field scan
		// so the terminal guard remains correct for those rows. This path is
		// expected to be rare (only legacy rows) and will be removed after a
		// one-shot backfill (same pattern as BackfillCanonicalIDs).
		jobs, lerr := m.store.List(ctx)
		if lerr != nil {
			m.mu.Unlock()
			return core.DownloadJob{}, lerr
		}
		for _, job := range jobs {
			if !strings.EqualFold(job.Source, source) || job.ExternalID != externalID {
				continue
			}
			same, serr := m.sameSectionRange(ctx, job.ID, req)
			if serr != nil {
				m.mu.Unlock()
				return core.DownloadJob{}, serr
			}
			if same {
				m.mu.Unlock()
				return m.joinExisting(ctx, job)
			}
		}
	}

	dl, err := m.pick(ctx, req)
	if err != nil {
		m.mu.Unlock()
		return core.DownloadJob{}, err
	}

	job := core.DownloadJob{
		ID:             uuid.NewString(),
		DedupKey:       dedup,
		Status:         core.DownloadQueued,
		Progress:       0,
		DownloaderName: dl.Name(),
		Source:         req.Source,
		ExternalID:     req.ExternalID,
		// Carry the request fields so any JobStore (incl. in-memory) and the worker
		// fallback have enough to run; the sqlStore ALSO persists request_json.
		Artist:          req.Artist,
		Title:           req.Title,
		Album:           req.Album,
		ISRC:            req.ISRC,
		PlayWhenReady:   req.PlayWhenReady,
		AddToPlaylistID: req.AddToPlaylistID,
		Quality:         req.Quality,
		CreatedAt:       m.clock.Now().Unix(),
	}
	if err := m.store.Insert(ctx, job, req); err != nil {
		m.mu.Unlock()
		return core.DownloadJob{}, err
	}
	m.reqs[job.ID] = req
	m.publishEvent(TopicQueued, job, "")
	log.Printf("download queued: %q by %q (job %s, downloader %s)", job.Title, job.Artist, shortID(job.ID), job.DownloaderName)
	id := job.ID

	// Unlock BEFORE dispatching. Workers re-acquire m.mu inside the progress
	// callback, so a blocking send under m.mu would deadlock. The job is already
	// persisted as queued, so nothing is lost even if we shut down between here and
	// the dispatch.
	m.mu.Unlock()

	// Async downloader (e.g. Lidarr): hand off via Submit and let the reconciler
	// advance the job — never pin a worker. Submit runs OUTSIDE m.mu (it makes
	// network calls).
	if async, ok := dl.(AsyncDownloader); ok {
		m.submitAsync(ctx, job, req, async)
		return job, nil
	}

	// Sync downloader: dispatch to the worker pool.
	select {
	case m.queue <- id:
	case <-m.stopCh:
	}
	return job, nil
}

// requestForJob returns the originating DownloadRequest for job, preferring the
// in-memory map (fast path) then the persisted request_json, falling back to a
// minimal reconstruction from the job's denormalized columns. Single rehydration
// point so a new request field is added once, not four times.
func (m *Manager) requestForJob(ctx context.Context, job core.DownloadJob) core.DownloadRequest {
	req, _ := m.loadRequest(ctx, job)
	return req
}

// loadRequest keeps completion pending when the durable request cannot be read.
// Acquisition callers keep their legacy reconstruction on a transient error;
// completion callers must propagate it so attribution cannot be skipped.
func (m *Manager) loadRequest(ctx context.Context, job core.DownloadJob) (core.DownloadRequest, error) {
	m.mu.Lock()
	req, ok := m.reqs[job.ID]
	m.mu.Unlock()
	if ok {
		return req, nil
	}
	stored, found, err := m.store.GetRequest(ctx, job.ID)
	if found && err == nil {
		return stored, nil
	}
	// Legacy rows with empty request_json lack fields not denormalized on the job.
	// Return the reconstruction with the read error so completion fails closed.
	return core.DownloadRequest{
		Source: job.Source, ExternalID: job.ExternalID, Artist: job.Artist,
		Title: job.Title, Album: job.Album, ISRC: job.ISRC, DurationMs: job.DurationMs,
		PlayWhenReady: job.PlayWhenReady, AddToPlaylistID: job.AddToPlaylistID, Quality: job.Quality,
	}, err
}

// sameSectionRange reports whether an existing job was created for the same trim
// range as req. Kept for legacy fallback where dedup does not match (pre-section dedup).
func (m *Manager) sameSectionRange(ctx context.Context, jobID string, req core.DownloadRequest) (bool, error) {
	start, end := strings.TrimSpace(req.SectionStart), strings.TrimSpace(req.SectionEnd)
	prev, ok := m.reqs[jobID]
	if !ok {
		stored, found, err := m.store.GetRequest(ctx, jobID)
		if err != nil {
			return false, err
		}
		if found {
			prev = stored
		}
	}
	return strings.TrimSpace(prev.SectionStart) == start && strings.TrimSpace(prev.SectionEnd) == end, nil
}

// Status returns the current persisted job.
func (m *Manager) Status(ctx context.Context, jobID string) (core.DownloadJob, error) {
	j, ok, err := m.store.Get(ctx, jobID)
	if err != nil {
		return core.DownloadJob{}, err
	}
	if !ok {
		return core.DownloadJob{}, fmt.Errorf("job %q not found", jobID)
	}
	return j, nil
}

// List returns all jobs (newest-first ordering is the store's responsibility).
func (m *Manager) List(ctx context.Context) ([]core.DownloadJob, error) {
	return m.store.List(ctx)
}

// publishEvent emits a DownloadEvent on the given topic from the job's state.
func (m *Manager) publishEvent(topic string, job core.DownloadJob, errMsg string) {
	if m.bus == nil {
		return
	}
	m.bus.Publish(events.Event{Topic: topic, Payload: core.DownloadEvent{
		JobID:          job.ID,
		DedupKey:       job.DedupKey,
		Status:         job.Status,
		Progress:       job.Progress,
		Error:          errMsg,
		Source:         job.Source,
		ExternalID:     job.ExternalID,
		LibraryTrackID: job.LibraryTrackID,
		CoverArtID:     job.CoverArtID,
		CanonicalID:    job.CanonicalID,
	}})
}

// --- worker plumbing ---

func (m *Manager) worker() {
	defer m.wg.Done()
	for {
		// Wait at the gate BEFORE pulling a job so a paused queue dispatches
		// nothing new. In-flight jobs are unaffected (already pulled). Stop
		// unblocks a gated worker.
		select {
		case <-m.stopCh:
			return
		case <-m.gate():
		}
		select {
		case <-m.stopCh:
			return
		case id := <-m.queue:
			// Guard: if Pause() arrived while we were waiting at the queue,
			// return the job to the channel and re-loop so the gate blocks us.
			select {
			case <-m.gate():
				m.process(id)
			default:
				// Paused — re-queue and let the next gate iteration block.
				select {
				case m.queue <- id:
				case <-m.stopCh:
					return
				}
			}
		}
	}
}

func (m *Manager) process(id string) {
	ctx := context.Background()
	m.mu.Lock()
	if _, busy := m.cancels[id]; busy {
		// Another worker already has this job. A queued row can reach the channel
		// twice — recovery and RedispatchQueued both dispatch from the same
		// persisted state, as does a queued job canceled and retried — and running
		// it twice would download the same track into two files.
		m.mu.Unlock()
		return
	}
	// Registered before the job is started, so a Cancel from here on stops this
	// worker rather than racing it to the stored row.
	jctx, cancel := context.WithCancel(ctx)
	m.cancels[id] = cancel
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		delete(m.cancels, id)
		m.mu.Unlock()
		cancel()
		m.redispatchIfRequeued(ctx, id)
	}()

	// Only a job still queued starts. A dispatch for a job canceled, finished
	// or cleared since it was queued is stale and is dropped.
	job, started, err := m.transition(ctx, id, func(j *core.DownloadJob) (publication, bool) {
		if j.Status != core.DownloadQueued {
			return publication{}, false
		}
		j.Status, j.Progress, j.Error, j.StartedAt = core.DownloadRunning, 0, "", 0
		return publication{topic: TopicProgress}, true
	})
	m.logTransitionError(id, "start", err)
	if !started {
		return
	}
	attempt := job.Attempts
	m.mu.Lock()
	req, haveReq := m.reqs[id]
	m.mu.Unlock()
	if !haveReq {
		req = m.requestForJob(ctx, job)
	}
	jobTmo := m.jobTimeout(req)
	jctx, cancelTimeout := context.WithTimeout(jctx, jobTmo)
	defer cancelTimeout()

	var dl Downloader
	for _, e := range m.downloaders {
		if e.Downloader.Name() == job.DownloaderName {
			dl = e.Downloader
			break
		}
	}
	if dl == nil {
		m.fail(ctx, id, attempt, "downloader not registered")
		log.Printf("download failed: %q — downloader %q not registered", job.Title, job.DownloaderName)
		return
	}
	log.Printf("download running: %q (job %s via %s)", job.Title, shortID(id), dl.Name())

	// Fallback loop: try the current downloader; on a genuine "couldn't produce"
	// error (not timeout, not cancel) fall through to the next downloader in the same
	// granularity chain via pickAfter.  Only when the chain is exhausted does the job
	// reach DownloadFailed.  Timeout and cancel remain terminal — no fallback.
	//
	// Note: this fallback is the sync (track) lane only.  Async downloaders (e.g.
	// Lidarr) run on their own reconciler lane and are not subject to this loop.
	var outPath string
	var lastErr error
	succeeded := false
	for {
		if jctx.Err() != nil {
			// Canceled before the downloader ran: nothing to abort.
			break
		}
		log.Printf("download attempting: %q (job %s via %s)", job.Title, shortID(id), dl.Name())

		// Heartbeat: while the download runs, log every 30s so a long-running or stuck
		// job is visibly alive (with elapsed time). Stops as soon as Start returns.
		hbStop := make(chan struct{})
		go func() {
			start := time.Now()
			tk := time.NewTicker(30 * time.Second)
			defer tk.Stop()
			for {
				select {
				case <-hbStop:
					return
				case <-tk.C:
					log.Printf("download still running: %q (job %s, %s elapsed)", job.Title, shortID(id), time.Since(start).Round(time.Second))
				}
			}
		}()

		var serr error
		outPath, serr = dl.Start(jctx, req, func(p int) { m.progress(ctx, id, attempt, p) })
		close(hbStop)
		m.recordAttemptOutcome(dl.Name(), serr)

		if serr == nil {
			succeeded = true
			break
		}
		if jctx.Err() != nil {
			break // timed out or canceled: decided below, no fallback
		}
		// The chosen downloader couldn't produce the file (e.g. spotDL
		// LookupError).  Try the next downloader in the same granularity
		// chain before giving up.  The manual "download from a link" fallback
		// (DownloadRequest.ManualURL, surfaced on the failed state) remains the
		// last resort and is only reachable once every auto-downloader is exhausted.
		log.Printf("download: %q (job %s) downloader %q failed (%v), trying next in chain", job.Title, shortID(id), dl.Name(), serr)
		next, nerr := m.pickAfter(ctx, req, dl.Name())
		if nerr != nil {
			lastErr = serr // chain exhausted
			break
		}
		// Persist the new downloader so the job is recoverable after restart.
		_, moved, err := m.transition(ctx, id, func(j *core.DownloadJob) (publication, bool) {
			if !runningAttempt(*j, attempt) {
				return publication{}, false
			}
			j.DownloaderName = next.Name()
			return publication{}, true
		})
		m.logTransitionError(id, "fall back", err)
		if !moved && err == nil {
			return // this attempt was ended elsewhere
		}
		dl = next
	}

	switch {
	case succeeded:
		// Output exists: completion owns it from here, even if a cancel arrived
		// as the downloader finished. Discarding written bytes would only
		// download them again.
		m.completeAttempt(ctx, job, attempt, outPath)
	case errors.Is(jctx.Err(), context.DeadlineExceeded):
		// Hit the per-job timeout — terminal, no fallback.
		reason := fmt.Sprintf("timed out after %s", jobTmo)
		if _, ok := m.fail(ctx, id, attempt, reason); ok {
			log.Printf("download timed out: %q (job %s) after %s", job.Title, shortID(id), jobTmo)
		}
	case jctx.Err() != nil:
		m.endCanceled(ctx, id, attempt)
	default:
		// All downloaders in the chain failed — mark the job failed.
		// The ManualURL last-resort (Retry with a URL) is still available to the user.
		if _, ok := m.fail(ctx, id, attempt, lastErr.Error()); ok {
			log.Printf("download failed (chain exhausted): %q (job %s) — %v", job.Title, shortID(id), lastErr)
			m.scheduleAutoRetryIfRetryable(id, lastErr, attempt)
		}
	}
}

// redispatchIfRequeued puts a job back on the worker channel when it was
// retried while this worker still held it: that retry's dispatch found the job
// busy and was dropped. A duplicate dispatch is harmless; only a queued row starts.
func (m *Manager) redispatchIfRequeued(ctx context.Context, id string) {
	if m.isStopping() {
		return // the next manager's recovery dispatches queued rows
	}
	if j, ok, err := m.store.Get(ctx, id); err != nil || !ok || j.Status != core.DownloadQueued {
		return
	}
	select {
	case m.queue <- id:
	case <-m.stopCh:
	default: // queue full; recovery on the next boot picks it up
	}
}

// endCanceled ends an attempt whose context was canceled. Stop() cancels the
// same way the user does, so they are told apart: a job aborted by shutdown or
// an adapter reload goes back to queued, since nothing about it failed and the
// next manager's recovery pass re-dispatches queued rows. Marking it canceled
// (or leaving it running) would strand it.
func (m *Manager) endCanceled(ctx context.Context, id string, attempt int) {
	stopping := m.isStopping()
	_, ok, err := m.transition(ctx, id, func(j *core.DownloadJob) (publication, bool) {
		if !runningAttempt(*j, attempt) {
			return publication{}, false
		}
		if stopping {
			j.Status, j.Progress = core.DownloadQueued, 0
			return publication{topic: TopicProgress}, true
		}
		j.Status = core.DownloadCanceled
		return publication{TopicFailed, "canceled"}, true
	})
	m.logTransitionError(id, "cancel", err)
	if ok && stopping {
		log.Printf("download requeued for shutdown: job %s", shortID(id))
	}
}

// completeAttempt hands an attempt's output to completion. Output from an
// attempt that is no longer the job's current one (a reload's recovery failed
// it and it was retried) is not recorded against the newer attempt.
func (m *Manager) completeAttempt(ctx context.Context, job core.DownloadJob, attempt int, outPath string) {
	m.transitionMu.Lock()
	defer m.transitionMu.Unlock()
	cur, found, err := m.store.Get(ctx, job.ID)
	switch {
	case err != nil || !found:
		// The downloader has already written the file. Keep its identity in
		// memory even if the job store is unavailable right now.
		cur = job
		cur.Status = core.DownloadRunning
	case !runningAttempt(cur, attempt):
		log.Printf("download completion: job %s attempt %d ended before its output %s arrived; not recorded", shortID(job.ID), attempt, outPath)
		return
	}
	cur.OutputPath = outPath
	if _, err := m.completeOutputLocked(ctx, cur); err != nil {
		log.Printf("download completion: job %s: %v", shortID(job.ID), err)
	}
}

// ScheduleScan asks for a library rescan through the same debounced window a
// completed download uses, so a file that arrived by another route (an upload)
// is picked up without a second, competing rescan mechanism.
func (m *Manager) ScheduleScan() { m.scheduleScan("") }

// scheduleScan (re)arms the debounce timer. Multiple completions within the
// window collapse into ONE scan. Uses the injectable clock so tests advance time.
func (m *Manager) scheduleScan(jobID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pending = true
	if m.debounce != nil {
		m.debounce() // cancel the previous timer; we re-arm to extend the window
	}
	m.debounce = m.clock.AfterFunc(m.cfg.DebounceWindow, func() {
		m.runScan()
	})
}

// runScan performs the coalesced library refresh: StartScan → poll → re-match all
// recently-completed jobs → set library_track_id → bump library_version →
// publish library.updated + per-job download.complete (with artist/album IDs).
func (m *Manager) runScan() {
	m.mu.Lock()
	if !m.pending {
		m.mu.Unlock()
		return
	}
	m.pending = false
	m.debounce = nil
	m.mu.Unlock()

	ctx := context.Background()
	if m.scanner == nil {
		return
	}
	if err := m.scanner.StartScan(ctx); err != nil {
		log.Printf("library scan after download failed: %v", err)
		return
	}
	if s, ok := m.scanner.(SynchronousScanner); !ok || !s.ScansSynchronously() {
		m.waitForScan(ctx)
	}

	// Bump library_version FIRST so re-matches recompute against fresh data
	// (invalidates match_cache rows whose library_version is now stale).
	if m.version != nil {
		if cur, err := m.version.LibraryVersion(ctx); err == nil {
			_ = m.version.SetLibraryVersion(ctx, cur+1)
		}
	}

	// Re-match every completed job that has no library_track_id yet.
	jobs, err := m.store.List(ctx)
	if err != nil {
		return
	}
	// linkedCanonicalIDs accumulates the canonical ids minted for jobs linked in
	// THIS scan. Used below to call RefreshLinked (Task 4 pre-warm). Empty ids
	// (nil minter, minter miss) are excluded — they have no binding to refresh.
	var linkedCanonicalIDs []string
	for _, j := range jobs {
		if j.Status != core.DownloadCompleted || j.LibraryTrackID != "" {
			continue
		}
		if m.rematcher == nil {
			continue
		}
		// Forward all job metadata so the matcher can search the library by title/artist/ISRC.
		// An empty Title would leave the matcher with no candidate query → no match ever found.
		res, merr := m.rematcher.Match(ctx, m.recordingOf(ctx, j))
		if merr != nil || res.Status != core.MatchInLibrary {
			continue
		}
		if cid, ok := m.linkJob(ctx, j, res); ok && cid != "" {
			linkedCanonicalIDs = append(linkedCanonicalIDs, cid)
		}
	}

	// Task 4: best-effort pre-warm — refresh resolver bindings for the jobs linked
	// in this scan so the next Resolve hits a warm cache. Never fails the scan.
	// NOTE: this does NOT bump binding_epoch; RefreshLinked operates at the current
	// epoch, marking only these ids stale then re-resolving them.
	if m.resolve != nil && len(linkedCanonicalIDs) > 0 {
		if r := m.resolve(); r != nil {
			if rerr := r.RefreshLinked(ctx, linkedCanonicalIDs); rerr != nil {
				log.Printf("download: RefreshLinked after scan failed (best-effort, scan still succeeded): %v", rerr)
			}
		}
	}

	// Per-album/artist IDs on LibraryUpdatedEvent are deferred to a later milestone;
	// the frontend does broad library invalidation on this event.
	if m.bus != nil {
		m.bus.Publish(events.Event{Topic: TopicLibraryUpdate, Payload: core.LibraryUpdatedEvent{}})
	}
}

// waitForScan blocks until the Navidrome scan triggered by StartScan has run to
// completion (or a budget elapses). It is deliberately two-phase to defeat the
// scan-start RACE:
//
//  1. SETTLE: poll until getScanStatus.scanning flips true (the scan has actually
//     engaged) OR a short settle budget (ScanSettleMax) elapses. startScan is
//     asynchronous on Navidrome — for a brief window after it returns, getScanStatus
//     still reports scanning=false. The OLD code broke out of its poll loop on that
//     first false, re-matching against the PRE-download index, so a just-downloaded
//     file was never found (library_track_id stayed empty permanently).
//  2. DRAIN: once scanning has been observed (or the settle budget lapsed for an
//     instantaneous scan), poll until scanning=false again OR the poll budget
//     (ScanPollMax) elapses — the file is now indexed and re-match can find it.
//
// All budgets use wall-clock time (this runs inside the debounce timer fn, off the
// hot path) so a frozen test clock can't stall the loop; the cadence is ScanPollEvery.
func (m *Manager) waitForScan(ctx context.Context) {
	poll := m.cfg.ScanPollEvery
	if poll <= 0 {
		poll = 500 * time.Millisecond
	}

	// Phase 1 — SETTLE: wait for the scan to begin.
	settleDeadline := time.Now().Add(m.cfg.ScanSettleMax)
	started := false
	for time.Now().Before(settleDeadline) {
		st, err := m.scanner.ScanStatus(ctx)
		if err != nil {
			break
		}
		if st.Scanning {
			started = true
			break
		}
		time.Sleep(poll)
	}

	// Phase 2 — DRAIN: wait for the scan to finish. If we never observed it start
	// (an already-idle/instantaneous scan), there is nothing to drain.
	if !started {
		return
	}
	drainDeadline := time.Now().Add(m.cfg.ScanPollMax)
	for time.Now().Before(drainDeadline) {
		st, err := m.scanner.ScanStatus(ctx)
		if err != nil || !st.Scanning {
			break
		}
		time.Sleep(poll)
	}
}

// publishComplete emits a final download.complete carrying the library_track_id
// and canonical_id. The FE prefers canonicalId for cover resolution so covers
// survive a backend swap. artistId/albumId are deferred to a later milestone.
func (m *Manager) publishComplete(job core.DownloadJob, libraryTrackID string) {
	if m.bus == nil {
		return
	}
	m.bus.Publish(events.Event{Topic: TopicComplete, Payload: core.DownloadEvent{
		JobID: job.ID, DedupKey: job.DedupKey, Status: core.DownloadCompleted, Progress: 100,
		Source: job.Source, ExternalID: job.ExternalID, LibraryTrackID: libraryTrackID,
		CoverArtID:  job.CoverArtID,
		CanonicalID: job.CanonicalID,
	}})
}

// Cancel aborts an in-flight or queued job. An in-flight exec is killed via its
// context; a queued job is marked canceled so the worker skips it. Once a
// downloader has produced output, the job can no longer be canceled: its
// output is recorded instead.
func (m *Manager) Cancel(ctx context.Context, jobID string) error {
	m.mu.Lock()
	cancel, inFlight := m.cancels[jobID]
	_, unrecorded := m.unrecorded[jobID]
	m.mu.Unlock()
	if unrecorded {
		return fmt.Errorf("cannot cancel download %q while its output is being recorded", jobID)
	}
	if inFlight {
		cancel() // kills the in-flight Start; process() marks it canceled
		return nil
	}
	job, ok, err := m.store.Get(ctx, jobID)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("job %q not found", jobID)
	}
	if job.Status == core.DownloadRunning && job.CompletionPending {
		return fmt.Errorf("cannot cancel download %q while its output is being recorded", jobID)
	}

	// Async job (running via an external manager like Lidarr): cancel externally.
	if job.DownloaderRef != "" {
		if async := m.asyncFor(job.DownloaderName); async != nil {
			_ = async.CancelAsync(ctx, job.DownloaderRef)
		}
	}
	return m.cancelStoredJob(ctx, jobID)
}

// cancelStoredJob rechecks ownership under the transition lock. An external
// cancellation may return after its output was handed to completion; its older
// snapshot must not overwrite the pending phase or terminal commit, so it
// waits for an in-flight poll of the job to be applied. A worker
// that picked the job up meanwhile is stopped instead, and ends it itself.
func (m *Manager) cancelStoredJob(ctx context.Context, jobID string) error {
	m.mu.Lock()
	polling := m.polls[jobID]
	m.mu.Unlock()
	if polling != nil {
		// A poll that is confirming this job's output decides first.
		select {
		case <-polling:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	m.transitionMu.Lock()
	defer m.transitionMu.Unlock()
	m.mu.Lock()
	cancel, inFlight := m.cancels[jobID]
	_, pending := m.unrecorded[jobID]
	m.mu.Unlock()
	if pending {
		return fmt.Errorf("cannot cancel download %q while its output is being recorded", jobID)
	}
	if inFlight {
		cancel()
		return nil
	}
	recording := false
	_, found, err := m.transitionLocked(ctx, jobID, func(j *core.DownloadJob) (publication, bool) {
		if j.CompletionPending {
			recording = true
			return publication{}, false
		}
		if j.Status != core.DownloadQueued && j.Status != core.DownloadRunning {
			return publication{}, false
		}
		j.Status = core.DownloadCanceled
		return publication{TopicFailed, "canceled"}, true
	})
	if err != nil {
		return err
	}
	if recording {
		return fmt.Errorf("cannot cancel download %q while its output is being recorded", jobID)
	}
	if !found {
		if _, ok, err := m.store.Get(ctx, jobID); err == nil && !ok {
			return fmt.Errorf("job %q not found", jobID)
		}
	}
	return nil
}

// Retry starts a new attempt of a failed or canceled job (attempts++) and
// dispatches it. When manualURL is non-empty it is stored on the job's
// DownloadRequest so the spotDL adapter can use the pipe syntax (or direct
// URL) on the next attempt; a plain retry searches again. A job whose output
// is awaiting its record retries that record instead of downloading again.
//
// Dispatch mirrors Enqueue: async downloaders (e.g. Lidarr) are re-submitted
// via submitAsync, sync downloaders go to the worker channel.
func (m *Manager) Retry(ctx context.Context, jobID string, manualURL string) (core.DownloadJob, error) {
	job, ok, err := m.store.Get(ctx, jobID)
	if err != nil {
		return core.DownloadJob{}, err
	}
	if !ok {
		return core.DownloadJob{}, fmt.Errorf("job %q not found", jobID)
	}
	m.mu.Lock()
	_, pending := m.unrecorded[job.ID]
	m.mu.Unlock()
	if pending || job.CompletionPending {
		return m.retryCompletion(ctx, job)
	}
	return m.retry(ctx, jobID, manualURL, anyAttempt)
}

// anyAttempt lets retry start a new attempt whichever attempt last ended.
const anyAttempt = -1

// retry starts a new attempt if the job is failed or canceled and, unless
// from is anyAttempt, its last attempt is from: an automatic retry belongs to
// the attempt that failed, and does nothing once another has begun.
func (m *Manager) retry(ctx context.Context, jobID, manualURL string, from int) (core.DownloadJob, error) {
	retryable := func(j core.DownloadJob) bool {
		return (j.Status == core.DownloadFailed || j.Status == core.DownloadCanceled) && !j.CompletionPending &&
			(from == anyAttempt || j.Attempts == from)
	}
	cur, ok, err := m.store.Get(ctx, jobID)
	if err != nil || !ok || !retryable(cur) {
		if err == nil && !ok {
			err = fmt.Errorf("job %q not found", jobID)
		}
		return cur, err // nothing to retry
	}
	// The rest of the original request (quality, sections, attribution) is
	// kept as it was. A new attempt starts at the top of its fallback chain.
	req := m.requestForJob(ctx, cur)
	req.ManualURL = manualURL
	downloader := cur.DownloaderName
	if dl, err := m.pick(ctx, req); err == nil {
		downloader = dl.Name()
	}
	m.transitionMu.Lock()
	if now, ok, err := m.store.Get(ctx, jobID); err != nil || !ok || !retryable(now) {
		m.transitionMu.Unlock()
		return now, err // another retry got there first
	}
	// A manual URL belongs to the retry it came with. It is persisted before
	// the job is queued, so a restart in between still runs it with the URL.
	if stored, _, err := m.store.GetRequest(ctx, jobID); err != nil || stored.ManualURL != manualURL {
		if err := m.store.UpdateRequest(ctx, jobID, req); err != nil {
			m.transitionMu.Unlock()
			return cur, fmt.Errorf("retry %s: keep its request: %w", shortID(jobID), err)
		}
	}
	job, queued, err := m.transitionLocked(ctx, jobID, func(j *core.DownloadJob) (publication, bool) {
		if !retryable(*j) {
			return publication{}, false
		}
		j.Status, j.Progress, j.Error, j.OutputPath = core.DownloadQueued, 0, "", ""
		j.Attempts++
		j.DownloaderName, j.DownloaderRef = downloader, ""
		return publication{topic: TopicQueued}, true
	})
	if queued {
		m.mu.Lock()
		m.reqs[jobID] = req
		m.mu.Unlock()
	}
	m.transitionMu.Unlock()
	if err != nil || !queued {
		return job, err
	}
	if async := m.asyncFor(job.DownloaderName); async != nil {
		go m.submitAsync(context.WithoutCancel(ctx), job, req, async)
		return job, nil
	}
	select {
	case m.queue <- job.ID:
	case <-m.stopCh:
	}
	return job, nil
}

// scheduleAutoRetryIfRetryable auto-re-enqueues a just-failed job after
// Config.PacingCooldown when lastErr classifies as ClassRateLimited or
// ClassBotChallenge — waiting genuinely helps those, unlike unavailable/
// no_match/spotify_api_error/unknown, which are left failed for a human.
// Bounded by Config.MaxAutoRetries so a permanently rate-limited setup
// doesn't retry forever. The retry belongs to the attempt that failed, and
// does nothing once the manager has stopped.
func (m *Manager) scheduleAutoRetryIfRetryable(jobID string, lastErr error, attempt int) {
	var ce ClassifiedError
	if !errors.As(lastErr, &ce) {
		return
	}
	if ce.Class != ClassRateLimited && ce.Class != ClassBotChallenge {
		return
	}
	if attempt >= m.cfg.MaxAutoRetries {
		log.Printf("download: job %s exhausted %d auto-retries, leaving failed", shortID(jobID), m.cfg.MaxAutoRetries)
		return
	}
	log.Printf("download: job %s failed as %s — auto-retrying in %s", shortID(jobID), ce.Class, m.cfg.PacingCooldown)
	time.AfterFunc(m.cfg.PacingCooldown, func() {
		select {
		case <-m.stopCh:
			return // a stopped manager no longer owns the job
		default:
		}
		if _, err := m.retry(context.Background(), jobID, "", attempt); err != nil {
			log.Printf("download: auto-retry of job %s failed: %v", shortID(jobID), err)
		}
	})
}

// Clear hard-deletes a single terminal job (completed/failed/canceled) and
// publishes download.removed. It refuses to delete a queued/running job — those
// are canceled, not cleared.
func (m *Manager) Clear(ctx context.Context, jobID string) error {
	removed, err := m.store.Delete(ctx, jobID)
	if err != nil {
		return err
	}
	if !removed {
		job, ok, err := m.store.Get(ctx, jobID)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("job %q not found", jobID)
		}
		return fmt.Errorf("cannot clear active job %q (status %s)", jobID, job.Status)
	}
	m.mu.Lock()
	delete(m.reqs, jobID)
	m.mu.Unlock()
	m.publishRemoved([]string{jobID})
	return nil
}

// ClearFinished hard-deletes ALL terminal jobs and publishes a single
// download.removed carrying every deleted id.
func (m *Manager) ClearFinished(ctx context.Context) ([]string, error) {
	ids, err := m.store.DeleteFinished(ctx)
	if err != nil {
		return nil, err
	}
	if len(ids) > 0 {
		m.mu.Lock()
		for _, id := range ids {
			delete(m.reqs, id)
		}
		m.mu.Unlock()
		m.publishRemoved(ids)
	}
	return ids, nil
}

// publishRemoved emits download.removed for the given job ids.
func (m *Manager) publishRemoved(ids []string) {
	if m.bus == nil {
		return
	}
	m.bus.Publish(events.Event{Topic: TopicRemoved, Payload: core.DownloadRemovedEvent{JobIDs: ids}})
}

// gate returns the current resume channel. When running it is closed (receiving
// returns instantly); when paused it is open (receiving blocks until Resume).
func (m *Manager) gate() <-chan struct{} {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.resumeCh
}

// Pause stops dispatching NEW jobs. Jobs already running finish; queued jobs stay
// queued. In-memory only (a restart comes up running). Idempotent.
func (m *Manager) Pause() {
	m.mu.Lock()
	if m.paused {
		m.mu.Unlock()
		return
	}
	m.paused = true
	m.resumeCh = make(chan struct{}) // open: workers block on it at the gate
	m.mu.Unlock()
	m.publishQueueState(true)
}

// Resume re-enables dispatch, unblocking any gated workers. Idempotent.
func (m *Manager) Resume() {
	m.mu.Lock()
	if !m.paused {
		m.mu.Unlock()
		return
	}
	m.paused = false
	close(m.resumeCh) // unblock workers; the now-closed channel reads as "running"
	m.mu.Unlock()
	m.publishQueueState(false)
}

// IsPaused reports the current gate state.
func (m *Manager) IsPaused() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.paused
}

// recordAttemptOutcome updates the per-downloader consecutive-failure counter
// used by adaptive pacing. err == nil (a successful attempt) resets the
// counter. A failure only counts toward the threshold when it classifies as
// ClassRateLimited or ClassBotChallenge — other classes are left untouched
// (they aren't signs of being rate-limited, so a string of "no_match" results
// must not trigger a pause). Reaching Config.PacingThreshold auto-pauses
// dispatch and schedules an auto-Resume after Config.PacingCooldown.
func (m *Manager) recordAttemptOutcome(downloaderName string, err error) {
	if err == nil {
		m.mu.Lock()
		m.consecutiveFail[downloaderName] = 0
		m.mu.Unlock()
		return
	}
	var ce ClassifiedError
	class := ClassUnknown
	if errors.As(err, &ce) {
		class = ce.Class
	}
	if class != ClassRateLimited && class != ClassBotChallenge {
		return
	}
	m.mu.Lock()
	m.consecutiveFail[downloaderName]++
	n := m.consecutiveFail[downloaderName]
	threshold := m.cfg.PacingThreshold
	if n >= threshold {
		m.consecutiveFail[downloaderName] = 0
	}
	m.mu.Unlock()
	if n >= threshold {
		log.Printf("download: %q hit %d consecutive rate-limit/bot-challenge failures — pausing dispatch for %s", downloaderName, n, m.cfg.PacingCooldown)
		m.Pause()
		time.AfterFunc(m.cfg.PacingCooldown, m.Resume)
	}
}

// publishQueueState emits download.queue with the current paused flag.
func (m *Manager) publishQueueState(paused bool) {
	if m.bus == nil {
		return
	}
	m.bus.Publish(events.Event{Topic: TopicQueueState, Payload: core.QueueStateEvent{Paused: paused}})
}

// ListChapters enumerates a source URL's internal chapters using the first
// configured downloader that implements ChapterLister. It returns
// ErrNoChapterLister when no such downloader is configured, so a caller can
// tell "nothing can inspect this" apart from "this video has no chapters".
func (m *Manager) ListChapters(ctx context.Context, url string) ([]core.Chapter, error) {
	for _, e := range m.downloaders {
		cl, ok := e.Downloader.(ChapterLister)
		if !ok {
			continue
		}
		return cl.ListChapters(ctx, url)
	}
	return nil, ErrNoChapterLister
}
