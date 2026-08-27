package main

import (
	"os"
	"path/filepath"
	"testing"
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
