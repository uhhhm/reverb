// Package ytdlp is the yt-dlp Downloader adapter. It downloads audio directly
// from YouTube (or any other site yt-dlp supports) without going through
// spotDL's Spotify-metadata-first flow, which makes it a useful fallback when
// spotDL's own lookup fails, and the natural handler for a pasted link.
//
// Metadata quality is the trade-off: YouTube titles are not tags. When the
// request carries artist/title/album, they are forced onto the output file via
// --parse-metadata so Navidrome indexes it correctly.
package ytdlp

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/uhhhm/reverb/internal/core"
	"github.com/uhhhm/reverb/internal/download"
	"github.com/uhhhm/reverb/internal/registry"
)

var _ download.Downloader = (*Adapter)(nil)

// progressRe extracts yt-dlp's download percentage, e.g. "[download]  42.3% of ~5MiB".
var progressRe = regexp.MustCompile(`\[download\]\s+(\d{1,3})(?:\.\d+)?%`)

// stageProgress maps yt-dlp's post-processing stage labels to coarse progress,
// so the ring keeps moving after the byte-level download hits 100%.
var stageProgress = []struct {
	re  *regexp.Regexp
	pct int
}{
	{regexp.MustCompile(`\[ExtractAudio\]`), 90},
	{regexp.MustCompile(`\[Metadata\]|\[EmbedThumbnail\]|\[ThumbnailsConvertor\]`), 95},
	{regexp.MustCompile(`has already been downloaded`), 100},
}

const defaultBinary = "yt-dlp"
const defaultAudioFormat = "mp3"
const defaultAudioQuality = "0"

// Adapter implements download.Downloader for yt-dlp.
type Adapter struct {
	runner          Runner
	outputDir       string
	binary          string
	audioFormat     string
	audioQuality    string
	audioFormatSet  bool // operator set audio_format explicitly; overrides quality tiers
	audioQualitySet bool
	cookiesFile     string // path to a written cookies.txt, or "" if not configured
}

func New() *Adapter {
	return &Adapter{runner: ExecRunner{}, binary: defaultBinary}
}

// WithRunner injects a Runner (test seam). Call before Init.
func (a *Adapter) WithRunner(r Runner) *Adapter {
	a.runner = r
	return a
}

func (a *Adapter) Type() string { return "downloader" }
func (a *Adapter) Name() string { return "ytdlp" }

// SupportedGranularities is track-only: an album request carries a Spotify/Deezer
// album ID, which yt-dlp has no way to resolve.
func (a *Adapter) SupportedGranularities() []core.DownloadGranularity {
	return []core.DownloadGranularity{core.GranularityTrack}
}

func (a *Adapter) ConfigSchema() registry.ConfigSchema {
	return registry.ConfigSchema{Fields: []registry.ConfigField{
		{Key: "output_dir", Label: "Output directory", Type: "string", Required: true},
		{Key: "binary_path", Label: "yt-dlp binary path", Type: "string", Required: false,
			Help: "Defaults to \"yt-dlp\" on PATH."},
		{Key: "audio_format", Label: "Audio format override", Type: "string", Required: false,
			Help: "yt-dlp --audio-format value: mp3 (default), opus, m4a, flac, best."},
		{Key: "audio_quality", Label: "Audio quality override", Type: "string", Required: false,
			Help: "Leave empty to follow the download quality tier. Setting either override pins every download to these yt-dlp values instead: --audio-quality is 0 (best) to 10 (worst), or a bitrate like 192K."},
		{
			Key: "youtube_cookies", Label: "YouTube cookies (Netscape format) — optional",
			Type: "textarea", Required: false, Secret: true,
			Help: "Export cookies while logged into YouTube using a browser extension (e.g. \"Get cookies.txt LOCALLY\"), then paste the entire file's contents here. This ties downloads to that Google account, and heavy automated use carries some risk of that account being flagged by YouTube.",
		},
	}}
}

func (a *Adapter) Init(cfg map[string]any) error {
	if v, ok := cfg["output_dir"].(string); ok && v != "" {
		a.outputDir = v
	}
	if a.outputDir == "" {
		return fmt.Errorf("ytdlp: output_dir is required")
	}
	a.binary = defaultBinary
	if v, ok := cfg["binary_path"].(string); ok && v != "" {
		a.binary = v
	}
	a.audioFormat, a.audioFormatSet = defaultAudioFormat, false
	if v, ok := cfg["audio_format"].(string); ok && strings.TrimSpace(v) != "" {
		a.audioFormat, a.audioFormatSet = strings.TrimSpace(v), true
	}
	a.audioQuality, a.audioQualitySet = defaultAudioQuality, false
	if v, ok := cfg["audio_quality"].(string); ok && strings.TrimSpace(v) != "" {
		a.audioQuality, a.audioQualitySet = strings.TrimSpace(v), true
	}
	a.cookiesFile = ""
	if v, ok := cfg["youtube_cookies"].(string); ok && strings.TrimSpace(v) != "" {
		path, err := writeCookiesFile(v)
		if err != nil {
			return fmt.Errorf("ytdlp: writing youtube cookies file: %w", err)
		}
		a.cookiesFile = path
	}
	if a.runner == nil {
		a.runner = ExecRunner{}
	}
	return nil
}

