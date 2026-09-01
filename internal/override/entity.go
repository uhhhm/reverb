package override

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/uhhhm/reverb/internal/core"
	"github.com/uhhhm/reverb/internal/matching"
	"github.com/uhhhm/reverb/internal/store/db"
)

// Entity types an override can name. They match core.EntityType, restated here
// so the storage layer does not depend on which of them the API exposes.
const (
	KindAlbum  = "album"
	KindArtist = "artist"
)

// AlbumKey is the identity two devices agree on for one album. A backend album
// id belongs to one library backend, so it cannot travel; the normalised artist
// and title can.
func AlbumKey(artist, album string) string {
	a := matching.Normalize(matching.PrimaryArtist(artist))
	t := matching.Normalize(album)
	if a == "" && t == "" {
		return ""
	}
	return a + "\x1f" + t
}

// ArtistKey is the identity two devices agree on for one artist.
func ArtistKey(name string) string { return matching.Normalize(matching.PrimaryArtist(name)) }

// Entities stores user-supplied display names for albums and artists. Like
// track renames, nothing is written back to the library: the name is recorded
// here and applied when albums, artists, and their tracks are read out.
type Entities struct {
	q *db.Queries
}

func NewEntities(q *db.Queries) *Entities { return &Entities{q: q} }

// Set records a rename. An empty name deletes the override, so the table never
// accumulates rows that say nothing. key may be empty when the caller cannot
// derive one; the rename then applies locally but does not replicate.
func (e *Entities) Set(ctx context.Context, kind, entityID, key, name string) error {
	if e == nil || e.q == nil {
		return errors.New("override: no store")
	}
	if entityID == "" {
		return errors.New("override: missing entity id")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		if key != "" {
			if err := e.q.DeleteEntityOverrideByKey(ctx, db.DeleteEntityOverrideByKeyParams{EntityType: kind, EntityKey: key}); err != nil {
				return err
			}
		}
		return e.q.DeleteEntityOverride(ctx, db.DeleteEntityOverrideParams{EntityType: kind, EntityID: entityID})
	}
	return e.q.UpsertEntityOverride(ctx, db.UpsertEntityOverrideParams{
		EntityType: kind,
		EntityID:   entityID,
		EntityKey:  key,
		Name:       name,
		UpdatedAt:  time.Now().Unix(),
	})
}

// SetByKey applies a rename that arrived from a peer, which names the entity by
// its stable key. When this device has no backend id bound to that key the row
// is parked under the key itself, so it takes effect once the id turns up.
func (e *Entities) SetByKey(ctx context.Context, kind, key, name string) error {
	if e == nil || e.q == nil {
		return errors.New("override: no store")
	}
	if key == "" {
		return errors.New("override: missing entity key")
	}
	entityID := key
	if row, err := e.q.GetEntityOverrideByKey(ctx, db.GetEntityOverrideByKeyParams{EntityType: kind, EntityKey: key}); err == nil && row.EntityID != "" {
		entityID = row.EntityID
	}
	return e.Set(ctx, kind, entityID, key, name)
}

// Get returns the rename for one entity, or "" when there is none.
func (e *Entities) Get(ctx context.Context, kind, entityID string) (string, error) {
	if e == nil || e.q == nil {
		return "", nil
	}
	row, err := e.q.GetEntityOverride(ctx, db.GetEntityOverrideParams{EntityType: kind, EntityID: entityID})
	if err != nil {
		return "", nil
	}
	return row.Name, nil
}

// nameIndex loads every override of one kind, indexed by backend id and by
// stable key. Overrides are few, so one read beats a query per row.
func (e *Entities) nameIndex(ctx context.Context, kind string) map[string]string {
	if e == nil || e.q == nil {
		return nil
	}
	rows, err := e.q.ListEntityOverrides(ctx, kind)
	if err != nil || len(rows) == 0 {
		return nil
	}
	out := make(map[string]string, len(rows)*2)
	for _, r := range rows {
		out[r.EntityID] = r.Name
		if r.EntityKey != "" {
			out["k:"+r.EntityKey] = r.Name
		}
	}
	return out
}

func pick(idx map[string]string, id, key string) string {
	if idx == nil {
		return ""
	}
	if n := idx[id]; n != "" {
		return n
	}
	if key != "" {
		return idx["k:"+key]
	}
	return ""
}

// ApplyAlbums rewrites album names in place, cascading into any nested tracks.
func (e *Entities) ApplyAlbums(ctx context.Context, albums []core.Album) {
	if len(albums) == 0 {
		return
	}
	e.applyAlbums(albums, e.nameIndex(ctx, KindAlbum), e.nameIndex(ctx, KindArtist))
}

// ApplyArtists rewrites artist names in place, including any nested albums.
func (e *Entities) ApplyArtists(ctx context.Context, artists []core.Artist) {
	if len(artists) == 0 {
		return
	}
	e.applyArtists(artists, e.nameIndex(ctx, KindAlbum), e.nameIndex(ctx, KindArtist))
}

// ApplyTracks cascades album and artist renames onto the tracks under them. A
// per-track rename is applied afterwards by Service.ApplyTracks and wins, which
// is what a user who renamed one track of a renamed album expects.
func (e *Entities) ApplyTracks(ctx context.Context, tracks []core.Track) {
	if len(tracks) == 0 {
		return
	}
	e.applyTracks(tracks, e.nameIndex(ctx, KindAlbum), e.nameIndex(ctx, KindArtist))
}

// The apply* helpers take the two indices as arguments so a nested structure —
// artist holding albums holding tracks — costs one read of each, not one per
// level.
func (e *Entities) applyArtists(artists []core.Artist, albumIdx, artistIdx map[string]string) {
	for i := range artists {
		if n := pick(artistIdx, artists[i].ID, ArtistKey(artists[i].Name)); n != "" {
			artists[i].Name = n
		}
		e.applyAlbums(artists[i].Albums, albumIdx, artistIdx)
	}
}

func (e *Entities) applyAlbums(albums []core.Album, albumIdx, artistIdx map[string]string) {
	for i := range albums {
		// Keys come from the library's own names, so a renamed artist does not
		// change the identity of the albums under it.
		artistKey := ArtistKey(albums[i].Artist)
		albumKey := AlbumKey(albums[i].Artist, albums[i].Name)
		if n := pick(artistIdx, albums[i].ArtistID, artistKey); n != "" {
			albums[i].Artist = n
		}
		if n := pick(albumIdx, albums[i].ID, albumKey); n != "" {
			albums[i].Name = n
		}
		e.applyTracks(albums[i].Tracks, albumIdx, artistIdx)
	}
}

func (e *Entities) applyTracks(tracks []core.Track, albumIdx, artistIdx map[string]string) {
	for i := range tracks {
		artistKey := ArtistKey(tracks[i].Artist)
		albumKey := AlbumKey(tracks[i].Artist, tracks[i].Album)
		if n := pick(artistIdx, tracks[i].ArtistID, artistKey); n != "" {
			tracks[i].Artist = n
		}
		if n := pick(albumIdx, tracks[i].AlbumID, albumKey); n != "" {
			tracks[i].Album = n
		}
	}
}
