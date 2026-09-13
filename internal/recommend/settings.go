package recommend

import (
	"context"
	"log"
	"math"

	"github.com/uhhhm/reverb/internal/core"
)

// DefaultAdventurousness leaves every surface at its own balance of new and
// known music.
const DefaultAdventurousness = 50

// Settings are the household's recommendation settings. They replicate with
// the rest of the taste profile's inputs.
type Settings struct {
	// Adventurousness, 0 to 100, shifts every surface's share of new music:
	// 0 is only known music, 100 only new, and 50 each surface's default.
	Adventurousness int
	// Online allows similarity lookups against Last.fm, ListenBrainz and
	// Deezer. Off, nothing is looked up and the surfaces that need online
	// data come back unavailable.
	Online bool
}

// WithSettings reads the household's settings per request, so a change on
// any device applies to the next recommendation.
func WithSettings(load func(ctx context.Context) (Settings, error)) Option {
	return func(s *Service) { s.loadSettings = load }
}

// settings returns the current settings. A failed read turns online lookups
// off: the owner may have switched them off, and sending seeds out against
// that is worse than a missing section.
func (s *Service) settings(ctx context.Context) Settings {
	if s.loadSettings == nil {
		return Settings{Adventurousness: DefaultAdventurousness, Online: true}
	}
	st, err := s.loadSettings(ctx)
	if err != nil {
		log.Printf("recommend: reading settings: %v", err)
		return Settings{Adventurousness: DefaultAdventurousness}
	}
	return st
}

// newShare is the share of new music a surface aims for: its default at
// adventurousness 50, moving linearly to 0 at 0 and to 1 at 100.
func newShare(surfaceDefault float64, adventurousness int) float64 {
	a := float64(min(max(adventurousness, 0), 100))
	if a >= 50 {
		return surfaceDefault + (1-surfaceDefault)*(a-50)/50
	}
	return surfaceDefault * a / 50
}

// mixNewAndKnown reorders ranked tracks so that every prefix holds new and
// known tracks in about the given share (within one track), taking the
// better-ranked track whenever either kind would do. At a share of 0 or 1 the
// other kind is left out entirely; in between, when one kind runs out, the
// other fills the rest.
func mixNewAndKnown(ranked []core.ExternalResult, share float64, p *Profile) []core.ExternalResult {
	var fresh, familiar []int
	for i, t := range ranked {
		if known(t, p) {
			familiar = append(familiar, i)
		} else {
			fresh = append(fresh, i)
		}
	}
	pick := func(idx []int) []core.ExternalResult {
		out := make([]core.ExternalResult, len(idx))
		for i, j := range idx {
			out[i] = ranked[j]
		}
		return out
	}
	switch {
	case share <= 0:
		return pick(familiar)
	case share >= 1:
		return pick(fresh)
	}
	out := make([]core.ExternalResult, 0, len(ranked))
	newCount := 0
	for len(fresh)+len(familiar) > 0 {
		target := share * float64(len(out)+1)
		newOK := len(fresh) > 0 && math.Abs(float64(newCount+1)-target) < 1
		knownOK := len(familiar) > 0 && math.Abs(float64(newCount)-target) < 1
		takeNew := len(familiar) == 0
		switch {
		case newOK && knownOK:
			takeNew = fresh[0] < familiar[0]
		case newOK:
			takeNew = true
		case knownOK:
			takeNew = false
		}
		if takeNew {
			out = append(out, ranked[fresh[0]])
			fresh = fresh[1:]
			newCount++
		} else {
			out = append(out, ranked[familiar[0]])
			familiar = familiar[1:]
		}
	}
	return out
}
