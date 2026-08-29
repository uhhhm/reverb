package api

import (
	"net/http"
	"strings"

	"github.com/uhhhm/reverb/internal/core"
)

// upgradeBody identifies the track to re-fetch. The source/externalId pair is
// the only identity an upgrade may run on: without it the downloader would fall
// back to an "<artist> - <title>" web search, which for a track whose tags are
// filename-shaped ("01 - Dunanna Pit") happily returns a completely different
// song and then overwrites the original with it. When the caller does not know
// the pair, it is recovered from Reverb's own download history; if that lookup
// fails the request is refused.
type upgradeBody struct {
	Source     string `json:"source"`
	ExternalID string `json:"externalId"`
	// LibraryTrackID, when supplied, is the strongest handle for finding the
	// original download job and therefore the source the file came from.
	LibraryTrackID string `json:"libraryTrackId"`
	Artist         string `json:"artist"`
	Title          string `json:"title"`
	Album          string `json:"album"`
	Quality        string `json:"quality"`
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

	req := core.DownloadRequest{
		Source:         body.Source,
		ExternalID:     body.ExternalID,
		Artist:         body.Artist,
		Title:          body.Title,
		Album:          body.Album,
		Quality:        target,
		ForceOverwrite: true,
	}
	if req.Source == "" || req.ExternalID == "" {
		prev, ok := s.findOriginalDownload(r, body)
		if !ok {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{
				"error": "Reverb has no record of where this track came from, so it cannot re-fetch it safely",
			})
			return
		}
		req.Source = prev.Source
		req.ExternalID = prev.ExternalID
		if req.Album == "" {
			req.Album = prev.Album
		}
		req.ISRC = prev.ISRC
		req.DurationMs = prev.DurationMs
	}

	cu, _ := currentUser(r)
	req.InitiatedBy = cu.ID
	job, err := dl.Enqueue(r.Context(), req)
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
	// LibraryTrackID lets the SPA tell whether a row it is rendering is one of
	// these, and therefore whether an upgrade is offerable at all.
	LibraryTrackID string `json:"libraryTrackId,omitempty"`
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
		// No source/externalId means no way to re-fetch this exact recording.
		if j.Source == "" || j.ExternalID == "" {
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
		key := strings.ToLower(strings.TrimSpace(j.Artist) + "\x00" + strings.TrimSpace(j.Title))
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, upgradableTrack{
			JobID:          j.ID,
			Source:         j.Source,
			ExternalID:     j.ExternalID,
			Artist:         j.Artist,
			Title:          j.Title,
			Album:          j.Album,
			Quality:        string(q),
			CanonicalID:    j.CanonicalID,
			LibraryTrackID: j.LibraryTrackID,
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

// findOriginalDownload recovers the source/externalId a track was originally
// fetched with, so an upgrade re-fetches that exact recording rather than
// whatever a text search happens to surface. Matches on the library track id
// when the caller knows it, otherwise on artist+title.
func (s *Server) findOriginalDownload(r *http.Request, body upgradeBody) (core.DownloadJob, bool) {
	dl := s.downloads()
	if dl == nil {
		return core.DownloadJob{}, false
	}
	jobs, err := dl.List(r.Context())
	if err != nil {
		return core.DownloadJob{}, false
	}
	want := strings.ToLower(strings.TrimSpace(body.Artist) + "\x00" + strings.TrimSpace(body.Title))
	var fallback core.DownloadJob
	var haveFallback bool
	for _, j := range jobs {
		if j.Status != core.DownloadCompleted || j.Source == "" || j.ExternalID == "" {
			continue
		}
		if body.LibraryTrackID != "" && j.LibraryTrackID == body.LibraryTrackID {
			return j, true
		}
		if body.LibraryTrackID == "" && !haveFallback && strings.ToLower(strings.TrimSpace(j.Artist)+"\x00"+strings.TrimSpace(j.Title)) == want {
			fallback, haveFallback = j, true
		}
	}
	return fallback, haveFallback
}
