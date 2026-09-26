package ytdlpupdate_test

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/uhhhm/reverb/internal/ytdlpupdate"
)

// Failure inventory: missing/wrong signatures, changed bytes, unsupported
// manifests, replay/downgrade, traversal, symlinks, oversized downloads,
// broken Python activation, interrupted persistence, and restart with corrupt
// installed bytes. None may replace the last working package.
type release struct{ manifest, signature, archive []byte }

func signed(t *testing.T, key ed25519.PrivateKey, sequence int64, files map[string]string) release {
	t.Helper()
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	for name, body := range files {
		w, err := z.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(body))
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(b.Bytes())
	manifest, _ := json.Marshal(ytdlpupdate.Manifest{Format: 1, Sequence: sequence, Version: "2026.09.26", SHA256: hex.EncodeToString(sum[:])})
	return release{manifest, ed25519.Sign(key, manifest), b.Bytes()}
}
func TestCheckAuthenticatesBeforeActivationAndKeepsWorkingRelease(t *testing.T) {
	pub, key, _ := ed25519.GenerateKey(nil)
	_, wrong, _ := ed25519.GenerateKey(nil)
	good := signed(t, key, 1, map[string]string{"yt_dlp/__init__.py": "working"})
	next := good
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/manifest.json":
			_, _ = w.Write(next.manifest)
		case "/manifest.sig":
			_, _ = w.Write(next.signature)
		case "/package.zip":
			_, _ = w.Write(next.archive)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	active := "bundle"
	activate := func(_ context.Context, dir, version string) error {
		b, err := os.ReadFile(filepath.Join(dir, "yt_dlp", "__init__.py"))
		if err != nil {
			return err
		}
		if string(b) == "broken" {
			return errors.New("cannot import")
		}
		active = string(b)
		return nil
	}
	root := t.TempDir()
	u := ytdlpupdate.New(root, pub, activate)
	check := func() error { return u.Check(context.Background(), server.Client(), server.URL) }
	if err := check(); err != nil {
		t.Fatal(err)
	}
	if active != "working" || u.Version() != "2026.09.26" {
		t.Fatalf("activation/version: %s %s", active, u.Version())
	}
	cases := []struct {
		name   string
		change func()
	}{
		{"unsigned", func() {
			next = signed(t, key, 2, map[string]string{"yt_dlp/__init__.py": "untrusted"})
			next.signature = nil
		}},
		{"wrong signer", func() { next = signed(t, wrong, 2, map[string]string{"yt_dlp/__init__.py": "untrusted"}) }},
		{"tampered archive", func() {
			next = signed(t, key, 2, map[string]string{"yt_dlp/__init__.py": "untrusted"})
			next.archive[0] ^= 1
		}},
		{"tampered manifest", func() { next = good; next.manifest = append(append([]byte{}, good.manifest...), ' ') }},
		{"traversal", func() {
			next = signed(t, key, 2, map[string]string{"../escape.py": "bad", "yt_dlp/__init__.py": "untrusted"})
		}},
		{"native code", func() {
			next = signed(t, key, 2, map[string]string{"yt_dlp/evil.so": "bad", "yt_dlp/__init__.py": "untrusted"})
		}},
		{"activation failure", func() { next = signed(t, key, 2, map[string]string{"yt_dlp/__init__.py": "broken"}) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.change()
			if err := check(); err == nil {
				t.Fatal("accepted invalid update")
			}
			if active != "working" {
				t.Fatalf("lost working package: %s", active)
			}
		})
	}
	next = signed(t, key, 2, map[string]string{"yt_dlp/__init__.py": "new"})
	if err := check(); err != nil {
		t.Fatal(err)
	}
	next = good
	if err := check(); err != nil {
		t.Fatal(err)
	}
	if active != "new" {
		t.Fatal("replayed an old update")
	}
	restarted := ytdlpupdate.New(root, pub, activate)
	active = "bundle"
	if err := restarted.Restore(context.Background()); err != nil {
		t.Fatal(err)
	}
	if active != "new" {
		t.Fatal("restart lost installed update")
	}
	// The last known-good archive remains available if the current one is damaged.
	entries, _ := filepath.Glob(filepath.Join(root, "*.zip"))
	for _, p := range entries {
		b, _ := os.ReadFile(p)
		if bytes.Equal(b, next.archive) {
			continue
		}
		_ = os.WriteFile(p, []byte("damaged"), 0600)
	}
	active = "bundle"
	restarted = ytdlpupdate.New(root, pub, activate)
	if err := restarted.Restore(context.Background()); err != nil {
		t.Fatal(err)
	}
	if active != "working" {
		t.Fatalf("did not fall back after corrupt update: %s", active)
	}
}
