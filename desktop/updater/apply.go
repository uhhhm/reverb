package updater

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/uhhhm/reverb/internal/childproc"
)

// Installing an update touches three things the operating system has opinions
// about, so each is a per-OS seam (platform_darwin.go, platform_unix.go). A
// port must supply all three:
//
//   - backupPath says where the outgoing binary is kept until the successor has
//     started. It must be somewhere a rename can reach without crossing a
//     filesystem, and somewhere that deleting the file afterwards cannot damage
//     the installed application — on macOS that means outside the code-signed
//     .app.
//   - resealInstalled re-establishes whatever the OS demands before it will run
//     the replaced binary. Returning an error here is taken to mean the install
//     is unusable and triggers a rollback, so a port that has nothing to do must
//     return nil rather than guess.
//   - relaunchCommand builds the command that starts the successor, and reports
//     whether it must be waited on. waitForExit is for hosts where the command
//     merely hands the application to something else and exits: its status is
//     then the only signal that the launch failed, which has to reach the caller
//     before it quits. A command that *is* the successor must report false —
//     waiting on it would hang until the new instance exits.

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
// executing the outgoing file, which is renamed rather than overwritten, so
// this is safe to call before shutdown. A port to an OS that will not let a
// running executable be renamed has to change that ordering, not just this
// function. On any failure the original binary is restored.
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
	backup := backupPath(exePath)
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
	if err := resealInstalled(exePath); err != nil {
		if restoreErr := restoreBackup(exePath); restoreErr != nil {
			return fmt.Errorf("%w; rollback: %v", err, restoreErr)
		}
		return err
	}
	return nil
}

// Relaunch starts the updated binary and returns once it has been spawned. The
// caller is expected to quit immediately afterwards so the successor — which
// waits on the marker this writes — can take over.
func Relaunch(dataDir, exePath string, args ...string) error {
	if err := os.MkdirAll(StagingDir(dataDir), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(relaunchMarker(dataDir), []byte(strconv.Itoa(os.Getpid())), 0o644); err != nil {
		return err
	}
	cmd, waitForExit := relaunchCommand(exePath, args)
	// Environment and working directory are both inherited: relative --db and
	// other paths must keep naming the same files after the restart.
	cmd.Env = os.Environ()
	// The successor has to survive this process quitting moments from now.
	childproc.Detach(cmd)
	if waitForExit {
		if err := cmd.Run(); err != nil {
			_ = os.Remove(relaunchMarker(dataDir))
			return err
		}
		return nil
	}
	if err := cmd.Start(); err != nil {
		_ = os.Remove(relaunchMarker(dataDir))
		return err
	}
	// Nothing waits on the child; releasing it avoids a zombie in the brief
	// window before this process exits.
	go func() { _ = cmd.Wait() }()
	return nil
}

func restoreBackup(exePath string) error {
	path, err := filepath.EvalSymlinks(exePath)
	if err != nil {
		return err
	}
	if err := os.Rename(backupPath(path), path); err != nil {
		return err
	}
	return resealInstalled(path)
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
		if !childproc.Alive(pid) {
			// Give the predecessor's listeners and child processes a moment to
			// be reaped after the process itself is gone.
			time.Sleep(300 * time.Millisecond)
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// CleanupAfterUpdate discards the previous binary and the staged payload once
// the updated build is running. currentVersion is this build's version: the
// staged manifest is only cleared when it names the version now running, so an
// update staged for a *newer* release than this one survives.
func CleanupAfterUpdate(dataDir, exePath, currentVersion string) {
	if resolved, err := filepath.EvalSymlinks(exePath); err == nil {
		_ = os.Remove(backupPath(resolved))
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
