package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/uhhhm/reverb/internal/offlineset"
	"github.com/uhhhm/reverb/internal/store/db"
)

// OfflineSetStore is the persistence slice the offline-set handlers need.
// *db.Queries satisfies it directly.
type OfflineSetStore interface {
	UpsertOfflineSet(ctx context.Context, arg db.UpsertOfflineSetParams) error
	ListOfflineSetForDevice(ctx context.Context, deviceID string) ([]db.OfflineSet, error)
	GetOfflineSetEntry(ctx context.Context, arg db.GetOfflineSetEntryParams) (db.OfflineSet, error)
	DeleteOfflineSetEntry(ctx context.Context, arg db.DeleteOfflineSetEntryParams) error
	GetSyncedPlaylist(ctx context.Context, id string) (db.SyncedPlaylist, error)
	CountSyncChanges(ctx context.Context) (int64, error)
	GetSetting(ctx context.Context, key string) (string, error)
	GetDeviceByID(ctx context.Context, id string) (db.Device, error)
	ListDevices(ctx context.Context) ([]db.Device, error)
}

// serverDeviceID returns the server device id (is_server=1) using the
// settings key server_device_id with a ListDevices fallback.
func (s *Server) serverDeviceID(ctx context.Context) (string, error) {
	if s.deps.OfflineSet == nil {
		return "", sql.ErrNoRows
	}
	// Try settings key.
	if id, err := s.deps.OfflineSet.GetSetting(ctx, "server_device_id"); err == nil && id != "" {
		if dev, err := s.deps.OfflineSet.GetDeviceByID(ctx, id); err == nil {
			return dev.ID, nil
		} else if !errors.Is(err, sql.ErrNoRows) {
			// fall through to list fallback on any error
		}
	}
	devices, err := s.deps.OfflineSet.ListDevices(ctx)
	if err != nil {
		return "", err
	}
	for _, d := range devices {
		if d.IsServer == 1 {
			return d.ID, nil
		}
	}
	return "", sql.ErrNoRows
}

type offlineSetListItem struct {
	PlaylistID   string `json:"playlistId"`
	Enabled      bool   `json:"enabled"`
	PlaylistName string `json:"playlistName"`
	UpdatedAt    int64  `json:"updatedAt"`
}

type offlineSetPutBody struct {
	Enabled *bool `json:"enabled"`
}

func (s *Server) handleListOfflineSet(w http.ResponseWriter, r *http.Request) {
	if s.deps.OfflineSet == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "offline set unavailable"})
		return
	}
	ctx := r.Context()
	deviceID, err := s.serverDeviceID(ctx)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not resolve server device"})
		return
	}
	svc := offlineset.NewService(s.deps.OfflineSet)
	entries, err := svc.ListForDevice(ctx, deviceID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not list offline set"})
		return
	}
	out := make([]offlineSetListItem, 0, len(entries))
	for _, e := range entries {
		name := ""
		if pl, err := s.deps.OfflineSet.GetSyncedPlaylist(ctx, e.PlaylistID); err == nil {
			name = pl.Name
		}
		out = append(out, offlineSetListItem{
			PlaylistID:   e.PlaylistID,
			Enabled:      e.Enabled,
			PlaylistName: name,
			UpdatedAt:    e.UpdatedAt,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleSetOfflineSet(w http.ResponseWriter, r *http.Request) {
	if s.deps.OfflineSet == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "offline set unavailable"})
		return
	}
	ctx := r.Context()
	deviceID, err := s.serverDeviceID(ctx)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not resolve server device"})
		return
	}
	playlistID := chi.URLParam(r, "playlistId")
	if playlistID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "playlistId is required"})
		return
	}
	var body offlineSetPutBody
	if err := decode(r, &body); err != nil || body.Enabled == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "enabled is required"})
		return
	}
	svc := offlineset.NewService(s.deps.OfflineSet)
	err = svc.Set(ctx, deviceID, playlistID, *body.Enabled)
	if err != nil {
		if errors.Is(err, offlineset.ErrPlaylistNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "playlist not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	entry, err := svc.Get(ctx, deviceID, playlistID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not read offline set entry"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"playlistId": entry.PlaylistID,
		"enabled":    entry.Enabled,
		"updatedAt":  entry.UpdatedAt,
	})
}

func (s *Server) handleDeleteOfflineSet(w http.ResponseWriter, r *http.Request) {
	if s.deps.OfflineSet == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "offline set unavailable"})
		return
	}
	ctx := r.Context()
	deviceID, err := s.serverDeviceID(ctx)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not resolve server device"})
		return
	}
	playlistID := chi.URLParam(r, "playlistId")
	if playlistID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "playlistId is required"})
		return
	}
	// Validate playlist exists — 404 if not.
	if _, err := s.deps.OfflineSet.GetSyncedPlaylist(ctx, playlistID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "playlist not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not validate playlist"})
		return
	}
	svc := offlineset.NewService(s.deps.OfflineSet)
	if err := svc.Remove(ctx, deviceID, playlistID); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
