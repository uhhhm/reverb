// Package localfiles is a library adapter over a plain directory of audio
// files, with no server behind it. It is what a phone runs (ADR 0003): its
// library is the offline set plus downloads waiting to upload, which arrive as
// files through P2P file sync, so reading tags from disk is all a backend has to
// do. Anything else that has files but no Navidrome can use it too.
//
// Ids are derived from the data, not stored: a track's from its path in the
// directory, an album's from its artist and title, an artist's from its name.
// A rescan therefore gives everything it already knew the same id, and a
// peer's file landing at the same path is the same track.
//
// It is pure Go so it builds for iOS and Android. It never decodes audio, so it
// reports no duration and serves files byte for byte: StreamOpts that ask for a
// transcode are ignored.
package localfiles

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/uhhhm/reverb/internal/audiotag"
	"github.com/uhhhm/reverb/internal/core"
	"github.com/uhhhm/reverb/internal/registry"
)

// Name is the adapter's registry name.
const Name = "localfiles"

// Id prefixes. CoverArt accepts any of them, so it has to tell them apart.
const (
	trackPrefix    = "tr-"
	albumPrefix    = "al-"
	artistPrefix   = "ar-"
	playlistPrefix = "pl-"
)

// contentTypes are the audio files the adapter indexes; see audiotag.
var contentTypes = audiotag.ContentTypes

// folderImages are the names a directory's own cover is looked for under,
// when a track carries no embedded picture.
var folderImages = []string{"cover.jpg", "cover.jpeg", "cover.png", "folder.jpg", "folder.jpeg", "folder.png"}

// Adapter indexes one directory. The index is built on first use and rebuilt
// by StartScan; reads between scans see the last complete index.
type Adapter struct {
	dir string

	mu       sync.RWMutex
	idx      *index
	scanning bool
	// scanMu serialises scans so two StartScan calls do not walk at once.
	scanMu sync.Mutex
}

// New returns an unconfigured adapter; Init gives it its directory.
func New() *Adapter { return &Adapter{} }

func (a *Adapter) Type() string { return "library" }
func (a *Adapter) Name() string { return Name }

func (a *Adapter) ConfigSchema() registry.ConfigSchema {
	return registry.ConfigSchema{Fields: []registry.ConfigField{
		{Key: "dir", Label: "Music folder", Type: "string", Required: true},
	}}
}

func (a *Adapter) Init(cfg map[string]any) error {
	dir, _ := cfg["dir"].(string)
	if dir == "" {
		return errors.New("localfiles: dir is required")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return fmt.Errorf("localfiles: %w", err)
	}
	a.mu.Lock()
	a.dir, a.idx = abs, nil
	a.mu.Unlock()
	return nil
}

// TestConnection fails only when the directory exists and cannot be read. A
// directory that does not exist yet is an empty library: on a new phone
// nothing has been synced into it.
func (a *Adapter) TestConnection(ctx context.Context) error {
	if _, err := os.ReadDir(a.dir); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("localfiles: %w", err)
	}
	return nil
}

// LocalMusicDir is the directory the library is read from.
func (a *Adapter) LocalMusicDir() string { return a.dir }

// LocalTrackPath resolves a track id to its file, for the endpoints that read
// the file itself (waveform peaks, loudness, deletion).
func (a *Adapter) LocalTrackPath(id string) (string, bool) {
	ix, err := a.current(context.Background())
	if err != nil {
		return "", false
	}
	t, ok := ix.tracks[id]
	if !ok {
		return "", false
	}
	return filepath.Join(a.dir, filepath.FromSlash(t.rel)), true
}

