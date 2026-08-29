package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/uhhhm/reverb/internal/core"
	"github.com/uhhhm/reverb/internal/library"
	"github.com/uhhhm/reverb/internal/trackref"
)

// isCanonicalID reports whether id carries a catalog-entity prefix. Only these
// ids may be passed to the resolver; raw backend ids must never reach it.
func isCanonicalID(id string) bool {
	return strings.HasPrefix(id, "trk_") ||
		strings.HasPrefix(id, "alb_") ||
		strings.HasPrefix(id, "art_")
}

// handleStream proxies an audio stream from the library adapter, forwarding the
// inbound Range header upstream and copying back the status, Content-Type,
// Content-Length, Accept-Ranges, and Content-Range. Subsonic credentials never
// reach the browser.
//
// For canonical ids (trk_/alb_/art_) the resolver is consulted first to obtain
// the current backend track id; raw backend ids pass through directly.
func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	lib, ok := s.libraryReady(w)
	if !ok {
		return
	}
	id := chi.URLParam(r, "id")
	if isCanonicalID(id) {
		if s.deps.Resolver == nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
			return
		}
		addr, err := s.deps.Resolver.Resolve(r.Context(), id)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
			return
		}
		if !addr.Found || addr.BackendID == "" {
			// No copy in the library. The track may still be playable from the
			// source it was played from before — history and anything else that
			// addresses tracks canonically would otherwise dead-end on a track
			// that plays perfectly well from search.
			if s.streamCanonicalExternally(w, r, id) {
				return
			}
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
			return
		}
		id = addr.BackendID
	}
	s.serveStream(w, r, lib, id)
}

// handleCover proxies cover art from the library adapter.
//
// For canonical ids (trk_/alb_/art_) the resolver is consulted first to obtain
// the current backend cover art id; raw backend ids pass through directly.
func (s *Server) handleCover(w http.ResponseWriter, r *http.Request) {
	lib, ok := s.libraryReady(w)
	if !ok {
		return
	}
	id := chi.URLParam(r, "id")
	size, _ := strconv.Atoi(r.URL.Query().Get("size"))
	if isCanonicalID(id) {
		if s.deps.Resolver == nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
			return
		}
		addr, err := s.deps.Resolver.Resolve(r.Context(), id)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
			return
		}
		if !addr.Found || addr.CoverArtID == "" {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
			return
		}
		id = addr.CoverArtID
	}
	s.serveCover(w, r, lib, id, size)
}

// serveStream is the shared adapter-calling body for handleStream and its
// canonical-id resolution path. It threads the Range header and copies back
// all relevant response headers.
func (s *Server) serveStream(w http.ResponseWriter, r *http.Request, lib library.LibraryAdapter, backendID string) {
	handle, err := lib.Stream(r.Context(), backendID, core.StreamOpts{}, r.Header.Get("Range"))
	if err != nil {
		if errors.Is(err, core.ErrLibraryItemNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
			return
		}
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	defer handle.Body.Close()

	h := w.Header()
	if handle.ContentType != "" {
		h.Set("Content-Type", handle.ContentType)
	}
	if handle.AcceptRanges != "" {
		h.Set("Accept-Ranges", handle.AcceptRanges)
	}
	if handle.ContentRange != "" {
		h.Set("Content-Range", handle.ContentRange)
	}
	if handle.ContentLength > 0 {
		h.Set("Content-Length", strconv.FormatInt(handle.ContentLength, 10))
	}
	status := handle.StatusCode
	if status == 0 {
		status = http.StatusOK
	}
	w.WriteHeader(status)
	_, _ = io.Copy(w, handle.Body)
}

// serveCover is the shared adapter-calling body for handleCover and its
// canonical-id resolution path. Cache-Control is set after the adapter call
// (consistent with the original handler's placement).
func (s *Server) serveCover(w http.ResponseWriter, r *http.Request, lib library.LibraryAdapter, backendID string, size int) {
	cover, err := lib.CoverArt(r.Context(), backendID, size)
	if err != nil {
		if errors.Is(err, core.ErrLibraryItemNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
			return
		}
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	defer cover.Body.Close()
	if cover.ContentType != "" {
		w.Header().Set("Content-Type", cover.ContentType)
	}
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, cover.Body)
}

// streamCanonicalExternally plays a canonical track from a search source when
// the library has no copy of it. Reports whether it handled the response; false
// means there is nothing to fall back to and the caller should answer 404.
//
// The "external" alias recorded when the track was played from a source is the
// precise answer, and it carries the source id that keeps the resolve cached
// under the same key the search page uses. Failing that, the entity's own
// artist and title are enough to find the track again — which is what makes a
// history entry recorded before that alias existed still playable.
func (s *Server) streamCanonicalExternally(w http.ResponseWriter, r *http.Request, catalogID string) bool {
	if s.deps.ExternalStream == nil || s.deps.Catalog == nil {
		return false
	}
	source, externalID := s.externalAliasFor(r.Context(), catalogID)

	e, err := s.deps.Catalog.GetCatalogEntity(r.Context(), catalogID)
	if err != nil {
		return false
	}
	if source == "" || externalID == "" {
		if e.Title == "" {
			return false
		}
		// The source pair is only a cache key once artist and title are known,
		// so the catalog id itself is a perfectly good one — and a stable one,
		// which keeps the resolve reusable across plays.
		source, externalID = "catalog", catalogID
	}
	s.serveExternalStream(w, r, source, externalID, e.Artist, e.Title)
	return true
}

// externalAliasFor returns the source addressing recorded for a catalog entity,
// or empty strings when it has none.
func (s *Server) externalAliasFor(ctx context.Context, catalogID string) (source, externalID string) {
	aliases, err := s.deps.Catalog.ListAliasesForCatalog(ctx, catalogID)
	if err != nil {
		return "", ""
	}
	for _, a := range aliases {
		if a.AliasKind != "external" {
			continue
		}
		if src, ext, ok := trackref.DecodeExternalID(a.AliasValue); ok {
			return src, ext
		}
	}
	return "", ""
}

// localPathFor resolves a track id to a file on disk, mapping a canonical id
// through the resolver first. The library only knows its own backend ids, so
// handing it a canonical one produces nothing but a failed lookup.
//
// Returns ("", false) for an external track (no file exists), when the library
// has no local files (a remote Subsonic server), or when nothing resolves.
func (s *Server) localPathFor(ctx context.Context, id string) (string, bool) {
	if id == "" || isExternalTrackID(id) {
		return "", false
	}
	paths, ok := s.library().(localTrackPath)
	if !ok {
		return "", false
	}
	if isCanonicalID(id) {
		if s.deps.Resolver == nil {
			return "", false
		}
		addr, err := s.deps.Resolver.Resolve(ctx, id)
		if err != nil || !addr.Found || addr.BackendID == "" {
			return "", false
		}
		id = addr.BackendID
	}
	return paths.LocalTrackPath(id)
}
