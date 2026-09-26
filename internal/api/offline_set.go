package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/uhhhm/reverb/internal/offlineset"
	"github.com/uhhhm/reverb/internal/store/db"
	reverbsync "github.com/uhhhm/reverb/internal/sync"
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

// OfflineKeeper is the phone's offline-set file keeper. *offlineset.Keeper
// satisfies it.
type OfflineKeeper interface {
	Status(ctx context.Context) (offlineset.Status, error)
	// Changed starts fetching and pruning for a changed offline set.
	Changed()
	// PendingUploads is the Downloads made here that no paired device holds yet.
	PendingUploads(ctx context.Context) ([]offlineset.PendingUpload, error)
}

// offlineSet is the server Device's Offline set. A change wakes the keeper,
// when there is one: a desktop keeps every file.
func (s *Server) offlineSet() *offlineset.Service {
	var changed func()
	if s.deps.OfflineKeeper != nil {
		changed = s.deps.OfflineKeeper.Changed
	}
	return offlineset.NewService(s.deps.OfflineSet, s.serverDeviceID, changed)
}

func (s *Server) handleOfflineSetStatus(w http.ResponseWriter, r *http.Request) {
	if s.deps.OfflineKeeper == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "offline storage is only tracked on a phone; a desktop keeps every file"})
		return
	}
	st, err := s.deps.OfflineKeeper.Status(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not read offline set status"})
		return
	}
	writeJSON(w, http.StatusOK, st)
}

// pendingUploadsResponse is GET /pending-uploads.
type pendingUploadsResponse struct {
	Files      []offlineset.PendingUpload `json:"files"`
	TotalBytes int64                      `json:"totalBytes"`
}

// handlePendingUploads lists a phone's Downloads that are still waiting for a
// paired device to hold them. They stay on the phone until then.
func (s *Server) handlePendingUploads(w http.ResponseWriter, r *http.Request) {
	if s.deps.OfflineKeeper == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "pending uploads are only kept on a phone; a desktop keeps every file"})
		return
	}
	files, err := s.deps.OfflineKeeper.PendingUploads(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not read pending uploads"})
		return
	}
	out := pendingUploadsResponse{Files: files}
	if out.Files == nil {
		out.Files = []offlineset.PendingUpload{}
	}
	for _, f := range out.Files {
		out.TotalBytes += f.SizeBytes
	}
	writeJSON(w, http.StatusOK, out)
}

// serverDeviceID returns the server device id (is_server=1) via the single
// canonical sync.ServerDeviceID implementation.
func (s *Server) serverDeviceID(ctx context.Context) (string, error) {
	if s.deps.OfflineSet == nil {
		return "", sql.ErrNoRows
	}
	return reverbsync.ServerDeviceID(ctx, s.deps.OfflineSet)
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
	entries, err := s.offlineSet().List(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not list offline set"})
		return
	}
	out := make([]offlineSetListItem, 0, len(entries))
	for _, e := range entries {
		out = append(out, offlineSetListItem{
			PlaylistID:   e.PlaylistID,
			Enabled:      e.Enabled,
			PlaylistName: e.PlaylistName,
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
	entry, err := s.offlineSet().Set(r.Context(), playlistID, *body.Enabled)
	if err != nil {
		writeOfflineSetError(w, err)
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
	playlistID := chi.URLParam(r, "playlistId")
	if playlistID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "playlistId is required"})
		return
	}
	if err := s.offlineSet().Remove(r.Context(), playlistID); err != nil {
		writeOfflineSetError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func writeOfflineSetError(w http.ResponseWriter, err error) {
	if errors.Is(err, offlineset.ErrPlaylistNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "playlist not found"})
		return
	}
	writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
}
