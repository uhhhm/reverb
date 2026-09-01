package cover

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/uhhhm/reverb/internal/core"
	"github.com/uhhhm/reverb/internal/override"
	"github.com/uhhhm/reverb/internal/store"
)

func openService(t *testing.T) (*Service, string) {
	t.Helper()
	st, err := store.Open(t.TempDir() + "/x.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir() + "/covers"
	// New creates dir if needed
	svc := New(st.Q(), dir)
	return svc, dir
}

func tinyPNG(t *testing.T, col color.RGBA) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, col)
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestDir(t *testing.T) {
	tests := []struct {
		dataDir string
		want    string
	}{
		{dataDir: "/data", want: filepath.Join("/data", "entity-covers")},
		{dataDir: "/tmp/foo", want: filepath.Join("/tmp/foo", "entity-covers")},
		{dataDir: "", want: "entity-covers"},
	}
	for _, tc := range tests {
		if got := Dir(tc.dataDir); got != tc.want {
			t.Fatalf("Dir(%q)=%q want %q", tc.dataDir, got, tc.want)
		}
	}
}

func TestExtFromContentType(t *testing.T) {
	tests := []struct {
		ct   string
		ext  string
		ok   bool
		name string
	}{
		{ct: "image/jpeg", ext: "jpg", ok: true, name: "jpeg"},
		{ct: "image/jpg", ext: "jpg", ok: true, name: "jpg alias"},
		{ct: "image/png", ext: "png", ok: true, name: "png"},
		{ct: "image/webp", ext: "webp", ok: true, name: "webp"},
		{ct: "IMAGE/PNG", ext: "png", ok: true, name: "case insensitive"},
		{ct: "image/png; charset=utf-8", ext: "png", ok: true, name: "with charset param"},
		{ct: "image/jpeg; charset=binary", ext: "jpg", ok: true, name: "jpeg with param"},
		{ct: " text/plain ", ext: "", ok: false, name: "unsupported"},
		{ct: "image/gif", ext: "", ok: false, name: "gif unsupported"},
		{ct: "", ext: "", ok: false, name: "empty"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotExt, gotOK := ExtFromContentType(tc.ct)
			if gotOK != tc.ok || gotExt != tc.ext {
				t.Fatalf("ExtFromContentType(%q)=%q,%v want %q,%v", tc.ct, gotExt, gotOK, tc.ext, tc.ok)
			}
		})
	}
}

func TestContentTypeForExt(t *testing.T) {
	tests := []struct {
		ext  string
		want string
	}{
		{ext: "jpg", want: "image/jpeg"},
		{ext: "png", want: "image/png"},
		{ext: "webp", want: "image/webp"},
		{ext: "gif", want: "application/octet-stream"},
		{ext: "", want: "application/octet-stream"},
		{ext: "PNG", want: "application/octet-stream"},
	}
	for _, tc := range tests {
		if got := ContentTypeForExt(tc.ext); got != tc.want {
			t.Fatalf("ContentTypeForExt(%q)=%q want %q", tc.ext, got, tc.want)
		}
	}
}

func TestStoreRejectsNonImage(t *testing.T) {
	svc, _ := openService(t)

	_, _, err := svc.Store([]byte("not an image"))
	if err == nil {
		t.Fatal("expected error for non-image bytes")
	}
	if err != ErrUnsupportedType {
		// Use errors.Is via direct comparison: ErrUnsupportedType is sentinel
		if !strings.Contains(err.Error(), "unsupported") {
			t.Fatalf("want ErrUnsupportedType, got %v", err)
		}
	}

	// Empty bytes also unsupported.
	if _, _, err := svc.Store(nil); err == nil {
		t.Fatal("expected error for nil bytes")
	}
}

func TestStoreRejectsTooLarge(t *testing.T) {
	svc, _ := openService(t)

	large := make([]byte, MaxBytes+1)
	// Even if it were a valid image, size check must trigger first.
	_, _, err := svc.Store(large)
	if err == nil {
		t.Fatal("expected ErrTooLarge")
	}
	if err != ErrTooLarge {
		if !strings.Contains(err.Error(), "exceeds") {
			t.Fatalf("want ErrTooLarge, got %v", err)
		}
	}

	// Exactly MaxBytes but invalid image should be unsupported, not too large.
	// Generate MaxBytes+1 with correct error precedence - already covered.
	// Test exactly MaxBytes valid PNG should succeed, but that's not needed here.
}

