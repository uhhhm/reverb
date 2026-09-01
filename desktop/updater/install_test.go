package updater

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"
)

// fakeBinary returns bytes that pass verifyExecutable on this platform: the
// right magic number and comfortably over the minimum size.
func fakeBinary(marker string) []byte {
	var magic []byte
	switch runtime.GOOS {
	case "linux":
		magic = []byte("\x7fELF")
	case "darwin":
		magic = []byte{0xcf, 0xfa, 0xed, 0xfe}
	default:
		magic = []byte("MZ\x00\x00")
	}
	b := append([]byte{}, magic...)
	b = append(b, []byte(marker)...)
	return append(b, bytes.Repeat([]byte{0x41}, 2<<20)...)
}

func zipOf(t *testing.T, name string, payload []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// releaseServer serves a GitHub release feed whose single asset is a zip
// holding payload, named the way the desktop workflow names them.
func releaseServer(t *testing.T, tag string, payload []byte) *httptest.Server {
	t.Helper()
	assetName := fmt.Sprintf("reverb-desktop-%s-%s-%s.zip", tag, runtime.GOOS, runtime.GOARCH)
	archive := zipOf(t, "reverb-desktop", payload)
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	mux.HandleFunc("/asset.zip", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(len(archive)))
		_, _ = w.Write(archive)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tag_name": tag,
			"body":     "notes for " + tag,
			"assets": []any{map[string]any{
				"name":                 assetName,
				"browser_download_url": srv.URL + "/asset.zip",
			}},
		})
	})
	t.Cleanup(srv.Close)
	return srv
}

func useServer(t *testing.T, srv *httptest.Server) {
	t.Helper()
	orig := githubAPIBase
	githubAPIBase = srv.URL
	t.Cleanup(func() { githubAPIBase = orig })
}

// The whole point of the feature: a newer release is downloaded in the
// background and left staged, and the running binary is untouched until the
// install is explicitly asked for.
func TestCheckNowStagesWithoutInstalling(t *testing.T) {
	dataDir := t.TempDir()
	payload := fakeBinary("v2")
	useServer(t, releaseServer(t, "v2.0.0", payload))

	exe := filepath.Join(t.TempDir(), "reverb-desktop")
	if err := os.WriteFile(exe, fakeBinary("v1"), 0o755); err != nil {
		t.Fatal(err)
	}

	svc := New(Options{Repo: "owner/name", CurrentVersion: "v1.0.0", DataDir: dataDir, ExePath: exe})
	st := svc.CheckNow(context.Background())

	if st.Staged != "v2.0.0" {
		t.Fatalf("Staged=%q err=%q want v2.0.0", st.Staged, st.Error)
	}
	if st.Downloading || st.Progress != 1 {
		t.Fatalf("after staging: Downloading=%v Progress=%v want false 1", st.Downloading, st.Progress)
	}
	if got, err := os.ReadFile(exe); err != nil || !bytes.Equal(got, fakeBinary("v1")) {
		t.Fatalf("running binary was modified by a check; it must not be")
	}

	su, ok := ReadStaged(dataDir)
	if !ok || su.Tag != "v2.0.0" || su.Notes != "notes for v2.0.0" {
		t.Fatalf("ReadStaged = %+v ok=%v", su, ok)
	}

	// Applying swaps the payload in and keeps the outgoing binary as a backup.
	if err := ApplyStaged(dataDir, exe); err != nil {
		t.Fatalf("ApplyStaged: %v", err)
	}
	if got, _ := os.ReadFile(exe); !bytes.Equal(got, payload) {
		t.Fatalf("binary was not replaced with the staged payload")
	}
	if got, err := os.ReadFile(exe + backupSuffix); err != nil || !bytes.Equal(got, fakeBinary("v1")) {
		t.Fatalf("outgoing binary was not kept as %s: %v", backupSuffix, err)
	}
	if fi, err := os.Stat(exe); err != nil || fi.Mode().Perm()&0o111 == 0 {
		t.Fatalf("installed binary is not executable: %v", err)
	}
}

