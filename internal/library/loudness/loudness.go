// Package loudness measures how loud a track is and turns that into the gain
// needed to bring it to a common reference level.
//
// Reverb never re-encodes the file for this: the gain is applied at playback
// time by the player's Web Audio graph, so a measurement is a fact about the
// file that can be cached and re-used, and turning normalization off is
// instant and lossless.
package loudness

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"os/exec"
	"strconv"
	"strings"
)

// TargetLUFS is the reference level tracks are normalized to. -14 LUFS is the
// de facto streaming reference (Spotify, YouTube, Tidal all sit at or near it),
// so a library normalized to it sounds level against everything else.
const TargetLUFS = -14.0

// MaxGainDB bounds the correction. A very quiet or mis-measured file would
// otherwise be pushed up far enough to clip badly on the loud passages.
const MaxGainDB = 12.0

var ErrUnavailable = errors.New("loudness: ffmpeg not available")

// ffmpegSummary is the tail of loudnorm's print_format=json output.
type ffmpegSummary struct {
	InputI string `json:"input_i"`
}

// Measure runs loudnorm's analysis pass and returns the gain in dB that brings
// the file to TargetLUFS. A silent file (loudnorm reports -inf or a very low
// level) yields 0: there is nothing to normalize, and amplifying it would only
// raise the noise floor.
func Measure(ctx context.Context, ffmpegPath, path string) (float64, error) {
	if ffmpegPath == "" {
		ffmpegPath = "ffmpeg"
	}
	if _, err := exec.LookPath(ffmpegPath); err != nil {
		return 0, ErrUnavailable
	}
	cmd := exec.CommandContext(ctx, ffmpegPath,
		"-nostdin", "-hide_banner",
		"-i", path,
		"-af", "loudnorm=print_format=json",
		"-f", "null", "-",
	)
	// loudnorm prints its JSON summary on stderr, alongside ffmpeg's own log.
	out, err := cmd.CombinedOutput()
	if err != nil {
		return 0, err
	}
	measured, err := parseIntegratedLUFS(string(out))
	if err != nil {
		return 0, err
	}
	return GainFor(measured), nil
}

// GainFor converts a measured integrated loudness into a clamped gain in dB.
func GainFor(measuredLUFS float64) float64 {
	if math.IsInf(measuredLUFS, 0) || math.IsNaN(measuredLUFS) || measuredLUFS <= -70 {
		return 0
	}
	gain := TargetLUFS - measuredLUFS
	return math.Max(-MaxGainDB, math.Min(MaxGainDB, gain))
}

// parseIntegratedLUFS pulls input_i out of the last JSON object ffmpeg printed.
func parseIntegratedLUFS(out string) (float64, error) {
	start := strings.LastIndex(out, "{")
	end := strings.LastIndex(out, "}")
	if start < 0 || end <= start {
		return 0, errors.New("loudness: no loudnorm summary in ffmpeg output")
	}
	var summary ffmpegSummary
	if err := json.Unmarshal([]byte(out[start:end+1]), &summary); err != nil {
		return 0, err
	}
	v := strings.TrimSpace(summary.InputI)
	if v == "" {
		return 0, errors.New("loudness: loudnorm reported no integrated loudness")
	}
	if strings.EqualFold(v, "-inf") {
		return math.Inf(-1), nil
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return 0, err
	}
	return f, nil
}
