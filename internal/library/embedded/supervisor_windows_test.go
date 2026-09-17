//go:build windows

package embedded

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

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
func startHelperProcess(t *testing.T) (*exec.Cmd, string, <-chan struct{}) {
	t.Helper()
	const program = "ping"
	cmd := childproc.Command(program, "-n", "60", "127.0.0.1")
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

func TestWindowsBundledNavidromeStartsServesAndReleasesItsPort(t *testing.T) {
	root := repoRoot(t)
	binary := filepath.Join(root, "desktop", "tools", "bin", "navidrome.exe")
	if _, err := os.Stat(binary); err != nil {
		if os.Getenv("CI") != "" {
			t.Fatalf("Windows CI bundle is missing Navidrome: %v", err)
		}
		t.Skip("Windows dependency bundle has not been fetched")
	}

	probeListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := probeListener.Addr().(*net.TCPAddr).Port
	_ = probeListener.Close()

	dataDir := t.TempDir()
	musicDir := filepath.Join(dataDir, "music")
	if err := os.MkdirAll(musicDir, 0o755); err != nil {
		t.Fatal(err)
	}
	opts := DefaultNaviOptions(dataDir, musicDir, "windows-lifecycle-test")
	opts.Port = port
	ctx, cancel := context.WithCancel(context.Background())
	proc, err := ExecRunner(binary, filepath.Join(opts.DataDir, "navidrome.pid"))(ctx, BuildNavidromeEnv(opts))
	if err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- proc.Wait() }()
	reaped := false
	t.Cleanup(func() {
		if reaped {
			return
		}
		cancel()
		select {
		case <-exited:
		case <-time.After(15 * time.Second):
		}
	})

	probe := PingProbe("http://127.0.0.1:"+strconv.Itoa(port), nil)
	deadline := time.Now().Add(20 * time.Second)
	for {
		probeCtx, probeCancel := context.WithTimeout(context.Background(), time.Second)
		err = probe(probeCtx)
		probeCancel()
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("Navidrome did not become ready: %v", err)
		}
		time.Sleep(100 * time.Millisecond)
	}

	cancel()
	select {
	case <-exited:
		reaped = true
	case <-time.After(15 * time.Second):
		t.Fatal("Navidrome did not stop after cancellation")
	}
	rebound, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		t.Fatalf("Navidrome did not release port %d: %v", port, err)
	}
	_ = rebound.Close()
}

func TestReapOrphanSparesSameNamedReusedPID(t *testing.T) {
	cmd := childproc.Command(os.Args[0], "-test.run=^TestSameNameHelperProcess$")
	cmd.Env = append(os.Environ(), "REVERB_SAME_NAME_HELPER=1")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan struct{})
	go func() { _ = cmd.Wait(); close(exited) }()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		<-exited
	})

	record, err := json.Marshal(pidRecord{PID: cmd.Process.Pid, Identity: "different-process-instance"})
	if err != nil {
		t.Fatal(err)
	}
	pidPath := filepath.Join(t.TempDir(), "navidrome.pid")
	if err := os.WriteFile(pidPath, record, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := reapOrphan(pidPath, os.Args[0]); err != nil {
		t.Fatal(err)
	}
	if !childproc.Alive(cmd.Process.Pid) {
		t.Fatal("reapOrphan signalled a same-named process whose pid had been reused")
	}
}

func TestSameNameHelperProcess(t *testing.T) {
	if os.Getenv("REVERB_SAME_NAME_HELPER") != "1" {
		return
	}
	time.Sleep(30 * time.Second)
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not locate repository root")
		}
		dir = parent
	}
}
