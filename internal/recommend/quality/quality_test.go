package quality_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/uhhhm/reverb/internal/recommend"
	"github.com/uhhhm/reverb/internal/recommend/quality"
	"github.com/uhhhm/reverb/internal/store"
	"github.com/uhhhm/reverb/internal/store/db"
)

type recordingSource struct {
	seeds []recommend.TrackSeed
}

func (*recordingSource) Name() string { return "recorded-test" }
func (s *recordingSource) SimilarTracks(_ context.Context, seed recommend.TrackSeed, _ int) ([]recommend.TrackCandidate, error) {
	s.seeds = append(s.seeds, seed)
	return []recommend.TrackCandidate{{Artist: seed.Artist, Title: seed.Title + " Similar"}}, nil
}

func TestEvaluateHidesRecentPlaysAndReportsEverySurface(t *testing.T) {
	fixture := quality.Fixture{
		Name:        "anonymous-test",
		HoldoutDays: 2,
		K:           10,
		Plays: []quality.Play{
			{Artist: "Artist 001", Title: "Track 001", MBID: "recording-001", PlayedAt: 86400},
			{Artist: "Artist 002", Title: "Track 002", MBID: "recording-002", PlayedAt: 172800},
			{Artist: "Artist 003", Title: "Track 003", MBID: "recording-003", PlayedAt: 259200},
			{Artist: "Artist 004", Title: "Track 004", PlayedAt: 432000},
			{Artist: "Artist 005", Title: "Track 005", MBID: "recording-005", PlayedAt: 518400},
		},
		Sources: []quality.RecordedSource{
			{Name: "lastfm", Lookups: []quality.RecordedLookup{
				{Seed: recommend.TrackSeed{MBID: "recording-003"}, Candidates: []recommend.TrackCandidate{{Artist: "Artist 004", Title: "Track 004", MBID: "recording-004"}}},
			}},
		},
	}

	first, err := quality.Evaluate(context.Background(), fixture)
	if err != nil {
		t.Fatal(err)
	}
	second, err := quality.Evaluate(context.Background(), fixture)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("evaluation is not deterministic:\nfirst  %+v\nsecond %+v", first, second)
	}
	if first.TrainingPlays != 3 || first.HiddenPlays != 2 {
		t.Fatalf("split = %d training, %d hidden; want 3 and 2", first.TrainingPlays, first.HiddenPlays)
	}
	for _, surface := range []string{"radio", "similar_tracks"} {
		metrics, ok := first.Surfaces[surface]
		if !ok {
			t.Fatalf("missing %s metrics: %+v", surface, first.Surfaces)
		}
		if metrics.Hits != 1 || metrics.HitRate != 0.5 || metrics.RecallAtK != 0.5 || metrics.NDCGAtK <= 0 {
			t.Fatalf("%s metrics = %+v", surface, metrics)
		}
	}
}

func TestRecordSourceCapturesEveryEvaluationSeed(t *testing.T) {
	fixture := quality.Fixture{Name: "database", HoldoutDays: 1, K: 10}
	for i := 1; i <= 7; i++ {
		fixture.Plays = append(fixture.Plays, quality.Play{
			Artist: "Artist", Title: "Track " + string(rune('0'+i)), PlayedAt: int64(i * 86400),
		})
	}
	source := &recordingSource{}

	recorded, err := quality.RecordSource(context.Background(), fixture, source)
	if err != nil {
		t.Fatal(err)
	}
	if len(source.seeds) != 5 || len(recorded.Sources) != 1 || len(recorded.Sources[0].Lookups) != 5 {
		t.Fatalf("recorded seeds = %d calls and %+v", len(source.seeds), recorded.Sources)
	}
	if source.seeds[0].Title != "Track 5" || source.seeds[4].Title != "Track 1" {
		t.Fatalf("seed order = %+v, want newest training plays first", source.seeds)
	}
}

func TestLoadDatabaseReadsCanonicalPlayIdentity(t *testing.T) {
	path := t.TempDir() + "/reverb.db"
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := st.Q().InsertCatalogEntity(ctx, db.InsertCatalogEntityParams{
		ID: "track-1", Kind: "track", Title: "Track 001", Artist: "Artist 001",
		Mbid: "recording-001", CreatedAt: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.Q().InsertPlay(ctx, db.InsertPlayParams{
		ID: "play-1", UserID: "owner", CatalogID: "track-1", PlayedAt: 123, CreatedAt: 123,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	plays, err := quality.LoadDatabase(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	want := []quality.Play{{CatalogID: "track-1", Artist: "Artist 001", Title: "Track 001", MBID: "recording-001", PlayedAt: 123}}
	if !reflect.DeepEqual(plays, want) {
		t.Fatalf("plays = %+v, want %+v", plays, want)
	}
}

func TestDefaultAnonymisedFixtureMatchesCheckedInBaseline(t *testing.T) {
	fixture, err := quality.LoadFixture("testdata/history.json")
	if err != nil {
		t.Fatal(err)
	}
	report, err := quality.Evaluate(context.Background(), fixture)
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := quality.LoadReport("testdata/baseline.json")
	if err != nil {
		t.Fatal(err)
	}
	if regressions := quality.Compare(report, baseline); len(regressions) != 0 {
		t.Fatalf("default fixture regressed from baseline: %v", regressions)
	}
	for _, play := range fixture.Plays {
		if play.MBID == "" || play.Artist == "" || play.Title == "" {
			t.Fatalf("fixture play lacks anonymous identity: %+v", play)
		}
	}
}
