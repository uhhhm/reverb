package updater

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"
)

func TestLatestReleaseSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/maxjb-xyz/reverb/releases/latest" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if r.Method != http.MethodGet {
			t.Fatalf("unexpected method %s", r.Method)
		}
		resp := map[string]any{
			"tag_name": "v1.2.3",
			"body":     "release notes",
			"assets": []map[string]string{
				{"name": "reverb-desktop-v1.2.3-linux-amd64.deb", "browser_download_url": "https://example.com/a.deb"},
				{"name": "reverb-desktop-v1.2.3-darwin-arm64.zip", "browser_download_url": "https://example.com/b.zip"},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	origBase := githubAPIBase
	githubAPIBase = srv.URL
	defer func() { githubAPIBase = origBase }()

	rel, err := LatestRelease(context.Background(), "maxjb-xyz/reverb")
	if err != nil {
		t.Fatalf("LatestRelease error: %v", err)
	}
	if rel.Tag != "v1.2.3" {
		t.Fatalf("tag = %q want v1.2.3", rel.Tag)
	}
	if rel.Body != "release notes" {
		t.Fatalf("body = %q want release notes", rel.Body)
	}
	if len(rel.Assets) != 2 {
		t.Fatalf("assets len = %d want 2", len(rel.Assets))
	}
	if rel.Assets[0].Name != "reverb-desktop-v1.2.3-linux-amd64.deb" {
		t.Fatalf("asset0 name = %q", rel.Assets[0].Name)
	}
	if rel.Assets[0].URL != "https://example.com/a.deb" {
		t.Fatalf("asset0 url = %q", rel.Assets[0].URL)
	}
}

func TestLatestReleaseHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()

	origBase := githubAPIBase
	githubAPIBase = srv.URL
	defer func() { githubAPIBase = origBase }()

	_, err := LatestRelease(context.Background(), "maxjb-xyz/reverb")
	if err == nil {
		t.Fatal("expected error for 404")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Fatalf("error should contain 404, got %v", err)
	}
}

func TestPickAssetSelection(t *testing.T) {
	rel := &Release{
		Tag: "v1.2.3",
		Assets: []Asset{
			{Name: "reverb-desktop-v1.2.3-linux-amd64.deb", URL: "https://example.com/linux-deb"},
			{Name: "reverb-desktop-v1.2.3-linux-amd64.AppImage", URL: "https://example.com/appimage"},
			{Name: "reverb-desktop-v1.2.3-darwin-arm64.zip", URL: "https://example.com/darwin-zip"},
			{Name: "reverb-desktop-v1.2.3-darwin-amd64.zip", URL: "https://example.com/darwin-amd64"},
			{Name: "other.txt", URL: "https://example.com/other"},
		},
	}

	tests := []struct {
		goos, goarch string
		wantName     string
		wantNil      bool
	}{
		{"linux", "amd64", "reverb-desktop-v1.2.3-linux-amd64.deb", false},
		{"darwin", "arm64", "reverb-desktop-v1.2.3-darwin-arm64.zip", false},
		{"darwin", "amd64", "reverb-desktop-v1.2.3-darwin-amd64.zip", false},
		{"windows", "amd64", "", true},
		{"linux", "arm64", "", true},
	}
	for _, tc := range tests {
		got := PickAsset(rel, tc.goos, tc.goarch)
		if tc.wantNil {
			if got != nil {
				t.Fatalf("PickAsset(%q,%q) = %v want nil", tc.goos, tc.goarch, got.Name)
			}
			continue
		}
		if got == nil {
			t.Fatalf("PickAsset(%q,%q) = nil want %q", tc.goos, tc.goarch, tc.wantName)
		}
		if got.Name != tc.wantName {
			t.Fatalf("PickAsset(%q,%q) = %q want %q", tc.goos, tc.goarch, got.Name, tc.wantName)
		}
	}

	// Nil release returns nil.
	if got := PickAsset(nil, "linux", "amd64"); got != nil {
		t.Fatalf("PickAsset nil release = %v want nil", got)
	}

	// Empty assets returns nil.
	empty := &Release{Tag: "v1.0.0"}
	if got := PickAsset(empty, "linux", "amd64"); got != nil {
		t.Fatalf("PickAsset empty = %v want nil", got)
	}
}

