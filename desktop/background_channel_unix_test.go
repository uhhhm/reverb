//go:build !windows

package main

import (
	"os"
	"path/filepath"
	"testing"
)

// The control channel is what lets the window stop a detached background
// runtime, so the operating system — not a check in Reverb's own code — has to
// be the thing keeping other users off it.
func assertControlChannelPrivate(t *testing.T, dataDir string) {
	t.Helper()
	for path, mode := range map[string]os.FileMode{
		filepath.Dir(backgroundSocket(dataDir)): 0700,
		backgroundSocket(dataDir):               0600,
	} {
		st, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if st.Mode().Perm() != mode {
			t.Fatalf("permissions %s: %v", path, st.Mode())
		}
	}
}

// A world-readable directory left by an older build must be tightened, not
// trusted.
func TestListenBackgroundControlTightensLooseDirectoryPermissions(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Dir(backgroundSocket(dir)), 0755); err != nil {
		t.Fatal(err)
	}
	ln, err := listenBackgroundControl(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	assertControlChannelPrivate(t, dir)
}
