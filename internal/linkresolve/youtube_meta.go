package linkresolve

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// youtubeMeta is the subset of YouTube's oEmbed response we use.
type youtubeMeta struct {
	Title     string
	Artist    string
	Thumbnail string
}

// oembedEndpoint is YouTube's public oEmbed endpoint: no API key, no auth, and
// only the video URL is sent. Overridden in tests.
var oembedEndpoint = "https://www.youtube.com/oembed"

var oembedClient = &http.Client{Timeout: 8 * time.Second}

// fetchYouTubeMeta asks oEmbed for a video's real title and channel. ok is false
// on any failure (offline, private video, rate limit) so the caller can fall back
// to synthetic values rather than failing the whole resolve.
func fetchYouTubeMeta(ctx context.Context, videoURL string) (youtubeMeta, bool) {
	endpoint := oembedEndpoint + "?format=json&url=" + url.QueryEscape(videoURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return youtubeMeta{}, false
	}
	resp, err := oembedClient.Do(req)
	if err != nil {
		return youtubeMeta{}, false
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return youtubeMeta{}, false
	}
	var body struct {
		Title        string `json:"title"`
		AuthorName   string `json:"author_name"`
		ThumbnailURL string `json:"thumbnail_url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return youtubeMeta{}, false
	}
	if strings.TrimSpace(body.Title) == "" {
		return youtubeMeta{}, false
	}
	artist, title := splitArtistTitle(body.Title, body.AuthorName)
	return youtubeMeta{Title: title, Artist: artist, Thumbnail: body.ThumbnailURL}, true
}

// noiseRe-style suffixes YouTube uploaders append to song titles. Stripped so the
// downloaded file is tagged "Song", not "Song (Official Music Video) [4K]".
var titleNoise = []string{
	"official music video", "official video", "official audio", "official lyric video",
	"official visualizer", "lyric video", "lyrics", "audio", "visualizer", "music video",
	"hd", "hq", "4k", "remastered", "full album stream",
}

// splitArtistTitle turns an uploader's video title into (artist, title).
// "Artist - Song (Official Video)" is the dominant convention, so a leading
// "<x> - <y>" is read as artist/title. Otherwise the channel name is the artist,
// with YouTube's auto-generated " - Topic" suffix removed.
func splitArtistTitle(videoTitle, channel string) (artist, title string) {
	t := strings.TrimSpace(videoTitle)
	channel = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(channel), "- Topic"))
	channel = strings.TrimSpace(strings.TrimSuffix(channel, "-"))

	artist = channel
	title = t
	// Split on the first " - " / " – " / " — " separator.
	for _, sep := range []string{" - ", " – ", " — "} {
		if i := strings.Index(t, sep); i > 0 {
			left, right := strings.TrimSpace(t[:i]), strings.TrimSpace(t[i+len(sep):])
			if left != "" && right != "" {
				artist, title = left, right
			}
			break
		}
	}
	title = stripTitleNoise(title)
	if title == "" {
		title = t
	}
	if artist == "" {
		artist = "Unknown"
	}
	return artist, title
}

// stripTitleNoise removes trailing (...) / [...] groups whose contents are known
// promotional noise. Groups with real content (e.g. "(feat. X)") are kept.
func stripTitleNoise(title string) string {
	out := strings.TrimSpace(title)
	for {
		trimmed := false
		for _, pair := range [][2]string{{"(", ")"}, {"[", "]"}} {
			if !strings.HasSuffix(out, pair[1]) {
				continue
			}
			i := strings.LastIndex(out, pair[0])
			if i < 0 {
				continue
			}
			inner := strings.ToLower(strings.TrimSpace(out[i+1 : len(out)-1]))
			for _, noise := range titleNoise {
				if inner == noise {
					out = strings.TrimSpace(out[:i])
					trimmed = true
					break
				}
			}
			if trimmed {
				break
			}
		}
		if !trimmed {
			return out
		}
	}
}