// TestConnection runs `<binary> --version` to confirm yt-dlp is present/runnable.
func (a *Adapter) TestConnection(ctx context.Context) error {
	if err := a.runner.Run(ctx, a.binary, []string{"--version"}, func(string) {}); err != nil {
		return fmt.Errorf("yt-dlp --version: %w", err)
	}
	return nil
}

// CanDownload is a cheap heuristic: yt-dlp can attempt anything it can turn into
// a URL or a search query. No network call.
func (a *Adapter) CanDownload(ctx context.Context, req core.DownloadRequest) (bool, error) {
	return buildQuery(req) != "", nil
}

// cookiesFilePath returns the path this adapter's cookies.txt lives at.
func cookiesFilePath() string {
	cfg, err := os.UserConfigDir()
	if err != nil || cfg == "" {
		return ""
	}
	return filepath.Join(cfg, "reverb", "ytdlp-cookies.txt")
}

// writeCookiesFile persists admin-pasted cookies.txt content so it can be handed
// to yt-dlp as a real file path. Mode 0600: this is authenticated session data.
func writeCookiesFile(content string) (string, error) {
	path := cookiesFilePath()
	if path == "" {
		return "", fmt.Errorf("could not resolve user config dir")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		return "", err
	}
	// WriteFile's mode only applies on create; chmod so a pre-existing file with
	// looser permissions is corrected on every write.
	if err := os.Chmod(path, 0o600); err != nil {
		return "", err
	}
	return path, nil
}

// normalizeURL strips playlist/radio parameters so yt-dlp fetches only the single
// video. Non-YouTube URLs are returned unchanged.
func normalizeURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	switch strings.TrimPrefix(u.Hostname(), "www.") {
	case "youtube.com", "m.youtube.com", "music.youtube.com":
		if v := u.Query().Get("v"); v != "" {
			return "https://www.youtube.com/watch?v=" + v
		}
		return raw
	case "youtu.be":
		if id := strings.TrimPrefix(u.Path, "/"); id != "" {
			return "https://www.youtube.com/watch?v=" + id
		}
		return raw
	default:
		return raw
	}
}

// buildQuery turns a request into the single yt-dlp target argument, in priority
// order: an explicit manual URL, a YouTube-sourced external ID, then a
// "ytsearch1:" text search. Returns "" when there is nothing to go on.
func buildQuery(req core.DownloadRequest) string {
	if u := strings.TrimSpace(req.ManualURL); u != "" {
		return normalizeURL(u)
	}
	if req.Source == "youtube" && req.ExternalID != "" {
		return "https://www.youtube.com/watch?v=" + req.ExternalID
	}
	terms := strings.TrimSpace(strings.TrimSpace(req.Artist) + " " + strings.TrimSpace(req.Title))
	if terms == "" {
		return ""
	}
	return "ytsearch1:" + terms
}

// sanitizeSegment makes a known artist/title safe to embed in an -o template:
// "%" would be read as an output-template placeholder and "/" as a path separator.
func sanitizeSegment(s string) string {
	r := strings.NewReplacer("%", "", "/", "-", "\\", "-")
	return strings.TrimSpace(r.Replace(s))
}

// resolveAudioArgs picks the encode settings for this request. An explicitly
// configured audio_format/audio_quality on the adapter instance wins outright —
// that is an operator overriding the whole scheme. Otherwise the request's tier
// decides, probing the source bitrate first so a tier never upscales.
func (a *Adapter) resolveAudioArgs(ctx context.Context, req core.DownloadRequest, query string) []string {
	if a.audioFormatSet || a.audioQualitySet {
		return []string{"--audio-format", a.audioFormat, "--audio-quality", a.audioQuality}
	}
	return audioArgs(req.Quality, a.probeBitrate(ctx, query))
}

// metadataLiteral prepares a known tag value for the FROM side of --parse-metadata.
// yt-dlp splits "FROM:TO" at the first UNESCAPED colon, so a colon inside the value
// (e.g. "Lullaby of the New Moon (I) : Somnias a Luna") would otherwise truncate the
// tag and shift the rest into the field name.
func metadataLiteral(s string) string {
	return strings.ReplaceAll(sanitizeSegment(s), ":", "\\:")
}

// outputTemplate builds the -o value. When artist and title are known they are
// used literally, so the file lands with a sane name instead of a YouTube title
// like "Artist - Song (Official Video) [HD]".
func (a *Adapter) outputTemplate(req core.DownloadRequest) string {
	dir := strings.TrimRight(a.outputDir, "/")
	artist, title := sanitizeSegment(req.Artist), sanitizeSegment(req.Title)
	if artist != "" && title != "" {
		return dir + "/" + artist + " - " + title + ".%(ext)s"
	}
	return dir + "/%(title)s.%(ext)s"
}

