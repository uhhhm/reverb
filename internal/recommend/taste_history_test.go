package recommend_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/uhhhm/reverb/internal/core"
	"github.com/uhhhm/reverb/internal/recommend"
)

func TestImportedHistoryShapesTheProfileWithoutCountingAsPlays(t *testing.T) {
	cands := []recommend.TrackCandidate{{Artist: "Stranger", Title: "Alpha"}, {Artist: "Loved", Title: "Bravo"}}
	in := &tasteInputs{signals: []recommend.TasteSignal{
		{Kind: recommend.SignalHistory, Artist: "Loved", Plays: 40, At: 1_700_000_000},
		{Kind: recommend.SignalHistory, Artist: "Seed Artist", Title: "Seed", Plays: 12, At: 1_700_000_000},
	}}
	got := tasteService(cands, libraryMatcher{}, recommend.WithTaste(in)).SimilarTracksFor(context.Background(), seed)
	if want := []string{"Bravo", "Alpha"}; !reflect.DeepEqual(titles(got.Tracks), want) {
		t.Fatalf("got %v, want %v", titles(got.Tracks), want)
	}
	// History is not a play: the seed is still one the household has not
	// played in Reverb.
	if r := got.Tracks[0].Reason; r == nil || r.Kind != core.ReasonSimilar {
		t.Fatalf("reason = %+v, want similar", r)
	}
}