func TestPickAssetPriority(t *testing.T) {
	// Linux should prefer .deb over .AppImage when both match.
	rel := &Release{
		Tag: "v1.2.3",
		Assets: []Asset{
			{Name: "reverb-desktop-v1.2.3-linux-amd64.AppImage", URL: "https://example.com/a"},
			{Name: "reverb-desktop-v1.2.3-linux-amd64.deb", URL: "https://example.com/b"},
		},
	}
	got := PickAsset(rel, "linux", "amd64")
	if got == nil || got.Name != "reverb-desktop-v1.2.3-linux-amd64.deb" {
		t.Fatalf("linux priority: got %v want deb", got)
	}

	// Darwin should prefer zip.
	rel2 := &Release{
		Tag: "v1.2.3",
		Assets: []Asset{
			{Name: "reverb-desktop-v1.2.3-darwin-arm64.deb", URL: "https://example.com/a"},
			{Name: "reverb-desktop-v1.2.3-darwin-arm64.zip", URL: "https://example.com/b"},
		},
	}
	got2 := PickAsset(rel2, "darwin", "arm64")
	if got2 == nil || !strings.HasSuffix(got2.Name, ".zip") {
		t.Fatalf("darwin priority: got %v want zip", got2)
	}
}

func TestIsNewerSemver(t *testing.T) {
	tests := []struct {
		current, latest string
		want            bool
	}{
		{"v1.0.0", "v1.0.1", true},
		{"v1.0.1", "v1.0.0", false},
		{"v1.0.0", "v1.0.0", false},
		{"v1.2.3", "v1.10.0", true},
		{"v0.9.9", "v1.0.0", true},
		{"1.0.0", "1.0.1", true},
		{"v1.0.0", "v1.0.0-beta", false},
		{"v1.0.0-beta", "v1.0.0", true},
		{"dev", "v1.0.0", false},
		{"v1.0.0", "dev", false},
		{"", "v1.0.0", false},
		{"v1.0.0", "", false},
		{"v1.2.3", "v1.2.3", false},
		{"v1.2", "v1.2.1", true},
	}
	for _, tc := range tests {
		got := IsNewer(tc.current, tc.latest)
		if got != tc.want {
			t.Fatalf("IsNewer(%q,%q)=%v want %v", tc.current, tc.latest, got, tc.want)
		}
	}
}

func TestCheckAndEmit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"tag_name": "v2.0.0",
			"body":     "new release",
			"assets":   []any{},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	origBase := githubAPIBase
	githubAPIBase = srv.URL
	defer func() { githubAPIBase = origBase }()

	// Older current -> available
	ok, tag := CheckAndEmit(context.Background(), "v1.0.0")
	if !ok || tag != "v2.0.0" {
		t.Fatalf("CheckAndEmit old -> ok=%v tag=%q want true v2.0.0", ok, tag)
	}

	// Same version -> not available
	ok, tag = CheckAndEmit(context.Background(), "v2.0.0")
	if ok || tag != "" {
		t.Fatalf("CheckAndEmit same -> ok=%v tag=%q want false", ok, tag)
	}

	// Newer current -> not available
	ok, tag = CheckAndEmit(context.Background(), "v3.0.0")
	if ok {
		t.Fatalf("CheckAndEmit newer current -> ok=%v want false", ok)
	}

	// dev -> not available
	ok, tag = CheckAndEmit(context.Background(), "dev")
	if ok {
		t.Fatalf("CheckAndEmit dev -> ok=%v want false", ok)
	}
}

func TestUpgradeYtDlpCommandConstruction(t *testing.T) {
	var capturedName string
	var capturedArgs []string
	origExec := ExecCommand
	defer func() { ExecCommand = origExec }()

	ExecCommand = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		capturedName = name
		capturedArgs = append([]string(nil), args...)
		// Use 'true' which exits 0 without needing python.
		return exec.CommandContext(ctx, "true")
	}

	// Explicit python bin
	if err := UpgradeYtDlp(context.Background(), "/usr/bin/python3"); err != nil {
		t.Fatalf("UpgradeYtDlp error: %v", err)
	}
	if capturedName != "/usr/bin/python3" {
		t.Fatalf("python bin = %q want /usr/bin/python3", capturedName)
	}
	wantArgs := []string{"-m", "pip", "install", "--upgrade", "yt-dlp"}
	if strings.Join(capturedArgs, " ") != strings.Join(wantArgs, " ") {
		t.Fatalf("args = %v want %v", capturedArgs, wantArgs)
	}

	// Default python bin when empty
	capturedName = ""
	capturedArgs = nil
	if err := UpgradeYtDlp(context.Background(), ""); err != nil {
		t.Fatalf("UpgradeYtDlp default error: %v", err)
	}
	if capturedName != DefaultPythonBin {
		t.Fatalf("default python = %q want %q", capturedName, DefaultPythonBin)
	}
}

func TestUpgradeYtDlpFailure(t *testing.T) {
	origExec := ExecCommand
	defer func() { ExecCommand = origExec }()
	ExecCommand = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		// 'false' exits 1
		return exec.CommandContext(ctx, "false")
	}
	err := UpgradeYtDlp(context.Background(), "python3")
	if err == nil {
		t.Fatal("expected error for failing command")
	}
}
