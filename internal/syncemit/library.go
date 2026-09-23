package syncemit

import (
	"context"
	"log"

	"github.com/uhhhm/reverb/internal/catalog"
	"github.com/uhhhm/reverb/internal/core"
	reverbsync "github.com/uhhhm/reverb/internal/sync"
)

const libraryPageSize = 500

// LibraryBrowser is the read seam needed to publish a device's library
// metadata. Both the Subsonic and local-files adapters satisfy it.
type LibraryBrowser interface {
	GetSongsBrowse(ctx context.Context, size, offset int) ([]core.Track, error)
}

// CatalogMinter resolves a library track to its replicated catalog identity.
type CatalogMinter interface {
	CanonicalFor(ctx context.Context, id catalog.Identity) (string, error)
}

// PublishLibrary makes every track visible to peers as catalog metadata, even
// when it has never been played, downloaded through Reverb, or added to a
// playlist. It is safe to run on every boot: CanonicalFor reuses an existing
// entity and EnsureCatalogEntity skips an identity already present in the log.
func (s *Service) PublishLibrary(ctx context.Context, library LibraryBrowser, catalogMinter CatalogMinter) {
	if !s.ready() || library == nil || catalogMinter == nil {
		return
	}
	for offset := 0; ; offset += libraryPageSize {
		if err := ctx.Err(); err != nil {
			return
		}
		tracks, err := library.GetSongsBrowse(ctx, libraryPageSize, offset)
		if err != nil {
			log.Printf("sync library metadata: list tracks at offset %d: %v", offset, err)
			return
		}
		for _, track := range tracks {
			cid, err := catalogMinter.CanonicalFor(ctx, catalog.Identity{
				Kind: "track", Title: track.Title, Artist: track.Artist, Album: track.Album,
				ISRC: track.ISRC, MBID: track.MBID, DurationMs: track.DurationMs,
			})
			if err != nil {
				log.Printf("sync library metadata: catalog track %q by %q: %v", track.Title, track.Artist, err)
				continue
			}
			s.EnsureCatalogEntity(ctx, cid)
			s.EnsureLibraryMembership(ctx, cid)
		}
		if len(tracks) < libraryPageSize {
			return
		}
	}
}

// EnsureLibraryMembership marks a catalog track as in the owner's library
// unless the log already says so. Catalog source describes how an identity was
// first minted, not whether it later entered the library. Deleting the track
// sets the marker false; the boot publish or a download linking the track
// again sets it back.
func (s *Service) EnsureLibraryMembership(ctx context.Context, catalogID string) {
	if !s.ready() || catalogID == "" {
		return
	}
	changes, err := s.log.ListLatestForEntity(ctx, reverbsync.EntityTrack, catalogID)
	if err != nil {
		return
	}
	for _, change := range changes {
		if change.Field == reverbsync.FieldDeleted {
			return
		}
		if present, _ := change.Value.(bool); change.Field == reverbsync.FieldLibraryPresent && present {
			return
		}
	}
	if device := s.device(ctx); device != "" {
		s.append(ctx, device, reverbsync.EntityTrack, catalogID, reverbsync.FieldLibraryPresent, true)
	}
}
