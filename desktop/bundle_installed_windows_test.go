//go:build windows

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestInstalledBundle verifies an extracted Windows first-install archive
// through the same executable-relative lookup the app uses at runtime.
func TestInstalledBundle(t *testing.T) {
	exe := os.Getenv("REVERB_BUNDLE_EXE")
	if exe == "" {
		t.Skip("REVERB_BUNDLE_EXE is not set; the release workflow sets it")
	}
	exe, err := filepath.Abs(exe)
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Dir(exe)
	orig := executablePath
	executablePath = func() (string, error) { return exe, nil }
	t.Cleanup(func() { executablePath = orig })
	t.Chdir(t.TempDir())
	t.Setenv("PATH", t.TempDir())
	for _, key := range []string{"REVERB_NAVIDROME_BIN", "REVERB_SPOTDL_PATH", "REVERB_YTDLP_PATH", "REVERB_DENO_PATH", "REVERB_YTDLP_PYTHON"} {
		t.Setenv(key, "")
	}

	ffmpeg, navidrome, spotdl, deno, ytdlp := ResolveBundledTools()
	for _, tool := range []struct {
		name, path string
		version    []string
	}{
		{"ffmpeg", ffmpeg, []string{"-version"}},
		{"navidrome", navidrome, []string{"--version"}},
		{"deno", deno, []string{"--version"}},
		{"spotdl", spotdl, []string{"--version"}},
		{"yt-dlp", ytdlp, []string{"--version"}},
		{"python", ResolveBundledPython(), []string{"--version"}},
	} {
		if !strings.HasPrefix(strings.ToLower(tool.path), strings.ToLower(root+string(filepath.Separator))) {
			t.Errorf("%s resolved to %q, outside the bundle %s", tool.name, tool.path, root)
			continue
		}
		if out, err := exec.Command(tool.path, tool.version...).CombinedOutput(); err != nil {
			t.Errorf("%s: %v\n%s", tool.name, err, out)
		}
	}
}
