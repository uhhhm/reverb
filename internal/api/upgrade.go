package api

import (
	"net/http"
	"strings"

	"github.com/maxjb-xyz/reverb/internal/core"
)

// upgradeBody identifies the track to re-fetch. Source/externalId are used when
// known (the most reliable identity); otherwise the downloader falls back to an
// "<artist> - <title>" search, which is how spotDL finds a track anyway.
type upgradeBody struct {
	Source     string `json:"source"`
	ExternalID string `json:"externalId"`
	Artist     string `json:"artist"`
	Title      string `json:"title"`
	Album      string `json:"album"`
	Quality    string `json:"quality"`
	// CurrentQuality, when supplied, guards against pointless work: a request
	// that would not actually raise the tier is rejected rather than queued.
	CurrentQuality string `json:"currentQuality"`
}

// handleUpgradeDownload re-downloads a track at a higher quality tier, replacing
// the existing file. This is the only path that sets ForceOverwrite: both
// downloaders skip an existing target by default, which is exactly wrong here.
func (s *Server) handleUpgradeDownload(w http.ResponseWriter, r *http.Request) {
	dl := s.downloads()
	if dl == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "no downloader configured"})
		return
	}
	var body upgradeBody
	if err := decode(r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad request"})
		return
	}
	if strings.TrimSpace(body.Title) == "" || strings.TrimSpace(body.Artist) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "artist and title are required"})
		return
	}
	target := core.ParseAudioQuality(body.Quality, "")
	if !target.Valid() {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "quality must be one of: low, medium, high, best"})
		return
	}
	if current := core.ParseAudioQuality(body.CurrentQuality, ""); current.Valid() && !target.Exceeds(current) {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "that is not an upgrade over the current quality"})
		return
	}

	cu, _ := currentUser(r)
	job, err := dl.Enqueue(r.Context(), core.DownloadRequest{
		Source:         body.Source,
		ExternalID:     body.ExternalID,
		Artist:         body.Artist,
		Title:          body.Title,
		Album:          body.Album,
		Quality:        target,
		ForceOverwrite: true,
		InitiatedBy:    cu.ID,
	})
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, job)
}

// upgradableTrack is one completed download sitting below the target tier.
type upgradableTrack struct {
	JobID       string `json:"jobId"`
	Source      string `json:"source"`
	ExternalID  string `json:"externalId"`
	Artist      string `json:"artist"`
	Title       string `json:"title"`
	Album       string `json:"album"`
	Quality     string `json:"quality"`
	CanonicalID string `json:"canonicalId,omitempty"`
}

// handleListUpgradable reports completed downloads whose tier is below the
// target (the download_quality setting unless ?quality= overrides it).
//
// It reads Reverb's own download history rather than enumerating the library:
// a file Reverb did not fetch has no known source to re-fetch it from, and
// Navidrome exposes no bulk track listing to scan cheaply.
func (s *Server) handleListUpgradable(w http.ResponseWriter, r *http.Request) {
	dl := s.downloads()
	if dl == nil {
		writeJSON(w, http.StatusOK, []upgradableTrack{})
		return
	}
	target := core.ParseAudioQuality(r.URL.Query().Get("quality"), s.configuredQuality(r))
	jobs, err := dl.List(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not list downloads"})
		return
	}
	out := []upgradableTrack{}
	seen := map[string]bool{}
	for _, j := range jobs {
		if j.Status != core.DownloadCompleted {
			continue
		}
		// An empty tier predates this feature: treat it as spotDL's old 128k
		// default, which is what those files actually are.
		q := j.Quality
		if !q.Valid() {
			q = core.QualityLow
		}
		if !target.Exceeds(q) {
			continue
		}
		key := strings.ToLower(j.Artist + "\x00" + j.Title)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, upgradableTrack{
			JobID:       j.ID,
			Source:      j.Source,
			ExternalID:  j.ExternalID,
			Artist:      j.Artist,
			Title:       j.Title,
			Album:       j.Album,
			Quality:     string(q),
			CanonicalID: j.CanonicalID,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// configuredQuality reads the download_quality setting, falling back to the default.
func (s *Server) configuredQuality(r *http.Request) core.AudioQuality {
	if s.deps.Adapters == nil {
		return core.DefaultAudioQuality
	}
	v, err := s.deps.Adapters.GetSetting(r.Context(), keyDownloadQuality)
	if err != nil {
		return core.DefaultAudioQuality
	}
	return core.ParseAudioQuality(v, core.DefaultAudioQuality)
}
