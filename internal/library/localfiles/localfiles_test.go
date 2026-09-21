package localfiles

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/uhhhm/reverb/internal/core"
	"github.com/uhhhm/reverb/internal/library"
)

// id3 builds an ID3v2.3 tag carrying the given text frames and, when art is
// non-nil, an attached front cover. What follows the tag does not have to be
// audio: the adapter reads tags and serves bytes, it never decodes.
func id3(frames map[string]string, art []byte) []byte {
	var body bytes.Buffer
	frame := func(id string, data []byte) {
		body.WriteString(id)
		_ = binary.Write(&body, binary.BigEndian, uint32(len(data)))
		body.Write([]byte{0, 0})
		body.Write(data)
	}
	for id, text := range frames {
		frame(id, append([]byte{0}, text...))
	}
	if art != nil {
		var apic bytes.Buffer
		apic.WriteByte(0)
		apic.WriteString("image/jpeg\x00")
		apic.WriteByte(3)
		apic.WriteByte(0)
		apic.Write(art)
		frame("APIC", apic.Bytes())
	}
	n := body.Len()
	head := []byte{'I', 'D', '3', 3, 0, 0,
		byte(n >> 21 & 0x7f), byte(n >> 14 & 0x7f), byte(n >> 7 & 0x7f), byte(n & 0x7f)}
	return append(head, body.Bytes()...)
}