// current returns the index, building it on first use.
func (a *Adapter) current(ctx context.Context) (*index, error) {
	a.mu.RLock()
	ix := a.idx
	a.mu.RUnlock()
	if ix != nil {
		return ix, nil
	}
	// Requests that arrive together before the first scan wait for one scan
	// rather than each walking the folder again.
	a.scanMu.Lock()
	defer a.scanMu.Unlock()
	a.mu.RLock()
	ix = a.idx
	a.mu.RUnlock()
	if ix != nil {
		return ix, nil
	}
	if err := a.scanLocked(ctx); err != nil {
		return nil, err
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.idx, nil
}

// StartScan re-reads the directory. It runs to completion before returning,
// so ScanStatus never has to be polled for it; a caller that does poll sees
// the scan already finished.
func (a *Adapter) StartScan(ctx context.Context) error {
	if a.dir == "" {
		return errors.New("localfiles: not initialised")
	}
	a.scanMu.Lock()
	defer a.scanMu.Unlock()
	return a.scanLocked(ctx)
}

// scanLocked reads the directory; the caller holds scanMu.
func (a *Adapter) scanLocked(ctx context.Context) error {
	a.mu.Lock()
	a.scanning = true
	a.mu.Unlock()
	ix, err := scan(ctx, a.dir)
	a.mu.Lock()
	defer a.mu.Unlock()
	a.scanning = false
	if err != nil {
		return err
	}
	a.idx = ix
	return nil
}

func (a *Adapter) ScanStatus(ctx context.Context) (core.ScanStatus, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	n := 0
	if a.idx != nil {
		n = len(a.idx.tracks)
	}
	return core.ScanStatus{Scanning: a.scanning, Count: n}, nil
}

func (a *Adapter) Search(ctx context.Context, q string, types []core.EntityType) (core.SearchResults, error) {
	ix, err := a.current(ctx)
	if err != nil {
		return core.SearchResults{}, err
	}
	want := map[core.EntityType]bool{}
	for _, t := range types {
		want[t] = true
	}
	if len(types) == 0 {
		want = map[core.EntityType]bool{core.EntityTrack: true, core.EntityAlbum: true, core.EntityArtist: true}
	}
	q = fold(q)
	out := core.SearchResults{Tracks: []core.Track{}, Albums: []core.Album{}, Artists: []core.Artist{}}
	if want[core.EntityTrack] {
		for _, t := range ix.trackOrder {
			tr := ix.tracks[t].track
			if strings.Contains(fold(tr.Title+" "+tr.Artist+" "+tr.Album), q) {
				out.Tracks = append(out.Tracks, tr)
			}
		}
	}
	if want[core.EntityAlbum] {
		for _, id := range ix.albumOrder {
			al := ix.albums[id]
			if strings.Contains(fold(al.album.Name+" "+al.album.Artist), q) {
				out.Albums = append(out.Albums, al.album)
			}
		}
	}
	if want[core.EntityArtist] {
		for _, id := range ix.artistOrder {
			ar := ix.artists[id]
			if strings.Contains(fold(ar.artist.Name), q) {
				out.Artists = append(out.Artists, ar.artist)
			}
		}
	}
	return out, nil
}

func (a *Adapter) GetArtist(ctx context.Context, id string) (core.Artist, error) {
	ix, err := a.current(ctx)
	if err != nil {
		return core.Artist{}, err
	}
	ar, ok := ix.artists[id]
	if !ok {
		return core.Artist{}, notFound("artist", id)
	}
	out := ar.artist
	out.Albums = make([]core.Album, 0, len(ar.albumIDs))
	for _, alID := range ar.albumIDs {
		out.Albums = append(out.Albums, ix.albums[alID].album)
	}
	return out, nil
}

func (a *Adapter) GetAlbum(ctx context.Context, id string) (core.Album, error) {
	ix, err := a.current(ctx)
	if err != nil {
		return core.Album{}, err
	}
	al, ok := ix.albums[id]
	if !ok {
		return core.Album{}, notFound("album", id)
	}
	out := al.album
	out.Tracks = ix.trackList(al.trackIDs)
	return out, nil
}

// GetPlaylists lists the .m3u and .m3u8 files in the directory. Reverb's own
// playlists live in its database and replicate through the change log; these
// are only whatever playlist files the folder happens to hold.
func (a *Adapter) GetPlaylists(ctx context.Context) ([]core.Playlist, error) {
	ix, err := a.current(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]core.Playlist, 0, len(ix.playlistOrder))
	for _, id := range ix.playlistOrder {
		out = append(out, ix.playlists[id].summary())
	}
	return out, nil
}

func (a *Adapter) GetPlaylist(ctx context.Context, id string) (core.Playlist, error) {
	ix, err := a.current(ctx)
	if err != nil {
		return core.Playlist{}, err
	}
	pl, ok := ix.playlists[id]
	if !ok {
		return core.Playlist{}, notFound("playlist", id)
	}
	out := pl.summary()
	out.Tracks = ix.trackList(pl.trackIDs)
	return out, nil
}

// CreatePlaylist and AddTracksToPlaylist are refused. Writing a playlist file
// into the directory would replicate it to every peer as if it were music.
func (a *Adapter) CreatePlaylist(ctx context.Context, name string) (core.Playlist, error) {
	return core.Playlist{}, fmt.Errorf("localfiles: create playlist: %w", errors.ErrUnsupported)
}

func (a *Adapter) AddTracksToPlaylist(ctx context.Context, playlistID string, trackIDs []string) error {
	return fmt.Errorf("localfiles: add to playlist: %w", errors.ErrUnsupported)
}

// GetArtistsBrowse lists every artist by name.
func (a *Adapter) GetArtistsBrowse(ctx context.Context) ([]core.Artist, error) {
	ix, err := a.current(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]core.Artist, 0, len(ix.artistOrder))
	for _, id := range ix.artistOrder {
		out = append(out, ix.artists[id].artist)
	}
	return out, nil
}

// GetAlbumsBrowse lists albums. "newest" (the default) orders by when their
// newest file arrived; every other list type is alphabetical.
func (a *Adapter) GetAlbumsBrowse(ctx context.Context, listType string, size int) ([]core.Album, error) {
	ix, err := a.current(ctx)
	if err != nil {
		return nil, err
	}
	if size <= 0 {
		size = 50
	}
	ids := append([]string(nil), ix.albumOrder...)
	if listType == "" || listType == "newest" {
		sort.SliceStable(ids, func(i, j int) bool { return ix.albums[ids[i]].newest.After(ix.albums[ids[j]].newest) })
	}
	if len(ids) > size {
		ids = ids[:size]
	}
	out := make([]core.Album, 0, len(ids))
	for _, id := range ids {
		out = append(out, ix.albums[id].album)
	}
	return out, nil
}

// GetSongsBrowse pages through every track, by artist, album and position.
// size <= 0 means everything from offset on.
func (a *Adapter) GetSongsBrowse(ctx context.Context, size, offset int) ([]core.Track, error) {
	ix, err := a.current(ctx)
	if err != nil {
		return nil, err
	}
	ids := ix.trackOrder
	if offset > len(ids) {
		offset = len(ids)
	}
	ids = ids[offset:]
	if size > 0 && len(ids) > size {
		ids = ids[:size]
	}
	return ix.trackList(ids), nil
}

func notFound(kind, id string) error {
	return fmt.Errorf("localfiles: %s %q: %w", kind, id, core.ErrLibraryItemNotFound)
}

// fold is the case-insensitive form names are matched and keyed on.
func fold(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// digest is a short, stable id suffix for a key.
func digest(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:8])
}

// relID is the track id for a path relative to the directory, always with
// forward slashes so a Windows peer and a phone agree.
func relID(prefix, rel string) string { return prefix + digest(path.Clean(rel)) }
