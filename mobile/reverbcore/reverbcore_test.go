package reverbcore

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func health(t *testing.T, port int) error {
	t.Helper()
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/api/v1/health", port))
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
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/api/v1/library/status", port))
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
