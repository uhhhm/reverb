package ytdlp

import (
	"context"
	"strings"
	"testing"

	"github.com/uhhhm/reverb/internal/core"
)

// A tier is a ceiling: a source at or below it is kept as-is rather than
// re-encoded upward, which would only inflate the file.
func TestAudioArgsNeverUpscales(t *testing.T) {
	cases := []struct {
		name       string
		quality    core.AudioQuality
		sourceKbps int
		want       string
	}{
		{"youtube opus under high ceiling is kept", core.QualityHigh, 130, "--audio-format best"},
		{"source exactly at ceiling is kept", core.QualityHigh, 320, "--audio-format best"},
		{"source above ceiling is transcoded down", core.QualityLow, 130, "--audio-format mp3 --audio-quality 128K"},
		{"medium ceiling below source", core.QualityMedium, 256, "--audio-format mp3 --audio-quality 192K"},
		{"best never re-encodes", core.QualityBest, 999, "--audio-format best"},
		{"unknown source falls back to the tier", core.QualityHigh, 0, "--audio-format mp3 --audio-quality 320K"},
	}
	for _, c := range cases {
		got := strings.Join(audioArgs(c.quality, c.sourceKbps), " ")
		if got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestProbeBitrateParsesFloatAndNA(t *testing.T) {
	a := newAdapter(t, &fakeRunner{lines: []string{"129.478"}}, nil)
	if got := a.probeBitrate(context.Background(), "x"); got != 129 {
		t.Errorf("float abr: got %d", got)
	}
	a = newAdapter(t, &fakeRunner{lines: []string{"NA"}}, nil)
	if got := a.probeBitrate(context.Background(), "x"); got != 0 {
		t.Errorf("NA must read as unknown: got %d", got)
	}
}

// An operator who pins audio_format/audio_quality on the instance keeps control.
func TestExplicitAdapterConfigOverridesTier(t *testing.T) {
	a := newAdapter(t, &fakeRunner{}, map[string]any{"output_dir": "/music", "audio_quality": "5"})
	got := strings.Join(a.resolveAudioArgs(context.Background(), core.DownloadRequest{Quality: core.QualityBest}, "x"), " ")
	if got != "--audio-format mp3 --audio-quality 5" {
		t.Errorf("got %q", got)
	}
}

func TestForceOverwriteAddsFlag(t *testing.T) {
	r := &fakeRunner{}
	a := newAdapter(t, r, nil)
	if _, err := a.Start(context.Background(), core.DownloadRequest{
		Artist: "A", Title: "T", ManualURL: "https://www.youtube.com/watch?v=abc",
		Quality: core.QualityHigh, ForceOverwrite: true,
	}, func(int) {}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.argString(), "--force-overwrites") {
		t.Errorf("upgrade must overwrite: %s", r.argString())
	}
}
