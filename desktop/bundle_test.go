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
	f, n, s, d := ResolveBundledTools()
	_ = f
	_ = n
	_ = s
	_ = d
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
