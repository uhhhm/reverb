package api

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/uhhhm/reverb/internal/library"
	"github.com/uhhhm/reverb/internal/override"
)

// maxBatchItems bounds one batch request. A rename batch is a user selecting
// rows on a page, not an import format, and every album in one costs a lookup
// against the library backend.
const maxBatchItems = 500

// entityRename is one album or artist rename. An empty Name clears the rename.
type entityRename struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// trackNamePatch is a track rename as it arrives off the wire. A field that is
// absent keeps whatever the track already has, and a field that is present and
// blank clears it back to the library's own name — two different requests, so
// they cannot share the zero string. Without that distinction a client sending
// only the field it edited would silently clear the other two.
type trackNamePatch struct {
	Title  *string `json:"title"`
	Artist *string `json:"artist"`
	Album  *string `json:"album"`
}

// merge folds the patch onto the override a track already carries.
func (p trackNamePatch) merge(cur override.Name) override.Name {
	if p.Title != nil {
		cur.Title = *p.Title
	}
	if p.Artist != nil {
		cur.Artist = *p.Artist
	}
	if p.Album != nil {
		cur.Album = *p.Album
	}
	return cur
}

// trackRename is one track rename inside a batch.
type trackRename struct {
	ID string `json:"id"`
	trackNamePatch
}

// applyTrackRename stores one track rename and publishes it, returning the
// override as it now stands.
func (s *Server) applyTrackRename(ctx context.Context, id string, p trackNamePatch) (override.Name, error) {
	cur, err := s.deps.Overrides.Get(ctx, id)
	if err != nil {
		return override.Name{}, err
	}
	if err := s.deps.Overrides.Set(ctx, id, p.merge(cur)); err != nil {
		return override.Name{}, err
	}
	name, err := s.deps.Overrides.Get(ctx, id)
	if err != nil {
		return override.Name{}, err
	}
	s.emitTrackRename(ctx, id, name)
	return name, nil
}

// batchRenameRequest is a whole batch. The three lists are independent, so one
// request can rename tracks and the album they sit on together.
type batchRenameRequest struct {
	Tracks  []trackRename  `json:"tracks"`
	Albums  []entityRename `json:"albums"`
	Artists []entityRename `json:"artists"`
}

// batchRenameResponse reports what landed. Failures are per-item rather than
// fatal: one album id that no longer exists must not discard the other forty
// renames the user just confirmed.
type batchRenameResponse struct {
	Applied int               `json:"applied"`
	Errors  map[string]string `json:"errors,omitempty"`
}

func (b *batchRenameResponse) fail(id string, err error) {
	if b.Errors == nil {
		b.Errors = map[string]string{}
	}
	b.Errors[id] = err.Error()
}

// albumKeyFor derives the stable identity of an album from the library's own
// names rather than from what the client sent, which is already showing any
// earlier rename and would key the second rename differently from the first.
func (s *Server) albumKeyFor(ctx context.Context, lib library.LibraryAdapter, id string) string {
	if lib == nil {
		return ""
	}
	al, err := lib.GetAlbum(ctx, id)
	if err != nil {
		return ""
	}
	return override.AlbumKey(al.Artist, al.Name)
}

func (s *Server) artistKeyFor(ctx context.Context, lib library.LibraryAdapter, id string) string {
	if lib == nil {
		return ""
	}
	ar, err := lib.GetArtist(ctx, id)
	if err != nil {
		return ""
	}
	return override.ArtistKey(ar.Name)
}

// handleRenameAlbum records a user-supplied display name for a library album.
// The name cascades onto every track on the album; sending an empty name
// clears it.
func (s *Server) handleRenameAlbum(w http.ResponseWriter, r *http.Request) {
	s.renameEntity(w, r, override.KindAlbum)
}

// handleRenameArtist records a user-supplied display name for a library artist.
func (s *Server) handleRenameArtist(w http.ResponseWriter, r *http.Request) {
	s.renameEntity(w, r, override.KindArtist)
}

func (s *Server) renameEntity(w http.ResponseWriter, r *http.Request, kind string) {
	if s.deps.Entities == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "renaming unavailable"})
		return
	}
	id := chi.URLParam(r, "id")
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing id"})
		return
	}
	var body entityRename
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	if err := s.applyEntityRename(r.Context(), s.library(), kind, id, body.Name); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, entityRename{ID: id, Name: body.Name})
}

func (s *Server) applyEntityRename(ctx context.Context, lib library.LibraryAdapter, kind, id, name string) error {
	var key string
	if kind == override.KindAlbum {
		key = s.albumKeyFor(ctx, lib, id)
	} else {
		key = s.artistKeyFor(ctx, lib, id)
	}
	if err := s.deps.Entities.Set(ctx, kind, id, key, name); err != nil {
		return err
	}
	s.emitEntityRename(ctx, kind, key, name)
	return nil
}

// handleBatchRename applies many renames in one request. The client computes
// the new names — a find-and-replace preview it has already shown the user —
// and sends the results, so the server never interprets a pattern and what the
// user approved is exactly what is stored.
func (s *Server) handleBatchRename(w http.ResponseWriter, r *http.Request) {
	if s.deps.Overrides == nil || s.deps.Entities == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "renaming unavailable"})
		return
	}
	var body batchRenameRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	if len(body.Tracks)+len(body.Albums)+len(body.Artists) > maxBatchItems {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "too many items in one batch"})
		return
	}
	ctx := r.Context()
	lib := s.library()
	out := batchRenameResponse{}

	for _, t := range body.Tracks {
		if t.ID == "" {
			continue
		}
		if _, err := s.applyTrackRename(ctx, t.ID, t.trackNamePatch); err != nil {
			out.fail(t.ID, err)
			continue
		}
		out.Applied++
	}
	for _, a := range body.Albums {
		if a.ID == "" {
			continue
		}
		if err := s.applyEntityRename(ctx, lib, override.KindAlbum, a.ID, a.Name); err != nil {
			out.fail(a.ID, err)
			continue
		}
		out.Applied++
	}
	for _, a := range body.Artists {
		if a.ID == "" {
			continue
		}
		if err := s.applyEntityRename(ctx, lib, override.KindArtist, a.ID, a.Name); err != nil {
			out.fail(a.ID, err)
			continue
		}
		out.Applied++
	}
	writeJSON(w, http.StatusOK, out)
}
