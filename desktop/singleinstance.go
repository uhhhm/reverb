package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

var (
	singleMu   sync.Mutex
	singleHeld = map[string]*os.File{}
)

// AcquireSingleInstanceLock acquires a file lock on DataDir/lock.
// It uses an O_CREATE|O_EXCL file plus an in-process map so that a
// second call in the same process fails even if the kernel flock would
// allow it. The returned release func closes and removes the lock file
// and must be called to allow a later reacquire. Pure stdlib, no flock
// dependency, portable across linux/mac.
func AcquireSingleInstanceLock(dataDir string) (func(), error) {
	if dataDir == "" {
		return nil, fmt.Errorf("dataDir empty")
	}
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return nil, err
	}
	lockPath := filepath.Join(dataDir, "lock")

	singleMu.Lock()
	defer singleMu.Unlock()

	if _, ok := singleHeld[lockPath]; ok {
		return nil, fmt.Errorf("another instance is running (lock %s)", lockPath)
	}

	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0644)
	if err != nil {
		if os.IsExist(err) {
			return nil, fmt.Errorf("another instance is running (lock %s): %w", lockPath, err)
		}
		return nil, err
	}

	singleHeld[lockPath] = f

	release := func() {
		singleMu.Lock()
		defer singleMu.Unlock()
		if held, ok := singleHeld[lockPath]; ok && held == f {
			_ = held.Close()
			delete(singleHeld, lockPath)
			_ = os.Remove(lockPath)
			return
		}
		_ = f.Close()
		_ = os.Remove(lockPath)
	}

	return release, nil
}
