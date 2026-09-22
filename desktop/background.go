package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"time"

	"github.com/uhhhm/reverb/internal/childproc"
)

const windowFocusedHeader = "X-Reverb-Window-Focused"

// Background controls travel over a user-private channel, never the HTTP API or
// Wails bindings. A website cannot request a shutdown through the loopback API.
func backgroundRequest(ctx context.Context, dataDir, method, path string) error {
	_, err := instanceRequest(ctx, dataDir, method, path)
	return err
}

func instanceRequest(ctx context.Context, dataDir, method, path string) (http.Header, error) {
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return dialBackgroundControl(ctx, dataDir)
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport}
	req, err := http.NewRequestWithContext(ctx, method, "http://localhost"+path, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("background control: %s", resp.Status)
	}
	_, err = io.Copy(io.Discard, resp.Body)
	return resp.Header.Clone(), err
}

// Wait for the acknowledgement: it is sent only after the background runtime
// has closed its database, listeners and child process, and released its lock.
func stopBackgroundSync(dataDir string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	err := backgroundRequest(ctx, dataDir, http.MethodPost, "/stop")
	if backgroundNotRunning(err) {
		return nil
	}
	return err
}

// openExistingInstance either focuses the running window, or waits for a
// headless background runtime to stop so this process can become the window.
func openExistingInstance(dataDir string) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	header, err := instanceRequest(ctx, dataDir, http.MethodPost, "/open")
	if backgroundNotRunning(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return header.Get(windowFocusedHeader) == "true", nil
}

// awaitExistingInstance closes the startup race between the owner taking the
// lock and publishing its control socket. It returns focused when a window
// answered, or lockReleased when a background runtime stopped (or the owner
// exited) and the caller should retry booting itself.
func awaitExistingInstance(dataDir string, timeout time.Duration) (focused, lockReleased bool, err error) {
	deadline := time.Now().Add(timeout)
	for {
		focused, err := openExistingInstance(dataDir)
		if err != nil {
			return false, false, err
		}
		if focused {
			return true, false, nil
		}
		release, lockErr := AcquireSingleInstanceLock(dataDir)
		if lockErr == nil {
			release()
			return false, true, nil
		}
		if !errors.Is(lockErr, errInstanceAlreadyRunning) {
			return false, false, lockErr
		}
		if time.Now().After(deadline) {
			return false, false, fmt.Errorf("running instance did not publish its control channel within %s", timeout)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func backgroundControl(cancel context.CancelFunc, stopped <-chan struct{}) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	stop := func(w http.ResponseWriter, r *http.Request) {
		cancel()
		select {
		case <-stopped:
			w.WriteHeader(http.StatusOK)
		case <-r.Context().Done():
		}
	}
	mux.HandleFunc("POST /stop", stop)
	mux.HandleFunc("POST /open", stop)
	return privateControl(mux)
}

func windowControl(activate func()) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("POST /open", func(w http.ResponseWriter, _ *http.Request) {
		activate()
		w.Header().Set(windowFocusedHeader, "true")
		w.WriteHeader(http.StatusOK)
	})
	return privateControl(mux)
}

func privateControl(handler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Origin") != "" {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		handler.ServeHTTP(w, r)
	})
}

func startWindowControl(a *App) error {
	ln, err := listenBackgroundControl(a.dataDir)
	if err != nil {
		return err
	}
	a.controlLn = ln
	a.controlSrv = &http.Server{
		Handler:           windowControl(a.requestActivation),
		ReadHeaderTimeout: time.Second,
		IdleTimeout:       time.Second,
	}
	go func() {
		if err := a.controlSrv.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Printf("desktop: instance control failed: %v", err)
		}
	}()
	return nil
}

// runBackground runs the ordinary desktop backend without initialising Wails,
// GTK/Cocoa or a webview. The existing incremental sync/file workers are reused.
func runBackground(args []string) error {
	ctx, cancel := signal.NotifyContext(context.Background(), childproc.ShutdownSignals()...)
	defer cancel()
	a, err := boot(args)
	if err != nil {
		return err
	}
	a.stopBackground = cancel
	defer a.OnShutdown(context.Background())
	ln, err := listenBackgroundControl(a.dataDir)
	if err != nil {
		return err
	}
	defer ln.Close()
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
	return spawnBackgroundWithTimeout(dataDir, args, 45*time.Second, 2*time.Second)
}

func spawnBackgroundWithTimeout(dataDir string, args []string, startupTimeout, terminationGrace time.Duration) error {
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
	// Background sync is the household's least urgent work, and it must keep
	// running after the window that started it has gone.
	cmd := childproc.LowPriorityCommand(exe, append([]string{"--background"}, args...)...)
	cmd.Env = os.Environ()
	cmd.Stdout, cmd.Stderr = output, output
	childproc.Detach(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	ctx, cancel := context.WithTimeout(context.Background(), startupTimeout)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case err := <-exited:
			return fmt.Errorf("background process exited before ready: %v (see %s)", err, logPath)
		case <-ctx.Done():
			stopStartingBackground(cmd.Process.Pid, exited, terminationGrace)
			return fmt.Errorf("background startup timed out (see %s)", logPath)
		case <-ticker.C:
			if backgroundRequest(ctx, dataDir, http.MethodGet, "/status") == nil {
				return nil
			}
		}
	}
}

// stopStartingBackground does not return until the failed child has been
// reaped. A graceful request may be ignored when startup is stuck, so escalate
// after a short grace period. The window can then report failure without
// leaving a hidden database owner behind.
func stopStartingBackground(pid int, exited <-chan error, grace time.Duration) {
	_ = childproc.Terminate(pid)
	timer := time.NewTimer(grace)
	defer timer.Stop()
	select {
	case <-exited:
		return
	case <-timer.C:
	}
	_ = childproc.Kill(pid)
	<-exited
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
