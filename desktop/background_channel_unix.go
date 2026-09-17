//go:build !windows

package main

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"syscall"
)

// The background control channel is deliberately not the HTTP API: the desktop
// window must be able to ask a detached background runtime to shut down, and a
// web page must not. The transport therefore has to be one the browser cannot
// reach and the operating system restricts to this user's own processes.
//
// A unix domain socket under the data directory satisfies both: it has no host
// or port a page could name, and its directory and file permissions are
// enforced by the kernel on every connect.

// backgroundSocket is the control channel's address. Tests assert on the file's
// permissions, so it is kept as a path rather than hidden behind the dialer.
func backgroundSocket(dataDir string) string {
	return filepath.Join(dataDir, "background", "control.sock")
}

// dialBackgroundControl connects to the running background runtime.
func dialBackgroundControl(ctx context.Context, dataDir string) (net.Conn, error) {
	return (&net.Dialer{}).DialContext(ctx, "unix", backgroundSocket(dataDir))
}

// listenBackgroundControl publishes the control channel, restricted to this
// user. The directory is created and re-chmodded rather than trusted, so a
// channel left behind by an older build cannot be world-reachable.
//
// Only the database lock owner reaches this point, which is what makes it safe
// to replace a socket left by a crash: nothing else can be serving on it.
func listenBackgroundControl(dataDir string) (net.Listener, error) {
	sock := backgroundSocket(dataDir)
	dir := filepath.Dir(sock)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	if err := os.Chmod(dir, 0700); err != nil {
		return nil, err
	}
	if err := os.Remove(sock); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	ln, err := net.Listen("unix", sock)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(sock, 0600); err != nil {
		_ = ln.Close()
		return nil, err
	}
	return ln, nil
}

// backgroundNotRunning reports whether err means there is no background runtime
// to talk to, as opposed to one that answered badly. Asking an absent process
// to stop is a success, so this must not classify a real failure as absence.
func backgroundNotRunning(err error) bool {
	return errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ECONNREFUSED)
}
