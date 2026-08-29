package api

import (
	"context"
	"log"
	"time"

	reverbsync "github.com/uhhhm/reverb/internal/sync"
)

// emitPlaylistDeletion emits a playlist __deleted tombstone via DeletionService
// (or SyncStore fallback). Best-effort: logs on error and never fails the caller.
func (s *Server) emitPlaylistDeletion(ctx context.Context, playlistID string) {
	if playlistID == "" {
		return
	}
	if s.deps.Deletion != nil {
		if _, err := s.deps.Deletion.DeletePlaylist(ctx, "", playlistID, 0); err != nil {
			log.Printf("sync tombstone playlist %q: %v", playlistID, err)
		}
		return
	}
	if s.deps.SyncStore == nil {
		return
	}
	deviceID := s.resolveAuthorDeviceForSync(ctx)
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

// emitTrackDeletion emits a track __deleted tombstone via DeletionService.
func (s *Server) emitTrackDeletion(ctx context.Context, catalogID string) {
	if catalogID == "" {
		return
	}
	if s.deps.Deletion != nil {
		if _, err := s.deps.Deletion.DeleteTrack(ctx, "", catalogID, 0); err != nil {
			log.Printf("sync tombstone track %q: %v", catalogID, err)
		}
		return
	}
	if s.deps.SyncStore == nil {
		return
	}
	deviceID := s.resolveAuthorDeviceForSync(ctx)
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

// resolveAuthorDeviceForSync returns the identity tombstones are authored under
// -- the local device, the only one that can be signed (see AuthorDeviceID).
func (s *Server) resolveAuthorDeviceForSync(ctx context.Context) string {
	if s.deps.OfflineSet != nil {
		if id, err := reverbsync.AuthorDeviceID(ctx, s.deps.OfflineSet); err == nil && id != "" {
			return id
		}
	}
	if s.deps.PairingStore != nil {
		if id, err := reverbsync.AuthorDeviceID(ctx, s.deps.PairingStore); err == nil && id != "" {
			return id
		}
	}
	return ""
}
