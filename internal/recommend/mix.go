package recommend

import (
	"context"
	"errors"
	"log"
	"time"

	"github.com/uhhhm/reverb/internal/core"
)

// MixKind names a Mix.
type MixKind string

const (
	// MixDiscoverWeekly refreshes Monday at local midnight with tracks that
	// are all new to the library.
	MixDiscoverWeekly MixKind = "discoverWeekly"
	// MixReleaseRadar refreshes Friday at local midnight with new releases by
	// Tracked artists.
	MixReleaseRadar MixKind = "releaseRadar"
)

// MixKinds lists every Mix, in the order Home shows them.
var MixKinds = []MixKind{MixDiscoverWeekly, MixReleaseRadar}

// Title is the Mix's name, used for a playlist saved from it.
func (k MixKind) Title() string {
	switch k {
	case MixDiscoverWeekly:
		return "Discover Weekly"
	case MixReleaseRadar:
		return "Release Radar"
	}
	return string(k)
}

// Valid reports whether k names a Mix.
func (k MixKind) Valid() bool {
	for _, kind := range MixKinds {
		if k == kind {
			return true
		}
	}
	return false
}

const (
	periodLayout = "2006-01-02"
	// scheduleTick is how often RunSchedule checks the wall clock. A timer
	// set for midnight would not fire on time after the device sleeps.
	scheduleTick = 15 * time.Minute

	// mixSize is how many tracks a Mix shows.
	mixSize = 30
	// discoverWeeklyStored leaves spares for Not interested marks made after
	// generation, which apply when the Mix is read.
	discoverWeeklyStored = 40
	// discoverWeeklySeeds are drawn from the most played tracks of the
	// discoverWeeklyHistory before the period, a different draw each week.
	discoverWeeklySeeds     = 6
	discoverWeeklySeedPool  = 25
	discoverWeeklyHistory   = 90 * 24 * time.Hour
	discoverWeeklyArtistCap = 2
)

// Mix is a generated, regularly refreshed list of recommendations (ADR 0002).
// It is not a playlist and is never replicated: each device generates its
// own from the synced inputs with a seed shared per period (ADR 0001). A
// refresh replaces the previous Mix; none are kept.
type Mix struct {
	Kind MixKind `json:"kind"`
	// Period is the local date the Mix's period began on.
	Period    string                `json:"period"`
	Tracks    []core.ExternalResult `json:"tracks"`
	UpdatedAt int64                 `json:"updatedAt,omitempty"`
	// Available is false when the Mix could not be generated: online
	// recommendations are off, no source is configured or answered, or a
	// read failed. An earlier Mix with tracks then stays, marked Offline.
	Available  bool `json:"available"`
	Offline    bool `json:"offline,omitempty"`
	Refreshing bool `json:"refreshing"`
}

func mixKey(kind MixKind) string { return "recommend.mix." + string(kind) }

// periodStart is local midnight on the Mix's refresh weekday, at or before now.
func periodStart(kind MixKind, now time.Time) time.Time {
	weekday := time.Monday
	if kind == MixReleaseRadar {
		weekday = time.Friday
	}
	y, m, d := now.Date()
	midnight := time.Date(y, m, d, 0, 0, 0, 0, now.Location())
	back := (int(midnight.Weekday()) - int(weekday) + 7) % 7
	return midnight.AddDate(0, 0, -back)
}

// periodSeed is shared by every device generating the Mix for one period.
func periodSeed(kind MixKind, start time.Time) string {
	return string(kind) + ":" + start.Format(periodLayout)
}

// Mix returns a Mix as last generated on this device. When its period has
// passed, as when the device was asleep at the refresh time, a regeneration
// starts in the background and Refreshing is set; the previous Mix shows only
// until it lands. While Not interested marks cannot be read, the Mix is
// unavailable rather than shown without them.
func (s *Service) Mix(ctx context.Context, kind MixKind) Mix {
	ctx, ok := s.withMarks(ctx)
	if !ok {
		return Mix{Kind: kind, Tracks: []core.ExternalResult{}}
	}
	var m Mix
	if !s.load(ctx, mixKey(kind), &m) {
		m = Mix{Kind: kind}
	}
	if m.Period != periodStart(kind, s.now()).Format(periodLayout) {
		s.startRefresh(ctx, mixKey(kind), func(ctx context.Context) { s.RefreshMix(ctx, kind) })
	}
	m.Refreshing = s.isRefreshing(mixKey(kind))
	m.Tracks = firstN(s.withoutDisconnectedPersonal(ctx, s.withoutMarkedTracks(ctx, m.Tracks)), mixSize)
	return m
}

