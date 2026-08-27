package api

import (
	"context"
	"log"
	"time"

	reverbsync "github.com/uhhhm/reverb/internal/sync"
)

// emitPlaylistDeletion emits a playlist __deleted tombstone via SyncStore.
// Best-effort: logs on error and never fails the caller. Used by T5 for canonical
// library deletion propagation; offline-set removal must NOT call this.
func (s *Server) emitPlaylistDeletion(ctx context.Context, playlistID string) {
	if s.deps.SyncStore == nil || playlistID == "" {
		return
	}
	deviceID := s.resolveServerDeviceForSync(ctx)
	if deviceID == "" {
		return
	}
	if _, err := s.deps.SyncStore.AppendChange(ctx, deviceID, reverbsync.SyncChange{
		EntityType: "playlist",
		EntityID:   playlistID,
		Field:      "__deleted",
		UpdatedAt:  time.Now().UnixMilli(),
	}); err != nil {
		log.Printf("sync tombstone playlist %q: %v", playlistID, err)
	}
}

// emitTrackDeletion emits a track __deleted tombstone via SyncStore.
// Best-effort: logs on error and never fails the caller.
func (s *Server) emitTrackDeletion(ctx context.Context, catalogID string) {
	if s.deps.SyncStore == nil || catalogID == "" {
		return
	}
	deviceID := s.resolveServerDeviceForSync(ctx)
	if deviceID == "" {
		return
	}
	if _, err := s.deps.SyncStore.AppendChange(ctx, deviceID, reverbsync.SyncChange{
		EntityType: "track",
		EntityID:   catalogID,
		Field:      "__deleted",
		UpdatedAt:  time.Now().UnixMilli(),
	}); err != nil {
		log.Printf("sync tombstone track %q: %v", catalogID, err)
	}
}

// resolveServerDeviceForSync returns the server device ID for sync tombstone emission.
// It tries OfflineSet.serverDeviceID first, then PairingStore, then OfflineSet ListDevices fallback.
func (s *Server) resolveServerDeviceForSync(ctx context.Context) string {
	if sid, err := s.serverDeviceID(ctx); err == nil && sid != "" {
		return sid
	}
	if s.deps.PairingStore != nil {
		if devices, err := s.deps.PairingStore.ListDevices(ctx); err == nil {
			for _, d := range devices {
				if d.IsServer == 1 {
					return d.ID
				}
			}
		}
	}
	if s.deps.OfflineSet != nil {
		if devices, err := s.deps.OfflineSet.ListDevices(ctx); err == nil {
			for _, d := range devices {
				if d.IsServer == 1 {
					return d.ID
				}
			}
		}
	}
	return ""
}