func TestStoreAcceptsPNG(t *testing.T) {
	svc, dir := openService(t)

	data := tinyPNG(t, color.RGBA{255, 0, 0, 255})
	sha, ext, err := svc.Store(data)
	if err != nil {
		t.Fatalf("Store PNG failed: %v", err)
	}
	if ext != "png" {
		t.Fatalf("ext = %q want png", ext)
	}
	if len(sha) != 64 {
		t.Fatalf("sha length = %d want 64", len(sha))
	}
	// File should exist.
	if _, err := os.Stat(filepath.Join(dir, sha+"."+ext)); err != nil {
		t.Fatalf("blob file missing: %v", err)
	}
	// Open round-trip
	id := Prefix + sha + "." + ext
	gotData, ct, ok := svc.Open(id)
	if !ok {
		t.Fatal("Open after Store returned not ok")
	}
	if ct != "image/png" {
		t.Fatalf("contentType = %q want image/png", ct)
	}
	if !bytes.Equal(gotData, data) {
		t.Fatal("round-trip bytes differ")
	}
}

func TestStoreContentAddressed(t *testing.T) {
	svc, dir := openService(t)

	data := tinyPNG(t, color.RGBA{0, 255, 0, 255})
	sha1, ext1, err := svc.Store(data)
	if err != nil {
		t.Fatal(err)
	}
	sha2, ext2, err := svc.Store(data)
	if err != nil {
		t.Fatal(err)
	}
	if sha1 != sha2 || ext1 != ext2 {
		t.Fatalf("same bytes gave different address: %s.%s vs %s.%s", sha1, ext1, sha2, ext2)
	}

	// Directory should contain exactly one file.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 file for content-addressed store, got %d", len(entries))
	}

	// Storing different bytes yields different sha.
	data2 := tinyPNG(t, color.RGBA{0, 0, 255, 255})
	sha3, _, err := svc.Store(data2)
	if err != nil {
		t.Fatal(err)
	}
	if sha3 == sha1 {
		t.Fatal("different bytes should give different sha")
	}
	entries, _ = os.ReadDir(dir)
	if len(entries) != 2 {
		t.Fatalf("expected 2 files after storing different bytes, got %d", len(entries))
	}
}

