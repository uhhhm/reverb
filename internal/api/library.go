package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/uhhhm/reverb/internal/core"
	"github.com/uhhhm/reverb/internal/library"
	"github.com/uhhhm/reverb/internal/override"
)

// optional browse interfaces (implemented by the subsonic adapter).
type artistBrowser interface {
	GetArtistsBrowse(ctx context.Context) ([]core.Artist, error)
}
type albumBrowser interface {
	GetAlbumsBrowse(ctx context.Context, listType string, size int) ([]core.Album, error)
}
type songBrowser interface {
	GetSongsBrowse(ctx context.Context, size, offset int) ([]core.Track, error)
}

// libraryReady returns the active library adapter, or writes a 503 and returns
// (nil, false) when none is configured. Callers use the returned adapter for the
// whole request so a concurrent reload can't swap it out mid-handler.
func (s *Server) libraryReady(w http.ResponseWriter) (library.LibraryAdapter, bool) {
	lib := s.library()
	if lib == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "no library configured"})
		return nil, false
	}
	return lib, true
}

func (s *Server) handleLibrarySearch(w http.ResponseWriter, r *http.Request) {
	lib, ok := s.libraryReady(w)
	if !ok {
		return
	}
	q := r.URL.Query().Get("q")
	var types []core.EntityType
	switch r.URL.Query().Get("type") {
	case "track":
		types = []core.EntityType{core.EntityTrack}
	case "album":
		types = []core.EntityType{core.EntityAlbum}
	case "artist":
		types = []core.EntityType{core.EntityArtist}
	default:
		types = []core.EntityType{core.EntityTrack, core.EntityAlbum, core.EntityArtist}
	}
	res, err := lib.Search(r.Context(), q, types)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	s.deps.Overrides.ApplyTracks(r.Context(), res.Tracks)
	s.deps.Crop.ApplyTracks(r.Context(), res.Tracks)
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleLibraryArtist(w http.ResponseWriter, r *http.Request) {
	lib, ok := s.libraryReady(w)
	if !ok {
		return
	}
	ar, err := lib.GetArtist(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, ar)
}

func (s *Server) handleLibraryAlbum(w http.ResponseWriter, r *http.Request) {
	lib, ok := s.libraryReady(w)
	if !ok {
		return
	}
	al, err := lib.GetAlbum(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	s.deps.Overrides.ApplyTracks(r.Context(), al.Tracks)
	s.deps.Crop.ApplyTracks(r.Context(), al.Tracks)
	writeJSON(w, http.StatusOK, al)
}

func (s *Server) handleLibraryArtists(w http.ResponseWriter, r *http.Request) {
	lib, ready := s.libraryReady(w)
	if !ready {
		return
	}
	br, ok := lib.(artistBrowser)
	if !ok {
		writeJSON(w, http.StatusOK, []core.Artist{})
		return
	}
	arts, err := br.GetArtistsBrowse(r.Context())
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, arts)
}

func (s *Server) handleLibraryAlbums(w http.ResponseWriter, r *http.Request) {
	lib, ready := s.libraryReady(w)
	if !ready {
		return
	}
	br, ok := lib.(albumBrowser)
	if !ok {
		writeJSON(w, http.StatusOK, []core.Album{})
		return
	}
	listType := r.URL.Query().Get("type")
	size, _ := strconv.Atoi(r.URL.Query().Get("size"))
	albs, err := br.GetAlbumsBrowse(r.Context(), listType, size)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, albs)
}

// handleLibrarySongs lists every song in the library. Adapters that cannot
// enumerate songs return an empty list rather than an error, matching the
// artists/albums browse handlers.
func (s *Server) handleLibrarySongs(w http.ResponseWriter, r *http.Request) {
	lib, ready := s.libraryReady(w)
	if !ready {
		return
	}
	br, ok := lib.(songBrowser)
	if !ok {
		writeJSON(w, http.StatusOK, []core.Track{})
		return
	}
	size, _ := strconv.Atoi(r.URL.Query().Get("size"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if offset < 0 {
		offset = 0
	}
	songs, err := br.GetSongsBrowse(r.Context(), size, offset)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	s.deps.Overrides.ApplyTracks(r.Context(), songs)
	s.deps.Crop.ApplyTracks(r.Context(), songs)
	writeJSON(w, http.StatusOK, songs)
}

// handleRenameTrack records a user-supplied display name for a library track.
// Reverb never rewrites file tags — the override is applied when tracks are
// read back out. Sending both fields empty clears the rename.
func (s *Server) handleRenameTrack(w http.ResponseWriter, r *http.Request) {
	if s.deps.Overrides == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "renaming unavailable"})
		return
	}
	id := chi.URLParam(r, "id")
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing track id"})
		return
	}
	var body override.Name
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	if err := s.deps.Overrides.Set(r.Context(), id, body); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	name, err := s.deps.Overrides.Get(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, name)
}
