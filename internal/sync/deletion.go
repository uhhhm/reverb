package sync

import (
	"context"
	"time"
)

// DeletionService emits tombstones via SyncStore.
type DeletionService struct {
	store *SyncStore
	q     Querier
}

// NewDeletionService creates a service using store and q. If q is nil and store is non-nil, store.q is used.
func NewDeletionService(store *SyncStore, q Querier) *DeletionService {
	if q == nil && store != nil {
		q = store.q
	}
	return &DeletionService{store: store, q: q}
}

// DeletePlaylist appends a __deleted tombstone for playlistID.
func (s *DeletionService) DeletePlaylist(ctx context.Context, deviceID, playlistID string, updatedAt int64) (int64, error) {
	if updatedAt == 0 {
		updatedAt = time.Now().UnixMilli()
	}
	if deviceID == "" {
		if id, err := resolveServerDevice(ctx, s.q); err == nil {
			deviceID = id
		}
	}
	if deviceID == "" {
		return 0, ErrNoServerDevice
	}
	return s.store.AppendChange(ctx, deviceID, SyncChange{
		EntityType: "playlist",
		EntityID:   playlistID,
		Field:      "__deleted",
		Value:      nil,
		UpdatedAt:  updatedAt,
	})
}

// DeleteTrack appends a __deleted tombstone for catalogID (e.g. trk_…).
func (s *DeletionService) DeleteTrack(ctx context.Context, deviceID, catalogID string, updatedAt int64) (int64, error) {
	if updatedAt == 0 {
		updatedAt = time.Now().UnixMilli()
	}
	if deviceID == "" {
		if id, err := resolveServerDevice(ctx, s.q); err == nil {
			deviceID = id
		}
	}
	if deviceID == "" {
		return 0, ErrNoServerDevice
	}
	return s.store.AppendChange(ctx, deviceID, SyncChange{
		EntityType: "track",
		EntityID:   catalogID,
		Field:      "__deleted",
		Value:      nil,
		UpdatedAt:  updatedAt,
	})
}

// IsDeleted reports whether entityType+entityID has a __deleted tombstone.
func (s *DeletionService) IsDeleted(ctx context.Context, entityType, entityID string) (bool, error) {
	ch, err := s.store.GetLatestForField(ctx, entityType, entityID, "__deleted")
	if err != nil {
		return false, err
	}
	return ch != nil, nil
}

// resolveServerDevice mirrors offline_set serverDeviceID lookup: settings key then ListDevices fallback.
// resolveServerDevice picks the identity a tombstone is authored under. It is
// the author identity (local device), not the server device: see AuthorDeviceID.
func resolveServerDevice(ctx context.Context, q Querier) (string, error) {
	return AuthorDeviceID(ctx, q)
}

// ErrNoServerDevice is returned when no server device can be resolved.
var ErrNoServerDevice = errNoServerDevice("no server device")

type errNoServerDevice string

func (e errNoServerDevice) Error() string { return string(e) }
