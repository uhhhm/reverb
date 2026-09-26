package player

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/uhhhm/reverb/internal/matching"
)

// RadioSeed names a recording, or an artist when Title is empty.
type RadioSeed struct {
	Artist string `json:"artist"`
	Title  string `json:"title,omitempty"`
	MBID   string `json:"mbid,omitempty"`
}

// RadioStart is what a Radio session starts with: tracks to play first (none
// for an artist's Radio) and one to five seeds to recommend from.
type RadioStart struct {
	Lead  []json.RawMessage `json:"lead"`
	Seeds []RadioSeed       `json:"seeds"`
}

// Progress is a sample of playback, tagged with the exact play it describes.
// Seeking samples reset the baseline without adding listening time.
type Progress struct {
	EntryID    string  `json:"entryId"`
	PlayID     int64   `json:"playId"`
	PositionMS float64 `json:"positionMs"`
	DurationMS float64 `json:"durationMs"`
	Playing    bool    `json:"playing"`
	Seeking    bool    `json:"seeking"`
}

// radioRetry is how long a failed lookup waits before Radio asks again.
const radioRetry = 10 * time.Second

// RadioFetch looks up recommendations for seeds, as playable queue tracks.
type RadioFetch func(context.Context, []RadioSeed) ([]json.RawMessage, error)

// radioTrack is what Radio reads of a queued track.
type radioTrack struct {
	RadioSeed
	DurationMS float64    `json:"durationMs"`
	Reason     *RadioSeed `json:"reason"`
}

func track(raw json.RawMessage) radioTrack {
	var t radioTrack
	_ = json.Unmarshal(raw, &t)
	return t
}

// reissue matches a bracketed or dashed qualifier naming a reissue, not a
// different version.
var reissue = regexp.MustCompile(`(?i)\s*(?:[\(\[\{][^\)\]\}]*\b(?:remaster(?:ed)?|deluxe|bonus track)\b[^\)\]\}]*[\)\]\}]|[-–—]\s+[^-–—]*\b(?:remaster(?:ed)?|deluxe)\b.*$)`)

// recording keys one recording however many sources or reissues carry it, so
// a reissue in a later batch is not queued again.
func recording(t RadioSeed) string {
	return matching.Normalize(t.Artist) + "\x1f" + matching.Normalize(reissue.ReplaceAllString(t.Title, ""))
}

type radioSession struct {
	pool                   []json.RawMessage
	pending                []RadioSeed
	seen, seeded, finished map[string]bool
	steering, skips        map[string]int
	entry                  string
	playID                 int64
	heard, last, duration  float64
	complete               bool
	// fetching is set while a background lookup is out.
	fetching bool
	// restore is how many tracks a re-rank took back from the queue; the next
	// fill lines up at least as many again, even behind the listener's own.
	restore int
	retry   time.Time
}

// StartRadio starts a fresh, unsteered session. Invalid input leaves playback intact.
func (q *Queue) StartRadio(start RadioStart) error {
	if len(start.Seeds) == 0 || len(start.Seeds) > 5 {
		return errors.New("radio needs one to five seeds")
	}
	for _, s := range start.Seeds {
		if strings.TrimSpace(s.Artist) == "" {
			return errors.New("radio seed needs an artist")
		}
	}
	if err := q.Play(start.Lead, 0, OriginListener); err != nil {
		return err
	}
	q.SetShuffle(false)
	_ = q.SetRepeat(RepeatOff)
	r := &radioSession{pending: start.Seeds, seen: map[string]bool{}, seeded: map[string]bool{}, finished: map[string]bool{}, steering: map[string]int{}, skips: map[string]int{}}
	for _, t := range start.Lead {
		r.seen[recording(track(t).RadioSeed)] = true
	}
	for _, s := range start.Seeds {
		if s.Title != "" {
			r.seeded[recording(s)] = true
		}
	}
	q.radio = r
	q.changed()
	r.observe(q)
	return nil
}

