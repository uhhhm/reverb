package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBundleFindNotFound(t *testing.T) {
	got := findBundledTool("nonexistent-tool-xyz123")
	if got != "" {
		t.Fatalf("want empty for missing tool, got %q", got)
	}
}

func TestBundleFindViaPATH(t *testing.T) {
	dir := t.TempDir()
	tool := "test-tool-bundle-xyz"
	path := filepath.Join(dir, tool)
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatalf("write: %v", err)
	}
	orig := os.Getenv("PATH")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+orig)
	got := findBundledTool(tool)
	if got == "" {
		t.Fatalf("want found via PATH, got empty")
	}
	if !filepath.IsAbs(got) {
		t.Fatalf("want absolute path, got %q", got)
	}
	if _, err := os.Stat(got); err != nil {
		t.Fatalf("returned path not stat-able: %v", err)
	}
}

func TestBundleFindNonExecutable(t *testing.T) {
	dir := t.TempDir()
	tool := "non-exec-tool-xyz"
	path := filepath.Join(dir, tool)
	if err := os.WriteFile(path, []byte("x"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	orig := os.Getenv("PATH")
	t.Setenv("PATH", dir)
	// also clear any desktop/tools candidate by using unique name
	got := findBundledTool(tool)
	if got != "" {
		t.Fatalf("want empty for non-executable, got %q", got)
	}
	_ = orig
}

func TestBundleResolveDoesNotPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("ResolveBundledTools panicked: %v", r)
		}
	}()
	f, n, s, d, y := ResolveBundledTools()
	_ = f
	_ = n
	_ = s
	_ = d
	_ = y
}

func TestBundleFindViaDesktopToolsBin(t *testing.T) {
	// Verify that a tool placed in desktop/tools/bin is discoverable
	// even when test cwd is package dir (desktop). Use unique name.
	tool := "bundle-desktop-bin-test"
	// Try to locate repo root: walk up from wd looking for go.mod
	wd, _ := os.Getwd()
	var repoRoot string
	dir := wd
	for i := 0; i < 5; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			repoRoot = dir
			break
		}
		dir = filepath.Join(dir, "..")
	}
	if repoRoot == "" {
		t.Skip("cannot locate repo root")
	}
	binDir := filepath.Join(repoRoot, "desktop/tools/bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(binDir, tool)
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatalf("write: %v", err)
	}
	defer os.Remove(path)
	got := findBundledTool(tool)
	if got == "" {
		t.Fatalf("want found via desktop/tools/bin, got empty")
	}
	if !filepath.IsAbs(got) {
		t.Fatalf("want absolute, got %q", got)
	}
}

func TestApplyBundledToolEnvSetsNavidromeBin(t *testing.T) {
	// The bug this guards: without REVERB_NAVIDROME_BIN the wiring falls back to
	// a bare "navidrome" on PATH, which a desktop install does not have, so the
	// built-in library never starts.
	t.Setenv("REVERB_NAVIDROME_BIN", "")
	t.Setenv("REVERB_SPOTDL_PATH", "")
	t.Setenv("REVERB_YTDLP_PATH", "")
	t.Setenv("REVERB_DENO_PATH", "")

	_, navidrome, _, _, _ := ResolveBundledTools()
	ApplyBundledToolEnv()

	if got := os.Getenv("REVERB_NAVIDROME_BIN"); got != navidrome {
		t.Errorf("REVERB_NAVIDROME_BIN = %q, want %q", got, navidrome)
	}
}

func TestApplyBundledToolEnvDoesNotOverrideExplicitEnv(t *testing.T) {
	t.Setenv("REVERB_NAVIDROME_BIN", "/custom/navidrome")
	ApplyBundledToolEnv()
	if got := os.Getenv("REVERB_NAVIDROME_BIN"); got != "/custom/navidrome" {
		t.Errorf("explicit env was overridden: got %q", got)
	}
}

func TestPrependToPathSkipsEmptyAndDuplicates(t *testing.T) {
	t.Setenv("PATH", "/usr/bin")
	prependToPath("", "/opt/tools/ffmpeg", "/opt/tools/yt-dlp", "/usr/bin/something")
	got := os.Getenv("PATH")
	want := "/opt/tools" + string(filepath.ListSeparator) + "/usr/bin"
	if got != want {
		t.Errorf("PATH = %q, want %q", got, want)
	}
}
