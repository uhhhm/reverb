package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/uhhhm/reverb/internal/library/peaks"
)

// localTrackPath is deliberately optional: remote/Subsonic libraries do not
// expose filesystem paths, and should retain the flat seek rail.
type localTrackPath interface {
	LocalTrackPath(id string) (string, bool)
}

func (s *Server) handleTrackPeaks(w http.ResponseWriter, r *http.Request) {
	path, ok := s.localPathFor(r.Context(), chi.URLParam(r, "id"))
	if !ok || path == "" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	values, err := peaks.Compute(r.Context(), "ffmpeg", path, 200)
	if err != nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"peaks": values})
}
