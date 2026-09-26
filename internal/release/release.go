// Package release reports installable phone releases without installing them.
package release

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"golang.org/x/mod/semver"
)

// Status is the newest installable phone release, if newer than this build,
// and where the owner gets it.
type Status struct {
	LatestVersion string `json:"latestVersion"`
	SourceURL     string `json:"sourceUrl"`
	ReleaseURL    string `json:"releaseUrl"`
}

// Tracker checks GitHub for a newer release that carries an IPA.
type Tracker struct {
	mu            sync.Mutex
	repo, current string
	checked       time.Time
	status        Status
	Client        *http.Client
}

var repository = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

// New tracks releases of repo ("owner/name") newer than current. An invalid
// repo disables checks.
func New(repo, current string) *Tracker {
	if !repository.MatchString(repo) {
		repo = ""
	}
	s := Status{}
	if repo != "" {
		s.SourceURL = "https://github.com/" + repo + "/releases/download/ios-source/source.json"
	}
	return &Tracker{repo: repo, current: current, status: s, Client: &http.Client{Timeout: 5 * time.Second}}
}

// Status retains the last good answer after a network failure and limits checks
// to one per hour. Development builds still expose the source, without a
// prompt. Concurrent callers get the last answer while a check is out.
func (t *Tracker) Status(ctx context.Context) Status {
	t.mu.Lock()
	if t.repo == "" || !semver.IsValid(version(t.current)) || time.Since(t.checked) < time.Hour {
		defer t.mu.Unlock()
		return t.status
	}
	t.checked = time.Now()
	status := t.status
	t.mu.Unlock()
	if latest, ok := t.latest(ctx); ok {
		status.LatestVersion = latest
		status.ReleaseURL = "https://github.com/" + t.repo + "/releases/tag/" + latest
		t.mu.Lock()
		t.status = status
		t.mu.Unlock()
	}
	return status
}

// latest is the newest stable release tag newer than this build that carries
// Reverb.ipa.
func (t *Tracker) latest(ctx context.Context) (string, bool) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/repos/"+t.repo+"/releases/latest", nil)
	if err != nil {
		return "", false
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := t.Client.Do(req)
	if err != nil {
		return "", false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", false
	}
	var latest struct {
		Tag        string `json:"tag_name"`
		Draft      bool   `json:"draft"`
		Prerelease bool   `json:"prerelease"`
		Assets     []struct {
			Name string `json:"name"`
		} `json:"assets"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&latest) != nil || latest.Draft || latest.Prerelease || !semver.IsValid(version(latest.Tag)) {
		return "", false
	}
	if semver.Compare(version(latest.Tag), version(t.current)) <= 0 {
		return "", false
	}
	for _, a := range latest.Assets {
		if a.Name == "Reverb.ipa" {
			return latest.Tag, true
		}
	}
	return "", false
}

func version(v string) string { return "v" + strings.TrimPrefix(v, "v") }