// withoutDisconnectedPersonal drops tracks a personal source recommended once
// no personal account is connected, as when a Mix kept offline outlives the
// account's link.
func (s *Service) withoutDisconnectedPersonal(ctx context.Context, tracks []core.ExternalResult) []core.ExternalResult {
	for _, src := range s.personal {
		if src.Connected(ctx) {
			return tracks
		}
	}
	out := make([]core.ExternalResult, 0, len(tracks))
	for _, t := range tracks {
		if t.Reason == nil || t.Reason.Kind != core.ReasonPersonal {
			out = append(out, t)
		}
	}
	return out
}

// RefreshMix generates a Mix for the current period and replaces the stored
// one. When it cannot be generated (see Mix.Available), the last Mix stays,
// marked offline, if it has tracks; otherwise nothing is stored for the
// period. Either way the old period makes the next check try again.
func (s *Service) RefreshMix(ctx context.Context, kind MixKind) Mix {
	start := periodStart(kind, s.now())
	var m Mix
	switch kind {
	case MixDiscoverWeekly:
		m = s.discoverWeekly(ctx, start)
	case MixReleaseRadar:
		m = s.releaseRadar(ctx, start)
	default:
		return Mix{Kind: kind, Tracks: []core.ExternalResult{}}
	}
	m.Kind, m.Period, m.UpdatedAt = kind, start.Format(periodLayout), s.now().Unix()
	if m.Tracks == nil {
		m.Tracks = []core.ExternalResult{}
	}
	if !m.Available {
		var prev Mix
		if s.load(ctx, mixKey(kind), &prev) && len(prev.Tracks) > 0 {
			prev.Offline, prev.Refreshing = true, false
			s.save(ctx, mixKey(kind), prev)
			return prev
		}
		m.Period = ""
		m.Offline = true
	}
	s.save(ctx, mixKey(kind), m)
	return m
}

