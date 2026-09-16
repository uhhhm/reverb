package updater

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"golang.org/x/mod/semver"

	"github.com/uhhhm/reverb/internal/config"
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

// PickAsset selects the executable zip produced by the desktop workflow.
// Packages such as .deb require a package manager, and an AppImage cannot be
// installed by replacing the executable inside its read-only mount.
func PickAsset(rel *Release, goos, goarch string) *Asset {
	if rel == nil {
		return nil
	}
	suffix := "-" + strings.ToLower(goos) + "-" + strings.ToLower(goarch) + ".zip"
	for i := range rel.Assets {
		name := strings.ToLower(rel.Assets[i].Name)
		if strings.HasPrefix(name, "reverb-desktop-") && strings.HasSuffix(name, suffix) {
			return &rel.Assets[i]
		}
	}
	return nil
}

// IsNewer follows semantic-version precedence, ignoring build metadata and
// rejecting development labels or malformed tags.
func IsNewer(current, latest string) bool {
	normalize := func(v string) string {
		return "v" + strings.TrimPrefix(strings.TrimSpace(v), "v")
	}
	current, latest = normalize(current), normalize(latest)
	return semver.IsValid(current) && semver.IsValid(latest) && semver.Compare(latest, current) > 0
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

// downloadClient fetches release assets. Separate from httpClient: an asset is
// tens of megabytes, so it gets no overall timeout — cancellation is the
// caller's context, and the transport still bounds connect and idle time.
var downloadClient = &http.Client{}

// errNothingStaged is returned when an install is requested with no verified
// payload waiting.
var errNothingStaged = errors.New("no update is ready to install")
