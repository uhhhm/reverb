//go:build windows

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The control channel is what lets the window stop a detached background
// runtime, so it must not be something a web page can name. On Windows that
// means the channel is a filesystem object inside the data directory rather
// than a loopback port; who may open it is decided by the ACL the per-user
// profile applies to that directory, which Reverb inherits rather than sets.
func assertControlChannelPrivate(t *testing.T, dataDir string) {
	t.Helper()
	sock := backgroundSocket(dataDir)
	if !strings.HasPrefix(filepath.Clean(sock), filepath.Clean(dataDir)+string(filepath.Separator)) {
		t.Fatalf("control channel %s is outside the data directory %s", sock, dataDir)
	}
	for _, path := range []string{filepath.Dir(sock), sock} {
		if _, err := os.Stat(path); err != nil {
			t.Fatal(err)
		}
	}
}