func TestAssignGetClear_GC(t *testing.T) {
	ctx := context.Background()
	svc, dir := openService(t)

	dataA := tinyPNG(t, color.RGBA{10, 20, 30, 255})
	shaA, extA, err := svc.Store(dataA)
	if err != nil {
		t.Fatal(err)
	}
	dataB := tinyPNG(t, color.RGBA{40, 50, 60, 255})
	shaB, extB, err := svc.Store(dataB)
	if err != nil {
		t.Fatal(err)
	}

	// Assign same blob to two entities.
	album1 := "album-1"
	album2 := "album-2"
	key1 := override.AlbumKey("Artist", "Album One")
	key2 := override.AlbumKey("Artist", "Album Two")

	if err := svc.Assign(ctx, KindAlbum, album1, key1, shaA, extA); err != nil {
		t.Fatal(err)
	}
	if err := svc.Assign(ctx, KindAlbum, album2, key2, shaA, extA); err != nil {
		t.Fatal(err)
	}

	idA := Prefix + shaA + "." + extA
	if got := svc.Get(ctx, KindAlbum, album1); got != idA {
		t.Fatalf("Get album1 = %q want %q", got, idA)
	}
	if got := svc.Get(ctx, KindAlbum, album2); got != idA {
		t.Fatalf("Get album2 = %q want %q", got, idA)
	}

	// Blob file must exist.
	blobPath := filepath.Join(dir, shaA+"."+extA)
	if _, err := os.Stat(blobPath); err != nil {
		t.Fatalf("blob missing before clear: %v", err)
	}

	// Clear one entity: blob should stay because other still references it.
	if err := svc.Clear(ctx, KindAlbum, album1); err != nil {
		t.Fatal(err)
	}
	if got := svc.Get(ctx, KindAlbum, album1); got != "" {
		t.Fatalf("after Clear Get album1 = %q want empty", got)
	}
	if got := svc.Get(ctx, KindAlbum, album2); got != idA {
		t.Fatalf("after clearing album1, album2 should still have cover, got %q", got)
	}
	if _, err := os.Stat(blobPath); err != nil {
		t.Fatalf("blob should still exist after clearing one ref, err %v", err)
	}

	// Clear second entity: blob should be deleted.
	if err := svc.Clear(ctx, KindAlbum, album2); err != nil {
		t.Fatal(err)
	}
	if got := svc.Get(ctx, KindAlbum, album2); got != "" {
		t.Fatalf("after Clear album2 = %q want empty", got)
	}
	if _, err := os.Stat(blobPath); err == nil {
		t.Fatal("blob should be deleted after last ref cleared")
	}

	// Test that reassigning to a different blob GCs old blob.
	// Blobs were deleted after the previous clears; recreate them.
	if _, _, err := svc.Store(dataA); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.Store(dataB); err != nil {
		t.Fatal(err)
	}
	if err := svc.Assign(ctx, KindAlbum, album1, key1, shaA, extA); err != nil {
		t.Fatal(err)
	}
	if err := svc.Assign(ctx, KindAlbum, album2, key2, shaA, extA); err != nil {
		t.Fatal(err)
	}
	// album1 currently points at shaA. Reassign to shaB.
	if err := svc.Assign(ctx, KindAlbum, album1, key1, shaB, extB); err != nil {
		t.Fatal(err)
	}
	if got := svc.Get(ctx, KindAlbum, album1); got != Prefix+shaB+"."+extB {
		t.Fatalf("reassign Get = %q", got)
	}
	// Old blob shaA should still exist because album2 still references it.
	if _, err := os.Stat(filepath.Join(dir, shaA+"."+extA)); err != nil {
		t.Fatalf("old blob should remain while album2 still refs it: %v", err)
	}
	// Clear album2, now no refs to shaA, but album1 refs shaB.
	if err := svc.Clear(ctx, KindAlbum, album2); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, shaA+"."+extA)); err == nil {
		t.Fatal("old blob shaA should be GC'd after last ref cleared")
	}
	if _, err := os.Stat(filepath.Join(dir, shaB+"."+extB)); err != nil {
		t.Fatal("new blob shaB should still exist")
	}

	// Clean up remaining.
	if err := svc.Clear(ctx, KindAlbum, album1); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, shaB+"."+extB)); err == nil {
		t.Fatal("shaB blob should be deleted after last clear")
	}
}

func TestAssignByKeyAndClearByKey(t *testing.T) {
	ctx := context.Background()
	svc, dir := openService(t)

	data := tinyPNG(t, color.RGBA{1, 2, 3, 255})
	sha, ext, err := svc.Store(data)
	if err != nil {
		t.Fatal(err)
	}
	key := override.AlbumKey("Artist", "Album")

	// AssignByKey parks under key when no real id yet.
	if err := svc.AssignByKey(ctx, KindAlbum, key, sha, ext); err != nil {
		t.Fatal(err)
	}
	// Should be retrievable via album that shares that key even with different backend ID.
	albums := []core.Album{
		{ID: "backend-999", Name: "Album", Artist: "Artist"},
	}
	svc.ApplyAlbums(ctx, albums)
	wantID := Prefix + sha + "." + ext
	if albums[0].CoverArtID != wantID {
		t.Fatalf("AssignByKey via stable key not applied, got %q want %q", albums[0].CoverArtID, wantID)
	}

	// ClearByKey should remove the parked cover and GC blob.
	if err := svc.ClearByKey(ctx, KindAlbum, key); err != nil {
		t.Fatal(err)
	}
	albums[0].CoverArtID = ""
	svc.ApplyAlbums(ctx, albums)
	if albums[0].CoverArtID != "" {
		t.Fatalf("after ClearByKey cover should be empty, got %q", albums[0].CoverArtID)
	}
	if _, err := os.Stat(filepath.Join(dir, sha+"."+ext)); err == nil {
		t.Fatal("blob should be deleted after ClearByKey")
	}

	// AssignByKey with empty sha should clear.
	data2 := tinyPNG(t, color.RGBA{9, 9, 9, 255})
	sha2, ext2, _ := svc.Store(data2)
	if err := svc.AssignByKey(ctx, KindAlbum, key, sha2, ext2); err != nil {
		t.Fatal(err)
	}
	if err := svc.AssignByKey(ctx, KindAlbum, key, "", ""); err != nil {
		t.Fatal(err)
	}
	albums[0].CoverArtID = ""
	svc.ApplyAlbums(ctx, albums)
	if albums[0].CoverArtID != "" {
		t.Fatalf("AssignByKey empty sha should clear, got %q", albums[0].CoverArtID)
	}
}

