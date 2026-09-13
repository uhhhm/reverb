package recommend

import (
	"sort"

	"github.com/uhhhm/reverb/internal/core"
)

// features describe one candidate for ranking. Each is on a small fixed
// scale, so rankWeights reads directly as how much each one matters.
type features struct {
	// support is how strongly the seeds propose the candidate: supportAt its
	// rank, summed over every seed's list it appears in. No open source
	// reports popularity, so the rank a source gives stands in for it, and
	// appearing for several seeds counts as agreement between them.
	support float64
	// agreement is how many similarity sources beyond the first proposed it.
	agreement float64
	// trackTaste and artistTaste are the profile's affinity, from -1 to 1.
	trackTaste  float64
	artistTaste float64
	// familiar is 1 when the household has played the artist.
	familiar float64
	// novel is 1 when the track is neither owned nor played.
	novel float64
}

// rankWeights is the whole ranking model: a candidate scores the weighted sum
// of its features. The profile is exact (see tally), so devices holding the
// same inputs compute the same scores and rank identically (ADR 0001).
var rankWeights = features{support: 1, agreement: 0.5, trackTaste: 1.5, artistTaste: 1, familiar: 0.25, novel: 0.1}

func (f features) score() float64 {
	w := rankWeights
	return w.support*f.support + w.agreement*f.agreement +
		w.trackTaste*f.trackTaste + w.artistTaste*f.artistTaste +
		w.familiar*f.familiar + w.novel*f.novel
}

// supportAt is the support a seed's list gives the candidate at pos.
func supportAt(pos int) float64 { return 1 / float64(1+pos) }

func agreement(sources []string) float64 { return float64(max(len(sources)-1, 0)) }

func artistFeatures(artist string, support float64, sources []string, p *Profile) features {
	f := features{support: support, agreement: agreement(sources), artistTaste: p.ArtistTaste(artist)}
	if p.PlayedArtist(artist) {
		f.familiar = 1
	}
	return f
}

func trackFeatures(t core.ExternalResult, support float64, p *Profile) features {
	f := artistFeatures(t.Artist, support, t.RecommendationSources, p)
	f.trackTaste = p.TrackTaste(t.Title, t.Artist)
	if !known(t, p) {
		f.novel = 1
	}
	return f
}

// known reports whether a track is already the household's: owned or played.
func known(t core.ExternalResult, p *Profile) bool {
	return t.Source == "library" || p.Played(t.Title, t.Artist)
}

// sortByScore orders items best first in place; ties keep their order.
func sortByScore[T any](items []T, score func(pos int, item T) float64) {
	type scored struct {
		item  T
		score float64
	}
	list := make([]scored, len(items))
	for i, item := range items {
		list[i] = scored{item, score(i, item)}
	}
	sort.SliceStable(list, func(i, j int) bool { return list[i].score > list[j].score })
	for i := range list {
		items[i] = list[i].item
	}
}

// rankTracks orders tracks best first in place. support is keyed by
// recording key.
func rankTracks(tracks []core.ExternalResult, support map[string]float64, p *Profile) {
	sortByScore(tracks, func(_ int, t core.ExternalResult) float64 {
		return trackFeatures(t, support[recordingKey(t.Title, t.Artist)], p).score()
	})
}

// listSupport adds the support each recording gets from one seed's list.
func listSupport(support map[string]float64, tracks []core.ExternalResult) {
	for pos, t := range tracks {
		support[recordingKey(t.Title, t.Artist)] += supportAt(pos)
	}
}

// rankArtists orders similar artists best first in place, on the same model:
// an artist has no track taste and no novelty.
func rankArtists(artists []core.ExternalArtist, p *Profile) {
	sortByScore(artists, func(pos int, a core.ExternalArtist) float64 {
		return artistFeatures(a.Name, supportAt(pos), a.RecommendationSources, p).score()
	})
}

// trackReason explains a recommendation made from seed.
func trackReason(seed Seed, p *Profile) *core.RecommendationReason {
	switch {
	case seed.Title == "":
		return &core.RecommendationReason{Kind: core.ReasonRadioArtist, Artist: seed.Artist}
	case p.Played(seed.Title, seed.Artist):
		return &core.RecommendationReason{Kind: core.ReasonPlayed, Artist: seed.Artist, Title: seed.Title}
	default:
		return &core.RecommendationReason{Kind: core.ReasonSimilar, Artist: seed.Artist, Title: seed.Title}
	}
}
