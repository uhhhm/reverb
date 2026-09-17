//go:build !windows

package childproc

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// hide is a no-op: starting a process on a unix system does not create a
// window of any kind.
func hide(*exec.Cmd) {}

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
