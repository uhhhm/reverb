package recommend

import (
	"context"
	"log"
	"maps"
	"math"
	"slices"
	"sync"
	"time"

	"github.com/uhhhm/reverb/internal/matching"
)

// TastePlay is one play in the household's history. Seq is the order this
// device stored plays in, so the profile can fold in only what is new; it
// never affects the result, which depends only on the plays themselves.
//
// Only qualified plays are returned to this model (half the track, or four
// minutes), so a short recommendation skip can be retained for quality stats
// without becoming a negative taste signal.
type TastePlay struct {
	Seq       int64
	Artist    string
	Title     string
	PlayedAt  int64 // unix seconds
	Completed bool
}

// SignalKind is what a non-play taste input records.
type SignalKind int

const (
	// SignalPlaylist is a track in one of the household's playlists.
	SignalPlaylist SignalKind = iota
	// SignalNotInterested is a Not interested mark: on a track when Title is
	// set, otherwise on the artist.
	SignalNotInterested
)

// TasteSignal is a taste input other than a play.
type TasteSignal struct {
	Kind   SignalKind
	Artist string
	Title  string
	At     int64 // unix seconds
}

// TasteInputs reads the replicated inputs the taste profile is derived from.
// Devices holding the same inputs build the same profile (ADR 0001).
type TasteInputs interface {
	// PlaysAfter returns up to limit plays with Seq greater than after, in
	// Seq order.
	PlaysAfter(ctx context.Context, after int64, limit int) ([]TastePlay, error)
	// PlayCount counts every stored play, so a removed play is noticed.
	PlayCount(ctx context.Context) (int64, error)
	// Signals returns every playlist track and Not interested mark. They are
	// few and can be undone, so they are read whole on every build.
	Signals(ctx context.Context) ([]TasteSignal, error)
}

// WithTaste ranks recommendations by the household's taste profile. Without
// it, candidates keep source order and agreement.
func WithTaste(in TasteInputs) Option { return func(s *Service) { s.taste = in } }

// Input weights, in quarters so they sum exactly as integers. A positive
// weight pulls towards a track or artist, a negative one pushes away. Each
// day's inputs decay with their age (tasteHalfLife), measured from the day of
// the newest input rather than the clock, so the profile is a function of the
// data alone.
const (
	completedTrack  = 4
	completedArtist = 4
	// An unfinished play counts against the track and says nothing either
	// way about the artist.
	unfinishedTrack  = -2
	unfinishedArtist = 0
	playlistTrack    = 8
	playlistArtist   = 4
	// Not interested is the one explicit rejection, so it outweighs a play.
	markedTrack       = -12
	markedTrackArtist = -8
	markedArtist      = -12
	quartersPerWeight = 4

	tasteHalfLife = 90 * 24 * time.Hour
	secondsPerDay = 24 * 60 * 60

	tasteBatch = 5000
)

var dayDecay = math.Ln2 / (tasteHalfLife.Hours() / 24)

// bucket is one track's or artist's inputs on one day.
type bucket struct {
	key string
	day int64
}

// tally sums inputs per track and artist per day, as integer quarter-weights.
// Integer sums do not depend on the order inputs arrive in, so devices that
// stored the same inputs in a different order hold identical tallies, and
// plays can be folded in incrementally.
type tally struct {
	weights map[bucket]int64
	// days lists, per key, the days it has inputs on, in arrival order. A
	// cloned tally shares these slices: appends never touch what an older
	// copy can see.
	days   map[string][]int64
	plays  map[string]int
	latest int64
}

func newTally() tally {
	return tally{weights: map[bucket]int64{}, days: map[string][]int64{}, plays: map[string]int{}}
}

// clone copies the tally, so a profile handed out keeps its own view while
// the next build adds to a copy.
func (t tally) clone() tally {
	return tally{weights: maps.Clone(t.weights), days: maps.Clone(t.days), plays: maps.Clone(t.plays), latest: t.latest}
}

func trackKey(title, artist string) string { return "t\x1f" + recordingKey(title, artist) }

func artistKey(artist string) string {
	return "a\x1f" + matching.Normalize(matching.PrimaryArtist(artist))
}

func (t *tally) addKey(key string, at int64, quarters int64, played bool) {
	b := bucket{key, floorDiv(at, secondsPerDay)}
	if _, ok := t.weights[b]; !ok {
		t.days[key] = append(t.days[key], b.day)
	}
	t.weights[b] += quarters
	if played {
		t.plays[key]++
	}
}

func (t *tally) add(artist, title string, at int64, trackQ, artistQ int64, played bool) {
	if title != "" {
		t.addKey(trackKey(title, artist), at, trackQ, played)
	}
	t.addKey(artistKey(artist), at, artistQ, played)
	t.latest = max(t.latest, at)
}

