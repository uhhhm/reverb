package main

import (
	"os"
	"path/filepath"
	"testing"
)

// A socket left behind by a crash must not stop the next run from publishing
// the channel: the lock owner is by definition the only possible server.
func TestListenBackgroundControlReplacesAStaleSocket(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Dir(backgroundSocket(dir)), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(backgroundSocket(dir), nil, 0600); err != nil {
		t.Fatal(err)
	}
	ln, err := listenBackgroundControl(dir)
	if err != nil {
		t.Fatalf("listen over a stale socket: %v", err)
	}
	defer ln.Close()
	assertControlChannelPrivate(t, dir)
}
