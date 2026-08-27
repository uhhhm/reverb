package offlineset

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/uhhhm/reverb/internal/store/db"
)

// Entry is the domain view of an offline_set row.
type Entry struct {
	DeviceID   string
	PlaylistID string
	Enabled    bool
	UpdatedAt  int64
}

// Querier is the store seam for offline_set plus the playlist existence check
// and the sync invariant helper. *db.Queries satisfies it.
type Querier interface {
	UpsertOfflineSet(ctx context.Context, arg db.UpsertOfflineSetParams) error
	ListOfflineSetForDevice(ctx context.Context, deviceID string) ([]db.OfflineSet, error)
	GetOfflineSetEntry(ctx context.Context, arg db.GetOfflineSetEntryParams) (db.OfflineSet, error)
	DeleteOfflineSetEntry(ctx context.Context, arg db.DeleteOfflineSetEntryParams) error
	GetSyncedPlaylist(ctx context.Context, id string) (db.SyncedPlaylist, error)
	CountSyncChanges(ctx context.Context) (int64, error)
}

// Service manages per-device offline_set rows. The table is local-only and
// never emits sync_change rows (D3).
type Service struct {
	q Querier
}

// NewService creates an offline set service.
func NewService(q Querier) *Service {
	return &Service{q: q}
}

// ErrPlaylistNotFound is returned when Set references a playlist that does not exist.
var ErrPlaylistNotFound = errors.New("playlist not found")

// ErrEntryNotFound is returned when Get cannot find an offline entry.
var ErrEntryNotFound = errors.New("offline set entry not found")

// Set validates the playlist exists and upserts the offline entry for the device.
func (s *Service) Set(ctx context.Context, deviceID, playlistID string, enabled bool) error {
	if _, err := s.q.GetSyncedPlaylist(ctx, playlistID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrPlaylistNotFound
		}
		return err
	}
	var en int64
	if enabled {
		en = 1
	}
	return s.q.UpsertOfflineSet(ctx, db.UpsertOfflineSetParams{
		DeviceID:   deviceID,
		PlaylistID: playlistID,
		Enabled:    en,
		UpdatedAt:  time.Now().UnixMilli(),
	})
}

// ListForDevice returns all offline entries for a device, ordered by playlist_id.
func (s *Service) ListForDevice(ctx context.Context, deviceID string) ([]Entry, error) {
	rows, err := s.q.ListOfflineSetForDevice(ctx, deviceID)
	if err != nil {
		return nil, err
	}
	out := make([]Entry, 0, len(rows))
	for _, r := range rows {
		out = append(out, Entry{
			DeviceID:   r.DeviceID,
			PlaylistID: r.PlaylistID,
			Enabled:    r.Enabled != 0,
			UpdatedAt:  r.UpdatedAt,
		})
	}
	return out, nil
}

// Get returns a single offline entry or ErrEntryNotFound.
func (s *Service) Get(ctx context.Context, deviceID, playlistID string) (*Entry, error) {
	row, err := s.q.GetOfflineSetEntry(ctx, db.GetOfflineSetEntryParams{
		DeviceID:   deviceID,
		PlaylistID: playlistID,
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrEntryNotFound
		}
		return nil, err
	}
	e := Entry{
		DeviceID:   row.DeviceID,
		PlaylistID: row.PlaylistID,
		Enabled:    row.Enabled != 0,
		UpdatedAt:  row.UpdatedAt,
	}
	return &e, nil
}

// Remove deletes the offline entry for the device+playlist pair. Idempotent.
func (s *Service) Remove(ctx context.Context, deviceID, playlistID string) error {
	return s.q.DeleteOfflineSetEntry(ctx, db.DeleteOfflineSetEntryParams{
		DeviceID:   deviceID,
		PlaylistID: playlistID,
	})
}
