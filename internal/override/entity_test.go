package override

import (
	"context"
	"testing"

	"github.com/uhhhm/reverb/internal/core"
	"github.com/uhhhm/reverb/internal/store"
)

func openEntities(t *testing.T) *Entities {
	t.Helper()
	st, err := store.Open(t.TempDir() + "/x.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	return NewEntities(st.Q())
}

func TestAlbumKey_Normalisation(t *testing.T) {
	tests := []struct {
		name    string
		a1, b1  string // first pair (artist, album)
		a2, b2  string // second pair
		collide bool
	}{
		{
			name: "case insensitive",
			a1:   "The Beatles", b1: "Abbey Road",
			a2: "the beatles", b2: "abbey road",
			collide: true,
		},
		{
			name: "punctuation dropped",
			a1:   "AC/DC", b1: "Back in Black!!",
			a2: "ac dc", b2: "back in black",
			collide: true,
		},
		{
			name: "ampersand vs and",
			a1:   "Simon & Garfunkel", b1: "Bridge",
			a2: "Simon and Garfunkel", b2: "Bridge",
			collide: true,
		},
		{
			name: "diacritics folded",
			a1:   "Björk", b1: "Jóga",
			a2: "bjork", b2: "joga",
			collide: true,
		},
		{
			name: "primary artist splitting semicolon",
			a1:   "Egzod; Maestro Chives; Neoni", b1: "Some Album",
			a2: "Egzod", b2: "Some Album",
			collide: true,
		},
		{
			name: "primary artist feat splitting",
			a1:   "Sunrise feat. Aluna", b1: "Album",
			a2: "Sunrise", b2: "Album",
			collide: true,
		},
		{
			name: "different album title does not collide",
			a1:   "Artist", b1: "Album One",
			a2: "Artist", b2: "Album Two",
			collide: false,
		},
		{
			name: "different artist does not collide",
			a1:   "Artist A", b1: "Album",
			a2: "Artist B", b2: "Album",
			collide: false,
		},
		{
			name: "whitespace trimmed and collapsed",
			a1:   "  Artist  ", b1: "  Album   Name  ",
			a2: "artist", b2: "album name",
			collide: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			k1 := AlbumKey(tc.a1, tc.b1)
			k2 := AlbumKey(tc.a2, tc.b2)
			if tc.collide && k1 != k2 {
				t.Fatalf("expected collision but keys differ: %q vs %q", k1, k2)
			}
			if !tc.collide && k1 == k2 {
				t.Fatalf("expected no collision but keys equal: %q", k1)
			}
		})
	}
}

func TestAlbumKey_Empty(t *testing.T) {
	if got := AlbumKey("", ""); got != "" {
		t.Fatalf("AlbumKey empty -> %q want empty", got)
	}
	if got := AlbumKey("", "Album"); got == "" {
		t.Fatal("expected non-empty key when album non-empty")
	}
	if got := AlbumKey("Artist", ""); got == "" {
		t.Fatal("expected non-empty key when artist non-empty")
	}
}

func TestArtistKey_Normalisation(t *testing.T) {
	tests := []struct {
		name    string
		n1, n2  string
		collide bool
	}{
		{
			name: "case insensitive",
			n1:   "Radiohead", n2: "radiohead",
			collide: true,
		},
		{
			name: "punctuation dropped",
			n1:   "AC/DC!", n2: "ac dc",
			collide: true,
		},
		{
			name: "ampersand and vs and",
			n1:   "Simon & Garfunkel", n2: "simon and garfunkel",
			collide: true,
		},
		{
			name: "diacritics",
			n1:   "Björk", n2: "bjork",
			collide: true,
		},
		{
			name: "primary artist split semicolon",
			n1:   "Egzod; Maestro Chives; Neoni", n2: "Egzod",
			collide: true,
		},
		{
			name: "feat split",
			n1:   "Artist feat. Someone", n2: "Artist",
			collide: true,
		},
		{
			name: "different artists do not collide",
			n1:   "Artist A", n2: "Artist B",
			collide: false,
		},
		{
			name: "acdc slash is not split for artist key (unambiguous only)",
			n1:   "AC/DC", n2: "AC",
			collide: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			k1 := ArtistKey(tc.n1)
			k2 := ArtistKey(tc.n2)
			if tc.collide && k1 != k2 {
				t.Fatalf("expected collision but %q != %q", k1, k2)
			}
			if !tc.collide && k1 == k2 {
				t.Fatalf("expected no collision but both %q", k1)
			}
		})
	}
}

func TestArtistKey_Empty(t *testing.T) {
	if got := ArtistKey(""); got != "" {
		t.Fatalf("empty artist key -> %q want empty", got)
	}
	if got := ArtistKey("   "); got != "" {
		t.Fatalf("whitespace artist key -> %q want empty", got)
	}
}

