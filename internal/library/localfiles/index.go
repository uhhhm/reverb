package localfiles

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/dhowden/tag"

	"github.com/uhhhm/reverb/internal/audiotag"
	"github.com/uhhhm/reverb/internal/core"
)

// index is one complete read of the directory. It is replaced whole by a scan
// and never mutated afterwards, so readers need no lock once they hold it.
type index struct {
	tracks    map[string]*trackEntry
	albums    map[string]*albumEntry
	artists   map[string]*artistEntry
	playlists map[string]*playlistEntry
	byRel     map[string]string // relative path -> track id

	trackOrder    []string
	albumOrder    []string
	artistOrder   []string
	playlistOrder []string
}

type trackEntry struct {
	track core.Track
	rel   string
	// hasPicture records an embedded picture; the bytes are read again when
	// asked for rather than held for every track in memory.
	hasPicture bool
	modTime    time.Time
}

type albumEntry struct {
	album    core.Album
	trackIDs []string
	// art is the track whose embedded picture is the album's cover, or failing
	// that folderArt, an image file beside the tracks.
	art       string
	folderArt string
	newest    time.Time
}

type artistEntry struct {
	artist   core.Artist
	albumIDs []string
}

type playlistEntry struct {
	id, name string
	trackIDs []string
}

func (p *playlistEntry) summary() core.Playlist {
	return core.Playlist{ID: p.id, Name: p.name, SongCount: len(p.trackIDs)}
}

func (ix *index) trackList(ids []string) []core.Track {
	out := make([]core.Track, 0, len(ids))
	for _, id := range ids {
		if t, ok := ix.tracks[id]; ok {
			out = append(out, t.track)
		}
	}
	return out
}

