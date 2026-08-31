package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/uhhhm/reverb/internal/cover"
	"github.com/uhhhm/reverb/internal/library"
	"github.com/uhhhm/reverb/internal/override"
)

// coverTarget names one thing an uploaded image is applied to, as "album:<id>"
// or "track:<id>". One flat form makes the batch case — one image, many
// entities of possibly mixed kinds — a single list rather than two.
type coverTarget struct {
	Kind string
	ID   string
}

func parseCoverTarget(s string) (coverTarget, bool) {
	kind, id, ok := strings.Cut(strings.TrimSpace(s), ":")
	if !ok || id == "" {
		return coverTarget{}, false
	}
	switch kind {
	case cover.KindAlbum, cover.KindTrack:
		return coverTarget{Kind: kind, ID: id}, true
	}
	return coverTarget{}, false
}

// coverKeyFor is the stable identity a cover replicates on: an album's
// normalised artist and title, a track's catalog id. Empty when it cannot be
// derived, which leaves the cover working locally but not replicating.
func (s *Server) coverKeyFor(ctx context.Context, lib library.LibraryAdapter, t coverTarget) string {
	if t.Kind == cover.KindTrack {
		if s.deps.Overrides == nil {
			return ""
		}
		return s.deps.Overrides.CatalogIDForTrack(ctx, t.ID)
	}
	if lib == nil {
		return ""
	}
	al, err := lib.GetAlbum(ctx, t.ID)
	if err != nil {
		return ""
	}
	return override.AlbumKey(al.Artist, al.Name)
}

type coverBatchResponse struct {
	CoverArtID string            `json:"coverArtId,omitempty"`
	Applied    int               `json:"applied"`
	Errors     map[string]string `json:"errors,omitempty"`
}

// handleUploadCovers accepts one image and the entities to put it on. The
// request is multipart: an "image" file part and one or more "target" values.
// One image applied to many entities is stored once — blobs are addressed by
// content hash — so a batch costs no more disk than a single upload.
func (s *Server) handleUploadCovers(w http.ResponseWriter, r *http.Request) {
	if s.deps.Covers == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "cover uploads unavailable"})
		return
	}
	// Bound the whole body before parsing, not just the file part.
	r.Body = http.MaxBytesReader(w, r.Body, cover.MaxBytes+1<<20)
	if err := r.ParseMultipartForm(cover.MaxBytes + 1<<20); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid multipart form"})
		return
	}
	targets, ok := coverTargetsFrom(r.MultipartForm.Value["target"])
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "at least one valid target is required"})
		return
	}
	file, _, err := r.FormFile("image")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "image field is required"})
		return
	}
	defer file.Close()

	// Read one byte past the limit so an oversized upload is detected without
	// holding all of it.
	data, err := io.ReadAll(io.LimitReader(file, cover.MaxBytes+1))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "could not read image"})
		return
	}
	sha, ext, err := s.deps.Covers.Store(data)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, cover.ErrUnsupportedType) || errors.Is(err, cover.ErrTooLarge) {
			status = http.StatusBadRequest
		}
		writeJSON(w, status, map[string]string{"error": err.Error()})
		return
	}

	ctx := r.Context()
	lib := s.library()
	out := coverBatchResponse{CoverArtID: cover.Prefix + sha + "." + ext}
	for _, t := range targets {
		key := s.coverKeyFor(ctx, lib, t)
		if err := s.deps.Covers.Assign(ctx, t.Kind, t.ID, key, sha, ext); err != nil {
			addCoverError(&out, t, err)
			continue
		}
		s.emitEntityCover(ctx, t.Kind, key, sha, ext)
		out.Applied++
	}
	writeJSON(w, http.StatusOK, out)
}

// handleDeleteCovers removes uploaded art from the given entities, so the
// library backend's own art shows again.
func (s *Server) handleDeleteCovers(w http.ResponseWriter, r *http.Request) {
	if s.deps.Covers == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "cover uploads unavailable"})
		return
	}
	var body struct {
		Targets []string `json:"targets"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	targets, ok := coverTargetsFrom(body.Targets)
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "at least one valid target is required"})
		return
	}
	ctx := r.Context()
	lib := s.library()
	out := coverBatchResponse{}
	for _, t := range targets {
		key := s.coverKeyFor(ctx, lib, t)
		if err := s.deps.Covers.Clear(ctx, t.Kind, t.ID); err != nil {
			addCoverError(&out, t, err)
			continue
		}
		s.emitEntityCover(ctx, t.Kind, key, "", "")
		out.Applied++
	}
	writeJSON(w, http.StatusOK, out)
}

func coverTargetsFrom(raw []string) ([]coverTarget, bool) {
	if len(raw) == 0 || len(raw) > maxBatchItems {
		return nil, false
	}
	out := make([]coverTarget, 0, len(raw))
	for _, v := range raw {
		if t, ok := parseCoverTarget(v); ok {
			out = append(out, t)
		}
	}
	return out, len(out) > 0
}

func addCoverError(out *coverBatchResponse, t coverTarget, err error) {
	if out.Errors == nil {
		out.Errors = map[string]string{}
	}
	out.Errors[t.Kind+":"+t.ID] = err.Error()
}
