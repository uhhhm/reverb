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

const (
	similarArtistCandidates = 30
	similarArtistLimit      = 10
)

// ArtistSeed identifies the artist a similarity source should relate. MBID is
// preferred when known; Source and ExternalID let catalogue-backed providers
// use their native identity while Name is the cross-source fallback.
type ArtistSeed struct {
	Source     string
	ExternalID string
	Name       string
	MBID       string
}

// ArtistCandidate is a source's related artist before it has necessarily been
// resolved to a browseable search-source profile.
type ArtistCandidate struct {
	Source     string
	ExternalID string
	Name       string
	MBID       string
	CoverURL   string
	CoverArtID string
	Sources    []string
}

// ArtistSimilarity is a dedicated source of related artists. Search adapters
// may instead implement search.SimilarArtistsProvider.
type ArtistSimilarity interface {
	Name() string
	SimilarArtists(ctx context.Context, seed ArtistSeed, limit int) ([]ArtistCandidate, error)
}

// ArtistResult is the "Fans also like" section of an artist page. Offline
// results are derived locally or served from the last successful cache entry.
type ArtistResult struct {
	Available bool                  `json:"available"`
	Artists   []core.ExternalArtist `json:"artists"`
	Offline   bool                  `json:"offline,omitempty"`
	UpdatedAt int64                 `json:"updatedAt,omitempty"`
}

type similarArtistSource struct {
	search.SearchSource
	provider search.SimilarArtistsProvider
}

// SimilarArtists merges candidates from every capable search adapter and each
// dedicated source, then ranks them by agreement and the taste profile. With
// online recommendations off, it uses local-library similarity or stale cache.
func (s *Service) SimilarArtists(ctx context.Context, source, id string) ArtistResult {
	if !s.settings(ctx).Online {
		if source == "library" {
			return s.localArtists(ctx, s.artistSeed(ctx, source, id))
		}
		key := "artists\x1f" + source + "\x1f" + id
		if cached, at, ok := s.cache.stale(key); ok {
			return ArtistResult{Available: true, Artists: s.withoutMarkedArtists(ctx, cached.([]core.ExternalArtist)), Offline: true, UpdatedAt: at.Unix()}
		}
		return ArtistResult{Artists: []core.ExternalArtist{}, Offline: true, UpdatedAt: s.now().Unix()}
	}
	seed := s.artistSeed(ctx, source, id)
	result := s.similarArtists(ctx, source, id)
	if len(result.Artists) == 0 {
		local := s.localArtists(ctx, seed)
		if local.Available && len(local.Artists) > 0 {
			return local
		}
		key := "artists\x1f" + source + "\x1f" + id
		if cached, at, ok := s.cache.stale(key); ok {
			return ArtistResult{Available: true, Artists: s.withoutMarkedArtists(ctx, cached.([]core.ExternalArtist)), Offline: true, UpdatedAt: at.Unix()}
		}
		return local
	}
	rankArtists(result.Artists, s.profile(ctx))
	return result
}

func (s *Service) localArtists(ctx context.Context, seed ArtistSeed) ArtistResult {
	result := ArtistResult{Artists: []core.ExternalArtist{}, Offline: true, UpdatedAt: s.now().Unix()}
	if s.local == nil || seed.Name == "" {
		return result
	}
	artists, err := s.local.SimilarLocalArtists(ctx, seed, similarArtistLimit)
	if err != nil {
		log.Printf("recommend: local similar artists for %q: %v", seed.Name, err)
		return result
	}
	result.Available = true
	result.Artists = s.withoutMarkedArtists(ctx, artists)
	return result
}

func (s *Service) similarArtists(ctx context.Context, source, id string) ArtistResult {
	var capable []similarArtistSource
	for _, src := range s.liveSources() {
		if provider, ok := src.(search.SimilarArtistsProvider); ok {
			capable = append(capable, similarArtistSource{SearchSource: src, provider: provider})
		}
	}
	if len(capable) == 0 && len(s.artists) == 0 {
		return ArtistResult{Artists: []core.ExternalArtist{}}
	}
	result := ArtistResult{Available: true, Artists: []core.ExternalArtist{}}
	key := "artists\x1f" + source + "\x1f" + id
	if cached, ok := s.cache.get(key); ok {
		result.Artists = s.withoutMarkedArtists(ctx, cached.([]core.ExternalArtist))
		return result
	}

	sourceCtx, cancelSources := context.WithTimeout(ctx, s.timeout)
	seed := s.artistSeed(sourceCtx, source, id)
	candidates, available := s.artistCandidates(sourceCtx, seed, capable)
	cancelSources()
	if !available {
		return ArtistResult{Artists: []core.ExternalArtist{}}
	}
	matchCtx, cancelMatches := context.WithTimeout(ctx, s.timeout)
	defer cancelMatches()
	resolved := s.matchArtistCandidates(matchCtx, candidates)
	if len(resolved) > similarArtistLimit {
		resolved = resolved[:similarArtistLimit]
	}
	if seed.Name != "" {
		reason := &core.RecommendationReason{Kind: core.ReasonFansAlsoLike, Artist: seed.Name}
		for i := range resolved {
			resolved[i].Reason = reason
		}
	}
	// Preserve the last good cache entry when a refresh produces nothing; an
	// expired entry is still useful as the explicit offline fallback.
	if len(resolved) > 0 {
		s.cache.put(key, resolved)
	}
	result.Artists = s.withoutMarkedArtists(ctx, resolved)
	return result
}

