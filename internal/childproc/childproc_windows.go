//go:build windows

package childproc

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
)

// hide keeps a console child off the screen and makes it individually
// addressable.
//
// CREATE_NO_WINDOW is what stops a terminal from flashing when the app starts
// a one-shot tool such as ffmpeg. The child has no console at all, so it cannot
// receive the console control event Terminate uses; long-lived children that
// need orderly shutdown are created by hideGracefully instead.
//
// CREATE_NEW_PROCESS_GROUP insulates the child from console control events
// aimed at Reverb's own group, which is what Reverb wants: it stops its
// children deliberately. It buys nothing for shutdown here — a child with no
// console handle is unreachable by a control event however it is aimed — so a
// child that must be asked to stop is created by hideGracefully instead.
//
// stdio is untouched. The child's handles are still whatever the caller set,
// so pipes attached for progress parsing keep working.
func hide(cmd *exec.Cmd) {
	attr := sysProcAttr(cmd)
	attr.HideWindow = true
	attr.CreationFlags |= windows.CREATE_NO_WINDOW | windows.CREATE_NEW_PROCESS_GROUP
}

// hideGracefully gives a console-capable child its own hidden console, keeping
// it out of sight without removing its console control channel. The explicit
// console matters in production: Reverb is a GUI-subsystem process and has no
// parent console for Navidrome to inherit. Terminate attaches to that private
// console and broadcasts Ctrl-Break only to its members. CREATE_NEW_PROCESS_GROUP
// is deliberately absent because Windows ignores it with CREATE_NEW_CONSOLE.
//
// CREATE_NO_WINDOW is deliberately absent: Microsoft documents that it leaves
// the application with no console handle, at which point AttachConsole cannot
// reach it and graceful shutdown is impossible.
func hideGracefully(cmd *exec.Cmd) {
	attr := sysProcAttr(cmd)
	attr.HideWindow = true
	attr.CreationFlags |= windows.CREATE_NEW_CONSOLE
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
	kernel32           = windows.NewLazySystemDLL("kernel32.dll")
	procFreeConsole    = kernel32.NewProc("FreeConsole")
	procAttachConsole  = kernel32.NewProc("AttachConsole")
	procSetCtrlHandler = kernel32.NewProc("SetConsoleCtrlHandler")
)

var (
	ctrlBreakObserved atomic.Bool
	ctrlBreakHandler  = syscall.NewCallback(func(event uint32) uintptr {
		if event == windows.CTRL_BREAK_EVENT {
			ctrlBreakObserved.Store(true)
			return 1
		}
		return 0
	})
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

func setConsoleCtrlHandler(add bool) error {
	value := uintptr(0)
	if add {
		value = 1
	}
	if r, _, err := procSetCtrlHandler.Call(ctrlBreakHandler, value); r == 0 {
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

// Terminate delivers CTRL_BREAK to the target, which a Go program — Reverb's
// background runtime, and Navidrome — surfaces as an interrupt and can unwind
// on. It is the closest Windows equivalent of SIGTERM for a process with no
// window. It does not wait for the target to exit, but neither does it return
// at once: it holds the console it borrowed for as long as the delivery below
// takes, so the caller's grace period effectively begins after it returns.
//
// Reaching the target means borrowing its console, which only a process with
// no console of its own may do. That is the GUI build, which is what ships.
// Graceful children own a private console. The event is broadcast to that
// console (group zero); a temporary last-installed handler absorbs the copy
// delivered to this process, while the child receives its normal Ctrl-Break.
// The console is given back once that handler has seen the event, or after a
// short wait when it has not — and always before the handler is uninstalled,
// since leaving the console is what actually stops a late copy arriving.
//
// Two targets cannot be asked at all: one with no console — every child made
// by Command/CommandContext and every GUI-subsystem image, including Reverb's
// own background runtime — and any target at all when the caller holds a
// console of its own, since Windows will not let it borrow a second one. Only
// those two end in Kill, because Windows offers no other way to say "please
// exit" to them and the caller could only answer the failure by killing
// anyway. Declining to free the caller's own console to get around the second
// case is deliberate: it could never be restored, and detaching a terminal the
// household is looking at is a worse outcome than a hard stop.
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
	ctrlBreakObserved.Store(false)
	if err := setConsoleCtrlHandler(true); err != nil {
		_ = freeConsole()
		return fmt.Errorf("install console control handler: %w", err)
	}
	// Order matters: the console is given back before the handler is
	// uninstalled. The event is asynchronous: a copy that lands after the
	// handler is gone but while this process is still a console member would
	// fall through to the Go runtime and interrupt Reverb itself.
	defer func() {
		_ = freeConsole()
		_ = setConsoleCtrlHandler(false)
	}()
	if err := windows.GenerateConsoleCtrlEvent(windows.CTRL_BREAK_EVENT, 0); err != nil {
		return err
	}
	deadline := time.Now().Add(time.Second)
	for !ctrlBreakObserved.Load() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	return nil
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
	got, err := processImageName(pid)
	if err != nil {
		return false
	}
	got = filepath.Base(got)
	return strings.EqualFold(got, want) ||
		strings.EqualFold(strings.TrimSuffix(got, filepath.Ext(got)), want)
}

func instanceIdentity(pid int) (string, error) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(h)
	return instanceIdentityFromHandle(h)
}

func instanceIdentityFromHandle(h windows.Handle) (string, error) {
	var created, exited, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(h, &created, &exited, &kernel, &user); err != nil {
		return "", err
	}
	path, err := processImageNameFromHandle(h)
	if err != nil {
		return "", err
	}
	ticks := uint64(created.HighDateTime)<<32 | uint64(created.LowDateTime)
	return strings.ToLower(filepath.Clean(path)) + "|" + strconv.FormatUint(ticks, 10), nil
}

func stopInstance(pid int, expected string, grace time.Duration) (bool, error) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
			return false, nil
		}
		return false, err
	}
	defer windows.CloseHandle(h)
	identity, err := instanceIdentityFromHandle(h)
	if err != nil {
		return false, err
	}
	if expected == "" || identity != expected {
		return false, nil
	}
	// Even if the orderly request cannot be delivered, keep the stable handle
	// through the grace period and hard-kill that same process instance.
	_ = Terminate(pid)
	exited, err := waitForHandleExit(h, grace)
	if err != nil {
		return true, err
	}
	if exited {
		return true, nil
	}
	if err := windows.TerminateProcess(h, 1); err != nil {
		return true, err
	}
	exited, err = waitForHandleExit(h, grace)
	if err != nil {
		return true, err
	}
	if !exited {
		return true, fmt.Errorf("pid %d did not exit after kill", pid)
	}
	return true, nil
}

func waitForHandleExit(h windows.Handle, timeout time.Duration) (bool, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var code uint32
		if err := windows.GetExitCodeProcess(h, &code); err != nil {
			return false, err
		}
		if code != stillActive {
			return true, nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return false, err
	}
	return code != stillActive, nil
}

func processImageName(pid int) (string, error) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(h)
	return processImageNameFromHandle(h)
}

func processImageNameFromHandle(h windows.Handle) (string, error) {
	buf := make([]uint16, windows.MAX_LONG_PATH)
	size := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(h, 0, &buf[0], &size); err != nil {
		return "", err
	}
	return windows.UTF16ToString(buf[:size]), nil
}

// ShutdownSignals: os.Interrupt is how the Go runtime surfaces both console
// control events (CTRL_C and the CTRL_BREAK that Terminate sends) to a
// signal.Notify channel. Windows has nothing else that means "please exit".
func ShutdownSignals() []os.Signal { return []os.Signal{os.Interrupt} }
