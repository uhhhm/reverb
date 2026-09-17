//go:build windows

package main

import (
	"os"

	"golang.org/x/sys/windows"
)

// lockOffset is where the exclusive byte range is taken. An exclusive lock on
// Windows blocks reads as well as writes from other handles, so locking the
// start of the file would stop the losing instance from reading the owner's
// pid out of it to name in the error. A single byte far past any content is
// never written and never read, so it carries the lock and nothing else.
const lockOffset = int64(1) << 62

func lockOverlapped() *windows.Overlapped {
	return &windows.Overlapped{
		Offset:     uint32(lockOffset & 0xFFFFFFFF),
		OffsetHigh: uint32(lockOffset >> 32),
	}
}

// lockExclusive takes a one-byte exclusive range lock. LOCKFILE_FAIL_IMMEDIATELY
// turns "someone else holds it" into an error instead of a wait, which is what
// lets the second instance report and exit rather than hang behind the first.
//
// The lock belongs to the file handle. Windows releases every lock a process
// held when it dies, however it dies, so a crash or an End Task cannot leave
// the app permanently unable to start.
func lockExclusive(f *os.File) error {
	return windows.LockFileEx(
		windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0, 1, 0, lockOverlapped(),
	)
}

func unlock(f *os.File) error {
	return windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, lockOverlapped())
}
