package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
)

var errInstanceAlreadyRunning = errors.New("another instance is running")

var (
	singleMu   sync.Mutex
	singleHeld = map[string]*os.File{}
)

// AcquireSingleInstanceLock takes an exclusive lock on DataDir/lock.
//
// Two copies of the app on one machine mean two writers on one SQLite file, two
// attempts to bind the fixed p2p port and two supervised Navidromes fighting
// over 4533, so the second copy must not get that far.
//
// The lock is held by the operating system on the open file, not by the
// existence of the file. That is the contract a port must reproduce: the OS
// must drop the lock when the holding process dies, so a crash or a force quit
// cannot leave a lock behind that keeps the app from ever starting again, and
// the attempt must fail immediately rather than wait for the holder. The file
// itself is left in place on release — removing it would let another process
// lock a file nobody can find any more and believe it holds the lock. Its
// contents are the owner's pid, which is only there to name the culprit in the
// error.
//
// An in-process map is kept alongside so a second call in the same process
// fails too. The OS lock is typically per open file, so the same process would
// otherwise re-lock its own file happily.
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
		return nil, fmt.Errorf("%w (lock %s)", errInstanceAlreadyRunning, lockPath)
	}

	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return nil, err
	}
	if err := lockExclusive(f); err != nil {
		owner := ""
		if b, rerr := os.ReadFile(lockPath); rerr == nil && len(b) > 0 {
			owner = fmt.Sprintf(" held by pid %s", string(b))
		}
		_ = f.Close()
		return nil, fmt.Errorf("%w (lock %s%s): %v", errInstanceAlreadyRunning, lockPath, owner, err)
	}

	if err := f.Truncate(0); err == nil {
		_, _ = f.WriteAt([]byte(strconv.Itoa(os.Getpid())), 0)
	}

	singleHeld[lockPath] = f

	release := func() {
		singleMu.Lock()
		defer singleMu.Unlock()
		if held, ok := singleHeld[lockPath]; !ok || held != f {
			return
		}
		delete(singleHeld, lockPath)
		_ = unlock(f)
		_ = f.Close()
	}

	return release, nil
}
