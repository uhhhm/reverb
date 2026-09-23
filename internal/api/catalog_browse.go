package api

import (
	"context"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/uhhhm/reverb/internal/core"
	"github.com/uhhhm/reverb/internal/store/db"
)

// handleCatalogTracks browses the replicated household catalogue. The local
// library endpoints intentionally remain backend-scoped; this route is what
// lets a phone browse metadata for tracks it has not chosen to store offline.
func (s *Server) handleCatalogTracks(w http.ResponseWriter, r *http.Request) {
	if s.deps.CatalogBrowse == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "catalogue unavailable"})
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if offset < 0 {
		offset = 0
	}
	entities, err := s.deps.CatalogBrowse.ListBrowsableCatalogTracks(r.Context(), db.ListBrowsableCatalogTracksParams{
		Query: r.URL.Query().Get("q"), Limit: int64(limit), Offset: int64(offset),
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	rows := make([]core.CatalogLibraryTrack, len(entities))
	var wg sync.WaitGroup
	slots := make(chan struct{}, 8)
	for i, entity := range entities {
		wg.Add(1)
		slots <- struct{}{}
		go func(i int, entity db.CatalogEntity) {
			defer wg.Done()
			defer func() { <-slots }()
			rows[i] = s.catalogTrack(r.Context(), entity)
		}(i, entity)
	}
	wg.Wait()
	writeJSON(w, http.StatusOK, rows)
}

func (s *Server) catalogTrack(ctx context.Context, entity db.CatalogEntity) core.CatalogLibraryTrack {
	row := core.CatalogLibraryTrack{
		ID: entity.ID, Title: entity.Title, Artist: entity.Artist, Album: entity.Album,
		DurationMs: int(entity.DurationMs), Playback: core.PlaybackUnavailable,
	}
	if s.deps.Overrides != nil {
		if name, err := s.deps.Overrides.GetByCatalogID(ctx, entity.ID); err == nil {
			if name.Title != "" {
				row.Title = name.Title
			}
			if name.Artist != "" {
				row.Artist = name.Artist
			}
			if name.Album != "" {
				row.Album = name.Album
			}
		}
	}
	if s.deps.Crop != nil {
		if points, err := s.deps.Crop.GetByCatalogID(ctx, entity.ID); err == nil {
			row.CropStartMs, row.CropEndMs = points.StartMs, points.EndMs
		}
	}
	if s.deps.Resolver != nil {
		if addr, err := s.deps.Resolver.Resolve(ctx, entity.ID); err == nil && addr.Found && addr.BackendID != "" {
			row.Playback = core.PlaybackLocal
			row.LocalTrackID, row.CoverArtID = addr.BackendID, addr.CoverArtID
			return row
		}
	}
	if s.deps.DelegatedStream != nil {
		probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		if s.deps.DelegatedStream.Playable(probeCtx, entity.ID) {
			row.Playback = core.PlaybackDelegated
			row.CoverArtID = entity.ID
		}
	}
	return row
}
