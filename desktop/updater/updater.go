package updater

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/maxjb-xyz/reverb/internal/config"
)

// Release holds the tag, body and assets for a GitHub release.
type Release struct {
	Tag    string
	Body   string
	Assets []Asset
}

// Asset is a single release artifact.
type Asset struct {
	Name string
	URL  string
}

// DefaultRepo is the GitHub repository used for update checks (stable channel)
// when the caller passes no repo. Configurable via REVERB_UPDATE_REPO /
// --update-repo; see internal/config.
const DefaultRepo = config.DefaultUpdateRepo

// githubAPIBase is the base URL for GitHub API; overridden in tests via
// httptest server URL.
var githubAPIBase = "https://api.github.com"

// httpClient is the client used for GitHub API calls. Overridable for tests.
var httpClient = &http.Client{Timeout: 15 * time.Second}

// githubReleaseResponse mirrors the JSON returned by GitHub's releases/latest.
type githubReleaseResponse struct {
	TagName string `json:"tag_name"`
	Body    string `json:"body"`
	Assets  []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
	} `json:"assets"`
}

// LatestRelease fetches the latest release for repo via unauthenticated GET
// to https://api.github.com/repos/<repo>/releases/latest.
func LatestRelease(ctx context.Context, repo string) (*Release, error) {
	if repo == "" {
		repo = DefaultRepo
	}
	url := fmt.Sprintf("%s/repos/%s/releases/latest", strings.TrimRight(githubAPIBase, "/"), repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	req.Header.Set("User-Agent", "reverb-updater")
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github releases: %s %d", url, resp.StatusCode)
	}
	var gr githubReleaseResponse
	if err := json.NewDecoder(resp.Body).Decode(&gr); err != nil {
		return nil, err
	}
	rel := &Release{
		Tag:  gr.TagName,
		Body: gr.Body,
	}
	for _, a := range gr.Assets {
		rel.Assets = append(rel.Assets, Asset{Name: a.Name, URL: a.BrowserDownloadURL})
	}
	return rel, nil
}

// PickAsset selects the asset matching the given GOOS/GOARCH.
// Pattern: reverb-desktop-$VERSION-$GOOS-$GOARCH.{zip,deb,AppImage}
// Returns nil if no match.
func PickAsset(rel *Release, goos, goarch string) *Asset {
	if rel == nil {
		return nil
	}
	goos = strings.ToLower(goos)
	goarch = strings.ToLower(goarch)
	var best *Asset
	bestScore := -1
	for i := range rel.Assets {
		a := &rel.Assets[i]
		nameLower := strings.ToLower(a.Name)
		if !strings.Contains(nameLower, goos) {
			continue
		}
		if !strings.Contains(nameLower, goarch) {
			continue
		}
		if !strings.Contains(nameLower, "reverb-desktop") {
			continue
		}
		// Must have expected extension.
		hasExt := strings.HasSuffix(nameLower, ".zip") || strings.HasSuffix(nameLower, ".deb") || strings.HasSuffix(nameLower, ".appimage")
		if !hasExt {
			continue
		}
		score := 0
		// Prefer zip for darwin/windows, deb for linux.
		switch {
		case strings.HasSuffix(nameLower, ".zip"):
			score = 3
		case strings.HasSuffix(nameLower, ".deb"):
			score = 2
		case strings.HasSuffix(nameLower, ".appimage"):
			score = 1
		}
		// On darwin prefer zip, on linux prefer deb.
		if goos == "darwin" && strings.HasSuffix(nameLower, ".zip") {
			score += 10
		}
		if goos == "linux" && strings.HasSuffix(nameLower, ".deb") {
			score += 10
		}
		if score > bestScore {
			bestScore = score
			best = a
		}
	}
	return best
}

// IsNewer reports whether latest is newer than current using semver comparison.
// It strips a leading "v" and does numeric dot-separated comparison.
// "dev" or empty versions are never considered newer.
func IsNewer(current, latest string) bool {
	if latest == "" || current == "" {
		return false
	}
	if latest == current {
		return false
	}
	cur := strings.TrimSpace(current)
	lat := strings.TrimSpace(latest)
	if cur == "dev" || lat == "dev" {
		return false
	}
	cur = strings.TrimPrefix(cur, "v")
	lat = strings.TrimPrefix(lat, "v")
	if cur == lat {
		return false
	}
	// Split off pre-release/build metadata (e.g. 1.2.3-beta+001)
	curCore := strings.SplitN(cur, "-", 2)[0]
	latCore := strings.SplitN(lat, "-", 2)[0]
	curCore = strings.SplitN(curCore, "+", 2)[0]
	latCore = strings.SplitN(latCore, "+", 2)[0]

	curParts := strings.Split(curCore, ".")
	latParts := strings.Split(latCore, ".")

	maxLen := len(curParts)
	if len(latParts) > maxLen {
		maxLen = len(latParts)
	}
	for i := 0; i < maxLen; i++ {
		var curN, latN int
		if i < len(curParts) {
			fmt.Sscan(curParts[i], &curN)
		}
		if i < len(latParts) {
			fmt.Sscan(latParts[i], &latN)
		}
		if latN > curN {
			return true
		}
		if latN < curN {
			return false
		}
	}
	// Numeric core equal — if latest has pre-release and current doesn't, latest is not newer.
	// Simple heuristic: release (no suffix) > pre-release.
	curHasPre := strings.Contains(strings.TrimPrefix(current, "v"), "-")
	latHasPre := strings.Contains(strings.TrimPrefix(latest, "v"), "-")
	if curHasPre && !latHasPre {
		// e.g. current 1.2.3-beta, latest 1.2.3 => newer
		return true
	}
	if !curHasPre && latHasPre {
		return false
	}
	// Fallback lexical compare for pre-release identifiers.
	return lat > cur
}

// CheckAndEmit checks repo for a release newer than currentVersion.
// If a newer tag is found it returns (true, tag). Channel is stable only.
// An empty repo means update checks are disabled.
func CheckAndEmit(ctx context.Context, repo, currentVersion string) (bool, string) {
	if repo == "" {
		return false, ""
	}
	rel, err := LatestRelease(ctx, repo)
	if err != nil {
		log.Printf("updater: check failed: %v", err)
		return false, ""
	}
	if IsNewer(currentVersion, rel.Tag) {
		// TODO: selfupdate Apply placeholder — actual binary replacement via
		// github.com/creativeprojects/go-selfupdate is deferred to avoid CGO dep.
		// For now we just report availability; future App wiring will call
		// Apply(asset) and emit wails event "update:available".
		log.Printf("updater: update available %s -> %s", currentVersion, rel.Tag)
		return true, rel.Tag
	}
	return false, ""
}

// StartPollers launches background goroutines that periodically check for app
// updates (every 6h) and yt-dlp upgrades (every 24h). It returns immediately.
// Context cancellation stops both pollers.
// An empty repo disables the release poller; yt-dlp upgrades still run.
func StartPollers(ctx context.Context, repo, currentVersion string) {
	go pollUpdates(ctx, repo, currentVersion)
	go pollYtDlp(ctx)
}

func pollUpdates(ctx context.Context, repo, currentVersion string) {
	if repo == "" {
		return
	}
	// Immediate check on start.
	_, _ = CheckAndEmit(ctx, repo, currentVersion)

	ticker := time.NewTicker(6 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_, _ = CheckAndEmit(ctx, repo, currentVersion)
		}
	}
}

func pollYtDlp(ctx context.Context) {
	// Immediate attempt best-effort (log only).
	_ = UpgradeYtDlp(ctx, "")

	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = UpgradeYtDlp(ctx, "")
		}
	}
}
