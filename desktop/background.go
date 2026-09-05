package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

// Background controls use a user-private Unix socket, never the HTTP API or
// Wails bindings. A website cannot request a shutdown through the loopback API.
func backgroundSocket(dataDir string) string {
	return filepath.Join(dataDir, "background", "control.sock")
}

func backgroundRequest(ctx context.Context, dataDir, method, path string) error {
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", backgroundSocket(dataDir))
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport}
	req, err := http.NewRequestWithContext(ctx, method, "http://localhost"+path, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("background control: %s", resp.Status)
	}
	_, err = io.Copy(io.Discard, resp.Body)
	return err
}

// Wait for the acknowledgement: it is sent only after the background runtime
// has closed its database, listeners and child process, and released its lock.
func stopBackgroundSync(dataDir string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	err := backgroundRequest(ctx, dataDir, http.MethodPost, "/stop")
	if errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ECONNREFUSED) {
		return nil
	}
	return err
}

func backgroundControl(cancel context.CancelFunc, stopped <-chan struct{}) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("POST /stop", func(w http.ResponseWriter, r *http.Request) {
		cancel()
		select {
		case <-stopped:
			w.WriteHeader(http.StatusOK)
		case <-r.Context().Done():
		}
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Origin") != "" {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

// runBackground runs the ordinary desktop backend without initialising Wails,
// GTK/Cocoa or a webview. The existing incremental sync/file workers are reused.
func runBackground(args []string) error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	a, err := boot(args)
	if err != nil {
		return err
	}
	a.stopBackground = cancel
	defer a.OnShutdown(context.Background())
	dir := filepath.Dir(backgroundSocket(a.dataDir))
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0700); err != nil {
		return err
	}
	// Only the database lock owner may replace a socket left by a crash.
	if err := os.Remove(backgroundSocket(a.dataDir)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	ln, err := net.Listen("unix", backgroundSocket(a.dataDir))
	if err != nil {
		return err
	}
	defer ln.Close()
	if err := os.Chmod(backgroundSocket(a.dataDir), 0600); err != nil {
		return err
	}
	stopped := make(chan struct{})
	srv := &http.Server{Handler: backgroundControl(cancel, stopped), ReadHeaderTimeout: time.Second, IdleTimeout: time.Second}
	a.OnStartup(a.ctx)
	a.StartServices()
	go func() { _ = srv.Serve(ln) }()
	<-ctx.Done()
	// Stop accepting controls before releasing ownership. Existing stop requests
	// remain open until shutdown has finished; they are not on a.srv.
	_ = ln.Close()
	a.OnShutdown(context.Background())
	close(stopped)
	shutCtx, shutCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutCancel()
	return srv.Shutdown(shutCtx)
}

func spawnBackground(dataDir string, args []string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	logPath := filepath.Join(dataDir, "background.log")
	// Bound retained startup diagnostics across launches.
	if st, err := os.Stat(logPath); err == nil && st.Size() > 5<<20 {
		_ = os.Rename(logPath, logPath+".old")
	}
	output, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer output.Close()
	// nice runs before Go creates OS threads, so every worker and bundled child
	// inherits the lower priority (setpriority in Go would only affect one
	// thread on Linux). nice is provided by both supported desktop platforms.
	cmd := exec.Command("nice", append([]string{"-n", "10", exe, "--background"}, args...)...)
	cmd.Env = os.Environ()
	cmd.Stdout, cmd.Stderr = output, output
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case err := <-exited:
			return fmt.Errorf("background process exited before ready: %v (see %s)", err, logPath)
		case <-ctx.Done():
			_ = cmd.Process.Signal(syscall.SIGTERM)
			return fmt.Errorf("background startup timed out (see %s)", logPath)
		case <-ticker.C:
			if backgroundRequest(ctx, dataDir, http.MethodGet, "/status") == nil {
				return nil
			}
		}
	}
}

var backgroundPreferenceMu sync.Mutex

func (a *App) loadBackgroundPreference() error {
	a.backgroundEnabled.Store(true)
	b, err := os.ReadFile(filepath.Join(a.dataDir, "desktop.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var settings struct {
		BackgroundSync bool `json:"backgroundSync"`
	}
	if err := json.Unmarshal(b, &settings); err != nil {
		return fmt.Errorf("desktop settings: %w", err)
	}
	a.backgroundEnabled.Store(settings.BackgroundSync)
	return nil
}

func (a *App) GetBackgroundSyncEnabled() bool { return a.backgroundEnabled.Load() }

// SetBackgroundSyncEnabled is a desktop binding, deliberately absent from the
// HTTP API. It changes close behaviour; it does not install a login service.
func (a *App) SetBackgroundSyncEnabled(enabled bool) error {
	backgroundPreferenceMu.Lock()
	defer backgroundPreferenceMu.Unlock()
	b, err := json.Marshal(struct {
		BackgroundSync bool `json:"backgroundSync"`
	}{enabled})
	if err != nil {
		return err
	}
	path := filepath.Join(a.dataDir, "desktop.json")
	f, err := os.CreateTemp(a.dataDir, ".desktop-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(b); err != nil {
		_ = f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Rename(f.Name(), path); err != nil {
		return err
	}
	a.backgroundEnabled.Store(enabled)
	return nil
}

func (a *App) QuitAndStopSync() { quitApp(a) }
