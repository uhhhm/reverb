package updater

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// buildProbe compiles a real binary that records its own version in the file
// named by the PROBE_MARK environment variable. Real binaries, not fixtures:
// the swap renames a file the operating system is executing and the relaunch
// spawns whatever landed in its place, and neither behaviour is observable
// against a stub.
func buildProbe(t *testing.T, version, outPath string) {
	t.Helper()
	src := filepath.Join(t.TempDir(), "main.go")
	code := `package main

import "os"

func main() {
	if p := os.Getenv("PROBE_MARK"); p != "" {
		_ = os.WriteFile(p, []byte("` + version + `"), 0o644)
	}
}
`
	if err := os.WriteFile(src, []byte(code), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("go", "build", "-o", outPath, src)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("building probe %s: %v\n%s", version, err, out)
	}
}

// The end-to-end install: a staged build replaces the running binary and the
// version that actually starts afterwards is the new one.
func TestInstallReplacesAndRelaunchesTheRealBinary(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles two binaries")
	}
	dataDir := t.TempDir()
	binDir := t.TempDir()
	exe := filepath.Join(binDir, "reverb-desktop")
	payload := filepath.Join(StagingDir(dataDir), "reverb-desktop")

	buildProbe(t, "v1", exe)
	if err := os.MkdirAll(StagingDir(dataDir), 0o755); err != nil {
		t.Fatal(err)
	}
	buildProbe(t, "v2", payload)

	sum, err := FileSHA256(payload)
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteStaged(dataDir, StagedUpdate{Tag: "v2.0.0", File: payload, SHA256: sum}); err != nil {
		t.Fatal(err)
	}

	if err := ApplyStaged(dataDir, exe); err != nil {
		t.Fatalf("ApplyStaged: %v", err)
	}

	mark := filepath.Join(t.TempDir(), "mark")
	t.Setenv("PROBE_MARK", mark)
	if err := Relaunch(dataDir, exe); err != nil {
		t.Fatalf("Relaunch: %v", err)
	}

	deadline := time.Now().Add(15 * time.Second)
	for {
		if b, err := os.ReadFile(mark); err == nil {
			if string(b) != "v2" {
				t.Fatalf("the relaunched binary reported %q, want v2", b)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the relaunched binary never ran")
		}
		time.Sleep(50 * time.Millisecond)
	}

	// The successor is told which process to wait for before it touches the
	// database or the Navidrome port.
	b, err := os.ReadFile(relaunchMarker(dataDir))
	if err != nil {
		t.Fatalf("relaunch marker: %v", err)
	}
	if got, _ := strconv.Atoi(string(b)); got != os.Getpid() {
		t.Fatalf("relaunch marker names pid %s, want %d", b, os.Getpid())
	}
}
