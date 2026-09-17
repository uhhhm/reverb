//go:build !windows

package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBundleFindNonExecutable(t *testing.T) {
	dir := t.TempDir()
	tool := "non-exec-tool-xyz"
	path := filepath.Join(dir, tool)
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	t.Setenv("PATH", dir)
	if got := findBundledTool(tool); got != "" {
		t.Fatalf("want empty for non-executable, got %q", got)
	}
}
