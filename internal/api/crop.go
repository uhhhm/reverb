package api

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/uhhhm/reverb/internal/crop"
)

// handleGetTrackCrop reports a track's playback boundaries. A track with no
// crop reports zeroes, which mean "the whole file".
func (s *Server) handleGetTrackCrop(w http.ResponseWriter, r *http.Request) {
	if s.deps.Crop == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "cropping unavailable"})
		return
	}
	id := chi.URLParam(r, "id")
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing track id"})
		return
	}
	points, err := s.deps.Crop.Get(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, points)
}

// handleSetTrackCrop records playback boundaries for a track. Nothing is
// re-encoded — the file keeps every sample, and the crop can be changed or
// removed later, which is why cropping twice is not a lossy operation.
func (s *Server) handleSetTrackCrop(w http.ResponseWriter, r *http.Request) {
	if s.deps.Crop == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "cropping unavailable"})
		return
	}
	id := chi.URLParam(r, "id")
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing track id"})
		return
	}
	var body crop.Points
	if err := decode(r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	if err := s.deps.Crop.Set(r.Context(), id, body); err != nil {
		if errors.Is(err, crop.ErrInvalid) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	s.emitTrackCrop(r.Context(), id, body.StartMs, body.EndMs)
	s.handleGetTrackCrop(w, r)
}

// handleDeleteTrackCrop uncrops a track — the file was never modified, so this
// simply restores full-length playback.
func (s *Server) handleDeleteTrackCrop(w http.ResponseWriter, r *http.Request) {
	if s.deps.Crop == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "cropping unavailable"})
		return
	}
	id := chi.URLParam(r, "id")
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing track id"})
		return
	}
	if err := s.deps.Crop.Clear(r.Context(), id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	s.emitTrackCrop(r.Context(), id, 0, 0)
	writeJSON(w, http.StatusOK, crop.Points{})
}
