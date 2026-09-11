package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/uhhhm/reverb/internal/notinterested"
	"github.com/uhhhm/reverb/internal/store/db"
)

// NotInterestedService is the slice of *notinterested.Service the API needs.
// Marks and undos made here replicate to every paired device.
type NotInterestedService interface {
	Mark(ctx context.Context, m notinterested.Mark) (notinterested.Mark, error)
	Undo(ctx context.Context, key string) error
	List(ctx context.Context) ([]notinterested.Mark, error)
}

type notInterestedRequest struct {
	Kind       string `json:"kind"`
	Source     string `json:"source"`
	ExternalID string `json:"externalId"`
	TrackID    string `json:"trackId"`
	ID         string `json:"id"`
	Title      string `json:"title"`
	Artist     string `json:"artist"`
	Album      string `json:"album"`
	DurationMs int    `json:"durationMs"`
	Name       string `json:"name"`
}

func (s *Server) handleListNotInterested(w http.ResponseWriter, r *http.Request) {
	if s.deps.NotInterested == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "not interested unavailable"})
		return
	}
	marks, err := s.deps.NotInterested.List(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"marks": marks})
}

func (s *Server) handleMarkNotInterested(w http.ResponseWriter, r *http.Request) {
	if s.deps.NotInterested == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "not interested unavailable"})
		return
	}
	var req notInterestedRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	m, ok := s.notInterestedMark(r.Context(), req)
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "a track needs source and externalId (or trackId in the library); an artist needs a name"})
		return
	}
	m, err := s.deps.NotInterested.Mark(r.Context(), m)
	if errors.Is(err, notinterested.ErrInvalid) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, m)
}

func (s *Server) handleUndoNotInterested(w http.ResponseWriter, r *http.Request) {
	if s.deps.NotInterested == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "not interested unavailable"})
		return
	}
	var req struct {
		Key string `json:"key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Key == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "key is required"})
		return
	}
	if err := s.deps.NotInterested.Undo(r.Context(), req.Key); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// notInterestedMark builds the mark a request names, keyed on an identity
// every device derives the same way. Library tracks and artists are keyed on
// the library's original names, never on names a rename rewrote, or a mark
// made before a rename would not match one made after it.
func (s *Server) notInterestedMark(ctx context.Context, req notInterestedRequest) (notinterested.Mark, bool) {
	switch notinterested.Kind(req.Kind) {
	case notinterested.KindTrack:
		if req.Source == "library" {
			if req.TrackID == "" {
				return notinterested.Mark{}, false
			}
			if e, ok := s.catalogIdentity(ctx, req.TrackID); ok {
				return notinterested.LibraryTrackMark(e.Title, e.Artist, e.Album, int(e.DurationMs)), true
			}
			// Not yet in the catalog: the names the UI shows are all there is.
			if req.Title == "" || req.Artist == "" {
				return notinterested.Mark{}, false
			}
			return notinterested.LibraryTrackMark(req.Title, req.Artist, req.Album, req.DurationMs), true
		}
		if req.Source == "" || req.ExternalID == "" {
			return notinterested.Mark{}, false
		}
		return notinterested.TrackMark(req.Source, req.ExternalID, req.Title, req.Artist), true
	case notinterested.KindArtist:
		name := req.Name
		if req.Source == "library" && req.ID != "" {
			if lib := s.active().Library; lib != nil {
				if a, err := lib.GetArtist(ctx, req.ID); err == nil && a.Name != "" {
					name = a.Name
				}
			}
		}
		if name == "" {
			return notinterested.Mark{}, false
		}
		m := notinterested.ArtistMark(name)
		m.Source, m.ExternalID = req.Source, req.ID
		return m, true
	}
	return notinterested.Mark{}, false
}

// catalogIdentity returns the catalog entity a library track is bound to.
func (s *Server) catalogIdentity(ctx context.Context, backendID string) (db.CatalogEntity, bool) {
	if s.deps.Catalog == nil {
		return db.CatalogEntity{}, false
	}
	cid := s.deps.Overrides.CatalogIDsForTracks(ctx, []string{backendID})[backendID]
	if cid == "" {
		return db.CatalogEntity{}, false
	}
	e, err := s.deps.Catalog.GetCatalogEntity(ctx, cid)
	if err != nil || e.Title == "" {
		return db.CatalogEntity{}, false
	}
	return e, true
}