func TestOpen(t *testing.T) {
	svc, dir := openService(t)
	ctx := context.Background()

	data := tinyPNG(t, color.RGBA{123, 45, 67, 255})
	sha, ext, err := svc.Store(data)
	if err != nil {
		t.Fatal(err)
	}
	validID := Prefix + sha + "." + ext

	t.Run("round trips valid id", func(t *testing.T) {
		got, ct, ok := svc.Open(validID)
		if !ok {
			t.Fatal("expected ok for valid id")
		}
		if ct != "image/png" {
			t.Fatalf("ct = %q want image/png", ct)
		}
		if !bytes.Equal(got, data) {
			t.Fatal("data mismatch")
		}
	})

	t.Run("missing blob returns not ok", func(t *testing.T) {
		// Generate a valid-looking but non-existent sha.
		fakeSha := strings.Repeat("b", 64)
		fakeID := Prefix + fakeSha + ".png"
		_, _, ok := svc.Open(fakeID)
		if ok {
			t.Fatal("expected not ok for missing blob")
		}
	})

	// Table-driven invalid IDs that must never return ok (security).
	invalidCases := []struct {
		name string
		id   string
	}{
		{name: "non custom prefix", id: "other:" + sha + ".png"},
		{name: "no prefix", id: sha + ".png"},
		{name: "empty", id: ""},
		{name: "just custom", id: "custom:"},
		{name: "bad hash length short", id: "custom:abc.png"},
		{name: "bad hash length long", id: "custom:" + strings.Repeat("a", 65) + ".png"},
		{name: "non hex hash", id: "custom:" + strings.Repeat("g", 64) + ".png"},
		{name: "non hex hash with digits", id: "custom:" + strings.Repeat("z", 64) + ".png"},
		{name: "bad extension", id: "custom:" + sha + ".bmp"},
		{name: "bad extension txt", id: "custom:" + sha + ".txt"},
		{name: "bad extension empty", id: "custom:" + sha + "."},
		{name: "bad extension uppercase", id: "custom:" + sha + ".PNG"},
		{name: "missing dot", id: "custom:" + sha},
		{name: "path traversal etc passwd", id: "custom:../../etc/passwd.png"},
		{name: "path traversal with valid ext", id: "custom:../../etc/passwd.jpg"},
		{name: "path traversal absolute", id: "custom:/etc/passwd.png"},
		{name: "path traversal with dots", id: "custom:../" + sha + ".png"},
		{name: "double dot ext", id: "custom:" + sha + ".jpg.png"},
		{name: "hash with slash", id: "custom:" + strings.Repeat("a", 32) + "/" + strings.Repeat("a", 32) + ".png"},
		{name: "extra prefix", id: "custom:custom:" + sha + ".png"},
	}
	for _, tc := range invalidCases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, ok := svc.Open(tc.id)
			if ok {
				t.Fatalf("Open(%q) should return ok=false", tc.id)
			}
		})
	}

	// Ensure that assigning then clearing doesn't allow traversal to escape dir.
	// After storing a blob, attempt to open a traversal id should not read the blob.
	t.Run("traversal does not read blob", func(t *testing.T) {
		_, _, ok := svc.Open("custom:../../" + filepath.Base(dir) + "/" + sha + ".png")
		if ok {
			t.Fatal("traversal open should not succeed")
		}
	})

	// Assign a cover then test Open still validates id format, not just file existence.
	t.Run("assign does not affect invalid open", func(t *testing.T) {
		// Ensure the blob exists via assignment.
		if err := svc.Assign(ctx, KindAlbum, "album-x", override.AlbumKey("A", "B"), sha, ext); err != nil {
			t.Fatal(err)
		}
		// Invalid ID that happens to contain correct sha as substring should still fail.
		_, _, ok := svc.Open("custom:" + sha + ".png\x00")
		if ok {
			t.Fatal("null byte injection should fail")
		}
		// Clean up.
		_ = svc.Clear(ctx, KindAlbum, "album-x")
	})

	// Keep blob for other tests; don't delete yet.
	_ = dir
}

