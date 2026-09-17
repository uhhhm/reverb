//go:build !darwin && !windows

package updater

import (
	"os/exec"

	"github.com/uhhhm/reverb/internal/childproc"
)

// backupPath keeps the outgoing binary beside the new one, where a rename can
// reach it without crossing a filesystem.
func backupPath(exePath string) string { return exePath + backupSuffix }

// resealInstalled has nothing to do: Linux does not verify a signature before
// running a binary, so the replaced file is runnable as soon as it is in place.
func resealInstalled(string) error { return nil }

// relaunchCommand runs the new binary directly. There is no application
// wrapper to go through, so nothing waits on it — the successor is expected to
// outlive this process, not report back to it.
func relaunchCommand(exePath string, args []string) (cmd *exec.Cmd, waitForExit bool) {
	return childproc.Command(exePath, args...), false
}
