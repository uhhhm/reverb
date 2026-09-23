package reverbcore

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/uhhhm/reverb/internal/p2p"
)

// get calls the running core's API as the app does, with the launch secret.
func get(url string) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set(SecretHeader, Secret())
	return http.DefaultClient.Do(req)
}

func health(t *testing.T, port int) error {
	t.Helper()
	resp, err := get(fmt.Sprintf("http://127.0.0.1:%d/api/v1/health", port))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var body struct{ Status string }
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK || body.Status != "ok" {
		return fmt.Errorf("health: %d %q", resp.StatusCode, body.Status)
	}
	return nil
}

// The app starts the core on launch, talks to it on its loopback port, stops
// it on termination, and starts it again over the same data directory when it
// comes back.
func TestStartServesThePhoneCoreAndStopReleasesIt(t *testing.T) {
	if testing.Short() {
		t.Skip("boots a runtime with a real libp2p host")
	}
	t.Setenv("REVERB_P2P_PORT", "0")
	dir := filepath.Join(t.TempDir(), "Reverb")
	port, err := Start(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(Stop)
	if err := health(t, port); err != nil {
		t.Fatal(err)
	}
	if again, err := Start(dir); err != nil || again != port || Port() != port {
		t.Fatalf("a second Start gave %d, %v; want the running core's %d", again, err, port)
	}
	var status struct {
		Mode string `json:"mode"`
	}
	resp, err := get(fmt.Sprintf("http://127.0.0.1:%d/api/v1/library/status", port))
	if err != nil {
		t.Fatal(err)
	}
	_ = json.NewDecoder(resp.Body).Decode(&status)
	resp.Body.Close()
	if status.Mode != "local" {
		t.Fatalf("library mode = %q, want the phone's local folder", status.Mode)
	}
	if _, err := os.Stat(filepath.Join(dir, "reverb.db")); err != nil {
		t.Fatalf("no database in the data directory: %v", err)
	}

	Stop()
	if Port() != 0 {
		t.Fatal("a stopped core still reports a port")
	}
	if err := health(t, port); err == nil {
		t.Fatal("the API still answers after Stop")
	}
	Stop()

	port, err = Start(dir)
	if err != nil {
		t.Fatalf("restart: %v", err)
	}
	if err := health(t, port); err != nil {
		t.Fatal(err)
	}
}

func TestSpotifyCredentialsStayInMemoryAndCanBeRevoked(t *testing.T) {
	if testing.Short() {
		t.Skip("boots a runtime with a real libp2p host")
	}
	t.Setenv("REVERB_P2P_PORT", "0")
	t.Setenv("REVERB_SPOTIFY_CLIENT_SECRET", "environment-must-not-win")
	dir := filepath.Join(t.TempDir(), "Reverb")
	SetSpotifyCredentials("keychain-id", "keychain-secret")
	defer func() { Stop(); SetSpotifyCredentials("", "") }()
	port, err := Start(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := running.rt.Getenv("REVERB_SPOTIFY_CLIENT_SECRET"); got != "keychain-secret" {
		t.Fatal("phone core did not use the in-memory credential")
	}
	if got := os.Getenv("REVERB_SPOTIFY_CLIENT_SECRET"); got != "environment-must-not-win" {
		t.Fatal("phone core changed the process environment")
	}
	if sources := running.rt.Bundle.Aggregator.Sources(); len(sources) != 2 || sources[1].Name() != "spotify" {
		t.Fatalf("provisioned sources = %+v", sources)
	}
	// Revoking reloads the running core's search sources in place.
	SetSpotifyCredentials("", "")
	if sources := running.rt.Reloader.SearchSourcesProvider()(); len(sources) != 1 || sources[0].Name() != "deezer" {
		t.Fatalf("revoked sources = %+v", sources)
	}
	if Port() != port {
		t.Fatal("revoking credentials restarted the core")
	}
}

// The app shows where a pairing link points before it pairs, so a link from a
// stranger cannot pair silently.
func TestInspectPairPayloadDescribesTheTarget(t *testing.T) {
	priv, _, err := crypto.GenerateEd25519Key(nil)
	if err != nil {
		t.Fatal(err)
	}
	pid, _ := peer.IDFromPrivateKey(priv)
	link, err := p2p.EncodePairPayload(p2p.PairPayload{
		Code: "AB12CD34", ExpiresAt: time.Now().Add(time.Minute).Unix(),
		Addrs: []string{"/ip4/192.168.1.20/tcp/4331/p2p/" + pid.String(), "/ip4/203.0.113.9/tcp/4331/p2p/" + pid.String()},
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := InspectPairPayload(link)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		PeerID string `json:"peerId"`
		Addrs  []struct {
			Addr  string `json:"addr"`
			Local bool   `json:"local"`
		} `json:"addrs"`
	}
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatal(err)
	}
	if got.PeerID != pid.String() || len(got.Addrs) != 2 ||
		got.Addrs[0].Addr != "/ip4/192.168.1.20/tcp/4331" || !got.Addrs[0].Local ||
		got.Addrs[1].Addr != "/ip4/203.0.113.9/tcp/4331" || got.Addrs[1].Local {
		t.Fatalf("inspected %s", raw)
	}
	if strings.Contains(raw, "AB12CD34") {
		t.Fatal("the description carries the pairing code")
	}
	if _, err := InspectPairPayload("reverb://pair?v=1"); err == nil {
		t.Fatal("a malformed link was described")
	}
}

// Loopback is not a trust boundary on a phone: another app can reach the
// port. Only a caller holding this launch's secret, which crosses the binding
// and never HTTP, is the owner.
func TestLoopbackAPIRequiresTheLaunchSecret(t *testing.T) {
	if testing.Short() {
		t.Skip("boots a runtime with a real libp2p host")
	}
	t.Setenv("REVERB_P2P_PORT", "0")
	dir := filepath.Join(t.TempDir(), "Reverb")
	port, err := Start(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(Stop)
	secret := Secret()
	if len(secret) < 32 {
		t.Fatalf("secret %q is too short", secret)
	}
	base := fmt.Sprintf("http://127.0.0.1:%d/api/v1", port)
	send := func(method, path, secret string) int {
		t.Helper()
		req, _ := http.NewRequest(method, base+path, strings.NewReader(`{}`))
		req.Header.Set("Content-Type", "application/json")
		if secret != "" {
			req.Header.Set(SecretHeader, secret)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	refused := []struct{ method, path string }{
		{http.MethodGet, "/health"},
		{http.MethodGet, "/library/status"},
		{http.MethodGet, "/ws"},
		{http.MethodPost, "/pairing/code"},
		{http.MethodPost, "/pairing/redeem"},
		{http.MethodPost, "/p2p/pair/redeem"},
		{http.MethodPost, "/p2p/pair/redeem-qr"},
		{http.MethodGet, "/no/such/route"},
	}
	for _, r := range refused {
		for _, s := range []string{"", "wrong", secret[:len(secret)-1]} {
			if got := send(r.method, r.path, s); got != http.StatusUnauthorized {
				t.Errorf("%s %s with secret %q: %d, want %d", r.method, r.path, s, got, http.StatusUnauthorized)
			}
		}
	}
	if got := send(http.MethodGet, "/library/status", secret); got != http.StatusOK {
		t.Fatalf("with the secret: %d", got)
	}
	if got := send(http.MethodPost, "/pairing/code", secret); got != http.StatusOK {
		t.Fatalf("minting a code with the secret: %d", got)
	}

	Stop()
	if Secret() != "" {
		t.Fatal("a stopped core still has a secret")
	}
	if _, err := Start(dir); err != nil {
		t.Fatal(err)
	}
	if again := Secret(); again == "" || again == secret {
		t.Fatal("the secret did not change with the start")
	}
}
