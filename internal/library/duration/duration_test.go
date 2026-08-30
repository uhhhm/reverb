package duration

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// ffmpegOrSkip keeps the suite runnable on a machine without ffmpeg; the
// measurement is a decode, so there is nothing to assert without one.
func ffmpegOrSkip(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
}

// tone writes a WAV of exactly the given length.
func tone(t *testing.T, seconds string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "tone.wav")
	cmd := exec.Command("ffmpeg", "-v", "error", "-f", "lavfi",
		"-i", "sine=frequency=440:duration="+seconds, path)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generate tone: %v\n%s", err, out)
	}
	return path
}

func TestMeasureReturnsTheDecodedLength(t *testing.T) {
	ffmpegOrSkip(t)
	got, err := Measure(context.Background(), "ffmpeg", tone(t, "2.5"))
	if err != nil {
		t.Fatal(err)
	}
	if got < 2495 || got > 2505 {
		t.Fatalf("duration = %d ms, want ~2500", got)
	}
}

// The point of decoding: a header that disagrees with the audio loses.
func TestMeasureIgnoresAMisleadingHeader(t *testing.T) {
	ffmpegOrSkip(t)
	src := tone(t, "3")
	// Copying only the first second of the stream leaves the WAV header
	// advertising the original three.
	truncated := filepath.Join(t.TempDir(), "truncated.wav")
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	// 44-byte canonical WAV header + 1s of 44.1 kHz 16-bit mono.
	if err := os.WriteFile(truncated, data[:44+44100*2], 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Measure(context.Background(), "ffmpeg", truncated)
	if err != nil {
		t.Fatal(err)
	}
	if got < 950 || got > 1050 {
		t.Fatalf("duration = %d ms, want ~1000 (the audio, not the header)", got)
	}
}

func TestMeasureReportsUnavailableFFmpeg(t *testing.T) {
	_, err := Measure(context.Background(), "definitely-not-ffmpeg", "a.mp3")
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
}

func TestMeasureFailsOnAnUnreadableFile(t *testing.T) {
	ffmpegOrSkip(t)
	if _, err := Measure(context.Background(), "ffmpeg", filepath.Join(t.TempDir(), "missing.mp3")); err == nil {
		t.Fatal("want an error for a file that does not exist")
	}
}
