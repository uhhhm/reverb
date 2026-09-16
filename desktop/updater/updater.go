package updater

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
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

// assetRedirectHosts are the domains a release asset may be redirected to.
// GitHub answers a browser_download_url with a redirect to its object storage,
// so redirects cannot simply be refused -- but they must stay on GitHub. The
// whole githubusercontent.com suffix is allowed rather than the two asset hosts
// in use today, since GitHub has moved release downloads between hostnames
// before and a stricter list would break updates when it does again.
var assetRedirectHosts = []string{"github.com", "githubusercontent.com"}

// allowedAssetRedirect reports whether a redirect target is still a GitHub host
// reached over TLS. An asset becomes the running executable, so a redirect that
// moves the download to another origin -- or downgrades it to plaintext, where
// anyone on the path can answer -- is refused rather than followed.
func allowedAssetRedirect(u *url.URL) bool {
	if u.Scheme != "https" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	for _, domain := range assetRedirectHosts {
		if host == domain || strings.HasSuffix(host, "."+domain) {
			return true
		}
	}
	return false
}

// maxAssetRedirects matches net/http's own default hop limit; CheckRedirect
// replaces that default, so the limit has to be reimposed here.
const maxAssetRedirects = 10

// downloadClient fetches release assets. Separate from httpClient: an asset is
// tens of megabytes, so it gets no overall timeout — cancellation is the
// caller's context, and the transport still bounds connect and idle time.
//
// The first request goes wherever the release feed pointed; every redirect
// after it has to stay on GitHub over HTTPS.
var downloadClient = &http.Client{
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= maxAssetRedirects {
			return fmt.Errorf("stopped after %d redirects", maxAssetRedirects)
		}
		if !allowedAssetRedirect(req.URL) {
			return fmt.Errorf("refusing redirect to %s://%s: a release asset must come from GitHub over HTTPS", req.URL.Scheme, req.URL.Host)
		}
		return nil
	},
}

// errNothingStaged is returned when an install is requested with no verified
// payload waiting.
var errNothingStaged = errors.New("no update is ready to install")
