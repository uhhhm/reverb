package recommend

import (
	"math"
	"sort"

	"github.com/uhhhm/reverb/internal/core"
)

// features describe one candidate for ranking. Each is on a small fixed
// scale, so rankWeights reads directly as how much each one matters.
type features struct {
	// support is how strongly the seeds propose the candidate: 1/(1+rank)
	// summed over every seed's list it appears in. No open source reports
	// popularity, so the rank a source gives stands in for it, and appearing
	// for several seeds counts as agreement between them.
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
// of its features.
var rankWeights = features{support: 1, agreement: 0.5, trackTaste: 1.5, artistTaste: 1, familiar: 0.25, novel: 0.1}

func (f features) score() float64 {
	w := rankWeights
	s := w.support*f.support + w.agreement*f.agreement +
		w.trackTaste*f.trackTaste + w.artistTaste*f.artistTaste +
		w.familiar*f.familiar + w.novel*f.novel
	// Rounded so that float summation order, which differs between devices
	// that stored the same plays in another order, cannot break a tie
	// differently (ADR 0001).
	return math.Round(s*1e9) / 1e9
}

func trackFeatures(t core.ExternalResult, support float64, p *Profile) features {
	f := features{
		support:     support,
		agreement:   float64(max(len(t.RecommendationSources)-1, 0)),
		trackTaste:  p.TrackTaste(t.Title, t.Artist),
		artistTaste: p.ArtistTaste(t.Artist),
	}
	if p.PlayedArtist(t.Artist) {
		f.familiar = 1
	}
	if !known(t, p) {
		f.novel = 1
	}
	return f
}

// known reports whether a track is already the household's: owned or played.
func known(t core.ExternalResult, p *Profile) bool {
	return t.Source == "library" || p.Played(t.Title, t.Artist)
}

// rankTracks orders tracks best first in place. support is keyed by
// recording key; ties keep their order.
func rankTracks(tracks []core.ExternalResult, support map[string]float64, p *Profile) {
	type scored struct {
		t     core.ExternalResult
		score float64
	}
	list := make([]scored, len(tracks))
	for i, t := range tracks {
		list[i] = scored{t, trackFeatures(t, support[recordingKey(t.Title, t.Artist)], p).score()}
	}
	sort.SliceStable(list, func(i, j int) bool { return list[i].score > list[j].score })
	for i := range list {
		tracks[i] = list[i].t
	}
}

// listSupport is the support each recording gets from one seed's list.
func listSupport(support map[string]float64, tracks []core.ExternalResult) {
	for pos, t := range tracks {
		support[recordingKey(t.Title, t.Artist)] += 1 / float64(1+pos)
	}
}

// rankArtists orders similar artists best first in place, on the same model:
// an artist has no track taste and is never novel in a way that matters here.
func rankArtists(artists []core.ExternalArtist, p *Profile) {
	type scored struct {
		a     core.ExternalArtist
		score float64
	}
	list := make([]scored, len(artists))
	for pos, a := range artists {
		f := features{
			support:     1 / float64(1+pos),
			agreement:   float64(max(len(a.RecommendationSources)-1, 0)),
			artistTaste: p.ArtistTaste(a.Name),
		}
		if p.PlayedArtist(a.Name) {
			f.familiar = 1
		}
		list[pos] = scored{a, f.score()}
	}
	sort.SliceStable(list, func(i, j int) bool { return list[i].score > list[j].score })
	for i := range list {
		artists[i] = list[i].a
	}
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
