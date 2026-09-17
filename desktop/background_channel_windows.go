//go:build windows

package main

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/windows"
)

// The control channel keeps the same shape it has on unix: a unix domain
// socket in the data directory. Windows has supported AF_UNIX since Windows 10
// 1803, and Go's net package speaks it there, so the desktop window and a
// detached background runtime talk over a path rather than a port — which is
// the security property that matters, since a web page can name a loopback
// port but not a file.
//
// Access is decided by the filesystem. The socket lives under the per-user
// config directory (AppData\Roaming), whose ACL already restricts it to this
// user and Administrators, and the socket inherits that ACL. There is no
// chmod step as there is on unix: Windows has no permission bits to set, and
// os.Chmod there only toggles the read-only attribute.

// backgroundSocket is the control channel's address.
func backgroundSocket(dataDir string) string {
	return filepath.Join(dataDir, "background", "control.sock")
}

// dialBackgroundControl connects to the running background runtime.
func dialBackgroundControl(ctx context.Context, dataDir string) (net.Conn, error) {
	return (&net.Dialer{}).DialContext(ctx, "unix", backgroundSocket(dataDir))
}

// listenBackgroundControl publishes the control channel.
//
// Only the database lock owner reaches this point, which is what makes it safe
// to replace a socket left by a crash: nothing else can be serving on it.
func listenBackgroundControl(dataDir string) (net.Listener, error) {
	sock := backgroundSocket(dataDir)
	if err := os.MkdirAll(filepath.Dir(sock), 0700); err != nil {
		return nil, err
	}
	if err := os.Remove(sock); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	return net.Listen("unix", sock)
}

// backgroundNotRunning reports whether err means there is no background runtime
// to talk to, as opposed to one that answered badly. Asking an absent process
// to stop is a success, so this must not classify a real failure as absence.
//
// Absence shows up two ways: the socket file is gone, or the file survived a
// crash and nothing is listening on it. Winsock reports the refusal under its
// own error number, which is distinct from the syscall package's ECONNREFUSED.
func backgroundNotRunning(err error) bool {
	return errors.Is(err, os.ErrNotExist) ||
		errors.Is(err, syscall.ECONNREFUSED) ||
		errors.Is(err, windows.WSAECONNREFUSED)
}
