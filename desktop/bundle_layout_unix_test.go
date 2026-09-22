//go:build !windows

package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/uhhhm/reverb/desktop/updater"
)

// installBundle writes exe and each tool (relative to exe's directory) and
// makes the running binary appear to be exe inside it. Every tool is a script
// that exits 0, so only its location matters.
func installBundle(t *testing.T, exe string, tools ...string) {
	t.Helper()
	paths := []string{exe}
	for _, rel := range tools {
		paths = append(paths, filepath.Join(filepath.Dir(exe), rel))
	}
	for _, path := range paths {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	orig := executablePath
	executablePath = func() (string, error) { return exe, nil }
	t.Cleanup(func() { executablePath = orig })
	// Nothing on PATH, so a hit can only have come from the bundle.
	t.Setenv("PATH", t.TempDir())
}

// The Linux install bundle: the binary at the root, the tools and the runtime's
// wrappers in bin/, the runtime itself in python/.
func TestLinuxBundleLayoutResolves(t *testing.T) {
	root := t.TempDir()
	exe := filepath.Join(root, "reverb-desktop")
	installBundle(t, exe,
		"bin/ffmpeg", "bin/navidrome", "bin/deno", "bin/spotdl", "bin/yt-dlp",
		"python/bin/python", "python/bin/spotdl", "python/bin/yt-dlp")

	ffmpeg, navidrome, spotdl, deno, ytdlp := ResolveBundledTools()
	for name, got := range map[string]string{"ffmpeg": ffmpeg, "navidrome": navidrome, "spotdl": spotdl, "deno": deno, "yt-dlp": ytdlp} {
		// bin/ wins over python/bin/: pip rewrites the latter on every yt-dlp
		// upgrade, with a shebang that breaks when the install moves.
		if want := filepath.Join(root, "bin", name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
	if got, want := ResolveBundledPython(), filepath.Join(root, "python/bin/python"); got != want {
		t.Errorf("python = %q, want %q", got, want)
	}
}

// Reverb.app: the binary in Contents/MacOS, everything else in Resources.
func TestMacAppLayoutResolves(t *testing.T) {
	resources := filepath.Join(t.TempDir(), "Reverb.app/Contents/Resources")
	exe := filepath.Join(filepath.Dir(resources), "MacOS/reverb-desktop")
	installBundle(t, exe,
		"../Resources/bin/ffmpeg", "../Resources/bin/spotdl", "../Resources/bin/yt-dlp",
		"../Resources/python/bin/python")

	ffmpeg, _, spotdl, _, ytdlp := ResolveBundledTools()
	for name, got := range map[string]string{"ffmpeg": ffmpeg, "spotdl": spotdl, "yt-dlp": ytdlp} {
		if want := filepath.Join(resources, "bin", name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
	if got, want := ResolveBundledPython(), filepath.Join(resources, "python/bin/python"); got != want {
		t.Errorf("python = %q, want %q", got, want)
	}
}

// Self-update from an installed Linux bundle swaps only the binary: the tools
// and the runtime beside it are left in place and still resolve afterwards.
func TestSelfUpdateInLinuxBundleKeepsTools(t *testing.T) {
	root := t.TempDir()
	exe := filepath.Join(root, "reverb-desktop")
	tools := []string{"bin/ffmpeg", "bin/navidrome", "bin/deno", "bin/spotdl", "bin/yt-dlp", "python/bin/python"}
	installBundle(t, exe, tools...)

	// Any real executable for this platform will do as the new build; the
	// updater refuses anything that is not one.
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dataDir := t.TempDir()
	payload := filepath.Join(updater.StagingDir(dataDir), "reverb-desktop")
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
		t.Fatalf("ApplyStaged: %v", err)
	}
	if got, err := updater.FileSHA256(exe); err != nil || got != sum {
		t.Fatalf("installed binary digest = %q (%v), want the staged %q", got, err, sum)
	}
	for _, rel := range tools {
		if b, err := os.ReadFile(filepath.Join(root, rel)); err != nil || string(b) != "#!/bin/sh\nexit 0\n" {
			t.Errorf("%s changed by the update: %q, %v", rel, b, err)
		}
	}
	_, navidrome, spotdl, _, ytdlp := ResolveBundledTools()
	for name, got := range map[string]string{"navidrome": navidrome, "spotdl": spotdl, "yt-dlp": ytdlp} {
		if want := filepath.Join(root, "bin", name); got != want {
			t.Errorf("after the update %s = %q, want %q", name, got, want)
		}
	}
	if got, want := ResolveBundledPython(), filepath.Join(root, "python/bin/python"); got != want {
		t.Errorf("after the update python = %q, want %q", got, want)
	}
}