// A second check with the payload already staged must not download it again.
func TestCheckNowSkipsAlreadyStagedTag(t *testing.T) {
	dataDir := t.TempDir()
	srv := releaseServer(t, "v2.0.0", fakeBinary("v2"))
	useServer(t, srv)

	var downloads int
	orig := downloadClient.Transport
	downloadClient.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		downloads++
		return http.DefaultTransport.RoundTrip(r)
	})
	t.Cleanup(func() { downloadClient.Transport = orig })

	svc := New(Options{Repo: "owner/name", CurrentVersion: "v1.0.0", DataDir: dataDir})
	svc.CheckNow(context.Background())
	svc.CheckNow(context.Background())

	if downloads != 1 {
		t.Fatalf("asset fetched %d times, want 1", downloads)
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// A payload corrupted after download must not be installed over a working
// binary.
func TestReadStagedRejectsCorruptedPayload(t *testing.T) {
	dataDir := t.TempDir()
	useServer(t, releaseServer(t, "v2.0.0", fakeBinary("v2")))
	svc := New(Options{Repo: "owner/name", CurrentVersion: "v1.0.0", DataDir: dataDir})
	if st := svc.CheckNow(context.Background()); st.Staged == "" {
		t.Fatalf("setup: nothing staged (%q)", st.Error)
	}

	su, _ := ReadStaged(dataDir)
	if err := os.WriteFile(su.File, fakeBinary("tampered"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, ok := ReadStaged(dataDir); ok {
		t.Fatal("ReadStaged accepted a payload whose digest no longer matches")
	}

	exe := filepath.Join(t.TempDir(), "reverb-desktop")
	if err := os.WriteFile(exe, fakeBinary("v1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := ApplyStaged(dataDir, exe); err == nil {
		t.Fatal("ApplyStaged installed a corrupted payload")
	}
	if got, _ := os.ReadFile(exe); !bytes.Equal(got, fakeBinary("v1")) {
		t.Fatal("a refused install still modified the binary")
	}
}

// A truncated or error-page download is not an executable and must be refused
// before it reaches the binary.
func TestStagingRejectsUndersizedPayload(t *testing.T) {
	dataDir := t.TempDir()
	useServer(t, releaseServer(t, "v2.0.0", []byte("<html>404</html>")))
	svc := New(Options{Repo: "owner/name", CurrentVersion: "v1.0.0", DataDir: dataDir})
	st := svc.CheckNow(context.Background())
	if st.Staged != "" {
		t.Fatalf("staged a non-executable payload: %+v", st)
	}
	if st.Error == "" {
		t.Fatal("no error reported for a non-executable payload")
	}
	if st.Available != "v2.0.0" {
		t.Fatalf("Available=%q; a failed download should not hide the release", st.Available)
	}
}

func TestInstallAndRestartWithoutStagedUpdate(t *testing.T) {
	svc := New(Options{Repo: "owner/name", CurrentVersion: "v1.0.0", DataDir: t.TempDir()})
	if err := svc.InstallAndRestart(); err == nil {
		t.Fatal("InstallAndRestart succeeded with nothing staged")
	}
}

// A release with no build for this platform is reported, not staged.
func TestNoAssetForPlatform(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tag_name": "v2.0.0",
			"assets": []any{map[string]any{
				"name":                 "reverb-desktop-v2.0.0-plan9-mips.zip",
				"browser_download_url": "http://example.invalid/x.zip",
			}},
		})
	}))
	defer srv.Close()
	useServer(t, srv)

	svc := New(Options{Repo: "owner/name", CurrentVersion: "v1.0.0", DataDir: t.TempDir()})
	st := svc.CheckNow(context.Background())
	if st.Staged != "" || st.Error == "" {
		t.Fatalf("st=%+v want no staged payload and an explanatory error", st)
	}
}

