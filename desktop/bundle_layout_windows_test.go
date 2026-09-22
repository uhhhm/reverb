//go:build windows

package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/uhhhm/reverb/desktop/updater"
)

func TestSelfUpdateInWindowsBundleKeepsTools(t *testing.T) {
	root := t.TempDir()
	exe := filepath.Join(root, "reverb-desktop.exe")
	tools := []string{
		"bin/ffmpeg.exe", "bin/navidrome.exe", "bin/deno.exe",
		"bin/spotdl.exe", "bin/yt-dlp.exe", "python/python.exe",
	}
	for _, path := range append([]string{exe}, tools...) {
		full := path
		if path != exe {
			full = filepath.Join(root, path)
		}
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("tool"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	originalExecutablePath := executablePath
	executablePath = func() (string, error) { return exe, nil }
	t.Cleanup(func() { executablePath = originalExecutablePath })
	t.Setenv("PATH", t.TempDir())

	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dataDir := t.TempDir()
	payload := filepath.Join(updater.StagingDir(dataDir), "reverb-desktop.exe")
	if err := os.MkdirAll(filepath.Dir(payload), 0o755); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(payload, b, 0o755); err != nil {
		t.Fatal(err)
	}
	sum, err := updater.FileSHA256(payload)
	if err != nil {
		t.Fatal(err)
	}
	if err := updater.WriteStaged(dataDir, updater.StagedUpdate{
		Tag: "v9.9.9", File: payload, SHA256: sum, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := updater.ApplyStaged(dataDir, exe); err != nil {
		t.Fatal(err)
	}
	for _, rel := range tools {
		if got, err := os.ReadFile(filepath.Join(root, rel)); err != nil || string(got) != "tool" {
			t.Errorf("%s changed by update: %q, %v", rel, got, err)
		}
	}
	_, navidrome, spotdl, _, ytdlp := ResolveBundledTools()
	for name, got := range map[string]string{"navidrome": navidrome, "spotdl": spotdl, "yt-dlp": ytdlp} {
		if want := filepath.Join(root, "bin", name+".exe"); got != want {
			t.Errorf("after update %s = %q, want %q", name, got, want)
		}
	}
	if got, want := ResolveBundledPython(), filepath.Join(root, "python", "python.exe"); got != want {
		t.Errorf("after update python = %q, want %q", got, want)
	}
}
