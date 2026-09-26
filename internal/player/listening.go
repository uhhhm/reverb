package player

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
)

// Listen is the core's judgement of one play, from the samples its player
// reported. An ordinary play is recorded once it qualifies; a recommendation
// is recorded once as an attempt when it ends, qualified or not, so a short
// skip stays measurable.
type Listen struct {
	// Session is the player session that played it.
	Session    string
	TrackID    string
	Title      string
	Artist     string
	Album      string
	ISRC       string
	DurationMS int
	MsPlayed   int
	// Completed is whether playback was within 1.5s of the end when the
	// listen was recorded.
	Completed bool
	// Origin is the recommendation surface that supplied the track; empty for
	// ordinary playback.
	Origin string
	// Run groups the consecutive plays of one listening run: it ends when the
	// queue empties or plays out.
	Run string
	// Qualified is whether the play counts as a listen: over 30s long, and
	// half of it or four minutes heard.
	Qualified bool
}

const (
	listenMinDurationMS = 30_000
	listenThresholdMS   = 240_000
	// listenEndWithinMS is how near the end counts as the end.
	listenEndWithinMS = 1_500
	// listenMaxStepMS bounds one sample's advance: a larger jump is a seek,
	// whose span was never heard.
	listenMaxStepMS = 5_000
	// listenRestartWithinMS is how near the start a seek back after a
	// recorded listen is a fresh listen of the same play.
	listenRestartWithinMS = 3_000
)

// listenTrack is what listening reads of a queued track.
type listenTrack struct {
	ID                   string  `json:"id"`
	Title                string  `json:"title"`
	Artist               string  `json:"artist"`
	Album                string  `json:"album"`
	ISRC                 string  `json:"isrc"`
	DurationMS           float64 `json:"durationMs"`
	RecommendationOrigin string  `json:"recommendationOrigin"`
}

// listening follows the current play of a queue.
type listening struct {
	entry    string // "" with nothing playing
	playID   int64
	track    listenTrack
	origin   string
	heard    float64
	last     float64
	duration float64
	recorded bool
	run      string
	out      []Listen
}

// settle follows the queue onto its current play, ending the one it leaves.
func (l *listening) settle(q *Queue) {
	playing := q.index >= 0 && !q.finished
	if playing && l.entry == q.Current() && l.playID == q.playID {
		return
	}
	if l.entry != "" && l.origin != "" && !l.recorded {
		l.record(l.duration > 0 && l.last >= l.duration-listenEndWithinMS)
	}
	l.entry = ""
	if !playing {
		l.run = ""
		return
	}
	e := q.entries[q.index]
	var t listenTrack
	_ = json.Unmarshal(e.Track, &t)
	origin := t.RecommendationOrigin
	if origin == "" && e.Origin == OriginRadio {
		origin = "radio"
	}
	if l.run == "" {
		l.run = newRun()
	}
	*l = listening{entry: e.ID, playID: q.playID, track: t, origin: origin, duration: t.DurationMS, run: l.run, out: l.out}
}

// sample applies one playback sample of the current play.
func (l *listening) sample(q *Queue, p Progress) {
	l.settle(q)
	if l.entry == "" || p.EntryID != l.entry || p.PlayID != l.playID {
		return
	}
	if p.DurationMS > 0 {
		l.duration = p.DurationMS
	}
	delta := p.PositionMS - l.last
	l.last = p.PositionMS
	if delta < 0 {
		// Back to the start after a recorded listen: this play is heard afresh.
		if l.recorded && p.PositionMS <= listenRestartWithinMS {
			l.recorded = false
			l.heard = 0
		}
		return
	}
	if p.Playing && !p.Seeking && delta < listenMaxStepMS {
		l.heard += delta
	}
	if l.recorded || l.duration <= 0 {
		return
	}
	atEnd := p.PositionMS >= l.duration-listenEndWithinMS
	switch {
	case l.origin != "" && atEnd:
		l.record(true)
	case l.origin == "" && l.qualified():
		l.record(atEnd)
	}
}

func (l *listening) qualified() bool {
	return l.duration > listenMinDurationMS && (l.heard >= l.duration/2 || l.heard >= listenThresholdMS)
}

func (l *listening) record(completed bool) {
	l.recorded = true
	l.out = append(l.out, Listen{
		TrackID:    l.track.ID,
		Title:      l.track.Title,
		Artist:     l.track.Artist,
		Album:      l.track.Album,
		ISRC:       l.track.ISRC,
		DurationMS: int(l.duration),
		MsPlayed:   int(l.heard),
		Completed:  completed,
		Origin:     l.origin,
		Run:        l.run,
		Qualified:  l.qualified(),
	})
}

// take returns and forgets the listens recorded so far.
func (l *listening) take() []Listen {
	out := l.out
	l.out = nil
	return out
}

func newRun() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
