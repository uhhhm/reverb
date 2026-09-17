package updater

import (
	"context"
	"fmt"
	"log"
	"os"
	"runtime"
	"sync"
	"time"

	"github.com/uhhhm/reverb/internal/events"
)

// Bus topics. The frontend listens on the same WebSocket it already uses for
// downloads, so an update that finishes downloading surfaces without polling.
const TopicUpdateState = events.TopicUpdate

// checkInterval is how often the release feed is polled, and ytdlpInterval how
// often yt-dlp is upgraded in place.
//
// assetRetryInterval applies while a newer release exists but carries no
// build for this platform yet. Publishing a GitHub release is what starts the
// desktop workflow, so for the first quarter of an hour or so the latest
// release is real but its zips are still being built; waiting a full
// checkInterval before looking again would hold the update back for hours.
const (
	checkInterval      = 6 * time.Hour
	assetRetryInterval = 15 * time.Minute
	ytdlpInterval      = 24 * time.Hour
)

// State is what the UI knows about updates. It is a value: Status returns a
// copy, so a caller can never observe a half-written update.
type State struct {
	// CurrentVersion is the running build ("dev" for a local build, which is
	// never considered outdated).
	CurrentVersion string `json:"currentVersion"`
	// Repo is the GitHub owner/name polled, empty when updates are disabled.
	Repo string `json:"repo"`
	// Checking is true while the release feed is being read.
	Checking bool `json:"checking"`
	// Available is the newer tag found, empty when the build is current.
	Available string `json:"available"`
	// Notes is the release body for Available.
	Notes string `json:"notes"`
	// Downloading is true while the payload is being fetched, with Progress
	// running 0..1.
	Downloading bool    `json:"downloading"`
	Progress    float64 `json:"progress"`
	// Staged is the tag downloaded and verified, waiting for a restart. This is
	// the signal the UI prompts on: nothing is ever applied until the user asks.
	Staged string `json:"staged"`
	// Error is the last failure, cleared by the next successful step. An update
	// that cannot be fetched is not an error the user has to act on.
	Error string `json:"error"`
	// LastCheck is when the release feed was last read successfully.
	LastCheck time.Time `json:"lastCheck,omitempty"`
}

// Publisher is the EventBus slice the service publishes state on.
type Publisher interface {
	Publish(ev events.Event)
}

// Options configures a Service.
type Options struct {
	// Repo is the GitHub owner/name to poll. Empty disables update checks
	// entirely; the yt-dlp upgrader still runs.
	Repo string
	// CurrentVersion is the running build's version.
	CurrentVersion string
	// DataDir is where downloads are staged (alongside the database).
	DataDir string
	// ExePath is the binary to replace. Defaults to os.Executable().
	ExePath string
	// Args are the original launch arguments, including any custom DB or ports.
	Args []string
	// Bus receives update:state events. Optional.
	Bus Publisher
	// Quit is called after the successor process has been spawned, to shut this
	// instance down cleanly (closing the database and stopping Navidrome).
	// Without it the old instance keeps running beside the new one.
	Quit func()
}

// Service polls for releases, downloads a newer one in the background, and
// applies it only when the user asks for a restart.
//
// The download is deliberately eager and the install deliberately not: waiting
// for a restart costs the user nothing, whereas swapping the binary underneath
// a running app does.
type Service struct {
	opts Options

	mu         sync.Mutex
	state      State
	opMu       sync.Mutex // serializes staging and installation, which share files
	installing bool       // protected by opMu; prevents a second successor launch
	// awaitingAsset is set when the newest release has no payload for this
	// platform yet, so the next check comes sooner. Protected by mu.
	awaitingAsset bool
}

