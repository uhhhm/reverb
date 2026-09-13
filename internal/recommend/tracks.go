package recommend

import (
	"context"
	"errors"
	"log"
	"sort"
	"sync"

	"github.com/uhhhm/reverb/internal/core"
	"github.com/uhhhm/reverb/internal/matching"
	"github.com/uhhhm/reverb/internal/search"
)

// ErrNotConfigured is returned by a source that cannot run without setup (a
// missing API key). The surfaces it feeds are hidden rather than shown empty.
var ErrNotConfigured = errors.New("recommend: source not configured")

// TrackCandidate is a similar recording before it is matched to something
// playable. MBID and duration are optional; Sources records its provenance.
type TrackCandidate struct {
	Artist     string
	Title      string
	DurationMs int
	MBID       string
	Sources    []string
}

// TrackSeed identifies the recording a recommendation lookup starts from.
// MBID is preferred when known; artist and title remain the portable fallback.
type TrackSeed struct {
	Artist string `json:"artist"`
	Title  string `json:"title"`
	MBID   string `json:"mbid,omitempty"`
}

// TrackSimilarity is a source of tracks similar to a seed, most similar first.
type TrackSimilarity interface {
	Name() string
	SimilarTracks(ctx context.Context, seed TrackSeed, limit int) ([]TrackCandidate, error)
}

const (
	// similarTrackCandidates is how many candidates are asked for: some never
	// match anything playable, so more are fetched than are shown.
	similarTrackCandidates = 30
	similarTrackLimit      = 20
	// matchWorkers bounds concurrent lookups while matching candidates, so one
	// request cannot flood a search source.
	matchWorkers = 4
)

// TrackResult is the "Similar tracks" list for a seed track. Every entry is
// playable: a library track (Source "library", ExternalID the backend id,
// CanonicalID the catalog id, Match set) or a search-source result that plays
// through external playback. Available is false when no source is configured.
type TrackResult struct {
	Available bool                  `json:"available"`
	Tracks    []core.ExternalResult `json:"tracks"`
	Offline   bool                  `json:"offline,omitempty"`
	UpdatedAt int64                 `json:"updatedAt,omitempty"`
}

// SimilarTracks returns playable tracks similar to the seed.
func (s *Service) SimilarTracks(ctx context.Context, artist, title string) TrackResult {
	return s.SimilarTracksFor(ctx, TrackSeed{Artist: artist, Title: title})
}

// SimilarTracksFor returns playable tracks similar to a seed, using its MBID
// when one is available and retaining which sources proposed each candidate.
// They are ranked by the taste profile and carry a reason. With online
// recommendations off, nothing is looked up.
func (s *Service) SimilarTracksFor(ctx context.Context, seed TrackSeed) TrackResult {
	if !s.settings(ctx).Online {
		return s.localTracks(ctx, seed)
	}
	result := s.similarTracks(ctx, seed)
	if len(result.Tracks) == 0 {
		return s.localTracksOrStale(ctx, seed, trackCacheKey(seed))
	}
	if len(result.Tracks) == 0 {
		return result
	}
	p := s.profile(ctx)
	support := map[string]float64{}
	listSupport(support, result.Tracks)
	rankTracks(result.Tracks, support, p)
	reason := trackReason(Seed{Artist: seed.Artist, Title: seed.Title}, p)
	for i := range result.Tracks {
		result.Tracks[i].Reason = reason
	}
	return result
}

func (s *Service) localTracks(ctx context.Context, seed TrackSeed) TrackResult {
	result := TrackResult{Tracks: []core.ExternalResult{}, Offline: true, UpdatedAt: s.now().Unix()}
	if s.local == nil {
		return result
	}
	tracks, err := s.local.SimilarLocalTracks(ctx, seed, similarTrackLimit)
	if err != nil {
		log.Printf("recommend: local similar tracks for %q by %q: %v", seed.Title, seed.Artist, err)
		return result
	}
	result.Available = true
	result.Tracks = s.withoutMarkedTracks(ctx, tracks)
	return result
}

func (s *Service) localTracksOrStale(ctx context.Context, seed TrackSeed, key string) TrackResult {
	local := s.localTracks(ctx, seed)
	if local.Available && len(local.Tracks) > 0 {
		return local
	}
	if cached, at, ok := s.cache.stale(key); ok {
		return TrackResult{Available: true, Tracks: s.withoutMarkedTracks(ctx, cached.([]core.ExternalResult)), Offline: true, UpdatedAt: at.Unix()}
	}
	return local
}