// scan walks dir and builds a fresh index. A directory that does not exist is
// an empty library. Unreadable entries are skipped rather than failing the
// scan: one bad file must not hide the rest of the library.
func scan(ctx context.Context, dir string) (*index, error) {
	ix := &index{
		tracks:    map[string]*trackEntry{},
		albums:    map[string]*albumEntry{},
		artists:   map[string]*artistEntry{},
		playlists: map[string]*playlistEntry{},
		byRel:     map[string]string{},
	}
	var playlistFiles []string
	folderArt := map[string]string{} // directory rel -> image rel
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if p == dir && errors.Is(err, fs.ErrNotExist) {
				return fs.SkipAll
			}
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if strings.HasPrefix(d.Name(), ".") && p != dir {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() || !d.Type().IsRegular() {
			return nil
		}
		relOS, err := filepath.Rel(dir, p)
		if err != nil {
			return nil
		}
		rel := filepath.ToSlash(relOS)
		ext := strings.ToLower(path.Ext(rel))
		switch {
		case ext == ".m3u" || ext == ".m3u8":
			playlistFiles = append(playlistFiles, rel)
		case isFolderImage(d.Name()):
			if _, seen := folderArt[path.Dir(rel)]; !seen {
				folderArt[path.Dir(rel)] = rel
			}
		case contentTypes[ext] != "":
			info, err := d.Info()
			if err != nil {
				return nil
			}
			ix.addTrack(p, rel, ext, info.ModTime())
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	ix.group(folderArt)
	for _, rel := range playlistFiles {
		ix.addPlaylist(dir, rel)
	}
	ix.sortAll()
	return ix, nil
}

func isFolderImage(name string) bool {
	name = strings.ToLower(name)
	for _, n := range folderImages {
		if name == n {
			return true
		}
	}
	return false
}

func (ix *index) addTrack(p, rel, ext string, mod time.Time) {
	info := audiotag.Read(p, rel)
	title, artist, album := info.Title, info.Artist, info.Album
	trackNo, discNo, year, isrc, hasPicture := info.Track, info.Disc, info.Year, info.ISRC, info.HasPicture
	albumArtist := info.AlbumArtist
	if albumArtist == "" {
		albumArtist = artist
	}

	id := relID(trackPrefix, rel)
	albumID := albumPrefix + digest(fold(albumArtist)+"\x00"+fold(album))
	artistID := artistPrefix + digest(fold(artist))
	ix.tracks[id] = &trackEntry{
		rel: rel, hasPicture: hasPicture, modTime: mod,
		track: core.Track{
			ID: id, Title: title,
			AlbumID: albumID, Album: album,
			ArtistID: artistID, Artist: artist,
			TrackNumber: trackNo, DiscNumber: discNo,
			Suffix:      strings.TrimPrefix(ext, "."),
			ContentType: contentTypes[ext],
			ISRC:        isrc,
		},
	}
	ix.byRel[rel] = id

	al, ok := ix.albums[albumID]
	if !ok {
		al = &albumEntry{album: core.Album{
			ID: albumID, Name: album, Year: year,
			ArtistID: ix.ensureArtist(albumArtist), Artist: albumArtist,
		}}
		ix.albums[albumID] = al
		ar := ix.artists[al.album.ArtistID]
		ar.albumIDs = append(ar.albumIDs, albumID)
	}
	al.trackIDs = append(al.trackIDs, id)
	if hasPicture && al.art == "" {
		al.art = id
	}
	if mod.After(al.newest) {
		al.newest = mod
	}
	ix.ensureArtist(artist)
}

func (ix *index) ensureArtist(name string) string {
	id := artistPrefix + digest(fold(name))
	if _, ok := ix.artists[id]; !ok {
		ix.artists[id] = &artistEntry{artist: core.Artist{ID: id, Name: name}}
	}
	return id
}

// group settles what depends on a whole album being seen: its cover, its
// counts, and the cover every track and artist borrows from it.
func (ix *index) group(folderArt map[string]string) {
	for _, al := range ix.albums {
		sort.SliceStable(al.trackIDs, func(i, j int) bool {
			return trackLess(ix.tracks[al.trackIDs[i]], ix.tracks[al.trackIDs[j]])
		})
		if al.art == "" {
			for _, id := range al.trackIDs {
				if img, ok := folderArt[path.Dir(ix.tracks[id].rel)]; ok {
					al.folderArt = img
					break
				}
			}
		}
		if al.art != "" || al.folderArt != "" {
			al.album.CoverArtID = al.album.ID
		}
		al.album.SongCount = len(al.trackIDs)
		for _, id := range al.trackIDs {
			ix.tracks[id].track.CoverArtID = al.album.CoverArtID
		}
	}
	for _, ar := range ix.artists {
		sort.SliceStable(ar.albumIDs, func(i, j int) bool {
			return fold(ix.albums[ar.albumIDs[i]].album.Name) < fold(ix.albums[ar.albumIDs[j]].album.Name)
		})
		ar.artist.AlbumCount = len(ar.albumIDs)
		for _, alID := range ar.albumIDs {
			if c := ix.albums[alID].album.CoverArtID; c != "" {
				ar.artist.CoverArtID = c
				break
			}
		}
	}
}

func trackLess(a, b *trackEntry) bool {
	if a.track.DiscNumber != b.track.DiscNumber {
		return a.track.DiscNumber < b.track.DiscNumber
	}
	if a.track.TrackNumber != b.track.TrackNumber {
		return a.track.TrackNumber < b.track.TrackNumber
	}
	if fa, fb := fold(a.track.Title), fold(b.track.Title); fa != fb {
		return fa < fb
	}
	return a.rel < b.rel
}

// addPlaylist reads an M3U file. Entries resolve relative to the playlist's
// own folder; one that points outside the library, or at no indexed track, is
// dropped.
func (ix *index) addPlaylist(dir, rel string) {
	raw, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
	if err != nil {
		return
	}
	raw = bytes.TrimPrefix(raw, []byte("\xef\xbb\xbf"))
	pl := &playlistEntry{
		id:   relID(playlistPrefix, rel),
		name: strings.TrimSuffix(path.Base(rel), path.Ext(rel)),
	}
	sc := bufio.NewScanner(bytes.NewReader(raw))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.ReplaceAll(line, `\`, "/")
		var target string
		if filepath.IsAbs(filepath.FromSlash(line)) {
			r, err := filepath.Rel(dir, filepath.FromSlash(line))
			if err != nil {
				continue
			}
			target = filepath.ToSlash(r)
		} else {
			target = path.Join(path.Dir(rel), line)
		}
		target = path.Clean(target)
		if target == ".." || strings.HasPrefix(target, "../") {
			continue
		}
		if id, ok := ix.byRel[target]; ok {
			pl.trackIDs = append(pl.trackIDs, id)
		}
	}
	ix.playlists[pl.id] = pl
}

func (ix *index) sortAll() {
	for id := range ix.tracks {
		ix.trackOrder = append(ix.trackOrder, id)
	}
	sort.Slice(ix.trackOrder, func(i, j int) bool {
		a, b := ix.tracks[ix.trackOrder[i]], ix.tracks[ix.trackOrder[j]]
		if fa, fb := fold(a.track.Artist), fold(b.track.Artist); fa != fb {
			return fa < fb
		}
		if fa, fb := fold(a.track.Album), fold(b.track.Album); fa != fb {
			return fa < fb
		}
		return trackLess(a, b)
	})
	for id := range ix.albums {
		ix.albumOrder = append(ix.albumOrder, id)
	}
	sort.Slice(ix.albumOrder, func(i, j int) bool {
		a, b := ix.albums[ix.albumOrder[i]].album, ix.albums[ix.albumOrder[j]].album
		if fa, fb := fold(a.Name), fold(b.Name); fa != fb {
			return fa < fb
		}
		return fold(a.Artist) < fold(b.Artist)
	})
	for id := range ix.artists {
		ix.artistOrder = append(ix.artistOrder, id)
	}
	sort.Slice(ix.artistOrder, func(i, j int) bool {
		return fold(ix.artists[ix.artistOrder[i]].artist.Name) < fold(ix.artists[ix.artistOrder[j]].artist.Name)
	})
	for id := range ix.playlists {
		ix.playlistOrder = append(ix.playlistOrder, id)
	}
	sort.Slice(ix.playlistOrder, func(i, j int) bool {
		return fold(ix.playlists[ix.playlistOrder[i]].name) < fold(ix.playlists[ix.playlistOrder[j]].name)
	})
}

// Stream serves the file byte for byte. rangeHeader may name one byte range
// ("bytes=a-b", "bytes=a-" or "bytes=-n"); anything else serves the whole
// file, which is what a server that ignores Range does.
func (a *Adapter) Stream(ctx context.Context, trackID string, opts core.StreamOpts, rangeHeader string) (core.StreamHandle, error) {
	ix, err := a.current(ctx)
	if err != nil {
		return core.StreamHandle{}, err
	}
	t, ok := ix.tracks[trackID]
	if !ok {
		return core.StreamHandle{}, notFound("track", trackID)
	}
	f, err := a.openRel(t.rel)
	if err != nil {
		return core.StreamHandle{}, err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return core.StreamHandle{}, err
	}
	size := info.Size()
	h := core.StreamHandle{ContentType: t.track.ContentType, AcceptRanges: "bytes"}
	start, end, ranged, satisfiable := parseRange(rangeHeader, size)
	switch {
	case !ranged:
		h.Body, h.ContentLength, h.StatusCode = f, size, 200
	case !satisfiable:
		f.Close()
		h.Body = io.NopCloser(strings.NewReader(""))
		h.ContentRange = "bytes */" + strconv.FormatInt(size, 10)
		h.StatusCode = 416
	default:
		if _, err := f.Seek(start, io.SeekStart); err != nil {
			f.Close()
			return core.StreamHandle{}, err
		}
		n := end - start + 1
		h.Body = struct {
			io.Reader
			io.Closer
		}{io.LimitReader(f, n), f}
		h.ContentLength = n
		h.ContentRange = fmt.Sprintf("bytes %d-%d/%d", start, end, size)
		h.StatusCode = 206
	}
	return h, nil
}

// openRel opens a file under the directory, refusing anything that resolves
// outside it.
func (a *Adapter) openRel(rel string) (*os.File, error) {
	root, err := os.OpenRoot(a.dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	f, err := root.Open(filepath.FromSlash(rel))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("localfiles: %s: %w", rel, core.ErrLibraryItemNotFound)
	}
	return f, err
}

// parseRange reads a single-range Range header. ranged is false when there is
// no usable range at all; satisfiable is false when the range starts past the
// end of the file.
func parseRange(h string, size int64) (start, end int64, ranged, satisfiable bool) {
	spec, ok := strings.CutPrefix(strings.TrimSpace(h), "bytes=")
	if !ok || strings.Contains(spec, ",") {
		return 0, 0, false, false
	}
	first, last, ok := strings.Cut(strings.TrimSpace(spec), "-")
	if !ok {
		return 0, 0, false, false
	}
	if first == "" {
		n, err := strconv.ParseInt(last, 10, 64)
		if err != nil || n <= 0 {
			return 0, 0, false, false
		}
		if n > size {
			n = size
		}
		if size == 0 {
			return 0, 0, true, false
		}
		return size - n, size - 1, true, true
	}
	s, err := strconv.ParseInt(first, 10, 64)
	if err != nil || s < 0 {
		return 0, 0, false, false
	}
	e := size - 1
	if last != "" {
		v, err := strconv.ParseInt(last, 10, 64)
		if err != nil || v < s {
			return 0, 0, false, false
		}
		if v < e {
			e = v
		}
	}
	if s >= size {
		return 0, 0, true, false
	}
	return s, e, true, true
}

// CoverArt returns the art for a track, album, artist or playlist id: a
// track's embedded picture, then its album's, then an image file in the
// album's folder. size is ignored; images are served as stored.
func (a *Adapter) CoverArt(ctx context.Context, id string, size int) (core.CoverArt, error) {
	ix, err := a.current(ctx)
	if err != nil {
		return core.CoverArt{}, err
	}
	var al *albumEntry
	switch {
	case strings.HasPrefix(id, trackPrefix):
		t, ok := ix.tracks[id]
		if !ok {
			break
		}
		if t.hasPicture {
			if c, err := a.embeddedPicture(t.rel); err == nil {
				return c, nil
			}
		}
		al = ix.albums[t.track.AlbumID]
	case strings.HasPrefix(id, albumPrefix):
		al = ix.albums[id]
	case strings.HasPrefix(id, artistPrefix):
		if ar, ok := ix.artists[id]; ok && ar.artist.CoverArtID != "" {
			al = ix.albums[ar.artist.CoverArtID]
		}
	case strings.HasPrefix(id, playlistPrefix):
		if pl, ok := ix.playlists[id]; ok {
			for _, tid := range pl.trackIDs {
				if cand := ix.albums[ix.tracks[tid].track.AlbumID]; cand.album.CoverArtID != "" {
					al = cand
					break
				}
			}
		}
	}
	if al != nil {
		if al.art != "" {
			if c, err := a.embeddedPicture(ix.tracks[al.art].rel); err == nil {
				return c, nil
			}
		}
		if al.folderArt != "" {
			if f, err := a.openRel(al.folderArt); err == nil {
				return core.CoverArt{Body: f, ContentType: imageType(al.folderArt)}, nil
			}
		}
	}
	return core.CoverArt{}, notFound("cover art", id)
}

func (a *Adapter) embeddedPicture(rel string) (core.CoverArt, error) {
	f, err := a.openRel(rel)
	if err != nil {
		return core.CoverArt{}, err
	}
	defer f.Close()
	m, err := tag.ReadFrom(f)
	if err != nil {
		return core.CoverArt{}, err
	}
	pic := m.Picture()
	if pic == nil || len(pic.Data) == 0 {
		return core.CoverArt{}, notFound("embedded picture", rel)
	}
	ct := pic.MIMEType
	if !strings.HasPrefix(ct, "image/") {
		ct = imageType("x." + pic.Ext)
	}
	return core.CoverArt{Body: io.NopCloser(bytes.NewReader(pic.Data)), ContentType: ct}, nil
}

func imageType(name string) string {
	if strings.HasSuffix(strings.ToLower(name), ".png") {
		return "image/png"
	}
	return "image/jpeg"
}