func TestEntities_SetGetDelete(t *testing.T) {
	ctx := context.Background()
	e := openEntities(t)

	// Set album rename.
	artist := "Original Artist"
	album := "Original Album"
	key := AlbumKey(artist, album)
	if err := e.Set(ctx, KindAlbum, "album-1", key, "Renamed Album"); err != nil {
		t.Fatal(err)
	}
	got, err := e.Get(ctx, KindAlbum, "album-1")
	if err != nil {
		t.Fatal(err)
	}
	if got != "Renamed Album" {
		t.Fatalf("Get = %q want Renamed Album", got)
	}

	// Set with empty name deletes the row.
	if err := e.Set(ctx, KindAlbum, "album-1", key, ""); err != nil {
		t.Fatal(err)
	}
	got, err = e.Get(ctx, KindAlbum, "album-1")
	if err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Fatalf("after delete Get = %q want empty", got)
	}

	// Whitespace-only name also deletes.
	if err := e.Set(ctx, KindAlbum, "album-2", AlbumKey("A", "B"), "  something  "); err != nil {
		t.Fatal(err)
	}
	if err := e.Set(ctx, KindAlbum, "album-2", AlbumKey("A", "B"), "   "); err != nil {
		t.Fatal(err)
	}
	got, _ = e.Get(ctx, KindAlbum, "album-2")
	if got != "" {
		t.Fatalf("whitespace delete failed, got %q", got)
	}
}

func TestEntities_SetEmptyDeletesBothKeys(t *testing.T) {
	ctx := context.Background()
	e := openEntities(t)
	key := AlbumKey("Artist", "Album")
	// Insert via Set with key.
	if err := e.Set(ctx, KindAlbum, "id-1", key, "Name"); err != nil {
		t.Fatal(err)
	}
	// Deleting via same key but different id should remove key-indexed entry too.
	// Set with empty name deletes both entity_id row and entity_key row.
	if err := e.Set(ctx, KindAlbum, "id-1", key, ""); err != nil {
		t.Fatal(err)
	}
	// Verify no row remains under either id or key by trying SetByKey park.
	// After deletion, SetByKey with same key should park under key (no existing binding).
	if err := e.SetByKey(ctx, KindAlbum, key, "Parked"); err != nil {
		t.Fatal(err)
	}
	// The parked row has entity_id == key. Get via key id should return Parked.
	got, _ := e.Get(ctx, KindAlbum, key)
	if got != "Parked" {
		t.Fatalf("expected parked name, got %q", got)
	}
}

func TestEntities_ApplyAlbums_RewritesAndCascades(t *testing.T) {
	ctx := context.Background()
	e := openEntities(t)

	albumArtist := "Old Artist"
	albumName := "Old Album"
	albumID := "alb-1"
	artistID := "art-1"

	albumKey := AlbumKey(albumArtist, albumName)
	artistKey := ArtistKey(albumArtist)

	if err := e.Set(ctx, KindAlbum, albumID, albumKey, "New Album"); err != nil {
		t.Fatal(err)
	}
	if err := e.Set(ctx, KindArtist, artistID, artistKey, "New Artist"); err != nil {
		t.Fatal(err)
	}

	tracks := []core.Track{
		{ID: "t-1", Title: "Song", AlbumID: albumID, Album: albumName, ArtistID: artistID, Artist: albumArtist},
		{ID: "t-2", Title: "Song 2", AlbumID: albumID, Album: albumName, ArtistID: artistID, Artist: albumArtist},
	}
	albums := []core.Album{
		{ID: albumID, Name: albumName, ArtistID: artistID, Artist: albumArtist, Tracks: tracks},
	}

	e.ApplyAlbums(ctx, albums)

	if albums[0].Name != "New Album" {
		t.Fatalf("album name not rewritten: got %q", albums[0].Name)
	}
	if albums[0].Artist != "New Artist" {
		t.Fatalf("album artist not rewritten: got %q", albums[0].Artist)
	}
	for i, tr := range albums[0].Tracks {
		if tr.Album != "New Album" {
			t.Fatalf("track %d album not cascaded: got %q", i, tr.Album)
		}
		if tr.Artist != "New Artist" {
			t.Fatalf("track %d artist not cascaded: got %q", i, tr.Artist)
		}
	}
}

