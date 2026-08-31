package api

import (
	"context"

	"github.com/uhhhm/reverb/internal/core"
)

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
	s.deps.Covers.ApplyTracks(ctx, tracks)
	s.deps.Entities.ApplyTracks(ctx, tracks)
	s.deps.Overrides.ApplyTracks(ctx, tracks)
	s.deps.Crop.ApplyTracks(ctx, tracks)
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

// decorateDetailTracks handles album- and playlist-detail rows. Only the owned
// rows carry a LibraryTrack, and only those get renames and uploaded art; a
// missing row is described by a search source and has nothing local to override.
// CoverURL is left alone for the same reason — it points at the search source's
// image, not at the library.
func (s *Server) decorateDetailTracks(ctx context.Context, rows []core.AlbumDetailTrack) {
	if len(rows) == 0 {
		return
	}
	owned := make([]core.Track, 0, len(rows))
	at := make([]int, 0, len(rows))
	for i := range rows {
		if rows[i].LibraryTrack != nil {
			owned = append(owned, *rows[i].LibraryTrack)
			at = append(at, i)
		}
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
