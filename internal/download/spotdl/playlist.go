package spotdl

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os/exec"
	"regexp"
	"strings"

	"github.com/uhhhm/reverb/internal/core"
	"github.com/uhhhm/reverb/internal/pyrun"
)

//go:embed playlist.py
var playlistScript string

// ErrNotInstalled means the interpreter, or spotDL inside it, is missing.
var ErrNotInstalled = errors.New("spotDL is not installed")

var playlistIDRe = regexp.MustCompile(`^[A-Za-z0-9]+$`)

// PlaylistReader reads a public Spotify playlist through spotDL's own Spotify
// client, which needs no Spotify app credentials. It returns what the Spotify
// search provider's GetPlaylist returns, so it can stand in for it when no
// provider is configured. spotDL's shared client is rate-limited by Spotify
// more readily than an app of one's own.
type PlaylistReader struct {
	py pyrun.ScriptRunner
}

// NewPlaylistReader reads playlists with the spotDL installed in py.
func NewPlaylistReader(py pyrun.ScriptRunner) *PlaylistReader {
	return &PlaylistReader{py: py}
}

type scriptPlaylist struct {
	Name     string `json:"name"`
	CoverURL string `json:"cover_url"`
}

type scriptTrack struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	Artist   string  `json:"artist"`
	Album    string  `json:"album"`
	Duration float64 `json:"duration"` // seconds
	ISRC     string  `json:"isrc"`
	CoverURL string  `json:"cover_url"`
	AlbumID  string  `json:"album_id"`
	ArtistID string  `json:"artist_id"`
}

type scriptError struct {
	Error string `json:"error"`
}

// GetPlaylist lists the playlist with the given Spotify id. Canceling ctx
// stops spotDL.
func (r *PlaylistReader) GetPlaylist(ctx context.Context, externalID string) (core.ExternalPlaylist, error) {
	if !playlistIDRe.MatchString(externalID) {
		return core.ExternalPlaylist{}, fmt.Errorf("spotdl: invalid playlist id %q", externalID)
	}
	pl := core.ExternalPlaylist{Source: "spotify", ExternalID: externalID, Tracks: []core.ExternalResult{}}
	var (
		complete, sawHeader bool
		failure, missing    string
		parseErr            error
		tail                []string // spotDL's last log lines, for an unexplained failure
	)
	onLine := func(line string) {
		kind, payload, ok := strings.Cut(line, " ")
		if !ok || !strings.HasPrefix(kind, "REVERB-") {
			if strings.TrimSpace(line) != "" {
				tail = append(tail, strings.TrimSpace(line))
				if len(tail) > 5 {
					tail = tail[1:]
				}
			}
			return
		}
		var err error
		switch strings.TrimPrefix(kind, "REVERB-") {
		case "PLAYLIST":
			var h scriptPlaylist
			err = json.Unmarshal([]byte(payload), &h)
			pl.Name, pl.CoverURL, sawHeader = h.Name, h.CoverURL, true
		case "TRACK":
			var t scriptTrack
			if err = json.Unmarshal([]byte(payload), &t); err == nil && t.ID != "" {
				pl.Tracks = append(pl.Tracks, core.ExternalResult{
					Source: "spotify", ExternalID: t.ID, Title: t.Name, Artist: t.Artist, Album: t.Album,
					DurationMs: int(math.Round(t.Duration * 1000)), ISRC: t.ISRC, CoverURL: t.CoverURL,
					Type: core.EntityTrack, ArtistExternalID: t.ArtistID, AlbumExternalID: t.AlbumID,
				})
			}
		case "END":
			complete = true
		case "ERROR", "MISSING":
			var e scriptError
			err = json.Unmarshal([]byte(payload), &e)
			if kind == "REVERB-MISSING" {
				missing = e.Error
			} else {
				failure = e.Error
			}
		}
		if err != nil && parseErr == nil {
			parseErr = fmt.Errorf("spotdl: unreadable %s line: %w", kind, err)
		}
	}
	runErr := r.py.RunScript(ctx, playlistScript, []string{"https://open.spotify.com/playlist/" + externalID}, onLine)
	switch {
	case ctx.Err() != nil:
		return core.ExternalPlaylist{}, fmt.Errorf("spotdl: reading playlist: %w", ctx.Err())
	case missing != "":
		return core.ExternalPlaylist{}, fmt.Errorf("%w: %s", ErrNotInstalled, missing)
	case errors.Is(runErr, exec.ErrNotFound), errors.Is(runErr, fs.ErrNotExist):
		return core.ExternalPlaylist{}, fmt.Errorf("%w: %v", ErrNotInstalled, runErr)
	case failure != "":
		return core.ExternalPlaylist{}, fmt.Errorf("spotdl: %s", failure)
	case runErr != nil:
		return core.ExternalPlaylist{}, fmt.Errorf("spotdl: reading playlist: %w: %s", runErr, strings.Join(tail, " | "))
	case parseErr != nil:
		return core.ExternalPlaylist{}, parseErr
	case !sawHeader || !complete:
		return core.ExternalPlaylist{}, errors.New("spotdl: playlist listing ended early")
	}
	return pl, nil
}
