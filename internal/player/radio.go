package player

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/uhhhm/reverb/internal/matching"
	"github.com/uhhhm/reverb/internal/recommend"
)

// RadioStart is what a Radio session starts with: tracks to play first (none
// for an artist's Radio) and one to five seeds to recommend from.
type RadioStart struct {
	Lead  []json.RawMessage `json:"lead"`
	Seeds []recommend.Seed  `json:"seeds"`
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
type RadioFetch func(context.Context, []recommend.Seed) ([]json.RawMessage, error)

// radioTrack is what Radio reads of a queued track.
type radioTrack struct {
	recommend.Seed
	DurationMS float64         `json:"durationMs"`
	Reason     *recommend.Seed `json:"reason"`
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
func recording(t recommend.Seed) string {
	return matching.Normalize(t.Artist) + "\x1f" + matching.Normalize(reissue.ReplaceAllString(t.Title, ""))
}

type radioSession struct {
	pool    []json.RawMessage
	pending []recommend.Seed
	// seen, seeded and finished are recordings: fetched already, used as a seed,
	// and judged completed this session.
	seen, seeded, finished map[string]bool
	steering, skips        map[string]int
	// fetching is set while a background lookup is out.
	fetching bool
	// restore is how many tracks a re-rank took back from the queue; the next
	// fill lines up at least as many again, even behind the listener's own.
	restore int
	retry   time.Time
}

// StartRadio starts a fresh, unsteered session. Invalid input leaves playback intact.
func (q *Queue) StartRadio(start RadioStart) error {
	if len(start.Seeds) == 0 || len(start.Seeds) > recommend.RadioSeedLimit {
		return fmt.Errorf("radio needs one to %d seeds", recommend.RadioSeedLimit)
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
		r.seen[recording(track(t).Seed)] = true
	}
	for _, s := range start.Seeds {
		if s.Title != "" {
			r.seeded[recording(s)] = true
		}
	}
	q.radio = r
	q.changed()
	return nil
}

// Progress applies a playback sample of the current play. The core judges what
// was listened to from it (see listening.go); Radio steers by that judgement.
// A sample for another play is ignored.
func (q *Queue) Progress(p Progress) {
	q.listen.sample(q, p)
	q.steerByJudgement()
}

// settle follows the queue onto its current play, ending the one it leaves, and
// has Radio steer by what was judged.
func (q *Queue) settle() {
	q.listen.settle(q)
	q.steerByJudgement()
}

// steerByJudgement hands the judgements made so far to the Radio session each
// was made in, if that session is still the one running.
func (q *Queue) steerByJudgement() {
	pending := q.listen.pending
	q.listen.pending = nil
	for _, j := range pending {
		if j.radio != nil && j.radio == q.radio {
			q.radio.steerBy(q, j)
		}
	}
}

// steerBy steers by one turn in a play's judgement: finishing a track, or
// starting one already finished, pulls its artist and the tracks recommended
// from or alongside it up; skipping pushes them down.
func (r *radioSession) steerBy(q *Queue, j judgement) {
	t := track(j.track)
	key := recording(t.Seed)
	switch j.kind {
	case playBegan:
		if r.finished[key] {
			r.steer(q, t, 1)
		}
	case playCompleted:
		r.finished[key] = true
		r.steer(q, t, 1)
	case playSkipped:
		r.steer(q, t, -1)
	}
}
func (r *radioSession) steer(q *Queue, t radioTrack, by int) {
	key := recording(t.Seed)
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
func (r *radioSession) seeds(q *Queue) []recommend.Seed {
	if len(r.pending) > 0 {
		out := r.pending
		r.pending = nil
		return out
	}
	var out []recommend.Seed
	for i := len(q.entries) - 1; i >= 0 && len(out) < 3; i-- {
		t := track(q.entries[i].Track)
		k := recording(t.Seed)
		if !r.seeded[k] && r.skips[matching.Normalize(t.Artist)] < 2 {
			r.seeded[k] = true
			out = append(out, t.Seed)
		}
	}
	return out
}

// radioNeeds lines up what the pool holds and reports the seeds to look up
// next, if Radio needs more. With nothing left to look up or play, Radio ends.
func (q *Queue) radioNeeds() ([]recommend.Seed, bool) {
	r := q.radio
	if r == nil {
		return nil, false
	}
	q.settle()
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
func (q *Queue) radioFetched(r *radioSession, seeds []recommend.Seed, rows []json.RawMessage, err error) {
	if err != nil {
		r.pending = seeds
		r.retry = time.Now().Add(radioRetry)
		return
	}
	for _, raw := range rows {
		k := recording(track(raw).Seed)
		if !r.seen[k] {
			r.seen[k] = true
			r.pool = append(r.pool, raw)
		}
	}
	r.sort()
	r.fill(q)
}
