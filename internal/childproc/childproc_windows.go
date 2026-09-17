//go:build windows

package childproc

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	"golang.org/x/sys/windows"
)

// hide keeps a console child off the screen and makes it individually
// addressable.
//
// CREATE_NO_WINDOW is what stops a terminal from flashing when the app starts
// ffmpeg or Navidrome: the child still gets a console, but an invisible one.
// That console is also the only route Terminate has, since console control
// events are the sole orderly-shutdown notification Windows offers. It has no
// effect on a GUI-subsystem child, which gets no console at all — see
// Terminate for what that costs.
//
// CREATE_NEW_PROCESS_GROUP makes the child the leader of a group of its own,
// so its group id is its pid. Terminate needs that to aim a control event at
// one child instead of at every process sharing the console, this one
// included. It also insulates the child from events aimed at Reverb's group,
// which is what Reverb wants: it stops its children deliberately.
//
// stdio is untouched. The child's handles are still whatever the caller set,
// so pipes attached for progress parsing keep working.
func hide(cmd *exec.Cmd) {
	attr := sysProcAttr(cmd)
	attr.HideWindow = true
	attr.CreationFlags |= windows.CREATE_NO_WINDOW | windows.CREATE_NEW_PROCESS_GROUP
}

func sysProcAttr(cmd *exec.Cmd) *syscall.SysProcAttr {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	return cmd.SysProcAttr
}

// Detach has nothing to do. Windows does not tie a process's lifetime to its
// parent's: there is no session to leave, no controlling terminal, and no
// hangup delivered when the parent exits. The process group a detached child
// needs is already set by Command.
//
// DETACHED_PROCESS exists and would also survive, but it is mutually exclusive
// with CREATE_NO_WINDOW and would leave the child with no console at all, so
// the child would lose the one channel Terminate can reach it through.
func Detach(*exec.Cmd) {}

// LowPriorityCommand sets the priority class at creation time, so it is in
// force before the child's runtime starts any thread. A priority class applies
// to the whole process and is inherited by the processes it goes on to create,
// which is what keeps spotDL's own children out of the way too.
func LowPriorityCommand(exe string, args ...string) *exec.Cmd {
	cmd := Command(exe, args...)
	sysProcAttr(cmd).CreationFlags |= windows.BELOW_NORMAL_PRIORITY_CLASS
	return cmd
}

// Windows console management that golang.org/x/sys/windows does not wrap.
// Only the handful of calls Terminate needs are bound here.
var (
	kernel32          = windows.NewLazySystemDLL("kernel32.dll")
	procFreeConsole   = kernel32.NewProc("FreeConsole")
	procAttachConsole = kernel32.NewProc("AttachConsole")
)

func freeConsole() error {
	if r, _, err := procFreeConsole.Call(); r == 0 {
		return err
	}
	return nil
}

func attachConsole(pid uint32) error {
	if r, _, err := procAttachConsole.Call(uintptr(pid)); r == 0 {
		return err
	}
	return nil
}

// stillActive is the exit code Windows reports for a process that has not
// exited (STILL_ACTIVE / STATUS_PENDING).
const stillActive = 259

// consoleMu serialises the console juggling Terminate performs: console
// attachment is per-process state, so two concurrent terminations would
// interleave and signal each other's target.
var consoleMu sync.Mutex

// Terminate delivers CTRL_BREAK to the target's process group, which a Go
// program — Reverb's background runtime, and Navidrome — surfaces as an
// interrupt and can unwind on. It is the closest Windows equivalent of SIGTERM
// for a process with no window, and it does not block: the event is posted,
// not waited on.
//
// Reaching the target means borrowing its console, which only a process with
// no console of its own may do. That is the GUI build, which is what ships.
// The event is then aimed at the target's own group rather than at the whole
// console, which is what keeps it off this process: masking would not do
// instead, since SetConsoleCtrlHandler(nil, true) suppresses CTRL+C and never
// CTRL+BREAK. The console is given back immediately, so this process ends up
// where it started.
//
// Two targets cannot be asked at all: one with no console — every
// GUI-subsystem image, Reverb's own background runtime included — and any
// target at all when the caller holds a console of its own, since Windows will
// not let it borrow a second one. Only those two end in Kill, because Windows
// offers no other way to say "please exit" to them and the caller could only
// answer the failure by killing anyway. Declining to free the caller's own
// console to get around the second case is deliberate: it could never be
// restored, and detaching a terminal the household is looking at is a worse
// outcome than a hard stop.
//
// Any other failure is reported rather than escalated. Navidrome corrupts its
// index when it is cut off mid-write, so an unexplained error must leave the
// caller's grace period intact instead of spending it.
//
// The background runtime's ordinary stop is not this. It is the control
// channel, which shuts the database and the bundled library down cleanly and
// is what every caller tries first.
func Terminate(pid int) error {
	consoleMu.Lock()
	defer consoleMu.Unlock()

	if err := attachConsole(uint32(pid)); err != nil {
		if errors.Is(err, windows.ERROR_INVALID_HANDLE) || errors.Is(err, windows.ERROR_ACCESS_DENIED) {
			return Kill(pid)
		}
		return fmt.Errorf("attach to console of pid %d: %w", pid, err)
	}
	defer func() { _ = freeConsole() }()
	// Command makes every child a group leader, so the group id is the pid.
	return windows.GenerateConsoleCtrlEvent(windows.CTRL_BREAK_EVENT, uint32(pid))
}

// Kill ends the process outright. TerminateProcess gives the target no chance
// to run any more code, which is exactly the escalation the callers want after
// Terminate's grace period has run out.
func Kill(pid int) error {
	h, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		return err
	}
	defer windows.CloseHandle(h)
	return windows.TerminateProcess(h, 1)
}

// Alive asks for the process's exit code without disturbing it. A handle that
// cannot be opened means the pid names nothing this user can see, and
// STILL_ACTIVE means it has not exited.
//
// A process that has exited but whose handle is still held by someone reports
// its real exit code here, so a reaped-but-not-yet-recycled pid is correctly
// reported dead — unless it exited with 259, which is indistinguishable from
// "still running". Windows gives no way to tell the two apart, and the callers
// use Alive to decide whether to keep waiting, so the ambiguity costs a grace
// period rather than correctness.
func Alive(pid int) bool {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return false
	}
	return code == stillActive
}

// IsNamed compares want against the base name of the image the pid is actually
// running, read from the kernel rather than from whatever wrote the pid file.
// Windows reports the full path with its extension, so a caller naming a tool
// without one still matches: "navidrome" and "navidrome.exe" are the same
// program here, and nothing else is.
func IsNamed(pid int, want string) bool {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)
	buf := make([]uint16, windows.MAX_LONG_PATH)
	size := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(h, 0, &buf[0], &size); err != nil {
		return false
	}
	got := filepath.Base(windows.UTF16ToString(buf[:size]))
	return strings.EqualFold(got, want) ||
		strings.EqualFold(strings.TrimSuffix(got, filepath.Ext(got)), want)
}

// ShutdownSignals: os.Interrupt is how the Go runtime surfaces both console
// control events (CTRL_C and the CTRL_BREAK that Terminate sends) to a
// signal.Notify channel. Windows has nothing else that means "please exit".
func ShutdownSignals() []os.Signal { return []os.Signal{os.Interrupt} }
