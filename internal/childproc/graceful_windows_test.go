//go:build windows

package childproc_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/uhhhm/reverb/internal/childproc"
)

func TestGracefulCommandReceivesCtrlBreakFromGUIProcess(t *testing.T) {
	dir := t.TempDir()
	ready := filepath.Join(dir, "ready")
	stopped := filepath.Join(dir, "stopped")
	target := childproc.GracefulCommandContext(context.Background(), os.Args[0])
	target.Env = append(os.Environ(), helperEnv+"=graceful", "REVERB_CHILDPROC_READY="+ready, "REVERB_CHILDPROC_STOPPED="+stopped)
	if err := target.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- target.Wait() }()
	reaped := false
	t.Cleanup(func() {
		if reaped {
			return
		}
		_ = target.Process.Kill()
		<-exited
	})
	waitForFile(t, ready)

	signaler := filepath.Join(dir, "terminate.exe")
	build := exec.Command("go", "build", "-ldflags=-H windowsgui", "-o", signaler, "./testhelper/windows_terminate")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build GUI signaler: %v: %s", err, output)
	}
	if output, err := exec.Command(signaler, strconv.Itoa(target.Process.Pid)).CombinedOutput(); err != nil {
		t.Fatalf("signal managed child: %v: %s", err, output)
	}
	waitForFile(t, stopped)

	select {
	case err := <-exited:
		reaped = true
		if err != nil {
			t.Fatalf("managed child did not exit cleanly: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("managed child received Ctrl-Break but did not exit")
	}
}

func waitForFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", path)
}