func (s *Service) artistSeed(ctx context.Context, source, id string) ArtistSeed {
	seed := ArtistSeed{Source: source, ExternalID: id}
	if source == "library" {
		if lib := s.liveLibrary(); lib != nil {
			if artist, err := lib.GetArtist(ctx, id); err == nil {
				seed.Name = artist.Name
			}
		}
		return seed
	}
	for _, src := range s.liveSources() {
		if src.Name() != source {
			continue
		}
		if provider, ok := src.(search.ArtistProvider); ok {
			if artist, err := provider.GetArtist(ctx, id); err == nil {
				seed.Name = artist.Name
				seed.MBID = artist.MBID
			}
		}
		break
	}
	return seed
}

type artistSourceResult struct {
	candidates []ArtistCandidate
	available  bool
}

func (s *Service) artistCandidates(ctx context.Context, seed ArtistSeed, capable []similarArtistSource) ([]ArtistCandidate, bool) {
	results := make([]artistSourceResult, len(capable)+len(s.artists))
	var wg sync.WaitGroup
	for i, src := range capable {
		wg.Add(1)
		go func() {
			defer wg.Done()
			externalID := s.resolveArtistID(ctx, seed, src.SearchSource)
			if externalID == "" {
				return
			}
			results[i].available = true
			artists, err := src.provider.SimilarArtists(ctx, externalID, similarArtistCandidates)
			if err != nil {
				log.Printf("recommend: similar artists from %s for %q: %v", src.Name(), seed.Name, err)
				return
			}
			for _, artist := range artists {
				results[i].candidates = append(results[i].candidates, ArtistCandidate{
					Source: artist.Source, ExternalID: artist.ExternalID, Name: artist.Name,
					MBID: artist.MBID, CoverURL: artist.CoverURL, CoverArtID: artist.CoverArtID,
					Sources: []string{src.Name()},
				})
			}
		}()
	}
	for j, src := range s.artists {
		i := len(capable) + j
		wg.Add(1)
		go func() {
			defer wg.Done()
			artists, err := src.SimilarArtists(ctx, seed, similarArtistCandidates)
			if errors.Is(err, ErrNotConfigured) {
				return
			}
			results[i].available = true
			if err != nil {
				log.Printf("recommend: similar artists from %s for %q: %v", src.Name(), seed.Name, err)
				return
			}
			for k := range artists {
				artists[k].Sources = []string{src.Name()}
				if artists[k].Source == "" {
					artists[k].Source = src.Name()
				}
			}
			results[i].candidates = artists
		}()
	}
	wg.Wait()

	available := false
	var all []ArtistCandidate
	for _, item := range results {
		available = available || item.available
		all = append(all, item.candidates...)
	}
	return mergeArtistCandidates(all, seed), available
}

func (s *Service) resolveArtistID(ctx context.Context, seed ArtistSeed, target search.SearchSource) string {
	if seed.Source == target.Name() && seed.ExternalID != "" {
		return seed.ExternalID
	}
	if seed.Name == "" {
		return ""
	}
	want := matching.Normalize(seed.Name)
	hits, err := target.Search(ctx, seed.Name, core.EntityArtist)
	if err != nil {
		return ""
	}
	for _, hit := range hits {
		if matching.Normalize(hit.Title) == want {
			return hit.ExternalID
		}
	}
	return ""
}

func mergeArtistCandidates(in []ArtistCandidate, seed ArtistSeed) []ArtistCandidate {
	var out []ArtistCandidate
	byMBID := map[string]int{}
	byName := map[string][]int{}
	for _, candidate := range in {
		if candidate.Name == "" || seed.MBID != "" && candidate.MBID == seed.MBID || matching.Normalize(candidate.Name) == matching.Normalize(seed.Name) {
			continue
		}
		idx, found := -1, false
		if candidate.MBID != "" {
			idx, found = byMBID[candidate.MBID]
		}
		name := matching.Normalize(candidate.Name)
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
			}
			if out[idx].ExternalID == "" && candidate.ExternalID != "" {
				out[idx].Source, out[idx].ExternalID = candidate.Source, candidate.ExternalID
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

func (s *Service) matchArtistCandidates(ctx context.Context, candidates []ArtistCandidate) []core.ExternalArtist {
	sources := s.liveSources()
	out := make([]core.ExternalArtist, 0, len(candidates))
	for _, candidate := range candidates {
		artist := core.ExternalArtist{
			Source: candidate.Source, ExternalID: candidate.ExternalID, Name: candidate.Name,
			MBID: candidate.MBID, CoverURL: candidate.CoverURL, CoverArtID: candidate.CoverArtID,
			RecommendationSources: append([]string(nil), candidate.Sources...),
		}
		if !hasSearchSource(sources, artist.Source) || artist.ExternalID == "" {
			artist = resolveArtistCandidate(ctx, candidate, sources)
		}
		if artist.ExternalID != "" {
			out = append(out, artist)
		}
	}
	return out
}

func hasSearchSource(sources []search.SearchSource, name string) bool {
	for _, source := range sources {
		if source.Name() == name {
			return true
		}
	}
	return false
}

func resolveArtistCandidate(ctx context.Context, candidate ArtistCandidate, sources []search.SearchSource) core.ExternalArtist {
	want := matching.Normalize(candidate.Name)
	for _, source := range sources {
		hits, err := source.Search(ctx, candidate.Name, core.EntityArtist)
		if err != nil {
			continue
		}
		for _, hit := range hits {
			if matching.Normalize(hit.Title) != want {
				continue
			}
			mbid := candidate.MBID
			if mbid == "" {
				mbid = hit.MBID
			}
			return core.ExternalArtist{
				Source: hit.Source, ExternalID: hit.ExternalID, Name: candidate.Name,
				MBID: mbid, CoverURL: hit.CoverURL,
				RecommendationSources: append([]string(nil), candidate.Sources...),
			}
		}
	}
	return core.ExternalArtist{}
}
