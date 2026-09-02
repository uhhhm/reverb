package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/uhhhm/reverb/internal/p2p"
	"github.com/uhhhm/reverb/internal/store/db"
	"github.com/uhhhm/reverb/internal/sync"
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
	// The owner is pairing right now; give the new code a full attempt budget
	// rather than whatever a stranger left of it.
	p2p.RearmPairAttempts()
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
	// This endpoint is unauthenticated by necessity — the device redeeming a
	// code has no credentials yet — so it needs its own brute-force bound.
	limiterKey := pairingClientKey(r)
	if !p2p.AllowPairAttempt(limiterKey) {
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "too many pairing attempts; try again later"})
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
	p2p.ResetPairAttempts(limiterKey)
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
	// Atomic FK cleanup + delete: wrap in a transaction when we have a *sql.DB.
	if sqlDB, ok := s.deps.PairingDB.(*sql.DB); ok {
		tx, err := sqlDB.BeginTx(r.Context(), nil)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		defer func() { _ = tx.Rollback() }()
		if err := deleteDeviceRows(r.Context(), tx, id); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		if err := tx.Commit(); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		s.invalidateP2PTrust()
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		return
	}
	// Fallback when PairingDB is not *sql.DB (e.g., *sql.Tx, mock, or nil).
	// Resolve an Exec handle: prefer PairingDB, else try PairingStore's underlying DB.
	var execHandle interface {
		ExecContext(context.Context, string, ...interface{}) (sql.Result, error)
	}
	if s.deps.PairingDB != nil {
		execHandle = s.deps.PairingDB
	} else if dbq, ok := s.deps.PairingStore.(*db.Queries); ok && dbq.UnderlyingDB() != nil {
		if eh, ok := dbq.UnderlyingDB().(interface {
			ExecContext(context.Context, string, ...interface{}) (sql.Result, error)
		}); ok {
			execHandle = eh
		}
	}
	// Try generic transaction if handle supports BeginTx.
	if execHandle != nil {
		if beginner, ok := execHandle.(interface {
			BeginTx(context.Context, *sql.TxOptions) (*sql.Tx, error)
		}); ok {
			if tx, err := beginner.BeginTx(r.Context(), nil); err == nil {
				defer func() { _ = tx.Rollback() }()
				if err := deleteDeviceRows(r.Context(), tx, id); err != nil {
					writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
					return
				}
				if err := tx.Commit(); err != nil {
					writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
					return
				}
				s.invalidateP2PTrust()
				writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
				return
			}
		}
	}
	// Non-transactional best-effort fallback.
	if err := s.deps.PairingStore.DeleteSyncCursor(r.Context(), id); err != nil && !errors.Is(err, sql.ErrNoRows) {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if execHandle != nil {
		if err := deleteDeviceReferences(r.Context(), execHandle, id); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
	}
	if err := s.deps.PairingStore.DeleteDevice(r.Context(), id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	s.invalidateP2PTrust()
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// deviceReferenceDeletes clears every row that references device(id). Only
// p2p_peer cascades, so each of the others has to be cleared by hand or the
// device delete fails the foreign key -- which is what "remove device" did for
// any peer that had ever synced, since sync_vector and file_manifest own a row
// per peer from its first accepted change.
var deviceReferenceDeletes = []string{
	`DELETE FROM sync_cursor WHERE device_id = ?`,
	`DELETE FROM pairing_code WHERE used_by_device_id = ?`,
	`DELETE FROM sync_change WHERE device_id = ?`,
	`DELETE FROM sync_vector WHERE device_id = ?`,
	`DELETE FROM file_manifest WHERE device_id = ?`,
	`DELETE FROM offline_set WHERE device_id = ?`,
	`DELETE FROM p2p_peer WHERE device_id = ?`,
}

type execer interface {
	ExecContext(context.Context, string, ...interface{}) (sql.Result, error)
}

func deleteDeviceReferences(ctx context.Context, ex execer, id string) error {
	for _, stmt := range deviceReferenceDeletes {
		if _, err := ex.ExecContext(ctx, stmt, id); err != nil {
			return err
		}
	}
	return nil
}

func deleteDeviceRows(ctx context.Context, ex execer, id string) error {
	if err := deleteDeviceReferences(ctx, ex, id); err != nil {
		return err
	}
	_, err := ex.ExecContext(ctx, `DELETE FROM device WHERE id = ?`, id)
	return err
}
