//go:build pyembed

package embedded

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/uhhhm/reverb/internal/ytdlpupdate"
)

// End-to-end: the release CLI signs a real yt-dlp package; HTTP delivers it;
// the updater authenticates it and the app's CPython interpreter activates it.
// Failures cross the same boundaries, with observable --version after each.
func TestSignedUpdateEndToEnd(t *testing.T) {
	bundle := os.Getenv("REVERB_TEST_SITE_PACKAGES")
	if bundle == "" {
		t.Skip("bundled packages required")
	}
	ctx := context.Background()
	version := func() string {
		lines, err := runLines(t, "yt_dlp", "--version")
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(strings.Join(lines, "\n"))
	}
	original := version()
	t.Cleanup(func() {
		if err := testRunner.ActivateYtDlp(ctx, bundle, original); err != nil {
			t.Error(err)
		}
	})
	pub, key, _ := ed25519.GenerateKey(nil)
	temp := t.TempDir()
	pubPath := filepath.Join(temp, "public-key.txt")
	must(os.WriteFile(pubPath, []byte(base64.StdEncoding.EncodeToString(pub)), 0600))
	assets := filepath.Join(temp, "assets")
	packageRelease := func(sequence, releaseVersion string, broken bool) {
		t.Helper()
		var b bytes.Buffer
		z := zip.NewWriter(&b)
		err := filepath.WalkDir(filepath.Join(bundle, "yt_dlp"), func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if d.Name() == "__pycache__" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(p, ".py") {
				return nil
			}
			name, _ := filepath.Rel(bundle, p)
			data, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			if name == "yt_dlp/version.py" {
				data = []byte("__version__ = '" + releaseVersion + "'\nRELEASE_GIT_HEAD = None\nVARIANT = None\nUPDATE_HINT = None\nCHANNEL = 'stable'\nORIGIN = 'yt-dlp/yt-dlp'\n")
			}
			if name == "yt_dlp/__init__.py" && broken {
				data = []byte("raise RuntimeError('incompatible release')\n")
			}
			w, err := z.Create(filepath.ToSlash(name))
			if err != nil {
				return err
			}
			_, err = w.Write(data)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		must(z.Close())
		archive := filepath.Join(temp, "package.zip")
		must(os.WriteFile(archive, b.Bytes(), 0600))
		cmd := exec.Command("go", "run", "./cmd/reverb-ytdlp-sign", "-package", archive, "-public-key", pubPath, "-out", assets, "-version", releaseVersion, "-sequence", sequence)
		cmd.Dir = "../../.."
		cmd.Env = append(os.Environ(), "YTDLP_SIGNING_KEY="+base64.StdEncoding.EncodeToString(key))
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("sign: %v\n%s", err, out)
		}
	}
	server := httptest.NewServer(http.FileServer(http.Dir(assets)))
	defer server.Close()
	root := filepath.Join(temp, "installed")
	u := ytdlpupdate.New(root, pub, testRunner.ActivateYtDlp)
	check := func() error { return u.Check(ctx, server.Client(), server.URL) }
	steps := []string{}
	packageRelease("1", "2099.01.01", false)
	// Prove that activation waits for a real Python job, not just its Go caller.
	hold := filepath.Join(modulesDir, "update_hold.py")
	must(os.WriteFile(hold, []byte("import time\nprint('running', flush=True)\ntime.sleep(0.8)\n"), 0600))
	started := make(chan struct{})
	done := make(chan error, 1)
	var once sync.Once
	go func() {
		done <- testRunner.RunModule(ctx, "update_hold", nil, func(string) { once.Do(func() { close(started) }) })
	}()
	<-started
	installed := make(chan error, 1)
	go func() { installed <- check() }()
	select {
	case err := <-installed:
		t.Fatalf("activated during Python job: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := <-installed; err != nil {
		t.Fatal(err)
	}
	if got := version(); got != "2099.01.01" {
		t.Fatalf("active version %s", got)
	}
	steps = append(steps, "signed release activated after in-flight Python job")
	packageRelease("2", "2099.01.02", false)
	sig := filepath.Join(assets, "manifest.sig")
	saved, err := os.ReadFile(sig)
	if err != nil {
		t.Fatal(err)
	}
	must(os.Remove(sig))
	if check() == nil {
		t.Fatal("accepted unsigned release")
	}
	must(os.WriteFile(sig, bytes.Repeat([]byte{1}, 64), 0600))
	if check() == nil {
		t.Fatal("accepted wrong signature")
	}
	must(os.WriteFile(sig, saved, 0600))
	must(os.WriteFile(filepath.Join(assets, "package.zip"), []byte("tampered"), 0600))
	if check() == nil {
		t.Fatal("accepted tampered archive")
	}
	if got := version(); got != "2099.01.01" {
		t.Fatalf("untrusted update changed runtime: %s", got)
	}
	steps = append(steps, "unsigned, wrong-signature and tampered packages refused")
	packageRelease("2", "2099.01.02", true)
	if check() == nil {
		t.Fatal("accepted incompatible package")
	}
	if got := version(); got != "2099.01.01" {
		t.Fatalf("rollback lost runtime: %s", got)
	}
	steps = append(steps, "failed Python import rolled back to working version")
	packageRelease("3", "2099.01.03", false)
	if err := check(); err != nil {
		t.Fatal(err)
	}
	if got := version(); got != "2099.01.03" {
		t.Fatalf("second update kept cached version: %s", got)
	}
	// Re-create the updater and interpreter imports, as on an app restart.
	if err := testRunner.ActivateYtDlp(ctx, bundle, original); err != nil {
		t.Fatal(err)
	}
	u = ytdlpupdate.New(root, pub, testRunner.ActivateYtDlp)
	if err := u.Restore(ctx); err != nil {
		t.Fatal(err)
	}
	if got := version(); got != "2099.01.03" {
		t.Fatalf("restart version: %s", got)
	}
	steps = append(steps, "second release replaced cached imports and survived restart")
	manifestBytes, err := os.ReadFile(filepath.Join(assets, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest ytdlpupdate.Manifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		t.Fatal(err)
	}
	must(os.WriteFile(filepath.Join(root, manifest.SHA256+".zip"), []byte("corrupt installed archive"), 0600))
	if err := testRunner.ActivateYtDlp(ctx, bundle, original); err != nil {
		t.Fatal(err)
	}
	u = ytdlpupdate.New(root, pub, testRunner.ActivateYtDlp)
	if err := u.Restore(ctx); err != nil {
		t.Fatal(err)
	}
	if got := version(); got != "2099.01.01" {
		t.Fatalf("corrupt archive fallback: %s", got)
	}
	if err := check(); err != nil {
		t.Fatal(err)
	}
	if got := version(); got != "2099.01.03" {
		t.Fatalf("manual check did not repair current signed release: %s", got)
	}
	steps = append(steps, "corrupt installed archive fell back and was repaired from the same signed release")
	if report := os.Getenv("REVERB_YTDLP_E2E_REPORT"); report != "" {
		b, _ := json.MarshalIndent(map[string]any{"passed": true, "bundledVersion": original, "installedVersion": version(), "steps": steps}, "", "  ")
		must(os.WriteFile(report, append(b, '\n'), 0600))
	}
}
