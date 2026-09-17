package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSingleInstanceAcquireSuccess(t *testing.T) {
	dir := t.TempDir()
	release, err := AcquireSingleInstanceLock(dir)
	if err != nil {
		t.Fatalf("first acquire failed: %v", err)
	}
	if release == nil {
		t.Fatal("release func is nil")
	}
	defer release()
	lockPath := filepath.Join(dir, "lock")
	if _, err := os.Stat(lockPath); err != nil {
		t.Fatalf("lock file missing: %v", err)
	}
}

func TestSingleInstanceSecondFails(t *testing.T) {
	dir := t.TempDir()
	r1, err := AcquireSingleInstanceLock(dir)
	if err != nil {
		t.Fatalf("first acquire failed: %v", err)
	}
	defer r1()
	_, err = AcquireSingleInstanceLock(dir)
	if err == nil {
		t.Fatal("expected second acquire to fail, got nil")
	}
}

func TestSingleInstanceReleaseAllowsReacquire(t *testing.T) {
	dir := t.TempDir()
	r1, err := AcquireSingleInstanceLock(dir)
	if err != nil {
		t.Fatalf("first acquire failed: %v", err)
	}
	r1()
	r2, err := AcquireSingleInstanceLock(dir)
	if err != nil {
		t.Fatalf("reacquire after release failed: %v", err)
	}
	defer r2()
	lockPath := filepath.Join(dir, "lock")
	if _, err := os.Stat(lockPath); err != nil {
		t.Fatalf("lock file missing after reacquire: %v", err)
	}
}

func TestSingleInstanceEmptyDir(t *testing.T) {
	_, err := AcquireSingleInstanceLock("")
	if err == nil {
		t.Fatal("expected error for empty dataDir")
	}
}

// A force-quit leaves the lock file on disk with no process behind it. The next
// launch must still start: an existence-based lock would refuse forever.
func TestSingleInstanceStaleLockFileDoesNotBlock(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "lock"), []byte("999999"), 0o644); err != nil {
		t.Fatal(err)
	}

	release, err := AcquireSingleInstanceLock(dir)
	if err != nil {
		t.Fatalf("acquire over a stale lock file failed: %v", err)
	}
	release()
}

// The lock that matters is the one the operating system holds on an open file,
// not the in-process map beside it: two copies of the app are two processes.
// This is the assertion that a port has to earn, so it runs a real second
// process rather than a second call in this one.
func TestSingleInstanceRefusesASecondProcess(t *testing.T) {
	if dir := os.Getenv("REVERB_LOCK_HOLDER_DIR"); dir != "" {
		release, err := AcquireSingleInstanceLock(dir)
		if err != nil {
			t.Fatalf("holder could not acquire: %v", err)
		}
		defer release()
		// Announce the lock is held, then wait to be killed.
		if err := os.WriteFile(filepath.Join(dir, "held"), nil, 0o644); err != nil {
			t.Fatal(err)
		}
		time.Sleep(60 * time.Second)
		return
	}

	dir := t.TempDir()
	holder := exec.Command(os.Args[0], "-test.run=^TestSingleInstanceRefusesASecondProcess$")
	holder.Env = append(os.Environ(), "REVERB_LOCK_HOLDER_DIR="+dir)
	if err := holder.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = holder.Process.Kill(); _ = holder.Wait() }()

	deadline := time.Now().Add(30 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(dir, "held")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("holder never took the lock")
		}
		time.Sleep(20 * time.Millisecond)
	}

	if _, err := AcquireSingleInstanceLock(dir); err == nil {
		t.Fatal("a second process acquired a lock another process holds")
	} else if !strings.Contains(err.Error(), "another instance is running") {
		t.Fatalf("error must name the cause: %v", err)
	}

	// Killing the holder stands in for a crash or a force quit: the operating
	// system must drop the lock, or the app could never start again.
	if err := holder.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = holder.Wait()

	deadline = time.Now().Add(30 * time.Second)
	for {
		release, err := AcquireSingleInstanceLock(dir)
		if err == nil {
			release()
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("lock not released by a killed holder: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
