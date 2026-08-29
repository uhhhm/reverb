package api

import (
	"log"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/uhhhm/reverb/internal/library/loudness"
	"github.com/uhhhm/reverb/internal/store/db"
)

// handleTrackGain reports the playback gain, in dB, that brings a track to
// Reverb's reference level.
//
// Measurement is lazy and cached: it runs ffmpeg over the file the first time a
// track is asked about, then stores the result. Measuring the whole library up
// front would spend hours of CPU on tracks nobody plays, and the player only
// needs the gain for the track it is about to start.
//
// 204 means "no gain available" — a remote library with no file to inspect, or
// no ffmpeg — and the player then plays the track unmodified.
func (s *Server) handleTrackGain(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if s.deps.Loudness != nil {
		if row, err := s.deps.Loudness.GetTrackLoudness(r.Context(), id); err == nil {
			writeJSON(w, http.StatusOK, map[string]any{"gainDb": row.GainDb})
			return
		}
	}
	paths, ok := s.library().(localTrackPath)
	if !ok {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	path, ok := paths.LocalTrackPath(id)
	if !ok || path == "" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	gain, err := loudness.Measure(r.Context(), "ffmpeg", path)
	if err != nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if s.deps.Loudness != nil {
		if err := s.deps.Loudness.UpsertTrackLoudness(r.Context(), db.UpsertTrackLoudnessParams{
			TrackID:   id,
			GainDb:    gain,
			UpdatedAt: time.Now().Unix(),
		}); err != nil {
			// A cache write failure only costs a re-measure next time.
			log.Printf("WARNING: could not cache track loudness for %s: %v", id, err)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"gainDb": gain})
}
