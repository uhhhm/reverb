package api

import (
	"context"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/uhhhm/reverb/internal/core"
	"github.com/uhhhm/reverb/internal/recommend"
)

// Recommendations is the slice of *recommend.Service the API needs. Lookups
// never fail: a source that errors or times out yields an empty result, so a
// recommendation section hides instead of breaking the page it sits on.
type Recommendations interface {
	SimilarArtists(ctx context.Context, source, id string) recommend.ArtistResult
	SimilarTracks(ctx context.Context, artist, title string) recommend.TrackResult
}

func (s *Server) handleSimilarArtists(w http.ResponseWriter, r *http.Request) {
	if s.deps.Recommend == nil {
		writeJSON(w, http.StatusOK, recommend.ArtistResult{Artists: []core.ExternalArtist{}})
		return
	}
	writeJSON(w, http.StatusOK, s.deps.Recommend.SimilarArtists(r.Context(), chi.URLParam(r, "source"), chi.URLParam(r, "id")))
}

func (s *Server) handleSimilarTracks(w http.ResponseWriter, r *http.Request) {
	artist := strings.TrimSpace(r.URL.Query().Get("artist"))
	title := strings.TrimSpace(r.URL.Query().Get("title"))
	if artist == "" || title == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "artist and title are required"})
		return
	}
	if s.deps.Recommend == nil {
		writeJSON(w, http.StatusOK, recommend.TrackResult{Tracks: []core.ExternalResult{}})
		return
	}
	writeJSON(w, http.StatusOK, s.deps.Recommend.SimilarTracks(r.Context(), artist, title))
}
