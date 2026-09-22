// Package audiotag reads what a music file says it is: its tags, with its path
// as the fallback. The local-files library indexes a folder with it and P2P
// file sync records it beside each file's hash, so a device that keeps only
// some of a peer's files (a phone's offline set) can find a playlist's tracks
// among them under the same names its own library will give them.
package audiotag

import (
	"os"
	"path"
	"strings"

	"github.com/dhowden/tag"
)

// Placeholders for a file whose tags and folders name no artist or album.
const (
	UnknownArtist = "Unknown Artist"
	UnknownAlbum  = "Unknown Album"
)

// ContentTypes are the audio formats Reverb recognises, by lower-case
// extension, with the type a player needs to be told. Source-native downloads
// land as AAC in MP4 and Opus in WebM or Ogg, so those are here beside the
// usual formats. The file itself is served as it is; nothing is decoded.
var ContentTypes = map[string]string{
	".mp3":  "audio/mpeg",
	".flac": "audio/flac",
	".m4a":  "audio/mp4",
	".mp4":  "audio/mp4",
	".aac":  "audio/aac",
	".ogg":  "audio/ogg",
	".oga":  "audio/ogg",
	".opus": "audio/ogg",
	".webm": "audio/webm",
	".wav":  "audio/wav",
}

// IsAudio reports whether a slash-separated path names an audio file.
func IsAudio(rel string) bool {
	return ContentTypes[strings.ToLower(path.Ext(rel))] != ""
}

// Info is one file's identity. Title, Artist and Album are never empty.
type Info struct {
	Title, Artist, Album string
	// AlbumArtist is empty when the tags do not name one.
	AlbumArtist string
	ISRC        string
	Track, Disc int
	Year        int
	HasPicture  bool
}

// Read reads the file at p, whose slash-separated path inside the library is
// rel. Tags win; a file with none, or tags the reader cannot parse, falls back
// to its path: Artist/Album/Title.ext.
func Read(p, rel string) Info {
	dirs := strings.Split(path.Dir(rel), "/")
	if len(dirs) == 1 && dirs[0] == "." {
		dirs = nil
	}
	info := Info{
		Title:  strings.TrimSuffix(path.Base(rel), path.Ext(rel)),
		Artist: UnknownArtist,
		Album:  UnknownAlbum,
	}
	if n := len(dirs); n >= 1 {
		info.Album = dirs[n-1]
	}
	if n := len(dirs); n >= 2 {
		info.Artist = dirs[n-2]
	}
	if m, ok := readTags(p); ok {
		info.Title = firstNonEmpty(m.Title(), info.Title)
		info.Artist = firstNonEmpty(m.Artist(), info.Artist)
		info.Album = firstNonEmpty(m.Album(), info.Album)
		info.AlbumArtist = strings.TrimSpace(m.AlbumArtist())
		info.Track, _ = m.Track()
		info.Disc, _ = m.Disc()
		info.Year = m.Year()
		info.ISRC = rawString(m.Raw(), "TSRC", "isrc", "ISRC")
		info.HasPicture = m.Picture() != nil && len(m.Picture().Data) > 0
	}
	return info
}

// Picture returns the embedded picture of the file at p, or nil.
func Picture(p string) *tag.Picture {
	m, ok := readTags(p)
	if !ok {
		return nil
	}
	return m.Picture()
}

func readTags(p string) (tag.Metadata, bool) {
	f, err := os.Open(p)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	m, err := tag.ReadFrom(f)
	if err != nil {
		return nil, false
	}
	return m, true
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}

func rawString(raw map[string]interface{}, keys ...string) string {
	for _, k := range keys {
		if s, ok := raw[k].(string); ok && strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	}
	return ""
}
