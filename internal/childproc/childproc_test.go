package childproc_test

import (
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"testing"
	"time"

	"github.com/uhhhm/reverb/internal/childproc"
)

// The helper mode lets a test start a real, long-lived child of a known
// program: the pid checks and the shutdown escalation are about what the
// operating system reports, so a fake process would prove nothing.
const helperEnv = "REVERB_CHILDPROC_HELPER"

func TestMain(m *testing.M) {
	if os.Getenv(helperEnv) == "graceful" {
		stopped := make(chan os.Signal, 1)
		signal.Notify(stopped, childproc.ShutdownSignals()...)
		if ready := os.Getenv("REVERB_CHILDPROC_READY"); ready != "" {
			_ = os.WriteFile(ready, []byte("ready"), 0o600)
		}
		<-stopped
		if marker := os.Getenv("REVERB_CHILDPROC_STOPPED"); marker != "" {
			_ = os.WriteFile(marker, []byte("stopped"), 0o600)
		}
		os.Exit(0)
	}
	if os.Getenv(helperEnv) == "1" {
		time.Sleep(60 * time.Second)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func startHelper(t *testing.T) *exec.Cmd {
	t.Helper()
	cmd := childproc.LowPriorityCommand(os.Args[0])
	cmd.Env = append(os.Environ(), helperEnv+"=1")
	childproc.Detach(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start helper: %v", err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	t.Cleanup(func() {
		select {
		case <-exited:
			return
		default:
		}
		_ = childproc.Kill(cmd.Process.Pid)
		<-exited
	})
	return cmd
}

func waitGone(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if !childproc.Alive(pid) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("pid %d still alive", pid)
}

func TestAliveTracksTheProcessLifetime(t *testing.T) {
	cmd := startHelper(t)
	pid := cmd.Process.Pid
	if !childproc.Alive(pid) {
		t.Fatal("a running child must report alive")
	}
	if err := childproc.Terminate(cmd.Process.Pid); err != nil {
		t.Fatalf("Terminate: %v", err)
	}
	waitGone(t, pid)
}

// Escalation after a graceful request is ignored: Kill must end a process that
// did not respond to Terminate.
func TestKillEndsAnUnresponsiveChild(t *testing.T) {
	cmd := startHelper(t)
	pid := cmd.Process.Pid
	if err := childproc.Kill(cmd.Process.Pid); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	waitGone(t, pid)
}

// The guard that stops a stale pid file from aiming a signal at whatever
// process inherited the number.
func TestIsNamedDistinguishesAReusedPid(t *testing.T) {
	cmd := startHelper(t)
	pid := cmd.Process.Pid
	if !childproc.IsNamed(pid, filepath.Base(os.Args[0])) {
		t.Fatalf("pid %d runs %s but IsNamed said otherwise", pid, os.Args[0])
	}
	if childproc.IsNamed(pid, "reverb-not-this-program") {
		t.Fatal("IsNamed matched a program the pid is not running")
	}
}

func TestAliveIsFalseForAPidThatIsNotRunning(t *testing.T) {
	// Started and reaped, so the number is known not to name a live process
	// for as long as the OS has not handed it out again.
	cmd := startHelper(t)
	pid := cmd.Process.Pid
	_ = childproc.Kill(cmd.Process.Pid)
	waitGone(t, pid)
	if childproc.Alive(pid) {
		t.Fatal("Alive reported a process that has exited")
	}
	if childproc.IsNamed(pid, filepath.Base(os.Args[0])) {
		t.Fatal("IsNamed matched a dead pid")
	}
}
