package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/uhhhm/reverb/internal/tastesettings"
)

// TasteSettingsService is the slice of *tastesettings.Service the API needs.
// Changes made here replicate to every paired device.
type TasteSettingsService interface {
	Get(ctx context.Context) (tastesettings.Settings, error)
	Update(ctx context.Context, p tastesettings.Patch) (tastesettings.Settings, error)
}

func (s *Server) handleGetRecommendationSettings(w http.ResponseWriter, r *http.Request) {
	if s.deps.TasteSettings == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "recommendation settings unavailable"})
		return
	}
	st, err := s.deps.TasteSettings.Get(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) handleUpdateRecommendationSettings(w http.ResponseWriter, r *http.Request) {
	if s.deps.TasteSettings == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "recommendation settings unavailable"})
		return
	}
	var p tastesettings.Patch
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	st, err := s.deps.TasteSettings.Update(r.Context(), p)
	if errors.Is(err, tastesettings.ErrInvalid) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, st)
}
