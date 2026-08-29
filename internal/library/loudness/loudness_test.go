package loudness

import (
	"math"
	"testing"
)

func TestParseIntegratedLUFSReadsTheLastSummary(t *testing.T) {
	// ffmpeg logs before the JSON, and the JSON is the last object printed.
	out := `Input #0, mp3, from 'a.mp3':
[Parsed_loudnorm_0 @ 0x1] 
{
	"input_i" : "-19.30",
	"input_tp" : "-1.20"
}
`
	got, err := parseIntegratedLUFS(out)
	if err != nil {
		t.Fatal(err)
	}
	if got != -19.30 {
		t.Fatalf("input_i = %v, want -19.30", got)
	}
}

func TestParseIntegratedLUFSHandlesSilence(t *testing.T) {
	got, err := parseIntegratedLUFS(`{"input_i" : "-inf"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !math.IsInf(got, -1) {
		t.Fatalf("got %v, want -inf", got)
	}
}

func TestParseIntegratedLUFSErrorsWithoutASummary(t *testing.T) {
	if _, err := parseIntegratedLUFS("ffmpeg version 7.0\n"); err == nil {
		t.Fatal("want an error when loudnorm printed nothing")
	}
}

func TestGainForBringsTracksToTheReference(t *testing.T) {
	if got := GainFor(-19); math.Abs(got-5) > 1e-9 {
		t.Fatalf("quiet track gain = %v, want +5", got)
	}
	if got := GainFor(-9); math.Abs(got+5) > 1e-9 {
		t.Fatalf("loud track gain = %v, want -5", got)
	}
}

// Amplifying silence only raises the noise floor, so it must be a no-op.
func TestGainForSilenceIsZero(t *testing.T) {
	if got := GainFor(math.Inf(-1)); got != 0 {
		t.Fatalf("silence gain = %v, want 0", got)
	}
	if got := GainFor(-90); got != 0 {
		t.Fatalf("near-silence gain = %v, want 0", got)
	}
}

// A wild measurement must not be turned into a clipping amount of boost.
func TestGainForIsClamped(t *testing.T) {
	if got := GainFor(-40); got != MaxGainDB {
		t.Fatalf("gain = %v, want clamp at %v", got, MaxGainDB)
	}
	if got := GainFor(5); got != -MaxGainDB {
		t.Fatalf("gain = %v, want clamp at %v", got, -MaxGainDB)
	}
}
