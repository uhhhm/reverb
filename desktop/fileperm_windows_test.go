//go:build windows

package main

import (
	"os"
	"testing"
)

// assertUserOnlyFile checks that a file Reverb writes into the data directory
// exists. Windows has no permission bits to assert on — os.Chmod there only
// toggles the read-only attribute — so what restricts the file is the ACL the
// per-user profile applies to AppData, which Reverb inherits rather than sets.
func assertUserOnlyFile(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
}