// Progress applies a playback sample to Radio's judgement of the current
// play: listening time, and a finish once heard to within 1.5s of the end
// having heard at least half. A sample for another play is ignored.
func (q *Queue) Progress(p Progress) {
	r := q.radio
	if r == nil || p.EntryID != q.Current() || p.PlayID != q.playID {
		return
	}
	r.observe(q)
	if p.DurationMS > 0 {
		r.duration = p.DurationMS
	}
	// Started over after finishing: a repeat, judged afresh from here.
	if r.complete && p.PositionMS < 1500 {
		r.complete = false
		r.heard = 0
		r.last = p.PositionMS
		r.steer(q, track(q.entries[q.index].Track), 1)
		return
	}
	delta := p.PositionMS - r.last
	if p.Playing && !p.Seeking && delta > 0 && delta < 5000 {
		r.heard += delta
	}
	r.last = p.PositionMS
	if !r.complete && r.duration > 0 && p.PositionMS >= r.duration-1500 && r.heard >= r.duration/2 {
		r.complete = true
		r.finished[recording(track(q.entries[q.index].Track).RadioSeed)] = true
		r.steer(q, track(q.entries[q.index].Track), 1)
	}
}
func (r *radioSession) observe(q *Queue) {
	if q.Current() == r.entry && q.playID == r.playID {
		return
	}
	// The same entry started over is not a move away from it: finished, it
	// is a repeat; otherwise listening goes on counting.
	if r.entry != "" && q.Current() == r.entry {
		r.playID = q.playID
		r.last = 0
		if r.complete {
			r.complete = false
			r.heard = 0
			r.steer(q, track(q.entries[q.index].Track), 1)
		}
		return
	}
	if r.entry != "" && !r.complete && r.duration > 0 && r.heard < r.duration/2 {
		for _, e := range q.entries {
			if e.ID == r.entry {
				r.steer(q, track(e.Track), -1)
				break
			}
		}
	}
	r.entry = q.Current()
	r.playID = q.playID
	r.heard = 0
	r.last = 0
	r.duration = 0
	r.complete = false
	if q.index >= 0 {
		t := track(q.entries[q.index].Track)
		r.duration = t.DurationMS
		if r.finished[recording(t.RadioSeed)] {
			r.steer(q, t, 1)
		}
	}
}
func (r *radioSession) steer(q *Queue, t radioTrack, by int) {
	key := recording(t.RadioSeed)
	artist := matching.Normalize(t.Artist)
	r.steering["artist:"+artist] += by
	r.steering["seed:"+key] += by
	if t.Reason != nil && t.Reason.Title != "" {
		r.steering["seed:"+recording(*t.Reason)] += by
	}
	if by < 0 {
		r.seeded[key] = true
		r.skips[artist]++
	}
	var remove []int
	var taken []json.RawMessage
	for _, i := range q.upcoming(MaxEntries) {
		if q.entries[i].Origin == OriginRadio {
			taken = append(taken, q.entries[i].Track)
			remove = append(remove, i)
		}
	}
	q.Remove(remove)
	r.pool = append(taken, r.pool...)
	r.restore += len(taken)
	r.sort()
}
func (r *radioSession) sort() {
	kept := r.pool[:0]
	for _, raw := range r.pool {
		if r.skips[matching.Normalize(track(raw).Artist)] < 2 {
			kept = append(kept, raw)
		}
	}
	r.pool = kept
	score := func(raw json.RawMessage) int {
		t := track(raw)
		v := r.steering["artist:"+matching.Normalize(t.Artist)]
		if t.Reason != nil {
			v += r.steering["seed:"+recording(*t.Reason)]
		}
		return v
	}
	sort.SliceStable(r.pool, func(i, j int) bool { return score(r.pool[i]) > score(r.pool[j]) })
}
func (r *radioSession) fill(q *Queue) {
	defer func() { r.restore = 0 }()
	for (len(q.upcoming(3)) < 3 || r.restore > 0) && len(r.pool) > 0 && len(q.entries) < MaxEntries {
		lined := []int{}
		if q.index >= 0 {
			lined = append(lined, q.index)
		}
		lined = append(lined, q.upcoming(MaxEntries)...)
		blocked := ""
		if len(lined) >= 2 {
			a := matching.Normalize(track(q.entries[lined[len(lined)-1]].Track).Artist)
			b := matching.Normalize(track(q.entries[lined[len(lined)-2]].Track).Artist)
			if a == b {
				blocked = a
			}
		}
		pick := -1
		for i, t := range r.pool {
			if matching.Normalize(track(t).Artist) != blocked {
				pick = i
				break
			}
		}
		if pick < 0 {
			return
		}
		raw := r.pool[pick]
		if err := q.Enqueue([]json.RawMessage{raw}, OriginRadio); err != nil {
			return
		}
		r.pool = append(r.pool[:pick], r.pool[pick+1:]...)
		r.restore--
	}
}
func (r *radioSession) seeds(q *Queue) []RadioSeed {
	if len(r.pending) > 0 {
		out := r.pending
		r.pending = nil
		return out
	}
	var out []RadioSeed
	for i := len(q.entries) - 1; i >= 0 && len(out) < 3; i-- {
		t := track(q.entries[i].Track)
		k := recording(t.RadioSeed)
		if !r.seeded[k] && r.skips[matching.Normalize(t.Artist)] < 2 {
			r.seeded[k] = true
			out = append(out, t.RadioSeed)
		}
	}
	return out
}

// radioNeeds lines up what the pool holds and reports the seeds to look up
// next, if Radio needs more. With nothing left to look up or play, Radio ends.
func (q *Queue) radioNeeds() ([]RadioSeed, bool) {
	r := q.radio
	if r == nil {
		return nil, false
	}
	r.observe(q)
	r.fill(q)
	if r.fetching || len(q.upcoming(3)) >= 3 || time.Now().Before(r.retry) {
		return nil, false
	}
	seeds := r.seeds(q)
	if len(seeds) == 0 {
		if len(r.pool) == 0 {
			q.RadioEnded()
		}
		return nil, false
	}
	return seeds, true
}

// radioFetched adds a lookup's answer to the pool, never a recording twice.
// A failed lookup keeps its seeds for a retry after radioRetry.
func (q *Queue) radioFetched(r *radioSession, seeds []RadioSeed, rows []json.RawMessage, err error) {
	if err != nil {
		r.pending = seeds
		r.retry = time.Now().Add(radioRetry)
		return
	}
	for _, raw := range rows {
		k := recording(track(raw).RadioSeed)
		if !r.seen[k] {
			r.seen[k] = true
			r.pool = append(r.pool, raw)
		}
	}
	r.sort()
	r.fill(q)
	r.observe(q)
}
