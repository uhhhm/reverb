// Package duration measures how long a track actually plays.
//
// A file's own metadata is not that answer. A tag or container header carries
// whatever the encoder wrote, which for a VBR file without a proper header is a
// guess extrapolated from the first frames, and for a truncated or re-muxed
// file is simply stale — Reverb has seen both a header claiming 6:31 over three
// minutes of audio and a tag understating a file by seconds. The player has no
// way to reconcile that with what it hears.
//
// Decoding does answer it: the number of samples that come out is the length,
// whatever any header says.
package duration

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
)

var ErrUnavailable = errors.New("duration: ffmpeg not available")

// sampleRate is the rate the file is decoded to. Nothing here needs fidelity —
// only a sample count — so the lowest rate that stays exact to the millisecond
// keeps the decode cheap.
const sampleRate = 8000

// Measure returns the playable length of the file in milliseconds, by decoding
// it and counting what comes out.
//
// This is a full decode. It costs roughly a second for a typical track, so it
// belongs behind a cache, measured once per file and re-used.
func Measure(ctx context.Context, ffmpegPath, path string) (int64, error) {
	if ffmpegPath == "" {
		ffmpegPath = "ffmpeg"
	}
	if _, err := exec.LookPath(ffmpegPath); err != nil {
		return 0, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	cmd := exec.CommandContext(ctx, ffmpegPath,
		"-v", "error", "-nostdin",
		"-i", path,
		"-ac", "1", "-ar", fmt.Sprint(sampleRate), "-f", "s16le", "-",
	)
	out, err := cmd.StdoutPipe()
	if err != nil {
		return 0, err
	}
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	// The PCM is never held: only its size matters, so it is counted as it is
	// discarded rather than buffered — a long track would otherwise be read
	// into memory in full for a single number.
	n, copyErr := io.Copy(io.Discard, out)
	if err := cmd.Wait(); err != nil {
		return 0, fmt.Errorf("decode duration: %w", err)
	}
	if copyErr != nil {
		return 0, copyErr
	}
	// 2 bytes per sample, mono.
	return n / 2 * 1000 / sampleRate, nil
}
