package api

import (
	"net/http"

	"github.com/uhhhm/reverb/internal/core"
)

// The bulk read and batch write behind the Manage tracks page. Its list shows a
// standing quality per row, which one request per track would turn into an N+1
// against the database, and its selection applies one tier to many tracks at
// once.

// trackQualityIndex is every per-track override plus the tier a track with no
// override falls back to. Only overridden tracks appear, so the map is the size
// of what the user has actually changed rather than of the library.
type trackQualityIndex struct {
	Default   string            `json:"default"`
	Overrides map[string]string `json:"overrides"`
}

// handleListTrackQuality reports the whole override table in one read.
func (s *Server) handleListTrackQuality(w http.ResponseWriter, r *http.Request) {
	out := trackQualityIndex{
		Default:   string(s.configuredQuality(r)),
		Overrides: map[string]string{},
	}
	if s.deps.TrackQuality == nil {
		writeJSON(w, http.StatusOK, out)
		return
	}
	rows, err := s.deps.TrackQuality.ListTrackQualityOverrides(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	for _, row := range rows {
		// A row holding something this build does not recognise is not a tier
		// the picker can show, so it reads as "no override" rather than as a
		// value the user never chose.
		if q := core.ParseAudioQuality(row.Quality, ""); q.Valid() {
			out.Overrides[row.TrackID] = string(q)
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// batchQualityRequest sets one tier on many tracks. A blank quality clears the
// override on every id, which is how "follow the default again" travels.
type batchQualityRequest struct {
	TrackIDs []string `json:"trackIds"`
	Quality  string   `json:"quality"`
}

// handleBatchTrackQuality applies one tier across a selection. Like the rename
// batch, a failing id is reported rather than fatal: one track that no longer
// exists must not discard the rest of what the user selected.
func (s *Server) handleBatchTrackQuality(w http.ResponseWriter, r *http.Request) {
	if s.deps.TrackQuality == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "per-track quality unavailable"})
		return
	}
	var body batchQualityRequest
	if err := decode(r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	if len(body.TrackIDs) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "at least one track id is required"})
		return
	}
	if len(body.TrackIDs) > maxBatchItems {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "too many items in one batch"})
		return
	}
	q := core.ParseAudioQuality(body.Quality, "")
	if body.Quality != "" && !q.Valid() {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "quality must be one of: low, medium, high, best"})
		return
	}
	out := batchRenameResponse{}
	for _, id := range body.TrackIDs {
		if id == "" {
			continue
		}
		if err := s.setTrackQuality(r, id, q); err != nil {
			out.fail(id, err)
			continue
		}
		out.Applied++
	}
	writeJSON(w, http.StatusOK, out)
}
