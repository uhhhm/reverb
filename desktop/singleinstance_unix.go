//go:build !windows

package main

import (
	"os"
	"syscall"
)

// lockExclusive takes an advisory flock. LOCK_NB makes it fail immediately
// rather than queue behind the holder, which is what turns "someone else has
// it" into an error the caller can report instead of a hang.
//
// flock is held by the open file description and released by the kernel when
// the holder's last descriptor closes — including when the process dies — which
// is the crash-safety property AcquireSingleInstanceLock relies on.
func lockExclusive(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
}

func unlock(f *os.File) error { return syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }
