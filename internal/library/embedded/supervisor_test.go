package embedded

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"testing"
	"time"
)

// fakeProcess returns from Wait when its ctx is canceled or crash is signaled.
type fakeProcess struct {
	ctx   context.Context
	crash chan struct{}
}

func (p *fakeProcess) Wait() error {
	select {
	case <-p.ctx.Done():
		return p.ctx.Err()
	case <-p.crash:
		return errors.New("crashed")
	}
}

func TestSupervisor_ExternalMode_RunsNothing(t *testing.T) {
	var started bool
	s := New(Options{
		Mode:   ModeExternal,
		Runner: func(ctx context.Context, _ []string) (Process, error) { started = true; return nil, nil },
		Probe:  func(context.Context) error { return nil },
	})
	s.Start()
	if started {
		t.Fatal("external mode must not start a child")
	}
	if s.Health() != HealthExternal {
		t.Errorf("health = %q, want external", s.Health())
	}
	if err := s.Shutdown(context.Background()); err != nil {
		t.Errorf("shutdown: %v", err)
	}
}

func TestSupervisor_BuiltIn_BecomesReadyThenShutsDown(t *testing.T) {
	var mu sync.Mutex
	var proc *fakeProcess
	s := New(Options{
		Mode: ModeBuiltIn,
		Runner: func(ctx context.Context, _ []string) (Process, error) {
			mu.Lock()
			proc = &fakeProcess{ctx: ctx, crash: make(chan struct{})}
			mu.Unlock()
			return proc, nil
		},
		Probe:      func(context.Context) error { return nil }, // immediately ready
		ProbeEvery: time.Millisecond,
	})
	s.Start()

	deadline := time.Now().Add(2 * time.Second)
	for !s.Ready() && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	if !s.Ready() {
		t.Fatalf("never became ready; health=%q", s.Health())
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := s.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
}

func TestSupervisor_CrashLoop_GoesDegraded(t *testing.T) {
	var starts int
	var mu sync.Mutex
	s := New(Options{
		Mode: ModeBuiltIn,
		Runner: func(ctx context.Context, _ []string) (Process, error) {
			mu.Lock()
			starts++
			mu.Unlock()
			// crash immediately: Wait returns at once
			crash := make(chan struct{})
			close(crash)
			return &fakeProcess{ctx: ctx, crash: crash}, nil
		},
		Probe:        func(context.Context) error { return errors.New("never ready") },
		ProbeEvery:   time.Millisecond,
		RestartDelay: time.Millisecond,
		MaxRestarts:  3,
	})
	s.Start()

	deadline := time.Now().Add(2 * time.Second)
	for s.Health() != HealthDegraded && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	if s.Health() != HealthDegraded {
		t.Fatalf("health = %q, want degraded", s.Health())
	}
	mu.Lock()
	got := starts
	mu.Unlock()
	if got != 3 {
		t.Errorf("runner started %d times, want 3 (MaxRestarts)", got)
	}
	if err := s.Shutdown(context.Background()); err != nil {
		t.Errorf("shutdown after degraded: %v", err)
	}
}

// TestShutdownBeforeStartReturnsImmediately guards against a quit that happens
// before startup stalling for the caller's whole timeout: done is only closed by
// Start/supervise, so an unstarted supervisor used to block until ctx expired.
func TestShutdownBeforeStartReturnsImmediately(t *testing.T) {
	s := New(Options{Mode: ModeBuiltIn})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	start := time.Now()
	if err := s.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown before Start: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 250*time.Millisecond {
		t.Errorf("Shutdown before Start took %v, want it to return immediately", elapsed)
	}
}

// A force-quit can leave the previous navidrome running on its fixed port. The
// probe only asks whether the port answers, so it reports ready while our own
// child fails to bind and exits at once. Treating that as a healthy run resets
// the restart budget, and the supervisor respawns forever. A run that ends
// immediately is not healthy, whatever the port says.
func TestSupervisor_ReadyButInstantExit_GoesDegraded(t *testing.T) {
	var starts int
	var mu sync.Mutex
	s := New(Options{
		Mode: ModeBuiltIn,
		Runner: func(ctx context.Context, _ []string) (Process, error) {
			mu.Lock()
			starts++
			mu.Unlock()
			crash := make(chan struct{})
			close(crash)
			return &fakeProcess{ctx: ctx, crash: crash}, nil
		},
		// An orphan on the port answers every probe.
		Probe:        func(context.Context) error { return nil },
		ProbeEvery:   time.Millisecond,
		RestartDelay: time.Millisecond,
		MaxRestarts:  3,
		MinStableRun: time.Hour,
	})
	s.Start()

	deadline := time.Now().Add(2 * time.Second)
	for s.Health() != HealthDegraded && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	if s.Health() != HealthDegraded {
		t.Fatalf("health = %q, want degraded: the restart budget never ran out", s.Health())
	}
	mu.Lock()
	got := starts
	mu.Unlock()
	if got > 3 {
		t.Errorf("runner started %d times, want at most 3 (MaxRestarts)", got)
	}
	if err := s.Shutdown(context.Background()); err != nil {
		t.Errorf("shutdown after degraded: %v", err)
	}
}

// A child that ran healthily for a long stretch and then crashed still earns a
// fresh budget -- that is an ordinary crash, not a startup failure.
func TestSupervisor_LongHealthyRun_ResetsBudget(t *testing.T) {
	crash := make(chan struct{})
	var starts int
	var mu sync.Mutex
	s := New(Options{
		Mode: ModeBuiltIn,
		Runner: func(ctx context.Context, _ []string) (Process, error) {
			mu.Lock()
			starts++
			mu.Unlock()
			return &fakeProcess{ctx: ctx, crash: crash}, nil
		},
		Probe:        func(context.Context) error { return nil },
		ProbeEvery:   time.Millisecond,
		RestartDelay: time.Millisecond,
		MaxRestarts:  2,
		MinStableRun: 20 * time.Millisecond,
	})
	s.Start()

	deadline := time.Now().Add(2 * time.Second)
	for !s.Ready() && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	time.Sleep(40 * time.Millisecond) // outlive MinStableRun
	close(crash)                      // the healthy child dies

	deadline = time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		got := starts
		mu.Unlock()
		if got >= 2 {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	if s.Health() == HealthDegraded {
		t.Fatalf("health = degraded: a long healthy run must not spend the restart budget")
	}
	if err := s.Shutdown(context.Background()); err != nil {
		t.Errorf("shutdown: %v", err)
	}
}

// A force-quit leaves navidrome holding its fixed port. The next run reaps it
// from the pid file before starting its own child.
func TestReapOrphanKillsRecordedProcess(t *testing.T) {
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Skipf("cannot start helper process: %v", err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()

	pidPath := filepath.Join(t.TempDir(), "navidrome.pid")
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(cmd.Process.Pid)), 0o644); err != nil {
		t.Fatal(err)
	}

	reapOrphan(pidPath, "/opt/tools/sleep")

	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("orphan still running: it will keep holding the navidrome port")
	}
	if _, err := os.Stat(pidPath); !os.IsNotExist(err) {
		t.Errorf("pid file still present after reaping: %v", err)
	}
}

// Pids are reused. A stale file must never let Reverb signal whatever process
// happens to hold that number now.
func TestReapOrphanSparesUnrelatedProcess(t *testing.T) {
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Skipf("cannot start helper process: %v", err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()

	pidPath := filepath.Join(t.TempDir(), "navidrome.pid")
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(cmd.Process.Pid)), 0o644); err != nil {
		t.Fatal(err)
	}

	reapOrphan(pidPath, "/opt/tools/navidrome")

	time.Sleep(200 * time.Millisecond)
	if err := cmd.Process.Signal(syscall.Signal(0)); err != nil {
		t.Fatalf("unrelated process was killed: %v", err)
	}
}
