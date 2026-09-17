//go:build windows

package embedded

import (
	"os/exec"
	"testing"

	"github.com/uhhhm/reverb/internal/childproc"
)

// startHelperProcess starts a long-lived process of a known program and returns
// it with that program's name. The reaping tests are about what the operating
// system reports for a pid, so they need a real process — but which stock
// program to use is a per-OS detail, not part of what they assert.
//
// ping is used rather than timeout, which refuses to run without a console
// input handle and would exit immediately under a test harness. It is started
// through childproc, as the real navidrome is: the orphan this stands in for
// was spawned that way by the run that crashed, and how it was spawned is what
// decides whether it can be asked to stop rather than killed.
func startHelperProcess(t *testing.T) (*exec.Cmd, string) {
	t.Helper()
	const program = "ping"
	cmd := childproc.Command(program, "-n", "60", "127.0.0.1")
	if err := cmd.Start(); err != nil {
		t.Skipf("cannot start helper process: %v", err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	return cmd, program
}
