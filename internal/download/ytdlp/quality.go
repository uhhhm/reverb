package ytdlp

import (
	"context"
	"strconv"
	"strings"

	"github.com/maxjb-xyz/reverb/internal/core"
)

// probeBitrate asks yt-dlp for the source's average audio bitrate without
// downloading anything. Returns 0 when unknown (offline, blocked, no abr field),
// which callers treat as "just use the tier".
func (a *Adapter) probeBitrate(ctx context.Context, query string) int {
	args := []string{"--no-playlist", "--no-warnings", "--skip-download", "--print", "%(abr)s", "--", query}
	var out []string
	if err := a.runner.Run(ctx, a.binary, args, func(line string) {
		if s := strings.TrimSpace(line); s != "" {
			out = append(out, s)
		}
	}); err != nil {
		return 0
	}
	for _, line := range out {
		// yt-dlp prints "NA" for missing fields and may print a float ("129.478").
		f, err := strconv.ParseFloat(line, 64)
		if err != nil || f <= 0 {
			continue
		}
		return int(f)
	}
	return 0
}

// audioArgs picks the --audio-format/--audio-quality pair for a tier, given the
// source's bitrate (0 when unknown).
//
// The tier is a ceiling, not a target. YouTube serves roughly 130-160 kbps Opus,
// so re-encoding that to 320 kbps mp3 would only make the file bigger. When the
// source already sits at or below the ceiling there is nothing to gain by
// transcoding, so the source stream is kept as-is; only a source ABOVE the
// ceiling is transcoded down to it.
func audioArgs(q core.AudioQuality, sourceKbps int) []string {
	ceiling := q.KbpsCeiling()
	if ceiling == 0 {
		// QualityBest: keep whatever the source served, no re-encode.
		return []string{"--audio-format", "best"}
	}
	if sourceKbps > 0 && sourceKbps <= ceiling {
		return []string{"--audio-format", "best"}
	}
	return []string{"--audio-format", "mp3", "--audio-quality", strconv.Itoa(ceiling) + "K"}
}
