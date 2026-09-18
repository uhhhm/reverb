//go:build !windows

package main

import (
	"os"
	"testing"
)

// assertUserOnlyFile checks that a file Reverb writes into the data directory
// is readable only by the user who owns it. Preferences and credentials live
// there, and the mode bits are what the kernel enforces on unix.
func assertUserOnlyFile(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("permissions %s: %v", path, info.Mode())
	}
}
