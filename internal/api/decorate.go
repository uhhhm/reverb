package api

import (
	"context"
	"sync"
	"time"

	"github.com/uhhhm/reverb/internal/core"
	"github.com/uhhhm/reverb/internal/store/db"
)

type trackIdentityLookup interface {
	ListTrackIdentitiesByBackendIDs(ctx context.Context, backendIDs []string) ([]db.ListTrackIdentitiesByBackendIDsRow, error)
}

// The decorate* helpers are the one place library data passes through on its
// way out of the API. Everything a user has changed about a track without
// touching the file — renames, crops, uploaded art — is applied here, so a new
// read path gets all of it by calling one function.
//
// Order matters twice over. Uploaded art goes on first, because a cover is
// addressed by a key derived from the library's own artist and album names and
// a rename applied ahead of it would key the lookup off a name the user
// invented. Renames then run outside-in: an album or artist rename cascades
// onto its tracks, and a per-track rename is applied last so it wins.

func (s *Server) decorateTracks(ctx context.Context, tracks []core.Track) {
	if len(tracks) == 0 {
		return
	}
	s.applyTrackIdentities(ctx, tracks)
	s.deps.Covers.ApplyTracks(ctx, tracks)
	s.deps.Entities.ApplyTracks(ctx, tracks)
	s.deps.Overrides.ApplyTracks(ctx, tracks)
	s.deps.Crop.ApplyTracks(ctx, tracks)
}

// applyTrackIdentities projects canonical recording identifiers onto adapter
// tracks. Adapters are not required to know the catalog, but recommendation
// actions on their API results should still use an MBID when Reverb has one.
func (s *Server) applyTrackIdentities(ctx context.Context, tracks []core.Track) {
	lookup, ok := s.deps.Catalog.(trackIdentityLookup)
	if !ok || len(tracks) == 0 {
		return
	}
	ids := make([]string, 0, len(tracks))
	for _, track := range tracks {
		ids = append(ids, track.ID)
	}
	rows, err := lookup.ListTrackIdentitiesByBackendIDs(ctx, ids)
	if err != nil {
		return
	}
	byID := make(map[string]db.ListTrackIdentitiesByBackendIDsRow, len(rows))
	for _, row := range rows {
		byID[row.BackendID] = row
	}
	for i := range tracks {
		if identity, found := byID[tracks[i].ID]; found {
			if tracks[i].ISRC == "" {
				tracks[i].ISRC = identity.Isrc
			}
			if tracks[i].MBID == "" {
				tracks[i].MBID = identity.Mbid
			}
		}
	}
}

func (s *Server) decorateAlbums(ctx context.Context, albums []core.Album) {
	if len(albums) == 0 {
		return
	}
	s.deps.Covers.ApplyAlbums(ctx, albums)
	s.deps.Entities.ApplyAlbums(ctx, albums)
	for i := range albums {
		s.deps.Overrides.ApplyTracks(ctx, albums[i].Tracks)
		s.deps.Crop.ApplyTracks(ctx, albums[i].Tracks)
	}
}

func (s *Server) decorateArtists(ctx context.Context, artists []core.Artist) {
	if len(artists) == 0 {
		return
	}
	s.deps.Covers.ApplyArtists(ctx, artists)
	s.deps.Entities.ApplyArtists(ctx, artists)
	for i := range artists {
		for j := range artists[i].Albums {
			s.deps.Overrides.ApplyTracks(ctx, artists[i].Albums[j].Tracks)
			s.deps.Crop.ApplyTracks(ctx, artists[i].Albums[j].Tracks)
		}
	}
}

// decorateArtist is decorateArtists for a single value the caller owns.
func (s *Server) decorateArtist(ctx context.Context, ar *core.Artist) {
	if ar == nil {
		return
	}
	one := []core.Artist{*ar}
	s.decorateArtists(ctx, one)
	*ar = one[0]
}

// decorateAlbum is decorateAlbums for a single value the caller owns.
func (s *Server) decorateAlbum(ctx context.Context, al *core.Album) {
	if al == nil {
		return
	}
	one := []core.Album{*al}
	s.decorateAlbums(ctx, one)
	*al = one[0]
}

// decorateDetailTracks handles album- and playlist-detail rows. Owned rows
// carry a LibraryTrack for local artwork and metadata. Delegated rows have no
// local track but do have a catalog id, so their synced renames and crops still
// apply. External-only rows have neither. CoverURL continues to point at the
// search source's image rather than a local library cover.
func (s *Server) decorateDetailTracks(ctx context.Context, rows []core.AlbumDetailTrack) {
	if len(rows) == 0 {
		return
	}
	owned := make([]core.Track, 0, len(rows))
	at := make([]int, 0, len(rows))
	for i := range rows {
		if rows[i].LibraryTrack != nil {
			rows[i].Playback = core.PlaybackLocal
			owned = append(owned, *rows[i].LibraryTrack)
			at = append(at, i)
		} else {
			rows[i].Playback = core.PlaybackUnavailable
		}
	}
	if s.deps.DelegatedStream != nil {
		var wg sync.WaitGroup
		for i := range rows {
			if rows[i].LibraryTrack != nil || rows[i].CanonicalID == "" {
				continue
			}
			i := i
			wg.Add(1)
			go func() {
				defer wg.Done()
				probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
				defer cancel()
				if s.deps.DelegatedStream.Playable(probeCtx, rows[i].CanonicalID) {
					rows[i].Playback = core.PlaybackDelegated
				}
			}()
		}
		wg.Wait()
	}
	s.deps.Covers.ApplyTracks(ctx, owned)
	s.deps.Entities.ApplyTracks(ctx, owned)
	for k, i := range at {
		*rows[i].LibraryTrack = owned[k]
		rows[i].Artist = owned[k].Artist
		if owned[k].Album != "" {
			rows[i].Album = owned[k].Album
		}
	}
	s.deps.Overrides.ApplyDetailTracks(ctx, rows)
	s.deps.Crop.ApplyDetailTracks(ctx, rows)
}
