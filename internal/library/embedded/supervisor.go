package embedded

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Process is a running child (test seam).
type Process interface{ Wait() error }

// Runner starts a child process with env and returns it.
type Runner func(ctx context.Context, env []string) (Process, error)

// Probe reports nil when the child is serving.
type Probe func(ctx context.Context) error

type Options struct {
	Mode         Mode
	Env          []string
	Runner       Runner
	Probe        Probe
	ProbeEvery   time.Duration
	RestartDelay time.Duration
	MaxRestarts  int
	// MinStableRun is how long a child must stay up before the run counts as
	// healthy enough to earn a fresh restart budget.
	MinStableRun time.Duration
}

type Supervisor struct {
	opts     Options
	mu       sync.Mutex
	health   Health
	sawReady bool
	cancel   context.CancelFunc
	done     chan struct{}
	started  bool
}

func New(o Options) *Supervisor {
	if o.ProbeEvery == 0 {
		o.ProbeEvery = 500 * time.Millisecond
	}
	if o.RestartDelay == 0 {
		o.RestartDelay = time.Second
	}
	if o.MaxRestarts == 0 {
		o.MaxRestarts = 5
	}
	if o.MinStableRun == 0 {
		o.MinStableRun = 15 * time.Second
	}
	h := HealthStarting
	if o.Mode != ModeBuiltIn {
		h = HealthExternal
	}
	return &Supervisor{opts: o, health: h, done: make(chan struct{})}
}

func (s *Supervisor) Health() Health { s.mu.Lock(); defer s.mu.Unlock(); return s.health }
func (s *Supervisor) Ready() bool    { return s.Health() == HealthReady }

func (s *Supervisor) setHealth(h Health) { s.mu.Lock(); s.health = h; s.mu.Unlock() }

// Start launches the supervise loop. No-op (beyond external health) when not built-in.
func (s *Supervisor) Start() {
	s.mu.Lock()
	s.started = true
	s.mu.Unlock()
	if s.opts.Mode != ModeBuiltIn {
		close(s.done)
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	go s.supervise(ctx)
}

func (s *Supervisor) supervise(ctx context.Context) {
	defer close(s.done)
	restarts := 0
	var ranFor time.Duration
	for {
		ranFor = 0
		proc, err := s.opts.Runner(ctx, s.opts.Env)
		if err != nil {
			log.Printf("navidrome: start failed: %v", err)
		} else {
			s.mu.Lock()
			s.sawReady = false
			s.mu.Unlock()
			readyCtx, stopReady := context.WithCancel(ctx)
			go s.waitReady(readyCtx)
			startedAt := time.Now()
			werr := proc.Wait()
			ranFor = time.Since(startedAt)
			stopReady()
			if ctx.Err() != nil {
				return // shutting down
			}
			log.Printf("navidrome: exited after %s: %v", ranFor.Round(time.Millisecond), werr)
		}
		if ctx.Err() != nil {
			return
		}
		s.mu.Lock()
		hadReady := s.sawReady
		s.mu.Unlock()
		// The probe only asks whether the port answers, and after a force-quit
		// the port may be answered by an orphaned navidrome from the previous
		// run. Our own child then fails to bind and exits at once while the
		// probe reports ready. Requiring the run to have lasted as well is what
		// separates a healthy instance that crashed -- fresh budget -- from a
		// child that never really started, which must spend the budget and end
		// in degraded rather than respawning forever.
		if hadReady && ranFor >= s.opts.MinStableRun {
			restarts = 0 // a previously-healthy instance crashed: fresh budget
		} else {
			if hadReady {
				log.Printf("navidrome: probe reported ready but the child exited after %s — "+
					"another navidrome may already be serving that port", ranFor.Round(time.Millisecond))
			}
			restarts++
		}
		if restarts >= s.opts.MaxRestarts {
			s.setHealth(HealthDegraded)
			log.Printf("navidrome: %d consecutive failures — degraded; stopping restarts", restarts)
			return
		}
		s.setHealth(HealthStarting)
		select {
		case <-ctx.Done():
			return
		case <-time.After(s.opts.RestartDelay * time.Duration(restarts+1)):
		}
	}
}

func (s *Supervisor) waitReady(ctx context.Context) {
	t := time.NewTicker(s.opts.ProbeEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := s.opts.Probe(ctx); err == nil {
				s.mu.Lock()
				if s.health != HealthDegraded {
					s.health = HealthReady
				}
				s.sawReady = true
				s.mu.Unlock()
				return
			}
		}
	}
}