// RunSchedule keeps Mixes and shelves current until ctx ends. It checks at
// once, so a device that was asleep at a refresh time regenerates on launch,
// and then every scheduleTick, which also notices a wake from sleep.
func (s *Service) RunSchedule(ctx context.Context) {
	t := time.NewTicker(scheduleTick)
	defer t.Stop()
	for {
		for _, kind := range MixKinds {
			s.Mix(ctx, kind)
		}
		s.Shelves(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// discoverWeekly finds tracks new to the library: neither owned nor played
// before the period. Its seeds, recent-play exclusions and taste profile read
// only plays, playlist tracks and marks dated before the period began, so a
// device generating it late in the week, after more plays, makes the same
// Mix. Not interested marks made since apply when the Mix is read, which is
// why a few spare tracks are kept. Ownership is the library as it stands.
//
// A connected personal source (ListenBrainz) adds its recommendations for the
// account as one more list. They come from the service rather than the
// synced inputs, so only devices connected to the same account agree on them.
func (s *Service) discoverWeekly(ctx context.Context, start time.Time) Mix {
	m := Mix{Tracks: []core.ExternalResult{}}
	if !s.settings(ctx).Online || len(s.tracks)+len(s.personal) == 0 || s.listening == nil {
		return m
	}
	// Any failed read leaves the Mix unavailable, so it is retried rather than
	// stored for the week without its taste, seeds or exclusions.
	ctx, ok := s.withMarks(ctx)
	if !ok {
		return m
	}
	profile, err := s.profileBefore(ctx, start)
	if err != nil {
		log.Printf("recommend: reading Discover Weekly's taste profile: %v", err)
		return m
	}
	seeds, err := s.mixSeeds(ctx, start, periodSeed(MixDiscoverWeekly, start), profile)
	if err != nil {
		log.Printf("recommend: choosing Discover Weekly seeds: %v", err)
		return m
	}
	personal := s.personalLists(ctx)
	if len(seeds)+len(personal) == 0 {
		m.Available = true
		return m
	}
	recent, err := s.playedBetween(ctx, start.Add(-recentPlayWindow), start)
	if err != nil {
		log.Printf("recommend: reading plays Discover Weekly excludes: %v", err)
		return m
	}
	tracks, available := s.fromSeedsWith(ctx, seeds, seeds, discoverySurface, recent, profile, personal)
	m.Available = available
	fresh := make([]core.ExternalResult, 0, len(tracks))
	for _, t := range tracks {
		if !known(t, profile) {
			fresh = append(fresh, t)
		}
	}
	m.Tracks = firstN(capPerArtist(fresh, discoverWeeklyArtistCap), discoverWeeklyStored)
	return m
}

// mixSeeds draws a period's seeds from the most played tracks before it
// began, or from the library when nothing has been played. A seed the
// profile rejects (a Not interested mark from before the period) is skipped.
// A failed read is returned, so the Mix is retried rather than stored empty.
func (s *Service) mixSeeds(ctx context.Context, start time.Time, seed string, profile *Profile) ([]Seed, error) {
	var pool []Seed
	top, err := s.listening.TopTracks(ctx, start.Add(-discoverWeeklyHistory), start, discoverWeeklySeedPool)
	if err != nil {
		return nil, err
	}
	for _, t := range top {
		if profile.TrackTaste(t.Title, t.Artist) >= 0 && profile.ArtistTaste(t.Artist) >= 0 {
			pool = append(pool, Seed{Artist: t.Artist, Title: t.Title})
		}
	}
	if len(pool) == 0 {
		lib, err := s.listening.LibraryArtists(ctx, discoverWeeklySeeds*2)
		if err != nil {
			return nil, err
		}
		for _, artist := range unmarkedArtists(nil, lib, discoverWeeklySeeds*2) {
			if profile.ArtistTaste(artist) < 0 {
				continue
			}
			tracks, err := s.listening.LibraryTracks(ctx, artist, 1)
			if err != nil {
				return nil, err
			}
			if len(tracks) > 0 {
				pool = append(pool, Seed{Artist: tracks[0].Artist, Title: tracks[0].Title})
			}
		}
	}
	seededOrder(pool, seed, func(sd Seed) string { return recordingKey(sd.Title, sd.Artist) })
	return firstN(pool, discoverWeeklySeeds), nil
}

// personalCandidates is how many recommendations are asked of a personal
// source; like similar tracks, some never match anything playable.
const personalCandidates = 50

// personalLists matches each connected personal source's recommendations to
// something playable. A source with no account connected, or one that fails,
// adds nothing.
func (s *Service) personalLists(ctx context.Context) []extraList {
	var out []extraList
	for _, src := range s.personal {
		sourceCtx, cancel := context.WithTimeout(ctx, s.timeout)
		cands, err := src.Recommendations(sourceCtx, personalCandidates)
		cancel()
		if err != nil {
			if !errors.Is(err, ErrNotConfigured) {
				log.Printf("recommend: personal recommendations from %s: %v", src.Name(), err)
			}
			continue
		}
		for i := range cands {
			cands[i].Sources = []string{src.Name()}
		}
		matchCtx, cancel := context.WithTimeout(ctx, s.timeout)
		tracks := s.matchCandidates(matchCtx, cands)
		cancel()
		if len(tracks) > 0 {
			out = append(out, extraList{tracks: tracks, reason: &core.RecommendationReason{Kind: core.ReasonPersonal}})
		}
	}
	return out
}

// PersonalSourceChanged regenerates Discover Weekly in the background after
// a personal account is connected or disconnected, so the Mix gains or loses
// its recommendations without waiting for next week. A refresh already
// running read the account as it was, so it runs once more.
func (s *Service) PersonalSourceChanged(ctx context.Context) {
	key := mixKey(MixDiscoverWeekly)
	s.refreshMu.Lock()
	delete(s.attempts, key)
	if s.refreshing[key] {
		s.rerun[key] = true
		s.refreshMu.Unlock()
		return
	}
	s.refreshMu.Unlock()
	s.startRefresh(ctx, key, func(ctx context.Context) { s.RefreshMix(ctx, MixDiscoverWeekly) })
}
