package recommend

import (
	"context"
	"log"
	"maps"
	"math"
	"sync"
	"time"

	"github.com/uhhhm/reverb/internal/matching"
)

// TastePlay is one play in the household's history. Seq is the order this
// device stored plays in, so the profile can fold in only what is new; it
// never affects the result, which depends only on the plays themselves.
//
// Only plays that qualified are stored (half the track, or four minutes), so
// the skip signal available here is a play that was not completed.
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

// Input weights. A positive weight pulls towards a track or artist, a
// negative one pushes away. Each input decays with its age (tasteHalfLife),
// measured from the newest input rather than the clock, so the profile is a
// function of the data alone.
const (
	completedTrack  = 1.0
	completedArtist = 1.0
	// A play left after half the track is the skip signal that is stored:
	// the track counts against itself, and says nothing either way about the
	// artist.
	partialTrack   = -0.5
	partialArtist  = 0.0
	playlistTrack  = 2.0
	playlistArtist = 1.0
	// Not interested is the one explicit rejection, so it outweighs a play.
	markedTrack       = -3.0
	markedTrackArtist = -2.0
	markedArtist      = -3.0

	tasteHalfLife = 90 * 24 * time.Hour
	// tasteEpoch keeps the stored exponentials in range: weights are summed
	// as w·e^(λ·(t−epoch)) and scaled back to the newest input when read.
	tasteEpoch = 1_577_836_800 // 2020-01-01 UTC

	tasteBatch = 5000
)

var tasteDecay = math.Ln2 / tasteHalfLife.Seconds()

type affinity struct {
	weight float64
	plays  int
}

// tally sums weighted, time-stamped inputs per track and per artist. Sums do
// not depend on the order inputs arrive in, which is what lets plays be
// folded in incrementally.
type tally struct {
	tracks  map[string]affinity
	artists map[string]affinity
	latest  int64
}

func newTally() tally {
	return tally{tracks: map[string]affinity{}, artists: map[string]affinity{}}
}

// clone copies the tally, so a profile handed out keeps its own maps while
// the next build adds to a copy.
func (t tally) clone() tally {
	return tally{tracks: maps.Clone(t.tracks), artists: maps.Clone(t.artists), latest: t.latest}
}

func (t *tally) add(artist, title string, at int64, trackW, artistW float64, played bool) {
	scale := math.Exp(tasteDecay * float64(at-tasteEpoch))
	n := 0
	if played {
		n = 1
	}
	if title != "" {
		key := recordingKey(title, artist)
		a := t.tracks[key]
		a.weight += trackW * scale
		a.plays += n
		t.tracks[key] = a
	}
	key := artistKey(artist)
	a := t.artists[key]
	a.weight += artistW * scale
	a.plays += n
	t.artists[key] = a
	t.latest = max(t.latest, at)
}

func (t *tally) addPlay(p TastePlay) {
	if p.Completed {
		t.add(p.Artist, p.Title, p.PlayedAt, completedTrack, completedArtist, true)
	} else {
		t.add(p.Artist, p.Title, p.PlayedAt, partialTrack, partialArtist, true)
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

func artistKey(artist string) string {
	return matching.Normalize(matching.PrimaryArtist(artist))
}

// Profile is the household's taste profile: how much it likes each track and
// artist it has played, collected or rejected. A nil Profile knows nothing.
type Profile struct {
	plays, signals tally
	// scale brings stored weights to the newest input's time.
	scale float64
}

// TrackTaste is the household's affinity for a recording, from -1 to 1.
func (p *Profile) TrackTaste(title, artist string) float64 {
	if p == nil {
		return 0
	}
	key := recordingKey(title, artist)
	return p.squash(p.plays.tracks[key].weight + p.signals.tracks[key].weight)
}

// ArtistTaste is the household's affinity for an artist, from -1 to 1.
func (p *Profile) ArtistTaste(artist string) float64 {
	if p == nil {
		return 0
	}
	key := artistKey(artist)
	return p.squash(p.plays.artists[key].weight + p.signals.artists[key].weight)
}

// Played reports whether the household has played a recording.
func (p *Profile) Played(title, artist string) bool {
	return p != nil && p.plays.tracks[recordingKey(title, artist)].plays > 0
}

// PlayedArtist reports whether the household has played an artist.
func (p *Profile) PlayedArtist(artist string) bool {
	return p != nil && p.plays.artists[artistKey(artist)].plays > 0
}

func (p *Profile) squash(w float64) float64 {
	w *= p.scale
	return w / (1 + math.Abs(w))
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
	latest := max(p.plays.latest, p.signals.latest)
	p.scale = math.Exp(-tasteDecay * float64(latest-tasteEpoch))
	return p
}

// foldPlays adds plays stored since the last build. When the stored count
// falls below what was folded in, a play was removed, and the tally is
// rebuilt from the start.
func (s *Service) foldPlays(ctx context.Context) error {
	st := &s.tasteState
	if st.plays.tracks == nil {
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
