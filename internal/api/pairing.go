package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/maxjb-xyz/reverb/internal/store/db"
	"github.com/maxjb-xyz/reverb/internal/sync"
)

// PairingStore is the persistence seam pairing handlers need.
// *db.Queries satisfies it.
type PairingStore interface {
	ListDevices(ctx context.Context) ([]db.Device, error)
	GetDeviceByID(ctx context.Context, id string) (db.Device, error)
	GetDeviceByTokenHash(ctx context.Context, tokenHash string) (db.Device, error)
	DeleteDevice(ctx context.Context, id string) error
	DeleteSyncCursor(ctx context.Context, deviceID string) error
	GetSetting(ctx context.Context, key string) (string, error)
	UpsertSetting(ctx context.Context, arg db.UpsertSettingParams) error
	TouchDeviceLastSeen(ctx context.Context, id string) error
}

type codeResponse struct {
	Code      string `json:"code"`
	ExpiresAt int64  `json:"expiresAt"`
}

func (s *Server) handlePairingCode(w http.ResponseWriter, r *http.Request) {
	if s.deps.Pairing == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "pairing unavailable"})
		return
	}
	code, expiresAt, err := s.deps.Pairing.GenerateCode(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, codeResponse{Code: code, ExpiresAt: expiresAt})
}

type redeemRequest struct {
	Code       string `json:"code"`
	DeviceName string `json:"deviceName"`
}

type redeemResponse struct {
	DeviceID       string `json:"deviceId"`
	Token          string `json:"token"`
	ServerDeviceID string `json:"serverDeviceId"`
}

func (s *Server) handlePairingRedeem(w http.ResponseWriter, r *http.Request) {
	if s.deps.Pairing == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "pairing unavailable"})
		return
	}
	var body redeemRequest
	if err := decode(r, &body); err != nil || body.Code == "" || body.DeviceName == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "code and deviceName are required"})
		return
	}
	deviceID, token, err := s.deps.Pairing.Redeem(r.Context(), body.Code, body.DeviceName)
	if err != nil {
		switch {
		case errors.Is(err, sync.ErrCodeInvalid):
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		case errors.Is(err, sync.ErrCodeExpired):
			writeJSON(w, http.StatusGone, map[string]string{"error": err.Error()})
		case errors.Is(err, sync.ErrCodeUsed):
			writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		default:
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return
	}
	serverID, _ := s.syncServerDeviceID(r.Context())
	writeJSON(w, http.StatusOK, redeemResponse{
		DeviceID:       deviceID,
		Token:          token,
		ServerDeviceID: serverID,
	})
}

type deviceDTO struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	IsServer  bool   `json:"isServer"`
	CreatedAt int64  `json:"createdAt"`
	LastSeen  int64  `json:"lastSeen"`
}

func toDeviceDTO(d db.Device) deviceDTO {
	return deviceDTO{
		ID:        d.ID,
		Name:      d.Name,
		IsServer:  d.IsServer == 1,
		CreatedAt: d.CreatedAt,
		LastSeen:  d.LastSeen,
	}
}

func (s *Server) handlePairingDevices(w http.ResponseWriter, r *http.Request) {
	if s.deps.PairingStore == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "pairing unavailable"})
		return
	}
	devices, err := s.deps.PairingStore.ListDevices(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not list devices"})
		return
	}
	out := make([]deviceDTO, 0, len(devices))
	for _, d := range devices {
		out = append(out, toDeviceDTO(d))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handlePairingDeviceDelete(w http.ResponseWriter, r *http.Request) {
	if s.deps.PairingStore == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "pairing unavailable"})
		return
	}
	id := chi.URLParam(r, "id")
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "id is required"})
		return
	}
	dev, err := s.deps.PairingStore.GetDeviceByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "device not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not get device"})
		return
	}
	if dev.IsServer == 1 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "cannot delete server device"})
		return
	}
	_ = s.deps.PairingStore.DeleteSyncCursor(r.Context(), id)
	// FK cleanup: pairing_code and sync_change reference device. Clear them before
	// deleting the device so the delete does not hit FOREIGN KEY constraint.
	if s.deps.PairingDB != nil {
		_, _ = s.deps.PairingDB.ExecContext(r.Context(), `DELETE FROM pairing_code WHERE used_by_device_id = ?`, id)
		_, _ = s.deps.PairingDB.ExecContext(r.Context(), `DELETE FROM sync_change WHERE device_id = ?`, id)
		_, _ = s.deps.PairingDB.ExecContext(r.Context(), `DELETE FROM sync_cursor WHERE device_id = ?`, id)
		// offline_set also references device, but deviceId+playlistId is composite; clean if present.
		_, _ = s.deps.PairingDB.ExecContext(r.Context(), `DELETE FROM offline_set WHERE device_id = ?`, id)
	}
	if err := s.deps.PairingStore.DeleteDevice(r.Context(), id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
