//go:build windows

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrependToPathTreatsWindowsPathsCaseInsensitively(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", strings.ToUpper(dir))
	prependToPath(filepath.Join(strings.ToLower(dir), "ffmpeg.exe"))
	if got := os.Getenv("PATH"); got != strings.ToUpper(dir) {
		t.Fatalf("PATH = %q, duplicated existing directory %q", got, dir)
	}
}

func TestWindowsDependencyBundleResolvesAndWiresEveryTool(t *testing.T) {
	ffmpeg, navidrome, spotdl, deno, ytdlp := ResolveBundledTools()
	python := ResolveBundledPython()
	resolved := map[string]string{
		"ffmpeg": ffmpeg, "navidrome": navidrome, "spotdl": spotdl,
		"deno": deno, "yt-dlp": ytdlp, "python": python,
	}
	bundleRoot, err := filepath.Abs("tools")
	if err != nil {
		t.Fatal(err)
	}
	missing := false
	for name, path := range resolved {
		if path == "" || !strings.HasPrefix(filepath.Clean(path), filepath.Clean(bundleRoot)+string(filepath.Separator)) {
			missing = true
			t.Logf("%s did not resolve from the Windows dependency bundle: %q", name, path)
		}
	}
	if missing {
		if os.Getenv("CI") != "" {
			t.Fatal("Windows CI dependency bundle is incomplete")
		}
		t.Skip("Windows dependency bundle has not been fetched")
	}

	for _, key := range []string{"REVERB_NAVIDROME_BIN", "REVERB_SPOTDL_PATH", "REVERB_YTDLP_PATH", "REVERB_DENO_PATH", "REVERB_YTDLP_PYTHON"} {
		t.Setenv(key, "")
	}
	ApplyBundledToolEnv()
	want := map[string]string{
		"REVERB_NAVIDROME_BIN": navidrome,
		"REVERB_SPOTDL_PATH":   spotdl,
		"REVERB_YTDLP_PATH":    ytdlp,
		"REVERB_DENO_PATH":     deno,
		"REVERB_YTDLP_PYTHON":  python,
	}
	for key, path := range want {
		if got := os.Getenv(key); got != path {
			t.Errorf("%s = %q, want %q", key, got, path)
		}
	}
}
