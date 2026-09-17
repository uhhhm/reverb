//go:build !windows

package embedded

import (
	"os/exec"
	"testing"
)

// startHelperProcess starts a long-lived process of a known program and returns
// it with that program's name. The reaping tests are about what the operating
// system reports for a pid, so they need a real process — but which stock
// program to use is a per-OS detail, not part of what they assert.
func startHelperProcess(t *testing.T) (*exec.Cmd, string, <-chan struct{}) {
	t.Helper()
	const program = "sleep"
	cmd := exec.Command(program, "30")
	if err := cmd.Start(); err != nil {
		t.Skipf("cannot start helper process: %v", err)
	}
	exited := make(chan struct{})
	go func() { _ = cmd.Wait(); close(exited) }()
	t.Cleanup(func() {
		select {
		case <-exited:
			return
		default:
		}
		_ = cmd.Process.Kill()
		<-exited
	})
	return cmd, program, exited
}