func TestCoverApplyTracks_TrackWinsOverAlbum(t *testing.T) {
	ctx := context.Background()
	svc, _ := openService(t)

	// Create two distinct images.
	dataAlbum := tinyPNG(t, color.RGBA{100, 0, 0, 255})
	shaAlbum, extAlbum, err := svc.Store(dataAlbum)
	if err != nil {
		t.Fatal(err)
	}
	dataTrack := tinyPNG(t, color.RGBA{0, 100, 0, 255})
	shaTrack, extTrack, err := svc.Store(dataTrack)
	if err != nil {
		t.Fatal(err)
	}
	albumID := "alb-1"
	trackIDWithCover := "track-1"
	trackIDWithoutCover := "track-2"
	artist := "Artist"
	albumName := "Album"

	// Assign album cover.
	if err := svc.Assign(ctx, KindAlbum, albumID, override.AlbumKey(artist, albumName), shaAlbum, extAlbum); err != nil {
		t.Fatal(err)
	}
	// Assign track cover for one track only.
	if err := svc.Assign(ctx, KindTrack, trackIDWithCover, "", shaTrack, extTrack); err != nil {
		// For track kind, key is usually catalog id; but empty key still works for direct id lookup.
		t.Fatal(err)
	}

	albumCoverID := Prefix + shaAlbum + "." + extAlbum
	trackCoverID := Prefix + shaTrack + "." + extTrack

	tracks := []core.Track{
		{ID: trackIDWithCover, AlbumID: albumID, Album: albumName, Artist: artist},
		{ID: trackIDWithoutCover, AlbumID: albumID, Album: albumName, Artist: artist},
		// Track with unrelated album should get no cover.
		{ID: "track-3", AlbumID: "other-alb", Album: "Other Album", Artist: "Other Artist"},
	}

	svc.ApplyTracks(ctx, tracks)

	if tracks[0].CoverArtID != trackCoverID {
		t.Fatalf("track with own cover should win: got %q want %q", tracks[0].CoverArtID, trackCoverID)
	}
	if tracks[1].CoverArtID != albumCoverID {
		t.Fatalf("track without own cover should fall through to album: got %q want %q", tracks[1].CoverArtID, albumCoverID)
	}
	if tracks[2].CoverArtID != "" {
		t.Fatalf("unrelated track should have no cover, got %q", tracks[2].CoverArtID)
	}

	// Also test that ApplyAlbums cascades to nested tracks the same way.
	albums := []core.Album{
		{
			ID: albumID, Name: albumName, Artist: artist,
			Tracks: []core.Track{
				{ID: trackIDWithCover, AlbumID: albumID, Album: albumName, Artist: artist},
				{ID: trackIDWithoutCover, AlbumID: albumID, Album: albumName, Artist: artist},
			},
		},
	}
	// Reset coverArtIDs
	albums[0].CoverArtID = ""
	albums[0].Tracks[0].CoverArtID = ""
	albums[0].Tracks[1].CoverArtID = ""

	svc.ApplyAlbums(ctx, albums)

	if albums[0].CoverArtID != albumCoverID {
		t.Fatalf("album cover not applied: got %q want %q", albums[0].CoverArtID, albumCoverID)
	}
	if albums[0].Tracks[0].CoverArtID != trackCoverID {
		t.Fatalf("nested track with own cover should win: got %q want %q", albums[0].Tracks[0].CoverArtID, trackCoverID)
	}
	if albums[0].Tracks[1].CoverArtID != albumCoverID {
		t.Fatalf("nested track without cover should fallback: got %q want %q", albums[0].Tracks[1].CoverArtID, albumCoverID)
	}
}

