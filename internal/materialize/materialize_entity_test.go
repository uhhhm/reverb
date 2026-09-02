package materialize

import (
	"context"
	"strings"
	"testing"

	"github.com/uhhhm/reverb/internal/core"
	"github.com/uhhhm/reverb/internal/cover"
	"github.com/uhhhm/reverb/internal/override"
	"github.com/uhhhm/reverb/internal/store"
	reverbsync "github.com/uhhhm/reverb/internal/sync"
)

func newEntityService(t *testing.T) (*Service, *override.Service, *override.Entities, *cover.Service) {
	t.Helper()
	st, err := store.Open(t.TempDir() + "/materialize_entity.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	o := override.New(st.Q())
	e := override.NewEntities(st.Q())
	cov := cover.New(st.Q(), t.TempDir())
	svc := New(o, nil).WithEntities(e).WithCovers(cov)
	return svc, o, e, cov
}

func TestApplyEntityAlbumName(t *testing.T) {
	svc, _, entities, _ := newEntityService(t)
	ctx := context.Background()
	key := override.AlbumKey("The Artist", "The Album")
	ch := reverbsync.SyncChange{EntityType: EntityAlbum, EntityID: key, Field: FieldName, Value: "New Name", UpdatedAt: 1000}
	if err := svc.Apply(ctx, ch); err != nil {
		t.Fatalf("Apply album name: %v", err)
	}
	// Readable via Entities.Get (entity_id is the key itself on first write)
	got, err := entities.Get(ctx, override.KindAlbum, key)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != "New Name" {
		t.Fatalf("Get = %q want New Name", got)
	}
	// And via ApplyAlbums (the decorator path)
	albums := []core.Album{{ID: "a1", Name: "The Album", Artist: "The Artist", ArtistID: "ar1"}}
	entities.ApplyAlbums(ctx, albums)
	if albums[0].Name != "New Name" {
		t.Fatalf("ApplyAlbums Name = %q want New Name", albums[0].Name)
	}
	// Also verify that a different album with different key is untouched
	other := []core.Album{{ID: "a2", Name: "Other", Artist: "Other"}}
	entities.ApplyAlbums(ctx, other)
	if other[0].Name != "Other" {
		t.Fatalf("other album changed: %q", other[0].Name)
	}
}

func TestApplyEntityAlbumCoverAssignAndClear(t *testing.T) {
	svc, _, entities, covers := newEntityService(t)
	ctx := context.Background()
	_ = entities // ensure import used
	key := override.AlbumKey("Artist", "Album")
	sha := strings.Repeat("a", 64)
	ext := "jpg"
	value := sha + "." + ext
	ch := reverbsync.SyncChange{EntityType: EntityAlbum, EntityID: key, Field: FieldCover, Value: value, UpdatedAt: 1000}
	if err := svc.Apply(ctx, ch); err != nil {
		t.Fatalf("Apply album cover: %v", err)
	}
	wantID := cover.Prefix + sha + "." + ext
	// Readable via Covers.Get with the stable key as entity id
	got := covers.Get(ctx, cover.KindAlbum, key)
	if got != wantID {
		t.Fatalf("Get cover = %q want %q", got, wantID)
	}
	// And via ApplyAlbums
	albums := []core.Album{{ID: "a1", Name: "Album", Artist: "Artist", CoverArtID: "orig"}}
	covers.ApplyAlbums(ctx, albums)
	if albums[0].CoverArtID != wantID {
		t.Fatalf("ApplyAlbums CoverArtID = %q want %q", albums[0].CoverArtID, wantID)
	}
	// Empty value clears it
	ch2 := reverbsync.SyncChange{EntityType: EntityAlbum, EntityID: key, Field: FieldCover, Value: "", UpdatedAt: 1001}
	if err := svc.Apply(ctx, ch2); err != nil {
		t.Fatalf("clear Apply: %v", err)
	}
	got2 := covers.Get(ctx, cover.KindAlbum, key)
	if got2 != "" {
		t.Fatalf("after clear Get = %q want empty", got2)
	}
	// After clear, ApplyAlbums should leave original
	albums2 := []core.Album{{ID: "a1", Name: "Album", Artist: "Artist", CoverArtID: "orig"}}
	covers.ApplyAlbums(ctx, albums2)
	if albums2[0].CoverArtID != "orig" {
		t.Fatalf("after clear ApplyAlbums = %q want orig (no override)", albums2[0].CoverArtID)
	}
}

func TestApplyEntityArtistName(t *testing.T) {
	svc, _, entities, _ := newEntityService(t)
	ctx := context.Background()
	key := override.ArtistKey("The Artist")
	ch := reverbsync.SyncChange{EntityType: EntityArtist, EntityID: key, Field: FieldName, Value: "Renamed Artist", UpdatedAt: 1000}
	if err := svc.Apply(ctx, ch); err != nil {
		t.Fatalf("Apply artist name: %v", err)
	}
	got, _ := entities.Get(ctx, override.KindArtist, key)
	if got != "Renamed Artist" {
		t.Fatalf("Get = %q want Renamed Artist", got)
	}
	artists := []core.Artist{{ID: "ar1", Name: "The Artist"}}
	entities.ApplyArtists(ctx, artists)
	if artists[0].Name != "Renamed Artist" {
		t.Fatalf("ApplyArtists = %q want Renamed Artist", artists[0].Name)
	}
}

func TestApplyEntityArtistCoverIgnored(t *testing.T) {
	// Only album covers replicate; artist cover should be ignored even if sent.
	svc, _, _, covers := newEntityService(t)
	ctx := context.Background()
	key := override.ArtistKey("Artist")
	sha := strings.Repeat("b", 64)
	value := sha + ".png"
	ch := reverbsync.SyncChange{EntityType: EntityArtist, EntityID: key, Field: FieldCover, Value: value, UpdatedAt: 1000}
	if err := svc.Apply(ctx, ch); err != nil {
		t.Fatalf("Apply artist cover: %v", err)
	}
	// Artist has no cover storage, so Get should be empty
	if got := covers.Get(ctx, cover.KindAlbum, key); got != "" {
		t.Fatalf("artist cover should be ignored, got %q", got)
	}
	if got := covers.Get(ctx, cover.KindTrack, key); got != "" {
		t.Fatalf("artist cover should not affect track, got %q", got)
	}
}

func TestApplyTrackCoverAssignAndClear(t *testing.T) {
	svc, _, _, covers := newEntityService(t)
	ctx := context.Background()
	catID := "cat_cover_1"
	sha := strings.Repeat("c", 64)
	value := sha + ".webp"
	ch := reverbsync.SyncChange{EntityType: EntityTrack, EntityID: catID, Field: FieldCover, Value: value, UpdatedAt: 1000}
	if err := svc.Apply(ctx, ch); err != nil {
		t.Fatalf("Apply track cover: %v", err)
	}
	want := cover.Prefix + sha + ".webp"
	got := covers.Get(ctx, cover.KindTrack, catID)
	if got != want {
		t.Fatalf("track cover Get = %q want %q", got, want)
	}
	tracks := []core.Track{{ID: catID, Title: "T"}}
	covers.ApplyTracks(ctx, tracks)
	if tracks[0].CoverArtID != want {
		t.Fatalf("ApplyTracks CoverArtID = %q want %q", tracks[0].CoverArtID, want)
	}
	// Clear
	ch2 := reverbsync.SyncChange{EntityType: EntityTrack, EntityID: catID, Field: FieldCover, Value: "", UpdatedAt: 1001}
	if err := svc.Apply(ctx, ch2); err != nil {
		t.Fatalf("clear track cover: %v", err)
	}
	if got := covers.Get(ctx, cover.KindTrack, catID); got != "" {
		t.Fatalf("after clear got %q want empty", got)
	}
}

func TestApplyTrackAlbumMergesFields(t *testing.T) {
	svc, overrides, _, _ := newEntityService(t)
	ctx := context.Background()
	catID := "cat_merge"
	// First set title and artist
	if err := svc.Apply(ctx, reverbsync.SyncChange{EntityType: EntityTrack, EntityID: catID, Field: FieldTitle, Value: "Title", UpdatedAt: 1000}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Apply(ctx, reverbsync.SyncChange{EntityType: EntityTrack, EntityID: catID, Field: FieldArtist, Value: "Artist", UpdatedAt: 1001}); err != nil {
		t.Fatal(err)
	}
	got, _ := overrides.GetByCatalogID(ctx, catID)
	if got.Title != "Title" || got.Artist != "Artist" || got.Album != "" {
		t.Fatalf("after title+artist: %+v", got)
	}
	// Now set album only
	if err := svc.Apply(ctx, reverbsync.SyncChange{EntityType: EntityTrack, EntityID: catID, Field: FieldAlbum, Value: "New Album", UpdatedAt: 1002}); err != nil {
		t.Fatal(err)
	}
	got, _ = overrides.GetByCatalogID(ctx, catID)
	if got.Title != "Title" {
		t.Fatalf("Title lost after album update: %q", got.Title)
	}
	if got.Artist != "Artist" {
		t.Fatalf("Artist lost after album update: %q", got.Artist)
	}
	if got.Album != "New Album" {
		t.Fatalf("Album = %q want New Album", got.Album)
	}
	// Clearing album should leave title/artist intact
	if err := svc.Apply(ctx, reverbsync.SyncChange{EntityType: EntityTrack, EntityID: catID, Field: FieldAlbum, Value: "", UpdatedAt: 1003}); err != nil {
		t.Fatal(err)
	}
	got, _ = overrides.GetByCatalogID(ctx, catID)
	if got.Title != "Title" || got.Artist != "Artist" {
		t.Fatalf("title/artist lost after clearing album: %+v", got)
	}
	if got.Album != "" {
		t.Fatalf("Album not cleared: %q", got.Album)
	}
	// Verify ApplyTracks reflects all three fields via decorate
	tracks := []core.Track{{ID: catID, Title: "orig", Artist: "orig", Album: "orig"}}
	// Need to re-apply title/artist/album after clearing? Let's set them again
	_ = svc.Apply(ctx, reverbsync.SyncChange{EntityType: EntityTrack, EntityID: catID, Field: FieldTitle, Value: "Final Title", UpdatedAt: 1004})
	_ = svc.Apply(ctx, reverbsync.SyncChange{EntityType: EntityTrack, EntityID: catID, Field: FieldArtist, Value: "Final Artist", UpdatedAt: 1005})
	_ = svc.Apply(ctx, reverbsync.SyncChange{EntityType: EntityTrack, EntityID: catID, Field: FieldAlbum, Value: "Final Album", UpdatedAt: 1006})
	tracks = []core.Track{{ID: catID}}
	overrides.ApplyTracks(ctx, tracks)
	if tracks[0].Title != "Final Title" || tracks[0].Artist != "Final Artist" || tracks[0].Album != "Final Album" {
		t.Fatalf("ApplyTracks after merge = %+v want all three", tracks[0])
	}
}

func TestApplyIgnoresUnknownEntityAndField(t *testing.T) {
	svc, _, _, _ := newEntityService(t)
	ctx := context.Background()
	// Unknown entity type
	if err := svc.Apply(ctx, reverbsync.SyncChange{EntityType: "unknownEntity", EntityID: "x", Field: "name", Value: "y", UpdatedAt: 1000}); err != nil {
		t.Fatalf("unknown entity should be ignored, got %v", err)
	}
	// Unknown field on known entity
	if err := svc.Apply(ctx, reverbsync.SyncChange{EntityType: EntityAlbum, EntityID: "k", Field: "unknownField", Value: "y", UpdatedAt: 1000}); err != nil {
		t.Fatalf("unknown field on album should be ignored, got %v", err)
	}
	if err := svc.Apply(ctx, reverbsync.SyncChange{EntityType: EntityTrack, EntityID: "cat_1", Field: "unknown", Value: "y", UpdatedAt: 1000}); err != nil {
		t.Fatalf("unknown field on track should be ignored, got %v", err)
	}
	// Track unknown field with cover-like value should still be ignored
	if err := svc.Apply(ctx, reverbsync.SyncChange{EntityType: EntityTrack, EntityID: "cat_1", Field: "notAField", Value: "blah", UpdatedAt: 1000}); err != nil {
		t.Fatalf("unknown track field should be ignored, got %v", err)
	}
	// Ensure no side effects: empty entity id also ignored
	if err := svc.Apply(ctx, reverbsync.SyncChange{EntityType: EntityAlbum, EntityID: "", Field: FieldName, Value: "x"}); err != nil {
		t.Fatalf("empty id should be ignored, got %v", err)
	}
}

func TestApplyEntityWithoutDepsIsNoop(t *testing.T) {
	// Service without Entities/Covers should not error on entity changes,
	// mirroring the projection-after-upgrade behaviour where those changes stay
	// in log until a version that knows them materializes them.
	st, err := store.Open(t.TempDir() + "/no_deps.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	_ = st.Migrate()
	o := override.New(st.Q())
	svc := New(o, nil) // no WithEntities, no WithCovers
	ctx := context.Background()
	key := override.AlbumKey("A", "B")
	if err := svc.Apply(ctx, reverbsync.SyncChange{EntityType: EntityAlbum, EntityID: key, Field: FieldName, Value: "Name"}); err != nil {
		t.Fatalf("without Entities should be noop, got %v", err)
	}
	if err := svc.Apply(ctx, reverbsync.SyncChange{EntityType: EntityAlbum, EntityID: key, Field: FieldCover, Value: strings.Repeat("a", 64) + ".jpg"}); err != nil {
		t.Fatalf("without Covers should be noop, got %v", err)
	}
	if err := svc.Apply(ctx, reverbsync.SyncChange{EntityType: EntityTrack, EntityID: "cat1", Field: FieldCover, Value: strings.Repeat("b", 64) + ".png"}); err != nil {
		t.Fatalf("without Covers track cover should be noop, got %v", err)
	}
}

// A cover address off the sync log is untrusted input. It is split at the first
// dot and both halves become a file name, so a peer must not be able to send
// one that names a file outside the cover directory.
func TestApplyRejectsCoverRefsThatEscapeTheCoverDir(t *testing.T) {
	svc, _, _, covers := newEntityService(t)
	ctx := context.Background()
	key := override.AlbumKey("Artist", "Album")

	hostile := []string{
		"x./../../reverb.db",
		"x.../reverb.db",
		strings.Repeat("a", 64) + ".png/../../reverb.db",
		strings.Repeat("a", 63) + ".png",
		strings.Repeat("a", 64) + ".exe",
	}
	for _, value := range hostile {
		album := reverbsync.SyncChange{EntityType: EntityAlbum, EntityID: key, Field: FieldCover, Value: value, UpdatedAt: 1000}
		if err := svc.Apply(ctx, album); err == nil {
			t.Fatalf("album cover %q was accepted", value)
		}
		track := reverbsync.SyncChange{EntityType: EntityTrack, EntityID: "cat_1", Field: FieldCover, Value: value, UpdatedAt: 1000}
		if err := svc.Apply(ctx, track); err == nil {
			t.Fatalf("track cover %q was accepted", value)
		}
	}
	if got := covers.Get(ctx, cover.KindAlbum, key); got != "" {
		t.Fatalf("a malformed cover was stored anyway: %q", got)
	}
}
