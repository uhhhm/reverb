package linkresolve

import (
	"context"
	"errors"
	"testing"
)

func TestLinkResolve(t *testing.T) {
	t.Run("ParseSpotify track with query", func(t *testing.T) {
		kind, id, ok := ParseSpotifyURL("https://open.spotify.com/track/abc123XYZ?si=foo")
		if !ok || kind != "track" || id != "abc123XYZ" {
			t.Fatalf("got %q %q %v", kind, id, ok)
		}
	})
	t.Run("ParseSpotify album without scheme", func(t *testing.T) {
		kind, id, ok := ParseSpotifyURL("open.spotify.com/album/albumID123")
		if !ok || kind != "album" || id != "albumID123" {
			t.Fatalf("got %q %q %v", kind, id, ok)
		}
	})
	t.Run("ParseSpotify playlist with http", func(t *testing.T) {
		kind, id, ok := ParseSpotifyURL("http://open.spotify.com/playlist/PL123abc")
		if !ok || kind != "playlist" || id != "PL123abc" {
			t.Fatalf("got %q %q %v", kind, id, ok)
		}
	})
	t.Run("ParseSpotify uri track", func(t *testing.T) {
		kind, id, ok := ParseSpotifyURL("spotify:track:xyz789")
		if !ok || kind != "track" || id != "xyz789" {
			t.Fatalf("got %q %q %v", kind, id, ok)
		}
	})
	t.Run("ParseSpotify uri album", func(t *testing.T) {
		kind, id, ok := ParseSpotifyURL("spotify:album:alb456")
		if !ok || kind != "album" || id != "alb456" {
			t.Fatalf("got %q %q %v", kind, id, ok)
		}
	})
	t.Run("ParseSpotify uri playlist", func(t *testing.T) {
		kind, id, ok := ParseSpotifyURL("spotify:playlist:pl789")
		if !ok || kind != "playlist" || id != "pl789" {
			t.Fatalf("got %q %q %v", kind, id, ok)
		}
	})
	t.Run("ParseSpotify with trailing slash", func(t *testing.T) {
		kind, id, ok := ParseSpotifyURL("https://open.spotify.com/track/abc123/")
		if !ok || kind != "track" || id != "abc123" {
			t.Fatalf("got %q %q %v", kind, id, ok)
		}
	})
	t.Run("ParseYouTube watch", func(t *testing.T) {
		kind, id, ok := ParseYouTubeURL("https://www.youtube.com/watch?v=dQw4w9WgXcQ")
		if !ok || kind != "track" || id != "dQw4w9WgXcQ" {
			t.Fatalf("got %q %q %v", kind, id, ok)
		}
	})
	t.Run("ParseYouTube watch music subdomain", func(t *testing.T) {
		kind, id, ok := ParseYouTubeURL("https://music.youtube.com/watch?v=abcDEF12345")
		if !ok || kind != "track" || id != "abcDEF12345" {
			t.Fatalf("got %q %q %v", kind, id, ok)
		}
	})
	t.Run("ParseYouTube youtu.be", func(t *testing.T) {
		kind, id, ok := ParseYouTubeURL("https://youtu.be/dQw4w9WgXcQ")
		if !ok || kind != "track" || id != "dQw4w9WgXcQ" {
			t.Fatalf("got %q %q %v", kind, id, ok)
		}
	})
	t.Run("ParseYouTube youtu.be without scheme", func(t *testing.T) {
		kind, id, ok := ParseYouTubeURL("youtu.be/xyz987")
		if !ok || kind != "track" || id != "xyz987" {
			t.Fatalf("got %q %q %v", kind, id, ok)
		}
	})
	t.Run("ParseYouTube playlist", func(t *testing.T) {
		kind, id, ok := ParseYouTubeURL("https://www.youtube.com/playlist?list=PLabc123XYZ")
		if !ok || kind != "playlist" || id != "PLabc123XYZ" {
			t.Fatalf("got %q %q %v", kind, id, ok)
		}
	})
	t.Run("ParseYouTube playlist without www", func(t *testing.T) {
		kind, id, ok := ParseYouTubeURL("youtube.com/playlist?list=PL123")
		if !ok || kind != "playlist" || id != "PL123" {
			t.Fatalf("got %q %q %v", kind, id, ok)
		}
	})
	t.Run("ResolveURL dispatches spotify", func(t *testing.T) {
		res, err := ResolveURL(context.Background(), "https://open.spotify.com/track/sp1")
		if err != nil || res.Source != "spotify" || res.ExternalID != "sp1" || res.Kind != "track" {
			t.Fatalf("resolve spotify %v %+v", err, res)
		}
	})
	t.Run("ResolveURL dispatches youtube", func(t *testing.T) {
		res, err := ResolveURL(context.Background(), "https://www.youtube.com/watch?v=yt1")
		if err != nil || res.Source != "youtube" || res.ExternalID != "yt1" {
			t.Fatalf("resolve youtube %v %+v", err, res)
		}
	})
	t.Run("ResolveURL unsupported", func(t *testing.T) {
		_, err := ResolveURL(context.Background(), "https://example.com/foo")
		if !errors.Is(err, ErrUnsupportedURL) {
			t.Fatalf("want ErrUnsupportedURL, got %v", err)
		}
	})
	t.Run("ResolveURL youtu.be", func(t *testing.T) {
		res, err := ResolveURL(context.Background(), "https://youtu.be/abc123")
		if err != nil || res.Source != "youtube" || res.ExternalID != "abc123" {
			t.Fatalf("resolve youtu.be %v %+v", err, res)
		}
	})
}