func TestCoverApplyTracks_WithCatalogResolver(t *testing.T) {
	ctx := context.Background()
	svc, _ := openService(t)

	data := tinyPNG(t, color.RGBA{7, 7, 7, 255})
	sha, ext, err := svc.Store(data)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a track whose backend ID differs from its stable catalog ID.
	backendID := "backend-track-1"
	catalogID := "catalog-abc-123"
	// Install resolver: backendID -> catalogID
	svc.SetCatalogResolver(func(ctx context.Context, ids []string) map[string]string {
		m := make(map[string]string)
		for _, id := range ids {
			if id == backendID {
				m[id] = catalogID
			}
		}
		return m
	})
	// Assign cover keyed on catalogID via stable key path (mimicking replication).
	// We can assign via Assign with entityID == catalogID, entityKey == catalogID
	// or via AssignByKey. Use AssignByKey with key=catalogID to park under key.
	// However KindTrack cover with key=catalogID and entity_id=key initially.
	// Then backend track should resolve via catalog map.
	if err := svc.Assign(ctx, KindTrack, catalogID, catalogID, sha, ext); err != nil {
		t.Fatal(err)
	}
	tracks := []core.Track{
		{ID: backendID, AlbumID: "alb-x", Album: "Alb", Artist: "Art"},
	}
	svc.ApplyTracks(ctx, tracks)
	want := Prefix + sha + "." + ext
	if tracks[0].CoverArtID != want {
		t.Fatalf("catalog resolver fallback failed: got %q want %q", tracks[0].CoverArtID, want)
	}
}

func TestCoverNilAndEmptyNoPanic(t *testing.T) {
	ctx := context.Background()
	var nilSvc *Service

	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("nil Service panicked: %v", r)
			}
		}()
		// All apply methods should be no-ops.
		nilSvc.ApplyAlbums(ctx, nil)
		nilSvc.ApplyAlbums(ctx, []core.Album{})
		nilSvc.ApplyArtists(ctx, nil)
		nilSvc.ApplyArtists(ctx, []core.Artist{})
		nilSvc.ApplyTracks(ctx, nil)
		nilSvc.ApplyTracks(ctx, []core.Track{})
		if got := nilSvc.Get(ctx, KindAlbum, "x"); got != "" {
			t.Fatalf("nil Get should return empty, got %q", got)
		}
		if _, _, ok := nilSvc.Open("custom:" + strings.Repeat("a", 64) + ".png"); ok {
			t.Fatal("nil Open should return not ok")
		}
		if _, _, err := nilSvc.Store([]byte("png")); err == nil {
			t.Fatal("nil Store should error")
		}
		if err := nilSvc.Assign(ctx, KindAlbum, "id", "key", "sha", "png"); err == nil {
			t.Fatal("nil Assign should error")
		}
		if err := nilSvc.Clear(ctx, KindAlbum, "id"); err == nil {
			t.Fatal("nil Clear should error")
		}
	}()

	// Non-nil service but empty slices also no-ops.
	svc, _ := openService(t)
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("empty slice panicked: %v", r)
			}
		}()
		svc.ApplyAlbums(ctx, nil)
		svc.ApplyAlbums(ctx, []core.Album{})
		svc.ApplyArtists(ctx, nil)
		svc.ApplyArtists(ctx, []core.Artist{})
		svc.ApplyTracks(ctx, nil)
		svc.ApplyTracks(ctx, []core.Track{})
	}()
}

func TestCoverStoreNilService(t *testing.T) {
	var nilSvc *Service
	if _, _, err := nilSvc.Store([]byte{}); err == nil {
		t.Fatal("expected error for nil service Store")
	}
}