func TestEntities_ApplyArtists_Rewrites(t *testing.T) {
	ctx := context.Background()
	e := openEntities(t)

	artistID := "art-10"
	artistName := "Old Artist"
	artistKey := ArtistKey(artistName)
	if err := e.Set(ctx, KindArtist, artistID, artistKey, "New Artist"); err != nil {
		t.Fatal(err)
	}
	albumID := "alb-10"
	albumName := "Some Album"
	albumKey := AlbumKey(artistName, albumName)
	if err := e.Set(ctx, KindAlbum, albumID, albumKey, "New Album"); err != nil {
		t.Fatal(err)
	}

	artists := []core.Artist{
		{
			ID:   artistID,
			Name: artistName,
			Albums: []core.Album{
				{ID: albumID, Name: albumName, ArtistID: artistID, Artist: artistName, Tracks: []core.Track{
					{ID: "t10", AlbumID: albumID, Album: albumName, ArtistID: artistID, Artist: artistName},
				}},
			},
		},
	}

	e.ApplyArtists(ctx, artists)

	if artists[0].Name != "New Artist" {
		t.Fatalf("artist name not rewritten: %q", artists[0].Name)
	}
	if artists[0].Albums[0].Name != "New Album" {
		t.Fatalf("nested album not rewritten: %q", artists[0].Albums[0].Name)
	}
	if artists[0].Albums[0].Artist != "New Artist" {
		t.Fatalf("nested album artist not rewritten: %q", artists[0].Albums[0].Artist)
	}
	if artists[0].Albums[0].Tracks[0].Artist != "New Artist" {
		t.Fatalf("nested track artist not rewritten: %q", artists[0].Albums[0].Tracks[0].Artist)
	}
	if artists[0].Albums[0].Tracks[0].Album != "New Album" {
		t.Fatalf("nested track album not rewritten: %q", artists[0].Albums[0].Tracks[0].Album)
	}
}

func TestEntities_ApplyTracks_Cascades(t *testing.T) {
	ctx := context.Background()
	e := openEntities(t)

	albumArtist := "Old Artist"
	albumName := "Old Album"
	albumID := "alb-t-1"
	artistID := "art-t-1"
	albumKey := AlbumKey(albumArtist, albumName)
	artistKey := ArtistKey(albumArtist)
	if err := e.Set(ctx, KindAlbum, albumID, albumKey, "New Album"); err != nil {
		t.Fatal(err)
	}
	if err := e.Set(ctx, KindArtist, artistID, artistKey, "New Artist"); err != nil {
		t.Fatal(err)
	}

	tracks := []core.Track{
		{ID: "t-a", AlbumID: albumID, Album: albumName, ArtistID: artistID, Artist: albumArtist},
	}
	e.ApplyTracks(ctx, tracks)
	if tracks[0].Album != "New Album" {
		t.Fatalf("track album not rewritten: %q", tracks[0].Album)
	}
	if tracks[0].Artist != "New Artist" {
		t.Fatalf("track artist not rewritten: %q", tracks[0].Artist)
	}
}

func TestEntities_SetByKey_AppliesViaStableKey(t *testing.T) {
	ctx := context.Background()
	e := openEntities(t)

	artist := "Original Artist"
	album := "Original Album"
	key := AlbumKey(artist, album)

	// Store rename via stable key, no real backend ID yet.
	if err := e.SetByKey(ctx, KindAlbum, key, "Renamed Via Key"); err != nil {
		t.Fatal(err)
	}

	// Album has different backend ID but same library identity.
	albums := []core.Album{
		{ID: "backend-999", Name: album, Artist: artist, ArtistID: "artist-backend-999"},
	}
	e.ApplyAlbums(ctx, albums)
	if albums[0].Name != "Renamed Via Key" {
		t.Fatalf("SetByKey did not apply via stable key: got %q", albums[0].Name)
	}

	// Also test artist SetByKey.
	artistKey := ArtistKey("Original Artist")
	if err := e.SetByKey(ctx, KindArtist, artistKey, "Renamed Artist Via Key"); err != nil {
		t.Fatal(err)
	}
	albums2 := []core.Album{
		{ID: "other-id", Name: "Some Album", Artist: "Original Artist", ArtistID: "different-artist-id"},
	}
	e.ApplyAlbums(ctx, albums2)
	if albums2[0].Artist != "Renamed Artist Via Key" {
		t.Fatalf("artist SetByKey via key failed: got %q", albums2[0].Artist)
	}

	// When a real binding exists, SetByKey should reuse existing entity_id.
	// Seed a real ID row first.
	realID := "real-alb-1"
	realKey := AlbumKey("Real Artist", "Real Album")
	if err := e.Set(ctx, KindAlbum, realID, realKey, "First"); err != nil {
		t.Fatal(err)
	}
	if err := e.SetByKey(ctx, KindAlbum, realKey, "Second"); err != nil {
		t.Fatal(err)
	}
	got, _ := e.Get(ctx, KindAlbum, realID)
	if got != "Second" {
		t.Fatalf("SetByKey should have updated existing id row, got %q", got)
	}
	// Parked key id should not exist as separate row; Get on key should not return Second
	// unless the lookup via nameIndex prefers id. Ensure no duplicate.
	gotParked, _ := e.Get(ctx, KindAlbum, realKey)
	// If realKey == realID it's same; but realKey is hash-like with \x1f, not equal realID.
	// After update, Get on parked id (which equals key) should be empty because row is under realID.
	if gotParked != "" && realKey != realID {
		// The key equals the stable key string, not realID. There should be no row with entity_id == key after reuse.
		t.Fatalf("expected no parked row after reusing real ID, got %q", gotParked)
	}
}

