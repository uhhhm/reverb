package api

import (
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/uhhhm/reverb/internal/core"
	"github.com/uhhhm/reverb/internal/recommend"
)

type mixList struct {
	Mixes []recommend.Mix `json:"mixes"`
}

type saveMixBody struct {
	Name string `json:"name"`
}

func (s *Server) handleShelves(w http.ResponseWriter, r *http.Request) {
	if s.deps.Recommend == nil {
		writeJSON(w, http.StatusOK, recommend.Shelves{Shelves: []recommend.Shelf{}})
		return
	}
	writeJSON(w, http.StatusOK, s.deps.Recommend.Shelves(r.Context()))
}

func (s *Server) handleMixes(w http.ResponseWriter, r *http.Request) {
	out := mixList{Mixes: []recommend.Mix{}}
	if s.deps.Recommend != nil {
		for _, kind := range recommend.MixKinds {
			out.Mixes = append(out.Mixes, s.deps.Recommend.Mix(r.Context(), kind))
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleMix(w http.ResponseWriter, r *http.Request) {
	kind := recommend.MixKind(chi.URLParam(r, "kind"))
	if !kind.Valid() {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "mix not found"})
		return
	}
	if s.deps.Recommend == nil {
		writeJSON(w, http.StatusOK, recommend.Mix{Kind: kind, Tracks: []core.ExternalResult{}})
		return
	}
	writeJSON(w, http.StatusOK, s.deps.Recommend.Mix(r.Context(), kind))
}

// handleSaveMix copies a Mix into a new managed playlist, search-source tracks
// included. The copy is an ordinary playlist: it replicates and can join an
// offline set, while the Mix itself stays a regenerated view (ADR 0002).
func (s *Server) handleSaveMix(w http.ResponseWriter, r *http.Request) {
	kind := recommend.MixKind(chi.URLParam(r, "kind"))
	if !kind.Valid() {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "mix not found"})
		return
	}
	svc := s.sync()
	if svc == nil || s.deps.Recommend == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "playlist service unavailable"})
		return
	}
	var body saveMixBody
	if r.ContentLength != 0 {
		if err := decode(r, &body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
			return
		}
	}
	mix := s.deps.Recommend.Mix(r.Context(), kind)
	if len(mix.Tracks) == 0 {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "mix is empty"})
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		name = kind.Title()
	}
	det, err := svc.CreateManaged(r.Context(), name)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	s.stampPlaylistOwner(r, det.ID)
	id := det.ID
	entries := make([]core.ExternalResult, 0, len(mix.Tracks))
	for _, t := range mix.Tracks {
		entries = append(entries, core.ExternalResult{
			Source: t.Source, ExternalID: t.ExternalID, Title: t.Title, Artist: t.Artist,
			Album: t.Album, ISRC: t.ISRC, MBID: t.MBID, DurationMs: t.DurationMs,
			CoverArtID: t.CoverArtID, Type: core.EntityTrack,
		})
	}
	// One edit, and nothing downloads: the copy downloads only if it joins an
	// offline set. A failed edit takes the empty playlist away again.
	det, _, err = svc.AddTracks(r.Context(), id, entries, false)
	if err != nil {
		if delErr := svc.Delete(r.Context(), id); delErr != nil {
			log.Printf("save mix: removing unfilled playlist %s: %v", id, delErr)
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, det)
}

// handlePlaylistSuggestions suggests songs for a managed playlist. A mirrored
// (synced-mode) playlist is rebuilt from upstream, so it gets none.
func (s *Server) handlePlaylistSuggestions(w http.ResponseWriter, r *http.Request) {
	none := recommend.TrackResult{Tracks: []core.ExternalResult{}}
	id := chi.URLParam(r, "id")
	svc := s.sync()
	if svc == nil || s.deps.Recommend == nil {
		writeJSON(w, http.StatusOK, none)
		return
	}
	if !s.playlistAccessAllowed(r, id) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "playlist not found"})
		return
	}
	det, err := svc.Detail(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "playlist not found"})
		return
	}
	if det.Mode != "once" {
		writeJSON(w, http.StatusOK, none)
		return
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	seeds := make([]recommend.Seed, 0, len(det.Tracks))
	for _, t := range det.Tracks {
		seeds = append(seeds, recommend.Seed{Artist: t.Artist, Title: t.Title})
	}
	writeJSON(w, http.StatusOK, s.deps.Recommend.PlaylistSuggestions(r.Context(), seeds, page))
}