// similarTracks is the unranked list for one seed, in source order with
// agreement first. The slice is the caller's to modify.
func (s *Service) similarTracks(ctx context.Context, seed TrackSeed) TrackResult {
	if len(s.tracks) == 0 {
		return TrackResult{Tracks: []core.ExternalResult{}}
	}
	result := TrackResult{Available: true, Tracks: []core.ExternalResult{}}
	key := trackCacheKey(seed)
	if cached, ok := s.cache.get(key); ok {
		result.Tracks = s.withoutMarkedTracks(ctx, cached.([]core.ExternalResult))
		return result
	}

	sourceCtx, cancelSources := context.WithTimeout(ctx, s.timeout)
	cands, available := s.trackCandidates(sourceCtx, seed)
	cancelSources()
	if !available {
		return TrackResult{Tracks: []core.ExternalResult{}}
	}

	seedCandidate := TrackCandidate{Artist: seed.Artist, Title: seed.Title, MBID: seed.MBID}
	kept := cands[:0:0]
	for _, c := range cands {
		if !sameCandidate(seedCandidate, c) {
			kept = append(kept, c)
		}
	}
	matchCtx, cancelMatches := context.WithTimeout(ctx, s.timeout)
	defer cancelMatches()
	tracks := filterTracks(s.matchCandidates(matchCtx, kept), []Seed{{Artist: seed.Artist, Title: seed.Title, MBID: seed.MBID}}, similarTracksSurface, nil)
	// A lookup cut short by the deadline keeps what it matched: re-asking the
	// source on every reopen would be worse than a shorter list.
	if matchCtx.Err() == nil || len(tracks) > 0 {
		s.cache.put(key, tracks)
	}
	result.Tracks = s.withoutMarkedTracks(ctx, tracks)
	return result
}

func trackCacheKey(seed TrackSeed) string {
	if seed.MBID != "" {
		return "tracks\x1fmbid\x1f" + seed.MBID
	}
	return "tracks\x1fname\x1f" + matching.Normalize(matching.PrimaryArtist(seed.Artist)) + "\x1f" + matching.Normalize(seed.Title)
}

type trackSourceResult struct {
	candidates []TrackCandidate
	available  bool
}

// trackCandidates asks every source independently. Failures stay local to one
// result, and candidates with more source agreement rank first.
func (s *Service) trackCandidates(ctx context.Context, seed TrackSeed) ([]TrackCandidate, bool) {
	results := make([]trackSourceResult, len(s.tracks))
	var wg sync.WaitGroup
	for i, src := range s.tracks {
		wg.Add(1)
		go func() {
			defer wg.Done()
			gate := s.trackGates[src.Name()]
			if gate != nil {
				if err := gate.wait(ctx, s.now, s.sleep); err != nil {
					results[i].available = !errors.Is(err, ErrNotConfigured)
					return
				}
			}
			cands, err := src.SimilarTracks(ctx, seed, similarTrackCandidates)
			if errors.Is(err, ErrNotConfigured) {
				return
			}
			results[i].available = true
			if err != nil {
				if ctx.Err() == nil && gate != nil {
					gate.failed(s.now())
				}
				log.Printf("recommend: similar tracks from %s for %q by %q: %v", src.Name(), seed.Title, seed.Artist, err)
				return
			}
			if gate != nil {
				gate.succeeded()
			}
			for j := range cands {
				cands[j].Sources = []string{src.Name()}
			}
			results[i].candidates = cands
		}()
	}
	wg.Wait()

	available := false
	var all []TrackCandidate
	for _, result := range results {
		available = available || result.available
		all = append(all, result.candidates...)
	}
	return mergeTrackCandidates(all), available
}

func mergeTrackCandidates(in []TrackCandidate) []TrackCandidate {
	var out []TrackCandidate
	byMBID := map[string]int{}
	byName := map[string][]int{}
	for _, candidate := range in {
		idx, found := -1, false
		if candidate.MBID != "" {
			idx, found = byMBID[candidate.MBID]
		}
		name := recordingKey(candidate.Title, candidate.Artist)
		if !found {
			for _, existing := range byName[name] {
				if out[existing].MBID == "" || candidate.MBID == "" || out[existing].MBID == candidate.MBID {
					idx, found = existing, true
					break
				}
			}
		}
		if found {
			out[idx].Sources = appendUnique(out[idx].Sources, candidate.Sources...)
			if out[idx].MBID == "" {
				out[idx].MBID = candidate.MBID
				if candidate.MBID != "" {
					byMBID[candidate.MBID] = idx
				}
			}
			continue
		}
		idx = len(out)
		candidate.Sources = appendUnique(nil, candidate.Sources...)
		out = append(out, candidate)
		byName[name] = append(byName[name], idx)
		if candidate.MBID != "" {
			byMBID[candidate.MBID] = idx
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return len(out[i].Sources) > len(out[j].Sources) })
	return out
}

func appendUnique(dst []string, values ...string) []string {
	for _, value := range values {
		found := false
		for _, existing := range dst {
			if existing == value {
				found = true
				break
			}
		}
		if !found && value != "" {
			dst = append(dst, value)
		}
	}
	return dst
}

