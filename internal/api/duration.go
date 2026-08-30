package api

import (
	"log"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/uhhhm/reverb/internal/library/duration"
	"github.com/uhhhm/reverb/internal/store/db"
)

// handleTrackDuration reports how long a track actually plays, in ms.
//
// The tag is not asked. A VBR header extrapolated from the first frames, or a
// file re-muxed without its metadata being fixed, leaves the library reporting
// a length the audio does not have — the player then either runs past its own
// progress bar or appears to skip a track early. The file is decoded instead,
// which is the one source that cannot disagree with what is heard.
//
// Measurement is lazy and cached, like loudness: a decode costs about a second,
// so it runs when a track is first played rather than over the whole library.
//
// 204 means "no measurement available" — a remote library with no file to
// inspect, or no ffmpeg — and the player falls back to the reported length.
func (s *Server) handleTrackDuration(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if s.deps.Duration != nil {
		if row, err := s.deps.Duration.GetTrackDuration(r.Context(), id); err == nil {
			writeJSON(w, http.StatusOK, map[string]any{"durationMs": row.DurationMs})
			return
		}
	}
	path, ok := s.localPathFor(r.Context(), id)
	if !ok || path == "" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	ms, err := duration.Measure(r.Context(), "ffmpeg", path)
	if err != nil || ms <= 0 {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if s.deps.Duration != nil {
		if err := s.deps.Duration.UpsertTrackDuration(r.Context(), db.UpsertTrackDurationParams{
			TrackID:    id,
			DurationMs: ms,
			UpdatedAt:  time.Now().Unix(),
		}); err != nil {
			// A cache write failure only costs a re-measure next time.
			log.Printf("WARNING: could not cache track duration for %s: %v", id, err)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"durationMs": ms})
}
