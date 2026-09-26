package offlineset

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/uhhhm/reverb/internal/store/db"
)

// Entry is one playlist in a Device's Offline set.
type Entry struct {
	DeviceID     string
	PlaylistID   string
	PlaylistName string // "" when the playlist cannot be read
	Enabled      bool
	UpdatedAt    int64
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

// Service is this Device's Offline set: the playlists it keeps playable
// offline. The table is local-only and never emits sync_change rows (ADR 0003).
//
// Every change that is stored is announced through changed, after the write,
// so the Keeper fetches and prunes against the new set; a change that fails
// is not announced.
type Service struct {
	q        Querier
	deviceID func(context.Context) (string, error)
	changed  func()
}

// NewService creates the Offline set for the Device deviceID resolves. changed
// wakes the Keeper; it is nil on a desktop, which keeps every file.
func NewService(q Querier, deviceID func(context.Context) (string, error), changed func()) *Service {
	return &Service{q: q, deviceID: deviceID, changed: changed}
}

// ErrPlaylistNotFound is returned when a change names a playlist that does not exist.
var ErrPlaylistNotFound = errors.New("playlist not found")

// List returns the Device's Offline set, ordered by playlist id.
func (s *Service) List(ctx context.Context) ([]Entry, error) {
	deviceID, err := s.deviceID(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := s.q.ListOfflineSetForDevice(ctx, deviceID)
	if err != nil {
		return nil, err
	}
	out := make([]Entry, 0, len(rows))
	for _, r := range rows {
		out = append(out, s.entry(ctx, r))
	}
	return out, nil
}

// Set keeps playlistID offline (or stops, when enabled is false) and returns
// the resulting entry.
func (s *Service) Set(ctx context.Context, playlistID string, enabled bool) (Entry, error) {
	deviceID, err := s.target(ctx, playlistID)
	if err != nil {
		return Entry{}, err
	}
	var en int64
	if enabled {
		en = 1
	}
	if err := s.q.UpsertOfflineSet(ctx, db.UpsertOfflineSetParams{
		DeviceID:   deviceID,
		PlaylistID: playlistID,
		Enabled:    en,
		UpdatedAt:  time.Now().UnixMilli(),
	}); err != nil {
		return Entry{}, err
	}
	s.notify()
	row, err := s.q.GetOfflineSetEntry(ctx, db.GetOfflineSetEntryParams{DeviceID: deviceID, PlaylistID: playlistID})
	if err != nil {
		return Entry{}, err
	}
	return s.entry(ctx, row), nil
}

// Remove takes playlistID out of the Offline set. Removing a playlist that is
// not in the set succeeds.
func (s *Service) Remove(ctx context.Context, playlistID string) error {
	deviceID, err := s.target(ctx, playlistID)
	if err != nil {
		return err
	}
	if err := s.q.DeleteOfflineSetEntry(ctx, db.DeleteOfflineSetEntryParams{
		DeviceID:   deviceID,
		PlaylistID: playlistID,
	}); err != nil {
		return err
	}
	s.notify()
	return nil
}

// target resolves the Device a change applies to and checks the playlist exists.
func (s *Service) target(ctx context.Context, playlistID string) (string, error) {
	deviceID, err := s.deviceID(ctx)
	if err != nil {
		return "", err
	}
	if _, err := s.q.GetSyncedPlaylist(ctx, playlistID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrPlaylistNotFound
		}
		return "", err
	}
	return deviceID, nil
}

func (s *Service) notify() {
	if s.changed != nil {
		s.changed()
	}
}

func (s *Service) entry(ctx context.Context, r db.OfflineSet) Entry {
	e := Entry{
		DeviceID:   r.DeviceID,
		PlaylistID: r.PlaylistID,
		Enabled:    r.Enabled != 0,
		UpdatedAt:  r.UpdatedAt,
	}
	if pl, err := s.q.GetSyncedPlaylist(ctx, r.PlaylistID); err == nil {
		e.PlaylistName = pl.Name
	}
	return e
}
