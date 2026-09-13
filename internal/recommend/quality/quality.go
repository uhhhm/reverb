// Package quality evaluates recommendation output against a hidden tail of
// play history. It uses recorded similarity responses so repeated runs are
// deterministic and ranking changes can be compared to a checked-in baseline.
package quality

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/uhhhm/reverb/internal/core"
	"github.com/uhhhm/reverb/internal/matching"
	"github.com/uhhhm/reverb/internal/recommend"
	"github.com/uhhhm/reverb/internal/registry"
	"github.com/uhhhm/reverb/internal/search"
)

const secondsPerDay = int64(24 * 60 * 60)

// Play is the minimum history identity required by the evaluator.
type Play struct {
	CatalogID string `json:"catalogId,omitempty"`
	Artist    string `json:"artist"`
	Title     string `json:"title"`
	MBID      string `json:"mbid,omitempty"`
	PlayedAt  int64  `json:"playedAt"`
}

// RecordedLookup is one cached similarity response for a seed.
type RecordedLookup struct {
	Seed       recommend.TrackSeed        `json:"seed"`
	Candidates []recommend.TrackCandidate `json:"candidates"`
}

// RecordedSource groups deterministic responses from one network source.
type RecordedSource struct {
	Name    string           `json:"name"`
	Lookups []RecordedLookup `json:"lookups"`
}

// Fixture combines anonymised history and recorded network responses.
type Fixture struct {
	Name        string           `json:"name"`
	HoldoutDays int              `json:"holdoutDays"`
	K           int              `json:"k"`
	Plays       []Play           `json:"plays"`
	Sources     []RecordedSource `json:"sources"`
}

// SurfaceMetrics measures one recommendation surface against the hidden plays.
type SurfaceMetrics struct {
	Predictions int     `json:"predictions"`
	Hits        int     `json:"hits"`
	HitRate     float64 `json:"hitRate"`
	RecallAtK   float64 `json:"recallAtK"`
	NDCGAtK     float64 `json:"ndcgAtK"`
}

// Report is stable JSON suitable for checking in as a ranking baseline.
type Report struct {
	Fixture       string                    `json:"fixture"`
	HoldoutDays   int                       `json:"holdoutDays"`
	K             int                       `json:"k"`
	TrainingPlays int                       `json:"trainingPlays"`
	HiddenPlays   int                       `json:"hiddenPlays"`
	Surfaces      map[string]SurfaceMetrics `json:"surfaces"`
}

// Evaluate hides the most recent period, generates both current recommendation
// surfaces from the remaining history, ranked by a taste profile built from
// that history, and scores their top-k results.
func Evaluate(ctx context.Context, fixture Fixture) (Report, error) {
	if fixture.HoldoutDays <= 0 {
		return Report{}, errors.New("quality: holdoutDays must be positive")
	}
	if fixture.K <= 0 {
		return Report{}, errors.New("quality: k must be positive")
	}
	training, hidden := splitHistory(fixture.Plays, fixture.HoldoutDays)
	if len(training) == 0 || len(hidden) == 0 {
		return Report{}, errors.New("quality: history must contain plays on both sides of the holdout boundary")
	}

	catalog := newReplayCatalog(fixture.Sources)
	options := []recommend.Option{
		recommend.WithTimeout(time.Second),
		recommend.WithClock(func() time.Time { return time.Unix(1_700_000_000, 0) }),
		recommend.WithSleep(func(context.Context, time.Duration) error { return nil }),
		recommend.WithTaste(historyTaste(training)),
	}
	for i := range fixture.Sources {
		options = append(options, recommend.WithTrackSource(replaySource{source: &fixture.Sources[i]}))
	}
	service := recommend.New(func() []search.SearchSource { return []search.SearchSource{catalog} }, options...)

	latest := training[len(training)-1]
	similar := service.SimilarTracksFor(ctx, trackSeed(latest)).Tracks
	radio := service.Radio(ctx, radioSeeds(training)).Tracks
	return Report{
		Fixture: fixture.Name, HoldoutDays: fixture.HoldoutDays, K: fixture.K,
		TrainingPlays: len(training), HiddenPlays: len(hidden),
		Surfaces: map[string]SurfaceMetrics{
			"radio":          metrics(radio, hidden, fixture.K),
			"similar_tracks": metrics(similar, hidden, fixture.K),
		},
	}, nil
}