// New builds a Service. It does not start any goroutines; call Start.
func New(opts Options) *Service {
	if opts.ExePath == "" {
		if exe, err := os.Executable(); err == nil {
			opts.ExePath = exe
		}
	}
	if opts.CurrentVersion == "" {
		opts.CurrentVersion = "dev"
	}
	s := &Service{opts: opts}
	s.state = State{
		CurrentVersion: opts.CurrentVersion,
		Repo:           opts.Repo,
	}
	// A payload staged by a previous run is still good; surface it immediately
	// so the prompt survives a restart the user made for their own reasons.
	if su, ok := ReadStaged(opts.DataDir); ok && IsNewer(opts.CurrentVersion, su.Tag) {
		s.state.Staged = su.Tag
		s.state.Available = su.Tag
		s.state.Notes = su.Notes
	}
	return s
}

// Status returns the current state.
func (s *Service) Status() State {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

// Start launches the background pollers and returns immediately. Cancelling ctx
// stops them.
func (s *Service) Start(ctx context.Context) {
	go s.pollReleases(ctx)
	go s.pollYtDlp(ctx)
}

func (s *Service) pollReleases(ctx context.Context) {
	if s.opts.Repo == "" {
		return
	}
	s.CheckNow(ctx)
	for {
		timer := time.NewTimer(s.nextCheckIn())
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
			s.CheckNow(ctx)
		}
	}
}

// nextCheckIn is how long to wait before reading the release feed again.
func (s *Service) nextCheckIn() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.awaitingAsset {
		return assetRetryInterval
	}
	return checkInterval
}

func (s *Service) pollYtDlp(ctx context.Context) {
	_ = UpgradeYtDlp(ctx, "")
	ticker := time.NewTicker(ytdlpInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = UpgradeYtDlp(ctx, "")
		}
	}
}

// CheckNow reads the release feed and, when a newer release is found, downloads
// and stages it. It blocks for the duration of the check and the download, so
// callers that must not block (the HTTP handler) run it in a goroutine.
func (s *Service) CheckNow(ctx context.Context) State {
	if !s.opMu.TryLock() {
		return s.Status()
	}
	defer s.opMu.Unlock()
	if s.installing {
		return s.Status()
	}
	if s.opts.Repo == "" {
		return s.Status()
	}
	s.update(func(st *State) { st.Checking = true; st.Error = "" })

	rel, err := LatestRelease(ctx, s.opts.Repo)
	if err != nil {
		// A missing release feed is the normal state of a repo with no
		// releases yet, and an offline laptop is not a fault either. Record it
		// for the UI, but do not treat it as something the user must fix.
		log.Printf("updater: check failed: %v", err)
		s.update(func(st *State) { st.Checking = false; st.Error = err.Error() })
		return s.Status()
	}
	s.update(func(st *State) {
		st.Checking = false
		st.LastCheck = time.Now()
	})
	if !IsNewer(s.opts.CurrentVersion, rel.Tag) {
		s.update(func(st *State) { st.Available = ""; st.Notes = "" })
		s.setAwaitingAsset(false)
		return s.Status()
	}
	s.update(func(st *State) { st.Available = rel.Tag; st.Notes = rel.Body })

	if su, ok := ReadStaged(s.opts.DataDir); ok && su.Tag == rel.Tag {
		s.setAwaitingAsset(false)
		return s.Status() // already downloaded and waiting for a restart
	}
	s.setAwaitingAsset(PickAsset(rel, runtime.GOOS, runtime.GOARCH) == nil)
	s.stage(ctx, rel)
	return s.Status()
}

func (s *Service) setAwaitingAsset(v bool) {
	s.mu.Lock()
	s.awaitingAsset = v
	s.mu.Unlock()
}

