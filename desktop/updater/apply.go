package updater

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// backupSuffix marks the outgoing binary. It is kept until the successor has
// started, so a failed swap can be rolled back and a failed launch is still
// recoverable by hand.
const backupSuffix = ".old"

// relaunchMarker names the file the outgoing instance writes with its own PID.
// The successor waits for that PID to exit before it opens the database or
// starts the bundled Navidrome, which listens on a fixed port and would
// otherwise collide with the instance still shutting down.
func relaunchMarker(dataDir string) string { return filepath.Join(StagingDir(dataDir), "relaunch") }

// ApplyStaged swaps the staged payload over exePath. The running process keeps
// executing the old inode — on macOS and Linux a running executable can be
// renamed out from under itself — so this is safe to call before shutdown.
// On any failure the original binary is restored.
func ApplyStaged(dataDir, exePath string) error {
	su, ok := ReadStaged(dataDir)
	if !ok {
		return fmt.Errorf("no verified update is staged")
	}
	if err := verifyExecutable(su.File); err != nil {
		return err
	}
	exePath, err := filepath.EvalSymlinks(exePath)
	if err != nil {
		return err
	}

	// Stage the replacement beside the target first: a cross-device rename
	// fails, and the copy must land on the same filesystem as the binary.
	next := exePath + ".new"
	if err := copyFile(su.File, next, 0o755); err != nil {
		return err
	}
	backup := exePath + backupSuffix
	_ = os.Remove(backup)
	if err := os.Rename(exePath, backup); err != nil {
		_ = os.Remove(next)
		return err
	}
	if err := os.Rename(next, exePath); err != nil {
		// Put the working binary back before giving up.
		_ = os.Rename(backup, exePath)
		_ = os.Remove(next)
		return err
	}
	return nil
}

// Relaunch starts the updated binary and returns once it has been spawned. The
// caller is expected to quit immediately afterwards so the successor — which
// waits on the marker this writes — can take over.
func Relaunch(dataDir, exePath string) error {
	if err := os.MkdirAll(StagingDir(dataDir), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(relaunchMarker(dataDir), []byte(strconv.Itoa(os.Getpid())), 0o644); err != nil {
		return err
	}
	var cmd *exec.Cmd
	if bundle := macAppBundle(exePath); bundle != "" {
		// Launching the bundle rather than the executable keeps the Dock icon,
		// the app name and the activation behaviour macOS attaches to it.
		cmd = exec.Command("open", "-n", bundle)
	} else {
		cmd = exec.Command(exePath)
	}
	cmd.Env = os.Environ()
	cmd.Dir = filepath.Dir(exePath)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		_ = os.Remove(relaunchMarker(dataDir))
		return err
	}
	// Nothing waits on the child; releasing it avoids a zombie in the brief
	// window before this process exits.
	go func() { _ = cmd.Wait() }()
	return nil
}

// macAppBundle returns the .app directory exePath lives in, or "" when the
// binary is not inside a bundle.
func macAppBundle(exePath string) string {
	if runtime.GOOS != "darwin" {
		return ""
	}
	// <bundle>.app/Contents/MacOS/<binary>
	dir := filepath.Dir(exePath)
	if filepath.Base(dir) != "MacOS" {
		return ""
	}
	contents := filepath.Dir(dir)
	if filepath.Base(contents) != "Contents" {
		return ""
	}
	bundle := filepath.Dir(contents)
	if !strings.HasSuffix(bundle, ".app") {
		return ""
	}
	return bundle
}

// WaitForPredecessor blocks until the instance that relaunched this one has
// exited, or until timeout. It is a no-op when this process was started
// normally. Call it at boot, before anything opens the database or binds a
// port.
func WaitForPredecessor(dataDir string, timeout time.Duration) {
	marker := relaunchMarker(dataDir)
	b, err := os.ReadFile(marker)
	if err != nil {
		return
	}
	defer func() { _ = os.Remove(marker) }()
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || pid <= 0 || pid == os.Getpid() {
		return
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !processAlive(pid) {
			// Give the predecessor's listeners and child processes a moment to
			// be reaped after the process itself is gone.
			time.Sleep(300 * time.Millisecond)
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// processAlive reports whether pid is a live process. Signal 0 performs the
// permission and existence checks without delivering anything.
func processAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return p.Signal(syscall.Signal(0)) == nil
}

// CleanupAfterUpdate discards the previous binary and the staged payload once
// the updated build is running. currentVersion is this build's version: the
// staged manifest is only cleared when it names the version now running, so an
// update staged for a *newer* release than this one survives.
func CleanupAfterUpdate(dataDir, exePath, currentVersion string) {
	if resolved, err := filepath.EvalSymlinks(exePath); err == nil {
		_ = os.Remove(resolved + backupSuffix)
		_ = os.Remove(resolved + ".new")
	}
	su, ok := ReadStaged(dataDir)
	if !ok {
		// A manifest that no longer verifies is dead weight; drop it and any
		// partial downloads beside it.
		_ = os.Remove(manifestPath(dataDir))
		return
	}
	if !IsNewer(currentVersion, su.Tag) {
		_ = ClearStaged(dataDir)
	}
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		_ = os.Remove(dst)
		return err
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(dst)
		return err
	}
	return os.Chmod(dst, mode)
}