// Shutdown cancels the supervise loop (which SIGTERMs the child via ExecRunner's
// cmd.Cancel) and waits for it to exit, or until ctx is done.
func (s *Supervisor) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	started := s.started
	s.mu.Unlock()
	if !started {
		// Never started, so there is nothing to wind down — and s.done is only
		// closed by Start/supervise, so waiting on it here would block until ctx
		// expires (15s of dead time on a quit that happens before startup).
		return nil
	}
	if s.cancel != nil {
		s.cancel()
	}
	select {
	case <-s.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// ExecRunner runs the real navidrome binary. Context cancel sends SIGTERM (via
// cmd.Cancel), then SIGKILL after WaitDelay — a graceful child shutdown.
//
// pidPath records the child so the next run can find it. A force-quit of the
// desktop app kills the parent without unwinding anything, and the child keeps
// the fixed navidrome port; the relaunched app then probes that orphan, sees a
// healthy port, and serves a library out of the stale process while its own
// child fails to bind. Reaping before the start is what makes a relaunch after
// a hard kill behave like a normal one. An empty pidPath disables both halves.
func ExecRunner(binaryPath, pidPath string) Runner {
	return func(ctx context.Context, env []string) (Process, error) {
		reapOrphan(pidPath, binaryPath)
		cmd := exec.CommandContext(ctx, binaryPath)
		cmd.Env = env
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
		cmd.WaitDelay = 10 * time.Second
		if err := cmd.Start(); err != nil {
			return nil, err
		}
		writePidFile(pidPath, cmd.Process.Pid)
		return execProcess{cmd}, nil
	}
}

func writePidFile(pidPath string, pid int) {
	if pidPath == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(pidPath), 0o755); err != nil {
		log.Printf("navidrome: pid file dir: %v", err)
		return
	}
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(pid)), 0o644); err != nil {
		log.Printf("navidrome: write pid file: %v", err)
	}
}

// reapOrphan terminates the navidrome recorded in pidPath if it is still
// running. The pid is checked against the binary's own name first: pids are
// reused, and a stale file must never let Reverb signal a process that merely
// inherited the number.
func reapOrphan(pidPath, binaryPath string) {
	if pidPath == "" {
		return
	}
	raw, err := os.ReadFile(pidPath)
	if err != nil {
		return
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil || pid <= 1 {
		_ = os.Remove(pidPath)
		return
	}
	proc, err := os.FindProcess(pid)
	if err != nil || proc.Signal(syscall.Signal(0)) != nil {
		_ = os.Remove(pidPath) // already gone
		return
	}
	if !processIsNamed(pid, filepath.Base(binaryPath)) {
		_ = os.Remove(pidPath)
		return
	}
	log.Printf("navidrome: reaping orphaned instance (pid %d) left by a previous run", pid)
	_ = proc.Signal(syscall.SIGTERM)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if proc.Signal(syscall.Signal(0)) != nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if proc.Signal(syscall.Signal(0)) == nil {
		_ = proc.Signal(syscall.SIGKILL)
	}
	_ = os.Remove(pidPath)
}

// processIsNamed reports whether pid's command name matches want.
func processIsNamed(pid int, want string) bool {
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "comm=").Output()
	if err != nil {
		return false
	}
	return filepath.Base(strings.TrimSpace(string(out))) == want
}

type execProcess struct{ cmd *exec.Cmd }

func (p execProcess) Wait() error { return p.cmd.Wait() }

// PingProbe returns a Probe that hits the Subsonic ping endpoint (auth omitted —
// any HTTP response means the server is up and accepting connections).
func PingProbe(baseURL string, hc *http.Client) Probe {
	if hc == nil {
		hc = &http.Client{Timeout: 3 * time.Second}
	}
	return func(ctx context.Context) error {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/rest/ping", nil)
		if err != nil {
			return err
		}
		resp, err := hc.Do(req)
		if err != nil {
			return err
		}
		resp.Body.Close()
		return nil
	}
}
