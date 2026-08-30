package playlistcrdt

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/uhhhm/reverb/internal/core"
	"github.com/uhhhm/reverb/internal/playlistsync"
	reverbsync "github.com/uhhhm/reverb/internal/sync"
)

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

	existing, err := s.store.Get(ctx, id)
	exists := err == nil
	switch {
	case exists:
	case errors.Is(err, playlistsync.ErrNotFound):
		existing = playlistsync.SyncedRow{}
	default:
		return err
	}

	// A playlist's identity is fixed when it is created and no edit changes it,
	// so it is only read from the log for a row that does not exist yet. Writing
	// it on an existing row would also collide: the insert resolves conflicts on
	// (source, external_id), so changing either on a row already keyed by id
	// fails on the primary key.
	mode := existing.Mode
	if !exists {
		// Changes for one playlist are appended together, but a peer sends its
		// log in pages and a page can split them. Creating the row from half an
		// entity would fix the wrong identity onto it, so wait: the fields
		// already stored are re-read when the rest arrives.
		if st.fields[FieldSource] == nil {
			return nil
		}
		mode = str(st.fields[FieldMode])
		if mode == "" {
			mode = "once"
		}
	}

	// A mirrored playlist rebuilds its own tracklist from upstream, so the log
	// carries no membership for it and whatever is here already stands.
	tracksJSON := existing.TracksJSON
	if tracksReplicate(mode) {
		// Catalog ids do not travel, so the ones this device minted are carried
		// over rather than blanked every time a peer's edit is applied.
		local := map[string]string{}
		for _, t := range decodeTracks(existing.TracksJSON) {
			if t.CanonicalID != "" {
				local[MemberKey(t)] = t.CanonicalID
			}
		}
		tracks := make([]core.ExternalResult, 0, len(st.members))
		for _, k := range st.orderedMembers() {
			e := st.members[k].Entry
			if e == nil {
				continue
			}
			entry := *e
			entry.CanonicalID = local[k]
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

	// Fields the log does not carry keep what the row already says, rather than
	// being blanked by a half-delivered entity.
	name := existing.Name
	if v, ok := st.fields[FieldName]; ok {
		name = str(v)
	}
	cover := existing.CoverURL
	if v, ok := st.fields[FieldCoverURL]; ok {
		cover = str(v)
	}

	if !exists {
		createdAt := int64(num(st.fields[FieldCreatedAt]))
		if createdAt == 0 {
			createdAt = time.Now().Unix()
		}
		externalID := str(st.fields[FieldExternalID])
		if externalID == "" {
			externalID = id
		}
		if _, err := s.store.Upsert(ctx, core.SyncedPlaylist{
			ID:         id,
			Source:     str(st.fields[FieldSource]),
			ExternalID: externalID,
			Name:       name,
			CoverURL:   cover,
			Mode:       mode,
		}, tracksJSON, createdAt); err != nil {
			return err
		}
		// A playlist that arrived whole was synced when its author made it, so
		// it is stamped rather than shown as "Never synced" on this device.
		existing.LastSyncedAt = createdAt
	}

	if err := s.store.UpdateTracks(ctx, id, name, cover, tracksJSON, existing.LastSyncedAt); err != nil {
		return err
	}
	// Settings the log does not carry are left alone for the same reason as the
	// name: a half-delivered entity must not switch off a playlist's auto-sync.
	if st.fields[FieldSyncEnabled] == nil && st.fields[FieldSyncIntervalSec] == nil && st.fields[FieldAutoDownload] == nil {
		return nil
	}
	return s.store.UpdateSettings(ctx, id,
		boolean(st.fields[FieldSyncEnabled]),
		num(st.fields[FieldSyncIntervalSec]),
		boolean(st.fields[FieldAutoDownload]),
	)
}
