//go:build !windows

package childproc

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// hide is a no-op: starting a process on a unix system does not create a
// window of any kind.
func hide(*exec.Cmd) {}

// Unix signals do not depend on console membership, so a gracefully managed
// child uses the same process configuration as every other child.
func hideGracefully(cmd *exec.Cmd) { hide(cmd) }

// Unix orphan handling retains its existing pid-plus-name behavior. The
// stronger instance token is needed for Windows, where the ticket explicitly
// guards against signalling a reused pid.
func instanceIdentity(int) (string, error) { return "", nil }

func stopInstance(pid int, _ string, grace time.Duration) (bool, error) {
	if !Alive(pid) {
		return false, nil
	}
	_ = Terminate(pid)
	deadline := time.Now().Add(grace)
	for Alive(pid) && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if !Alive(pid) {
		return true, nil
	}
	if err := Kill(pid); err != nil {
		return true, fmt.Errorf("kill pid %d: %w", pid, err)
	}
	deadline = time.Now().Add(grace)
	for Alive(pid) && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if Alive(pid) {
		return true, fmt.Errorf("pid %d did not exit after kill", pid)
	}
	return true, nil
}

// Detach puts the child in its own session, which detaches it from the parent's
// controlling terminal. It then no longer receives the terminal's SIGHUP when
// the parent exits, and is not in the parent's process group, so a group signal
// aimed at the parent misses it.
func Detach(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setsid = true
}

// LowPriorityCommand runs exe under nice(1), which is present on both supported
// unix desktops. nice applies the niceness before exec, so it is in force
// before the Go runtime creates any thread and is inherited by every thread and
// descendant. Calling setpriority from Go instead would only lower the calling
// thread on Linux, where niceness is per-thread.
func LowPriorityCommand(exe string, args ...string) *exec.Cmd {
	return Command("nice", append([]string{"-n", "10", exe}, args...)...)
}

// Terminate sends SIGTERM, the conventional request to shut down cleanly.
func Terminate(pid int) error { return signal(pid, syscall.SIGTERM) }

// Kill sends SIGKILL, which the process cannot catch or ignore.
func Kill(pid int) error { return signal(pid, syscall.SIGKILL) }

func signal(pid int, sig syscall.Signal) error {
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return p.Signal(sig)
}

// Alive reports whether pid is a live process. Signal 0 runs the kernel's
// existence and permission checks without delivering anything.
func Alive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return p.Signal(syscall.Signal(0)) == nil
}

// IsNamed compares want against the command name ps reports for pid. Reading
// the name from the OS rather than trusting the pid file is what stops a reused
// pid from being signalled.
//
// Linux stores that name in a fixed 15-character field, so a program whose base
// name is longer is reported truncated and no name will ever match it. The
// answer is then a false negative — Reverb declines to signal — which is the
// safe direction, but it does mean a bundled tool must be named within that
// limit to be reapable. navidrome is.
func IsNamed(pid int, want string) bool {
	out, err := Command("ps", "-p", strconv.Itoa(pid), "-o", "comm=").Output()
	if err != nil {
		return false
	}
	return filepath.Base(strings.TrimSpace(string(out))) == want
}

// ShutdownSignals are the signals that mean "the host is asking this process to
// exit" and that a long-running headless process must handle rather than die on.
func ShutdownSignals() []os.Signal { return []os.Signal{os.Interrupt, syscall.SIGTERM} }