func TestEntities_ApplyAlbums_KeysFromOriginalNames(t *testing.T) {
	ctx := context.Background()
	e := openEntities(t)

	// Original library values.
	originalArtist := "Old Artist"
	originalAlbum := "Old Album"
	albumID := "alb-orig"
	artistID := "art-orig"

	// Artist override renames the artist.
	artistKey := ArtistKey(originalArtist)
	if err := e.Set(ctx, KindArtist, artistID, artistKey, "New Artist"); err != nil {
		t.Fatal(err)
	}
	// Album override is keyed on the *original* artist+album.
	albumKey := AlbumKey(originalArtist, originalAlbum)
	if err := e.Set(ctx, KindAlbum, albumID, albumKey, "New Album"); err != nil {
		t.Fatal(err)
	}

	// Crucially, the library still reports the original names. ApplyAlbums must
	// derive keys from those originals, not from the already-rewritten artist.
	albums := []core.Album{
		{ID: albumID, Name: originalAlbum, Artist: originalArtist, ArtistID: artistID},
	}

	e.ApplyAlbums(ctx, albums)

	if albums[0].Artist != "New Artist" {
		t.Fatalf("artist rewrite failed: got %q want New Artist", albums[0].Artist)
	}
	if albums[0].Name != "New Album" {
		t.Fatalf("album rewrite should use original keys despite artist rename: got %q want New Album", albums[0].Name)
	}

	// If the implementation derived albumKey from the rewritten artist,
	// the lookup would use AlbumKey("New Artist","Old Album") which does NOT
	// match the stored key AlbumKey("Old Artist","Old Album") and would fail.
	// This test asserts the correct behaviour.
}

func TestEntities_NilAndEmptyNoPanic(t *testing.T) {
	ctx := context.Background()

	// Nil *Entities must not panic for any Apply* or Get.
	var nilEnt *Entities

	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("nil ApplyAlbums panicked: %v", r)
			}
		}()
		nilEnt.ApplyAlbums(ctx, nil)
		nilEnt.ApplyAlbums(ctx, []core.Album{})
		nilEnt.ApplyArtists(ctx, nil)
		nilEnt.ApplyArtists(ctx, []core.Artist{})
		nilEnt.ApplyTracks(ctx, nil)
		nilEnt.ApplyTracks(ctx, []core.Track{})
		if _, err := nilEnt.Get(ctx, KindAlbum, "x"); err != nil {
			t.Fatalf("nil Get should not error, got %v", err)
		}
		got, _ := nilEnt.Get(ctx, KindAlbum, "x")
		if got != "" {
			t.Fatalf("nil Get should return empty, got %q", got)
		}
	}()

	// Non-nil but empty slices are also no-ops and must not panic nor require DB reads.
	e := openEntities(t)
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("empty slice panicked: %v", r)
			}
		}()
		e.ApplyAlbums(ctx, nil)
		e.ApplyAlbums(ctx, []core.Album{})
		e.ApplyArtists(ctx, nil)
		e.ApplyArtists(ctx, []core.Artist{})
		e.ApplyTracks(ctx, nil)
		e.ApplyTracks(ctx, []core.Track{})
	}()
}

func TestEntities_GetMissingReturnsEmpty(t *testing.T) {
	ctx := context.Background()
	e := openEntities(t)
	got, err := e.Get(ctx, KindAlbum, "nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Fatalf("want empty, got %q", got)
	}
	var nilEnt *Entities
	got, err = nilEnt.Get(ctx, KindAlbum, "x")
	if err != nil {
		t.Fatalf("nil Get error %v", err)
	}
	if got != "" {
		t.Fatalf("nil Get want empty, got %q", got)
	}
}

func TestEntities_SetValidation(t *testing.T) {
	ctx := context.Background()
	e := openEntities(t)

	if err := e.Set(ctx, KindAlbum, "", AlbumKey("A", "B"), "Name"); err == nil {
		t.Fatal("expected error for missing entity id")
	}
	var nilEnt *Entities
	if err := nilEnt.Set(ctx, KindAlbum, "id", "key", "Name"); err == nil {
		t.Fatal("expected error for nil Entities Set")
	}
	if err := e.SetByKey(ctx, KindAlbum, "", "Name"); err == nil {
		t.Fatal("expected error for missing key in SetByKey")
	}
	if err := nilEnt.SetByKey(ctx, KindAlbum, "key", "Name"); err == nil {
		t.Fatal("expected error for nil SetByKey")
	}
}
