package play_test

import (
	"context"
	"testing"
	"time"

	"github.com/uhhhm/reverb/internal/catalog"
	"github.com/uhhhm/reverb/internal/play"
	"github.com/uhhhm/reverb/internal/store"
	"github.com/uhhhm/reverb/internal/store/db"
)

func TestRecommendationStatsRatesBySurfaceAndTimeRange(t *testing.T) {
	st, err := store.Open(t.TempDir() + "/recommendations.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	n := 0
	ids := func() string { n++; return string(rune('a' + n)) }
	now := func() time.Time { return time.Unix(1700000000, 0) }
	cat := catalog.NewService(st.Q(), now, ids)
	svc := play.NewService(st.Q(), cat, now, ids)
	qualified, skipped := true, false
	for _, in := range []play.PlayInput{
		{Title: "Completed", Artist: "A", Origin: "radio", PlayedAt: 1100, Completed: true, Qualified: &qualified},
		{Title: "Skipped", Artist: "B", Origin: "radio", PlayedAt: 1200, Qualified: &skipped},
		{Title: "Outside", Artist: "C", Origin: "shelf", PlayedAt: 900, Qualified: &qualified},
	} {
		if err := svc.Record(context.Background(), "u1", in); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.Q().InsertRecommendationAddIfAbsent(context.Background(), db.InsertRecommendationAddIfAbsentParams{
		ID: "add-1", UserID: "u1", Origin: "radio", Action: "playlist", CreatedAt: 1300,
	}); err != nil {
		t.Fatal(err)
	}

	got, err := play.NewStats(st.Q()).Recommendations(context.Background(), "u1", 1000, 2000)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Origin != "radio" || got[0].Plays != 2 || got[0].SkipRate != .5 || got[0].CompletionRate != .5 || got[0].AddRate != .5 {
		t.Fatalf("recommendation stats = %+v", got)
	}
}
