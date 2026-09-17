//go:build windows

package updater

import (
	"os/exec"

	"github.com/uhhhm/reverb/internal/childproc"
)

// backupPath keeps the outgoing binary beside the new one, where a rename can
// reach it without crossing a filesystem.
//
// Renaming the running executable is the swap mechanism, and it needs no
// helper process: Windows locks an image against deletion and against writes
// while it is mapped, but not against being moved within its volume. What it
// does refuse is deleting a still-mapped image, so the old name cannot always
// be reused — freeBackupPath falls back to a unique sibling, and
// CleanupAfterUpdate collects both forms once the predecessor has exited.
// The remaining failure mode is a locked directory (antivirus, a shell with
// the folder open), which fails the rename and rolls back to the working
// binary rather than leaving a half-installed app.
func backupPath(exePath string) string { return exePath + backupSuffix }

// resealInstalled has nothing to do: Windows runs an unsigned binary, and
// Reverb's builds carry no signature to invalidate by replacing the file.
func resealInstalled(string) error { return nil }

// relaunchCommand runs the new binary directly. There is no application
// wrapper to go through, so nothing waits on it — the successor is expected to
// outlive this process, not report back to it.
func relaunchCommand(exePath string, args []string) (cmd *exec.Cmd, waitForExit bool) {
	return childproc.Command(exePath, args...), false
}