// stage downloads the release asset for this platform and records it as the
// payload to apply on the next restart.
func (s *Service) stage(ctx context.Context, rel *Release) {
	asset := PickAsset(rel, runtime.GOOS, runtime.GOARCH)
	if asset == nil {
		s.update(func(st *State) {
			st.Error = "release " + rel.Tag + " has no build for " + runtime.GOOS + "/" + runtime.GOARCH +
				" yet; it will be checked again shortly"
		})
		return
	}
	s.update(func(st *State) { st.Downloading = true; st.Progress = 0; st.Error = "" })

	root := StagingDir(s.opts.DataDir)
	if err := os.MkdirAll(root, 0o755); err != nil {
		s.update(func(st *State) { st.Downloading = false; st.Error = err.Error() })
		return
	}
	dir, err := os.MkdirTemp(root, "payload-")
	if err != nil {
		s.update(func(st *State) { st.Downloading = false; st.Error = err.Error() })
		return
	}
	keep := false
	defer func() {
		if !keep {
			_ = os.RemoveAll(dir)
		}
	}()
	previous, hadPrevious := ReadStaged(s.opts.DataDir)
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	path, err := DownloadAsset(ctx, *asset, dir, func(f float64) {
		s.update(func(st *State) { st.Progress = f })
	})
	if err != nil {
		log.Printf("updater: download %s: %v", asset.Name, err)
		s.update(func(st *State) { st.Downloading = false; st.Progress = 0; st.Error = err.Error() })
		return
	}
	if err := verifyExecutable(path); err != nil {
		_ = os.Remove(path)
		s.update(func(st *State) { st.Downloading = false; st.Progress = 0; st.Error = err.Error() })
		return
	}
	sum, err := FileSHA256(path)
	if err == nil {
		err = WriteStaged(s.opts.DataDir, StagedUpdate{
			Tag: rel.Tag, Notes: rel.Body, File: path, SHA256: sum,
			GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
		})
	}
	if err != nil {
		s.update(func(st *State) { st.Downloading = false; st.Progress = 0; st.Error = err.Error() })
		return
	}
	log.Printf("updater: %s downloaded and ready to install on restart", rel.Tag)
	keep = true
	if hadPrevious {
		_ = os.Remove(previous.File)
	}
	s.update(func(st *State) {
		st.Downloading = false
		st.Progress = 1
		st.Staged = rel.Tag
	})
}

// InstallAndRestart swaps the staged payload over the running binary, spawns
// the new one and quits this instance. It returns an error without touching
// anything when no verified payload is staged.
func (s *Service) InstallAndRestart() error {
	if !s.opMu.TryLock() {
		return fmt.Errorf("an update operation is already in progress")
	}
	defer s.opMu.Unlock()
	if s.installing {
		return fmt.Errorf("the updated app is already starting")
	}
	if s.Status().Staged == "" {
		return errNothingStaged
	}
	backup, err := ApplyStaged(s.opts.DataDir, s.opts.ExePath)
	if err != nil {
		s.update(func(st *State) { st.Error = err.Error() })
		return err
	}
	if err := Relaunch(s.opts.DataDir, s.opts.ExePath, s.opts.Args...); err != nil {
		if restoreErr := restoreBackup(s.opts.ExePath, backup); restoreErr != nil {
			err = fmt.Errorf("%w; restore previous binary: %v", err, restoreErr)
		}
		s.update(func(st *State) { st.Error = err.Error() })
		return err
	}
	s.installing = true
	if s.opts.Quit != nil {
		// Let the HTTP response for this request reach the UI before the server
		// it came from goes away.
		go func() {
			time.Sleep(250 * time.Millisecond)
			s.opts.Quit()
		}()
	}
	return nil
}

// Dismiss clears the pending prompt for tag without discarding the download, so
// "Later" stops nagging until the next check finds something newer.
func (s *Service) Dismiss() {
	s.update(func(st *State) { st.Available = "" })
}

// update mutates the state under lock and publishes the result.
func (s *Service) update(fn func(*State)) {
	s.mu.Lock()
	fn(&s.state)
	next := s.state
	s.mu.Unlock()
	if s.opts.Bus != nil {
		s.opts.Bus.Publish(events.Event{Topic: TopicUpdateState, Payload: next})
	}
}
