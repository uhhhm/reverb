package playlistcrdt

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/uhhhm/reverb/internal/core"
	"github.com/uhhhm/reverb/internal/playlistsync"
	reverbsync "github.com/uhhhm/reverb/internal/sync"
)

// CatalogResolver maps a catalog id minted on another device onto the id this
// one stores the same entity under. *catalog.Service satisfies it.
type CatalogResolver interface {
	Resolve(ctx context.Context, catalogID string) string
}

// WithCatalogResolver attaches catalog-id translation for incoming tracklists.
// Without it a peer's catalog ids are carried through unchanged, which still
// works — they simply do not resolve to a local binding until they are minted.
func (s *Service) WithCatalogResolver(r CatalogResolver) *Service {
	s.catalog = r
	return s
}

// Apply rebuilds one playlist from the change log.
//
// It reads the whole entity rather than acting on the single change that
// arrived: a playlist row holds its name and its entire tracklist in one
// record, so there is no way to write "this one track moved" into it without
// knowing everything else the log currently says.
func (s *Service) Apply(ctx context.Context, id string) error {
	if s == nil || s.log == nil || s.store == nil || id == "" {
		return nil
	}
	changes, err := s.log.ListLatestForEntity(ctx, reverbsync.EntityPlaylist, id)
	if err != nil {
		return err
	}
	if len(changes) == 0 {
		return nil
	}
	st := readState(changes)
	if st.deleted {
		if err := s.store.Delete(ctx, id); err != nil && !errors.Is(err, playlistsync.ErrNotFound) {
			return err
		}
		return nil
	}

	mode := str(st.fields[FieldMode])
	if mode == "" {
		mode = "once"
	}
	source := str(st.fields[FieldSource])
	if source == "" {
		source = "local"
	}
	externalID := str(st.fields[FieldExternalID])
	if externalID == "" {
		externalID = id
	}

	existing, err := s.store.Get(ctx, id)
	switch {
	case err == nil:
	case errors.Is(err, playlistsync.ErrNotFound):
		existing = playlistsync.SyncedRow{}
	default:
		return err
	}

	// A mirrored playlist rebuilds its own tracklist from upstream, so the log
	// carries no membership for it and whatever is here already stands.
	tracksJSON := existing.TracksJSON
	if tracksReplicate(mode) {
		tracks := make([]core.ExternalResult, 0, len(st.members))
		for _, k := range st.orderedMembers() {
			e := st.members[k].Entry
			if e == nil {
				continue
			}
			entry := *e
			if s.catalog != nil && entry.CanonicalID != "" {
				entry.CanonicalID = s.catalog.Resolve(ctx, entry.CanonicalID)
			}
			tracks = append(tracks, entry)
		}
		encoded, mErr := json.Marshal(tracks)
		if mErr != nil {
			return mErr
		}
		tracksJSON = string(encoded)
	}
	if tracksJSON == "" {
		tracksJSON = "[]"
	}

	createdAt := int64(num(st.fields[FieldCreatedAt]))
	if createdAt == 0 {
		createdAt = existing.CreatedAt
	}
	name := str(st.fields[FieldName])
	cover := str(st.fields[FieldCoverURL])

	storedID, err := s.store.Upsert(ctx, core.SyncedPlaylist{
		ID:         id,
		Source:     source,
		ExternalID: externalID,
		Name:       name,
		CoverURL:   cover,
		Mode:       mode,
	}, tracksJSON, createdAt)
	if err != nil {
		return err
	}
	// Upsert only writes name/cover/tracks on conflict, so the fields it leaves
	// alone are written explicitly.
	if err := s.store.UpdateTracks(ctx, storedID, name, cover, tracksJSON, existing.LastSyncedAt); err != nil {
		return err
	}
	return s.store.UpdateSettings(ctx, storedID,
		boolean(st.fields[FieldSyncEnabled]),
		num(st.fields[FieldSyncIntervalSec]),
		boolean(st.fields[FieldAutoDownload]),
	)
}
