package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
)

const maxWindowLogSize = 5 << 20

// startupErrorDialog synchronously presents the supplied title and message in
// a native error surface. It returns only after that surface closes, or with an
// error when the host cannot show one; callers may then fall back to the log.
// Tests replace it to exercise startup dispatch without requiring a display.
var startupErrorDialog = showStartupErrorDialog

func windowLogPath(dataDir string) string { return filepath.Join(dataDir, "reverb.log") }

// openWindowLog keeps startup diagnostics available to GUI-subsystem builds,
// whose stderr is not visible. Rotation is deliberately simple and bounded:
// at most the current file and one previous file are retained.
func openWindowLog(dataDir string) (string, func(), error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return windowLogPath(dataDir), func() {}, err
	}
	path := windowLogPath(dataDir)
	if st, err := os.Stat(path); err == nil && st.Size() > maxWindowLogSize {
		_ = os.Remove(path + ".old")
		if err := os.Rename(path, path+".old"); err != nil {
			return path, func() {}, err
		}
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return path, func() {}, err
	}
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return path, func() {}, err
	}
	previous := log.Writer()
	log.SetOutput(io.MultiWriter(previous, f))
	return path, func() {
		log.SetOutput(previous)
		_ = f.Close()
	}, nil
}

func reportStartupFailure(err error, logPath string) int {
	message := fmt.Sprintf("%v\n\nDiagnostic log: %s", err, logPath)
	log.Printf("desktop startup failed: %v", err)
	if dialogErr := startupErrorDialog("Reverb could not start", message); dialogErr != nil {
		log.Printf("desktop: could not show startup error dialog: %v", dialogErr)
	}
	return 1
}
