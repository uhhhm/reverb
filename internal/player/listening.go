package player

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"

	"github.com/uhhhm/reverb/internal/core"
)

// Listen is the core's judgement of one play, from the samples its player
// reported. A play is judged once, and that verdict drives both what is
// recorded and how Radio steers:
//
//   - qualified: over 30s long, with half of it or four minutes heard;
//   - completed: reached the end having heard at least half of it;
//   - skipped: left without being either.
//
// An ordinary play is recorded once it qualifies; a recommendation is recorded
// once as an attempt when it completes or is left, so a short skip stays
// measurable.
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
	// Completed is whether the play reached its end having heard at least half
	// of it, so a seek to the end is not a completion. An ordinary play is
	// recorded as it qualifies, so it is completed only if that is at its end.
	Completed bool
	// Origin is the recommendation surface that supplied the track; empty for
	// ordinary playback.
	Origin string
	// Run groups the consecutive plays of one listening run: it ends when the
	// queue empties or plays out.
	Run string
	// Qualified is whether the play counts as a listen.
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
	// recorded or completed listen is a fresh listen of the same play.
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

// judgement is a turn in the judging of a play that Radio steers by.
type judgement struct {
	kind judgementKind
	// track is the queued track as the queue held it.
	track json.RawMessage
	// radio is the Radio session the play began in; nil if none. A judgement
	// steers only that session: one started or ended since has no claim on it.
	radio *radioSession
}

type judgementKind int

const (
	// playBegan is a play starting, or starting over.
	playBegan judgementKind = iota
	// playCompleted is a play reaching its end having heard at least half.
	playCompleted
	// playSkipped is a play left without qualifying or completing.
	playSkipped
)

// listening follows the current play of a queue.
type listening struct {
	entry     string // "" with nothing playing
	playID    int64
	raw       json.RawMessage
	track     listenTrack
	origin    string
	radio     *radioSession
	heard     float64
	last      float64
	duration  float64
	recorded  bool
	completed bool
	run       string
	out       []Listen
	// pending holds the judgements Radio has yet to steer by.
	pending []judgement
}

// settle follows the queue onto its current play, ending the one it leaves.
func (l *listening) settle(q *Queue) {
	playing := q.index >= 0 && !q.finished
	if playing && l.entry == q.Current() && l.playID == q.playID {
		return
	}
	if playing && l.entry == q.Current() && !l.qualified() && !l.completed {
		// Restarting an unfinished attempt does not leave it. Keep its heard
		// time, as a seek back does, rather than recording a skip for Stats
		// while telling Radio that nothing was skipped.
		l.playID = q.playID
		l.last = 0
		return
	}
	if l.entry != "" {
		// The same entry starting over is a replay, not a move away from it.
		l.leave(playing && l.entry == q.Current())
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
		origin = string(core.RecommendationRadio)
	}
	if l.run == "" {
		l.run = newRun()
	}
	*l = listening{entry: e.ID, playID: q.playID, raw: e.Track, track: t, origin: origin, radio: q.radio,
		duration: t.DurationMS, run: l.run, out: l.out, pending: l.pending}
	l.judge(playBegan)
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
		// Back to the start after a judged play: this play is heard afresh.
		if (l.recorded || l.completed) && p.PositionMS <= listenRestartWithinMS {
			l.recorded, l.completed = false, false
			l.heard = 0
			l.judge(playBegan)
		}
		return
	}
	if p.Playing && !p.Seeking && delta < listenMaxStepMS {
		l.heard += delta
	}
	if l.duration <= 0 {
		return
	}
	if !l.completed && p.PositionMS >= l.duration-listenEndWithinMS && l.heard >= l.duration/2 {
		l.completed = true
		l.judge(playCompleted)
		if l.origin != "" && !l.recorded {
			l.record()
		}
	}
	if l.origin == "" && !l.recorded && l.qualified() {
		l.record()
	}
}

// leave ends the current play: a recommendation not yet recorded is recorded as
// the attempt it was, and a play that neither qualified nor completed was
// skipped, unless it was left by starting it over.
func (l *listening) leave(replayed bool) {
	if l.origin != "" && !l.recorded {
		l.record()
	}
	if !replayed && !l.qualified() && !l.completed {
		l.judge(playSkipped)
	}
}

func (l *listening) qualified() bool {
	return l.duration > listenMinDurationMS && (l.heard >= l.duration/2 || l.heard >= listenThresholdMS)
}

func (l *listening) judge(kind judgementKind) {
	l.pending = append(l.pending, judgement{kind: kind, track: l.raw, radio: l.radio})
}

func (l *listening) record() {
	l.recorded = true
	l.out = append(l.out, Listen{
		TrackID:    l.track.ID,
		Title:      l.track.Title,
		Artist:     l.track.Artist,
		Album:      l.track.Album,
		ISRC:       l.track.ISRC,
		DurationMS: int(l.duration),
		MsPlayed:   int(l.heard),
		Completed:  l.completed,
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
