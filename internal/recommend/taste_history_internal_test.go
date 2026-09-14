package recommend

import "testing"

func TestHistoryWeightGrowsWithListensButSlowly(t *testing.T) {
	artist := func(plays int) float64 {
		tl := newTally()
		tl.addSignal(TasteSignal{Kind: SignalHistory, Artist: "A", Plays: plays})
		return tl.weight(artistKey("A"), 0)
	}
	if artist(0) != 0 || !(artist(1) < artist(2) && artist(2) < artist(200)) {
		t.Fatalf("weights 0:%v 1:%v 2:%v 200:%v, want growing with listens", artist(0), artist(1), artist(2), artist(200))
	}
	// A thousand listens of one artist must not drown out everything else,
	// nor outweigh a few completed plays in Reverb.
	if artist(1000) > 10*artist(1) || artist(1000) > 3 {
		t.Fatalf("1000 listens weigh %v (one listen %v), want at most ten times and under three plays", artist(1000), artist(1))
	}

	tl := newTally()
	tl.addSignal(TasteSignal{Kind: SignalHistory, Artist: "A", Title: "T", Plays: 50})
	if tl.weight(trackKey("T", "A"), 0) <= 0 || tl.weight(artistKey("A"), 0) != 0 {
		t.Fatal("a track's history should weigh the track and leave its artist to artist rows")
	}
}