// RecordSource captures the source responses used by the evaluator so a real
// database run can be replayed without network access.
func RecordSource(ctx context.Context, fixture Fixture, source recommend.TrackSimilarity) (Fixture, error) {
	training, hidden := splitHistory(fixture.Plays, fixture.HoldoutDays)
	if len(training) == 0 || len(hidden) == 0 {
		return Fixture{}, errors.New("quality: history must contain plays on both sides of the holdout boundary")
	}
	seeds := evaluationSeeds(training)
	recorded := RecordedSource{Name: source.Name(), Lookups: make([]RecordedLookup, 0, len(seeds))}
	for _, seed := range seeds {
		candidates, err := source.SimilarTracks(ctx, seed, 30)
		if err != nil {
			return Fixture{}, fmt.Errorf("record %s for %q by %q: %w", source.Name(), seed.Title, seed.Artist, err)
		}
		recorded.Lookups = append(recorded.Lookups, RecordedLookup{Seed: seed, Candidates: candidates})
	}
	kept := fixture.Sources[:0:0]
	for _, existing := range fixture.Sources {
		if existing.Name != source.Name() {
			kept = append(kept, existing)
		}
	}
	fixture.Sources = append(kept, recorded)
	return fixture, nil
}

func splitHistory(plays []Play, holdoutDays int) ([]Play, []Play) {
	ordered := append([]Play(nil), plays...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].PlayedAt < ordered[j].PlayedAt })
	if len(ordered) == 0 {
		return nil, nil
	}
	cutoff := ordered[len(ordered)-1].PlayedAt - int64(holdoutDays)*secondsPerDay
	index := sort.Search(len(ordered), func(i int) bool { return ordered[i].PlayedAt >= cutoff })
	return ordered[:index], ordered[index:]
}

func trackSeed(play Play) recommend.TrackSeed {
	return recommend.TrackSeed{Artist: play.Artist, Title: play.Title, MBID: play.MBID}
}

func radioSeeds(training []Play) []recommend.Seed {
	trackSeeds := evaluationSeeds(training)
	seeds := make([]recommend.Seed, 0, len(trackSeeds))
	for _, seed := range trackSeeds {
		seeds = append(seeds, recommend.Seed{Artist: seed.Artist, Title: seed.Title, MBID: seed.MBID})
	}
	return seeds
}

func evaluationSeeds(training []Play) []recommend.TrackSeed {
	plays := make([]Play, 0, 5)
	for i := len(training) - 1; i >= 0 && len(plays) < 5; i-- {
		if containsPlay(plays, training[i]) {
			continue
		}
		plays = append(plays, training[i])
	}
	seeds := make([]recommend.TrackSeed, 0, len(plays))
	for _, play := range plays {
		seeds = append(seeds, trackSeed(play))
	}
	return seeds
}

func metrics(predictions []core.ExternalResult, hidden []Play, k int) SurfaceMetrics {
	if len(predictions) > k {
		predictions = predictions[:k]
	}
	hiddenUnique := make([]Play, 0, len(hidden))
	hits := 0
	for _, play := range hidden {
		if !containsPlay(hiddenUnique, play) {
			hiddenUnique = append(hiddenUnique, play)
		}
		if anyResultMatchesPlay(predictions, play) {
			hits++
		}
	}
	uniqueHits := 0
	for _, play := range hiddenUnique {
		if anyResultMatchesPlay(predictions, play) {
			uniqueHits++
		}
	}
	dcg := 0.0
	seenRelevant := make([]bool, len(hiddenUnique))
	for i, prediction := range predictions {
		for hiddenIndex, play := range hiddenUnique {
			if !seenRelevant[hiddenIndex] && resultMatchesPlay(prediction, play) {
				dcg += 1 / math.Log2(float64(i)+2)
				seenRelevant[hiddenIndex] = true
				break
			}
		}
	}
	idealCount := min(k, len(hiddenUnique))
	idcg := 0.0
	for i := range idealCount {
		idcg += 1 / math.Log2(float64(i)+2)
	}
	return SurfaceMetrics{
		Predictions: len(predictions), Hits: hits,
		HitRate: fraction(hits, len(hidden)), RecallAtK: fraction(uniqueHits, len(hiddenUnique)),
		NDCGAtK: fractionFloat(dcg, idcg),
	}
}

func anyResultMatchesPlay(results []core.ExternalResult, play Play) bool {
	for _, result := range results {
		if resultMatchesPlay(result, play) {
			return true
		}
	}
	return false
}

func containsPlay(plays []Play, candidate Play) bool {
	for _, play := range plays {
		if identitiesMatch(play.Artist, play.Title, play.MBID, candidate.Artist, candidate.Title, candidate.MBID) {
			return true
		}
	}
	return false
}

func resultMatchesPlay(result core.ExternalResult, play Play) bool {
	return identitiesMatch(result.Artist, result.Title, result.MBID, play.Artist, play.Title, play.MBID)
}

func identitiesMatch(artistA, titleA, mbidA, artistB, titleB, mbidB string) bool {
	if mbidA != "" && mbidB != "" {
		return mbidA == mbidB
	}
	return nameKey(artistA, titleA) == nameKey(artistB, titleB)
}

