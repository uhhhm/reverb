// Package spotdltest stands in for spotDL's Python API, so code that reads a
// Spotify playlist through spotDL runs on the real host interpreter without
// spotDL or the network.
package spotdltest

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/uhhhm/reverb/internal/pyrun"
)

// Track is one playlist entry the way spotDL's Song exposes it: Duration is
// in seconds, and the optional fields may be absent (nil in Python).
type Track struct {
	ID       string   `json:"song_id"`
	Name     string   `json:"name"`
	Artists  []string `json:"artists"`
	Album    *string  `json:"album_name"`
	Duration int      `json:"duration"`
	ISRC     *string  `json:"isrc"`
	CoverURL *string  `json:"cover_url"`
	AlbumID  *string  `json:"album_id"`
	ArtistID *string  `json:"artist_id"`
}

// Playlist is what the stand-in returns for one playlist id. Error makes
// Playlist.get_metadata raise it, as spotDL does for a private or missing
// playlist; Hang makes it never return.
type Playlist struct {
	Name     string  `json:"name,omitempty"`
	CoverURL string  `json:"cover_url,omitempty"`
	Tracks   []Track `json:"tracks,omitempty"`
	Error    string  `json:"error,omitempty"`
	Hang     bool    `json:"hang,omitempty"`
}

// Spotify is a stand-in spotDL whose playlists a test can change between
// reads.
type Spotify struct {
	t       testing.TB
	fixture string
}

// Set replaces every playlist the stand-in knows, keyed by playlist id.
func (s *Spotify) Set(playlists map[string]Playlist) {
	s.t.Helper()
	b, err := json.Marshal(playlists)
	if err != nil {
		s.t.Fatal(err)
	}
	if err := os.WriteFile(s.fixture, b, 0o644); err != nil {
		s.t.Fatal(err)
	}
}

// Host returns a host runner whose spotdl package is the stand-in, and the
// stand-in's controls. It skips the test when python3 is not installed.
func Host(t testing.TB) (pyrun.Host, *Spotify) {
	t.Helper()
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not installed")
	}
	dir := t.TempDir()
	files := map[string]string{
		"spotdl/__init__.py":       "",
		"spotdl/utils/__init__.py": "",
		"spotdl/types/__init__.py": "",
		"spotdl/utils/config.py":   configPy,
		"spotdl/utils/spotify.py":  spotifyPy,
		"spotdl/types/playlist.py": playlistPy,
	}
	for name, src := range files {
		p := filepath.Join(dir, "lib", name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	s := &Spotify{t: t, fixture: filepath.Join(dir, "playlists.json")}
	s.Set(map[string]Playlist{})
	return pyrun.Host{Python: py, Env: []string{
		"PYTHONPATH=" + filepath.Join(dir, "lib"),
		"REVERB_TEST_SPOTDL_PLAYLISTS=" + s.fixture,
	}}, s
}

const configPy = `DEFAULT_CONFIG = {"client_id": "shared-id", "client_secret": "shared-secret"}
`

// spotifyPy refuses a second init, as spotDL's SpotifyClient does.
const spotifyPy = `class SpotifyError(Exception):
    pass

class SpotifyClient:
    _instance = None

    @classmethod
    def init(cls, client_id, client_secret, **kwargs):
        if cls._instance is not None:
            raise SpotifyError("A spotify client has already been initialized")
        if not client_id or not client_secret:
            raise SpotifyError("client credentials are required")
        cls._instance = object()
        return cls._instance
`

// playlistPy logs around its work on both streams, as spotDL does.
const playlistPy = `import json, os, sys, time
from types import SimpleNamespace
from spotdl.utils.spotify import SpotifyClient

class PlaylistError(Exception):
    pass

class Playlist:
    @staticmethod
    def get_metadata(url):
        if SpotifyClient._instance is None:
            raise Exception("Spotify client not created. Call SpotifyClient.init first.")
        print("[12:00:00] INFO     Processing query: " + url, flush=True)
        print("[12:00:00] DEBUG    a log line on stderr", file=sys.stderr, flush=True)
        with open(os.environ["REVERB_TEST_SPOTDL_PLAYLISTS"]) as f:
            playlists = json.load(f)
        pl = playlists.get(url.rstrip("/").rsplit("/", 1)[-1])
        if pl is None:
            raise PlaylistError("Invalid playlist URL.")
        if pl.get("hang"):
            time.sleep(600)
        if pl.get("error"):
            raise PlaylistError(pl["error"])
        songs = []
        for t in pl.get("tracks", []):
            artists = t.get("artists") or []
            songs.append(SimpleNamespace(
                song_id=t.get("song_id"), name=t.get("name"), artists=artists,
                artist=artists[0] if artists else None, album_name=t.get("album_name"),
                duration=t.get("duration"), isrc=t.get("isrc"), cover_url=t.get("cover_url"),
                album_id=t.get("album_id"), artist_id=t.get("artist_id"),
            ))
        meta = {"name": pl.get("name"), "url": url, "cover_url": pl.get("cover_url", "")}
        print("[12:00:01] INFO     Found %d songs in %s (Playlist)" % (len(songs), pl.get("name")), flush=True)
        return meta, songs
`
