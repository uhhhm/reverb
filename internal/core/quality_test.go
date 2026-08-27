package core

import "testing"

func TestAudioQualityCeilings(t *testing.T) {
	if QualityLow.KbpsCeiling() != 128 || QualityMedium.KbpsCeiling() != 192 || QualityHigh.KbpsCeiling() != 320 {
		t.Fatal("unexpected ceilings")
	}
	if QualityBest.KbpsCeiling() != 0 {
		t.Fatal("best must mean no re-encode (0)")
	}
}

func TestParseAudioQuality(t *testing.T) {
	if got := ParseAudioQuality(" HIGH ", QualityLow); got != QualityHigh {
		t.Fatalf("got %q", got)
	}
	if got := ParseAudioQuality("nonsense", QualityMedium); got != QualityMedium {
		t.Fatalf("fallback: got %q", got)
	}
	if got := ParseAudioQuality("", DefaultAudioQuality); got != QualityHigh {
		t.Fatalf("empty: got %q", got)
	}
}

func TestAudioQualityExceeds(t *testing.T) {
	if !QualityBest.Exceeds(QualityHigh) || !QualityHigh.Exceeds(QualityLow) {
		t.Fatal("ranking is wrong")
	}
	if QualityLow.Exceeds(QualityLow) || QualityMedium.Exceeds(QualityHigh) {
		t.Fatal("non-upgrades must not count as exceeding")
	}
}

func TestQualityForBitrate(t *testing.T) {
	cases := map[int]AudioQuality{0: "", 96: QualityLow, 128: QualityLow, 160: QualityMedium, 192: QualityMedium, 256: QualityHigh, 320: QualityHigh}
	for kbps, want := range cases {
		if got := QualityForBitrate(kbps); got != want {
			t.Errorf("QualityForBitrate(%d) = %q, want %q", kbps, got, want)
		}
	}
}