// matchCandidates turns candidates into playable tracks, keeping the source's
// order and dropping candidates nothing playable matches.
func (s *Service) matchCandidates(ctx context.Context, cands []TrackCandidate) []core.ExternalResult {
	sources := s.liveSources()
	matcher := s.liveMatcher()
	resolved := make([]*core.ExternalResult, len(cands))
	sem := make(chan struct{}, matchWorkers)
	var wg sync.WaitGroup
	for i, c := range cands {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			if r, ok := resolveCandidate(ctx, c, sources, matcher); ok {
				resolved[i] = &r
			}
		}()
	}
	wg.Wait()

	out := []core.ExternalResult{}
	seen := map[string]int{}
	var owned []string
	for _, r := range resolved {
		if r == nil {
			continue
		}
		id := r.Source + "\x00" + r.ExternalID
		if previous, ok := seen[id]; ok {
			out[previous].RecommendationSources = appendUnique(out[previous].RecommendationSources, r.RecommendationSources...)
			continue
		}
		seen[id] = len(out)
		if r.Source == "library" {
			owned = append(owned, r.ExternalID)
		}
		out = append(out, *r)
		if len(out) == similarTrackLimit {
			break
		}
	}
	if len(owned) > 0 {
		s.assignCatalogIDs(ctx, out)
	}
	return out
}

// assignCatalogIDs sets the catalog id on every library track in place.
func (s *Service) assignCatalogIDs(ctx context.Context, tracks []core.ExternalResult) {
	if s.catalogIDs == nil {
		return
	}
	var owned []string
	for _, t := range tracks {
		if t.Source == "library" {
			owned = append(owned, t.ExternalID)
		}
	}
	if len(owned) == 0 {
		return
	}
	ids := s.catalogIDs(ctx, owned)
	for i := range tracks {
		if tracks[i].Source == "library" {
			tracks[i].CanonicalID = ids[tracks[i].ExternalID]
		}
	}
}

// resolveCandidate finds what a candidate plays as: the library copy when one
// is owned, otherwise the first search result that is the same recording.
func resolveCandidate(ctx context.Context, c TrackCandidate, sources []search.SearchSource, matcher Matcher) (core.ExternalResult, bool) {
	probe := core.ExternalResult{Title: c.Title, Artist: c.Artist, DurationMs: c.DurationMs, MBID: c.MBID, RecommendationSources: append([]string(nil), c.Sources...), Type: core.EntityTrack}
	if owned, ok := ownedCopy(ctx, matcher, probe); ok {
		return owned, true
	}
	query := matching.PrimaryArtist(c.Artist) + " " + c.Title
	for _, src := range sources {
		hits, err := src.Search(ctx, query, core.EntityTrack)
		if err != nil {
			continue
		}
		for _, hit := range hits {
			if hit.Type != "" && hit.Type != core.EntityTrack {
				continue
			}
			if !candidateMatchesResult(c, hit) {
				continue
			}
			hit.RecommendationSources = append([]string(nil), c.Sources...)
			if hit.MBID == "" {
				hit.MBID = c.MBID
			}
			// The hit carries an album and duration the bare candidate lacked,
			// which can confirm a library copy the first match missed.
			if owned, ok := ownedCopy(ctx, matcher, hit); ok {
				return owned, true
			}
			return hit, true
		}
	}
	return core.ExternalResult{}, false
}

func ownedCopy(ctx context.Context, matcher Matcher, ext core.ExternalResult) (core.ExternalResult, bool) {
	if matcher == nil {
		return core.ExternalResult{}, false
	}
	m, err := matcher.Match(ctx, ext)
	if err != nil || m.Status != core.MatchInLibrary || m.LibraryTrackID == "" {
		return core.ExternalResult{}, false
	}
	return core.ExternalResult{
		Source: "library", ExternalID: m.LibraryTrackID,
		Title: ext.Title, Artist: ext.Artist, Album: ext.Album, DurationMs: ext.DurationMs,
		MBID: ext.MBID, RecommendationSources: append([]string(nil), ext.RecommendationSources...),
		CoverArtID: m.CoverArtID, Type: core.EntityTrack, Match: &m,
	}, true
}

func sameCandidate(a, b TrackCandidate) bool {
	if a.MBID != "" && b.MBID != "" {
		return a.MBID == b.MBID
	}
	return sameRecording(a, b.Title, b.Artist)
}

func candidateMatchesResult(candidate TrackCandidate, result core.ExternalResult) bool {
	if candidate.MBID != "" && result.MBID != "" {
		return candidate.MBID == result.MBID
	}
	return sameRecording(candidate, result.Title, result.Artist)
}

// sameRecording reports whether a title and artist name the candidate. The
// title must be equal after normalisation, which keeps version qualifiers, so
// a live cut or a remix is a different recording; the artist uses the
// matcher's composite-credit rules.
func sameRecording(c TrackCandidate, title, artist string) bool {
	return matching.Normalize(title) == matching.Normalize(c.Title) && matching.ArtistMatches(artist, c.Artist)
}
