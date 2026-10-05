package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"
	reverbsync "github.com/uhhhm/reverb/internal/sync"
	"github.com/uhhhm/reverb/internal/syncemit"
)

var errTrackRemovalUnavailable = errors.New("track removal needs the built-in library")

// localTrackPathProvider is intentionally consumer-owned and optional. The
// bundled Subsonic adapter implements it because its music directory is on this
// machine; an external library must never expose a remote path for deletion.
type localTrackPathProvider interface {
	LocalTrackPath(id string) (string, bool)
}

// handleRemoveLibraryTrack deletes an owned audio file, then asks the bundled
// library to rescan. The file's content tombstone removes its copies on every
// device. The track itself is only withdrawn from household browsing, under its
// stable catalog id: its identity, plays and overrides stay, and downloading it
// again returns it. The HTTP contract remains keyed by the backend id shown in
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
	managed, err := os.OpenRoot(root)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not open music directory"})
		return
	}
	defer func() { _ = managed.Close() }()
	file, err := managed.Open(rel)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "track file not found"})
		return
	}
	info, statErr := file.Stat()
	if statErr != nil || !info.Mode().IsRegular() {
		file.Close()
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "track is not a regular file"})
		return
	}
	hash := sha256.New()
	_, hashErr := io.Copy(hash, file)
	_ = file.Close()
	if hashErr != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not hash track"})
		return
	}
	// The deletion reaches the log before the file goes, or not at all.
	switch err := s.deps.SyncEmit.EmitLibraryRemoval(r.Context(), hex.EncodeToString(hash.Sum(nil)), catalogID); {
	case err == nil, errors.Is(err, syncemit.ErrUnavailable):
	case errors.Is(err, syncemit.ErrNoIdentity):
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "sync identity unavailable"})
		return
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not persist track deletion"})
		return
	}
	if err := managed.Remove(rel); err != nil && !errors.Is(err, os.ErrNotExist) {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not remove the track file"})
		return
	}

	scanning := false
	if scanner, ok := s.downloads().(interface{ ScheduleScan() }); ok && scanner != nil {
		scanner.ScheduleScan()
		scanning = true
	}
	writeJSON(w, http.StatusOK, map[string]any{"removed": true, "scanning": scanning})
}

// emitPlaylistDeletion emits a playlist tombstone through DeletionService.
// Best-effort: logs on error and never fails the caller.
func (s *Server) emitPlaylistDeletion(ctx context.Context, playlistID string) {
	if playlistID == "" || s.deps.Deletion == nil {
		return
	}
	if _, err := s.deps.Deletion.DeletePlaylist(ctx, "", playlistID, 0); err != nil {
		log.Printf("sync tombstone playlist %q: %v", playlistID, err)
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