// classifyFailure turns captured yt-dlp output into a FailureClass plus a
// human-readable reason and an optional operator hint.
func classifyFailure(raw string) (class download.FailureClass, reason, hint string) {
	low := strings.ToLower(strings.TrimSpace(raw))
	switch {
	case strings.Contains(low, "429"), strings.Contains(low, "too many requests"):
		return download.ClassRateLimited,
			"YouTube is rate-limiting this server (HTTP 429 Too Many Requests)",
			"downloads will pause and resume automatically; configuring authenticated YouTube cookies in this downloader's settings reduces how often this happens"
	case strings.Contains(low, "sign in to confirm"), strings.Contains(low, "login_required"),
		strings.Contains(low, "confirm your age"):
		return download.ClassBotChallenge,
			"YouTube is requiring sign-in to confirm this request isn't a bot",
			"configure YouTube cookies in this downloader's settings to authenticate as a real browser session"
	case strings.Contains(low, "no video results"), strings.Contains(low, "unable to extract"):
		return download.ClassNoMatch, "yt-dlp found no result for this track", ""
	case strings.Contains(low, "video unavailable"), strings.Contains(low, "private video"),
		strings.Contains(low, "not available in your country"), strings.Contains(low, "removed by the uploader"):
		return download.ClassUnavailable, "the video is unavailable, private or region-locked", ""
	case strings.Contains(low, "ffmpeg") || strings.Contains(low, "ffprobe"):
		return download.ClassUnknown,
			"yt-dlp could not post-process the audio — ffmpeg appears to be missing or broken",
			"install ffmpeg and make sure it is on PATH for the Reverb process"
	case low == "":
		return download.ClassUnknown, "yt-dlp failed with no output", ""
	default:
		return download.ClassUnknown, raw, ""
	}
}

// Start shells out to yt-dlp and streams progress. On success it returns the
// output directory as the path hint — the library scan picks up the new file.
func (a *Adapter) Start(ctx context.Context, req core.DownloadRequest, onProgress func(int)) (string, error) {
	query := buildQuery(req)
	if query == "" {
		return "", download.ClassifiedError{
			Class: download.ClassNoMatch,
			Err:   fmt.Errorf("ytdlp: request has no URL and no artist/title to search for"),
		}
	}

	args := []string{
		"--no-playlist", // a watch URL carrying &list= must not pull the whole playlist
		"--newline",     // progress as discrete lines, not a redrawn bar, so onLine sees each
		"--no-warnings",
		"--extract-audio",
	}
	args = append(args, a.resolveAudioArgs(ctx, req, query)...)
	args = append(args,
		"--embed-metadata",
		"--embed-thumbnail",
	)
	if req.ForceOverwrite {
		// yt-dlp skips a target that already exists, which is exactly what a
		// quality upgrade must not do.
		args = append(args, "--force-overwrites")
	}
	if a.cookiesFile != "" {
		args = append(args, "--cookies", a.cookiesFile)
	}
	// Force the tags we already know onto the file. yt-dlp expands the FROM side
	// as an output template, and a literal with no placeholders expands to itself.
	if s := metadataLiteral(req.Artist); s != "" {
		args = append(args, "--parse-metadata", s+":%(meta_artist)s")
	}
	if s := metadataLiteral(req.Title); s != "" {
		args = append(args, "--parse-metadata", s+":%(meta_title)s")
	}
	if s := metadataLiteral(req.Album); s != "" {
		args = append(args, "--parse-metadata", s+":%(meta_album)s")
	}
	args = append(args, "--output", a.outputTemplate(req), "--", query)

	log.Printf("ytdlp: exec %s %s", a.binary, strings.Join(args, " "))

	sawProgress := false
	// A rolling window of recent output, so a failure can be classified from the
	// lines leading up to it rather than just the exit code.
	const contextLines = 12
	var recent []string
	rerr := a.runner.Run(ctx, a.binary, args, func(line string) {
		s := strings.TrimSpace(line)
		if s != "" {
			log.Printf("ytdlp> %s", s)
			recent = append(recent, s)
			if len(recent) > contextLines {
				recent = recent[1:]
			}
		}
		if m := progressRe.FindStringSubmatch(line); m != nil {
			if p, err := strconv.Atoi(m[1]); err == nil && p >= 0 && p <= 100 {
				sawProgress = true
				// Byte-level download is the first ~85% of the job; the rest is
				// extraction and tagging, reported by stageProgress below.
				onProgress(p * 85 / 100)
				return
			}
		}
		for _, st := range stageProgress {
			if st.re.MatchString(line) {
				sawProgress = true
				onProgress(st.pct)
				return
			}
		}
		// Unparseable line: ignore (graceful degradation).
	})
	if rerr != nil {
		class, reason, hint := classifyFailure(strings.Join(recent, "\n"))
		log.Printf("ytdlp: %q failed: %v", query, rerr)
		if hint != "" {
			log.Printf("ytdlp: hint — %s", hint)
		}
		return "", download.ClassifiedError{Class: class, Err: fmt.Errorf("yt-dlp download %q: %s", query, reason)}
	}
	if !sawProgress {
		onProgress(-1) // indeterminate: yt-dlp gave no parseable progress
	}
	log.Printf("ytdlp: %q finished (output_dir=%s)", query, a.outputDir)
	return a.outputDir, nil
}