func fraction(numerator, denominator int) float64 {
	if denominator == 0 {
		return 0
	}
	return float64(numerator) / float64(denominator)
}

func fractionFloat(numerator, denominator float64) float64 {
	if denominator == 0 {
		return 0
	}
	return numerator / denominator
}

func playKey(play Play) string {
	if play.MBID != "" {
		return "mbid:" + play.MBID
	}
	return nameKey(play.Artist, play.Title)
}

func resultKey(result core.ExternalResult) string {
	if result.MBID != "" {
		return "mbid:" + result.MBID
	}
	return nameKey(result.Artist, result.Title)
}

func nameKey(artist, title string) string {
	return "name:" + matching.Normalize(matching.PrimaryArtist(artist)) + "\x1f" + matching.Normalize(title)
}

// historyTaste feeds the training plays to the taste profile. The fixture
// keeps only what the evaluator needs, so every play counts as completed.
type historyTaste []Play

func (h historyTaste) PlaysAfter(_ context.Context, after int64, limit int) ([]recommend.TastePlay, error) {
	var out []recommend.TastePlay
	for i := int(after); i < len(h) && len(out) < limit; i++ {
		out = append(out, recommend.TastePlay{Seq: int64(i + 1), Artist: h[i].Artist, Title: h[i].Title, PlayedAt: h[i].PlayedAt, Completed: true})
	}
	return out, nil
}
func (h historyTaste) PlayCount(context.Context) (int64, error) { return int64(len(h)), nil }
func (historyTaste) Signals(context.Context) ([]recommend.TasteSignal, error) {
	return nil, nil
}

type replaySource struct{ source *RecordedSource }

func (s replaySource) Name() string { return s.source.Name }
func (s replaySource) SimilarTracks(_ context.Context, seed recommend.TrackSeed, limit int) ([]recommend.TrackCandidate, error) {
	for _, lookup := range s.source.Lookups {
		if seedsMatch(seed, lookup.Seed) {
			candidates := append([]recommend.TrackCandidate(nil), lookup.Candidates...)
			if limit > 0 && len(candidates) > limit {
				candidates = candidates[:limit]
			}
			return candidates, nil
		}
	}
	return []recommend.TrackCandidate{}, nil
}

func seedsMatch(a, b recommend.TrackSeed) bool {
	if a.MBID != "" && b.MBID != "" {
		return a.MBID == b.MBID
	}
	return nameKey(a.Artist, a.Title) == nameKey(b.Artist, b.Title)
}

type replayCatalog struct {
	tracks []core.ExternalResult
}

func newReplayCatalog(sources []RecordedSource) *replayCatalog {
	byKey := map[string]core.ExternalResult{}
	for _, source := range sources {
		for _, lookup := range source.Lookups {
			for _, candidate := range lookup.Candidates {
				result := core.ExternalResult{
					Source: "quality-fixture", ExternalID: candidate.MBID,
					Title: candidate.Title, Artist: candidate.Artist, MBID: candidate.MBID,
					Type: core.EntityTrack,
				}
				if result.ExternalID == "" {
					result.ExternalID = strings.TrimPrefix(nameKey(result.Artist, result.Title), "name:")
				}
				byKey[resultKey(result)] = result
			}
		}
	}
	keys := make([]string, 0, len(byKey))
	for key := range byKey {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	tracks := make([]core.ExternalResult, 0, len(keys))
	for _, key := range keys {
		tracks = append(tracks, byKey[key])
	}
	return &replayCatalog{tracks: tracks}
}

func (*replayCatalog) Type() string                         { return "search" }
func (*replayCatalog) Name() string                         { return "quality-fixture" }
func (*replayCatalog) ConfigSchema() registry.ConfigSchema  { return registry.ConfigSchema{} }
func (*replayCatalog) Init(map[string]any) error            { return nil }
func (*replayCatalog) TestConnection(context.Context) error { return nil }
func (c *replayCatalog) Search(_ context.Context, query string, kind core.EntityType) ([]core.ExternalResult, error) {
	if kind != core.EntityTrack {
		return nil, nil
	}
	want := matching.Normalize(query)
	var out []core.ExternalResult
	for _, track := range c.tracks {
		if strings.Contains(want, matching.Normalize(track.Title)) && strings.Contains(want, matching.Normalize(matching.PrimaryArtist(track.Artist))) {
			out = append(out, track)
		}
	}
	return out, nil
}
func (*replayCatalog) GetAlbum(context.Context, string) (core.ExternalAlbum, error) {
	return core.ExternalAlbum{}, fmt.Errorf("quality fixture has no albums")
}
