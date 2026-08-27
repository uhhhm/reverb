package ytdlp

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/uhhhm/reverb/internal/core"
	"github.com/uhhhm/reverb/internal/download"
)

var _ download.ChapterLister = (*Adapter)(nil)

// timestampRe accepts the forms a user can reasonably type into a start/end
// field: plain seconds, M:SS, or H:MM:SS, each with optional decimals.
var timestampRe = regexp.MustCompile(`^(?:(\d+):)?(?:(\d+):)?(\d+(?:\.\d+)?)$`)

// parseTimestamp converts a user-entered timestamp to seconds. It exists so a
// bad value is rejected up front with a clear message rather than being passed
// through to yt-dlp, which would fail late and cryptically.
func parseTimestamp(s string) (float64, error) {
	t := strings.TrimSpace(s)
	m := timestampRe.FindStringSubmatch(t)
	if m == nil {
		return 0, fmt.Errorf("invalid timestamp %q: use seconds, M:SS or H:MM:SS", s)
	}
	// The regex is greedy left-to-right, so for "1:30" the hour group is empty
	// and the minute group holds "1"; for "1:02:30" all three are filled.
	var h, min float64
	sec, err := strconv.ParseFloat(m[3], 64)
	if err != nil {
		return 0, fmt.Errorf("invalid timestamp %q", s)
	}
	switch {
	case m[2] != "": // H:MM:SS
		h, _ = strconv.ParseFloat(m[1], 64)
		min, _ = strconv.ParseFloat(m[2], 64)
	case m[1] != "": // M:SS
		min, _ = strconv.ParseFloat(m[1], 64)
	}
	// The under-60 rule only applies to a field that has a higher-order field
	// above it: "90" is a valid 90 seconds, but "1:90" is not a valid 1:30.
	if m[1] != "" && sec >= 60 {
		return 0, fmt.Errorf("invalid timestamp %q: seconds must be under 60", s)
	}
	if m[2] != "" && min >= 60 {
		return 0, fmt.Errorf("invalid timestamp %q: minutes must be under 60", s)
	}
	return h*3600 + min*60 + sec, nil
}

// formatSeconds renders seconds for yt-dlp's --download-sections. Whole seconds
// are emitted without a decimal point purely to keep the logged command legible.
func formatSeconds(v float64) string {
	if v == float64(int64(v)) {
		return strconv.FormatInt(int64(v), 10)
	}
	return strconv.FormatFloat(v, 'f', 3, 64)
}

// sectionArg builds the --download-sections value for a request's trim range, or
// "" when the request asks for no trimming. An empty start means "from the
// beginning" (0) and an empty end "to the end" (yt-dlp's "inf").
func sectionArg(req core.DownloadRequest) (string, error) {
	rawStart, rawEnd := strings.TrimSpace(req.SectionStart), strings.TrimSpace(req.SectionEnd)
	if rawStart == "" && rawEnd == "" {
		return "", nil
	}
	start := 0.0
	if rawStart != "" {
		v, err := parseTimestamp(rawStart)
		if err != nil {
			return "", fmt.Errorf("start time: %w", err)
		}
		start = v
	}
	end := "inf"
	if rawEnd != "" {
		v, err := parseTimestamp(rawEnd)
		if err != nil {
			return "", fmt.Errorf("end time: %w", err)
		}
		if v <= start {
			return "", fmt.Errorf("end time must be after start time")
		}
		end = formatSeconds(v)
	}
	// The "*" prefix is what tells yt-dlp this is a time range rather than a
	// chapter-title regex.
	return "*" + formatSeconds(start) + "-" + end, nil
}

// ListChapters returns the video's internal chapters without downloading it,
// using yt-dlp's --dump-json. A video with no chapters yields an empty slice,
// not an error — "this video has no chapters" is a normal answer.
func (a *Adapter) ListChapters(ctx context.Context, rawURL string) ([]core.Chapter, error) {
	query := normalizeURL(strings.TrimSpace(rawURL))
	if query == "" {
		return nil, fmt.Errorf("ytdlp: no URL to inspect")
	}
	args := []string{"--no-playlist", "--no-warnings", "--skip-download", "--dump-json"}
	if a.cookiesFile != "" {
		args = append(args, "--cookies", a.cookiesFile)
	}
	args = append(args, "--", query)

	var out strings.Builder
	if err := a.runner.Run(ctx, a.binary, args, func(line string) {
		out.WriteString(line)
		out.WriteString("\n")
	}); err != nil {
		return nil, fmt.Errorf("yt-dlp --dump-json %q: %w", query, err)
	}

	// --dump-json emits one JSON object per line, interleaved with any progress
	// or warning text, so scan for the line that actually parses.
	for _, line := range strings.Split(out.String(), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var payload struct {
			Chapters []struct {
				Title     string  `json:"title"`
				StartTime float64 `json:"start_time"`
				EndTime   float64 `json:"end_time"`
			} `json:"chapters"`
		}
		if err := json.Unmarshal([]byte(line), &payload); err != nil {
			continue
		}
		chapters := make([]core.Chapter, 0, len(payload.Chapters))
		for i, c := range payload.Chapters {
			title := strings.TrimSpace(c.Title)
			if title == "" {
				title = fmt.Sprintf("Chapter %d", i+1)
			}
			chapters = append(chapters, core.Chapter{Title: title, StartSec: c.StartTime, EndSec: c.EndTime})
		}
		return chapters, nil
	}
	return nil, fmt.Errorf("yt-dlp --dump-json %q: no JSON in output", query)
}
