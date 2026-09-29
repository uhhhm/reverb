package download

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/uhhhm/reverb/internal/core"
)

// timestampRe accepts the forms a user can reasonably type into a start/end
// field: plain seconds, M:SS, or H:MM:SS, each with optional decimals.
var timestampRe = regexp.MustCompile(`^(?:(\d+):)?(?:(\d+):)?(\d+(?:\.\d+)?)$`)

// ParseSectionTime converts a user-entered section timestamp to seconds. A
// downloader validates with it so a bad value is rejected up front with a clear
// message rather than failing late inside the tool; the manager reads a
// section's length with it.
func ParseSectionTime(s string) (float64, error) {
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

// sectioned reports whether req trims its source to a time range. A chapter
// split is one such request per chapter.
func sectioned(req core.DownloadRequest) bool {
	return strings.TrimSpace(req.SectionStart) != "" || strings.TrimSpace(req.SectionEnd) != ""
}

// sectionDurationMs is the length of req's section, or 0 when it cannot be
// known: an open end needs the source's full length (fullMs), and a malformed
// or inverted range has no length.
func sectionDurationMs(req core.DownloadRequest, fullMs int) int {
	rawStart, rawEnd := strings.TrimSpace(req.SectionStart), strings.TrimSpace(req.SectionEnd)
	start := 0.0
	if rawStart != "" {
		v, err := ParseSectionTime(rawStart)
		if err != nil {
			return 0
		}
		start = v
	}
	end := float64(fullMs) / 1000
	if rawEnd != "" {
		v, err := ParseSectionTime(rawEnd)
		if err != nil {
			return 0
		}
		end = v
	}
	if end <= start {
		return 0
	}
	return int(math.Round((end - start) * 1000))
}