func (t *tally) addPlay(p TastePlay) {
	if p.Completed {
		t.add(p.Artist, p.Title, p.PlayedAt, completedTrack, completedArtist, true)
	} else {
		t.add(p.Artist, p.Title, p.PlayedAt, unfinishedTrack, unfinishedArtist, true)
	}
}

func (t *tally) addSignal(sg TasteSignal) {
	switch {
	case sg.Kind == SignalPlaylist:
		t.add(sg.Artist, sg.Title, sg.At, playlistTrack, playlistArtist, false)
	case sg.Title != "":
		t.add(sg.Artist, sg.Title, sg.At, markedTrack, markedTrackArtist, false)
	default:
		t.add(sg.Artist, "", sg.At, 0, markedArtist, false)
	}
}

// weight is a key's decayed weight as of today. Days are summed in date
// order, so the float result is the same on every device.
func (t tally) weight(key string, today int64) float64 {
	days := slices.Clone(t.days[key])
	slices.Sort(days)
	w := 0.0
	for _, d := range days {
		w += float64(t.weights[bucket{key, d}]) / quartersPerWeight * math.Exp(-dayDecay*float64(today-d))
	}
	return w
}

func floorDiv(a, b int64) int64 {
	q := a / b
	if a%b != 0 && (a < 0) != (b < 0) {
		q--
	}
	return q
}

// Profile is the household's taste profile: how much it likes each track and
// artist it has played, collected or rejected. A nil Profile knows nothing.
type Profile struct {
	plays, signals tally
	// today is the day of the newest input, which ages are measured from.
	today int64
}

// TrackTaste is the household's affinity for a recording, from -1 to 1.
func (p *Profile) TrackTaste(title, artist string) float64 {
	return p.taste(trackKey(title, artist))
}

// ArtistTaste is the household's affinity for an artist, from -1 to 1.
func (p *Profile) ArtistTaste(artist string) float64 {
	return p.taste(artistKey(artist))
}

func (p *Profile) taste(key string) float64 {
	if p == nil {
		return 0
	}
	w := p.plays.weight(key, p.today) + p.signals.weight(key, p.today)
	return w / (1 + math.Abs(w))
}

// Played reports whether the household has played a recording.
func (p *Profile) Played(title, artist string) bool {
	return p != nil && p.plays.plays[trackKey(title, artist)] > 0
}

// PlayedArtist reports whether the household has played an artist.
func (p *Profile) PlayedArtist(artist string) bool {
	return p != nil && p.plays.plays[artistKey(artist)] > 0
}

// tasteState is the part of the profile kept between requests: plays are
// only ever added, so each build reads just the plays stored since the last.
type tasteState struct {
	mu    sync.Mutex
	plays tally
	seq   int64
	count int64
}

// profile returns the current taste profile, or nil when there is none. A
// failed read ranks without taste rather than failing the surface.
func (s *Service) profile(ctx context.Context) *Profile {
	if s.taste == nil {
		return nil
	}
	st := &s.tasteState
	st.mu.Lock()
	defer st.mu.Unlock()
	if err := s.foldPlays(ctx); err != nil {
		log.Printf("recommend: reading plays for the taste profile: %v", err)
		return nil
	}
	signals, err := s.taste.Signals(ctx)
	if err != nil {
		log.Printf("recommend: reading taste signals: %v", err)
		return nil
	}
	p := &Profile{plays: st.plays, signals: newTally()}
	for _, sg := range signals {
		p.signals.addSignal(sg)
	}
	p.today = floorDiv(max(p.plays.latest, p.signals.latest), secondsPerDay)
	return p
}

// foldPlays adds plays stored since the last build. When the stored count
// falls below what was folded in, a play was removed, and the tally is
// rebuilt from the start.
func (s *Service) foldPlays(ctx context.Context) error {
	st := &s.tasteState
	if st.plays.weights == nil {
		st.plays = newTally()
	}
	for rebuilt := false; ; rebuilt = true {
		copied := false
		for {
			plays, err := s.taste.PlaysAfter(ctx, st.seq, tasteBatch)
			if err != nil {
				return err
			}
			if len(plays) > 0 && !copied {
				st.plays, copied = st.plays.clone(), true
			}
			for _, p := range plays {
				st.plays.addPlay(p)
				st.seq = p.Seq
			}
			st.count += int64(len(plays))
			if len(plays) < tasteBatch {
				break
			}
		}
		total, err := s.taste.PlayCount(ctx)
		if err != nil {
			return err
		}
		// More plays than folded means one arrived between the two reads; the
		// next build picks it up.
		if total >= st.count || rebuilt {
			return nil
		}
		st.plays, st.seq, st.count = newTally(), 0, 0
	}
}
