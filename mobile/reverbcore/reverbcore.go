// Package reverbcore is the Go core as a phone links it (ADR 0003). gomobile
// binds it into the iOS app, and it covers lifecycle only: start the core with
// a data directory, report its loopback port, stop it. Everything else goes
// over the core's HTTP and WebSocket API on that port, through the client
// generated from OpenAPI.
//
// The core runs the phone profile: its library is the folder of offline files
// under the data directory, and it pairs and syncs as a full Device.
package reverbcore

import (
	"context"
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/uhhhm/reverb/internal/api"
	"github.com/uhhhm/reverb/internal/app"
	"github.com/uhhhm/reverb/internal/config"
)

// Version is the release, stamped at build time with
// -ldflags "-X github.com/uhhhm/reverb/mobile/reverbcore.Version=…".
var Version = "dev"

type instance struct {
	rt     *app.Runtime
	srv    *http.Server
	cancel context.CancelFunc
	port   int
}

var (
	mu      sync.Mutex
	running *instance
)

// Start boots the core over dataDir, creating it if needed, and returns the
// loopback port its API listens on. Starting a running core returns its port.
// The environment is read as on a desktop, so REVERB_P2P_PORT moves the libp2p
// port off its fixed default, which a test on a simulator needs when the Mac
// runs a desktop too.
func Start(dataDir string) (int, error) {
	mu.Lock()
	defer mu.Unlock()
	if running != nil {
		return running.port, nil
	}
	if dataDir == "" {
		return 0, errors.New("reverbcore: a data directory is required")
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return 0, err
	}
	// The environment is read as on a desktop; the flags are not.
	cfg, err := config.Load(nil, os.Getenv)
	if err != nil {
		return 0, err
	}
	rt, err := app.Build(context.Background(), app.Options{
		DBPath:  filepath.Join(dataDir, "reverb.db"),
		Version: Version,
		P2PPort: cfg.P2PPort,
		Profile: app.ProfilePhone,
		Getenv:  os.Getenv,
	})
	if err != nil {
		return 0, err
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		rt.Close()
		return 0, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	rt.StartBackground(ctx)
	srv := &http.Server{Handler: api.NewServer(rt.Deps).Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("reverbcore: serve: %v", err)
		}
	}()
	running = &instance{rt: rt, srv: srv, cancel: cancel, port: ln.Addr().(*net.TCPAddr).Port}
	return running.port, nil
}

// Port is the running core's loopback API port, or 0 when it is stopped.
func Port() int {
	mu.Lock()
	defer mu.Unlock()
	if running == nil {
		return 0
	}
	return running.port
}

// Stop shuts the core down and waits for it to release the database and the
// music folder. Stopping a stopped core does nothing.
func Stop() {
	mu.Lock()
	defer mu.Unlock()
	if running == nil {
		return
	}
	in := running
	running = nil
	in.cancel()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = in.srv.Shutdown(ctx)
	in.rt.Close()
}
