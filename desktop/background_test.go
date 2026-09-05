package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestBackgroundPreferenceAndShutdownHandoff(t *testing.T) {
	for _, tc := range []struct {
		name          string
		enabled, quit bool
		want          int
	}{
		{"close", true, false, 1}, {"disabled", false, false, 0}, {"quit", true, true, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := NewApp()
			a.dataDir = t.TempDir()
			if err := a.loadBackgroundPreference(); err != nil {
				t.Fatal(err)
			}
			if !a.GetBackgroundSyncEnabled() {
				t.Fatal("default must keep syncing")
			}
			if err := a.SetBackgroundSyncEnabled(tc.enabled); err != nil {
				t.Fatal(err)
			}
			loaded := NewApp()
			loaded.dataDir = a.dataDir
			if err := loaded.loadBackgroundPreference(); err != nil {
				t.Fatal(err)
			}
			if loaded.GetBackgroundSyncEnabled() != tc.enabled {
				t.Fatal("preference was not persisted")
			}
			info, err := os.Stat(filepath.Join(a.dataDir, "desktop.json"))
			if err != nil {
				t.Fatal(err)
			}
			if info.Mode().Perm() != 0600 {
				t.Fatalf("settings permissions: %v", info.Mode())
			}
			a.quitRequested.Store(tc.quit)
			released := false
			a.releaseLock = func() { released = true }
			lifetime, cancel := context.WithCancel(context.Background())
			a.cancel = cancel
			starts := 0
			a.startBackground = func() error {
				if !released {
					t.Error("background started before lock released")
				}
				if lifetime.Err() == nil {
					t.Error("workers were not cancelled")
				}
				starts++
				return nil
			}
			a.OnShutdown(context.Background())
			a.OnShutdown(context.Background())
			if starts != tc.want {
				t.Fatalf("starts=%d, want %d", starts, tc.want)
			}
		})
	}
}

func TestBackgroundControlRejectsBrowserAndWrongMethod(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stopped := make(chan struct{})
	handler := backgroundControl(cancel, stopped)
	for _, tc := range []struct {
		method, origin string
		want           int
	}{
		{"POST", "https://example.com", 403}, {"GET", "", 405},
	} {
		req := httptest.NewRequest(tc.method, "http://localhost/stop", nil)
		req.Header.Set("Origin", tc.origin)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != tc.want {
			t.Fatalf("status=%d, want %d", rec.Code, tc.want)
		}
		if ctx.Err() != nil {
			t.Fatal("unauthorized request stopped background")
		}
	}
}

func TestBackgroundControlWaitsForShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stopped := make(chan struct{})
	srv := httptest.NewServer(backgroundControl(cancel, stopped))
	defer srv.Close()
	done := make(chan error, 1)
	go func() {
		resp, err := http.Post(srv.URL+"/stop", "", nil)
		if err == nil {
			resp.Body.Close()
		}
		done <- err
	}()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("stop not requested")
	}
	select {
	case <-done:
		t.Fatal("acknowledged before shutdown")
	default:
	}
	close(stopped)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("stop did not finish")
	}
}

// Run the actual headless entry point in a child: priority changes and signal
// registration must never affect the test runner. The process has its own home,
// database, music folder and random P2P port.
func TestBackgroundProcess(t *testing.T) {
	if os.Getenv("REVERB_BACKGROUND_TEST") == "1" {
		if err := runBackground([]string{"--p2p-port=0"}); err != nil {
			t.Fatal(err)
		}
		return
	}
	dir, err := os.MkdirTemp("", "rv-bg-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	cmd := exec.Command(os.Args[0], "-test.run=^TestBackgroundProcess$")
	cmd.Env = append(os.Environ(), "REVERB_BACKGROUND_TEST=1", "HOME="+dir, "XDG_CONFIG_HOME="+dir,
		"REVERB_DB="+filepath.Join(dir, "reverb.db"), "REVERB_DOWNLOAD_DIR="+filepath.Join(dir, "music"),
		"REVERB_UPDATE_REPO=off", "REVERB_PORT=0", "REVERB_NAVIDROME_BIN=/nonexistent")
	logFile, err := os.Create(filepath.Join(dir, "test.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer logFile.Close()
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for backgroundRequest(ctx, dir, http.MethodGet, "/status") != nil {
		select {
		case <-ctx.Done():
			b, _ := os.ReadFile(logFile.Name())
			t.Fatalf("background not ready: %s", b)
		case <-ticker.C:
		}
	}
	for path, mode := range map[string]os.FileMode{filepath.Dir(backgroundSocket(dir)): 0700, backgroundSocket(dir): 0600} {
		st, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if st.Mode().Perm() != mode {
			t.Fatalf("permissions %s: %v", path, st.Mode())
		}
	}
	if _, err := AcquireSingleInstanceLock(dir); err == nil {
		t.Fatal("background did not own the database lock")
	}
	if err := stopBackgroundSync(dir); err != nil {
		t.Fatal(err)
	}
	release, err := AcquireSingleInstanceLock(dir)
	if err != nil {
		t.Fatalf("stop acknowledged before lock released: %v", err)
	}
	release()
	if err := cmd.Wait(); err != nil {
		b, _ := os.ReadFile(logFile.Name())
		t.Fatalf("background exit: %v: %s", err, b)
	}
	if err := stopBackgroundSync(dir); err != nil {
		t.Fatalf("stopping absent process: %v", err)
	}
}
