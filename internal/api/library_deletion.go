package api

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	reverbsync "github.com/uhhhm/reverb/internal/sync"
)

var errTrackRemovalUnavailable = errors.New("track removal needs the built-in library")

// localTrackPathProvider is intentionally consumer-owned and optional. The
// bundled Subsonic adapter implements it because its music directory is on this
// machine; an external library must never expose a remote path for deletion.
type localTrackPathProvider interface {
	LocalTrackPath(id string) (string, bool)
}

// handleRemoveLibraryTrack deletes an owned audio file, then asks the bundled
// library to rescan. The sync tombstone uses the stable catalog id when one is
// available, while the HTTP contract remains keyed by the backend id shown in
// track payloads.
func (s *Server) handleRemoveLibraryTrack(w http.ResponseWriter, r *http.Request) {
	trackID := chi.URLParam(r, "id")
	if trackID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing track id"})
		return
	}
	if s.deps.MusicDir == "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": errTrackRemovalUnavailable.Error()})
		return
	}
	if s.deps.LibraryStatus != nil {
		if mode, _ := s.deps.LibraryStatus(); mode != "built-in" {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": errTrackRemovalUnavailable.Error()})
			return
		}
	}

	lib, ok := s.libraryReady(w)
	if !ok {
		return
	}
	paths, ok := lib.(localTrackPathProvider)
	if !ok {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": errTrackRemovalUnavailable.Error()})
		return
	}
	path, ok := paths.LocalTrackPath(trackID)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "track file not found"})
		return
	}

	// Treat the adapter result as untrusted. A future adapter implementation
	// must not be able to make this endpoint remove a file outside MusicDir.
	root, err := filepath.Abs(s.deps.MusicDir)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not resolve the music directory"})
		return
	}
	target, err := filepath.Abs(path)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not resolve the track file"})
		return
	}
	rel, err := filepath.Rel(root, target)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "track file is outside the managed music directory"})
		return
	}

	catalogID := ""
	if s.deps.Overrides != nil {
		catalogID = s.deps.Overrides.CatalogIDForTrack(r.Context(), trackID)
	}
	if err := os.Remove(target); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "track file not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not remove the track file"})
		return
	}

	scanning := false
	if scanner, ok := s.downloads().(interface{ ScheduleScan() }); ok && scanner != nil {
		scanner.ScheduleScan()
		scanning = true
	}
	s.emitTrackDeletion(r.Context(), catalogID)
	writeJSON(w, http.StatusOK, map[string]any{"removed": true, "scanning": scanning})
}

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