// A staged update outlives the process that downloaded it, so the prompt is
// still there after an unrelated restart.
func TestNewSurfacesPreviouslyStagedUpdate(t *testing.T) {
	dataDir := t.TempDir()
	useServer(t, releaseServer(t, "v2.0.0", fakeBinary("v2")))
	if st := New(Options{Repo: "owner/name", CurrentVersion: "v1.0.0", DataDir: dataDir}).
		CheckNow(context.Background()); st.Staged == "" {
		t.Fatalf("setup: nothing staged (%q)", st.Error)
	}

	fresh := New(Options{Repo: "owner/name", CurrentVersion: "v1.0.0", DataDir: dataDir})
	if got := fresh.Status().Staged; got != "v2.0.0" {
		t.Fatalf("fresh service Staged=%q want v2.0.0", got)
	}
	// ...but not once that version is the one running.
	current := New(Options{Repo: "owner/name", CurrentVersion: "v2.0.0", DataDir: dataDir})
	if got := current.Status().Staged; got != "" {
		t.Fatalf("Staged=%q after the update was installed, want empty", got)
	}
}

func TestCleanupAfterUpdate(t *testing.T) {
	dataDir := t.TempDir()
	useServer(t, releaseServer(t, "v2.0.0", fakeBinary("v2")))
	svc := New(Options{Repo: "owner/name", CurrentVersion: "v1.0.0", DataDir: dataDir})
	if st := svc.CheckNow(context.Background()); st.Staged == "" {
		t.Fatalf("setup: nothing staged (%q)", st.Error)
	}
	exe := filepath.Join(t.TempDir(), "reverb-desktop")
	if err := os.WriteFile(exe, fakeBinary("v2"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exe+backupSuffix, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}

	CleanupAfterUpdate(dataDir, exe, "v2.0.0")

	if _, err := os.Stat(exe + backupSuffix); !os.IsNotExist(err) {
		t.Fatal("backup binary survived cleanup")
	}
	if _, ok := ReadStaged(dataDir); ok {
		t.Fatal("staged manifest for the running version survived cleanup")
	}

	// An update staged for a version newer than the one running is kept.
	useServer(t, releaseServer(t, "v3.0.0", fakeBinary("v3")))
	New(Options{Repo: "owner/name", CurrentVersion: "v2.0.0", DataDir: dataDir}).CheckNow(context.Background())
	CleanupAfterUpdate(dataDir, exe, "v2.0.0")
	if _, ok := ReadStaged(dataDir); !ok {
		t.Fatal("cleanup discarded an update staged for a newer version")
	}
}

func TestWaitForPredecessor(t *testing.T) {
	dataDir := t.TempDir()
	// No marker: returns immediately.
	done := make(chan struct{})
	go func() { WaitForPredecessor(dataDir, time.Second); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("WaitForPredecessor blocked with no marker present")
	}

	// A marker naming a process that has already exited is consumed and does
	// not hold up the boot.
	if err := os.MkdirAll(StagingDir(dataDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(relaunchMarker(dataDir), []byte(strconv.Itoa(exitedPID(t))), 0o644); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	WaitForPredecessor(dataDir, 5*time.Second)
	if time.Since(start) > 3*time.Second {
		t.Fatal("WaitForPredecessor waited on a process that had already exited")
	}
	if _, err := os.Stat(relaunchMarker(dataDir)); !os.IsNotExist(err) {
		t.Fatal("relaunch marker was not consumed")
	}
}

// exitedPID runs a trivial command to completion and returns its pid. A reaped
// child's pid is not alive, and the kernel will not reassign it this quickly,
// so it stands in for a predecessor that has already quit.
func exitedPID(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("true")
	if err := cmd.Run(); err != nil {
		t.Fatalf("spawning a throwaway process: %v", err)
	}
	return cmd.Process.Pid
}

func TestDownloadAssetRejectsDeb(t *testing.T) {
	_, err := DownloadAsset(context.Background(), Asset{Name: "reverb-desktop-v2-linux-amd64.deb", URL: "http://example.invalid"}, t.TempDir(), nil)
	if err == nil {
		t.Fatal("DownloadAsset accepted a .deb, which cannot be installed without root")
	}
}

func TestUnzipRejectsTraversal(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "evil.zip")
	if err := os.WriteFile(path, zipOf(t, "../../escape", []byte("x")), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := unzipSingle(path, dir); err == nil {
		t.Fatal("unzipSingle accepted an entry escaping the destination")
	}
}
