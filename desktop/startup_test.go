package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStartupFailureDispatchesNativeDialogWithLogPath(t *testing.T) {
	original := startupErrorDialog
	defer func() { startupErrorDialog = original }()
	var title, message string
	startupErrorDialog = func(gotTitle, gotMessage string) error {
		title, message = gotTitle, gotMessage
		return nil
	}
	logPath := filepath.Join(t.TempDir(), "reverb.log")
	if code := reportStartupFailure(errors.New("database is locked"), logPath); code == 0 {
		t.Fatal("startup failure returned a success exit code")
	}
	if title != "Reverb could not start" {
		t.Fatalf("dialog title = %q", title)
	}
	if !strings.Contains(message, "database is locked") || !strings.Contains(message, logPath) {
		t.Fatalf("dialog message = %q, want the error and log path", message)
	}
}

func TestWindowLogRotatesAtSizeCap(t *testing.T) {
	dir := t.TempDir()
	path := windowLogPath(dir)
	if err := os.WriteFile(path, make([]byte, maxWindowLogSize+1), 0o600); err != nil {
		t.Fatal(err)
	}
	got, closeLog, err := openWindowLog(dir)
	if err != nil {
		t.Fatal(err)
	}
	closeLog()
	if got != path {
		t.Fatalf("log path = %q, want %q", got, path)
	}
	if st, err := os.Stat(path + ".old"); err != nil || st.Size() != maxWindowLogSize+1 {
		t.Fatalf("rotated log = %+v, %v", st, err)
	}
	if st, err := os.Stat(path); err != nil || st.Size() != 0 {
		t.Fatalf("current log = %+v, %v", st, err)
	}
}
