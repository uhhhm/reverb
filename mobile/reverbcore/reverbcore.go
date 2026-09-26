// Package reverbcore is the Go core as a phone links it (ADR 0003). gomobile
// binds it into the iOS app. Lifecycle, the loopback API's launch secret and
// paired secret transfer cross the binding; ordinary operations use the core's
// loopback HTTP/WebSocket API, which refuses any request without the secret.
//
// The core runs the phone profile: its library is the folder of offline files
// under the data directory, and it pairs and syncs as a full Device.
package reverbcore

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/uhhhm/reverb/internal/api"
	"github.com/uhhhm/reverb/internal/app"
	"github.com/uhhhm/reverb/internal/config"
	"github.com/uhhhm/reverb/internal/p2p"
)

// Version is the release, stamped at build time with
// -ldflags "-X github.com/uhhhm/reverb/mobile/reverbcore.Version=…".
var Version = "dev"

// SecretHeader is the request header that carries Secret.
const SecretHeader = api.LocalSecretHeader

type instance struct {
	rt     *app.Runtime
	srv    *http.Server
	cancel context.CancelFunc
	port   int
	secret string
}

var (
	mu      sync.Mutex
	running *instance

	credMu              sync.RWMutex
	spotifyClientID     string
	spotifyClientSecret string

	pythonMu       sync.Mutex
	pythonHome     string
	pythonPackages string
)

// ConfigurePython names the app bundle's Python: home holds the standard
// library under lib/python3.14, and packages the bundled yt-dlp. Call it
// before Start. A build without the embedded interpreter ignores it.
func ConfigurePython(home, packages string) {
	pythonMu.Lock()
	defer pythonMu.Unlock()
	pythonHome, pythonPackages = home, packages
}

// SetSpotifyCredentials supplies this app's Keychain values to the phone core
// in memory. Empty values revoke them. They are never written to process-wide
// environment, where child processes could inherit the secret. A running core
// reloads its search sources in place, so playback and the port are untouched.
func SetSpotifyCredentials(clientID, clientSecret string) {
	credMu.Lock()
	changed := spotifyClientID != clientID || spotifyClientSecret != clientSecret
	spotifyClientID, spotifyClientSecret = clientID, clientSecret
	credMu.Unlock()
	mu.Lock()
	inst := running
	mu.Unlock()
	if !changed || inst == nil || inst.rt.Reloader == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := inst.rt.Reloader.Reload(ctx); err != nil {
		log.Printf("reverbcore: reload search sources: %v", err)
	}
}

// getenv is the process environment with the Spotify credentials replaced by
// the in-memory values, read per call so a reload sees the latest ones.
func getenv(key string) string {
	switch key {
	case "REVERB_SPOTIFY_CLIENT_ID", "REVERB_SPOTIFY_CLIENT_SECRET":
		credMu.RLock()
		defer credMu.RUnlock()
		if key == "REVERB_SPOTIFY_CLIENT_ID" {
			return spotifyClientID
		}
		return spotifyClientSecret
	default:
		return os.Getenv(key)
	}
}

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
	python, err := phonePython(dataDir)
	if err != nil {
		return 0, err
	}
	rt, err := app.Build(context.Background(), app.Options{
		DBPath:     filepath.Join(dataDir, "reverb.db"),
		Version:    Version,
		UpdateRepo: "uhhhm/reverb",
		P2PPort:    cfg.P2PPort,
		Profile:    app.ProfilePhone,
		Getenv:     getenv,
		Python:     python,
	})
	if err != nil {
		return 0, err
	}
	// Other apps on the phone can reach a loopback port, so only a caller
	// holding this launch's secret is the owner. It lives in memory alone and
	// reaches the app through Secret, never over HTTP.
	var key [32]byte
	if _, err := rand.Read(key[:]); err != nil {
		rt.Close()
		return 0, err
	}
	secret := hex.EncodeToString(key[:])
	deps := rt.Deps
	deps.LocalSecret = secret
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		rt.Close()
		return 0, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	rt.StartBackground(ctx)
	srv := &http.Server{Handler: api.NewServer(deps).Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("reverbcore: serve: %v", err)
		}
	}()
	running = &instance{rt: rt, srv: srv, cancel: cancel, port: ln.Addr().(*net.TCPAddr).Port, secret: secret}
	return running.port, nil
}

// Secret is the running core's loopback API secret, to send in SecretHeader
// on every request, or "" when it is stopped. Each Start makes a new one.
func Secret() string {
	mu.Lock()
	defer mu.Unlock()
	if running == nil {
		return ""
	}
	return running.secret
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

// CopySpotifyCredentials retrieves the paired desktop's Spotify credentials
// over the trusted P2P transport. This intentionally bypasses the loopback
// HTTP API, which other local apps could potentially call. The iOS layer must
// save the result in its Keychain; the Go core never persists the secret.
//
// An empty string with no error means a paired device answered without any
// credentials, so a previously copied secret should be forgotten.
func CopySpotifyCredentials() (string, error) {
	// The lookup dials peers, so it runs outside mu: Port and Stop stay
	// responsive, and a Stop meanwhile only makes the request fail.
	mu.Lock()
	inst := running
	mu.Unlock()
	if inst == nil {
		return "", errors.New("reverbcore: not running")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	credentials, err := inst.rt.CopySpotifyCredentials(ctx)
	if errors.Is(err, p2p.ErrNoSearchCredentials) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	data, err := json.Marshal(credentials)
	return string(data), err
}

// pairTarget is what a pairing link points at, for the app to confirm before
// it pairs: the device and each address it would dial, with whether the
// address is on a LAN or VPN rather than the open internet.
type pairTarget struct {
	PeerID    string     `json:"peerId"`
	ExpiresAt int64      `json:"expiresAt"`
	Addrs     []pairAddr `json:"addrs"`
}

type pairAddr struct {
	Addr  string `json:"addr"`
	Local bool   `json:"local"`
}

// InspectPairPayload reads a pairing link (a reverb://pair URL, from the
// system Camera, another app, or the in-app scanner) without dialling or
// redeeming anything, and returns its target as JSON for the confirmation the
// owner answers before pairing. The code is left out. It runs without a
// started core and refuses a link the redeem would refuse.
func InspectPairPayload(link string) (string, error) {
	payload, target, err := p2p.ParsePairPayload(link, time.Now())
	if err != nil {
		return "", err
	}
	out := pairTarget{PeerID: target.ID.String(), ExpiresAt: payload.ExpiresAt}
	// ParsePairPayload returns each address ending in the peer ID, which the
	// confirmation shows once rather than on every line.
	suffix := "/p2p/" + target.ID.String()
	for _, a := range payload.Addrs {
		out.Addrs = append(out.Addrs, pairAddr{Addr: strings.TrimSuffix(a, suffix), Local: p2p.PairAddrIsLocal(a)})
	}
	data, err := json.Marshal(out)
	return string(data), err
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