func writeFile(t *testing.T, root, rel string, data []byte) string {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

var audio = bytes.Repeat([]byte{0xff, 0xfb, 0x90, 0x00}, 64)

func song(title, artist, album, track string, art []byte) []byte {
	return append(id3(map[string]string{"TIT2": title, "TPE1": artist, "TALB": album, "TRCK": track, "TSRC": "GB" + track}, art), audio...)
}

// fixtureLibrary lays out a small library the way a peer's downloads land on
// disk: Artist/Album/NN Title.ext, one playlist file beside them.
func fixtureLibrary(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeFile(t, root, "Band/Record/01 Opener.mp3", song("Opener", "Band", "Record", "1/2", []byte("jpeg-bytes")))
	writeFile(t, root, "Band/Record/02 Closer.mp3", song("Closer", "Band", "Record", "2/2", nil))
	writeFile(t, root, "Other/Single/Song.mp3", song("Test Song", "Other", "Single", "1", nil))
	writeFile(t, root, "Other/Single/folder.jpg", []byte("folder-art"))
	writeFile(t, root, "Road trip.m3u8", []byte("#EXTM3U\n#EXTINF:1,Band - Closer\nBand/Record/02 Closer.mp3\nOther/Single/Song.mp3\n../outside.mp3\nmissing.mp3\n"))
	writeFile(t, root, "notes.txt", []byte("not music"))
	return root
}

func open(t *testing.T, dir string) *Adapter {
	t.Helper()
	a := New()
	if err := a.Init(map[string]any{"dir": dir}); err != nil {
		t.Fatal(err)
	}
	return a
}

func trackTitled(t *testing.T, a *Adapter, title string) core.Track {
	t.Helper()
	songs, err := a.GetSongsBrowse(context.Background(), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range songs {
		if s.Title == title {
			return s
		}
	}
	t.Fatalf("no track titled %q in %+v", title, songs)
	return core.Track{}
}

func TestConformance(t *testing.T) {
	a := open(t, fixtureLibrary(t))
	opener := trackTitled(t, a, "Opener")
	pls, err := a.GetPlaylists(context.Background())
	if err != nil || len(pls) != 1 {
		t.Fatalf("playlists = %+v, %v", pls, err)
	}
	library.RunConformanceWith(t, a, library.Fixture{
		Query:      "test",
		ArtistID:   opener.ArtistID,
		AlbumID:    opener.AlbumID,
		PlaylistID: pls[0].ID,
		TrackID:    opener.ID,
		CoverID:    opener.CoverArtID,
	})
}

func TestIndexesTagsIntoArtistsAlbumsAndTracks(t *testing.T) {
	a := open(t, fixtureLibrary(t))
	ctx := context.Background()
	opener := trackTitled(t, a, "Opener")
	if opener.Artist != "Band" || opener.Album != "Record" || opener.TrackNumber != 1 || opener.Suffix != "mp3" || opener.ContentType != "audio/mpeg" || opener.ISRC != "GB1/2" {
		t.Fatalf("opener = %+v", opener)
	}
	al, err := a.GetAlbum(ctx, opener.AlbumID)
	if err != nil {
		t.Fatal(err)
	}
	if al.Name != "Record" || al.Artist != "Band" || al.SongCount != 2 || len(al.Tracks) != 2 || al.Tracks[0].Title != "Opener" || al.Tracks[1].Title != "Closer" {
		t.Fatalf("album = %+v", al)
	}
	ar, err := a.GetArtist(ctx, opener.ArtistID)
	if err != nil {
		t.Fatal(err)
	}
	if ar.Name != "Band" || ar.AlbumCount != 1 || len(ar.Albums) != 1 {
		t.Fatalf("artist = %+v", ar)
	}
	arts, _ := a.GetArtistsBrowse(ctx)
	if len(arts) != 2 {
		t.Fatalf("artists = %+v", arts)
	}
	res, err := a.Search(ctx, "closer", []core.EntityType{core.EntityTrack, core.EntityAlbum, core.EntityArtist})
	if err != nil || len(res.Tracks) != 1 || res.Tracks[0].Title != "Closer" || len(res.Albums) != 0 {
		t.Fatalf("search = %+v, %v", res, err)
	}
}

func TestUntaggedFileFallsBackToItsPath(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "Some Artist/Some Album/03 Untitled Thing.opus", audio)
	a := open(t, root)
	tr := trackTitled(t, a, "03 Untitled Thing")
	if tr.Artist != "Some Artist" || tr.Album != "Some Album" || tr.ContentType != "audio/ogg" {
		t.Fatalf("track = %+v", tr)
	}
}

func TestPlaylistFilesResolveInsideTheLibraryOnly(t *testing.T) {
	a := open(t, fixtureLibrary(t))
	ctx := context.Background()
	pls, _ := a.GetPlaylists(ctx)
	pl, err := a.GetPlaylist(ctx, pls[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if pl.Name != "Road trip" || pl.SongCount != 2 || len(pl.Tracks) != 2 || pl.Tracks[0].Title != "Closer" || pl.Tracks[1].Title != "Test Song" {
		t.Fatalf("playlist = %+v", pl)
	}
	if _, err := a.CreatePlaylist(ctx, "x"); !errors.Is(err, errors.ErrUnsupported) {
		t.Fatalf("CreatePlaylist err = %v, want unsupported", err)
	}
}

func TestStreamHonoursByteRanges(t *testing.T) {
	root := fixtureLibrary(t)
	a := open(t, root)
	ctx := context.Background()
	tr := trackTitled(t, a, "Closer")
	whole, _ := os.ReadFile(filepath.Join(root, "Band/Record/02 Closer.mp3"))

	h, err := a.Stream(ctx, tr.ID, core.StreamOpts{}, "bytes=2-5")
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(h.Body)
	h.Body.Close()
	if h.StatusCode != 206 || h.ContentLength != 4 || !bytes.Equal(got, whole[2:6]) || h.AcceptRanges != "bytes" {
		t.Fatalf("range: status %d len %d range %q body %q", h.StatusCode, h.ContentLength, h.ContentRange, got)
	}
	if want := "bytes 2-5/" + strconv.Itoa(len(whole)); h.ContentRange != want {
		t.Fatalf("Content-Range = %q, want %q", h.ContentRange, want)
	}

	h, err = a.Stream(ctx, tr.ID, core.StreamOpts{}, "bytes=-3")
	if err != nil {
		t.Fatal(err)
	}
	got, _ = io.ReadAll(h.Body)
	h.Body.Close()
	if h.StatusCode != 206 || !bytes.Equal(got, whole[len(whole)-3:]) {
		t.Fatalf("suffix range: status %d body %q", h.StatusCode, got)
	}

	h, err = a.Stream(ctx, tr.ID, core.StreamOpts{}, "bytes=999999-")
	if err != nil {
		t.Fatal(err)
	}
	h.Body.Close()
	if h.StatusCode != 416 {
		t.Fatalf("unsatisfiable range: status %d", h.StatusCode)
	}

	if _, err := a.Stream(ctx, "tr-nope", core.StreamOpts{}, ""); !errors.Is(err, core.ErrLibraryItemNotFound) {
		t.Fatalf("unknown track err = %v", err)
	}
}

func TestCoverArtComesFromTheTagOrTheFolder(t *testing.T) {
	a := open(t, fixtureLibrary(t))
	ctx := context.Background()
	for title, want := range map[string]string{"Opener": "jpeg-bytes", "Closer": "jpeg-bytes", "Test Song": "folder-art"} {
		tr := trackTitled(t, a, title)
		c, err := a.CoverArt(ctx, tr.CoverArtID, 300)
		if err != nil {
			t.Fatalf("%s: %v", title, err)
		}
		b, _ := io.ReadAll(c.Body)
		c.Body.Close()
		if string(b) != want || c.ContentType != "image/jpeg" {
			t.Fatalf("%s cover = %q (%s), want %q", title, b, c.ContentType, want)
		}
	}
}

func TestRescanFindsNewFilesAndKeepsIDs(t *testing.T) {
	root := fixtureLibrary(t)
	a := open(t, root)
	ctx := context.Background()
	before := trackTitled(t, a, "Opener")
	writeFile(t, root, "Band/Record/03 Encore.mp3", song("Encore", "Band", "Record", "3", nil))
	if err := a.StartScan(ctx); err != nil {
		t.Fatal(err)
	}
	st, _ := a.ScanStatus(ctx)
	if st.Scanning || st.Count != 4 {
		t.Fatalf("scan status = %+v", st)
	}
	if after := trackTitled(t, a, "Opener"); after.ID != before.ID || after.AlbumID != before.AlbumID {
		t.Fatalf("ids moved across a rescan: %+v -> %+v", before, after)
	}
	trackTitled(t, a, "Encore")
	if p, ok := a.LocalTrackPath(before.ID); !ok || p != filepath.Join(root, "Band", "Record", "01 Opener.mp3") {
		t.Fatalf("LocalTrackPath = %q, %v", p, ok)
	}
}

func TestInitRequiresADirectory(t *testing.T) {
	if err := New().Init(map[string]any{}); err == nil {
		t.Fatal("Init without dir succeeded")
	}
	a := open(t, filepath.Join(t.TempDir(), "not-yet"))
	if err := a.TestConnection(context.Background()); err != nil {
		t.Fatalf("a missing library directory is an empty library, got %v", err)
	}
	if st, _ := a.ScanStatus(context.Background()); st.Count != 0 {
		t.Fatalf("count = %d", st.Count)
	}
}
