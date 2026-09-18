package main

import (
	"os"
	"path/filepath"
	"testing"
)

// A socket left behind by a crash must not stop the next run from publishing
// the channel: the lock owner is by definition the only possible server.
func TestListenBackgroundControlReplacesAStaleSocket(t *testing.T) {
	dir := socketTempDir(t)
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

// socketTempDir is a temporary data directory short enough to hold a unix
// socket. A socket address is a sockaddr_un, whose path is capped at 108 bytes
// on both unix and Windows, and t.TempDir() builds its path out of the test's
// own name — which here is long enough that the socket under it exceeds the
// cap and bind fails with EINVAL. The real data directory (AppData\Reverb,
// ~/.local/share/reverb) is nowhere near the limit, so this is the test's
// problem to solve rather than the channel's.
func socketTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "rvb")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}
