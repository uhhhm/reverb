package api

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/uhhhm/reverb/internal/core"
	"github.com/uhhhm/reverb/internal/store/db"
)

var errNoTrackQualityStore = errors.New("per-track quality unavailable")

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
	// for the tier the file is already at is rejected rather than queued.
	CurrentQuality string `json:"currentQuality"`
	// SetOverride persists Quality as this track's standing quality, so future
	// re-fetches use it instead of the global download_quality setting.
	SetOverride bool `json:"setOverride"`
}

// handleUpgradeDownload re-downloads a track at a different quality tier,
// replacing the existing file. The tier may be lower than the current one — a
// deliberate downgrade to save space is as valid as an upgrade; only a re-fetch
// at the tier the file already has is refused as a no-op. This is the only path
// that sets ForceOverwrite: both downloaders skip an existing target by
// default, which is exactly wrong here.
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
	// No explicit tier means "use this track's standing quality" — its override
	// when it has one, otherwise the global setting.
	target := core.ParseAudioQuality(body.Quality, "")
	if !target.Valid() && strings.TrimSpace(body.Quality) == "" {
		target = s.qualityForTrack(r, body.LibraryTrackID)
	}
	if !target.Valid() {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "quality must be one of: low, medium, high, best"})
		return
	}
	if current := core.ParseAudioQuality(body.CurrentQuality, ""); current.Valid() && current == target {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "this track is already at that quality"})
		return
	}
	if body.SetOverride && body.LibraryTrackID != "" {
		if err := s.setTrackQuality(r, body.LibraryTrackID, target); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not save the quality for this track"})
			return
		}
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

// handleListUpgradable reports completed downloads whose tier DIFFERS from the
// target (the download_quality setting unless ?quality= overrides it) — both
// the ones below it and, when the target is lower, the ones above it.
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
	// ?all=1 drops the tier filter: the per-track quality picker needs every
	// track Reverb can re-fetch, including the ones already at the target.
	all := r.URL.Query().Get("all") == "1" || r.URL.Query().Get("all") == "true"
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
		if !all && q == target {
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

// qualityForTrack resolves the tier to (re-)fetch a track at: the per-track
// override wins, then the global download_quality setting, then the default.
func (s *Server) qualityForTrack(r *http.Request, libraryTrackID string) core.AudioQuality {
	if libraryTrackID != "" && s.deps.TrackQuality != nil {
		if row, err := s.deps.TrackQuality.GetTrackQualityOverride(r.Context(), libraryTrackID); err == nil {
			if q := core.ParseAudioQuality(row.Quality, ""); q.Valid() {
				return q
			}
		}
	}
	return s.configuredQuality(r)
}

// setTrackQuality persists (or, for an invalid tier, clears) a track's standing
// quality override.
func (s *Server) setTrackQuality(r *http.Request, libraryTrackID string, q core.AudioQuality) error {
	if s.deps.TrackQuality == nil {
		return errNoTrackQualityStore
	}
	if !q.Valid() {
		return s.deps.TrackQuality.DeleteTrackQualityOverride(r.Context(), libraryTrackID)
	}
	return s.deps.TrackQuality.UpsertTrackQualityOverride(r.Context(), db.UpsertTrackQualityOverrideParams{
		TrackID:   libraryTrackID,
		Quality:   string(q),
		UpdatedAt: time.Now().Unix(),
	})
}

// handleGetTrackQuality reports a track's standing quality: its override when
// it has one, otherwise the global setting it would fall back to.
func (s *Server) handleGetTrackQuality(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing track id"})
		return
	}
	overridden := false
	if s.deps.TrackQuality != nil {
		if row, err := s.deps.TrackQuality.GetTrackQualityOverride(r.Context(), id); err == nil {
			overridden = core.ParseAudioQuality(row.Quality, "").Valid()
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"quality":    string(s.qualityForTrack(r, id)),
		"overridden": overridden,
		"default":    string(s.configuredQuality(r)),
	})
}

// handleSetTrackQuality records (or clears, with an empty quality) a track's
// standing quality override. It only stores the preference — re-fetching at the
// new tier is a separate, explicit action.
func (s *Server) handleSetTrackQuality(w http.ResponseWriter, r *http.Request) {
	if s.deps.TrackQuality == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "per-track quality unavailable"})
		return
	}
	id := chi.URLParam(r, "id")
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing track id"})
		return
	}
	var body struct {
		Quality string `json:"quality"`
	}
	if err := decode(r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	q := core.ParseAudioQuality(body.Quality, "")
	if strings.TrimSpace(body.Quality) != "" && !q.Valid() {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "quality must be one of: low, medium, high, best"})
		return
	}
	if err := s.setTrackQuality(r, id, q); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	s.handleGetTrackQuality(w, r)
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
